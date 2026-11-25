#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""A stand-in for a service's sign-in page and token endpoint, to test
Driveway's browser sign-in without a real account. /authorize sends the
browser straight back to the app with a code (as if the person signed in);
/token trades the code, or a refresh token, for a token.

  python3 fake-oauth.py 127.0.0.1 8765 [--expires SECONDS]

A sign-in that sends a PKCE challenge (Driveway's own, for Dropbox and
OneDrive) is held to a public client's rules, the way Dropbox and Microsoft
are: the code is traded only with the matching code_verifier, the same
redirect_uri and client_id, and no secret; a Basic header (oauth2's first
try, with an empty secret) is refused with invalid_client, as Dropbox does;
its refresh tokens follow the same rules. Sign-ins without a challenge
(rclone's own, with a secret) are accepted.

Other pages, for the ways a sign-in can end:
  /deny     the person declines: back with error=access_denied
  /stale    back with a code and a state from some other sign-in
  /badcode  back with a code the token endpoint never issued
  /hang     a page that never sends the browser back (cancel, time out)

And a stand-in for Microsoft Graph after a OneDrive sign-in (the engine's
graph_url setting), with the drives of a personal account as Microsoft lists
them: system drives first, then the OneDrive, then a Bundles_ store.
  /graph/me/drive, /graph/me/drives          the account's own drive is named
  /graph-nome/me/drive (404), .../me/drives   only the list

--expires sets the tokens' lifetime (default 3600); 1 makes rclone refresh
on first use (it refreshes ten seconds early). --confidential ID (Box: a
client with a secret, which Driveway sends along with PKCE) requires a
secret from that client, in the form or a Basic header, instead of refusing
one.
"""
import base64
import hashlib
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlencode, urlparse

LOCK = threading.Lock()
CODES = {}     # code -> {"challenge", "client_id", "redirect_uri"}
REFRESH = {}   # refresh token -> client_id if issued to a public client, else None
COUNT = [0]
EXPIRES = 3600
CONFIDENTIAL = set()  # client IDs that must send a secret


ME = {"id": "0123456789ABCDEF", "driveType": "personal", "name": "OneDrive"}
DRIVES = {"value": [
    {"id": "b!Ex4mpleSite0-nOtReAl0000sItEiDfOrT3st-FakesAaaaLibraryOneAaaaaaa", "driveType": "documentLibrary", "name": "5d0c1f2a"},
    {"id": "b!Ex4mpleSite0-nOtReAl0000sItEiDfOrT3st-FakeuCcccLibraryThreeCcccc", "driveType": "documentLibrary", "name": "ODCMetadataArchive"},
    ME,
    {"id": "0123456789ABCDEF!s8b", "driveType": "personal", "name": "Bundles_00112233445566778899aabbccddeeff"},
]}


def s256(verifier):
    return base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b"=").decode()


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        u = urlparse(self.path)
        q = {k: v[0] for k, v in parse_qs(u.query).items()}
        if u.path.startswith("/graph"):
            self.graph(u.path)
            return
        if u.path == "/hang":
            self.page(200, "Sign in here (this page never sends you back).")
            print("authorize: hang", flush=True)
            return
        if u.path not in ("/authorize", "/deny", "/stale", "/badcode"):
            self.send_error(404)
            return
        if "redirect_uri" not in q or "state" not in q:
            self.page(400, "missing redirect_uri or state")
            return
        pkce = "code_challenge" in q
        if pkce and (q.get("code_challenge_method") != "S256" or q.get("response_type") != "code" or not q.get("client_id")):
            self.page(400, "bad PKCE request: " + u.query)
            print("authorize: refused", u.query, flush=True)
            return
        back = {"state": q["state"]}
        if u.path == "/deny":
            back = {"error": "access_denied", "error_description": "The user denied access", "state": q["state"]}
        elif u.path == "/stale":
            back = {"code": "from-elsewhere", "state": "another-sign-in"}
        elif u.path == "/badcode":
            back["code"] = "never-issued"
        else:
            with LOCK:
                COUNT[0] += 1
                code = f"code-{COUNT[0]}"
                if pkce:
                    CODES[code] = {"challenge": q["code_challenge"], "client_id": q["client_id"], "redirect_uri": q["redirect_uri"]}
                else:
                    CODES[code] = None
            back["code"] = code
        self.send_response(302)
        self.send_header("Location", q["redirect_uri"] + "?" + urlencode(back))
        self.end_headers()
        print(f"authorize{u.path}: pkce={pkce} client={q.get('client_id', '')} scope={q.get('scope', '')!r} "
              f"extra={ {k: q[k] for k in ('token_access_type', 'prompt') if k in q} } -> {q['redirect_uri']}", flush=True)

    def graph(self, path):
        ok = self.headers.get("Authorization", "").startswith("Bearer at-")
        body, status = None, 404
        if not ok:
            status = 401
        elif path in ("/graph/me/drive",):
            body, status = ME, 200
        elif path in ("/graph/me/drives", "/graph-nome/me/drives"):
            body, status = DRIVES, 200
        print(f"graph {path}: {status}", flush=True)
        data = json.dumps(body if body is not None else {"error": {"code": "itemNotFound"}}).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        if urlparse(self.path).path != "/token":
            self.send_error(404)
            return
        body = {k: v[0] for k, v in parse_qs(self.rfile.read(int(self.headers.get("Content-Length", 0))).decode()).items()}
        grant = body.get("grant_type", "")
        auth = self.headers.get("Authorization", "")
        basic = auth.startswith("Basic ")
        secret = bool(body.get("client_secret"))
        client = body.get("client_id", "")
        if basic and not client:
            client = base64.b64decode(auth[6:]).decode().split(":", 1)[0]
        public = None  # the client ID a public-client grant was issued to
        with LOCK:
            if grant == "authorization_code":
                if body.get("code") not in CODES:
                    return self.refuse(grant, "invalid_grant", "unknown code")
                issued = CODES[body["code"]]
                if issued is not None:
                    public = issued["client_id"]
                    if public in CONFIDENTIAL and not (basic or secret):
                        return self.refuse(grant, "invalid_client", "this client must send its secret")
                    if public not in CONFIDENTIAL and (basic or secret):
                        return self.refuse(grant, "invalid_client", "a public client sent a secret" + (" (Basic)" if basic else ""))
                    if client != public or body.get("redirect_uri") != issued["redirect_uri"]:
                        return self.refuse(grant, "invalid_grant", "client_id or redirect_uri differs")
                    if s256(body.get("code_verifier", "")) != issued["challenge"]:
                        return self.refuse(grant, "invalid_grant", "code_verifier doesn't match")
                del CODES[body["code"]]
            elif grant == "refresh_token":
                if body.get("refresh_token") not in REFRESH:
                    return self.refuse(grant, "invalid_grant", "unknown refresh token")
                public = REFRESH[body["refresh_token"]]
                if public is not None:
                    if public in CONFIDENTIAL and not (basic or secret):
                        return self.refuse(grant, "invalid_client", "this client must send its secret")
                    if public not in CONFIDENTIAL and (basic or secret):
                        return self.refuse(grant, "invalid_client", "a public client sent a secret" + (" (Basic)" if basic else ""))
                    if client != public:
                        return self.refuse(grant, "invalid_grant", "client_id differs")
            else:
                return self.refuse(grant, "unsupported_grant_type", grant)
            COUNT[0] += 1
            n = COUNT[0]
            REFRESH[f"rt-{n}"] = public
        tok = {"access_token": f"at-{n}", "token_type": "bearer", "refresh_token": f"rt-{n}", "expires_in": EXPIRES}
        self.json(200, tok)
        print(f"token: {grant} client={client or '-'} public={public is not None} secret={secret or basic} -> at-{n}", flush=True)

    def refuse(self, grant, error, why):
        self.json(400 if error != "invalid_client" else 401, {"error": error, "error_description": why})
        print(f"token: {grant} refused: {error} ({why})", flush=True)

    def json(self, status, obj):
        data = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def page(self, status, text):
        data = f"<!DOCTYPE html><title>Stand-in sign-in</title><p>{text}</p>".encode()
        self.send_response(status)
        self.send_header("Content-Type", "text/html")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    if "--expires" in sys.argv:
        EXPIRES = int(sys.argv[sys.argv.index("--expires") + 1])
    for i, a in enumerate(sys.argv):
        if a == "--confidential":
            CONFIDENTIAL.add(sys.argv[i + 1])
    ThreadingHTTPServer((sys.argv[1], int(sys.argv[2])), Handler).serve_forever()

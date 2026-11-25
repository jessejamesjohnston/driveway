#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""A stand-in for Google's token endpoint and Cloud Storage's JSON API, to
test Driveway's service account setup without a Google account. /token
trades any signed service account assertion for a token; /storage/v1/
lists one made-up bucket with one file in it.

  python3 fake-gcs.py 127.0.0.1 8766 [KEYFILE]

With KEYFILE, it also writes a made-up service account key there whose
token address is http://127.0.0.1:8766/token (so, with `adb reverse tcp:8766
tcp:8766`, the emulator's Driveway reaches this stand-in). In Driveway, put
http://127.0.0.1:8766/storage/v1/ in More settings, Endpoint.
"""
import json
import subprocess
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

TOKEN = "made-up-access"


class Handler(BaseHTTPRequestHandler):
    def reply(self, code, body):
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        body = parse_qs(self.rfile.read(int(self.headers.get("Content-Length", 0))).decode())
        if urlparse(self.path).path != "/token" or not body.get("assertion"):
            return self.reply(400, {"error": "invalid_grant"})
        print("token: service account assertion -> token", flush=True)
        return self.reply(200, {"access_token": TOKEN, "token_type": "Bearer", "expires_in": 3600})

    def do_GET(self):
        u = urlparse(self.path)
        if self.headers.get("Authorization") != "Bearer " + TOKEN:
            return self.reply(401, {"error": {"code": 401, "message": "no token"}})
        if u.path == "/storage/v1/b":
            print("list buckets of", parse_qs(u.query).get("project"), flush=True)
            return self.reply(200, {"kind": "storage#buckets", "items": [{"name": "made-up-bucket"}]})
        if u.path == "/storage/v1/b/made-up-bucket/o":
            return self.reply(200, {"kind": "storage#objects", "items": [
                {"name": "hello.txt", "size": "6", "updated": "2026-10-01T00:00:00Z", "contentType": "text/plain"}]})
        return self.reply(404, {"error": {"code": 404, "message": u.path}})

    def log_message(self, *args):
        pass


if len(sys.argv) > 3:
    key = subprocess.run(["openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048"],
                         check=True, capture_output=True, text=True).stdout
    with open(sys.argv[3], "w") as f:
        json.dump({"type": "service_account", "project_id": "made-up-project", "private_key_id": "1",
                   "private_key": key, "client_email": "driveway-test@made-up-project.iam.gserviceaccount.com",
                   "client_id": "1", "token_uri": f"http://127.0.0.1:{sys.argv[2]}/token"}, f, indent=2)
ThreadingHTTPServer((sys.argv[1], int(sys.argv[2])), Handler).serve_forever()

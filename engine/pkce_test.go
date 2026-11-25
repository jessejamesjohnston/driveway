// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/lib/oauthutil"
	"golang.org/x/oauth2"
)

// RFC 7636, appendix B.
func TestPKCEChallenge(t *testing.T) {
	if got := challengeS256("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("challenge %q", got)
	}
	a, _ := newVerifier()
	b, _ := newVerifier()
	if a == b || !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(a) {
		t.Fatalf("verifiers %q %q", a, b)
	}
}

func TestAuthURL(t *testing.T) {
	for name, reg := range ownRegistration {
		u, err := url.Parse(buildAuthURL(reg, reg.AuthURL, "st", "ch"))
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		want := map[string]string{"client_id": reg.ClientID, "response_type": "code", "redirect_uri": "http://localhost:53682/",
			"state": "st", "code_challenge": "ch", "code_challenge_method": "S256"}
		for k, v := range want {
			if q.Get(k) != v {
				t.Errorf("%s: %s = %q, want %q", name, k, q.Get(k), v)
			}
		}
		if q.Has("client_secret") {
			t.Errorf("%s: a secret in the sign-in address", name)
		}
		scopes := q.Get("scope")
		if strings.Contains(scopes, "sharing") || strings.Contains(scopes, ".All") {
			t.Errorf("%s: asks for %q", name, scopes)
		}
		if u.Scheme+"://"+u.Host+u.Path != reg.AuthURL {
			t.Errorf("%s: goes to %s", name, u)
		}
	}
	d := ownRegistration["dropbox"]
	q, _ := url.ParseQuery(strings.SplitN(buildAuthURL(d, d.AuthURL, "s", "c"), "?", 2)[1])
	if q.Get("scope") != "account_info.read files.metadata.read files.metadata.write files.content.read files.content.write" ||
		q.Get("token_access_type") != "offline" {
		t.Errorf("dropbox: %v", q)
	}
	o := ownRegistration["onedrive"]
	q, _ = url.ParseQuery(strings.SplitN(buildAuthURL(o, o.AuthURL, "s", "c"), "?", 2)[1])
	if q.Get("scope") != "Files.ReadWrite offline_access User.Read" || q.Get("prompt") != "select_account" {
		t.Errorf("onedrive: %v", q)
	}
	// A base address with a query of its own keeps it.
	if u := buildAuthURL(d, "http://x/a?k=v", "s", "c"); !strings.HasPrefix(u, "http://x/a?k=v&") {
		t.Errorf("%s", u)
	}
}

// What comes back to the device: only this sign-in's answer is used.
func TestReceiverAnswers(t *testing.T) {
	cases := []struct{ query, kind, code string }{
		{"code=c1&state=good", "", "c1"},
		{"error=access_denied&error_description=The+user+denied&state=good", "declined", ""},
		{"error=invalid_scope&error_description=AADSTS70011:+bad+scope.+Trace+ID:+1+Correlation+ID:+2&state=good", "signin", ""},
	}
	for _, c := range cases {
		r := &receiver{state: "good", service: "Dropbox", result: make(chan answer, 1)}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/?"+c.query, nil))
		a := <-r.result
		switch {
		case c.code != "":
			// The code isn't traded for a token yet: the page doesn't say
			// the sign-in worked, only where it finishes.
			if a.code != c.code || a.err != nil || w.Code != 200 || !strings.Contains(w.Body.String(), "Almost done") || !strings.Contains(w.Body.String(), returnLink) ||
				!strings.Contains(w.Body.String(), "finish connecting to Dropbox") || strings.Contains(w.Body.String(), "Signed in") {
				t.Errorf("%s: %+v %d %s", c.query, a, w.Code, w.Body.String())
			}
		default:
			if a.err == nil || !strings.HasPrefix(a.err.Error(), "["+c.kind+"]") || a.code != "" ||
				!strings.Contains(w.Body.String(), "Couldn&#39;t sign in to Dropbox") {
				t.Errorf("%s: %+v, want %s", c.query, a, c.kind)
			}
			if strings.Contains(a.err.Error(), "Trace ID") {
				t.Errorf("%s: %v", c.query, a.err)
			}
		}
	}
	// Another sign-in's answer is turned away in the browser, and this one
	// keeps waiting.
	r := &receiver{state: "good", service: "Dropbox", result: make(chan answer, 1)}
	for _, q := range []string{"code=c1&state=old", "code=c1", "error=access_denied&state=old"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/?"+q, nil))
		if w.Code != 400 || !strings.Contains(w.Body.String(), "earlier sign-in") {
			t.Errorf("%s: %d %s", q, w.Code, w.Body.String())
		}
	}
	// Anything else on the port (a favicon, the bare address) is ignored.
	for _, p := range []string{"/favicon.ico", "/", "/x?code=1&state=good"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 404 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	select {
	case a := <-r.result:
		t.Errorf("answered %+v", a)
	default:
	}
}

// pkceProvider stands in for Dropbox or Microsoft with a public client:
// PKCE required, a secret refused, the client ID accepted only in the form
// (a Basic header with an empty secret is refused, as Dropbox does).
type pkceProvider struct {
	*httptest.Server
	t         *testing.T
	clientID  string
	expiresIn int
	mu        sync.Mutex
	codes     map[string]string // code -> challenge
	refresh   map[string]bool
	n         int
	log       []string
	// noRefresh issues tokens without a refresh token.
	noRefresh bool
	// secret: the client secret the token endpoint requires (Box); empty
	// for a public client, which must send none.
	secret string
	// tokenHit hears each request to /hangtoken, which never answers (until
	// the test ends: the engine's cancelled request may leave its
	// connection open).
	tokenHit chan struct{}
	release  chan struct{}
}

func newPKCEProvider(t *testing.T, clientID string) *pkceProvider {
	p := &pkceProvider{t: t, clientID: clientID, expiresIn: 3600, codes: map[string]string{}, refresh: map[string]bool{},
		tokenHit: make(chan struct{}, 1), release: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/", p.authorize)
	mux.HandleFunc("/token", p.token)
	// Token endpoints that go wrong.
	mux.HandleFunc("/hangtoken", func(w http.ResponseWriter, r *http.Request) {
		p.tokenHit <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-p.release:
		}
	})
	mux.HandleFunc("/token500", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal trouble", http.StatusInternalServerError)
	})
	mux.HandleFunc("/tokenempty", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"token_type":"bearer"}`)
	})
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	t.Cleanup(func() { close(p.release) }) // runs first
	return p
}

func (p *pkceProvider) note(s string) {
	p.mu.Lock()
	p.log = append(p.log, s)
	p.mu.Unlock()
}

func (p *pkceProvider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != p.clientID || q.Get("response_type") != "code" || q.Get("redirect_uri") != redirectURI ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		http.Error(w, "bad authorize request: "+r.URL.RawQuery, 400)
		return
	}
	back := q.Get("redirect_uri") + "?"
	switch r.URL.Path {
	case "/authorize", "/badcode":
		code := fmt.Sprintf("code-%d", time.Now().UnixNano())
		if r.URL.Path == "/badcode" {
			code = "never-issued"
		} else {
			p.mu.Lock()
			p.codes[code] = q.Get("code_challenge")
			p.mu.Unlock()
		}
		back += url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
	case "/deny":
		back += url.Values{"error": {"access_denied"}, "error_description": {"The user denied access"}, "state": {q.Get("state")}}.Encode()
	case "/stale":
		back += url.Values{"code": {"x"}, "state": {"from-another-sign-in"}}.Encode()
	default: // "/hang": the person never finishes
		fmt.Fprint(w, "sign in here")
		return
	}
	http.Redirect(w, r, back, http.StatusFound)
}

func (p *pkceProvider) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	fail := func(status int, code string) {
		p.note("refused " + r.Form.Get("grant_type") + ": " + code)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":%q}`, code)
	}
	if _, _, ok := r.BasicAuth(); ok {
		fail(400, "invalid_client") // an empty secret in a Basic header
		return
	}
	if p.secret == "" && r.Form.Has("client_secret") {
		fail(401, "invalid_client")
		return
	}
	if p.secret != "" && r.Form.Get("client_secret") != p.secret {
		fail(401, "invalid_client")
		return
	}
	if r.Form.Get("client_id") != p.clientID {
		fail(400, "invalid_client")
		return
	}
	p.mu.Lock()
	refused := ""
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		challenge, ok := p.codes[r.Form.Get("code")]
		if !ok || r.Form.Get("redirect_uri") != redirectURI || challengeS256(r.Form.Get("code_verifier")) != challenge {
			refused = "invalid_grant"
		}
		delete(p.codes, r.Form.Get("code"))
	case "refresh_token":
		if !p.refresh[r.Form.Get("refresh_token")] {
			refused = "invalid_grant"
		}
	default:
		refused = "unsupported_grant_type"
	}
	if refused != "" {
		p.mu.Unlock()
		fail(400, refused)
		return
	}
	p.n++
	n := p.n
	rt := fmt.Sprintf("rt-%d", n)
	p.refresh[rt] = true
	p.log = append(p.log, fmt.Sprintf("issued %s at-%d for %s", r.Form.Get("grant_type"), n, r.Form.Get("client_id")))
	p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if p.noRefresh {
		fmt.Fprintf(w, `{"access_token":"at-%d","token_type":"bearer","expires_in":%d}`, n, p.expiresIn)
		return
	}
	fmt.Fprintf(w, `{"access_token":"at-%d","token_type":"bearer","refresh_token":%q,"expires_in":%d}`, n, rt, p.expiresIn)
}

// browserHost plays the browser: it follows each page it is asked to open,
// redirects included (back to the engine at localhost:53682).
type browserHost struct {
	testHost
}

func (h *browserHost) OpenURL(u string) {
	h.testHost.OpenURL(u)
	if !strings.Contains(u, "/hang") {
		go func() {
			if resp, err := http.Get(u); err == nil {
				resp.Body.Close()
			}
		}()
	}
}

func ownParams(p *pkceProvider, path string, extra map[string]string) string {
	m := map[string]string{"auth_url": p.URL + path, "token_url": p.URL + "/token"}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// The whole sign-in with Merriment Labs' Dropbox registration, then a
// refresh the way rclone does it.
func TestOwnSignInDropbox(t *testing.T) {
	reg := ownRegistration["dropbox"]
	p := newPKCEProvider(t, reg.ClientID)
	p.expiresIn = 1 // expired at once (oauth2 refreshes 10 s early)
	h := &browserHost{testHost{saved: map[string]string{}}}
	SetHost(h)
	defer RemoveRemote("t-own")
	out, err := ConfigStart("t-own", "dropbox", ownParams(p, "/authorize", nil))
	if err != nil {
		t.Fatal(err)
	}
	var q question
	_ = json.Unmarshal([]byte(out), &q)
	if !q.Done {
		t.Fatalf("a question after signing in: %s", out)
	}
	if !strings.HasPrefix(h.urls[0], p.URL+"/authorize?") {
		t.Fatalf("opened %q", h.urls[0])
	}
	var vals map[string]string
	_ = json.Unmarshal([]byte(ConfigValues("t-own")), &vals)
	if vals["client_id"] != reg.ClientID {
		t.Errorf("client_id %q", vals["client_id"])
	}
	if _, ok := vals["client_secret"]; ok {
		t.Errorf("a client secret was kept")
	}
	for k := range vals {
		if strings.HasPrefix(k, "config_") {
			t.Errorf("setup answer %s kept", k)
		}
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(vals["token"]), &tok); err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at-1" || tok.RefreshToken != "rt-1" || tok.TokenType != "bearer" || tok.Expiry.IsZero() {
		t.Fatalf("token %s", vals["token"])
	}

	// rclone's refresh: its own client config (rclone's ID and secret)
	// with the server's settings over it, as the Dropbox backend does.
	m := fs.ConfigMap("", nil, "t-own", nil)
	_, ts, err := oauthutil.NewClient(context.Background(), "t-own", m,
		&oauthutil.Config{ClientID: "rclone-id", ClientSecret: "rclone-secret", TokenURL: "http://127.0.0.1:1/unused"})
	if err != nil {
		t.Fatal(err)
	}
	nt, err := ts.Token()
	if err != nil {
		t.Fatalf("refresh: %v (provider saw %v)", err, p.log)
	}
	if nt.AccessToken != "at-2" {
		t.Fatalf("refreshed to %q (provider saw %v)", nt.AccessToken, p.log)
	}
	joined := strings.Join(p.log, "\n")
	if !strings.Contains(joined, "refused refresh_token: invalid_client") ||
		!strings.Contains(joined, "issued refresh_token at-2 for "+reg.ClientID) {
		t.Errorf("provider saw:\n%s", joined)
	}
	h.mu.Lock()
	saved := h.saved["t-own/token"]
	h.mu.Unlock()
	if !strings.Contains(saved, "at-2") || !strings.Contains(saved, "rt-2") {
		t.Errorf("the app wasn't given the refreshed token: %q", saved)
	}
}

// OneDrive: the sign-in, then Driveway sets the account's own drive
// (onedrive_drive_test.go has the drive cases), so setup ends without a
// question.
func TestOwnSignInOneDrive(t *testing.T) {
	reg := ownRegistration["onedrive"]
	p := newPKCEProvider(t, reg.ClientID)
	g := newFakeGraph(t)
	g.me = meDrive(personalMe)
	h := &browserHost{testHost{saved: map[string]string{}}}
	SetHost(h)
	defer RemoveRemote("t-od")
	out, err := ConfigStart("t-od", "onedrive", ownParams(p, "/authorize", nil))
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"done":true}` {
		t.Fatalf("after signing in: %s", out)
	}
	var vals map[string]string
	_ = json.Unmarshal([]byte(ConfigValues("t-od")), &vals)
	if vals["client_id"] != reg.ClientID || !strings.Contains(vals["token"], `"refresh_token":"rt-1"`) {
		t.Fatalf("settings %v", vals)
	}
	u, _ := url.Parse(h.urls[0])
	if u.Query().Get("scope") != "Files.ReadWrite offline_access User.Read" {
		t.Errorf("scope %q", u.Query().Get("scope"))
	}
	// Without a type of its own, the sign-in answers "the person's OneDrive".
	m := map[string]any{"auth_url": p.URL + "/authorize", "token_url": p.URL + "/token"}
	if err := ownSignInSettings(reg, m); err != nil {
		t.Fatal(err)
	}
	if m["config_type"] != "onedrive" || m["config_refresh_token"] != "false" || m["client_id"] != reg.ClientID {
		t.Errorf("settings %v", m)
	}
}

// Who signs in with which registration.
func TestUsesOwnRegistration(t *testing.T) {
	for _, c := range []struct {
		backend string
		m       map[string]any
		own     bool
	}{
		{"dropbox", map[string]any{}, true},
		{"dropbox", map[string]any{"client_id": "nnd1cgs8tlnkafa"}, true},
		{"dropbox", map[string]any{"client_id": "someone-elses"}, false},
		{"onedrive", map[string]any{"region": "global"}, true},
		{"onedrive", map[string]any{"region": "us"}, false},
		{"onedrive", map[string]any{"drive_type": "documentLibrary"}, false},
		{"onedrive", map[string]any{"drive_type": "personal"}, true},
		{"box", map[string]any{}, false},
	} {
		if _, own := usesOwnRegistration(c.backend, c.m); own != c.own {
			t.Errorf("%s %v: own %v", c.backend, c.m, own)
		}
	}
}

// Declined, a stale answer, a refused code, a cancel and a timeout all end
// the sign-in with their own kind of error, and free the port at once.
func TestOwnSignInEndings(t *testing.T) {
	reg := ownRegistration["dropbox"]
	p := newPKCEProvider(t, reg.ClientID)
	h := &browserHost{testHost{saved: map[string]string{}}}
	SetHost(h)
	defer RemoveRemote("t-end")
	for path, kind := range map[string]string{"/deny": "[declined]", "/badcode": "[signin]"} {
		_, err := ConfigStart("t-end", "dropbox", ownParams(p, path, nil))
		if err == nil || !strings.HasPrefix(err.Error(), kind) {
			t.Errorf("%s: %v, want %s", path, err, kind)
		}
	}
	// A stale answer doesn't end the sign-in: it is still waiting (and
	// then cancelled).
	go func() {
		time.Sleep(700 * time.Millisecond)
		ConfigCancel()
	}()
	if _, err := ConfigStart("t-end", "dropbox", ownParams(p, "/stale", nil)); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("stale: %v, want still waiting until cancelled", err)
	}
	// Cancel while the page is open, then sign in again straight away.
	go func() {
		time.Sleep(300 * time.Millisecond)
		ConfigCancel()
	}()
	start := time.Now()
	_, err := ConfigStart("t-end", "dropbox", ownParams(p, "/hang", nil))
	if err == nil || !strings.Contains(err.Error(), "cancelled") || time.Since(start) > 5*time.Second {
		t.Errorf("cancel: %v after %v", err, time.Since(start))
	}
	if _, err := ConfigStart("t-end", "dropbox", ownParams(p, "/authorize", nil)); err != nil {
		t.Errorf("signing in right after a cancel: %v", err)
	}
	// Left open past the limit.
	old := signInTimeout
	signInTimeout = 300 * time.Millisecond
	defer func() { signInTimeout = old }()
	if _, err := ConfigStart("t-end", "dropbox", ownParams(p, "/hang", nil)); err == nil || !strings.HasPrefix(err.Error(), "[expired]") {
		t.Errorf("timeout: %v", err)
	}
}

// An error page without a description shows the service's error code; a
// long description is cut to a length the app can show.
func TestReceiverErrorText(t *testing.T) {
	r := &receiver{state: "good", service: "OneDrive", result: make(chan answer, 1)}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/?error=server_error&state=good", nil))
	a := <-r.result
	if a.err == nil || a.err.Error() != "[signin] server_error" || w.Code != 400 {
		t.Errorf("no description: %v %d", a.err, w.Code)
	}
	if d := cleanDescription(strings.Repeat("a", 400)); len(d) != 300 {
		t.Errorf("long description kept %d bytes", len(d))
	}
}

// The token request's failures, each as the app reports it: a refusal
// without an error code names the HTTP status, an answer without a token
// is a failed sign-in, and an unreachable service is a network error.
func TestExchangeFailures(t *testing.T) {
	reg := ownRegistration["dropbox"]
	p := newPKCEProvider(t, reg.ClientID)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + l.Addr().String() + "/token"
	l.Close()
	for _, c := range []struct{ url, want string }{
		{p.URL + "/token500", "[signin] the token request failed (500 Internal Server Error)"},
		{p.URL + "/tokenempty", "[signin] oauth2: server response missing access_token"},
		{closed, "[network] "},
	} {
		tok, err := exchange(context.Background(), reg, c.url, "code", "verifier")
		if tok != nil || err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: %v %v, want %q", c.url, tok, err, c.want)
		}
	}
}

// A Microsoft directory (tenant) replaces "common" in both addresses: the
// sign-in and the token request go to the directory's own.
func TestOwnSignInTenant(t *testing.T) {
	reg := ownRegistration["onedrive"]
	p := newPKCEProvider(t, reg.ClientID)
	// Only the directory's addresses answer.
	mux := http.NewServeMux()
	mux.Handle("/contoso/", http.StripPrefix("/contoso", p.Config.Handler))
	dir := httptest.NewServer(mux)
	defer dir.Close()
	SetHost(&browserHost{testHost{saved: map[string]string{}}})
	old := signInTimeout
	signInTimeout = 5 * time.Second
	defer func() { signInTimeout = old }()
	tok, err := pkceSignIn(reg, map[string]any{"auth_url": dir.URL + "/common/authorize",
		"token_url": dir.URL + "/common/token", "tenant": "contoso"})
	if err != nil || !strings.Contains(tok, `"refresh_token":"rt-1"`) {
		t.Fatalf("tenant sign-in: %s %v (provider saw %v)", tok, err, p.log)
	}
}

// A service that gives no refresh token would sign Driveway out within the
// hour: the sign-in fails and says why.
func TestOwnSignInNoRefreshToken(t *testing.T) {
	reg := ownRegistration["dropbox"]
	p := newPKCEProvider(t, reg.ClientID)
	p.noRefresh = true
	SetHost(&browserHost{testHost{saved: map[string]string{}}})
	_, err := pkceSignIn(reg, map[string]any{"auth_url": p.URL + "/authorize", "token_url": p.URL + "/token"})
	if err == nil || !strings.HasPrefix(err.Error(), "[signin] Dropbox didn't let Driveway stay signed in") {
		t.Fatalf("no refresh token: %v", err)
	}
}

// Cancel while the code is being traded for a token: the sign-in ends as
// cancelled, not as a failed request.
func TestOwnSignInCancelDuringExchange(t *testing.T) {
	reg := ownRegistration["dropbox"]
	p := newPKCEProvider(t, reg.ClientID)
	SetHost(&browserHost{testHost{saved: map[string]string{}}})
	go func() {
		<-p.tokenHit
		ConfigCancel()
	}()
	start := time.Now()
	_, err := pkceSignIn(reg, map[string]any{"auth_url": p.URL + "/authorize", "token_url": p.URL + "/hangtoken"})
	if err == nil || err.Error() != "[other] sign-in cancelled" || time.Since(start) > 5*time.Second {
		t.Fatalf("cancelled during the token request: %v after %v", err, time.Since(start))
	}
}

// The port the service sends the browser back to: a sign-in that was just
// cancelled may hold it for a moment (the next one waits), but one held
// for good fails the sign-in with a reason, without opening the browser.
func TestOwnSignInPortBusy(t *testing.T) {
	reg := ownRegistration["dropbox"]
	p := newPKCEProvider(t, reg.ClientID)
	h := &browserHost{testHost{saved: map[string]string{}}}
	SetHost(h)
	m := map[string]any{"auth_url": p.URL + "/authorize", "token_url": p.URL + "/token"}
	hold, err := net.Listen("tcp", "127.0.0.1:"+loopbackPort)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(300*time.Millisecond, func() { hold.Close() })
	if _, err := pkceSignIn(reg, m); err != nil {
		t.Fatalf("a port freed after a moment: %v", err)
	}
	held, err := net.Listen("tcp", "127.0.0.1:"+loopbackPort)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	h.mu.Lock()
	h.urls = nil
	h.mu.Unlock()
	_, err = pkceSignIn(reg, m)
	if err == nil || !strings.HasPrefix(err.Error(), "[other] can't receive the sign-in on this device") {
		t.Fatalf("a port held for good: %v", err)
	}
	if len(h.urls) != 0 {
		t.Errorf("opened %q with nowhere to come back to", h.urls)
	}
}

// Cancel while the sign-in still waits for its port (a sign-in just
// cancelled may hold it for a moment): it ends as cancelled once it has the
// port, without opening the browser.
func TestOwnSignInCancelWaitingForPort(t *testing.T) {
	reg := ownRegistration["dropbox"]
	p := newPKCEProvider(t, reg.ClientID)
	h := &testHost{saved: map[string]string{}}
	SetHost(h)
	defer SetHost(&testHost{saved: map[string]string{}})
	hold, err := net.Listen("tcp", "127.0.0.1:"+loopbackPort)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := pkceSignIn(reg, map[string]any{"auth_url": p.URL + "/authorize", "token_url": p.URL + "/token"})
		done <- err
	}()
	stopped := false
	for until := time.Now().Add(2 * time.Second); !stopped && time.Now().Before(until); {
		time.Sleep(20 * time.Millisecond)
		stopped = stopOwnSignIn()
	}
	hold.Close()
	if !stopped {
		t.Error("a cancel while it waited for the port found nothing to stop")
	}
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		stopOwnSignIn()
		err = <-done
	}
	if err == nil || err.Error() != "[other] sign-in cancelled" {
		t.Errorf("cancelled while waiting for the port: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.urls) != 0 {
		t.Errorf("opened %q after the sign-in was cancelled", h.urls)
	}
}

// A sign-in started while the one before still ends (it holds the port a
// moment longer) can still be cancelled: the earlier one, ending, doesn't
// take the new one's Cancel with it.
func TestOwnSignInCancelAfterAnother(t *testing.T) {
	reg := ownRegistration["dropbox"]
	p := newPKCEProvider(t, reg.ClientID)
	h := &testHost{saved: map[string]string{}}
	SetHost(h)
	defer SetHost(&testHost{saved: map[string]string{}})
	m := map[string]any{"auth_url": p.URL + "/authorize", "token_url": p.URL + "/token"}
	urls := func(n int) []string {
		t.Helper()
		for until := time.Now().Add(5 * time.Second); time.Now().Before(until); time.Sleep(20 * time.Millisecond) {
			h.mu.Lock()
			got := append([]string(nil), h.urls...)
			h.mu.Unlock()
			if len(got) >= n {
				return got
			}
		}
		t.Fatalf("%d pages opened, want %d", len(h.urls), n)
		return nil
	}
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { _, err := pkceSignIn(reg, m); first <- err }()
	u, _ := url.Parse(urls(1)[0])
	signInMu.Lock()
	run := signInRun
	signInMu.Unlock()
	go func() { _, err := pkceSignIn(reg, m); second <- err }()
	for started := false; !started; time.Sleep(10 * time.Millisecond) {
		signInMu.Lock()
		started = signInRun != run
		signInMu.Unlock()
	}
	// The first ends (declined in the browser) while the second waits for
	// the port.
	resp, err := http.Get("http://127.0.0.1:" + loopbackPort + "/?error=access_denied&state=" + url.QueryEscape(u.Query().Get("state")))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if err := <-first; err == nil || !strings.HasPrefix(err.Error(), "[declined]") {
		t.Fatalf("first: %v", err)
	}
	urls(2)
	if !stopOwnSignIn() {
		t.Fatal("the second sign-in can't be cancelled")
	}
	if err := <-second; err == nil || err.Error() != "[other] sign-in cancelled" {
		t.Fatalf("second: %v", err)
	}
}

// With no app connected there is no browser: both sign-ins say so, and
// Driveway's frees its port.
func TestSignInNoBrowser(t *testing.T) {
	SetHost(nil)
	defer SetHost(&testHost{saved: map[string]string{}})
	if err := oauthutil.OpenURL("http://127.0.0.1:1/"); err == nil || err.Error() != "no browser" {
		t.Errorf("rclone's sign-in without a browser: %v", err)
	}
	reg := ownRegistration["dropbox"]
	p := newPKCEProvider(t, reg.ClientID)
	_, err := pkceSignIn(reg, map[string]any{"auth_url": p.URL + "/authorize", "token_url": p.URL + "/token"})
	if err == nil || err.Error() != "[other] no browser" {
		t.Fatalf("Driveway's sign-in without a browser: %v", err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+loopbackPort)
	if err != nil {
		t.Fatalf("the port is still held: %v", err)
	}
	l.Close()
	h := &testHost{saved: map[string]string{}}
	SetHost(h)
	if err := oauthutil.OpenURL("http://example.invalid/"); err != nil || len(h.urls) != 1 || h.urls[0] != "http://example.invalid/" {
		t.Errorf("rclone's sign-in with a browser: %v %q", err, h.urls)
	}
}

// A long description is cut on a character boundary, so the app never shows
// half a character.
func TestCleanDescriptionUTF8(t *testing.T) {
	d := cleanDescription("x" + strings.Repeat("é", 200))
	if !utf8.ValidString(d) || len(d) > 300 || len(d) < 297 {
		t.Fatalf("cut to %d bytes, valid UTF-8 %v", len(d), utf8.ValidString(d))
	}
}

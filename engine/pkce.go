// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fshttp"
	"github.com/rclone/rclone/lib/oauthutil"
	"golang.org/x/oauth2"
)

// Driveway's own sign-in for services where Merriment Labs has its own app
// registration (ownRegistration, below). Dropbox and OneDrive are
// public clients: the app carries no secret, and each sign-in proves itself
// with PKCE (RFC 7636) instead. Box has no public-client sign-in: its
// registration's secret is built into the app (boxClientSecret) and sent
// with the token requests, the way rclone's own Box registration works.
// Anything compiled into an app can be read back out of it, so that secret
// is public to anyone holding the APK; it identifies the app, not a person.
// The engine opens the service's page in the browser, receives the answer
// on this device at redirectURI, trades it for a token, and hands rclone a
// complete token with the registration's client ID and, except for Box, no
// secret. rclone then refreshes it as usual: for a public client its oauth2
// library tries the ID in a Basic header first (the services refuse an
// empty secret) and then in the form, which they accept; for Box the
// engine adds the secret back at connect time (engine.go).

// redirectURI is the address registered with each service. The listener
// is on the loopback interface only.
const (
	redirectURI  = "http://localhost:53682/"
	loopbackPort = "53682"
)

// registration is one of Merriment Labs' app registrations.
type registration struct {
	// Service is the name people know it by, for the browser page.
	Service  string
	ClientID string
	AuthURL  string
	TokenURL string
	// Scopes are exactly those the registration has enabled: Dropbox
	// refuses a sign-in that asks for one it doesn't.
	Scopes []string
	// Extra authorize parameters.
	Extra map[string]string
	// Presets answer rclone's setup questions after the sign-in, unless
	// the server's settings already do.
	Presets map[string]string
	// Secret: the registration's client secret, for a service that
	// requires one on its token requests (Box). Empty for a public client.
	Secret string
}

// Box's registration is filled in at build time (build.sh reads
// ~/.config/driveway/box-client, kept out of the repository):
//
//	-ldflags "-X github.com/jessejamesjohnston/driveway/engine.boxClientID=..."
//
// Without it Box connects only with an app's settings file.
var boxClientID, boxClientSecret string

func init() { initBox() }

// initBox turns on Box's own registration when the build carries it.
func initBox() {
	if boxClientID == "" || boxClientSecret == "" {
		return
	}
	ownRegistration["box"] = registration{
		Service:  "Box",
		ClientID: boxClientID,
		Secret:   boxClientSecret,
		AuthURL:  "https://account.box.com/api/oauth2/authorize",
		TokenURL: "https://api.box.com/oauth2/token",
		// No scopes: Box uses those set on the registration (read and
		// write all files and folders).
	}
	forms["box"] = boxSignInForm
}

// ownRegistration are the services that sign in with Merriment Labs' own
// registration. A server of one of these that was set up with rclone's
// built-in registration (no client_id) keeps it until it signs in again;
// one with a client ID of its own keeps rclone's sign-in with that ID.
var ownRegistration = map[string]registration{
	"dropbox": {
		Service:  "Dropbox",
		ClientID: "nnd1cgs8tlnkafa",
		AuthURL:  "https://www.dropbox.com/oauth2/authorize",
		TokenURL: "https://api.dropboxapi.com/oauth2/token",
		// Not sharing.write or sharing.read: Driveway makes no links and
		// doesn't use the shared-files modes (hidden on the form).
		Scopes: []string{"account_info.read", "files.metadata.read", "files.metadata.write",
			"files.content.read", "files.content.write"},
		// A refresh token, so the sign-in lasts.
		Extra: map[string]string{"token_access_type": "offline"},
	},
	"onedrive": {
		Service:  "OneDrive",
		ClientID: "16cf5e4e-49d2-4264-a4c5-40674afe4235",
		AuthURL:  "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
		TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
		// The person's own files. Not the .All scopes, which many work and
		// school directories let only an administrator approve: files
		// others shared with the person don't open, and SharePoint sites
		// aren't offered.
		Scopes: []string{"Files.ReadWrite", "offline_access", "User.Read"},
		// Lets someone with several Microsoft accounts pick which one.
		Extra: map[string]string{"prompt": "select_account"},
		// The person's OneDrive; SharePoint sites need Sites.Read.All.
		Presets: map[string]string{"config_type": "onedrive"},
	},
}

// usesOwnRegistration reports whether a sign-in to the service with these
// settings uses Merriment Labs' registration: no client ID of its own (or
// ours), and for OneDrive the global cloud, the only one the registration
// covers, and not a SharePoint library.
func usesOwnRegistration(backend string, m map[string]any) (registration, bool) {
	reg, ok := ownRegistration[backend]
	if !ok {
		return reg, false
	}
	if id := fmtValue(m["client_id"]); id != "" && id != reg.ClientID {
		return reg, false
	}
	if backend == "onedrive" {
		if r := fmtValue(m["region"]); r != "" && r != "global" {
			return reg, false
		}
		// A SharePoint library, set up with rclone's registration: ours
		// can't reach it (no Sites scope), so signing in again keeps rclone's.
		if fmtValue(m["drive_type"]) == "documentLibrary" {
			return reg, false
		}
	}
	if backend == "box" {
		// A server set up with an app's settings file (JWT) or a developer
		// token keeps that way in.
		for _, k := range []string{"config_credentials", "box_config_file", "access_token"} {
			if fmtValue(m[k]) != "" {
				return reg, false
			}
		}
	}
	return reg, true
}

// newVerifier is a PKCE code verifier: 32 random bytes, base64url without
// padding (43 characters from the unreserved set).
func newVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// challengeS256 is the verifier's S256 code challenge.
func challengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// buildAuthURL is the service's sign-in page for one sign-in.
func buildAuthURL(reg registration, base, state, challenge string) string {
	q := url.Values{}
	q.Set("client_id", reg.ClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	if len(reg.Scopes) > 0 {
		q.Set("scope", strings.Join(reg.Scopes, " "))
	}
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	for k, v := range reg.Extra {
		q.Set(k, v)
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + q.Encode()
}

// tokenJSON is the token as rclone keeps it (oauthutil.PutToken).
func tokenJSON(t *oauth2.Token) (string, error) {
	b, err := json.Marshal(t)
	return string(b), err
}

// cleanDescription drops what a person can't use from a service's error
// text (Microsoft's trace and correlation IDs and timestamp).
func cleanDescription(s string) string {
	if i := strings.Index(s, " Trace ID:"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.ReplaceAll(s, "+", " "))
	if len(s) > 300 {
		// Not through a character.
		s = strings.ToValidUTF8(s[:300], "")
	}
	return s
}

// The answer the browser brought back.
type answer struct {
	code string
	err  error
}

// receiver is the listener at redirectURI for one sign-in.
type receiver struct {
	state     string
	service   string
	result    chan answer
	once      sync.Once
	listeners []net.Listener
	srv       *http.Server
	served    sync.WaitGroup
}

func (r *receiver) deliver(a answer) {
	r.once.Do(func() { r.result <- a })
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	code, failure := q.Get("code"), q.Get("error")
	if req.URL.Path != "/" || (code == "" && failure == "") {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if q.Get("state") != r.state {
		// Not this sign-in's answer: an earlier sign-in's page (the
		// browser's Back button can reload one), or something else on the
		// device. Nothing from it is used, and this sign-in keeps waiting
		// for its own page.
		fs.Logf(nil, "sign-in: turned away an answer from another sign-in")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, page("This page is from an earlier sign-in",
			"Go back to Driveway. If it is still waiting, finish signing in on the page it opened, or open that page again."))
		return
	}
	var a answer
	switch {
	case failure == "access_denied":
		why := "access wasn't allowed"
		if d := cleanDescription(q.Get("error_description")); d != "" {
			why += ": " + d
		}
		a.err = tag("declined", errors.New(why))
	case failure != "":
		d := cleanDescription(q.Get("error_description"))
		if d == "" {
			d = failure
		}
		a.err = tag("signin", errors.New(d))
	default:
		a.code = code
	}
	if a.err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, page("Couldn't sign in to "+r.service, "Go back to Driveway to try again."))
	} else {
		// Not signed in yet: Driveway still trades the code for a token,
		// and says there whether that worked.
		_, _ = fmt.Fprint(w, page("Almost done", "Return to Driveway to finish connecting to "+r.service+"."))
	}
	r.deliver(a)
}

// returnLink opens Driveway from the browser page (ReturnActivity, which
// only brings Driveway's screen back). Chrome follows an intent: link on a
// tap; without Driveway it would offer Play.
const returnLink = "intent://signin#Intent;scheme=driveway;package=com.merrimentlabs.driveway;end"

// page is what the browser shows once the service sends it back: what
// happened, and a button back to Driveway, where the sign-in finishes or
// can be tried again.
func page(title, body string) string {
	return `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + html.EscapeString(title) + `</title>
<style>
:root { color-scheme: light dark; }
body { font-family: system-ui, sans-serif; margin: 0; padding: 48px 24px; line-height: 1.5; }
main { max-width: 32rem; margin: 0 auto; }
h1 { font-size: 1.5rem; font-weight: 600; margin: 0 0 8px; }
p { margin: 0; font-size: 1.125rem; }
a.back { display: block; margin-top: 32px; padding: 16px 24px; border-radius: 28px; text-align: center;
  font-size: 1.125rem; font-weight: 600; text-decoration: none; background: #1b6b5a; color: #fff; }
@media (prefers-color-scheme: dark) { a.back { background: #8fd4bf; color: #00382d; } }
</style></head>
<body><main><h1>` + html.EscapeString(title) + `</h1><p>` + html.EscapeString(body) + `</p>
<a class="back" href="` + returnLink + `">Return to Driveway</a></main></body></html>
`
}

// listen starts the receiver on 127.0.0.1 and on ::1 where there is one,
// since a browser may try either for "localhost". A sign-in that was just
// cancelled may still hold the port for a moment, so a busy port is tried
// again for a few seconds. ::1 held by anything else is a refusal, not a
// shrug: the service's answer (code and state) would go to whatever holds
// it, so the sign-in does not start (RFC 8252 section 8.3).
func listen(state, service string) (*receiver, error) {
	r := &receiver{state: state, service: service, result: make(chan answer, 1)}
	l, err := bindLoopback("127.0.0.1:" + loopbackPort)
	if err != nil {
		return nil, err
	}
	r.listeners = append(r.listeners, l)
	l6, err := holdV6Loopback()
	if err != nil {
		_ = l.Close()
		return nil, err
	}
	if l6 != nil {
		r.listeners = append(r.listeners, l6)
	}
	r.srv = &http.Server{Handler: r, ReadHeaderTimeout: 10 * time.Second}
	r.srv.SetKeepAlivesEnabled(false)
	for _, l := range r.listeners {
		r.served.Add(1)
		go func(l net.Listener) {
			defer r.served.Done()
			_ = r.srv.Serve(l)
		}(l)
	}
	return r, nil
}

// errV6Taken: something other than Driveway holds [::1]:53682, where a
// browser may deliver the service's answer for "localhost".
var errV6Taken = errors.New("another app on this device is using the sign-in port (53682); close it and try again")

// bindLoopback listens on addr, trying a busy port again for a few seconds
// (a sign-in that was just cancelled may still hold it).
func bindLoopback(addr string) (net.Listener, error) {
	var l net.Listener
	var err error
	for i := 0; i < 30; i++ {
		l, err = net.Listen("tcp", addr)
		if err == nil || !errors.Is(err, syscall.EADDRINUSE) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return l, err
}

// holdV6Loopback takes [::1]:53682 for a sign-in. No IPv6 loopback on the
// device returns (nil, nil): browsers then use 127.0.0.1. The port held by
// someone else returns errV6Taken.
func holdV6Loopback() (net.Listener, error) {
	l, err := bindLoopback("[::1]:" + loopbackPort)
	if err == nil {
		return l, nil
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return nil, errV6Taken
	}
	return nil, nil
}

// stop closes the listeners and waits until they are closed, so the next
// sign-in can have the port.
func (r *receiver) stop() {
	_ = r.srv.Close()
	r.served.Wait()
}

var (
	signInMu sync.Mutex
	// signInStop ends the sign-in waiting for the browser (ConfigCancel).
	signInStop context.CancelFunc
	// signInRun numbers the sign-ins, so one that ends only clears
	// signInStop while it is still its own.
	signInRun int
	// signInTimeout: past this, a sign-in left open in the browser ends by
	// itself (the app gives up sooner, at ten minutes).
	signInTimeout = 15 * time.Minute
)

// stopOwnSignIn ends a sign-in waiting for the browser, if there is one.
// It doesn't wait: the browser call (Host.OpenURL) may call it.
func stopOwnSignIn() bool {
	signInMu.Lock()
	defer signInMu.Unlock()
	if signInStop == nil {
		return false
	}
	signInStop()
	signInStop = nil
	return true
}

// errCancelled is a sign-in ended by ConfigCancel.
var errCancelled = errors.New("sign-in cancelled")

// pkceSignIn runs the service's sign-in with Merriment Labs' registration and
// returns the token as rclone keeps it. Settings may point it elsewhere
// (auth_url, token_url: the test stand-in) or at a Microsoft directory
// (tenant).
func pkceSignIn(reg registration, m map[string]any) (string, error) {
	authBase, tokenURL := reg.AuthURL, reg.TokenURL
	if s := fmtValue(m["auth_url"]); s != "" {
		authBase = s
	}
	if s := fmtValue(m["token_url"]); s != "" {
		tokenURL = s
	}
	if t := fmtValue(m["tenant"]); t != "" && strings.Contains(authBase, "/common/") {
		authBase = strings.Replace(authBase, "/common/", "/"+url.PathEscape(t)+"/", 1)
		tokenURL = strings.Replace(tokenURL, "/common/", "/"+url.PathEscape(t)+"/", 1)
	}
	verifier, err := newVerifier()
	if err != nil {
		return "", tag("other", err)
	}
	state, err := newVerifier()
	if err != nil {
		return "", tag("other", err)
	}
	c, cancel := context.WithTimeout(context.Background(), signInTimeout)
	defer cancel()
	// ConfigCancel can stop it from here on, while it waits for the port
	// included.
	signInMu.Lock()
	signInRun++
	run := signInRun
	signInStop = cancel
	signInMu.Unlock()
	defer func() {
		signInMu.Lock()
		if signInRun == run {
			signInStop = nil
		}
		signInMu.Unlock()
	}()
	r, err := listen(state, reg.Service)
	if errors.Is(err, errV6Taken) {
		return "", tag("port", err)
	}
	if err != nil {
		return "", tag("other", fmt.Errorf("can't receive the sign-in on this device: %w", err))
	}
	defer r.stop()
	if host == nil {
		return "", tag("other", errors.New("no browser"))
	}
	// Cancelled already: the person has left, so no page opens.
	if c.Err() != nil {
		return "", tag("other", errCancelled)
	}
	host.OpenURL(buildAuthURL(reg, authBase, state, challengeS256(verifier)))
	var a answer
	select {
	case a = <-r.result:
	case <-c.Done():
		if errors.Is(c.Err(), context.DeadlineExceeded) {
			return "", tag("expired", errors.New("the sign-in page was left open too long"))
		}
		return "", tag("other", errCancelled)
	}
	if a.err != nil {
		return "", a.err
	}
	tok, err := exchange(c, reg, tokenURL, a.code, verifier)
	if err != nil {
		if c.Err() != nil && !errors.Is(c.Err(), context.DeadlineExceeded) {
			return "", tag("other", errCancelled)
		}
		return "", err
	}
	if tok.RefreshToken == "" {
		return "", tag("signin", fmt.Errorf("%s didn't let Driveway stay signed in (no refresh token)", reg.Service))
	}
	return tokenJSON(tok)
}

// exchange trades the code for a token: the client ID in the form, the
// verifier, and the registration's secret if it has one (Box).
func exchange(c context.Context, reg registration, tokenURL, code, verifier string) (*oauth2.Token, error) {
	conf := &oauth2.Config{
		ClientID:     reg.ClientID,
		ClientSecret: reg.Secret,
		RedirectURL:  redirectURI,
		Endpoint:     oauth2.Endpoint{TokenURL: tokenURL, AuthStyle: oauth2.AuthStyleInParams},
	}
	c = oauthutil.Context(c, fshttp.NewClient(c))
	tok, err := conf.Exchange(c, code, oauth2.VerifierOption(verifier))
	if err == nil {
		return tok, nil
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		d := cleanDescription(re.ErrorDescription)
		if d == "" {
			d = re.ErrorCode
		}
		if d == "" {
			d = fmt.Sprintf("the token request failed (%s)", re.Response.Status)
		}
		return nil, tag("signin", errors.New(d))
	}
	if e := classify(err); strings.HasPrefix(e.Error(), "[network]") {
		return nil, e
	}
	return nil, tag("signin", err)
}

// ownSignInSettings runs the sign-in for ConfigStart and puts its outcome
// into the settings rclone's setup starts from: the token, our client ID
// (and secret, for Box), an answer to "replace the token?" (no: it is new),
// and the registration's presets.
func ownSignInSettings(reg registration, m map[string]any) error {
	tok, err := pkceSignIn(reg, m)
	if err != nil {
		return err
	}
	m["token"] = tok
	m["client_id"] = reg.ClientID
	if reg.Secret != "" {
		m["client_secret"] = reg.Secret
	} else {
		delete(m, "client_secret")
	}
	m["config_refresh_token"] = "false"
	keys := make([]string, 0, len(reg.Presets))
	for k := range reg.Presets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			m[k] = reg.Presets[k]
		}
	}
	return nil
}

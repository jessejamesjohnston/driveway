// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/rc"
	"github.com/rclone/rclone/lib/oauthutil"
)

// Host is what the engine needs from the app: a browser for signing in, and
// somewhere to keep settings the engine changes by itself (a service's
// refreshed sign-in token, which some services replace on every refresh).
type Host interface {
	OpenURL(url string)
	Saved(id, key, value string)
}

var host Host

// SetHost connects the engine to the app.
func SetHost(h Host) {
	setup()
	host = h
	store.onChange = func(section, key, value string) {
		if host != nil && key != "type" {
			host.Saved(section, key, value)
		}
	}
	// rclone's sign-in (a service without Merriment Labs' registration, or
	// a server with a client ID of its own) opens the service's page in a
	// browser and waits for it to come back to port 53682 on this device.
	oauthutil.OpenURL = func(url string) error {
		if host == nil {
			return errors.New("no browser")
		}
		if err := guardRcloneSignIn(); err != nil {
			// The browser never opens; stop rclone's wait so the setup
			// ends with an error instead of hanging until Cancel. rclone
			// only logs this error and reports a cancelled sign-in, so
			// next() puts the reason back.
			guardMu.Lock()
			guardRefused = err
			guardMu.Unlock()
			go func() {
				if call := rc.Calls.Get("config/oauthstop"); call != nil {
					_, _ = call.Fn(ctx(), rc.Params{})
				}
			}()
			return err
		}
		host.OpenURL(url)
		return nil
	}
}

var (
	guardMu     sync.Mutex
	rcloneGuard *http.Server
	// guardRefused: why the guard stopped rclone's sign-in, until next()
	// reports it.
	guardRefused error
)

// takeGuardRefusal returns and clears why the guard stopped rclone's
// sign-in, or nil.
func takeGuardRefusal() error {
	guardMu.Lock()
	defer guardMu.Unlock()
	err := guardRefused
	guardRefused = nil
	return err
}

// guardRcloneSignIn covers rclone's own sign-in, whose receiver listens on
// 127.0.0.1:53682 only while the service is told to come back to
// "http://localhost:53682/" (no PKCE, a public client secret). A browser
// may try ::1 first for localhost, so another app holding [::1]:53682
// would get the code and could redeem it. Driveway holds ::1 for the
// sign-in and sends the browser on to 127.0.0.1 with the same path and
// query; if something else already holds it, the sign-in does not start.
func guardRcloneSignIn() error {
	releaseRcloneGuard() // a second page open in the same sign-in takes it over
	l, err := holdV6Loopback()
	if err != nil || l == nil {
		return err
	}
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://127.0.0.1:"+loopbackPort+r.URL.RequestURI(), http.StatusTemporaryRedirect)
		})}
	srv.SetKeepAlivesEnabled(false)
	guardMu.Lock()
	old := rcloneGuard
	rcloneGuard = srv
	guardMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	go func() { _ = srv.Serve(l) }()
	return nil
}

// releaseRcloneGuard frees [::1]:53682 once rclone's sign-in is over.
func releaseRcloneGuard() {
	guardMu.Lock()
	s := rcloneGuard
	rcloneGuard = nil
	guardMu.Unlock()
	if s != nil {
		_ = s.Close()
	}
}

// question is one step of a service's setup, as JSON for the app; Done means
// setup is complete.
type question struct {
	Done      bool     `json:"done"`
	State     string   `json:"state,omitempty"`
	Name      string   `json:"name,omitempty"`
	Help      string   `json:"help,omitempty"`
	Default   string   `json:"default,omitempty"`
	Required  bool     `json:"required,omitempty"`
	Password  bool     `json:"password,omitempty"`
	Exclusive bool     `json:"exclusive,omitempty"`
	Examples  []choice `json:"examples,omitempty"`
	Error     string   `json:"error,omitempty"`
	// Friendly: Driveway's own question, in plain words: Help is the
	// question and each choice's Help its label (its Value is an ID).
	Friendly bool `json:"friendly,omitempty"`
}

// setupRun is what a setup in progress answers by itself, by setup id.
type setupRun struct {
	// answers: rclone's setup questions answered by name, each time asked
	// (presets, and the config_ settings the setup started with).
	answers map[string]string
	// own: the service's name when Driveway signed in with its own
	// registration. Then no rclone question or error text reaches the app.
	own string
}

var (
	runMu sync.Mutex
	runs  = map[string]setupRun{}
)

func runFor(id string) setupRun {
	runMu.Lock()
	defer runMu.Unlock()
	return runs[id]
}

func endRun(id string) {
	runMu.Lock()
	delete(runs, id)
	runMu.Unlock()
}

type choice struct {
	Value string `json:"value"`
	Help  string `json:"help,omitempty"`
}

// ConfigStart sets up the server id with rclone's own setup steps for the
// service: the settings in params (JSON, passwords in the clear), then any
// questions the service asks, returned one at a time as JSON. Services that
// sign in through a web page open the browser here and wait for the person
// to finish (ConfigCancel stops the wait).
func ConfigStart(id, backend, params string) (string, error) {
	setup()
	var m map[string]any
	if err := json.Unmarshal([]byte(params), &m); err != nil {
		return "", tag("other", err)
	}
	mu.Lock()
	dropRemote(id)
	mu.Unlock()
	// One sign-in at a time, on one port: a sign-in left waiting (the browser
	// closed, the person never came back) would otherwise block every later one.
	ConfigCancel()
	if ri, err := fs.Find(backend); err == nil {
		applyDefaults(ri, m)
	}
	applyPresets(forms[backend], m)
	endRun(id)
	run := setupRun{answers: map[string]string{}}
	// Merriment Labs' own registration: Driveway signs in (PKCE; Box with
	// its secret) and rclone's setup starts from the token.
	if reg, ok := usesOwnRegistration(backend, m); ok {
		run.own = reg.Service
		if fmtValue(m["token"]) == "" {
			if err := ownSignInSettings(reg, m); err != nil {
				return "", err
			}
		}
		if backend == "onedrive" {
			// Driveway picks the drive (onedrive_drive.go).
			out, err := pickOneDrive(id, m)
			if err == nil && !strings.Contains(out, `"done":true`) {
				runMu.Lock()
				runs[id] = run
				runMu.Unlock()
			}
			return out, err
		}
	}
	for k, v := range m {
		if strings.HasPrefix(k, "config_") {
			if s := fmtValue(v); s != "" {
				run.answers[k] = oneLine(s)
			}
		}
	}
	runMu.Lock()
	runs[id] = run
	runMu.Unlock()
	kv := rc.Params{}
	for k, v := range m {
		if s := fmtValue(v); s != "" {
			kv[k] = oneLine(s)
		}
	}
	out, err := config.CreateRemote(ctx(), id, backend, kv,
		config.UpdateRemoteOpt{NonInteractive: true, Obscure: true})
	return next(id, out, err)
}

// ConfigAnswer answers the question in state and returns the next one.
func ConfigAnswer(id, state, answer string) (string, error) {
	if state == oneDrivePickState {
		endRun(id)
		return answerOneDrive(id, answer)
	}
	out, err := config.UpdateRemote(ctx(), id, rc.Params{},
		config.UpdateRemoteOpt{NonInteractive: true, Continue: true, State: state, Result: answer})
	return next(id, out, err)
}

// next answers the questions the app never needs to show (this device has a
// browser; a preset settles it), and turns the rest into JSON. rclone only
// takes the setup's answers on its first call, so a step it goes back to
// (OneDrive's "type of connection" after a drive failed to open) would
// reach the person raw; it is answered here every time it comes. A step
// that comes back with an error isn't answered again: for Driveway's own
// sign-ins the setup stops with a plain error, for the others the person
// sees the question and the error.
func next(id string, out *fs.ConfigOut, err error) (string, error) {
	// rclone's browser sign-in, if any, has ended by the time next returns.
	defer releaseRcloneGuard()
	run := runFor(id)
	answered := map[string]int{}
	for err == nil && out != nil && out.Option != nil {
		name := out.Option.Name
		a, ok := run.answers[name]
		if name == "config_is_local" {
			a, ok = "true", true
		}
		if !ok {
			break
		}
		if out.Error != "" {
			if run.own != "" {
				endRun(id)
				return "", tag("setup", errors.New(out.Error))
			}
			break
		}
		answered[out.State]++
		if answered[out.State] > 2 {
			endRun(id)
			return "", tag("setup", fmt.Errorf("setup asked %s again and again", name))
		}
		out, err = config.UpdateRemote(ctx(), id, rc.Params{},
			config.UpdateRemoteOpt{NonInteractive: true, Continue: true, State: out.State, Result: a})
	}
	if err != nil {
		endRun(id)
		// rclone's sign-in stopped because another app holds [::1]:53682:
		// say that, not "cancelled" or "no longer signed in".
		if refused := takeGuardRefusal(); refused != nil {
			return "", tag("port", refused)
		}
		if run.own != "" {
			if e := classify(err); strings.HasPrefix(e.Error(), "[network]") {
				return "", e
			}
			return "", tag("setup", err)
		}
		return "", classify(err)
	}
	// A setup that ended on an error (Google Drive found no shared drive):
	// say so, rather than finish as if all went well.
	if out != nil && out.State == "" && out.Error != "" {
		endRun(id)
		if run.own != "" {
			return "", tag("setup", errors.New(out.Error))
		}
		return "", tag("other", errors.New(out.Error))
	}
	// Driveway's own sign-ins ask nothing more: a question rclone still has
	// would be in its words (drive IDs, SharePoint), so the setup stops
	// plainly instead.
	if run.own != "" && out != nil && out.State != "" {
		endRun(id)
		if out.Error != "" {
			return "", tag("setup", errors.New(out.Error))
		}
		name := ""
		if out.Option != nil {
			name = out.Option.Name
		}
		return "", tag("setup", fmt.Errorf("%s setup asked %q", run.own, name))
	}
	if out == nil || out.State == "" {
		endRun(id)
	}
	q := question{Done: out == nil || out.State == ""}
	if out != nil {
		q.State, q.Error = out.State, out.Error
		if o := out.Option; o != nil {
			q.Name, q.Help, q.Required, q.Password, q.Exclusive = o.Name, o.Help, o.Required, o.IsPassword, o.Exclusive
			if o.Default != nil {
				q.Default = fmtValue(o.Default)
			}
			for _, e := range o.Examples {
				q.Examples = append(q.Examples, choice{Value: e.Value, Help: e.Help})
			}
		}
	}
	b, _ := json.Marshal(q)
	return string(b), nil
}

// ConfigCancel stops a sign-in that is waiting for the browser.
func ConfigCancel() {
	runMu.Lock()
	for id := range runs {
		delete(runs, id)
	}
	runMu.Unlock()
	pickMu.Lock()
	pending = map[string]pendingPick{}
	pickMu.Unlock()
	stopOwnSignIn()
	if call := rc.Calls.Get("config/oauthstop"); call != nil {
		_, _ = call.Fn(ctx(), rc.Params{})
	}
	releaseRcloneGuard()
	_ = takeGuardRefusal()
}

// ConfigValues returns the server's settings after setup, as JSON, leaving
// out its type and its passwords (the app keeps those itself, in the clear).
func ConfigValues(id string) string {
	t, _ := store.GetValue(id, "type")
	secret := map[string]bool{}
	if ri, err := fs.Find(t); err == nil {
		for _, o := range ri.Options {
			if o.IsPassword {
				secret[o.Name] = true
			}
		}
	}
	// Merriment Labs' Box secret stays with the engine (SetRemote puts it
	// back): the app's settings, which it saves and may show, never carry it.
	ownSecret := ownRegistration[t].Secret
	out := map[string]string{}
	for _, k := range store.GetKeyList(id) {
		if k == "type" || secret[k] {
			continue
		}
		v, _ := store.GetValue(id, k)
		if k == "client_secret" && ownSecret != "" && v == ownSecret {
			continue
		}
		out[k] = v
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func fmtValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.Trim(string(b), `"`)
}

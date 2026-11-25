// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
)

// withBoxRegistration turns on Box's own registration for one test, as a
// build with ~/.config/driveway/box-client does.
func withBoxRegistration(t *testing.T, id, secret string) registration {
	t.Helper()
	oldReg, hadReg := ownRegistration["box"]
	oldForm := forms["box"]
	oldID, oldSecret := boxClientID, boxClientSecret
	boxClientID, boxClientSecret = id, secret
	delete(ownRegistration, "box")
	initBox()
	t.Cleanup(func() {
		boxClientID, boxClientSecret = oldID, oldSecret
		if hadReg {
			ownRegistration["box"] = oldReg
		} else {
			delete(ownRegistration, "box")
		}
		forms["box"] = oldForm
	})
	return ownRegistration["box"]
}

// Without the build's registration Box connects with an app's settings
// file, with no one-tap sign-in.
func TestBoxWithoutRegistration(t *testing.T) {
	if boxClientID != "" {
		t.Skip("built with a Box registration")
	}
	if _, ok := ownRegistration["box"]; ok || forms["box"].Method != methodAlt {
		t.Fatalf("Box without build values: registered %v, form %q", ok, forms["box"].Method)
	}
	defer func() { boxClientID, boxClientSecret = "", "" }()
	boxClientID = "only-id"
	initBox()
	if _, ok := ownRegistration["box"]; ok || forms["box"].Method != methodAlt {
		t.Fatal("an ID without a secret must not register Box")
	}
}

// One-tap Box: Driveway's sign-in sends the registration's secret with the
// code (Box has no public-client sign-in), keeps the token, and the secret
// stays with the engine: the app's settings never carry it, and the engine
// puts it back whenever the app hands the server over.
func TestBoxOwnSignIn(t *testing.T) {
	reg := withBoxRegistration(t, "boxid123", "boxsecret456")
	if forms["box"].Method != methodSignIn {
		t.Fatalf("Box form %q", forms["box"].Method)
	}
	p := newPKCEProvider(t, reg.ClientID)
	p.secret = reg.Secret
	h := &browserHost{testHost{saved: map[string]string{}}}
	SetHost(h)
	defer RemoveRemote("t-box")
	out, err := ConfigStart("t-box", "box", ownParams(p, "/authorize", nil))
	if err != nil || out != `{"done":true}` {
		t.Fatalf("setup: %s %v (provider %v)", out, err, p.log)
	}
	u, _ := url.Parse(h.urls[0])
	if u.Query().Has("scope") || u.Query().Get("client_id") != "boxid123" || u.Query().Get("redirect_uri") != redirectURI {
		t.Errorf("authorize %s", h.urls[0])
	}
	v := values("t-box")
	if v["client_id"] != "boxid123" || !strings.Contains(v["token"], `"refresh_token":"rt-1"`) {
		t.Fatalf("settings %v", v)
	}
	if _, ok := v["client_secret"]; ok {
		t.Fatalf("the app was given the secret: %v", v)
	}
	// The app hands the server over without the secret: the engine adds it
	// (rclone needs it for every refresh).
	b, _ := json.Marshal(map[string]string{"client_id": "boxid123", "token": v["token"]})
	if err := SetRemote("t-box", "box", string(b)); err != nil {
		t.Fatal(err)
	}
	if s, _ := store.GetValue("t-box", "client_secret"); s != "boxsecret456" {
		t.Fatalf("secret %q", s)
	}
	// Not for a server with another client ID, or set up with an app's
	// settings file.
	b, _ = json.Marshal(map[string]string{"client_id": "their-id", "token": v["token"]})
	_ = SetRemote("t-box", "box", string(b))
	if s, _ := store.GetValue("t-box", "client_secret"); s != "" {
		t.Errorf("secret given to another registration: %q", s)
	}
	for _, k := range []string{"config_credentials", "box_config_file", "access_token"} {
		if _, ok := usesOwnRegistration("box", map[string]any{k: "x"}); ok {
			t.Errorf("%s: a server with its own way in must keep it", k)
		}
	}
	if _, ok := usesOwnRegistration("box", map[string]any{}); !ok {
		t.Error("a new Box server signs in with Merriment Labs' registration")
	}
}

// A wrong secret is refused by the token endpoint: a sign-in error, never
// rclone's words.
func TestBoxOwnSignInWrongSecret(t *testing.T) {
	reg := withBoxRegistration(t, "boxid123", "boxsecret456")
	p := newPKCEProvider(t, reg.ClientID)
	p.secret = "another"
	SetHost(&browserHost{testHost{saved: map[string]string{}}})
	defer RemoveRemote("t-box-bad")
	_, err := ConfigStart("t-box-bad", "box", ownParams(p, "/authorize", nil))
	if err == nil || !strings.HasPrefix(err.Error(), "[signin]") {
		t.Fatalf("got %v", err)
	}
}

// A test service whose setup goes back to a question it asked before, as
// OneDrive's does after a drive fails to open. Hidden, so it is in no list.
func init() {
	fs.Register(&fs.RegInfo{
		Name: "tloop", Description: "setup loop stand-in", Hide: true,
		NewFs: func(context.Context, string, string, configmap.Mapper) (fs.Fs, error) {
			return nil, fs.ErrorNotImplemented
		},
		Config: func(_ context.Context, _ string, m configmap.Mapper, in fs.ConfigIn) (*fs.ConfigOut, error) {
			kinds := []fs.OptionExample{{Value: "a", Help: "A"}, {Value: "b", Help: "B"}}
			switch in.State {
			case "":
				if s, ok := m.Get("config_start"); ok && s != "" {
					return fs.ConfigChooseExclusiveFixed("start", "config_start", "Start", kinds)
				}
				return fs.ConfigChooseExclusiveFixed("typed", "config_type", "Type", kinds)
			case "typed":
				return fs.ConfigInput("other_done", "config_other", "Other")
			case "other_done":
				if in.Result == "fail" {
					return fs.ConfigError("again", "it broke")
				}
				if in.Result == "erroropt" { // a question this setup answers, back with an error
					return &fs.ConfigOut{State: "typed2", Error: "drive gone",
						Option: &fs.Option{Name: "config_type", Examples: kinds, Exclusive: true}}, nil
				}
				if in.Result == "circle" {
					return fs.ConfigChooseExclusiveFixed("circleq", "config_type", "Type", kinds)
				}
				return fs.ConfigGoto("again")
			case "circleq": // the same question, forever
				return fs.ConfigChooseExclusiveFixed("circleq", "config_type", "Type", kinds)
			case "again":
				return fs.ConfigChooseExclusiveFixed("typed2", "config_type", "Type", kinds)
			// The setup paths below start from a config_start answer.
			case "start":
				switch in.Result {
				case "erroropt": // a question that comes back carrying an error
					return &fs.ConfigOut{State: "typed2", Error: "drive gone",
						Option: &fs.Option{Name: "config_type", Examples: kinds, Exclusive: true}}, nil
				case "boom":
					return nil, errors.New("the service broke")
				case "offline":
					return nil, errors.New("dial tcp: connection refused")
				case "enderr":
					return &fs.ConfigOut{Error: "ended badly"}, nil
				}
			}
			return nil, nil
		},
	})
}

func asQuestion(t *testing.T, out string) question {
	t.Helper()
	var q question
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	return q
}

// A preset answers its question every time it comes, not only the first.
func TestPresetAnsweredWhenAskedAgain(t *testing.T) {
	defer RemoveRemote("t-loop")
	out, err := ConfigStart("t-loop", "tloop", `{"config_type":"a"}`)
	if err != nil {
		t.Fatal(err)
	}
	q := asQuestion(t, out)
	if q.Name != "config_other" {
		t.Fatalf("first question %s", out)
	}
	out, err = ConfigAnswer("t-loop", q.State, "ok")
	if err != nil || !asQuestion(t, out).Done {
		t.Fatalf("type of connection asked again reached the person: %s %v", out, err)
	}
}

// A question that comes back with an error isn't answered again: the person
// sees it with the error (services Driveway didn't sign in to)...
func TestPresetNotReansweredAfterError(t *testing.T) {
	defer RemoveRemote("t-loop2")
	out, _ := ConfigStart("t-loop2", "tloop", `{"config_type":"a"}`)
	out, err := ConfigAnswer("t-loop2", asQuestion(t, out).State, "fail")
	if err != nil {
		t.Fatal(err)
	}
	if q := asQuestion(t, out); q.Done || q.Error != "it broke" {
		t.Fatalf("%s", out)
	}
}

// ...while Driveway's own sign-ins stop plainly: no rclone question or error
// text reaches the app.
func TestOwnSetupNeverShowsRcloneQuestions(t *testing.T) {
	ownRegistration["tloop"] = registration{Service: "Tloop", ClientID: "x"}
	defer delete(ownRegistration, "tloop")
	defer RemoveRemote("t-loop3")
	// Signed in already (a token): the setup's next question is rclone's.
	_, err := ConfigStart("t-loop3", "tloop", `{"token":"{}","config_type":"a"}`)
	if err == nil || !strings.HasPrefix(err.Error(), "[setup]") {
		t.Fatalf("own setup with a question left: %v", err)
	}
	// And one that fails partway (the failure comes on a later call, which
	// is when rclone hands the question back with its error).
	delete(ownRegistration, "tloop")
	defer RemoveRemote("t-loop4")
	out, err := ConfigStart("t-loop4", "tloop", `{"config_type":"a"}`)
	if err != nil {
		t.Fatal(err)
	}
	runMu.Lock()
	r := runs["t-loop4"]
	r.own = "Tloop"
	runs["t-loop4"] = r
	runMu.Unlock()
	out, err = ConfigAnswer("t-loop4", asQuestion(t, out).State, "fail")
	if err == nil || !strings.HasPrefix(err.Error(), "[setup]") || !strings.Contains(err.Error(), "it broke") {
		t.Fatalf("own setup after a failure: %s %v", out, err)
	}
}

// The setup loop's other endings, each in Driveway's own sign-in (plain) and
// in another service's.
func TestSetupLoopEndings(t *testing.T) {
	own := func(on bool) {
		if on {
			ownRegistration["tloop"] = registration{Service: "Tloop", ClientID: "x"}
		} else {
			delete(ownRegistration, "tloop")
		}
	}
	defer own(false)
	start := func(path string) (string, error) {
		defer RemoveRemote("t-end")
		// The token makes an own setup skip the sign-in; config_start is
		// answered only once by rclone, then by Driveway each time it comes.
		return ConfigStart("t-end", "tloop", `{"token":"{}","config_start":"`+path+`"}`)
	}
	cases := []struct {
		path        string
		ownWant     string
		otherPrefix string
	}{
		{"erroropt", "[setup]", ""},
		{"boom", "[setup]", "[other]"},
		{"offline", "[network]", "[network]"},
		{"enderr", "[setup]", "[setupended]"},
	}
	for _, c := range cases {
		own(true)
		_, err := start(c.path)
		if err == nil || !strings.HasPrefix(err.Error(), c.ownWant) {
			t.Errorf("own %s: %v", c.path, err)
		}
		own(false)
		out, err := start(c.path)
		if c.otherPrefix == "" {
			if err != nil || asQuestion(t, out).Error != "drive gone" {
				t.Errorf("other %s: %s %v", c.path, out, err)
			}
		} else if err == nil || !strings.HasPrefix(err.Error(), c.otherPrefix) {
			t.Errorf("other %s: %s %v", c.path, out, err)
		}
	}
}

// A question rclone asks again and again is answered twice, then the setup
// stops (it would never end otherwise).
func TestPresetCircleStops(t *testing.T) {
	defer RemoveRemote("t-circle")
	out, err := ConfigStart("t-circle", "tloop", `{"config_type":"a"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConfigAnswer("t-circle", asQuestion(t, out).State, "circle"); err == nil || !strings.HasPrefix(err.Error(), "[setup]") {
		t.Fatalf("got %v", err)
	}
}

// A question the setup answers by itself, back with an error: not answered
// again. Another service's setup shows it with the error; Driveway's own
// stops plainly.
func TestPresetBackWithError(t *testing.T) {
	for _, own := range []bool{false, true} {
		id := fmt.Sprintf("t-backerr-%v", own)
		out, err := ConfigStart(id, "tloop", `{"config_type":"a"}`)
		if err != nil {
			t.Fatal(err)
		}
		if own {
			runMu.Lock()
			r := runs[id]
			r.own = "Tloop"
			runs[id] = r
			runMu.Unlock()
		}
		out, err = ConfigAnswer(id, asQuestion(t, out).State, "erroropt")
		RemoveRemote(id)
		if own && (err == nil || !strings.HasPrefix(err.Error(), "[setup]") || !strings.Contains(err.Error(), "drive gone")) {
			t.Errorf("own: %s %v", out, err)
		}
		if !own && (err != nil || asQuestion(t, out).Error != "drive gone" || asQuestion(t, out).Name != "config_type") {
			t.Errorf("other: %s %v", out, err)
		}
	}
}

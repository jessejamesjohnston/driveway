// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
)

type testHost struct {
	mu    sync.Mutex
	urls  []string
	saved map[string]string
}

func (h *testHost) OpenURL(u string) {
	h.mu.Lock()
	h.urls = append(h.urls, u)
	h.mu.Unlock()
}

func (h *testHost) Saved(id, key, value string) {
	h.mu.Lock()
	h.saved[id+"/"+key] = value
	h.mu.Unlock()
}

// fakeProvider stands in for a service's sign-in page and token endpoint.
func fakeProvider(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		// The person signs in and approves: back to the app with a code.
		back := r.URL.Query().Get("redirect_uri") + "?code=good-code&state=" + url.QueryEscape(r.URL.Query().Get("state"))
		http.Redirect(w, r, back, http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "good-code" && r.Form.Get("grant_type") != "refresh_token" {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"at-1","token_type":"bearer","refresh_token":"rt-1","expires_in":3600}`)
	})
	return httptest.NewServer(mux)
}

// rclone's own browser sign-in (a server with a client ID of its own), with
// a stand-in provider: the engine opens the
// browser, the "browser" signs in and comes back, the token is stored.
func TestSignIn(t *testing.T) {
	p := fakeProvider(t)
	defer p.Close()
	h := &testHost{saved: map[string]string{}}
	SetHost(h)
	// Play the browser: follow the URL the engine opened, redirects included.
	go func() {
		for i := 0; i < 100; i++ {
			h.mu.Lock()
			n := len(h.urls)
			h.mu.Unlock()
			if n > 0 {
				h.mu.Lock()
				u := h.urls[0]
				h.mu.Unlock()
				resp, err := http.Get(u)
				if err == nil {
					resp.Body.Close()
				}
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	params, _ := json.Marshal(map[string]string{"auth_url": p.URL + "/authorize", "token_url": p.URL + "/token",
		"client_id": "test-client", "client_secret": "test-secret"})
	out, err := ConfigStart("t-dropbox", "dropbox", string(params))
	if err != nil {
		t.Fatal(err)
	}
	var q question
	_ = json.Unmarshal([]byte(out), &q)
	for !q.Done {
		t.Logf("question %q (%s)", q.Name, strings.SplitN(q.Help, "\n", 2)[0])
		ans := q.Default
		if len(q.Examples) > 0 && ans == "" {
			ans = q.Examples[0].Value
		}
		out, err = ConfigAnswer("t-dropbox", q.State, ans)
		if err != nil {
			t.Fatal(err)
		}
		q = question{}
		_ = json.Unmarshal([]byte(out), &q)
	}
	if !strings.HasPrefix(h.urls[0], "http://127.0.0.1:53682/auth?state=") {
		t.Fatalf("opened %q", h.urls[0])
	}
	vals := ConfigValues("t-dropbox")
	if !strings.Contains(vals, `"token"`) || !strings.Contains(vals, "at-1") {
		t.Fatalf("no token stored: %s", vals)
	}
	h.mu.Lock()
	tok := h.saved["t-dropbox/token"]
	h.mu.Unlock()
	if !strings.Contains(tok, "rt-1") {
		t.Fatalf("the app wasn't told about the token: %v", h.saved)
	}
	RemoveRemote("t-dropbox")
}

// Cancelling while the browser is open ends the wait with an error.
func TestSignInCancel(t *testing.T) {
	p := fakeProvider(t)
	defer p.Close()
	h := &testHost{saved: map[string]string{}}
	SetHost(h)
	go func() {
		time.Sleep(500 * time.Millisecond)
		ConfigCancel()
	}()
	// A client ID of the server's own: rclone's sign-in, not Driveway's.
	params, _ := json.Marshal(map[string]string{"auth_url": p.URL + "/authorize", "token_url": p.URL + "/token",
		"client_id": "test-client", "client_secret": "test-secret"})
	done := make(chan error, 1)
	go func() {
		_, err := ConfigStart("t-cancel", "dropbox", string(params))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled sign-in reported success")
		}
		t.Logf("cancelled: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("cancel didn't end the wait")
	}
	RemoveRemote("t-cancel")
}

// cSetup is a service whose setup asks one question with choices: one
// answer finishes, any other ends the setup on an error, as Google Drive's
// does when it finds no shared drive. Hidden, so it is never listed.
func init() {
	fs.Register(&fs.RegInfo{
		Name: "c-setup",
		Hide: true,
		NewFs: func(context.Context, string, string, configmap.Mapper) (fs.Fs, error) {
			return nil, errors.New("not a real service")
		},
		Config: func(_ context.Context, _ string, m configmap.Mapper, in fs.ConfigIn) (*fs.ConfigOut, error) {
			switch in.State {
			case "":
				return fs.ConfigChooseExclusiveFixed("color", "c_color", "Which color?",
					[]fs.OptionExample{{Value: "red", Help: "Warm"}, {Value: "blue", Help: "Cool"}})
			case "color":
				if in.Result == "red" || in.Result == "blue" {
					m.Set("c_color", in.Result)
					return nil, nil
				}
				return fs.ConfigError("", "no "+in.Result+" here")
			}
			return nil, fmt.Errorf("unknown state %q", in.State)
		},
	})
}

// A setup question reaches the app with its choices; the answer finishes
// the setup and is kept, and a setup that ends on an error says so.
func TestConfigQuestion(t *testing.T) {
	defer RemoveRemote("c-setup1")
	out, err := ConfigStart("c-setup1", "c-setup", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var q question
	_ = json.Unmarshal([]byte(out), &q)
	if q.Done || q.Name != "c_color" || q.Help != "Which color?" || !q.Exclusive || q.Default != "red" ||
		len(q.Examples) != 2 || q.Examples[1] != (choice{Value: "blue", Help: "Cool"}) {
		t.Fatalf("question %s", out)
	}
	done, err := ConfigAnswer("c-setup1", q.State, "blue")
	if err != nil || done != `{"done":true}` {
		t.Fatalf("answered: %s %v", done, err)
	}
	if v, _ := store.GetValue("c-setup1", "c_color"); v != "blue" {
		t.Errorf("kept %q", v)
	}

	out, _ = ConfigStart("c-setup1", "c-setup", `{}`)
	q = question{}
	_ = json.Unmarshal([]byte(out), &q)
	if _, err := ConfigAnswer("c-setup1", q.State, "green"); err == nil || err.Error() != "[setupended] no green here" {
		t.Fatalf("a setup that ends on an error: %v", err)
	}
}

// Settings that aren't JSON are refused before anything starts.
func TestConfigStartBadSettings(t *testing.T) {
	if _, err := ConfigStart("c-setup2", "c-setup", `{"c_color":`); err == nil || !strings.HasPrefix(err.Error(), "[badsettings] bad settings: ") {
		t.Fatalf("broken settings: %v", err)
	}
}

// Settings arrive from JSON as strings, numbers and booleans, and reach
// rclone as text; a value with no text form is left out.
func TestFmtValue(t *testing.T) {
	for _, c := range []struct {
		v    any
		want string
	}{
		{"x", "x"}, {nil, ""}, {float64(2222), "2222"}, {true, "true"}, {math.Inf(1), ""}, {make(chan int), ""},
	} {
		if got := fmtValue(c.v); got != c.want {
			t.Errorf("fmtValue(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

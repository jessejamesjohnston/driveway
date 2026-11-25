// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeGraph stands in for Microsoft Graph after a OneDrive sign-in.
type fakeGraph struct {
	*httptest.Server
	mu     sync.Mutex
	hits   []string
	me     func(w http.ResponseWriter) // /me/drive
	drives string                      // /me/drives JSON, "" = 500
}

func newFakeGraph(t *testing.T) *fakeGraph {
	g := &fakeGraph{}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.hits = append(g.hits, r.URL.Path)
		g.mu.Unlock()
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer at-") {
			http.Error(w, `{"error":{"code":"InvalidAuthenticationToken"}}`, 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/me/drive":
			g.me(w)
		case "/me/drives":
			if g.drives == "" {
				http.Error(w, `{"error":{"code":"generalException"}}`, 500)
				return
			}
			fmt.Fprint(w, g.drives)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(g.Close)
	old := graphBase
	graphBase = g.URL
	t.Cleanup(func() { graphBase = old })
	return g
}

func (g *fakeGraph) paths() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.hits...)
}

func meDrive(body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) { fmt.Fprint(w, body) }
}

func meStatus(code int) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) { http.Error(w, `{"error":{"code":"itemNotFound"}}`, code) }
}

// A personal account's drive list, in the order Microsoft Graph returns it:
// system drives first (document libraries named by an ID, ODCMetadataArchive),
// then the account's OneDrive, then a Bundles_ store. rclone offered the
// first, which fails to open. (Synthetic IDs, shaped like real ones.)
const personalDrives = `{"value":[
 {"id":"b!Ex4mpleSite0-nOtReAl0000sItEiDfOrT3st-FakesAaaaLibraryOneAaaaaaa","driveType":"documentLibrary","name":"5d0c1f2a-7e3b"},
 {"id":"b!Ex4mpleSite0-nOtReAl0000sItEiDfOrT3st-FakesBbbb_LibraryTwo_Bbbbb","driveType":"documentLibrary","name":"B1A2C3D4-56"},
 {"id":"b!Ex4mpleSite0-nOtReAl0000sItEiDfOrT3st-FakeuCcccLibraryThreeCcccc","driveType":"documentLibrary","name":"ODCMetadataArchive"},
 {"id":"0123456789ABCDEF","driveType":"personal","name":"OneDrive","owner":{"user":{"displayName":"Sam"}}},
 {"id":"b!Ex4mpleSite0-nOtReAl0000sItEiDfOrT3st-FaketDdddLibraryFourDd-ddd","driveType":"documentLibrary","name":"E0F1A2"},
 {"id":"0123456789ABCDEF!s8b","driveType":"personal","name":"Bundles_00112233445566778899aabbccddeeff"}]}`

const personalMe = `{"id":"0123456789ABCDEF","driveType":"personal","name":"OneDrive","owner":{"user":{"displayName":"Sam"}}}`

// A work or school account: its OneDrive's ID starts with b! too.
const workMe = `{"id":"b!Wq9-workdrive-AbCdEf","driveType":"business","name":"OneDrive","owner":{"user":{"displayName":"Pat Lee"}}}`

func oneDriveSetup(t *testing.T, id string, extra map[string]string) (string, error) {
	t.Helper()
	reg := ownRegistration["onedrive"]
	p := newPKCEProvider(t, reg.ClientID)
	SetHost(&browserHost{testHost{saved: map[string]string{}}})
	t.Cleanup(func() { RemoveRemote(id) })
	return ConfigStart(id, "onedrive", ownParams(p, "/authorize", extra))
}

func values(id string) map[string]string {
	var v map[string]string
	_ = json.Unmarshal([]byte(ConfigValues(id)), &v)
	return v
}

// The account's own drive is set without a question: no "type of
// connection", no list of drive IDs, no "Drive OK?".
func TestOneDriveOwnDrivePersonal(t *testing.T) {
	g := newFakeGraph(t)
	g.me, g.drives = meDrive(personalMe), personalDrives
	out, err := oneDriveSetup(t, "t-od-personal", nil)
	if err != nil || out != `{"done":true}` {
		t.Fatalf("setup: %s %v", out, err)
	}
	v := values("t-od-personal")
	if v["drive_id"] != "0123456789ABCDEF" || v["drive_type"] != "personal" ||
		v["client_id"] != ownRegistration["onedrive"].ClientID || !strings.Contains(v["token"], `"refresh_token":"rt-1"`) {
		t.Fatalf("settings %v", v)
	}
	for k := range v {
		if strings.HasPrefix(k, "config_") {
			t.Errorf("setup answer kept as a setting: %s", k)
		}
	}
	if p := g.paths(); len(p) != 1 || p[0] != "/me/drive" {
		t.Errorf("Graph calls %v", p)
	}
}

func TestOneDriveOwnDriveWork(t *testing.T) {
	g := newFakeGraph(t)
	g.me = meDrive(workMe)
	out, err := oneDriveSetup(t, "t-od-work", nil)
	if err != nil || out != `{"done":true}` {
		t.Fatalf("setup: %s %v", out, err)
	}
	if v := values("t-od-work"); v["drive_id"] != "b!Wq9-workdrive-AbCdEf" || v["drive_type"] != "business" {
		t.Fatalf("settings %v", v)
	}
}

// Microsoft won't name the account's own drive: of the drives it lists, only
// one is a real OneDrive (the system drives and Bundles_ don't count), so it
// is set without a question.
func TestOneDriveFallbackOneRealDrive(t *testing.T) {
	g := newFakeGraph(t)
	g.me, g.drives = meStatus(500), personalDrives
	out, err := oneDriveSetup(t, "t-od-fallback", nil)
	if err != nil || out != `{"done":true}` {
		t.Fatalf("setup: %s %v", out, err)
	}
	if v := values("t-od-fallback"); v["drive_id"] != "0123456789ABCDEF" {
		t.Fatalf("settings %v", v)
	}
}

// Two real drives and no answer about which is the account's own: Driveway
// asks in plain words, and the answer settles it.
func TestOneDriveFallbackPicker(t *testing.T) {
	g := newFakeGraph(t)
	g.me = meStatus(404)
	g.drives = `{"value":[` + personalMe + `,` + workMe + `,
	 {"id":"b!lib","driveType":"documentLibrary","name":"Documents"}]}`
	out, err := oneDriveSetup(t, "t-od-pick", nil)
	if err != nil {
		t.Fatal(err)
	}
	var q question
	if err := json.Unmarshal([]byte(out), &q); err != nil || q.Done || !q.Friendly || !q.Exclusive || q.State != oneDrivePickState {
		t.Fatalf("question %s", out)
	}
	if q.Help != "Which OneDrive should Driveway open?" || len(q.Examples) != 2 ||
		q.Examples[0].Help != "Your OneDrive" || q.Examples[1].Help != "Your work or school OneDrive" {
		t.Fatalf("question %s", out)
	}
	for _, e := range q.Examples {
		if strings.Contains(e.Help, "b!") || strings.Contains(e.Help, "0123456789") {
			t.Errorf("a label shows an ID: %q", e.Help)
		}
	}
	out, err = ConfigAnswer("t-od-pick", q.State, q.Examples[1].Value)
	if err != nil || out != `{"done":true}` {
		t.Fatalf("answer: %s %v", out, err)
	}
	if v := values("t-od-pick"); v["drive_id"] != "b!Wq9-workdrive-AbCdEf" || v["drive_type"] != "business" {
		t.Fatalf("settings %v", v)
	}
	// An answer with nothing waiting (a stale screen) is a plain setup error.
	if _, err := ConfigAnswer("t-od-pick", oneDrivePickState, "x"); err == nil || !strings.HasPrefix(err.Error(), "[setup]") {
		t.Errorf("stale answer: %v", err)
	}
}

func TestOneDrivePickerLabelsTwoOfAKind(t *testing.T) {
	a := graphDrive{ID: "1", DriveType: "personal", Name: "OneDrive"}
	a.Owner.User.DisplayName = "Sam"
	b := graphDrive{ID: "2", DriveType: "personal", Name: "Family"}
	if got := driveLabel(a, true); got != "Your OneDrive (Sam)" {
		t.Errorf("%q", got)
	}
	if got := driveLabel(b, true); got != "Your OneDrive (Family)" {
		t.Errorf("%q", got)
	}
	if got := driveLabel(graphDrive{DriveType: "personal"}, true); got != "Your OneDrive" {
		t.Errorf("%q", got)
	}
}

func TestOneDriveNoDrives(t *testing.T) {
	g := newFakeGraph(t)
	g.me, g.drives = meStatus(404), `{"value":[{"id":"b!lib","driveType":"documentLibrary","name":"Docs"}]}`
	_, err := oneDriveSetup(t, "t-od-none", nil)
	if err == nil || !strings.HasPrefix(err.Error(), "[setup]") {
		t.Fatalf("no real drive: %v", err)
	}
	g.drives = ""
	_, err = oneDriveSetup(t, "t-od-none2", nil)
	if err == nil || !strings.HasPrefix(err.Error(), "[setup]") {
		t.Fatalf("no list: %v", err)
	}
}

func TestOneDriveGraphRefusesSignIn(t *testing.T) {
	g := newFakeGraph(t)
	g.me = meStatus(401)
	_, err := oneDriveSetup(t, "t-od-401", nil)
	if err == nil || !strings.HasPrefix(err.Error(), "[signinfailed]") {
		t.Fatalf("got %v", err)
	}
}

func TestOneDriveGraphUnreachable(t *testing.T) {
	g := newFakeGraph(t)
	g.Close()
	_, err := oneDriveSetup(t, "t-od-down", nil)
	if err == nil || !strings.HasPrefix(err.Error(), "[network]") {
		t.Fatalf("got %v", err)
	}
}

// Signing in again keeps the drive the server had, and asks Graph nothing.
func TestOneDriveSignInAgainKeepsDrive(t *testing.T) {
	g := newFakeGraph(t)
	g.me = meDrive(workMe)
	out, err := oneDriveSetup(t, "t-od-again", map[string]string{"drive_id": "kept-id", "drive_type": "personal"})
	if err != nil || out != `{"done":true}` {
		t.Fatalf("setup: %s %v", out, err)
	}
	if v := values("t-od-again"); v["drive_id"] != "kept-id" || v["drive_type"] != "personal" {
		t.Fatalf("settings %v", v)
	}
	if p := g.paths(); len(p) != 0 {
		t.Errorf("Graph calls %v", p)
	}
}

func TestOneDriveBadDriveJSON(t *testing.T) {
	g := newFakeGraph(t)
	g.me, g.drives = meDrive(`{not json`), personalDrives
	out, err := oneDriveSetup(t, "t-od-badjson", nil)
	if err != nil || out != `{"done":true}` {
		t.Fatalf("setup: %s %v", out, err)
	}
}

func TestOneDrivePickerUnknownAnswer(t *testing.T) {
	g := newFakeGraph(t)
	g.me = meStatus(404)
	g.drives = `{"value":[` + personalMe + `,` + workMe + `]}`
	out, err := oneDriveSetup(t, "t-od-unknown", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConfigAnswer("t-od-unknown", oneDrivePickState, "not-offered"); err == nil || !strings.HasPrefix(err.Error(), "[setup]") {
		t.Fatalf("%s %v", out, err)
	}
}

// Graph goes away between the two calls: a network error, not a setup one.
func TestOneDriveGraphGoneForTheList(t *testing.T) {
	g := newFakeGraph(t)
	g.me = func(w http.ResponseWriter) {
		fmt.Fprint(w, `{"id":"b!lib","driveType":"documentLibrary"}`)
		go g.Close()
	}
	_, err := oneDriveSetup(t, "t-od-gone", nil)
	if err == nil || !strings.HasPrefix(err.Error(), "[network]") {
		t.Fatalf("got %v", err)
	}
}

func TestGraphGetBadAddress(t *testing.T) {
	old := graphBase
	graphBase = "http://bad host"
	defer func() { graphBase = old }()
	if _, err := graphGet(ctx(), graphBase, "t", "/me/drive", &graphDrive{}); err == nil {
		t.Fatal("a bad address must fail")
	}
}

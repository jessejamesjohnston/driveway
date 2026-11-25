// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
)

// TestForms keeps the curated forms complete and true to rclone: run after
// each rclone upgrade (no test servers needed).
func TestForms(t *testing.T) {
	listed := map[string]bool{}
	for _, ri := range fs.Registry {
		if hidden[ri.Name] || ri.Hide || handMade[ri.Name] {
			continue
		}
		listed[ri.Name] = true
		f, ok := forms[ri.Name]
		if !ok {
			t.Errorf("%s isn't in forms: curate it (its name, essentials and method)", ri.Name)
			continue
		}
		if f.Label == "" {
			t.Errorf("%s has no label", ri.Name)
		}
		has := map[string]bool{}
		for _, o := range ri.Options {
			has[o.Name] = true
		}
		essential := map[string]bool{}
		for _, e := range f.Essentials {
			essential[e.Key] = true
			if e.Label == "" {
				t.Errorf("%s: essential %s has no label", ri.Name, e.Key)
			}
			// rclone's own setup answers (config_*) aren't options.
			if !has[e.Key] && !(e.Ephemeral && strings.HasPrefix(e.Key, fs.ConfigKeyEphemeralPrefix)) {
				t.Errorf("%s: essential %s isn't one of rclone's settings", ri.Name, e.Key)
			}
			if e.Ephemeral && !strings.HasPrefix(e.Key, fs.ConfigKeyEphemeralPrefix) {
				t.Errorf("%s: ephemeral %s must be a config_ answer (it is never kept)", ri.Name, e.Key)
			}
		}
		for _, k := range f.Hidden {
			if !has[k] {
				t.Errorf("%s: hidden %s isn't one of rclone's settings", ri.Name, k)
			}
		}
		for k := range f.Prefill {
			if !has[k] {
				t.Errorf("%s: prefill %s isn't one of rclone's settings", ri.Name, k)
			}
		}
		for k := range f.Presets {
			if !strings.HasPrefix(k, fs.ConfigKeyEphemeralPrefix) {
				t.Errorf("%s: preset %s must be a config_ answer", ri.Name, k)
			}
		}
		for key, presets := range f.PresetsWith {
			if !has[key] {
				t.Errorf("%s: presets depend on %s, which isn't one of rclone's settings", ri.Name, key)
			}
			for k := range presets {
				if !strings.HasPrefix(k, fs.ConfigKeyEphemeralPrefix) {
					t.Errorf("%s: preset %s must be a config_ answer", ri.Name, k)
				}
			}
		}
		switch f.Method {
		case methodUnavailable:
			continue
		case methodSignIn:
			// Offered through the browser only with a Merriment Labs
			// registration (ownRegistration in pkce.go).
			if _, own := ownRegistration[ri.Name]; !browserSignIn(ri) || !own {
				t.Errorf("%s: browser sign-in is only for services with Merriment Labs' registration", ri.Name)
			}
		case methodAlt:
			if f.Alt == nil || f.Alt.With == "" || f.Alt.How == "" || f.Alt.Row == "" {
				t.Errorf("%s: an alternative needs its words (with, how, row)", ri.Name)
			}
			if !borrowedSignIn[ri.Name] {
				t.Errorf("%s: an alternative is for a service whose browser sign-in would borrow rclone's registration", ri.Name)
			}
			if len(f.Essentials) == 0 || !f.Essentials[0].Required {
				t.Errorf("%s: an alternative needs its credential as the first, required essential", ri.Name)
			}
		case methodForm:
			if browserSignIn(ri) {
				t.Errorf("%s signs in through the browser: it can't be a plain form", ri.Name)
			}
		default:
			t.Errorf("%s: unknown method %q", ri.Name, f.Method)
		}
		// Every setting rclone can't do without is above Connect (a
		// required field folded under More settings couldn't be found),
		// or one rclone sets itself. Koofr's endpoint is required only for
		// its "other" provider, and is an essential.
		hide := map[string]bool{}
		for _, k := range f.Hidden {
			hide[k] = true
		}
		for _, o := range ri.Options {
			if o.Required && (o.Default == nil || fmt.Sprint(o.Default) == "") && o.Hide&fs.OptionHideConfigurator == 0 && !essential[o.Name] && !hide[o.Name] && !neverOnForm[o.Name] {
				t.Errorf("%s: required %s isn't an essential", ri.Name, o.Name)
			}
		}
		// Browser sign-in services get their token from signing in; any
		// other service with a "token" setting shows it or hides it on
		// purpose (Linkbox's is an API token from its site).
		if has["token"] && !browserSignIn(ri) && !essential["token"] && !hide["token"] {
			t.Errorf("%s: its token setting is neither an essential nor hidden", ri.Name)
		}
	}
	for name := range forms {
		if !listed[name] {
			t.Errorf("forms names %q, which isn't a listed rclone service", name)
		}
	}

	// What the app reads.
	var list []backend
	if err := json.Unmarshal([]byte(Backends()), &list); err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, b := range list {
		count[b.Method]++
		if b.BrowserSignIn && b.Method == methodForm && !handMade[b.Name] {
			t.Errorf("%s: browser sign-in listed as a plain form", b.Name)
		}
		seen := map[string]bool{}
		for _, e := range b.Essentials {
			seen[e.Key] = true
			if e.Choice && len(e.Examples) == 0 {
				t.Errorf("%s: choice %s has no examples to pick from", b.Name, e.Key)
			}
		}
		for _, o := range b.Options {
			if seen[o.Name] {
				t.Errorf("%s: %s is both an essential and under More settings, or listed twice", b.Name, o.Name)
			}
			seen[o.Name] = true
			if neverOnForm[o.Name] {
				t.Errorf("%s: %s should never be on the form", b.Name, o.Name)
			}
		}
	}
	t.Logf("%d services: %d sign in through the browser, %d forms, %d alternatives, %d with no way in",
		len(list), count[methodSignIn], count[methodForm]-len(handMade), count[methodAlt], count[methodUnavailable])
}

// The sign-in classification: a service signs in
// through the browser only if it uses rclone's shared sign-in settings.
// Linkbox, File Fabric and Shade have a "token" setting of their own, and
// are forms.
func TestSignInClassification(t *testing.T) {
	for _, name := range []string{"linkbox", "filefabric", "shade", "mailru"} {
		ri, err := fs.Find(name)
		if err != nil {
			t.Fatal(err)
		}
		if browserSignIn(ri) {
			t.Errorf("%s is classified as a browser sign-in", name)
		}
		if forms[name].Method != methodForm {
			t.Errorf("%s isn't a form", name)
		}
	}
	for _, name := range []string{"dropbox", "onedrive", "drive", "box"} {
		ri, _ := fs.Find(name)
		if !browserSignIn(ri) {
			t.Errorf("%s isn't classified as a browser sign-in", name)
		}
	}
}

// The forms' essentials are enough to connect, the way the app does it
// (rclone's setup with the form's settings, then a check and a listing),
// for the services with a test server: S3, FTP and a web folder (the WebDAV
// test server's own listing). Only the test servers' ports, which aren't
// the usual ones, come from More settings.
func TestEssentialsConnect(t *testing.T) {
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
	h := &testHost{saved: map[string]string{}}
	SetHost(h)
	cases := []struct {
		backend string
		values  map[string]string // essentials, as typed
		more    map[string]string // from More settings
		folder  string
	}{
		{"s3", map[string]string{"provider": "Other", "access_key_id": "driveway", "secret_access_key": "driveway-test",
			"endpoint": "http://127.0.0.1:9000"}, nil, ""},
		{"ftp", map[string]string{"host": "127.0.0.1", "user": "driveway", "pass": "driveway-test"},
			map[string]string{"port": "2121"}, ""},
		{"http", map[string]string{"url": "http://driveway:driveway-test@127.0.0.1:8080/"}, nil, ""},
	}
	for _, c := range cases {
		t.Run(c.backend, func(t *testing.T) {
			f := forms[c.backend]
			essential := map[string]bool{}
			for _, e := range f.Essentials {
				essential[e.Key] = true
				if e.Required && c.values[e.Key] == "" {
					t.Fatalf("required %s not given", e.Key)
				}
			}
			p := map[string]string{}
			for k, v := range f.Prefill {
				p[k] = v
			}
			for k, v := range c.values {
				if !essential[k] {
					t.Fatalf("%s isn't an essential", k)
				}
				p[k] = v
			}
			for k, v := range c.more {
				p[k] = v
			}
			id := "t-ess-" + c.backend
			params, _ := json.Marshal(p)
			out, err := ConfigStart(id, c.backend, string(params))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `"done":true`) || len(h.urls) > 0 {
				t.Fatalf("setup asked: %s (browser %v)", out, h.urls)
			}
			var kept map[string]any
			_ = json.Unmarshal([]byte(ConfigValues(id)), &kept)
			for k, v := range p {
				if _, ok := kept[k]; !ok {
					kept[k] = v // passwords: the app keeps those itself
				}
			}
			k2, _ := json.Marshal(kept)
			if err := SetRemote(id, c.backend, string(k2)); err != nil {
				t.Fatal(err)
			}
			defer RemoveRemote(id)
			if err := Check(id); err != nil {
				t.Fatal(err)
			}
			if _, err := ListNow(id, c.folder); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// listedBeforeNew are the services listed when New labels began (0.2.4):
// they have no Since and are never New. A service turned on later needs the
// version that first lists it.
var listedBeforeNew = map[string]bool{}

func init() {
	for _, n := range []string{"azureblob", "azurefiles", "b2", "box", "cloudinary", "doi", "drime", "drive",
		"dropbox", "fichier", "filefabric", "filelu", "filen", "filescom", "ftp", "gofile", "google cloud storage",
		"hdfs", "http", "iclouddrive", "imagekit", "internetarchive", "internxt", "jottacloud", "koofr", "linkbox",
		"mailru", "mega", "netstorage", "onedrive", "opendrive", "pikpak", "pixeldrain", "premiumizeme",
		"protondrive", "qingstor", "quatrix", "s3", "seafile", "shade", "sia", "storj", "swift", "ulozto"} {
		listedBeforeNew[n] = true
	}
}

// TestCatalogStates: which services are listed (every service with no way
// in is hidden, so nothing is listed before it works), that a hidden one still
// reaches the app (a server of it added before keeps working), and that
// every service listed since New labels began says since when.
func TestCatalogStates(t *testing.T) {
	wantHidden := map[string]bool{"hidrive": true, "huaweidrive": true, "oracleobjectstorage": true, "pcloud": true,
		"putio": true, "sugarsync": true, "yandex": true, "zoho": true}
	manifest, err := os.ReadFile("../AndroidManifest.xml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`versionName="([^"]+)"`).FindSubmatch(manifest)
	if m == nil {
		t.Fatal("no versionName in AndroidManifest.xml")
	}
	current := string(m[1])
	if compareVersions(sinceFirst, current) > 0 {
		t.Errorf("sinceFirst %s is after the app's version %s", sinceFirst, current)
	}
	var list []backend
	if err := json.Unmarshal([]byte(Backends()), &list); err != nil {
		t.Fatal(err)
	}
	got := map[string]backend{}
	for _, b := range list {
		got[b.Name] = b
		switch b.State {
		case stateListed, stateHidden:
		default:
			t.Errorf("%s: state %q", b.Name, b.State)
		}
		if b.State == stateHidden && !wantHidden[b.Name] {
			t.Errorf("%s is hidden", b.Name)
		}
		if b.State == stateListed && b.Method == methodUnavailable {
			t.Errorf("%s is listed with no way in: hide it (nothing is listed before it works)", b.Name)
		}
		if b.State == stateListed && !listedBeforeNew[b.Name] && !handMade[b.Name] {
			if !versionRe.MatchString(b.Since) {
				t.Errorf("%s was listed after New labels began: give it the version that lists it (Since), not %q", b.Name, b.Since)
			} else if compareVersions(b.Since, sinceFirst) <= 0 || compareVersions(b.Since, current) > 0 {
				t.Errorf("%s: Since %s must be after %s and no later than the app's version %s", b.Name, b.Since, sinceFirst, current)
			}
		}
		if listedBeforeNew[b.Name] && b.Since != "" {
			t.Errorf("%s was listed before New labels began: no Since", b.Name)
		}
	}
	for name := range wantHidden {
		b, ok := got[name]
		if !ok {
			t.Errorf("%s isn't in Backends(): a server of it added before couldn't open", name)
		} else if b.State != stateHidden {
			t.Errorf("%s is %s, want hidden", name, b.State)
		}
	}
	if got["drive"].State != stateListed {
		t.Errorf("Google Drive is %s, want listed", got["drive"].State)
	}
	// An uncurated service rclone adds with a browser sign-in is hidden.
	if f := formFor(&fs.RegInfo{Name: "made-up", Options: fs.Options{{Name: config.ConfigClientCredentials}}}); f.State != stateHidden {
		t.Errorf("an uncurated browser sign-in is %q, want hidden", f.State)
	}
}

var versionRe = regexp.MustCompile(`^\d+(\.\d+)*$`)

// compareVersions compares dotted versions as numbers ("0.2.10" > "0.2.9").
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			fmt.Sscan(pa[i], &x)
		}
		if i < len(pb) {
			fmt.Sscan(pb[i], &y)
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

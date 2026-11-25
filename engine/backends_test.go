// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
)

// TestBorrowedSignIn keeps the borrowed sign-in list complete: every service with
// rclone's shared sign-in settings is classified, and every classified name
// is a real service. Run after each rclone upgrade (no test servers needed).
func TestBorrowedSignIn(t *testing.T) {
	registered := map[string]bool{}
	for _, ri := range fs.Registry {
		registered[ri.Name] = true
		shared := false
		for _, o := range ri.Options {
			if o.Name == config.ConfigClientCredentials {
				shared = true
			}
		}
		_, own := ownRegistration[ri.Name]
		if shared && !borrowedSignIn[ri.Name] && !ownSignIn[ri.Name] && !own && !hidden[ri.Name] && !ri.Hide {
			t.Errorf("%s signs in with rclone's shared settings but isn't in borrowedSignIn, ownRegistration or ownSignIn", ri.Name)
		}
		if own && borrowedSignIn[ri.Name] {
			t.Errorf("%s has Merriment Labs' registration and is still in borrowedSignIn", ri.Name)
		}
	}
	for name := range borrowedSignIn {
		if !registered[name] {
			t.Errorf("borrowedSignIn names %q, which rclone doesn't register", name)
		}
	}
	for name := range ownRegistration {
		if !registered[name] {
			t.Errorf("ownRegistration names %q, which rclone doesn't register", name)
		}
	}
	for name := range ownSignIn {
		if !registered[name] {
			t.Errorf("ownSignIn names %q, which rclone doesn't register", name)
		}
	}

	var list []backend
	if err := json.Unmarshal([]byte(Backends()), &list); err != nil {
		t.Fatal(err)
	}
	borrowed, offered, own := 0, 0, 0
	for _, b := range list {
		if b.OwnClientID != "" {
			own++
			if b.OwnClientID != ownRegistration[b.Name].ClientID || b.BorrowedSignIn || b.Method != methodSignIn {
				t.Errorf("%s: listed with client %q, borrowed %v, method %s", b.Name, b.OwnClientID, b.BorrowedSignIn, b.Method)
			}
		}
		if b.BorrowedSignIn {
			borrowed++
		} else {
			offered++
		}
	}
	if own != len(ownRegistration) {
		t.Errorf("Backends lists %d services with Merriment Labs' registration, want %d", own, len(ownRegistration))
	}
	if borrowed != len(borrowedSignIn) {
		t.Errorf("Backends marks %d services as borrowed sign-in, want %d", borrowed, len(borrowedSignIn))
	}
	t.Logf("%d services listed: %d with a borrowed sign-in, %d others", len(list), borrowed, offered)
}

// TestCompiledBackends keeps Driveway's list of rclone services
// (rclone_backends.go) equal to rclone's own (backend/all) minus the ones
// removed by decision, and checks those are gone from the app.
// TestReadOnly keeps the read-only list true: every service named is
// registered and marked in the catalog, and every form that calls its
// service read-only is on the list.
func TestReadOnly(t *testing.T) {
	var list []backend
	if err := json.Unmarshal([]byte(Backends()), &list); err != nil {
		t.Fatal(err)
	}
	marked := map[string]bool{}
	for _, b := range list {
		marked[b.Name] = b.ReadOnly
		if b.ReadOnly != readOnly[b.Name] {
			t.Errorf("%s: readOnly %v in the catalog", b.Name, b.ReadOnly)
		}
	}
	for name := range readOnly {
		if !marked[name] {
			t.Errorf("readOnly names %q, which the catalog doesn't mark", name)
		}
	}
	for name, f := range forms {
		if strings.Contains(f.Blurb, "Read-only") && !readOnly[name] {
			t.Errorf("%s's form says it is read-only, but readOnly doesn't name it", name)
		}
	}
}

func TestCompiledBackends(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, "github.com/rclone/rclone/backend/all").Output()
	if err != nil {
		t.Skipf("go list: %v", err)
	}
	src, err := os.ReadFile("rclone_backends.go")
	if err != nil {
		t.Fatal(err)
	}
	ours := map[string]bool{}
	for _, m := range regexp.MustCompile(`_ "(github.com/rclone/rclone/backend/[a-z0-9]+)"`).FindAllStringSubmatch(string(src), -1) {
		ours[m[1]] = true
	}
	for _, p := range strings.Fields(string(out)) {
		name := p[strings.LastIndex(p, "/")+1:]
		if _, removed := removedBackends[name]; removed {
			if ours[p] {
				t.Errorf("%s was removed from Driveway but is compiled in", name)
			}
			continue
		}
		if !ours[p] {
			t.Errorf("rclone has %s, which rclone_backends.go doesn't list: add it (and curate its form)", p)
		}
		delete(ours, p)
	}
	for p := range ours {
		t.Errorf("rclone_backends.go lists %s, which rclone's backend/all doesn't", p)
	}
	for _, name := range removedBackends {
		if _, err := fs.Find(name); err == nil {
			t.Errorf("%q is still registered", name)
		}
		if strings.Contains(Backends(), `"name":"`+name+`"`) {
			t.Errorf("%q is still listed", name)
		}
	}
}

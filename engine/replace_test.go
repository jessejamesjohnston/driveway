// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"os"
	"strings"
	"testing"
)

// formAcceptedRows are addresses the app's form once accepted that Go's url
// package refuses. Each one, given to SetRemote for a server that already
// works, must leave that server unusable with a settings error, never
// working on the settings it had before.
var formAcceptedRows = []string{
	"http://127.0.0.1:8080/50%off",
	"https://nas.local/dav/100%",
	"https://[fe80::1%eth0]:5006/",
	"http://my nas.local/dav",
	"https://nas.local:5006:/dav",
	"https://cloud.example.com/dav/a%2",
	"https://alice:pw@[fe80::1%wlan0]/dav",
}

// TestRefusedReplace: a replace SetRemote refuses leaves the server unusable
// with a settings error (Check and every later call say [badsettings]), not
// working on the settings it had before and not "no such server".
func TestRefusedReplace(t *testing.T) {
	for _, addr := range formAcceptedRows {
		const id = "replace-refused"
		if err := SetRemote(id, "local", `{}`); err != nil {
			t.Fatal(err)
		}
		if err := Check(id); err != nil {
			t.Fatalf("the first settings don't work: %v", err)
		}
		err := SetRemote(id, "webdav", `{"url":`+quoteJSON(addr)+`,"user":"bob","pass":"wrong"}`)
		if err == nil {
			t.Errorf("%s: SetRemote accepted it", addr)
			continue
		}
		if !strings.HasPrefix(err.Error(), "[badsettings]") {
			t.Errorf("%s: SetRemote said %v", addr, err)
		}
		if err := Check(id); err == nil || !strings.HasPrefix(err.Error(), "[badsettings]") {
			t.Errorf("%s: Check after the refused replace: %v (want [badsettings])", addr, err)
		}
		if _, err := List(id, ""); err == nil || !strings.HasPrefix(err.Error(), "[badsettings]") {
			t.Errorf("%s: List after the refused replace: %v (want [badsettings])", addr, err)
		}
		if typ, _ := store.GetValue(id, "type"); typ != "" {
			t.Errorf("%s: the old settings are still in the store (type %q)", addr, typ)
		}
		RemoveRemote(id)
	}

	// A first add that is refused: the same settings error, not "no such server".
	const fresh = "replace-first"
	if err := SetRemote(fresh, "webdav", `{"url":"http://my nas.local/dav"}`); err == nil {
		t.Fatal("SetRemote accepted a first add with a bad address")
	}
	if err := Check(fresh); err == nil || !strings.HasPrefix(err.Error(), "[badsettings]") {
		t.Errorf("Check after a refused first add: %v (want [badsettings])", err)
	}
	// Removing it forgets the refusal too.
	RemoveRemote(fresh)
	if err := Check(fresh); err == nil || !strings.HasPrefix(err.Error(), "[notfound]") {
		t.Errorf("Check after removing a refused server: %v (want [notfound])", err)
	}

	// A replace with good settings after a refusal works on the new ones.
	const again = "replace-again"
	if err := SetRemote(again, "webdav", `{"url":"https://nas.local/dav/100%"}`); err == nil {
		t.Fatal("SetRemote accepted a bad address")
	}
	if err := SetRemote(again, "local", `{}`); err != nil {
		t.Fatal(err)
	}
	if err := Check(again); err != nil {
		t.Errorf("Check after a good replace: %v", err)
	}
	RemoveRemote(again)
}

// TestRefusedReplaceOnServer is the reviewer's sequence against the local
// WebDAV test server: a working server, a replace with an address Go can't
// read and another account, then Check. It must not succeed on the old
// account.
func TestRefusedReplaceOnServer(t *testing.T) {
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
	const id = "replace-webdav"
	t.Cleanup(func() { RemoveRemote(id) })
	if err := SetRemote(id, "webdav", `{"url":"http://127.0.0.1:8080","vendor":"other","user":"driveway","pass":"driveway-test"}`); err != nil {
		t.Fatal(err)
	}
	if err := Check(id); err != nil {
		t.Fatalf("the working settings: %v", err)
	}
	if err := SetRemote(id, "webdav", `{"url":"http://127.0.0.1:8080/50%off","vendor":"other","user":"bob","pass":"wrong"}`); err == nil {
		t.Fatal("SetRemote accepted the address")
	}
	if user, _ := store.GetValue(id, "user"); user == "driveway" {
		t.Errorf("the engine still holds the old account")
	}
	if err := Check(id); err == nil {
		t.Errorf("Check succeeded after the refused replace (on the old account)")
	} else if !strings.HasPrefix(err.Error(), "[badsettings]") {
		t.Errorf("Check after the refused replace: %v (want [badsettings])", err)
	}
}

// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"strings"
	"testing"

	"github.com/rclone/rclone/fs/config/obscure"
)

// TestAddressAtTheDoor: SetRemote refuses an address Go can't read with
// Driveway's own sentence (never the address), and moves a user name and
// password typed in an address into a service's own settings.
func TestAddressAtTheDoor(t *testing.T) {
	const refused = "[badsettings] bad settings: an address in the settings can't be read"
	for _, r := range addressRows {
		err := SetRemote("addr-door", r.backend, `{"url":`+quoteJSON(r.url)+`}`)
		if err == nil || err.Error() != refused {
			t.Errorf("%s: got %v, want %q", r.url, err, refused)
		}
	}
	for _, c := range []struct{ backend, params string }{
		{"webdav", `{"url":"dav.example.com/x"}`},
		{"webdav", `{"url":"https://nas.example.com:80a/dav"}`},
		{"s3", `{"provider":"Other","endpoint":"alice:MADEUP@s3.example.com"}`},
		{"http", `{"url":"http://[2001:db8::1/x"}`},
	} {
		if err := SetRemote("addr-door", c.backend, c.params); err == nil || err.Error() != refused {
			t.Errorf("%s: got %v, want %q", c.params, err, refused)
		}
	}
	t.Cleanup(func() { RemoveRemote("addr-door") })

	get := func(key string) string {
		v, _ := store.GetValue("addr-door", key)
		return v
	}
	reveal := func() string {
		p, err := obscure.Reveal(get("pass"))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Moved, with its escapes decoded.
	if err := SetRemote("addr-door", "webdav", `{"url":"https://alice:s3cr%40t%20x@dav.example.com:8443/remote.php/dav/files/alice@example.com/"}`); err != nil {
		t.Fatal(err)
	}
	if get("url") != "https://dav.example.com:8443/remote.php/dav/files/alice@example.com/" || get("user") != "alice" || reveal() != "s3cr@t x" {
		t.Errorf("not moved: url %q, user %q", get("url"), get("user"))
	}
	// Settings already set win, as they do in rclone; the address loses its copy.
	if err := SetRemote("addr-door", "webdav", `{"url":"http://u:p@[2001:db8::1]:2222/files/","user":"bob","pass":"theirs"}`); err != nil {
		t.Fatal(err)
	}
	if get("url") != "http://[2001:db8::1]:2222/files/" || get("user") != "bob" || reveal() != "theirs" {
		t.Errorf("the settings didn't win: url %q, user %q", get("url"), get("user"))
	}
	// A user name alone.
	if err := SetRemote("addr-door", "webdav", `{"url":"https://alice@dav.example.com/x"}`); err != nil {
		t.Fatal(err)
	}
	if get("url") != "https://dav.example.com/x" || get("user") != "alice" || get("pass") != "" {
		t.Errorf("user name alone: url %q, user %q", get("url"), get("user"))
	}
	// HTTP has no user name or password settings: the address keeps them.
	if err := SetRemote("addr-door", "http", `{"url":"https://alice:s3cret@files.example.com/"}`); err != nil {
		t.Fatal(err)
	}
	if get("url") != "https://alice:s3cret@files.example.com/" {
		t.Errorf("HTTP lost its sign-in: %q", get("url"))
	}
	// An endpoint may be a bare host; an address without a user name is as typed.
	if err := SetRemote("addr-door", "s3", `{"provider":"Other","endpoint":"s3.example.com"}`); err != nil {
		t.Fatal(err)
	}
	if err := SetRemote("addr-door", "webdav", `{"url":"https://dav.example.com/x?y=1#z"}`); err != nil || get("url") != "https://dav.example.com/x?y=1#z" {
		t.Errorf("a plain address changed: %v %q", err, get("url"))
	}
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

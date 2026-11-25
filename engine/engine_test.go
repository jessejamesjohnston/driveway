// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The same checks against every test server in test/servers/compose.yml.
// Run with DRIVEWAY_SERVERS=1 once those are up.
var servers = []struct {
	id, backend, params, base string
}{
	{"t-sftp", "sftp", `{"host":"127.0.0.1","port":"2222","user":"driveway","pass":"driveway-test","shell_type":"none"}`, "share"},
	{"t-smb", "smb", `{"host":"127.0.0.1","port":"1445","user":"driveway","pass":"driveway-test"}`, "share"},
	{"t-webdav", "webdav", `{"url":"http://127.0.0.1:8080","vendor":"other","user":"driveway","pass":"driveway-test"}`, ""},
	{"t-nfs", "nfs", `{"host":"127.0.0.1","port":"2050","export":"/"}`, ""},
	{"t-sftp-shell", "sftp", `{"host":"127.0.0.1","port":"2226","user":"driveway","pass":"driveway-test"}`, ""},
	{"t-s3", "s3", `{"provider":"Other","endpoint":"http://127.0.0.1:9000","access_key_id":"driveway","secret_access_key":"driveway-test"}`, "dw-bucket"},
	{"t-ftp", "ftp", `{"host":"127.0.0.1","port":"2121","user":"driveway","pass":"driveway-test"}`, ""},
}

func TestServers(t *testing.T) {
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
	for _, s := range servers {
		t.Run(s.backend, func(t *testing.T) {
			if err := SetRemote(s.id, s.backend, s.params); err != nil {
				t.Fatal(err)
			}
			if err := Check(s.id); err != nil {
				t.Fatalf("check: %v", err)
			}
			conformance(t, s.id, join(s.base, "t-"+token()))
		})
	}
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "/" + b
}

func conformance(t *testing.T, id, dir string) {
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	defer Delete(id, dir)
	must(Mkdir(id, dir+"/sub"))

	// Upload a new file, list it, stat it.
	data := make([]byte, 3<<20+123)
	_, _ = rand.Read(data)
	must(Upload(id, dir+"/a.bin", local(t, data)))
	ls, err := List(id, dir)
	must(err)
	var es []entry
	must(json.Unmarshal([]byte(ls), &es))
	if len(es) != 2 || es[0].Name != "a.bin" || es[0].Size != int64(len(data)) || !es[1].Dir {
		t.Fatalf("listing: %s", ls)
	}
	if strings.Contains(ls, ".driveway-") {
		t.Fatalf("temporary file left behind: %s", ls)
	}
	st, err := Stat(id, dir+"/sub")
	must(err)
	if !strings.Contains(st, `"dir":true`) {
		t.Fatalf("stat dir: %s", st)
	}

	// Ranged reads: start, a jump ahead, a jump back, the tail, past the end.
	r, err := Open(id, dir+"/a.bin")
	must(err)
	for _, c := range []struct{ off, n int }{{0, 4096}, {4096, 65536}, {2 << 20, 1000}, {100, 50}, {len(data) - 10, 4096}, {len(data) + 5, 10}} {
		got, err := r.ReadAt(int64(c.off), c.n)
		must(err)
		end := min(c.off+c.n, len(data))
		want := []byte{}
		if c.off < len(data) {
			want = data[c.off:end]
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("ReadAt(%d, %d): %d bytes, want %d", c.off, c.n, len(got), len(want))
		}
	}
	r.Close()

	// Replace it: the new contents, and no temporary files.
	data2 := []byte("second version")
	must(Upload(id, dir+"/a.bin", local(t, data2)))
	r, err = Open(id, dir+"/a.bin")
	must(err)
	got, err := r.ReadAt(0, 100)
	must(err)
	r.Close()
	if !bytes.Equal(got, data2) {
		t.Fatalf("after replace: %q", got)
	}
	ls, err = List(id, dir)
	must(err)
	if strings.Contains(ls, ".driveway-") {
		t.Fatalf("temporary file left behind: %s", ls)
	}

	// Rename a file, refuse to rename over an existing one, move a folder.
	must(Upload(id, dir+"/b.txt", local(t, []byte("b"))))
	must(Move(id, dir+"/b.txt", dir+"/c.txt"))
	if err := Move(id, dir+"/c.txt", dir+"/a.bin"); err == nil || !strings.HasPrefix(err.Error(), "[exists]") {
		t.Fatalf("move over existing: %v", err)
	}
	must(Upload(id, dir+"/sub/d.txt", local(t, []byte("d"))))
	// Files' move of a folder: in one step where the service can, else
	// refused at once (Files then moves the files one by one).
	if err := Move(id, dir+"/sub", dir+"/sub2"); err != nil {
		if !strings.HasPrefix(err.Error(), "[unsupported]") {
			t.Fatalf("folder move: %v", err)
		}
		if _, err := Stat(id, dir+"/sub/d.txt"); err != nil {
			t.Fatalf("refused folder move changed something: %v", err)
		}
		must(Rename(id, dir+"/sub", dir+"/sub2"))
	}
	if _, err := Stat(id, dir+"/sub2/d.txt"); err != nil {
		t.Fatalf("after folder move: %v", err)
	}

	// A save dialog's new file: empty, made within one call's deadline, and
	// seen by the bounded listing.
	must(Create(id, dir+"/new.txt"))
	ls, err = ListNow(id, dir)
	must(err)
	if !strings.Contains(ls, `"name":"new.txt","dir":false,"size":0`) || strings.Contains(ls, ".driveway-") {
		t.Fatalf("after create: %s", ls)
	}
	must(Delete(id, dir+"/new.txt"))

	// New documents and folders take a free name, even when the app's
	// listing is out of date (taken "[]" while new.txt exists).
	for _, c := range []struct{ name, taken, want string }{
		{"new.txt", "", "new.txt"},
		{"new.txt", "", "new (1).txt"},
		{"new.txt", "[]", "new (2).txt"},
		{".hidden", "", ".hidden"},
		{".hidden", "", ".hidden (1)"},
	} {
		got, err := CreateNew(id, dir, c.name, false, c.taken)
		must(err)
		if got != c.want {
			t.Fatalf("CreateNew(%s, %s) = %s, want %s", c.name, c.taken, got, c.want)
		}
	}
	for _, want := range []string{"made", "made (1)"} {
		got, err := CreateNew(id, dir, "made", true, "")
		must(err)
		if got != want {
			t.Fatalf("CreateNew folder = %s, want %s", got, want)
		}
	}
	if st, err := Stat(id, dir+"/made (1)"); err != nil || !strings.Contains(st, `"dir":true`) {
		t.Fatalf("new folder: %s %v", st, err)
	}
	if st, err := Stat(id, dir+"/new (2).txt"); err != nil || !strings.Contains(st, `"size":0`) {
		t.Fatalf("new file: %s %v", st, err)
	}
	for _, n := range []string{"new.txt", "new (1).txt", "new (2).txt", ".hidden", ".hidden (1)", "made", "made (1)"} {
		must(Delete(id, dir+"/"+n))
	}

	// Errors carry their category.
	if _, err := Stat(id, dir+"/missing"); err == nil || !strings.HasPrefix(err.Error(), "[notfound]") {
		t.Fatalf("missing file: %v", err)
	}

	// Delete a file, then a folder with something in it.
	must(Delete(id, dir+"/c.txt"))
	must(Delete(id, dir+"/sub2"))
	ls, err = List(id, dir)
	must(err)
	if strings.Contains(ls, "c.txt") || strings.Contains(ls, "sub2") {
		t.Fatalf("after delete: %s", ls)
	}
}

func local(t *testing.T, data []byte) string {
	p := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWrongPassword(t *testing.T) {
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
	for _, c := range []struct{ backend, params string }{
		{"sftp", `{"host":"127.0.0.1","port":"2222","user":"driveway","pass":"wrong","shell_type":"none"}`},
		{"smb", `{"host":"127.0.0.1","port":"1445","user":"driveway","pass":"wrong"}`},
		{"webdav", `{"url":"http://127.0.0.1:8080","vendor":"other","user":"driveway","pass":"wrong"}`},
	} {
		if err := SetRemote("t-bad", c.backend, c.params); err != nil {
			t.Fatal(err)
		}
		if err := Check("t-bad"); err == nil || !strings.HasPrefix(err.Error(), "[auth]") {
			t.Errorf("%s with a wrong password: %v", c.backend, err)
		}
	}
	RemoveRemote("t-bad")
}

func TestSSHKey(t *testing.T) {
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
	hk, err := HostKey("127.0.0.1", 2222)
	if err != nil || !strings.Contains(hk, "SHA256:") {
		t.Fatalf("host key: %s %v", hk, err)
	}
	// The type of the key pinned, so the app names the file to compare with.
	var typed map[string]string
	_ = json.Unmarshal([]byte(hk), &typed)
	if !strings.HasPrefix(typed["line"], "[127.0.0.1]:2222 "+typed["type"]+" ") {
		t.Fatalf("key type %q doesn't match the pinned line %q", typed["type"], typed["line"])
	}
	pem := testKey(t)
	var h map[string]string
	_ = json.Unmarshal([]byte(hk), &h)
	known := filepath.Join(t.TempDir(), "known_hosts")
	_ = os.WriteFile(known, []byte(h["line"]+"\n"), 0o600)
	params, _ := json.Marshal(map[string]string{"host": "127.0.0.1", "port": "2222", "user": "driveway",
		"key_pem": string(pem), "known_hosts_file": known, "shell_type": "none"})
	if err := SetRemote("t-key", "sftp", string(params)); err != nil {
		t.Fatal(err)
	}
	if err := Check("t-key"); err != nil {
		t.Fatalf("key login with pinned host key: %v", err)
	}
	// A different pinned key must be refused.
	_ = os.WriteFile(known, []byte("[127.0.0.1]:2222 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGb0JzZ9XqkV0Qy3V6sRjv5pY5i1n6+4i8U3k2m1nQ9x\n"), 0o600)
	if err := Check("t-key"); err == nil || !strings.HasPrefix(err.Error(), "[auth]") {
		t.Fatalf("changed host key: %v", err)
	}
	RemoveRemote("t-key")
	if k, err := NewKey("driveway@test"); err != nil || !strings.Contains(k, "ssh-ed25519 ") {
		t.Fatalf("new key: %v", err)
	}
}

func TestBackends(t *testing.T) {
	var bs []backend
	if err := json.Unmarshal([]byte(Backends()), &bs); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, b := range bs {
		names[b.Name] = true
	}
	for _, n := range []string{"sftp", "smb", "webdav", "nfs", "s3", "ftp"} {
		if !names[n] {
			t.Errorf("missing backend %s", n)
		}
	}
	if names["local"] {
		t.Error("local storage is listed")
	}
	t.Logf("%d backends", len(bs))
}

func TestServerCopy(t *testing.T) {
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
	for _, s := range servers {
		t.Run(s.backend, func(t *testing.T) {
			params := s.params
			if s.backend == "sftp" {
				// The copy pins host keys like the app: fetch and trust them here.
				port := map[string]int{"t-sftp": 2222, "t-sftp-shell": 2226}[s.id]
				hk, err := HostKey("127.0.0.1", port)
				if err != nil {
					t.Fatal(err)
				}
				var h map[string]string
				_ = json.Unmarshal([]byte(hk), &h)
				kh := filepath.Join(t.TempDir(), "known_hosts")
				_ = os.WriteFile(kh, []byte(h["line"]+"\n"), 0o600)
				params = strings.TrimSuffix(params, "}") + `,"known_hosts_file":"` + kh + `"}`
			}
			if err := SetRemote(s.id, s.backend, params); err != nil {
				t.Fatal(err)
			}
			dir := join(s.base, "c-"+token())
			defer Delete(s.id, dir)
			if err := Upload(s.id, dir+"/a.txt", local(t, []byte("copy me"))); err != nil {
				t.Fatal(err)
			}
			err := Copy(s.id, dir+"/a.txt", dir+"/b.txt")
			switch {
			case err == nil:
				if err := Copy(s.id, dir+"/a.txt", dir+"/b.txt"); err == nil || !strings.HasPrefix(err.Error(), "[exists]") {
					t.Fatalf("copy over an existing file: %v", err)
				}
				r, err := Open(s.id, dir+"/b.txt")
				if err != nil {
					t.Fatal(err)
				}
				got, _ := r.ReadAt(0, 100)
				r.Close()
				if string(got) != "copy me" {
					t.Fatalf("copy has %q", got)
				}
				t.Logf("%s: copied on the server", s.backend)
			case strings.HasPrefix(err.Error(), "[unsupported]"):
				t.Logf("%s: no server-side copy (the app copies the ordinary way)", s.backend)
			default:
				t.Fatal(err)
			}
		})
	}
}

// A kernel NFS server with the default "secure" export refuses clients on
// ports above 1024, as every Android app is: the error must say what to change.
func TestKernelNFSSecure(t *testing.T) {
	host := os.Getenv("DRIVEWAY_KNFS")
	if host == "" {
		t.Skip("set DRIVEWAY_KNFS=host with the kernel-nfs test server, export secure (the default)")
	}
	if err := SetRemote("t-knfs", "nfs", `{"host":"`+host+`","export":"/export"}`); err != nil {
		t.Fatal(err)
	}
	err := Check("t-knfs")
	t.Logf("error: %v", err)
	if err == nil || !strings.Contains(err.Error(), "non-privileged ports") {
		t.Fatalf("want the non-privileged ports explanation, got %v", err)
	}
}

// A copy on an SFTP server that outlasts its limit (a slow disk, a shell
// that hangs) is stopped, leaves nothing behind, and says "unsupported" so
// Files copies the ordinary way, all well inside Files' time limit.
func TestServerCopySlow(t *testing.T) {
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
	hk, err := HostKey("127.0.0.1", 2226)
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]string
	_ = json.Unmarshal([]byte(hk), &h)
	kh := filepath.Join(t.TempDir(), "known_hosts")
	_ = os.WriteFile(kh, []byte(h["line"]+"\n"), 0o600)
	if err := SetRemote("t-slowcp", "sftp", `{"host":"127.0.0.1","port":"2226","user":"driveway","pass":"driveway-test","known_hosts_file":"`+kh+`"}`); err != nil {
		t.Fatal(err)
	}
	defer RemoveRemote("t-slowcp")
	oldCmd, oldLimit := cpCommand, copyLimitTime
	cpCommand, copyLimitTime = "cp -p -- %s %s; sleep 30", 3*time.Second
	defer func() { cpCommand, copyLimitTime = oldCmd, oldLimit }()
	dir := "slow-" + token()
	defer Delete("t-slowcp", dir)
	if err := Upload("t-slowcp", dir+"/a.txt", local(t, []byte("copy me"))); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = Copy("t-slowcp", dir+"/a.txt", dir+"/b.txt")
	took := time.Since(start)
	if err == nil || !strings.HasPrefix(err.Error(), "[unsupported]") {
		t.Fatalf("want unsupported, got %v", err)
	}
	if took > 10*time.Second {
		t.Fatalf("took %v", took)
	}
	if _, err := Stat("t-slowcp", dir+"/b.txt"); err == nil || !strings.HasPrefix(err.Error(), "[notfound]") {
		t.Fatalf("the stopped copy: %v, want [notfound]", err)
	}
	t.Logf("stopped after %v: %v", took.Round(100*time.Millisecond), err)
}

// testKey is the SFTP test server's key pair's private half, which
// ./build.sh test makes the first time (test_key) and hands to the server it
// starts. The server checks are run through it: a checkout without the pair
// can't sign in to servers started from another one.
func testKey(t *testing.T) []byte {
	t.Helper()
	pem, err := os.ReadFile("../test/servers/keys/driveway")
	if err != nil {
		t.Fatalf("no SFTP test key (%v): ./build.sh test makes it and starts the servers with it", err)
	}
	return pem
}

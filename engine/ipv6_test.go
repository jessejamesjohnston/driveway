// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// What the engine keeps for a server's host, for each kind that takes one
// with a port: rclone's SFTP, SMB and FTP join host and port with a bare
// colon, so an IPv6 address must reach them in brackets; Driveway's own NFS
// adds the brackets itself and needs it bare. The app may hand over either
// form (a server found on the network is saved bare, one typed long ago may
// be bracketed), and the result is the same.
func TestHostForms(t *testing.T) {
	cases := []struct{ backend, given, want string }{
		{"sftp", "::1", "[::1]"},
		{"sftp", "[::1]", "[::1]"},
		{"sftp", "2001:db8::5", "[2001:db8::5]"},
		{"sftp", "fe80::1%wlan0", "[fe80::1%wlan0]"},
		{"sftp", "[fe80::1%wlan0]", "[fe80::1%wlan0]"},
		{"sftp", "nas.local", "nas.local"},
		{"sftp", "192.0.2.10", "192.0.2.10"},
		{"smb", "fd00::5", "[fd00::5]"},
		{"smb", "[fd00::5]", "[fd00::5]"},
		{"smb", "nas.local", "nas.local"},
		{"ftp", "2001:db8::1", "[2001:db8::1]"},
		{"ftp", "[2001:db8::1]", "[2001:db8::1]"},
		{"ftp", "ftp.example.com", "ftp.example.com"},
		{"nfs", "[fd00::5]", "fd00::5"},
		{"nfs", "fd00::5", "fd00::5"},
		{"nfs", "nas.local", "nas.local"},
		// Not an address: left exactly as it was given.
		{"sftp", "[nas.local]", "[nas.local]"},
		{"smb", "fd00::5]", "fd00::5]"},
	}
	for _, c := range cases {
		b, _ := json.Marshal(map[string]string{"host": c.given})
		if err := SetRemote("t-host", c.backend, string(b)); err != nil {
			t.Fatal(err)
		}
		if got, _ := store.GetValue("t-host", "host"); got != c.want {
			t.Errorf("%s with host %q: the engine holds %q, want %q", c.backend, c.given, got, c.want)
		}
		// The app reads settings back after a setup and saves them: it gets
		// its own form again (bare), never the backend's.
		var back map[string]string
		_ = json.Unmarshal([]byte(ConfigValues("t-host")), &back)
		wantBack := c.want
		if strings.Contains(c.want, ":") && strings.HasPrefix(c.want, "[") && strings.HasSuffix(c.want, "]") {
			wantBack = c.want[1 : len(c.want)-1]
		}
		if back["host"] != wantBack {
			t.Errorf("%s with host %q: settings read back hold %q, want %q", c.backend, c.given, back["host"], wantBack)
		}
	}
	RemoveRemote("t-host")
}

// A host key pinned for an IPv6 server on the usual port is found again when
// rclone and the server copy check it, whichever form the host was given in.
func TestHostKeyLineIPv6(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	for _, host := range []string{"::1", "[::1]", "2001:db8::5", "[2001:db8::5]"} {
		for _, port := range []int{22, 2222} {
			line := knownHostsLine(host, port, signer.PublicKey())
			kh := filepath.Join(t.TempDir(), "known_hosts")
			_ = os.WriteFile(kh, []byte(line+"\n"), 0o600)
			check, err := knownhosts.New(kh)
			if err != nil {
				t.Fatalf("%s port %d: line %q doesn't parse: %v", host, port, line, err)
			}
			bare := strings.Trim(host, "[]")
			addr := net.JoinHostPort(bare, fmt.Sprint(port))
			tcp := &net.TCPAddr{IP: net.ParseIP(bare), Port: port}
			if err := check(addr, tcp, signer.PublicKey()); err != nil {
				t.Errorf("%s port %d: line %q doesn't match %s: %v", host, port, line, addr, err)
			}
		}
	}
}

// forward listens on IPv6 loopback and passes each connection to the test
// server on 127.0.0.1:to (the test servers publish their ports on IPv4
// loopback only). at is the port to listen on, 0 for any. It returns the
// port it listens on.
func forward(t *testing.T, at, to int) int {
	t.Helper()
	ln, err := net.Listen("tcp", fmt.Sprintf("[::1]:%d", at))
	if err != nil {
		t.Skipf("can't listen on IPv6 loopback: %v", err)
	}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		ln.Close()
		wg.Wait()
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				s, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", to))
				if err != nil {
					return
				}
				defer s.Close()
				done := make(chan struct{}, 2)
				go func() { _, _ = io.Copy(s, c); s.(*net.TCPConn).CloseWrite(); done <- struct{}{} }()
				go func() { _, _ = io.Copy(c, s); c.(*net.TCPConn).CloseWrite(); done <- struct{}{} }()
				<-done
				<-done
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// Each test server that takes a host and a port, reached at the IPv6
// loopback address, with the host given bare (as a server found on the
// network is saved) and in brackets (as one typed with a port once was).
func TestServersIPv6(t *testing.T) {
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
	smb := forward(t, 0, 1445)
	sftp := forward(t, 0, 2222)
	shell := forward(t, 0, 2226)
	nfs := forward(t, 0, 2050)
	ftp := forward(t, 0, 2121)
	// FTP's data connections go to the passive ports the server names, on
	// the address the control connection used.
	for p := 21000; p <= 21010; p++ {
		forward(t, p, p)
	}
	pem, err := os.ReadFile("../test/servers/keys/driveway")
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"::1", "[::1]"} {
		t.Run(host, func(t *testing.T) {
			pin := func(port int) string {
				t.Helper()
				hk, err := HostKey(host, port)
				if err != nil {
					t.Fatalf("host key at %s port %d: %v", host, port, err)
				}
				var h map[string]string
				_ = json.Unmarshal([]byte(hk), &h)
				if want := fmt.Sprintf("[::1]:%d ", port); !strings.HasPrefix(h["line"], want) {
					t.Fatalf("pinned line %q, want it to start %q", h["line"], want)
				}
				kh := filepath.Join(t.TempDir(), "known_hosts")
				_ = os.WriteFile(kh, []byte(h["line"]+"\n"), 0o600)
				return kh
			}
			set := func(id, backend string, m map[string]string) {
				t.Helper()
				m["host"] = host
				b, _ := json.Marshal(m)
				if err := SetRemote(id, backend, string(b)); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { RemoveRemote(id) })
				if err := Check(id); err != nil {
					t.Fatalf("%s at %s: %v", backend, host, err)
				}
			}
			// A file written, listed and read back: the whole path works, not only the greeting.
			roundTrip := func(id, base string) {
				t.Helper()
				dir := join(base, "v6-"+token())
				defer Delete(id, dir)
				if err := Upload(id, dir+"/a.txt", local(t, []byte("over IPv6"))); err != nil {
					t.Fatalf("upload: %v", err)
				}
				r, err := Open(id, dir+"/a.txt")
				if err != nil {
					t.Fatalf("open: %v", err)
				}
				got, _ := r.ReadAt(0, 100)
				r.Close()
				if string(got) != "over IPv6" {
					t.Fatalf("read back %q", got)
				}
			}

			set("t6-smb", "smb", map[string]string{"port": fmt.Sprint(smb), "user": "driveway", "pass": "driveway-test"})
			roundTrip("t6-smb", "share")

			set("t6-sftp", "sftp", map[string]string{"port": fmt.Sprint(sftp), "user": "driveway", "key_pem": string(pem),
				"known_hosts_file": pin(sftp), "shell_type": "none"})
			roundTrip("t6-sftp", "share")

			// The server copy dials on its own, with the same pinned key.
			set("t6-shell", "sftp", map[string]string{"port": fmt.Sprint(shell), "user": "driveway", "pass": "driveway-test",
				"known_hosts_file": pin(shell)})
			dir := "v6c-" + token()
			defer Delete("t6-shell", dir)
			if err := Upload("t6-shell", dir+"/a.txt", local(t, []byte("copy me"))); err != nil {
				t.Fatal(err)
			}
			if err := Copy("t6-shell", dir+"/a.txt", dir+"/b.txt"); err != nil {
				t.Fatalf("copy on the server: %v", err)
			}

			set("t6-nfs", "nfs", map[string]string{"port": fmt.Sprint(nfs), "export": "/"})
			roundTrip("t6-nfs", "")

			set("t6-ftp", "ftp", map[string]string{"port": fmt.Sprint(ftp), "user": "driveway", "pass": "driveway-test"})
			roundTrip("t6-ftp", "")
		})
	}
}

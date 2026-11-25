// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// fakeSSH is an SSH server in the test, for the answers the Docker servers
// never give: a refused session or exec, no SFTP, a shell that starts
// somewhere else, cp's exit codes, a connection that drops during cp, an rm
// that never ends or is refused. It logs every command it is asked to run,
// with the number of the connection it came on. cp and rm work on real
// files in dir (paths relative to it, as the shell's are to its home), so a
// test sees what is left behind, not only what was asked.
type fakeSSH struct {
	t     *testing.T
	addr  string // 127.0.0.1:port
	port  string
	known string // a known_hosts file pinning its key
	dir   string // where SFTP starts

	noSession bool         // refuse every session
	maxSess   int          // refuse sessions past this many on a connection (0: no limit)
	noShell   bool         // the probe gets an SFTP-only refusal
	pwd       string       // what the shell's pwd prints ("": dir)
	noSFTP    bool         // refuse the sftp subsystem
	refuseCp  bool         // refuse cp's exec request
	cpExit    int          // cp's exit status
	cpStderr  string       // and what it prints
	partial   bool         // a cp that fails or is cut off leaves half the copy behind
	dropOnCp  bool         // hang up the whole connection when cp starts
	hangRm    bool         // never finish rm
	rmExit    int          // rm's exit status; anything but 0 leaves the file
	silentAt  atomic.Int32 // from this connection on (1 for all), take it but never answer

	mu    sync.Mutex
	conns []net.Conn
	log   []string
}

// clientKey is the login key fakeSSH accepts besides the password.
var clientKey = func() ed25519.PrivateKey {
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return k
}()

func newFakeSSH(t *testing.T) *fakeSSH {
	_, hostPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(hostPriv)
	if err != nil {
		t.Fatal(err)
	}
	clientPub, _ := ssh.NewPublicKey(clientKey.Public())
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "driveway" && string(pass) == "driveway-test" {
				return nil, nil
			}
			return nil, fmt.Errorf("wrong password")
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() == "driveway" && string(k.Marshal()) == string(clientPub.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("unknown key")
		},
	}
	cfg.AddHostKey(signer)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSSH{t: t, addr: l.Addr().String(), dir: t.TempDir()}
	_, f.port, _ = net.SplitHostPort(f.addr)
	f.known = filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(f.addr)}, signer.PublicKey())
	if err := os.WriteFile(f.known, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		l.Close()
		f.mu.Lock()
		for _, c := range f.conns {
			c.Close()
		}
		f.mu.Unlock()
	})
	go func() {
		for {
			nc, err := l.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns = append(f.conns, nc)
			n := len(f.conns)
			f.mu.Unlock()
			if at := int(f.silentAt.Load()); at > 0 && n >= at {
				continue // closed when the test ends
			}
			go f.serve(nc, n, cfg)
		}
	}()
	return f
}

func (f *fakeSSH) serve(nc net.Conn, n int, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		nc.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	sessions := 0
	for nch := range chans {
		sessions++
		if nch.ChannelType() != "session" || f.noSession || (f.maxSess > 0 && sessions > f.maxSess) {
			_ = nch.Reject(ssh.Prohibited, "no sessions")
			continue
		}
		ch, creqs, err := nch.Accept()
		if err != nil {
			continue
		}
		go f.session(nc, n, ch, creqs)
	}
}

func (f *fakeSSH) session(nc net.Conn, n int, ch ssh.Channel, reqs <-chan *ssh.Request) {
	exit := func(status int) {
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(status)}))
		_ = ch.Close()
	}
	for req := range reqs {
		switch req.Type {
		case "exec":
			var p struct{ Command string }
			_ = ssh.Unmarshal(req.Payload, &p)
			f.mu.Lock()
			f.log = append(f.log, fmt.Sprintf("%d %s", n, p.Command))
			f.mu.Unlock()
			switch {
			case p.Command == "echo driveway-shell; pwd":
				_ = req.Reply(true, nil)
				if f.noShell {
					fmt.Fprint(ch.Stderr(), "This service allows sftp connections only.\n")
					exit(1)
					return
				}
				pwd := f.pwd
				if pwd == "" {
					pwd = f.dir
				}
				fmt.Fprintf(ch, "driveway-shell\n%s\n", pwd)
				exit(0)
			case strings.HasPrefix(p.Command, "cp "):
				if f.refuseCp {
					_ = req.Reply(false, nil)
					continue
				}
				_ = req.Reply(true, nil)
				// A cp that fails without f.partial fails before it writes.
				if (f.cpExit == 0 && !f.dropOnCp) || f.partial {
					w := shellWords(p.Command)
					data, err := os.ReadFile(f.path(w[len(w)-2]))
					if err != nil {
						fmt.Fprintf(ch.Stderr(), "cp: %v\n", err)
						exit(1)
						return
					}
					if f.partial {
						data = data[:len(data)/2]
					}
					_ = os.WriteFile(f.path(w[len(w)-1]), data, 0o600)
				}
				if f.dropOnCp {
					nc.Close()
					return
				}
				fmt.Fprint(ch.Stderr(), f.cpStderr)
				exit(f.cpExit)
			case strings.HasPrefix(p.Command, "rm "):
				_ = req.Reply(true, nil)
				if f.hangRm {
					continue // until the client closes the connection
				}
				if f.rmExit != 0 {
					fmt.Fprint(ch.Stderr(), "rm: cannot remove: Permission denied\n")
					exit(f.rmExit)
					return
				}
				w := shellWords(p.Command)
				_ = os.Remove(f.path(w[len(w)-1]))
				exit(0)
			default:
				_ = req.Reply(false, nil)
			}
		case "subsystem":
			var p struct{ Name string }
			_ = ssh.Unmarshal(req.Payload, &p)
			if p.Name != "sftp" || f.noSFTP {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			go func() {
				defer ch.Close()
				s, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(f.dir))
				if err == nil {
					_ = s.Serve()
				}
			}()
		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// path is where the shell's relative path rel is on this machine.
func (f *fakeSSH) path(rel string) string { return filepath.Join(f.dir, rel) }

// put makes the file rel in dir.
func (f *fakeSSH) put(rel, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.path(rel)), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(f.path(rel), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// holds checks that rel holds want, or with want "" that nothing is there.
func (f *fakeSSH) holds(t *testing.T, rel, want string) {
	t.Helper()
	b, err := os.ReadFile(f.path(rel))
	switch {
	case want == "" && !errors.Is(err, os.ErrNotExist):
		t.Fatalf("%s: %q, %v; want nothing there", rel, b, err)
	case want != "" && (err != nil || string(b) != want):
		t.Fatalf("%s holds %q (%v), want %q", rel, b, err, want)
	}
}

// shellWords splits a command as sh would, for the commands sftpCopy sends:
// bare words and single-quoted ones (an apostrophe in a name ends the
// quotes, comes escaped with a backslash, and the quotes start again).
func shellWords(cmd string) []string {
	var out []string
	var w strings.Builder
	word, quoted, escaped := false, false, false
	for _, r := range cmd {
		switch {
		case escaped:
			w.WriteRune(r)
			escaped = false
		case r == '\\' && !quoted:
			escaped, word = true, true
		case r == '\'':
			quoted, word = !quoted, true
		case r == ' ' && !quoted:
			if word {
				out = append(out, w.String())
				w.Reset()
				word = false
			}
		default:
			w.WriteRune(r)
			word = true
		}
	}
	if word {
		out = append(out, w.String())
	}
	return out
}

// commands is what the server was asked to run, "conn command" each.
func (f *fakeSSH) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

// remote sets up the server id for f, with the password unless params
// say otherwise.
func (f *fakeSSH) remote(t *testing.T, id string, extra map[string]string) {
	m := map[string]string{"host": "127.0.0.1", "port": f.port, "user": "driveway", "pass": "driveway-test",
		"known_hosts_file": f.known}
	for k, v := range extra {
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	b, _ := json.Marshal(m)
	if err := SetRemote(id, "sftp", string(b)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { RemoveRemote(id) })
}

func wantKind(t *testing.T, what string, err error, kind string) {
	t.Helper()
	if err == nil || !strings.HasPrefix(err.Error(), "["+kind+"]") {
		t.Fatalf("%s: %v, want [%s]", what, err, kind)
	}
}

// A copy with a login key only: the command cp runs is quoted for the shell,
// an apostrophe in a name included, and nothing else is run.
func TestServerCopyKeyLogin(t *testing.T) {
	f := newFakeSSH(t)
	block, err := ssh.MarshalPrivateKey(clientKey, "")
	if err != nil {
		t.Fatal(err)
	}
	f.pwd = f.dir + "/" // a trailing slash still names the same folder
	f.remote(t, "c-keycopy", map[string]string{"pass": "", "key_pem": string(pem.EncodeToMemory(block))})
	f.put("a dir/it's.txt", "copy me")
	if err := sftpCopy(ctx(), "c-keycopy", "a dir/it's.txt", "b.txt"); err != nil {
		t.Fatal(err)
	}
	f.holds(t, "b.txt", "copy me")
	got := f.commands()
	want := []string{"1 echo driveway-shell; pwd", `1 cp -p -- 'a dir/it'\''s.txt' 'b.txt'`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ran %q, want %q", got, want)
	}
}

// Settings that can't make a copy fail before anything is dialed, each with
// the category the app acts on: a bad key or stored password is an error,
// no pinned host key (or one that can't be read) means Files copies the
// ordinary way.
func TestServerCopySettings(t *testing.T) {
	f := newFakeSSH(t)
	f.remote(t, "c-badkey", map[string]string{"key_pem": "-----BEGIN OPENSSH PRIVATE KEY-----\nnot a key\n-----END OPENSSH PRIVATE KEY-----"})
	wantKind(t, "a key that can't be read", sftpCopy(ctx(), "c-badkey", "a", "b"), "other")

	f.remote(t, "c-badpass", nil)
	store.setQuiet("c-badpass", "pass", "!!") // not something obscure.Obscure makes
	wantKind(t, "a stored password that can't be revealed", sftpCopy(ctx(), "c-badpass", "a", "b"), "other")

	f.remote(t, "c-nopin", map[string]string{"known_hosts_file": ""})
	err := sftpCopy(ctx(), "c-nopin", "a", "b")
	wantKind(t, "no pinned host key", err, "unsupported")
	if !strings.Contains(err.Error(), "no pinned host key") {
		t.Errorf("no pinned host key: %v", err)
	}

	f.remote(t, "c-gonepin", map[string]string{"known_hosts_file": filepath.Join(t.TempDir(), "missing")})
	wantKind(t, "an unreadable known_hosts file", sftpCopy(ctx(), "c-gonepin", "a", "b"), "unsupported")

	if got := f.commands(); len(got) != 0 {
		t.Errorf("ran %q", got)
	}
}

// A server that can't be reached, or one whose key changed, fails the copy
// with the category Files' message is chosen by.
func TestServerCopyDial(t *testing.T) {
	// A port nothing listens on.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, closed, _ := net.SplitHostPort(l.Addr().String())
	l.Close()
	f := newFakeSSH(t)
	f.remote(t, "c-closed", map[string]string{"port": closed})
	wantKind(t, "a closed port", sftpCopy(ctx(), "c-closed", "a", "b"), "network")

	// No port: the SSH default. (The host doesn't resolve, so nothing on
	// this machine's own port 22 is contacted.)
	f.remote(t, "c-noport", map[string]string{"host": "driveway-test.invalid", "port": ""})
	wantKind(t, "an unknown host", sftpCopy(ctx(), "c-noport", "a", "b"), "network")

	// The server presents a key other than the pinned one.
	other := newFakeSSH(t)
	pin, _ := os.ReadFile(other.known)
	moved := filepath.Join(t.TempDir(), "known_hosts")
	_ = os.WriteFile(moved, []byte(strings.Replace(string(pin), other.port, f.port, 1)), 0o600)
	f.remote(t, "c-otherkey", map[string]string{"known_hosts_file": moved})
	err = sftpCopy(ctx(), "c-otherkey", "a", "b")
	wantKind(t, "a changed host key", err, "auth")
	if !strings.Contains(err.Error(), "key mismatch") {
		t.Errorf("a changed host key: %v", err)
	}
}

// Servers that log in but can't copy with cp the way Driveway needs say
// [unsupported], so Files copies the ordinary way, and nothing is removed:
// no session, no shell, no SFTP, a shell that starts elsewhere than SFTP
// (a chrooted NAS account), no third session, cp refused, or cp missing or
// not allowed.
func TestServerCopyUnsupported(t *testing.T) {
	cases := []struct {
		name string
		set  func(f *fakeSSH)
		ran  string // the last command the server saw
	}{
		{"no session", func(f *fakeSSH) { f.noSession = true }, ""},
		{"no shell", func(f *fakeSSH) { f.noShell = true }, "echo driveway-shell; pwd"},
		{"no sftp", func(f *fakeSSH) { f.noSFTP = true }, "echo driveway-shell; pwd"},
		{"chrooted sftp", func(f *fakeSSH) { f.pwd = "/volume1/homes/driveway" }, "echo driveway-shell; pwd"},
		{"two sessions only", func(f *fakeSSH) { f.maxSess = 2 }, "echo driveway-shell; pwd"},
		{"exec refused", func(f *fakeSSH) { f.refuseCp = true }, "cp "},
		{"no cp", func(f *fakeSSH) { f.cpExit, f.cpStderr = 127, "sh: cp: not found" }, "cp "},
		{"cp not executable", func(f *fakeSSH) { f.cpExit, f.cpStderr = 126, "sh: cp: Permission denied" }, "cp "},
		{"restricted shell", func(f *fakeSSH) { f.cpExit, f.cpStderr = 1, "rbash: cp: command not allowed" }, "cp "},
		{"sftp only", func(f *fakeSSH) { f.cpExit, f.cpStderr = 1, "This service allows sftp connections only." }, "cp "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeSSH(t)
			c.set(f)
			f.remote(t, "c-unsup", nil)
			wantKind(t, c.name, sftpCopy(ctx(), "c-unsup", "a.txt", "b.txt"), "unsupported")
			got := f.commands()
			last := ""
			if len(got) > 0 {
				last = strings.SplitN(got[len(got)-1], " ", 2)[1]
			}
			if !strings.HasPrefix(last, c.ran) || (c.ran == "" && last != "") {
				t.Errorf("ran %q, want the last to be %q", got, c.ran)
			}
			for _, cmd := range got {
				if strings.Contains(cmd, " rm ") {
					t.Errorf("removed something: %q", got)
				}
			}
		})
	}
}

// A cp that fails on the server (it couldn't finish writing) is reported
// by what went wrong, and its partial copy is removed over the same
// connection.
func TestServerCopyFails(t *testing.T) {
	f := newFakeSSH(t)
	f.cpExit, f.cpStderr, f.partial = 1, "cp: error writing 'b.txt': Permission denied", true
	f.remote(t, "c-cpfail", nil)
	f.put("a.txt", "the whole file")
	err := sftpCopy(ctx(), "c-cpfail", "a.txt", "b.txt")
	wantKind(t, "cp failed", err, "denied")
	if !strings.Contains(err.Error(), "cp exited 1: cp: error writing") {
		t.Errorf("cp failed: %v", err)
	}
	got := f.commands()
	if len(got) != 3 || got[2] != "1 rm -f -- 'b.txt'" {
		t.Fatalf("ran %q, want the partial copy removed on the same connection", got)
	}
	f.holds(t, "b.txt", "")
	f.holds(t, "a.txt", "the whole file")
}

// A connection that drops while cp runs (Wi-Fi lost, the server
// restarted) is a network failure, and the partial copy is removed over a
// new connection.
func TestServerCopyDropped(t *testing.T) {
	f := newFakeSSH(t)
	f.dropOnCp, f.partial = true, true
	f.remote(t, "c-cpdrop", nil)
	f.put("a.txt", "the whole file")
	err := sftpCopy(ctx(), "c-cpdrop", "a.txt", "b.txt")
	wantKind(t, "dropped during cp", err, "network")
	if !strings.Contains(err.Error(), "the connection dropped during cp") {
		t.Errorf("dropped during cp: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := f.commands()
		if len(got) == 3 && got[2] == "2 rm -f -- 'b.txt'" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ran %q, want the partial copy removed on a second connection", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.holds(t, "b.txt", "")
	f.holds(t, "a.txt", "the whole file")
}

// The tidying up after a failed cp is bounded too: an rm that never ends is
// cut off and the copy still fails with cp's own error, in seconds. The
// partial copy then stays on the server (the tidying up is best effort).
func TestServerCopyStuckRm(t *testing.T) {
	f := newFakeSSH(t)
	f.cpExit, f.cpStderr, f.hangRm, f.partial = 1, "cp: error writing 'b.txt': No space left on device", true, true
	f.remote(t, "c-stuckrm", nil)
	f.put("a.txt", "the whole file")
	start := time.Now()
	err := sftpCopy(ctx(), "c-stuckrm", "a.txt", "b.txt")
	took := time.Since(start)
	wantKind(t, "cp failed, rm stuck", err, "full")
	if took < 3*time.Second || took > 8*time.Second {
		t.Fatalf("took %v, want the 4 s limit on the tidying up", took)
	}
	if got := f.commands(); len(got) != 3 || !strings.HasPrefix(got[2], "1 rm ") {
		t.Fatalf("ran %q", got)
	}
	f.holds(t, "b.txt", "the whole file"[:7])
}

// A server that takes the connection and then never logs in can't hold a
// copy past its limit, nor the tidying up after a dropped cp past its own.
func TestServerCopyLoginStalls(t *testing.T) {
	within := func(t *testing.T, limit time.Duration, call func() error) error {
		t.Helper()
		done := make(chan error, 1)
		start := time.Now()
		go func() { done <- call() }()
		select {
		case err := <-done:
			if took := time.Since(start); took > limit {
				t.Fatalf("took %v, want under %v", took, limit)
			}
			return err
		case <-time.After(limit + 5*time.Second):
			t.Fatalf("still waiting after %v", limit+5*time.Second)
			return nil
		}
	}
	t.Run("copy", func(t *testing.T) {
		f := newFakeSSH(t)
		f.silentAt.Store(1)
		f.remote(t, "c-silent", nil)
		old := copyLimitTime
		copyLimitTime = 500 * time.Millisecond
		defer func() { copyLimitTime = old }()
		err := within(t, 2*time.Second, func() error { return sftpCopy(ctx(), "c-silent", "a.txt", "b.txt") })
		wantKind(t, "a login that never ends", err, "unsupported")
		if !strings.Contains(err.Error(), "took too long") {
			t.Errorf("a login that never ends: %v", err)
		}
	})
	t.Run("the call's end", func(t *testing.T) {
		f := newFakeSSH(t)
		f.silentAt.Store(1)
		f.remote(t, "c-silentcall", nil)
		c, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		err := within(t, 2*time.Second, func() error { return sftpCopy(c, "c-silentcall", "a.txt", "b.txt") })
		wantKind(t, "a login that outlasts the call", err, "unsupported")
		if !strings.Contains(err.Error(), "took too long") {
			t.Errorf("a login that outlasts the call: %v", err)
		}
	})
	t.Run("tidying up", func(t *testing.T) {
		f := newFakeSSH(t)
		f.dropOnCp = true
		f.silentAt.Store(2)
		f.remote(t, "c-silentrm", nil)
		err := within(t, 6*time.Second, func() error { return sftpCopy(ctx(), "c-silentrm", "a.txt", "b.txt") })
		wantKind(t, "dropped during cp, no new login", err, "network")
	})
}

// An rm the server refuses leaves the partial copy where it is, and the
// copy fails with cp's own error, not rm's.
func TestServerCopyRmRefused(t *testing.T) {
	f := newFakeSSH(t)
	f.cpExit, f.cpStderr, f.rmExit, f.partial = 1, "cp: error writing 'b.txt': No space left on device", 1, true
	f.remote(t, "c-rmrefused", nil)
	f.put("a.txt", "the whole file")
	err := sftpCopy(ctx(), "c-rmrefused", "a.txt", "b.txt")
	wantKind(t, "cp failed, rm refused", err, "full")
	if got := f.commands(); len(got) != 3 || got[2] != "1 rm -f -- 'b.txt'" {
		t.Fatalf("ran %q", got)
	}
	f.holds(t, "b.txt", "the whole file"[:7])
}

// The fake's shell words: what sftpCopy quotes comes back as it was.
func TestShellWords(t *testing.T) {
	got := shellWords(fmt.Sprintf(cpCommand, shellQuote("a dir/it's.txt"), shellQuote(`b\ 'c'.txt`)))
	want := []string{"cp", "-p", "--", "a dir/it's.txt", `b\ 'c'.txt`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q, want %q", got, want)
	}
}

// On the real SFTP server with a shell: cp into a folder that isn't there
// fails as not found, with nothing left behind.
func TestServerCopyMissingFolder(t *testing.T) {
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
	if err := SetRemote("c-cpmissing", "sftp", `{"host":"127.0.0.1","port":"2226","user":"driveway","pass":"driveway-test","known_hosts_file":"`+kh+`"}`); err != nil {
		t.Fatal(err)
	}
	defer RemoveRemote("c-cpmissing")
	dir := "c-" + token()
	defer Delete("c-cpmissing", dir)
	if err := Upload("c-cpmissing", dir+"/a.txt", local(t, []byte("copy me"))); err != nil {
		t.Fatal(err)
	}
	err = sftpCopy(ctx(), "c-cpmissing", dir+"/a.txt", dir+"/nowhere/b.txt")
	wantKind(t, "cp into a missing folder", err, "notfound")
	if !strings.Contains(err.Error(), "cp exited 1") {
		t.Errorf("cp into a missing folder: %v", err)
	}
}

// HostKey for a server that can't be reached returns no key and a network
// error; without a port it asks the SSH default.
func TestHostKeyUnreachable(t *testing.T) {
	out, err := HostKey("driveway-test.invalid", 0)
	if out != "" {
		t.Errorf("a key from nowhere: %s", out)
	}
	wantKind(t, "an unknown host", err, "network")
	f := newFakeSSH(t)
	port := 0
	fmt.Sscan(f.port, &port)
	out, err = HostKey("127.0.0.1", port)
	if err != nil {
		t.Fatal(err)
	}
	pin, _ := os.ReadFile(f.known)
	var h map[string]string
	_ = json.Unmarshal([]byte(out), &h)
	if h["line"] != strings.TrimSpace(string(pin)) || !strings.HasPrefix(h["fingerprint"], "SHA256:") {
		t.Errorf("host key %v, want the line %q", h, pin)
	}
}

// A slow TCP connection counts toward the limit: it is given no more than the
// limit, and the handshake only what is left after it.
func TestDialCountsTowardLimit(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	defer func(old time.Duration) { hostKeyLimit = old }(hostKeyLimit)
	hostKeyLimit = time.Second
	var given time.Duration
	defer func(old func(string, string, time.Duration) (net.Conn, error)) { dial = old }(dial)
	dial = func(network, addr string, timeout time.Duration) (net.Conn, error) {
		given = timeout
		time.Sleep(800 * time.Millisecond)
		return net.DialTimeout(network, addr, timeout)
	}
	start := time.Now()
	_, err = HostKey("127.0.0.1", l.Addr().(*net.TCPAddr).Port)
	took := time.Since(start)
	wantKind(t, "a slow connection to a silent server", err, "network")
	if given > hostKeyLimit || took > hostKeyLimit+400*time.Millisecond {
		t.Fatalf("the connection was given %v and the check took %v, want both within %v", given, took, hostKeyLimit)
	}
}

// A server that takes the connection and never speaks SSH stops the identity
// check at its limit, not after the TCP timeout alone (which a silent server
// never trips).
func TestHostKeySilentServer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	defer func(old time.Duration) { hostKeyLimit = old }(hostKeyLimit)
	hostKeyLimit = 500 * time.Millisecond
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := HostKey("127.0.0.1", l.Addr().(*net.TCPAddr).Port)
		done <- result{out, err}
	}()
	var out string
	select {
	case r := <-done:
		out, err = r.out, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("the identity check is still waiting on a silent server")
	}
	if out != "" {
		t.Fatalf("a key from a silent server: %s", out)
	}
	wantKind(t, "a silent server", err, "network")
}

// shellQuote is POSIX quoting; under a fish login shell a backslash inside
// single quotes is an escape, so `'a\'` does not end where sh's would and the
// rest of the line could run as a command. A name with a backslash never
// reaches the server's shell: Files copies it the ordinary way.
func TestServerCopyBackslashNameStaysOffTheShell(t *testing.T) {
	f := newFakeSSH(t)
	f.remote(t, "c-bslash", nil)
	for _, p := range [][2]string{{`evil\`, "b.txt"}, {"a.txt", `x\';touch pwned;'`}} {
		wantKind(t, p[0]+" -> "+p[1], sftpCopy(ctx(), "c-bslash", p[0], p[1]), "unsupported")
	}
	if got := f.commands(); len(got) != 0 {
		t.Errorf("the server saw %q, want no command", got)
	}
}

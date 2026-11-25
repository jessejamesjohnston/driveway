// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fshttp"
)

// TestRedactCases runs the cases the app's Logs.java is checked against too.
func TestRedactCases(t *testing.T) {
	b, err := os.ReadFile("../test/redact_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Cases []struct{ Why, In, Out string }
	}
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) < 30 {
		t.Fatalf("only %d cases", len(file.Cases))
	}
	for _, c := range file.Cases {
		if got := redact(c.In); got != c.Out {
			t.Errorf("%s:\n in:   %s\n got:  %s\n want: %s", c.Why, c.In, got, c.Out)
		}
		if got := redact(c.Out); got != c.Out {
			t.Errorf("%s: redacting twice changed it: %s", c.Why, got)
		}
	}
}

// TestEveryExportScrubs: each function the app can call that returns an
// error redacts it on the way out (defer scrub(&err) first thing), so no
// path through the engine hands the app a credential.
func TestEveryExportScrubs(t *testing.T) {
	fset := gotoken.NewFileSet()
	files, _ := filepath.Glob("*.go")
	n := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() || fn.Type.Results == nil {
				continue
			}
			if fn.Recv != nil && !ast.IsExported(receiverType(fn.Recv.List[0].Type)) {
				continue // not reachable from the app
			}
			res := fn.Type.Results.List
			last := res[len(res)-1]
			if id, ok := last.Type.(*ast.Ident); !ok || id.Name != "error" {
				continue
			}
			n++
			where := name + ": " + fn.Name.Name
			if len(last.Names) != 1 || last.Names[0].Name != "err" {
				t.Errorf("%s: its error result isn't named err", where)
				continue
			}
			first, ok := fn.Body.List[0].(*ast.DeferStmt)
			if !ok {
				t.Errorf("%s: doesn't start with defer scrub(&err)", where)
				continue
			}
			call, ok := first.Call.Fun.(*ast.Ident)
			arg, _ := first.Call.Args[0].(*ast.UnaryExpr)
			if !ok || call.Name != "scrub" || arg == nil || arg.X.(*ast.Ident).Name != "err" {
				t.Errorf("%s: doesn't start with defer scrub(&err)", where)
			}
		}
	}
	if n < 20 {
		t.Errorf("only %d exported functions return an error", n)
	}
}

func receiverType(e ast.Expr) string {
	if s, ok := e.(*ast.StarExpr); ok {
		e = s.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// TestTagRedacts: a tagged error keeps its kind and its cause for errors.Is,
// and loses the key.
func TestTagRedacts(t *testing.T) {
	raw := fmt.Errorf("Get \"https://api.example.com/x?api_key=K3Y\": %w", io.ErrUnexpectedEOF)
	err := classify(raw)
	if !strings.HasPrefix(err.Error(), "[network] ") || strings.Contains(err.Error(), "K3Y") {
		t.Errorf("got %q", err)
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Error("the cause is lost")
	}
	var nilErr error
	scrub(&nilErr)
	if nilErr != nil {
		t.Error("scrub made an error out of nil")
	}
}

// captured is what reached the log while a check ran: rclone's logger and
// Go's log package as they write it (redacted), and stderr as it was
// written, before the filter Android puts on it (filterFD).
type captured struct {
	mu                  sync.Mutex
	rclone, std, stderr bytes.Buffer
}

func (c *captured) all() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rclone.String() + "\n" + c.std.String() + "\n" + redact(c.stderr.String())
}

type lockedWriter struct {
	mu *sync.Mutex
	b  *bytes.Buffer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

// captureLogs collects everything logged until the check ends.
func captureLogs(t *testing.T) *captured {
	setup()
	c := &captured{}
	std, ok := log.Writer().(*redactWriter)
	if !ok {
		t.Fatalf("Go's log package writes to %T, not through redaction", log.Writer())
	}
	swap := func(r *redactWriter, w io.Writer) io.Writer {
		r.mu.Lock()
		defer r.mu.Unlock()
		old := r.w
		r.w = w
		return old
	}
	oldRclone := swap(rcloneLog, lockedWriter{&c.mu, &c.rclone})
	oldStd := swap(std, lockedWriter{&c.mu, &c.std})
	stopStderr := captureFD(t, 2, lockedWriter{&c.mu, &c.stderr})
	t.Cleanup(func() {
		stopStderr()
		swap(rcloneLog, oldRclone)
		swap(std, oldStd)
	})
	return c
}

// blockedNetwork stands in for the internet: rclone's HTTP requests go to a
// proxy on this machine that notes where each was going and closes it, and
// a name lookup fails, so nothing leaves the machine. It returns the hosts
// the requests were for.
func blockedNetwork(t *testing.T) func() []string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seen []string
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.SetDeadline(time.Now().Add(5 * time.Second))
			line, _ := bufio.NewReader(c).ReadString('\n')
			if f := strings.Fields(line); len(f) > 1 {
				mu.Lock()
				seen = append(seen, f[1])
				mu.Unlock()
			}
			c.Close()
		}
	}()
	ci := fs.GetConfig(context.Background())
	oldProxy, oldResolver := ci.HTTPProxy, net.DefaultResolver
	ci.HTTPProxy = "http://" + l.Addr().String()
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("the network is blocked in this check")
	}}
	fshttp.ResetTransport()
	t.Cleanup(func() {
		l.Close()
		ci.HTTPProxy, net.DefaultResolver = oldProxy, oldResolver
		fshttp.ResetTransport()
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// rawText is the error's text before redaction.
func rawText(err error) string {
	for {
		var c *cleanError
		if !errors.As(err, &c) {
			return err.Error()
		}
		err = c.err
	}
}

// TestPremiumizeKeyNeverLeaves: premiumize.me takes its API key in the
// address, so a failed request's error holds it. With the network blocked
// (no request leaves the machine), the error Check returns and everything
// logged meanwhile must not hold the key.
func TestPremiumizeKeyNeverLeaves(t *testing.T) {
	const key = "SECRETAPIKEY123"
	seen := blockedNetwork(t)
	logs := captureLogs(t)
	if err := SetRemote("redact-pm", "premiumizeme", `{"api_key":"`+key+`"}`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { RemoveRemote("redact-pm") })
	err := Check("redact-pm")
	if err == nil {
		t.Fatal("Check worked without a network")
	}
	if !strings.Contains(rawText(err), key) {
		t.Fatalf("the check doesn't reproduce the leak: %s", rawText(err))
	}
	if !strings.HasPrefix(err.Error(), "[network] ") || !strings.Contains(err.Error(), "https://www.premiumize.me/api/folder/list?***") {
		t.Errorf("lost what the app needs: %s", err)
	}
	// The same text through each way to the log.
	fs.Errorf(nil, "listing failed: %s", rawText(err))
	log.Printf("listing failed: %s", rawText(err))
	fmt.Fprintf(os.Stderr, "listing failed: %s\n", rawText(err))
	for _, s := range []string{err.Error(), logs.all()} {
		if strings.Contains(s, key) {
			t.Errorf("the key reached: %s", s)
		}
	}
	if !strings.Contains(logs.all(), "listing failed: [network] couldn't list files") {
		t.Errorf("the log lines are missing: %s", logs.all())
	}
	if got := seen(); len(got) == 0 || got[0] != "www.premiumize.me:443" {
		t.Errorf("requests went to %v, not only to the blocked proxy", got)
	}
}

// TestFilterFD: what is written to a filtered descriptor arrives redacted.
func TestFilterFD(t *testing.T) {
	if !filterable {
		t.Skip("only on Linux and Android")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	fd, err := dupFD(int(w.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if err := filterFD(fd); err != nil {
		t.Fatal(err)
	}
	out := os.NewFile(uintptr(fd), "filtered")
	fmt.Fprintln(out, `Get "https://www.premiumize.me/api/x?apikey=SECRET1": EOF`)
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if want := "Get \"https://www.premiumize.me/api/x?***\": EOF\n"; line != want {
		t.Errorf("got %q, want %q", line, want)
	}
}

// secretWords: a setting rclone marks Sensitive whose name says it holds a
// credential (the others it marks are user names, hosts, addresses and
// buckets, which the log keeps on purpose).
var secretWords = regexp.MustCompile(`(?i)` + secretName + `$`)

// TestSecretSettingsNeverLogged walks every service Driveway offers and
// every setting rclone marks as a password, or as sensitive and named like a
// credential. Each gets a value of its own; the service is set up and
// checked with the network blocked; none of those values (nor the scrambled
// form rclone keeps of a password) may be in the error or in the log.
func TestSecretSettingsNeverLogged(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	seen := blockedNetwork(t)
	logs := captureLogs(t)
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := strconv.Itoa(closed.Addr().(*net.TCPAddr).Port)
	closed.Close()
	var names []string
	for _, ri := range fs.Registry {
		if !hidden[ri.Name] { // the services left out aren't compiled in
			names = append(names, ri.Name)
		}
	}
	sort.Strings(names)
	type secret struct{ service, option, value string }
	var secrets []secret
	var errs []string
	checked, options, setFailed := 0, 0, 0
	var reached, leakedRaw []string
	for i, name := range names {
		ri, err := fs.Find(name)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]any{"host": "127.0.0.1", "port": closedPort}
		var mine []secret
		for _, o := range ri.Options {
			if !o.IsPassword && !(o.Sensitive && secretWords.MatchString(o.Name)) {
				// An account name gets the service as far as signing in
				// (Linkbox sends its password in the address).
				if o.Sensitive && strings.Contains(o.Name, "email") {
					m[o.Name] = "walk@example.com"
				} else if o.Sensitive && (o.Name == "user" || o.Name == "username" || o.Name == "login") {
					m[o.Name] = "walk"
				}
				continue
			}
			options++
			v := fmt.Sprintf("Zq%02dx%sQz", i, strings.ReplaceAll(o.Name, "_", ""))
			if strings.Contains(o.Help, "JSON blob") { // a browser sign-in's token
				access, refresh := v+"A", v+"R"
				m[o.Name] = `{"access_token":"` + access + `","token_type":"Bearer","refresh_token":"` + refresh + `","expiry":"2099-01-01T00:00:00Z"}`
				mine = append(mine, secret{name, o.Name + " (access)", access}, secret{name, o.Name + " (refresh)", refresh})
				continue
			}
			m[o.Name] = v
			mine = append(mine, secret{name, o.Name, v})
		}
		if len(mine) == 0 {
			continue
		}
		b, _ := json.Marshal(m)
		id := "redact-" + strings.ReplaceAll(name, " ", "-")
		err = SetRemote(id, name, string(b))
		if err != nil {
			setFailed++
		} else {
			// rclone keeps passwords scrambled: those must not show either.
			for _, s := range mine {
				if v, _ := store.GetValue(id, s.option); v != "" && v != s.value && !strings.Contains(s.option, " ") {
					mine = append(mine, secret{name, s.option + " (scrambled)", v})
				}
			}
			before := len(seen())
			done := make(chan error, 1)
			go func() { done <- Check(id) }()
			select {
			case err = <-done:
			case <-time.After(60 * time.Second):
				err = fmt.Errorf("%s: Check took over a minute", name)
				t.Error(err)
			}
			if len(seen()) > before {
				reached = append(reached, name)
			}
		}
		RemoveRemote(id)
		checked++
		if err != nil {
			errs = append(errs, err.Error())
			for _, s := range mine {
				if strings.Contains(rawText(err), s.value) {
					leakedRaw = append(leakedRaw, name)
					break
				}
			}
		}
		secrets = append(secrets, mine...)
	}
	all := strings.Join(errs, "\n") + "\n" + logs.all()
	for _, s := range secrets {
		if strings.Contains(all, s.value) {
			t.Errorf("%s's %s reached the error or the log", s.service, s.option)
		}
	}
	if checked < 30 || options < 40 || len(reached) < 20 {
		t.Errorf("only %d services and %d settings checked, %d reaching the network", checked, options, len(reached))
	}
	// The services whose own error held one of their values before
	// redaction: the check finds real leaks, not only clean errors.
	if len(leakedRaw) < 4 {
		t.Errorf("only %v put a value in their error before redaction", leakedRaw)
	}
	t.Logf("%d services, %d secret settings (%d values); %d refused their settings, %d sent a request (stopped at the "+
		"blocked proxy); %d put a value in their error before redaction: %s",
		checked, options, len(secrets), setFailed, len(reached), len(leakedRaw), strings.Join(leakedRaw, ", "))
}

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
	"unicode/utf8"

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
	const key = "made-up-premiumize-key"
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

// addressRows are addresses with a password typed in them that Go's url
// package can't read (or, without a scheme, its HTTP client can't use). Each
// holds MADEUP, which must never reach the error or the log.
var addressRows = []struct{ backend, url string }{
	{"webdav", "https://alice:pa ssMADEUP1@dav.example.com/x"},
	{"webdav", `https://alice:pa"MADEUP2@dav.example.com/x`},
	{"webdav", "https://alice:pa<MADEUP3@dav.example.com/x"},
	{"webdav", "https://alice:pa>MADEUP4@dav.example.com/x"},
	{"webdav", `https://alice:pa\MADEUP5@dav.example.com/x`},
	{"webdav", "https://alice:pa`MADEUP6@dav.example.com/x"},
	{"http", "https://alice:pa ssMADEUP8@files.example.com/"},
	{"webdav", "https://alice:hunterMADEUP/abc@dav.example.com/x"},
	{"webdav", "https://alice:hunterMADEUP?abc@dav.example.com/x"},
	{"webdav", "https://alice:hunterMADEUP#abc@dav.example.com/x"},
	{"webdav", "alice:MADEUPNS@dav.example.com/x"},
	{"webdav", "https://alice:pa%zzMADEUP@dav.example.com/x"},
}

// TestUnreadableAddressNeverLeaks puts each of addressRows straight into the
// settings, past SetRemote's own check, so the error path and the redaction
// are what stand between the password and the log: Check's error holds no
// part of the password, an address Go's url package refused as rclone
// connected is Driveway's own bad-settings sentence, the log holds none of
// it, and Go's raw text, which does, comes out of redact without it.
func TestUnreadableAddressNeverLeaks(t *testing.T) {
	seen := blockedNetwork(t)
	logs := captureLogs(t)
	settings := 0
	for i, r := range addressRows {
		id := fmt.Sprintf("redact-addr-%d", i)
		mu.Lock()
		dropRemote(id)
		store.DeleteSection(id)
		store.setQuiet(id, "type", r.backend)
		store.setQuiet(id, "url", r.url)
		mu.Unlock()
		err := Check(id)
		RemoveRemote(id)
		if err == nil {
			t.Errorf("%s: Check worked", r.url)
			continue
		}
		if s := err.Error(); strings.Contains(s, "MADEUP") || strings.Contains(s, "hunter") {
			t.Errorf("%s: the error holds the password: %s", r.url, s)
		}
		raw := err.Error()
		var ae *addressError
		if errors.As(err, &ae) {
			settings++
			if want := "[badsettings] bad settings: an address in the settings can't be read"; err.Error() != want {
				t.Errorf("%s: got %q, want %q", r.url, err, want)
			}
			raw = ae.err.Error()
			if !strings.Contains(raw, "MADEUP") {
				t.Errorf("%s: Go's text doesn't reproduce the leak: %s", r.url, raw)
			}
		} else {
			var ce *cleanError
			if errors.As(err, &ce) {
				for e := ce.err; e != nil; e = errors.Unwrap(e) {
					raw = e.Error()
					if _, ok := e.(*cleanError); !ok {
						break
					}
				}
			}
		}
		if got := redact(raw); strings.Contains(got, "MADEUP") || strings.Contains(got, "hunter") {
			t.Errorf("%s: redact leaves the password in Go's text: %s", r.url, got)
		}
		fs.Errorf(nil, "check failed: %s", raw)
	}
	// All but the address without a scheme, which Go reads (its scheme is
	// "alice") and the HTTP client refuses later.
	if settings != len(addressRows)-1 {
		t.Errorf("%d of %d were bad-settings errors", settings, len(addressRows))
	}
	if all := logs.all(); strings.Contains(all, "MADEUP") || strings.Contains(all, "hunter") {
		t.Errorf("the password reached the log: %s", all)
	}
	if got := seen(); len(got) != 0 {
		t.Errorf("requests went out: %v", got)
	}
}

// TestRedactIsLinear: a server's answer can put a megabyte of text in an
// error (rclone quotes a failed request's body), so every shape must take
// time in proportion to its length. The bound is generous.
func TestRedactIsLinear(t *testing.T) {
	for _, shape := range []string{"a.", "a-", "x-x-", "a:", "a@", "a=", "a ", "a/", "%2", `\"`, "https://", `h:\/\/a\/`, "key", "x-a"} {
		big := strings.Repeat(shape, 1_000_000/len(shape))
		t0 := time.Now()
		redact(big)
		if d := time.Since(t0); d > 5*time.Second {
			t.Errorf("a megabyte of %q took %v", shape, d)
		}
	}
	// One long value inside each rule's own frame (LogsTest checks the
	// app's patterns don't overflow the stack on these).
	run := strings.Repeat(`a\`, 250_000) + strings.Repeat("b", 500_000)
	for _, f := range [][2]string{{`"token":"`, `"`}, {`invalid port "`, `" after host`}, {`https:\/\/`, ""},
		{`token="`, `"`}, {"Cookie: ", ""}, {"https://alice:p s", "@host/"}, {"<password>", "</password>"}} {
		t0 := time.Now()
		redact(f[0] + run + f[1])
		if d := time.Since(t0); d > 5*time.Second {
			t.Errorf("a megabyte inside %q took %v", f[0], d)
		}
	}
}

// TestLogLinesAreCut: what reaches the log is cut to maxLogLine characters
// (the unit the app's Logs.MAX counts), after redaction, and says so.
func TestLogLinesAreCut(t *testing.T) {
	var b bytes.Buffer
	w := &redactWriter{w: &b}
	long := "listing failed: " + strings.Repeat("wörd ", 4000) + "token=SECRETTAIL\n"
	if _, err := w.Write([]byte(long)); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if utf8.RuneCountInString(got) > maxLogLine+40 || utf8.RuneCountInString(got) < maxLogLine || !strings.HasPrefix(got, "listing failed: wörd") || !strings.Contains(got, "[cut:") ||
		strings.Contains(got, "SECRETTAIL") || !strings.HasSuffix(got, "\n") || !utf8.ValidString(got) {
		t.Errorf("not cut as it should be (%d bytes): ...%s", len(got), got[len(got)-60:])
	}
	b.Reset()
	if _, err := w.Write([]byte("short\n")); err != nil || b.String() != "short\n" {
		t.Errorf("a short line changed: %q", b.String())
	}
}

// notSecret are settings rclone marks sensitive that name or locate an
// account rather than unlock it, which the log keeps on purpose (a person
// reading it needs them), each with why.
var notSecret = map[string]string{
	"user": "a user name", "username": "a user name", "email": "an account's email address",
	"apple_id": "an account name", "account": "an account name", "cloud_name": "an account name",
	"host": "a host: the address rules apply", "url": "an address: the address rules apply",
	"api_url": "an address: the address rules apply", "namenode": "a host", "domain": "a Windows domain",
	"spn": "a service name", "service_principal_name": "a service name", "namespace": "a storage namespace",
	"compartment": "a cloud compartment's id", "project_number": "a cloud project's number",
	"user_project": "a cloud project's id", "tenant": "a directory's name or id", "tenant_id": "a directory's id",
	"tenant_domain": "a directory's domain", "account_id": "an account's id", "user_id": "a user's id",
	"client_id": "an app's public id", "app_id": "an app's public id", "client_uid": "an app session's public id",
	"application_credential_id":   "the id of a credential, not the credential",
	"application_credential_name": "the name of a credential, not the credential",
	"access_key_id":               "the id of a key, not the key (the secret has its own setting)",
	"sse_kms_key_id":              "the id of a key, not the key", "sse_customer_key_md5": "a checksum of a key, not the key",
	"msi_client_id": "a managed identity's id", "msi_object_id": "a managed identity's id",
	"msi_mi_res_id": "a managed identity's id", "impersonate": "the account to act as",
	"impersonate_admin": "the account to act as", "device_id": "a device's id", "drive_id": "a drive's id",
	"team_drive": "a shared drive's id", "root_folder_id": "a folder's id", "root_folder_slug": "a folder's id",
	"root_id": "a folder's id", "base_folder_uuid": "a folder's id", "deleted_id": "a folder's id",
	"workspace_id": "a workspace's id",
}

// TestSecretNamesFromRclone: every setting rclone marks as a password, or as
// sensitive, in every service compiled in, is either a name the redactors
// hide (secretName) or listed in notSecret with why. A setting a new
// version of rclone adds fails here until someone decides which it is. Each
// hidden name is also in a shared case, so the app's redactor is checked
// against the same list.
func TestSecretNamesFromRclone(t *testing.T) {
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
	stillThere := map[string]bool{}
	hiddenNames := map[string]bool{}
	for _, ri := range fs.Registry {
		if hidden[ri.Name] {
			continue
		}
		for _, o := range ri.Options {
			if !o.IsPassword && !o.Sensitive {
				continue
			}
			if why, ok := notSecret[o.Name]; ok {
				stillThere[o.Name] = true
				if o.IsPassword {
					t.Errorf("%s's %s is a password, but notSecret says %q", ri.Name, o.Name, why)
				}
				continue
			}
			hiddenNames[o.Name] = true
			if got := redact(o.Name + "=MADEUPVALUE"); got != o.Name+"=***" {
				t.Errorf("%s's %s isn't hidden (%s): add it to secretName, or to notSecret with why", ri.Name, o.Name, got)
			}
		}
	}
	for name := range notSecret {
		if !stillThere[name] {
			t.Errorf("notSecret lists %s, which no service has any more", name)
		}
	}
	for name := range hiddenNames {
		re := regexp.MustCompile(`(?:^|[^a-z0-9_.-])` + regexp.QuoteMeta(name) + `=MADEUP\S*`)
		found := false
		for _, c := range file.Cases {
			found = found || re.MatchString(c.In) && !re.MatchString(c.Out)
		}
		if !found {
			t.Errorf("no shared case hides %s=", name)
		}
	}
	if len(hiddenNames) < 40 {
		t.Errorf("only %d secret settings found", len(hiddenNames))
	}
}

// TestKeyAcrossLines: a private key's block written a line at a time (as
// the log writers receive it) loses every line between its first and last.
func TestKeyAcrossLines(t *testing.T) {
	var b bytes.Buffer
	w := &redactWriter{w: &b}
	for _, l := range []string{"can't use this key:\n", "-----BEGIN RSA PRIVATE KEY-----\n", "MADEUPKEYLINE1\n",
		"MADEUPKEYLINE2\n", "-----END RSA PRIVATE KEY----- (rejected)\n", "next line\n"} {
		if _, err := w.Write([]byte(l)); err != nil {
			t.Fatal(err)
		}
	}
	want := "can't use this key:\n-----BEGIN RSA PRIVATE KEY-----***\n***\n***\n***-----END RSA PRIVATE KEY----- (rejected)\nnext line\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
	// A block of any length is blanked to its end (an 8192-bit key is
	// about a hundred lines; this one is four hundred), PEM headers and
	// blank lines included.
	var l lineRedactor
	l.line("-----BEGIN RSA PRIVATE KEY-----\n")
	for i, kl := range append([]string{"Proc-Type: 4,ENCRYPTED\n", "\n"}, strings.Split(strings.Repeat("MADEUPKEYLINE+/=\n", 400), "\n")...) {
		if got := l.line(kl); got != "***\n" && got != "***" {
			t.Fatalf("line %d of a long block reached the log: %q", i, got)
		}
	}
	// A block that never ends stops at the first line that can't be in one.
	if got := l.line("later line\n"); got != "later line\n" {
		t.Errorf("an unended block still blanks the log: %q", got)
	}
}

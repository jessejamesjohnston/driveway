// SPDX-License-Identifier: Apache-2.0

package engine

// The address rule, proven by generation rather than by examples. A user
// name and password typed into an address, with a password that holds a /
// ? or #, can read to Go's url package as a host and a port followed by a
// folder, and Go's errors then repeat the password. Hand-picked cases kept
// missing a neighbor of the shape they covered, so these checks build every
// combination of user name, password, host and folder shapes, put each into
// every address setting of every service compiled in (found from rclone's
// catalog, never listed by hand), and check what a person and the log would
// see:
//   - TestAddressMatrix: the address check (CheckSettings) on all of them,
//     and the redactor alone on Go's own errors for every generated address;
//   - TestAddressMatrixNetwork: SetRemote and a connection attempt, with the
//     network cut, for a deterministic pairwise sample of the accepted values
//     (DRIVEWAY_MATRIX_FULL=1: every one that keeps the password in an
//     address, which only the HTTP kind's and the proxies' own sign-ins do);
//   - TestRedactGenJSON writes the redactor's inputs and outputs for the app's
//     LogsTest, so the app's redactor is checked against the same values.
//
// Each password with content holds a marker (genMarkers): after the digits
// where it starts with digits (2024/QZQb), and elsewhere on both sides of
// whatever character makes it hard, so a partial leak counts as a leak.
// The look-alike shapes (a full-width @ or colon, and the like, where a
// separator goes) carry the marker in the user name, host or path instead.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/fshttp"
)

// genMarkers: text that is part of a generated password and of nothing else.
var genMarkers = []string{"QZQ", "73915062"}

func hasMarker(s string) bool {
	for _, m := range genMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

type genShape struct{ name, v string }

// noPass: a user name alone, with no colon.
const noPass = "\x00"

var (
	genUsers = []genShape{
		{"plain", "alice"}, {"dotted", "alice.smith"}, {"email", "alice@example.com"},
		{"punct", "a-b_c+d"}, {"empty", ""}, {"encoded", "al%40ice"}, {"unicode", "jürgen"},
		// Look-alikes (genLookalike): a full-width or modifier-letter colon
		// where the colon before a password goes, so the "user name" holds
		// the password.
		{"fullwidth:", "alice\uff1a2024/QZQd"}, {"modifier:", "alice\ua789/QZQd"},
	}
	genPasswords = []genShape{
		{"none", noPass}, {"empty", ""}, {"plain", "QZQa1"}, {"digits", "73915062"},
		{"digits/", "2024/QZQb"}, {"digits//", "2024/QZQb/QZQc"}, {"/start", "/QZQb"}, {"//start", "//QZQb"},
		{"mid/", "QZQa/QZQb"}, {"digits?", "2024?QZQb"}, {"?start", "?QZQb"}, {"mid?", "QZQa?QZQb"},
		{"digits/?", "2024/QZQb?QZQc"}, {"digits#", "2024#QZQb"}, {"#start", "#QZQb"}, {"mid#", "QZQa#QZQb"},
		{"mid@", "QZQa@QZQb"}, {"mid/@", "QZQa/QZQb@QZQc"}, {"digits@", "2024@QZQb"}, {"digits/@", "2024/QZQb@QZQc"}, {"mid:", "QZQa:QZQb"},
		{"digits:", "2024:QZQb"}, {"space", "QZQa QZQb"}, {"digits/space", "2024/QZQb QZQc"},
		{"quote", `QZQa"QZQb`}, {"squote", "QZQa'QZQb"}, {"percent", "QZQa%QZQb"}, {"digits%", "50%QZQb"},
		{"backslash", `QZQa\QZQb`}, {"encoded", "QZQa%2FQZQb"}, {"digits-encoded", "2024%2FQZQb"},
		{"long", strings.Repeat("QZQa", 50) + "/" + strings.Repeat("QZQb", 50)},
		{"digits/long", "2024/" + strings.Repeat("QZQb", 60)},
		// Look-alikes in a password: a full-width @ and a full-width solidus.
		{"fullwidth@", "2024/QZQb\uff20QZQc"}, {"fullwidth/", "2024\uff0fQZQb"},
	}
	// Hosts nothing answers at: documentation names and addresses
	// (RFC 2606, 5737, 3849), never the local test servers' ports.
	genHosts = []genShape{
		{"name", "nas"}, {"name:port", "nas:5006"}, {"dotted", "h.example.com"}, {"dotted:port", "h.example.com:5006"},
		{"ipv4", "192.0.2.10"}, {"ipv4:port", "192.0.2.10:5006"}, {"ipv6", "[2001:db8::10]"}, {"ipv6:port", "[2001:db8::10]:5006"},
		// An international name, which must keep working.
		{"idn", "b\u00fccher.example"},
		// Look-alikes: a whole user name and password ahead of the host,
		// joined to it by a full-width or small @ after a digits or empty
		// port, so the generated user name and password can be empty.
		{"fullwidth@login", "alice:2024/QZQe\uff20nas"}, {"small@login", "alice:/QZQe\ufe6bh.example.com"},
	}
	genPaths = []genShape{
		{"none", ""}, {"plain", "/dav"}, {"at", "/f@x/dav"}, {"colon", "/a:b/dav"},
		{"email-folder", "/remote.php/dav/files/alice@example.com/"}, {"encoded", "/files/x%40y/"},
		// Folder names in other scripts, which must keep working.
		{"unicode", "/Fotos/\u65e5\u672c/Gr\u00f6\u00dfe/"},
		// A look-alike: a full-width @ in a folder, which after a port
		// reads as a password and a host.
		{"fullwidth@", "/QZQf\uff20h.example.com/"},
		// Folders named with full-width punctuation (会议：2024, なぜ？,
		// 第＃1, 上／下, why⁉), which are legitimate and hold no marker:
		// every user name, password and host shape meets them.
		{"fullwidth-folder", "/\u4f1a\u8bae\uff1a2024/\u306a\u305c\uff1f/\u7b2c\uff031/\u4e0a\uff0f\u4e0b/why\u2049/"},
	}
)

// genSetting is one address setting of one service.
type genSetting struct {
	ri       *fs.RegInfo
	name     string // the option's name
	hostOnly bool
	login    bool // the service has User name and Password settings
}

func (s genSetting) String() string { return s.ri.Name + "." + s.name }

// genSettings: every address setting of every service in rclone's catalog
// (the ones the app offers and the ones it hides alike).
func genSettings(t *testing.T) []genSetting {
	var out []genSetting
	for _, ri := range fs.Registry {
		for _, o := range ri.Options {
			if isAddress(o) {
				out = append(out, genSetting{ri, o.Name, hostName.MatchString(o.Name), hasOption(ri, "user") && hasOption(ri, "pass")})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	if len(out) < 40 {
		t.Fatalf("only %d address settings in the catalog", len(out))
	}
	return out
}

// intlRow is a legitimate address whose folder or host is named in another
// script, with the punctuation those names are written with: a full-width
// colon, question mark, number sign or solidus (the usual way to write
// those marks in a Chinese or Japanese file name, where Windows bans the
// ASCII ones), the exclamation-question marks, dot forms in a folder. It
// holds no @ and no user name or password, so it must be accepted as typed
// and reach its server. SFTP's address setting is a host alone (the folder
// is opened in Files), so its rows are international host names.
type intlRow struct{ backend, setting, value, host string }

func intlRows() []intlRow {
	const (
		colon  = "会议：2024" // 会议：2024
		why    = "なぜ？"     // なぜ？
		number = "第＃1"     // 第＃1
		slash  = "上／下"     // 上／下
		marks  = "why⁉"    // why⁉
		marks2 = "what⁈"   // what⁈
		dots   = "終わり。"    // 終わり。
	)
	var rows []intlRow
	add := func(backend, setting, host string, values ...string) {
		for _, v := range values {
			rows = append(rows, intlRow{backend, setting, v, host})
		}
	}
	add("webdav", "url", "nas.example",
		"https://nas.example/dav/"+colon+"/",
		"https://nas.example/dav/"+why+"/",
		"https://nas.example/dav/"+number+"/",
		"https://nas.example/dav/"+slash+"/",
		"https://nas.example/dav/"+marks+"/",
		"https://nas.example/dav/"+marks2+"/",
		"https://nas.example/dav/"+dots+"/",
		"https://nas.example/dav/Notes…/",
		"https://nas.example:5006/dav/"+colon+"/"+why+"/"+number+"/",
		"https://nas.example/remote.php/dav/files/alice/"+colon+" "+slash+"/",
		"https://nas.example/dav/"+colon+"/?x=1",
	)
	add("webdav", "url", "192.0.2.10", "http://192.0.2.10:8080/dav/"+colon+"/"+marks+"/")
	add("webdav", "url", "例え.example", "https://例え.example/dav/"+colon+"/")
	add("webdav", "url", "дача.example", "https://дача.example/dav/Фото/")
	add("webdav", "url", "bücher.example", "https://bücher.example/dav/Größe/"+why+"/")
	add("http", "url", "files.example",
		"https://files.example/pub/"+colon+"/",
		"https://files.example/pub/"+why+number+"/",
		"https://files.example/pub/"+slash+"/"+marks2+"/",
	)
	add("http", "url", "例え.example", "https://例え.example/pub/写真/")
	add("s3", "endpoint", "minio.example", "https://minio.example:9000/"+colon)
	add("s3", "endpoint", "例え.example", "https://例え.example:9000")
	add("s3", "endpoint", "minio.bücher.example", "minio.bücher.example:9000")
	add("sftp", "host", "例え.example", "例え.example")
	add("sftp", "host", "дача.example", "дача.example")
	return rows
}

// intlTwin: the row with each of its punctuation characters in a name
// swapped for a plain letter (字). The two must get the same verdict: the
// punctuation changes nothing.
func intlTwin(v string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune("：？＃／⁉⁈。…", r) {
			return '字'
		}
		return r
	}, v)
}

// TestInternationalFolders: every legitimate row is accepted, kept byte for
// byte (a full-width colon is not a colon: turning one into the other names
// a different folder), and gets the verdict its twin gets.
func TestInternationalFolders(t *testing.T) {
	refused := 0
	rows := intlRows()
	for _, r := range rows {
		ri, err := fs.Find(r.backend)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]any{r.setting: r.value}
		v := checkSettings(ri, m)
		tw := map[string]any{r.setting: intlTwin(r.value)}
		vt := checkSettings(ri, tw)
		if v.Reason != "" {
			refused++
			t.Errorf("%s.%s %q: refused (%s)", r.backend, r.setting, r.value, v.Reason)
			continue
		}
		if got := fmtValue(m[r.setting]); got != r.value {
			t.Errorf("%s.%s %q: kept as %q", r.backend, r.setting, r.value, got)
		}
		if vt.Reason != "" || intlTwin(fmtValue(m[r.setting])) != fmtValue(tw[r.setting]) {
			t.Errorf("%s.%s %q: its twin reads differently (%q)", r.backend, r.setting, r.value, vt.Reason)
		}
	}
	t.Logf("%d legitimate international addresses: %d accepted, %d refused", len(rows), len(rows)-refused, refused)
}

// genValue is one generated address and the shapes it was made of.
type genValue struct {
	user, pass, host, path genShape
	scheme                 bool // with https://
	v                      string
}

// secret: the value holds a marker, in its password or, for the look-alike
// shapes that stand where a separator goes, in its user name, host or path.
func (g genValue) secret() bool { return hasMarker(g.v) }

// genLookalikes: the look-alike characters the shapes above put in a value,
// listed here on their own so the test doesn't trust the check's own list.
const genLookalikes = "\uff20\ufe6b\uff1a\ua789\uff0f\uff1f\uff03\u2049"

func genLookalike(v string) bool { return strings.ContainsAny(v, genLookalikes) }

// genValues: every combination, with and without a scheme.
func genValues() []genValue {
	var out []genValue
	for _, scheme := range []bool{true, false} {
		for _, u := range genUsers {
			for _, p := range genPasswords {
				for _, h := range genHosts {
					for _, pa := range genPaths {
						ui := ""
						switch {
						case p.v != noPass:
							ui = u.v + ":" + p.v + "@"
						case u.v != "":
							ui = u.v + "@"
						}
						v := ui + h.v + pa.v
						if scheme {
							v = "https://" + v
						}
						out = append(out, genValue{u, p, h, pa, scheme, v})
					}
				}
			}
		}
	}
	return out
}

// genFailures counts failures by kind and shows the first few of each.
type genFailures struct {
	mu     sync.Mutex
	counts map[string]int
	shown  map[string]int
	tally  map[string]int // what the look-alike values came to (not failures)
}

func (f *genFailures) count(kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tally == nil {
		f.tally = map[string]int{}
	}
	f.tally[kind]++
}

func (f *genFailures) add(t *testing.T, kind, format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.counts == nil {
		f.counts, f.shown = map[string]int{}, map[string]int{}
	}
	f.counts[kind]++
	if f.shown[kind] < 4 {
		f.shown[kind]++
		t.Errorf("%s: "+format, append([]any{kind}, args...)...)
	}
}

func (f *genFailures) report(t *testing.T) {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	var kinds []string
	for k, n := range f.counts {
		kinds = append(kinds, fmt.Sprintf("%s %d", k, n))
		total += n
	}
	sort.Strings(kinds)
	if total > 0 {
		t.Errorf("%d failing cases: %s", total, strings.Join(kinds, ", "))
	}
}

// matrixRun is the outcome of the check on one setting, value and field
// state.
type matrixRun struct {
	set    genSetting
	val    genValue
	fields bool // User name and Password already hold a login
	params map[string]string
	// dangerous: accepted, and the password is still in an address.
	dangerous bool
}

func (r matrixRun) json() string {
	return paramsJSON(r.set.ri, r.params, loginInAddress[r.set.String()] != "")
}

// paramsJSON is params for SetRemote, with a made-up value for each required
// setting without a default that the form would have asked for: a password
// (rclone ends the process on revealing an empty one, as NetStorage's
// secret), or an address (FTP's server, which it needs before it tries its
// proxy). With signIn, also every other password setting and the user name,
// so a service that won't try a connection without them (FTP, Swift) does:
// for a canary, and for a setting whose own sign-in stays in the address,
// where the fields move nothing.
func paramsJSON(ri *fs.RegInfo, params map[string]string, signIn bool) string {
	m := map[string]string{}
	for _, o := range ri.Options {
		switch {
		case signIn && o.IsPassword:
			m[o.Name] = "fillerpw7"
		case signIn && o.Name == "user":
			m[o.Name] = "filleruser"
		case !o.Required || fmt.Sprint(o.Default) != "":
		case o.IsPassword:
			m[o.Name] = "fillerpw7"
		case isAddress(o) && hostName.MatchString(o.Name):
			// FTP's server, so a connection through its proxy is tried.
			m[o.Name] = "filler.example.com"
		case isAddress(o):
			m[o.Name] = "https://filler.example.com/"
		}
	}
	for k, v := range params {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// runMatrix runs the check on every combination and returns the accepted
// runs whose value holds a password.
func runMatrix(t *testing.T, fails *genFailures) (accepted []matrixRun, refused []matrixRun, n int) {
	settings := genSettings(t)
	values := genValues()
	for _, s := range settings {
		for _, val := range values {
			for _, fields := range []bool{false, true} {
				if fields && !s.login {
					continue
				}
				n++
				params := map[string]string{s.name: val.v}
				if fields {
					params["user"], params["pass"] = "bob", "fieldpw9"
				}
				r := matrixRun{set: s, val: val, fields: fields, params: params}
				m := map[string]any{}
				for k, v := range params {
					m[k] = v
				}
				v := checkSettings(s.ri, m)
				if genLookalike(val.v) {
					kind := "look-alike without a marker"
					if val.secret() {
						kind = "look-alike with a marker"
					}
					if v.Reason != "" {
						fails.count(kind + ": refused")
					} else {
						fails.count(kind + ": accepted")
					}
				}
				if v.Reason != "" {
					// What the app shows and SetRemote returns: Driveway's
					// own words, never the address.
					if txt := (&settingsError{v.Field, v.Reason}).Error(); hasMarker(txt) || hasMarker(v.Field) {
						fails.add(t, "refusal repeats the password", "%s %q: %s", s, val.v, txt)
					}
					if val.secret() {
						refused = append(refused, r)
					}
					continue
				}
				checkAccepted(t, fails, &r, m, v)
				if val.secret() {
					accepted = append(accepted, r)
				}
			}
		}
	}
	return accepted, refused, n
}

// checkAccepted: what an accepted value comes to.
func checkAccepted(t *testing.T, fails *genFailures, r *matrixRun, m map[string]any, v verdict) {
	s, val := r.set, r.val
	exception := loginInAddress[s.String()] != ""
	addr := fmtValue(m[s.name])
	// A value with a look-alike and a marker is refused in every setting,
	// the sign-in exceptions too: the services' errors write the look-alike
	// percent-encoded, where no redactor can tell it from a folder's name.
	// (Decided from the value's shapes, not from the check's own rule.)
	if genLookalike(val.v) && val.secret() {
		fails.add(t, "accepted with a look-alike character", "%s %q (fields %v)", s, val.v, r.fields)
	}
	if hasMarker(addr) {
		r.dangerous = true
		if !exception {
			fails.add(t, "accepted with the password left in the address", "%s %q (fields %v) -> %q", s, val.v, r.fields, addr)
		}
	}
	// A login moved to User name and Password is the one typed, whole:
	// never part of it, never a piece of the host.
	switch v.Login {
	case loginMoved, loginMovedUser:
		wantUser, err1 := url.PathUnescape(val.user.v)
		wantPass, err2 := url.PathUnescape(val.pass.v)
		gotUser, gotPass := fmtValue(m["user"]), fmtValue(m["pass"])
		if err1 != nil || err2 != nil || gotUser != wantUser ||
			(v.Login == loginMoved && gotPass != wantPass) || (v.Login == loginMovedUser && val.pass.v != noPass) {
			fails.add(t, "a login moved in part", "%s %q -> user %q pass %q (%s)", s, val.v, gotUser, gotPass, v.Login)
		}
	}
}

// genTexts: the errors Go writes about an address: its parse error, or a
// request's error as the HTTP client writes it (the address whole, and with
// Go's own *** for a password it found).
func genTexts(v string) []string {
	u, err := url.Parse(v)
	if err != nil {
		return []string{err.Error(), "[badsettings] couldn't connect: " + err.Error()}
	}
	out := []string{
		(&url.Error{Op: "Propfind", URL: u.String(), Err: io.ErrUnexpectedEOF}).Error(),
		"[network] couldn't list files: " + (&url.Error{Op: "Get", URL: u.Redacted(), Err: io.ErrUnexpectedEOF}).Error(),
	}
	// As the HTTP client sends it (it drops an empty port, and with it the
	// colon a password followed).
	if req, err := http.NewRequest("PROPFIND", v, nil); err == nil {
		out = append(out, (&url.Error{Op: "Propfind", URL: req.URL.Redacted(), Err: io.ErrUnexpectedEOF}).Error())
	}
	return append(out, looseTexts(v)...)
}

// looseTexts: the address as a service may rebuild it and quote it, with one
// slash after the scheme (Koofr's client joins its paths into
// https:/word@host), with // and no scheme, and with no scheme at all.
// Only for addresses without a space, a quote or a backslash, which no
// service rebuilds into these forms unescaped.
func looseTexts(v string) []string {
	rest, ok := strings.CutPrefix(v, "https://")
	if !ok || strings.ContainsAny(v, " \t\"'\\") {
		return nil
	}
	forms := []string{"https:/" + rest, "//" + rest, rest}
	if i := strings.Index(rest, "/"); i >= 0 {
		forms = append(forms, "https:/"+rest[i+1:])
	}
	var out []string
	for _, f := range forms {
		out = append(out, (&url.Error{Op: "Get", URL: f, Err: io.ErrUnexpectedEOF}).Error())
	}
	return out
}

// genRedactCases: every distinct text Go writes about a generated address
// that holds a password, with what redact makes of it.
func genRedactCases() [][2]string {
	seen := map[string]bool{}
	var out [][2]string
	for _, val := range genValues() {
		// Values with a look-alike are left out: those with a password are
		// refused in every setting (TestAddressMatrix fails on any it
		// accepts), those without one hold nothing to hide, and Go
		// writes the look-alike percent-encoded (%EF%BC%A0 for a full-width
		// @), which the redactor doesn't read as an @.
		if !val.secret() || !val.scheme || genLookalike(val.v) {
			continue
		}
		for _, txt := range genTexts(val.v) {
			if seen[txt] || !hasMarker(txt) {
				continue
			}
			seen[txt] = true
			out = append(out, [2]string{txt, redact(txt)})
		}
	}
	return out
}

// TestAddressMatrix: the address check on every generated value in every
// address setting, and the redactor on Go's errors for every generated
// address.
func TestAddressMatrix(t *testing.T) {
	var fails genFailures
	t0 := time.Now()
	accepted, refused, n := runMatrix(t, &fails)
	dangerous := 0
	for _, r := range accepted {
		if r.dangerous {
			dangerous++
		}
	}
	var tl []string
	for k, n := range fails.tally {
		tl = append(tl, fmt.Sprintf("%s %d", k, n))
	}
	sort.Strings(tl)
	t.Logf("look-alike values: %s", strings.Join(tl, ", "))
	t.Logf("check: %d settings x %d values (with and without a login in the fields) = %d runs in %v; with a password: %d accepted (%d still in an address, all in a setting whose sign-in stays there), %d refused",
		len(genSettings(t)), len(genValues()), n, time.Since(t0).Round(time.Millisecond), len(accepted), dangerous, len(refused))

	t0 = time.Now()
	cases := genRedactCases()
	for _, c := range cases {
		if hasMarker(c[1]) {
			fails.add(t, "redact leaves the password", "\n in:  %s\n out: %s", c[0], c[1])
		}
		if again := redact(c[1]); again != c[1] {
			fails.add(t, "redacting twice changes it", "%s -> %s", c[1], again)
		}
	}
	t.Logf("redact: %d distinct error texts in %v", len(cases), time.Since(t0).Round(time.Millisecond))
	fails.report(t)
}

// TestRedactGenJSON writes the generated redaction cases where the app's
// LogsTest reads them (DRIVEWAY_REDACT_GEN_JSON, set by ./build.sh): the app's
// redactor must make of each what the engine's does.
func TestRedactGenJSON(t *testing.T) {
	path := os.Getenv("DRIVEWAY_REDACT_GEN_JSON")
	if path == "" {
		t.Skip("DRIVEWAY_REDACT_GEN_JSON not set")
	}
	type row struct {
		In  string `json:"in"`
		Out string `json:"out"`
	}
	var out []row
	for _, c := range genRedactCases() {
		out = append(out, row{c[0], c[1]})
	}
	b, err := json.Marshal(map[string]any{"markers": genMarkers, "cases": out})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// pairwise picks, in order, each run that covers a pair of shapes (or of a
// setting and a shape) no run picked before covers: every pair of levels
// that occurs among runs is covered at least once.
func pairwise(runs []matrixRun) []matrixRun {
	dims := func(r matrixRun) []string {
		return []string{"s=" + r.set.String(), "u=" + r.val.user.name, "p=" + r.val.pass.name, "h=" + r.val.host.name,
			"a=" + r.val.path.name, fmt.Sprintf("f=%v,%v", r.fields, r.val.scheme)}
	}
	covered := map[string]bool{}
	var out []matrixRun
	for _, r := range runs {
		d := dims(r)
		picked := false
		for i := 0; i < len(d); i++ {
			for j := i + 1; j < len(d); j++ {
				if k := d[i] + "|" + d[j]; !covered[k] {
					covered[k] = true
					picked = true
				}
			}
		}
		if picked {
			out = append(out, r)
		}
	}
	return out
}

// ownDialers are services whose connections don't go through rclone's dialer
// (so the in-process network guard can't stop a connection to an address).
var ownDialers = map[string]bool{"hdfs": true, "storj": true, "tardigrade": true}

// TestAddressMatrixNetwork: SetRemote and a connection attempt (Check) for a
// pairwise sample of the accepted values with a password: of those that keep
// it in an address (the HTTP kind's and the proxies' own sign-ins; with
// DRIVEWAY_MATRIX_FULL=1, all of them) and, apart, of the others. And
// SetRemote for a pairwise sample of the refused ones. With the network cut, no
// request leaves the machine. No returned error and no log line may hold
// the password. It runs itself again inside a network namespace with no
// network at all (bwrap --unshare-net) when it can; otherwise it runs here,
// with rclone's HTTP requests sent to a proxy on this machine that answers
// none, name lookups failing, rclone's other connections bound to the
// loopback address (so none can reach another machine), and the values of
// services with their own dialer and an address for a host left out.
func TestAddressMatrixNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	inNamespace := os.Getenv("DRIVEWAY_NO_NETWORK") == "1"
	if !inNamespace {
		if bw, err := exec.LookPath("bwrap"); err == nil && exec.Command(bw, "--unshare-net", "--dev-bind", "/", "/", "true").Run() == nil {
			limit := "20m"
			if d, ok := t.Deadline(); ok {
				limit = time.Until(d).Truncate(time.Second).String()
			}
			cmd := exec.Command(bw, "--unshare-net", "--die-with-parent", "--dev-bind", "/", "/", os.Args[0],
				"-test.run", "^TestAddressMatrixNetwork$", "-test.count", "1", "-test.v", "-test.timeout", limit)
			cmd.Env = append(os.Environ(), "DRIVEWAY_NO_NETWORK=1")
			out, err := cmd.CombinedOutput()
			for _, l := range strings.Split(string(out), "\n") {
				if strings.Contains(l, "address_matrix_test.go") || strings.HasPrefix(strings.TrimSpace(l), "---") {
					t.Log(strings.TrimSpace(l))
				}
			}
			if err != nil {
				t.Fatalf("in a namespace with no network: %v\n%s", err, out)
			}
			return
		}
		t.Log("bwrap can't make a namespace without a network here: running with the in-process guard")
	}
	var fails genFailures
	seen := blockedNetwork(t)
	logs := captureLogs(t)
	ci := fs.GetConfig(context.Background())
	// One try per request: a failed connection is all an attempt needs to
	// show, and a service's retries (Seafile's take seconds) would make
	// this check take minutes.
	oldRetries := ci.LowLevelRetries
	ci.LowLevelRetries = 1
	t.Cleanup(func() { ci.LowLevelRetries = oldRetries })
	if !inNamespace {
		old := ci.BindAddr
		ci.BindAddr = net.ParseIP("127.0.0.1")
		fshttp.ResetTransport()
		t.Cleanup(func() { ci.BindAddr = old; fshttp.ResetTransport() })
	}
	accepted, refused, _ := runMatrix(t, &genFailures{})
	var runs []matrixRun
	var rest []matrixRun
	for _, r := range accepted {
		if r.dangerous {
			runs = append(runs, r)
		} else {
			rest = append(rest, r)
		}
	}
	// Pairwise within each pool: the values that keep the password in an
	// address (the HTTP kind's and the proxies' own sign-ins), and the others.
	nAllDangerous := len(runs)
	if os.Getenv("DRIVEWAY_MATRIX_FULL") != "1" {
		runs = pairwise(runs)
	}
	nDangerous := len(runs)
	runs = append(runs, pairwise(rest)...)
	skipped := 0
	if !inNamespace {
		kept := runs[:0]
		for _, r := range runs {
			if ownDialers[r.set.ri.Name] && strings.ContainsAny(r.val.host.v, "[0123456789") && r.val.host.name != "name" && r.val.host.name != "dotted" {
				skipped++
				continue
			}
			kept = append(kept, r)
		}
		runs = kept
	}

	// Refused: SetRemote says so in its own words and keeps nothing.
	refusedSample := pairwise(refused)
	for i, r := range refusedSample {
		id := fmt.Sprintf("matrix-refused-%d", i)
		err := SetRemote(id, r.set.ri.Name, r.json())
		if err == nil {
			fails.add(t, "SetRemote accepted what the check refused", "%s %q", r.set, r.val.v)
		} else if hasMarker(err.Error()) {
			fails.add(t, "SetRemote's refusal repeats the password", "%s: %s", r.set, err)
		}
		mu.Lock()
		kept := store.HasSection(id)
		mu.Unlock()
		if kept {
			fails.add(t, "a refused server was kept", "%s %q", r.set, r.val.v)
		}
		RemoveRemote(id)
	}

	// Legitimate international folders (intlRows): SetRemote keeps each as
	// typed, and a connection attempt reaches its host with no refusal.
	intlReached := 0
	for i, r := range intlRows() {
		id := fmt.Sprintf("matrix-intl-%d", i)
		ri, _ := fs.Find(r.backend)
		if err := SetRemote(id, r.backend, paramsJSON(ri, map[string]string{r.setting: r.value}, true)); err != nil {
			fails.add(t, "a legitimate international address was refused", "%s.%s %q: %v", r.backend, r.setting, r.value, err)
			continue
		}
		mu.Lock()
		kept, _ := store.GetValue(id, r.setting)
		mu.Unlock()
		if kept != r.value {
			fails.add(t, "a legitimate international address was changed", "%s.%s %q -> %q", r.backend, r.setting, r.value, kept)
		}
		err := Check(id)
		RemoveRemote(id)
		txt := ""
		if err != nil {
			txt = rawText(err)
		}
		if strings.Contains(txt, "badsettings") || strings.Contains(txt, "lookalike") || strings.Contains(txt, "looks like") {
			fails.add(t, "a legitimate international address got a refusal", "%s.%s %q: %s", r.backend, r.setting, r.value, txt)
		}
		hit := strings.Contains(txt, r.host) || strings.Contains(txt, "xn--")
		for _, h := range seen() {
			hit = hit || strings.Contains(h, r.host) || strings.Contains(h, "xn--")
		}
		if hit {
			intlReached++
		} else {
			fails.add(t, "a legitimate international address never reached its host", "%s.%s %q: %s", r.backend, r.setting, r.value, txt)
		}
	}
	t.Logf("network: %d legitimate international addresses kept as typed and tried: %d reached their host", len(intlRows()), intlReached)

	// Accepted: a connection attempt, a few at a time.
	t0 := time.Now()
	var tmu sync.Mutex
	spent := map[string]time.Duration{}
	work := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				r := runs[i]
				id := fmt.Sprintf("matrix-%d", i)
				if err := SetRemote(id, r.set.ri.Name, r.json()); err != nil {
					if hasMarker(err.Error()) {
						fails.add(t, "SetRemote's error repeats the password", "%s: %s", r.set, err)
					}
					fails.add(t, "SetRemote refused what the check accepted", "%s %q: %v", r.set, r.val.v, err)
					continue
				}
				t1 := time.Now()
				err := Check(id)
				RemoveRemote(id)
				took := time.Since(t1)
				tmu.Lock()
				spent[r.set.ri.Name] += took
				tmu.Unlock()
				if err != nil && hasMarker(err.Error()) {
					fails.add(t, "the returned error holds the password", "%s %q (fields %v):\n %s", r.set, r.val.v, r.fields, err)
				}
				if err != nil {
					if txt := rawText(err); hasMarker(txt) && hasMarker(redact(txt)) {
						fails.add(t, "redact leaves the password in Go's text", "%s: %s", r.set, redact(txt))
					}
				}
			}
		}()
	}
	for i := range runs {
		work <- i
	}
	close(work)
	wg.Wait()
	t.Logf("network: %d connection attempts (a pairwise sample of %d of the %d values that keep the password in an address, and of %d of the %d others) in %v; %d refused values given to SetRemote (a pairwise sample of %d); %d left out (own dialer, address for a host, no namespace)",
		len(runs), nDangerous, nAllDangerous, len(runs)-nDangerous, len(rest), time.Since(t0).Round(time.Millisecond), len(refusedSample), len(refused), skipped)

	var slow []string
	for name, d := range spent {
		if d > 5*time.Second {
			slow = append(slow, fmt.Sprintf("%s %v", name, d.Round(time.Second)))
		}
	}
	sort.Strings(slow)
	t.Logf("services whose attempts took over 5s in all: %s", strings.Join(slow, ", "))

	// Which settings a connection attempt actually reaches: a harmless
	// address with a name of its own, which must show up in the error Go
	// wrote or among the requests the proxy saw. A service that fails
	// before it uses the address (a missing key, no sign-in yet) checks
	// only its settings' path, and is named here.
	var notReached []string
	var cmu sync.Mutex
	var cwg sync.WaitGroup
	reached := 0
	for i, s := range genSettings(t) {
		cwg.Add(1)
		go func(i int, s genSetting) {
			defer cwg.Done()
			canary := fmt.Sprintf("canary-%d.example.com", i)
			v := "https://" + canary + "/"
			if s.hostOnly {
				v = canary
			}
			id := fmt.Sprintf("matrix-canary-%d", i)
			err := SetRemote(id, s.ri.Name, paramsJSON(s.ri, map[string]string{s.name: v}, true))
			if err == nil {
				err = Check(id)
			}
			RemoveRemote(id)
			hit := err != nil && strings.Contains(rawText(err), canary)
			for _, h := range seen() {
				hit = hit || strings.Contains(h, canary)
			}
			cmu.Lock()
			defer cmu.Unlock()
			if hit {
				reached++
			} else {
				notReached = append(notReached, s.String())
			}
		}(i, s)
	}
	cwg.Wait()
	sort.Strings(notReached)
	t.Logf("a connection attempt reaches the address in %d settings; not in %d: %s", reached, len(notReached), strings.Join(notReached, " "))

	all := logs.all()
	logs.mu.Lock()
	raw := logs.stderr.String()
	logs.mu.Unlock()
	for _, l := range strings.Split(all+"\n"+raw, "\n") {
		if hasMarker(l) {
			fails.add(t, "a log line holds the password", "%s", l)
		}
	}
	for _, h := range seen() {
		if hasMarker(h) {
			fails.add(t, "a request carried the password to the proxy", "%s", h)
		}
	}
	fails.report(t)
}

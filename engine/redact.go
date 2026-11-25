// SPDX-License-Identifier: Apache-2.0

package engine

// Redaction: an error's text can carry a credential. A service that takes its
// API key in the address (premiumize.me's ?apikey=) puts it in every network
// error Go reports, and the app logs the engine's errors, where a person may
// copy them into a public bug report. So every error that leaves the engine
// (scrub, at each exported function) and every line it logs (rclone's
// logger, Go's log package and, on Android, everything written to stdout and
// stderr) goes through redact first. The app redacts its own log lines again
// (Logs.java), with the same rules and the same test cases
// (test/redact_cases.json).

import (
	"context"
	"io"
	"log"
	"log/slog"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/rclone/rclone/fs"
)

// secretName is a setting or parameter name that holds a credential: it ends
// in one of these words (access_token, client_secret, X-Amz-Signature,
// session_id, Mega's sid, Linkbox's pwd, ...). A few harmless names end the
// same way; hiding their values costs nothing.
const secretName = `[a-z0-9_.-]*?(?:api[_-]?key|key|token|passw(?:or)?d|passphrase|pass|pwd|secret|signature|sig|credential|` +
	`sess(?:ion)?[_-]?id|sid|auth|authorization)`

var (
	// urlPattern: an address with a scheme, up to a space, a quote or a
	// bracket that can't be part of it.
	urlPattern = regexp.MustCompile("(?i)\\b[a-z][a-z0-9+.-]*://[^\\s\"'<>`\\\\]+")
	// The start of an address, for userinfo that holds a character
	// urlPattern stops at (looseUserinfo).
	schemeStart = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://`)
	// Go's url.Parse names the port it couldn't read, which is the start of
	// a password typed with a / ? or # in it (alice:pass/word@host).
	invalidPort = regexp.MustCompile(`(invalid port ")(?:[^"\\]|\\.)*(" after host)`)
	// user:password@host without a scheme (an address typed without one).
	barePair = regexp.MustCompile(`(?i)(^|[\s"'(<\[=,])[^\s"'<>/@:=,(\[]+:[^\s"'<>/]{1,200}@([a-z0-9][a-z0-9.-]*|\[[0-9a-f:.%]+\])`)
	// name=value, as in a query string, a form body or a settings line.
	namedValue = regexp.MustCompile(`(?i)\b(` + secretName + `)(\s*=\s*)("[^"]*"|'[^']*'|[^\s&"',;)\]}]+)`)
	// "name": "value" in JSON, also escaped inside a quoted string.
	jsonValue = regexp.MustCompile(`(?i)(\\?"` + secretName + `\\?"\s*:\s*)(\\?")((?:[^"\\]|\\[^"])*)(\\?")`)
	// <name>value</name> in XML, and the parts of S3's signature errors
	// (AWS's documented error format).
	xmlValue = regexp.MustCompile(`(?i)(<(?:` + secretName +
		`|awsaccesskeyid|stringtosign(?:bytes)?|canonicalrequest(?:bytes)?|signatureprovided)>)[^<]*(<)`)
	// An HTTP header that carries a credential, as a line or as Go prints
	// http.Header (map[Authorization:[Bearer ...]]), whatever its scheme
	// (Bearer, Basic, LOW, Zoho-oauthtoken and the other schemes listed).
	authHeader = regexp.MustCompile(`(?i)\b((?:proxy-)?authorization|pm-storage-token|` +
		`x-[a-z0-9-]*?(?:token|key|auth|auth-data|auth-sign|sign|signature|secret|sess-id|session-id|password))` +
		`(\s*[:=]\s*\[?)((?:(?:bearer|basic|digest|token|oauth|low|sharedkey(?:lite)?|aws4-hmac-sha256|aws|zoho-oauthtoken|negotiate|ntlm|mac)[ \t]+)?[^\s"',;\]]+)`)
	cookieHeader = regexp.MustCompile(`(?i)\b((?:set-)?cookie)(\s*:\s*)[^\r\n"]+`)
	bearer       = regexp.MustCompile(`(?i)\b(bearer)\s+[a-z0-9._~+/=-]{8,}`)
	// Credentials in a path, wherever it appears (with an address or
	// without one): Seafile's file links (seafhttp/files/<token>/...),
	// Uloz.to's upload, Quatrix's upload key, a pixeldrain shared folder
	// (but not "me", the account's own).
	pathTokens = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(seafhttp/[a-z-]+/)([^/\s'"?#]+)`),
		regexp.MustCompile(`(?i)(/upload-batch/private/)([^/\s'"?#]+)`),
		regexp.MustCompile(`(?i)(/upload/(?:chunked|finalize)/)([^/\s'"?#]+)`),
		regexp.MustCompile(`(?i)(/api/filesystem/)([^/\s'"?#]+)`),
	}
	// Messages from services' code that print a credential without an
	// address or a name to find it by: the rest of the message goes.
	tails = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(failed to get bearer token using )[^\n]*`),    // WebDAV's token command and its output
		regexp.MustCompile(`(?i)(fetched invalid link )[^\n]*`),                // pCloud's signed download link
		regexp.MustCompile(`(?i)(cannot unmarshal response(?: data)? )[^\n]*`), // Filen's answer, which can hold its keys
		regexp.MustCompile(`(?i)(\bupload )\S+( finalize)`),                    // Quatrix's upload key
	}
)

// redact returns s without the credentials it may hold. Of an address it
// keeps the scheme, the host, the port and the path, and drops the user name
// and password, the query string and the fragment; elsewhere it hides the
// values of settings named like credentials and of the headers that carry
// them. What a person or a developer needs to see stays: the host, the
// status, the kind of error.
func redact(s string) string {
	s = invalidPort.ReplaceAllString(s, `${1}:***${2}`)
	s = looseUserinfo(s)
	s = urlPattern.ReplaceAllStringFunc(s, redactURL)
	s = barePair.ReplaceAllString(s, "${1}***@${2}")
	s = namedValue.ReplaceAllString(s, "${1}${2}***")
	s = jsonValue.ReplaceAllString(s, "${1}${2}***${4}")
	s = xmlValue.ReplaceAllString(s, "${1}***${2}")
	s = authHeader.ReplaceAllString(s, "${1}${2}***")
	s = cookieHeader.ReplaceAllString(s, "${1}${2}***")
	s = bearer.ReplaceAllString(s, "${1} ***")
	for _, re := range pathTokens {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			g := re.FindStringSubmatch(m)
			if g[2] == "me" || g[2] == "***" {
				return m
			}
			return g[1] + "***"
		})
	}
	for _, re := range tails {
		s = re.ReplaceAllString(s, "${1}***${2}")
	}
	return s
}

// redactURL redacts one address found by urlPattern.
func redactURL(m string) string {
	// Punctuation after an address ("(see https://host/x).") belongs to the
	// sentence.
	core := strings.TrimRight(m, ".,;:!?)]}")
	trail := m[len(core):]
	i := strings.Index(core, "://")
	if i < 0 {
		return m
	}
	scheme, rest := core[:i+3], core[i+3:]
	user := userinfoEnd(rest)
	if user >= 0 {
		rest = rest[user+1:]
	}
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	authority, tail := rest[:end], rest[end:]
	if q := strings.IndexAny(tail, "?#"); q >= 0 {
		tail = tail[:q] + "?***"
	}
	tail = redactPath(authority, tail)
	// What follows a colon in the host part and isn't a port is a password
	// the address was cut before (Go cuts alice:pass#word@host at the #).
	if c := strings.LastIndex(authority, ":"); c >= 0 && !strings.Contains(authority, "[") &&
		strings.Trim(authority[c+1:], "0123456789") != "" {
		authority = authority[:c+1] + "***"
	}
	if user >= 0 {
		authority = "***@" + authority
	}
	return scheme + authority + tail + trail
}

// looseUserinfo redacts the user name and password of an address when they
// hold a character urlPattern stops at (a space, a quote, <, >, a backtick
// or a backslash, as Go's %q prints a quote): Go's url.Parse refuses such an
// address and repeats it whole in its error. The user name and password are
// everything from the scheme to the first @ that a host follows (letters,
// digits, dots, dashes, an IPv6 address in brackets, a port) and then the
// end of the address, when it reads as user:password (a colon, nothing
// before it that a user name can't hold, and no port after it). What
// urlPattern can redact itself is left to it, and so is text with Go's ": "
// between an address and its error.
func looseUserinfo(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range schemeStart.FindAllStringIndex(s, -1) {
		if m[0] < last {
			continue
		}
		start := m[1]
		end := userinfoAt(s, start)
		if end < 0 {
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString("***")
		last = end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// userinfoAt is where the loose user name and password that start at i end
// (their @), or -1 when there are none for looseUserinfo to redact.
func userinfoAt(s string, i int) int {
	limit := i + 512
	if limit > len(s) {
		limit = len(s)
	}
	for j := i; j < limit; j++ {
		c := s[j]
		if c == '\n' || c == '\r' {
			return -1
		}
		if c != '@' || !hostFollows(s, j+1) {
			continue
		}
		u := s[i:j]
		if u == "***" || strings.Contains(u, "://") || strings.Contains(u, `": `) {
			return -1
		}
		// A later @ may end it (a password holding an @ and a space).
		if strings.ContainsAny(u, " \t\"'<>`\\") && userPass(u) {
			return j
		}
	}
	return -1
}

// userPass: u reads as user:password, not as a host and a port or a
// sentence ("https://host:8443/x and mail help@example.com").
func userPass(u string) bool {
	c := strings.Index(u, ":")
	if c <= 0 || strings.ContainsAny(u[:c], " \t\"'/\\") {
		return false
	}
	p := u[c+1:]
	if k := strings.IndexAny(p, "/?#"); k >= 0 {
		p = p[:k]
	}
	return strings.Trim(p, "0123456789") != ""
}

// hostFollows: s[i:] starts with a host (and maybe a port), then the end of
// the address.
func hostFollows(s string, i int) bool {
	j := i
	for j < len(s) && (isHostByte(s[j])) {
		j++
	}
	if j == i {
		return false
	}
	return j == len(s) || strings.IndexByte("/?#\"'\\ \t\r\n),;>`", s[j]) >= 0
}

func isHostByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		c == '.' || c == '-' || c == ':' || c == '[' || c == ']' || c == '%' || c == '_'
}

// userinfoEnd is where the user name and password before an address's host
// end (the @), or -1. A password typed with a / ? or # in it, which a
// well-formed address escapes, still counts (alice:p/ss@host) unless what
// follows the colon is a port (host:8080/path/a@b).
func userinfoEnd(rest string) int {
	at := strings.Index(rest, "@")
	if at < 0 {
		return -1
	}
	end := strings.IndexAny(rest, "/?#")
	if end < 0 || at < end {
		if end < 0 {
			end = len(rest)
		}
		return strings.LastIndex(rest[:end], "@")
	}
	host := rest[:end]
	c := strings.Index(host, ":")
	if c < 0 || strings.Contains(host, "[") || strings.Trim(host[c+1:], "0123456789") == "" {
		return -1
	}
	return at
}

// secretPaths are services that put a credential in an address's path, by
// the end of the host: OpenDrive's session id (after .json/), OneDrive's
// upload session (/up/...), a 1Fichier shared folder (/dir/...).
var secretPaths = []struct {
	host string
	re   *regexp.Regexp
}{
	{"opendrive.com", regexp.MustCompile(`(\.json/)[^/?#]+`)},
	{"1drv.com", regexp.MustCompile(`^(/up/)[^/?#]+`)},
	{"1fichier.com", regexp.MustCompile(`^(/dir/)[^/?#]+`)},
}

// redactPath hides a credential that a service puts in the path itself.
func redactPath(authority, p string) string {
	host := strings.ToLower(authority)
	if c := strings.LastIndex(host, ":"); c >= 0 && !strings.HasSuffix(host, "]") {
		host = host[:c]
	}
	for _, r := range secretPaths {
		if host == r.host || strings.HasSuffix(host, "."+r.host) {
			p = r.re.ReplaceAllString(p, "${1}***")
		}
	}
	return p
}

// cleanError is an error whose text has been redacted; the error it came
// from stays reachable for errors.Is and errors.As inside the engine.
type cleanError struct {
	msg string
	err error
}

func (e *cleanError) Error() string { return e.msg }
func (e *cleanError) Unwrap() error { return e.err }

// cleanErr returns err with its text redacted, or nil.
func cleanErr(err error) error {
	if err == nil {
		return nil
	}
	return &cleanError{msg: redact(err.Error()), err: err}
}

// scrub redacts the error an exported function returns: each one starts
// with defer scrub(&err), and TestEveryExportScrubs checks that it does.
func scrub(err *error) { *err = cleanErr(*err) }

// redactWriter redacts what it passes on. Each Write is taken as whole lines
// (Go's log package and the handler below write a line at a time).
type redactWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (r *redactWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := io.WriteString(r.w, redact(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// rcloneLog is where rclone's log lines go: stderr, which gomobile sends to
// Android's log under the GoLog tag.
var rcloneLog = &redactWriter{w: os.Stderr}

// logHandler writes rclone's log lines ("NOTICE: object: text") through
// rcloneLog. rclone filters by its own log level before calling it.
type logHandler struct{}

func (logHandler) Enabled(context.Context, slog.Level) bool { return true }

func (logHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(levelName(r.Level))
	b.WriteString(": ")
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "object" {
			b.WriteString(a.Value.String())
			b.WriteString(": ")
		}
		return true
	})
	b.WriteString(r.Message)
	b.WriteString("\n")
	_, err := rcloneLog.Write([]byte(b.String()))
	return err
}

func (h logHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h logHandler) WithGroup(string) slog.Handler      { return h }

func levelName(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING"
	case l >= fs.SlogLevelNotice:
		return "NOTICE"
	case l >= slog.LevelInfo:
		return "INFO"
	}
	return "DEBUG"
}

// rclone's fs/log package sets a logger that writes to stderr unredacted
// when it is initialized; this package is initialized after it.
func init() {
	fs.SetLogger(logHandler{})
}

var logsOnce sync.Once

// redactLogs puts redaction in front of the other ways text reaches the log:
// Go's log package (and log/slog's default logger, which writes through it),
// and on Android stdout and stderr, which gomobile copies to the GoLog tag
// and which some libraries print to directly. It runs from setup, after
// gomobile has pointed them at Android's log.
func redactLogs() {
	logsOnce.Do(func() {
		log.SetOutput(&redactWriter{w: log.Writer()})
		if runtime.GOOS == "android" {
			for _, fd := range []int{1, 2} {
				if err := filterFD(fd); err != nil {
					log.Printf("can't redact what is written to fd %d: %v", fd, err)
				}
			}
		}
	})
}

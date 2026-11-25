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
	"fmt"
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
// session_id, Mega's sid, Linkbox's pwd, SFTP's key_pem, Internxt's
// mnemonic, Storj's access_grant, Azure's connection_string and sas_url,
// ...). A few harmless names end the same way; hiding their values costs
// nothing. TestSecretNamesFromRclone checks every setting rclone marks as a
// password or as sensitive against it.
// authSchemes are the schemes an Authorization value starts with.
const authSchemes = `(?:bearer|basic|digest|token|oauth|low|sharedkey(?:lite)?|aws4-hmac-sha256|aws|zoho-oauthtoken|negotiate|ntlm|mac)`

const secretName = `[a-z0-9_.-]*?(?:api[_-]?key|keys?|key_pem|key_base64|tokens?|passw(?:or)?d|passphrase|pass|pwd|secrets?|` +
	`signature|sig|credentials?|sess(?:ion)?[_-]?id|sid|auth|authorization|mnemonic|grant|cookies?|connection_string|sas_url)`

var (
	// urlPattern: an address with a scheme, up to a space, a quote or a
	// bracket that can't be part of it.
	urlPattern = regexp.MustCompile("(?i)\\b[a-z][a-z0-9+.-]{0,30}://[^\\s\"'<>`\\\\]+")
	// An address in JSON that escapes its slashes (https:\/\/host\/path),
	// once or, quoted again by Go's %q, twice.
	// The rest of it is read by escapedEnd: as a regex, a loop over two
	// alternatives would overflow the app's regex engine's stack on a long
	// address.
	escapedStart = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]{0,30}:\\\\?/\\\\?/`)
	// The start of an address, for userinfo that holds a character
	// urlPattern stops at (looseUserinfo).
	schemeStart = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]{0,30}://`)
	// Go's url.Parse names the port it couldn't read, which is the start of
	// a password typed with a / ? or # in it (alice:pass/word@host).
	invalidPort = regexp.MustCompile(`(invalid port ")(?:[^"\\]|\\.)*(" after host)`)
	// user:password@host without a scheme (an address typed without one),
	// or after a scheme spelled in a way urlPattern doesn't read (one slash,
	// extra slashes, a scheme doubled by a service that adds its own:
	// https://https//alice:pass@host).
	barePair = regexp.MustCompile(`(?i)(^|[\s"'(<\[=,]|:/+|//)[^\s"'<>/@:=,(\[]{1,200}:[^\s"'<>/]{1,200}@([a-z0-9][a-z0-9.-]*|\[[0-9a-f:.%]+\])`)
	// A setting whose value can be several words (a mnemonic, a
	// passphrase): its start. multiWordEnd finds where the value ends.
	multiWord = regexp.MustCompile(`(?i)(^|[^a-z0-9_.-])([a-z0-9_.-]*?(?:mnemonic|passphrase))[ \t]*=[ \t]*`)
	// name=value, as in a query string, a form body or a settings line.
	// A name starts where a run of the characters a name holds starts (the
	// character before is kept as it is): starting inside a run would make
	// the app's regex engine, which backtracks, take time in the square of a
	// long dotted or dashed run's length.
	namedValue = regexp.MustCompile(`(?i)(^|[^a-z0-9_.-])(` + secretName + `)(\s*=\s*)("[^"]*"|'[^']*'|[^\s&"',;)\]}]+)`)
	// "name": "value" in JSON, also escaped inside a quoted string.
	jsonValue = regexp.MustCompile(`(?i)(\\?"` + secretName + `\\?"\s*:\s*)(\\?")((?:[^"\\]|\\[^"])*)(\\?")`)
	// <name>value</name> in XML, and the parts of S3's signature errors
	// (AWS's documented error format).
	xmlValue = regexp.MustCompile(`(?i)(<(?:` + secretName +
		`|awsaccesskeyid|stringtosign(?:bytes)?|canonicalrequest(?:bytes)?|signatureprovided)>)[^<]*(<)`)
	// An HTTP header that carries a credential, as a line or as Go prints
	// http.Header (map[Authorization:[Bearer ...]]), whatever its scheme
	// (Bearer, Basic, LOW, Zoho-oauthtoken and the other schemes listed).
	authHeader = regexp.MustCompile(`(?i)(^|[^a-z0-9_-])(pm-storage-token|` +
		`x-[a-z0-9-]*?(?:token|key|auth|auth-data|auth-sign|sign|signature|secret|sess-id|session-id|password)|` +
		`[a-z0-9-]*?(?:api-?key|auth-token|access-token))` +
		`(\s*[:=]\s*\[?)((?:` + authSchemes + `[ \t]+)?[^\s"',;\]]+)`)
	// A private key's PEM block, from its first line to its last (or the
	// end of the text, when cut before it); rclone keeps SFTP's on one line
	// with \n escapes. The line-by-line writers carry a block's middle lines
	// over (lineRedactor).
	pemBlock = regexp.MustCompile(`(?s)(-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----).*?(-----END [A-Z0-9 ]*PRIVATE KEY-----|\z)`)
	pemBegin = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)
	pemEnd   = regexp.MustCompile(`-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	// The Authorization, Proxy-Authorization and X-Authorization headers:
	// the whole value, whatever its scheme, quotes included, up to the end
	// of the line (or, as Go prints http.Header, the closing bracket).
	authValue = regexp.MustCompile(`(?i)(^|[^a-z0-9_-])((?:proxy-|x-)?authorization)([ \t]*:[ \t]*\[?)[^\r\n\]]+`)
	// The same written as a setting, scheme and all (authorization= Bearer x).
	authSetting  = regexp.MustCompile(`(?i)(^|[^a-z0-9_-])((?:proxy-|x-)?authorization)(\s*=\s*)(?:` + authSchemes + `[ \t]+)?[^\s&"',;)\]}]+`)
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
	s = pemBlock.ReplaceAllString(s, "${1}***${2}")
	s = invalidPort.ReplaceAllString(s, `${1}:***${2}`)
	s = looseUserinfo(s)
	s = redactEscaped(s)
	s = urlPattern.ReplaceAllStringFunc(s, redactURL)
	s = looseForms(s)
	s = barePair.ReplaceAllString(s, "${1}***@${2}")
	s = redactMultiWord(s)
	s = authSetting.ReplaceAllString(s, "${1}${2}${3}***")
	s = namedValue.ReplaceAllString(s, "${1}${2}${3}***")
	s = jsonValue.ReplaceAllString(s, "${1}${2}***${4}")
	s = xmlValue.ReplaceAllString(s, "${1}***${2}")
	s = authValue.ReplaceAllString(s, "${1}${2}${3}***")
	s = authHeader.ReplaceAllString(s, "${1}${2}${3}***")
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

// redactMultiWord hides the whole value of each multiWord setting: words up
// to the end of the line, a quote, an & or the next name=value.
func redactMultiWord(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range multiWord.FindAllStringIndex(s, -1) {
		if m[0] < last {
			continue
		}
		end := multiWordEnd(s, m[1])
		if end == m[1] {
			continue
		}
		b.WriteString(s[last:m[1]])
		b.WriteString("***")
		last = end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// multiWordEnd is where a multi-word value that starts at i ends.
func multiWordEnd(s string, i int) int {
	end := i
	for j := i; j <= len(s); {
		k := j
		for k < len(s) && strings.IndexByte(" \t\r\n\"'&", s[k]) < 0 {
			k++
		}
		if k == j || strings.IndexByte(s[j:k], '=') >= 0 {
			break
		}
		end = k
		for k < len(s) && (s[k] == ' ' || s[k] == '\t') {
			k++
		}
		j = k
	}
	return end
}

// redactEscaped redacts each address whose slashes are escaped.
func redactEscaped(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range escapedStart.FindAllStringIndex(s, -1) {
		if m[0] < last {
			continue
		}
		end := escapedEnd(s, m[1])
		b.WriteString(s[last:m[0]])
		b.WriteString(redactEscapedURL(s[m[0]:end]))
		last = end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// escapedEnd is where an escaped address that goes on at i ends: at a space,
// a quote, <, >, a backtick, or a backslash that doesn't escape a slash.
func escapedEnd(s string, i int) int {
	for i < len(s) {
		switch {
		case strings.HasPrefix(s[i:], `\/`):
			i += 2
		case strings.HasPrefix(s[i:], `\\/`):
			i += 3
		case strings.IndexByte(" \t\n\r\f\v\"'<>`\\", s[i]) >= 0:
			return i
		default:
			i++
		}
	}
	return i
}

// redactEscapedURL redacts an address whose slashes are escaped: as one
// with plain slashes, escaped again afterwards.
func redactEscapedURL(m string) string {
	esc := `\/`
	if strings.Contains(m, `\\/`) {
		esc = `\\/`
	}
	return strings.ReplaceAll(redactURL(strings.ReplaceAll(m, esc, "/")), "/", esc)
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
	// An @ after a ? or # in the path may end a password that holds the ?
	// or # (https://alice/word?word@host, as Go's HTTP client sends
	// https://alice:/word?word@host): the path goes with the query.
	if q := strings.IndexAny(tail, "?#"); q > 0 && strings.Contains(tail[q:], "@") {
		tail = "/***?***"
	} else if q >= 0 {
		tail = tail[:q] + "?***"
	}
	// An @ in the path: the part before it may be a password whose colon
	// Go's HTTP client took out (it drops an empty port, so
	// https://alice:/word@host is sent as https://alice/word@host), so the
	// path up to its last @ goes and the host stays.
	if a := strings.LastIndex(tail, "@"); a > 0 && tail[:a] != "/***" {
		tail = "/***" + tail[a:]
	}
	tail = redactPath(authority, tail)
	// What follows a colon in the host part and isn't a port is a password
	// the address was cut before (Go cuts alice:pass#word@host at the #);
	// so is what follows the first of two colons (alice:pass:2024, cut the
	// same way).
	if c := strings.LastIndex(authority, ":"); c >= 0 && !strings.Contains(authority, "[") {
		if f := strings.Index(authority, ":"); f < c {
			authority = authority[:f+1] + "***"
		} else if strings.Trim(authority[c+1:], "0123456789") != "" {
			authority = authority[:c+1] + "***"
		}
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
		end := userinfoAt(s, start, m[0] > 0 && s[m[0]-1] == '"')
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
// (their @), or -1 when there are none for looseUserinfo to redact. Inside
// Go's quotes (quoted: parse "https://...": ...), where the closing quote
// ends the address, an address that reads two ways (ambiguousAt) counts too,
// space and all.
func userinfoAt(s string, i int, quoted bool) int {
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
		// Inside Go's quotes, a user name with a space in it counts too.
		if strings.ContainsAny(u, " \t\"'<>`\\") && (userPass(u) ||
			quoted && (ambiguousAt(s[i:j+1]) >= 0 || strings.Contains(u, ":") && !strings.Contains(u, `"`))) {
			return j
		}
	}
	return -1
}

// userPass: u reads as user:password, not as a host and a port or a
// sentence ("https://host:8443/x and mail help@example.com").
func userPass(u string) bool {
	c := strings.Index(u, ":")
	if c < 0 || strings.ContainsAny(u[:c], " \t\"'/\\") {
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

// isHostByte: a byte of a host's name, an international one's included.
func isHostByte(c byte) bool {
	return c >= 0x80 || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		c == '.' || c == '-' || c == ':' || c == '[' || c == ']' || c == '%' || c == '_'
}

// userinfoEnd is where the user name and password before an address's host
// end (the @), or -1. An address that reads two ways (ambiguousAt, the same
// rule the address check refuses by) ends at its last @, however far down:
// https://alice:2024/word@host and a folder named for an email address
// after a port both lose everything before that @ (a folder's name lost in
// the log, never a password). A password typed with a / ? or # in it after anything but a
// port (alice:p/ss@host), which Go can't read and repeats whole, also ends
// at the last @.
func userinfoEnd(rest string) int {
	if a := pairAt(rest); a >= 0 {
		return a
	}
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	return strings.LastIndex(rest[:end], "@")
}

// pairAt is where a user name and password that hold a / ? or # end: the
// last @ of an address that reads two ways (ambiguousAt), or of one whose
// colon is followed by something that isn't a port before the / ? or #
// (alice:p/ss@host). Otherwise -1.
func pairAt(rest string) int {
	if a := ambiguousAt(rest); a >= 0 {
		return a
	}
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	hostport := rest[:end]
	if a := strings.LastIndex(hostport, "@"); a >= 0 {
		hostport = hostport[a+1:]
	}
	if c := strings.LastIndex(hostport, ":"); c >= 0 && !strings.Contains(hostport[c:], "]") &&
		strings.Trim(hostport[c+1:], "0123456789") != "" {
		if a := strings.LastIndex(rest, "@"); a >= end {
			return a
		}
	}
	return -1
}

// looseStops end a word for looseForms.
const looseStops = " \t\n\r\f\v\"'<>`\\(),="

// looseForms redacts the user name and password of an address written
// without the // after its scheme, as a service may rebuild one and quote
// it: with one slash (Koofr's client joins its paths into https:/word@host),
// as //host, or with no scheme at all. Each word (up to a space, a quote,
// a bracket, a comma or an =) that holds no scheme's :// (hasScheme) is read
// as an address:
//   - scheme:/rest: everything before the last @ goes (an address with one
//     slash is never a host's own; https:/alice:2024/word@host and
//     https:/word@host both keep only the host);
//   - //rest: what userinfoEnd says goes, as after a scheme's //;
//   - anything else: only an address that reads two ways (ambiguousAt),
//     one whose colon is followed by a password with a / ? or # (pairAt),
//     or a colon and then an @ before any / ? or # (loginAt), so a mail
//     address in a sentence stays.
func looseForms(s string) string {
	var b strings.Builder
	last := 0
	for i := 0; i < len(s); {
		if strings.IndexByte(looseStops, s[i]) >= 0 {
			i++
			continue
		}
		j := i
		for j < len(s) && strings.IndexByte(looseStops, s[j]) < 0 {
			j++
		}
		w := s[i:j]
		start, end := i, -1
		if !hasScheme(w) {
			switch k := oneSlash(w); {
			case k > 0:
				start = i + k
				end = strings.LastIndex(w[k:], "@")
			case strings.HasPrefix(w, "//"):
				start = i + 2
				end = userinfoEnd(w[2:])
			default:
				if end = pairAt(w); end < 0 {
					end = loginAt(w)
				}
			}
		}
		if end > 0 && s[start:start+end] != "***" {
			b.WriteString(s[last:start])
			b.WriteString("***")
			last = start + end
		}
		i = j
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// loginAt: where a user name and password end in an address without a
// scheme, when barePair can't read them (a user name that is a mail address,
// alice@example.com:pass@host, or an empty one, :pass@host): the last @
// before any / ? or #, when a colon comes before it. Otherwise -1.
func loginAt(w string) int {
	seg := w
	if k := strings.IndexAny(seg, "/?#"); k >= 0 {
		seg = seg[:k]
	}
	c, l := strings.Index(seg, ":"), strings.LastIndex(seg, "@")
	if c < 0 || l < c {
		return -1
	}
	return l
}

// hasScheme: w holds a scheme and its :// (left to urlPattern); a :// with
// nothing before it that a scheme can end with (https:/://word@host, from a
// password that starts with //) doesn't count.
func hasScheme(w string) bool {
	for i := 0; ; {
		k := strings.Index(w[i:], "://")
		if k < 0 {
			return false
		}
		if c := i + k - 1; c >= 0 && (isLetter(w[c]) || w[c] >= '0' && w[c] <= '9' || strings.IndexByte("+.-", w[c]) >= 0) {
			return true
		}
		i += k + 1
	}
}

// oneSlash: where what follows a scheme and one slash starts in w
// (https:/word), or 0.
func oneSlash(w string) int {
	k := 0
	for k < len(w) && k <= 31 && (isLetter(w[k]) || k > 0 && (w[k] >= '0' && w[k] <= '9' || strings.IndexByte("+.-", w[k]) >= 0)) {
		k++
	}
	if k == 0 || k > 31 || !strings.HasPrefix(w[k:], ":/") || strings.HasPrefix(w[k:], "://") {
		return 0
	}
	return k + 2
}

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }

// secretPaths are credentials services put in an address's path: on the
// service's own host, whatever the segment holds (OpenDrive's session id
// after .json/, OneDrive's upload session after /up/, a 1Fichier shared
// folder after /dir/). Upload session addresses come from the service, on
// whatever host it chooses, so the two shapes distinctive enough to stand
// alone (host "") also count on any host when the segment looks like a
// token (tokenLike): a session after a first segment named up or rup, and
// an id after a segment ending in .json. A folder named up stays ("/up/photos").
var secretPaths = []struct {
	host string
	re   *regexp.Regexp
}{
	{"opendrive.com", regexp.MustCompile(`(\.json/)([^/?#]+)`)},
	{"1drv.com", regexp.MustCompile(`^(/up/)([^/?#]+)`)},
	{"1fichier.com", regexp.MustCompile(`^(/dir/)([^/?#]+)`)},
	{"", regexp.MustCompile(`^(/(?:up|rup)/)([^/?#]+)`)},
	{"", regexp.MustCompile(`(\.json/)([^/?#]+)`)},
}

// tokenLike: a path segment that looks like a token, not a name: 24
// characters or more of letters, digits and - _ . ! ~, with both a letter
// and a digit. A long folder name of that shape is hidden in the log too,
// which loses nothing a person needs.
func tokenLike(seg string) bool {
	if len(seg) < 24 || strings.Trim(seg, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.!~") != "" {
		return false
	}
	return strings.ContainsAny(seg, "0123456789") && strings.Trim(seg, "0123456789-_.!~") != ""
}

// redactPath hides a credential that a service puts in the path itself.
func redactPath(authority, p string) string {
	host := strings.ToLower(authority)
	if c := strings.LastIndex(host, ":"); c >= 0 && !strings.HasSuffix(host, "]") {
		host = host[:c]
	}
	for _, r := range secretPaths {
		anyHost := r.host == ""
		if !anyHost && host != r.host && !strings.HasSuffix(host, "."+r.host) {
			continue
		}
		p = r.re.ReplaceAllStringFunc(p, func(m string) string {
			g := r.re.FindStringSubmatch(m)
			if anyHost && !tokenLike(g[2]) {
				return m
			}
			return g[1] + "***"
		})
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

// maxLogLine is the most of one line that reaches the log, in characters
// (as the app's Logs.MAX counts them: UTF-16 units): Android's log keeps
// about 4 KB of an entry anyway, and a server's answer quoted whole in an
// error can be megabytes. The cut comes after redaction, so a credential cut
// in two is never left half-matched.
const maxLogLine = 8000

// capLine cuts s to maxLogLine characters (never inside one), says how many
// were left out, and keeps a final line break.
func capLine(s string) string {
	nl := ""
	if strings.HasSuffix(s, "\n") {
		s, nl = s[:len(s)-1], "\n"
	}
	units, end := 0, -1
	for i, r := range s {
		n := utf16Len(r)
		if units+n > maxLogLine && end < 0 {
			end = i
		}
		units += n
	}
	if end < 0 {
		return s + nl
	}
	left := 0
	for _, r := range s[end:] {
		left += utf16Len(r)
	}
	return s[:end] + fmt.Sprintf(" [cut: %d more characters]", left) + nl
}

// utf16Len is how many characters r is to Java (and so to the app's cap).
func utf16Len(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// lineRedactor redacts text that arrives a line at a time. A private key's
// block printed over several lines would leave its middle lines with
// nothing to match, so from a line that starts a block to the line that
// ends it, each line is replaced whole, however many there are. A line
// that can't be part of a block (anything but base64, a blank line or a
// PEM header) ends the carry, so a block that never ends doesn't blank the
// log for good.
type lineRedactor struct {
	inKey bool
}

// keyLine: a line that can be inside a PEM block.
var keyLine = regexp.MustCompile(`^(?:[A-Za-z0-9+/=]*\s*|(?:Proc-Type|DEK-Info): .*)$`)

func (l *lineRedactor) line(s string) string {
	nl := ""
	if strings.HasSuffix(s, "\n") {
		s, nl = s[:len(s)-1], "\n"
	}
	if l.inKey {
		loc := pemEnd.FindStringIndex(s)
		switch {
		case loc != nil:
			l.inKey = false
			s = "***" + s[loc[0]:]
		case keyLine.MatchString(s):
			return "***" + nl
		default:
			l.inKey = false
		}
	}
	if begins := pemBegin.FindAllStringIndex(s, -1); len(begins) > 0 {
		l.inKey = !pemEnd.MatchString(s[begins[len(begins)-1][1]:])
	}
	return redact(s) + nl
}

// redactWriter redacts what it passes on. Each Write is taken as whole lines
// (Go's log package and the handler below write a line at a time).
type redactWriter struct {
	mu    sync.Mutex
	w     io.Writer
	lines lineRedactor
}

func (r *redactWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, err := io.WriteString(r.w, capLine(r.lines.line(string(p)))); err != nil {
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

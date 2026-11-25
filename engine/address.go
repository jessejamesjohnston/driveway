// SPDX-License-Identifier: Apache-2.0

package engine

// The address check: the one place that decides whether an address in a
// server's settings (a WebDAV or HTTP server's url, a service's endpoint or
// host) can be used. The app's form calls it through CheckSettings (as focus
// leaves an address, and on Connect and Save), and SetRemote and ConfigStart
// run it before rclone sees the settings, so the form and the engine can't
// disagree about an address. Go's url package is the judge: an address it
// can't read is refused here, because its error repeats the address,
// password and all, and a user name and password typed in an address would
// otherwise ride along in every error that names it.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/rclone/rclone/fs"
	"golang.org/x/text/unicode/norm"
)

// addressName is the name of a setting that holds an address, as rclone's
// catalog names them: a URL (url, auth_url, an endpoint, swift's auth, a
// proxy) or a host (host, hostname, a satellite address).
// TestEveryAddressSettingClassified checks every setting rclone describes as
// a URL, an endpoint, a host or an address against it.
var addressName = regexp.MustCompile(`^(url|auth|endpoint|host|hostname|server|site|namenode)$|_(url|endpoint|address|proxy)$`)

// hostName: of the settings above, those that hold a host (and maybe a port
// or a path) without a scheme.
var hostName = regexp.MustCompile(`^(host|hostname|server|site|namenode)$|_address$|^socks_proxy$`)

// loginInAddress are the settings whose own help in rclone gives
// user:pass@ in the address as the way to sign in, so a user name and
// password there are kept where they are.
var loginInAddress = map[string]string{
	"http.url":         "the HTTP kind's only sign-in (https://user:pass@example.com)",
	"ftp.http_proxy":   "a proxy's own sign-in (http://user:pass@host:port)",
	"sftp.http_proxy":  "a proxy's own sign-in (http://user:pass@host:port)",
	"ftp.socks_proxy":  "a proxy's own sign-in (user:pass@host:port)",
	"sftp.socks_proxy": "a proxy's own sign-in (user:pass@host:port)",
}

// atInAddress are settings whose documented form holds an @ that is no
// sign-in.
var atInAddress = map[string]string{
	"storj.satellite_address":      "<nodeid>@<address>:<port>",
	"tardigrade.satellite_address": "<nodeid>@<address>:<port>",
}

// Why an address is refused. The app turns each into a sentence that names
// the field (Kinds.addressProblem); none of them repeats what was typed.
const (
	reasonScheme      = "scheme"      // no scheme where one is needed, or one that isn't http or https
	reasonHostOnly    = "hostonly"    // a scheme or a path in a setting that takes only a host
	reasonLogin       = "login"       // a user name or password that can't be read, on a kind with User name and Password
	reasonPort        = "port"        // what follows a colon isn't a port (a password typed with a / in it looks like this)
	reasonEscape      = "escape"      // a user name or password that can't be read, on a kind without those settings
	reasonCredentials = "credentials" // a user name or password in a setting that has no use for one
	reasonUnreadable  = "unreadable"  // anything else Go's url package can't read
	reasonAmbiguous   = "ambiguous"   // reads two ways (ambiguousAt): a password with a / ? or # in it, or a folder's name with an @
	// The same where a user name and password stay in the address
	// (loginInAddress), so the way out is escaping the password.
	reasonAmbiguousEscape = "ambiguousescape"
	// A second @ in a SOCKS proxy (user:pass@host:port): rclone splits at
	// the first, so a user name or password can't hold one there, and the
	// rest would reach its error as the proxy's host.
	reasonProxyAt = "proxyat"
	// A character that looks like one of @ : / ? # . but isn't (lookalike).
	reasonLookalike = "lookalike"
)

// What became of a user name and password typed in an address, on a kind
// with User name and Password settings. One rule, here only: when User name
// and Password are both empty, they move there; when either holds
// something, those settings are what signs in, and the address's copy is
// taken out (rclone sends the settings and ignores the address's anyway).
const (
	loginMoved       = "moved"       // a user name and password moved to User name and Password
	loginMovedUser   = "moveduser"   // a user name alone moved to User name
	loginDropped     = "dropped"     // taken out of the address; User name and Password sign in
	loginDroppedUser = "droppeduser" // the same, for a user name typed alone
)

// settingsError is SetRemote's refusal of an address: Driveway's own words,
// the setting it is in and why, never the address.
type settingsError struct{ field, reason string }

func (e *settingsError) Error() string {
	return fmt.Sprintf("bad settings: the address in %s can't be used (%s)", e.field, e.reason)
}

// verdict is what checkSettings decides.
type verdict struct {
	// Refused: the setting, and why (a reason above).
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Accepted: the settings to use (an address's scheme in lower case,
	// a user name and password moved or taken out), and what became of a
	// user name and password typed in an address, and where.
	Login      string `json:"login,omitempty"`
	LoginField string `json:"login_field,omitempty"`
}

// CheckSettings checks the addresses in a server's settings (params, JSON)
// for the service backend, as SetRemote will, and changes nothing: the app's
// form calls it. It returns JSON: {"category":"badsettings","field":...,
// "reason":...} for an address that can't be used, or {"settings":{...},
// "login":...,"login_field":...} with the settings SetRemote would use.
func CheckSettings(backend, params string) string {
	out := map[string]any{}
	ri, err := fs.Find(backend)
	var m map[string]any
	if err == nil {
		err = json.Unmarshal([]byte(params), &m)
	}
	if err != nil {
		out["category"] = "badsettings"
	} else if v := checkSettings(ri, m); v.Reason != "" {
		out["category"], out["field"], out["reason"] = "badsettings", v.Field, v.Reason
	} else {
		out["settings"], out["login"], out["login_field"] = m, v.Login, v.LoginField
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// checkAddresses runs checkSettings for SetRemote and ConfigStart: an
// address that can't be used is a settings error.
func checkAddresses(ri *fs.RegInfo, m map[string]any) error {
	if v := checkSettings(ri, m); v.Reason != "" {
		return tag("badsettings", &settingsError{v.Field, v.Reason})
	}
	return nil
}

// checkSettings checks every address setting the service has, in m, and
// changes m into the settings to use. Pure: no store, no lock, no network.
func checkSettings(ri *fs.RegInfo, m map[string]any) verdict {
	login := hasOption(ri, "user") && hasOption(ri, "pass")
	var out verdict
	for _, o := range ri.Options {
		if !isAddress(o) {
			continue
		}
		v, ok := m[o.Name]
		if !ok {
			continue
		}
		s := strings.TrimSpace(fmtValue(v))
		if s == "" {
			continue
		}
		name := ri.Name + "." + o.Name
		a := readAddress(s, o.Name)
		if a.reason == "" && a.hasUser {
			switch {
			case loginInAddress[name] != "":
				a.addr = a.whole
			case !login:
				a.reason = reasonCredentials
			}
		}
		if a.reason == "" && a.at && !a.hasUser && atInAddress[name] == "" && loginInAddress[name] == "" {
			a.reason = reasonCredentials
		}
		if a.reason == "" && a.at && atInAddress[name] != "" && !nodeID.MatchString(s[:strings.LastIndex(s, "@")]) {
			a.reason = reasonCredentials
		}
		if a.reason == "" && o.Name == "socks_proxy" && strings.Count(s, "@") > 1 {
			a.reason = reasonProxyAt
		}
		if a.reason == reasonLogin && !login {
			a.reason = reasonEscape
		}
		if a.reason == reasonAmbiguous && loginInAddress[name] != "" {
			a.reason = reasonAmbiguousEscape
		}
		if a.reason != "" {
			return verdict{Field: o.Name, Reason: a.reason}
		}
		if a.addr != fmtValue(v) {
			m[o.Name] = a.addr
		}
		if !a.hasUser || !login || loginInAddress[name] != "" {
			continue
		}
		out.LoginField = o.Name
		if fmtValue(m["user"]) == "" && fmtValue(m["pass"]) == "" {
			m["user"] = a.user
			out.Login = loginMovedUser
			if a.hasPass {
				m["pass"] = a.pass
				out.Login = loginMoved
			}
		} else if a.hasPass {
			out.Login = loginDropped
		} else {
			out.Login = loginDroppedUser
		}
	}
	return out
}

// isAddress: the setting holds an address (and isn't a switch about one,
// like use_accelerate_endpoint).
func isAddress(o fs.Option) bool {
	if _, isBool := o.Default.(bool); isBool {
		return false
	}
	return addressName.MatchString(o.Name)
}

func hasOption(ri *fs.RegInfo, name string) bool {
	for _, o := range ri.Options {
		if o.Name == name {
			return true
		}
	}
	return false
}

// The rule for an address that reads two ways, in one sentence: when what
// follows the last colon of an address's authority is empty or only digits,
// and an @ comes anywhere after it, the address reads as
// https://user:pass/word@host as easily as https://host:port/folder@x,
// whatever the host or the user name looks like.
//
// ambiguousAt applies it to rest, an address after its scheme's //. The
// authority is Go's reading of it: up to the first / ? or #. It returns
// where the @ that would end a user name and password is (the last @), or
// -1. Both readers use it as it is:
//   - The address check refuses such an address, so nothing in it is moved
//     and nothing is kept: a password in an accepted address would go with
//     every request to the wrong host, and through each service's own way
//     of building addresses into errors no redactor can predict. An @ in a
//     folder's name after a port is written %40 (the refusal says so).
//   - The redactor hides from the // to the last @, for text the check
//     never saw (an error that quotes an address a service built).
func ambiguousAt(rest string) int {
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		return -1
	}
	hostport := rest[:end]
	if a := strings.LastIndex(hostport, "@"); a >= 0 {
		hostport = hostport[a+1:]
	}
	c := strings.LastIndex(hostport, ":")
	if c < 0 || strings.Contains(hostport[c:], "]") || strings.Trim(hostport[c+1:], "0123456789") != "" {
		return -1
	}
	a := strings.LastIndex(rest[end:], "@")
	if a < 0 {
		return -1
	}
	return end + a
}

// nodeID: what goes before the @ in a Storj satellite address (a node's ID,
// letters and digits); anything else there is a sign-in typed by mistake.
var nodeID = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// readAddress is what an address typed in the setting named name comes to.
type address struct {
	reason string // why it can't be used, or ""
	whole  string // as typed, with its scheme in lower case
	addr   string // and without a user name and password
	// A user name and password in it, decoded.
	user, pass       string
	hasUser, hasPass bool
	// An @ in an address without a scheme (where one is a sign-in, or a typo).
	at bool
}

// schemeAhead is a scheme and at least one slash: https://, or a typo with
// one slash (https:/). host:port has no slash after its colon.
var schemeAhead = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*):/`)

func readAddress(s, name string) address {
	if lookalike(s) {
		return address{reason: reasonLookalike}
	}
	hostOnly := hostName.MatchString(name)
	m := schemeAhead.FindStringSubmatch(s)
	if m == nil {
		switch {
		case name == "url" || name == "http_proxy":
			// rclone reads these as whole addresses (a proxy's with
			// url.Parse, whose error would repeat a sign-in typed in it).
			return address{reason: reasonScheme}
		case strings.HasPrefix(s, "/"):
			// //alice:pw@host, or a path: rclone would put https:// before it.
			if strings.Contains(s, "@") {
				return address{reason: reasonCredentials}
			}
			return address{reason: reasonScheme}
		}
		return address{whole: s, addr: s, at: strings.Contains(s, "@")}
	}
	if hostOnly {
		return address{reason: reasonHostOnly}
	}
	scheme := strings.ToLower(m[1])
	rest := s[len(m[1])+1:]
	if !strings.HasPrefix(rest, "//") {
		// https:/host: rclone checks for "https://" and would add another.
		return address{reason: reasonUnreadable}
	}
	if scheme != "http" && scheme != "https" {
		return address{reason: reasonScheme}
	}
	whole := scheme + ":" + rest
	rest = rest[2:]
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	authority, after := rest[:end], rest[end:]
	// Decided on the text as typed, before Go's reading of it is trusted
	// and before any user name is taken from it: an address that reads two
	// ways moves nothing and keeps nothing.
	if ambiguousAt(rest) >= 0 {
		return address{reason: reasonAmbiguous}
	}
	at := strings.LastIndex(authority, "@")
	bare := scheme + "://" + authority[at+1:] + after
	u, err := url.Parse(whole)
	if err != nil {
		// Without the user name and password, does it read? Then they are
		// the trouble.
		if _, err2 := url.Parse(bare); at >= 0 && err2 == nil {
			return address{reason: reasonLogin}
		}
		if strings.Contains(err.Error(), "invalid port") {
			return address{reason: reasonPort}
		}
		return address{reason: reasonUnreadable}
	}
	if u.Host == "" || u.Hostname() == "" {
		return address{reason: reasonUnreadable}
	}
	a := address{whole: whole, addr: whole}
	if u.User != nil {
		a.addr = bare
		a.user = u.User.Username()
		a.pass, a.hasPass = u.User.Password()
		a.hasUser = true
	}
	return a
}

// The look-alike rule, in one sentence: an address setting may not hold a
// look-alike of @ anywhere, nor a look-alike of : / ? # or the dot in its
// authority (from the start to the first ASCII / ? or # after the scheme's
// //), nor a look-alike of : / ? # in its path when the address holds an @.
//
// A look-alike is a character whose NFKC form differs from it and holds one
// of @ : / ? # or the dot (＠ ﹫ ： ﹕ ／ ？ ﹖ ＃ ﹟ ． ⁉ ⁇ ℀ …), or one in
// shapeLookalikes, which NFKC leaves alone. Go's url package reads only the
// ASCII ones, and the rule covers the two ways that leaked a password:
//   - a look-alike @ where the @ before a host goes:
//     https://alice:2024/pass＠host reads as the host alice, the port 2024
//     and a folder, the two-ways rule (ambiguousAt) finds no @ to refuse it
//     by, and every error that quotes the address repeats the password (the
//     look-alike written as %EF%BC%A0);
//   - a look-alike colon in the authority, which Koofr's client rewrites
//     into an address that keeps the password.
//
// Anywhere else the separators only matter around an @ (a password needs
// one after it to end it), so without one a folder may be named with the
// full-width colon, question mark, number sign or solidus that Chinese and
// Japanese file names use, and dot forms in a folder are always accepted.
// Letters in any script stay: an international host name (bücher.example)
// and folders named in any script are accepted.
func lookalike(s string) bool {
	// The authority: from the start to the first / ? or # after the
	// scheme's // (or after the start, without a scheme).
	hostStart := 0
	if m := schemeAhead.FindStringIndex(s); m != nil {
		hostStart = m[1]
		for hostStart < len(s) && s[hostStart] == '/' {
			hostStart++
		}
	}
	hostEnd := len(s)
	if i := strings.IndexAny(s[hostStart:], "/?#"); i >= 0 {
		hostEnd = hostStart + i
	}
	at := strings.Contains(s, "@")
	for i, r := range s {
		if r < 0x80 {
			continue
		}
		shape := shapeLookalikes[r]
		if shape == "" {
			if n := norm.NFKC.String(string(r)); n != string(r) {
				shape = n
			}
		}
		switch {
		case strings.Contains(shape, "@"):
			return true
		case i < hostEnd && strings.ContainsAny(shape, ":/?#."):
			return true
		case at && strings.ContainsAny(shape, ":/?#"):
			return true
		}
	}
	return false
}

// shapeLookalikes are confusable forms of @ : / and the dot that NFKC leaves
// as they are, with the character each looks like: colons (the modifier
// letter colon, the ratio sign, the raised colon, the Armenian full stop,
// the Hebrew sof pasuq), slashes (the division and fraction slashes, the big
// solidus), and the ideographic full stops a Japanese or Chinese keyboard
// types for a dot (which international host names read as one).
var shapeLookalikes = map[rune]string{
	'\ua789': ":", '\u2236': ":", '\u02f8': ":", '\u0589': ":", '\u05c3': ":",
	'\u2215': "/", '\u2044': "/", '\u29f8': "/",
	'\u3002': ".", '\uff61': ".",
}

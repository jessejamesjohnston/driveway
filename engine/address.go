// SPDX-License-Identifier: Apache-2.0

package engine

// Addresses in the settings (a WebDAV or HTTP server's url, a service's
// endpoint), checked before rclone sees them, with the same rules as the
// app's form (Kinds.urlParts). Go's url package repeats an address it can't
// read in its error, password and all, and a user name and password typed
// in an address would otherwise ride along in every error that names it.

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/rclone/rclone/fs"
)

// urlSettings hold a whole address. Only url needs its scheme: an endpoint
// may be a bare host.
var urlSettings = []string{"url", "endpoint", "api_url", "auth_url", "token_url"}

var urlScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// errAddress is what SetRemote says about an address it refuses: Driveway's
// own words, never the address.
var errAddress = errors.New("bad settings: an address in the settings can't be read")

// checkAddresses refuses an address Go can't read, or a url without a
// scheme, and, for a service with user and pass settings of its own (WebDAV,
// Seafile), moves a user name and password typed in an address there: into
// a setting that is empty (the form has already moved what was typed; this
// is for settings saved before it did), and out of the address either way
// (where both are set, rclone sends the settings' and ignores the
// address's). A service without them (HTTP) keeps them in the address.
func checkAddresses(ri *fs.RegInfo, m map[string]any) error {
	login := hasOption(ri, "user") && hasOption(ri, "pass")
	for _, k := range urlSettings {
		v, ok := m[k]
		if !ok {
			continue
		}
		s := strings.TrimSpace(fmtValue(v))
		if s == "" {
			continue
		}
		addr, user, pass, hasUser, hasPass, ok := urlParts(s, k == "url")
		if !ok {
			return tag("badsettings", errAddress)
		}
		if !hasUser || !login {
			continue
		}
		m[k] = addr
		if fmtValue(m["user"]) == "" {
			m["user"] = user
		}
		if hasPass && fmtValue(m["pass"]) == "" {
			m["pass"] = pass
		}
	}
	return nil
}

func hasOption(ri *fs.RegInfo, name string) bool {
	for _, o := range ri.Options {
		if o.Name == name {
			return true
		}
	}
	return false
}

// urlParts takes an address apart as Kinds.urlParts does: the address
// without its user name and password, and those decoded. ok is false for
// what Go can't read: no scheme where one is needed (or a user name without
// one), a user name or password with a character an address can't hold or
// a broken % escape, or a port that isn't a number.
func urlParts(s string, needScheme bool) (addr, user, pass string, hasUser, hasPass, ok bool) {
	scheme := urlScheme.FindString(s)
	if scheme == "" {
		authority, _, _ := strings.Cut(s, "/")
		authority = strings.SplitN(strings.SplitN(authority, "?", 2)[0], "#", 2)[0]
		return s, "", "", false, false, !needScheme && !strings.Contains(authority, "@")
	}
	rest := s[len(scheme):]
	end := strings.IndexAny(rest, "/?#")
	if end < 0 {
		end = len(rest)
	}
	authority, after := rest[:end], rest[end:]
	at := strings.LastIndex(authority, "@")
	host := authority[at+1:]
	if at >= 0 && !cleanUserinfo(authority[:at]) || !cleanHostPort(host) {
		return s, "", "", false, false, false
	}
	u, err := url.Parse(s)
	if err != nil {
		return s, "", "", false, false, false
	}
	if at < 0 || u.User == nil {
		return s, "", "", false, false, true
	}
	pass, hasPass = u.User.Password()
	return scheme + host + after, u.User.Username(), pass, true, hasPass, true
}

// cleanUserinfo: the characters Go's url package reads in a user name and
// password, and % escapes.
func cleanUserinfo(u string) bool {
	for i := 0; i < len(u); i++ {
		c := u[i]
		switch {
		case c == '%':
			if i+2 >= len(u) || !isHex(u[i+1]) || !isHex(u[i+2]) {
				return false
			}
			i += 2
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9',
			strings.IndexByte("-._:~!$&'()*+,;=@", c) >= 0:
		default:
			return false
		}
	}
	return true
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// cleanHostPort: a host, then a port that is a number if there is one; an
// IPv6 address in brackets.
func cleanHostPort(h string) bool {
	var port string
	if strings.HasPrefix(h, "[") {
		close := strings.Index(h, "]")
		if close < 0 {
			return false
		}
		port = h[close+1:]
		if port == "" {
			return true
		}
		if !strings.HasPrefix(port, ":") {
			return false
		}
		port = port[1:]
	} else {
		c := strings.LastIndex(h, ":")
		if c < 0 {
			return true
		}
		port = h[c+1:]
	}
	return strings.Trim(port, "0123456789") == ""
}

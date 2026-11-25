// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/obscure"
)

// addressCase is one row of test/address_cases.json, which the app's
// KindsTest reads too.
type addressCase struct {
	Why        string            `json:"why"`
	Backend    string            `json:"backend"`
	Settings   map[string]string `json:"settings"`
	Field      string            `json:"field"`
	Reason     string            `json:"reason"`
	Out        map[string]string `json:"settings_out"`
	Login      string            `json:"login"`
	LoginField string            `json:"login_field"`
}

func addressCases(t *testing.T) []addressCase {
	t.Helper()
	b, err := os.ReadFile("../test/address_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Cases []addressCase }
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("no cases")
	}
	return doc.Cases
}

func (c addressCase) params(t *testing.T) string {
	b, err := json.Marshal(c.Settings)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAddressCases: CheckSettings gives each shared case's verdict, and its
// reasons never repeat what was typed.
func TestAddressCases(t *testing.T) {
	for _, c := range addressCases(t) {
		var got struct {
			Category   string            `json:"category"`
			Field      string            `json:"field"`
			Reason     string            `json:"reason"`
			Settings   map[string]string `json:"settings"`
			Login      string            `json:"login"`
			LoginField string            `json:"login_field"`
		}
		raw := CheckSettings(c.Backend, c.params(t))
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if c.Reason != "" {
			if got.Category != "badsettings" || got.Field != c.Field || got.Reason != c.Reason {
				t.Errorf("%s %v (%s): got %s, want %s %s", c.Backend, c.Settings, c.Why, raw, c.Field, c.Reason)
			}
			if strings.Contains(raw, "MADEUP") {
				t.Errorf("%s: the verdict repeats the password: %s", c.Why, raw)
			}
			continue
		}
		if got.Category != "" || !reflect.DeepEqual(got.Settings, c.Out) || got.Login != c.Login || got.LoginField != c.LoginField {
			t.Errorf("%s %v (%s): got %s, want %v login %q in %q", c.Backend, c.Settings, c.Why, raw, c.Out, c.Login, c.LoginField)
		}
	}
}

// TestAddressAtTheDoor: SetRemote takes the same verdict as CheckSettings
// (one check), refuses in Driveway's own words, never the address, and
// stores what it accepts with the password obscured.
func TestAddressAtTheDoor(t *testing.T) {
	const id = "addr-door"
	t.Cleanup(func() { RemoveRemote(id) })
	get := func(key string) string {
		v, _ := store.GetValue(id, key)
		return v
	}
	for _, c := range addressCases(t) {
		err := SetRemote(id, c.Backend, c.params(t))
		if c.Reason != "" {
			want := "[badsettings] bad settings: the address in " + c.Field + " can't be used (" + c.Reason + ")"
			if err == nil || err.Error() != want {
				t.Errorf("%s: got %v, want %q", c.Why, err, want)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.Why, err)
			continue
		}
		for k, v := range c.Out {
			s := get(k)
			if k == "pass" {
				if s, err = obscure.Reveal(s); err != nil {
					t.Fatal(err)
				}
			}
			if k == "host" {
				v = hostFor(c.Backend, v)
			}
			if s != v {
				t.Errorf("%s: %s stored as %q, want %q", c.Why, k, s, v)
			}
		}
		if _, set := c.Out["pass"]; !set && get("pass") != "" {
			t.Errorf("%s: a password was stored", c.Why)
		}
	}
}

// TestAddressVerdictsJSON writes the engine's verdict for every shared case
// where the app's JVM checks read it (DRIVEWAY_VERDICTS_JSON, set by
// ./build.sh): their stand-in for CheckSettings answers with the real
// engine's output, so KindsTest checks the form against this engine, not a
// copy of it.
func TestAddressVerdictsJSON(t *testing.T) {
	path := os.Getenv("DRIVEWAY_VERDICTS_JSON")
	if path == "" {
		t.Skip("DRIVEWAY_VERDICTS_JSON not set")
	}
	type row struct {
		Backend  string            `json:"backend"`
		Settings map[string]string `json:"settings"`
		Verdict  json.RawMessage   `json:"verdict"`
	}
	var out []row
	for _, c := range addressCases(t) {
		out = append(out, row{c.Backend, c.Settings, json.RawMessage(CheckSettings(c.Backend, c.params(t)))})
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// notAddress are settings rclone describes with the words of an address
// (URL, endpoint, host, server, domain) that hold none, each with why.
var notAddress = map[string]string{
	"local.no_clone":                      "a switch",
	"cloudinary.upload_prefix":            "a region's name prefix, not an address",
	"doi.doi":                             "a DOI, or a doi.org link to one: rclone reads it itself",
	"ftp.no_check_certificate":            "a switch",
	"ftp.disable_epsv":                    "a switch",
	"ftp.disable_mlsd":                    "a switch",
	"ftp.disable_utf8":                    "a switch",
	"ftp.disable_tls13":                   "a switch",
	"http.no_escape":                      "a switch",
	"internetarchive.disable_checksum":    "a switch",
	"internetarchive.wait_archive":        "a duration",
	"seafile.user":                        "a user name",
	"sftp.known_hosts_file":               "a file's path",
	"sftp.pin_host_key":                   "a switch",
	"sftp.host_keys":                      "host keys",
	"sftp.shell_type":                     "a choice",
	"sftp.subsystem":                      "a command",
	"sftp.server_command":                 "a command",
	"sftp.host_key_algorithms":            "a list of algorithms",
	"sftp.copy_is_hardlink":               "a switch",
	"smb.domain":                          "an NTLM domain name (WORKGROUP), not a host",
	"smb.case_insensitive":                "a switch",
	"azurefiles.username":                 "a user name",
	"azureblob.username":                  "a user name",
	"webdav.unix_socket":                  "a socket's path",
	"b2.sse_customer_algorithm":           "an algorithm's name",
	"s3.server_side_encryption":           "an algorithm's name",
	"s3.sse_customer_algorithm":           "an algorithm's name",
	"s3.force_path_style":                 "a switch",
	"s3.use_dual_stack":                   "a switch",
	"s3.use_accelerate_endpoint":          "a switch",
	"s3.list_url_encode":                  "a choice",
	"s3.use_x_id":                         "a choice",
	"swift.domain":                        "a Keystone domain name, not a host",
	"swift.tenant_domain":                 "a Keystone domain name, not a host",
	"swift.auth_version":                  "a number",
	"swift.endpoint_type":                 "a choice",
	"jottacloud.no_versions":              "a switch",
	"onedrive.server_side_across_configs": "a switch",
	"onedrive.av_override":                "a switch",
	"box.owned_by":                        "an email address, not a server",
	"drive.copy_shortcut_content":         "a switch",
	"drive.server_side_across_configs":    "a switch",
}

// describesAddress: a setting's first help line uses the words of an address.
var describesAddress = regexp.MustCompile(`(?i)\burl\b|endpoint|\bhost|address|server|domain`)

// TestEveryAddressSettingClassified: every setting of every compiled-in
// service that rclone describes as a URL, an endpoint, a host or an address
// is either checked as one (addressName) or in notAddress with a reason, so
// a setting a new rclone adds can't slip past the check. The exceptions for
// a user name and password, or an @, in an address must still exist.
func TestEveryAddressSettingClassified(t *testing.T) {
	seen := map[string]bool{}
	for _, ri := range fs.Registry {
		for _, o := range ri.Options {
			name := ri.Name + "." + o.Name
			seen[name] = true
			first, _, _ := strings.Cut(o.Help, "\n")
			if isAddress(o) {
				if notAddress[name] != "" {
					t.Errorf("%s is checked as an address and listed in notAddress", name)
				}
				continue
			}
			if addressName.MatchString(o.Name) {
				if _, isBool := o.Default.(bool); !isBool {
					t.Errorf("%s: named like an address but not checked", name)
				}
				continue
			}
			if describesAddress.MatchString(first) && notAddress[name] == "" {
				t.Errorf("%s (%q): an address setting the check doesn't know; add it to addressName or notAddress", name, first)
			}
		}
	}
	for _, m := range []map[string]string{loginInAddress, atInAddress} {
		for name := range m {
			if !seen[name] {
				t.Errorf("%s: an exception for a setting rclone no longer has", name)
			}
		}
	}
	for name := range notAddress {
		if !seen[name] {
			t.Errorf("notAddress names %s, which rclone no longer has", name)
		}
	}
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

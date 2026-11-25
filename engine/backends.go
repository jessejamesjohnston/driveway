// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"

	_ "github.com/jessejamesjohnston/driveway/engine/nfs"
)

// hidden are rclone backends that make no sense as a Driveway server: the
// device's own storage, a test backend and a deprecated name. (Services
// taken out altogether are in removedBackends.)
var hidden = map[string]bool{"local": true, "memory": true, "tardigrade": true}

// readOnly are the services rclone can only read (a web server's folder
// listing, DOI datasets): their uploads, new folders, renames and deletes
// always fail, so Files offers none of them there.
var readOnly = map[string]bool{"doi": true, "http": true}

type option struct {
	Name     string   `json:"name"`
	Help     string   `json:"help"`
	Default  string   `json:"default,omitempty"`
	Required bool     `json:"required,omitempty"`
	Password bool     `json:"password,omitempty"`
	Advanced bool     `json:"advanced,omitempty"`
	Examples []string `json:"examples,omitempty"`
}

type backend struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Label: the service's name as people call it.
	Label string `json:"label"`
	// Method: how it is set up (methodSignIn, methodForm, methodAlt, methodUnavailable).
	Method string `json:"method"`
	// Essentials: the settings above the Connect button, in order.
	Essentials []essentialOut `json:"essentials,omitempty"`
	// Options: every other setting, for "More settings".
	Options []option `json:"options"`
	// BrowserSignIn: the service signs in through its own web page, with
	// rclone's shared sign-in settings (a server set up that way has a token).
	BrowserSignIn bool `json:"browserSignIn,omitempty"`
	// BorrowedSignIn: the service's sign-in presents an app registration
	// built into rclone, not Merriment Labs' own (see borrowedSignIn).
	BorrowedSignIn bool `json:"borrowedSignIn,omitempty"`
	// OwnClientID: Merriment Labs' registration with the service. A server
	// that signed in before it existed has no client_id, and keeps rclone's
	// until it signs in again.
	OwnClientID string            `json:"ownClientId,omitempty"`
	FolderHint  string            `json:"folderHint,omitempty"`
	Blurb       string            `json:"blurb,omitempty"`
	Alt         *alt              `json:"alt,omitempty"`
	Prefill     map[string]string `json:"prefill,omitempty"`
	SkipSetup   bool              `json:"skipSetup,omitempty"`
	// State: stateListed or stateHidden (listed nowhere, but a server of it
	// added before still opens and can be changed).
	State string `json:"state"`
	// Since: the app version that first offered it (for its New label), or
	// empty for a service offered before New labels began.
	Since string `json:"since,omitempty"`
	// ReadOnly: rclone can only read it (readOnly).
	ReadOnly bool `json:"readOnly,omitempty"`
}

// essentialOut is an essential with what rclone says about it.
type essentialOut struct {
	essential
	// Password: rclone keeps it scrambled in its settings (the app keeps it
	// as typed).
	Password bool     `json:"password,omitempty"`
	Default  string   `json:"default,omitempty"`
	Examples []string `json:"examples,omitempty"`
}

// borrowedSignIn are the services whose sign-in uses an app registration
// built into rclone: rclone's own client ID with a browser sign-in
// (oauthutil.ConfigOut), for Jottacloud its "jottacli" client, for SugarSync
// rclone's app keys. A published app may not present itself as another
// developer's app, so their forms (forms.go) offer no browser sign-in: each
// has no way in (methodUnavailable), or is set up another way rclone supports (methodAlt). Dropbox and OneDrive sign in
// with Merriment Labs' own registrations (ownRegistration, pkce.go). Every
// service with rclone's shared sign-in settings (oauthutil.SharedOptions)
// must be here, in ownRegistration or in ownSignIn: TestBorrowedSignIn
// fails on a new one after an rclone upgrade.
var borrowedSignIn = map[string]bool{
	"box": true, "drive": true, "google cloud storage": true,
	"hidrive": true, "huaweidrive": true, "jottacloud": true, "pcloud": true,
	"premiumizeme": true, "putio": true, "yandex": true, "zoho": true,
	// No browser, but rclone's own SugarSync app keys: the same borrowing.
	"sugarsync": true,
}

// ownSignIn use rclone's sign-in settings without a browser or rclone's
// registration: Mail.ru signs in with the account's user name and an app
// password.
var ownSignIn = map[string]bool{"mailru": true}

// browserSignIn reports whether a service signs in through its own web page
// with rclone's shared sign-in settings (oauthutil.SharedOptions).
func browserSignIn(ri *fs.RegInfo) bool {
	if ownSignIn[ri.Name] {
		return false
	}
	for _, o := range ri.Options {
		if o.Name == config.ConfigClientCredentials {
			return true
		}
	}
	return false
}

// formFor is the service's form; a service rclone added since the list was
// last curated gets its required settings as essentials (TestForms fails
// until it is curated).
func formFor(ri *fs.RegInfo) form {
	if f, ok := forms[ri.Name]; ok {
		return f
	}
	f := form{Label: ri.Description, Method: methodForm}
	if browserSignIn(ri) {
		f.Method, f.State = methodUnavailable, stateHidden
	}
	seen := map[string]bool{}
	for _, o := range ri.Options {
		if o.Required && !o.Advanced && !seen[o.Name] && o.Hide&fs.OptionHideConfigurator == 0 {
			seen[o.Name] = true
			f.Essentials = append(f.Essentials, essential{Key: o.Name, Label: o.Name, Required: true, Secret: o.IsPassword})
		}
	}
	return f
}

// Backends lists the services Driveway can connect to, with their forms,
// as JSON. The app builds its setup forms from this.
func Backends() string {
	var out []backend
	for _, ri := range fs.Registry {
		if hidden[ri.Name] || ri.Hide {
			continue
		}
		f := formFor(ri)
		b := backend{Name: ri.Name, Description: ri.Description, Label: f.Label, Method: f.Method,
			BrowserSignIn: browserSignIn(ri), BorrowedSignIn: borrowedSignIn[ri.Name], FolderHint: f.FolderHint,
			Blurb: f.Blurb, Alt: f.Alt, Prefill: f.Prefill, SkipSetup: f.SkipSetup,
			OwnClientID: ownRegistration[ri.Name].ClientID, State: f.State, Since: f.Since, ReadOnly: readOnly[ri.Name]}
		if b.State == "" {
			b.State = stateListed
		}
		if handMade[ri.Name] {
			b.Method = methodForm
		}
		skip := map[string]bool{}
		for _, k := range f.Hidden {
			skip[k] = true
		}
		// The client ID and secret are for signing in through the browser
		// with your own app registration: offered (under More) only where
		// the browser sign-in is.
		if f.Method != methodSignIn {
			skip[config.ConfigClientID], skip[config.ConfigClientSecret] = true, true
		}
		for _, e := range f.Essentials {
			skip[e.Key] = true
			eo := essentialOut{essential: e}
			for _, o := range ri.Options {
				if o.Name != e.Key {
					continue
				}
				eo.Password = o.IsPassword
				if o.Default != nil {
					if s := fmt.Sprint(o.Default); s != "" && s != "<nil>" && s != "[]" {
						eo.Default = s
					}
				}
				for _, x := range o.Examples {
					eo.Examples = append(eo.Examples, x.Value)
				}
				break // the first of a name (Koofr has one password per provider)
			}
			b.Essentials = append(b.Essentials, eo)
		}
		for _, o := range ri.Options {
			if o.Hide&fs.OptionHideConfigurator != 0 || neverOnForm[o.Name] || skip[o.Name] {
				continue
			}
			skip[o.Name] = true // once per name
			op := option{Name: o.Name, Help: o.Help, Required: o.Required, Password: o.IsPassword, Advanced: o.Advanced}
			if o.Default != nil {
				if s := fmt.Sprint(o.Default); s != "" && s != "<nil>" {
					op.Default = s
				}
			}
			for _, e := range o.Examples {
				op.Examples = append(op.Examples, e.Value)
			}
			b.Options = append(b.Options, op)
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	j, _ := json.Marshal(out)
	return string(j)
}

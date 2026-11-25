// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	boxapi "github.com/rclone/rclone/backend/box/api"
	jottaapi "github.com/rclone/rclone/backend/jottacloud/api"
	"github.com/youmark/pkcs8"
)

// checked is what the app shows for a credential that looks right: a short
// line naming it, and settings it fills in (a key's project).
type checked struct {
	Summary string `json:"summary"`
	// Value: the credential as Driveway keeps it (a JSON file on one line:
	// rclone's settings can't hold a line break).
	Value string            `json:"value"`
	Fill  map[string]string `json:"fill,omitempty"`
}

// CheckCredential looks at a key file or token before connecting, on the
// device, without sending it anywhere: that it is the kind of file the
// service needs and that its private key can be read. It returns JSON
// (checked), or an error tagged [credential] saying in plain words what is
// wrong (the one engine error the app shows as it is).
func CheckCredential(backend, key, value string) (_ string, err error) {
	defer scrub(&err)
	var c checked
	switch {
	case key == "service_account_credentials":
		c, err = checkServiceAccount(value)
	case backend == "box" && key == "config_credentials":
		c, err = checkBoxConfig(value)
	case backend == "jottacloud" && key == "config_login_token":
		c, err = checkJottaToken(value)
	default:
		return "", tag("other", fmt.Errorf("nothing to check for %s", key))
	}
	if err != nil {
		return "", tag("credential", err)
	}
	c.Value = oneLine(strings.TrimSpace(value))
	b, _ := json.Marshal(c)
	return string(b), nil
}

// oneLine puts a JSON file's contents on one line (rclone's settings can't
// hold a line break; inside JSON strings they are already escaped). Any
// other value is left exactly as it is.
func oneLine(value string) string {
	t := strings.TrimSpace(value)
	if strings.ContainsAny(value, "\r\n") && strings.HasPrefix(t, "{") {
		var b bytes.Buffer
		if json.Compact(&b, []byte(t)) == nil {
			return b.String()
		}
	}
	return value
}

func checkServiceAccount(value string) (checked, error) {
	var sa struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		ProjectID   string `json:"project_id"`
	}
	notKey := errors.New("That file isn't a service account key. In the Google Cloud console, open the service " +
		"account, then Keys, Add key, JSON.")
	if json.Unmarshal([]byte(value), &sa) != nil || sa.Type != "service_account" || sa.ClientEmail == "" {
		return checked{}, notKey
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return checked{}, notKey
	}
	if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
		if _, err2 := x509.ParsePKCS1PrivateKey(block.Bytes); err2 != nil {
			return checked{}, errors.New("The key in that file can't be read. Make a new JSON key in the Google Cloud console.")
		}
	}
	c := checked{Summary: sa.ClientEmail}
	if sa.ProjectID != "" {
		c.Fill = map[string]string{"project_number": sa.ProjectID}
	}
	return c, nil
}

func checkBoxConfig(value string) (checked, error) {
	var cfg boxapi.ConfigJSON
	if json.Unmarshal([]byte(value), &cfg) != nil || cfg.BoxAppSettings.ClientID == "" || cfg.BoxAppSettings.AppAuth.PrivateKey == "" {
		return checked{}, errors.New("That file isn't a Box app's settings file. In the Box Developer Console, open the " +
			"app, then Configuration, and generate a key pair to download config.json.")
	}
	block, _ := pem.Decode([]byte(cfg.BoxAppSettings.AppAuth.PrivateKey))
	if block == nil {
		return checked{}, errors.New("The private key in that file can't be read. Generate a new key pair in the Box Developer Console.")
	}
	if _, err := pkcs8.ParsePKCS8PrivateKey(block.Bytes, []byte(cfg.BoxAppSettings.AppAuth.Passphrase)); err != nil {
		return checked{}, errors.New("The private key in that file doesn't open with its passphrase. Generate a new key pair in the Box Developer Console.")
	}
	if cfg.EnterpriseID == "" {
		return checked{}, errors.New("That settings file has no enterprise ID. Download it again after the app is authorized.")
	}
	return checked{Summary: "Box app " + cfg.BoxAppSettings.ClientID}, nil
}

func checkJottaToken(value string) (checked, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	var t jottaapi.LoginToken
	if err != nil || json.Unmarshal(b, &t) != nil || t.Username == "" || t.WellKnownLink == "" || t.AuthToken == "" {
		return checked{}, errors.New("That isn't a personal login token. Copy the whole token from jottacloud.com/web/secure.")
	}
	return checked{Summary: t.Username}, nil
}

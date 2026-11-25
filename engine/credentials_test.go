// SPDX-License-Identifier: Apache-2.0

package engine

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/youmark/pkcs8"
)

// serviceAccount is a made-up Google service account key whose token
// address is tokenURL.
func serviceAccount(t *testing.T, tokenURL string) string {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(k)
	key := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	// Indented, as the console's download is: several lines must survive
	// the form, the server list and the engine's settings.
	b, _ := json.MarshalIndent(map[string]string{
		"type": "service_account", "project_id": "made-up-project", "private_key_id": "1",
		"private_key": key, "client_email": "driveway@made-up-project.iam.gserviceaccount.com",
		"client_id": "1", "token_uri": tokenURL,
	}, "", "  ")
	return string(b)
}

func TestCheckCredential(t *testing.T) {
	sa := serviceAccount(t, "https://oauth2.googleapis.com/token")
	out, err := CheckCredential("drive", "service_account_credentials", sa)
	if err != nil || !strings.Contains(out, "driveway@made-up-project") || !strings.Contains(out, `"project_number":"made-up-project"`) {
		t.Errorf("service account: %s %v", out, err)
	}
	for _, bad := range []string{"", "{}", `{"type":"authorized_user","client_email":"x"}`, "not json",
		strings.Replace(sa, "MII", "MIX", 1)} {
		if _, err := CheckCredential("google cloud storage", "service_account_credentials", bad); err == nil {
			t.Errorf("service account %.30q accepted", bad)
		}
	}

	// A Box app's config.json, its key encrypted with its passphrase.
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, err := pkcs8.MarshalPrivateKey(k, []byte("made-up-passphrase"), nil)
	if err != nil {
		t.Fatal(err)
	}
	box := func(pass, enterprise string) string {
		b, _ := json.Marshal(map[string]any{
			"boxAppSettings": map[string]any{"clientID": "madeupclient", "clientSecret": "s", "appAuth": map[string]string{
				"publicKeyID": "k1", "privateKey": string(pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: der})),
				"passphrase": pass}},
			"enterpriseID": enterprise,
		})
		return string(b)
	}
	if out, err := CheckCredential("box", "config_credentials", box("made-up-passphrase", "123")); err != nil || !strings.Contains(out, "madeupclient") {
		t.Errorf("box: %s %v", out, err)
	}
	if _, err := CheckCredential("box", "config_credentials", box("wrong", "123")); err == nil {
		t.Error("box: a wrong passphrase was accepted")
	}
	if _, err := CheckCredential("box", "config_credentials", box("made-up-passphrase", "")); err == nil {
		t.Error("box: no enterprise ID was accepted")
	}
	if _, err := CheckCredential("box", "config_credentials", sa); err == nil {
		t.Error("box: a Google key was accepted")
	}

	tok, _ := json.Marshal(map[string]string{"username": "sam", "realm": "jottacloud",
		"well_known_link": "https://id.jottacloud.com/.well-known", "auth_token": "made-up"})
	if out, err := CheckCredential("jottacloud", "config_login_token", base64.RawURLEncoding.EncodeToString(tok)+"\n"); err != nil || !strings.Contains(out, "sam") {
		t.Errorf("jottacloud: %s %v", out, err)
	}
	if _, err := CheckCredential("jottacloud", "config_login_token", "hunter2"); err == nil {
		t.Error("jottacloud: a password was accepted as a login token")
	}
}

// Google Cloud Storage with a service account key, end to end against a
// stand-in for Google: setup asks nothing and opens no browser, the key is
// traded for a token at the key's own token address, and the buckets and
// objects list.
func TestServiceAccountStorage(t *testing.T) {
	var mu sync.Mutex
	var tokens, listings int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || r.Form.Get("assertion") == "" {
				http.Error(w, "bad grant", http.StatusBadRequest)
				return
			}
			mu.Lock()
			tokens++
			mu.Unlock()
			fmt.Fprint(w, `{"access_token":"made-up-access","token_type":"Bearer","expires_in":3600}`)
		case r.Header.Get("Authorization") != "Bearer made-up-access":
			http.Error(w, `{"error":{"code":401,"message":"no token"}}`, http.StatusUnauthorized)
		case r.URL.Path == "/storage/v1/b":
			if r.URL.Query().Get("project") != "made-up-project" {
				http.Error(w, `{"error":{"code":400,"message":"project"}}`, http.StatusBadRequest)
				return
			}
			mu.Lock()
			listings++
			mu.Unlock()
			fmt.Fprint(w, `{"kind":"storage#buckets","items":[{"name":"family-photos"}]}`)
		case r.URL.Path == "/storage/v1/b/family-photos/o":
			fmt.Fprint(w, `{"kind":"storage#objects","items":[{"name":"beach.jpg","size":"5","updated":"2026-10-01T00:00:00Z","md5Hash":"XUFAKrxLKna5cZ2REBfFkg=="}]}`)
		default:
			http.Error(w, `{"error":{"code":404,"message":"`+url.PathEscape(r.URL.Path)+`"}}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	h := &testHost{saved: map[string]string{}}
	SetHost(h)
	sa := serviceAccount(t, srv.URL+"/token")
	c, err := CheckCredential("google cloud storage", "service_account_credentials", sa)
	if err != nil {
		t.Fatal(err)
	}
	var fill checked
	_ = json.Unmarshal([]byte(c), &fill)
	params, _ := json.Marshal(map[string]string{"service_account_credentials": sa,
		"project_number": fill.Fill["project_number"], "endpoint": srv.URL + "/storage/v1/"})
	out, err := ConfigStart("t-gcs", "google cloud storage", string(params))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"done":true`) {
		t.Fatalf("setup asked something: %s", out)
	}
	if len(h.urls) > 0 {
		t.Fatalf("setup opened a browser: %v", h.urls)
	}
	// As the app does after setup: the server's settings, then a check.
	values := ConfigValues("t-gcs")
	var kept map[string]string
	_ = json.Unmarshal([]byte(values), &kept)
	if kept["service_account_credentials"] != fill.Value || strings.Contains(fill.Value, "\n  ") {
		t.Fatalf("the key didn't survive setup intact (%d bytes kept of %d)", len(kept["service_account_credentials"]), len(fill.Value))
	}
	kept["endpoint"] = srv.URL + "/storage/v1/"
	p2, _ := json.Marshal(kept)
	if err := SetRemote("t-gcs", "google cloud storage", string(p2)); err != nil {
		t.Fatal(err)
	}
	defer RemoveRemote("t-gcs")
	if err := Check("t-gcs"); err != nil {
		t.Fatal(err)
	}
	top, err := ListNow("t-gcs", "")
	if err != nil || !strings.Contains(top, "family-photos") {
		t.Fatalf("buckets: %s %v", top, err)
	}
	files, err := ListNow("t-gcs", "family-photos")
	if err != nil || !strings.Contains(files, "beach.jpg") {
		t.Fatalf("objects: %s %v", files, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if tokens == 0 || listings == 0 {
		t.Fatalf("tokens %d, bucket listings %d", tokens, listings)
	}
}

// Google Drive with a service account key: setup finishes on the device
// with no browser and no question (the shared drive question is answered by
// the form's preset), and keeps the key.
func TestServiceAccountDriveSetup(t *testing.T) {
	h := &testHost{saved: map[string]string{}}
	SetHost(h)
	sa := serviceAccount(t, "http://127.0.0.1:1/token")
	// The form's presets apply by themselves (a folder link: no shared drive question).
	params, _ := json.Marshal(map[string]string{"service_account_credentials": sa, "root_folder_id": "made-up-folder"})
	out, err := ConfigStart("t-drive", "drive", string(params))
	if err != nil {
		t.Fatal(err)
	}
	defer RemoveRemote("t-drive")
	if !strings.Contains(out, `"done":true`) || len(h.urls) > 0 {
		t.Fatalf("setup: %s, browser %v", out, h.urls)
	}
	var kept map[string]string
	_ = json.Unmarshal([]byte(ConfigValues("t-drive")), &kept)
	if kept["service_account_credentials"] != oneLine(sa) || kept["root_folder_id"] != "made-up-folder" {
		t.Fatalf("kept: %v", keys(kept))
	}
	if _, ok := kept["config_change_team_drive"]; ok {
		t.Fatal("a setup answer was kept as a setting")
	}
}

// Jottacloud with a personal login token: the form's presets answer the
// setup's questions, so the only thing it needs from the person is the
// token (which is then traded with Jottacloud: not run here, it would reach
// the real service). A token that isn't one stops before any network call.
func TestJottacloudPresets(t *testing.T) {
	h := &testHost{saved: map[string]string{}}
	SetHost(h)
	params, _ := json.Marshal(map[string]string{"config_login_token": "not-a-token"})
	_, err := ConfigStart("t-jotta", "jottacloud", string(params))
	defer RemoveRemote("t-jotta")
	if err == nil || len(h.urls) > 0 {
		t.Fatalf("a bad token: err %v, browser %v", err, h.urls)
	}
	if !strings.Contains(err.Error(), "illegal base64") && !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("stopped somewhere else than reading the token: %v", err)
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Without a folder link, Drive's setup goes on to list the key's shared
// drives (here its token address refuses, so nothing reaches Google): the
// setup stops with an error instead of finishing on the service account's
// own empty Drive.
func TestServiceAccountDriveSharedDrives(t *testing.T) {
	h := &testHost{saved: map[string]string{}}
	SetHost(h)
	params, _ := json.Marshal(map[string]string{"service_account_credentials": serviceAccount(t, "http://127.0.0.1:1/token")})
	out, err := ConfigStart("t-drive2", "drive", string(params))
	defer RemoveRemote("t-drive2")
	if err == nil || len(h.urls) > 0 {
		t.Fatalf("setup: %s, err %v, browser %v", out, err, h.urls)
	}
	t.Logf("stopped as expected: %v", err)
}

// Files that look right on the outside but hold no readable key, and a
// setting with nothing to check, each get their own plain answer.
func TestCheckCredentialNoKey(t *testing.T) {
	if _, err := CheckCredential("sftp", "key_file", "x"); err == nil || err.Error() != "[other] nothing to check for key_file" {
		t.Errorf("nothing to check: %v", err)
	}
	sa, _ := json.Marshal(map[string]string{"type": "service_account", "client_email": "a@b", "private_key": "not a key"})
	if _, err := CheckCredential("drive", "service_account_credentials", string(sa)); err == nil ||
		!strings.HasPrefix(err.Error(), "[credential] That file isn't a service account key.") {
		t.Errorf("a service account with no key: %v", err)
	}
	box, _ := json.Marshal(map[string]any{"enterpriseID": "1", "boxAppSettings": map[string]any{"clientID": "c",
		"appAuth": map[string]string{"privateKey": "not a key", "passphrase": "p"}}})
	if _, err := CheckCredential("box", "config_credentials", string(box)); err == nil ||
		!strings.HasPrefix(err.Error(), "[credential] The private key in that file can't be read.") {
		t.Errorf("a Box file with no key: %v", err)
	}
}

// A file that isn't the right one is refused in Driveway's own words, tagged
// [credential]: the one engine text the app shows as it is.
func TestCredentialRefusalsTagged(t *testing.T) {
	for _, c := range []struct{ backend, key, value string }{
		{"drive", "service_account_credentials", "{}"},
		{"box", "config_credentials", "not json"},
		{"jottacloud", "config_login_token", "hunter2"},
	} {
		_, err := CheckCredential(c.backend, c.key, c.value)
		if err == nil || !strings.HasPrefix(err.Error(), "[credential] ") || strings.HasPrefix(err.Error(), "[credential] [") {
			t.Errorf("%s %s: %v", c.backend, c.key, err)
		}
	}
}

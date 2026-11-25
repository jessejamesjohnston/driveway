// SPDX-License-Identifier: Apache-2.0

package engine

// The other direction from address_matrix_test.go: the address rule must not
// refuse a setup people really use. Each row is a realistic address for a
// service, from the service's own documentation or rclone's help, with the
// verdict the check must give. The legitimately ambiguous rows (an @ after a
// port) are refused, and their sentence must give the
// %40 way out (KindsTest checks the wording against the strings).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type validRow struct {
	backend, setting, value string
	want                    string // "" accepted, or the reason refused
}

func validRows() []validRow {
	var rows []validRow
	add := func(backend, setting, want string, values ...string) {
		for _, v := range values {
			rows = append(rows, validRow{backend, setting, v, want})
		}
	}
	add("webdav", "url", "",
		"https://cloud.example.com/remote.php/dav/files/alice/",
		"https://cloud.example.com/remote.php/dav/files/alice@example.com/",
		"https://cloud.example.com/remote.php/dav/files/alice%40example.com/",
		"https://cloud.example.com:8443/remote.php/dav/files/alice%40example.com/",
		"https://nas.local:5006/alice%40example.com/",
		"https://192.168.1.20/remote.php/webdav/",
		"https://cloud.example.com/nextcloud/remote.php/dav/files/alice/",
		"https://owncloud.example.com/remote.php/dav/files/alice/",
		"https://ocis.example.com/dav/spaces/1284d238-aa92-42ce-bdc4-0b0000009157$alice/",
		"https://contoso.sharepoint.com/sites/My Site/Shared Documents",
		"https://contoso.sharepoint.com/sites/team/Shared%20Documents",
		"https://contoso-my.sharepoint.com/personal/alice_contoso_com/Documents",
		"https://dav.box.example.com/dav",
		"https://webdav.fastmail.com/",
		"https://myfiles.fastmail.com/",
		"http://nas.local:5005/",
		"https://nas.local:5006/homes/alice/",
		"https://nas:5006/",
		"http://localhost:8080/",
		"http://127.0.0.1:8080/dav/",
		"https://[2001:db8::20]:5006/",
		"https://[fe80::1%25wlan0]:5006/",
		"https://[::1]/dav",
		"HTTPS://nas.local/dav",
		"https://bücher.example/dav",
		"https://xn--bcher-kva.example/dav",
		"https://nas-01_backup.example.com./dav",
		"https://seafile.example.com/seafdav",
		"https://dav.example.com/files/a:b@c.txt/",
		"https://dav.example.com/remote.php/dav/files/alice/My Files/",
		"https://cloud.example.com/remote.php/dav/files/alice@example.com/2024/photos/",
		"https://alice:pw@dav.example.com/dav",
		"https://alice@dav.example.com/dav",
		"https://alice%40example.com:pw@cloud.example.com/remote.php/dav/files/alice%40example.com/",
		"https://alice:s3cr%40t%20x@dav.example.com:8443/remote.php/dav/files/alice%40example.com/",
		"https://webdav.yandex.ru/",
		"https://dav.jianguoyun.com/dav/",
		"https://u123456.your-storagebox.de/",
		"https://app.koofr.net/dav/Koofr",
		"https://cloud.example.com/public.php/webdav/?token=abc",
		"https://nas.local:5006/home/Photos 2024/",
	)
	// Ambiguous on purpose: an @ anywhere after a port. Each has a %40
	// twin above that passes.
	add("webdav", "url", reasonAmbiguous,
		"https://cloud.example.com:8443/remote.php/dav/files/alice@example.com/",
		"https://cloud.example.com:8443/remote.php/dav/files/alice@example.com/?x=1",
		"https://alice:s3cr%40t%20x@dav.example.com:8443/remote.php/dav/files/alice@example.com/",
		"https://nas.local:5006/alice@example.com/",
		"http://nas:5005/me@example.com/",
		"http://localhost:8080/me@example.com/",
		"https://nas.local:8443/team@home/x",
	)
	add("http", "url", "",
		"https://files.example.com/",
		"https://user:pass@files.example.com/pub/",
		"https://downloads.example.com:8443/releases/",
		"http://192.168.1.20:8000/",
		"https://[2001:db8::30]/files/",
		"https://example.com/files/alice@example.com/",
	)
	add("http", "url", reasonAmbiguousEscape, "https://files.example.com:8443/me@example.com/")
	add("s3", "endpoint", "",
		"minio.local:9000", "http://10.0.0.5:9000", "s3.eu-west-1.amazonaws.com",
		"https://s3.eu-west-1.amazonaws.com", "[::1]:9000", "minio:9000", "HTTP://minio:9000",
		"https://s3.us-west-002.backblazeb2.com", "https://storage.googleapis.com",
		"https://nyc3.digitaloceanspaces.com", "https://s3.wasabisys.com",
		"https://0123456789abcdef.r2.cloudflarestorage.com", "https://gateway.storjshare.io",
		"https://objectstorage.us-ashburn-1.oraclecloud.com", "http://minio.local:9000/",
	)
	add("s3", "sts_endpoint", "", "https://sts.amazonaws.com")
	add("azureblob", "endpoint", "", "https://myaccount.blob.core.windows.net", "http://127.0.0.1:10000/devstoreaccount1")
	add("azureblob", "sas_url", "",
		"https://myaccount.blob.core.windows.net/container?sv=2022-11-02&ss=b&srt=co&sp=rl&se=2027-01-01&sig=abc%2Bdef%3D",
		"https://myaccount.blob.core.windows.net/container?sp=r&sig=abc+def=")
	add("azurefiles", "endpoint", "", "https://myaccount.file.core.windows.net")
	add("azurefiles", "sas_url", "", "https://myaccount.file.core.windows.net/share?sv=2022-11-02&sig=abc")
	add("b2", "endpoint", "", "https://api.backblazeb2.com")
	add("b2", "download_url", "", "https://f002.backblazeb2.com", "https://files.example.com")
	add("swift", "auth", "",
		"https://auth.cloud.ovh.net/v3", "https://identity.api.rackspacecloud.com/v2.0",
		"http://192.168.1.30:5000/v3", "https://keystone.example.com:5000/v3/")
	add("swift", "storage_url", "", "https://storage.example.com/v1/AUTH_0123456789abcdef")
	add("seafile", "url", "", "https://cloud.seafile.com", "https://seafile.example.com:8443/", "http://192.168.1.40:8000")
	add("koofr", "endpoint", "", "https://app.koofr.net", "https://storage.rcs-rds.ro")
	add("qingstor", "endpoint", "", "https://qingstor.com:443")
	add("sia", "api_url", "", "http://127.0.0.1:9980", "http://sia.example.lan:9980")
	add("filefabric", "url", "", "https://storagemadeeasy.com", "https://eu.storagemadeeasy.com", "https://yourfabric.smestorage.com")
	add("hidrive", "endpoint", "", "https://api.hidrive.strato.com/2.1")
	add("imagekit", "endpoint", "", "https://ik.imagekit.io/your_imagekit_id")
	add("internetarchive", "endpoint", "", "https://s3.us.archive.org")
	add("internetarchive", "front_endpoint", "", "https://archive.org")
	add("pixeldrain", "api_url", "", "https://pixeldrain.com/api")
	add("doi", "doi_resolver_api_url", "", "https://doi.org/api")
	add("oracleobjectstorage", "endpoint", "", "https://objectstorage.us-phoenix-1.oraclecloud.com")
	add("onedrive", "tenant_url", "", "https://login.microsoftonline.com/contoso.onmicrosoft.com")
	add("google cloud storage", "endpoint", "", "https://storage.googleapis.com", "storage.example.com")
	add("drive", "auth_url", "", "https://accounts.google.com/o/oauth2/auth")
	add("drive", "token_url", "", "https://oauth2.googleapis.com/token")
	add("dropbox", "token_url", "", "https://api.dropboxapi.com/oauth2/token")
	add("sftp", "host", "",
		"nas.local", "192.168.1.10", "nas", "2001:db8::10", "[2001:db8::10]", "fe80::1%wlan0",
		"sftp.example.com", "nas-01.example.com.")
	add("sftp", "host", reasonCredentials, "alice@nas.local")
	add("sftp", "socks_proxy", "", "proxy.example.com:1080", "alice:pw@proxy.example.com:1080")
	add("sftp", "http_proxy", "", "http://proxy.example.com:3128", "http://alice:pw@proxy.example.com:3128")
	add("ftp", "host", "", "ftp.example.com", "192.168.1.11", "nas.local")
	add("ftp", "socks_proxy", "", "proxy.example.com:1080", "alice:pw@proxy.example.com:1080")
	add("ftp", "http_proxy", "", "http://proxy.example.com:3128")
	add("smb", "host", "", "nas.local", "192.168.1.12", "NAS", "[2001:db8::12]")
	add("hdfs", "namenode", "", "namenode-1:8020", "namenode-1:8020,namenode-2:8020", "192.168.1.13:8020")
	add("storj", "satellite_address", "",
		"us1.storj.io", "12EayRS2V1kEsWESU9QMRseFhdxYxKicsiFmxrsLZHeLUtdps3S@us1.storj.io:7777",
		"121RTSDpyNZVcEU84Ticf2L1ntiuUimbWgfATz21tuvgk3vzoA6@ap1.storj.io:7777")
	add("filescom", "site", "", "mysite", "files.example.com")
	add("filescom", "site", reasonHostOnly, "https://mysite.files.com")
	add("quatrix", "host", "", "yourcompany.quatrix.it")
	add("netstorage", "host", "", "example-nsu.akamaihd.net/123456/subdirectory")
	add("mailru", "auth_url", "", "https://o2.mail.ru/token")
	add("zoho", "auth_url", "", "https://accounts.zoho.eu/oauth/v2/auth")
	add("jottacloud", "token_url", "", "https://id.jottacloud.com/auth/realms/jottacloud/protocol/openid-connect/token")
	add("pcloud", "token_url", "", "https://api.pcloud.com/oauth2_token")
	add("box", "token_url", "", "https://api.box.com/oauth2/token")
	add("yandex", "token_url", "", "https://oauth.yandex.com/token")
	add("putio", "auth_url", "", "https://api.put.io/v2/oauth2/authenticate")
	add("premiumizeme", "auth_url", "", "https://www.premiumize.me/authorize")
	add("huaweidrive", "auth_url", "", "https://oauth-login.cloud.huawei.com/oauth2/v3/authorize")
	add("shade", "endpoint", "", "https://fs.shade.inc")
	add("s3", "ibm_iam_endpoint", "", "https://iam.cloud.ibm.com/identity/token")
	add("s3", "download_url", "", "https://cdn.example.com")
	return rows
}

// TestValidAddresses: every realistic address gets its verdict.
func TestValidAddresses(t *testing.T) {
	rows := validRows()
	bad := 0
	for _, r := range rows {
		b, _ := json.Marshal(map[string]string{r.setting: r.value})
		var got struct{ Reason string }
		raw := CheckSettings(r.backend, string(b))
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatal(err)
		}
		if got.Reason != r.want {
			bad++
			t.Errorf("%s.%s %q: got %s, want %q", r.backend, r.setting, r.value, raw, r.want)
		}
	}
	accepted := 0
	for _, r := range rows {
		if r.want == "" {
			accepted++
		}
	}
	t.Logf("%d realistic addresses (%d to accept, %d to refuse): %d as expected", len(rows), accepted, len(rows)-accepted, len(rows)-bad)
	if len(rows) < 150 {
		t.Errorf("only %d rows", len(rows))
	}
	// The ambiguous sentence's way out is in the strings KindsTest checks;
	// here, that each ambiguous reason is one the app maps.
	for _, r := range rows {
		if strings.HasPrefix(r.want, "ambiguous") && r.want != reasonAmbiguous && r.want != reasonAmbiguousEscape {
			t.Errorf("unknown reason %q", r.want)
		}
	}
}

// TestPercent40OnServer: the way out the refusal gives for an @ in a folder's
// name after a port (write it %40) really reaches that folder. On the WebDAV
// test server, a folder named for an email address is made, a file put in
// it, and a server whose address ends in that folder, written with %40, lists
// the file; the same address with a raw @ is refused.
func TestPercent40OnServer(t *testing.T) {
	if os.Getenv("DRIVEWAY_SERVERS") == "" {
		t.Skip("set DRIVEWAY_SERVERS=1 with test/servers running")
	}
	const root, inside = "pct40-root", "pct40-inside"
	login := `"vendor":"other","user":"driveway","pass":"driveway-test"`
	t.Cleanup(func() {
		_ = Delete(root, "me@example.com")
		RemoveRemote(root)
		RemoveRemote(inside)
	})
	if err := SetRemote(root, "webdav", `{"url":"http://127.0.0.1:8080/",`+login+`}`); err != nil {
		t.Fatal(err)
	}
	if err := Mkdir(root, "me@example.com"); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "marker.txt")
	if err := os.WriteFile(src, []byte("here"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Upload(root, "me@example.com/marker.txt", src); err != nil {
		t.Fatal(err)
	}
	if err := SetRemote(inside, "webdav", `{"url":"http://127.0.0.1:8080/me@example.com/",`+login+`}`); err == nil ||
		!strings.Contains(err.Error(), "(ambiguous)") {
		t.Fatalf("a raw @ after the port: %v", err)
	}
	if err := SetRemote(inside, "webdav", `{"url":"http://127.0.0.1:8080/me%40example.com/",`+login+`}`); err != nil {
		t.Fatal(err)
	}
	got, err := List(inside, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "marker.txt") {
		t.Fatalf("the %%40 address lists %s, not the folder's file", got)
	}
}

// SPDX-License-Identifier: Apache-2.0

package engine

// How Driveway's setup form presents each of rclone's services: its name as
// people call it, the few settings it can't connect without (shown above the
// Connect button), and how it signs in. Everything else rclone offers goes
// under the form's collapsed "More settings". The app builds the forms from
// this (Backends), and TestForms keeps it complete across rclone upgrades.

const (
	// methodSignIn: the service's own sign-in page, in the browser.
	methodSignIn = "signin"
	// methodForm: the settings on the form are the sign-in (a key, an app
	// password, an address).
	methodForm = "form"
	// methodAlt: no browser sign-in (it would borrow a registration built
	// into rclone); the service is set up another way rclone supports (a
	// key file, an API key).
	methodAlt = "alternative"
	// methodUnavailable: no way in Driveway offers (a sign-in that would
	// borrow rclone's registration, or a config file on disk); always hidden
	// (stateHidden, TestCatalogStates).
	methodUnavailable = "unavailable"
)

// Where a service shows: only services that can be added are listed. One
// value turns a service on or off.
const (
	// stateListed (the default): in More services, and in "Where are your
	// files?" for the services it names.
	stateListed = "listed"
	// stateHidden: not listed anywhere. A server of the service added
	// before keeps working (its card, Change and Files).
	stateHidden = "hidden"
)

// sinceFirst: the app version New labels began with. A service listed
// before then has no Since (it is never New); one listed later has the
// version that first listed it.
const sinceFirst = "0.2.4"

// essential is one setting above the Connect button.
type essential struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
	// Required: Connect asks for it first.
	Required bool `json:"required,omitempty"`
	// Secret: typed out of sight, like a password.
	Secret bool `json:"secret,omitempty"`
	// Choice: picked from rclone's examples for the setting.
	Choice bool `json:"choice,omitempty"`
	// File: chosen with Android's file picker; the file's contents are the
	// value ("json": a JSON key or settings file).
	File string `json:"file,omitempty"`
	// Link: a pasted link is turned into the ID it carries ("drive-folder").
	Link string `json:"link,omitempty"`
	// Ephemeral: used once during setup, never kept (rclone's config_*
	// answers, like a single-use login token). Needed only until the
	// service has given Driveway a token.
	Ephemeral bool `json:"ephemeral,omitempty"`
}

// alt is the way in for a methodAlt service, in the person's words.
type alt struct {
	// With: "Connect with <With>."
	With string `json:"with"`
	// How: where to get it, in a sentence or two.
	How string `json:"how"`
	// Row: the service's subtitle in the list.
	Row string `json:"row"`
	// MakesToken: setup trades the credential for a sign-in token (so a
	// server with a token isn't one that signed in through the browser).
	MakesToken bool `json:"makesToken,omitempty"`
}

type form struct {
	Label      string
	Method     string
	Essentials []essential
	// Hidden: settings rclone keeps for itself (a session token and its
	// expiry), never on the form.
	Hidden []string
	// FolderHint: the "Start in folder" hint, where the top is a list of
	// buckets, containers or items.
	FolderHint string
	Alt        *alt
	// Presets: answers to rclone's setup questions, sent only while setting
	// up (never kept with the server).
	Presets map[string]string
	// PresetsWith: presets that replace those in Presets when the named
	// setting is filled in (Google Drive: a shared folder's link means no
	// shared drive to choose).
	PresetsWith map[string]map[string]string
	// Prefill: settings a new server starts with (still changeable under
	// More settings).
	Prefill map[string]string
	// SkipSetup: with the alternative's credential, connect without rclone's
	// setup (premiumize.me's setup always opens the browser).
	SkipSetup bool
	// Why (methodUnavailable): for the docs, not shown.
	Why string
	// Blurb: one plain line under the name in the list, when the name isn't enough.
	Blurb string
	// State: stateListed (empty) or stateHidden.
	State string
	// Since: the app version that first offered the service, for its New
	// label (shown until the person has seen it in a list twice). Required
	// for a service turned on after sinceFirst (TestCatalogStates).
	Since string
}

const (
	bucketHint    = "A bucket, or bucket/folder. Leave empty to see every bucket."
	containerHint = "A container, or container/folder. Leave empty to see every container."
	borrowedWhy   = "browser sign-in would borrow rclone's app registration, which a published app may not present as its own"
)

func user(required bool) essential {
	return essential{Key: "user", Label: "User name", Required: required}
}
func pass(key string) essential {
	return essential{Key: key, Label: "Password", Required: true, Secret: true}
}
func email(key string) essential {
	return essential{Key: key, Label: "Email", Required: true}
}

// boxSignInForm replaces Box's form when the build carries Merriment Labs'
// Box registration (pkce.go): one tap, like Dropbox. A server set up with an
// app's settings file before keeps it (usesOwnRegistration), and the app
// doesn't treat it as a browser sign-in (Kinds.browser). The JWT, developer
// token and enterprise settings are for those ways in, not this one.
var boxSignInForm = form{Label: "Box", Method: methodSignIn, Hidden: []string{"client_id", "client_secret",
	"box_sub_type", "access_token", "box_config_file", "config_credentials", "impersonate"}}

// forms is the curated list: every service rclone registers that Driveway
// lists (all but the hand-made SFTP, SMB, NFS and WebDAV forms, and the
// hidden ones).
var forms = map[string]form{
	"azureblob": {Label: "Azure Blob Storage", Method: methodForm, FolderHint: containerHint, Essentials: []essential{
		{Key: "account", Label: "Storage account", Hint: "The storage account's name"},
		{Key: "key", Label: "Access key", Hint: "From the storage account's Access keys page", Secret: true},
	}},
	"azurefiles": {Label: "Azure Files", Method: methodForm, Essentials: []essential{
		{Key: "account", Label: "Storage account", Hint: "The storage account's name"},
		{Key: "key", Label: "Access key", Hint: "From the storage account's Access keys page", Secret: true},
		{Key: "share_name", Label: "Share", Required: true},
	}},
	"b2": {Label: "Backblaze B2", Method: methodForm, FolderHint: bucketHint, Essentials: []essential{
		{Key: "account", Label: "Application key ID", Required: true},
		{Key: "key", Label: "Application key", Required: true, Secret: true},
	}},
	"box": {Label: "Box", Method: methodAlt, Prefill: map[string]string{"box_sub_type": "enterprise"},
		Alt: &alt{With: "a Box app's settings file", Row: "Connect with an app settings file",
			MakesToken: false,
			How: "In the Box Developer Console (a free developer account works), make a Custom App with Server " +
				"Authentication (with JWT), turn on two-step verification, and generate a key pair to download its " +
				"config.json. Driveway sees the folders shared with the app's service account."},
		Essentials: []essential{
			{Key: "config_credentials", Label: "App settings file", Hint: "The config.json from the Box Developer Console", Required: true, File: "json"},
		}},
	"cloudinary": {Label: "Cloudinary", Method: methodForm, Essentials: []essential{
		{Key: "cloud_name", Label: "Cloud name", Required: true},
		{Key: "api_key", Label: "API key", Required: true},
		{Key: "api_secret", Label: "API secret", Required: true, Secret: true},
	}},
	"doi": {Label: "DOI datasets", Method: methodForm, Blurb: "Read-only datasets from Zenodo, Dataverse, and others", Essentials: []essential{
		{Key: "doi", Label: "DOI", Hint: "Like 10.5281/zenodo.123456, or its doi.org link", Required: true},
	}},
	"drime": {Label: "Drime", Method: methodForm, Essentials: []essential{
		{Key: "access_token", Label: "Access token", Hint: "From Drime's web control panel", Required: true, Secret: true},
	}},
	// A service account can't own files (Google, 2025): it saves into a
	// Google Workspace shared drive it is a member of; a folder shared from a
	// personal Drive opens but takes no new files. Without a folder link,
	// setup lists the shared drives to pick from.
	"drive": {Label: "Google Drive", Method: methodAlt,
		Presets:     map[string]string{"config_change_team_drive": "true"},
		PresetsWith: map[string]map[string]string{"root_folder_id": {"config_change_team_drive": "false"}},
		Alt: &alt{With: "a service account key", Row: "Connect with a service account key",
			How: "In the Google Cloud console, make a service account and add a JSON key. Then add the service " +
				"account's email to a shared drive in Google Workspace, and Driveway lists it here. A folder shared " +
				"from a personal Drive opens too, but read-only: a service account can't save new files there."},
		Essentials: []essential{
			{Key: "service_account_credentials", Label: "Service account key", Hint: "The JSON key file from the Google Cloud console", Required: true, File: "json"},
			{Key: "root_folder_id", Label: "Shared folder", Hint: "Paste a shared folder's link. Leave empty to pick a shared drive.", Link: "drive-folder"},
		}},
	// Settings that only work with a sign-in Driveway's registration doesn't
	// ask for are left off: shared-files modes and a team admin's
	// impersonation need the sharing and team scopes; the client ID and
	// secret would replace the registration.
	"dropbox": {Label: "Dropbox", Method: methodSignIn, Hidden: []string{"client_id", "client_secret",
		"impersonate", "impersonate_admin", "shared_files", "shared_folders", "skip_unowned_folders"}},
	"fichier": {Label: "1Fichier", Method: methodForm, Essentials: []essential{
		{Key: "api_key", Label: "API key", Hint: "From 1fichier.com: Parameters, API", Required: true, Secret: true},
	}},
	"filefabric": {Label: "Enterprise File Fabric", Method: methodForm, Hidden: []string{"token", "token_expiry", "version"},
		Essentials: []essential{
			{Key: "url", Label: "Address", Hint: "Like https://storagemadeeasy.com", Required: true},
			{Key: "permanent_token", Label: "Permanent token", Hint: "From your dashboard: Security, Permanent authentication tokens", Required: true, Secret: true},
		}},
	"filelu": {Label: "FileLu", Method: methodForm, Essentials: []essential{
		{Key: "key", Label: "Rclone key", Hint: "From FileLu: My Account", Required: true, Secret: true},
	}},
	"filen": {Label: "Filen", Method: methodForm, Essentials: []essential{
		email("email"), pass("password"),
		{Key: "api_key", Label: "API key", Hint: "From the Filen command-line app: export-api-key", Required: true, Secret: true},
	}},
	"filescom": {Label: "Files.com", Method: methodForm, Essentials: []essential{
		{Key: "site", Label: "Site", Hint: "Your subdomain (mysite) or your own domain", Required: true},
		{Key: "username", Label: "User name"},
		{Key: "password", Label: "Password", Secret: true},
	}},
	"ftp": {Label: "FTP", Method: methodForm, Essentials: []essential{
		{Key: "host", Label: "Server", Hint: "ftp.example.com or 192.168.1.10", Required: true},
		{Key: "user", Label: "User name", Hint: "anonymous, for a public server", Required: true},
		{Key: "pass", Label: "Password", Secret: true},
	}},
	"gofile": {Label: "Gofile", Method: methodForm, Essentials: []essential{
		{Key: "access_token", Label: "Access token", Hint: "From Gofile's web control panel", Required: true, Secret: true},
	}},
	"google cloud storage": {Label: "Google Cloud Storage", Blurb: "Buckets in Google Cloud (not Google Drive)", Method: methodAlt, FolderHint: bucketHint,
		Alt: &alt{With: "a service account key", Row: "Connect with a service account key",
			How: "In the Google Cloud console, make a service account with a Storage role (Storage Admin, or Storage " +
				"Viewer to only look) and add a JSON key. Newer Google Cloud accounts block key creation until it is " +
				"allowed under Organization policies."},
		Essentials: []essential{
			{Key: "service_account_credentials", Label: "Service account key", Hint: "The JSON key file from the Google Cloud console", Required: true, File: "json"},
			{Key: "project_number", Label: "Project ID or number", Hint: "Filled in from the key. Needed to list your buckets."},
		}},
	"hdfs": {Label: "Hadoop (HDFS)", Method: methodForm, Essentials: []essential{
		{Key: "namenode", Label: "Name node", Hint: "namenode-1:8020", Required: true},
		{Key: "username", Label: "User name"},
	}},
	"hidrive": {Label: "HiDrive", Method: methodUnavailable, State: stateHidden, Why: borrowedWhy},
	"http": {Label: "Web folder (HTTP)", Method: methodForm, Blurb: "Read-only: a web server's folder listing", Essentials: []essential{
		{Key: "url", Label: "Address", Hint: "https://example.com/files/", Required: true},
	}},
	"huaweidrive": {Label: "Huawei Drive", Method: methodUnavailable, State: stateHidden, Why: borrowedWhy},
	"iclouddrive": {Label: "iCloud Drive and Photos", Method: methodForm, Essentials: []essential{
		{Key: "apple_id", Label: "Apple Account", Hint: "Your Apple Account's email", Required: true},
		pass("password"),
		{Key: "service", Label: "Show", Required: true, Choice: true},
	}},
	"imagekit": {Label: "ImageKit", Method: methodForm, Essentials: []essential{
		{Key: "endpoint", Label: "URL endpoint", Hint: "From ImageKit's dashboard: Developer, API keys", Required: true},
		{Key: "public_key", Label: "Public key", Required: true},
		{Key: "private_key", Label: "Private key", Required: true, Secret: true},
	}},
	"internetarchive": {Label: "Internet Archive", Method: methodForm, FolderHint: "An item, or item/folder", Essentials: []essential{
		{Key: "access_key_id", Label: "Access key", Hint: "From archive.org/account/s3.php. Leave empty to browse without one."},
		{Key: "secret_access_key", Label: "Secret key", Secret: true},
	}},
	"internxt": {Label: "Internxt Drive", Method: methodForm, Essentials: []essential{email("email"), pass("pass")}},
	"jottacloud": {Label: "Jottacloud", Method: methodAlt,
		Presets: map[string]string{"config_type": "standard", "config_non_standard": "false"},
		Alt: &alt{With: "a personal login token", Row: "Connect with a login token",
			MakesToken: true,
			How: "On jottacloud.com, open Settings, Security, and generate a personal login token right before " +
				"you paste it here. It works once: Driveway trades it for its own access."},
		Essentials: []essential{
			{Key: "config_login_token", Label: "Personal login token", Hint: "From jottacloud.com/web/secure", Required: true, Secret: true, Ephemeral: true},
		}},
	"koofr": {Label: "Koofr and Digi Storage", Method: methodForm, Prefill: map[string]string{"provider": "koofr"}, Essentials: []essential{
		{Key: "provider", Label: "Service", Choice: true, Required: true},
		user(true),
		{Key: "password", Label: "App password", Hint: "Make one in your account's settings: Preferences, Password", Required: true, Secret: true},
		{Key: "endpoint", Label: "Address (Other only)", Hint: "For Other: your service's address"},
	}},
	"linkbox": {Label: "Linkbox", Method: methodForm, Essentials: []essential{
		{Key: "token", Label: "API token", Hint: "From linkbox.to: Account", Required: true, Secret: true},
		email("email"), pass("password"),
	}},
	"mailru": {Label: "Mail.ru Cloud", Method: methodForm, Hidden: []string{"token"}, Essentials: []essential{
		email("user"),
		{Key: "pass", Label: "App password", Hint: "Make one in your Mail.ru security settings", Required: true, Secret: true},
	}},
	"mega": {Label: "MEGA", Method: methodForm, Essentials: []essential{
		email("user"), pass("pass"),
		{Key: "2fa", Label: "Two-factor code", Hint: "Only if your account uses one"},
	}},
	"netstorage": {Label: "Akamai NetStorage", Method: methodForm, Essentials: []essential{
		{Key: "host", Label: "Host", Hint: "domain/folder", Required: true},
		{Key: "account", Label: "Account", Required: true},
		{Key: "secret", Label: "Secret (G2O key)", Required: true, Secret: true},
	}},
	// Likewise: the scopes are the registration's, it covers only the global
	// cloud, and a tenant is for a sign-in with a secret.
	"onedrive": {Label: "OneDrive", Method: methodSignIn, Hidden: []string{"client_id", "client_secret",
		"access_scopes", "disable_site_permission", "region", "tenant"}},
	"opendrive": {Label: "OpenDrive", Method: methodForm, Essentials: []essential{{Key: "username", Label: "User name", Required: true}, pass("password")}},
	"oracleobjectstorage": {Label: "Oracle Object Storage", Method: methodUnavailable, State: stateHidden,
		Why: "signs in with an OCI config file and key file on disk; only public buckets would work"},
	"pcloud": {Label: "pCloud", Method: methodUnavailable, State: stateHidden, Why: borrowedWhy},
	"pikpak": {Label: "PikPak", Method: methodForm, Essentials: []essential{user(true), pass("pass")}},
	"pixeldrain": {Label: "Pixeldrain", Method: methodForm, Essentials: []essential{
		{Key: "api_key", Label: "API key", Hint: "From pixeldrain.com/user/api_keys", Required: true, Secret: true},
	}},
	"premiumizeme": {Label: "premiumize.me", Method: methodAlt, SkipSetup: true,
		Alt: &alt{With: "your API key", Row: "Connect with an API key",
			How: "Your API key is on your account page, premiumize.me/account."},
		Essentials: []essential{
			{Key: "api_key", Label: "API key", Hint: "From your premiumize.me account page", Required: true, Secret: true},
		}},
	"protondrive": {Label: "Proton Drive", Method: methodForm, Essentials: []essential{
		{Key: "username", Label: "User name", Required: true}, pass("password"),
		{Key: "2fa", Label: "Two-factor code", Hint: "Only if your account uses one"},
	}},
	"putio": {Label: "put.io", Method: methodUnavailable, State: stateHidden, Why: borrowedWhy},
	"qingstor": {Label: "QingStor", Method: methodForm, FolderHint: bucketHint, Essentials: []essential{
		{Key: "access_key_id", Label: "Access key ID"},
		{Key: "secret_access_key", Label: "Secret access key", Secret: true},
		{Key: "zone", Label: "Zone", Hint: "Like pek3a"},
	}},
	"quatrix": {Label: "Quatrix", Method: methodForm, Essentials: []essential{
		{Key: "host", Label: "Account address", Hint: "yourcompany.quatrix.it", Required: true},
		{Key: "api_key", Label: "API key", Required: true, Secret: true},
	}},
	"s3": {Label: "S3 storage", Method: methodForm, FolderHint: bucketHint, Prefill: map[string]string{"provider": "AWS"},
		Blurb: "Amazon S3 and services that work like it", Essentials: []essential{
			{Key: "provider", Label: "Provider", Choice: true, Required: true},
			{Key: "access_key_id", Label: "Access key ID", Required: true},
			{Key: "secret_access_key", Label: "Secret access key", Required: true, Secret: true},
			{Key: "endpoint", Label: "Endpoint", Hint: "Your provider's address. Leave empty for Amazon."},
			{Key: "region", Label: "Region", Hint: "Like us-east-1. Leave empty if your provider has none."},
		}},
	"seafile": {Label: "Seafile", Method: methodForm, Essentials: []essential{
		{Key: "url", Label: "Address", Hint: "https://cloud.seafile.com", Required: true},
		email("user"),
		{Key: "pass", Label: "Password", Secret: true},
		{Key: "library", Label: "Library", Hint: "Leave empty to see every library"},
	}},
	"shade": {Label: "Shade", Method: methodForm, Hidden: []string{"token", "token_expiry"}, Essentials: []essential{
		{Key: "drive_id", Label: "Drive ID", Hint: "In the drive's settings", Required: true},
		{Key: "api_key", Label: "API key", Required: true, Secret: true},
	}},
	"sia": {Label: "Sia", Method: methodForm, Essentials: []essential{
		{Key: "api_url", Label: "Daemon address", Hint: "Like http://sia.example.lan:9980", Required: true},
		{Key: "api_password", Label: "API password", Secret: true},
	}},
	"storj": {Label: "Storj", Method: methodForm, FolderHint: bucketHint, Essentials: []essential{
		{Key: "access_grant", Label: "Access grant", Hint: "Make one in the Storj console: Access, Create access grant", Required: true, Secret: true},
	}},
	"sugarsync": {Label: "SugarSync", Method: methodUnavailable, State: stateHidden,
		Why: "signs in with rclone's own SugarSync app keys, which a published app may not present as its own"},
	"swift": {Label: "OpenStack Swift", Method: methodForm, FolderHint: containerHint, Essentials: []essential{
		{Key: "auth", Label: "Sign-in address", Hint: "The authentication URL (OS_AUTH_URL)", Required: true},
		user(false),
		{Key: "key", Label: "API key or password", Secret: true},
		{Key: "tenant", Label: "Project", Hint: "The project or tenant name, if your provider uses one"},
	}},
	"ulozto": {Label: "Uloz.to", Method: methodForm, Essentials: []essential{
		{Key: "app_token", Label: "App token", Hint: "From Uloz.to's API documentation or support", Required: true, Secret: true},
		{Key: "username", Label: "User name", Required: true}, pass("password"),
	}},
	"yandex": {Label: "Yandex Disk", Method: methodUnavailable, State: stateHidden, Why: borrowedWhy},
	"zoho":   {Label: "Zoho WorkDrive", Method: methodUnavailable, State: stateHidden, Why: borrowedWhy},
}

// applyPresets adds the form's answers to rclone's setup questions to the
// settings setup starts with, unless the settings answer them already.
func applyPresets(f form, m map[string]any) {
	set := map[string]string{}
	for k, v := range f.Presets {
		set[k] = v
	}
	for key, presets := range f.PresetsWith {
		if s, _ := m[key].(string); s != "" {
			for k, v := range presets {
				set[k] = v
			}
		}
	}
	for k, v := range set {
		if _, ok := m[k]; !ok {
			m[k] = v
		}
	}
}

// handMade services have their own forms in the app.
var handMade = map[string]bool{"sftp": true, "smb": true, "nfs": true, "webdav": true}

// neverOnForm are settings no form shows: how rclone signs in through the
// browser (the token it gets, the sign-in addresses), its file name
// encoding and a remote's description.
var neverOnForm = map[string]bool{
	"token": true, "auth_url": true, "token_url": true, "client_credentials": true,
	"encoding": true, "description": true,
}

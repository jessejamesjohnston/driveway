# Testing Driveway

## Automated

```sh
./build.sh unit                                     # no servers: go vet, the engine's unit tests, the plain-Java checks
./build.sh test                                     # everything, against throwaway servers it starts in Docker
./build.sh servers                                  # start those servers and leave them up (logins driveway / driveway-test)
./build.sh cover                                    # the same checks with coverage numbers (build/cover); starts the servers like test
./build.sh debug && adb install -r build/Driveway-debug.apk
adb shell pm grant com.merrimentlabs.driveway android.permission.ACCESS_LOCAL_NETWORK
tools/harness.sh                                    # through Android's documents API, on an emulator
```

Credentials are taken out of errors and log lines twice, once in the engine (Go) and once in the
app (Java). Both layers run the same cases, kept in `test/redact_cases.json`: each case is an input
and the exact text that must come out. Add a case there and both test suites check it. Around
them:

- `TestSecretNamesFromRclone` walks every setting rclone marks as a password or as sensitive, and
  fails on one that is neither hidden nor listed as not secret with a reason, and on a hidden name
  no shared case covers.
- `TestUnreadableAddressNeverLeaks` puts addresses with a password Go can't parse straight into
  the settings, past the check at the door, and asserts that the error, the log and the redacted
  raw text hold none of it.
- The address check (`engine/address.go`) has its own shared cases, `test/address_cases.json`:
  each gives a server's settings and either the setting refused and why, with the sentence the
  form shows, or the settings used and the note. `TestAddressCases` and `TestAddressAtTheDoor`
  check the engine and SetRemote against them. The form has no address rules of its own: it asks
  the engine, and `KindsTest` runs the same cases through the form's side, answered with the real
  engine's verdicts (written by `TestAddressVerdictsJSON`). `TestEveryAddressSettingClassified`
  fails on any setting rclone describes as an address that the check doesn't know.
- `engine/address_matrix_test.go` generates the addresses instead of listing them: 9 user name
  shapes, 35 password shapes, 11 hosts and 9 folders, with and without `https://`, in every
  address setting of every service rclone has, with and without a login in User name and
  Password. Among the shapes are look-alikes (a full-width or small `@`, a full-width or
  modifier-letter colon, a full-width solidus, standing where a separator goes), an
  international host name, folders named in other scripts, and folders named with the
  full-width `：` `？` `＃` `／` and `⁉` that hold no password. Each password with content holds
  a marker: after the digits where it starts with digits (`2024/QZQb`), and elsewhere on both
  sides of what makes it hard; the look-alike shapes carry it in the user name, host or folder.
  `TestAddressMatrix` runs the check on all of them (about 5.2 million runs) and asserts that a
  refusal never repeats the password, an accepted address keeps no password except where the
  service's own sign-in stays in it (the HTTP kind, a proxy), a login moves whole or not at all,
  and no value that holds a look-alike and a password is accepted in any setting (decided from
  the value's shapes, not from the check's own rule); it then runs the redactor on every
  error Go writes about them (its parse error, a request's error as the HTTP client prints it,
  and the address with one slash after its scheme, with `//` and no scheme, and with no scheme,
  as a service may rebuild and quote it). Values with a look-alike are left out of that step:
  those with a password are refused everywhere, those without one hold nothing to hide, and Go
  writes the look-alike percent-encoded, which the redactor doesn't read as an `@`.
  `TestInternationalFolders` holds 24 legitimate addresses (WebDAV, the HTTP kind, S3
  endpoints and SFTP hosts) with folders named with the full-width `：` `？` `＃` `／`, `⁉`, `⁈`
  and dot forms, international host names and folders in other scripts, and no `@`: each must
  be accepted, kept byte for byte, and read as the same address with those characters swapped
  for a plain letter. `TestAddressMatrixNetwork` calls
  SetRemote and Check, with the network cut, for a pairwise sample of the accepted ones (every
  pair of a setting and a shape, or of two shapes, at least once; `DRIVEWAY_MATRIX_FULL=1` runs
  every accepted value that keeps the password in an address) and of the refused ones, and
  asserts that no returned error, log line or request holds the password. It gives SetRemote
  and Check each of the 24 legitimate addresses too: each must be kept as typed and reach its
  host, with no refusal in the error. It also names the
  settings a connection attempt doesn't reach (a service that stops first for want of a key or a
  sign-in): for those, the check's own assertion is what stands. It runs itself inside
  a namespace with no network (`bwrap --unshare-net`) when it can, and otherwise in-process with
  requests sent to a proxy that answers none, name lookups failing and connections bound to the
  loopback address. `TestValidAddresses` holds 180 realistic addresses with the verdict each
  must get (international host names, folders in other scripts and folders with full-width
  punctuation among those accepted), takes every character the look-alike rule names through
  a folder, a folder with an `@` in the address, and the server part, and
  `TestPercent40OnServer` lists a folder named for an email address through a `%40` address on
  the WebDAV test server. `TestRedactGenJSON` writes the redactor's output for every generated error,
  and `LogsTest` requires the app's redactor to give the same.
- `TestRefusedReplace` checks that settings SetRemote refuses leave nothing of the server's
  earlier settings working, and `TestServerTextIsNoSettingsError` that a server's own answer is
  never taken for a settings error.
- Both suites redact a megabyte of each of a dozen shapes, and of a long value inside each rule,
  within a generous time bound (a pattern that backtracks would take minutes, or overflow the
  app's stack).
- `LogsTest` scans the app's and the debug harness's code for any log call or console write that
  goes around `Logs`.

`./build.sh unit` needs only the toolchain from `./build.sh fetch`. `./build.sh test` also needs
Docker: it makes a throwaway SSH key pair for the SFTP server on first use
(`test/servers/keys/`, gitignored), starts the servers, waits until each answers, runs every check
and stops the servers again if it started them. Without Docker it runs the unit checks and says
that the server checks were skipped. The kernel NFS checks and the on-emulator harness below are
separate runs.

`./build.sh cover` starts and stops the servers the way `test` does (and leaves them up if they
were already running), then prints the engine's statement coverage over both its
packages and JaCoCo's line and branch coverage of the plain Java (NewLabels,
Kinds, UpdateCheck, and Errors but for explain(), which fills in message()'s sentence on Android); the screens and
the documents provider are covered by the harness below, not by a number.
The Java checks run against AOSP's org.json and read the engine's real
catalog, so Kinds can't drift from forms.go. They are plain `main()` programs
with a small `check(condition, description)` helper rather than JUnit: the
build has no Gradle or Maven to resolve a test framework, and each check's
description is printed as it passes, so the output reads as the specification.
`ErrorsTest` fills in the sentence for every kind of engine error from the real
`strings.xml` and checks that none carries the engine's own words, and that the
app's code puts no exception's message on screen. `StringStyleTest` checks the
wording of every string in `strings.xml`.

More harness actions, one at a time (`tools/act.sh ACTION ID [key value]...`; `add` never
prints PASS, so run it with `WAIT=4` rather than waiting out the default 600 s):
`bigio mb N` (save, upload, byte-compare), `save`/`check name N mb M [wait 1]`,
`copy name N` (server-side copy), `editinplace` (an "rw" edit, reopened at
once), `list path P` (timed listing), `doc op delete|rename|move|read|rw path P
[to T]` (one provider call as Files makes it, timed; `rw` opens to edit,
reads and closes unchanged, and checks the card asks for nothing after),
`names` (awkward file names), `run` (the full set on one server).
`staleflag` (on a server that can't be reached: a failed save's card and
Discard leave a newer save alone), `oldmove` (a move refused eleven minutes
ago keeps its original while the copy is still being written), `movekill`
(through `tools/harness.sh` only, on t-webdav in three phases: a refused
move's copy waits on the device while Driveway is force-stopped; the
provider's first delete after the restart, with no window, keeps the
original; once the copy is sent its record goes and the delete goes through.
`act.sh` can't run phase 2: it brings the window forward, which restores the
record before the provider is used, and it can't reach a stopped app) and
`readonly` (on a web folder: no change flags, a write refused before
staging) check the save bookkeeping. For a physical device on the network,
set `HARNESS_HOST` to this machine's LAN address with `compose.expose.yml` up.
The kernel NFS server is a compose profile (`kernel-nfs`, see compose.yml).
With its export secure (the default), `DRIVEWAY_KNFS=127.0.0.1 ./build.sh test`
checks the "non-privileged ports" message; with `KNFS_OPTS=insecure`,
`DRIVEWAY_KNFS_RW=127.0.0.1 ./build.sh test` checks that a save's temporary
file is owner-only while it uploads and that a replaced file keeps its mode.
S3 (Versity's gateway) and FTP are in compose.yml too.

Browser sign-in without a real account: `ANDROID_SERIAL=emulator-NNNN
tools/signin-harness.sh` starts `test/servers/fake-oauth.py` (a stand-in that
holds Driveway's PKCE sign-in to a public client's rules: verifier, redirect
and client ID checked, any secret or Basic header refused), reverses its port
and runs, through the emulator's real browser: a Dropbox and a OneDrive
sign-in with Merriment Labs' client IDs and a complete token; for OneDrive,
the account's own drive taken from the stand-in's Graph pages (`graph_url`),
with no setup question, and the one real drive when `/me/drive` doesn't
answer (system drives and `Bundles_` skipped); a Box sign-in when the build
carries the Box registration (the stand-in, `--confidential`, requires that
client's secret; the app must not keep it); rclone's refresh of each (tokens
that expire at once; the app must keep the new one);
declined; a code the service refuses; a page that never sends the browser
back and an answer from another sign-in, both waiting until Cancel sign-in;
a sign-in straight after a cancel; another app holding [::1]:53682 (a
`toybox nc` in `adb shell`), where both Driveway's own sign-in and rclone's
must refuse at once and say why; and one started with Driveway in the
background, which must end at once (Android blocks the browser silently;
`act.sh` keeps Driveway in front otherwise, `FRONT=0` doesn't). On a fresh
emulator, open Chrome once and choose "Use without an account" first: its
first-run screen otherwise swallows the sign-in page. By hand: `python3
test/servers/fake-oauth.py 127.0.0.1 8765`, `adb reverse tcp:8765 tcp:8765`,
then `tools/act.sh signin ID label L backend dropbox params
'{"auth_url":"http://localhost:8765/authorize","token_url":"http://127.0.0.1:8765/token"}'`
(paths `/deny`, `/stale`, `/badcode`, `/hang` for the other endings; `expect
KIND` for a sign-in that should end that way). Use localhost for the page, not
a LAN address: Chrome's HTTPS-first mode stops at a plain-http LAN page;
localhost is exempt. In-process, `TestOwnSignInDropbox`, `TestOwnSignInOneDrive`
and `TestOwnSignInEndings` do the same against a Go stand-in, and
`TestCompiledBackends` keeps the removed services out.

Google Cloud Storage with a service account key, on the emulator, without
a Google account: `python3 test/servers/fake-gcs.py 127.0.0.1 8766
/tmp/made-up-key.json` (it writes a made-up key whose token address is the
stand-in), `adb reverse tcp:8766 tcp:8766`, `adb push /tmp/made-up-key.json
/sdcard/Download/`, then in Driveway add Google Cloud Storage, choose the
file, put `http://127.0.0.1:8766/storage/v1/` in More settings, Endpoint,
and connect. The engine's own checks do the same in-process
(`TestServiceAccountStorage`), plus Google Drive's setup with a key
(`TestServiceAccountDriveSetup`), the key and settings file checks
(`TestCheckCredential`: Google keys, a Box config.json with its passphrase,
a Jottacloud login token) and Jottacloud's presets (`TestJottacloudPresets`).
`TestForms` keeps every service's form complete and true to rclone (run it
after each rclone upgrade); `TestEssentialsConnect` connects S3, FTP and a
web folder with nothing but their essentials.

`tools/harness.sh` also runs the connection checklist without a screen
(`connect`) and checks the step it stops at: a good SFTP server and NFS
export finish; a wrong password stops at Signed in; a closed port at Found;
a missing folder at the folder; S3 and a web folder connect with only their
forms' essentials, and a wrong S3 key stops at Signed in.

Which services are listed (`TestCatalogStates`: the hidden ones, no service
listed without a way in, every service listed since 0.2.4 with its
`Since`) is an engine check; the New label's life (absent on a fresh install,
shown after the update that lists a service, gone after two visits, a skipped
version, a downgrade) is `test/java/NewLabelsTest.java`, run first by
`./build.sh test`. On the emulator, `tools/screens.sh` searches More services
for three hidden services, walks a New label (the harness's `newlabel`
action marks Box) through three visits, checks that Storj, far down the
list, stays New through two visits that don't scroll to it and goes after
two that do, and opens a hidden service's server
(pCloud) for changes.

The screens: `ANDROID_SERIAL=emulator-NNNN tools/screens.sh OUT` walks every
screen with made-up servers, runs `tools/a11y-check.py` on each and saves a
screenshot of each (run it again at 200% text and in the dark theme).
`tools/ui.py` (tap, type, wait, shot) is what it drives the screens with; the
harness's `open`, `found`, `problem`, `failsave` and `clear` actions set the
scene. `ANDROID_SERIAL=emulator-NNNN tools/permissions.sh` (an Android 17
emulator; it clears Driveway's data) checks where each Allow button leads:
Android's dialog when never asked and when refused once, Driveway on screen
after each "Don't allow", App info once refused for good, and the row updated
after allowing it there.

The harness exists only in debug builds and only answers adb (its receiver
requires `android.permission.DUMP`).

## By hand, on a device

1. Launch asks for nothing. "Add a server" (or "Look for servers") asks for
   Nearby devices and notifications the first time; allow both.
2. Add an SFTP server with Make a key: copy the line into authorized_keys,
   Test connection, confirm the fingerprint, Save. It appears in Files.
3. In Files, open the server: folders list, a photo opens, a video plays and
   seeks without waiting for the whole file.
4. Save from another app (its Save to dialog) into the server; reopen it.
5. Rename, move into a subfolder, delete, new folder, all from Files.
6. Turn the server off, open it in Files: "Can't reach …" above the list.
7. Save to a server while it's off: a notification says the save failed;
   turn it on and press Try again.
8. Add SMB, NFS and WebDAV servers the same way.

# Security

## Reporting a problem

Please report security problems privately, not in a public issue: use GitHub's
[private vulnerability reporting](../../security/advisories/new) for this repository, or write to
hello@merrimentlabs.com. Say what you found, how to reproduce it, and which version (Settings,
About) you tested. You will get an answer as soon as I can manage; this is a one-person project.

## What is in scope

- **Stored sign-ins** (`Servers.java`): passwords, keys, key files and tokens are kept in the
  server list, encrypted under an Android Keystore key and excluded from backup (`allowBackup` is
  off). The app never writes them to its log. A user name and password typed into a server's
  address (`https://you:password@host/...`) move to the server's own User name and Password
  settings, in the form and again in the engine (`Kinds.urlParts`, `engine/address.go`); the HTTP
  kind, which has no such settings, keeps them in the address. An address Go can't read (a
  password with a space or a quote in it, a missing `https://`) is refused in Driveway's own
  words before it reaches rclone, because Go's own error repeats the address whole.
- **What reaches the log.** An error's text can carry a credential: some services put an API key
  or token in the request address (premiumize.me, Mail.ru, Linkbox), and Go includes the address
  in a network error. The engine redacts every error it returns and every line it logs (rclone's
  logger, Go's `log` package and, on Android, everything written to stdout and stderr, which
  appears under the `GoLog` tag), and the app redacts each line it logs again (`Logs.java`, the
  `Driveway` tag). The debug-only test harness logs through the same redaction; `LogsTest` scans
  the app's and the harness's code for any log call or console write that goes around it.
  Redaction keeps an address's scheme, host, port and path and drops its user name, password
  (also when the address is one Go couldn't read), query string and fragment; it replaces with
  `***` the values of settings, JSON and XML fields and HTTP headers named like credentials (the
  whole value of `Authorization`), private key blocks, and the tokens some services put in an
  address's path (`engine/redact.go`, with its cases in `test/redact_cases.json`). The setting
  names are checked against every setting rclone marks as a password or as sensitive, so a new
  one fails the tests until it is classified. A logged line is cut to 8,000 characters, after
  redaction. It works by pattern: a credential in a shape it doesn't know, such as a field printed
  as `Name:value`, would pass, so the contributing guide asks people to read a log before sharing
  it. The log does name servers, their addresses, user names and files. A
  crash of the engine (a Go panic) is written by Go's runtime straight to the system log under
  the `Go` tag, without redaction; that tag is not part of the log the guide asks for.
- **Browser sign-in** (`engine/pkce.go`, `engine/signin.go`): PKCE with no client secret in the
  app for Dropbox and OneDrive; the loopback receiver on port 53682 holds both `127.0.0.1` and
  `[::1]`, or refuses to start, so another app on the device can't take the answer.
- **Box's client secret is in release APKs, readable by anyone.** Box offers no sign-in for apps
  that can't keep a secret, so one-tap Box sign-in needs the registration's client secret on the
  device. It is not in this repository: `build.sh` compiles it into the engine from a file outside
  it, and a release built that way carries it as plain text that anyone can extract. rclone ships
  its own Box secret in its public source for the same reason. The secret identifies the app, not
  a person, and gives no access to anyone's files without that person's own sign-in; the realistic
  risk is someone reusing the registration and Box revoking it. Because the engine needs the
  secret for every token refresh, that would stop one-tap Box sign-in and also break Box servers
  already signed in that way, until a release with a new registration and a fresh sign-in. A build
  from this source without that file carries no Box secret, and Box connects there with an app's
  settings file instead. Neither is a vulnerability to report.
- **SFTP host keys** (`engine/ssh.go`): a server's key is pinned on first use, after the person
  checks its fingerprint; a different key is refused.
- **What other apps can reach.** The release build's manifest exports three components; everything
  else (the other screens, both services, the notification receiver) is `exported="false"`.
  - `MainActivity`: the launcher screen. It reads nothing from the intent that opens it.
  - `ReturnActivity`: opened by the `driveway://signin` link on the page a browser sign-in comes
    back to. It reads nothing from the link: it closes, or opens the server list.
  - `DrivewayProvider` (`DrivewayProvider.java`), the documents provider: it requires
    `MANAGE_DOCUMENTS`, which only Android's documents UI holds, so other apps reach a server only
    through a file the person picks there; a document id never reaches outside the server's own
    folder.
  The weekly update job also requires `BIND_JOB_SERVICE`, so only the system can start it.
- **Saves** (`Uploads.java`, `engine/upload.go`): a file on the server is replaced only by a
  complete upload; on NFS the temporary file is owner-only while it uploads (`engine/nfs`).
- **The update check** (`UpdateCheck.java`): one request to GitHub's public API with no
  identifiers; the page it opens is fixed in the app, never taken from the answer; nothing is
  downloaded or installed.
- **The test harness** (`src-debug/`) exists only in debug builds and answers only adb (its
  receiver requires `android.permission.DUMP`). A release build that contains it is a bug.

rclone and the other Go modules compiled into the engine are listed in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md); report problems in them upstream as well.

## Supported versions

Only the latest release gets fixes.

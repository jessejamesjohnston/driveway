# Security

## Reporting a problem

Please report security problems privately, not in a public issue: use GitHub's
[private vulnerability reporting](../../security/advisories/new) for this repository, or write to
hello@merrimentlabs.com. Say what you found, how to reproduce it, and which version (Settings,
About) you tested. You will get an answer as soon as I can manage; this is a one-person project.

## What is in scope

- **Stored sign-ins** (`Servers.java`): passwords, keys, key files and tokens are kept in the
  server list, encrypted under an Android Keystore key and excluded from backup (`allowBackup` is
  off). The app never writes them to its log. One address check (`engine/address.go`), which
  the form calls as you leave a field and on Connect and Save, and which the engine runs again
  before rclone sees the settings, covers every setting rclone declares as an address (a URL, an
  endpoint, a host). A user name and password typed into an address
  (`https://you:password@host/...`) move to the server's own User name and Password settings when
  those are empty; when they hold a login, that login is used and the address's copy is taken
  out, and the form says which. A service without such settings refuses a user name and password
  in an address, except where rclone documents the address as the way to sign in (the HTTP kind,
  a proxy); Storj's satellite address takes only a node ID before its `@`. An address Go's url
  package can't read (a password with a space or a quote in it, a stray `%`, a missing
  `https://`) is refused before it reaches rclone, naming the setting and never repeating the
  address, because Go's own error repeats the address whole. One rule covers an address that
  reads two ways: when what follows the last colon of its authority is empty or only digits and
  an `@` comes after it, `https://alice:2024/word@host` reads as a user name and a password with
  a `/` in it as easily as a server, a port and a folder. The check refuses it, whatever the
  host or user name looks like and however far down the `@` is; nothing in it is moved, and the
  sentence gives both ways out: for an `@` in a folder's name, `%40` (as in
  `https://host:8443/remote.php/dav/files/me%40example.com/`); for a password, User name and
  Password where the service has them, `%2F` for a `/` where its sign-in stays in the address
  (the HTTP kind, a proxy), and otherwise that a user name and password can't go there. An
  address setting may not hold a look-alike of `@` anywhere, a look-alike of `: / ? #` or the
  dot in its server part (up to the first `/`, `?` or `#` after the scheme's `//`), or a
  look-alike of `: / ? #` in its path when the address also holds an `@`. A look-alike is a
  character whose NFKC form differs from it and holds one of those, such as a full-width `＠` or
  `：`, or one of a short list NFKC leaves alone (the modifier-letter colon, the ratio sign, the
  division and fraction slashes, the ideographic full stop). Go reads only the ASCII
  characters, so `https://alice:2024/word＠host` would read as a server, a port and a folder,
  and the services' errors would repeat the password with the look-alike percent-encoded; one
  service also rewrites an address with a look-alike colon in its server part into a form that
  keeps the password. A password needs an `@` after it, so without one a look-alike in the
  path ends nothing: folders named with the full-width `：` `？` `＃` `／` that Chinese and
  Japanese file names use, or with `⁉`, are accepted as typed, and so are dot forms in a
  folder. With an `@` anywhere in the address (a user name and password typed in it, say),
  folders with those full-width characters or `⁉` are refused too; dot forms in a folder are
  still accepted. A refusal doesn't repeat the address, and says to set the
  address to the folder that holds that folder and open it in Files when the character is
  part of a folder's name, or to retype it when it was meant as the ordinary character. The
  redaction below doesn't read a percent-encoded look-alike as an `@`, so this case rests on
  the check. Letters and digits in any script are accepted, in host names and in folders.
  A generated test (`engine/address_matrix_test.go`) puts every combination of user name,
  password, host and folder shapes into every address setting rclone has and checks the verdict,
  the redaction of Go's errors, and, with the network cut, what a connection attempt returns and
  logs. Settings the
  engine refuses replace the server's earlier ones, so nothing goes on working on the settings
  the person changed.
- **What reaches the log.** An error's text can carry a credential: some services put an API key
  or token in the request address (premiumize.me, Mail.ru, Linkbox), and Go includes the address
  in a network error. The engine redacts every error it returns and every line it logs (rclone's
  logger, Go's `log` package and, on Android, everything written to stdout and stderr, which
  appears under the `GoLog` tag), and the app redacts each line it logs again (`Logs.java`, the
  `Driveway` tag). The debug-only test harness logs through the same redaction; `LogsTest` scans
  the app's and the harness's code for any log call or console write that goes around it.
  Redaction keeps an address's scheme, host, port and path and drops its query string and
  fragment, and its user name and password in the shapes the cases file covers (among them an
  address Go couldn't read and a scheme typed twice). An address that reads two ways (the same
  rule, for text the check never saw) loses everything from its `//` to its last `@`, host and
  port included; so does one written with `//` and no scheme or with no scheme at all
  (`alice:2024/word@host`). An address with one slash after its scheme, as a service may
  rebuild one (Koofr's client turns an address into `https:/word@host`), loses everything before
  its last `@`. An address with an `@` after a `?` or `#` in its path loses the path with the
  query. An address with an `@` in its path loses the path up to that
  `@` but keeps its host (Go's HTTP client drops an empty port, and with it the colon a password
  followed); it replaces with `***` the values of settings, JSON and XML fields
  and HTTP headers named like credentials (the whole value of `Authorization`, also written as a
  setting, and of a mnemonic or passphrase of several words), private key blocks of any length,
  and the tokens some services put in an
  address's path (`engine/redact.go`, with its cases in `test/redact_cases.json`). The setting
  names are checked against every setting rclone marks as a password or as sensitive, so a new
  one fails the tests until it is classified. Each line the engine logs, and each entry the app
  logs (a stack trace is one entry), is cut to 8,000 characters, counted the same way in both,
  after redaction. It works by pattern: a credential in a shape it doesn't know, such as a field printed
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

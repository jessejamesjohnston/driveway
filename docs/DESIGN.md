# How Driveway fits together

**Files and other apps** talk to `DrivewayProvider`, an Android documents
provider. Each server is a root; a document id is `serverId:path`, with the
path relative to the folder the server starts in.

**The engine** is rclone, compiled into the app with gomobile (engine/). The
app keeps the servers (encrypted with an Android Keystore key) and hands each
to the engine with `SetRemote`; rclone never reads or writes a config file.
Errors carry a category (`[network]`, `[auth]`, `[notfound]`, ...) that the
app turns into sentences (Errors.java).

**Listings answer at once.** Files gives a provider about 20 seconds before it
declares it not responding and Android kills it. A listing returns what it
has, marked loading, fetches in the background, and asks Files to query again
when the contents arrive. Results are kept for 5 seconds; any change Driveway
makes clears its folder's listing.

**Each engine call Files waits on has one deadline.** A stat, opening a
file, a new document, a delete, a rename or a copy on the server ends 18
seconds after the engine call starts, connecting included; each step inside
keeps its own shorter limit but ends with the call. A connection that
finishes after its call gave up is kept for the next one. Tidying up after a
failure (a partial upload or copy) has its own few seconds, even past the
deadline. A delete, rename, move or copy that first waits for a pending save
of the same file (up to 10 seconds) hands the engine only what is left of
the 18, so the whole call still answers in time; a copy picks its free name
in the same engine call.

**Reads stream.** Opening a file for reading returns a proxy file descriptor;
each read fetches that range from the server, reusing one stream while reads
are sequential and reopening at the new offset after a jump.

**Writes are whole or not at all.** Opening for writing returns a proxy file
descriptor over a local file. When the app closes it, Driveway uploads under a
temporary name beside the original (`.driveway-…`, hidden from listings),
checks the size, and renames it over the original. Most protocols replace in
one step (SFTP posix-rename, SMB ReplaceIfExists, WebDAV Overwrite, NFS
RENAME); otherwise the old file is moved aside first and put back on failure.
Android reports the save as done when the app closes the file, before the
upload; a failure later shows a notification with Try again, and the bytes
stay on the device until the server has them.

**Android 15 to 17.** Network is cut for apps in the background, and a
provider only counts as foreground while the app using it is on screen: a
copy Files runs from its own background service, or an upload after the
saving app has moved on, would lose its connection. So any use of a server
while Driveway is allowed to start one (Files or the saving app on screen)
starts SessionService, a foreground service with a quiet "Connected to your
servers" notification; it stops itself three minutes after the last use,
once no upload is running. Android 17 needs `ACCESS_LOCAL_NETWORK`
("Nearby devices") to reach anything on the local network; without it
connections time out silently, so Driveway asks the first time a server is
added or looked for, and its errors name the permission when it's missing.

**Copies stay on the server when they can.** Files asks a provider to copy
within itself first. Driveway does it on the server: WebDAV's COPY, the
object stores' copy, and on SFTP servers with a shell, `cp` over the same
login and pinned host key (rclone's own SFTP copy is a hard link, which is not
a copy), for files up to 512 MB (Files stops a provider that takes over
about 20 seconds). Where the server can't (SMB, NFS, FTP, SFTP-only accounts,
bigger files), Driveway
says no and Files copies through the device, which it does on its own.

**Saves the device can't hold.** A save is staged on the device in full,
so Driveway refuses to start one with under 64 MB free, and if the device
fills up mid-save the app gets "no space left", a notification says why, and
the incomplete copy is dropped, never uploaded: the server keeps its file.

**Progress.** Uploads report bytes sent; the notification reads "Saving
report.pdf to Home server, 45% · 120 MB of 268 MB" (or "Saving 3 files").

**Permissions when needed.** Nothing is asked at launch. "Add a server" and
"Look for servers" ask for Nearby devices (local network) and notifications
the first time; adding goes ahead either way. Every Allow button (the home
banner, Settings, Look for servers, a failed check's Allow Nearby devices)
shows Android's own dialog while Android still asks, and opens Driveway's App
info page only once the permission was refused for good (twice): Android
doesn't say beforehand which it will be, so Driveway notes what it has asked
for (`Permissions`). A "Don't allow" in the dialog is the answer; App info
never opens after it.

**Every rclone service.** "More services" lists all of rclone's backends.
Each has a curated form (`engine/forms.go`, kept complete and true to rclone
by `TestForms`): the service's name as people call it, the few settings it
can't connect without (above the Connect button), and everything else rclone
offers under a collapsed "More settings". A service signs in through the
browser only if it uses rclone's shared sign-in settings; any other "token" setting is the service's own (Linkbox's API
token, shown) or one rclone keeps for itself (File Fabric's and Shade's
session tokens, hidden). Setup runs rclone's own steps (its config state
machine) one question at a time; a form can answer known questions itself
(presets, never kept) and start a new server with settings filled in
(prefill). Services that sign in on
a web page use rclone's sign-in unchanged: Driveway catches rclone's "open a
browser" hook and opens the page; the service sends the browser back to
rclone's server on http://127.0.0.1:53682 on the device; rclone trades the
code for a token. A browser may deliver "localhost" to [::1] (Chrome does on
Android 16), so a sign-in owns both loopbacks: Driveway's own receiver
listens on both, and for rclone's (127.0.0.1 only) Driveway holds
[::1]:53682 and redirects to 127.0.0.1 with the same path and query.
Another app already on [::1]:53682 stops either sign-in before the page
opens ("another app on this device is using the port the sign-in comes back
to"). A foreground hold keeps Driveway's network while the person
is in the browser. Tokens rclone refreshes later are handed back to the app
and saved, since some services replace the refresh token every time.
Driveway also turns on two rclone settings by default: listing hidden files
on FTP (its temporary names start with a dot) and folder markers on object
stores (so empty folders stay).

**Generations.** Every save is stamped with the time the app opened it, kept
in its staging note with whether the app finished writing. The newest
generation of a file always wins: over a late close, a second Try again, or
a save resumed after Driveway was stopped. Unfinished writes are never
resumed. A move Driveway refuses (Files then copies and deletes) gets a
record there too, so its original stays until the copy has reached its
server even if Driveway is stopped in between. Saves begun or handed over
since the refusal hold it; the note keeps when each save was first handed
over, so one from before the refusal, still failing on another server,
doesn't hold it after a restart either.

**SFTP identity.** The first connection shows the server's fingerprint and
pins its key (known_hosts per server); a server that answers with a different
key is refused. A server saved without a pinned key has an empty known_hosts,
so it is refused, not trusted.

**NFS.** NFSv3 over TCP, AUTH_SYS with a configurable uid/gid, the portmapper
by default or one fixed port. Android apps can't use privileged ports, so the
export must allow others ("insecure"; Synology's "non-privileged ports").

## Driveway's own screens

Driveway's screens matter at three moments: adding a server, something going
wrong, and housekeeping. They share the look of Merriment Labs' other apps:
Driveway's own page and card colors (no wallpaper color), Figtree (bundled,
`res/font`), cards, pill buttons, outlined fields, Material Symbols icons on
soft tiles, a gear for Settings, "this device", no toasts.

- **Home**: one compact row per server: its tile, its name, and a second
  line only when it adds something (the kind and address, the starting
  folder). Tapping the row opens the server in Files; Change and Remove are
  in its ⋮ menu (also long press, right-click, the Menu key or Shift+F10).
  Being in Files is the normal state and isn't said; a status line shows
  only what needs a hand: a save that
  didn't reach the server (Try again, Discard, which asks first), "Driveway is no
  longer signed in" (Sign in again; Change for a server that can't sign in
  again, below; Remove for one with no other way in) or "answered as a different server than
  before" (Check identity). The last two come from `Problems`, a
  server id and a word remembered when Files or a save meets that error, and
  cleared by the next success or by saving the server (an SFTP server saved
  before its identity was checked offers Check identity at once); a form's
  Test never records one. Servers found on the network show as one line on
  top that leads into Add a server. A server just saved "parks": its row slides in
  (200ms) and says "Parked in Files" for a few seconds; with animations off
  the line simply shows.
- **Add a server** is a path of three steps. "Where are your files?" lists
  servers found on the network, then the four kinds as tiles, then the cloud
  services that sign in with one tap (Box, Dropbox and OneDrive, A to Z) and
  More services (a searchable page of every other rclone service, one row
  each). Every service whose sign-in would
  borrow an app registration built into rclone (the engine's
  `borrowedSignIn`, kept complete by `TestBorrowedSignIn`) offers no browser
  sign-in: a published app may not present itself as another developer's
  app. Dropbox and
  OneDrive sign in with Merriment Labs' own registrations: Driveway runs the
  browser sign-in itself (PKCE, no secret in the app, `engine/pkce.go`) and
  hands rclone the token; servers that signed in with rclone's registration
  keep it until they sign in again. Box signs in the same way when the build
  carries Merriment Labs' Box registration, whose client secret `build.sh`
  reads from a file outside the repository and compiles into the engine (Box
  has no sign-in without a secret), so anyone holding such an APK can read
  the secret out of it (SECURITY.md). A build without that file (any build
  from this source, unless you supply a registration of your own as
  CONTRIBUTING.md describes) connects to Box with an app's settings file
  instead.
  Only these sign in again (`Kinds.signsInAgain`): a server of
  any other service that signed in through the browser before Driveway
  stopped offering that keeps working with its token, but nothing offers to
  sign it in again (that would borrow rclone's registration). When its
  sign-in ends, its card, Files and its notifications say to choose Change,
  and its form opens at the other way in, with the same "Connect with"
  note a new server shows. One with no other way in says it can't sign in
  again and offers Remove. Where
  rclone has another way in, the service is set up that way and its page
  says what to connect with: Google Drive and Google
  Cloud Storage with a service account key, Box (in a build without the Box
  registration) with an app's settings file (JWT), premiumize.me with its API key, Jottacloud with
  a personal login token. Key and settings files are chosen with Android's
  file picker (no storage permission), checked on the device first
  (`CheckCredential`: the right kind of file, a private key that opens), and
  kept, like passwords, in the encrypted server list. The rest (pCloud,
  Yandex, Zoho, HiDrive, Huawei, put.io, SugarSync: their only sign-in would
  borrow rclone's registration) and Oracle (a config file on disk) have no
  way in Driveway offers (`methodUnavailable`) and are
  hidden: each form has a `State` (`listed` or `hidden`),
  the one value that turns a service on or off; hidden ones aren't in More
  services, "Where are your files?" or a search. Nothing is listed before
  it works (`TestCatalogStates` fails on a listed service with no way in,
  and the app shows only `Kinds.offered` services). A server of any of
  these added before keeps working and can be edited.
- **New labels.** Each form has `Since`, the app version that first
  listed it (none for the services listed when New labels began). After an
  update, a service listed since the version last run carries a "New" chip
  (read "pCloud, new") in More services and, for the services it names, in
  "Where are your files?", until the person has seen it on
  two visits to Add a server (a visit counts once the row has been at least
  half on screen). A fresh install labels nothing; the version is noted at
  every start, so an update labels only what it added. The state is
  in the app's own preferences (`new_labels`; backups are off), never sent
  anywhere. The logic is plain Java (`NewLabels`), checked on the JVM by
  `./build.sh test` (`test/java`). Turning a hidden service on is three
  edits to its form: set its method and registration, drop
  `State: stateHidden`, and set `Since` to the release that lists it.
- **Removed.** Google Photos (its API now
  shows an app only the photos it uploaded itself), ShareFile (a paid
  account and an https sign-in address), and every service that only
  layers on top of another server (crypt, union, chunker, combine,
  compress, hasher, archive, alias, cache: what they make can only be read
  through Driveway, too narrow a use) are gone: not listed, no forms, out of
  the engine (`engine/rclone_backends.go`; crypt's code stays linked because
  rclone's own operations import it, but it is unregistered). A server of
  one added before shows on its card that Driveway no longer works with it,
  is not in Files, and offers only Remove.
- **The details step.** SFTP, SMB, NFS and WebDAV keep their hand-made forms
  (Server, Sign in and Folder, Connect pinned at the bottom). Every other
  service's form is one card: "Name in Files" (prefilled with the service's
  name; for a service with an address, it follows the address until typed
  in), the essentials, Start in folder; then one centered "Connect to
  <service>" (for a browser sign-in, "Opens <service>'s sign-in page in your
  browser" under it, and that the access stays encrypted on this device);
  then More settings, folded. Changing a server opens the same form with
  Save, Test connection and Cancel in the bar.
- **Connecting** is a live checklist (`Connection`): Found, Identity (SFTP),
  Signed in, Folder opened. SFTP's first identity is asked inline: the
  fingerprint with Cancel focused for the keyboard and "Trust and continue";
  a changed key says so in red and is still refused unless trusted here.
  Browser sign-ins wait at their step with "Open the page again" and "Cancel
  sign-in"; rclone's setup questions are asked at the step too. A failure
  stops at its step with the fix in place (Allow Nearby devices, Change
  sign-in, Change folder, Sign in again for Dropbox and OneDrive, Try
  again), and Save anyway stays
  available. For SMB, NFS and WebDAV the engine finds the server and signs in
  in one call, so "Found" comes from a quick connection to the server's port
  (4 s, under the same Nearby devices permission); if that look fails, the
  engine's own answer decides (a refused password means it was found).
- **The finish**: "<name> is in Files", with Driveway's house and a car that
  rolls up the driveway (500ms; parked from the start with animations off),
  Open Files and Done.
- **Changing a server** skips the path: its form opens with Save, Test
  connection and Cancel (and Sign in again for Dropbox and OneDrive), and
  the same checklist appears under the form. While the checklist runs (Save
  or Test connection), the form is locked: its fields,
  choices, Choose file, Make a key and Forget, and Sign in again are
  disabled, so what the run checks is what it saves; Cancel, More settings,
  Copy and the checklist itself (a question's answer, Trust, Cancel
  sign-in) stay usable, and everything comes back when the run ends, also
  on a screen rebuilt around it.
- **A connection outlives its screen.** `Connection` holds only the
  application context; a change of theme, text size or density rebuilds the
  screen around the same run (handed over in memory with the form), so a
  test, a save or a sign-in in progress carries on and the new screen shows
  where it is. Leaving the screen cancels it.
- **Settings**: Permissions (Nearby devices, Notifications, each with Allow
  Nearby devices / Allow notifications when off), then the About block every
  Merriment Labs app shares (version, Merriment Labs, Source code, Privacy,
  Open-source notices, License, Check for updates and the opt-in weekly
  check), then Help and Contact, and the copyright line. The update check
  (`UpdateCheck`, `UpdateJob`) asks GitHub's public latest-release endpoint
  for the newest version and opens the release page; it sends nothing about
  the person or the device, and with the weekly check off it runs only when
  tapped. Licenses are a screen of their own:
  Driveway's license first, then every component, each opening its full text
  as a page. The typeface (OFL-1.1) and the icons (Apache-2.0) are listed with
  the Go modules (`tools/notices`).

## Where the Add a server code lives

`ServerActivity` runs the Add a server path and the Change server form. Parts with a clean
boundary live in their own classes: `SetupChecklist` (the live checklist and its questions),
`NewLabelRows` (the New chips on service rows) and `FormViews` (the form's field, label, card and
section builders). `Layouts` and `Ui` hold the layouts and widgets every screen shares.

The rest is still one class of about 1,900 lines, in this order: the screen's state and the
in-memory handoff to a rebuilt screen (`Kept`, `restore`); Back, Escape and the keyboard
shortcuts; Where are your files (network discovery, the kind tiles); More services (the
searchable list); the details form (building it, locking it while a connection runs, reading it
back in `collect`); the key and settings-file fields; and the connecting, checklist and finish
steps. No method is longer than about 80 lines.

It stays together for one reason: the form's fields (the inputs, chosen files, a made SFTP key,
the status line, the connection in progress) are read and written by nearly every step, and a
change of theme or text size hands all of it to a new screen in one object, with work that
finishes afterward (a key being made, a file being read) finding the screen on show through one
static reference. Splitting the form out properly means a form-state object that owns those
fields and that handoff, which moves who owns what. Such a split would be its own series of
changes, checked after each step with the on-emulator harness and the screen walk; the pieces
that depend least on the shared fields are the More services page and discovery on Where, and
the form's state with its key and file fields would come last.

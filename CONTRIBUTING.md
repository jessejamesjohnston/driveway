# Contributing to Driveway

Thanks for taking a look. Driveway is maintained by one person in spare time, so there is no
support promise and replies can take a while.

## Issues

Bug reports and ideas are welcome as [issues](../../issues/new/choose). The exact message Driveway
showed, the kind of server and its make (if it is a NAS) help most. Please leave out server
addresses, user names and anything else personal. Security problems go through
[SECURITY.md](SECURITY.md), not a public issue.

## Pull requests

Pull requests are read. This public repository is a copy of a private working repository, so a
change that fits is applied there and carried back here as its own commit, credited to you in the
commit message. Small, focused changes with a test are the easiest to take. Before a larger change,
open an issue so we can agree on the shape first. There is no contributor agreement; contributions
are accepted under the [Apache License 2.0](LICENSE).

## Building and testing

Driveway builds without Gradle: `build.sh` drives the Android SDK's own tools. You need Linux on
x86_64, JDK 21 (with `javac`, `keytool` and `jarsigner`; the version the build and CI are tested
with), Python 3, unzip, `ssh-keygen`, and an Android SDK with build-tools and
`cmdline-tools/latest`, its licenses accepted (`sdkmanager --licenses`). Point `ANDROID_HOME` at
the SDK (default `~/Android/Sdk`). Docker runs the test servers.

```sh
./build.sh fetch     # once: Go, gomobile, go-licenses, the Android jars and the NDK
./build.sh unit      # go vet, the engine's unit tests, the app's plain-Java checks
./build.sh test      # everything, against throwaway servers it starts in Docker
./build.sh debug     # build/Driveway-debug.apk, with the adb-only test harness
```

CI runs `fetch`, `test` and `debug` on every pull request. [docs/TESTING.md](docs/TESTING.md)
covers the on-emulator harness, the screen walk and the sign-in checks.

Where things go: `~/.cache/driveway` and `~/.cache/android` hold the toolchain (`DRIVEWAY_CACHE`
and `ANDROID_CACHE` move them). A Debian `aapt2` on PATH is used before the SDK's build-tools.

## What the code holds to

- **Licenses.** `tools/licenses.py` fails the build on any Go module outside a permissive set
  (MIT, BSD, Apache-2.0, ISC and the like). MPL-2.0 is allowed only unmodified: a `replace` of an
  MPL module fails the build. GPL, LGPL and AGPL code is never compiled in; a package under one of
  them is replaced (as `engine/third_party/sddl` replaces an LGPL one).
- **Other apps that do the same job.** Compare behavior only. Do not read or copy the code of a
  GPL-licensed app with the same purpose.
- **Your own sign-in registrations in a fork.** `engine/pkce.go` carries Merriment Labs' public
  client IDs for Dropbox and Microsoft. A fork that ships to other people should register its own
  apps and put its IDs there, so its users sign in to the fork, not to Driveway. Box's
  registration needs a client secret, which `build.sh` reads from a file outside the repository
  (`DRIVEWAY_BOX_CLIENT`, default `~/.config/driveway/box-client`); without it Box connects with an
  app's settings file and offers no one-tap sign-in. To turn one-tap Box on in your own build,
  create an OAuth 2.0 app in the Box developer console with `http://localhost:53682/` as its
  redirect URI and read and write access to files and folders, then put its two values in that
  file as `BOX_CLIENT_ID=...` and `BOX_CLIENT_SECRET=...` lines. Any APK built that way carries
  the secret where anyone holding the APK can read it ([SECURITY.md](SECURITY.md)).
- **Test servers only.** The engine's checks run against the throwaway servers in `test/servers`,
  never a real NAS or account. Fixtures use made-up names, addresses and IDs.
- **User-facing text** lives in `res/values/strings.xml`, in sentence case. `StringStyleTest`
  checks it on every build: no closing period on a single sentence, an ellipsis only on something
  in progress, a middle dot or a comma (never a dash or a semicolon) to join two parts, American
  spelling and natural contractions. `KindsTest` holds each kind's own labels, blurbs and hints to
  the same rules. An error says what happened and what to do, in Driveway's words; the engine's or
  a library's own words go to the log, never on screen (`ErrorsTest` checks both).
- **Zero warnings.** `javac -Xlint:all` runs with `-Werror`, and `go vet` runs before every build.
- **Comments say why**, for a stranger reading the code later.
- **A refactor changes no behavior** and lands apart from fixes.

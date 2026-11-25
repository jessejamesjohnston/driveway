# Releasing Driveway

Driveway is released as a signed APK on GitHub. Every release is built from a clean checkout of
its tag and signed on the maintainer's machine with the app's release key, by
`scripts/release.sh`. CI never sees the key and never builds a release; it builds and tests only.

## The release key

- One key signs every release, so each one installs as an update over the last. A lost key or
  password means no more updates to installed copies, so the keystore is backed up on a second
  machine and its password lives in a password manager, never in a file.
- Certificate SHA-256 (published in the README and in every release's notes so anyone can check
  an APK):
  `C5:19:F7:89:47:10:A0:7D:A2:F0:18:88:7C:93:9F:A6:55:27:9F:4C:56:B2:39:F3:DB:9F:3F:D0:70:EC:07:9E`
  (`apksigner verify --print-certs` prints the same digest in lowercase with no colons:
  `c519f7894710a07da2f018887c939fa655279f4c56b239f3db9f3fd070ec079e`). `scripts/release.sh --verify <apk>` checks it for you,
  and `DRIVEWAY_EXPECTED_CERT` takes either form.
- Never commit a keystore (`*.jks`, `*.keystore`, `*.p12` and `*.pfx` are gitignored) and never put the
  password in a file, a command line or a log.

How `build.sh` gets the key: a release build signs with `~/.config/driveway/keystore.jks`
(`DRIVEWAY_KEYS` names another folder, `DRIVEWAY_KEYSTORE` another file, and
`DRIVEWAY_KEY_ALIAS` a key in it). The password comes from `DRIVEWAY_KEYSTORE_PASS`, or is asked
for in the terminal. A release signed with that key builds only from a git checkout with nothing
uncommitted, because it records the commit it was built from in
`META-INF/version-control-info.textproto` (the same record the Android Gradle Plugin writes into
Gradle apps' release builds). When there is no release keystore, `DRIVEWAY_TEST_KEY=1 ./build.sh`
signs a release build with the throwaway test key instead (a keystore, when present, always wins);
such a build must never be distributed.

Box: when `~/.config/driveway/box-client` exists (`DRIVEWAY_BOX_CLIENT` names another file), the
build compiles Merriment Labs' Box registration in, for one-tap Box sign-in, and the APK then
carries its secret ([SECURITY.md](../SECURITY.md) says what that risks). Without the file, Box
connects with an app's settings file.

## Signing your own build

You can build and sign Driveway with your own key. Android will not install it over an official
release (the signatures differ), so uninstall the official copy first; your servers will need
adding again.

1. Set `android:versionName` in `AndroidManifest.xml` to your version and commit. The tag must
   be `v` plus that version: for the tag `v0.3.0-mine` in step 3, `versionName` is `0.3.0-mine`.
2. Find your certificate's SHA-256. The script refuses an APK signed with any other certificate,
   and without this it expects the official one:

   ```sh
   keytool -list -v -keystore ~/keys/mykey.p12 | grep 'SHA256:'
   ```

3. Tag and build:

   ```sh
   git tag v0.3.0-mine
   DRIVEWAY_KEYSTORE=~/keys/mykey.p12 \
   DRIVEWAY_EXPECTED_CERT=<the SHA256 value from step 2> \
   scripts/release.sh v0.3.0-mine
   ```

The script asks for the password. Without a terminal, set `DRIVEWAY_KEYSTORE_PASS` in the
environment instead. It uses that one password for the keystore and the key, so your key password
must equal your store password (keytool makes PKCS12 keystores that way). A keystore with one key
is used whatever the key's alias; if yours holds several, name one with `DRIVEWAY_KEY_ALIAS`.

## Every release

1. **Version.** Raise `android:versionCode` by one and set `android:versionName` in
   `AndroidManifest.xml`. An installed copy refuses an update whose `versionCode` is not higher.
2. **Notices.** Every build regenerates `THIRD_PARTY_NOTICES.md` from the Go modules compiled in.
   If a dependency changed, commit the regenerated file.
3. **Changelog.** Write the release's section in `CHANGELOG.md`.
4. **Green.** `./build.sh test`, then on an emulator `tools/harness.sh` and `tools/screens.sh`
   ([TESTING.md](TESTING.md)).
5. **Tag.** Commit, then make a signed tag `vX.Y.Z` and push it. A tag starts no release build.
6. **Build.** `scripts/release.sh vX.Y.Z`. It clones the tag into a temporary directory, runs
   `./build.sh` there, and verifies the APK:
   - `apksigner verify`, the certificate SHA-256 equals the expected one, and the certificate is
     neither the test key nor a debug key;
   - `META-INF/version-control-info.textproto` names the tag's commit;
   - no debug-only test harness, and native code for exactly the two ABIs the app ships.

   Only then does it write `dist/driveway-X.Y.Z.apk` and `dist/SHA256SUMS`.
   `scripts/release.sh --verify <apk>` runs the checks on any APK (without the commit comparison).
7. **Try the release build.** Install the APK on an emulator with no debug build of Driveway, add
   a server, open it in Files, save a file to it, and open Settings, About, Open-source notices
   and Check for updates.
8. **Publish.** Create the release as a draft from the tag, attach the APK and `SHA256SUMS`,
   paste the changelog section and the certificate SHA-256 into the notes, then publish:

   ```sh
   gh release create vX.Y.Z dist/driveway-X.Y.Z.apk dist/SHA256SUMS --draft \
     --title "Driveway X.Y.Z" --notes-file <notes.md>
   gh release edit vX.Y.Z --draft=false
   ```

   The in-app update check reads GitHub's latest release, so a published release is what
   existing users will be told about. Pre-releases and drafts are never offered.

## CI

`.github/workflows/build.yml` runs the tests (against the Docker test servers) and a debug build
on every push to `main` and on pull requests. It has read-only permissions, pins every action to
a commit, holds no secrets and has no release job.

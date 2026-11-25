#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Builds and signs Driveway without Gradle, with the Android SDK's own tools
# (aapt2, javac, D8, zipalign, apksigner), plus the file engine: rclone and
# Driveway's Go code (engine/), compiled for Intel and Arm with gomobile.
#
#   ./build.sh fetch   once per machine: Go, gomobile, go-licenses and the
#                      Android jars (~/.cache/driveway, ~/.cache/android). The
#                      Android NDK comes from an SDK (ANDROID_HOME); Google
#                      ships it for x86_64 Linux and macOS only.
#   ./build.sh         builds build/Driveway.apk
#   ./build.sh bundle  also builds build/Driveway.aab, an Android App Bundle (bundletool)
#   ./build.sh unit    the checks that need no servers: go vet, the engine's
#                      unit tests and the app's plain-Java checks (test/java)
#   ./build.sh test    everything: the plain-Java checks, then the engine's
#                      checks against the throwaway servers in test/servers,
#                      which it starts in Docker (and stops again if it started
#                      them). Without Docker it runs the unit checks and says
#                      which checks it skipped.
#   ./build.sh servers starts the test servers and leaves them running (for the
#                      on-emulator harness, tools/harness.sh)
#   ./build.sh cover   the same checks, measured: the engine's statement
#                      coverage (against the test servers, started and stopped
#                      as for test), and line and branch coverage of the plain Java
#
# The release key is not in the repository: it lives in ~/.config/driveway
# (keystore.jks; the password is typed or set in DRIVEWAY_KEYSTORE_PASS; DRIVEWAY_KEYS points elsewhere,
# DRIVEWAY_KEYSTORE names the file itself, DRIVEWAY_KEY_ALIAS picks a key in it).
# Debug builds are always signed with the dev key in ~/.cache/driveway, which
# can't update a Driveway installed from a release (and vice versa). A release
# build without the release key stops (DRIVEWAY_TEST_KEY=1 signs it with the
# dev key anyway).
#
# A release build records the commit it was built from in the APK and the
# bundle (META-INF/version-control-info.textproto), and one signed with the
# release key refuses to build from anything but a clean git checkout.
# scripts/release.sh builds a release from a clean checkout of a tag.
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")"
C=${ANDROID_CACHE:-$HOME/.cache/android}
B=${DRIVEWAY_CACHE:-$HOME/.cache/driveway}
SDK=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Android/Sdk}}
GO_VERSION=1.27.1
NDK_VERSION=28.2.13676358
GOMOBILE=v0.0.0-20260908204917-8b95e45f8d3e
BUNDLETOOL=1.18.3
# Test-only jars, never in the APK: JaCoCo (./build.sh cover) and AOSP's own
# org.json (Apache-2.0), which the plain-Java checks run against in place of
# android.jar's stubs.
JACOCO=0.8.15
JSON_JAR=android-json-0.0.20131108.vaadin1.jar

# GOWORK=off: only engine/go.mod decides what is compiled in, so a go.work
# somewhere above can't swap in a modified module the license gate never sees.
export GOPATH=$B/gopath GOFLAGS=-modcacherw GOTOOLCHAIN=local GOWORK=off
export PATH=$B/go/bin:$GOPATH/bin:$PATH
export ANDROID_HOME=$SDK ANDROID_NDK_HOME=$SDK/ndk/$NDK_VERSION

fetch() {
  mkdir -p "$C/dl" "$B"
  local repo=https://dl.google.com/android
  unzip_one() { python3 -c "import zipfile,sys; open(sys.argv[3],'wb').write(zipfile.ZipFile(sys.argv[1]).read(sys.argv[2]))" "$@"; }
  [[ -f $C/android-31.jar ]] || { download $repo/repository/platform-31_r01.zip "$C/dl/p31.zip" &&
    unzip_one "$C/dl/p31.zip" android-12/android.jar "$C/android-31.jar"; }
  [[ -f $C/android-37.jar ]] || { download $repo/repository/platform-37.2_r01.zip "$C/dl/p37.zip" &&
    unzip_one "$C/dl/p37.zip" android-37.2/android.jar "$C/android-37.jar"; }
  [[ -f $C/r8.jar ]] || download https://maven.google.com/com/android/tools/r8/8.5.35/r8-8.5.35.jar "$C/r8.jar"
  [[ -f $C/bundletool-$BUNDLETOOL.jar ]] || download \
    https://github.com/google/bundletool/releases/download/$BUNDLETOOL/bundletool-all-$BUNDLETOOL.jar "$C/bundletool-$BUNDLETOOL.jar"
  rm -rf "$C/dl"
  # Pinned by SHA-256 (each matched Maven Central's own .sha1 when pinned);
  # a download that doesn't match is thrown away.
  local central=https://repo1.maven.org/maven2
  checked_jar "$B/jacocoagent-$JACOCO.jar" fb5b0036a0899ea97edfa0fc2c7985b55f3f7c5695028163e5e60d4f3cf6075d \
    $central/org/jacoco/org.jacoco.agent/$JACOCO/org.jacoco.agent-$JACOCO-runtime.jar
  checked_jar "$B/jacococli-$JACOCO.jar" d2b74b20b415163c1f53261e7c5ef4445b788327094208ff821e0c2baf9bc8f1 \
    $central/org/jacoco/org.jacoco.cli/$JACOCO/org.jacoco.cli-$JACOCO-nodeps.jar
  checked_jar "$B/$JSON_JAR" dfb7bae2f404cfe0b72b4d23944698cb716b7665171812a0a4d0f5926c0fac79 \
    $central/com/vaadin/external/google/android-json/0.0.20131108.vaadin1/$JSON_JAR
  if [[ "$("$B/go/bin/go" version 2>/dev/null)" != *"go$GO_VERSION "* ]]; then
    local arch
    arch=$(uname -m | sed 's/aarch64/arm64/;s/x86_64/amd64/')
    mkdir -p "$B/go.new"
    download "https://go.dev/dl/go$GO_VERSION.linux-$arch.tar.gz" "$B/go.tar.gz"
    tar xzf "$B/go.tar.gz" -C "$B/go.new" --strip-components=1
    rm -f "$B/go.tar.gz"
    [[ -d $B/go ]] && mv "$B/go" "$B/go.old.$(date +%s)"
    mv "$B/go.new" "$B/go"
  fi
  (cd engine && go install golang.org/x/mobile/cmd/gomobile@$GOMOBILE golang.org/x/mobile/cmd/gobind@$GOMOBILE &&
    go install github.com/google/go-licenses/v2@v2.0.1 && go mod download)
  [[ -d $ANDROID_NDK_HOME ]] || "$SDK"/cmdline-tools/latest/bin/sdkmanager --install "ndk;$NDK_VERSION"
  echo "toolchain: $(go version), gomobile, NDK $NDK_VERSION"
}

# URL FILE: downloads URL to FILE. Python rather than curl, which reads
# files from the account's home (~/.curlrc, ~/.ssh/known_hosts) whatever
# HOME says, so a clean-room build could depend on them.
download() {
  python3 - "$1" "$2" <<'DL'
import shutil, sys, urllib.request
url, out = sys.argv[1:]
try:
    with urllib.request.urlopen(urllib.request.Request(url, headers={"User-Agent": "driveway-build"}), timeout=120) as r, \
            open(out, "wb") as f:
        shutil.copyfileobj(r, f)
except Exception as e:
    sys.exit(f"download failed: {url}: {e}")
DL
}

# FILE SHA256 URL: downloads FILE unless it is there and matches.
checked_jar() {
  [[ -f $1 ]] && sha256sum -c --status <<<"$2  $1" && return
  download "$3" "$1.part" || return 1
  sha256sum -c --status <<<"$2  $1.part" || { rm -f "$1.part"; echo "checksum mismatch: $3" >&2; return 1; }
  mv "$1.part" "$1"
}

need() {
  for f in "$@"; do [[ -e $f ]] || { echo "missing $f: run ./build.sh fetch" >&2; return 1; }; done
}

# The app's plain-Java logic on the JVM (no device): NewLabels, Kinds, the
# update check (UpdateCheck) and the parsing half of Errors, run against AOSP's
# org.json with stand-ins for the gomobile Engine class and the Android
# classes they name (test/stubs). Kinds
# reads the engine's real catalog (engine/backends_json_test.go writes it).
# $1: an option for the JVM (cover's JaCoCo agent).
JT=build/test-java
jvm_tests() {
  need "$C/android-37.jar" "$B/$JSON_JAR" || return 1
  rm -rf $JT && mkdir -p $JT/gen
  # R from res/, as aapt2 would make it for these two types (distinct ids).
  python3 - res $JT/gen/R.java <<'PY'
import glob, os, re, sys
res, out = sys.argv[1:]
drawables = sorted({os.path.splitext(os.path.basename(f))[0] for f in glob.glob(f"{res}/drawable/*")})
strings = sorted(set(re.findall(r'<string\s+name="([^"]+)"', open(f"{res}/values/strings.xml").read())))
lines = ["package com.merrimentlabs.driveway;", "public final class R {"]
for kind, names, base in (("drawable", drawables, 0x7f010000), ("string", strings, 0x7f020000)):
    lines.append(f"  public static final class {kind} {{")
    lines += [f"    public static final int {n} = {base + i};" for i, n in enumerate(names)]
    lines.append("  }")
open(out, "w").write("\n".join(lines + ["}", ""]))
PY
  (cd engine && DRIVEWAY_BACKENDS_JSON=../$JT/backends.json go test -count=1 -run '^TestBackendsJSON$' . >/dev/null) || return 1
  javac --release 17 -Xlint:all -Werror -encoding UTF-8 -cp "$C/android-37.jar" -d $JT/classes \
    src/com/merrimentlabs/driveway/{NewLabels,Kinds,Errors,Logs,UpdateCheck}.java $JT/gen/R.java \
    $(find test/java test/stubs -name '*.java') || return 1
  for t in NewLabelsTest KindsTest ErrorsTest LogsTest UpdateCheckTest StringStyleTest; do
    java ${1:+"$1"} -Ddriveway.backends=$JT/backends.json -cp "$JT/classes:$B/$JSON_JAR:$C/android-37.jar" \
      com.merrimentlabs.driveway.$t || return 1
  done
}

# The engine's statement coverage over both its packages (a block counts once
# whichever package's checks ran it), against the test servers, then the plain
# Java's under JaCoCo. Reports in build/cover (java/index.html shows the Java
# line by line).
cover() {
  local out=build/cover agent=$B/jacocoagent-$JACOCO.jar
  need "$agent" "$B/jacococli-$JACOCO.jar" || return 1
  docker info >/dev/null 2>&1 || { echo "./build.sh cover runs the engine's checks against the test servers, which need Docker" >&2; return 1; }
  rm -rf $out && mkdir -p $out/classes
  with_servers bash -c "cd engine && DRIVEWAY_SERVERS=1 go test -count=1 -coverpkg=./... -coverprofile=../$out/engine.out ./..." ||
    return 1
  echo "engine: $(python3 tools/cover.py go $out/engine.out $out/engine-merged.out)" \
    "= $(cd engine && go tool cover -func=../$out/engine-merged.out | awk 'END {print $NF}')"
  # (Failures and each check's summary line show; the passes don't.)
  jvm_tests "-javaagent:$agent=destfile=$out/java.exec,includes=com.merrimentlabs.driveway.*" | grep -v '^PASS ' ||
    return 1
  # Only the app's classes: not the checks, the stand-ins or the made-up R.
  find $JT/classes/com/merrimentlabs/driveway -maxdepth 1 \
    -regex '.*/\(NewLabels\|Kinds\|Logs\|UpdateCheck\)\(\$.*\)?\.class\|.*/Errors\.class' -exec cp {} $out/classes/ \;
  java -jar "$B/jacococli-$JACOCO.jar" report $out/java.exec --classfiles $out/classes --sourcefiles src \
    --xml $out/java.xml --html $out/java >/dev/null
  python3 tools/cover.py java $out/java.xml
}

# The SFTP server lets the tests in with a key pair made on this machine the
# first time (test-only, gitignored, used for nothing else).
SERVERS=test/servers
TEST_KEY=$SERVERS/keys/driveway
export KNFS_OPTS=${KNFS_OPTS:-}  # (the kernel NFS server's export options; compose warns when unset)
# The SFTP server's key, handed to compose (which copies it in) when the pair exists.
servers_env() {
  [[ -f $TEST_KEY.pub ]] && export DRIVEWAY_SFTP_PUBKEY=$(<"$TEST_KEY.pub")
  export DRIVEWAY_SFTP_PUBKEY=${DRIVEWAY_SFTP_PUBKEY:-none}
}
# Returns 0 when it made a new pair, 1 when the pair was there, 2 on failure.
test_key() {
  local f
  for f in "$TEST_KEY" "$TEST_KEY.pub"; do
    if [[ -e $f && ! -f $f ]]; then
      echo "$f is not a file (docker compose makes a directory there when it starts before the key exists): remove it and run again" >&2
      return 2
    fi
  done
  [[ -f $TEST_KEY && -f $TEST_KEY.pub ]] && return 1
  mkdir -p "$(dirname "$TEST_KEY")" || return 2
  rm -f "$TEST_KEY" "$TEST_KEY.pub" || return 2
  ssh-keygen -q -t ed25519 -N '' -C driveway-test-only -f "$TEST_KEY" || return 2
  echo "made the SFTP test key pair $TEST_KEY (test servers only)"
}

# Starts the test servers and waits until each one answers.
servers_up() {
  command -v ssh-keygen >/dev/null || { echo "the SFTP checks need ssh-keygen (openssh-client)" >&2; return 1; }
  local recreate=() made=0
  test_key || made=$?
  case $made in
    0) recreate=(--force-recreate) ;;
    1) ;;
    *) return 1 ;;
  esac
  servers_env
  docker compose --progress quiet -f $SERVERS/compose.yml up -d --quiet-pull "${recreate[@]}" >/dev/null || return 1
  python3 - <<'WAIT'
import socket, sys, time
# Port: the greeting the service sends first, or what it answers to a
# request. None: a connection that is accepted is all there is to check.
checks = {2222: b"SSH-", 2226: b"SSH-", 2121: b"220", 8080: b"HTTP/", 9000: b"HTTP/", 1445: None, 2050: None}
deadline = time.time() + 120
# Twice, a few seconds apart: a server that dies on its first connection
# (the FTP image has, once) restarts and is caught by the second round.
for port, want in [*checks.items(), (None, None), *checks.items()]:
    if port is None:
        time.sleep(3)
        continue
    while True:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=3) as s:
                if want == b"HTTP/":
                    s.sendall(b"HEAD / HTTP/1.0\r\n\r\n")
                if want is None or s.recv(16).startswith(want):
                    break
        except OSError:
            pass
        if time.time() > deadline:
            sys.exit(f"the test server on port {port} did not answer within two minutes")
        time.sleep(1)
WAIT
}

# Runs a command with the test servers up: starts them when they aren't
# running, and stops them again afterward only if it started them, so servers
# left up by ./build.sh servers (for the harness) stay up. Returns the
# command's status.
with_servers() {
  local started= status=0
  servers_env
  [[ -n $(docker compose -f $SERVERS/compose.yml ps -q 2>/dev/null) ]] || started=1
  servers_up || return 1
  "$@" || status=$?
  [[ -z $started ]] || docker compose -f $SERVERS/compose.yml down -v >/dev/null 2>&1
  return $status
}

unit_tests() {
  jvm_tests || return 1
  (cd engine && go vet ./... && go test -count=1 ./...)
}

DEBUG= BUNDLE=
case ${1:-} in
  fetch) fetch; exit ;;
  unit) need "$B/go/bin/go" || exit 1
    unit_tests; exit ;;
  servers) servers_up || exit 1
    echo "test servers running; stop them with: docker compose -f $SERVERS/compose.yml down -v"; exit ;;
  test) need "$B/go/bin/go" || exit 1
    if ! docker info >/dev/null 2>&1; then
      unit_tests || exit 1
      echo "Docker is not available, so the engine's checks against the test servers (SFTP, SMB," \
        "WebDAV, NFS, S3 and FTP) were skipped. Install Docker and run" \
        "./build.sh test again to include them."
      exit 0
    fi
    # The app's plain-Java logic first (no device needed), then the engine.
    jvm_tests || exit 1
    status=0
    with_servers bash -c 'cd engine && go vet ./... && DRIVEWAY_SERVERS=1 go test -count=1 ./...' || status=$?
    echo "Not part of this run: the kernel NFS checks (DRIVEWAY_KNFS, docs/TESTING.md) and the" \
      "on-emulator harness (tools/harness.sh)."
    exit $status ;;
  cover) need "$B/go/bin/go" || exit 1
    cover; exit ;;
  # Adds the adb-only test harness (src-debug/Harness.java). Never for a release.
  debug) DEBUG=1 ;;
  # An Android App Bundle: the same release build, packaged by bundletool.
  bundle) BUNDLE=1 ;;
esac
for f in "$C/android-31.jar" "$C/android-37.jar" "$C/r8.jar" "$B/go/bin/go" "$GOPATH/bin/gomobile" \
    "$GOPATH/bin/go-licenses" "$ANDROID_NDK_HOME" ${BUNDLE:+"$C/bundletool-$BUNDLETOOL.jar"}; do
  [[ -e $f ]] || { echo "missing $f: run ./build.sh fetch" >&2; exit 1; }
done
# aapt2, zipalign and apksigner: Debian's, else the newest SDK build-tools.
if ! command -v aapt2 >/dev/null; then
  BT=$(ls -d "$SDK"/build-tools/* 2>/dev/null | sort -V | tail -1)
  [[ -n $BT ]] || { echo "no aapt2: sudo apt install aapt zipalign apksigner, or set ANDROID_HOME" >&2; exit 1; }
  export PATH=$BT:$PATH
fi
VERSION=$(sed -n 's/.*versionName="\([^"]*\)".*/\1/p' AndroidManifest.xml)

KEYS=${DRIVEWAY_KEYS:-$HOME/.config/driveway}
KEYSTORE=${DRIVEWAY_KEYSTORE:-$KEYS/keystore.jks}
# The commit a release is built from, for the version-control record the
# Android Gradle Plugin writes into release builds of Gradle apps, so anyone
# can check out the source an APK came from. Only from a git checkout of this
# directory with nothing uncommitted, so the record never names a commit the
# build doesn't match. Debug builds carry none.
vcs_revision() {
  local top dirty
  top=$(git rev-parse --show-toplevel 2>/dev/null) && [[ $top == "$PWD" ]] || return 1
  dirty=$(git status --porcelain 2>/dev/null) && [[ -z $dirty ]] || return 1
  git rev-parse --verify HEAD 2>/dev/null
}
REVISION=
if [[ -z $DEBUG ]]; then
  REVISION=$(vcs_revision) || REVISION=
  if [[ -n $REVISION ]]; then
    echo "source commit: $REVISION (recorded in the APK)"
  elif [[ -f $KEYSTORE ]]; then
    # A release that names no commit can't be traced back to its source.
    echo "a release signed with the release key builds only from a git checkout with nothing uncommitted (see git status)" >&2
    exit 1
  else
    echo "source commit: not recorded (not a git checkout, or uncommitted changes)"
  fi
fi
OUT=build
APK=$OUT/Driveway${DEBUG:+-debug}.apk
rm -rf $OUT/gen $OUT/classes $OUT/dex $OUT/assets $OUT/engine $OUT/app.apk $OUT/aligned.apk \
  $OUT/proto.apk $OUT/base.zip $OUT/Driveway.aab
mkdir -p $OUT/gen $OUT/classes $OUT/dex $OUT/assets $OUT/engine

# The engine, for both chip families Android devices use (x86_64 and arm64). -s -w drops debug symbols (a third
# of the size); -trimpath keeps build paths out of the binary.
# go mod verify: the modules compiled in are the ones go.sum names (the
# module cache is writable here, so a stray edit would otherwise pass).
# Merriment Labs' Box registration (one-tap Box sign-in) is kept out of the
# repository: ~/.config/driveway/box-client holds BOX_CLIENT_ID= and
# BOX_CLIENT_SECRET= lines (DRIVEWAY_BOX_CLIENT points elsewhere). Without it
# Box connects only with an app's settings file.
LDFLAGS='-s -w'
BOX_FILE=${DRIVEWAY_BOX_CLIENT:-$HOME/.config/driveway/box-client}
if [[ -f $BOX_FILE ]]; then
  BOX_ID=$(sed -n 's/^BOX_CLIENT_ID=//p' "$BOX_FILE" | tr -d '[:space:]')
  BOX_SECRET=$(sed -n 's/^BOX_CLIENT_SECRET=//p' "$BOX_FILE" | tr -d '[:space:]')
  [[ $BOX_ID =~ ^[A-Za-z0-9]+$ && $BOX_SECRET =~ ^[A-Za-z0-9]+$ ]] ||
    { echo "$BOX_FILE: needs BOX_CLIENT_ID= and BOX_CLIENT_SECRET= lines (letters and digits)" >&2; exit 1; }
  ENG=github.com/jessejamesjohnston/driveway/engine
  LDFLAGS="$LDFLAGS -X $ENG.boxClientID=$BOX_ID -X $ENG.boxClientSecret=$BOX_SECRET"
  echo "Box: one-tap sign-in with Merriment Labs' registration"
else
  echo "Box: no registration ($BOX_FILE); Box connects with an app's settings file"
fi
(cd engine && go mod verify >/dev/null && go vet ./... &&
  gomobile bind -target=android/amd64,android/arm64 -androidapi 34 -javapkg com.merrimentlabs.driveway \
    -trimpath -ldflags="$LDFLAGS" -o ../$OUT/engine/engine.aar .)
(cd $OUT/engine && unzip -q engine.aar classes.jar 'jni/*')
# -trimpath keeps build paths out of the code, but Go's build record still
# names the folders that local modules were built from (the engine, and
# third_party/sddl). Blank this checkout's path there, same length, so the
# APK doesn't carry the build machine's folder names.
python3 - "$PWD" $OUT/engine/jni/*/libgojni.so <<'TRIM'
import sys
root, libs = sys.argv[1].encode(), sys.argv[2:]
# Only where the record names the engine's folder ("<checkout>/engine" and
# "<checkout>/engine/third_party/sddl"), so nothing else in the library moves.
path = root + b"/engine"
blank = b"/" + b"_" * (len(root) - 1) + b"/engine"
for lib in libs:
    data = open(lib, "rb").read()
    if path not in data:
        sys.exit(f"{lib}: the build path isn't where it was expected; check the build record")
    data = data.replace(path, blank)
    open(lib, "wb").write(data)
TRIM

# The license gate: everything compiled into the engine, for the chips we ship.
for arch in arm64 amd64; do
  (cd engine && GOOS=android GOARCH=$arch CGO_ENABLED=1 CC=true go-licenses report ./... \
    --template ../tools/licenses.tpl 2>/dev/null) > $OUT/engine/licenses-$arch.tsv || exit 1
done
sort -u $OUT/engine/licenses-*.tsv > $OUT/engine/licenses.tsv
python3 tools/licenses.py $OUT/engine/licenses.tsv engine/go.mod --version "$VERSION" \
  --json $OUT/assets/licenses.json --md THIRD_PARTY_NOTICES.md

MANIFEST=AndroidManifest.xml SRC=src
if [[ -n $DEBUG ]]; then
  MANIFEST=$OUT/AndroidManifest.xml SRC="src src-debug"
  python3 - AndroidManifest.xml $MANIFEST <<'PY'
import sys
m = open(sys.argv[1]).read()
receiver = """    <receiver android:name=".Harness" android:exported="true" android:permission="android.permission.DUMP" />
  </application>"""
assert m.count("  </application>") == 1
open(sys.argv[2], "w").write(m.replace("  </application>", receiver))
PY
  echo "debug build: includes the adb test harness" >&2
fi
aapt2 compile --dir res -o $OUT/res.zip
# Resources link against API 31 (Debian's aapt2 2.19 can't read 37's jar); the code compiles against 37.
aapt2 link -o $OUT/app.apk -I "$C/android-31.jar" --manifest $MANIFEST \
  -A $OUT/assets --java $OUT/gen $OUT/res.zip
# Zero warnings is the rule: any warning fails the build (-Werror).
javac --release 17 -Xlint:all -Werror -Xmaxwarns 50 -encoding UTF-8 \
  -cp "$C/android-37.jar:$OUT/engine/classes.jar" -d $OUT/classes $(find $SRC $OUT/gen -name '*.java')
java -cp "$C/r8.jar" com.android.tools.r8.D8 --release --min-api 34 --lib "$C/android-37.jar" \
  --output $OUT/dex $(find $OUT/classes -name '*.class') $OUT/engine/classes.jar
# The version-control record, in the shape the Android Gradle Plugin writes.
VCS_RECORD=
[[ -z $REVISION ]] || VCS_RECORD=$(printf 'repositories {\n  system: GIT\n  local_root_path: "$PROJECT_DIR"\n  revision: "%s"\n}' "$REVISION")
python3 - "$OUT" "$VCS_RECORD" <<'PY'
import sys, zipfile
out, vcs = sys.argv[1:]
# (strict_timestamps: gomobile's libraries are dated 1980-00-00)
with zipfile.ZipFile(f"{out}/app.apk", "a", zipfile.ZIP_DEFLATED, strict_timestamps=False) as apk:
    apk.write(f"{out}/dex/classes.dex", "classes.dex")
    if vcs:
        apk.writestr("META-INF/version-control-info.textproto", vcs + "\n")
    # The engine, compressed (extractNativeLibs="true" in the manifest).
    for abi in ("x86_64", "arm64-v8a"):
        apk.write(f"{out}/engine/jni/{abi}/libgojni.so", f"lib/{abi}/libgojni.so")
PY
# Exactly the two ABIs we ship (arm64-v8a and x86_64), and nothing that asks
# for more than the manifest says.
python3 - "$OUT/app.apk" <<'PY'
import sys, zipfile
libs = sorted(n for n in zipfile.ZipFile(sys.argv[1]).namelist() if n.startswith("lib/"))
if libs != ["lib/arm64-v8a/libgojni.so", "lib/x86_64/libgojni.so"]:
    sys.exit(f"unexpected native code: {libs}")
PY
PERMS=$(aapt2 dump permissions $OUT/app.apk)
for p in $(grep -o "android.permission.[A-Z_]*" <<<"$PERMS"); do
  case $p in
    android.permission.INTERNET|android.permission.ACCESS_NETWORK_STATE|android.permission.ACCESS_LOCAL_NETWORK|android.permission.POST_NOTIFICATIONS|\
    android.permission.FOREGROUND_SERVICE|android.permission.FOREGROUND_SERVICE_DATA_SYNC|\
    android.permission.MANAGE_DOCUMENTS) ;;
    *) echo "unexpected permission $p" >&2; exit 1 ;;
  esac
done
zipalign -f 4 $OUT/app.apk $OUT/aligned.apk
# Debug builds always take the dev key, so a debug build never needs the
# release password; only releases use the real key.
ALIAS=${DRIVEWAY_KEY_ALIAS:-}
if [[ -z $DEBUG && -f $KEYSTORE ]]; then
  KS=$KEYSTORE PASS=env:DRIVEWAY_KEYSTORE_PASS
  # The password is never written to disk, where any process running as
  # this user could read it: DRIVEWAY_KEYSTORE_PASS, else ask.
  if [[ -z ${DRIVEWAY_KEYSTORE_PASS:-} ]]; then
    [[ -t 0 ]] || { echo "release signing needs the Driveway keystore password: run this in a terminal, or set DRIVEWAY_KEYSTORE_PASS" >&2; exit 1; }
    read -rsp "Driveway keystore password: " DRIVEWAY_KEYSTORE_PASS; echo >&2
  fi
  export DRIVEWAY_KEYSTORE_PASS
else
  # A release-named APK with the test key couldn't update (or be updated by)
  # a release install: only debug builds fall back, unless asked for.
  if [[ -z $DEBUG && ${DRIVEWAY_TEST_KEY:-} != 1 ]]; then
    echo "no release key in $KEYS: build debug, or set DRIVEWAY_TEST_KEY=1 to sign Driveway.apk with the test key" >&2
    exit 1
  fi
  KS=$B/test-key.jks PASS=pass:test-key ALIAS=driveway
  [[ -f $KS ]] || keytool -genkeypair -keystore "$KS" -storetype PKCS12 -storepass test-key -alias driveway \
    -keyalg RSA -keysize 2048 -validity 36500 -dname "CN=Driveway test build" 2>/dev/null
  if [[ -n $DEBUG ]]; then echo "debug build: signing with the dev key $KS" >&2; else echo "no release key in $KEYS: signing with the test key $KS" >&2; fi
fi
apksigner sign --ks "$KS" --ks-pass "$PASS" ${ALIAS:+--ks-key-alias "$ALIAS"} --out $APK $OUT/aligned.apk
rm -f $APK.idsig
ls -la $APK

[[ -n $BUNDLE ]] || exit 0
# The App Bundle holds the same parts as the APK, laid out as one "base"
# module, with resources in aapt2's protobuf format instead of binary XML.
aapt2 link --proto-format -o $OUT/proto.apk -I "$C/android-31.jar" --manifest $MANIFEST \
  -A $OUT/assets $OUT/res.zip
python3 - "$OUT" "$VCS_RECORD" <<'PY'
import sys, zipfile
out, vcs = sys.argv[1:]
with zipfile.ZipFile(f"{out}/proto.apk") as src, \
     zipfile.ZipFile(f"{out}/base.zip", "w", zipfile.ZIP_DEFLATED, strict_timestamps=False) as base:
    for n in src.namelist():
        base.writestr("manifest/AndroidManifest.xml" if n == "AndroidManifest.xml" else n, src.read(n))
    base.write(f"{out}/dex/classes.dex", "dex/classes.dex")
    for abi in ("x86_64", "arm64-v8a"):
        base.write(f"{out}/engine/jni/{abi}/libgojni.so", f"lib/{abi}/libgojni.so")
    # root/ lands at the top of every APK made from the bundle.
    if vcs:
        base.writestr("root/META-INF/version-control-info.textproto", vcs + "\n")
PY
# Keep the engine compressed in the APKs made from the bundle, as in ours
# (extractNativeLibs="true"): installs stay ~50 MB instead of ~140.
echo '{"optimizations":{"uncompressNativeLibraries":{"enabled":false}}}' > $OUT/bundle-config.json
java -jar "$C/bundletool-$BUNDLETOOL.jar" build-bundle --modules=$OUT/base.zip \
  --config=$OUT/bundle-config.json --output=$OUT/Driveway.aab
# Bundles are signed with jarsigner, not apksigner.
if [[ $PASS == env:* ]]; then STOREPASS=(-storepass:env "${PASS#env:}"); else STOREPASS=(-storepass "${PASS#pass:}"); fi
jarsigner -sigalg SHA256withRSA -digestalg SHA-256 -keystore "$KS" "${STOREPASS[@]}" $OUT/Driveway.aab "${ALIAS:-driveway}" >/dev/null
java -jar "$C/bundletool-$BUNDLETOOL.jar" validate --bundle=$OUT/Driveway.aab >/dev/null
jarsigner -verify $OUT/Driveway.aab >/dev/null
ls -la $OUT/Driveway.aab

#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Build and sign a Driveway release APK from a clean checkout of a tag, then verify it.
#
#   scripts/release.sh v0.3.0            build, sign and verify
#   scripts/release.sh --verify <apk>    verify an APK only (its recorded commit is not compared)
#
# You supply the key, through the environment:
#   DRIVEWAY_KEYSTORE        the keystore file (PKCS12 or JKS). Default: ~/.config/driveway/keystore.jks.
#   DRIVEWAY_KEY_ALIAS       the key's alias. Optional when the keystore holds one key (it is used
#                            whatever its alias), or a key named driveway.
#   DRIVEWAY_KEYSTORE_PASS   the password, used for the store and the key. If unset, the script
#                            asks for it (so it needs a terminal).
#   DRIVEWAY_EXPECTED_CERT   the SHA-256 of the certificate the APK must be signed with. Default:
#                            the certificate of the published Driveway releases. Set it to your own
#                            certificate when you sign with your own key.
#
# The tag is cloned into a temporary directory and built there with ./build.sh, so uncommitted
# changes and untracked files in this checkout never reach the release, and the commit recorded
# inside the APK (META-INF/version-control-info.textproto) is the tag's. The password reaches
# build.sh only through the environment: it is never printed, never put on a command line and
# never written to disk. The toolchain comes from ./build.sh fetch, as for any build.
#
# Output in dist/: driveway-<version>.apk and SHA256SUMS. Nothing lands there unless the build
# succeeds and every check passes.
set -euo pipefail

OFFICIAL_CERT="C5:19:F7:89:47:10:A0:7D:A2:F0:18:88:7C:93:9F:A6:55:27:9F:4C:56:B2:39:F3:DB:9F:3F:D0:70:EC:07:9E"
EXPECTED_CERT="${DRIVEWAY_EXPECTED_CERT:-$OFFICIAL_CERT}"
VCS_ENTRY=META-INF/version-control-info.textproto

die() { echo "release: $*" >&2; exit 1; }

# apksigner: the one on PATH (Debian's apksigner package), else the newest SDK build-tools, the
# same order build.sh uses.
SDK="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Android/Sdk}}"
if command -v apksigner >/dev/null; then
    APKSIGNER="$(command -v apksigner)"
else
    APKSIGNER="$( (ls -d "$SDK"/build-tools/* 2>/dev/null || true) | sort -V | tail -1)/apksigner"
    [[ -x "$APKSIGNER" ]] || die "no apksigner: sudo apt install apksigner, or set ANDROID_HOME to an SDK with build-tools"
fi

cert_sha256() {
    "$APKSIGNER" verify --print-certs "$1" | sed -n 's/^.*[Ss]igner.* certificate SHA-256 digest: //p' | head -1 \
        | tr 'a-f' 'A-F' | sed 's/../&:/g; s/:$//'
}

# Every check a release APK must pass. $2, when given, is the commit the APK must record.
# Prints what it checked; returns non-zero on any failure.
verify() {
    local apk="$1" want="${2:-}" fail=0 sha dn rev libs dex found
    [[ -f "$apk" ]] || die "no APK at $apk"

    echo "== Signature"
    "$APKSIGNER" verify --verbose "$apk" | grep -E "^Verified using|^Verifies" || true
    sha=$(cert_sha256 "$apk")
    dn=$("$APKSIGNER" verify --print-certs "$apk" | sed -n 's/^.*[Ss]igner.* certificate DN: //p' | head -1)
    echo "Certificate SHA-256: $sha"
    echo "Expected:            $EXPECTED_CERT"
    [[ "$sha" == "$EXPECTED_CERT" ]] || { echo "  not the expected certificate" >&2; fail=1; }
    # build.sh's throwaway test key and Android's debug keys carry these subjects; an APK signed
    # with either must never be published.
    [[ "$dn" != *"CN=Driveway test build"* && "$dn" != *"CN=Android Debug"* ]] \
        || { echo "  signed with a test or debug key ($dn)" >&2; fail=1; }

    echo "== Source commit"
    rev=$(unzip -p "$apk" "$VCS_ENTRY" 2>/dev/null | sed -n 's/^ *revision: "\([0-9a-f]*\)"/\1/p')
    if [[ -z "$rev" ]]; then
        echo "  no $VCS_ENTRY naming a commit" >&2; fail=1
    else
        echo "Recorded: $rev"
        if [[ -n "$want" ]]; then
            echo "Tag:      $want"
            [[ "$rev" == "$want" ]] || { echo "  not the tag's commit" >&2; fail=1; }
        fi
    fi

    echo "== Contents"
    # A release never carries the adb test harness that debug builds add. The dex files are
    # extracted to a file first: piping unzip into grep -q under pipefail reads as "not found"
    # whenever grep stops early and unzip dies of SIGPIPE, so the check would pass on an APK that
    # does carry the harness.
    dex=$(mktemp)
    if ! unzip -p "$apk" 'classes*.dex' > "$dex" 2>/dev/null || [[ ! -s "$dex" ]]; then
        echo "  could not read classes.dex" >&2; fail=1
    else
        grep -aq 'com/merrimentlabs/driveway/Harness;' "$dex" && found=0 || found=$?
        case "$found" in
            0) echo "  the debug-only test harness is in classes.dex" >&2; fail=1 ;;
            1) echo "no test harness" ;;
            *) echo "  could not search classes.dex" >&2; fail=1 ;;
        esac
    fi
    rm -f "$dex"
    libs=$(unzip -Z1 "$apk" 'lib/*' 2>/dev/null | sort | paste -sd' ' -)
    echo "native code: $libs"
    [[ "$libs" == "lib/arm64-v8a/libgojni.so lib/x86_64/libgojni.so" ]] || { echo "  unexpected native code" >&2; fail=1; }

    return "$fail"
}

if [[ "${1:-}" == "--verify" ]]; then
    verify "${2:-}" || die "verification FAILED"
    echo "release: verified"
    exit 0
fi

TAG="${1:-}"
[[ -n "$TAG" ]] || { echo "usage: scripts/release.sh <tag> | --verify <apk>" >&2; exit 2; }

REPO="$(cd "$(dirname "$0")/.." && pwd)"
git -C "$REPO" rev-parse -q --verify "refs/tags/$TAG" >/dev/null || die "no tag $TAG in $REPO"
TAG_COMMIT="$(git -C "$REPO" rev-parse "$TAG^{commit}")"

STORE="${DRIVEWAY_KEYSTORE:-${DRIVEWAY_KEYS:-$HOME/.config/driveway}/keystore.jks}"
[[ -f "$STORE" ]] || die "no keystore at $STORE (set DRIVEWAY_KEYSTORE to your keystore file)"
STORE="$(cd "$(dirname "$STORE")" && pwd)/$(basename "$STORE")"

if [[ -z "${DRIVEWAY_KEYSTORE_PASS:-}" ]]; then
    [[ -t 0 ]] || die "set DRIVEWAY_KEYSTORE_PASS or run this in a terminal"
    read -rsp "Keystore password: " DRIVEWAY_KEYSTORE_PASS; echo >&2
fi
[[ -n "$DRIVEWAY_KEYSTORE_PASS" ]] || die "empty keystore password"
export DRIVEWAY_KEYSTORE_PASS
# Fail before the build if the password or alias is wrong (keytool reads the password from the environment).
KEYS="$(keytool -list -keystore "$STORE" -storepass:env DRIVEWAY_KEYSTORE_PASS 2>/dev/null)" \
    || die "the keystore or its password did not check out"
ALIASES="$(sed -n 's/^\([^,]*\), .*PrivateKeyEntry.*/\1/p' <<<"$KEYS")"
if [[ -n "${DRIVEWAY_KEY_ALIAS:-}" ]]; then
    ALIAS="$DRIVEWAY_KEY_ALIAS"
    grep -qxF "$ALIAS" <<<"$ALIASES" || die "no key named '$ALIAS' in the keystore (it holds: $(paste -sd, - <<<"$ALIASES"))"
elif [[ "$(grep -c . <<<"$ALIASES")" == 1 ]]; then
    ALIAS="$ALIASES"
elif grep -qxF driveway <<<"$ALIASES"; then
    ALIAS=driveway
else
    die "the keystore holds several keys ($(paste -sd, - <<<"$ALIASES")); set DRIVEWAY_KEY_ALIAS to one"
fi
echo "release: signing with key '$ALIAS'"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
git clone --quiet --no-local --depth 1 --branch "$TAG" "file://$REPO" "$WORK/src"
cd "$WORK/src"

VERSION="$(sed -n 's/.*android:versionName="\([^"]*\)".*/\1/p' AndroidManifest.xml)"
[[ "$TAG" == "v$VERSION" ]] || die "tag $TAG does not match versionName $VERSION in AndroidManifest.xml (expected v$VERSION)"

echo "release: building $TAG ($(git rev-parse --short HEAD))"
DRIVEWAY_KEYSTORE="$STORE" DRIVEWAY_KEY_ALIAS="$ALIAS" DRIVEWAY_TEST_KEY= ./build.sh
unset DRIVEWAY_KEYSTORE_PASS

BUILT_APK=build/Driveway.apk
verify "$BUILT_APK" "$TAG_COMMIT" || die "verification FAILED; dist/ left as it was"

mkdir -p "$REPO/dist"
APK="driveway-$VERSION.apk"
cp "$BUILT_APK" "$REPO/dist/$APK"
cd "$REPO/dist"
if command -v sha256sum >/dev/null; then sha256sum "$APK" > SHA256SUMS; else shasum -a 256 "$APK" > SHA256SUMS; fi
cat SHA256SUMS
ls -l "$APK" | awk '{printf "%s  %.1f MB\n", $NF, $5/1048576}'
echo "release: wrote dist/$APK and dist/SHA256SUMS"
echo "release: certificate SHA-256: $EXPECTED_CERT"

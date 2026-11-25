#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Walks every Driveway screen on an emulator with made-up servers (backed by
# the Docker test servers), runs tools/a11y-check.py on each, and saves a
# screenshot of each into OUT. Needs the debug build installed.
#
#   ANDROID_SERIAL=emulator-5554 tools/screens.sh OUT
#
# Prints "name: N problems" per screen; exit status 1 if any screen has one.
# Run it again at 200% text (adb shell settings put system font_scale 2.0)
# and in the dark theme (adb shell cmd uimode night yes). A check sees only
# what is on screen: for the ends of long pages, scroll and run
# tools/a11y-check.py again (docs/LARGE-SCREEN-A11Y.md, "How it is checked").
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")/.."

# A vertical swipe in the middle of the screen, from FROM% to TO% of its
# height (the walk runs at phone, tablet and 1920 x 1080 sizes; these were
# phone pixels, 540 1800 -> 540 500 on 1080 x 2400).
read -r SW SH < <(adb shell wm size | awk -F'[ x]' '/size/ {w=$(NF-1); h=$NF} END {print w, h}')
swipe() { adb shell input swipe $((SW / 2)) $((SH * $1 / 100)) $((SW / 2)) $((SH * $2 / 100)) "$3"; }
[[ -n ${ANDROID_SERIAL:-} ]] || { echo "set ANDROID_SERIAL to the emulator (never a phone in use)" >&2; exit 2; }
[[ $ANDROID_SERIAL == emulator-* ]] || { echo "ANDROID_SERIAL must be an emulator" >&2; exit 2; }
OUT=${1:?OUT}
mkdir -p "$OUT"
H=${HARNESS_HOST:-10.0.2.2}
PKG=com.merrimentlabs.driveway
RX=$PKG/.Harness
fail=0

act() { # action id [key value]... ; waits for the harness's PASS/FAIL
  local action=$1 id=$2; shift 2
  local args=(--user current -n $RX -a "$action" --es id "$id")
  while (($#)); do args+=(--es "$1" "'$2'"); shift 2; done
  adb logcat -c
  adb shell am broadcast "${args[@]}" >/dev/null
  for i in $(seq 1 240); do
    # A broadcast lost while the app restarts: send it once more.
    [[ $i == 40 ]] && adb shell am broadcast "${args[@]}" >/dev/null
    local out
    out=$(adb logcat -d -s Driveway:V | grep -E "HARNESS (PASS|FAIL|ADDED|REMOVED) $id( |:|$)" || true)
    if [[ -n $out ]]; then
      [[ $out == *FAIL* ]] && { echo "  $action $id: ${out#*: }" >&2; return 1; }
      return 0
    fi
    sleep 0.5
  done
  echo "  $action $id: no answer" >&2; return 1
}

check() { # name: a11y check and screenshot of what is on screen now
  sleep "${SETTLE:-1.5}"
  local r
  r=$(python3 tools/a11y-check.py 2>&1) || true
  echo "$1: ${r##*$'\n'}"
  [[ $r == *", 0 problems"* ]] || { echo "$r" | sed '$d' | sed 's/^/    /'; fail=1; }
  python3 tools/ui.py shot "$OUT/$1.png"
}

adb shell pm grant $PKG android.permission.ACCESS_LOCAL_NETWORK 2>/dev/null || true
adb shell pm grant $PKG android.permission.POST_NOTIFICATIONS 2>/dev/null || true
adb shell am start -W --user current -n $PKG/.MainActivity >/dev/null

# A clean slate, then made-up servers on the test servers.
act clear x
act found x list none
act open x screen home
check home-empty

key=$(ssh-keyscan -p 2222 127.0.0.1 2>/dev/null | sed "s/^\[127.0.0.1\]/[$H]/" | head -1)
act add s-home label "Home server" backend sftp folder share \
  params "{\"host\":\"$H\",\"port\":\"2222\",\"user\":\"driveway\",\"pass\":\"driveway-test\"}" hostkey "$key"
act add s-photos label "Family photos" backend smb folder share \
  params "{\"host\":\"$H\",\"port\":\"1445\",\"user\":\"driveway\",\"pass\":\"driveway-test\"}"
act add s-media label Media backend nfs params "{\"host\":\"$H\",\"port\":\"2050\",\"export\":\"/\"}"
act add s-archive label "Photo archive" backend sftp \
  params "{\"host\":\"$H\",\"port\":\"2299\",\"user\":\"sam\",\"pass\":\"x\"}"
act add s-drop label Dropbox backend dropbox \
  params '{"token":"{\"access_token\":\"made-up\",\"token_type\":\"bearer\",\"expiry\":\"2026-01-01T00:00:00Z\"}"}'
# Servers of services taken out of Driveway (ShareFile, crypt): each card
# says so, with Remove.
act add s-gone label "Client files" backend sharefile params '{}'
act add s-vault label Vault backend crypt params '{"remote":"s-home:vault"}'
# A server of a service that is no longer listed (pCloud):
# its card and Change still work.
act add s-pcloud label pCloud backend pcloud \
  params '{"token":"{\"access_token\":\"made-up\",\"token_type\":\"bearer\",\"expiry\":\"2026-01-01T00:00:00Z\"}"}'
# Google Drive signed in through the browser before Driveway stopped
# offering that: it keeps its token and is changed (to a key), never signed
# in again.
act add s-gdrive label "Team Drive" backend drive \
  params '{"token":"{\"access_token\":\"made-up\",\"token_type\":\"bearer\",\"expiry\":\"2026-01-01T00:00:00Z\"}"}'
# An SFTP server whose identity isn't checked yet: Test connection waits at
# the question, with the form locked meanwhile.
act add s-office label "Office Mac" backend sftp folder share \
  params "{\"host\":\"$H\",\"port\":\"2222\",\"user\":\"driveway\",\"pass\":\"driveway-test\"}"
act failsave s-archive name vacation-042.jpg
act problem s-drop code signin
act problem s-pcloud code signin
act found x list '[{"name":"DiskStation","backend":"smb","host":"192.168.1.20"},{"name":"Studio iMac","backend":"smb","host":"192.168.1.31"}]'

act open x screen home
check home
# The end of the list (the removed service's card, the sign-in problems:
# Sign in again on Dropbox; on pCloud, with no other way in, a note and Remove).
for _ in 1 2 3; do swipe 75 21 300; done
check home-end
# pCloud's card is further down at large text.
for _ in 1 2 3 4 5 6; do python3 tools/ui.py has "can't sign in to it again" && break
  swipe 75 38 300; sleep 0.5; done
python3 tools/ui.py has "can't sign in to it again" || { echo "home-end: pCloud's sign-in problem doesn't say it can't sign in again"; fail=1; }
python3 tools/ui.py has "Remove pCloud" || { echo "home-end: pCloud's sign-in problem offers no Remove"; fail=1; }
python3 tools/ui.py has "Change pCloud" && { echo "home-end: pCloud offers a Change that can't bring it back"; fail=1; }
python3 tools/ui.py has "Sign in again to pCloud" && { echo "home-end: pCloud offers Sign in again"; fail=1; }
act newlabel x backend none
act open x screen add
check where
python3 tools/ui.py tap "More services"
check more-services
# Hidden services aren't listed, even when searched for.
python3 tools/ui.py tap "Search services"
for name in pCloud HiDrive Yandex; do
  adb shell input text "$name"
  sleep 1
  if python3 tools/ui.py has "$name," || ! python3 tools/ui.py has "No service matches"; then
    echo "more-services: $name is listed"; fail=1
  fi
  for _ in $(seq ${#name}); do adb shell input keyevent DEL; done
done
adb shell input keyevent ESCAPE; adb shell input keyevent ESCAPE

# The New label: on a service an update made available, until it has been
# seen on two visits to Add a server (Box stands in for one here).
act newlabel x backend box is box expect true
for visit in 1 2 3; do
  act open x screen add
  python3 tools/ui.py tap "More services"
  if ((visit < 3)); then
    python3 tools/ui.py has "Box, new" || { echo "new-label: visit $visit has no New label"; fail=1; }
    ((visit == 1)) && check more-services-new
  else
    python3 tools/ui.py has "Box, new" && { echo "new-label: still New on visit 3"; fail=1; }
  fi
  adb shell input keyevent ESCAPE; adb shell input keyevent ESCAPE
done
act newlabel x is box expect false
# A row further down counts only once it has been on screen: two visits
# without scrolling to Storj leave it new; two that scroll to it don't.
act newlabel x backend storj
for visit in 1 2 3 4; do
  act open x screen add
  python3 tools/ui.py tap "More services"
  if ((visit > 2)); then
    for _ in 1 2 3 4 5 6 7 8; do swipe 75 21 300; done
    sleep 1
    python3 tools/ui.py has "Storj, new" || { echo "new-label: Storj not New on visit $visit"; fail=1; }
  fi
  adb shell input keyevent ESCAPE; adb shell input keyevent ESCAPE
  ((visit == 2)) && { act newlabel x is storj expect true || { echo "new-label: Storj counted while off screen"; fail=1; }; }
done
act newlabel x is storj expect false || { echo "new-label: Storj still New after two visits that showed it"; fail=1; }

# A new SFTP server through the whole path: details, identity, finish.
act open x screen form backend sftp label "Office Mac" host "$H" port 2222
check form-sftp
python3 tools/ui.py type "User name" driveway
python3 tools/ui.py type "Password" driveway-test
python3 tools/ui.py tap Connect
python3 tools/ui.py wait "Is this" 30
check connect-identity
python3 tools/ui.py tap "Trust and continue"
python3 tools/ui.py wait "is in Files" 40
check done
python3 tools/ui.py tap Done

# A failure stops at its step with the fix in place.
act open x screen form backend smb label "Old NAS" host "$H" port 1445
python3 tools/ui.py type "User name" driveway
python3 tools/ui.py type "Password" wrong
python3 tools/ui.py tap Connect
python3 tools/ui.py wait "Change sign-in" 40
check connect-failed
adb shell input keyevent ESCAPE; adb shell input keyevent ESCAPE
python3 tools/ui.py has "Discard" && python3 tools/ui.py tap "Discard" || true

# The service forms: the name in Files and what the service needs, one
# Connect to <service>, the rest under More settings. Dropbox and OneDrive
# sign in through the browser; Google Drive connects with a service account
# key; S3 is a form, connected for real (the test
# server) with nothing but its essentials.
for svc in dropbox onedrive drive; do
  act open x screen form backend $svc
  check form-$svc
  adb shell input keyevent ESCAPE
done
act open x screen form backend onedrive
python3 tools/ui.py tap "More settings"
check form-onedrive-more
adb shell input keyevent ESCAPE
act open x screen form backend s3
check form-s3
python3 tools/ui.py type "Access key ID" driveway
python3 tools/ui.py type "Secret access key" driveway-test
python3 tools/ui.py type "Endpoint" "http://$H:9000"
python3 tools/ui.py tap "Connect to S3 storage"
python3 tools/ui.py wait "is in Files" 40
check done-s3
python3 tools/ui.py tap Done

# Changing a server: the form, and Test connection's checklist under it.
act open x screen edit server s-photos
check edit
python3 tools/ui.py tap "Test connection"
python3 tools/ui.py wait "Opened share" 40
check edit-tested
adb shell input keyevent ESCAPE

# While Test connection runs, the form is locked: the question, then the
# fields (disabled), then the identity's Cancel unlocks them.
act open x screen edit server s-office
python3 tools/ui.py tap "Test connection"
python3 tools/ui.py wait "Is this" 30
check edit-running
for _ in 1 2 3; do swipe 25 79 200; done
check edit-running-form
if adb exec-out uiautomator dump /dev/tty | grep -qE 'class="android.widget.(EditText|Spinner)"[^>]*enabled="true"'; then
  echo "edit-running-form: a field can be changed while the run uses the form"; fail=1
fi
python3 tools/ui.py tap "Cancel, don't trust"
python3 tools/ui.py wait "Not trusted" 20
if adb exec-out uiautomator dump /dev/tty | grep -qE 'class="android.widget.(EditText|Spinner)"[^>]*enabled="false"'; then
  echo "edit-running-form: a field stayed locked after the run"; fail=1
fi
adb shell input keyevent ESCAPE

# A server of a hidden service (pCloud) opens for changes; neither it nor
# Google Drive signed in before offers Sign in again.
act open x screen edit server s-pcloud
check edit-hidden-service
python3 tools/ui.py has "Sign in again" && { echo "edit-hidden-service: Sign in again offered"; fail=1; }
adb shell input keyevent ESCAPE
act open x screen edit server s-gdrive
check edit-token-only
python3 tools/ui.py has "Sign in again" && { echo "edit-token-only: Sign in again offered"; fail=1; }
adb shell input keyevent ESCAPE

# Dropbox signed in before Driveway had its own registration: the note.
act problem s-drop code none
act open x screen edit server s-drop
check edit-old-signin
python3 tools/ui.py has "Sign in again" || { echo "edit-old-signin: no Sign in again"; fail=1; }
adb shell input keyevent ESCAPE

act open x screen settings
check settings
act open x screen licenses
check licenses
act open x screen license item 0
check license-text
adb shell input keyevent ESCAPE; adb shell input keyevent ESCAPE; adb shell input keyevent ESCAPE

act open x screen home
exit $fail

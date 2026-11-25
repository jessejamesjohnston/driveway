#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Checks where each Allow button for a permission leads, on an Android 17
# emulator (it has the Nearby devices permission): Android's own dialog while
# it still asks (never asked, or refused once), Driveway's App info page once
# it was refused for good, and never App info straight after a "Don't allow".
# Reinstalls nothing but clears Driveway's data and permissions first, so
# never on a phone in use. Needs the debug build installed.
#
#   ANDROID_SERIAL=emulator-5556 tools/permissions.sh
#
# Prints PASS/FAIL per step; exit status 1 on any FAIL.
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")/.."
[[ -n ${ANDROID_SERIAL:-} ]] || { echo "set ANDROID_SERIAL to the emulator (never a phone in use)" >&2; exit 2; }
[[ $ANDROID_SERIAL == emulator-* ]] || { echo "ANDROID_SERIAL must be an emulator" >&2; exit 2; }
PKG=com.merrimentlabs.driveway
NEARBY=android.permission.ACCESS_LOCAL_NETWORK
NOTIFY=android.permission.POST_NOTIFICATIONS
(( $(adb shell getprop ro.build.version.sdk | tr -d '\r') >= 37 )) || { echo "needs Android 17 (API 37), for $NEARBY" >&2; exit 2; }
fail=0

top() { adb shell dumpsys activity activities | grep -m1 topResumedActivity | grep -o '[A-Za-z0-9_.]*/[A-Za-z0-9_.$]*' | head -1; }
expect() { # what: dialog | appinfo | driveway
  sleep 2
  local t want; t=$(top)
  case $2 in
    dialog) want=GrantPermissionsActivity ;;
    appinfo) want=com.android.settings/ ;;
    driveway) want=$PKG/ ;;
  esac
  if [[ $t == *"$want"* ]]; then echo "PASS $1"; else echo "FAIL $1: $t"; fail=1; fi
}
refuse() { python3 tools/ui.py tap "Don’t allow" 2>/dev/null || python3 tools/ui.py tap "Don't allow"; }
fresh() { # Driveway as just installed: no data, nothing granted or refused
  adb shell pm clear $PKG >/dev/null
  for p in $NEARBY $NOTIFY; do
    adb shell pm revoke $PKG $p 2>/dev/null || true
    adb shell pm clear-permission-flags $PKG $p user-set user-fixed 2>/dev/null || true
  done
}
open() { # screen [key value]...: through the debug harness, Driveway in front
  WAIT=4 tools/act.sh open x screen "$@" >/dev/null 2>&1 || true
  sleep 1.5
}

# Settings, Nearby devices: never asked, refused once, refused for good.
fresh
open settings
python3 tools/ui.py tap "Allow Nearby devices"; expect "settings: never asked opens Android's dialog" dialog
refuse; expect "settings: a refusal stays on Driveway" driveway
python3 tools/ui.py tap "Allow Nearby devices"; expect "settings: refused once, Android's dialog again" dialog
refuse; expect "settings: the second refusal stays on Driveway (no App info)" driveway
python3 tools/ui.py tap "Allow Nearby devices"; expect "settings: refused for good opens App info" appinfo
# Allowed there, back to Driveway: the row says so.
adb shell pm grant $PKG $NEARBY
adb shell input keyevent BACK; sleep 1.5
if python3 tools/ui.py has "Allow Nearby devices"; then echo "FAIL settings: still offers Allow after it was allowed"; fail=1
else echo "PASS settings: allowed in App info, the row updates"; fi

# Settings, notifications: never asked opens Android's dialog.
python3 tools/ui.py tap "Allow notifications"; expect "settings: notifications, never asked opens Android's dialog" dialog
refuse

# Home, the Nearby devices banner (shown once there is a server to reach).
fresh
WAIT=4 tools/act.sh add p-x label "Test server" backend sftp params '{"host":"10.0.2.2","port":"2222"}' >/dev/null 2>&1 || true
open home
python3 tools/ui.py tap "Allow Nearby devices"; expect "home banner: never asked opens Android's dialog" dialog
refuse
python3 tools/ui.py tap "Allow Nearby devices"; expect "home banner: refused once, Android's dialog again" dialog
refuse
python3 tools/ui.py tap "Allow Nearby devices"; expect "home banner: refused for good opens App info" appinfo
adb shell input keyevent BACK; sleep 1

# Add a server, Look for servers: never asked opens Android's dialog.
fresh
open add
python3 tools/ui.py tap "Look for servers"; expect "add: Look for servers, never asked opens Android's dialog" dialog
refuse

fresh
exit $fail

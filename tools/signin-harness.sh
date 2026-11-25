#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Driveway's own sign-in (Dropbox and OneDrive, PKCE with no secret) on an
# emulator, end to end, against the stand-in provider: the real browser
# opens the stand-in's page, which sends it back to the app at
# http://localhost:53682/ like Dropbox and Microsoft do. Needs a debug build
# installed and ANDROID_SERIAL set to the emulator (never a phone).
#
#   ANDROID_SERIAL=emulator-5560 tools/signin-harness.sh
#
# Checks: a Dropbox and a OneDrive sign-in with Merriment Labs' client IDs
# and a complete token (the stand-in refuses a secret and checks the
# verifier); rclone's refresh of each, kept by the app; and the ways a
# sign-in ends: declined, a refused code, Cancel while the page is open or
# after another sign-in's answer was turned away (then a sign-in straight
# after); and a sign-in started with Driveway in the background, which ends
# at once (Android wouldn't open the page) instead of waiting ten minutes;
# and another app holding [::1]:53682, which both sign-ins refuse.
# Prints PASS/FAIL per check; exit 1 on any FAIL.
set -uo pipefail
cd "$(dirname "$(readlink -f "$0")")/.."
[[ ${ANDROID_SERIAL:-} == emulator-* ]] || { echo "set ANDROID_SERIAL to an emulator" >&2; exit 2; }
PORT=8765
LOG=${SIGNIN_LOG:-/tmp/fake-oauth-$PORT.log}
# Box signs in with one tap only in a build with Merriment Labs' Box
# registration (build.sh reads its file); its ID is public, its secret
# stays in that file (the stand-in only checks that one is sent).
BOX=$(sed -n 's/^BOX_CLIENT_ID=//p' "${DRIVEWAY_BOX_CLIENT:-$HOME/.config/driveway/box-client}" 2>/dev/null | tr -d '[:space:]' || true)
python3 test/servers/fake-oauth.py 127.0.0.1 $PORT --expires 1 ${BOX:+--confidential "$BOX"} >"$LOG" 2>&1 &
FAKE=$!
# The [::1] hold below goes with the script, however it ends: left behind,
# every later sign-in on the emulator would refuse.
trap 'kill $FAKE 2>/dev/null; adb reverse --remove tcp:$PORT >/dev/null 2>&1; adb shell "pkill -f '"'"'nc -6 -s ::1 -p 53682'"'"'" >/dev/null 2>&1' EXIT
adb reverse tcp:$PORT tcp:$PORT >/dev/null
sleep 1

DROPBOX=nnd1cgs8tlnkafa
ONEDRIVE=16cf5e4e-49d2-4264-a4c5-40674afe4235
# localhost for the page (Chrome's HTTPS-first mode exempts it), 127.0.0.1
# for the token endpoint (the engine's own HTTP).
p() { echo "{\"auth_url\":\"http://localhost:$PORT/$1\",\"token_url\":\"http://127.0.0.1:$PORT/token\"${2:-}}"; }
fail=0
check() { # name, then tools/act.sh arguments
  local name=$1; shift
  if out=$(WAIT=120 tools/act.sh "$@"); then echo "PASS $name"; else echo "FAIL $name"; fail=1; fi
  sed 's/^/    /' <<<"$out"
}

check "Dropbox signs in" signin s-dropbox label Dropbox backend dropbox params "$(p authorize)" client $DROPBOX
check "Dropbox refresh kept" refresh s-dropbox
# OneDrive: Driveway sets the account's own drive from the stand-in Graph,
# with no question (no "type of connection", no drive IDs, no "Drive OK?");
# without /me/drive it takes the one real drive from the list (system
# drives and the Bundles_ store don't count).
check "OneDrive signs in, own drive, no questions" signin s-onedrive label OneDrive backend onedrive \
  params "$(p authorize ',"graph_url":"http://127.0.0.1:'$PORT'/graph"')" \
  client $ONEDRIVE drive 0123456789ABCDEF noquestion 1
check "OneDrive refresh kept" refresh s-onedrive
check "OneDrive without /me/drive: the real drive" signin s-onedrive2 label OneDrive backend onedrive \
  params "$(p authorize ',"graph_url":"http://127.0.0.1:'$PORT'/graph-nome"')" \
  client $ONEDRIVE drive 0123456789ABCDEF noquestion 1
if [[ -n $BOX ]]; then
  check "Box signs in (its secret sent, never kept by the app)" signin s-box label Box backend box \
    params "$(p authorize)" client "$BOX" noquestion 1
  check "Box refresh kept" refresh s-box
else
  echo "SKIP Box: this build has no Box registration"
fi
check "declined" signin s-end label Dropbox backend dropbox params "$(p deny)" expect declined
check "refused code" signin s-end label Dropbox backend dropbox params "$(p badcode)" expect signin
# Pages that leave the sign-in waiting, ended with Cancel sign-in: one that
# never sends the browser back, and an answer from another sign-in (turned
# away in the browser; the sign-in keeps waiting for its own).
T=$(mktemp -d)
for page in hang stale; do
  (WAIT=120 tools/act.sh signin s-$page label Dropbox backend dropbox params "$(p $page)" expect cancelled >"$T/$page.out" 2>&1
   echo $? >"$T/$page.rc") &
  WAITING=$!
  sleep 8
  adb shell am broadcast --user current -n com.merrimentlabs.driveway/.Harness -a cancelsignin --es id s-cancelbtn >/dev/null
  wait $WAITING
  if [[ $(cat "$T/$page.rc") == 0 ]]; then echo "PASS $page: waits until cancelled"; else echo "FAIL $page: waits until cancelled"; fail=1; fi
  sed 's/^/    /' "$T/$page.out"
done
check "sign in right after a cancel" signin s-again label Dropbox backend dropbox params "$(p authorize)" client $DROPBOX
# Driveway in the background: the page can't open, so the sign-in ends at
# once, saying why (not after the ten-minute limit).
adb shell input keyevent HOME
sleep 2
start=$(date +%s)
if out=$(FRONT=0 WAIT=60 tools/act.sh signin s-bg label Dropbox backend dropbox params "$(p authorize)" expect background); then
  took=$(($(date +%s) - start))
  if ((took <= 20)); then echo "PASS background: ends at once (${took}s)"; else echo "FAIL background: took ${took}s"; fail=1; fi
else
  echo "FAIL background: ends at once"; fail=1
fi
sed 's/^/    /' <<<"$out"
# Another app holding [::1]:53682, where a browser may deliver the answer
# for localhost: Driveway's own sign-in and rclone's (someone else's client
# ID) both refuse at once, saying why, and open no page.
adb shell 'toybox nc -6 -s ::1 -p 53682 -L cat' >/dev/null 2>&1 &
HOLD=$!
sleep 2
check "[::1] taken: own sign-in refuses, says why" signin s-port label Dropbox backend dropbox params "$(p authorize)" expect port
check "[::1] taken: rclone's sign-in refuses, says why" signin s-port label Dropbox backend dropbox \
  params "$(p hang ',"client_id":"someone-else","client_secret":"s"')" expect port
adb shell "pkill -f 'nc -6 -s ::1 -p 53682'" >/dev/null 2>&1
kill $HOLD 2>/dev/null
wait $HOLD 2>/dev/null
for id in s-dropbox s-onedrive s-onedrive2 s-box s-again; do
  adb shell am broadcast --user current -n com.merrimentlabs.driveway/.Harness -a remove --es id $id >/dev/null
done
echo "stand-in provider saw:"
sed 's/^/    /' "$LOG"
exit $fail

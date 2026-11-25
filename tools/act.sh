#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# One harness action on the connected device, waiting for its result:
#   tools/act.sh ACTION ID [key value]...   e.g. tools/act.sh bigio t-smb mb 100
# Prints the action's HARNESS lines; exit 1 on FAIL. Keeps Driveway in front
# (Android cuts the network of background apps); FRONT=0 leaves the screen as
# it is (a sign-in started with Driveway in the background).
set -euo pipefail
action=$1 id=$2; shift 2
args=(--user current -n com.merrimentlabs.driveway/.Harness -a "$action" --es id "$id")
while (($#)); do args+=(--es "$1" "'$2'"); shift 2; done
[[ ${FRONT:-1} == 0 ]] || adb shell am start -W --user current -n com.merrimentlabs.driveway/.MainActivity >/dev/null
adb logcat -c
adb shell am broadcast "${args[@]}" >/dev/null
for _ in $(seq 1 ${WAIT:-600}); do
  out=$(adb logcat -d -s Driveway:V | grep -E "HARNESS [A-Z]+ $id( |:|$)" || true)
  if grep -q -E "HARNESS (PASS|FAIL) $id" <<<"$out"; then
    grep -v -E 'HARNESS (ADDED|REMOVED)' <<<"$out" | sed 's/^.*HARNESS /  /'
    grep -q "HARNESS FAIL" <<<"$out" && exit 1 || exit 0
  fi
  sleep 1
done
echo "  no result for $action $id"; exit 1

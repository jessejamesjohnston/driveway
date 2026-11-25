#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Drives a debug build's test harness (src-debug/Harness.java) on an emulator
# against the Docker servers in test/servers, where the emulator reaches the
# host at 10.0.2.2. Prints PASS/FAIL per server; exit status 1 on any FAIL.
#
#   tools/harness.sh            add all four servers and run every check
#   tools/harness.sh sftp smb   just these
set -euo pipefail
cd "$(dirname "$(readlink -f "$0")")/.."
H=${HARNESS_HOST:-10.0.2.2}
RX=com.merrimentlabs.driveway/.Harness

bcast() { # action id [--es key value]...
  local action=$1 id=$2; shift 2
  local args=(--user current -n $RX -a "$action" --es id "$id")
  while (($#)); do args+=("$1" "$2" "'$3'"); shift 3; done
  adb shell am broadcast "${args[@]}" >/dev/null
}

wait_result() { # id -> prints the PASS/FAIL line
  local id=$1 line
  for _ in $(seq 1 240); do
    line=$(adb logcat -d -s Driveway:V | grep -E "HARNESS (PASS|FAIL) $id( |:|$)" | tail -1 || true)
    [[ -n $line ]] && { echo "${line#*: }"; return; }
    sleep 0.5
  done
  echo "HARNESS FAIL $id: no result"
}

wait_added() { # id -> success once the add is acknowledged; failure on FAIL or after 30 s
  local line
  for _ in $(seq 1 60); do
    line=$(adb logcat -d -s Driveway:V | grep -E "HARNESS (ADDED|FAIL) $1( |:|$)" | tail -1 || true)
    [[ $line == *"HARNESS ADDED"* ]] && return 0
    [[ $line == *"HARNESS FAIL"* ]] && return 1
    sleep 0.5
  done
  return 1
}

params() {
  case $1 in
    sftp)   echo "{\"host\":\"$H\",\"port\":\"2222\",\"user\":\"driveway\",\"pass\":\"driveway-test\"}" ;;
    smb)    echo "{\"host\":\"$H\",\"port\":\"1445\",\"user\":\"driveway\",\"pass\":\"driveway-test\"}" ;;
    webdav) echo "{\"url\":\"http://$H:8080\",\"vendor\":\"other\",\"user\":\"driveway\",\"pass\":\"driveway-test\"}" ;;
    nfs)    echo "{\"host\":\"$H\",\"port\":\"2050\",\"export\":\"/\"}" ;;
  esac
}
folder() { case $1 in sftp|smb) echo share ;; *) echo "" ;; esac; }

kinds=("$@")
((${#kinds[@]})) || kinds=(sftp smb webdav nfs)
adb logcat -c
# Android 15+ cuts an app's network while it's in the background, and a
# broadcast alone doesn't bring Driveway forward: keep its window in front.
adb shell am start -W --user current -n com.merrimentlabs.driveway/.MainActivity >/dev/null
fail=0
for k in "${kinds[@]}"; do
  extra=()
  if [[ $k == sftp ]]; then
    # Every key type the server has: the engine pins whichever it negotiates.
    key=$(ssh-keyscan -p 2222 127.0.0.1 2>/dev/null | sed "s/^\[127.0.0.1\]/[$H]/")
    extra=(--es hostkey "$key")
  fi
  bcast add "t-$k" --es label "Test $k" --es backend "$k" --es params "$(params "$k")" --es folder "$(folder "$k")" "${extra[@]}"
  # The add runs on its own thread: wait for it before running the checks,
  # and run them only on the server just added.
  if ! wait_added "t-$k"; then echo "$k: HARNESS FAIL t-$k: not added"; fail=1; continue; fi
  bcast run "t-$k"
  r=$(wait_result "t-$k"); echo "$k: $r"; [[ $r == *PASS* ]] || fail=1
done
# A server that's down: the listing says so.
bcast add t-down --es label Down --es backend sftp --es params "{\"host\":\"$H\",\"port\":\"2299\",\"user\":\"x\",\"pass\":\"x\"}"
wait_added t-down || { echo "unreachable: HARNESS FAIL t-down: not added"; exit 1; }
bcast unreachable t-down
r=$(wait_result t-down); echo "unreachable: $r"; [[ $r == *PASS* ]] || fail=1
adb logcat -c
bcast failedsaves t-down
r=$(wait_result t-down); echo "failed saves: $r"; [[ $r == *PASS* ]] || fail=1
bcast remove t-down
# The connection checklist (what Add a server shows) stops at the right step.
checklist() { # name expect backend params [folder]
  adb logcat -c
  bcast connect "c-$1" --es backend "$3" --es params "$4" --es folder "${5:-}" --es expect "$2"
  r=$(wait_result "c-$1"); echo "checklist $1 (expect $2): $r"; [[ $r == *PASS* ]] || fail=1
}
checklist sftp ok sftp "{\"host\":\"$H\",\"port\":\"2222\",\"user\":\"driveway\",\"pass\":\"driveway-test\"}" share
checklist sftp-password signin sftp "{\"host\":\"$H\",\"port\":\"2222\",\"user\":\"driveway\",\"pass\":\"wrong\"}"
checklist smb-down found smb "{\"host\":\"$H\",\"port\":\"1999\",\"user\":\"driveway\",\"pass\":\"x\"}"
checklist smb-password signin smb "{\"host\":\"$H\",\"port\":\"1445\",\"user\":\"driveway\",\"pass\":\"wrong\"}"
checklist webdav-folder folder webdav "{\"url\":\"http://$H:8080\",\"vendor\":\"other\",\"user\":\"driveway\",\"pass\":\"driveway-test\"}" no-such-folder
checklist nfs ok nfs "{\"host\":\"$H\",\"port\":\"2050\",\"export\":\"/\"}"
# Services from rclone's list, with nothing but their forms' essentials.
checklist s3 ok s3 "{\"provider\":\"AWS\",\"access_key_id\":\"driveway\",\"secret_access_key\":\"driveway-test\",\"endpoint\":\"http://$H:9000\"}"
checklist s3-key signin s3 "{\"provider\":\"AWS\",\"access_key_id\":\"driveway\",\"secret_access_key\":\"wrong\",\"endpoint\":\"http://$H:9000\"}"
checklist http ok http "{\"url\":\"http://driveway:driveway-test@$H:8080/\"}"
# A move Files makes by copying keeps its original through Driveway being
# stopped: set up, stop Driveway, then the provider's first call (no window
# yet, so nothing else has started) must refuse the delete; with the window
# back, the copy uploads and the delete goes through.
if [[ " ${kinds[*]} " == *" webdav "* ]]; then
  for phase in 1 2 3; do
    stopped=()
    case $phase in
      2) adb shell am force-stop com.merrimentlabs.driveway; stopped=(--include-stopped-packages) ;;
      3) adb shell am start -W --user current -n com.merrimentlabs.driveway/.MainActivity >/dev/null ;;
    esac
    adb logcat -c
    adb shell am broadcast --user current "${stopped[@]}" -n $RX -a movekill --es id t-webdav --es phase $phase \
      --es host "$H" >/dev/null
    r=$(wait_result t-webdav); echo "move through a restart, step $phase: $r"; [[ $r == *PASS* ]] || fail=1
  done
fi
exit $fail

#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Drives Driveway's screens on an emulator for checks and screenshots.

  tools/ui.py tap LABEL      tap the control whose text or description starts with LABEL
  tools/ui.py type LABEL T   type T into the field labeled LABEL (the label just above it), replacing its text
  tools/ui.py wait TEXT [S]  wait up to S seconds (default 30) until text containing TEXT is on screen
  tools/ui.py has TEXT       exit 0 if TEXT is on screen now
  tools/ui.py shot FILE      save a screenshot (PNG)

Uses `adb`: set ANDROID_SERIAL to the emulator's serial when more than one
device is attached (never a phone in use).
"""
import re
import subprocess
import sys
import time
import xml.etree.ElementTree as ET


def dump():
    for _ in range(5):
        out = subprocess.run(["adb", "exec-out", "uiautomator", "dump", "/dev/tty"], capture_output=True, text=True).stdout
        a, b = out.find("<?xml"), out.rfind("</hierarchy>")
        if a >= 0 and b > a:
            return ET.fromstring(out[a:b + len("</hierarchy>")])
    sys.exit("uiautomator dump failed")


def find(root, label, clickable, anywhere=False):
    for n in root.iter("node"):
        names = (n.get("text") or "", n.get("content-desc") or "")
        hit = any((label in x) if anywhere else x.startswith(label) for x in names if x)
        if hit and (not clickable or n.get("clickable") == "true"):
            return n
    return None


def hide_keyboard():
    """BACK closes the on-screen keyboard, only when it is up (BACK would otherwise leave the screen)."""
    out = subprocess.run(["adb", "shell", "dumpsys", "input_method"], capture_output=True, text=True).stdout
    if "mInputShown=true" in out or "isInputViewShown=true" in out:
        subprocess.run(["adb", "shell", "input", "keyevent", "BACK"], check=True)
        time.sleep(0.5)


def scroll():
    """Scrolls the page down a little, to bring more of it on screen."""
    size = re.findall(r"(\d+)x(\d+)", subprocess.run(["adb", "shell", "wm", "size"], capture_output=True, text=True).stdout)
    w, h = map(int, size[-1])
    subprocess.run(["adb", "shell", "input", "swipe", str(w // 2), str(h * 2 // 3), str(w // 2), str(h // 3), "300"], check=True)
    time.sleep(0.6)


def main():
    cmd, arg = sys.argv[1], sys.argv[2]
    if cmd == "tap":
        n = None
        for _ in range(4):
            root = dump()
            n = find(root, arg, True)
            if n is None:
                n = find(root, arg, False)
            if n is not None:
                break
            scroll()
        if n is None:
            sys.exit(f"nothing labeled {arg!r} on screen")
        x1, y1, x2, y2 = map(int, re.findall(r"\d+", n.get("bounds")))
        subprocess.run(["adb", "shell", "input", "tap", str((x1 + x2) // 2), str((y1 + y2) // 2)], check=True)
    elif cmd == "type":
        hide_keyboard()
        for _ in range(4):
            root = dump()
            label = find(root, arg, False)
            if label is not None:
                break
            scroll()
        if label is None:
            sys.exit(f"no field labeled {arg!r}")
        parents = {c: p for p in root.iter() for c in p}
        sib = list(parents[label])
        field = next((n for n in sib[sib.index(label) + 1:] if n.get("class", "").endswith("EditText")), None)
        if field is None:
            sys.exit(f"no field after {arg!r}")
        x1, y1, x2, y2 = map(int, re.findall(r"\d+", field.get("bounds")))
        subprocess.run(["adb", "shell", "input", "tap", str((x1 + x2) // 2), str((y1 + y2) // 2)], check=True)
        time.sleep(0.4)
        # Replaces what's there: select all (Ctrl+A), delete, then type.
        subprocess.run(["adb", "shell", "input", "keycombination", "113", "29"], check=True)
        subprocess.run(["adb", "shell", "input", "keyevent", "DEL"], check=True)
        subprocess.run(["adb", "shell", "input", "text", sys.argv[3].replace(" ", "%s")], check=True)
        time.sleep(0.3)
        hide_keyboard()
    elif cmd == "wait":
        until = time.time() + float(sys.argv[3] if len(sys.argv) > 3 else 30)
        while time.time() < until:
            if find(dump(), arg, False, True) is not None:
                return
            time.sleep(0.5)
        sys.exit(f"{arg!r} never showed")
    elif cmd == "has":
        sys.exit(0 if find(dump(), arg, False, True) is not None else 1)
    elif cmd == "shot":
        with open(arg, "wb") as f:
            f.write(subprocess.run(["adb", "exec-out", "screencap", "-p"], check=True, capture_output=True).stdout)
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main()

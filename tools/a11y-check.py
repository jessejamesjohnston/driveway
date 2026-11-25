#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Checks the screen on the connected device for three accessibility rules.

  tools/a11y-check.py            dump the current screen with uiautomator and check it
  tools/a11y-check.py dump.xml   check a dump made earlier

For every node of Driveway's own windows (package com.merrimentlabs.driveway):
  - label: a node that is clickable or long-clickable has text, a content
    description, or text in a child (what a screen reader reads out); a
    text field or choice list may instead have a hint, or the label text
    just before it (tied to it with setLabelFor, which dumps don't show);
  - size: a node that is clickable, long-clickable or focusable is at least
    48 x 48 dp (scroll containers, and rows cut off at the screen's edge,
    excepted, as are rows cut off at the edge of the screen or of their
    scrolling page), using the device's real density;
  - distinct: no two clickable controls on one screen have the same label,
    so a screen reader user knows which "Try again" or "Cancel" is which
    (a server card's buttons name their server or file).
Prints each problem and exits 1 if there are any. Uses `adb` (set
ANDROID_SERIAL when more than one device is attached).
"""
import re
import subprocess
import sys
import xml.etree.ElementTree as ET

PKG = "com.merrimentlabs.driveway"
SCROLLERS = ("ScrollView", "ListView", "RecyclerView")


def adb(*args):
    return subprocess.run(["adb", *args], check=True, capture_output=True, text=True).stdout


def dump():
    # uiautomator dump fails now and then on recent Android: retry.
    for _ in range(5):
        out = subprocess.run(["adb", "exec-out", "uiautomator", "dump", "/dev/tty"],
                             capture_output=True, text=True).stdout
        start = out.find("<?xml")
        end = out.rfind("</hierarchy>")
        if start >= 0 and end > start:
            return out[start:end + len("</hierarchy>")]
    sys.exit("uiautomator dump failed")


def screen_height():
    m = re.findall(r"size: (\d+)x(\d+)", adb("shell", "wm", "size"))
    return int(m[-1][1])


def density():
    out = adb("shell", "wm", "density")
    m = re.findall(r"density: (\d+)", out)
    return int(m[-1]) / 160  # the override, when there is one, is listed last


def has_text(node):
    if node.get("text") or node.get("content-desc"):
        return True
    return any(has_text(c) for c in node)


def name_of(node):
    """What a screen reader announces for a control: its description, else its text and its children's."""
    if node.get("content-desc"):
        return node.get("content-desc")
    parts = [node.get("text") or ""] + [name_of(c) for c in node if c.get("clickable") != "true"]
    return " ".join(p for p in parts if p).strip()


def labeled_field(node, parent):
    """A text field or choice list labeled by the text just before it.

    Driveway ties each form label to its field with setLabelFor, which a
    screen reader reads but uiautomator doesn't show; this stands in for it.
    """
    if not node.get("class", "").endswith(("EditText", "Spinner")) or parent is None:
        return False
    if node.get("hint"):
        return True
    siblings = list(parent)
    i = siblings.index(node)
    return i > 0 and siblings[i - 1].get("class", "").endswith("TextView") and bool(siblings[i - 1].get("text"))


def main():
    xml = open(sys.argv[1]).read() if len(sys.argv) > 1 else dump()
    scale = density()
    height = screen_height()
    problems, checked = [], 0
    root = ET.fromstring(xml)
    parents = {c: p for p in root.iter() for c in p}
    for node in root.iter("node"):
        if node.get("package") != PKG:
            continue
        clickable = node.get("clickable") == "true" or node.get("long-clickable") == "true"
        focusable = node.get("focusable") == "true"
        if not (clickable or focusable):
            continue
        cls = node.get("class", "")
        if cls.endswith(SCROLLERS):
            continue
        checked += 1
        x1, y1, x2, y2 = map(int, re.findall(r"\d+", node.get("bounds")))
        name = f'{cls.rsplit(".", 1)[-1]} "{node.get("text") or node.get("content-desc")}" {node.get("bounds")}'
        if clickable and not has_text(node) and not labeled_field(node, parents.get(node)):
            problems.append(f"no label: {name}")
        w, h = (x2 - x1) / scale, (y2 - y1) / scale
        # A row cut off by the screen's edge or its scrolling page's edge
        # (scrolled partly out of view; pages end at the system bars) is
        # measured when it is scrolled into view, not here.
        clipped = y1 <= 0 or y2 >= height
        up = parents.get(node)
        while up is not None and not up.get("class", "").endswith(SCROLLERS):
            up = parents.get(up)
        if up is not None:
            _, sy1, _, sy2 = map(int, re.findall(r"\d+", up.get("bounds")))
            clipped = clipped or y1 <= sy1 or y2 >= sy2
        if (w < 47.5 or h < 47.5) and not clipped:
            problems.append(f"smaller than 48dp ({w:.0f}x{h:.0f}dp): {name}")
    names = {}
    for node in root.iter("node"):
        if node.get("package") != PKG or node.get("clickable") != "true":
            continue
        if node.get("class", "").endswith(("EditText", "Spinner")) or node.get("class", "").endswith(SCROLLERS):
            continue
        n = name_of(node)
        if n:
            names.setdefault(n, []).append(node.get("bounds"))
    for n, where in sorted(names.items()):
        if len(where) > 1:
            problems.append(f'same label on {len(where)} controls: "{n}" {" ".join(where)}')
    for p in problems:
        print(p)
    print(f"{checked} controls checked, {len(problems)} problems")
    sys.exit(1 if problems else 0)


if __name__ == "__main__":
    main()

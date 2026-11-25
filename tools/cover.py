#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Coverage numbers for ./build.sh cover.

  cover.py go PROFILE MERGED [-v]
      go test -coverpkg=./... over several packages writes each block once
      per package run; MERGED has it once, covered if any run covered it,
      for `go tool cover -func`. Prints covered/total statements; -v also
      lists each file's uncovered lines.
  cover.py java REPORT.xml
      line and branch coverage from JaCoCo's XML report: NewLabels, Kinds
      and UpdateCheck whole, and Errors but for explain(), which needs Android.
"""
import collections
import sys
import xml.etree.ElementTree as ET

# Errors' methods that need an Android Context (explain(), which fills in
# message()'s sentence, and its lambda): covered on the emulator, not by the
# JVM checks.
ERRORS_ANDROID = {"explain", "lambda$explain$0"}


def go(profile, merged, verbose):
    mode, count, stmts = None, collections.OrderedDict(), {}
    for line in open(profile):
        if line.startswith("mode:"):
            mode = mode or line.split()[1]
            continue
        block, n, c = line.rsplit(" ", 2)
        stmts[block] = int(n)
        # A set-mode block is covered or not; counts add up.
        count[block] = max(count.get(block, 0), int(c)) if mode == "set" else count.get(block, 0) + int(c)
    with open(merged, "w") as out:
        out.write(f"mode: {mode}\n")
        for block, c in count.items():
            out.write(f"{block} {stmts[block]} {c}\n")
    print(f"{sum(n for b, n in stmts.items() if count[b])}/{sum(stmts.values())} statements")
    if verbose:
        per = collections.defaultdict(lambda: [0, 0, []])
        for block, n in stmts.items():
            name, pos = block.rsplit(":", 1)
            f = per[name]
            f[1] += n
            if count[block]:
                f[0] += n
            else:
                f[2].append(f"{pos.split('.')[0]}-{pos.split(',')[1].split('.')[0]}")
        for name, (hit, total, missing) in sorted(per.items()):
            print(f"{name}: {hit}/{total}" + (f" uncovered {' '.join(missing)}" if missing else ""))


def counters(el):
    return {c.get("type"): (int(c.get("missed")), int(c.get("covered"))) for c in el.findall("counter")}


def add(total, c):
    for kind in ("LINE", "BRANCH"):
        m, h = c.get(kind, (0, 0))
        total[kind][0] += m
        total[kind][1] += h


def show(name, total):
    parts = []
    for kind, label in (("LINE", "lines"), ("BRANCH", "branches")):
        m, h = total[kind]
        parts.append(f"{label} {h}/{m + h} = {100 * h / max(m + h, 1):.1f}%")
    print(f"{name}: " + ", ".join(parts))


def java(report):
    groups = {g: {"LINE": [0, 0], "BRANCH": [0, 0]} for g in ("NewLabels", "Kinds", "UpdateCheck", "Errors")}
    for cls in ET.parse(report).getroot().iter("class"):
        top = cls.get("name").rsplit("/", 1)[1].split("$")[0]
        if top not in groups:
            continue
        if top == "Errors":
            for m in cls.findall("method"):
                if m.get("name") not in ERRORS_ANDROID:
                    add(groups[top], counters(m))
        else:
            add(groups[top], counters(cls))
    both = {"LINE": [0, 0], "BRANCH": [0, 0]}
    for g in ("NewLabels", "Kinds"):
        show(g, groups[g])
        add(both, {k: tuple(v) for k, v in groups[g].items()})
    show("NewLabels + Kinds", both)
    show("UpdateCheck", groups["UpdateCheck"])
    show("Errors (all but explain)", groups["Errors"])


if __name__ == "__main__":
    if sys.argv[1:2] == ["go"] and len(sys.argv) in (4, 5):
        go(sys.argv[2], sys.argv[3], sys.argv[4:] == ["-v"])
    elif sys.argv[1:2] == ["java"] and len(sys.argv) == 3:
        java(sys.argv[2])
    else:
        sys.exit(__doc__)

#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""The license gate for Driveway's engine, and the notices that ship with it.

Reads `go-licenses report` output (name, version, license file, license name;
tab-separated) for everything compiled into the engine, and stops the build
on any license outside the allowed set. MPL-2.0 is allowed only for modules
we use unmodified: a `replace` of an MPL module in
go.mod fails the build, since a modified MPL file must be published.

Writes the licenses screen's data (assets/licenses.json: every component with
its license text) and THIRD_PARTY_NOTICES.md. The app's typeface and icons
are not Go modules: they are listed in tools/notices/notices.json (each with
its license text beside it) and added after the gate, which they don't pass
through (OFL-1.1 is a font license, allowed for the bundled typeface only).

  licenses.py REPORT.tsv GO_MOD --json OUT.json --md OUT.md --version V
"""
import argparse
import json
import os
import re
import sys

PERMISSIVE = {"MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "ISC", "CC0-1.0", "0BSD", "Zlib",
              "UPL-1.0", "Unlicense", "BSD-2-Clause-FreeBSD", "BSL-1.0"}
WEAK = {"MPL-2.0"}  # file-level copyleft: fine unmodified

# What each module is for, so the list reads as more than names.
# Driveway's own module. (The replaced github.com/cloudsoda/sddl is NOT
# skipped: it is reported under its replacement's Apache-2.0 license, so if the
# replace in go.mod ever goes, its LGPL fails the gate.)
OURS = "github.com/jessejamesjohnston/driveway/engine"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("report")
    ap.add_argument("gomod")
    ap.add_argument("--json", required=True)
    ap.add_argument("--md", required=True)
    ap.add_argument("--version", required=True)
    a = ap.parse_args()

    replaced = set(re.findall(r"^replace\s+(\S+)", open(a.gomod).read(), re.M))
    replaced |= set(re.findall(r"^\s+(\S+)\s+(?:v\S+\s+)?=>", open(a.gomod).read(), re.M))

    rows, bad = [], []
    for line in open(a.report):
        line = line.rstrip("\n")
        if not line:
            continue
        name, version, path, lic = (line.split("\t") + ["", "", "", ""])[:4]
        if name == OURS or name.startswith(OURS + "/"):
            continue
        if lic in WEAK:
            if any(name == r or name.startswith(r + "/") for r in replaced):
                bad.append(f"{name}: {lic} and modified (replaced in go.mod)")
        elif lic not in PERMISSIVE:
            bad.append(f"{name}: {lic or 'no license found'}")
        rows.append((name, version, path, lic))
    if bad:
        sys.exit("license gate: these can't ship in Driveway:\n  " + "\n  ".join(bad))
    if len(rows) < 50:
        sys.exit(f"license gate: only {len(rows)} components reported; the report looks incomplete")

    texts, index, comps = [], {}, []
    for name, version, path, lic in sorted(rows):
        text = read(path)
        for extra in notices(path):
            text += "\n\n" + read(extra)
        if text not in index:
            index[text] = len(texts)
            texts.append(text)
        comp = {"name": name, "version": version, "license": lic, "text": index[text]}
        if lic in WEAK:
            # MPL-2.0 section 3.2: say where the source of what we ship is.
            comp["source"] = source_link(name, version)
        comps.append(comp)

    # The typeface and the icons, listed first.
    here = os.path.join(os.path.dirname(os.path.abspath(__file__)), "notices")
    assets = []
    for n in json.load(open(os.path.join(here, "notices.json"))):
        text = read(os.path.join(here, n["text"]))
        if text not in index:
            index[text] = len(texts)
            texts.append(text)
        assets.append({"name": n["name"], "version": n["version"], "license": n["license"], "text": index[text]})
    comps = assets + comps

    # Driveway's own license, its NOTICE on top (Apache-2.0 section 4(d)).
    root = os.path.join(os.path.dirname(os.path.abspath(a.gomod)), "..")
    with open(os.path.join(root, "LICENSE"), encoding="utf-8") as f:
        license_text = f.read().lstrip("\n").rstrip()  # keeps the title's indent
    own = read(os.path.join(root, "NOTICE")) + "\n\n" + license_text
    data = {"version": a.version, "own": own, "components": comps, "texts": texts}
    with open(a.json, "w") as f:
        json.dump(data, f, separators=(",", ":"))

    counts = {}
    for c in comps:
        if c not in assets:
            counts[c["license"]] = counts.get(c["license"], 0) + 1
    with open(a.md, "w") as f:
        f.write("# Third-party notices\n\n")
        f.write(f"Driveway {a.version} is under the Apache License 2.0 (LICENSE, with its NOTICE). Its file engine\n"
                f"is built from the Go modules below, compiled into the app unmodified, except\n"
                f"`github.com/cloudsoda/sddl` (LGPL-3.0), which is replaced by Driveway's own Apache-2.0 code in\n"
                f"`engine/third_party/sddl`. The app shows every\n"
                f"license text in full under **Settings, Open-source licenses**. The app also bundles the Figtree\n"
                f"typeface (SIL Open Font License 1.1) and Material Symbols icons (Apache-2.0); their notices are in\n"
                f"`tools/notices`.\n\n")
        f.write("Trademarks: Box is a trademark of Box, Inc., Dropbox of Dropbox, Inc. and OneDrive of Microsoft\n"
                "Corporation. Driveway names these services in text only, to say which it connects to, and shows none\n"
                "of their logos (docs/BRAND-MARKS.md). Driveway is not affiliated with, sponsored or endorsed by any\n"
                "of them.\n\n")
        f.write("MPL-2.0 components are used unmodified; their source is at the links below.\n\n")
        f.write("| Licenses | Components |\n|---|---|\n")
        for lic, n in sorted(counts.items(), key=lambda kv: -kv[1]):
            f.write(f"| {lic} | {n} |\n")
        f.write("\n| Component | Version | License |\n|---|---|---|\n")
        for c in comps:
            if c in assets:
                continue
            link = source_link(c["name"], c["version"])
            f.write(f"| [{c['name']}]({link}) | {c['version']} | {c['license']} |\n")
    print(f"license gate: {len(comps) - len(assets)} components, {len(texts)} distinct license texts, all allowed")


def source_link(name, version):
    return f"https://pkg.go.dev/{name}@{version}" if version else f"https://pkg.go.dev/{name}"


def read(path):
    with open(path, encoding="utf-8", errors="replace") as f:
        return f.read().strip()


def notices(license_path):
    d = os.path.dirname(license_path)
    return [os.path.join(d, n) for n in sorted(os.listdir(d)) if re.match(r"NOTICE(\.txt|\.md)?$", n, re.I)]


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""AC-032 debt audit: PONYTAIL-DEBT.md exists, every `ponytail:` marker in the repository's
tracked files names an upgrade trigger, and the ledger's last "Debt count: N." line matches
its numbered entries plus its unnumbered ponytail sections. Prints the counts; exits 1 on
any problem."""
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def main():
    ledger = os.path.join(ROOT, "PONYTAIL-DEBT.md")
    if not os.path.isfile(ledger):
        print("PONYTAIL-DEBT.md is missing")
        return 1
    files = subprocess.run(["git", "ls-files"], cwd=ROOT, capture_output=True, text=True, check=True).stdout.split()
    problems, markers = [], 0
    ledger_text = open(ledger, encoding="utf-8").read()
    ledger_entries = [e for e in re.split(r"\n(?=\d+\. |## )", ledger_text)]
    for rel in files:
        path = os.path.join(ROOT, rel)
        if os.path.islink(path) or not os.path.isfile(path):
            continue
        try:
            text = open(path, encoding="utf-8").read()
        except (UnicodeDecodeError, OSError):
            continue
        lines = text.splitlines()
        for i, line in enumerate(lines):
            if "ponytail:" not in line or rel == "scripts/release/check_debt.py":
                continue
            if re.search(r"`ponytail:|\"ponytail:|'ponytail:|ponytail:` ", line):
                continue  # a mention of the marker format, not a marker
            markers += 1
            # A marker may wrap over the comment lines that follow it.
            span = " ".join(lines[i:i + 8]).lower()
            # An applied migration's text is frozen by its checksum (PRD 6), so its marker
            # may defer the trigger to a ledger entry that names the file.
            frozen = re.search(r"/migrations/[^/]+\.sql$", rel) and any(
                rel in e and "trigger" in e.lower() for e in ledger_entries)
            if "trigger" not in span and not frozen:
                problems.append("%s:%d ponytail marker names no upgrade trigger" % (rel, i + 1))
    text = open(ledger, encoding="utf-8").read()
    entries = len(re.findall(r"^\d+\. ", text, re.M)) + len(re.findall(r"^ponytail: ", text, re.M))
    counts = re.findall(r"^Debt count: (\d+)\.", text, re.M)
    if not counts:
        problems.append("PONYTAIL-DEBT.md has no 'Debt count: N.' line")
    elif int(counts[-1]) != entries:
        problems.append("PONYTAIL-DEBT.md says Debt count: %s but lists %d entries" % (counts[-1], entries))
    print("ponytail markers: %d; ledger entries: %d" % (markers, entries))
    for p in problems:
        print(p)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())

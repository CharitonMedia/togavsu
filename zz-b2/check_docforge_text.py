#!/usr/bin/env python3
"""Checks that Patrick Nolan's template and clause text ships exactly as he wrote it.

docforge/SOURCE.sha256 records, for every template, clause and master copied from DocForge,
the SHA-256 of the file as copied and the SHA-256 of the same file with its front-matter
metadata keys (provenance, authored_by, version) removed. A file passes when it is unchanged,
or when the only change is to those metadata keys inside its leading {# --- ... --- #} block.
Any other change, and any recorded file that is missing, fails the check. Nothing here may be
rewritten by a build agent: change the wording only by Patrick's own edit in DocForge.
"""
import hashlib
import pathlib
import re
import sys

META = re.compile(r"^(provenance|authored_by|version)\s*:")


def strip_meta(data: bytes) -> bytes:
    lines = data.split(b"\n")
    out, in_front, dropping = [], False, False
    for i, raw in enumerate(lines):
        line = raw.decode("utf-8", "surrogateescape")
        if i == 0 and line.startswith("{# ---"):
            in_front = True
            out.append(raw)
            continue
        if in_front and line.startswith("--- #}"):
            in_front = dropping = False
            out.append(raw)
            continue
        if in_front:
            if META.match(line):
                dropping = True
                continue
            if dropping and line[:1] in (" ", "\t"):
                continue
            dropping = False
        out.append(raw)
    return b"\n".join(out)


def main(root: pathlib.Path) -> int:
    record = root / "docforge" / "SOURCE.sha256"
    problems, checked = [], 0
    for line in record.read_text().splitlines():
        if not line.strip() or line.startswith("#"):
            continue
        exact, stripped, path = line.split(None, 2)
        target = root / path
        checked += 1
        if not target.is_file():
            problems.append(f"missing: {path}")
            continue
        data = target.read_bytes()
        if hashlib.sha256(data).hexdigest() == exact:
            continue
        if hashlib.sha256(strip_meta(data)).hexdigest() != stripped:
            problems.append(f"text changed: {path}")
    for p in problems:
        print(p)
    print(f"{checked} files checked, {len(problems)} problems")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main(pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else ".")))

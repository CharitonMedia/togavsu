#!/usr/bin/env python3
"""Write docs/build/reports/release.json from full-acceptance results.

usage: python3 scripts/release/write_report.py FULL_RESULTS PHASE_DIR [--extra notes.json]

FULL_RESULTS is the results.json of `scripts/ci/full-acceptance.sh` at the tested revision.
PHASE_DIR holds one <phase>.json per phase case, each the results.json of
`full_acceptance.py --phase <phase> --reuse FULL_RESULTS`, with that run's exit code in
<phase>.exit. The report's status is pass only when the full run passed; every criterion
lands in exactly one of passed_acceptance_ids and pending_acceptance_ids, from the run's
own decisions. --extra merges a JSON object of further report fields (notes, CI links).
"""
import json
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
PHASES = {
    "full-acceptance": None,
    "desktop-install": ["AC-001"],
    "signed-images-updater-load": ["AC-013", "AC-016"],
    "backup-restore": ["AC-018", "AC-040"],
}


def main(argv):
    if len(argv) < 3:
        raise SystemExit(__doc__)
    with open(argv[1]) as f:
        full = json.load(f)
    extra = {}
    if "--extra" in argv:
        with open(argv[argv.index("--extra") + 1]) as f:
            extra = json.load(f)
    with open(os.path.join(ROOT, "docs", "build", "acceptance.json")) as f:
        ids = [c["id"] for c in json.load(f)]
    passed, pending = full["passed_acceptance_ids"], full["pending_acceptance_ids"]
    if sorted(passed + pending) != sorted(ids) or set(passed) & set(pending):
        raise SystemExit("results do not partition the 52 criteria")
    cases = []
    for phase, mapped in PHASES.items():
        name = os.path.join(argv[2], phase + ".json")
        with open(name) as f:
            res = json.load(f)
        with open(os.path.join(argv[2], phase + ".exit")) as f:
            code = int(f.read().strip())
        if res["tested_revision"] != full["tested_revision"]:
            raise SystemExit(phase + " was decided at another revision")
        acc = ids if mapped is None else mapped
        command = "sh scripts/ci/full-acceptance.sh" if mapped is None else \
            "python3 scripts/release/full_acceptance.py --phase " + phase + " --reuse <full results.json>"
        cases.append({
            "id": phase, "acceptance_ids": acc, "status": "pass" if code == 0 and res["status"] == "pass" else "fail",
            "command": command, "exit_code": code, "revision": full["tested_revision"],
            "passed_acceptance_ids": res["passed_acceptance_ids"], "pending_acceptance_ids": res["pending_acceptance_ids"],
            "release_gates": res["release_gates"],
        })
    full_case = next(c for c in cases if c["id"] == "full-acceptance")
    tests = []
    for cid, r in full["checks"].items():
        t = {"id": cid, "command": r["command"], "exit_code": r["exit_code"], "status": r["status"]}
        for k in ("seconds", "problems", "reason"):
            if k in r:
                t[k] = r[k]
        tests.append(t)
    report = {
        "task_id": "release",
        "status": "pass" if full["status"] == "pass" and all(c["status"] == "pass" for c in cases) else "pending",
        "tested_revision": full["tested_revision"],
        "exit_code": full_case["exit_code"],
        "tests": tests,
        "phase_cases": cases,
        "passed_acceptance_ids": passed,
        "pending_acceptance_ids": pending,
        "pending_reasons": {i: full["criteria"][i]["reasons"] for i in pending},
        "release_gates": full["release_gates"],
    }
    report.update(extra)
    out = os.path.join(ROOT, "docs", "build", "reports", "release.json")
    with open(out, "w") as f:
        json.dump(report, f, indent=2)
        f.write("\n")
    print("wrote %s: status %s, %d passed, %d pending" % (os.path.relpath(out, ROOT), report["status"], len(passed), len(pending)))


if __name__ == "__main__":
    main(sys.argv)

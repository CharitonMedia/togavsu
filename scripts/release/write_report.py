#!/usr/bin/env python3
"""Write docs/build/reports/release.json from subset acceptance runs.

usage: python3 scripts/release/write_report.py PHASE_DIR [--extra notes.json]

The release assignment in docs/build/tasks.json names its acceptance_ids and a
phase_acceptance map. PHASE_DIR holds, for every phase in that map, <phase>.json (the
results.json of `TOGAVSU_ACCEPTANCE_IDS=<its ids> sh scripts/ci/full-acceptance.sh`) and
<phase>.exit (that run's exit code). Every phase must have run exactly its mapped ids in
subset mode at one clean revision. The phase covering all assigned ids decides the
partition of the assigned ids into passed_acceptance_ids and pending_acceptance_ids; the
report's status is pass only when every phase run exited 0 with status pass.
--extra merges a JSON object of further report fields (notes, CI links).
"""
import json
import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def assignment():
    with open(os.path.join(ROOT, "docs", "build", "tasks.json")) as f:
        tasks = json.load(f)
    tasks = tasks["tasks"] if isinstance(tasks, dict) else tasks
    return next(t for t in tasks if t["id"] == "release")


def build(task, phase_dir):
    assigned = task["acceptance_ids"]
    cases, runs = [], {}
    for phase, mapped in task["phase_acceptance"].items():
        with open(os.path.join(phase_dir, phase + ".json")) as f:
            res = json.load(f)
        with open(os.path.join(phase_dir, phase + ".exit")) as f:
            code = int(f.read().strip())
        if res.get("mode") != "subset" or res.get("acceptance_ids") != mapped:
            raise SystemExit(phase + " did not run exactly its mapped criteria in subset mode")
        if res["working_tree_dirty"]:
            raise SystemExit(phase + " ran on a dirty working tree")
        runs[phase] = res
        cases.append({
            "id": phase, "acceptance_ids": mapped,
            "status": "pass" if code == 0 and res["status"] == "pass" else "fail",
            "command": "TOGAVSU_ACCEPTANCE_IDS=" + ",".join(mapped) + " sh scripts/ci/full-acceptance.sh",
            "exit_code": code, "revision": res["tested_revision"],
            "passed_acceptance_ids": res["passed_acceptance_ids"],
            "pending_acceptance_ids": res["pending_acceptance_ids"],
        })
    revisions = {r["tested_revision"] for r in runs.values()}
    if len(revisions) != 1:
        raise SystemExit("phase runs were made at different revisions: " + ", ".join(sorted(revisions)))
    whole = [p for p, m in task["phase_acceptance"].items() if sorted(m) == sorted(assigned)]
    if len(whole) != 1:
        raise SystemExit("exactly one phase must cover every assigned criterion")
    main_case = next(c for c in cases if c["id"] == whole[0])
    main_run = runs[whole[0]]
    passed, pending = main_run["passed_acceptance_ids"], main_run["pending_acceptance_ids"]
    if sorted(passed + pending) != sorted(assigned) or set(passed) & set(pending):
        raise SystemExit("results do not partition the assigned criteria")
    tests = []
    for cid, r in main_run["checks"].items():
        t = {"id": cid, "command": r["command"], "exit_code": r["exit_code"], "status": r["status"]}
        for k in ("seconds", "problems", "reason"):
            if k in r:
                t[k] = r[k]
        tests.append(t)
    ok = all(c["status"] == "pass" for c in cases)
    return {
        "task_id": "release",
        "status": "pass" if ok and not pending else "pending",
        "tested_revision": revisions.pop(),
        "exit_code": main_case["exit_code"],
        "command": main_case["command"],
        "tests": tests,
        "phase_cases": cases,
        "passed_acceptance_ids": passed,
        "pending_acceptance_ids": pending,
        "pending_reasons": {i: main_run["criteria"][i]["reasons"] for i in pending},
    }


def main(argv):
    if len(argv) < 2:
        raise SystemExit(__doc__)
    report = build(assignment(), argv[1])
    if "--extra" in argv:
        with open(argv[argv.index("--extra") + 1]) as f:
            report.update(json.load(f))
    out = os.path.join(ROOT, "docs", "build", "reports", "release.json")
    with open(out, "w") as f:
        json.dump(report, f, indent=2)
        f.write("\n")
    print("wrote %s: status %s, %d passed, %d pending" % (
        os.path.relpath(out, ROOT), report["status"], len(report["passed_acceptance_ids"]),
        len(report["pending_acceptance_ids"])))


if __name__ == "__main__":
    main(sys.argv)

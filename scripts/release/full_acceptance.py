#!/usr/bin/env python3
"""Full release acceptance (PRD 12): run every check in docs/release/acceptance-evidence.json
once and decide each of the 52 criteria from the results.

A criterion passes only when it has at least one check, every one of its checks exited 0,
and it lists no gap. A go test check passes only when each test its -run pattern names
printed "--- PASS" and none was skipped, so a pattern that matches nothing never passes.
The full run passes only when all 52 criteria pass and every release gate's check passed.
The exit code is 0 only then; anything not executed or not proven is pending, never pass.

Usage:
  python3 scripts/release/full_acceptance.py [--out DIR] [--ids AC-003,AC-004 ...] [--check ID ...] [--list]

--ids (scripts/ci/full-acceptance.sh passes TOGAVSU_ACCEPTANCE_IDS here) runs the checks of
exactly the listed criteria and decides only those; release gates belong to the full run and
are not decided. It exits 0 only when every listed criterion passes, and 2 on an empty list
or an id that is not in docs/build/acceptance.json.
"""
import argparse
import datetime
import json
import os
import re
import subprocess
import sys
import time

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
EVIDENCE = os.path.join(ROOT, "docs", "release", "acceptance-evidence.json")
CRITERIA = os.path.join(ROOT, "docs", "build", "acceptance.json")

NEEDS = {
    "db": lambda: bool(os.environ.get("TOGAVSU_TEST_DATABASE_URL")),
    "db_b": lambda: bool(os.environ.get("TOGAVSU_TEST_DATABASE_URL_B")),
    "pg_tools": lambda: bool(os.environ.get("TOGAVSU_PG_DUMP")) and bool(os.environ.get("TOGAVSU_PSQL")),
    "docker": lambda: subprocess.run(["docker", "info"], capture_output=True).returncode == 0,
    "wordcraft": lambda: bool(os.environ.get("TOGAVSU_WORDCRAFT_CLI")),
}


def load():
    with open(EVIDENCE) as f:
        ev = json.load(f)
    with open(CRITERIA) as f:
        ids = [c["id"] for c in json.load(f)]
    problems = []
    if sorted(ev["criteria"]) != sorted(ids):
        problems.append("evidence map does not list exactly the criteria in docs/build/acceptance.json")
    for cid, c in ev["criteria"].items():
        if not c.get("checks"):
            problems.append(cid + " has no check")
        for ch in c.get("checks", []):
            if ch not in ev["checks"]:
                problems.append(cid + " names unknown check " + ch)
    for gid, g in ev["release_gates"].items():
        if not g.get("checks"):
            problems.append("gate " + gid + " has no check")
        for ch in g.get("checks", []):
            if ch not in ev["checks"]:
                problems.append("gate " + gid + " names unknown check " + ch)
    return ev, ids, problems


def go_expectations(command):
    """The test names each go test -run pattern requires, and whether it is a prefix pattern."""
    out = []
    for m in re.finditer(r"go test[^&;|]*?-run\s+'([^']+)'", command):
        pat = m.group(1)
        lit = re.fullmatch(r"\^\(?([A-Za-z0-9_|]+)\)?\$", pat)
        if lit:
            out.append((lit.group(1).split("|"), False))
        else:
            out.append(([pat], True))
    return out


def verbose(command):
    return re.sub(r"\bgo test (?!-v\b)", "go test -v ", command)


def run_check(cid, spec, logdir):
    missing = [n for n in spec.get("needs", []) if not NEEDS[n]()]
    if missing:
        return {"id": cid, "command": spec["command"], "status": "not_run", "exit_code": None,
                "reason": "prerequisites missing: " + ", ".join(missing)}
    command = verbose(spec["command"])
    log = os.path.join(logdir, cid + ".log")
    start = time.time()
    with open(log, "w") as f:
        proc = subprocess.run(command, shell=True, cwd=ROOT, stdout=f, stderr=subprocess.STDOUT)
    took = round(time.time() - start, 1)
    with open(log, errors="replace") as f:
        text = f.read()
    result = {"id": cid, "command": spec["command"], "exit_code": proc.returncode, "seconds": took,
              "log": os.path.relpath(log, ROOT) if log.startswith(ROOT) else log}
    problems = []
    if "go test" in command:
        if re.search(r"^\s*--- SKIP", text, re.M):
            problems.append("a test was skipped")
        for names, prefix in go_expectations(spec["command"]):
            if prefix:
                if not re.search(r"^\s*--- PASS: " + names[0].lstrip("^"), text, re.M):
                    problems.append("no test matching " + names[0] + " passed")
                continue
            for n in names:
                if not re.search(r"^\s*--- PASS: " + re.escape(n) + r" ", text, re.M):
                    problems.append(n + " did not pass")
    if re.search(r"^PENDING\b", text, re.M):
        problems.append("the check reported PENDING")
    result["status"] = "pass" if proc.returncode == 0 and not problems else "fail"
    if problems:
        result["problems"] = problems
    tail = text.strip().splitlines()[-8:]
    result["tail"] = tail
    return result


def revision():
    head = subprocess.run(["git", "rev-parse", "HEAD"], cwd=ROOT, capture_output=True, text=True).stdout.strip()
    dirty = subprocess.run(["git", "status", "--porcelain", "--untracked-files=no"], cwd=ROOT,
                           capture_output=True, text=True).stdout.strip()
    return head, bool(dirty)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=os.path.join(os.environ.get("TMPDIR", "/tmp"), "togavsu-acceptance"))
    ap.add_argument("--ids", help="comma-separated criteria to run instead of all 52 and the release gates")
    ap.add_argument("--check", action="append", default=[])
    ap.add_argument("--list", action="store_true")
    ap.add_argument("--reuse", help="decide from the check results in this results.json instead of running them again; "
                    "it must have been recorded at this same revision with a clean working tree")
    args = ap.parse_args()
    ev, ids, problems = load()
    if args.list:
        for cid, spec in ev["checks"].items():
            print(cid + ": " + spec["command"])
        return 0
    if problems:
        print("\n".join(problems), file=sys.stderr)
        return 2
    want_ids, want_gates = ids, list(ev["release_gates"])
    if args.ids is not None:
        want_ids = []
        for i in args.ids.split(","):
            i = i.strip()
            if i and i not in want_ids:
                want_ids.append(i)
        unknown = [i for i in want_ids if i not in ids]
        if not want_ids or unknown:
            print("--ids must list criteria from docs/build/acceptance.json; unknown: " + ", ".join(unknown or ["(empty list)"]),
                  file=sys.stderr)
            return 2
        want_gates = []
    checks = []
    for cid in want_ids:
        checks += [c for c in ev["criteria"][cid]["checks"] if c not in checks]
    for g in want_gates:
        checks += [c for c in ev["release_gates"][g]["checks"] if c not in checks]
    if args.check:
        checks = [c for c in checks if c in args.check]
    os.makedirs(args.out, exist_ok=True)
    head, dirty = revision()
    results = {}
    if args.reuse:
        with open(args.reuse) as f:
            prior = json.load(f)
        if prior.get("tested_revision") != head or prior.get("working_tree_dirty") or dirty:
            print("--reuse results were not recorded at this clean revision", file=sys.stderr)
            return 2
        results = {c: prior["checks"][c] for c in checks if c in prior.get("checks", {})}
    for cid in [c for c in checks if c not in results]:
        print("== " + cid + ": " + ev["checks"][cid]["command"], flush=True)
        results[cid] = run_check(cid, ev["checks"][cid], args.out)
        r = results[cid]
        print("   " + r["status"] + " exit=" + str(r["exit_code"]) + " " + "; ".join(r.get("problems", [])) + r.get("reason", ""), flush=True)
    passed, pending, decided = [], [], {}
    for cid in want_ids:
        c = ev["criteria"][cid]
        why = []
        for ch in c["checks"]:
            r = results.get(ch)
            if r is None:
                why.append(ch + " not run")
            elif r["status"] != "pass":
                why.append(ch + " " + r["status"])
        why += ["gap: " + g for g in c.get("gaps", [])]
        decided[cid] = {"status": "pass" if not why else "pending", "checks": c["checks"], "reasons": why}
        (passed if not why else pending).append(cid)
    gates = {}
    for g in want_gates:
        rs = [results.get(c) for c in ev["release_gates"][g]["checks"]]
        gates[g] = "pass" if all(r and r["status"] == "pass" for r in rs) else "pending"
    ok = not pending and all(v == "pass" for v in gates.values()) and not dirty
    summary = {
        "tested_revision": head, "working_tree_dirty": dirty,
        "finished_at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "mode": "subset" if args.ids is not None else "full", "acceptance_ids": want_ids,
        "status": "pass" if ok else "pending",
        "passed_acceptance_ids": passed, "pending_acceptance_ids": pending,
        "release_gates": gates, "criteria": decided, "checks": results,
    }
    with open(os.path.join(args.out, "results.json"), "w") as f:
        json.dump(summary, f, indent=2)
        f.write("\n")
    print("passed %d, pending %d; gates %s; status %s" % (len(passed), len(pending), json.dumps(gates), summary["status"]))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

"""Validate setup metadata, never product acceptance."""
import json
from pathlib import Path

root = Path(__file__).resolve().parents[1]
plan = json.loads((root / "docs/build/tasks.json").read_text())
acceptance = json.loads((root / "docs/build/acceptance.json").read_text())
ids = {x["id"] for x in acceptance}
assert len(ids) == len(acceptance) == 52
prd = (root / "togavsu-core-prd.md").read_text()
rows = [s[2:] for s in prd.split("## 12. Acceptance criteria")[1].splitlines() if s.startswith("- ")]
assert rows == [x["requirement"] for x in acceptance], "Acceptance index drift"
tasks = {t["id"]: t for t in plan["tasks"]}
assert len(tasks) == len(plan["tasks"])
for t in tasks.values():
    assert set(t["depends_on"]) <= tasks.keys()
    assert set(t["acceptance_ids"]) <= ids
    assert t["owned_paths"] and t["prompt"] and t["prd_sections"]
    visiting = set()
    def walk(key):
        assert key not in visiting, "Task dependency cycle"
        visiting.add(key)
        for dep in tasks[key]["depends_on"]:
            walk(dep)
        visiting.remove(key)
    walk(t["id"])
initial = [t for t in tasks.values() if t["wave"] == "bootstrap"]
assert len(initial) == plan["max_implementation_agents"] == 4
for i, a in enumerate(initial):
    for b in initial[i+1:]:
        for x in a["owned_paths"]:
            for y in b["owned_paths"]:
                assert x != y and not x.startswith(y.rstrip("/") + "/") and not y.startswith(x.rstrip("/") + "/"), (a["id"],b["id"],x,y)
assert set.union(*(set(t["acceptance_ids"]) for t in tasks.values())) == ids
# The controller plan and tasks.json are the same assignments; agents and reviewers read tasks.json.
controller = json.loads((root / "docs/build/controller-plan.json").read_text())
assert controller["tasks"] == plan["tasks"], "controller-plan.json and tasks.json assignments differ"
assert controller["max_implementation_agents"] <= 4, "At most four implementation agents"
assert set(controller.get("held_tasks", [])) <= tasks.keys()
# Implementation lanes that can run at once own disjoint paths. A broader path may give up a
# narrower one through excluded_paths; the excluded file then belongs to the other lane only.
from fnmatch import fnmatch
def overlaps(a, b):
    for x in a["owned_paths"]:
        for y in b["owned_paths"]:
            if x == y:
                return (x, y)
            for wide, narrow, owner in ((x, y, a), (y, x, b)):
                if narrow.startswith(wide.rstrip("/") + "/"):
                    if not any(fnmatch(narrow, g) for g in owner.get("excluded_paths", [])):
                        return (x, y)
    return None
lanes = [t for t in tasks.values() if t["wave"] not in ("bootstrap", "integration", "release")]
for i, a in enumerate(lanes):
    for b in lanes[i+1:]:
        if a["wave"] == b["wave"]:
            hit = overlaps(a, b)
            assert not hit, (a["id"], b["id"]) + hit
for p in ["AGENTS.md","docs/build/WORKFLOW.md","docs/build/review.schema.json","docs/build/prompts/codex-review.md","PONYTAIL-DEBT.md"]:
    assert (root / p).is_file(), p
print("PASS: 52 criteria indexed, dependency graph valid, four disjoint bootstrap assignments, controller plan matches tasks.json, parallel lanes disjoint. Product acceptance pending.")

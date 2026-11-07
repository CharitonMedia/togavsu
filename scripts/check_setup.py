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
for p in ["AGENTS.md","docs/build/WORKFLOW.md","docs/build/review.schema.json","docs/build/prompts/codex-review.md","PONYTAIL-DEBT.md"]:
    assert (root / p).is_file(), p
print("PASS: 52 criteria indexed, dependency graph valid, four disjoint bootstrap assignments. Product acceptance pending.")

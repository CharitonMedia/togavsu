#!/usr/bin/env python3
"""Lets one controller instance drive a branch other than main (plan field base_branch)."""
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
src = root / "scripts/build_controller.py"
text = src.read_text()


def sub(old, new, count):
    global text
    found = text.count(old)
    if found != count:
        sys.exit(f"expected {count} of {old!r}, found {found}")
    text = text.replace(old, new)


# A plan's base_branch (default main) is the branch its lanes start from, its PRs target and its merges land on.
sub('REPO = "CharitonMedia/togavsu"\n',
    'REPO = "CharitonMedia/togavsu"\n'
    '# A plan may name another base_branch, such as an integration branch built beside main. Lane workflows are\n'
    '# always dispatched from main, where the protected Lane agent workflow lives.\n'
    'BASE_BRANCH_PATTERN = r"[A-Za-z0-9][A-Za-z0-9._-]{0,40}"\n\n\n'
    'def base_branch(plan):\n'
    '    base = plan.get("base_branch", "main")\n'
    '    if not isinstance(base, str) or not re.fullmatch(BASE_BRANCH_PATTERN, base):\n'
    '        raise ValueError("Invalid base_branch: " + repr(base))\n'
    '    return base\n', 1)
sub('    def __init__(self, state_dir, repo_dir, dry=False, new_on_cursor=False, worker="claude", model=None):\n',
    '    def __init__(self, state_dir, repo_dir, dry=False, new_on_cursor=False, worker="claude", model=None, base="main"):\n', 1)
sub('        self.new_on_cursor = new_on_cursor\n',
    '        self.new_on_cursor = new_on_cursor\n        self.base = base_branch({"base_branch": base})\n', 1)
sub('                "base": "main", "body": body, "draft": False})',
    '                "base": self.base, "body": body, "draft": False})', 1)
sub('        self.remote, self.plan, self.dir, self.dry, self.mailer = remote, plan, Path(state_dir), dry, mailer\n',
    '        self.remote, self.plan, self.dir, self.dry, self.mailer = remote, plan, Path(state_dir), dry, mailer\n'
    '        self.base = base_branch(plan)\n', 1)
sub('self.remote.gh("branches/main/protection")', 'self.remote.gh("branches/" + self.base + "/protection")', 4)
sub('self.remote.gh("branches/main/protection/required_status_checks", "PATCH"',
    'self.remote.gh("branches/" + self.base + "/protection/required_status_checks", "PATCH"', 1)
sub('self.remote.gh("git/ref/heads/main")', 'self.remote.gh("git/ref/heads/" + self.base)', 3)
sub('pr["base"]["ref"] != "main"', 'pr["base"]["ref"] != self.base', 1)
sub('fresh.get("base", {}).get("ref") != "main"', 'fresh.get("base", {}).get("ref") != self.base', 1)
sub('if pr.get("base", {}).get("ref") != "main":\n            self.halt(task, record, "Merge target changed; refusing dependency completion outside main")',
    'if pr.get("base", {}).get("ref") != self.base:\n            self.halt(task, record, "Merge target changed; refusing dependency completion outside " + self.base)', 1)
sub('"Merge origin/main into your existing PR branch', '"Merge origin/" + self.base + " into your existing PR branch', 1)
sub('"Refresh branch against main; await fresh tests and Codex"', '"Refresh branch against " + self.base + "; await fresh tests and Codex"', 1)
sub('"Await verified merge commit on main; dependencies remain blocked"',
    '"Await verified merge commit on " + self.base + "; dependencies remain blocked"', 1)
sub('"Merge response could not be verified on main within 15 minutes"',
    '"Merge response could not be verified on " + self.base + " within 15 minutes"', 1)
sub('"Verified protected merge on main: " + merged', '"Verified protected merge on " + self.base + ": " + merged', 1)
sub('"Do not merge, push main, force-push or alter controller/gates/acceptance criteria. "',
    '"Do not merge, push " + self.base + ", force-push or alter controller/gates/acceptance criteria. "', 1)
for old in ['ActionsRemote(state, state.resolve().parent, dry, worker="cursor", model=plan.get("cursor_model", "auto"))',
            'ActionsRemote(state, state.resolve().parent, dry)',
            'ActionsRemote(state, state.resolve().parent, dry, new_on_cursor=True)']:
    sub(old, old[:-1] + ', base=base_branch(plan))', 1)
src.write_text(text)

tests = root / "scripts/test_build_controller.py"
t = tests.read_text()
anchor = "    def test_admin_enforcement_off_halts_until_an_operator_adopts_it(self):\n"
if t.count(anchor) != 1:
    sys.exit("test anchor missing")
t = t.replace(anchor, '''    def test_base_branch_plan_reads_merges_and_verifies_only_its_branch(self):
        remote = self.remote
        original = remote.gh

        def on_batch2(path, method="GET", payload=None, binary=False):
            if "heads/main" in path or "branches/main/" in path:
                raise AssertionError("main touched by a batch2 plan: " + path)
            return original(path.replace("heads/batch2", "heads/main").replace("branches/batch2/", "branches/main/"),
                            method, payload, binary)
        remote.gh = on_batch2
        remote.pr["base"]["ref"] = "batch2"
        self.controller = Controller(remote, {"tasks": [SPEC], "max_repairs": 3, "max_implementation_agents": 4,
                                              "base_branch": "batch2"}, self.temp.name)
        self.controller.tasks["one"] = self.record
        self.assertTrue(self.process())
        self.assertEqual(self.record["completed"]["merge_sha"], NEW)
        self.assertTrue(any(c[1] == "branches/main/protection" for c in remote.calls))

    def test_pr_on_main_is_refused_by_a_base_branch_plan(self):
        self.controller = Controller(self.remote, {"tasks": [SPEC], "max_repairs": 3, "max_implementation_agents": 4,
                                                   "base_branch": "batch2"}, self.temp.name)
        self.controller.tasks["one"] = self.record
        self.assertFalse(self.process())
        self.assertEqual(self.record["halted"], "Unexpected PR repository or target branch")
        self.assertFalse(any(c[1].endswith("/merge") for c in self.remote.calls))

    def test_invalid_base_branch_is_refused(self):
        for bad in ["", "-x", "a/b", "x" * 60, 7]:
            with self.assertRaises(ValueError):
                Controller(self.remote, {"tasks": [SPEC], "base_branch": bad}, self.temp.name)

''' + anchor)
anchor2 = "    def test_followup_starts_from_branch_head_and_carries_assignment(self):\n"
if t.count(anchor2) != 1:
    sys.exit("lane test anchor missing")
t = t.replace(anchor2, '''    def test_base_branch_lane_opens_its_pr_against_that_branch(self):
        self.remote.base = "batch2"
        run_id = self.launch()["run"]["id"]
        head, bundle = self.lane_commit(self.base, "first")
        self.finish(run_id, self.base, head, bundle)
        self.assertEqual(self.remote.agent(self.record)["status"], "FINISHED")
        created = [c for c in self.remote.calls if c[:2] == ("POST", "pulls")]
        self.assertEqual(created[0][2]["base"], "batch2")
        dispatch = [c for c in self.remote.calls if c[1].endswith("/dispatches")]
        self.assertEqual(dispatch[0][2]["ref"], "main")

''' + anchor2)
tests.write_text(t)
print("patched")

import base64
import copy
import datetime as dt
import io
import json
import os
import stat
import subprocess
import sys
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import urllib.error
import urllib.parse
import zipfile
import zlib

import build_controller
from build_controller import AgentNotFound, Controller, ObservationError, Remote, ReviewPending, ReviewSuperseded, APP_ID, LEGACY_REPAIR_HALT, atomic, extract_review, latest_checks, problem_fingerprints, review_problem, validate_review

BASE, HEAD, NEW = "a" * 40, "b" * 40, "c" * 40
SPEC = {"id": "one", "depends_on": [], "prompt": "bounded task"}


def check(name, conclusion="success", ident=1):
    return {"name": name, "status": "completed", "conclusion": conclusion, "id": ident,
            "head_sha": HEAD, "app": {"id": APP_ID},
            "details_url": "https://github.com/CharitonMedia/togavsu/actions/runs/123/job/456"}


class FakeRemote:
    def __init__(self):
        self.main = BASE
        self.pr = {"number": 7, "state": "open", "merged": False, "draft": False, "body": "Original evidence",
                   "mergeable": True, "labels": [{"name": "togavsu-task:one"}], "head": {"sha": HEAD, "ref": "cursor/one", "repo": {"full_name": "CharitonMedia/togavsu"}},
                   "base": {"ref": "main"}, "html_url": "https://github.com/CharitonMedia/togavsu/pull/7"}
        self.report = {"task_id": "one", "reviewed_head": HEAD, "reviewed_base": BASE, "verdict": "pass", "findings": [], "summary": "Reviewed"}
        self.context = {"task_id": "one", "head": HEAD, "base": BASE, "pr": 7, "body": "Original evidence"}
        self.behind = 0
        self.calls = []
        self.run_id = "run-old"
        self.run_status = "FINISHED"
        self.runs = {}
        self.fail_post = False
        self.fail_merge = False
        self.infra = {}
        self.check_rows = {name: check(name) for name in ["Setup checks", "Codex checkpoint"]}
        self.protection = {"required_status_checks": {"strict": True, "contexts": ["Setup checks", "Codex checkpoint"]},
                           "enforce_admins": {"enabled": True}, "required_pull_request_reviews": {}}

    def gh(self, path, method="GET", payload=None, binary=False):
        self.calls.append((method, path, payload))
        if path == "git/ref/heads/main":
            return {"object": {"sha": self.main}}
        if path == "pulls/7":
            return copy.deepcopy(self.pr)
        if path == "branches/main/protection":
            return copy.deepcopy(self.protection)
        if path == "branches/main/protection/required_status_checks" and method == "PATCH":
            self.protection["required_status_checks"] = {"strict": payload["strict"], "checks": payload["checks"],
                "contexts": [c["context"] for c in payload["checks"]]}
            return copy.deepcopy(self.protection["required_status_checks"])
        if path.startswith("compare/"):
            return {"behind_by": self.behind}
        if path == "pulls/7/merge":
            if self.fail_merge:
                raise RuntimeError(self.fail_merge if isinstance(self.fail_merge, str) else "GitHub protection rejected merge")
            self.pr.update(merged=True,merge_commit_sha=NEW,state="closed")
            self.main=NEW
            return {"merged": True, "sha": NEW}
        if path == "issues/1/comments":
            return {"id": 99}
        return {}

    def agent(self, record):
        return {"status": self.run_status, "run_id": self.run_id, "git": {}}

    def run(self, record, run_id):
        # Serves the latest run and any run added to self.runs by ID; others are absent.
        self.calls.append(("GET", "/agents/" + record["agent_id"] + "/runs/" + run_id, None))
        if run_id == self.run_id:
            return {"status": self.run_status, "run_id": run_id, "git": {}, "updated_at": None, "created_at": None}
        if run_id in self.runs:
            return dict(self.runs[run_id], run_id=run_id)
        raise urllib.error.HTTPError("https://api.cursor.com/v1/agents/" + record["agent_id"] + "/runs/" + run_id,
            404, "missing run", {}, None)

    def cursor(self, method, path, body=None):
        self.calls.append((method, path, body))
        if self.fail_post:
            raise TimeoutError("ambiguous delivery")
        count = len([c for c in self.calls if c[0] == "POST" and c[1].endswith("/runs")])
        return {"run": {"id": "run-new" + ("-" + str(count) if count > 1 else "")}, "agent": {"id": "bc-one"}}

    def checks(self, head):
        return copy.deepcopy(self.check_rows)

    def changed_files(self, number):
        return []

    def review(self, check):
        return copy.deepcopy(self.report), copy.deepcopy(self.context)

    def test_failures(self, checks):
        return [{"name": c["name"]} for c in checks.values() if c["conclusion"] != "success"]

    def infrastructure_failures(self, failures):
        return {f["name"]: self.infra[f["name"]] for f in failures if f["name"] in self.infra}


class ControllerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.remote = FakeRemote()
        self.controller = Controller(self.remote, {"tasks": [SPEC], "max_repairs": 3, "max_implementation_agents": 4}, self.temp.name)
        self.record = {"agent_id": "bc-one", "expected_branch": "cursor/one", "submitted": True, "pr": 7, "repair_count": 2}
        self.controller.tasks["one"] = self.record

    def process(self):
        return self.controller.process(SPEC, self.record, self.remote.agent(self.record), BASE)

    def posts(self):
        return [c for c in self.remote.calls if c[0] == "POST" and c[1].endswith("/runs")]

    def failed_review(self):
        self.remote.report.update(verdict="fail", findings=[{"detail": "concrete defect"}])
        self.remote.check_rows["Codex checkpoint"]["conclusion"] = "failure"

    def assert_ci_ownership(self, prompt):
        self.assertEqual(prompt.count("The controller and integration lead own GitHub CI monitoring, review feedback, and future dispatch."), 1)
        self.assertIn("Do not create background CI subscriptions (including github:ci:branch) or CI-wait continuation events", prompt)
        self.assertIn("do not start asynchronous background waits", prompt)
        self.assertIn("Read-only synchronous status checks are allowed", prompt)
        self.assertIn("exact pushed commit SHA, local test commands and results, and links to pending CI checks", prompt)
        self.assertIn("Report pending CI honestly, then finish this run", prompt)
        self.assertIn("the controller will monitor CI and dispatch any further correction", prompt)
        self.assertIn("Do not start another run or subscription to continue waiting", prompt)

    def test_launch_prompt_assigns_ci_monitoring_to_controller(self):
        self.controller.tasks.clear()
        self.controller.tick()
        launches = [c for c in self.remote.calls if c[0] == "POST" and c[1] == "/agents"]
        self.assertEqual(len(launches), 1)
        self.assert_ci_ownership(launches[0][2]["prompt"]["text"])
        self.assertIn("Do not merge, push main, force-push", launches[0][2]["prompt"]["text"])

    def test_launch_prompt_names_modules_prd_only_for_module_tasks(self):
        for spec, expected in ((SPEC, False), (dict(SPEC, module_prd_sections=["4"]), True)):
            with self.subTest(module_task=expected):
                self.setUp()
                self.controller.plan["tasks"] = [spec]
                self.controller.tasks.clear()
                self.controller.tick()
                launches = [c for c in self.remote.calls if c[0] == "POST" and c[1] == "/agents"]
                self.assertEqual(len(launches), 1)
                text = launches[0][2]["prompt"]["text"]
                self.assertEqual("also read the full togavsu-initial-modules-prd.md" in text, expected)
                self.assert_ci_ownership(text)

    def test_initial_and_repeated_correction_prompts_keep_ci_ownership(self):
        self.failed_review()
        with self.advance(0):
            self.process()
        self.finish_correction()
        with self.advance(5):
            self.process()
        self.assertEqual(len(self.posts()), 2)
        for post in self.posts():
            self.assert_ci_ownership(post[2]["prompt"]["text"])
        self.assertIn("Diagnose why the prior approach", self.posts()[1][2]["prompt"]["text"])

    def test_conflict_followup_prompt_keeps_ci_ownership(self):
        self.remote.behind = 1
        self.remote.pr["mergeable"] = False
        self.process()
        self.assertEqual(len(self.posts()), 1)
        self.assert_ci_ownership(self.posts()[0][2]["prompt"]["text"])
        self.assertIn("resolve integration conflicts", self.posts()[0][2]["prompt"]["text"])
        self.assertEqual(self.record["sync_count"], 1)
        self.assertEqual(self.record["repair_count"], 2)

    def test_failed_review_dispatches_exactly_once(self):
        self.failed_review()
        self.process()
        self.process()
        self.assertEqual(len(self.posts()), 1)
        self.assertEqual(self.record["repair_count"], 3)

    def test_active_agent_never_gets_duplicate_prompt(self):
        self.failed_review()
        self.remote.run_status = "RUNNING"
        self.process()
        self.assertFalse(self.posts())

    def test_lifetime_repair_total_does_not_stop_new_work(self):
        self.failed_review()
        self.record["repair_count"] = 38
        self.process()
        self.assertNotIn("halted", self.record)
        self.assertEqual(len(self.posts()), 1)
        self.assertEqual(self.record["repair_count"], 39)
        self.assertEqual(self.record["repair_window"]["attempts"], 1)

    def test_ambiguous_delivery_is_durable_and_not_replayed(self):
        self.failed_review()
        self.remote.fail_post = True
        with self.assertRaises(TimeoutError):
            self.process()
        self.assertTrue(self.record["intent"])
        self.process()
        self.assertEqual(len(self.posts()), 1)
        saved = json.loads((Path(self.temp.name) / "controller.json").read_text())
        self.assertTrue(saved["tasks"]["one"]["intent"])

    def test_ambiguous_delivery_requires_verified_acknowledgement_without_replay(self):
        self.failed_review()
        self.remote.fail_post = True
        with self.assertRaises(TimeoutError):
            self.process()
        self.remote.run_id, self.remote.run_status = "run-arrived", "RUNNING"
        self.process()
        self.assertIn("intent", self.record)
        self.assertEqual(self.record["repair_count"], 2)
        # Simulate the integration lead supplying independently verified delivery,
        # not the controller inferring it from a changed latestRunId.
        self.controller.acknowledge(self.record, "run-arrived")
        self.process()
        self.assertEqual(self.record["repair_count"], 3)
        self.assertNotIn("intent", self.record)
        self.assertEqual(len(self.posts()), 1)

    def test_pass_merges_only_named_head(self):
        self.assertTrue(self.process())
        merge = [c for c in self.remote.calls if c[1] == "pulls/7/merge"]
        self.assertEqual(merge[0][2]["sha"], HEAD)
        self.assertEqual(self.record["completed"]["merge_sha"], NEW)

    def test_admin_enforcement_off_halts_until_an_operator_adopts_it(self):
        self.remote.protection["enforce_admins"] = {"enabled": False}
        self.assertFalse(self.process())
        self.assertEqual(self.record["halted"], build_controller.PROTECTION_HALT)
        self.assertFalse(any(c[1].endswith("/merge") for c in self.remote.calls))
        self.controller.adopt_protection("Integration lead")
        self.assertNotIn("halted", self.record)
        entry = self.controller.state["adopted_protection"]
        self.assertEqual(entry["operator"], "Integration lead")
        self.assertIs(entry["enforce_admins"], False)
        self.assertEqual(entry["cleared_halts"], ["one"])
        self.assertEqual(self.controller.state["protection_adoptions"], [entry])
        backup = json.loads((Path(self.temp.name) / entry["backup"]).read_text())
        self.assertEqual(backup["tasks"]["one"]["halted"], build_controller.PROTECTION_HALT)
        self.assertEqual(self.record["recovered_halts"][-1]["operator"], "Integration lead")
        self.assertTrue(self.process())
        self.assertEqual(self.record["completed"]["merge_sha"], NEW)

    def test_adopted_protection_still_requires_checks_and_reviews(self):
        self.remote.protection["enforce_admins"] = {"enabled": False}
        self.controller.adopt_protection("Integration lead")
        for change in ({"required_pull_request_reviews": None},
                       {"required_status_checks": {"strict": False, "contexts": ["Setup checks", "Codex checkpoint"]}},
                       {"required_status_checks": {"strict": True, "contexts": ["Setup checks"]}}):
            with self.subTest(change=change):
                saved = copy.deepcopy(self.remote.protection)
                self.remote.protection.update(change)
                self.record.pop("halted", None)
                self.assertFalse(self.process())
                self.assertEqual(self.record["halted"], build_controller.PROTECTION_HALT)
                self.remote.protection = saved
        self.assertFalse(any(c[1].endswith("/merge") for c in self.remote.calls))

    def test_adoption_refuses_protection_without_checkpoints_and_needs_an_operator(self):
        self.record["halted"] = build_controller.PROTECTION_HALT
        self.remote.protection["required_pull_request_reviews"] = None
        with self.assertRaises(ValueError):
            self.controller.adopt_protection("Integration lead")
        self.remote.protection["required_pull_request_reviews"] = {}
        for operator in ("", "  ", None):
            with self.assertRaises(ValueError):
                self.controller.adopt_protection(operator)
        self.assertEqual(self.record["halted"], build_controller.PROTECTION_HALT)
        self.assertNotIn("adopted_protection", self.controller.state)

    def test_adoption_clears_only_protection_halts(self):
        self.record["halted"] = "Delivery outcome uncertain; no automatic replay"
        self.controller.adopt_protection("Integration lead")
        self.assertEqual(self.record["halted"], "Delivery outcome uncertain; no automatic replay")
        self.assertEqual(self.controller.state["adopted_protection"]["cleared_halts"], [])

    def test_adopt_protection_cli_requires_an_operator(self):
        plan_path = Path(self.temp.name) / "plan.json"
        plan_path.write_text(json.dumps({"tasks": [SPEC]}))
        argv = ["build_controller.py", "--plan", str(plan_path), "--state", str(Path(self.temp.name) / "state"),
                "--adopt-protection"]
        with patch.object(sys, "argv", argv), self.assertRaises(SystemExit):
            build_controller.main()
        with patch.object(sys, "argv", argv + ["--operator", "Integration lead"]), \
                patch.object(build_controller.Controller, "adopt_protection") as adopt:
            build_controller.main()
        adopt.assert_called_once_with("Integration lead")

    def test_github_rejection_never_records_completion(self):
        self.remote.fail_merge = True
        with self.assertRaises(RuntimeError):
            self.process()
        self.assertNotIn("completed", self.record)

    def test_main_change_before_merge_prevents_merge(self):
        self.remote.main = NEW
        self.assertFalse(self.process())
        self.assertFalse(any(c[1].endswith("/merge") for c in self.remote.calls))

    def test_missing_or_running_checks_block_merge(self):
        del self.remote.check_rows["Setup checks"]
        self.assertFalse(self.process())
        self.assertFalse(any(c[1].endswith("/merge") for c in self.remote.calls))
        self.remote.check_rows["Setup checks"] = check("Setup checks")
        self.remote.check_rows["Setup checks"]["status"] = "in_progress"
        self.assertFalse(self.process())

    def test_failing_product_ci_dispatches_repair_even_if_review_passes(self):
        self.remote.check_rows["Core tests"] = check("Core tests", "failure")
        self.process()
        self.assertEqual(len(self.posts()), 1)
        self.assertNotIn("completed", self.record)

    def test_base_refresh_is_not_a_code_repair(self):
        self.remote.behind = 1
        self.process()
        self.process()
        self.assertEqual(len([c for c in self.remote.calls if c[0] == "PUT"]), 1)
        self.assertEqual(self.record["repair_count"], 2)

    def test_conflicts_return_to_worker(self):
        self.remote.behind = 1
        self.remote.pr["mergeable"] = False
        self.process()
        self.assertEqual(len(self.posts()), 1)
        self.assertEqual(self.record["repair_count"], 2)
        self.assertEqual(self.record["sync_count"], 1)

    def test_git_conflict_after_three_code_repairs_can_still_integrate(self):
        self.record["repair_count"] = 3
        self.remote.behind = 1
        self.remote.pr["mergeable"] = False
        self.process()
        self.assertEqual(len(self.posts()), 1)
        self.assertEqual(self.record["repair_count"], 3)

    def test_git_sync_budget_is_also_bounded(self):
        self.record["sync_count"] = 3
        self.remote.behind = 1
        self.remote.pr["mergeable"] = False
        self.process()
        self.assertFalse(self.posts())
        self.assertTrue(self.record["halted"])

    def test_unavailable_review_has_one_retry_across_check_id_changes(self):
        self.remote.context["base"] = NEW
        self.process()
        self.remote.check_rows["Codex checkpoint"]["id"] = 99
        self.process()
        retries = [c for c in self.remote.calls if c[1].endswith("/rerun")]
        self.assertEqual(len(retries), 1)
        self.assertIn("unavailable", self.record["halted"])

    def test_pause_file_prevents_all_remote_operations(self):
        (Path(self.temp.name) / "STOP").touch()
        self.controller.tick()
        self.assertFalse(self.remote.calls)

    def test_unmerged_dependency_does_not_launch(self):
        self.controller.plan["tasks"] = [SPEC, {"id": "two", "depends_on": ["one"]}]
        self.remote.run_status = "RUNNING"
        self.controller.tick()
        self.assertNotIn("two", self.controller.tasks)

    def test_ready_dependency_launches_from_current_main(self):
        self.record["completed"] = {"merge_sha": BASE}
        self.controller.plan["tasks"] = [SPEC, {"id": "two", "depends_on": ["one"]}]
        self.controller.tick()
        launches = [c for c in self.remote.calls if c[1] == "/agents"]
        self.assertEqual(launches[0][2]["repos"][0]["startingRef"], BASE)

    def test_held_task_waits_while_its_sibling_launches(self):
        self.record["completed"] = {"merge_sha": BASE}
        self.controller.plan["tasks"] = [SPEC, {"id": "two", "depends_on": ["one"]}, {"id": "three", "depends_on": ["one"]}]
        self.controller.plan["held_tasks"] = ["three"]
        self.controller.tick()
        self.assertIn("two", self.controller.tasks)
        self.assertNotIn("three", self.controller.tasks)

    def test_recovered_merge_refreshes_main_before_dependent_launch(self):
        for response_recorded in (False, True):
            with self.subTest(response_recorded=response_recorded):
                self.setUp()
                self.controller.plan["tasks"] = [SPEC, {"id": "two", "depends_on": ["one"]}]
                self.record["merge_intent"] = {"head": HEAD, "base": BASE}
                if response_recorded:
                    self.record["merge_intent"]["result_sha"] = NEW
                self.remote.pr.update(merged=True, merge_commit_sha=NEW, state="closed")
                original = self.remote.gh
                def gh(path, method="GET", payload=None, **kwargs):
                    if path == "pulls/7":
                        self.remote.main = NEW  # Merge becomes visible after tick read BASE.
                    return original(path, method, payload, **kwargs)
                self.remote.gh = gh
                self.controller.tick()
                self.assertIn("completed", self.record)
                self.assertNotIn("two", self.controller.tasks)
                self.controller.tick()
                launches = [c for c in self.remote.calls if c[1] == "/agents"]
                self.assertEqual(len(launches), 1)
                self.assertEqual(launches[0][2]["repos"][0]["startingRef"], NEW)

    def test_dry_run_does_not_send_or_persist(self):
        self.failed_review()
        self.controller.dry = True
        self.process()
        self.assertFalse(self.posts())
        self.assertFalse((Path(self.temp.name) / "controller.json").exists())

    def test_full_pool_defers_finished_workers_correction_even_when_observed_first(self):
        self.failed_review()
        for i in range(4):
            task = "active-" + str(i)
            self.controller.plan["tasks"].append({"id": task, "depends_on": []})
            self.controller.tasks[task] = {"agent_id": task, "submitted": True}
        original = self.remote.agent
        self.remote.agent = lambda r: original(r) if r["agent_id"] == "bc-one" else {"status": "RUNNING", "run_id": "active", "git": {}}
        self.controller.tick()
        self.assertFalse(self.posts())
        self.assertIn("slots occupied", self.record["waiting"])

    def test_finished_correction_with_unchanged_failed_head_backs_off(self):
        self.failed_review()
        self.process()
        self.remote.run_id = "run-new"
        self.process()
        self.assertEqual(len(self.posts()), 1)
        self.assertIn("backoff", self.record["waiting"])
        self.assertNotIn("halted", self.record)

    def restart(self):
        self.controller.save()
        self.controller = Controller(self.remote, self.controller.plan, self.temp.name)
        self.record = self.controller.tasks["one"]

    def advance(self, minutes):
        return patch("build_controller.now", return_value=(dt.datetime(2026, 11, 8,
            tzinfo=dt.timezone.utc) + dt.timedelta(minutes=minutes)).isoformat())

    def finish_correction(self, revision=None):
        self.remote.run_id = self.record["last_dispatched_run"]
        self.remote.run_status = "FINISHED"
        if revision:
            self.remote.pr["head"]["sha"] = revision
            self.remote.context["head"] = revision
            self.remote.report["reviewed_head"] = revision

    def test_three_repeated_problems_back_off_then_surface_stall_across_restarts(self):
        self.failed_review()
        for minutes, total in [(0, 1), (4, 1), (5, 2), (14, 2), (15, 3), (30, 3)]:
            with self.advance(minutes):
                self.process()
                self.assertEqual(len(self.posts()), total)
                self.finish_correction()
                self.restart()
        self.assertEqual(self.record["stalled"]["kind"], "no_progress")
        self.assertEqual(self.record["repair_count"], 5)
        self.assertEqual(len(set(c[2]["prompt"]["text"] for c in self.posts())), 2)
        self.assertEqual(len(set(self.record["delivered"])), 3)
        self.assertNotIn("completed", self.record)

    def test_unreachable_ci_server_reruns_once_then_holds_without_a_fix_pass(self):
        self.remote.check_rows["QA bootstrap tests"] = check("QA bootstrap tests", "failure")
        self.remote.infra = {"QA bootstrap tests": ("123", "Temporary failure resolving 'archive.ubuntu.com'")}
        reruns = lambda: [c for c in self.remote.calls if c[1] == "actions/runs/123/rerun-failed-jobs"]
        self.process()
        self.assertEqual(self.posts(), [])
        self.assertEqual(len(reruns()), 1)
        self.assertNotIn("stalled", self.record)
        self.restart()
        self.process()
        self.assertEqual(self.posts(), [])
        self.assertEqual(len(reruns()), 1)
        self.assertEqual(self.record["stalled"]["kind"], "infrastructure")
        self.assertIn("archive.ubuntu.com", self.record["stalled"]["reason"])
        self.assertTrue(any(k.startswith("stalled:one:infrastructure") for k in self.controller.alert_conditions()))
        self.remote.check_rows["QA bootstrap tests"]["conclusion"] = "success"
        self.remote.infra = {}
        self.process()
        self.assertNotIn("infra_hold", self.record)
        self.assertNotEqual(self.record.get("stalled", {}).get("kind"), "infrastructure")
        self.assertEqual(self.posts(), [])

    def test_a_code_failure_beside_an_outage_gets_a_fix_pass_for_the_code_failure_only(self):
        self.remote.check_rows["Core tests"] = check("Core tests", "failure")
        self.remote.check_rows["QA bootstrap tests"] = check("QA bootstrap tests", "failure")
        self.remote.infra = {"QA bootstrap tests": ("123", "Temporary failure resolving 'archive.ubuntu.com'")}
        self.process()
        self.assertEqual(len(self.posts()), 1)
        prompt = self.posts()[0][2]["prompt"]["text"]
        failures = json.loads(prompt.split("Failures: ", 1)[1].split(" These checks", 1)[0])
        self.assertEqual([f["name"] for f in failures], ["Core tests"])
        self.assertIn("not yours to fix and will rerun: QA bootstrap tests.", prompt)
        self.assertNotIn("stalled", self.record)
        self.assertNotIn("infra_hold", self.record)
        self.assertIn(problem_fingerprints({"failed_checks": ["Core tests"]})[0], self.record["repair_problems"])

    def test_code_failures_beside_no_infrastructure_still_get_a_fix_pass(self):
        self.remote.check_rows["Core tests"] = check("Core tests", "failure")
        self.process()
        self.assertEqual(len(self.posts()), 1)
        self.assertNotIn("infra_hold", self.record)

    def test_infrastructure_failures_read_each_failed_job_log(self):
        logs = {"actions/jobs/456/logs": b"x\n2026-11-11T13:50:43Z   Temporary failure resolving 'archive.ubuntu.com'\ny\n",
                "actions/jobs/789/logs": b"--- FAIL: TestStall\n    context deadline exceeded\n",
                "actions/jobs/790/logs": b"2026-11-11T13:51:30Z W: Failed to fetch http://x/InRelease"}
        remote = Remote()
        remote.gh = lambda path, method="GET", payload=None, binary=False: logs[path]
        url = "https://github.com/CharitonMedia/togavsu/actions/runs/{}/job/{}"
        found = remote.infrastructure_failures([{"name": "QA", "url": url.format(123, 456)},
            {"name": "Core", "url": url.format(124, 789)}, {"name": "Apt", "url": url.format(125, 790)},
            {"name": "Unlinked", "url": None}])
        self.assertEqual(sorted(found), ["Apt", "QA"])
        self.assertEqual(found["QA"], ("123", "2026-11-11T13:50:43Z   Temporary failure resolving 'archive.ubuntu.com'"))
        self.assertEqual(found["Apt"][0], "125")

    def test_fix_pass_limit_holds_after_three_then_resume_grants_three_more(self):
        self.controller.plan["max_corrections_per_assignment"] = 3
        self.failed_review()
        self.remote.cursor = self.unique_run_post
        for i in range(5):
            self.remote.report["findings"] = [{"file": "internal/a.go", "detail": "defect " + str(i)}]
            if i:
                self.finish_correction(format(i, "040x"))
            with self.advance(i * 30):
                self.process()
            self.restart()
        self.assertEqual(len(self.posts()), 3)
        self.assertEqual(self.record["stalled"]["kind"], "correction_ceiling")
        self.assertIn("3 fix passes", self.record["stalled"]["reason"])
        self.controller.resume("one", "Patrick")
        self.assertEqual(self.record["correction_base"], 3)
        self.remote.report["findings"] = [{"file": "internal/a.go", "detail": "defect 9"}]
        self.finish_correction(format(9, "040x"))
        with self.advance(300):
            self.process()
        self.assertEqual(len(self.posts()), 4)
        self.assertNotIn("stalled", self.record)

    def test_new_sha_and_run_ids_do_not_reset_identical_findings_budget(self):
        self.failed_review()
        for i in range(4):
            with self.advance(i * 30):
                self.remote.cursor = lambda *args, **kwargs: self.unique_run_post(*args, **kwargs)
                self.process()
                self.finish_correction(str(i + 1) * 40)
        self.assertEqual(len(self.posts()), 3)
        self.assertEqual(self.record["stalled"]["kind"], "no_progress")

    def unique_run_post(self, method, path, body=None):
        self.remote.calls.append((method, path, body))
        return {"run": {"id": "run-" + str(len(self.posts()))}}

    def test_new_findings_continue_after_repeated_problem_stall(self):
        self.failed_review()
        for i in range(4):
            with self.advance(i * 30):
                self.process()
                self.finish_correction()
        self.assertIn("stalled", self.record)
        self.remote.report["findings"] = [{"detail": "Different independently reviewed defect"}]
        self.finish_correction(NEW)
        with self.advance(91):
            self.process()
        self.assertEqual(len(self.posts()), 4)
        self.assertEqual(self.record["repair_window"]["attempts"], 1)
        self.assertNotIn("stalled", self.record)
        self.assertEqual(self.record["repair_count"], 6)

    def test_alternating_findings_do_not_reset_previously_consumed_budget(self):
        self.failed_review()
        for i, defect in enumerate(("A", "B", "A", "B", "A", "B", "A")):
            with self.advance(i * 30):
                self.remote.report["findings"] = [{"detail": defect}]
                self.process()
                self.finish_correction(format(i + 1, "040x"))
                self.restart()
        self.assertEqual(len(self.posts()), 6)
        self.assertEqual(self.record["repair_window"]["attempts"], 3)
        self.assertEqual(self.record["stalled"]["kind"], "no_progress")
        self.assertEqual(len(self.record["repair_problems"]), 2)

    def test_mixed_finding_sets_preserve_each_budget_and_backoff_across_restarts(self):
        self.failed_review()
        for minute, defects, total in ((0, ["A"], 1), (4, ["A", "B"], 1), (5, ["A", "B"], 2),
                (14, ["A", "C"], 2), (15, ["A", "C"], 3), (30, ["A", "D"], 3)):
            with self.advance(minute):
                self.remote.report["findings"] = [{"detail": defect} for defect in defects]
                self.process()
                self.assertEqual(len(self.posts()), total)
                self.finish_correction(format(minute + 1, "040x"))
                self.restart()
        for defect, attempts in (("A", 3), ("B", 1), ("C", 1), ("D", 0)):
            fingerprint = problem_fingerprints(review_problem([{"detail": defect}]))[0]
            self.assertEqual(self.record["repair_problems"][fingerprint]["attempts"], attempts)
        self.assertEqual(self.record["stalled"]["kind"], "no_progress")
        self.assertEqual(self.record["repair_count"], 5)

    def test_mixed_failed_check_sets_preserve_persistent_check_budget(self):
        for i, companion in enumerate((None, "QA harness tests", "API contract tests", "Supervisor tests")):
            with self.advance(i * 30):
                self.remote.check_rows = {name: check(name) for name in ("Setup checks", "Codex checkpoint")}
                self.remote.check_rows["Core tests"] = check("Core tests", "failure")
                if companion:
                    self.remote.check_rows[companion] = check(companion, "failure")
                self.process()
                self.finish_correction(format(i + 1, "040x"))
                self.restart()
        fingerprint = problem_fingerprints({"failed_checks": ["Core tests"]})[0]
        self.assertEqual(self.record["repair_problems"][fingerprint]["attempts"], 3)
        self.assertEqual(len(self.posts()), 3)
        self.assertEqual(self.record["stalled"]["kind"], "no_progress")

    def test_mixed_missing_check_sets_preserve_persistent_check_budget(self):
        names = ("A", "B", "C", "D")
        spec = dict(SPEC, required_checks=list(names))
        for i, missing in enumerate(({"A"}, {"A", "B"}, {"A", "C"}, {"A", "D"})):
            with self.advance(i * 30):
                self.remote.check_rows = {name: check(name) for name in ("Setup checks", "Codex checkpoint") + names
                    if name not in missing}
                self.record["check_wait"] = {"head": HEAD, "since": "2026-11-07T22:00:00+00:00"}
                self.controller.process(spec, self.record, self.remote.agent(self.record), BASE)
                self.finish_correction()
                self.restart()
        fingerprint = problem_fingerprints({"missing_checks": ["A"]})[0]
        self.assertEqual(self.record["repair_problems"][fingerprint]["attempts"], 3)
        self.assertEqual(len(self.posts()), 3)
        self.assertEqual(self.record["stalled"]["kind"], "no_progress")

    def test_mixed_set_waits_for_latest_individual_backoff_deadline(self):
        self.failed_review()
        for minute, defects, total in ((0, ["A"], 1), (5, ["A"], 2), (20, ["B"], 3),
                (21, ["A", "B"], 3), (25, ["A", "B"], 4)):
            with self.advance(minute):
                self.remote.report["findings"] = [{"detail": defect} for defect in defects]
                self.process()
                self.assertEqual(len(self.posts()), total)
                if minute == 21:
                    self.assertIn("00:25:00", self.record["waiting"])
                self.finish_correction(format(minute + 1, "040x"))
                self.restart()

    def test_mixed_ambiguous_dispatch_increments_each_distinct_problem_once(self):
        self.failed_review()
        self.remote.report["findings"] = [{"detail": "A"}, {"detail": "A"}, {"detail": "B"}]
        self.remote.fail_post = True
        with self.advance(0):
            with self.assertRaises(TimeoutError):
                self.process()
            self.assertEqual(len(self.record["intent"]["problems"]), 2)
            self.restart()
            self.process()
            self.assertTrue(all(w["attempts"] == 0 for w in self.record["repair_problems"].values()))
            self.remote.run_id, self.remote.run_status = "arrived", "RUNNING"
            self.process()
            self.assertIn("intent", self.record)
            self.assertTrue(all(w["attempts"] == 0 for w in self.record["repair_problems"].values()))
            self.controller.acknowledge(self.record, "arrived")  # Independently verified delivery.
            self.restart()
            self.process()
        self.assertTrue(all(w["attempts"] == 1 for w in self.record["repair_problems"].values()))
        self.assertEqual(self.record["repair_count"], 3)
        self.assertEqual(len(self.posts()), 1)

    def test_review_metadata_and_order_changes_do_not_reset_budget(self):
        self.failed_review()
        findings = [{"file": "one.go", "detail": "Same defect", "priority": "P1"},
                    {"file": "two.go", "detail": "Another defect", "priority": "P2"}]
        for i in range(4):
            with self.advance(i * 30):
                self.remote.report["findings"] = copy.deepcopy(findings[::1 if i % 2 else -1])
                for finding in self.remote.report["findings"]:
                    finding["priority"] = "P0" if i % 2 else "P2"
                self.remote.report["summary"] = "Summary " + str(i)
                self.remote.pr["body"] = self.remote.context["body"] = "Evidence " + str(i)
                self.remote.check_rows["Codex checkpoint"]["id"] = i + 1
                self.process()
                self.finish_correction(format(i + 1, "040x"))
                self.restart()
        self.assertEqual(len(self.posts()), 3)
        self.assertEqual(len(self.record["repair_problems"]), 2)
        self.assertIn("stalled", self.record)

    def test_line_locations_do_not_reset_same_defect_budget(self):
        self.failed_review()
        for i in range(4):
            with self.advance(i * 30):
                file = "internal/api.go:" + str(20 + i)
                self.remote.report["findings"] = [{"file": file,
                    "detail": "Missing authorization in " + file + " (line " + str(20 + i) + ")"}]
                self.process()
                self.finish_correction(format(i + 1, "040x"))
                self.restart()
        self.assertEqual(len(self.posts()), 3)
        self.assertEqual(len(self.record["repair_problems"]), 1)
        self.assertIn("stalled", self.record)

    def test_location_normalization_preserves_defect_numbers_and_file_identity(self):
        first = [{"file": "api.go:12:3", "detail": "Wrong HTTP 401 (lines 12-14)"}]
        moved = [{"file": "api.go#L40-L42", "detail": "Wrong HTTP 401 (line 40)"}]
        self.assertEqual(review_problem(first), review_problem(moved))
        moved[0]["detail"] = "Wrong HTTP 403 (line 40)"
        self.assertNotEqual(review_problem(first), review_problem(moved))
        moved[0] = {"file": "other.go:12", "detail": first[0]["detail"]}
        self.assertNotEqual(review_problem(first), review_problem(moved))

    def test_inline_paths_and_full_line_column_ranges_normalize(self):
        first = [{"file": "internal/api.go", "detail": "At internal/api.go:20, missing HTTP 401 check"}]
        moved = [{"file": "internal/api.go:21:5-23:8", "detail": "At internal/api.go:21:5-23:8, missing HTTP 401 check"}]
        self.assertEqual(review_problem(first), review_problem(moved))
        moved[0] = {"file": "internal/api.go#L45-L46", "detail": "At internal/api.go#L45-L46, missing HTTP 401 check"}
        self.assertEqual(review_problem(first), review_problem(moved))
        moved[0]["detail"] = "At internal/api.go:22, missing HTTP 403 check"
        self.assertNotEqual(review_problem(first), review_problem(moved))

    def test_many_distinct_submissions_are_not_lifetime_limited(self):
        self.failed_review()
        self.record["repair_count"] = 38
        for i in range(8):
            self.remote.report["findings"] = [{"detail": "Defect " + str(i)}]
            self.finish_correction(format(i + 1, "040x")) if i else None
            with self.advance(i):
                self.process()
                self.restart()
        self.assertEqual(len(self.posts()), 8)
        self.assertEqual(self.record["repair_count"], 46)
        self.assertNotIn("halted", self.record)

    def test_consecutive_fixed_key_repeats_stay_bounded_despite_new_run_ids(self):
        for case in ("interrupted", "missing-pr", "draft"):
            with self.subTest(case=case):
                self.setUp()
                if case == "interrupted":
                    self.remote.run_status = "ERROR"
                elif case == "missing-pr":
                    self.record.pop("pr")
                else:
                    self.remote.pr["draft"] = True
                self.remote.cursor = self.unique_run_post
                for i in range(5):
                    with self.advance(i * 30):
                        self.process()
                        self.remote.run_id = self.record["last_dispatched_run"]
                self.assertEqual(len(self.posts()), 3)
                self.assertEqual(self.record["stalled"]["kind"], "no_progress")

    def test_fixed_key_budget_resets_after_progress(self):
        for case in ("interrupted", "draft"):
            with self.subTest(case=case):
                self.setUp()
                self.remote.cursor = self.unique_run_post
                if case == "interrupted":
                    self.remote.run_status = "ERROR"
                else:
                    self.remote.pr["draft"] = True
                for i in range(2):
                    with self.advance(i * 30):
                        self.process()
                    self.remote.run_id = self.record["last_dispatched_run"]
                self.assertEqual(self.record["repair_window"]["attempts"], 2)
                if case == "interrupted":
                    # A finished run with its PR is a different state; the next interruption starts a new streak.
                    self.hold_review()
                    self.remote.run_status = "FINISHED"
                    with self.advance(60):
                        self.process()
                    self.hold_review(False)
                    self.remote.run_id, self.remote.run_status = "later-run", "ERROR"
                else:
                    self.remote.pr["head"]["sha"] = NEW  # New head, still draft.
                for i in range(3):
                    self.restart()  # Fresh tick capacity; direct process() calls never release slots.
                    with self.advance(90 + i * 30):
                        self.process()
                    self.remote.run_id = self.record["last_dispatched_run"]
                    if i == 0:
                        self.assertEqual(self.record["repair_window"]["attempts"], 1)
                    self.assertNotIn("stalled", self.record)
                self.assertEqual(len(self.posts()), 5)
                self.restart()
                with self.advance(200):
                    self.process()
                self.assertEqual(len(self.posts()), 5)
                self.assertEqual(self.record["stalled"]["kind"], "no_progress")

    def test_reworded_findings_surface_attention_then_hold_at_correction_ceiling(self):
        for soft, hard in ((3, 5), (None, None)):
            with self.subTest(soft=soft, hard=hard):
                self.setUp()
                if soft:
                    self.controller.plan.update(correction_attention_threshold=soft, max_corrections_per_assignment=hard)
                soft, hard = soft or 10, hard or 30
                self.failed_review()
                self.remote.cursor = self.unique_run_post
                for i in range(hard + 2):
                    # The same unresolved defect, reworded on each new head.
                    self.remote.report["findings"] = [{"file": "internal/trust/ledger.go",
                        "detail": "Ledger reversal still drops the memo (wording " + str(i) + ")"}]
                    if i:
                        self.finish_correction(format(i, "040x"))
                    with self.advance(i):
                        self.controller.tick()
                    self.restart()
                    volume = self.record.get("correction_volume", 0)
                    self.assertEqual(volume, min(i + 1, hard))
                    self.assertEqual(len(self.posts()), min(i + 1, hard))
                    status = json.loads((Path(self.temp.name) / "status.json").read_text())
                    self.assertEqual(status["tasks"]["one"]["correction_volume"], volume)
                    if volume < soft:
                        self.assertEqual(status["health"], "observing")
                        self.assertNotIn("stalled", self.record)
                    elif i < hard:
                        self.assertEqual(status["health"], "needs_attention")
                        self.assertEqual(self.record["stalled"]["kind"], "high_correction_volume")
                        self.assertNotIn("halted", self.record)
                    else:
                        self.assertEqual(status["health"], "needs_attention")
                        self.assertEqual(self.record["stalled"]["kind"], "correction_ceiling")
                        self.assertNotIn("halted", self.record)
                self.assertNotEqual(self.record.get("halted"), LEGACY_REPAIR_HALT)
                # The hold stops corrections only; a passing exact-revision review still merges.
                self.remote.report.update(verdict="pass", findings=[])
                self.remote.check_rows["Codex checkpoint"]["conclusion"] = "success"
                self.finish_correction(NEW)
                with self.advance(hard + 5):
                    self.controller.tick()
                self.assertIn("completed", self.record)
                self.assertEqual(len(self.posts()), hard)

    def test_repeated_ci_failure_is_bounded_across_heads(self):
        self.remote.check_rows["Core tests"] = check("Core tests", "failure")
        self.remote.cursor = self.unique_run_post
        for i in range(4):
            with self.advance(i * 30):
                self.process()
                self.finish_correction(format(i + 1, "040x"))
        self.assertEqual(len(self.posts()), 3)
        self.assertIn("stalled", self.record)

    def test_only_obsolete_lifetime_cap_is_recovered_and_history_survives(self):
        self.failed_review()
        self.record.update(halted=LEGACY_REPAIR_HALT, repair_count=38, delivered=["old-delivery"])
        self.process()
        self.assertNotIn("halted", self.record)
        self.assertEqual(self.record["recovered_halts"][0]["reason"], LEGACY_REPAIR_HALT)
        self.assertEqual(self.record["repair_count"], 39)
        self.assertIn("old-delivery", self.record["delivered"])
        self.restart()
        self.process()
        self.assertEqual(len(self.record["recovered_halts"]), 1)
        self.assertEqual(len(self.posts()), 1)

    def test_other_safety_halts_never_clear_for_new_revisions_or_runs(self):
        for reason in ("Worker changed protected control-plane file; requires independent control-plane review",
                "Branch protection changed or no longer enforces required checkpoints", "Assignment PR closed without merge",
                "Correction finished without changing the failing revision; no-progress stop"):
            with self.subTest(reason=reason):
                self.setUp()
                self.record["halted"] = reason
                self.remote.pr["head"]["sha"] = NEW
                self.remote.run_id = "different-run"
                self.process()
                self.assertEqual(self.record["halted"], reason)
                self.assertFalse(self.remote.calls)

    def test_legacy_delivered_correction_gets_bounded_recovery_only_after_observation(self):
        self.failed_review()
        self.record.update(delivered=["review:" + HEAD], last_dispatched_run="legacy-run")
        with self.advance(0):
            self.process()
            self.assertFalse(self.posts())
            self.remote.run_id = "legacy-run"
            self.process()
            self.assertFalse(self.posts())
        with self.advance(5):
            self.process()
        self.assertEqual(len(self.posts()), 1)
        self.assertEqual(self.record["repair_window"]["attempts"], 2)

    def test_ambiguous_recovery_keeps_budget_and_intent_across_restart(self):
        self.failed_review()
        with self.advance(0):
            self.process()
            self.finish_correction()
        self.remote.fail_post = True
        with self.advance(5):
            with self.assertRaises(TimeoutError):
                self.process()
            self.restart()
            self.process()
            self.assertEqual(len(self.posts()), 2)
            self.assertEqual(self.record["repair_window"]["attempts"], 1)
            self.assertIn("intent", self.record)
            self.remote.run_id, self.remote.run_status = "recovered-run", "RUNNING"
            self.process()
            self.assertIn("intent", self.record)
            self.assertEqual(self.record["repair_window"]["attempts"], 1)
            self.controller.acknowledge(self.record, "recovered-run")  # Independently verified delivery.
            self.process()
            self.assertEqual(self.record["repair_window"]["attempts"], 2)
            self.assertNotIn("intent", self.record)
            self.assertNotIn("halted", self.record)
            self.assertEqual(len(self.posts()), 2)
            self.restart()
            fingerprint = self.record["repair_window"]["fingerprint"]
            self.assertEqual(self.record["repair_problems"][fingerprint]["attempts"], 2)

    def test_crash_after_intent_before_post_never_blindly_replays(self):
        self.failed_review()
        original = self.controller.save
        def crash():
            original()
            if self.record.get("intent"):
                raise RuntimeError("simulated crash before POST")
        self.controller.save = crash
        with self.assertRaises(RuntimeError):
            self.process()
        self.controller.save = original
        self.restart()
        self.process()
        self.assertFalse(self.posts())
        self.assertIn("intent", self.record)
        self.assertEqual(self.record["repair_window"]["attempts"], 0)

    def test_reconciling_intent_does_not_clear_unrelated_safety_halt(self):
        self.record["intent"] = {"key": "old", "repair": True, "previous_run": "prior", "time": "2026-11-08T00:00:00+00:00"}
        self.record["halted"] = "Ownership mismatch"
        self.process()
        self.assertEqual(self.record["halted"], "Ownership mismatch")
        self.assertFalse(self.posts())

    def test_unrelated_halt_survives_predecessor_then_new_run_reconciliation(self):
        self.record["intent"] = {"key": "old", "repair": True, "previous_run": "run-old",
            "time": "2026-11-08T00:00:00+00:00"}
        self.record["halted"] = "Ownership mismatch"
        self.process()
        self.assertEqual(self.record["halted"], "Ownership mismatch")
        self.assertIn("intent", self.record)
        self.restart()
        self.remote.run_id, self.remote.run_status = "run-arrived", "RUNNING"
        self.process()
        self.assertIn("intent", self.record)
        self.controller.acknowledge(self.record, "run-arrived")  # Independently verified delivery.
        self.process()
        self.assertNotIn("intent", self.record)
        self.assertEqual(self.record["halted"], "Ownership mismatch")
        self.assertFalse(self.remote.calls)
        self.assertNotIn("completed", self.record)

    def test_three_run_identity_reserves_capacity_and_blocks_green_merge_after_restart(self):
        for legacy in (False, True):
            with self.subTest(legacy=legacy):
                self.setUp()
                self.record.update(last_dispatched_run="R3", last_dispatch_at="2026-11-08T00:00:00+00:00")
                if not legacy:
                    self.record["last_dispatch_source_run"] = "R2"
                for i in range(3):
                    task = "active-" + str(i)
                    self.controller.plan["tasks"].append({"id": task, "depends_on": []})
                    self.controller.tasks[task] = {"agent_id": task, "submitted": True}
                self.controller.plan["tasks"].append({"id": "five", "depends_on": []})
                original = self.remote.agent
                self.remote.agent = lambda r: original(r) if r["agent_id"] == "bc-one" else {
                    "status": "RUNNING", "run_id": "active", "git": {}}
                for minute, run_id in ((0, "R1"), (1, None), (2, ""), (16, "R2"), (17, "R4")):
                    self.remote.run_id = run_id
                    self.restart()
                    with self.advance(minute):
                        self.controller.tick()
                    self.assertEqual(self.controller.occupied, 4)
                    self.assertNotIn("five", self.controller.tasks)
                    self.assertNotIn("completed", self.record)
                    self.assertEqual(self.record["last_dispatched_run"], "R3")
                    self.assertFalse(any(c[1].startswith("pulls/") or c[1] == "/agents" for c in self.remote.calls))
                    self.assertFalse(self.posts())
                self.assertEqual(self.record["stalled"]["kind"], "run_observation")
                self.remote.run_id, self.remote.run_status = "R3", "RUNNING"
                self.restart()
                with self.advance(18):
                    self.controller.tick()
                self.assertNotIn("stalled", self.record)
                self.assertEqual(self.controller.occupied, 4)
                # Even after seeing R3 active, an older terminal snapshot cannot
                # prove R3 ended or release its slot on a later restart.
                self.remote.run_id, self.remote.run_status = "R1", "FINISHED"
                self.restart()
                with self.advance(19):
                    self.controller.tick()
                self.assertEqual(self.controller.occupied, 4)
                self.assertNotIn("five", self.controller.tasks)
                self.assertNotIn("completed", self.record)
                self.assertFalse(any(c[1].startswith("pulls/") for c in self.remote.calls))
                self.remote.run_id = "R3"
                self.restart()
                with self.advance(20):
                    self.controller.tick()
                self.assertIn("completed", self.record)
                self.assertFalse(self.posts())

    def observe_at(self, minute, run_id, status):
        self.remote.calls.clear()
        self.remote.run_id, self.remote.run_status = run_id, status
        self.restart()
        with self.advance(minute):
            self.controller.tick()

    def pr_reads(self):
        return [c for c in self.remote.calls if c[1].startswith("pulls/")]

    def hold_review(self, held=True):
        original = FakeRemote.review.__get__(self.remote)
        def pending(check):
            raise ReviewPending("Workflow attempt is still queued or running")
        self.remote.review = pending if held else original

    def test_newer_run_after_observed_acknowledgement_merges_green_pr_and_releases_slot(self):
        self.record.update(last_dispatched_run="R3", last_dispatch_source_run="R2",
            last_dispatch_at="2026-11-08T00:00:00+00:00")
        self.hold_review()
        self.observe_at(1, "R3", "RUNNING")
        self.assertTrue(self.record["last_dispatched_run_observed"])
        self.assertEqual(self.controller.occupied, 1)
        self.observe_at(2, "R3", "FINISHED")
        self.assertEqual(self.controller.occupied, 0)
        self.assertEqual(self.record["waiting"], "Codex workflow attempt queued or running")
        self.hold_review(False)
        self.observe_at(20, "Z", "FINISHED")
        self.assertIn("completed", self.record)
        self.assertEqual(self.controller.occupied, 0)
        self.assertNotIn("stalled", self.record)
        self.assertFalse(self.posts())

    def test_continuation_after_observed_run_is_an_active_worker_not_an_unobserved_dispatch(self):
        self.record.update(last_dispatched_run="R3", last_dispatch_source_run="R2",
            last_dispatch_at="2026-11-08T00:00:00+00:00")
        self.observe_at(1, "R3", "RUNNING")
        self.hold_review()
        self.observe_at(2, "R3", "FINISHED")
        self.assertEqual(self.controller.occupied, 0)
        self.hold_review(False)
        for minute in (16, 20):
            self.observe_at(minute, "Z", "RUNNING")
            self.assertEqual(self.controller.occupied, 1)
            self.assertEqual(self.record["waiting"], "Cursor run active; awaiting completion")
            self.assertEqual(self.record["active_watch"]["run_id"], "Z")
            self.assertNotIn("stalled", self.record)
            self.assertNotIn("dispatch_visibility_since", self.record)
            self.assertFalse(self.pr_reads())
        self.assertFalse(self.posts())

    def test_stale_or_unidentified_runs_stay_reserved_around_acknowledged_run_observation(self):
        self.record.update(last_dispatched_run="R3", last_dispatch_source_run="R2",
            last_dispatch_at="2026-11-08T00:00:00+00:00")
        for minute, run_id in ((1, "R2"), (2, "R9")):
            self.observe_at(minute, run_id, "FINISHED")
            self.assertEqual(self.controller.occupied, 1)
            self.assertNotIn("last_dispatched_run_observed", self.record)
            self.assertFalse(self.pr_reads())
        self.assertEqual(self.record["last_dispatch_stale_runs"], ["R2", "R9"])
        self.observe_at(3, "R3", "RUNNING")
        # Seeing R3 does not turn its predecessor or a run reported before it into a newer run.
        for minute, run_id, status in ((4, "R2", "FINISHED"), (5, "R9", "FINISHED"), (6, None, "RUNNING"), (7, "", "RUNNING")):
            self.observe_at(minute, run_id, status)
            self.assertEqual(self.controller.occupied, 1)
            self.assertNotIn("completed", self.record)
            self.assertFalse(self.pr_reads())
            self.assertEqual(self.record["waiting"], "Await observation of acknowledged Cursor run; no duplicate prompt")
        self.observe_at(8, "Z", "FINISHED")
        self.assertIn("completed", self.record)
        self.assertFalse(self.posts())

    def test_new_acknowledgement_after_observed_run_reserves_until_its_own_run_is_seen(self):
        self.record.update(last_dispatched_run="R3", last_dispatch_source_run="R2",
            last_dispatch_at="2026-11-08T00:00:00+00:00")
        self.hold_review()
        self.observe_at(1, "R3", "FINISHED")
        self.assertTrue(self.record["last_dispatched_run_observed"])
        self.assertNotIn("completed", self.record)
        self.hold_review(False)
        self.record["intent"] = {"key": "conflict:" + HEAD + ":" + BASE, "repair": False,
            "previous_run": "R3", "time": "2026-11-08T00:02:00+00:00"}
        self.controller.acknowledge(self.record, "R5")
        self.assertNotIn("last_dispatched_run_observed", self.record)
        self.assertNotIn("last_dispatch_stale_runs", self.record)
        for minute, run_id in ((3, "R3"), (4, "Z")):
            self.observe_at(minute, run_id, "FINISHED")
            self.assertEqual(self.controller.occupied, 1)
            self.assertNotIn("completed", self.record)
            self.assertFalse(self.pr_reads())
        self.observe_at(5, "R5", "FINISHED")
        self.assertTrue(self.record["last_dispatched_run_observed"])
        self.assertIn("completed", self.record)
        self.assertFalse(self.posts())

    def test_acknowledged_run_superseded_between_ticks_is_verified_directly_and_merges(self):
        for legacy in (False, True):
            with self.subTest(legacy=legacy):
                self.setUp()
                if legacy:
                    # Base-format record already stuck: no predecessor, dispatch time or observed flag.
                    self.record.update(last_dispatched_run="R3", dispatch_visibility_since="2026-11-07T00:00:00+00:00",
                        stalled={"kind": "run_observation", "reason": "Acknowledged Cursor run not observed within 15 minutes"})
                else:
                    self.record.update(last_dispatched_run="R3", last_dispatch_source_run="R2",
                        last_dispatch_at="2026-11-08T00:00:00+00:00")
                self.remote.runs["R3"] = {"status": "FINISHED", "git": {}}
                self.observe_at(30, "Z", "FINISHED")
                self.assertIn(("GET", "/agents/bc-one/runs/R3", None), self.remote.calls)
                self.assertTrue(self.record["last_dispatched_run_observed"])
                self.assertIn("completed", self.record)
                self.assertNotIn("stalled", self.record)
                self.assertNotIn("dispatch_visibility_since", self.record)
                self.assertEqual(self.controller.occupied, 0)
                self.assertFalse(self.posts())

    def test_latest_run_reported_while_acknowledged_run_was_unverifiable_needs_creation_order(self):
        for z_created, merges in (("2026-11-08T00:03:00+00:00", True), ("2026-11-07T23:00:00+00:00", False), (None, False)):
            with self.subTest(z_created=z_created):
                self.setUp()
                self.record.update(last_dispatched_run="R3", last_dispatch_source_run="R2",
                    last_dispatch_at="2026-11-08T00:00:00+00:00")
                self.observe_at(1, "Z", "FINISHED")  # R3 not yet served by ID.
                self.assertEqual(self.record["last_dispatch_stale_runs"], ["Z"])
                self.remote.runs["R3"] = {"status": "FINISHED", "git": {}, "created_at": "2026-11-08T00:01:00+00:00"}
                original = self.remote.agent
                self.remote.agent = lambda r: dict(original(r), created_at=z_created)
                self.observe_at(2, "Z", "FINISHED")
                self.assertTrue(self.record["last_dispatched_run_observed"])
                self.assertEqual("completed" in self.record, merges)
                self.assertEqual(self.controller.occupied, 0 if merges else 1)

    def test_unverifiable_acknowledged_run_stays_fail_closed_without_failing_other_tasks(self):
        failures = {"404": None, "mismatch": ObservationError("Cursor run response does not match the requested agent and run"),
                    "timeout": TimeoutError("unreachable")}
        for name, error in failures.items():
            with self.subTest(failure=name):
                self.setUp()
                self.record.update(last_dispatched_run="R3", last_dispatch_source_run="R2",
                    last_dispatch_at="2026-11-08T00:00:00+00:00")
                if error is not None:
                    def failing(record, run_id, error=error):
                        raise error
                    self.remote.run = failing
                self.controller.plan["tasks"].append({"id": "two", "depends_on": []})
                self.controller.tasks["two"] = {"agent_id": "two", "submitted": True}
                original = self.remote.agent
                self.remote.agent = lambda r: original(r) if r["agent_id"] == "bc-one" else {
                    "status": "RUNNING", "run_id": "two-run", "git": {}}
                for minute in (1, 16):
                    self.observe_at(minute, "Z", "FINISHED")
                    self.assertEqual(self.controller.occupied, 2)
                    self.assertNotIn("completed", self.record)
                    self.assertNotIn("last_dispatched_run_observed", self.record)
                    self.assertFalse(self.pr_reads())
                    self.assertIn("last_error", self.record)
                    self.assertEqual(self.controller.state["health"], "needs_attention")
                    self.assertEqual(self.controller.tasks["two"]["waiting"], "Cursor run active; awaiting completion")
                self.assertEqual(self.record["stalled"]["kind"], "run_observation")
                if name == "404":
                    self.assertIn("HTTP 404", self.record["last_error"])
                self.assertFalse(self.posts())

    def reconcile_controller(self, run):
        remote, reads = Remote(), []
        def cursor(method, path, body=None):
            reads.append((method, path))
            if isinstance(run, Exception):
                raise run
            return run
        remote.cursor = cursor
        controller = Controller(remote, self.controller.plan, self.temp.name)
        return controller, reads

    def test_operator_reconciliation_acknowledges_only_a_verified_later_run(self):
        intent = {"key": "review:" + HEAD, "repair": True, "previous_run": "R2", "time": "2026-11-08T00:00:00+00:00"}
        self.record.update(intent=intent, halted="Delivery outcome uncertain; no automatic replay")
        self.controller.save()
        good = {"id": "R3", "agentId": "bc-one", "status": "RUNNING", "createdAt": "2026-11-08T00:01:00Z"}
        missing = urllib.error.HTTPError("https://api.cursor.com/v1/agents/bc-one/runs/R3", 404, "missing run", {}, None)
        refusals = [("R3", dict(good, agentId="bc-other"), ObservationError), ("R3", dict(good, id="R9"), ObservationError),
                    ("R3", dict(good, status="UNKNOWN"), ObservationError), ("R3", missing, urllib.error.HTTPError),
                    ("R3", dict(good, createdAt="2026-11-07T23:59:00Z"), ValueError), ("R2", good, ValueError)]
        for run_id, run, expected in refusals:
            with self.subTest(run_id=run_id, run=run):
                controller, reads = self.reconcile_controller(run)
                before = copy.deepcopy(controller.tasks["one"])
                with self.assertRaises(expected):
                    controller.reconcile_intent("one", run_id, "integration-lead")
                self.assertEqual(controller.tasks["one"], before)
                self.assertTrue(all(method == "GET" for method, _ in reads))
                self.assertEqual(reads, [] if run_id == "R2" else [("GET", "/agents/bc-one/runs/" + run_id)])
        for created in ("2026-11-08T00:01:00Z", None):
            with self.subTest(created=created):
                self.setUp()
                self.record.update(intent=intent, halted="Delivery outcome uncertain; no automatic replay")
                self.controller.save()
                controller, reads = self.reconcile_controller(dict(good, createdAt=created))
                with self.advance(5):
                    controller.reconcile_intent("one", "R3", "integration-lead")
                record = controller.tasks["one"]
                self.assertNotIn("intent", record)
                self.assertNotIn("halted", record)
                self.assertEqual(record["last_dispatched_run"], "R3")
                self.assertTrue(record["last_dispatched_run_observed"])
                self.assertEqual(record["delivered"], ["review:" + HEAD])
                self.assertEqual(record["reconciled"][-1]["operator"], "integration-lead")
                self.assertIn("creation time unavailable" if created is None else "after intent", record["reconciled"][-1]["evidence"])
                self.assertIn("Operator integration-lead reconciled", controller.state["events"][-1]["message"])
                self.assertEqual(reads, [("GET", "/agents/bc-one/runs/R3")])
                with self.assertRaises(ValueError):  # Nothing left to reconcile.
                    controller.reconcile_intent("one", "R3", "integration-lead")
                # The reconciled run then releases normally and a green PR merges without a new prompt.
                self.controller, controller.remote = controller, self.remote
                self.restart()
                self.observe_at(10, "R3", "FINISHED")
                self.assertIn("completed", self.record)
                self.assertFalse(self.posts())

    def test_reconcile_cli_requires_operator_and_never_ticks(self):
        intent = {"key": "review:" + HEAD, "repair": True, "previous_run": "R2", "time": "2026-11-08T00:00:00+00:00"}
        self.record["intent"] = intent
        self.controller.save()
        plan = Path(self.temp.name) / "plan.json"
        plan.write_text(json.dumps(self.controller.plan))
        base = ["build_controller.py", "--plan", str(plan), "--state", self.temp.name, "--reconcile-intent", "one", "R3"]
        with patch("sys.argv", base), patch("sys.stderr", io.StringIO()), self.assertRaises(SystemExit):
            build_controller.main()
        self.remote.runs["R3"] = {"status": "RUNNING", "git": {}, "created_at": "2026-11-08T00:02:00+00:00"}
        with patch("sys.argv", base + ["--operator", "integration-lead"]), patch("build_controller.Remote", return_value=self.remote):
            build_controller.main()
        state = json.loads((Path(self.temp.name) / "controller.json").read_text())
        self.assertEqual(state["tasks"]["one"]["last_dispatched_run"], "R3")
        self.assertNotIn("intent", state["tasks"]["one"])
        self.assertEqual(state["tick"], 0)
        self.assertFalse(self.posts())
        self.assertFalse(self.pr_reads())

    def test_uncertain_intent_never_binds_an_arbitrary_third_run(self):
        self.failed_review()
        self.remote.run_id = "R2"
        self.remote.fail_post = True
        with self.advance(0):
            with self.assertRaises(TimeoutError):
                self.process()
        self.remote.fail_post = False
        self.remote.report.update(verdict="pass", findings=[])
        self.remote.check_rows["Codex checkpoint"]["conclusion"] = "success"
        for i in range(3):
            task = "active-" + str(i)
            self.controller.plan["tasks"].append({"id": task, "depends_on": []})
            self.controller.tasks[task] = {"agent_id": task, "submitted": True}
        self.controller.plan["tasks"].append({"id": "five", "depends_on": []})
        original = self.remote.agent
        self.remote.agent = lambda r: original(r) if r["agent_id"] == "bc-one" else {
            "status": "RUNNING", "run_id": "active", "git": {}}
        self.remote.calls.clear()
        for minute, run_id in enumerate(("R1", None, "", "R2", "R3"), start=1):
            self.remote.run_id = run_id
            self.restart()
            with self.advance(minute):
                self.controller.tick()
            self.assertEqual(self.controller.occupied, 4)
            self.assertNotIn("five", self.controller.tasks)
            self.assertNotIn("completed", self.record)
            self.assertNotIn("last_dispatched_run", self.record)
            self.assertEqual(self.record["intent"]["previous_run"], "R2")
            self.assertEqual(self.record["repair_window"]["attempts"], 0)
            self.assertEqual(self.record["repair_count"], 2)
            self.assertEqual(self.controller.state["health"], "needs_attention")
            self.assertIn("verify delivery", self.record["waiting"])
            self.assertFalse(any(c[1].startswith("pulls/") or c[1] == "/agents" for c in self.remote.calls))
            self.assertFalse(self.posts())

    def test_invalid_acknowledgement_id_keeps_intent_and_budget_across_restart(self):
        for run_id in (None, "", 123):
            with self.subTest(run_id=run_id):
                self.setUp()
                self.failed_review()
                self.remote.cursor = lambda *args: {"run": {"id": run_id}}
                with self.assertRaisesRegex(ValueError, "valid run ID"):
                    self.process()
                self.assertIn("intent", self.record)
                self.assertEqual(self.record["repair_window"]["attempts"], 0)
                self.assertEqual(self.record["repair_count"], 2)
                self.remote.run_id = "unrelated"
                self.restart()
                self.controller.tick()
                self.assertEqual(self.controller.occupied, 1)
                self.assertIn("intent", self.record)
                self.assertNotIn("completed", self.record)

    def test_invalid_persisted_acknowledgement_is_not_a_missing_reservation(self):
        for run_id in (None, "", 123):
            with self.subTest(run_id=run_id):
                self.setUp()
                self.record["last_dispatched_run"] = run_id
                self.remote.run_id = run_id
                self.restart()
                self.controller.tick()
                self.assertEqual(self.controller.occupied, 1)
                self.assertNotIn("completed", self.record)
                self.assertFalse(any(c[1].startswith("pulls/") for c in self.remote.calls))

    def test_unobserved_acknowledged_run_reserves_capacity_across_restart(self):
        self.failed_review()
        for i in range(3):
            task = "active-" + str(i)
            self.controller.plan["tasks"].append({"id": task, "depends_on": []})
            self.controller.tasks[task] = {"agent_id": task, "submitted": True}
        self.controller.plan["tasks"].append({"id": "five", "depends_on": []})
        original = self.remote.agent
        self.remote.agent = lambda r: original(r) if r["agent_id"] == "bc-one" else {
            "status": "RUNNING", "run_id": "active", "git": {}}
        with self.advance(0):
            self.controller.tick()
        self.assertEqual(len(self.posts()), 1)
        self.assertEqual(self.controller.occupied, 4)
        self.restart()
        with self.advance(1):
            self.controller.tick()
        self.assertEqual(self.controller.occupied, 4)
        self.assertNotIn("five", self.controller.tasks)
        self.assertFalse(any(c[1] == "/agents" for c in self.remote.calls))
        self.assertEqual(len(self.posts()), 1)

    def test_unobserved_acknowledged_run_blocks_green_merge_and_pr_actions(self):
        self.failed_review()
        with self.advance(0):
            self.process()
        self.remote.report.update(verdict="pass", findings=[])
        self.remote.check_rows["Codex checkpoint"]["conclusion"] = "success"
        self.restart()
        self.remote.calls.clear()
        with self.advance(1):
            self.controller.tick()
        self.assertNotIn("completed", self.record)
        self.assertEqual(self.controller.occupied, 1)
        self.assertFalse(any(c[1].startswith("pulls/") for c in self.remote.calls))

    def test_unobserved_run_deadline_precedes_pending_checks_and_clears_on_active(self):
        self.failed_review()
        with self.advance(0):
            self.process()
        self.remote.check_rows["Codex checkpoint"]["status"] = "in_progress"
        self.restart()
        with self.advance(16):
            self.controller.tick()
        self.assertEqual(self.record["stalled"]["kind"], "run_observation")
        self.assertEqual(self.controller.state["health"], "needs_attention")
        self.remote.run_id = self.record["last_dispatched_run"]
        self.remote.run_status = "RUNNING"
        self.restart()
        with self.advance(17):
            self.controller.tick()
        self.assertNotIn("stalled", self.record)
        self.assertEqual(self.controller.state["health"], "observing")
        self.assertEqual(self.controller.occupied, 1)
        self.assertEqual(len(self.posts()), 1)

    def test_missing_snapshot_run_id_reserves_acknowledged_worker(self):
        self.failed_review()
        with self.advance(0):
            self.process()
        self.remote.run_id = None
        self.restart()
        with self.advance(16):
            self.controller.tick()
        self.assertEqual(self.controller.occupied, 1)
        self.assertIn("Terminal Cursor observation lacks a run identity", self.record["last_error"])
        self.assertEqual(self.controller.state["health"], "needs_attention")
        self.assertEqual(len(self.posts()), 1)

    def test_base_format_acknowledgement_reserves_slot_and_blocks_pr_across_restarts(self):
        self.record.update(last_dispatched_run="legacy-run", delivered=["review:" + HEAD], repair_count=38,
            halted=LEGACY_REPAIR_HALT)
        for i in range(3):
            task = "active-" + str(i)
            self.controller.plan["tasks"].append({"id": task, "depends_on": []})
            self.controller.tasks[task] = {"agent_id": task, "submitted": True}
        self.controller.plan["tasks"].append({"id": "five", "depends_on": []})
        original = self.remote.agent
        self.remote.agent = lambda r: original(r) if r["agent_id"] == "bc-one" else {
            "status": "RUNNING", "run_id": "active", "git": {}}
        self.restart()  # Load exactly the old persisted shape, with no source or dispatch time.
        for minute in (0, 1, 16):
            with self.advance(minute):
                self.controller.tick()
                self.assertEqual(self.controller.occupied, 4)
                self.assertNotIn("five", self.controller.tasks)
                self.assertNotIn("completed", self.record)
                self.assertFalse(any(c[1].startswith("pulls/") or c[1] == "/agents" for c in self.remote.calls))
                self.assertFalse(self.posts())
                self.assertNotIn("last_dispatch_at", self.record)
                self.assertEqual(self.record["dispatch_visibility_since"], "2026-11-08T00:00:00+00:00")
                self.restart()
        self.assertEqual(self.record["stalled"]["kind"], "run_observation")
        self.assertEqual(self.controller.state["health"], "needs_attention")
        self.remote.run_id, self.remote.run_status = "legacy-run", "RUNNING"
        with self.advance(17):
            self.controller.tick()
        self.assertNotIn("stalled", self.record)
        self.assertNotIn("dispatch_visibility_since", self.record)
        self.assertEqual(self.controller.occupied, 4)
        self.assertNotIn("five", self.controller.tasks)

    def test_base_format_run_must_match_before_green_merge(self):
        self.record["last_dispatched_run"] = "legacy-run"
        self.restart()
        with self.advance(0):
            self.controller.tick()
        self.assertNotIn("completed", self.record)
        self.assertEqual(self.controller.occupied, 1)
        self.restart()
        self.remote.run_id = "legacy-run"
        with self.advance(1):
            self.controller.tick()
        self.assertIn("completed", self.record)
        self.assertFalse(self.posts())
        self.assertNotIn("dispatch_visibility_since", self.record)

    def test_acknowledged_run_not_yet_visible_never_replays_and_flags_stall(self):
        self.failed_review()
        with self.advance(0):
            self.process()
        with self.advance(16):
            self.process()
        self.assertEqual(len(self.posts()), 1)
        self.assertEqual(self.record["stalled"]["kind"], "run_observation")
        self.finish_correction()
        with self.advance(17):
            self.process()
        self.assertEqual(len(self.posts()), 2)
        self.assertNotIn("stalled", self.record)

    def test_active_timeout_survives_restart_and_heartbeat_without_duplicate_runs(self):
        self.remote.run_status = "RUNNING"
        with self.advance(0):
            self.controller.tick()
        self.restart()
        self.remote.agent = lambda r: {"status": "ACTIVE", "run_id": "run-old", "updated_at": "now", "git": {}}
        with self.advance(31):
            self.controller.tick()
        self.assertEqual(self.record["stalled"]["kind"], "active_run")
        self.assertEqual(self.controller.state["health"], "needs_attention")
        self.assertFalse(self.posts())
        self.assertNotIn("last_productive_progress", self.controller.state)
        self.assertIn("last_successful_tick", self.controller.state)
        status = json.loads((Path(self.temp.name) / "status.json").read_text())
        self.assertEqual(status["health"], "needs_attention")
        self.assertIn("STALLED", [c for c in self.remote.calls if c[0] == "PATCH"][-1][2]["body"])

    def test_new_active_run_or_finished_run_clears_only_active_stall(self):
        self.remote.run_status = "RUNNING"
        with self.advance(0):
            self.process()
        with self.advance(91):
            self.process()
            self.remote.run_id = "new-active"
            self.process()
        self.assertNotIn("stalled", self.record)
        with self.advance(182):
            self.process()
            self.assertIn("stalled", self.record)
            self.remote.run_status = "FINISHED"
            self.failed_review()
            self.process()
        self.assertNotIn("stalled", self.record)
        self.assertEqual(len(self.posts()), 1)

    def test_initial_active_run_keeps_deadline_through_unknown_id_and_restart(self):
        self.remote.run_status = "RUNNING"
        for minute, run_id in ((0, "run-A"), (31, None), (32, "run-A"), (33, None)):
            with self.advance(minute):
                self.remote.run_id = run_id
                self.controller.tick()
                self.assertEqual(self.record["active_watch"]["since"], "2026-11-08T00:00:00+00:00")
                self.assertEqual(self.record["active_watch"]["run_id"], "run-A")
                self.assertEqual(self.record["active_watch"]["observed_run_id"], run_id)
                self.assertEqual(self.controller.occupied, 1)
                if minute:
                    self.assertEqual(self.record["stalled"]["kind"], "active_run")
                    self.assertEqual(self.controller.state["health"], "needs_attention")
                self.restart()
        self.assertFalse(self.posts())
        self.remote.run_id = "run-B"
        with self.advance(34):
            self.controller.tick()
        self.assertEqual(self.record["active_watch"]["since"], "2026-11-08T00:34:00+00:00")
        self.assertNotIn("stalled", self.record)
        self.assertEqual(self.controller.occupied, 1)

    def test_unknown_initial_run_binds_known_id_without_resetting_expired_watch(self):
        self.remote.run_status, self.remote.run_id = "RUNNING", None
        with self.advance(0):
            self.controller.tick()
        self.restart()
        with self.advance(31):
            self.controller.tick()
        self.assertIsNone(self.record["active_watch"]["run_id"])
        self.assertIsNone(self.record["active_watch"]["observed_run_id"])
        self.assertEqual(self.record["stalled"]["kind"], "active_run")
        self.restart()
        self.remote.run_id = "run-A"
        with self.advance(32):
            self.controller.tick()
        self.assertEqual(self.record["active_watch"]["run_id"], "run-A")
        self.assertEqual(self.record["active_watch"]["since"], "2026-11-08T00:00:00+00:00")
        self.assertEqual(self.record["stalled"]["kind"], "active_run")
        self.assertEqual(self.record["stalled"]["run_id"], "run-A")
        self.assertEqual(self.controller.state["health"], "needs_attention")
        self.assertFalse(self.posts())

    def test_active_identity_changes_never_replace_unrelated_warning(self):
        self.remote.run_status = "RUNNING"
        warning = {"kind": "no_progress", "reason": "Existing repeated defect requires inspection", "fingerprint": "existing"}
        self.record["stalled"] = dict(warning)
        for minute, run_id in ((0, None), (31, "run-A"), (32, None), (33, "run-A"), (34, "run-B")):
            with self.advance(minute):
                self.remote.run_id = run_id
                self.controller.tick()
                self.assertEqual(self.record["stalled"], warning)
                self.assertEqual(self.controller.state["health"], "needs_attention")
                self.restart()
        self.assertFalse(self.posts())

    def test_all_halted_tick_is_visible_and_not_product_progress(self):
        self.record["halted"] = "Ownership mismatch"
        self.controller.tick()
        self.assertEqual(self.controller.state["health"], "needs_attention")
        self.assertIn("Needs supervision", self.controller.state["last_result"])
        self.assertNotIn("last_productive_progress", self.controller.state)

    def test_observation_failure_publishes_control_error_without_capacity_leak(self):
        self.remote.agent = lambda r: (_ for _ in ()).throw(TimeoutError("unreachable"))
        for _ in range(2):
            self.controller.tick()
            self.assertEqual(self.controller.occupied, 1)
        self.assertEqual(self.controller.state["health"], "needs_attention")
        self.assertNotIn("last_successful_tick", self.controller.state)
        self.assertFalse(self.posts())
        status = json.loads((Path(self.temp.name) / "status.json").read_text())
        self.assertIn("last_error", status["tasks"]["one"])

    def occupy_other_slots(self):
        for i in range(3):
            task = "active-" + str(i)
            self.controller.plan["tasks"].append({"id": task, "depends_on": []})
            self.controller.tasks[task] = {"agent_id": task, "submitted": True}
        self.controller.plan["tasks"].append({"id": "five", "depends_on": []})

    def test_invalid_statuses_reserve_capacity_across_origins_holds_and_restarts(self):
        invalid = [None, [], {}, {"run_id": "R3"}] + [
            {"status": status, "run_id": "R3"} for status in (None, "", "UNKNOWN", "IDLE", "ARCHIVED", "STOPPED", 0, True, [], {})]
        for origin in ("submitted", "adopted", "launch"):
            for hold in (("plain",) if origin == "adopted" else ("plain", "acknowledged", "intent", "halted")):
                for invalid_snapshot in invalid:
                    with self.subTest(origin=origin, hold=hold, snapshot=invalid_snapshot):
                        self.setUp()
                        if origin == "adopted":
                            self.controller.tasks.clear()
                            (Path(self.temp.name) / "agents.json").write_text(json.dumps({"one": {
                                "agent_id": "bc-one", "submitted": True, "starting_sha": BASE}}))
                        elif origin == "launch":
                            self.record["submitted"] = False
                        if hold in {"acknowledged", "intent"}:
                            self.record.update(last_dispatched_run="R3", last_dispatch_source_run="R2")
                        if hold == "intent":
                            self.record["intent"] = {"key": "pending", "repair": True, "previous_run": "R3", "time": "2026-11-08T00:00:00+00:00"}
                        if hold == "halted":
                            self.record["halted"] = "Ownership mismatch"
                        expected_intent = copy.deepcopy(self.record.get("intent"))
                        self.occupy_other_slots()
                        self.remote.agent = lambda r: copy.deepcopy(invalid_snapshot) if r["agent_id"] == "bc-one" else {
                            "status": "RUNNING", "run_id": "active", "git": {}}
                        for minute in (0, 1):
                            with self.advance(minute):
                                self.controller.tick()
                            self.record = self.controller.tasks["one"]
                            self.assertEqual(self.controller.occupied, 4)
                            self.assertNotIn("five", self.controller.tasks)
                            self.assertNotIn("completed", self.record)
                            self.assertIn("Cursor observation invalid", self.record["last_error"])
                            self.assertEqual(self.record.get("intent"), expected_intent)
                            if hold == "halted":
                                self.assertEqual(self.record["halted"], "Ownership mismatch")
                            if origin == "launch":
                                self.assertTrue(self.record["launch_uncertain"])
                                self.assertFalse(self.record["submitted"])
                            self.assertEqual(self.controller.state["health"], "needs_attention")
                            self.assertNotIn("last_successful_tick", self.controller.state)
                            self.assertFalse(any(c[1].startswith("pulls/") or c[1] == "/agents" for c in self.remote.calls))
                            self.assertFalse(self.posts())
                            self.restart()
                        self.remote.agent = lambda r: {"status": "RUNNING", "run_id": "R3", "git": {}}
                        with self.advance(2):
                            self.controller.tick()
                        self.assertEqual(self.controller.occupied, 4)
                        self.assertTrue(self.record["submitted"])
                        self.assertNotIn("launch_uncertain", self.record)
                        self.assertNotIn("last_error", self.record)
                        self.assertEqual(self.record.get("intent"), expected_intent)
                        if hold == "halted":
                            self.assertEqual(self.record["halted"], "Ownership mismatch")
                        self.assertFalse(any(c[1].startswith("pulls/") or c[1] == "/agents" for c in self.remote.calls))
                        self.assertFalse(self.posts())

    def test_terminal_without_valid_identity_reserves_slot_until_valid_recovery(self):
        for status in ("FINISHED", "ERROR", "CANCELLED", "EXPIRED"):
            for run_id in (None, "", " ", 7, [], {}):
                with self.subTest(status=status, run_id=run_id):
                    self.setUp()
                    self.occupy_other_slots()
                    self.remote.agent = lambda r: {"status": status, "run_id": run_id, "git": {}} if r["agent_id"] == "bc-one" else {
                        "status": "RUNNING", "run_id": "active", "git": {}}
                    for minute in (0, 1):
                        self.restart()
                        with self.advance(minute):
                            self.controller.tick()
                        self.assertEqual(self.controller.occupied, 4)
                        self.assertNotIn("five", self.controller.tasks)
                        self.assertNotIn("completed", self.record)
                        self.assertIn("Cursor observation invalid", self.record["last_error"])
                        self.assertFalse(any(c[1].startswith("pulls/") or c[1] == "/agents" for c in self.remote.calls))
                        self.assertFalse(self.posts())
                    self.remote.agent = lambda r: {"status": status, "run_id": "R3", "git": {}} if r["agent_id"] == "bc-one" else {
                        "status": "RUNNING", "run_id": "active", "git": {}}
                    with self.advance(2):
                        self.controller.tick()
                    self.assertNotIn("last_error", self.record)
                    if status == "FINISHED":
                        self.assertIn("completed", self.record)
                        self.assertFalse(self.posts())
                    else:
                        self.assertEqual(len(self.posts()), 1)
                        self.assertEqual(self.controller.occupied, 4)
                        self.assertNotIn("five", self.controller.tasks)

    def test_direct_processing_rejects_invalid_observation_before_state_changes(self):
        self.record["halted"] = LEGACY_REPAIR_HALT
        original = copy.deepcopy(self.record)
        for snapshot in ({"status": None}, {"status": "FUTURE", "run_id": "R3"}, {"status": "FINISHED"}):
            with self.assertRaises(ObservationError):
                self.controller.process(SPEC, self.record, snapshot, BASE)
            self.assertEqual(self.record, original)
            self.assertFalse(self.remote.calls)

    def test_worker_observation_errors_never_free_capacity_or_overwrite_holds(self):
        errors = [urllib.error.HTTPError("https://api.cursor.com/v1/agents/bc-one/runs/R3", 404, "missing run", {}, None),
                  urllib.error.HTTPError("https://api.cursor.com/v1/agents/bc-one", 403, "forbidden", {}, None),
                  urllib.error.HTTPError("https://api.cursor.com/v1/agents/bc-one", 503, "unavailable", {}, None),
                  TimeoutError("unreachable"), AgentNotFound("Cursor agent endpoint returned HTTP 404")]
        for error in errors:
            for pending in ("acknowledged", "intent", "initial"):
                with self.subTest(error=type(error).__name__, pending=pending):
                    self.setUp()
                    self.record["halted"] = "Ownership mismatch"
                    if pending != "initial":
                        self.record.update(last_dispatched_run="R3", last_dispatch_source_run="R2")
                    if pending == "intent":
                        self.record["intent"] = {"key": "pending", "repair": True, "previous_run": "R3", "time": "2026-11-08T00:00:00+00:00"}
                    before = copy.deepcopy(self.record)
                    self.occupy_other_slots()
                    def unavailable(record):
                        if record["agent_id"] == "bc-one":
                            raise error
                        return {"status": "RUNNING", "run_id": "active", "git": {}}
                    self.remote.agent = unavailable
                    for minute in (0, 1):
                        self.restart()
                        with self.advance(minute):
                            self.controller.tick()
                        self.assertEqual(self.controller.occupied, 4)
                        self.assertNotIn("five", self.controller.tasks)
                        self.assertNotIn("completed", self.record)
                        self.assertEqual(self.record["halted"], "Ownership mismatch")
                        self.assertEqual(self.record.get("intent"), before.get("intent"))
                        self.assertEqual(self.record.get("last_dispatched_run"), before.get("last_dispatched_run"))
                        self.assertIn("last_error", self.record)
                        self.assertEqual(self.controller.state["health"], "needs_attention")
                        self.assertNotIn("last_successful_tick", self.controller.state)
                        self.assertFalse(any(c[1].startswith("pulls/") or c[1] == "/agents" for c in self.remote.calls))
                        self.assertFalse(self.posts())

    def test_run_404_during_launch_reconciliation_keeps_slot_until_observed(self):
        self.record["submitted"] = False
        self.occupy_other_slots()
        def unavailable(record):
            if record["agent_id"] == "bc-one":
                raise urllib.error.HTTPError("https://api.cursor.com/v1/agents/bc-one/runs/R3", 404, "missing run", {}, None)
            return {"status": "RUNNING", "run_id": "active", "git": {}}
        self.remote.agent = unavailable
        for minute in (0, 1):
            self.restart()
            with self.advance(minute):
                self.controller.tick()
            self.assertTrue(self.record["launch_uncertain"])
            self.assertFalse(self.record["submitted"])
            self.assertNotIn("halted", self.record)
            self.assertEqual(self.controller.occupied, 4)
            self.assertNotIn("five", self.controller.tasks)
        self.remote.agent = lambda record: {"status": "RUNNING", "run_id": "R3", "git": {}}
        self.restart()
        with self.advance(2):
            self.controller.tick()
        self.assertTrue(self.record["submitted"])
        self.assertNotIn("launch_uncertain", self.record)
        self.assertNotIn("last_error", self.record)
        self.assertEqual(self.controller.occupied, 4)
        self.assertFalse(any(c[1] == "/agents" for c in self.remote.calls))
        self.assertFalse(self.posts())

    def test_failed_create_or_conflict_inspection_keeps_slot_and_durable_id(self):
        for outcome in ("timeout", "conflict"):
            with self.subTest(outcome=outcome):
                self.setUp()
                self.record["submitted"] = False
                self.record["starting_sha"] = BASE
                self.occupy_other_slots()
                def absent(record):
                    if record["agent_id"] == "bc-one":
                        raise AgentNotFound("Cursor agent endpoint returned HTTP 404")
                    return {"status": "RUNNING", "run_id": "active", "git": {}}
                self.remote.agent = absent
                def uncertain(method, path, payload=None):
                    self.remote.calls.append((method, path, payload))
                    if outcome == "timeout":
                        raise TimeoutError("creation response lost")
                    raise urllib.error.HTTPError("https://api.cursor.com/v1" + path,
                        409 if method == "POST" else 404, "unavailable", {}, None)
                self.remote.cursor = uncertain
                with self.advance(0):
                    self.controller.tick()
                self.assertEqual(self.controller.occupied, 4)
                self.assertNotIn("five", self.controller.tasks)
                self.assertIn("last_error", self.record)
                launches = [c for c in self.remote.calls if c[0] == "POST" and c[1] == "/agents"]
                self.assertEqual(len(launches), 1)
                self.assertEqual(launches[0][2]["agentId"], "bc-one")
                self.remote.agent = lambda record: {"status": "RUNNING", "run_id": "active", "git": {}}
                self.restart()
                with self.advance(1):
                    self.controller.tick()
                self.assertEqual(self.controller.occupied, 4)
                self.assertNotIn("five", self.controller.tasks)
                self.assertTrue(self.record["submitted"])
                self.assertEqual(self.record["agent_id"], "bc-one")
                self.assertEqual(len([c for c in self.remote.calls if c[0] == "POST" and c[1] == "/agents"]), 1)

    def test_agent_endpoint_absence_cannot_discard_pending_dispatch(self):
        for pending in ("acknowledged", "intent", "halted"):
            with self.subTest(pending=pending):
                self.setUp()
                self.record["submitted"] = False
                if pending == "acknowledged":
                    self.record["last_dispatched_run"] = "R3"
                elif pending == "intent":
                    self.record["intent"] = {"key": "pending", "repair": True, "previous_run": "R2", "time": "2026-11-08T00:00:00+00:00"}
                else:
                    self.record["halted"] = "Ownership mismatch"
                self.occupy_other_slots()
                def unavailable(record):
                    if record["agent_id"] == "bc-one":
                        raise AgentNotFound("Cursor agent endpoint returned HTTP 404")
                    return {"status": "RUNNING", "run_id": "active", "git": {}}
                self.remote.agent = unavailable
                for minute in (0, 1):
                    self.restart()
                    with self.advance(minute):
                        self.controller.tick()
                    self.assertEqual(self.controller.occupied, 4)
                    self.assertNotIn("five", self.controller.tasks)
                    self.assertTrue(self.record["launch_uncertain"])
                    if pending == "halted":
                        self.assertEqual(self.record["halted"], "Ownership mismatch")
                    self.assertFalse(any(c[1] == "/agents" for c in self.remote.calls))
                    self.assertFalse(self.posts())

    def test_transient_run_404_blocks_green_merge_until_exact_run_is_visible(self):
        self.record.update(last_dispatched_run="R3", last_dispatch_source_run="R2")
        original = self.remote.agent
        def unavailable(record):
            raise urllib.error.HTTPError("https://api.cursor.com/v1/agents/bc-one/runs/R3", 404, "missing run", {}, None)
        self.remote.agent = unavailable
        for minute in (0, 1):
            self.restart()
            with self.advance(minute):
                self.controller.tick()
            self.assertEqual(self.controller.occupied, 1)
            self.assertNotIn("halted", self.record)
            self.assertNotIn("completed", self.record)
            self.assertFalse(any(c[1].startswith("pulls/") for c in self.remote.calls))
        self.remote.agent = original
        self.remote.run_id = "R1"
        self.restart()
        with self.advance(2):
            self.controller.tick()
        self.assertEqual(self.controller.occupied, 1)
        self.assertNotIn("completed", self.record)
        self.assertFalse(any(c[1].startswith("pulls/") for c in self.remote.calls))
        self.remote.run_id = "R3"
        self.restart()
        with self.advance(3):
            self.controller.tick()
        self.assertIn("completed", self.record)
        self.assertFalse(self.posts())

    def test_global_control_error_is_saved_even_before_worker_observation(self):
        original = self.remote.gh
        def fail_main(path, *args, **kwargs):
            if path == "git/ref/heads/main":
                raise TimeoutError("GitHub unavailable")
            return original(path, *args, **kwargs)
        self.remote.gh = fail_main
        with self.assertRaises(TimeoutError):
            self.controller.tick()
        status = json.loads((Path(self.temp.name) / "status.json").read_text())
        self.assertEqual(status["health"], "needs_attention")
        self.assertIn("GitHub unavailable", status["tick_error"])
        self.assertNotIn("last_successful_tick", self.controller.state)

    def test_post_and_patch_publication_failure_is_local_unhealthy_and_recovers(self):
        for comment_id in (None, 99):
            with self.subTest(comment_id=comment_id):
                self.setUp()
                self.remote.run_status = "RUNNING"
                previous_success = "2026-11-07T23:00:00+00:00"
                self.controller.state["last_successful_tick"] = previous_success
                if comment_id:
                    self.controller.state["status_comment"] = comment_id
                target = "issues/comments/99" if comment_id else "issues/1/comments"
                method = "PATCH" if comment_id else "POST"
                original = self.remote.gh
                def fail_publication(path, requested_method="GET", payload=None, **kwargs):
                    if path == target and requested_method == method:
                        self.remote.calls.append((requested_method, path, payload))
                        raise RuntimeError("publication unavailable")
                    return original(path, requested_method, payload, **kwargs)
                self.remote.gh = fail_publication
                for minute in (0, 1):
                    with self.advance(minute):
                        with self.assertRaisesRegex(RuntimeError, "publication unavailable"):
                            self.controller.tick()
                        summary = json.loads((Path(self.temp.name) / "status.json").read_text())
                        self.assertEqual(summary["health"], "needs_attention")
                        self.assertEqual(summary["publication_error"], "RuntimeError: publication unavailable")
                        self.assertEqual(summary["updated"], "2026-11-08T00:0" + str(minute) + ":00+00:00")
                        self.assertEqual(self.controller.state["last_successful_tick"], previous_success)
                        self.assertEqual(len([c for c in self.remote.calls if c[0] == method and c[1] == target]), minute + 1)
                        self.restart()
                self.remote.gh = original
                with self.advance(2):
                    self.controller.tick()
                summary = json.loads((Path(self.temp.name) / "status.json").read_text())
                self.assertEqual(summary["health"], "observing")
                self.assertIsNone(summary["publication_error"])
                self.assertNotIn("publication_error", self.controller.state)
                self.assertEqual(self.controller.state["last_successful_tick"], "2026-11-08T00:02:00+00:00")
                self.assertEqual(len([c for c in self.remote.calls if c[0] == method and c[1] == target]), 3)

    def test_publication_recovery_preserves_current_task_observation_error(self):
        self.remote.agent = lambda r: (_ for _ in ()).throw(TimeoutError("Cursor unavailable"))
        original = self.remote.gh
        def fail_publication(path, method="GET", payload=None, **kwargs):
            if method == "POST" and path == "issues/1/comments":
                self.remote.calls.append((method, path, payload))
                raise RuntimeError("publication unavailable")
            return original(path, method, payload, **kwargs)
        self.remote.gh = fail_publication
        with self.assertRaisesRegex(RuntimeError, "publication unavailable"):
            self.controller.tick()
        summary = json.loads((Path(self.temp.name) / "status.json").read_text())
        self.assertIn("last_error", summary["tasks"]["one"])
        self.assertIn("publication unavailable", summary["publication_error"])
        self.assertNotIn("last_successful_tick", self.controller.state)
        self.remote.gh = original
        self.restart()
        self.controller.tick()
        summary = json.loads((Path(self.temp.name) / "status.json").read_text())
        self.assertEqual(summary["health"], "needs_attention")
        self.assertIn("last_error", summary["tasks"]["one"])
        self.assertIsNone(summary["publication_error"])
        self.assertNotIn("last_successful_tick", self.controller.state)

    def test_publication_error_does_not_mask_global_observation_exception(self):
        original = self.remote.gh
        def fail_both(path, method="GET", payload=None, **kwargs):
            if path == "git/ref/heads/main":
                raise TimeoutError("main unavailable")
            if method == "POST" and path == "issues/1/comments":
                self.remote.calls.append((method, path, payload))
                raise RuntimeError("publication unavailable")
            return original(path, method, payload, **kwargs)
        self.remote.gh = fail_both
        with self.assertRaisesRegex(TimeoutError, "main unavailable"):
            self.controller.tick()
        summary = json.loads((Path(self.temp.name) / "status.json").read_text())
        self.assertEqual(summary["tick_error"], "TimeoutError: main unavailable")
        self.assertEqual(summary["publication_error"], "RuntimeError: publication unavailable")
        self.assertEqual(summary["health"], "needs_attention")
        self.assertNotIn("last_successful_tick", self.controller.state)
        self.assertEqual(len([c for c in self.remote.calls if c[0] == "POST"]), 1)

    def test_merge_publishes_progress_on_same_tick_before_early_return(self):
        self.record["last_error"] = "Previous observation failed"
        self.controller.tick()
        status = json.loads((Path(self.temp.name) / "status.json").read_text())
        self.assertEqual(status["last_productive_progress"]["merge_sha"], NEW)
        self.assertEqual(status["last_productive_progress"]["task"], "one")
        self.assertIn("completed", status["tasks"]["one"])
        self.assertNotIn("last_error", status["tasks"]["one"])
        self.assertIn("last_successful_tick", self.controller.state)

    def test_dry_run_legacy_migration_and_recovery_do_not_write_or_send(self):
        self.failed_review()
        self.record["halted"] = LEGACY_REPAIR_HALT
        self.controller.dry = True
        self.controller.tick()
        self.assertFalse(any(c[0] != "GET" for c in self.remote.calls))
        self.assertFalse((Path(self.temp.name) / "controller.json").exists())
        self.assertFalse((Path(self.temp.name) / "status.json").exists())

    def evidence_fixture(self):
        spec = dict(SPEC, acceptance_ids=["AC-001", "AC-002"], phase_acceptance={"setup": ["AC-001"]})
        evidence = {"task_id": "one", "status": "pass", "tested_revision": HEAD,
                    "tests": [{"command": "go test ./...", "exit_code": 0}],
                    "passed_acceptance_ids": ["AC-001"], "pending_acceptance_ids": ["AC-002"],
                    "phase_cases": [{"id": "setup", "acceptance_ids": ["AC-001"], "status": "pass", "command": "test first run", "exit_code": 0}]}
        return spec, evidence

    def test_complete_phase_evidence_is_accepted(self):
        spec, evidence = self.evidence_fixture()
        self.controller.validate_evidence(spec, evidence, HEAD)

    def test_incomplete_phase_evidence_is_rejected(self):
        spec, evidence = self.evidence_fixture()
        for key in ["tested_revision", "passed_acceptance_ids", "pending_acceptance_ids", "phase_cases"]:
            broken = copy.deepcopy(evidence)
            del broken[key]
            with self.assertRaises(ValueError):
                self.controller.validate_evidence(spec, broken, HEAD)

    def test_code_changes_after_tested_revision_are_rejected(self):
        spec, evidence = self.evidence_fixture()
        evidence["tested_revision"] = BASE
        self.remote.gh = lambda *a, **k: {"status": "ahead", "behind_by": 0, "files": [{"filename": "internal/changed.go"}]}
        with self.assertRaises(ValueError):
            self.controller.validate_evidence(spec, evidence, HEAD)

    def test_report_only_commit_after_tested_code_is_allowed(self):
        spec, evidence = self.evidence_fixture()
        evidence["tested_revision"] = BASE
        self.remote.gh = lambda *a, **k: {"status": "ahead", "behind_by": 0, "files": [{"filename": "docs/build/reports/one.json"}]}
        self.controller.validate_evidence(spec, evidence, HEAD)

    def test_full_release_cannot_keep_pending_acceptance(self):
        spec, evidence = self.evidence_fixture()
        spec["full_acceptance"] = True
        with self.assertRaises(ValueError):
            self.controller.validate_evidence(spec, evidence, HEAD)

    def test_unrelated_pr_cannot_complete_assignment(self):
        self.remote.pr["head"]["ref"] = "cursor/unrelated"
        self.process()
        self.assertTrue(self.record["halted"])
        self.assertNotIn("completed", self.record)

    def test_pr_cannot_be_reused_by_another_task(self):
        self.controller.tasks["other"] = {"pr": 7}
        self.process()
        self.assertTrue(self.record["halted"])

    def test_review_for_another_assignment_is_rejected(self):
        self.remote.context["task_id"] = "other"
        self.process()
        self.assertNotIn("completed", self.record)

    def test_null_report_and_null_member_are_rejected(self):
        spec, evidence = self.evidence_fixture()
        with self.assertRaises(ValueError):
            self.controller.validate_evidence(spec, None, HEAD)
        evidence["phase_cases"] = [None]
        with self.assertRaises(ValueError):
            self.controller.validate_evidence(spec, evidence, HEAD)

    def test_unconfirmed_legacy_launch_404_reuses_its_id(self):
        self.controller.tasks.clear()
        (Path(self.temp.name) / "agents.json").write_text(json.dumps({"one": {"agent_id": "bc-existing", "starting_sha": BASE, "submitted": False}}))
        def absent(record):
            raise AgentNotFound("Cursor agent endpoint returned HTTP 404")
        self.remote.agent = absent
        self.controller.tick()
        launches = [c for c in self.remote.calls if c[1] == "/agents"]
        self.assertEqual(launches[0][2]["agentId"], "bc-existing")
        self.assertTrue(self.controller.tasks["one"]["submitted"])

    def test_malformed_verdict_uses_one_retry_then_stops(self):
        self.remote.report["verdict"] = []
        self.process()
        self.process()
        self.assertEqual(len([c for c in self.remote.calls if c[1].endswith("/rerun")]), 1)
        self.assertTrue(self.record["halted"])

    def test_queued_rerun_does_not_consume_another_retry_or_halt(self):
        def pending(check):
            raise ReviewPending()
        self.remote.review = pending
        self.process()
        self.assertNotIn("halted", self.record)
        self.assertNotIn("infra_retried", self.record)

    def test_unchanged_head_with_edited_body_waits_for_matching_review(self):
        self.remote.pr["body"] = "New evidence"
        self.process()
        self.assertNotIn("completed", self.record)
        self.assertIn("fresh matching", self.record["waiting"])
        self.assertNotIn("infra_retried", self.record)

    def test_body_edit_during_final_refresh_prevents_merge(self):
        original, gets = self.remote.gh, []
        def gh(path, **kwargs):
            value = original(path, **kwargs)
            if path == "pulls/7":
                gets.append(path)
                if len(gets) == 2:
                    value["body"] = "Changed after initial validation"
            return value
        self.remote.gh = gh
        self.process()
        self.assertNotIn("completed", self.record)
        self.assertIn("fresh matching", self.record["waiting"])

    def test_task_label_edit_during_final_refresh_prevents_merge(self):
        original, gets = self.remote.gh, []
        def gh(path, **kwargs):
            value = original(path, **kwargs)
            if path == "pulls/7":
                gets.append(path)
                if len(gets) == 2:
                    value["labels"] = [{"name": "togavsu-task:other"}]
            return value
        self.remote.gh = gh
        self.process()
        self.assertNotIn("completed", self.record)
        self.assertIn("identity changed", self.record["waiting"])

    def test_product_check_change_during_final_refresh_blocks_merge(self):
        spec = dict(SPEC, required_checks=["Core tests"])
        self.remote.check_rows["Core tests"] = check("Core tests")
        original, reads = self.remote.checks, []
        def checks(head):
            value = original(head)
            reads.append(head)
            if len(reads) == 2:
                value["Core tests"]["conclusion"] = "failure"
            return value
        self.remote.checks = checks
        self.controller.process(spec, self.record, self.remote.agent(self.record), BASE)
        self.assertNotIn("completed", self.record)
        self.assertFalse(any(c[1].endswith("/merge") for c in self.remote.calls))
        self.assertIn("Core tests", self.remote.protection["required_status_checks"]["contexts"])

    def test_product_checks_are_server_enforced_before_merge(self):
        spec = dict(SPEC, required_checks=["Core tests"])
        self.remote.check_rows["Core tests"] = check("Core tests")
        self.controller.process(spec, self.record, self.remote.agent(self.record), BASE)
        self.assertTrue(self.record["completed"])
        bindings = self.remote.protection["required_status_checks"]["checks"]
        self.assertIn({"context": "Core tests", "app_id": APP_ID}, bindings)

    def test_retarget_during_final_refresh_never_merges_or_completes(self):
        original, gets = self.remote.gh, []
        def gh(path, **kwargs):
            value = original(path, **kwargs)
            if path == "pulls/7":
                gets.append(path)
                if len(gets) == 2:
                    value["base"]["ref"] = "another-branch"
            return value
        self.remote.gh = gh
        self.process()
        self.assertNotIn("completed", self.record)
        self.assertFalse(any(c[1].endswith("/merge") for c in self.remote.calls))
        self.assertIn("retargeted", self.record["halted"])

    def test_retarget_during_merge_never_unlocks_dependencies(self):
        original=self.remote.gh
        def gh(path,method="GET",payload=None,**kwargs):
            if path=="pulls/7/merge":
                self.remote.pr["base"]["ref"]="other"
                self.remote.pr.update(merged=True,merge_commit_sha=NEW,state="closed")
                return {"merged":True,"sha":NEW}
            return original(path,method,payload,**kwargs)
        self.remote.gh=gh
        self.process()
        self.assertNotIn("completed",self.record)
        self.assertIn("outside main",self.record["halted"])

    def test_accepted_branch_update_without_progress_stops_after_deadline(self):
        self.remote.behind=1
        self.process()
        self.record["update_requested_at"]=(dt.datetime.now(dt.timezone.utc)-dt.timedelta(minutes=16)).isoformat()
        self.process()
        self.assertIn("did not advance",self.record["halted"])
        self.assertEqual(len([c for c in self.remote.calls if c[1].endswith("/update-branch")]),1)

    def test_merge_response_without_main_membership_does_not_complete(self):
        self.record["merge_intent"]={"head":HEAD,"base":BASE,"result_sha":NEW}
        self.remote.pr.update(merged=True,merge_commit_sha=NEW,state="closed")
        self.process()
        self.assertNotIn("completed",self.record)
        self.assertIn("dependencies remain blocked",self.record["waiting"])

    def test_accepted_merge_with_stale_pr_view_is_not_resubmitted(self):
        self.record["merge_intent"]={"head":HEAD,"base":BASE,"result_sha":NEW}
        self.process()
        self.process()
        self.assertNotIn("completed",self.record)
        self.assertFalse(any(c[1].endswith("/merge") or c[1].endswith("/update-branch") for c in self.remote.calls))

    def test_external_merge_is_not_accepted_without_controller_intent(self):
        self.remote.pr["merged"] = True
        self.remote.pr["merge_commit_sha"] = NEW
        self.process()
        self.assertNotIn("completed", self.record)
        self.assertTrue(self.record["halted"])

    def test_interrupted_run_is_repaired_instead_of_merging_old_green_head(self):
        self.remote.run_status = "ERROR"
        self.process()
        self.assertEqual(len(self.posts()), 1)
        self.assertNotIn("completed", self.record)

    def test_worker_cannot_replace_review_gate(self):
        self.remote.changed_files = lambda n: [{"filename": ".github/workflows/codex-checkpoint.yml"}]
        self.process()
        self.assertTrue(self.record["halted"])
        self.assertNotIn("completed", self.record)

    def test_worker_cannot_change_module_scope_files(self):
        for name in ("togavsu-initial-modules-prd.md", "docs/modules/CORE-CHANGES.md", "docs/modules/quickbooks/SPEC.md"):
            with self.subTest(name):
                self.setUp()
                self.remote.changed_files = lambda n, name=name: [{"filename": name}]
                self.process()
                self.assertTrue(self.record["halted"])
                self.assertNotIn("completed", self.record)

    def test_truncated_inventory_cannot_hide_protected_file(self):
        remote = Remote()
        all_files = [{"filename": "safe/" + str(i)} for i in range(3000)]
        all_files.append({"filename": ".github/workflows/codex-checkpoint.yml"})
        def gh(path, **kwargs):
            if path == "pulls/7":
                return {"changed_files": len(all_files), "head": {"sha": HEAD}}
            page = int(path.rsplit("=", 1)[1])
            return all_files[:3000][(page-1)*100:page*100]
        remote.gh = gh
        self.remote.changed_files = remote.changed_files
        self.process()
        self.assertIn("3000-file cap", self.record["halted"])
        self.assertNotIn("completed", self.record)
        self.assertFalse(any(c[1].endswith("/merge") for c in self.remote.calls))

    def test_file_inventory_requires_complete_unique_stable_count(self):
        for count, rows, fresh_head in [(2, [{"filename": "safe"}], HEAD),
                (2, [{"filename": "safe"}]*2, HEAD),
                (1, [{"filename": "safe"}], NEW)]:
            with self.subTest(count=count, rows=rows, fresh_head=fresh_head):
                remote, reads = Remote(), []
                def gh(path, **kwargs):
                    if path == "pulls/7":
                        reads.append(path)
                        return {"changed_files": count, "head": {"sha": HEAD if len(reads)==1 else fresh_head}}
                    return rows
                remote.gh = gh
                with self.assertRaises(ValueError):
                    remote.changed_files(7)

    def test_complete_file_inventory_is_accepted(self):
        remote = Remote()
        remote.gh = lambda path: ({"changed_files": 1, "head": {"sha": HEAD}}
            if path == "pulls/7" else [{"filename": "safe"}])
        self.assertEqual(remote.changed_files(7), [{"filename": "safe"}])

    def test_renaming_control_script_also_blocks_merge(self):
        self.remote.changed_files = lambda n: [{"filename": "other.py", "previous_filename": "scripts/build_controller.py"}]
        self.process()
        self.assertTrue(self.record["halted"])

    def test_missing_required_ci_is_sent_back_after_grace(self):
        spec = dict(SPEC, required_checks=["Core tests"])
        self.record["check_wait"] = {"head": HEAD, "since": (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=16)).isoformat()}
        self.controller.process(spec, self.record, self.remote.agent(self.record), BASE)
        self.assertEqual(len(self.posts()), 1)

    def test_running_ci_is_never_halted_for_time(self):
        self.remote.check_rows["Codex checkpoint"]["status"] = "in_progress"
        self.record["check_wait"] = {"head": HEAD, "since": (dt.datetime.now(dt.timezone.utc) - dt.timedelta(hours=6)).isoformat()}
        self.process()
        self.assertNotIn("halted", self.record)
        self.assertIn("Checks pending", self.record["waiting"])

    def test_required_ci_that_never_reports_halts(self):
        spec = dict(SPEC, required_checks=["Core tests"])
        self.remote.check_rows["Codex checkpoint"]["status"] = "in_progress"
        self.record["check_wait"] = {"head": HEAD, "since": (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=91)).isoformat()}
        self.controller.process(spec, self.record, self.remote.agent(self.record), BASE)
        self.assertIn("missing for 90 minutes", self.record["halted"])

    def test_missing_phase_report_is_a_bounded_correction(self):
        spec = dict(SPEC, report_path="docs/build/reports/one.json")
        self.controller.process(spec, self.record, self.remote.agent(self.record), BASE)
        self.assertEqual(len(self.posts()), 1)
        self.assertNotIn("completed", self.record)


    def test_cancelled_event_waits_for_replacement_with_a_bounded_deadline(self):
        def review(check):
            raise ReviewSuperseded("Cancelled checkpoint; await the replacement review event")
        self.remote.review = review
        self.remote.check_rows["Codex checkpoint"]["conclusion"] = "cancelled"
        self.process()
        self.assertIn("replacement", self.record["waiting"])
        self.assertNotIn("infra_retried", self.record)
        self.assertFalse(any(c[1].endswith("/rerun") for c in self.remote.calls))
        self.record["evidence_wait"]["since"] = (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=91)).isoformat()
        self.process()
        self.assertIn("90 minutes", self.record["halted"])
        self.assertFalse(self.posts())


class FakeMailer:
    def __init__(self):
        self.sent, self.fail = [], False

    def send(self, to, subject, body):
        if self.fail:
            raise OSError("mail server unreachable")
        self.sent.append((to, subject, body))


THREADS_405 = "gh: All comments must be resolved. (HTTP 405)\n"


class StallAlertTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.remote = FakeRemote()
        self.mailer = FakeMailer()
        self.plan = {"tasks": [SPEC], "max_repairs": 1, "max_implementation_agents": 4, "alert_email": "lead@example.com",
                     "active_run_timeout_seconds": 18900}
        self.controller = Controller(self.remote, self.plan, self.temp.name, mailer=self.mailer)
        self.record = {"agent_id": "bc-one", "expected_branch": "cursor/one", "submitted": True, "pr": 7, "repair_count": 0}
        self.controller.tasks["one"] = self.record

    def at(self, minutes):
        return patch("build_controller.now", return_value=(dt.datetime(2026, 11, 8,
            tzinfo=dt.timezone.utc) + dt.timedelta(minutes=minutes)).isoformat())

    def tick(self, minutes):
        with self.at(minutes):
            self.controller.tick()

    def merges(self):
        return [c for c in self.remote.calls if c[1] == "pulls/7/merge"]

    def posts(self):
        return [c for c in self.remote.calls if c[0] == "POST" and c[1].endswith("/runs")]

    def stage_comments(self):
        return [c for c in self.remote.calls if c[0] == "POST" and c[1] == "issues/1/comments"
                and c[2]["body"].startswith("Stage report: ")]

    def test_merged_stage_task_reports_once_by_issue_and_email(self):
        self.plan["tasks"] = [dict(SPEC, stage_report=True)]
        self.record["completed"] = {"pr": 7, "merge_sha": BASE, "head": HEAD, "report": "pr"}
        self.remote.pr["body"] = "Intro\n\n## Stage report\nmodule-core merged; AC-002 passed.\n\n## Proposed CI change\nnone"
        self.controller.stage_reports()
        self.controller.stage_reports()
        self.assertEqual(len(self.mailer.sent), 1)
        to, subject, body = self.mailer.sent[0]
        self.assertEqual(to, "lead@example.com")
        self.assertIn("one", subject)
        self.assertIn("module-core merged; AC-002 passed.", body)
        self.assertNotIn("Proposed CI change", body)
        self.assertEqual(len(self.stage_comments()), 1)

    def test_task_without_stage_report_or_unmerged_sends_nothing(self):
        self.record["completed"] = {"pr": 7, "merge_sha": BASE, "head": HEAD, "report": "pr"}
        self.controller.stage_reports()
        self.plan["tasks"] = [dict(SPEC, stage_report=True)]
        self.record.pop("completed")
        self.controller.stage_reports()
        self.assertFalse(self.mailer.sent)
        self.assertFalse(self.stage_comments())

    def test_failed_stage_email_is_retried_without_reposting(self):
        self.plan["tasks"] = [dict(SPEC, stage_report=True)]
        self.record["completed"] = {"pr": 7, "merge_sha": BASE, "head": HEAD, "report": "pr"}
        self.mailer.fail = True
        self.controller.stage_reports()
        self.assertIn("stage_report_failure", self.record)
        self.controller.stage_reports()
        self.mailer.fail = False
        self.record["stage_report_failure"]["time"] = (dt.datetime.now(dt.timezone.utc) - dt.timedelta(hours=1)).isoformat()
        self.controller.stage_reports()
        self.assertEqual(len(self.mailer.sent), 1)
        self.assertIn("no Stage report section", self.mailer.sent[0][2])
        self.assertEqual(len(self.stage_comments()), 1)
        self.assertNotIn("stage_report_failure", self.record)

    def test_dry_run_sends_no_stage_report(self):
        self.controller.dry = True
        self.plan["tasks"] = [dict(SPEC, stage_report=True)]
        self.record["completed"] = {"pr": 7, "merge_sha": BASE, "head": HEAD, "report": "pr"}
        self.controller.stage_reports()
        self.assertFalse(self.mailer.sent)
        self.assertFalse(self.stage_comments())

    def test_merge_refused_on_two_cycles_is_held_and_emailed_once(self):
        self.remote.fail_merge = THREADS_405
        self.remote.pr["mergeable_state"] = "blocked"
        self.tick(0)
        self.assertEqual(len(self.merges()), 1)
        self.assertNotIn("last_error", self.record)
        self.assertNotIn("stalled", self.record)
        self.assertFalse(self.mailer.sent)
        self.tick(3)
        self.assertEqual(len(self.merges()), 2)
        self.assertEqual(self.record["stalled"]["kind"], "merge_blocked")
        self.assertEqual(len(self.mailer.sent), 1)
        to, subject, body = self.mailer.sent[0]
        self.assertEqual(to, "lead@example.com")
        self.assertIn("one", subject)
        self.assertIn("All comments must be resolved", body)
        self.assertIn("https://github.com/CharitonMedia/togavsu/pull/7", body)
        for minutes in (6, 9, 60):
            self.tick(minutes)
        self.assertEqual(len(self.merges()), 2)  # Held: no further merge attempts.
        self.assertEqual(len(self.mailer.sent), 1)  # One email per condition.
        self.assertNotIn("completed", self.record)

    def test_held_merge_retries_once_github_reports_a_new_state_and_merges(self):
        self.remote.fail_merge = THREADS_405
        self.remote.pr["mergeable_state"] = "blocked"
        self.tick(0)
        self.tick(3)
        self.remote.fail_merge = False
        self.remote.pr["mergeable_state"] = "clean"
        self.tick(6)
        self.assertEqual(len(self.merges()), 3)
        self.assertEqual(self.record["completed"]["merge_sha"], NEW)
        self.assertNotIn("merge_blocked", self.record)
        self.assertNotIn("stalled", self.record)

    def test_held_merge_retries_once_per_pull_request_change(self):
        self.remote.fail_merge = THREADS_405
        self.remote.pr["mergeable_state"] = "blocked"
        self.remote.pr["updated_at"] = "2026-11-08T00:00:00Z"
        self.tick(0)
        self.tick(3)
        self.remote.pr["mergeable_state"] = "clean"  # GitHub shows clean but still refuses the merge.
        for minutes in (6, 9, 12, 15):
            self.tick(minutes)
        self.assertEqual(len(self.merges()), 3)  # Exactly one retry for the changed state.
        self.assertTrue(self.record["merge_blocked"]["held"])
        self.assertEqual(len(self.mailer.sent), 2)  # Held again after the retry: a second email.
        self.remote.pr["updated_at"] = "2026-11-08T00:20:00Z"
        for minutes in (18, 21, 24):
            self.tick(minutes)
        self.assertEqual(len(self.merges()), 4)
        self.assertEqual(len(self.mailer.sent), 3)

    def test_new_head_clears_held_merge(self):
        self.remote.fail_merge = THREADS_405
        self.tick(0)
        self.tick(3)
        self.assertTrue(self.record["merge_blocked"]["held"])
        self.remote.fail_merge = False
        self.remote.pr["head"]["sha"] = NEW
        self.remote.context["head"] = NEW
        self.remote.report["reviewed_head"] = NEW
        for row in self.remote.check_rows.values():
            row["head_sha"] = NEW
        with self.at(6):
            self.controller.process(SPEC, self.record, self.remote.agent(self.record), BASE)
        self.assertNotIn("merge_blocked", self.record)
        self.assertNotEqual(self.record.get("stalled", {}).get("kind"), "merge_blocked")

    def test_unrecognised_merge_error_still_raises(self):
        self.remote.fail_merge = True
        with self.assertRaises(RuntimeError):
            self.controller.process(SPEC, self.record, self.remote.agent(self.record), BASE)
        self.assertNotIn("merge_blocked", self.record)

    def test_same_failure_twice_stops_after_one_correction_and_emails(self):
        self.remote.run_status = "ERROR"
        self.tick(0)
        self.assertEqual(len(self.posts()), 1)
        self.remote.run_id = self.record["last_dispatched_run"]
        self.remote.run_status = "ERROR"
        for minutes in (30, 33, 36):
            self.tick(minutes)
        self.assertEqual(len(self.posts()), 1)
        self.assertEqual(self.record["stalled"]["kind"], "no_progress")
        self.assertEqual(len(self.mailer.sent), 1)
        self.assertIn("No more agent runs for this problem", self.mailer.sent[0][2])

    def test_resume_gives_a_stalled_problem_one_more_attempt(self):
        self.remote.run_status = "ERROR"
        self.tick(0)
        self.remote.run_id = self.record["last_dispatched_run"]
        self.tick(30)
        self.assertEqual(self.record["stalled"]["kind"], "no_progress")
        with self.at(40):
            self.controller.resume("one", "Integration lead")
        self.assertNotIn("stalled", self.record)
        self.assertEqual(self.controller.state["operator_resumes"][0]["operator"], "Integration lead")
        self.tick(45)
        self.assertEqual(len(self.posts()), 2)
        with self.assertRaises(ValueError):
            self.controller.resume("one", "Integration lead")  # Not stalled any more.

    def test_resume_resets_every_exhausted_finding_of_a_multi_finding_stall(self):
        self.remote.report.update(verdict="fail", findings=[{"detail": "defect A"}, {"detail": "defect B"}])
        self.remote.check_rows["Codex checkpoint"]["conclusion"] = "failure"
        with self.at(0):
            self.controller.process(SPEC, self.record, self.remote.agent(self.record), BASE)
        self.assertEqual(len(self.posts()), 1)
        self.remote.run_id = self.record["last_dispatched_run"]
        with self.at(30):
            self.controller.process(SPEC, self.record, self.remote.agent(self.record), BASE)
        self.assertEqual(self.record["stalled"]["kind"], "no_progress")
        with self.at(40):
            self.controller.resume("one", "Integration lead")
        with self.at(45):
            self.controller.process(SPEC, self.record, self.remote.agent(self.record), BASE)
        self.assertEqual(len(self.posts()), 2)
        self.assertNotIn("stalled", self.record)

    def test_test_alert_honours_dry_run(self):
        plan_path = Path(self.temp.name) / "plan.json"
        plan_path.write_text(json.dumps(self.plan))
        argv = ["build_controller.py", "--plan", str(plan_path), "--state", str(Path(self.temp.name) / "state"),
                "--dry-run", "--test-alert"]
        with patch.object(sys, "argv", argv), patch("build_controller.Mailer.send") as send:
            build_controller.main()
        send.assert_not_called()

    def test_operator_note_leads_every_later_fix_pass_until_cleared(self):
        with self.at(0):
            self.controller.note("one", "Integration lead", "Fix the ledger seal")
        self.assertEqual(self.record["operator_notes"][0]["operator"], "Integration lead")
        self.remote.run_status = "ERROR"
        self.tick(5)
        text = self.posts()[0][2]["prompt"]["text"]
        self.assertTrue(text.startswith("Integration lead instructions"))
        self.assertIn("- Fix the ledger seal", text)
        with self.at(10):
            self.controller.clear_notes("one", "Integration lead")
        self.assertNotIn("operator_notes", self.record)
        with self.assertRaises(ValueError):
            self.controller.clear_notes("one", "Integration lead")

    def test_operator_note_needs_operator_text_and_an_open_task(self):
        for operator, text in (("", "x"), ("Integration lead", " "), ("Integration lead", "x" * 4001)):
            with self.assertRaises(ValueError):
                self.controller.note("one", operator, text)
        self.record["completed"] = True
        with self.assertRaises(ValueError):
            self.controller.note("one", "Integration lead", "x")
        self.assertNotIn("operator_notes", self.record)

    def test_operator_note_changes_no_budget_or_halt(self):
        self.record["halted"] = "Delivery outcome uncertain; no automatic replay"
        self.controller.note("one", "Integration lead", "Fix the ledger seal")
        self.assertIn("halted", self.record)
        self.assertNotIn("stalled", self.record)
        self.assertEqual(self.record.get("correction_volume", 0), 0)

    def test_operator_note_cli_requires_an_operator(self):
        plan_path = Path(self.temp.name) / "plan.json"
        plan_path.write_text(json.dumps(self.plan))
        argv = ["build_controller.py", "--plan", str(plan_path), "--state", str(Path(self.temp.name) / "state"),
                "--note", "one", "x"]
        with patch.object(sys, "argv", argv), self.assertRaises(SystemExit):
            build_controller.main()

    def test_operator_note_cli_passes_task_operator_and_text_in_order(self):
        plan_path = Path(self.temp.name) / "plan.json"
        plan_path.write_text(json.dumps(self.plan))
        argv = ["build_controller.py", "--plan", str(plan_path), "--state", str(Path(self.temp.name) / "state"),
                "--note", "one", "Fix the ledger exposure", "--operator", "Integration lead"]
        with patch.object(sys, "argv", argv), patch.object(build_controller.Controller, "note") as note:
            build_controller.main()
        note.assert_called_once_with("one", "Integration lead", "Fix the ledger exposure")

    def test_resume_never_clears_a_halt(self):
        self.record["halted"] = "Delivery outcome uncertain; no automatic replay"
        with self.assertRaises(ValueError):
            self.controller.resume("one", "Integration lead")
        self.assertIn("halted", self.record)

    def test_halt_is_emailed_once_and_again_only_after_it_clears(self):
        self.record["halted"] = "PR is already assigned to another task"
        self.tick(0)
        self.tick(3)
        self.assertEqual(len(self.mailer.sent), 1)
        self.assertIn("PR is already assigned to another task", self.mailer.sent[0][2])
        self.record.pop("halted")
        self.remote.run_status = "RUNNING"
        self.tick(6)
        self.record["halted"] = "PR is already assigned to another task"
        self.tick(9)
        self.assertEqual(len(self.mailer.sent), 2)

    def test_repeating_control_error_alerts_after_three_ticks(self):
        self.remote.checks = lambda head: (_ for _ in ()).throw(RuntimeError("gh: Server Error (HTTP 502)"))
        self.tick(0)
        self.tick(3)
        self.assertFalse(self.mailer.sent)
        self.tick(6)
        self.assertEqual(len(self.mailer.sent), 1)
        self.assertIn("Same control error on 3 ticks", self.mailer.sent[0][2])

    def test_no_progress_for_an_hour_alerts_but_active_runs_do_not(self):
        self.remote.run_status = "RUNNING"
        for minutes in (0, 30, 61, 90):
            self.tick(minutes)
        self.assertFalse(self.mailer.sent)
        def pending(check):
            raise ReviewPending("queued")
        self.remote.review = pending  # Waits on the review workflow, with no deadline of its own.
        self.remote.run_status = "FINISHED"
        self.tick(100)
        self.tick(130)
        self.assertFalse(self.mailer.sent)
        self.tick(161)
        self.assertEqual(len(self.mailer.sent), 1)
        self.assertIn("No progress for 61 minutes", self.mailer.sent[0][2])

    def test_check_still_running_emails_after_an_hour_and_never_halts(self):
        self.remote.check_rows["Codex checkpoint"]["status"] = "in_progress"
        self.remote.run_status = "FINISHED"
        for minutes in (0, 30):
            self.tick(minutes)
        self.assertFalse(self.mailer.sent)
        self.tick(61)
        self.assertEqual(len(self.mailer.sent), 1)
        self.assertIn("No progress for 61 minutes", self.mailer.sent[0][2])
        self.tick(400)
        self.assertNotIn("halted", self.record)
        self.assertIn("Checks pending", self.record["waiting"])

    def test_failed_alert_email_never_breaks_the_tick_and_retries_later(self):
        self.mailer.fail = True
        self.record["halted"] = "PR is already assigned to another task"
        self.tick(0)
        self.assertIn("mail server unreachable", self.controller.state["alert_failure"]["error"])
        self.assertEqual(self.controller.state["health"], "needs_attention")
        self.mailer.fail = False
        self.tick(5)
        self.assertFalse(self.mailer.sent)  # Waits before trying the mail server again.
        self.tick(16)
        self.assertEqual(len(self.mailer.sent), 1)
        self.assertNotIn("alert_failure", self.controller.state)

    def test_no_alert_email_configured_sends_nothing(self):
        self.plan.pop("alert_email")
        self.record["halted"] = "PR is already assigned to another task"
        self.tick(0)
        self.assertFalse(self.mailer.sent)
        self.assertNotIn("alerts_sent", self.controller.state)

    def test_dry_run_sends_no_alert(self):
        self.record["halted"] = "PR is already assigned to another task"
        self.controller.dry = True
        self.tick(0)
        self.assertFalse(self.mailer.sent)

    def test_pinned_plan_alerts_and_stops_on_second_repeat(self):
        plan = json.loads((Path(__file__).resolve().parents[1] / "docs/build/controller-plan.json").read_text())
        self.assertTrue(plan["alert_email"])
        self.assertEqual(plan["max_repairs"], 1)
        self.assertGreaterEqual(plan["active_run_timeout_seconds"], 300 * 60)


class ReviewDataTests(unittest.TestCase):
    def test_agent_lifecycle_without_run_never_proves_completion(self):
        for status in (None, "IDLE", "ARCHIVED", "FINISHED", "ERROR", "CANCELLED", "EXPIRED", [], {}):
            for latest in ({}, {"latestRunId": None}, {"latestRunId": ""}):
                with self.subTest(status=status, latest=latest):
                    remote = Remote()
                    reads = []
                    def cursor(method, path):
                        reads.append(path)
                        return {"id": "bc-one", "status": status, **latest}
                    remote.cursor = cursor
                    with self.assertRaises(ObservationError):
                        remote.agent({"agent_id": "bc-one"})
                    self.assertEqual(reads, ["/agents/bc-one"])
        remote.cursor = lambda *args: {"id": "bc-one", "status": "ACTIVE"}
        snapshot = remote.agent({"agent_id": "bc-one"})
        self.assertEqual(snapshot["status"], "ACTIVE")
        self.assertIsNone(snapshot["run_id"])

    def test_malformed_agent_identity_or_latest_run_is_not_an_observation(self):
        agents = [None, [], {}, {"id": "bc-other", "status": "ACTIVE"}] + [
            {"id": "bc-one", "status": "ACTIVE", "latestRunId": value} for value in (True, 7, [], {}, " ")]
        for agent in agents:
            with self.subTest(agent=agent):
                remote = Remote()
                remote.cursor = lambda *args: agent
                with self.assertRaises(ObservationError):
                    remote.agent({"agent_id": "bc-one"})

    def test_run_identity_and_status_are_validated_without_agent_fallback(self):
        valid = {"id": "R3", "agentId": "bc-one", "status": "FINISHED"}
        invalid = [None, [], {}, {"agentId": "bc-one", "status": "FINISHED"},
                   {"id": "R3", "status": "FINISHED"}, {"id": "R3", "agentId": "bc-one"},
                   dict(valid, id="R1"), dict(valid, agentId="bc-other")] + [
                   dict(valid, status=status) for status in (None, "", "UNKNOWN", "ACTIVE", "IDLE", "STOPPED", 0, True, [], {})]
        for run in invalid:
            with self.subTest(run=run):
                remote = Remote()
                remote.cursor = lambda method, path: run if "/runs/" in path else {
                    "id": "bc-one", "latestRunId": "R3", "status": "ACTIVE"}
                with self.assertRaises(ObservationError):
                    remote.agent({"agent_id": "bc-one"})
        for status in ("CREATING", "RUNNING", "WAITING_FOR_BACKGROUND_WORK", "FINISHED", "ERROR", "CANCELLED", "EXPIRED"):
            with self.subTest(valid_status=status):
                remote.cursor = lambda method, path: dict(valid, status=status) if "/runs/" in path else {
                    "id": "bc-one", "latestRunId": "R3", "status": "IDLE"}
                snapshot = remote.agent({"agent_id": "bc-one"})
                self.assertEqual(snapshot["status"], status)
                self.assertEqual(snapshot["run_id"], "R3")

    def test_only_agent_endpoint_404_establishes_agent_absence(self):
        for endpoint, code in (("agent", 404), ("run", 404), ("agent", 403), ("run", 503)):
            with self.subTest(endpoint=endpoint, code=code):
                remote = Remote()
                calls = []
                def cursor(method, path, body=None):
                    calls.append(path)
                    if endpoint == "agent" or path.endswith("/runs/R3"):
                        raise urllib.error.HTTPError("https://api.cursor.com/v1" + path, code, "unavailable", {}, None)
                    return {"id": "bc-one", "latestRunId": "R3", "status": "RUNNING"}
                remote.cursor = cursor
                expected = AgentNotFound if endpoint == "agent" and code == 404 else urllib.error.HTTPError
                with self.assertRaises(expected):
                    remote.agent({"agent_id": "bc-one"})
                self.assertEqual(calls, ["/agents/bc-one"] + (["/agents/bc-one/runs/R3"] if endpoint == "run" else []))

    def test_state_durability_orders_file_sync_replace_then_directory_sync(self):
        events = []
        sync, replace = os.fsync, os.replace
        def fsync(fd):
            events.append("directory-sync" if stat.S_ISDIR(os.fstat(fd).st_mode) else "file-sync")
            sync(fd)
        def rename(a,b):
            events.append("replace")
            replace(a,b)
        with tempfile.TemporaryDirectory() as folder:
            target = Path(folder) / "state.json"
            with patch("build_controller.os.fsync",side_effect=fsync), patch("build_controller.os.replace",side_effect=rename):
                atomic(target,{"agent_id":"durable"})
            self.assertEqual(json.loads(target.read_text())["agent_id"],"durable")
        self.assertEqual(events,["file-sync","replace","directory-sync"])
    def test_non_string_verdict_types_are_rejected(self):
        r = FakeRemote()
        for value in [[], {}]:
            report = dict(r.report, verdict=value)
            with self.assertRaises(ValueError):
                validate_review(report, r.context, r.pr, BASE)

    def test_cancelled_review_does_not_fetch_artifacts_or_replay_the_old_event(self):
        remote, fetched = Remote(), []
        def gh(path, **kwargs):
            fetched.append(path)
            if path == "actions/jobs/456":
                return {"id": 456, "run_id": 123,
                    "check_run_url": "https://api.github.com/repos/CharitonMedia/togavsu/check-runs/1",
                    "head_sha": HEAD, "name": "Codex checkpoint", "status": "completed", "conclusion": "cancelled"}
            if path == "actions/runs/123":
                return {"path": ".github/workflows/codex-checkpoint.yml", "event": "pull_request",
                    "head_sha": HEAD, "status": "completed", "conclusion": "cancelled", "run_attempt": 1}
            raise AssertionError("Cancelled event must not be replayed or read for artifacts: " + path)
        remote.gh = gh
        with self.assertRaises(ReviewSuperseded):
            remote.review(check("Codex checkpoint", conclusion="cancelled"))
        self.assertFalse(any("artifacts" in path or "rerun" in path for path in fetched))

    def test_retry_selects_attempt_two_artifact_over_existing_attempt_one(self):
        r = FakeRemote()
        output = io.BytesIO()
        with zipfile.ZipFile(output, "w") as archive:
            archive.writestr("review-result.json", json.dumps(r.report))
            archive.writestr("review-context.json", json.dumps(r.context))
        remote, fetched = Remote(), []
        def gh(path, **kwargs):
            fetched.append(path)
            if path == "actions/jobs/456":
                return {"id":456,"run_id":123,"check_run_url":"https://api.github.com/repos/CharitonMedia/togavsu/check-runs/1","head_sha":HEAD,"name":"Codex checkpoint","status":"completed","conclusion":"success"}
            if path == "actions/runs/123":
                return {"path": ".github/workflows/codex-checkpoint.yml", "head_sha": HEAD, "event": "pull_request", "status": "completed", "conclusion":"success", "run_attempt": 2}
            if path.endswith("/artifacts?per_page=100"):
                return {"artifacts": [{"name": "codex-review-" + HEAD + "-attempt-" + str(n), "id": n, "expired": False} for n in [1,2]]}
            if path == "actions/artifacts/2/zip":
                return output.getvalue()
            raise AssertionError(path)
        remote.gh = gh
        report, context = remote.review(check("Codex checkpoint"))
        self.assertEqual(report["verdict"], "pass")
        self.assertIn("actions/artifacts/2/zip", fetched)

    def test_forged_check_copying_real_url_cannot_hide_newer_genuine_failure(self):
        remote = Remote()
        genuine_pass = check("Codex checkpoint","success",1)
        genuine_pass["details_url"]="https://github.com/CharitonMedia/togavsu/actions/runs/101/job/201"
        genuine_fail = check("Codex checkpoint","failure",2)
        genuine_fail["details_url"]="https://github.com/CharitonMedia/togavsu/actions/runs/102/job/202"
        forged = dict(genuine_pass,id=3)
        def gh(path, **kwargs):
            if path.startswith("commits/"):
                return {"check_runs":[genuine_pass,genuine_fail,forged]}
            if path.startswith("actions/jobs/"):
                job=int(path.rsplit("/",1)[1]); ident=job-200
                return {"id":job,"run_id":job-100,"check_run_url":"https://api.github.com/repos/CharitonMedia/togavsu/check-runs/"+str(ident),"head_sha":HEAD,"name":"Codex checkpoint","status":"completed","conclusion":"failure" if ident==2 else "success"}
            if path.startswith("actions/runs/"):
                return {"path":".github/workflows/codex-checkpoint.yml","event":"pull_request","head_sha":HEAD}
            raise AssertionError(path)
        remote.gh=gh
        selected=remote.checks(HEAD)["Codex checkpoint"]
        self.assertEqual(selected["id"],2)
        self.assertEqual(selected["conclusion"],"failure")
        with self.assertRaises(ValueError):
            remote.review(forged)

    def test_forged_product_check_cannot_override_native_job_outcome(self):
        remote = Remote()
        real = check("Core tests", "failure", 1)
        forged = dict(real,id=2,conclusion="success")
        def gh(path, **kwargs):
            if path.startswith("commits/"):
                return {"check_runs":[real,forged]}
            if path == "actions/jobs/456":
                return {"id":456,"run_id":123,"check_run_url":"https://api.github.com/repos/CharitonMedia/togavsu/check-runs/1","head_sha":HEAD,"name":"Core tests","status":"completed","conclusion":"failure"}
            if path == "actions/runs/123":
                return {"path":".github/workflows/product-ci.yml","event":"pull_request","head_sha":HEAD,"status":"completed","conclusion":"failure"}
            raise AssertionError(path)
        remote.gh=gh
        selected=remote.checks(HEAD)["Core tests"]
        self.assertEqual(selected["id"],1)
        self.assertEqual(selected["conclusion"],"failure")
    def test_null_review_or_context_is_rejected(self):
        r = FakeRemote()
        for report, context in [(None,r.context),(r.report,None)]:
            with self.assertRaises(ValueError):
                validate_review(report, context, r.pr, BASE)
    def test_stale_or_other_pr_report_rejected(self):
        r = FakeRemote()
        validate_review(r.report, r.context, r.pr, BASE)
        for context in [dict(r.context, head=NEW), dict(r.context, base=NEW), dict(r.context, pr=8)]:
            with self.assertRaises(ValueError):
                validate_review(r.report, context, r.pr, BASE)

    def test_failed_or_foreign_check_cannot_spoof_actions_success(self):
        trusted = check("Codex checkpoint", "failure", 10)
        forged = check("Codex checkpoint", "success", 99)
        forged["app"]["id"] = 777
        self.assertEqual(latest_checks([trusted, forged])["Codex checkpoint"]["conclusion"], "failure")

    def test_duplicate_artifact_documents_rejected(self):
        output = io.BytesIO()
        with zipfile.ZipFile(output, "w") as z:
            z.writestr("a/review-result.json", "{}")
            z.writestr("b/review-result.json", "{}")
            z.writestr("review-context.json", "{}")
        with self.assertRaises(ValueError):
            extract_review(output.getvalue())

    def test_same_named_job_from_another_workflow_cannot_impersonate_codex(self):
        remote = Remote()
        remote.gh = lambda *a, **k: {"path": ".github/workflows/product-ci.yml", "event": "pull_request", "head_sha": HEAD}
        with self.assertRaises(ValueError):
            remote.review(check("Codex checkpoint"))


LANE_AGENT = "bc-12345678-1234-4234-8234-123456789abc"


class FakeLaneRemote(build_controller.ActionsRemote):
    """ActionsRemote with GitHub replaced by memory and the push target replaced by a local bare repo."""

    def __init__(self, state_dir, repo_dir, bare, dry=False):
        super().__init__(state_dir, repo_dir, dry)
        self.push_url = str(bare)
        self.bare = bare
        self.calls, self.runs, self.artifacts, self.pulls = [], [], {}, []
        self.dispatch_error = None

    def gh(self, path, method="GET", payload=None, binary=False):
        self.calls.append((method, path, payload))
        if path.startswith("git/ref/heads/"):
            branch = urllib.parse.unquote(path[len("git/ref/heads/"):])
            proc = subprocess.run(["git", "-C", str(self.bare), "rev-parse", "--verify", "-q", "refs/heads/" + branch],
                                  capture_output=True, text=True)
            if proc.returncode:
                raise RuntimeError("gh: Not Found (HTTP 404)")
            return {"object": {"sha": proc.stdout.strip()}}
        if path.endswith("/dispatches"):
            if self.dispatch_error:
                raise RuntimeError(self.dispatch_error)
            return {}
        if "/runs?event=workflow_dispatch" in path:
            return {"workflow_runs": self.runs}
        if path.startswith("actions/runs/") and path.endswith("/artifacts?per_page=100"):
            return {"artifacts": [{"id": 5, "name": name, "expired": False, "size_in_bytes": len(blob)}
                                  for name, blob in self.artifacts.items()]}
        if path.startswith("actions/runs/"):
            return next(r for r in self.runs if str(r["id"]) == path.split("/")[2])
        if path == "actions/artifacts/5/zip":
            return next(iter(self.artifacts.values()))
        if path.startswith("pulls?state=all"):
            return list(self.pulls)
        if path == "pulls" and method == "POST":
            pr = {"number": 31, "html_url": "https://github.com/CharitonMedia/togavsu/pull/31", "body": payload["body"], "state": "open"}
            self.pulls.append(pr)
            return pr
        if path == "pulls/31":
            if method == "PATCH":
                self.pulls[0]["body"] = payload["body"]
            return self.pulls[0]
        raise AssertionError("unexpected gh call " + method + " " + path)


class LaneBackendTests(unittest.TestCase):
    def git(self, cwd, *args):
        return subprocess.run(["git", "-C", str(cwd)] + list(args), check=True, capture_output=True, text=True).stdout.strip()

    def setUp(self):
        env = patch.dict(os.environ)
        env.start()
        self.addCleanup(env.stop)
        os.environ.pop("CURSOR_API_KEY", None)
        # No background gc or maintenance, which can still be writing .git when the directory is removed.
        os.environ.update({"GIT_CONFIG_COUNT": "2", "GIT_CONFIG_KEY_0": "gc.auto", "GIT_CONFIG_VALUE_0": "0",
                           "GIT_CONFIG_KEY_1": "maintenance.auto", "GIT_CONFIG_VALUE_1": "false"})
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.bare, self.control, self.worker = root / "origin.git", root / "control", root / "worker"
        subprocess.run(["git", "init", "-q", "--bare", str(self.bare)], check=True)
        subprocess.run(["git", "init", "-q", str(self.control)], check=True)
        for repo in (self.control,):
            self.git(repo, "config", "user.email", "t@example.com")
            self.git(repo, "config", "user.name", "t")
        (self.control / "README").write_text("base\n")
        self.git(self.control, "add", "README")
        self.git(self.control, "commit", "-q", "-m", "base")
        self.base = self.git(self.control, "rev-parse", "HEAD")
        self.git(self.control, "push", "-q", str(self.bare), "HEAD:refs/heads/main")
        self.state = self.control / ".build-state"
        self.state.mkdir()
        self.remote = FakeLaneRemote(self.state, self.control, self.bare)
        self.record = {"agent_id": LANE_AGENT}

    def launch(self, prompt="Implement Togavsu assignment one."):
        return self.remote.cursor("POST", "/agents", {"agentId": LANE_AGENT, "name": "Togavsu one",
            "prompt": {"text": prompt}, "repos": [{"url": "https://github.com/CharitonMedia/togavsu", "startingRef": self.base}]})

    def lane_commit(self, base, text, files=None):
        if not self.worker.exists():
            subprocess.run(["git", "clone", "-q", str(self.bare), str(self.worker)], check=True)
            self.git(self.worker, "config", "user.email", "t@example.com")
            self.git(self.worker, "config", "user.name", "t")
        self.git(self.worker, "fetch", "-q", "origin")
        self.git(self.worker, "checkout", "-q", "-B", "lane", base)
        for name, content in (files or {"work.txt": text}).items():
            path = self.worker / name
            path.parent.mkdir(parents=True, exist_ok=True)
            if content is None:
                path.symlink_to(".github")
            else:
                path.write_text(content)
            self.git(self.worker, "add", name)
        self.git(self.worker, "commit", "-q", "-m", text)
        head = self.git(self.worker, "rev-parse", "HEAD")
        bundle = Path(self.temp.name) / "lane.bundle"
        self.git(self.worker, "bundle", "create", str(bundle), "refs/heads/lane", "^" + base)
        return head, bundle.read_bytes()

    def finish(self, lane_run, base, head, bundle, conclusion="success", **result):
        run_id = lane_run
        data = {"agent_id": LANE_AGENT, "run_id": run_id, "task": "one", "branch": "claude/togavsu-one-12345678",
                "base": base, "head": head, "pr_title": "One", "pr_body": "Evidence for " + head}
        data.update(result)
        out = io.BytesIO()
        with zipfile.ZipFile(out, "w") as archive:
            archive.writestr("lane-result.json", json.dumps(data))
            if bundle is not None:
                archive.writestr("lane.bundle", bundle)
        self.remote.artifacts = {"lane-" + run_id: out.getvalue()}
        self.remote.runs = [{"id": 900 + len(self.remote.runs), "path": ".github/workflows/lane-agent.yml",
            "event": "workflow_dispatch", "head_branch": "main", "display_title": "Lane " + run_id,
            "status": "completed", "conclusion": conclusion, "updated_at": "2026-11-09T23:00:00Z"}]

    def test_launch_persists_run_before_dispatch_and_conflict_is_not_resent(self):
        result = self.launch()
        run_id = result["run"]["id"]
        registry = json.loads((self.state / "lane-agents.json").read_text())
        self.assertEqual(registry[LANE_AGENT]["runs"][0]["id"], run_id)
        self.assertEqual(registry[LANE_AGENT]["runs"][0]["state"], "dispatched")
        dispatch = [c for c in self.remote.calls if c[1].endswith("/dispatches")]
        self.assertEqual(len(dispatch), 1)
        inputs = dispatch[0][2]["inputs"]
        self.assertEqual(inputs["base"], self.base)
        self.assertEqual(zlib.decompress(base64.b64decode(inputs["prompt_z"])).decode(), "Implement Togavsu assignment one.")
        with self.assertRaises(urllib.error.HTTPError) as error:
            self.launch()
        self.assertEqual(error.exception.code, 409)
        self.assertEqual(len([c for c in self.remote.calls if c[1].endswith("/dispatches")]), 1)

    def test_unknown_agent_is_agent_not_found(self):
        with self.assertRaises(AgentNotFound):
            self.remote.agent(self.record)

    def test_unseen_and_running_runs_stay_active(self):
        run_id = self.launch()["run"]["id"]
        snapshot = self.remote.agent(self.record)
        self.assertEqual((snapshot["status"], snapshot["run_id"]), ("CREATING", run_id))
        self.remote.runs = [{"id": 900, "path": ".github/workflows/lane-agent.yml", "event": "workflow_dispatch",
            "head_branch": "main", "display_title": "Lane " + run_id, "status": "in_progress"}]
        self.assertEqual(self.remote.agent(self.record)["status"], "RUNNING")
        self.remote.runs[0]["path"] = ".github/workflows/other.yml"
        with self.assertRaises(ObservationError):
            self.remote.agent(self.record)

    def test_finished_run_is_pushed_without_force_and_opens_one_pr(self):
        run_id = self.launch()["run"]["id"]
        head, bundle = self.lane_commit(self.base, "first")
        self.finish(run_id, self.base, head, bundle)
        snapshot = self.remote.agent(self.record)
        build_controller.validate_snapshot(snapshot)
        self.assertEqual(snapshot["status"], "FINISHED")
        self.assertEqual(self.git(self.bare, "rev-parse", "refs/heads/claude/togavsu-one-12345678"), head)
        self.assertEqual(snapshot["git"]["branches"][0]["prUrl"], "https://github.com/CharitonMedia/togavsu/pull/31")
        self.assertEqual(len(self.remote.pulls), 1)
        self.remote.agent(self.record)  # Re-observation never republishes.
        self.assertEqual(len([c for c in self.remote.calls if c[:2] == ("POST", "pulls")]), 1)
        self.assertEqual(self.git(self.control, "for-each-ref", "refs/lane-import/"), "")
        self.assertEqual(list(self.state.glob(".lane-*")), [])

    def test_followup_starts_from_branch_head_and_carries_assignment(self):
        run_id = self.launch("Original assignment text")["run"]["id"]
        head, bundle = self.lane_commit(self.base, "first")
        self.finish(run_id, self.base, head, bundle)
        self.remote.agent(self.record)
        second = self.remote.cursor("POST", "/agents/" + LANE_AGENT + "/runs", {"prompt": {"text": "Fix finding A"}})["run"]["id"]
        inputs = [c for c in self.remote.calls if c[1].endswith("/dispatches")][-1][2]["inputs"]
        self.assertEqual(inputs["base"], head)
        prompt = zlib.decompress(base64.b64decode(inputs["prompt_z"])).decode()
        self.assertIn("Original assignment text", prompt)
        self.assertIn("Fix finding A", prompt)
        newer, bundle = self.lane_commit(head, "second")
        self.finish(second, head, newer, bundle, pr_body="Updated evidence")
        self.assertEqual(self.remote.agent(self.record)["status"], "FINISHED")
        self.assertEqual(self.git(self.bare, "rev-parse", "refs/heads/claude/togavsu-one-12345678"), newer)
        self.assertEqual(self.remote.pulls[0]["body"], "Updated evidence")

    def test_moved_branch_or_mismatched_result_is_rejected_without_push(self):
        run_id = self.launch()["run"]["id"]
        head, bundle = self.lane_commit(self.base, "first")
        other, _ = self.lane_commit(self.base, "someone else")
        self.git(self.worker, "push", "-q", "origin", other + ":refs/heads/claude/togavsu-one-12345678")
        self.finish(run_id, self.base, head, bundle)
        snapshot = self.remote.agent(self.record)
        self.assertEqual(snapshot["status"], "ERROR")
        self.assertEqual(self.git(self.bare, "rev-parse", "refs/heads/claude/togavsu-one-12345678"), other)
        registry = json.loads((self.state / "lane-agents.json").read_text())
        self.assertIn("fast-forward", registry[LANE_AGENT]["runs"][0]["rejected"]["reason"])

    def test_result_for_another_run_is_rejected(self):
        run_id = self.launch()["run"]["id"]
        head, bundle = self.lane_commit(self.base, "first")
        self.finish(run_id, self.base, head, bundle, run_id="run-00000000-0000-4000-8000-000000000000")
        self.assertEqual(self.remote.agent(self.record)["status"], "ERROR")
        self.assertIsNone(self.remote.branch_head("claude/togavsu-one-12345678"))

    def test_failed_run_still_publishes_partial_work_but_reports_error(self):
        run_id = self.launch()["run"]["id"]
        head, bundle = self.lane_commit(self.base, "partial")
        self.finish(run_id, self.base, head, bundle, conclusion="failure")
        self.assertEqual(self.remote.agent(self.record)["status"], "ERROR")
        self.assertEqual(self.remote.branch_head("claude/togavsu-one-12345678"), head)

    def test_dry_run_never_publishes(self):
        run_id = self.launch()["run"]["id"]
        head, bundle = self.lane_commit(self.base, "first")
        self.finish(run_id, self.base, head, bundle)
        dry = FakeLaneRemote(self.state, self.control, self.bare, dry=True)
        dry.runs, dry.artifacts = self.remote.runs, self.remote.artifacts
        self.assertEqual(dry.agent(self.record)["status"], "RUNNING")
        self.assertIsNone(dry.branch_head("claude/togavsu-one-12345678"))

    def test_oversized_prompt_is_refused_before_dispatch(self):
        with self.assertRaises(ValueError):
            self.launch(os.urandom(60_000).hex())
        self.assertFalse([c for c in self.remote.calls if c[1].endswith("/dispatches")])
        self.assertFalse((self.state / "lane-agents.json").exists())

    def test_base_missing_from_control_checkout_is_fetched_before_verify(self):
        # Main advanced by an API squash merge that the control checkout never fetched.
        subprocess.run(["git", "clone", "-q", str(self.bare), str(self.worker)], check=True)
        self.git(self.worker, "config", "user.email", "t@example.com")
        self.git(self.worker, "config", "user.name", "t")
        self.git(self.worker, "checkout", "-q", "-B", "main", "origin/main")
        (self.worker / "merged.txt").write_text("merged\n")
        self.git(self.worker, "add", "merged.txt")
        self.git(self.worker, "commit", "-q", "-m", "squash merge")
        self.git(self.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
        self.base = self.git(self.worker, "rev-parse", "HEAD")
        self.assertNotEqual(subprocess.run(["git", "-C", str(self.control), "cat-file", "-e", self.base]).returncode, 0)
        run_id = self.launch()["run"]["id"]
        head, bundle = self.lane_commit(self.base, "first")
        self.finish(run_id, self.base, head, bundle)
        self.assertEqual(self.remote.agent(self.record)["status"], "FINISHED")
        self.assertEqual(self.git(self.bare, "rev-parse", "refs/heads/claude/togavsu-one-12345678"), head)

    def test_agents_launched_on_cursor_keep_using_cursor(self):
        legacy = {"agent_id": "bc-c1cb681b-2606-42d3-b619-5750b5561c4a"}
        seen = []
        def cursor(remote, method, path, body=None):
            seen.append((method, path))
            if path == "/agents/" + legacy["agent_id"]:
                return {"id": legacy["agent_id"], "status": "ACTIVE", "latestRunId": "run-cursor"}
            return {"id": "run-cursor", "agentId": legacy["agent_id"], "status": "RUNNING", "git": {}}
        os.environ["CURSOR_API_KEY"] = "test-only"
        with patch.object(Remote, "cursor", cursor):
            snapshot = self.remote.agent(legacy)
            self.remote.cursor("POST", "/agents/" + legacy["agent_id"] + "/runs", {"prompt": {"text": "fix"}})
        self.assertEqual(snapshot["status"], "RUNNING")
        self.assertEqual([m for m, _ in seen], ["GET", "GET", "POST"])
        self.assertFalse([c for c in self.remote.calls if c[1].endswith("/dispatches")])
        os.environ.pop("CURSOR_API_KEY")
        with self.assertRaises(AgentNotFound):
            self.remote.agent(legacy)

    def test_lane_cannot_publish_control_or_unsafe_workflow_changes(self):
        safe = "name: Product CI\non: [push]\npermissions:\n  contents: read\njobs: {}\n"
        cases = {
            "new workflow": {".github/workflows/x.yml": safe},
            "review gate": {".github/workflows/codex-checkpoint.yml": safe},
            "controller": {"scripts/build_controller.py": "pass\n"},
            "installer": {"deploy/controller/run_controller.py": "pass\n"},
            "symlink": {"internal/one/link": None},
            "owned read-only workflow": {".github/workflows/product-ci.yml": safe},
            "escaped write scope": {".github/workflows/product-ci.yml": safe.replace("read", '"wr\\x69te"')},
            "repository setting": {".github/CODEOWNERS": "* @someone\n"},
            "non-ASCII workflow name": {".github/workflows/\u00e9.yml": safe + "# ${{ secrets.X }}\n"},
            "backslash workflow name": {".github/workflows/a\\b.yml": safe},
            "non-ASCII control file": {"deploy/controller/\u00e9.py": "pass\n"},
            "modules PRD": {"togavsu-initial-modules-prd.md": "scope\n"},
            "module core changes": {"docs/modules/CORE-CHANGES.md": "scope\n"},
            "module spec": {"docs/modules/email/SPEC.md": "scope\n"},
        }
        for label, files in cases.items():
            with self.subTest(label):
                self.setUp()
                run_id = self.launch()["run"]["id"]
                head, bundle = self.lane_commit(self.base, label, files)
                self.finish(run_id, self.base, head, bundle)
                self.assertEqual(self.remote.agent(self.record)["status"], "ERROR")
                self.assertNotEqual(subprocess.run(["git", "-C", str(self.bare), "rev-parse", "--verify", "-q",
                    "refs/heads/claude/togavsu-one-12345678"], capture_output=True).returncode, 0)
                self.assertFalse(self.remote.pulls)

    def test_refused_create_dispatch_leaves_no_agent_for_a_clean_retry(self):
        self.remote.dispatch_error = "gh: No ref found for: main (HTTP 422)"
        with self.assertRaises(RuntimeError):
            self.launch()
        with self.assertRaises(AgentNotFound):
            self.remote.agent(self.record)
        self.remote.dispatch_error = None
        self.assertTrue(self.launch()["run"]["id"])

    def test_refused_or_lost_followup_reports_error_not_creating(self):
        self.launch()
        self.remote.dispatch_error = "gh: Server Error (HTTP 422)"
        with self.assertRaises(RuntimeError):
            self.remote.cursor("POST", "/agents/" + LANE_AGENT + "/runs", {"prompt": {"text": "fix"}})
        self.assertEqual(self.remote.agent(self.record)["status"], "ERROR")
        self.remote.dispatch_error = "gh: connection reset"  # Ambiguous: maybe created, so wait, then give up.
        with self.assertRaises(RuntimeError):
            self.remote.cursor("POST", "/agents/" + LANE_AGENT + "/runs", {"prompt": {"text": "fix again"}})
        self.assertEqual(self.remote.agent(self.record)["status"], "CREATING")
        registry = json.loads((self.state / "lane-agents.json").read_text())
        registry[LANE_AGENT]["runs"][-1]["dispatched_at"] = "2026-01-01T00:00:00+00:00"
        (self.state / "lane-agents.json").write_text(json.dumps(registry))
        self.assertEqual(self.remote.agent(self.record)["status"], "ERROR")

    def test_operator_rehomes_a_stopped_cursor_record(self):
        plan = {"worker_backend": "claude-actions", "max_implementation_agents": 4, "tasks": [
            {"id": "one", "depends_on": [], "owned_paths": ["internal/one/"]}]}
        cursor_agent = "bc-c1cb681b-2606-42d3-b619-5750b5561c4a"
        stopped = {"agent_id": cursor_agent, "agent_status": "ERROR", "submitted": True, "halted": "Delivery outcome uncertain"}
        (self.state / "controller.json").write_text(json.dumps({"tasks": {"one": dict(stopped)}, "events": [], "tick": 0}))
        controller = Controller(self.remote, plan, self.state)
        controller.rehome("one", "integration lead")
        saved = json.loads((self.state / "controller.json").read_text())
        self.assertNotIn("one", saved["tasks"])
        self.assertEqual(saved["retired_records"]["one"][0]["record"]["agent_id"], cursor_agent)
        self.assertIn("retired Cursor agent", saved["events"][-1]["message"])
        refusals = {"open PR": dict(stopped, pr=40), "still running": {"agent_id": cursor_agent, "agent_status": "RUNNING"},
                    "halted but running": {"agent_id": cursor_agent, "agent_status": "RUNNING", "halted": "x"},
                    "already a lane": dict(stopped, agent_id=LANE_AGENT), "finished": dict(stopped, completed={"pr": 1})}
        self.launch()
        for label, record in refusals.items():
            with self.subTest(label):
                controller.tasks["one"] = record
                with self.assertRaises(ValueError):
                    controller.rehome("one", "integration lead")


class LanePackageScanTests(unittest.TestCase):
    """Runs the Package result step of the protected lane workflow against a local repository."""

    # A piece of a real repository secret still withholds the whole result.
    TOKEN = "sk-ant-oat01-" + "Q" * 48

    def setUp(self):
        text = (Path(__file__).resolve().parents[1] / ".github/workflows/lane-agent.yml").read_text()
        step = text[text.index("- name: Package result"):]
        start = step.index("python3 - <<'PY'\n") + len("python3 - <<'PY'\n")
        lines = []
        for line in step[start:].splitlines():
            if line.strip() == "PY":
                break
            lines.append(line)
        indent = min(len(l) - len(l.lstrip()) for l in lines if l.strip())
        self.script = "\n".join(l[indent:] for l in lines)
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name) / "repo"
        self.runner = Path(self.temp.name) / "runner"
        self.runner.mkdir()
        subprocess.run(["git", "init", "-q", str(self.repo)], check=True)
        for key, value in (("user.email", "t@example.com"), ("user.name", "t")):
            self.git("config", key, value)
        self.commit("README", "base\n")
        self.base = self.git("rev-parse", "HEAD")
        self.git("checkout", "-q", "-b", "lane")

    def git(self, *args):
        return subprocess.run(["git", "-C", str(self.repo)] + list(args), check=True, capture_output=True, text=True).stdout.strip()

    def commit(self, name, text):
        path = self.repo / name
        if text is None:
            path.unlink()
            self.git("rm", "-q", name)
        else:
            path.write_bytes(text) if isinstance(text, bytes) else path.write_text(text)
            self.git("add", name)
        self.git("commit", "-q", "-m", "change " + name)

    def package(self):
        env = dict(os.environ, RUNNER_TEMP=str(self.runner), LANE_BASE=self.base, LANE_AGENT=LANE_AGENT,
                   LANE_RUN="run-1", LANE_TASK="one", LANE_BRANCH="claude/togavsu-one-12345678",
                   CLAUDE_OUTCOME="success", SECRET_OAUTH=self.TOKEN, SECRET_GITHUB="")
        return subprocess.run([sys.executable, "-I", "-c", self.script], cwd=self.repo, env=env, capture_output=True, text=True)

    def test_token_added_then_removed_is_withheld(self):
        self.commit("debug.txt", "env dump " + self.TOKEN + "\n")
        self.commit("debug.txt", None)
        self.assertEqual(self.git("diff", self.base + "..HEAD"), "")
        result = self.package()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("credential", result.stderr)
        self.assertFalse((self.runner / "lane-pack" / "lane.bundle").exists())

    def test_clean_history_is_bundled(self):
        self.commit("work.txt", "done\n")
        result = self.package()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.runner / "lane-pack" / "lane.bundle").exists())
        data = json.loads((self.runner / "lane-pack" / "lane-result.json").read_text())
        self.assertEqual(data["head"], self.git("rev-parse", "lane"))

    PNG = b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\xff\xfe\x80\x81"

    def test_binary_file_is_bundled(self):
        self.commit("screenshot.png", self.PNG + bytes(range(256)))
        result = self.package()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.runner / "lane-pack" / "lane.bundle").exists())

    def test_token_inside_binary_file_is_withheld(self):
        self.commit("screenshot.png", self.PNG + b"tEXt" + self.TOKEN.encode() + b"\xff\x00")
        result = self.package()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("credential", result.stderr)
        self.assertFalse((self.runner / "lane-pack" / "lane.bundle").exists())

    # Shaped like a GitHub token but not a repository secret, as a lane's fake test fixture would be.
    FAKE = "gh" + "p_" + "A" * 36

    def result(self):
        return json.loads((self.runner / "lane-pack" / "lane-result.json").read_text())

    def lane_log(self):
        return self.git("log", "-p", "-m", "--text", "--format=%H%n%B", self.base + "..lane")

    def test_fake_token_line_is_removed_and_the_rest_is_kept(self):
        self.commit("redact_test.go", "package x\nconst a = 1\nvar fake = \"" + self.FAKE + "\"\nconst b = 2\n")
        self.commit("work.txt", "done\n")
        result = self.package()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.runner / "lane-pack" / "lane.bundle").exists())
        data = self.result()
        self.assertEqual(data["head"], self.git("rev-parse", "lane"))
        self.assertEqual(data["credential_lines_removed"], {"redact_test.go": 1})
        self.assertIn("Credential guard: removed 1 line(s)", data["pr_body"])
        self.assertEqual(self.git("show", "lane:redact_test.go"), "package x\nconst a = 1\nconst b = 2")
        self.assertEqual(self.git("show", "lane:work.txt"), "done")
        self.assertNotIn(self.FAKE, self.lane_log())
        self.assertEqual(len(self.git("rev-list", self.base + "..lane").split()), 2)

    def test_fake_token_added_then_removed_is_scrubbed_from_history(self):
        self.commit("debug.txt", "keep me\nleft " + self.FAKE + "\n")
        self.commit("debug.txt", "keep me\n")
        result = self.package()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn(self.FAKE, self.lane_log())
        self.assertEqual(self.git("show", "lane:debug.txt"), "keep me")

    def test_fake_token_in_pr_text_and_summary_drops_only_those_lines(self):
        (self.runner / "lane-out").mkdir()
        (self.runner / "lane-out" / "pr-body.md").write_text("Fixed the ledger.\nSample " + self.FAKE + "\nTests pass.\n")
        (self.runner / "lane-claude.json").write_text(json.dumps({"result": "ok\nused " + self.FAKE + "\nend"}))
        self.commit("work.txt", "done\n")
        result = self.package()
        self.assertEqual(result.returncode, 0, result.stderr)
        data = self.result()
        self.assertTrue(data["pr_body"].startswith("Fixed the ledger.\nTests pass.\n"))
        self.assertEqual(data["summary"], "ok\nend")
        self.assertEqual(data["credential_lines_removed"], {"PR body": 1, "run summary": 1})

    def test_fake_token_in_binary_file_drops_only_that_file(self):
        self.commit("screenshot.png", self.PNG + b"tEXt" + self.FAKE.encode() + b"\xff\x00")
        self.commit("work.txt", "done\n")
        result = self.package()
        self.assertEqual(result.returncode, 0, result.stderr)
        files = self.git("ls-tree", "-r", "--name-only", "lane").split()
        self.assertNotIn("screenshot.png", files)
        self.assertIn("work.txt", files)
        self.assertEqual(self.result()["credential_lines_removed"], {"screenshot.png": 1})

    def test_merged_main_commit_keeps_its_parent_and_content(self):
        self.git("checkout", "-q", "-b", "main-side", self.base)
        self.commit("main.txt", "from main\n")
        side = self.git("rev-parse", "HEAD")
        self.git("checkout", "-q", "lane")
        self.commit("fixture.txt", "a\nt " + self.FAKE + "\n")
        self.git("merge", "-q", "--no-edit", "main-side")
        result = self.package()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.git("rev-list", "--parents", "-n", "1", "lane").split()[2], side)
        self.assertEqual(self.git("show", "lane:main.txt"), "from main")
        self.assertEqual(self.git("show", "lane:fixture.txt"), "a")
        self.assertNotIn(self.FAKE, self.lane_log())

    def test_token_shaped_text_merged_in_from_main_withholds_the_result(self):
        self.git("checkout", "-q", "-b", "main-side", self.base)
        self.commit("main_fixture.txt", "m " + self.FAKE + "\n")
        self.git("checkout", "-q", "lane")
        self.commit("work.txt", "done\n")
        self.git("merge", "-q", "--no-edit", "main-side")
        result = self.package()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("credential", result.stderr)
        self.assertFalse((self.runner / "lane-pack" / "lane.bundle").exists())

    def test_removal_notice_survives_a_full_pr_body(self):
        (self.runner / "lane-out").mkdir()
        (self.runner / "lane-out" / "pr-body.md").write_text("x" * 70000)
        self.commit("fixture.txt", "a\nt " + self.FAKE + "\n")
        result = self.package()
        self.assertEqual(result.returncode, 0, result.stderr)
        body = self.result()["pr_body"]
        self.assertLessEqual(len(body), 60000)
        self.assertIn("Credential guard: removed 1 line(s)", body)


if __name__ == "__main__":
    unittest.main()

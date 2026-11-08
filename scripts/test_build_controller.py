import copy
import datetime as dt
import io
import json
import os
import stat
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import urllib.error
import zipfile

from build_controller import Controller, Remote, ReviewPending, ReviewSuperseded, APP_ID, LEGACY_REPAIR_HALT, atomic, extract_review, latest_checks, review_problem, validate_review

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
        self.fail_post = False
        self.fail_merge = False
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
                raise RuntimeError("GitHub protection rejected merge")
            self.pr.update(merged=True,merge_commit_sha=NEW,state="closed")
            self.main=NEW
            return {"merged": True, "sha": NEW}
        if path == "issues/1/comments":
            return {"id": 99}
        return {}

    def agent(self, record):
        return {"status": self.run_status, "run_id": self.run_id, "git": {}}

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

    def test_ambiguous_delivery_reconciles_new_run_without_replay(self):
        self.failed_review()
        self.remote.fail_post = True
        with self.assertRaises(TimeoutError):
            self.process()
        self.remote.run_id, self.remote.run_status = "run-arrived", "RUNNING"
        self.process()
        self.assertEqual(self.record["repair_count"], 3)
        self.assertNotIn("intent", self.record)
        self.assertEqual(len(self.posts()), 1)

    def test_pass_merges_only_named_head(self):
        self.assertTrue(self.process())
        merge = [c for c in self.remote.calls if c[1] == "pulls/7/merge"]
        self.assertEqual(merge[0][2]["sha"], HEAD)
        self.assertEqual(self.record["completed"]["merge_sha"], NEW)

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
        self.assertEqual(len(self.record["repair_problems"]), 1)
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

    def test_interrupted_and_missing_pr_loops_are_bounded_despite_new_run_ids(self):
        for status in ("ERROR", "FINISHED"):
            with self.subTest(status=status):
                self.setUp()
                self.record.pop("pr")
                self.remote.run_status = status
                self.remote.cursor = self.unique_run_post
                for i in range(4):
                    with self.advance(i * 30):
                        self.process()
                        self.remote.run_id = self.record["last_dispatched_run"]
                self.assertEqual(len(self.posts()), 3)
                self.assertIn("stalled", self.record)

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
        self.assertNotIn("intent", self.record)
        self.assertEqual(self.record["halted"], "Ownership mismatch")
        self.assertFalse(self.remote.calls)
        self.assertNotIn("completed", self.record)

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
        self.assertEqual(self.record["stalled"]["kind"], "run_observation")
        self.assertEqual(len(self.posts()), 1)

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
            raise urllib.error.HTTPError("test", 404, "missing", {}, None)
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

    def test_stalled_ci_does_not_silently_wait_forever(self):
        self.remote.check_rows["Codex checkpoint"]["status"] = "in_progress"
        self.record["check_wait"] = {"head": HEAD, "since": (dt.datetime.now(dt.timezone.utc) - dt.timedelta(minutes=91)).isoformat()}
        self.process()
        self.assertIn("90 minutes", self.record["halted"])

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


class ReviewDataTests(unittest.TestCase):
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


if __name__ == "__main__":
    unittest.main()

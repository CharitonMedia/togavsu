import copy
import datetime as dt
import io
import json
from pathlib import Path
import tempfile
import unittest
import zipfile

from build_controller import Controller, Remote, APP_ID, extract_review, latest_checks, validate_review

BASE, HEAD, NEW = "a" * 40, "b" * 40, "c" * 40
SPEC = {"id": "one", "depends_on": [], "prompt": "bounded task"}


def check(name, conclusion="success", ident=1):
    return {"name": name, "status": "completed", "conclusion": conclusion, "id": ident,
            "head_sha": HEAD, "app": {"id": APP_ID},
            "details_url": "https://github.com/CharitonMedia/togavsu/actions/runs/123/job/456"}


class FakeRemote:
    def __init__(self):
        self.main = BASE
        self.pr = {"number": 7, "state": "open", "merged": False, "draft": False,
                   "mergeable": True, "head": {"sha": HEAD, "repo": {"full_name": "CharitonMedia/togavsu"}},
                   "base": {"ref": "main"}, "html_url": "https://github.com/CharitonMedia/togavsu/pull/7"}
        self.report = {"reviewed_head": HEAD, "reviewed_base": BASE, "verdict": "pass", "findings": [], "summary": "Reviewed"}
        self.context = {"head": HEAD, "base": BASE, "pr": 7}
        self.behind = 0
        self.calls = []
        self.run_id = "run-old"
        self.run_status = "FINISHED"
        self.fail_post = False
        self.fail_merge = False
        self.check_rows = {name: check(name) for name in ["Setup checks", "Codex checkpoint"]}

    def gh(self, path, method="GET", payload=None, binary=False):
        self.calls.append((method, path, payload))
        if path == "git/ref/heads/main":
            return {"object": {"sha": self.main}}
        if path == "pulls/7":
            return copy.deepcopy(self.pr)
        if path == "branches/main/protection":
            return {"required_status_checks": {"strict": True, "contexts": ["Setup checks", "Codex checkpoint"]},
                    "enforce_admins": {"enabled": True}, "required_pull_request_reviews": {}}
        if path.startswith("compare/"):
            return {"behind_by": self.behind}
        if path == "pulls/7/merge":
            if self.fail_merge:
                raise RuntimeError("GitHub protection rejected merge")
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
        return {"run": {"id": "run-new"}, "agent": {"id": "bc-one"}}

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
        self.record = {"agent_id": "bc-one", "submitted": True, "pr": 7, "repair_count": 2}
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

    def test_existing_repair_budget_is_respected(self):
        self.failed_review()
        self.record["repair_count"] = 3
        self.process()
        self.assertIn("exhausted", self.record["halted"])
        self.assertFalse(self.posts())

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

    def test_dry_run_does_not_send_or_persist(self):
        self.failed_review()
        self.controller.dry = True
        self.process()
        self.assertFalse(self.posts())
        self.assertFalse((Path(self.temp.name) / "controller.json").exists())

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


class ReviewDataTests(unittest.TestCase):
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

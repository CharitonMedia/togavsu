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

from build_controller import Controller, Remote, ReviewPending, ReviewSuperseded, APP_ID, atomic, extract_review, latest_checks, validate_review

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

    def test_finished_correction_with_unchanged_failed_head_stops_explicitly(self):
        self.failed_review()
        self.process()
        self.remote.run_id = "run-new"
        self.process()
        self.assertEqual(len(self.posts()), 1)
        self.assertIn("no-progress", self.record["halted"])

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

"""Product evidence rules. Mocks and stand-ins do not accept a criterion."""

import copy
import unittest

from harness.catalog import build_matrix
from harness.corpus import build_files, standin_hashes
from harness.evidence import decide, docker_desktop_ok, summarize


def _evidence(outcome, kind="command", file_hash=None, mocked=False):
    """The old generic suite stub. It must not accept a case."""
    item = {
        "kind": kind,
        "command": "go test ./...",
        "exit_code": 0,
        "observation": "observed",
        "outcome": outcome,
        "mocked": mocked,
    }
    if file_hash:
        item["file_hash"] = file_hash
    return item


def _bound(case, outcome=None):
    """Shape a later domain runner can submit. This helper does not run the product."""
    if outcome is None:
        outcome = "passed" if case["polarity"] == "positive" else "rejected"
    command = f"go test ./internal/domain -run {case['id']}"
    observation = f"{case['id']} result: " + " | ".join(case["expect"])
    return {
        "case_id": case["id"],
        "kind": "command",
        "command": command,
        "exit_code": case["expected_exit_code"],
        "outcome": outcome,
        "observation": observation,
        "assertions": [
            {"expect": text, "ok": True, "detail": f"{case['id']}: {text}"}
            for text in case["expect"]
        ],
        "provenance": {"command": command, "runner": f"domain:{case['id']}"},
    }


def _install(**overrides):
    command = overrides.get("command", "docker compose up")
    item = {
        "kind": "clean_install",
        "host": "docker_desktop",
        "outcome": "passed",
        "command": command,
        "exit_code": 0,
        "observation": "first-run form on a clean Docker Desktop install",
        "provenance": {"command": command, "runner": "release-clean-install"},
    }
    item.update(overrides)
    if "command" in overrides and "provenance" not in overrides:
        item["provenance"] = {"command": overrides["command"], "runner": "release-clean-install"}
    return item


class EvidenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.matrix = build_matrix()
        cls.manifest, cls.files = build_files()
        cls.standins = standin_hashes(cls.manifest, cls.files)
        cls.case = cls.matrix[0]["positive"][0]
        cls.failure = cls.matrix[0]["failure"][0]

    def test_missing_evidence_is_pending(self):
        decision = decide(self.case, None, self.standins)
        self.assertEqual(decision["status"], "pending")
        self.assertFalse(decision["accepted"])

    def test_mock_is_refused(self):
        for kind in ("mock", "skip", "scaffold", "documentation", "todo", "oracle", "synthetic_fixture"):
            decision = decide(self.case, _evidence("passed", kind=kind), self.standins)
            self.assertEqual(decision["status"], "refused", kind)
            self.assertFalse(decision["accepted"])
        decision = decide(self.case, _evidence("passed", mocked=True), self.standins)
        self.assertEqual(decision["status"], "refused")

    def test_standin_hash_is_refused(self):
        standin = next(item for item in self.manifest["files"] if item["legacy_id"] == "old_doc")
        decision = decide(self.case, _evidence("passed", file_hash=standin["sha256"]), self.standins)
        self.assertEqual(decision["status"], "refused")
        self.assertIn("stand-in", decision["reason"])

    def test_positive_and_failure_outcomes(self):
        self.assertFalse(decide(self.case, _evidence("passed"), self.standins)["accepted"])
        self.assertTrue(decide(self.case, _bound(self.case), self.standins)["accepted"])
        rejected = _bound(self.case, "rejected")
        self.assertEqual(decide(self.case, rejected, self.standins)["status"], "failed")
        self.assertTrue(decide(self.failure, _bound(self.failure), self.standins)["accepted"])
        self.assertEqual(decide(self.failure, _bound(self.failure, "passed"), self.standins)["status"], "failed")

    def test_this_tree_is_not_accepted(self):
        report = summarize(self.matrix, standins=self.standins)
        self.assertEqual(report["accepted_count"], 0)
        self.assertEqual(report["pending_count"], 52)
        self.assertFalse(report["app_complete"])
        self.assertEqual(report["product_acceptance"], "pending")

    def _filled(self):
        """Generic go-test stubs, one per case. These must not accept the product."""
        evidence = {}
        for criterion in self.matrix:
            for case in criterion["positive"] + criterion["failure"]:
                outcome = "passed" if case["polarity"] == "positive" else "rejected"
                item = _evidence(outcome)
                item["case_id"] = case["id"]
                item["exit_code"] = case["expected_exit_code"]
                evidence[case["id"]] = item
        return evidence

    def _bound_matrix(self):
        evidence = {}
        for criterion in self.matrix:
            for case in criterion["positive"] + criterion["failure"]:
                evidence[case["id"]] = _bound(case)
        return evidence

    def test_generic_records_cannot_complete_the_app(self):
        report = summarize(self.matrix, self._filled(), _install(), self.standins)
        self.assertEqual(report["accepted_count"], 0)
        self.assertEqual(report["pending_count"], 52)
        self.assertFalse(report["app_complete"])
        self.assertEqual(report["product_acceptance"], "pending")

    def test_full_evidence_without_docker_desktop_is_not_app_complete(self):
        report = summarize(self.matrix, self._bound_matrix(), attestation=None, standins=self.standins)
        self.assertEqual(report["accepted_count"], 52)
        self.assertFalse(report["app_complete"])

    def test_app_complete_needs_docker_desktop_and_every_case(self):
        evidence = self._bound_matrix()
        report = summarize(self.matrix, evidence, _install(), self.standins)
        self.assertTrue(report["app_complete"])
        evidence[self.case["id"]] = _evidence("passed", kind="mock")
        refused = summarize(self.matrix, evidence, _install(), self.standins)
        self.assertFalse(refused["app_complete"])
        self.assertLess(refused["accepted_count"], 52)

    def test_nonzero_exit_does_not_pass_a_successful_command(self):
        case = self._case("AC-002-P1")
        bad = _bound(case)
        bad["exit_code"] = 1
        decision = decide(case, bad, self.standins)
        self.assertFalse(decision["accepted"])
        self.assertEqual(decision["status"], "failed")
        self.assertIn("exit_code 1", decision["reason"])
        good = _bound(case)
        self.assertTrue(decide(case, good, self.standins)["accepted"])

    def test_exit_code_follows_the_case_not_the_polarity(self):
        failing_build = self._case("AC-030-P1")
        self.assertEqual(failing_build["expected_exit_code"], 1)
        zero = _bound(failing_build)
        zero["exit_code"] = 0
        self.assertFalse(decide(failing_build, zero, self.standins)["accepted"])
        one = _bound(failing_build)
        self.assertTrue(decide(failing_build, one, self.standins)["accepted"])
        validator = self._case("AC-002-F1")
        self.assertEqual(validator["expected_exit_code"], 1)
        quiet = _bound(validator)
        quiet["exit_code"] = 0
        self.assertFalse(decide(validator, quiet, self.standins)["accepted"])
        loud = _bound(validator)
        self.assertTrue(decide(validator, loud, self.standins)["accepted"])

    def test_boolean_exit_codes_are_not_integers(self):
        success = self._case("AC-002-P1")
        self.assertEqual(success["expected_exit_code"], 0)
        fake_zero = _bound(success)
        fake_zero["exit_code"] = False
        decision = decide(success, fake_zero, self.standins)
        self.assertEqual(decision["status"], "refused")
        self.assertFalse(decision["accepted"])
        failure = self._case("AC-030-P1")
        self.assertEqual(failure["expected_exit_code"], 1)
        fake_one = _bound(failure)
        fake_one["exit_code"] = True
        decision = decide(failure, fake_one, self.standins)
        self.assertEqual(decision["status"], "refused")
        self.assertFalse(decision["accepted"])
        floated = _bound(success)
        floated["exit_code"] = 0.0
        self.assertEqual(decide(success, floated, self.standins)["status"], "refused")
        quoted = _bound(success)
        quoted["exit_code"] = "0"
        self.assertFalse(decide(success, quoted, self.standins)["accepted"])
        attestation = _install(exit_code=False)
        self.assertFalse(docker_desktop_ok(attestation))
        report = summarize(self.matrix, self._bound_matrix(), attestation, self.standins)
        self.assertEqual(report["accepted_count"], 52)
        self.assertFalse(report["app_complete"])
        self.assertFalse(docker_desktop_ok(_install(exit_code=True)))

    def test_empty_command_and_observation_are_refused(self):
        for field, value in (("command", ""), ("command", "   "), ("observation", ""), ("observation", "\n")):
            item = _evidence("passed")
            item[field] = value
            decision = decide(self.case, item, self.standins)
            self.assertEqual(decision["status"], "refused", field)
            self.assertFalse(decision["accepted"])

    def test_failed_compose_does_not_complete_the_app(self):
        evidence = self._bound_matrix()
        attestation = _install(exit_code=1, observation="compose failed")
        self.assertFalse(docker_desktop_ok(attestation))
        report = summarize(self.matrix, evidence, attestation, self.standins)
        self.assertEqual(report["accepted_count"], 52)
        self.assertFalse(report["app_complete"])
        blank = _install()
        blank["observation"] = "  "
        self.assertFalse(docker_desktop_ok(blank))

    def test_summarize_loads_the_denylist_when_omitted(self):
        standin = next(item for item in self.manifest["files"] if item["legacy_id"] == "old_doc")
        password = next(item for item in self.manifest["files"] if item["legacy_id"] == "password_pdf")
        evidence = {
            self.case["id"]: _evidence("passed", file_hash=standin["sha256"]),
            self.failure["id"]: _evidence("rejected", file_hash=password["sha256"]),
        }
        report = summarize(self.matrix, evidence)
        by_id = {item["id"]: item for item in report["cases"]}
        self.assertEqual(by_id[self.case["id"]]["status"], "refused")
        self.assertEqual(by_id[self.failure["id"]]["status"], "refused")
        self.assertFalse(report["app_complete"])
        self.assertEqual(report["accepted_count"], 0)

    def test_empty_denylist_fails_closed(self):
        evidence = {self.case["id"]: _evidence("passed")}
        report = summarize(self.matrix, evidence, standins=frozenset())
        decision = next(item for item in report["cases"] if item["id"] == self.case["id"])
        self.assertEqual(decision["status"], "refused")
        self.assertIn("unavailable", decision["reason"])
        self.assertFalse(report["app_complete"])

    def test_declared_doubles_are_refused_for_trust_and_access(self):
        for case_id, field, value in (
            ("AC-005-P1", "doubles", True),
            ("AC-003-P1", "backend_mode", "mock"),
        ):
            case = self._case(case_id)
            self.assertFalse(case.get("rejects_doubles"))
            item = _bound(case)
            item[field] = value
            decision = decide(case, item, self.standins)
            self.assertEqual(decision["status"], "refused", case_id)
            self.assertFalse(decision["accepted"])
            self.assertIn("double", decision["reason"])

    def test_missing_failure_cases_cannot_accept_the_matrix(self):
        stripped = copy.deepcopy(self.matrix)
        for item in stripped:
            item["failure"] = []
        evidence = {}
        for item in stripped:
            for case in item["positive"]:
                outcome = "passed" if case["polarity"] == "positive" else "rejected"
                row = _evidence(outcome)
                row["exit_code"] = case["expected_exit_code"]
                evidence[case["id"]] = row
        report = summarize(stripped, evidence, _install(), self.standins)
        self.assertTrue(report["inventory_errors"])
        self.assertEqual(report["accepted"], [])
        self.assertFalse(report["app_complete"])

    def test_duplicate_case_ids_cannot_complete_the_app(self):
        broken = copy.deepcopy(self.matrix)
        broken[0]["positive"].append(dict(broken[0]["positive"][0]))
        report = summarize(broken, self._bound_matrix(), _install(), self.standins)
        self.assertTrue(any("duplicate" in error for error in report["inventory_errors"]))
        self.assertEqual(report["accepted_count"], 0)
        self.assertFalse(report["app_complete"])

    def test_canonical_exit_code_and_polarity_are_required(self):
        broken = copy.deepcopy(self.matrix)
        for criterion in broken:
            for case in criterion["positive"] + criterion["failure"]:
                if case["id"] == "AC-002-P1":
                    case["expected_exit_code"] = 1
        evidence = self._bound_matrix()
        evidence["AC-002-P1"]["exit_code"] = 1
        evidence["AC-002-P1"]["outcome"] = "passed"
        report = summarize(broken, evidence, _install(), self.standins)
        self.assertTrue(any("AC-002-P1" in error and "expected_exit_code" in error for error in report["inventory_errors"]))
        self.assertNotIn("AC-002", report["accepted"])
        self.assertFalse(report["app_complete"])
        flipped = copy.deepcopy(self.matrix)
        for criterion in flipped:
            for case in criterion["positive"]:
                if case["id"] == "AC-002-P1":
                    case["polarity"] = "failure"
        report = summarize(flipped, self._bound_matrix(), _install(), self.standins)
        self.assertTrue(any("AC-002-P1" in error and "polarity" in error for error in report["inventory_errors"]))
        self.assertFalse(report["app_complete"])

    def test_clean_install_doubles_do_not_complete_the_app(self):
        evidence = self._bound_matrix()
        for field, value in (("doubles", True), ("backend_mode", "mock")):
            attestation = _install()
            attestation[field] = value
            self.assertFalse(docker_desktop_ok(attestation), field)
            report = summarize(self.matrix, evidence, attestation, self.standins)
            self.assertEqual(report["accepted_count"], 52)
            self.assertFalse(report["app_complete"])

    def test_ac040_doubles_are_not_acceptance(self):
        case = self._case("AC-040-P1")
        self.assertTrue(case["rejects_doubles"])
        self.assertNotIn("test double", case["arrange"].lower())
        item = _bound(case)
        item["doubles"] = True
        decision = decide(case, item, self.standins)
        self.assertEqual(decision["status"], "refused")
        self.assertIn("double", decision["reason"])
        item = _bound(case)
        item["backend_mode"] = "test_double"
        self.assertFalse(decide(case, item, self.standins)["accepted"])
        self.assertTrue(decide(case, _bound(case), self.standins)["accepted"])

    def test_generic_and_reused_records_are_refused(self):
        other = self.failure
        borrowed = _bound(self.case)
        self.assertFalse(decide(other, borrowed, self.standins)["accepted"])
        renamed = _bound(self.case)
        renamed["case_id"] = other["id"]
        decision = decide(other, renamed, self.standins)
        self.assertEqual(decision["status"], "refused")
        self.assertFalse(decision["accepted"])
        missing = _bound(self.case)
        missing.pop("assertions")
        self.assertIn("assertions", decide(self.case, missing, self.standins)["reason"])
        mismatched = _bound(self.case)
        mismatched["assertions"][0]["expect"] = "not this case"
        self.assertIn("does not match", decide(self.case, mismatched, self.standins)["reason"])
        flagged = _bound(self.case)
        flagged["assertions"][0]["ok"] = 1
        self.assertIn("boolean", decide(self.case, flagged, self.standins)["reason"])

    def _case(self, case_id):
        for criterion in self.matrix:
            for case in criterion["positive"] + criterion["failure"]:
                if case["id"] == case_id:
                    return case
        raise AssertionError(case_id)


if __name__ == "__main__":
    unittest.main()

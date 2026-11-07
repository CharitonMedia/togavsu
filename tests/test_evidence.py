"""Product evidence rules. Mocks and stand-ins do not accept a criterion."""

import unittest

from harness.catalog import build_matrix
from harness.corpus import build_files, standin_hashes
from harness.evidence import decide, summarize


def _evidence(outcome, kind="command", file_hash=None, mocked=False):
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
        self.assertTrue(decide(self.case, _evidence("passed"), self.standins)["accepted"])
        self.assertEqual(decide(self.case, _evidence("rejected"), self.standins)["status"], "failed")
        self.assertTrue(decide(self.failure, _evidence("rejected"), self.standins)["accepted"])
        self.assertEqual(decide(self.failure, _evidence("passed"), self.standins)["status"], "failed")

    def test_this_tree_is_not_accepted(self):
        report = summarize(self.matrix, standins=self.standins)
        self.assertEqual(report["accepted_count"], 0)
        self.assertEqual(report["pending_count"], 52)
        self.assertFalse(report["app_complete"])
        self.assertEqual(report["product_acceptance"], "pending")

    def test_full_evidence_without_docker_desktop_is_not_app_complete(self):
        evidence = {}
        for criterion in self.matrix:
            for case in criterion["positive"]:
                evidence[case["id"]] = _evidence("passed")
            for case in criterion["failure"]:
                evidence[case["id"]] = _evidence("rejected")
        report = summarize(self.matrix, evidence, attestation=None, standins=self.standins)
        self.assertEqual(report["accepted_count"], 52)
        self.assertFalse(report["app_complete"])

    def test_app_complete_needs_docker_desktop_and_every_case(self):
        evidence = {}
        for criterion in self.matrix:
            for case in criterion["positive"]:
                evidence[case["id"]] = _evidence("passed")
            for case in criterion["failure"]:
                evidence[case["id"]] = _evidence("rejected")
        attestation = {
            "kind": "clean_install",
            "host": "docker_desktop",
            "outcome": "passed",
            "command": "docker compose up",
            "exit_code": 0,
            "observation": "first-run form on a clean Docker Desktop install",
        }
        report = summarize(self.matrix, evidence, attestation, self.standins)
        self.assertTrue(report["app_complete"])
        evidence[self.case["id"]] = _evidence("passed", kind="mock")
        refused = summarize(self.matrix, evidence, attestation, self.standins)
        self.assertFalse(refused["app_complete"])
        self.assertLess(refused["accepted_count"], 52)


if __name__ == "__main__":
    unittest.main()

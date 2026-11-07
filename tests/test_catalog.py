"""The acceptance map matches the PRD index and does not soften it."""

import copy
import unittest

from harness.catalog import build_matrix, integrity_errors, load_acceptance, load_tasks, repo_root


class CatalogTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.root = repo_root()
        cls.acceptance = load_acceptance(cls.root)
        cls.tasks = load_tasks(cls.root)
        cls.matrix = build_matrix(cls.root)

    def test_integrity_is_clean(self):
        self.assertEqual(integrity_errors(self.matrix, self.acceptance, self.tasks), [])

    def test_fifty_two_pending_criteria(self):
        self.assertEqual(len(self.matrix), 52)
        self.assertEqual([item["id"] for item in self.matrix], [item["id"] for item in self.acceptance])
        self.assertTrue(all(item["status"] == "pending" for item in self.matrix))
        self.assertTrue(all(item["product_acceptance"] == "pending" for item in self.matrix))

    def test_requirements_match_the_index(self):
        for criterion, source in zip(self.matrix, self.acceptance):
            self.assertEqual(criterion["requirement"], source["requirement"])

    def test_rewritten_requirement_is_flagged(self):
        broken = copy.deepcopy(self.matrix)
        broken[0]["requirement"] = broken[0]["requirement"] + " optional"
        errors = integrity_errors(broken, self.acceptance, self.tasks)
        self.assertTrue(any("requirement text" in error for error in errors))

    def test_missing_failure_case_is_flagged(self):
        broken = copy.deepcopy(self.matrix)
        broken[4]["failure"] = []
        errors = integrity_errors(broken, self.acceptance, self.tasks)
        self.assertTrue(any("failure case" in error for error in errors))

    def test_passed_status_is_flagged(self):
        broken = copy.deepcopy(self.matrix)
        broken[2]["status"] = "passed"
        errors = integrity_errors(broken, self.acceptance, self.tasks)
        self.assertTrue(any("not pending" in error for error in errors))

    def test_boolean_expected_exit_code_is_flagged(self):
        broken = copy.deepcopy(self.matrix)
        broken[1]["positive"][0]["expected_exit_code"] = False
        errors = integrity_errors(broken, self.acceptance, self.tasks)
        self.assertTrue(any("AC-002-P1" in error and "expected exit" in error for error in errors))

    def test_domains_come_from_tasks(self):
        by_id = {task["id"]: task for task in self.tasks}
        self.assertEqual(by_id["qa"]["acceptance_ids"].__len__(), 52)
        walls = next(item for item in self.matrix if item["id"] == "AC-003")
        self.assertIn("identity", walls["implementing_domains"])
        self.assertIn("practice", walls["implementing_domains"])
        self.assertIn("qa", walls["responsible_domains"])
        self.assertIn("release", walls["responsible_domains"])
        perf = next(item for item in self.matrix if item["id"] == "AC-017")
        integration = [
            "integrate-practice",
            "integrate-finance",
            "integrate-documents",
            "integrate-extensions",
        ]
        self.assertEqual(perf["implementing_domains"], integration)
        for task_id in integration:
            self.assertIn(task_id, perf["responsible_domains"])
        self.assertIn("contracts", perf["pending_dependencies"])
        self.assertIn("platform", perf["pending_dependencies"])
        for item in self.matrix:
            for task_id in integration:
                self.assertIn(task_id, item["implementing_domains"], item["id"])

    def _case(self, ac_id, suffix):
        item = next(row for row in self.matrix if row["id"] == ac_id)
        cases = item["positive"] + item["failure"]
        return next(case for case in cases if case["id"] == f"{ac_id}-{suffix}")

    def test_expense_billing_is_mapped(self):
        draft = self._case("AC-008", "P2")
        void = self._case("AC-008", "P3")
        twice = self._case("AC-008", "F4")
        stuck = self._case("AC-008", "F5")
        again = self._case("AC-008", "F6")
        self.assertIn("exactly one line", " ".join(draft["expect"]))
        self.assertIn("no longer unbilled", " ".join(draft["expect"]))
        self.assertIn("unbilled again", " ".join(void["expect"]))
        self.assertIn("second draft", " ".join(twice["expect"]))
        self.assertIn("still billed", " ".join(stuck["expect"]))
        self.assertIn("new draft", " ".join(again["expect"]))

    def test_trust_sql_immutability_is_mapped(self):
        service = self._case("AC-006", "F4")
        sql = self._case("AC-006", "F5")
        reversal = self._case("AC-006", "P2")
        self.assertIn("no update or delete method is not this proof", " ".join(service["expect"]))
        self.assertIn("history are unchanged", " ".join(sql["expect"]))
        self.assertIn("new row", " ".join(reversal["expect"]))
        self.assertIn("No database trigger", " ".join(sql["expect"]))
        for ac_id, suffix in (("AC-024", "F4"), ("AC-031", "F3"), ("AC-039", "F3")):
            case = self._case(ac_id, suffix)
            text = " ".join(case["expect"])
            self.assertIn("not this proof", text)
            self.assertIn("No database trigger", text)

    def test_hardcoded_tunable_scan_is_mapped(self):
        bad = self._case("AC-030", "P3")
        control = self._case("AC-030", "P4")
        missed = self._case("AC-030", "F3")
        self.assertEqual(bad["expected_exit_code"], 1)
        self.assertEqual(control["expected_exit_code"], 0)
        self.assertEqual(missed["expected_exit_code"], 1)
        self.assertIn("45", " ".join(bad["expect"]))
        self.assertIn("registry-key lookup does not satisfy", " ".join(bad["expect"]))
        self.assertIn("exits 0", " ".join(control["expect"]))
        self.assertIn("absent from the settings registry", " ".join(missed["expect"]))


if __name__ == "__main__":
    unittest.main()

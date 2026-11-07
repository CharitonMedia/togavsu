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

    def test_domains_come_from_tasks(self):
        by_id = {task["id"]: task for task in self.tasks}
        self.assertEqual(by_id["qa"]["acceptance_ids"].__len__(), 52)
        walls = next(item for item in self.matrix if item["id"] == "AC-003")
        self.assertIn("identity", walls["implementing_domains"])
        self.assertIn("practice", walls["implementing_domains"])
        self.assertIn("qa", walls["responsible_domains"])
        self.assertIn("release", walls["responsible_domains"])
        perf = next(item for item in self.matrix if item["id"] == "AC-017")
        self.assertEqual(perf["implementing_domains"], [])
        self.assertIn("contracts", perf["pending_dependencies"])
        self.assertIn("platform", perf["pending_dependencies"])


if __name__ == "__main__":
    unittest.main()

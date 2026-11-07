import unittest
from check_review import validate


class ReviewGateTests(unittest.TestCase):
    def setUp(self):
        self.base, self.head = "a"*40, "b"*40
        self.report = dict(reviewed_base=self.base, reviewed_head=self.head, verdict="pass", summary="Reviewed bounded scope", findings=[])

    def test_accepts_current_clean_review(self):
        validate(self.report, self.base, self.head)

    def test_rejects_stale_head_or_base(self):
        for field in ["reviewed_head", "reviewed_base"]:
            report = dict(self.report, **{field:"c"*40})
            with self.assertRaises(ValueError):
                validate(report,self.base,self.head)

    def test_rejects_fail_unresolved_and_missing_output(self):
        for change in [dict(verdict="fail"),dict(findings=[{"detail":"defect"}]),dict(summary=""),dict(verdict=None)]:
            with self.assertRaises(ValueError):
                validate(dict(self.report,**change),self.base,self.head)

    def test_rejects_missing_report(self):
        with self.assertRaises(ValueError):
            validate({},self.base,self.head)


if __name__ == "__main__":
    unittest.main()

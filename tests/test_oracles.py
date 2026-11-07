"""Executable PRD rules. Passing these does not accept a product criterion."""

import unittest
from datetime import date

from harness.oracles import (
    TrustLedger,
    accountant_allowed,
    apply_reprocess,
    can_save_layout,
    category_publishable,
    debt_marker_errors,
    default_naming_pattern,
    free_days,
    low_confidence,
    markdown_copy_name,
    naming_pattern_errors,
    perf_within_budget,
    phase_report_errors,
    plan_doc_job,
    scan_env_and_tunables,
    wall_visible,
    withdraw_below_zero,
)


class OracleTests(unittest.TestCase):
    def test_layout_cap(self):
        self.assertTrue(can_save_layout(9))
        self.assertFalse(can_save_layout(10))

    def test_perf_budget_rejects_a_short_seed_and_a_300_ms_sample(self):
        self.assertTrue(perf_within_budget(299, 299, 50, 20_000, 1_000_000))
        self.assertFalse(perf_within_budget(300, 299, 50, 20_000, 1_000_000))
        self.assertFalse(perf_within_budget(299, 299, 50, 19_999, 1_000_000))

    def test_confidence_threshold(self):
        self.assertTrue(low_confidence(899))
        self.assertFalse(low_confidence(900))

    def test_naming_pattern(self):
        self.assertEqual(naming_pattern_errors(default_naming_pattern()), [])
        self.assertTrue(any("status" in error for error in naming_pattern_errors("<clientlast>-<status>.<ext>")))
        self.assertTrue(naming_pattern_errors("<draft>.<ext>"))
        self.assertTrue(naming_pattern_errors("<file-status>.<ext>"))
        self.assertTrue(naming_pattern_errors("<received>.<ext>"))
        self.assertEqual(naming_pattern_errors("<clientlast>-<received-date>.<ext>"), [])
        self.assertTrue(naming_pattern_errors("<foobar>.<ext>"))

    def test_high_tier_needs_agreement(self):
        self.assertTrue(category_publishable("court_filing", "court_filing", "court_filing"))
        self.assertFalse(category_publishable("court_filing", "court_filing", "correspondence"))
        self.assertTrue(category_publishable("correspondence", "correspondence", "other"))

    def test_wall_hides_unnamed_admin(self):
        self.assertTrue(wall_visible("staff", None))
        self.assertTrue(wall_visible("attorney-1", ["attorney-1"]))
        self.assertFalse(wall_visible("admin", ["attorney-1"]))
        self.assertFalse(wall_visible("staff", ["attorney-1"]))

    def test_accountant_access(self):
        self.assertTrue(accountant_allowed("GET", "invoices"))
        self.assertTrue(accountant_allowed("GET", "trust_ledger"))
        self.assertTrue(accountant_allowed("GET", "file:receipt"))
        self.assertTrue(accountant_allowed("GET", "file:invoice_pdf"))
        self.assertFalse(accountant_allowed("POST", "invoices"))
        self.assertFalse(accountant_allowed("DELETE", "trust_ledger"))
        self.assertFalse(accountant_allowed("GET", "notes"))
        self.assertFalse(accountant_allowed("GET", "file:correspondence"))

    def test_trust_posts_below_zero_and_is_append_only(self):
        ledger = TrustLedger()
        result = withdraw_below_zero(ledger, 100, 250, ["admin-a", "admin-b"], "attorney-1")
        self.assertTrue(result["posted"])
        self.assertFalse(result["blocked"])
        self.assertTrue(result["flag"])
        self.assertEqual(result["emails"], ["admin-a", "admin-b", "attorney-1"])
        self.assertEqual(result["balance_cents"], -150)
        original = ledger.get(result["entry_id"])
        ledger.reverse(result["entry_id"], "attorney-1")
        self.assertEqual(ledger.get(result["entry_id"]), original)
        with self.assertRaises(PermissionError):
            ledger.update(result["entry_id"], amount_cents=0)
        with self.assertRaises(PermissionError):
            ledger.delete(result["entry_id"])
        quiet = withdraw_below_zero(TrustLedger(), 500, 100, ["admin-a"], "attorney-1")
        self.assertFalse(quiet["flag"])
        self.assertTrue(quiet["posted"])

    def test_locked_fields_and_closed_matter(self):
        updated = apply_reprocess(
            {"name": "kept", "category": "notes", "matter": "2026-0001", "grouping": "g1",
             "locked": ["name", "category", "matter", "grouping"], "markdown_versions": [b"old"]},
            {"name": "changed", "category": "other", "summary": "new"},
            b"new",
        )
        self.assertEqual(updated["name"], "kept")
        self.assertEqual(updated["category"], "notes")
        self.assertEqual(updated["grouping"], "g1")
        self.assertEqual(updated["summary"], "new")
        self.assertEqual(updated["markdown_versions"], [b"old", b"new"])
        closed = plan_doc_job("closed")
        self.assertFalse(closed["model_call"])
        self.assertEqual(closed["label"], "Not processed (matter closed)")
        self.assertTrue(plan_doc_job("open")["model_call"])

    def test_markdown_name(self):
        self.assertEqual(
            markdown_copy_name("example-2026-0001-motion-to-withdraw-2026-10-15.txt"),
            "example-2026-0001-motion-to-withdraw-2026-10-15.md",
        )
        self.assertEqual(markdown_copy_name("notes.md"), "notes.md")

    def test_available_dates_do_not_need_a_calendar_write(self):
        start = date(2026, 10, 1)
        end = date(2026, 10, 3)
        events = [
            {"date": date(2026, 10, 1), "kind": "court date"},
            {"date": date(2026, 10, 2), "kind": "client meeting", "all_day": False},
        ]
        before = [dict(item) for item in events]
        free = free_days(start, end, events)
        self.assertEqual(events, before)
        self.assertEqual(free, [date(2026, 10, 2), date(2026, 10, 3)])
        half = free_days(start, start, [{"date": start, "kind": "court date", "part": "morning"}], half_days=True)
        self.assertEqual(half, [(start, "afternoon")])
        morning_block = free_days(start, start, [{"date": start, "kind": "block", "part": "morning"}], half_days=True)
        afternoon_block = free_days(start, start, [{"date": start, "kind": "block", "part": "afternoon"}], half_days=True)
        full_block = free_days(start, start, [{"date": start, "kind": "block", "all_day": True}], half_days=True)
        unnamed_block = free_days(start, start, [{"date": start, "kind": "block"}], half_days=True)
        both_halves = free_days(start, start, [
            {"date": start, "kind": "block", "part": "morning"},
            {"date": start, "kind": "block", "part": "afternoon"},
        ], half_days=True)
        half_off = free_days(start, start, [{"date": start, "kind": "block", "part": "morning"}], half_days=False)
        self.assertEqual(morning_block, [(start, "afternoon")])
        self.assertEqual(afternoon_block, [(start, "morning")])
        self.assertEqual(full_block, [])
        self.assertEqual(unnamed_block, [])
        self.assertEqual(both_halves, [])
        self.assertEqual(half_off, [])

    def test_env_scanner_and_debt_markers(self):
        files = {
            "internal/billing/fee.go": 'package billing\nfunc init() { _ = os.Getenv("SECRET") }\nsettings.Get("unknown")\n',
            "internal/bootstrap/config.go": 'package bootstrap\n_ = os.Getenv("DATABASE_URL")\n',
            "internal/settings/read.go": 'settings.Get("time_increment")\n',
        }
        violations = scan_env_and_tunables(files, {"time_increment"})
        self.assertTrue(any("billing" in item and "environment" in item for item in violations))
        self.assertTrue(any("unknown" in item for item in violations))
        self.assertFalse(any(item.startswith("internal/bootstrap/") for item in violations))
        self.assertFalse(any("time_increment" in item for item in violations))
        self.assertEqual(debt_marker_errors("no markers here\n"), [])
        marker = "pony" + "tail:"
        self.assertEqual(debt_marker_errors(f"// {marker} ceiling, path, trigger: wordcraft opens the file\n"), [])
        self.assertTrue(debt_marker_errors(f"// {marker} ceiling only\n"))
        self.assertEqual(phase_report_errors("Simplification net line count: 10. Debt ledger count: 0.\n"), [])
        self.assertTrue(phase_report_errors("shipped the phase\n"))


if __name__ == "__main__":
    unittest.main()

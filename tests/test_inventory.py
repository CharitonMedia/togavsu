"""Named defaults and section 5 records stay tied to the PRD text."""

import unittest

from harness.catalog import repo_root
from harness.catalog import build_matrix
from harness.defaults import (
    CONDITIONAL_AI_KEYS,
    DEFAULTS,
    missing_anchors,
    omitted_prd_defaults,
    prd_category_rows,
    setting_defaults,
)
from harness.records import LATER_RECORDS, SECTION5_RECORDS, record_in_section5, section5_text


class InventoryTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.prd = (repo_root() / "togavsu-core-prd.md").read_text()

    def test_every_default_anchor_is_in_the_prd(self):
        self.assertEqual(missing_anchors(self.prd), [])

    def test_setting_defaults_have_tabs_and_fixed_rules_do_not_pretend_to_be_switches(self):
        self.assertGreaterEqual(len(setting_defaults()), 50)
        for item in DEFAULTS:
            if item["setting"]:
                self.assertTrue(item["tab"], item["key"])
            else:
                self.assertIn(item["key"], {
                    "trust_ledger_append_only",
                    "audit_kept_forever",
                    "below_zero_always_flags",
                    "service_layer_permissions",
                    "s3_and_dropbox_encryption_always_on",
                    "labeling_categorizing_no_local_endpoint",
                })

    def test_prd_categories_and_conditional_ai_defaults_are_required(self):
        rows = prd_category_rows(self.prd)
        self.assertEqual(len(rows), 17)
        self.assertEqual(omitted_prd_defaults(self.prd), [])
        for key in CONDITIONAL_AI_KEYS:
            self.assertIn(key, {item["key"] for item in setting_defaults()})
        dropped = [item for item in DEFAULTS if item["key"] != "doc_category_court_filing"]
        self.assertIn("doc_category_court_filing", omitted_prd_defaults(self.prd, dropped))
        dropped_ai = [item for item in DEFAULTS if item["key"] != "categorizing_model_with_openai_key"]
        self.assertIn("categorizing_model_with_openai_key", omitted_prd_defaults(self.prd, dropped_ai))
        dropped_check = [item for item in DEFAULTS if item["key"] != "labeling_check_model_without_secondary"]
        self.assertIn("labeling_check_model_without_secondary", omitted_prd_defaults(self.prd, dropped_check))
        for key in (
            "ocr_check_model_with_secondary",
            "ocr_check_model_without_secondary",
            "photo_description_grouping_check_model_with_secondary",
            "photo_description_grouping_check_model_without_secondary",
            "transcription_check_model_with_secondary",
            "transcription_check_model_without_secondary",
        ):
            self.assertIn(key, {item["key"] for item in setting_defaults()})
            dropped_task = [item for item in DEFAULTS if item["key"] != key]
            self.assertIn(key, omitted_prd_defaults(self.prd, dropped_task))

    def test_ac029_change_and_reset_cover_every_setting(self):
        criterion = next(item for item in build_matrix() if item["id"] == "AC-029")
        keys = {item["key"] for item in setting_defaults()}
        changes = {case["id"] for case in criterion["positive"] if "-P2-" in case["id"]}
        resets = {case["id"] for case in criterion["positive"] if "-P3-" in case["id"]}
        change_failures = {case["id"] for case in criterion["failure"] if "-F2-" in case["id"]}
        reset_failures = {case["id"] for case in criterion["failure"] if "-F3-" in case["id"]}
        self.assertEqual(changes, {f"AC-029-P2-{key}" for key in keys})
        self.assertEqual(resets, {f"AC-029-P3-{key}" for key in keys})
        self.assertEqual(change_failures, {f"AC-029-F2-{key}" for key in keys})
        self.assertEqual(reset_failures, {f"AC-029-F3-{key}" for key in keys})
        combined = " ".join(case["arrange"] for case in criterion["positive"])
        self.assertNotIn("Pick one", combined)

    def test_section5_records_are_named_in_the_prd(self):
        section = section5_text(self.prd)
        missing = [name for name in SECTION5_RECORDS if not record_in_section5(section, name)]
        self.assertEqual(missing, [])

    def test_later_records_do_not_satisfy_section5(self):
        section = section5_text(self.prd)
        for name in LATER_RECORDS:
            self.assertNotIn(name.replace("_", r"\_"), section)


if __name__ == "__main__":
    unittest.main()

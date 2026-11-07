"""Named defaults and section 5 records stay tied to the PRD text."""

import unittest

from harness.catalog import repo_root
from harness.defaults import DEFAULTS, missing_anchors, setting_defaults
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
                })

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

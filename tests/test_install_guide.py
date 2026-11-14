"""docs/release/INSTALL.md must not describe pending PRD 6 behavior as shipped.

docs/platform/pending.md lists automatic migration rollback, module data volumes in
backups, and the dashboard restore screens as not built; the supervisor's admin notices
stay in its mail outbox (PONYTAIL-DEBT.md). The guide is the firm's operations manual,
so each of those must read as pending there too.
"""

import os
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def _read(rel):
    with open(os.path.join(ROOT, rel), encoding="utf-8") as f:
        return f.read()


class InstallGuideTest(unittest.TestCase):
    def setUp(self):
        self.guide = _read("docs/release/INSTALL.md")
        self.pending = _read("docs/platform/pending.md")

    def test_no_shipped_claims_for_pending_behavior(self):
        for claim in (
            "restores the backup if a migration ran",
            "and module data volumes)",
            "the dashboard warns before it starts",
            "Upload a backup file, or pick one from the list or a destination",
            "Failures email the admins",
            "and emails the admins",
        ):
            self.assertNotIn(claim, self.guide, claim)

    def test_pending_items_are_marked_pending_in_both(self):
        # Each item still pending in pending.md is named as pending in the guide.
        for in_pending, in_guide in (
            ("Automatic migration rollback", "Automatic rollback of a failed migration is pending"),
            ("Restoring module data volumes", "Module data volumes are not restored yet (pending)"),
            ("The dashboard upload, destination picker, and the warning before a restore starts are not built",
             "the dashboard warning, upload and destination picker for restore are pending"),
        ):
            self.assertIn(in_pending, self.pending, in_pending)
            self.assertIn(in_guide, self.guide, in_guide)

    def test_api_paths_the_guide_names_exist(self):
        # The guide points admins at API routes instead; each one is served by the core.
        routes = _read("internal/core/backups.go") + _read("internal/core/updates.go")
        for route in ("POST /api/v1/backup-uploads", "POST /api/v1/restores", "GET /api/v1/restores/{restore_id}",
                      "GET /api/v1/backups", "POST /api/v1/updates", "GET /api/v1/updates"):
            self.assertIn('"' + route, routes, route)
            self.assertIn(route.split(" ", 1)[1].split("{")[0].rstrip("/"), self.guide, route)


if __name__ == "__main__":
    unittest.main()

"""Contract checks for requested-user busy descriptions in both generated catalogs."""

import json
import unittest

from contract.build import MCP_PATH, OPENAPI_PATH


class CalendarBusyContractTest(unittest.TestCase):
    def test_calendar_busy_description_excludes_unassigned_walled_events(self):
        spec = json.loads(OPENAPI_PATH.read_text(encoding="utf-8"))
        catalog = json.loads(MCP_PATH.read_text(encoding="utf-8"))
        operation = spec["paths"]["/api/v1/calendar-events"]["get"]
        tool = next(tool for tool in catalog["tools"] if tool["name"] == "listCalendarEvent")
        for catalog, description in (
            ("OpenAPI", operation["description"]),
            ("OpenAPI MCP", operation["x-togavsu-mcp"]["description"]),
            ("MCP", tool["description"]),
        ):
            with self.subTest(catalog=catalog):
                self.assertIn("only events where the requested active user is an attendee", description)
                self.assertIn("never includes unassigned walled events", description)
                self.assertIn("those appear only on the service occupancy read", description)
                self.assertNotIn("Walled events with no active attendee user also appear even when not assigned to the requested user.", description)


if __name__ == "__main__":
    unittest.main()

"""Occupancy exception and transport descriptions, with the approved 2i all_day addition."""
import json
from pathlib import Path
import unittest

from jsonschema import Draft202012Validator, ValidationError


class Core2HContract(unittest.TestCase):
    def test_occupancy_exception_and_mcp_description(self):
        root = Path(__file__).resolve().parents[1]
        spec = json.loads((root / 'openapi/v1/openapi.json').read_text())
        tools = json.loads((root / 'openapi/v1/mcp-tools.json').read_text())['tools']
        operation = spec['paths']['/api/v1/calendar-events']['get']
        tool = next(t for t in tools if t['name'] == 'listCalendarEvent')
        self.assertEqual(tool['description'], operation['description'])
        for text in (tool['description'], spec['components']['schemas']['CalendarOccupancyBlock']['description']):
            self.assertIn('no assigned attendee user', text)
            self.assertIn('inactive user', text)
        self.assertEqual(tool['scopes'], ['calendar:read'])
        validator = Draft202012Validator(spec['components']['schemas']['CalendarOccupancyBlock'])
        interval = dict(starts_at='2026-11-21T10:00:00Z', ends_at='2026-11-21T11:00:00Z', all_day=False)
        validator.validate(interval)
        for field in ('id', 'title', 'matter_id', 'contact_id', 'attendee_user_ids', 'kind', 'walled', 'video', 'count'):
            with self.subTest(field=field), self.assertRaises(ValidationError):
                validator.validate(dict(interval, **{field: 'private'}))

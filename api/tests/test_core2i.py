"""Approved 2i calendar wire shapes and REST/MCP descriptions."""
import json
from pathlib import Path
import unittest

from jsonschema import Draft202012Validator, ValidationError


class Core2IContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        root = Path(__file__).resolve().parents[1]
        cls.spec = json.loads((root / 'openapi/v1/openapi.json').read_text())
        cls.schemas = cls.spec['components']['schemas']
        cls.tools = json.loads((root / 'openapi/v1/mcp-tools.json').read_text())['tools']

    def test_video_create_and_patch_semantics(self):
        for schema in ('CalendarEvent', 'CalendarEventWrite', 'CalendarEventWritePatch'):
            validator = Draft202012Validator(self.schemas[schema]['properties']['video'],
                                            resolver=__import__('jsonschema').RefResolver.from_schema(self.spec))
            for value in (None, {'requested': False}, {'requested': True, 'provider': None,
                          'host_user_id': None, 'provider_meeting_id': 'é' * 200}):
                validator.validate(value)
            validator.validate({'requested': True, 'provider': 'a' * 42})
            if schema.endswith('Patch'):
                for value in ({}, {'requested': None}, {'provider_meeting_id': 'meeting'}):
                    validator.validate(value)
            else:
                for value in ({}, {'requested': None}):
                    with self.assertRaises(ValidationError):
                        validator.validate(value)
            for value in ({'requested': 'true'},
                          {'requested': True, 'provider_meeting_id': 'é' * 201},
                          {'requested': True, 'secret': 'extra'},
                          *({'requested': True, 'provider': p} for p in ('', 'a', 'Phone-Zoom', '-phone', 'phone-', 'a' * 43))):
                with self.subTest(schema=schema, value=value), self.assertRaises(ValidationError):
                    validator.validate(value)
        self.assertIn('invalid_input', self.schemas['Problem']['properties']['code']['enum'])

    def test_occupancy_has_exactly_three_required_fields(self):
        schema = self.schemas['CalendarOccupancyBlock']
        self.assertEqual(set(schema['properties']), {'starts_at', 'ends_at', 'all_day'})
        self.assertEqual(set(schema['required']), set(schema['properties']))
        self.assertFalse(schema['additionalProperties'])

    def test_mcp_video_descriptions_match_rest(self):
        for name in ('createCalendarEvent', 'getCalendarEvent', 'updateCalendarEvent', 'listCalendarEvent'):
            tool = next(t for t in self.tools if t['name'] == name)
            op = self.spec['paths'][tool['path']][tool['method'].lower()]
            self.assertEqual(tool['description'], op['description'])
            self.assertIn('video', tool['description'])

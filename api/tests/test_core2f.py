"""Public batch 2f schemas; production behaviors run in Go/Postgres tests."""
import json
from pathlib import Path
import unittest

from jsonschema import Draft202012Validator, ValidationError


class Core2FContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.spec = json.loads((Path(__file__).resolve().parents[1] / 'openapi/v1/openapi.json').read_text())
        cls.schemas = cls.spec['components']['schemas']

    def test_busy_blocks_cannot_carry_details(self):
        schema = self.schemas['CalendarBusyBlock']
        value = {'starts_at': '2026-11-20T12:00:00Z', 'ends_at': '2026-11-20T13:00:00Z', 'all_day': False}
        Draft202012Validator(schema).validate(value)
        for field in ('id', 'title', 'matter_id', 'matter_name', 'attendee_user_ids', 'created_by'):
            with self.subTest(field=field), self.assertRaises(ValidationError):
                Draft202012Validator(schema).validate(dict(value, **{field: 'secret'}))
        op = self.spec['paths']['/api/v1/calendar-events']['get']
        self.assertIn('busy', next(p for p in op['parameters'] if p['name'] == 'view')['schema']['enum'])
        self.assertEqual(op['x-togavsu-access']['module_delegation'], 'delegated_viewer_or_service_principal')

    def test_named_conflicts_and_scoped_counties(self):
        op = self.spec['paths']['/api/v1/matters/{matter_id}/conflict-checks']['post']
        self.assertFalse(op['requestBody']['required'])
        body = op['requestBody']['content']['application/json']['schema']
        Draft202012Validator(body).validate({'names': ['Synthetic Person', 'Another Person']})
        with self.assertRaises(ValidationError):
            Draft202012Validator(body).validate({'names': ['']})
        for path, method, scope in (('/api/v1/counties/{county_id}', 'get', 'settings:read'), ('/api/v1/conflict-records', 'post', 'conflicts:write')):
            op = self.spec['paths'][path][method]
            self.assertEqual(op['x-togavsu-access']['module_delegation'], 'delegated_viewer_or_service_principal')
            self.assertIn(scope, op['x-togavsu-access']['scopes'])

    def test_manifest_reasons_and_publication_fields(self):
        permissions = self.schemas['Module']['properties']['permissions']['items']
        self.assertIn('reason', permissions['required'])
        fields = self.schemas['FileBulkItem']['properties']
        self.assertIn('draft', fields['status']['enum'])
        self.assertIn('on_behalf_of_user_id', fields)
        self.assertNotIn('on_behalf_of_user_id', self.schemas['FileBulkItem']['required'])

    def test_processor_contract_is_bounded(self):
        body = {'intent_id': 'synthetic-intent', 'amount_cents': 2500, 'destination': 'operating', 'payment_token': 'synthetic-placeholder'}
        schema = self.schemas['ProcessorPaymentWrite']
        Draft202012Validator(schema).validate(body)
        for patch in ({'amount_cents': 0}, {'destination': 'trust'}, {'card_number': 'placeholder'}, {'matter_id': 'guessed'}):
            with self.subTest(patch=patch), self.assertRaises(ValidationError):
                Draft202012Validator(schema).validate(dict(body, **patch))
        description = self.spec['paths']['/api/v1/modules/contracts/{contract}/{operation}']['post']['description']
        self.assertIn('payments:write', description)
        self.assertIn('ProcessorPaymentRecover', description)

    def test_generated_event_keeps_one_envelope(self):
        body = self.spec['paths']['/api/v1/modules/contracts/{contract}/events']['post']['requestBody']['content']['application/json']['schema']
        self.assertEqual(len(body['oneOf']), 2)
        body = self.schemas['TargetedContractEvent']
        self.assertEqual(set(body['required']), {'id', 'contract', 'consumer_id', 'occurred_at', 'action'})
        for field in ('type', 'event', 'event_type', 'data', 'provider_id'):
            self.assertNotIn(field, body['properties'])
        self.assertFalse(body['additionalProperties'])

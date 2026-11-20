"""Public schemas for the 2g behavior; real routes are tested on Postgres in Go."""
import json
from pathlib import Path
import unittest

from jsonschema import Draft202012Validator, FormatChecker, ValidationError


class Core2GContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.spec = json.loads((Path(__file__).resolve().parents[1] / 'openapi/v1/openapi.json').read_text())
        cls.schemas = cls.spec['components']['schemas']

    def test_binding_and_targeted_envelopes_are_disjoint(self):
        body = self.spec['paths']['/api/v1/modules/contracts/{contract}/events']['post']['requestBody']['content']['application/json']['schema']
        validator = Draft202012Validator(dict(body, components=self.spec['components']), format_checker=FormatChecker())
        binding = dict(contract='documents.generate.v1', event='document.generated', event_id='d210ad4a-3c92-4ed1-9684-8fb164155760', occurred_at='2026-11-20T12:00:00Z', data={})
        targeted = dict(id='legacy-event', contract='phone.provider.v1', consumer_id='phone', action='phone.provider.v1.completed', occurred_at=binding['occurred_at'], payload={})
        validator.validate(binding)
        validator.validate(targeted)
        for patch in ({'event_id': 'bad'}, {'occurred_at': 'bad'}, {'data': None}, {'data': []}, {'consumer_id': 'mixed'}, {'event': ''}, {'type': 'spoof'}):
            with self.subTest(patch=patch), self.assertRaises(ValidationError):
                validator.validate(dict(binding, **patch))
        self.assertEqual(self.spec['paths']['/api/v1/modules/contracts/{contract}/events']['post']['x-togavsu-access']['scopes'], ['contracts:provide'])

    def test_recording_request_accepts_no_money_fields(self):
        validator = Draft202012Validator(self.schemas['ProcessorPaymentRecord'], format_checker=FormatChecker())
        body = dict(intent_id='synthetic-intent', contact_id='019b6968-4600-7001-8000-000000000002')
        validator.validate(body)
        validator.validate(dict(body, matter_id='019b6968-4600-7001-8000-000000000003'))
        for field in ('amount_cents', 'destination', 'provider_id', 'reference_number', 'payment_token', 'status'):
            with self.subTest(field=field), self.assertRaises(ValidationError):
                validator.validate(dict(body, **{field: 'changed'}))

    def test_firm_occupancy_is_start_end_only(self):
        validator = Draft202012Validator(self.schemas['CalendarOccupancyBlock'])
        body = dict(starts_at='2026-11-20T12:00:00Z', ends_at='2026-11-20T13:00:00Z')
        validator.validate(body)
        for field in ('id', 'title', 'matter_id', 'attendee_user_ids', 'all_day', 'wall'):
            with self.subTest(field=field), self.assertRaises(ValidationError):
                validator.validate(dict(body, **{field: 'secret'}))
        op = self.spec['paths']['/api/v1/calendar-events']['get']
        self.assertIn('occupancy', next(p for p in op['parameters'] if p['name'] == 'view')['schema']['enum'])

    def test_frame_header_and_form_field_are_published(self):
        csrf = self.spec['x-togavsu-frame-session-csrf']
        self.assertEqual(csrf['request_header'], 'X-Togavsu-Session-CSRF')
        self.assertEqual(csrf['form_field'], 'csrf')
        self.assertEqual(csrf['method'], 'GET')
        self.assertIn('no-store', csrf['description'])

    def test_background_audit_actor_is_a_module_id(self):
        actor = self.schemas['AuditEntry']['properties']['actor_id']
        Draft202012Validator(actor).validate('synthetic-intake')
        Draft202012Validator(actor).validate('019b6968-4600-7001-8000-000000000001')

    def test_emitted_action_scanner(self):
        from contract.build import emitted_actions
        source = """
        const reversed = "trust.entry_reversed"
        const ( completed = "payment.completed" )
        e.Action = "contact.phone_changed"
        action := "matter.party_updated"
        changedFields("county", id, matter, "county.updated", "Updated", fields)
        t.event(reversed, "trust", id)
        t.event(completed, "payment", id)
        s.emit(ctx, "contact.created")
        Event{Action: "note.created"}
        Event{Name: "trust.entry_posted"}
        """
        self.assertEqual(emitted_actions(source), {"trust.entry_reversed", "payment.completed", "contact.phone_changed", "matter.party_updated", "county.updated", "contact.created", "note.created", "trust.entry_posted"})
        op = self.spec['paths']['/api/v1/calendar-events']['get']
        self.assertEqual(op['x-togavsu-access']['scopes'], ['calendar:read'])
        self.assertNotIn('view_scopes', op['x-togavsu-access'])

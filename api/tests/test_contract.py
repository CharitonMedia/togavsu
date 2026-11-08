"""Contract checks for the OpenAPI proposal.

These tests validate the documents and the parity rules. They do not stand in for
AC-002 product acceptance: no HTTP handler is exercised.
"""

from __future__ import annotations

import unittest

from openapi_spec_validator import validate

from contract.build import (
    COVERAGE_PATH,
    MARKETPLACE_PATH,
    MCP_PATH,
    OPENAPI_PATH,
    build_marketplace,
    build_spec,
    dump,
    render_coverage,
    render_mcp,
)
from contract.policy import (
    CORE_PAGE_TARGETS,
    EXTENSIBLE_RECORDS,
    background_file_step,
    commit_concurrent_processor_creates,
    commit_concurrent_publishes,
    authorized_badge_counts,
    committed_events,
    payment_lifecycle_events,
    custom_fields_batch,
    custom_field_bulk_apply,
    trust_flag_rows,
    custom_fields_outcome,
    event_read_scopes,
    accountant_file_rows,
    file_download_decision,
    malware_scan_decision,
    operation_authorization,
    require_supported_qualifiers,
    SUPPORTED_QUALIFIERS,
    is_core_file_download,
    confirm_totp_reset,
    bulk_invoice_apply,
    invoice_draft_decision,
    bulk_privilege_expansions,
    external_effect_decision,
    include_views_decision,
    operation_names_matter,
    project_invoice_for_role,
    project_record,
    module_help_registration,
    module_marker_registration,
    attach_matter_marker,
    trust_ledger_events,
    rendered_markdown,
    webhook_signature,
    webhook_signature_matches,
    sidebar_panel,
    start_totp_reset,
    issue_api_token,
    notification_page,
    notification_recipient_decision,
    decide_module_permissions,
    replace_matter_wall,
    manifest_contributions,
    module_id_slug,
    normalize_module_target,
    manifest_custom_fields,
    module_ui_targets,
    open_widget_target,
    page_size_decision,
    sidebar_entries,
    _mcp_operation_call,
    _module_upload_document,
    background_grant_issuance,
    doc_tender_events,
    inherited_history_rows,
    marketplace_package,
    matter_page,
    mcp_credential_rejection,
    mcp_tool_dispatch,
    payment_effect_decision,
    payment_reversal_decision,
    processor_apply,
    processor_callback,
    processor_issuance,
    published_version_events,
    trust_debit_decision,
    webhook_envelope,
)

PRD_PATHS = [
    "/api/v1/openapi.json",
    "/mcp",
    "/llms.txt",
    "/api/v1/llms.txt",
    "/healthz",
    "/api/v1/health",
    "/api/v1/backups",
    "/api/v1/backup-destinations",
    "/api/v1/restores",
    "/api/v1/me/layouts",
    "/api/v1/settings",
    "/api/v1/settings/appearance",
    "/api/v1/nav/context",
    "/api/v1/notifications",
    "/api/v1/timers",
    "/api/v1/search",
    "/api/v1/help/sections",
    "/api/v1/help/items",
    "/api/v1/help/modules/{module_id}/pages",
    "/api/v1/calendar/available-dates",
    "/api/v1/files",
    "/api/v1/files/{file_id}/versions",
    "/api/v1/files/{file_id}/renditions",
    "/api/v1/storage-backends",
    "/api/v1/storage-moves",
    "/api/v1/files/{file_id}/editor-runs",
    "/api/v1/editor/commands",
    "/api/v1/files/{file_id}/conversions",
    "/api/v1/doc-tender/jobs",
    "/api/v1/doc-tender/exceptions",
    "/api/v1/doc-tender/categories",
    "/api/v1/doc-tender/naming",
    "/help/guide",
    "/help/guide.pdf",
    "/ical/{secret}.ics",
    "/api/v1/contacts/{contact_id}/history",
    "/api/v1/matters/{matter_id}/history",
    "/api/v1/invoices/{invoice_id}/history",
    "/api/v1/fee-agreements/{fee_agreement_id}/history",
    "/api/v1/time-entries/{time_entry_id}/history",
    "/api/v1/expenses/{expense_id}/history",
]

REQUIRED_FEATURES = {
    "openapi",
    "mcp",
    "llms",
    "health",
    "setup",
    "auth",
    "api-tokens",
    "users",
    "profile",
    "me-settings",
    "notification-preferences",
    "sidebar",
    "contacts",
    "contact-channels",
    "matters",
    "matter-parties",
    "matter-walls",
    "matter-close",
    "matter-reopen",
    "matter-markers",
    "matter-glance",
    "missing-case-number",
    "merge",
    "practice-areas",
    "counties",
    "referral-sources",
    "party-roles",
    "fee-agreements",
    "fee-items",
    "notes",
    "conflicts",
    "time-entries",
    "timers",
    "expenses",
    "invoices",
    "payments",
    "billing-worklist",
    "collections",
    "reports",
    "trust-accounts",
    "trust-ledger",
    "trust-below-zero",
    "trust-flags",
    "trust-replenishment",
    "trust-reconciliation",
    "trust-reports",
    "calendar",
    "deadlines",
    "ical",
    "available-dates",
    "search",
    "nav",
    "notifications",
    "widgets",
    "layouts",
    "role-layouts",
    "help",
    "help-guide",
    "module-help",
    "settings",
    "appearance",
    "files",
    "file-versions",
    "file-renditions",
    "file-uploads",
    "storage-backends",
    "storage-moves",
    "editor",
    "editor-runs",
    "conversions",
    "court-profiles",
    "doc-tender",
    "custom-fields",
    "audit",
    "edit-history",
    "events",
    "activity",
    "backups",
    "backup-destinations",
    "restores",
    "updates",
    "modules",
    "marketplace",
    "domain-replacement",
    "ai",
    "mail",
    "problem-reports",
    "seats",
    "webhooks",
    "bulk",
    "include-extensions",
    "folders",
    "edit-locks",
    "quarantine",
}

PRD_SETTING_DEFAULTS = {
    "firm.matter_number_format": "YYYY-NNNN",
    "firm.staff_portal_label": "Staff portal",
    "security.totp_required.admin": True,
    "security.totp_required.attorney": True,
    "security.totp_required.staff": True,
    "security.totp_required.accountant": True,
    "security.totp_required.client": False,
    "security.idle_timeout_seconds": 12 * 60 * 60,
    "security.failed_sign_in_limit": 5,
    "security.lockout_seconds": 15 * 60,
    "security.api_rate_limit_per_minute": 600,
    "practice.conflict_score_floor": 60,
    "billing.time_increment_minutes": 6,
    "billing.default_expense_markup_percent": 0,
    "billing.payment_terms_days": 30,
    "billing.grace_period_days": 15,
    "billing.late_interest_enabled": False,
    "trust.reconciliation_reminder_day": 5,
    "calendar.deadline_reminder_offsets_days": [7, 1],
    "calendar.event_reminder_offsets_days": [1],
    "calendar.available_dates_range_months": 3,
    "calendar.available_dates_purpose": "one day trial",
    "calendar.available_dates_title": "AVAILABLE DATES FOR HEARING",
    "files.max_upload_bytes": 2 * 1024 * 1024 * 1024,
    "files.upload_chunk_bytes": 8 * 1024 * 1024,
    "files.drop_settle_seconds": 30,
    "editor.lock_timeout_seconds": 300,
    "editor.max_concurrent_runs": 2,
    "editor.run_timeout_seconds": 60,
    "editor.run_memory_bytes": 1024 * 1024 * 1024,
    "editor.pdf_previews": True,
    "doc_tender.enabled": True,
    "doc_tender.naming_pattern": "<clientlast>-<matter#>-<what-it-is>-<doc-date>.<ext>",
    "doc_tender.confidence_floor": 0.90,
    "doc_tender.photo_group_quiet_seconds": 600,
    "home.saved_layouts_per_user": 10,
    "search.min_characters": 2,
    "search.delay_ms": 180,
    "search.hits_per_scope_all": 5,
    "search.hits_one_scope": 8,
    "activity.window_days": 30,
    "activity.page_size": 100,
    "notes.page_size": 25,
    "appearance.mode": "device",
    "ai.timeout_seconds": 30,
    "ai.doc_tender.labeling_model": "gpt-6.1-sol",
    "ai.doc_tender.categorizing_model": "gpt-6.1-sol",
    "updates.window_start_local": "02:00",
    "updates.window_end_local": "04:00",
    "backups.nightly_local_time": "01:00",
    "backups.local_retention_days": 30,
    "backups.test_restore_day": 1,
    "logs.retention_days": 400,
    "webhooks.retry_attempts": 8,
    "webhooks.retry_window_hours": 24,
}

HIGH_TIER = {
    "court_filing",
    "court_notice",
    "discovery",
    "agency_notice",
    "police_criminal",
    "estate_planning",
    "contracts_agreements",
}

BILLING_PREFIXES = (
    "/api/v1/invoices",
    "/api/v1/invoice-drafts",
    "/api/v1/payments",
    "/api/v1/trust",
    "/api/v1/time-entries",
    "/api/v1/timers",
    "/api/v1/expenses",
    "/api/v1/billing",
    "/api/v1/reports",
)


def _ops(spec: dict):
    for path, item in spec["paths"].items():
        for method, operation in item.items():
            if method.startswith("x-"):
                continue
            yield method, path, operation


class ContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.spec = build_spec()
        cls.market = build_marketplace()

    def test_core_spec_validates(self):
        validate(self.spec)

    def test_marketplace_spec_validates(self):
        validate(self.market)

    def test_validator_rejects_a_broken_spec(self):
        broken = {
            "openapi": "3.1.0",
            "info": {"title": "broken", "version": "0"},
            "paths": {
                "/x": {
                    "get": {
                        "responses": {
                            "200": {
                                "description": "missing target",
                                "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Missing"}}},
                            }
                        }
                    }
                }
            },
        }
        with self.assertRaises(Exception):
            validate(broken)

    def test_version_and_conventions(self):
        self.assertEqual(self.spec["openapi"], "3.1.0")
        self.assertTrue(self.spec["info"]["version"].startswith("1."))
        conventions = self.spec["x-togavsu-conventions"]
        self.assertEqual(conventions["version_prefix"], "/api/v1")
        self.assertEqual(conventions["mcp_path"], "/mcp")
        self.assertEqual(conventions["pagination"], "cursor")
        self.assertEqual(conventions["error_media_type"], "application/problem+json")
        self.assertEqual(conventions["idempotency_header"], "Idempotency-Key")
        self.assertEqual(conventions["money"], "integer_cents")
        self.assertEqual(conventions["ids"], "uuidv7")

    def test_prd_paths_exist(self):
        for path in PRD_PATHS:
            self.assertIn(path, self.spec["paths"], path)

    def test_required_features_have_an_mcp_tool(self):
        required = set()
        transport = set()
        for _method, _path, operation in _ops(self.spec):
            parity = operation["x-togavsu-mcp"]["parity"]
            if parity == "required":
                required.update(operation["x-togavsu-features"])
            elif parity == "transport":
                transport.update(operation["x-togavsu-features"])
        self.assertEqual(transport, {"mcp"})
        self.assertEqual(sorted(REQUIRED_FEATURES - required - transport), [])

    def test_mcp_parity_rules(self):
        tools = set()
        canonicals = set()
        for _method, path, operation in _ops(self.spec):
            mcp = operation["x-togavsu-mcp"]
            self.assertIn(mcp["parity"], {"required", "alias", "transport"})
            if mcp["parity"] == "required":
                self.assertEqual(mcp["tool"], operation["operationId"])
                self.assertNotIn(operation["operationId"], tools)
                tools.add(operation["operationId"])
            elif mcp["parity"] == "alias":
                canonicals.add(mcp["canonical"])
            else:
                self.assertEqual(path, "/mcp")
                self.assertEqual(operation["operationId"], "postMcp")
        self.assertTrue(canonicals <= tools)
        self.assertGreater(len(tools), 300)

    def test_public_and_authenticated_security(self):
        public = {
            "confirmPasswordReset",
            "confirmTotpEnrollment",
            "createSetup",
            "downloadIcal",
            "getHealth",
            "getHealthz",
            "getLlmsTxt",
            "getLlmsTxtV1",
            "getOpenAPI",
            "getSetup",
            "login",
            "requestPasswordReset",
            "startTotpEnrollment",
            "verifyRecoveryCode",
            "verifyTotp",
        }
        for _method, _path, operation in _ops(self.spec):
            if operation["operationId"] in public:
                self.assertEqual(operation["security"], [])
            else:
                schemes = operation["security"]
                self.assertIn({"sessionCookie": []}, schemes, operation["operationId"])
                self.assertIn({"bearerAuth": []}, schemes, operation["operationId"])
        transport = _operation(self.spec, "postMcp")
        self.assertIn({}, transport["security"])
        self.assertEqual(transport["x-togavsu-access"]["tool_authorization"], "underlying_http_operation")

    def test_mcp_public_bootstrap_tools_need_no_credential(self):
        anonymous = {"arguments": {}}
        for tool in ("createSetup", "startTotpEnrollment", "confirmTotpEnrollment", "login"):
            decision = mcp_tool_dispatch(self.spec, {**anonymous, "tool": tool})
            self.assertEqual(decision["result"], "allowed", tool)
            self.assertTrue(decision["effects"], tool)
            self.assertTrue(decision["public"], tool)
        protected = mcp_tool_dispatch(self.spec, {**anonymous, "tool": "listMatter"})
        self.assertEqual(protected["result"], "unauthenticated")
        self.assertFalse(protected["effects"])
        opened = mcp_tool_dispatch(
            self.spec,
            {
                "tool": "listMatter",
                "arguments": {},
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "staff",
                "token_scopes": ["matters:read"],
            },
        )
        self.assertEqual(opened["result"], "allowed")
        self.assertTrue(opened["effects"])
        self.assertFalse(opened["public"])
        staff_setting = mcp_tool_dispatch(
            self.spec,
            {
                "tool": "updateSetting",
                "arguments": {"path": {"setting_key": "api.page_size"}},
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "staff",
                "token_scopes": ["settings:write"],
            },
        )
        self.assertEqual(staff_setting["result"], "forbidden")
        self.assertFalse(staff_setting["effects"])
        admin_setting = mcp_tool_dispatch(
            self.spec,
            {
                "tool": "updateSetting",
                "arguments": {"path": {"setting_key": "api.page_size"}},
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "admin",
                "token_scopes": ["settings:write"],
            },
        )
        self.assertEqual(admin_setting["result"], "allowed")
        self.assertTrue(admin_setting["effects"])
        missing_scope = mcp_tool_dispatch(
            self.spec,
            {
                "tool": "updateSetting",
                "arguments": {},
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "admin",
                "token_scopes": ["settings:read"],
            },
        )
        self.assertEqual(missing_scope["result"], "scope_denied")
        self.assertFalse(missing_scope["effects"])
        matter_id = "018f1a00-7b3c-7d4e-8a11-333333333333"
        off_wall = mcp_tool_dispatch(
            self.spec,
            {
                "tool": "getMatter",
                "arguments": {"path": {"matter_id": matter_id}, "on_wall": True},
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "admin",
                "token_scopes": ["matters:read"],
                "matter_walled": True,
                "on_wall": False,
                "matter_membership": {matter_id: False},
            },
        )
        self.assertEqual(off_wall["result"], "forbidden")
        self.assertFalse(off_wall["effects"])
        off_wall_context = mcp_tool_dispatch(
            self.spec,
            {
                "tool": "getMatter",
                "arguments": {"path": {"matter_id": matter_id}},
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "admin",
                "token_scopes": ["matters:read"],
                "matter_walled": True,
                "on_wall": False,
                "matter_membership": {matter_id: False},
            },
        )
        self.assertEqual(off_wall_context["result"], "not_found")
        self.assertFalse(off_wall_context["effects"])
        module_denied = mcp_tool_dispatch(
            self.spec,
            {
                "tool": "getMatter",
                "arguments": {"path": {"matter_id": matter_id}},
                "authorization": "Bearer module",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "attorney",
                "module_id": "intake",
                "approved_scopes": ["contacts:read"],
                "token_scopes": ["matters:read"],
                "grant_revoked": True,
            },
        )
        self.assertEqual(module_denied["result"], "forbidden")
        self.assertFalse(module_denied["effects"])
        wrong = mcp_tool_dispatch(self.spec, {"tool": "createSetup", "arguments": {}, "authorization": "Bearer nope", "authorization_scheme": "Bearer", "credential_valid": False})
        self.assertEqual(wrong["result"], "unauthenticated")
        self.assertFalse(wrong["effects"])
        both = mcp_tool_dispatch(
            self.spec,
            {"tool": "login", "arguments": {}, "authorization": "Bearer secret", "cookie": "session", "authorization_scheme": "Bearer", "credential_valid": True},
        )
        self.assertEqual(both["result"], "ambiguous_auth")
        self.assertFalse(both["effects"])
        spoofed = mcp_tool_dispatch(self.spec, {"tool": "createSetup", "arguments": {"Authorization": "Bearer stolen"}})
        self.assertEqual(spoofed["result"], "forbidden")
        self.assertFalse(spoofed["effects"])
        planted = mcp_tool_dispatch(self.spec, {"tool": "listMatter", "arguments": {"roles": ["admin"]}})
        self.assertEqual(planted["result"], "forbidden")
        self.assertFalse(planted["effects"])
        listed = mcp_tool_dispatch(self.spec, {"method": "tools/list", "arguments": {}})
        self.assertEqual(listed["result"], "allowed")
        self.assertFalse(listed["effects"])
        grant = _operation(self.spec, "issueFilePublicationGrant")
        self.assertEqual(grant["x-togavsu-access"]["authorization_category"], "service_principal_issuance")
        self.assertEqual(grant["x-togavsu-access"]["module_delegation"], "service_principal")
        self.assertEqual(mcp_tool_dispatch(self.spec, {"tool": "issueFilePublicationGrant", "arguments": {}})["result"], "unauthenticated")
        payment = _operation(self.spec, "createPayment")
        self.assertEqual(payment["x-togavsu-access"]["module_delegation"], "delegated_viewer_or_noninteractive")
        self.assertEqual(mcp_tool_dispatch(self.spec, {"tool": "createPayment", "arguments": {}})["result"], "unauthenticated")

    def test_mcp_dispatch_reuses_the_operation_policy(self):
        import copy

        def bearer(**extra):
            return {"authorization": "Bearer secret", "authorization_scheme": "Bearer", "credential_valid": True, **extra}

        require_supported_qualifiers(self.spec)
        covered = set()
        for _method, _path, operation in _ops(self.spec):
            covered.update(operation["x-togavsu-access"])
        self.assertTrue(covered)
        self.assertTrue(covered <= SUPPORTED_QUALIFIERS)
        mutated = copy.deepcopy(self.spec)
        setting = _operation(mutated, "updateSetting")
        setting["x-togavsu-access"]["unlisted_gate"] = True
        with self.assertRaises(RuntimeError):
            require_supported_qualifiers(mutated)
        unknown = mcp_tool_dispatch(
            mutated,
            {**bearer(role="admin", token_scopes=["settings:write"]), "tool": "updateSetting", "arguments": {"path": {"setting_key": "api.page_size"}}},
        )
        self.assertEqual(unknown["result"], "forbidden")
        self.assertFalse(unknown["effects"])
        deposit = mcp_tool_dispatch(
            self.spec,
            {**bearer(role="staff", token_scopes=["trust:write"]), "tool": "postTrustEntry", "arguments": {"body": {"entry_type": "deposit"}}},
        )
        self.assertEqual(deposit["result"], "allowed")
        self.assertTrue(deposit["effects"])
        for entry_type in ("withdrawal", "transfer_to_operating", "refund", "adjustment"):
            denied_entry = mcp_tool_dispatch(
                self.spec,
                {**bearer(role="staff", token_scopes=["trust:write"]), "tool": "postTrustEntry", "arguments": {"body": {"entry_type": entry_type}}},
            )
            self.assertEqual(denied_entry["result"], "scope_denied", entry_type)
            self.assertFalse(denied_entry["effects"], entry_type)
        staff_withdraw = mcp_tool_dispatch(
            self.spec,
            {**bearer(role="staff", token_scopes=["trust:withdraw"]), "tool": "postTrustEntry", "arguments": {"body": {"entry_type": "withdrawal"}}},
        )
        self.assertEqual(staff_withdraw["result"], "forbidden")
        self.assertFalse(staff_withdraw["effects"])
        attorney_withdraw = mcp_tool_dispatch(
            self.spec,
            {**bearer(role="attorney", token_scopes=["trust:withdraw"]), "tool": "postTrustEntry", "arguments": {"body": {"entry_type": "withdrawal"}}},
        )
        self.assertEqual(attorney_withdraw["result"], "allowed")
        self.assertTrue(attorney_withdraw["effects"])
        account = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00001"
        accounts = [{"id": account, "active": True, "authorized": True, "firm_id": "firm-1"}]
        trust_payment = {"body": {"destination": "trust", "status": "completed", "trust_account_id": account}}
        missing_trust = mcp_tool_dispatch(
            self.spec,
            {**bearer(role="staff", token_scopes=["payments:write"], trust_accounts=accounts, firm_id="firm-1"), "tool": "createPayment", "arguments": trust_payment},
        )
        self.assertEqual(missing_trust["result"], "scope_denied")
        self.assertFalse(missing_trust["effects"])
        held_trust = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["payments:write", "trust:write"], trust_accounts=accounts, firm_id="firm-1"),
                "tool": "createPayment",
                "arguments": trust_payment,
            },
        )
        self.assertEqual(held_trust["result"], "allowed")
        self.assertTrue(held_trust["effects"])
        pending = mcp_tool_dispatch(
            self.spec,
            {**bearer(role="staff", token_scopes=["payments:write"]), "tool": "createPayment", "arguments": {"body": {"destination": "operating", "status": "pending"}}},
        )
        self.assertEqual(pending["result"], "allowed")
        self.assertTrue(pending["effects"])
        unavailable = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["payments:write"], domain_handlers={"time": {"replaced": True, "available": False, "module_id": "time-mod"}}),
                "tool": "createPayment",
                "arguments": {"body": {"destination": "operating", "status": "pending"}},
            },
        )
        self.assertEqual(unavailable["result"], "forbidden")
        self.assertFalse(unavailable["effects"])
        routed = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["payments:write"], domain_handlers={"time": {"replaced": True, "available": True, "module_id": "time-mod"}}),
                "tool": "createPayment",
                "arguments": {"body": {"destination": "operating", "status": "pending"}},
            },
        )
        self.assertEqual(routed["result"], "allowed")
        self.assertFalse(routed["effects"])
        self.assertEqual(routed["routed"], "time-mod")
        malware = self.spec["x-togavsu-module-delegation"]["noninteractive"]["malware_scan"]
        matter = "matter-1"
        scan_module = {
            "registered": True,
            "subscriptions": ["file.version_created"],
            "module_id": "malware",
            "recipient_id": "malware",
            "file_id": "file-1",
            "version_id": "ver-1",
            "matter_id": matter,
        }
        scan_grant = _issue_malware_grant(malware, scan_module)

        def scan(task=scan_grant, **extra):
            path = {"file_id": extra.pop("file_id", "file-1"), "version_id": extra.pop("version_id", "ver-1")}
            scopes = extra.pop("module_scopes", ["files:read"])
            token = extra.pop("token_scopes", list(scopes))
            approved = extra.pop("approved_scopes", list(scopes))
            call = {
                **bearer(
                    viewer_present=False,
                    module_id=extra.pop("module_id", "malware"),
                    caller_module_id=extra.pop("caller_module_id", "malware"),
                ),
                "tool": "downloadFileVersion",
                "module_scopes": scopes,
                "registered": True,
                "subscriptions": ["file.version_created"],
                "matter_id": extra.pop("matter_id", matter),
                "arguments": {"task_authorization": task, "path": path},
                **extra,
            }
            if token is not None:
                call["token_scopes"] = token
            if approved is not None:
                call["approved_scopes"] = approved
            return mcp_tool_dispatch(self.spec, _as_trusted_grant_fixture(call))

        allowed_scan = scan()
        self.assertEqual(allowed_scan["result"], "allowed")
        self.assertTrue(allowed_scan["effects"])
        self.assertIsNone(allowed_scan.get("role"))
        for label, decision in (
            ("revoked", scan(task={**scan_grant, "revoked": True})),
            ("expired", scan(task={**scan_grant, "expired": True})),
            ("invalid", scan(task={**scan_grant, "invalid": True})),
            ("other-module", scan(caller_module_id="other-module")),
            ("missing", scan(task=None)),
        ):
            self.assertNotEqual(decision["result"], "allowed", label)
            self.assertFalse(decision["effects"], label)
        self.assertEqual(scan(version_id="ver-2")["result"], "not_found")
        self.assertFalse(scan(version_id="ver-2")["effects"])
        self.assertEqual(scan(module_scopes=["files:write"])["result"], "scope_denied")
        self.assertFalse(scan(module_scopes=["files:write"])["effects"])
        self.assertEqual(scan(token_scopes=[], approved_scopes=["files:read"])["result"], "scope_denied")
        self.assertFalse(scan(token_scopes=[], approved_scopes=["files:read"])["effects"])
        self.assertEqual(scan(token_scopes=None, approved_scopes=None)["result"], "scope_denied")
        self.assertFalse(scan(token_scopes=None, approved_scopes=None)["effects"])
        self.assertEqual(scan(current_approved_scopes=[])["result"], "scope_denied")
        self.assertFalse(scan(current_approved_scopes=[])["effects"])
        self.assertEqual(scan(current_approved_scopes=["files:write"])["result"], "scope_denied")
        self.assertFalse(scan(current_approved_scopes=["files:write"])["effects"])
        off_wall = scan(matter_walled=True, matter_membership={matter: False}, on_wall=False)
        self.assertEqual(off_wall["result"], "not_found")
        self.assertFalse(off_wall["effects"])
        payment_policy = self.spec["x-togavsu-module-delegation"]["noninteractive"]["payment_processor"]
        pay_module = {"registered": True, "module_id": "lawpay", "recipient_id": "lawpay", "approved_scopes": ["payments:write", "trust:write"]}
        operating_grant = processor_issuance(
            payment_policy,
            pay_module,
            {
                "processor_module_id": "lawpay",
                "initialization_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1",
                "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbb1",
                "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
                "contact_id": "018f1a00-7b3c-7d4e-8a11-222222222222",
                "invoice_id": None,
                "amount_cents": 2500,
                "destination": "operating",
                "caller_scopes": ["payments:write"],
                "expires_at": "2026-11-07T18:00:00Z",
            },
        )["grant"]
        trust_grant = processor_issuance(
            payment_policy,
            pay_module,
            {
                "processor_module_id": "lawpay",
                "initialization_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa2",
                "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbb2",
                "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
                "contact_id": "018f1a00-7b3c-7d4e-8a11-222222222222",
                "invoice_id": None,
                "amount_cents": 2500,
                "destination": "trust",
                "trust_account_id": account,
                "trust_accounts": accounts,
                "firm_id": "firm-1",
                "caller_scopes": ["payments:write", "trust:write"],
                "expires_at": "2026-11-07T18:00:00Z",
            },
        )["grant"]

        def callback(grant, **extra):
            scopes = extra.pop("module_scopes", ["payments:write"])
            token = extra.pop("token_scopes", list(scopes))
            approved = extra.pop("approved_scopes", list(scopes))
            call = {
                **bearer(viewer_present=False, module_id=extra.pop("module_id", "lawpay"), caller_module_id=extra.pop("caller_module_id", "lawpay")),
                "tool": "createPayment",
                "module_scopes": scopes,
                "registered": True,
                "matter_id": grant["matter_id"] if grant else None,
                "contact_id": grant.get("contact_id") if grant else None,
                "amount_cents": grant.get("amount_cents") if grant else None,
                "destination": extra.pop("destination", grant.get("destination") if grant else None),
                "status": extra.pop("status", "pending"),
                "trust_accounts": accounts,
                "firm_id": "firm-1",
                "arguments": {"task_authorization": grant},
                **extra,
            }
            if token is not None:
                call["token_scopes"] = token
            if approved is not None:
                call["approved_scopes"] = approved
            return mcp_tool_dispatch(self.spec, _as_trusted_grant_fixture(call))

        operating_callback = callback(operating_grant)
        self.assertEqual(operating_callback["result"], "allowed")
        self.assertTrue(operating_callback["effects"])
        for label, decision in (
            ("revoked", callback({**operating_grant, "revoked": True})),
            ("expired", callback({**operating_grant, "expired": True})),
            ("other-module", callback(operating_grant, caller_module_id="other")),
            ("missing", callback(None)),
        ):
            self.assertNotEqual(decision["result"], "allowed", label)
            self.assertFalse(decision["effects"], label)
        trust_denied = callback(trust_grant, destination="trust", status="completed", module_scopes=["payments:write"])
        self.assertEqual(trust_denied["result"], "scope_denied")
        self.assertFalse(trust_denied["effects"])
        trust_allowed = callback(trust_grant, destination="trust", status="completed", module_scopes=["payments:write", "trust:write"], trust_account_id=account)
        self.assertEqual(trust_allowed["result"], "allowed")
        self.assertTrue(trust_allowed["effects"])
        self.assertEqual(callback(operating_grant, token_scopes=[], approved_scopes=["payments:write"])["result"], "scope_denied")
        self.assertFalse(callback(operating_grant, token_scopes=[], approved_scopes=["payments:write"])["effects"])
        self.assertEqual(callback(operating_grant, token_scopes=None, approved_scopes=None)["result"], "scope_denied")
        self.assertFalse(callback(operating_grant, token_scopes=None, approved_scopes=None)["effects"])
        self.assertEqual(callback(operating_grant, current_approved_scopes=[])["result"], "scope_denied")
        self.assertFalse(callback(operating_grant, current_approved_scopes=[])["effects"])
        self.assertEqual(callback(operating_grant, current_approved_scopes=["trust:write"])["result"], "scope_denied")
        self.assertFalse(callback(operating_grant, current_approved_scopes=["trust:write"])["effects"])
        marker_id = "018f1a00-7b3c-7d4e-8a11-111111111111"
        marker_body = {"marker_id": marker_id, "label": "After hours", "color": "#336699", "target": "intake/header"}
        own_marker = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(viewer_present=False, module_id="intake", approved_scopes=["markers:write"], token_scopes=["markers:write"]),
                "tool": "createModuleMarker",
                "arguments": {"path": {"module_id": "intake"}, "body": marker_body},
            },
        )
        self.assertEqual(own_marker["result"], "allowed")
        self.assertTrue(own_marker["effects"])
        victim_marker = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(viewer_present=False, module_id="intake", approved_scopes=["markers:write"], token_scopes=["markers:write"]),
                "tool": "createModuleMarker",
                "arguments": {"path": {"module_id": "victim"}, "body": marker_body},
            },
        )
        self.assertEqual(victim_marker["result"], "forbidden")
        self.assertFalse(victim_marker["effects"])
        human_marker = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="admin", human_session=True, module_id="intake", approved_scopes=["markers:write"], token_scopes=["markers:write"]),
                "tool": "createModuleMarker",
                "arguments": {"path": {"module_id": "intake"}, "body": marker_body},
            },
        )
        self.assertEqual(human_marker["result"], "forbidden")
        self.assertFalse(human_marker["effects"])
        own_help = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(viewer_present=False, module_id="matter-clock", approved_scopes=["help:write"], token_scopes=["help:write"]),
                "tool": "createModuleHelpPage",
                "arguments": {"path": {"module_id": "matter-clock"}, "body": {"heading": "matter-clock"}},
            },
        )
        self.assertEqual(own_help["result"], "allowed")
        self.assertTrue(own_help["effects"])
        other_help = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(viewer_present=False, module_id="matter-clock", approved_scopes=["help:write"], token_scopes=["help:write"]),
                "tool": "createModuleHelpPage",
                "arguments": {"path": {"module_id": "victim"}, "body": {"heading": "matter-clock"}},
            },
        )
        self.assertEqual(other_help["result"], "forbidden")
        self.assertFalse(other_help["effects"])
        human_help = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", human_session=True, approved_scopes=["help:write"], token_scopes=["help:write"]),
                "tool": "createModuleHelpPage",
                "arguments": {"path": {"module_id": "matter-clock"}, "body": {"heading": "matter-clock"}},
            },
        )
        self.assertEqual(human_help["result"], "forbidden")
        self.assertFalse(human_help["effects"])
        publication = background_grant_issuance(
            _operation(self.spec, "issueFilePublicationGrant"),
            {"installed": True, "registered": True, "module_id": "drop-folder", "approved_scopes": ["files:write"]},
            {
                "module_id": "drop-folder",
                "source": "drop_folder",
                "assignment": "matter",
                "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
                "expires_at": "2026-11-07T18:00:00Z",
                "matter_walled": False,
                "principal_matter_ids": ["018f1a00-7b3c-7d4e-8a11-333333333333"],
            },
        )
        self.assertEqual(publication["result"], "allowed")
        publication["grant"]["upload_ids"] = ["up-1"]
        owned = mcp_tool_dispatch(
            self.spec,
            _as_trusted_grant_fixture(
                {
                    **bearer(viewer_present=False, module_id="drop-folder", caller_module_id="drop-folder", token_scopes=["files:write"], approved_scopes=["files:write"]),
                    "tool": "getFileUpload",
                    "module_scopes": ["files:write"],
                    "registered": True,
                    "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
                    "owner": {"type": "module", "id": "drop-folder"},
                    "arguments": {"task_authorization": publication["grant"], "path": {"upload_id": "up-1"}},
                }
            ),
        )
        self.assertEqual(owned["result"], "allowed")
        self.assertTrue(owned["effects"])
        fabricated = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["files:write"], principal_user_id="018f1a00-7b3c-7d4e-8a11-999999999999"),
                "tool": "getFileUpload",
                "owner": {"type": "module", "id": "drop-folder"},
                "arguments": {"path": {"upload_id": "up-1"}},
            },
        )
        self.assertEqual(fabricated["result"], "not_found")
        self.assertFalse(fabricated["effects"])
        revoked_publication = mcp_tool_dispatch(
            self.spec,
            _as_trusted_grant_fixture(
                {
                    **bearer(viewer_present=False, module_id="drop-folder", caller_module_id="drop-folder"),
                    "tool": "getFileUpload",
                    "module_scopes": ["files:write"],
                    "registered": True,
                    "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
                    "arguments": {"task_authorization": {**publication["grant"], "revoked": True}, "path": {"upload_id": "up-1"}},
                }
            ),
        )
        self.assertEqual(revoked_publication["result"], "forbidden")
        self.assertFalse(revoked_publication["effects"])

    def test_writes_accept_idempotency_keys(self):
        for method, path, operation in _ops(self.spec):
            if method not in {"post", "put", "patch", "delete"}:
                continue
            if operation["x-togavsu-mcp"]["parity"] == "transport":
                continue
            names = [_param_name(parameter) for parameter in operation["parameters"]]
            self.assertIn("idempotencyKey", names, f"{method} {path}")

    def test_list_operations_use_cursor_pages(self):
        for _method, path, operation in _ops(self.spec):
            if not operation["operationId"].startswith("list"):
                continue
            names = [_param_name(parameter) for parameter in operation["parameters"]]
            self.assertIn("cursor", names, path)
            self.assertIn("limit", names, path)
        from contract.settings import SETTINGS

        rows = {item["key"]: item for item in SETTINGS}
        self.assertEqual(rows["api.page_size"]["default"], 50)
        self.assertEqual(rows["api.page_size"]["minimum"], 1)
        self.assertEqual(rows["api.page_size"]["maximum"], 10000)
        self.assertEqual(rows["api.page_size"]["tab"], "agents_and_api")
        self.assertEqual(rows["api.max_page_size"]["default"], 200)
        self.assertEqual(rows["api.max_page_size"]["maximum"], 10000)
        self.assertEqual(rows["activity.page_size"]["maximum"], 500)
        self.assertEqual(rows["notes.page_size"]["maximum"], 200)
        for _method, path, operation in _ops(self.spec):
            limit = next((parameter for parameter in operation.get("parameters") or [] if parameter.get("name") == "limit"), None)
            if limit is None:
                continue
            schema = limit["schema"]
            named = operation["x-togavsu-access"].get("page_size_setting")
            if named == "activity.page_size":
                self.assertEqual(schema["default"], 100, operation["operationId"])
                self.assertEqual(schema["maximum"], 500, operation["operationId"])
            elif named == "notes.page_size":
                self.assertEqual(schema["default"], 25, operation["operationId"])
                self.assertEqual(schema["maximum"], 200, operation["operationId"])
            else:
                self.assertEqual(named, "api.page_size", operation["operationId"])
                self.assertEqual(schema.get("default"), 50, path)
                self.assertEqual(schema.get("maximum"), 10000, path)
                self.assertIn("10000", limit["description"], path)
                self.assertIn("200", limit["description"], path)
            self.assertEqual(page_size_decision(operation, None)["limit"], schema["default"], operation["operationId"])
            self.assertEqual(page_size_decision(operation, schema["maximum"] + 1)["result"], "validation_failed", operation["operationId"])
        events = _operation(self.spec, "listEvents")
        notes = _operation(self.spec, "listNote")
        changed = page_size_decision(events, None, {"activity.page_size": 40})
        self.assertEqual(changed["limit"], 40)
        capped = page_size_decision(events, None, {"activity.page_size": 900})
        self.assertEqual(capped["limit"], 500)
        self.assertEqual(page_size_decision(events, 501)["result"], "validation_failed")
        general = page_size_decision(_operation(self.spec, "listMatter"), None, {"api.page_size": 80, "api.max_page_size": 100})
        self.assertEqual(general["limit"], 80)
        self.assertEqual(page_size_decision(_operation(self.spec, "listMatter"), 100, {"api.page_size": 80, "api.max_page_size": 100})["result"], "allowed")
        self.assertEqual(page_size_decision(_operation(self.spec, "listMatter"), 101, {"api.page_size": 80, "api.max_page_size": 100})["result"], "validation_failed")
        squeezed = page_size_decision(_operation(self.spec, "listMatter"), None, {"api.page_size": 300, "api.max_page_size": 200})
        self.assertEqual(squeezed["limit"], 200)
        self.assertEqual(notes["x-togavsu-access"]["page_size_setting"], "notes.page_size")
        raised = page_size_decision(_operation(self.spec, "listMatter"), 300, {"api.max_page_size": 500})
        self.assertEqual(raised["result"], "allowed")
        self.assertEqual(raised["limit"], 300)
        matter_limit = next(parameter["schema"] for parameter in _operation(self.spec, "listMatter")["parameters"] if parameter.get("name") == "limit")
        self.assertGreaterEqual(matter_limit["maximum"], 300)
        self.assertEqual(page_size_decision(_operation(self.spec, "listMatter"), 300, {"api.max_page_size": 100})["result"], "validation_failed")
        self.assertEqual(page_size_decision(events, 300)["result"], "allowed")
        self.assertEqual(page_size_decision(notes, 201)["result"], "validation_failed")
        tools = {tool["name"]: tool for tool in render_mcp(self.spec)["tools"]}
        self.assertEqual(tools["listMatter"]["limit"]["maximum"], 10000)
        self.assertEqual(tools["listEvents"]["limit"]["maximum"], 500)
        self.assertEqual(tools["listNote"]["limit"]["maximum"], 200)

    def test_trust_ledger_is_append_only(self):
        collection = self.spec["paths"]["/api/v1/trust-ledger"]
        item = self.spec["paths"]["/api/v1/trust-ledger/{trust_entry_id}"]
        self.assertEqual(set(collection), {"get", "post"})
        self.assertEqual(set(item), {"get"})
        self.assertIn("post", self.spec["paths"]["/api/v1/trust-ledger/{trust_entry_id}/reversals"])

    def test_below_zero_posts_and_flags(self):
        operation = self.spec["paths"]["/api/v1/trust-ledger"]["post"]
        access = operation["x-togavsu-access"]
        self.assertEqual(access["below_zero"], "posts")
        self.assertNotIn("accountant", access["roles"])
        self.assertNotIn("staff", access["entry_type_roles"]["withdrawal"])
        self.assertNotIn("accountant", access["entry_type_roles"]["deposit"])
        self.assertIn("staff", access["entry_type_roles"]["deposit"])
        self.assertIn("attorney", access["entry_type_roles"]["withdrawal"])
        text = operation["description"].lower()
        self.assertIn("trust flag", text)
        self.assertIn("email", text)
        codes = self.spec["components"]["schemas"]["Problem"]["properties"]["code"]["enum"]
        self.assertNotIn("below_zero", codes)

    def test_accountant_cannot_write_billing_or_trust(self):
        for method, path, operation in _ops(self.spec):
            if method not in {"post", "put", "patch", "delete"}:
                continue
            if not path.startswith(BILLING_PREFIXES):
                continue
            self.assertNotIn("accountant", operation["x-togavsu-access"]["roles"], f"{method} {path}")

    def test_conflict_decisions_are_attorney_only(self):
        operation = self.spec["paths"]["/api/v1/conflict-checks/{conflict_check_id}/decisions"]["post"]
        self.assertEqual(set(operation["x-togavsu-access"]["roles"]), {"admin", "attorney"})
        self.assertIn("conflicts:decide", operation["x-togavsu-access"]["scopes"])

    def test_search_is_staff_only_and_walled(self):
        operation = self.spec["paths"]["/api/v1/search"]["get"]
        roles = set(operation["x-togavsu-access"]["roles"])
        self.assertNotIn("client", roles)
        self.assertNotIn("accountant", roles)
        self.assertTrue(operation["x-togavsu-access"]["matter_wall"])

    def test_matter_reads_are_walled(self):
        operation = self.spec["paths"]["/api/v1/matters/{matter_id}"]["get"]
        self.assertTrue(operation["x-togavsu-access"]["matter_wall"])
        names = [_param_name(parameter) for parameter in operation["parameters"]]
        self.assertIn("include", names)

    def test_layout_cap(self):
        operation = self.spec["paths"]["/api/v1/me/layouts"]["post"]
        self.assertIn("layout_limit", operation["x-togavsu-error-codes"])
        self.assertIn("10", operation["description"])

    def test_audit_log_has_no_write(self):
        self.assertEqual(set(self.spec["paths"]["/api/v1/audit-log"]), {"get"})

    def test_file_versions_have_no_mutation(self):
        for path, item in self.spec["paths"].items():
            if "/versions" not in path:
                continue
            self.assertFalse({"put", "patch", "delete"} & set(item), path)

    def test_available_dates_do_not_write_events(self):
        operation = self.spec["paths"]["/api/v1/calendar/available-dates"]["post"]
        self.assertEqual(operation["x-togavsu-events"], [])
        self.assertFalse(operation["x-togavsu-writes"])

    def test_guide_hides_from_clients(self):
        for path in ("/api/v1/help/guide", "/help/guide", "/api/v1/help/modules/{module_id}/pages"):
            access = self.spec["paths"][path]["get"]["x-togavsu-access"]
            self.assertEqual(access["client"], "not_found", path)
            self.assertEqual(access["markdown"], "sanitize", path)
            self.assertEqual(access["runtime_registration"], "pending", path)
            self.assertEqual(access["on_update"], "replace", path)
            self.assertEqual(access["on_disable"], "hide", path)
            self.assertEqual(access["on_uninstall"], "remove", path)

    def test_editor_pin(self):
        editor = self.spec["components"]["schemas"]["EditorInfo"]
        self.assertEqual(
            editor["properties"]["commit"]["const"],
            "770a8fab12392485c0c2789fb254da7d9296df4d",
        )

    def test_naming_refuses_a_status_token(self):
        operation = self.spec["paths"]["/api/v1/doc-tender/naming"]["put"]
        self.assertIn("status_token_refused", operation["x-togavsu-error-codes"])

    def test_settings_defaults_and_locked_admin_totp(self):
        rows = {item["key"]: item for item in self.spec["x-togavsu-settings"]}
        for key, expected in PRD_SETTING_DEFAULTS.items():
            self.assertIn(key, rows, key)
            self.assertEqual(rows[key]["default"], expected, key)
        self.assertTrue(rows["security.totp_required.admin"]["locked"])
        enum = self.spec["components"]["schemas"]["SettingKey"]["enum"]
        self.assertEqual(set(enum), set(rows))

    def test_doc_category_seed(self):
        categories = self.spec["x-togavsu-seeds"]["doc_categories"]
        self.assertEqual(len(categories), 17)
        self.assertEqual({item["key"] for item in categories if item["tier"] == "high"}, HIGH_TIER)

    def test_widget_manifest_requires_a_target(self):
        widget = self.spec["components"]["schemas"]["ManifestWidget"]
        self.assertIn("target", widget["required"])

    def test_close_warns_without_a_block(self):
        operation = self.spec["paths"]["/api/v1/matters/{matter_id}/close"]["post"]
        text = operation["description"].lower()
        self.assertIn("warning", text)
        self.assertIn("does not stop", text)

    def test_disk_matches_the_builder(self):
        self.assertEqual(dump(self.spec), OPENAPI_PATH.read_text(encoding="utf-8"))
        self.assertEqual(dump(self.market), MARKETPLACE_PATH.read_text(encoding="utf-8"))
        self.assertEqual(dump(render_mcp(self.spec)), MCP_PATH.read_text(encoding="utf-8"))
        self.assertEqual(render_coverage(self.spec), COVERAGE_PATH.read_text(encoding="utf-8"))

    def test_trust_entry_scopes_deny_a_deposit_only_token(self):
        for operation_id in ("postTrustEntry", "bulkTrustEntry"):
            operation = _operation(self.spec, operation_id)
            access = operation["x-togavsu-access"]
            self.assertEqual(access["scope_combination"], "per_entry_type")
            scopes = access["entry_type_scopes"]
            self.assertEqual(scopes["deposit"], ["trust:write"])
            for entry_type in ("withdrawal", "transfer_to_operating", "refund", "adjustment"):
                self.assertEqual(scopes[entry_type], ["trust:withdraw"], entry_type)
            self.assertIn("scope_denied", operation["x-togavsu-error-codes"])
            denied = [case for case in access["scope_cases"] if case["result"] == "scope_denied"]
            self.assertGreaterEqual(len(denied), 4)
            for case in access["scope_cases"]:
                self.assertEqual(
                    _scope_decision(scopes, case["token_scopes"], case["entry_type"]),
                    case["result"],
                    case,
                )
            self.assertEqual(_scope_decision(scopes, ["trust:write"], "withdrawal"), "scope_denied")

    def test_matter_wall_is_inherited_on_named_flows(self):
        expected = {
            "getMatter": "matter",
            "listMatter": "matter",
            "search": "matter",
            "listConflictRecord": "matter",
            "getConflictRecord": "matter",
            "pauseTimer": "matter",
            "stopTimer": "matter",
            "cancelTrustReplenishment": "matter",
            "satisfyTrustReplenishment": "matter",
            "listDocTenderException": "file",
            "getDocTenderException": "file",
            "resolveDocTenderException": "file",
            "getDocTenderResult": "file",
            "quarantineFileVersion": "file",
            "updateFileMetadata": "file",
        }
        for operation_id, inherits in expected.items():
            access = _operation(self.spec, operation_id)["x-togavsu-access"]
            self.assertTrue(access["matter_wall"], operation_id)
            self.assertEqual(access["access_inherits"], inherits, operation_id)
        put_wall = _operation(self.spec, "putMatterWall")
        get_wall = _operation(self.spec, "getMatterWall")
        self.assertTrue(put_wall["x-togavsu-access"]["matter_wall"])
        self.assertTrue(get_wall["x-togavsu-access"]["matter_wall"])
        self.assertEqual(put_wall["x-togavsu-access"]["access_inherits"], "matter")
        self.assertEqual(get_wall["x-togavsu-access"]["access_inherits"], "matter")
        self.assertEqual(put_wall["x-togavsu-access"]["authorize_before"], "any_mutation")
        self.assertFalse(put_wall["x-togavsu-access"]["admin_bypasses_wall"])
        current = {"matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333", "user_ids": ["018f1a00-7b3c-7d4e-8a11-222222222222"]}
        off_wall = {
            "role": "admin",
            "matter_walled": True,
            "on_wall": False,
            "user_ids": [],
            "current_wall": current,
            "token_scopes": ["matters:write"],
            "matter_id": current["matter_id"],
        }
        denied = replace_matter_wall(put_wall, off_wall)
        self.assertEqual(denied["result"], "not_found")
        self.assertFalse(denied["mutated"])
        self.assertIsNone(denied["event"])
        self.assertEqual(denied["wall"], current)
        delegated = replace_matter_wall(
            put_wall,
            {**off_wall, "viewer_role": "admin", "caller_module_id": "clock", "module_scopes": ["matters:write"], "user_ids": ["018f1a00-7b3c-7d4e-8a11-111111111111"]},
        )
        self.assertEqual(delegated["result"], "not_found")
        self.assertFalse(delegated["mutated"])
        self.assertIsNone(delegated["event"])
        kept = replace_matter_wall(
            put_wall,
            {**off_wall, "on_wall": True, "user_ids": ["018f1a00-7b3c-7d4e-8a11-111111111111"]},
        )
        self.assertEqual(kept["result"], "allowed")
        self.assertTrue(kept["mutated"])
        self.assertEqual(kept["event"], "matter.wall_changed")
        opened = replace_matter_wall(
            put_wall,
            {"role": "admin", "matter_walled": False, "on_wall": False, "user_ids": ["018f1a00-7b3c-7d4e-8a11-111111111111"], "token_scopes": ["matters:write"], "matter_id": current["matter_id"]},
        )
        self.assertEqual(opened["result"], "allowed")
        self.assertTrue(opened["mutated"])
        nav = _operation(self.spec, "getNavContext")
        self.assertTrue(nav["x-togavsu-access"]["matter_wall"])
        self.assertFalse(nav["x-togavsu-access"]["admin_bypasses_wall"])
        self.assertEqual(nav["x-togavsu-access"]["badge_counts"]["filter_before"], "count")
        for operation_id in ("getBillingWorklist", "widgetMyOpenMatters", "reportArAging", "search", "listEvents"):
            access = _operation(self.spec, operation_id)["x-togavsu-access"]
            self.assertTrue(access["matter_wall"], operation_id)
            self.assertFalse(access.get("admin_bypasses_wall", False), operation_id)
        matter_a = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaaa"
        matter_b = "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbbb"
        contributions = [
            {"badge": "active_matters", "count": 3, "matter_walled": True, "matter_id": matter_a, "required_scopes": ["matters:read"]},
            {"badge": "active_matters", "count": 2, "matter_walled": False, "required_scopes": ["matters:read"]},
            {"badge": "draft_invoices", "count": 4, "matter_walled": True, "matter_id": matter_b, "required_scopes": ["invoices:read"]},
            {"badge": "draft_invoices", "count": 1, "matter_walled": False, "required_scopes": ["invoices:read"]},
        ]
        off_wall_admin = authorized_badge_counts(nav, {"role": "admin", "on_wall": False, "scopes": ["matters:read", "invoices:read"]}, contributions)
        self.assertEqual(off_wall_admin["badges"], {"active_matters": 2, "draft_invoices": 1})
        both = {"role": "admin", "matter_membership": {matter_a: True, matter_b: True}, "scopes": ["matters:read", "invoices:read"]}
        on_wall_admin = authorized_badge_counts(nav, both, contributions)
        self.assertEqual(on_wall_admin["badges"], {"active_matters": 5, "draft_invoices": 5})
        staff = authorized_badge_counts(nav, {"role": "staff", "matter_membership": {matter_a: True}, "scopes": ["matters:read"]}, contributions)
        self.assertEqual(staff["badges"], {"active_matters": 5})
        self.assertEqual(nav["x-togavsu-access"]["badge_counts"]["membership"], "per_matter_id")
        mixed_caller = {"role": "admin", "matter_membership": {matter_a: True, matter_b: False}, "scopes": ["matters:read", "invoices:read"]}
        mixed_badges = authorized_badge_counts(nav, mixed_caller, contributions)
        self.assertEqual(mixed_badges["badges"], {"active_matters": 5, "draft_invoices": 1})
        self.assertNotEqual(mixed_badges["badges"].get("draft_invoices"), 5)
        lone_flag = authorized_badge_counts(nav, {"role": "admin", "on_wall": True, "scopes": ["matters:read", "invoices:read"]}, contributions)
        self.assertEqual(lone_flag["badges"], {"active_matters": 2, "draft_invoices": 1})
        history = _operation(self.spec, "listEvents")
        rows = [
            {"id": "a", "matter_id": matter_a, "matter_walled": True, "summary": "allowed matter", "required_scopes": ["events:read"]},
            {"id": "b", "matter_id": matter_b, "matter_walled": True, "summary": "denied matter", "required_scopes": ["events:read"]},
            {"id": "firm", "matter_walled": False, "summary": "firm", "required_scopes": ["events:read"]},
        ]
        caller = {"role": "admin", "matter_membership": {matter_a: True, matter_b: False}, "scopes": ["events:read"]}
        kept = inherited_history_rows(history, caller, rows)
        self.assertEqual([row["id"] for row in kept["rows"]], ["a", "firm"])
        self.assertNotIn("denied matter", str(kept))
        page = matter_page(caller, rows, 1)
        self.assertEqual([row["id"] for row in page["data"]], ["a"])
        self.assertEqual(page["total"], 2)
        self.assertEqual(page["next_cursor"], "more")
        self.assertNotIn(matter_b, str(page))
        self.assertNotIn("denied matter", str(page))
        revoked = {**caller, "matter_membership": {matter_a: False, matter_b: False}}
        self.assertEqual([row["id"] for row in inherited_history_rows(history, revoked, rows)["rows"]], ["firm"])
        self.assertEqual([row["id"] for row in matter_page(revoked, rows, 10)["data"]], ["firm"])
        self.assertIsNone(matter_page(revoked, rows, 10)["next_cursor"])

    def test_first_admin_enrolls_totp_before_a_session(self):
        setup = self.spec["paths"]["/api/v1/setup"]["post"]
        self.assertEqual(setup["x-togavsu-next"], "confirmTotpEnrollment")
        self.assertEqual(_schema_name(setup["responses"]["201"]), "EnrollmentChallenge")
        challenge = self.spec["components"]["schemas"]["EnrollmentChallenge"]
        for field in ("enrollment_token", "otpauth_uri", "secret"):
            self.assertIn(field, challenge["required"])
        enroll = _operation(self.spec, "startTotpEnrollment")
        confirm = _operation(self.spec, "confirmTotpEnrollment")
        self.assertEqual(enroll["security"], [])
        self.assertEqual(confirm["security"], [])
        self.assertEqual(_schema_name(enroll["responses"]["200"]), "EnrollmentChallenge")
        started = self.spec["components"]["schemas"]["SessionStarted"]
        self.assertIn("recovery_codes", started["required"])
        self.assertEqual(_schema_name(confirm["responses"]["200"]), "SessionStarted")
        reset = _operation(self.spec, "resetTotp")
        confirm_reset = _operation(self.spec, "confirmTotpReset")
        self.assertNotEqual(reset["security"], [])
        self.assertNotEqual(confirm_reset["security"], [])
        challenge_schema = self.spec["components"]["schemas"]["TotpResetChallenge"]
        for field in ("enrollment_token", "expires_at", "otpauth_uri", "secret"):
            self.assertIn(field, challenge_schema["required"])
        self.assertEqual(_schema_name(reset["responses"]["200"]), "TotpResetChallenge")
        self.assertEqual(reset["x-togavsu-events"], ["user.totp_reset_started"])
        self.assertEqual(confirm_reset["x-togavsu-events"], ["user.totp_reset"])
        started = start_totp_reset(reset, {"role": "staff", "owner": "self", "caller_user_id": "user-1", "user_id": "user-1", "issued_token": "reset-1", "expires_at": "2026-11-07T18:10:00Z", "current_password": "correct-horse", "password_matches": True})
        self.assertEqual(started["result"], "allowed")
        self.assertTrue(started["old_secret_active"])
        self.assertFalse(started["activated"])
        self.assertEqual(started["challenge"]["enrollment_token"], "reset-1")
        pending = started["challenge"]
        wrong = confirm_totp_reset(confirm_reset, {"role": "staff", "owner": "self", "caller_user_id": "user-1", "enrollment_token": "reset-1", "code": "000000", "expected_code": "123456"}, pending)
        self.assertEqual(wrong["result"], "unauthenticated")
        self.assertTrue(wrong["old_secret_active"])
        self.assertFalse(pending["used"])
        other_owner = confirm_totp_reset(confirm_reset, {"role": "staff", "owner": "self", "caller_user_id": "user-2", "enrollment_token": "reset-1", "code": "123456", "expected_code": "123456"}, pending)
        self.assertEqual(other_owner["result"], "forbidden")
        self.assertTrue(other_owner["old_secret_active"])
        activated = confirm_totp_reset(confirm_reset, {"role": "staff", "owner": "self", "caller_user_id": "user-1", "enrollment_token": "reset-1", "code": "123456", "expected_code": "123456"}, pending)
        self.assertEqual(activated["result"], "allowed")
        self.assertTrue(activated["activated"])
        self.assertFalse(activated["old_secret_active"])
        self.assertTrue(activated["challenge"]["used"])
        replayed = confirm_totp_reset(confirm_reset, {"role": "staff", "owner": "self", "caller_user_id": "user-1", "enrollment_token": "reset-1", "code": "123456", "expected_code": "123456"}, activated["challenge"])
        self.assertEqual(replayed["result"], "forbidden")
        self.assertFalse(replayed["activated"])
        expired = confirm_totp_reset(confirm_reset, {"role": "staff", "owner": "self", "caller_user_id": "user-1", "enrollment_token": "reset-2", "code": "123456", "expected_code": "123456", "expired": True}, {**pending, "enrollment_token": "reset-2", "used": False})
        self.assertEqual(expired["result"], "forbidden")
        self.assertTrue(expired["old_secret_active"])

    def test_mcp_binary_upload_and_download(self):
        chunk = _operation(self.spec, "putFileChunk")
        transfer = chunk["x-togavsu-mcp"]["transfer"]
        self.assertEqual(transfer["body"]["encoding"], "base64")
        self.assertIn("Content-Range", transfer["headers"])
        self.assertIn("body_base64", transfer["arguments"])
        self.assertIn("headers", transfer["arguments"])
        download = _operation(self.spec, "downloadFileVersion")
        self.assertEqual(download["x-togavsu-mcp"]["transfer"]["result"]["encoding"], "base64")
        self.assertEqual(download["x-togavsu-mcp"]["transfer"]["result"]["media_type"], "application/octet-stream")
        self.assertEqual(_operation(self.spec, "getInvoicePdf")["x-togavsu-mcp"]["transfer"]["result"]["encoding"], "base64")
        catalog = render_mcp(self.spec)
        listed = next(tool for tool in catalog["tools"] if tool["name"] == "putFileChunk")
        self.assertEqual(listed["transfer"]["body"]["encoding"], "base64")
        listed_download = next(tool for tool in catalog["tools"] if tool["name"] == "downloadFileVersion")
        self.assertEqual(listed_download["transfer"]["result"]["encoding"], "base64")

    def test_local_module_install_is_multipart(self):
        operation = _operation(self.spec, "installModule")
        content = operation["requestBody"]["content"]
        self.assertIn("application/json", content)
        self.assertIn("multipart/form-data", content)
        self.assertEqual(content["multipart/form-data"]["schema"]["$ref"], "#/components/schemas/ModulePackageUpload")
        package = self.spec["components"]["schemas"]["ModulePackageUpload"]
        self.assertIn("package", package["required"])
        transfer = operation["x-togavsu-mcp"]["transfer"]
        self.assertEqual(transfer["body"]["encoding"], "one_of")
        self.assertEqual(transfer["body"]["multipart"]["package"]["encoding"], "base64")
        self.assertIn("package_base64", transfer["arguments"])

    def test_restore_accepts_a_downloaded_archive_and_passphrase(self):
        upload = _operation(self.spec, "createBackupUpload")
        self.assertEqual(upload["x-togavsu-mcp"]["transfer"]["body"]["encoding"], "base64")
        self.assertEqual(_schema_name(upload["responses"]["201"]), "BackupUpload")
        self.assertIn("upload_token", self.spec["components"]["schemas"]["BackupUpload"]["required"])
        write = self.spec["components"]["schemas"]["RestoreWrite"]
        self.assertIn("passphrase", write["required"])
        self.assertIn("upload_token", write["properties"])
        self.assertIn("backup_id", write["properties"])
        restore = _operation(self.spec, "createRestore")
        self.assertIn("passphrase", restore["description"])
        self.assertIn("upload_token", restore["description"])
        download = _operation(self.spec, "downloadBackup")
        self.assertEqual(download["x-togavsu-mcp"]["transfer"]["result"]["media_type"], "application/octet-stream")

    def test_upload_staging_is_separate_from_publication(self):
        complete = _operation(self.spec, "completeFileUpload")
        self.assertEqual(_schema_name(complete["responses"]["200"]), "FileUpload")
        self.assertNotIn("file.created", complete["x-togavsu-events"])
        self.assertNotIn("file.version_created", complete["x-togavsu-events"])
        self.assertIn("staged", complete["description"])
        publish = _operation(self.spec, "publishFile")
        self.assertIn("one transaction", publish["description"])
        self.assertIn("file.created", publish["x-togavsu-events"])
        attach = _operation(self.spec, "addFileVersion")
        self.assertIn("staged", attach["description"])
        self.assertIn("one transaction", attach["description"])
        bulk = _operation(self.spec, "bulkFile")
        self.assertIn("staged", bulk["description"])
        self.assertIn("batch_rejected", bulk["x-togavsu-error-codes"])

    def test_draft_invoice_lines_are_editable(self):
        operation = _operation(self.spec, "updateInvoiceDraft")
        self.assertEqual(operation["x-togavsu-access"]["required_status"], "draft")
        self.assertIn("conflict", operation["x-togavsu-error-codes"])
        self.assertNotIn("accountant", operation["x-togavsu-access"]["roles"])
        self.assertIn("invoices:write", operation["x-togavsu-access"]["scopes"])
        edit = self.spec["components"]["schemas"]["InvoiceDraftEdit"]
        self.assertIn("lines", edit["required"])
        self.assertIn("adjustment_cents", edit["properties"])
        self.assertIn("adjustment_reason", edit["properties"])
        line = self.spec["components"]["schemas"]["InvoiceLine"]["properties"]
        for field in ("fee_item_id", "description", "amount_cents"):
            self.assertIn(field, line)

    def test_file_metadata_edit_respects_locks(self):
        operation = _operation(self.spec, "updateFileMetadata")
        self.assertTrue(operation["x-togavsu-access"]["matter_wall"])
        lock = operation["x-togavsu-access"]["person_lock"]
        self.assertEqual(lock["human_edit"], "allowed")
        self.assertEqual(lock["automated_actor"], "field_locked")
        self.assertIn("doc_tender", lock["blocked_actors"])
        self.assertNotIn("field_locked", operation.get("x-togavsu-error-codes", []))
        self.assertIn("file.updated", operation["x-togavsu-events"])
        self.assertNotIn("file.version_created", operation["x-togavsu-events"])
        fields = self.spec["components"]["schemas"]["FileMetadataWrite"]["properties"]
        for field in ("attorney_only", "shared_with_client", "category_id", "matter_id"):
            self.assertIn(field, fields)
        rename = _operation(self.spec, "renameFile")
        self.assertEqual(rename["x-togavsu-access"]["person_lock"]["human_edit"], "allowed")
        self.assertNotIn("field_locked", rename.get("x-togavsu-error-codes", []))

    def test_complete_module_manifest_validates(self):
        import copy

        from jsonschema import Draft202012Validator

        schemas = copy.deepcopy(self.spec["components"]["schemas"])

        def rewrite(node):
            if isinstance(node, dict):
                ref = node.get("$ref")
                if isinstance(ref, str) and ref.startswith("#/components/schemas/"):
                    node["$ref"] = "#/$defs/" + ref.rsplit("/", 1)[1]
                for value in node.values():
                    rewrite(value)
            elif isinstance(node, list):
                for value in node:
                    rewrite(value)

        rewrite(schemas)
        validator = Draft202012Validator(
            {
                "$schema": "https://json-schema.org/draft/2020-12/schema",
                "$defs": schemas,
                "$ref": "#/$defs/ModuleManifest",
            }
        )
        manifest = {
            "id": "matter-clock",
            "name": "Matter Clock",
            "version": "0.1.0",
            "image": {"reference": "ghcr.io/example/matter-clock", "digest": "sha256:abc"},
            "permissions": ["matters:read", {"scope": "files:write", "required": False}],
            "widgets": [
                {
                    "id": "hours",
                    "title": "Hours this month",
                    "default_size": {"w": 3, "h": 2},
                    "allowed_sizes": [{"w": 3, "h": 2}, {"w": 4, "h": 2}],
                    "target": {"kind": "module", "path": "hours", "filters": {"month": "current"}, "scopes": ["matters:read"], "roles": ["staff"]},
                },
                {
                    "id": "deadlines",
                    "title": "Deadlines",
                    "default_size": {"w": 4, "h": 2},
                    "allowed_sizes": [{"w": 4, "h": 2}],
                    "target": {
                        "kind": "core",
                        "route": "deadlines",
                        "filters": {"from_date": "2026-11-01", "to_date": "2026-11-07"},
                        "scopes": ["deadlines:read"],
                        "roles": ["staff"],
                    },
                },
            ],
            "sidebar_panel": {
                "id": "softphone",
                "title": "Softphone",
                "slot": "top-band",
                "target": "softphone",
                "scopes": ["matters:read"],
                "roles": ["staff"],
            },
            "sidebar": [
                {
                    "id": "targets",
                    "section": "Activity",
                    "label": "Targets",
                    "icon": "clock",
                    "order": 10,
                    "target": "targets",
                    "scopes": ["matters:read"],
                    "roles": ["staff"],
                },
                {
                    "id": "reports",
                    "section": "Activity",
                    "label": "Reports",
                    "icon": "clock",
                    "order": 20,
                    "target": "reports",
                    "scopes": ["matters:read"],
                    "roles": ["staff"],
                },
            ],
            "quick_create": [
                {"id": "set-target", "label": "Set target", "target": "target", "scopes": ["matters:read"], "roles": ["staff"]}
            ],
            "search_scopes": [{"id": "matter-clock", "label": "Targets"}],
            "matter_screen": {
                "panels": [
                    {"id": "target", "title": "Monthly target", "target": "targets", "scopes": ["matters:read"], "roles": ["staff"]},
                    {"id": "history", "title": "History", "target": "history", "scopes": ["matters:read"], "roles": ["staff"]},
                ],
                "actions": [
                    {"id": "edit-target", "label": "Edit target", "target": "edit-target", "scopes": ["matters:write"], "roles": ["staff"]},
                    {"id": "close-target", "label": "Close target", "target": "close-target", "scopes": ["matters:write"], "roles": ["staff"]},
                ],
            },
            "tabs": [
                {
                    "id": "hours-home",
                    "title": "Hours",
                    "placement": "home-screen",
                    "target": "hours",
                    "guide": "hours-guide",
                    "scopes": ["matters:read"],
                    "roles": ["staff"],
                },
                {
                    "id": "hours-matter",
                    "title": "Matter hours",
                    "placement": "matter-screen",
                    "target": "history",
                    "guide": "matter-guide",
                    "scopes": ["matters:read"],
                    "roles": ["staff"],
                },
            ],
            "help": {
                "heading": "Matter Clock",
                "pages": [
                    {
                        "id": "hours-guide",
                        "title": "Hours",
                        "markdown": "Read the hours widget.",
                        "keywords": ["hours"],
                        "covers": [{"kind": "widget", "id": "hours"}, {"kind": "tab", "id": "hours-home"}],
                    },
                    {
                        "id": "matter-guide",
                        "title": "Matter hours",
                        "markdown": "The matter tab. <script>alert(1)</script>",
                        "keywords": ["matter"],
                        "covers": [{"kind": "tab", "id": "hours-matter"}, {"kind": "screen", "id": "history"}],
                    },
                ],
            },
            "data": {
                "migrations": [{"version": 1, "name": "targets", "checksum": "sha256:" + ("ab" * 32)}],
                "extensions": [{"core_record": "matter", "table": "matter_targets", "primary_key": "id", "foreign_key": "id"}],
                "views": [
                    {
                        "name": "targets",
                        "core_record": "matter",
                        "key_column": "id",
                        "columns": ["goal_hours"],
                        "scope": "views:matter-clock.targets:read",
                    }
                ],
                "custom_fields": [
                    {
                        "record_type": "matter",
                        "key": "matter-clock.goal_hours",
                        "label": "Goal hours",
                        "field_type": "number",
                        "sort_order": 10,
                    },
                    {
                        "record_type": "matter",
                        "key": "matter-clock.stage",
                        "label": "Stage",
                        "field_type": "pick_list",
                        "sort_order": 20,
                        "options": ["intake", "active"],
                        "practice_area_ids": ["018f1a00-7b3c-7d4e-8a11-111111111111"],
                    },
                ],
            },
        }
        validator.validate(manifest)
        from contract.policy import MODULE_ID_PATTERN

        self.assertEqual(self.spec["components"]["schemas"]["ModuleManifest"]["properties"]["id"]["pattern"], MODULE_ID_PATTERN)
        module_parameter = next(parameter for parameter in _operation(self.spec, "getModule")["parameters"] if parameter.get("name") == "module_id")
        self.assertEqual(module_parameter["schema"]["pattern"], MODULE_ID_PATTERN)
        self.assertEqual(normalize_module_target("matter-clock", "hours"), "/m/matter-clock/hours")
        self.assertTrue(module_id_slug("ab-c"))
        hostile_ids = (
            "foo/../victim",
            "foo/bar",
            "foo\\bar",
            "..",
            ".",
            "foo/./bar",
            "%2e%2e",
            "%2f",
            "foo%2fbar",
            "a?b",
            "a#b",
            "foo\u0000bar",
            "matter\u2044clock",
            "foo\u2215bar",
            "foo\uff0fbar",
            "Foo",
            "-leading",
        )
        for hostile_id in hostile_ids:
            self.assertFalse(module_id_slug(hostile_id), hostile_id)
            self.assertIsNone(normalize_module_target(hostile_id, "hours"), hostile_id)
            hostile_manifest = copy.deepcopy(manifest)
            hostile_manifest["id"] = hostile_id
            with self.assertRaises(Exception, msg=hostile_id):
                validator.validate(hostile_manifest)
            contributed = manifest_contributions(hostile_manifest, {"role": "staff"})
            self.assertEqual(contributed["result"], "validation_failed", hostile_id)
            self.assertEqual(contributed["pages"], [], hostile_id)
            self.assertEqual(contributed["tabs"], [], hostile_id)
            self.assertNotIn("/m/victim", str(contributed), hostile_id)
            self.assertNotIn("/m/foo", str(contributed), hostile_id)
            self.assertEqual(
                sidebar_entries(hostile_id, manifest["sidebar"], {"role": "staff", "scopes": ["matters:read"]})["result"],
                "validation_failed",
                hostile_id,
            )
            self.assertEqual(
                manifest_custom_fields(hostile_manifest)["result"],
                "validation_failed",
                hostile_id,
            )
            help_call = {"actor": "module", "module_id": hostile_id, "path_module_id": hostile_id, "scopes": ["help:write"], "heading": hostile_id}
            self.assertEqual(module_help_registration(_operation(self.spec, "createModuleHelpPage"), help_call), "forbidden", hostile_id)
            bound = issue_api_token(
                _operation(self.spec, "createApiToken"),
                {
                    "role": "admin",
                    "credential": "session",
                    "caller_scopes": ["tokens:write", "files:write"],
                    "name": "importer",
                    "module_id": hostile_id,
                    "scopes": ["files:write"],
                    "module_approved_scopes": ["files:write"],
                },
            )
            self.assertEqual(bound["result"], "validation_failed", hostile_id)
            self.assertIsNone(bound["token"], hostile_id)
        widget = self.spec["components"]["schemas"]["ManifestWidget"]
        self.assertIn("allowed_sizes", widget["required"])
        for field in ("sidebar", "quick_create", "search_scopes", "matter_screen"):
            self.assertIn(field, self.spec["components"]["schemas"]["ModuleManifest"]["properties"])
        opened = sidebar_entries(
            "matter-clock",
            manifest["sidebar"],
            {"role": "staff", "scopes": ["matters:read"]},
        )
        self.assertEqual(opened["result"], "allowed")
        self.assertEqual([page["path"] for page in opened["pages"]], ["/m/matter-clock/targets", "/m/matter-clock/reports"])
        self.assertEqual(len({page["path"] for page in opened["pages"]}), 2)
        hidden = sidebar_entries("matter-clock", manifest["sidebar"], {"role": "client", "scopes": ["matters:read"]})
        self.assertEqual(hidden["pages"], [])
        for hostile in (
            "https://evil.example/page",
            "javascript:alert(1)",
            "data:text/html,hi",
            "//evil.example/page",
            "../other-module/secret",
            "/api/v1/matters",
            "/m/other-module/page",
            "reports/../../secrets",
            "Targets",
        ):
            refused = sidebar_entries(
                "matter-clock",
                [{**manifest["sidebar"][0], "target": hostile}],
                {"role": "staff", "scopes": ["matters:read"]},
            )
            self.assertEqual(refused["result"], "validation_failed", hostile)
            self.assertEqual(refused["pages"], [], hostile)
        duplicate = sidebar_entries(
            "matter-clock",
            [manifest["sidebar"][0], {**manifest["sidebar"][1], "id": "targets", "target": "other"}],
            {"role": "staff", "scopes": ["matters:read"]},
        )
        self.assertEqual(duplicate["result"], "validation_failed")
        panels = module_ui_targets("matter-clock", manifest["matter_screen"]["panels"], {"role": "staff", "scopes": ["matters:read"]})
        actions = module_ui_targets("matter-clock", manifest["matter_screen"]["actions"], {"role": "staff", "scopes": ["matters:write"]})
        self.assertEqual([page["path"] for page in panels["pages"]], ["/m/matter-clock/targets", "/m/matter-clock/history"])
        self.assertEqual([page["path"] for page in actions["pages"]], ["/m/matter-clock/edit-target", "/m/matter-clock/close-target"])
        self.assertEqual(module_ui_targets("matter-clock", manifest["matter_screen"]["panels"], {"role": "staff", "scopes": []})["pages"], [])
        self.assertEqual(module_ui_targets("matter-clock", [{"id": "bad", "target": "https://evil.example", "roles": ["staff"], "scopes": ["matters:read"]}], {"role": "staff", "scopes": ["matters:read"]})["result"], "validation_failed")
        install = _operation(self.spec, "installModule")
        self.assertEqual(install["x-togavsu-mcp"]["parity"], "required")
        self.assertIn("ModuleManifest", install["description"])
        matter = _operation(self.spec, "getMatter")
        self.assertEqual(
            include_views_decision(matter, {"role": "staff", "approved_scopes": ["matters:read", "views:matter-clock.targets:read"], "include": ["matter-clock.targets"]}),
            "allowed",
        )
        broken = copy.deepcopy(manifest)
        broken["data"] = {"views": [{"name": "targets", "core_record": "not_a_record", "key_column": "id", "columns": ["goal_hours"], "scope": "views:matter-clock.targets:read"}]}
        with self.assertRaises(Exception):
            validator.validate(broken)
        missing_columns = copy.deepcopy(manifest)
        missing_columns["data"] = {"views": [{"name": "targets", "core_record": "matter", "key_column": "id", "columns": [], "scope": "views:matter-clock.targets:read"}]}
        with self.assertRaises(Exception):
            validator.validate(missing_columns)
        bad_name = copy.deepcopy(manifest)
        bad_name["data"] = {"extensions": [{"core_record": "matter", "table": "MatterTargets", "primary_key": "id", "foreign_key": "id"}]}
        with self.assertRaises(Exception):
            validator.validate(bad_name)
        omitted = {key: value for key, value in manifest.items() if key != "data"}
        validator.validate(omitted)
        empty_data = {**omitted, "data": {}}
        validator.validate(empty_data)
        no_ui = {key: value for key, value in manifest.items() if key not in ("help", "tabs")}
        validator.validate(no_ui)
        for label, broken_manifest in (
            ("blank-help", {**manifest, "help": {}}),
            ("blank-tab", {**manifest, "tabs": [{}]}),
            ("admin-placement", {**manifest, "tabs": [{**manifest["tabs"][0], "placement": "admin"}]}),
            ("profile-placement", {**manifest, "tabs": [{**manifest["tabs"][0], "placement": "profile"}]}),
            ("raw-core-url", {**manifest, "widgets": [{**manifest["widgets"][1], "target": {**manifest["widgets"][1]["target"], "route": "https://evil.example/deadlines"}}]}),
            ("other-module-widget", {**manifest, "widgets": [{**manifest["widgets"][0], "target": {**manifest["widgets"][0]["target"], "path": "/m/other-module/page"}}]}),
        ):
            with self.assertRaises(Exception, msg=label):
                validator.validate(broken_manifest)
        staff = {"role": "staff", "scopes": ["deadlines:read", "matters:read"]}
        core_page = open_widget_target("matter-clock", manifest["widgets"][1]["target"], staff)
        self.assertEqual(core_page["result"], "allowed")
        self.assertEqual(core_page["route"], "deadlines")
        self.assertEqual(core_page["operation"], "listDeadline")
        self.assertEqual(core_page["filters"]["from_date"], "2026-11-01")
        self.assertIsNone(core_page["path"])
        own_page = open_widget_target("matter-clock", manifest["widgets"][0]["target"], {"role": "staff", "scopes": ["matters:read"]})
        self.assertEqual(own_page["path"], "/m/matter-clock/hours")
        unauthorized = open_widget_target(
            "matter-clock",
            {"kind": "core", "route": "ar_aging", "filters": {}, "scopes": ["reports:read"], "roles": ["staff"]},
            {"role": "staff", "scopes": ["matters:read"]},
        )
        self.assertEqual(unauthorized["result"], "forbidden")
        self.assertIsNone(unauthorized["route"])
        walled = open_widget_target(
            "matter-clock",
            {**manifest["widgets"][1]["target"], "filters": {"matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333"}},
            {**staff, "matter_walled": True, "on_wall": False},
        )
        self.assertEqual(walled["result"], "not_found")
        for hostile in (
            {"kind": "module", "path": "javascript:alert(1)", "scopes": ["matters:read"], "roles": ["staff"]},
            {"kind": "module", "path": "https://evil.example/page", "scopes": ["matters:read"], "roles": ["staff"]},
            {"kind": "module", "path": "data:text/html,hi", "scopes": ["matters:read"], "roles": ["staff"]},
            {"kind": "module", "path": "//evil.example/page", "scopes": ["matters:read"], "roles": ["staff"]},
            {"kind": "module", "path": "../other-module/secret", "scopes": ["matters:read"], "roles": ["staff"]},
            {"kind": "core", "route": "https://evil.example", "scopes": ["matters:read"], "roles": ["staff"]},
            {"kind": "core", "route": "/api/v1/matters", "scopes": ["matters:read"], "roles": ["staff"]},
        ):
            refused_target = open_widget_target("matter-clock", hostile, staff)
            self.assertEqual(refused_target["result"], "validation_failed", hostile)
            self.assertIsNone(refused_target["path"], hostile)
        for route, page in CORE_PAGE_TARGETS.items():
            operation = _operation(self.spec, page["operation"])
            self.assertEqual(operation["x-togavsu-access"]["roles"], page["roles"], route)
            self.assertEqual(operation["x-togavsu-access"]["scopes"], page["scopes"], route)
        guides = manifest_contributions(manifest, {"role": "staff"})
        self.assertEqual(guides["result"], "allowed")
        self.assertEqual(guides["namespace"], "matter-clock")
        self.assertEqual(guides["heading"], "Matter Clock")
        self.assertEqual([page["id"] for page in guides["pages"]], ["hours-guide", "matter-guide"])
        self.assertEqual([tab["path"] for tab in guides["tabs"]], ["/m/matter-clock/hours", "/m/matter-clock/history"])
        rendered = guides["pages"][1]["markdown"]
        self.assertIn("matter tab", rendered)
        self.assertNotIn("<script", rendered.lower())
        self.assertNotIn("onmouseover", rendered.lower())
        for hostile in (
            '<a onmouseover="alert(1)">x</a>',
            '<a href="javascript:alert(1)">x</a>',
            '<a href="JaVaScRiPt:alert(1)">x</a>',
            '<a href="&#106;avascript:alert(1)">x</a>',
            '<a href="java&#115;cript:alert(1)">x</a>',
            "<ScRiPt>alert(1)</ScRiPt>",
            "<script>alert(1)",
            '<img src=x onerror=alert(1)>',
            '<a href="data:text/html,hi">x</a>',
        ):
            safe = rendered_markdown(hostile)
            lowered = safe.lower()
            self.assertNotIn("<script", lowered, hostile)
            self.assertNotIn("onmouseover", lowered, hostile)
            self.assertNotIn("onerror", lowered, hostile)
            self.assertNotIn("javascript:", lowered, hostile)
            self.assertNotIn("data:", lowered, hostile)
            if hostile.lower().startswith("<a"):
                self.assertIn("x", safe, hostile)
        self.assertIn(">x</a>", rendered_markdown('<a href="https://example.com/help" onclick="alert(1)">x</a>'))
        self.assertIn('href="https://example.com/help"', rendered_markdown('<a href="https://example.com/help" onclick="alert(1)">x</a>'))
        self.assertEqual(manifest_contributions(manifest, {"role": "client"})["result"], "not_found")
        self.assertEqual(manifest_contributions(manifest, {"role": "client"})["pages"], [])
        dangling = copy.deepcopy(manifest)
        dangling["tabs"][0]["guide"] = "missing-guide"
        self.assertEqual(manifest_contributions(dangling)["result"], "validation_failed")
        wrong_target = copy.deepcopy(manifest)
        wrong_target["tabs"][1]["target"] = "https://evil.example/tab"
        self.assertEqual(manifest_contributions(wrong_target)["result"], "validation_failed")
        omitted_ui = manifest_contributions(no_ui, {"role": "staff"})
        self.assertEqual(omitted_ui["result"], "allowed")
        self.assertEqual(omitted_ui["pages"], [])
        panel = sidebar_panel("matter-clock", manifest["sidebar_panel"], {"role": "staff", "scopes": ["matters:read"]})
        self.assertEqual(panel["result"], "allowed")
        self.assertEqual(panel["path"], "/m/matter-clock/softphone")
        self.assertEqual(sidebar_panel("matter-clock", manifest["sidebar_panel"], {"role": "client", "scopes": ["matters:read"]})["result"], "forbidden")
        self.assertEqual(sidebar_panel("matter-clock", {**manifest["sidebar_panel"], "target": "https://evil.example"}, {"role": "staff", "scopes": ["matters:read"]})["result"], "validation_failed")
        self.assertEqual(sidebar_panel("matter-clock", [manifest["sidebar_panel"], {**manifest["sidebar_panel"], "id": "second"}])["result"], "validation_failed")
        two_panels = copy.deepcopy(manifest)
        two_panels["sidebar_panel"] = [manifest["sidebar_panel"], manifest["sidebar_panel"]]
        with self.assertRaises(Exception):
            validator.validate(two_panels)
        fields = manifest_custom_fields(manifest, "matter-clock")
        self.assertEqual(fields["result"], "allowed")
        self.assertEqual(fields["module_id"], "matter-clock")
        self.assertEqual([field["key"] for field in fields["fields"]], ["matter-clock.goal_hours", "matter-clock.stage"])
        self.assertEqual(fields["fields"][1]["options"], ["intake", "active"])
        bare = {key: value for key, value in manifest.items() if key != "data"}
        self.assertEqual(manifest_custom_fields(bare)["fields"], [])
        self.assertEqual(manifest_custom_fields({**bare, "data": {}})["fields"], [])
        forged = manifest_custom_fields(manifest, "other-module")
        self.assertEqual(forged["result"], "validation_failed")
        self.assertEqual(forged["fields"], [])
        foreign_key = copy.deepcopy(manifest)
        foreign_key["data"]["custom_fields"][0]["key"] = "other-module.goal_hours"
        self.assertEqual(manifest_custom_fields(foreign_key)["result"], "validation_failed")
        duplicate_key = copy.deepcopy(manifest)
        duplicate_key["data"]["custom_fields"].append(dict(duplicate_key["data"]["custom_fields"][0]))
        self.assertEqual(manifest_custom_fields(duplicate_key)["result"], "validation_failed")
        bad_pick = copy.deepcopy(manifest)
        bad_pick["data"]["custom_fields"][1]["options"] = ["intake", "intake"]
        self.assertEqual(manifest_custom_fields(bad_pick)["result"], "validation_failed")
        for label, broken_field in (
            ("bad-type", {**manifest["data"]["custom_fields"][0], "field_type": "blob"}),
            ("bad-record", {**manifest["data"]["custom_fields"][0], "record_type": "invoice"}),
            ("pick-without-options", {"record_type": "matter", "key": "matter-clock.stage", "label": "Stage", "field_type": "pick_list", "sort_order": 1}),
            ("options-on-text", {**manifest["data"]["custom_fields"][0], "options": ["a"]}),
        ):
            bad_fields = copy.deepcopy(manifest)
            bad_fields["data"]["custom_fields"] = [broken_field]
            with self.assertRaises(Exception, msg=label):
                validator.validate(bad_fields)
        optional = decide_module_permissions(
            manifest,
            [{"scope": "matters:read", "decision": "approve"}, {"scope": "files:write", "decision": "decline", "required": True}],
            signature_verified=True,
        )
        self.assertEqual(optional["result"], "installed")
        self.assertEqual(optional["token_scopes"], ["matters:read"])
        self.assertFalse(next(row for row in optional["permissions"] if row["scope"] == "files:write")["approved"])
        self.assertTrue(next(row for row in optional["permissions"] if row["scope"] == "matters:read")["required"])
        required_decline = decide_module_permissions(
            manifest,
            [{"scope": "matters:read", "decision": "decline", "required": False}, {"scope": "files:write", "decision": "approve"}],
            signature_verified=True,
        )
        self.assertEqual(required_decline["outcome"], "cancelled")
        self.assertEqual(required_decline["token_scopes"], [])
        waiting = decide_module_permissions(
            {**manifest, "version": "0.2.0", "permissions": ["matters:read", "trust:read"]},
            [],
            signature_verified=True,
            previous={"version": "0.1.0", "approved_scopes": ["matters:read"]},
        )
        self.assertEqual(waiting["outcome"], "awaiting_approval")
        self.assertEqual(waiting["version"], "0.1.0")
        self.assertEqual(waiting["token_scopes"], ["matters:read"])
        remove_and_add = decide_module_permissions(
            {**manifest, "version": "0.2.0", "permissions": ["matters:read", "trust:read"]},
            [],
            signature_verified=True,
            previous={"version": "0.1.0", "approved_scopes": ["matters:read", "files:write"]},
        )
        self.assertEqual(remove_and_add["outcome"], "awaiting_approval")
        self.assertEqual(remove_and_add["version"], "0.1.0")
        self.assertEqual(remove_and_add["token_scopes"], ["matters:read", "files:write"])
        self.assertEqual(remove_and_add["active_scopes"], ["matters:read", "files:write"])
        self.assertNotIn("trust:read", remove_and_add["token_scopes"])
        revoked = decide_module_permissions(
            {**manifest, "version": "0.2.0", "permissions": ["matters:read", "trust:read"]},
            [{"scope": "files:write", "decision": "decline"}],
            signature_verified=True,
            previous={"version": "0.1.0", "approved_scopes": ["matters:read", "files:write"]},
        )
        self.assertEqual(revoked["outcome"], "awaiting_approval")
        self.assertEqual(revoked["version"], "0.1.0")
        self.assertEqual(revoked["token_scopes"], ["matters:read"])
        self.assertNotIn("files:write", revoked["token_scopes"])
        self.assertNotIn("trust:read", revoked["token_scopes"])
        self.assertEqual(decide_module_permissions(manifest, [{"scope": "matters:read", "decision": "approve"}], signature_verified=False)["result"], "unsigned_module")
        self.assertEqual(decide_module_permissions({**manifest, "permissions": ["matters:read", "matters:read"]}, [], signature_verified=True)["result"], "validation_failed")
        self.assertEqual(
            decide_module_permissions(
                {**manifest, "permissions": ["matters:read", {"scope": "matters:read", "required": False}]},
                [],
                signature_verified=True,
            )["result"],
            "validation_failed",
        )
        self.assertEqual(decide_module_permissions({**manifest, "permissions": ["settings:write"]}, [], signature_verified=True)["result"], "validation_failed")
        published = ["matter-clock.targets"]
        view_scope = "views:matter-clock.targets:read"
        unchanged = decide_module_permissions(
            {**manifest, "version": "0.2.0", "permissions": ["matters:read", {"scope": "files:write", "required": False}]},
            [],
            signature_verified=True,
            previous={"version": "0.1.0", "approved_scopes": ["matters:read", "files:write"]},
        )
        self.assertEqual(unchanged["outcome"], "installed")
        self.assertEqual(unchanged["version"], "0.2.0")
        self.assertEqual(unchanged["token_scopes"], ["matters:read", "files:write"])
        only_new = decide_module_permissions(
            {**manifest, "version": "0.2.0", "permissions": ["matters:read", "trust:read"]},
            [{"scope": "trust:read", "decision": "approve"}],
            signature_verified=True,
            previous={"version": "0.1.0", "approved_scopes": ["matters:read"]},
        )
        self.assertEqual(only_new["outcome"], "installed")
        self.assertEqual(only_new["token_scopes"], ["matters:read", "trust:read"])
        self.assertNotIn("notes:read", only_new["token_scopes"])
        removed = decide_module_permissions(
            {**manifest, "version": "0.2.0", "permissions": ["matters:read"]},
            [{"scope": "files:write", "decision": "decline"}],
            signature_verified=True,
            previous={"version": "0.1.0", "approved_scopes": ["matters:read", "files:write", "notes:read"]},
        )
        self.assertEqual(removed["token_scopes"], ["matters:read"])
        self.assertNotIn("files:write", removed["token_scopes"])
        self.assertNotIn("notes:read", removed["token_scopes"])
        for bad_scope in ("views:matter-clock.*:read", "views:matter-clock.targets:write", "views:matter-clock.missing:read", "users:write"):
            self.assertEqual(
                decide_module_permissions({**manifest, "permissions": ["matters:read", bad_scope]}, [], signature_verified=True, published_views=published)["result"],
                "validation_failed",
                bad_scope,
            )
        granted = decide_module_permissions(
            {"id": "intake", "version": "0.2.0", "permissions": ["matters:read", {"scope": view_scope, "required": False}]},
            [{"scope": view_scope, "decision": "approve", "required": True}],
            signature_verified=True,
            previous={"version": "0.1.0", "approved_scopes": ["matters:read"]},
            published_views=published,
        )
        self.assertEqual(granted["outcome"], "installed")
        self.assertEqual(granted["token_scopes"], ["matters:read", view_scope])
        self.assertTrue(next(row for row in granted["permissions"] if row["scope"] == view_scope)["required"] is False)
        matter = _operation(self.spec, "getMatter")
        self.assertEqual(
            include_views_decision(matter, {"role": "staff", "approved_scopes": granted["token_scopes"], "current_approved_scopes": granted["token_scopes"], "include": ["matter-clock.targets"]}),
            "allowed",
        )
        self.assertEqual(
            include_views_decision(matter, {"role": "staff", "approved_scopes": ["matters:read", view_scope], "current_approved_scopes": ["matters:read"], "include": ["matter-clock.targets"]}),
            "forbidden",
        )
        self.assertEqual(
            include_views_decision(matter, {"role": "staff", "approved_scopes": granted["token_scopes"], "include": ["other.secret"]}),
            "forbidden",
        )
        self.assertEqual(
            include_views_decision(matter, {"role": "client", "approved_scopes": granted["token_scopes"], "include": ["matter-clock.targets"]}),
            "forbidden",
        )
        self.assertEqual(
            include_views_decision(
                matter,
                {"role": "staff", "approved_scopes": granted["token_scopes"], "include": ["matter-clock.targets"], "matter_walled": True, "on_wall": False},
            ),
            "not_found",
        )
        held = decide_module_permissions(
            {"id": "intake", "version": "0.2.0", "permissions": ["matters:read", view_scope]},
            [],
            signature_verified=True,
            previous={"version": "0.1.0", "approved_scopes": ["matters:read"]},
            published_views=published,
        )
        self.assertEqual(held["outcome"], "awaiting_approval")
        self.assertEqual(held["version"], "0.1.0")
        self.assertEqual(held["token_scopes"], ["matters:read"])
        self.assertNotIn(view_scope, held["token_scopes"])

    def test_doc_tender_result_is_readable(self):
        file_schema = self.spec["components"]["schemas"]["File"]["properties"]
        self.assertIn("summary", file_schema)
        result = self.spec["components"]["schemas"]["DocTenderResult"]
        for field in ("summary", "fields", "file_id", "file_version_id"):
            self.assertIn(field, result["required"])
        cited = self.spec["components"]["schemas"]["CitedField"]["properties"]
        self.assertIn("confidence", cited)
        citation_fields = set()
        for branch in self.spec["components"]["schemas"]["Citation"]["oneOf"]:
            citation_fields.update(branch["properties"])
        self.assertEqual(citation_fields, {"page", "timestamp"})
        operation = _operation(self.spec, "getDocTenderResult")
        self.assertTrue(operation["x-togavsu-access"]["matter_wall"])
        self.assertEqual(operation["x-togavsu-access"]["access_inherits"], "file")
        self.assertIn("doc_tender:read", operation["x-togavsu-access"]["scopes"])
        self.assertEqual(_schema_name(operation["responses"]["200"]), "DocTenderResult")
        retry = _operation(self.spec, "retryDocTenderJob")
        self.assertEqual(retry["x-togavsu-events"], ["doc_tender.retry_scheduled"])
        self.assertNotIn("doc_tender.published", retry["x-togavsu-events"])
        self.assertEqual(doc_tender_events(retry, "retry_accepted"), ["doc_tender.retry_scheduled"])
        self.assertEqual(doc_tender_events(retry, "attempt_failed"), [])
        self.assertNotIn("doc_tender.published", doc_tender_events(retry, "attempt_failed"))
        self.assertEqual(doc_tender_events(retry, "results_published"), ["doc_tender.published"])
        self.assertEqual(committed_events(retry, committed=True), ["doc_tender.retry_scheduled"])
        self.assertEqual(committed_events(retry, committed=False), [])

    def test_profile_edit_rejects_admin_fields(self):
        from jsonschema import Draft202012Validator

        operation = _operation(self.spec, "updateMe")
        self.assertEqual(_schema_name(operation["requestBody"]), "MeProfileWrite")
        rejected = operation["x-togavsu-access"]["rejected_fields"]
        for field in ("roles", "email", "linked_contact_id"):
            self.assertIn(field, rejected)
        schema = self.spec["components"]["schemas"]["MeProfileWrite"]
        for field in rejected:
            self.assertNotIn(field, schema["properties"])
        validator = Draft202012Validator(schema)
        validator.validate({"name": "Ada", "phone": "+15555550100", "initials": "AL"})
        for field, value in (
            ("roles", ["admin"]),
            ("email", "ada@example.com"),
            ("linked_contact_id", "018f1a00-7b3c-7d4e-8a11-111111111111"),
        ):
            with self.assertRaises(Exception):
                validator.validate({field: value})

    def test_file_writes_use_the_file_wall(self):
        for operation_id in (
            "renameFile",
            "moveFile",
            "markFileStatus",
            "trashFile",
            "restoreFile",
            "addFileVersion",
            "restoreFileVersion",
            "acquireEditLock",
            "renewEditLock",
            "releaseEditLock",
            "runEditor",
            "convertFile",
            "getFile",
            "downloadFileVersion",
        ):
            access = _operation(self.spec, operation_id)["x-togavsu-access"]
            self.assertEqual(access["access_inherits"], "file", operation_id)
            self.assertEqual(access["attorney_only_files"], "not_found", operation_id)
            denied = [case for case in access["access_cases"] if case["role"] == "staff" and case.get("attorney_only")]
            self.assertEqual(denied[0]["result"], "not_found", operation_id)

    def test_batches_and_context_tokens_reject_walled_matters(self):
        for operation_id in ("bulkCustomFieldValue", "bulkEvent"):
            access = _operation(self.spec, operation_id)["x-togavsu-access"]
            self.assertTrue(access["matter_wall"], operation_id)
            self.assertEqual(access["batch_authorization"], "all_or_nothing")
            self.assertEqual(access["inaccessible_result"], "not_found")
            self.assertIn("not_found", _operation(self.spec, operation_id)["x-togavsu-error-codes"])
        token = _operation(self.spec, "createModuleContextToken")
        self.assertTrue(token["x-togavsu-access"]["matter_wall"])
        self.assertEqual(token["x-togavsu-access"]["access_inherits"], "matter")
        self.assertIn("not_found", token["description"])

    def test_module_help_token_is_not_an_admin(self):
        for operation_id in ("createModuleHelpPage", "replaceModuleHelpPages", "deleteModuleHelpPage"):
            operation = _operation(self.spec, operation_id)
            self.assertEqual(operation["x-togavsu-access"]["roles"], [])
            principal = operation["x-togavsu-access"]["module_principal"]
            self.assertFalse(principal["grants_admin"])
            self.assertTrue(principal["own_heading_only"])
            self.assertEqual(principal["scope"], "help:write")
            self.assertEqual(operation["x-togavsu-access"]["scopes"], ["help:write"])
        firm = _operation(self.spec, "createHelpSection")
        self.assertEqual(firm["x-togavsu-access"]["roles"], ["admin"])
        self.assertNotEqual(firm["x-togavsu-access"].get("module_delegation"), "delegated_viewer")
        self.assertEqual(module_help_registration(firm, {"actor": "module", "module_id": "matter-clock", "path_module_id": "matter-clock", "scopes": ["help:write"]}), "forbidden")
        installed = decide_module_permissions(
            {"id": "matter-clock", "version": "0.1.0", "permissions": ["help:write"]},
            [{"scope": "help:write", "decision": "approve"}],
            signature_verified=True,
        )
        self.assertEqual(installed["token_scopes"], ["help:write"])
        omitted = decide_module_permissions(
            {"id": "matter-clock", "version": "0.1.0", "permissions": ["matters:read"]},
            [{"scope": "matters:read", "decision": "approve"}],
            signature_verified=True,
        )
        self.assertNotIn("help:write", omitted["token_scopes"])
        create = _operation(self.spec, "createModuleHelpPage")
        own = {"actor": "module", "module_id": "matter-clock", "path_module_id": "matter-clock", "scopes": installed["token_scopes"], "heading": "matter-clock"}
        self.assertEqual(module_help_registration(create, own), "allowed")
        self.assertEqual(module_help_registration(create, {**own, "path_module_id": "other-module"}), "forbidden")
        self.assertEqual(module_help_registration(create, {**own, "target": "firm"}), "forbidden")
        self.assertEqual(module_help_registration(create, {**own, "actor": "human", "human_session": True}), "forbidden")
        self.assertEqual(module_help_registration(create, {**own, "scopes": ["help:read"]}), "scope_denied")

    def test_module_update_keeps_the_old_version_on_failure(self):
        operation = _operation(self.spec, "updateModule")
        self.assertEqual(operation["x-togavsu-mcp"]["parity"], "required")
        content = operation["requestBody"]["content"]
        self.assertIn("application/json", content)
        self.assertIn("multipart/form-data", content)
        self.assertIn("version", self.spec["components"]["schemas"]["ModuleUpdate"]["properties"])
        self.assertEqual(operation["x-togavsu-access"]["on_failure"], "keep_previous_version")
        self.assertEqual(operation["x-togavsu-access"]["on_new_permissions"], "awaiting_approval_keep_running")
        self.assertIn("migration_refused", operation["x-togavsu-error-codes"])
        self.assertIn("previous_version", self.spec["components"]["schemas"]["ModuleUpdateResult"]["required"])

    def test_matter_archive_is_an_mcp_tool(self):
        operation = _operation(self.spec, "archiveMatter")
        self.assertEqual(operation["x-togavsu-mcp"]["parity"], "required")
        self.assertEqual(operation["x-togavsu-mcp"]["tool"], "archiveMatter")
        self.assertIn("archived", self.spec["components"]["schemas"]["Matter"]["properties"])
        self.assertIn("archived", self.spec["components"]["schemas"]["Matter"]["required"])

    def test_paused_timer_resumes_without_resetting_elapsed_time(self):
        resume = _operation(self.spec, "resumeTimer")
        self.assertIn("accumulated_seconds", resume["description"])
        self.assertIn("not reset", resume["description"])
        self.assertIn("accumulated_seconds", self.spec["components"]["schemas"]["Timer"]["required"])
        start = _operation(self.spec, "createTimer")
        self.assertIn("accumulated_seconds", start["description"])
        self.assertIn("resumes", start["description"])

    def test_file_links_and_conversion_result(self):
        link = self.spec["components"]["schemas"]["FileLink"]["properties"]["relationship"]
        self.assertEqual(set(link["enum"]), {"conversion_of", "grouped_photo_source", "attachment"})
        links = _operation(self.spec, "listFileLinks")
        self.assertEqual(links["x-togavsu-access"]["access_inherits"], "file")
        converted = _operation(self.spec, "convertFile")
        self.assertEqual(_schema_name(converted["responses"]["201"]), "ConversionResult")
        result = self.spec["components"]["schemas"]["ConversionResult"]["required"]
        self.assertEqual(result, ["file", "link"])

    def test_file_history_is_not_admin_only(self):
        operation = _operation(self.spec, "historyFile")
        self.assertIn("staff", operation["x-togavsu-access"]["roles"])
        self.assertNotEqual(operation["x-togavsu-access"]["roles"], ["admin"])
        self.assertEqual(operation["x-togavsu-access"]["scopes"], ["files:read"])
        self.assertEqual(operation["x-togavsu-mcp"]["parity"], "required")
        self.assertEqual(operation["x-togavsu-access"]["access_inherits"], "file")
        kind = self.spec["components"]["schemas"]["FileHistoryEntry"]["properties"]["kind"]
        self.assertEqual(set(kind["enum"]), {"field_edit", "version_save"})

    def test_bulk_endpoints_cover_record_types(self):
        required = {
            "contacts": "bulkContact",
            "matters": "bulkMatter",
            "matter-markers": "bulkMatterMarker",
            "replenishment-requests": "bulkTrustReplenishment",
            "trust-reconciliations": "bulkTrustReconciliation",
            "trust-ledger": "bulkTrustEntry",
            "notes": "bulkNote",
            "time-entries": "bulkTimeEntry",
            "expenses": "bulkExpense",
            "payments": "bulkPayment",
            "invoices": "bulkInvoice",
            "files": "bulkFile",
            "events": "bulkEvent",
            "calendar-events": "bulkCalendarEvent",
            "deadlines": "bulkDeadline",
            "fee-agreements": "bulkFeeAgreement",
            "matter-parties": "bulkMatterParty",
            "conflict-records": "bulkConflictRecord",
            "custom-field-values": "bulkCustomFieldValue",
        }
        for record_type, operation_id in required.items():
            operation = _operation(self.spec, operation_id)
            self.assertIn("bulk", operation["x-togavsu-features"], record_type)
            self.assertEqual(operation["x-togavsu-mcp"]["parity"], "required", record_type)
            self.assertTrue(operation["x-togavsu-mcp"]["tool"], record_type)
            text = operation["description"] + " " + " ".join(operation.get("x-togavsu-error-codes") or [])
            self.assertTrue("batch" in text or "whole" in text, record_type)

    def test_bulk_invoice_reconciles_or_writes_nothing(self):
        operation = _operation(self.spec, "bulkInvoice")
        self.assertEqual(operation["x-togavsu-mcp"]["parity"], "required")
        self.assertEqual(operation["x-togavsu-mcp"]["tool"], "bulkInvoice")
        self.assertEqual(operation["x-togavsu-events"], ["invoice.created"])
        self.assertEqual(operation["x-togavsu-access"]["reconciliation"], "exact_cents")
        self.assertEqual(operation["x-togavsu-access"]["batch_authorization"], "all_or_nothing")
        matter = "018f1a00-7b3c-7d4e-8a11-111111111111"
        item = {
            "matter_id": matter,
            "number": "2026-0001",
            "issued_on": "2026-11-01",
            "status": "draft",
            "lines": [{"kind": "flat_fee", "description": "Fee", "amount_cents": 10000}],
            "subtotal_cents": 10000,
            "adjustment_cents": -500,
            "interest_cents": 0,
            "total_cents": 9500,
            "amount_paid_cents": 0,
            "write_off_cents": 0,
            "balance_due_cents": 9500,
            "internal_notes": "staff only",
        }
        invoice_write = _component_validator(self.spec, "InvoiceWrite")
        invoice_write.validate(item)
        with self.assertRaises(Exception):
            invoice_write.validate({**item, "total_cents": "9500"})
        staff = {"role": "staff", "token_scopes": ["invoices:write"], "matter_walled": True, "on_wall": True, "matter_membership": {matter: True}}
        store = {"invoices": [], "events": [], "idempotency": {}}
        for channel in ("http", "mcp"):
            first = bulk_invoice_apply(store, operation, {**staff, "channel": channel, "idempotency_key": "inv-1", "items": [item]})
            self.assertEqual(first["result"], "allowed", channel)
            self.assertEqual(first["events"], ["invoice.created"])
            break
        self.assertEqual(len(store["invoices"]), 1)
        self.assertEqual(store["events"], ["invoice.created"])
        self.assertIn("internal_notes", first["data"][0])
        self.assertNotIn("internal_notes", project_invoice_for_role(operation, "client", first["data"][0]))
        replay = bulk_invoice_apply(store, operation, {**staff, "idempotency_key": "inv-1", "items": [item]})
        self.assertEqual(replay["result"], "original_result")
        self.assertEqual(replay["events"], [])
        self.assertEqual(len(store["invoices"]), 1)
        self.assertEqual(store["events"], ["invoice.created"])
        changed = {**item, "number": "2026-0002"}
        conflict = bulk_invoice_apply(store, operation, {**staff, "idempotency_key": "inv-1", "items": [changed]})
        self.assertEqual(conflict["result"], "idempotency_conflict")
        self.assertEqual(len(store["invoices"]), 1)
        bad = {**item, "number": "2026-0003", "total_cents": 9600}
        rejected = bulk_invoice_apply(store, operation, {**staff, "idempotency_key": "inv-2", "items": [item, bad]})
        self.assertEqual(rejected["result"], "batch_rejected")
        self.assertEqual(rejected["events"], [])
        self.assertEqual(len(store["invoices"]), 1)
        walled = bulk_invoice_apply(store, operation, {**staff, "on_wall": False, "matter_membership": {matter: False}, "idempotency_key": "inv-3", "items": [{**item, "matter_walled": True}]})
        self.assertEqual(walled["result"], "not_found")
        self.assertFalse(walled["written"])
        self.assertEqual(len(store["invoices"]), 1)
        other_matter = "018f1a00-7b3c-7d4e-8a11-222222222222"
        mixed_items = [item, {**item, "number": "2026-0008", "matter_id": other_matter}]
        mixed = bulk_invoice_apply(
            store,
            operation,
            {**staff, "on_wall": True, "matter_membership": {matter: True, other_matter: False}, "idempotency_key": "mixed-wall", "items": mixed_items},
        )
        self.assertEqual(mixed["result"], "not_found")
        self.assertFalse(mixed["written"])
        self.assertEqual(mixed["events"], [])
        self.assertEqual(len(store["invoices"]), 1)
        self.assertEqual(store["events"], ["invoice.created"])
        for role in ("accountant", "client"):
            denied = bulk_invoice_apply(store, operation, {"role": role, "token_scopes": ["invoices:write"], "items": [item]})
            self.assertEqual(denied["result"], "forbidden", role)
            self.assertFalse(denied["written"], role)
        self.assertEqual(len(store["invoices"]), 1)
        sent = {**item, "number": "2026-0004", "status": "sent"}
        paid = {**item, "number": "2026-0005", "status": "paid", "amount_paid_cents": 9500, "balance_due_cents": 0}
        voided = {**item, "number": "2026-0006", "status": "void"}
        written_off = {**item, "number": "2026-0007", "write_off_cents": 500, "balance_due_cents": 9000}
        for label, extra in (("sent", sent), ("paid", paid), ("void", voided), ("write-off", written_off)):
            denied = bulk_invoice_apply(store, operation, {**staff, "idempotency_key": label, "items": [extra]})
            self.assertEqual(denied["result"], "forbidden", label)
            self.assertFalse(denied["written"], label)
            self.assertEqual(denied["events"], [], label)
        mixed = bulk_invoice_apply(store, operation, {**staff, "idempotency_key": "mixed", "items": [item, sent]})
        self.assertEqual(mixed["result"], "forbidden")
        self.assertFalse(mixed["written"])
        self.assertEqual(len(store["invoices"]), 1)
        self.assertEqual(store["events"], ["invoice.created"])
        privileged = {"role": "attorney", "token_scopes": ["invoices:write", "invoices:send", "payments:write"], "matter_walled": True, "on_wall": True, "matter_membership": {matter: True}}
        for label, extra in (("sent", sent), ("paid", paid), ("void", voided), ("write-off", written_off)):
            allowed = bulk_invoice_apply(store, operation, {**privileged, "idempotency_key": "attorney-" + label, "items": [extra]})
            self.assertEqual(allowed["result"], "allowed", label)
            self.assertEqual(allowed["events"], ["invoice.created"], label)
        self.assertEqual(len(store["invoices"]), 5)
        missing_send = bulk_invoice_apply(
            store,
            operation,
            {"role": "attorney", "token_scopes": ["invoices:write"], "matter_walled": True, "on_wall": True, "idempotency_key": "no-send", "items": [sent]},
        )
        self.assertEqual(missing_send["result"], "scope_denied")
        self.assertFalse(missing_send["written"])
        self.assertEqual(len(store["invoices"]), 5)
        self.assertEqual(bulk_privilege_expansions(self.spec), [])
        for key, rule in operation["x-togavsu-access"]["item_privileges"].items():
            individual = _operation(self.spec, rule["operation"])
            self.assertEqual(rule["roles"], individual["x-togavsu-access"]["roles"], key)
            self.assertEqual(rule["scopes"], individual["x-togavsu-access"]["scopes"], key)

    def test_expense_and_payment_includes(self):
        for operation_id, schema_name in (
            ("listExpense", "Expense"),
            ("getExpense", "Expense"),
            ("listPayment", "Payment"),
            ("getPayment", "Payment"),
        ):
            operation = _operation(self.spec, operation_id)
            names = [_param_name(parameter) for parameter in operation["parameters"]]
            self.assertIn("include", names, operation_id)
            self.assertTrue(operation["x-togavsu-access"]["matter_wall"], operation_id)
            self.assertEqual(
                operation["x-togavsu-access"]["include_scope_pattern"],
                "views:{module_id}.{view}:read",
            )
            self.assertIn("included", self.spec["components"]["schemas"][schema_name]["properties"])

    def test_trust_balance_rows_name_their_owners(self):
        from jsonschema import Draft202012Validator

        row = self.spec["components"]["schemas"]["TrustBalanceRow"]
        validator = Draft202012Validator(
            {
                "$schema": "https://json-schema.org/draft/2020-12/schema",
                "$defs": {"Uuid": self.spec["components"]["schemas"]["Uuid"]},
                **row,
            }
        )
        # Uuid refs inside oneOf still point at components. Validate the required keys structurally.
        matter = next(branch for branch in row["oneOf"] if branch["properties"]["scope"]["const"] == "matter")
        client = next(branch for branch in row["oneOf"] if branch["properties"]["scope"]["const"] == "client")
        account = next(branch for branch in row["oneOf"] if branch["properties"]["scope"]["const"] == "account")
        self.assertIn("client_id", matter["required"])
        self.assertIn("matter_id", matter["required"])
        self.assertIn("trust_account_id", matter["required"])
        self.assertIn("client_id", client["required"])
        self.assertNotIn("matter_id", client["required"])
        self.assertIn("trust_account_id", account["required"])
        self.assertEqual(_schema_name_from_page(_operation(self.spec, "getTrustBalances")), "TrustBalanceRow")
        self.assertIsNotNone(validator)

    def test_mcp_arguments_use_path_and_query_objects(self):
        import json
        from pathlib import Path

        matter = _operation(self.spec, "getMatter")["x-togavsu-mcp"]["transfer"]
        self.assertEqual(matter["arguments"], ["path", "query", "context_token"])
        self.assertNotIn("matter_id", matter["arguments"])
        self.assertEqual(matter["credentials"]["context_token"]["header"], "X-Togavsu-Context-Token")
        self.assertFalse(matter["credentials"]["context_token"]["roles_from_arguments"])
        self.assertEqual(matter["forbidden_header_overrides"], ["Authorization", "Host"])
        chunk = _operation(self.spec, "putFileChunk")["x-togavsu-mcp"]["transfer"]["arguments"]
        self.assertEqual(chunk[:2], ["path", "query"])
        self.assertIn("headers", chunk)
        self.assertIn("body_base64", chunk)
        self.assertNotIn("upload_id", chunk)
        text = Path("docs/api/mcp.md").read_text(encoding="utf-8")
        blocks = [json.loads(part.split("```", 1)[0]) for part in text.split("```json\n")[1:]]
        get_args = blocks[0]["params"]["arguments"]
        put_args = blocks[1]["params"]["arguments"]
        self.assertEqual(set(get_args), {"path", "query", "context_token"})
        self.assertIn("matter_id", get_args["path"])
        self.assertIn("include", get_args["query"])
        self.assertEqual(set(put_args), set(chunk))
        self.assertIn("upload_id", put_args["path"])
        self.assertIn("Content-Range", put_args["headers"])

    def test_marketplace_flag_requires_a_note(self):
        operation = self.market["paths"]["/v1/operator/listings/{listing_id}/flag"]["post"]
        schema = operation["requestBody"]["content"]["application/json"]["schema"]
        self.assertEqual(schema["$ref"], "#/components/schemas/FlagRequest")
        flag = self.market["components"]["schemas"]["FlagRequest"]
        self.assertEqual(flag["required"], ["note"])
        self.assertIn("version", flag["properties"])
        self.assertNotIn("version", flag["required"])

    def test_ical_feeds_are_owner_bound_and_wall_filtered(self):
        for operation_id in ("listIcalFeed", "getIcalFeed", "createIcalFeed", "revokeIcalFeed", "downloadIcal"):
            access = _operation(self.spec, operation_id)["x-togavsu-access"]
            self.assertTrue(access["matter_wall"], operation_id)
            self.assertEqual(access["feed_owner"], "self_or_admin", operation_id)
            self.assertEqual(access["download_filter"], "omit_events_on_walled_matters", operation_id)
            cases = {(case.get("action"), case.get("role"), case.get("owner"), case.get("result")) for case in access["access_cases"]}
            self.assertIn(("read", "staff", "other_user", "not_found"), cases)
            self.assertIn(("create", "staff", "firm", "forbidden"), cases)
            self.assertIn(("create", "admin", "firm", "allowed"), cases)
        download = _operation(self.spec, "downloadIcal")
        omitted = [case for case in download["x-togavsu-access"]["access_cases"] if case.get("event_on_walled_matter")]
        self.assertEqual(omitted[0]["result"], "omitted")

    def test_client_invoice_hides_internal_notes_and_history(self):
        from jsonschema import Draft202012Validator

        client = self.spec["components"]["schemas"]["InvoiceClient"]
        self.assertNotIn("internal_notes", client["properties"])
        bundled = {
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "$defs": {"InvoiceLine": self.spec["components"]["schemas"]["InvoiceLine"]},
            **{key: value for key, value in client.items() if key != "properties"},
            "properties": {
                name: (
                    {"type": "array", "items": {"$ref": "#/$defs/InvoiceLine"}}
                    if name == "lines"
                    else value
                )
                for name, value in client["properties"].items()
            },
        }
        Draft202012Validator(bundled).validate(
            {
                "id": "018f1a00-7b3c-7d4e-8a11-111111111111",
                "matter_id": "018f1a00-7b3c-7d4e-8a11-222222222222",
                "number": "1",
                "issued_on": "2026-11-01",
                "status": "sent",
                "lines": [{"kind": "flat_fee", "description": "Fee", "amount_cents": 10000}],
                "subtotal_cents": 10000,
                "adjustment_cents": 0,
                "interest_cents": 0,
                "total_cents": 10000,
                "amount_paid_cents": 0,
                "balance_due_cents": 10000,
            }
        )
        with self.assertRaises(Exception):
            Draft202012Validator(bundled).validate({"internal_notes": "do not share"})
        history = self.spec["components"]["schemas"]["InvoiceClientHistoryEntry"]
        self.assertNotIn("old_value", history["properties"])
        self.assertNotIn("new_value", history["properties"])
        self.assertNotIn("internal_notes", history["properties"]["field"]["enum"])
        allow = list(client["properties"])
        staff_invoice = {
            "id": "018f1a00-7b3c-7d4e-8a11-111111111111",
            "created_at": "2026-11-01T00:00:00Z",
            "created_by": "018f1a00-7b3c-7d4e-8a11-333333333333",
            "updated_at": "2026-11-02T00:00:00Z",
            "matter_id": "018f1a00-7b3c-7d4e-8a11-222222222222",
            "number": "1",
            "issued_on": "2026-11-01",
            "status": "sent",
            "lines": [{"kind": "flat_fee", "description": "Fee", "amount_cents": 10000}],
            "subtotal_cents": 10000,
            "adjustment_cents": -100,
            "adjustment_reason": "courtesy",
            "interest_cents": 0,
            "total_cents": 9900,
            "amount_paid_cents": 0,
            "balance_due_cents": 9400,
            "write_off_cents": 500,
            "write_off_reason": "hardship",
            "void_reason": "entered twice",
            "dispute_status": "open",
            "dispute_notes": "client called",
            "sent_at": "2026-11-01T12:00:00Z",
            "paid_at": None,
            "client_message": "Thank you",
            "internal_notes": "do not share",
            "future_staff_field": "not for the client",
        }
        for operation_id in ("listInvoice", "getInvoice"):
            operation = _operation(self.spec, operation_id)
            self.assertEqual(operation["x-togavsu-access"]["client_projection"], "allowlist", operation_id)
            self.assertEqual(operation["x-togavsu-access"]["client_fields"], allow, operation_id)
            self.assertNotIn("client_omitted_fields", operation["x-togavsu-access"], operation_id)
            projected = project_invoice_for_role(operation, "client", staff_invoice)
            self.assertEqual(set(projected), set(allow) & set(staff_invoice), operation_id)
            self.assertEqual(projected["client_message"], "Thank you", operation_id)
            self.assertEqual(projected["adjustment_cents"], -100, operation_id)
            self.assertEqual(projected["number"], "1", operation_id)
            for hidden in (
                "adjustment_reason",
                "write_off_cents",
                "write_off_reason",
                "void_reason",
                "dispute_status",
                "dispute_notes",
                "internal_notes",
                "future_staff_field",
                "created_at",
            ):
                self.assertNotIn(hidden, projected, hidden)
            recorded = project_record(operation, {"role": "client", "on_wall": True}, staff_invoice)
            self.assertEqual(recorded["record"], projected, operation_id)
        listed = _operation(self.spec, "listInvoice")
        self.assertEqual(listed["x-togavsu-access"]["client_csv_columns"], allow)
        self.assertNotIn("client_csv_omitted_columns", listed["x-togavsu-access"])
        self.assertEqual(next(tool for tool in __import__("contract.build", fromlist=["render_mcp"]).render_mcp(self.spec)["tools"] if tool["name"] == "listInvoice")["name"], "listInvoice")
        history_operation = _operation(self.spec, "historyInvoice")
        history_allow = list(history["properties"])
        self.assertEqual(history_operation["x-togavsu-access"]["client_fields"], history_allow)
        history_row = {
            "at": "2026-11-01T00:00:00Z",
            "actor_kind": "staff",
            "field": "status",
            "old_value": "draft",
            "new_value": "sent",
            "reason": "approved",
            "internal_notes": "do not share",
            "future_staff_field": "hidden",
        }
        history_projected = project_record(history_operation, {"role": "client", "on_wall": True}, history_row)
        self.assertEqual(set(history_projected["record"]), {"at", "actor_kind", "field"})
        self.assertNotIn("old_value", history_projected["record"])

    def test_complete_marketplace_listing_validates(self):
        import copy

        from jsonschema import Draft202012Validator

        listing = {
            "id": "matter-clock",
            "name": "Matter Clock",
            "author": "Example",
            "signing_key": "ed25519:abc",
            "version": "0.1.0",
            "permissions": ["matters:read"],
            "version_history": [{"version": "0.1.0", "released_at": "2026-11-01T00:00:00Z"}],
            "price_note": "",
            "flagged": False,
            "pulled": False,
            "notice": "Modules are published by their authors.",
        }
        core = copy.deepcopy(self.spec["components"]["schemas"])

        def rewrite(node):
            if isinstance(node, dict):
                ref = node.get("$ref")
                if isinstance(ref, str) and ref.startswith("#/components/schemas/"):
                    node["$ref"] = "#/$defs/" + ref.rsplit("/", 1)[1]
                for value in node.values():
                    rewrite(value)
            elif isinstance(node, list):
                for value in node:
                    rewrite(value)

        rewrite(core)
        Draft202012Validator(
            {"$schema": "https://json-schema.org/draft/2020-12/schema", "$defs": core, "$ref": "#/$defs/MarketplaceListing"}
        ).validate(listing)
        Draft202012Validator(self.market["components"]["schemas"]["Listing"]).validate(listing)
        for field in ("signing_key", "permissions", "version_history", "price_note"):
            self.assertIn(field, self.spec["components"]["schemas"]["MarketplaceListing"]["required"])
            self.assertIn(field, self.market["components"]["schemas"]["Listing"]["required"])

    def test_archive_quarantine_requires_confirmation(self):
        operation = _operation(self.spec, "createFileArchive")
        self.assertIn("confirm_quarantine", self.spec["components"]["schemas"]["FileArchiveRequest"]["properties"])
        self.assertIn("quarantine_confirmation_required", operation["x-togavsu-error-codes"])
        cases = {(case["confirm_quarantine"], case["result"]) for case in operation["x-togavsu-access"]["archive_cases"]}
        self.assertIn((False, "quarantine_confirmation_required"), cases)
        self.assertIn((True, "allowed"), cases)

    def test_citation_requires_a_location(self):
        from jsonschema import Draft202012Validator

        citation = self.spec["components"]["schemas"]["Citation"]
        validator = Draft202012Validator(citation)
        validator.validate({"page": 4})
        validator.validate({"timestamp": "00:01:30"})
        validator.validate({"page": 4, "timestamp": "00:01:30"})
        for invalid in ({}, {"page": None, "timestamp": None}, {"page": None}, {"timestamp": None}):
            with self.assertRaises(Exception):
                validator.validate(invalid)

    def test_ai_call_log_includes_cost(self):
        from jsonschema import Draft202012Validator

        schemas = self.spec["components"]["schemas"]
        call = schemas["AiCall"]
        self.assertIn("cost_micro_usd", call["properties"])
        self.assertIn("cost_micro_usd", call["required"])
        self.assertEqual(call["properties"]["cost_micro_usd"]["type"], "integer")
        attempt = schemas["AiCallAttempt"]
        self.assertIn("cost_micro_usd", attempt["required"])
        document = {
            "$schema": "https://json-schema.org/draft/2020-12/schema",
            "$defs": {"AiCallAttempt": attempt},
            **{key: value for key, value in call.items() if key != "properties"},
            "properties": {
                name: ({"$ref": "#/$defs/AiCallAttempt"} if value.get("$ref", "").endswith("AiCallAttempt") else value)
                if name != "attempts"
                else {"type": "array", "items": {"$ref": "#/$defs/AiCallAttempt"}}
                for name, value in call["properties"].items()
            },
        }
        row = {
            "id": "018f1a00-7b3c-7d4e-8a11-111111111111",
            "created_at": "2026-11-07T12:00:00Z",
            "created_by": "018f1a00-7b3c-7d4e-8a11-222222222222",
            "task": "labeling",
            "provider": "openai",
            "model": "gpt-6.1-sol",
            "tokens_in": 20,
            "tokens_out": 20,
            "cost_micro_usd": 6000,
            "fell_back": True,
            "attempts": [
                {"provider": "openai", "model": "gpt-6.1-sol", "status": "timeout", "tokens_in": 10, "tokens_out": 0, "cost_micro_usd": 1000},
                {"provider": "anthropic", "model": "strong", "status": "ok", "tokens_in": 10, "tokens_out": 20, "cost_micro_usd": 5000},
            ],
            "requester": "matter-clock",
        }
        Draft202012Validator(document).validate(row)
        operation = _operation(self.spec, "createAiCall")
        self.assertEqual(operation["x-togavsu-mcp"]["tool"], "createAiCall")
        self.assertEqual(operation["x-togavsu-access"]["external_effects"], ["provider_invocation", "log_write"])
        self.assertEqual(operation["x-togavsu-access"]["authorize_before"], "external_effect")
        matter = "018f1a00-7b3c-7d4e-8a11-111111111111"
        allowed_call = {"role": "staff", "token_scopes": ["ai:use"], "matter_id": matter, "matter_walled": True, "on_wall": True}
        for channel in ("http", "mcp"):
            allowed = external_effect_decision(operation, {**allowed_call, "channel": channel})
            self.assertEqual(allowed["result"], "allowed", channel)
            self.assertTrue(allowed["provider_invoked"], channel)
            self.assertTrue(allowed["log_written"], channel)
            denied = external_effect_decision(operation, {**allowed_call, "channel": channel, "on_wall": False, "role": "admin"})
            self.assertEqual(denied["result"], "not_found", channel)
            self.assertFalse(denied["provider_invoked"], channel)
            self.assertFalse(denied["log_written"], channel)
            self.assertFalse(denied["mutated"], channel)
        firmwide = external_effect_decision(operation, {"role": "staff", "token_scopes": ["ai:use"]})
        self.assertEqual(firmwide["result"], "allowed")
        self.assertTrue(firmwide["provider_invoked"])
        move = _operation(self.spec, "createStorageMove")
        copied = external_effect_decision(
            move,
            {"role": "admin", "token_scopes": ["files:write"], "scope": "matter", "matter_id": matter, "matter_walled": True, "on_wall": False},
        )
        self.assertEqual(copied["result"], "not_found")
        self.assertEqual(copied["effects"]["byte_copy"], False)
        self.assertFalse(copied["mutated"])
        confirm = _operation(self.spec, "confirmStorageMove")
        deleted = external_effect_decision(
            confirm,
            {"role": "admin", "token_scopes": ["files:write"], "matter_id": matter, "matter_walled": True, "on_wall": False},
        )
        self.assertEqual(deleted["result"], "not_found")
        self.assertFalse(deleted["effects"]["byte_delete"])
        missing = []
        for method, path, candidate in _ops(self.spec):
            if method == "get" or candidate.get("x-togavsu-writes") is False:
                continue
            if not operation_names_matter(self.spec, path, candidate):
                continue
            access = candidate["x-togavsu-access"]
            if not access.get("matter_wall") or access.get("admin_bypasses_wall") or access.get("effect_order") != "authorize_before_external_effect":
                missing.append(candidate["operationId"])
        self.assertEqual(missing, [])
        incomplete = dict(row)
        del incomplete["cost_micro_usd"]
        with self.assertRaises(Exception):
            Draft202012Validator(document).validate(incomplete)

    def test_module_delegation_does_not_escalate_the_viewer(self):
        policy = self.spec["x-togavsu-module-delegation"]
        self.assertFalse(policy["scope_bypasses_wall"])
        self.assertFalse(policy["scope_bypasses_viewer_role"])
        self.assertFalse(policy["grants_admin"])
        self.assertEqual(policy["context_header"], "X-Togavsu-Context-Token")
        for operation_id in (
            "getMatter",
            "listMatter",
            "updateFileMetadata",
            "createNote",
            "createTimeEntry",
            "runEditor",
            "postTrustEntry",
        ):
            self.assertEqual(
                _operation(self.spec, operation_id)["x-togavsu-access"]["module_delegation"],
                "delegated_viewer",
                operation_id,
            )
        background = set(policy["noninteractive"]["operations"])
        self.assertFalse(policy["noninteractive"]["viewer_context_required"])
        self.assertFalse(policy["noninteractive"]["grants_admin"])
        self.assertFalse(policy["noninteractive"]["firmwide_bypass"])
        self.assertTrue(policy["noninteractive"]["attorney_only_redaction"])
        for operation_id in background:
            self.assertEqual(
                _operation(self.spec, operation_id)["x-togavsu-access"]["module_delegation"],
                "delegated_viewer_or_noninteractive",
                operation_id,
            )
        for case in policy["noninteractive_cases"]:
            self.assertEqual(_background_result(case), case["result"], case)
        for operation_id in ("updateSetting", "listAuditLog", "createUser", "listApiToken", "updateMe", "requestEmailChange"):
            self.assertNotEqual(
                _operation(self.spec, operation_id)["x-togavsu-access"].get("module_delegation"),
                "delegated_viewer",
                operation_id,
            )
        for case in policy["cases"]:
            self.assertEqual(_delegation_result(case), case["result"], case)

    def test_malware_scan_uses_the_event_grant_for_the_whole_sequence(self):
        policy = self.spec["x-togavsu-module-delegation"]["noninteractive"]["malware_scan"]
        webhook = self.spec["webhooks"]["moduleEvent"]["post"]
        self.assertEqual(webhook["x-togavsu-task-delivery"], policy)
        self.assertEqual(policy["sequence"], ["getFile", "listFileVersions", "downloadFileVersion", "quarantineFileVersion"])
        self.assertFalse(policy["viewer_context_required"])
        self.assertFalse(policy["grants_admin"])
        self.assertFalse(policy["firmwide_bypass"])
        self.assertIn("X-Togavsu-Task-Authorization", webhook["description"])
        self.assertIn("revoked", webhook["description"])
        self.assertIn("installed module", webhook["description"])
        header_names = [item["name"] for item in webhook["parameters"]]
        self.assertIn("X-Togavsu-Task-Authorization", header_names)
        grant_schema = self.spec["components"]["schemas"]["TaskAuthorization"]
        for field in ("module_id", "event_id", "file_id", "version_id", "allowed_operations", "allowed_fields", "expires_at", "revoked"):
            self.assertIn(field, grant_schema["required"], field)
        event = self.spec["components"]["schemas"]["WebhookEvent"]
        self.assertIn("task_authorization", event["properties"])
        module = {
            "registered": True,
            "subscriptions": ["file.version_created"],
            "module_id": "malware",
            "recipient_id": "malware",
            "file_id": "file-1",
            "version_id": "ver-1",
            "matter_id": "matter-1",
        }
        grant = _issue_malware_grant(policy, module)
        self.assertIsNotNone(grant)
        self.assertIsNone(_issue_malware_grant(policy, {**module, "registered": False}))
        self.assertIsNone(_issue_malware_grant(policy, {**module, "subscriptions": ["invoice.sent"]}))
        self.assertIsNone(_issue_malware_grant(policy, {**module, "recipient_id": "other-module"}))
        base = {
            "registered": True,
            "subscriptions": ["file.version_created"],
            "caller_module_id": "malware",
            "module_scopes": ["files:read", "files:write"],
            "grant": grant,
            "matter_id": "matter-1",
            "file_id": "file-1",
            "version_id": "ver-1",
            "viewer_present": False,
        }
        steps = [
            ("getFile", "name"),
            ("listFileVersions", "sha256"),
            ("downloadFileVersion", None),
            ("quarantineFileVersion", "quarantined"),
        ]
        for operation_id, field in steps:
            operation = _operation(self.spec, operation_id)
            self.assertEqual(operation["x-togavsu-access"]["module_delegation"], "delegated_viewer_or_noninteractive", operation_id)
            self.assertIn(operation["x-togavsu-access"]["scopes"][0], ("files:read", "files:write"), operation_id)
            call = {**base, "operation": operation_id, "field": field}
            self.assertEqual(_malware_call(policy, operation, call), "allowed", operation_id)
            self.assertFalse(call["viewer_present"])
        denied = [
            ({"module_scopes": ["files:write"]}, "getFile", "name", "scope_denied"),
            ({"module_scopes": ["files:read"]}, "quarantineFileVersion", "quarantined", "scope_denied"),
            ({"grant": {**grant, "expired": True}}, "downloadFileVersion", None, "forbidden"),
            ({"grant": {**grant, "revoked": True}}, "downloadFileVersion", None, "forbidden"),
            ({"grant": {**grant, "invalid": True}}, "getFile", "name", "forbidden"),
            ({"grant": None}, "downloadFileVersion", None, "forbidden"),
            ({"token_revoked": True}, "quarantineFileVersion", "quarantined", "unauthenticated"),
            ({"caller_module_id": "other-module"}, "getFile", "name", "forbidden"),
            ({"matter_id": "matter-2"}, "getFile", "name", "not_found"),
            ({"file_id": "file-2"}, "listFileVersions", "sha256", "not_found"),
            ({"version_id": "ver-2"}, "downloadFileVersion", None, "not_found"),
            ({"field": "note"}, "getFile", "note", "not_found"),
            ({"field": "folder_id"}, "getFile", "folder_id", "field_locked"),
            ({"operation": "listFile"}, "listFile", None, "forbidden"),
        ]
        for patch, operation_id, field, expected in denied:
            operation = _operation(self.spec, operation_id)
            call = {**base, "operation": operation_id, "field": field, **patch}
            self.assertEqual(_malware_call(policy, operation, call), expected, patch)

    def test_file_byte_downloads_are_audited_and_highlighted(self):
        found = list(_file_byte_downloads(self.spec))
        names = [operation["operationId"] for _method, _path, operation in found]
        self.assertIn("downloadFileVersion", names)
        self.assertIn("downloadFileRendition", names)
        self.assertIn("createFileArchive", names)
        self.assertIn("getInvoicePdf", names)
        self.assertGreaterEqual(len(found), 4)
        outside_files = [path for _method, path, _operation in found if not (path.startswith("/api/v1/files") or path.startswith("/api/v1/file-archives"))]
        self.assertTrue(outside_files, names)
        for operation_id in ("downloadBackup", "getHelpGuidePdf", "getHelpGuide", "getLlmsTxt", "putFileChunk"):
            self.assertNotIn(operation_id, names, operation_id)
        calls = [
            ({"role": "client", "shared": True, "attorney_only": False, "on_wall": True}, "highlighted"),
            ({"role": "staff", "shared": True, "attorney_only": False, "on_wall": True}, "audited"),
            ({"role": "client", "shared": False, "attorney_only": False, "on_wall": True}, "not_found"),
            ({"role": "staff", "shared": True, "attorney_only": True, "on_wall": True}, "not_found"),
            ({"role": "attorney", "shared": True, "attorney_only": True, "on_wall": True}, "audited"),
            ({"role": "staff", "shared": True, "attorney_only": False, "matter_walled": True, "on_wall": False}, "not_found"),
        ]
        for _method, path, operation in found:
            confirm = operation["x-togavsu-access"].get("quarantine_confirmation_input")
            self.assertIsNotNone(confirm, operation["operationId"])
            self.assertIn(confirm["name"], ("confirm", "confirm_quarantine"), operation["operationId"])
            if confirm["in"] == "query":
                names = [parameter.get("name") for parameter in operation.get("parameters", [])]
                self.assertIn(confirm["name"], names, (operation["operationId"], path))
            for call, expected in calls:
                self.assertEqual(_download_outcome(operation, call), expected, (operation["operationId"], path, call["role"]))

    def test_accountant_reads_only_stored_receipts_and_invoice_pdfs(self):
        self.assertIn("financial_classification", self.spec["components"]["schemas"]["File"]["properties"])
        self.assertNotIn("financial_classification", self.spec["components"]["schemas"]["FileMetadataWrite"]["properties"])
        file_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1"
        matter = "018f1a00-7b3c-7d4e-8a11-333333333333"
        stamp = "2026-11-07T18:00:00Z"
        actor = "018f1a00-7b3c-7d4e-8a11-555555555555"

        def stored(classification, **extra):
            row = {
                "id": file_id,
                "created_at": stamp,
                "updated_at": stamp,
                "created_by": actor,
                "name": "receipt.pdf",
                "extension": "pdf",
                "origin": "firm",
                "attorney_only": False,
                "shared_with_client": False,
                "source": "upload",
                "locked_fields": [],
                "warnings": [],
                "financial_classification": classification,
                "matter_id": matter,
            }
            row.update(extra)
            return row

        receipt = stored("receipt")
        invoice = stored("invoice_pdf", id="018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa2", name="invoice.pdf")
        ordinary = stored(None, id="018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa3", name="pleading.pdf")
        absent = {key: value for key, value in stored(None, id="018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa4").items() if key != "financial_classification"}
        for row in (receipt, invoice, ordinary, absent):
            _component_validator(self.spec, "File").validate(row)
        download = _operation(self.spec, "downloadFileVersion")
        get_file = _operation(self.spec, "getFile")
        invoice_pdf = _operation(self.spec, "getInvoicePdf")
        self.assertEqual(download["x-togavsu-access"]["accountant"], "receipts-and-invoice-pdfs")

        def http(operation, record, **extra):
            call = {
                "role": "accountant",
                "token_scopes": ["files:read"] if operation["operationId"] != "getInvoicePdf" else ["invoices:read"],
                "file_id": None if record is None else record.get("id"),
                "stored_file": record,
                "matter_walled": False,
                "attorney_only": False,
                "on_wall": True,
                "confirm": True,
                "quarantined": False,
                "shared": True,
                **extra,
            }
            return file_download_decision(operation, call), operation_authorization(self.spec, operation, call)

        for record in (receipt, invoice):
            download_result, auth = http(download, record)
            self.assertEqual(download_result, "audited", record["financial_classification"])
            self.assertEqual(auth["result"], "allowed", record["financial_classification"])
            self.assertTrue(auth["effects"], record["financial_classification"])
            self.assertEqual(http(get_file, record)[1]["result"], "allowed", record["id"])
        for label, record in (("ordinary", ordinary), ("absent", absent), ("missing", None)):
            download_result, auth = http(download, record, file_id=file_id)
            self.assertEqual(download_result, "not_found", label)
            self.assertEqual(auth["result"], "not_found", label)
            self.assertFalse(auth["effects"], label)
        staff_download, staff_auth = http(download, ordinary, role="staff")
        self.assertEqual(staff_download, "audited")
        self.assertEqual(staff_auth["result"], "allowed")
        self.assertTrue(staff_auth["effects"])
        forged = {
            "role": "accountant",
            "token_scopes": ["files:read"],
            "file_id": ordinary["id"],
            "stored_file": ordinary,
            "financial_classification": "receipt",
            "matter_walled": False,
            "confirm": True,
            "quarantined": False,
        }
        self.assertEqual(file_download_decision(download, forged), "not_found")
        self.assertFalse(operation_authorization(self.spec, download, forged)["effects"])
        walled = {**receipt, "matter_walled": True}
        wall_call = {
            "role": "accountant",
            "token_scopes": ["files:read"],
            "file_id": receipt["id"],
            "stored_file": walled,
            "matter_membership": {matter: False},
            "confirm": True,
            "quarantined": False,
        }
        self.assertEqual(file_download_decision(download, wall_call), "not_found")
        self.assertEqual(operation_authorization(self.spec, get_file, wall_call)["result"], "not_found")
        hidden = {**receipt, "attorney_only": True}
        hidden_call = {
            "role": "accountant",
            "token_scopes": ["files:read"],
            "file_id": receipt["id"],
            "stored_file": hidden,
            "attorney_only": False,
            "matter_walled": False,
            "confirm": True,
            "quarantined": False,
        }
        self.assertEqual(operation_authorization(self.spec, get_file, hidden_call)["result"], "not_found")
        self.assertFalse(operation_authorization(self.spec, get_file, hidden_call)["effects"])
        invoice_result, invoice_auth = http(invoice_pdf, None)
        self.assertEqual(invoice_result, "audited")
        self.assertEqual(invoice_auth["result"], "allowed")
        self.assertTrue(invoice_auth["effects"])
        visible = accountant_file_rows(
            {"role": "accountant", "matter_membership": {matter: False}},
            [receipt, invoice, ordinary, absent, hidden, walled],
        )
        self.assertEqual([row["id"] for row in visible], [receipt["id"], invoice["id"]])
        self.assertEqual([row["id"] for row in accountant_file_rows({"role": "staff"}, [ordinary, receipt])], [ordinary["id"], receipt["id"]])

        def opened(tool, record, **extra):
            return mcp_tool_dispatch(
                self.spec,
                {
                    "tool": tool,
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "role": extra.pop("role", "accountant"),
                    "token_scopes": extra.pop("token_scopes", ["files:read"]),
                    "stored_file": record,
                    "matter_walled": False,
                    "confirm": True,
                    "arguments": {
                        "path": {
                            "file_id": file_id if record is None else record["id"],
                            "version_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa9",
                        },
                        "body": {"financial_classification": "receipt"},
                        "query": {"financial_classification": "invoice_pdf"},
                    },
                    **extra,
                },
            )

        allowed_download = opened("downloadFileVersion", receipt)
        self.assertEqual(allowed_download["result"], "allowed")
        self.assertTrue(allowed_download["effects"])
        ordinary_read = opened("getFile", ordinary)
        self.assertEqual(ordinary_read["result"], "not_found")
        self.assertFalse(ordinary_read["effects"])
        self.assertEqual(opened("getFile", None)["result"], "not_found")
        self.assertFalse(opened("getFile", None)["effects"])
        staff_read = opened("getFile", ordinary, role="staff")
        self.assertEqual(staff_read["result"], "allowed")
        self.assertTrue(staff_read["effects"])
        pdf = opened("getInvoicePdf", None, token_scopes=["invoices:read"], matter_id=matter, matter_walled=False)
        self.assertEqual(pdf["result"], "allowed")
        self.assertTrue(pdf["effects"])

    def test_client_context_token_is_own_matter_only(self):
        operation = _operation(self.spec, "createModuleContextToken")
        access = operation["x-togavsu-access"]
        self.assertIn("client", access["roles"])
        self.assertEqual(access["client"], "own_matter")
        policy = access["client_context"]
        self.assertTrue(policy["own_matters_only"])
        self.assertEqual(policy["impersonation"], "forbidden")
        self.assertFalse(policy["firm_roles_unless_held"])
        self.assertEqual(policy["client_session_authorized_by"], "role")
        self.assertTrue(policy["scope_does_not_bypass_matter"])
        sequence = policy["sequence"]
        self.assertEqual(
            [step["step"] for step in sequence],
            [
                "obtain_context",
                "init_upload",
                "chunk",
                "resume_status",
                "resumed_chunk",
                "complete",
                "publish",
                "attachment",
            ],
        )
        allowed = [step["operation"] for step in sequence if step["step"] != "obtain_context"]
        for operation_id in dict.fromkeys(allowed):
            self.assertIn(operation_id, policy["client_allowed_operations"], operation_id)
        for operation_id in (
            "listFile",
            "getFile",
            "listFileVersions",
            "downloadFileVersion",
            "downloadFileRendition",
            "createFileArchive",
            "listInvoice",
            "getInvoice",
            "getInvoicePdf",
            "historyInvoice",
            "getFileUpload",
        ):
            self.assertIn(operation_id, policy["client_allowed_operations"], operation_id)
        for operation_id in ("updateMe", "createUser", "postTrustEntry", "reversePayment", "listAuditLog", "updateSetting"):
            self.assertNotIn(operation_id, policy["client_allowed_operations"], operation_id)
        covered = set()
        for _method, _path, operation in _ops(self.spec):
            features = operation["x-togavsu-features"]
            roles = operation["x-togavsu-access"]["roles"]
            if "file-uploads" in features and "client" in roles:
                covered.add(operation["operationId"])
                self.assertIn(operation["operationId"], policy["client_allowed_operations"], operation["operationId"])
        self.assertIn("getFileUpload", covered)
        own = {"role": "client", "matter": "own_shared", "owner": "self", "from_visible": True, "to_visible": True, "same_matter": True, "relationship": "attachment"}
        for step in sequence:
            self.assertEqual(_upload_step_result(self.spec, policy, step, own), "allowed", step)
            if step["step"] == "obtain_context":
                self.assertEqual(
                    _upload_step_result(self.spec, policy, step, {**own, "matter": "other_client"}),
                    "not_found",
                    step,
                )
                self.assertEqual(
                    _upload_step_result(self.spec, policy, step, {**own, "requested_roles": ["admin"]}),
                    "forbidden",
                    step,
                )
                self.assertEqual(
                    _upload_step_result(self.spec, policy, step, {**own, "impersonate": True}),
                    "forbidden",
                    step,
                )
                continue
            self.assertTrue(operation_denies_other_client(_operation(self.spec, step["operation"])), step)
            self.assertEqual(
                _upload_step_result(self.spec, policy, step, {**own, "matter": "other_client", "owner": "other_client", "from_visible": False}),
                "not_found",
                step,
            )
            self.assertEqual(
                _upload_step_result(self.spec, policy, step, {**own, "user_id": "018f1a00-7b3c-7d4e-8a11-999999999999"}),
                "forbidden",
                step,
            )
        resume = _operation(self.spec, "getFileUpload")
        self.assertIn("next_offset", self.spec["components"]["schemas"]["FileUpload"]["required"])
        self.assertEqual(
            resume["responses"]["200"]["content"]["application/json"]["schema"]["$ref"].rsplit("/", 1)[1],
            "FileUpload",
        )
        upload_write = self.spec["components"]["schemas"]["FileUploadWrite"]
        self.assertNotIn("user_id", upload_write["properties"])
        self.assertNotIn("roles", upload_write["properties"])
        self.assertFalse(upload_write["additionalProperties"])
        request = self.spec["components"]["schemas"]["ContextTokenRequest"]
        self.assertEqual(set(request["properties"]), {"matter_id"})
        self.assertFalse(request["additionalProperties"])
        token = self.spec["components"]["schemas"]["ContextToken"]
        self.assertIn("viewer_user_id", token["required"])
        self.assertIn("roles", token["required"])
        for case in policy["access_cases"]:
            self.assertEqual(_client_context_result(case), case["result"], case)

    def test_invoicing_is_time_domain_and_trust_posts_once(self):
        for operation_id in (
            "listInvoice",
            "getInvoice",
            "updateInvoice",
            "historyInvoice",
            "createInvoiceDrafts",
            "updateInvoiceDraft",
            "approveInvoice",
            "sendInvoice",
            "getInvoicePdf",
            "voidInvoice",
            "writeOffInvoice",
            "getBillingWorklist",
            "listPayment",
            "getPayment",
            "createPayment",
            "bulkPayment",
            "updatePaymentStatus",
            "reversePayment",
            "applyTrustToInvoice",
            "applyFlatFee",
            "reportUnbilledTime",
            "reportTimeByUser",
            "reportArAging",
            "reportPaymentsReceived",
            "reportWriteOffs",
            "listTimeEntry",
            "createTimeEntry",
            "listExpense",
            "createExpense",
        ):
            self.assertEqual(
                _operation(self.spec, operation_id).get("x-togavsu-replaceable-domain"),
                "time",
                operation_id,
            )
        for operation_id in ("postTrustEntry", "listTrustEntry", "requestInvoiceReplenishment", "reportTrustBalances", "reportReconciliations"):
            self.assertEqual(
                _operation(self.spec, operation_id).get("x-togavsu-replaceable-domain"),
                "trust",
                operation_id,
            )
        self.assertEqual(_operation(self.spec, "listCalendarEvent").get("x-togavsu-replaceable-domain"), "calendar")
        self.assertIsNone(_operation(self.spec, "listFeeAgreement").get("x-togavsu-replaceable-domain"))
        self.assertNotIn("x-togavsu-domain-dispatch", _operation(self.spec, "postTrustEntry"))
        switch = _operation(self.spec, "putDomainHandler")["x-togavsu-access"]["trust_posting"]
        self.assertTrue(switch["exactly_once"])
        self.assertTrue(switch["append_only"])
        self.assertFalse(switch["time_module_also_posts"])
        self.assertEqual(switch["billing_transfer_posted_by"], "trust_handler")
        for operation_id in ("applyTrustToInvoice", "applyFlatFee", "reversePayment"):
            dispatch = _operation(self.spec, operation_id)["x-togavsu-domain-dispatch"]
            self.assertEqual(dispatch["primary_domain"], "time", operation_id)
            for trust_replaced in (False, True):
                poster = _trust_poster(trust_replaced, dispatch)
                self.assertEqual(poster, "trust_module" if trust_replaced else "core", operation_id)

    def test_api_token_reads_are_self_or_admin(self):
        for operation_id in ("listApiToken", "getApiToken", "createApiToken", "revokeApiToken"):
            access = _operation(self.spec, operation_id)["x-togavsu-access"]
            self.assertEqual(access["owner"], "self_or_admin", operation_id)
            self.assertEqual(access["client"], "own", operation_id)
            self.assertFalse(access["secret_on_read"], operation_id)
        self.assertNotIn("secret", self.spec["components"]["schemas"]["ApiToken"]["properties"])
        self.assertIn("secret", self.spec["components"]["schemas"]["ApiTokenCreated"]["properties"])
        cases = {
            (case["action"], case["role"], case["owner"]): case["result"]
            for case in _operation(self.spec, "listApiToken")["x-togavsu-access"]["access_cases"]
        }
        self.assertEqual(cases[("read", "client", "self")], "allowed")
        self.assertEqual(cases[("read", "client", "other_user")], "not_found")
        self.assertEqual(cases[("read", "staff", "other_user")], "not_found")
        self.assertEqual(cases[("read", "admin", "other_user")], "allowed")
        self.assertEqual(cases[("revoke", "staff", "self")], "allowed")
        self.assertEqual(cases[("revoke", "client", "self")], "allowed")
        self.assertEqual(cases[("create", "staff", "self")], "forbidden")
        self.assertEqual(cases[("create", "accountant", "self")], "forbidden")
        self.assertEqual(cases[("create", "client", "self")], "forbidden")
        create = _operation(self.spec, "createApiToken")
        self.assertEqual(create["x-togavsu-access"]["roles"], ["admin"])
        self.assertEqual(create["x-togavsu-access"]["issuance"], "admin_only")
        self.assertFalse(create["x-togavsu-access"]["bearer_amplification"])
        self.assertNotEqual(create["x-togavsu-access"].get("module_delegation"), "delegated_viewer")
        write = self.spec["components"]["schemas"]["ApiTokenWrite"]
        self.assertIn("oneOf", write)
        staff_owner = "018f1a00-7b3c-7d4e-8a11-222222222222"
        issued = issue_api_token(
            create,
            {
                "role": "admin",
                "credential": "session",
                "caller_scopes": ["tokens:write", "matters:read", "trust:read"],
                "name": "staff-matters",
                "owner_user_id": staff_owner,
                "scopes": ["matters:read"],
                "owner_role_scopes": ["matters:read", "profile:read"],
            },
        )
        self.assertEqual(issued["result"], "allowed")
        self.assertEqual(issued["token"]["owner_user_id"], staff_owner)
        self.assertEqual(issued["token"]["scopes"], ["matters:read"])
        self.assertEqual(issued["secret"], "issued-once")
        for role in ("staff", "accountant", "client"):
            denied = issue_api_token(create, {"role": role, "credential": "session", "caller_scopes": ["tokens:write"], "owner_user_id": staff_owner, "scopes": ["profile:read"], "owner_role_scopes": ["profile:read"]})
            self.assertEqual(denied["result"], "forbidden", role)
            self.assertIsNone(denied["token"], role)
            self.assertIsNone(denied["secret"], role)
        amplified = issue_api_token(
            create,
            {
                "role": "admin",
                "credential": "bearer",
                "token_scopes": ["tokens:write"],
                "owner_user_id": staff_owner,
                "scopes": ["trust:read"],
                "owner_role_scopes": ["trust:read", "tokens:write"],
            },
        )
        self.assertEqual(amplified["result"], "scope_denied")
        self.assertIsNone(amplified["secret"])
        beyond_owner = issue_api_token(
            create,
            {
                "role": "admin",
                "credential": "session",
                "caller_scopes": ["tokens:write", "trust:withdraw"],
                "owner_user_id": staff_owner,
                "scopes": ["trust:withdraw"],
                "owner_role_scopes": ["matters:read"],
            },
        )
        self.assertEqual(beyond_owner["result"], "scope_denied")
        delegated = issue_api_token(
            create,
            {
                "role": "admin",
                "viewer_role": "admin",
                "caller_module_id": "clock",
                "module_scopes": ["tokens:write", "trust:withdraw"],
                "credential": "bearer",
                "token_scopes": ["tokens:write", "trust:withdraw"],
                "module_id": "clock",
                "scopes": ["trust:withdraw"],
                "module_approved_scopes": ["trust:withdraw"],
            },
        )
        self.assertEqual(delegated["result"], "forbidden")
        self.assertIsNone(delegated["token"])
        unbound = issue_api_token(create, {"role": "admin", "credential": "session", "caller_scopes": ["tokens:write"], "scopes": ["tokens:write"]})
        self.assertEqual(unbound["result"], "validation_failed")
        module_token = issue_api_token(
            create,
            {
                "role": "admin",
                "credential": "session",
                "caller_scopes": ["tokens:write", "files:write"],
                "name": "importer",
                "module_id": "importer",
                "scopes": ["files:write", "matters:read"],
                "module_approved_scopes": ["files:write"],
            },
        )
        self.assertEqual(module_token["result"], "scope_denied")

    def test_trust_adjustment_credit_and_debit(self):
        from jsonschema import Draft202012Validator

        write = self.spec["components"]["schemas"]["TrustEntryWrite"]
        read = self.spec["components"]["schemas"]["TrustEntry"]
        credit = _adjustment_body(5000, "credit")
        debit = _adjustment_body(-5000, "debit")
        Draft202012Validator(write).validate(credit)
        Draft202012Validator(write).validate(debit)
        Draft202012Validator(read).validate(_adjustment_row(credit))
        Draft202012Validator(read).validate(_adjustment_row(debit))
        deposit = dict(credit)
        deposit["entry_type"] = "deposit"
        deposit["amount_cents"] = -1
        deposit.pop("adjustment_direction")
        with self.assertRaises(Exception):
            Draft202012Validator(write).validate(deposit)
        missing_direction = dict(credit)
        del missing_direction["adjustment_direction"]
        with self.assertRaises(Exception):
            Draft202012Validator(write).validate(missing_direction)
        post = _operation(self.spec, "postTrustEntry")
        self.assertEqual(post["x-togavsu-access"]["below_zero"], "posts")
        self.assertNotIn("put", self.spec["paths"]["/api/v1/trust-ledger"])
        self.assertNotIn("delete", self.spec["paths"]["/api/v1/trust-ledger"])

    def test_attachment_link_authorizes_both_files(self):
        operation = _operation(self.spec, "createFileLink")
        self.assertEqual(operation["x-togavsu-mcp"]["parity"], "required")
        self.assertEqual(operation["x-togavsu-access"]["authorizes"], "both_files")
        self.assertTrue(operation["x-togavsu-access"]["relationship_immutable"])
        link = self.spec["components"]["schemas"]["FileLinkWrite"]
        self.assertEqual(link["properties"]["relationship"]["const"], "attachment")
        cases = {(case.get("result")) for case in operation["x-togavsu-access"]["access_cases"]}
        self.assertIn("not_found", cases)
        self.assertIn("matter_mismatch", cases)
        self.assertIn("validation_failed", cases)
        self.assertIn("allowed", cases)
        hidden = [case for case in operation["x-togavsu-access"]["access_cases"] if case.get("to_visible") is False]
        self.assertEqual(hidden[0]["result"], "not_found")

    def test_verified_email_change_keeps_privileged_fields_rejected(self):
        request = _operation(self.spec, "requestEmailChange")
        confirm = _operation(self.spec, "confirmEmailChange")
        self.assertEqual(request["x-togavsu-mcp"]["parity"], "required")
        self.assertEqual(confirm["x-togavsu-mcp"]["parity"], "required")
        self.assertEqual(request["x-togavsu-access"]["client"], "own")
        self.assertEqual(confirm["x-togavsu-access"]["client"], "own")
        self.assertIn("duplicate_email", request["x-togavsu-error-codes"])
        self.assertEqual(_schema_name(request), "EmailChangeRequest")
        self.assertIn("email", _operation(self.spec, "updateMe")["x-togavsu-access"]["rejected_fields"])
        self.assertNotIn("email", self.spec["components"]["schemas"]["MeProfileWrite"]["properties"])

    def test_trust_moving_payments_are_scoped_before_mutation(self):
        reverse = _operation(self.spec, "reversePayment")
        self.assertEqual(reverse["x-togavsu-domain-dispatch"]["applies_when"], "payment_has_trust_entry")
        self.assertEqual(reverse["x-togavsu-access"]["scopes"], ["payments:write"])
        account = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00001"
        accounts = [{"id": account, "active": True, "authorized": True, "firm_id": "firm-1"}]
        trust_ok = {"trust_account_id": account, "trust_accounts": accounts, "firm_id": "firm-1"}
        compensation = {
            "client_id": "018f1a00-7b3c-7d4e-8a11-222222222222",
            "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
            "amount_cents": 1500,
            "trust_entry_id": "018f1a00-7b3c-7d4e-8a11-444444444444",
            "created_by": "018f1a00-7b3c-7d4e-8a11-555555555555",
            "posted_entry_id": "018f1a00-7b3c-7d4e-8a11-666666666666",
            "created_at": "2026-11-07T18:00:00Z",
            "balance_cents": -1500,
            "original_entry": {
                "id": "018f1a00-7b3c-7d4e-8a11-444444444444",
                "entry_type": "deposit",
                "amount_cents": 1500,
            },
        }
        cases = [
            ({"role": "attorney", "token_scopes": ["payments:write"], "trust_linked": False, "matter_walled": False}, "allowed", False, False),
            ({"role": "attorney", "token_scopes": ["payments:write", "trust:withdraw"], "trust_linked": True, "matter_walled": False, "below_zero": True, **trust_ok, **compensation}, "allowed", True, True),
            ({"role": "attorney", "token_scopes": ["payments:write"], "trust_linked": True, "matter_walled": False}, "scope_denied", False, False),
            ({"role": "attorney", "token_scopes": ["trust:withdraw"], "trust_linked": True, "matter_walled": False}, "scope_denied", False, False),
            ({"role": "staff", "token_scopes": ["payments:write", "trust:withdraw"], "trust_linked": True, "matter_walled": False}, "forbidden", False, False),
            ({"role": "accountant", "token_scopes": ["payments:write", "trust:withdraw"], "trust_linked": False, "matter_walled": False}, "forbidden", False, False),
            ({"role": "attorney", "token_scopes": ["payments:write", "trust:withdraw"], "trust_linked": True, "matter_walled": True, "on_wall": False}, "not_found", False, False),
        ]
        for call, result, ledger, flag in cases:
            decision = payment_reversal_decision(reverse, call)
            self.assertEqual(decision["result"], result, call)
            self.assertEqual(decision["ledger_mutated"], ledger, call)
            self.assertEqual(decision["flag"], flag, call)
            if result != "allowed":
                self.assertFalse(decision["payment_mutated"], call)
            else:
                self.assertTrue(decision["payment_mutated"], call)
                self.assertEqual(decision["payment_status"], "reversed")
                self.assertTrue(decision["amount_unchanged"])
        trust = payment_reversal_decision(
            reverse,
            {"role": "attorney", "token_scopes": ["payments:write", "trust:withdraw"], "trust_linked": True, "matter_walled": False, "below_zero": True, **trust_ok, **compensation},
        )
        self.assertTrue(trust["ledger_append_only"])
        self.assertFalse(trust["original_trust_edited"])
        self.assertEqual(trust["trust_posted_by"], "trust_handler")
        self.assertEqual(trust["trust_account_id"], account)
        self.assertEqual(trust["trust_entry"]["entry_type"], "reversal")
        self.assertEqual(trust["trust_entry"]["amount_cents"], -1500)
        self.assertEqual(trust["trust_entry"]["direction"], "debit")
        self.assertEqual(trust["trust_entry"]["reverses_entry_id"], compensation["trust_entry_id"])
        _component_validator(self.spec, "TrustEntry").validate(trust["trust_entry"])
        _component_validator(self.spec, "TrustPostResult").validate(trust["trust_post"])
        write_row = {key: value for key, value in trust["trust_entry"].items() if key not in {"id", "created_at", "created_by"}}
        with self.assertRaises(Exception):
            _component_validator(self.spec, "TrustEntryWrite").validate(write_row)
        incomplete = payment_reversal_decision(
            reverse,
            {"role": "attorney", "token_scopes": ["payments:write", "trust:withdraw"], "trust_linked": True, "matter_walled": False, **trust_ok},
        )
        self.assertEqual(incomplete["result"], "validation_failed")
        self.assertFalse(incomplete["ledger_mutated"])
        transfer = payment_reversal_decision(
            reverse,
            {
                "role": "attorney",
                "token_scopes": ["payments:write", "trust:withdraw"],
                "trust_linked": True,
                "matter_walled": False,
                **trust_ok,
                **compensation,
                "original_entry": {"id": compensation["trust_entry_id"], "entry_type": "transfer_to_operating", "amount_cents": 1500},
            },
        )
        self.assertEqual(transfer["result"], "allowed")
        self.assertEqual(transfer["trust_entry"]["direction"], "credit")
        self.assertEqual(transfer["trust_entry"]["amount_cents"], 1500)
        self.assertEqual(transfer["trust_entry"]["reverses_entry_id"], compensation["trust_entry_id"])
        _component_validator(self.spec, "TrustEntry").validate(transfer["trust_entry"])
        _component_validator(self.spec, "TrustPostResult").validate(transfer["trust_post"])
        adjustment = payment_reversal_decision(
            reverse,
            {
                "role": "attorney",
                "token_scopes": ["payments:write", "trust:withdraw"],
                "trust_linked": True,
                "matter_walled": False,
                **trust_ok,
                **compensation,
                "amount_cents": 400,
                "original_entry": {"id": compensation["trust_entry_id"], "entry_type": "adjustment", "amount_cents": 400},
            },
        )
        self.assertEqual(adjustment["trust_entry"]["amount_cents"], -400)
        self.assertEqual(adjustment["trust_entry"]["direction"], "debit")
        _component_validator(self.spec, "TrustEntry").validate(adjustment["trust_entry"])
        zero = payment_reversal_decision(
            reverse,
            {
                "role": "attorney",
                "token_scopes": ["payments:write", "trust:withdraw"],
                "trust_linked": True,
                "matter_walled": False,
                **trust_ok,
                **compensation,
                "amount_cents": 0,
                "original_entry": {"id": compensation["trust_entry_id"], "entry_type": "deposit", "amount_cents": 0},
            },
        )
        self.assertEqual(zero["result"], "validation_failed")
        self.assertFalse(zero["ledger_mutated"])
        self.assertFalse(zero["payment_mutated"])
        again = payment_reversal_decision(
            reverse,
            {
                "role": "attorney",
                "token_scopes": ["payments:write", "trust:withdraw"],
                "trust_linked": True,
                "matter_walled": False,
                **trust_ok,
                **compensation,
                "existing_reversal": trust["trust_entry"],
            },
        )
        self.assertEqual(again["result"], "original_result")
        self.assertFalse(again["ledger_mutated"])
        self.assertFalse(again["payment_mutated"])
        self.assertEqual(again["trust_entry"], trust["trust_entry"])
        mismatched = payment_reversal_decision(
            reverse,
            {"role": "attorney", "token_scopes": ["payments:write", "trust:withdraw"], "trust_linked": True, "matter_walled": False, "reversal_account_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaa00002", **trust_ok},
        )
        self.assertEqual(mismatched["result"], "forbidden")
        self.assertFalse(mismatched["payment_mutated"])
        self.assertFalse(mismatched["ledger_mutated"])
        operating = payment_reversal_decision(
            reverse,
            {"role": "attorney", "token_scopes": ["payments:write"], "trust_linked": False, "matter_walled": False},
        )
        self.assertFalse(operating["ledger_mutated"])
        create = _operation(self.spec, "createPayment")
        status = _operation(self.spec, "updatePaymentStatus")
        bulk = _operation(self.spec, "bulkPayment")
        writes = [
            (create, {"role": "staff", "token_scopes": ["payments:write"], "destination": "operating", "status": "completed"}, "allowed", False),
            (create, {"role": "staff", "token_scopes": ["payments:write"], "destination": "trust", "status": "completed"}, "scope_denied", False),
            (create, {"role": "staff", "token_scopes": ["payments:write", "trust:write"], "destination": "trust", "status": "completed", **trust_ok}, "allowed", True),
            (create, {"role": "staff", "token_scopes": ["payments:write"], "destination": "trust", "status": "pending", **trust_ok}, "allowed", False),
            (create, {"role": "staff", "token_scopes": ["payments:write"], "destination": "trust", **trust_ok}, "allowed", False),
            (bulk, {"role": "staff", "token_scopes": ["payments:write"], "trust_accounts": accounts, "firm_id": "firm-1", "items": [{"destination": "trust", "trust_account_id": account}]}, "allowed", False),
            (create, {"role": "staff", "token_scopes": ["payments:write", "trust:write"], "destination": "trust", "status": "completed", "matter_walled": True, "on_wall": False}, "not_found", False),
            (status, {"role": "staff", "token_scopes": ["payments:write"], "destination": "trust", "status": "completed", "trust_already_posted": False}, "scope_denied", False),
            (status, {"role": "staff", "token_scopes": ["payments:write", "trust:write"], "destination": "trust", "status": "completed", "trust_already_posted": False, **trust_ok}, "allowed", True),
            (status, {"role": "staff", "token_scopes": ["payments:write"], "destination": "trust", "status": "completed", "trust_already_posted": True, **trust_ok}, "allowed", False),
            (bulk, {"role": "staff", "token_scopes": ["payments:write"], "items": [{"destination": "operating", "status": "completed"}, {"destination": "trust", "status": "completed"}]}, "scope_denied", False),
            (bulk, {"role": "staff", "token_scopes": ["payments:write", "trust:write"], "trust_accounts": accounts, "firm_id": "firm-1", "items": [{"destination": "operating", "status": "completed"}, {"destination": "trust", "status": "completed", "trust_account_id": account}]}, "allowed", True),
        ]
        for operation, call, result, ledger in writes:
            decision = payment_effect_decision(operation, call)
            self.assertEqual(decision["result"], result, (operation["operationId"], call))
            self.assertEqual(decision["ledger_mutated"], ledger, (operation["operationId"], call))
            if result != "allowed":
                self.assertFalse(decision["payment_mutated"], call)

    def test_processor_callback_records_and_finishes_without_a_viewer(self):
        from jsonschema import Draft202012Validator

        policy = self.spec["x-togavsu-module-delegation"]["noninteractive"]["payment_processor"]
        webhook = self.spec["webhooks"]["moduleEvent"]["post"]
        self.assertEqual(webhook["x-togavsu-processor-delivery"], policy)
        self.assertEqual(webhook["x-togavsu-task-deliveries"]["payment_processor"], policy)
        self.assertFalse(policy["viewer_context_required"])
        self.assertFalse(policy["callback_requires_viewer"])
        self.assertTrue(policy["initialization_requires_viewer"])
        self.assertFalse(policy["grants_admin"])
        self.assertFalse(policy["firmwide_bypass"])
        self.assertFalse(policy["ambient_payment_write"])
        self.assertEqual(policy["sequence"], ["createPayment", "updatePaymentStatus"])
        graph = policy["status_graph"]
        self.assertEqual(graph["transitions"]["pending"], ["completed", "failed"])
        self.assertEqual(graph["transitions"]["completed"], [])
        self.assertEqual(graph["transitions"]["reversed"], [])
        self.assertEqual(graph["settled_correction"], "reversePayment")
        self.assertEqual(graph["settled_correction_excludes"], "failed")
        self.assertTrue(graph["reversed_terminal"])
        status_operation = _operation(self.spec, "updatePaymentStatus")
        self.assertEqual(status_operation["x-togavsu-access"]["payment_status"], graph)
        self.assertIn("conflict", status_operation["x-togavsu-error-codes"])
        status_tool = next(tool for tool in render_mcp(self.spec)["tools"] if tool["name"] == "updatePaymentStatus")
        self.assertEqual(status_tool["payment_status"], graph)
        self.assertIn("reversePayment", self.spec["components"]["schemas"]["PaymentStatusWrite"]["description"])
        self.assertIn("processor_authorization", self.spec["components"]["schemas"]["WebhookEvent"]["properties"])
        initiation = _operation(self.spec, "initiateProcessorPayment")
        self.assertEqual(initiation["x-togavsu-access"]["module_delegation"], "delegated_viewer")
        self.assertNotIn("allowed_operations", self.spec["components"]["schemas"]["ProcessorInitialization"]["properties"])
        for operation_id in policy["sequence"]:
            operation = _operation(self.spec, operation_id)
            self.assertEqual(operation["x-togavsu-access"]["module_delegation"], "delegated_viewer_or_noninteractive", operation_id)
            self.assertEqual(operation["x-togavsu-access"]["noninteractive_workflow"], "payment_processor", operation_id)
        module = {
            "registered": True,
            "module_id": "lawpay",
            "recipient_id": "lawpay",
            "approved_scopes": ["payments:write"],
        }
        initialization = {
            "processor_module_id": "lawpay",
            "initialization_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaaa",
            "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbbb",
            "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
            "contact_id": "018f1a00-7b3c-7d4e-8a11-222222222222",
            "invoice_id": None,
            "amount_cents": 2500,
            "destination": "operating",
            "trust_account_id": None,
            "caller_scopes": ["payments:write"],
            "expires_at": "2026-11-07T18:00:00Z",
            "matter_walled": False,
        }
        issued = processor_issuance(policy, module, initialization)
        self.assertEqual(issued["result"], "allowed")
        self.assertFalse(issued["payment_mutated"])
        self.assertFalse(issued["ledger_mutated"])
        self.assertEqual(issued["grant"]["payment_id"], initialization["reserved_payment_id"])
        self.assertIsNone(issued["grant"]["trust_account_id"])
        Draft202012Validator(self.spec["components"]["schemas"]["ProcessorAuthorization"]).validate(issued["grant"])
        with self.assertRaises(Exception):
            Draft202012Validator(self.spec["components"]["schemas"]["ProcessorAuthorization"]).validate({**issued["grant"], "admin": True})
        for label, bad_module, bad_init in (
            ("unregistered", {**module, "registered": False}, initialization),
            ("recipient", {**module, "recipient_id": "other"}, initialization),
            ("other-module", {**module, "module_id": "other", "recipient_id": "other"}, initialization),
            ("wall", module, {**initialization, "matter_walled": True, "on_wall": False}),
        ):
            denied = processor_issuance(policy, bad_module, bad_init)
            self.assertNotEqual(denied["result"], "allowed", label)
            self.assertIsNone(denied["grant"], label)
            self.assertFalse(denied["payment_mutated"], label)
            self.assertFalse(denied["ledger_mutated"], label)
        trust_init = {**initialization, "destination": "trust", "caller_scopes": ["payments:write"]}
        trust_module = {**module, "approved_scopes": ["payments:write", "trust:write"]}
        denied_caller = processor_issuance(policy, trust_module, trust_init)
        self.assertEqual(denied_caller["result"], "scope_denied")
        self.assertIsNone(denied_caller["grant"])
        denied_module = processor_issuance(policy, module, {**trust_init, "caller_scopes": ["payments:write", "trust:write"]})
        self.assertEqual(denied_module["result"], "scope_denied")
        self.assertFalse(denied_module["payment_mutated"])
        trust_account = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00001"
        trust_accounts = [{"id": trust_account, "active": True, "authorized": True, "firm_id": "firm-1"}]
        trust_issued = processor_issuance(
            policy,
            trust_module,
            {
                **trust_init,
                "initialization_id": "018f1a00-7b3c-7d4e-8a11-cccccccccccc",
                "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-dddddddddddd",
                "caller_scopes": ["payments:write", "trust:write"],
                "trust_account_id": trust_account,
                "trust_accounts": trust_accounts,
                "firm_id": "firm-1",
            },
        )
        self.assertEqual(trust_issued["result"], "allowed")
        self.assertFalse(trust_issued["payment_mutated"])

        store = {"payments": {}, "ledger": [], "idempotency": {}}

        def callback(operation_id, call):
            operation = _operation(self.spec, operation_id)
            before = (len(store["payments"]), len(store["ledger"]))
            outcome = processor_apply(store, policy, operation, {**call, "operation": operation_id})
            if outcome["result"] != "allowed":
                self.assertEqual(len(store["payments"]), before[0], call)
                self.assertEqual(len(store["ledger"]), before[1], call)
            return outcome["result"]

        grant = issued["grant"]
        base = {
            "registered": True,
            "caller_module_id": "lawpay",
            "module_scopes": ["payments:write"],
            "grant": grant,
            "matter_id": initialization["matter_id"],
            "contact_id": initialization["contact_id"],
            "invoice_id": None,
            "amount_cents": 2500,
            "destination": "operating",
            "viewer_present": False,
            "posts_trust": False,
            "status": "pending",
        }
        self.assertEqual(callback("createPayment", {**base, "operation": "createPayment", "idempotency_key": "pay-1"}), "allowed")
        self.assertEqual(len(store["payments"]), 1)
        self.assertEqual(store["ledger"], [])
        self.assertFalse(base["viewer_present"])
        replayed = processor_apply(store, policy, _operation(self.spec, "createPayment"), {**base, "operation": "createPayment", "idempotency_key": "pay-1"})
        self.assertEqual(replayed["result"], "original_result")
        omitted_status = {key: value for key, value in base.items() if key != "status"}
        omitted_replay = processor_apply(store, policy, _operation(self.spec, "createPayment"), {**omitted_status, "operation": "createPayment", "idempotency_key": "pay-1"})
        self.assertEqual(omitted_replay["result"], "original_result")
        self.assertEqual(omitted_replay["payment"]["accepted_request"]["status"], "pending")
        self.assertEqual(len(store["ledger"]), 0)
        self.assertEqual(replayed["payment"]["id"], next(iter(store["payments"].values()))["id"])
        self.assertEqual(len(store["payments"]), 1)
        status_conflict = processor_apply(
            store,
            policy,
            _operation(self.spec, "createPayment"),
            {**base, "operation": "createPayment", "idempotency_key": "pay-1", "status": "completed"},
        )
        self.assertEqual(status_conflict["result"], "idempotency_conflict")
        self.assertIsNone(status_conflict["payment"])
        self.assertEqual(len(store["ledger"]), 0)
        self.assertEqual(next(iter(store["payments"].values()))["status"], "pending")
        self.assertEqual(len(store["payments"]), 1)
        bound = grant["payment_id"]
        self.assertEqual(
            callback("updatePaymentStatus", {**base, "operation": "updatePaymentStatus", "payment_id": bound, "status": "completed", "field": "status"}),
            "allowed",
        )
        self.assertEqual(next(iter(store["payments"].values()))["status"], "completed")
        self.assertEqual(store["ledger"], [])
        denied = [
            ("amount", {**base, "operation": "createPayment", "amount_cents": 1}, "forbidden"),
            ("matter", {**base, "operation": "createPayment", "matter_id": "018f1a00-7b3c-7d4e-8a11-999999999999"}, "not_found"),
            ("contact", {**base, "operation": "createPayment", "contact_id": "018f1a00-7b3c-7d4e-8a11-888888888888"}, "not_found"),
            ("invoice", {**base, "operation": "createPayment", "invoice_id": "018f1a00-7b3c-7d4e-8a11-777777777777"}, "not_found"),
            ("destination", {**base, "operation": "createPayment", "destination": "trust"}, "forbidden"),
            ("module", {**base, "operation": "createPayment", "caller_module_id": "other"}, "forbidden"),
            ("scopes", {**base, "operation": "createPayment", "module_scopes": ["invoices:read"]}, "scope_denied"),
            ("expired", {**base, "operation": "createPayment", "grant": {**grant, "expired": True}}, "forbidden"),
            ("revoked", {**base, "operation": "createPayment", "grant": {**grant, "revoked": True}}, "forbidden"),
            ("missing", {**base, "operation": "createPayment", "grant": None}, "forbidden"),
            ("token", {**base, "operation": "updatePaymentStatus", "payment_id": bound, "token_revoked": True}, "unauthenticated"),
            ("other-payment", {**base, "operation": "updatePaymentStatus", "payment_id": "pay-other", "status": "failed"}, "not_found"),
            ("outside", {**base, "operation": "listPayment"}, "forbidden"),
            ("field", {**base, "operation": "updatePaymentStatus", "payment_id": bound, "field": "amount_cents"}, "field_locked"),
        ]
        for label, call, expected in denied:
            self.assertEqual(callback(call["operation"], call), expected, label)
        self.assertEqual(len(store["payments"]), 1)
        saved = next(iter(store["payments"].values()))
        self.assertEqual(saved["status"], "completed")
        self.assertEqual(saved["amount_cents"], 2500)
        self.assertEqual(store["ledger"], [])
        after_status = processor_apply(
            store,
            policy,
            _operation(self.spec, "createPayment"),
            {**base, "operation": "createPayment", "idempotency_key": "pay-1", "status": "pending"},
        )
        self.assertEqual(after_status["result"], "original_result")
        self.assertEqual(after_status["payment"]["id"], bound)
        self.assertEqual(after_status["payment"]["status"], "completed")
        self.assertEqual(after_status["payment"]["accepted_request"]["status"], "pending")
        self.assertEqual(len(store["ledger"]), 0)
        method_edit = processor_apply(
            store,
            policy,
            _operation(self.spec, "createPayment"),
            {**base, "operation": "createPayment", "idempotency_key": "pay-1", "method": "check"},
        )
        self.assertEqual(method_edit["result"], "idempotency_conflict")
        self.assertIsNone(method_edit["payment"])
        fresh_method = processor_apply(
            store,
            policy,
            _operation(self.spec, "createPayment"),
            {**base, "operation": "createPayment", "idempotency_key": "pay-2", "method": "card"},
        )
        self.assertEqual(fresh_method["result"], "conflict")
        self.assertIsNone(fresh_method["payment"])
        omitted_reference = processor_apply(
            store,
            policy,
            _operation(self.spec, "createPayment"),
            {**base, "operation": "createPayment", "reference_number": "1008"},
        )
        self.assertEqual(omitted_reference["result"], "conflict")
        self.assertIsNone(omitted_reference["payment"])
        hidden = processor_apply(
            store,
            policy,
            _operation(self.spec, "createPayment"),
            {**base, "operation": "createPayment", "idempotency_key": "pay-1", "grant": {**grant, "expired": True}},
        )
        self.assertEqual(hidden["result"], "forbidden")
        self.assertIsNone(hidden["payment"])
        self.assertEqual(len(store["payments"]), 1)

        trust_grant = trust_issued["grant"]
        trust_base = {
            **base,
            "grant": trust_grant,
            "destination": "trust",
            "trust_account_id": trust_account,
            "trust_accounts": trust_accounts,
            "firm_id": "firm-1",
            "module_scopes": ["payments:write", "trust:write"],
            "status": "pending",
        }
        self.assertEqual(callback("createPayment", {**trust_base, "operation": "createPayment"}), "allowed")
        self.assertEqual(len(store["ledger"]), 0)
        trust_payment = trust_grant["payment_id"]
        self.assertEqual(
            callback(
                "updatePaymentStatus",
                {**trust_base, "operation": "updatePaymentStatus", "payment_id": trust_payment, "status": "completed", "idempotency_key": "trust-done"},
            ),
            "allowed",
        )
        self.assertEqual(len(store["ledger"]), 1)
        status_key = processor_apply(
            store,
            policy,
            _operation(self.spec, "updatePaymentStatus"),
            {**trust_base, "operation": "updatePaymentStatus", "payment_id": trust_payment, "status": "failed", "idempotency_key": "trust-done"},
        )
        self.assertEqual(status_key["result"], "idempotency_conflict")
        self.assertIsNone(status_key["payment"])
        self.assertEqual(len(store["ledger"]), 1)
        second_key = processor_apply(
            store,
            policy,
            _operation(self.spec, "updatePaymentStatus"),
            {**trust_base, "operation": "updatePaymentStatus", "payment_id": trust_payment, "status": "completed", "idempotency_key": "trust-done-2"},
        )
        self.assertEqual(second_key["result"], "original_result")
        self.assertEqual(second_key["payment"]["id"], trust_payment)
        self.assertFalse(second_key["ledger_inserted"])
        self.assertEqual(len(store["ledger"]), 1)
        noop_key = ("lawpay", "updatePaymentStatus", trust_grant["initialization_id"], "trust-done-2")
        self.assertEqual(store["idempotency"][noop_key]["initialization_id"], trust_grant["initialization_id"])
        failed_same_key = processor_apply(
            store,
            policy,
            _operation(self.spec, "updatePaymentStatus"),
            {**trust_base, "operation": "updatePaymentStatus", "payment_id": trust_payment, "status": "failed", "idempotency_key": "trust-done-2"},
        )
        self.assertEqual(failed_same_key["result"], "idempotency_conflict")
        self.assertIsNone(failed_same_key["payment"])
        self.assertEqual(store["payments"][trust_grant["initialization_id"]]["status"], "completed")
        self.assertEqual(len(store["ledger"]), 1)
        self.assertEqual(store["ledger"][0]["trust_account_id"], trust_account)
        late_failed = processor_apply(
            store,
            policy,
            _operation(self.spec, "updatePaymentStatus"),
            {**trust_base, "operation": "updatePaymentStatus", "payment_id": trust_payment, "status": "failed", "idempotency_key": "trust-late-failed"},
        )
        self.assertEqual(late_failed["result"], "conflict")
        self.assertEqual(late_failed["payment"]["status"], "completed")
        self.assertFalse(late_failed["ledger_inserted"])
        self.assertEqual(len(store["ledger"]), 1)
        self.assertEqual(
            callback(
                "updatePaymentStatus",
                {**trust_base, "operation": "updatePaymentStatus", "payment_id": trust_payment, "status": "completed"},
            ),
            "original_result",
        )
        self.assertEqual(len(store["ledger"]), 1)
        store["payments"][trust_grant["initialization_id"]]["status"] = "reversed"
        reopened = processor_apply(
            store,
            policy,
            _operation(self.spec, "updatePaymentStatus"),
            {**trust_base, "operation": "updatePaymentStatus", "payment_id": trust_payment, "status": "completed", "idempotency_key": "trust-reopen"},
        )
        self.assertEqual(reopened["result"], "conflict")
        self.assertEqual(reopened["payment"]["status"], "reversed")
        self.assertFalse(reopened["ledger_inserted"])
        self.assertEqual(len(store["ledger"]), 1)
        bare = {**trust_base, "operation": "createPayment", "status": "completed", "module_scopes": ["payments:write"], "grant": {**trust_grant, "payment_id": None}}
        self.assertEqual(callback("createPayment", bare), "scope_denied")
        self.assertEqual(len(store["payments"]), 2)
        self.assertEqual(len(store["ledger"]), 1)

    def test_processor_initialization_binds_one_payment(self):
        policy = self.spec["x-togavsu-module-delegation"]["noninteractive"]["payment_processor"]
        self.assertTrue(policy["binding"]["idempotency_independent"])
        self.assertEqual(policy["binding"]["cardinality"], "one_payment_per_initialization")
        module = {"registered": True, "module_id": "lawpay", "recipient_id": "lawpay", "approved_scopes": ["payments:write", "trust:write"]}
        account = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00001"
        other = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00002"
        accounts = [
            {"id": account, "active": True, "authorized": True, "firm_id": "firm-1"},
            {"id": other, "active": True, "authorized": True, "firm_id": "firm-1"},
            {"id": "018f1a00-7b3c-7d4e-8a11-aaaaaaa00003", "active": False, "authorized": True, "firm_id": "firm-1"},
            {"id": "018f1a00-7b3c-7d4e-8a11-aaaaaaa00004", "active": True, "authorized": False, "firm_id": "firm-1"},
            {"id": "018f1a00-7b3c-7d4e-8a11-aaaaaaa00005", "active": True, "authorized": True, "firm_id": "other-firm"},
        ]
        issued = processor_issuance(
            policy,
            module,
            {
                "processor_module_id": "lawpay",
                "initialization_id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeeee",
                "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-ffffffffffff",
                "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
                "contact_id": "018f1a00-7b3c-7d4e-8a11-222222222222",
                "invoice_id": None,
                "amount_cents": 4000,
                "destination": "trust",
                "trust_account_id": account,
                "trust_accounts": accounts,
                "firm_id": "firm-1",
                "caller_scopes": ["payments:write", "trust:write"],
                "expires_at": "2026-11-07T18:00:00Z",
            },
        )
        self.assertEqual(issued["result"], "allowed")
        self.assertFalse(issued["payment_mutated"])
        grant = issued["grant"]
        create = _operation(self.spec, "createPayment")
        store = {"payments": {}, "ledger": [], "idempotency": {}}
        call = {
            "operation": "createPayment",
            "registered": True,
            "caller_module_id": "lawpay",
            "module_scopes": ["payments:write", "trust:write"],
            "grant": grant,
            "matter_id": grant["matter_id"],
            "contact_id": grant["contact_id"],
            "invoice_id": None,
            "amount_cents": 4000,
            "destination": "trust",
            "trust_account_id": account,
            "trust_accounts": accounts,
            "firm_id": "firm-1",
            "status": "completed",
            "viewer_present": False,
        }
        first = processor_apply(store, policy, create, call)
        self.assertEqual(first["result"], "allowed")
        self.assertTrue(first["payment_inserted"])
        self.assertTrue(first["ledger_inserted"])
        self.assertEqual(len(store["payments"]), 1)
        self.assertEqual(len(store["ledger"]), 1)
        self.assertEqual(store["ledger"][0]["trust_account_id"], account)
        for label, patch in (
            ("same-key", {"idempotency_key": "same"}),
            ("other-key", {"idempotency_key": "other"}),
            ("omitted-key", {}),
        ):
            again = processor_apply(store, policy, create, {**call, **patch})
            self.assertEqual(again["result"], "original_result", label)
            self.assertEqual(again["payment"]["id"], grant["payment_id"], label)
            self.assertFalse(again["payment_inserted"], label)
            self.assertFalse(again["ledger_inserted"], label)
            self.assertEqual(len(store["payments"]), 1, label)
            self.assertEqual(len(store["ledger"]), 1, label)
        changed = processor_apply(store, policy, create, {**call, "idempotency_key": "fresh", "method": "card", "status": "pending"})
        self.assertEqual(changed["result"], "conflict")
        self.assertFalse(changed["payment_inserted"])
        self.assertEqual(len(store["ledger"]), 1)
        raced, results = commit_concurrent_processor_creates(
            policy,
            create,
            [{**call, "idempotency_key": "race-a"}, {**call, "idempotency_key": "race-b"}],
        )
        self.assertEqual(len(raced["payments"]), 1)
        self.assertEqual(len(raced["ledger"]), 1)
        self.assertEqual(sum(1 for item in results if item["payment_inserted"]), 1)
        status = _operation(self.spec, "updatePaymentStatus")
        repeat = processor_apply(
            store,
            policy,
            status,
            {**call, "operation": "updatePaymentStatus", "payment_id": grant["payment_id"], "status": "completed"},
        )
        self.assertEqual(repeat["result"], "original_result")
        self.assertFalse(repeat["ledger_inserted"])
        self.assertEqual(len(store["ledger"]), 1)
        for label, init_patch, expected in (
            ("missing-account", {"trust_account_id": None}, "validation_failed"),
            ("unknown-account", {"trust_account_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaa00009"}, "not_found"),
            ("inactive", {"trust_account_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaa00003"}, "forbidden"),
            ("unauthorized", {"trust_account_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaa00004"}, "forbidden"),
            ("cross-firm", {"trust_account_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaa00005"}, "not_found"),
            ("operating-with-account", {"destination": "operating", "trust_account_id": account, "caller_scopes": ["payments:write"]}, "validation_failed"),
        ):
            denied = processor_issuance(
                policy,
                module if init_patch.get("destination", "trust") == "trust" else {**module, "approved_scopes": ["payments:write"]},
                {
                    "processor_module_id": "lawpay",
                    "initialization_id": "018f1a00-7b3c-7d4e-8a11-121212121212",
                    "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-131313131313",
                    "matter_id": grant["matter_id"],
                    "contact_id": grant["contact_id"],
                    "amount_cents": 4000,
                    "destination": "trust",
                    "trust_accounts": accounts,
                    "firm_id": "firm-1",
                    "caller_scopes": ["payments:write", "trust:write"],
                    "expires_at": "2026-11-07T18:00:00Z",
                    **init_patch,
                },
            )
            self.assertEqual(denied["result"], expected, label)
            self.assertIsNone(denied["grant"], label)
            self.assertFalse(denied["payment_mutated"], label)
            self.assertFalse(denied["ledger_mutated"], label)
        second = processor_issuance(
            policy,
            module,
            {
                "processor_module_id": "lawpay",
                "initialization_id": "018f1a00-7b3c-7d4e-8a11-141414141414",
                "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-151515151515",
                "matter_id": grant["matter_id"],
                "contact_id": grant["contact_id"],
                "amount_cents": 900,
                "destination": "trust",
                "trust_account_id": other,
                "trust_accounts": accounts,
                "firm_id": "firm-1",
                "caller_scopes": ["payments:write", "trust:write"],
                "expires_at": "2026-11-07T18:00:00Z",
            },
        )
        other_store = {"payments": {}, "ledger": [], "idempotency": {}}
        other_call = {**call, "grant": second["grant"], "amount_cents": 900, "trust_account_id": other}
        placed = processor_apply(other_store, policy, create, other_call)
        self.assertEqual(placed["result"], "allowed")
        self.assertEqual(other_store["ledger"][0]["trust_account_id"], other)
        self.assertNotEqual(store["ledger"][0]["trust_account_id"], other_store["ledger"][0]["trust_account_id"])
        from jsonschema import Draft202012Validator

        operating_body = {
            "contact_id": grant["contact_id"],
            "matter_id": grant["matter_id"],
            "amount_cents": 4000,
            "destination": "operating",
            "method": "card",
        }
        trust_body = {**operating_body, "destination": "trust", "trust_account_id": account}
        write = _component_validator(self.spec, "PaymentWrite")
        init_write = _component_validator(self.spec, "ProcessorInitializationWrite")
        write.validate(operating_body)
        write.validate(trust_body)
        init_write.validate({"processor_module_id": "lawpay", "contact_id": grant["contact_id"], "matter_id": grant["matter_id"], "amount_cents": 4000, "destination": "operating"})
        init_write.validate({"processor_module_id": "lawpay", "contact_id": grant["contact_id"], "matter_id": grant["matter_id"], "amount_cents": 4000, "destination": "trust", "trust_account_id": account})
        for bad in ({**operating_body, "trust_account_id": account}, {**operating_body, "destination": "trust"}, {"processor_module_id": "lawpay", "contact_id": grant["contact_id"], "matter_id": grant["matter_id"], "amount_cents": 4000, "destination": "trust"}):
            with self.assertRaises(Exception):
                (init_write if "processor_module_id" in bad else write).validate(bad)

    def test_custom_fields_are_written_with_the_record(self):
        from jsonschema import Draft202012Validator

        area = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00011"
        other_area = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00012"
        contact_id = "018f1a00-7b3c-7d4e-8a11-222222222222"
        veteran = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00021"
        matter_pick = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00022"
        time_note = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00023"
        expense_link = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00024"
        retired = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00025"
        definitions = [
            {"id": veteran, "key": "veteran", "record_type": "contact", "field_type": "boolean", "required": True, "practice_area_ids": [], "retired": False, "options": []},
            {"id": matter_pick, "key": "case_kind", "record_type": "matter", "field_type": "pick_list", "required": True, "practice_area_ids": [area], "retired": False, "options": ["civil", "criminal"]},
            {"id": time_note, "key": "phase", "record_type": "time_entry", "field_type": "text", "required": True, "practice_area_ids": [area], "retired": False, "options": []},
            {"id": expense_link, "key": "vendor", "record_type": "expense", "field_type": "contact", "required": True, "practice_area_ids": [area], "retired": False, "options": []},
            {"id": retired, "key": "old_flag", "record_type": "contact", "field_type": "boolean", "required": False, "practice_area_ids": [], "retired": True, "options": []},
        ]
        bodies = {
            "contact": {"kind": "person", "given_name": "Ada", "custom_fields": [{"definition_id": veteran, "key": "veteran", "boolean": True}]},
            "matter": {"name": "Smith", "practice_area_id": area, "billing_type": "hourly", "opened_on": "2026-11-01", "custom_fields": [{"definition_id": matter_pick, "key": "case_kind", "pick": "civil"}]},
            "time_entry": {"matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333", "worked_on": "2026-11-02", "increments": 2, "description": "Hearing", "activity_type_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaa00031", "custom_fields": [{"definition_id": time_note, "key": "phase", "text": "prep"}]},
            "expense": {"matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333", "incurred_on": "2026-11-03", "description": "Filing", "amount_cents": 2500, "expense_type_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaa00032", "custom_fields": [{"definition_id": expense_link, "key": "vendor", "contact_id": contact_id}]},
        }
        schema_names = {"contact": "ContactWrite", "matter": "MatterWrite", "time_entry": "TimeEntryWrite", "expense": "ExpenseWrite"}
        record_types = {"contact": "contact", "matter": "matter", "time_entry": "time_entry", "expense": "expense"}
        for kind, body in bodies.items():
            _component_validator(self.spec, schema_names[kind]).validate(body)
            self.assertFalse(self.spec["components"]["schemas"][schema_names[kind]].get("additionalProperties", True))
            with self.assertRaises(Exception):
                _component_validator(self.spec, schema_names[kind]).validate({**body, "not_a_field": True})
            self.assertEqual(
                custom_fields_outcome(record_types[kind], body, definitions, practice_area_id=area, visible_contact_ids={contact_id}),
                "allowed",
                kind,
            )
        denied = [
            ("contact", {**bodies["contact"], "custom_fields": []}, "validation_failed"),
            ("contact", {**bodies["contact"], "custom_fields": [{"definition_id": retired, "key": "old_flag", "boolean": False}]}, "validation_failed"),
            ("contact", {**bodies["contact"], "custom_fields": [{"definition_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaa00999", "key": "veteran", "boolean": True}]}, "validation_failed"),
            ("matter", bodies["matter"], "validation_failed"),
            ("matter", {**bodies["matter"], "custom_fields": [{"definition_id": matter_pick, "key": "case_kind", "pick": "family"}]}, "validation_failed"),
            ("expense", bodies["expense"], "not_found"),
            ("time_entry", {**bodies["time_entry"], "custom_fields": [{"definition_id": time_note, "key": "phase", "number": 1}]}, "validation_failed"),
        ]
        for kind, body, expected in denied:
            practice = other_area if kind == "matter" and body is bodies["matter"] else area
            visible = set() if kind == "expense" and expected == "not_found" else {contact_id}
            self.assertEqual(custom_fields_outcome(record_types[kind], body, definitions, practice_area_id=practice, visible_contact_ids=visible), expected, (kind, expected))
        with self.assertRaises(Exception):
            _component_validator(self.spec, "CustomFieldInput").validate({"definition_id": veteran, "key": "veteran", "boolean": True, "text": "no"})
        items = [
            {"record_type": "contact", "body": bodies["contact"], "definitions": definitions, "visible_contact_ids": {contact_id}},
            {"record_type": "matter", "body": {**bodies["matter"], "custom_fields": []}, "definitions": definitions, "practice_area_id": area},
        ]
        written: list = []
        if custom_fields_batch(items) == "allowed":
            written.extend(items)
        self.assertEqual(written, [])
        self.assertEqual(custom_fields_batch(items), "batch_rejected")
        good = [
            {"record_type": record_types[kind], "body": bodies[kind], "definitions": definitions, "practice_area_id": area, "visible_contact_ids": {contact_id}}
            for kind in bodies
        ]
        written = []
        self.assertEqual(custom_fields_batch(good), "allowed")
        if custom_fields_batch(good) == "allowed":
            written.extend(good)
        self.assertEqual(len(written), 4)
        for operation_id, schema_name in (
            ("bulkContact", "ContactWrite"),
            ("bulkMatter", "MatterWrite"),
            ("bulkTimeEntry", "TimeEntryWrite"),
            ("bulkExpense", "ExpenseWrite"),
        ):
            operation = _operation(self.spec, operation_id)
            item_schema = operation["requestBody"]["content"]["application/json"]["schema"]["properties"]["items"]["items"]["$ref"]
            self.assertTrue(item_schema.endswith("/" + schema_name), operation_id)
            self.assertIn("custom_fields", self.spec["components"]["schemas"][schema_name]["properties"])

    def test_mcp_forwards_context_and_task_grants(self):
        from contract.build import render_mcp

        catalog = render_mcp(self.spec)
        get_file = next(tool for tool in catalog["tools"] if tool["name"] == "getFile")
        create = next(tool for tool in catalog["tools"] if tool["name"] == "createPayment")
        for tool in (get_file, create):
            self.assertEqual(tool["transfer"]["credentials"]["context_token"]["header"], "X-Togavsu-Context-Token")
            self.assertEqual(tool["transfer"]["credentials"]["task_authorization"]["header"], "X-Togavsu-Task-Authorization")
            self.assertFalse(tool["transfer"]["credentials"]["context_token"]["roles_from_arguments"])
            self.assertFalse(tool["transfer"]["credentials"]["task_authorization"]["roles_from_arguments"])
            self.assertNotIn("Authorization", tool["transfer"].get("headers") or [])
            self.assertNotIn("Host", tool["transfer"].get("headers") or [])
            self.assertIn("context_token", tool["transfer"]["arguments"])
            self.assertIn("task_authorization", tool["transfer"]["arguments"])
        self.assertEqual(mcp_credential_rejection({"roles": ["admin"]}), "forbidden")
        self.assertEqual(mcp_credential_rejection({"Authorization": "Bearer stolen"}), "forbidden")
        self.assertEqual(mcp_credential_rejection({"headers": {"Host": "evil.example"}}), "forbidden")
        self.assertIsNone(mcp_credential_rejection({"context_token": "ctx", "task_authorization": {"module_id": "malware"}}))
        malware = self.spec["x-togavsu-module-delegation"]["noninteractive"]["malware_scan"]
        module = {
            "registered": True,
            "subscriptions": ["file.version_created"],
            "module_id": "malware",
            "recipient_id": "malware",
            "file_id": "file-1",
            "version_id": "ver-1",
            "matter_id": "matter-1",
        }
        grant = _issue_malware_grant(malware, module)
        operation = _operation(self.spec, "getFile")

        def mcp_get(arguments, **overrides):
            self.assertIsNone(mcp_credential_rejection(arguments))
            task = arguments.get("task_authorization")
            if isinstance(task, dict) or isinstance(task, bool):
                return "forbidden"
            http_call = {
                "registered": True,
                "subscriptions": ["file.version_created"],
                "caller_module_id": overrides.get("caller_module_id", "malware"),
                "module_scopes": ["files:read", "files:write"],
                "grant": overrides.get("claims"),
                "matter_id": "matter-1",
                "file_id": "file-1",
                "version_id": "ver-1",
                "viewer_present": "context_token" in arguments and task is None,
                "operation": "getFile",
                "field": "name",
            }
            return _malware_call(malware, operation, http_call)

        allowed_args = {"context_token": "ctx", "task_authorization": "scan-token", "path": {"file_id": "file-1"}, "query": {}}
        self.assertEqual(mcp_get(allowed_args, claims=grant), "allowed")
        self.assertEqual(mcp_get({"context_token": "ctx", "path": {}, "query": {}}), "forbidden")
        self.assertEqual(mcp_get({"task_authorization": grant}), "forbidden")
        self.assertEqual(mcp_get({"task_authorization": "scan-token"}, claims={**grant, "expired": True}), "forbidden")
        self.assertEqual(mcp_get({"task_authorization": "scan-token"}, claims=grant, caller_module_id="other-module"), "forbidden")
        self.assertEqual(_malware_call(malware, operation, {"registered": True, "subscriptions": ["file.version_created"], "caller_module_id": "malware", "module_scopes": ["files:read", "files:write"], "grant": grant, "matter_id": "matter-1", "file_id": "file-1", "version_id": "ver-1", "operation": "getFile", "field": "name"}), mcp_get(allowed_args, claims=grant))
        payment_policy = self.spec["x-togavsu-module-delegation"]["noninteractive"]["payment_processor"]
        pay_module = {"registered": True, "module_id": "lawpay", "recipient_id": "lawpay", "approved_scopes": ["payments:write"]}
        pay_grant = processor_issuance(
            payment_policy,
            pay_module,
            {
                "processor_module_id": "lawpay",
                "initialization_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaaa",
                "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbbb",
                "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
                "contact_id": "018f1a00-7b3c-7d4e-8a11-222222222222",
                "invoice_id": None,
                "amount_cents": 2500,
                "destination": "operating",
                "trust_account_id": None,
                "caller_scopes": ["payments:write"],
                "expires_at": "2026-11-07T18:00:00Z",
            },
        )["grant"]
        pay_op = _operation(self.spec, "createPayment")
        http_pay = {
            "operation": "createPayment",
            "registered": True,
            "caller_module_id": "lawpay",
            "module_scopes": ["payments:write"],
            "grant": pay_grant,
            "matter_id": pay_grant["matter_id"],
            "contact_id": pay_grant["contact_id"],
            "invoice_id": None,
            "amount_cents": 2500,
            "destination": "operating",
            "status": "pending",
        }
        self.assertEqual(processor_callback(payment_policy, pay_op, http_pay), "allowed")
        self.assertEqual(processor_callback(payment_policy, pay_op, {**http_pay, "grant": None}), "forbidden")
        self.assertEqual(processor_callback(payment_policy, pay_op, {**http_pay, "grant": {**pay_grant, "expired": True}}), "forbidden")
        self.assertEqual(processor_callback(payment_policy, pay_op, {**http_pay, "caller_module_id": "other"}), "forbidden")

    def test_invoice_pdf_confirmation_is_a_real_input(self):
        operation = _operation(self.spec, "getInvoicePdf")
        confirm = operation["x-togavsu-access"]["quarantine_confirmation_input"]
        self.assertEqual(confirm, {"in": "query", "name": "confirm_quarantine"})
        names = [parameter.get("name") for parameter in operation["parameters"]]
        self.assertIn("confirm_quarantine", names)
        catalog_tool = next(tool for tool in __import__("contract.build", fromlist=["render_mcp"]).render_mcp(self.spec)["tools"] if tool["name"] == "getInvoicePdf")
        self.assertIn("query", catalog_tool["transfer"]["arguments"])
        own = {"role": "client", "shared": True, "owner": "self", "attorney_only": False, "on_wall": True, "quarantined": True}
        self.assertEqual(file_download_decision(operation, {**own, "confirm": None}), "quarantine_confirmation_required")
        self.assertEqual(file_download_decision(operation, {**own, "confirm": False}), "quarantine_confirmation_required")
        self.assertEqual(file_download_decision(operation, {**own, "confirm": True}), "highlighted")
        self.assertEqual(file_download_decision(operation, {**own, "confirm": True, "shared": False}), "not_found")
        self.assertEqual(file_download_decision(operation, {**own, "confirm": True, "owner": "other_client"}), "not_found")
        self.assertEqual(file_download_decision(operation, {**own, "role": "staff", "confirm": True, "attorney_only": True}), "not_found")
        self.assertEqual(file_download_decision(operation, {**own, "quarantined": False}), "highlighted")
        for _method, _path, download in _file_byte_downloads(self.spec):
            self.assertIn("quarantine_confirmation_input", download["x-togavsu-access"], download["operationId"])

    def test_client_context_reads_follow_published_access(self):
        policy = _operation(self.spec, "createModuleContextToken")["x-togavsu-access"]["client_context"]
        self.assertFalse(policy["grants_admin"])
        self.assertFalse(policy["firmwide_bypass"])
        self.assertEqual(policy["published_reads"]["role"], "client")
        invoice = _operation(self.spec, "getInvoice")
        self.assertEqual(invoice["x-togavsu-access"]["client_fields"], list(self.spec["components"]["schemas"]["InvoiceClient"]["properties"]))
        history = _operation(self.spec, "historyInvoice")
        self.assertEqual(history["x-togavsu-access"]["client_fields"], list(self.spec["components"]["schemas"]["InvoiceClientHistoryEntry"]["properties"]))
        calls = [
            ("listFile", {"matter": "own_shared", "shared": True, "owner": "self", "attorney_only": False, "on_wall": True}, "allowed"),
            ("getFile", {"matter": "own_shared", "shared": True, "owner": "self", "attorney_only": False, "on_wall": True}, "allowed"),
            ("downloadFileVersion", {"matter": "own_shared", "shared": True, "owner": "self", "attorney_only": False, "on_wall": True}, "allowed"),
            ("getInvoice", {"matter": "own_shared", "owner": "self", "on_wall": True}, "allowed"),
            ("getInvoicePdf", {"matter": "own_shared", "shared": True, "owner": "self", "attorney_only": False, "on_wall": True}, "allowed"),
            ("listInvoice", {"matter": "own_shared", "owner": "self", "on_wall": True}, "allowed"),
            ("getFile", {"matter": "other_client", "shared": True, "owner": "other_client", "on_wall": True}, "not_found"),
            ("getInvoice", {"matter": "other_client", "owner": "other_client", "on_wall": True}, "not_found"),
            ("getInvoicePdf", {"matter": "other_client", "shared": True, "owner": "other_client", "on_wall": True}, "not_found"),
            ("getFile", {"matter": "own_unshared", "shared": False, "owner": "self", "on_wall": True}, "not_found"),
            ("downloadFileVersion", {"matter": "own_shared", "shared": False, "owner": "self", "attorney_only": False, "on_wall": True}, "not_found"),
            ("getFile", {"matter": "own_shared", "shared": True, "owner": "self", "attorney_only": True, "on_wall": True}, "not_found"),
            ("listFile", {"matter": "own_shared", "shared": True, "owner": "self", "matter_walled": True, "on_wall": False}, "not_found"),
            ("getInvoice", {"matter": "own_shared", "owner": "self", "matter_walled": True, "on_wall": False}, "not_found"),
            ("getFileUpload", {"owner": "self", "matter": "own_shared"}, "allowed"),
            ("getFileUpload", {"owner": "other_client", "matter": "own_shared"}, "not_found"),
            ("updateMe", {"owner": "self", "matter": "own_shared"}, "forbidden"),
            ("listFile", {"matter": "own_shared", "shared": True, "owner": "self", "on_wall": True, "module_scopes": ["invoices:read"]}, "scope_denied"),
        ]
        for operation_id, call, expected in calls:
            self.assertEqual(_client_portal_result(self.spec, policy, operation_id, call), expected, (operation_id, call))

    def test_published_versions_emit_after_commit(self):
        found = {}
        publishers = []
        for _method, _path, operation in _ops(self.spec):
            publication = operation["x-togavsu-access"].get("version_publication")
            if not publication:
                continue
            publishers.append(operation)
            self.assertIn("file.version_created", operation["x-togavsu-events"], operation["operationId"])
            self.assertEqual(publication["emit_after"], "commit", operation["operationId"])
            self.assertEqual(publication["on_failure"], "no_event_no_grant", operation["operationId"])
            self.assertFalse(publication["staged_bytes_emit"], operation["operationId"])
            self.assertEqual(published_version_events(operation, committed=False, version_count=3), [], operation["operationId"])
            self.assertEqual(
                published_version_events(operation, committed=True, version_count=2),
                ["file.version_created", "file.version_created"],
                operation["operationId"],
            )
            for path_name in publication["paths"]:
                found[path_name] = operation["operationId"]
        self.assertGreaterEqual(len(publishers), 6)
        self.assertEqual(
            found,
            {
                "bulk": "bulkFile",
                "import": "bulkFile",
                "conversion": "convertFile",
                "upload": "publishFile",
                "new_version": "addFileVersion",
                "copy": "restoreFileVersion",
                "editor": "runEditor",
            },
        )
        for operation_id in ("completeFileUpload", "putFileChunk", "createFileUpload", "updateFileMetadata"):
            operation = _operation(self.spec, operation_id)
            self.assertNotIn("file.version_created", operation["x-togavsu-events"], operation_id)
            self.assertNotIn("version_publication", operation["x-togavsu-access"], operation_id)
            self.assertEqual(published_version_events(operation, committed=True, version_count=1), [], operation_id)

    def test_malware_grant_responses_match_projection_schemas(self):
        from jsonschema import Draft202012Validator

        policy = self.spec["x-togavsu-module-delegation"]["noninteractive"]["malware_scan"]
        private = {"origin", "created_by", "storage_backend_id", "saved_by"}
        file_schema = self.spec["components"]["schemas"]["FileGrantView"]
        version_schema = self.spec["components"]["schemas"]["FileVersionGrantView"]
        self.assertTrue(set(file_schema["required"]) <= set(policy["metadata_fields"]))
        self.assertTrue(set(version_schema["required"]) <= set(policy["metadata_fields"]))
        self.assertFalse(private & set(file_schema["properties"]))
        self.assertFalse(private & set(version_schema["properties"]))
        self.assertFalse(file_schema["additionalProperties"])
        self.assertFalse(version_schema["additionalProperties"])
        file_body = {"name": "smith-motion.pdf", "extension": "pdf"}
        version_body = {
            "media_type": "application/pdf",
            "size_bytes": 1200,
            "sha256": "ab" * 32,
            "quarantined": False,
        }
        page_validator = _component_validator(self.spec, "FileVersionGrantPage")
        Draft202012Validator(file_schema).validate(file_body)
        Draft202012Validator(version_schema).validate(version_body)
        page_validator.validate({"projection": "grant", "data": [version_body], "page": {"limit": 50, "next_cursor": None}})
        page_validator.validate({"projection": "grant", "data": [], "page": {"limit": 50, "next_cursor": None}})
        for extra in ({"origin": "firm"}, {"created_by": "018f1a00-7b3c-7d4e-8a11-555555555555"}):
            with self.assertRaises(Exception):
                Draft202012Validator(file_schema).validate({**file_body, **extra})
        for extra in (
            {"storage_backend_id": "018f1a00-7b3c-7d4e-8a11-555555555555"},
            {"saved_by": "018f1a00-7b3c-7d4e-8a11-555555555555"},
            {"created_by": "018f1a00-7b3c-7d4e-8a11-555555555555"},
            {"note": "hidden"},
        ):
            with self.assertRaises(Exception):
                Draft202012Validator(version_schema).validate({**version_body, **extra})
        projections = {
            "getFile": ("FileGrantView", file_body),
            "listFileVersions": ("FileVersionGrantPage", {"projection": "grant", "data": [version_body], "page": {"limit": 50, "next_cursor": None}}),
            "quarantineFileVersion": ("FileVersionGrantView", version_body),
        }
        for operation_id, (schema_name, body) in projections.items():
            operation = _operation(self.spec, operation_id)
            projection = operation["x-togavsu-access"]["response_projection"]
            self.assertEqual(projection["schema"], schema_name, operation_id)
            self.assertEqual(projection["when"], "task_authorization", operation_id)
            self.assertTrue(set(projection["fields"]) <= set(policy["metadata_fields"]), operation_id)
            self.assertFalse(private & set(projection["fields"]), operation_id)
            _component_validator(self.spec, schema_name).validate(body)
        download = _operation(self.spec, "downloadFileVersion")
        self.assertEqual(download["x-togavsu-mcp"]["transfer"]["result"]["encoding"], "base64")
        self.assertNotIn("response_projection", download["x-togavsu-access"])
        versions = _operation(self.spec, "listFileVersions")
        response = versions["responses"]["200"]["content"]["application/json"]["schema"]
        empty_page = {"data": [], "page": {"limit": 50, "next_cursor": None}}
        _schema_validator(self.spec, response).validate(empty_page)
        _schema_validator(self.spec, response).validate({"projection": "grant", **empty_page})
        with self.assertRaises(Exception):
            page_validator.validate(empty_page)

    def test_webhook_delivery_is_authorized_before_serialization(self):
        webhook = self.spec["webhooks"]["moduleEvent"]["post"]
        order = webhook["x-togavsu-delivery-authorization"]["order"]
        self.assertEqual(order[-1], "serialize")
        self.assertLess(order.index("scope"), order.index("serialize"))
        self.assertLess(order.index("matter_wall"), order.index("serialize"))
        self.assertLess(order.index("attorney_only"), order.index("serialize"))
        event = {
            "id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeeee",
            "type": "matter.created",
            "occurred_at": "2026-11-07T18:00:00Z",
            "actor_kind": "staff",
            "record_type": "matter",
            "record_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
            "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
            "summary": "Smith opened",
            "matter_walled": False,
        }
        subscriber = {"installed": True, "registered": True, "approved_scopes": ["matters:read"], "subscriptions": ["matter.created"], "on_wall": False, "attorney_only": False}
        delivered = webhook_envelope(subscriber, event)
        self.assertEqual(delivered["record_id"], event["record_id"])
        self.assertEqual(delivered["summary"], "Smith opened")
        _component_validator(self.spec, "WebhookEvent").validate(delivered)
        for label, module, sample in (
            ("no-scope", {**subscriber, "approved_scopes": ["files:read"]}, event),
            ("not-subscribed", {**subscriber, "subscriptions": ["invoice.sent"]}, event),
            ("not-installed", {**subscriber, "installed": False}, event),
            ("wall", subscriber, {**event, "matter_walled": True}),
            ("conflict", {**subscriber, "approved_scopes": ["conflicts:read"], "subscriptions": ["conflict_review.decided"]}, {"id": event["id"], "type": "conflict_review.decided", "occurred_at": event["occurred_at"], "record_type": "conflict_review", "record_id": "018f1a00-7b3c-7d4e-8a11-999999999999", "matter_id": event["matter_id"], "summary": "attorney reason", "attorney_only_record": True}),
        ):
            self.assertIsNone(webhook_envelope(module, sample), label)
        on_wall = webhook_envelope({**subscriber, "on_wall": True, "matter_id": event["matter_id"]}, {**event, "matter_walled": True})
        self.assertEqual(on_wall["record_id"], event["record_id"])
        redacted = webhook_envelope(subscriber, {**event, "summary_attorney_only": True, "summary": "secret reason"})
        self.assertEqual(redacted["summary"], {"redacted": True})
        self.assertNotIn("secret reason", str(redacted))
        _component_validator(self.spec, "WebhookEvent").validate(redacted)
        self.assertIsNone(webhook_envelope(subscriber, {**event, "summary": None}))
        attorney_file = {**event, "type": "file.created", "record_type": "file", "attorney_only": True, "summary": "privileged"}
        file_module = {**subscriber, "approved_scopes": ["files:read"], "subscriptions": ["file.created"]}
        self.assertIsNone(webhook_envelope(file_module, attorney_file))
        self.assertIsNone(webhook_envelope({k: v for k, v in subscriber.items() if k != "registered"}, event))
        self.assertIsNone(webhook_envelope({**subscriber, "approved_scopes": ["events:read"], "subscriptions": ["custom_field.updated"]}, {**event, "type": "custom_field.updated", "record_type": None, "summary": "contact field"}))
        processor = webhook_envelope(
            {**subscriber, "approved_scopes": ["payments:write"], "subscriptions": ["payment.processor_authorized"]},
            {**event, "type": "payment.processor_authorized", "record_type": "payment"},
        )
        self.assertEqual(processor["record_id"], event["record_id"])
        self.assertIsNone(
            webhook_envelope(
                {**subscriber, "approved_scopes": ["payments:read"], "subscriptions": ["payment.processor_authorized"]},
                {**event, "type": "payment.processor_authorized", "record_type": "payment"},
            )
        )
        secret = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaaa"
        for label, scope, event_type, grant_field, grant in (
            ("task", "files:read", "file.version_created", "task_authorization", {"module_id": "owner-mod", "file_id": secret, "version_id": secret}),
            ("processor", "payments:write", "payment.processor_authorized", "processor_authorization", {"module_id": "owner-mod", "payment_id": secret, "amount_cents": 1500}),
            ("background", "files:write", "file.background_authorized", "background_file_authorization", {"module_id": "owner-mod", "matter_id": secret, "upload_ids": []}),
        ):
            owner = {"installed": True, "registered": True, "module_id": "owner-mod", "approved_scopes": [scope], "subscriptions": [event_type], "on_wall": True, "attorney_only": False}
            other = {**owner, "module_id": "other-mod"}
            sample = {**event, "type": event_type, "record_type": "file", grant_field: grant}
            delivered_grant = webhook_envelope(owner, sample)
            self.assertIsNotNone(delivered_grant, label)
            self.assertEqual(delivered_grant[grant_field]["module_id"], "owner-mod", label)
            denied_grant = webhook_envelope(other, sample)
            self.assertIsNone(denied_grant, label)
            self.assertNotIn(secret, str(denied_grant), label)
            unknown = webhook_envelope(owner, {**sample, "capability": {"module_id": "owner-mod", "token": "hidden-token"}})
            self.assertIsNone(unknown, label)
            self.assertIsNone(webhook_envelope(owner, {**sample, grant_field: {"file_id": secret}}), label)

    def test_every_emitted_event_uses_its_resource_read_scope(self):
        emitted = sorted({event for _method, _path, operation in _ops(self.spec) for event in operation.get("x-togavsu-events") or []})
        self.assertGreater(len(emitted), 100)
        published = self.spec["x-togavsu-event-scopes"]
        self.assertEqual(published["calendar_event.created"], ["calendar:read"])
        self.assertEqual(published["trust_account.created"], ["trust:read"])
        self.assertEqual(published["doc_tender.published"], ["doc_tender:read"])
        self.assertEqual(published["doc_tender.exception_opened"], ["doc_tender:read"])
        background = self.spec["x-togavsu-background-events"]
        for event_type, scopes in (
            ("matter.created", ["matters:read"]),
            ("time_entry.updated", ["time:read"]),
            ("trust.flag_raised", ["trust:read"]),
            ("file.version_created", ["files:read"]),
            ("doc_tender.published", ["doc_tender:read"]),
            ("doc_tender.exception_opened", ["doc_tender:read"]),
        ):
            self.assertEqual(published[event_type], scopes, event_type)
            self.assertEqual(background[event_type]["scopes"], scopes, event_type)
            self.assertTrue(background[event_type]["matter_wall"], event_type)
            self.assertEqual(background[event_type]["attorney_only"], "redacted", event_type)
        self.assertNotIn("doc_tender.exception_opened", {event for _method, _path, operation in _ops(self.spec) for event in operation.get("x-togavsu-events") or []})
        self.assertNotIn("events:read", published["calendar_event.created"])
        self.assertNotIn("not.a.real.event", published)
        base = {
            "id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeeee",
            "occurred_at": "2026-11-07T18:00:00Z",
            "actor_kind": "staff",
            "record_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
            "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
            "summary": "ordinary",
            "matter_walled": False,
        }
        catalog = [scope for scopes in published.values() for scope in scopes]
        for event_type in emitted:
            if event_type.startswith("custom_field."):
                record_type = "contact"
            elif event_type.startswith("conflict_record"):
                record_type = "conflict_record"
            else:
                record_type = "matter"
            scopes = event_read_scopes(event_type, record_type if event_type.startswith("custom_field.") else None)
            if event_type.startswith("custom_field."):
                self.assertNotIn(event_type, published, event_type)
                self.assertEqual(scopes, ["contacts:read"], event_type)
            else:
                self.assertEqual(scopes, published.get(event_type, []), event_type)
            sample = {**base, "type": event_type, "record_type": record_type}
            owner_rule = self.spec.get("x-togavsu-event-owners", {}).get(event_type)
            owner_id = "018f1a00-7b3c-7d4e-8a11-111111111111"
            if owner_rule:
                subject = {"recipient_user_id": owner_id} if owner_rule["binding"] == "recipient" else {"owner_user_id": owner_id}
                if event_type.endswith(".deleted"):
                    sample["predelete"] = subject
                    sample["deleted"] = True
                    sample["recipient_user_id"] = "018f1a00-7b3c-7d4e-8a11-999999999999"
                else:
                    sample["context"] = subject
            if event_type == "trust.flag_raised":
                sample.update(
                    {
                        "client_id": base["matter_id"],
                        "trust_account_id": base["record_id"],
                        "balance_cents": -1,
                        "trust_entry_id": base["record_id"],
                        "balance_scope": "matter",
                    }
                )
            events_only = {"installed": True, "registered": True, "approved_scopes": ["events:read"], "subscriptions": [event_type], "on_wall": True, "attorney_only": False}
            if "events:read" not in scopes:
                self.assertIsNone(webhook_envelope(events_only, sample), event_type)
            if not scopes:
                everything = {**events_only, "approved_scopes": sorted(set(catalog + ["events:read"])), "attorney_only": True}
                self.assertIsNone(webhook_envelope(everything, sample), event_type)
                continue
            holder = {**events_only, "approved_scopes": [scopes[0]], "attorney_only": record_type == "conflict_record"}
            if owner_rule:
                holder["principal_user_id"] = owner_id
                holder["principal_role"] = "staff"
            delivered = webhook_envelope(holder, sample)
            if record_type == "conflict_record":
                self.assertIsNone(webhook_envelope({**holder, "attorney_only": False}, sample), event_type)
            self.assertIsNotNone(delivered, event_type)
            self.assertEqual(delivered["record_id"], sample["record_id"])
            self.assertNotEqual(delivered["summary"], None)
            _component_validator(self.spec, "WebhookEvent").validate(delivered)
        unknown = {**base, "type": "not.a.real.event", "record_type": "matter"}
        self.assertEqual(event_read_scopes("not.a.real.event"), [])
        self.assertIsNone(webhook_envelope({**events_only, "subscriptions": ["not.a.real.event"], "approved_scopes": catalog + ["events:read"]}, unknown))
        opened = {**base, "type": "doc_tender.exception_opened", "record_type": "file", "summary": "low confidence"}
        subscriber = {"installed": True, "registered": True, "module_id": "tender", "approved_scopes": ["doc_tender:read"], "subscriptions": ["doc_tender.exception_opened"], "on_wall": True, "attorney_only": False}
        delivered_exception = webhook_envelope(subscriber, opened)
        self.assertIsNotNone(delivered_exception)
        self.assertEqual(delivered_exception["type"], "doc_tender.exception_opened")
        self.assertIsNone(webhook_envelope({**subscriber, "approved_scopes": ["events:read"]}, opened))
        self.assertIsNone(webhook_envelope({**subscriber, "on_wall": False}, {**opened, "matter_walled": True}))
        self.assertIsNone(webhook_envelope(subscriber, {**opened, "attorney_only": True}))
        redacted_exception = webhook_envelope({**subscriber, "attorney_only": True}, {**opened, "summary_attorney_only": True, "attorney_only": True})
        self.assertEqual(redacted_exception["summary"], {"redacted": True})
        self.assertEqual(event_read_scopes("not.a.real.event"), [])
        self.assertEqual(event_read_scopes("custom_field.updated", None), [])
        self.assertEqual(event_read_scopes("custom_field.updated", "time_entry"), ["time:read"])
        secret = b"top-secret"
        payload = b'{"type":"matter.created"}'
        signature = webhook_signature(secret, payload)
        self.assertEqual(signature, "sha256=e9464e954a1b5f9acf9da3a55d8f0a31d00090a745fa83e3591fe7d2afcbae88")
        self.assertTrue(webhook_signature_matches(secret, payload, signature))
        self.assertFalse(webhook_signature_matches(secret, payload + b" ", signature))
        self.assertFalse(webhook_signature_matches(b"other-secret", payload, signature))
        import hashlib

        plain = "sha256=" + hashlib.sha256(payload).hexdigest()
        self.assertNotEqual(plain, signature)
        self.assertFalse(webhook_signature_matches(secret, payload, plain))
        self.assertIn("HMAC-SHA256", self.spec["components"]["schemas"]["WebhookEvent"]["description"])
        self.assertIn("keyed", self.spec["webhooks"]["moduleEvent"]["post"]["description"])

    def test_include_views_cover_extensible_core_records(self):
        expected = {
            "User": "users:read",
            "Contact": "contacts:read",
            "Matter": "matters:read",
            "MatterParty": "matters:read",
            "Note": "notes:read",
            "FeeAgreement": "matters:read",
            "TimeEntry": "time:read",
            "Expense": "expenses:read",
            "Invoice": "invoices:read",
            "Payment": "payments:read",
            "CalendarEvent": "calendar:read",
            "Deadline": "deadlines:read",
            "File": "files:read",
            "ConflictRecord": "conflicts:read",
            "TrustAccount": "trust:read",
        }
        self.assertEqual(EXTENSIBLE_RECORDS, expected)
        seen = set()
        for _method, _path, operation in _ops(self.spec):
            if _method != "get":
                continue
            matched = _response_record_schemas(operation) & set(expected)
            if not matched:
                continue
            seen.update(matched)
            schema_name = sorted(matched)[0]
            names = [_param_name(parameter) for parameter in operation.get("parameters") or []]
            self.assertIn("include", names, operation["operationId"])
            self.assertEqual(operation["x-togavsu-access"]["include_scope_pattern"], "views:{module_id}.{view}:read", operation["operationId"])
            canonical = _operation(self.spec, f"list{schema_name}")
            self.assertEqual(canonical["x-togavsu-access"]["scopes"], [expected[schema_name]], schema_name)
            self.assertEqual(_operation(self.spec, f"get{schema_name}")["x-togavsu-access"]["scopes"], [expected[schema_name]], schema_name)
            schema = self.spec["components"]["schemas"][schema_name]
            self.assertIn("included", schema["properties"], schema_name)
            self.assertNotIn("included", schema.get("required", []), schema_name)
            arguments = operation["x-togavsu-mcp"]["transfer"]["arguments"]
            self.assertIn("query", arguments, operation["operationId"])
            self.assertNotIn("roles", arguments, operation["operationId"])
        self.assertEqual(seen, set(expected))
        users = _operation(self.spec, "listUser")
        approved = ["users:read", "views:clock.targets:read"]
        self.assertEqual(include_views_decision(users, {"role": "admin", "approved_scopes": approved, "include": ["clock.targets"]}), "allowed")
        self.assertEqual(include_views_decision(users, {"role": "staff", "approved_scopes": approved, "include": ["clock.targets"]}), "forbidden")
        self.assertEqual(include_views_decision(users, {"role": "admin", "approved_scopes": ["users:read"], "include": ["clock.targets"]}), "forbidden")
        self.assertEqual(include_views_decision(users, {"role": "admin", "approved_scopes": ["users:read"], "include": ["not-a-view"]}), "validation_failed")
        self.assertEqual(
            include_views_decision(users, {"role": "admin", "approved_scopes": ["users:read"], "caller_module_id": "clock", "include": ["other.targets"]}),
            "forbidden",
        )
        self.assertEqual(
            include_views_decision(
                users,
                {"role": "admin", "approved_scopes": ["users:read", "views:other.targets:read"], "caller_module_id": "clock", "include": ["other.targets"]},
            ),
            "allowed",
        )
        matter = _operation(self.spec, "getMatter")
        self.assertEqual(
            include_views_decision(matter, {"role": "staff", "approved_scopes": ["matters:read", "views:clock.targets:read"], "include": ["clock.targets"], "matter_walled": True, "on_wall": False}),
            "not_found",
        )
        self.assertEqual(matter["x-togavsu-access"]["extension_include"]["declared_table"], "owner_module_or_viewer")
        self.assertEqual(matter["x-togavsu-access"]["extension_include"]["other_module_unpublished_table"], "forbidden")
        extension = {
            "role": "staff",
            "approved_scopes": ["matters:read"],
            "include": ["matter-clock.matter_targets"],
            "extensions": ["matter-clock.matter_targets"],
        }
        self.assertEqual(include_views_decision(matter, extension), "allowed")
        self.assertEqual(include_views_decision(matter, {**extension, "caller_module_id": "matter-clock"}), "allowed")
        self.assertEqual(include_views_decision(matter, {**extension, "caller_module_id": "other-module"}), "forbidden")
        self.assertEqual(
            include_views_decision(matter, {**extension, "caller_module_id": "other-module", "approved_scopes": ["matters:read", "views:matter-clock.matter_targets:read"]}),
            "forbidden",
        )
        self.assertEqual(
            include_views_decision(
                matter,
                {
                    "role": "staff",
                    "caller_module_id": "other-module",
                    "approved_scopes": ["matters:read", "views:matter-clock.targets:read"],
                    "include": ["matter-clock.targets"],
                    "published_views": ["matter-clock.targets"],
                },
            ),
            "allowed",
        )
        self.assertEqual(
            include_views_decision(
                matter,
                {"role": "staff", "caller_module_id": "other-module", "approved_scopes": ["matters:read"], "include": ["matter-clock.targets"], "published_views": ["matter-clock.targets"]},
            ),
            "forbidden",
        )
        self.assertEqual(include_views_decision(matter, {**extension, "matter_walled": True, "on_wall": False}), "not_found")
        conflict = _operation(self.spec, "getConflictRecord")
        staff_conflict = {"role": "staff", "approved_scopes": ["conflicts:read", "views:clock.targets:read"], "include": ["clock.targets"]}
        self.assertEqual(include_views_decision(conflict, staff_conflict), "allowed")
        conflict_row = project_record(conflict, staff_conflict, {"id": "row-1", "reason": "prior client", "name": "Ada"})
        self.assertEqual(conflict_row["result"], "allowed")
        self.assertEqual(conflict_row["record"]["reason"], {"redacted": True})
        self.assertEqual(conflict_row["record"]["name"], "Ada")
        self.assertEqual(
            include_views_decision(conflict, {"role": "attorney", "approved_scopes": ["conflicts:read", "views:clock.targets:read"], "include": ["clock.targets"]}),
            "allowed",
        )
        contact = _operation(self.spec, "getContact")
        staff_contact = {"role": "staff", "approved_scopes": ["contacts:read"], "include": []}
        self.assertEqual(include_views_decision(contact, staff_contact), "allowed")
        shown = project_record(contact, staff_contact, {"id": "contact-1", "full_name": "Ada Lovelace", "identifier": "123-45-6789"})
        self.assertEqual(shown["result"], "allowed")
        self.assertEqual(shown["record"]["full_name"], "Ada Lovelace")
        self.assertEqual(shown["record"]["identifier"], {"redacted": True})
        self.assertNotIn("123-45-6789", str(shown))
        attorney = project_record(contact, {"role": "attorney"}, {"id": "contact-1", "full_name": "Ada Lovelace", "identifier": "123-45-6789"})
        self.assertEqual(attorney["record"]["identifier"], "123-45-6789")
        hidden_file = project_record(
            _operation(self.spec, "getFile"),
            {"role": "staff", "on_wall": True},
            {"id": "file-1", "name": "memo.pdf", "attorney_only": True},
        )
        self.assertEqual(hidden_file["result"], "not_found")
        self.assertIsNone(hidden_file["record"])

    def test_notifications_are_limited_to_the_recipient(self):
        for operation_id in ("listNotification", "getNotification", "markNotificationRead", "markAllNotificationsRead", "deleteNotification"):
            access = _operation(self.spec, operation_id)["x-togavsu-access"]
            self.assertEqual(access["owner"], "self", operation_id)
            self.assertEqual(access["recipient"], "self", operation_id)
            self.assertFalse(access["admin_sees_others"], operation_id)
            self.assertEqual(access["filter_before"], ["pagination", "unread_count"], operation_id)
            self.assertNotEqual(access.get("client"), "own", operation_id)
        create = _operation(self.spec, "createNotification")
        self.assertTrue(create["x-togavsu-access"]["creation_separate"])
        self.assertNotEqual(create["x-togavsu-access"].get("owner"), "self")
        self.assertEqual(notification_recipient_decision(create, {"role": "staff", "user_id": "018f1a00-7b3c-7d4e-8a11-222222222222"}), "allowed")
        caller = "018f1a00-7b3c-7d4e-8a11-111111111111"
        other = "018f1a00-7b3c-7d4e-8a11-222222222222"
        for operation_id in ("listNotification", "getNotification", "markNotificationRead", "deleteNotification"):
            operation = _operation(self.spec, operation_id)
            for role in ("admin", "attorney", "staff", "accountant", "client"):
                self.assertEqual(
                    notification_recipient_decision(operation, {"role": role, "owner": "self", "caller_user_id": caller, "recipient_user_id": caller}),
                    "allowed",
                    (operation_id, role),
                )
                self.assertEqual(
                    notification_recipient_decision(operation, {"role": role, "owner": "other_user", "caller_user_id": caller, "recipient_user_id": other}),
                    "not_found",
                    (operation_id, role),
                )
        rows = [
            {"id": "own-unread", "user_id": caller, "read_at": None},
            {"id": "own-read", "user_id": caller, "read_at": "2026-11-07T18:00:00Z"},
            {"id": "other-unread", "user_id": other, "read_at": None},
        ]
        page = notification_page(_operation(self.spec, "listNotification"), {"role": "admin", "user_id": caller, "caller_user_id": caller}, rows, 1)
        self.assertEqual([row["id"] for row in page["data"]], ["own-unread"])
        self.assertEqual(page["unread_count"], 1)
        self.assertEqual(page["total"], 2)
        self.assertNotIn("other-unread", str(page))
        walled = {"id": "own-walled", "user_id": caller, "read_at": None, "title": "Smith matter", "link": "/matters/secret", "matter_walled": True}
        firm = {"id": "own-firm", "user_id": caller, "read_at": None, "title": "Backup finished", "matter_walled": False}
        off_wall = notification_page(
            _operation(self.spec, "listNotification"),
            {"role": "staff", "user_id": caller, "caller_user_id": caller, "on_wall": False},
            [walled, firm],
            1,
        )
        self.assertEqual([row["id"] for row in off_wall["data"]], ["own-firm"])
        self.assertEqual(off_wall["unread_count"], 1)
        self.assertNotIn("Smith matter", str(off_wall))
        admin_off = notification_page(
            _operation(self.spec, "listNotification"),
            {"role": "admin", "user_id": caller, "caller_user_id": caller, "on_wall": False},
            [walled, firm],
            10,
        )
        self.assertEqual([row["id"] for row in admin_off["data"]], ["own-firm"])
        notice_matter = "018f1a00-7b3c-7d4e-8a11-cccccccccccccccc"
        walled = {**walled, "matter_id": notice_matter}
        on_wall = notification_page(
            _operation(self.spec, "listNotification"),
            {"role": "staff", "user_id": caller, "caller_user_id": caller, "matter_id": notice_matter, "on_wall": True},
            [walled, firm],
            10,
        )
        self.assertEqual([row["id"] for row in on_wall["data"]], ["own-walled", "own-firm"])
        self.assertEqual(
            notification_recipient_decision(
                _operation(self.spec, "markNotificationRead"),
                {"role": "staff", "owner": "self", "caller_user_id": caller, "recipient_user_id": caller, "matter_walled": True, "on_wall": False},
            ),
            "not_found",
        )

    def test_dispatch_checks_notification_scopes_include_strings_and_marker_updates(self):
        def bearer(**extra):
            return {"authorization": "Bearer secret", "authorization_scheme": "Bearer", "credential_valid": True, **extra}

        caller = "018f1a00-7b3c-7d4e-8a11-111111111111"
        own = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["notifications:read"], owner="self", caller_user_id=caller),
                "tool": "listNotification",
                "arguments": {},
            },
        )
        self.assertEqual(own["result"], "allowed")
        self.assertTrue(own["effects"])
        missing = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["matters:read"], owner="self", caller_user_id=caller),
                "tool": "listNotification",
                "arguments": {},
            },
        )
        self.assertEqual(missing["result"], "scope_denied")
        self.assertFalse(missing["effects"])
        read_only_delete = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["notifications:read"], owner="self", caller_user_id=caller),
                "tool": "deleteNotification",
                "arguments": {"path": {"notification_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaaa"}},
            },
        )
        self.assertEqual(read_only_delete["result"], "scope_denied")
        self.assertFalse(read_only_delete["effects"])
        deleted = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["notifications:write"], owner="self", caller_user_id=caller),
                "tool": "deleteNotification",
                "arguments": {"path": {"notification_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaaa"}},
            },
        )
        self.assertEqual(deleted["result"], "allowed")
        self.assertTrue(deleted["effects"])
        other = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["notifications:write"], owner="other_user", caller_user_id=caller),
                "tool": "deleteNotification",
                "arguments": {},
            },
        )
        self.assertEqual(other["result"], "not_found")
        self.assertFalse(other["effects"])
        created = mcp_tool_dispatch(
            self.spec,
            {**bearer(role="staff", token_scopes=["matters:read"]), "tool": "createNotification", "arguments": {}},
        )
        self.assertEqual(created["result"], "scope_denied")
        self.assertFalse(created["effects"])
        created_ok = mcp_tool_dispatch(
            self.spec,
            {**bearer(role="staff", token_scopes=["notifications:write"]), "tool": "createNotification", "arguments": {}},
        )
        self.assertEqual(created_ok["result"], "allowed")
        self.assertTrue(created_ok["effects"])
        secret_view = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["matters:read"], approved_scopes=["matters:read"]),
                "tool": "listMatter",
                "arguments": {"query": {"include": "other.secret"}},
            },
        )
        self.assertEqual(secret_view["result"], "forbidden")
        self.assertFalse(secret_view["effects"])
        paired = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["matters:read", "views:matter-clock.targets:read"], approved_scopes=["matters:read", "views:matter-clock.targets:read"]),
                "tool": "listMatter",
                "arguments": {"query": {"include": "matter-clock.targets, other.secret"}},
            },
        )
        self.assertEqual(paired["result"], "forbidden")
        self.assertFalse(paired["effects"])
        listed = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["matters:read", "views:matter-clock.targets:read"], approved_scopes=["matters:read", "views:matter-clock.targets:read"]),
                "tool": "listMatter",
                "arguments": {"query": {"include": ["matter-clock.targets"]}},
            },
        )
        self.assertEqual(listed["result"], "allowed")
        self.assertTrue(listed["effects"])
        named = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["matters:read", "views:matter-clock.targets:read"], approved_scopes=["matters:read", "views:matter-clock.targets:read"]),
                "tool": "listMatter",
                "arguments": {"query": {"include": "matter-clock.targets"}},
            },
        )
        self.assertEqual(named["result"], "allowed")
        self.assertTrue(named["effects"])
        broken = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["matters:read"], approved_scopes=["matters:read"]),
                "tool": "listMatter",
                "arguments": {"query": {"include": 5}},
            },
        )
        self.assertEqual(broken["result"], "validation_failed")
        self.assertFalse(broken["effects"])
        marker_id = "018f1a00-7b3c-7d4e-8a11-111111111111"
        definition = {
            "id": marker_id,
            "label": "After hours",
            "color": "#336699",
            "active": True,
            "module_id": "intake",
            "namespace": "intake",
            "target": None,
        }
        updated = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(viewer_present=False, module_id="intake", approved_scopes=["markers:write"], token_scopes=["markers:write"], stored_definition=definition),
                "tool": "updateModuleMarker",
                "arguments": {"path": {"module_id": "intake", "marker_id": marker_id}, "body": {"color": "#AABBCC"}},
            },
        )
        self.assertEqual(updated["result"], "allowed")
        self.assertTrue(updated["effects"])
        self.assertTrue(definition["active"])
        self.assertEqual(definition["color"], "#336699")
        revoked = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(viewer_present=False, module_id="intake", approved_scopes=["markers:write"], token_scopes=["markers:write"], stored_definition=dict(definition)),
                "tool": "revokeModuleMarker",
                "arguments": {"path": {"module_id": "intake", "marker_id": marker_id}},
            },
        )
        self.assertEqual(revoked["result"], "allowed")
        self.assertTrue(revoked["effects"])
        self.assertTrue(definition["active"])
        missing_marker = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(viewer_present=False, module_id="intake", approved_scopes=["markers:write"], token_scopes=["markers:write"]),
                "tool": "updateModuleMarker",
                "arguments": {
                    "path": {"module_id": "intake", "marker_id": marker_id},
                    "body": {"definition": {**definition, "module_id": "intake"}, "color": "#000000"},
                },
            },
        )
        self.assertEqual(missing_marker["result"], "not_found")
        self.assertFalse(missing_marker["effects"])
        foreign = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(
                    viewer_present=False,
                    module_id="intake",
                    approved_scopes=["markers:write"], token_scopes=["markers:write"],
                    stored_definition={**definition, "module_id": "victim"},
                ),
                "tool": "revokeModuleMarker",
                "arguments": {"path": {"module_id": "intake", "marker_id": marker_id}},
            },
        )
        self.assertEqual(foreign["result"], "forbidden")
        self.assertFalse(foreign["effects"])
        replayed = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(viewer_present=False, module_id="intake", approved_scopes=["markers:write"], token_scopes=["markers:write"], stored_definition=dict(definition), replay=True),
                "tool": "revokeModuleMarker",
                "arguments": {"path": {"module_id": "intake", "marker_id": marker_id}},
            },
        )
        self.assertEqual(replayed["result"], "original_result")
        self.assertFalse(replayed["effects"])
        inactive = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(
                    viewer_present=False,
                    module_id="intake",
                    approved_scopes=["markers:write"], token_scopes=["markers:write"],
                    stored_definition={**definition, "active": False},
                ),
                "tool": "updateModuleMarker",
                "arguments": {"path": {"module_id": "intake", "marker_id": marker_id}, "body": {"color": "#000000"}},
            },
        )
        self.assertEqual(inactive["result"], "conflict")
        self.assertFalse(inactive["effects"])

    def test_mcp_dispatcher_covers_special_authorization_families(self):
        """Every published family that takes a special authorization path is decided by mcp_tool_dispatch."""
        families = {"batch": set(), "module_principal": set(), "issuance": set(), "trust_debit": set()}
        for _method, _path, operation in _ops(self.spec):
            access = operation["x-togavsu-access"]
            name = operation["operationId"]
            if access.get("batch_authorization") == "all_or_nothing" and access.get("matter_wall"):
                families["batch"].add(name)
            if access.get("module_principal"):
                families["module_principal"].add(name)
            if access.get("issuance") == "admin_only":
                families["issuance"].add(name)
            if access.get("trust_debit"):
                families["trust_debit"].add(name)
        self.assertEqual(
            families,
            {
                "batch": {"bulkCustomFieldValue", "bulkEvent", "bulkInvoice"},
                "module_principal": {
                    "createModuleHelpPage",
                    "replaceModuleHelpPages",
                    "deleteModuleHelpPage",
                    "createModuleMarker",
                    "updateModuleMarker",
                    "revokeModuleMarker",
                },
                "issuance": {"createApiToken"},
                "trust_debit": {"applyTrustToInvoice", "applyFlatFee"},
            },
        )

        def bearer(**extra):
            return {"authorization": "Bearer secret", "authorization_scheme": "Bearer", "credential_valid": True, **extra}

        visible = "018f1a00-7b3c-7d4e-8a11-333333333333"
        hidden = "018f1a00-7b3c-7d4e-8a11-333333333339"
        membership = {visible: True, hidden: False}
        client = "018f1a00-7b3c-7d4e-8a11-222222222222"
        account = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00001"
        owner = "018f1a00-7b3c-7d4e-8a11-555555555555"

        def item(matter_id):
            return {
                "action": "noted",
                "record_type": "matter",
                "record_id": matter_id,
                "summary": "Batch row",
                "matter_id": matter_id,
                "matter_walled": False,
                "on_wall": True,
            }

        for operation_id in sorted(families["batch"]):
            operation = _operation(self.spec, operation_id)
            role = operation["x-togavsu-access"]["roles"][0]
            mixed = mcp_tool_dispatch(
                self.spec,
                {
                    **bearer(role=role, token_scopes=list(operation["x-togavsu-access"]["scopes"]), matter_membership=membership),
                    "tool": operation_id,
                    "arguments": {"body": {"items": [item(visible), item(hidden)]}},
                },
            )
            self.assertEqual(mixed["result"], "not_found", operation_id)
            self.assertFalse(mixed["effects"], operation_id)
        admitted = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=["events:write"], matter_membership=membership),
                "tool": "bulkEvent",
                "arguments": {"body": {"items": [item(visible), item(visible)]}},
            },
        )
        self.assertEqual(admitted["result"], "allowed")
        self.assertTrue(admitted["effects"])
        _component_validator(self.spec, "EventWrite").validate({key: value for key, value in item(visible).items() if key in {"action", "record_type", "record_id", "summary", "matter_id"}})
        unscoped = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="staff", token_scopes=[], matter_membership=membership),
                "tool": "bulkEvent",
                "arguments": {"body": {"items": [item(visible)]}},
            },
        )
        self.assertEqual(unscoped["result"], "scope_denied")
        self.assertFalse(unscoped["effects"])
        help_body = {"path": {"module_id": "matter-clock"}, "body": {"heading": "matter-clock"}}
        help_ok = mcp_tool_dispatch(
            self.spec,
            {**bearer(viewer_present=False, module_id="matter-clock", approved_scopes=["help:write"], token_scopes=["help:write"], installed=True, grant_revoked=False), "tool": "createModuleHelpPage", "arguments": help_body},
        )
        self.assertEqual(help_ok["result"], "allowed")
        self.assertTrue(help_ok["effects"])
        for operation_id in sorted(families["module_principal"]):
            operation = _operation(self.spec, operation_id)
            scope = operation["x-togavsu-access"]["module_principal"]["scope"]
            module_id = "intake" if scope == "markers:write" else "matter-clock"
            arguments = {"path": {"module_id": module_id}}
            if scope == "help:write":
                arguments["body"] = {"heading": module_id}
            else:
                arguments["body"] = {"marker_id": "018f1a00-7b3c-7d4e-8a11-111111111111", "label": "After hours", "color": "#336699"}
                arguments["path"]["marker_id"] = "018f1a00-7b3c-7d4e-8a11-111111111111"
            empty = mcp_tool_dispatch(
                self.spec,
                {
                    **bearer(viewer_present=False, module_id=module_id, approved_scopes=[], token_scopes=[scope], installed=True),
                    "tool": operation_id,
                    "arguments": arguments,
                },
            )
            self.assertEqual(empty["result"], "scope_denied", operation_id)
            self.assertFalse(empty["effects"], operation_id)
            revoked = mcp_tool_dispatch(
                self.spec,
                {
                    **bearer(viewer_present=False, module_id=module_id, approved_scopes=[scope], token_scopes=[scope], installed=True, grant_revoked=True),
                    "tool": operation_id,
                    "arguments": arguments,
                },
            )
            self.assertEqual(revoked["result"], "forbidden", operation_id)
            self.assertFalse(revoked["effects"], operation_id)
            uninstalled = mcp_tool_dispatch(
                self.spec,
                {
                    **bearer(viewer_present=False, module_id=module_id, approved_scopes=[scope], token_scopes=[scope], installed=False),
                    "tool": operation_id,
                    "arguments": arguments,
                },
            )
            self.assertEqual(uninstalled["result"], "forbidden", operation_id)
            self.assertFalse(uninstalled["effects"], operation_id)
        issued = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="admin", token_scopes=["tokens:write", "profile:read"], owner_role_scopes=["profile:read"]),
                "tool": "createApiToken",
                "arguments": {"body": {"name": "Desk", "owner_user_id": owner, "scopes": ["profile:read"]}},
            },
        )
        self.assertEqual(issued["result"], "allowed")
        self.assertTrue(issued["effects"])
        _component_validator(self.spec, "ApiTokenWrite").validate({"name": "Desk", "owner_user_id": owner, "scopes": ["profile:read"]})
        nameless = mcp_tool_dispatch(
            self.spec,
            {
                **bearer(role="admin", token_scopes=["tokens:write", "profile:read"], owner_role_scopes=["profile:read"]),
                "tool": "createApiToken",
                "arguments": {"body": {"owner_user_id": owner}},
            },
        )
        self.assertEqual(nameless["result"], "validation_failed")
        self.assertFalse(nameless["effects"])
        openings = {
            "trust_account_id": account,
            "client_id": client,
            "matter_id": visible,
            "matter_cents": 1000,
            "client_cents": 5000,
            "account_cents": 4000,
            "active": True,
            "authorized": True,
            "firm_id": "firm-1",
        }
        hinted = trust_debit_decision(
            {
                "role": "attorney",
                "token_scopes": ["trust:withdraw"],
                "client_id": client,
                "trust_account_id": account,
                "matter_id": visible,
                "amount_cents": 1500,
                "balance_after": 0,
                "matter_walled": False,
                "balances": [openings],
                "firm_id": "firm-1",
            }
        )
        self.assertEqual(hinted["result"], "allowed")
        self.assertTrue(hinted["below_zero"])
        self.assertEqual(hinted["balances"], {"matter_cents": -500, "client_cents": 3500, "account_cents": 2500})
        self.assertEqual([event["type"] for event in hinted["events"]], ["trust.entry_posted", "trust.flag_raised"])
        trust_body = {"client_id": client, "trust_account_id": account, "amount_cents": 1500}
        _component_validator(self.spec, "FlatFeeApply").validate(trust_body)
        _component_validator(self.spec, "ApplyTrust").validate(trust_body)
        for operation_id, arguments in (
            ("applyFlatFee", {"path": {"matter_id": visible}, "body": trust_body}),
            ("applyTrustToInvoice", {"path": {"invoice_id": "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbbb"}, "body": trust_body}),
        ):
            posted = mcp_tool_dispatch(
                self.spec,
                {
                    **bearer(role="attorney", token_scopes=["trust:withdraw"], matter_id=visible, balances=[openings], firm_id="firm-1", matter_walled=False),
                    "tool": operation_id,
                    "arguments": arguments,
                },
            )
            self.assertEqual(posted["result"], "allowed", operation_id)
            self.assertTrue(posted["effects"], operation_id)
            missing_balances = mcp_tool_dispatch(
                self.spec,
                {
                    **bearer(role="attorney", token_scopes=["trust:withdraw"], matter_id=visible, matter_walled=False),
                    "tool": operation_id,
                    "arguments": arguments,
                },
            )
            self.assertEqual(missing_balances["result"], "not_found", operation_id)
            self.assertFalse(missing_balances["effects"], operation_id)
            bare = mcp_tool_dispatch(
                self.spec,
                {
                    **bearer(role="attorney", token_scopes=[], matter_id=visible, balances=[openings], firm_id="firm-1", matter_walled=False),
                    "tool": operation_id,
                    "arguments": arguments,
                },
            )
            self.assertEqual(bare["result"], "scope_denied", operation_id)
            self.assertFalse(bare["effects"], operation_id)
            off_wall = mcp_tool_dispatch(
                self.spec,
                {
                    **bearer(
                        role="attorney",
                        token_scopes=["trust:withdraw"],
                        matter_id=visible,
                        balances=[openings],
                        firm_id="firm-1",
                        matter_walled=True,
                        matter_membership={visible: False},
                    ),
                    "tool": operation_id,
                    "arguments": arguments,
                },
            )
            self.assertEqual(off_wall["result"], "not_found", operation_id)
            self.assertFalse(off_wall["effects"], operation_id)

    def test_owner_bound_events_follow_the_read_policy(self):
        owners = self.spec["x-togavsu-event-owners"]
        order = self.spec["webhooks"]["moduleEvent"]["post"]["x-togavsu-delivery-authorization"]["order"]
        self.assertLess(order.index("owner"), order.index("serialize"))
        families = {rule["family"] for rule in owners.values()}
        self.assertEqual(families, {"notification", "api_token", "session", "ical_feed", "file_upload"})
        for event_type, rule in owners.items():
            if event_type.startswith("notification."):
                self.assertEqual(rule["binding"], "recipient", event_type)
                self.assertFalse(rule["admin_bypass"], event_type)
            self.assertEqual(rule["deleted_source"], "predelete", event_type)
            self.assertEqual(rule["asserted_recipient"], "ignored", event_type)
            self.assertEqual(rule["missing_owner"], "fail_closed", event_type)
        for event_type in ("notification.created", "notification.read", "notification.deleted"):
            self.assertIn(event_type, owners, event_type)
        self_id = "018f1a00-7b3c-7d4e-8a11-111111111111"
        other_id = "018f1a00-7b3c-7d4e-8a11-222222222222"
        secret = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaaa"
        summary = "only the recipient may read this"
        base = {
            "id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeeee",
            "occurred_at": "2026-11-07T18:00:00Z",
            "actor_kind": "system",
            "record_id": secret,
            "summary": summary,
            "matter_walled": False,
        }

        def subscriber(event_type, user_id, role, scopes):
            return {
                "installed": True,
                "registered": True,
                "module_id": "desk",
                "approved_scopes": scopes,
                "subscriptions": [event_type],
                "on_wall": True,
                "attorney_only": False,
                "principal_user_id": user_id,
                "principal_role": role,
            }

        def event_for(event_type, stored, asserted=None):
            rule = owners[event_type]
            subject = {"recipient_user_id": stored} if rule["binding"] == "recipient" else {"owner_user_id": stored}
            body = {**base, "type": event_type, "record_type": rule["family"]}
            if event_type.endswith(".deleted"):
                body["deleted"] = True
                body["predelete"] = subject
                body["recipient_user_id"] = asserted or other_id
            else:
                body["context"] = subject
                if asserted:
                    body["recipient_user_id"] = asserted
            return body

        for event_type, scope in (
            ("notification.created", "notifications:read"),
            ("notification.read", "notifications:read"),
            ("notification.deleted", "notifications:read"),
            ("api_token.created", "tokens:read"),
            ("api_token.revoked", "tokens:read"),
            ("session.created", "sessions:read"),
            ("session.ended", "sessions:read"),
            ("ical_feed.created", "calendar:read"),
            ("ical_feed.revoked", "calendar:read"),
            ("file_upload.created", "files:write"),
            ("file_upload.staged", "files:write"),
        ):
            sample = event_for(event_type, self_id, asserted=other_id)
            own = webhook_envelope(subscriber(event_type, self_id, "staff", [scope]), sample)
            self.assertIsNotNone(own, event_type)
            self.assertEqual(own["record_id"], secret, event_type)
            self.assertEqual(own["summary"], summary, event_type)
            self.assertNotIn("predelete", own, event_type)
            for label, module in (
                ("other", subscriber(event_type, other_id, "staff", [scope])),
                ("admin", subscriber(event_type, other_id, "admin", [scope])),
                ("revoked", {**subscriber(event_type, self_id, "admin", [scope]), "principal_revoked": True}),
            ):
                if label == "admin" and owners[event_type]["admin_bypass"]:
                    delivered = webhook_envelope(module, sample)
                    self.assertEqual(delivered["record_id"], secret, event_type)
                    continue
                denied = webhook_envelope(module, sample)
                self.assertIsNone(denied, (event_type, label))
                self.assertNotIn(secret, str(denied), (event_type, label))
                self.assertNotIn(summary, str(denied), (event_type, label))
            missing = dict(sample)
            missing.pop("context", None)
            missing.pop("predelete", None)
            missing["recipient_user_id"] = self_id
            missing["owner_user_id"] = self_id
            denied_missing = webhook_envelope(subscriber(event_type, self_id, "admin", [scope]), missing)
            self.assertIsNone(denied_missing, event_type)
            self.assertNotIn(secret, str(denied_missing), event_type)
            self.assertNotIn(summary, str(denied_missing), event_type)
        history = _operation(self.spec, "listWebhookDeliveries")
        deleted = event_for("notification.deleted", self_id, asserted=other_id)
        deleted.update({"required_scopes": ["notifications:read"], "matter_walled": False, "entity_id": secret})
        own_page = inherited_history_rows(history, {"role": "staff", "user_id": self_id, "scopes": ["webhooks:read", "notifications:read"]}, [deleted])
        self.assertEqual([row["record_id"] for row in own_page["rows"]], [secret])
        self.assertNotIn("predelete", own_page["rows"][0])
        for caller in (
            {"role": "admin", "user_id": other_id, "scopes": ["webhooks:read", "notifications:read"]},
            {"role": "staff", "user_id": self_id, "scopes": ["webhooks:read", "notifications:read"], "revoked": True},
        ):
            hidden = inherited_history_rows(history, caller, [deleted])
            self.assertEqual(hidden["rows"], [], caller["role"])
            self.assertNotIn(secret, str(hidden["rows"]))
            self.assertNotIn(summary, str(hidden["rows"]))
        no_owner = {key: value for key, value in deleted.items() if key != "predelete"}
        no_owner["recipient_user_id"] = self_id
        omitted = inherited_history_rows(history, {"role": "admin", "user_id": self_id, "scopes": ["webhooks:read", "notifications:read"]}, [no_owner])
        self.assertEqual(omitted["rows"], [])
        self.assertNotIn(summary, str(omitted["rows"]))

    def test_invoice_drafts_accept_a_date_range(self):
        operation = _operation(self.spec, "createInvoiceDrafts")
        text = operation["description"]
        schema = self.spec["components"]["schemas"]["InvoiceDraftRequest"]
        self.assertNotIn("matter_id or matter_ids is required", text)
        self.assertNotIn("matter_id or matter_ids is required", schema["description"])
        self.assertIn("date range", text.lower())
        self.assertIn("does not require", text)
        self.assertFalse(operation["x-togavsu-access"]["draft_selector"]["matters_required"])
        self.assertEqual(operation["x-togavsu-access"]["draft_selector"]["alternatives"], ["matter_id", "matter_ids", "date_range"])
        validator = _component_validator(self.spec, "InvoiceDraftRequest")
        matter_a = "018f1a00-7b3c-7d4e-8a11-333333333331"
        matter_b = "018f1a00-7b3c-7d4e-8a11-333333333332"
        matter_c = "018f1a00-7b3c-7d4e-8a11-333333333333"
        validator.validate({"matter_id": matter_a})
        validator.validate({"matter_ids": [matter_a, matter_b]})
        validator.validate({"from_date": "2026-03-01", "to_date": "2026-03-31"})
        for body in (
            {},
            {"from_date": "2026-03-01"},
            {"to_date": "2026-03-31"},
            {"matter_id": matter_a, "from_date": "2026-03-01", "to_date": "2026-03-31"},
            {"matter_id": matter_a, "matter_ids": [matter_b]},
            {"matter_ids": []},
        ):
            with self.assertRaises(Exception):
                validator.validate(body)
        matters = [
            {"matter_id": matter_a, "matter_walled": True, "unbilled_on": ["2026-03-02"]},
            {"matter_id": matter_b, "matter_walled": True, "unbilled_on": ["2026-03-03"], "fee_on": "2026-03-03"},
            {"matter_id": matter_c, "matter_walled": False, "unbilled_on": ["2026-03-04"]},
        ]
        staff = {
            "role": "staff",
            "token_scopes": ["invoices:write"],
            "matter_membership": {matter_a: True, matter_b: False},
            "on_wall": True,
        }
        ranged = invoice_draft_decision(operation, {**staff, "from_date": "2026-03-01", "to_date": "2026-03-31"}, matters)
        self.assertEqual(ranged["result"], "allowed")
        self.assertEqual(ranged["reason"], "date_range")
        self.assertEqual(ranged["drafted"], [matter_a, matter_c])
        self.assertNotIn(matter_b, str(ranged))
        admin = invoice_draft_decision(
            operation,
            {"role": "admin", "token_scopes": ["invoices:write"], "matter_membership": {matter_a: True, matter_b: False}, "on_wall": True, "from_date": "2026-03-01", "to_date": "2026-03-31"},
            matters,
        )
        self.assertEqual(admin["drafted"], [matter_a, matter_c])
        self.assertNotIn(matter_b, str(admin))
        outside = invoice_draft_decision(operation, {**staff, "from_date": "2026-01-01", "to_date": "2026-01-31"}, matters)
        self.assertEqual(outside["result"], "allowed")
        self.assertEqual(outside["drafted"], [])
        named = invoice_draft_decision(operation, {**staff, "matter_id": matter_a}, matters)
        self.assertEqual(named["drafted"], [matter_a])
        hidden = invoice_draft_decision(operation, {**staff, "matter_id": matter_b}, matters)
        self.assertEqual(hidden["result"], "not_found")
        self.assertEqual(hidden["drafted"], [])
        self.assertNotIn(matter_b, str(hidden))
        mixed = invoice_draft_decision(operation, {**staff, "matter_ids": [matter_a, matter_b]}, matters)
        self.assertEqual(mixed["result"], "not_found")
        self.assertEqual(mixed["drafted"], [])
        self.assertNotIn(matter_b, str(mixed))
        for label, call, expected in (
            ("empty", staff, "validation_failed"),
            ("one-date", {**staff, "from_date": "2026-03-01"}, "validation_failed"),
            ("reversed", {**staff, "from_date": "2026-04-02", "to_date": "2026-03-01"}, "validation_failed"),
            ("both", {**staff, "matter_id": matter_a, "from_date": "2026-03-01", "to_date": "2026-03-31"}, "validation_failed"),
            ("two-ids", {**staff, "matter_id": matter_a, "matter_ids": [matter_c]}, "validation_failed"),
            ("blank-list", {**staff, "matter_ids": []}, "validation_failed"),
            ("bad-day", {**staff, "from_date": "2026-13-01", "to_date": "2026-13-02"}, "validation_failed"),
            ("accountant", {**staff, "role": "accountant", "from_date": "2026-03-01", "to_date": "2026-03-31"}, "forbidden"),
            ("read-only", {**staff, "token_scopes": ["invoices:read"], "from_date": "2026-03-01", "to_date": "2026-03-31"}, "scope_denied"),
        ):
            decision = invoice_draft_decision(operation, call, matters)
            self.assertEqual(decision["result"], expected, label)
            self.assertEqual(decision["drafted"], [], label)
            self.assertNotIn("required", decision.get("reason") or "", label)

    def test_attorney_only_files_are_omitted_from_aggregates(self):
        rule = _operation(self.spec, "search")["x-togavsu-access"]["inherited_record_access"]
        self.assertEqual(rule["record_restrictions"][0]["effect"], "omit_row")
        self.assertEqual(rule["record_restrictions"][0]["record_type"], "file")
        self.assertEqual(rule["field_redactions"][0]["effect"], "redact_field")
        self.assertEqual(rule["attorney_only_values"], "redacted")
        file_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1"
        contact_id = "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbb1"
        file_row = {
            "id": file_id,
            "record_id": file_id,
            "entity_id": file_id,
            "record_type": "file",
            "record_restriction": {"type": "file", "visibility": "attorney_only"},
            "attorney_only": True,
            "title": "privileged motion",
            "link": "/files/privileged-motion",
            "summary": "privileged motion",
            "matter_walled": False,
            "required_scopes": ["files:read"],
            "count": 4,
        }
        contact_row = {
            "id": contact_id,
            "record_type": "contact",
            "attorney_only": True,
            "identifier": "123-45-6789",
            "summary": "Ada Lovelace",
            "matter_walled": False,
            "required_scopes": ["contacts:read"],
        }
        staff = {"role": "staff", "on_wall": True, "scopes": ["search:use", "files:read", "contacts:read", "events:read", "audit:read"], "next_cursor": file_id}
        for operation_id in ("search", "listEvents", "widgetRecentActivity", "historyFile", "reportArAging", "listAuditLog"):
            page = inherited_history_rows(_operation(self.spec, operation_id), staff, [file_row, contact_row])
            self.assertEqual(page["total"], 1, operation_id)
            self.assertEqual([row["id"] for row in page["rows"]], [contact_id], operation_id)
            self.assertIsNone(page["next_cursor"], operation_id)
            self.assertEqual(page["rows"][0]["identifier"], {"redacted": True}, operation_id)
            self.assertEqual(page["rows"][0]["summary"], {"redacted": True}, operation_id)
            rendered = str(page)
            self.assertNotIn(file_id, rendered, operation_id)
            self.assertNotIn("privileged motion", rendered, operation_id)
            self.assertNotIn("/files/privileged-motion", rendered, operation_id)
            self.assertNotIn("123-45-6789", rendered, operation_id)
        attorney = inherited_history_rows(_operation(self.spec, "search"), {**staff, "role": "attorney"}, [file_row])
        self.assertEqual(attorney["rows"][0]["title"], "privileged motion")
        client = inherited_history_rows(_operation(self.spec, "search"), {**staff, "role": "client"}, [file_row])
        self.assertEqual(client["rows"], [])
        self.assertEqual(client["total"], 0)
        self.assertNotIn(file_id, str(client))

    def test_bulk_custom_fields_follow_the_time_domain(self):
        operation = _operation(self.spec, "bulkCustomFieldValue")
        access = operation["x-togavsu-access"]
        self.assertEqual(access["record_type_domain"], {"matter": None, "contact": None, "time_entry": "time", "expense": "time"})
        self.assertEqual(_operation(self.spec, "putTimeEntryCustomFields").get("x-togavsu-replaceable-domain"), "time")
        self.assertEqual(_operation(self.spec, "putExpenseCustomFields").get("x-togavsu-replaceable-domain"), "time")
        self.assertEqual(access["record_type_scopes"]["time_entry"], _operation(self.spec, "putTimeEntryCustomFields")["x-togavsu-access"]["scopes"])
        self.assertEqual(access["record_type_scopes"]["expense"], _operation(self.spec, "putExpenseCustomFields")["x-togavsu-access"]["scopes"])
        self.assertTrue(access["resolve_before_write"])
        self.assertFalse(access["partial_core_write"])
        self.assertEqual(access["replaced_core"], "read_only")
        matter = "018f1a00-7b3c-7d4e-8a11-333333333331"
        other = "018f1a00-7b3c-7d4e-8a11-333333333332"
        caller = {"role": "staff", "token_scopes": ["matters:write", "time:write", "expenses:write"], "matter_membership": {matter: True, other: False}, "on_wall": True}
        core = {"time": {"replaced": False, "available": True, "module_id": "time-mod"}}
        items = [
            {"record_type": "matter", "record_id": matter, "matter_id": matter, "matter_walled": True},
            {"record_type": "time_entry", "record_id": "time-1", "matter_id": matter, "matter_walled": True},
        ]
        allowed = custom_field_bulk_apply(operation, caller, items, core)
        self.assertEqual(allowed["result"], "allowed")
        self.assertEqual(allowed["written"], [matter, "time-1"])
        self.assertEqual(allowed["events"], ["custom_field.updated"])
        replaced = {"time": {"replaced": True, "available": True, "module_id": "time-mod"}}
        routed = custom_field_bulk_apply(operation, caller, items, replaced)
        self.assertEqual(routed["result"], "allowed")
        self.assertEqual(routed["written"], [matter])
        self.assertEqual(routed["routed"], ["time-1"])
        self.assertNotIn("time-1", routed["written"])
        down = custom_field_bulk_apply(operation, caller, items, {"time": {"replaced": True, "available": False, "module_id": "time-mod"}})
        self.assertEqual(down["result"], "batch_rejected")
        self.assertEqual(down["written"], [])
        self.assertEqual(down["routed"], [])
        self.assertEqual(down["events"], [])
        hidden = custom_field_bulk_apply(
            operation,
            caller,
            [{"record_type": "matter", "record_id": matter, "matter_id": matter, "matter_walled": True}, {"record_type": "expense", "record_id": "expense-1", "matter_id": other, "matter_walled": True}],
            replaced,
        )
        self.assertEqual(hidden["result"], "not_found")
        self.assertEqual(hidden["written"], [])
        self.assertEqual(hidden["events"], [])
        denied = custom_field_bulk_apply(operation, {**caller, "token_scopes": ["matters:write"]}, items, core)
        self.assertEqual(denied["result"], "scope_denied")
        self.assertEqual(denied["written"], [])

    def test_module_owned_upload_events_use_the_tagged_owner(self):
        owners = self.spec["x-togavsu-event-owners"]
        self.assertEqual(owners["file_upload.created"]["owner_form"], "tagged")
        self.assertEqual(owners["file_upload.staged"]["owner_form"], "tagged")
        upload = _operation(self.spec, "getFileUpload")
        self.assertEqual(upload["x-togavsu-access"]["scopes"], ["files:write"])
        self.assertEqual(event_read_scopes("file_upload.created"), ["files:write"])
        self.assertEqual(event_read_scopes("file_upload.staged"), ["files:write"])
        self.assertEqual(next(tool["scopes"] for tool in render_mcp(self.spec)["tools"] if tool["name"] == "getFileUpload"), ["files:write"])
        write_gets = [
            operation["operationId"]
            for method, _path, operation in _ops(self.spec)
            if method == "get" and any(scope.endswith(":write") for scope in operation["x-togavsu-access"]["scopes"])
        ]
        self.assertEqual(write_gets, ["getFileUpload", "listStorageBackend", "getStorageBackend"])
        for event_type in ("storage_backend.created", "storage_backend.updated", "storage_backend.tested"):
            self.assertEqual(event_read_scopes(event_type), ["files:write"], event_type)
        secret = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa9"
        matter = "018f1a00-7b3c-7d4e-8a11-333333333333"
        summary = "module staged the upload"
        event = {
            "id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeee9",
            "type": "file_upload.staged",
            "occurred_at": "2026-11-07T18:00:00Z",
            "actor_kind": "module",
            "record_type": "file_upload",
            "record_id": secret,
            "matter_id": matter,
            "summary": summary,
            "matter_walled": True,
            "context": {"owner": {"type": "module", "id": "drop-folder"}, "owner_user_id": "018f1a00-7b3c-7d4e-8a11-999999999999"},
        }
        owner = {
            "installed": True,
            "registered": True,
            "module_id": "drop-folder",
            "approved_scopes": ["files:write"],
            "subscriptions": ["file_upload.staged"],
            "on_wall": True,
            "matter_id": matter,
            "matter_membership": {matter: True},
            "attorney_only": False,
        }
        delivered = webhook_envelope(owner, event)
        self.assertEqual(delivered["record_id"], secret)
        self.assertNotIn("owner_user_id", delivered)
        for label, module in (
            ("other-module", {**owner, "module_id": "other-mod"}),
            ("fabricated-user", {**owner, "module_id": "other-mod", "principal_user_id": "018f1a00-7b3c-7d4e-8a11-999999999999"}),
            ("revoked-grant", {**owner, "grant_revoked": True}),
            ("off-wall", {**owner, "on_wall": False, "matter_membership": {matter: False}}),
            ("wrong-scope", {**owner, "approved_scopes": ["matters:read"]}),
            ("read-only", {**owner, "approved_scopes": ["files:read"]}),
        ):
            denied = webhook_envelope(module, event)
            self.assertIsNone(denied, label)
            self.assertNotIn(secret, str(denied), label)
            self.assertNotIn(summary, str(denied), label)
        history = _operation(self.spec, "listWebhookDeliveries")
        row = {**event, "required_scopes": ["files:read"], "action": "file_upload.staged"}
        own_page = inherited_history_rows(history, {"module_id": "drop-folder", "role": "staff", "scopes": ["webhooks:read", "files:write"], "matter_membership": {matter: True}, "on_wall": True}, [row])
        read_only_page = inherited_history_rows(history, {"module_id": "drop-folder", "role": "staff", "scopes": ["webhooks:read", "files:read"], "matter_membership": {matter: True}, "on_wall": True}, [row])
        self.assertEqual(read_only_page["rows"], [])
        self.assertNotIn(secret, str(read_only_page["rows"]))
        self.assertEqual([item["record_id"] for item in own_page["rows"]], [secret])
        other_page = inherited_history_rows(history, {"module_id": "other-mod", "principal_user_id": "018f1a00-7b3c-7d4e-8a11-999999999999", "role": "admin", "scopes": ["webhooks:read", "files:write"], "matter_membership": {matter: True}, "on_wall": True}, [row])
        self.assertEqual(other_page["rows"], [])
        self.assertNotIn(secret, str(other_page["rows"]))

    def test_account_trust_flags_match_the_emitted_scope(self):
        operation = _operation(self.spec, "postTrustEntry")
        self.assertEqual(set(self.spec["components"]["schemas"]["TrustFlag"]["oneOf"][i]["properties"]["scope"]["const"] for i in range(3)), {"matter", "client", "account"})
        matter = "018f1a00-7b3c-7d4e-8a11-333333333333"
        client = "018f1a00-7b3c-7d4e-8a11-222222222222"
        account = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00001"
        other = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00002"
        entry = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa4"
        events = trust_ledger_events(
            operation,
            [
                {
                    "entry_type": "withdrawal",
                    "amount_cents": 10,
                    "method": "check",
                    "description": "withdrawal",
                    "opening": {"matter_cents": 100, "client_cents": 100, "account_cents": 5},
                    "matter_id": matter,
                    "client_id": client,
                    "trust_account_id": account,
                    "trust_entry_id": entry,
                }
            ],
            committed=True,
        )
        self.assertEqual([event["type"] for event in events], ["trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(events[1]["balance_scope"], "account")
        self.assertEqual(events[1]["balance_cents"], -5)
        stamp = "2026-11-07T18:00:00Z"
        actor = "018f1a00-7b3c-7d4e-8a11-555555555555"
        flag = {
            "id": "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa5",
            "created_at": stamp,
            "created_by": actor,
            "updated_at": stamp,
            "trust_entry_id": entry,
            "trust_account_id": account,
            "balance_cents": events[1]["balance_cents"],
            "scope": "account",
            "status": "open",
            "source": {"trust_entry_id": entry, "matter_id": matter, "matter_walled": False, "client_id": client, "trust_account_id": account},
        }
        flag_body = {key: value for key, value in flag.items() if key != "source"}
        _component_validator(self.spec, "TrustFlag").validate(flag_body)
        posted = {
            "entry": {
                "id": entry,
                "created_at": stamp,
                "created_by": actor,
                "trust_account_id": account,
                "client_id": client,
                "matter_id": matter,
                "entry_type": "withdrawal",
                "amount_cents": 10,
                "method": "check",
                "description": "Account shortfall",
            },
            "balances": {"matter_cents": 90, "client_cents": 90, "account_cents": -5},
            "flags": [flag_body],
        }
        _component_validator(self.spec, "TrustPostResult").validate(posted)
        accounts = [
            {"id": account, "active": True, "authorized": True, "firm_id": "firm-1"},
            {"id": other, "active": False, "authorized": True, "firm_id": "firm-1"},
        ]
        caller = {"role": "staff", "token_scopes": ["trust:read"], "firm_id": "firm-1", "matter_walled": False}
        stale = {**flag, "id": "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa6", "trust_account_id": other}
        foreign = {**flag, "id": "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa7", "trust_account_id": "018f1a00-7b3c-7d4e-8a11-aaaaaaa00009"}
        rows = trust_flag_rows(caller, [flag, stale, foreign], accounts, {"flag_scope": "account"})
        self.assertEqual(rows["result"], "allowed")
        self.assertEqual([row["id"] for row in rows["rows"]], [flag["id"]])
        self.assertEqual(rows["total"], 1)
        self.assertNotIn(other, str(rows["rows"]))
        self.assertEqual(trust_flag_rows({**caller, "token_scopes": ["trust:withdraw"]}, [flag], accounts)["result"], "scope_denied")
        hidden_matter = "018f1a00-7b3c-7d4e-8a11-333333333339"
        hidden_entry = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa8"
        client_flag = {
            "id": "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa9",
            "scope": "client",
            "client_id": client,
            "trust_entry_id": hidden_entry,
            "balance_cents": -10,
            "status": "open",
            "matter_walled": False,
            "source": {"trust_entry_id": hidden_entry, "matter_id": hidden_matter, "matter_walled": True, "client_id": client},
        }
        for role in ("staff", "admin"):
            denied_flags = trust_flag_rows(
                {"role": role, "token_scopes": ["trust:read"], "firm_id": "firm-1", "matter_membership": {hidden_matter: False}, "on_wall": True},
                [client_flag],
                accounts,
                {"cursor": client_flag["id"]},
            )
            self.assertEqual(denied_flags["rows"], [], role)
            self.assertEqual(denied_flags["total"], 0, role)
            self.assertIsNone(denied_flags["next_cursor"], role)
            self.assertNotIn(client_flag["id"], str(denied_flags), role)
            self.assertNotIn(hidden_entry, str(denied_flags), role)
        visible_flags = trust_flag_rows(
            {"role": "staff", "token_scopes": ["trust:read"], "firm_id": "firm-1", "matter_membership": {hidden_matter: True}, "on_wall": True},
            [client_flag],
            accounts,
        )
        self.assertEqual([row["id"] for row in visible_flags["rows"]], [client_flag["id"]])
        self.assertEqual(visible_flags["rows"][0]["trust_entry_id"], hidden_entry)
        unknown = trust_flag_rows(caller, [{**client_flag, "source": None, "matter_id": matter, "matter_walled": False}], accounts)
        self.assertEqual(unknown["rows"], [])
        self.assertNotIn(client_flag["id"], str(unknown))

    def test_history_inherits_the_underlying_record(self):
        covered = []
        for method, _path, operation in _ops(self.spec):
            if method != "get":
                continue
            if "inherited_record_access" not in operation["x-togavsu-access"]:
                continue
            rule = operation["x-togavsu-access"]["inherited_record_access"]
            self.assertTrue(operation["x-togavsu-access"]["matter_wall"], operation["operationId"])
            self.assertFalse(operation["x-togavsu-access"]["admin_bypasses_wall"], operation["operationId"])
            self.assertFalse(rule["admin_bypasses_wall"], operation["operationId"])
            self.assertEqual(rule["scopes_from"], "underlying_record", operation["operationId"])
            self.assertEqual(rule["attorney_only_values"], "redacted", operation["operationId"])
            self.assertEqual(rule["membership"], "per_matter_id", operation["operationId"])
            covered.append(operation["operationId"])
        for operation_id in (
            "listAuditLog",
            "listWebhookDeliveries",
            "listEvents",
            "listAiCalls",
            "historyMatter",
            "historyFile",
            "historyInvoice",
            "reportArAging",
            "search",
            "widgetRecentActivity",
        ):
            self.assertIn(operation_id, covered, operation_id)
        hidden = {
            "id": "018f1a00-7b3c-7d4e-8a11-111111111111",
            "entity_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
            "record_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
            "summary": "walled matter opened",
            "old_value": "secret reason",
            "new_value": "secret reason",
            "matter_walled": True,
            "required_scopes": ["matters:read"],
            "attorney_only": True,
        }
        visible = {
            "id": "018f1a00-7b3c-7d4e-8a11-222222222222",
            "entity_id": "018f1a00-7b3c-7d4e-8a11-444444444444",
            "summary": "firm setting",
            "matter_walled": False,
            "required_scopes": ["settings:read"],
            "attorney_only": False,
        }
        admin_off_wall = {"role": "admin", "on_wall": False, "scopes": ["audit:read", "webhooks:read", "matters:read", "settings:read"]}
        for operation_id in ("listAuditLog", "listWebhookDeliveries", "listEvents", "historyMatter", "search", "reportArAging"):
            operation = _operation(self.spec, operation_id)
            page = inherited_history_rows(operation, admin_off_wall, [hidden, visible])
            self.assertEqual(page["result"], "allowed", operation_id)
            self.assertEqual([row["id"] for row in page["rows"]], [visible["id"]], operation_id)
            self.assertNotIn(hidden["entity_id"], str(page["rows"]), operation_id)
        audit = _operation(self.spec, "listAuditLog")
        redacted = inherited_history_rows(
            audit,
            {"role": "staff", "on_wall": True, "scopes": ["audit:read", "matters:read"]},
            [{**hidden, "matter_walled": False}],
        )
        self.assertEqual(redacted["rows"][0]["old_value"], {"redacted": True})
        self.assertEqual(redacted["rows"][0]["new_value"], {"redacted": True})
        self.assertNotIn("secret reason", str(redacted["rows"]))
        narrowed = inherited_history_rows(
            audit,
            {"role": "admin", "on_wall": True, "scopes": ["audit:read"]},
            [hidden],
        )
        self.assertEqual(narrowed["rows"], [])
        stamp = "2026-11-07T18:00:00Z"
        actor = "018f1a00-7b3c-7d4e-8a11-555555555555"
        matter = "018f1a00-7b3c-7d4e-8a11-333333333333"
        normal_event = {
            "id": "018f1a00-7b3c-7d4e-8a11-111111111111",
            "created_at": stamp,
            "created_by": actor,
            "actor_kind": "staff",
            "action": "matter.created",
            "record_type": "matter",
            "record_id": matter,
            "matter_id": matter,
            "summary": "Smith opened",
            "client_visible": True,
        }
        redacted_event = {**normal_event, "id": "018f1a00-7b3c-7d4e-8a11-222222222222", "summary": {"redacted": True}}
        event_page = {"data": [normal_event, redacted_event], "page": {"limit": 50, "next_cursor": None}}
        for operation_id in ("listEvents", "listMatterTimeline"):
            response = _operation(self.spec, operation_id)["responses"]["200"]["content"]["application/json"]["schema"]
            _schema_validator(self.spec, response).validate(event_page)
        audit_row = {
            "id": "018f1a00-7b3c-7d4e-8a11-111111111111",
            "at": stamp,
            "actor_kind": "staff",
            "source": "ui",
            "entity_type": "matter",
            "entity_id": matter,
            "kind": "update",
            "old_value": "prospect",
            "new_value": {"redacted": True},
            "reason": {"redacted": True},
        }
        audit_page = {"data": [audit_row, {**audit_row, "id": "018f1a00-7b3c-7d4e-8a11-222222222222", "old_value": {"redacted": True}, "new_value": "open", "reason": "status"}], "page": {"limit": 50, "next_cursor": None}}
        for operation_id in ("listAuditLog", "historyMatter"):
            response = _operation(self.spec, operation_id)["responses"]["200"]["content"]["application/json"]["schema"]
            _schema_validator(self.spec, response).validate(audit_page)
        file_history = {
            "data": [
                {"at": stamp, "actor_kind": "staff", "kind": "field_edit", "old_value": {"redacted": True}, "new_value": "motion.pdf"},
                {"at": stamp, "actor_kind": "staff", "kind": "version_save", "old_value": "draft.pdf", "new_value": "motion.pdf"},
            ],
            "page": {"limit": 50, "next_cursor": None},
        }
        _schema_validator(self.spec, _operation(self.spec, "historyFile")["responses"]["200"]["content"]["application/json"]["schema"]).validate(file_history)
        webhook_page = {
            "data": [
                {
                    "id": "018f1a00-7b3c-7d4e-8a11-111111111111",
                    "type": "matter.created",
                    "occurred_at": stamp,
                    "actor_kind": "staff",
                    "record_type": "matter",
                    "record_id": matter,
                    "matter_id": matter,
                    "summary": "Smith opened",
                    "task_authorization": None,
                    "processor_authorization": None,
                    "background_file_authorization": None,
                },
                {
                    "id": "018f1a00-7b3c-7d4e-8a11-222222222222",
                    "type": "matter.created",
                    "occurred_at": stamp,
                    "actor_kind": "staff",
                    "record_type": "matter",
                    "record_id": matter,
                    "summary": {"redacted": True},
                },
            ],
            "page": {"limit": 50, "next_cursor": None},
        }
        _schema_validator(self.spec, _operation(self.spec, "listWebhookDeliveries")["responses"]["200"]["content"]["application/json"]["schema"]).validate(webhook_page)
        result = {
            "file_id": matter,
            "file_version_id": actor,
            "summary": {"redacted": True},
            "name_parts": {"what_it_is": "motion"},
            "fields": [],
        }
        _component_validator(self.spec, "DocTenderResult").validate(result)
        _component_validator(self.spec, "DocTenderResult").validate({**result, "summary": "A motion"})
        _component_validator(self.spec, "RedactedText").validate({"redacted": True})

    def test_background_file_publication_grant(self):
        policy = self.spec["x-togavsu-module-delegation"]["noninteractive"]["background_file_publication"]
        self.assertFalse(policy["viewer_context_required"])
        self.assertFalse(policy["grants_admin"])
        for operation_id in ("createFileUpload", "putFileChunk", "completeFileUpload", "publishFile", "bulkFile"):
            operation = _operation(self.spec, operation_id)
            self.assertEqual(operation["x-togavsu-access"]["module_delegation"], "delegated_viewer_or_noninteractive", operation_id)
            self.assertEqual(operation["x-togavsu-access"]["noninteractive_workflow"], "background_file_publication", operation_id)
        issuance = _operation(self.spec, "issueFilePublicationGrant")
        self.assertEqual(issuance["x-togavsu-mcp"]["parity"], "required")
        self.assertEqual(issuance["x-togavsu-mcp"]["tool"], "issueFilePublicationGrant")
        self.assertEqual(issuance["x-togavsu-access"]["grant_issuance"], "background_file_publication")
        self.assertEqual(issuance["x-togavsu-access"]["authorization_category"], "service_principal_issuance")
        self.assertEqual(issuance["x-togavsu-access"]["module_delegation"], "service_principal")
        self.assertFalse(issuance["x-togavsu-access"]["viewer_required"])
        self.assertFalse(issuance["x-togavsu-access"]["task_grant_required"])
        self.assertFalse(issuance["x-togavsu-access"]["existing_grant_authorizes_issuance"])
        credentials = issuance["x-togavsu-mcp"]["transfer"]["credentials"]
        self.assertNotIn("task_authorization", credentials)
        self.assertNotIn("context_token", credentials)
        self.assertFalse(credentials["module_bearer"]["viewer_required"])
        self.assertFalse(credentials["module_bearer"]["task_grant_required"])
        self.assertNotIn("issueFilePublicationGrant", self.spec["x-togavsu-module-delegation"]["noninteractive"]["operations"])
        self.assertIn("issueFilePublicationGrant", self.spec["x-togavsu-module-delegation"]["service_principal_issuance"]["operations"])
        self.assertFalse(issuance["x-togavsu-access"]["grants_admin"])
        module = {"installed": True, "registered": True, "module_id": "email", "approved_scopes": ["files:write"]}
        job = {
            "module_id": "email",
            "source": "email",
            "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
            "expires_at": "2026-11-07T20:00:00Z",
            "matter_walled": False,
            "principal_matter_ids": ["018f1a00-7b3c-7d4e-8a11-333333333333"],
            "event_id": "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbbb",
        }
        issued = background_grant_issuance(issuance, module, job)
        self.assertEqual(issued["result"], "allowed")
        self.assertFalse(issued["file_created"])
        self.assertEqual(issued["events"], ["file.background_authorized"])
        _component_validator(self.spec, "BackgroundFileAuthorization").validate(issued["grant"])
        _component_validator(self.spec, "BackgroundFileGrantRequest").validate({"assignment": "matter", "matter_id": job["matter_id"], "source": "email"})
        delivered_grant = webhook_envelope(
            {**module, "subscriptions": ["file.background_authorized"], "on_wall": True, "attorney_only": False},
            issued["webhook_event"],
        )
        self.assertEqual(delivered_grant["background_file_authorization"]["matter_id"], job["matter_id"])
        _component_validator(self.spec, "WebhookEvent").validate(delivered_grant)
        self.assertIsNone(background_grant_issuance(issuance, {**module, "approved_scopes": ["matters:read"]}, job)["grant"])
        self.assertIsNone(background_grant_issuance(issuance, {**module, "installed": False}, job)["grant"])
        self.assertIsNone(background_grant_issuance(issuance, module, {**job, "matter_walled": True, "on_wall": False})["grant"])
        self.assertIsNone(background_grant_issuance(issuance, module, {**job, "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333334"})["grant"])
        renewed = background_grant_issuance(issuance, module, {**job, "previous_grant_expired": True, "viewer_present": False})
        self.assertEqual(renewed["result"], "allowed")
        _component_validator(self.spec, "BackgroundFileAuthorization").validate(renewed["grant"])
        blocked_grant = background_grant_issuance(issuance, module, {**job, "task_authorization": {"module_id": "email"}, "viewer_present": False})
        self.assertEqual(blocked_grant["result"], "forbidden")
        self.assertIsNone(blocked_grant["grant"])
        self.assertIsNone(background_grant_issuance(issuance, module, {**job, "module_id": "zoom"})["grant"])
        grant = issued["grant"]
        matter = job["matter_id"]
        store = {"uploads": {}, "files": {}, "events": []}
        base = {"grant": grant, "caller_module_id": "email", "module_scopes": ["files:write"], "matter_id": matter, "viewer_present": False}
        created = background_file_step(store, policy, _operation(self.spec, "createFileUpload"), {**base, "operation": "createFileUpload"})
        self.assertEqual(created["result"], "allowed")
        _component_validator(self.spec, "FileUpload").validate(created["upload"])
        self.assertEqual(created["upload"]["owner"], {"owner_kind": "module", "owner_module_id": "email"})
        user_upload = {
            "id": "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaaa",
            "owner": {"owner_kind": "user", "owner_user_id": "018f1a00-7b3c-7d4e-8a11-555555555555"},
            "chunk_size_bytes": 8388608,
            "next_offset": 0,
            "size_bytes": 0,
            "expires_at": "2026-11-07T20:00:00Z",
            "status": "receiving",
        }
        _component_validator(self.spec, "FileUpload").validate(user_upload)
        with self.assertRaises(Exception):
            _component_validator(self.spec, "FileUpload").validate({key: value for key, value in user_upload.items() if key != "owner"})
        upload_id = created["upload_id"]
        self.assertEqual(background_file_step(store, policy, _operation(self.spec, "putFileChunk"), {**base, "operation": "putFileChunk", "upload_id": upload_id})["events"], [])
        self.assertEqual(background_file_step(store, policy, _operation(self.spec, "completeFileUpload"), {**base, "operation": "completeFileUpload", "upload_id": upload_id})["result"], "allowed")
        published = background_file_step(store, policy, _operation(self.spec, "publishFile"), {**base, "operation": "publishFile", "upload_id": upload_id})
        self.assertEqual(published["result"], "allowed")
        self.assertTrue(published["file_created"])
        self.assertIn("file.version_created", published["events"])
        self.assertEqual(len(store["files"]), 1)
        denied = background_file_step(store, policy, _operation(self.spec, "publishFile"), {**base, "operation": "publishFile", "upload_id": upload_id, "matter_id": "matter-2"})
        self.assertEqual(denied["result"], "not_found")
        self.assertEqual(len(store["files"]), 1)
        expired = background_file_step(store, policy, _operation(self.spec, "publishFile"), {**base, "operation": "publishFile", "upload_id": upload_id, "grant": {**grant, "expired": True}})
        self.assertEqual(expired["result"], "forbidden")
        self.assertEqual(len(store["files"]), 1)
        staged = background_file_step(store, policy, _operation(self.spec, "createFileUpload"), {**base, "operation": "createFileUpload", "upload_id": "up-staged"})
        self.assertEqual(background_file_step(store, policy, _operation(self.spec, "completeFileUpload"), {**base, "operation": "completeFileUpload", "upload_id": staged["upload_id"]})["result"], "allowed")
        other = background_file_step(store, policy, _operation(self.spec, "createFileUpload"), {**base, "operation": "createFileUpload", "upload_id": "up-open"})
        before_files = len(store["files"])
        before_events = len(store["events"])
        bulk_op = _operation(self.spec, "bulkFile")
        for label, items in (
            ("missing-upload", [{"upload_id": "up-unknown", "matter_id": matter}]),
            ("not-in-grant", [{"upload_id": "foreign", "matter_id": matter}]),
            ("omitted-matter", [{"upload_id": staged["upload_id"]}]),
            ("unfinished", [{"upload_id": other["upload_id"], "matter_id": matter}]),
            ("second-item-unfinished", [{"upload_id": staged["upload_id"], "matter_id": matter}, {"upload_id": other["upload_id"], "matter_id": matter}]),
            ("duplicate-id", [{"upload_id": staged["upload_id"], "matter_id": matter}, {"upload_id": staged["upload_id"], "matter_id": matter}]),
        ):
            if label == "not-in-grant":
                store["uploads"]["foreign"] = {"id": "foreign", "status": "staged", "matter_id": matter, "owner": "email"}
            denied_bulk = background_file_step(store, policy, bulk_op, {**base, "operation": "bulkFile", "items": items})
            self.assertNotEqual(denied_bulk["result"], "allowed", label)
            self.assertEqual(denied_bulk["events"], [], label)
            self.assertEqual(len(store["files"]), before_files, label)
            self.assertEqual(len(store["events"]), before_events, label)
        store["uploads"][staged["upload_id"]]["matter_id"] = "matter-9"
        wrong_matter = background_file_step(store, policy, bulk_op, {**base, "operation": "bulkFile", "items": [{"upload_id": staged["upload_id"], "matter_id": matter}]})
        self.assertEqual(wrong_matter["result"], "not_found")
        self.assertEqual(len(store["files"]), before_files)
        self.assertEqual(len(store["events"]), before_events)
        store["uploads"][staged["upload_id"]]["matter_id"] = matter
        store["uploads"][staged["upload_id"]]["owner"] = "zoom"
        stolen = background_file_step(store, policy, bulk_op, {**base, "operation": "bulkFile", "items": [{"upload_id": staged["upload_id"], "matter_id": matter}]})
        self.assertEqual(stolen["result"], "forbidden")
        self.assertEqual(len(store["files"]), before_files)
        store["uploads"][staged["upload_id"]]["owner"] = "email"
        published_bulk = background_file_step(store, policy, bulk_op, {**base, "operation": "bulkFile", "items": [{"upload_id": staged["upload_id"], "matter_id": matter}]})
        self.assertEqual(published_bulk["result"], "allowed")
        version_events = [event for event in published_bulk["events"] if event == "file.version_created"]
        self.assertEqual(version_events, ["file.version_created"])
        replayed = background_file_step(store, policy, bulk_op, {**base, "operation": "bulkFile", "items": [{"upload_id": staged["upload_id"], "matter_id": matter}]})
        self.assertEqual(replayed["result"], "original_result")
        self.assertEqual(replayed["events"], [])
        self.assertEqual(len(store["files"]), before_files + 1)
        raced = commit_concurrent_publishes(policy, _operation(self.spec, "publishFile"), grant, upload_id)
        self.assertEqual(len(raced["store"]["files"]), 1)
        self.assertEqual(raced["second"]["result"], "original_result")
        self.assertFalse(raced["second"]["file_created"])
        for source in ("zoom", "importer", "drop_folder"):
            slug = "drop-folder" if source == "drop_folder" else source
            other = background_grant_issuance(issuance, {**module, "module_id": slug}, {**job, "module_id": slug, "source": source})
            self.assertEqual(other["result"], "allowed", source)
            self.assertFalse(other["file_created"], source)
        unassigned_job = {"module_id": "email", "assignment": "unassigned", "source": "importer", "expires_at": "2026-11-07T20:00:00Z", "viewer_present": False}
        unassigned = background_grant_issuance(issuance, module, unassigned_job)
        self.assertEqual(unassigned["result"], "allowed")
        self.assertIsNone(unassigned["grant"]["matter_id"])
        _component_validator(self.spec, "BackgroundFileAuthorization").validate(unassigned["grant"])
        _component_validator(self.spec, "BackgroundFileGrantRequest").validate({"assignment": "unassigned", "source": "importer"})
        self.assertIsNone(background_grant_issuance(issuance, module, {**unassigned_job, "matter_id": matter})["grant"])
        ingest = {"uploads": {}, "files": {}, "events": []}
        ingest_base = {"grant": unassigned["grant"], "caller_module_id": "email", "module_scopes": ["files:write"], "matter_id": None, "viewer_present": False}
        staged_open = background_file_step(ingest, policy, _operation(self.spec, "createFileUpload"), {**ingest_base, "operation": "createFileUpload", "upload_id": "018f1a00-7b3c-7d4e-8a11-abababababab"})
        self.assertEqual(staged_open["upload"]["owner"]["owner_kind"], "module")
        _component_validator(self.spec, "FileUpload").validate(staged_open["upload"])
        self.assertEqual(background_file_step(ingest, policy, _operation(self.spec, "completeFileUpload"), {**ingest_base, "operation": "completeFileUpload", "upload_id": staged_open["upload_id"]})["upload"]["status"], "staged")
        published_open = background_file_step(ingest, policy, _operation(self.spec, "publishFile"), {**ingest_base, "operation": "publishFile", "upload_id": staged_open["upload_id"]})
        self.assertEqual(published_open["result"], "allowed")
        self.assertIsNone(published_open["file"]["matter_id"])
        assigned = background_file_step(ingest, policy, _operation(self.spec, "publishFile"), {**ingest_base, "operation": "publishFile", "upload_id": staged_open["upload_id"], "assign_matter_id": matter})
        self.assertEqual(assigned["result"], "forbidden")
        self.assertIsNone(ingest["files"][staged_open["upload_id"]]["matter_id"])
        review = background_file_step(ingest, policy, _operation(self.spec, "reviewUnassignedFile"), {**ingest_base, "operation": "reviewUnassignedFile", "upload_id": staged_open["upload_id"]})
        self.assertEqual(review["result"], "allowed")
        self.assertEqual(review["matching"], {"status": "unassigned", "matter_id": None})
        self.assertEqual(_operation(self.spec, "reviewUnassignedFile")["x-togavsu-mcp"]["parity"], "required")
        foreign = background_file_step(ingest, policy, _operation(self.spec, "reviewUnassignedFile"), {**ingest_base, "operation": "reviewUnassignedFile", "upload_id": staged_open["upload_id"], "caller_module_id": "zoom"})
        self.assertEqual(foreign["result"], "forbidden")

    def test_trust_application_selects_the_account_and_client(self):
        from jsonschema import Draft202012Validator

        apply = _component_validator(self.spec, "ApplyTrust")
        flat = _component_validator(self.spec, "FlatFeeApply")
        client = "018f1a00-7b3c-7d4e-8a11-222222222222"
        other_client = "018f1a00-7b3c-7d4e-8a11-222222222223"
        matter = "018f1a00-7b3c-7d4e-8a11-333333333333"
        account_a = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00001"
        account_b = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00002"
        body = {"client_id": client, "trust_account_id": account_a, "amount_cents": 1500}
        apply.validate(body)
        flat.validate(body)
        with self.assertRaises(Exception):
            apply.validate({"client_id": client, "amount_cents": 1500})
        with self.assertRaises(Exception):
            flat.validate({"amount_cents": 1500})
        balances = [
            {"trust_account_id": account_a, "client_id": client, "matter_id": matter, "balance_cents": 5000, "active": True, "authorized": True, "firm_id": "firm-1"},
            {"trust_account_id": account_b, "client_id": client, "matter_id": matter, "balance_cents": 8000, "active": True, "authorized": True, "firm_id": "firm-1"},
        ]
        call = {"role": "attorney", "token_scopes": ["trust:withdraw"], "client_id": client, "trust_account_id": account_a, "matter_id": matter, "amount_cents": 1500, "balances": balances, "firm_id": "firm-1", "matter_walled": False}
        chosen = trust_debit_decision(call)
        self.assertEqual(chosen["result"], "allowed")
        self.assertEqual(chosen["debited_account_id"], account_a)
        self.assertNotEqual(chosen["debited_account_id"], account_b)
        other = trust_debit_decision({**call, "trust_account_id": account_b, "client_id": other_client})
        self.assertEqual(other["result"], "not_found")
        self.assertFalse(other["ledger_mutated"])
        for label, patch, expected in (
            ("missing-account", {"trust_account_id": None}, "validation_failed"),
            ("missing-client", {"client_id": None}, "validation_failed"),
            ("scope", {"token_scopes": ["trust:read"]}, "scope_denied"),
            ("wall", {"matter_walled": True, "on_wall": False}, "not_found"),
            ("staff", {"role": "staff"}, "forbidden"),
            ("inactive", {"balances": [{**balances[0], "active": False}, balances[1]]}, "forbidden"),
        ):
            decision = trust_debit_decision({**call, **patch})
            self.assertEqual(decision["result"], expected, label)
            self.assertFalse(decision["ledger_mutated"], label)
        self.assertEqual(trust_debit_decision({**call, "trust_account_id": account_b})["debited_account_id"], account_b)
        for operation_id in ("applyTrustToInvoice", "applyFlatFee"):
            rule = _operation(self.spec, operation_id)["x-togavsu-access"]["trust_debit"]
            self.assertEqual(rule["trust_account_id"], "required", operation_id)
            self.assertEqual(rule["client_id"], "required", operation_id)
            self.assertEqual(rule["other_accounts"], "unchanged", operation_id)
            self.assertEqual(rule["authorize_before"], "any_mutation", operation_id)

    def test_module_marker_registration_owns_its_namespace(self):
        firm = _operation(self.spec, "createMarkerDefinition")
        self.assertEqual(firm["x-togavsu-access"]["roles"], ["admin"])
        self.assertEqual(firm["x-togavsu-access"]["scopes"], ["settings:write"])
        self.assertNotIn("module_principal", firm["x-togavsu-access"])
        self.assertNotIn("markers:write", firm["x-togavsu-access"]["scopes"])
        denied_settings = decide_module_permissions(
            {"id": "intake", "version": "0.1.0", "permissions": ["settings:write"]},
            [{"scope": "settings:write", "decision": "approve"}],
            signature_verified=True,
        )
        self.assertEqual(denied_settings["result"], "validation_failed")
        installed = decide_module_permissions(
            {"id": "intake", "version": "0.1.0", "permissions": ["markers:write"]},
            [{"scope": "markers:write", "decision": "approve"}],
            signature_verified=True,
        )
        self.assertEqual(installed["result"], "installed")
        self.assertEqual(installed["token_scopes"], ["markers:write"])
        unsigned = decide_module_permissions(
            {"id": "intake", "version": "0.1.0", "permissions": ["markers:write"]},
            [{"scope": "markers:write", "decision": "approve"}],
            signature_verified=False,
        )
        self.assertNotEqual(unsigned["result"], "installed")
        tools = {tool["name"]: tool for tool in render_mcp(self.spec)["tools"]}
        for operation_id in ("createModuleMarker", "updateModuleMarker", "revokeModuleMarker"):
            operation = _operation(self.spec, operation_id)
            self.assertEqual(operation["x-togavsu-mcp"]["parity"], "required", operation_id)
            self.assertEqual(operation["x-togavsu-mcp"]["tool"], operation_id, operation_id)
            self.assertEqual(operation["x-togavsu-access"]["roles"], [], operation_id)
            self.assertEqual(operation["x-togavsu-access"]["scopes"], ["markers:write"], operation_id)
            self.assertNotIn("settings:write", operation["x-togavsu-access"]["scopes"], operation_id)
            principal = operation["x-togavsu-access"]["module_principal"]
            self.assertFalse(principal["grants_admin"], operation_id)
            self.assertTrue(principal["own_namespace_only"], operation_id)
            self.assertEqual(principal["ownership"], "signed_module_principal", operation_id)
            self.assertEqual(principal["scope"], "markers:write", operation_id)
            self.assertIn(operation_id, tools, operation_id)
            self.assertEqual(tools[operation_id]["scopes"], operation["x-togavsu-access"]["scopes"], operation_id)
            self.assertEqual(tools[operation_id]["roles"], operation["x-togavsu-access"]["roles"], operation_id)
            self.assertNotEqual(operation["security"], [], operation_id)
        write = _component_validator(self.spec, "ModuleMarkerWrite")
        write.validate({"label": "After hours", "color": "#336699", "target": "intake/header"})
        for body in (
            {"label": "After hours", "color": "red"},
            {"label": "<script>", "color": "#336699"},
            {"label": "After hours", "color": "#336699", "target": "https://evil.example"},
            {"label": "After hours", "color": "#336699", "target": "javascript:alert(1)"},
            {"label": "After hours", "color": "#336699", "module_id": "other-mod"},
        ):
            with self.assertRaises(Exception):
                write.validate(body)
        create = _operation(self.spec, "createModuleMarker")
        update = _operation(self.spec, "updateModuleMarker")
        revoke = _operation(self.spec, "revokeModuleMarker")
        self.assertEqual(module_marker_registration(firm, {"definitions": []}, {"actor": "module", "module_id": "intake", "path_module_id": "intake", "scopes": ["markers:write"], "action": "create"}), {"result": "forbidden", "definition": None, "events": [], "mutated": False})
        marker_id = "018f1a00-7b3c-7d4e-8a11-111111111111"
        other_id = "018f1a00-7b3c-7d4e-8a11-222222222222"
        firm_id = "018f1a00-7b3c-7d4e-8a11-333333333333"
        actor = "018f1a00-7b3c-7d4e-8a11-444444444444"
        matter_id = "018f1a00-7b3c-7d4e-8a11-555555555555"
        stamp = "2026-11-07T18:00:00Z"
        own = {
            "actor": "module",
            "module_id": "intake",
            "path_module_id": "intake",
            "scopes": installed["token_scopes"],
            "action": "create",
            "marker_id": marker_id,
            "label": "After hours",
            "color": "#336699",
            "target": "intake/header",
            "created_at": stamp,
            "created_by": actor,
            "committed": True,
        }
        store = {"definitions": [{"id": firm_id, "label": "Pro bono", "color": "#112233", "active": True, "module_id": None, "namespace": "firm", "target": None, "placement": "matter-header", "created_at": stamp, "created_by": actor, "updated_at": stamp}], "attachments": []}
        for label, call in (
            ("settings", {**own, "scopes": ["settings:write"]}),
            ("admin", {**own, "role": "admin", "human_session": True, "actor": "human", "scopes": ["markers:write", "settings:write"]}),
            ("human", {**own, "actor": "human", "human_session": True}),
            ("other-path", {**own, "path_module_id": "other-mod"}),
            ("owner", {**own, "owner_module_id": "other-mod"}),
            ("traversal", {**own, "module_id": "../intake", "path_module_id": "../intake"}),
        ):
            denied = module_marker_registration(create, store, call)
            self.assertIn(denied["result"], {"forbidden", "scope_denied"}, label)
            self.assertEqual(denied["events"], [], label)
            self.assertFalse(denied["mutated"], label)
        self.assertEqual(len(store["definitions"]), 1)
        registered = module_marker_registration(create, store, own)
        self.assertEqual(registered["result"], "allowed")
        self.assertEqual(registered["events"], [{"type": "marker.created", "marker_id": marker_id, "module_id": "intake"}])
        self.assertEqual(registered["definition"]["module_id"], "intake")
        self.assertEqual(registered["definition"]["namespace"], "intake")
        self.assertEqual(registered["definition"]["color"], "#336699")
        self.assertEqual(registered["definition"]["target"], "/m/intake/intake/header")
        self.assertEqual(registered["definition"]["placement"], "matter-header")
        _component_validator(self.spec, "ModuleMarker").validate(registered["definition"])
        duplicate = module_marker_registration(create, store, {**own, "marker_id": other_id, "label": " after   HOURS "})
        self.assertEqual(duplicate["result"], "conflict")
        self.assertEqual(duplicate["events"], [])
        self.assertEqual(len(store["definitions"]), 2)
        bad_color = module_marker_registration(create, store, {**own, "marker_id": other_id, "label": "Weekend", "color": "red"})
        self.assertEqual(bad_color["result"], "validation_failed")
        bad_target = module_marker_registration(create, store, {**own, "marker_id": other_id, "label": "Weekend", "target": "javascript:alert(1)"})
        self.assertEqual(bad_target["result"], "validation_failed")
        self.assertEqual(len(store["definitions"]), 2)
        rolled = module_marker_registration(create, store, {**own, "marker_id": other_id, "label": "Weekend", "committed": False})
        self.assertEqual(rolled["result"], "rolled_back")
        self.assertEqual(rolled["events"], [])
        replay = module_marker_registration(create, store, {**own, "marker_id": other_id, "label": "Weekend", "replay": True})
        self.assertEqual(replay["result"], "original_result")
        self.assertEqual(replay["events"], [])
        self.assertEqual(len(store["definitions"]), 2)
        firm_edit = module_marker_registration(update, store, {**own, "action": "update", "marker_id": firm_id, "label": "Stolen", "scopes": ["markers:write", "settings:write"]})
        self.assertEqual(firm_edit["result"], "forbidden")
        self.assertEqual(store["definitions"][0]["label"], "Pro bono")
        other_store_id = "018f1a00-7b3c-7d4e-8a11-666666666666"
        store["definitions"].append({"id": other_store_id, "label": "Foreign", "color": "#abcdef", "active": True, "module_id": "other-mod", "namespace": "other-mod", "target": None, "placement": "matter-header", "created_at": stamp, "created_by": actor, "updated_at": stamp})
        foreign = module_marker_registration(update, store, {**own, "action": "update", "marker_id": other_store_id, "color": "#000000"})
        self.assertEqual(foreign["result"], "forbidden")
        self.assertEqual(store["definitions"][-1]["color"], "#abcdef")
        recolored = module_marker_registration(update, store, {**own, "action": "update", "marker_id": marker_id, "color": "#AABBCC"})
        self.assertEqual(recolored["result"], "allowed")
        self.assertEqual(recolored["definition"]["color"], "#aabbcc")
        self.assertEqual(recolored["definition"]["label"], "After hours")
        self.assertEqual(recolored["events"][0]["type"], "marker.updated")
        second = module_marker_registration(create, store, {**own, "marker_id": other_id, "label": "Weekend", "target": None})
        self.assertEqual(second["result"], "allowed")
        clash = module_marker_registration(update, store, {**own, "action": "update", "marker_id": other_id, "label": "After hours"})
        self.assertEqual(clash["result"], "conflict")
        self.assertEqual(next(row for row in store["definitions"] if row["id"] == other_id)["label"], "Weekend")
        revoked = module_marker_registration(revoke, store, {**own, "action": "revoke", "marker_id": marker_id})
        self.assertEqual(revoked["result"], "allowed")
        self.assertFalse(revoked["definition"]["active"])
        self.assertEqual(revoked["events"][0]["type"], "marker.revoked")
        again = module_marker_registration(revoke, store, {**own, "action": "revoke", "marker_id": marker_id})
        self.assertEqual(again["result"], "original_result")
        self.assertEqual(again["events"], [])
        firm_revoke = module_marker_registration(revoke, store, {**own, "action": "revoke", "marker_id": firm_id})
        self.assertEqual(firm_revoke["result"], "forbidden")
        self.assertTrue(store["definitions"][0]["active"])
        reused = module_marker_registration(create, store, {**own, "marker_id": "018f1a00-7b3c-7d4e-8a11-777777777777", "label": "After hours"})
        self.assertEqual(reused["result"], "allowed")
        staff = {"role": "staff", "token_scopes": ["matters:write"], "matter_id": matter_id, "marker_id": reused["definition"]["id"], "matter_walled": False, "committed": True}
        pinned = attach_matter_marker(store, staff)
        self.assertEqual(pinned["result"], "allowed")
        self.assertEqual(pinned["events"], ["matter.updated"])
        self.assertEqual(store["attachments"], [{"matter_id": matter_id, "marker_id": reused["definition"]["id"]}])
        repeat = attach_matter_marker(store, {**staff, "replay": True})
        self.assertEqual(repeat["result"], "original_result")
        self.assertEqual(repeat["events"], [])
        self.assertEqual(len(store["attachments"]), 1)
        hidden = attach_matter_marker(store, {**staff, "matter_id": "018f1a00-7b3c-7d4e-8a11-888888888888", "matter_walled": True, "on_wall": False, "matter_membership": {"018f1a00-7b3c-7d4e-8a11-888888888888": False}})
        self.assertEqual(hidden["result"], "not_found")
        self.assertEqual(hidden["events"], [])
        self.assertEqual(len(store["attachments"]), 1)
        dead = attach_matter_marker(store, {**staff, "marker_id": marker_id})
        self.assertEqual(dead["result"], "validation_failed")
        self.assertEqual(len(store["attachments"]), 1)

    def test_trust_flag_follows_the_committed_balance(self):
        writers = (
            "postTrustEntry",
            "bulkTrustEntry",
            "reverseTrustEntry",
            "applyTrustToInvoice",
            "applyFlatFee",
            "reversePayment",
            "createPayment",
            "bulkPayment",
            "updatePaymentStatus",
        )
        metadata = ["matter_id", "client_id", "trust_account_id", "balance_cents", "trust_entry_id", "balance_scope"]
        for operation_id in writers:
            operation = _operation(self.spec, operation_id)
            rule = operation["x-togavsu-access"]["trust_flag"]
            self.assertEqual(rule["selection"], "resulting_balance", operation_id)
            self.assertFalse(rule["blocks"], operation_id)
            self.assertEqual(rule["below_zero"], "posts", operation_id)
            self.assertEqual(rule["webhook_scope"], "trust:read", operation_id)
            self.assertEqual(rule["metadata"], metadata, operation_id)
            self.assertIn("trust.entry_posted", operation["x-togavsu-events"], operation_id)
            self.assertIn("trust.flag_raised", operation["x-togavsu-events"], operation_id)
        background = self.spec["x-togavsu-background-events"]["trust.flag_raised"]
        self.assertEqual(background["scopes"], ["trust:read"])
        self.assertEqual(background["metadata"], metadata)
        self.assertEqual(background["below_zero"], "warning")
        self.assertEqual(event_read_scopes("trust.flag_raised"), ["trust:read"])
        self.assertEqual(event_read_scopes("trust.entry_posted"), ["trust:read"])
        self.assertNotIn("below_zero", self.spec["components"]["schemas"]["Problem"]["properties"]["code"]["enum"])
        matter = "018f1a00-7b3c-7d4e-8a11-333333333333"
        other_matter = "018f1a00-7b3c-7d4e-8a11-333333333334"
        client = "018f1a00-7b3c-7d4e-8a11-222222222222"
        account = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00001"
        entry_a = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1"
        entry_b = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa2"
        entry_c = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa3"

        def row(entry, amount, matter_id=matter, opening=None, kind="deposit"):
            item = {
                "amount_cents": amount,
                "entry_type": kind,
                "matter_id": matter_id,
                "client_id": client,
                "trust_account_id": account,
                "trust_entry_id": entry,
                "method": "check",
                "description": kind,
            }
            if kind == "reversal":
                item["direction"] = "debit" if amount < 0 else "credit"
            if kind == "adjustment":
                item["adjustment_direction"] = "credit" if amount > 0 else "debit"
            if opening is not None:
                item["opening"] = opening
            return item

        def write_body(item):
            body = {
                "trust_account_id": item["trust_account_id"],
                "client_id": item["client_id"],
                "matter_id": item["matter_id"],
                "entry_type": item["entry_type"],
                "amount_cents": item["amount_cents"],
                "method": item["method"],
                "description": item["description"],
            }
            if item["entry_type"] == "adjustment":
                body["adjustment_direction"] = item["adjustment_direction"]
            return body

        opening = {"matter_cents": 100, "client_cents": 100, "account_cents": 500}
        post = _operation(self.spec, "postTrustEntry")
        bulk = _operation(self.spec, "bulkTrustEntry")
        reverse = _operation(self.spec, "reverseTrustEntry")
        safe_item = row(entry_a, 25, opening={"matter_cents": 0, "client_cents": 0, "account_cents": 0})
        _component_validator(self.spec, "TrustEntryWrite").validate(write_body(safe_item))
        safe = trust_ledger_events(post, [safe_item], committed=True)
        self.assertEqual([event["type"] for event in safe], ["trust.entry_posted"])
        flagged_item = row(entry_a, 25, opening={"matter_cents": 0, "client_cents": 0, "account_cents": 0}, kind="withdrawal")
        _component_validator(self.spec, "TrustEntryWrite").validate(write_body(flagged_item))
        flagged = trust_ledger_events(post, [flagged_item], committed=True)
        self.assertEqual([event["type"] for event in flagged], ["trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(flagged[1]["balance_cents"], -25)
        self.assertEqual(flagged[1]["balance_scope"], "matter")
        canonical = row(entry_b, 1500, opening={"matter_cents": 1000, "client_cents": 1000, "account_cents": 1000}, kind="withdrawal")
        _component_validator(self.spec, "TrustEntryWrite").validate(write_body(canonical))
        canonical_events = trust_ledger_events(post, [canonical], committed=True)
        self.assertEqual(canonical_events[0]["balances"]["matter_cents"], -500)
        self.assertEqual([event["type"] for event in canonical_events], ["trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(canonical_events[1]["balance_cents"], -500)
        for kind in ("refund", "transfer_to_operating"):
            typed = row(entry_c, 1500, opening={"matter_cents": 1000, "client_cents": 1000, "account_cents": 1000}, kind=kind)
            _component_validator(self.spec, "TrustEntryWrite").validate(write_body(typed))
            typed_events = trust_ledger_events(post, [typed], committed=True)
            self.assertEqual(typed_events[0]["balances"]["matter_cents"], -500, kind)
            self.assertEqual(typed_events[1]["balance_scope"], "matter", kind)
        invalid = {"entry_type": "withdrawal", "amount_cents": -1500, "matter_id": matter, "client_id": client, "trust_account_id": account, "trust_entry_id": entry_a, "method": "check", "description": "bad", "opening": {"matter_cents": 1000, "client_cents": 1000, "account_cents": 1000}}
        with self.assertRaises(Exception):
            _component_validator(self.spec, "TrustEntryWrite").validate(write_body(invalid))
        self.assertEqual(trust_ledger_events(post, [invalid], committed=True), [])
        for label, kwargs in (("rollback", {"committed": False}), ("replay", {"committed": True, "replay": True}), ("denied", {"committed": True, "denied": True})):
            self.assertEqual(trust_ledger_events(post, [row(entry_a, 25, opening=opening, kind="withdrawal")], **kwargs), [], label)
            self.assertEqual(trust_ledger_events(bulk, [row(entry_a, 25, opening=opening, kind="withdrawal")], **kwargs), [], label)
            self.assertEqual(trust_ledger_events(reverse, [row(entry_a, 25, opening=opening, kind="withdrawal")], **kwargs), [], label)
        ordered_items = [row(entry_a, 40, opening=opening, kind="withdrawal"), row(entry_b, 80, kind="withdrawal"), row(entry_c, 5)]
        for item in ordered_items:
            _component_validator(self.spec, "TrustEntryWrite").validate(write_body(item))
        ordered = trust_ledger_events(bulk, ordered_items, committed=True)
        self.assertEqual([event["type"] for event in ordered], ["trust.entry_posted", "trust.entry_posted", "trust.flag_raised", "trust.entry_posted", "trust.flag_raised"])
        self.assertEqual([event["balance_cents"] for event in ordered if event["type"] == "trust.flag_raised"], [-20, -15])
        separate = trust_ledger_events(
            bulk,
            [
                row(entry_a, 5, opening={"matter_cents": 10, "client_cents": 10, "account_cents": 10}, kind="withdrawal"),
                row(entry_b, 5, matter_id=other_matter, opening={"matter_cents": 100, "client_cents": 0, "account_cents": 0}, kind="withdrawal"),
            ],
            committed=True,
        )
        self.assertEqual([event["type"] for event in separate], ["trust.entry_posted", "trust.entry_posted"])
        deposit_reversal = trust_ledger_events(reverse, [row(entry_a, -250, opening={"matter_cents": 200, "client_cents": 200, "account_cents": 200}, kind="reversal")], committed=True)
        self.assertEqual([event["type"] for event in deposit_reversal], ["trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(deposit_reversal[1]["balance_cents"], -50)
        client_only = trust_ledger_events(
            post,
            [row(entry_a, 10, opening={"matter_cents": 50, "client_cents": 5, "account_cents": 80}, kind="withdrawal")],
            committed=True,
        )
        self.assertEqual(client_only[1]["balance_scope"], "client")
        self.assertEqual(client_only[1]["balance_cents"], -5)
        account_a = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00011"
        account_b = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00012"
        matter_a = "018f1a00-7b3c-7d4e-8a11-333333333341"
        matter_b = "018f1a00-7b3c-7d4e-8a11-333333333342"
        mixed_accounts = trust_ledger_events(
            bulk,
            [
                {"entry_type": "withdrawal", "amount_cents": 30, "method": "check", "description": "withdrawal", "matter_id": matter_a, "client_id": client, "trust_account_id": account_a, "trust_entry_id": entry_a, "opening": {"matter_cents": 100, "client_cents": 50, "account_cents": 100}},
                {"entry_type": "withdrawal", "amount_cents": 30, "method": "check", "description": "withdrawal", "matter_id": matter_b, "client_id": client, "trust_account_id": account_b, "trust_entry_id": entry_b, "opening": {"matter_cents": 80, "client_cents": 50, "account_cents": 80}},
            ],
            committed=True,
        )
        self.assertEqual([event["type"] for event in mixed_accounts], ["trust.entry_posted", "trust.entry_posted", "trust.flag_raised"])
        same_matter_items = [
            {"entry_type": "withdrawal", "amount_cents": 30, "method": "check", "description": "withdrawal", "matter_id": matter, "client_id": client, "trust_account_id": account_a, "trust_entry_id": entry_a, "opening": {"matter_cents": 40, "client_cents": 200, "account_cents": 100}},
            {"entry_type": "withdrawal", "amount_cents": 30, "method": "check", "description": "withdrawal", "matter_id": matter, "client_id": client, "trust_account_id": account_b, "trust_entry_id": entry_b, "opening": {"matter_cents": 1000, "client_cents": 200, "account_cents": 100}},
        ]
        same_matter = trust_ledger_events(bulk, same_matter_items, committed=True)
        for item in same_matter_items:
            _component_validator(self.spec, "TrustEntryWrite").validate(write_body(item))
        self.assertEqual([event["type"] for event in same_matter], ["trust.entry_posted", "trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(same_matter[2]["balance_scope"], "matter")
        self.assertEqual(same_matter[2]["balance_cents"], -20)
        self.assertGreaterEqual(same_matter[2]["balances"]["client_cents"], 0)
        self.assertGreaterEqual(same_matter[2]["balances"]["account_cents"], 0)
        self.assertEqual(mixed_accounts[2]["balance_scope"], "client")
        self.assertEqual(mixed_accounts[2]["balance_cents"], -10)
        self.assertEqual(mixed_accounts[2]["client_id"], client)
        self.assertGreaterEqual(mixed_accounts[0]["balance_cents"], 0)
        self.assertGreaterEqual(mixed_accounts[1]["balance_cents"], 0)
        debit = trust_debit_decision(
            {
                "role": "attorney",
                "token_scopes": ["trust:withdraw"],
                "client_id": client,
                "trust_account_id": account,
                "matter_id": matter,
                "amount_cents": 1500,
                "balance_after": -20,
                "trust_entry_id": entry_a,
                "matter_walled": False,
                "balances": [{"trust_account_id": account, "client_id": client, "matter_id": matter, "balance_cents": 1480, "active": True, "authorized": True, "firm_id": "firm-1"}],
                "firm_id": "firm-1",
            }
        )
        self.assertEqual(debit["result"], "allowed")
        self.assertTrue(debit["below_zero"])
        self.assertEqual([event["type"] for event in debit["events"]], ["trust.entry_posted", "trust.flag_raised"])
        covered_call = {"role": "attorney", "token_scopes": ["trust:withdraw"], "client_id": client, "trust_account_id": account, "matter_id": matter, "amount_cents": 1500, "balance_after": 100, "matter_walled": False, "balances": [{"trust_account_id": account, "client_id": client, "matter_id": matter, "balance_cents": 1600, "active": True, "authorized": True, "firm_id": "firm-1"}], "firm_id": "firm-1"}
        covered = trust_debit_decision(covered_call)
        self.assertEqual([event["type"] for event in covered["events"]], ["trust.entry_posted"])
        refused = trust_debit_decision({**covered_call, "token_scopes": ["trust:read"]})
        self.assertEqual(refused["events"], [])
        self.assertFalse(refused["ledger_mutated"])
        account_row = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00001"
        accounts = [{"id": account_row, "active": True, "authorized": True, "firm_id": "firm-1"}]
        compensation = {
            "client_id": client,
            "matter_id": matter,
            "amount_cents": 1500,
            "trust_entry_id": entry_a,
            "created_by": "018f1a00-7b3c-7d4e-8a11-555555555555",
            "posted_entry_id": entry_b,
            "created_at": "2026-11-07T18:00:00Z",
            "original_entry": {"id": entry_a, "entry_type": "deposit", "amount_cents": 1500},
        }
        reversal = payment_reversal_decision(
            _operation(self.spec, "reversePayment"),
            {
                "role": "attorney",
                "token_scopes": ["payments:write", "trust:withdraw"],
                "trust_linked": True,
                "matter_walled": False,
                "trust_account_id": account_row,
                "trust_accounts": accounts,
                "firm_id": "firm-1",
                "opening": {"matter_cents": 200, "client_cents": 200, "account_cents": 200},
                **compensation,
            },
        )
        self.assertEqual(reversal["result"], "allowed")
        self.assertTrue(reversal["ledger_mutated"])
        self.assertTrue(reversal["flag"])
        self.assertEqual([event["type"] for event in reversal["events"]], ["trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(reversal["trust_post"]["balances"], {"matter_cents": -1300, "client_cents": -1300, "account_cents": -1300})
        self.assertEqual(reversal["trust_post"]["flags"][0]["balance_cents"], -1300)
        self.assertEqual(reversal["trust_post"]["flags"][0]["scope"], "matter")
        _component_validator(self.spec, "TrustPostResult").validate(reversal["trust_post"])
        restored = payment_reversal_decision(
            _operation(self.spec, "reversePayment"),
            {
                "role": "attorney",
                "token_scopes": ["payments:write", "trust:withdraw"],
                "trust_linked": True,
                "matter_walled": False,
                "trust_account_id": account_row,
                "trust_accounts": accounts,
                "firm_id": "firm-1",
                "opening": {"matter_cents": 5000, "client_cents": 5000, "account_cents": 5000},
                **compensation,
            },
        )
        self.assertEqual(restored["result"], "allowed")
        self.assertFalse(restored["flag"])
        self.assertEqual([event["type"] for event in restored["events"]], ["trust.entry_posted"])
        self.assertEqual(restored["trust_post"]["balances"], {"matter_cents": 3500, "client_cents": 3500, "account_cents": 3500})
        self.assertEqual(restored["trust_post"]["flags"], [])
        _component_validator(self.spec, "TrustPostResult").validate(restored["trust_post"])
        hinted = payment_reversal_decision(
            _operation(self.spec, "reversePayment"),
            {
                "role": "attorney",
                "token_scopes": ["payments:write", "trust:withdraw"],
                "trust_linked": True,
                "matter_walled": False,
                "below_zero": True,
                "trust_account_id": account_row,
                "trust_accounts": accounts,
                "firm_id": "firm-1",
                "opening": {"matter_cents": 5000, "client_cents": 5000, "account_cents": 5000},
                **compensation,
            },
        )
        self.assertFalse(hinted["flag"])
        self.assertEqual(hinted["trust_post"]["flags"], [])
        self.assertEqual(hinted["trust_post"]["balances"]["matter_cents"], 3500)
        replayed_reversal = payment_reversal_decision(
            _operation(self.spec, "reversePayment"),
            {
                "role": "attorney",
                "token_scopes": ["payments:write", "trust:withdraw"],
                "trust_linked": True,
                "matter_walled": False,
                "below_zero": True,
                "trust_account_id": account_row,
                "trust_accounts": accounts,
                "firm_id": "firm-1",
                "opening": {"matter_cents": 200, "client_cents": 200, "account_cents": 200},
                "existing_reversal": {
                    "id": entry_b,
                    "entry_type": "reversal",
                    "amount_cents": -1500,
                    "direction": "debit",
                    "reverses_entry_id": entry_a,
                    "created_at": "2026-11-07T18:00:00Z",
                    "created_by": "018f1a00-7b3c-7d4e-8a11-555555555555",
                    "trust_account_id": account_row,
                    "client_id": client,
                    "matter_id": matter,
                    "method": "internal_transfer",
                    "description": "Payment reversal",
                },
                **compensation,
            },
        )
        self.assertEqual(replayed_reversal["result"], "original_result")
        self.assertEqual(replayed_reversal["events"], [])
        self.assertEqual(replayed_reversal["trust_post"]["flags"], [])
        self.assertFalse(replayed_reversal["ledger_mutated"])
        denied_reversal = payment_reversal_decision(
            _operation(self.spec, "reversePayment"),
            {"role": "attorney", "token_scopes": ["payments:write"], "trust_linked": True, "matter_walled": False, "trust_account_id": account_row, "trust_accounts": accounts, "firm_id": "firm-1", **compensation},
        )
        self.assertEqual(denied_reversal["result"], "scope_denied")
        self.assertEqual(denied_reversal.get("events", []), [])
        self.assertFalse(denied_reversal["ledger_mutated"])
        policy = self.spec["x-togavsu-module-delegation"]["noninteractive"]["payment_processor"]
        issued = processor_issuance(
            policy,
            {"registered": True, "module_id": "lawpay", "recipient_id": "lawpay", "approved_scopes": ["payments:write", "trust:write"]},
            {
                "processor_module_id": "lawpay",
                "initialization_id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeee1",
                "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeee2",
                "matter_id": matter,
                "contact_id": client,
                "invoice_id": None,
                "amount_cents": 40,
                "destination": "trust",
                "trust_account_id": account,
                "trust_accounts": accounts,
                "firm_id": "firm-1",
                "caller_scopes": ["payments:write", "trust:write"],
                "expires_at": "2026-11-07T18:00:00Z",
            },
        )
        self.assertEqual(issued["result"], "allowed")
        create = _operation(self.spec, "createPayment")
        store = {"payments": {}, "ledger": [], "idempotency": {}}
        call = {
            "operation": "createPayment",
            "registered": True,
            "caller_module_id": "lawpay",
            "module_scopes": ["payments:write", "trust:write"],
            "grant": issued["grant"],
            "matter_id": matter,
            "contact_id": client,
            "invoice_id": None,
            "amount_cents": 40,
            "destination": "trust",
            "trust_account_id": account,
            "trust_accounts": accounts,
            "firm_id": "firm-1",
            "status": "completed",
            "viewer_present": False,
            "opening": {"matter_cents": -100, "client_cents": -100, "account_cents": -100},
        }
        posted = processor_apply(store, policy, create, call)
        self.assertEqual(posted["result"], "allowed")
        self.assertTrue(posted["ledger_inserted"])
        self.assertEqual([event["type"] for event in posted["events"]], ["payment.created", "payment.completed", "trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(posted["events"][-1]["balance_cents"], -60)
        for event in posted["events"]:
            self.assertIn(event["type"], create["x-togavsu-events"])
        replayed = processor_apply(store, policy, create, call)
        self.assertEqual(replayed["result"], "original_result")
        self.assertEqual(replayed["events"], [])
        self.assertEqual(len(store["ledger"]), 1)
        denied = processor_apply(store, policy, create, {**call, "destination": "operating", "grant": {**issued["grant"], "destination": "operating"}, "module_scopes": ["trust:read"]})
        self.assertNotEqual(denied["result"], "allowed")
        self.assertEqual(denied["events"], [])
        self.assertEqual(len(store["ledger"]), 1)
        flag = posted["events"][-1]
        envelope = {
            "id": entry_c,
            "type": "trust.flag_raised",
            "occurred_at": "2026-11-07T18:00:00Z",
            "actor_kind": "system",
            "record_type": "trust_entry",
            "record_id": flag["trust_entry_id"],
            "summary": "Trust balance below zero",
            "matter_walled": False,
            **flag,
        }
        subscriber = {"installed": True, "registered": True, "module_id": "intake", "approved_scopes": ["trust:read"], "subscriptions": ["trust.flag_raised"], "on_wall": True, "attorney_only": False}
        delivered = webhook_envelope(subscriber, envelope)
        self.assertEqual(delivered["balance_cents"], -60)
        self.assertEqual(delivered["balance_scope"], "matter")
        self.assertEqual(delivered["client_id"], client)
        self.assertEqual(delivered["trust_account_id"], account)
        _component_validator(self.spec, "WebhookEvent").validate(delivered)
        self.assertIsNone(webhook_envelope({**subscriber, "approved_scopes": ["events:read"]}, envelope))
        self.assertIsNone(webhook_envelope(subscriber, {key: value for key, value in envelope.items() if key != "balance_cents"}))
        operating_issued = processor_issuance(
            policy,
            {"registered": True, "module_id": "lawpay", "recipient_id": "lawpay", "approved_scopes": ["payments:write"]},
            {
                "processor_module_id": "lawpay",
                "initialization_id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeee3",
                "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeee4",
                "matter_id": matter,
                "contact_id": client,
                "invoice_id": None,
                "amount_cents": 40,
                "destination": "operating",
                "caller_scopes": ["payments:write"],
                "expires_at": "2026-11-07T18:00:00Z",
            },
        )
        self.assertEqual(operating_issued["result"], "allowed")
        operating = {
            "operation": "createPayment",
            "registered": True,
            "caller_module_id": "lawpay",
            "module_scopes": ["payments:write"],
            "grant": operating_issued["grant"],
            "matter_id": matter,
            "contact_id": client,
            "invoice_id": None,
            "amount_cents": 40,
            "destination": "operating",
            "method": "card",
            "firm_id": "firm-1",
            "status": "pending",
            "viewer_present": False,
        }
        operating_store = {"payments": {}, "ledger": [], "idempotency": {}}
        pending = processor_apply(operating_store, policy, create, operating)
        self.assertEqual([event["type"] for event in pending["events"]], ["payment.created"])
        self.assertEqual(len(operating_store["ledger"]), 0)
        failed = processor_apply(operating_store, policy, _operation(self.spec, "updatePaymentStatus"), {**operating, "operation": "updatePaymentStatus", "payment_id": operating_issued["grant"]["payment_id"], "status": "failed"})
        self.assertEqual([event["type"] for event in failed["events"]], ["payment.updated"])
        self.assertEqual(len(operating_store["ledger"]), 0)
        repeat_status = processor_apply(operating_store, policy, _operation(self.spec, "updatePaymentStatus"), {**operating, "operation": "updatePaymentStatus", "payment_id": operating_issued["grant"]["payment_id"], "status": "failed"})
        self.assertEqual(repeat_status["events"], [])
        _component_validator(self.spec, "PaymentWrite").validate(
            {"contact_id": client, "matter_id": matter, "amount_cents": 40, "destination": "operating", "method": "card", "status": "pending", "invoice_id": None}
        )
        _component_validator(self.spec, "PaymentStatusWrite").validate({"status": "failed"})
        _component_validator(self.spec, "PaymentStatusWrite").validate({"status": "completed"})
        failed_issued = processor_issuance(
            policy,
            {"registered": True, "module_id": "lawpay", "recipient_id": "lawpay", "approved_scopes": ["payments:write"]},
            {
                "processor_module_id": "lawpay",
                "initialization_id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeee5",
                "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeee6",
                "matter_id": matter,
                "contact_id": client,
                "invoice_id": None,
                "amount_cents": 40,
                "destination": "operating",
                "caller_scopes": ["payments:write"],
                "expires_at": "2026-11-07T18:00:00Z",
            },
        )
        failed_store = {"payments": {}, "ledger": [], "idempotency": {}}
        failed_create = processor_apply(failed_store, policy, create, {**operating, "grant": failed_issued["grant"], "status": "failed"})
        self.assertEqual([event["type"] for event in failed_create["events"]], ["payment.created"])
        self.assertEqual(len(failed_store["ledger"]), 0)
        completed_issued = processor_issuance(
            policy,
            {"registered": True, "module_id": "lawpay", "recipient_id": "lawpay", "approved_scopes": ["payments:write"]},
            {
                "processor_module_id": "lawpay",
                "initialization_id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeee7",
                "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeee8",
                "matter_id": matter,
                "contact_id": client,
                "invoice_id": None,
                "amount_cents": 40,
                "destination": "operating",
                "caller_scopes": ["payments:write"],
                "expires_at": "2026-11-07T18:00:00Z",
            },
        )
        completed_store = {"payments": {}, "ledger": [], "idempotency": {}}
        created_completed = processor_apply(completed_store, policy, create, {**operating, "grant": completed_issued["grant"], "status": "completed"})
        self.assertEqual([event["type"] for event in created_completed["events"]], ["payment.created", "payment.completed"])
        self.assertEqual(len(completed_store["ledger"]), 0)
        trust_pending_issued = processor_issuance(
            policy,
            {"registered": True, "module_id": "lawpay", "recipient_id": "lawpay", "approved_scopes": ["payments:write", "trust:write"]},
            {
                "processor_module_id": "lawpay",
                "initialization_id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeee9",
                "reserved_payment_id": "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeeea",
                "matter_id": matter,
                "contact_id": client,
                "invoice_id": None,
                "amount_cents": 40,
                "destination": "trust",
                "trust_account_id": account,
                "trust_accounts": accounts,
                "firm_id": "firm-1",
                "caller_scopes": ["payments:write", "trust:write"],
                "expires_at": "2026-11-07T18:00:00Z",
            },
        )
        trust_store = {"payments": {}, "ledger": [], "idempotency": {}}
        trust_pending_call = {
            **operating,
            "grant": trust_pending_issued["grant"],
            "module_scopes": ["payments:write", "trust:write"],
            "destination": "trust",
            "trust_account_id": account,
            "trust_accounts": accounts,
            "status": "pending",
            "opening": {"matter_cents": -100, "client_cents": -100, "account_cents": -100},
        }
        trust_pending = processor_apply(trust_store, policy, create, trust_pending_call)
        self.assertEqual([event["type"] for event in trust_pending["events"]], ["payment.created"])
        self.assertEqual(len(trust_store["ledger"]), 0)
        trust_completed = processor_apply(
            trust_store,
            policy,
            _operation(self.spec, "updatePaymentStatus"),
            {**trust_pending_call, "operation": "updatePaymentStatus", "payment_id": trust_pending_issued["grant"]["payment_id"], "status": "completed"},
        )
        self.assertEqual([event["type"] for event in trust_completed["events"]], ["payment.completed", "trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(trust_completed["events"][-1]["balance_cents"], -60)
        trust_replay = processor_apply(
            trust_store,
            policy,
            _operation(self.spec, "updatePaymentStatus"),
            {**trust_pending_call, "operation": "updatePaymentStatus", "payment_id": trust_pending_issued["grant"]["payment_id"], "status": "completed"},
        )
        self.assertEqual(trust_replay["events"], [])
        self.assertEqual(len(trust_store["ledger"]), 1)

    def test_record_mutations_emit_events_only_after_commit(self):
        exempt = []
        uncovered = []
        for _method, _path, operation in _ops(self.spec):
            if not operation.get("x-togavsu-writes"):
                continue
            if operation["x-togavsu-access"].get("event_exemption"):
                exempt.append(operation["operationId"])
                self.assertEqual(committed_events(operation, committed=False), [])
                self.assertEqual(committed_events(operation, committed=True), [])
                continue
            self.assertTrue(operation["x-togavsu-events"], operation["operationId"])
            self.assertEqual(operation["x-togavsu-access"].get("events_on_failure"), "none", operation["operationId"])
            self.assertEqual(committed_events(operation, committed=False), [], operation["operationId"])
            access = operation["x-togavsu-access"]
            if access.get("trust_flag", {}).get("selection") == "resulting_balance":
                self.assertFalse(access["trust_flag"]["blocks"], operation["operationId"])
                self.assertEqual(trust_ledger_events(operation, [{"amount_cents": -1}], committed=False), [], operation["operationId"])
                self.assertEqual(trust_ledger_events(operation, [{"amount_cents": -1}], committed=True, replay=True), [], operation["operationId"])
                self.assertEqual(trust_ledger_events(operation, [{"amount_cents": -1}], committed=True, denied=True), [], operation["operationId"])
            if access.get("payment_events", {}).get("selection") == "resulting_state":
                self.assertEqual(payment_lifecycle_events(operation, [{"status": "completed"}], committed=False), [], operation["operationId"])
                continue
            if access.get("trust_flag", {}).get("selection") == "resulting_balance":
                continue
            self.assertEqual(committed_events(operation, committed=True), operation["x-togavsu-events"], operation["operationId"])
            if not operation["x-togavsu-events"]:
                uncovered.append(operation["operationId"])
        self.assertEqual(uncovered, [])
        self.assertEqual(exempt, ["putFileChunk"])
        self.assertEqual(_operation(self.spec, "cancelTrustReplenishment")["x-togavsu-events"], ["trust.replenishment_cancelled"])
        self.assertEqual(_operation(self.spec, "satisfyTrustReplenishment")["x-togavsu-events"], ["trust.replenishment_satisfied"])
        self.assertEqual(_operation(self.spec, "bulkCustomFieldValue")["x-togavsu-events"], ["custom_field.updated"])
        create_payment = _operation(self.spec, "createPayment")
        bulk_payment = _operation(self.spec, "bulkPayment")
        update_payment = _operation(self.spec, "updatePaymentStatus")
        reverse_payment = _operation(self.spec, "reversePayment")
        self.assertEqual(create_payment["x-togavsu-events"], ["payment.created", "payment.completed", "trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(bulk_payment["x-togavsu-events"], ["payment.created", "payment.completed", "trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(
            payment_lifecycle_events(create_payment, [{"status": "pending", "inserted": True}], committed=True),
            ["payment.created"],
        )
        self.assertEqual(
            payment_lifecycle_events(create_payment, [{"inserted": True}], committed=True),
            ["payment.created"],
        )
        self.assertEqual(
            payment_lifecycle_events(create_payment, [{"status": "failed", "inserted": True}], committed=True),
            ["payment.created"],
        )
        self.assertEqual(
            payment_lifecycle_events(create_payment, [{"status": "completed", "inserted": True}], committed=True),
            ["payment.created", "payment.completed"],
        )
        self.assertEqual(
            payment_lifecycle_events(create_payment, [{"status": "completed", "inserted": True}], committed=True, replay=True),
            [],
        )
        self.assertEqual(
            payment_lifecycle_events(
                bulk_payment,
                [{"status": "pending", "inserted": True}, {"status": "completed", "inserted": True}, {"status": "failed", "inserted": True}],
                committed=True,
            ),
            ["payment.created", "payment.created", "payment.completed", "payment.created"],
        )
        self.assertEqual(
            payment_lifecycle_events(update_payment, [{"previous_status": "pending", "status": "completed"}], committed=True),
            ["payment.completed"],
        )
        self.assertEqual(
            payment_lifecycle_events(update_payment, [{"previous_status": "pending", "status": "failed"}], committed=True),
            ["payment.updated"],
        )
        self.assertEqual(
            payment_lifecycle_events(update_payment, [{"previous_status": "completed", "status": "completed"}], committed=True, replay=True),
            [],
        )
        self.assertEqual(
            payment_lifecycle_events(update_payment, [{"previous_status": "completed", "status": "failed"}], committed=False),
            [],
        )
        self.assertEqual(
            payment_lifecycle_events(reverse_payment, [{"previous_status": "completed", "status": "reversed"}], committed=True),
            ["payment.reversed"],
        )
        self.assertEqual(
            payment_lifecycle_events(reverse_payment, [{"previous_status": "reversed", "status": "reversed"}], committed=True, replay=True),
            [],
        )
        self.assertNotIn("postMcp", exempt)
        self.assertFalse(_operation(self.spec, "postMcp")["x-togavsu-writes"])

    def test_marketplace_flag_and_pull(self):
        paths = self.market["paths"]
        self.assertIn("/v1/operator/listings/{listing_id}/flag", paths)
        self.assertIn("/v1/operator/listings/{listing_id}/pull", paths)
        self.assertIn("/v1/alerts", paths)
        self.assertIn("/v1/listings/{listing_id}/versions/{version}/package", paths)
        package = paths["/v1/listings/{listing_id}/versions/{version}/package"]["get"]
        self.assertEqual(package["responses"]["200"]["content"]["application/json"]["schema"]["$ref"], "#/components/schemas/SignedPackage")
        signed = self.market["components"]["schemas"]["SignedPackage"]
        self.assertEqual(signed["additionalProperties"], False)
        self.assertNotIn("url", signed["properties"])
        self.assertEqual(package["x-togavsu-verification"]["artifact_url_is_install"], False)
        self.assertEqual(self.market["components"]["schemas"]["Listing"]["additionalProperties"], False)
        self.assertNotIn("url", self.market["components"]["schemas"]["Listing"]["properties"])
        self.assertEqual(self.market["servers"][0]["url"], "https://marketplace.example.invalid")
        catalog = {
            "trusted_key_ids": ["key-1"],
            "listings": {
                "matter-clock": {
                    "pulled": False,
                    "yanked_versions": ["0.0.9"],
                    "versions": [
                        {
                            "version": "0.1.0",
                            "manifest": {"id": "matter-clock", "version": "0.1.0"},
                            "digest": "sha256:" + ("ab" * 32),
                            "signature": "sig",
                            "key_id": "key-1",
                        }
                    ],
                }
            },
        }
        verified = marketplace_package(catalog, {"listing_id": "matter-clock", "version": "0.1.0"})
        self.assertEqual(verified["result"], "verified")
        self.assertFalse(verified["package"]["execute"])
        self.assertEqual(verified["package"]["version"], "0.1.0")
        self.assertNotIn("url", verified["package"])
        for label, patch, expected in (
            ("missing", {"listing_id": "missing", "version": "0.1.0"}, "not_found"),
            ("version", {"listing_id": "matter-clock", "version": "9.9.9"}, "not_found"),
            ("yanked", {"listing_id": "matter-clock", "version": "0.0.9"}, "yanked"),
            ("digest", {"listing_id": "matter-clock", "version": "0.1.0", "digest": "sha256:" + ("cd" * 32)}, "digest_mismatch"),
            ("network", {"listing_id": "matter-clock", "version": "0.1.0", "network_error": True}, "unavailable"),
        ):
            self.assertEqual(marketplace_package(catalog, patch)["result"], expected, label)
            self.assertIsNone(marketplace_package(catalog, patch)["package"], label)
        bad_signature = {"version": "0.1.0", "digest": catalog["listings"]["matter-clock"]["versions"][0]["digest"], "signature": "nope", "key_id": "key-1", "signature_ok": False, "manifest": {}}
        self.assertEqual(
            marketplace_package({"trusted_key_ids": ["key-1"], "listings": {"matter-clock": {"versions": [bad_signature]}}}, {"listing_id": "matter-clock", "version": "0.1.0"})["result"],
            "signature_invalid",
        )
        bad_key = {**bad_signature, "signature_ok": True, "key_id": "unknown"}
        self.assertEqual(
            marketplace_package({"trusted_key_ids": ["key-1"], "listings": {"matter-clock": {"versions": [bad_key]}}}, {"listing_id": "matter-clock", "version": "0.1.0"})["result"],
            "untrusted_key",
        )
        pulled = marketplace_package({"trusted_key_ids": ["key-1"], "listings": {"matter-clock": {"pulled": True, "versions": catalog["listings"]["matter-clock"]["versions"]}}}, {"listing_id": "matter-clock", "version": "0.1.0"})
        self.assertEqual(pulled["result"], "yanked")
        self.assertIsNone(pulled["package"])

    def test_custom_acceptance_cannot_skip_operation_scopes(self):
        """A custom policy may narrow a call. It does not waive the operation's own scopes."""
        custom = (
            "trust_debit",
            "entry_type_scopes",
            "payment_reversal",
            "payment_effect",
            "record_type_domain",
            "item_privileges",
            "external_effects",
            "processor_issuance",
            "issuance",
            "recipient",
            "authorization_category",
        )
        checked = []
        for _method, _path, operation in _ops(self.spec):
            access = operation["x-togavsu-access"]
            scopes = list(access.get("scopes") or [])
            if not scopes or access.get("noninteractive_workflow"):
                continue
            if access.get("entry_type_scopes") or access.get("scope_combination") == "per_item":
                continue
            if not any(access.get(key) for key in custom):
                continue
            role = next(iter(access.get("roles") or []), None)
            if role is None:
                continue
            decision = mcp_tool_dispatch(
                self.spec,
                {
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "role": role,
                    "token_scopes": scopes[1:],
                    "tool": operation["operationId"],
                    "arguments": {},
                },
            )
            self.assertEqual(decision["result"], "scope_denied", operation["operationId"])
            self.assertFalse(decision["effects"], operation["operationId"])
            checked.append(operation["operationId"])
        self.assertIn("createApiToken", checked)
        self.assertIn("initiateProcessorPayment", checked)

    def test_dispatcher_parity_matrix(self):
        """Schema-valid MCP calls for every authorization family the adapter reaches.

        Trusted context stays on the call. Requested values stay in path, query, and body.
        """
        visible = "018f1a00-7b3c-7d4e-8a11-333333333333"
        hidden = "018f1a00-7b3c-7d4e-8a11-333333333339"
        client = "018f1a00-7b3c-7d4e-8a11-222222222222"
        account = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00001"
        other_account = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00002"
        missing_account = "018f1a00-7b3c-7d4e-8a11-aaaaaaa00009"
        owner = "018f1a00-7b3c-7d4e-8a11-555555555555"
        other_user = "018f1a00-7b3c-7d4e-8a11-666666666666"
        upload_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaaa"
        file_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1"
        backend_a = "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbb1"
        backend_b = "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbb2"
        entry_a = "018f1a00-7b3c-7d4e-8a11-ccccccccccc1"
        entry_b = "018f1a00-7b3c-7d4e-8a11-ccccccccccc2"
        view = "matter-clock.targets"
        extension = "matter-clock.matter_targets"
        view_scope = "views:matter-clock.targets:read"

        def cookie(**extra):
            return {"cookie": "togavsu_session", "credential_valid": True, **extra}

        def bearer(**extra):
            return {"authorization": "Bearer secret", "authorization_scheme": "Bearer", "credential_valid": True, **extra}

        def validate(operation_id, body):
            operation = _operation(self.spec, operation_id)
            schema = operation["requestBody"]["content"]["application/json"]["schema"]
            if "$ref" in schema:
                _component_validator(self.spec, schema["$ref"].rsplit("/", 1)[1]).validate(body)
            else:
                _schema_validator(self.spec, schema).validate(body)

        list_matter = _operation(self.spec, "listMatter")
        archive = _operation(self.spec, "createFileArchive")
        session_subject = _mcp_operation_call(
            {
                "role": "staff",
                "cookie": "togavsu_session",
                "current_approved_scopes": [],
                "extensions": [extension],
                "published_views": [view],
                "balances": [{"trust_account_id": account, "client_id": client, "matter_id": visible}],
            },
            {"query": {"confirm_quarantine": True}, "body": {"role": "admin", "token_scopes": ["matters:write"], "balances": [], "scopes": ["admin:all"]}},
            list_matter,
        )
        self.assertEqual(session_subject["credential"], "session")
        self.assertNotIn("token_scopes", session_subject)
        self.assertEqual(session_subject["role"], "staff")
        self.assertEqual(session_subject["current_approved_scopes"], [])
        self.assertEqual(session_subject["extensions"], [extension])
        self.assertEqual(session_subject["published_views"], [view])
        self.assertEqual(session_subject["balances"][0]["trust_account_id"], account)
        empty_bearer = _mcp_operation_call({"role": "staff", "authorization": "Bearer secret"}, {"path": {}, "query": {}, "body": {}}, list_matter)
        self.assertEqual(empty_bearer["credential"], "bearer")
        self.assertEqual(empty_bearer["token_scopes"], [])
        self.assertIs(_mcp_operation_call({"role": "staff", "authorization": "Bearer secret", "token_scopes": ["files:read"], "quarantined": True}, {"query": {"confirm_quarantine": False}, "body": {"file_ids": [file_id], "confirm_quarantine": True}}, archive)["confirm"], True)
        self.assertIs(_mcp_operation_call({"role": "staff", "authorization": "Bearer secret", "token_scopes": ["files:read"], "quarantined": True}, {"query": {"confirm_quarantine": True}, "body": {"file_ids": [file_id], "confirm_quarantine": False}}, archive)["confirm"], False)
        self.assertIs(_mcp_operation_call({"role": "staff", "authorization": "Bearer secret", "token_scopes": ["files:read"], "quarantined": True}, {"query": {"confirm_quarantine": True}, "body": {"file_ids": [file_id]}}, archive)["confirm"], None)

        issuance = _operation(self.spec, "issueFilePublicationGrant")
        issued = background_grant_issuance(
            issuance,
            {"installed": True, "registered": True, "module_id": "email", "approved_scopes": ["files:write"]},
            {"module_id": "email", "source": "email", "matter_id": visible, "expires_at": "2026-11-07T20:00:00Z", "matter_walled": False, "principal_matter_ids": [visible]},
        )
        self.assertEqual(issued["result"], "allowed")
        grant = issued["grant"]
        publication = self.spec["x-togavsu-module-delegation"]["noninteractive"]["background_file_publication"]
        created = background_file_step(
            {"uploads": {}, "files": {}, "events": []},
            publication,
            _operation(self.spec, "createFileUpload"),
            {"grant": grant, "caller_module_id": "email", "module_scopes": ["files:write"], "matter_id": visible, "viewer_present": False, "operation": "createFileUpload", "upload_id": upload_id},
        )
        module_upload = created["upload"]
        self.assertEqual(module_upload, _module_upload_document(upload_id, grant, "receiving"))
        _component_validator(self.spec, "FileUpload").validate(module_upload)
        user_upload = {
            "id": upload_id,
            "owner": {"owner_kind": "user", "owner_user_id": owner},
            "chunk_size_bytes": 8388608,
            "next_offset": 0,
            "size_bytes": 0,
            "expires_at": "2026-11-07T20:00:00Z",
            "status": "receiving",
        }
        _component_validator(self.spec, "FileUpload").validate(user_upload)
        grant["upload_ids"] = [upload_id]
        help_body = {"id": "start", "title": "Start", "markdown": "Hello"}
        marker_body = {"label": "After hours", "color": "#336699"}
        token_body = {"name": "Desk", "owner_user_id": owner, "scopes": ["profile:read"]}
        processor_operating = {"processor_module_id": "lawpay", "contact_id": client, "matter_id": visible, "amount_cents": 2500, "destination": "operating"}
        processor_trust = {**processor_operating, "destination": "trust", "trust_account_id": account}
        withdrawal = {"trust_account_id": account, "client_id": client, "matter_id": visible, "entry_type": "withdrawal", "amount_cents": 100, "method": "check", "description": "Distribution"}
        payment = {"contact_id": client, "matter_id": visible, "amount_cents": 100, "destination": "operating", "method": "check", "status": "pending"}
        archive_body = {"file_ids": [file_id], "confirm_quarantine": True}
        archive_row = {"id": file_id, "matter_id": None, "matter_walled": False, "attorney_only": False, "shared_with_client": False, "quarantined": True}
        fee_body = {"client_id": client, "trust_account_id": account, "amount_cents": 1500}
        move_all = {"from_backend_id": backend_a, "to_backend_id": backend_b, "scope": "all"}
        move_matter = {"from_backend_id": backend_a, "to_backend_id": backend_b, "scope": "matter", "matter_id": visible}
        move_matter_blank = {"from_backend_id": backend_a, "to_backend_id": backend_b, "scope": "matter"}
        move_all_named = {"from_backend_id": backend_a, "to_backend_id": backend_b, "scope": "all", "matter_id": visible}
        reason = {"reason": "Correct the deposit"}
        event_row = {"action": "noted", "record_type": "matter", "record_id": visible, "summary": "Batch row", "matter_id": visible}
        hidden_row = {**event_row, "record_id": hidden, "matter_id": hidden}
        for operation_id, body in (
            ("createModuleHelpPage", help_body),
            ("createModuleMarker", marker_body),
            ("createApiToken", token_body),
            ("initiateProcessorPayment", processor_operating),
            ("initiateProcessorPayment", processor_trust),
            ("postTrustEntry", withdrawal),
            ("createPayment", payment),
            ("createFileArchive", archive_body),
            ("applyFlatFee", fee_body),
            ("createStorageMove", move_all),
            ("createStorageMove", move_matter),
            ("createStorageMove", move_matter_blank),
            ("createStorageMove", move_all_named),
            ("reversePayment", reason),
            ("bulkEvent", {"items": [event_row, hidden_row]}),
        ):
            validate(operation_id, body)
        owned_balance = {"trust_account_id": account, "client_id": client, "matter_id": visible, "matter_cents": 1000, "client_cents": 5000, "account_cents": 4000, "active": True, "authorized": True, "firm_id": "firm-1"}
        other_balance = {"trust_account_id": other_account, "client_id": client, "matter_id": visible, "matter_cents": 9000, "client_cents": 9000, "account_cents": 9000, "active": True, "authorized": True, "firm_id": "firm-1"}
        separated = trust_debit_decision(
            {"role": "attorney", "credential": "session", "client_id": client, "trust_account_id": account, "matter_id": visible, "amount_cents": 1500, "matter_walled": False, "balances": [owned_balance, other_balance], "firm_id": "firm-1"}
        )
        self.assertEqual(separated["result"], "allowed")
        self.assertEqual(separated["balances"], {"matter_cents": -500, "client_cents": 3500, "account_cents": 2500})
        membership = {visible: True, hidden: False}
        malware = self.spec["x-togavsu-module-delegation"]["noninteractive"]["malware_scan"]
        scan_grant = _issue_malware_grant(
            malware,
            {"registered": True, "subscriptions": ["file.version_created"], "module_id": "malware", "recipient_id": "malware", "file_id": file_id, "version_id": upload_id, "matter_id": visible},
        )
        cases = [
            {"family": "session_or_bearer", "name": "cookie listMatter", "result": "allowed", "call": {**cookie(role="staff"), "tool": "listMatter", "arguments": {}}},
            {"family": "session_or_bearer", "name": "empty bearer listMatter", "result": "scope_denied", "call": {**bearer(role="staff", token_scopes=[]), "tool": "listMatter", "arguments": {}}},
            {"family": "session_or_bearer", "name": "absent bearer scopes", "result": "scope_denied", "call": {**bearer(role="staff"), "tool": "listMatter", "arguments": {}}},
            {"family": "session_or_bearer", "name": "cookie notifications", "result": "allowed", "call": {**cookie(role="staff", owner="self", caller_user_id=owner), "tool": "listNotification", "arguments": {}}},
            {"family": "session_or_bearer", "name": "empty bearer notifications", "result": "scope_denied", "call": {**bearer(role="staff", token_scopes=[], owner="self", caller_user_id=owner), "tool": "listNotification", "arguments": {}}},
            {"family": "include", "name": "unpublished extension", "result": "allowed", "call": {**bearer(role="staff", token_scopes=["matters:read"], approved_scopes=["matters:read"], extensions=[extension], published_views=[]), "tool": "listMatter", "arguments": {"query": {"include": extension}}}},
            {"family": "include", "name": "other module unpublished extension", "result": "forbidden", "call": {**bearer(role="staff", token_scopes=["matters:read"], approved_scopes=["matters:read"], extensions=[extension], published_views=[], caller_module_id="other-module"), "tool": "listMatter", "arguments": {"query": {"include": extension}}}},
            {"family": "include", "name": "published view", "result": "scope_denied", "call": {**bearer(role="staff", token_scopes=["matters:read"], approved_scopes=["matters:read", view_scope], current_approved_scopes=["matters:read", view_scope], published_views=[view]), "tool": "listMatter", "arguments": {"query": {"include": view}}}},
            {"family": "include", "name": "published view minimal scopes", "result": "allowed", "call": {**bearer(role="staff", token_scopes=["matters:read", view_scope], approved_scopes=["matters:read", view_scope], current_approved_scopes=["matters:read", view_scope], published_views=[view]), "tool": "listMatter", "arguments": {"query": {"include": view}}}},
            {"family": "include", "name": "published view empty token", "result": "scope_denied", "call": {**bearer(role="staff", token_scopes=[], approved_scopes=["matters:read", view_scope], current_approved_scopes=["matters:read", view_scope], published_views=[view]), "tool": "listMatter", "arguments": {"query": {"include": view}}}},
            {"family": "include", "name": "published view omitted token", "result": "scope_denied", "call": {**bearer(role="staff", approved_scopes=["matters:read", view_scope], current_approved_scopes=["matters:read", view_scope], published_views=[view]), "tool": "listMatter", "arguments": {"query": {"include": view}}}},
            {"family": "include", "name": "cookie published view", "result": "allowed", "call": {**cookie(role="staff", approved_scopes=["matters:read", view_scope], published_views=[view]), "tool": "listMatter", "arguments": {"query": {"include": view}}}},
            {"family": "include", "name": "revoked view scope", "result": "forbidden", "call": {**bearer(role="staff", token_scopes=["matters:read", view_scope], approved_scopes=["matters:read", view_scope], current_approved_scopes=["matters:read"], published_views=[view]), "tool": "listMatter", "arguments": {"query": {"include": view}}}},
            {"family": "include", "name": "absent current approval", "result": "allowed", "call": {**bearer(role="staff", token_scopes=["matters:read", view_scope], approved_scopes=["matters:read", view_scope], published_views=[view]), "tool": "listMatter", "arguments": {"query": {"include": view}}}},
            {"family": "include", "name": "explicit empty current approval", "result": "forbidden", "call": {**bearer(role="staff", token_scopes=["matters:read"], approved_scopes=["matters:read", view_scope], current_approved_scopes=[], published_views=[view]), "tool": "listMatter", "arguments": {"query": {"include": view}}}},
            {"family": "include", "name": "missing extension context", "result": "forbidden", "call": {**bearer(role="staff", token_scopes=["matters:read"], approved_scopes=["matters:read"]), "tool": "listMatter", "arguments": {"query": {"include": extension}}}},
            {"family": "quarantine", "name": "archive body confirms quarantine", "result": "allowed", "call": {**bearer(role="staff", token_scopes=["files:read"], quarantined=True), "stored_files": [archive_row], "tool": "createFileArchive", "arguments": {"body": archive_body}}},
            {"family": "quarantine", "name": "archive query does not confirm", "result": "quarantine_confirmation_required", "call": {**bearer(role="staff", token_scopes=["files:read"], quarantined=True), "stored_files": [archive_row], "tool": "createFileArchive", "arguments": {"query": {"confirm_quarantine": True}, "body": {"file_ids": [file_id]}}}},
            {"family": "quarantine", "name": "archive body false", "result": "quarantine_confirmation_required", "call": {**bearer(role="staff", token_scopes=["files:read"], quarantined=True), "stored_files": [archive_row], "tool": "createFileArchive", "arguments": {"body": {"file_ids": [file_id], "confirm_quarantine": False}}}},
            {"family": "quarantine", "name": "version query confirms", "result": "allowed", "call": {**bearer(role="staff", token_scopes=["files:read"], quarantined=True, matter_id=None), "tool": "downloadFileVersion", "arguments": {"path": {"file_id": file_id, "version_id": upload_id}, "query": {_operation(self.spec, "downloadFileVersion")["x-togavsu-access"]["quarantine_confirmation_input"]["name"]: True}}}},
            {"family": "quarantine", "name": "version query omitted", "result": "quarantine_confirmation_required", "call": {**bearer(role="staff", token_scopes=["files:read"], quarantined=True, matter_id=None), "tool": "downloadFileVersion", "arguments": {"path": {"file_id": file_id, "version_id": upload_id}}}},
            {"family": "background_file", "name": "module resume omitted scopes", "result": "scope_denied", "call": {**bearer(viewer_present=False, module_id="email", caller_module_id="email"), "tool": "getFileUpload", "module_scopes": ["files:write"], "registered": True, "matter_id": visible, "record": module_upload, "arguments": {"task_authorization": grant, "path": {"upload_id": upload_id}}}},
            {"family": "background_file", "name": "module resume minimal scopes", "result": "allowed", "call": {**bearer(viewer_present=False, module_id="email", caller_module_id="email", token_scopes=["files:write"], approved_scopes=["files:write"]), "tool": "getFileUpload", "module_scopes": ["files:write"], "registered": True, "matter_id": visible, "record": module_upload, "arguments": {"task_authorization": grant, "path": {"upload_id": upload_id}}}},
            {"family": "background_file", "name": "module resume empty token", "result": "scope_denied", "call": {**bearer(viewer_present=False, module_id="email", caller_module_id="email", token_scopes=[], approved_scopes=["files:write"]), "tool": "getFileUpload", "module_scopes": ["files:write"], "registered": True, "matter_id": visible, "record": module_upload, "arguments": {"task_authorization": grant, "path": {"upload_id": upload_id}}}},
            {"family": "background_file", "name": "module resume empty current approval", "result": "scope_denied", "call": {**bearer(viewer_present=False, module_id="email", caller_module_id="email", token_scopes=["files:write"], approved_scopes=["files:write"], current_approved_scopes=[]), "tool": "getFileUpload", "module_scopes": ["files:write"], "registered": True, "matter_id": visible, "record": module_upload, "arguments": {"task_authorization": grant, "path": {"upload_id": upload_id}}}},
            {"family": "upload_owner", "name": "user resume", "result": "allowed", "call": {**cookie(role="staff", user_id=owner), "tool": "getFileUpload", "record": user_upload, "arguments": {"path": {"upload_id": upload_id}}}},
            {"family": "upload_owner", "name": "other user resume", "result": "not_found", "call": {**bearer(role="staff", token_scopes=["files:write"], user_id=other_user), "tool": "getFileUpload", "record": user_upload, "arguments": {"path": {"upload_id": upload_id}}}},
            {"family": "upload_owner", "name": "staff is not the module owner", "result": "not_found", "call": {**bearer(role="staff", token_scopes=["files:write"], user_id=owner), "tool": "getFileUpload", "record": module_upload, "arguments": {"path": {"upload_id": upload_id}}}},
            {"family": "background_file", "name": "revoked publication grant", "result": "forbidden", "call": {**bearer(viewer_present=False, module_id="email", caller_module_id="email"), "tool": "getFileUpload", "module_scopes": ["files:write"], "matter_id": visible, "record": module_upload, "arguments": {"task_authorization": {**grant, "revoked": True}, "path": {"upload_id": upload_id}}}},
            {"family": "background_file", "name": "empty module scopes", "result": "scope_denied", "call": {**bearer(viewer_present=False, module_id="email", caller_module_id="email", approved_scopes=["files:write"]), "tool": "getFileUpload", "module_scopes": [], "matter_id": visible, "record": module_upload, "arguments": {"task_authorization": grant, "path": {"upload_id": upload_id}}}},
            {"family": "background_file", "name": "absent module scopes", "result": "scope_denied", "call": {**bearer(viewer_present=False, module_id="email", caller_module_id="email", approved_scopes=["files:write"]), "tool": "getFileUpload", "matter_id": visible, "record": module_upload, "arguments": {"task_authorization": grant, "path": {"upload_id": upload_id}}}},
            {"family": "malware_scan", "name": "revoked scan grant", "result": "forbidden", "call": {**bearer(viewer_present=False, module_id="malware", caller_module_id="malware"), "tool": "downloadFileVersion", "module_scopes": ["files:read"], "registered": True, "subscriptions": ["file.version_created"], "matter_id": visible, "arguments": {"task_authorization": {**scan_grant, "revoked": True}, "path": {"file_id": file_id, "version_id": upload_id}}}},
            {"family": "payment_processor", "name": "revoked processor grant", "result": "forbidden", "call": {**bearer(viewer_present=False, module_id="lawpay", caller_module_id="lawpay"), "tool": "createPayment", "registered": True, "arguments": {"task_authorization": {"revoked": True}}}},
            {"family": "module_principal", "name": "empty help approval", "result": "scope_denied", "call": {**bearer(viewer_present=False, module_id="matter-clock", approved_scopes=[], token_scopes=["help:write"], installed=True), "tool": "createModuleHelpPage", "arguments": {"path": {"module_id": "matter-clock"}, "body": help_body}}},
            {"family": "module_principal", "name": "absent help approval", "result": "scope_denied", "call": {**bearer(viewer_present=False, module_id="matter-clock", token_scopes=["help:write"], installed=True), "tool": "createModuleHelpPage", "arguments": {"path": {"module_id": "matter-clock"}, "body": help_body}}},
            {"family": "module_principal", "name": "empty marker approval", "result": "scope_denied", "call": {**bearer(viewer_present=False, module_id="intake", approved_scopes=[], token_scopes=["markers:write"], installed=True), "tool": "createModuleMarker", "arguments": {"path": {"module_id": "intake"}, "body": marker_body}}},
            {"family": "module_principal", "name": "revoked module install", "result": "forbidden", "call": {**bearer(viewer_present=False, module_id="matter-clock", approved_scopes=["help:write"], token_scopes=["help:write"], installed=True, grant_revoked=True), "tool": "createModuleHelpPage", "arguments": {"path": {"module_id": "matter-clock"}, "body": help_body}}},
            {"family": "issuance", "name": "cookie issues within role", "result": "allowed", "call": {**cookie(role="admin", caller_scopes=["tokens:write", "profile:read"], owner_role_scopes=["profile:read"]), "tool": "createApiToken", "arguments": {"body": token_body}}},
            {"family": "issuance", "name": "empty bearer cannot issue", "result": "scope_denied", "call": {**bearer(role="admin", token_scopes=[], owner_role_scopes=["profile:read"]), "tool": "createApiToken", "arguments": {"body": token_body}}},
            {"family": "issuance", "name": "session without caller scopes", "result": "scope_denied", "call": {**cookie(role="admin", owner_role_scopes=["profile:read"]), "tool": "createApiToken", "arguments": {"body": token_body}}},
            {"family": "issuance", "name": "bearer missing tokens:write", "result": "scope_denied", "call": {**bearer(role="admin", token_scopes=["profile:read"], owner_role_scopes=["profile:read"]), "tool": "createApiToken", "arguments": {"body": token_body}}},
            {"family": "issuance", "name": "bearer minimal tokens:write", "result": "allowed", "call": {**bearer(role="admin", token_scopes=["tokens:write", "profile:read"], owner_role_scopes=["profile:read"]), "tool": "createApiToken", "arguments": {"body": token_body}}},
            {"family": "processor_issuance", "name": "trust init payments write only", "result": "scope_denied", "call": {**bearer(role="staff", token_scopes=["payments:write"], registered=True, module_id="lawpay", recipient_id="lawpay", approved_scopes=["payments:write", "trust:write"], current_approved_scopes=["payments:write", "trust:write"], initialization_id=upload_id, reserved_payment_id=file_id, expires_at="2026-11-07T18:00:00Z", trust_accounts=[{"id": account, "active": True, "authorized": True, "firm_id": "firm-1"}], firm_id="firm-1", matter_walled=False), "tool": "initiateProcessorPayment", "arguments": {"body": processor_trust}}},
            {"family": "processor_issuance", "name": "trust init unregistered", "result": "forbidden", "call": {**bearer(role="staff", token_scopes=["payments:write", "trust:write"], registered=False, module_id="lawpay", recipient_id="lawpay", approved_scopes=["payments:write", "trust:write"], current_approved_scopes=["payments:write", "trust:write"], initialization_id=upload_id, reserved_payment_id=file_id, expires_at="2026-11-07T18:00:00Z", trust_accounts=[{"id": account, "active": True, "authorized": True, "firm_id": "firm-1"}], firm_id="firm-1", matter_walled=False), "tool": "initiateProcessorPayment", "arguments": {"body": processor_trust}}},
            {"family": "processor_issuance", "name": "trust init empty current approval", "result": "scope_denied", "call": {**bearer(role="staff", token_scopes=["payments:write", "trust:write"], registered=True, module_id="lawpay", recipient_id="lawpay", approved_scopes=["payments:write", "trust:write"], current_approved_scopes=[], initialization_id=upload_id, reserved_payment_id=file_id, expires_at="2026-11-07T18:00:00Z", trust_accounts=[{"id": account, "active": True, "authorized": True, "firm_id": "firm-1"}], firm_id="firm-1", matter_walled=False), "tool": "initiateProcessorPayment", "arguments": {"body": processor_trust}}},
            {"family": "processor_issuance", "name": "trust init omitted current approval", "result": "allowed", "call": {**bearer(role="staff", token_scopes=["payments:write", "trust:write"], registered=True, module_id="lawpay", recipient_id="lawpay", approved_scopes=["payments:write", "trust:write"], initialization_id=upload_id, reserved_payment_id=file_id, expires_at="2026-11-07T18:00:00Z", trust_accounts=[{"id": account, "active": True, "authorized": True, "firm_id": "firm-1"}], firm_id="firm-1", matter_walled=False), "tool": "initiateProcessorPayment", "arguments": {"body": processor_trust}}},
            {"family": "processor_issuance", "name": "trust init minimal scopes", "result": "allowed", "call": {**bearer(role="staff", token_scopes=["payments:write", "trust:write"], registered=True, module_id="lawpay", recipient_id="lawpay", approved_scopes=["payments:write", "trust:write"], current_approved_scopes=["payments:write", "trust:write"], initialization_id=upload_id, reserved_payment_id=file_id, expires_at="2026-11-07T18:00:00Z", trust_accounts=[{"id": account, "active": True, "authorized": True, "firm_id": "firm-1"}], firm_id="firm-1", matter_walled=False), "tool": "initiateProcessorPayment", "arguments": {"body": processor_trust}}},
            {"family": "entry_type_scopes", "name": "cookie attorney withdrawal", "result": "allowed", "call": {**cookie(role="attorney", matter_walled=False), "tool": "postTrustEntry", "arguments": {"body": withdrawal}}},
            {"family": "entry_type_scopes", "name": "empty bearer withdrawal", "result": "scope_denied", "call": {**bearer(role="attorney", token_scopes=[]), "tool": "postTrustEntry", "arguments": {"body": withdrawal}}},
            {"family": "entry_type_scopes", "name": "cookie staff withdrawal", "result": "forbidden", "call": {**cookie(role="staff"), "tool": "postTrustEntry", "arguments": {"body": withdrawal}}},
            {"family": "payment_effect", "name": "cookie pending payment", "result": "allowed", "call": {**cookie(role="staff", matter_walled=False), "tool": "createPayment", "arguments": {"body": payment}}},
            {"family": "payment_effect", "name": "empty bearer payment", "result": "scope_denied", "call": {**bearer(role="staff", token_scopes=[]), "tool": "createPayment", "arguments": {"body": payment}}},
            {"family": "payment_reversal", "name": "cookie operating reversal", "result": "allowed", "call": {**cookie(role="attorney", trust_linked=False, matter_id=visible, matter_walled=False), "tool": "reversePayment", "arguments": {"path": {"payment_id": entry_a}, "body": reason}}},
            {"family": "payment_reversal", "name": "empty bearer trust reversal", "result": "scope_denied", "call": {**bearer(role="attorney", token_scopes=[], trust_linked=True, matter_walled=False, trust_account_id=account), "tool": "reversePayment", "arguments": {"path": {"payment_id": entry_a}, "body": reason}}},
            {"family": "trust_debit", "name": "cookie flat fee", "result": "allowed", "call": {**cookie(role="attorney", balances=[owned_balance, other_balance], firm_id="firm-1", matter_walled=False), "tool": "applyFlatFee", "arguments": {"path": {"matter_id": visible}, "body": fee_body}}},
            {"family": "trust_debit", "name": "empty bearer flat fee", "result": "scope_denied", "call": {**bearer(role="attorney", token_scopes=[], balances=[owned_balance, other_balance], firm_id="firm-1", matter_walled=False), "tool": "applyFlatFee", "arguments": {"path": {"matter_id": visible}, "body": fee_body}}},
            {"family": "trust_debit", "name": "other account balance", "result": "not_found", "call": {**bearer(role="attorney", token_scopes=["trust:withdraw"], balances=[other_balance], firm_id="firm-1", matter_walled=False), "tool": "applyFlatFee", "arguments": {"path": {"matter_id": visible}, "body": {**fee_body, "trust_account_id": account}}}},
            {"family": "trust_debit", "name": "missing balances", "result": "not_found", "call": {**bearer(role="attorney", token_scopes=["trust:withdraw"], matter_walled=False), "tool": "applyFlatFee", "arguments": {"path": {"matter_id": visible}, "body": {**fee_body, "trust_account_id": missing_account}}}},
            {"family": "external_effects", "name": "cookie firm move", "result": "allowed", "call": {**cookie(role="admin"), "tool": "createStorageMove", "arguments": {"body": move_all}}},
            {"family": "external_effects", "name": "empty bearer move", "result": "scope_denied", "call": {**bearer(role="admin", token_scopes=[]), "tool": "createStorageMove", "arguments": {"body": move_all}}},
            {"family": "external_effects", "name": "matter move without matter", "result": "validation_failed", "call": {**bearer(role="admin", token_scopes=["files:write"]), "tool": "createStorageMove", "arguments": {"body": move_matter_blank}}},
            {"family": "external_effects", "name": "off-wall matter move", "result": "not_found", "call": {**bearer(role="admin", token_scopes=["files:write"], matter_walled=True, matter_membership={visible: False}), "tool": "createStorageMove", "arguments": {"body": move_matter}}},
            {"family": "external_effects", "name": "firm move names a matter", "result": "validation_failed", "call": {**bearer(role="admin", token_scopes=["files:write"]), "tool": "createStorageMove", "arguments": {"body": move_all_named}}},
            {"family": "batch_wall", "name": "mixed off-wall batch", "result": "not_found", "call": {**bearer(role="staff", token_scopes=["events:write"], matter_membership=membership), "tool": "bulkEvent", "arguments": {"body": {"items": [event_row, hidden_row]}}}},
            {"family": "batch_wall", "name": "cookie on-wall batch", "result": "allowed", "call": {**cookie(role="staff", matter_membership=membership), "tool": "bulkEvent", "arguments": {"body": {"items": [event_row, event_row]}}}},
            {"family": "batch_wall", "name": "empty bearer batch", "result": "scope_denied", "call": {**bearer(role="staff", token_scopes=[], matter_membership=membership), "tool": "bulkEvent", "arguments": {"body": {"items": [event_row]}}}},
        ]
        principal_ids = []
        for _method, path, operation in _ops(self.spec):
            principal = operation["x-togavsu-access"].get("module_principal")
            if not principal:
                continue
            operation_id = operation["operationId"]
            principal_ids.append(operation_id)
            scope = principal["scope"]
            module_id = "intake" if scope == "markers:write" else "matter-clock"
            arguments = {"path": {"module_id": module_id}}
            if "{marker_id}" in path:
                arguments["path"]["marker_id"] = "018f1a00-7b3c-7d4e-8a11-111111111111"
            if "{page_id}" in path:
                arguments["path"]["page_id"] = "start"
            held = {"viewer_present": False, "module_id": module_id, "installed": True, "approved_scopes": [scope], "module_scopes": [scope]}
            cases.append({"family": "module_principal", "name": operation_id + " empty token", "result": "scope_denied", "call": {**bearer(**held, token_scopes=[]), "tool": operation_id, "arguments": arguments}})
            cases.append({"family": "module_principal", "name": operation_id + " empty current approval", "result": "scope_denied", "call": {**bearer(**held, token_scopes=[scope], current_approved_scopes=[]), "tool": operation_id, "arguments": arguments}})
        self.assertEqual(
            set(principal_ids),
            {"createModuleHelpPage", "replaceModuleHelpPages", "deleteModuleHelpPage", "createModuleMarker", "updateModuleMarker", "revokeModuleMarker"},
        )
        cases.append({"family": "module_principal", "name": "help bearer matches current approval", "result": "allowed", "call": {**bearer(viewer_present=False, module_id="matter-clock", installed=True, approved_scopes=["help:write"], token_scopes=["help:write"], current_approved_scopes=["help:write"]), "tool": "createModuleHelpPage", "arguments": {"path": {"module_id": "matter-clock"}, "body": help_body}}})
        grant_body = {"assignment": "matter", "matter_id": visible, "source": "email"}
        validate("issueFilePublicationGrant", grant_body)
        grant_call = {"viewer_present": False, "module_id": "email", "installed": True, "registered": True, "expires_at": "2026-11-07T20:00:00Z", "matter_walled": False, "principal_matter_ids": [visible]}
        cases.extend(
            [
                {"family": "publication_grant", "name": "publication grant effective scopes", "result": "allowed", "call": {**bearer(**grant_call, approved_scopes=["files:write"], token_scopes=["files:write"]), "tool": "issueFilePublicationGrant", "arguments": {"body": grant_body}}},
                {"family": "publication_grant", "name": "empty approval ignores stale module scopes", "result": "scope_denied", "call": {**bearer(**grant_call, approved_scopes=[], token_scopes=["files:write"], module_scopes=["files:write"]), "tool": "issueFilePublicationGrant", "arguments": {"body": grant_body}}},
                {"family": "publication_grant", "name": "empty token ignores stale module scopes", "result": "scope_denied", "call": {**bearer(**grant_call, approved_scopes=["files:write"], token_scopes=[], module_scopes=["files:write"]), "tool": "issueFilePublicationGrant", "arguments": {"body": grant_body}}},
                {"family": "publication_grant", "name": "empty current approval ignores stale module scopes", "result": "scope_denied", "call": {**bearer(**grant_call, approved_scopes=["files:write"], token_scopes=["files:write"], current_approved_scopes=[], module_scopes=["files:write"]), "tool": "issueFilePublicationGrant", "arguments": {"body": grant_body}}},
            ]
        )
        calendar = {"calendar": {"replaced": True, "available": True, "module_id": "cal-mod"}}
        calendar_down = {"calendar": {"replaced": True, "available": False, "module_id": "cal-mod"}}
        event_id = "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeee1"
        cal_write = {"title": "Hearing", "kind": "court_date", "starts_at": "2026-11-07T18:00:00Z", "ends_at": "2026-11-07T19:00:00Z"}
        validate("createCalendarEvent", cal_write)
        staff_read = bearer(role="staff", token_scopes=["calendar:read"])
        cases.extend(
            [
                {"family": "replacement", "name": "calendar list routes", "result": "allowed", "effects": False, "routed": "cal-mod", "call": {**staff_read, "domain_handlers": calendar, "tool": "listCalendarEvent", "arguments": {}}},
                {"family": "replacement", "name": "calendar get routes", "result": "allowed", "effects": False, "routed": "cal-mod", "call": {**staff_read, "matter_id": None, "domain_handlers": calendar, "tool": "getCalendarEvent", "arguments": {"path": {"calendar_event_id": event_id}}}},
                {"family": "replacement", "name": "calendar history stays on core", "result": "allowed", "effects": True, "routed": None, "call": {**staff_read, "domain_handlers": calendar, "core_history": True, "tool": "listCalendarEvent", "arguments": {}}},
                {"family": "replacement", "name": "unavailable calendar read", "result": "forbidden", "call": {**staff_read, "domain_handlers": calendar_down, "tool": "listCalendarEvent", "arguments": {}}},
                {"family": "replacement", "name": "core calendar list", "result": "allowed", "effects": True, "routed": None, "call": {**staff_read, "tool": "listCalendarEvent", "arguments": {}}},
                {"family": "replacement", "name": "calendar write routes", "result": "allowed", "effects": False, "routed": "cal-mod", "call": {**bearer(role="staff", token_scopes=["calendar:write"], domain_handlers=calendar), "tool": "createCalendarEvent", "arguments": {"body": cal_write}}},
                {"family": "replacement", "name": "history flag does not keep a write", "result": "allowed", "effects": False, "routed": "cal-mod", "call": {**bearer(role="staff", token_scopes=["calendar:write"], domain_handlers=calendar, core_history=True), "tool": "createCalendarEvent", "arguments": {"body": cal_write}}},
            ]
        )
        activity = "018f1a00-7b3c-7d4e-8a11-121212121212"
        open_time = {"matter_id": visible, "worked_on": "2026-11-07", "increments": 2, "description": "Review", "activity_type_id": activity}
        hidden_time = {**open_time, "matter_id": hidden}
        validate("bulkTimeEntry", {"items": [open_time]})
        validate("bulkTimeEntry", {"items": [open_time, hidden_time]})
        cases.extend(
            [
                {"family": "batch_wall", "name": "unwalled time batch", "result": "allowed", "call": {**cookie(role="staff", matter_walls={visible: False}), "tool": "bulkTimeEntry", "arguments": {"body": {"items": [open_time]}}}},
                {"family": "batch_wall", "name": "call wall flag for one matter", "result": "allowed", "call": {**cookie(role="staff", matter_id=visible, matter_walled=False), "tool": "bulkTimeEntry", "arguments": {"body": {"items": [open_time]}}}},
                {"family": "batch_wall", "name": "mixed unwalled and off-wall", "result": "not_found", "call": {**cookie(role="staff", matter_walls={visible: False, hidden: True}, matter_membership={hidden: False}), "tool": "bulkTimeEntry", "arguments": {"body": {"items": [open_time, hidden_time]}}}},
                {"family": "batch_wall", "name": "mixed unwalled and on-wall", "result": "allowed", "call": {**cookie(role="staff", matter_walls={visible: False, hidden: True}, matter_membership={hidden: True}), "tool": "bulkTimeEntry", "arguments": {"body": {"items": [open_time, hidden_time]}}}},
                {"family": "batch_wall", "name": "item flag does not clear a wall", "result": "not_found", "call": {**bearer(role="staff", token_scopes=["time:write"], matter_walls={visible: True}, matter_membership={visible: False}), "tool": "bulkTimeEntry", "arguments": {"body": {"items": [{**open_time, "matter_walled": False}]}}}},
            ]
        )
        present = set()
        for _method, _path, operation in _ops(self.spec):
            access = operation["x-togavsu-access"]
            workflow = access.get("noninteractive_workflow")
            if workflow == "malware_scan":
                present.add("malware_scan")
            elif workflow == "payment_processor":
                present.add("payment_processor")
            elif workflow == "background_file_publication":
                present.add("background_file")
            if access.get("module_principal"):
                present.add("module_principal")
            if access.get("trust_debit"):
                present.add("trust_debit")
            if access.get("entry_type_scopes"):
                present.add("entry_type_scopes")
            if access.get("payment_effect"):
                present.add("payment_effect")
            if access.get("payment_reversal"):
                present.add("payment_reversal")
            if access.get("external_effects"):
                present.add("external_effects")
            if access.get("include_scope_pattern"):
                present.add("include")
            if access.get("quarantine_confirmation") == "required":
                present.add("quarantine")
            if access.get("batch_authorization") == "all_or_nothing" and access.get("matter_wall"):
                present.add("batch_wall")
            if access.get("issuance") == "admin_only":
                present.add("issuance")
            if access.get("owner") == "self" and operation["operationId"] == "getFileUpload":
                present.add("upload_owner")
            if access.get("scopes") and operation.get("security") != []:
                present.add("session_or_bearer")
        self.assertEqual(
            present,
            {
                "malware_scan",
                "payment_processor",
                "background_file",
                "module_principal",
                "trust_debit",
                "entry_type_scopes",
                "payment_effect",
                "payment_reversal",
                "external_effects",
                "include",
                "quarantine",
                "batch_wall",
                "issuance",
                "upload_owner",
                "session_or_bearer",
            },
        )
        self.assertTrue(present <= {case["family"] for case in cases})
        for case in cases:
            decision = mcp_tool_dispatch(self.spec, _session_csrf(_as_trusted_grant_fixture(case["call"])))
            self.assertEqual(decision["result"], case["result"], case["name"])
            expected_effects = case["effects"] if "effects" in case else case["result"] == "allowed"
            self.assertEqual(decision["effects"], expected_effects, case["name"])
            if "routed" in case:
                self.assertEqual(decision.get("routed"), case["routed"], case["name"])
            self.assertFalse(decision["public"], case["name"])

    def test_merge_matter_authorizes_both_records(self):
        source = "018f1a00-7b3c-7d4e-8a11-333333333333"
        survivor = "018f1a00-7b3c-7d4e-8a11-333333333339"
        other = "018f1a00-7b3c-7d4e-8a11-333333333331"
        operation = _operation(self.spec, "mergeMatter")

        def decide(walls, body, membership=None, role="attorney", scopes=None):
            raw = {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": role,
                "token_scopes": ["matters:write"] if scopes is None else scopes,
                "matter_walls": walls,
                "matter_membership": {} if membership is None else membership,
                "tool": "mergeMatter",
                "arguments": {"path": {"matter_id": source}, "body": body},
            }
            mcp = mcp_tool_dispatch(self.spec, raw)
            http = operation_authorization(self.spec, operation, _mcp_operation_call(raw, raw["arguments"], operation))
            self.assertEqual(http["result"], mcp["result"])
            self.assertEqual(http["effects"], mcp["effects"])
            self.assertEqual(http.get("events"), mcp.get("events"))
            self.assertEqual(http.get("relinked"), mcp.get("relinked"))
            self.assertEqual(http.get("archived"), mcp.get("archived"))
            return mcp

        allowed = decide({source: False, survivor: False}, {"survivor_id": survivor})
        self.assertEqual(allowed["result"], "allowed")
        self.assertTrue(allowed["effects"])
        self.assertEqual(allowed["events"], ["matter.merged"])
        self.assertTrue(allowed["relinked"])
        self.assertTrue(allowed["archived"])
        on_wall = decide({source: True, survivor: True}, {"survivor_id": survivor}, {source: True, survivor: True})
        self.assertEqual(on_wall["result"], "allowed")
        self.assertTrue(on_wall["relinked"])
        self.assertTrue(on_wall["archived"])
        hidden_survivor = decide({source: False, survivor: True}, {"survivor_id": survivor, "matter_walled": False}, {survivor: False})
        self.assertEqual(hidden_survivor["result"], "not_found")
        self.assertFalse(hidden_survivor["effects"])
        self.assertEqual(hidden_survivor["events"], [])
        self.assertFalse(hidden_survivor["relinked"])
        self.assertFalse(hidden_survivor["archived"])
        hidden_source = decide({source: True, survivor: False}, {"survivor_id": survivor}, {source: False})
        self.assertEqual(hidden_source["result"], "not_found")
        self.assertFalse(hidden_source["effects"])
        self.assertFalse(hidden_source["relinked"])
        self.assertFalse(hidden_source["archived"])
        missing = decide({source: False, survivor: False}, {})
        self.assertEqual(missing["result"], "validation_failed")
        self.assertFalse(missing["effects"])
        self.assertFalse(missing["relinked"])
        self.assertFalse(missing["archived"])
        mismatched = decide({source: False, other: False}, {"survivor_id": survivor})
        self.assertEqual(mismatched["result"], "not_found")
        self.assertFalse(mismatched["relinked"])
        self.assertFalse(mismatched["archived"])
        same = decide({source: False}, {"survivor_id": source})
        self.assertEqual(same["result"], "validation_failed")
        self.assertFalse(same["relinked"])
        staff = decide({source: False, survivor: False}, {"survivor_id": survivor}, role="staff")
        self.assertEqual(staff["result"], "forbidden")
        self.assertFalse(staff["effects"])
        self.assertEqual(staff["events"], [])

    def test_matter_patch_cannot_reopen(self):
        matter = "018f1a00-7b3c-7d4e-8a11-333333333333"
        update = _operation(self.spec, "updateMatter")
        reopen = _operation(self.spec, "reopenMatter")
        patch_schema = self.spec["components"]["schemas"]["MatterWritePatch"]
        self.assertEqual(set(patch_schema["properties"]["status"]["enum"]), {"prospect", "open", "on_hold"})
        _component_validator(self.spec, "MatterWritePatch").validate({"status": "on_hold", "name": "Smith"})
        with self.assertRaises(Exception):
            _component_validator(self.spec, "MatterWritePatch").validate({"status": "closed"})

        def call(operation, role, body, **trusted):
            raw = {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": role,
                "token_scopes": ["matters:write"],
                "matter_walled": False,
                "tool": operation["operationId"],
                "arguments": {"path": {"matter_id": matter}, "body": body},
                **trusted,
            }
            mcp = mcp_tool_dispatch(self.spec, raw)
            http = operation_authorization(self.spec, operation, _mcp_operation_call(raw, raw["arguments"], operation))
            self.assertEqual(http["result"], mcp["result"])
            self.assertEqual(http["effects"], mcp["effects"])
            self.assertEqual(http.get("events"), mcp.get("events"))
            self.assertEqual(http.get("status"), mcp.get("status"))
            self.assertEqual(http.get("closed_on"), mcp.get("closed_on"))
            self.assertEqual(http.get("close_reason"), mcp.get("close_reason"))
            return mcp

        for role in ("staff", "attorney", "admin"):
            denied = call(update, role, {"status": "open"}, stored_status="closed")
            self.assertEqual(denied["result"], "forbidden", role)
            self.assertFalse(denied["effects"], role)
            self.assertEqual(denied["events"], [], role)
        untouched = call(update, "staff", {"name": "Still closed"}, stored_status="closed")
        self.assertEqual(untouched["result"], "forbidden")
        self.assertFalse(untouched["effects"])
        self.assertEqual(untouched["events"], [])
        moved = call(update, "staff", {"status": "on_hold"}, stored_status="open")
        self.assertEqual(moved["result"], "allowed")
        self.assertTrue(moved["effects"])
        self.assertEqual(moved["events"], ["matter.updated"])
        ordinary = call(update, "staff", {"name": "Smith"}, stored_status="open")
        self.assertEqual(ordinary["result"], "allowed")
        self.assertEqual(ordinary["events"], ["matter.updated"])
        closed_value = call(update, "attorney", {"status": "closed"}, stored_status="open")
        self.assertEqual(closed_value["result"], "validation_failed")
        self.assertFalse(closed_value["effects"])
        self.assertEqual(closed_value["events"], [])
        unknown = call(update, "staff", {"name": "Smith"})
        self.assertEqual(unknown["result"], "forbidden")
        self.assertFalse(unknown["effects"])
        kept = {"stored_status": "closed", "status_before_close": "on_hold", "closed_on": "2026-11-01", "close_reason": "Done"}
        opened = call(reopen, "attorney", {"reason": "Client returned"}, **kept)
        self.assertEqual(opened["result"], "allowed")
        self.assertTrue(opened["effects"])
        self.assertEqual(opened["status"], "on_hold")
        self.assertEqual(opened["closed_on"], "2026-11-01")
        self.assertEqual(opened["close_reason"], "Done")
        self.assertEqual(opened["events"], ["matter.reopened"])
        admin_opened = call(reopen, "admin", {"reason": "Client returned"}, **kept)
        self.assertEqual(admin_opened["result"], "allowed")
        self.assertEqual(admin_opened["events"], ["matter.reopened"])
        missing_reason = call(reopen, "attorney", {}, **kept)
        self.assertEqual(missing_reason["result"], "validation_failed")
        self.assertFalse(missing_reason["effects"])
        self.assertEqual(missing_reason["events"], [])
        self.assertNotIn("status", missing_reason)
        blank_reason = call(reopen, "attorney", {"reason": " "}, **kept)
        self.assertEqual(blank_reason["result"], "validation_failed")
        self.assertEqual(blank_reason["events"], [])
        staff_reopen = call(reopen, "staff", {"reason": "Client returned"}, **kept)
        self.assertEqual(staff_reopen["result"], "forbidden")
        self.assertFalse(staff_reopen["effects"])
        self.assertEqual(staff_reopen["events"], [])
        self.assertNotIn("status", staff_reopen)

    def test_stopped_timer_response_is_terminal(self):
        schemas = self.spec["components"]["schemas"]
        self.assertEqual(set(schemas["Timer"]["properties"]["status"]["enum"]), {"running", "paused"})
        self.assertEqual(schemas["StoppedTimer"]["properties"]["status"]["const"], "stopped")
        self.assertEqual(_schema_name(_operation(self.spec, "stopTimer")["responses"]["200"]), "TimerStopResult")
        self.assertEqual(_schema_name(_operation(self.spec, "resumeTimer")["responses"]["200"]), "Timer")
        self.assertEqual(_schema_name(_operation(self.spec, "pauseTimer")["responses"]["200"]), "Timer")
        entry_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1"
        timer_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa2"
        matter = "018f1a00-7b3c-7d4e-8a11-333333333333"
        user = "018f1a00-7b3c-7d4e-8a11-555555555555"
        activity = "018f1a00-7b3c-7d4e-8a11-121212121212"
        entry = {
            "id": entry_id,
            "created_at": "2026-11-07T18:00:00Z",
            "created_by": user,
            "updated_at": "2026-11-07T18:00:00Z",
            "matter_id": matter,
            "user_id": user,
            "worked_on": "2026-11-07",
            "increments": 2,
            "description": "Hearing",
            "activity_type_id": activity,
            "billable": True,
            "rate_cents": 0,
            "source": "timer",
            "custom_fields": [],
        }
        terminal = {
            "id": timer_id,
            "matter_id": matter,
            "user_id": user,
            "started_at": "2026-11-07T17:00:00Z",
            "stopped_at": "2026-11-07T18:00:00Z",
            "accumulated_seconds": 3600,
            "status": "stopped",
            "time_entry_id": entry_id,
        }
        _component_validator(self.spec, "TimerStopResult").validate({"timer": terminal, "time_entry": entry})
        self.assertEqual(terminal["status"], "stopped")
        self.assertEqual(terminal["time_entry_id"], entry["id"])
        self.assertEqual(terminal["matter_id"], entry["matter_id"])
        stopped = _component_validator(self.spec, "StoppedTimer")
        for status in ("running", "paused"):
            with self.assertRaises(Exception):
                stopped.validate({**terminal, "status": status})
        active = {
            "id": timer_id,
            "created_at": "2026-11-07T17:00:00Z",
            "created_by": user,
            "updated_at": "2026-11-07T17:30:00Z",
            "matter_id": matter,
            "user_id": user,
            "started_at": "2026-11-07T17:00:00Z",
            "accumulated_seconds": 1800,
            "status": "paused",
        }
        _component_validator(self.spec, "Timer").validate(active)
        _component_validator(self.spec, "Timer").validate({**active, "status": "running"})
        with self.assertRaises(Exception):
            _component_validator(self.spec, "Timer").validate({**active, "status": "stopped"})

    def test_merge_wall_values_must_be_resolved_booleans(self):
        source = "018f1a00-7b3c-7d4e-8a11-333333333333"
        survivor = "018f1a00-7b3c-7d4e-8a11-333333333339"
        operation = _operation(self.spec, "mergeMatter")

        def decide(walls, body):
            raw = {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "attorney",
                "token_scopes": ["matters:write"],
                "matter_walls": walls,
                "matter_membership": {},
                "tool": "mergeMatter",
                "arguments": {"path": {"matter_id": source}, "body": body},
            }
            mcp = mcp_tool_dispatch(self.spec, raw)
            http = operation_authorization(self.spec, operation, _mcp_operation_call(raw, raw["arguments"], operation))
            self.assertEqual(http["result"], mcp["result"])
            self.assertEqual(http["effects"], mcp["effects"])
            self.assertEqual(http.get("relinked"), mcp.get("relinked"))
            self.assertEqual(http.get("archived"), mcp.get("archived"))
            self.assertEqual(http.get("events"), mcp.get("events"))
            return mcp

        allowed = decide({source: False, survivor: False}, {"survivor_id": survivor, "matter_walled": False})
        self.assertEqual(allowed["result"], "allowed")
        self.assertTrue(allowed["relinked"])
        self.assertTrue(allowed["archived"])
        self.assertEqual(allowed["events"], ["matter.merged"])
        for walls in (
            {source: None, survivor: False},
            {source: False, survivor: None},
            {source: "false", survivor: False},
            {source: False, survivor: 0},
            {source: False, survivor: []},
            {survivor: False},
            {source: False},
        ):
            denied = decide(walls, {"survivor_id": survivor, "matter_walled": False})
            self.assertNotEqual(denied["result"], "allowed", walls)
            self.assertFalse(denied["effects"], walls)
            self.assertFalse(denied["relinked"], walls)
            self.assertFalse(denied["archived"], walls)
            self.assertEqual(denied["events"], [], walls)

    def test_file_reparent_authorizes_source_and_destination(self):
        source = "018f1a00-7b3c-7d4e-8a11-333333333333"
        destination = "018f1a00-7b3c-7d4e-8a11-333333333339"
        file_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1"
        operation = _operation(self.spec, "updateFileMetadata")

        def decide(role, walls, body, membership=None, stored=source, **extra):
            raw = {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": role,
                "token_scopes": ["files:write"],
                "matter_id": stored,
                "matter_walled": False,
                "matter_walls": walls,
                "matter_membership": {} if membership is None else membership,
                "tool": "updateFileMetadata",
                "arguments": {"path": {"file_id": file_id}, "body": body},
                **extra,
            }
            mcp = mcp_tool_dispatch(self.spec, raw)
            http = operation_authorization(self.spec, operation, _mcp_operation_call(raw, raw["arguments"], operation))
            self.assertEqual(http["result"], mcp["result"])
            self.assertEqual(http["effects"], mcp["effects"])
            self.assertEqual(http.get("reparented"), mcp.get("reparented"))
            self.assertEqual(http.get("cleared"), mcp.get("cleared"))
            self.assertEqual(http.get("events"), mcp.get("events"))
            return mcp

        same = decide("staff", {source: False}, {"matter_id": source, "matter_walled": False})
        self.assertEqual(same["result"], "allowed")
        self.assertTrue(same["effects"])
        self.assertFalse(same.get("reparented"))
        moved = decide("staff", {source: False, destination: False}, {"matter_id": destination})
        self.assertEqual(moved["result"], "allowed")
        self.assertTrue(moved["reparented"])
        self.assertEqual(moved["events"], ["file.updated"])
        staff_into_wall = decide("staff", {source: False, destination: True}, {"matter_id": destination, "matter_walled": False}, {destination: False})
        self.assertEqual(staff_into_wall["result"], "not_found")
        self.assertFalse(staff_into_wall["effects"])
        self.assertFalse(staff_into_wall["reparented"])
        self.assertEqual(staff_into_wall["events"], [])
        attorney_from_wall = decide("attorney", {source: True, destination: False}, {"matter_id": destination, "matter_walled": False}, {source: False, destination: True})
        self.assertEqual(attorney_from_wall["result"], "not_found")
        self.assertFalse(attorney_from_wall["effects"])
        self.assertFalse(attorney_from_wall.get("reparented"))
        self.assertEqual(attorney_from_wall.get("events") or [], [])
        unresolved = decide("staff", {source: False, destination: None}, {"matter_id": destination, "matter_walled": False})
        self.assertEqual(unresolved["result"], "not_found")
        self.assertFalse(unresolved["effects"])
        self.assertFalse(unresolved["reparented"])
        missing_destination = decide("staff", {source: False}, {"matter_id": destination})
        self.assertEqual(missing_destination["result"], "not_found")
        self.assertFalse(missing_destination["reparented"])
        cleared = decide("staff", {source: False}, {"matter_id": None})
        self.assertEqual(cleared["result"], "allowed")
        self.assertTrue(cleared["effects"])
        self.assertTrue(cleared["cleared"])
        self.assertFalse(cleared.get("reparented"))
        self.assertEqual(cleared["events"], ["file.updated"])
        blocked_clear = decide("staff", {source: True}, {"matter_id": None, "matter_walled": False}, {source: False})
        self.assertEqual(blocked_clear["result"], "not_found")
        self.assertFalse(blocked_clear["effects"])
        self.assertEqual(blocked_clear["events"], [])
        self.assertFalse(blocked_clear.get("cleared"))
        attorney_only = decide("staff", {source: False, destination: False}, {"matter_id": destination, "attorney_only": False}, attorney_only=True)
        self.assertEqual(attorney_only["result"], "not_found")
        self.assertFalse(attorney_only["effects"])
        self.assertEqual(attorney_only.get("events") or [], [])
        self.assertFalse(attorney_only.get("reparented"))
        authorized_walls = decide("attorney", {source: True, destination: True}, {"matter_id": destination}, {source: True, destination: True})
        self.assertEqual(authorized_walls["result"], "allowed")
        self.assertTrue(authorized_walls["reparented"])
        assigned = decide("staff", {destination: False}, {"matter_id": destination}, stored=None)
        self.assertEqual(assigned["result"], "allowed")
        self.assertTrue(assigned["reparented"])
        self.assertEqual(assigned["events"], ["file.updated"])
        unresolved_source = decide("staff", {destination: False}, {"matter_id": destination, "matter_walled": False}, stored=0)
        self.assertEqual(unresolved_source["result"], "not_found")
        self.assertFalse(unresolved_source["effects"])
        self.assertEqual(unresolved_source["events"], [])
        self.assertFalse(unresolved_source.get("reparented"))

        rows = (
            ("resolveDocTenderException", "doc_tender:write", "attorney", "exception_id", ["doc_tender.exception_resolved"]),
            ("updateTimeEntry", "time:write", "staff", "time_entry_id", ["time_entry.updated"]),
            ("updateCalendarEvent", "calendar:write", "staff", "calendar_event_id", ["calendar_event.updated"]),
        )
        for operation_id, scope, role, path_key, events in rows:
            target = _operation(self.spec, operation_id)

            def one(body, walls, membership=None, stored=source, _operation=target, _scope=scope, _role=role, _path=path_key, **extra):
                raw = {
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "role": _role,
                    "token_scopes": [_scope],
                    "matter_id": stored,
                    "matter_walls": walls,
                    "matter_membership": {} if membership is None else membership,
                    "tool": _operation["operationId"],
                    "arguments": {"path": {_path: file_id}, "body": body},
                    **extra,
                }
                mcp = mcp_tool_dispatch(self.spec, raw)
                http = operation_authorization(self.spec, _operation, _mcp_operation_call(raw, raw["arguments"], _operation))
                self.assertEqual(http["result"], mcp["result"], _operation["operationId"])
                self.assertEqual(http["effects"], mcp["effects"], _operation["operationId"])
                self.assertEqual(http.get("events"), mcp.get("events"), _operation["operationId"])
                return mcp

            denied = one({"matter_id": destination, "attorney_only": False}, {source: False, destination: True}, {destination: False})
            self.assertEqual(denied["result"], "not_found", operation_id)
            self.assertFalse(denied["effects"], operation_id)
            self.assertEqual(denied.get("events") or [], [], operation_id)
            opened = one({"matter_id": destination}, {source: False, destination: False})
            self.assertEqual(opened["result"], "allowed", operation_id)
            self.assertTrue(opened["effects"], operation_id)
            self.assertEqual(opened["events"], events, operation_id)
            if operation_id == "updateTimeEntry":
                blank = one({"matter_id": None}, {source: False})
                self.assertEqual(blank["result"], "validation_failed", operation_id)
                self.assertFalse(blank["effects"], operation_id)
                self.assertEqual(blank.get("events") or [], [], operation_id)
            else:
                blank = one({"matter_id": None}, {source: False})
                self.assertEqual(blank["result"], "allowed", operation_id)
                self.assertTrue(blank.get("cleared"), operation_id)
                self.assertEqual(blank["events"], events, operation_id)
        exception = _operation(self.spec, "resolveDocTenderException")
        staff_raw = {
            "authorization": "Bearer secret",
            "authorization_scheme": "Bearer",
            "credential_valid": True,
            "role": "staff",
            "token_scopes": ["doc_tender:write"],
            "matter_id": source,
            "attorney_only": True,
            "matter_walls": {source: False, destination: False},
            "tool": "resolveDocTenderException",
            "arguments": {"path": {"exception_id": file_id}, "body": {"action": "assign_matter", "matter_id": destination, "attorney_only": False}},
        }
        staff_decision = mcp_tool_dispatch(self.spec, staff_raw)
        self.assertEqual(staff_decision["result"], "forbidden")
        self.assertFalse(staff_decision["effects"])
        self.assertEqual(staff_decision.get("events") or [], [])

    def test_association_walls_fail_closed(self):
        source = "018f1a00-7b3c-7d4e-8a11-333333333333"
        destination = "018f1a00-7b3c-7d4e-8a11-333333333339"
        file_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1"
        activity = "018f1a00-7b3c-7d4e-8a11-121212121212"
        time_body = {"matter_id": destination, "worked_on": "2026-11-07", "increments": 2, "description": "Review", "activity_type_id": activity}
        _component_validator(self.spec, "FileMetadataWrite").validate({"matter_id": None})
        _component_validator(self.spec, "FileMetadataWrite").validate({"matter_id": destination})
        _component_validator(self.spec, "TimeEntryWritePatch").validate({"matter_id": destination})
        with self.assertRaises(Exception):
            _component_validator(self.spec, "TimeEntryWritePatch").validate({"matter_id": None})
        _component_validator(self.spec, "CalendarEventWritePatch").validate({"matter_id": None})
        _component_validator(self.spec, "CalendarEventWritePatch").validate({"matter_id": destination})
        _component_validator(self.spec, "TimeEntryWrite").validate(time_body)

        def both(operation_id, raw):
            operation = _operation(self.spec, operation_id)
            mcp = mcp_tool_dispatch(self.spec, raw)
            http = operation_authorization(self.spec, operation, _mcp_operation_call(raw, raw["arguments"], operation))
            self.assertEqual(http["result"], mcp["result"], operation_id)
            self.assertEqual(http["effects"], mcp["effects"], operation_id)
            self.assertEqual(http.get("events"), mcp.get("events"), operation_id)
            self.assertEqual(http.get("routed"), mcp.get("routed"), operation_id)
            return mcp

        def file_call(body, **trusted):
            raw = {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "staff",
                "token_scopes": ["files:write"],
                "tool": "updateFileMetadata",
                "arguments": {"path": {"file_id": file_id}, "body": body},
                **trusted,
            }
            return both("updateFileMetadata", raw)

        omitted_clear = file_call({"matter_id": None})
        self.assertEqual(omitted_clear["result"], "not_found")
        self.assertFalse(omitted_clear["effects"])
        self.assertEqual(omitted_clear.get("events") or [], [])
        omitted_move = file_call({"matter_id": destination}, matter_walls={destination: False})
        self.assertEqual(omitted_move["result"], "not_found")
        self.assertFalse(omitted_move["effects"])
        self.assertEqual(omitted_move.get("events") or [], [])
        unassigned = file_call({"matter_id": destination}, matter_id=None, matter_walls={destination: False})
        self.assertEqual(unassigned["result"], "allowed")
        self.assertTrue(unassigned["effects"])
        self.assertTrue(unassigned["reparented"])
        known_clear = file_call({"matter_id": None}, matter_id=source, matter_walls={source: False})
        self.assertEqual(known_clear["result"], "allowed")
        self.assertTrue(known_clear["cleared"])
        bad_target = file_call({"matter_id": destination}, matter_id=source, matter_walls={source: False, destination: None})
        self.assertEqual(bad_target["result"], "not_found")
        self.assertFalse(bad_target["effects"])
        self.assertEqual(bad_target.get("events") or [], [])
        missing_target = file_call({"matter_id": destination}, matter_id=source, matter_walls={source: False})
        self.assertEqual(missing_target["result"], "not_found")
        self.assertFalse(missing_target["effects"])
        resolved_move = file_call({"matter_id": destination}, matter_id=source, matter_walls={source: False, destination: False})
        self.assertEqual(resolved_move["result"], "allowed")
        self.assertTrue(resolved_move["reparented"])
        hidden_source = file_call({"matter_id": destination}, matter_id=source, matter_walls={source: True, destination: False}, matter_membership={source: False})
        self.assertEqual(hidden_source["result"], "not_found")
        self.assertFalse(hidden_source["effects"])
        unknown_source_wall = file_call({"matter_id": None}, matter_id=source, matter_walls={source: None})
        self.assertEqual(unknown_source_wall["result"], "not_found")
        self.assertFalse(unknown_source_wall["effects"])
        self.assertFalse(unknown_source_wall.get("cleared"))
        created = both(
            "createTimeEntry",
            {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "staff",
                "token_scopes": ["time:write"],
                "matter_walled": False,
                "tool": "createTimeEntry",
                "arguments": {"body": time_body},
            },
        )
        self.assertEqual(created["result"], "allowed")
        self.assertTrue(created["effects"])

        time_handler = {"time": {"replaced": True, "available": True, "module_id": "time-mod"}}
        calendar_handler = {"calendar": {"replaced": True, "available": True, "module_id": "cal-mod"}}

        def replaced(operation_id, scope, handler, body, walls, membership=None):
            raw = {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "staff",
                "token_scopes": [scope],
                "matter_id": source,
                "matter_walls": walls,
                "matter_membership": {} if membership is None else membership,
                "domain_handlers": handler,
                "tool": operation_id,
                "arguments": {"path": {"time_entry_id" if operation_id == "updateTimeEntry" else "calendar_event_id": file_id}, "body": body},
            }
            return both(operation_id, raw)

        for operation_id, scope, handler in (
            ("updateTimeEntry", "time:write", time_handler),
            ("updateCalendarEvent", "calendar:write", calendar_handler),
        ):
            hidden = replaced(operation_id, scope, handler, {"matter_id": destination}, {source: False, destination: True}, {destination: False})
            self.assertEqual(hidden["result"], "not_found", operation_id)
            self.assertFalse(hidden["effects"], operation_id)
            self.assertIsNone(hidden.get("routed"), operation_id)
            self.assertEqual(hidden.get("events") or [], [], operation_id)
            routed = replaced(operation_id, scope, handler, {"matter_id": destination}, {source: False, destination: False})
            self.assertEqual(routed["result"], "allowed", operation_id)
            self.assertFalse(routed["effects"], operation_id)
            self.assertEqual(routed["routed"], handler[next(iter(handler))]["module_id"], operation_id)
        calendar_clear = replaced("updateCalendarEvent", "calendar:write", calendar_handler, {"matter_id": None}, {source: False})
        self.assertEqual(calendar_clear["result"], "allowed")
        self.assertFalse(calendar_clear["effects"])
        self.assertEqual(calendar_clear["routed"], "cal-mod")
        time_null = replaced("updateTimeEntry", "time:write", time_handler, {"matter_id": None}, {source: False})
        self.assertEqual(time_null["result"], "validation_failed")
        self.assertFalse(time_null["effects"])
        self.assertIsNone(time_null.get("routed"))
        client_blocked = replaced("updateTimeEntry", "time:write", time_handler, {"matter_id": destination}, {source: False, destination: False})
        client_blocked = both(
            "updateTimeEntry",
            {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "client",
                "token_scopes": ["time:write"],
                "matter_id": source,
                "shared": False,
                "matter_walls": {source: False, destination: False},
                "domain_handlers": time_handler,
                "tool": "updateTimeEntry",
                "arguments": {"path": {"time_entry_id": file_id}, "body": {"matter_id": destination}},
            },
        )
        self.assertNotEqual(client_blocked["result"], "allowed")
        self.assertFalse(client_blocked["effects"])
        self.assertIsNone(client_blocked.get("routed"))

        entry = {"matter_id": source, "worked_on": "2026-11-07", "increments": 2, "description": "Review", "activity_type_id": activity}
        other = {"matter_id": destination, "worked_on": "2026-11-07", "increments": 2, "description": "Review", "activity_type_id": activity}
        _component_validator(self.spec, "TimeEntryWrite").validate(entry)

        def batch(walls, items, membership=None, **extra):
            raw = {
                "cookie": "togavsu_session",
                "credential_valid": True,
                "role": "staff",
                "matter_walls": walls,
                "csrf_verifier": {"valid": True, "token": "csrf-1"},
                "transport_headers": {"X-CSRF-Token": "csrf-1"},
                "tool": "bulkTimeEntry",
                "arguments": {"body": {"items": items}},
                **extra,
            }
            if membership is not None:
                raw["matter_membership"] = membership
            return both("bulkTimeEntry", raw)

        unwalled = batch({source: False}, [entry])
        self.assertEqual(unwalled["result"], "allowed")
        self.assertTrue(unwalled["effects"])
        on_wall = batch({source: True}, [entry], {source: True})
        self.assertEqual(on_wall["result"], "allowed")
        self.assertTrue(on_wall["effects"])
        off_wall = batch({source: True}, [entry])
        self.assertEqual(off_wall["result"], "not_found")
        self.assertFalse(off_wall["effects"])
        null_wall = batch({source: None}, [entry])
        self.assertEqual(null_wall["result"], "not_found")
        self.assertFalse(null_wall["effects"])
        self.assertEqual(null_wall.get("events") or [], [])
        for bad in ("false", 0, 1, []):
            malformed = batch({source: bad}, [entry])
            self.assertEqual(malformed["result"], "not_found", bad)
            self.assertFalse(malformed["effects"], bad)
        mixed = batch({source: False, destination: None}, [entry, other])
        self.assertEqual(mixed["result"], "not_found")
        self.assertFalse(mixed["effects"])
        mixed_hidden = batch({source: False, destination: True}, [entry, other], {destination: False})
        self.assertEqual(mixed_hidden["result"], "not_found")
        self.assertFalse(mixed_hidden["effects"])

    def test_trusted_wall_grant_and_csrf(self):
        source = "018f1a00-7b3c-7d4e-8a11-333333333333"
        destination = "018f1a00-7b3c-7d4e-8a11-333333333339"
        file_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1"
        version_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa2"
        other_file = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa3"
        other_version = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa4"
        event_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa5"
        activity = "018f1a00-7b3c-7d4e-8a11-121212121212"
        _component_validator(self.spec, "FileMetadataWrite").validate({"matter_id": None})
        _component_validator(self.spec, "FileMetadataWrite").validate({"matter_id": destination})
        _component_validator(self.spec, "ContactWrite").validate({"kind": "person", "given_name": "Ada"})
        _component_validator(self.spec, "CalendarEventWritePatch").validate({"matter_id": destination})
        _component_validator(self.spec, "CalendarEventWritePatch").validate({"matter_id": None})
        issued = {
            "module_id": "malware",
            "event_id": event_id,
            "event_type": "file.version_created",
            "file_id": file_id,
            "version_id": version_id,
            "matter_id": source,
            "allowed_operations": ["downloadFileVersion"],
            "allowed_fields": ["name", "extension"],
            "expires_at": "2026-11-07T18:00:00Z",
            "revoked": False,
            "issued_by": "core",
            "expired": False,
            "invalid": False,
        }
        fabricated = {**issued, "file_id": other_file, "version_id": other_version, "matter_id": destination}
        _component_validator(self.spec, "TaskAuthorization").validate({key: issued[key] for key in ("module_id", "event_id", "event_type", "file_id", "version_id", "matter_id", "allowed_operations", "allowed_fields", "expires_at", "revoked")})
        _component_validator(self.spec, "TaskAuthorization").validate({key: fabricated[key] for key in ("module_id", "event_id", "event_type", "file_id", "version_id", "matter_id", "allowed_operations", "allowed_fields", "expires_at", "revoked")})
        transport = _operation(self.spec, "postMcp")
        self.assertIn("csrfToken", [_param_name(item) for item in transport["parameters"]])
        from contract.build import render_mcp

        catalog = render_mcp(self.spec)
        self.assertEqual(catalog["csrf_header"], "X-CSRF-Token")
        download = next(tool for tool in catalog["tools"] if tool["name"] == "downloadFileVersion")
        credential = download["transfer"]["credentials"]["task_authorization"]
        self.assertEqual(credential["schema"]["type"], "string")
        self.assertFalse(credential["claims_from_arguments"])

        def quiet(decision):
            self.assertFalse(decision["effects"])
            self.assertEqual(decision.get("events") or [], [])
            self.assertIsNone(decision.get("routed"))
            self.assertFalse(decision.get("cleared"))
            return decision

        def file_call(body, **trusted):
            raw = {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "staff",
                "token_scopes": ["files:write"],
                "tool": "updateFileMetadata",
                "arguments": {"path": {"file_id": file_id}, "body": body},
                **trusted,
            }
            return mcp_tool_dispatch(self.spec, raw)

        for label, trusted in (
            ("null", {"matter_id": source, "matter_walled": None}),
            ("omitted", {"matter_id": source}),
            ("string", {"matter_id": source, "matter_walled": "false"}),
            ("zero", {"matter_id": source, "matter_walled": 0}),
            ("list map", {"matter_id": source, "matter_walls": ["nope"], "matter_walled": False}),
            ("null map", {"matter_id": source, "matter_walls": {source: None}, "matter_walled": False}),
        ):
            denied = quiet(file_call({"matter_id": None}, **trusted))
            self.assertEqual(denied["result"], "not_found", label)
        cleared = file_call({"matter_id": None}, matter_id=source, matter_walled=False)
        self.assertEqual(cleared["result"], "allowed")
        self.assertTrue(cleared["effects"])
        self.assertTrue(cleared["cleared"])
        on_wall = file_call({"matter_id": None}, matter_id=source, matter_walled=True, matter_membership={source: True})
        self.assertEqual(on_wall["result"], "allowed")
        self.assertTrue(on_wall["cleared"])
        off_wall = quiet(file_call({"matter_id": None}, matter_id=source, matter_walled=True))
        self.assertEqual(off_wall["result"], "not_found")
        map_wins = file_call({"matter_id": None}, matter_id=source, matter_walls={source: False}, matter_walled=True)
        self.assertEqual(map_wins["result"], "allowed")
        self.assertTrue(map_wins["cleared"])
        reassign = quiet(file_call({"matter_id": destination}, matter_id=source, matter_walled=False))
        self.assertEqual(reassign["result"], "not_found")
        reassign_scalar = quiet(file_call({"matter_id": destination}, matter_id=source, matter_walled=None, matter_walls={destination: False}))
        self.assertEqual(reassign_scalar["result"], "not_found")
        created = mcp_tool_dispatch(
            self.spec,
            {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "staff",
                "token_scopes": ["time:write"],
                "matter_walled": False,
                "tool": "createTimeEntry",
                "arguments": {"body": {"matter_id": source, "worked_on": "2026-11-07", "increments": 2, "description": "Review", "activity_type_id": activity}},
            },
        )
        self.assertEqual(created["result"], "allowed")
        self.assertTrue(created["effects"])

        def scan(arguments, **trusted):
            raw = {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "viewer_present": False,
                "module_id": "malware",
                "caller_module_id": "malware",
                "tool": "downloadFileVersion",
                "module_scopes": ["files:read"],
                "token_scopes": ["files:read"],
                "approved_scopes": ["files:read"],
                "registered": True,
                "subscriptions": ["file.version_created"],
                "matter_id": issued["matter_id"],
                "arguments": arguments,
                **trusted,
            }
            return mcp_tool_dispatch(self.spec, raw)

        verified = scan(
            {"task_authorization": "scan-token", "path": {"file_id": file_id, "version_id": version_id}},
            task_verifier={"valid": True, "credential": "scan-token", "claims": issued},
        )
        self.assertEqual(verified["result"], "allowed")
        self.assertTrue(verified["effects"])
        for label, arguments, trusted in (
            ("fabricated", {"task_authorization": fabricated, "path": {"file_id": other_file, "version_id": other_version}}, {"matter_id": destination}),
            ("override", {"task_authorization": fabricated, "path": {"file_id": other_file, "version_id": other_version}}, {"grant": issued, "matter_id": destination}),
            ("boolean", {"task_authorization": True, "path": {"file_id": file_id, "version_id": version_id}}, {}),
            ("unverified", {"task_authorization": "scan-token", "path": {"file_id": file_id, "version_id": version_id}}, {}),
            ("valid missing", {"task_authorization": "scan-token", "path": {"file_id": file_id, "version_id": version_id}}, {"task_verifier": {"credential": "scan-token", "claims": issued}}),
            ("invalid", {"task_authorization": "scan-token", "path": {"file_id": file_id, "version_id": version_id}}, {"task_verifier": {"valid": True, "credential": "scan-token", "claims": {**issued, "invalid": True}}}),
            ("expired", {"task_authorization": "scan-token", "path": {"file_id": file_id, "version_id": version_id}}, {"task_verifier": {"valid": True, "credential": "scan-token", "claims": {**issued, "expired": True}}}),
            ("revoked", {"task_authorization": "scan-token", "path": {"file_id": file_id, "version_id": version_id}}, {"task_verifier": {"valid": True, "credential": "scan-token", "claims": {**issued, "revoked": True}}}),
        ):
            denied = quiet(scan(arguments, **trusted))
            self.assertEqual(denied["result"], "forbidden", label)
        unrelated = quiet(
            scan(
                {"task_authorization": "scan-token", "path": {"file_id": other_file, "version_id": other_version}},
                task_verifier={"valid": True, "credential": "scan-token", "claims": issued},
                matter_id=destination,
            )
        )
        self.assertEqual(unrelated["result"], "not_found")

        def cookie_call(tool, arguments, **trusted):
            return mcp_tool_dispatch(
                self.spec,
                {"cookie": "togavsu_session", "credential_valid": True, "role": "staff", "tool": tool, "arguments": arguments, **trusted},
            )

        contact = {"kind": "person", "given_name": "Ada"}
        missing_csrf = quiet(cookie_call("createContact", {"body": contact}))
        self.assertEqual(missing_csrf["result"], "csrf_required")
        flagged = quiet(cookie_call("createContact", {"body": contact, "csrf_valid": True}))
        self.assertEqual(flagged["result"], "csrf_required")
        mismatched = quiet(cookie_call("createContact", {"body": contact}, transport_headers={"X-CSRF-Token": "nope"}, csrf_verifier={"valid": True, "token": "csrf-1"}))
        self.assertEqual(mismatched["result"], "csrf_required")
        created_contact = cookie_call("createContact", {"body": contact}, transport_headers={"X-CSRF-Token": "csrf-1"}, csrf_verifier={"valid": True, "token": "csrf-1"})
        self.assertEqual(created_contact["result"], "allowed")
        self.assertTrue(created_contact["effects"])
        listed = cookie_call("listMatter", {})
        self.assertEqual(listed["result"], "allowed")
        bearer_contact = mcp_tool_dispatch(
            self.spec,
            {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "staff",
                "token_scopes": ["contacts:write"],
                "tool": "createContact",
                "arguments": {"body": contact},
            },
        )
        self.assertEqual(bearer_contact["result"], "allowed")
        self.assertTrue(bearer_contact["effects"])
        handler = {"calendar": {"replaced": True, "available": True, "module_id": "cal-mod"}}
        hidden = quiet(
            cookie_call(
                "updateCalendarEvent",
                {"path": {"calendar_event_id": file_id}, "body": {"matter_id": destination}},
                matter_id=source,
                matter_walls={source: False, destination: True},
                matter_membership={destination: False},
                domain_handlers=handler,
            )
        )
        self.assertEqual(hidden["result"], "csrf_required")
        hidden_verified = quiet(
            cookie_call(
                "updateCalendarEvent",
                {"path": {"calendar_event_id": file_id}, "body": {"matter_id": destination}},
                transport_headers={"X-CSRF-Token": "csrf-1"},
                csrf_verifier={"valid": True, "token": "csrf-1"},
                matter_id=source,
                matter_walls={source: False, destination: True},
                matter_membership={destination: False},
                domain_handlers=handler,
            )
        )
        self.assertEqual(hidden_verified["result"], "not_found")
        routed = cookie_call(
            "updateCalendarEvent",
            {"path": {"calendar_event_id": file_id}, "body": {"matter_id": destination}},
            transport_headers={"X-CSRF-Token": "csrf-1"},
            csrf_verifier={"valid": True, "token": "csrf-1"},
            matter_id=source,
            matter_walls={source: False, destination: False},
            domain_handlers=handler,
        )
        self.assertEqual(routed["result"], "allowed")
        self.assertFalse(routed["effects"])
        self.assertEqual(routed["routed"], "cal-mod")
        bearer_routed = mcp_tool_dispatch(
            self.spec,
            {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "staff",
                "token_scopes": ["calendar:write"],
                "matter_id": source,
                "matter_walls": {source: False, destination: False},
                "domain_handlers": handler,
                "tool": "updateCalendarEvent",
                "arguments": {"path": {"calendar_event_id": file_id}, "body": {"matter_id": destination}},
            },
        )
        self.assertEqual(bearer_routed["result"], "allowed")
        self.assertFalse(bearer_routed["effects"])
        self.assertEqual(bearer_routed["routed"], "cal-mod")

    def test_factor_reset_requires_reauthentication(self):
        reset = _operation(self.spec, "resetTotp")
        regenerate = _operation(self.spec, "regenerateRecoveryCodes")
        schema = _component_validator(self.spec, "FactorReauthentication")
        schema.validate({"current_password": "correct-horse"})
        schema.validate({"reauthentication_token": "proof-1"})
        with self.assertRaises(Exception):
            schema.validate({"reauthenticated": True})
        owner = "018f1a00-7b3c-7d4e-8a11-555555555555"
        other = "018f1a00-7b3c-7d4e-8a11-666666666666"

        def decide(operation, body, **trusted):
            raw = {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "staff",
                "owner": "self",
                "token_scopes": ["profile:write"],
                "caller_user_id": owner,
                "user_id": owner,
                "tool": operation["operationId"],
                "arguments": {"body": body},
                **trusted,
            }
            mcp = mcp_tool_dispatch(self.spec, raw)
            http = operation_authorization(self.spec, operation, _mcp_operation_call(raw, raw["arguments"], operation))
            self.assertEqual(http["result"], mcp["result"])
            self.assertEqual(http["effects"], mcp["effects"])
            self.assertEqual(http.get("events"), mcp.get("events"))
            self.assertEqual(http.get("challenge_issued"), mcp.get("challenge_issued"))
            self.assertEqual(http.get("codes_issued"), mcp.get("codes_issued"))
            self.assertNotIn("secret", mcp)
            self.assertNotIn("codes", mcp)
            return mcp

        alone = decide(reset, {})
        self.assertEqual(alone["result"], "unauthenticated")
        self.assertFalse(alone["effects"])
        self.assertEqual(alone["events"], [])
        self.assertFalse(alone["challenge_issued"])
        self.assertTrue(alone["old_secret_active"])
        spoofed = decide(reset, {"reauthentication_token": "proof-1"}, reauthentication_proof={"token": "other-proof", "owner_user_id": owner, "action": "resetTotp"})
        self.assertEqual(spoofed["result"], "unauthenticated")
        self.assertFalse(spoofed["effects"])
        self.assertFalse(spoofed["challenge_issued"])
        stale = decide(reset, {"reauthentication_token": "proof-1"}, reauthentication_proof={"token": "proof-1", "owner_user_id": owner, "action": "resetTotp", "expired": True})
        self.assertEqual(stale["result"], "unauthenticated")
        reused = decide(reset, {"reauthentication_token": "proof-1"}, reauthentication_proof={"token": "proof-1", "owner_user_id": owner, "action": "resetTotp", "used": True})
        self.assertEqual(reused["result"], "unauthenticated")
        wrong_owner = decide(reset, {"reauthentication_token": "proof-1"}, reauthentication_proof={"token": "proof-1", "owner_user_id": other, "action": "resetTotp"})
        self.assertEqual(wrong_owner["result"], "forbidden")
        self.assertFalse(wrong_owner["effects"])
        wrong_action = decide(reset, {"reauthentication_token": "proof-1"}, reauthentication_proof={"token": "proof-1", "owner_user_id": owner, "action": "regenerateRecoveryCodes"})
        self.assertEqual(wrong_action["result"], "forbidden")
        self.assertFalse(wrong_action["challenge_issued"])
        new_code = decide(reset, {"totp_code": "123456"})
        self.assertEqual(new_code["result"], "unauthenticated")
        self.assertFalse(new_code["challenge_issued"])
        reset_ok = decide(reset, {"current_password": "correct-horse"}, password_matches=True)
        self.assertEqual(reset_ok["result"], "allowed")
        self.assertTrue(reset_ok["effects"])
        self.assertTrue(reset_ok["challenge_issued"])
        self.assertTrue(reset_ok["old_secret_active"])
        self.assertEqual(reset_ok["events"], ["user.totp_reset_started"])
        factor_ok = decide(reset, {"totp_code": "654321"}, existing_totp_matches=True)
        self.assertEqual(factor_ok["result"], "allowed")
        self.assertTrue(factor_ok["challenge_issued"])
        codes_alone = decide(regenerate, {})
        self.assertEqual(codes_alone["result"], "unauthenticated")
        self.assertFalse(codes_alone["effects"])
        self.assertFalse(codes_alone["codes_issued"])
        self.assertEqual(codes_alone["events"], [])
        codes_ok = decide(regenerate, {"current_password": "correct-horse"}, password_matches=True)
        self.assertEqual(codes_ok["result"], "allowed")
        self.assertTrue(codes_ok["codes_issued"])
        self.assertEqual(codes_ok["events"], ["user.recovery_codes_regenerated"])
        codes_wrong_action = decide(regenerate, {"reauthentication_token": "proof-1"}, reauthentication_proof={"token": "proof-1", "owner_user_id": owner, "action": "resetTotp"})
        self.assertEqual(codes_wrong_action["result"], "forbidden")
        self.assertFalse(codes_wrong_action["codes_issued"])
        unresolved_proof = decide(reset, {"reauthentication_token": "proof-1"}, reauthentication_proof={"token": "proof-1", "owner_user_id": owner, "action": "resetTotp"})
        self.assertEqual(unresolved_proof["result"], "unauthenticated")
        self.assertFalse(unresolved_proof["effects"])
        self.assertFalse(unresolved_proof["challenge_issued"])
        bound = decide(
            reset,
            {"reauthentication_token": "proof-1"},
            reauthentication_proof={"token": "proof-1", "owner_user_id": owner, "action": "resetTotp", "valid": True},
        )
        self.assertEqual(bound["result"], "allowed")
        self.assertTrue(bound["challenge_issued"])
        self.assertTrue(bound["old_secret_active"])
        self.assertEqual(bound["events"], ["user.totp_reset_started"])

    def test_transport_link_and_matter_grant(self):
        source = "018f1a00-7b3c-7d4e-8a11-333333333333"
        other_matter = "018f1a00-7b3c-7d4e-8a11-333333333339"
        file_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1"
        other_file = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa2"
        contact = {"kind": "person", "given_name": "Ada"}
        link_body = {"to_file_id": other_file, "relationship": "attachment"}
        grant_body = {"assignment": "matter", "matter_id": source, "source": "email"}
        _component_validator(self.spec, "ContactWrite").validate(contact)
        _component_validator(self.spec, "FileLinkWrite").validate(link_body)
        _component_validator(self.spec, "BackgroundFileGrantRequest").validate(grant_body)
        _component_validator(self.spec, "BackgroundFileGrantRequest").validate({"assignment": "unassigned", "source": "importer"})
        _component_validator(self.spec, "FileMetadataWrite").validate({"matter_id": None})
        self.assertIn("HTTP header", _operation(self.spec, "postMcp")["description"])
        self.assertIn("not a field of params.arguments", self.spec["components"]["schemas"]["JsonRpcRequest"]["description"])

        def quiet(decision):
            self.assertFalse(decision["effects"])
            self.assertEqual(decision.get("events") or [], [])
            self.assertIsNone(decision.get("routed"))
            return decision

        def cookie_write(arguments, **trusted):
            return mcp_tool_dispatch(
                self.spec,
                {"cookie": "togavsu_session", "credential_valid": True, "role": "staff", "tool": "createContact", "arguments": arguments, **trusted},
            )

        transport = cookie_write({"body": contact}, transport_headers={"X-CSRF-Token": "csrf-1"}, csrf_verifier={"valid": True, "token": "csrf-1"})
        self.assertEqual(transport["result"], "allowed")
        self.assertTrue(transport["effects"])
        argument_header = quiet(cookie_write({"headers": {"X-CSRF-Token": "csrf-1"}, "body": contact}, csrf_verifier={"valid": True, "token": "csrf-1"}))
        self.assertEqual(argument_header["result"], "csrf_required")
        flagged = quiet(cookie_write({"body": contact, "csrf_valid": True}, transport_headers={"X-CSRF-Token": "csrf-1"}))
        self.assertEqual(flagged["result"], "csrf_required")
        mismatched = quiet(cookie_write({"body": contact}, transport_headers={"X-CSRF-Token": "other"}, csrf_verifier={"valid": True, "token": "csrf-1"}))
        self.assertEqual(mismatched["result"], "csrf_required")
        missing_verifier = quiet(cookie_write({"body": contact}, transport_headers={"X-CSRF-Token": "csrf-1"}, csrf_verifier={"token": "csrf-1"}))
        self.assertEqual(missing_verifier["result"], "csrf_required")
        listed = mcp_tool_dispatch(self.spec, {"cookie": "togavsu_session", "credential_valid": True, "role": "staff", "tool": "listMatter", "arguments": {}})
        self.assertEqual(listed["result"], "allowed")
        bearer_contact = mcp_tool_dispatch(
            self.spec,
            {"authorization": "Bearer secret", "authorization_scheme": "Bearer", "credential_valid": True, "role": "staff", "token_scopes": ["contacts:write"], "tool": "createContact", "arguments": {"body": contact}},
        )
        self.assertEqual(bearer_contact["result"], "allowed")
        handler = {"calendar": {"replaced": True, "available": True, "module_id": "cal-mod"}}

        def calendar(arguments, **trusted):
            return mcp_tool_dispatch(
                self.spec,
                {
                    "cookie": "togavsu_session",
                    "credential_valid": True,
                    "role": "staff",
                    "tool": "updateCalendarEvent",
                    "matter_id": source,
                    "matter_walls": {source: False, other_matter: False},
                    "domain_handlers": handler,
                    "arguments": arguments,
                    **trusted,
                },
            )

        argument_route = quiet(calendar({"headers": {"X-CSRF-Token": "csrf-1"}, "path": {"calendar_event_id": file_id}, "body": {"matter_id": other_matter}}, csrf_verifier={"valid": True, "token": "csrf-1"}))
        self.assertEqual(argument_route["result"], "csrf_required")
        hidden_route = quiet(
            calendar(
                {"path": {"calendar_event_id": file_id}, "body": {"matter_id": other_matter}},
                transport_headers={"X-CSRF-Token": "csrf-1"},
                csrf_verifier={"valid": True, "token": "csrf-1"},
                matter_walls={source: False, other_matter: True},
                matter_membership={other_matter: False},
            )
        )
        self.assertEqual(hidden_route["result"], "not_found")
        routed = calendar(
            {"path": {"calendar_event_id": file_id}, "body": {"matter_id": other_matter}},
            transport_headers={"X-CSRF-Token": "csrf-1"},
            csrf_verifier={"valid": True, "token": "csrf-1"},
        )
        self.assertEqual(routed["result"], "allowed")
        self.assertFalse(routed["effects"])
        self.assertEqual(routed["routed"], "cal-mod")

        def file_row(row_id, matter, **flags):
            return {"id": row_id, "matter_id": matter, "matter_walled": False, "attorney_only": False, "shared_with_client": True, **flags}

        def link(records, body=None, **trusted):
            return mcp_tool_dispatch(
                self.spec,
                {
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "role": trusted.pop("role", "staff"),
                    "token_scopes": ["files:write"],
                    "stored_files": records,
                    "tool": "createFileLink",
                    "arguments": {"path": {"file_id": file_id}, "body": body or {**link_body, "from_visible": True, "to_visible": True, "same_matter": True, "attorney_only": False, "matter_walled": False}},
                    **trusted,
                },
            )

        walled_target = quiet(link([file_row(file_id, source), file_row(other_file, source, matter_walled=True)], matter_membership={source: False}))
        self.assertEqual(walled_target["result"], "not_found")
        attorney_target = quiet(link([file_row(file_id, source), file_row(other_file, source, attorney_only=True)]))
        self.assertEqual(attorney_target["result"], "not_found")
        wrong_matter = quiet(link([file_row(file_id, source), file_row(other_file, other_matter)]))
        self.assertEqual(wrong_matter["result"], "matter_mismatch")
        missing_target = quiet(link([file_row(file_id, source)]))
        self.assertEqual(missing_target["result"], "not_found")
        substituted = quiet(link([file_row(file_id, source), file_row("018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa3", source)]))
        self.assertEqual(substituted["result"], "not_found")
        hidden_source = quiet(link([file_row(file_id, source, matter_walled=True), file_row(other_file, source)], matter_membership={source: False}))
        self.assertEqual(hidden_source["result"], "not_found")
        unresolved_target = quiet(link([file_row(file_id, source), {"id": other_file, "matter_id": source, "attorney_only": False}]))
        self.assertEqual(unresolved_target["result"], "not_found")
        linked = link([file_row(file_id, source), file_row(other_file, source)])
        self.assertEqual(linked["result"], "allowed")
        self.assertTrue(linked["effects"])
        attorney_link = link([file_row(file_id, source), file_row(other_file, source, attorney_only=True)], role="attorney")
        self.assertEqual(attorney_link["result"], "allowed")
        self.assertTrue(attorney_link["effects"])

        issuance = _operation(self.spec, "issueFilePublicationGrant")
        module = {"installed": True, "registered": True, "module_id": "email", "approved_scopes": ["files:write"]}
        job = {"module_id": "email", "source": "email", "assignment": "matter", "matter_id": source, "expires_at": "2026-11-07T20:00:00Z", "matter_walled": False}

        def issued(extra):
            return background_grant_issuance(issuance, module, {**job, **extra})

        for label, extra in (
            ("missing", {}),
            ("null", {"principal_matter_ids": None}),
            ("empty", {"principal_matter_ids": []}),
            ("string", {"principal_matter_ids": source}),
            ("null item", {"principal_matter_ids": [None]}),
            ("other matter", {"principal_matter_ids": [other_matter]}),
        ):
            denied = issued(extra)
            self.assertEqual(denied["result"], "not_found", label)
            self.assertIsNone(denied["grant"], label)
            self.assertEqual(denied["events"], [], label)
        matched = issued({"principal_matter_ids": [source]})
        self.assertEqual(matched["result"], "allowed")
        self.assertEqual(matched["grant"]["matter_id"], source)
        unassigned = background_grant_issuance(issuance, module, {"module_id": "email", "assignment": "unassigned", "source": "importer", "expires_at": "2026-11-07T20:00:00Z"})
        self.assertEqual(unassigned["result"], "allowed")
        self.assertIsNone(unassigned["grant"]["matter_id"])

        def grant_call(body, **trusted):
            return mcp_tool_dispatch(
                self.spec,
                {
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "viewer_present": False,
                    "module_id": "email",
                    "installed": True,
                    "registered": True,
                    "expires_at": "2026-11-07T20:00:00Z",
                    "matter_walled": False,
                    "token_scopes": ["files:write"],
                    "approved_scopes": ["files:write"],
                    "tool": "issueFilePublicationGrant",
                    "arguments": {"body": body},
                    **trusted,
                },
            )

        spoofed = quiet(grant_call({**grant_body, "principal_matter_ids": [source]}))
        self.assertEqual(spoofed["result"], "not_found")
        granted = grant_call(grant_body, principal_matter_ids=[source])
        self.assertEqual(granted["result"], "allowed")
        self.assertTrue(granted["effects"])
        cleared = mcp_tool_dispatch(
            self.spec,
            {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "staff",
                "token_scopes": ["files:write"],
                "matter_id": source,
                "matter_walled": False,
                "tool": "updateFileMetadata",
                "arguments": {"path": {"file_id": file_id}, "body": {"matter_id": None}},
            },
        )
        self.assertEqual(cleared["result"], "allowed")
        self.assertTrue(cleared["cleared"])
        null_map = quiet(
            mcp_tool_dispatch(
                self.spec,
                {
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "role": "staff",
                    "token_scopes": ["files:write"],
                    "matter_id": source,
                    "matter_walls": None,
                    "matter_walled": False,
                    "tool": "updateFileMetadata",
                    "arguments": {"path": {"file_id": file_id}, "body": {"matter_id": None}},
                },
            )
        )
        self.assertEqual(null_map["result"], "not_found")
        self.assertFalse(null_map.get("cleared"))

    def test_concrete_matter_and_selected_files(self):
        matter = "018f1a00-7b3c-7d4e-8a11-333333333333"
        other = "018f1a00-7b3c-7d4e-8a11-333333333339"
        file_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1"
        other_file = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa2"
        folder_id = "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbb1"
        activity = "018f1a00-7b3c-7d4e-8a11-121212121212"
        link_body = {"to_file_id": other_file, "relationship": "attachment"}
        _component_validator(self.spec, "FileLinkWrite").validate(link_body)
        _component_validator(self.spec, "FileArchiveRequest").validate({"file_ids": [file_id, other_file]})
        _component_validator(self.spec, "FileArchiveRequest").validate({"folder_id": folder_id})
        _component_validator(self.spec, "MatterWritePatch").validate({"status": "open"})
        self.assertIn({"from_visible": False, "to_visible": False, "result": "not_found"}, _operation(self.spec, "createFileLink")["x-togavsu-access"]["access_cases"])

        def quiet(decision):
            self.assertFalse(decision["effects"])
            self.assertEqual(decision.get("events") or [], [])
            self.assertIsNone(decision.get("routed"))
            return decision

        def matter_call(tool, scopes, arguments, **trusted):
            return mcp_tool_dispatch(
                self.spec,
                {
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "role": "staff",
                    "token_scopes": scopes,
                    "tool": tool,
                    "arguments": arguments,
                    **trusted,
                },
            )

        for label, trusted in (
            ("missing evidence", {}),
            ("null map", {"matter_walls": None}),
            ("absent entry", {"matter_walls": {}}),
            ("null value", {"matter_walls": {matter: None}}),
            ("string value", {"matter_walls": {matter: "false"}}),
            ("membership denied", {"matter_walls": {matter: True}, "matter_membership": {matter: False}}),
        ):
            denied = quiet(matter_call("getMatter", ["matters:read"], {"path": {"matter_id": matter}}, **trusted))
            self.assertEqual(denied["result"], "not_found", label)
            denied_write = quiet(matter_call("updateMatter", ["matters:write"], {"path": {"matter_id": matter}, "body": {"status": "open"}}, stored_status="open", **trusted))
            self.assertEqual(denied_write["result"], "not_found", label)
        unwalled = matter_call("getMatter", ["matters:read"], {"path": {"matter_id": matter}}, matter_walls={matter: False})
        self.assertEqual(unwalled["result"], "allowed")
        self.assertTrue(unwalled["effects"])
        on_wall = matter_call("getMatter", ["matters:read"], {"path": {"matter_id": matter}}, matter_walls={matter: True}, matter_membership={matter: True})
        self.assertEqual(on_wall["result"], "allowed")
        self.assertTrue(on_wall["effects"])
        written = matter_call("updateMatter", ["matters:write"], {"path": {"matter_id": matter}, "body": {"status": "open"}}, stored_status="open", matter_walled=False)
        self.assertEqual(written["result"], "allowed")
        self.assertTrue(written["effects"])
        listed = matter_call("listMatter", ["matters:read"], {})
        self.assertEqual(listed["result"], "allowed")
        time_body = {"matter_id": matter, "worked_on": "2026-11-07", "increments": 2, "description": "Review", "activity_type_id": activity}
        _component_validator(self.spec, "TimeEntryWrite").validate(time_body)
        missing_time = quiet(matter_call("createTimeEntry", ["time:write"], {"body": time_body}))
        self.assertEqual(missing_time["result"], "not_found")
        created = matter_call("createTimeEntry", ["time:write"], {"body": time_body}, matter_walled=False)
        self.assertEqual(created["result"], "allowed")
        self.assertTrue(created["effects"])

        def link(records, body=None, **trusted):
            return mcp_tool_dispatch(
                self.spec,
                {
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "role": "staff",
                    "token_scopes": ["files:write"],
                    "stored_files": records,
                    "tool": "createFileLink",
                    "arguments": {"path": {"file_id": file_id}, "body": body or {**link_body, "from_visible": True, "to_visible": True, "same_matter": True}},
                    **trusted,
                },
            )

        def row(row_id, matter_id=matter, **flags):
            return {"id": row_id, "matter_id": matter_id, "matter_walled": False, "attorney_only": False, "shared_with_client": True, "quarantined": False, "folder_id": folder_id, **flags}

        for label, records in (
            ("no rows", []),
            ("both unresolved", [{"id": file_id, "matter_id": matter}, {"id": other_file, "matter_id": matter}]),
            ("source only", [row(file_id)]),
            ("target only", [row(other_file)]),
            ("mismatched id", [row(file_id), row("018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa3")]),
        ):
            denied = quiet(link(records))
            self.assertEqual(denied["result"], "not_found", label)
        both_hidden = quiet(link([row(file_id, matter_walled=True), row(other_file, matter_walled=True)], matter_membership={matter: False}))
        self.assertEqual(both_hidden["result"], "not_found")
        linked = link([row(file_id), row(other_file)])
        self.assertEqual(linked["result"], "allowed")
        self.assertTrue(linked["effects"])

        def archive(body, records, role="staff", **trusted):
            return mcp_tool_dispatch(
                self.spec,
                {
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "role": role,
                    "token_scopes": ["files:read"],
                    "stored_files": records,
                    "tool": "createFileArchive",
                    "arguments": {"body": body},
                    **trusted,
                },
            )

        mixed = quiet(archive({"file_ids": [file_id, other_file]}, [row(file_id), row(other_file, matter_walled=True)], matter_membership={matter: False}, attorney_only=False, matter_walled=False))
        self.assertEqual(mixed["result"], "not_found")
        attorney_only = quiet(archive({"file_ids": [file_id]}, [row(file_id, attorney_only=True)], attorney_only=False))
        self.assertEqual(attorney_only["result"], "not_found")
        client_hidden = quiet(archive({"file_ids": [file_id]}, [row(file_id, shared_with_client=False)], role="client", shared=True, client_matter_ids=[matter]))
        self.assertEqual(client_hidden["result"], "not_found")
        client_ok = archive({"file_ids": [file_id]}, [row(file_id)], role="client", client_matter_ids=[matter])
        self.assertEqual(client_ok["result"], "allowed")
        self.assertTrue(client_ok["effects"])
        ordinary = row(other_file)
        ordinary.pop("financial_classification", None)
        receipt = row(file_id, financial_classification="receipt")
        accountant_mixed = quiet(archive({"file_ids": [file_id, other_file]}, [receipt, ordinary], role="accountant"))
        self.assertEqual(accountant_mixed["result"], "not_found")
        accountant_ok = archive({"file_ids": [file_id]}, [receipt], role="accountant")
        self.assertEqual(accountant_ok["result"], "allowed")
        self.assertTrue(accountant_ok["effects"])
        unknown = quiet(archive({"file_ids": [file_id, other_file]}, [row(file_id)]))
        self.assertEqual(unknown["result"], "not_found")
        duplicate = quiet(archive({"file_ids": [file_id, file_id]}, [row(file_id)]))
        self.assertEqual(duplicate["result"], "not_found")
        both_selectors = archive({"file_ids": [file_id], "folder_id": folder_id}, [row(file_id)])
        self.assertEqual(both_selectors["result"], "validation_failed")
        self.assertFalse(both_selectors["effects"])
        unknown_folder = quiet(archive({"folder_id": folder_id}, [row(file_id)], stored_folders=[]))
        self.assertEqual(unknown_folder["result"], "not_found")
        hidden_member = quiet(archive({"folder_id": folder_id}, [row(file_id), row(other_file, matter_walled=True)], stored_folders=[{"id": folder_id}], matter_membership={matter: False}))
        self.assertEqual(hidden_member["result"], "not_found")
        folder_ok = archive({"folder_id": folder_id}, [row(file_id), row(other_file)], stored_folders=[{"id": folder_id}])
        self.assertEqual(folder_ok["result"], "allowed")
        self.assertTrue(folder_ok["effects"])
        unassigned = archive({"file_ids": [file_id]}, [row(file_id, matter_id=None)])
        self.assertEqual(unassigned["result"], "allowed")
        self.assertTrue(unassigned["effects"])
        quarantined = quiet(archive({"file_ids": [file_id]}, [row(file_id, quarantined=True)]))
        self.assertEqual(quarantined["result"], "quarantine_confirmation_required")
        confirmed = archive({"file_ids": [file_id], "confirm_quarantine": True}, [row(file_id, quarantined=True)])
        self.assertEqual(confirmed["result"], "allowed")
        self.assertTrue(confirmed["effects"])
        omitted_scan = quiet(archive({"file_ids": [file_id]}, [row(file_id, quarantined=None)]))
        self.assertEqual(omitted_scan["result"], "not_found")
        bare = row(file_id)
        bare.pop("quarantined")
        omitted_key = quiet(archive({"file_ids": [file_id]}, [bare]))
        self.assertEqual(omitted_key["result"], "not_found")
        bad_ids = archive({"file_ids": [{"id": file_id}]}, [row(file_id)])
        self.assertEqual(bad_ids["result"], "validation_failed")
        self.assertFalse(bad_ids["effects"])

    def test_record_owner_and_client_file_access(self):
        matter = "018f1a00-7b3c-7d4e-8a11-333333333333"
        foreign = "018f1a00-7b3c-7d4e-8a11-333333333339"
        entry = "018f1a00-7b3c-7d4e-8a11-ccccccccccc1"
        event = "018f1a00-7b3c-7d4e-8a11-eeeeeeeeeee1"
        file_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1"
        other_file = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa2"
        job = "018f1a00-7b3c-7d4e-8a11-ddddddddddd1"
        contact = "018f1a00-7b3c-7d4e-8a11-222222222222"
        time_patch = {"description": "Review"}
        _component_validator(self.spec, "TimeEntryWritePatch").validate(time_patch)
        _component_validator(self.spec, "CalendarEventWritePatch").validate({"matter_id": None})
        _component_validator(self.spec, "FileArchiveRequest").validate({"file_ids": [file_id]})

        def quiet(decision):
            self.assertFalse(decision["effects"])
            self.assertEqual(decision.get("events") or [], [])
            self.assertIsNone(decision.get("routed"))
            return decision

        def call(tool, scopes, arguments, **trusted):
            return mcp_tool_dispatch(
                self.spec,
                {
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "role": "staff",
                    "token_scopes": scopes,
                    "tool": tool,
                    "arguments": arguments,
                    **trusted,
                },
            )

        handler = {"time": {"replaced": True, "available": True, "module_id": "time-mod"}}
        calendar = {"calendar": {"replaced": True, "available": True, "module_id": "cal-mod"}}
        for tool, scope, path_key, path_id in (
            ("getTimeEntry", "time:read", "time_entry_id", entry),
            ("updateTimeEntry", "time:write", "time_entry_id", entry),
            ("deleteCalendarEvent", "calendar:write", "calendar_event_id", event),
            ("getCalendarEvent", "calendar:read", "calendar_event_id", event),
            ("getFile", "files:read", "file_id", file_id),
            ("getDocTenderJob", "doc_tender:read", "job_id", job),
        ):
            body = {"body": time_patch} if tool == "updateTimeEntry" else {}
            denied = quiet(call(tool, [scope], {"path": {path_key: path_id}, **body}, matter_walls=None, matter_walled=False))
            self.assertEqual(denied["result"], "not_found", tool)
        absent = quiet(call("getTimeEntry", ["time:read"], {"path": {"time_entry_id": entry}}))
        self.assertEqual(absent["result"], "not_found")
        malformed = quiet(call("getTimeEntry", ["time:read"], {"path": {"time_entry_id": entry}}, matter_id=0, matter_walled=False))
        self.assertEqual(malformed["result"], "not_found")
        null_time = quiet(call("updateTimeEntry", ["time:write"], {"path": {"time_entry_id": entry}, "body": time_patch}, matter_id=None, matter_walled=False))
        self.assertEqual(null_time["result"], "not_found")
        body_rescue = quiet(
            call(
                "updateTimeEntry",
                ["time:write"],
                {"path": {"time_entry_id": entry}, "body": {"matter_id": matter, "description": "Review"}},
                matter_walled=False,
                matter_walls={matter: False},
            )
        )
        self.assertEqual(body_rescue["result"], "not_found")
        hidden = quiet(call("getTimeEntry", ["time:read"], {"path": {"time_entry_id": entry}}, matter_id=matter, matter_walls={matter: True}, matter_membership={matter: False}))
        self.assertEqual(hidden["result"], "not_found")
        assigned = call("getTimeEntry", ["time:read"], {"path": {"time_entry_id": entry}}, matter_id=matter, matter_walled=False)
        self.assertEqual(assigned["result"], "allowed")
        self.assertTrue(assigned["effects"])
        updated = call("updateTimeEntry", ["time:write"], {"path": {"time_entry_id": entry}, "body": time_patch}, matter_id=matter, matter_walls={matter: False})
        self.assertEqual(updated["result"], "allowed")
        self.assertTrue(updated["effects"])
        removed = call("deleteCalendarEvent", ["calendar:write"], {"path": {"calendar_event_id": event}}, matter_id=matter, matter_walled=False)
        self.assertEqual(removed["result"], "allowed")
        self.assertTrue(removed["effects"])
        unassigned_event = call("getCalendarEvent", ["calendar:read"], {"path": {"calendar_event_id": event}}, matter_id=None)
        self.assertEqual(unassigned_event["result"], "allowed")
        self.assertTrue(unassigned_event["effects"])
        unassigned_file = call("getFile", ["files:read"], {"path": {"file_id": file_id}}, matter_id=None)
        self.assertEqual(unassigned_file["result"], "allowed")
        routed_hidden = quiet(
            call(
                "updateTimeEntry",
                ["time:write"],
                {"path": {"time_entry_id": entry}, "body": time_patch},
                matter_walls=None,
                domain_handlers=handler,
            )
        )
        self.assertEqual(routed_hidden["result"], "not_found")
        routed = call(
            "updateTimeEntry",
            ["time:write"],
            {"path": {"time_entry_id": entry}, "body": time_patch},
            matter_id=matter,
            matter_walled=False,
            domain_handlers=handler,
        )
        self.assertEqual(routed["result"], "allowed")
        self.assertFalse(routed["effects"])
        self.assertEqual(routed["routed"], "time-mod")
        routed_clear = call(
            "updateCalendarEvent",
            ["calendar:write"],
            {"path": {"calendar_event_id": event}, "body": {"matter_id": None}},
            matter_id=None,
            domain_handlers=calendar,
        )
        self.assertEqual(routed_clear["result"], "allowed")
        self.assertFalse(routed_clear["effects"])
        self.assertEqual(routed_clear["routed"], "cal-mod")

        def file_row(row_id, matter_id, **flags):
            return {
                "id": row_id,
                "matter_id": matter_id,
                "matter_walled": False,
                "attorney_only": False,
                "shared_with_client": True,
                "quarantined": False,
                "contact_id": contact,
                **flags,
            }

        def client_archive(records, body=None, **trusted):
            return mcp_tool_dispatch(
                self.spec,
                {
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "role": "client",
                    "token_scopes": ["files:read"],
                    "stored_files": records,
                    "tool": "createFileArchive",
                    "arguments": {"body": body or {"file_ids": [row["id"] for row in records]}},
                    **trusted,
                },
            )

        foreign_open = quiet(client_archive([file_row(file_id, foreign)]))
        self.assertEqual(foreign_open["result"], "not_found")
        with self.assertRaises(Exception):
            _component_validator(self.spec, "FileArchiveRequest").validate({"file_ids": [file_id], "owner": "self", "shared_with_client": True})
        spoofed = quiet(client_archive([file_row(file_id, foreign)], {"file_ids": [file_id], "owner": "self", "shared_with_client": True}))
        self.assertEqual(spoofed["result"], "not_found")
        foreign_wall = quiet(client_archive([file_row(file_id, foreign, matter_walled=True)], client_matter_ids=[matter], matter_membership={foreign: False}))
        self.assertEqual(foreign_wall["result"], "not_found")
        missing_relationship = quiet(client_archive([file_row(file_id, matter)], client_matter_ids=None, client_contact_id=None))
        self.assertEqual(missing_relationship["result"], "not_found")
        mixed = quiet(client_archive([file_row(file_id, matter), file_row(other_file, foreign)], client_matter_ids=[matter]))
        self.assertEqual(mixed["result"], "not_found")
        own = client_archive([file_row(file_id, matter)], client_matter_ids=[matter])
        self.assertEqual(own["result"], "allowed")
        self.assertTrue(own["effects"])
        own_contact = client_archive([file_row(file_id, None, contact_id=contact)], client_contact_id=contact)
        self.assertEqual(own_contact["result"], "allowed")
        unshared = quiet(client_archive([file_row(file_id, matter, shared_with_client=False)], client_matter_ids=[matter], shared=True))
        self.assertEqual(unshared["result"], "not_found")
        link = mcp_tool_dispatch(
            self.spec,
            {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "client",
                "token_scopes": ["files:write"],
                "stored_files": [file_row(file_id, foreign), file_row(other_file, foreign)],
                "tool": "createFileLink",
                "arguments": {"path": {"file_id": file_id}, "body": {"to_file_id": other_file, "relationship": "attachment", "shared_with_client": True}},
                "client_matter_ids": [matter],
            },
        )
        self.assertEqual(quiet(link)["result"], "not_found")

    def test_parent_schema_and_client_file_reads(self):
        from contract.policy import _parent_matter_mode, iter_operations

        matter = "018f1a00-7b3c-7d4e-8a11-333333333333"
        foreign = "018f1a00-7b3c-7d4e-8a11-333333333339"
        contact = "018f1a00-7b3c-7d4e-8a11-222222222222"
        file_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa1"
        version_id = "018f1a00-7b3c-7d4e-8a11-aaaaaaaaaaa9"
        named = {
            "getInvoice": "required",
            "updateInvoice": "required",
            "historyInvoice": "required",
            "getInvoicePdf": "required",
            "getTrustEntry": "required",
            "historyTimeEntry": "required",
            "getTimeEntry": "required",
            "updateTimeEntry": "required",
            "stopTimer": "required",
            "pauseTimer": "required",
            "getTimer": "required",
            "getCalendarEvent": "nullable",
            "getFile": "nullable",
            "downloadFileVersion": "nullable",
            "deleteNotification": "absent",
            "markNotificationRead": "absent",
        }
        found = {}
        for _method, path, operation in iter_operations(self.spec):
            if not operation["x-togavsu-access"].get("matter_wall"):
                continue
            if not any(part.startswith("{") and part != "{matter_id}" for part in path.split("/")):
                continue
            found[operation["operationId"]] = _parent_matter_mode(self.spec, operation)
        for operation_id, mode in named.items():
            self.assertEqual(found[operation_id], mode, operation_id)
        self.assertGreater(len(found), len(named))
        self.assertNotIn("listMatter", found)
        self.assertNotIn("createTimeEntry", found)
        self.assertTrue(all(mode in {"required", "nullable", "absent"} for mode in found.values()))

        def quiet(decision):
            self.assertFalse(decision["effects"])
            self.assertEqual(decision.get("events") or [], [])
            self.assertIsNone(decision.get("routed"))
            return decision

        def staff(tool, scopes, path, **trusted):
            return mcp_tool_dispatch(
                self.spec,
                {
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "role": "staff",
                    "token_scopes": scopes,
                    "tool": tool,
                    "arguments": {"path": path, **trusted.pop("arguments", {})},
                    **trusted,
                },
            )

        samples = (
            ("getInvoice", ["invoices:read"], {"invoice_id": "018f1a00-7b3c-7d4e-8a11-111111111111"}),
            ("getTrustEntry", ["trust:read"], {"trust_entry_id": "018f1a00-7b3c-7d4e-8a11-222222222221"}),
            ("stopTimer", ["time:write"], {"timer_id": "018f1a00-7b3c-7d4e-8a11-333333333331"}),
            ("historyTimeEntry", ["time:read"], {"time_entry_id": "018f1a00-7b3c-7d4e-8a11-ccccccccccc1"}),
            ("getTimeEntry", ["time:read"], {"time_entry_id": "018f1a00-7b3c-7d4e-8a11-ccccccccccc1"}),
        )
        handler = {"time": {"replaced": True, "available": True, "module_id": "time-mod"}}
        for tool, scopes, path in samples:
            denied = quiet(staff(tool, scopes, path, matter_walls=None))
            self.assertEqual(denied["result"], "not_found", tool)
            rescued = quiet(staff(tool, scopes, path, matter_walled=False, arguments={"body": {"matter_id": matter}}))
            self.assertEqual(rescued["result"], "not_found", tool)
            allowed = staff(tool, scopes, path, matter_id=matter, matter_walled=False)
            self.assertEqual(allowed["result"], "allowed", tool)
            self.assertTrue(allowed["effects"], tool)
        routed_denied = quiet(staff("stopTimer", ["time:write"], {"timer_id": "018f1a00-7b3c-7d4e-8a11-333333333331"}, domain_handlers=handler))
        self.assertEqual(routed_denied["result"], "not_found")
        routed = staff("stopTimer", ["time:write"], {"timer_id": "018f1a00-7b3c-7d4e-8a11-333333333331"}, matter_id=matter, matter_walls={matter: False}, domain_handlers=handler)
        self.assertEqual(routed["result"], "allowed")
        self.assertFalse(routed["effects"])
        self.assertEqual(routed["routed"], "time-mod")
        unassigned = staff("getFile", ["files:read"], {"file_id": file_id}, matter_id=None)
        self.assertEqual(unassigned["result"], "allowed")
        listed = mcp_tool_dispatch(
            self.spec,
            {"authorization": "Bearer secret", "authorization_scheme": "Bearer", "credential_valid": True, "role": "staff", "token_scopes": ["invoices:read"], "tool": "listInvoice", "arguments": {}},
        )
        self.assertEqual(listed["result"], "allowed")

        def file_row(matter_id, **flags):
            return {
                "id": file_id,
                "matter_id": matter_id,
                "matter_walled": False,
                "attorney_only": False,
                "shared_with_client": True,
                "quarantined": False,
                "contact_id": contact,
                "version_id": version_id,
                **flags,
            }

        def client_read(tool, row, path, **trusted):
            return mcp_tool_dispatch(
                self.spec,
                {
                    "authorization": "Bearer secret",
                    "authorization_scheme": "Bearer",
                    "credential_valid": True,
                    "role": "client",
                    "token_scopes": ["files:read"],
                    "shared": True,
                    "owner": "self",
                    "stored_file": row,
                    "client_matter_ids": [matter],
                    "client_contact_id": contact,
                    "tool": tool,
                    "arguments": {"path": path},
                    **trusted,
                },
            )

        foreign_read = quiet(client_read("getFile", file_row(foreign), {"file_id": file_id}))
        self.assertEqual(foreign_read["result"], "not_found")
        foreign_download = quiet(client_read("downloadFileVersion", file_row(foreign), {"file_id": file_id, "version_id": version_id}, confirm=True))
        self.assertEqual(foreign_download["result"], "not_found")
        own_read = client_read("getFile", file_row(matter), {"file_id": file_id})
        self.assertEqual(own_read["result"], "allowed")
        self.assertTrue(own_read["effects"])
        own_download = client_read("downloadFileVersion", file_row(matter), {"file_id": file_id, "version_id": version_id}, confirm=True, quarantined=False)
        self.assertEqual(own_download["result"], "allowed")
        contact_only = client_read("getFile", file_row(None), {"file_id": file_id}, client_matter_ids=[])
        self.assertEqual(contact_only["result"], "allowed")
        contact_on_foreign = quiet(client_read("downloadFileVersion", file_row(foreign), {"file_id": file_id, "version_id": version_id}, confirm=True))
        self.assertEqual(contact_on_foreign["result"], "not_found")
        staff_read = mcp_tool_dispatch(
            self.spec,
            {
                "authorization": "Bearer secret",
                "authorization_scheme": "Bearer",
                "credential_valid": True,
                "role": "staff",
                "token_scopes": ["files:read"],
                "stored_file": file_row(foreign),
                "matter_walled": False,
                "tool": "getFile",
                "arguments": {"path": {"file_id": file_id}},
            },
        )
        self.assertEqual(staff_read["result"], "allowed")


def _operation(spec: dict, operation_id: str) -> dict:
    for _method, _path, operation in _ops(spec):
        if operation["operationId"] == operation_id:
            return operation
    raise AssertionError(operation_id)


def _schema_name(node: dict) -> str:
    if "requestBody" in node:
        return node["requestBody"]["content"]["application/json"]["schema"]["$ref"].rsplit("/", 1)[1]
    return node["content"]["application/json"]["schema"]["$ref"].rsplit("/", 1)[1]


def _schema_name_from_page(operation: dict) -> str:
    return operation["responses"]["200"]["content"]["application/json"]["schema"]["properties"]["data"]["items"]["$ref"].rsplit("/", 1)[1]


def _as_trusted_grant_fixture(call: dict, token: str = "fixture-task") -> dict:
    """Move a fixture claim object into trusted verifier context. A caller dict is not a grant."""
    arguments = dict(call.get("arguments") or {})
    task = arguments.get("task_authorization")
    if not isinstance(task, dict):
        return call
    arguments["task_authorization"] = token
    return {**call, "arguments": arguments, "task_verifier": {"valid": True, "credential": token, "claims": task}}


def _session_csrf(call: dict, token: str = "csrf-1") -> dict:
    """Existing cookie scenarios present the HTTP header and a verified token. Bearer calls are unchanged."""
    if not call.get("cookie") or call.get("authorization"):
        return call
    headers = dict(call.get("transport_headers") or {})
    headers.setdefault("X-CSRF-Token", token)
    return {**call, "transport_headers": headers, "csrf_verifier": call.get("csrf_verifier") or {"valid": True, "token": token}}


def _issue_malware_grant(policy: dict, module: dict) -> dict | None:
    if not module.get("registered") or not policy.get("registered_module") or not policy.get("subscription_match"):
        return None
    if policy["event_type"] not in module.get("subscriptions", []):
        return None
    if module.get("module_id") != module.get("recipient_id"):
        return None
    return {
        "module_id": module["module_id"],
        "file_id": module["file_id"],
        "version_id": module["version_id"],
        "matter_id": module["matter_id"],
        "allowed_operations": list(policy["sequence"]),
        "allowed_fields": list(policy["metadata_fields"]),
        "issued_by": policy["issued_by"],
        "expired": False,
        "revoked": False,
        "invalid": False,
    }


def _malware_call(policy: dict, operation: dict, call: dict) -> str:
    return malware_scan_decision(policy, operation, call)


def _case_matches(case: dict, call: dict) -> bool:
    for key, value in case.items():
        if key == "result":
            continue
        if call.get(key) != value:
            return False
    return True


def operation_denies_other_client(operation: dict) -> bool:
    for case in operation["x-togavsu-access"].get("access_cases", []):
        other = case.get("owner") == "other_client" or case.get("matter") == "other_client" or case.get("from_visible") is False
        if other and case.get("result") == "not_found":
            return True
    return False


def _upload_step_result(spec: dict, policy: dict, step: dict, call: dict) -> str:
    operation = _operation(spec, step["operation"])
    access = operation["x-togavsu-access"]
    if call.get("user_id") or call.get("requested_roles") or call.get("impersonate"):
        return "forbidden"
    if step["step"] == "obtain_context":
        return _client_context_result({**call, "role": "client"})
    if step["operation"] not in policy["client_allowed_operations"] or "client" not in access["roles"]:
        return "forbidden"
    for case in access.get("access_cases", []):
        if _case_matches(case, {**call, "role": "client"}):
            return case["result"]
    if call.get("matter") not in (None, "own_shared"):
        return "not_found"
    if call.get("owner") == "other_client":
        return "not_found"
    return "allowed"


def _file_byte_downloads(spec: dict):
    for method, path, operation in _ops(spec):
        if is_core_file_download(operation):
            yield method, path, operation


def _client_portal_result(spec: dict, policy: dict, operation_id: str, call: dict) -> str:
    operation = _operation(spec, operation_id)
    access = operation["x-togavsu-access"]
    if operation_id not in policy["client_allowed_operations"] or "client" not in access.get("roles", []):
        return "forbidden"
    if call.get("module_scopes") is not None and any(scope not in call["module_scopes"] for scope in access.get("scopes", [])):
        return "scope_denied"
    if call.get("attorney_only") and access.get("attorney_only_files") == "not_found":
        return "not_found"
    if access.get("matter_wall") and call.get("matter_walled") and not call.get("on_wall"):
        return "not_found"
    client = access.get("client")
    if client == "shared":
        if call.get("matter") in ("other_client", "own_unshared") or call.get("owner") == "other_client":
            return "not_found"
        if access.get("access_inherits") == "file" and call.get("shared") is False:
            return "not_found"
    if client == "own" and call.get("owner") not in (None, "self"):
        return "not_found"
    return "allowed"


def _schema_validator(spec: dict, schema: dict):
    import copy

    from jsonschema import Draft202012Validator

    schemas = copy.deepcopy(spec["components"]["schemas"])
    node = copy.deepcopy(schema)

    def rewrite(value):
        if isinstance(value, dict):
            ref = value.get("$ref")
            if isinstance(ref, str) and ref.startswith("#/components/schemas/"):
                value["$ref"] = "#/$defs/" + ref.rsplit("/", 1)[1]
            for child in value.values():
                rewrite(child)
        elif isinstance(value, list):
            for child in value:
                rewrite(child)

    rewrite(schemas)
    rewrite(node)
    return Draft202012Validator({"$defs": schemas, **node})


def _component_validator(spec: dict, schema_name: str):
    import copy

    from jsonschema import Draft202012Validator

    schemas = copy.deepcopy(spec["components"]["schemas"])

    def rewrite(node):
        if isinstance(node, dict):
            ref = node.get("$ref")
            if isinstance(ref, str) and ref.startswith("#/components/schemas/"):
                node["$ref"] = "#/$defs/" + ref.rsplit("/", 1)[1]
            for value in node.values():
                rewrite(value)
        elif isinstance(node, list):
            for value in node:
                rewrite(value)

    rewrite(schemas)
    return Draft202012Validator({"$defs": schemas, "$ref": "#/$defs/" + schema_name})


def _download_outcome(operation: dict, call: dict) -> str:
    access = operation["x-togavsu-access"]
    if "file.downloaded" not in operation.get("x-togavsu-events", []):
        return "missing_event"
    if access.get("event") != "file.downloaded" or not access.get("audit") or not access.get("timeline"):
        return "missing_event"
    if access.get("client_download") != "highlighted" or access.get("quarantine_confirmation") != "required":
        return "missing_highlight"
    if "quarantine_confirmation_required" not in operation.get("x-togavsu-error-codes", []):
        return "missing_highlight"
    if call.get("attorney_only") and call["role"] in ("staff", "client", "accountant"):
        return "not_found"
    if call.get("matter_walled") and not call.get("on_wall"):
        return "not_found"
    if call["role"] == "client" and not call.get("shared"):
        return "not_found"
    if access.get("attorney_only_files") != "not_found" or not access.get("matter_wall"):
        return "missing_wall"
    return "highlighted" if call["role"] == "client" else "audited"


def _background_result(case: dict) -> str:
    if case.get("token_revoked"):
        return "unauthenticated"
    if case["operation_scope"] not in case["module_scopes"]:
        return "scope_denied"
    capability = case.get("capability")
    if not capability or capability.get("expired") or capability.get("revoked"):
        return "forbidden"
    if capability.get("resource") == "unrelated" or capability.get("matter") == "unrelated":
        return "not_found"
    if capability.get("attorney_only") and not capability.get("attorney_only_grant"):
        return "not_found"
    if case.get("field") not in capability.get("allowed_fields", []):
        return "field_locked"
    return "allowed"


def _client_context_result(case: dict) -> str:
    if case.get("requested_roles") or case.get("impersonate"):
        return "forbidden"
    if case["role"] == "client" and case.get("matter") != "own_shared":
        return "not_found"
    return "allowed"


def _trust_poster(trust_replaced: bool, dispatch: dict) -> str:
    entry = dispatch["trust_entry"]
    if entry["time_module_also_posts"]:
        return "ambiguous"
    if not entry["exactly_once"] or not entry["append_only"] or entry["posted_by"] != "trust_handler":
        return "ambiguous"
    return "trust_module" if trust_replaced else "core"


def _delegation_result(case: dict) -> str:
    scopes = case["module_scopes"]
    needed = case["operation_scope"]
    if needed not in scopes:
        return "scope_denied"
    if case.get("operation_roles") and case["viewer_role"] not in case["operation_roles"]:
        return "forbidden"
    if case.get("attorney_only") and case["viewer_role"] == "client":
        return "not_found"
    if case.get("matter_walled") and not case.get("on_wall"):
        return "not_found"
    return "allowed"


def _adjustment_body(amount: int, direction: str) -> dict:
    return {
        "trust_account_id": "018f1a00-7b3c-7d4e-8a11-111111111111",
        "client_id": "018f1a00-7b3c-7d4e-8a11-222222222222",
        "matter_id": "018f1a00-7b3c-7d4e-8a11-333333333333",
        "entry_type": "adjustment",
        "amount_cents": amount,
        "adjustment_direction": direction,
        "method": "check",
        "description": "Paper adjustment",
    }


def _adjustment_row(body: dict) -> dict:
    return {
        "id": "018f1a00-7b3c-7d4e-8a11-444444444444",
        "created_at": "2026-11-07T12:00:00Z",
        "created_by": "018f1a00-7b3c-7d4e-8a11-555555555555",
        **body,
    }


def _scope_decision(entry_type_scopes: dict, token_scopes: list[str], entry_type: str) -> str:
    needed = entry_type_scopes[entry_type]
    if all(scope in token_scopes for scope in needed):
        return "allowed"
    return "scope_denied"


def _response_record_schemas(operation: dict) -> set[str]:
    found: set[str] = set()

    def walk(node):
        if isinstance(node, dict):
            ref = node.get("$ref")
            if isinstance(ref, str) and ref.startswith("#/components/schemas/"):
                found.add(ref.rsplit("/", 1)[1])
            for value in node.values():
                walk(value)
        elif isinstance(node, list):
            for value in node:
                walk(value)

    content = operation.get("responses", {}).get("200", {}).get("content", {}).get("application/json", {})
    walk(content.get("schema") or {})
    return found


def _param_name(parameter: dict) -> str:
    if "$ref" in parameter:
        return parameter["$ref"].rsplit("/", 1)[1]
    return parameter["name"]


if __name__ == "__main__":
    unittest.main()

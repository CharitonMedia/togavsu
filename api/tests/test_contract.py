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
    custom_fields_outcome,
    event_read_scopes,
    file_download_decision,
    is_core_file_download,
    confirm_totp_reset,
    bulk_invoice_apply,
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
        opened = mcp_tool_dispatch(self.spec, {"tool": "listMatter", "arguments": {}, "authorization": "Bearer secret", "authorization_scheme": "Bearer", "credential_valid": True})
        self.assertEqual(opened["result"], "allowed")
        self.assertTrue(opened["effects"])
        self.assertFalse(opened["public"])
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
                self.assertEqual(schema.get("maximum"), 200, path)
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
        started = start_totp_reset(reset, {"role": "staff", "owner": "self", "caller_user_id": "user-1", "user_id": "user-1", "issued_token": "reset-1", "expires_at": "2026-11-07T18:10:00Z"})
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
            http_call = {
                "registered": True,
                "subscriptions": ["file.version_created"],
                "caller_module_id": overrides.get("caller_module_id", "malware"),
                "module_scopes": ["files:read", "files:write"],
                "grant": task,
                "matter_id": "matter-1",
                "file_id": "file-1",
                "version_id": "ver-1",
                "viewer_present": "context_token" in arguments and task is None,
                "operation": "getFile",
                "field": "name",
            }
            return _malware_call(malware, operation, http_call)

        allowed_args = {"context_token": "ctx", "task_authorization": grant, "path": {"file_id": "file-1"}, "query": {}}
        self.assertEqual(mcp_get(allowed_args), "allowed")
        self.assertEqual(mcp_get({"context_token": "ctx", "path": {}, "query": {}}), "forbidden")
        self.assertEqual(mcp_get({"task_authorization": {**grant, "expired": True}}), "forbidden")
        self.assertEqual(mcp_get({"task_authorization": grant}, caller_module_id="other-module"), "forbidden")
        self.assertEqual(_malware_call(malware, operation, {"registered": True, "subscriptions": ["file.version_created"], "caller_module_id": "malware", "module_scopes": ["files:read", "files:write"], "grant": grant, "matter_id": "matter-1", "file_id": "file-1", "version_id": "ver-1", "operation": "getFile", "field": "name"}), mcp_get(allowed_args))
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

        def row(entry, amount, matter_id=matter, opening=None):
            item = {"amount_cents": amount, "matter_id": matter_id, "client_id": client, "trust_account_id": account, "trust_entry_id": entry}
            if opening is not None:
                item["opening"] = opening
            return item

        opening = {"matter_cents": 100, "client_cents": 100, "account_cents": 500}
        post = _operation(self.spec, "postTrustEntry")
        bulk = _operation(self.spec, "bulkTrustEntry")
        reverse = _operation(self.spec, "reverseTrustEntry")
        safe = trust_ledger_events(post, [row(entry_a, 25, opening={"matter_cents": 0, "client_cents": 0, "account_cents": 0})], committed=True)
        self.assertEqual([event["type"] for event in safe], ["trust.entry_posted"])
        flagged = trust_ledger_events(post, [row(entry_a, -25, opening={"matter_cents": 0, "client_cents": 0, "account_cents": 0})], committed=True)
        self.assertEqual([event["type"] for event in flagged], ["trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(flagged[1]["balance_cents"], -25)
        self.assertEqual(flagged[1]["balance_scope"], "matter")
        for label, kwargs in (("rollback", {"committed": False}), ("replay", {"committed": True, "replay": True}), ("denied", {"committed": True, "denied": True})):
            self.assertEqual(trust_ledger_events(post, [row(entry_a, -25, opening=opening)], **kwargs), [], label)
            self.assertEqual(trust_ledger_events(bulk, [row(entry_a, -25, opening=opening)], **kwargs), [], label)
            self.assertEqual(trust_ledger_events(reverse, [row(entry_a, -25, opening=opening)], **kwargs), [], label)
        ordered = trust_ledger_events(
            bulk,
            [row(entry_a, -40, opening=opening), row(entry_b, -80), row(entry_c, 5)],
            committed=True,
        )
        self.assertEqual([event["type"] for event in ordered], ["trust.entry_posted", "trust.entry_posted", "trust.flag_raised", "trust.entry_posted", "trust.flag_raised"])
        self.assertEqual([event["balance_cents"] for event in ordered if event["type"] == "trust.flag_raised"], [-20, -15])
        separate = trust_ledger_events(
            bulk,
            [
                row(entry_a, -5, opening={"matter_cents": 10, "client_cents": 10, "account_cents": 10}),
                row(entry_b, -5, matter_id=other_matter, opening={"matter_cents": 100, "client_cents": 0, "account_cents": 0}),
            ],
            committed=True,
        )
        self.assertEqual([event["type"] for event in separate], ["trust.entry_posted", "trust.entry_posted"])
        deposit_reversal = trust_ledger_events(reverse, [row(entry_a, -250, opening={"matter_cents": 200, "client_cents": 200, "account_cents": 200})], committed=True)
        self.assertEqual([event["type"] for event in deposit_reversal], ["trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(deposit_reversal[1]["balance_cents"], -50)
        client_only = trust_ledger_events(
            post,
            [row(entry_a, -10, opening={"matter_cents": 50, "client_cents": 5, "account_cents": 80})],
            committed=True,
        )
        self.assertEqual(client_only[1]["balance_scope"], "client")
        self.assertEqual(client_only[1]["balance_cents"], -5)
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
        self.assertEqual([event["type"] for event in posted["events"]], ["trust.entry_posted", "trust.flag_raised"])
        self.assertEqual(posted["events"][1]["balance_cents"], -60)
        replayed = processor_apply(store, policy, create, call)
        self.assertEqual(replayed["result"], "original_result")
        self.assertEqual(replayed["events"], [])
        self.assertEqual(len(store["ledger"]), 1)
        denied = processor_apply(store, policy, create, {**call, "destination": "operating", "grant": {**issued["grant"], "destination": "operating"}, "module_scopes": ["trust:read"]})
        self.assertNotEqual(denied["result"], "allowed")
        self.assertEqual(denied["events"], [])
        self.assertEqual(len(store["ledger"]), 1)
        flag = posted["events"][1]
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
    if call.get("token_revoked"):
        return policy["revoked_module_token"]
    if not call.get("registered") or policy["event_type"] not in call.get("subscriptions", []):
        return policy["invalid"]
    grant = call.get("grant")
    if not grant or grant.get("invalid"):
        return policy["invalid"]
    if grant.get("expired"):
        return policy["expired"]
    if grant.get("revoked"):
        return policy["revoked"]
    if grant.get("issued_by") != policy["issued_by"] or grant.get("module_id") != call.get("caller_module_id"):
        return policy["module_identity_mismatch"]
    if call["operation"] not in policy["sequence"] or call["operation"] not in grant.get("allowed_operations", []):
        return policy["operation_outside_grant"]
    needed = operation["x-togavsu-access"]["scopes"]
    if any(scope not in call["module_scopes"] for scope in needed):
        return policy["missing_scope"]
    if call.get("matter_id") != grant.get("matter_id"):
        return policy["unrelated_matter"]
    if call.get("file_id") != grant.get("file_id"):
        return policy["unrelated_file"]
    if call["operation"] != "getFile" and call.get("version_id") != grant.get("version_id"):
        return policy["unrelated_version"]
    field = call.get("field")
    if field in policy["attorney_only_fields"]:
        return policy["attorney_only_field"]
    if field and (field not in grant.get("allowed_fields", []) or field not in policy["metadata_fields"]):
        return policy["field_outside_grant"]
    return "allowed"


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

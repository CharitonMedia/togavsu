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
    commit_concurrent_processor_creates,
    custom_fields_batch,
    custom_fields_outcome,
    file_download_decision,
    is_core_file_download,
    mcp_credential_rejection,
    payment_effect_decision,
    payment_reversal_decision,
    processor_apply,
    processor_callback,
    processor_issuance,
    published_version_events,
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
        for path in ("/api/v1/help/guide", "/help/guide"):
            access = self.spec["paths"][path]["get"]["x-togavsu-access"]
            self.assertEqual(access["client"], "not_found", path)

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
        self.assertNotEqual(reset["security"], [])

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
            "permissions": ["matters:read"],
            "widgets": [
                {
                    "id": "hours",
                    "title": "Hours this month",
                    "default_size": {"w": 3, "h": 2},
                    "allowed_sizes": [{"w": 3, "h": 2}, {"w": 4, "h": 2}],
                    "target": {"path": "/m/matter-clock/hours", "filters": {"month": "current"}},
                }
            ],
            "sidebar": [{"section": "Activity", "label": "Matter Clock", "icon": "clock", "order": 10}],
            "quick_create": [{"label": "Set target", "target": "/m/matter-clock/target"}],
            "search_scopes": [{"id": "matter-clock", "label": "Targets"}],
            "matter_screen": {
                "panels": [{"id": "target", "title": "Monthly target"}],
                "actions": [{"id": "edit-target", "label": "Edit target"}],
            },
        }
        validator.validate(manifest)
        widget = self.spec["components"]["schemas"]["ManifestWidget"]
        self.assertIn("allowed_sizes", widget["required"])
        for field in ("sidebar", "quick_create", "search_scopes", "matter_screen"):
            self.assertIn(field, self.spec["components"]["schemas"]["ModuleManifest"]["properties"])

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
            "files": "bulkFile",
            "events": "bulkEvent",
            "custom-field-values": "bulkCustomFieldValue",
        }
        for record_type, operation_id in required.items():
            operation = _operation(self.spec, operation_id)
            self.assertIn("bulk", operation["x-togavsu-features"], record_type)
            self.assertEqual(operation["x-togavsu-mcp"]["parity"], "required", record_type)
            self.assertTrue(operation["x-togavsu-mcp"]["tool"], record_type)

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
        for operation_id in ("listInvoice", "getInvoice", "historyInvoice"):
            operation = _operation(self.spec, operation_id)
            self.assertIn("client", operation["x-togavsu-access"]["roles"])
            self.assertIn("internal_notes", operation["x-togavsu-access"]["client_omitted_fields"])
        self.assertEqual(_operation(self.spec, "listInvoice")["x-togavsu-access"]["client_schema"], "InvoiceClient")
        self.assertEqual(_operation(self.spec, "historyInvoice")["x-togavsu-access"]["client_schema"], "InvoiceClientHistoryEntry")
        self.assertEqual(_operation(self.spec, "listInvoice")["x-togavsu-access"]["client_csv_omitted_columns"], ["internal_notes"])

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
            "publishFile",
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
        cases = [
            ({"role": "attorney", "token_scopes": ["payments:write"], "trust_linked": False, "matter_walled": False}, "allowed", False, False),
            ({"role": "attorney", "token_scopes": ["payments:write", "trust:withdraw"], "trust_linked": True, "matter_walled": False, "below_zero": True, **trust_ok}, "allowed", True, True),
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
            {"role": "attorney", "token_scopes": ["payments:write", "trust:withdraw"], "trust_linked": True, "matter_walled": False, "below_zero": True, **trust_ok},
        )
        self.assertTrue(trust["ledger_append_only"])
        self.assertFalse(trust["original_trust_edited"])
        self.assertEqual(trust["trust_posted_by"], "trust_handler")
        self.assertEqual(trust["trust_account_id"], account)
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
        self.assertEqual(callback("createPayment", {**base, "operation": "createPayment", "idempotency_key": "pay-1"}), "original_result")
        self.assertEqual(len(store["payments"]), 1)
        self.assertEqual(
            callback("createPayment", {**base, "operation": "createPayment", "idempotency_key": "pay-1", "status": "completed"}),
            "idempotency_conflict",
        )
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
                {**trust_base, "operation": "updatePaymentStatus", "payment_id": trust_payment, "status": "completed"},
            ),
            "allowed",
        )
        self.assertEqual(len(store["ledger"]), 1)
        self.assertEqual(store["ledger"][0]["trust_account_id"], trust_account)
        self.assertEqual(
            callback(
                "updatePaymentStatus",
                {**trust_base, "operation": "updatePaymentStatus", "payment_id": trust_payment, "status": "completed"},
            ),
            "original_result",
        )
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
            self.assertFalse(again["payment_inserted"], label)
            self.assertFalse(again["ledger_inserted"], label)
            self.assertEqual(len(store["payments"]), 1, label)
            self.assertEqual(len(store["ledger"]), 1, label)
        changed = processor_apply(store, policy, create, {**call, "idempotency_key": "fresh", "reference_note": "x", "status": "pending"})
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
        self.assertEqual(invoice["x-togavsu-access"]["client_omitted_fields"], ["internal_notes"])
        history = _operation(self.spec, "historyInvoice")
        self.assertIn("internal_notes", history["x-togavsu-access"]["client_omitted_fields"])
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
        page_validator.validate({"data": [version_body], "page": {"limit": 50, "next_cursor": None}})
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
            "listFileVersions": ("FileVersionGrantPage", {"data": [version_body], "page": {"limit": 50, "next_cursor": None}}),
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

    def test_marketplace_flag_and_pull(self):
        paths = self.market["paths"]
        self.assertIn("/v1/operator/listings/{listing_id}/flag", paths)
        self.assertIn("/v1/operator/listings/{listing_id}/pull", paths)
        self.assertIn("/v1/alerts", paths)
        self.assertEqual(self.market["servers"][0]["url"], "https://marketplace.example.invalid")


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


def _param_name(parameter: dict) -> str:
    if "$ref" in parameter:
        return parameter["$ref"].rsplit("/", 1)[1]
    return parameter["name"]


if __name__ == "__main__":
    unittest.main()

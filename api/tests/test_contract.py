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
        self.assertIn("field_locked", operation["x-togavsu-error-codes"])
        self.assertIn("file.updated", operation["x-togavsu-events"])
        self.assertNotIn("file.version_created", operation["x-togavsu-events"])
        fields = self.spec["components"]["schemas"]["FileMetadataWrite"]["properties"]
        for field in ("attorney_only", "shared_with_client", "category_id", "matter_id"):
            self.assertIn(field, fields)
        self.assertIn("field_locked", _operation(self.spec, "renameFile")["x-togavsu-error-codes"])

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
        self.assertIn("page", self.spec["components"]["schemas"]["Citation"]["properties"])
        self.assertIn("timestamp", self.spec["components"]["schemas"]["Citation"]["properties"])
        operation = _operation(self.spec, "getDocTenderResult")
        self.assertTrue(operation["x-togavsu-access"]["matter_wall"])
        self.assertEqual(operation["x-togavsu-access"]["access_inherits"], "file")
        self.assertIn("doc_tender:read", operation["x-togavsu-access"]["scopes"])
        self.assertEqual(_schema_name(operation["responses"]["200"]), "DocTenderResult")

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


def _schema_name(response: dict) -> str:
    return response["content"]["application/json"]["schema"]["$ref"].rsplit("/", 1)[1]


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

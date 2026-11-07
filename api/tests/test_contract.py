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
        self.assertEqual(matter["arguments"], ["path", "query"])
        self.assertNotIn("matter_id", matter["arguments"])
        chunk = _operation(self.spec, "putFileChunk")["x-togavsu-mcp"]["transfer"]["arguments"]
        self.assertEqual(chunk[:2], ["path", "query"])
        self.assertIn("headers", chunk)
        self.assertIn("body_base64", chunk)
        self.assertNotIn("upload_id", chunk)
        text = Path("docs/api/mcp.md").read_text(encoding="utf-8")
        blocks = [json.loads(part.split("```", 1)[0]) for part in text.split("```json\n")[1:]]
        get_args = blocks[0]["params"]["arguments"]
        put_args = blocks[1]["params"]["arguments"]
        self.assertEqual(set(get_args), {"path", "query"})
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
            "createAiCall",
            "getMatter",
            "listMatter",
            "updateFileMetadata",
            "publishFile",
            "downloadFileVersion",
            "createNote",
            "createTimeEntry",
            "createEvent",
            "runEditor",
            "postTrustEntry",
        ):
            self.assertEqual(
                _operation(self.spec, operation_id)["x-togavsu-access"]["module_delegation"],
                "delegated_viewer",
                operation_id,
            )
        for operation_id in ("updateSetting", "listAuditLog", "createUser", "listApiToken", "updateMe", "requestEmailChange"):
            self.assertNotEqual(
                _operation(self.spec, operation_id)["x-togavsu-access"].get("module_delegation"),
                "delegated_viewer",
                operation_id,
            )
        for case in policy["cases"]:
            self.assertEqual(_delegation_result(case), case["result"], case)

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

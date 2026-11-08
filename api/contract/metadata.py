"""Declarative metadata for the OpenAPI builder.

Constants and pure derivations that publish security, ownership, grants, events,
projections, trust, pagination, and MCP parity onto the contract. This module does
not decide a call. The call simulator stays in contract.policy, which re-exports
these names for the existing tests.

Stage 1 baseline: b4350f8af3803b8dc8439b2b2e7a431f198583d9
"""

from __future__ import annotations

# The path parameter {module_id} uses this same pattern. Callers do not get a second, looser rule.
MODULE_ID_PATTERN = "^[a-z0-9][a-z0-9-]{0,63}$"

_FLAG_METADATA = ("matter_id", "client_id", "trust_account_id", "balance_cents", "trust_entry_id", "balance_scope")
_TRUST_FLAG = {
    "selection": "resulting_balance",
    "posted": "trust.entry_posted",
    "flag": "trust.flag_raised",
    "when": "balance_below_zero",
    "blocks": False,
    "below_zero": "posts",
    "warning": True,
    "rollback": "no_event",
    "denied": "no_event",
    "replay": "no_event",
    "bulk": "chronological_per_item",
    "webhook_scope": "trust:read",
    "metadata": list(_FLAG_METADATA),
}

FILE_DOWNLOAD = {
    "audit": True,
    "timeline": True,
    "client_download": "highlighted",
    "event": "file.downloaded",
    "quarantine_confirmation": "required",
}

_FILE_BYTE_TYPES = {"application/octet-stream", "application/zip", "application/pdf", "text/markdown"}
_EXCLUDED_DOWNLOAD_FEATURES = {"backups", "help-guide", "llms", "health", "setup", "auth"}
_CORE_FILE_FEATURES = {"files", "file-versions", "file-renditions", "invoices", "expenses", "quarantine"}

MALWARE_SEQUENCE = ["getFile", "listFileVersions", "downloadFileVersion", "quarantineFileVersion"]
FILE_GRANT_FIELDS = ["name", "extension"]
VERSION_GRANT_FIELDS = ["media_type", "size_bytes", "sha256", "quarantined"]
PRIVATE_GRANT_FIELDS = {"origin", "created_by", "storage_backend_id", "saved_by"}

MALWARE_DELIVERY = {
    "event_type": "file.version_created",
    "delivered_on": "moduleEvent",
    "header": "X-Togavsu-Task-Authorization",
    "body_field": "task_authorization",
    "issued_by": "core",
    "registered_module": True,
    "subscription_match": True,
    "viewer_context_required": False,
    "module_identity_mismatch": "forbidden",
    "invalid": "forbidden",
    "expired": "forbidden",
    "revoked": "forbidden",
    "revoked_module_token": "unauthenticated",
    "missing_scope": "scope_denied",
    "missing_grant": "forbidden",
    "unrelated_matter": "not_found",
    "unrelated_file": "not_found",
    "unrelated_version": "not_found",
    "operation_outside_grant": "forbidden",
    "field_outside_grant": "field_locked",
    "attorney_only_field": "not_found",
    "grants_admin": False,
    "firmwide_bypass": False,
    "sequence": list(MALWARE_SEQUENCE),
    "metadata_fields": ["name", "extension", "media_type", "size_bytes", "sha256", "quarantined"],
    "response_fields": {
        "getFile": list(FILE_GRANT_FIELDS),
        "listFileVersions": list(VERSION_GRANT_FIELDS),
        "quarantineFileVersion": list(VERSION_GRANT_FIELDS),
    },
    "attorney_only_fields": ["note", "summary"],
}

PAYMENT_PROCESSOR = {
    "event_type": "payment.processor_authorized",
    "delivered_on": "moduleEvent",
    "header": "X-Togavsu-Task-Authorization",
    "body_field": "processor_authorization",
    "issued_by": "core",
    "registered_module": True,
    "initialization_requires_viewer": True,
    "viewer_context_required": False,
    "callback_requires_viewer": False,
    "module_identity_mismatch": "forbidden",
    "invalid": "forbidden",
    "unregistered": "forbidden",
    "expired": "forbidden",
    "revoked": "forbidden",
    "revoked_module_token": "unauthenticated",
    "missing_scope": "scope_denied",
    "missing_grant": "forbidden",
    "unrelated_matter": "not_found",
    "unrelated_payment": "not_found",
    "unrelated_resource": "not_found",
    "amount_mismatch": "forbidden",
    "destination_mismatch": "forbidden",
    "operation_outside_grant": "forbidden",
    "field_outside_grant": "field_locked",
    "grants_admin": False,
    "firmwide_bypass": False,
    "ambient_payment_write": False,
    "sequence": ["createPayment", "updatePaymentStatus"],
    "allowed_fields": ["status", "method", "reference_number"],
    "binds": [
        "module_id",
        "initialization_id",
        "payment_id",
        "matter_id",
        "contact_id",
        "invoice_id",
        "amount_cents",
        "destination",
        "trust_account_id",
    ],
    "binding": {
        "cardinality": "one_payment_per_initialization",
        "reserved_at": "authorization",
        "idempotency_independent": True,
        "repeat_same_body": "original_result",
        "repeat_different_body": "conflict",
        "storage_constraint": (
            "unique payments.initialization_id, payments.id equals the reserved payment id, "
            "and the payment row plus any trust row commit in one transaction"
        ),
    },
    "caller_scopes": {
        "operating": ["payments:write"],
        "trust": ["payments:write", "trust:write"],
    },
    "module_scopes": {
        "operating": ["payments:write"],
        "trust": ["payments:write", "trust:write"],
    },
    "idempotency_replay": "original_result",
    "idempotency_conflict": "idempotency_conflict",
    "no_mutation_on_denial": True,
}
PAYMENT_STATUS_GRAPH = {
    "initial": "pending",
    "transitions": {
        "pending": ["completed", "failed"],
        "completed": [],
        "failed": [],
        "reversed": [],
    },
    "same_status": "original_result",
    "illegal_transition": "conflict",
    "settled_correction": "reversePayment",
    "settled_correction_excludes": "failed",
    "settled_trust_scope": "trust:withdraw",
    "reversed_terminal": True,
    "no_mutation_on_denial": True,
}

MODULE_GUIDE = {
    "owner_namespace": "module_id",
    "heading_default": "module_name",
    "staff_sign_in": True,
    "client": "not_found",
    "markdown": "sanitize",
    "sanitize_rejects": ["script", "javascript:", "data:", "event_handler", "iframe"],
    "on_update": "replace",
    "on_disable": "hide",
    "on_uninstall": "remove",
    "own_heading_only": True,
    "runtime_registration": "pending",
}
# Section 9.9 records a module may extend by id. The manifest enum and include
# reads both come from this list. Wrappers, credentials, admin catalogs, storage
# infrastructure, and append-only streams are not records in this catalog.
# file_version is listed by listFileVersions; it has no separate get.
CORE_EXTENSION_TARGETS = (
    ("user", "User", "users:read", ("listUser", "getUser")),
    ("contact", "Contact", "contacts:read", ("listContact", "getContact")),
    ("matter", "Matter", "matters:read", ("listMatter", "getMatter")),
    ("matter_party", "MatterParty", "matters:read", ("listMatterParty", "getMatterParty")),
    ("note", "Note", "notes:read", ("listNote", "getNote")),
    ("fee_agreement", "FeeAgreement", "matters:read", ("listFeeAgreement", "getFeeAgreement")),
    ("time_entry", "TimeEntry", "time:read", ("listTimeEntry", "getTimeEntry")),
    ("expense", "Expense", "expenses:read", ("listExpense", "getExpense")),
    ("invoice", "Invoice", "invoices:read", ("listInvoice", "getInvoice")),
    ("payment", "Payment", "payments:read", ("listPayment", "getPayment")),
    ("calendar_event", "CalendarEvent", "calendar:read", ("listCalendarEvent", "getCalendarEvent")),
    ("deadline", "Deadline", "deadlines:read", ("listDeadline", "getDeadline")),
    ("file", "File", "files:read", ("listFile", "getFile")),
    ("file_version", "FileVersion", "files:read", ("listFileVersions",)),
    ("conflict_record", "ConflictRecord", "conflicts:read", ("listConflictRecord", "getConflictRecord")),
    ("trust_account", "TrustAccount", "trust:read", ("listTrustAccount", "getTrustAccount")),
    ("trust_entry", "TrustEntry", "trust:read", ("listTrustEntry", "getTrustEntry")),
)

CORE_PAGE_TARGETS = {
    "time_entries": {"operation": "listTimeEntry", "scopes": ["time:read"], "roles": ["admin", "attorney", "staff", "accountant"], "matter_wall": True},
    "matters": {"operation": "listMatter", "scopes": ["matters:read"], "roles": ["admin", "attorney", "staff"], "matter_wall": True},
    "deadlines": {"operation": "listDeadline", "scopes": ["deadlines:read"], "roles": ["admin", "attorney", "staff"], "matter_wall": True},
    "calendar": {"operation": "listCalendarEvent", "scopes": ["calendar:read"], "roles": ["admin", "attorney", "staff"], "matter_wall": True},
    "trust_alerts": {"operation": "listTrustFlag", "scopes": ["trust:read"], "roles": ["admin", "attorney", "staff", "accountant"], "matter_wall": True},
    "replenishment_requests": {"operation": "listTrustReplenishment", "scopes": ["trust:read"], "roles": ["admin", "attorney", "staff", "accountant"], "matter_wall": True},
    "ar_aging": {"operation": "reportArAging", "scopes": ["reports:read"], "roles": ["admin", "attorney", "staff", "accountant"], "matter_wall": True},
    "activity": {"operation": "listEvents", "scopes": ["events:read"], "roles": ["admin", "attorney", "staff"], "matter_wall": True},
    "conflict_checks": {"operation": "listConflictChecks", "scopes": ["conflicts:read"], "roles": ["admin", "attorney", "staff"], "matter_wall": True},
    "module_alerts": {"operation": "listModules", "scopes": ["modules:read"], "roles": ["admin"], "matter_wall": False},
}

BACKGROUND_FILE_SEQUENCE = [
    "createFileUpload",
    "getFileUpload",
    "putFileChunk",
    "completeFileUpload",
    "publishFile",
    "bulkFile",
    "reviewUnassignedFile",
]
BACKGROUND_FILE_PUBLICATION = {
    "sources": ["email", "zoom", "importer", "drop_folder"],
    "sequence": list(BACKGROUND_FILE_SEQUENCE),
    "issued_by": "core",
    "registered_module": True,
    "viewer_context_required": False,
    "scopes": ["files:write"],
    "grants_admin": False,
    "firmwide_bypass": False,
    "expired": "forbidden",
    "revoked": "forbidden",
    "missing_scope": "scope_denied",
    "wrong_module": "forbidden",
    "wrong_matter": "not_found",
    "unregistered": "forbidden",
    "operation_outside_grant": "forbidden",
    "on_failure": "no_file_no_event",
    "publish_events": ["file.created", "file.version_created"],
}
GENERIC_NONINTERACTIVE = frozenset(
    {
        "createEvent",
        "bulkEvent",
        "createNotification",
        "createFileLink",
        "createAiCall",
    }
)
SERVICE_PRINCIPAL_ISSUANCE = frozenset({"issueFilePublicationGrant"})
NONINTERACTIVE_OPERATIONS = (
    GENERIC_NONINTERACTIVE
    | frozenset(MALWARE_SEQUENCE)
    | frozenset(PAYMENT_PROCESSOR["sequence"])
    | frozenset(BACKGROUND_FILE_SEQUENCE)
)

_TRUST_POST = {
    "posted_by": "trust_handler",
    "append_only": True,
    "exactly_once": True,
    "time_module_also_posts": False,
    "below_zero": "posts",
    "below_zero_blocks": False,
    "original_edited": False,
}

def version_publication(*paths: str) -> dict:
    return {
        "version_publication": {
            "paths": list(paths),
            "event": "file.version_created",
            "cardinality": "one_per_published_version",
            "emit_after": "commit",
            "on_failure": "no_event_no_grant",
            "staged_bytes_emit": False,
        }
    }

def iter_operations(spec: dict):
    for path, item in spec.get("paths", {}).items():
        for method, operation in item.items():
            if method.startswith("x-") or not isinstance(operation, dict) or "operationId" not in operation:
                continue
            yield method, path, operation

def operation_by_id(spec: dict, operation_id: str) -> dict:
    for _method, _path, operation in iter_operations(spec):
        if operation["operationId"] == operation_id:
            return operation
    raise KeyError(operation_id)

def _success_medias(operation: dict) -> set[str]:
    medias: set[str] = set()
    for code, response in operation.get("responses", {}).items():
        if not (isinstance(code, str) and code.startswith("2") and isinstance(response, dict)):
            continue
        for media, body in (response.get("content") or {}).items():
            if media in _FILE_BYTE_TYPES or media.startswith(("image/", "audio/", "video/")):
                medias.add(media)
            declared = (body.get("schema") or {}).get("contentMediaType")
            if isinstance(declared, str) and (
                declared in _FILE_BYTE_TYPES or declared.startswith(("image/", "audio/", "video/"))
            ):
                medias.add(declared)
    return medias

def is_core_file_download(operation: dict) -> bool:
    """A core file-byte response. Path prefixes are not part of this test."""
    access = operation.get("x-togavsu-access") or {}
    if access.get("public") or operation.get("x-togavsu-mcp", {}).get("parity") == "transport":
        return False
    features = set(operation.get("x-togavsu-features") or [])
    if features & _EXCLUDED_DOWNLOAD_FEATURES:
        return False
    if not _success_medias(operation):
        return False
    if access.get("access_inherits") == "file":
        return True
    return bool(features & _CORE_FILE_FEATURES)

_CONFIRM_QUERY = {
    "name": "confirm_quarantine",
    "in": "query",
    "required": False,
    "schema": {"type": "boolean"},
    "description": "Must be true to download a quarantined file. Omitted or false returns quarantine_confirmation_required and no bytes.",
}

def _confirmation_input(operation: dict) -> dict | None:
    for parameter in operation.get("parameters", []):
        name = parameter.get("name")
        if name in ("confirm", "confirm_quarantine"):
            return {"in": "query", "name": name}
    body = (operation.get("requestBody") or {}).get("content", {}).get("application/json", {}).get("schema", {})
    ref = body.get("$ref", "")
    if ref.endswith("/FileArchiveRequest") or "confirm_quarantine" in (body.get("properties") or {}):
        return {"in": "body", "name": "confirm_quarantine"}
    return None

def apply_file_download_contract(spec: dict) -> None:
    for _method, _path, operation in iter_operations(spec):
        if not is_core_file_download(operation):
            continue
        access = operation["x-togavsu-access"]
        for key, value in FILE_DOWNLOAD.items():
            access[key] = value
        events = operation.setdefault("x-togavsu-events", [])
        if "file.downloaded" not in events:
            events.append("file.downloaded")
        codes = operation.setdefault("x-togavsu-error-codes", [])
        if "quarantine_confirmation_required" not in codes:
            codes.append("quarantine_confirmation_required")
        confirm = _confirmation_input(operation)
        if confirm is None:
            operation.setdefault("parameters", []).append(dict(_CONFIRM_QUERY))
            confirm = {"in": "query", "name": "confirm_quarantine"}
        access["quarantine_confirmation_input"] = confirm
        if "audit log" not in operation.get("description", ""):
            operation["description"] = operation["description"].rstrip() + (
                " The download is written to the audit log and the case timeline as file.downloaded."
                " A client download is highlighted. A quarantined file requires confirmation."
            )

def is_published_client_read(operation: dict) -> bool:
    if operation.get("x-togavsu-mcp", {}).get("parity") == "transport":
        return False
    if operation["operationId"] == "createModuleContextToken":
        return False
    access = operation.get("x-togavsu-access") or {}
    if "client" not in access.get("roles", []):
        return False
    if access.get("client") not in ("shared", "own"):
        return False
    if not operation.get("x-togavsu-writes"):
        return True
    return access.get("event") == "file.downloaded"

def client_context_operation_ids(spec: dict) -> list[str]:
    token = operation_by_id(spec, "createModuleContextToken")
    sequence = token["x-togavsu-access"]["client_context"]["sequence"]
    allowed: list[str] = []
    for step in sequence:
        if step["step"] == "obtain_context":
            continue
        if step["operation"] not in allowed:
            allowed.append(step["operation"])
    for _method, _path, operation in iter_operations(spec):
        operation_id = operation["operationId"]
        if operation_id in allowed:
            continue
        if is_published_client_read(operation):
            allowed.append(operation_id)
    return allowed

def publish_client_context(spec: dict) -> None:
    policy = operation_by_id(spec, "createModuleContextToken")["x-togavsu-access"]["client_context"]
    policy["published_reads"] = {
        "role": "client",
        "client_access": ["shared", "own"],
        "non_writes": True,
        "file_byte_downloads": True,
        "exclude_transport": True,
        "exclude_operations": ["createModuleContextToken"],
    }
    policy["client_allowed_operations"] = client_context_operation_ids(spec)
    policy["grants_admin"] = False
    policy["firmwide_bypass"] = False

def _payment_effect() -> dict:
    return {
        "authorize_before": "any_mutation",
        "insufficient_scopes": "scope_denied",
        "no_mutation_on_denial": True,
        "operating": {"scopes": ["payments:write"], "trust_posted": False},
        "trust_destination": {
            "scopes": ["payments:write", "trust:write"],
            "when": "first_completed_trust_destination",
            "entry_type": "deposit",
            "trust_entry": {**_TRUST_POST, "entry_type": "deposit"},
        },
    }

def _payment_reversal() -> dict:
    return {
        "authorize_before": "any_mutation",
        "insufficient_scopes": "scope_denied",
        "no_mutation_on_denial": True,
        "operating": {"when": "no_trust_entry", "scopes": ["payments:write"], "trust_posted": False},
        "trust_linked": {
            "when": "payment_has_trust_entry",
            "scopes": ["payments:write", "trust:withdraw"],
            "trust_entry": {
                **_TRUST_POST,
                "entry_type": "reversal",
                "flag": "trust.flag_raised",
            },
        },
    }

def _ensure_code(operation: dict, code: str) -> None:
    codes = operation.setdefault("x-togavsu-error-codes", [])
    if code not in codes:
        codes.append(code)

def publish_payment_policy(spec: dict) -> None:
    for operation_id in ("createPayment", "bulkPayment", "updatePaymentStatus"):
        operation = operation_by_id(spec, operation_id)
        operation["x-togavsu-access"]["payment_effect"] = _payment_effect()
        _ensure_code(operation, "scope_denied")
    reversal = operation_by_id(spec, "reversePayment")
    reversal["x-togavsu-access"]["payment_reversal"] = _payment_reversal()
    _ensure_code(reversal, "scope_denied")
    status = operation_by_id(spec, "updatePaymentStatus")
    status["x-togavsu-access"]["payment_status"] = PAYMENT_STATUS_GRAPH
    _ensure_code(status, "conflict")
    bulk = operation_by_id(spec, "bulkPayment")
    if "trust:write" not in bulk["description"]:
        bulk["description"] = bulk["description"].rstrip() + (
            " A completed trust destination also needs trust:write. "
            "The scope check runs before any payment or ledger write. One item that fails the check rejects the batch and writes nothing."
        )

def tag_noninteractive_workflows(spec: dict) -> None:
    for _method, _path, operation in iter_operations(spec):
        operation_id = operation["operationId"]
        if operation_id in PAYMENT_PROCESSOR["sequence"]:
            operation["x-togavsu-access"]["noninteractive_workflow"] = "payment_processor"
        elif operation_id in MALWARE_SEQUENCE:
            operation["x-togavsu-access"]["noninteractive_workflow"] = "malware_scan"
        elif operation_id in BACKGROUND_FILE_SEQUENCE:
            operation["x-togavsu-access"]["noninteractive_workflow"] = "background_file_publication"

def _json_schema_slot(operation: dict) -> dict:
    for code, response in operation["responses"].items():
        if isinstance(code, str) and code.startswith("2") and isinstance(response, dict) and "content" in response:
            return response["content"]["application/json"]
    raise KeyError(operation["operationId"])

def attach_grant_projections(spec: dict) -> None:
    schemas = spec["components"]["schemas"]
    granted = set(MALWARE_DELIVERY["metadata_fields"])
    for name in ("FileGrantView", "FileVersionGrantView"):
        schema = schemas[name]
        required = set(schema["required"])
        if not required <= granted:
            raise RuntimeError(f"{name} requires {sorted(required - granted)} outside the malware grant")
        if PRIVATE_GRANT_FIELDS & set(schema["properties"]):
            raise RuntimeError(f"{name} still publishes private fields")
        if schema.get("additionalProperties") is not False:
            raise RuntimeError(f"{name} must reject ungranted fields")
    projections = {
        "getFile": ("FileGrantView", FILE_GRANT_FIELDS),
        "listFileVersions": ("FileVersionGrantPage", VERSION_GRANT_FIELDS),
        "quarantineFileVersion": ("FileVersionGrantView", VERSION_GRANT_FIELDS),
    }
    for operation_id, (schema_name, fields) in projections.items():
        operation = operation_by_id(spec, operation_id)
        operation["x-togavsu-access"]["response_projection"] = {
            "when": "task_authorization",
            "schema": schema_name,
            "fields": list(fields),
            "additional_properties": False,
            "required_subset_of_grant": True,
        }
        slot = _json_schema_slot(operation)
        slot["schema"] = {
            "description": (
                "A viewer receives the full record. A file.version_created task grant receives "
                f"{schema_name} and no ungranted field. FileVersionGrantPage requires projection=grant, "
                "so an empty data array matches only one branch."
            ),
            "oneOf": [slot["schema"], {"$ref": f"#/components/schemas/{schema_name}"}],
        }

def publish_extension_include(spec: dict) -> None:
    """An extension table is included with the core record. A published view still needs its scope."""
    rule = {
        "declared_table": "owner_module_or_viewer",
        "published_view": "views:{module}.{view}:read",
        "other_module_unpublished_table": "forbidden",
    }
    for _method, _path, operation in iter_operations(spec):
        access = operation["x-togavsu-access"]
        if "include_scope_pattern" in access:
            access["extension_include"] = dict(rule)

def finalize(spec: dict) -> None:
    apply_file_download_contract(spec)
    publish_client_context(spec)
    publish_payment_policy(spec)
    tag_noninteractive_workflows(spec)
    attach_grant_projections(spec)
    publish_extension_include(spec)
    ensure_mutation_events(spec)
    publish_event_owners(spec)
    publish_trust_debit(spec)
    publish_inherited_history(spec)
    publish_event_scopes(spec)
    publish_owner_read_scopes(spec)
    align_event_scopes_to_gets(spec)
    publish_trust_flags(spec)
    publish_matter_effect_order(spec)
    publish_invoice_item_privileges(spec)
    publish_client_projections(spec)
    publish_page_sizes(spec)
    require_supported_qualifiers(spec)

# Every key written on x-togavsu-access. A key missing here fails the build.
# context: the key documents post-authorization behavior and does not by itself allow a call.
_CONTEXT_QUALIFIERS = frozenset(
    {
        "anonymous_when",
        "archive_cases",
        "assigns_matter",
        "audit",
        "authorizes",
        "badge_counts",
        "below_zero",
        "client_context",
        "client_csv_columns",
        "client_download",
        "client_fields",
        "client_projection",
        "client_schema",
        "creation_separate",
        "doc_tender_retry",
        "download_filter",
        "draft_selector",
        "effect_order",
        "email_uniqueness",
        "event",
        "event_exemption",
        "event_read_scopes",
        "events_on",
        "events_on_failure",
        "existing_grant_authorizes_issuance",
        "extension_include",
        "filter_before",
        "firm_feed_roles",
        "heading_default",
        "idempotency",
        "inherits",
        "markdown",
        "old_secret",
        "on_denial",
        "on_disable",
        "on_failure",
        "on_new_permissions",
        "on_uninstall",
        "on_update",
        "open_matter",
        "own_heading_only",
        "owner_namespace",
        "page_size_cap_setting",
        "page_size_setting",
        "payment_events",
        "payment_status",
        "person_lock",
        "processor_issuance",
        "quarantine_confirmation_input",
        "reconciliation",
        "rejected_fields",
        "relationship_immutable",
        "replay_events",
        "required_status",
        "response_projection",
        "roles_bound",
        "runtime_registration",
        "sanitize_rejects",
        "scope_cases",
        "secret_on_read",
        "server_owned_relationships",
        "staff_schema",
        "staff_sign_in",
        "task_grant_required",
        "timeline",
        "token",
        "tool_authorization",
        "trust_flag",
        "trust_posting",
        "unassigned_review",
        "unique_upload_ids",
        "version_publication",
        "viewer_required",
    }
)
_GATE_QUALIFIERS = frozenset(
    {
        "access_cases",
        "access_inherits",
        "accountant",
        "admin_bypasses_wall",
        "admin_sees_others",
        "attorney_only",
        "attorney_only_files",
        "authorization_category",
        "authorize_before",
        "batch_authorization",
        "bearer_amplification",
        "client",
        "domain_routing",
        "entry_type_roles",
        "entry_type_scopes",
        "external_effects",
        "feed_owner",
        "firmwide_bypass",
        "grant_issuance",
        "grants_admin",
        "inaccessible_result",
        "include_scope_pattern",
        "inherited_record_access",
        "issuance",
        "item_privileges",
        "matter_id",
        "matter_wall",
        "module_delegation",
        "module_principal",
        "noninteractive_workflow",
        "off_wall",
        "owner",
        "owner_binding",
        "partial_core_write",
        "payment_effect",
        "payment_reversal",
        "public",
        "quarantine_confirmation",
        "recipient",
        "record_type_domain",
        "record_type_scopes",
        "replaced_core",
        "resolve_before_write",
        "roles",
        "scope_bound",
        "scope_combination",
        "scopes",
        "trust_debit",
        "unavailable_replacement",
    }
)
SUPPORTED_QUALIFIERS = _CONTEXT_QUALIFIERS | _GATE_QUALIFIERS | frozenset({"replacement"})

def require_supported_qualifiers(spec: dict) -> None:
    """Fail the build when an operation declares a qualifier the dispatcher does not implement."""
    unknown = []
    for _method, _path, operation in iter_operations(spec):
        for key in operation.get("x-togavsu-access") or {}:
            if key not in SUPPORTED_QUALIFIERS:
                unknown.append(f"{operation['operationId']}.{key}")
        if operation.get("x-togavsu-replaceable-domain") and "replacement" not in SUPPORTED_QUALIFIERS:
            unknown.append(operation["operationId"] + ".replacement")
    if unknown:
        raise RuntimeError("unsupported access qualifiers: " + ", ".join(sorted(unknown)))

# Byte transport only. The upload record is created by createFileUpload and staged by completeFileUpload.
EVENT_EXEMPTIONS = {
    "putFileChunk": (
        "Transports bytes of an upload that is not yet a file. "
        "completeFileUpload emits file_upload.staged. "
        "A file and file.version_created are emitted only after publish commits. "
        "A failed chunk emits nothing."
    ),
}

MUTATION_EVENTS = {
    "login": ["auth.login_challenged"],
    "verifyTotp": ["session.created"],
    "startTotpEnrollment": ["auth.enrollment_started"],
    "confirmTotpEnrollment": ["session.created"],
    "verifyRecoveryCode": ["session.created"],
    "logout": ["session.ended"],
    "requestPasswordReset": ["auth.password_reset_requested"],
    "confirmPasswordReset": ["user.password_changed"],
    "requestEmailChange": ["user.email_change_requested"],
    "changePassword": ["user.password_changed"],
    "resetTotp": ["user.totp_reset_started"],
    "confirmTotpReset": ["user.totp_reset"],
    "regenerateRecoveryCodes": ["user.recovery_codes_regenerated"],
    "updateMeSettings": ["user.settings_updated"],
    "putNotificationPreferences": ["notification_preferences.updated"],
    "putSidebarState": ["user.sidebar_updated"],
    "deleteSession": ["session.ended"],
    "createApiToken": ["api_token.created"],
    "revokeApiToken": ["api_token.revoked"],
    "createMarkerDefinition": ["marker.created"],
    "updateMarkerDefinition": ["marker.updated"],
    "bulkCustomFieldValue": ["custom_field.updated"],
    "pauseTimer": ["timer.paused"],
    "resumeTimer": ["timer.resumed"],
    "cancelTrustReplenishment": ["trust.replenishment_cancelled"],
    "satisfyTrustReplenishment": ["trust.replenishment_satisfied"],
    "createIcalFeed": ["ical_feed.created"],
    "revokeIcalFeed": ["ical_feed.revoked"],
    "createFileUpload": ["file_upload.created"],
    "acquireEditLock": ["file.lock_acquired"],
    "renewEditLock": ["file.lock_renewed"],
    "testStorageBackend": ["storage_backend.tested"],
    "markNotificationRead": ["notification.read"],
    "markAllNotificationsRead": ["notification.read"],
    "putCurrentLayout": ["layout.updated"],
    "setDefaultLayout": ["layout.default_set"],
    "applyLayout": ["layout.applied"],
    "createModuleHelpPage": ["help.updated"],
    "replaceModuleHelpPages": ["help.updated"],
    "deleteModuleHelpPage": ["help.updated"],
    "createEvent": ["event.appended"],
    "bulkEvent": ["event.appended"],
    "checkAiProvider": ["ai.provider_checked"],
    "testMail": ["mail.tested"],
    "testBackupDestination": ["backup_destination.tested"],
    "createBackupUpload": ["backup_upload.created"],
    "createModuleContextToken": ["module.context_issued"],
    "updateTrustedKey": ["signing_key.updated"],
}

def ensure_mutation_events(spec: dict) -> None:
    missing = []
    for _method, _path, operation in iter_operations(spec):
        if not operation.get("x-togavsu-writes"):
            continue
        operation_id = operation["operationId"]
        access = operation["x-togavsu-access"]
        if operation_id in EVENT_EXEMPTIONS:
            access["event_exemption"] = EVENT_EXEMPTIONS[operation_id]
            access["events_on_failure"] = "none"
            continue
        if not operation.get("x-togavsu-events"):
            events = MUTATION_EVENTS.get(operation_id)
            if not events:
                missing.append(operation_id)
                continue
            operation["x-togavsu-events"] = list(events)
        access["events_on"] = "commit"
        access["events_on_failure"] = "none"
    if missing:
        raise RuntimeError("writes without events: " + ", ".join(sorted(missing)))

_EVENT_SCOPE_REGISTRY: dict[str, list[str]] = {}

_SCOPE_READ_ALIAS = {
    "trust:withdraw": "trust:read",
    "invoices:approve": "invoices:read",
    "invoices:send": "invoices:read",
    "conflicts:decide": "conflicts:read",
    "editor:run": "files:read",
}

WEBHOOK_DELIVERY = {
    "order": ["installed", "subscription", "scope", "matter_wall", "attorney_only", "owner", "serialize"],
    "unauthorized_envelope": None,
    "grants_admin": False,
    "firmwide_bypass": False,
}

def _domain_read(scope: str) -> str | None:
    from contract.scopes import SCOPES

    if scope in _SCOPE_READ_ALIAS:
        return _SCOPE_READ_ALIAS[scope]
    if scope.endswith(":write"):
        candidate = scope[: -len(":write")] + ":read"
        return candidate if candidate in SCOPES else None
    if scope.endswith(":read") or scope.endswith(":admin") or scope == "ai:use":
        return scope if scope in SCOPES or scope == "ai:use" else None
    return None

HISTORY_RESPONSE_SCHEMAS = {
    "AuditEntry",
    "WebhookEvent",
    "Event",
    "FileHistoryEntry",
    "AiCall",
    "ReportTable",
    "SearchResponse",
    "Widget",
}

def _response_schema_names(operation: dict) -> set[str]:
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

    for code, response in operation.get("responses", {}).items():
        if isinstance(code, str) and code.startswith("2") and isinstance(response, dict):
            walk(response.get("content") or {})
    return found

def publish_inherited_history(spec: dict) -> None:
    """Firm logs, exports, and delivery history use the underlying record. Admin does not bypass a wall."""
    rule = {
        "matter_wall": True,
        "admin_bypasses_wall": False,
        "unreadable_row": "omitted",
        "attorney_only_values": "redacted",
        "record_restrictions": [
            {
                "record_type": "file",
                "visibility": "attorney_only",
                "effect": "omit_row",
                "roles_denied": ["staff", "accountant", "client"],
            }
        ],
        "field_redactions": [
            {"record_type": "contact", "fields": ["identifier"], "effect": "redact_field"},
            {"record_type": "conflict_record", "fields": ["reason"], "effect": "redact_field"},
            {"record_type": "conflict_review", "fields": ["reason"], "effect": "redact_field"},
        ],
        "scopes_from": "underlying_record",
        "membership": "per_matter_id",
    }
    for method, _path, operation in iter_operations(spec):
        if method != "get":
            continue
        if not (_response_schema_names(operation) & HISTORY_RESPONSE_SCHEMAS):
            continue
        access = operation["x-togavsu-access"]
        access["matter_wall"] = True
        access["admin_bypasses_wall"] = False
        access["inherited_record_access"] = dict(rule)

REQUIRED_BACKGROUND_EVENTS = {
    "matter.created": ["matters:read"],
    "time_entry.updated": ["time:read"],
    "trust.flag_raised": ["trust:read"],
    "file.version_created": ["files:read"],
    "doc_tender.published": ["doc_tender:read"],
    "doc_tender.exception_opened": ["doc_tender:read"],
}

def publish_event_scopes(spec: dict) -> None:
    """Map every emitted event to the read scope of the resource that emits it. Unknown events stay unmapped."""
    buckets: dict[str, list[set[str]]] = {}
    for _method, _path, operation in iter_operations(spec):
        events = operation.get("x-togavsu-events") or []
        if not events:
            continue
        explicit = operation["x-togavsu-access"].get("event_read_scopes")
        if explicit is not None:
            derived = set(explicit)
        else:
            derived = {read for scope in operation["x-togavsu-access"].get("scopes") or [] if (read := _domain_read(scope))}
        for event in events:
            if event.startswith("custom_field."):
                continue
            buckets.setdefault(event, []).append(derived)
        rule = operation["x-togavsu-access"].get("doc_tender_retry")
        published_event = (rule or {}).get("published_event")
        if published_event and published_event not in events:
            buckets.setdefault(published_event, []).append(derived)
    registry: dict[str, list[str]] = {}
    for event, groups in buckets.items():
        registry[event] = _event_scope_choice(event, groups)
    for event, scopes in REQUIRED_BACKGROUND_EVENTS.items():
        current = registry.get(event) or []
        if current != scopes:
            registry[event] = list(scopes)
    spec["x-togavsu-background-events"] = {
        event: {
            "scopes": list(scopes),
            "matter_wall": True,
            "attorney_only": "redacted",
            "recipient": "subscribed_module",
            "unknown_event": "fail_closed",
        }
        for event, scopes in REQUIRED_BACKGROUND_EVENTS.items()
    }
    spec["x-togavsu-event-scopes"] = registry
    _EVENT_SCOPE_REGISTRY.clear()
    _EVENT_SCOPE_REGISTRY.update(registry)

def publish_owner_read_scopes(spec: dict) -> None:
    """A public emitter does not leave an owner-bound event without its read scope."""
    family_scopes: dict[str, list[str]] = {}
    for method, path, operation in iter_operations(spec):
        if method != "get":
            continue
        family = _path_event_family(path)
        if not family or not _owner_binding(operation["x-togavsu-access"]):
            continue
        reads = [read for scope in operation["x-togavsu-access"].get("scopes") or [] if (read := _domain_read(scope))]
        if reads:
            family_scopes[family] = sorted(set(reads))
    registry = spec["x-togavsu-event-scopes"]
    for event, rule in spec.get("x-togavsu-event-owners", {}).items():
        if not registry.get(event):
            registry[event] = list(family_scopes.get(rule["family"]) or [])
    _EVENT_SCOPE_REGISTRY.clear()
    _EVENT_SCOPE_REGISTRY.update(registry)

def align_event_scopes_to_gets(spec: dict) -> None:
    """When the collection GET requires a write scope, its events keep that scope instead of a normalized read."""
    gets: dict[str, list[str]] = {}
    emitted: dict[str, set[str]] = {}
    for method, path, operation in iter_operations(spec):
        parts = [part for part in path.split("/") if part]
        if len(parts) < 3 or parts[0] != "api" or parts[1] != "v1":
            continue
        collection = parts[2]
        if method == "get":
            scopes = list(operation["x-togavsu-access"].get("scopes") or [])
            if scopes and ("{" in path or collection not in gets):
                gets[collection] = scopes
        for event in operation.get("x-togavsu-events") or []:
            emitted.setdefault(event, set()).add(collection)
    registry = spec["x-togavsu-event-scopes"]
    for event, collections in emitted.items():
        for collection in collections:
            scopes = gets.get(collection)
            if not scopes:
                continue
            if any(_domain_read(scope) not in (None, scope) for scope in scopes):
                registry[event] = sorted(set(scopes))
                break
    _EVENT_SCOPE_REGISTRY.clear()
    _EVENT_SCOPE_REGISTRY.update(registry)

def _event_scope_choice(event: str, groups: list[set[str]]) -> list[str]:
    """One domain read, or none. Empty means fail closed: events:read is not a substitute.

    Emitters that publish no domain read (public auth writes) do not erase a known one.
    When the remaining emitters disagree, keep the read scope named by the event's own
    resource when one of them published it. Otherwise the event stays undeliverable.
    """
    nonempty = [group for group in groups if group]
    if not nonempty:
        return []
    agreed = set.intersection(*nonempty)
    if agreed:
        return sorted(agreed)
    published = set.union(*nonempty)
    token = event.split(".", 1)[0]
    for candidate in (f"{token}:read", f"{token}s:read"):
        if candidate in published:
            return [candidate]
    return []

_PATH_EVENT_FAMILY = {
    "notifications": "notification",
    "api-tokens": "api_token",
    "sessions": "session",
    "ical-feeds": "ical_feed",
    "file-uploads": "file_upload",
}
_EVENT_OWNERS: dict[str, dict] = {}

def _owner_binding(access: dict) -> dict | None:
    """Read constraint for a record another user must not receive. None when scope and wall are enough."""
    if access.get("recipient") == "self" and access.get("admin_sees_others") is False:
        return {"binding": "recipient", "admin_bypass": False}
    if access.get("feed_owner") == "self_or_admin" or access.get("owner") == "self_or_admin":
        return {"binding": "owner", "admin_bypass": True}
    if access.get("owner") == "self":
        return {"binding": "owner", "admin_bypass": False}
    return None

def _path_event_family(path: str) -> str | None:
    parts = [part for part in path.split("/") if part]
    if len(parts) < 3 or parts[0] != "api" or parts[1] != "v1":
        return None
    return _PATH_EVENT_FAMILY.get(parts[2])

def publish_event_owners(spec: dict) -> None:
    """Owner rules come from the read of the record, then apply to every event in that family."""
    families: dict[str, dict] = {}
    for _method, path, operation in iter_operations(spec):
        binding = _owner_binding(operation["x-togavsu-access"])
        family = _path_event_family(path)
        if not binding or not family:
            continue
        current = families.get(family)
        if current is None or (current["admin_bypass"] and not binding["admin_bypass"]):
            families[family] = binding
    registry: dict[str, dict] = {}
    for _method, _path, operation in iter_operations(spec):
        for event in operation.get("x-togavsu-events") or []:
            family = event.split(".", 1)[0]
            binding = families.get(family)
            if not binding:
                continue
            registry[event] = {
                "family": family,
                "binding": binding["binding"],
                "admin_bypass": binding["admin_bypass"],
                "deleted_source": "predelete",
                "asserted_recipient": "ignored",
                "missing_owner": "fail_closed",
                "owner_form": "tagged" if family == "file_upload" else "user",
            }
    spec["x-togavsu-event-owners"] = registry
    _EVENT_OWNERS.clear()
    _EVENT_OWNERS.update(registry)

def _schema_names_matter(spec: dict, schema: dict, seen: set[str] | None = None) -> bool:
    if not isinstance(schema, dict):
        return False
    ref = schema.get("$ref")
    if isinstance(ref, str) and ref.startswith("#/components/schemas/"):
        name = ref.rsplit("/", 1)[1]
        seen = set() if seen is None else seen
        if name in seen:
            return False
        seen.add(name)
        return _schema_names_matter(spec, spec["components"]["schemas"].get(name) or {}, seen)
    properties = schema.get("properties") or {}
    if "matter_id" in properties or "matter_ids" in properties:
        return True
    for key in ("allOf", "oneOf", "anyOf"):
        for part in schema.get(key) or []:
            if _schema_names_matter(spec, part, seen):
                return True
    return False

def operation_names_matter(spec: dict, path: str, operation: dict) -> bool:
    """True when the caller can name a matter on this operation."""
    if "{matter_id}" in path:
        return True
    for parameter in operation.get("parameters") or []:
        name = parameter.get("name")
        ref = parameter.get("$ref")
        if isinstance(ref, str) and ref.startswith("#/components/parameters/"):
            name = spec["components"]["parameters"][ref.rsplit("/", 1)[1]].get("name")
        if name in {"matter_id", "matter_ids"}:
            return True
    for payload in ((operation.get("requestBody") or {}).get("content") or {}).values():
        if _schema_names_matter(spec, payload.get("schema") or {}):
            return True
    return False

def publish_matter_effect_order(spec: dict) -> None:
    """A write that names a matter authorizes that wall before any external effect."""
    for method, path, operation in iter_operations(spec):
        if method == "get" or operation.get("x-togavsu-writes") is False:
            continue
        if not operation_names_matter(spec, path, operation):
            continue
        access = operation["x-togavsu-access"]
        if not access.get("matter_wall"):
            raise RuntimeError(f"{operation['operationId']} names a matter without a wall check")
        access["admin_bypasses_wall"] = False
        access.setdefault("effect_order", "authorize_before_external_effect")

def publish_trust_flags(spec: dict) -> None:
    """Every ledger writer catalogs the flag. Emission stays conditional on the committed balance."""
    names = ("trust.entry_posted", "trust.flag_raised")
    for operation_id in (
        "postTrustEntry",
        "bulkTrustEntry",
        "reverseTrustEntry",
        "applyTrustToInvoice",
        "applyFlatFee",
        "reversePayment",
        "createPayment",
        "bulkPayment",
        "updatePaymentStatus",
    ):
        operation = operation_by_id(spec, operation_id)
        access = operation["x-togavsu-access"]
        access["trust_flag"] = {**_TRUST_FLAG, "metadata": list(_TRUST_FLAG["metadata"])}
        if access.get("below_zero") not in (None, "posts"):
            raise RuntimeError(f"{operation_id} blocks a below-zero trust balance")
        events = operation.setdefault("x-togavsu-events", [])
        for name in names:
            if name not in events:
                events.append(name)
    flag = spec.get("x-togavsu-background-events", {}).get("trust.flag_raised")
    if flag is not None:
        flag["metadata"] = list(_FLAG_METADATA)
        flag["scopes"] = ["trust:read"]
        flag["below_zero"] = "warning"

def publish_trust_debit(spec: dict) -> None:
    rule = {
        "client_id": "required",
        "trust_account_id": "required",
        "selection": "explicit",
        "other_accounts": "unchanged",
        "authorize_before": "any_mutation",
        "inactive_or_unauthorized": "forbidden",
        "wrong_client_or_account": "not_found",
    }
    for operation_id in ("applyTrustToInvoice", "applyFlatFee"):
        operation_by_id(spec, operation_id)["x-togavsu-access"]["trust_debit"] = dict(rule)

def publish_invoice_item_privileges(spec: dict) -> None:
    """Copy each invoice effect from the individual operation. The bulk call cannot widen it."""
    mapping = {
        "draft": "createInvoiceDrafts",
        "sent": "sendInvoice",
        "viewed": "sendInvoice",
        "paid": "sendInvoice",
        "void": "voidInvoice",
        "write_off": "writeOffInvoice",
        "amount_paid": "createPayment",
    }
    privileges = {}
    for key, operation_id in mapping.items():
        access = operation_by_id(spec, operation_id)["x-togavsu-access"]
        privileges[key] = {"operation": operation_id, "roles": list(access["roles"]), "scopes": list(access["scopes"])}
    operation_by_id(spec, "bulkInvoice")["x-togavsu-access"]["item_privileges"] = privileges

def publish_client_projections(spec: dict) -> None:
    """Role projections are the client schema's properties. A short omission list is not the rule."""
    schemas = spec["components"]["schemas"]
    for _method, _path, operation in iter_operations(spec):
        access = operation["x-togavsu-access"]
        name = access.get("client_schema")
        if not isinstance(name, str) or name not in schemas:
            continue
        fields = list((schemas[name].get("properties") or {}))
        access["client_projection"] = "allowlist"
        access["client_fields"] = fields
        access.pop("client_omitted_fields", None)
        content = ((operation.get("responses") or {}).get("200") or {}).get("content") or {}
        if "text/csv" in content:
            access["client_csv_columns"] = list(fields)
            access.pop("client_csv_omitted_columns", None)

def _setting_value(key: str, current: dict | None = None):
    from contract.settings import SETTINGS

    registry = {item["key"]: item for item in SETTINGS}
    if current and key in current:
        return current[key]
    return registry[key]["default"]

def page_limit(current: dict | None = None, *, override: str | None = None) -> dict:
    """Effective page bounds. A list-specific setting replaces the general default and cannot exceed its own maximum."""
    from contract.settings import SETTINGS

    registry = {item["key"]: item for item in SETTINGS}
    cap = _setting_value("api.max_page_size", current)
    default = min(_setting_value("api.page_size", current), cap)
    minimum = registry["api.page_size"]["minimum"]
    if override:
        setting = registry[override]
        specific_cap = setting["maximum"]
        specific = min(_setting_value(override, current), specific_cap)
        return {
            "default": specific,
            "minimum": setting["minimum"],
            "maximum": specific_cap,
            "schema_maximum": specific_cap,
            "setting": override,
        }
    return {
        "default": default,
        "minimum": minimum,
        "maximum": cap,
        "schema_maximum": registry["api.max_page_size"]["maximum"],
        "setting": "api.page_size",
    }

def publish_page_sizes(spec: dict) -> None:
    """Publish each list's default and maximum from the settings registry."""
    general = page_limit()
    for _method, _path, operation in iter_operations(spec):
        description = operation.get("description") or ""
        named = [key for key in ("activity.page_size", "notes.page_size") if key in description]
        override = named[0] if len(named) == 1 else None
        bounds = page_limit(override=override)
        for parameter in operation.get("parameters") or []:
            if parameter.get("name") != "limit":
                continue
            parameter["schema"] = {
                "type": "integer",
                "minimum": bounds["minimum"],
                "maximum": bounds["schema_maximum"],
                "default": bounds["default"],
            }
            if override:
                parameter["description"] = (
                    f"Page size. The default is {override} (starting value {bounds['default']}). "
                    f"The published maximum is that setting's registry ceiling, {bounds['schema_maximum']}. "
                    "The next request uses an admin change, and the size cannot exceed that ceiling."
                )
                operation["x-togavsu-access"]["page_size_setting"] = override
            else:
                parameter["description"] = (
                    "Page size. The published maximum is the registry ceiling for api.max_page_size, 10000. "
                    "The active cap is the current api.max_page_size, starting at 200, and a limit above that cap is refused. "
                    "The default is api.page_size, starting at 50, and cannot exceed the active cap. The next request uses an admin change."
                )
                operation["x-togavsu-access"]["page_size_setting"] = "api.page_size"
                operation["x-togavsu-access"]["page_size_cap_setting"] = "api.max_page_size"

PAYMENT_PROCESSOR["status_graph"] = PAYMENT_STATUS_GRAPH

__all__ = [
    'BACKGROUND_FILE_PUBLICATION',
    'BACKGROUND_FILE_SEQUENCE',
    'CORE_EXTENSION_TARGETS',
    'CORE_PAGE_TARGETS',
    'EVENT_EXEMPTIONS',
    'FILE_DOWNLOAD',
    'FILE_GRANT_FIELDS',
    'GENERIC_NONINTERACTIVE',
    'HISTORY_RESPONSE_SCHEMAS',
    'MALWARE_DELIVERY',
    'MALWARE_SEQUENCE',
    'MODULE_GUIDE',
    'MODULE_ID_PATTERN',
    'MUTATION_EVENTS',
    'NONINTERACTIVE_OPERATIONS',
    'PAYMENT_PROCESSOR',
    'PAYMENT_STATUS_GRAPH',
    'PRIVATE_GRANT_FIELDS',
    'REQUIRED_BACKGROUND_EVENTS',
    'SERVICE_PRINCIPAL_ISSUANCE',
    'SUPPORTED_QUALIFIERS',
    'VERSION_GRANT_FIELDS',
    'WEBHOOK_DELIVERY',
    '_CONFIRM_QUERY',
    '_CONTEXT_QUALIFIERS',
    '_CORE_FILE_FEATURES',
    '_EVENT_OWNERS',
    '_EVENT_SCOPE_REGISTRY',
    '_EXCLUDED_DOWNLOAD_FEATURES',
    '_FILE_BYTE_TYPES',
    '_FLAG_METADATA',
    '_GATE_QUALIFIERS',
    '_PATH_EVENT_FAMILY',
    '_SCOPE_READ_ALIAS',
    '_TRUST_FLAG',
    '_TRUST_POST',
    '_confirmation_input',
    '_domain_read',
    '_ensure_code',
    '_event_scope_choice',
    '_json_schema_slot',
    '_owner_binding',
    '_path_event_family',
    '_payment_effect',
    '_payment_reversal',
    '_response_schema_names',
    '_schema_names_matter',
    '_setting_value',
    '_success_medias',
    'align_event_scopes_to_gets',
    'apply_file_download_contract',
    'attach_grant_projections',
    'client_context_operation_ids',
    'ensure_mutation_events',
    'finalize',
    'is_core_file_download',
    'is_published_client_read',
    'iter_operations',
    'operation_by_id',
    'operation_names_matter',
    'page_limit',
    'publish_client_context',
    'publish_client_projections',
    'publish_event_owners',
    'publish_event_scopes',
    'publish_extension_include',
    'publish_inherited_history',
    'publish_invoice_item_privileges',
    'publish_matter_effect_order',
    'publish_owner_read_scopes',
    'publish_page_sizes',
    'publish_payment_policy',
    'publish_trust_debit',
    'publish_trust_flags',
    'require_supported_qualifiers',
    'tag_noninteractive_workflows',
    'version_publication',
]

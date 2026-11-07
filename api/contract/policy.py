"""Derived access policy for the core contract.

Interactive module calls intersect approved scopes with the viewer's role, walls,
ownership, sharing, and field restrictions. Client portal reads come from operations
the core already publishes for a client. Noninteractive calls exist only when the
core issues a bounded workflow grant and delivers it. File-byte downloads are
recognized by response semantics, not by a path prefix.
"""

from __future__ import annotations

import hashlib
import hmac
import html
import json
import re
from datetime import date

# The path parameter {module_id} uses this same pattern. Callers do not get a second, looser rule.
MODULE_ID_PATTERN = "^[a-z0-9][a-z0-9-]{0,63}$"
_MODULE_ID_SLUG = re.compile(MODULE_ID_PATTERN)
_UUID_TEXT = re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")
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
from html.parser import HTMLParser

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
PAYMENT_PROCESSOR["status_graph"] = PAYMENT_STATUS_GRAPH
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
    publish_trust_flags(spec)
    publish_matter_effect_order(spec)
    publish_invoice_item_privileges(spec)
    publish_client_projections(spec)
    publish_page_sizes(spec)


def _denied(result: str) -> dict:
    return {"result": result, "payment_mutated": False, "ledger_mutated": False, "flag": False, "grant": None}


def trust_account_failure(call: dict) -> str | None:
    """None when the destination and account are allowed. Otherwise a denial code and no write."""
    destination = call.get("destination")
    if destination == "operating":
        if call.get("trust_account_id") is not None:
            return "validation_failed"
        return None
    if destination != "trust":
        return None
    account = call.get("trust_account_id")
    if not account:
        return "validation_failed"
    grant_account = (call.get("grant") or {}).get("trust_account_id")
    if grant_account not in (None, account):
        return "forbidden"
    record = None
    for item in call.get("trust_accounts") or []:
        if item.get("id") == account:
            record = item
            break
    if record is None:
        return "not_found"
    if record.get("firm_id") and call.get("firm_id") and record["firm_id"] != call["firm_id"]:
        return "not_found"
    if record.get("active") is False or record.get("authorized") is False:
        return "forbidden"
    return None


def payment_reversal_decision(operation: dict, call: dict) -> dict:
    access = operation["x-togavsu-access"]
    policy = access["payment_reversal"]
    if policy.get("authorize_before") != "any_mutation":
        return _denied("forbidden")
    if call["role"] not in access["roles"]:
        return _denied("forbidden")
    if not matter_visible(call, call, single_record=True):
        return _denied("not_found")
    branch = policy["trust_linked"] if call.get("trust_linked") else policy["operating"]
    if any(scope not in call["token_scopes"] for scope in branch["scopes"]):
        return _denied(policy["insufficient_scopes"])
    account_id = None
    compensation = None
    if call.get("trust_linked"):
        account_id = call.get("trust_account_id")
        failure = trust_account_failure({**call, "destination": "trust", "trust_account_id": account_id})
        if failure:
            return _denied(failure)
        if call.get("reversal_account_id") not in (None, account_id):
            return _denied("forbidden")
        compensation = _reversal_compensation(call, account_id)
        if compensation is None:
            return _denied("validation_failed")
    entry = branch.get("trust_entry") or {}
    idempotent = bool((compensation or {}).get("idempotent"))
    events: list[dict] = []
    if call.get("trust_linked") and compensation is not None and not idempotent:
        posted = compensation["entry"]
        opening = call.get("opening") if isinstance(call.get("opening"), dict) else {"matter_cents": 0, "client_cents": 0, "account_cents": 0}
        events = trust_ledger_events(
            operation,
            [
                {
                    "amount_cents": posted.get("amount_cents", 0),
                    "opening": opening,
                    "matter_id": posted.get("matter_id"),
                    "client_id": posted.get("client_id"),
                    "trust_account_id": posted.get("trust_account_id"),
                    "trust_entry_id": posted.get("id"),
                    "trust_posted": True,
                }
            ],
            committed=True,
        )
    flag = any(event.get("type") == "trust.flag_raised" for event in events)
    return {
        "result": "original_result" if idempotent else "allowed",
        "payment_mutated": not idempotent,
        "payment_status": "reversed",
        "amount_unchanged": True,
        "ledger_mutated": bool(call.get("trust_linked")) and not idempotent,
        "ledger_append_only": bool(entry.get("append_only", True)) if call.get("trust_linked") else False,
        "original_trust_edited": bool(entry.get("original_edited", False)) if call.get("trust_linked") else False,
        "flag": flag,
        "trust_posted_by": entry.get("posted_by") if call.get("trust_linked") else None,
        "trust_account_id": account_id if call.get("trust_linked") else None,
        "trust_entry": None if compensation is None else compensation["entry"],
        "trust_post": None if compensation is None else compensation["post"],
        "grant": None,
        "events": events,
    }


def _inverted_trust_amount(original: dict) -> int | None:
    """Invert the original trust posting. A deposit is undone by a debit. A transfer to operating is undone by a credit."""
    amount = original.get("amount_cents")
    if not isinstance(amount, int) or amount == 0:
        return None
    kind = original.get("entry_type")
    if kind == "deposit":
        return -abs(amount)
    if kind in {"withdrawal", "transfer_to_operating", "refund"}:
        return abs(amount)
    if kind == "adjustment":
        return -amount
    return None


def _reversal_compensation(call: dict, account_id: str) -> dict | None:
    """Read-only compensation row. The write schema does not accept entry_type reversal."""
    required = ("client_id", "matter_id", "trust_entry_id", "created_by", "posted_entry_id", "created_at")
    if any(call.get(key) in (None, "") for key in required):
        return None
    existing = call.get("existing_reversal")
    if existing:
        if existing.get("entry_type") != "reversal" or existing.get("reverses_entry_id") != call["trust_entry_id"]:
            return None
        if existing.get("amount_cents") in (None, 0):
            return None
        return {"entry": existing, "post": {"entry": existing, "balances": call.get("balances") or {"matter_cents": existing["amount_cents"], "client_cents": existing["amount_cents"], "account_cents": existing["amount_cents"]}, "flags": []}, "idempotent": True}
    original = call.get("original_entry")
    if not isinstance(original, dict) or original.get("id") != call["trust_entry_id"]:
        return None
    if call.get("amount_cents") not in (None, original.get("amount_cents")) and abs(call.get("amount_cents") or 0) != abs(original.get("amount_cents") or 0):
        return None
    signed = _inverted_trust_amount(original)
    if signed is None:
        return None
    direction = "credit" if signed > 0 else "debit"
    entry = {
        "id": call["posted_entry_id"],
        "created_at": call["created_at"],
        "created_by": call["created_by"],
        "trust_account_id": account_id,
        "client_id": call["client_id"],
        "matter_id": call["matter_id"],
        "entry_type": "reversal",
        "amount_cents": signed,
        "direction": direction,
        "method": call.get("method") or "internal_transfer",
        "description": call.get("description") or "Payment reversal",
        "reverses_entry_id": call["trust_entry_id"],
    }
    flags = []
    if call.get("below_zero"):
        flags.append(
            {
                "id": call.get("flag_id") or call["posted_entry_id"],
                "created_at": call["created_at"],
                "created_by": call["created_by"],
                "updated_at": call["created_at"],
                "trust_entry_id": call["posted_entry_id"],
                "client_id": call["client_id"],
                "matter_id": call["matter_id"],
                "balance_cents": call.get("balance_cents", signed),
                "scope": "matter",
                "status": "open",
            }
        )
    post = {
        "entry": entry,
        "balances": {
            "matter_cents": call.get("matter_cents", signed),
            "client_cents": call.get("client_cents", signed),
            "account_cents": call.get("account_cents", signed),
        },
        "flags": flags,
    }
    return {"entry": entry, "post": post}


def _effect_items(call: dict) -> list[dict]:
    if call.get("items"):
        return list(call["items"])
    return [call]


def _item_payment_status(item: dict) -> str:
    """A create or bulk item with no status is the graph's initial pending, not completed."""
    raw = item.get("status") if "status" in item else None
    if raw in (None, ""):
        return PAYMENT_STATUS_GRAPH["initial"]
    return raw


def payment_effect_decision(operation: dict, call: dict) -> dict:
    access = operation["x-togavsu-access"]
    policy = access["payment_effect"]
    if policy.get("authorize_before") != "any_mutation":
        return _denied("forbidden")
    if call["role"] not in access["roles"]:
        return _denied("forbidden")
    posts_trust = False
    for item in _effect_items(call):
        row = {
            "matter_walled": item.get("matter_walled", call.get("matter_walled")),
            "matter_id": item.get("matter_id", call.get("matter_id")),
        }
        if not matter_visible(call, row):
            return _denied("not_found")
        destination = item.get("destination", call.get("destination"))
        status = _item_payment_status(item)
        already = item.get("trust_already_posted", call.get("trust_already_posted", False))
        needs_trust = destination == "trust" and status == "completed" and not already
        needed = policy["trust_destination"]["scopes"] if needs_trust else policy["operating"]["scopes"]
        if any(scope not in call["token_scopes"] for scope in needed):
            return _denied(policy["insufficient_scopes"])
        failure = trust_account_failure({**call, **item, "trust_accounts": call.get("trust_accounts"), "firm_id": call.get("firm_id")})
        if failure:
            return _denied(failure)
        posts_trust = posts_trust or needs_trust
    entry = policy["trust_destination"]["trust_entry"]
    accounts = []
    if posts_trust:
        accounts = [
            item.get("trust_account_id", call.get("trust_account_id"))
            for item in _effect_items(call)
            if item.get("destination", call.get("destination")) == "trust"
            and _item_payment_status(item) == "completed"
            and not item.get("trust_already_posted", call.get("trust_already_posted", False))
        ]
    return {
        "result": "allowed",
        "payment_mutated": True,
        "ledger_mutated": posts_trust,
        "ledger_append_only": bool(entry["append_only"]) if posts_trust else False,
        "flag": False,
        "trust_posted_by": entry["posted_by"] if posts_trust else None,
        "trust_account_ids": accounts,
        "grant": None,
    }


def processor_issuance(policy: dict, module: dict, initialization: dict) -> dict:
    denied = _denied("forbidden")
    if not matter_visible(initialization, initialization, single_record=True):
        return _denied("not_found")
    if not policy.get("registered_module") or not module.get("registered"):
        return _denied(policy["unregistered"])
    recipient = module.get("recipient_id", module.get("module_id"))
    if not module_id_slug(module.get("module_id")) or not module_id_slug(initialization.get("processor_module_id")):
        return _denied("validation_failed")
    if recipient != module.get("module_id") or module.get("module_id") != initialization.get("processor_module_id"):
        return _denied(policy["module_identity_mismatch"])
    key = "trust" if initialization.get("destination") == "trust" else "operating"
    if any(scope not in initialization.get("caller_scopes", []) for scope in policy["caller_scopes"][key]):
        return _denied(policy["missing_scope"])
    if any(scope not in module.get("approved_scopes", []) for scope in policy["module_scopes"][key]):
        return _denied(policy["missing_scope"])
    if not initialization.get("reserved_payment_id"):
        return _denied("validation_failed")
    account_failure = trust_account_failure(initialization)
    if account_failure:
        return _denied(account_failure)
    if policy.get("grants_admin") or policy.get("firmwide_bypass") or policy.get("ambient_payment_write"):
        return denied
    grant = {
        "module_id": module["module_id"],
        "initialization_id": initialization["initialization_id"],
        "payment_id": initialization["reserved_payment_id"],
        "matter_id": initialization["matter_id"],
        "contact_id": initialization["contact_id"],
        "invoice_id": initialization.get("invoice_id"),
        "amount_cents": initialization["amount_cents"],
        "destination": initialization["destination"],
        "trust_account_id": initialization.get("trust_account_id"),
        "allowed_operations": list(policy["sequence"]),
        "allowed_fields": list(policy["allowed_fields"]),
        "expires_at": initialization["expires_at"],
        "revoked": False,
        "issued_by": policy["issued_by"],
    }
    return {"result": "allowed", "grant": grant, "payment_mutated": False, "ledger_mutated": False, "flag": False}


def processor_callback(policy: dict, operation: dict, call: dict) -> str:
    if call.get("token_revoked"):
        return policy["revoked_module_token"]
    if not call.get("registered"):
        return policy["unregistered"]
    grant = call.get("grant")
    if not grant:
        return policy["missing_grant"]
    if grant.get("invalid"):
        return policy["invalid"]
    if grant.get("expired"):
        return policy["expired"]
    if grant.get("revoked"):
        return policy["revoked"]
    if grant.get("issued_by") != policy["issued_by"] or grant.get("module_id") != call.get("caller_module_id"):
        return policy["module_identity_mismatch"]
    if call["operation"] not in policy["sequence"] or call["operation"] not in grant.get("allowed_operations", []):
        return policy["operation_outside_grant"]
    if operation["x-togavsu-access"].get("noninteractive_workflow") != "payment_processor":
        return policy["operation_outside_grant"]
    needed = list(operation["x-togavsu-access"]["scopes"])
    effect = operation["x-togavsu-access"].get("payment_effect")
    if effect and call.get("destination") == "trust" and call.get("status") == "completed":
        needed = list(effect["trust_destination"]["scopes"])
    if any(scope not in call.get("module_scopes", []) for scope in needed):
        return policy["missing_scope"]
    if call.get("matter_id") != grant.get("matter_id"):
        return policy["unrelated_matter"]
    if call.get("contact_id") not in (None, grant.get("contact_id")):
        return policy["unrelated_resource"]
    if "invoice_id" in call and call.get("invoice_id") != grant.get("invoice_id"):
        return policy["unrelated_resource"]
    if call.get("amount_cents") != grant.get("amount_cents"):
        return policy["amount_mismatch"]
    if call.get("destination") != grant.get("destination"):
        return policy["destination_mismatch"]
    reserved = grant.get("payment_id")
    if not reserved:
        return policy["missing_grant"]
    supplied = call.get("payment_id")
    if call["operation"] == "createPayment" and supplied not in (None, reserved):
        return policy["unrelated_payment"]
    if call["operation"] == "updatePaymentStatus" and supplied != reserved:
        return policy["unrelated_payment"]
    if grant.get("trust_account_id") != call.get("trust_account_id") and call.get("trust_account_id") is not None:
        return "forbidden"
    field = call.get("field")
    if field and field not in grant.get("allowed_fields", []):
        return policy["field_outside_grant"]
    return "allowed"


_PROCESSOR_REQUEST_FIELDS = (
    "amount_cents",
    "matter_id",
    "contact_id",
    "destination",
    "invoice_id",
    "trust_account_id",
    "method",
    "reference_number",
    "source",
    "payment_id",
)


def _normalized_processor_request(call: dict) -> dict:
    """Immutable request snapshot. Create status defaults to pending and stays the original request, not the payment's later status."""
    request = {name: call.get(name) for name in _PROCESSOR_REQUEST_FIELDS}
    if call.get("operation") == "updatePaymentStatus":
        request["status"] = call.get("status")
    else:
        request["status"] = call.get("status") or "pending"
    return request


def _same_processor_body(payment: dict, call: dict) -> bool:
    stored = payment.get("accepted_request") or {name: payment.get(name) for name in _PROCESSOR_REQUEST_FIELDS}
    return _normalized_processor_request(call) == stored


def _processor_body(call: dict) -> tuple:
    request = _normalized_processor_request(call)
    return tuple(sorted(request.items()))


def _idempotency_scope(call: dict, init_id: str | None, key: str) -> tuple:
    return (call.get("caller_module_id"), call.get("operation"), init_id, key)


def _store_processor_receipt(store: dict, call: dict, init_id: str | None, body: tuple) -> None:
    key = call.get("idempotency_key")
    if not key or not init_id:
        return
    store["idempotency"][_idempotency_scope(call, init_id, key)] = {"body": body, "initialization_id": init_id}


def _processor_success(store: dict, unchanged: dict, policy: dict, result: str, payment: dict | None, call: dict, init_id: str | None, body: tuple, **extra) -> dict:
    """Record the request fingerprint, then return. Denial and conflict paths do not call this."""
    _store_processor_receipt(store, call, init_id, body)
    return {**unchanged, "result": result, "payment": payment, "ledger_count": len(store["ledger"]), **extra}


def _posted_trust_events(operation: dict, payment: dict, call: dict, inserted: bool) -> list[dict]:
    if not inserted:
        return []
    amount = call.get("amount_cents")
    if isinstance(amount, bool) or not isinstance(amount, int):
        amount = 0
    opening = call.get("opening") if isinstance(call.get("opening"), dict) else {
        "matter_cents": call.get("matter_cents", 0),
        "client_cents": call.get("client_cents", 0),
        "account_cents": call.get("account_cents", 0),
    }
    return trust_ledger_events(
        operation,
        [
            {
                "amount_cents": amount,
                "opening": opening,
                "matter_id": payment.get("matter_id"),
                "client_id": call.get("client_id") or payment.get("contact_id"),
                "trust_account_id": payment.get("trust_account_id"),
                "trust_entry_id": payment.get("id"),
                "trust_posted": True,
            }
        ],
        committed=True,
    )


def processor_apply(store: dict, policy: dict, operation: dict, call: dict) -> dict:
    """Insert at most one payment per initialization. Denial leaves payment and ledger unchanged."""
    store.setdefault("payments", {})
    store.setdefault("ledger", [])
    store.setdefault("idempotency", {})
    unchanged = {"payment_inserted": False, "ledger_inserted": False, "payment": None, "ledger_count": len(store["ledger"]), "events": []}
    grant = call.get("grant") or {}
    init_id = grant.get("initialization_id")
    existing = store["payments"].get(init_id) if init_id else None
    enriched = dict(call)
    if existing and call.get("operation") == "createPayment":
        enriched["bound_payment"] = existing
    code = processor_callback(policy, operation, enriched)
    body = _processor_body(call)
    if code == policy["idempotency_replay"]:
        return _processor_success(store, unchanged, policy, code, existing, call, init_id, body)
    if code != "allowed":
        return {**unchanged, "result": code}
    key = call.get("idempotency_key")
    if key:
        scoped = _idempotency_scope(call, init_id, key)
        prior = store["idempotency"].get(scoped)
        if prior and prior["body"] != body:
            return {**unchanged, "result": policy["idempotency_conflict"]}
        if prior and prior["body"] == body:
            replayed = store["payments"].get(prior["initialization_id"])
            return _processor_success(store, unchanged, policy, policy["idempotency_replay"], replayed, call, init_id, body)
    account_call = {
        "destination": call.get("destination"),
        "trust_account_id": call.get("trust_account_id", grant.get("trust_account_id")),
        "trust_accounts": call.get("trust_accounts"),
        "firm_id": call.get("firm_id"),
        "grant": grant,
    }
    failure = trust_account_failure(account_call)
    if failure:
        return {**unchanged, "result": failure}
    if call.get("operation") == "createPayment":
        request = _normalized_processor_request(call)
        payment = {
            "id": grant["payment_id"],
            "initialization_id": init_id,
            "amount_cents": call.get("amount_cents"),
            "matter_id": call.get("matter_id"),
            "contact_id": call.get("contact_id"),
            "invoice_id": call.get("invoice_id"),
            "destination": call.get("destination"),
            "trust_account_id": grant.get("trust_account_id"),
            "method": call.get("method"),
            "reference_number": call.get("reference_number"),
            "source": call.get("source"),
            "status": _item_payment_status(call),
            "trust_posted": False,
            "accepted_request": request,
        }
        if init_id in store["payments"]:
            winner = store["payments"][init_id]
            if _same_processor_body(winner, call):
                return _processor_success(store, unchanged, policy, policy["idempotency_replay"], winner, call, init_id, body)
            return {**unchanged, "result": "conflict"}
        store["payments"][init_id] = payment
        ledger_inserted = _post_trust_once(store, payment, call)
        return _processor_success(
            store,
            unchanged,
            policy,
            "allowed",
            payment,
            call,
            init_id,
            body,
            payment_inserted=True,
            ledger_inserted=ledger_inserted,
            events=_posted_trust_events(operation, payment, call, ledger_inserted),
        )
    if not existing or call.get("payment_id") != existing["id"]:
        return {**unchanged, "result": policy["unrelated_payment"]}
    requested = call.get("status") if call.get("status") is not None else existing.get("status")
    transition = payment_status_transition(existing.get("status"), requested)
    if transition == policy["idempotency_replay"]:
        return _processor_success(store, unchanged, policy, transition, existing, call, init_id, body)
    if transition != "allowed":
        return {**unchanged, "result": transition, "payment": existing, "ledger_count": len(store["ledger"])}
    existing["status"] = requested
    ledger_inserted = _post_trust_once(store, existing, call)
    return _processor_success(
        store,
        unchanged,
        policy,
        "allowed",
        existing,
        call,
        init_id,
        body,
        payment_inserted=False,
        ledger_inserted=ledger_inserted,
        events=_posted_trust_events(operation, existing, call, ledger_inserted),
    )


def _post_trust_once(store: dict, payment: dict, call: dict) -> bool:
    del call
    if payment.get("destination") != "trust" or payment.get("status") != "completed" or payment.get("trust_posted"):
        return False
    store["ledger"].append(
        {
            "payment_id": payment["id"],
            "trust_account_id": payment.get("trust_account_id"),
            "kind": "deposit",
        }
    )
    payment["trust_posted"] = True
    return True


def commit_concurrent_processor_creates(policy: dict, operation: dict, calls: list[dict]) -> tuple[dict, list[dict]]:
    """Concurrent creates share one empty store. The unique initialization key admits one insert."""
    store = {"payments": {}, "ledger": [], "idempotency": {}}
    results = [processor_apply(store, policy, operation, call) for call in calls]
    return store, results


_FIELD_VALUE_KEY = {
    "text": "text",
    "long_text": "long_text",
    "number": "number",
    "money": "money_cents",
    "date": "date",
    "boolean": "boolean",
    "pick_list": "pick",
    "contact": "contact_id",
}


def custom_fields_outcome(record_type: str, body: dict, definitions: list[dict], *, practice_area_id: str | None = None, visible_contact_ids: set | None = None) -> str:
    supplied = body.get("custom_fields") or []
    by_id = {item["id"]: item for item in definitions}
    seen: set = set()
    for value in supplied:
        definition = by_id.get(value.get("definition_id"))
        if definition is None or definition.get("key") != value.get("key"):
            return "validation_failed"
        if definition.get("retired") or definition.get("record_type") != record_type:
            return "validation_failed"
        areas = definition.get("practice_area_ids") or []
        if areas and practice_area_id not in areas:
            return "validation_failed"
        value_key = _FIELD_VALUE_KEY[definition["field_type"]]
        present = [key for key in _FIELD_VALUE_KEY.values() if key in value]
        if present != [value_key]:
            return "validation_failed"
        if definition["field_type"] == "pick_list" and value.get("pick") not in (definition.get("options") or []):
            return "validation_failed"
        if definition["field_type"] == "contact" and value.get("contact_id") not in (visible_contact_ids or set()):
            return "not_found"
        seen.add(definition["id"])
    for definition in definitions:
        if definition.get("retired") or definition.get("record_type") != record_type or not definition.get("required"):
            continue
        areas = definition.get("practice_area_ids") or []
        if areas and practice_area_id not in areas:
            continue
        if definition["id"] not in seen:
            return "validation_failed"
    return "allowed"


def custom_field_bulk_apply(operation: dict, call: dict, items: list[dict], handlers: dict | None = None) -> dict:
    """Authorize every item and resolve its domain before a write. A replaced time item is not a core write."""
    quiet = {"written": [], "routed": [], "events": []}
    access = operation["x-togavsu-access"]
    if not access.get("resolve_before_write") or access.get("partial_core_write"):
        return {"result": "forbidden", **quiet}
    if call.get("role") not in access.get("roles", []):
        return {"result": "forbidden", **quiet}
    scopes = set(call.get("token_scopes") or [])
    domains = access.get("record_type_domain") or {}
    planned = []
    for item in items:
        record_type = item.get("record_type")
        needed = (access.get("record_type_scopes") or {}).get(record_type)
        if not needed or any(scope not in scopes for scope in needed):
            return {"result": "scope_denied", **quiet}
        if item.get("matter_id") or item.get("matter_walled"):
            if not matter_visible(call, {"matter_id": item.get("matter_id"), "matter_walled": item.get("matter_walled")}):
                return {"result": "not_found", **quiet}
        domain = domains.get(record_type)
        handler = (handlers or {}).get(domain) if domain else None
        if domain == "time":
            if not handler or handler.get("replaced") is not True:
                planned.append({"record_id": item.get("record_id"), "record_type": record_type, "writer": "core"})
                continue
            if not handler.get("available") or not handler.get("module_id"):
                return {"result": "batch_rejected", **quiet}
            planned.append({"record_id": item.get("record_id"), "record_type": record_type, "writer": "module", "module_id": handler["module_id"]})
            continue
        planned.append({"record_id": item.get("record_id"), "record_type": record_type, "writer": "core"})
    if any(item["writer"] == "core" and item["record_type"] in {"time_entry", "expense"} and (handlers or {}).get("time", {}).get("replaced") for item in planned):
        return {"result": "batch_rejected", **quiet}
    return {
        "result": "allowed",
        "written": [item["record_id"] for item in planned if item["writer"] == "core"],
        "routed": [item["record_id"] for item in planned if item["writer"] == "module"],
        "events": ["custom_field.updated"] if planned else [],
    }


def custom_fields_batch(items: list[dict]) -> str:
    """Validate every item before any record or custom value is written."""
    for item in items:
        result = custom_fields_outcome(
            item["record_type"],
            item["body"],
            item["definitions"],
            practice_area_id=item.get("practice_area_id"),
            visible_contact_ids=item.get("visible_contact_ids"),
        )
        if result != "allowed":
            return "batch_rejected"
    return "allowed"


def file_download_decision(operation: dict, call: dict) -> str:
    access = operation["x-togavsu-access"]
    if "file.downloaded" not in operation.get("x-togavsu-events", []):
        return "missing_event"
    if access.get("quarantine_confirmation") == "required" and not access.get("quarantine_confirmation_input"):
        return "missing_confirm_input"
    if call.get("attorney_only") and call.get("role") in ("staff", "client", "accountant"):
        return "not_found"
    if not matter_visible(call, call, single_record=True):
        return "not_found"
    if call.get("role") == "client" and not call.get("shared"):
        return "not_found"
    if call.get("owner") == "other_client":
        return "not_found"
    if call.get("quarantined") and call.get("confirm") is not True:
        return "quarantine_confirmation_required"
    return "highlighted" if call.get("role") == "client" else "audited"


def mcp_credential_rejection(arguments: dict) -> str | None:
    if any(name in arguments for name in ("Authorization", "Host", "roles")):
        return "forbidden"
    headers = arguments.get("headers") or {}
    if any(str(name).lower() in {"authorization", "host"} for name in headers):
        return "forbidden"
    return None


def mcp_tool_dispatch(spec: dict, call: dict) -> dict:
    """Authorize tools/call from the target operation's security before that tool runs."""
    denied = {"effects": False, "public": False}
    arguments = call.get("arguments") or {}
    if mcp_credential_rejection(arguments):
        return {"result": "forbidden", **denied}
    method = call.get("method") or "tools/call"
    if method in {"initialize", "tools/list"}:
        return {"result": "allowed", "effects": False, "public": True}
    operation = next((candidate for _method, _path, candidate in iter_operations(spec) if candidate["operationId"] == call.get("tool")), None)
    if operation is None or operation["x-togavsu-mcp"].get("parity") != "required":
        return {"result": "not_found", **denied}
    public = operation.get("security") == []
    presented_bearer = bool(call.get("authorization"))
    presented_cookie = bool(call.get("cookie"))
    scheme = call.get("authorization_scheme")
    if presented_bearer and scheme not in (None, "Bearer", "bearer"):
        return {"result": "unauthenticated", **denied, "public": public}
    if presented_bearer and presented_cookie:
        return {"result": "ambiguous_auth", **denied, "public": public}
    if (presented_bearer or presented_cookie) and not call.get("credential_valid"):
        return {"result": "unauthenticated", **denied, "public": public}
    if public:
        return {"result": "allowed", "effects": True, "public": True}
    if not presented_bearer and not presented_cookie:
        return {"result": "unauthenticated", **denied}
    return {"result": "allowed", "effects": True, "public": False}


def published_version_events(operation: dict, *, committed: bool, version_count: int = 0) -> list[str]:
    publication = operation["x-togavsu-access"].get("version_publication")
    if not publication:
        return []
    if not committed or version_count <= 0:
        return []
    if publication.get("cardinality") != "one_per_published_version":
        return []
    if publication["event"] not in operation.get("x-togavsu-events", []):
        return []
    return [publication["event"]] * version_count


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


def matter_visible(caller: dict, row: dict, *, single_record: bool = False) -> bool:
    """Membership is the matter id. One on_wall flag does not open every walled row."""
    if not row.get("matter_walled"):
        return True
    matter_id = row.get("matter_id")
    membership = caller.get("matter_membership")
    if isinstance(membership, dict):
        return bool(matter_id) and membership.get(matter_id) is True
    if matter_id and caller.get("matter_id") == matter_id:
        return bool(caller.get("on_wall"))
    if single_record and not matter_id:
        return bool(caller.get("on_wall"))
    return False


def matter_page(caller: dict, rows: list[dict], limit: int) -> dict:
    """Keep rows the viewer can see. The cursor does not name a hidden matter."""
    visible = [row for row in rows if matter_visible(caller, row)]
    page = visible[:limit]
    return {
        "data": [{key: value for key, value in row.items() if key != "matter_walled"} for row in page],
        "total": len(visible),
        "next_cursor": None if len(visible) <= limit else "more",
    }


def authorized_badge_counts(operation: dict, caller: dict, rows: list[dict]) -> dict:
    """Sum sidebar badges from records the viewer can read. Filter before the count."""
    access = operation["x-togavsu-access"]
    if not access.get("matter_wall") or access.get("admin_bypasses_wall") or access.get("badge_counts", {}).get("filter_before") != "count":
        return {"result": "forbidden", "badges": {}}
    totals: dict[str, int] = {}
    for row in rows:
        if not matter_visible(caller, row):
            continue
        if row.get("attorney_only") and caller.get("role") not in ("attorney", "admin"):
            continue
        needed = list(row.get("required_scopes") or [])
        if any(scope not in set(caller.get("scopes") or []) for scope in needed):
            continue
        badge = row.get("badge")
        if not isinstance(badge, str) or not badge:
            continue
        totals[badge] = totals.get(badge, 0) + int(row.get("count") or 0)
    return {"result": "allowed", "badges": totals}


def payment_lifecycle_events(operation: dict, items: list[dict], *, committed: bool, replay: bool = False) -> list[str]:
    """Emit from the resulting status. A replay of the same transition emits nothing."""
    if not committed or replay or operation["x-togavsu-access"].get("payment_events", {}).get("selection") != "resulting_state":
        return []
    catalog = set(operation.get("x-togavsu-events") or [])
    emitted: list[str] = []
    for item in items:
        previous = item.get("previous_status")
        inserted = bool(item.get("inserted", previous is None))
        status = _item_payment_status(item) if inserted else item.get("status")
        if inserted and "payment.created" in catalog:
            emitted.append("payment.created")
        if status == "completed" and previous != "completed" and "payment.completed" in catalog:
            emitted.append("payment.completed")
        elif status == "reversed" and previous != "reversed" and "payment.reversed" in catalog:
            emitted.append("payment.reversed")
        elif not inserted and previous is not None and status != previous and status != "completed" and "payment.updated" in catalog:
            emitted.append("payment.updated")
    return emitted


def _balance_value(value, default: int = 0) -> int:
    if isinstance(value, bool) or not isinstance(value, int):
        return default
    return value


def trust_ledger_events(operation: dict, items: list[dict], *, committed: bool, replay: bool = False, denied: bool = False) -> list[dict]:
    """Post every committed ledger row. Raise trust.flag_raised only when that row's resulting balance is below zero."""
    rule = (operation.get("x-togavsu-access") or {}).get("trust_flag") or {}
    if rule.get("selection") != "resulting_balance" or rule.get("blocks") or rule.get("below_zero") == "blocks":
        return []
    if not committed or replay or denied:
        return []
    catalog = set(operation.get("x-togavsu-events") or [])
    posted_name = rule.get("posted", "trust.entry_posted")
    flag_name = rule.get("flag", "trust.flag_raised")
    matter_bal: dict = {}
    client_bal: dict = {}
    account_bal: dict = {}
    emitted: list[dict] = []
    for item in items:
        if item.get("trust_posted") is False:
            continue
        amount = item.get("amount_cents", 0)
        if isinstance(amount, bool) or not isinstance(amount, int):
            amount = 0
        opening = item.get("opening") if isinstance(item.get("opening"), dict) else {}
        matter_id = item.get("matter_id")
        client_id = item.get("client_id")
        account_id = item.get("trust_account_id")
        matter_key = (account_id, client_id, matter_id)
        client_key = (account_id, client_id)
        account_key = (account_id,)
        if matter_key not in matter_bal:
            matter_bal[matter_key] = _balance_value(opening.get("matter_cents"))
        if client_key not in client_bal:
            client_bal[client_key] = _balance_value(opening.get("client_cents", opening.get("matter_cents")))
        if account_key not in account_bal:
            account_bal[account_key] = _balance_value(opening.get("account_cents", opening.get("matter_cents")))
        matter_bal[matter_key] += amount
        client_bal[client_key] += amount
        account_bal[account_key] += amount
        balances = {
            "matter": matter_bal[matter_key],
            "client": client_bal[client_key],
            "account": account_bal[account_key],
        }
        scope = next((name for name in ("matter", "client", "account") if balances[name] < 0), None)
        body = {
            "type": posted_name,
            "matter_id": matter_id,
            "client_id": client_id,
            "trust_account_id": account_id,
            "balance_cents": balances["matter"],
            "trust_entry_id": item.get("trust_entry_id"),
            "balance_scope": scope or "matter",
        }
        if posted_name in catalog:
            emitted.append(dict(body))
        if scope and flag_name in catalog:
            emitted.append({**body, "type": flag_name, "balance_cents": balances[scope], "balance_scope": scope})
    return emitted


def committed_events(operation: dict, *, committed: bool, count: int = 1) -> list[str]:
    if not committed or operation["operationId"] in EVENT_EXEMPTIONS:
        return []
    events = list(operation.get("x-togavsu-events") or [])
    publication = operation["x-togavsu-access"].get("version_publication")
    if publication and publication.get("cardinality") == "one_per_published_version":
        if count <= 0:
            return []
        expanded = []
        for event in events:
            expanded.extend([event] * count if event == publication["event"] else [event])
        return expanded
    return events


_ATTORNEY_ONLY_RECORDS = {"conflict_record", "conflict_review"}
_EVENT_SCOPE_REGISTRY: dict[str, list[str]] = {}
_CUSTOM_FIELD_READ = {
    "contact": ["contacts:read"],
    "matter": ["matters:read"],
    "time_entry": ["time:read"],
    "expense": ["expenses:read"],
}
_SCOPE_READ_ALIAS = {
    "trust:withdraw": "trust:read",
    "invoices:approve": "invoices:read",
    "invoices:send": "invoices:read",
    "conflicts:decide": "conflicts:read",
    "editor:run": "files:read",
}

EXTENSIBLE_RECORDS = {
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
_HISTORY_REDACTED_FIELDS = ("old_value", "new_value", "summary", "reason", "excerpt")


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


def _row_record_type(row: dict) -> str | None:
    value = row.get("record_type") or row.get("entity_type")
    return value if isinstance(value, str) else None


def _attorney_only_file(row: dict) -> bool:
    """A file hidden from staff and clients. A contact identifier is not this restriction."""
    restriction = row.get("record_restriction")
    if isinstance(restriction, dict) and restriction.get("type") == "file" and restriction.get("visibility") == "attorney_only":
        return True
    return _row_record_type(row) == "file" and row.get("attorney_only") is True


def inherited_history_rows(operation: dict, caller: dict, rows: list[dict]) -> dict:
    rule = operation["x-togavsu-access"].get("inherited_record_access")
    if not rule or rule.get("admin_bypasses_wall") or not operation["x-togavsu-access"].get("matter_wall"):
        return {"result": "forbidden", "rows": []}
    visible = []
    hidden_tokens: set[str] = set()
    for row in rows:
        if _attorney_only_file(row) and caller.get("role") not in ("attorney", "admin"):
            for key in ("id", "record_id", "entity_id", "title", "link", "name"):
                if isinstance(row.get(key), str):
                    hidden_tokens.add(row[key])
            continue
        if not matter_visible(caller, row):
            continue
        needed = list(row.get("required_scopes") or [])
        if any(scope not in set(caller.get("scopes") or []) for scope in needed):
            continue
        copied = {
            key: value
            for key, value in row.items()
            if key
            not in {
                "matter_walled",
                "required_scopes",
                "attorney_only",
                "predelete",
                "context",
                "asserted_recipient_user_id",
                "deleted",
                "record_restriction",
            }
        }
        if not _history_owner_visible(caller, row):
            continue
        if row.get("attorney_only") and caller.get("role") not in ("attorney", "admin") and not _attorney_only_file(row):
            for field in _HISTORY_REDACTED_FIELDS:
                if field in copied and copied[field] is not None:
                    copied[field] = {"redacted": True}
            for spec in rule.get("field_redactions") or []:
                if _row_record_type(row) != spec["record_type"]:
                    continue
                for field in spec["fields"]:
                    if field in copied and copied[field] is not None:
                        copied[field] = {"redacted": True}
        visible.append(copied)
    cursor = caller.get("next_cursor")
    if not isinstance(cursor, str) or cursor in hidden_tokens:
        cursor = None
    return {"result": "allowed", "rows": visible, "total": len(visible), "next_cursor": cursor}


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


_GRANT_FIELDS = ("task_authorization", "processor_authorization", "background_file_authorization")


def _capability_bound(module: dict, event: dict) -> bool:
    """A grant-bearing webhook is serialized only for the module named on that grant."""
    recipient = module.get("module_id")
    for key, value in event.items():
        grant_like = key in _GRANT_FIELDS or key in {"capability", "grant"} or key.endswith("_authorization")
        if not grant_like or value is None:
            continue
        if key not in _GRANT_FIELDS or not isinstance(value, dict) or value.get("module_id") != recipient:
            return False
    return True


def doc_tender_events(operation: dict, phase: str) -> list[str]:
    """Retry scheduling and a later failed attempt do not emit doc_tender.published."""
    rule = operation["x-togavsu-access"].get("doc_tender_retry")
    if not rule:
        return []
    scheduled = [event for event in operation.get("x-togavsu-events") or [] if event != rule["published_event"]]
    if phase == "retry_accepted":
        return scheduled
    if phase == "attempt_failed":
        return []
    if phase == "results_published":
        return [rule["published_event"]]
    return []


def issue_api_token(operation: dict, call: dict) -> dict:
    """Only an admin issues a token, and the new scopes cannot exceed the issuing credential or the owner."""
    denied = {"result": "forbidden", "token": None, "secret": None}
    access = operation["x-togavsu-access"]
    if access.get("issuance") != "admin_only" or access.get("bearer_amplification"):
        return denied
    if call.get("caller_module_id") or call.get("role") not in access.get("roles", []):
        return denied
    requested = list(call.get("scopes") or [])
    if not requested:
        return {"result": "validation_failed", "token": None, "secret": None}
    effective = set(call.get("token_scopes") or []) if call.get("credential") == "bearer" else set(call.get("caller_scopes") or [])
    if any(scope not in effective for scope in requested):
        return {"result": "scope_denied", "token": None, "secret": None}
    owner = call.get("owner_user_id")
    module_id = call.get("module_id")
    if bool(owner) == bool(module_id) or (module_id is not None and not module_id_slug(module_id)):
        return {"result": "validation_failed", "token": None, "secret": None}
    if owner:
        if any(scope not in set(call.get("owner_role_scopes") or []) for scope in requested):
            return {"result": "scope_denied", "token": None, "secret": None}
    else:
        if any(scope not in set(call.get("module_approved_scopes") or []) for scope in requested):
            return {"result": "scope_denied", "token": None, "secret": None}
    return {
        "result": "allowed",
        "token": {
            "name": call.get("name"),
            "owner_user_id": owner,
            "module_id": module_id,
            "scopes": requested,
            "revoked": False,
        },
        "secret": "issued-once",
    }


def module_help_registration(operation: dict, call: dict) -> str:
    """help:write on an installed module registers pages under that module's heading only."""
    principal = operation["x-togavsu-access"].get("module_principal") or {}
    if not principal.get("own_heading_only") or principal.get("grants_admin") or principal.get("scope") != "help:write":
        return "forbidden"
    if call.get("actor") != "module" or call.get("human_session"):
        return "forbidden"
    if "help:write" not in set(call.get("scopes") or []):
        return "scope_denied"
    if not module_id_slug(call.get("module_id")) or not module_id_slug(call.get("path_module_id")):
        return "forbidden"
    if call.get("target") == "firm" or call.get("module_id") != call.get("path_module_id"):
        return "forbidden"
    if call.get("heading") not in (None, call.get("module_id")):
        return "forbidden"
    return "allowed"


def _marker_label(value) -> str | None:
    if not isinstance(value, str):
        return None
    text = " ".join(value.split())
    if not text or len(text) > 40:
        return None
    if any(ord(char) < 32 or char in "<>\"\\" for char in text):
        return None
    compact = "".join(text.casefold().split())
    if any(token in compact for token in ("javascript:", "vbscript:", "data:", "http:", "https:")):
        return None
    return text


def _marker_color(value) -> str | None:
    if not isinstance(value, str) or len(value) != 7 or not value.startswith("#"):
        return None
    if any(char not in "0123456789abcdefABCDEF" for char in value[1:]):
        return None
    return "#" + value[1:].lower()


def _marker_label_taken(definitions: list[dict], module_id: str, label: str, except_id: str | None) -> bool:
    key = " ".join(label.casefold().split())
    for row in definitions:
        if row.get("id") == except_id or not row.get("active") or row.get("module_id") != module_id:
            continue
        if " ".join(str(row.get("label", "")).casefold().split()) == key:
            return True
    return False


def _marker_row(call: dict, module_id: str, marker_id: str, label: str, color: str, target: str | None, active: bool) -> dict:
    stamp = call.get("created_at") or "2026-11-07T18:00:00Z"
    return {
        "id": marker_id,
        "created_at": call.get("created_at") or stamp,
        "created_by": call.get("created_by") or "00000000-0000-0000-0000-000000000000",
        "updated_at": call.get("updated_at") or stamp,
        "label": label,
        "color": color,
        "active": active,
        "module_id": module_id,
        "namespace": module_id,
        "target": target,
        "placement": "matter-header",
    }


def module_marker_registration(operation: dict, store: dict, call: dict) -> dict:
    """Register, update, or revoke a marker owned by the signed module principal."""
    quiet = {"definition": None, "events": [], "mutated": False}
    principal = (operation.get("x-togavsu-access") or {}).get("module_principal") or {}
    if (
        principal.get("scope") != "markers:write"
        or not principal.get("own_namespace_only")
        or principal.get("grants_admin")
        or principal.get("ownership") != "signed_module_principal"
    ):
        return {"result": "forbidden", **quiet}
    if call.get("actor") != "module" or call.get("human_session") or call.get("role") in {"admin", "attorney", "staff", "accountant", "client"}:
        return {"result": "forbidden", **quiet}
    if "markers:write" not in set(call.get("scopes") or []):
        return {"result": "scope_denied", **quiet}
    module_id = call.get("module_id")
    if not module_id_slug(module_id) or not module_id_slug(call.get("path_module_id")) or module_id != call.get("path_module_id"):
        return {"result": "forbidden", **quiet}
    if call.get("owner_module_id") not in (None, module_id):
        return {"result": "forbidden", **quiet}
    action = call.get("action")
    if action not in {"create", "update", "revoke"}:
        return {"result": "validation_failed", **quiet}
    definitions = store.setdefault("definitions", [])
    current = None
    if action in {"update", "revoke"}:
        current = next((row for row in definitions if row.get("id") == call.get("marker_id")), None)
        if current is None:
            return {"result": "not_found", **quiet}
        if current.get("module_id") != module_id:
            return {"result": "forbidden", **quiet}
    label = color = target = None
    if action == "create":
        label = _marker_label(call.get("label"))
        color = _marker_color(call.get("color"))
        if label is None or color is None or not isinstance(call.get("marker_id"), str) or _UUID_TEXT.fullmatch(call["marker_id"]) is None:
            return {"result": "validation_failed", **quiet}
        if call.get("target") not in (None, ""):
            target = normalize_module_target(module_id, call.get("target"))
            if target is None:
                return {"result": "validation_failed", **quiet}
        if _marker_label_taken(definitions, module_id, label, None):
            return {"result": "conflict", **quiet}
    elif action == "update":
        if not current.get("active"):
            return {"result": "conflict", **quiet}
        label = current["label"]
        color = current["color"]
        target = current.get("target")
        if "label" in call:
            label = _marker_label(call.get("label"))
            if label is None:
                return {"result": "validation_failed", **quiet}
        if "color" in call:
            color = _marker_color(call.get("color"))
            if color is None:
                return {"result": "validation_failed", **quiet}
        if "target" in call and call.get("target") not in (None, ""):
            target = normalize_module_target(module_id, call.get("target"))
            if target is None:
                return {"result": "validation_failed", **quiet}
        if _marker_label_taken(definitions, module_id, label, current.get("id")):
            return {"result": "conflict", **quiet}
    if call.get("replay") or not call.get("committed", True):
        return {"result": "original_result" if call.get("replay") else "rolled_back", **quiet}
    seen = store.setdefault("idempotency", {})
    key = call.get("idempotency_key")
    if key and key in seen:
        return {"result": "original_result", **quiet}
    if action == "revoke":
        if not current.get("active"):
            return {"result": "original_result", **quiet}
        current["active"] = False
        current["updated_at"] = call.get("updated_at") or current.get("updated_at")
        definition = current
        event = "marker.revoked"
    elif action == "update":
        current["label"] = label
        current["color"] = color
        current["target"] = target
        current["updated_at"] = call.get("updated_at") or current.get("updated_at")
        definition = current
        event = "marker.updated"
    else:
        definition = _marker_row(call, module_id, call["marker_id"], label, color, target, True)
        definitions.append(definition)
        event = "marker.created"
    if key:
        seen[key] = definition["id"]
    return {
        "result": "allowed",
        "definition": definition,
        "events": [{"type": event, "marker_id": definition["id"], "module_id": module_id}],
        "mutated": True,
    }


def attach_matter_marker(store: dict, call: dict) -> dict:
    """Pin an active marker to a matter. The wall is the matter on this call."""
    quiet = {"attachment": None, "events": [], "mutated": False}
    if call.get("role") not in {"admin", "attorney", "staff"}:
        return {"result": "forbidden", **quiet}
    scopes = set(call.get("scopes") or call.get("token_scopes") or [])
    if "matters:write" not in scopes:
        return {"result": "scope_denied", **quiet}
    if not call.get("matter_id"):
        return {"result": "validation_failed", **quiet}
    if not matter_visible(call, call, single_record=True):
        return {"result": "not_found", **quiet}
    marker = next((row for row in store.get("definitions") or [] if row.get("id") == call.get("marker_id")), None)
    if marker is None:
        return {"result": "not_found", **quiet}
    if not marker.get("active"):
        return {"result": "validation_failed", **quiet}
    if call.get("replay") or not call.get("committed", True):
        return {"result": "original_result" if call.get("replay") else "rolled_back", **quiet}
    pins = store.setdefault("attachments", [])
    for pin in pins:
        if pin.get("matter_id") == call.get("matter_id") and pin.get("marker_id") == marker["id"]:
            return {"result": "original_result", **quiet}
    pin = {"matter_id": call["matter_id"], "marker_id": marker["id"]}
    pins.append(pin)
    return {"result": "allowed", "attachment": pin, "events": ["matter.updated"], "mutated": True}


def webhook_signature(secret: bytes | str, body: bytes | str) -> str:
    """Canonical X-Togavsu-Signature: sha256= plus lowercase hex HMAC-SHA256 of the raw body bytes."""
    key = secret.encode("utf-8") if isinstance(secret, str) else secret
    payload = body.encode("utf-8") if isinstance(body, str) else body
    return "sha256=" + hmac.new(key, payload, hashlib.sha256).hexdigest()


def webhook_signature_matches(secret: bytes | str, body: bytes | str, header: str) -> bool:
    if not isinstance(header, str) or not header.startswith("sha256=") or len(header) != len("sha256=") + 64:
        return False
    return hmac.compare_digest(webhook_signature(secret, body), header)


def event_read_scopes(event_type: str, record_type: str | None = None) -> list[str]:
    """Fail closed. An unmapped restricted event does not fall back to events:read."""
    if event_type.startswith("custom_field."):
        return list(_CUSTOM_FIELD_READ.get(record_type or "", []))
    return list(_EVENT_SCOPE_REGISTRY.get(event_type, []))


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


def _event_name(record: dict) -> str:
    for key in ("type", "event_type", "action"):
        value = record.get(key)
        if isinstance(value, str) and "." in value:
            return value
    return ""


def _stored_subject(record: dict, rule: dict) -> dict | None:
    """Tagged owner from core metadata. A user id on the event is not a module owner."""
    deleted = _event_name(record).endswith(".deleted") or record.get("deleted") is True
    source = record.get("predelete") if deleted else record.get("context")
    if not isinstance(source, dict):
        return None
    tagged = source.get("owner")
    if isinstance(tagged, dict) and tagged.get("type") in {"user", "module"} and isinstance(tagged.get("id"), str) and tagged["id"]:
        return {"type": tagged["type"], "id": tagged["id"]}
    field = "recipient_user_id" if rule.get("binding") == "recipient" else "owner_user_id"
    value = source.get(field)
    if isinstance(value, str) and value:
        return {"type": "user", "id": value}
    return None


def _principal_can_read(rule: dict, principal: dict, record: dict) -> bool:
    if principal.get("principal_revoked") or principal.get("revoked") or principal.get("grant_revoked"):
        return False
    stored = _stored_subject(record, rule)
    if not stored:
        return False
    if stored["type"] == "module":
        module_id = principal.get("module_id")
        return isinstance(module_id, str) and module_id == stored["id"]
    if stored["type"] != "user":
        return False
    viewer = principal.get("principal_user_id") or principal.get("user_id")
    if not isinstance(viewer, str) or not viewer:
        return False
    if viewer == stored["id"]:
        return True
    role = principal.get("principal_role") or principal.get("role")
    return bool(rule.get("admin_bypass") and role == "admin")


def _history_owner_visible(caller: dict, row: dict) -> bool:
    rule = _EVENT_OWNERS.get(_event_name(row))
    if not rule:
        return True
    return _principal_can_read(rule, caller, row)


def webhook_envelope(module: dict, event: dict) -> dict | None:
    """Serialize only after the underlying read policy. Denial returns no id and no summary."""
    if not module.get("installed") or not module.get("registered"):
        return None
    if event.get("type") not in module.get("subscriptions", []):
        return None
    if not _capability_bound(module, event):
        return None
    needed = event_read_scopes(event["type"], event.get("record_type"))
    approved = set(module.get("approved_scopes") or [])
    if not needed or approved.isdisjoint(needed):
        return None
    if not matter_visible(module, event):
        return None
    attorney_only_record = (
        event.get("attorney_only")
        or event.get("attorney_only_record")
        or event.get("record_type") in _ATTORNEY_ONLY_RECORDS
    )
    if attorney_only_record and not module.get("attorney_only"):
        return None
    owner_rule = _EVENT_OWNERS.get(event.get("type") or "")
    if owner_rule and not _principal_can_read(owner_rule, module, event):
        return None
    if event.get("summary_attorney_only"):
        summary = {"redacted": True}
    elif isinstance(event.get("summary"), str):
        summary = event["summary"]
    else:
        return None
    if event.get("type") == "trust.flag_raised" and any(event.get(key) is None for key in _FLAG_METADATA):
        return None
    body = {
        "id": event["id"],
        "type": event["type"],
        "occurred_at": event["occurred_at"],
        "actor_kind": event.get("actor_kind", "system"),
        "record_type": event["record_type"],
        "record_id": event["record_id"],
        "matter_id": event.get("matter_id"),
        "summary": summary,
        "task_authorization": event.get("task_authorization"),
        "processor_authorization": event.get("processor_authorization"),
        "background_file_authorization": event.get("background_file_authorization"),
    }
    if event.get("type") == "trust.flag_raised":
        for key in _FLAG_METADATA:
            body[key] = event.get(key)
    return body


def background_grant_issuance(operation: dict, module: dict, job: dict) -> dict:
    """Issue a pre-upload grant from the published operation. A direct helper is not the contract."""
    denied = {"result": "forbidden", "grant": None, "file_created": False, "events": [], "webhook": None}
    access = operation.get("x-togavsu-access") or {}
    if access.get("grant_issuance") != "background_file_publication":
        return denied
    if operation.get("operationId") != "issueFilePublicationGrant":
        return denied
    policy = BACKGROUND_FILE_PUBLICATION
    if not module.get("installed") or not module.get("registered") or not policy.get("registered_module"):
        return {**denied, "result": policy["unregistered"]}
    if not module_id_slug(module.get("module_id")) or not module_id_slug(job.get("module_id")):
        return {**denied, "result": "validation_failed"}
    if module.get("module_id") != job.get("module_id"):
        return {**denied, "result": policy["wrong_module"]}
    if job.get("source") not in policy["sources"]:
        return denied
    if any(scope not in module.get("approved_scopes", []) for scope in policy["scopes"]):
        return {**denied, "result": policy["missing_scope"]}
    if job.get("task_authorization") or job.get("existing_grant"):
        return denied
    if access.get("authorization_category") != "service_principal_issuance":
        return denied
    if access.get("module_delegation") != "service_principal":
        return denied
    credentials = ((operation.get("x-togavsu-mcp") or {}).get("transfer") or {}).get("credentials") or {}
    bearer = credentials.get("module_bearer") or {}
    if bearer.get("viewer_required") or bearer.get("task_grant_required") or "task_authorization" in credentials or "context_token" in credentials:
        return denied
    if not job.get("expires_at"):
        return {**denied, "result": "validation_failed"}
    assignment = job.get("assignment") or ("matter" if job.get("matter_id") else None)
    if assignment == "unassigned":
        if job.get("matter_id"):
            return {**denied, "result": "validation_failed"}
        matter_id = None
    elif assignment == "matter":
        if not job.get("matter_id"):
            return {**denied, "result": "validation_failed"}
        if not matter_visible(job, job, single_record=True):
            return {**denied, "result": policy["wrong_matter"]}
        principal_matters = job.get("principal_matter_ids")
        if principal_matters is not None and job.get("matter_id") not in principal_matters:
            return {**denied, "result": policy["wrong_matter"]}
        matter_id = job["matter_id"]
    else:
        return {**denied, "result": "validation_failed"}
    if policy.get("grants_admin") or policy.get("firmwide_bypass") or access.get("grants_admin") or access.get("firmwide_bypass"):
        return denied
    grant = {
        "module_id": module["module_id"],
        "assignment": assignment,
        "matter_id": matter_id,
        "source": job["source"],
        "allowed_operations": list(policy["sequence"]),
        "expires_at": job["expires_at"],
        "revoked": False,
        "issued_by": policy["issued_by"],
        "upload_ids": [],
        "expired": False,
        "tenant_id": module.get("tenant_id") or "firm",
    }
    if assignment == "unassigned":
        if job.get("contact_id"):
            grant["contact_id"] = job["contact_id"]
        if job.get("queue"):
            grant["queue"] = job["queue"]
    event = {
        "id": job.get("event_id") or "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbbb",
        "type": "file.background_authorized",
        "occurred_at": job.get("occurred_at") or job["expires_at"],
        "actor_kind": "module",
        "record_type": "file" if assignment == "unassigned" else "matter",
        "record_id": matter_id or module["module_id"],
        "matter_id": matter_id,
        "summary": "Publication grant issued",
        "background_file_authorization": grant,
    }
    return {
        "result": "allowed",
        "grant": grant,
        "file_created": False,
        "events": list(operation.get("x-togavsu-events") or ["file.background_authorized"]),
        "webhook_event": event,
    }


def _module_upload_document(upload_id: str, grant: dict, status: str) -> dict:
    return {
        "id": upload_id,
        "owner": {"owner_kind": "module", "owner_module_id": grant["module_id"]},
        "chunk_size_bytes": 8388608,
        "next_offset": 0,
        "size_bytes": 0,
        "expires_at": grant["expires_at"],
        "status": status,
    }


def _background_denied(store: dict, before_files: int, before_events: int, result: str) -> dict:
    if len(store["files"]) != before_files or len(store["events"]) != before_events:
        raise RuntimeError("background file denial changed state")
    return {"result": result, "file_created": False, "events": []}


def background_file_step(store: dict, policy: dict, operation: dict, call: dict) -> dict:
    store.setdefault("uploads", {})
    store.setdefault("files", {})
    store.setdefault("events", [])
    before_files = len(store["files"])
    before_events = len(store["events"])
    grant = call.get("grant")
    if not grant:
        return _background_denied(store, before_files, before_events, "forbidden")
    if grant.get("expired"):
        return _background_denied(store, before_files, before_events, policy["expired"])
    if grant.get("revoked"):
        return _background_denied(store, before_files, before_events, policy["revoked"])
    if grant.get("issued_by") != policy["issued_by"] or grant.get("module_id") != call.get("caller_module_id"):
        return _background_denied(store, before_files, before_events, policy["wrong_module"])
    if any(scope not in call.get("module_scopes", []) for scope in policy["scopes"]):
        return _background_denied(store, before_files, before_events, policy["missing_scope"])
    if call.get("operation") not in grant.get("allowed_operations", []):
        return _background_denied(store, before_files, before_events, policy["operation_outside_grant"])
    if call.get("matter_id") != grant.get("matter_id"):
        return _background_denied(store, before_files, before_events, policy["wrong_matter"])
    operation_id = call["operation"]
    upload_id = call.get("upload_id")
    if operation_id != "createFileUpload" and operation_id != "bulkFile" and upload_id not in grant.get("upload_ids", []):
        return _background_denied(store, before_files, before_events, "not_found")
    if operation_id == "createFileUpload":
        upload_id = upload_id or f"up-{len(store['uploads']) + 1}"
        document = _module_upload_document(upload_id, grant, "receiving")
        store["uploads"][upload_id] = {
            "id": upload_id,
            "status": "receiving",
            "matter_id": grant.get("matter_id"),
            "owner": grant["module_id"],
            "document": document,
        }
        grant["upload_ids"].append(upload_id)
        emitted = committed_events(operation, committed=True)
        store["events"].extend(emitted)
        return {"result": "allowed", "upload_id": upload_id, "upload": document, "file_created": False, "events": emitted}
    if operation_id == "putFileChunk":
        store["uploads"][upload_id]["status"] = "receiving"
        store["uploads"][upload_id]["document"]["status"] = "receiving"
        return {"result": "allowed", "upload": store["uploads"][upload_id]["document"], "file_created": False, "events": []}
    if operation_id == "getFileUpload":
        return {"result": "allowed", "upload": store["uploads"][upload_id]["document"], "file_created": False, "events": []}
    if operation_id == "completeFileUpload":
        store["uploads"][upload_id]["status"] = "staged"
        store["uploads"][upload_id]["document"]["status"] = "staged"
        emitted = committed_events(operation, committed=True)
        store["events"].extend(emitted)
        return {"result": "allowed", "upload": store["uploads"][upload_id]["document"], "file_created": False, "events": emitted}
    if operation_id == "publishFile":
        if grant.get("assignment") == "unassigned" and call.get("assign_matter_id"):
            return _background_denied(store, before_files, before_events, "forbidden")
        upload = store["uploads"].get(upload_id)
        if not upload or upload.get("owner") != grant.get("module_id") or upload.get("matter_id") != grant.get("matter_id"):
            return _background_denied(store, before_files, before_events, "not_found")
        if upload.get("status") != "staged":
            return _background_denied(store, before_files, before_events, "validation_failed")
        if upload_id in store["files"]:
            return {"result": "original_result", "file_created": False, "events": [], "file": store["files"][upload_id]}
        file_row = {"id": f"file-{upload_id}", "matter_id": grant["matter_id"], "upload_id": upload_id}
        store["files"][upload_id] = file_row
        emitted = committed_events(operation, committed=True, count=1)
        store["events"].extend(emitted)
        return {"result": "allowed", "file_created": True, "events": emitted, "file": file_row}
    if operation_id == "bulkFile":
        items = call.get("items") or []
        seen_ids: set[str] = set()
        duplicate = False
        for item in items:
            upload_id = item.get("upload_id")
            if upload_id in seen_ids:
                duplicate = True
            if upload_id:
                seen_ids.add(upload_id)
        if duplicate:
            return _background_denied(store, before_files, before_events, "validation_failed")
        problems = [_bulk_upload_problem(store, grant, item) for item in items] or ["validation_failed"]
        if any(problems):
            return _background_denied(store, before_files, before_events, next(problem for problem in problems if problem))
        created = []
        replayed = []
        for item in items:
            key = item["upload_id"]
            if key in store["files"]:
                replayed.append(store["files"][key])
                continue
            row = {"id": f"file-{key}", "matter_id": grant["matter_id"], "upload_id": key}
            store["files"][key] = row
            created.append(row)
        emitted = committed_events(operation, committed=True, count=len(created))
        store["events"].extend(emitted)
        if not created:
            return {"result": "original_result", "file_created": False, "events": [], "files": replayed}
        return {"result": "allowed", "file_created": True, "events": emitted, "files": created + replayed}
    if operation_id == "reviewUnassignedFile":
        if grant.get("assignment") != "unassigned":
            return _background_denied(store, before_files, before_events, "forbidden")
        upload = store["uploads"].get(upload_id)
        published = store["files"].get(upload_id)
        if not upload or upload.get("owner") != grant.get("module_id") or not published or published.get("matter_id") is not None:
            return _background_denied(store, before_files, before_events, "not_found")
        return {
            "result": "allowed",
            "file_created": False,
            "events": [],
            "file": published,
            "matching": {"status": "unassigned", "matter_id": None},
        }
    return _background_denied(store, before_files, before_events, policy["operation_outside_grant"])


def _bulk_upload_problem(store: dict, grant: dict, item: dict) -> str | None:
    upload_id = item.get("upload_id")
    if not upload_id or item.get("matter_id") != grant.get("matter_id"):
        return "not_found" if item.get("matter_id") not in (None, grant.get("matter_id")) else "validation_failed"
    if upload_id not in grant.get("upload_ids", []):
        return "not_found"
    upload = store.get("uploads", {}).get(upload_id)
    if not upload:
        return "not_found"
    if upload.get("owner") != grant.get("module_id"):
        return "forbidden"
    if upload.get("matter_id") != grant.get("matter_id"):
        return "not_found"
    if upload.get("status") != "staged":
        return "validation_failed"
    return None


def commit_concurrent_publishes(policy: dict, operation: dict, grant: dict, upload_id: str) -> dict:
    store = {
        "uploads": {
            upload_id: {
                "id": upload_id,
                "status": "staged",
                "matter_id": grant["matter_id"],
                "owner": grant["module_id"],
            }
        },
        "files": {},
        "events": [],
    }
    grant = {**grant, "upload_ids": [upload_id]}
    call = {
        "operation": "publishFile",
        "grant": grant,
        "caller_module_id": grant["module_id"],
        "module_scopes": ["files:write"],
        "matter_id": grant["matter_id"],
        "upload_id": upload_id,
    }
    first = background_file_step(store, policy, operation, call)
    second = background_file_step(store, policy, operation, call)
    return {"store": store, "first": first, "second": second}


def notification_recipient_decision(operation: dict, call: dict) -> str:
    """Reads and read-state changes are the caller's own rows. Creation may name another recipient."""
    access = operation["x-togavsu-access"]
    if access.get("creation_separate") or access.get("recipient") == "named_on_create":
        if call.get("role") not in access.get("roles", []):
            return "forbidden"
        return "allowed"
    if access.get("owner") != "self" or access.get("admin_sees_others"):
        return "forbidden"
    if call.get("role") not in access.get("roles_bound", []):
        return "forbidden"
    if call.get("owner") != "self":
        return "not_found"
    if call.get("recipient_user_id") not in (None, call.get("caller_user_id")):
        return "not_found"
    if not matter_visible(call, call, single_record=True):
        return "not_found"
    if call.get("attorney_only") and call.get("role") not in ("attorney", "admin"):
        return "not_found"
    return "allowed"


def notification_page(operation: dict, caller: dict, rows: list[dict], limit: int) -> dict:
    """Drop other users' rows before the page and the unread count."""
    if notification_recipient_decision(operation, {**caller, "owner": "self", "recipient_user_id": caller.get("user_id")}) != "allowed":
        return {"data": [], "unread_count": 0, "total": 0}
    own = []
    for row in rows:
        if row.get("user_id") != caller.get("user_id"):
            continue
        if not matter_visible(caller, row):
            continue
        visible = notification_recipient_decision(
            operation,
            {
                **caller,
                "owner": "self",
                "caller_user_id": caller.get("user_id"),
                "recipient_user_id": row.get("user_id"),
                "matter_walled": False,
                "attorney_only": row.get("attorney_only"),
            },
        )
        if visible != "allowed":
            continue
        own.append(row)
    return {
        "data": own[:limit],
        "unread_count": sum(1 for row in own if not row.get("read_at")),
        "total": len(own),
    }


def payment_status_transition(current: str | None, requested: str | None) -> str:
    """Same status replays. Pending may complete or fail. Terminal states do not regress."""
    if requested in (None, current):
        return PAYMENT_STATUS_GRAPH["same_status"]
    allowed = PAYMENT_STATUS_GRAPH["transitions"].get(current or "", [])
    if requested in allowed:
        return "allowed"
    return PAYMENT_STATUS_GRAPH["illegal_transition"]


def module_id_slug(value) -> bool:
    """True only for the path slug. Traversal and confusable characters are rejected, not rewritten."""
    return isinstance(value, str) and _MODULE_ID_SLUG.fullmatch(value) is not None


def normalize_module_target(module_id: str, target: str) -> str | None:
    """Module-local path. Schemes, absolute URLs, traversal, and other modules' routes are refused."""
    if not module_id_slug(module_id) or not isinstance(target, str):
        return None
    lowered = target.strip().lower()
    if lowered.startswith(("javascript:", "data:", "http:", "https:", "file:", "ftp:", "net:")):
        return None
    if "\\" in target or target.startswith(("/", "//")) or ".." in target.split("/"):
        return None
    parts = target.split("/")
    if any(part in ("", ".", "..") for part in parts):
        return None
    for part in parts:
        if part.lower() != part:
            return None
        if not part or part[0] == "-" or not all(char.isalnum() or char == "-" for char in part):
            return None
        if not part.isascii() or not part[0].isalnum():
            return None
    return f"/m/{module_id}/{target}"


def sidebar_entries(module_id: str, entries: list[dict], caller: dict) -> dict:
    """Two entries open two pages. Render and the target both require the entry's roles and scopes."""
    seen: set[str] = set()
    opened = []
    for entry in entries:
        entry_id = entry.get("id")
        if not entry_id or entry_id in seen:
            return {"result": "validation_failed", "pages": []}
        seen.add(entry_id)
        path = normalize_module_target(module_id, entry.get("target"))
        if not path:
            return {"result": "validation_failed", "pages": []}
        roles = set(entry.get("roles") or [])
        scopes = set(entry.get("scopes") or [])
        if caller.get("role") not in roles or not scopes <= set(caller.get("scopes") or []):
            continue
        opened.append({"id": entry_id, "path": path})
    paths = [page["path"] for page in opened]
    if len(paths) != len(set(paths)):
        return {"result": "validation_failed", "pages": []}
    return {"result": "allowed", "pages": opened}


module_ui_targets = sidebar_entries


def _caller_can_open(caller: dict, roles: list, scopes: list) -> bool:
    if caller.get("role") not in set(roles):
        return False
    return set(scopes) <= set(caller.get("scopes") or [])


def _matter_filter_hidden(filters: dict, caller: dict) -> bool:
    matter_id = filters.get("matter_id")
    if not matter_id:
        return False
    return not matter_visible(caller, {"matter_walled": True, "matter_id": matter_id}, single_record=True)


def open_widget_target(module_id: str, target: dict, caller: dict) -> dict:
    """Open a widget target. Core pages use the destination operation's role, scope, and wall."""
    denied = {"result": "validation_failed", "path": None, "route": None, "operation": None, "filters": {}}
    if not isinstance(target, dict):
        return denied
    filters = target.get("filters") or {}
    if not isinstance(filters, dict):
        return denied
    kind = target.get("kind")
    if kind == "module":
        path = normalize_module_target(module_id, target.get("path"))
        if not path:
            return denied
        if not _caller_can_open(caller, target.get("roles") or [], target.get("scopes") or []):
            return {**denied, "result": "forbidden"}
        if _matter_filter_hidden(filters, caller):
            return {**denied, "result": "not_found"}
        return {"result": "allowed", "path": path, "route": None, "operation": None, "filters": filters}
    if kind == "core":
        page = CORE_PAGE_TARGETS.get(target.get("route"))
        if page is None:
            return denied
        if not _caller_can_open(caller, page["roles"], page["scopes"]):
            return {**denied, "result": "forbidden"}
        if page["matter_wall"] and _matter_filter_hidden(filters, caller):
            return {**denied, "result": "not_found"}
        return {"result": "allowed", "path": None, "route": target["route"], "operation": page["operation"], "filters": filters}
    return denied


_MARKDOWN_TAGS = frozenset({"p", "br", "strong", "em", "code", "pre", "ul", "ol", "li", "blockquote", "h1", "h2", "h3", "a"})
_MARKDOWN_DROP = frozenset({"script", "style", "iframe", "object", "embed", "svg", "math", "link", "meta", "form"})
_VOID_TAGS = frozenset({"br"})


def _safe_href(value: str | None) -> str | None:
    if not isinstance(value, str) or not value.strip():
        return None
    text = html.unescape(value)
    text = "".join(ch for ch in text if ch.isprintable() and ch not in "\t\n\r")
    text = text.strip()
    if text.startswith("//"):
        return None
    compact = "".join(text.lower().split())
    for scheme in ("javascript:", "vbscript:", "data:", "file:"):
        if scheme in compact:
            return None
    head = compact.split("/", 1)[0]
    if ":" in head and not compact.startswith(("http://", "https://", "mailto:")):
        return None
    return text


class _MarkdownHTML(HTMLParser):
    """Allowlist rebuild. This models guide rendering. It is not UI acceptance."""

    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.parts: list[str] = []
        self._drop = 0

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        self._open(tag, attrs, close=False)

    def handle_startendtag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        self._open(tag, attrs, close=True)

    def _open(self, tag: str, attrs: list[tuple[str, str | None]], *, close: bool) -> None:
        name = tag.lower()
        if name in _MARKDOWN_DROP:
            if not close:
                self._drop += 1
            return
        if self._drop or name not in _MARKDOWN_TAGS:
            return
        if name == "a":
            href = _safe_href(dict(attrs).get("href"))
            if href:
                self.parts.append(f'<a href="{html.escape(href, quote=True)}">')
            else:
                self.parts.append("<a>")
            if close:
                self.parts.append("</a>")
            return
        if name in _VOID_TAGS:
            self.parts.append(f"<{name}>")
            return
        self.parts.append(f"<{name}>")
        if close:
            self.parts.append(f"</{name}>")

    def handle_endtag(self, tag: str) -> None:
        name = tag.lower()
        if name in _MARKDOWN_DROP and self._drop:
            self._drop -= 1
            return
        if self._drop or name not in _MARKDOWN_TAGS or name in _VOID_TAGS:
            return
        self.parts.append(f"</{name}>")

    def handle_data(self, data: str) -> None:
        if self._drop or not data:
            return
        self.parts.append(html.escape(data))

    def handle_comment(self, data: str) -> None:
        return

    def handle_decl(self, decl: str) -> None:
        return


def rendered_markdown(source: str) -> str:
    """Rebuild guide HTML from an allowlist. Handlers, scripts, and unsafe URLs are not emitted."""
    if not isinstance(source, str) or not source:
        return ""
    parser = _MarkdownHTML()
    parser.feed(source)
    parser.close()
    return "".join(parser.parts)


def _unique_ids(items: list) -> set[str] | None:
    seen: set[str] = set()
    for item in items:
        if not isinstance(item, dict):
            return None
        ident = item.get("id")
        if not isinstance(ident, str) or not ident or ident in seen:
            return None
        seen.add(ident)
    return seen


def manifest_contributions(manifest: dict, caller: dict | None = None) -> dict:
    """Resolve help pages and tabs against this module's own contributions. Clients receive no guide."""
    module_id = manifest.get("id") if module_id_slug(manifest.get("id")) else ""
    empty = {"result": "validation_failed", "namespace": module_id, "heading": None, "pages": [], "tabs": []}
    if not module_id:
        return empty
    widgets = manifest.get("widgets") or []
    tabs = manifest.get("tabs") or []
    screen = manifest.get("matter_screen") or {}
    panels = screen.get("panels") or [] if isinstance(screen, dict) else []
    actions = screen.get("actions") or [] if isinstance(screen, dict) else []
    widget_ids = _unique_ids(widgets)
    tab_ids = _unique_ids(tabs)
    screen_ids = _unique_ids([*panels, *actions])
    if widget_ids is None or tab_ids is None or screen_ids is None:
        return empty
    opened_tabs = []
    for tab in tabs:
        if tab.get("placement") not in ("home-screen", "matter-screen"):
            return empty
        path = normalize_module_target(module_id, tab.get("target"))
        if not path:
            return empty
        opened_tabs.append({"id": tab["id"], "placement": tab["placement"], "path": path, "guide": tab.get("guide")})
    heading = manifest.get("name") if isinstance(manifest.get("name"), str) and manifest.get("name") else module_id
    help_doc = manifest.get("help")
    pages = []
    page_ids: set[str] = set()
    if help_doc is not None:
        if not isinstance(help_doc, dict) or not help_doc.get("pages"):
            return empty
        if isinstance(help_doc.get("heading"), str) and help_doc["heading"].strip():
            heading = help_doc["heading"]
        found = _unique_ids(help_doc["pages"])
        if found is None:
            return empty
        page_ids = found
        for page in help_doc["pages"]:
            if not page.get("title") or not isinstance(page.get("markdown"), str) or not page["markdown"].strip():
                return empty
            covers = page.get("covers") or []
            if not covers:
                return empty
            for cover in covers:
                kind = cover.get("kind") if isinstance(cover, dict) else None
                ident = cover.get("id") if isinstance(cover, dict) else None
                if kind == "widget" and ident in widget_ids:
                    continue
                if kind == "tab" and ident in tab_ids:
                    continue
                if kind == "screen" and ident in screen_ids:
                    continue
                return empty
            pages.append(
                {
                    "id": page["id"],
                    "title": page["title"],
                    "markdown": rendered_markdown(page["markdown"]),
                    "keywords": list(page.get("keywords") or []),
                    "covers": covers,
                    "namespace": module_id,
                }
            )
    for tab in opened_tabs:
        if tab["guide"] is not None and tab["guide"] not in page_ids:
            return empty
    if caller and caller.get("role") == "client":
        return {"result": "not_found", "namespace": module_id, "heading": heading, "pages": [], "tabs": []}
    return {"result": "allowed", "namespace": module_id, "heading": heading, "pages": pages, "tabs": opened_tabs}


_CUSTOM_FIELD_TYPES = frozenset({"text", "long_text", "number", "money", "date", "boolean", "pick_list", "contact"})
_CUSTOM_FIELD_RECORDS = frozenset({"matter", "contact", "time_entry", "expense"})


def manifest_custom_fields(manifest: dict, claimed_module_id: str | None = None) -> dict:
    """Custom fields belong to the signed manifest id. A caller-supplied module id cannot reassign them."""
    owner = manifest.get("id") if module_id_slug(manifest.get("id")) else ""
    empty = {"result": "validation_failed", "module_id": owner, "fields": []}
    if not owner:
        return empty
    if claimed_module_id not in (None, owner):
        return empty
    data = manifest.get("data")
    if data is None:
        return {"result": "allowed", "module_id": owner, "fields": []}
    if not isinstance(data, dict):
        return empty
    declared = data.get("custom_fields") or []
    if not isinstance(declared, list):
        return empty
    seen: set[str] = set()
    fields = []
    for field in declared:
        if not isinstance(field, dict):
            return empty
        if field.get("record_type") not in _CUSTOM_FIELD_RECORDS or field.get("field_type") not in _CUSTOM_FIELD_TYPES:
            return empty
        key = field.get("key")
        if not isinstance(key, str) or not key.startswith(owner + "."):
            return empty
        local = key[len(owner) + 1 :]
        if not local or local != local.lower() or not local[0].isalpha() or not all(char.isalnum() or char == "_" for char in local):
            return empty
        if key in seen:
            return empty
        seen.add(key)
        options = field.get("options")
        if field["field_type"] == "pick_list":
            if not isinstance(options, list) or not options or len(options) != len(set(options)):
                return empty
            if any(not isinstance(option, str) or not option.strip() for option in options):
                return empty
        elif options not in (None, []):
            return empty
        areas = field.get("practice_area_ids") or []
        if not isinstance(areas, list):
            return empty
        fields.append(
            {
                "module_id": owner,
                "record_type": field["record_type"],
                "key": key,
                "label": field.get("label"),
                "field_type": field["field_type"],
                "required": bool(field.get("required", False)),
                "sort_order": field.get("sort_order", 0),
                "options": list(options or []),
                "practice_area_ids": list(areas),
            }
        )
    return {"result": "allowed", "module_id": owner, "fields": fields}


_VIEW_SCOPE = re.compile(r"^views:(" + MODULE_ID_PATTERN[1:-1] + r")\.([a-z][a-z0-9_]{0,62}):read$")


def _registered_view_names(manifest: dict | None, published_views: list[str] | None) -> set[str]:
    names = {name for name in (published_views or []) if isinstance(name, str)}
    if not manifest:
        return names
    module_id = manifest.get("id") if module_id_slug(manifest.get("id")) else ""
    data = manifest.get("data") if isinstance(manifest.get("data"), dict) else {}
    for view in data.get("views") or []:
        if isinstance(view, dict) and isinstance(view.get("name"), str) and module_id:
            names.add(f"{module_id}.{view['name']}")
    return names


def _scope_is_granted(scope: str, *, delegable: set[str], registered_views: set[str]) -> bool:
    if scope in delegable:
        return True
    match = _VIEW_SCOPE.fullmatch(scope)
    if not match or "*" in scope or "?" in scope:
        return False
    return f"{match.group(1)}.{match.group(2)}" in registered_views


def normalize_manifest_permissions(permissions, *, signature_verified: bool, published_views: list[str] | None = None, manifest: dict | None = None) -> dict:
    """Strings are required-true shorthand. Normalization runs only after the signature verifies."""
    if not signature_verified:
        return {"result": "unsigned_module", "permissions": []}
    from contract.model import DELEGABLE_SCOPES

    registered = _registered_view_names(manifest, published_views)
    normalized = []
    seen: dict[str, bool] = {}
    for item in permissions or []:
        if isinstance(item, str):
            scope, required = item, True
        elif isinstance(item, dict) and isinstance(item.get("scope"), str) and "required" not in item:
            scope, required = item["scope"], True
        elif isinstance(item, dict) and isinstance(item.get("scope"), str) and isinstance(item.get("required"), bool):
            scope, required = item["scope"], item["required"]
        else:
            return {"result": "validation_failed", "permissions": []}
        if scope in seen or not _scope_is_granted(scope, delegable=set(DELEGABLE_SCOPES), registered_views=registered):
            return {"result": "validation_failed", "permissions": []}
        seen[scope] = required
        normalized.append({"scope": scope, "required": required})
    return {"result": "allowed", "permissions": normalized}


def decide_module_permissions(
    manifest: dict,
    decisions: list[dict] | None,
    *,
    signature_verified: bool,
    previous: dict | None = None,
    published_views: list[str] | None = None,
) -> dict:
    """Required comes from the signed manifest. A required flag on the approval request is ignored."""
    parsed = normalize_manifest_permissions(
        manifest.get("permissions"),
        signature_verified=signature_verified,
        published_views=published_views,
        manifest=manifest,
    )
    version = manifest.get("version")
    kept = previous.get("version") if previous else None
    if parsed["result"] != "allowed":
        return {"result": parsed["result"], "outcome": parsed["result"], "token_scopes": [], "permissions": [], "version": kept, "previous_version": kept}
    approved: set[str] = set()
    declined: set[str] = set()
    for decision in decisions or []:
        if not isinstance(decision, dict):
            continue
        scope = decision.get("scope")
        choice = decision.get("decision")
        if choice == "approve":
            approved.add(scope)
        elif choice == "decline":
            declined.add(scope)
    rows = parsed["permissions"]
    requested = [row["scope"] for row in rows]
    active_scopes = [scope for scope in (previous or {}).get("approved_scopes") or [] if scope not in declined]
    if any(row["required"] and row["scope"] in declined for row in rows):
        listed = [{**row, "approved": False} for row in rows]
        return {
            "result": "permission_declined",
            "outcome": "cancelled",
            "token_scopes": active_scopes if previous else [],
            "active_scopes": active_scopes if previous else [],
            "permissions": listed,
            "version": kept,
            "previous_version": kept,
        }
    pending = [scope for scope in requested if scope not in active_scopes and scope not in approved and scope not in declined]
    if previous and pending:
        listed = [{**row, "approved": row["scope"] in active_scopes} for row in rows]
        return {
            "result": "awaiting_approval",
            "outcome": "awaiting_approval",
            "token_scopes": active_scopes,
            "active_scopes": active_scopes,
            "proposed_scopes": requested,
            "permissions": listed,
            "version": kept,
            "previous_version": kept,
        }
    if previous is None and any(row["required"] and row["scope"] not in approved for row in rows):
        listed = [{**row, "approved": False} for row in rows]
        return {"result": "permission_declined", "outcome": "cancelled", "token_scopes": [], "permissions": listed, "version": kept, "previous_version": kept}
    token = [scope for scope in requested if scope not in declined and (scope in active_scopes or scope in approved)]
    listed = [{**row, "approved": row["scope"] in token} for row in rows]
    return {"result": "installed", "outcome": "installed", "token_scopes": token, "permissions": listed, "version": version, "previous_version": kept}


def replace_matter_wall(operation: dict, call: dict) -> dict:
    """Check the current wall before replacing it. Denial leaves the membership and emits nothing."""
    access = operation["x-togavsu-access"]
    current = call.get("current_wall")
    unchanged = {"mutated": False, "event": None, "wall": current}
    if not access.get("matter_wall") or access.get("admin_bypasses_wall"):
        return {"result": "forbidden", **unchanged}
    role = call.get("viewer_role", call.get("role"))
    if role not in access.get("roles", []):
        return {"result": "forbidden", **unchanged}
    scopes = call.get("module_scopes") if call.get("caller_module_id") else call.get("token_scopes")
    if scopes is not None and any(scope not in scopes for scope in access.get("scopes", [])):
        return {"result": "scope_denied", **unchanged}
    if not matter_visible(call, call, single_record=True):
        return {"result": "not_found", **unchanged}
    user_ids = call.get("user_ids")
    if not isinstance(user_ids, list):
        return {"result": "validation_failed", **unchanged}
    return {
        "result": "allowed",
        "mutated": True,
        "event": "matter.wall_changed",
        "wall": {"matter_id": call.get("matter_id"), "user_ids": list(user_ids)},
    }


def sidebar_panel(module_id: str, panel, caller: dict | None = None) -> dict:
    """One module panel may sit under the sidebar top band. Its target stays module-local."""
    if panel is None:
        return {"result": "allowed", "path": None}
    if isinstance(panel, list):
        if len(panel) != 1:
            return {"result": "validation_failed", "path": None}
        panel = panel[0]
    if not isinstance(panel, dict) or panel.get("slot") != "top-band":
        return {"result": "validation_failed", "path": None}
    path = normalize_module_target(module_id, panel.get("target"))
    if not path:
        return {"result": "validation_failed", "path": None}
    if caller is not None:
        roles = set(panel.get("roles") or [])
        scopes = set(panel.get("scopes") or [])
        if caller.get("role") not in roles or not scopes <= set(caller.get("scopes") or []):
            return {"result": "forbidden", "path": None}
    return {"result": "allowed", "path": path}


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


def external_effect_decision(operation: dict, call: dict) -> dict:
    """Refuse the provider, the log, and the byte copy before they start. HTTP and MCP share this result."""
    access = operation["x-togavsu-access"]
    effects = list(access.get("external_effects") or [])
    silent = {name: False for name in effects}
    denied = {"effects": silent, "provider_invoked": False, "log_written": False, "mutated": False}
    if call.get("role") not in access.get("roles", []):
        return {"result": "forbidden", **denied}
    held = set(call.get("token_scopes") or [])
    if any(scope not in held for scope in access.get("scopes") or []):
        return {"result": "scope_denied", **denied}
    matter_id = call.get("matter_id")
    scope = call.get("scope")
    if operation["operationId"] == "createStorageMove":
        if scope == "matter" and not matter_id:
            return {"result": "validation_failed", **denied}
        if scope == "all" and matter_id:
            return {"result": "validation_failed", **denied}
        needs_wall = scope == "matter"
    else:
        needs_wall = bool(matter_id)
    if needs_wall and not matter_visible(call, call, single_record=True):
        return {"result": "not_found", **denied}
    if not access.get("matter_wall") or access.get("authorize_before") != "external_effect":
        return {"result": "forbidden", **denied}
    return {
        "result": "allowed",
        "effects": {name: True for name in effects},
        "provider_invoked": "provider_invocation" in effects,
        "log_written": "log_write" in effects,
        "mutated": True,
    }


def _attorney_only_fields(access: dict) -> list[str]:
    value = access.get("attorney_only")
    if isinstance(value, list) and all(isinstance(item, str) for item in value):
        return list(value)
    return []


def _hides_attorney_only_record(access: dict) -> bool:
    """A boolean or an exclusion hides the record. A field list does not."""
    value = access.get("attorney_only")
    if isinstance(value, list) or value in (None, False, "redacted"):
        return False
    return value in (True, "excluded", "not_found")


def include_views_decision(operation: dict, call: dict) -> str:
    """include= is the approved view scope, not a bypass of role or wall."""
    access = operation["x-togavsu-access"]
    if "include_scope_pattern" not in access:
        return "forbidden"
    if call.get("role") not in access.get("roles", []):
        return "forbidden"
    if access.get("matter_wall") and not matter_visible(call, call, single_record=True):
        return "not_found"
    if _hides_attorney_only_record(access) and call.get("role") not in ("attorney", "admin"):
        return "not_found"
    approved = set(call.get("approved_scopes") or [])
    if "current_approved_scopes" in call:
        approved &= set(call.get("current_approved_scopes") or [])
    for view in call.get("include") or []:
        if view.count(".") != 1:
            return "validation_failed"
        if "*" in view or "?" in view:
            return "validation_failed"
        module_id, name = view.split(".", 1)
        if not module_id_slug(module_id) or not name:
            return "validation_failed"
        key = f"{module_id}.{name}"
        extensions = set(call.get("extensions") or [])
        published = set(call.get("published_views") or [])
        # The owning module, or a viewer who can already read the core record, may include a declared table that has no published view.
        if key in extensions and call.get("caller_module_id") in (None, module_id):
            continue
        if key in extensions and key not in published:
            return "forbidden"
        needed = f"views:{module_id}.{name}:read"
        # Section 9.9: another module may include a published view only after the admin approves this scope.
        if needed not in approved:
            return "forbidden"
    return "allowed"


def project_record(operation: dict, caller: dict, record: dict) -> dict:
    """Redact attorney-only fields. Hide the record only when the record itself is attorney-only."""
    access = operation["x-togavsu-access"]
    hidden = {"result": "not_found", "record": None, "redacted_fields": []}
    if caller.get("role") not in access.get("roles", []):
        return {"result": "forbidden", "record": None, "redacted_fields": []}
    if access.get("matter_wall") and not matter_visible(caller, {**record, "matter_walled": record.get("matter_walled") or caller.get("matter_walled"), "matter_id": record.get("matter_id") or caller.get("matter_id")}, single_record=True):
        return hidden
    record_hidden = record.get("attorney_only") is True and (
        access.get("attorney_only_files") == "not_found" or _hides_attorney_only_record(access)
    )
    if record_hidden and caller.get("role") not in ("attorney", "admin"):
        return hidden
    copied = dict(record)
    redacted: list[str] = []
    if caller.get("role") not in ("attorney", "admin"):
        for field in _attorney_only_fields(access):
            if field in copied:
                copied[field] = {"redacted": True}
                redacted.append(field)
        if caller.get("role") == "client" and access.get("client_fields"):
            copied = {key: copied[key] for key in access["client_fields"] if key in copied}
    return {"result": "allowed", "record": copied, "redacted_fields": redacted}


def start_totp_reset(operation: dict, call: dict) -> dict:
    """Begin a reset without retiring the current secret. The challenge token is single-use and expires."""
    denied = {"result": "forbidden", "old_secret_active": True, "activated": False, "challenge": None}
    access = operation["x-togavsu-access"]
    if call.get("role") not in access.get("roles", []) or call.get("owner") not in (None, "self"):
        return denied
    if call.get("user_id") not in (None, call.get("caller_user_id")):
        return {"result": "not_found", "old_secret_active": True, "activated": False, "challenge": None}
    return {
        "result": "allowed",
        "old_secret_active": True,
        "activated": False,
        "challenge": {
            "status": "totp_reset_pending",
            "enrollment_token": call.get("issued_token") or "reset-token",
            "expires_at": call.get("expires_at"),
            "otpauth_uri": "otpauth://totp/togavsu",
            "secret": "NEWSECRET",
            "user_id": call.get("caller_user_id"),
            "used": False,
        },
    }


def confirm_totp_reset(operation: dict, call: dict, challenge: dict | None) -> dict:
    """Activate the new secret only after the owner's code. A bad code or a replay leaves the current secret in place."""
    access = operation["x-togavsu-access"]
    kept = {"old_secret_active": True, "activated": False, "challenge": challenge}
    if call.get("role") not in access.get("roles", []) or call.get("owner") not in (None, "self"):
        return {"result": "forbidden", **kept}
    if not challenge or challenge.get("user_id") != call.get("caller_user_id") or challenge.get("enrollment_token") != call.get("enrollment_token"):
        return {"result": "forbidden", **kept}
    if challenge.get("used") or call.get("expired"):
        return {"result": "forbidden", **kept}
    if call.get("code") != call.get("expected_code"):
        return {"result": "unauthenticated", **kept}
    spent = {**challenge, "used": True}
    return {"result": "allowed", "old_secret_active": False, "activated": True, "challenge": spent}


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


def trust_debit_decision(call: dict) -> dict:
    """Select the client and trust account before any ledger post. Denial changes nothing."""
    denied = {"ledger_mutated": False, "debited_account_id": None, "amount_cents": 0, "events": []}
    if call.get("role") not in ("admin", "attorney"):
        return {"result": "forbidden", **denied}
    if not matter_visible(call, call, single_record=True):
        return {"result": "not_found", **denied}
    if "trust:withdraw" not in call.get("token_scopes", []):
        return {"result": "scope_denied", **denied}
    if not call.get("client_id") or not call.get("trust_account_id"):
        return {"result": "validation_failed", **denied}
    matches = [
        row
        for row in call.get("balances") or []
        if row.get("trust_account_id") == call["trust_account_id"]
        and row.get("client_id") == call["client_id"]
        and row.get("matter_id") == call.get("matter_id")
    ]
    if not matches:
        return {"result": "not_found", **denied}
    row = matches[0]
    if row.get("firm_id") and call.get("firm_id") and row["firm_id"] != call["firm_id"]:
        return {"result": "not_found", **denied}
    if row.get("active") is False or row.get("authorized") is False:
        return {"result": "forbidden", **denied}
    amount = call.get("amount_cents")
    signed = -abs(amount) if isinstance(amount, int) and not isinstance(amount, bool) else 0
    resulting = call.get("balance_after", 0)
    if isinstance(resulting, bool) or not isinstance(resulting, int):
        resulting = 0
    events = trust_ledger_events(
        {
            "operationId": "trustDebit",
            "x-togavsu-events": ["trust.entry_posted", "trust.flag_raised"],
            "x-togavsu-access": {"trust_flag": _TRUST_FLAG},
        },
        [
            {
                "amount_cents": signed,
                "opening": {"matter_cents": resulting - signed, "client_cents": resulting - signed, "account_cents": resulting - signed},
                "matter_id": call.get("matter_id"),
                "client_id": call.get("client_id"),
                "trust_account_id": row["trust_account_id"],
                "trust_entry_id": call.get("trust_entry_id"),
                "trust_posted": True,
            }
        ],
        committed=True,
    )
    return {
        "result": "allowed",
        "ledger_mutated": True,
        "debited_account_id": row["trust_account_id"],
        "client_id": row["client_id"],
        "amount_cents": call["amount_cents"],
        "below_zero": resulting < 0,
        "events": events,
    }


def _exact_cents(value) -> int | None:
    if isinstance(value, bool) or not isinstance(value, int):
        return None
    return value


def _iso_day(value) -> date | None:
    if not isinstance(value, str) or len(value) != 10:
        return None
    try:
        return date.fromisoformat(value)
    except ValueError:
        return None


def _draft_modes(call: dict) -> tuple[str | None, str | None]:
    """Return the one selector, or an error. A date range does not need a matter id."""
    one = call.get("matter_id") not in (None, "")
    many = isinstance(call.get("matter_ids"), list) and len(call.get("matter_ids") or []) > 0
    has_from = call.get("from_date") not in (None, "")
    has_to = call.get("to_date") not in (None, "")
    if "matter_ids" in call and call.get("matter_ids") is not None and not many:
        return None, "selector"
    if (has_from or has_to) and not (has_from and has_to):
        return None, "date_order"
    dates = has_from and has_to
    selected = [name for name, present in (("matter_id", one), ("matter_ids", many), ("date_range", dates)) if present]
    if len(selected) != 1:
        return None, "selector"
    if dates:
        start = _iso_day(call.get("from_date"))
        end = _iso_day(call.get("to_date"))
        if start is None or end is None or start > end:
            return None, "date_order"
    return selected[0], None


def _matter_billable(matter: dict, start: date | None, end: date | None) -> bool:
    if start is None or end is None:
        return True
    days = list(matter.get("unbilled_on") or [])
    if matter.get("fee_on"):
        days.append(matter["fee_on"])
    parsed = [_iso_day(day) for day in days]
    return any(day is not None and start <= day <= end for day in parsed)


def invoice_draft_decision(operation: dict, call: dict, matters: list[dict]) -> dict:
    """Draft one selector. A date range keeps only matters this caller can bill."""
    quiet = {"drafted": [], "reason": None}
    access = operation["x-togavsu-access"]
    if call.get("role") not in access.get("roles", []):
        return {"result": "forbidden", **quiet}
    if "invoices:write" not in set(call.get("token_scopes") or []):
        return {"result": "scope_denied", **quiet}
    mode, reason = _draft_modes(call)
    if mode is None:
        return {"result": "validation_failed", "reason": reason, "drafted": []}
    start = _iso_day(call.get("from_date")) if mode == "date_range" else None
    end = _iso_day(call.get("to_date")) if mode == "date_range" else None
    catalog = {row.get("matter_id"): row for row in matters}
    if mode == "matter_id":
        wanted = [call.get("matter_id")]
    elif mode == "matter_ids":
        wanted = list(call.get("matter_ids") or [])
    else:
        wanted = [row.get("matter_id") for row in matters if _matter_billable(row, start, end)]
    drafted = []
    for matter_id in wanted:
        row = catalog.get(matter_id)
        if row is None or not matter_visible(call, row):
            if mode == "date_range":
                continue
            return {"result": "not_found", "reason": "matter", "drafted": []}
        if mode == "date_range" and not _matter_billable(row, start, end):
            continue
        if matter_id not in drafted:
            drafted.append(matter_id)
    return {"result": "allowed", "reason": mode, "drafted": drafted}


def trust_flag_rows(caller: dict, flags: list[dict], accounts: list[dict], query: dict | None = None) -> dict:
    """Account flags stay on a current authorized account at this firm. Other scopes keep the matter wall."""
    if caller.get("role") not in {"admin", "attorney", "staff", "accountant"}:
        return {"result": "forbidden", "rows": [], "total": 0}
    if "trust:read" not in set(caller.get("token_scopes") or caller.get("scopes") or []):
        return {"result": "scope_denied", "rows": [], "total": 0}
    query = query or {}
    current = {
        row.get("id"): row
        for row in accounts
        if row.get("active") is True and row.get("authorized") is True and row.get("firm_id") == caller.get("firm_id")
    }
    visible = []
    for flag in flags:
        scope = flag.get("scope")
        if query.get("flag_scope") not in (None, scope):
            continue
        if query.get("matter_id") not in (None, flag.get("matter_id")):
            continue
        if query.get("trust_account_id") not in (None, flag.get("trust_account_id")):
            continue
        if query.get("status") not in (None, flag.get("status")):
            continue
        if scope == "account":
            account = current.get(flag.get("trust_account_id"))
            if account is None:
                continue
        elif scope == "matter":
            if not matter_visible(caller, flag):
                continue
        elif scope != "client":
            continue
        visible.append({key: value for key, value in flag.items() if key != "matter_walled"})
    return {"result": "allowed", "rows": visible, "total": len(visible)}


def invoice_reconciliation_error(item: dict) -> str | None:
    """None when line sums and the balance identity hold. Otherwise the batch cannot start."""
    lines = item.get("lines")
    if not isinstance(lines, list) or not lines:
        return "validation_failed"
    amounts = [_exact_cents(line.get("amount_cents")) for line in lines if isinstance(line, dict)]
    if len(amounts) != len(lines) or any(amount is None for amount in amounts):
        return "validation_failed"
    subtotal = _exact_cents(item.get("subtotal_cents"))
    adjustment = _exact_cents(item.get("adjustment_cents"))
    interest = _exact_cents(item.get("interest_cents"))
    total = _exact_cents(item.get("total_cents"))
    paid = _exact_cents(item.get("amount_paid_cents"))
    write_off = _exact_cents(item.get("write_off_cents"))
    balance = _exact_cents(item.get("balance_due_cents"))
    if None in (subtotal, adjustment, interest, total, paid, write_off, balance):
        return "validation_failed"
    if interest < 0 or paid < 0 or write_off < 0:
        return "validation_failed"
    if subtotal != sum(amounts) or total != subtotal + adjustment + interest or balance != total - paid - write_off:
        return "validation_failed"
    return None


def _invoice_off_wall(item: dict, call: dict) -> bool:
    """Each invoice names its matter. A call-wide on_wall flag does not admit the rest of the batch."""
    return not matter_visible(
        call,
        {
            "matter_walled": item.get("matter_walled", call.get("matter_walled")),
            "matter_id": item.get("matter_id", call.get("matter_id")),
        },
    )


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


def bulk_privilege_expansions(spec: dict) -> list[str]:
    """Bulk roles or scopes that exceed the individual write. Empty means the bulk is no wider."""
    found = []
    paired = {"bulkTrustEntry": "postTrustEntry", "bulkInvoice": "createInvoiceDrafts", "bulkFile": "publishFile", "bulkEvent": "createEvent"}
    for _method, _path, operation in iter_operations(spec):
        name = operation["operationId"]
        if not name.startswith("bulk"):
            continue
        if name == "bulkCustomFieldValue":
            continue
        individual_name = paired.get(name, "create" + name[len("bulk") :])
        individual = next((candidate for _method, _path, candidate in iter_operations(spec) if candidate["operationId"] == individual_name), None)
        if individual is None:
            found.append(name)
            continue
        bulk_access = operation["x-togavsu-access"]
        individual_access = individual["x-togavsu-access"]
        if set(bulk_access.get("roles") or []) - set(individual_access.get("roles") or []):
            found.append(name + " roles")
        if set(individual_access.get("scopes") or []) - set(bulk_access.get("scopes") or []):
            found.append(name + " scopes")
    return found


def _privilege_denial(rule: dict, call: dict) -> str | None:
    if call.get("role") not in rule["roles"]:
        return "forbidden"
    if any(scope not in set(call.get("token_scopes") or []) for scope in rule["scopes"]):
        return "scope_denied"
    return None


def _invoice_item_denial(access: dict, item: dict, call: dict) -> str | None:
    privileges = access.get("item_privileges") or {}
    rule = privileges.get(item.get("status"))
    if not rule:
        return "validation_failed"
    denial = _privilege_denial(rule, call)
    if denial:
        return denial
    write_off = _exact_cents(item.get("write_off_cents"))
    paid = _exact_cents(item.get("amount_paid_cents"))
    if write_off is None or paid is None:
        return "validation_failed"
    if write_off > 0:
        denial = _privilege_denial(privileges["write_off"], call)
        if denial:
            return denial
    if paid > 0:
        denial = _privilege_denial(privileges["amount_paid"], call)
        if denial:
            return denial
    return None


def project_invoice_for_role(operation: dict, role: str, invoice: dict) -> dict:
    """Client JSON, CSV, and MCP keep InvoiceClient fields. Anything else is dropped."""
    if role != "client":
        return dict(invoice)
    allow = operation["x-togavsu-access"].get("client_fields") or []
    return {key: invoice[key] for key in allow if key in invoice}


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
        return {"default": specific, "minimum": setting["minimum"], "maximum": specific_cap, "setting": override}
    return {"default": default, "minimum": minimum, "maximum": cap, "setting": "api.page_size"}


def page_size_decision(operation: dict, requested, current: dict | None = None) -> dict:
    """The next request uses the current settings. A size above the accepted maximum is refused."""
    named = operation["x-togavsu-access"].get("page_size_setting")
    override = named if named not in (None, "api.page_size") else None
    bounds = page_limit(current, override=override)
    if requested is None:
        return {"result": "allowed", "limit": bounds["default"]}
    if isinstance(requested, bool) or not isinstance(requested, int) or requested < bounds["minimum"] or requested > bounds["maximum"]:
        return {"result": "validation_failed", "limit": None}
    return {"result": "allowed", "limit": requested}


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
                "maximum": bounds["maximum"],
                "default": bounds["default"],
            }
            if override:
                parameter["description"] = (
                    f"Page size. The default is {override} (starting value {bounds['default']}). "
                    f"The maximum is {bounds['maximum']}. The next request uses an admin change, and the size cannot exceed that setting's maximum."
                )
                operation["x-togavsu-access"]["page_size_setting"] = override
            else:
                parameter["description"] = (
                    "Page size. The default is api.page_size and the maximum is api.max_page_size. "
                    "The effective default is the smaller of the two. The next request uses an admin change."
                )
                operation["x-togavsu-access"]["page_size_setting"] = "api.page_size"
                operation["x-togavsu-access"]["page_size_cap_setting"] = "api.max_page_size"


def marketplace_package(catalog: dict, call: dict) -> dict:
    """A listing id and version resolve to one signed package. Nothing here is executed."""
    empty = {"package": None}
    if call.get("network_error"):
        return {"result": "unavailable", **empty}
    listing = (catalog.get("listings") or {}).get(call.get("listing_id"))
    if not listing:
        return {"result": "not_found", **empty}
    version = call.get("version")
    if listing.get("pulled") or version in set(listing.get("yanked_versions") or []):
        return {"result": "yanked", **empty}
    artifact = next((item for item in listing.get("versions") or [] if item.get("version") == version), None)
    if not artifact:
        return {"result": "not_found", **empty}
    if artifact.get("digest_ok") is False or (call.get("digest") and call.get("digest") != artifact.get("digest")):
        return {"result": "digest_mismatch", **empty}
    if artifact.get("signature_ok") is False:
        return {"result": "signature_invalid", **empty}
    if artifact.get("key_id") not in set(catalog.get("trusted_key_ids") or []):
        return {"result": "untrusted_key", **empty}
    return {
        "result": "verified",
        "package": {
            "listing_id": call["listing_id"],
            "version": version,
            "immutable": True,
            "manifest": artifact.get("manifest"),
            "digest": artifact.get("digest"),
            "signature": artifact.get("signature"),
            "key_id": artifact.get("key_id"),
            "execute": False,
        },
    }


def bulk_invoice_apply(store: dict, operation: dict, call: dict) -> dict:
    """Authorize, reconcile, then write the whole batch. A replay emits no second invoice.created."""
    store.setdefault("invoices", [])
    store.setdefault("events", [])
    store.setdefault("idempotency", {})
    access = operation["x-togavsu-access"]
    quiet = {"written": False, "data": None, "events": [], "invoice_count": len(store["invoices"])}
    if call.get("role") not in access.get("roles", []):
        return {"result": "forbidden", **quiet}
    if any(scope not in set(call.get("token_scopes") or []) for scope in access.get("scopes") or []):
        return {"result": "scope_denied", **quiet}
    items = call.get("items")
    if not isinstance(items, list) or not items or len(items) > int(call.get("batch_max") or 500):
        return {"result": "validation_failed", **quiet}
    denials = [_invoice_item_denial(access, item, call) for item in items]
    if any(denial == "forbidden" for denial in denials):
        return {"result": "forbidden", **quiet}
    if any(denial == "scope_denied" for denial in denials):
        return {"result": "scope_denied", **quiet}
    if any(denial == "validation_failed" for denial in denials):
        return {"result": "validation_failed", **quiet}
    for item in items:
        if _invoice_off_wall(item, call):
            return {"result": "not_found", **quiet}
        if invoice_reconciliation_error(item):
            return {"result": "batch_rejected", **quiet}
    key = call.get("idempotency_key")
    stored_items = [{key: value for key, value in item.items() if key not in {"matter_walled", "on_wall"}} for item in items]
    body = json.dumps(stored_items, sort_keys=True, default=str)
    if key and key in store["idempotency"]:
        prior = store["idempotency"][key]
        if prior["body"] != body:
            return {"result": "idempotency_conflict", **quiet}
        return {"result": "original_result", "written": False, "data": prior["data"], "events": [], "invoice_count": len(store["invoices"])}
    rows = []
    for item in stored_items:
        rows.append({"id": f"invoice-{len(store['invoices']) + len(rows) + 1}", **item})
    store["invoices"].extend(rows)
    events = ["invoice.created"] * len(rows)
    store["events"].extend(events)
    if key:
        store["idempotency"][key] = {"body": body, "data": rows}
    return {"result": "allowed", "written": True, "data": rows, "events": events, "invoice_count": len(store["invoices"])}

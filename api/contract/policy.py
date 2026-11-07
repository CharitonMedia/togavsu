"""Derived access policy for the core contract.

Interactive module calls intersect approved scopes with the viewer's role, walls,
ownership, sharing, and field restrictions. Client portal reads come from operations
the core already publishes for a client. Noninteractive calls exist only when the
core issues a bounded workflow grant and delivers it. File-byte downloads are
recognized by response semantics, not by a path prefix.
"""

from __future__ import annotations

import re

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


def finalize(spec: dict) -> None:
    apply_file_download_contract(spec)
    publish_client_context(spec)
    publish_payment_policy(spec)
    tag_noninteractive_workflows(spec)
    attach_grant_projections(spec)
    ensure_mutation_events(spec)
    publish_trust_debit(spec)
    publish_inherited_history(spec)
    publish_event_scopes(spec)


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
    if call.get("matter_walled") and not call.get("on_wall"):
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
    flag = bool(
        call.get("trust_linked")
        and call.get("below_zero")
        and entry.get("below_zero") == "posts"
        and not entry.get("below_zero_blocks")
        and not (compensation or {}).get("idempotent")
    )
    idempotent = bool((compensation or {}).get("idempotent"))
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


def payment_effect_decision(operation: dict, call: dict) -> dict:
    access = operation["x-togavsu-access"]
    policy = access["payment_effect"]
    if policy.get("authorize_before") != "any_mutation":
        return _denied("forbidden")
    if call["role"] not in access["roles"]:
        return _denied("forbidden")
    if call.get("matter_walled") and not call.get("on_wall"):
        return _denied("not_found")
    posts_trust = False
    for item in _effect_items(call):
        destination = item.get("destination", call.get("destination"))
        status = item.get("status", call.get("status", "completed"))
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
            and item.get("status", call.get("status", "completed")) == "completed"
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
    if initialization.get("matter_walled") and not initialization.get("on_wall"):
        return _denied("not_found")
    if not policy.get("registered_module") or not module.get("registered"):
        return _denied(policy["unregistered"])
    recipient = module.get("recipient_id", module.get("module_id"))
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


def processor_apply(store: dict, policy: dict, operation: dict, call: dict) -> dict:
    """Insert at most one payment per initialization. Denial leaves payment and ledger unchanged."""
    store.setdefault("payments", {})
    store.setdefault("ledger", [])
    store.setdefault("idempotency", {})
    unchanged = {"payment_inserted": False, "ledger_inserted": False, "payment": None, "ledger_count": len(store["ledger"])}
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
            "status": call.get("status", "pending"),
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
    if call.get("matter_walled") and not call.get("on_wall"):
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
    "resetTotp": ["user.totp_reset"],
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


def authorized_badge_counts(operation: dict, caller: dict, rows: list[dict]) -> dict:
    """Sum sidebar badges from records the viewer can read. Filter before the count."""
    access = operation["x-togavsu-access"]
    if not access.get("matter_wall") or access.get("admin_bypasses_wall") or access.get("badge_counts", {}).get("filter_before") != "count":
        return {"result": "forbidden", "badges": {}}
    totals: dict[str, int] = {}
    for row in rows:
        if row.get("matter_walled") and not caller.get("on_wall"):
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
        status = item.get("status")
        inserted = bool(item.get("inserted", previous is None))
        if inserted and "payment.created" in catalog:
            emitted.append("payment.created")
        if status == "completed" and previous != "completed" and "payment.completed" in catalog:
            emitted.append("payment.completed")
        elif status == "reversed" and previous != "reversed" and "payment.reversed" in catalog:
            emitted.append("payment.reversed")
        elif not inserted and previous is not None and status != previous and status != "completed" and "payment.updated" in catalog:
            emitted.append("payment.updated")
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
    "order": ["installed", "subscription", "scope", "matter_wall", "attorney_only", "serialize"],
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
        "scopes_from": "underlying_record",
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


def inherited_history_rows(operation: dict, caller: dict, rows: list[dict]) -> dict:
    rule = operation["x-togavsu-access"].get("inherited_record_access")
    if not rule or rule.get("admin_bypasses_wall") or not operation["x-togavsu-access"].get("matter_wall"):
        return {"result": "forbidden", "rows": []}
    visible = []
    for row in rows:
        if row.get("matter_walled") and not caller.get("on_wall"):
            continue
        needed = list(row.get("required_scopes") or [])
        if any(scope not in set(caller.get("scopes") or []) for scope in needed):
            continue
        copied = {key: value for key, value in row.items() if key not in {"matter_walled", "required_scopes", "attorney_only"}}
        if row.get("attorney_only") and caller.get("role") not in ("attorney", "admin"):
            for field in _HISTORY_REDACTED_FIELDS:
                if field in copied and copied[field] is not None:
                    copied[field] = {"redacted": True}
        visible.append(copied)
    return {"result": "allowed", "rows": visible}


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
    if bool(owner) == bool(module_id):
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


def event_read_scopes(event_type: str, record_type: str | None = None) -> list[str]:
    """Fail closed. An unmapped restricted event does not fall back to events:read."""
    if event_type.startswith("custom_field."):
        return list(_CUSTOM_FIELD_READ.get(record_type or "", []))
    return list(_EVENT_SCOPE_REGISTRY.get(event_type, []))


def webhook_envelope(module: dict, event: dict) -> dict | None:
    """Return the serialized body only after scope, wall, and attorney-only checks. Denial returns None."""
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
    if event.get("matter_walled") and not module.get("on_wall"):
        return None
    attorney_only_record = (
        event.get("attorney_only")
        or event.get("attorney_only_record")
        or event.get("record_type") in _ATTORNEY_ONLY_RECORDS
    )
    if attorney_only_record and not module.get("attorney_only"):
        return None
    if event.get("summary_attorney_only"):
        summary = {"redacted": True}
    elif isinstance(event.get("summary"), str):
        summary = event["summary"]
    else:
        return None
    return {
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
        if job.get("matter_walled") and not job.get("on_wall"):
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
    if call.get("matter_walled") and not call.get("on_wall"):
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
        visible = notification_recipient_decision(
            operation,
            {
                **caller,
                "owner": "self",
                "caller_user_id": caller.get("user_id"),
                "recipient_user_id": row.get("user_id"),
                "matter_walled": row.get("matter_walled"),
                "on_wall": caller.get("on_wall"),
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


def normalize_module_target(module_id: str, target: str) -> str | None:
    """Module-local path. Schemes, absolute URLs, traversal, and other modules' routes are refused."""
    if not isinstance(target, str) or not isinstance(module_id, str) or not module_id:
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
    return bool(filters.get("matter_id")) and bool(caller.get("matter_walled")) and not caller.get("on_wall")


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


_UNSAFE_MARKDOWN = re.compile(
    r"(?i)<\s*/?\s*script\b|javascript\s*:|data\s*:|on(?:error|load|click)\s*=|<\s*/?\s*(?:iframe|object|embed)\b"
)


def rendered_markdown(source: str) -> str:
    """Core-rendered Markdown. Script, handlers, and non-http URL schemes are removed."""
    if not isinstance(source, str):
        return ""
    return _UNSAFE_MARKDOWN.sub("", source)


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
    module_id = manifest.get("id") if isinstance(manifest.get("id"), str) else ""
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
    owner = manifest.get("id") if isinstance(manifest.get("id"), str) else ""
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


_VIEW_SCOPE = re.compile(r"^views:([a-z0-9][a-z0-9-]*)\.([a-z][a-z0-9_]{0,62}):read$")


def _registered_view_names(manifest: dict | None, published_views: list[str] | None) -> set[str]:
    names = {name for name in (published_views or []) if isinstance(name, str)}
    if not manifest:
        return names
    module_id = manifest.get("id") if isinstance(manifest.get("id"), str) else ""
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
    if any(row["required"] and row["scope"] in declined for row in rows):
        listed = [{**row, "approved": False} for row in rows]
        return {"result": "permission_declined", "outcome": "cancelled", "token_scopes": [], "permissions": listed, "version": kept, "previous_version": kept}
    previous_scopes = [scope for scope in (previous or {}).get("approved_scopes") or [] if scope in requested and scope not in declined]
    pending = [scope for scope in requested if scope not in previous_scopes and scope not in approved and scope not in declined]
    if previous and pending:
        listed = [{**row, "approved": row["scope"] in previous_scopes} for row in rows]
        return {
            "result": "awaiting_approval",
            "outcome": "awaiting_approval",
            "token_scopes": previous_scopes,
            "permissions": listed,
            "version": kept,
            "previous_version": kept,
        }
    if previous is None and any(row["required"] and row["scope"] not in approved for row in rows):
        listed = [{**row, "approved": False} for row in rows]
        return {"result": "permission_declined", "outcome": "cancelled", "token_scopes": [], "permissions": listed, "version": kept, "previous_version": kept}
    token = [scope for scope in requested if scope not in declined and (scope in previous_scopes or scope in approved)]
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
    if call.get("matter_walled") and not call.get("on_wall"):
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


def include_views_decision(operation: dict, call: dict) -> str:
    """include= is the approved view scope, not a bypass of role or wall."""
    access = operation["x-togavsu-access"]
    if "include_scope_pattern" not in access:
        return "forbidden"
    if call.get("role") not in access.get("roles", []):
        return "forbidden"
    if access.get("matter_wall") and call.get("matter_walled") and not call.get("on_wall"):
        return "not_found"
    if access.get("attorney_only") and call.get("role") not in ("attorney", "admin"):
        return "not_found"
    approved = set(call.get("approved_scopes") or [])
    if "current_approved_scopes" in call:
        approved &= set(call.get("current_approved_scopes") or [])
    for view in call.get("include") or []:
        if view.count(".") != 1:
            return "validation_failed"
        module_id, view_name = view.split(".", 1)
        needed = f"views:{module_id}.{view_name}:read"
        # Section 9.9: another module may include this view only after the admin approves this scope.
        if needed not in approved:
            return "forbidden"
    return "allowed"


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
    denied = {"ledger_mutated": False, "debited_account_id": None, "amount_cents": 0}
    if call.get("role") not in ("admin", "attorney"):
        return {"result": "forbidden", **denied}
    if call.get("matter_walled") and not call.get("on_wall"):
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
    return {
        "result": "allowed",
        "ledger_mutated": True,
        "debited_account_id": row["trust_account_id"],
        "client_id": row["client_id"],
        "amount_cents": call["amount_cents"],
        "below_zero": call.get("balance_after", 0) < 0,
    }

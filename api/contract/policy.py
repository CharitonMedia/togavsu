"""Derived access policy for the core contract.

Interactive module calls intersect approved scopes with the viewer's role, walls,
ownership, sharing, and field restrictions. Client portal reads come from operations
the core already publishes for a client. Noninteractive calls exist only when the
core issues a bounded workflow grant and delivers it. File-byte downloads are
recognized by response semantics, not by a path prefix.
"""

from __future__ import annotations

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

BACKGROUND_FILE_SEQUENCE = [
    "createFileUpload",
    "getFileUpload",
    "putFileChunk",
    "completeFileUpload",
    "publishFile",
    "bulkFile",
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
NONINTERACTIVE_OPERATIONS = (
    GENERIC_NONINTERACTIVE
    | frozenset(MALWARE_SEQUENCE)
    | frozenset(PAYMENT_PROCESSOR["sequence"])
    | frozenset(BACKGROUND_FILE_SEQUENCE)
    | frozenset({"issueFilePublicationGrant"})
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
        elif operation_id in BACKGROUND_FILE_SEQUENCE or operation_id == "issueFilePublicationGrant":
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
    )
    return {
        "result": "allowed",
        "payment_mutated": True,
        "payment_status": "reversed",
        "amount_unchanged": True,
        "ledger_mutated": bool(call.get("trust_linked")),
        "ledger_append_only": bool(entry.get("append_only", True)) if call.get("trust_linked") else False,
        "original_trust_edited": bool(entry.get("original_edited", False)) if call.get("trust_linked") else False,
        "flag": flag,
        "trust_posted_by": entry.get("posted_by") if call.get("trust_linked") else None,
        "trust_account_id": account_id if call.get("trust_linked") else None,
        "trust_entry": None if compensation is None else compensation["entry"],
        "trust_post": None if compensation is None else compensation["post"],
        "grant": None,
    }


def _reversal_compensation(call: dict, account_id: str) -> dict | None:
    """Read-only compensation row. The write schema does not accept entry_type reversal."""
    required = ("client_id", "matter_id", "amount_cents", "trust_entry_id", "created_by", "posted_entry_id", "created_at")
    if any(call.get(key) in (None, "") for key in required):
        return None
    amount = call["amount_cents"]
    if not isinstance(amount, int) or amount == 0:
        return None
    signed = -abs(amount)
    entry = {
        "id": call["posted_entry_id"],
        "created_at": call["created_at"],
        "created_by": call["created_by"],
        "trust_account_id": account_id,
        "client_id": call["client_id"],
        "matter_id": call["matter_id"],
        "entry_type": "reversal",
        "amount_cents": signed,
        "direction": "debit",
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
    if call["operation"] == "createPayment" and call.get("bound_payment"):
        if _same_processor_body(call["bound_payment"], call):
            return policy["idempotency_replay"]
        return "conflict"
    field = call.get("field")
    if field and field not in grant.get("allowed_fields", []):
        return policy["field_outside_grant"]
    return "allowed"


def _same_processor_body(payment: dict, call: dict) -> bool:
    for name in ("amount_cents", "matter_id", "contact_id", "destination", "invoice_id", "trust_account_id"):
        if name in call and call.get(name) != payment.get(name):
            return False
    if "status" in call and call.get("status") != payment.get("status"):
        return False
    return True


def _processor_body(call: dict) -> tuple:
    names = ("amount_cents", "matter_id", "contact_id", "destination", "invoice_id", "trust_account_id", "status", "payment_id")
    return tuple((name, call.get(name)) for name in names)


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
    key = call.get("idempotency_key")
    body = _processor_body(call)
    if key and key in store["idempotency"] and store["idempotency"][key]["body"] != body:
        return {"result": policy["idempotency_conflict"], **unchanged}
    if key and key in store["idempotency"] and store["idempotency"][key]["body"] == body:
        prior_id = store["idempotency"][key]["initialization_id"]
        return {"result": policy["idempotency_replay"], "payment": store["payments"].get(prior_id), **unchanged}
    code = processor_callback(policy, operation, enriched)
    if code != "allowed":
        return {"result": code, "payment": existing, **unchanged}
    account_call = {
        "destination": call.get("destination"),
        "trust_account_id": call.get("trust_account_id", grant.get("trust_account_id")),
        "trust_accounts": call.get("trust_accounts"),
        "firm_id": call.get("firm_id"),
        "grant": grant,
    }
    failure = trust_account_failure(account_call)
    if failure:
        return {"result": failure, **unchanged}
    if call.get("operation") == "createPayment":
        payment = {
            "id": grant["payment_id"],
            "initialization_id": init_id,
            "amount_cents": call.get("amount_cents"),
            "matter_id": call.get("matter_id"),
            "contact_id": call.get("contact_id"),
            "invoice_id": call.get("invoice_id"),
            "destination": call.get("destination"),
            "trust_account_id": grant.get("trust_account_id"),
            "status": call.get("status", "pending"),
            "trust_posted": False,
        }
        if init_id in store["payments"]:
            winner = store["payments"][init_id]
            if _same_processor_body(winner, call):
                return {"result": policy["idempotency_replay"], "payment": winner, **unchanged}
            return {"result": "conflict", **unchanged}
        store["payments"][init_id] = payment
        ledger_inserted = _post_trust_once(store, payment, call)
        if key:
            store["idempotency"][key] = {"body": body, "initialization_id": init_id}
        return {"result": "allowed", "payment_inserted": True, "ledger_inserted": ledger_inserted, "payment": payment, "ledger_count": len(store["ledger"])}
    if not existing or call.get("payment_id") != existing["id"]:
        return {"result": policy["unrelated_payment"], **unchanged}
    if existing.get("status") == "completed" and call.get("status") == "completed":
        return {"result": policy["idempotency_replay"], "payment": existing, **unchanged, "ledger_count": len(store["ledger"])}
    existing["status"] = call.get("status", existing["status"])
    ledger_inserted = _post_trust_once(store, existing, call)
    if key:
        store["idempotency"][key] = {"body": body, "initialization_id": init_id}
    return {"result": "allowed", "payment_inserted": False, "ledger_inserted": ledger_inserted, "payment": existing, "ledger_count": len(store["ledger"])}


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
    registry: dict[str, list[str]] = {}
    for event, groups in buckets.items():
        registry[event] = _event_scope_choice(event, groups)
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
    if not job.get("expires_at") or job.get("expired"):
        return {**denied, "result": policy["expired"]}
    if job.get("matter_walled") and not job.get("on_wall"):
        return {**denied, "result": policy["wrong_matter"]}
    principal_matters = job.get("principal_matter_ids")
    if principal_matters is not None and job.get("matter_id") not in principal_matters:
        return {**denied, "result": policy["wrong_matter"]}
    if not job.get("matter_id"):
        return {**denied, "result": "validation_failed"}
    if policy.get("grants_admin") or policy.get("firmwide_bypass") or access.get("grants_admin") or access.get("firmwide_bypass"):
        return denied
    grant = {
        "module_id": module["module_id"],
        "matter_id": job["matter_id"],
        "source": job["source"],
        "allowed_operations": list(policy["sequence"]),
        "expires_at": job["expires_at"],
        "revoked": False,
        "issued_by": policy["issued_by"],
        "upload_ids": [],
        "expired": False,
    }
    event = {
        "id": job.get("event_id") or "018f1a00-7b3c-7d4e-8a11-bbbbbbbbbbbb",
        "type": "file.background_authorized",
        "occurred_at": job.get("occurred_at") or job["expires_at"],
        "actor_kind": "module",
        "record_type": "matter",
        "record_id": job["matter_id"],
        "matter_id": job["matter_id"],
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
        store["uploads"][upload_id] = {
            "id": upload_id,
            "status": "receiving",
            "matter_id": grant["matter_id"],
            "owner": grant["module_id"],
        }
        grant["upload_ids"].append(upload_id)
        emitted = committed_events(operation, committed=True)
        store["events"].extend(emitted)
        return {"result": "allowed", "upload_id": upload_id, "file_created": False, "events": emitted}
    if operation_id == "putFileChunk":
        store["uploads"][upload_id]["status"] = "receiving"
        return {"result": "allowed", "file_created": False, "events": []}
    if operation_id == "getFileUpload":
        return {"result": "allowed", "upload": store["uploads"][upload_id], "file_created": False, "events": []}
    if operation_id == "completeFileUpload":
        store["uploads"][upload_id]["status"] = "staged"
        emitted = committed_events(operation, committed=True)
        store["events"].extend(emitted)
        return {"result": "allowed", "file_created": False, "events": emitted}
    if operation_id == "publishFile":
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

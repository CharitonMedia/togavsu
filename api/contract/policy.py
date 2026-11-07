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

GENERIC_NONINTERACTIVE = frozenset(
    {
        "createEvent",
        "bulkEvent",
        "createNotification",
        "createFileLink",
        "createAiCall",
    }
)
NONINTERACTIVE_OPERATIONS = GENERIC_NONINTERACTIVE | frozenset(MALWARE_SEQUENCE) | frozenset(PAYMENT_PROCESSOR["sequence"])

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
                f"{schema_name} and no ungranted field."
            ),
            "oneOf": [slot["schema"], {"$ref": f"#/components/schemas/{schema_name}"}],
        }


def finalize(spec: dict) -> None:
    apply_file_download_contract(spec)
    publish_client_context(spec)
    publish_payment_policy(spec)
    tag_noninteractive_workflows(spec)
    attach_grant_projections(spec)


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
    if call.get("trust_linked"):
        account_id = call.get("trust_account_id")
        failure = trust_account_failure({**call, "destination": "trust", "trust_account_id": account_id})
        if failure:
            return _denied(failure)
        if call.get("reversal_account_id") not in (None, account_id):
            return _denied("forbidden")
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
        "grant": None,
    }


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

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
    ],
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
        posts_trust = posts_trust or needs_trust
    entry = policy["trust_destination"]["trust_entry"]
    return {
        "result": "allowed",
        "payment_mutated": True,
        "ledger_mutated": posts_trust,
        "ledger_append_only": bool(entry["append_only"]) if posts_trust else False,
        "flag": False,
        "trust_posted_by": entry["posted_by"] if posts_trust else None,
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
    if policy.get("grants_admin") or policy.get("firmwide_bypass") or policy.get("ambient_payment_write"):
        return denied
    grant = {
        "module_id": module["module_id"],
        "initialization_id": initialization["initialization_id"],
        "payment_id": None,
        "matter_id": initialization["matter_id"],
        "contact_id": initialization["contact_id"],
        "invoice_id": initialization.get("invoice_id"),
        "amount_cents": initialization["amount_cents"],
        "destination": initialization["destination"],
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
    if effect and call.get("destination") == "trust" and call.get("posts_trust"):
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
    if call["operation"] == "updatePaymentStatus" and call.get("payment_id") != grant.get("payment_id"):
        return policy["unrelated_payment"]
    field = call.get("field")
    if field and field not in grant.get("allowed_fields", []):
        return policy["field_outside_grant"]
    return "allowed"


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


def copy_policy() -> dict:
    return copy.deepcopy(PAYMENT_PROCESSOR)

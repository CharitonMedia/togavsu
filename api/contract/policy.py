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
from html.parser import HTMLParser

from contract.metadata import *  # compatibility re-export for existing tests and the simulator

_MODULE_ID_SLUG = re.compile(MODULE_ID_PATTERN)
_UUID_TEXT = re.compile(r"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")


def _requested_confirmation(operation: dict, query: dict, body: dict, trusted: dict):
    """The operation's declared input. A query flag does not confirm a body field, or the reverse."""
    declared = (operation.get("x-togavsu-access") or {}).get("quarantine_confirmation_input")
    if not isinstance(declared, dict):
        declared = _confirmation_input(operation)
    if isinstance(declared, dict):
        source = body if declared.get("in") == "body" else query if declared.get("in") == "query" else {}
        name = declared.get("name")
        if isinstance(source, dict) and isinstance(name, str) and name in source:
            return source[name]
    if "confirm" in trusted:
        return trusted.get("confirm")
    return None


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


def _trust_flag_from_event(event: dict, entry: dict) -> dict:
    """One TrustFlag built from the same balances that emitted trust.flag_raised."""
    scope = event.get("balance_scope") or "matter"
    flag = {
        "id": entry["id"],
        "created_at": entry["created_at"],
        "created_by": entry["created_by"],
        "updated_at": entry["created_at"],
        "trust_entry_id": entry["id"],
        "balance_cents": event["balance_cents"],
        "scope": scope,
        "status": "open",
    }
    if scope == "account":
        flag["trust_account_id"] = entry["trust_account_id"]
    elif scope == "client":
        flag["client_id"] = entry["client_id"]
    else:
        flag["client_id"] = entry["client_id"]
        flag["matter_id"] = entry["matter_id"]
    return flag


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
    if bearer_lacks_scopes(call, branch["scopes"]):
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
                    "entry_type": posted.get("entry_type"),
                    "amount_cents": posted.get("amount_cents", 0),
                    "direction": posted.get("direction"),
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
    if compensation is not None and not idempotent and events:
        posted_event = next(event for event in events if event["type"] == "trust.entry_posted")
        compensation["post"]["balances"] = dict(posted_event["balances"])
        compensation["post"]["flags"] = [_trust_flag_from_event(event, compensation["entry"]) for event in events if event["type"] == "trust.flag_raised"]
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
    post = {"entry": entry, "balances": {"matter_cents": 0, "client_cents": 0, "account_cents": 0}, "flags": []}
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
        if bearer_lacks_scopes(call, needed):
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
    if grant_scope_denial(call, needed):
        return policy["missing_scope"]
    if call.get("matter_id") != grant.get("matter_id"):
        return policy["unrelated_matter"]
    if call.get("matter_walled") and not matter_visible(call, {"matter_id": call.get("matter_id"), "matter_walled": True}, single_record=True):
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


def malware_scan_decision(policy: dict, operation: dict, call: dict) -> str:
    """Bounded file.version_created grant. HTTP and MCP both use this decision."""
    if call.get("token_revoked"):
        return policy["revoked_module_token"]
    if not call.get("registered") or policy["event_type"] not in call.get("subscriptions", []):
        return policy["invalid"]
    grant = call.get("grant")
    if not isinstance(grant, dict) or grant.get("invalid"):
        return policy["invalid"]
    if grant.get("expired"):
        return policy["expired"]
    if grant.get("revoked"):
        return policy["revoked"]
    if grant.get("issued_by") != policy["issued_by"] or grant.get("module_id") != call.get("caller_module_id"):
        return policy["module_identity_mismatch"]
    if call.get("operation") not in policy["sequence"] or call.get("operation") not in grant.get("allowed_operations", []):
        return policy["operation_outside_grant"]
    if operation["x-togavsu-access"].get("noninteractive_workflow") != "malware_scan":
        return policy["operation_outside_grant"]
    needed = operation["x-togavsu-access"]["scopes"]
    if grant_scope_denial(call, needed):
        return policy["missing_scope"]
    if call.get("matter_id") != grant.get("matter_id"):
        return policy["unrelated_matter"]
    if call.get("matter_walled") and not matter_visible(call, {"matter_id": call.get("matter_id"), "matter_walled": True}, single_record=True):
        return policy["unrelated_matter"]
    if call.get("file_id") != grant.get("file_id"):
        return policy["unrelated_file"]
    if call.get("operation") != "getFile" and call.get("version_id") != grant.get("version_id"):
        return policy["unrelated_version"]
    field = call.get("field")
    if field in policy["attorney_only_fields"]:
        return policy["attorney_only_field"]
    if field and (field not in grant.get("allowed_fields", []) or field not in policy["metadata_fields"]):
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
                "entry_type": "deposit",
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


def _composed_payment_events(operation: dict, payment: dict, call: dict, *, inserted: bool, previous_status, ledger_inserted: bool, committed: bool = True, replay: bool = False) -> list[dict]:
    """Payment lifecycle events, then the trust events from the same committed status."""
    if not committed or replay:
        return []
    lifecycle = payment_lifecycle_events(
        operation,
        [{"status": payment.get("status"), "previous_status": previous_status, "inserted": inserted}],
        committed=True,
    )
    trust = _posted_trust_events(operation, payment, call, ledger_inserted) if ledger_inserted else []
    return [{"type": name} for name in lifecycle] + trust


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
            events=_composed_payment_events(operation, payment, call, inserted=True, previous_status=None, ledger_inserted=ledger_inserted),
        )
    if not existing or call.get("payment_id") != existing["id"]:
        return {**unchanged, "result": policy["unrelated_payment"]}
    requested = call.get("status") if call.get("status") is not None else existing.get("status")
    previous_status = existing.get("status")
    transition = payment_status_transition(previous_status, requested)
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
            events=_composed_payment_events(
                operation,
                existing,
                call,
                inserted=False,
                previous_status=previous_status,
                ledger_inserted=ledger_inserted,
            ),
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
    domains = access.get("record_type_domain") or {}
    planned = []
    for item in items:
        record_type = item.get("record_type")
        needed = (access.get("record_type_scopes") or {}).get(record_type)
        if not needed or bearer_lacks_scopes(call, needed):
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


_ACCOUNTANT_FILE_CLASSES = frozenset({"receipt", "invoice_pdf"})


def _trusted_file_records(call: dict) -> list[dict]:
    """Records the core loaded. A classification on the call or in tool arguments is not one of these."""
    rows: list[dict] = []
    one = call.get("stored_file")
    if isinstance(one, dict):
        rows.append(one)
    many = call.get("stored_files")
    if isinstance(many, list):
        rows.extend(row for row in many if isinstance(row, dict))
    return rows


def _financial_classification(record: dict | None) -> str | None:
    if not isinstance(record, dict):
        return None
    value = record.get("financial_classification")
    if value in _ACCOUNTANT_FILE_CLASSES:
        return value
    return None


def _stored_file_hidden(caller: dict, record: dict) -> bool:
    if record.get("attorney_only") is True and caller.get("role") not in ("attorney", "admin"):
        return True
    matter_id = record.get("matter_id", caller.get("matter_id"))
    walled = record.get("matter_walled")
    if walled is True and not matter_visible(caller, {"matter_id": matter_id, "matter_walled": True}, single_record=True):
        return True
    return False


def accountant_file_rows(caller: dict, rows: list[dict]) -> list[dict]:
    """Accountants receive receipts and invoice PDFs. Other roles keep the rows they were given."""
    if caller.get("role") != "accountant":
        return list(rows)
    visible = []
    for row in rows:
        if not isinstance(row, dict) or _financial_classification(row) is None or _stored_file_hidden(caller, row):
            continue
        visible.append(row)
    return visible


def _accountant_financial_denial(operation: dict, call: dict) -> str | None:
    """None when this caller may read the file. Accountants need the core's classification."""
    if operation.get("x-togavsu-access", {}).get("accountant") != "receipts-and-invoice-pdfs":
        return None
    if call.get("role") != "accountant":
        return None
    if operation.get("operationId") == "getInvoicePdf":
        return None
    records = _trusted_file_records(call)
    if operation.get("operationId") == "listFile":
        return None
    if operation.get("operationId") == "createFileArchive":
        selected = call.get("archive_rows") if isinstance(call.get("archive_rows"), list) else records
        if not selected or any(_financial_classification(row) is None or _stored_file_hidden(call, row) for row in selected):
            return "not_found"
        return None
    file_id = call.get("file_id")
    if file_id:
        matches = [row for row in records if row.get("id") == file_id or row.get("file_id") == file_id]
        record = matches[0] if matches else None
    else:
        record = records[0] if len(records) == 1 else None
    if _financial_classification(record) is None or _stored_file_hidden(call, record or {}):
        return "not_found"
    return None


def _file_row_denial(subject: dict, row: dict) -> str | None:
    """None when this caller can read this trusted file row. Caller flags are not the row."""
    if _trusted_file([row], row.get("id")) is None:
        return "not_found"
    if row.get("attorney_only") is True and subject.get("role") not in ("attorney", "admin"):
        return "not_found"
    if not matter_visible(subject, {"matter_id": row.get("matter_id"), "matter_walled": row["matter_walled"]}, single_record=isinstance(row.get("matter_id"), str)):
        return "not_found"
    if _client_file_denied(subject, row):
        return "not_found"
    if subject.get("role") == "accountant" and _financial_classification(row) is None:
        return "not_found"
    if type(row.get("quarantined")) is not bool:
        return "not_found"
    return None


def _client_file_denied(subject: dict, row: dict) -> bool:
    """A shared assigned file must be on this client's matter. Contact applies only when the file is unassigned."""
    if subject.get("role") != "client":
        return False
    if row.get("shared_with_client") is not True or "matter_id" not in row:
        return True
    matters = subject.get("client_matter_ids")
    matter_list_ok = isinstance(matters, list) and bool(matters) and all(isinstance(item, str) and item for item in matters)
    matter_id = row.get("matter_id")
    if isinstance(matter_id, str) and matter_id:
        return not (matter_list_ok and matter_id in matters)
    if matter_id is not None:
        return True
    contact = subject.get("client_contact_id")
    return not (isinstance(row.get("contact_id"), str) and row["contact_id"] and isinstance(contact, str) and contact == row["contact_id"])


def _stored_file_for_request(call: dict) -> dict | None:
    """The trusted file row for this path. A caller shared or owner flag is not a row."""
    file_id = call.get("file_id")
    rows = []
    if isinstance(call.get("stored_file"), dict):
        rows.append(call["stored_file"])
    rows.extend(row for row in call.get("stored_files") or [] if isinstance(row, dict))
    if isinstance(file_id, str) and file_id:
        matches = [row for row in rows if row.get("id") == file_id or row.get("file_id") == file_id]
        if len(matches) != 1:
            return None
        return matches[0] if _trusted_file([matches[0]], matches[0].get("id") or file_id) else None
    if len(rows) == 1 and _trusted_file([rows[0]], rows[0].get("id")):
        return rows[0]
    return None


def _bind_archive(subject: dict, call: dict, body: dict) -> None:
    """Every requested file or folder member must resolve. Inaccessible rows are not dropped."""
    file_ids = body.get("file_ids") if "file_ids" in body else None
    folder_id = body.get("folder_id") if isinstance(body.get("folder_id"), str) and body.get("folder_id") else None
    has_files = isinstance(file_ids, list)
    if has_files == (folder_id is not None) or (has_files and not file_ids):
        subject["archive_result"] = "validation_failed"
        subject["archive_rows"] = []
        return
    records = [row for row in call.get("stored_files") or [] if isinstance(row, dict)]
    if folder_id is not None:
        folders = call.get("stored_folders") if isinstance(call.get("stored_folders"), list) else []
        if not any(isinstance(folder, dict) and folder.get("id") == folder_id for folder in folders):
            subject["archive_result"] = "not_found"
            subject["archive_rows"] = []
            return
        rows = []
        for row in records:
            if row.get("folder_id") != folder_id:
                continue
            resolved = _trusted_file([row], row.get("id"))
            if resolved is None:
                subject["archive_result"] = "not_found"
                subject["archive_rows"] = []
                return
            rows.append(resolved)
        if not rows:
            subject["archive_result"] = "not_found"
            subject["archive_rows"] = []
            return
        subject["archive_rows"] = rows
        return
    if any(not isinstance(item, str) or not item for item in file_ids):
        subject["archive_result"] = "validation_failed"
        subject["archive_rows"] = []
        return
    if len(file_ids) != len(set(file_ids)):
        subject["archive_result"] = "not_found"
        subject["archive_rows"] = []
        return
    rows = []
    for file_id in file_ids:
        resolved = _trusted_file(records, file_id)
        if resolved is None:
            subject["archive_result"] = "not_found"
            subject["archive_rows"] = []
            return
        rows.append(resolved)
    subject["archive_rows"] = rows


def file_download_decision(operation: dict, call: dict) -> str:
    access = operation["x-togavsu-access"]
    if operation.get("operationId") == "createFileArchive":
        if "file.downloaded" not in operation.get("x-togavsu-events", []):
            return "missing_event"
        if call.get("archive_result"):
            return call["archive_result"]
        rows = call.get("archive_rows")
        if not isinstance(rows, list) or not rows:
            return "not_found"
        for row in rows:
            if _file_row_denial(call, row):
                return "not_found"
        if any(row.get("quarantined") is True for row in rows) and call.get("confirm") is not True:
            return "quarantine_confirmation_required"
        return "highlighted" if call.get("role") == "client" else "audited"
    if "file.downloaded" not in operation.get("x-togavsu-events", []):
        return "missing_event"
    if access.get("quarantine_confirmation") == "required" and not access.get("quarantine_confirmation_input"):
        return "missing_confirm_input"
    if call.get("attorney_only") and call.get("role") in ("staff", "client", "accountant"):
        return "not_found"
    if not matter_visible(call, call, single_record=True):
        return "not_found"
    if call.get("role") == "client" and access.get("access_inherits") == "file" and call.get("file_id"):
        row = _stored_file_for_request(call)
        if row is None or _client_file_denied(call, row):
            return "not_found"
    elif call.get("role") == "client" and not call.get("shared"):
        return "not_found"
    if call.get("owner") == "other_client":
        return "not_found"
    denied = _accountant_financial_denial(operation, call)
    if denied:
        return denied
    if call.get("quarantined") and call.get("confirm") is not True:
        return "quarantine_confirmation_required"
    return "highlighted" if call.get("role") == "client" else "audited"


def trust_entry_authorization(operation: dict, call: dict) -> str:
    """Scope and role come from the entry type. A deposit does not require trust:withdraw."""
    access = operation["x-togavsu-access"]
    if access.get("scope_combination") != "per_entry_type":
        return "forbidden"
    items = call.get("items") if isinstance(call.get("items"), list) else [call]
    if not items:
        return "validation_failed"
    for item in items:
        kind = item.get("entry_type")
        roles = (access.get("entry_type_roles") or {}).get(kind)
        needed = (access.get("entry_type_scopes") or {}).get(kind)
        if not roles or not needed:
            return "validation_failed"
        if bearer_lacks_scopes(call, needed):
            return "scope_denied"
        if call.get("role") not in roles:
            return "forbidden"
        row = {
            "matter_id": item.get("matter_id", call.get("matter_id")),
            "matter_walled": item.get("matter_walled", call.get("matter_walled")),
        }
        if row["matter_id"] is not None or row["matter_walled"] or call.get("matter_membership") is not None:
            if not matter_visible(call, row, single_record=row["matter_id"] is not None):
                return "not_found"
    return "allowed"


def canonical_owner(value) -> dict | None:
    """One owner shape. Events use type/id. FileUpload uses owner_kind and the matching id field."""
    if not isinstance(value, dict):
        return None
    kind = value.get("type")
    ident = value.get("id")
    if kind in {"user", "module"} and isinstance(ident, str) and ident:
        return {"type": kind, "id": ident}
    kind = value.get("owner_kind")
    if kind == "user" and isinstance(value.get("owner_user_id"), str) and value["owner_user_id"]:
        return {"type": "user", "id": value["owner_user_id"]}
    if kind == "module" and isinstance(value.get("owner_module_id"), str) and value["owner_module_id"]:
        return {"type": "module", "id": value["owner_module_id"]}
    return None


def _tagged_owner_denial(operation: dict, call: dict) -> str | None:
    """A tagged user or module owner is the record owner. A fabricated user id does not pass for a module."""
    access = operation["x-togavsu-access"]
    tagged = None
    supplied = call.get("owner")
    if isinstance(supplied, dict):
        tagged = canonical_owner(supplied)
        if tagged is None:
            return "not_found"
    elif supplied in (None,):
        record = call.get("record") if isinstance(call.get("record"), dict) else None
        published = record.get("owner") if isinstance(record, dict) else None
        if isinstance(published, dict):
            tagged = canonical_owner(published)
            if tagged is None:
                return "not_found"
    if tagged is not None:
        rule = {"binding": "owner", "admin_bypass": access.get("owner") == "self_or_admin" or access.get("feed_owner") == "self_or_admin"}
        stored = {"type": "file_upload.created", "context": {"owner": tagged}}
        if not _principal_can_read(rule, call, stored):
            return "not_found"
        return None
    owner = access.get("owner")
    if owner == "self" and call.get("owner") not in (None, "self"):
        return "not_found"
    if owner == "self_or_admin" and call.get("owner") not in (None, "self") and call.get("role") != "admin":
        return "not_found"
    if access.get("feed_owner") == "self_or_admin" and call.get("owner") not in (None, "self") and call.get("role") != "admin":
        return "not_found"
    return None


def _replacement_decision(operation: dict, call: dict) -> dict | None:
    """Live reads and writes of a replaced domain go to that module. Retained core rows are a separate read."""
    domain = operation.get("x-togavsu-replaceable-domain")
    if not domain:
        return None
    handler = (call.get("domain_handlers") or {}).get(domain) or {}
    if not handler.get("replaced"):
        return None
    if not operation.get("x-togavsu-writes") and call.get("core_history") is True:
        return None
    if not handler.get("available"):
        return {"result": "forbidden", "effects": False}
    return {"result": "allowed", "effects": False, "routed": handler.get("module_id")}


def _workflow_code(spec: dict, operation: dict, call: dict) -> str | None:
    """None when a human viewer is present. A module with no viewer must pass the bounded grant."""
    workflow = operation["x-togavsu-access"].get("noninteractive_workflow")
    if not workflow or call.get("role") or call.get("viewer_present"):
        return None
    policy = spec.get("x-togavsu-module-delegation", {}).get("noninteractive", {}).get(workflow)
    if not policy:
        return "forbidden"
    if workflow == "payment_processor":
        return processor_callback(policy, operation, call)
    if workflow == "malware_scan":
        return malware_scan_decision(policy, operation, call)
    if workflow == "background_file_publication":
        return background_grant_authorization(policy, call)
    return "forbidden"


def _principal_code(operation: dict, call: dict) -> str | None:
    principal = operation["x-togavsu-access"].get("module_principal") or {}
    if not principal:
        return None
    scope = principal.get("scope")
    adapted = {
        "actor": "module" if call.get("module_id") and not call.get("human_session") and not call.get("role") else "human",
        "human_session": bool(call.get("human_session") or call.get("role")),
        "role": call.get("role"),
        "scopes": list(call.get("token_scopes") or []) if "token_scopes" in call else [],
        "module_id": call.get("module_id"),
        "path_module_id": call.get("path_module_id"),
        "owner_module_id": call.get("owner_module_id"),
        "action": call.get("action"),
        "marker_id": call.get("marker_id"),
        "created_at": call.get("created_at"),
        "created_by": call.get("created_by"),
        "committed": True,
        "replay": bool(call.get("replay")),
    }
    for name in ("label", "color", "target", "heading"):
        if name in call:
            adapted[name] = call.get(name)
    if call.get("grant_revoked") or call.get("installed") is False:
        return "forbidden"
    if scope == "help:write":
        return module_help_registration(operation, adapted)
    if scope == "markers:write":
        rows = []
        if isinstance(call.get("stored_definition"), dict):
            rows.append(dict(call["stored_definition"]))
        if isinstance(call.get("stored_definitions"), list):
            rows.extend(dict(row) for row in call["stored_definitions"] if isinstance(row, dict))
        return module_marker_registration(operation, {"definitions": rows, "attachments": []}, adapted)["result"]
    return "forbidden"


def _processor_initialization_code(spec: dict, operation: dict, call: dict) -> str:
    """The declared processor_issuance policy. Registration, caller scopes, and current approvals all apply."""
    name = operation["x-togavsu-access"].get("processor_issuance")
    policy = (spec.get("x-togavsu-module-delegation") or {}).get("noninteractive", {}).get(name or "")
    if not isinstance(policy, dict):
        return "forbidden"
    if policy.get("initialization_requires_viewer") and call.get("role") not in (operation["x-togavsu-access"].get("roles") or []):
        return "forbidden"
    approved = list(call.get("approved_scopes") or [])
    if "current_approved_scopes" in call:
        current = set(call.get("current_approved_scopes") or [])
        approved = [scope for scope in approved if scope in current]
    if session_authorizes(call):
        caller_scopes = list(call.get("caller_scopes") or [])
    elif call.get("credential") == "bearer" or "token_scopes" in call:
        caller_scopes = list(call.get("token_scopes") or [])
    else:
        caller_scopes = list(call.get("caller_scopes") or [])
    module = {
        "registered": bool(call.get("registered")),
        "module_id": call.get("module_id"),
        "recipient_id": call.get("recipient_id") or call.get("module_id"),
        "approved_scopes": approved,
    }
    initialization = {
        "processor_module_id": call.get("processor_module_id"),
        "initialization_id": call.get("initialization_id"),
        "reserved_payment_id": call.get("reserved_payment_id"),
        "matter_id": call.get("matter_id"),
        "contact_id": call.get("contact_id"),
        "invoice_id": call.get("invoice_id"),
        "amount_cents": call.get("amount_cents"),
        "destination": call.get("destination"),
        "trust_account_id": call.get("trust_account_id"),
        "caller_scopes": caller_scopes,
        "expires_at": call.get("expires_at"),
        "matter_walled": bool(call.get("matter_walled")),
        "on_wall": call.get("on_wall"),
        "matter_membership": call.get("matter_membership"),
        "trust_accounts": call.get("trust_accounts"),
        "firm_id": call.get("firm_id"),
    }
    return processor_issuance(policy, module, initialization)["result"]


def _conditional_scope_code(spec: dict, operation: dict, call: dict) -> str | None:
    """None when this operation has no conditional financial or per-item scope rule."""
    access = operation["x-togavsu-access"]
    if call.get("noninteractive_accepted"):
        return None
    if access.get("processor_issuance"):
        return _processor_initialization_code(spec, operation, call)
    if access.get("trust_debit"):
        if not call.get("role"):
            return "forbidden"
        return trust_debit_decision(call)["result"]
    if access.get("entry_type_scopes"):
        return trust_entry_authorization(operation, call)
    if access.get("payment_reversal"):
        if not call.get("role"):
            return "forbidden"
        return payment_reversal_decision(operation, call)["result"]
    if access.get("payment_effect"):
        if not call.get("role"):
            return "forbidden"
        return payment_effect_decision(operation, call)["result"]
    if access.get("record_type_domain"):
        items = call.get("items") if isinstance(call.get("items"), list) else []
        if not items:
            return "validation_failed"
        return custom_field_bulk_apply(operation, call, items, call.get("domain_handlers"))["result"]
    if access.get("item_privileges"):
        return bulk_invoice_apply({"invoices": [], "events": [], "idempotency": {}}, operation, call)["result"]
    if access.get("external_effects"):
        return external_effect_decision(operation, call)["result"]
    if access.get("authorization_category") == "service_principal_issuance":
        module = {
            "installed": bool(call.get("installed", call.get("registered"))),
            "registered": bool(call.get("registered")),
            "module_id": call.get("module_id"),
            "approved_scopes": list(call.get("token_scopes") or []) if "token_scopes" in call else [],
            "tenant_id": call.get("tenant_id"),
        }
        job = {
            "module_id": call.get("module_id"),
            "source": call.get("source"),
            "assignment": call.get("assignment"),
            "matter_id": call.get("matter_id"),
            "matter_walled": call.get("matter_walled"),
            "on_wall": call.get("on_wall"),
            "matter_membership": call.get("matter_membership"),
            "expires_at": call.get("expires_at"),
            "principal_matter_ids": call.get("principal_matter_ids"),
            "contact_id": call.get("contact_id"),
            "queue": call.get("queue"),
        }
        return background_grant_issuance(operation, module, job)["result"]
    if access.get("issuance") == "admin_only":
        return issue_api_token(operation, call)["result"]
    if access.get("recipient") == "self":
        return notification_recipient_decision(operation, call)
    return None


def _resolved_source_wall(subject: dict, matter_id: str) -> bool | None:
    """Resolved wall boolean for one known matter.

    A supplied matter_walls value wins, including null and any non-object. Those are unresolved and
    do not fall through to a scalar. A genuinely omitted map uses the trusted scalar, which must be
    a boolean. Omitted scalar evidence is unresolved.
    """
    if "matter_walls" in subject:
        walls = subject.get("matter_walls")
        if not isinstance(walls, dict) or matter_id not in walls:
            return None
        value = walls[matter_id]
        return value if type(value) is bool else None
    if "matter_walled" not in subject:
        return None
    value = subject.get("matter_walled")
    return value if type(value) is bool else None


def _item_matter_walled(call: dict, matter_id: str) -> bool | None:
    """A resolved wall boolean, or None when the trusted value is not a boolean.

    An id absent from the map stays walled. Null, strings, and numbers are unresolved, not unwalled.
    An explicit null or non-object map does not fall through to the scalar. The item body is not a source.
    """
    if "matter_walls" in call:
        walls = call.get("matter_walls")
        if not isinstance(walls, dict):
            return None
        if matter_id not in walls:
            return True
        value = walls[matter_id]
        return value if type(value) is bool else None
    if call.get("matter_id") == matter_id and "matter_walled" in call:
        value = call.get("matter_walled")
        return value if type(value) is bool else None
    return True


def _batch_matter_denial(access: dict, call: dict) -> str | None:
    """Every item matter is authorized before any batch effect. One inaccessible item rejects the batch."""
    if not access.get("matter_wall"):
        return None
    items = call.get("items") if isinstance(call.get("items"), list) else None
    if not items:
        return None
    for item in items:
        if not isinstance(item, dict):
            return "not_found"
        matter_id = item.get("matter_id")
        if not matter_id:
            continue
        walled = _item_matter_walled(call, matter_id)
        if walled is None or not matter_visible(call, {"matter_id": matter_id, "matter_walled": walled}, single_record=True):
            return "not_found"
    return None


def _module_scope_intersection(call: dict) -> dict:
    if not call.get("module_id") or call.get("noninteractive_accepted"):
        return call
    if call.get("grant_revoked"):
        return call
    approved = set(call.get("approved_scopes") or [])
    if "current_approved_scopes" in call:
        approved &= set(call.get("current_approved_scopes") or [])
    narrowed = dict(call)
    narrowed["token_scopes"] = [scope for scope in call.get("token_scopes") or [] if scope in approved]
    return narrowed


def session_authorizes(call: dict) -> bool:
    """A cookie session is authorized by role. A bearer is not, including when its scope list is empty."""
    if call.get("credential") == "session":
        return True
    if call.get("credential") == "bearer":
        return False
    return bool(call.get("cookie")) and not call.get("authorization")


def bearer_lacks_scopes(call: dict, needed) -> bool:
    """True when a bearer is missing a required scope.

    Sessions skip this check. An absent scope list and an explicit empty list both deny.
    Neither falls back to another grant.
    """
    required = [scope for scope in (needed or [])]
    if not required or session_authorizes(call):
        return False
    held = set(call.get("token_scopes") or []) if "token_scopes" in call else set()
    return any(scope not in held for scope in required)


def _bearer_presented(call: dict) -> bool:
    """True when this call carries a bearer rather than a module_scopes-only probe."""
    return (
        call.get("credential") == "bearer"
        or bool(call.get("authorization"))
        or "token_scopes" in call
        or "approved_scopes" in call
        or "current_approved_scopes" in call
    )


def grant_scope_denial(call: dict, needed) -> str | None:
    """Grant workflows still require module_scopes. A presented bearer also needs the effective token.

    Empty or omitted token scopes, empty or omitted approvals, and a current approval list that
    drops the scope do not fall back to stale module_scopes. PRD 9.3.
    """
    required = [scope for scope in (needed or [])]
    if any(scope not in set(call.get("module_scopes") or []) for scope in required):
        return "scope_denied"
    if not _bearer_presented(call) or session_authorizes(call):
        return None
    subject = call if call.get("module_id") else {**call, "module_id": call.get("caller_module_id") or "_bearer"}
    if bearer_lacks_scopes(_module_scope_intersection(subject), required):
        return "scope_denied"
    return None


def _canonical_file_rows(subject: dict) -> list[dict]:
    rows: list[dict] = []
    one = subject.get("stored_file")
    if isinstance(one, dict):
        rows.append(one)
    rows.extend(row for row in subject.get("stored_files") or [] if isinstance(row, dict))
    return rows


def _file_record_request(operation: dict, subject: dict) -> bool:
    """A path file id names one stored file. Invoice PDF, archive, and link use their own rows."""
    access = operation.get("x-togavsu-access") or {}
    if subject.get("noninteractive_accepted"):
        return False
    if access.get("access_inherits") != "file":
        return False
    if operation.get("operationId") in {"getInvoicePdf", "createFileLink", "createFileArchive"}:
        return False
    file_id = subject.get("file_id")
    return isinstance(file_id, str) and bool(file_id)


def _apply_file_parent(subject: dict, row: dict) -> str | None:
    """The stored row's matter and restrictions replace caller flags.

    The row's wall is authoritative for a file-record request. A trusted matter_walls map that
    disagrees with the row for the row's matter, or is malformed, fails closed as not_found.
    Otherwise the map carries the row's value, so the wall step cannot resolve a different answer.
    """
    matter = row.get("matter_id")
    if matter is not None and "matter_walls" in subject:
        walls = subject.get("matter_walls")
        if not isinstance(walls, dict):
            return "not_found"
        if matter in walls and walls[matter] is not row["matter_walled"]:
            return "not_found"
        subject["matter_walls"] = {**walls, matter: row["matter_walled"]}
    subject["attorney_only"] = row["attorney_only"]
    subject["matter_walled"] = row["matter_walled"]
    if type(row.get("shared_with_client")) is bool:
        subject["shared"] = row["shared_with_client"]
    if matter is None:
        subject["stored_matter_state"] = "unassigned"
        subject["matter_id"] = None
        subject.pop("stored_matter_id", None)
        return None
    subject["stored_matter_state"] = "matter"
    subject["stored_matter_id"] = matter
    subject["matter_id"] = matter
    return None


def _canonical_file_row(subject: dict, file_id: str) -> tuple[dict | None, str | None]:
    """One matching trusted row, or not_found. A missing or conflicting row does not fall through."""
    matches = [row for row in _canonical_file_rows(subject) if row.get("id") == file_id or row.get("file_id") == file_id]
    if len(matches) != 1:
        return None, "not_found"
    row = matches[0]
    matter = row.get("matter_id") if "matter_id" in row else False
    if "matter_id" not in row or (matter is not None and (type(matter) is not str or not matter)):
        return None, "not_found"
    if type(row.get("matter_walled")) is not bool or type(row.get("attorney_only")) is not bool:
        return None, "not_found"
    if subject.get("_caller_matter_supplied") and subject.get("_caller_matter_id") != matter:
        return None, "not_found"
    return row, None


def _bind_file_row_restrictions(operation: dict, subject: dict) -> str | None:
    """The path file's trusted row is the parent. Caller matter and flags cannot replace it."""
    if not _file_record_request(operation, subject):
        return None
    row, denial = _canonical_file_row(subject, subject["file_id"])
    if denial:
        return denial
    return _apply_file_parent(subject, row or {})


def _trusted_client_matters(subject: dict) -> list[str] | None:
    """The authenticated client's matters. Empty, null, duplicate, or non-string membership is not a grant."""
    raw = subject.get("client_matter_ids")
    if not isinstance(raw, list) or not raw:
        return None
    if any(type(item) is not str or not item for item in raw):
        return None
    if len(set(raw)) != len(raw):
        return None
    return raw


def _invoice_client_read(spec: dict, operation: dict) -> bool:
    """Client reads of an invoice. A new read under /api/v1/invoices uses this same rule."""
    access = operation.get("x-togavsu-access") or {}
    if access.get("client") != "shared" or "client" not in (access.get("roles") or []):
        return False
    if operation.get("x-togavsu-writes"):
        return False
    path = _operation_path(spec, operation) or ""
    return path == "/api/v1/invoices" or path.startswith("/api/v1/invoices/")


def _client_context_matter_denial(subject: dict) -> str | None:
    """A requested context matter must be this client's own shared matter. No matter stays allowed.

    Caller owner and shared flags are not evidence. A missing, empty, foreign, or malformed list denies.
    """
    if not subject.get("destination_requested"):
        return None
    requested = subject.get("requested_matter_id")
    if requested is None:
        return None
    if type(requested) is not str or not requested:
        return "not_found"
    matters = _trusted_client_matters(subject)
    shares = subject.get("client_shared_matter_ids")
    if matters is None or requested not in matters:
        return "not_found"
    if not isinstance(shares, list) or not shares or any(type(item) is not str or not item for item in shares):
        return "not_found"
    if len(set(shares)) != len(shares) or requested not in shares:
        return "not_found"
    return None


def _invoice_relationship_denial(subject: dict) -> str | None:
    """None when this client is tied to the invoice matter. Caller owner and shared flags are ignored."""
    matters = _trusted_client_matters(subject)
    if matters is None:
        return "not_found"
    if "filter_matter_id" in subject:
        requested = subject.get("filter_matter_id")
        if type(requested) is not str or requested not in matters:
            return "not_found"
        return None
    if subject.get("existing_record"):
        if subject.get("stored_matter_state") != "matter" or type(subject.get("stored_matter_id")) is not str:
            return "not_found"
        if subject["stored_matter_id"] not in matters:
            return "not_found"
    return None


def operation_authorization(spec: dict, operation: dict, call: dict) -> dict:
    """One decision for the HTTP operation. MCP tools/call uses it before effects."""
    denied = {"effects": False, "public": False}
    if call.get("_credential_denial"):
        return {"result": call["_credential_denial"], **denied, "events": []}
    access = operation.get("x-togavsu-access") or {}
    unknown = [key for key in access if key not in SUPPORTED_QUALIFIERS]
    if unknown:
        return {"result": "forbidden", **denied, "unsupported": unknown}
    if access.get("admin_bypasses_wall") or access.get("firmwide_bypass") or access.get("grants_admin"):
        return {"result": "forbidden", **denied}
    subject = dict(call)
    if _is_matter_free_create(spec, operation):
        subject["existing_record"] = False
    if subject.get("module_id") and subject.get("grant_revoked"):
        return {"result": "forbidden", **denied}
    workflow = _workflow_code(spec, operation, subject)
    if workflow is not None:
        if workflow != "allowed":
            return {"result": workflow, **denied}
        subject["noninteractive_accepted"] = True
    if subject.get("module_id") and not subject.get("noninteractive_accepted"):
        if subject.get("grant_revoked"):
            return {"result": "forbidden", **denied}
        subject = _module_scope_intersection(subject)
    # Base operation scopes are mandatory. A custom policy may narrow the decision. It cannot skip this check.
    # entry_type_scopes and per-item scope lists are alternatives, not a conjunction. Their own rule selects one.
    # A noninteractive grant already applied its scopes. A session is authorized by role.
    menu = bool(access.get("entry_type_scopes") or access.get("scope_combination") == "per_item")
    if not menu and not subject.get("noninteractive_accepted") and bearer_lacks_scopes(subject, access.get("scopes") or []):
        return {"result": "scope_denied", **denied}
    principal = _principal_code(operation, subject)
    if principal is not None:
        if principal != "allowed":
            return {"result": principal, **denied}
        subject["principal_accepted"] = True
    if not subject.get("noninteractive_accepted"):
        batch = _batch_matter_denial(access, subject)
        if batch:
            return {"result": batch, **denied}
    conditional = _conditional_scope_code(spec, operation, subject)
    if conditional is not None and conditional != "allowed":
        return {"result": conditional, **denied}
    if not subject.get("noninteractive_accepted") and conditional is None and not subject.get("principal_accepted"):
        roles = access.get("roles") or []
        if not roles or subject.get("role") not in roles:
            if operation.get("operationId") in {"mergeMatter", "updateMatter", "reopenMatter"}:
                return {"result": "forbidden", **denied, "events": []}
            return {"result": "forbidden", **denied}
    if operation.get("operationId") == "mergeMatter":
        return _merge_matter_decision(subject)
    if operation.get("operationId") == "resetTotp":
        return _totp_reset_authorization(operation, subject)
    if operation.get("operationId") == "regenerateRecoveryCodes":
        return _recovery_regeneration_authorization(operation, subject)
    file_lookup = _bind_file_row_restrictions(operation, subject)
    if file_lookup:
        return {"result": file_lookup, **denied, "events": []}
    if access.get("matter_wall") and not subject.get("noninteractive_accepted"):
        variant = _variant_matter_requirement(spec, operation, subject)
        if variant == "deny":
            return {"result": "not_found", **denied, "events": []}
        matter_id = None
        if variant == "none":
            matter_id = None
        elif subject.get("existing_record"):
            state = subject.get("stored_matter_state")
            mode = _parent_matter_mode(spec, operation)
            if mode == "absent":
                matter_id = subject.get("matter_id") if isinstance(subject.get("matter_id"), str) and subject.get("matter_id") else None
            elif state == "matter" and isinstance(subject.get("stored_matter_id"), str):
                matter_id = subject["stored_matter_id"]
            elif state == "unassigned" and mode == "nullable":
                matter_id = None
            else:
                return {"result": "not_found", **denied, "events": []}
        elif isinstance(subject.get("matter_id"), str) and subject.get("matter_id"):
            matter_id = subject["matter_id"]
        if isinstance(matter_id, str):
            walled = _resolved_source_wall(subject, matter_id)
            if walled is None or not matter_visible(subject, {"matter_id": matter_id, "matter_walled": walled}, single_record=True):
                return {"result": "not_found", **denied, "events": []}
    if operation.get("operationId") in {"getTrustFlag", "resolveTrustFlag"}:
        record = subject.get("stored_record")
        if not isinstance(record, dict) or _trust_flag_source_denial(subject, record):
            return {"result": "not_found", **denied, "events": []}
    if operation.get("operationId") == "updateMatter":
        patch = _matter_patch_decision(subject)
        if patch["result"] != "allowed":
            return patch
        subject["_patch_events"] = patch["events"]
    if operation.get("operationId") == "reopenMatter":
        return _reopen_matter_decision(subject)
    owner_denial = _tagged_owner_denial(operation, subject)
    if owner_denial:
        return {"result": owner_denial, **denied}
    if access.get("attorney_only_files") == "not_found" and subject.get("attorney_only") and subject.get("role") not in ("attorney", "admin"):
        return {"result": "not_found", **denied}
    if _hides_attorney_only_record(access) and subject.get("attorney_only") and subject.get("role") not in ("attorney", "admin"):
        return {"result": "not_found", **denied}
    client = access.get("client")
    role = subject.get("role")
    if client == "deny" and role == "client":
        return {"result": "forbidden", **denied}
    if client == "not_found" and role == "client":
        return {"result": "not_found", **denied}
    if client in {"own", "own_matter"} and role == "client" and subject.get("owner") not in (None, "self"):
        return {"result": "not_found", **denied}
    if operation.get("operationId") == "createModuleContextToken" and role == "client":
        context_denial = _client_context_matter_denial(subject)
        if context_denial:
            return {"result": context_denial, **denied, "events": []}
    if _invoice_client_read(spec, operation) and role == "client":
        if _invoice_relationship_denial(subject):
            return {"result": "not_found", **denied, "events": []}
        # The relationship is the grant. A caller owner or shared flag is not.
        subject["shared"] = True
        subject["owner"] = "self"
    elif client == "shared" and role == "client" and access.get("access_inherits") == "file" and subject.get("file_id"):
        row = _stored_file_for_request(subject)
        if row is None or _client_file_denied(subject, row):
            return {"result": "not_found", **denied, "events": []}
    elif client == "shared" and role == "client" and (subject.get("owner") == "other_client" or subject.get("shared") is False):
        return {"result": "not_found", **denied}
    if access.get("quarantine_confirmation") == "required" and not subject.get("noninteractive_accepted"):
        download = file_download_decision(operation, subject)
        if download in {"not_found", "missing_event", "missing_confirm_input", "quarantine_confirmation_required", "validation_failed"}:
            return {"result": download, **denied}
    if "include_scope_pattern" in access and not subject.get("noninteractive_accepted") and not subject.get("principal_accepted"):
        viewed = include_views_decision(operation, subject)
        if viewed != "allowed":
            return {"result": viewed, **denied}
    for case in access.get("access_cases") or []:
        if _access_case_matches(case, subject) and case.get("result") not in (None, "allowed"):
            return {"result": case["result"], **denied}
    inherited = _inherited_record_denial(operation, subject)
    if inherited:
        return {"result": inherited, **denied}
    financial = _accountant_financial_denial(operation, subject)
    if financial:
        return {"result": financial, **denied}
    subject["matter_null_allowed"] = _matter_null_allowed(spec, operation)
    destination = _requested_matter_denial(subject)
    if destination:
        return {"result": destination, **denied, "events": [], "reparented": False}
    routed = _replacement_decision(operation, subject)
    if routed:
        return {**denied, **routed, "public": False}
    allowed = {"result": "allowed", "effects": True, "public": False}
    if subject.get("_patch_events") is not None:
        allowed["events"] = subject["_patch_events"]
    elif subject.get("existing_record") and subject.get("destination_requested") and subject.get("requested_matter_id") != subject.get("stored_matter_id"):
        allowed["events"] = list(operation.get("x-togavsu-events") or [])
        if subject.get("requested_matter_id") is None:
            allowed["cleared"] = True
        else:
            allowed["reparented"] = True
    return allowed


_ORDINARY_MATTER_STATUSES = {"prospect", "open", "on_hold"}


def _merge_matter_decision(subject: dict) -> dict:
    """Both matter ids come from the request. Wall state comes only from trusted matter_walls."""
    source = subject.get("matter_id")
    target = subject.get("survivor_id")
    quiet = {"effects": False, "public": False, "events": [], "relinked": False, "archived": False}
    if not isinstance(source, str) or not isinstance(target, str) or not source or not target or source == target:
        return {"result": "validation_failed", **quiet}
    walls = subject.get("matter_walls")
    if not isinstance(walls, dict) or source not in walls or target not in walls:
        return {"result": "not_found", **quiet}
    for matter_id in (source, target):
        walled = walls[matter_id]
        if type(walled) is not bool:
            return {"result": "not_found", **quiet}
        row = {"matter_id": matter_id, "matter_walled": walled}
        if not matter_visible(subject, row, single_record=True):
            return {"result": "not_found", **quiet}
    return {"result": "allowed", "effects": True, "public": False, "events": ["matter.merged"], "relinked": True, "archived": True}


def _matter_null_allowed(spec: dict, operation: dict) -> bool:
    content = ((operation.get("requestBody") or {}).get("content") or {}).get("application/json") or {}
    schema = content.get("schema") or {}
    if isinstance(schema.get("$ref"), str):
        schema = spec["components"]["schemas"].get(schema["$ref"].rsplit("/", 1)[-1]) or {}
    prop = (schema.get("properties") or {}).get("matter_id")
    if not isinstance(prop, dict):
        return False
    options = prop.get("anyOf") or prop.get("oneOf") or []
    return any(isinstance(item, dict) and item.get("type") == "null" for item in options)


def _requested_matter_denial(subject: dict) -> str | None:
    """Deny a requested matter. This never grants, and null clears only after the source gates."""
    if not subject.get("destination_requested"):
        return None
    if subject.get("existing_record") and subject.get("stored_matter_state") in {"absent", "unresolved"}:
        return "not_found"
    if subject.get("stored_matter_state") == "unresolved":
        return "not_found"
    requested = subject.get("requested_matter_id")
    if requested is None:
        return None if subject.get("matter_null_allowed") else "validation_failed"
    if not isinstance(requested, str) or not requested:
        return "not_found"
    if subject.get("stored_matter_state") == "matter" and requested == subject.get("stored_matter_id"):
        return None
    walls = subject.get("matter_walls")
    if not isinstance(walls, dict) or requested not in walls or type(walls[requested]) is not bool:
        return "not_found"
    if not matter_visible(subject, {"matter_id": requested, "matter_walled": walls[requested]}, single_record=True):
        return "not_found"
    return None


def _matter_patch_decision(subject: dict) -> dict:
    """PATCH cannot close a matter or change a closed one. reopenMatter is the only reopen path."""
    quiet = {"effects": False, "public": False, "events": []}
    stored = subject.get("stored_status")
    status = subject.get("patch_status", None)
    if stored not in _ORDINARY_MATTER_STATUSES:
        return {"result": "forbidden", **quiet}
    if "patch_status" in subject and status not in _ORDINARY_MATTER_STATUSES:
        return {"result": "validation_failed", **quiet}
    return {"result": "allowed", "effects": True, "public": False, "events": ["matter.updated"]}


def _reopen_matter_decision(subject: dict) -> dict:
    """Attorney or admin, with a reason, restores the trusted prior status. The close record stays."""
    quiet = {"effects": False, "public": False, "events": []}
    if subject.get("role") not in ("admin", "attorney"):
        return {"result": "forbidden", **quiet}
    reason = subject.get("reason")
    if not isinstance(reason, str) or not reason.strip():
        return {"result": "validation_failed", **quiet}
    if subject.get("stored_status") != "closed":
        return {"result": "validation_failed", **quiet}
    previous = subject.get("status_before_close")
    if previous not in _ORDINARY_MATTER_STATUSES:
        return {"result": "validation_failed", **quiet}
    return {
        "result": "allowed",
        "effects": True,
        "public": False,
        "events": ["matter.reopened"],
        "status": previous,
        "closed_on": subject.get("closed_on"),
        "close_reason": subject.get("close_reason"),
    }


def _inherited_record_denial(operation: dict, call: dict) -> str | None:
    """Reuse the history omission rule when the call names a record. A list with no record stays filtered by that helper."""
    if "inherited_record_access" not in (operation.get("x-togavsu-access") or {}):
        return None
    rows = call.get("rows") if isinstance(call.get("rows"), list) else None
    if rows is None and isinstance(call.get("record"), dict):
        rows = [call["record"]]
    if not rows:
        return None
    page = inherited_history_rows(operation, call, rows)
    if page.get("result") == "forbidden":
        return "forbidden"
    if isinstance(call.get("record"), dict) and not page.get("rows"):
        return "not_found"
    return None


def _access_case_matches(case: dict, call: dict) -> bool:
    for key, value in case.items():
        if key == "result":
            continue
        if call.get(key) != value:
            return False
    return True


# Copied from the authenticated call only when that key is present. Arguments never write these.
# include_views_decision reads current approvals, extensions, and published views.
# payment_reversal_decision reads the stored opening and the original ledger row.
# inherited history, notifications, and wall replacement read rows, caller_user_id, and user_ids.
_PRESERVED_CONTEXT = (
    "current_approved_scopes",
    "extensions",
    "published_views",
    "rows",
    "caller_user_id",
    "opening",
    "original_entry",
    "existing_reversal",
    "posted_entry_id",
    "trust_entry_id",
    "reversal_account_id",
    "batch_max",
    "matter_ids",
    "from_date",
    "to_date",
    "user_ids",
    "current_wall",
    "viewer_role",
    "description",
    "matter_walls",
    "stored_status",
    "status_before_close",
    "closed_on",
    "close_reason",
    "password_matches",
    "existing_totp_matches",
    "recovery_code_matches",
    "reauthentication_proof",
    "core_history",
    "client_matter_ids",
    "client_shared_matter_ids",
    "client_contact_id",
)


def _deref_schema(spec: dict, schema: dict | None) -> dict:
    if not isinstance(schema, dict):
        return {}
    ref = schema.get("$ref")
    if isinstance(ref, str) and ref.startswith("#/components/schemas/"):
        return spec.get("components", {}).get("schemas", {}).get(ref.rsplit("/", 1)[-1]) or {}
    return schema


def _response_record_schema(spec: dict, operation: dict) -> dict | None:
    responses = operation.get("responses") or {}
    for code in ("200", "201"):
        content = ((responses.get(code) or {}).get("content") or {}).get("application/json") or {}
        if isinstance(content.get("schema"), dict):
            return _deref_schema(spec, content["schema"])
    for path_item in spec.get("paths", {}).values():
        if not isinstance(path_item, dict):
            continue
        matched = False
        for method, candidate in path_item.items():
            if method.startswith("x-") or not isinstance(candidate, dict):
                continue
            if candidate.get("operationId") == operation.get("operationId"):
                matched = True
                break
        if not matched:
            continue
        getter = path_item.get("get")
        if isinstance(getter, dict) and getter.get("operationId") != operation.get("operationId"):
            return _response_record_schema(spec, getter)
        return None
    return None


def _matter_prop_mode(prop: dict) -> str:
    options = prop.get("anyOf") or prop.get("oneOf") or []
    if prop.get("type") == "null" or any(isinstance(item, dict) and item.get("type") == "null" for item in options):
        return "nullable"
    return "required"


def _collect_matter_modes(spec: dict, schema: dict, seen: set[str] | None = None) -> set[str]:
    """Matter modes from a schema, including oneOf branches, wrappers, and page items."""
    if not isinstance(schema, dict):
        return set()
    seen = seen if seen is not None else set()
    ref = schema.get("$ref")
    if isinstance(ref, str):
        if ref in seen:
            return set()
        seen.add(ref)
        schema = _deref_schema(spec, schema)
    modes: set[str] = set()
    for key in ("oneOf", "anyOf", "allOf"):
        for branch in schema.get(key) or []:
            if isinstance(branch, dict):
                modes |= _collect_matter_modes(spec, branch, seen)
    properties = schema.get("properties") or {}
    matter = properties.get("matter_id")
    if isinstance(matter, dict):
        modes.add(_matter_prop_mode(matter))
    for name, value in properties.items():
        if name == "matter_id" or not isinstance(value, dict):
            continue
        if value.get("type") in {"string", "integer", "number", "boolean"} and "$ref" not in value and "oneOf" not in value and "anyOf" not in value:
            continue
        if "items" in value and isinstance(value.get("items"), dict):
            modes |= _collect_matter_modes(spec, value["items"], seen)
        else:
            modes |= _collect_matter_modes(spec, value, seen)
    return modes


def _operation_path(spec: dict, operation: dict) -> str | None:
    for path, item in spec.get("paths", {}).items():
        if not isinstance(item, dict):
            continue
        for method, candidate in item.items():
            if method.startswith("x-") or not isinstance(candidate, dict):
                continue
            if candidate.get("operationId") == operation.get("operationId"):
                return path
    return None


def _parent_item_schema(spec: dict, operation: dict) -> dict | None:
    """The item GET behind a history or action route. The item itself has no further parent."""
    path = _operation_path(spec, operation)
    if not path or "{" not in path or path.endswith("}"):
        return None
    parts = path.split("/")
    indexes = [index for index, part in enumerate(parts) if part.startswith("{") and part.endswith("}")]
    if not indexes:
        return None
    parent_path = "/".join(parts[: indexes[-1] + 1])
    parent = spec.get("paths", {}).get(parent_path, {}).get("get")
    if not isinstance(parent, dict):
        parent_path = "/".join(parts[: indexes[0] + 1])
        parent = spec.get("paths", {}).get(parent_path, {}).get("get")
    if isinstance(parent, dict) and parent.get("operationId") != operation.get("operationId"):
        return _response_record_schema(spec, parent)
    return None


def _parent_matter_mode(spec: dict, operation: dict) -> str:
    """required, nullable, or absent. A wrapper or oneOf does not make a matter-bound record matter-free.

    Scope-discriminated records do not use this union. _variant_matter_requirement reads the trusted variant.
    """
    modes = _collect_matter_modes(spec, _response_record_schema(spec, operation) or {})
    parent = _parent_item_schema(spec, operation)
    if parent:
        modes |= _collect_matter_modes(spec, parent)
    if "required" in modes:
        return "required"
    if "nullable" in modes:
        return "nullable"
    if (operation.get("x-togavsu-access") or {}).get("access_inherits") == "file":
        return "nullable"
    return "absent"


def _method_and_path(spec: dict, operation: dict) -> tuple[str | None, str | None]:
    for path, item in spec.get("paths", {}).items():
        if not isinstance(item, dict):
            continue
        for method, candidate in item.items():
            if method.startswith("x-") or not isinstance(candidate, dict):
                continue
            if candidate.get("operationId") == operation.get("operationId"):
                return method, path
    return None, None


def _record_schema_has_matter(spec: dict, schema: dict) -> bool:
    """True when this record itself has matter_id. Nested wrappers are not a parent matter."""
    schema = _deref_schema(spec, schema)
    branches = schema.get("oneOf") or schema.get("anyOf") or []
    if branches:
        return any(_record_schema_has_matter(spec, branch) for branch in branches if isinstance(branch, dict))
    return "matter_id" in (schema.get("properties") or {})


def _is_matter_free_create(spec: dict, operation: dict) -> bool:
    """A create whose parent resource stores no matter. The path id is not a stored matter target."""
    method, path = _method_and_path(spec, operation)
    if method != "post" or not str(operation.get("operationId") or "").startswith("create"):
        return False
    if not path or "{" not in path:
        return False
    parent = _parent_item_schema(spec, operation)
    if not isinstance(parent, dict):
        return False
    return not _record_schema_has_matter(spec, parent)


def _scope_variants(spec: dict, schema: dict) -> dict[str, dict] | None:
    """scope const branches, or None when the record is not a scope discriminator."""
    schema = _deref_schema(spec, schema)
    branches = schema.get("oneOf")
    if not isinstance(branches, list) or len(branches) < 2:
        return None
    found: dict[str, dict] = {}
    for branch in branches:
        if not isinstance(branch, dict):
            return None
        branch = _deref_schema(spec, branch)
        scope = (branch.get("properties") or {}).get("scope")
        if not isinstance(scope, dict) or type(scope.get("const")) is not str:
            return None
        found[scope["const"]] = branch
    return found or None


def _variant_matter_requirement(spec: dict, operation: dict, subject: dict) -> str | None:
    """matter, none, or deny for a scope-discriminated existing record. None for every other operation.

    The trusted stored record selects the branch. An unknown or mismatched branch fails closed.
    """
    if not subject.get("existing_record"):
        return None
    variants = _scope_variants(spec, _response_record_schema(spec, operation) or {})
    if not variants:
        return None
    record = subject.get("stored_record")
    if not isinstance(record, dict):
        return "deny"
    scope = record.get("scope")
    branch = variants.get(scope) if type(scope) is str else None
    if branch is None:
        return "deny"
    props = branch.get("properties") or {}
    matter = record.get("matter_id", None)
    if "matter_id" in props:
        client = record.get("client_id")
        if type(matter) is not str or not matter or ("client_id" in props and (type(client) is not str or not client)):
            return "deny"
        subject["stored_matter_state"] = "matter"
        subject["stored_matter_id"] = matter
        subject["matter_id"] = matter
        if type(record.get("matter_walled")) is bool and "matter_walled" not in subject:
            subject["matter_walled"] = record["matter_walled"]
        return "matter"
    if type(matter) is str and matter:
        return "deny"
    for name in ("client_id", "trust_account_id"):
        if name not in props:
            continue
        value = record.get(name)
        if type(value) is not str or not value:
            return "deny"
    return "none"


def _credential_kind(call: dict) -> str | None:
    named = call.get("credential")
    if named in {"session", "bearer"}:
        return named
    if call.get("cookie") and not call.get("authorization"):
        return "session"
    if call.get("authorization"):
        return "bearer"
    return None


def mcp_credential_rejection(arguments: dict) -> str | None:
    planted = ("Authorization", "Host", "roles", "role", "on_wall", "matter_membership", "token_scopes", "scopes", "approved_scopes")
    if any(name in arguments for name in planted):
        return "forbidden"
    headers = arguments.get("headers") or {}
    if any(str(name).lower() in {"authorization", "host"} for name in headers):
        return "forbidden"
    return None


def _task_grant(call: dict, arguments: dict) -> tuple[dict | None, str | None]:
    """Opaque task credential. Verified claims come only from trusted context.

    A dictionary, boolean, or other caller value is not a grant and cannot replace one.
    task_verifier["valid"] must be True and its credential must be the presented string.
    This model does not check a signature, a clock, or whether the credential was already used.
    Go verifies the binding to the operation, module, file, version, expiry, and revocation,
    then passes that explicit result.
    """
    if "task_authorization" not in arguments:
        existing = call.get("grant")
        return (existing, None) if isinstance(existing, dict) else (None, None)
    presented = arguments.get("task_authorization")
    if not isinstance(presented, str) or not presented:
        return None, "forbidden"
    verifier = call.get("task_verifier")
    if not isinstance(verifier, dict) or verifier.get("valid") is not True or verifier.get("credential") != presented:
        return None, "forbidden"
    claims = verifier.get("claims")
    if not isinstance(claims, dict):
        return None, "forbidden"
    existing = call.get("grant")
    if isinstance(existing, dict) and existing != claims:
        return None, "forbidden"
    return claims, None


def _transport_csrf_token(call: dict) -> str | None:
    """The X-CSRF-Token value captured from the outer HTTP request, not from tool arguments."""
    headers = call.get("transport_headers")
    if not isinstance(headers, dict):
        return None
    for name, value in headers.items():
        if str(name).lower() == "x-csrf-token" and isinstance(value, str) and value:
            return value
    return None


def _cookie_csrf_denial(call: dict, arguments: dict) -> str | None:
    """Cookie writes need the transport header and a trusted verifier result.

    arguments is not a source. A tool-argument header or a caller valid flag is not the HTTP header.
    The model does not mint, expire, or bind the token to a session. Go does that and passes valid.
    """
    del arguments
    presented = _transport_csrf_token(call)
    verifier = call.get("csrf_verifier")
    if not isinstance(verifier, dict) or verifier.get("valid") is not True:
        return "csrf_required"
    token = verifier.get("token")
    if not isinstance(presented, str) or not isinstance(token, str) or presented != token:
        return "csrf_required"
    return None


def _configured_matter(configured, matter_id: str | None) -> bool:
    """A matter grant needs an explicit trusted list that contains that matter. Null is not public."""
    if not isinstance(matter_id, str) or not matter_id or not isinstance(configured, list) or not configured:
        return False
    if any(not isinstance(item, str) or not item for item in configured):
        return False
    return matter_id in configured


def _trusted_file(records: list, file_id) -> dict | None:
    """One trusted file row whose id is the requested id. A missing or non-boolean flag is unresolved."""
    if not isinstance(file_id, str) or not file_id:
        return None
    matches = [row for row in records if isinstance(row, dict) and row.get("id") == file_id]
    if len(matches) != 1:
        return None
    row = matches[0]
    matter = row.get("matter_id")
    if "matter_id" not in row or (matter is not None and (not isinstance(matter, str) or not matter)):
        return None
    if type(row.get("matter_walled")) is not bool or type(row.get("attorney_only")) is not bool:
        return None
    return row


def _trusted_file_visible(subject: dict, row: dict | None) -> bool:
    if row is None:
        return False
    if row.get("attorney_only") and subject.get("role") not in ("attorney", "admin"):
        return False
    if _client_file_denied(subject, row):
        return False
    return matter_visible(subject, {"matter_id": row.get("matter_id"), "matter_walled": row["matter_walled"]}, single_record=isinstance(row.get("matter_id"), str))


def _bind_file_link(subject: dict, call: dict, path: dict, body: dict) -> None:
    """Both file ids come from the request. Visibility and matter come only from trusted rows."""
    records = [row for row in call.get("stored_files") or [] if isinstance(row, dict)]
    source = _trusted_file(records, path.get("file_id"))
    target = _trusted_file(records, body.get("to_file_id"))
    subject["to_file_id"] = body.get("to_file_id") if isinstance(body.get("to_file_id"), str) else None
    subject["relationship"] = body.get("relationship") if isinstance(body.get("relationship"), str) else None
    subject["from_visible"] = _trusted_file_visible(subject, source)
    subject["to_visible"] = _trusted_file_visible(subject, target)
    if source is None or target is None:
        subject["same_matter"] = False
        subject["shared"] = False
        return
    if subject.get("_caller_matter_supplied") and subject.get("_caller_matter_id") != source.get("matter_id"):
        subject["stored_matter_state"] = "unresolved"
        subject["from_visible"] = False
        subject["to_visible"] = False
        return
    subject["attorney_only"] = bool(source["attorney_only"] or target["attorney_only"])
    source_matter = source.get("matter_id")
    target_matter = target.get("matter_id")
    subject["same_matter"] = not (isinstance(source_matter, str) and isinstance(target_matter, str) and source_matter != target_matter)
    subject["shared"] = source.get("shared_with_client") is True and target.get("shared_with_client") is True
    if "matter_id" in call and call.get("matter_id") != source_matter:
        subject["stored_matter_state"] = "unresolved"
        return
    if source_matter is None:
        subject["stored_matter_state"] = "unassigned"
        subject["matter_id"] = None
    else:
        subject["stored_matter_state"] = "matter"
        subject["stored_matter_id"] = source_matter
        subject["matter_id"] = source_matter
    subject["matter_walled"] = source["matter_walled"]


def _mcp_operation_call(call: dict, arguments: dict, operation: dict) -> dict:
    """Credential context plus path, query, and body. Arguments cannot supply a role, wall, or scope."""
    path = arguments.get("path") if isinstance(arguments.get("path"), dict) else {}
    query = arguments.get("query") if isinstance(arguments.get("query"), dict) else {}
    body = arguments.get("body") if isinstance(arguments.get("body"), dict) else {}
    items = body.get("items") if isinstance(body.get("items"), list) else None
    grant, credential_denial = _task_grant(call, arguments)
    principal = operation["x-togavsu-access"].get("module_principal") or {}
    action = {"createModuleMarker": "create", "updateModuleMarker": "update", "revokeModuleMarker": "revoke"}.get(operation["operationId"])
    subject = {
        "role": call.get("role"),
        "token_scopes": list(call.get("token_scopes") or []),
        "approved_scopes": list(call.get("approved_scopes") or []),
        "module_scopes": list(call.get("module_scopes") or []),
        "module_id": call.get("module_id"),
        "caller_module_id": call.get("caller_module_id") or call.get("module_id"),
        "grant_revoked": call.get("grant_revoked"),
        "human_session": bool(call.get("human_session") or call.get("role")),
        "viewer_present": bool(call.get("viewer_present")),
        "grant": grant,
        "_credential_denial": credential_denial,
        "operation": operation["operationId"],
        "registered": call.get("registered", False),
        "subscriptions": list(call.get("subscriptions") or []),
        "installed": call.get("installed"),
        "token_revoked": call.get("token_revoked"),
        "survivor_id": body.get("survivor_id") if isinstance(body.get("survivor_id"), str) else None,
        "matter_membership": call.get("matter_membership"),
        "on_wall": call.get("on_wall"),
        "owner": call.get("owner"),
        "record": call.get("record"),
        "attorney_only": call.get("attorney_only"),
        "shared": call.get("shared"),
        "file_id": path.get("file_id") or body.get("file_id") or call.get("file_id"),
        "stored_file": call.get("stored_file") if isinstance(call.get("stored_file"), dict) else None,
        "stored_files": [row for row in call.get("stored_files") or [] if isinstance(row, dict)],
        "version_id": path.get("version_id") or body.get("version_id") or call.get("version_id"),
        "upload_id": path.get("upload_id") or call.get("upload_id"),
        "field": call.get("field") or query.get("field"),
        "path_module_id": path.get(principal.get("module_id_param") or "module_id"),
        "heading": body.get("heading", call.get("heading")),
        "target": body.get("target", call.get("target")),
        "owner_module_id": body.get("owner_module_id"),
        "action": action or call.get("action"),
        "marker_id": path.get("marker_id") or body.get("marker_id") or call.get("marker_id"),
        "created_at": call.get("created_at"),
        "created_by": call.get("created_by"),
        "entry_type": body.get("entry_type", call.get("entry_type")),
        "destination": body.get("destination", call.get("destination")),
        "trust_account_id": body.get("trust_account_id", call.get("trust_account_id")),
        "trust_accounts": call.get("trust_accounts"),
        "firm_id": call.get("firm_id"),
        "amount_cents": body.get("amount_cents", call.get("amount_cents")),
        "processor_module_id": body.get("processor_module_id", call.get("processor_module_id")),
        "initialization_id": call.get("initialization_id"),
        "reserved_payment_id": call.get("reserved_payment_id"),
        "recipient_id": call.get("recipient_id"),
        "contact_id": body.get("contact_id", call.get("contact_id")),
        "method": body.get("method", call.get("method")),
        "payment_id": path.get("payment_id") or call.get("payment_id"),
        "client_id": body.get("client_id", call.get("client_id")),
        "owner_user_id": body.get("owner_user_id", call.get("owner_user_id")),
        "name": body.get("name", call.get("name")),
        "requested_scopes": list(body["scopes"]) if isinstance(body.get("scopes"), list) else list(call.get("requested_scopes") or []),
        "credential": _credential_kind(call),
        "caller_scopes": list(call.get("caller_scopes") or []),
        "owner_role_scopes": list(call.get("owner_role_scopes") or []),
        "module_approved_scopes": list(call.get("module_approved_scopes") or []),
        "balances": list(call.get("balances") or []) if isinstance(call.get("balances"), list) else [],
        "trust_linked": call.get("trust_linked"),
        "domain_handlers": call.get("domain_handlers"),
        "include": published_include(query["include"]) if "include" in query else published_include(call.get("include")),
        "stored_definition": dict(call["stored_definition"]) if isinstance(call.get("stored_definition"), dict) else None,
        "stored_definitions": [dict(row) for row in call.get("stored_definitions") or [] if isinstance(row, dict)],
        "confirm": _requested_confirmation(operation, query, body, call),
        "quarantined": call.get("quarantined"),
        "principal_user_id": call.get("principal_user_id") or call.get("user_id"),
        "user_id": call.get("user_id"),
        "source": body.get("source", call.get("source")),
        "assignment": body.get("assignment", call.get("assignment")),
        "expires_at": call.get("expires_at") or body.get("expires_at"),
        "tenant_id": call.get("tenant_id"),
        "principal_matter_ids": call.get("principal_matter_ids"),
        "queue": body.get("queue", call.get("queue")),
        "replay": bool(call.get("replay")),
    }
    if "matter_walled" in call:
        subject["matter_walled"] = call.get("matter_walled")
    if subject.get("credential") == "session":
        subject.pop("token_scopes", None)
    elif "token_scopes" in call:
        subject["token_scopes"] = list(call.get("token_scopes") or [])
    elif subject.get("credential") == "bearer":
        subject["token_scopes"] = []
    if "approved_scopes" in call:
        subject["approved_scopes"] = list(call.get("approved_scopes") or [])
    else:
        subject.pop("approved_scopes", None)
    if "module_scopes" in call:
        subject["module_scopes"] = list(call.get("module_scopes") or [])
    else:
        subject.pop("module_scopes", None)
    for key in _PRESERVED_CONTEXT:
        if key in call:
            subject[key] = call[key]
    if isinstance(call.get("stored_record"), dict):
        subject["stored_record"] = dict(call["stored_record"])
    if operation.get("operationId") == "listInvoice" and "matter_id" in query:
        subject["filter_matter_id"] = query.get("matter_id")
    path_matter = path.get("matter_id") if isinstance(path.get("matter_id"), str) else None
    resource_update = any(key != "matter_id" for key in path)
    subject["existing_record"] = resource_update
    if "matter_id" in call:
        subject["_caller_matter_supplied"] = True
        subject["_caller_matter_id"] = call.get("matter_id")
        stored_value = call.get("matter_id")
        if stored_value is None:
            subject["stored_matter_state"] = "unassigned"
        elif isinstance(stored_value, str) and stored_value:
            subject["stored_matter_state"] = "matter"
            subject["stored_matter_id"] = stored_value
        else:
            subject["stored_matter_state"] = "unresolved"
    else:
        record = call.get("stored_file") if isinstance(call.get("stored_file"), dict) else None
        if record is None and isinstance(call.get("stored_record"), dict):
            record = call["stored_record"]
        if isinstance(record, dict) and "matter_id" in record:
            stored_value = record.get("matter_id")
            if stored_value is None:
                subject["stored_matter_state"] = "unassigned"
            elif isinstance(stored_value, str) and stored_value:
                subject["stored_matter_state"] = "matter"
                subject["stored_matter_id"] = stored_value
            else:
                subject["stored_matter_state"] = "unresolved"
            if "matter_walled" not in subject and type(record.get("matter_walled")) is bool:
                subject["matter_walled"] = record["matter_walled"]
        else:
            subject["stored_matter_state"] = "absent"
    if "matter_id" in body and (resource_update or "matter_id" in call):
        subject["destination_requested"] = True
        subject["requested_matter_id"] = body.get("matter_id")
    if path_matter:
        subject["matter_id"] = path_matter
    elif subject.get("stored_matter_state") == "matter":
        subject["matter_id"] = subject["stored_matter_id"]
    elif not subject.get("destination_requested") and isinstance(body.get("matter_id"), str):
        subject["matter_id"] = body.get("matter_id")
    if "status" in body:
        subject["patch_status"] = body.get("status")
    if "reason" in body and operation.get("operationId") == "reopenMatter":
        subject["reason"] = body.get("reason")
    for name in ("current_password", "totp_code", "recovery_code", "reauthentication_token"):
        if name in body:
            subject[name] = body.get(name)
    if isinstance(body, dict) and "scope" in body:
        subject["scope"] = body["scope"]
    elif "scope" in call:
        subject["scope"] = call["scope"]
    if "idempotency_key" in call:
        subject["idempotency_key"] = call["idempotency_key"]
    elif isinstance(arguments.get("idempotency_key"), str):
        subject["idempotency_key"] = arguments["idempotency_key"]
    if isinstance(body.get("module_id"), str):
        subject["bound_module_id"] = body["module_id"]
    for name in ("label", "color", "target"):
        if name in body or name in call:
            subject[name] = body[name] if name in body else call.get(name)
    if items is not None:
        subject["items"] = items
    elif isinstance(call.get("items"), list):
        subject["items"] = call["items"]
    if "status" in body or "status" in call:
        subject["status"] = body["status"] if "status" in body else call.get("status")
    if "invoice_id" in body or "invoice_id" in call:
        subject["invoice_id"] = body["invoice_id"] if "invoice_id" in body else call.get("invoice_id")
    if operation.get("operationId") == "createFileLink":
        _bind_file_link(subject, call, path, body)
    if operation.get("operationId") == "createFileArchive":
        _bind_archive(subject, call, body)
    return subject


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
    if operation is not None and operation["x-togavsu-mcp"].get("parity") == "alias":
        canonical_id = operation["x-togavsu-mcp"].get("canonical")
        operation = next((candidate for _method, _path, candidate in iter_operations(spec) if candidate["operationId"] == canonical_id), None)
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
    if presented_cookie and not presented_bearer and operation.get("x-togavsu-writes") and _cookie_csrf_denial(call, arguments):
        return {"result": "csrf_required", **denied}
    decision = operation_authorization(spec, operation, _mcp_operation_call(call, arguments, operation))
    forwarded = {
        key: decision[key]
        for key in ("routed", "events", "relinked", "archived", "status", "closed_on", "close_reason", "reparented", "cleared", "challenge_issued", "codes_issued", "old_secret_active")
        if key in decision
    }
    return {"result": decision["result"], "effects": decision.get("effects", False), "public": False, **forwarded}


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


def _ledger_delta(item: dict) -> int | None:
    """Signed effect of one schema-valid row. A withdrawal amount is nonnegative; entry_type supplies the direction."""
    amount = item.get("amount_cents")
    if isinstance(amount, bool) or not isinstance(amount, int):
        return None
    kind = item.get("entry_type")
    if kind == "deposit":
        return amount if amount >= 0 else None
    if kind in {"withdrawal", "transfer_to_operating", "refund"}:
        return -amount if amount >= 0 else None
    if kind == "adjustment":
        if amount == 0:
            return None
        direction = item.get("adjustment_direction")
        if amount > 0 and direction not in (None, "credit"):
            return None
        if amount < 0 and direction not in (None, "debit"):
            return None
        return amount
    if kind == "reversal":
        if amount == 0:
            return None
        direction = item.get("direction")
        if amount < 0 and direction not in (None, "debit"):
            return None
        if amount > 0 and direction not in (None, "credit"):
            return None
        return amount
    return None


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
        amount = _ledger_delta(item)
        if amount is None:
            continue
        opening = item.get("opening") if isinstance(item.get("opening"), dict) else {}
        matter_id = item.get("matter_id")
        client_id = item.get("client_id")
        account_id = item.get("trust_account_id")
        matter_key = (client_id, matter_id)
        account_key = (account_id,)
        if matter_key not in matter_bal:
            matter_bal[matter_key] = _balance_value(opening.get("matter_cents"))
        if client_id not in client_bal:
            client_bal[client_id] = _balance_value(opening.get("client_cents"))
        if account_key not in account_bal:
            account_bal[account_key] = _balance_value(opening.get("account_cents"))
        matter_bal[matter_key] += amount
        client_bal[client_id] += amount
        account_bal[account_key] += amount
        balances = {
            "matter": matter_bal[matter_key],
            "client": client_bal[client_id],
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
            "balances": {
                "matter_cents": balances["matter"],
                "client_cents": balances["client"],
                "account_cents": balances["account"],
            },
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
_CUSTOM_FIELD_READ = {
    "contact": ["contacts:read"],
    "matter": ["matters:read"],
    "time_entry": ["time:read"],
    "expense": ["expenses:read"],
}

EXTENSIBLE_RECORDS = {schema: scope for _key, schema, scope, _reads in CORE_EXTENSION_TARGETS}


_HISTORY_REDACTED_FIELDS = ("old_value", "new_value", "summary", "reason", "excerpt")


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
        event_name = _event_name(row)
        published = event_read_scopes(event_name) if event_name else []
        needed = list(published) if published else list(row.get("required_scopes") or [])
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
    requested = list(call["requested_scopes"]) if "requested_scopes" in call else list(call.get("scopes") or [])
    if not requested:
        return {"result": "validation_failed", "token": None, "secret": None}
    # tokens:write is the operation scope. It is not implied by the child scope being inside the same credential.
    if call.get("credential") == "bearer" or "token_scopes" in call:
        issuing = set(call.get("token_scopes") or [])
    else:
        issuing = set(call.get("caller_scopes") or [])
    if any(scope not in issuing for scope in (access.get("scopes") or [])):
        return {"result": "scope_denied", "token": None, "secret": None}
    effective = set(call.get("token_scopes") or []) if call.get("credential") == "bearer" else set(call.get("caller_scopes") or [])
    if any(scope not in effective for scope in requested):
        return {"result": "scope_denied", "token": None, "secret": None}
    owner = call.get("owner_user_id")
    module_id = call.get("bound_module_id")
    if module_id is None and not call.get("caller_module_id"):
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
    if bearer_lacks_scopes(call, ["matters:write"]):
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
    owner = canonical_owner(source.get("owner"))
    if owner:
        return owner
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
        if not _configured_matter(job.get("principal_matter_ids"), job.get("matter_id")):
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


def background_grant_authorization(policy: dict, call: dict) -> str:
    """Bounded publication grant. The same checks admit a background file step and its MCP tool."""
    grant = call.get("grant")
    if not isinstance(grant, dict):
        return "forbidden"
    if grant.get("expired"):
        return policy["expired"]
    if grant.get("revoked"):
        return policy["revoked"]
    if grant.get("issued_by") != policy["issued_by"] or grant.get("module_id") != call.get("caller_module_id"):
        return policy["wrong_module"]
    if grant_scope_denial(call, policy["scopes"]):
        return policy["missing_scope"]
    if call.get("operation") not in grant.get("allowed_operations", []):
        return policy["operation_outside_grant"]
    if call.get("matter_id") != grant.get("matter_id"):
        return policy["wrong_matter"]
    if call.get("matter_walled") and not matter_visible(call, {"matter_id": call.get("matter_id"), "matter_walled": True}, single_record=True):
        return policy["wrong_matter"]
    operation_id = call.get("operation")
    if operation_id not in {"createFileUpload", "bulkFile"} and call.get("upload_id") not in grant.get("upload_ids", []):
        return "not_found"
    return "allowed"


def background_file_step(store: dict, policy: dict, operation: dict, call: dict) -> dict:
    store.setdefault("uploads", {})
    store.setdefault("files", {})
    store.setdefault("events", [])
    before_files = len(store["files"])
    before_events = len(store["events"])
    admitted = background_grant_authorization(policy, call)
    if admitted != "allowed":
        return _background_denied(store, before_files, before_events, admitted)
    grant = call["grant"]
    operation_id = call["operation"]
    upload_id = call.get("upload_id")
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


def _notification_scope(access: dict, call: dict) -> str | None:
    """A presented bearer must hold the operation scope. A session call is authorized by role."""
    if session_authorizes(call):
        return None
    if "token_scopes" not in call and call.get("credential") != "bearer":
        return None
    needed = list(access.get("scopes") or [])
    held = set(call.get("token_scopes") or [])
    if call.get("module_id"):
        held &= set(call.get("approved_scopes") or [])
    if any(scope not in held for scope in needed):
        return "scope_denied"
    return None


def notification_recipient_decision(operation: dict, call: dict) -> str:
    """Reads and read-state changes are the caller's own rows. Creation may name another recipient."""
    access = operation["x-togavsu-access"]
    if access.get("creation_separate") or access.get("recipient") == "named_on_create":
        if call.get("role") not in access.get("roles", []):
            return "forbidden"
        denied = _notification_scope(access, call)
        if denied:
            return denied
        return "allowed"
    if access.get("owner") != "self" or access.get("admin_sees_others"):
        return "forbidden"
    if call.get("role") not in access.get("roles_bound", []):
        return "forbidden"
    denied = _notification_scope(access, call)
    if denied:
        return denied
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
    if call.get("caller_module_id"):
        module_scopes = call.get("module_scopes") if "module_scopes" in call else []
        if any(scope not in module_scopes for scope in access.get("scopes", [])):
            return {"result": "scope_denied", **unchanged}
    elif bearer_lacks_scopes(call, access.get("scopes", [])):
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


def external_effect_decision(operation: dict, call: dict) -> dict:
    """Refuse the provider, the log, and the byte copy before they start. HTTP and MCP share this result."""
    access = operation["x-togavsu-access"]
    effects = list(access.get("external_effects") or [])
    silent = {name: False for name in effects}
    denied = {"effects": silent, "provider_invoked": False, "log_written": False, "mutated": False}
    if call.get("role") not in access.get("roles", []):
        return {"result": "forbidden", **denied}
    if bearer_lacks_scopes(call, access.get("scopes") or []):
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


def published_include(value) -> list[str]:
    """The published include parameter is a comma-separated string. A list is the same names."""
    if value is None:
        return []
    if isinstance(value, str):
        return [part.strip() for part in value.split(",") if part.strip()]
    if isinstance(value, list):
        names: list[str] = []
        for item in value:
            if not isinstance(item, str):
                return ["*"]
            names.extend(part.strip() for part in item.split(",") if part.strip())
        return names
    return ["*"]


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
    for view in published_include(call.get("include")):
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
        # A presented bearer also needs that view scope on the token. Approvals alone do not add it.
        if (call.get("credential") == "bearer" or "token_scopes" in call) and not session_authorizes(call):
            if bearer_lacks_scopes(call, [needed]):
                return "scope_denied"
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


def _reauthentication_denial(call: dict, action: str) -> str | None:
    """None only for an explicit trusted verifier outcome.

    This model does not read a clock and does not consume a proof. The Go identity
    owner checks the owner, the action, freshness, and that the proof is unused,
    then sets password_matches, existing_totp_matches, recovery_code_matches, or
    reauthentication_proof["valid"] to True. A missing or unknown flag is not success.
    A request boolean is ignored.
    """
    token = call.get("reauthentication_token")
    if isinstance(token, str) and token:
        proof = call.get("reauthentication_proof")
        if not isinstance(proof, dict) or proof.get("token") != token:
            return "unauthenticated"
        if proof.get("used") or proof.get("expired"):
            return "unauthenticated"
        if proof.get("owner_user_id") != call.get("caller_user_id"):
            return "forbidden"
        if proof.get("action") != action:
            return "forbidden"
        if proof.get("valid") is not True:
            return "unauthenticated"
        return None
    if isinstance(call.get("current_password"), str) and call.get("current_password"):
        return None if call.get("password_matches") is True else "unauthenticated"
    if isinstance(call.get("totp_code"), str) and call.get("totp_code"):
        return None if call.get("existing_totp_matches") is True else "unauthenticated"
    if isinstance(call.get("recovery_code"), str) and call.get("recovery_code"):
        return None if call.get("recovery_code_matches") is True else "unauthenticated"
    return "unauthenticated"


def _totp_reset_authorization(operation: dict, call: dict) -> dict:
    decision = start_totp_reset(operation, call)
    issued = decision["result"] == "allowed"
    return {
        "result": decision["result"],
        "effects": issued,
        "public": False,
        "events": ["user.totp_reset_started"] if issued else [],
        "old_secret_active": decision["old_secret_active"],
        "activated": decision["activated"],
        "challenge_issued": issued,
    }


def _recovery_regeneration_authorization(operation: dict, call: dict) -> dict:
    decision = regenerate_recovery_codes(operation, call)
    issued = decision["result"] == "allowed"
    return {
        "result": decision["result"],
        "effects": issued,
        "public": False,
        "events": ["user.recovery_codes_regenerated"] if issued else [],
        "codes_issued": issued,
    }


def regenerate_recovery_codes(operation: dict, call: dict) -> dict:
    """Replace recovery codes only after recent reauthentication. The new codes are synthetic in this model."""
    quiet = {"codes": None}
    access = operation["x-togavsu-access"]
    denial = _reauthentication_denial(call, "regenerateRecoveryCodes")
    if denial:
        return {"result": denial, **quiet}
    if call.get("role") not in access.get("roles", []) or call.get("owner") not in (None, "self"):
        return {"result": "forbidden", **quiet}
    if call.get("user_id") not in (None, call.get("caller_user_id")):
        return {"result": "not_found", **quiet}
    return {"result": "allowed", "codes": ["synthetic-recovery-1", "synthetic-recovery-2"]}


def start_totp_reset(operation: dict, call: dict) -> dict:
    """Begin a reset without retiring the current secret. The challenge token is single-use and expires."""
    denied = {"result": "forbidden", "old_secret_active": True, "activated": False, "challenge": None}
    access = operation["x-togavsu-access"]
    denial = _reauthentication_denial(call, "resetTotp")
    if denial:
        return {"result": denial, "old_secret_active": True, "activated": False, "challenge": None}
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


def _trust_openings(row: dict) -> dict:
    """Matter, client, and account openings stay separate. A lone balance_cents is only the fallback for a dimension the row does not name."""
    nested = row.get("opening") if isinstance(row.get("opening"), dict) else {}

    def cents(name: str) -> int:
        for source in (nested, row):
            value = source.get(name)
            if isinstance(value, int) and not isinstance(value, bool):
                return value
        value = row.get("balance_cents", 0)
        return value if isinstance(value, int) and not isinstance(value, bool) else 0

    return {"matter_cents": cents("matter_cents"), "client_cents": cents("client_cents"), "account_cents": cents("account_cents")}


def trust_debit_decision(call: dict) -> dict:
    """Select the client and trust account before any ledger post. Denial changes nothing."""
    denied = {"ledger_mutated": False, "debited_account_id": None, "amount_cents": 0, "events": []}
    if call.get("role") not in ("admin", "attorney"):
        return {"result": "forbidden", **denied}
    if not matter_visible(call, call, single_record=True):
        return {"result": "not_found", **denied}
    if bearer_lacks_scopes(call, ["trust:withdraw"]):
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
    debit = abs(amount) if isinstance(amount, int) and not isinstance(amount, bool) else 0
    opening = _trust_openings(row)
    events = trust_ledger_events(
        {
            "operationId": "trustDebit",
            "x-togavsu-events": ["trust.entry_posted", "trust.flag_raised"],
            "x-togavsu-access": {"trust_flag": _TRUST_FLAG},
        },
        [
            {
                "entry_type": "transfer_to_operating",
                "amount_cents": debit,
                "opening": opening,
                "matter_id": call.get("matter_id"),
                "client_id": call.get("client_id"),
                "trust_account_id": row["trust_account_id"],
                "trust_entry_id": call.get("trust_entry_id"),
                "trust_posted": True,
            }
        ],
        committed=True,
    )
    posted = next((event for event in events if event.get("type") == "trust.entry_posted"), {})
    return {
        "result": "allowed",
        "ledger_mutated": True,
        "debited_account_id": row["trust_account_id"],
        "client_id": row["client_id"],
        "amount_cents": call.get("amount_cents"),
        "below_zero": any(event.get("type") == "trust.flag_raised" for event in events),
        "events": events,
        "balances": dict(posted.get("balances") or {}),
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
    if bearer_lacks_scopes(call, ["invoices:write"]):
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


def _trust_flag_source_denial(caller: dict, flag: dict) -> str | None:
    """The originating ledger matter must be visible. Scope does not erase that wall.

    source is trusted context. A body copy of it is not this argument.
    """
    if not isinstance(flag, dict):
        return "not_found"
    entry_id = flag.get("trust_entry_id")
    source = flag.get("source") if isinstance(flag.get("source"), dict) else None
    if not isinstance(entry_id, str) or not entry_id or source is None or source.get("trust_entry_id") != entry_id:
        return "not_found"
    matter_id = source.get("matter_id")
    if not isinstance(matter_id, str) or not matter_id or type(source.get("matter_walled")) is not bool:
        return "not_found"
    if flag.get("scope") == "matter" and flag.get("matter_id") != matter_id:
        return "not_found"
    if not matter_visible(caller, {"matter_id": matter_id, "matter_walled": source["matter_walled"]}, single_record=True):
        return "not_found"
    return None


def trust_flag_rows(caller: dict, flags: list[dict], accounts: list[dict], query: dict | None = None) -> dict:
    """Every scope is authorized from the source ledger entry before a row, count, or cursor is returned."""
    if caller.get("role") not in {"admin", "attorney", "staff", "accountant"}:
        return {"result": "forbidden", "rows": [], "total": 0, "next_cursor": None}
    if bearer_lacks_scopes(caller, ["trust:read"]):
        return {"result": "scope_denied", "rows": [], "total": 0, "next_cursor": None}
    query = query or {}
    current = {
        row.get("id"): row
        for row in accounts
        if row.get("active") is True and row.get("authorized") is True and row.get("firm_id") == caller.get("firm_id")
    }
    visible = []
    hidden: set[str] = set()

    def conceal(flag: dict, source: dict | None = None) -> None:
        for value in (flag.get("id"), flag.get("trust_entry_id"), (source or {}).get("trust_entry_id")):
            if isinstance(value, str):
                hidden.add(value)

    for flag in flags:
        source = flag.get("source") if isinstance(flag.get("source"), dict) else None
        if _trust_flag_source_denial(caller, flag):
            conceal(flag, source)
            continue
        entry_id = source.get("trust_entry_id") if isinstance(source, dict) else None
        scope = flag.get("scope")
        if scope == "account":
            if current.get(flag.get("trust_account_id")) is None:
                conceal(flag, source)
                continue
        elif scope not in {"matter", "client"}:
            conceal(flag, source)
            continue
        if query.get("flag_scope") not in (None, scope):
            continue
        if query.get("matter_id") not in (None, flag.get("matter_id")):
            continue
        if query.get("trust_account_id") not in (None, flag.get("trust_account_id")):
            continue
        if query.get("status") not in (None, flag.get("status")):
            continue
        projected = {key: value for key, value in flag.items() if key not in {"source", "matter_walled"}}
        projected["trust_entry_id"] = entry_id
        visible.append(projected)
    cursor = query.get("cursor")
    if not isinstance(cursor, str) or cursor in hidden:
        cursor = None
    return {"result": "allowed", "rows": visible, "total": len(visible), "next_cursor": cursor}


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
    if bearer_lacks_scopes(call, rule["scopes"]):
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
    if bearer_lacks_scopes(call, access.get("scopes") or []):
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

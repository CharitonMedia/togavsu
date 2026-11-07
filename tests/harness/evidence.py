"""Decide whether a case has product evidence.

Mocks, skipped tests, oracle passes, scaffold output, and the two structural
stand-ins cannot accept a criterion. An empty evidence store leaves every
criterion pending. app_complete also requires a clean Docker Desktop install.
"""

from __future__ import annotations

ALLOWED_KINDS = frozenset({"command", "http", "sql", "file_hash", "log", "clean_install"})
REFUSED_KINDS = frozenset({
    "mock",
    "skip",
    "scaffold",
    "documentation",
    "todo",
    "oracle",
    "synthetic_fixture",
})
GENERIC_COMMANDS = frozenset({"go test ./..."})
GENERIC_OBSERVATIONS = frozenset({"observed"})
GENERIC_RUNNERS = frozenset({"scaffold", "generic", "filled"})


def _nonempty(value) -> bool:
    return isinstance(value, str) and bool(value.strip())


def _real_exit_code(value):
    """bool is an int subclass, and False == 0. Only a real int counts."""
    if type(value) is int:
        return value
    return None


def _generic(value, banned: frozenset[str]) -> bool:
    if not isinstance(value, str):
        return False
    return value.strip().lower() in {item.lower() for item in banned}


def _binding_error(case: dict, evidence: dict) -> str | None:
    """Case id plus one assertion per expected line. A shared go-test stub does not qualify."""
    if evidence.get("case_id") != case["id"]:
        return "evidence is not bound to this case id"
    if _generic(evidence.get("command"), GENERIC_COMMANDS) or _generic(evidence.get("observation"), GENERIC_OBSERVATIONS):
        return "generic suite output is not case evidence"
    provenance = evidence.get("provenance")
    if not isinstance(provenance, dict):
        return "evidence has no command provenance"
    if provenance.get("command") != evidence.get("command") or not _nonempty(provenance.get("runner")):
        return "command provenance does not match the evidence command"
    if _generic(provenance.get("runner"), GENERIC_RUNNERS):
        return "scaffold provenance is not a domain run"
    assertions = evidence.get("assertions")
    if not isinstance(assertions, list) or not assertions:
        return "evidence has no structured assertions"
    expects = list(case.get("expect") or [])
    seen = []
    for item in assertions:
        if not isinstance(item, dict):
            return "assertion is malformed"
        if type(item.get("ok")) is not bool:
            return "assertion ok must be a boolean"
        if item.get("ok") is not True:
            return "assertion was not observed"
        expect = item.get("expect")
        detail = item.get("detail")
        if not isinstance(expect, str) or expect not in expects:
            return "assertion does not match this case"
        if not _nonempty(detail) or _generic(detail, GENERIC_OBSERVATIONS) or case["id"] not in detail:
            return "assertion detail is not bound to this case"
        seen.append(expect)
    if sorted(seen) != sorted(expects) or len(seen) != len(expects):
        return "assertions do not cover this case"
    return None


def decide(case: dict, evidence: dict | None, standins: frozenset[str], denylist_ok: bool = True) -> dict:
    if not evidence:
        return {"status": "pending", "accepted": False, "reason": "no product evidence"}
    if not denylist_ok:
        return {
            "status": "refused",
            "accepted": False,
            "reason": "structural stand-in denylist is unavailable",
        }
    kind = evidence.get("kind")
    if evidence.get("mocked") is True or kind in REFUSED_KINDS or kind not in ALLOWED_KINDS or _uses_double(evidence):
        return {
            "status": "refused",
            "accepted": False,
            "reason": "mocks, doubles, skips, oracle passes, and scaffold output are not acceptance",
        }
    hashes = []
    if evidence.get("file_hash"):
        hashes.append(evidence["file_hash"])
    hashes.extend(evidence.get("file_hashes") or [])
    if any(item in standins for item in hashes):
        return {
            "status": "refused",
            "accepted": False,
            "reason": "a structural stand-in hash is not product evidence",
        }
    binding = _binding_error(case, evidence)
    if binding:
        return {"status": "refused", "accepted": False, "reason": binding}
    exit_code = _real_exit_code(evidence.get("exit_code"))
    if not _nonempty(evidence.get("command")) or not _nonempty(evidence.get("observation")) or exit_code is None:
        return {
            "status": "refused",
            "accepted": False,
            "reason": "evidence needs a non-empty command, a non-empty observation, and an integer exit code",
        }
    expected_exit = case.get("expected_exit_code", 0)
    if exit_code != expected_exit:
        return {
            "status": "failed",
            "accepted": False,
            "reason": f"exit_code {exit_code} does not match expected {expected_exit}",
        }
    polarity = case["polarity"]
    outcome = evidence.get("outcome")
    if polarity == "positive" and outcome != "passed":
        return {"status": "failed", "accepted": False, "reason": "positive case did not pass"}
    if polarity == "failure" and outcome != "rejected":
        return {"status": "failed", "accepted": False, "reason": "failure case did not observe the rejection"}
    return {"status": "passed", "accepted": True, "reason": "evidence accepted for this case"}


def _uses_double(evidence: dict) -> bool:
    mode = str(evidence.get("backend_mode", "")).lower()
    return evidence.get("doubles") is True or mode in {"double", "mock", "fake", "test_double"}


def docker_desktop_ok(attestation: dict | None) -> bool:
    if not attestation:
        return False
    return (
        attestation.get("kind") == "clean_install"
        and attestation.get("host") == "docker_desktop"
        and attestation.get("outcome") == "passed"
        and attestation.get("mocked") is not True
        and not _uses_double(attestation)
        and _real_exit_code(attestation.get("exit_code")) == 0
        and _nonempty(attestation.get("command"))
        and _nonempty(attestation.get("observation"))
        and not _generic(attestation.get("command"), GENERIC_COMMANDS)
        and not _generic(attestation.get("observation"), GENERIC_OBSERVATIONS)
        and isinstance(attestation.get("provenance"), dict)
        and attestation["provenance"].get("command") == attestation.get("command")
        and _nonempty(attestation["provenance"].get("runner"))
        and not _generic(attestation["provenance"].get("runner"), GENERIC_RUNNERS)
    )


def _load_standins(standins: frozenset[str] | None) -> tuple[frozenset[str], bool]:
    """None loads the corpus denylist. An empty denylist fails closed."""
    if standins is None:
        try:
            from .corpus import standin_hashes
            standins = standin_hashes()
        except Exception:
            return frozenset(), False
    if not standins:
        return frozenset(), False
    return standins, True


def _blocked(matrix: list[dict], errors: list[str]) -> dict:
    pending_ids = [item.get("id") for item in matrix]
    return {
        "task": "qa",
        "criteria": len(matrix),
        "accepted": [],
        "pending": pending_ids,
        "failed": [],
        "accepted_count": 0,
        "pending_count": len(pending_ids),
        "app_complete": False,
        "product_acceptance": "pending",
        "inventory_errors": errors,
        "cases": [],
    }


def summarize(matrix: list[dict], evidence_by_case: dict | None = None, attestation: dict | None = None, standins: frozenset[str] | None = None) -> dict:
    evidence_by_case = evidence_by_case or {}
    try:
        from .catalog import inventory_errors
        errors = inventory_errors(matrix)
    except Exception as exc:
        errors = [f"canonical inventory unavailable: {exc}"]
    if errors:
        return _blocked(matrix, errors)
    standins, denylist_ok = _load_standins(standins)
    accepted = []
    pending = []
    failed = []
    case_results = []
    for criterion in matrix:
        case_decisions = []
        for case in criterion["positive"] + criterion["failure"]:
            decision = decide(case, evidence_by_case.get(case["id"]), standins, denylist_ok=denylist_ok)
            case_decisions.append(decision)
            case_results.append({"id": case["id"], **decision})
        if case_decisions and all(item["accepted"] for item in case_decisions):
            accepted.append(criterion["id"])
        elif any(item["status"] == "failed" for item in case_decisions):
            failed.append(criterion["id"])
            pending.append(criterion["id"])
        else:
            pending.append(criterion["id"])
    app_complete = len(accepted) == len(matrix) == 52 and not failed and docker_desktop_ok(attestation)
    return {
        "task": "qa",
        "criteria": len(matrix),
        "accepted": accepted,
        "pending": pending,
        "failed": failed,
        "accepted_count": len(accepted),
        "pending_count": len(pending),
        "app_complete": app_complete,
        "product_acceptance": "pending" if not app_complete else "claimed",
        "inventory_errors": [],
        "cases": case_results,
    }

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


def decide(case: dict, evidence: dict | None, standins: frozenset[str]) -> dict:
    if not evidence:
        return {"status": "pending", "accepted": False, "reason": "no product evidence"}
    kind = evidence.get("kind")
    if evidence.get("mocked") is True or kind in REFUSED_KINDS or kind not in ALLOWED_KINDS:
        return {
            "status": "refused",
            "accepted": False,
            "reason": "mocks, skips, oracle passes, and scaffold output are not acceptance",
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
    if "command" not in evidence or "observation" not in evidence or not isinstance(evidence.get("exit_code"), int):
        return {
            "status": "refused",
            "accepted": False,
            "reason": "evidence needs a command, an integer exit code, and an observation",
        }
    polarity = case["polarity"]
    outcome = evidence.get("outcome")
    if polarity == "positive" and outcome != "passed":
        return {"status": "failed", "accepted": False, "reason": "positive case did not pass"}
    if polarity == "failure" and outcome != "rejected":
        return {"status": "failed", "accepted": False, "reason": "failure case did not observe the rejection"}
    return {"status": "passed", "accepted": True, "reason": "evidence accepted for this case"}


def docker_desktop_ok(attestation: dict | None) -> bool:
    if not attestation:
        return False
    return (
        attestation.get("kind") == "clean_install"
        and attestation.get("host") == "docker_desktop"
        and attestation.get("outcome") == "passed"
        and attestation.get("mocked") is not True
        and isinstance(attestation.get("exit_code"), int)
        and bool(attestation.get("command"))
        and bool(attestation.get("observation"))
    )


def summarize(matrix: list[dict], evidence_by_case: dict | None = None, attestation: dict | None = None, standins: frozenset[str] | None = None) -> dict:
    evidence_by_case = evidence_by_case or {}
    standins = standins if standins is not None else frozenset()
    accepted = []
    pending = []
    failed = []
    case_results = []
    for criterion in matrix:
        case_decisions = []
        for case in criterion["positive"] + criterion["failure"]:
            decision = decide(case, evidence_by_case.get(case["id"]), standins)
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
        "cases": case_results,
    }

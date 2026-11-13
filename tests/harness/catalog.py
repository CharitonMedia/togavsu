"""Build the acceptance matrix from the PRD index, tasks, and case specs."""

from __future__ import annotations

import json
from pathlib import Path

from .cases import SPECS
from .corpus import LEGACY_IDS

CASE_KINDS = frozenset({"e2e", "integration", "security", "performance"})
NON_IMPLEMENTING = frozenset({"qa", "release", "release-final"})


def repo_root() -> Path:
    here = Path(__file__).resolve()
    for candidate in [here, *here.parents]:
        if (candidate / "togavsu-core-prd.md").is_file() and (candidate / "docs/build/acceptance.json").is_file():
            return candidate
    raise RuntimeError("repo root not found")


def load_acceptance(root: Path | None = None) -> list[dict]:
    root = root or repo_root()
    return json.loads((root / "docs/build/acceptance.json").read_text())


def load_tasks(root: Path | None = None) -> list[dict]:
    root = root or repo_root()
    return json.loads((root / "docs/build/tasks.json").read_text())["tasks"]


def responsible_domains(ac_id: str, tasks: list[dict]) -> list[str]:
    return [task["id"] for task in tasks if ac_id in task["acceptance_ids"]]


def _case(ac_id: str, polarity: str, raw: dict) -> dict:
    case = dict(raw)
    case["id"] = f"{ac_id}-{raw['suffix']}"
    case["criterion"] = ac_id
    case["polarity"] = polarity
    case.pop("suffix")
    if case.get("expected_exit_code") is None:
        case["expected_exit_code"] = 0
    return case


def build_matrix(root: Path | None = None) -> list[dict]:
    root = root or repo_root()
    acceptance = load_acceptance(root)
    tasks = load_tasks(root)
    matrix = []
    for item in acceptance:
        spec = SPECS[item["id"]]
        domains = responsible_domains(item["id"], tasks)
        implementing = [domain for domain in domains if domain not in NON_IMPLEMENTING]
        pending = list(implementing)
        for extra in spec["extra_dependencies"]:
            if extra not in pending:
                pending.append(extra)
        matrix.append({
            "id": item["id"],
            "requirement": item["requirement"],
            "status": "pending",
            "product_acceptance": "pending",
            "in_scope_phases": list(spec["in_scope_phases"]),
            "full_behavior_phase": spec["full_behavior_phase"],
            "recheck_phases": list(spec["recheck_phases"]),
            "responsible_domains": domains,
            "implementing_domains": implementing,
            "pending_dependencies": pending,
            "note": spec["note"],
            "legacy_failures": list(spec["legacy_failures"]),
            "positive": [_case(item["id"], "positive", raw) for raw in spec["positive"]],
            "failure": [_case(item["id"], "failure", raw) for raw in spec["failure"]],
        })
    return matrix


def inventory_errors(matrix: list[dict]) -> list[str]:
    """Compare a matrix to the canonical case inventory. Fail closed on drift."""
    try:
        canonical = build_matrix()
    except Exception as exc:
        return [f"canonical inventory unavailable: {exc}"]
    errors = []
    submitted_ids = [item.get("id") for item in matrix]
    canonical_ids = [item["id"] for item in canonical]
    if len(submitted_ids) != len(set(submitted_ids)):
        errors.append("duplicate criterion ids")
    if submitted_ids != canonical_ids:
        errors.append("criterion ids do not match the canonical inventory")
    canon_by_id = {item["id"]: item for item in canonical}
    seen_cases = []
    for item in matrix:
        expected = canon_by_id.get(item.get("id"))
        if expected is None:
            continue
        if item != expected:
            errors.append(f"{item['id']} criterion definition does not match the canonical inventory")
        if item.get("requirement") != expected["requirement"]:
            errors.append(f"{item['id']} requirement does not match the canonical inventory")
        for polarity in ("positive", "failure"):
            got_cases = item.get(polarity) or []
            want_cases = expected[polarity]
            got = [case.get("id") for case in got_cases]
            want = [case["id"] for case in want_cases]
            seen_cases.extend(got)
            if got != want:
                errors.append(f"{item['id']} {polarity} cases do not match the canonical inventory")
                continue
            for got_case, want_case in zip(got_cases, want_cases):
                if got_case != want_case:
                    errors.append(f"{got_case.get('id')} case definition does not match the canonical inventory")
                if got_case.get("polarity") != want_case.get("polarity"):
                    errors.append(f"{got_case.get('id')} polarity does not match the canonical inventory")
                got_exit = got_case.get("expected_exit_code")
                want_exit = want_case.get("expected_exit_code")
                if type(got_exit) is not int or got_exit != want_exit:
                    errors.append(f"{got_case.get('id')} expected_exit_code does not match the canonical inventory")
    if len(seen_cases) != len(set(seen_cases)):
        errors.append("duplicate case ids")
    return errors


def integrity_errors(matrix: list[dict], acceptance: list[dict], tasks: list[dict]) -> list[str]:
    errors = []
    ids = [item["id"] for item in acceptance]
    if [item["id"] for item in matrix] != ids:
        errors.append("matrix ids do not match acceptance.json order")
    if set(SPECS) != set(ids):
        errors.append("case specs do not match the 52 acceptance ids")
    seen_legacy = []
    seen_case_ids = []
    for criterion, source in zip(matrix, acceptance):
        if criterion["requirement"] != source["requirement"]:
            errors.append(f"{criterion['id']} requirement text does not match acceptance.json")
        if criterion["status"] != "pending" or criterion["product_acceptance"] != "pending":
            errors.append(f"{criterion['id']} is not pending")
        if not criterion["positive"] or not criterion["failure"]:
            errors.append(f"{criterion['id']} needs a positive case and a failure case")
        expected_domains = responsible_domains(criterion["id"], tasks)
        if criterion["responsible_domains"] != expected_domains:
            errors.append(f"{criterion['id']} domains do not match tasks.json")
        phases = criterion["in_scope_phases"]
        if not phases or phases != sorted(set(phases)) or any(phase < 1 or phase > 7 for phase in phases):
            errors.append(f"{criterion['id']} phases are not an ascending subset of 1..7")
        case_phases = []
        for case in criterion["positive"] + criterion["failure"]:
            case_phases.append(case["phase"])
            seen_case_ids.append(case["id"])
            if case["status"] != "pending" or case["mocks_accepted"] is not False:
                errors.append(f"{case['id']} is marked passed or accepts mocks")
            if case["phase"] not in phases:
                errors.append(f"{case['id']} phase is outside the criterion")
            if case["kind"] not in CASE_KINDS:
                errors.append(f"{case['id']} has an unknown kind")
            if type(case.get("expected_exit_code")) is not int:
                errors.append(f"{case['id']} has no expected exit code")
            for field in ("title", "arrange", "action"):
                if not str(case.get(field, "")).strip():
                    errors.append(f"{case['id']} has an empty {field}")
            if not case["expect"] or not case["evidence"]:
                errors.append(f"{case['id']} has no expect or evidence")
        if not case_phases or max(case_phases) != criterion["full_behavior_phase"]:
            errors.append(f"{criterion['id']} full behavior phase is not the latest case phase")
        if criterion["full_behavior_phase"] not in phases:
            errors.append(f"{criterion['id']} full behavior phase is out of scope")
        recheck = criterion["recheck_phases"]
        if recheck != sorted(recheck):
            errors.append(f"{criterion['id']} recheck phases are unsorted")
        for phase in recheck:
            if phase not in phases or phase <= criterion["full_behavior_phase"]:
                errors.append(f"{criterion['id']} recheck phase {phase} is not after full behavior")
        if 7 not in phases:
            errors.append(f"{criterion['id']} is not in scope at phase 7")
        if criterion["full_behavior_phase"] < 7 and 7 not in recheck:
            errors.append(f"{criterion['id']} needs a phase 7 recheck")
        if criterion["full_behavior_phase"] == 7 and 7 in recheck:
            errors.append(f"{criterion['id']} lists phase 7 as both full behavior and a recheck")
        for legacy in criterion["legacy_failures"]:
            if legacy not in LEGACY_IDS:
                errors.append(f"{criterion['id']} names an unknown legacy failure {legacy}")
            seen_legacy.append(legacy)
    if set(seen_legacy) != set(LEGACY_IDS):
        errors.append("legacy Doc Tender failures are not all attached to cases")
    if len(seen_case_ids) != len(set(seen_case_ids)):
        errors.append("case ids are not unique")
    return errors

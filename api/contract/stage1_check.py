"""Deterministic Stage 1b inventory classification.

This module does not decide authorization and it does not execute fixtures.
It only reads source structure so the inventory cannot mark a simulator path
as a contract-only check.
"""

from __future__ import annotations

import ast
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
POLICY_PATH = ROOT / "api" / "contract" / "policy.py"
TEST_PATH = ROOT / "api" / "tests" / "test_contract.py"
BASELINE = "b4350f8af3803b8dc8439b2b2e7a431f198583d9"

DECISION_RESULTS = {
    "allowed",
    "scope_denied",
    "forbidden",
    "not_found",
    "unauthenticated",
    "validation_failed",
    "highlighted",
    "audited",
    "missing_event",
    "missing_wall",
    "field_locked",
    "conflict",
    "quarantine_confirmation_required",
}
ROUTING_RESULTS = {"trust_module", "core", "ambiguous"}

# First matching token in the test name wins. Acceptance ids are the domain's
# existing plan ids, not a stand-in AC-002 label.
OWNER_RULES = (
    ("mcp_dispatch_reuses", "multiple", ["AC-003", "AC-008", "AC-012", "AC-026", "AC-039"]),
    ("mcp_forwards_context", "files", ["AC-012", "AC-039"]),
    ("every_emitted_event", "practice", ["AC-024"]),
    ("mcp_dispatcher_covers", "multiple", ["AC-003", "AC-005", "AC-008", "AC-012", "AC-039"]),
    ("mcp_public_bootstrap", "identity", ["AC-001", "AC-003"]),
    ("dispatcher_parity", "multiple", ["AC-003", "AC-008", "AC-012", "AC-039"]),
    ("cursor_pages", "platform", ["AC-030"]),
    ("ai_call", "modules", ["AC-031"]),
    ("malware", "files", ["AC-039"]),
    ("processor_callback", "billing", ["AC-008", "AC-026"]),
    ("custom_field", "practice", ["AC-023"]),
    ("published_version", "files", ["AC-039"]),
    ("include_view", "modules", ["AC-012"]),
    ("owner_bound", "identity", ["AC-018", "AC-024"]),
    ("bulk_custom", "practice", ["AC-023"]),
    ("history_inherits", "practice", ["AC-024"]),
    ("record_mutation", "practice", ["AC-024"]),
    ("invoicing", "billing", ["AC-006", "AC-008", "AC-009", "AC-026"]),
    ("client_context", "modules", ["AC-012", "AC-003"]),
    ("delegation", "modules", ["AC-012", "AC-014", "AC-031"]),
    ("module", "modules", ["AC-012", "AC-013", "AC-014", "AC-031"]),
    ("marker", "modules", ["AC-012", "AC-031"]),
    ("manifest", "modules", ["AC-012", "AC-013"]),
    ("webhook", "modules", ["AC-031"]),
    ("sidebar", "modules", ["AC-012", "AC-019"]),
    ("widget", "modules", ["AC-011", "AC-012"]),
    ("marketplace", "marketplace", ["AC-015"]),
    ("trust", "trust", ["AC-005", "AC-006", "AC-007"]),
    ("invoice", "billing", ["AC-008", "AC-009", "AC-026"]),
    ("payment", "billing", ["AC-008", "AC-026"]),
    ("time_entry", "billing", ["AC-008"]),
    ("expense", "billing", ["AC-008"]),
    ("billing", "billing", ["AC-004", "AC-008"]),
    ("calendar", "calendar", ["AC-011", "AC-027"]),
    ("deadline", "calendar", ["AC-011", "AC-027"]),
    ("ical", "calendar", ["AC-027"]),
    ("upload", "files", ["AC-039"]),
    ("storage", "files", ["AC-039"]),
    ("quarantine", "files", ["AC-039"]),
    ("file", "files", ["AC-039"]),
    ("editor", "editor", ["AC-040"]),
    ("doc_tender", "doc-tender", ["AC-041", "AC-042"]),
    ("tender", "doc-tender", ["AC-041", "AC-042"]),
    ("totp", "identity", ["AC-001", "AC-024"]),
    ("password", "identity", ["AC-001"]),
    ("login", "identity", ["AC-001", "AC-024"]),
    ("session", "identity", ["AC-024"]),
    ("token", "identity", ["AC-018", "AC-024"]),
    ("user", "identity", ["AC-004", "AC-024"]),
    ("notification", "identity", ["AC-019"]),
    ("nav", "identity", ["AC-019"]),
    ("matter", "practice", ["AC-003", "AC-020", "AC-023"]),
    ("contact", "practice", ["AC-003", "AC-020"]),
    ("conflict", "practice", ["AC-003"]),
    ("search", "practice", ["AC-020"]),
    ("note", "practice", ["AC-023"]),
    ("page_size", "platform", ["AC-030"]),
    ("setting", "platform", ["AC-030"]),
    ("backup", "platform", ["AC-018"]),
    ("mcp", "api", ["AC-002"]),
    ("openapi", "api", ["AC-002"]),
    ("scope", "api", ["AC-002"]),
)


def _policy_surface() -> tuple[set[str], dict[str, str], list[dict]]:
    tree = ast.parse(POLICY_PATH.read_text(encoding="utf-8"))
    functions = set()
    aliases = {}
    classes = []
    for node in tree.body:
        if isinstance(node, ast.FunctionDef):
            functions.add(node.name)
        elif isinstance(node, ast.ClassDef):
            methods = [item.name for item in node.body if isinstance(item, ast.FunctionDef)]
            classes.append({"name": node.name, "line": node.lineno, "methods": methods})
        elif isinstance(node, ast.Assign) and len(node.targets) == 1 and isinstance(node.targets[0], ast.Name):
            if isinstance(node.value, ast.Name) and isinstance(node.value.ctx, ast.Load):
                aliases[node.targets[0].id] = node.value.id
    return functions, aliases, classes


def _call_names(node: ast.AST) -> set[str]:
    found = set()
    for child in ast.walk(node):
        if isinstance(child, ast.Call):
            func = child.func
            if isinstance(func, ast.Name):
                found.add(func.id)
            elif isinstance(func, ast.Attribute):
                found.add(func.attr)
    return found


def _returned_constants(node: ast.AST) -> set[str]:
    found = set()
    for child in ast.walk(node):
        if not isinstance(child, ast.Return):
            continue
        for item in ast.walk(child):
            if isinstance(item, ast.Constant) and isinstance(item.value, str):
                found.add(item.value)
    return found


def _owner(name: str) -> tuple[str, list[str]]:
    lowered = name.lower()
    for token, owner, acceptance in OWNER_RULES:
        if token in lowered:
            return owner, list(acceptance)
    return "unmapped", []


def _case_tables(node: ast.AST, lines: list[str]) -> list[dict]:
    """Lists and loops whose elements are tuples. This is the explicit case count, not a method count."""
    tables = []
    for child in ast.walk(node):
        target = None
        if isinstance(child, ast.Assign) and isinstance(child.value, (ast.List, ast.Tuple)):
            target = child.value
            line = child.lineno
        elif isinstance(child, ast.For) and isinstance(child.iter, (ast.List, ast.Tuple)):
            target = child.iter
            line = child.lineno
        else:
            continue
        if not target.elts or not all(isinstance(elt, ast.Tuple) for elt in target.elts):
            continue
        rows = []
        for elt in target.elts:
            if not isinstance(elt, ast.Tuple):
                continue
            values = [item.value for item in elt.elts if isinstance(item, ast.Constant)]
            rows.append([value for value in values if isinstance(value, (str, int, bool))])
        tables.append({"line": line, "cases": len(target.elts), "constants": rows, "text": lines[line - 1].strip()})
    return tables


def classify_contract_tests() -> dict:
    """Return the classification the inventory must match."""
    functions, aliases, classes = _policy_surface()
    simulator = set(functions) | set(aliases)
    tree = ast.parse(TEST_PATH.read_text(encoding="utf-8"))
    lines = TEST_PATH.read_text(encoding="utf-8").splitlines()
    helpers = [node for node in tree.body if isinstance(node, ast.FunctionDef)]
    helper_calls = {node.name: _call_names(node) for node in helpers}
    behavioral_helpers = set()

    def reaches(name: str, seen: set[str] | None = None) -> bool:
        if name in simulator or name in behavioral_helpers:
            return True
        if name not in helper_calls:
            return False
        seen = set() if seen is None else seen
        if name in seen:
            return False
        seen.add(name)
        return any(reaches(item, seen) for item in helper_calls[name])

    for node in helpers:
        returns = _returned_constants(node)
        if returns & DECISION_RESULTS or returns & ROUTING_RESULTS or reaches(node.name):
            behavioral_helpers.add(node.name)
    # A second pass picks up helpers that only call helpers marked above.
    changed = True
    while changed:
        changed = False
        for node in helpers:
            if node.name in behavioral_helpers:
                continue
            if any(item in behavioral_helpers for item in helper_calls[node.name]):
                behavioral_helpers.add(node.name)
                changed = True

    methods = []
    nested_in_tests = []
    for node in tree.body:
        if not isinstance(node, ast.ClassDef) or node.name != "ContractTest":
            continue
        for item in node.body:
            if not isinstance(item, ast.FunctionDef):
                continue
            calls = _call_names(item)
            for sub in ast.walk(item):
                if isinstance(sub, ast.FunctionDef) and sub is not item:
                    nested_in_tests.append({"test": f"ContractTest.{item.name}", "name": sub.name, "line": sub.lineno})
                    calls |= _call_names(sub)
            behavioral = any(name in simulator or name in behavioral_helpers for name in calls)
            uses_spec = "validate" in calls or "Draft202012Validator" in calls or any(
                isinstance(child, ast.Attribute) and child.attr == "spec" for child in ast.walk(item)
            )
            if behavioral and uses_spec:
                kind = "both"
            elif behavioral:
                kind = "simulator_behavior"
            else:
                kind = "contract_validation"
            owner, acceptance = _owner(item.name)
            assertion_calls = [
                child
                for child in ast.walk(item)
                if isinstance(child, ast.Call)
                and isinstance(child.func, ast.Attribute)
                and child.func.attr.startswith("assert")
            ]
            loops = [child for child in ast.walk(item) if isinstance(child, ast.For)]
            case_tables = _case_tables(item, lines)
            methods.append(
                {
                    "id": f"ContractTest.{item.name}",
                    "name": item.name,
                    "line": item.lineno,
                    "end_line": item.end_lineno,
                    "kind": kind,
                    "go_owner": owner,
                    "acceptance": acceptance,
                    "behavioral": behavioral,
                    "assertion_count": len(assertion_calls),
                    "loop_count": len(loops),
                    "explicit_case_count": sum(table["cases"] for table in case_tables),
                    "case_tables": case_tables,
                    "assertion_lines": [lines[child.lineno - 1].strip() for child in assertion_calls],
                    "source": "api/tests/test_contract.py",
                }
            )
    nested_in_helpers = []
    for node in helpers:
        for sub in ast.walk(node):
            if isinstance(sub, ast.FunctionDef) and sub is not node:
                nested_in_helpers.append({"helper": node.name, "name": sub.name, "line": sub.lineno, "structural": True})
    return {
        "baseline_sha": BASELINE,
        "methods": methods,
        "behavioral_helpers": sorted(behavioral_helpers),
        "helpers": [{"name": node.name, "line": node.lineno, "kind": "simulator_helper" if node.name in behavioral_helpers else "contract_helper"} for node in helpers],
        "nested_in_tests": nested_in_tests,
        "nested_structural_helpers": nested_in_helpers,
        "classes": classes,
        "aliases": aliases,
        "unittest_tests": sum(1 for row in methods if row["name"].startswith("test_")),
        "setup_methods": sum(1 for row in methods if not row["name"].startswith("test_")),
        "lines": lines,
    }


def write_stage1_documents() -> None:
    import json

    data = classify_contract_tests()
    inventory_path = ROOT / "docs" / "api" / "stage1-inventory.json"
    fixture_path = ROOT / "docs" / "api" / "runtime-fixtures.json"
    manifest_path = ROOT / "docs" / "api" / "scenario-manifest.json"
    previous = json.loads(inventory_path.read_text(encoding="utf-8"))
    lines = data["lines"]
    methods = []
    for row in data["methods"]:
        blocking = row["behavioral"]
        methods.append(
            {
                "id": row["id"],
                "source": row["source"],
                "line": row["line"],
                "end_line": row["end_line"],
                "kind": row["kind"],
                "baseline_sha": BASELINE,
                "go_owner": row["go_owner"],
                "acceptance": row["acceptance"] if row["behavioral"] else (row["acceptance"] or ["AC-002"]),
                "assertion_count": row["assertion_count"],
                "loop_count": row["loop_count"],
                "explicit_case_count": row["explicit_case_count"],
                "runtime_status": "runtime_pending" if blocking else "contract_check",
                "blocking_removal": blocking,
                "gap": (
                    "Behavioral method. Identity and modules cases are expanded in scenario-manifest.json. "
                    "Other domains remain a named batch and are not claimed complete."
                    if blocking
                    else ""
                ),
            }
        )
    behavioral = [row for row in data["methods"] if row["behavioral"]]
    by_domain: dict[str, list] = {}
    for row in behavioral:
        by_domain.setdefault(row["go_owner"], []).append(row)
    inventory = {
        "baseline_sha": BASELINE,
        "stage": "1b",
        "purpose": "Correct inventory classification and keep the simulator. Deletion stays blocked.",
        "deletion_blocked": True,
        "deletion_block_reason": "Simulator tests remain. Unexpanded domain batches are not complete. Runtime fixtures are not executed.",
        "counting": {
            "contract_test_methods_including_setup": len(methods),
            "unittest_tests": data["unittest_tests"],
            "setup_methods": data["setup_methods"],
            "meaning": "102 ContractTest methods are 101 unittest tests plus setUpClass.",
            "top_level_helpers": len(data["helpers"]),
            "nested_functions_inside_test_methods": len(data["nested_in_tests"]),
            "nested_structural_helpers_inside_top_level_helpers": data["nested_structural_helpers"],
            "nested_structural_note": "rewrite/rewrite/walk sit inside schema helpers. They are not top-level helpers and are not behavioral scenarios.",
            "markdown_html": data["classes"],
            "markdown_html_note": "Eight methods on _MarkdownHTML are class methods. They are outside the 176 top-level function count.",
            "alias": data["aliases"],
            "alias_note": "module_ui_targets is sidebar_entries. Counting it again would double one function.",
            "behavioral_methods": len(behavioral),
            "behavioral_methods_are_not_scenarios": True,
        },
        "counts": {
            "contract_tests": sum(1 for row in methods if row["kind"] == "contract_validation"),
            "simulator_tests": sum(1 for row in methods if row["kind"] == "simulator_behavior"),
            "mixed_tests": sum(1 for row in methods if row["kind"] == "both"),
            "test_methods": len(methods),
            "behavioral_methods": len(behavioral),
            "local_helpers": len(data["helpers"]),
            "simulator_helpers": len(data["behavioral_helpers"]),
            "nested_helpers": len(data["nested_in_tests"]),
            "blocking_gaps": len(behavioral),
            "explicit_cases_in_behavioral_methods": sum(row["explicit_case_count"] for row in behavioral),
            "assertion_calls_in_behavioral_methods": sum(row["assertion_count"] for row in behavioral),
        },
        "artifacts": previous["artifacts"],
        "intentional_corrections": [
            {
                "id": "grant-workflow-effective-scopes",
                "prd": "9.3",
                "source": "api/tests/test_contract.py",
                "was": "A background getFileUpload resume with only module_scopes=['files:write'] was allowed.",
                "now": "scope_denied. The bearer token and current approvals must hold files:write. Stale module_scopes do not replace an omitted, empty, or revoked scope.",
                "workflows": ["malware_scan", "payment_processor", "background_file_publication"],
            }
        ],
        "tests": methods,
        "helpers": data["helpers"],
        "nested_helpers": data["nested_in_tests"],
        "behavioral_helpers": data["behavioral_helpers"],
        "blocking_gaps": [row["id"] for row in methods if row["blocking_removal"]],
        "hidden_runtime_consumers": previous.get("hidden_runtime_consumers", []),
        "non_python_consumers": previous.get("non_python_consumers", []),
        "importers": previous.get("importers", {}),
    }
    domains = {}
    expanded = []
    remaining = {}
    for owner, rows in sorted(by_domain.items()):
        entry = {
            "go_owner": owner,
            "methods": len(rows),
            "assertion_calls": sum(row["assertion_count"] for row in rows),
            "explicit_case_rows": sum(row["explicit_case_count"] for row in rows),
            "method_names": [row["name"] for row in rows],
        }
        if owner in {"identity", "modules"}:
            entry["status"] = "expanded_in_this_batch"
            for row in rows:
                expanded.append(
                    {
                        "id": row["id"],
                        "go_owner": owner,
                        "acceptance": row["acceptance"],
                        "source": f"api/tests/test_contract.py:{row['line']}-{row['end_line']}",
                        "kind": row["kind"],
                        "assertion_count": row["assertion_count"],
                        "loop_count": row["loop_count"],
                        "explicit_cases": row["case_tables"],
                        "assertions": row["assertion_lines"],
                        "runtime_status": "runtime_pending",
                        "complete": True,
                    }
                )
        else:
            entry["status"] = "remaining_batch"
            entry["complete"] = False
        domains[owner] = entry
        if entry["status"] == "remaining_batch":
            remaining[owner] = entry
    from contract.build import build_spec

    operation_ids = sorted(
        operation["operationId"]
        for item in build_spec()["paths"].values()
        for operation in item.values()
        if isinstance(operation, dict) and operation.get("operationId")
    )
    manifest = {
        "baseline_sha": BASELINE,
        "stage": "1b",
        "note": "A method is not a scenario. explicit_cases are tuple rows copied from the test source. Remaining domains are not expanded and are not complete.",
        "spec_operation_ids": operation_ids,
        "spec_operation_guard": "A new operation id must be added here before the stage check passes. This records the mapped operation set. It does not execute those operations.",
        "identity_and_modules": expanded,
        "domains": domains,
        "remaining_batches": remaining,
        "runtime_status": "runtime_pending",
    }
    fixtures = json.loads(fixture_path.read_text(encoding="utf-8"))
    bodies = {
        "createModuleMarker": {"label": "After hours", "color": "#336699"},
        "updateModuleMarker": {"color": "#336699"},
        "createModuleHelpPage": {"id": "start", "title": "Start", "markdown": "Hello"},
        "replaceModuleHelpPages": {"pages": [{"id": "start", "title": "Start", "markdown": "Hello"}]},
    }
    probe_ops = set(bodies)
    rewritten = []
    existing_ids = {row["id"] for row in fixtures["fixtures"]}
    for fixture in fixtures["fixtures"]:
        if fixture["id"].count("-endpoint") > 1:
            continue
        fixture["expected"]["public"] = False
        fixture["runtime_status"] = "runtime_pending"
        operation = fixture["operation"]
        if fixture["id"].endswith("-endpoint"):
            fixture["fixture_kind"] = "endpoint"
            rewritten.append(fixture)
            continue
        if operation in probe_ops and ("empty-token" in fixture["id"] or "empty-current" in fixture["id"]):
            fixture["fixture_kind"] = "policy_probe"
            fixture["label"] = "Lower-level policy probe. The request omits the published body and is not a complete endpoint call."
            rewritten.append(fixture)
            endpoint_id = fixture["id"] + "-endpoint"
            if endpoint_id not in existing_ids:
                probe = dict(fixture)
                probe["id"] = endpoint_id
                probe["fixture_kind"] = "endpoint"
                probe["label"] = "Schema-valid endpoint request. The expected scope_denied is the same denial the policy probe already asserts."
                arguments = dict(probe["request"].get("arguments") or {})
                arguments["body"] = bodies[operation]
                probe["request"] = {"tool": operation, "arguments": arguments}
                rewritten.append(probe)
        elif fixture["id"] == "reg-bulkTimeEntry-item-flag-ignored":
            fixture["fixture_kind"] = "policy_probe"
            fixture["label"] = "Malformed TimeEntryWrite. matter_walled is not a published field. The trusted wall still denies the item."
            rewritten.append(fixture)
        else:
            fixture.setdefault("fixture_kind", "endpoint")
            rewritten.append(fixture)
    public_line = next(index for index, line in enumerate(lines, start=1) if 'assertFalse(decision["public"]' in line)
    present = {row["id"] for row in rewritten}
    rewritten.extend(row for row in _grant_fixtures(lines) if row["id"] not in present)
    for fixture in rewritten:
        fixture["source_line"] = _source_line(lines, fixture)
        fixture["line"] = fixture["source_line"]
        fixture["source_range"] = [fixture["source_line"], fixture["source_line"]]
        fixture["public_assertion_line"] = public_line
        fixture["expected"]["public"] = False
    unique = []
    seen_ids = set()
    for fixture in rewritten:
        if fixture["id"] in seen_ids:
            continue
        seen_ids.add(fixture["id"])
        unique.append(fixture)
    fixtures["fixtures"] = unique
    fixtures["stage"] = "1b"
    fixtures["runtime_status"] = "runtime_pending"
    fixtures["note"] = "Structure and schema shape only. runtime_pending means these fixtures were not executed and are not acceptance."
    inventory["counts"]["runtime_fixtures"] = len(unique)
    inventory_path.write_text(json.dumps(inventory, indent=2) + "\n", encoding="utf-8")
    fixture_path.write_text(json.dumps(fixtures, indent=2) + "\n", encoding="utf-8")
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    print("fixtures", len(rewritten), "behavioral", len(behavioral) if False else inventory["counts"]["behavioral_methods"])


def _grant_fixtures(lines: list[str]) -> list[dict]:
    """Policy probes copied from the grant-workflow assertions. Not executed."""
    del lines
    rows = []

    def add(fixture_id, operation, owner, acceptance, result, effects, anchor, trusted, inventory):
        rows.append(
            {
                "id": fixture_id,
                "inventory_id": inventory,
                "source": "api/tests/test_contract.py",
                "line": 0,
                "baseline_sha": BASELINE,
                "operation": operation,
                "requirement": {
                    "prd": ["9.3"],
                    "acceptance": acceptance,
                    "review_finding": "A grant workflow still checks the effective bearer scope. Stale module_scopes do not allow an empty, omitted, or revoked scope.",
                },
                "go_owner": owner,
                "request": {"tool": operation, "arguments": {"path": {}}},
                "trusted_context": {"authorization": "Bearer secret", "credential_valid": True, "viewer_present": False, **trusted},
                "expected": {"result": result, "effects": effects, "public": False},
                "runtime_status": "runtime_pending",
                "fixture_kind": "policy_probe",
                "label": "Grant-workflow policy probe. The expected result is the assertion already in the contract test.",
                "source_anchor": anchor,
            }
        )

    scan = "ContractTest.test_mcp_dispatch_reuses_the_operation_policy"
    add("reg-downloadFileVersion-minimal-scopes", "downloadFileVersion", "files", ["AC-039"], "allowed", True, "allowed_scan = scan()", {"module_id": "malware", "token_scopes": ["files:read"], "approved_scopes": ["files:read"], "module_scopes": ["files:read"]}, scan)
    add("reg-downloadFileVersion-empty-token", "downloadFileVersion", "files", ["AC-039"], "scope_denied", False, 'scan(token_scopes=[], approved_scopes=["files:read"])', {"module_id": "malware", "token_scopes": [], "approved_scopes": ["files:read"], "module_scopes": ["files:read"]}, scan)
    add("reg-downloadFileVersion-omitted-scopes", "downloadFileVersion", "files", ["AC-039"], "scope_denied", False, "scan(token_scopes=None, approved_scopes=None)", {"module_id": "malware", "module_scopes": ["files:read"]}, scan)
    add("reg-downloadFileVersion-empty-current", "downloadFileVersion", "files", ["AC-039"], "scope_denied", False, "scan(current_approved_scopes=[])", {"module_id": "malware", "token_scopes": ["files:read"], "approved_scopes": ["files:read"], "current_approved_scopes": [], "module_scopes": ["files:read"]}, scan)
    add("reg-downloadFileVersion-revoked-current", "downloadFileVersion", "files", ["AC-039"], "scope_denied", False, 'scan(current_approved_scopes=["files:write"])', {"module_id": "malware", "token_scopes": ["files:read"], "approved_scopes": ["files:read"], "current_approved_scopes": ["files:write"], "module_scopes": ["files:read"]}, scan)
    pay = scan
    add("reg-createPayment-minimal-scopes", "createPayment", "billing", ["AC-008", "AC-026"], "allowed", True, "operating_callback = callback(operating_grant)", {"module_id": "lawpay", "token_scopes": ["payments:write"], "approved_scopes": ["payments:write"], "module_scopes": ["payments:write"]}, pay)
    add("reg-createPayment-empty-token", "createPayment", "billing", ["AC-008", "AC-026"], "scope_denied", False, 'callback(operating_grant, token_scopes=[], approved_scopes=["payments:write"])', {"module_id": "lawpay", "token_scopes": [], "approved_scopes": ["payments:write"], "module_scopes": ["payments:write"]}, pay)
    add("reg-createPayment-omitted-scopes", "createPayment", "billing", ["AC-008", "AC-026"], "scope_denied", False, "callback(operating_grant, token_scopes=None, approved_scopes=None)", {"module_id": "lawpay", "module_scopes": ["payments:write"]}, pay)
    add("reg-createPayment-empty-current", "createPayment", "billing", ["AC-008", "AC-026"], "scope_denied", False, "callback(operating_grant, current_approved_scopes=[])", {"module_id": "lawpay", "token_scopes": ["payments:write"], "approved_scopes": ["payments:write"], "current_approved_scopes": [], "module_scopes": ["payments:write"]}, pay)
    add("reg-createPayment-revoked-current", "createPayment", "billing", ["AC-008", "AC-026"], "scope_denied", False, 'callback(operating_grant, current_approved_scopes=["trust:write"])', {"module_id": "lawpay", "token_scopes": ["payments:write"], "approved_scopes": ["payments:write"], "current_approved_scopes": ["trust:write"], "module_scopes": ["payments:write"]}, pay)
    matrix = "ContractTest.test_dispatcher_parity_matrix"
    add("reg-getFileUpload-omitted-scopes", "getFileUpload", "files", ["AC-039"], "scope_denied", False, "module resume omitted scopes", {"module_id": "email", "module_scopes": ["files:write"]}, matrix)
    add("reg-getFileUpload-minimal-scopes", "getFileUpload", "files", ["AC-039"], "allowed", True, "module resume minimal scopes", {"module_id": "email", "token_scopes": ["files:write"], "approved_scopes": ["files:write"], "module_scopes": ["files:write"]}, matrix)
    add("reg-getFileUpload-empty-token", "getFileUpload", "files", ["AC-039"], "scope_denied", False, "module resume empty token", {"module_id": "email", "token_scopes": [], "approved_scopes": ["files:write"], "module_scopes": ["files:write"]}, matrix)
    add("reg-getFileUpload-empty-current", "getFileUpload", "files", ["AC-039"], "scope_denied", False, "module resume empty current approval", {"module_id": "email", "token_scopes": ["files:write"], "approved_scopes": ["files:write"], "current_approved_scopes": [], "module_scopes": ["files:write"]}, matrix)
    return rows


def _source_line(lines: list[str], fixture: dict) -> int:
    anchor = fixture.get("source_anchor")
    if anchor:
        for index, line in enumerate(lines, start=1):
            if anchor in line:
                return index
    needles = {
        "reg-createModuleHelpPage-current-match": "help bearer matches current approval",
        "reg-listCalendarEvent-routed": "calendar list routes",
        "reg-getCalendarEvent-routed": "calendar get routes",
        "reg-listCalendarEvent-core-history": "calendar history stays on core",
        "reg-listCalendarEvent-unavailable": "unavailable calendar read",
        "reg-listCalendarEvent-core": "core calendar list",
        "reg-createCalendarEvent-routed": "calendar write routes",
        "reg-createCalendarEvent-history-does-not-keep-write": "history flag does not keep a write",
        "reg-bulkTimeEntry-unwalled": "unwalled time batch",
        "reg-bulkTimeEntry-call-flag": "call wall flag for one matter",
        "reg-bulkTimeEntry-mixed-off-wall": "mixed unwalled and off-wall",
        "reg-bulkTimeEntry-mixed-on-wall": "mixed unwalled and on-wall",
        "reg-bulkTimeEntry-item-flag-ignored": "item flag does not clear a wall",
        "reg-issueFilePublicationGrant-effective": "publication grant effective scopes",
        "reg-issueFilePublicationGrant-empty-approval": "empty approval ignores stale module scopes",
        "reg-issueFilePublicationGrant-empty-token": "empty token ignores stale module scopes",
        "reg-issueFilePublicationGrant-empty-current": "empty current approval ignores stale module scopes",
        "module resume omitted scopes": "module resume omitted scopes",
        "module resume minimal scopes": "module resume minimal scopes",
        "module resume empty token": "module resume empty token",
        "module resume empty current approval": "module resume empty current approval",
    }
    fixture_id = fixture["id"].removesuffix("-endpoint")
    if fixture_id.endswith("-empty-token") and fixture["operation"] in {
        "createModuleHelpPage",
        "replaceModuleHelpPages",
        "deleteModuleHelpPage",
        "createModuleMarker",
        "updateModuleMarker",
        "revokeModuleMarker",
    }:
        for index, line in enumerate(lines, start=1):
            if 'operation_id + " empty token"' in line:
                return index
    if fixture_id.endswith("-empty-current") and "Publication" not in fixture["operation"]:
        for index, line in enumerate(lines, start=1):
            if 'operation_id + " empty current approval"' in line:
                return index
    needle = needles.get(fixture_id) or needles.get(fixture["id"])
    if needle:
        for index, line in enumerate(lines, start=1):
            if needle in line:
                return index
    if fixture["operation"] in {
        "createModuleHelpPage",
        "replaceModuleHelpPages",
        "deleteModuleHelpPage",
        "createModuleMarker",
        "updateModuleMarker",
        "revokeModuleMarker",
    }:
        for index, line in enumerate(lines, start=1):
            if "principal_ids = []" in line:
                return index
    return fixture.get("line", 0)


if __name__ == "__main__":
    write_stage1_documents()

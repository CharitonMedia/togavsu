"""Deterministic Stage 1b inventory classification.

This module does not decide authorization and it does not execute fixtures.
It only reads source structure so the inventory cannot mark a simulator path
as a contract-only check.
"""

from __future__ import annotations

import ast
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
POLICY_PATH = ROOT / "api" / "contract" / "policy.py"
TEST_PATH = ROOT / "api" / "tests" / "test_contract.py"
BASELINE = "b4350f8af3803b8dc8439b2b2e7a431f198583d9"
def _file_hashes() -> dict[str, str]:
    import hashlib

    return {rel: hashlib.sha256((ROOT / rel).read_bytes()).hexdigest() for rel in BASELINE_ARTIFACTS}


BASELINE_ARTIFACTS = {
    "api/openapi/v1/openapi.json": "e2aad30720be7394306142b786649c0414d620fc595c175d63a924989e3d8195",
    "api/openapi/marketplace/openapi.json": "d5a6b6855048a88944827473bf431ec22c5ff98275d446a6af715f72e581eefb",
    "api/openapi/v1/mcp-tools.json": "4581fcdcb373bf6d4e17dbd1676a618089604c77836d4840b8d85f5b85363fc6",
    "docs/api/coverage.md": "a7a9f8a630722b302c13338135e0e36ffab008b1acfced995de4ac3aaad94bfa",
}

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

MAP_PATH = ROOT / "docs" / "api" / "method-acceptance.json"
ACCEPTANCE_PATH = ROOT / "docs" / "build" / "acceptance.json"
TASKS_PATH = ROOT / "docs" / "build" / "tasks.json"


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


def reviewed_method_map() -> dict:
    """Explicit method map. Acceptance ids must exist and belong to the named task."""
    mapping = json.loads(MAP_PATH.read_text(encoding="utf-8"))
    known = {row["id"] for row in json.loads(ACCEPTANCE_PATH.read_text(encoding="utf-8"))}
    tasks = {
        row["id"]: set(row.get("acceptance_ids") or [])
        for row in json.loads(TASKS_PATH.read_text(encoding="utf-8"))["tasks"]
    }
    for name, row in mapping.items():
        owners = list(row.get("tasks") or [])
        if row.get("go_owner") != "multiple" and row.get("go_owner") not in owners:
            owners.append(row["go_owner"])
        for acceptance_id in row.get("acceptance") or []:
            if acceptance_id not in known:
                raise ValueError(f"{name} cites unknown {acceptance_id}")
            if not any(acceptance_id in tasks.get(owner, ()) for owner in owners):
                raise ValueError(f"{name} cites {acceptance_id}, which is not on {owners}")
    return mapping


def _owner(name: str) -> tuple[str, list[str], list[str]]:
    if name == "setUpClass":
        return "unmapped", [], []
    row = reviewed_method_map().get(name)
    if row is None:
        raise ValueError(f"{name} has no reviewed method mapping")
    return row["go_owner"], list(row.get("acceptance") or []), list(row.get("prd") or [])


def _literal_part(node: ast.AST) -> dict:
    if isinstance(node, ast.Constant):
        value = node.value
        if isinstance(value, (str, int, float, bool)) or value is None:
            return {"kind": "constant", "value": value}
        return {"kind": "constant", "unrepresented": True}
    if isinstance(node, ast.Dict):
        keys = []
        for key in node.keys:
            keys.append(key.value if isinstance(key, ast.Constant) else None)
        return {"kind": "dict", "keys": keys, "values_omitted": True}
    if isinstance(node, ast.Name):
        return {"kind": "name", "id": node.id}
    if isinstance(node, ast.Call):
        func = node.func
        label = func.id if isinstance(func, ast.Name) else getattr(func, "attr", "?")
        return {"kind": "call", "name": label}
    return {"kind": type(node).__name__}


def _statement_text(lines: list[str], node: ast.AST) -> str:
    return " ".join(line.strip() for line in lines[node.lineno - 1 : node.end_lineno])


def _case_tables(node: ast.AST, lines: list[str]) -> list[dict]:
    """Literal tuple rows. Dicts and calls stay visible and are not scenarios."""
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
        rows = [[_literal_part(item) for item in elt.elts] for elt in target.elts if isinstance(elt, ast.Tuple)]
        tables.append(
            {
                "line": line,
                "literal_rows": len(target.elts),
                "rows": rows,
                "text": _statement_text(lines, child),
                "complete": False,
                "is_scenario": False,
            }
        )
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
            owner, acceptance, prd = _owner(item.name)
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
                    "prd": prd,
                    "behavioral": behavioral,
                    "assertion_count": len(assertion_calls),
                    "loop_count": len(loops),
                    "explicit_case_count": sum(table["literal_rows"] for table in case_tables),
                    "case_tables": case_tables,
                    "assertion_lines": [_statement_text(lines, child) for child in assertion_calls],
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
                "acceptance": row["acceptance"],
                "prd": row["prd"],
                "source_line_basis": "current_worktree",
                "source_line_note": "line and end_line are the current file. They are not positions in baseline b4350f8.",
                "assertion_count": row["assertion_count"],
                "loop_count": row["loop_count"],
                "explicit_case_count": row["explicit_case_count"],
                "runtime_status": "runtime_pending" if blocking else "contract_check",
                "blocking_removal": blocking,
                "gap": (
                    "Pending. This method is not converted. The source index does not make it complete."
                    if blocking
                    else ""
                ),
                "complete": False,
                "retirement_status": "pending" if blocking else "not_behavioral",
            }
        )
    behavioral = [row for row in data["methods"] if row["behavioral"]]
    by_domain: dict[str, list] = {}
    for row in behavioral:
        by_domain.setdefault(row["go_owner"], []).append(row)
    inventory = {
        "baseline_sha": BASELINE,
        "stage": "1b",
        "purpose": "Source index and contract checks. The simulator and its tests are retained. Generated documents are no longer byte-identical to the baseline after the named corrections.",
        "baseline_artifacts": BASELINE_ARTIFACTS,
        "deletion_blocked": True,
        "deletion_block_reason": "Simulator tests remain. Unexpanded domain batches are not complete. Runtime fixtures are not executed.",
        "counting": {
            "contract_test_methods_including_setup": len(methods),
            "unittest_tests": data["unittest_tests"],
            "setup_methods": data["setup_methods"],
            "meaning": f"{len(methods)} ContractTest methods are {data['unittest_tests']} unittest tests plus {data['setup_methods']} setUpClass.",
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
            "converted_for_retirement": 0,
            "retirement_note": "All behavioral methods remain pending. A fixture or a source index entry does not convert one.",
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
        "artifacts": _file_hashes(),
        "intentional_generated_deltas": [
            {
                "id": "merge-matter-both-walls",
                "files": ["api/openapi/v1/openapi.json"],
                "change": "mergeMatter's description now says both matters must be visible. The MCP catalog and coverage do not inline that description.",
            },
            {
                "id": "matter-patch-cannot-reopen",
                "files": ["api/openapi/v1/openapi.json"],
                "change": "MatterWritePatch status is prospect, open, or on_hold, and updateMatter's description keeps close and reopen on their own methods.",
            },
            {
                "id": "stopped-timer-response",
                "files": ["api/openapi/v1/openapi.json"],
                "change": "stopTimer's TimerStopResult now references StoppedTimer, whose status is stopped. The MCP tool still names TimerStopResult. Active Timer values stay running or paused.",
            },
            {
                "id": "merge-wall-boolean",
                "files": ["api/openapi/v1/openapi.json"],
                "change": "mergeMatter's description says each wall value must be a resolved boolean. Null is not unwalled.",
            },
            {
                "id": "file-reparent-both-matters",
                "files": ["api/openapi/v1/openapi.json"],
                "change": "updateFileMetadata's description requires access to the file's current matter and the destination. The stored matter id is not replaced by the request.",
            },
            {
                "id": "factor-reauthentication",
                "files": ["api/openapi/v1/openapi.json", "api/openapi/v1/mcp-tools.json"],
                "change": "resetTotp and regenerateRecoveryCodes require FactorReauthentication. The MCP catalog records that JSON body. A boolean is not proof.",
            },
            {
                "id": "opaque-task-credential",
                "files": ["api/openapi/v1/openapi.json", "api/openapi/v1/mcp-tools.json"],
                "change": "The caller presents task_authorization as an opaque string. The issued TaskAuthorization object stays on the webhook the core sends. A caller claim object is not a grant.",
            },
            {
                "id": "mcp-cookie-csrf",
                "files": ["api/openapi/v1/openapi.json", "api/openapi/v1/mcp-tools.json"],
                "change": "POST /mcp publishes X-CSRF-Token, and the MCP catalog names that header. Cookie tools/call writes require it. Bearer calls and cookie reads do not.",
            },
            {
                "id": "csrf-header-is-transport",
                "files": ["api/openapi/v1/openapi.json"],
                "change": "POST /mcp and JsonRpcRequest say X-CSRF-Token is the HTTP header on the transport, not a tools/call argument.",
            },
        ],
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
    index_entries = []
    for owner, rows in sorted(by_domain.items()):
        domains[owner] = {
            "go_owner": owner,
            "methods": len(rows),
            "assertion_call_sites": sum(row["assertion_count"] for row in rows),
            "literal_rows": sum(row["explicit_case_count"] for row in rows),
            "method_names": [row["name"] for row in rows],
            "status": "not_converted",
            "complete": False,
        }
        for row in rows:
            index_entries.append(
                {
                    "id": row["id"],
                    "go_owner": owner,
                    "acceptance": row["acceptance"],
                    "prd": row["prd"],
                    "source": f"api/tests/test_contract.py:{row['line']}-{row['end_line']}",
                    "source_line_basis": "current_worktree",
                    "kind": row["kind"],
                    "assertion_call_sites": row["assertion_count"],
                    "loop_count": row["loop_count"],
                    "literal_rows": row["case_tables"],
                    "assertion_statements": row["assertion_lines"],
                    "runtime_status": "runtime_pending",
                    "complete": False,
                    "is_scenario": False,
                    "executed": False,
                    "retirement_status": "pending",
                }
            )
    from contract.build import build_spec

    operation_ids = sorted(
        operation["operationId"]
        for item in build_spec()["paths"].values()
        for operation in item.values()
        if isinstance(operation, dict) and operation.get("operationId")
    )
    manifest = {
        "baseline_sha": BASELINE,
        "document": "source_index",
        "stage": "1b",
        "complete": False,
        "converted_for_retirement": 0,
        "note": "Source index only. methods, assertion_call_sites, and literal_rows are not scenarios and were not executed. Every behavioral method stays pending for retirement.",
        "spec_operation_ids": operation_ids,
        "spec_operation_guard": "A new operation id must be added here before the stage check passes. This records the mapped operation set. It does not execute those operations.",
        "entries": index_entries,
        "domains": domains,
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
            valid_id = "reg-bulkTimeEntry-trusted-wall"
            if valid_id not in existing_ids:
                valid = json.loads(json.dumps(fixture))
                valid["id"] = valid_id
                valid["fixture_kind"] = "endpoint"
                valid["label"] = "Schema-valid TimeEntryWrite. The same trusted wall denies the item. The item body does not carry matter_walled."
                valid["request"]["arguments"]["body"]["items"][0].pop("matter_walled", None)
                rewritten.append(valid)
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
    for fixture in unique:
        if fixture["id"].startswith(("reg-downloadFileVersion-", "reg-createPayment-", "reg-getFileUpload-")):
            fixture["fixture_kind"] = "incomplete_source_excerpt"
            fixture["complete"] = False
            fixture["reproducible"] = False
            fixture["executed"] = False
            fixture["runtime_assertion"] = False
            fixture["missing_for_replay"] = ["grant", "registration", "subscriptions", "resource", "path", "state"]
            fixture["label"] = "Incomplete source excerpt. It does not carry the grant, registration, subscription, or resource state the recorded dispatcher result needs. The original test remains the regression."
        else:
            fixture["complete"] = False
            fixture["executed"] = False
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

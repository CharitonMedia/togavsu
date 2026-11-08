"""Stage 1 checks for the contract/simulator split.

These checks validate inventory shape, fixture traceability, builder imports, and
byte-identical OpenAPI output. They do not execute runtime fixtures and they do
not accept AC-002.
"""

from __future__ import annotations

import ast
import hashlib
import json
import unittest
from pathlib import Path

from contract.build import (
    COVERAGE_PATH,
    MARKETPLACE_PATH,
    MCP_PATH,
    OPENAPI_PATH,
    build_marketplace,
    build_spec,
    dump,
    render_coverage,
    render_mcp,
)
from contract.metadata import __all__ as METADATA_EXPORTS

ROOT = Path(__file__).resolve().parents[2]
INVENTORY_PATH = ROOT / "docs" / "api" / "stage1-inventory.json"
FIXTURE_PATH = ROOT / "docs" / "api" / "runtime-fixtures.json"
CONTRACT_TEST = ROOT / "api" / "tests" / "test_contract.py"

TRUSTED_NOT_IN_ARGUMENTS = {
    "approved_scopes",
    "authorization",
    "cookie",
    "core_history",
    "current_approved_scopes",
    "domain_handlers",
    "matter_membership",
    "matter_walls",
    "module_scopes",
    "role",
    "token_scopes",
}


def _load(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def _contract_symbols(path: Path) -> tuple[set[str], set[str]]:
    tree = ast.parse(path.read_text(encoding="utf-8"))
    methods = set()
    helpers = set()
    for node in tree.body:
        if isinstance(node, ast.FunctionDef):
            helpers.add(node.name)
        elif isinstance(node, ast.ClassDef) and node.name == "ContractTest":
            for item in node.body:
                if isinstance(item, ast.FunctionDef):
                    methods.add(f"ContractTest.{item.name}")
    return methods, helpers


def _imports(path: Path) -> set[str]:
    tree = ast.parse(path.read_text(encoding="utf-8"))
    found = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.ImportFrom) and node.module:
            found.add(node.module)
    return found


class Stage1InventoryTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.inventory = _load(INVENTORY_PATH)
        cls.fixtures = _load(FIXTURE_PATH)

    def test_inventory_lists_every_contract_test(self):
        methods, helpers = _contract_symbols(CONTRACT_TEST)
        listed = {row["id"] for row in self.inventory["tests"]}
        listed_helpers = {row["name"] for row in self.inventory["helpers"]}
        self.assertEqual(listed, methods)
        self.assertEqual(listed_helpers, helpers)
        nested_tests = {row["test"] for row in self.inventory["nested_helpers"]}
        self.assertTrue(nested_tests <= methods)
        self.assertGreater(len(self.inventory["nested_helpers"]), 0)
        self.assertTrue(self.inventory["deletion_blocked"])
        self.assertGreater(self.inventory["counts"]["blocking_gaps"], 0)
        self.assertEqual(len(self.inventory["blocking_gaps"]), self.inventory["counts"]["blocking_gaps"])
        for row in self.inventory["tests"]:
            self.assertIn(row["kind"], {"contract_validation", "simulator_behavior", "both"})
            self.assertEqual(row["baseline_sha"], "b4350f8af3803b8dc8439b2b2e7a431f198583d9")
            self.assertIn("AC-002", row["acceptance"])
            if row["kind"] == "contract_validation":
                self.assertFalse(row["blocking_removal"])
                self.assertEqual(row["runtime_status"], "contract_check")
            else:
                self.assertTrue(row["blocking_removal"])
                self.assertEqual(row["runtime_status"], "runtime_pending")
                self.assertTrue(row.get("gap"))

    def test_fixtures_are_runtime_pending_and_traceable(self):
        listed = {row["id"] for row in self.inventory["tests"]}
        ids = set()
        for fixture in self.fixtures["fixtures"]:
            self.assertEqual(fixture["runtime_status"], "runtime_pending")
            self.assertNotIn("passed", fixture)
            self.assertNotEqual(fixture.get("outcome"), "passed")
            self.assertIn(fixture["inventory_id"], listed)
            self.assertEqual(set(fixture["request"]), {"tool", "arguments"})
            self.assertIsInstance(fixture["trusted_context"], dict)
            self.assertIsInstance(fixture["request"]["arguments"], dict)
            for key in TRUSTED_NOT_IN_ARGUMENTS:
                self.assertNotIn(key, fixture["request"]["arguments"], fixture["id"])
            self.assertIsInstance(fixture["expected"]["result"], str)
            self.assertIsInstance(fixture["expected"]["effects"], bool)
            self.assertTrue(fixture["requirement"]["prd"])
            self.assertIn("AC-002", fixture["requirement"]["acceptance"])
            self.assertTrue(fixture["go_owner"])
            self.assertEqual(fixture["baseline_sha"], self.fixtures["baseline_sha"])
            ids.add(fixture["id"])
        self.assertEqual(len(ids), len(self.fixtures["fixtures"]))
        self.assertEqual(self.fixtures["runtime_status"], "runtime_pending")
        self.assertEqual(self.inventory["counts"]["runtime_fixtures"], len(ids))

    def test_required_regressions_are_present(self):
        by_id = {row["id"]: row for row in self.fixtures["fixtures"]}
        principals = {
            "createModuleHelpPage",
            "replaceModuleHelpPages",
            "deleteModuleHelpPage",
            "createModuleMarker",
            "updateModuleMarker",
            "revokeModuleMarker",
        }
        for operation_id in principals:
            for suffix in ("empty-token", "empty-current"):
                row = by_id[f"reg-{operation_id}-{suffix}"]
                self.assertEqual(row["expected"]["result"], "scope_denied")
                self.assertFalse(row["expected"]["effects"])
                self.assertEqual(row["go_owner"], "modules")
        for suffix in ("empty-approval", "empty-token", "empty-current"):
            row = by_id[f"reg-issueFilePublicationGrant-{suffix}"]
            self.assertEqual(row["trusted_context"]["module_scopes"], ["files:write"])
            self.assertEqual(row["expected"]["result"], "scope_denied")
            self.assertFalse(row["expected"]["effects"])
        self.assertEqual(by_id["reg-listCalendarEvent-routed"]["expected"]["routed"], "cal-mod")
        self.assertFalse(by_id["reg-listCalendarEvent-routed"]["expected"]["effects"])
        self.assertEqual(by_id["reg-getCalendarEvent-routed"]["expected"]["routed"], "cal-mod")
        self.assertIsNone(by_id["reg-listCalendarEvent-core-history"]["expected"]["routed"])
        self.assertTrue(by_id["reg-listCalendarEvent-core-history"]["expected"]["effects"])
        self.assertTrue(by_id["reg-listCalendarEvent-core-history"]["trusted_context"]["core_history"])
        unwalled = by_id["reg-bulkTimeEntry-unwalled"]
        self.assertEqual(unwalled["expected"]["result"], "allowed")
        self.assertTrue(unwalled["expected"]["effects"])
        self.assertIn("matter_walls", unwalled["trusted_context"])
        mixed = by_id["reg-bulkTimeEntry-mixed-off-wall"]
        self.assertEqual(mixed["expected"]["result"], "not_found")
        self.assertFalse(mixed["expected"]["effects"])
        ignored = by_id["reg-bulkTimeEntry-item-flag-ignored"]
        self.assertEqual(ignored["expected"]["result"], "not_found")
        self.assertTrue(ignored["trusted_context"]["matter_walls"]["018f1a00-7b3c-7d4e-8a11-333333333333"])

    def test_generated_artifacts_match_committed_bytes(self):
        spec = build_spec()
        produced = {
            OPENAPI_PATH: dump(spec),
            MARKETPLACE_PATH: dump(build_marketplace()),
            MCP_PATH: dump(render_mcp(spec)),
            COVERAGE_PATH: render_coverage(spec),
        }
        for path, text in produced.items():
            disk = path.read_text(encoding="utf-8")
            self.assertEqual(disk, text, path.name)
            digest = hashlib.sha256(disk.encode("utf-8")).hexdigest()
            rel = str(path.relative_to(ROOT))
            self.assertEqual(self.inventory["artifacts"][rel], digest, rel)

    def test_builder_does_not_import_the_simulator(self):
        for rel in (
            "api/contract/build.py",
            "api/contract/model.py",
            "api/contract/routes.py",
            "api/contract/schemas.py",
        ):
            self.assertNotIn("contract.policy", _imports(ROOT / rel), rel)
            self.assertIn("contract.metadata", _imports(ROOT / rel), rel)
        self.assertIn("finalize", METADATA_EXPORTS)
        self.assertIn("version_publication", METADATA_EXPORTS)
        self.assertNotIn("mcp_tool_dispatch", METADATA_EXPORTS)
        self.assertNotIn("operation_authorization", METADATA_EXPORTS)

    def test_no_hidden_runtime_consumers(self):
        found = {"contract.policy": set(), "contract.metadata": set()}
        for path in ROOT.rglob("*.py"):
            if ".git" in path.parts or path.name == "test_stage1.py":
                continue
            for module in _imports(path):
                if module in found:
                    found[module].add(str(path.relative_to(ROOT)))
        self.assertEqual(found["contract.policy"], {"api/tests/test_contract.py"})
        self.assertEqual(
            found["contract.metadata"],
            {
                "api/contract/build.py",
                "api/contract/model.py",
                "api/contract/policy.py",
                "api/contract/routes.py",
                "api/contract/schemas.py",
            },
        )
        for path in list(ROOT.rglob("*.go")) + list(ROOT.rglob("*.yml")) + list(ROOT.rglob("*.yaml")):
            if ".git" in path.parts:
                continue
            text = path.read_text(encoding="utf-8", errors="ignore")
            self.assertNotIn("contract.policy", text, str(path))
        self.assertEqual(self.inventory["hidden_runtime_consumers"], [])
        self.assertEqual(self.inventory["non_python_consumers"], [])

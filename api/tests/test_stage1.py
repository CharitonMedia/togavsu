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
from contract.stage1_check import classify_contract_tests

ROOT = Path(__file__).resolve().parents[2]
INVENTORY_PATH = ROOT / "docs" / "api" / "stage1-inventory.json"
FIXTURE_PATH = ROOT / "docs" / "api" / "runtime-fixtures.json"
MANIFEST_PATH = ROOT / "docs" / "api" / "scenario-manifest.json"
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
        cls.manifest = _load(MANIFEST_PATH)
        cls.classification = classify_contract_tests()

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
        self.assertGreaterEqual(self.inventory["counts"]["behavioral_methods"], 46)
        self.assertEqual(len(self.inventory["blocking_gaps"]), self.inventory["counts"]["blocking_gaps"])
        self.assertEqual(self.inventory["counting"]["unittest_tests"], self.classification["unittest_tests"])
        self.assertEqual(self.inventory["counting"]["setup_methods"], 1)
        self.assertEqual(
            self.inventory["counting"]["contract_test_methods_including_setup"],
            self.classification["unittest_tests"] + 1,
        )
        live = {row["name"]: row for row in self.classification["methods"]}
        for row in self.inventory["tests"]:
            current = live[row["id"].split(".", 1)[1]]
            self.assertEqual(row["kind"], current["kind"], row["id"])
            self.assertEqual(row["go_owner"], current["go_owner"], row["id"])
            self.assertEqual(row["acceptance"], current["acceptance"], row["id"])
            self.assertEqual(row["prd"], current["prd"], row["id"])
            self.assertFalse(row["complete"], row["id"])
            if row["blocking_removal"]:
                self.assertNotEqual(row["go_owner"], "unmapped", row["id"])
            self.assertIn(row["kind"], {"contract_validation", "simulator_behavior", "both"})
            self.assertEqual(row["baseline_sha"], "b4350f8af3803b8dc8439b2b2e7a431f198583d9")
            if row["kind"] == "contract_validation":
                self.assertFalse(row["blocking_removal"])
                self.assertEqual(row["runtime_status"], "contract_check")
            else:
                self.assertTrue(row["blocking_removal"])
                self.assertEqual(row["runtime_status"], "runtime_pending")
                self.assertTrue(row.get("gap"))
                self.assertEqual(row["retirement_status"], "pending")
        self.assertIn("_trust_poster", self.classification["behavioral_helpers"])
        self.assertEqual(self.classification["aliases"], {"module_ui_targets": "sidebar_entries"})
        self.assertEqual(len(self.classification["classes"][0]["methods"]), 8)
        self.assertEqual(len(self.classification["nested_structural_helpers"]), 3)
        reads = live["test_client_context_reads_follow_published_access"]
        self.assertEqual(reads["explicit_case_count"], 18)
        self.assertEqual(reads["kind"], "both")

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
            self.assertTrue(fixture["requirement"]["acceptance"])
            self.assertFalse(fixture["expected"]["public"])
            public_line = CONTRACT_TEST.read_text(encoding="utf-8").splitlines()[fixture["public_assertion_line"] - 1]
            self.assertIn('assertFalse(decision["public"]', public_line)
            self.assertGreater(fixture["line"], 0)
            self.assertIn(fixture["fixture_kind"], {"endpoint", "policy_probe", "incomplete_source_excerpt"})
            self.assertFalse(fixture["complete"])
            self.assertFalse(fixture["executed"])
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
        self.assertEqual(ignored["fixture_kind"], "policy_probe")
        trusted_wall = by_id["reg-bulkTimeEntry-trusted-wall"]
        self.assertEqual(trusted_wall["fixture_kind"], "endpoint")
        self.assertEqual(trusted_wall["expected"]["result"], "not_found")
        self.assertFalse(trusted_wall["expected"]["effects"])
        self.assertNotIn("matter_walled", trusted_wall["request"]["arguments"]["body"]["items"][0])
        self.assertTrue(ignored["trusted_context"]["matter_walls"]["018f1a00-7b3c-7d4e-8a11-333333333333"])
        for operation_id in ("createModuleMarker", "updateModuleMarker", "createModuleHelpPage", "replaceModuleHelpPages"):
            for suffix in ("empty-token", "empty-current"):
                probe = by_id[f"reg-{operation_id}-{suffix}"]
                endpoint = by_id[f"reg-{operation_id}-{suffix}-endpoint"]
                self.assertEqual(probe["fixture_kind"], "policy_probe")
                self.assertNotIn("body", probe["request"]["arguments"])
                self.assertEqual(endpoint["fixture_kind"], "endpoint")
                self.assertIn("body", endpoint["request"]["arguments"])
                self.assertEqual(endpoint["expected"]["result"], "scope_denied")
        excerpt = by_id["reg-downloadFileVersion-omitted-scopes"]
        self.assertEqual(excerpt["expected"]["result"], "scope_denied")
        self.assertEqual(excerpt["fixture_kind"], "incomplete_source_excerpt")
        self.assertFalse(excerpt["reproducible"])
        self.assertFalse(excerpt["runtime_assertion"])
        self.assertIn("grant", excerpt["missing_for_replay"])
        self.assertEqual(by_id["reg-createPayment-empty-token"]["expected"]["result"], "scope_denied")
        self.assertEqual(by_id["reg-getFileUpload-omitted-scopes"]["expected"]["result"], "scope_denied")
        self.assertEqual(by_id["reg-getFileUpload-minimal-scopes"]["expected"]["result"], "allowed")
        self.assertTrue(by_id["reg-getFileUpload-minimal-scopes"]["expected"]["effects"])

    def test_endpoint_fixtures_match_request_schemas(self):
        from api.tests.test_contract import _schema_validator

        spec = build_spec()
        operations = {
            operation["operationId"]: operation
            for item in spec["paths"].values()
            for operation in item.values()
            if isinstance(operation, dict) and operation.get("operationId")
        }
        for fixture in self.fixtures["fixtures"]:
            if fixture["fixture_kind"] != "endpoint":
                continue
            body = fixture["request"]["arguments"].get("body")
            operation = operations[fixture["operation"]]
            if body is None:
                self.assertFalse(operation.get("requestBody", {}).get("required", False), fixture["id"])
                continue
            schema = operation["requestBody"]["content"]["application/json"]["schema"]
            errors = list(_schema_validator(spec, schema).iter_errors(body))
            self.assertEqual(errors, [], fixture["id"])
        probe = next(row for row in self.fixtures["fixtures"] if row["id"] == "reg-bulkTimeEntry-item-flag-ignored")
        schema = operations["bulkTimeEntry"]["requestBody"]["content"]["application/json"]["schema"]
        item = probe["request"]["arguments"]["body"]["items"][0]
        errors = list(_schema_validator(spec, schema["properties"]["items"]["items"]).iter_errors(item))
        self.assertTrue(errors)

    def test_source_index_does_not_claim_conversion(self):
        self.assertEqual(self.manifest["document"], "source_index")
        self.assertFalse(self.manifest["complete"])
        self.assertEqual(self.manifest["converted_for_retirement"], 0)
        self.assertNotIn("identity_and_modules", self.manifest)
        behavioral = [row for row in self.classification["methods"] if row["behavioral"]]
        indexed = {row["id"] for row in self.manifest["entries"]}
        self.assertEqual(indexed, {f"ContractTest.{row['name']}" for row in behavioral})
        for row in self.manifest["entries"]:
            self.assertFalse(row["complete"], row["id"])
            self.assertFalse(row["is_scenario"], row["id"])
            self.assertFalse(row["executed"], row["id"])
            self.assertEqual(row["retirement_status"], "pending")
            self.assertEqual(row["source_line_basis"], "current_worktree")
        for domain in self.manifest["domains"].values():
            self.assertFalse(domain["complete"])
            self.assertEqual(domain["status"], "not_converted")
        reads = next(row for row in self.manifest["entries"] if row["id"].endswith("test_client_context_reads_follow_published_access"))
        self.assertEqual(sum(table["literal_rows"] for table in reads["literal_rows"]), 18)
        self.assertTrue(any(part.get("kind") == "dict" for table in reads["literal_rows"] for item in table["rows"] for part in item))
        joined = "\n".join(reads["assertion_statements"])
        self.assertIn("assertEqual", joined)
        generator = (ROOT / "api" / "contract" / "stage1_check.py").read_text(encoding="utf-8")
        self.assertNotIn('"complete": True', generator)
        self.assertNotIn("OWNER_RULES", generator)
        mapped = {
            "test_custom_fields_are_written_with_the_record": ("practice", ["AC-025"]),
            "test_bulk_custom_fields_follow_the_time_domain": ("practice", ["AC-025"]),
            "test_editor_pin": ("editor", ["AC-041"]),
            "test_file_links_and_conversion_result": ("multiple", ["AC-039", "AC-042"]),
            "test_doc_tender_result_is_readable": ("doc-tender", ["AC-043"]),
            "test_naming_refuses_a_status_token": ("doc-tender", ["AC-051"]),
            "test_include_views_cover_extensible_core_records": ("modules", ["AC-036"]),
            "test_expense_and_payment_includes": ("modules", ["AC-036"]),
        }
        live = {row["name"]: row for row in self.classification["methods"]}
        for name, (owner, acceptance) in mapped.items():
            self.assertEqual(live[name]["go_owner"], owner, name)
            self.assertEqual(live[name]["acceptance"], acceptance, name)
        covered = {row["inventory_id"] for row in self.fixtures["fixtures"]}
        self.assertTrue(covered <= {"ContractTest.test_dispatcher_parity_matrix", "ContractTest.test_mcp_dispatch_reuses_the_operation_policy"})
        self.assertEqual(self.inventory["counting"]["converted_for_retirement"], 0)
        spec_ids = sorted(
            operation["operationId"]
            for item in build_spec()["paths"].values()
            for operation in item.values()
            if isinstance(operation, dict) and operation.get("operationId")
        )
        self.assertEqual(self.manifest["spec_operation_ids"], spec_ids)
        self.assertTrue(self.inventory["deletion_blocked"])
        self.assertEqual(self.fixtures["runtime_status"], "runtime_pending")

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
            self.assertEqual(self.inventory["baseline_artifacts"][rel], {
                "api/openapi/v1/openapi.json": "e2aad30720be7394306142b786649c0414d620fc595c175d63a924989e3d8195",
                "api/openapi/marketplace/openapi.json": "d5a6b6855048a88944827473bf431ec22c5ff98275d446a6af715f72e581eefb",
                "api/openapi/v1/mcp-tools.json": "4581fcdcb373bf6d4e17dbd1676a618089604c77836d4840b8d85f5b85363fc6",
                "docs/api/coverage.md": "a7a9f8a630722b302c13338135e0e36ffab008b1acfced995de4ac3aaad94bfa",
            }[rel])
            self.assertEqual(self.inventory["artifacts"][rel], digest, rel)
        self.assertNotEqual(
            self.inventory["artifacts"]["api/openapi/v1/openapi.json"],
            self.inventory["baseline_artifacts"]["api/openapi/v1/openapi.json"],
        )
        for rel in (
            "api/openapi/marketplace/openapi.json",
            "api/openapi/v1/mcp-tools.json",
            "docs/api/coverage.md",
        ):
            self.assertEqual(self.inventory["artifacts"][rel], self.inventory["baseline_artifacts"][rel], rel)
        delta_ids = {row["id"] for row in self.inventory["intentional_generated_deltas"]}
        self.assertEqual(
            delta_ids,
            {"merge-matter-both-walls", "matter-patch-cannot-reopen", "stopped-timer-response"},
        )

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

"""Assemble, check, and write the Togavsu contract documents."""

from __future__ import annotations

import json
from pathlib import Path

from contract.model import Contract
from contract.routes import add_routes
from contract.scopes import SCOPE_PATTERNS, SCOPES
from contract.seeds import DOC_CATEGORIES, LAWCRAFT_COMMIT, NAMING_TOKENS, PARTY_ROLES
from contract.settings import SETTINGS

ROOT = Path(__file__).resolve().parents[2]
OPENAPI_PATH = ROOT / "api" / "openapi" / "v1" / "openapi.json"
MCP_PATH = ROOT / "api" / "openapi" / "v1" / "mcp-tools.json"
COVERAGE_PATH = ROOT / "docs" / "api" / "coverage.md"
MARKETPLACE_PATH = ROOT / "api" / "openapi" / "marketplace" / "openapi.json"


def build_spec() -> dict:
    api = Contract()
    add_routes(api)
    spec = api.spec
    spec["x-togavsu-settings"] = [
        {key: value for key, value in item.items() if key != "help"} | {"help": item["help"]}
        for item in SETTINGS
    ]
    spec["x-togavsu-seeds"] = {
        "doc_categories": DOC_CATEGORIES,
        "party_roles": PARTY_ROLES,
        "naming_tokens": NAMING_TOKENS,
        "lawcraft_commit": LAWCRAFT_COMMIT,
        "default_folders": ["Correspondence", "Pleadings", "Discovery", "Evidence", "Client documents", "Billing", "Notes"],
    }
    spec["x-togavsu-scope-catalog"] = [{"scope": key, "description": value} for key, value in sorted(SCOPES.items())]
    spec["x-togavsu-scope-patterns"] = SCOPE_PATTERNS
    spec["webhooks"] = _webhooks()
    _check(spec)
    return spec


def build_marketplace() -> dict:
    spec = {
        "openapi": "3.1.0",
        "jsonSchemaDialect": "https://json-schema.org/draft/2020-12/schema",
        "info": {
            "title": "Togavsu Marketplace API",
            "version": "1.0.0",
            "summary": "Read and operator contract for the separate marketplace service.",
            "description": (
                "Proposal only. The core browses and polls this service. "
                "The operator does not review modules. Signing identifies the publisher. "
                "No production host is named here."
            ),
        },
        "servers": [{"url": "https://marketplace.example.invalid", "description": "Placeholder. Not a live host."}],
        "paths": {},
        "components": {
            "securitySchemes": {
                "bearerAuth": {"type": "http", "scheme": "bearer"}
            },
            "schemas": {
                "Listing": {
                    "type": "object",
                    "additionalProperties": False,
                    "required": ["id", "name", "author", "version", "flagged", "pulled", "notice"],
                    "properties": {
                        "id": {"type": "string"},
                        "module_id": {"type": "string"},
                        "name": {"type": "string"},
                        "author": {"type": "string"},
                        "signing_key": {"type": "string"},
                        "version": {"type": "string"},
                        "permissions": {"type": "array", "items": {"type": "string"}},
                        "price_note": {"type": "string"},
                        "flagged": {"type": "boolean"},
                        "flag_note": {"type": "string"},
                        "pulled": {"type": "boolean"},
                        "notice": {"type": "string"},
                    },
                },
                "Alert": {
                    "type": "object",
                    "additionalProperties": False,
                    "required": ["module_id", "kind", "note"],
                    "properties": {
                        "module_id": {"type": "string"},
                        "version": {"type": "string"},
                        "kind": {"type": "string", "enum": ["flag", "pull"]},
                        "note": {"type": "string"},
                    },
                },
                "Problem": {
                    "type": "object",
                    "additionalProperties": False,
                    "required": ["status", "code", "detail"],
                    "properties": {
                        "status": {"type": "integer"},
                        "code": {"type": "string"},
                        "detail": {"type": "string"},
                    },
                },
            },
        },
    }
    problem = {"application/problem+json": {"schema": {"$ref": "#/components/schemas/Problem"}}}

    def add(method: str, path: str, operation_id: str, summary: str, *, auth: bool, response: str, body: str | None = None) -> None:
        operation = {
            "operationId": operation_id,
            "summary": summary,
            "responses": {
                "200": {
                    "description": "Success.",
                    "content": {"application/json": {"schema": {"$ref": f"#/components/schemas/{response}"}}},
                },
                "404": {"description": "Not found.", "content": problem},
            },
            "security": [{"bearerAuth": []}] if auth else [],
        }
        if body:
            operation["requestBody"] = {
                "required": True,
                "content": {"application/json": {"schema": {"$ref": f"#/components/schemas/{body}"}}},
            }
        names = [part.strip("{}") for part in path.split("/") if part.startswith("{")]
        if names:
            operation["parameters"] = [
                {"name": name, "in": "path", "required": True, "schema": {"type": "string"}} for name in names
            ]
        spec["paths"].setdefault(path, {})[method] = operation

    add("get", "/v1/listings", "listListings", "Search listings. Pulled modules are omitted.", auth=False, response="Listing")
    spec["paths"]["/v1/listings"]["get"]["responses"]["200"]["content"]["application/json"]["schema"] = {
        "type": "object",
        "required": ["data"],
        "properties": {"data": {"type": "array", "items": {"$ref": "#/components/schemas/Listing"}}},
    }
    add("get", "/v1/listings/{listing_id}", "getListing", "One listing, including a flag note when flagged.", auth=False, response="Listing")
    add("get", "/v1/alerts", "listAlerts", "Flags and pulls for module ids the core asks about.", auth=True, response="Alert")
    spec["paths"]["/v1/alerts"]["get"]["parameters"] = [
        {"name": "module_id", "in": "query", "required": False, "schema": {"type": "string"}},
        {"name": "since", "in": "query", "required": False, "schema": {"type": "string", "format": "date-time"}},
    ]
    spec["paths"]["/v1/alerts"]["get"]["responses"]["200"]["content"]["application/json"]["schema"] = {
        "type": "object",
        "required": ["data"],
        "properties": {"data": {"type": "array", "items": {"$ref": "#/components/schemas/Alert"}}},
    }
    add("get", "/v1/signing-keys", "listSigningKeys", "Publisher signing keys the core trusts from the marketplace.", auth=True, response="Listing")
    spec["paths"]["/v1/signing-keys"]["get"]["responses"]["200"]["content"]["application/json"]["schema"] = {
        "type": "object",
        "required": ["data"],
        "properties": {"data": {"type": "array", "items": {"type": "object", "required": ["author", "public_key_pem"], "properties": {"author": {"type": "string"}, "public_key_pem": {"type": "string"}}}}},
    }
    add(
        "post",
        "/v1/operator/listings/{listing_id}/flag",
        "flagListing",
        "Operator flag. Installs stay possible. The note is shown on the listing.",
        auth=True,
        response="Listing",
    )
    add(
        "post",
        "/v1/operator/listings/{listing_id}/pull",
        "pullListing",
        "Operator pull. The listing leaves search and cannot be installed from the marketplace. A local file install remains possible.",
        auth=True,
        response="Listing",
    )
    return spec


def render_coverage(spec: dict) -> str:
    lines = [
        "# API and MCP coverage",
        "",
        "Generated from `api/openapi/v1/openapi.json`. This is the contract matrix for AC-002.",
        "It does not claim the handlers or the MCP server are implemented.",
        "",
        "| Feature | Method | Path | Operation | MCP | Roles | Scopes |",
        "| --- | --- | --- | --- | --- | --- | --- |",
    ]
    for path, item in spec["paths"].items():
        for method, operation in item.items():
            if method.startswith("x-"):
                continue
            mcp = operation["x-togavsu-mcp"]
            parity = mcp["parity"]
            tool = mcp.get("tool") or mcp.get("canonical") or parity
            access = operation["x-togavsu-access"]
            roles = ", ".join(access["roles"]) or "public"
            scopes = ", ".join(access["scopes"]) or "none"
            features = ", ".join(operation["x-togavsu-features"])
            lines.append(
                f"| {features} | `{method.upper()}` | `{path}` | `{operation['operationId']}` | {parity}:{tool} | {roles} | {scopes} |"
            )
    lines.append("")
    lines.append(f"Operations: {sum(1 for item in spec['paths'].values() for method in item if not method.startswith('x-'))}.")
    lines.append("")
    return "\n".join(lines)


def render_mcp(spec: dict) -> dict:
    tools = []
    for path, item in spec["paths"].items():
        for method, operation in item.items():
            if method.startswith("x-"):
                continue
            mcp = operation["x-togavsu-mcp"]
            if mcp["parity"] != "required":
                continue
            tools.append(
                {
                    "name": operation["operationId"],
                    "method": method.upper(),
                    "path": path,
                    "features": operation["x-togavsu-features"],
                    "scopes": operation["x-togavsu-access"]["scopes"],
                    "roles": operation["x-togavsu-access"]["roles"],
                }
            )
    return {"transport": "POST /mcp", "tools": tools}


def dump(document: dict) -> str:
    return json.dumps(document, indent=2, ensure_ascii=False) + "\n"


def write_outputs() -> dict:
    spec = build_spec()
    market = build_marketplace()
    OPENAPI_PATH.parent.mkdir(parents=True, exist_ok=True)
    MARKETPLACE_PATH.parent.mkdir(parents=True, exist_ok=True)
    COVERAGE_PATH.parent.mkdir(parents=True, exist_ok=True)
    OPENAPI_PATH.write_text(dump(spec), encoding="utf-8")
    MARKETPLACE_PATH.write_text(dump(market), encoding="utf-8")
    MCP_PATH.write_text(dump(render_mcp(spec)), encoding="utf-8")
    COVERAGE_PATH.write_text(render_coverage(spec), encoding="utf-8")
    return spec


def _webhooks() -> dict:
    return {
        "moduleEvent": {
            "post": {
                "operationId": "deliverModuleEvent",
                "summary": "Deliver a signed event to a subscribed module.",
                "description": (
                    "The core POSTs the event to the module URL from the manifest subscription. "
                    "X-Togavsu-Signature is sha256= plus the hex HMAC-SHA256 of the raw body. "
                    "X-Togavsu-Timestamp is unix seconds. Retries follow webhooks.retry_attempts "
                    "across webhooks.retry_window_hours. The starting values are 8 attempts and 24 hours."
                ),
                "parameters": [
                    {
                        "name": "X-Togavsu-Signature",
                        "in": "header",
                        "required": True,
                        "schema": {"type": "string"},
                    },
                    {
                        "name": "X-Togavsu-Timestamp",
                        "in": "header",
                        "required": True,
                        "schema": {"type": "string"},
                    },
                ],
                "requestBody": {
                    "required": True,
                    "content": {"application/json": {"schema": {"$ref": "#/components/schemas/WebhookEvent"}}},
                },
                "responses": {
                    "2XX": {"description": "The module accepted the event."},
                    "default": {"description": "The core will retry."},
                },
            }
        }
    }


def _check(spec: dict) -> None:
    schemas = spec["components"]["schemas"]
    missing = []

    def walk(node):
        if isinstance(node, dict):
            ref = node.get("$ref")
            if isinstance(ref, str) and ref.startswith("#/components/schemas/"):
                name = ref.rsplit("/", 1)[1]
                if name not in schemas:
                    missing.append(name)
            for value in node.values():
                walk(value)
        elif isinstance(node, list):
            for value in node:
                walk(value)

    walk(spec)
    if missing:
        raise SystemExit(f"missing schemas: {sorted(set(missing))}")
    used = set()
    for item in spec["paths"].values():
        for method, operation in item.items():
            if method.startswith("x-"):
                continue
            for scope in operation["x-togavsu-access"]["scopes"]:
                used.add(scope)
                if scope not in SCOPES:
                    raise SystemExit(f"scope {scope} is not in the catalog")
            mcp = operation["x-togavsu-mcp"]
            if mcp["parity"] == "required" and mcp["tool"] != operation["operationId"]:
                raise SystemExit(f"mcp tool mismatch on {operation['operationId']}")
            if mcp["parity"] == "alias":
                canonical = mcp["canonical"]
                if canonical not in _operation_ids(spec):
                    raise SystemExit(f"alias {operation['operationId']} missing {canonical}")
    unused = sorted(set(SCOPES) - used)
    if unused:
        raise SystemExit(f"unused scopes: {unused}")


def _operation_ids(spec: dict) -> set[str]:
    found = set()
    for item in spec["paths"].values():
        for method, operation in item.items():
            if method.startswith("x-") or not isinstance(operation, dict):
                continue
            found.add(operation["operationId"])
    return found


if __name__ == "__main__":
    spec = write_outputs()
    count = sum(1 for item in spec["paths"].values() for method in item if not method.startswith("x-"))
    print(f"wrote {OPENAPI_PATH} operations={count}")

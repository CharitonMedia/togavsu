# API contract proposal

Versioned OpenAPI 3.1 for the Togavsu core and a separate marketplace read/operator document. This tree does not serve HTTP.

| File | What it is |
| --- | --- |
| `openapi/v1/openapi.json` | Core contract. Paths under `/api/v1`, plus `/mcp`, `/llms.txt`, `/healthz`, `/help/guide`, and `/ical/{secret}.ics`. |
| `openapi/v1/mcp-tools.json` | One MCP tool per operation, generated from the core document. |
| `openapi/marketplace/openapi.json` | Marketplace service the core polls. Host is a placeholder. |
| `contract/` | Builder. Edit this, then regenerate. `metadata.py` publishes the document. `policy.py` is the call simulator and is not imported by the builder. |
| `tests/test_contract.py` | Validator and parity checks. |

Regenerate and check:

```bash
python3 -m pip install -r api/requirements-dev.txt
sh api/check.sh
```

`api/requirements-dev.txt` pins `openapi-spec-validator` 0.9.0. `api/check.sh` fails if that package or a generated file is missing. It runs every test under `api/tests`, both OpenAPI validators, the broken-spec and reference-resolution check, then regenerates the four contract artifacts and requires `git diff` to be empty. It does not execute `docs/api/runtime-fixtures.json`.

Conventions: [docs/api/conventions.md](../docs/api/conventions.md). MCP mapping: [docs/api/mcp.md](../docs/api/mcp.md). Coverage matrix: [docs/api/coverage.md](../docs/api/coverage.md).

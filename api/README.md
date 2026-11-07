# API contract proposal

Versioned OpenAPI 3.1 for the Togavsu core and a separate marketplace read/operator document. This tree does not serve HTTP.

| File | What it is |
| --- | --- |
| `openapi/v1/openapi.json` | Core contract. Paths under `/api/v1`, plus `/mcp`, `/llms.txt`, `/healthz`, `/help/guide`, and `/ical/{secret}.ics`. |
| `openapi/v1/mcp-tools.json` | One MCP tool per operation, generated from the core document. |
| `openapi/marketplace/openapi.json` | Marketplace service the core polls. Host is a placeholder. |
| `contract/` | Builder. Edit this, then regenerate. |
| `tests/test_contract.py` | Validator and parity checks. |

Regenerate and check:

```bash
pip install -r api/requirements-dev.txt
PYTHONPATH=api python3 -m contract.build
PYTHONPATH=api python3 -m unittest api.tests.test_contract
```

`openapi-spec-validator` 0.9.0 is the validator. The unit test runs it on both documents and on a broken document that must fail.

Conventions: [docs/api/conventions.md](../docs/api/conventions.md). MCP mapping: [docs/api/mcp.md](../docs/api/mcp.md). Coverage matrix: [docs/api/coverage.md](../docs/api/coverage.md).

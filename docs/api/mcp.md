# MCP parity

`POST /mcp` is JSON-RPC 2.0. It is the transport, not a tool that calls itself. Auth is the same bearer token or session cookie as the HTTP API. `tools/list` and `tools/call` are the methods an agent uses. `tools/call` takes:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "getMatter",
    "arguments": {
      "matter_id": "018f...",
      "include": "matter-clock.targets",
      "idempotency_key": "optional-on-writes",
      "body": {}
    }
  }
}
```

`name` is the OpenAPI `operationId`. Arguments are the path parameters, the query parameters, `idempotency_key` on writes, and `body` when the HTTP call has a JSON body. The success payload is the HTTP success body. A failure sets the JSON-RPC error `data` to the problem+json body.

Every operation carries `x-togavsu-mcp`:

| `parity` | Meaning |
| --- | --- |
| `required` | `tool` equals `operationId`. The tool is in `api/openapi/v1/mcp-tools.json`. |
| `alias` | Browser path for a canonical tool. `/help/guide` aliases `getHelpGuide`. `/api/v1/llms.txt` aliases `getLlmsTxt`. `/api/v1/health` aliases `getHealthz`. |
| `transport` | Only `postMcp`. |

A feature in the coverage matrix is reachable on `/api/v1` (or the public path the product scope names) and through one of these tools. The matrix is `docs/api/coverage.md`. Generating that file is part of `python3 -m contract.build`. Handlers and the MCP server are later work. This document is the mapping they have to match.

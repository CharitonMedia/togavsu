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

`name` is the OpenAPI `operationId`. Each required tool records `x-togavsu-mcp.transfer`, and the same object is copied onto `api/openapi/v1/mcp-tools.json`.

JSON calls use `path`, `query`, `idempotency_key`, and `body`. A call with a header parameter also sends `headers`. A raw body sends `body_base64` instead of `body`: standard base64 of the octets, no line breaks. A binary success uses the same encoding in the tool result, with `media_type` naming the HTTP type. A failure still sets the JSON-RPC error `data` to the problem+json body.

Chunk upload:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "putFileChunk",
    "arguments": {
      "upload_id": "018f...",
      "headers": {"Content-Range": "bytes 0-8388607/9000000"},
      "idempotency_key": "chunk-0",
      "body_base64": "AAECAw=="
    }
  }
}
```

`putFileChunk.transfer.body.encoding` is `base64` and `transfer.headers` includes `Content-Range`. `downloadFileVersion`, `downloadFileRendition`, `getInvoicePdf`, and `createFileArchive` set `transfer.result.encoding` to `base64`. The result is `{ "media_type": "application/octet-stream", "body_base64": "..." }` (or `application/pdf` or `application/zip`).

`installModule` accepts either JSON `body` (`ModuleInstall`) or multipart `package_base64` (`ModulePackageUpload`). `transfer.body.encoding` is `one_of`. `createBackupUpload` is `body_base64` of the archive.

Every operation carries `x-togavsu-mcp`:

| `parity` | Meaning |
| --- | --- |
| `required` | `tool` equals `operationId`. The tool is in `api/openapi/v1/mcp-tools.json`. |
| `alias` | Browser path for a canonical tool. `/help/guide` aliases `getHelpGuide`. `/api/v1/llms.txt` aliases `getLlmsTxt`. `/api/v1/health` aliases `getHealthz`. |
| `transport` | Only `postMcp`. |

A feature in the coverage matrix is reachable on `/api/v1` (or the public path the product scope names) and through one of these tools. The matrix is `docs/api/coverage.md`. Generating that file is part of `python3 -m contract.build`. Handlers and the MCP server are later work. This document is the mapping they have to match.

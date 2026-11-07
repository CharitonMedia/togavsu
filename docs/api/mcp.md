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
      "path": {"matter_id": "018f..."},
      "query": {"include": "matter-clock.targets"},
      "context_token": "signed-context"
    }
  }
}
```

`name` is the OpenAPI `operationId`. Arguments use one shape: `path` and `query` objects, then `idempotency_key` on writes, `headers` when the operation has header parameters, `body` for JSON, and `body_base64` for raw octets. A delegated call adds `context_token`, forwarded only as `X-Togavsu-Context-Token`. A noninteractive call also adds `task_authorization`, forwarded only as `X-Togavsu-Task-Authorization`. Those arguments do not accept `Authorization`, `Host`, or a `roles` list. The viewer role comes from the verified context token. `x-togavsu-mcp.transfer.arguments` lists those keys in that order, and `mcp-tools.json` copies the same object. A failure sets the JSON-RPC error `data` to the problem+json body.

Chunk upload:

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/call",
  "params": {
    "name": "putFileChunk",
    "arguments": {
      "path": {"upload_id": "018f..."},
      "query": {},
      "idempotency_key": "chunk-0",
      "headers": {"Content-Range": "bytes 0-8388607/9000000"},
      "body_base64": "AAECAw==",
      "context_token": "signed-context",
      "task_authorization": "signed-task"
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

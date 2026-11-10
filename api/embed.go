// Package api embeds the published API contract so the core serves the same files CI validates.
package api

import _ "embed"

// OpenAPI is api/openapi/v1/openapi.json.
//
//go:embed openapi/v1/openapi.json
var OpenAPI []byte

// MCPTools is api/openapi/v1/mcp-tools.json: one tool per API operation, with scopes and roles.
//
//go:embed openapi/v1/mcp-tools.json
var MCPTools []byte

package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
)

// tool is one entry of api/openapi/v1/mcp-tools.json: an API operation and its scopes.
type tool struct {
	Name   string   `json:"name"`
	Method string   `json:"method"`
	Path   string   `json:"path"`
	Scopes []string `json:"scopes"`
	Roles  []string `json:"roles"`
	segs   []string
}

func loadTools(raw []byte) ([]tool, error) {
	var doc struct {
		Tools []tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	for i := range doc.Tools {
		doc.Tools[i].segs = strings.Split(strings.Trim(doc.Tools[i].Path, "/"), "/")
	}
	return doc.Tools, nil
}

func (t tool) match(method, path string) (int, bool) {
	if !strings.EqualFold(t.Method, method) {
		return 0, false
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) != len(t.segs) {
		return 0, false
	}
	var wild int
	for i, s := range t.segs {
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			if segs[i] == "" {
				return 0, false
			}
			wild++
			continue
		}
		if s != segs[i] {
			return 0, false
		}
	}
	return wild, true
}

// toolFor finds the contract operation for a request, preferring literal segments.
func (a *App) toolFor(method, path string) (tool, bool) {
	if method == http.MethodHead {
		method = http.MethodGet
	}
	var best tool
	var found bool
	var fewest int
	for _, t := range a.tools {
		if wild, ok := t.match(method, path); ok && (!found || wild < fewest) {
			best, found, fewest = t, true, wild
		}
	}
	return best, found
}

// mounted reports whether this core serves the operation, without running it.
func (a *App) mounted(t tool) bool {
	if t.Path == "/mcp" {
		return false
	}
	path := t.Path
	for _, s := range t.segs {
		if strings.HasPrefix(s, "{") {
			path = strings.ReplaceAll(path, s, "x")
		}
	}
	r, err := http.NewRequest(t.Method, path, nil)
	if err != nil {
		return false
	}
	_, pattern := a.mux.Handler(r)
	if pattern == "" {
		return false
	}
	if pattern != "/" {
		return true
	}
	for _, mux := range a.domains {
		if _, pattern = mux.Handler(r); pattern != "" {
			return true
		}
	}
	return false
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func rpcReply(w http.ResponseWriter, id json.RawMessage, result any, e *rpcErr) {
	out := map[string]any{"jsonrpc": "2.0", "id": id}
	if len(id) == 0 {
		out["id"] = nil
	}
	if e != nil {
		out["error"] = e
	} else {
		out["result"] = result
	}
	writeJSON(w, http.StatusOK, out)
}

// mcp serves JSON-RPC 2.0 over HTTP POST. Every tools/call is dispatched through the
// same /api/v1 handler with the caller's credentials, so roles, scopes, walls and
// redaction apply exactly as they do to the API.
func (a *App) mcp(w http.ResponseWriter, r *http.Request) {
	var req rpcReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
		rpcReply(w, nil, nil, &rpcErr{Code: -32600, Message: "invalid JSON-RPC request"})
		return
	}
	if req.Method == "notifications/initialized" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if _, err := a.mcpCaller(r); err != nil {
		writeErr(w, err)
		return
	}
	switch req.Method {
	case "initialize":
		rpcReply(w, req.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "togavsu", "version": Version},
		}, nil)
	case "ping":
		rpcReply(w, req.ID, map[string]any{}, nil)
	case "tools/list":
		rpcReply(w, req.ID, map[string]any{"tools": a.listTools()}, nil)
	case "tools/call":
		a.callTool(w, r, req)
	default:
		rpcReply(w, req.ID, nil, &rpcErr{Code: -32601, Message: "method not found"})
	}
}

// mcpCaller authenticates the /mcp request. A cookie session is a POST and needs its
// CSRF token. A bearer token needs no route scope here: each tools/call is checked
// against its own operation's scopes when it is dispatched.
func (a *App) mcpCaller(r *http.Request) (domain.Principal, error) {
	if h := r.Header.Get("Authorization"); h != "" {
		token, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || strings.TrimSpace(token) == "" {
			return domain.Principal{}, domain.Unauthenticated("authorization must be a bearer token")
		}
		c, err := a.Identity.Authenticate(r.Context(), identity.Presented{Bearer: strings.TrimSpace(token)})
		return c.Principal, err
	}
	return a.principal(r)
}

func (a *App) listTools() []map[string]any {
	var out []map[string]any
	for _, t := range a.tools {
		if !a.mounted(t) {
			continue
		}
		var params []string
		for _, s := range t.segs {
			if strings.HasPrefix(s, "{") {
				params = append(params, strings.Trim(s, "{}"))
			}
		}
		sort.Strings(params)
		pathProps := map[string]any{}
		for _, p := range params {
			pathProps[p] = map[string]any{"type": "string"}
		}
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Method + " " + t.Path,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":  map[string]any{"type": "object", "properties": pathProps, "required": params},
					"query": map[string]any{"type": "object"},
					"body":  map[string]any{},
				},
			},
		})
	}
	return out
}

func (a *App) callTool(w http.ResponseWriter, r *http.Request, req rpcReq) {
	var params struct {
		Name      string `json:"name"`
		Arguments struct {
			Path  map[string]string `json:"path"`
			Query map[string]any    `json:"query"`
			Body  json.RawMessage   `json:"body"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		rpcReply(w, req.ID, nil, &rpcErr{Code: -32602, Message: "invalid tool arguments"})
		return
	}
	var t tool
	found := false
	for _, c := range a.tools {
		if c.Name == params.Name {
			t, found = c, true
			break
		}
	}
	if !found || !a.mounted(t) {
		rpcReply(w, req.ID, nil, &rpcErr{Code: -32602, Message: "unknown tool"})
		return
	}
	path := t.Path
	for _, s := range t.segs {
		if strings.HasPrefix(s, "{") {
			v := params.Arguments.Path[strings.Trim(s, "{}")]
			if v == "" {
				rpcReply(w, req.ID, nil, &rpcErr{Code: -32602, Message: "missing path argument " + s})
				return
			}
			path = strings.ReplaceAll(path, s, url.PathEscape(v))
		}
	}
	q := url.Values{}
	for k, v := range params.Arguments.Query {
		switch x := v.(type) {
		case string:
			q.Set(k, x)
		default:
			b, _ := json.Marshal(x)
			q.Set(k, string(b))
		}
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var body []byte
	if len(params.Arguments.Body) > 0 && string(params.Arguments.Body) != "null" {
		body = params.Arguments.Body
	}
	inner, err := http.NewRequestWithContext(r.Context(), t.Method, path, bytes.NewReader(body))
	if err != nil {
		rpcReply(w, req.ID, nil, &rpcErr{Code: -32602, Message: "invalid tool arguments"})
		return
	}
	for _, h := range []string{"Authorization", "Cookie", "X-CSRF-Token", "Idempotency-Key"} {
		if v := r.Header.Get(h); v != "" {
			inner.Header.Set(h, v)
		}
	}
	if body != nil {
		inner.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	a.Handler().ServeHTTP(rec, inner)
	text := rec.Body.String()
	result := map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": rec.Code >= http.StatusBadRequest,
	}
	var structured any
	if json.Unmarshal(rec.Body.Bytes(), &structured) == nil {
		if obj, ok := structured.(map[string]any); ok {
			result["structuredContent"] = obj
		} else {
			result["structuredContent"] = map[string]any{"result": structured}
		}
	}
	rpcReply(w, req.ID, result, nil)
}

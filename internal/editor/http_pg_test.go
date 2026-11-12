package editor

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/CharitonMedia/togavsu/internal/domain"
	editorpages "github.com/CharitonMedia/togavsu/web/templates/editor"
)

// server serves the editor's routes; X-Test-User picks the principal, as the core's
// session or bearer authentication would.
func (e *env) server(dist fstest.MapFS) (*httptest.Server, map[string]domain.Principal) {
	e.t.Helper()
	who := map[string]domain.Principal{"staff": e.staff, "other": e.other, "admin": e.admin, "client": e.client, "attorney": e.attorney,
		"outsider": e.outsider, "accountant": e.accountant}
	var d fs.FS
	if dist != nil {
		d = dist
	}
	h, err := NewHandler(e.svc, editorpages.FS, d, func(r *http.Request) (domain.Principal, error) {
		p, ok := who[r.Header.Get("X-Test-User")]
		if !ok {
			return domain.Principal{}, domain.Unauthenticated("sign in is required")
		}
		return p, nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	srv := httptest.NewServer(h.Routes())
	e.t.Cleanup(srv.Close)
	return srv, who
}

func call(t *testing.T, srv *httptest.Server, user, method, path string, body io.Reader, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Test-User", user)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func TestPGEditorAPIOverHTTP(t *testing.T) {
	e := newEnv(t)
	srv, _ := e.server(nil)
	f := e.create(e.staff, "Letter.docx", e.matter, e.letterWithMergeFields(), false)
	path := "/api/v1/files/" + string(f.ID)

	res, b := call(t, srv, "staff", "GET", "/api/v1/editor", nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"commit":"770a8fab12392485c0c2789fb254da7d9296df4d"`) || !strings.Contains(string(b), `"read_only":true`) {
		t.Fatalf("GET /api/v1/editor = %d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, "staff", "GET", "/api/v1/editor/commands", nil)
	var list struct {
		Data []EditorCommand `json:"data"`
	}
	if err := json.Unmarshal(b, &list); err != nil || res.StatusCode != 200 {
		t.Fatalf("commands = %d %s", res.StatusCode, b)
	}
	ids := map[string]bool{}
	for _, c := range list.Data {
		ids[c.ID] = true
	}
	if !ids["law.pleadings.new"] || !ids["mailings.recipients"] || ids["file.open"] || ids["insert.textFromFile"] {
		t.Fatal("the allowed command list is wrong")
	}
	if res, _ := call(t, srv, "client", "GET", "/api/v1/editor/commands", nil); res.StatusCode != 403 {
		t.Fatalf("a client listed editor commands: %d", res.StatusCode)
	}

	if res, b := call(t, srv, "staff", "POST", path+"/lock", nil); res.StatusCode != 200 || !strings.Contains(string(b), string(e.staff.UserID)) {
		t.Fatalf("lock = %d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, "other", "POST", path+"/lock", nil)
	var held lockHeldBody
	_ = json.Unmarshal(b, &held)
	if res.StatusCode != 409 || held.Code != "edit_lock_held" || held.Holder.UserID != e.staff.UserID || held.Holder.Name != "Sam Staff" || held.Since == "" {
		t.Fatalf("second lock = %d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, "staff", "POST", path+"/lock/renew", nil); res.StatusCode != 200 {
		t.Fatalf("renew = %d", res.StatusCode)
	}
	if res, _ := call(t, srv, "other", "DELETE", path+"/lock", nil); res.StatusCode != 403 {
		t.Fatalf("other's release = %d", res.StatusCode)
	}
	if res, _ := call(t, srv, "admin", "DELETE", path+"/lock", nil); res.StatusCode != 204 {
		t.Fatalf("admin release = %d", res.StatusCode)
	}

	refusedRun := `{"commands":[{"id":"insert.textFromFile","parameters":{"path":"/etc/hostname"}}],"save_as":"version"}`
	res, b = call(t, srv, "staff", "POST", path+"/editor-runs", strings.NewReader(refusedRun), "Content-Type", "application/json")
	if res.StatusCode != 422 || !strings.Contains(string(b), `"code":"command_refused"`) {
		t.Fatalf("refused run = %d %s", res.StatusCode, b)
	}
	run := `{"commands":[{"id":"mailings.start","parameters":{"kind":"letters"}},{"id":"mailings.recipients","parameters":{"csv":"ClientName,HearingDate\nJane Roe,December 3 2026"}},{"id":"mailings.finish"}],"save_as":"version"}`
	res, b = call(t, srv, "staff", "POST", path+"/editor-runs", strings.NewReader(run), "Content-Type", "application/json")
	if res.StatusCode != 201 || !strings.Contains(string(b), `"how":"editor_run"`) {
		t.Fatalf("run = %d %s", res.StatusCode, b)
	}
	res, b = call(t, srv, "staff", "POST", path+"/conversions", strings.NewReader(`{"format":"pdf"}`), "Content-Type", "application/json")
	if res.StatusCode != 201 || !strings.Contains(string(b), `"relationship":"conversion_of"`) {
		t.Fatalf("conversion = %d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, "staff", "POST", path+"/conversions", strings.NewReader(`{"format":"pdf","extra":1}`)); res.StatusCode != 400 {
		t.Fatalf("an unknown field = %d", res.StatusCode)
	}

	profile, _ := json.Marshal(map[string]any{"name": "Synthetic", "body": profileBody(t, "synthetic-county-brief")})
	res, b = call(t, srv, "staff", "POST", "/api/v1/court-profiles", bytes.NewReader(profile))
	var cp CourtProfile
	if err := json.Unmarshal(b, &cp); err != nil || res.StatusCode != 201 || cp.Verified {
		t.Fatalf("create profile = %d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, "staff", "POST", "/api/v1/court-profiles/"+string(cp.ID)+"/verify", nil); res.StatusCode != 403 {
		t.Fatalf("staff verify = %d", res.StatusCode)
	}
	if res, b := call(t, srv, "attorney", "POST", "/api/v1/court-profiles/"+string(cp.ID)+"/verify", nil); res.StatusCode != 200 || !strings.Contains(string(b), `"verified":true`) {
		t.Fatalf("attorney verify = %d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, "nobody", "GET", "/api/v1/court-profiles", nil); res.StatusCode != 401 {
		t.Fatalf("unauthenticated = %d", res.StatusCode)
	}
}

func TestPGEditPageOpensSavesAndShowsTheLock(t *testing.T) {
	e := newEnv(t)
	srv, _ := e.server(nil)
	original := e.doc(Command{ID: "text.insert", Parameters: map[string]any{"text": "Synthetic page test."}})
	f := e.create(e.staff, "Page test.docx", e.matter, original, false)
	page := "/files/" + string(f.ID) + "/edit"

	res, b := call(t, srv, "staff", "GET", page, nil)
	html := string(b)
	if res.StatusCode != 200 || !strings.Contains(html, `src="/editor/?host=parent&amp;author=Sam&#43;Staff"`) || !strings.Contains(html, `data-mode="edit"`) {
		t.Fatalf("edit page = %d\n%s", res.StatusCode, html)
	}
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("edit page CSP = %q", csp)
	}
	if !strings.Contains(html, "770a8fab12392485c0c2789fb254da7d9296df4d") || !strings.Contains(html, "/matters/"+string(e.matter)+"/pleadings/new") {
		t.Fatal("the edit page lacks the commit or New Pleading")
	}
	// The page sends the current version in, and saves what the editor hands back.
	res, b = call(t, srv, "staff", "GET", page+"/document", nil)
	if res.StatusCode != 200 || !bytes.Equal(b, original) || res.Header.Get("X-File-Name") != url.PathEscape("Page test.docx") {
		t.Fatalf("document = %d (%d bytes)", res.StatusCode, len(b))
	}
	edited, err := e.cli.Run(e.ctx, RunInput{Document: b, Ext: "docx", Commands: []Command{{ID: "caret.docEnd"}, {ID: "text.insert", Parameters: map[string]any{"text": " Saved from the page."}}}, Out: "docx"})
	if err != nil {
		t.Fatal(err)
	}
	res, b = call(t, srv, "staff", "POST", page+"/save", bytes.NewReader(edited), "X-File-Name", url.PathEscape("Page test.docx"))
	if res.StatusCode != 201 || !strings.Contains(string(b), `"number":2`) || !strings.Contains(string(b), `"how":"editor"`) {
		t.Fatalf("save = %d %s", res.StatusCode, b)
	}
	if res, b := call(t, srv, "staff", "GET", page+"/context", nil); res.StatusCode != 200 || !strings.Contains(string(b), "law.firm.set") {
		t.Fatalf("context = %d %s", res.StatusCode, b)
	}

	// A second user sees who is editing and since when, with preview and download.
	res, b = call(t, srv, "other", "GET", page, nil)
	html = string(b)
	if res.StatusCode != 200 || !strings.Contains(html, "<strong>Sam Staff</strong> is editing this file, since") || strings.Contains(html, "<iframe") || !strings.Contains(html, ">Download</a>") {
		t.Fatalf("locked page = %d\n%s", res.StatusCode, html)
	}
	if res, _ := call(t, srv, "other", "GET", page+"/document", nil); res.StatusCode != 409 {
		t.Fatalf("the second user got the document for editing: %d", res.StatusCode)
	}
	if res, _ := call(t, srv, "other", "POST", page+"/save", bytes.NewReader(edited), "X-File-Name", "x.docx"); res.StatusCode != 409 {
		t.Fatalf("the second user saved: %d", res.StatusCode)
	}
	// Client users get no editor; a format the editor does not open is read only.
	if res, _ := call(t, srv, "client", "GET", page, nil); res.StatusCode != 403 {
		t.Fatalf("client edit page = %d", res.StatusCode)
	}
	scan := e.create(e.staff, "Scan.pdf", e.matter, []byte("%PDF-1.4\n%%EOF\n"), false)
	res, b = call(t, srv, "staff", "GET", "/files/"+string(scan.ID)+"/edit", nil)
	if res.StatusCode != 200 || !strings.Contains(string(b), `data-mode="readonly"`) || strings.Contains(string(b), "<iframe") {
		t.Fatalf("pdf edit page = %d\n%s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, "nobody", "GET", page, nil); res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
		t.Fatalf("signed out = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
}

func TestPGNewPleadingFormMakesAFileAndOpensIt(t *testing.T) {
	e := newEnv(t)
	srv, _ := e.server(nil)
	form := url.Values{"type": {"motion"}, "title": {"Motion to Withdraw"}}.Encode()
	res, b := call(t, srv, "staff", "POST", "/matters/"+string(e.matter)+"/pleadings", strings.NewReader(form), "Content-Type", "application/x-www-form-urlencoded")
	if res.StatusCode != http.StatusSeeOther || !strings.HasPrefix(res.Header.Get("Location"), "/files/") || !strings.HasSuffix(res.Header.Get("Location"), "/edit") {
		t.Fatalf("pleading post = %d %s %s", res.StatusCode, res.Header.Get("Location"), b)
	}
	res, b = call(t, srv, "staff", "POST", "/matters/"+string(e.matter)+"/pleadings", strings.NewReader("type=motion&title="), "Content-Type", "application/x-www-form-urlencoded")
	if res.StatusCode != 400 || !strings.Contains(string(b), "title is required") {
		t.Fatalf("empty title = %d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, "staff", "GET", "/matters/"+string(e.matter)+"/pleadings/new", nil); res.StatusCode != 200 {
		t.Fatalf("pleading form = %d", res.StatusCode)
	}
	// The form follows the matter as the POST does: an outsider of a walled matter, a
	// matter that does not exist and a client user all get no form.
	for user, path := range map[string]string{"outsider": "/matters/" + string(e.walled), "staff": "/matters/01a1c38b-0000-7000-8000-000000000000", "client": "/matters/" + string(e.matter)} {
		res, b := call(t, srv, user, "GET", path+"/pleadings/new", nil)
		if want := map[string]int{"outsider": 404, "staff": 404, "client": 403}[user]; res.StatusCode != want || strings.Contains(string(b), `name="title"`) {
			t.Fatalf("%s pleading form for %s = %d, want %d", user, path, res.StatusCode, want)
		}
	}
}

const trunkIndex = `<!DOCTYPE html><html><head>
<script type="module">
import init from './togavsu-editor-abc.js';
await init({ module_or_path: './togavsu-editor-abc_bg.wasm' });
</script>
<style>html, body { margin: 0; }</style>
<link rel="modulepreload" href="./togavsu-editor-abc.js"></head><body><canvas id="wordcraft_canvas"></canvas></body></html>`

func TestEditorBuildIsServedWithAWasmPolicy(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":                 {Data: []byte(trunkIndex)},
		"togavsu-editor-abc_bg.wasm": {Data: []byte("\x00asm\x01\x00\x00\x00")},
		"togavsu-editor-abc.js":      {Data: []byte("export default function init() {}")},
	}
	h, err := NewHandler(&Service{}, editorpages.FS, dist, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()
	res, b := call(t, srv, "", "GET", "/editor/?host=parent&author=x", nil)
	csp := res.Header.Get("Content-Security-Policy")
	if res.StatusCode != 200 || !bytes.Equal(b, []byte(trunkIndex)) {
		t.Fatalf("/editor/ = %d", res.StatusCode)
	}
	script := "\nimport init from './togavsu-editor-abc.js';\nawait init({ module_or_path: './togavsu-editor-abc_bg.wasm' });\n"
	for _, want := range []string{"'wasm-unsafe-eval'", hash(script), hash("html, body { margin: 0; }"), "frame-ancestors 'self'", "object-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %s: %s", want, csp)
		}
	}
	if strings.Contains(csp, "'unsafe-eval'") || strings.Contains(csp, "'unsafe-inline'") {
		t.Fatalf("CSP is too broad: %s", csp)
	}
	res, _ = call(t, srv, "", "GET", "/editor/togavsu-editor-abc_bg.wasm", nil)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/wasm" || !strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("wasm = %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	for _, bad := range []string{"/editor/../index.html", "/editor/missing.js", "/editor/%2e%2e/x"} {
		if res, _ := call(t, srv, "", "GET", bad, nil); res.StatusCode == 200 {
			t.Errorf("%s was served", bad)
		}
	}
	for path, want := range map[string]bool{
		"GET /files/0193/edit": true, "GET /files/0193/edit/document": true, "POST /files/0193/edit/save": true,
		"POST /api/v1/files/0193/lock": true, "DELETE /api/v1/files/0193/lock": true, "POST /api/v1/files/0193/editor-runs": true,
		"GET /api/v1/court-profiles": true, "GET /editor/": true, "GET /editor/x_bg.wasm": true, "POST /matters/0193/pleadings": true,
		"GET /files/static/files.css": false, "GET /api/v1/files/0193": false, "GET /api/v1/files/0193/versions": false,
		"GET /practice/matters/0193/files": false, "GET /files/0193/editx": false,
	} {
		method, target, _ := strings.Cut(path, " ")
		if got := h.Owns(httptest.NewRequest(method, target, nil)); got != want {
			t.Errorf("Owns(%s) = %v", path, got)
		}
	}
	if res, b := call(t, srv, "", "GET", "/editor-host/edit.js", nil); res.StatusCode != 200 || !strings.Contains(string(b), "wordcraft:open") {
		t.Fatalf("edit.js = %d", res.StatusCode)
	}
	if res, _ := call(t, srv, "", "GET", "/editor-host/edit.html", nil); res.StatusCode != 404 {
		t.Fatalf("a template was served as an asset: %d", res.StatusCode)
	}
}

// The real trunk build (TOGAVSU_EDITOR_DIST, from web/editor/build.sh): every inline
// script and style it ships is allowed by hash, and the wasm is LawCraft's wrapper.
func TestEditorBuildAtThePinIsServable(t *testing.T) {
	distDir := os.Getenv("TOGAVSU_EDITOR_DIST")
	if distDir == "" {
		t.Skip("TOGAVSU_EDITOR_DIST is not set (build the browser editor with web/editor/build.sh)")
	}
	a, err := loadAssets(os.DirFS(distDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range inlineScript.FindAllSubmatch(a.index, -1) {
		if !bytes.Contains(m[1], []byte("src=")) && !strings.Contains(a.csp, hash(string(m[2]))) {
			t.Fatalf("an inline script of the build is not allowed by the CSP")
		}
	}
	if !bytes.Contains(a.index, []byte("togavsu-editor")) || !bytes.Contains(a.index, []byte("/editor/")) {
		t.Fatal("the build is not the Togavsu wrapper served at /editor/")
	}
	entries, err := os.ReadDir(distDir)
	if err != nil {
		t.Fatal(err)
	}
	var wasm bool
	for _, en := range entries {
		wasm = wasm || strings.HasSuffix(en.Name(), "_bg.wasm")
	}
	if !wasm {
		t.Fatal("the build has no WebAssembly module")
	}
}

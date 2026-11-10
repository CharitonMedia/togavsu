package doctender

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// AC-050 P1, P2, F1, F2 (phase 4 parts): the Markdown copy has the original's base
// name with .md, follows the original's wall, and is what Files search matches.
func TestAC050MarkdownNameWallAndSearch(t *testing.T) {
	e := newEnv(t)
	e.dir.mu.Lock()
	m := e.dir.matters[e.walled]
	m.ClientLast, m.Number = "Example", "2026-0002"
	e.dir.matters[e.walled] = m
	e.dir.mu.Unlock()
	body := "MOTION TO WITHDRAW\nDated 2026-10-15.\nThe phrase quietly-unique-marmalade appears only in this body.\n"
	f := e.upload(e.staff, e.walled, "Scan 77.txt", "text/plain", []byte(body))
	e.drain()
	got := e.get(e.attorney, f.ID)
	if got.Name != "example-2026-0002-motion-to-withdraw-2026-10-15.txt" {
		t.Fatalf("name %q", got.Name)
	}
	r, md := e.markdown(e.attorney, f.ID)
	if r.Name != "example-2026-0002-motion-to-withdraw-2026-10-15.md" || !strings.Contains(md, "quietly-unique-marmalade") {
		t.Fatalf("markdown %q", r.Name)
	}
	// Outside the wall: no list, no copy, no search hit, no Doc Tender result.
	if _, _, err := e.files.MarkdownCopy(e.ctx, e.outsider, f.ID); err == nil {
		t.Fatal("outsider read the Markdown copy")
	}
	if _, err := e.files.OpenRendition(e.ctx, e.outsider, f.ID, r.ID, false); err == nil {
		t.Fatal("outsider downloaded the Markdown copy")
	}
	if hits, err := e.files.Search(e.ctx, e.outsider, "marmalade", 8); err != nil || len(hits) != 0 {
		t.Fatalf("outsider search hits %d (%v)", len(hits), err)
	}
	if _, err := e.svc.FileResult(e.ctx, e.outsider, f.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("outsider read the result: %v", err)
	}
	if jobs, _, err := e.svc.Jobs(e.ctx, e.outsider, JobQuery{}); err != nil || len(jobs) != 0 {
		t.Fatalf("outsider sees %d jobs (%v)", len(jobs), err)
	}
	// On the wall: the search hit is this file, under its pattern name, from the Markdown.
	hits, err := e.files.Search(e.ctx, e.attorney, "marmalade", 8)
	if err != nil || len(hits) != 1 || hits[0].ID != f.ID || hits[0].Name != got.Name {
		t.Fatalf("search %+v %v", hits, err)
	}
	if hits, _ := e.files.Search(e.ctx, e.attorney, "Scan 77", 8); len(hits) != 0 {
		t.Fatalf("the arrival name matched as the file's name: %+v", hits)
	}
	// A rename carries the copy's name.
	if _, err := e.files.Rename(e.ctx, e.attorney, f.ID, "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.markdown(e.attorney, f.ID); r.Name != "renamed.md" {
		t.Fatalf("copy name after rename %q", r.Name)
	}
}

// AC-051 P1, P2, F1, F2: the pattern edited on the Doc Tender tab names the next file;
// a status token or synonym is refused and the saved pattern stays; a category added
// there is used for the next file and its folder.
func TestAC051PatternAndCategoryFromTheTab(t *testing.T) {
	e := newEnv(t)
	h := e.handler(t)
	p := "<clientlast>-<matter#>-<what-it-is>-<received-date>.<ext>"
	res := e.call(h, e.admin, "PUT", "/api/v1/doc-tender/naming", `{"pattern": "`+p+`"}`)
	if res.Code != http.StatusOK {
		t.Fatalf("save pattern: %d %s", res.Code, res.Body)
	}
	for _, bad := range []string{"<status>-<what-it-is>.<ext>", "<draft>-<what-it-is>.<ext>", "<file-status>-<what-it-is>.<ext>", "<received>-<what-it-is>.<ext>"} {
		res := e.call(h, e.admin, "PUT", "/api/v1/doc-tender/naming", `{"pattern": "`+bad+`"}`)
		if res.Code != http.StatusUnprocessableEntity && res.Code != http.StatusBadRequest {
			t.Fatalf("%s saved: %d %s", bad, res.Code, res.Body)
		}
		// The tab form refuses it too and shows why.
		form := e.form(h, e.admin, "/admin/doc-tender/naming", "pattern="+bad)
		if form.Code != http.StatusUnprocessableEntity || !strings.Contains(form.Body.String(), "The pattern was not saved") {
			t.Fatalf("tab saved %s: %d", bad, form.Code)
		}
	}
	if v, _ := e.reg.Get(e.ctx, "doctender.naming_pattern"); v != p {
		t.Fatalf("stored pattern %q", v)
	}
	if res := e.call(h, e.staff, "PUT", "/api/v1/doc-tender/naming", `{"pattern": "<what-it-is>.<ext>"}`); res.Code != http.StatusForbidden {
		t.Fatalf("staff saved the pattern: %d", res.Code)
	}
	// Add a category on the tab.
	form := e.form(h, e.admin, "/admin/doc-tender/categories", "key=synthetic_widget&description=Synthetic+widget+paperwork&tier=standard&folder_name=Widgets")
	if form.Code != http.StatusSeeOther {
		t.Fatalf("add category: %d %s", form.Code, form.Body)
	}
	e.ai.set(func(f *fakeAI) { f.category["WIDGET"] = map[string]string{"*": "synthetic_widget"} })
	f := e.upload(e.staff, e.open, "w.txt", "text/plain", []byte("WIDGET INVENTORY\nCounted 2026-09-01.\n"))
	e.drain()
	got := e.get(e.attorney, f.ID)
	if got.Name != "example-2026-0001-widget-inventory-2026-11-10.txt" {
		t.Fatalf("name %q", got.Name)
	}
	if got.CategoryID == nil || *got.CategoryID != "synthetic_widget" {
		t.Fatalf("category %v", got.CategoryID)
	}
	folders, _ := e.files.Folders(e.ctx, e.attorney, e.open)
	for _, fo := range folders {
		if fo.ID == *got.FolderID && fo.Name != "Widgets" {
			t.Fatalf("filed in %s", fo.Name)
		}
	}
	// Retiring keeps the category on the file.
	cats, _ := e.svc.Categories(e.ctx, e.admin)
	for _, c := range cats {
		if c.Key == "synthetic_widget" {
			off := false
			if _, err := e.svc.UpdateCategory(e.ctx, e.admin, c.ID, CategoryWrite{Active: &off}); err != nil {
				t.Fatal(err)
			}
			key := "renamed_widget"
			if _, err := e.svc.UpdateCategory(e.ctx, e.admin, c.ID, CategoryWrite{Key: &key}); !isCode(err, domain.CodeConflict) {
				t.Fatalf("renamed a category in use: %v", err)
			}
		}
	}
	if got := e.get(e.attorney, f.ID); got.CategoryID == nil || *got.CategoryID != "synthetic_widget" {
		t.Fatal("retiring removed the category from the file")
	}
}

// AC-052 P1, F1, F2: with an OpenAI test key set, the labeling call is logged with
// gpt-6.1-sol; labeling and categorizing never offer or run on a local endpoint.
func TestAC052LabelingModelAndNoLocalEndpoint(t *testing.T) {
	e := newEnv(t)
	f := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	rows, err := e.pool.QueryContext(e.ctx, `SELECT task, provider, model FROM ai_call_log WHERE file_id = $1 AND task IN ('labeling', 'categorizing') ORDER BY created_at`, string(f.ID))
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	for rows.Next() {
		var task, provider, model string
		if err := rows.Scan(&task, &provider, &model); err != nil {
			t.Fatal(err)
		}
		seen = append(seen, task+" "+provider+" "+model)
	}
	rows.Close()
	if len(seen) == 0 || seen[0] != "labeling openai "+domain.LabelingModel {
		t.Fatalf("call log %v", seen)
	}
	for _, task := range []string{TaskLabeling, TaskCategorizing} {
		for _, c := range ChoicesForTask(task) {
			if c == ProviderLocal {
				t.Fatalf("%s offers a local endpoint", task)
			}
		}
	}
	h := e.handler(t)
	res := e.call(h, e.admin, "GET", "/api/v1/doc-tender/model-choices", "")
	var choices map[string][]string
	_ = json.Unmarshal(res.Body.Bytes(), &choices)
	if strings.Contains(strings.Join(choices[TaskLabeling], ","), "local") || !strings.Contains(strings.Join(choices[TaskOCR], ","), "local") {
		t.Fatalf("choices %v", choices)
	}
	page := e.call(h, e.admin, "GET", "/admin/doc-tender", "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "never a local endpoint") || strings.Contains(page.Body.String(), "openai, anthropic, compatible, local") {
		t.Fatalf("tab: %d", page.Code)
	}
	// Even an override pointing labeling at a local endpoint is refused.
	e.set("ai.local_base_url", e.ai.srv.URL)
	e.set("ai.task_overrides", `{"labeling": {"provider": "local", "model": "llama-local"}}`)
	if _, err := e.router.Route(e.ctx, TaskLabeling, false); err != ErrLocalRefused {
		t.Fatalf("local labeling route: %v", err)
	}
	// Without an OpenAI key, labeling uses the primary provider's strong model.
	e.set("ai.task_overrides", `{}`)
	e.set("ai.openai_api_key", "")
	e.set("ai.anthropic_api_key", "test-anthropic-key")
	e.set("ai.primary_provider", "anthropic")
	if rt, err := e.router.Route(e.ctx, TaskLabeling, false); err != nil || rt != (Route{ProviderAnthropic, "gpt-test-strong"}) {
		t.Fatalf("route without an OpenAI key: %v %v", rt, err)
	}
}

// Before AI is set up, files are stored and listed and jobs hold; they start on their
// own once a key is set.
func TestHeldUntilAIIsSetUp(t *testing.T) {
	e := newEnv(t)
	e.set("ai.openai_api_key", "")
	f := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	if j := e.jobFor(f.ID); j.Status != StatusHeldAISetup {
		t.Fatalf("job %s", j.Status)
	}
	if e.ai.callCount("") != 0 {
		t.Fatal("called a provider with no key")
	}
	e.set("ai.openai_api_key", "test-openai-key")
	e.drain()
	if j := e.jobFor(f.ID); j.Status != StatusPublished {
		t.Fatalf("job %s %s", j.Status, j.LastError)
	}
}

// While a provider is down no attempts are spent; after an hour it is a review item,
// and when it is back the job finishes and the item clears.
func TestProviderDownSpendsNoAttempts(t *testing.T) {
	e := newEnv(t)
	e.ai.set(func(f *fakeAI) { f.down = true })
	f := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	j := e.jobFor(f.ID)
	if j.Status != StatusHeldProvider {
		t.Fatalf("job %s", j.Status)
	}
	for k := 0; k < 5; k++ {
		e.clock.advance(15 * minuteUnit)
		e.drain()
	}
	for _, u := range e.unitsFor(j.ID) {
		// Reading a text file calls no model; labeling and categorizing wait.
		if u.Name != "text" && u.Attempts != 0 {
			t.Fatalf("%s spent %d attempts while the provider was down", u.Name, u.Attempts)
		}
	}
	ex := e.openExceptions(f.ID)
	if len(ex) != 1 || ex[0].Kind != "provider_down" {
		t.Fatalf("exceptions %+v", ex)
	}
	e.ai.set(func(f *fakeAI) { f.down = false })
	e.clock.advance(15 * minuteUnit)
	e.drain()
	if j := e.jobFor(f.ID); j.Status != StatusPublished {
		t.Fatalf("job %s %s", j.Status, j.LastError)
	}
	if ex := e.openExceptions(f.ID); len(ex) != 0 {
		t.Fatalf("provider_down still open: %+v", ex)
	}
}

// A file whose content points to another matter keeps its matter and goes to review
// with the best match; one that arrives with no matter is not named until a person
// assigns the matter.
func TestMatterMatching(t *testing.T) {
	e := newEnv(t)
	other := e.upload(e.staff, e.open, "wrong matter.txt", "text/plain", []byte("NOTICE OF HEARING\nCase 24JE-DR01234 on 2026-12-01 for Morgan Testperson.\n"))
	loose, err := e.files.Create(e.ctx, e.staff, files.NewFile{Name: "loose.txt", MediaType: "text/plain", Source: "upload", ContactID: ""},
		strings.NewReader("LETTER ABOUT CASE 23AR-CV00825\nFrom Jordan Example.\n"), int64(len("LETTER ABOUT CASE 23AR-CV00825\nFrom Jordan Example.\n")))
	if err != nil {
		t.Skipf("files refuses a file with no matter or contact: %v", err)
	}
	e.drain()
	got := e.get(e.attorney, other.ID)
	if got.MatterID == nil || *got.MatterID != e.open {
		t.Fatal("Doc Tender moved a file between matters")
	}
	ex := e.openExceptions(other.ID)
	if len(ex) != 1 || ex[0].Kind != "unmatched_matter" || ex[0].BestAnswer["matter_id"] != string(e.other) {
		t.Fatalf("exceptions %+v", ex)
	}
	lex := e.openExceptions(loose.ID)
	if len(lex) != 1 || lex[0].BestAnswer["matter_id"] != string(e.open) {
		t.Fatalf("loose exceptions %+v", lex)
	}
	if g := e.get(e.attorney, loose.ID); g.Name != "loose.txt" {
		t.Fatalf("named before a matter was assigned: %q", g.Name)
	}
	// Assign matter: the matter is locked and the file is named and filed.
	m := e.open
	if _, err := e.svc.ResolveException(e.ctx, e.attorney, lex[0].ID, Resolve{Action: "assign_matter", MatterID: &m}); err != nil {
		t.Fatal(err)
	}
	e.drain()
	g := e.get(e.attorney, loose.ID)
	if g.MatterID == nil || *g.MatterID != e.open || !strings.HasPrefix(g.Name, "example-2026-0001-letter-about-case") {
		t.Fatalf("after assign: %q %v locked %v", g.Name, g.MatterID, g.LockedFields)
	}
}

// The review queue's actions: Accept applies and locks the best answer; Keep leaves the
// file as is with its flag; Retry requeues failed units only.
func TestReviewActions(t *testing.T) {
	e := newEnv(t)
	e.ai.set(func(f *fakeAI) {
		f.label["UNCLEAR"] = Label{WhatItIs: "meeting note", WhatItIsConfidence: 0.5, Summary: "A note."}
	})
	a := e.upload(e.staff, e.open, "a.txt", "text/plain", []byte("UNCLEAR ONE\n"))
	b := e.upload(e.staff, e.open, "b.pdf", "application/pdf", damagedPDF())
	e.drain()
	ea, eb := e.openExceptions(a.ID)[0], e.openExceptions(b.ID)[0]
	if _, err := e.svc.ResolveException(e.ctx, e.staff, ea.ID, Resolve{Action: "accept"}); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("staff resolved: %v", err)
	}
	if _, err := e.svc.ResolveException(e.ctx, e.attorney, ea.ID, Resolve{Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	got := e.get(e.attorney, a.ID)
	if got.Name != "example-2026-0001-meeting-note.txt" || !contains(got.LockedFields, "name") {
		t.Fatalf("accept: %q locked %v", got.Name, got.LockedFields)
	}
	if _, err := e.svc.ResolveException(e.ctx, e.attorney, eb.ID, Resolve{Action: "keep", Note: "client will resend"}); err != nil {
		t.Fatal(err)
	}
	st, _ := e.svc.Status(e.ctx, e.attorney, b.ID)
	if len(st.Flags) != 1 || st.Flags[0].Kind != "damaged" {
		t.Fatalf("kept file lost its flag: %+v", st)
	}
	if _, err := e.svc.ResolveException(e.ctx, e.attorney, eb.ID, Resolve{Action: "keep"}); !isCode(err, domain.CodeConflict) {
		t.Fatalf("resolved twice: %v", err)
	}
}

// The HTTP API and pages apply the same access as the service.
func TestHTTPRoutesAndPages(t *testing.T) {
	e := newEnv(t)
	h := e.handler(t)
	walled := e.upload(e.staff, e.walled, "w.pdf", "application/pdf", damagedPDF())
	e.drain()
	res := e.call(h, e.attorney, "GET", "/api/v1/doc-tender/exceptions?status=open", "")
	var page struct {
		Data []Exception    `json:"data"`
		Page map[string]any `json:"page"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil || res.Code != http.StatusOK || len(page.Data) != 1 {
		t.Fatalf("attorney queue %d %s", res.Code, res.Body)
	}
	if res := e.call(h, e.outsider, "GET", "/api/v1/doc-tender/exceptions?status=open", ""); !strings.Contains(res.Body.String(), `"data":[]`) {
		t.Fatalf("outsider queue %s", res.Body)
	}
	if res := e.call(h, e.outsider, "GET", "/api/v1/doc-tender/exceptions/"+string(page.Data[0].ID), ""); res.Code != http.StatusNotFound {
		t.Fatalf("outsider read the item: %d", res.Code)
	}
	if res := e.call(h, e.outsider, "GET", "/doc-tender/review/"+string(page.Data[0].ID), ""); res.Code != http.StatusNotFound {
		t.Fatalf("outsider opened the item page: %d", res.Code)
	}
	if res := e.call(h, e.staff, "GET", "/api/v1/doc-tender/exceptions", ""); res.Code != http.StatusForbidden {
		t.Fatalf("staff queue: %d", res.Code)
	}
	jobs := e.call(h, e.staff, "GET", "/api/v1/doc-tender/jobs?status=exception", "")
	if jobs.Code != http.StatusOK || !strings.Contains(jobs.Body.String(), `"status":"exception"`) {
		t.Fatalf("jobs %d %s", jobs.Code, jobs.Body)
	}
	if res := e.call(h, e.attorney, "GET", "/doc-tender/review", ""); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Damaged file") || !strings.Contains(res.Body.String(), `aria-label="1 open review items"`) {
		t.Fatalf("review page %d", res.Code)
	}
	if res := e.call(h, e.attorney, "GET", "/doc-tender/review/"+string(page.Data[0].ID), ""); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "Keep as is") {
		t.Fatalf("item page %d", res.Code)
	}
	if res := e.form(h, e.attorney, "/doc-tender/review/"+string(page.Data[0].ID), "action=keep"); res.Code != http.StatusSeeOther {
		t.Fatalf("resolve form %d %s", res.Code, res.Body)
	}
	if res := e.call(h, e.attorney, "POST", "/api/v1/doc-tender/exceptions/"+string(page.Data[0].ID)+"/resolve", `{"action":"bogus"}`); res.Code != http.StatusConflict && res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad action %d", res.Code)
	}
	if res := e.call(h, e.staff, "GET", "/api/v1/files/"+string(walled.ID)+"/doc-tender-status", ""); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "damaged") {
		t.Fatalf("status %d %s", res.Code, res.Body)
	}
	if res := e.call(h, e.outsider, "GET", "/api/v1/files/"+string(walled.ID)+"/doc-tender-status", ""); res.Code != http.StatusNotFound {
		t.Fatalf("outsider status %d", res.Code)
	}
	cats := e.call(h, e.staff, "GET", "/api/v1/doc-tender/categories", "")
	if cats.Code != http.StatusOK || !strings.Contains(cats.Body.String(), `"key":"court_filing"`) {
		t.Fatalf("categories %d", cats.Code)
	}
	if res := e.call(h, e.staff, "POST", "/api/v1/doc-tender/categories", `{"key":"x","description":"x","tier":"standard","folder_name":"X"}`); res.Code != http.StatusForbidden {
		t.Fatalf("staff added a category: %d", res.Code)
	}
	spend := e.call(h, e.admin, "GET", "/api/v1/reports/doc-tender-spend?format=csv", "")
	if spend.Code != http.StatusOK || !strings.HasPrefix(spend.Body.String(), "day,matter_id,file_type") {
		t.Fatalf("spend %d %s", spend.Code, spend.Body)
	}
	if res := e.call(h, e.attorney, "GET", "/doc-tender/guide", ""); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "How names are made") {
		t.Fatalf("guide %d", res.Code)
	}
}

func (e *env) handler(t *testing.T) *Handler {
	t.Helper()
	h, err := NewHandler(e.svc, e.reg, os.DirFS("../../web/templates/doctender"), func(r *http.Request) (domain.Principal, error) {
		switch r.Header.Get("X-Test-User") {
		case string(e.admin.UserID):
			return e.admin, nil
		case string(e.attorney.UserID):
			return e.attorney, nil
		case string(e.staff.UserID):
			return e.staff, nil
		case string(e.outsider.UserID):
			return e.outsider, nil
		}
		return domain.Principal{}, domain.Unauthenticated("no session")
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (e *env) call(h *Handler, p domain.Principal, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("X-Test-User", string(p.UserID))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)
	return rec
}

func (e *env) form(h *Handler, p domain.Principal, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("X-Test-User", string(p.UserID))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)
	return rec
}

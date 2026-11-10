package doctender

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// After the retry schedule is spent, the unit is read by its check method once more,
// then it goes to the review queue; the page is marked unresolved and the file stays
// available.
func TestRetriesThenCheckMethodThenException(t *testing.T) {
	e := newEnv(t)
	e.set("doctender.retry_schedule", `["0s","0s","0s","0s","0s","0s"]`)
	e.ai.set(func(f *fakeAI) { f.failOCR["PAGE 2 OF 3"] = 100 })
	f := e.upload(e.staff, e.open, "scan.pdf", "application/pdf", scannedPDF(t, 3))
	e.drain()
	units := e.unitsFor(e.jobFor(f.ID).ID)
	p2 := units[1]
	if p2.Status != unitPassed || p2.Attempts != 7 || !strings.HasPrefix(p2.Method, methodCheckFallback) {
		t.Fatalf("page 2: %s attempts %d method %s", p2.Status, p2.Attempts, p2.Method)
	}
	// With the check model failing too, the page is an exception.
	t.Run("check method fails too", func(t *testing.T) { checkMethodFails(t) })
}

func checkMethodFails(t *testing.T) {
	e2 := newEnv(t)
	e2.set("doctender.retry_schedule", `["0s","0s","0s","0s","0s","0s"]`)
	e2.set("doctender.max_attempts", "2")
	e2.ai.set(func(f *fakeAI) { f.failOCR["PAGE 2 OF 3"] = 100; f.down = false })
	e2.set("ai.primary_fast_model", "gpt-test-strong-check")
	g := e2.upload(e2.staff, e2.open, "scan.pdf", "application/pdf", scannedPDF(t, 3))
	e2.drain()
	units := e2.unitsFor(e2.jobFor(g.ID).ID)
	if units[1].Status != unitFailed || units[1].Attempts != 3 {
		t.Fatalf("page 2: %s after %d", units[1].Status, units[1].Attempts)
	}
	ex := e2.openExceptions(g.ID)
	if len(ex) != 1 || ex[0].Kind != "extraction_failed" || !strings.Contains(ex[0].Detail, "page 2 failed after 3 attempts") {
		t.Fatalf("exceptions %+v", ex)
	}
	_, md := e2.markdown(e2.attorney, g.ID)
	if !strings.Contains(md, "[unresolved: page 2]") || !strings.Contains(md, "PAGE 3 OF 3") {
		t.Fatalf("markdown:\n%s", md)
	}
	// Retry requeues only the failed page.
	e2.ai.set(func(f *fakeAI) { f.failOCR = map[string]int{} })
	if _, err := e2.svc.RetryJob(e2.ctx, e2.attorney, e2.jobFor(g.ID).ID); err != nil {
		t.Fatal(err)
	}
	e2.drain()
	units = e2.unitsFor(e2.jobFor(g.ID).ID)
	if units[0].Attempts != 1 || units[1].Status != unitPassed || units[1].Attempts != 1 {
		t.Fatalf("after retry: page 1 %d attempts, page 2 %s %d", units[0].Attempts, units[1].Status, units[1].Attempts)
	}
}

func xlsx(t *testing.T) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	add("xl/workbook.xml", `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Budget" sheetId="1" r:id="rId1"/></sheets></workbook>`)
	add("xl/_rels/workbook.xml.rels", `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`)
	add("xl/sharedStrings.xml", `<sst><si><t>Item</t></si><si><t>Amount</t></si><si><t>Rent</t></si></sst>`)
	add("xl/worksheets/sheet1.xml", `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row><row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>1250</v></c></row></sheetData></worksheet>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSpreadsheetsBecomeCheckedTables(t *testing.T) {
	e := newEnv(t)
	x := e.upload(e.staff, e.open, "budget.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", xlsx(t))
	c := e.upload(e.staff, e.open, "ledger.csv", "text/csv", []byte("Date,Payee,Amount\n2026-10-01,Landlord,1250\n2026-10-02,Utility,80\n"))
	e.drain()
	_, md := e.markdown(e.attorney, x.ID)
	if !strings.Contains(md, "## Budget") || !strings.Contains(md, "| Rent | 1250 |") {
		t.Fatalf("xlsx markdown:\n%s", md)
	}
	units := e.unitsFor(e.jobFor(c.ID).ID)
	if units[0].CheckResult != "rows 3/3 columns 3/3" {
		t.Fatalf("csv check %q", units[0].CheckResult)
	}
	if shape, _ := tableShape(markdownTable([][]string{{"a", "b"}, {"1", "2"}})); shape != 2 {
		t.Fatalf("table rows %d", shape)
	}
}

func TestWordDocumentsWaitForWordcraft(t *testing.T) {
	e := newEnv(t)
	e.set("doctender.retry_schedule", `["0s","0s","0s","0s","0s","0s"]`)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte("<w:document/>"))
	_ = zw.Close()
	f := e.upload(e.staff, e.open, "brief.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", buf.Bytes())
	e.drain()
	ex := e.openExceptions(f.ID)
	if len(ex) != 1 || ex[0].Kind != "extraction_failed" || !strings.Contains(ex[0].Detail, "wordcraft-cli is not available") {
		t.Fatalf("exceptions %+v", ex)
	}
	if _, err := e.files.Get(e.ctx, e.attorney, f.ID); err != nil {
		t.Fatal("the document is not listed")
	}
}

func TestRouteDefaults(t *testing.T) {
	reg, err := settings.New(settings.NewMemory(), quiet{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r := &Router{Settings: reg}
	if _, err := r.Route(ctx, TaskOCR, false); err != ErrNoAI {
		t.Fatalf("no keys: %v", err)
	}
	admin := newAdmin(t)
	set := func(k, v string) {
		if _, err := reg.Set(ctx, k, v, admin); err != nil {
			t.Fatal(err)
		}
	}
	set("ai.anthropic_api_key", "k")
	set("ai.primary_provider", "anthropic")
	set("ai.primary_strong_model", "claude-strong")
	set("ai.primary_fast_model", "claude-fast")
	check := func(task string, chk bool, want Route) {
		t.Helper()
		got, err := r.Route(ctx, task, chk)
		if err != nil || got != want {
			t.Fatalf("%s check=%v: %v %v, want %v", task, chk, got, err, want)
		}
	}
	check(TaskLabeling, false, Route{"anthropic", "claude-strong"})
	check(TaskLabeling, true, Route{"anthropic", "claude-fast"})
	set("ai.openai_api_key", "o")
	check(TaskLabeling, false, Route{"openai", "gpt-6.1-sol"})
	set("ai.secondary_provider", "openai")
	set("ai.secondary_strong_model", "gpt-check")
	check(TaskCategorizing, true, Route{"openai", "gpt-check"})
	check(TaskOCR, false, Route{"anthropic", "claude-strong"})
	if _, err := r.Route(ctx, TaskTranscription, true); err != ErrNoCheckModel {
		t.Fatalf("transcription check: %v", err)
	}
}

func TestRenderName(t *testing.T) {
	m := MatterInfo{Number: "2026-0042", Name: "Smith v. Smith", ClientLast: "Smith"}
	got, err := renderName("<clientlast>-<matter#>-<what-it-is>-<doc-date>.<ext>", nameParts{Matter: m, WhatItIs: "Motion to Withdraw", DocDate: "2026-10-15", Ext: "docx"})
	if err != nil || got != "smith-2026-0042-motion-to-withdraw-2026-10-15.docx" {
		t.Fatalf("%q %v", got, err)
	}
	got, _ = renderName("<clientlast>-<matter#>-<what-it-is>-<doc-date>.<ext>", nameParts{Matter: m, WhatItIs: "Letter", Ext: "pdf"})
	if got != "smith-2026-0042-letter.pdf" {
		t.Fatalf("no date: %q", got)
	}
	org := MatterInfo{Number: "2026-0003", ClientOrg: "Acme Holding Company of Missouri LLC"}
	got, _ = renderName("<clientlast>-<matter#>-<what-it-is>.<ext>", nameParts{Matter: org, WhatItIs: "Deed", Ext: "pdf"})
	if got != "acme-holding-company-2026-0003-deed.pdf" {
		t.Fatalf("organization: %q", got)
	}
	if _, err := renderName("<status>-<what-it-is>.<ext>", nameParts{}); err == nil {
		t.Fatal("a status token rendered")
	}
}

func TestSweepAndDailySummary(t *testing.T) {
	e := newEnv(t)
	f := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	// A version whose job went missing is found by the nightly sweep.
	if _, err := e.pool.ExecContext(e.ctx, `DELETE FROM doc_tender_jobs WHERE file_id = $1`, string(f.ID)); err != nil {
		t.Fatal(err)
	}
	n, err := e.svc.Sweep(e.ctx)
	if err != nil || n != 1 {
		t.Fatalf("sweep queued %d (%v)", n, err)
	}
	if j := e.jobFor(f.ID); j.Reason != reasonSweep {
		t.Fatalf("reason %s", j.Reason)
	}
	e.drain()
	e.upload(e.staff, e.open, "bad.pdf", "application/pdf", damagedPDF())
	e.drain()
	body, err := e.svc.DailySummary(e.ctx, e.clock.now())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Files processed since the last summary: 2", "Open review-queue items: 1", "Damaged file", "model calls"} {
		if !strings.Contains(body, want) {
			t.Fatalf("summary misses %q:\n%s", want, body)
		}
	}
	before := len(e.mail.all())
	e.clock.advance(16 * hourUnit) // past 07:00 the next morning (UTC firm time)
	if err := e.svc.dailyWork(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.dailyWork(e.ctx); err != nil {
		t.Fatal(err)
	}
	mail := e.mail.all()
	if len(mail) != before+1 || !strings.Contains(mail[len(mail)-1], "Doc Tender daily summary") {
		t.Fatalf("summary mail %d -> %d", before, len(mail))
	}
}

func TestSimilarityAndCitations(t *testing.T) {
	if s := similarity("PAGE 49 OF 120", "page 49 of 120"); s != 1 {
		t.Fatalf("case-insensitive similarity %v", s)
	}
	if s := similarity("a b c d e f g h i j", "a b c d e f g h i x"); s != 0.9 {
		t.Fatalf("one word in ten %v", s)
	}
	c := &content{Pages: map[int]string{1: "Case 23AR-CV00825 filed"}}
	page := 1
	if p := checkLabel(Label{Fields: []CitedField{{Name: "case_number", Value: "23AR-CV00825", Citation: Citation{Page: &page}}}}, c); len(p) != 0 {
		t.Fatalf("a true citation failed: %v", p)
	}
	other := 2
	if p := checkLabel(Label{Fields: []CitedField{{Name: "case_number", Value: "23AR-CV00825", Citation: Citation{Page: &other}}}}, c); len(p) != 1 {
		t.Fatal("a wrong page citation passed")
	}
	if p := checkLabel(Label{DocDate: "2026-13-45"}, c); len(p) != 1 {
		t.Fatal("an impossible date passed")
	}
}

func newAdmin(t *testing.T) domain.Principal {
	return domain.Principal{UserID: id(t), Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
}

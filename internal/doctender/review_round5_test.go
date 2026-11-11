package doctender

import (
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// Opus checkpoint P1: Doc Tender sets a file's name, category and folder once (PRD
// 8.21). A new version and a reprocess label the file again but leave an unlocked name
// and category as the first result published them.
func TestDocTenderNamesAFileOnce(t *testing.T) {
	e := newEnv(t)
	f := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	first := e.get(e.attorney, f.ID)
	if first.Name == "motion.txt" || first.CategoryID == nil {
		t.Fatalf("first pass did not name and file it: name %q category %v", first.Name, first.CategoryID)
	}
	e.ai.set(func(fa *fakeAI) {
		fa.label["MOTION TO WITHDRAW"] = Label{WhatItIs: "something else entirely", WhatItIsConfidence: 0.99, DocDate: "2026-01-01", DocDateConfidence: 0.99, Summary: "Different."}
		fa.category["MOTION TO WITHDRAW"] = map[string]string{"*": "reference"}
	})
	body := motion + "Second version from the editor.\n"
	if _, err := e.files.SaveVersion(e.ctx, e.staff, f.ID, strings.NewReader(body), int64(len(body)), files.SaveOptions{How: "editor"}); err != nil {
		t.Fatal(err)
	}
	e.drain()
	if _, err := e.svc.Reprocess(e.ctx, e.attorney, f.ID); err != nil {
		t.Fatal(err)
	}
	e.drain()
	got := e.get(e.attorney, f.ID)
	if got.Name != first.Name {
		t.Fatalf("Doc Tender renamed %q to %q", first.Name, got.Name)
	}
	if got.CategoryID == nil || *got.CategoryID != *first.CategoryID {
		t.Fatalf("Doc Tender re-filed %v as %v", *first.CategoryID, got.CategoryID)
	}
	var results, named int
	if err := e.pool.QueryRowContext(e.ctx, `SELECT count(*), COALESCE(SUM(CASE WHEN name_set THEN 1 ELSE 0 END), 0) FROM doc_tender_results WHERE file_id = $1`,
		string(f.ID)).Scan(&results, &named); err != nil {
		t.Fatal(err)
	}
	if results != 3 || named != 1 {
		t.Fatalf("results %d named %d, want 3 results and the name set by one", results, named)
	}
}

// Opus checkpoint P2: Retry on a grouping disagreement is refused, as Accept is, since
// the photo's job waits on its group and a retry would leave it unpublished.
func TestRetryOnAGroupingDisagreementIsRefused(t *testing.T) {
	e := newEnv(t)
	e.ai.set(func(f *fakeAI) { f.splitGroups = map[string]bool{"gpt-test-fast": true} })
	photos := uploadPhotos(e)
	e.drain()
	for k := 0; k < 12; k++ {
		e.clock.advance(24 * 60 * minuteUnit)
		e.drain()
	}
	items := e.openExceptions(photos[0].ID)
	if len(items) != 1 || !items[0].Grouping() {
		t.Fatalf("items %+v, want one grouping disagreement", items)
	}
	if _, err := e.svc.ResolveException(e.ctx, e.attorney, items[0].ID, Resolve{Action: "retry"}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("Retry on a grouping disagreement: %v, want refused", err)
	}
	if left := e.openExceptions(photos[0].ID); len(left) != 1 || left[0].ID != items[0].ID {
		t.Fatalf("the grouping item did not stay open: %+v", left)
	}
}

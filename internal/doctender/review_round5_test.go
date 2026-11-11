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

// Opus checkpoint P2: a file whose arrival name already is the pattern name still counts
// as named by Doc Tender, so a later reprocess leaves that name alone.
func TestDocTenderNameAlreadyInPlaceCountsAsSet(t *testing.T) {
	e := newEnv(t)
	const want = "example-2026-0001-motion-to-withdraw-2026-10-15.txt"
	f := e.upload(e.staff, e.open, want, "text/plain", []byte(motion))
	e.drain()
	if got := e.get(e.attorney, f.ID); got.Name != want {
		t.Fatalf("setup: name %q, want the pattern name %q", got.Name, want)
	}
	e.ai.set(func(fa *fakeAI) {
		fa.label["MOTION TO WITHDRAW"] = Label{WhatItIs: "something else entirely", WhatItIsConfidence: 0.99, DocDate: "2026-01-01", DocDateConfidence: 0.99, Summary: "Different."}
	})
	if _, err := e.svc.Reprocess(e.ctx, e.attorney, f.ID); err != nil {
		t.Fatal(err)
	}
	e.drain()
	if got := e.get(e.attorney, f.ID); got.Name != want {
		t.Fatalf("Doc Tender renamed %q to %q", want, got.Name)
	}
}

// Opus checkpoint P2: Accept on a category item never renames a file Doc Tender already
// named, whether the name took a clash suffix or a later label proposed another one.
func TestAcceptOnACategoryItemKeepsThePublishedName(t *testing.T) {
	e := newEnv(t)
	e.ai.set(func(f *fakeAI) {
		f.category["MOTION TO WITHDRAW"] = map[string]string{"gpt-6.1-sol": "court_filing", "gpt-test-fast": "correspondence"}
	})
	first := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	second := e.upload(e.staff, e.open, "motion again.txt", "text/plain", []byte(motion))
	e.drain()
	a, b := e.get(e.attorney, first.ID), e.get(e.attorney, second.ID)
	if a.Name == b.Name || !strings.HasSuffix(b.Name, "-2.txt") {
		t.Fatalf("setup: names %q and %q, want a -2 suffix on the second", a.Name, b.Name)
	}
	accept := func(f files.FileView) {
		t.Helper()
		items := e.openExceptions(f.ID)
		if len(items) != 1 || items[0].Kind != "check_disagreement" {
			t.Fatalf("%s: items %+v, want one category disagreement", f.Name, items)
		}
		if _, ok := items[0].BestAnswer["name"]; ok {
			t.Fatalf("%s: the category item offers a name: %+v", f.Name, items[0].BestAnswer)
		}
		if _, err := e.svc.ResolveException(e.ctx, e.attorney, items[0].ID, Resolve{Action: "accept"}); err != nil {
			t.Fatal(err)
		}
	}
	// Case 1: the clash-suffixed file keeps its own name.
	accept(b)
	if got := e.get(e.attorney, second.ID); got.Name != b.Name {
		t.Fatalf("Accept renamed %q to %q (the first file is %q)", b.Name, got.Name, a.Name)
	}
	// Case 2: the first file's item is kept as is; a reprocess with another label opens a
	// new category item, and Accept keeps the name Doc Tender published first.
	for _, it := range e.openExceptions(first.ID) {
		if _, err := e.svc.ResolveException(e.ctx, e.attorney, it.ID, Resolve{Action: "keep"}); err != nil {
			t.Fatal(err)
		}
	}
	e.ai.set(func(fa *fakeAI) {
		fa.label["MOTION TO WITHDRAW"] = Label{WhatItIs: "something else entirely", WhatItIsConfidence: 0.99, DocDate: "2026-01-01", DocDateConfidence: 0.99, Summary: "Different."}
	})
	if _, err := e.svc.Reprocess(e.ctx, e.attorney, first.ID); err != nil {
		t.Fatal(err)
	}
	e.drain()
	accept(a)
	if got := e.get(e.attorney, first.ID); got.Name != a.Name {
		t.Fatalf("Accept renamed %q to %q after a reprocess", a.Name, got.Name)
	}
}

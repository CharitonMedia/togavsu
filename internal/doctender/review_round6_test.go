package doctender

import (
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/files"
)

// lowName makes the label for the motion fall below the review threshold, so the name
// is held for a person while the category may still be published.
func lowName(fa *fakeAI) {
	fa.label["MOTION TO WITHDRAW"] = Label{WhatItIs: "motion to withdraw", WhatItIsConfidence: 0.3, DocDate: "2026-10-15", DocDateConfidence: 0.99, Summary: "A motion."}
}

// Opus checkpoint P2: Accept on a name item never changes a category Doc Tender already
// published, even when a later version proposes a disputed one.
func TestAcceptOnANameItemLeavesASettledCategory(t *testing.T) {
	e := newEnv(t)
	e.ai.set(lowName)
	f := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	first := e.get(e.attorney, f.ID)
	if first.CategoryID == nil || first.Name != "motion.txt" {
		t.Fatalf("setup: name %q category %v, want the category filed and the name held", first.Name, first.CategoryID)
	}
	for _, it := range e.openExceptions(f.ID) {
		if _, err := e.svc.ResolveException(e.ctx, e.attorney, it.ID, Resolve{Action: "keep"}); err != nil {
			t.Fatal(err)
		}
	}
	e.ai.set(func(fa *fakeAI) {
		fa.category["MOTION TO WITHDRAW"] = map[string]string{"gpt-6.1-sol": "discovery", "gpt-test-fast": "correspondence"}
	})
	body := motion + "Second version from the editor.\n"
	if _, err := e.files.SaveVersion(e.ctx, e.staff, f.ID, strings.NewReader(body), int64(len(body)), files.SaveOptions{How: "editor"}); err != nil {
		t.Fatal(err)
	}
	e.drain()
	items := e.openExceptions(f.ID)
	if len(items) != 1 || items[0].Kind != "low_confidence" {
		t.Fatalf("items %+v, want one low-confidence name item and no category item", items)
	}
	if _, ok := items[0].BestAnswer["category"]; ok {
		t.Fatalf("the name item offers a category: %+v", items[0].BestAnswer)
	}
	if _, ok := items[0].BestAnswer["name"]; !ok {
		t.Fatalf("the name item offers no name: %+v", items[0].BestAnswer)
	}
	if _, err := e.svc.ResolveException(e.ctx, e.attorney, items[0].ID, Resolve{Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	got := e.get(e.attorney, f.ID)
	if got.CategoryID == nil || *got.CategoryID != *first.CategoryID {
		t.Fatalf("Accept on the name item re-filed %v as %v", *first.CategoryID, got.CategoryID)
	}
	if got.Name == "motion.txt" {
		t.Fatalf("Accept on the name item did not set the name")
	}
}

// Opus checkpoint P2: within one pass, Accept on the name item does not apply a disputed
// category; the category item stays open for its own decision.
func TestNameItemDoesNotApplyADisputedCategory(t *testing.T) {
	e := newEnv(t)
	e.ai.set(func(fa *fakeAI) {
		lowName(fa)
		fa.category["MOTION TO WITHDRAW"] = map[string]string{"gpt-6.1-sol": "court_filing", "gpt-test-fast": "correspondence"}
	})
	f := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	var name, disagree *Exception
	items := e.openExceptions(f.ID)
	for k := range items {
		switch items[k].Kind {
		case "low_confidence":
			name = &items[k]
		case "check_disagreement":
			disagree = &items[k]
		}
	}
	if len(items) != 2 || name == nil || disagree == nil {
		t.Fatalf("items %+v, want a name item and a category disagreement", items)
	}
	if _, ok := name.BestAnswer["category"]; ok {
		t.Fatalf("the name item offers the disputed category: %+v", name.BestAnswer)
	}
	if _, err := e.svc.ResolveException(e.ctx, e.attorney, name.ID, Resolve{Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	if got := e.get(e.attorney, f.ID); got.CategoryID != nil {
		t.Fatalf("Accept on the name item filed the disputed category %v", *got.CategoryID)
	}
	left := e.openExceptions(f.ID)
	if len(left) != 1 || left[0].ID != disagree.ID {
		t.Fatalf("open items %+v, want the category disagreement still open", left)
	}
	if _, err := e.svc.ResolveException(e.ctx, e.attorney, disagree.ID, Resolve{Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	if got := e.get(e.attorney, f.ID); got.CategoryID == nil {
		t.Fatal("Accept on the category item did not file it")
	}
}

// A name and a category both held for low confidence share the version's one
// low-confidence item, which names both and applies both on Accept.
func TestLowConfidenceNameAndCategoryShareOneItem(t *testing.T) {
	e := newEnv(t)
	e.ai.set(func(fa *fakeAI) {
		lowName(fa)
		fa.categoryConfidence = 0.3
	})
	f := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	items := e.openExceptions(f.ID)
	if len(items) != 1 || items[0].Kind != "low_confidence" {
		t.Fatalf("items %+v, want one low-confidence item", items)
	}
	it := items[0]
	if !strings.Contains(it.Detail, "name") || !strings.Contains(it.Detail, "category") {
		t.Fatalf("detail %q, want both the name and the category", it.Detail)
	}
	if _, ok := it.BestAnswer["name"]; !ok {
		t.Fatalf("no name offered: %+v", it.BestAnswer)
	}
	if _, ok := it.BestAnswer["category"]; !ok {
		t.Fatalf("no category offered: %+v", it.BestAnswer)
	}
	if _, err := e.svc.ResolveException(e.ctx, e.attorney, it.ID, Resolve{Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	got := e.get(e.attorney, f.ID)
	if got.Name == "motion.txt" || got.CategoryID == nil {
		t.Fatalf("Accept left name %q category %v, want both set", got.Name, got.CategoryID)
	}
}

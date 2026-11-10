package doctender

import (
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

const motion = "MOTION TO WITHDRAW\nFiled 2026-10-15 in case 23AR-CV00825.\nCounsel for Jordan Example asks leave to withdraw.\n"

// AC-043 (P1, F1, F2): staff upload, client upload and a drop-folder style arrival are
// each queued at arrival (no nightly batch), processed, and come out with the pattern
// name as their only name, a category, a summary, a folder and a saved Markdown copy.
func TestAC043ArrivalsAreProcessedAtOnce(t *testing.T) {
	e := newEnv(t)
	sources := []struct{ name, source string }{{"Scan_0001 final(2).txt", "upload"}, {"from client.txt", "client_upload"}, {"dropped.txt", "drop_folder"}}
	for k, src := range sources {
		body := strings.Replace(motion, "2026-10-15", []string{"2026-10-15", "2026-10-16", "2026-10-17"}[k], 1)
		f := e.uploadFrom(e.staff, e.open, src.name, "text/plain", src.source, []byte(body))
		// F1: the job is queued at arrival, before any sweep.
		j := e.jobFor(f.ID)
		if j.Status != StatusQueued || j.Reason != reasonArrival {
			t.Fatalf("%s: job %s/%s at arrival", src.source, j.Status, j.Reason)
		}
		arrived := e.clock.now()
		e.drain()
		j = e.jobFor(f.ID)
		if j.Status != StatusPublished {
			t.Fatalf("%s: job %s (%s)", src.source, j.Status, j.LastError)
		}
		if took := j.FinishedAt.Sub(arrived); took > 10*minuteUnit {
			t.Fatalf("%s took %v", src.source, took)
		}
		got := e.get(e.attorney, f.ID)
		want := "example-2026-0001-motion-to-withdraw-" + []string{"2026-10-15", "2026-10-16", "2026-10-17"}[k] + ".txt"
		if got.Name != want {
			t.Fatalf("%s: name %q, want %q", src.source, got.Name, want)
		}
		// F2: the arrival name is kept for audit and never shown as the name.
		if got.ArrivedAs != src.name || got.Name == src.name {
			t.Fatalf("arrival name %q / name %q", got.ArrivedAs, got.Name)
		}
		if got.CategoryID == nil || *got.CategoryID != "court_filing" {
			t.Fatalf("category %v", got.CategoryID)
		}
		if got.FolderID == nil {
			t.Fatal("not filed into a folder")
		}
		folders, err := e.files.Folders(e.ctx, e.attorney, e.open)
		if err != nil {
			t.Fatal(err)
		}
		inPleadings := false
		for _, fo := range folders {
			if fo.ID == *got.FolderID && fo.Name == "Pleadings" {
				inPleadings = true
			}
		}
		if !inPleadings {
			t.Fatalf("filed in %v, want Pleadings", *got.FolderID)
		}
		res, err := e.svc.FileResult(e.ctx, e.attorney, f.ID)
		if err != nil {
			t.Fatal(err)
		}
		if res.Summary == "" || res.NameParts.WhatItIs != "motion to withdraw" || res.CategorySource != "model_agreement" {
			t.Fatalf("result %+v", res)
		}
		if len(res.Fields) != 1 || res.Fields[0].Value != "23AR-CV00825" || *res.Fields[0].Citation.Page != 1 {
			t.Fatalf("cited fields %+v", res.Fields)
		}
		r, md := e.markdown(e.attorney, f.ID)
		if r.Name != strings.TrimSuffix(want, ".txt")+".md" || !strings.Contains(md, "Counsel for Jordan Example") || !strings.Contains(md, "source: "+want) {
			t.Fatalf("markdown %q:\n%s", r.Name, md)
		}
	}
	if n, err := e.svc.Sweep(e.ctx); err != nil || n != 0 {
		t.Fatalf("the nightly sweep found %d unprocessed files (%v)", n, err)
	}
}

// AC-043-F3: a high-tier category is published only when the check model agrees.
func TestAC043HighTierNeedsAgreement(t *testing.T) {
	e := newEnv(t)
	e.ai.set(func(f *fakeAI) {
		f.category["MOTION TO WITHDRAW"] = map[string]string{"gpt-6.1-sol": "court_filing", "gpt-test-fast": "correspondence"}
	})
	f := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	got := e.get(e.attorney, f.ID)
	if got.CategoryID != nil {
		t.Fatalf("court_filing published on disagreement: %v", *got.CategoryID)
	}
	ex := e.openExceptions(f.ID)
	if len(ex) != 1 || ex[0].Kind != "check_disagreement" || ex[0].BestAnswer["category"] != "court_filing" {
		t.Fatalf("exceptions %+v", ex)
	}
	if j := e.jobFor(f.ID); j.Status != StatusException {
		t.Fatalf("job %s", j.Status)
	}
	// The name passed its own checks and is still published.
	if !strings.HasPrefix(got.Name, "example-2026-0001-motion-to-withdraw") {
		t.Fatalf("name %q", got.Name)
	}
	_ = domain.ID("")
}

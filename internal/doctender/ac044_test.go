package doctender

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func pageNumber(u Unit) int {
	var n int
	fmt.Sscanf(u.Name, "page %d", &n)
	return n
}

// AC-044 P1 and F1/F2 with a provider failure: 120 image-only pages are read page by
// page, each OCR read checked by a second reader; the OCR model fails page 49 (twice,
// so the retry schedule's 15-minute wait applies) and the job resumes at page 49.
// Pages 1 through 48 are never read again.
func TestAC044ScannedPDFResumesAtPage49(t *testing.T) {
	e := newEnv(t)
	e.ai.set(func(f *fakeAI) { f.failOCR["PAGE 49 OF 120"] = 2 })
	f := e.upload(e.staff, e.open, "exhibit-scan.pdf", "application/pdf", scannedPDF(t, 120))
	e.drain()
	j := e.jobFor(f.ID)
	units := e.unitsFor(j.ID)
	var pages []Unit
	for _, u := range units {
		if strings.HasPrefix(u.Name, "page ") {
			pages = append(pages, u)
		}
	}
	if len(pages) != 120 {
		t.Fatalf("%d page units, want 120", len(pages))
	}
	for _, u := range pages[:48] {
		if u.Status != unitPassed || u.Attempts != 1 {
			t.Fatalf("%s: %s after %d attempts", u.Name, u.Status, u.Attempts)
		}
	}
	p49 := pages[48]
	if p49.Status != unitPending || p49.Attempts != 2 || !strings.Contains(p49.LastError, "injected OCR failure") {
		t.Fatalf("page 49: %s attempts %d %q", p49.Status, p49.Attempts, p49.LastError)
	}
	if wait := p49.NextAttempt.Sub(p49.FinishedAt); wait < 15*minuteUnit-secondUnit || wait > 15*minuteUnit+secondUnit {
		t.Fatalf("page 49 waits %v, want the schedule's 15 minutes", wait)
	}
	for _, u := range pages[49:] {
		if u.Attempts != 0 {
			t.Fatalf("%s started before page 49 passed", u.Name)
		}
	}
	if j.Status != StatusQueued {
		t.Fatalf("job %s", j.Status)
	}
	// Before the wait is over nothing runs.
	e.drain()
	if got := e.unitsFor(j.ID)[48]; got.Attempts != 2 {
		t.Fatalf("page 49 retried early (%d attempts)", got.Attempts)
	}
	var firstAfter []string
	e.ai.set(func(f *fakeAI) {
		f.onOCR = func(text string) {
			if len(firstAfter) < 1 {
				firstAfter = append(firstAfter, text)
			}
		}
	})
	e.clock.advance(15 * minuteUnit)
	e.drain()
	if len(firstAfter) == 0 || !strings.Contains(firstAfter[0], "PAGE 49 OF 120") {
		t.Fatalf("the resumed job read %q first, want page 49", firstAfter)
	}
	j = e.jobFor(f.ID)
	if j.Status != StatusPublished {
		t.Fatalf("job %s: %s", j.Status, j.LastError)
	}
	units = e.unitsFor(j.ID)
	for k, u := range units[:120] {
		want := 1
		if k == 48 {
			want = 3
		}
		if u.Status != unitPassed || u.Attempts != want {
			t.Fatalf("%s: %s after %d attempts, want %d", u.Name, u.Status, u.Attempts, want)
		}
		if !strings.Contains(u.Text, fmt.Sprintf("PAGE %d OF 120", k+1)) {
			t.Fatalf("%s reads %q", u.Name, u.Text)
		}
		if u.Method != "ocr" || u.CheckMethod != e.deps.Second.Name() || !strings.HasPrefix(u.CheckResult, "agreement ") {
			t.Fatalf("%s: method %s check %s %s", u.Name, u.Method, u.CheckMethod, u.CheckResult)
		}
		// Each unit is stored before the next page starts.
		if k > 0 && !units[k-1].FinishedAt.Before(u.StartedAt) && k != 48 {
			t.Fatalf("%s started before %s was stored", u.Name, units[k-1].Name)
		}
	}
	_, md := e.markdown(e.attorney, f.ID)
	if !strings.Contains(md, "## Page 49\n\nEXHIBIT SCAN\nPAGE 49 OF 120") || !strings.Contains(md, "pages: 120") || strings.Contains(md, "unresolved") {
		t.Fatalf("markdown:\n%s", md[:400])
	}
}

// AC-044-F1/F2 with a stopped worker: the worker dies while page 49 is being read.
// After its lease lapses another worker resumes at page 49, not page 1.
func TestAC044StoppedWorkerResumesAtPage49(t *testing.T) {
	e := newEnv(t)
	f := e.upload(e.staff, e.open, "exhibit-scan.pdf", "application/pdf", scannedPDF(t, 120))
	ctx, stop := context.WithCancel(e.ctx)
	e.ai.set(func(fa *fakeAI) {
		fa.onOCR = func(text string) {
			if strings.Contains(text, "PAGE 49 OF 120") {
				stop()
			}
		}
	})
	_, _ = e.svc.RunOnce(ctx)
	j := e.jobFor(f.ID)
	units := e.unitsFor(j.ID)
	if units[47].Status != unitPassed || units[48].Status == unitPassed {
		t.Fatalf("stop point: page 48 %s, page 49 %s", units[47].Status, units[48].Status)
	}
	if j.LeaseOwner != "worker-1" {
		t.Fatalf("lease owner %q", j.LeaseOwner)
	}
	var reads []string
	e.ai.set(func(fa *fakeAI) { fa.onOCR = func(text string) { reads = append(reads, text) } })
	second := e.worker("worker-2")
	// While the lease holds, the other worker can't take the job.
	if ran, err := second.RunOnce(e.ctx); err != nil || ran {
		t.Fatalf("second worker took a leased job: %v %v", ran, err)
	}
	e.clock.advance(16 * minuteUnit)
	if err := second.Drain(e.ctx); err != nil {
		t.Fatal(err)
	}
	if len(reads) != 72 || !strings.Contains(reads[0], "PAGE 49 OF 120") {
		t.Fatalf("resume read %d pages starting %q", len(reads), reads[0])
	}
	for _, u := range e.unitsFor(j.ID)[:48] {
		if u.Attempts != 1 {
			t.Fatalf("%s was redone (%d attempts)", u.Name, u.Attempts)
		}
	}
	if j := e.jobFor(f.ID); j.Status != StatusPublished {
		t.Fatalf("job %s %s", j.Status, j.LastError)
	}
}

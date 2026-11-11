package doctender

import (
	"bytes"
	"image/color"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/doctender/dtcorpus"
)

// A file an admin empties from the trash before Doc Tender reaches it leaves a queued
// job with nothing to read. The worker must not take it (or fail on it, which stopped
// Drain and retried forever), and must not wait on it; a live file queued after it is
// still processed.
func TestPurgedFileJobIsNeverClaimed(t *testing.T) {
	e := newEnv(t)
	gone := e.upload(e.staff, e.open, "to-be-purged.txt", "text/plain", []byte("NOTICE OF HEARING\nCase 23AR-CV00825\n"))
	if _, err := e.files.Trash(e.ctx, e.staff, gone.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.files.Purge(e.ctx, e.admin, gone.ID, gone.Name); err != nil {
		t.Fatal(err)
	}
	live := e.upload(e.staff, e.open, "live.txt", "text/plain", []byte("NOTICE OF HEARING\nCase 23AR-CV00825\n"))
	e.drain()
	if j := e.jobFor(gone.ID); j.Status != StatusQueued || j.Attempts != 0 || j.LeaseOwner != "" {
		t.Fatalf("the purged file's job ran: %+v", j)
	}
	if j := e.jobFor(live.ID); j.Status != StatusPublished {
		t.Fatalf("the live file after it: %s %s", j.Status, j.LastError)
	}
	if ran, err := e.svc.RunOnce(e.ctx); err != nil || ran {
		t.Fatalf("a pass after the purge ran a job: %v %v", ran, err)
	}
	wait, err := e.svc.nextWait(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lease, _ := e.svc.duration(e.ctx, "doctender.lease_timeout"); wait != lease {
		t.Fatalf("the worker waits %v for a purged file's job, want the idle %v", wait, lease)
	}
}

// The core's phase cases build their corpus with dtcorpus; it must read the way this
// package's own corpus does.
func TestDTCorpusMatchesTheReaders(t *testing.T) {
	doc, err := parsePDF(dtcorpus.ScannedPDF(3))
	if err != nil || doc.pageCount() != 3 {
		t.Fatalf("scanned PDF: %v", err)
	}
	img, _, ok := doc.pageImage(1)
	if !ok || readGlyphs(img) != "EXHIBIT SCAN\nPAGE 2 OF 3\nPLAINTIFF EXHIBIT FOR HEARING\nSYNTHETIC RECORD ONLY" {
		t.Fatalf("page 2 image reads %q", readGlyphs(img))
	}
	tp, err := parsePDF(dtcorpus.TextPDF([]string{"NOTICE OF HEARING", "Case 23AR-CV00825"}, []string{"PAGE TWO"}))
	if err != nil || tp.pageCount() != 2 {
		t.Fatalf("text PDF: %v", err)
	}
	if text, _ := tp.pageText(0); !bytes.Contains([]byte(text), []byte("23AR-CV00825")) {
		t.Fatalf("text layer %q", text)
	}
	if _, err := parsePDF(dtcorpus.EncryptedPDF("pw")); err != ErrEncrypted {
		t.Fatalf("encrypted PDF: %v", err)
	}
	if _, err := parsePDF(dtcorpus.DamagedPDF()); err == nil {
		t.Fatal("the damaged PDF opened")
	}
	if d, err := wavDuration(dtcorpus.ToneWAV(dtcorpus.On(2))); err != nil || d != 2*secondUnit {
		t.Fatalf("tone: %v %v", d, err)
	}
	v, err := readAVI(dtcorpus.FramesAVI([]color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}}))
	if err != nil || v.audio == nil || v.total != 3*secondUnit {
		t.Fatalf("AVI: %v", err)
	}
	for k, p := range dtcorpus.LetterLeasePhotos() {
		if got := dtcorpus.ReadGlyphs(p); got != readGlyphs(p) {
			t.Fatalf("photo %d: dtcorpus reads %q, this package %q", k+1, got, readGlyphs(p))
		}
	}
}

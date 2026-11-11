package doctender

import (
	"fmt"
	"image/color"
	"io"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// uploadPhotos uploads the eight photos in a shuffled order and returns them by page
// (letter 1-3, lease 1-5).
func uploadPhotos(e *env) []files.FileView {
	photos := letterLeasePhotos()
	byPage := make([]files.FileView, len(photos))
	for _, k := range []int{4, 1, 7, 0, 3, 6, 2, 5} {
		byPage[k] = e.upload(e.staff, e.open, fmt.Sprintf("IMG_%04d.png", 100+k), "image/png", photos[k])
	}
	return byPage
}

func pdfPagesRead(t *testing.T, e *env, f files.FileView) []string {
	t.Helper()
	dl, err := e.files.OpenVersion(e.ctx, e.attorney, f.ID, *f.CurrentVersionID, false)
	if err != nil {
		t.Fatal(err)
	}
	defer dl.Close()
	raw, err := io.ReadAll(dl.Body)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parsePDF(raw)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for k := 0; k < doc.pageCount(); k++ {
		img, _, ok := doc.pageImage(k)
		if !ok {
			t.Fatalf("page %d has no image", k+1)
		}
		lines := strings.Split(readGlyphs(img), "\n")
		out = append(out, lines[0]+" "+lines[1])
	}
	return out
}

// AC-045 P1 and F1-F3: eight photos of a three-page letter and a five-page lease,
// uploaded together, become two PDFs and two Markdown copies with pages in order.
func TestAC045PhotosBecomeTwoDocumentsInOrder(t *testing.T) {
	e := newEnv(t)
	photos := uploadPhotos(e)
	e.drain()
	// Photos are read, then wait for the matter's quiet time.
	for _, p := range photos {
		if j := e.jobFor(p.ID); j.Stage != stageAwaitingGroup {
			t.Fatalf("photo job at %s/%s before quiet time", j.Stage, j.Status)
		}
	}
	if e.ai.callCount("grouping") != 0 {
		t.Fatal("grouped before the quiet time")
	}
	e.clock.advance(10 * minuteUnit)
	e.drain()
	list, _, err := e.files.List(e.ctx, e.attorney, files.ListQuery{MatterID: e.open})
	if err != nil {
		t.Fatal(err)
	}
	var pdfs []files.FileView
	for _, f := range list {
		if f.Extension == "pdf" {
			pdfs = append(pdfs, f)
		}
	}
	if len(pdfs) != 2 {
		t.Fatalf("%d PDFs, want 2 (not one merged, not a 4/4 split)", len(pdfs))
	}
	want := map[string][]string{
		"letter": {"LETTER PAGE 1 OF 3", "LETTER PAGE 2 OF 3", "LETTER PAGE 3 OF 3"},
		"lease":  {"LEASE PAGE 1 OF 5", "LEASE PAGE 2 OF 5", "LEASE PAGE 3 OF 5", "LEASE PAGE 4 OF 5", "LEASE PAGE 5 OF 5"},
	}
	for _, pdf := range pdfs {
		pages := pdfPagesRead(t, e, pdf)
		kind := strings.ToLower(strings.Fields(pages[0])[0])
		if strings.Join(pages, "|") != strings.Join(want[kind], "|") {
			t.Fatalf("%s pages %v", pdf.Name, pages)
		}
		if !strings.HasPrefix(pdf.Name, "example-2026-0001-"+kind) {
			t.Fatalf("grouped PDF name %q", pdf.Name)
		}
		r, md := e.markdown(e.attorney, pdf.ID)
		if r.Name != strings.TrimSuffix(pdf.Name, ".pdf")+".md" {
			t.Fatalf("markdown name %q", r.Name)
		}
		for k, line := range want[kind] {
			parts := strings.SplitN(line, " ", 2)
			if !strings.Contains(md, fmt.Sprintf("## Page %d\n\n%s\n%s", k+1, parts[0], parts[1])) {
				t.Fatalf("markdown page %d missing:\n%s", k+1, md)
			}
		}
		// The photos stay linked as its sources, in page order.
		links, err := e.files.Links(e.ctx, e.attorney, pdf.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(links) != len(want[kind]) {
			t.Fatalf("%d source links", len(links))
		}
		for _, l := range links {
			if l.Relationship != "grouped_photo_source" {
				t.Fatalf("link %+v", l)
			}
		}
	}
	// The photos themselves stay listed and downloadable, without their own copies.
	for _, p := range photos {
		if j := e.jobFor(p.ID); j.Status != StatusPublished || j.Stage != stageGrouped {
			t.Fatalf("photo job %s/%s", j.Status, j.Stage)
		}
		if _, err := e.files.OpenVersion(e.ctx, e.attorney, p.ID, *p.CurrentVersionID, false); err != nil {
			t.Fatalf("photo not downloadable: %v", err)
		}
	}
	// Each grouping was decided once, by two agreeing models.
	if n := e.ai.callCount("grouping"); n != 2 {
		t.Fatalf("%d grouping calls, want 2 (model and check)", n)
	}
	e.clock.advance(10 * minuteUnit)
	e.drain()
	if n := e.ai.callCount("grouping"); n != 2 {
		t.Fatalf("grouping redone: %d calls", n)
	}
}

// A new photo restarts the quiet time.
func TestAC045QuietTimeWaitsForTheLastPhoto(t *testing.T) {
	e := newEnv(t)
	photos := letterLeasePhotos()
	e.upload(e.staff, e.open, "a.png", "image/png", photos[0])
	e.drain()
	e.clock.advance(9 * minuteUnit)
	e.upload(e.staff, e.open, "b.png", "image/png", photos[1])
	e.drain()
	e.clock.advance(2 * minuteUnit)
	e.drain()
	if e.ai.callCount("grouping") != 0 {
		t.Fatal("grouped inside the quiet time of the second photo")
	}
	e.clock.advance(9 * minuteUnit)
	e.drain()
	if e.ai.callCount("grouping") != 2 {
		t.Fatalf("grouping calls %d", e.ai.callCount("grouping"))
	}
}

// AC-046 P1: an audio file's Markdown transcript has timestamps covering the recording.
func TestAC046AudioTranscriptCoversTheRecording(t *testing.T) {
	e := newEnv(t)
	e.set("ai.task_overrides", `{"transcription": {"check_provider": "openai", "check_model": "whisper-check"}}`)
	short := e.upload(e.staff, e.open, "tone.wav", "audio/wav", toneWAV(onFor(1)))
	// A longer recording with a 20-second pause: under the 30-second rule.
	pattern := onFor(90)
	for s := 30; s < 50; s++ {
		pattern[s] = false
	}
	long := e.upload(e.staff, e.open, "call.wav", "audio/wav", toneWAV(pattern))
	e.drain()
	for _, f := range []files.FileView{short, long} {
		j := e.jobFor(f.ID)
		if j.Status != StatusPublished {
			t.Fatalf("%s: %s %s %+v", f.Name, j.Status, j.LastError, e.openExceptions(f.ID))
		}
		_, md := e.markdown(e.attorney, f.ID)
		if !strings.Contains(md, "## Transcript") || !strings.Contains(md, "- **00:00:00** Said: Speaker 1: steady tone") {
			t.Fatalf("%s transcript:\n%s", f.Name, md)
		}
	}
	_, md := e.markdown(e.attorney, long.ID)
	if !strings.Contains(md, "- **00:00:50** Said:") {
		t.Fatalf("long transcript misses the second part:\n%s", md)
	}
	units := e.unitsFor(e.jobFor(long.ID).ID)
	if !strings.Contains(units[0].CheckMethod, "second_transcription") || !strings.Contains(units[0].CheckResult, "covered 00:01:30 of 00:01:30") {
		t.Fatalf("check %s %s", units[0].CheckMethod, units[0].CheckResult)
	}
}

// AC-046 F1: a silent gap over 30 seconds that the transcript doesn't explain fails
// the coverage check, and the file is not published as correct.
func TestAC046UnexplainedGapFailsTheCheck(t *testing.T) {
	e := newEnv(t)
	e.set("doctender.retry_schedule", `["0s","0s","0s","0s","0s","0s"]`)
	pattern := onFor(100)
	for s := 30; s < 70; s++ {
		pattern[s] = false
	}
	f := e.upload(e.staff, e.open, "gap.wav", "audio/wav", toneWAV(pattern))
	e.drain()
	units := e.unitsFor(e.jobFor(f.ID).ID)
	if units[0].Status != unitFailed || !strings.Contains(units[0].LastError, "unexplained gap 00:00:30 to 00:01:10") {
		t.Fatalf("transcript unit %s %q", units[0].Status, units[0].LastError)
	}
	ex := e.openExceptions(f.ID)
	if len(ex) == 0 || ex[0].Kind != "extraction_failed" {
		t.Fatalf("exceptions %+v", ex)
	}
	_, md := e.markdown(e.attorney, f.ID)
	if !strings.Contains(md, "[unresolved: transcript]") {
		t.Fatalf("markdown does not mark the transcript unresolved:\n%s", md)
	}
	if j := e.jobFor(f.ID); j.Status != StatusException {
		t.Fatalf("job %s", j.Status)
	}
	// A segment that says why it is quiet explains the gap.
	if ok, _ := coverage(transcript{Duration: 100, Segments: []Segment{{0, 30, "", "talk"}, {30, 70, "", "[silence]"}, {70, 100, "", "talk"}}}, 30*secondUnit); !ok {
		t.Fatal("an explained gap failed")
	}
}

// AC-046 P2 and F2: the video's report puts the transcript and what's on screen on one
// timeline.
func TestAC046VideoReportAddsWhatIsOnScreen(t *testing.T) {
	e := newEnv(t)
	f := e.upload(e.staff, e.open, "frames.avi", "video/x-msvideo", framesAVI([]color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}}))
	e.drain()
	if j := e.jobFor(f.ID); j.Status != StatusPublished {
		t.Fatalf("job %s %s %+v", j.Status, j.LastError, e.openExceptions(f.ID))
	}
	_, md := e.markdown(e.attorney, f.ID)
	for _, want := range []string{"## Timeline: transcript and what's on screen", "- **00:00:00** Said: Speaker 1: steady tone",
		"- **00:00:00** On screen: a solid red frame", "- **00:00:01** On screen: a solid green frame", "- **00:00:02** On screen: a solid blue frame"} {
		if !strings.Contains(md, want) {
			t.Fatalf("report misses %q:\n%s", want, md)
		}
	}
	// A Zoom recording gets the transcript only.
	z := e.uploadFrom(e.staff, e.open, "zoom.avi", "video/x-msvideo", "zoom", framesAVI([]color.RGBA{{255, 0, 0, 255}}))
	e.drain()
	_, zmd := e.markdown(e.attorney, z.ID)
	if strings.Contains(zmd, "On screen") || !strings.Contains(zmd, "Said:") {
		t.Fatalf("zoom report:\n%s", zmd)
	}
	_ = domain.ID("")
}

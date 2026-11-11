package doctender

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// lowCaps lowers the decode caps for one test.
func lowCaps(t *testing.T, decoded, pixels int64) {
	t.Helper()
	old := decodeCaps
	decodeCaps = func() (int64, int64) { return decoded, pixels }
	t.Cleanup(func() { decodeCaps = old })
}

// Opus checkpoint P1: the worker sleeps until claim or groupDue has work. A job held
// for AI setup while no AI is set up, a photo waiting for its group's quiet time and a
// job whose lease is held give no reason to wake now.
func TestNextWaitIgnoresWorkClaimWillNotTake(t *testing.T) {
	e := newEnv(t)
	lease, err := e.svc.duration(e.ctx, "doctender.lease_timeout")
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := e.svc.duration(e.ctx, "doctender.photo_quiet")
	if err != nil {
		t.Fatal(err)
	}
	wait := func() time.Duration {
		t.Helper()
		d, err := e.svc.nextWait(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	e.set("ai.openai_api_key", "")
	held := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	if j := e.jobFor(held.ID); j.Status != StatusHeldAISetup {
		t.Fatalf("job %s, want held for AI setup", j.Status)
	}
	if d := wait(); d != lease {
		t.Fatalf("with only a job held for AI setup the worker waits %v, want one lease period %v", d, lease)
	}
	e.set("ai.openai_api_key", "test-openai-key")
	e.drain()

	photos := uploadPhotos(e)
	e.drain()
	if d := wait(); d <= zeroDuration || d > quiet {
		t.Fatalf("with photos in their quiet time the worker waits %v, want more than 0 and at most %v", d, quiet)
	}
	e.clock.advance(quiet)
	if d := wait(); d != zeroDuration {
		t.Fatalf("once the quiet time is over the worker waits %v, want 0", d)
	}
	e.drain()

	// A due job whose lease another worker holds is due when that lease lapses.
	f := e.upload(e.staff, e.open, "note.txt", "text/plain", []byte("LETTER TO THE CLIENT\nSent 2026-10-08.\n"))
	until := e.clock.now().Add(lease / 3)
	if _, err := e.svc.exec(e.ctx, `UPDATE doc_tender_jobs SET lease_owner = 'other', lease_until = ? WHERE file_id = ?`, us(until), f.ID); err != nil {
		t.Fatal(err)
	}
	// The test clock moves a little on every read, so allow a minute's slack.
	if d := wait(); d > lease/3 || d < lease/3-minuteUnit {
		t.Fatalf("with a leased job the worker waits %v, want the %v left on its lease", d, lease/3)
	}
	_ = photos
}

// Opus checkpoint P2: a route the AI tab can't build (here labeling sent to a local
// endpoint) holds the job on the retry schedule and puts the file in the review queue
// with the reason; once the setting is fixed the file finishes and the item closes.
func TestAISetupErrorReachesTheReviewQueue(t *testing.T) {
	e := newEnv(t)
	e.set("ai.local_base_url", e.ai.srv.URL)
	e.set("ai.task_overrides", `{"labeling": {"provider": "local", "model": "llama-local"}}`)
	f := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	j := e.jobFor(f.ID)
	if j.Status != StatusHeldAISetup || !j.NextAttempt.After(e.clock.now()) || j.LeaseOwner != "" {
		t.Fatalf("job %s next %v lease %q, want held for AI setup on the retry schedule with no lease", j.Status, j.NextAttempt, j.LeaseOwner)
	}
	items := e.openExceptions(f.ID)
	if len(items) != 1 || items[0].Kind != "provider_down" || !strings.Contains(items[0].Detail, "local endpoint") {
		t.Fatalf("review items %+v, want one naming the local endpoint", items)
	}
	e.set("ai.task_overrides", `{}`)
	e.clock.advance(e.svc.waitDelay(e.ctx))
	e.drain()
	if j := e.jobFor(f.ID); j.Status != StatusPublished {
		t.Fatalf("job %s %s after the setting was fixed, want published", j.Status, j.LastError)
	}
	if items := e.openExceptions(f.ID); len(items) != 0 {
		t.Fatalf("review items left open: %+v", items)
	}
}

// Opus checkpoint P1: decompression and pixel bombs are damaged files, not memory.
func TestDecodeCapsRefuseBombs(t *testing.T) {
	lowCaps(t, 1024, 100)
	if _, err := readCapped(bytes.NewReader(make([]byte, 2048))); !errors.Is(err, errDecodeLimit) {
		t.Fatalf("2 KiB through a 1 KiB cap: %v", err)
	}
	if got, err := readCapped(bytes.NewReader(make([]byte, 1024))); err != nil || len(got) != 1024 {
		t.Fatalf("1 KiB at the cap: %d bytes, %v", len(got), err)
	}
	var d pdfDoc
	_, err := d.decode(&pdfStream{Dict: pdfDict{"Filter": pdfName("FlateDecode")}, Raw: zlibBytes(make([]byte, 64*1024))})
	if !errors.Is(err, errDecodeLimit) {
		t.Fatalf("flate stream past the cap: %v", err)
	}
	photo := renderPage([]string{"A PHOTO PAST THE PIXEL CAP"})
	if err := checkPixels(photo); !errors.Is(err, errDecodeLimit) {
		t.Fatalf("photo past the pixel cap: %v", err)
	}
	if _, err := imageAsPNG(photo); !errors.Is(err, errDecodeLimit) {
		t.Fatalf("imageAsPNG decoded a photo past the cap: %v", err)
	}
}

func TestPixelBombPhotoIsDamaged(t *testing.T) {
	e := newEnv(t)
	lowCaps(t, 1<<20, 100)
	f := e.upload(e.staff, e.open, "IMG_9000.png", "image/png", renderPage([]string{"A PHOTO PAST THE PIXEL CAP"}))
	e.drain()
	e.clock.advance(24 * 60 * minuteUnit)
	e.drain()
	items := e.openExceptions(f.ID)
	if len(items) != 1 || items[0].Kind != "damaged" || !strings.Contains(items[0].Detail, "decode limit") {
		t.Fatalf("review items %+v, want one damaged item naming the decode limit", items)
	}
	if e.ai.callCount("") != 0 {
		t.Fatal("a model was called for a photo past the pixel cap")
	}
}

// Opus checkpoint P2: Accept on a grouping disagreement is refused (there is no single
// answer), and a person's grouping closes the photos' items.
func TestGroupingDisagreementNeedsAPersonsGrouping(t *testing.T) {
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
	if _, err := e.svc.ResolveException(e.ctx, e.attorney, items[0].ID, Resolve{Action: "accept"}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("Accept on a grouping disagreement: %v, want refused", err)
	}
	var all []domain.ID
	for _, p := range photos {
		all = append(all, p.ID)
	}
	spec := strings.Join(idStrings(all[:3]), ",") + ";" + strings.Join(idStrings(all[3:]), ",")
	if _, err := e.svc.ResolveException(e.ctx, e.attorney, items[0].ID, Resolve{Action: "correct", Grouping: spec}); err != nil {
		t.Fatal(err)
	}
	e.drain()
	for _, p := range photos {
		if left := e.openExceptions(p.ID); len(left) != 0 {
			t.Fatalf("%s keeps open items after a person grouped it: %+v", p.Name, left)
		}
	}
}

// Opus checkpoint P2: review mail and the daily summary describe an attorney-only file
// by kind only, as they do a walled one, since the recipients may be any address.
func TestReviewMailRedactsAttorneyOnlyFiles(t *testing.T) {
	e := newEnv(t)
	e.set("doctender.review_recipients", "admins, outside@example.test")
	body := []byte("SETTLEMENT POSITION\xff\xfe\n")
	f, err := e.files.Create(e.ctx, e.attorney, files.NewFile{Name: "Plea offer draft.txt", MatterID: e.open, MediaType: "text/plain",
		Source: "upload", AttorneyOnly: true}, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	e.drain()
	if items := e.openExceptions(f.ID); len(items) != 1 {
		t.Fatalf("items %+v, want the damaged-text item", items)
	}
	e.clock.advance(24 * 60 * minuteUnit)
	if err := e.svc.FlushExceptionMail(e.ctx); err != nil {
		t.Fatal(err)
	}
	summary, err := e.svc.DailySummary(e.ctx, e.clock.now())
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Join(e.mail.all(), "\n") + "\n" + summary
	if !strings.Contains(out, "an attorney-only file") {
		t.Fatalf("mail and summary do not mention the item by kind:\n%s", out)
	}
	for _, leak := range []string{"Plea offer", "not valid UTF-8"} {
		if strings.Contains(out, leak) {
			t.Fatalf("mail or summary shows %q for an attorney-only file:\n%s", leak, out)
		}
	}
}

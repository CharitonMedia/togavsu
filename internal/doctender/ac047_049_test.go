package doctender

import (
	"io"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// AC-047 P1, P2, F1, F2: a file on a closed matter makes no model call and is listed as
// Not processed (matter closed); reopening the matter processes it.
func TestAC047ClosedMatterSpendsNothingUntilReopened(t *testing.T) {
	e := newEnv(t)
	before := e.aiCalls("1 = 1")
	f := e.upload(e.staff, e.closed, "late letter.txt", "text/plain", []byte("LETTER TO THE CLIENT\nSent 2026-11-01.\n"))
	e.drain()
	if got := e.aiCalls("1 = 1"); got != before {
		t.Fatalf("closed matter made %d model calls", got-before)
	}
	if e.ai.callCount("") != 0 {
		t.Fatal("the provider was called")
	}
	st, err := e.svc.Status(e.ctx, e.attorney, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != StatusMatterClosed || st.Label != "Not processed (matter closed)" {
		t.Fatalf("status %+v", st)
	}
	// It is stored and listed.
	if got := e.get(e.attorney, f.ID); got.Name != "late letter.txt" {
		t.Fatalf("listed as %q", got.Name)
	}
	// Still nothing on later passes while closed.
	e.clock.advance(hourUnit)
	e.drain()
	if e.aiCalls("1 = 1") != before {
		t.Fatal("a later pass spent tokens on the closed matter")
	}
	e.dir.setStatus(e.closed, "open")
	if err := e.svc.MatterReopened(e.ctx, e.closed); err != nil {
		t.Fatal(err)
	}
	e.drain()
	if e.aiCalls("file_id = ?", string(f.ID)) == 0 {
		t.Fatal("reopening made no model call")
	}
	if j := e.jobFor(f.ID); j.Status != StatusPublished {
		t.Fatalf("after reopen: %s %s", j.Status, j.LastError)
	}
	if got := e.get(e.attorney, f.ID); !strings.HasPrefix(got.Name, "archive-2025-0099-letter-to-the-client") {
		t.Fatalf("name after reopen %q", got.Name)
	}
}

// The worker also notices a reopened matter by itself.
func TestAC047ReopenNoticedWithoutAHook(t *testing.T) {
	e := newEnv(t)
	f := e.upload(e.staff, e.closed, "note.txt", "text/plain", []byte("LETTER ABOUT THE ESTATE\n"))
	e.drain()
	e.dir.setStatus(e.closed, "open")
	e.drain()
	if j := e.jobFor(f.ID); j.Status != StatusPublished {
		t.Fatalf("job %s", j.Status)
	}
}

func download(t *testing.T, e *env, p domain.Principal, f files.FileView) []byte {
	t.Helper()
	dl, err := e.files.OpenVersion(e.ctx, p, f.ID, *f.CurrentVersionID, false)
	if err != nil {
		t.Fatalf("download %s: %v", f.Name, err)
	}
	defer dl.Close()
	b, err := io.ReadAll(dl.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// AC-048 P1 and F1: a password-protected PDF, a damaged file and a low-confidence label
// each land in the review queue, email the admin, and stay listed, viewable and
// downloadable with a flag naming the failure.
func TestAC048ExceptionsStayAvailableWithAFlag(t *testing.T) {
	e := newEnv(t)
	e.ai.set(func(f *fakeAI) {
		f.label["UNCLEAR SCRAWL"] = Label{WhatItIs: "handwritten note", WhatItIsConfidence: 0.89, Summary: "A note."}
	})
	locked := encryptedPDF("open-sesame")
	damaged := damagedPDF()
	low := []byte("UNCLEAR SCRAWL\nsomething about a meeting\n")
	fs := []files.FileView{
		e.upload(e.staff, e.open, "password.pdf", "application/pdf", locked),
		e.upload(e.staff, e.open, "damaged.pdf", "application/pdf", damaged),
		e.upload(e.staff, e.open, "note.txt", "text/plain", low),
	}
	before := e.ai.callCount("")
	e.drain()
	kinds := []string{"password_protected", "damaged", "low_confidence"}
	bodies := [][]byte{locked, damaged, low}
	for k, f := range fs {
		ex := e.openExceptions(f.ID)
		if len(ex) != 1 || ex[0].Kind != kinds[k] {
			t.Fatalf("%s: exceptions %+v", f.Name, ex)
		}
		// Listed, viewable and downloadable as an attorney.
		list, _, err := e.files.List(e.ctx, e.attorney, files.ListQuery{MatterID: e.open})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, l := range list {
			found = found || l.ID == f.ID
		}
		if !found {
			t.Fatalf("%s is not listed", f.Name)
		}
		if got := download(t, e, e.attorney, e.get(e.attorney, f.ID)); string(got) != string(bodies[k]) {
			t.Fatalf("%s download differs", f.Name)
		}
		st, err := e.svc.Status(e.ctx, e.attorney, f.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Flags) != 1 || st.Flags[0].Kind != kinds[k] || st.Flags[0].Label == "" {
			t.Fatalf("%s flags %+v", f.Name, st.Flags)
		}
		items, _, err := e.svc.Exceptions(e.ctx, e.attorney, ExceptionQuery{Status: "open", Kind: kinds[k]})
		if err != nil || len(items) != 1 || items[0].FileID != f.ID {
			t.Fatalf("queue for %s: %+v %v", kinds[k], items, err)
		}
	}
	// The password PDF and damaged file made no model call.
	if got := e.aiCalls("file_id IN (?, ?)", string(fs[0].ID), string(fs[1].ID)); got != 0 {
		t.Fatalf("%d model calls on unreadable files", got)
	}
	_ = before
	// The low-confidence name was not published.
	if got := e.get(e.attorney, fs[2].ID); got.Name != "note.txt" {
		t.Fatalf("low-confidence name published: %q", got.Name)
	}
	// The first exception emails the admins at once; the rest are bundled into at most
	// one email every 15 minutes.
	mail := e.mail.all()
	if len(mail) != 1 || !strings.HasPrefix(mail[0], "admins|Doc Tender: 1 item(s) to review") || !strings.Contains(mail[0], "Password-protected") {
		t.Fatalf("first mail %q", mail)
	}
	e.clock.advance(14 * minuteUnit)
	if err := e.svc.FlushExceptionMail(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(e.mail.all()); n != 1 {
		t.Fatalf("%d emails inside the bundling window", n)
	}
	e.clock.advance(minuteUnit)
	if err := e.svc.FlushExceptionMail(e.ctx); err != nil {
		t.Fatal(err)
	}
	mail = e.mail.all()
	if len(mail) != 2 || !strings.Contains(mail[1], "2 item(s)") || !strings.Contains(mail[1], "Damaged file") || !strings.Contains(mail[1], "Low confidence") {
		t.Fatalf("bundled mail %q", mail)
	}
	if e.notes.count != 3 {
		t.Fatalf("%d notifications", e.notes.count)
	}
	// Staff don't get the review queue.
	if _, _, err := e.svc.Exceptions(e.ctx, e.staff, ExceptionQuery{}); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("staff read the queue: %v", err)
	}
}

// AC-048-F2: confidence 0.900 is not queued for low confidence; 0.899 is.
func TestAC048ThresholdBoundary(t *testing.T) {
	e := newEnv(t)
	e.ai.set(func(f *fakeAI) {
		f.label["AT THE LINE"] = Label{WhatItIs: "boundary memo", WhatItIsConfidence: 0.900, Summary: "At the line."}
		f.label["UNDER THE LINE"] = Label{WhatItIs: "boundary memo", WhatItIsConfidence: 0.899, Summary: "Under the line."}
	})
	at := e.upload(e.staff, e.open, "at.txt", "text/plain", []byte("AT THE LINE\n"))
	under := e.upload(e.staff, e.open, "under.txt", "text/plain", []byte("UNDER THE LINE\n"))
	e.drain()
	if ex := e.openExceptions(at.ID); len(ex) != 0 {
		t.Fatalf("0.900 queued: %+v", ex)
	}
	if ex := e.openExceptions(under.ID); len(ex) != 1 || ex[0].Kind != "low_confidence" {
		t.Fatalf("0.899 not queued: %+v", ex)
	}
}

// AC-049 P1, P2, F1, F2: a name, category and matter a person set survive a reprocess
// whose model output proposes different values; the reprocess adds a Markdown version
// and the old version's bytes are unchanged.
func TestAC049PersonSetFieldsSurviveReprocess(t *testing.T) {
	e := newEnv(t)
	f := e.upload(e.staff, e.open, "motion.txt", "text/plain", []byte(motion))
	e.drain()
	first, firstMD := e.markdown(e.attorney, f.ID)
	if _, err := e.files.Rename(e.ctx, e.attorney, f.ID, "my-own-name.txt"); err != nil {
		t.Fatal(err)
	}
	cat := "firm_work_product"
	if _, err := e.files.UpdateMetadata(e.ctx, e.attorney, f.ID, files.Metadata{CategoryID: &cat}); err != nil {
		t.Fatal(err)
	}
	moved := e.walled
	if _, err := e.files.UpdateMetadata(e.ctx, e.attorney, f.ID, files.Metadata{MatterID: &moved}); err != nil {
		t.Fatal(err)
	}
	e.ai.set(func(fa *fakeAI) {
		fa.label["MOTION TO WITHDRAW"] = Label{WhatItIs: "something else entirely", WhatItIsConfidence: 0.99, DocDate: "2026-01-01", DocDateConfidence: 0.99, Summary: "Different."}
		fa.category["MOTION TO WITHDRAW"] = map[string]string{"*": "reference"}
	})
	if _, err := e.svc.Reprocess(e.ctx, e.attorney, f.ID); err != nil {
		t.Fatal(err)
	}
	e.drain()
	got := e.get(e.attorney, f.ID)
	if got.Name != "my-own-name.txt" || got.CategoryID == nil || *got.CategoryID != cat || got.MatterID == nil || *got.MatterID != moved {
		t.Fatalf("a locked field changed: name %q category %v matter %v", got.Name, got.CategoryID, got.MatterID)
	}
	rs, err := e.files.Renditions(e.ctx, e.attorney, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	var mds []files.RenditionView
	for _, r := range rs {
		if r.Kind == "markdown" {
			mds = append(mds, r)
		}
	}
	if len(mds) != 2 || mds[0].Number != 1 || mds[1].Number != 2 {
		t.Fatalf("markdown versions %+v", mds)
	}
	if mds[0].ID != first.ID || mds[0].SHA256 != first.SHA256 {
		t.Fatalf("the old Markdown version changed: %+v vs %+v", mds[0], first)
	}
	dl, err := e.files.OpenRendition(e.ctx, e.attorney, f.ID, first.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := io.ReadAll(dl.Body)
	dl.Close()
	if string(old) != firstMD {
		t.Fatal("the old Markdown bytes changed")
	}
}

// AC-049-F3: a photo grouping a person set is not changed by a reprocess.
func TestAC049PersonGroupingIsLocked(t *testing.T) {
	e := newEnv(t)
	photos := uploadPhotos(e)
	e.drain()
	// A person groups the first two letter pages before the quiet time is over.
	spec := string(photos[1].ID) + "," + string(photos[0].ID)
	if err := e.svc.PersonGrouping(e.ctx, e.attorney, spec); err != nil {
		t.Fatal(err)
	}
	e.clock.advance(10 * minuteUnit)
	e.drain()
	g, err := e.svc.groupOf(e.ctx, photos[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.How != "person" || len(g.Members) != 2 || g.Members[0] != photos[1].ID || g.Members[1] != photos[0].ID {
		t.Fatalf("person grouping %+v", g)
	}
	for _, p := range photos[:2] {
		if _, err := e.svc.Reprocess(e.ctx, e.attorney, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	e.drain()
	e.clock.advance(10 * minuteUnit)
	e.drain()
	again, err := e.svc.groupOf(e.ctx, photos[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != g.ID || strings.Join(idStrings(again.Members), ",") != strings.Join(idStrings(g.Members), ",") {
		t.Fatalf("grouping changed: %+v -> %+v", g, again)
	}
	if err := e.svc.PersonGrouping(e.ctx, e.attorney, string(photos[0].ID)); !isCode(err, domain.CodeConflict) {
		t.Fatalf("regrouping a locked photo: %v", err)
	}
}

func idStrings(ids []domain.ID) []string {
	var out []string
	for _, i := range ids {
		out = append(out, string(i))
	}
	return out
}

package doctender

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

func (e *env) noMatterFile(p domain.Principal, name, contact string, body []byte) files.FileView {
	e.t.Helper()
	f, err := e.files.Create(e.ctx, p, files.NewFile{Name: name, MediaType: mediaFor(name), Source: "upload", ContactID: domain.ID(contact)},
		bytes.NewReader(body), int64(len(body)))
	if err != nil {
		e.t.Fatalf("create %s: %v", name, err)
	}
	return f
}

func mediaFor(name string) string {
	if strings.HasSuffix(name, ".png") {
		return "image/png"
	}
	return "text/plain"
}

func (e *env) contact(full string) string {
	e.t.Helper()
	c := id(e.t)
	at := domain.UnixMicroUTC(e.clock.t)
	parts := strings.Fields(full)
	if _, err := e.pool.ExecContext(e.ctx, db.Rebind(`INSERT INTO contacts (id, created_at, updated_at, created_by, kind, given_name, middle_name, family_name,
prefix, suffix, full_name, preferred_name, also_known_as, date_of_birth, date_of_death, ssn_ein_ciphertext, entity_type, formation_state, formation_date, archived, source)
VALUES (?,?,?,?,'person',?,'',?,'','',?,'','','','','','','','',0,'test')`, "postgres"), c, at, at, string(domain.SystemUserID), parts[0], parts[1], full); err != nil {
		e.t.Fatal(err)
	}
	return string(c)
}

// leaks reports which of the walled matter's details appear in s.
func leaks(s string) []string {
	var out []string
	for _, w := range []string{"Casey", "Sample", "2026-0002", "Sealed"} {
		if strings.Contains(s, w) {
			out = append(out, w)
		}
	}
	return out
}

// Codex P1: a matter-matching suggestion names a matter, so it is shown only to a
// reviewer who may read that matter, and never goes into mail, summaries or spend for
// a walled matter.
func TestMatterSuggestionRespectsWalls(t *testing.T) {
	e := newEnv(t)
	// The walled matter (2026-0002, client Casey Sample): attorney and staff are on the
	// wall; admin and outsider are not.
	loose := e.noMatterFile(e.attorney, "loose.txt", "", []byte("LETTER TO THE COURT\nSigned by Casey Sample on 2026-10-01.\n"))
	onOpen := e.upload(e.attorney, e.open, "hearing.txt", "text/plain", []byte("NOTICE OF HEARING\nCasey Sample will appear on 2026-12-01.\n"))
	e.drain()

	lex, oex := e.openExceptions(loose.ID), e.openExceptions(onOpen.ID)
	if len(lex) != 1 || lex[0].Kind != "unmatched_matter" || lex[0].BestAnswer["matter_id"] != string(e.walled) {
		t.Fatalf("loose: %+v", lex)
	}
	if len(oex) != 1 || oex[0].Kind != "unmatched_matter" || oex[0].BestAnswer["matter_id"] != string(e.walled) {
		t.Fatalf("on open: %+v", oex)
	}
	// What is stored and shown as detail names no matter.
	for _, x := range append(lex, oex...) {
		if l := leaks(x.Detail); l != nil {
			t.Fatalf("detail %q leaks %v", x.Detail, l)
		}
	}

	// The attorney on the wall sees the suggestion and why.
	got, err := e.svc.Exception(e.ctx, e.attorney, lex[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BestAnswer["matter_id"] != string(e.walled) || !strings.Contains(fmt.Sprint(got.BestAnswer["match_reason"]), "Casey Sample") {
		t.Fatalf("attorney on the wall: %+v", got.BestAnswer)
	}
	if _, err := e.svc.Exception(e.ctx, e.attorney, oex[0].ID); err != nil {
		t.Fatalf("attorney on the wall lost the on-matter item: %v", err)
	}

	// Walled-out reviewers (an admin and an attorney) never learn the walled matter.
	for _, p := range []domain.Principal{e.admin, e.outsider} {
		items, _, err := e.svc.Exceptions(e.ctx, p, ExceptionQuery{Status: "open"})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(items)
		if l := leaks(string(raw)); l != nil || strings.Contains(string(raw), string(e.walled)) {
			t.Fatalf("walled-out list leaks %v: %s", l, raw)
		}
		var sawLoose bool
		for _, x := range items {
			if x.ID == oex[0].ID {
				t.Fatal("a walled-out reviewer sees the item that is only about the walled matter")
			}
			if x.ID == lex[0].ID {
				sawLoose = true
				if _, ok := x.BestAnswer["matter_id"]; ok {
					t.Fatalf("matter_id not redacted: %+v", x.BestAnswer)
				}
			}
		}
		if !sawLoose {
			t.Fatal("the loose file's item is hidden; only the suggestion should be")
		}
		if _, err := e.svc.Exception(e.ctx, p, oex[0].ID); !isCode(err, domain.CodeNotFound) {
			t.Fatalf("walled-out read of the on-matter item: %v", err)
		}
		one, err := e.svc.Exception(e.ctx, p, lex[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ = json.Marshal(one)
		if l := leaks(string(raw)); l != nil || strings.Contains(string(raw), string(e.walled)) {
			t.Fatalf("walled-out item leaks %v: %s", l, raw)
		}
		st, err := e.svc.Status(e.ctx, p, onOpen.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Flags) != 0 {
			t.Fatalf("walled-out flags %+v", st.Flags)
		}
		// Accept has nothing to accept for them, and does not reach the walled matter.
		if _, err := e.svc.ResolveException(e.ctx, p, lex[0].ID, Resolve{Action: "accept"}); !isCode(err, domain.CodeValidation) {
			t.Fatalf("walled-out accept: %v", err)
		}
	}
	// The HTML item page renders the same redacted item.
	row := (&Handler{Svc: e.svc}).row(httptest.NewRequest("GET", "/doc-tender/review", nil), e.admin, mustException(t, e, e.admin, lex[0].ID))
	if l := leaks(row.BestJSON); l != nil || strings.Contains(row.BestJSON, string(e.walled)) {
		t.Fatalf("item page leaks %v: %s", l, row.BestJSON)
	}

	// Mail to admins and the daily summary carry nothing about the walled matter.
	if err := e.svc.FlushExceptionMail(e.ctx); err != nil {
		t.Fatal(err)
	}
	mail := strings.Join(e.mail.all(), "\n")
	if !strings.Contains(mail, "review queue") {
		t.Fatalf("no exception mail: %q", mail)
	}
	if l := leaks(mail); l != nil {
		t.Fatalf("mail leaks %v:\n%s", l, mail)
	}
	summary, err := e.svc.DailySummary(e.ctx, e.clock.now())
	if err != nil {
		t.Fatal(err)
	}
	if l := leaks(summary); l != nil {
		t.Fatalf("summary leaks %v:\n%s", l, summary)
	}

	// A grouping call on the walled matter (a matter, no file) is not in a walled-out
	// reviewer's spend report.
	if err := e.svc.logCall(e.ctx, TaskGrouping, Response{Provider: "openai", Model: "gpt-test-strong", PromptTokens: 10}, Route{}, "", e.walled); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		p    domain.Principal
		want bool
	}{{e.admin, false}, {e.attorney, true}} {
		rows, err := e.svc.Spend(e.ctx, c.p, SpendQuery{})
		if err != nil {
			t.Fatal(err)
		}
		var saw bool
		for _, r := range rows {
			saw = saw || r.MatterID == string(e.walled)
		}
		if saw != c.want {
			t.Fatalf("spend for %v shows the walled grouping row: %v", c.p.Roles, saw)
		}
	}
}

func mustException(t *testing.T, e *env, p domain.Principal, x domain.ID) Exception {
	t.Helper()
	got, err := e.svc.Exception(e.ctx, p, x)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Codex P1: Accept on an unmatched_matter item sets the suggested matter and lets the
// requeue name the file from that matter; it never locks a name built from the
// arrival matter (or from no matter).
func TestAcceptSuggestedMatterNamesFromTheNewMatter(t *testing.T) {
	e := newEnv(t)
	wrong := e.upload(e.staff, e.open, "wrong matter.txt", "text/plain", []byte("NOTICE OF HEARING\nCase 24JE-DR01234 on 2026-12-01 for Morgan Testperson.\n"))
	loose := e.noMatterFile(e.staff, "loose.txt", "", []byte("LETTER ABOUT CASE 23AR-CV00825\nFrom Jordan Example.\n"))
	e.drain()
	for _, c := range []struct {
		f      files.FileView
		matter domain.ID
		prefix string
	}{
		{wrong, e.other, "acme-holding-company-2026-0003-notice-of-hearing"},
		{loose, e.open, "example-2026-0001-letter-about-case"},
	} {
		ex := e.openExceptions(c.f.ID)
		if len(ex) != 1 || ex[0].Kind != "unmatched_matter" || ex[0].BestAnswer["matter_id"] != string(c.matter) {
			t.Fatalf("%s: %+v", c.f.Name, ex)
		}
		if _, ok := ex[0].BestAnswer["name"]; ok {
			t.Fatalf("%s: a name is proposed before the matter is settled: %+v", c.f.Name, ex[0].BestAnswer)
		}
		if _, err := e.svc.ResolveException(e.ctx, e.attorney, ex[0].ID, Resolve{Action: "accept"}); err != nil {
			t.Fatal(err)
		}
		e.drain()
		got := e.get(e.attorney, c.f.ID)
		if got.MatterID == nil || *got.MatterID != c.matter {
			t.Fatalf("%s: matter %v", c.f.Name, got.MatterID)
		}
		if !strings.HasPrefix(got.Name, c.prefix) {
			t.Fatalf("%s: named %q, want the new matter's tokens (%s...)", c.f.Name, got.Name, c.prefix)
		}
		if contains(got.LockedFields, "name") || !contains(got.LockedFields, "matter") {
			t.Fatalf("%s: locked %v", c.f.Name, got.LockedFields)
		}
		if open := e.openExceptions(c.f.ID); len(open) != 0 {
			t.Fatalf("%s: still open %+v", c.f.Name, open)
		}
	}
}

// Codex P1: a grouped PDF is as restricted as its most restricted photo.
func TestGroupedPDFTakesTheMostRestrictiveAccess(t *testing.T) {
	e := newEnv(t)
	photos := letterLeasePhotos()[:3]
	for k, img := range photos {
		nf := files.NewFile{Name: fmt.Sprintf("IMG_%04d.png", 200+k), MatterID: e.open, MediaType: "image/png", Source: "upload",
			AttorneyOnly: k == 1, SharedWithClient: k == 0}
		if _, err := e.files.Create(e.ctx, e.attorney, nf, bytes.NewReader(img), int64(len(img))); err != nil {
			t.Fatal(err)
		}
	}
	e.drain()
	e.clock.advance(10 * minuteUnit)
	e.drain()
	list, _, err := e.files.List(e.ctx, e.attorney, files.ListQuery{MatterID: e.open})
	if err != nil {
		t.Fatal(err)
	}
	var pdf *files.FileView
	for k := range list {
		if list[k].Extension == "pdf" {
			pdf = &list[k]
		}
	}
	if pdf == nil {
		t.Fatal("no grouped PDF")
	}
	if !pdf.AttorneyOnly || pdf.SharedWithClient {
		t.Fatalf("grouped PDF attorney_only %v shared_with_client %v", pdf.AttorneyOnly, pdf.SharedWithClient)
	}
	if _, err := e.files.Get(e.ctx, e.staff, pdf.ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("staff reads the PDF holding an attorney-only photo: %v", err)
	}
	if _, _, err := e.files.MarkdownCopy(e.ctx, e.staff, pdf.ID); err == nil {
		t.Fatal("staff reads the Markdown copy holding an attorney-only photo")
	}
}

// Codex P1: photos with no matter are grouped only within one contact, and a photo
// with neither is its own document; the grouping model never sees two clients'
// photos together.
func TestNoMatterPhotosAreNeverGroupedAcrossContacts(t *testing.T) {
	e := newEnv(t)
	a, b := e.contact("Avery Alpha"), e.contact("Blake Beta")
	photos := letterLeasePhotos()
	byFile := map[domain.ID]string{}
	add := func(name, contact string, img []byte) {
		f := e.noMatterFile(e.staff, name, contact, img)
		byFile[f.ID] = contact
	}
	// Letter pages 1 and 3 from Avery, letter page 2 with no contact, lease pages 1 and
	// 2 from Blake. Grouped across scopes, the letter would become three pages.
	add("IMG_0301.png", a, photos[0])
	add("IMG_0302.png", b, photos[3])
	add("IMG_0303.png", a, photos[2])
	add("IMG_0304.png", "", photos[1])
	add("IMG_0305.png", b, photos[4])
	e.drain()
	e.clock.advance(10 * minuteUnit)
	e.drain()
	rows, err := e.pool.QueryContext(e.ctx, `SELECT g.id, g.how, m.file_id FROM core.doc_tender_groups g JOIN core.doc_tender_group_members m ON m.group_id = g.id`)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]map[string]bool{}
	how := map[string]string{}
	placed := 0
	for rows.Next() {
		var g, h, f string
		if err := rows.Scan(&g, &h, &f); err != nil {
			t.Fatal(err)
		}
		if groups[g] == nil {
			groups[g] = map[string]bool{}
		}
		groups[g][byFile[domain.ID(f)]] = true
		how[g] = h
		placed++
	}
	rows.Close()
	if placed != 5 {
		t.Fatalf("%d of 5 photos grouped", placed)
	}
	var docGroups int
	for g, cs := range groups {
		if len(cs) != 1 {
			t.Fatalf("group %s spans contacts %v", g, cs)
		}
		if cs[""] && how[g] != "single" {
			t.Fatalf("a photo with no matter and no contact was grouped (%s)", how[g])
		}
		if how[g] != "single" {
			docGroups++
		}
	}
	if docGroups != 2 {
		t.Fatalf("%d document groups, want one per contact (Avery's letter, Blake's lease)", docGroups)
	}
	// Each PDF carries its contact, and only its contact's photos are its sources.
	list, _, err := e.files.List(e.ctx, e.attorney, files.ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range list {
		if f.Extension != "pdf" {
			continue
		}
		if f.ContactID == nil {
			t.Fatalf("grouped PDF %s has no contact", f.Name)
		}
		links, err := e.files.Links(e.ctx, e.attorney, f.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range links {
			src := l.ToFileID
			if src == f.ID {
				src = l.FromFileID
			}
			if byFile[src] != string(*f.ContactID) {
				t.Fatalf("PDF of %s has a source from %q", *f.ContactID, byFile[src])
			}
		}
	}
	// The no-contact photo was labeled on its own, without waiting for a grouping call.
	if n := e.ai.callCount("grouping"); n != 4 {
		t.Fatalf("%d grouping calls, want 4 (model and check, once per contact)", n)
	}
}

func TestGroupedFileRefusesMixedScopes(t *testing.T) {
	m1, m2, c1, c2 := domain.ID("m1"), domain.ID("m2"), domain.ID("c1"), domain.ID("c2")
	cases := []struct {
		name string
		ms   []files.FileView
	}{
		{"two matters", []files.FileView{{MatterID: &m1}, {MatterID: &m2}}},
		{"matter and none", []files.FileView{{MatterID: &m1}, {}}},
		{"no matter, two contacts", []files.FileView{{ContactID: &c1}, {ContactID: &c2}}},
		{"no matter, no contact", []files.FileView{{}, {}}},
	}
	for _, c := range cases {
		if _, err := groupedFile("x", c.ms); !isCode(err, domain.CodeValidation) {
			t.Fatalf("%s: %v", c.name, err)
		}
	}
	nf, err := groupedFile("x", []files.FileView{{MatterID: &m1, ContactID: &c1, SharedWithClient: true}, {MatterID: &m1, ContactID: &c2, AttorneyOnly: true, SharedWithClient: true}})
	if err != nil || !nf.AttorneyOnly || !nf.SharedWithClient || nf.ContactID != "" || nf.MatterID != m1 {
		t.Fatalf("one matter: %+v %v", nf, err)
	}
	nf, _ = groupedFile("x", []files.FileView{{MatterID: &m1, SharedWithClient: true}, {MatterID: &m1}})
	if nf.SharedWithClient {
		t.Fatal("shared with the client though one photo is not")
	}
}

// ---- Codex P2: DOC and RTF are checked against counts that are not the converter's ----

type fakeDocs struct {
	md           string
	words, pages int
}

func (f fakeDocs) ToMarkdown(context.Context, string, []byte) (string, int, int, error) {
	return f.md, f.words, f.pages, nil
}

// cfb builds a version 3 compound file holding one \x05SummaryInformation stream with
// the given counts, in the mini stream (as Word writes it) or in regular sectors.
func cfb(t *testing.T, words, pages int, mini bool) []byte {
	t.Helper()
	le := binary.LittleEndian
	// The property set: header, one section with PIDSI_PAGECOUNT and PIDSI_WORDCOUNT.
	ps := make([]byte, 48)
	le.PutUint16(ps[0:], 0xFFFE)
	le.PutUint32(ps[24:], 1)
	le.PutUint32(ps[44:], 48)
	sec := make([]byte, 8+16+16)
	le.PutUint32(sec[0:], uint32(len(sec)))
	le.PutUint32(sec[4:], 2)
	le.PutUint32(sec[8:], 14)
	le.PutUint32(sec[12:], 24)
	le.PutUint32(sec[16:], 15)
	le.PutUint32(sec[20:], 32)
	le.PutUint32(sec[24:], 3)
	le.PutUint32(sec[28:], uint32(pages))
	le.PutUint32(sec[32:], 3)
	le.PutUint32(sec[36:], uint32(words))
	stream := append(ps, sec...)
	if !mini {
		stream = append(stream, make([]byte, 4096-len(stream))...)
	}
	const ss = 512
	free, end, fatSect := uint32(0xFFFFFFFF), uint32(0xFFFFFFFE), uint32(0xFFFFFFFD)
	fat := make([]uint32, ss/4)
	for k := range fat {
		fat[k] = free
	}
	fat[0], fat[1] = fatSect, end // sector 0 FAT, sector 1 directory
	var sectors [][]byte
	pad := func(b []byte) []byte { return append(b, make([]byte, (ss-len(b)%ss)%ss)...) }
	miniFATStart, rootStart, streamStart := end, end, uint32(0)
	next := uint32(2)
	chain := func(n int) uint32 {
		start := next
		for k := 0; k < n; k++ {
			if k == n-1 {
				fat[next] = end
			} else {
				fat[next] = next + 1
			}
			next++
		}
		return start
	}
	rootSize := 0
	if mini {
		// sector 2: mini FAT; sector 3: the mini stream (the stream in 64-byte pieces).
		n := (len(stream) + 63) / 64
		mf := make([]byte, ss)
		for k := 0; k < ss/4; k++ {
			v := free
			if k < n-1 {
				v = uint32(k + 1)
			} else if k == n-1 {
				v = end
			}
			le.PutUint32(mf[k*4:], v)
		}
		miniFATStart = chain(1)
		sectors = append(sectors, mf)
		ms := pad(append([]byte(nil), stream...))
		rootStart = chain(len(ms) / ss)
		for k := 0; k < len(ms); k += ss {
			sectors = append(sectors, ms[k:k+ss])
		}
		rootSize = n * 64
		streamStart = 0
	} else {
		body := pad(append([]byte(nil), stream...))
		streamStart = chain(len(body) / ss)
		for k := 0; k < len(body); k += ss {
			sectors = append(sectors, body[k:k+ss])
		}
	}
	entry := func(name string, kind byte, start uint32, size int) []byte {
		e := make([]byte, 128)
		u := utf16.Encode([]rune(name))
		for k, c := range u {
			le.PutUint16(e[k*2:], c)
		}
		le.PutUint16(e[64:], uint16((len(u)+1)*2))
		e[66] = kind
		le.PutUint32(e[68:], free)
		le.PutUint32(e[72:], free)
		le.PutUint32(e[76:], free)
		le.PutUint32(e[116:], start)
		le.PutUint32(e[120:], uint32(size))
		return e
	}
	dir := append(entry("Root Entry", 5, rootStart, rootSize), entry("\x05SummaryInformation", 2, streamStart, len(stream))...)
	dir = pad(dir)
	h := make([]byte, 512)
	copy(h, cfbMagic)
	le.PutUint16(h[24:], 0x3E)
	le.PutUint16(h[26:], 3)
	le.PutUint16(h[28:], 0xFFFE)
	le.PutUint16(h[30:], 9)
	le.PutUint16(h[32:], 6)
	le.PutUint32(h[44:], 1)
	le.PutUint32(h[48:], 1)
	le.PutUint32(h[56:], 4096)
	le.PutUint32(h[60:], miniFATStart)
	nMini := 0
	if mini {
		nMini = 1
	}
	le.PutUint32(h[64:], uint32(nMini))
	le.PutUint32(h[68:], end)
	for k := 0; k < 109; k++ {
		le.PutUint32(h[76+k*4:], free)
	}
	le.PutUint32(h[76:], 0)
	fb := make([]byte, ss)
	for k, v := range fat {
		le.PutUint32(fb[k*4:], v)
	}
	out := append(h, fb...)
	out = append(out, dir...)
	for _, s := range sectors {
		out = append(out, s...)
	}
	return out
}

func TestDocAndRTFCountsAreTheDocumentsOwn(t *testing.T) {
	for _, mini := range []bool{true, false} {
		w, p, ok := docCounts(cfb(t, 123, 4, mini))
		if !ok || w != 123 || p != 4 {
			t.Fatalf("mini %v: words %d pages %d ok %v", mini, w, p, ok)
		}
		// A truncated or garbled file has no counts and does not panic.
		raw := cfb(t, 123, 4, mini)
		for _, cut := range []int{600, 1100, len(raw) - 10} {
			if _, _, ok := docCounts(raw[:cut]); ok && cut < len(raw)-10 {
				t.Fatalf("mini %v: counts from a file cut at %d", mini, cut)
			}
		}
		bad := append([]byte(nil), raw...)
		binary.LittleEndian.PutUint32(bad[512+8:], 1) // a FAT loop on sector 2
		_, _, _ = docCounts(bad)
	}
	if _, _, ok := docCounts([]byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1 WordDocument")); ok {
		t.Fatal("counts from a stand-in with no SummaryInformation")
	}
	if w, p, ok := rtfCounts([]byte(`{\rtf1{\info{\nofpages2}{\nofwords57}}Body}`)); !ok || w != 57 || p != 2 {
		t.Fatalf("rtf info: %d %d %v", w, p, ok)
	}
	body := `{\rtf1\ansi{\fonttbl{\f0 Times New Roman;}}{\colortbl;\red0\green0\blue0;}{\*\generator Test 1.0;}\f0 Dear counsel,\par Caf\'e9 \u8212? hearing {\b moved} to\~Monday.\par}`
	if got := strings.Fields(rtfText([]byte(body))); strings.Join(got, " ") != "Dear counsel, Café — hearing moved to Monday." {
		t.Fatalf("rtf text %q", got)
	}
	if _, _, ok := rtfCounts([]byte("not rtf")); ok {
		t.Fatal("counts from a file that is not RTF")
	}
}

// A DOC or RTF passes only when the converter agrees with the document's own counts;
// with none to check against, it goes to review rather than being published as
// checked.
func TestWordFamilyChecksAreIndependent(t *testing.T) {
	e := newEnv(t)
	e.set("doctender.retry_schedule", `["0s","0s","0s","0s","0s","0s"]`)
	e.svc.d.Docs = fakeDocs{md: "Dear counsel the hearing moved", words: 5, pages: 1}
	good := e.upload(e.staff, e.open, "letter.doc", "application/msword", cfb(t, 5, 1, true))
	off := e.upload(e.staff, e.open, "off.doc", "application/msword", cfb(t, 9, 1, true))
	none := e.upload(e.staff, e.open, "none.doc", "application/msword", []byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1 WordDocument"))
	rtfInfo := e.upload(e.staff, e.open, "info.rtf", "application/rtf", []byte(`{\rtf1{\info{\nofpages1}{\nofwords5}}Dear counsel the hearing moved\par}`))
	rtfText := e.upload(e.staff, e.open, "plain.rtf", "application/rtf", []byte(`{\rtf1\ansi Dear counsel the hearing moved\par}`))
	rtfOff := e.upload(e.staff, e.open, "longer.rtf", "application/rtf", []byte(`{\rtf1\ansi Dear counsel the hearing moved to Monday\par}`))
	e.drain()
	for _, f := range []files.FileView{good, rtfInfo, rtfText} {
		u := e.unitsFor(e.jobFor(f.ID).ID)[0]
		if u.Status != unitPassed || !strings.Contains(u.CheckResult, "document says 5") {
			t.Fatalf("%s: %s %q", f.Name, u.Status, u.CheckResult)
		}
		if ex := e.openExceptions(f.ID); len(ex) != 0 {
			t.Fatalf("%s: %+v", f.Name, ex)
		}
	}
	for _, c := range []struct {
		f    files.FileView
		want string
	}{{off, "document says 9"}, {none, "no word count of its own"}, {rtfOff, "document says 7"}} {
		u := e.unitsFor(e.jobFor(c.f.ID).ID)[0]
		if u.Status != unitFailed || !strings.Contains(u.CheckResult, c.want) {
			t.Fatalf("%s: %s %q", c.f.Name, u.Status, u.CheckResult)
		}
		ex := e.openExceptions(c.f.ID)
		if len(ex) != 1 || ex[0].Kind != "extraction_failed" {
			t.Fatalf("%s: %+v", c.f.Name, ex)
		}
		_, md := e.markdown(e.attorney, c.f.ID)
		if !strings.Contains(md, "[unresolved: document]") {
			t.Fatalf("%s published as checked:\n%s", c.f.Name, md)
		}
	}
}

// Opus checkpoint P1: a matter a person set or confirmed stays settled. Assign matter to
// the file's own matter, Keep as is, or Assign matter on a file with no matter each
// close the question for good: the requeue names the file from that matter and no new
// "matter unclear" item opens, though the content still points at another matter.
func TestPersonsMatterDecisionStaysSettled(t *testing.T) {
	e := newEnv(t)
	sameAssign := e.upload(e.staff, e.open, "assign same.txt", "text/plain", []byte("NOTICE OF HEARING\nCase 24JE-DR01234 on 2026-12-02 for Morgan Testperson.\n"))
	kept := e.upload(e.staff, e.open, "keep as is.txt", "text/plain", []byte("NOTICE OF HEARING\nCase 24JE-DR01234 on 2026-12-03 for Morgan Testperson.\n"))
	loose := e.noMatterFile(e.staff, "loose assign.txt", "", []byte("LETTER ABOUT CASE 23AR-CV00825\nFrom Jordan Example on 2026-10-04.\n"))
	e.drain()
	for _, c := range []struct {
		f      files.FileView
		res    Resolve
		matter domain.ID
		prefix string
	}{
		{sameAssign, Resolve{Action: "assign_matter", MatterID: &e.open}, e.open, "example-2026-0001-notice-of-hearing"},
		{kept, Resolve{Action: "keep"}, e.open, "example-2026-0001-notice-of-hearing"},
		{loose, Resolve{Action: "assign_matter", MatterID: &e.other}, e.other, "acme-holding-company-2026-0003-letter-about-case"},
	} {
		ex := e.openExceptions(c.f.ID)
		if len(ex) != 1 || ex[0].Kind != "unmatched_matter" {
			t.Fatalf("%s: want one matter-unclear item, got %+v", c.f.Name, ex)
		}
		if _, err := e.svc.ResolveException(e.ctx, e.attorney, ex[0].ID, c.res); err != nil {
			t.Fatalf("%s: %v", c.f.Name, err)
		}
		e.drain()
		if open := e.openExceptions(c.f.ID); len(open) != 0 {
			t.Fatalf("%s (%s): the matter question reopened: %+v", c.f.Name, c.res.Action, open)
		}
		got := e.get(e.attorney, c.f.ID)
		if got.MatterID == nil || *got.MatterID != c.matter {
			t.Fatalf("%s: matter %v, want %s", c.f.Name, got.MatterID, c.matter)
		}
		if !strings.HasPrefix(got.Name, c.prefix) {
			t.Fatalf("%s (%s): named %q, want the pattern name from the settled matter (%s...)", c.f.Name, c.res.Action, got.Name, c.prefix)
		}
	}
}

// Opus checkpoint P2: every model call is logged with its cost from the AI tab's model
// prices (PRD 8.21 Cost), so the Spend report and daily summary show spend, not $0.00.
func TestCallLogCostFromModelPrices(t *testing.T) {
	e := newEnv(t)
	// One cent per input token and two per output token, so each row's cost is exact.
	e.set("ai.model_prices", `{"gpt-test-fast": {"input": 1000000, "output": 2000000}, "gpt-test-strong": {"input": 1000000, "output": 2000000}, "`+domain.LabelingModel+`": {"input": 1000000, "output": 2000000}}`)
	f := e.upload(e.staff, e.open, "priced.txt", "text/plain", []byte("NOTICE OF HEARING\nCase 23AR-CV00825 on 2026-12-04 for Jordan Example.\n"))
	e.drain()
	rows, err := e.pool.QueryContext(e.ctx, `SELECT model, prompt_tokens, completion_tokens, cost_cents FROM ai_call_log WHERE file_id = $1`, string(f.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n, total := 0, int64(0)
	for rows.Next() {
		var model string
		var prompt, completion int
		var cost int64
		if err := rows.Scan(&model, &prompt, &completion, &cost); err != nil {
			t.Fatal(err)
		}
		if want := int64(prompt + 2*completion); cost != want {
			t.Fatalf("%s: cost %d for %d in and %d out, want %d", model, cost, prompt, completion, want)
		}
		n++
		total += cost
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n == 0 || total == 0 {
		t.Fatalf("calls %d, total cost %d: want priced calls", n, total)
	}
	// A model with no price is logged at 0 cents.
	e.set("ai.model_prices", `{}`)
	g := e.upload(e.staff, e.open, "unpriced.txt", "text/plain", []byte("LETTER TO THE COURT\nSigned by Casey Sample on 2026-10-05.\n"))
	e.drain()
	var unpriced int64
	if err := e.pool.QueryRowContext(e.ctx, `SELECT COALESCE(SUM(cost_cents), 0) FROM ai_call_log WHERE file_id = $1`, string(g.ID)).Scan(&unpriced); err != nil {
		t.Fatal(err)
	}
	if unpriced != 0 {
		t.Fatalf("unpriced calls cost %d", unpriced)
	}
	// The setting refuses a price table that is not model to input and output cents.
	if _, err := e.reg.Set(e.ctx, "ai.model_prices", `{"gpt-test-fast": {"input": -1, "output": 1}}`, e.admin); err == nil {
		t.Fatal("a negative price was accepted")
	}
}

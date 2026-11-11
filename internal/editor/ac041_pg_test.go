package editor

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// AC-041, first clause: a DOCX opens in the editor, saves as a new version, and the
// saved package is one Word opens without a repair prompt.
func TestPGDocxOpensAndSavesAsANewWordCompatibleVersion(t *testing.T) {
	e := newEnv(t)
	original := e.doc(Command{ID: "text.insert", Parameters: map[string]any{"text": "Synthetic engagement letter."}})
	f := e.create(e.staff, "Engagement letter.docx", e.matter, original, false)

	if _, err := e.svc.AcquireLock(e.ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	name, sent, err := e.svc.Document(e.ctx, e.staff, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if name != "Engagement letter.docx" || !bytes.Equal(sent, original) {
		t.Fatalf("the editor was sent %q (%d bytes), not the current version", name, len(sent))
	}
	// What the browser editor hands back on Save: LawCraft's own DOCX writer at the pin.
	edited, err := e.cli.Run(e.ctx, RunInput{Document: sent, Ext: "docx", Commands: []Command{
		{ID: "caret.docEnd"}, {ID: "text.insert", Parameters: map[string]any{"text": " Edited in the editor."}},
	}, Out: "docx"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.Save(e.ctx, e.staff, f.ID, "Engagement letter.docx", edited)
	if err != nil {
		t.Fatal(err)
	}
	if v.Number != 2 || v.How != "editor" || v.SavedBy != e.staff.UserID || v.MediaType != docxMedia {
		t.Fatalf("saved version = %+v", v)
	}
	vs := e.versions(f.ID)
	if len(vs) != 2 || vs[0].SHA256 == vs[1].SHA256 {
		t.Fatalf("versions = %+v", vs)
	}
	cur, _, stored := e.current(f.ID)
	if cur.Name != "Engagement letter.docx" {
		t.Fatalf("name changed to %s", cur.Name)
	}
	if err := CheckDOCX(stored); err != nil {
		t.Fatalf("the saved version is not Word-compatible: %v", err)
	}
	if txt := e.text(stored, "docx"); !strings.Contains(txt, "Synthetic engagement letter. Edited in the editor.") {
		t.Fatalf("saved text = %q", txt)
	}
	// Version 1 is untouched.
	d, err := e.files.OpenForProcessing(e.ctx, e.admin, f.ID, vs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := io.ReadAll(d.Body)
	_ = d.Close()
	if !bytes.Equal(first, original) {
		t.Fatal("version 1 changed")
	}
}

// A file in another format saves as DOCX and its extension becomes .docx.
func TestPGOtherFormatsSaveAsDocx(t *testing.T) {
	e := newEnv(t)
	odt, err := e.cli.Convert(e.ctx, []byte("Synthetic notes for the file.\n"), "txt", "odt")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, savedAs string
		body          []byte
	}{
		{"Notes.odt", "Notes.odt", odt},
		{"Draft.md", "Draft.md", []byte("# Draft\n\nSynthetic markdown.\n")},
	} {
		f := e.create(e.staff, c.name, e.matter, c.body, false)
		if _, err := e.svc.AcquireLock(e.ctx, e.staff, f.ID); err != nil {
			t.Fatal(err)
		}
		v, err := e.svc.Save(e.ctx, e.staff, f.ID, c.savedAs, c.body)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		cur, _, stored := e.current(f.ID)
		want := strings.TrimSuffix(c.name, c.name[strings.LastIndex(c.name, "."):]) + ".docx"
		if cur.Name != want || cur.Extension != "docx" || v.MediaType != docxMedia {
			t.Fatalf("%s saved as %s (%s, %s)", c.name, cur.Name, cur.Extension, v.MediaType)
		}
		if err := CheckDOCX(stored); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if renames := e.audits("file", f.ID, "name"); len(renames) < 2 || renames[len(renames)-1].NewValue != want {
			t.Fatalf("%s: the rename is not in the edit history: %+v", c.name, renames)
		}
	}
}

func TestPGSaveRefusesExportsAndBrokenPackages(t *testing.T) {
	e := newEnv(t)
	f := e.create(e.staff, "Letter.docx", e.matter, e.doc(Command{ID: "text.insert", Parameters: map[string]any{"text": "x"}}), false)
	if _, err := e.svc.Save(e.ctx, e.staff, f.ID, "Letter.docx", e.doc()); code(err) != domain.CodeConflict {
		t.Fatalf("a save without the lock = %v", err)
	}
	if _, err := e.svc.AcquireLock(e.ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	pdf, err := e.cli.Convert(e.ctx, []byte("x"), "txt", "pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Save(e.ctx, e.staff, f.ID, "Letter.pdf", pdf); code(err) != domain.CodeValidation {
		t.Fatalf("a PDF export saved as a version: %v", err)
	}
	broken := pkg(t, without(good(), "word/styles.xml")...)
	if _, err := e.svc.Save(e.ctx, e.staff, f.ID, "Letter.docx", broken); code(err) != domain.CodeValidation {
		t.Fatalf("a package Word would repair was saved: %v", err)
	}
	if n := len(e.versions(f.ID)); n != 1 {
		t.Fatalf("refused saves made versions: %d", n)
	}
}

// AC-041, second clause: a second user sees the edit lock, which frees 5 minutes after
// the editor closes without saving.
func TestPGSecondUserSeesTheLockUntilItLapses(t *testing.T) {
	e := newEnv(t)
	f := e.create(e.staff, "Motion.docx", e.matter, e.doc(Command{ID: "text.insert", Parameters: map[string]any{"text": "x"}}), false)
	lock, err := e.svc.AcquireLock(e.ctx, e.staff, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if lock.UserID != e.staff.UserID {
		t.Fatalf("lock = %+v", lock)
	}
	_, err = e.svc.AcquireLock(e.ctx, e.other, f.ID)
	var held *LockHeld
	if !errors.As(err, &held) || held.Holder != e.staff.UserID || held.HolderName != "Sam Staff" || held.Since.IsZero() {
		t.Fatalf("second user = %v", err)
	}
	if !strings.Contains(held.Error(), "Sam Staff has been editing this file since") {
		t.Fatalf("notice = %q", held.Error())
	}
	// The second user can still preview and download the current version.
	if _, err := e.files.OpenVersion(e.ctx, e.other, f.ID, *f.CurrentVersionID, false); err != nil {
		t.Fatalf("download while locked: %v", err)
	}
	// Nor can they save, or run commands into a new version, under the open editor.
	if _, err := e.svc.Save(e.ctx, e.other, f.ID, "Motion.docx", e.doc()); code(err) != CodeEditLockHeld {
		t.Fatalf("save by the second user = %v", err)
	}
	if _, err := e.svc.Run(e.ctx, e.other, f.ID, RunRequest{SaveAs: "version", Commands: []Command{{ID: "text.insert", Parameters: map[string]any{"text": "y"}}}}); code(err) != CodeEditLockHeld {
		t.Fatalf("run into a version under the open editor = %v", err)
	}

	// Renewals keep it: 4 minutes, renew, 4 more minutes is past 5 from the start.
	e.clock.advance(4 * time.Minute)
	if _, err := e.svc.RenewLock(e.ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	e.clock.advance(4 * time.Minute)
	if _, err := e.svc.AcquireLock(e.ctx, e.other, f.ID); code(err) != CodeEditLockHeld {
		t.Fatalf("a renewed lock lapsed: %v", err)
	}
	// The editor closes without saving: no renewals. Just under 5 minutes it holds...
	e.clock.advance(time.Minute - 10*time.Millisecond)
	if _, err := e.svc.AcquireLock(e.ctx, e.other, f.ID); code(err) != CodeEditLockHeld {
		t.Fatalf("lock freed before 5 minutes: %v", err)
	}
	// ...and 5 minutes after the last renewal it is free.
	e.clock.advance(time.Second)
	taken, err := e.svc.AcquireLock(e.ctx, e.other, f.ID)
	if err != nil || taken.UserID != e.other.UserID {
		t.Fatalf("after 5 minutes the second user = %+v, %v", taken, err)
	}
	// The first user's editor can no longer renew or save.
	if _, err := e.svc.RenewLock(e.ctx, e.staff, f.ID); code(err) != CodeEditLockHeld {
		t.Fatalf("the lapsed holder renewed: %v", err)
	}
	if _, err := e.svc.Save(e.ctx, e.staff, f.ID, "Motion.docx", e.doc()); code(err) != CodeEditLockHeld {
		t.Fatalf("the lapsed holder saved: %v", err)
	}
}

func TestPGLockTimeoutFollowsTheDocumentEditorTab(t *testing.T) {
	e := newEnv(t)
	e.set("editor.lock_timeout", "2m0s")
	f := e.create(e.staff, "Notice.docx", e.matter, e.doc(), false)
	if _, err := e.svc.AcquireLock(e.ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	e.clock.advance(2*time.Minute + time.Second)
	if _, err := e.svc.AcquireLock(e.ctx, e.other, f.ID); err != nil {
		t.Fatalf("a 2 minute timeout did not lapse: %v", err)
	}
}

func TestPGOnlyTheHolderOrAnAdminReleasesAndAdminReleasesAreAudited(t *testing.T) {
	e := newEnv(t)
	f := e.create(e.staff, "Answer.docx", e.matter, e.doc(), false)
	if _, err := e.svc.AcquireLock(e.ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ReleaseLock(e.ctx, e.other, f.ID); code(err) != domain.CodeForbidden {
		t.Fatalf("another staff member released the lock: %v", err)
	}
	if rows := e.audits("file", f.ID, "edit_lock"); len(rows) != 0 {
		t.Fatalf("audit before any release: %+v", rows)
	}
	if err := e.svc.ReleaseLock(e.ctx, e.admin, f.ID); err != nil {
		t.Fatal(err)
	}
	rows := e.audits("file", f.ID, "edit_lock")
	if len(rows) != 1 || rows[0].ActorID != e.admin.UserID || !strings.Contains(rows[0].OldValue, "Sam Staff") || rows[0].NewValue != "released by an admin" {
		t.Fatalf("admin release audit = %+v", rows)
	}
	if _, err := e.svc.AcquireLock(e.ctx, e.other, f.ID); err != nil {
		t.Fatalf("after the admin release: %v", err)
	}
	// The holder's own release is not an admin action and is not audited as one.
	if err := e.svc.ReleaseLock(e.ctx, e.other, f.ID); err != nil {
		t.Fatal(err)
	}
	if rows := e.audits("file", f.ID, "edit_lock"); len(rows) != 1 {
		t.Fatalf("a holder's release was audited as an admin release: %+v", rows)
	}
	if err := e.svc.ReleaseLock(e.ctx, e.other, f.ID); code(err) != domain.CodeNotFound {
		t.Fatalf("releasing no lock = %v", err)
	}
}

// Editing needs write access to the file; walls and roles apply as they do to the file,
// and client users get no editor.
func TestPGEditorAccessFollowsTheFile(t *testing.T) {
	e := newEnv(t)
	walled := e.create(e.attorney, "Walled.docx", e.walled, e.doc(), false)
	secret := e.create(e.attorney, "Attorney only.docx", e.matter, e.doc(), true)
	open := e.create(e.staff, "Open.docx", e.matter, e.doc(), false)
	pdf, err := e.cli.Convert(e.ctx, []byte("x"), "txt", "pdf")
	if err != nil {
		t.Fatal(err)
	}
	scan := e.create(e.staff, "Scan.pdf", e.matter, pdf, false)
	cases := []struct {
		who  domain.Principal
		file domain.ID
		want string
	}{
		{e.outsider, walled.ID, domain.CodeNotFound},
		{e.staff, secret.ID, domain.CodeNotFound},
		{e.client, open.ID, domain.CodeForbidden},
		{e.accountant, open.ID, domain.CodeForbidden},
		{e.staff, scan.ID, domain.CodeValidation},
	}
	for _, c := range cases {
		if _, err := e.svc.AcquireLock(e.ctx, c.who, c.file); code(err) != c.want {
			t.Errorf("lock for %v on %s = %v, want %s", c.who.Roles, c.file, err, c.want)
		}
		if _, err := e.svc.Run(e.ctx, c.who, c.file, RunRequest{SaveAs: "version", Commands: []Command{{ID: "text.insert", Parameters: map[string]any{"text": "x"}}}}); code(err) == "" {
			t.Errorf("run for %v on %s succeeded", c.who.Roles, c.file)
		}
		if _, err := e.svc.Convert(e.ctx, c.who, c.file, "pdf"); code(err) == "" && c.file != scan.ID {
			t.Errorf("conversion for %v on %s succeeded", c.who.Roles, c.file)
		}
	}
	if _, err := e.svc.AcquireLock(e.ctx, e.attorney, walled.ID); err != nil {
		t.Fatalf("a user on the wall: %v", err)
	}
	if _, err := e.svc.AcquireLock(e.ctx, e.attorney, secret.ID); err != nil {
		t.Fatalf("an attorney on an attorney-only file: %v", err)
	}
}

// AC-041, third clause: New Pleading fills its caption from the matter.
func TestPGNewPleadingFillsItsCaptionFromTheMatter(t *testing.T) {
	e := newEnv(t)
	f, err := e.svc.NewPleading(e.ctx, e.staff, e.matter, PleadingRequest{Type: "motion", Title: "Motion to Continue"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "Motion to Continue.docx" || f.MatterID == nil || *f.MatterID != e.matter || f.FolderID == nil {
		t.Fatalf("pleading file = %+v", f)
	}
	folder, err := e.files.GetFolder(e.ctx, e.staff, *f.FolderID)
	if err != nil || folder.Name != "Pleadings" {
		t.Fatalf("filed in %+v, %v", folder, err)
	}
	_, v, b := e.current(f.ID)
	if v.How != "editor" {
		t.Fatalf("version how = %s", v.How)
	}
	if err := CheckDOCX(b); err != nil {
		t.Fatal(err)
	}
	txt := e.text(b, "docx")
	for _, want := range []string{
		"IN THE CIRCUIT COURT OF ADAIR COUNTY", "STATE OF MISSOURI", "JANE ROE", "Petitioner", "JOHN ROE", "Respondent",
		"Case No. 26AR-DR00123", "Motion to Continue", "COMES NOW Jane Roe", "Synthetic Law LLC", "1 Main St", "Kirksville, MO 63501",
		"(660) 555-0100", "office@example.invalid", "Ada Example, MO Bar No. 12345",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("the caption or signature lacks %q:\n%s", want, txt)
		}
	}
	// The values sit in LawCraft's tagged content controls, so Edit Pleading can rewrite them.
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var docXML string
	for _, zf := range zr.File {
		if zf.Name == "word/document.xml" {
			rc, _ := zf.Open()
			raw, _ := io.ReadAll(rc)
			_ = rc.Close()
			docXML = string(raw)
		}
	}
	if !strings.Contains(docXML, `w:val="law:caseNumber"`) || !strings.Contains(docXML, `w:val="law:county`) {
		t.Fatal("New Pleading's content controls are missing")
	}
}

func TestPGNewPleadingFollowsTheMatter(t *testing.T) {
	e := newEnv(t)
	// A respondent client: the client is on the second side.
	m := e.dir.matters[e.matter]
	m.ClientPosition = "respondent"
	e.dir.matters[e.matter] = m
	params, err := e.svc.PleadingParams(e.ctx, e.staff, e.matter, PleadingRequest{Type: "answer", Title: "Answer"})
	if err != nil {
		t.Fatal(err)
	}
	second := params["secondSide"].(map[string]any)
	if second["role"] != "Respondent" || params["firstSide"].(map[string]any)["role"] != "Petitioner" {
		t.Fatalf("sides = %v / %v", params["firstSide"], second)
	}
	if _, err := e.svc.NewPleading(e.ctx, e.outsider, e.walled, PleadingRequest{Type: "motion", Title: "X"}); code(err) != domain.CodeNotFound {
		t.Fatalf("a pleading on a walled matter by an outsider = %v", err)
	}
	if _, err := e.svc.NewPleading(e.ctx, e.client, e.matter, PleadingRequest{Type: "motion", Title: "X"}); code(err) != domain.CodeForbidden {
		t.Fatalf("a client's pleading = %v", err)
	}
	m.Parties = m.Parties[:1]
	e.dir.matters[e.matter] = m
	if _, err := e.svc.NewPleading(e.ctx, e.staff, e.matter, PleadingRequest{Type: "motion", Title: "X"}); code(err) != domain.CodeValidation || !strings.Contains(err.Error(), "opposing party") {
		t.Fatalf("no opposing party = %v", err)
	}
	e.set("firm.address", "Somewhere")
	m.Parties = append(m.Parties, Party{Role: "opposing party", Name: "John Roe"})
	e.dir.matters[e.matter] = m
	if _, err := e.svc.NewPleading(e.ctx, e.staff, e.matter, PleadingRequest{Type: "motion", Title: "X"}); code(err) != domain.CodeValidation || !strings.Contains(err.Error(), "firm address") {
		t.Fatalf("an unusable firm address = %v", err)
	}
	if _, err := e.svc.NewPleading(e.ctx, e.staff, e.matter, PleadingRequest{Type: "brief", Title: "X"}); code(err) != domain.CodeValidation {
		t.Fatalf("an unknown type = %v", err)
	}
}

// AC-041, last clause: the Document editor tab shows commit 770a8fa…, read only.
func TestPGDocumentEditorTabShowsThePinnedCommit(t *testing.T) {
	e := newEnv(t)
	got, err := e.reg.Get(e.ctx, "editor.lawcraft_commit")
	if err != nil || got != "770a8fab12392485c0c2789fb254da7d9296df4d" {
		t.Fatalf("editor.lawcraft_commit = %q, %v", got, err)
	}
	var found bool
	for _, d := range settings.Definitions() {
		if d.Key == "editor.lawcraft_commit" {
			found = d.Tab == settings.TabEditor && d.Locked
		}
	}
	if !found {
		t.Fatal("the commit is not a locked setting on the Document editor tab")
	}
	if _, err := e.reg.Set(e.ctx, "editor.lawcraft_commit", "0000000000000000000000000000000000000000", e.admin); err == nil {
		t.Fatal("an admin moved the pinned commit from settings")
	}
	if got, _ := e.reg.Get(e.ctx, "editor.lawcraft_commit"); got != Commit {
		t.Fatalf("after a refused change the commit is %s", got)
	}
}

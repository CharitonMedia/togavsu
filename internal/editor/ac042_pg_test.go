package editor

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// letterWithMergeFields is a DOCX with two MERGEFIELDs, made by LawCraft.
func (e *env) letterWithMergeFields() []byte {
	return e.doc(
		Command{ID: "text.insert", Parameters: map[string]any{"text": "Dear "}},
		Command{ID: "insert.field", Parameters: map[string]any{"instr": "MERGEFIELD ClientName"}},
		Command{ID: "text.insert", Parameters: map[string]any{"text": ", your hearing is on "}},
		Command{ID: "insert.field", Parameters: map[string]any{"instr": "MERGEFIELD HearingDate"}},
		Command{ID: "text.insert", Parameters: map[string]any{"text": "."}},
	)
}

var mergeRun = []Command{
	{ID: "mailings.start", Parameters: map[string]any{"kind": "letters"}},
	{ID: "mailings.recipients", Parameters: map[string]any{"csv": "ClientName,HearingDate\nJane Roe,December 3 2026"}},
	{ID: "mailings.finish"},
}

// matterLetter is a DOCX letter with MERGEFIELDs for the matter's client, case number
// and county, made by LawCraft.
func (e *env) matterLetter() []byte {
	return e.doc(
		Command{ID: "text.insert", Parameters: map[string]any{"text": "Dear "}},
		Command{ID: "insert.field", Parameters: map[string]any{"instr": "MERGEFIELD ClientName"}},
		Command{ID: "text.insert", Parameters: map[string]any{"text": ", case "}},
		Command{ID: "insert.field", Parameters: map[string]any{"instr": "MERGEFIELD CaseNumber"}},
		Command{ID: "text.insert", Parameters: map[string]any{"text": " in "}},
		Command{ID: "insert.field", Parameters: map[string]any{"instr": "MERGEFIELD County"}},
		Command{ID: "text.insert", Parameters: map[string]any{"text": " County is set."}},
	)
}

// matterMergeRun is an editor run whose merge values are the synthetic matter's own, read
// through the editor's Matters as the caller (the client party, case number and county).
func (e *env) matterMergeRun(p domain.Principal, matterID domain.ID) ([]Command, MatterInfo) {
	e.t.Helper()
	m, err := e.svc.matters.Matter(e.ctx, p, matterID)
	if err != nil {
		e.t.Fatal(err)
	}
	client := ""
	for _, party := range m.Parties {
		if party.Role == "client" {
			client = party.Name
			break
		}
	}
	if client == "" || m.CaseNumber == "" || m.County == "" {
		e.t.Fatalf("the synthetic matter lacks merge values: %+v", m)
	}
	csv := "ClientName,CaseNumber,County\n" + client + "," + m.CaseNumber + "," + m.County
	return []Command{
		{ID: "mailings.start", Parameters: map[string]any{"kind": "letters"}},
		{ID: "mailings.recipients", Parameters: map[string]any{"csv": csv}},
		{ID: "mailings.finish"},
	}, m
}

// AC-042, first clause (AC-042-P1): an editor run fills a document's merge fields with the
// matter's values and saves a new version; the prior version is unchanged.
func TestPGEditorRunFillsMergeFieldsAsANewVersion(t *testing.T) {
	e := newEnv(t)
	letter := e.matterLetter()
	f := e.create(e.staff, "Case letter.docx", e.matter, letter, false)
	run, m := e.matterMergeRun(e.staff, e.matter)
	want := "Dear Jane Roe, case " + m.CaseNumber + " in " + m.County + " County is set."
	if txt := e.text(letter, "docx"); strings.Contains(txt, "Jane Roe") || strings.Contains(txt, m.CaseNumber) {
		t.Fatalf("the template already holds the values: %q", txt)
	}
	res, err := e.svc.Run(e.ctx, e.staff, f.ID, RunRequest{Commands: run, SaveAs: "version"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version.Number != 2 || res.Version.How != "editor_run" || res.File.ID != f.ID || res.Version.SavedBy != e.staff.UserID {
		t.Fatalf("run result = %+v", res)
	}
	if !strings.Contains(res.Version.Note, "mailings.recipients") {
		t.Fatalf("version note = %q", res.Version.Note)
	}
	_, cur, b := e.current(f.ID)
	if cur.ID != res.Version.ID {
		t.Fatalf("current version = %s, run saved %s", cur.ID, res.Version.ID)
	}
	if err := CheckDOCX(b); err != nil {
		t.Fatal(err)
	}
	if txt := e.text(b, "docx"); !strings.Contains(txt, want) {
		t.Fatalf("merged text = %q, want the matter's values %q", txt, want)
	}
	// The prior version keeps its bytes and hash, and still has the unfilled fields.
	e.versionUnchanged(f.ID, letter)
	if txt := e.text(e.versionBytes(f.ID, e.versions(f.ID)[0].ID), "docx"); strings.Contains(txt, m.CaseNumber) {
		t.Fatalf("version 1 holds the merged values: %q", txt)
	}
	rows := e.audits("file", f.ID, "editor_run")
	if len(rows) != 1 || rows[0].ActorID != e.staff.UserID {
		t.Fatalf("editor run audit = %+v", rows)
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(rows[0].NewValue), &summary); err != nil || summary["version"] != string(res.Version.ID) {
		t.Fatalf("audit summary = %s (%v)", rows[0].NewValue, err)
	}
}

func TestPGEditorRunCanSaveANewFileWithTheSourcesAccess(t *testing.T) {
	e := newEnv(t)
	src := e.create(e.attorney, "Fee agreement.docx", e.matter, e.letterWithMergeFields(), true)
	res, err := e.svc.Run(e.ctx, e.attorney, src.ID, RunRequest{Commands: mergeRun, SaveAs: "new_file", FileName: "Fee agreement - Roe"})
	if err != nil {
		t.Fatal(err)
	}
	if res.File.ID == src.ID || res.File.Name != "Fee agreement - Roe.docx" || !res.File.AttorneyOnly || res.File.MatterID == nil || *res.File.MatterID != e.matter {
		t.Fatalf("new file = %+v", res.File)
	}
	if res.Version.Number != 1 || res.Version.How != "editor_run" || res.File.Source != "editor" {
		t.Fatalf("new version = %+v (source %s)", res.Version, res.File.Source)
	}
	// The attorney-only result is as hidden as its source.
	if _, err := e.files.Get(e.ctx, e.staff, res.File.ID); code(err) != domain.CodeNotFound {
		t.Fatalf("staff see the attorney-only run result: %v", err)
	}
	if n := len(e.versions(src.ID)); n != 1 {
		t.Fatalf("a new-file run changed the source: %d versions", n)
	}
}

// AC-042, second clause: a run that tries to read a local file is refused, and nothing
// runs or saves.
func TestPGEditorRunThatReadsALocalFileIsRefused(t *testing.T) {
	e := newEnv(t)
	f := e.create(e.staff, "Letter.docx", e.matter, e.letterWithMergeFields(), false)
	for name, cmds := range map[string][]Command{
		"text from a local file": {{ID: "insert.textFromFile", Parameters: map[string]any{"path": "/etc/hostname"}}},
		"open a local file":      {{ID: "file.open", Parameters: map[string]any{"path": "/etc/passwd"}}},
		"recipients from a file": {mergeRun[0], {ID: "mailings.recipients", Parameters: map[string]any{"path": "/etc/passwd"}}, mergeRun[2]},
		"merge written to disk":  {mergeRun[0], mergeRun[1], {ID: "mailings.finish", Parameters: map[string]any{"path": "/tmp/merged.docx"}}},
		"a picture from disk":    {{ID: "insert.picture", Parameters: map[string]any{"path": "/etc/hostname"}}},
		"check for updates":      {{ID: "file.checkForUpdates"}},
		"compare with a file":    {{ID: "review.compare", Parameters: map[string]any{"path": "/etc/hostname"}}},
	} {
		_, err := e.svc.Run(e.ctx, e.staff, f.ID, RunRequest{Commands: cmds, SaveAs: "version"})
		if code(err) != CodeCommandRefused || Status(err) != 422 {
			t.Errorf("%s: Run = %v, want command_refused", name, err)
		}
	}
	if n := len(e.versions(f.ID)); n != 1 {
		t.Fatalf("a refused run saved: %d versions", n)
	}
	_, _, b := e.current(f.ID)
	if host := e.text(b, "docx"); strings.Contains(host, "root:") {
		t.Fatal("a local file reached the document")
	}
	rows := e.audits("file", f.ID, "editor_run")
	if len(rows) != 7 {
		t.Fatalf("refused runs audited %d times, want 7", len(rows))
	}
	for _, r := range rows {
		if !strings.Contains(r.NewValue, `"refused"`) {
			t.Fatalf("audit row without the refusal: %s", r.NewValue)
		}
	}
	// Someone who cannot write the file learns nothing from a refusal and leaves no row.
	if _, err := e.svc.Run(e.ctx, e.client, f.ID, RunRequest{Commands: []Command{{ID: "file.open"}}, SaveAs: "version"}); code(err) != domain.CodeForbidden {
		t.Fatalf("client run = %v", err)
	}
	if n := len(e.audits("file", f.ID, "editor_run")); n != 7 {
		t.Fatalf("a client's refused run was audited")
	}
}

func TestPGEditorRunFailuresAreReportedAndAudited(t *testing.T) {
	e := newEnv(t)
	f := e.create(e.staff, "Letter.docx", e.matter, e.letterWithMergeFields(), false)
	_, err := e.svc.Run(e.ctx, e.staff, f.ID, RunRequest{SaveAs: "version", Commands: []Command{{ID: "law.pleadings.new", Parameters: map[string]any{"type": "motion"}}}})
	if code(err) != domain.CodeValidation || !strings.Contains(err.Error(), "law.pleadings.new") {
		t.Fatalf("a failing command = %v", err)
	}
	rows := e.audits("file", f.ID, "editor_run")
	if len(rows) != 1 || !strings.Contains(rows[0].NewValue, `"failed"`) {
		t.Fatalf("failed run audit = %+v", rows)
	}
	if _, err := e.svc.Run(e.ctx, e.staff, f.ID, RunRequest{SaveAs: "overwrite", Commands: mergeRun}); code(err) != domain.CodeValidation {
		t.Fatalf("save_as overwrite = %v", err)
	}
	e.set("editor.run_timeout", "1s")
	e.set("editor.max_runs", "1")
	lim, err := e.svc.Limits(e.ctx)
	if err != nil || lim.Concurrent != 1 || lim.Timeout.Seconds() != 1 || lim.MemoryBytes != 1<<30 {
		t.Fatalf("limits = %+v, %v", lim, err)
	}
}

// AC-042, last clause: a conversion to PDF makes a new file linked to its source.
func TestPGConversionToPDFIsANewLinkedFile(t *testing.T) {
	e := newEnv(t)
	petition := e.doc(Command{ID: "text.insert", Parameters: map[string]any{"text": "Synthetic petition text."}})
	src := e.create(e.staff, "Petition.docx", e.matter, petition, false)
	e.versionUnchanged(src.ID, petition)
	res, err := e.svc.Convert(e.ctx, e.staff, src.ID, "pdf")
	if err != nil {
		t.Fatal(err)
	}
	if res.File.ID == src.ID || res.File.Name != "Petition.pdf" || res.File.Extension != "pdf" || res.File.MatterID == nil || *res.File.MatterID != e.matter {
		t.Fatalf("converted file = %+v", res.File)
	}
	if res.Link.FromFileID != res.File.ID || res.Link.ToFileID != src.ID || res.Link.Relationship != "conversion_of" {
		t.Fatalf("link = %+v", res.Link)
	}
	links, err := e.files.Links(e.ctx, e.staff, src.ID)
	if err != nil || len(links) != 1 || links[0].ID != res.Link.ID {
		t.Fatalf("source links = %+v, %v", links, err)
	}
	_, v, pdf := e.current(res.File.ID)
	if v.How != "conversion" || v.MediaType != "application/pdf" || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("pdf version = %+v", v)
	}
	if txt := e.text(pdf, "pdf"); !strings.Contains(txt, "Synthetic petition text.") {
		t.Fatalf("the PDF text = %q", txt)
	}
	// AC-042-F2: the source's hash is the one recorded before the conversion.
	if n := len(e.versions(src.ID)); n != 1 {
		t.Fatalf("converting changed the source: %d versions", n)
	}
	e.versionUnchanged(src.ID, petition)
	if rows := e.audits("file", src.ID, "conversion"); len(rows) != 1 {
		t.Fatalf("conversion audit = %+v", rows)
	}
	// A second conversion does not clash with the first file's name.
	again, err := e.svc.Convert(e.ctx, e.staff, src.ID, "pdf")
	if err != nil || again.File.Name != "Petition-2.pdf" {
		t.Fatalf("second conversion = %+v, %v", again.File, err)
	}
	e.versionUnchanged(src.ID, petition)
}

func TestPGConversionsToEveryFormatAndTheirRestrictions(t *testing.T) {
	e := newEnv(t)
	src := e.create(e.attorney, "Notice.docx", e.matter, e.doc(Command{ID: "text.insert", Parameters: map[string]any{"text": "Synthetic notice."}}), true)
	for format, ext := range map[string]string{"odt": "odt", "rtf": "rtf", "html": "html", "markdown": "md", "text": "txt"} {
		res, err := e.svc.Convert(e.ctx, e.attorney, src.ID, format)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if res.File.Extension != ext || !res.File.AttorneyOnly {
			t.Fatalf("%s: %+v", format, res.File)
		}
		_, _, b := e.current(res.File.ID)
		if txt := e.text(b, ext); !strings.Contains(txt, "Synthetic notice.") {
			t.Fatalf("%s text = %q", format, txt)
		}
	}
	md := e.create(e.staff, "Memo.md", e.matter, []byte("# Memo\n\nSynthetic memo.\n"), false)
	res, err := e.svc.Convert(e.ctx, e.staff, md.ID, "docx")
	if err != nil {
		t.Fatal(err)
	}
	_, _, docx := e.current(res.File.ID)
	if err := CheckDOCX(docx); err != nil {
		t.Fatalf("markdown to DOCX is not Word-compatible: %v", err)
	}
	pdf := e.create(e.staff, "Scan.pdf", e.matter, []byte("%PDF-1.4\n%%EOF\n"), false)
	for name, c := range map[string]struct {
		file   domain.ID
		format string
	}{
		"same format":       {src.ID, "docx"},
		"unknown format":    {src.ID, "exe"},
		"not an editor doc": {pdf.ID, "docx"},
	} {
		if _, err := e.svc.Convert(e.ctx, e.attorney, c.file, c.format); code(err) != domain.CodeValidation {
			t.Errorf("%s: Convert = %v", name, err)
		}
	}
	if _, err := e.svc.Convert(e.ctx, e.staff, src.ID, "pdf"); code(err) != domain.CodeNotFound {
		t.Fatalf("staff converted an attorney-only file: %v", err)
	}
}

// Track changes and comments stay inside the DOCX through an editor run.
func TestPGTrackChangesAndCommentsSurviveARun(t *testing.T) {
	e := newEnv(t)
	tracked := e.doc(
		Command{ID: "text.insert", Parameters: map[string]any{"text": "Original clause."}},
		Command{ID: "review.trackChanges", Parameters: map[string]any{"value": true}},
		Command{ID: "text.insert", Parameters: map[string]any{"text": " Tracked insertion."}},
		Command{ID: "select.all"},
		Command{ID: "review.newComment", Parameters: map[string]any{"text": "Synthetic reviewer comment."}},
	)
	f := e.create(e.staff, "Redline.docx", e.matter, tracked, false)
	if _, err := e.svc.Run(e.ctx, e.staff, f.ID, RunRequest{SaveAs: "version", Commands: []Command{{ID: "law.symbols.fixSpacing", Parameters: map[string]any{"scope": "document"}}}}); err != nil {
		t.Fatal(err)
	}
	_, _, b := e.current(f.ID)
	parts := map[string]string{}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	for _, zf := range zr.File {
		rc, _ := zf.Open()
		raw, _ := io.ReadAll(rc)
		_ = rc.Close()
		parts[zf.Name] = string(raw)
	}
	if !strings.Contains(parts["word/document.xml"], "<w:ins ") || !strings.Contains(parts["word/document.xml"], "Tracked insertion.") {
		t.Fatal("the tracked insertion did not survive the run")
	}
	if !strings.Contains(parts["word/comments.xml"], "Synthetic reviewer comment.") || !strings.Contains(parts["word/document.xml"], "commentRangeStart") {
		t.Fatal("the comment did not survive the run")
	}
	if err := CheckDOCX(b); err != nil {
		t.Fatal(err)
	}
}

func TestPGPDFPreviewForEachEditableVersion(t *testing.T) {
	e := newEnv(t)
	f := e.create(e.staff, "Brief.docx", e.matter, e.doc(), false)
	made, err := e.svc.Preview(e.ctx, e.staff, f.ID, *f.CurrentVersionID)
	if err != nil || !made {
		t.Fatalf("preview = %v, %v", made, err)
	}
	rs, err := e.files.Renditions(e.ctx, e.staff, f.ID)
	if err != nil || len(rs) != 1 || rs[0].Kind != "pdf_preview" || rs[0].FileVersionID != *f.CurrentVersionID {
		t.Fatalf("renditions = %+v, %v", rs, err)
	}
	e.set("editor.pdf_previews", "false")
	if made, err := e.svc.Preview(e.ctx, e.staff, f.ID, *f.CurrentVersionID); err != nil || made {
		t.Fatalf("preview with previews off = %v, %v", made, err)
	}
}

package editor

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// A court profile body naming a file is refused before LawCraft sees it: the command it
// is checked with, law.profiles.validate, reads any file named by {"path": …}.
func TestPGCourtProfileBodyCannotMakeLawCraftReadAFile(t *testing.T) {
	e := newEnv(t)
	// The way in this closes: given a path, LawCraft reads the file the core can read.
	raw, err := json.Marshal(profileBody(t, "from-a-file"))
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "core-file.json")
	if err := os.WriteFile(local, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.cli.Run(e.ctx, RunInput{Commands: []Command{{ID: "law.profiles.validate", Parameters: map[string]any{"path": local}}}, Out: "txt"}); err != nil {
		t.Fatalf("LawCraft at the pin no longer reads a profile path, so this test should be reviewed: %v", err)
	}

	for _, body := range []map[string]any{
		{"path": local},
		{"schema": 1.0, "id": "x", "name": "x", "path": "/etc/hostname"},
		func() map[string]any { b := profileBody(t, "with-a-path"); b["path"] = local; return b }(),
	} {
		if _, err := e.svc.CreateProfile(e.ctx, e.staff, ProfileWrite{Name: ptr("Leak"), Body: body}); code(err) != domain.CodeValidation || !strings.Contains(err.Error(), "cannot name a file") {
			t.Fatalf("a profile naming a file was not refused: %v", err)
		}
	}
	ok, err := e.svc.CreateProfile(e.ctx, e.staff, ProfileWrite{Name: ptr("Good"), Body: profileBody(t, "good")})
	if err != nil {
		t.Fatal(err)
	}
	bad := profileBody(t, "good")
	bad["path"] = local
	if _, err := e.svc.UpdateProfile(e.ctx, e.staff, ok.ID, ProfileWrite{Body: bad}); code(err) != domain.CodeValidation {
		t.Fatalf("an update naming a file = %v", err)
	}
	all, err := e.svc.Profiles(e.ctx, e.staff)
	if err != nil || len(all) != 1 || all[0].Body["path"] != nil {
		t.Fatalf("profiles after refusals = %+v, %v", all, err)
	}
}

// A run given an address is refused unless the command only stores it (insert.link).
func TestPGEditorRunGivenAnAddressIsRefused(t *testing.T) {
	e := newEnv(t)
	f := e.create(e.staff, "Letter.docx", e.matter, e.doc(), false)
	for _, cmds := range [][]Command{
		{{ID: "text.insert", Parameters: map[string]any{"text": "x", "url": "https://example.invalid/"}}},
		{{ID: "caret.docEnd"}, {ID: "text.insert", Parameters: map[string]any{"text": "x", "opts": map[string]any{"src": "http://127.0.0.1:9/"}}}},
	} {
		if _, err := e.svc.Run(e.ctx, e.staff, f.ID, RunRequest{SaveAs: "version", Commands: cmds}); !isRefused(err) {
			t.Fatalf("a run with an address = %v", err)
		}
	}
	if n := len(e.versions(f.ID)); n != 1 {
		t.Fatalf("refused runs made versions: %d", n)
	}
	res, err := e.svc.Run(e.ctx, e.staff, f.ID, RunRequest{SaveAs: "version", Commands: []Command{
		{ID: "caret.docEnd"}, {ID: "insert.link", Parameters: map[string]any{"url": "https://example.invalid/court", "text": "the court"}},
	}})
	if err != nil || res.Version.Number != 2 {
		t.Fatalf("a hyperlink run = %+v, %v", res.Version, err)
	}
	_, _, stored := e.current(f.ID)
	if !bytes.Contains(unzipPart(t, stored, "word/_rels/document.xml.rels"), []byte("https://example.invalid/court")) {
		t.Fatal("the hyperlink's target is not in the saved document")
	}
	rows := e.audits("file", f.ID, "editor_run")
	if len(rows) != 3 || !strings.Contains(rows[0].NewValue, `"refused"`) || !strings.Contains(rows[1].NewValue, `"refused"`) {
		t.Fatalf("editor_run audit = %+v", rows)
	}
}

// Every run and conversion reaches the audit log, including those that fail after
// LawCraft finished: here the save itself fails.
func TestPGRunsAndConversionsThatFailAfterLawCraftAreAudited(t *testing.T) {
	e := newEnv(t)
	f := e.create(e.staff, "Letter.docx", e.matter, e.letterWithMergeFields(), false)
	// The file goes to the trash between the run and its save: SaveVersion refuses.
	e.svc.beforeLockedWrite = func() {
		if _, err := e.files.Trash(e.ctx, e.admin, f.ID); err != nil {
			t.Error(err)
		}
	}
	if _, err := e.svc.Run(e.ctx, e.staff, f.ID, RunRequest{SaveAs: "version", Commands: mergeRun}); code(err) != domain.CodeConflict {
		t.Fatalf("a run whose save failed = %v", err)
	}
	e.svc.beforeLockedWrite = nil
	rows := e.audits("file", f.ID, "editor_run")
	if len(rows) != 1 || !strings.Contains(rows[0].NewValue, `"failed"`) || !strings.Contains(rows[0].NewValue, "trash") {
		t.Fatalf("a run whose save failed was not audited: %+v", rows)
	}

	// A conversion whose new file cannot be made: the source's matter is gone from the
	// directory the file store files into.
	src := e.create(e.staff, "Petition.docx", e.matter, e.doc(), false)
	e.dir.mu.Lock()
	gone := e.dir.matters[e.matter]
	delete(e.dir.matters, e.matter)
	e.dir.mu.Unlock()
	_, cerr := e.svc.Convert(e.ctx, e.staff, src.ID, "pdf")
	e.dir.mu.Lock()
	e.dir.matters[e.matter] = gone
	e.dir.mu.Unlock()
	if cerr == nil {
		t.Fatal("a conversion into a missing matter succeeded")
	}
	conv := e.audits("file", src.ID, "conversion")
	if len(conv) != 1 || !strings.Contains(conv[0].NewValue, `"failed"`) {
		t.Fatalf("a conversion whose file could not be made was not audited: %+v (%v)", conv, cerr)
	}
}

// waitBlocked runs fn in the background and reports whether it is still running after a
// moment, then returns a function that waits for its result.
func waitBlocked(t *testing.T, fn func() error) (blocked bool, wait func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return false, func() error { return err }
	case <-time.After(300 * time.Millisecond):
		return true, func() error {
			select {
			case err := <-done:
				return err
			case <-time.After(30 * time.Second):
				t.Fatal("the waiting call never finished")
				return nil
			}
		}
	}
}

// The lock check and the version write are one step: a lock that lapses between them
// cannot be taken until the save is in, so the new holder never edits under it.
func TestPGSaveHoldsTheLockUntilTheVersionIsIn(t *testing.T) {
	e := newEnv(t)
	f := e.create(e.staff, "Motion.docx", e.matter, e.doc(), false)
	if _, err := e.svc.AcquireLock(e.ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	var blocked bool
	var wait func() error
	e.svc.beforeLockedWrite = func() {
		// The lock lapses right after the check, and the second user tries to take it.
		e.clock.advance(6 * time.Minute)
		blocked, wait = waitBlocked(t, func() error { _, err := e.svc.AcquireLock(e.ctx, e.other, f.ID); return err })
	}
	edited := e.doc(Command{ID: "text.insert", Parameters: map[string]any{"text": "Saved by the first user."}})
	v, err := e.svc.Save(e.ctx, e.staff, f.ID, "Motion.docx", edited)
	e.svc.beforeLockedWrite = nil
	if err != nil || v.Number != 2 {
		t.Fatalf("save = %+v, %v", v, err)
	}
	if !blocked {
		t.Fatalf("the second user took the lock between the check and the write: %v", wait())
	}
	// The save renewed the first user's lock in the same step, so it is still theirs.
	if err := wait(); code(err) != CodeEditLockHeld {
		t.Fatalf("after the save the second user = %v", err)
	}

	// A run saved as a version with nobody holding the lock: an AcquireLock during the
	// write waits too, and then opens the run's version.
	g := e.create(e.staff, "Letter.docx", e.matter, e.letterWithMergeFields(), false)
	e.svc.beforeLockedWrite = func() {
		blocked, wait = waitBlocked(t, func() error { _, err := e.svc.AcquireLock(e.ctx, e.other, g.ID); return err })
	}
	res, err := e.svc.Run(e.ctx, e.staff, g.ID, RunRequest{SaveAs: "version", Commands: mergeRun})
	e.svc.beforeLockedWrite = nil
	if err != nil || res.Version.Number != 2 {
		t.Fatalf("run = %+v, %v", res.Version, err)
	}
	if !blocked {
		t.Fatalf("the lock was taken while the run's version was written: %v", wait())
	}
	if err := wait(); err != nil {
		t.Fatalf("the lock after the run = %v", err)
	}
	_, opened, err := e.svc.Document(e.ctx, e.other, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if txt := e.text(opened, "docx"); !strings.Contains(txt, "Dear Jane Roe") {
		t.Fatalf("the new holder opened %q, not the run's version", txt)
	}
	// The run left no lock row of its own behind.
	if _, err := e.svc.RenewLock(e.ctx, e.other, g.ID); err != nil {
		t.Fatalf("the new holder's lock = %v", err)
	}
}

// unzipPart reads one part of a DOCX.
func unzipPart(t *testing.T, b []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			out, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
	}
	t.Fatalf("no part %s", name)
	return nil
}

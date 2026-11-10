package files

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// holdRow locks a files row in a separate transaction, runs op in the background,
// waits until op is blocked on that lock, then commits change (a concurrent save or
// edit) and returns op's error. Without FOR UPDATE on op's read, op writes back the
// row it read before change committed.
func holdRow(t *testing.T, e *env, file domain.ID, change string, args []any, op func() error) error {
	t.Helper()
	ctx := context.Background()
	tx, err := e.pool.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT id FROM files WHERE id = $1 FOR UPDATE`, file); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- op() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_stat_activity
WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("op finished without waiting for the row lock: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("op never waited for the row lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := tx.ExecContext(ctx, change, args...); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return <-done
}

func fileState(t *testing.T, e *env, id domain.ID) (current, locked, name, folder string) {
	t.Helper()
	var f sql.NullString
	if err := e.pool.QueryRowContext(context.Background(), `SELECT current_version_id, locked_fields, name, folder FROM files WHERE id = $1`, id).
		Scan(&current, &locked, &name, &f); err != nil {
		t.Fatal(err)
	}
	return current, locked, name, f.String
}

// TestPGEditsDoNotOverwriteAConcurrentCommit is checkpoint finding 1: ApplyTender,
// the change() edits and UpdateMetadata read the row FOR UPDATE, so a version or a
// person's lock committed while they wait is kept.
func TestPGEditsDoNotOverwriteAConcurrentCommit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sys := domain.Principal{Source: domain.SourceSystem, UserID: e.admin.UserID}
	evidence, err := e.svc.FolderByName(ctx, e.staff, e.open, "Evidence")
	if err != nil {
		t.Fatal(err)
	}
	f := e.create(e.staff, NewFile{Name: "scan.pdf", MatterID: e.open}, "v1")
	v2, err := e.svc.SaveVersion(ctx, e.staff, f.ID, strings.NewReader("v2"), 2, SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	v1 := string(*f.CurrentVersionID)

	// Doc Tender files the document while a save (here: the current version moving)
	// and a person's move (folder plus its lock) commit.
	err = holdRow(t, e, f.ID, `UPDATE files SET current_version_id = $1, folder = $2, locked_fields = '["folder"]' WHERE id = $3`,
		[]any{v1, string(evidence.ID), f.ID}, func() error {
			_, err := e.svc.ApplyTender(ctx, sys, f.ID, TenderUpdate{Name: "2026-11-10 Scan.pdf", Folder: "Correspondence"})
			return err
		})
	if err != nil {
		t.Fatal(err)
	}
	cur, locked, name, folder := fileState(t, e, f.ID)
	if cur != v1 || !strings.Contains(locked, "folder") || folder != string(evidence.ID) || name != "2026-11-10 Scan.pdf" {
		t.Fatalf("after tender: current=%s (want %s) locked=%s folder=%s name=%s", cur, v1, locked, folder, name)
	}

	// A rename (change) racing a save keeps the saved current version.
	err = holdRow(t, e, f.ID, `UPDATE files SET current_version_id = $1 WHERE id = $2`, []any{string(v2.ID), f.ID}, func() error {
		_, err := e.svc.Rename(ctx, e.staff, f.ID, "renamed.pdf")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	cur, locked, name, _ = fileState(t, e, f.ID)
	if cur != string(v2.ID) || name != "renamed.pdf" || !strings.Contains(locked, "folder") || !strings.Contains(locked, "name") {
		t.Fatalf("after rename: current=%s locked=%s name=%s", cur, locked, name)
	}

	// UpdateMetadata racing a person's rename keeps the new name and its lock.
	yes := true
	err = holdRow(t, e, f.ID, `UPDATE files SET name = 'person.pdf', current_version_id = $1 WHERE id = $2`, []any{v1, f.ID}, func() error {
		_, err := e.svc.UpdateMetadata(ctx, e.staff, f.ID, Metadata{SharedWithClient: &yes})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	cur, _, name, _ = fileState(t, e, f.ID)
	if cur != v1 || name != "person.pdf" {
		t.Fatalf("after metadata: current=%s name=%s", cur, name)
	}

	// Purge rechecks under the lock: a Restore committed while it waits wins.
	if _, err := e.svc.Trash(ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	err = holdRow(t, e, f.ID, `UPDATE files SET trashed_at = 0, trashed_by = NULL WHERE id = $1`, []any{f.ID}, func() error {
		return e.svc.Purge(ctx, e.admin, f.ID, "person.pdf")
	})
	if !isCode(err, domain.CodeConflict) {
		t.Fatalf("purge over a restore = %v", err)
	}
	if got, err := e.svc.Get(ctx, e.staff, f.ID); err != nil || got.TrashedAt != nil {
		t.Fatalf("restored file = %+v %v", got, err)
	}
}

// TestPGTenderRacingSavesKeepsTheNewestVersion races real saves and Doc Tender
// writes on one file: the newest version stays current.
func TestPGTenderRacingSavesKeepsTheNewestVersion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sys := domain.Principal{Source: domain.SourceSystem, UserID: e.admin.UserID}
	f := e.create(e.staff, NewFile{Name: "draft.docx", MatterID: e.open}, "v1")
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			body := strings.Repeat("x", i+2)
			_, err := e.svc.SaveVersion(ctx, e.staff, f.ID, strings.NewReader(body), int64(len(body)), SaveOptions{How: "editor"})
			errs <- err
		}(i)
		go func(i int) {
			defer wg.Done()
			_, err := e.svc.ApplyTender(ctx, sys, f.ID, TenderUpdate{Name: "draft-" + string(rune('a'+i)) + ".docx"})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	vs, err := e.svc.Versions(ctx, e.staff, f.ID)
	if err != nil || len(vs) != 11 {
		t.Fatalf("versions = %d %v", len(vs), err)
	}
	got, err := e.svc.Get(ctx, e.staff, f.ID)
	if err != nil || *got.CurrentVersionID != vs[len(vs)-1].ID {
		t.Fatalf("current = %v, newest = %s (%v)", got.CurrentVersionID, vs[len(vs)-1].ID, err)
	}
}

// TestPGFoldersFollowRoleAndClientScope is checkpoint finding 2: a client sees only
// their own matter's folders and Files tab, an accountant sees none, and neither
// creates folder rows.
func TestPGFoldersFollowRoleAndClientScope(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	count := func(matter domain.ID) int {
		var n int
		_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM folders WHERE matter_id = $1`, matter).Scan(&n)
		return n
	}
	// Readers who cannot write never seed folders, even on a matter they may read.
	if got, err := e.svc.Folders(ctx, e.client, e.open); err != nil || len(got) != 0 || count(e.open) != 0 {
		t.Fatalf("client first open of own matter = %v %v rows=%d", got, err, count(e.open))
	}
	for _, who := range []domain.Principal{e.client, e.accountant} {
		for _, m := range []domain.ID{e.closed, e.open} {
			if who.HasRole(domain.RoleClient) && m == e.open {
				continue
			}
			if _, err := e.svc.Folders(ctx, who, m); !isCode(err, domain.CodeNotFound) {
				t.Fatalf("%v folders of %s = %v", who.Roles, m, err)
			}
		}
	}
	if count(e.closed) != 0 {
		t.Fatal("a refused reader created folders")
	}
	staffFolders, err := e.svc.Folders(ctx, e.staff, e.open)
	if err != nil || len(staffFolders) == 0 {
		t.Fatalf("staff folders = %v %v", staffFolders, err)
	}
	closedFolders, err := e.svc.Folders(ctx, e.staff, e.closed)
	if err != nil || len(closedFolders) == 0 {
		t.Fatal(err)
	}
	if got, err := e.svc.Folders(ctx, e.client, e.open); err != nil || len(got) != len(staffFolders) {
		t.Fatalf("client own matter folders = %v %v", got, err)
	}
	if _, err := e.svc.GetFolder(ctx, e.client, staffFolders[0].ID); err != nil {
		t.Fatalf("client own folder = %v", err)
	}
	for _, who := range []domain.Principal{e.client, e.accountant} {
		if _, err := e.svc.GetFolder(ctx, who, closedFolders[0].ID); !isCode(err, domain.CodeNotFound) {
			t.Fatalf("%v other matter folder = %v", who.Roles, err)
		}
		if err := e.svc.Archive(ctx, who, nil, closedFolders[0].ID, true, io.Discard); !isCode(err, domain.CodeNotFound) {
			t.Fatalf("%v other matter folder zip = %v", who.Roles, err)
		}
	}
	if _, err := e.svc.GetFolder(ctx, e.accountant, staffFolders[0].ID); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("accountant folder = %v", err)
	}

	// The same over the API and on the Files tab, which shows the matter number.
	for _, c := range []struct {
		who  domain.Principal
		path string
		want int
	}{
		{e.client, "/api/v1/folders?matter_id=" + string(e.closed), http.StatusNotFound},
		{e.client, "/api/v1/folders/" + string(closedFolders[0].ID), http.StatusNotFound},
		{e.accountant, "/api/v1/folders?matter_id=" + string(e.open), http.StatusNotFound},
		{e.client, "/api/v1/folders?matter_id=" + string(e.open), http.StatusOK},
	} {
		who := c.who
		srv := e.server(func(*http.Request) (domain.Principal, error) { return who, nil })
		resp, err := http.Get(srv.URL + c.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		srv.Close()
		if resp.StatusCode != c.want || (c.want == http.StatusNotFound && strings.Contains(string(body), "Correspondence")) {
			t.Fatalf("%v GET %s = %d %s", who.Roles, c.path, resp.StatusCode, body)
		}
	}
	page := func(who domain.Principal, matter domain.ID) (int, string) {
		h, err := NewHandler(e.svc, os.DirFS("../../web/templates/files"), func(*http.Request) (domain.Principal, error) { return who, nil })
		if err != nil {
			t.Fatal(err)
		}
		srv := h.Routes()
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/practice/matters/"+string(matter)+"/files", nil))
		return rec.Code, rec.Body.String()
	}
	for _, c := range []struct {
		who    domain.Principal
		matter domain.ID
	}{{e.client, e.closed}, {e.accountant, e.open}, {e.accountant, e.closed}} {
		if code, body := page(c.who, c.matter); code != http.StatusNotFound || strings.Contains(body, "2025-0099") || strings.Contains(body, "2026-0001") {
			t.Fatalf("%v files tab of %s = %d %s", c.who.Roles, c.matter, code, body)
		}
	}
	if code, body := page(e.client, e.open); code != http.StatusOK || !strings.Contains(body, "2026-0001") {
		t.Fatalf("client own files tab = %d", code)
	}
}

// TestPGDefaultFoldersPerPracticeArea is checkpoint finding 4: a matter's folders
// come from its practice area's default_folders, else files.default_folders.
func TestPGDefaultFoldersPerPracticeArea(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	family := id(t)
	e.dir.mu.Lock()
	e.dir.areas = map[domain.ID][]string{family: {"Pleadings", "Financial disclosures", "Parenting plan"}}
	m := e.dir.matters[e.open]
	m.PracticeAreaID = family
	e.dir.matters[e.open] = m
	e.dir.mu.Unlock()
	names := func(fs []FolderView) string {
		var out []string
		for _, f := range fs {
			out = append(out, f.Name)
		}
		return strings.Join(out, ",")
	}
	got, err := e.svc.Folders(ctx, e.staff, e.open)
	if err != nil || names(got) != "Pleadings,Financial disclosures,Parenting plan" {
		t.Fatalf("area folders = %s %v", names(got), err)
	}
	e.set("files.default_folders", `["Global A","Global B"]`)
	got, err = e.svc.Folders(ctx, e.staff, e.closed)
	if err != nil || names(got) != "Global A,Global B" {
		t.Fatalf("fallback folders = %s %v", names(got), err)
	}
}

// TestPGDropFolderUndeletableFileIsTakenOnce is checkpoint finding 3: when the share
// refuses the delete, the file is not taken again on later passes or after a
// restart, and the admins get one alert.
func TestPGDropFolderUndeletableFileIsTakenOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	share := filepath.Join(e.root, "share")
	msg := []byte(`{"path":"` + share + `"}`)
	raw := json.RawMessage(msg)
	yes := true
	b, err := e.svc.CreateBackend(ctx, e.admin, BackendWrite{Kind: "network_path", Name: "NAS", DropFolder: &yes, Settings: &raw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ScanDrop(ctx); err != nil {
		t.Fatal(err)
	}
	refuse := func(string) error { return &fs.PathError{Op: "remove", Path: "x", Err: fs.ErrPermission} }
	e.svc.removeDrop = refuse
	dropped := filepath.Join(share, "drop", "2026-0001", "held-open.pdf")
	if err := os.WriteFile(dropped, []byte("signed retainer"), 0o600); err != nil {
		t.Fatal(err)
	}
	taken := 0
	for pass := 0; pass < 6; pass++ {
		got, err := e.svc.ScanDrop(ctx)
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		taken += len(got)
		e.clock.advance(31 * time.Second)
	}
	countDrops := func() int {
		var n int
		_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE source = 'drop_folder'`).Scan(&n)
		return n
	}
	if taken != 1 || countDrops() != 1 {
		t.Fatalf("taken %d times, %d records", taken, countDrops())
	}
	if _, err := os.Stat(dropped); err != nil {
		t.Fatal("the test share should have kept the file")
	}
	alerts, err := e.svc.Alerts(ctx, e.admin)
	if err != nil || len(alerts) != 1 || alerts[0].BackendID != b.ID || !strings.Contains(alerts[0].Detail, "could not remove") || e.mail.count() != 1 {
		t.Fatalf("alerts = %+v %v mails=%d", alerts, err, e.mail.count())
	}

	// After a restart the record in the database still says it was taken; with delete
	// rights back, the file is removed and no second record is made.
	svc := e.reopen()
	for pass := 0; pass < 3; pass++ {
		got, err := svc.ScanDrop(ctx)
		if err != nil || len(got) != 0 {
			t.Fatalf("restart pass %d = %v %v", pass, got, err)
		}
		e.clock.advance(31 * time.Second)
	}
	if _, err := os.Stat(dropped); !os.IsNotExist(err) || countDrops() != 1 {
		t.Fatalf("after restart: stat=%v records=%d", err, countDrops())
	}

	// A changed file under the same name is a new drop.
	if err := os.WriteFile(dropped, []byte("signed retainer, amended"), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []FileView
	for pass := 0; pass < 2; pass++ {
		g, err := svc.ScanDrop(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, g...)
		e.clock.advance(31 * time.Second)
	}
	if len(got) != 1 || countDrops() != 2 {
		t.Fatalf("changed file = %v records=%d", got, countDrops())
	}

	// A top-level drop that could not be removed, then filed to a matter by Doc Tender,
	// is still not taken again.
	svc.removeDrop = refuse
	top := filepath.Join(share, "drop", "unsorted-scan.pdf")
	if err := os.WriteFile(top, []byte("unsorted"), 0o600); err != nil {
		t.Fatal(err)
	}
	got = nil
	for pass := 0; pass < 2; pass++ {
		g, err := svc.ScanDrop(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, g...)
		e.clock.advance(31 * time.Second)
	}
	if len(got) != 1 {
		t.Fatalf("top-level drop = %v", got)
	}
	filed := e.open
	if _, err := svc.UpdateMetadata(ctx, e.staff, got[0].ID, Metadata{MatterID: &filed}); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 3; pass++ {
		if g, err := svc.ScanDrop(ctx); err != nil || len(g) != 0 {
			t.Fatalf("filed top-level drop retaken = %v %v", g, err)
		}
		e.clock.advance(31 * time.Second)
	}
	if countDrops() != 3 {
		t.Fatalf("records = %d", countDrops())
	}
}

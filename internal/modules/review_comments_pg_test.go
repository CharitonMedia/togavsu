package modules_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

// TestPGContactViewFollowsContactWalls: a contact-keyed published view shows a contact's
// rows only when the core contact rule lets the viewer see that contact. A contact that
// is only on a walled matter is hidden from staff outside the wall.
func TestPGContactViewFollowsContactWalls(t *testing.T) {
	e := newEnv(t)
	raw := e.build(func(f map[string][]byte) {
		man := string(f[modules.ManifestFile])
		man = strings.Replace(man, "  views:\n", `  views:
    - name: contact_notes
      record: contact
      key: contact_id
      columns:
        - name: body
`, 1)
		f[modules.ManifestFile] = []byte(man)
		f["migrations/0002_contact_notes.sql"] = []byte(`CREATE TABLE contact_note_rows (
  contact_id TEXT PRIMARY KEY,
  body TEXT NOT NULL
);

CREATE VIEW contact_notes AS
  SELECT n.contact_id, n.body FROM contact_note_rows n;
`)
	})
	if r := e.do("POST", "/api/v1/modules", raw, e.as("admin")...); r.code != http.StatusAccepted {
		t.Fatalf("install: %d %s", r.code, r.body)
	}
	e.approveAll("matter-clock")
	contact := func(name string) domain.Contact {
		c, err := e.app.Practice.CreateContact(e.ctx, e.attorney, practice.ContactInput{Kind: practice.KindPerson, FullName: name})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	loose, onOpen, walledOnly := contact("Loose Contact"), contact("Open Party"), contact("Walled Party")
	for m, c := range map[domain.ID]domain.ID{e.open.ID: onOpen.ID, e.walled.ID: walledOnly.ID} {
		if _, err := e.app.Practice.AddParty(e.ctx, e.attorney, m, practice.PartyInput{ContactID: c, Role: "client", Primary: true}); err != nil {
			t.Fatal(err)
		}
	}
	dep := e.rt.last("matter-clock")
	mdb := e.moduleDB(dep.Secrets["togavsu_db_role"], dep.Secrets["togavsu_db_password"])
	for _, c := range []domain.ID{loose.ID, onOpen.ID, walledOnly.ID} {
		if _, err := mdb.ExecContext(e.ctx, `INSERT INTO contact_note_rows (contact_id, body) VALUES ($1, $2)`, string(c), "note on "+string(c)); err != nil {
			t.Fatal(err)
		}
	}
	ids := []domain.ID{loose.ID, onOpen.ID, walledOnly.ID}
	seen := func(p domain.Principal) map[string]bool {
		rows, err := e.svc.ReadView(e.ctx, p, nil, false, "matter-clock", "contact_notes", ids)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, r := range rows {
			out[r["contact_id"].(string)] = true
		}
		return out
	}
	staff := seen(e.staff)
	if !staff[string(loose.ID)] || !staff[string(onOpen.ID)] || staff[string(walledOnly.ID)] {
		t.Fatalf("staff outside the wall: %v", staff)
	}
	if att := seen(e.attorney); len(att) != len(ids) {
		t.Fatalf("attorney on the wall: %v", att)
	}
}

// TestPGSampleTargetWriteNeedsRole: the sample module saves a target for firm staff but
// only an attorney or admin writes its attorney-only note; staff saves keep the note.
func TestPGSampleTargetWriteNeedsRole(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	e.rt.mu.Lock()
	base := e.rt.servers["matter-clock"].URL
	e.rt.mu.Unlock()
	post := func(who string, form url.Values) int {
		res, err := http.PostForm(base+"/targets?context="+url.QueryEscape(e.contextFor(who, "matter-clock", e.open.ID)), form)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post("attorney", url.Values{"monthly_target_tenths": {"30"}, "note": {"attorney-only note"}}); code != http.StatusNoContent {
		t.Fatalf("attorney save: %d", code)
	}
	if code := post("staff", url.Values{"monthly_target_tenths": {"40"}, "note": {"staff note"}}); code != http.StatusForbidden {
		t.Fatalf("staff writing the note: %d", code)
	}
	if code := post("staff", url.Values{"monthly_target_tenths": {"50"}}); code != http.StatusNoContent {
		t.Fatalf("staff target save: %d", code)
	}
	dep := e.rt.last("matter-clock")
	mdb := e.moduleDB(dep.Secrets["togavsu_db_role"], dep.Secrets["togavsu_db_password"])
	var tenths int
	var note string
	if err := mdb.QueryRowContext(e.ctx, `SELECT monthly_target_tenths, note FROM matter_targets WHERE matter_id = $1`, string(e.open.ID)).Scan(&tenths, &note); err != nil {
		t.Fatal(err)
	}
	if tenths != 50 || note != "attorney-only note" {
		t.Fatalf("after the staff save: %d %q", tenths, note)
	}
}

// TestPGUpdateDropsOmittedPermission: a new version that no longer asks for a scope loses
// its approval, and the module gets a new token, issued from the approvals that remain.
func TestPGUpdateDropsOmittedPermission(t *testing.T) {
	e := newEnv(t)
	e.installSample()
	before := e.rt.last("matter-clock").Secrets["togavsu_api_token"]
	next := e.build(func(f map[string][]byte) {
		man := string(f[modules.ManifestFile])
		man = strings.Replace(man, `version: "1.0.0"`, `version: "1.0.1"`, 1)
		man = strings.Replace(man, "  - scope: time:read\n    reason: Add up the hours worked on each matter this month.\n", "", 1)
		f[modules.ManifestFile] = []byte(man)
	})
	res, err := e.svc.Update(e.ctx, e.admin, "matter-clock", next, "local")
	if err != nil || res.Module.Version != "1.0.1" {
		t.Fatalf("update: %+v %v", res, err)
	}
	v, err := e.svc.Get(e.ctx, e.admin, "matter-clock")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range v.Permissions {
		if p.Scope == "time:read" {
			t.Fatalf("time:read survived an update that dropped it: %+v", v.Permissions)
		}
	}
	// The running module gets a token issued from what stays approved.
	if after := e.rt.last("matter-clock").Secrets["togavsu_api_token"]; after == "" || after == before {
		t.Fatal("the update did not reissue the module token")
	}
}

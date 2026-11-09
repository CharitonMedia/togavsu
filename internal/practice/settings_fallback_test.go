package practice

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// TestPracticeLimitsMatchTheRegistry locks the numbers the settings checker
// accepts in internal/domain/defaults.go to the catalog defaults. A raw
// fallback in practice fails internal/settings TestModuleClean.
func TestPracticeLimitsMatchTheRegistry(t *testing.T) {
	want := map[string]int{
		"practice.conflict_score_minimum": domain.ConflictScoreMinimum,
		"billing.time_increment_minutes":  domain.BillingTimeIncrementMinutes,
		"search.min_chars":                domain.SearchMinChars,
		"search.hits_all":                 domain.SearchHitsAll,
		"search.hits_scope":               domain.SearchHitsScope,
		"activity.window_days":            domain.ActivityWindowDays,
		"activity.page_size":              domain.ActivityPageSize,
		"notes.page_size":                 domain.NotesPageSize,
	}
	got := map[string]string{}
	for _, def := range settings.Definitions() {
		got[def.Key] = def.Default
	}
	for key, n := range want {
		if got[key] != strconv.Itoa(n) {
			t.Fatalf("%s registry default %q, constant %d", key, got[key], n)
		}
	}
}

// TestNilRegistryUsesCatalogLimits checks the path that runs when the registry
// is missing. Those fallbacks are the domain constants, so the results match
// a service that reads the catalog defaults.
func TestNilRegistryUsesCatalogLimits(t *testing.T) {
	ctx := context.Background()
	bareDB := NewMemory()
	bare := New(bareDB, nil)
	catDB := NewMemory()
	cat, err := NewWithSettings(catDB)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC)
	bare.now = func() time.Time { return when }
	cat.now = func() time.Time { return when }
	var n uint64
	next := func() (domain.ID, error) {
		n++
		return domain.ComposeV7(1_700_000_000_000+n, uint16(n%4000), n+1)
	}
	bare.newID = next
	cat.newID = next
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, bareDB, admin, "Ada Admin")
	putUser(t, bareDB, staff, "Sam Staff")
	putUser(t, catDB, admin, "Ada Admin")
	putUser(t, catDB, staff, "Sam Staff")

	for _, svc := range []*Service{bare, cat} {
		if _, err := svc.Search(ctx, admin, "a", ScopePeople); !isCode(err, domain.CodeValidation) {
			t.Fatalf("one character searched: %v", err)
		}
		if _, err := svc.Search(ctx, admin, "ab", ScopePeople); isCode(err, domain.CodeValidation) {
			t.Fatal("two characters were rejected")
		}
	}

	for _, pair := range []struct {
		svc *Service
		db  *Memory
	}{{bare, bareDB}, {cat, catDB}} {
		area := makeArea(t, pair.svc, admin, "Probate")
		matter := makeMatter(t, pair.svc, staff, area, "Limit matter", staff.UserID)
		for i := 0; i < domain.NotesPageSize+1; i++ {
			if _, err := pair.svc.CreateNote(ctx, staff, NoteInput{MatterID: matter.ID, Title: "n" + strconv.Itoa(i)}); err != nil {
				t.Fatal(err)
			}
		}
		notes, cursor, err := pair.svc.ListNotes(ctx, staff, NoteFilter{MatterID: matter.ID})
		if err != nil {
			t.Fatal(err)
		}
		if len(notes) != domain.NotesPageSize || cursor == "" {
			t.Fatalf("notes page %d cursor %q", len(notes), cursor)
		}
		if err := pair.db.PutTime(ctx, domain.TimeEntry{Meta: domain.Meta{ID: tid(uint64(40 + len(pair.db.times)))}, MatterID: matter.ID, Increments: 10}); err != nil {
			t.Fatal(err)
		}
		view, err := pair.svc.GetMatter(ctx, staff, matter.ID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Glance.TimeHours != "1.0" {
			t.Fatalf("time hours %q", view.Glance.TimeHours)
		}

		if _, err := pair.svc.CreateNote(ctx, staff, NoteInput{MatterID: matter.ID, Title: "old-window"}); err != nil {
			t.Fatal(err)
		}
	}
	when = when.AddDate(0, 0, domain.ActivityWindowDays+1)
	for _, pair := range []struct {
		svc *Service
	}{{bare}, {cat}} {
		areaMatters, err := pair.svc.ListMatters(ctx, staff, "", domain.ID(""))
		if err != nil || len(areaMatters) != 1 {
			t.Fatal(err, len(areaMatters))
		}
		if _, err := pair.svc.CreateNote(ctx, staff, NoteInput{MatterID: areaMatters[0].Matter.ID, Title: "inside-window"}); err != nil {
			t.Fatal(err)
		}
		feed, err := pair.svc.Activity(ctx, staff, ActivityQuery{})
		if err != nil {
			t.Fatal(err)
		}
		var old, inside bool
		for _, group := range feed.Groups {
			for _, row := range group.Rows {
				if row.Summary == "Added note old-window" {
					old = true
				}
				if row.Summary == "Added note inside-window" {
					inside = true
				}
			}
		}
		if old || !inside {
			t.Fatalf("activity window old=%v inside=%v", old, inside)
		}
	}

	weak := ScoreName("Ada Lovelace", "Xylace Zed", "", "")
	partial := ScoreName("Ada Lovelace", "Ada Quinn", "", "")
	if weak <= 0 || weak >= domain.ConflictScoreMinimum {
		t.Fatalf("weak score %d is not under the floor", weak)
	}
	if partial < domain.ConflictScoreMinimum {
		t.Fatalf("partial score %d is under the floor", partial)
	}
	for _, svc := range []*Service{bare, cat} {
		rows, err := svc.ListMatters(ctx, staff, "", domain.ID(""))
		if err != nil || len(rows) != 1 {
			t.Fatal(err, len(rows))
		}
		matter := rows[0].Matter
		party, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Ada", FamilyName: "Lovelace"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.AddParty(ctx, staff, matter.ID, PartyInput{ContactID: party.ID, Role: "client", Primary: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Ada", FamilyName: "Quinn"}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Xylace", FamilyName: "Zed"}); err != nil {
			t.Fatal(err)
		}
		run, err := svc.RunConflictCheck(ctx, staff, matter.ID)
		if err != nil {
			t.Fatal(err)
		}
		var sawPartial, sawWeak bool
		for _, hit := range run.Hits {
			if hit.Name == "Ada Quinn" {
				sawPartial = true
			}
			if hit.Name == "Xylace Zed" {
				sawWeak = true
			}
		}
		if !sawPartial || sawWeak {
			t.Fatalf("conflict floor partial=%v weak=%v hits=%v", sawPartial, sawWeak, run.Hits)
		}
	}
}

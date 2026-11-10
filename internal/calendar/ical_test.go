package calendar

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

func secretOf(t *testing.T, url string) string {
	t.Helper()
	i := strings.Index(url, "/ical/")
	if i < 0 || !strings.HasSuffix(url, ".ics") {
		t.Fatalf("feed url %q", url)
	}
	return strings.TrimSuffix(url[i+len("/ical/"):], ".ics")
}

func TestICalFeeds(t *testing.T) {
	f := newFixture(t)
	open := f.matter("Open")
	secret := f.walled("Sealed")
	ada, sam := f.attorney.UserID, f.staff.UserID
	f.event(f.staff, EventInput{Title: "Hearing, Div. 3; bring exhibits", Kind: KindCourtDate, MatterID: open.ID, StartsAt: at(t, "2026-11-18 09:00"), EndsAt: at(t, "2026-11-18 10:00"), Location: "Boone County Courthouse", Attendees: []domain.ID{sam}})
	f.event(f.staff, EventInput{Title: "Ada only", Kind: KindOther, StartsAt: at(t, "2026-11-19 09:00"), EndsAt: at(t, "2026-11-19 10:00"), Attendees: []domain.ID{ada}})
	f.event(f.attorney, EventInput{Title: "Sealed hearing", Kind: KindCourtDate, MatterID: secret.ID, StartsAt: at(t, "2026-11-20 09:00"), EndsAt: at(t, "2026-11-20 10:00"), Attendees: []domain.ID{ada, sam}})
	f.event(f.staff, EventInput{Title: "Thanksgiving", Kind: KindBlock, AllDay: true, StartsAt: at(t, "2026-11-26 00:00"), EndsAt: at(t, "2026-11-27 23:00"), Attendees: []domain.ID{sam}})
	f.deadline(f.staff, DeadlineInput{MatterID: open.ID, Title: "Answer", DueOn: "2026-11-24", ResponsibleUserID: sam})
	f.deadline(f.attorney, DeadlineInput{MatterID: secret.ID, Title: "Sealed report", DueOn: "2026-11-25", ResponsibleUserID: sam})

	feed, link, err := f.svc.CreateFeed(f.ctx, f.staff, sam)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "https://firm.example/ical/") || len(secretOf(t, link)) < 40 {
		t.Fatalf("link %q", link)
	}
	sec := secretOf(t, link)
	if feed.SecretHash == sec || strings.Contains(feed.SecretHash, sec) {
		t.Fatal("secret stored in the clear")
	}
	all, _ := f.audit.List(f.ctx, repository.Filter{})
	for _, row := range all.Items {
		if strings.Contains(row.OldValue+row.NewValue+row.Reason, sec) {
			t.Fatal("secret reached the audit log")
		}
	}
	body, err := f.svc.Download(f.ctx, sec)
	if err != nil {
		t.Fatal(err)
	}
	ics := string(body)
	for _, want := range []string{"BEGIN:VCALENDAR\r\n", "VERSION:2.0\r\n", "X-WR-CALNAME:Sam Staff calendar\r\n",
		`SUMMARY:Hearing\, Div. 3\; bring exhibits`, "DTSTART:20261118T150000Z\r\n", "DTEND:20261118T160000Z\r\n",
		"LOCATION:Boone County Courthouse", "DTSTART;VALUE=DATE:20261126\r\n", "DTEND;VALUE=DATE:20261128\r\n",
		"SUMMARY:Deadline: Answer", "DTSTART;VALUE=DATE:20261124\r\n", "END:VCALENDAR\r\n"} {
		if !strings.Contains(ics, want) {
			t.Fatalf("feed lacks %q:\n%s", want, ics)
		}
	}
	// Staff is an attendee on the sealed hearing and responsible for the sealed report,
	// but cannot see that matter, so neither is published. Ada's event is not Sam's.
	for _, no := range []string{"Sealed", "Ada only"} {
		if strings.Contains(ics, no) {
			t.Fatalf("user feed leaked %q", no)
		}
	}
	for _, line := range strings.Split(strings.TrimSuffix(ics, "\r\n"), "\r\n") {
		if len(line) > 75 || !utf8.ValidString(line) {
			t.Fatalf("bad content line %q", line)
		}
	}

	// The firm feed is admin-only and omits every walled matter, even for the admin.
	if _, _, err := f.svc.CreateFeed(f.ctx, f.staff, ""); code(err) != domain.CodeForbidden {
		t.Fatalf("staff firm feed: %v", err)
	}
	if _, _, err := f.svc.CreateFeed(f.ctx, f.staff, ada); code(err) != domain.CodeForbidden {
		t.Fatalf("staff made a feed for someone else: %v", err)
	}
	if _, _, err := f.svc.CreateFeed(f.ctx, f.accountant, f.accountant.UserID); code(err) != domain.CodeForbidden {
		t.Fatalf("accountant feed: %v", err)
	}
	firm, firmLink, err := f.svc.CreateFeed(f.ctx, f.admin, "")
	if err != nil {
		t.Fatal(err)
	}
	fb, err := f.svc.Download(f.ctx, secretOf(t, firmLink))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fb), "Ada only") || !strings.Contains(string(fb), "Thanksgiving") || strings.Contains(string(fb), "Sealed") {
		t.Fatalf("firm feed:\n%s", fb)
	}
	// The attorney on the wall gets sealed events in their own feed.
	_, adaLink, err := f.svc.CreateFeed(f.ctx, f.attorney, ada)
	if err != nil {
		t.Fatal(err)
	}
	ab, _ := f.svc.Download(f.ctx, secretOf(t, adaLink))
	if !strings.Contains(string(ab), "Sealed hearing") {
		t.Fatal("attorney feed lacks the walled event they may see")
	}
	// A wall added after the feed was created hides the matter at once.
	if err := f.practice.SetWall(f.ctx, f.admin, open.ID, []domain.ID{ada}); err != nil {
		t.Fatal(err)
	}
	body, _ = f.svc.Download(f.ctx, sec)
	if strings.Contains(string(body), "Hearing") || strings.Contains(string(body), "Answer") {
		t.Fatal("feed kept a matter walled after creation")
	}

	// Listing: staff sees only their own feed; the admin sees all.
	mine, _ := f.svc.ListFeeds(f.ctx, f.staff)
	every, _ := f.svc.ListFeeds(f.ctx, f.admin)
	if len(mine) != 1 || mine[0].ID != feed.ID || len(every) != 3 {
		t.Fatalf("lists: %d %d", len(mine), len(every))
	}
	if _, err := f.svc.GetFeed(f.ctx, f.staff, firm.ID); code(err) != domain.CodeNotFound {
		t.Fatalf("staff reads firm feed record: %v", err)
	}
	if _, err := f.svc.RevokeFeed(f.ctx, f.other, feed.ID); code(err) != domain.CodeNotFound {
		t.Fatalf("other user revoked: %v", err)
	}
	// Revocation kills the link for good.
	if _, err := f.svc.RevokeFeed(f.ctx, f.staff, feed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Download(f.ctx, sec); code(err) != domain.CodeNotFound {
		t.Fatalf("revoked feed served: %v", err)
	}
	if rows, _ := f.audit.Rows(f.ctx, audit.RowQuery{Entity: "ical_feed", EntityID: feed.ID, Field: "revoked"}); len(rows) != 1 {
		t.Fatal("revocation not audited")
	}
	// An unknown or short secret is not found.
	for _, s := range []string{"short", strings.Repeat("x", 43)} {
		if _, err := f.svc.Download(f.ctx, s); code(err) != domain.CodeNotFound {
			t.Fatalf("secret %q: %v", s, err)
		}
	}
	// Turning feeds off on the admin page stops every feed and new ones.
	if _, err := f.reg.Set(f.ctx, settingICal, "false", f.admin); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Download(f.ctx, secretOf(t, firmLink)); code(err) != domain.CodeNotFound {
		t.Fatalf("disabled feed served: %v", err)
	}
	if _, _, err := f.svc.CreateFeed(f.ctx, f.admin, ""); code(err) != domain.CodeValidation {
		t.Fatalf("created while off: %v", err)
	}
	// A deactivated owner's feed stops.
	if _, err := f.reg.Set(f.ctx, settingICal, "true", f.admin); err != nil {
		t.Fatal(err)
	}
	u, _ := f.pm.User(f.ctx, ada)
	u.Active = false
	_ = f.pm.PutUser(f.ctx, u)
	if _, err := f.svc.Download(f.ctx, secretOf(t, adaLink)); code(err) != domain.CodeNotFound {
		t.Fatalf("inactive owner feed served: %v", err)
	}
}

func TestICalHTTP(t *testing.T) {
	f := newFixture(t)
	srv := f.server()
	f.event(f.staff, EventInput{Title: "Visible", Kind: KindOther, StartsAt: at(t, "2026-11-18 09:00"), EndsAt: at(t, "2026-11-18 10:00"), Attendees: []domain.ID{f.staff.UserID}})
	status, out := send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/ical-feeds", `{"owner_user_id":"`+string(f.staff.UserID)+`"}`, nil)
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, out)
	}
	var created struct {
		Feed map[string]any `json:"feed"`
		URL  string         `json:"url"`
	}
	_ = json.Unmarshal([]byte(out), &created)
	if strings.Contains(out, "secret_hash") || created.Feed["revoked"] != false {
		t.Fatalf("feed shape: %s", out)
	}
	path := strings.TrimPrefix(created.URL, "https://firm.example")
	// No session needed: the secret is the credential.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/calendar") || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("download: %d %v", res.StatusCode, res.Header)
	}
	if status, _ := get(t, srv.URL, domain.Principal{}, strings.TrimSuffix(path, ".ics")); status != http.StatusNotFound {
		t.Fatalf("no .ics: %d", status)
	}
	id := created.Feed["id"].(string)
	if status, _ := send(t, srv.URL, f.staff, http.MethodPost, "/api/v1/ical-feeds/"+id+"/revoke", "", nil); status != http.StatusOK {
		t.Fatalf("revoke: %d", status)
	}
	if status, _ := get(t, srv.URL, domain.Principal{}, path); status != http.StatusNotFound {
		t.Fatalf("revoked download: %d", status)
	}
	// The feeds page shows the new link once, and the staff user gets no firm-wide button.
	status, page := postForm(t, srv.URL, f.staff, "/calendar/feeds", map[string][]string{"scope": {"me"}})
	if status != http.StatusOK || !strings.Contains(page, "/ical/") || strings.Contains(page, "New firm-wide link") {
		t.Fatalf("feeds page: %d", status)
	}
	if status, _ := postForm(t, srv.URL, f.staff, "/calendar/feeds", map[string][]string{"scope": {"firm"}}); status != http.StatusForbidden {
		t.Fatalf("staff firm feed via form: %d", status)
	}
}

func TestICalFolding(t *testing.T) {
	var w icsWriter
	w.prop("SUMMARY", strings.Repeat("é", 60)+", done")
	out := w.String()
	lines := strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n")
	if len(lines) < 2 {
		t.Fatalf("not folded: %q", out)
	}
	var joined strings.Builder
	for i, l := range lines {
		if len(l) > 75 || !utf8.ValidString(l) {
			t.Fatalf("line %d: %d octets", i, len(l))
		}
		if i > 0 {
			if l[0] != ' ' {
				t.Fatal("continuation without space")
			}
			l = l[1:]
		}
		joined.WriteString(l)
	}
	if joined.String() != "SUMMARY:"+strings.Repeat("é", 60)+`\, done` {
		t.Fatalf("unfold mismatch: %q", joined.String())
	}
}

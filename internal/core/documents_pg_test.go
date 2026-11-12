package core

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/editor"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/storage/storagetest"
	"github.com/CharitonMedia/togavsu/internal/supervisor"
)

// filepathWalk calls f for every entry under root; a missing root has none.
func filepathWalk(root string, f func(name string, dir bool)) error {
	return filepath.WalkDir(root, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		f(e.Name(), e.IsDir())
		return nil
	})
}

// tarEach calls f with each regular file in a tar stream.
func tarEach(b []byte, f func(name string, body []byte)) error {
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		f(h.Name, body)
	}
}

// docs is the phase 4 harness: the finance harness's running core (non-superuser role,
// first run over HTTP, admin, attorney, staff and accountant signed in, system mail on a
// fake SMTP server) with the calendar, files, editor and Doc Tender composed in.
type docs struct {
	*fin
	staffID string
}

func newDocs(t *testing.T, cfg ...func(*Config)) *docs {
	t.Helper()
	f := newFin(t, cfg...)
	d := &docs{fin: f}
	d.staffID = str(t, f.staff.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")
	return d
}

// raw sends body as is (an upload chunk) with headers; JSON is decoded when it parses.
func (c *client) raw(method, path string, body []byte, hdr ...string) resp {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	out := resp{code: res.StatusCode, body: string(b), loc: res.Header.Get("Location")}
	_ = json.Unmarshal(b, &out.json)
	return out
}

// upload stores data as a new file on matter through the resumable upload API, one
// chunk at a time, and publishes it.
func (d *docs) upload(c *client, matter, name, mediaType string, data []byte, extra map[string]any) map[string]any {
	d.t.Helper()
	up := c.must(http.StatusCreated, "POST", "/api/v1/file-uploads", map[string]any{"file_name": name, "matter_id": matter, "size_bytes": len(data), "media_type": mediaType})
	id := str(d.t, up.json, "id")
	chunk := int(num(d.t, up.json, "chunk_size_bytes"))
	for off := 0; off < len(data); off += chunk {
		end := min(off+chunk, len(data))
		r := c.raw("PUT", "/api/v1/file-uploads/"+id, data[off:end], "Content-Type", "application/octet-stream",
			"Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, end-1, len(data)))
		if r.code != http.StatusOK {
			d.t.Fatalf("chunk at %d: %d %s", off, r.code, r.body)
		}
	}
	c.must(http.StatusOK, "POST", "/api/v1/file-uploads/"+id+"/complete", map[string]any{})
	body := map[string]any{"upload_id": id}
	for k, v := range extra {
		body[k] = v
	}
	return c.must(http.StatusCreated, "POST", "/api/v1/files", body).json
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func firmToday(t *testing.T) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	n := time.Now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
}

func ids(list []map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, m := range list {
		if s, ok := m["id"].(string); ok {
			out[s] = true
		}
	}
	return out
}

// TestDocumentsCalendarDeadlines is the calendar-deadlines phase case (AC-011 calendar
// half, AC-027) on the running core: deadlines and events are stored in Postgres and
// survive a restart; the upcoming deadlines widget's target opens the deadlines page
// with the same date range and each row opens its deadline; a staff user outside a
// matter wall never reaches the walled deadline through the widget, list, page, API,
// /mcp or the firm iCal feed; available dates lists only days with no court date, block
// or all-day event, as a pleading and a letter, and never changes the calendar.
func TestDocumentsCalendarDeadlines(t *testing.T) {
	d := newDocs(t)
	t.Setenv("TZ", "UTC")
	open, _ := d.matter("Rivera Dissolution", "", map[string]any{"case_number": "26AR-DR00123", "court": "Circuit Court of Adair County"})
	walled, _ := d.matter("Sealed Guardianship", "", nil)
	d.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{d.adminID, d.attyID}})

	today := firmToday(t)
	day := func(n int) string { return today.AddDate(0, 0, n).Format("2006-01-02") }
	deadline := func(c *client, matter, title, due string) string {
		return str(t, c.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{
			"matter_id": matter, "title": title, "due_on": due, "responsible_user_id": d.attyID}).json, "id")
	}
	inRange := deadline(d.staff, open, "Answer due", day(3))
	overdue := deadline(d.staff, open, "Disclosures late", day(-4))
	later := deadline(d.staff, open, "Pretrial brief", day(40))
	hidden := deadline(d.atty, walled, "Sealed report", day(2))
	// A staff user cannot put a deadline on a matter they cannot see.
	if r := d.staff.do("POST", "/api/v1/deadlines", map[string]any{"matter_id": walled, "title": "x", "due_on": day(1), "responsible_user_id": d.attyID}); r.code != http.StatusNotFound {
		t.Fatalf("staff deadline on a walled matter: %d %s", r.code, r.body)
	}

	// Postgres holds them, and a restarted core serves them.
	if n := d.count(`SELECT COUNT(*) FROM deadlines`); n != 4 {
		t.Fatalf("%d deadline rows in Postgres, want 4", n)
	}
	d.restart()

	w := d.staff.must(http.StatusOK, "GET", "/api/v1/widgets/upcoming-deadlines", nil).json
	target, _ := w["target"].(map[string]any)
	q, _ := target["query"].(map[string]any)
	if target["path"] != "/calendar/deadlines" || w["page_path"] != "/calendar/deadlines" || q["from_date"] != day(0) {
		t.Fatalf("widget target %v", w)
	}
	rows, _ := w["rows"].([]any)
	seen := map[string]bool{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		seen[row["id"].(string)] = true
		if row["target_path"] != "/calendar/deadlines/"+row["id"].(string) {
			t.Fatalf("row target %v", row)
		}
	}
	if !seen[inRange] || !seen[overdue] || seen[later] || seen[hidden] {
		t.Fatalf("widget rows %v (in range %s, overdue %s, later %s, walled %s)", seen, inRange, overdue, later, hidden)
	}
	// The click-through: the page with the widget's own query lists the same rows, and
	// each row opens its deadline page.
	vals := url.Values{}
	for k, v := range q {
		vals.Set(k, v.(string))
	}
	page := d.staff.must(http.StatusOK, "GET", "/calendar/deadlines?"+vals.Encode(), nil).body
	for id := range seen {
		if !strings.Contains(page, `href="/calendar/deadlines/`+id+`"`) {
			t.Fatalf("deadlines page for the widget's range has no link to %s", id)
		}
		d.staff.must(http.StatusOK, "GET", "/calendar/deadlines/"+id, nil)
	}
	if strings.Contains(page, "Sealed") {
		t.Fatal("the deadlines page shows the walled deadline to staff")
	}
	list := d.staff.must(http.StatusOK, "GET", "/api/v1/deadlines?"+vals.Encode(), nil)
	if got := ids(items(t, list)); got[hidden] || !got[inRange] {
		t.Fatalf("deadline list for staff %v", got)
	}
	for _, path := range []string{"/calendar/deadlines/" + hidden, "/api/v1/deadlines/" + hidden} {
		if r := d.staff.do("GET", path, nil); r.code != http.StatusNotFound || strings.Contains(r.body, "Sealed") {
			t.Fatalf("staff %s: %d %s", path, r.code, r.body)
		}
	}
	d.atty.must(http.StatusOK, "GET", "/api/v1/deadlines/"+hidden, nil)
	// The same answer over /mcp.
	call := mcpCall(d.staff, toolName(t, "GET", "/api/v1/deadlines/{deadline_id}"), map[string]any{"path": map[string]any{"deadline_id": hidden}})
	if !strings.Contains(call.body, `"isError":true`) || strings.Contains(call.body, "Sealed report") {
		t.Fatalf("MCP read of the walled deadline: %s", call.body)
	}
	call = mcpCall(d.staff, toolName(t, "GET", "/api/v1/deadlines"), map[string]any{})
	if strings.Contains(call.body, `"isError":true`) || strings.Contains(call.body, "Sealed report") || !strings.Contains(call.body, "Answer due") {
		t.Fatalf("MCP deadline list: %s", call.body)
	}
	// Signed out, the widget and the page refuse.
	anon := newClient(t, d.srv.URL)
	if r := anon.do("GET", "/api/v1/widgets/upcoming-deadlines", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("anonymous widget: %d", r.code)
	}
	if r := anon.do("GET", "/calendar/deadlines", nil); r.code != http.StatusSeeOther || r.loc != "/login" {
		t.Fatalf("anonymous deadlines page: %d %s", r.code, r.loc)
	}

	// Available dates (AC-027): the attorney has a court date, a block and an all-day
	// event in the window, and a client meeting that does not make a day busy.
	at := func(n, hour int) string {
		return time.Date(today.Year(), today.Month(), today.Day()+n, hour, 0, 0, 0, today.Location()).Format(time.RFC3339)
	}
	event := func(c *client, title, kind string, n, from, to int, allDay bool, matter string) string {
		body := map[string]any{"title": title, "kind": kind, "starts_at": at(n, from), "ends_at": at(n, to), "all_day": allDay, "attendee_user_ids": []string{d.attyID, d.staffID}}
		if matter != "" {
			body["matter_id"] = matter
		}
		return str(t, c.must(http.StatusCreated, "POST", "/api/v1/calendar-events", body).json, "id")
	}
	// The window's first seven weekdays (the tool lists court days only).
	var wd []int
	for n := 1; len(wd) < 7; n++ {
		if wk := today.AddDate(0, 0, n).Weekday(); wk != time.Saturday && wk != time.Sunday {
			wd = append(wd, n)
		}
	}
	event(d.staff, "Hearing", "court_date", wd[0], 9, 11, false, open)
	event(d.atty, "Office block", "block", wd[1], 8, 17, false, "")
	event(d.atty, "Conference", "other", wd[2], 0, 23, true, "")
	event(d.staff, "Client meeting", "client_meeting", wd[3], 10, 11, false, open)
	sealed := event(d.atty, "Sealed hearing", "court_date", wd[4], 9, 10, false, walled)
	before := d.count(`SELECT COUNT(*) FROM calendar_events`)
	beforeAudit := d.count(`SELECT COUNT(*) FROM audit_log`)
	res := d.staff.must(http.StatusOK, "POST", "/api/v1/calendar/available-dates", map[string]any{
		"user_id": d.attyID, "start_date": day(1), "end_date": day(wd[6]), "matter_id": open, "service_date": day(0),
		"letter_recipient": "Opposing Counsel", "letter_address": "1 Synthetic Plaza", "letter_re": "Rivera", "letter_salutation": "Dear Counsel:",
	}).json
	var dates []string
	for _, raw := range res["dates"].([]any) {
		dates = append(dates, raw.(map[string]any)["date"].(string))
	}
	want := []string{day(wd[3]), day(wd[5]), day(wd[6])}
	if strings.Join(dates, ",") != strings.Join(want, ",") {
		t.Fatalf("available dates %v, want %v (court date, block, all-day event and a walled court date are busy; a client meeting is not)", dates, want)
	}
	pleading, letter := res["pleading"].(string), res["letter"].(string)
	for _, s := range []string{"AVAILABLE DATES FOR HEARING", "26AR-DR00123", "CERTIFICATE OF SERVICE"} {
		if !strings.Contains(strings.ToUpper(pleading), s) {
			t.Fatalf("pleading lacks %q:\n%s", s, pleading)
		}
	}
	if !strings.Contains(letter, "Dear Counsel:") || !strings.Contains(letter, "1 Synthetic Plaza") || strings.Contains(letter, "CERTIFICATE OF SERVICE") {
		t.Fatalf("letter:\n%s", letter)
	}
	if strings.Contains(pleading+letter, "Sealed") {
		t.Fatal("available dates named the walled hearing")
	}
	if after := d.count(`SELECT COUNT(*) FROM calendar_events`); after != before {
		t.Fatalf("available dates changed the calendar: %d events, was %d", after, before)
	}
	if after := d.count(`SELECT COUNT(*) FROM audit_log`); after != beforeAudit {
		t.Fatalf("available dates wrote %d audit rows", after-beforeAudit)
	}
	// A start date after the end date is refused.
	if r := d.staff.do("POST", "/api/v1/calendar/available-dates", map[string]any{"user_id": d.attyID, "start_date": day(9), "end_date": day(2)}); r.code != http.StatusBadRequest {
		t.Fatalf("reversed window: %d %s", r.code, r.body)
	}
	// The walled event is hidden from staff, also on the firm iCal feed.
	if r := d.staff.do("GET", "/api/v1/calendar-events/"+sealed, nil); r.code != http.StatusNotFound {
		t.Fatalf("staff read of a walled event: %d", r.code)
	}
	// The staff user is on every event's attendee list, the walled one included; the feed
	// still leaves it out.
	feed := d.staff.must(http.StatusCreated, "POST", "/api/v1/ical-feeds", map[string]any{"owner_user_id": d.staffID}).json
	feedURL := ""
	for _, k := range []string{"url", "feed_url"} {
		if s, ok := feed[k].(string); ok {
			feedURL = s
		}
	}
	u, err := url.Parse(feedURL)
	if err != nil || u.Path == "" {
		t.Fatalf("feed %v", feed)
	}
	ics := anon.must(http.StatusOK, "GET", u.Path, nil).body
	if !strings.Contains(ics, "BEGIN:VCALENDAR") || !strings.Contains(ics, "Hearing") || strings.Contains(ics, "Sealed") {
		t.Fatalf("staff feed:\n%s", ics)
	}
	d.staff.must(http.StatusOK, "POST", "/api/v1/ical-feeds/"+str(t, feed, "feed", "id")+"/revoke", map[string]any{})
	if r := anon.do("GET", u.Path, nil); r.code != http.StatusNotFound {
		t.Fatalf("revoked feed: %d", r.code)
	}
	// Writes are audited in the shared ledger.
	if n := d.count(`SELECT COUNT(*) FROM audit_log WHERE entity = 'deadline'`); n < 4 {
		t.Fatalf("%d deadline audit rows", n)
	}
}

// adminP is the admin as a service principal, for the file-bytes backup calls the
// supervisor makes (it has no /api/v1 route of its own in this phase).
func (d *docs) adminP() domain.Principal {
	return domain.Principal{UserID: domain.ID(d.adminID), Roles: []domain.Role{domain.RoleAdmin, domain.RoleAttorney}, Source: domain.SourceUI}
}

// useProvider points the AI tab at the synthetic provider, so Doc Tender runs.
func (d *docs) useProvider() {
	d.t.Helper()
	for k, v := range map[string]string{"ai.openai_api_key": providerKey, "ai.primary_provider": "openai", "ai.primary_strong_model": "gpt-test-strong",
		"ai.primary_fast_model": "gpt-test-fast", "ai.speech_to_text_model": "whisper-test"} {
		d.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/"+k, map[string]any{"value": v})
	}
}

// drain runs Doc Tender until no job is due.
func (d *docs) drain() {
	d.t.Helper()
	if err := d.app.DocTender.Drain(context.Background()); err != nil {
		d.t.Fatal(err)
	}
}

// download reads a file version over the API and returns its bytes and status.
func (d *docs) download(c *client, file, version string) (int, []byte) {
	d.t.Helper()
	req, _ := http.NewRequest("GET", c.base+"/api/v1/files/"+file+"/versions/"+version+"/download", nil)
	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return -1, nil
	}
	return res.StatusCode, b
}

// failingBody sends n bytes and then fails, like a connection that drops mid-chunk.
type failingBody struct {
	r io.Reader
	n int
}

func (f *failingBody) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, fmt.Errorf("connection dropped")
	}
	if len(p) > f.n {
		p = p[:f.n]
	}
	n, err := f.r.Read(p)
	f.n -= n
	return n, err
}

// synthChunk is chunk i of a synthetic upload: bytes no compressor shortens much,
// different in every chunk.
func synthChunk(i, size int) []byte {
	out := make([]byte, 0, size)
	seed := sha256.Sum256([]byte(fmt.Sprintf("synthetic-%d", i)))
	for len(out) < size {
		seed = sha256.Sum256(seed[:])
		out = append(out, seed[:]...)
	}
	return out[:size]
}

func localObjects(t *testing.T, root string) int {
	t.Helper()
	n := 0
	_ = filepathWalk(root, func(name string, dir bool) {
		if !dir && !strings.HasPrefix(name, ".") {
			n++
		}
	})
	return n
}

// TestDocumentsStorageVersionsMoves is the storage-versions-moves phase case (AC-039,
// AC-040, AC-050) on the running core over HTTP: a 1 GiB upload (64 MiB with -short)
// survives a dropped connection and a core restart and its SHA-256 matches; a file on a
// walled matter can't be listed, read, previewed, downloaded, archived or searched from
// outside the wall, over the API or /mcp; every save adds a version and none changes;
// all files move local disk -> S3 -> Dropbox while staying readable, with old copies
// deleted only after the admin confirms; a changed byte fails the read and mails the
// admins; the file-bytes backup brings every version and Markdown copy back with
// matching SHA-256s; the Markdown copy has the original's base name, follows its wall
// and is what Files search matches.
func TestDocumentsStorageVersionsMoves(t *testing.T) {
	prov := NewProvider()
	defer prov.Close()
	d := newDocs(t, func(c *Config) {
		c.NoBackground = true
		c.ProviderURLs = map[string]string{"openai": prov.URL()}
	})
	ctx := context.Background()
	open, _ := d.matter("Rivera Dissolution", "", nil)
	walled, _ := d.matter("Sealed Guardianship", "", nil)
	d.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{d.adminID, d.attyID}})

	// --- Resumable upload (AC-039) ---
	size := 1 << 30
	if testing.Short() {
		size = 64 << 20
	}
	up := d.staff.must(http.StatusCreated, "POST", "/api/v1/file-uploads", map[string]any{"file_name": "deposition-video.mp4", "matter_id": open, "size_bytes": size, "media_type": "video/mp4"})
	upID, chunk := str(t, up.json, "id"), int(num(t, up.json, "chunk_size_bytes"))
	if chunk != 8<<20 {
		t.Fatalf("chunk size %d, want 8 MiB", chunk)
	}
	chunks := (size + chunk - 1) / chunk
	whole := sha256.New()
	for i := 0; i < chunks; i++ {
		part := synthChunk(i, min(chunk, size-i*chunk))
		whole.Write(part)
		rng := fmt.Sprintf("bytes %d-%d/%d", i*chunk, i*chunk+len(part)-1, size)
		if i == chunks/3 {
			req, _ := http.NewRequest("PUT", d.srv.URL+"/api/v1/file-uploads/"+upID, &failingBody{r: bytes.NewReader(part), n: len(part) / 2})
			req.ContentLength = int64(len(part))
			req.Header.Set("Content-Range", rng)
			req.Header.Set("X-CSRF-Token", d.staff.csrf)
			req.Header.Set("Content-Type", "application/octet-stream")
			if res, err := d.staff.http.Do(req); err == nil {
				res.Body.Close()
				t.Fatalf("a chunk cut off halfway was accepted: %d", res.StatusCode)
			}
			if got := num(t, d.staff.must(http.StatusOK, "GET", "/api/v1/file-uploads/"+upID, nil).json, "next_offset"); got != int64(i*chunk) {
				t.Fatalf("after the drop the upload resumes at %d, want %d", got, i*chunk)
			}
		}
		if i == 2*chunks/3 {
			d.restart()
			if got := num(t, d.staff.must(http.StatusOK, "GET", "/api/v1/file-uploads/"+upID, nil).json, "next_offset"); got != int64(i*chunk) {
				t.Fatalf("after a core restart the upload resumes at %d, want %d", got, i*chunk)
			}
		}
		r := d.staff.raw("PUT", "/api/v1/file-uploads/"+upID, part, "Content-Type", "application/octet-stream", "Content-Range", rng)
		if r.code != http.StatusOK || num(t, r.json, "next_offset") != int64(i*chunk+len(part)) {
			t.Fatalf("chunk %d: %d %s", i, r.code, r.body)
		}
	}
	wantSHA := hex.EncodeToString(whole.Sum(nil))
	done := d.staff.must(http.StatusOK, "POST", "/api/v1/file-uploads/"+upID+"/complete", map[string]any{}).json
	if done["sha256"] != wantSHA {
		t.Fatalf("staged sha %v, want %s", done["sha256"], wantSHA)
	}
	big := d.staff.must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": upID}).json
	bigID, bigV := str(t, big, "id"), str(t, big, "current_version_id")
	{
		req, _ := http.NewRequest("GET", d.srv.URL+"/api/v1/files/"+bigID+"/versions/"+bigV+"/download", nil)
		res, err := d.staff.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		n, err := io.Copy(h, res.Body)
		res.Body.Close()
		if err != nil || n != int64(size) || hex.EncodeToString(h.Sum(nil)) != wantSHA {
			t.Fatalf("download of the %d-byte upload: %d bytes, err %v", size, n, err)
		}
	}
	// The big file has served its purpose; trash and purge it so the moves below copy
	// only the small corpus (the trash keeps it listed until an admin purges it).
	d.staff.must(http.StatusOK, "POST", "/api/v1/files/"+bigID+"/trash", map[string]any{})
	d.admin.must(http.StatusNoContent, "POST", "/api/v1/files/"+bigID+"/purge", map[string]any{"confirm_name": str(t, big, "name")})

	// --- Versions (AC-039) ---
	memo := []byte("SYNTHETIC MEMO\nRIVERA HEARING PREP\nNEEDLE-ALPHA-7731\n")
	f1 := d.upload(d.atty, open, "hearing prep.txt", "text/plain", memo, nil)
	fileID, v1 := str(t, f1, "id"), str(t, f1, "current_version_id")
	v2body := append(append([]byte{}, memo...), []byte("SECOND DRAFT\n")...)
	up2 := d.atty.must(http.StatusCreated, "POST", "/api/v1/file-uploads", map[string]any{"file_name": "hearing prep.txt", "matter_id": open, "size_bytes": len(v2body), "media_type": "text/plain"})
	d.atty.raw("PUT", "/api/v1/file-uploads/"+str(t, up2.json, "id"), v2body, "Content-Type", "application/octet-stream", "Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(v2body)-1, len(v2body)))
	d.atty.must(http.StatusOK, "POST", "/api/v1/file-uploads/"+str(t, up2.json, "id")+"/complete", map[string]any{})
	v2 := str(t, d.atty.must(http.StatusCreated, "POST", "/api/v1/files/"+fileID+"/versions", map[string]any{"upload_id": str(t, up2.json, "id")}).json, "id")
	v3 := str(t, d.atty.must(http.StatusCreated, "POST", "/api/v1/files/"+fileID+"/versions/"+v1+"/restore", map[string]any{}).json, "id")
	vs := items(t, d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+fileID+"/versions", nil))
	if len(vs) != 3 {
		t.Fatalf("%d versions after upload, save and restore", len(vs))
	}
	shaOf := map[string]string{}
	for _, v := range vs {
		shaOf[v["id"].(string)] = v["sha256"].(string)
	}
	if shaOf[v1] != sha(memo) || shaOf[v2] != sha(v2body) || shaOf[v3] != sha(memo) || v1 == v3 {
		t.Fatalf("versions %v", shaOf)
	}
	for v, want := range map[string][]byte{v1: memo, v2: v2body, v3: memo} {
		if code, got := d.download(d.atty, fileID, v); code != http.StatusOK || !bytes.Equal(got, want) {
			t.Fatalf("version %s reads %d %q", v, code, got)
		}
	}

	// --- Walls (AC-039) and the Markdown copy (AC-050) ---
	d.useProvider()
	secretBody := []byte("SEALED REPORT\nNEEDLE-BRAVO-4410\n")
	sealed := d.upload(d.atty, walled, "sealed report.txt", "text/plain", secretBody, nil)
	sealedID, sealedV := str(t, sealed, "id"), str(t, sealed, "current_version_id")
	d.drain()
	for _, id := range []string{fileID, sealedID} {
		if st := str(t, d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+id+"/doc-tender-status", nil).json, "status"); st != "published" {
			t.Fatalf("doc tender status of %s = %s", id, st)
		}
	}
	got := d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+fileID, nil).json
	name := str(t, got, "name")
	base := strings.TrimSuffix(name, ".txt")
	rends := items(t, d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+fileID+"/renditions", nil))
	var md map[string]any
	for _, r := range rends {
		if r["kind"] == "markdown" {
			md = r
		}
	}
	if md == nil || md["name"] != base+".md" || got["markdown_name"] != base+".md" {
		t.Fatalf("file %q: markdown copy %v", name, md)
	}
	mdBody := d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+fileID+"/renditions/"+md["id"].(string), nil).body
	if !strings.Contains(mdBody, "NEEDLE-ALPHA-7731") {
		t.Fatalf("markdown copy:\n%s", mdBody)
	}
	// Files search matches the Markdown copy's text, inside the wall only.
	if hits := ids(items(t, d.staff.must(http.StatusOK, "GET", "/api/v1/files?q=NEEDLE-ALPHA-7731", nil))); !hits[fileID] {
		t.Fatalf("search for the Markdown text: %v", hits)
	}
	if hits := ids(items(t, d.atty.must(http.StatusOK, "GET", "/api/v1/files?q=NEEDLE-BRAVO-4410", nil))); !hits[sealedID] {
		t.Fatalf("attorney inside the wall: %v", hits)
	}
	if r := d.staff.must(http.StatusOK, "GET", "/api/v1/files?q=NEEDLE-BRAVO-4410", nil); strings.Contains(r.body, sealedID) {
		t.Fatalf("staff search found the walled file: %s", r.body)
	}
	if r := d.staff.must(http.StatusOK, "GET", "/api/v1/files", nil); strings.Contains(r.body, sealedID) || !strings.Contains(r.body, fileID) {
		t.Fatalf("staff file list: %s", r.body)
	}
	srends := items(t, d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+sealedID+"/renditions", nil))
	for _, path := range []string{"/api/v1/files/" + sealedID, "/api/v1/files/" + sealedID + "/versions", "/api/v1/files/" + sealedID + "/renditions",
		"/api/v1/files/" + sealedID + "/versions/" + sealedV + "/download", "/api/v1/files/" + sealedID + "/versions/" + sealedV + "/download?disposition=inline",
		"/api/v1/files/" + sealedID + "/renditions/" + srends[0]["id"].(string), "/api/v1/files/" + sealedID + "/doc-tender-result", "/api/v1/files?matter_id=" + walled} {
		r := d.staff.do("GET", path, nil)
		if (r.code != http.StatusNotFound && !(strings.Contains(path, "matter_id") && r.code == http.StatusOK && !strings.Contains(r.body, sealedID))) || strings.Contains(r.body, "NEEDLE-BRAVO") {
			t.Fatalf("staff GET %s: %d %s", path, r.code, r.body)
		}
	}
	if r := d.staff.do("POST", "/api/v1/file-archives", map[string]any{"file_ids": []string{sealedID}}); r.code != http.StatusNotFound {
		t.Fatalf("staff zip of the walled file: %d", r.code)
	}
	call := mcpCall(d.staff, toolName(t, "GET", "/api/v1/files/{file_id}"), map[string]any{"path": map[string]any{"file_id": sealedID}})
	if !strings.Contains(call.body, `"isError":true`) || strings.Contains(call.body, "sealed") {
		t.Fatalf("MCP read of the walled file: %s", call.body)
	}

	// --- Moves local -> S3 -> Dropbox (AC-040) ---
	s3, dbx := storagetest.NewS3("togavsu"), storagetest.NewDropbox()
	defer s3.Server.Close()
	defer dbx.Server.Close()
	backends := items(t, d.admin.must(http.StatusOK, "GET", "/api/v1/storage-backends", nil))
	local := backends[0]["id"].(string)
	newBackend := func(kind, name string, set any) string {
		return str(t, d.admin.must(http.StatusCreated, "POST", "/api/v1/storage-backends", map[string]any{"kind": kind, "name": name, "settings": set}).json, "id")
	}
	s3ID, dbxID := newBackend("s3", "S3", s3.Settings()), newBackend("dropbox", "Dropbox", dbx.Settings())
	if r := d.staff.do("POST", "/api/v1/storage-moves", map[string]any{"from_backend_id": local, "to_backend_id": s3ID, "scope": "all"}); r.code != http.StatusForbidden {
		t.Fatalf("staff move: %d", r.code)
	}
	type obj struct {
		file, version string
		body          []byte
	}
	corpus := []obj{{fileID, v1, memo}, {fileID, v2, v2body}, {fileID, v3, memo}, {sealedID, sealedV, secretBody}}
	readAll := func() {
		t.Helper()
		for _, o := range corpus {
			if code, got := d.download(d.atty, o.file, o.version); code != http.StatusOK || !bytes.Equal(got, o.body) {
				t.Fatalf("during the move %s/%s reads %d %q", o.file, o.version, code, got)
			}
		}
	}
	localRoot := d.cfg.DataDir + "/files/objects"
	moveAll := func(from, to string) string {
		m := d.admin.must(http.StatusCreated, "POST", "/api/v1/storage-moves", map[string]any{"from_backend_id": from, "to_backend_id": to, "scope": "all"}).json
		id := str(t, m, "id")
		deadline := time.Now().Add(2 * time.Minute)
		for {
			readAll() // every file stays readable while the move runs
			mv := d.admin.must(http.StatusOK, "GET", "/api/v1/storage-moves/"+id, nil).json
			if mv["status"] == "awaiting_confirmation" {
				if num(t, mv, "progress_done") != num(t, mv, "progress_total") {
					t.Fatalf("checked move %v", mv)
				}
				break
			}
			if mv["status"] == "failed" || time.Now().After(deadline) {
				t.Fatalf("move %v", mv)
			}
			time.Sleep(50 * time.Millisecond)
		}
		return id
	}
	objectsBefore := localObjects(t, localRoot)
	m1 := moveAll(local, s3ID)
	if n := localObjects(t, localRoot); n != objectsBefore || s3.Len() != objectsBefore {
		t.Fatalf("before confirmation: %d local (was %d), %d on S3", n, objectsBefore, s3.Len())
	}
	if r := d.atty.do("POST", "/api/v1/storage-moves/"+m1+"/confirm", map[string]any{}); r.code != http.StatusForbidden {
		t.Fatalf("attorney confirm: %d", r.code)
	}
	d.admin.must(http.StatusOK, "POST", "/api/v1/storage-moves/"+m1+"/confirm", map[string]any{})
	if n := localObjects(t, localRoot); n != 0 {
		t.Fatalf("%d local copies after the admin confirmed", n)
	}
	for _, k := range s3.Keys() {
		if bytes.Contains(s3.Get(k), []byte("NEEDLE")) {
			t.Fatalf("plaintext on S3 under %s", k)
		}
	}
	readAll()
	m2 := moveAll(s3ID, dbxID)
	if s3.Len() != objectsBefore {
		t.Fatalf("S3 copies deleted before confirmation: %d", s3.Len())
	}
	d.admin.must(http.StatusOK, "POST", "/api/v1/storage-moves/"+m2+"/confirm", map[string]any{})
	if s3.Len() != 0 || dbx.Len() != objectsBefore {
		t.Fatalf("after confirming: S3 %d, Dropbox %d", s3.Len(), dbx.Len())
	}
	readAll()

	// --- A changed byte fails the read and mails the admins (AC-040) ---
	d.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/mail.smtp_from", map[string]any{"value": "files@firm.example"})
	mailBefore := len(d.smtp.to(adminEmail))
	if dbx.Flip(v2) != 1 {
		t.Fatal("no Dropbox object to change")
	}
	if code, body := d.download(d.atty, fileID, v2); code == http.StatusOK && bytes.Equal(body, v2body) {
		t.Fatal("a changed byte on Dropbox was served")
	}
	if len(d.smtp.to(adminEmail)) <= mailBefore {
		t.Fatal("the admins were not mailed about the changed byte")
	}
	if r := d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+fileID, nil); r.json["id"] != fileID {
		t.Fatal("a file that failed its check is no longer listed")
	}

	// --- Backup and restore of file bytes from every backend (AC-040, AC-050) ---
	dbx.Flip(v2) // put the byte back
	var archive bytes.Buffer
	rep, err := d.app.Files.ExportBytes(ctx, d.adminP(), &archive, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.app.Files.ExportBytes(ctx, domain.Principal{UserID: domain.ID(d.staffID), Roles: []domain.Role{domain.RoleStaff}}, io.Discard, nil); err == nil {
		t.Fatal("a staff user exported file bytes")
	}
	inBackup := map[string]bool{}
	if err := tarEach(archive.Bytes(), func(name string, body []byte) {
		if name == "files-manifest.json" {
			var man []struct {
				ID string `json:"object_id"`
			}
			if err := json.Unmarshal(body, &man); err != nil {
				t.Fatalf("manifest: %v", err)
			}
			for _, o := range man {
				inBackup[o.ID] = true
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	var sealedMD string
	for _, r := range srends {
		if r["kind"] == "markdown" {
			sealedMD = r["id"].(string)
		}
	}
	for _, o := range append(corpus, obj{version: md["id"].(string)}, obj{version: sealedMD}) {
		if !inBackup[o.version] {
			t.Fatalf("object %s (a version or Markdown copy) is not in the backup: %+v", o.version, rep)
		}
	}
	dbx.Wipe()
	if code, _ := d.download(d.atty, fileID, v1); code == http.StatusOK {
		t.Fatal("a wiped backend still served the file")
	}
	restored, err := d.app.Files.RestoreBytes(ctx, d.adminP(), bytes.NewReader(archive.Bytes()))
	if err != nil || restored.Restored < len(corpus) {
		t.Fatalf("restore %+v %v", restored, err)
	}
	readAll()
	if body := d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+fileID+"/renditions/"+md["id"].(string), nil).body; body != mdBody {
		t.Fatal("the restored Markdown copy differs")
	}
}

// testClock is a settable clock for the running core; it starts at the real time so
// sign-in codes made with time.Now stay valid.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock { return &testClock{t: time.Now().UTC()} }

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Microsecond)
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// wordcraft is LawCraft's headless build at the pin (web/editor/build.sh). Without it the
// editor case cannot run; TOGAVSU_REQUIRE_WORDCRAFT_CLI makes that a failure.
func wordcraft(t *testing.T) string {
	t.Helper()
	path := os.Getenv("TOGAVSU_WORDCRAFT_CLI")
	if path == "" {
		if os.Getenv("TOGAVSU_REQUIRE_WORDCRAFT_CLI") != "" {
			t.Fatal("TOGAVSU_WORDCRAFT_CLI is required: build it with web/editor/build.sh")
		}
		t.Skip("TOGAVSU_WORDCRAFT_CLI is not set (build wordcraft-cli with web/editor/build.sh)")
	}
	return path
}

func docText(t *testing.T, cli, ext string, b []byte) string {
	t.Helper()
	in := filepath.Join(t.TempDir(), "doc."+ext)
	if err := os.WriteFile(in, b, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(cli, "text", in).Output()
	if err != nil {
		t.Fatalf("wordcraft-cli text: %v", err)
	}
	return string(out)
}

// TestDocumentsLawCraftEditor is the lawcraft-editor phase case (AC-041, AC-042) on the
// running core with wordcraft-cli at the pinned commit: a DOCX opens on the edit page,
// the page hands the current version to the editor and saves what it returns as a new
// version that passes the Word package check; a second user sees who holds the edit
// lock and can only preview or download, until 5 minutes after the last renewal; New
// Pleading fills its caption from the matter; the Document editor tab and the editor API
// show commit 770a8fab12392485c0c2789fb254da7d9296df4d; an editor run fills merge fields
// as a new version, a run that reads a local file is refused, and a conversion to PDF is
// a new file linked to its source, over the API and /mcp, with walls applied.
func TestDocumentsLawCraftEditor(t *testing.T) {
	cliPath := wordcraft(t)
	clk := newTestClock()
	d := newDocs(t, func(c *Config) {
		c.WordcraftCLI = cliPath
		c.Now = clk.now
		c.NoBackground = true
	})
	ctx := context.Background()
	cli := &editor.CLI{Path: cliPath, TempDir: t.TempDir()}
	mk := func(cmds ...editor.Command) []byte {
		b, err := cli.Run(ctx, editor.RunInput{Commands: cmds, Out: "docx"})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	county := str(t, d.admin.must(http.StatusCreated, "POST", "/api/v1/counties", map[string]any{"name": "Adair", "state": "MO"}).json, "id")
	matter, client := d.matter("Roe Dissolution", "", map[string]any{"case_number": "26AR-DR00123", "court": "Circuit Court", "county_id": county, "client_position": "Petitioner"})
	_ = client
	other := str(t, d.admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{"kind": "person", "given_name": "John", "family_name": "Roe"}).json, "id")
	d.admin.must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": matter, "contact_id": other, "role": "opposing party", "primary": true})
	walled, _ := d.matter("Sealed Guardianship", "", nil)
	d.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{d.adminID, d.attyID}})

	// The pinned commit, on the editor API and the Document editor tab.
	const pin = "770a8fab12392485c0c2789fb254da7d9296df4d"
	if r := d.staff.must(http.StatusOK, "GET", "/api/v1/editor", nil); !strings.Contains(r.body, pin) {
		t.Fatalf("editor info: %s", r.body)
	}
	if r := d.admin.must(http.StatusOK, "GET", "/admin?tab=document_editor", nil); !strings.Contains(r.body, pin) {
		t.Fatalf("the Document editor tab does not show the pinned commit")
	}
	if r := d.admin.must(http.StatusOK, "GET", "/api/v1/settings/editor.lawcraft_commit", nil); r.json["value"] != pin || r.json["locked"] != true {
		t.Fatalf("editor.lawcraft_commit: %s", r.body)
	}

	// --- AC-041: open, edit, save as a new Word-compatible version ---
	original := mk(editor.Command{ID: "text.insert", Parameters: map[string]any{"text": "Synthetic engagement letter."}})
	f := d.upload(d.staff, matter, "Engagement letter.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", original, nil)
	fileID := str(t, f, "id")
	page := "/files/" + fileID + "/edit"
	html := d.staff.must(http.StatusOK, "GET", page, nil).body
	if !strings.Contains(html, `data-mode="edit"`) || !strings.Contains(html, "/editor/?host=parent") || !strings.Contains(html, pin) {
		t.Fatalf("edit page:\n%s", html)
	}
	sent := d.staff.must(http.StatusOK, "GET", page+"/document", nil)
	if sent.body != string(original) {
		t.Fatalf("the edit page sent %d bytes, not the current version", len(sent.body))
	}
	if r := d.staff.must(http.StatusOK, "GET", page+"/context", nil); !strings.Contains(r.body, "law.firm.set") || !strings.Contains(r.body, "26AR-DR00123") {
		t.Fatalf("editor context lacks the firm or matter: %s", r.body)
	}
	edited, err := cli.Run(ctx, editor.RunInput{Document: []byte(sent.body), Ext: "docx", Out: "docx", Commands: []editor.Command{
		{ID: "caret.docEnd"}, {ID: "text.insert", Parameters: map[string]any{"text": " Edited in the editor."}}}})
	if err != nil {
		t.Fatal(err)
	}
	saved := d.staff.raw("POST", page+"/save", edited, "X-File-Name", url.PathEscape("Engagement letter.docx"))
	if saved.code != http.StatusCreated || num(t, saved.json, "number") != 2 || saved.json["how"] != "editor" {
		t.Fatalf("save: %d %s", saved.code, saved.body)
	}
	vs := items(t, d.staff.must(http.StatusOK, "GET", "/api/v1/files/"+fileID+"/versions", nil))
	if len(vs) != 2 || vs[0]["sha256"] == vs[1]["sha256"] {
		t.Fatalf("versions %v", vs)
	}
	var cur string
	for _, v := range vs {
		if num(t, v, "number") == 2 {
			cur = v["id"].(string)
		}
	}
	_, stored := d.download(d.staff, fileID, cur)
	if err := editor.CheckDOCX(stored); err != nil {
		t.Fatalf("the saved version would open with a repair prompt: %v", err)
	}
	if txt := docText(t, cliPath, "docx", stored); !strings.Contains(txt, "Synthetic engagement letter. Edited in the editor.") {
		t.Fatalf("saved text %q", txt)
	}
	if _, first := d.download(d.staff, fileID, str(t, f, "current_version_id")); !bytes.Equal(first, original) {
		t.Fatal("version 1 changed")
	}

	// A second user sees the lock, can preview and download, and cannot save.
	locked := d.atty.must(http.StatusOK, "GET", page, nil).body
	if !strings.Contains(locked, "<strong>Sam Staff</strong> is editing this file, since") || strings.Contains(locked, "<iframe") || !strings.Contains(locked, ">Download</a>") {
		t.Fatalf("locked page for the second user:\n%s", locked)
	}
	if r := d.atty.do("POST", "/api/v1/files/"+fileID+"/lock", nil); r.code != http.StatusConflict || !strings.Contains(r.body, "Sam Staff") {
		t.Fatalf("second lock: %d %s", r.code, r.body)
	}
	if r := d.atty.raw("POST", page+"/save", edited, "X-File-Name", "x.docx"); r.code != http.StatusConflict {
		t.Fatalf("the second user saved: %d", r.code)
	}
	if code, _ := d.download(d.atty, fileID, cur); code != http.StatusOK {
		t.Fatalf("download while locked: %d", code)
	}
	// The editor closes without saving: no renewals. Just under 5 minutes it holds...
	clk.advance(5*time.Minute - 5*time.Second)
	if r := d.atty.do("POST", "/api/v1/files/"+fileID+"/lock", nil); r.code != http.StatusConflict {
		t.Fatalf("lock freed before 5 minutes: %d %s", r.code, r.body)
	}
	// ...and 5 minutes after the last renewal it is free.
	clk.advance(10 * time.Second)
	if r := d.atty.must(http.StatusOK, "POST", "/api/v1/files/"+fileID+"/lock", nil); r.json["user_id"] != d.attyID {
		t.Fatalf("after 5 minutes: %s", r.body)
	}
	d.atty.must(http.StatusNoContent, "DELETE", "/api/v1/files/"+fileID+"/lock", nil)

	// New Pleading fills its caption from the matter.
	d.staff.must(http.StatusOK, "GET", "/matters/"+matter+"/pleadings/new", nil)
	form := url.Values{"type": {"motion"}, "title": {"Motion to Continue"}, "csrf": {d.staff.csrf}}
	if r := d.staff.do("POST", "/matters/"+matter+"/pleadings", form); r.code != http.StatusBadRequest || !strings.Contains(r.body, "needs the firm address") {
		t.Fatalf("New Pleading without firm details: %d", r.code)
	}
	for k, v := range map[string]string{"firm.address": "1 Main St, Kirksville, MO 63501", "firm.phone": "+16605550100", "firm.email": "office@example.invalid"} {
		d.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/"+k, map[string]any{"value": v})
	}
	// /api/v1/users (where an admin sets a bar number) is a later integration; the
	// synthetic bar number is written as a fixture.
	if _, err := d.app.Pool.Exec(`UPDATE users SET bar_number = '12345', bar_state = 'MO' WHERE id = $1`, d.attyID); err != nil {
		t.Fatal(err)
	}
	pr := d.staff.do("POST", "/matters/"+matter+"/pleadings", form)
	if pr.code != http.StatusSeeOther || !strings.HasPrefix(pr.loc, "/files/") || !strings.HasSuffix(pr.loc, "/edit") {
		t.Fatalf("new pleading: %d %s %s", pr.code, pr.loc, pr.body)
	}
	pleadingID := strings.TrimSuffix(strings.TrimPrefix(pr.loc, "/files/"), "/edit")
	pf := d.staff.must(http.StatusOK, "GET", "/api/v1/files/"+pleadingID, nil).json
	pvs := items(t, d.staff.must(http.StatusOK, "GET", "/api/v1/files/"+pleadingID+"/versions", nil))
	_, pb := d.download(d.staff, pleadingID, pvs[0]["id"].(string))
	if err := editor.CheckDOCX(pb); err != nil {
		t.Fatal(err)
	}
	ptxt := docText(t, cliPath, "docx", pb)
	for _, want := range []string{"ADAIR COUNTY", "Case No. 26AR-DR00123", "Motion to Continue", "Petitioner", "JOHN ROE", "Respondent"} {
		if !strings.Contains(ptxt, want) {
			t.Errorf("the pleading lacks %q:\n%s", want, ptxt)
		}
	}
	if pf["matter_id"] != matter || pf["name"] != "Motion to Continue.docx" {
		t.Fatalf("pleading file %v", pf)
	}
	if r := d.staff.do("GET", "/matters/"+walled+"/pleadings/new", nil); r.code != http.StatusNotFound {
		t.Fatalf("staff New Pleading on a walled matter: %d", r.code)
	}

	// --- AC-042: runs and conversions ---
	letter := mk(
		editor.Command{ID: "text.insert", Parameters: map[string]any{"text": "Dear "}},
		editor.Command{ID: "insert.field", Parameters: map[string]any{"instr": "MERGEFIELD ClientName"}},
		editor.Command{ID: "text.insert", Parameters: map[string]any{"text": ", your hearing is on "}},
		editor.Command{ID: "insert.field", Parameters: map[string]any{"instr": "MERGEFIELD HearingDate"}},
		editor.Command{ID: "text.insert", Parameters: map[string]any{"text": "."}},
	)
	lf := d.upload(d.staff, matter, "Hearing letter.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", letter, nil)
	letterID := str(t, lf, "id")
	merge := []map[string]any{
		{"id": "mailings.start", "parameters": map[string]any{"kind": "letters"}},
		{"id": "mailings.recipients", "parameters": map[string]any{"csv": "ClientName,HearingDate\nJane Roe,December 3 2026"}},
		{"id": "mailings.finish"},
	}
	run := d.staff.must(http.StatusCreated, "POST", "/api/v1/files/"+letterID+"/editor-runs", map[string]any{"commands": merge, "save_as": "version"}).json
	if num(t, run, "version", "number") != 2 || run["version"].(map[string]any)["how"] != "editor_run" {
		t.Fatalf("run %v", run)
	}
	_, merged := d.download(d.staff, letterID, str(t, run, "version", "id"))
	if txt := docText(t, cliPath, "docx", merged); !strings.Contains(txt, "Dear Jane Roe, your hearing is on December 3 2026.") {
		t.Fatalf("merged text %q", txt)
	}
	if _, v1 := d.download(d.staff, letterID, str(t, lf, "current_version_id")); !bytes.Equal(v1, letter) {
		t.Fatal("the run changed version 1")
	}
	for name, cmds := range map[string][]map[string]any{
		"text from a local file": {{"id": "insert.textFromFile", "parameters": map[string]any{"path": "/etc/hostname"}}},
		"open a local file":      {{"id": "file.open", "parameters": map[string]any{"path": "/etc/passwd"}}},
		"recipients from a file": {merge[0], {"id": "mailings.recipients", "parameters": map[string]any{"path": "/etc/passwd"}}, merge[2]},
	} {
		r := d.staff.do("POST", "/api/v1/files/"+letterID+"/editor-runs", map[string]any{"commands": cmds, "save_as": "version"})
		if r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "command_refused") {
			t.Fatalf("%s: %d %s", name, r.code, r.body)
		}
	}
	if n := len(items(t, d.staff.must(http.StatusOK, "GET", "/api/v1/files/"+letterID+"/versions", nil))); n != 2 {
		t.Fatalf("a refused run saved: %d versions", n)
	}
	if n := d.count(`SELECT COUNT(*) FROM audit_log WHERE entity_id = $1 AND field = 'editor_run'`, letterID); n != 4 {
		t.Fatalf("%d editor_run audit rows, want 1 run and 3 refusals", n)
	}
	// The same refusal over /mcp.
	call := mcpCall(d.staff, toolName(t, "POST", "/api/v1/files/{file_id}/editor-runs"), map[string]any{"path": map[string]any{"file_id": letterID},
		"body": map[string]any{"commands": []map[string]any{{"id": "file.open", "parameters": map[string]any{"path": "/etc/passwd"}}}, "save_as": "version"}})
	if !strings.Contains(call.body, `"isError":true`) || !strings.Contains(call.body, "command_refused") {
		t.Fatalf("MCP refused run: %s", call.body)
	}
	if r := d.staff.must(http.StatusOK, "GET", "/api/v1/editor/commands", nil); strings.Contains(r.body, `"file.open"`) || !strings.Contains(r.body, "mailings.recipients") {
		t.Fatalf("allowed commands: %s", r.body)
	}
	conv := d.staff.must(http.StatusCreated, "POST", "/api/v1/files/"+letterID+"/conversions", map[string]any{"format": "pdf"}).json
	pdfID := str(t, conv, "file", "id")
	if pdfID == letterID || !strings.HasSuffix(str(t, conv, "file", "name"), ".pdf") {
		t.Fatalf("conversion %v", conv)
	}
	_, pdf := d.download(d.staff, pdfID, str(t, conv, "file", "current_version_id"))
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("the conversion is not a PDF: %q", pdf[:min(16, len(pdf))])
	}
	links := d.staff.must(http.StatusOK, "GET", "/api/v1/files/"+pdfID+"/links", nil).body
	if !strings.Contains(links, letterID) || !strings.Contains(links, "conversion") {
		t.Fatalf("the PDF is not linked to its source: %s", links)
	}
	// Walls and roles apply: staff can't run on a walled file, the accountant has no editor.
	sealed := d.upload(d.atty, walled, "Sealed letter.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", letter, nil)
	if r := d.staff.do("POST", "/api/v1/files/"+str(t, sealed, "id")+"/editor-runs", map[string]any{"commands": merge, "save_as": "version"}); r.code != http.StatusNotFound {
		t.Fatalf("staff run on a walled file: %d %s", r.code, r.body)
	}
	if r := d.staff.do("GET", "/files/"+str(t, sealed, "id")+"/edit", nil); r.code != http.StatusNotFound {
		t.Fatalf("staff edit page of a walled file: %d", r.code)
	}
	if r := d.acct.do("POST", "/api/v1/files/"+letterID+"/editor-runs", map[string]any{"commands": merge, "save_as": "version"}); r.code != http.StatusForbidden && r.code != http.StatusNotFound {
		t.Fatalf("accountant run: %d %s", r.code, r.body)
	}
}

// waitFor polls cond every 100 ms until it holds or limit passes.
func waitFor(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %v", what, limit)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// filesTabRow is the file's row on the matter's Files tab as c sees the page.
func (d *docs) filesTabRow(c *client, matter, file string) string {
	d.t.Helper()
	body := c.must(http.StatusOK, "GET", "/practice/matters/"+matter+"/files", nil).body
	for _, row := range strings.Split(body, "<tr>") {
		if strings.Contains(row, "/api/v1/files/"+file+"/") {
			return row
		}
	}
	d.t.Fatalf("file %s is not on the Files tab:\n%s", file, body)
	return ""
}

// dtStatus is a file's Doc Tender status as c sees it.
func (d *docs) dtStatus(c *client, file string) map[string]any {
	d.t.Helper()
	return c.must(http.StatusOK, "GET", "/api/v1/files/"+file+"/doc-tender-status", nil).json
}

// markdown is the file's newest Markdown copy (rendition row and body) as c sees it.
func (d *docs) markdown(c *client, file string) (map[string]any, string) {
	d.t.Helper()
	var md map[string]any
	for _, r := range items(d.t, c.must(http.StatusOK, "GET", "/api/v1/files/"+file+"/renditions", nil)) {
		if r["kind"] == "markdown" && (md == nil || num(d.t, r, "number") > num(d.t, md, "number")) {
			md = r
		}
	}
	if md == nil {
		d.t.Fatalf("file %s has no Markdown copy", file)
	}
	return md, c.must(http.StatusOK, "GET", "/api/v1/files/"+file+"/renditions/"+md["id"].(string), nil).body
}

// folderName is the name of the folder a file is filed in.
func (d *docs) folderName(c *client, matter, folder string) string {
	d.t.Helper()
	for _, f := range items(d.t, c.must(http.StatusOK, "GET", "/api/v1/folders?matter_id="+matter, nil)) {
		if f["id"] == folder {
			return f["name"].(string)
		}
	}
	return ""
}

// token issues a bearer token as the admin and returns a client using it.
func (d *docs) token(body map[string]any) *client {
	d.t.Helper()
	r := d.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", body)
	c := newClient(d.t, d.srv.URL)
	c.bearer = str(d.t, r.json, "secret")
	return c
}

const motionText = "MOTION TO WITHDRAW\nFiled 2026-10-15 in case 23AR-CV00825.\nCounsel for the petitioner asks leave to withdraw.\n"

// TestDocumentsDocTenderCorpus is the doctender-corpus phase case (AC-043 to AC-052)
// on the running core, with the generated synthetic corpus and a synthetic provider on
// the OpenAI wire. "arrival" runs the core's own background worker and drop-folder
// scan; "corpus" steps the worker on a settable clock to exercise retries and grouping.
func TestDocumentsDocTenderCorpus(t *testing.T) {
	t.Run("arrival", testDocTenderArrival)
	t.Run("corpus", testDocTenderCorpus)
}

// AC-043 (staff and drop-folder arrivals): each is processed by the running worker within
// 10 minutes of arrival with no nightly run, and each comes out with the pattern name as
// its only name, a category, a summary, its folder and a saved Markdown copy. The client
// upload and the module-written file need the module runtime (pending); the core
// refuses both writes today.
func testDocTenderArrival(t *testing.T) {
	prov := NewProvider()
	defer prov.Close()
	d := newDocs(t, func(c *Config) { c.ProviderURLs = map[string]string{"openai": prov.URL()} })
	d.useProvider()
	d.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/files.drop_steady_seconds", map[string]any{"value": 1})
	matter, clientContact := d.matter("Example Dissolution", "", map[string]any{"case_number": "23AR-CV00825"})
	number := str(t, d.admin.must(http.StatusOK, "GET", "/api/v1/matters/"+matter, nil).json, "number")
	clientUser, err := d.app.Identity.CreateUser(context.Background(), d.adminCaller(), identity.NewUser{Name: "Cleo Client", Email: "cleo@client.example",
		Password: "synthetic-password-cleo", Roles: []domain.Role{domain.RoleClient}, ContactID: domain.ID(clientContact)})
	if err != nil {
		t.Fatal(err)
	}
	_ = clientUser
	// A client user signs in with a session (two-factor is optional for clients); client
	// tokens carry no files:write scope.
	asClient := newClient(t, d.srv.URL)
	login := asClient.must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": "cleo@client.example", "password": "synthetic-password-cleo"})
	if str(t, login.json, "status") != "authenticated" {
		t.Fatalf("client sign-in: %s", login.body)
	}
	asClient.csrf = str(t, login.json, "csrf_token")
	asModule := d.token(map[string]any{"name": "sample module", "module_id": "sample-module", "scopes": []string{"files:read", "files:write"},
		"approved_scopes": []string{"files:read", "files:write"}})
	share := filepath.Join(d.cfg.DataDir, "share")
	d.admin.must(http.StatusCreated, "POST", "/api/v1/storage-backends", map[string]any{"kind": "network_path", "name": "Office NAS", "drop_folder": true, "settings": map[string]any{"path": share}})

	arrived := time.Now()
	var got []string
	body := func(date string) []byte { return []byte(strings.Replace(motionText, "2026-10-15", date, 1)) }
	got = append(got, str(t, d.upload(d.staff, matter, "Scan_0001 final(2).txt", "text/plain", body("2026-10-15"), nil), "id"))
	// Client users are read-only in the core (PRD 4); client uploads arrive through the
	// client portal module (PRD 8.19), a later phase. A direct client write is refused.
	if r := asClient.do("POST", "/api/v1/file-uploads", map[string]any{"file_name": "from client.txt", "matter_id": matter, "size_bytes": 10, "media_type": "text/plain"}); r.code != http.StatusForbidden {
		t.Fatalf("a client user's direct upload: %d %s", r.code, r.body)
	}
	// A module-bound token has no user and no roles until the module runtime grants a
	// module its manifest's permissions (PRD 9.3), a later phase; today its write is refused.
	if r := asModule.do("POST", "/api/v1/file-uploads", map[string]any{"file_name": "module output.txt", "matter_id": matter, "size_bytes": 10, "media_type": "text/plain"}); r.code != http.StatusForbidden {
		t.Fatalf("a module token's upload: %d %s", r.code, r.body)
	}
	dropDir := filepath.Join(share, "drop", number)
	waitFor(t, time.Minute, "the drop folder's matter subfolder", func() bool { _, err := os.Stat(dropDir); return err == nil })
	if err := os.WriteFile(filepath.Join(dropDir, "dropped.txt"), body("2026-10-18"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Minute, "the drop folder take", func() bool {
		for _, f := range items(t, d.atty.must(http.StatusOK, "GET", "/api/v1/files?matter_id="+matter, nil)) {
			if f["source"] == "drop_folder" {
				got = append(got, f["id"].(string))
				return true
			}
		}
		return false
	})
	wantSource := []string{"upload", "drop_folder"}
	for k, id := range got {
		waitFor(t, 10*time.Minute, "processing of the "+wantSource[k]+" file", func() bool { return d.dtStatus(d.atty, id)["status"] == "published" })
		f := d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+id, nil).json
		want := "dissolution-" + number + "-motion-to-withdraw-2026-10-" + []string{"15", "18"}[k] + ".txt"
		if f["name"] != want || f["source"] != wantSource[k] || f["arrived_as"] == f["name"] {
			t.Fatalf("%s file: name %v (want %s), source %v, arrived as %v", wantSource[k], f["name"], want, f["source"], f["arrived_as"])
		}
		if f["category_id"] != "court_filing" || f["folder_id"] == nil || d.folderName(d.atty, matter, f["folder_id"].(string)) != "Pleadings" {
			t.Fatalf("%s file: category %v, folder %v", wantSource[k], f["category_id"], f["folder_id"])
		}
		res := d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+id+"/doc-tender-result", nil).json
		if s, _ := res["summary"].(string); s == "" {
			t.Fatalf("%s file: no summary in %v", wantSource[k], res)
		}
		md, mdBody := d.markdown(d.atty, id)
		if md["name"] != strings.TrimSuffix(want, ".txt")+".md" || !strings.Contains(mdBody, "Counsel for the petitioner") {
			t.Fatalf("%s file: Markdown %v:\n%s", wantSource[k], md["name"], mdBody)
		}
	}
	if took := time.Since(arrived); took > 10*time.Minute {
		t.Fatalf("processing took %v", took)
	}
	// No nightly run was involved: the sweep finds nothing left to queue.
	if n, err := d.app.DocTender.Sweep(context.Background()); err != nil || n != 0 {
		t.Fatalf("the nightly sweep found %d files unprocessed (%v)", n, err)
	}
	// The client sees none of the firm's unshared files.
	if r := asClient.do("GET", "/api/v1/files?matter_id="+matter, nil); strings.Contains(r.body, got[0]) || strings.Contains(r.body, got[1]) || strings.Contains(r.body, "dissolution-") {
		t.Fatalf("client file list: %d %s", r.code, r.body)
	}
}

func (d *docs) adminCaller() identity.Caller {
	d.t.Helper()
	u, _ := url.Parse(d.srv.URL)
	var session string
	for _, c := range d.admin.http.Jar.Cookies(u) {
		if c.Name == sessionCookie {
			session = c.Value
		}
	}
	caller, err := d.app.Identity.Authenticate(context.Background(), identity.Presented{SessionToken: session})
	if err != nil {
		d.t.Fatal(err)
	}
	return caller
}

// unitRows are a job's units (name, status, attempts) in order, read from Postgres.
func (d *docs) unitRows(file string) []map[string]any {
	d.t.Helper()
	rows, err := d.app.Pool.Query(`SELECT u.unit, u.status, u.attempts, u.text, u.check_method, u.check_result FROM doc_tender_units u
JOIN doc_tender_jobs j ON j.id = u.job_id WHERE j.file_id = $1 AND j.kind = 'file' ORDER BY j.created_at DESC, u.ordinal`, file)
	if err != nil {
		d.t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var name, status, text, method, result string
		var attempts int
		if err := rows.Scan(&name, &status, &attempts, &text, &method, &result); err != nil {
			d.t.Fatal(err)
		}
		out = append(out, map[string]any{"unit": name, "status": status, "attempts": attempts, "text": text, "check_method": method, "check_result": result})
	}
	return out
}

// AC-044 to AC-052 on one install, stepping the worker on a settable clock.
func testDocTenderCorpus(t *testing.T) {
	prov := NewProvider()
	defer prov.Close()
	clk := newTestClock()
	d := newDocs(t, func(c *Config) {
		c.ProviderURLs = map[string]string{"openai": prov.URL()}
		c.NoBackground = true
		c.Now = clk.now
	})
	ctx := context.Background()
	matter, _ := d.matter("Example Dissolution", "", map[string]any{"case_number": "23AR-CV00825"})
	number := str(t, d.admin.must(http.StatusOK, "GET", "/api/v1/matters/"+matter, nil).json, "number")
	walled, _ := d.matter("Sealed Guardianship", "", nil)
	d.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{d.adminID, d.attyID}})

	// Before AI is set up every file is stored, listed and held (PRD 8.21).
	held := str(t, d.upload(d.staff, matter, "early.txt", "text/plain", []byte(motionText), nil), "id")
	d.drain()
	if st := d.dtStatus(d.atty, held); st["status"] != "held_for_ai_setup" || len(prov.Calls()) != 0 {
		t.Fatalf("before AI setup: %v, %d model calls", st, len(prov.Calls()))
	}
	d.useProvider()
	d.drain()
	if st := d.dtStatus(d.atty, held); st["status"] != "published" {
		t.Fatalf("after AI setup: %v", st)
	}

	// --- AC-052: labeling on gpt-6.1-sol, no local endpoint for labeling or categorizing ---
	var model string
	if err := d.app.Pool.QueryRow(`SELECT model FROM ai_call_log WHERE task = 'labeling' ORDER BY created_at LIMIT 1`).Scan(&model); err != nil || model != "gpt-6.1-sol" {
		t.Fatalf("labeling ran on %q (%v), want gpt-6.1-sol", model, err)
	}
	if prov.Count("labeling:gpt-6.1-sol") == 0 {
		t.Fatalf("the provider saw no labeling call on gpt-6.1-sol: %v", prov.Calls())
	}
	// The AI call log shows it: over the API, filtered by task and provider and paged by
	// cursor, over the admin's MCP, and on the AI and inference tab. Staff and the
	// accountant are refused.
	logged := d.admin.must(http.StatusOK, "GET", "/api/v1/ai/calls?task=labeling&provider=openai", nil)
	labeled := items(t, logged)
	if len(labeled) == 0 {
		t.Fatalf("no labeling call in the log: %s", logged.body)
	}
	for _, c := range labeled {
		if c["task"] != "labeling" || c["provider"] != "openai" || c["model"] != "gpt-6.1-sol" || c["requester"] != "system" {
			t.Fatalf("labeling log row: %v", c)
		}
	}
	if none := items(t, d.admin.must(http.StatusOK, "GET", "/api/v1/ai/calls?task=labeling&provider=anthropic", nil)); len(none) != 0 {
		t.Fatalf("provider filter: %v", none)
	}
	all := items(t, d.admin.must(http.StatusOK, "GET", "/api/v1/ai/calls", nil))
	if len(all) < 2 {
		t.Fatalf("want at least two logged calls to page: %d", len(all))
	}
	var paged []any
	cursor := ""
	for i := 0; i <= len(all); i++ {
		path := "/api/v1/ai/calls?limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		pg := d.admin.must(http.StatusOK, "GET", path, nil)
		for _, c := range items(t, pg) {
			paged = append(paged, c["id"])
		}
		next, _ := pg.json["page"].(map[string]any)["next_cursor"].(string)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(paged) != len(all) || paged[0] != all[0]["id"] || paged[len(paged)-1] != all[len(all)-1]["id"] {
		t.Fatalf("cursor pages %v differ from the full list of %d", paged, len(all))
	}
	for _, bad := range []string{"cursor=not-a-cursor", "limit=0", "limit=100000", "from_time=yesterday"} {
		if r := d.admin.do("GET", "/api/v1/ai/calls?"+bad, nil); r.code != http.StatusBadRequest {
			t.Fatalf("GET /api/v1/ai/calls?%s answered %d", bad, r.code)
		}
	}
	for _, c := range []*client{d.staff, d.acct, d.atty} {
		if r := c.do("GET", "/api/v1/ai/calls", nil); r.code != http.StatusForbidden {
			t.Fatalf("a non-admin read the AI call log: %d", r.code)
		}
		if r := c.do("GET", "/admin?tab=ai", nil); r.code != http.StatusForbidden {
			t.Fatalf("a non-admin opened the AI tab: %d", r.code)
		}
	}
	if mcp := mcpCall(d.admin, toolName(t, "GET", "/api/v1/ai/calls"), map[string]any{"query": map[string]any{"task": "labeling"}}); !strings.Contains(mcp.body, "gpt-6.1-sol") || strings.Contains(mcp.body, `"isError":true`) {
		t.Fatalf("call log over MCP: %s", mcp.body)
	}
	if mcp := mcpCall(d.staff, toolName(t, "GET", "/api/v1/ai/calls"), map[string]any{}); strings.Contains(mcp.body, "gpt-6.1-sol") {
		t.Fatalf("staff read the call log over MCP: %s", mcp.body)
	}
	tab := d.admin.must(http.StatusOK, "GET", "/admin?tab=ai&task=labeling", nil)
	if !strings.Contains(tab.body, `id="ai-call-log"`) || !strings.Contains(tab.body, `<td>labeling</td><td>openai</td><td><code>gpt-6.1-sol</code></td>`) {
		t.Fatalf("the AI tab does not show the labeling call:\n%s", tab.body)
	}
	if tab := d.admin.must(http.StatusOK, "GET", "/admin?tab=ai&task=ocr&provider=anthropic", nil); strings.Contains(tab.body, `id="call-`) || !strings.Contains(tab.body, "No AI calls match.") {
		t.Fatal("the AI tab's filter still lists calls")
	}
	choices := d.admin.must(http.StatusOK, "GET", "/api/v1/doc-tender/model-choices", nil).json
	for _, task := range []string{"labeling", "categorizing"} {
		list, _ := choices[task].([]any)
		if len(list) == 0 {
			t.Fatalf("no %s choices: %v", task, choices)
		}
		for _, c := range list {
			if strings.HasPrefix(c.(string), "local") {
				t.Fatalf("the AI tab offers %s for %s", c, task)
			}
		}
	}
	for _, key := range []string{"ai.labeling_allow_local", "ai.categorizing_allow_local"} {
		if r := d.admin.do("PATCH", "/api/v1/settings/"+key, map[string]any{"value": true}); r.code < 400 {
			t.Fatalf("%s could be turned on: %d", key, r.code)
		}
	}

	// --- AC-044: 120 scanned pages, a failure injected at page 49 resumes at page 49 ---
	prov.FailOCR("PAGE 49 OF 120", 2)
	scan := str(t, d.upload(d.staff, matter, "exhibit-scan.pdf", "application/pdf", ScannedPDF(120), nil), "id")
	d.drain()
	units := d.unitRows(scan)
	var pages []map[string]any
	for _, u := range units {
		if strings.HasPrefix(u["unit"].(string), "page ") {
			pages = append(pages, u)
		}
	}
	if len(pages) != 120 {
		t.Fatalf("%d page units, want 120", len(pages))
	}
	for _, u := range pages[:48] {
		if u["status"] != "passed" || u["attempts"] != 1 {
			t.Fatalf("%v before the failure", u)
		}
	}
	if pages[48]["status"] != "pending" || pages[48]["attempts"] != 2 {
		t.Fatalf("page 49 after the injected failures: %v", pages[48])
	}
	for _, u := range pages[49:] {
		if u["attempts"] != 0 {
			t.Fatalf("%v started before page 49 passed", u["unit"])
		}
	}
	readsBefore := len(prov.OCRReads())
	clk.advance(15 * time.Minute)
	d.drain()
	after := prov.OCRReads()[readsBefore:]
	if len(after) == 0 || !strings.Contains(after[0], "PAGE 49 OF 120") {
		t.Fatalf("the resumed job read %q first, want page 49", after[:min(1, len(after))])
	}
	for _, r := range after {
		for k := 1; k <= 48; k++ {
			if strings.Contains(r, fmt.Sprintf("PAGE %d OF 120\n", k)) {
				t.Fatalf("page %d was read again after the resume", k)
			}
		}
	}
	if st := d.dtStatus(d.atty, scan); st["status"] != "published" {
		t.Fatalf("scan job %v", st)
	}
	// With DOCTENDER_REQUIRE_TESSERACT (the core image's run, scripts/ci/test-core-image-cases.sh)
	// every page's second read is that Tesseract's, not the check model's.
	_, tessErr := exec.LookPath("tesseract")
	requireTess := os.Getenv("DOCTENDER_REQUIRE_TESSERACT") != ""
	if requireTess && tessErr != nil {
		t.Fatal("tesseract is required (DOCTENDER_REQUIRE_TESSERACT is set) and is not on PATH")
	}
	for k, u := range d.unitRows(scan)[:120] {
		if u["status"] != "passed" || !strings.Contains(u["text"].(string), fmt.Sprintf("PAGE %d OF 120", k+1)) || u["check_method"] == "" {
			t.Fatalf("page %d: %v", k+1, u)
		}
		if requireTess && u["check_method"] != "tesseract" {
			t.Fatalf("page %d was cross-checked by %v, want tesseract", k+1, u["check_method"])
		}
	}
	_, scanMD := d.markdown(d.atty, scan)
	if !strings.Contains(scanMD, "## Page 49\n\nEXHIBIT SCAN\nPAGE 49 OF 120") || strings.Contains(scanMD, "unresolved") {
		t.Fatalf("scan Markdown:\n%.600s", scanMD)
	}

	// --- AC-050: the Markdown copy is what Files search matches; walls; base name ---
	if hits := ids(items(t, d.staff.must(http.StatusOK, "GET", "/api/v1/files?q=PLAINTIFF+EXHIBIT+FOR+HEARING", nil))); !hits[scan] {
		t.Fatalf("search for text only the scanned pixels hold: %v", hits)
	}
	sealedScan := str(t, d.upload(d.atty, walled, "sealed-scan.pdf", "application/pdf", ScannedPDF(1), nil), "id")
	d.drain()
	smd, _ := d.markdown(d.atty, sealedScan)
	sname := str(t, d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+sealedScan, nil).json, "name")
	if smd["name"] != strings.TrimSuffix(sname, ".pdf")+".md" {
		t.Fatalf("Markdown copy %v of %s", smd["name"], sname)
	}
	if r := d.staff.do("GET", "/api/v1/files/"+sealedScan+"/renditions/"+smd["id"].(string), nil); r.code != http.StatusNotFound {
		t.Fatalf("staff outside the wall read its Markdown copy: %d", r.code)
	}
	if r := d.staff.must(http.StatusOK, "GET", "/api/v1/files?q=PLAINTIFF+EXHIBIT+FOR+HEARING", nil); strings.Contains(r.body, sealedScan) {
		t.Fatal("staff search matched the walled Markdown copy")
	}
	if r := d.staff.do("GET", "/api/v1/files/"+sealedScan+"/doc-tender-result", nil); r.code != http.StatusNotFound {
		t.Fatalf("staff read the walled result: %d", r.code)
	}
	var archive bytes.Buffer
	if _, err := d.app.Files.ExportBytes(ctx, d.adminP(), &archive, nil); err != nil {
		t.Fatal(err)
	}
	inBackup := map[string]bool{}
	_ = tarEach(archive.Bytes(), func(name string, _ []byte) { inBackup[strings.TrimPrefix(name, "objects/")] = true })
	if !inBackup[smd["id"].(string)] {
		t.Fatal("the walled file's Markdown copy is not in the backup")
	}
	// Each critical field is cited to the page it was read on.
	res := d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+held+"/doc-tender-result", nil).json
	fields, _ := res["fields"].([]any)
	if len(fields) != 1 || num(t, fields[0].(map[string]any), "citation", "page") != 1 || fields[0].(map[string]any)["value"] != "23AR-CV00825" {
		t.Fatalf("cited fields %v", res["fields"])
	}

	// --- AC-045: eight photos of a 3-page letter and a 5-page lease ---
	var photos []string
	for k, p := range LetterLeasePhotos() {
		photos = append(photos, str(t, d.upload(d.staff, matter, fmt.Sprintf("IMG_%04d.png", 100+k), "image/png", p, nil), "id"))
	}
	d.drain()
	clk.advance(11 * time.Minute) // no new photos for 10 minutes: the matter's photos are grouped
	d.drain()
	groups := map[string]string{}
	for _, f := range items(t, d.atty.must(http.StatusOK, "GET", "/api/v1/files?matter_id="+matter, nil)) {
		if f["extension"] != "pdf" {
			continue
		}
		// A grouped PDF's version is saved "how: grouping" (file_versions.how).
		for _, v := range items(t, d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+f["id"].(string)+"/versions", nil)) {
			if v["how"] == "grouping" {
				groups[f["id"].(string)] = f["name"].(string)
			}
		}
	}
	if len(groups) != 2 {
		t.Fatalf("%d grouped PDFs, want 2: %v", len(groups), groups)
	}
	var sawLetter, sawLease bool
	for id := range groups {
		_, md := d.markdown(d.atty, id)
		switch {
		case strings.Contains(md, "LETTER"):
			sawLetter = true
			if !inOrder(md, "LETTER\nPAGE 1 OF 3", "LETTER\nPAGE 2 OF 3", "LETTER\nPAGE 3 OF 3") || strings.Contains(md, "LEASE") {
				t.Fatalf("letter Markdown out of order:\n%s", md)
			}
		case strings.Contains(md, "LEASE"):
			sawLease = true
			if !inOrder(md, "PAGE 1 OF 5", "PAGE 2 OF 5", "PAGE 3 OF 5", "PAGE 4 OF 5", "PAGE 5 OF 5") || strings.Contains(md, "LETTER") {
				t.Fatalf("lease Markdown out of order:\n%s", md)
			}
		}
		links := d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+id+"/links", nil).body
		n := 0
		for _, p := range photos {
			if strings.Contains(links, p) {
				n++
			}
		}
		if n != 3 && n != 5 {
			t.Fatalf("grouped PDF %s links %d photos: %s", id, n, links)
		}
	}
	if !sawLetter || !sawLease {
		t.Fatalf("groups %v", groups)
	}

	// --- AC-046: audio and video transcripts with timestamps; the video adds the screen ---
	audio := str(t, d.upload(d.staff, matter, "voicemail.wav", "audio/wav", ToneWAV(On(3)), nil), "id")
	video := str(t, d.upload(d.staff, matter, "bodycam.avi", "video/x-msvideo",
		FramesAVI([]color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}}), nil), "id")
	d.drain()
	for _, id := range []string{audio, video} {
		if st := d.dtStatus(d.atty, id); st["status"] != "published" {
			t.Fatalf("media %s: %v", id, st)
		}
	}
	_, amd := d.markdown(d.atty, audio)
	_, vmd := d.markdown(d.atty, video)
	if !strings.Contains(amd, "00:00") || !strings.Contains(amd, "steady tone") {
		t.Fatalf("audio transcript:\n%s", amd)
	}
	if !strings.Contains(vmd, "00:00") || !strings.Contains(vmd, "steady tone") || !strings.Contains(vmd, "On screen") || !strings.Contains(vmd, "red") || !strings.Contains(vmd, "blue") {
		t.Fatalf("video report:\n%s", vmd)
	}

	// --- AC-047: a closed matter makes no model call; reopening processes the file ---
	closing, _ := d.matter("Closed Estate", "", nil)
	d.admin.must(http.StatusOK, "POST", "/api/v1/matters/"+closing+"/close", map[string]any{"reason": "completed", "confirm_warnings": true})
	calls := len(prov.Calls())
	shut := str(t, d.upload(d.staff, closing, "late letter.txt", "text/plain", []byte("LETTER FROM THE COURT\nDated 2026-11-01.\n"), nil), "id")
	d.drain()
	if st := d.dtStatus(d.atty, shut); st["status"] != "not_processed_matter_closed" || !strings.Contains(fmt.Sprint(st["label"]), "matter closed") {
		t.Fatalf("closed matter file: %v", st)
	}
	if n := len(prov.Calls()); n != calls {
		t.Fatalf("a closed matter's file made %d model calls", n-calls)
	}
	if row := d.filesTabRow(d.atty, closing, shut); !strings.Contains(row, "Not processed (matter closed)") {
		t.Fatalf("the closed matter's Files tab does not say Not processed (matter closed):\n%s", row)
	}
	if code, _ := d.download(d.atty, shut, str(t, d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+shut, nil).json, "current_version_id")); code != http.StatusOK {
		t.Fatalf("the closed matter's file is not downloadable: %d", code)
	}
	d.admin.must(http.StatusOK, "POST", "/api/v1/matters/"+closing+"/reopen", map[string]any{"reason": "new filing arrived"})
	d.drain()
	if st := d.dtStatus(d.atty, shut); st["status"] != "published" {
		t.Fatalf("after reopening: %v", st)
	}

	// --- AC-048: password-protected, damaged and low-confidence files ---
	d.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/mail.smtp_from", map[string]any{"value": "doctender@firm.example"})
	mailBefore := len(d.smtp.to(adminEmail))
	prov.Label("UNCERTAIN SCRIBBLE", "maybe a note", 0.42, "")
	bad := map[string]string{
		"password_protected": str(t, d.upload(d.staff, matter, "settlement.pdf", "application/pdf", EncryptedPDF("open-sesame"), nil), "id"),
		"damaged":            str(t, d.upload(d.staff, matter, "cut-off.pdf", "application/pdf", DamagedPDF(), nil), "id"),
		"low_confidence":     str(t, d.upload(d.staff, matter, "scribble.txt", "text/plain", []byte("UNCERTAIN SCRIBBLE\nsomething about the case\n"), nil), "id"),
	}
	d.drain()
	clk.advance(16 * time.Minute) // past the 15-minute bundle window
	d.drain()
	queue := d.atty.must(http.StatusOK, "GET", "/api/v1/doc-tender/exceptions?status=open", nil)
	for kind, id := range bad {
		found := false
		for _, ex := range items(t, queue) {
			if ex["file_id"] == id && ex["kind"] == kind {
				found = true
			}
		}
		if !found {
			t.Fatalf("no open %s exception for %s in %s", kind, id, queue.body)
		}
		st := d.dtStatus(d.atty, id)
		if flags, _ := st["flags"].([]any); len(flags) == 0 {
			t.Fatalf("%s file has no flag: %v", kind, st)
		}
		// The flag is on the screen too: the matter's Files tab names what failed beside
		// the file, for the attorney and for staff, and the row keeps Open and Download.
		label := map[string]string{"password_protected": "Password-protected", "damaged": "Damaged file", "low_confidence": "Low confidence"}[kind]
		for _, c := range []*client{d.atty, d.staff} {
			row := d.filesTabRow(c, matter, id)
			for _, want := range []string{"Needs review", `flag-` + kind + `">` + label + `<`, "disposition=inline", "/download"} {
				if !strings.Contains(row, want) {
					t.Fatalf("%s file's Files tab row lacks %q:\n%s", kind, want, row)
				}
			}
		}
		f := d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+id, nil).json
		if code, _ := d.download(d.atty, id, f["current_version_id"].(string)); code != http.StatusOK {
			t.Fatalf("%s file is not downloadable: %d", kind, code)
		}
		if r := d.atty.must(http.StatusOK, "GET", "/api/v1/files?matter_id="+matter, nil); !strings.Contains(r.body, id) {
			t.Fatalf("%s file is not listed", kind)
		}
	}
	if len(d.smtp.to(adminEmail)) <= mailBefore {
		t.Fatal("the admin was not mailed about the new exceptions")
	}
	if r := d.staff.do("GET", "/api/v1/doc-tender/exceptions", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff read the review queue: %d", r.code)
	}
	if n := d.count(`SELECT COUNT(*) FROM notifications WHERE kind LIKE 'doc_tender%' AND user_id = $1`, d.attyID); n == 0 {
		t.Fatal("no review-queue notification for the attorney")
	}

	// --- AC-049: what a person set is never changed; reprocessing adds a Markdown version ---
	target := held
	d.atty.must(http.StatusOK, "POST", "/api/v1/files/"+target+"/rename", map[string]any{"name": "my own name"})
	other, _ := d.matter("Other Matter", "", nil)
	d.atty.must(http.StatusOK, "PATCH", "/api/v1/files/"+target, map[string]any{"category_id": "correspondence", "matter_id": matter})
	_ = other
	before, beforeBody := d.markdown(d.atty, target)
	d.atty.must(http.StatusAccepted, "POST", "/api/v1/files/"+target+"/doc-tender-reprocess", map[string]any{})
	d.drain()
	f := d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+target, nil).json
	if f["name"] != "my own name.txt" || f["category_id"] != "correspondence" || f["matter_id"] != matter {
		t.Fatalf("reprocess changed what a person set: %v %v %v", f["name"], f["category_id"], f["matter_id"])
	}
	afterMD, _ := d.markdown(d.atty, target)
	if num(t, afterMD, "number") != num(t, before, "number")+1 {
		t.Fatalf("reprocess Markdown version %v after %v", afterMD["number"], before["number"])
	}
	if old := d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+target+"/renditions/"+before["id"].(string), nil).body; old != beforeBody {
		t.Fatal("reprocessing changed the old Markdown copy")
	}
	if afterMD["name"] != "my own name.md" {
		t.Fatalf("the Markdown copy's name does not follow the rename: %v", afterMD["name"])
	}

	// --- AC-051: the naming pattern and a new category apply to the next file ---
	if r := d.admin.do("PUT", "/api/v1/doc-tender/naming", map[string]any{"pattern": "<matter#>-<status>-<what-it-is>.<ext>"}); r.code != http.StatusBadRequest || !strings.Contains(r.body, "status") {
		t.Fatalf("a status token was accepted: %d %s", r.code, r.body)
	}
	if r := d.staff.do("PUT", "/api/v1/doc-tender/naming", map[string]any{"pattern": "<matter#>-<what-it-is>.<ext>"}); r.code != http.StatusForbidden {
		t.Fatalf("staff changed the pattern: %d", r.code)
	}
	d.admin.must(http.StatusOK, "PUT", "/api/v1/doc-tender/naming", map[string]any{"pattern": "<matter#>-<what-it-is>-<doc-date>.<ext>"})
	d.admin.must(http.StatusCreated, "POST", "/api/v1/doc-tender/categories", map[string]any{"key": "fee_dispute", "description": "Fee dispute papers", "tier": "standard", "folder_name": "Billing"})
	prov.Category("FEE DISPUTE", "fee_dispute")
	next := str(t, d.upload(d.staff, matter, "dispute.txt", "text/plain", []byte("FEE DISPUTE NOTICE\nDated 2026-11-02.\n"), nil), "id")
	d.drain()
	nf := d.atty.must(http.StatusOK, "GET", "/api/v1/files/"+next, nil).json
	if nf["name"] != number+"-fee-dispute-notice-2026-11-02.txt" || nf["category_id"] != "fee_dispute" {
		t.Fatalf("next file: name %v, category %v", nf["name"], nf["category_id"])
	}
}

// inOrder reports whether every part appears in s, each after the one before.
func inOrder(s string, parts ...string) bool {
	at := 0
	for _, p := range parts {
		i := strings.Index(s[at:], p)
		if i < 0 {
			return false
		}
		at += i + len(p)
	}
	return true
}

// The supervisor mounts the core's data volume on a replacement core at the path the
// core keeps its files under; the two constants must agree or an update would start on
// an empty file store.
func TestCoreDataDirMatchesTheSupervisorMount(t *testing.T) {
	if supervisor.CoreDataDir != DataDir {
		t.Fatalf("supervisor mounts the core data volume at %s, the core uses %s", supervisor.CoreDataDir, DataDir)
	}
	binds := supervisor.CoreBinds("p_appsecrets", "p_core_data")
	if len(binds) != 2 || binds[1] != "p_core_data:"+DataDir {
		t.Fatalf("replacement core binds %v", binds)
	}
}

// TestDocumentsScreens is the phase 4 visual QC: it seeds a running core with a matter's
// files (one processed by Doc Tender, one in the review queue), a deadline, an event and
// a DOCX, then drives headless Chromium over every changed screen as the roles that use
// them (scripts/ci/documents-qc.mjs). Every page must load without a console or page
// error or a failed request; a walled deadline is a 404 page for staff and a signed-out
// visitor lands on sign-in. It runs when TOGAVSU_QC_DIR, CHROME and PUPPETEER_CORE are
// set, and writes the screenshots and qc.json there.
func TestDocumentsScreens(t *testing.T) {
	out := os.Getenv("TOGAVSU_QC_DIR")
	if out == "" || os.Getenv("CHROME") == "" || os.Getenv("PUPPETEER_CORE") == "" {
		t.Skip("TOGAVSU_QC_DIR, CHROME and PUPPETEER_CORE are not all set")
	}
	prov := NewProvider()
	defer prov.Close()
	d := newDocs(t, func(c *Config) {
		c.ProviderURLs = map[string]string{"openai": prov.URL()}
		c.NoBackground = true
		c.WordcraftCLI = os.Getenv("TOGAVSU_WORDCRAFT_CLI")
		c.EditorDist = os.Getenv("TOGAVSU_EDITOR_DIST")
	})
	d.useProvider()
	matter, _ := d.matter("Example Dissolution", "", map[string]any{"case_number": "23AR-CV00825", "court": "Circuit Court"})
	walled, _ := d.matter("Sealed Guardianship", "", nil)
	d.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{d.adminID, d.attyID}})
	today := firmToday(t)
	deadline := str(t, d.staff.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"matter_id": matter, "title": "Answer due",
		"due_on": today.AddDate(0, 0, 3).Format("2006-01-02"), "responsible_user_id": d.attyID}).json, "id")
	hidden := str(t, d.atty.must(http.StatusCreated, "POST", "/api/v1/deadlines", map[string]any{"matter_id": walled, "title": "Sealed report",
		"due_on": today.AddDate(0, 0, 2).Format("2006-01-02"), "responsible_user_id": d.attyID}).json, "id")
	event := str(t, d.staff.must(http.StatusCreated, "POST", "/api/v1/calendar-events", map[string]any{"title": "Hearing", "kind": "court_date",
		"starts_at": today.AddDate(0, 0, 1).Add(9 * time.Hour).Format(time.RFC3339), "ends_at": today.AddDate(0, 0, 1).Add(11 * time.Hour).Format(time.RFC3339),
		"matter_id": matter, "attendee_user_ids": []string{d.attyID}}).json, "id")
	d.upload(d.staff, matter, "motion.txt", "text/plain", []byte(motionText), nil)
	d.upload(d.staff, matter, "cut-off.pdf", "application/pdf", DamagedPDF(), nil)
	d.drain()
	var exception string
	for _, ex := range items(t, d.atty.must(http.StatusOK, "GET", "/api/v1/doc-tender/exceptions?status=open", nil)) {
		exception = ex["id"].(string)
	}
	docx := []byte("PK")
	if cli := os.Getenv("TOGAVSU_WORDCRAFT_CLI"); cli != "" {
		b, err := (&editor.CLI{Path: cli, TempDir: t.TempDir()}).Run(context.Background(), editor.RunInput{Commands: []editor.Command{
			{ID: "text.insert", Parameters: map[string]any{"text": "Synthetic engagement letter."}}}, Out: "docx"})
		if err != nil {
			t.Fatal(err)
		}
		docx = b
	}
	// The Files tab screenshot must show the damaged file's flag (AC-048).
	if body := d.atty.must(http.StatusOK, "GET", "/practice/matters/"+matter+"/files", nil).body; !strings.Contains(body, `flag-damaged">Damaged file<`) {
		t.Fatalf("the Files tab shows no flag on cut-off.pdf:\n%s", body)
	}
	letter := str(t, d.upload(d.staff, matter, "Engagement letter.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", docx, nil), "id")

	u, _ := url.Parse(d.srv.URL)
	cookies := func(c *client) []map[string]any {
		var out []map[string]any
		for _, k := range c.http.Jar.Cookies(u) {
			out = append(out, map[string]any{"name": k.Name, "value": k.Value})
		}
		return out
	}
	type step struct {
		Name string `json:"name"`
		Role string `json:"role"`
		Path string `json:"path"`
		Wait int    `json:"wait,omitempty"`
	}
	steps := []step{
		{"01-calendar-week", "staff", "/calendar", 0},
		{"02-calendar-deadlines", "staff", "/calendar/deadlines", 0},
		{"03-calendar-deadline", "staff", "/calendar/deadlines/" + deadline, 0},
		{"04-calendar-event", "staff", "/calendar/events/" + event, 0},
		{"05-calendar-available-dates", "staff", "/calendar/available-dates", 0},
		{"06-calendar-feeds", "staff", "/calendar/feeds", 0},
		{"07-matter-files-tab", "attorney", "/practice/matters/" + matter + "/files", 0},
		{"08-files-and-storage", "admin", "/files/admin/storage", 0},
		{"09-edit-page", "staff", "/files/" + letter + "/edit", 3000},
		{"10-edit-page-locked", "attorney", "/files/" + letter + "/edit", 0},
		{"11-new-pleading", "staff", "/matters/" + matter + "/pleadings/new", 0},
		{"12-doc-tender-review", "attorney", "/doc-tender/review", 0},
		{"13-doc-tender-item", "attorney", "/doc-tender/review/" + exception, 0},
		{"14-doc-tender-tab", "admin", "/admin/doc-tender", 0},
		{"15-doc-tender-guide", "staff", "/doc-tender/guide", 0},
		{"16-walled-deadline-staff", "staff", "/calendar/deadlines/" + hidden, 0},
		{"17-signed-out-calendar", "anonymous", "/calendar", 0},
	}
	plan := map[string]any{"base": d.srv.URL, "pages": steps,
		"roles": map[string]any{"staff": cookies(d.staff), "attorney": cookies(d.atty), "admin": cookies(d.admin), "anonymous": []any{}}}
	raw, _ := json.Marshal(plan)
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join("..", "..", "scripts", "ci", "documents-qc.mjs"), planPath, out)
	cmd.Stderr = os.Stderr
	res, err := cmd.Output()
	if err != nil {
		t.Fatalf("documents-qc.mjs: %v", err)
	}
	var got struct {
		Pages []struct {
			Name    string   `json:"name"`
			Status  int      `json:"status"`
			Landed  string   `json:"landed"`
			Title   string   `json:"title"`
			Console []string `json:"console"`
			Failed  []string `json:"failed"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(res, &got); err != nil || len(got.Pages) != len(steps) {
		t.Fatalf("QC output %s: %v", res, err)
	}
	for _, p := range got.Pages {
		want := http.StatusOK
		switch p.Name {
		case "16-walled-deadline-staff":
			want = http.StatusNotFound
		case "17-signed-out-calendar":
			if p.Landed != "/login" {
				t.Errorf("%s landed on %s", p.Name, p.Landed)
			}
		}
		if p.Status != want {
			t.Errorf("%s: status %d, want %d", p.Name, p.Status, want)
		}
		console := p.Console
		if want == http.StatusNotFound && len(console) == 1 && strings.Contains(console[0], "status of 404") {
			console = nil // the browser logs the page's own 404
		}
		if len(console) > 0 || len(p.Failed) > 0 {
			t.Errorf("%s: console %v, failed requests %v", p.Name, p.Console, p.Failed)
		}
		if strings.Contains(p.Title, "Sealed") {
			t.Errorf("%s shows the walled matter", p.Name)
		}
	}
	summary, _ := json.MarshalIndent(map[string]any{"pages": got.Pages, "editor_dist": os.Getenv("TOGAVSU_EDITOR_DIST") != ""}, "", " ")
	if err := os.WriteFile(filepath.Join(out, "qc.json"), summary, 0o644); err != nil {
		t.Fatal(err)
	}
}

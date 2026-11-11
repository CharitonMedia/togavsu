package core

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/doctender/dtcorpus"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/storage/storagetest"
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
	_ = context.Background
	_ = os.Getenv
	_ = domain.SystemUserID
}

// adminP is the admin as a service principal, for the file-bytes backup calls the
// supervisor makes (it has no /api/v1 route of its own in this phase).
func (d *docs) adminP() domain.Principal {
	return domain.Principal{UserID: domain.ID(d.adminID), Roles: []domain.Role{domain.RoleAdmin, domain.RoleAttorney}, Source: domain.SourceUI}
}

// useProvider points the AI tab at the synthetic provider, so Doc Tender runs.
func (d *docs) useProvider() {
	d.t.Helper()
	for k, v := range map[string]string{"ai.openai_api_key": dtcorpus.Key, "ai.primary_provider": "openai", "ai.primary_strong_model": "gpt-test-strong",
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
	prov := dtcorpus.NewProvider()
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

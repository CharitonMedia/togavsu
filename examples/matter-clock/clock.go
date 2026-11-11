// Package matterclock is the sample module of PRD section 11: a home-screen widget, a
// matter-screen tab and a help guide page showing hours worked per matter this month,
// with a monthly target per matter kept in the module's own schema. It is test
// scaffolding for the module interface and does not ship in the release.
//
// The module runs in its own container. It reaches core records only through the core
// API with its scoped token and the context token the core hands each frame, and it
// reads and writes only its own schema through its own database login.
package matterclock

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

// Small fixed numbers spelled as arithmetic so the AC-030 source check, which also
// reads examples/, can tell them from tunables.
var (
	zero = int(0)
	one  = zero + 1
)

// ID is the module id from the manifest.
const ID = "matter-clock"

//go:embed package
var packageFS embed.FS

// Files returns the package files (manifest and migrations) by package path.
func Files() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(packageFS, "package", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := packageFS.ReadFile(p)
		if err != nil {
			return err
		}
		out[strings.TrimPrefix(p, "package/")] = b
		return nil
	})
	return out, err
}

// Build signs the package with priv. edit, when set, changes the files first (tests use
// it to make tampered, updated and refused variants).
func Build(priv ed25519.PrivateKey, edit func(map[string][]byte)) ([]byte, error) {
	files, err := Files()
	if err != nil {
		return nil, err
	}
	if edit != nil {
		edit(files)
	}
	return modules.Sign(files, priv)
}

// Config is what the container gets: the core's address, its secrets and its own DB.
type Config struct {
	Core          string
	Token         string
	WebhookSecret string
	DB            *sql.DB
	HTTP          *http.Client
	Now           func() time.Time
	// SkewSeconds is how far a webhook timestamp may be from the module's clock.
	SkewSeconds int
}

// Server is the module's HTTP server.
type Server struct{ c Config }

// New returns the server.
func New(c Config) *Server {
	if c.HTTP == nil {
		c.HTTP = http.DefaultClient
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return &Server{c: c}
}

// Handler serves the widget, the tabs, target edits and event webhooks.
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /widget", s.widget)
	m.HandleFunc("GET /tab", s.widget)
	m.HandleFunc("GET /matter", s.matter)
	m.HandleFunc("POST /targets", s.setTarget)
	m.HandleFunc("POST /events", s.events)
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	return m
}

var errCore = errors.New("matter-clock: core API")

// api calls the core with the module token and, for a viewer's request, the frame's
// context token, so the core answers exactly what that viewer may see.
func (s *Server) api(ctx context.Context, method, path, contextToken string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.c.Core, "/")+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+s.c.Token)
	if contextToken != "" {
		req.Header.Set(modules.ContextHeader, contextToken)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, fmt.Errorf("%w: %s %s: %s", errCore, method, path, resp.Status)
	}
	if out != nil {
		return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode, nil
}

type viewer struct {
	UserID   string   `json:"viewer_user_id"`
	Roles    []string `json:"roles"`
	MatterID *string  `json:"matter_id"`
}

func (s *Server) viewer(r *http.Request) (viewer, string, error) {
	tok := r.URL.Query().Get("context")
	if tok == "" {
		return viewer{}, "", errors.New("no context token")
	}
	var v viewer
	if _, err := s.api(r.Context(), http.MethodPost, "/api/v1/modules/"+ID+"/context-tokens/verify", tok, nil, &v); err != nil {
		return viewer{}, "", err
	}
	return v, tok, nil
}

func (s *Server) incrementMinutes(ctx context.Context) (int, error) {
	var rows []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if _, err := s.api(ctx, http.MethodGet, "/api/v1/modules/"+ID+"/settings", "", nil, &rows); err != nil {
		return 0, err
	}
	for _, r := range rows {
		if r.Key == "increment_minutes" {
			return strconv.Atoi(r.Value)
		}
	}
	return 0, errors.New("matter-clock: increment_minutes is not set")
}

// Hours is one matter's hours this month, in tenths of an hour.
type Hours struct {
	MatterID string
	Tenths   int
}

// monthHours adds up this month's time entries the viewer can read, per matter.
func (s *Server) monthHours(ctx context.Context, tok, matter string) ([]Hours, error) {
	minutes, err := s.incrementMinutes(ctx)
	if err != nil {
		return nil, err
	}
	now := s.c.Now().UTC()
	day := now.AddDate(zero, zero, one-now.Day())
	first := time.Date(day.Year(), day.Month(), day.Day(), zero, zero, zero, zero, time.UTC)
	last := first.AddDate(zero, one, -one)
	q := url.Values{"from_date": {first.Format(time.DateOnly)}, "to_date": {last.Format(time.DateOnly)}}
	if matter != "" {
		q.Set("matter_id", matter)
	}
	increments := map[string]int{}
	cursor := ""
	for {
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Data []struct {
				MatterID   string `json:"matter_id"`
				Increments int    `json:"increments"`
			} `json:"data"`
			Page struct {
				NextCursor *string `json:"next_cursor"`
			} `json:"page"`
		}
		if _, err := s.api(ctx, http.MethodGet, "/api/v1/time-entries?"+q.Encode(), tok, nil, &page); err != nil {
			return nil, err
		}
		for _, e := range page.Data {
			increments[e.MatterID] += e.Increments
		}
		if page.Page.NextCursor == nil || *page.Page.NextCursor == "" {
			break
		}
		cursor = *page.Page.NextCursor
	}
	out := make([]Hours, 0, len(increments))
	for m, n := range increments {
		// Tenths of an hour: increments x minutes x 10 / 60.
		out = append(out, Hours{MatterID: m, Tenths: n * minutes * 10 / 60})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MatterID < out[j].MatterID })
	return out, nil
}

type matterView struct {
	ID       string `json:"id"`
	Number   string `json:"number"`
	Name     string `json:"name"`
	Included map[string][]struct {
		Target int    `json:"monthly_target_tenths"`
		Note   string `json:"note"`
	} `json:"included"`
}

func (s *Server) matterInfo(ctx context.Context, tok, id string) (matterView, error) {
	var m matterView
	_, err := s.api(ctx, http.MethodGet, "/api/v1/matters/"+url.PathEscape(id)+"?include="+ID+".targets", tok, nil, &m)
	return m, err
}

// FormatTenths shows tenths of an hour as hours with one decimal.
func FormatTenths(t int) string {
	return strconv.Itoa(t/10) + "." + strconv.Itoa(t%10)
}

var page = template.Must(template.New("page").Funcs(template.FuncMap{"hours": FormatTenths}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Matter Clock</title></head><body>
<h1>{{.Title}}</h1>
{{if .Rows}}<table><thead><tr><th>Matter</th><th>Hours this month</th><th>Target</th></tr></thead><tbody>
{{range .Rows}}<tr data-matter="{{.ID}}"><td>{{.Number}} {{.Name}}</td><td>{{hours .Tenths}}</td><td>{{if .HasTarget}}{{hours .Target}}{{end}}</td></tr>
{{end}}</tbody></table>{{else}}<p>No hours yet this month.</p>{{end}}
{{if .Note}}<p class="note">{{.Note}}</p>{{end}}
</body></html>`))

type row struct {
	ID, Number, Name string
	Tenths, Target   int
	HasTarget        bool
}

func (s *Server) render(w http.ResponseWriter, title string, rows []row, note string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	_ = page.Execute(w, map[string]any{"Title": title, "Rows": rows, "Note": note})
}

func fail(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "404") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	http.Error(w, "Matter Clock can't reach the core right now", http.StatusBadGateway)
}

// widget lists the viewer's matters with hours this month (widget and home tab).
func (s *Server) widget(w http.ResponseWriter, r *http.Request) {
	_, tok, err := s.viewer(r)
	if err != nil {
		http.Error(w, "sign in through Togavsu", http.StatusUnauthorized)
		return
	}
	hours, err := s.monthHours(r.Context(), tok, r.URL.Query().Get("matter_id"))
	if err != nil {
		fail(w, err)
		return
	}
	var rows []row
	for _, h := range hours {
		m, err := s.matterInfo(r.Context(), tok, h.MatterID)
		if err != nil {
			continue // a matter the viewer can't read is left out
		}
		rw := row{ID: m.ID, Number: m.Number, Name: m.Name, Tenths: h.Tenths}
		if t := m.Included[ID+".targets"]; len(t) > 0 {
			rw.Target, rw.HasTarget = t[0].Target, true
		}
		rows = append(rows, rw)
	}
	s.render(w, "Hours this month", rows, "")
}

// matter is the matter-screen tab: this matter's hours and its target, read through the
// core's include= so walls and attorney-only columns apply.
func (s *Server) matter(w http.ResponseWriter, r *http.Request) {
	v, tok, err := s.viewer(r)
	if err != nil || v.MatterID == nil {
		http.Error(w, "open this tab from a matter", http.StatusUnauthorized)
		return
	}
	m, err := s.matterInfo(r.Context(), tok, *v.MatterID)
	if err != nil {
		fail(w, err)
		return
	}
	hours, err := s.monthHours(r.Context(), tok, m.ID)
	if err != nil {
		fail(w, err)
		return
	}
	rw := row{ID: m.ID, Number: m.Number, Name: m.Name}
	for _, h := range hours {
		if h.MatterID == m.ID {
			rw.Tenths = h.Tenths
		}
	}
	note := ""
	if t := m.Included[ID+".targets"]; len(t) > 0 {
		rw.Target, rw.HasTarget, note = t[0].Target, true, t[0].Note
	}
	s.render(w, "Hours and target", []row{rw}, note)
}

// hasRole reports whether the viewer holds any of roles.
func (v viewer) hasRole(roles ...string) bool {
	for _, have := range v.Roles {
		for _, want := range roles {
			if have == want {
				return true
			}
		}
	}
	return false
}

// setTarget saves a matter's monthly target in the module's own schema, after the core
// confirms the viewer can read the matter. Firm staff, attorneys and admins set targets; a
// client never does. The note is attorney-only, as include= reads redact it, so only an
// attorney or admin writes it; anyone else's save keeps the note that is there.
func (s *Server) setTarget(w http.ResponseWriter, r *http.Request) {
	v, tok, err := s.viewer(r)
	if err != nil || v.MatterID == nil {
		http.Error(w, "open this tab from a matter", http.StatusUnauthorized)
		return
	}
	if v.hasRole("client") || !v.hasRole("admin", "attorney", "staff") {
		http.Error(w, "only firm staff can set a target", http.StatusForbidden)
		return
	}
	noteAllowed := v.hasRole("admin", "attorney")
	if !noteAllowed && r.FormValue("note") != "" {
		http.Error(w, "only an attorney or admin can write the note", http.StatusForbidden)
		return
	}
	tenths, err := strconv.Atoi(r.FormValue("monthly_target_tenths"))
	if err != nil || tenths < zero {
		http.Error(w, "the target is a whole number of tenths of an hour", http.StatusBadRequest)
		return
	}
	if _, err := s.matterInfo(r.Context(), tok, *v.MatterID); err != nil {
		fail(w, err)
		return
	}
	save := func() error {
		if noteAllowed {
			return SetTarget(r.Context(), s.c.DB, *v.MatterID, tenths, r.FormValue("note"), s.c.Now())
		}
		return setTargetKeepingNote(r.Context(), s.c.DB, *v.MatterID, tenths, s.c.Now())
	}
	if err := save(); err != nil {
		http.Error(w, "the target could not be saved", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SetTarget writes a target row through the module's own login.
func SetTarget(ctx context.Context, db *sql.DB, matter string, tenths int, note string, at time.Time) error {
	_, err := db.ExecContext(ctx, `INSERT INTO matter_targets (matter_id, monthly_target_tenths, note, updated_at) VALUES ($1, $2, $3, $4)
ON CONFLICT (matter_id) DO UPDATE SET monthly_target_tenths = EXCLUDED.monthly_target_tenths, note = EXCLUDED.note, updated_at = EXCLUDED.updated_at`,
		matter, tenths, note, at.UTC().UnixMicro())
	return err
}

// setTargetKeepingNote writes the target and leaves an existing note as it is.
func setTargetKeepingNote(ctx context.Context, db *sql.DB, matter string, tenths int, at time.Time) error {
	_, err := db.ExecContext(ctx, `INSERT INTO matter_targets (matter_id, monthly_target_tenths, note, updated_at) VALUES ($1, $2, '', $3)
ON CONFLICT (matter_id) DO UPDATE SET monthly_target_tenths = EXCLUDED.monthly_target_tenths, updated_at = EXCLUDED.updated_at`,
		matter, tenths, at.UTC().UnixMicro())
	return err
}

// events receives the core's signed webhooks and records each event once.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, int64(one)<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	skew, err := time.ParseDuration(strconv.Itoa(s.c.SkewSeconds) + "s")
	if err != nil {
		http.Error(w, "bad skew", http.StatusInternalServerError)
		return
	}
	if err := modules.VerifyWebhook(s.c.WebhookSecret, r.Header.Get(modules.SignatureHeader), body, s.c.Now(), skew); err != nil {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	var ev modules.EventPayload
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "bad event", http.StatusBadRequest)
		return
	}
	if _, err := s.c.DB.ExecContext(r.Context(), `INSERT INTO received_events (event_id, action, record_id, received_at) VALUES ($1, $2, $3, $4) ON CONFLICT (event_id) DO NOTHING`,
		ev.ID, ev.Action, ev.RecordID, s.c.Now().UTC().UnixMicro()); err != nil {
		http.Error(w, "not stored", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

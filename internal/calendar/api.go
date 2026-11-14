package calendar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// maxBody bounds a request body at 1 MiB, the same guard the core handlers use.
const maxBody = 1 << 20

// --- wire shapes (contract CalendarEvent, Deadline, IcalFeed) ---

func eventJSON(e Event) map[string]any {
	att := make([]string, 0, len(e.Attendees))
	for _, id := range e.Attendees {
		att = append(att, string(id))
	}
	return map[string]any{
		"id": e.ID, "created_at": e.CreatedAt.UTC().Format(time.RFC3339), "updated_at": e.UpdatedAt.UTC().Format(time.RFC3339),
		"created_by": e.CreatedBy, "title": e.Title, "kind": e.Kind,
		"starts_at": fmtTime(e.StartsAt), "ends_at": fmtTime(e.EndsAt), "all_day": e.AllDay,
		"matter_id": nullID(e.MatterID), "location": e.Location, "travel_minutes": e.TravelMinutes,
		"notes": e.Notes, "attendee_user_ids": att,
	}
}

func deadlineJSON(d Deadline) map[string]any {
	offsets := d.Offsets
	if offsets == nil {
		offsets = []int{}
	}
	var doneAt any
	if isSet(d.DoneAt) {
		doneAt = fmtTime(d.DoneAt)
	}
	return map[string]any{
		"id": d.ID, "created_at": d.CreatedAt.UTC().Format(time.RFC3339), "updated_at": d.UpdatedAt.UTC().Format(time.RFC3339),
		"created_by": d.CreatedBy, "matter_id": d.MatterID, "title": d.Title, "due_on": d.DueOn,
		"responsible_user_id": d.ResponsibleUserID, "status": d.Status, "reminder_offsets_days": offsets,
		"done_by": nullID(d.DoneBy), "done_at": doneAt,
	}
}

func feedJSON(f Feed) map[string]any {
	return map[string]any{
		"id": f.ID, "created_at": f.CreatedAt.UTC().Format(time.RFC3339), "updated_at": f.UpdatedAt.UTC().Format(time.RFC3339),
		"created_by": f.CreatedBy, "owner_user_id": nullID(f.OwnerUserID), "revoked": f.Revoked,
	}
}

func nullID(id domain.ID) any {
	if id.IsZero() {
		return nil
	}
	return string(id)
}

// --- request bodies ---

type eventBody struct {
	Title         *string      `json:"title"`
	Kind          *string      `json:"kind"`
	StartsAt      *string      `json:"starts_at"`
	EndsAt        *string      `json:"ends_at"`
	AllDay        *bool        `json:"all_day"`
	MatterID      *string      `json:"matter_id"`
	Location      *string      `json:"location"`
	TravelMinutes *int         `json:"travel_minutes"`
	Notes         *string      `json:"notes"`
	Attendees     *[]domain.ID `json:"attendee_user_ids"`
}

// parseInstant reads RFC 3339 and keeps the written offset, which all-day events
// use to read the intended date.
func parseInstant(field, v string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, domain.Invalid(field, "must be an RFC 3339 date-time")
	}
	return t, nil
}

func (b eventBody) patch() (EventPatch, error) {
	var p EventPatch
	p.Title, p.Kind, p.AllDay, p.Location, p.TravelMinutes, p.Notes, p.Attendees = b.Title, b.Kind, b.AllDay, b.Location, b.TravelMinutes, b.Notes, b.Attendees
	if b.StartsAt != nil {
		t, err := parseInstant("starts_at", *b.StartsAt)
		if err != nil {
			return p, err
		}
		p.StartsAt = &t
	}
	if b.EndsAt != nil {
		t, err := parseInstant("ends_at", *b.EndsAt)
		if err != nil {
			return p, err
		}
		p.EndsAt = &t
	}
	if b.MatterID != nil {
		id := domain.ID(*b.MatterID)
		p.MatterID = &id
	}
	return p, nil
}

func (b eventBody) input() (EventInput, error) {
	for field, v := range map[string]*string{"title": b.Title, "kind": b.Kind, "starts_at": b.StartsAt, "ends_at": b.EndsAt} {
		if v == nil {
			return EventInput{}, domain.Invalid(field, field+" is required")
		}
	}
	p, err := b.patch()
	if err != nil {
		return EventInput{}, err
	}
	in := EventInput{Title: *p.Title, Kind: *p.Kind, StartsAt: *p.StartsAt, EndsAt: *p.EndsAt}
	if p.AllDay != nil {
		in.AllDay = *p.AllDay
	}
	if p.MatterID != nil {
		in.MatterID = *p.MatterID
	}
	if p.Location != nil {
		in.Location = *p.Location
	}
	if p.TravelMinutes != nil {
		in.TravelMinutes = *p.TravelMinutes
	}
	if p.Notes != nil {
		in.Notes = *p.Notes
	}
	if p.Attendees != nil {
		in.Attendees = *p.Attendees
	}
	return in, nil
}

type deadlineBody struct {
	MatterID          *string `json:"matter_id"`
	Title             *string `json:"title"`
	DueOn             *string `json:"due_on"`
	ResponsibleUserID *string `json:"responsible_user_id"`
	Status            *string `json:"status"`
	Offsets           *[]int  `json:"reminder_offsets_days"`
}

func (b deadlineBody) input() (DeadlineInput, error) {
	for field, v := range map[string]*string{"matter_id": b.MatterID, "title": b.Title, "due_on": b.DueOn, "responsible_user_id": b.ResponsibleUserID} {
		if v == nil {
			return DeadlineInput{}, domain.Invalid(field, field+" is required")
		}
	}
	in := DeadlineInput{MatterID: domain.ID(*b.MatterID), Title: *b.Title, DueOn: *b.DueOn, ResponsibleUserID: domain.ID(*b.ResponsibleUserID)}
	if b.Status != nil {
		in.Status = *b.Status
	}
	if b.Offsets != nil {
		in.Offsets = *b.Offsets
	}
	return in, nil
}

func (b deadlineBody) patch() DeadlinePatch {
	p := DeadlinePatch{Title: b.Title, DueOn: b.DueOn, Status: b.Status, Offsets: b.Offsets}
	if b.MatterID != nil {
		id := domain.ID(*b.MatterID)
		p.MatterID = &id
	}
	if b.ResponsibleUserID != nil {
		id := domain.ID(*b.ResponsibleUserID)
		p.ResponsibleUserID = &id
	}
	return p
}

type availableBody struct {
	UserID          string  `json:"user_id"`
	StartDate       string  `json:"start_date"`
	EndDate         *string `json:"end_date"`
	IncludeHalfDays bool    `json:"include_half_days"`
	MatterID        *string `json:"matter_id"`
	Court           string  `json:"court"`
	Caption         string  `json:"caption"`
	PartyLabels     *struct {
		Left  string `json:"left"`
		Right string `json:"right"`
	} `json:"party_labels"`
	LeftPartyName    string  `json:"left_party_name"`
	RightPartyName   string  `json:"right_party_name"`
	CaseNumber       string  `json:"case_number"`
	FirmSide         string  `json:"firm_side"`
	Purpose          string  `json:"purpose"`
	CoordinateWith   string  `json:"coordinate_with"`
	Title            string  `json:"title"`
	ServiceDate      *string `json:"service_date"`
	LetterRecipient  string  `json:"letter_recipient"`
	LetterAddress    string  `json:"letter_address"`
	LetterRe         string  `json:"letter_re"`
	LetterSalutation string  `json:"letter_salutation"`
}

func (b availableBody) request() AvailableRequest {
	r := AvailableRequest{
		UserID: domain.ID(b.UserID), StartDate: b.StartDate, IncludeHalfDays: b.IncludeHalfDays,
		Court: b.Court, Caption: b.Caption, LeftName: b.LeftPartyName, RightName: b.RightPartyName,
		CaseNumber: b.CaseNumber, FirmSide: b.FirmSide, Purpose: b.Purpose, CoordinateWith: b.CoordinateWith,
		Title: b.Title, LetterRecipient: b.LetterRecipient, LetterAddress: b.LetterAddress,
		LetterRe: b.LetterRe, LetterSalutation: b.LetterSalutation,
	}
	if b.EndDate != nil {
		r.EndDate = *b.EndDate
	}
	if b.MatterID != nil {
		r.MatterID = domain.ID(*b.MatterID)
	}
	if b.PartyLabels != nil {
		r.LeftLabel, r.RightLabel = b.PartyLabels.Left, b.PartyLabels.Right
	}
	if b.ServiceDate != nil {
		r.ServiceDate = *b.ServiceDate
	}
	return r
}

func availableJSON(res AvailableResult) map[string]any {
	dates := make([]map[string]string, 0, len(res.Dates))
	for _, d := range res.Dates {
		dates = append(dates, map[string]string{"date": d.Date, "portion": d.Portion})
	}
	return map[string]any{"dates": dates, "pleading": res.Pleading, "letter": res.Letter}
}

// --- helpers ---

func readBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBody))
	if err != nil {
		return nil, domain.Invalid("body", "request body is too large or unreadable")
	}
	return body, nil
}

func decodeStrict(body []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return domain.Invalid("body", "request body must be a JSON object with known fields: "+err.Error())
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, err error) {
	var api *domain.APIError
	if errors.As(err, &api) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(api.HTTPStatus())
		_ = json.NewEncoder(w).Encode(api)
		return
	}
	writeJSON(w, http.StatusInternalServerError, domain.APIError{Code: "internal", Message: "internal error"})
}

// pageLimits reads api.page_size and api.max_page_size, so the admin page sets them.
func (s *Service) pageLimits(ctx context.Context) (int, int, error) {
	size, err := s.settingInt(ctx, "api.page_size")
	if err != nil {
		return size, size, err
	}
	most, err := s.settingInt(ctx, "api.max_page_size")
	return min(size, most), most, err
}

var (
	positiveInt = regexp.MustCompile(`^[1-9][0-9]*$`)
	cursorText  = regexp.MustCompile(`^o:(0|[1-9][0-9]*)$`)
)

// page slices rows by an opaque offset cursor.
func page[T any](s *Service, r *http.Request, rows []T) ([]T, map[string]any, error) {
	off, limit, err := pageBounds(s, r)
	if err != nil {
		return nil, nil, err
	}
	if off > len(rows) {
		off = len(rows)
	}
	end := min(off+limit, len(rows))
	var next any
	if end < len(rows) {
		next = base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(end)))
	}
	return rows[off:end], map[string]any{"limit": limit, "next_cursor": next}, nil
}

// pageBounds reads a list request's offset (from its cursor) and page size.
func pageBounds(s *Service, r *http.Request) (off, limit int, err error) {
	q := r.URL.Query()
	limit, most, err := s.pageLimits(r.Context())
	if err != nil {
		return 0, 0, err
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || !positiveInt.MatchString(v) {
			return 0, 0, domain.Invalid("limit", "limit must be a positive integer")
		}
		if n > most {
			return 0, 0, domain.Invalid("limit", "limit must not exceed api.max_page_size ("+strconv.Itoa(most)+")")
		}
		limit = n
	}
	if c := q.Get("cursor"); c != "" {
		raw, err := base64.RawURLEncoding.DecodeString(c)
		m := cursorText.FindStringSubmatch(string(raw))
		if err != nil || m == nil {
			return 0, 0, domain.Invalid("cursor", "cursor is not valid")
		}
		if off, err = strconv.Atoi(m[len(m)-1]); err != nil {
			return 0, 0, domain.Invalid("cursor", "cursor is not valid")
		}
	}
	return off, limit, nil
}

// idempotent replays the stored response for a repeated Idempotency-Key from the
// same caller and route, and refuses the same key with a different body. The write and
// its stored response run in one audit.Service.Atomic, so on Postgres a write never
// commits without its record: a failed record rolls the write back, and a retry with
// the same key runs once. A failed read of the record is an error, never a second run.
func (h *Handler) idempotent(w http.ResponseWriter, r *http.Request, p domain.Principal, body []byte, run func(context.Context) (int, any, error)) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		status, out, err := run(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, status, out)
		return
	}
	scope := string(p.UserID) + "|" + string(p.TokenID) + "|" + p.ModuleID + "|" + r.Method + " " + r.URL.Path + "|" + key
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	h.idemMu.Lock()
	defer h.idemMu.Unlock()
	store := h.Svc.d.Store
	rec, ok, err := store.IdemGet(r.Context(), scope)
	if err != nil {
		writeErr(w, err)
		return
	}
	if ok {
		if rec.Hash != hash {
			writeErr(w, domain.Conflict("Idempotency-Key was used with a different request"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Idempotent-Replayed", "true")
		w.WriteHeader(rec.Status)
		_, _ = w.Write(rec.Body)
		return
	}
	var status int
	var enc []byte
	err = h.Svc.d.Audit.Atomic(r.Context(), func(ctx context.Context) error {
		var out any
		var err error
		if status, out, err = run(ctx); err != nil {
			return err
		}
		if enc, err = json.Marshal(out); err != nil {
			return err
		}
		enc = append(enc, '\n')
		return store.IdemPut(ctx, scope, IdemRecord{Hash: hash, Status: status, Body: enc})
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(enc)
}

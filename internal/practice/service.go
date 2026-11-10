package practice

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// Service is the practice application service.
type Service struct {
	db       Store
	settings *settings.Registry
	Policy   domain.Policy
	now      func() time.Time
	newID    func() (domain.ID, error)
	// key seals SSN and EIN.
	// ponytail: ceiling: when New is called without a key, each process generates its own AES-256-GCM key. A restart cannot open identifiers sealed by the previous key, and redactContact then returns an empty value. Upgrade path: identity calls NewWithKey with the installer Docker secret. Trigger: a persistent Store is constructed.
	key []byte
}

type wallSource struct{ s *Service }

func (w wallSource) View(ctx context.Context, p domain.Principal, matterID domain.ID) (domain.WallView, error) {
	return w.s.viewWall(ctx, p, matterID)
}

type quietAudit struct{}

func (quietAudit) SettingChanged(context.Context, settings.Change) error { return nil }

// New builds a practice service. settings may be nil in tests; registry defaults are then used as fallbacks.
func New(db Store, reg *settings.Registry) *Service {
	return NewWithKey(db, reg, nil)
}

// NewWithKey builds a practice service that seals identifiers with key.
// key must be 32 bytes. A nil or wrong-length key falls back to a process-local key.
func NewWithKey(db Store, reg *settings.Registry, key []byte) *Service {
	if len(key) != 32 {
		key = identifierKey()
	}
	s := &Service{db: db, settings: reg, now: time.Now, newID: domain.NewID, key: append([]byte(nil), key...)}
	s.Policy = domain.Policy{Walls: wallSource{s}}
	return s
}

// NewWithSettings builds a service on an empty settings memory store.
func NewWithSettings(db Store) (*Service, error) {
	reg, err := settings.New(settings.NewMemory(), quietAudit{})
	if err != nil {
		return nil, err
	}
	return New(db, reg), nil
}

func identifierKey() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil
	}
	return key
}

func (s *Service) clock() time.Time {
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	return now().UTC()
}

func (s *Service) id() (domain.ID, error) {
	if s.newID == nil {
		return domain.NewID()
	}
	return s.newID()
}

func (s *Service) meta(p domain.Principal) (domain.Meta, error) {
	id, err := s.id()
	if err != nil {
		return domain.Meta{}, err
	}
	now := s.clock()
	return domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actorID(p)}, nil
}

func actorID(p domain.Principal) domain.ID {
	if p.UserID.IsZero() {
		return domain.SystemUserID
	}
	return p.UserID
}

func actorKind(p domain.Principal) string {
	switch p.Source {
	case domain.SourceSystem:
		return "system"
	case domain.SourceModule:
		return "module"
	case domain.SourceAPIToken:
		return "agent"
	}
	if p.HasRole(domain.RoleClient) && !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) && !p.HasRole(domain.RoleStaff) && !p.HasRole(domain.RoleAccountant) {
		return "client"
	}
	return "staff"
}

// tokenReason keeps the token id on the row the way audit.provenance does for identity,
// so the shared audit_log has one convention: source api_token, reason "token=<id> ...".
func tokenReason(p domain.Principal, reason string) string {
	if p.Source != domain.SourceAPIToken || p.TokenID.IsZero() {
		return reason
	}
	if reason == "" {
		return "token=" + string(p.TokenID)
	}
	return "token=" + string(p.TokenID) + " " + reason
}

func sourceLabel(p domain.Principal) string {
	if p.Source == domain.SourceModule && p.ModuleID != "" {
		return p.ModuleID
	}
	if p.Source == "" {
		return string(domain.SourceUI)
	}
	return string(p.Source)
}

func roleLabel(p domain.Principal) string {
	if len(p.Roles) == 0 {
		return ""
	}
	return string(p.Roles[0])
}

func (s *Service) gate(ctx context.Context, p domain.Principal, ref domain.ResourceRef) (domain.Decision, error) {
	d, err := s.Policy.Authorize(ctx, p, ref)
	if err != nil {
		return d, err
	}
	if d.Allow {
		return d, nil
	}
	switch d.Reason {
	case domain.ReasonUnauthenticated:
		return d, domain.Unauthenticated("sign in required")
	case domain.ReasonMatterWall:
		return d, domain.NotFound("matter")
	default:
		return d, domain.Forbidden(d.Reason)
	}
}

// gateMatter authorizes a matter. A read of the matter record is shared with the client party.
// Notes, history, conflict checks and close preview pass shared=false so a client does not receive them.
func (s *Service) gateMatter(ctx context.Context, p domain.Principal, id domain.ID, action domain.Action) error {
	shared := action == domain.ActionRead && s.clientMatter(p, id)
	return s.gateMatterShared(ctx, p, id, action, shared)
}

func (s *Service) gateMatterShared(ctx context.Context, p domain.Principal, id domain.ID, action domain.Action, shared bool) error {
	_, err := s.gate(ctx, p, domain.ResourceRef{
		Resource:         domain.ResMatter,
		Action:           action,
		MatterID:         id,
		SharedWithClient: shared,
	})
	return err
}

func (s *Service) viewWall(ctx context.Context, p domain.Principal, matterID domain.ID) (domain.WallView, error) {
	walls := s.db.Walls(matterID)
	view := domain.WallView{ClientMatter: s.clientMatter(p, matterID)}
	if len(walls) == 0 {
		return view, nil
	}
	view.Walled = true
	for _, wall := range walls {
		if wall.UserID == p.UserID {
			view.Allows = true
			break
		}
	}
	_ = ctx
	return view, nil
}

func (s *Service) clientMatter(p domain.Principal, matterID domain.ID) bool {
	user, err := s.db.User(context.Background(), p.UserID)
	if err != nil || user.ContactID.IsZero() {
		return false
	}
	for _, party := range s.db.Parties(matterID, user.ContactID) {
		if party.Role == "client" || party.Role == "authorized user" {
			return true
		}
	}
	return false
}

func (s *Service) visibleMatter(ctx context.Context, p domain.Principal, id domain.ID) bool {
	return s.gateMatter(ctx, p, id, domain.ActionRead) == nil
}

func (s *Service) setting(ctx context.Context, key, fallback string) string {
	if s.settings == nil {
		return fallback
	}
	v, err := s.settings.Get(ctx, key)
	if err != nil || v == "" {
		return fallback
	}
	return v
}

func (s *Service) intSetting(ctx context.Context, key string, fallback int) int {
	v := s.setting(ctx, key, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func (s *Service) listSetting(ctx context.Context, key string, fallback []string) []string {
	v := s.setting(ctx, key, "")
	if v == "" {
		return fallback
	}
	var out []string
	if err := json.Unmarshal([]byte(v), &out); err != nil || len(out) == 0 {
		return fallback
	}
	return out
}

type change struct {
	MatterID      domain.ID
	ContactID     domain.ID
	Entity        string
	EntityID      domain.ID
	Field         string
	Old           string
	New           string
	Reason        string
	Kind          string
	AttorneyOnly  bool
	Action        string
	Summary       string
	ClientVisible bool
	SkipEvent     bool
}

func (s *Service) record(ctx context.Context, p domain.Principal, rows []change) error {
	var audits []storedAudit
	var events []domain.Event
	now := s.clock()
	for _, row := range rows {
		if row.Old == row.New && row.Kind != "create" {
			continue
		}
		meta, err := s.meta(p)
		if err != nil {
			return err
		}
		meta.CreatedAt = now
		meta.UpdatedAt = now
		audits = append(audits, storedAudit{
			Entry: domain.AuditEntry{
				Meta:     meta,
				ActorID:  actorID(p),
				Role:     roleLabel(p),
				Source:   sourceLabel(p),
				Entity:   row.Entity,
				EntityID: row.EntityID,
				Field:    row.Field,
				OldValue: row.Old,
				NewValue: row.New,
				Reason:   tokenReason(p, row.Reason),
			},
			MatterID:     row.MatterID,
			ContactID:    row.ContactID,
			AttorneyOnly: row.AttorneyOnly,
		})
		if row.SkipEvent {
			continue
		}
		em, err := s.meta(p)
		if err != nil {
			return err
		}
		em.CreatedAt = now
		em.UpdatedAt = now
		action := row.Action
		if action == "" {
			action = row.Entity + ".updated"
		}
		summary := row.Summary
		if summary == "" {
			summary = row.Field + " changed"
		}
		events = append(events, domain.Event{
			Meta:          em,
			ActorKind:     actorKind(p),
			ActorID:       actorID(p),
			Action:        action,
			RecordType:    row.Entity,
			RecordID:      row.EntityID,
			MatterID:      row.MatterID,
			Summary:       summary,
			ClientVisible: row.ClientVisible || actorKind(p) == "client",
		})
	}
	if len(audits) == 0 && len(events) == 0 {
		return nil
	}
	return s.db.AppendAudit(ctx, audits, events)
}

func missing(err error, entity string) error {
	if errors.Is(err, repository.ErrNotFound) {
		return domain.NotFound(entity)
	}
	return err
}

func conflict(err error) error {
	if errors.Is(err, repository.ErrConflict) {
		return domain.Conflict("duplicate")
	}
	return err
}

func validDate(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func validEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at >= len(s)-1 {
		return false
	}
	return !strings.ContainsAny(s[at+1:], "@ ")
}

func containsID(ids []domain.ID, id domain.ID) bool {
	for _, item := range ids {
		if item == id {
			return true
		}
	}
	return false
}

// idemRecord binds an Idempotency-Key to one actor, operation, target and request.
type idemRecord struct {
	Actor       domain.ID
	Op          string
	Target      string
	Fingerprint string
	Body        []byte
	Pending     bool
}

// idemHold is a key reserved before the operation runs. abort releases it if finish did not.
type idemHold struct {
	s     *Service
	key   string
	rec   idemRecord
	owned bool
}

func (h *idemHold) abort() {
	if h == nil || !h.owned {
		return
	}
	h.s.db.IdemAbort(h.key, h.rec)
	h.owned = false
}

func (h *idemHold) finish(ctx context.Context, body any) error {
	if h == nil || h.key == "" {
		return nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	h.rec.Body = raw
	h.rec.Pending = false
	if err := h.s.db.IdemComplete(ctx, h.key, h.rec); err != nil {
		return err
	}
	h.owned = false
	return nil
}

// idemBegin replays a stored result only when the caller, operation, target and request match.
// The key is reserved under the store lock before the operation runs.
func (s *Service) idemBegin(ctx context.Context, p domain.Principal, key, op, target string, request, dest any) (bool, *idemHold, error) {
	if key == "" {
		return false, nil, nil
	}
	rec := idemRecord{Actor: actorID(p), Op: op, Target: target, Fingerprint: fingerprint(request)}
	prev, replay, err := s.db.IdemClaim(ctx, key, rec)
	if err != nil {
		return false, nil, err
	}
	if replay {
		if err := json.Unmarshal(prev.Body, dest); err != nil {
			return false, nil, domain.Conflict("idempotency key was already used")
		}
		return true, nil, nil
	}
	return false, &idemHold{s: s, key: key, rec: rec, owned: true}, nil
}

func fingerprint(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		raw = []byte{}
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// unitStore is a store that can hold one service mutation in a single transaction.
type unitStore interface {
	Begin(context.Context) (context.Context, func(error) error)
}

// unit starts the store transaction for one mutation. Every exported Service method that
// writes calls it first and returns through done, so its rows and audit entries commit
// together or not at all, and its checks cannot interleave with another mutation.
func (s *Service) unit(ctx context.Context) (context.Context, func(error) error) {
	if u, ok := s.db.(unitStore); ok {
		return u.Begin(ctx)
	}
	return ctx, func(err error) error { return err }
}

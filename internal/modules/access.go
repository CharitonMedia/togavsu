package modules

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// ContextHeader carries a frame's context token on a module's calls back to the core.
const ContextHeader = "X-Togavsu-Context-Token"

// ContextClaims is what a context token says: which user is looking, with which roles,
// at which matter, through which module (PRD 9.4).
type ContextClaims struct {
	Module    string        `json:"mod"`
	UserID    domain.ID     `json:"sub"`
	Roles     []domain.Role `json:"roles"`
	MatterID  domain.ID     `json:"matter,omitempty"`
	ExpiresAt int64         `json:"exp"`
	IssuedAt  int64         `json:"iat"`
	Nonce     string        `json:"nonce"`
}

// ContextToken is the API contract's ContextToken.
type ContextToken struct {
	Token             string        `json:"token"`
	ExpiresAt         time.Time     `json:"expires_at"`
	ViewerUserID      domain.ID     `json:"viewer_user_id"`
	Roles             []domain.Role `json:"roles"`
	MatterID          *domain.ID    `json:"matter_id"`
	AllowedOperations []string      `json:"allowed_operations"`
}

func (s *Service) policy() domain.Policy { return domain.Policy{Walls: s.d.Walls} }

// canReadMatter is the service-layer matter check: walls, roles and client scope.
func (s *Service) canReadMatter(ctx context.Context, p domain.Principal, matter domain.ID) (domain.Decision, error) {
	return s.policy().Authorize(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionRead, ID: matter, MatterID: matter, SharedWithClient: true})
}

// IssueContext signs a short-lived context token for a frame of module showing matter
// (optional) to p. The module never sees the core session cookie; it gets this instead.
// A matter p can't read is not_found, before anything is signed.
func (s *Service) IssueContext(ctx context.Context, p domain.Principal, module string, matter domain.ID) (ContextToken, error) {
	if p.UserID.IsZero() || p.ModuleID != "" || p.Source == domain.SourceModule || p.Source == domain.SourceSystem {
		return ContextToken{}, domain.Forbidden("a context token is issued to a signed-in user")
	}
	in, err := s.Installed(ctx, module)
	if err != nil {
		return ContextToken{}, err
	}
	if !in.Enabled {
		return ContextToken{}, domain.NotFound("module")
	}
	if !matter.IsZero() {
		dec, err := s.canReadMatter(ctx, p, matter)
		if err != nil {
			return ContextToken{}, err
		}
		if !dec.Allow {
			return ContextToken{}, domain.NotFound("matter")
		}
	}
	nonce := make([]byte, limits().NonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return ContextToken{}, err
	}
	now := s.now()
	ttl, err := time.ParseDuration(strconv.Itoa(int(limits().ContextTTLSecond)) + "s")
	if err != nil {
		return ContextToken{}, err
	}
	exp := now.Add(ttl)
	claims := ContextClaims{Module: module, UserID: p.UserID, Roles: append([]domain.Role(nil), p.Roles...), MatterID: matter,
		ExpiresAt: exp.Unix(), IssuedAt: now.Unix(), Nonce: hex.EncodeToString(nonce)}
	body, err := json.Marshal(claims)
	if err != nil {
		return ContextToken{}, err
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	token := payload + "." + s.contextMAC(payload)
	out := ContextToken{Token: token, ExpiresAt: exp, ViewerUserID: p.UserID, Roles: claims.Roles, AllowedOperations: sortedKeys(in.Approved)}
	if !matter.IsZero() {
		m := matter
		out.MatterID = &m
	}
	return out, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *Service) contextMAC(payload string) string {
	mac := hmac.New(sha256.New, s.subkey("context-token"))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ErrContext is a context token that is malformed, forged or expired.
var ErrContext = domain.Unauthenticated("the context token is invalid or expired")

// VerifyContext checks a context token's signature and expiry and that its user is still
// active, and returns the user's current roles that the token also names.
func (s *Service) VerifyContext(ctx context.Context, token string) (ContextClaims, error) {
	payload, mac, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(mac), []byte(s.contextMAC(payload))) {
		return ContextClaims{}, ErrContext
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return ContextClaims{}, ErrContext
	}
	var c ContextClaims
	if err := json.Unmarshal(raw, &c); err != nil || c.Module == "" || c.UserID.IsZero() {
		return ContextClaims{}, ErrContext
	}
	if s.now().Unix() >= c.ExpiresAt {
		return ContextClaims{}, ErrContext
	}
	u, err := s.d.Users.Get(ctx, c.UserID)
	if errors.Is(err, repository.ErrNotFound) || (err == nil && !u.Active) {
		return ContextClaims{}, ErrContext
	}
	if err != nil {
		return ContextClaims{}, err
	}
	current, err := domain.ParseRoles(u.Roles)
	if err != nil {
		return ContextClaims{}, ErrContext
	}
	// A role removed since the token was signed is gone now.
	var roles []domain.Role
	for _, r := range c.Roles {
		for _, cur := range current {
			if r == cur {
				roles = append(roles, r)
			}
		}
	}
	if len(roles) == 0 {
		return ContextClaims{}, ErrContext
	}
	c.Roles = roles
	return c, nil
}

// Delegate turns a module token's call that carries a context token into a call on
// behalf of the viewer: the viewer's user and roles, source module, the module's id and
// token. Walls, roles and attorney-only rules then apply exactly as for the viewer, and
// the module's approved scopes still bound every route. A context token issued for
// another module is refused.
func (s *Service) Delegate(ctx context.Context, caller domain.Principal, contextToken string) (domain.Principal, error) {
	if caller.Source != domain.SourceModule || caller.ModuleID == "" {
		return domain.Principal{}, domain.Forbidden("only a module token presents a context token")
	}
	c, err := s.VerifyContext(ctx, contextToken)
	if err != nil {
		return domain.Principal{}, err
	}
	if c.Module != caller.ModuleID {
		return domain.Principal{}, domain.Forbidden("the context token was issued for another module")
	}
	in, err := s.Installed(ctx, caller.ModuleID)
	if err != nil || !in.Enabled {
		return domain.Principal{}, domain.Forbidden("the module isn't enabled")
	}
	return domain.Principal{UserID: c.UserID, Roles: c.Roles, Source: domain.SourceModule, ModuleID: caller.ModuleID, TokenID: caller.TokenID}, nil
}

// --- include= and published views ---

// recordResource is the access resource and the matter column of each core record.
var recordResource = map[string]struct {
	res    domain.Resource
	matter string
}{
	"matter": {domain.ResMatter, "id"}, "contact": {domain.ResContact, ""}, "user": {domain.ResUser, ""},
	"invoice": {domain.ResInvoice, "matter_id"}, "payment": {domain.ResPayment, "matter_id"},
	"time_entry": {domain.ResTime, "matter_id"}, "expense": {domain.ResExpense, "matter_id"},
	"file": {domain.ResFile, "matter_id"}, "note": {domain.ResNote, "matter_id"},
	"calendar_event": {domain.ResCalendar, "matter_id"}, "deadline": {domain.ResCalendar, "matter_id"},
	"trust_entry": {domain.ResTrust, "matter_id"}, "practice_area": {domain.ResSettings, ""},
}

// resolve finds module.name among enabled modules' readables.
func (s *Service) resolve(ctx context.Context, full string) (Readable, error) {
	mod, name, ok := strings.Cut(full, ".")
	if !ok || mod == "" || name == "" {
		return Readable{}, invalid("include", "include names look like module.name")
	}
	in, err := s.Installed(ctx, mod)
	if err != nil || !in.Enabled {
		return Readable{}, invalid("include", "no enabled module publishes "+full)
	}
	for _, r := range in.Manifest.Readables() {
		if r.Name == name {
			return r, nil
		}
	}
	return Readable{}, invalid("include", "module "+mod+" doesn't publish "+name)
}

// allowed returns the decisions for ids of record as p sees them; ids p can't read are
// absent. Access is the service-layer policy on the core record the rows point to.
func (s *Service) allowed(ctx context.Context, p domain.Principal, record string, ids []domain.ID) (map[domain.ID]domain.Decision, error) {
	rr, ok := recordResource[record]
	if !ok {
		return nil, invalid("record", "unknown record "+record)
	}
	matters := map[domain.ID]domain.ID{}
	if rr.matter == "id" {
		for _, id := range ids {
			matters[id] = id
		}
	} else if rr.matter != "" && len(ids) > 0 {
		table, _ := CoreTable(record)
		args := make([]any, len(ids))
		marks := make([]string, len(ids))
		for i, id := range ids {
			args[i], marks[i] = string(id), fmt.Sprintf("$%d", i+1)
		}
		rows, err := s.q(ctx).QueryContext(ctx, `SELECT id, COALESCE(`+rr.matter+`, '') FROM core.`+quoteIdent(table)+` WHERE id IN (`+strings.Join(marks, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, m string
			if err := rows.Scan(&id, &m); err != nil {
				rows.Close()
				return nil, err
			}
			matters[domain.ID(id)] = domain.ID(m)
		}
		rows.Close()
	}
	out := map[domain.ID]domain.Decision{}
	for _, id := range ids {
		if _, known := matters[id]; rr.matter != "" && !known {
			continue
		}
		dec, err := s.policy().Authorize(ctx, p, domain.ResourceRef{Resource: rr.res, Action: domain.ActionRead, ID: id, MatterID: matters[id], SharedWithClient: true})
		if err != nil {
			return nil, err
		}
		if dec.Allow {
			out[id] = dec
		}
	}
	if record == "contact" {
		return s.contactWalls(ctx, p, out)
	}
	return out, nil
}

// ContactReader is the core contact service: GetContact fails for a contact p may not
// see, such as one only on matters behind a wall p is outside.
type ContactReader interface {
	GetContact(ctx context.Context, p domain.Principal, id domain.ID) (domain.Contact, error)
}

// contactWalls applies the core contact rule to allowed contacts: a contact on matters is
// visible only through a matter p can read, as the core contact service decides.
func (s *Service) contactWalls(ctx context.Context, p domain.Principal, out map[domain.ID]domain.Decision) (map[domain.ID]domain.Decision, error) {
	for id := range out {
		if s.d.Contacts == nil {
			delete(out, id)
			continue
		}
		if _, err := s.d.Contacts.GetContact(ctx, p, id); err != nil {
			var de *domain.APIError
			if errors.As(err, &de) && (de.Code == domain.CodeNotFound || de.Code == domain.CodeForbidden) {
				delete(out, id)
				continue
			}
			return nil, err
		}
	}
	return out, nil
}

func seesAttorneyOnly(p domain.Principal, dec domain.Decision) bool {
	return !dec.Redact && (p.HasRole(domain.RoleAdmin) || p.HasRole(domain.RoleAttorney))
}

// Row is one module row keyed to a core record.
type Row map[string]any

// fetch reads r's columns for the allowed keys in one query on the module schema.
func (s *Service) fetch(ctx context.Context, p domain.Principal, r Readable, allowed map[domain.ID]domain.Decision) (map[domain.ID][]Row, error) {
	out := map[domain.ID][]Row{}
	if len(allowed) == 0 {
		return out, nil
	}
	keys := make([]string, 0, len(allowed))
	for id := range allowed {
		keys = append(keys, string(id))
	}
	sort.Strings(keys)
	cols := []string{quoteIdent(r.Key)}
	for _, c := range r.Columns {
		cols = append(cols, quoteIdent(c.Name))
	}
	args := make([]any, len(keys))
	marks := make([]string, len(keys))
	for i, k := range keys {
		args[i], marks[i] = k, fmt.Sprintf("$%d", i+1)
	}
	q := `SELECT ` + strings.Join(cols, ", ") + ` FROM ` + quoteIdent(SchemaFor(r.Module)) + `.` + quoteIdent(r.Object) +
		` WHERE ` + quoteIdent(r.Key) + ` IN (` + strings.Join(marks, ",") + `) ORDER BY ` + quoteIdent(r.Key)
	err := s.schemas.AsModule(ctx, SchemaFor(r.Module), func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return err
			}
			key := domain.ID(fmt.Sprint(plain(vals[0])))
			dec := allowed[key]
			row := Row{r.Key: string(key)}
			for i, c := range r.Columns {
				if c.AttorneyOnly && !seesAttorneyOnly(p, dec) {
					row[c.Name] = settings.Redacted
					continue
				}
				row[c.Name] = plain(vals[i+1])
			}
			out[key] = append(out[key], row)
		}

		return rows.Err()
	})
	return out, err
}

func plain(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	}
	return v
}

// Include answers include= on a core read or list endpoint (PRD 9.9): for each core
// record id p can read, the named module rows. Walls, roles and attorney-only columns
// apply through the core record the rows point to; rows of records p can't read are
// never returned. A module caller may include its own extensions, and another module's
// published views only with the views:{module}.{view}:read scope the admin approved.
func (s *Service) Include(ctx context.Context, p domain.Principal, scopes []string, record string, ids []domain.ID, names []string) (map[domain.ID]map[string][]Row, error) {
	var rs []Readable
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		r, err := s.resolve(ctx, n)
		if err != nil {
			return nil, err
		}
		if r.Record != record {
			return nil, invalid("include", n+" extends "+r.Record+", not "+record)
		}
		if p.ModuleID != "" && p.ModuleID != r.Module {
			if !r.View || !hasScope(scopes, ViewScope(r.Module, r.Name)) {
				return nil, domain.Forbidden("reading " + n + " needs the " + ViewScope(r.Module, r.Name) + " permission")
			}
		}
		rs = append(rs, r)
	}
	out := map[domain.ID]map[string][]Row{}
	if len(rs) == 0 {
		return out, nil
	}
	allowed, err := s.allowed(ctx, p, record, ids)
	if err != nil {
		return nil, err
	}
	for id := range allowed {
		out[id] = map[string][]Row{}
	}
	for _, r := range rs {
		got, err := s.fetch(ctx, p, r, allowed)
		if err != nil {
			return nil, err
		}
		full := r.Module + "." + r.Name
		for id := range allowed {
			rows := got[id]
			if rows == nil {
				rows = []Row{}
			}
			out[id][full] = rows
		}
	}
	return out, nil
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

// ReadView reads a module's published view for the given core record ids. A bearer
// caller needs views:{module}.{view}:read (approved by the admin at install for a
// module); rows follow the core record's access for p.
func (s *Service) ReadView(ctx context.Context, p domain.Principal, scopes []string, bearer bool, module, view string, ids []domain.ID) ([]Row, error) {
	in, err := s.Installed(ctx, module)
	if err != nil || !in.Enabled {
		return nil, domain.NotFound("view")
	}
	var r Readable
	found := false
	for _, x := range in.Manifest.Readables() {
		if x.View && x.Name == view {
			r, found = x, true
		}
	}
	if !found {
		return nil, domain.NotFound("view")
	}
	if (bearer || p.ModuleID != "") && p.ModuleID != module && !hasScope(scopes, ViewScope(module, view)) {
		return nil, domain.Forbidden("reading this view needs the " + ViewScope(module, view) + " permission")
	}
	if len(ids) == 0 {
		return nil, invalid("record_id", "name the core records to read")
	}
	allowed, err := s.allowed(ctx, p, r.Record, ids)
	if err != nil {
		return nil, err
	}
	got, err := s.fetch(ctx, p, r, allowed)
	if err != nil {
		return nil, err
	}
	out := []Row{}
	for _, id := range ids {
		out = append(out, got[id]...)
	}
	return out, nil
}

// --- frames and widgets ---

// FramePath is the /m/{module}/ address of a module page, carrying the context token.
func FramePath(module, page, token string, query url.Values) string {
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	q.Set("context", token)
	return "/m/" + module + page + "?" + q.Encode()
}

// widgetSource answers a module widget for the dashboard: its click-through target with
// the filters the manifest passes along, and the core page that frames it.
func (s *Service) widgetSource(module, widgetID string) dashboard.Source {
	return func(ctx context.Context, p domain.Principal, filters map[string]string) (dashboard.Widget, error) {
		in, err := s.Installed(ctx, module)
		if err != nil || !in.Enabled {
			return dashboard.Widget{}, domain.NotFound("widget")
		}
		w, ok := in.Manifest.Widget(widgetID)
		if !ok {
			return dashboard.Widget{}, domain.NotFound("widget")
		}
		ws, err := in.Manifest.DashboardWidgets()
		if err != nil {
			return dashboard.Widget{}, err
		}
		target := ""
		for _, dw := range ws {
			if dw.ID == widgetID {
				target = dw.Target
			}
		}
		query := map[string]string{}
		if w.Target != nil {
			for _, k := range w.Target.Params {
				if v, ok := filters[k]; ok {
					query[k] = v
				}
			}
		}
		t := dashboard.Target{Path: target, Query: query}
		frame := dashboard.Target{Path: "/modules/" + module + "/widgets/" + widgetID, Query: query}
		return dashboard.Widget{ID: dashboard.ModuleWidgetID(module, widgetID), Title: w.Title, Target: t, PagePath: target,
			Rows: []dashboard.WidgetRow{{ID: "frame", Title: w.Title, TargetPath: frame.URL()}}}, nil
	}
}

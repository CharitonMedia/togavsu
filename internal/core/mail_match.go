package core

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/practice"
)

func (a *App) moduleMailRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/v1/mail/messages", a.sendModuleMailJSON)
	m.HandleFunc("POST /api/v1/mail/matches", a.matchMailJSON)
	m.HandleFunc("POST /api/v1/mail/filing-grants", a.issueMailFilingGrantJSON)
}

type matchMailResponse struct {
	ContactsByAddress map[string][]practice.MailMatchContact `json:"contacts_by_address"`
	Matters           []practice.MailMatchMatter             `json:"matters"`
	MatchToken        string                                 `json:"match_token,omitempty"`
}

func (a *App) matchMailJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body practice.MailMatchRequest
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	res, err := a.Practice.MatchMail(r.Context(), p, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := matchMailResponse{ContactsByAddress: res.ContactsByAddress, Matters: res.Matters}
	if out.ContactsByAddress == nil {
		out.ContactsByAddress = map[string][]practice.MailMatchContact{}
	}
	if out.Matters == nil {
		out.Matters = []practice.MailMatchMatter{}
	}
	if moduleOwn(p) {
		ids := make([]string, 0, len(res.Matters))
		for _, m := range res.Matters {
			ids = append(ids, string(m.ID))
		}
		now := time.Now()
		tok, err := a.sealMailAuth(mailAuthClaims{
			Workflow: workflowMailMatching, Module: p.ModuleID, MatterIDs: ids,
			ExpiresAt: now.Add(mailAuthTTL).Unix(), IssuedAt: now.Unix(),
		})
		if err != nil {
			writeErr(w, err)
			return
		}
		out.MatchToken = tok
	}
	writeJSON(w, http.StatusOK, out)
}

type filingGrantWrite struct {
	MatchToken string    `json:"match_token"`
	MatterID   domain.ID `json:"matter_id"`
	MessageKey string    `json:"message_key"`
}

func (a *App) issueMailFilingGrantJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !moduleOwn(p) {
		writeErr(w, domain.Forbidden("a mail filing grant is issued to a module service principal"))
		return
	}
	var body filingGrantWrite
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	body.MessageKey = strings.TrimSpace(body.MessageKey)
	if body.MatchToken == "" || body.MatterID.IsZero() || body.MessageKey == "" {
		writeErr(w, domain.Invalid("body", "match_token, matter_id and message_key are required"))
		return
	}
	out, err := a.issueMailFilingGrant(r.Context(), p, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (a *App) issueMailFilingGrant(ctx context.Context, p domain.Principal, in filingGrantWrite) (MailFilingAuthorization, error) {
	match, err := a.openMailAuth(in.MatchToken)
	if err != nil {
		return MailFilingAuthorization{}, err
	}
	if match.Workflow != workflowMailMatching {
		return MailFilingAuthorization{}, domain.Forbidden("match_token is not a mail matching grant")
	}
	if match.Module != p.ModuleID {
		return MailFilingAuthorization{}, domain.Forbidden("the match token was issued to another module")
	}
	allowed := false
	for _, id := range match.MatterIDs {
		if id == string(in.MatterID) {
			allowed = true
			break
		}
	}
	if !allowed {
		return MailFilingAuthorization{}, domain.NotFound("matter")
	}
	view, err := a.Practice.Policy.Walls.View(ctx, domain.Principal{}, in.MatterID)
	if err != nil {
		return MailFilingAuthorization{}, err
	}
	if view.Walled {
		return MailFilingAuthorization{}, domain.NotFound("matter")
	}
	if _, err := a.Practice.GetMatter(ctx, domain.Principal{
		UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceSystem,
	}, in.MatterID); err != nil {
		return MailFilingAuthorization{}, domain.NotFound("matter")
	}
	now := time.Now()
	exp := now.Add(mailAuthTTL)
	tok, err := a.sealMailAuth(mailAuthClaims{
		Workflow: workflowMailFiling, Module: p.ModuleID, MatterID: string(in.MatterID),
		MessageKey: in.MessageKey, ExpiresAt: exp.Unix(), IssuedAt: now.Unix(),
	})
	if err != nil {
		return MailFilingAuthorization{}, err
	}
	if err := a.emitMailFilingAuthorized(ctx, p, in.MatterID, in.MessageKey); err != nil {
		return MailFilingAuthorization{}, err
	}
	return MailFilingAuthorization{
		TaskAuthorization: tok, ModuleID: p.ModuleID, MatterID: in.MatterID, MessageKey: in.MessageKey,
		ExpiresAt: domain.FormatUTC(exp), AllowedOperations: append([]string(nil), mailFilingOperations...), IssuedBy: "core",
	}, nil
}

func (a *App) emitMailFilingAuthorized(ctx context.Context, p domain.Principal, matter domain.ID, messageKey string) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	now := time.Now()
	actor := domain.SystemUserID
	if !p.UserID.IsZero() {
		actor = p.UserID
	}
	ev := domain.Event{
		Meta: domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actor},
		ActorKind: "module", ActorID: actor, Action: "mail.filing_authorized",
		RecordType: "matter", RecordID: matter, MatterID: matter,
		Summary: "Authorized " + p.ModuleID + " to file mail " + messageKey, ClientVisible: false,
	}
	if err := db.PublishEvent(ctx, audit.Conn(ctx, a.Pool), ev); err != nil {
		return err
	}
	if a.Audit == nil {
		return nil
	}
	_, err = a.Audit.Record(ctx, p, audit.Change{
		Entity: "event", EntityID: id, Field: "action", New: "mail.filing_authorized",
		Reason: "mail.filing_authorized",
	})
	return err
}

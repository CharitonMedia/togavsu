package trust

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// ModuleMailWrite is sendModuleMail's body (CORE-04).
type ModuleMailWrite struct {
	Subject      string      `json:"subject"`
	Text         string      `json:"text"`
	LinkPath     string      `json:"link_path"`
	MatterID     domain.ID   `json:"matter_id"`
	ToUserIDs    []domain.ID `json:"to_user_ids"`
	ToContactIDs []domain.ID `json:"to_contact_ids"`
}

// ModuleMailRecipientResult is one recipient outcome. Never includes an address.
type ModuleMailRecipientResult struct {
	UserID    domain.ID `json:"user_id,omitempty"`
	ContactID domain.ID `json:"contact_id,omitempty"`
	Status    string    `json:"status"`
	Error     string    `json:"error,omitempty"`
}

// ModuleMailResult is the sendModuleMail response.
type ModuleMailResult struct {
	Results []ModuleMailRecipientResult `json:"results"`
}

const (
	mailStatusSent    = "sent"
	mailStatusFailed  = "failed"
	mailStatusSkipped = "skipped"
	mailKindModule    = "module_mail"
)

// SendModuleMail resolves recipients, queues and delivers each message through the
// SMTP/outbox path (or MailSender when set), and optionally appends a mail.sent
// timeline row (CORE-04). Addresses never appear in the result.
func (s *Service) SendModuleMail(ctx context.Context, who domain.Principal, in ModuleMailWrite) (ModuleMailResult, error) {
	if who.ModuleID == "" || (who.Source != domain.SourceModule && who.Source != domain.SourceService) {
		return ModuleMailResult{}, domain.Forbidden("only a module token sends module mail")
	}
	subject := strings.TrimSpace(in.Subject)
	text := in.Text
	linkPath := strings.TrimSpace(in.LinkPath)
	switch {
	case subject == "" || utf8.RuneCountInString(subject) > domain.ModuleMailSubjectMaxChars || strings.ContainsAny(subject, "\r\n"):
		return ModuleMailResult{}, domain.Invalid("subject", "subject is 1 to 200 characters on one line")
	case strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > domain.ModuleMailTextMaxChars:
		return ModuleMailResult{}, domain.Invalid("text", "text is 1 to 20000 characters of plain text")
	}
	if linkPath != "" {
		if (!strings.HasPrefix(linkPath, "/portal/") && !strings.HasPrefix(linkPath, "/modules/")) ||
			strings.HasPrefix(linkPath, "//") || strings.ContainsAny(linkPath, "\\\r\n") {
			return ModuleMailResult{}, domain.Invalid("link_path", "link_path must start with /portal/ or /modules/")
		}
		host := strings.TrimSpace(s.FirmDomain)
		if host == "" {
			return ModuleMailResult{}, domain.Invalid("link_path", "firm domain is not configured")
		}
		text = strings.TrimRight(text, "\r\n") + "\n\nhttps://" + host + linkPath + "\n"
	}
	hasUsers := len(in.ToUserIDs) > 0
	hasContacts := len(in.ToContactIDs) > 0
	switch {
	case hasUsers == hasContacts:
		return ModuleMailResult{}, domain.Invalid("recipient", "exactly one of to_user_ids or to_contact_ids is required")
	case hasUsers && len(in.ToUserIDs) > domain.ModuleMailRecipientMax:
		return ModuleMailResult{}, domain.Invalid("to_user_ids", "to_user_ids is 1 to 20 user ids")
	case hasContacts && len(in.ToContactIDs) > domain.ModuleMailRecipientMax:
		return ModuleMailResult{}, domain.Invalid("to_contact_ids", "to_contact_ids is 1 to 20 contact ids")
	}
	serviceCall := who.Source == domain.SourceService && who.UserID.IsZero()
	if hasContacts {
		if in.MatterID.IsZero() {
			return ModuleMailResult{}, domain.Invalid("matter_id", "matter_id is required with to_contact_ids")
		}
		if !serviceCall && !who.HasRole(domain.RoleAdmin) && !who.HasRole(domain.RoleAttorney) && !who.HasRole(domain.RoleStaff) {
			return ModuleMailResult{}, domain.Forbidden("to_contact_ids needs the admin, attorney or staff role")
		}
	}
	if !in.MatterID.IsZero() {
		if err := s.moduleMailMatter(ctx, who, in.MatterID, serviceCall); err != nil {
			return ModuleMailResult{}, err
		}
	}

	type dest struct {
		userID, contactID domain.ID
		to, skipReason    string
	}
	var dests []dest
	if hasUsers {
		for _, id := range in.ToUserIDs {
			u, ok, err := s.activeUser(ctx, id)
			if err != nil {
				return ModuleMailResult{}, err
			}
			if !ok {
				dests = append(dests, dest{userID: id, skipReason: "inactive or unknown user"})
				continue
			}
			if !strings.Contains(u.Email, "@") {
				dests = append(dests, dest{userID: id, skipReason: "user has no email"})
				continue
			}
			dests = append(dests, dest{userID: id, to: u.Email})
		}
	} else {
		parties := map[domain.ID]bool{}
		for _, p := range s.practice.Parties(in.MatterID, "") {
			parties[p.ContactID] = true
		}
		today := s.now().UTC().Format("2006-01-02")
		for _, id := range in.ToContactIDs {
			if !parties[id] {
				dests = append(dests, dest{contactID: id, skipReason: "not a party on the matter"})
				continue
			}
			addr := currentPrimaryEmail(s.practice.Emails(id), today)
			if addr == "" {
				dests = append(dests, dest{contactID: id, skipReason: "contact has no current primary email"})
				continue
			}
			dests = append(dests, dest{contactID: id, to: addr})
		}
	}

	record := in.MatterID
	if record.IsZero() {
		id, err := s.newID()
		if err != nil {
			return ModuleMailResult{}, err
		}
		record = id
	}
	out := ModuleMailResult{Results: make([]ModuleMailRecipientResult, 0, len(dests))}
	anySent := false
	for _, d := range dests {
		row := ModuleMailRecipientResult{UserID: d.userID, ContactID: d.contactID}
		if d.skipReason != "" {
			row.Status = mailStatusSkipped
			row.Error = d.skipReason
			out.Results = append(out.Results, row)
			continue
		}
		mailID, err := s.queueMailID(ctx, who, mailKindModule, record, d.to, subject, text)
		if err != nil {
			return ModuleMailResult{}, err
		}
		status, errMsg := s.deliverQueuedModuleMail(ctx, mailID, d.to, subject, text)
		row.Status = status
		row.Error = errMsg
		if status == mailStatusSent {
			anySent = true
		}
		out.Results = append(out.Results, row)
	}
	if !in.MatterID.IsZero() && anySent {
		if err := s.appendMailSent(ctx, who, in.MatterID, subject); err != nil {
			return ModuleMailResult{}, err
		}
	}
	return out, nil
}

func (s *Service) queueMailID(ctx context.Context, who domain.Principal, kind string, record domain.ID, to, subject, body string) (domain.ID, error) {
	id, err := s.newID()
	if err != nil {
		return "", err
	}
	_, err = s.q(ctx).ExecContext(ctx, `INSERT INTO mail_outbox (id, created_at, updated_at, created_by, kind, record_id, to_address, subject, body) VALUES ($1,$2,$2,$3,$4,$5,$6,$7,$8)`,
		string(id), micro(s.now()), string(actorID(who)), kind, string(record), to, subject, body)
	return id, err
}

func (s *Service) deliverQueuedModuleMail(ctx context.Context, id domain.ID, to, subject, body string) (status, errMsg string) {
	sender := s.MailSender
	if sender == nil {
		return mailStatusFailed, "system mailer is not configured"
	}
	now := micro(s.now())
	// Prefer OutboxSender so Idempotency-Key is the outbox row id on every attempt
	// (first delivery and later DeliverMail retries) per CORE-05.
	var err error
	if os, ok := sender.(OutboxSender); ok {
		err = os.SendOutbox(ctx, Mail{ID: id, Kind: mailKindModule, To: to, Subject: subject, Body: body})
	} else {
		err = sender.Send(ctx, to, subject, body)
	}
	if err != nil {
		// Same connection as queueMailID so the update lands inside idempotentTx.
		_, _ = s.q(ctx).ExecContext(ctx,
			`UPDATE mail_outbox SET attempts = attempts + 1, last_error = $1, updated_at = $2 WHERE id = $3`,
			err.Error(), now, string(id))
		return mailStatusFailed, err.Error()
	}
	_, _ = s.q(ctx).ExecContext(ctx,
		`UPDATE mail_outbox SET attempts = attempts + 1, sent_at = $1, last_error = '', updated_at = $1 WHERE id = $2`,
		now, string(id))
	return mailStatusSent, ""
}

func (s *Service) appendMailSent(ctx context.Context, who domain.Principal, matter domain.ID, subject string) error {
	id, err := s.newID()
	if err != nil {
		return err
	}
	now := s.now()
	actor := actorID(who)
	kind := "staff"
	switch who.Source {
	case domain.SourceSystem:
		kind = "system"
	case domain.SourceAPIToken:
		kind = "agent"
	case domain.SourceModule, domain.SourceService:
		kind = "module"
	}
	ev := domain.Event{
		Meta:          domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actor},
		ActorKind:     kind,
		ActorID:       actor,
		Action:        "mail.sent",
		RecordType:    "matter",
		RecordID:      matter,
		MatterID:      matter,
		Summary:       "Email sent: " + subject,
		ClientVisible: false,
	}
	ev.ActorID = who.EventActor()
	if err := db.PublishEvent(ctx, audit.Conn(ctx, s.db), ev); err != nil {
		return err
	}
	return s.record(ctx, who, "event", id, "action", "", "mail.sent", "mail.sent")
}

func (s *Service) moduleMailMatter(ctx context.Context, who domain.Principal, matter domain.ID, serviceCall bool) error {
	if _, err := s.practice.Matter(ctx, matter); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return domain.NotFound("matter")
		}
		return err
	}
	if serviceCall {
		view, err := s.policy.Walls.View(ctx, who, matter)
		if err != nil {
			return err
		}
		if view.Walled {
			return domain.NotFound("matter")
		}
		return nil
	}
	return s.allow(ctx, who, domain.ResMatter, domain.ActionRead, matter)
}

func currentPrimaryEmail(emails []domain.ContactEmail, today string) string {
	for _, e := range emails {
		if !e.Primary || e.Email == "" {
			continue
		}
		if !domain.EffectiveOn(e.EffectiveFrom, e.EffectiveTo, today) {
			continue
		}
		return e.Email
	}
	return ""
}

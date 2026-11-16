package trust

import (
	"context"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// ClientReplenishment is the CORE-12 allowlist for a client's replenishment read.
type ClientReplenishment struct {
	ID          domain.ID `json:"id"`
	MatterID    domain.ID `json:"matter_id"`
	AmountCents int64     `json:"amount_cents"`
	Message     string    `json:"message"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

// ClientTrustMatter is one readable matter's trust summary for GET /api/v1/me/trust.
type ClientTrustMatter struct {
	MatterID              domain.ID             `json:"matter_id"`
	BalanceCents          int64                 `json:"balance_cents"`
	ReplenishmentRequests []ClientReplenishment `json:"replenishment_requests"`
}

// ClientLedgerEntry is one ledger row for GET /api/v1/me/trust-ledger (CORE-12).
type ClientLedgerEntry struct {
	Date        string `json:"date"`
	Type        string `json:"type"`
	AmountCents int64  `json:"amount_cents"`
	Description string `json:"description"`
}

// clientOnly reports a principal whose only firm-facing role is client (CORE-12).
func clientOnly(p domain.Principal) bool {
	return p.HasRole(domain.RoleClient) && !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) &&
		!p.HasRole(domain.RoleStaff) && !p.HasRole(domain.RoleAccountant)
}

// linkedContact is the caller's linked contact id, or empty when none.
func (s *Service) linkedContact(ctx context.Context, who domain.Principal) (domain.ID, error) {
	if who.UserID.IsZero() {
		return "", nil
	}
	u, err := s.users.Get(ctx, who.UserID)
	if err != nil {
		return "", err
	}
	return u.ContactID, nil
}

// clientReplenishment projects a replenishment to the CORE-12 allowlist.
func clientReplenishment(r Replenishment) ClientReplenishment {
	return ClientReplenishment{
		ID: r.ID, MatterID: r.MatterID, AmountCents: r.AmountCents,
		Message: r.Message, Status: r.Status, CreatedAt: r.CreatedAt,
	}
}

// ClientTrust lists each readable matter's own balance and active replenishment
// requests naming the client's linked contact (CORE-12).
func (s *Service) ClientTrust(ctx context.Context, who domain.Principal, matters []domain.ID) ([]ClientTrustMatter, error) {
	if !clientOnly(who) {
		return nil, domain.Forbidden("client role is required")
	}
	contact, err := s.linkedContact(ctx, who)
	if err != nil {
		return nil, err
	}
	if contact.IsZero() {
		return nil, domain.Forbidden("linked contact is required")
	}
	out := make([]ClientTrustMatter, 0, len(matters))
	for _, matter := range matters {
		if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, matter); err != nil {
			return nil, err
		}
		bal, err := s.clientMatterCents(ctx, contact, matter)
		if err != nil {
			return nil, err
		}
		repls, err := s.visibleRepls(ctx, who, matter, ReplenishActive)
		if err != nil {
			return nil, err
		}
		row := ClientTrustMatter{MatterID: matter, BalanceCents: bal, ReplenishmentRequests: []ClientReplenishment{}}
		for _, r := range repls {
			if r.ClientID == contact {
				row.ReplenishmentRequests = append(row.ReplenishmentRequests, clientReplenishment(r))
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// ClientTrustLedger lists ledger entries naming the client's linked contact on matter
// (CORE-12). Date, type, amount and description only; no account numbers.
func (s *Service) ClientTrustLedger(ctx context.Context, who domain.Principal, matter domain.ID) ([]ClientLedgerEntry, error) {
	if !clientOnly(who) {
		return nil, domain.Forbidden("client role is required")
	}
	if matter.IsZero() {
		return nil, domain.Invalid("matter_id", "matter_id is required")
	}
	contact, err := s.linkedContact(ctx, who)
	if err != nil {
		return nil, err
	}
	if contact.IsZero() {
		return nil, domain.Forbidden("linked contact is required")
	}
	if err := s.allow(ctx, who, domain.ResTrust, domain.ActionRead, matter); err != nil {
		return nil, err
	}
	entries, err := s.visibleEntries(ctx, who, EntryFilter{MatterID: matter, ClientID: contact})
	if err != nil {
		return nil, err
	}
	out := make([]ClientLedgerEntry, 0, len(entries))
	for _, e := range entries {
		if e.ClientID != contact {
			continue
		}
		out = append(out, ClientLedgerEntry{
			Date: e.CreatedAt.UTC().Format("2006-01-02"), Type: e.EntryType,
			AmountCents: e.AmountCents, Description: e.Description,
		})
	}
	return out, nil
}

// replenishmentForClient returns r when the client may see it, else not_found.
func (s *Service) replenishmentForClient(ctx context.Context, who domain.Principal, r Replenishment) (Replenishment, error) {
	contact, err := s.linkedContact(ctx, who)
	if err != nil {
		return Replenishment{}, err
	}
	if contact.IsZero() || r.ClientID != contact {
		return Replenishment{}, domain.NotFound("trust_replenishment")
	}
	return r, nil
}

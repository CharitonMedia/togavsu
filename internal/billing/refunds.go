package billing

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Refund events (CORE-24).
const (
	RefundEventCreated     = "payment.refund_created"
	RefundEventAuthorized  = "payment.refund_authorized"
	RefundEventCompleted   = "payment.refunded"
	RefundEventFailed      = "payment.refund_failed"
	RefundHeader           = ProcessorHeader
	kindRefund             = "payment_refund"
	refundGrantPrefix      = "tra1."
	RefundPending          = "pending"
	RefundCompleted        = "completed"
	RefundFailed           = "failed"
	RefundNone             = "none"
	RefundFull             = "full"
	RefundPartial          = "partial"
)

// Refund is a payment_refunds row (CORE-24). Status is pending, completed or failed.
type Refund struct {
	domain.Meta
	PaymentID       domain.ID    `json:"payment_id"`
	AmountCents     domain.Cents `json:"amount_cents"`
	Reason          string       `json:"reason"`
	ReferenceNumber string       `json:"reference_number"`
	Status          string       `json:"status"`
	TrustEntryID    domain.ID    `json:"trust_entry_id,omitempty"`
	Source          string       `json:"source"`
	SecretHash      string       `json:"-"`
	ExpiresAt       time.Time    `json:"-"`
	Revoked         bool         `json:"-"`
}

// RefundInput creates a refund.
type RefundInput struct {
	AmountCents     domain.Cents
	Reason          string
	ReferenceNumber string
	Status          string
}

// CreatePaymentRefund records a refund on a completed payment (CORE-24). Admin and attorney
// only. A trust payment also needs trust:withdraw. Zero or over-remaining amount is
// refund_exceeds_payment. Pending delivers refund_authorization to the payment's source module.
func (s *Service) CreatePaymentRefund(ctx context.Context, p domain.Principal, paymentID domain.ID, in RefundInput) (Refund, error) {
	if !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) {
		return Refund{}, domain.Forbidden("only an attorney or admin may refund a payment")
	}
	if err := viewerless(p); err != nil {
		return Refund{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return Refund{}, domain.Invalid("reason", "is required")
	}
	status := in.Status
	if status == "" {
		status = RefundPending
	}
	if status != RefundPending && status != RefundCompleted {
		return Refund{}, domain.Invalid("status", "must be pending or completed")
	}
	var out Refund
	err := s.write(ctx, p, func(t *txn) error {
		pay, err := s.payment(t.ctx, paymentID)
		if err != nil {
			return err
		}
		if err := s.allow(t.ctx, p, domain.ResPayment, domain.ActionWrite, pay.MatterID); err != nil {
			return err
		}
		if pay.Status != PayCompleted {
			return domain.Conflict("only a completed payment can be refunded")
		}
		if pay.Destination == DestTrust {
			if err := s.allow(t.ctx, p, domain.ResTrust, domain.ActionTrustWithdraw, pay.MatterID); err != nil {
				return err
			}
			if s.d.Trust == nil {
				return ErrTrustUnavailable
			}
		}
		remaining := pay.AmountCents - pay.RefundedCents
		// Count pending refunds against the remaining balance.
		pending, err := s.refundedPending(t.ctx, pay.ID)
		if err != nil {
			return err
		}
		remaining -= pending
		if !positive(int64(in.AmountCents)) || in.AmountCents > remaining {
			return domain.RefundExceeds("the refund exceeds the payment's remaining amount")
		}
		meta, err := s.meta(p)
		if err != nil {
			return err
		}
		row := Refund{
			Meta: meta, PaymentID: pay.ID, AmountCents: in.AmountCents, Reason: reason,
			ReferenceNumber: strings.TrimSpace(in.ReferenceNumber), Status: status,
			Source: sourceOf(p, ""),
		}
		if moduleIDPattern.MatchString(pay.Source) {
			row.Source = pay.Source
		}
		secret := make([]byte, sha256.Size)
		if status == RefundPending && moduleIDPattern.MatchString(pay.Source) && s.processors != nil {
			if _, err := rand.Read(secret); err != nil {
				return err
			}
			sum := sha256.Sum256(secret)
			lifetime, err := time.ParseDuration(s.setting(t.ctx, "billing.processor_authorization_lifetime"))
			if err != nil {
				return err
			}
			row.SecretHash = hex.EncodeToString(sum[:])
			row.ExpiresAt = s.now().Add(lifetime)
		}
		if status == RefundCompleted {
			if err := s.completeRefund(t, &pay, &row); err != nil {
				return err
			}
		}
		if err := t.put(kindPayment, pay.ID, pay); err != nil {
			return err
		}
		// SQL payment_refunds / payments columns are the module-linkable mirror (CORE-22/24).
		// billing_records stays authoritative; a missing projection must not fail the refund.
		if err := t.put(kindRefund, row.ID, row); err != nil {
			return err
		}
		if err := t.created("payment_refund", row.ID, RefundView(row), ""); err != nil {
			return err
		}
		if err := t.event(RefundEventCreated, "payment_refund", row.ID, pay.MatterID,
			"Refund "+money(row.AmountCents)+" created", false); err != nil {
			return err
		}
		if status == RefundCompleted {
			if err := t.event(RefundEventCompleted, "payment_refund", row.ID, pay.MatterID,
				"Refund "+money(row.AmountCents)+" completed", false); err != nil {
				return err
			}
		} else if row.SecretHash != "" && s.processors != nil {
			if err := t.event(RefundEventAuthorized, "payment_refund", row.ID, pay.MatterID,
				"Refund authorization for "+row.Source, false); err != nil {
				return err
			}
			ev := t.events[len(t.events)-1]
			cred := refundGrantPrefix + string(row.ID) + "." + base64.RawURLEncoding.EncodeToString(secret)
			if err := s.processors.DeliverGrant(t.ctx, row.Source, ev, "refund_authorization", refundAuthView(row, pay), cred); err != nil {
				return err
			}
		}
		out = row
		return nil
	})
	return out, err
}

// UpdatePaymentRefundStatus moves pending to completed or failed (CORE-24). Same status replays.
// A module may present the refund_authorization grant instead of a viewer.
func (s *Service) UpdatePaymentRefundStatus(ctx context.Context, p domain.Principal, paymentID, refundID domain.ID, status, grant string) (Refund, error) {
	if status != RefundCompleted && status != RefundFailed && status != RefundPending {
		return Refund{}, domain.Invalid("status", "must be pending, completed or failed")
	}
	var dp domain.Principal
	var err error
	if grant != "" {
		_, dp, err = s.refundGrant(ctx, p, grant)
		if err != nil {
			return Refund{}, err
		}
	} else {
		if err := viewerless(p); err != nil {
			return Refund{}, err
		}
		if !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) {
			return Refund{}, domain.Forbidden("only an attorney or admin may update a refund")
		}
		dp = p
	}
	var out Refund
	err = s.write(ctx, dp, func(t *txn) error {
		row, err := s.refund(t.ctx, refundID)
		if err != nil {
			return err
		}
		if row.PaymentID != paymentID {
			return domain.NotFound("payment_refund")
		}
		pay, err := s.payment(t.ctx, paymentID)
		if err != nil {
			return err
		}
		if err := s.allow(t.ctx, dp, domain.ResPayment, domain.ActionWrite, pay.MatterID); err != nil {
			return err
		}
		out = row
		if row.Status == status {
			return nil
		}
		if row.Status != RefundPending {
			return domain.Conflict("a " + row.Status + " refund cannot change status")
		}
		if status == RefundPending {
			return nil
		}
		before := row
		row.Status = status
		row.UpdatedAt = s.now()
		if status == RefundCompleted {
			if pay.Destination == DestTrust {
				if err := s.allow(t.ctx, dp, domain.ResTrust, domain.ActionTrustWithdraw, pay.MatterID); err != nil {
					return err
				}
			}
			if err := s.completeRefund(t, &pay, &row); err != nil {
				return err
			}
		}
		if err := t.put(kindPayment, pay.ID, pay); err != nil {
			return err
		}
		if err := t.put(kindRefund, row.ID, row); err != nil {
			return err
		}
		out = row
		if err := t.diff("payment_refund", row.ID, before, row, ""); err != nil {
			return err
		}
		ev := RefundEventFailed
		if status == RefundCompleted {
			ev = RefundEventCompleted
		}
		return t.event(ev, "payment_refund", row.ID, pay.MatterID, "Refund "+status, false)
	})
	return out, err
}

// ListPaymentRefunds lists refunds on a payment the caller may read.
func (s *Service) ListPaymentRefunds(ctx context.Context, p domain.Principal, paymentID domain.ID) ([]Refund, error) {
	pay, err := s.Payment(ctx, p, paymentID)
	if err != nil {
		return nil, err
	}
	_ = pay
	rows, err := listOf[Refund](ctx, s.d.Store, kindRefund)
	if err != nil {
		return nil, err
	}
	var out []Refund
	for _, r := range rows {
		if r.PaymentID == paymentID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *Service) refund(ctx context.Context, id domain.ID) (Refund, error) {
	var row Refund
	if err := s.d.Store.Get(ctx, kindRefund, string(id), &row); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Refund{}, domain.NotFound("payment_refund")
		}
		return Refund{}, err
	}
	return row, nil
}

func (s *Service) refundedPending(ctx context.Context, paymentID domain.ID) (domain.Cents, error) {
	rows, err := listOf[Refund](ctx, s.d.Store, kindRefund)
	if err != nil {
		return 0, err
	}
	var sum domain.Cents
	for _, r := range rows {
		if r.PaymentID == paymentID && r.Status == RefundPending {
			sum += r.AmountCents
		}
	}
	return sum, nil
}

func (s *Service) completeRefund(t *txn, pay *Payment, row *Refund) error {
	if pay.Destination == DestTrust {
		posted, err := s.d.Trust.Post(t.ctx, t.p, TrustPosting{
			EntryType: "refund", MatterID: pay.MatterID, ClientID: pay.ContactID, AccountID: pay.TrustAccountID,
			AmountCents: row.AmountCents, Method: pay.Method, Description: "Refund " + row.Reason, PaymentID: pay.ID,
		})
		if err != nil {
			return err
		}
		row.TrustEntryID = t.trusted(posted).EntryID
	} else if !pay.InvoiceID.IsZero() {
		if err := s.applyToInvoice(t, pay.InvoiceID, -row.AmountCents); err != nil {
			return err
		}
	}
	pay.RefundedCents += row.AmountCents
	switch {
	case pay.RefundedCents <= 0:
		pay.RefundState = RefundNone
	case pay.RefundedCents >= pay.AmountCents:
		pay.RefundState = RefundFull
	default:
		pay.RefundState = RefundPartial
	}
	pay.UpdatedAt = s.now()
	return nil
}

func (s *Service) refundGrant(ctx context.Context, p domain.Principal, credential string) (Refund, domain.Principal, error) {
	denied := domain.Forbidden("the refund authorization is missing, invalid, expired or revoked")
	rest, ok := strings.CutPrefix(strings.TrimSpace(credential), refundGrantPrefix)
	id, encoded, cut := strings.Cut(rest, ".")
	if !ok || !cut {
		return Refund{}, domain.Principal{}, denied
	}
	secret, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return Refund{}, domain.Principal{}, denied
	}
	row, err := s.refund(ctx, domain.ID(id))
	if err != nil {
		return Refund{}, domain.Principal{}, denied
	}
	sum := sha256.Sum256(secret)
	if !hmac.Equal([]byte(hex.EncodeToString(sum[:])), []byte(row.SecretHash)) {
		return Refund{}, domain.Principal{}, denied
	}
	if (p.Source != domain.SourceModule && p.Source != domain.SourceService) || p.ModuleID != row.Source {
		return Refund{}, domain.Principal{}, domain.Forbidden("the refund authorization was issued to another module")
	}
	if row.Revoked || row.Status != RefundPending || !s.now().Before(row.ExpiresAt) {
		return Refund{}, domain.Principal{}, denied
	}
	user, err := s.d.Practice.User(ctx, row.CreatedBy)
	if err != nil || !user.Active {
		return Refund{}, domain.Principal{}, denied
	}
	roles, err := domain.ParseRoles(user.Roles)
	if err != nil {
		return Refund{}, domain.Principal{}, denied
	}
	return row, domain.Principal{UserID: user.ID, Roles: roles, Source: domain.SourceModule, ModuleID: row.Source, TokenID: p.TokenID}, nil
}

func refundAuthView(row Refund, pay Payment) map[string]any {
	return map[string]any{
		"module_id": row.Source, "refund_id": row.ID, "payment_id": row.PaymentID,
		"matter_id": pay.MatterID, "amount_cents": int64(row.AmountCents),
		"allowed_operations": []string{"updatePaymentRefundStatus"},
		"expires_at":         row.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"revoked":            row.Revoked, "issued_by": "core",
	}
}

// RefundView is the API body for a refund.
func RefundView(r Refund) map[string]any {
	return map[string]any{
		"id": r.ID, "created_at": r.CreatedAt.UTC().Format(time.RFC3339Nano), "created_by": r.CreatedBy,
		"updated_at": r.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"payment_id": r.PaymentID, "amount_cents": int64(r.AmountCents), "reason": r.Reason,
		"reference_number": r.ReferenceNumber, "status": r.Status,
		"trust_entry_id": idOrNil(r.TrustEntryID), "source": r.Source,
	}
}

// upsertCoreRefund writes the SQL payment_refunds row when the store is Postgres.
func (s *Service) upsertCoreRefund(ctx context.Context, row Refund) error {
	pg, ok := s.d.Store.(*Postgres)
	if !ok {
		return nil
	}
	conn := audit.Conn(ctx, pg.pool)
	_, err := conn.ExecContext(ctx, `INSERT INTO payment_refunds (id, created_at, updated_at, created_by, payment_id, amount_cents, reason, reference_number, status, trust_entry_id, source)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (id) DO UPDATE SET updated_at = EXCLUDED.updated_at, status = EXCLUDED.status, trust_entry_id = EXCLUDED.trust_entry_id`,
		string(row.ID), row.CreatedAt.UnixMicro(), row.UpdatedAt.UnixMicro(), string(row.CreatedBy),
		string(row.PaymentID), int64(row.AmountCents), row.Reason, row.ReferenceNumber, row.Status,
		nullID(row.TrustEntryID), row.Source)
	return err
}

// syncCorePaymentRefund updates payments.refunded_cents and refund_state on the SQL table,
// upserting the payment row when needed so the refund FK can resolve.
func (s *Service) syncCorePaymentRefund(ctx context.Context, pay Payment) error {
	pg, ok := s.d.Store.(*Postgres)
	if !ok {
		return nil
	}
	conn := audit.Conn(ctx, pg.pool)
	_, err := conn.ExecContext(ctx, `INSERT INTO payments (id, created_at, updated_at, created_by, contact_id, matter_id, amount_cents, destination, method, reference_number, status, invoice_id, trust_entry_id, source, refunded_cents, refund_state)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
ON CONFLICT (id) DO UPDATE SET updated_at = EXCLUDED.updated_at, refunded_cents = EXCLUDED.refunded_cents, refund_state = EXCLUDED.refund_state`,
		string(pay.ID), pay.CreatedAt.UnixMicro(), pay.UpdatedAt.UnixMicro(), string(pay.CreatedBy),
		string(pay.ContactID), string(pay.MatterID), int64(pay.AmountCents), pay.Destination, pay.Method,
		pay.ReferenceNumber, pay.Status, nullID(pay.InvoiceID), nullID(pay.TrustEntryID), pay.Source,
		int64(pay.RefundedCents), pay.RefundState)
	return err
}

func nullID(id domain.ID) any {
	if id.IsZero() {
		return nil
	}
	return string(id)
}

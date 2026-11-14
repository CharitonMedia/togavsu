package billing

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// ProcessorEvent is the module event that carries a processor authorization.
const ProcessorEvent = "payment.processor_authorized"

// ProcessorHeader carries the opaque processor grant: on the delivery, and on the module's
// later createPayment and updatePaymentStatus calls.
const ProcessorHeader = "X-Togavsu-Task-Authorization"

const kindProcessorInit = "processor_initialization"

// grantPrefix starts every processor grant: tpa1.<initialization id>.<secret>.
const grantPrefix = "tpa1."

// Processors is the module runtime seam for payment processors. Processor reports whether
// module is a registered processor (installed, enabled and subscribed to ProcessorEvent) and
// the scopes the admin approved for it. DeliverGrant queues ev to module alone, inside the
// transaction ctx carries, with grant under field in the body and credential in
// ProcessorHeader. modules.Service provides it.
type Processors interface {
	Processor(ctx context.Context, module string) (approved map[string]bool, registered bool, err error)
	DeliverGrant(ctx context.Context, module string, ev domain.Event, field string, grant any, credential string) error
}

// SetProcessors wires the module runtime, which the core opens after billing. Until it is
// set every processor initialization is refused.
func (s *Service) SetProcessors(p Processors) { s.processors = p }

// ProcessorInput asks the core to authorize one processor for one payment.
type ProcessorInput struct {
	ProcessorModuleID string
	ContactID         domain.ID
	MatterID          domain.ID
	AmountCents       domain.Cents
	Destination       string
	TrustAccountID    domain.ID
	InvoiceID         domain.ID
}

// ProcessorInit is the stored initialization. PaymentID is reserved here; the processor's
// createPayment must use it, once. SecretHash is the SHA-256 of the grant's secret; the
// secret itself is only in the module's delivery. AuthorizedBy is the staff member whose
// access the grant carries.
type ProcessorInit struct {
	domain.Meta
	PaymentID         domain.ID    `json:"payment_id"`
	ProcessorModuleID string       `json:"processor_module_id"`
	ContactID         domain.ID    `json:"contact_id"`
	MatterID          domain.ID    `json:"matter_id"`
	AmountCents       domain.Cents `json:"amount_cents"`
	Destination       string       `json:"destination"`
	TrustAccountID    domain.ID    `json:"trust_account_id,omitempty"`
	InvoiceID         domain.ID    `json:"invoice_id,omitempty"`
	Status            string       `json:"status"`
	ExpiresAt         time.Time    `json:"expires_at"`
	Revoked           bool         `json:"revoked"`
	SecretHash        string       `json:"secret_hash"`
	AuthorizedBy      domain.ID    `json:"authorized_by"`
}

var moduleIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// processorFields are the payment fields a processor sets on its granted calls.
var processorFields = []string{"status", "method", "reference_number"}

// InitiateProcessorPayment authorizes one registered processor module for one payment on a
// matter the staff caller can see. It reserves the payment id, stores the initialization and
// queues payment.processor_authorized with the grant to that module only. It writes no
// payment and posts no trust. The caller's token scopes (payments:write, and trust:write for
// trust) are the handler's; the module's approved scopes are checked here, before delivery.
func (s *Service) InitiateProcessorPayment(ctx context.Context, p domain.Principal, in ProcessorInput) (ProcessorInit, error) {
	if p.UserID.IsZero() {
		return ProcessorInit{}, domain.Forbidden("a staff member authorizes a payment processor")
	}
	if !moduleIDPattern.MatchString(in.ProcessorModuleID) {
		return ProcessorInit{}, domain.Invalid("processor_module_id", "is not a module id")
	}
	if p.Source == domain.SourceModule && p.ModuleID == in.ProcessorModuleID {
		return ProcessorInit{}, domain.Forbidden("a processor cannot authorize itself")
	}
	if err := amountField("amount_cents", in.AmountCents); err != nil {
		return ProcessorInit{}, err
	}
	if err := s.allow(ctx, p, domain.ResPayment, domain.ActionWrite, in.MatterID); err != nil {
		return ProcessorInit{}, err
	}
	if _, err := s.matter(ctx, in.MatterID); err != nil {
		return ProcessorInit{}, err
	}
	scopes := []string{"payments:write"}
	switch in.Destination {
	case DestOperating:
		if !in.TrustAccountID.IsZero() {
			return ProcessorInit{}, domain.Invalid("trust_account_id", "an operating payment has no trust account")
		}
	case DestTrust:
		if in.TrustAccountID.IsZero() {
			return ProcessorInit{}, domain.Invalid("trust_account_id", "a trust payment names its trust account")
		}
		if !in.InvoiceID.IsZero() {
			return ProcessorInit{}, domain.Invalid("invoice_id", "a trust deposit is not applied to an invoice")
		}
		if err := s.allow(ctx, p, domain.ResTrust, domain.ActionTrustDeposit, in.MatterID); err != nil {
			return ProcessorInit{}, err
		}
		if s.d.Trust == nil {
			return ProcessorInit{}, ErrTrustUnavailable
		}
		scopes = append(scopes, "trust:write")
	default:
		return ProcessorInit{}, domain.Invalid("destination", "must be operating or trust")
	}
	if in.ContactID.IsZero() || !s.isParty(in.MatterID, in.ContactID) {
		return ProcessorInit{}, domain.Invalid("contact_id", "must be a party on the matter")
	}
	if !in.InvoiceID.IsZero() {
		inv, err := s.invoice(ctx, in.InvoiceID)
		if err != nil || inv.MatterID != in.MatterID {
			return ProcessorInit{}, domain.Invalid("invoice_id", "must be an invoice on this matter")
		}
		if !inv.Open() {
			return ProcessorInit{}, domain.Conflict("payments apply only to a sent invoice with a balance")
		}
	}
	if s.processors == nil {
		return ProcessorInit{}, domain.Forbidden("no payment processor is registered")
	}
	approved, registered, err := s.processors.Processor(ctx, in.ProcessorModuleID)
	if err != nil {
		return ProcessorInit{}, err
	}
	if !registered {
		return ProcessorInit{}, domain.Forbidden("that module is not a registered payment processor")
	}
	for _, sc := range scopes {
		if !approved[sc] {
			return ProcessorInit{}, domain.Forbidden("the processor module is not approved for " + sc)
		}
	}
	meta, err := s.meta(p)
	if err != nil {
		return ProcessorInit{}, err
	}
	payID, err := s.d.NewID()
	if err != nil {
		return ProcessorInit{}, err
	}
	secret := make([]byte, sha256.Size)
	if _, err := rand.Read(secret); err != nil {
		return ProcessorInit{}, err
	}
	sum := sha256.Sum256(secret)
	lifetime, err := time.ParseDuration(s.setting(ctx, "billing.processor_authorization_lifetime"))
	if err != nil {
		return ProcessorInit{}, err
	}
	row := ProcessorInit{
		Meta: meta, PaymentID: payID, ProcessorModuleID: in.ProcessorModuleID, ContactID: in.ContactID, MatterID: in.MatterID,
		AmountCents: in.AmountCents, Destination: in.Destination, TrustAccountID: in.TrustAccountID, InvoiceID: in.InvoiceID,
		Status: "authorized", ExpiresAt: s.now().Add(lifetime), SecretHash: hex.EncodeToString(sum[:]),
		AuthorizedBy: p.UserID,
	}
	credential := grantPrefix + string(row.ID) + "." + base64.RawURLEncoding.EncodeToString(secret)
	err = s.write(ctx, p, func(t *txn) error {
		if err := t.put(kindProcessorInit, row.ID, row); err != nil {
			return err
		}
		if err := t.created("processor_initialization", row.ID, ProcessorInitView(row), "authorized "+row.ProcessorModuleID); err != nil {
			return err
		}
		if err := t.event(ProcessorEvent, "processor_initialization", row.ID, row.MatterID,
			"Payment processor "+row.ProcessorModuleID+" authorized for "+money(row.AmountCents), false); err != nil {
			return err
		}
		ev := t.events[len(t.events)-1]
		return s.processors.DeliverGrant(t.ctx, row.ProcessorModuleID, ev, "processor_authorization", authorizationView(row), credential)
	})
	if err != nil {
		return ProcessorInit{}, err
	}
	return row, nil
}

// processorGrant resolves a grant a processor module presents. The module token must be the
// processor's own; the grant must check, be unexpired and unrevoked. The principal returned
// is the authorizing staff member's current access, acting through the module, so roles and
// walls still apply to the bound payment.
func (s *Service) processorGrant(ctx context.Context, p domain.Principal, credential string) (ProcessorInit, domain.Principal, error) {
	denied := domain.Forbidden("the processor authorization is missing, invalid, expired or revoked")
	rest, ok := strings.CutPrefix(strings.TrimSpace(credential), grantPrefix)
	id, encoded, cut := strings.Cut(rest, ".")
	if !ok || !cut {
		return ProcessorInit{}, domain.Principal{}, denied
	}
	secret, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return ProcessorInit{}, domain.Principal{}, denied
	}
	var row ProcessorInit
	if err := s.d.Store.Get(ctx, kindProcessorInit, id, &row); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ProcessorInit{}, domain.Principal{}, denied
		}
		return ProcessorInit{}, domain.Principal{}, err
	}
	sum := sha256.Sum256(secret)
	if !hmac.Equal([]byte(hex.EncodeToString(sum[:])), []byte(row.SecretHash)) {
		return ProcessorInit{}, domain.Principal{}, denied
	}
	if p.Source != domain.SourceModule || p.ModuleID != row.ProcessorModuleID {
		return ProcessorInit{}, domain.Principal{}, domain.Forbidden("the processor authorization was issued to another module")
	}
	if row.Revoked || row.Status != "authorized" || !s.now().Before(row.ExpiresAt) {
		return ProcessorInit{}, domain.Principal{}, denied
	}
	user, err := s.d.Practice.User(ctx, row.AuthorizedBy)
	if err != nil || !user.Active {
		return ProcessorInit{}, domain.Principal{}, denied
	}
	roles, err := domain.ParseRoles(user.Roles)
	if err != nil {
		return ProcessorInit{}, domain.Principal{}, denied
	}
	return row, domain.Principal{UserID: user.ID, Roles: roles, Source: domain.SourceModule, ModuleID: row.ProcessorModuleID, TokenID: p.TokenID}, nil
}

// ProcessorGrantDestination returns the destination a presented grant binds, for the
// handler's trust:write check on the module token.
func (s *Service) ProcessorGrantDestination(ctx context.Context, p domain.Principal, credential string) (string, error) {
	row, _, err := s.processorGrant(ctx, p, credential)
	return row.Destination, err
}

// CreateProcessorPayment records the one payment a processor grant binds, with the reserved
// payment id. The matter, contact and invoice must be the bound ones (another is not_found);
// the amount, destination and trust account must match (forbidden). A repeat with the same
// fields returns the original payment; a different one is a conflict.
func (s *Service) CreateProcessorPayment(ctx context.Context, p domain.Principal, credential string, in PaymentInput) (Payment, error) {
	grant, dp, err := s.processorGrant(ctx, p, credential)
	if err != nil {
		return Payment{}, err
	}
	switch {
	case in.MatterID != grant.MatterID:
		return Payment{}, domain.NotFound("matter")
	case in.ContactID != grant.ContactID:
		return Payment{}, domain.NotFound("contact")
	case in.InvoiceID != grant.InvoiceID:
		return Payment{}, domain.NotFound("invoice")
	case in.AmountCents != grant.AmountCents:
		return Payment{}, domain.Forbidden("the amount is not the authorized amount")
	case in.Destination != grant.Destination || in.TrustAccountID != grant.TrustAccountID:
		return Payment{}, domain.Forbidden("the destination is not the authorized destination")
	}
	// The grant lets a processor set status, method and reference_number (processorFields);
	// the bound fields must match above. Any other field is field_locked, so the payment is
	// always attributed to the processor the grant names.
	switch {
	case in.Source != "":
		return Payment{}, domain.FieldLocked("source", "source is outside the processor authorization")
	case in.ReceivedOn != "":
		return Payment{}, domain.FieldLocked("received_on", "received_on is outside the processor authorization")
	}
	in.Source = grant.ProcessorModuleID
	row, err := s.buildPayment(ctx, dp, in)
	if err != nil {
		return Payment{}, err
	}
	row.ID = grant.PaymentID
	var out Payment
	err = s.write(ctx, dp, func(t *txn) error {
		if prior, err := s.payment(t.ctx, grant.PaymentID); err == nil {
			if prior.Status != row.Status || prior.Method != row.Method || prior.ReferenceNumber != row.ReferenceNumber {
				return domain.Conflict("this authorization already recorded a different payment")
			}
			out = prior
			return nil
		}
		out = row
		return s.insertPayment(t, &out)
	})
	if err != nil {
		return Payment{}, err
	}
	return out, nil
}

// UpdateProcessorPaymentStatus is updatePaymentStatus under a processor grant: only the
// bound payment (another is not_found), and only its status.
func (s *Service) UpdateProcessorPaymentStatus(ctx context.Context, p domain.Principal, credential string, id domain.ID, status string) (Payment, error) {
	grant, dp, err := s.processorGrant(ctx, p, credential)
	if err != nil {
		return Payment{}, err
	}
	if id != grant.PaymentID {
		return Payment{}, domain.NotFound("payment")
	}
	return s.UpdatePaymentStatus(ctx, dp, id, status)
}

// authorizationView is the processor_authorization body the module receives.
func authorizationView(row ProcessorInit) map[string]any {
	return map[string]any{
		"module_id": row.ProcessorModuleID, "initialization_id": row.ID, "payment_id": row.PaymentID,
		"matter_id": row.MatterID, "contact_id": row.ContactID, "invoice_id": idOrNil(row.InvoiceID),
		"amount_cents": int64(row.AmountCents), "destination": row.Destination, "trust_account_id": idOrNil(row.TrustAccountID),
		"allowed_operations": []string{"createPayment", "updatePaymentStatus"}, "allowed_fields": processorFields,
		"expires_at": row.ExpiresAt.UTC().Format(time.RFC3339Nano), "revoked": row.Revoked, "issued_by": "core",
	}
}

// ProcessorInitView is the contract's ProcessorInitialization; it never carries the grant.
func ProcessorInitView(row ProcessorInit) map[string]any {
	return map[string]any{
		"id": row.ID, "payment_id": row.PaymentID, "processor_module_id": row.ProcessorModuleID, "contact_id": row.ContactID,
		"matter_id": row.MatterID, "amount_cents": int64(row.AmountCents), "destination": row.Destination,
		"trust_account_id": idOrNil(row.TrustAccountID), "invoice_id": idOrNil(row.InvoiceID), "status": row.Status,
		"expires_at": row.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
}

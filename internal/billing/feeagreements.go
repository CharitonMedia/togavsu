package billing

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/access"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Fee agreements (PRD 5): one active agreement per matter, with its fee type, amount,
// services, status (draft, sent, signed, void), signed date, void reason and the signed
// copy as a file record. E-signature is a module's; the core keeps the record and the
// file. Access follows the matter: admins, attorneys and staff who may read or write the
// matter, inside its wall; accountants and clients get none (the contract's roles).

const kindFeeAgreement = "fee_agreement"

// FeeAgreement is one agreement row.
type FeeAgreement struct {
	domain.Meta
	MatterID     domain.ID     `json:"matter_id"`
	FeeType      string        `json:"fee_type"`
	AmountCents  *domain.Cents `json:"amount_cents"`
	Services     string        `json:"services_description"`
	Status       string        `json:"status"`
	SignedOn     string        `json:"signed_on"`
	VoidReason   string        `json:"void_reason"`
	SignedFileID domain.ID     `json:"signed_file_id"`
}

// FeeAgreementInput creates or patches an agreement; nil fields stay as they are.
type FeeAgreementInput struct {
	MatterID     *domain.ID
	FeeType      *string
	AmountCents  **domain.Cents
	Services     *string
	Status       *string
	SignedOn     **string
	VoidReason   *string
	SignedFileID **domain.ID
}

var feeTypes = map[string]bool{"flat": true, "hourly": true, "mixed": true, "contingency": true, "pro_bono": true}

// feeStatusNext is each status's allowed next statuses. Void is final: a voided agreement
// is kept as it was, and the matter gets a new one.
var feeStatusNext = map[string]map[string]bool{
	"draft":  {"draft": true, "sent": true, "signed": true, "void": true},
	"sent":   {"sent": true, "signed": true, "void": true},
	"signed": {"signed": true, "void": true},
	"void":   {"void": true},
}

// SignedCopies checks a fee agreement's signed copy: a file p may read, not trashed, on
// the agreement's matter. FileLink provides it.
type SignedCopies interface {
	CheckMatterFile(ctx context.Context, p domain.Principal, matter, file domain.ID) error
}

func (s *Service) feeAccess(ctx context.Context, p domain.Principal, matter domain.ID, act domain.Action) error {
	if !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) && !p.HasRole(domain.RoleStaff) {
		return domain.Forbidden(domain.ReasonRole)
	}
	return s.allow(ctx, p, domain.ResMatter, act, matter)
}

// FeeAgreements lists the agreements p may read, newest first, filtered by matter and
// status when set.
func (s *Service) FeeAgreements(ctx context.Context, p domain.Principal, matter domain.ID, status string) ([]FeeAgreement, error) {
	if err := s.feeAccess(ctx, p, matter, domain.ActionRead); err != nil {
		return nil, err
	}
	rows, err := listOf[FeeAgreement](ctx, s.d.Store, kindFeeAgreement)
	if err != nil {
		return nil, err
	}
	out := []FeeAgreement{}
	for _, r := range rows {
		if (!matter.IsZero() && r.MatterID != matter) || (status != "" && r.Status != status) {
			continue
		}
		if err := s.feeAccess(ctx, p, r.MatterID, domain.ActionRead); err != nil {
			if access.IsNotFound(err) || access.IsForbidden(err) {
				continue
			}
			return nil, err
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// FeeAgreement returns one agreement p may read; one behind a wall is not found.
func (s *Service) FeeAgreement(ctx context.Context, p domain.Principal, id domain.ID) (FeeAgreement, error) {
	row, err := s.feeAgreement(ctx, id)
	if err != nil {
		return FeeAgreement{}, err
	}
	if err := s.feeAccess(ctx, p, row.MatterID, domain.ActionRead); err != nil {
		return FeeAgreement{}, err
	}
	return row, nil
}

func (s *Service) feeAgreement(ctx context.Context, id domain.ID) (FeeAgreement, error) {
	var row FeeAgreement
	if err := s.d.Store.Get(ctx, kindFeeAgreement, string(id), &row); err != nil {
		if errors.Is(err, ErrNotFound) {
			return FeeAgreement{}, domain.NotFound("fee_agreement")
		}
		return FeeAgreement{}, err
	}
	return row, nil
}

// CreateFeeAgreements adds agreements in one transaction (one for a single create, the
// whole batch for :bulk). Every item is checked first; any failure writes none.
func (s *Service) CreateFeeAgreements(ctx context.Context, p domain.Principal, items []FeeAgreementInput) ([]FeeAgreement, error) {
	if len(items) == 0 {
		return nil, domain.Invalid("items", "needs at least one agreement")
	}
	if s.overBulkMax(ctx, len(items)) {
		return nil, domain.Invalid("items", "batch is larger than api.bulk_batch_max")
	}
	var out []FeeAgreement
	err := s.write(ctx, p, func(t *txn) error {
		active := map[domain.ID]bool{}
		for i, in := range items {
			if in.MatterID == nil || in.MatterID.IsZero() || in.FeeType == nil || in.Services == nil {
				return itemInvalid(len(items), i, domain.Invalid("matter_id", "matter_id, fee_type and services_description are required"))
			}
			row := FeeAgreement{MatterID: *in.MatterID, Status: "draft"}
			if err := s.feeAccess(t.ctx, p, row.MatterID, domain.ActionWrite); err != nil {
				return itemInvalid(len(items), i, err)
			}
			if err := s.applyFeeAgreement(t.ctx, p, &row, in); err != nil {
				return itemInvalid(len(items), i, err)
			}
			if row.Status != "void" {
				busy, err := s.activeFeeAgreement(t.ctx, row.MatterID, "")
				if err != nil {
					return err
				}
				if busy || active[row.MatterID] {
					return itemInvalid(len(items), i, domain.Conflict("the matter already has an active fee agreement; void it first"))
				}
				active[row.MatterID] = true
			}
			meta, err := s.meta(p)
			if err != nil {
				return err
			}
			row.Meta = meta
			if err := t.put(kindFeeAgreement, row.ID, row); err != nil {
				return err
			}
			if err := t.created("fee_agreement", row.ID, row, ""); err != nil {
				return err
			}
			if err := t.event("fee_agreement.created", "fee_agreement", row.ID, row.MatterID, "Fee agreement added ("+row.FeeType+")", false); err != nil {
				return err
			}
			out = append(out, row)
		}
		return nil
	})
	return out, err
}

// UpdateFeeAgreement patches one agreement. The matter can't change; status moves
// forward only (draft, sent, signed), any status can be voided with a reason, and void is
// final. Every changed field goes to the audit log with its old and new value.
func (s *Service) UpdateFeeAgreement(ctx context.Context, p domain.Principal, id domain.ID, in FeeAgreementInput) (FeeAgreement, error) {
	var out FeeAgreement
	err := s.write(ctx, p, func(t *txn) error {
		before, err := s.FeeAgreement(t.ctx, p, id)
		if err != nil {
			return err
		}
		if err := s.feeAccess(t.ctx, p, before.MatterID, domain.ActionWrite); err != nil {
			return err
		}
		if in.MatterID != nil && *in.MatterID != before.MatterID {
			return domain.Invalid("matter_id", "a fee agreement stays on its matter")
		}
		after := before
		if err := s.applyFeeAgreement(t.ctx, p, &after, in); err != nil {
			return err
		}
		if !feeStatusNext[before.Status][after.Status] {
			return domain.Invalid("status", "a "+before.Status+" agreement can't become "+after.Status)
		}
		after.UpdatedAt = s.now()
		if err := t.put(kindFeeAgreement, id, after); err != nil {
			return err
		}
		if err := t.diff("fee_agreement", id, before, after, after.VoidReason); err != nil {
			return err
		}
		out = after
		return t.event("fee_agreement.updated", "fee_agreement", id, after.MatterID, "Fee agreement "+after.Status, false)
	})
	return out, err
}

// FeeAgreementHistory is the agreement's audit rows, newest first, after the read check.
func (s *Service) FeeAgreementHistory(ctx context.Context, p domain.Principal, id domain.ID) ([]domain.AuditEntry, error) {
	if _, err := s.FeeAgreement(ctx, p, id); err != nil {
		return nil, err
	}
	rows, err := s.d.Audit.ForEntity(ctx, "fee_agreement", id)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AuditEntry, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		out = append(out, rows[i])
	}
	return out, nil
}

func (s *Service) activeFeeAgreement(ctx context.Context, matter, except domain.ID) (bool, error) {
	rows, err := listOf[FeeAgreement](ctx, s.d.Store, kindFeeAgreement)
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if r.MatterID == matter && r.ID != except && r.Status != "void" {
			return true, nil
		}
	}
	return false, nil
}

func (s *Service) applyFeeAgreement(ctx context.Context, p domain.Principal, row *FeeAgreement, in FeeAgreementInput) error {
	if in.FeeType != nil {
		if !feeTypes[*in.FeeType] {
			return domain.Invalid("fee_type", "must be flat, hourly, mixed, contingency or pro_bono")
		}
		row.FeeType = *in.FeeType
	}
	if in.AmountCents != nil {
		if *in.AmountCents != nil && negative(int64(**in.AmountCents)) {
			return domain.Invalid("amount_cents", "must be zero or greater")
		}
		row.AmountCents = *in.AmountCents
	}
	if in.Services != nil {
		v := strings.TrimSpace(*in.Services)
		if v == "" {
			return domain.Invalid("services_description", "is required")
		}
		row.Services = v
	}
	if in.Status != nil {
		if _, ok := feeStatusNext[*in.Status]; !ok {
			return domain.Invalid("status", "must be draft, sent, signed or void")
		}
		row.Status = *in.Status
	}
	if in.SignedOn != nil {
		row.SignedOn = ""
		if *in.SignedOn != nil {
			if _, err := time.Parse("2006-01-02", **in.SignedOn); err != nil {
				return domain.Invalid("signed_on", "must be a date, YYYY-MM-DD")
			}
			row.SignedOn = **in.SignedOn
		}
	}
	if in.VoidReason != nil {
		row.VoidReason = strings.TrimSpace(*in.VoidReason)
	}
	if in.SignedFileID != nil {
		row.SignedFileID = ""
		if *in.SignedFileID != nil && !(**in.SignedFileID).IsZero() {
			files, ok := s.d.Receipts.(SignedCopies)
			if !ok {
				return domain.Invalid("signed_file_id", "the file store is not available")
			}
			if err := files.CheckMatterFile(ctx, p, row.MatterID, **in.SignedFileID); err != nil {
				return domain.Invalid("signed_file_id", "must be a file on this matter you can read")
			}
			row.SignedFileID = **in.SignedFileID
		}
	}
	if row.Status == "void" && row.VoidReason == "" {
		return domain.Invalid("void_reason", "a void agreement needs a reason")
	}
	if row.Status == "signed" && row.SignedOn == "" {
		return domain.Invalid("signed_on", "a signed agreement needs its signed date")
	}
	if row.FeeType == "flat" && row.AmountCents == nil {
		return domain.Invalid("amount_cents", "a flat fee agreement needs its amount")
	}
	return nil
}

// itemInvalid names the failing item of a batch of more than one.
func itemInvalid(n, i int, err error) error {
	var api *domain.APIError
	if n < 2 || !errors.As(err, &api) {
		return err
	}
	out := *api
	out.Message = "items[" + strconv.Itoa(i) + "]: " + out.Message
	return &out
}

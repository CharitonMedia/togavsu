package billing

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// scopes checks need on a bearer token; identity's scope_denied is a 403.
func (h *Handler) scopes(r *http.Request, need ...string) error {
	if h.Scopes == nil || len(need) == 0 {
		return nil
	}
	err := h.Scopes(r, need...)
	var api *domain.APIError
	if errors.As(err, &api) && api.Code == "scope_denied" {
		return domain.Forbidden(api.Message)
	}
	return err
}

// processorScopes is a granted processor call's token check: a trust deposit that
// completes needs trust:write on the module token too.
func (h *Handler) processorScopes(r *http.Request, p domain.Principal, grant string, completing bool) error {
	dest, err := h.Svc.ProcessorGrantDestination(r.Context(), p, grant)
	if err != nil {
		return err
	}
	if dest == DestTrust && completing {
		return h.scopes(r, "trust:write")
	}
	return nil
}

type invoiceImportBody struct {
	MatterID domain.ID `json:"matter_id"`
	Number   string    `json:"number"`
	IssuedOn string    `json:"issued_on"`
	Status   string    `json:"status"`
	Lines    []struct {
		Kind        string        `json:"kind"`
		SourceID    *domain.ID    `json:"source_id"`
		Description string        `json:"description"`
		AmountCents *domain.Cents `json:"amount_cents"`
		FeeItemID   *domain.ID    `json:"fee_item_id"`
	} `json:"lines"`
	SubtotalCents    *domain.Cents `json:"subtotal_cents"`
	AdjustmentCents  *domain.Cents `json:"adjustment_cents"`
	AdjustmentReason string        `json:"adjustment_reason"`
	InterestCents    *domain.Cents `json:"interest_cents"`
	TotalCents       *domain.Cents `json:"total_cents"`
	AmountPaidCents  *domain.Cents `json:"amount_paid_cents"`
	BalanceDueCents  *domain.Cents `json:"balance_due_cents"`
	WriteOffCents    *domain.Cents `json:"write_off_cents"`
	WriteOffReason   string        `json:"write_off_reason"`
	VoidReason       string        `json:"void_reason"`
	DisputeStatus    string        `json:"dispute_status"`
	DisputeNotes     string        `json:"dispute_notes"`
	ClientMessage    string        `json:"client_message"`
	InternalNotes    string        `json:"internal_notes"`
}

func (b invoiceImportBody) input(i int) (InvoiceImport, error) {
	in := InvoiceImport{MatterID: b.MatterID, Number: b.Number, IssuedOn: b.IssuedOn, Status: b.Status,
		AdjustmentReason: b.AdjustmentReason, WriteOffReason: b.WriteOffReason, VoidReason: b.VoidReason,
		DisputeStatus: b.DisputeStatus, DisputeNotes: b.DisputeNotes, ClientMessage: b.ClientMessage, InternalNotes: b.InternalNotes}
	for _, f := range []struct {
		name string
		src  *domain.Cents
		dst  *domain.Cents
	}{
		{"subtotal_cents", b.SubtotalCents, &in.SubtotalCents}, {"adjustment_cents", b.AdjustmentCents, &in.AdjustmentCents},
		{"interest_cents", b.InterestCents, &in.InterestCents}, {"total_cents", b.TotalCents, &in.TotalCents},
		{"amount_paid_cents", b.AmountPaidCents, &in.AmountPaidCents}, {"balance_due_cents", b.BalanceDueCents, &in.BalanceDueCents},
		{"write_off_cents", b.WriteOffCents, &in.WriteOffCents},
	} {
		if f.src == nil {
			return InvoiceImport{}, domain.Invalid(fmt.Sprintf("items/%d/%s", i, f.name), "is required")
		}
		*f.dst = *f.src
	}
	for _, l := range b.Lines {
		li := LineInput{Kind: l.Kind, Description: l.Description, AmountCents: l.AmountCents}
		if l.SourceID != nil {
			li.SourceID = *l.SourceID
		}
		if l.FeeItemID != nil {
			li.FeeItemID = *l.FeeItemID
		}
		in.Lines = append(in.Lines, li)
	}
	return in, nil
}

// bulkInvoices is bulkInvoice. The token needs each item's own scopes (ImportScopes) before
// the service checks roles, walls and reconciliation; one failure writes nothing.
func (h *Handler) bulkInvoices(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		Items []invoiceImportBody `json:"items"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		items := make([]InvoiceImport, 0, len(body.Items))
		seen := map[string]bool{}
		var need []string
		for i, b := range body.Items {
			in, err := b.input(i)
			if err != nil {
				return 0, nil, err
			}
			for _, sc := range ImportScopes(in) {
				if !seen[sc] {
					seen[sc] = true
					need = append(need, sc)
				}
			}
			items = append(items, in)
		}
		if err := h.scopes(r, need...); err != nil {
			return 0, nil, err
		}
		rows, err := h.Svc.BulkInvoices(r.Context(), p, items)
		client := ClientOnly(p)
		return http.StatusCreated, map[string]any{"data": anys(rows, func(i Invoice) any { return invoiceView(forReader(p, i), client) })}, err
	})
}

// initiateProcessor is initiateProcessorPayment. A trust destination needs trust:write on
// the caller's token as well as payments:write.
func (h *Handler) initiateProcessor(w http.ResponseWriter, r *http.Request, p domain.Principal) {
	var body struct {
		ProcessorModuleID string        `json:"processor_module_id"`
		ContactID         domain.ID     `json:"contact_id"`
		MatterID          domain.ID     `json:"matter_id"`
		AmountCents       *domain.Cents `json:"amount_cents"`
		Destination       string        `json:"destination"`
		TrustAccountID    *domain.ID    `json:"trust_account_id"`
		InvoiceID         *domain.ID    `json:"invoice_id"`
	}
	h.write(w, r, p, &body, func() (int, any, error) {
		if body.AmountCents == nil {
			return 0, nil, domain.Invalid("amount_cents", "is required")
		}
		in := ProcessorInput{ProcessorModuleID: body.ProcessorModuleID, ContactID: body.ContactID, MatterID: body.MatterID,
			AmountCents: *body.AmountCents, Destination: body.Destination}
		if body.TrustAccountID != nil {
			in.TrustAccountID = *body.TrustAccountID
		}
		if body.InvoiceID != nil {
			in.InvoiceID = *body.InvoiceID
		}
		if in.Destination == DestTrust {
			if err := h.scopes(r, "trust:write"); err != nil {
				return 0, nil, err
			}
		}
		row, err := h.Svc.InitiateProcessorPayment(r.Context(), p, in)
		return http.StatusCreated, ProcessorInitView(row), err
	})
}

package modules

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// ProcessorPaymentRecord binds an approved intent to a newly created matter and
// its paying contact, or just a newly created contact. Money fields come only
// from the stored receipt, never from this request.
type ProcessorPaymentRecord struct {
	IntentID  string    `json:"intent_id"`
	MatterID  domain.ID `json:"matter_id,omitempty"`
	ContactID domain.ID `json:"contact_id"`
}

func (s *Service) recordProcessorPayment(ctx context.Context, p domain.Principal, body []byte) (int, http.Header, []byte, error) {
	if !p.UserID.IsZero() || p.ModuleID == "" {
		return 0, nil, nil, domain.Forbidden("receipt recording requires the consuming module service principal")
	}
	var in ProcessorPaymentRecord
	if err := strictProcessorJSON(body, &in); err != nil {
		return 0, nil, nil, err
	}
	if !processorIntentPattern.MatchString(in.IntentID) || in.ContactID.IsZero() {
		return 0, nil, nil, domain.Invalid("body", "intent_id and contact_id are required; matter_id is optional")
	}
	if s.d.RecordProcessorPayment == nil {
		return 0, nil, nil, domain.Forbidden("processor recording unavailable")
	}
	var answer []byte
	err := s.commit(ctx, func(ctx context.Context) error {
		var raw string
		var claimedAt int64
		err := s.q(ctx).QueryRowContext(ctx, `SELECT response,created_at FROM core.core_idempotency WHERE actor=$1 AND operation=$2 AND idem_key=$3 FOR UPDATE`, "module-processor/"+p.ModuleID, ProcessorPaymentContract, in.IntentID).Scan(&raw, &claimedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NotFound("processor intent")
		}
		if err != nil {
			return err
		}
		var receipt processorContractReceipt
		if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
			return err
		}
		if _, err := s.processorContractProvider(ctx, p.ModuleID, receipt.ProviderID); err != nil {
			return err
		}
		if receipt.Result.Status != "approved" || receipt.Result.Destination != "operating" || !validProcessorResult(receipt.Result, receipt.Result) {
			return domain.Conflict("only an approved operating receipt can be recorded")
		}
		if receipt.Binding != nil {
			if *receipt.Binding != in {
				return domain.Conflict("intent_id is already bound to a different matter or contact")
			}
			if len(receipt.Payment) == 0 {
				return domain.Conflict("recorded receipt is incomplete")
			}
			answer, err = s.d.RecordProcessorPayment(ctx, p, in, receipt.ProviderID, receipt.Result, receipt.Payment)
			return err
		}
		// Practice's append-only event authenticates who created the target, even
		// though legacy record created_by uses the system id for viewerless writes.
		target, kind := in.MatterID, "matter"
		if target.IsZero() {
			target, kind = in.ContactID, "contact"
		}
		approvedAt := receipt.ApprovedAt
		if approvedAt == 0 {
			// 2f receipts have no timestamp field, but their approval transition
			// is already in the append-only ledger. Intent creation is too early:
			// a consumer must not bind a target it created while still pending.
			rows, err := s.d.Audit.Rows(ctx, audit.RowQuery{Entity: "module_processor_intent", EntityID: domain.ID("module-processor/" + p.ModuleID + "/" + in.IntentID), Field: "status"})
			if err != nil {
				return err
			}
			for _, row := range rows {
				at := row.CreatedAt.UnixMicro()
				if row.NewValue == "approved" && at >= claimedAt && (approvedAt == 0 || at < approvedAt) {
					approvedAt = at
				}
			}
			if approvedAt == 0 {
				return domain.Conflict("the stored receipt has no approval audit evidence")
			}
		}
		var owned bool
		if err := s.q(ctx).QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM core.events WHERE record_id=$1 AND record_type=$2 AND action=$3 AND actor_id=$4 AND created_at >= $5)`, string(target), kind, kind+".created", p.ModuleID, approvedAt).Scan(&owned); err != nil {
			return err
		}
		if !owned {
			return domain.Forbidden("bind the receipt to a record this consumer created after approval")
		}
		payment, err := s.d.RecordProcessorPayment(ctx, p, in, receipt.ProviderID, receipt.Result, nil)
		if err != nil {
			return err
		}
		receipt.Binding = &in
		receipt.Payment = payment
		encoded, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		_, err = s.q(ctx).ExecContext(ctx, `UPDATE core.core_idempotency SET response=$4 WHERE actor=$1 AND operation=$2 AND idem_key=$3`, "module-processor/"+p.ModuleID, ProcessorPaymentContract, in.IntentID, string(encoded))
		if err != nil {
			return err
		}
		answer = payment
		return s.auditProcessorReceipt(ctx, p, receipt.Result)
	})
	if err != nil {
		return 0, nil, nil, err
	}
	return http.StatusOK, http.Header{"Content-Type": {"application/json"}}, answer, nil
}

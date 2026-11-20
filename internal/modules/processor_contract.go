package modules

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// ProcessorPaymentContract authorizes an operating card payment before an intake
// matter exists. The generic broker remains unchanged for every other contract.
const ProcessorPaymentContract = "processor.payment.v1"

var processorIntentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)

// ProcessorPaymentWrite is the authorize operation. The token is a processor-hosted
// field token, never card data; only its request digest is kept by the core.
type ProcessorPaymentWrite struct {
	IntentID     string       `json:"intent_id"`
	AmountCents  domain.Cents `json:"amount_cents"`
	Destination  string       `json:"destination"`
	PaymentToken string       `json:"payment_token"`
}

// ProcessorPaymentResult is the durable, bounded result returned to the consumer.
// Pending forbids creating a matter as part of this paid intake attempt.
type ProcessorPaymentResult struct {
	IntentID        string       `json:"intent_id"`
	Status          string       `json:"status"`
	AmountCents     domain.Cents `json:"amount_cents"`
	Destination     string       `json:"destination"`
	ReferenceNumber string       `json:"reference_number,omitempty"`
	DeclineCode     string       `json:"decline_code,omitempty"`
}

type processorContractReceipt struct {
	ProviderID string                 `json:"provider_id"`
	Result     ProcessorPaymentResult `json:"result"`
}

func strictProcessorJSON(body []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return domain.Invalid("body", "use the processor.payment.v1 request schema")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return domain.Invalid("body", "send one JSON object")
	}
	return nil
}

// callProcessorPayment binds one intent to its original consumer, amount,
// destination and provider. A committed pending marker precedes the first provider
// request. An ambiguous attempt is only recovered, never charged again. Receipts
// have no retention expiry: reusing an old intake id cannot authorize new money.
func (s *Service) callProcessorPayment(ctx context.Context, p domain.Principal, operation string, body []byte, contextToken, prefer string) (int, http.Header, []byte, error) {
	if p.ModuleID == "" || (p.Source != domain.SourceModule && p.Source != domain.SourceService) {
		return 0, nil, nil, domain.Forbidden("only a module token may call a contract")
	}
	var in ProcessorPaymentWrite
	switch operation {
	case "authorize":
		if err := strictProcessorJSON(body, &in); err != nil {
			return 0, nil, nil, err
		}
		if in.AmountCents <= 0 || in.Destination != "operating" || strings.TrimSpace(in.PaymentToken) == "" {
			return 0, nil, nil, domain.Invalid("body", "authorize needs a positive amount_cents, operating destination and hosted payment_token")
		}
	case "recover":
		var recover struct {
			IntentID string `json:"intent_id"`
		}
		if err := strictProcessorJSON(body, &recover); err != nil {
			return 0, nil, nil, err
		}
		in.IntentID = recover.IntentID
	default:
		return 0, nil, nil, domain.NotFound("operation")
	}
	if !processorIntentPattern.MatchString(in.IntentID) {
		return 0, nil, nil, domain.Invalid("intent_id", "use 1 to 200 letters, digits, dots, underscores, colons or hyphens, starting with a letter or digit")
	}
	maxBytes, err := s.intSetting(ctx, contractCallMaxBytesKey)
	if err != nil {
		return 0, nil, nil, err
	}
	if maxBytes <= 0 {
		maxBytes = domain.ContractCallMaxBytes
	}
	if len(body) > maxBytes {
		return 0, nil, nil, &domain.APIError{Code: "payload_too_large", Message: "request body exceeds modules.contract_call_max_bytes"}
	}

	actor := "module-processor/" + p.ModuleID
	canonical, err := json.Marshal(in)
	if err != nil {
		return 0, nil, nil, err
	}
	sum := sha256.Sum256(canonical)
	requestHash := hex.EncodeToString(sum[:])
	var receipt processorContractReceipt
	var provider string
	var known bool
	for {
		var hash string
		hash, receipt, known, err = s.loadProcessorReceipt(ctx, actor, in.IntentID)
		if err != nil {
			return 0, nil, nil, err
		}
		if known {
			if operation == "authorize" && hash != requestHash {
				return 0, nil, nil, &domain.APIError{Code: domain.CodeIdempotencyConflict, Message: "intent_id already names a different processor authorization"}
			}
			if prefer != "" && prefer != receipt.ProviderID {
				return 0, nil, nil, domain.Conflict("intent_id is bound to another processor")
			}
			prefer = receipt.ProviderID
		}
		provider, err = s.processorContractProvider(ctx, p.ModuleID, prefer)
		if err != nil {
			return 0, nil, nil, err
		}
		if known && receipt.Result.Status != "pending" {
			return s.processorContractAnswer(ctx, p, operation, receipt.Result)
		}
		if known {
			break
		}
		if operation == "recover" {
			return 0, nil, nil, domain.NotFound("processor intent")
		}
		receipt = processorContractReceipt{ProviderID: provider, Result: ProcessorPaymentResult{IntentID: in.IntentID, Status: "pending", AmountCents: in.AmountCents, Destination: in.Destination}}
		claimed, err := s.claimProcessorReceipt(ctx, p, actor, requestHash, receipt)
		if err != nil {
			return 0, nil, nil, err
		}
		if claimed {
			break // Only the unique durable claim winner may authorize.
		}
		// A concurrent caller claimed it first. Re-read and bind its original
		// provider and request digest; the loser can only recover that intent.
	}
	providerOperation, providerBody := "authorize", canonical
	if known {
		providerOperation = "recover"
		providerBody, _ = json.Marshal(map[string]string{"intent_id": in.IntentID})
	}
	status, _, response, err := s.callContract(ctx, p, ProcessorPaymentContract, providerOperation, providerBody, contextToken, provider)
	if err != nil {
		return 0, nil, nil, err
	}
	// An unknown, failed, or malformed answer cannot undo an external approval.
	// Keep the marker pending and require a later recovery; never authorize again.
	if status != http.StatusOK && status != http.StatusAccepted {
		return s.processorContractAnswer(ctx, p, operation, receipt.Result)
	}
	var result ProcessorPaymentResult
	if err := strictProcessorJSON(response, &result); err != nil || !validProcessorResult(result, receipt.Result) {
		return 0, nil, nil, domain.Conflict("processor returned an invalid result; recover this pending intent")
	}
	receipt.Result = result
	receipt, err = s.completeProcessorReceipt(ctx, p, actor, receipt)
	if err != nil {
		return 0, nil, nil, err
	}
	return s.processorContractAnswer(ctx, p, operation, receipt.Result)
}

func validProcessorResult(got, bound ProcessorPaymentResult) bool {
	if got.IntentID != bound.IntentID || got.AmountCents != bound.AmountCents || got.Destination != bound.Destination {
		return false
	}
	switch got.Status {
	case "approved":
		return strings.TrimSpace(got.ReferenceNumber) != "" && len(got.ReferenceNumber) <= 200 && got.DeclineCode == ""
	case "declined":
		return got.ReferenceNumber == "" && (got.DeclineCode == "" || processorIntentPattern.MatchString(got.DeclineCode))
	case "pending":
		return got.ReferenceNumber == "" && got.DeclineCode == ""
	}
	return false
}

func (s *Service) processorContractProvider(ctx context.Context, consumerID, prefer string) (string, error) {
	consumer, err := s.Installed(ctx, consumerID)
	if err != nil || !consumer.Enabled {
		return "", domain.NotFound("module")
	}
	if !consumer.Approved["payments:write"] {
		return "", scopeDenied("payments:write")
	}
	if !consumer.Approved["contracts:call"] || !consumer.Manifest.ConsumesContract(ProcessorPaymentContract) {
		return "", scopeDenied("contracts:call")
	}
	providerID, err := s.linkedProvider(ctx, ProcessorPaymentContract, consumerID, prefer)
	if err != nil || providerID == "" {
		return "", &domain.APIError{Code: "contract_unlinked", Message: "no linked processor is available"}
	}
	provider, err := s.Installed(ctx, providerID)
	if err != nil || !provider.Enabled {
		return "", &domain.APIError{Code: "contract_unlinked", Message: "the linked processor is unavailable"}
	}
	if !provider.Approved["payments:write"] {
		return "", scopeDenied("payments:write")
	}
	if !provider.Approved["contracts:provide"] {
		return "", scopeDenied("contracts:provide")
	}
	for _, operation := range []string{"authorize", "recover"} {
		if _, ok := provider.Manifest.ProvideOperation(ProcessorPaymentContract, operation); !ok {
			return "", domain.Invalid("provider_id", "processor.payment.v1 requires authorize and recover operations")
		}
	}
	if provider.Manifest.PaymentProcessor == nil || !contains(provider.Manifest.PaymentProcessor.Destinations, "operating") {
		return "", domain.Invalid("provider_id", "the provider must declare payment_processor for operating")
	}
	return providerID, nil
}

func (s *Service) loadProcessorReceipt(ctx context.Context, actor, intent string) (string, processorContractReceipt, bool, error) {
	var hash, raw string
	err := s.q(ctx).QueryRowContext(ctx, `SELECT request_hash, response FROM core.core_idempotency WHERE actor=$1 AND operation=$2 AND idem_key=$3`, actor, ProcessorPaymentContract, intent).Scan(&hash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", processorContractReceipt{}, false, nil
	}
	if err != nil {
		return "", processorContractReceipt{}, false, err
	}
	var receipt processorContractReceipt
	err = json.Unmarshal([]byte(raw), &receipt)
	return hash, receipt, true, err
}

// claimProcessorReceipt commits a pending marker before any outside charge. The
// unique key, rather than a network-spanning lock, elects one authorization caller.
func (s *Service) claimProcessorReceipt(ctx context.Context, p domain.Principal, actor, hash string, receipt processorContractReceipt) (bool, error) {
	payload, err := json.Marshal(receipt)
	if err != nil {
		return false, err
	}
	claimed := false
	err = s.commit(ctx, func(ctx context.Context) error {
		result, err := s.q(ctx).ExecContext(ctx, `INSERT INTO core.core_idempotency (actor,operation,idem_key,request_hash,status,response,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (actor,operation,idem_key) DO NOTHING`, actor, ProcessorPaymentContract, receipt.Result.IntentID, hash, http.StatusAccepted, string(payload), s.now().UnixMicro())
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil || n == 0 {
			return err
		}
		claimed = true
		return s.auditProcessorReceipt(ctx, p, receipt.Result)
	})
	return claimed, err
}

// completeProcessorReceipt changes only a still-pending result. Concurrent recovery
// may observe pending while the initial charge is in flight; it must never replace
// a terminal approval or decline with that stale pending response.
func (s *Service) completeProcessorReceipt(ctx context.Context, p domain.Principal, actor string, receipt processorContractReceipt) (processorContractReceipt, error) {
	payload, err := json.Marshal(receipt)
	if err != nil {
		return processorContractReceipt{}, err
	}
	err = s.commit(ctx, func(ctx context.Context) error {
		result, err := s.q(ctx).ExecContext(ctx, `UPDATE core.core_idempotency SET status=$4,response=$5 WHERE actor=$1 AND operation=$2 AND idem_key=$3 AND status=$6`, actor, ProcessorPaymentContract, receipt.Result.IntentID, processorResultStatus(receipt.Result), string(payload), http.StatusAccepted)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			_, receipt, _, err = s.loadProcessorReceipt(ctx, actor, receipt.Result.IntentID)
			return err
		}
		return s.auditProcessorReceipt(ctx, p, receipt.Result)
	})
	return receipt, err
}

func (s *Service) auditProcessorReceipt(ctx context.Context, p domain.Principal, result ProcessorPaymentResult) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	_, err = s.d.Audit.Record(ctx, p, audit.Change{Entity: "module_processor_intent", EntityID: id, Field: "status", New: result.Status, Reason: "processor.payment.v1"})
	return err
}

func processorResultStatus(result ProcessorPaymentResult) int {
	if result.Status == "pending" {
		return http.StatusAccepted
	}
	return http.StatusOK
}

func (s *Service) processorContractAnswer(ctx context.Context, p domain.Principal, operation string, result ProcessorPaymentResult) (int, http.Header, []byte, error) {
	if err := s.auditContractCall(ctx, p, ProcessorPaymentContract, operation, p.ModuleID, "", processorResultStatus(result), result.Status); err != nil {
		return 0, nil, nil, err
	}
	payload, err := json.Marshal(result)
	return processorResultStatus(result), http.Header{"Content-Type": {"application/json"}}, payload, err
}

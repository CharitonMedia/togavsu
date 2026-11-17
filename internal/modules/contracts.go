package modules

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

const contractCallMaxBytesKey = "modules.contract_call_max_bytes"

// ContractLink is an admin-approved consumer→provider link for one contract (CORE-31).
type ContractLink struct {
	ID         string `json:"id"`
	Contract   string `json:"contract"`
	ConsumerID string `json:"consumer_id"`
	ProviderID string `json:"provider_id"`
	CreatedAt  string `json:"created_at"`
}

// ContractLinkWrite is the admin link request body.
type ContractLinkWrite struct {
	Contract   string `json:"contract"`
	ConsumerID string `json:"consumer_id"`
	ProviderID string `json:"provider_id"`
}

// ContractEventWrite is a provider-pushed contract event (CORE-31).
type ContractEventWrite struct {
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}

// ListContractLinks returns every approved contract link (admin).
func (s *Service) ListContractLinks(ctx context.Context, p domain.Principal) ([]ContractLink, error) {
	if err := requireAdmin(p); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT id, contract, consumer_id, provider_id, created_at FROM core.module_contract_links ORDER BY contract, consumer_id, provider_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContractLink{}
	for rows.Next() {
		var l ContractLink
		var at int64
		if err := rows.Scan(&l.ID, &l.Contract, &l.ConsumerID, &l.ProviderID, &at); err != nil {
			return nil, err
		}
		l.CreatedAt = time.UnixMicro(at).UTC().Format(time.RFC3339Nano)
		out = append(out, l)
	}
	return out, rows.Err()
}

// LinkContract records an admin-approved consumer↔provider link and emits module.contract_linked.
func (s *Service) LinkContract(ctx context.Context, p domain.Principal, in ContractLinkWrite) (ContractLink, error) {
	if err := requireAdmin(p); err != nil {
		return ContractLink{}, err
	}
	in.Contract = strings.TrimSpace(in.Contract)
	in.ConsumerID = strings.TrimSpace(in.ConsumerID)
	in.ProviderID = strings.TrimSpace(in.ProviderID)
	if in.Contract == "" || in.ConsumerID == "" || in.ProviderID == "" {
		return ContractLink{}, domain.Invalid("contract", "contract, consumer_id and provider_id are required")
	}
	if in.ConsumerID == in.ProviderID {
		return ContractLink{}, domain.Invalid("provider_id", "a module cannot link to itself")
	}
	consumer, err := s.Installed(ctx, in.ConsumerID)
	if err != nil || !consumer.Enabled {
		return ContractLink{}, domain.NotFound("consumer")
	}
	provider, err := s.Installed(ctx, in.ProviderID)
	if err != nil || !provider.Enabled {
		return ContractLink{}, domain.NotFound("provider")
	}
	if !consumer.Manifest.ConsumesContract(in.Contract) {
		return ContractLink{}, domain.Invalid("consumer_id", "consumer does not declare consumes for "+in.Contract)
	}
	offer, ok := provider.Manifest.Provide(in.Contract)
	if !ok {
		return ContractLink{}, domain.Invalid("provider_id", "provider does not declare provides for "+in.Contract)
	}
	if !offer.Many {
		var existing string
		err := s.q(ctx).QueryRowContext(ctx, `SELECT provider_id FROM core.module_contract_links WHERE contract = $1 LIMIT 1`, in.Contract).Scan(&existing)
		if err == nil && existing != in.ProviderID {
			return ContractLink{}, domain.Conflict("contract " + in.Contract + " already has provider " + existing)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return ContractLink{}, err
		}
	}
	id, err := domain.NewID()
	if err != nil {
		return ContractLink{}, err
	}
	now := s.now().UnixMicro()
	actor := string(p.UserID)
	var out ContractLink
	err = s.commit(ctx, func(ctx context.Context) error {
		var existing string
		err := s.q(ctx).QueryRowContext(ctx, `SELECT id FROM core.module_contract_links WHERE contract = $1 AND consumer_id = $2 AND provider_id = $3`,
			in.Contract, in.ConsumerID, in.ProviderID).Scan(&existing)
		if err == nil {
			out.ID = existing
			out.Contract, out.ConsumerID, out.ProviderID = in.Contract, in.ConsumerID, in.ProviderID
			out.CreatedAt = time.UnixMicro(now).UTC().Format(time.RFC3339Nano)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_contract_links (id, created_at, updated_at, created_by, contract, consumer_id, provider_id)
VALUES ($1,$2,$2,$3,$4,$5,$6)`, string(id), now, actor, in.Contract, in.ConsumerID, in.ProviderID); err != nil {
			return err
		}
		if _, err := s.d.Audit.Record(ctx, p, audit.Change{
			Entity: "module_contract_link", EntityID: id, Field: "link",
			New:    in.Contract + ":" + in.ConsumerID + "->" + in.ProviderID,
			Reason: "module.contract_linked",
		}); err != nil {
			return err
		}
		if err := s.event(ctx, p, "module.contract_linked", in.ConsumerID,
			"Linked "+in.Contract+" consumer="+in.ConsumerID+" provider="+in.ProviderID, ""); err != nil {
			return err
		}
		out = ContractLink{
			ID: string(id), Contract: in.Contract, ConsumerID: in.ConsumerID, ProviderID: in.ProviderID,
			CreatedAt: time.UnixMicro(now).UTC().Format(time.RFC3339Nano),
		}
		return nil
	})
	return out, err
}

// UnlinkContract removes a link and emits module.contract_unlinked. Further calls fail at once.
func (s *Service) UnlinkContract(ctx context.Context, p domain.Principal, linkID string) error {
	if err := requireAdmin(p); err != nil {
		return err
	}
	linkID = strings.TrimSpace(linkID)
	if linkID == "" {
		return domain.NotFound("contract_link")
	}
	return s.commit(ctx, func(ctx context.Context) error {
		var contract, consumer, provider string
		err := s.q(ctx).QueryRowContext(ctx, `SELECT contract, consumer_id, provider_id FROM core.module_contract_links WHERE id = $1`, linkID).
			Scan(&contract, &consumer, &provider)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NotFound("contract_link")
		}
		if err != nil {
			return err
		}
		if _, err := s.q(ctx).ExecContext(ctx, `DELETE FROM core.module_contract_links WHERE id = $1`, linkID); err != nil {
			return err
		}
		if _, err := s.d.Audit.Record(ctx, p, audit.Change{
			Entity: "module_contract_link", EntityID: domain.ID(linkID), Field: "link",
			Old:    contract + ":" + consumer + "->" + provider,
			Reason: "module.contract_unlinked",
		}); err != nil {
			return err
		}
		return s.event(ctx, p, "module.contract_unlinked", consumer,
			"Unlinked "+contract+" consumer="+consumer+" provider="+provider, "")
	})
}

// linkedProvider returns the provider id for a consumer on a contract, or empty when none.
func (s *Service) linkedProvider(ctx context.Context, contract, consumer string) (string, error) {
	var provider string
	err := s.q(ctx).QueryRowContext(ctx, `SELECT provider_id FROM core.module_contract_links WHERE contract = $1 AND consumer_id = $2 LIMIT 1`,
		contract, consumer).Scan(&provider)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return provider, err
}

// CallContract proxies a consumer's contract operation to the linked provider (CORE-31).
// The response never includes the provider's address. The body is HMAC-signed like events.
func (s *Service) CallContract(ctx context.Context, p domain.Principal, contract, operation string, body []byte, contextToken string) (status int, header http.Header, respBody []byte, err error) {
	if p.ModuleID == "" || (p.Source != domain.SourceModule && p.Source != domain.SourceService) {
		return 0, nil, nil, domain.Forbidden("only a module token may call a contract")
	}
	consumerID := p.ModuleID
	contract = strings.TrimSpace(contract)
	operation = strings.TrimSpace(operation)
	if contract == "" || operation == "" {
		return 0, nil, nil, domain.Invalid("contract", "contract and operation are required")
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
	consumer, err := s.Installed(ctx, consumerID)
	if err != nil || !consumer.Enabled {
		return 0, nil, nil, domain.NotFound("module")
	}
	if !consumer.Manifest.ConsumesContract(contract) {
		return 0, nil, nil, domain.Forbidden("module does not consume " + contract)
	}
	providerID, err := s.linkedProvider(ctx, contract, consumerID)
	if err != nil {
		return 0, nil, nil, err
	}
	if providerID == "" {
		return 0, nil, nil, &domain.APIError{Code: "contract_unlinked", Message: "no provider is linked for " + contract}
	}
	provider, err := s.Installed(ctx, providerID)
	if err != nil || !provider.Enabled {
		return 0, nil, nil, &domain.APIError{Code: "contract_unlinked", Message: "provider for " + contract + " is not available"}
	}
	op, ok := provider.Manifest.ProvideOperation(contract, operation)
	if !ok {
		return 0, nil, nil, domain.NotFound("operation")
	}
	if s.d.Runtime == nil {
		return 0, nil, nil, domain.Conflict("module runtime is not available")
	}
	row, err := s.row(ctx, providerID)
	if err != nil {
		return 0, nil, nil, err
	}
	secret, err := s.open(row.WebhookSecret)
	if err != nil {
		return 0, nil, nil, err
	}
	url := strings.TrimRight(s.d.Runtime.BaseURL(providerID), "/") + op.Path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(SignatureHeader, SignWebhook(secret, s.now(), body))
	req.Header.Set("X-Togavsu-Contract", contract)
	req.Header.Set("X-Togavsu-Contract-Operation", operation)
	req.Header.Set("X-Togavsu-Contract-Consumer", consumerID)
	if contextToken != "" {
		req.Header.Set(ContextHeader, contextToken)
	}
	resp, err := s.d.HTTP.Do(req)
	if err != nil {
		return 0, nil, nil, domain.Conflict("provider did not answer")
	}
	defer resp.Body.Close()
	respBody, err = io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return 0, nil, nil, err
	}
	if len(respBody) > maxBytes {
		return 0, nil, nil, &domain.APIError{Code: "payload_too_large", Message: "provider response exceeds modules.contract_call_max_bytes"}
	}
	// Strip any address-bearing headers so the consumer never learns the provider URL.
	outHeader := http.Header{}
	for _, k := range []string{"Content-Type", "X-Request-Id"} {
		if v := resp.Header.Get(k); v != "" {
			outHeader.Set(k, v)
		}
	}
	return resp.StatusCode, outHeader, respBody, nil
}

// PushContractEvent delivers a provider-pushed contract event to every linked consumer
// like a core event webhook (CORE-31). The payload never carries another module's address.
func (s *Service) PushContractEvent(ctx context.Context, p domain.Principal, contract string, in ContractEventWrite) error {
	if p.ModuleID == "" || (p.Source != domain.SourceModule && p.Source != domain.SourceService) {
		return domain.Forbidden("only a module token may push a contract event")
	}
	if !p.UserID.IsZero() {
		return domain.Forbidden("contract events ignore a context token")
	}
	contract = strings.TrimSpace(contract)
	in.Action = strings.TrimSpace(in.Action)
	if contract == "" || in.Action == "" {
		return domain.Invalid("action", "contract and action are required")
	}
	if !strings.HasPrefix(in.Action, contract+".") && in.Action != contract {
		// Allow actions scoped under the contract name.
		if !strings.Contains(in.Action, ".") {
			in.Action = contract + "." + in.Action
		}
	}
	provider, err := s.Installed(ctx, p.ModuleID)
	if err != nil || !provider.Enabled {
		return domain.NotFound("module")
	}
	if _, ok := provider.Manifest.Provide(contract); !ok {
		return domain.Forbidden("module does not provide " + contract)
	}
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT consumer_id FROM core.module_contract_links WHERE contract = $1 AND provider_id = $2`,
		contract, p.ModuleID)
	if err != nil {
		return err
	}
	var consumers []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		consumers = append(consumers, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(EventPayload{
		ID:         string(mustID()),
		Action:     in.Action,
		RecordType: "contract",
		RecordID:   contract,
		OccurredAt: s.now().UTC().Format(time.RFC3339Nano),
		Module:     p.ModuleID,
	})
	if err != nil {
		return err
	}
	if len(in.Payload) > 0 && string(in.Payload) != "null" {
		// Attach provider payload under "data" without leaking addresses.
		var wrap map[string]any
		if err := json.Unmarshal(payload, &wrap); err != nil {
			return err
		}
		var data any
		if err := json.Unmarshal(in.Payload, &data); err != nil {
			return domain.Invalid("payload", "payload must be JSON")
		}
		wrap["data"] = data
		payload, err = json.Marshal(wrap)
		if err != nil {
			return err
		}
	}
	now := s.now().UnixMicro()
	return s.commit(ctx, func(ctx context.Context) error {
		for _, consumer := range consumers {
			id, err := domain.NewID()
			if err != nil {
				return err
			}
			if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_deliveries (id, created_at, updated_at, created_by, module_id, event_id, action, payload, status, attempts, next_attempt_at, last_error, delivered_at, task_authorization)
VALUES ($1,$2,$2,$3,$4,$5,$6,$7,'pending',0,$2,'',0,'')`,
				string(id), now, p.ModuleID, consumer, string(id), in.Action, string(payload)); err != nil {
				return err
			}
		}
		return nil
	})
}

func mustID() domain.ID {
	id, err := domain.NewID()
	if err != nil {
		return domain.ID("00000000000000000000000000")
	}
	return id
}

// SyncPublicPages refreshes the public page registry for an installed module (CORE-32).
func (s *Service) SyncPublicPages(ctx context.Context, moduleID string, pages []PublicPage) error {
	if _, err := s.q(ctx).ExecContext(ctx, `DELETE FROM core.module_public_pages WHERE module_id = $1`, moduleID); err != nil {
		return err
	}
	for _, pg := range pages {
		path := "/" + strings.TrimPrefix(pg.Path, "/")
		if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_public_pages (module_id, path) VALUES ($1,$2)`, moduleID, path); err != nil {
			return err
		}
	}
	return nil
}

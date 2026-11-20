package modules

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
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
	ID         string          `json:"id"`
	ConsumerID string          `json:"consumer_id"`
	Contract   string          `json:"contract"`
	OccurredAt string          `json:"occurred_at"`
	Action     string          `json:"action"`
	Payload    json.RawMessage `json:"payload"`
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
	for field, id := range map[string]string{"consumer_id": in.ConsumerID, "provider_id": in.ProviderID} {
		if marketplace.ReservedModuleID(id) {
			return ContractLink{}, domain.Invalid(field, marketplace.ErrReservedModuleID.Error())
		}
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
	if !consumer.Approved["contracts:call"] {
		return ContractLink{}, domain.Invalid("consumer_id", "consumer lacks approved contracts:call scope")
	}
	if !provider.Approved["contracts:provide"] {
		return ContractLink{}, domain.Invalid("provider_id", "provider lacks approved contracts:provide scope")
	}
	if !consumer.Manifest.ConsumesContract(in.Contract) {
		return ContractLink{}, domain.Invalid("consumer_id", "consumer does not declare consumes for "+in.Contract)
	}
	offer, ok := provider.Manifest.Provide(in.Contract)
	if !ok {
		return ContractLink{}, domain.Invalid("provider_id", "provider does not declare provides for "+in.Contract)
	}
	id, err := domain.NewID()
	if err != nil {
		return ContractLink{}, err
	}
	now := s.now().UnixMicro()
	actor := string(p.UserID)
	var out ContractLink
	err = s.commit(ctx, func(ctx context.Context) error {
		// Serialize provider checks. Many providers are allowed only when every linked
		// provider (including this one) declares many: true for the contract.
		if _, err := s.q(ctx).ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "module_contract:"+in.Contract); err != nil {
			return err
		}
		if err := s.refuseExtraProvider(ctx, in.Contract, in.ProviderID, offer.Many); err != nil {
			return err
		}
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

// refuseExtraProvider enforces one provider per contract unless every linked provider
// (and the one being linked) declares many: true. Caller holds the contract advisory lock.
func (s *Service) refuseExtraProvider(ctx context.Context, contract, providerID string, newMany bool) error {
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT DISTINCT provider_id FROM core.module_contract_links WHERE contract = $1`, contract)
	if err != nil {
		return err
	}
	defer rows.Close()
	var providers []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		providers = append(providers, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(providers) == 0 {
		return nil
	}
	onlySame := true
	for _, id := range providers {
		if id != providerID {
			onlySame = false
			break
		}
	}
	if onlySame {
		return nil
	}
	if !newMany {
		return domain.Conflict("contract " + contract + " already has provider " + providers[0])
	}
	for _, id := range providers {
		in, err := s.Installed(ctx, id)
		if err != nil {
			return err
		}
		offer, ok := in.Manifest.Provide(contract)
		if !ok || !offer.Many {
			return domain.Conflict("contract " + contract + " already has provider " + id)
		}
	}
	return nil
}

// ContractProviderHeader selects a linked provider module id on a many-provider contract (CORE-31).
const ContractProviderHeader = "X-Togavsu-Contract-Provider"

// linkedProvider returns the provider id for a consumer on a contract, or empty when none.
// prefer, when set, must be one of the consumer's approved linked providers (module id only;
// no address). When prefer is empty and several providers are linked, the earliest link
// (created_at, id) is used.
func (s *Service) linkedProvider(ctx context.Context, contract, consumer, prefer string) (string, error) {
	prefer = strings.TrimSpace(prefer)
	if prefer != "" {
		var provider string
		err := s.q(ctx).QueryRowContext(ctx, `SELECT provider_id FROM core.module_contract_links WHERE contract = $1 AND consumer_id = $2 AND provider_id = $3`,
			contract, consumer, prefer).Scan(&provider)
		if errors.Is(err, sql.ErrNoRows) {
			return "", domain.NotFound("provider")
		}
		return provider, err
	}
	var provider string
	err := s.q(ctx).QueryRowContext(ctx, `SELECT provider_id FROM core.module_contract_links WHERE contract = $1 AND consumer_id = $2 ORDER BY created_at ASC, id ASC LIMIT 1`,
		contract, consumer).Scan(&provider)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return provider, err
}

// ListLinkedProviders returns provider module ids linked for the calling consumer on contract.
// Ids only — never addresses (CORE-31).
func (s *Service) ListLinkedProviders(ctx context.Context, p domain.Principal, contract string) ([]string, error) {
	if p.ModuleID == "" || (p.Source != domain.SourceModule && p.Source != domain.SourceService) {
		return nil, domain.Forbidden("only a module token may list linked providers")
	}
	contract = strings.TrimSpace(contract)
	if contract == "" {
		return nil, domain.Invalid("contract", "contract is required")
	}
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT provider_id FROM core.module_contract_links WHERE contract = $1 AND consumer_id = $2 ORDER BY created_at ASC, id ASC`,
		contract, p.ModuleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// CallContract proxies a consumer's contract operation to the linked provider (CORE-31).
// The response never includes the provider's address. The body is HMAC-signed like events.
// providerID, when set, selects among many-provider links (X-Togavsu-Contract-Provider).
func (s *Service) CallContract(ctx context.Context, p domain.Principal, contract, operation string, body []byte, contextToken, providerID string) (status int, header http.Header, respBody []byte, err error) {
	if strings.TrimSpace(contract) == ProcessorPaymentContract {
		return s.callProcessorPayment(ctx, p, operation, body, contextToken, providerID)
	}
	return s.callContract(ctx, p, contract, operation, body, contextToken, providerID)
}

func (s *Service) callContract(ctx context.Context, p domain.Principal, contract, operation string, body []byte, contextToken, providerID string) (status int, header http.Header, respBody []byte, err error) {
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
	if maxBytes <= zero {
		maxBytes = domain.ContractCallMaxBytes
	}
	if len(body) > maxBytes {
		return 0, nil, nil, &domain.APIError{Code: "payload_too_large", Message: "request body exceeds modules.contract_call_max_bytes"}
	}
	consumer, err := s.Installed(ctx, consumerID)
	if err != nil || !consumer.Enabled {
		return 0, nil, nil, domain.NotFound("module")
	}
	if !consumer.Approved["contracts:call"] {
		return 0, nil, nil, scopeDenied("contracts:call")
	}
	if !consumer.Manifest.ConsumesContract(contract) {
		return 0, nil, nil, domain.Forbidden("module does not consume " + contract)
	}
	providerID, err = s.linkedProvider(ctx, contract, consumerID, providerID)
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
	if !provider.Approved["contracts:provide"] {
		return 0, nil, nil, scopeDenied("contracts:provide")
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
	at := s.now()
	headers := SignWebhookSenderHeaders(secret, at, consumerID, body)
	if headers == nil {
		return 0, nil, nil, errors.New("modules: empty webhook secret or invalid/reserved module sender")
	}
	for key, values := range headers {
		req.Header[key] = values
	}
	req.Header.Set("X-Togavsu-Contract", contract)
	req.Header.Set("X-Togavsu-Contract-Operation", operation)
	req.Header.Set("X-Togavsu-Contract-Consumer", consumerID)
	if contextToken != "" {
		claims, err := s.VerifyContext(ctx, contextToken)
		if err != nil {
			return 0, nil, nil, err
		}
		if claims.Module != consumerID || claims.Provider != "" {
			return 0, nil, nil, domain.Forbidden("context token does not belong to the consumer")
		}
		claims.Provider = providerID
		claims.Contract = contract
		raw, err := json.Marshal(claims)
		if err != nil {
			return 0, nil, nil, err
		}
		payload := base64.RawURLEncoding.EncodeToString(raw)
		req.Header.Set(ContextHeader, payload+"."+s.contextMAC(payload))
	}
	resp, err := s.d.HTTP.Do(req)
	if err != nil {
		// Fail closed: every proxied attempt is audited (CORE-CHANGES-2 §4.1).
		if aerr := s.auditContractCall(ctx, p, contract, operation, consumerID, providerID, zero, "provider_unreachable"); aerr != nil {
			return 0, nil, nil, aerr
		}
		return 0, nil, nil, domain.Conflict("provider did not answer")
	}
	defer resp.Body.Close()
	respBody, err = io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return 0, nil, nil, err
	}
	if len(respBody) > maxBytes {
		if aerr := s.auditContractCall(ctx, p, contract, operation, consumerID, providerID, resp.StatusCode, "response_too_large"); aerr != nil {
			return 0, nil, nil, aerr
		}
		return 0, nil, nil, &domain.APIError{Code: "too_large", Message: "provider response exceeds modules.contract_call_max_bytes"}
	}
	// Strip any address-bearing headers so the consumer never learns the provider URL.
	outHeader := http.Header{}
	for _, k := range []string{"Content-Type", "X-Request-Id"} {
		if v := resp.Header.Get(k); v != "" {
			outHeader.Set(k, v)
		}
	}
	// Fail closed: never return the provider response without an audit row.
	if err := s.auditContractCall(ctx, p, contract, operation, consumerID, providerID, resp.StatusCode, "ok"); err != nil {
		return 0, nil, nil, err
	}
	return resp.StatusCode, outHeader, respBody, nil
}

func (s *Service) auditContractCall(ctx context.Context, p domain.Principal, contract, operation, consumer, provider string, status int, result string) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	_, err = s.d.Audit.Record(ctx, p, audit.Change{
		Entity: "module_contract_call", EntityID: id, Field: "call",
		New:    contract + ":" + operation + " consumer=" + consumer + " provider=" + provider + " status=" + strconv.Itoa(status) + " result=" + result,
		Reason: "module.contract_call",
	})
	return err
}

// PushContractEvent delivers the provider envelope to its named linked consumer
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
	if !strings.HasPrefix(in.Action, contract+".") && !(contract == "documents.generate.v1" && in.Action == "document.generated") {
		return domain.Invalid("action", "contract event action must be scoped under "+contract)
	}
	provider, err := s.Installed(ctx, p.ModuleID)
	if err != nil || !provider.Enabled {
		return domain.NotFound("module")
	}
	if !provider.Approved["contracts:provide"] {
		return scopeDenied("contracts:provide")
	}
	if _, ok := provider.Manifest.Provide(contract); !ok {
		return domain.Forbidden("module does not provide " + contract)
	}
	if in.ConsumerID == "" || in.ID == "" || in.Contract != contract || in.OccurredAt == "" {
		return domain.Invalid("event", "id, contract, consumer_id and occurred_at are required")
	}
	if _, err := time.Parse(time.RFC3339Nano, in.OccurredAt); err != nil {
		return domain.Invalid("occurred_at", "use an RFC3339 timestamp")
	}
	linked, err := s.linkedProvider(ctx, contract, in.ConsumerID, p.ModuleID)
	if err != nil || linked != p.ModuleID {
		return domain.Forbidden("event consumer is not linked to this provider")
	}
	consumer, err := s.Installed(ctx, in.ConsumerID)
	if err != nil || !consumer.Enabled || !consumer.Approved["contracts:call"] {
		return domain.NotFound("consumer")
	}
	consumers := []string{in.ConsumerID}
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	now := s.now().UnixMicro()
	return s.commit(ctx, func(ctx context.Context) error {
		for _, consumer := range consumers {
			id, err := domain.NewID()
			if err != nil {
				return err
			}
			if _, err := s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_deliveries (id, created_at, updated_at, created_by, module_id, event_id, action, payload, status, attempts, next_attempt_at, last_error, delivered_at, task_authorization)
VALUES ($1,$2,$2,$3,$4,$5,$6,$7,'pending',0,$2,'',0,'') ON CONFLICT (module_id,event_id) DO NOTHING`,
				string(id), now, p.ModuleID, consumer, in.ID, in.Action, string(payload)); err != nil {
				return err
			}
		}
		auditID, err := domain.NewID()
		if err != nil {
			return err
		}
		_, err = s.d.Audit.Record(ctx, p, audit.Change{
			Entity: "module_contract_event", EntityID: auditID, Field: "push",
			New:    contract + ":" + in.Action + " provider=" + p.ModuleID + " consumers=" + strconv.Itoa(len(consumers)),
			Reason: "module.contract_event",
		})
		return err
	})
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

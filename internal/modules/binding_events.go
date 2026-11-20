package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// BindingContractEvent is the batch 2 provider event, broadcast to approved linked
// consumers. ContractEventWrite remains the accepted targeted wire format.
type BindingContractEvent struct {
	Contract   string          `json:"contract"`
	Event      string          `json:"event"`
	EventID    string          `json:"event_id"`
	OccurredAt string          `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

// UUID punctuation and widths are fixed by the wire format, not firm settings.
var bindingEventUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validEventUUID(id string) bool { return bindingEventUUIDPattern.MatchString(id) }

func (s *Service) pushBindingContractEvent(ctx context.Context, p domain.Principal, contract string, in BindingContractEvent, raw []byte) error {
	if p.ModuleID == "" || (p.Source != domain.SourceModule && p.Source != domain.SourceService) || !p.UserID.IsZero() {
		return domain.Forbidden("only a provider module service token may push a contract event")
	}
	if in.Contract != contract || contract == "" {
		return domain.Invalid("contract", "body contract must equal route contract")
	}
	reserved, err := api.CoreEventNamespace(in.Event)
	if err != nil {
		return err
	}
	if strings.TrimSpace(in.Event) == "" || strings.TrimSpace(in.Event) != in.Event || reserved {
		return domain.Invalid("event", "use a nonempty contract event type outside core event namespaces")
	}
	if !validEventUUID(in.EventID) {
		return domain.Invalid("event_id", "use a UUID")
	}
	if _, err := time.Parse(time.RFC3339Nano, in.OccurredAt); err != nil {
		return domain.Invalid("occurred_at", "use an RFC3339 timestamp")
	}
	if data := bytes.TrimSpace(in.Data); len(data) == 0 || data[0] != '{' || !json.Valid(data) {
		return domain.Invalid("data", "data must be a JSON object")
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
	return s.commit(ctx, func(ctx context.Context) error {
		rows, err := s.q(ctx).QueryContext(ctx, `SELECT consumer_id FROM core.module_contract_links WHERE contract=$1 AND provider_id=$2 ORDER BY consumer_id`, contract, p.ModuleID)
		if err != nil {
			return err
		}
		var candidates []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			candidates = append(candidates, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		recipients := zero
		now := s.now().UnixMicro()
		for _, id := range candidates {
			consumer, err := s.Installed(ctx, id)
			if err != nil {
				return err
			}
			if !consumer.Enabled || !consumer.Approved["contracts:call"] || !consumer.Manifest.ConsumesContract(contract) {
				continue
			}
			delivery, err := domain.NewID()
			if err != nil {
				return err
			}
			result, err := s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_deliveries (id,created_at,updated_at,created_by,module_id,event_id,action,payload,status,attempts,next_attempt_at,last_error,delivered_at,task_authorization) VALUES ($1,$2,$2,$3,$4,$5,$6,$7,'pending',0,$2,'',0,'') ON CONFLICT (module_id,event_id) DO NOTHING`, string(delivery), now, p.ModuleID, id, contractEventKey(p.ModuleID, contract, strings.ToLower(in.EventID)), in.Event, string(raw))
			if err != nil {
				return err
			}
			n, err := result.RowsAffected()
			if err != nil {
				return err
			}
			recipients += int(n)
		}
		auditID, err := domain.NewID()
		if err != nil {
			return err
		}
		_, err = s.d.Audit.Record(ctx, p, audit.Change{Entity: "module_contract_event", EntityID: auditID, Field: "push", New: contract + ":" + in.Event + " provider=" + p.ModuleID + " consumers=" + strconv.Itoa(recipients), Reason: "module.contract_event"})
		return err
	})
}

// Core IDs are UUIDs; this fixed prefix cannot collide with any core event ID.
// JSON tuple encoding is unambiguous even for arbitrary targeted envelope IDs.
func contractEventKey(provider, contract, id string) string {
	raw, _ := json.Marshal([]string{provider, contract, id})
	return "contract:" + string(raw)
}

// Raw binding bodies are forwarded unchanged, so all recipients must see the
// same unambiguous outer object regardless of their JSON decoder.
func uniqueEventKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	first, err := dec.Token()
	if err != nil || first != json.Delim('{') {
		return domain.Invalid("body", "send a contract event object")
	}
	seen := map[string]bool{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return domain.Invalid("body", "invalid contract event object")
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return domain.Invalid("body", "duplicate contract event key")
		}
		seen[name] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return domain.Invalid("body", "invalid contract event object")
		}
	}
	_, err = dec.Token()
	return err
}

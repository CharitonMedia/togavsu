package modules

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// Reserve every namespace in the published core event registry, including events
// added in later core updates. A malformed registry fails closed.
var coreEventNamespaces, coreEventRegistryError = func() (map[string]bool, error) {
	var spec struct {
		Events map[string]json.RawMessage `json:"x-togavsu-event-scopes"`
	}
	err := json.Unmarshal(api.OpenAPI, &spec)
	if err == nil && len(spec.Events) == 0 {
		err = errors.New("core event registry is empty")
	}
	out := map[string]bool{"core": true, "file": true, "matter": true, "contact": true, "calendar": true, "payment": true}
	for name := range spec.Events {
		root, _, _ := strings.Cut(name, ".")
		out[root] = true
	}
	return out, err
}()

func validEventUUID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return false
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	return err == nil && len(raw) == 16 && raw[8]>>6 == 2 && raw[6]>>4 >= 1 && raw[6]>>4 <= 8
}

func (s *Service) pushBindingContractEvent(ctx context.Context, p domain.Principal, contract string, in BindingContractEvent, raw []byte) error {
	if p.ModuleID == "" || (p.Source != domain.SourceModule && p.Source != domain.SourceService) || !p.UserID.IsZero() {
		return domain.Forbidden("only a provider module service token may push a contract event")
	}
	if in.Contract != contract || contract == "" {
		return domain.Invalid("contract", "body contract must equal route contract")
	}
	root, _, _ := strings.Cut(in.Event, ".")
	if coreEventRegistryError != nil {
		return coreEventRegistryError
	}
	if strings.TrimSpace(in.Event) == "" || strings.TrimSpace(in.Event) != in.Event || coreEventNamespaces[root] {
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
		recipients := 0
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
			_, err = s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_deliveries (id,created_at,updated_at,created_by,module_id,event_id,action,payload,status,attempts,next_attempt_at,last_error,delivered_at,task_authorization) VALUES ($1,$2,$2,$3,$4,$5,$6,$7,'pending',0,$2,'',0,'') ON CONFLICT (module_id,event_id) DO NOTHING`, string(delivery), now, p.ModuleID, id, strings.ToLower(in.EventID), in.Event, string(raw))
			if err != nil {
				return err
			}
			recipients++
		}
		auditID, err := domain.NewID()
		if err != nil {
			return err
		}
		_, err = s.d.Audit.Record(ctx, p, audit.Change{Entity: "module_contract_event", EntityID: auditID, Field: "push", New: contract + ":" + in.Event + " provider=" + p.ModuleID + " consumers=" + strconv.Itoa(recipients), Reason: "module.contract_event"})
		return err
	})
}

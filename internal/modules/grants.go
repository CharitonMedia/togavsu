package modules

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TaskAuthorizationHeader carries a core-issued grant on a delivery; the module presents
// it on the later calls the grant covers.
const TaskAuthorizationHeader = "X-Togavsu-Task-Authorization"

// ProcessorEvent delivers a payment processor grant (billing.ProcessorEvent).
const ProcessorEvent = "payment.processor_authorized"

// directEvents go to one named module with its grant (DeliverGrant), never through the
// subscription fan-out, so no other module sees them.
var directEvents = map[string]bool{ProcessorEvent: true}

// Processor reports whether module is a registered payment processor: installed, enabled
// and subscribed to payment.processor_authorized. approved is the scopes the admin
// approved for it. An unknown module is not registered.
func (s *Service) Processor(ctx context.Context, module string) (map[string]bool, bool, error) {
	in, err := s.Installed(ctx, module)
	var api *domain.APIError
	if errors.As(err, &api) && api.Code == domain.CodeNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !in.Enabled || in.Status != StatusInstalled || !Subscribed(in.Manifest.Events, ProcessorEvent) {
		return nil, false, nil
	}
	return in.Approved, true, nil
}

// DeliverGrant queues ev to module alone, in the transaction ctx carries, with grant under
// field in the body and credential sealed for TaskAuthorizationHeader. Deliver signs and
// sends it like any event, with its retries.
func (s *Service) DeliverGrant(ctx context.Context, module string, ev domain.Event, field string, grant any, credential string) error {
	if !directEvents[ev.Action] {
		return errors.New("modules: " + ev.Action + " is not a direct event")
	}
	at := ev.CreatedAt
	if at.IsZero() {
		at = s.now()
	}
	base, err := json.Marshal(EventPayload{ID: string(ev.ID), Action: ev.Action, RecordType: ev.RecordType, RecordID: string(ev.RecordID),
		MatterID: string(ev.MatterID), OccurredAt: at.UTC().Format(time.RFC3339Nano), Module: module})
	if err != nil {
		return err
	}
	var body map[string]any
	if err := json.Unmarshal(base, &body); err != nil {
		return err
	}
	body[field] = grant
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	sealed, err := s.seal(credential)
	if err != nil {
		return err
	}
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	now := s.now().UnixMicro()
	_, err = s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_deliveries (id, created_at, updated_at, created_by, module_id, event_id, action, payload, status, attempts, next_attempt_at, last_error, delivered_at, task_authorization)
VALUES ($1,$2,$2,'',$3,$4,$5,$6,'pending',0,$2,'',0,$7)`, string(id), now, module, string(ev.ID), ev.Action, string(payload), sealed)
	return err
}

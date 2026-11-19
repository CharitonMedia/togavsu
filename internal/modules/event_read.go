package modules

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// EventAuthorizationHeader carries a record grant delivered with a core event.
const EventAuthorizationHeader = "X-Togavsu-Event-Authorization"

func (s *Service) eventReadMAC(payload string) string {
	mac := hmac.New(sha256.New, s.subkey("event-record-read"))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) eventReadToken(ctx context.Context, module string, body []byte) (string, error) {
	in, err := s.Installed(ctx, module)
	if err != nil {
		return "", err
	}
	if !in.Approved["event_records:read"] {
		return "", nil
	}
	var ev EventPayload
	if err := json.Unmarshal(body, &ev); err != nil {
		return "", err
	}
	switch ev.RecordType {
	case "matter", "contact", "calendar_event", "deadline", "file", "note", "matter_party", "contact_address", "contact_phone", "contact_email":
	default:
		return "", nil
	}
	if !in.Approved[eventRecordScope(ev.RecordType)] {
		return "", nil
	}
	// Only events emitted by the core grant record access. Provider contract events
	// cannot mint a record grant by supplying record-shaped data.
	if strings.HasPrefix(ev.Action, "module.") || ev.RecordID == "" {
		return "", nil
	}
	var actorKind string
	if err := s.q(ctx).QueryRowContext(ctx, `SELECT actor_kind FROM core.events WHERE id=$1`, ev.ID).Scan(&actorKind); err != nil {
		return "", nil
	}
	if actorKind == "module" {
		return "", nil
	}
	g := domain.EventRead{Module: module, EventID: ev.ID, RecordType: ev.RecordType, RecordID: domain.ID(ev.RecordID), MatterID: domain.ID(ev.MatterID), ExpiresAt: s.now().Add(eventReadTTL()).Unix()}
	b, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + s.eventReadMAC(payload), nil
}

// VerifyEventRead verifies the grant's module, signature, expiry and live approval.
func (s *Service) VerifyEventRead(ctx context.Context, module, token string) (*domain.EventRead, error) {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.eventReadMAC(payload))) {
		return nil, domain.Forbidden("invalid event record authorization")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, domain.Forbidden("invalid event record authorization")
	}
	var g domain.EventRead
	if json.Unmarshal(raw, &g) != nil || g.Module != module || g.RecordID.IsZero() || g.EventID == "" || s.now().Unix() >= g.ExpiresAt {
		return nil, domain.Forbidden("invalid or expired event record authorization")
	}
	in, err := s.Installed(ctx, module)
	if err != nil {
		return nil, err
	}
	if !in.Enabled || !in.Approved["event_records:read"] {
		return nil, scopeDenied("event_records:read")
	}
	return &g, nil
}

func eventRecordScope(record string) string {
	switch record {
	case "matter", "matter_party":
		return "matters:read"
	case "contact", "contact_address", "contact_phone", "contact_email":
		return "contacts:read"
	case "calendar_event":
		return "calendar:read"
	case "deadline":
		return "deadlines:read"
	case "file":
		return "files:read"
	case "note":
		return "notes:read"
	}
	return ""
}

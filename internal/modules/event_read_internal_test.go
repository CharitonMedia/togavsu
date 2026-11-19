package modules

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestEventReadRejectsExpiredChangedAndOtherModule(t *testing.T) {
	now := time.Unix(1800000000, 0)
	s := &Service{d: Deps{Key: []byte("synthetic-test-key-for-event-read"), Now: func() time.Time { return now }}}
	grant := domain.EventRead{Module: "sample", EventID: "event", RecordType: "deadline", RecordID: "deadline", MatterID: "matter", ExpiresAt: now.Add(-time.Second).Unix()}
	b, err := json.Marshal(grant)
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(b)
	token := payload + "." + s.eventReadMAC(payload)
	for name, tc := range map[string]struct{ module, token string }{"expired": {"sample", token}, "other module": {"other", token}, "tampered": {"sample", token + "changed"}, "malformed": {"sample", "junk"}} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.VerifyEventRead(context.Background(), tc.module, tc.token); err == nil {
				t.Fatal("invalid event grant accepted")
			}
		})
	}
	p := domain.Principal{Source: domain.SourceService, ModuleID: "sample", Roles: []domain.Role{domain.RoleStaff}, EventRead: &grant}
	if !p.EventRecord("deadline", "deadline", "matter") || p.EventRecord("calendar_event", "deadline", "matter") || p.EventRecord("deadline", "sibling", "matter") || p.EventRecord("deadline", "deadline", "other") {
		t.Fatal("record grant widened beyond named tuple")
	}
	p.UserID = "viewer"
	if p.EventRecord("deadline", "deadline", "matter") {
		t.Fatal("event grant accepted as delegated viewer")
	}
}

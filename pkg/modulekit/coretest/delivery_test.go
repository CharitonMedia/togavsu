package coretest_test

import (
	"bytes"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

func TestReadDeliveryRequiresSender(t *testing.T) {
	body := []byte(`{"sender":"claimed-provider"}`)
	m := coretest.Module{WebhookSecret: "synthetic-secret"}
	for _, bound := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/events", bytes.NewReader(body))
		r.Header = modulekit.SignWebhookHeaders(m.WebhookSecret, time.Now(), body)
		if bound {
			r.Header = modulekit.SignWebhookSenderHeaders(m.WebhookSecret, time.Now(), "actual-provider", body)
		}
		sender, raw, err := coretest.ReadDelivery(m, r)
		if bound {
			if err != nil || sender != "actual-provider" || !bytes.Equal(raw, body) {
				t.Fatalf("verified delivery %q %q: %v", sender, raw, err)
			}
		} else if err == nil || sender != "" || raw != nil {
			t.Fatalf("unsigned identity returned %q %q: %v", sender, raw, err)
		}
	}
}

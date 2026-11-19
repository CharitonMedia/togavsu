package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

func TestSystemMailSenderBinding(t *testing.T) {
	now := time.Unix(1800000000, 0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		sender, err := modules.VerifyWebhookSender("synthetic-secret", r.Header, body, now, 0)
		if err != nil || sender != "core" {
			t.Errorf("mail sender %q: %v", sender, err)
		}
		if err := modules.VerifyWebhookHeaders("synthetic-secret", r.Header, body, now, 0); err != nil {
			t.Errorf("legacy mail verification: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	m := &systemMailer{HTTP: srv.Client(), Now: func() time.Time { return now }}
	route := modules.SystemMailRoute{BaseURL: srv.URL, Path: "/system-mail", Secret: "synthetic-secret"}
	if err := m.postModule(context.Background(), route, systemMail{ID: "synthetic-mail", To: []string{"synthetic@example.test"}, Text: "Synthetic notice"}); err != nil {
		t.Fatal(err)
	}
}

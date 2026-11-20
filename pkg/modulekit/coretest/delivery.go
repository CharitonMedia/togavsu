package coretest

import (
	"io"
	"net/http"
	"time"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

// ReadDelivery reads a module request's raw body and verifies its sender using the
// deployed module's real webhook secret. It refuses unsigned/legacy deliveries;
// on failure it returns neither identity nor body. Use it in Serve handlers for
// acceptance tests. DeliverEvents uses the production pump and delivery path,
// including persisted contract-event senders and retries.
func ReadDelivery(m Module, r *http.Request) (sender string, body []byte, err error) {
	body, err = io.ReadAll(r.Body)
	if err != nil {
		return "", nil, err
	}
	sender, err = modulekit.VerifyWebhookSender(m.WebhookSecret, r.Header, body, time.Now(), time.Duration(zero))
	if err != nil {
		return "", nil, err
	}
	return sender, body, nil
}

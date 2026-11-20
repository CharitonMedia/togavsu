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

// PushContractEvent posts the binding batch 2 envelope through the real core
// using the installed provider's token. Read it in a Serve handler with ReadDelivery.
func (c *Core) PushContractEvent(provider string, event modulekit.BindingContractEvent) Resp {
	return c.ModuleClient(provider).Do(http.MethodPost, "/api/v1/modules/contracts/"+event.Contract+"/events", event)
}

// RecordProcessorPayment records a stored approval through the real broker with
// the consumer's bearer, without a viewer or a second provider request.
func (c *Core) RecordProcessorPayment(consumer string, in modulekit.ProcessorPaymentRecord) Resp {
	return c.ModuleClient(consumer).Do(http.MethodPost, "/api/v1/modules/contracts/"+modulekit.ProcessorPaymentContract+"/record", in)
}

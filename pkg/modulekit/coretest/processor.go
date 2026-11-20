package coretest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

// ProcessorCall records a verified broker request. Tokens are deliberately absent.
type ProcessorCall struct {
	ConsumerID, Operation, IntentID, Destination string
	AmountCents                                  int64
}

type processorOutcome struct {
	status, code string
	loseResponse bool
}

type fakeProcessorIntent struct {
	hash   [sha256.Size]byte
	result modulekit.ProcessorPaymentResult
}

// Processor is a deterministic in-process processor.payment.v1 provider. It checks
// the real broker's signature and consumer binding and never calls an outside
// service. Its durable provider state lasts for this fixture's lifetime.
type Processor struct {
	mu       sync.Mutex
	outcomes []processorOutcome
	intents  map[string]fakeProcessorIntent
	calls    []ProcessorCall
	charges  int
}

// NewProcessor creates an approving processor. Register Handler with c.Serve,
// install a provider declaring authorize:/authorize and recover:/recover, and
// approve its contract link to the consumer as the admin.
func NewProcessor() *Processor { return &Processor{intents: map[string]fakeProcessorIntent{}} }

// DeclineNext scripts the next new intent as declined, with no charge.
func (p *Processor) DeclineNext(code string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.outcomes = append(p.outcomes, processorOutcome{status: "declined", code: code})
}

// LoseNextApprovalResponse approves and stores the next new intent but returns
// HTTP 503. A later recover obtains that approval without another authorization.
func (p *Processor) LoseNextApprovalResponse() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.outcomes = append(p.outcomes, processorOutcome{status: "approved", loseResponse: true})
}

// Calls returns copied, verified requests in arrival order.
func (p *Processor) Calls() []ProcessorCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ProcessorCall(nil), p.calls...)
}

// Charges counts approved new intents, excluding replays and declines.
func (p *Processor) Charges() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.charges
}

// Handler verifies the actual deployed module's secret on every broker request.
func (p *Processor) Handler(m Module) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sender, body, err := ReadDelivery(m, r)
		if err != nil || sender == "core" || sender != r.Header.Get("X-Togavsu-Contract-Consumer") || r.Header.Get("X-Togavsu-Contract") != modulekit.ProcessorPaymentContract {
			http.Error(w, "invalid broker identity", http.StatusUnauthorized)
			return
		}
		op := r.Header.Get("X-Togavsu-Contract-Operation")
		var in modulekit.ProcessorPaymentWrite
		if op == "recover" {
			var recover struct {
				IntentID string `json:"intent_id"`
			}
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&recover); err != nil {
				http.Error(w, "invalid recovery", http.StatusBadRequest)
				return
			}
			in.IntentID = recover.IntentID
		} else if op == "authorize" {
			if err := json.Unmarshal(body, &in); err != nil || in.AmountCents <= modulekit.Cents(zero) || in.PaymentToken == "" || in.Destination != "operating" {
				http.Error(w, "invalid authorization", http.StatusBadRequest)
				return
			}
		} else {
			http.NotFound(w, r)
			return
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		p.calls = append(p.calls, ProcessorCall{ConsumerID: sender, Operation: op, IntentID: in.IntentID, Destination: in.Destination, AmountCents: int64(in.AmountCents)})
		key := sender + "/" + in.IntentID
		prior, ok := p.intents[key]
		canonical, _ := json.Marshal(in)
		hash := sha256.Sum256(canonical)
		if ok {
			if op == "authorize" && prior.hash != hash {
				http.Error(w, "intent changed", http.StatusConflict)
				return
			}
		} else {
			if op == "recover" {
				http.NotFound(w, r)
				return
			}
			outcome := processorOutcome{status: "approved"}
			if len(p.outcomes) != 0 {
				outcome, p.outcomes = p.outcomes[0], p.outcomes[one:]
			}
			result := modulekit.ProcessorPaymentResult{IntentID: in.IntentID, AmountCents: in.AmountCents, Destination: in.Destination, Status: outcome.status, DeclineCode: outcome.code}
			if outcome.status == "approved" {
				p.charges++
				result.ReferenceNumber = fmt.Sprintf("synthetic-approval-%d", p.charges)
			}
			prior = fakeProcessorIntent{hash: hash, result: result}
			p.intents[key] = prior
			if outcome.loseResponse {
				http.Error(w, "synthetic lost approval response", http.StatusServiceUnavailable)
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(prior.result)
	})
}

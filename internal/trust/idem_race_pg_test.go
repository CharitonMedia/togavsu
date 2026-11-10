package trust

import (
	"fmt"
	"sync"
	"testing"
)

// Codex checkpoint, P2 idem.go: two requests from one actor with the same
// Idempotency-Key, sent at once, must not both run the write. CreateAccount and
// CreateReplenishments do not take the trust write lock, so only the idempotency claim
// serializes them. Every racer must get the one stored result, and exactly one row lands.
func TestPGConcurrentIdempotencyKeyReplays(t *testing.T) {
	e := newEnv(t)
	const racers = 8
	race := func(run func(i int) (string, error)) []string {
		t.Helper()
		ids := make([]string, racers)
		errs := make([]error, racers)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				ids[i], errs[i] = run(i)
			}(i)
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("racer %d: %v", i, err)
			}
		}
		return ids
	}
	same := func(what string, ids []string) {
		t.Helper()
		for i, id := range ids {
			if id == "" || id != ids[0] {
				t.Fatalf("%s: racer %d got %q, racer 0 got %q", what, i, id, ids[0])
			}
		}
	}
	for round := 0; round < 5; round++ {
		accounts := e.count("trust_accounts")
		key := fmt.Sprintf("race-account-%d", round)
		in := AccountInput{Bank: "Synthetic Bank", Name: fmt.Sprintf("Operating %d", round), LastFour: "1234"}
		ids := race(func(int) (string, error) {
			a, err := e.svc.CreateAccount(e.ctx, e.admin, in, key)
			return string(a.ID), err
		})
		same("account", ids)
		if n := e.count("trust_accounts"); n != accounts+1 {
			t.Fatalf("round %d: %d accounts created, want 1", round, n-accounts)
		}

		repls := e.count("trust_replenishment_requests")
		amount := int64(10000 + round)
		items := []ReplenishmentInput{{MatterID: e.matter.ID, ClientID: e.clientID, AmountCents: &amount}}
		key = fmt.Sprintf("race-replenish-%d", round)
		ids = race(func(int) (string, error) {
			out, err := e.svc.CreateReplenishments(e.ctx, e.attorney, items, key)
			if err != nil || len(out) != 1 {
				return "", fmt.Errorf("%d results: %v", len(out), err)
			}
			return string(out[0].ID), nil
		})
		same("replenishment", ids)
		if n := e.count("trust_replenishment_requests"); n != repls+1 {
			t.Fatalf("round %d: %d replenishment requests created, want 1", round, n-repls)
		}
	}
}

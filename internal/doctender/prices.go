package doctender

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// modelPricesKey is the AI tab's price table, read on every call so a price change
// applies to the next call. It is a shared setting the integration lead registers
// (proposed in this lane's PR): until it is registered every call costs 0 cents.
const modelPricesKey = "ai.model_prices"

// modelPrice is one model's price in cents per million tokens, so $3.00 per million
// input tokens is {"input": 300}.
type modelPrice struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// modelPrices is the price table keyed by the model name the provider is called with.
type modelPrices map[string]modelPrice

// parseModelPrices reads the price table. Every price must be a finite number of cents
// at or above zero, and every model must give both.
func parseModelPrices(raw string) (modelPrices, error) {
	var in map[string]map[string]*float64
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return nil, errors.New("model prices are a JSON object of model to {\"input\", \"output\"} cents per million tokens")
	}
	out := modelPrices{}
	for model, p := range in {
		if model == "" || p == nil || p["input"] == nil || p["output"] == nil {
			return nil, errors.New("each model price gives input and output cents per million tokens")
		}
		for k := range p {
			if k != "input" && k != "output" {
				return nil, errors.New("a model price has only input and output")
			}
		}
		mp := modelPrice{Input: *p["input"], Output: *p["output"]}
		for _, v := range []float64{mp.Input, mp.Output} {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Signbit(v) {
				return nil, errors.New("a model price is a number of cents at or above zero")
			}
		}
		out[model] = mp
	}
	return out, nil
}

// costCents is the cost of one call at model's price, rounded to the nearest cent. A
// model with no price costs 0.
func (p modelPrices) costCents(model string, promptTokens, completionTokens int) int64 {
	mp := p[model] // the zero price when the model has none
	return int64(math.Round((float64(promptTokens)*mp.Input + float64(completionTokens)*mp.Output) / 1e6))
}

// prices reads the price table. A table that is not registered yet is empty.
func (s *Service) prices(ctx context.Context) (modelPrices, error) {
	raw, err := s.setting(ctx, modelPricesKey)
	if errors.Is(err, settings.ErrUnknown) {
		return modelPrices{}, nil
	}
	if err != nil {
		return nil, err
	}
	return parseModelPrices(raw)
}

// costTarget is the job and unit a model call's cost is added to (PRD 8.21: each
// step is recorded with its cost).
type costTarget struct{ job, unit domain.ID }

type costKey struct{}

// withCost returns ctx whose model calls add their cost to job and, when unit is set,
// to that unit.
func withCost(ctx context.Context, job, unit domain.ID) context.Context {
	return context.WithValue(ctx, costKey{}, costTarget{job: job, unit: unit})
}

// addCost adds cost to the job and unit ctx names, if any.
func (s *Service) addCost(ctx context.Context, cost int64) error {
	t, ok := ctx.Value(costKey{}).(costTarget)
	if !ok || cost == int64(zero) {
		return nil
	}
	if t.unit != "" {
		if _, err := s.exec(ctx, `UPDATE doc_tender_units SET cost_cents = cost_cents + ? WHERE id = ?`, cost, t.unit); err != nil {
			return err
		}
	}
	if t.job == "" {
		return nil
	}
	_, err := s.exec(ctx, `UPDATE doc_tender_jobs SET cost_cents = cost_cents + ? WHERE id = ?`, cost, t.job)
	return err
}

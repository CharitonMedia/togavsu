package doctender

import (
	"context"
	"errors"
	"math"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// modelPricesKey is the AI tab's price table, read on every call so a price change
// applies to the next call.
const modelPricesKey = domain.ModelPricesKey

// modelPrices is the price table keyed by the model name the provider is called with.
type modelPrices map[string]domain.ModelPrice

// parseModelPrices reads the price table (domain.ParseModelPrices, the setting's check).
func parseModelPrices(raw string) (modelPrices, error) {
	return domain.ParseModelPrices(raw)
}

// costCents is the cost of one call at model's price, rounded to the nearest cent. A
// model with no price costs 0.
func (p modelPrices) costCents(model string, promptTokens, completionTokens int) int64 {
	mp := p[model] // the zero price when the model has none
	return int64(math.Round((float64(promptTokens)*mp.Input + float64(completionTokens)*mp.Output) / 1e6))
}

// prices reads the price table. A registry without the setting (a test's) reads empty.
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

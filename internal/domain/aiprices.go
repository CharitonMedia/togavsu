package domain

import (
	"encoding/json"
	"errors"
	"math"
)

// ModelPrice is one model's price in cents per million tokens (the ai.model_prices
// setting), so $3.00 per million input tokens is {"input": 300}.
type ModelPrice struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// ModelPrices is the ai.model_prices setting: prices keyed by the model name the
// provider is called with.
type ModelPrices map[string]ModelPrice

// ParseModelPrices reads ai.model_prices. Every price must be a finite number of cents
// at or above zero, and every model must give both.
func ParseModelPrices(raw string) (ModelPrices, error) {
	var in map[string]map[string]*float64
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return nil, errors.New("model prices are a JSON object of model to {\"input\", \"output\"} cents per million tokens")
	}
	out := ModelPrices{}
	for model, p := range in {
		if model == "" || p == nil || p["input"] == nil || p["output"] == nil {
			return nil, errors.New("each model price gives input and output cents per million tokens")
		}
		for k := range p {
			if k != "input" && k != "output" {
				return nil, errors.New("a model price has only input and output")
			}
		}
		mp := ModelPrice{Input: *p["input"], Output: *p["output"]}
		for _, v := range []float64{mp.Input, mp.Output} {
			if math.IsNaN(v) || math.IsInf(v, 0) || math.Signbit(v) {
				return nil, errors.New("a model price is a number of cents at or above zero")
			}
		}
		out[model] = mp
	}
	return out, nil
}

// CostCents is the cost of one call at model's price, rounded to the nearest cent. A
// model with no price costs 0, as the AI tab says.
func (p ModelPrices) CostCents(model string, promptTokens, completionTokens int) int64 {
	mp := p[model] // the zero price when the model has none
	return int64(math.Round((float64(promptTokens)*mp.Input + float64(completionTokens)*mp.Output) / 1e6))
}

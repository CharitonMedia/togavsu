package domain

import (
	"encoding/json"
	"errors"
	"math"
)

// ModelPricesKey is the AI tab's price table (setting ai.model_prices): cents per
// million tokens keyed by the model name the provider is called with.
const ModelPricesKey = "ai.model_prices"

// ModelPrice is one model's price in cents per million tokens, so $3.00 per million
// input tokens is {"input": 300}.
type ModelPrice struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// ParseModelPrices reads the price table. Every price must be a finite number of cents
// at or above zero, and every model must give both. The settings registry uses it as
// the write check, and Doc Tender reads the table with it on every call.
func ParseModelPrices(raw string) (map[string]ModelPrice, error) {
	var in map[string]map[string]*float64
	if err := json.Unmarshal([]byte(raw), &in); err != nil || in == nil {
		return nil, errors.New("model prices are a JSON object of model to {\"input\", \"output\"} cents per million tokens")
	}
	out := map[string]ModelPrice{}
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

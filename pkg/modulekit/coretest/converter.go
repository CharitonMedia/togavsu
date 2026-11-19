package coretest

import (
	"context"
	"errors"
	"sync"
)

// Conversion records one synthetic converter invocation. Bytes are copied.
type Conversion struct {
	Input    []byte
	From, To string
}

// Converter stands in for document conversion. The core still checks file access,
// output format, audit, immutable versions and conversion links. It never runs a
// host executable. Results must be synthetic; this is not a document renderer.
type Converter struct {
	mu      sync.Mutex
	results map[string][]byte
	failure error
	calls   []Conversion
}

func NewConverter() *Converter {
	return &Converter{results: map[string][]byte{"pdf": []byte("%PDF-1.4\n% synthetic coretest conversion\n%%EOF\n")}}
}

// SetResult sets the synthetic output for a target extension.
func (c *Converter) SetResult(format string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results[format] = append([]byte(nil), data...)
}

// Fail makes subsequent conversions fail. Pass nil to restore normal conversion.
func (c *Converter) Fail(err error) { c.mu.Lock(); defer c.mu.Unlock(); c.failure = err }

// Calls returns a snapshot of converter invocations.
func (c *Converter) Calls() []Conversion {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]Conversion(nil), c.calls...)
	for i := range out {
		out[i].Input = append([]byte(nil), out[i].Input...)
	}
	return out
}

// Convert is the core's injected conversion transport.
func (c *Converter) Convert(ctx context.Context, data []byte, from, to string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, Conversion{append([]byte(nil), data...), from, to})
	if c.failure != nil {
		return nil, c.failure
	}
	out, ok := c.results[to]
	if !ok {
		return nil, errors.New("coretest: set a synthetic converter result for " + to)
	}
	return append([]byte(nil), out...), nil
}

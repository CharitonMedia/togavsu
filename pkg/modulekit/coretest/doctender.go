package coretest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/doctender"
)

// DocTender runs real Postgres jobs and the real extraction, labeling, category,
// naming and immutable-rendition pipeline. Model, OCR, conversion and media services
// are deterministic in-process stand-ins; no external programs or providers run.
type DocTender struct {
	core           *Core
	mu             sync.Mutex
	text, category string
	fail           error
	calls          []DocTenderCall
	log            doctender.CallLog
}

// DocTenderCall records an outside model request without leaking internal types.
type DocTenderCall struct {
	Task             string
	Check            bool
	FileID, MatterID string
}

func newDocTender(c *Core) *DocTender {
	return &DocTender{core: c, text: "Synthetic coretest document text.", category: "reference"}
}

func (d *DocTender) adapters(deps *doctender.Deps) {
	deps.Models = docTenderModels{d}
	deps.Second = docTenderReader{d}
	deps.Docs = docTenderDocs{d}
	deps.Media = docTenderMedia{}
}

// SetCategory sets the categorizer's proposed category key, which must exist and be
// active. Person-locked category hints remain protected by the production pipeline.
func (d *DocTender) SetCategory(key string) { d.mu.Lock(); defer d.mu.Unlock(); d.category = key }

// SetText supplies the synthetic OCR and document-conversion result. Native text,
// PDF and spreadsheet extraction still reads the actual uploaded bytes.
func (d *DocTender) SetText(text string) { d.mu.Lock(); defer d.mu.Unlock(); d.text = text }

// Fail injects an outside-model/reader failure; nil restores success.
func (d *DocTender) Fail(err error) { d.mu.Lock(); defer d.mu.Unlock(); d.fail = err }

// Calls returns a copy of the model request log.
func (d *DocTender) Calls() []DocTenderCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]DocTenderCall(nil), d.calls...)
}

// RunOnce processes one currently due production job. It never advances retry time.
func (d *DocTender) RunOnce(ctx context.Context) (bool, error) {
	return d.core.app.DocTender.RunOnce(ctx)
}

// Run drains currently due jobs. Deferred retries remain queued; errors fail the
// test. No goroutine, sleep, polling timeout or external executable is involved.
func (d *DocTender) Run() int {
	d.core.T.Helper()
	n := zero
	for {
		ran, err := d.RunOnce(context.Background())
		if err != nil {
			d.core.T.Fatal(err)
		}
		if !ran {
			return n
		}
		n++
	}
}

// RunDocTender drains due Doc Tender jobs using the harness stand-ins.
func (c *Core) RunDocTender() int { c.T.Helper(); return c.DocTender.Run() }

type docTenderModels struct{ d *DocTender }

func (docTenderModels) Ready(context.Context) (bool, error)  { return true, nil }
func (docTenderModels) Health(context.Context, string) error { return nil }
func (docTenderModels) Route(_ context.Context, task string, check bool) (doctender.Route, error) {
	model := "synthetic-primary"
	if check {
		model = "synthetic-check"
	}
	return doctender.Route{Provider: "coretest", Model: model}, nil
}
func (m docTenderModels) Call(ctx context.Context, req doctender.Request) (doctender.Response, error) {
	d := m.d
	d.mu.Lock()
	d.calls = append(d.calls, DocTenderCall{Task: req.Task, Check: req.Check, FileID: string(req.FileID), MatterID: string(req.MatterID)})
	text, category, err := d.text, d.category, d.fail
	log := d.log
	d.mu.Unlock()
	if err != nil {
		return doctender.Response{}, err
	}
	route, _ := m.Route(ctx, req.Task, req.Check)
	resp := doctender.Response{Provider: route.Provider, Model: route.Model, Text: text}
	var value any
	switch req.Task {
	case doctender.TaskLabeling:
		value = doctender.Label{WhatItIs: "synthetic document", WhatItIsConfidence: float64(one), Summary: "Synthetic acceptance document.", DocDateConfidence: float64(one)}
	case doctender.TaskCategorizing:
		value = doctender.CategoryAnswer{Category: category, Confidence: float64(one)}
	case doctender.TaskPhoto:
		value = map[string]any{"text": text, "description": "Synthetic page", "confidence": float64(one)}
	case doctender.TaskGrouping:
		return doctender.Response{}, errors.New("coretest: script grouping in a dedicated grouping acceptance fixture")
	case doctender.TaskTranscription:
		return doctender.Response{}, errors.New("coretest: media stand-in requires a dedicated media fixture")
	}
	if value != nil {
		b, _ := json.Marshal(value)
		resp.Text = string(b)
	}
	if log != nil {
		if err := log(ctx, req.Task, resp, doctender.Route{}, req.FileID, req.MatterID); err != nil {
			return doctender.Response{}, err
		}
	}
	return resp, nil
}

type docTenderReader struct{ d *DocTender }

func (docTenderReader) Name() string { return "coretest-ocr" }
func (r docTenderReader) Read(context.Context, []byte) (string, error) {
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	return r.d.text, r.d.fail
}

type docTenderDocs struct{ d *DocTender }

func (r docTenderDocs) ToMarkdown(context.Context, string, []byte) (string, int, int, error) {
	r.d.mu.Lock()
	defer r.d.mu.Unlock()
	return r.d.text, len(strings.Fields(r.d.text)), one, r.d.fail
}

// Media deliberately returns a deterministic error instead of selecting ffmpeg on
// a contributor's host. Media fidelity is covered by the core's own fixtures.
type docTenderMedia struct{}

func (docTenderMedia) Duration(context.Context, string) (time.Duration, error) {
	return 0, errors.New("coretest: use a dedicated media fixture")
}
func (docTenderMedia) HasAudio(context.Context, string) (bool, error) {
	return false, errors.New("coretest: use a dedicated media fixture")
}
func (docTenderMedia) Audio(context.Context, string, time.Duration, time.Duration) ([]byte, error) {
	return nil, errors.New("coretest: use a dedicated media fixture")
}
func (docTenderMedia) Frames(context.Context, string, time.Duration, time.Duration, time.Duration, func(doctender.Frame) error) error {
	return errors.New("coretest: use a dedicated media fixture")
}

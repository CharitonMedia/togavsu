package doctender

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Doc Tender's model tasks (PRD 8.7 AI tab). Each has a model and a check model.
const (
	TaskOCR           = "ocr"
	TaskPhoto         = "photo_description"
	TaskGrouping      = "photo_grouping"
	TaskTranscription = "transcription"
	TaskLabeling      = "labeling"
	TaskCategorizing  = "categorizing"
)

// Providers named on the AI tab.
const (
	ProviderOpenAI     = "openai"
	ProviderAnthropic  = "anthropic"
	ProviderCompatible = "compatible"
	ProviderLocal      = "local"
)

// Errors from routing.
var (
	// ErrNoAI means no provider key is set yet. Jobs hold until one is.
	ErrNoAI = errors.New("doctender: no AI provider is set up")
	// ErrLocalRefused means a labeling or categorizing route resolved to a local endpoint.
	ErrLocalRefused = errors.New("doctender: labeling and categorizing never run on a local endpoint")
	// ErrNoCheckModel means the task has no check model configured.
	ErrNoCheckModel = errors.New("doctender: no check model is set for this task")
)

// ProviderDown is a provider that failed its health check. No attempt is spent.
type ProviderDown struct {
	Provider string
	Err      error
}

func (e *ProviderDown) Error() string { return "provider " + e.Provider + " is down: " + e.Err.Error() }
func (e *ProviderDown) Unwrap() error { return e.Err }

//go:embed wire.json
var wireJSON []byte

// wire holds fixed protocol facts: public provider endpoints and API versions. They are
// not firm settings; the firm's keys, models and timeouts come from the registry.
type wireFacts struct {
	OpenAIBaseURL      string  `json:"openai_base_url"`
	AnthropicBaseURL   string  `json:"anthropic_base_url"`
	AnthropicVersion   string  `json:"anthropic_version"`
	AnthropicMaxTokens int     `json:"anthropic_max_tokens"`
	ErrorBodyBytes     int64   `json:"error_body_bytes"`
	HoverSummaryChars  int     `json:"hover_summary_chars"`
	ClientLastChars    int     `json:"client_last_chars"`
	TJWordSpace        float64 `json:"tj_word_space"`
	// MaxDecodedBytes caps what one decompressed stream or archive entry may expand to,
	// and MaxImagePixels caps a photo's width times height, so a small upload cannot
	// expand into an out-of-memory kill of the core. MaxSheetCells caps one sheet's
	// table, rows times its widest row, counting the empty rows and cells that a cell's
	// own reference asks for, before any of them is built.
	MaxDecodedBytes int64 `json:"max_decoded_bytes"`
	MaxImagePixels  int64 `json:"max_image_pixels"`
	MaxSheetCells   int64 `json:"max_sheet_cells"`
}

var wire = sync.OnceValue(func() wireFacts {
	var w wireFacts
	if err := json.Unmarshal(wireJSON, &w); err != nil {
		panic("doctender: wire.json: " + err.Error())
	}
	return w
})

// Part is one piece of a model request: text, or bytes with a media type.
type Part struct {
	Text      string
	MediaType string
	Name      string
	Data      []byte
}

// Request is one model call.
type Request struct {
	Task string
	// Check selects the task's check model instead of its model.
	Check  bool
	System string
	Parts  []Part
	// JSON asks for a JSON object answer.
	JSON     bool
	FileID   domain.ID
	MatterID domain.ID
}

// Segment is one transcript segment in seconds.
type Segment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Speaker string  `json:"speaker,omitempty"`
	Text    string  `json:"text"`
}

// Response is a model answer.
type Response struct {
	Text             string
	Provider         string
	Model            string
	PromptTokens     int
	CompletionTokens int
	FellBack         bool
	Segments         []Segment
	Duration         float64
}

// Route is a provider and model for one task.
type Route struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

func (r Route) String() string { return r.Provider + "/" + r.Model }

// override is one entry of ai.task_overrides.
type override struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	CheckProvider string `json:"check_provider"`
	CheckModel    string `json:"check_model"`
}

// Models is what Doc Tender needs from the AI layer. Router implements it.
type Models interface {
	// Ready reports whether any provider key is set.
	Ready(ctx context.Context) (bool, error)
	// Route resolves a task's model or check model from the AI tab.
	Route(ctx context.Context, task string, check bool) (Route, error)
	// Health checks a provider before calls; an error means down.
	Health(ctx context.Context, provider string) error
	// Call runs one request and logs it to the AI call log.
	Call(ctx context.Context, req Request) (Response, error)
}

// CallLog stores one AI call log row.
type CallLog func(ctx context.Context, task string, resp Response, fallback Route, fileID, matterID domain.ID) error

// Router routes Doc Tender's model calls by the AI tab settings and talks to providers.
type Router struct {
	Settings Settings
	HTTP     *http.Client
	// BaseURLs overrides a provider's public endpoint (an OpenAI-wire test server).
	BaseURLs map[string]string
	Log      CallLog
}

func (r *Router) get(ctx context.Context, key string) (string, error) {
	return r.Settings.Get(ctx, key)
}

func (r *Router) key(ctx context.Context, provider string) (string, error) {
	switch provider {
	case ProviderOpenAI:
		return r.get(ctx, "ai.openai_api_key")
	case ProviderAnthropic:
		return r.get(ctx, "ai.anthropic_api_key")
	case ProviderCompatible:
		return r.get(ctx, "ai.compatible_api_key")
	case ProviderLocal:
		return "", nil
	}
	return "", fmt.Errorf("doctender: unknown provider %q", provider)
}

func (r *Router) baseURL(ctx context.Context, provider string) (string, error) {
	if u := r.BaseURLs[provider]; u != "" {
		return strings.TrimRight(u, "/"), nil
	}
	switch provider {
	case ProviderOpenAI:
		return wire().OpenAIBaseURL, nil
	case ProviderAnthropic:
		return wire().AnthropicBaseURL, nil
	case ProviderCompatible:
		u, err := r.get(ctx, "ai.compatible_base_url")
		return strings.TrimRight(u, "/"), err
	case ProviderLocal:
		u, err := r.get(ctx, "ai.local_base_url")
		return strings.TrimRight(u, "/"), err
	}
	return "", fmt.Errorf("doctender: unknown provider %q", provider)
}

// Ready implements Models: a provider key (or a local endpoint for the tasks that may
// use one) is set.
func (r *Router) Ready(ctx context.Context) (bool, error) {
	for _, p := range []string{ProviderOpenAI, ProviderAnthropic, ProviderCompatible} {
		k, err := r.key(ctx, p)
		if err != nil {
			return false, err
		}
		if k != "" {
			return true, nil
		}
	}
	return false, nil
}

func (r *Router) overrides(ctx context.Context) (map[string]override, error) {
	raw, err := r.get(ctx, "ai.task_overrides")
	if err != nil {
		return nil, err
	}
	out := map[string]override{}
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("doctender: ai.task_overrides: %w", err)
	}
	return out, nil
}

// cloudOnly is a task that never runs on a local endpoint (PRD 8.7, 8.21).
func cloudOnly(task string) bool { return task == TaskLabeling || task == TaskCategorizing }

// Route implements Models (PRD 8.7): labeling and categorizing default to gpt-6.1-sol
// on OpenAI once an OpenAI key is set, otherwise the primary provider's strong model;
// each check model defaults to the secondary provider's model for that task, or to the
// primary provider's other tier when there is no secondary.
func (r *Router) Route(ctx context.Context, task string, check bool) (Route, error) {
	ov, err := r.overrides(ctx)
	if err != nil {
		return Route{}, err
	}
	o := ov[task]
	primary, err := r.get(ctx, "ai.primary_provider")
	if err != nil {
		return Route{}, err
	}
	secondary, err := r.get(ctx, "ai.secondary_provider")
	if err != nil {
		return Route{}, err
	}
	pStrong, _ := r.get(ctx, "ai.primary_strong_model")
	pFast, _ := r.get(ctx, "ai.primary_fast_model")
	sStrong, _ := r.get(ctx, "ai.secondary_strong_model")
	stt, _ := r.get(ctx, "ai.speech_to_text_model")
	openaiKey, err := r.key(ctx, ProviderOpenAI)
	if err != nil {
		return Route{}, err
	}
	var rt Route
	if !check {
		switch {
		case o.Provider != "" && o.Model != "":
			rt = Route{o.Provider, o.Model}
		case cloudOnly(task):
			key := "ai.labeling_model"
			if task == TaskCategorizing {
				key = "ai.categorizing_model"
			}
			model, err := r.get(ctx, key)
			if err != nil {
				return Route{}, err
			}
			switch {
			case model == domain.LabelingModel && openaiKey != "":
				rt = Route{ProviderOpenAI, model}
			case model == domain.LabelingModel:
				rt = Route{primary, pStrong}
			default:
				rt = Route{primary, model}
			}
		case task == TaskTranscription:
			rt = Route{primary, stt}
		default:
			rt = Route{primary, pStrong}
		}
	} else {
		switch {
		case o.CheckProvider != "" && o.CheckModel != "":
			rt = Route{o.CheckProvider, o.CheckModel}
		case task == TaskTranscription:
			// A second transcription runs only where a check model is set.
			return Route{}, ErrNoCheckModel
		case secondary != "" && sStrong != "":
			rt = Route{secondary, sStrong}
		default:
			rt = Route{primary, pFast}
		}
	}
	if rt.Provider == "" || rt.Model == "" {
		if ok, err := r.Ready(ctx); err != nil || !ok {
			return Route{}, ErrNoAI
		}
		return Route{}, fmt.Errorf("doctender: no model is set for %s on the AI tab", task)
	}
	if cloudOnly(task) && rt.Provider == ProviderLocal {
		return Route{}, ErrLocalRefused
	}
	return rt, nil
}

// fallbackRoute is the secondary provider's model of the same tier, used when the
// primary call fails or times out.
func (r *Router) fallbackRoute(ctx context.Context, task string, rt Route) (Route, bool) {
	secondary, _ := r.get(ctx, "ai.secondary_provider")
	model, _ := r.get(ctx, "ai.secondary_strong_model")
	if task == TaskTranscription {
		model, _ = r.get(ctx, "ai.speech_to_text_model")
	}
	if secondary == "" || model == "" || secondary == rt.Provider {
		return Route{}, false
	}
	if cloudOnly(task) && secondary == ProviderLocal {
		return Route{}, false
	}
	return Route{secondary, model}, true
}

// ChoicesForTask lists the providers the AI tab may offer for a task. Labeling and
// categorizing never list a local endpoint (AC-052).
func ChoicesForTask(task string) []string {
	out := []string{ProviderOpenAI, ProviderAnthropic, ProviderCompatible}
	if !cloudOnly(task) {
		out = append(out, ProviderLocal)
	}
	return out
}

// Health implements Models with each provider's model list.
func (r *Router) Health(ctx context.Context, provider string) error {
	base, err := r.baseURL(ctx, provider)
	if err != nil {
		return err
	}
	if base == "" {
		return fmt.Errorf("no endpoint for %s", provider)
	}
	key, err := r.key(ctx, provider)
	if err != nil {
		return err
	}
	ctx, cancel, err := r.withTimeout(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return err
	}
	r.auth(req, provider, key)
	resp, err := r.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= http.StatusInternalServerError || resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("health check: %s", resp.Status)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("health check refused: %s", resp.Status)
	}
	return nil
}

func (r *Router) client() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return http.DefaultClient
}

func (r *Router) withTimeout(ctx context.Context) (context.Context, context.CancelFunc, error) {
	raw, err := r.get(ctx, "ai.timeout")
	if err != nil {
		return nil, nil, err
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return nil, nil, err
	}
	c, cancel := context.WithTimeout(ctx, d)
	return c, cancel, nil
}

func (r *Router) auth(req *http.Request, provider, key string) {
	if provider == ProviderAnthropic {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", wire().AnthropicVersion)
		return
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
}

// Call implements Models. A failure or timeout on the route goes to the secondary
// provider once; every answered call is logged with whether it fell back.
func (r *Router) Call(ctx context.Context, req Request) (Response, error) {
	rt, err := r.Route(ctx, req.Task, req.Check)
	if err != nil {
		return Response{}, err
	}
	resp, err := r.callRoute(ctx, rt, req)
	if err != nil {
		var down *ProviderDown
		if errors.As(err, &down) {
			return Response{}, err
		}
		fb, ok := r.fallbackRoute(ctx, req.Task, rt)
		if !ok {
			return Response{}, err
		}
		resp, err = r.callRoute(ctx, fb, req)
		if err != nil {
			return Response{}, err
		}
		resp.FellBack = true
		if r.Log != nil {
			if lerr := r.Log(ctx, req.Task, resp, rt, req.FileID, req.MatterID); lerr != nil {
				return Response{}, lerr
			}
		}
		return resp, nil
	}
	if r.Log != nil {
		if err := r.Log(ctx, req.Task, resp, Route{}, req.FileID, req.MatterID); err != nil {
			return Response{}, err
		}
	}
	return resp, nil
}

func (r *Router) callRoute(ctx context.Context, rt Route, req Request) (Response, error) {
	base, err := r.baseURL(ctx, rt.Provider)
	if err != nil {
		return Response{}, err
	}
	key, err := r.key(ctx, rt.Provider)
	if err != nil {
		return Response{}, err
	}
	ctx, cancel, err := r.withTimeout(ctx)
	if err != nil {
		return Response{}, err
	}
	defer cancel()
	var resp Response
	switch {
	case req.Task == TaskTranscription:
		resp, err = r.transcribe(ctx, base, key, rt, req)
	case rt.Provider == ProviderAnthropic:
		resp, err = r.anthropic(ctx, base, key, rt, req)
	default:
		resp, err = r.chat(ctx, base, key, rt, req)
	}
	resp.Provider, resp.Model = rt.Provider, rt.Model
	return resp, err
}

func dataURL(p Part) string {
	return "data:" + p.MediaType + ";base64," + base64.StdEncoding.EncodeToString(p.Data)
}

// chat speaks the OpenAI chat completions wire (OpenAI, compatible and local endpoints).
func (r *Router) chat(ctx context.Context, base, key string, rt Route, req Request) (Response, error) {
	var content []map[string]any
	for _, p := range req.Parts {
		switch {
		case p.Data == nil:
			content = append(content, map[string]any{"type": "text", "text": p.Text})
		case strings.HasPrefix(p.MediaType, "image/"):
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": dataURL(p)}})
		default:
			content = append(content, map[string]any{"type": "file", "file": map[string]any{"filename": p.Name, "file_data": dataURL(p)}})
		}
	}
	body := map[string]any{
		"model": rt.Model,
		"messages": []map[string]any{
			{"role": "system", "content": req.System},
			{"role": "user", "content": content},
		},
	}
	if req.JSON {
		body["response_format"] = map[string]any{"type": "json_object"}
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := r.postJSON(ctx, base+"/chat/completions", rt.Provider, key, body, &out); err != nil {
		return Response{}, err
	}
	if len(out.Choices) == 0 {
		return Response{}, errors.New("model returned no choices")
	}
	return Response{Text: out.Choices[0].Message.Content, PromptTokens: out.Usage.PromptTokens, CompletionTokens: out.Usage.CompletionTokens}, nil
}

// anthropic speaks the Anthropic messages wire.
func (r *Router) anthropic(ctx context.Context, base, key string, rt Route, req Request) (Response, error) {
	var content []map[string]any
	for _, p := range req.Parts {
		src := map[string]any{"type": "base64", "media_type": p.MediaType, "data": base64.StdEncoding.EncodeToString(p.Data)}
		switch {
		case p.Data == nil:
			content = append(content, map[string]any{"type": "text", "text": p.Text})
		case strings.HasPrefix(p.MediaType, "image/"):
			content = append(content, map[string]any{"type": "image", "source": src})
		default:
			content = append(content, map[string]any{"type": "document", "source": src})
		}
	}
	system := req.System
	if req.JSON {
		system += "\nAnswer with one JSON object and nothing else."
	}
	body := map[string]any{
		"model": rt.Model, "max_tokens": wire().AnthropicMaxTokens, "system": system,
		"messages": []map[string]any{{"role": "user", "content": content}},
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := r.postJSON(ctx, base+"/messages", rt.Provider, key, body, &out); err != nil {
		return Response{}, err
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return Response{Text: b.String(), PromptTokens: out.Usage.InputTokens, CompletionTokens: out.Usage.OutputTokens}, nil
}

// transcribe speaks the OpenAI audio transcription wire with segment timestamps.
func (r *Router) transcribe(ctx context.Context, base, key string, rt Route, req Request) (Response, error) {
	var audio Part
	for _, p := range req.Parts {
		if p.Data != nil {
			audio = p
		}
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", audio.Name)
	if err != nil {
		return Response{}, err
	}
	if _, err := fw.Write(audio.Data); err != nil {
		return Response{}, err
	}
	for k, v := range map[string]string{"model": rt.Model, "response_format": "verbose_json", "timestamp_granularities[]": "segment"} {
		if err := mw.WriteField(k, v); err != nil {
			return Response{}, err
		}
	}
	if err := mw.Close(); err != nil {
		return Response{}, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/audio/transcriptions", &buf)
	if err != nil {
		return Response{}, err
	}
	hreq.Header.Set("Content-Type", mw.FormDataContentType())
	r.auth(hreq, rt.Provider, key)
	var out struct {
		Text     string    `json:"text"`
		Duration float64   `json:"duration"`
		Segments []Segment `json:"segments"`
		Usage    struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := r.do(hreq, &out); err != nil {
		return Response{}, err
	}
	return Response{Text: out.Text, Segments: out.Segments, Duration: out.Duration, PromptTokens: out.Usage.InputTokens, CompletionTokens: out.Usage.OutputTokens}, nil
}

func (r *Router) postJSON(ctx context.Context, url, provider, key string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	r.auth(req, provider, key)
	return r.do(req, out)
}

func (r *Router) do(req *http.Request, out any) error {
	resp, err := r.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, wire().ErrorBodyBytes))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

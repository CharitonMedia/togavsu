package core

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/doctender"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// aiProvider is one provider on the AI and inference tab (PRD 8.7) and the settings
// that hold its key and endpoint. The contract names the OpenAI-compatible endpoint
// openai_compatible; the router calls it compatible.
type aiProvider struct {
	ID      string // contract id
	Router  string // doctender provider id
	KeyKey  string // secret setting holding the key
	BaseKey string // setting holding the base URL; empty when the public endpoint is fixed
}

var aiProviders = []aiProvider{
	{ID: "anthropic", Router: doctender.ProviderAnthropic, KeyKey: "ai.anthropic_api_key"},
	{ID: "openai", Router: doctender.ProviderOpenAI, KeyKey: "ai.openai_api_key"},
	{ID: "openai_compatible", Router: doctender.ProviderCompatible, KeyKey: "ai.compatible_api_key", BaseKey: "ai.compatible_base_url"},
	{ID: "local", Router: doctender.ProviderLocal, KeyKey: "ai.local_api_key", BaseKey: "ai.local_base_url"},
}

func aiProviderOf(id string) (aiProvider, error) {
	for _, p := range aiProviders {
		if p.ID == id {
			return p, nil
		}
	}
	return aiProvider{}, domain.NotFound("provider")
}

func (a *App) aiProviderRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/ai/providers", a.listAIProvidersJSON)
	m.HandleFunc("PUT /api/v1/ai/providers/{provider}/key", a.putAIProviderKeyJSON)
	m.HandleFunc("POST /api/v1/ai/providers/{provider}/check", a.checkAIProviderJSON)
}

// aiProviderJSON is the contract's AiProvider: whether a key (or, for the local
// endpoint, a URL) is set and the key's last four characters. The key never leaves.
func (a *App) aiProviderJSON(ctx context.Context, pr aiProvider) (map[string]any, error) {
	key, err := a.Settings.Get(ctx, pr.KeyKey)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"provider": pr.ID, "configured": key != ""}
	if len(key) >= domain.KeyHintMinChars {
		out["last4"] = key[len(key)-4:]
	}
	if pr.BaseKey != "" {
		base, err := a.Settings.Get(ctx, pr.BaseKey)
		if err != nil {
			return nil, err
		}
		if base != "" {
			out["base_url"] = base
		}
		out["configured"] = base != "" && (key != "" || pr.ID == "local")
	}
	return out, nil
}

func (a *App) listAIProvidersJSON(w http.ResponseWriter, r *http.Request) {
	if _, err := a.admin(r); err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(aiProviders))
	for _, pr := range aiProviders {
		row, err := a.aiProviderJSON(r.Context(), pr)
		if err != nil {
			writeErr(w, err)
			return
		}
		data = append(data, row)
	}
	limit, err := a.intSetting(r.Context(), "api.page_size")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "page": map[string]any{"limit": limit, "next_cursor": nil}})
}

// checkBaseURL accepts an http or https URL with a host and nothing after the path.
func checkBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return domain.Invalid("base_url", "base_url is an http or https URL with a host")
	}
	return nil
}

// putAIProviderKeyJSON stores a provider's key through the settings registry, which seals
// it and keeps its value out of the audit log, and the endpoint's base URL where the
// provider has one. The answer and the setting.updated event never carry the key.
func (a *App) putAIProviderKeyJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.admin(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	pr, err := aiProviderOf(r.PathValue("provider"))
	if err != nil {
		writeErr(w, err)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeErr(w, domain.Invalid("body", "the request body is unreadable"))
		return
	}
	var body struct {
		APIKey  string  `json:"api_key"`
		BaseURL *string `json:"base_url"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeErr(w, domain.Invalid("body", "request body must be {\"api_key\": ..., \"base_url\": ...}"))
		return
	}
	key := strings.TrimSpace(body.APIKey)
	switch {
	case key == "" || strings.ContainsAny(key, " \r\n\t"):
		writeErr(w, domain.Invalid("api_key", "api_key is required and has no spaces"))
		return
	case body.BaseURL != nil && pr.BaseKey == "":
		writeErr(w, domain.Invalid("base_url", pr.ID+" uses its public endpoint; base_url is not accepted"))
		return
	case body.BaseURL != nil:
		if err := checkBaseURL(strings.TrimSpace(*body.BaseURL)); err != nil {
			writeErr(w, err)
			return
		}
	}
	res, _, err := a.idempotentTx(r.Context(), p, "putAiProviderKey/"+pr.ID, r.Header.Get("Idempotency-Key"), raw, func(ctx context.Context) (idemResult, error) {
		// The registry commits each setting in its own transaction; the stored answer and
		// the event commit after both.
		if body.BaseURL != nil {
			if _, err := a.Settings.Set(context.WithoutCancel(ctx), pr.BaseKey, strings.TrimRight(strings.TrimSpace(*body.BaseURL), "/"), p); err != nil {
				return idemResult{}, settingErr(err)
			}
		}
		if _, err := a.Settings.Set(context.WithoutCancel(ctx), pr.KeyKey, key, p); err != nil {
			return idemResult{}, settingErr(err)
		}
		if err := a.publish(ctx, p, "setting.updated", "setting", domain.ID(pr.KeyKey), "Set the "+pr.ID+" AI provider key"); err != nil {
			return idemResult{}, err
		}
		out, err := a.aiProviderJSON(ctx, pr)
		return idemResult{Status: http.StatusOK, Body: out}, err
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, res.Status, res.Body)
}

// checkAIProviderJSON is the Provider check button: the router's health check against
// the provider's model list with the stored key. A failed check is ok false with a
// detail that never carries the key; it is recorded as ai.provider_checked either way.
func (a *App) checkAIProviderJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.admin(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	pr, err := aiProviderOf(r.PathValue("provider"))
	if err != nil {
		writeErr(w, err)
		return
	}
	res, _, err := a.idempotentTx(r.Context(), p, "checkAiProvider/"+pr.ID, r.Header.Get("Idempotency-Key"), nil, func(ctx context.Context) (idemResult, error) {
		out := map[string]any{"ok": true}
		if err := a.aiRouter().Health(ctx, pr.Router); err != nil {
			detail := err.Error()
			if key, kerr := a.Settings.Get(ctx, pr.KeyKey); kerr == nil && key != "" {
				detail = strings.ReplaceAll(detail, key, "[redacted]")
			}
			out = map[string]any{"ok": false, "detail": detail}
		}
		summary := "Checked the " + pr.ID + " AI provider: ok"
		if out["ok"] == false {
			summary = "Checked the " + pr.ID + " AI provider: failed"
		}
		if _, err := a.Audit.Record(ctx, p, audit.Change{Entity: "ai_provider", EntityID: domain.ID(pr.ID), Field: "check", New: summary, Reason: "ai.provider_checked"}); err != nil {
			return idemResult{}, err
		}
		if err := a.publish(ctx, p, "ai.provider_checked", "ai_provider", domain.ID(pr.ID), summary); err != nil {
			return idemResult{}, err
		}
		return idemResult{Status: http.StatusOK, Body: out}, nil
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, res.Status, res.Body)
}

// aiRouter is a router on the live settings and this core's provider endpoints, the one
// Doc Tender and module AI calls use.
func (a *App) aiRouter() *doctender.Router {
	return &doctender.Router{Settings: a.Settings, BaseURLs: a.providerURLs}
}

// publish appends one event to core.events in the transaction on ctx.
func (a *App) publish(ctx context.Context, p domain.Principal, action, recordType string, recordID domain.ID, summary string) error {
	id, err := domain.NewID()
	if err != nil {
		return err
	}
	actor := p.UserID
	if actor.IsZero() {
		actor = domain.SystemUserID
	}
	now := time.Now().UTC()
	return db.PublishEvent(ctx, audit.Conn(ctx, a.Pool), domain.Event{
		Meta:      domain.Meta{ID: id, CreatedAt: now, UpdatedAt: now, CreatedBy: actor},
		ActorKind: eventActorKind(p), ActorID: actor, Action: action, RecordType: recordType, RecordID: recordID, Summary: summary,
	})
}

package modules

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/doctender"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// AI mediation (PRD 8.7): a module asks the core to run an AI call under ai:use and
// never holds a provider key. The call runs on the AI tab's providers and models, with
// the per-task override and the secondary-provider fallback, through the same router Doc
// Tender uses, and is written to the AI call log with the module that asked.

// AICallInput is the API contract's AiCallRequest.
type AICallInput struct {
	Task     string         `json:"task"`
	Input    map[string]any `json:"input"`
	MatterID *domain.ID     `json:"matter_id"`
}

// AIAttempt is one provider attempt of a call.
type AIAttempt struct {
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Status       string `json:"status"`
	TokensIn     int    `json:"tokens_in"`
	TokensOut    int    `json:"tokens_out"`
	CostMicroUSD int64  `json:"cost_micro_usd"`
}

// AICall is the API contract's AiCall.
type AICall struct {
	ID           domain.ID      `json:"id"`
	CreatedAt    time.Time      `json:"created_at"`
	CreatedBy    string         `json:"created_by"`
	Task         string         `json:"task"`
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	TokensIn     int            `json:"tokens_in"`
	TokensOut    int            `json:"tokens_out"`
	CostMicroUSD int64          `json:"cost_micro_usd"`
	FellBack     bool           `json:"fell_back"`
	Attempts     []AIAttempt    `json:"attempts"`
	Output       map[string]any `json:"output,omitempty"`
	Requester    string         `json:"requester"`
}

// AIRouter runs one model call; doctender.Router is the production one. Tests point it
// at local stand-in providers through its BaseURLs.
type AIRouter interface {
	Call(ctx context.Context, req doctender.Request) (doctender.Response, error)
}

// router builds a Doc Tender router whose call-log rows carry the module and user.
func (s *Service) router(p domain.Principal, logged *[]AIAttempt, ids *domain.ID) *doctender.Router {
	r := &doctender.Router{Settings: s.d.Settings, HTTP: s.d.HTTP, BaseURLs: s.AIBaseURLs}
	r.Log = func(ctx context.Context, task string, resp doctender.Response, fallback doctender.Route, fileID, matterID domain.ID) error {
		id, err := domain.NewID()
		if err != nil {
			return err
		}
		*ids = id
		at := s.now().UnixMicro()
		fell := zero
		if resp.FellBack {
			fell = one
			*logged = append(*logged, AIAttempt{Provider: fallback.Provider, Model: fallback.Model, Status: "failed"})
		}
		*logged = append(*logged, AIAttempt{Provider: resp.Provider, Model: resp.Model, Status: "succeeded", TokensIn: resp.PromptTokens, TokensOut: resp.CompletionTokens})
		var user, matter any
		if !p.UserID.IsZero() {
			user = string(p.UserID)
		}
		if !matterID.IsZero() {
			matter = string(matterID)
		}
		createdBy := string(p.UserID)
		if createdBy == "" {
			createdBy = p.ModuleID
		}
		return s.appendAICall(ctx, []any{string(id), at, at, createdBy, task, resp.Provider, resp.Model, resp.PromptTokens, resp.CompletionTokens,
			int64(0), fell, fallback.Provider, fallback.Model, user, p.ModuleID, matter})
	}
	return r
}

const aiWriterRole = "togavsu_ai_log_writer"

// installAILog makes the AI call log append-only for the role that writes module calls:
// insert and select, no update, delete or truncate (docs/architecture/contracts.md).
func (s *Service) installAILog(ctx context.Context) error {
	for _, st := range []string{
		`DO $body$ BEGIN
		  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '` + aiWriterRole + `') THEN
		    CREATE ROLE ` + aiWriterRole + ` NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE;
		  END IF;
		END $body$`,
		`GRANT ` + aiWriterRole + ` TO CURRENT_USER`,
		`GRANT USAGE ON SCHEMA core TO ` + aiWriterRole,
		`GRANT SELECT, INSERT ON TABLE core.ai_call_log TO ` + aiWriterRole,
		`REVOKE UPDATE, DELETE, TRUNCATE, TRIGGER ON TABLE core.ai_call_log FROM ` + aiWriterRole,
		`REVOKE UPDATE, DELETE, TRUNCATE ON TABLE core.ai_call_log FROM PUBLIC`,
	} {
		if _, err := s.d.DB.ExecContext(ctx, st); err != nil {
			return err
		}
	}
	return nil
}

// appendAICall inserts one call-log row as the append-only writer role.
func (s *Service) appendAICall(ctx context.Context, args []any) error {
	s.aiOnce.Do(func() { s.aiErr = s.installAILog(ctx) })
	if s.aiErr != nil {
		return s.aiErr
	}
	tx, err := s.d.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE `+aiWriterRole); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO core.ai_call_log (id, created_at, updated_at, created_by, task, provider, model, prompt_tokens, completion_tokens,
cost_cents, fell_back, fallback_provider, fallback_model, user_id, module_id, matter_id, file_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,NULL)`, args...); err != nil {
		return err
	}
	return tx.Commit()
}

// CallAI runs a module's (or a user's) AI call. p is the module principal, delegated to a
// viewer when the module passed a context token; scopes are the token's scopes (nil for
// a session). The task must be one the module declares; a matter is checked against p
// before any provider is called.
func (s *Service) CallAI(ctx context.Context, p domain.Principal, scopes []string, bearer bool, in AICallInput) (AICall, error) {
	if (bearer || p.ModuleID != "") && !hasScope(scopes, "ai:use") {
		return AICall{}, domain.Forbidden("AI calls need the ai:use permission")
	}
	task := strings.TrimSpace(in.Task)
	if task == "" {
		return AICall{}, invalid("task", "task is required")
	}
	requester := ""
	switch {
	case p.ModuleID != "":
		mod, err := s.Installed(ctx, p.ModuleID)
		if err != nil || !mod.Enabled {
			return AICall{}, domain.Forbidden("the module isn't enabled")
		}
		declared := false
		for _, t := range mod.Manifest.AITasks {
			declared = declared || t.ID == task
		}
		if !declared {
			return AICall{}, invalid("task", "module "+p.ModuleID+" doesn't declare AI task "+task)
		}
		requester = "module:" + p.ModuleID
		task = p.ModuleID + "." + task
	case !p.UserID.IsZero() && (p.HasRole(domain.RoleAdmin) || p.HasRole(domain.RoleAttorney) || p.HasRole(domain.RoleStaff)):
		requester = "user:" + string(p.UserID)
	default:
		return AICall{}, domain.Forbidden("AI calls are for staff and modules")
	}
	var matter domain.ID
	if in.MatterID != nil && !in.MatterID.IsZero() {
		matter = *in.MatterID
		dec, err := s.canReadMatter(ctx, p, matter)
		if err != nil {
			return AICall{}, err
		}
		if !dec.Allow {
			return AICall{}, domain.NotFound("matter")
		}
	}
	prompt, system, err := aiPrompt(in.Input)
	if err != nil {
		return AICall{}, err
	}
	var attempts []AIAttempt
	var logID domain.ID
	r := s.router(p, &attempts, &logID)
	resp, err := r.Call(ctx, doctender.Request{Task: task, System: system, Parts: []doctender.Part{{Text: prompt}}, MatterID: matter})
	if err != nil {
		if errors.Is(err, doctender.ErrNoAI) {
			return AICall{}, domain.Conflict("no AI provider is set up on the AI tab")
		}
		return AICall{}, &domain.APIError{Code: "ai_unavailable", Message: "the AI call failed on every provider: " + scrub(err.Error(), s.d.Settings, ctx)}
	}
	out := AICall{ID: logID, CreatedAt: s.now(), CreatedBy: string(p.UserID), Task: task, Provider: resp.Provider, Model: resp.Model,
		TokensIn: resp.PromptTokens, TokensOut: resp.CompletionTokens, FellBack: resp.FellBack, Attempts: attempts,
		Output: map[string]any{"text": resp.Text}, Requester: requester}
	if out.CreatedBy == "" {
		out.CreatedBy = p.ModuleID
	}
	return out, nil
}

// scrub removes provider keys from an error message so they never reach a module.
func scrub(msg string, st Settings, ctx context.Context) string {
	for _, k := range []string{"ai.openai_api_key", "ai.anthropic_api_key", "ai.compatible_api_key"} {
		if v, err := st.Get(ctx, k); err == nil && v != "" {
			msg = strings.ReplaceAll(msg, v, "[redacted]")
		}
	}
	return msg
}

func aiPrompt(input map[string]any) (string, string, error) {
	prompt, _ := input["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		if raw, err := json.Marshal(input); err == nil && len(input) > 0 {
			prompt = string(raw)
		}
	}
	if strings.TrimSpace(prompt) == "" {
		return "", "", invalid("input", "input.prompt is required")
	}
	system, _ := input["system"].(string)
	return prompt, system, nil
}

// AICalls lists the AI call log for admins, oldest first, with each call's attempts.
func (s *Service) AICalls(ctx context.Context, p domain.Principal, module string) ([]AICall, error) {
	if err := requireAdmin(p); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT id, created_at, created_by, task, provider, model, prompt_tokens, completion_tokens, fell_back,
fallback_provider, fallback_model, COALESCE(module_id, ''), COALESCE(user_id, '') FROM core.ai_call_log WHERE ($1 = '' OR module_id = $1) ORDER BY created_at, id`, module)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AICall{}
	for rows.Next() {
		var c AICall
		var at int64
		var fell int
		var fbProvider, fbModel, mod, user string
		if err := rows.Scan(&c.ID, &at, &c.CreatedBy, &c.Task, &c.Provider, &c.Model, &c.TokensIn, &c.TokensOut, &fell, &fbProvider, &fbModel, &mod, &user); err != nil {
			return nil, err
		}
		c.CreatedAt, c.FellBack = time.UnixMicro(at).UTC(), fell == one
		if c.FellBack {
			c.Attempts = append(c.Attempts, AIAttempt{Provider: fbProvider, Model: fbModel, Status: "failed"})
		}
		c.Attempts = append(c.Attempts, AIAttempt{Provider: c.Provider, Model: c.Model, Status: "succeeded", TokensIn: c.TokensIn, TokensOut: c.TokensOut})
		switch {
		case mod != "":
			c.Requester = "module:" + mod
		case user != "":
			c.Requester = "user:" + user
		default:
			c.Requester = "system"
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

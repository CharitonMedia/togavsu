package core

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/CharitonMedia/togavsu/internal/doctender"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// aiCallLogPanel is the AI call log section of the AI and inference admin tab (PRD 8.7,
// AC-052): one page of the log, newest first, filtered by task and provider.
type aiCallLogPanel struct {
	Calls     []modules.AICall
	Task      string
	Provider  string
	Tasks     []string
	Providers []string
	// Older links to the next page; empty on the last page.
	Older string
}

// aiCallPanel reads the panel through the module runtime's AICalls, the same service
// call GET /api/v1/ai/calls makes, so the admin role and matter walls are checked there.
// The page size is api.page_size.
func aiCallPanel(r *http.Request, p domain.Principal, svc *modules.Service, reg *settings.Registry) (any, error) {
	q := r.URL.Query()
	raw, err := reg.Get(r.Context(), "api.page_size")
	if err != nil {
		return nil, err
	}
	limit, err := strconv.Atoi(raw)
	if err != nil {
		return nil, err
	}
	v := aiCallLogPanel{Task: q.Get("task"), Provider: q.Get("provider"),
		Tasks: []string{doctender.TaskOCR, doctender.TaskPhoto, doctender.TaskGrouping, doctender.TaskTranscription,
			doctender.TaskLabeling, doctender.TaskCategorizing, "summaries"},
		Providers: []string{doctender.ProviderOpenAI, doctender.ProviderAnthropic, doctender.ProviderCompatible, doctender.ProviderLocal}}
	calls, next, err := svc.AICalls(r.Context(), p, modules.AICallQuery{Task: v.Task, Provider: v.Provider, Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		return nil, err
	}
	v.Calls = calls
	if next != "" {
		older := url.Values{"tab": {settings.TabAI}, "cursor": {next}}
		if v.Task != "" {
			older.Set("task", v.Task)
		}
		if v.Provider != "" {
			older.Set("provider", v.Provider)
		}
		v.Older = "/admin?" + older.Encode()
	}
	return v, nil
}

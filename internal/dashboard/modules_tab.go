package dashboard

import (
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// ModulesAdmin backs the Modules and marketplace admin tab (PRD 8.7, 9.3, 10.5): the
// installed modules with install, configure, permissions, enable, disable, update and
// remove; which module replaces time, trust or calendar; trusted local signing keys;
// marketplace browsing and alerts. The core supplies it from the module runtime and the
// marketplace component, whose services check the admin role, so the tab, the API and
// MCP give the same answer.
type ModulesAdmin interface {
	// Panel is the tab's data for p (rendered by admin.html under .Modules).
	Panel(r *http.Request, p domain.Principal) (any, error)
	// Act runs one tab action and returns the notice to show.
	Act(r *http.Request, p domain.Principal, action string) (string, error)
}

// ModulesActionPrefix is where the tab's forms post. Its install and update forms are
// multipart (a module package file); the body is capped before sign-in reads the CSRF
// field, as for the profile picture.
const ModulesActionPrefix = "/admin/modules/"

// ModulesUploadBytes caps a tab form's body: the largest signed package file plus room
// for the form fields.
const ModulesUploadBytes = 64 << 20

func (h *Handler) modulesRoutes(m *http.ServeMux) {
	m.HandleFunc("POST "+ModulesActionPrefix+"{action}", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, ModulesUploadBytes)
		h.form(func(r *http.Request, p domain.Principal) (string, string, error) {
			back := "/admin?tab=" + settings.TabModules
			if err := admin(p); err != nil {
				return back, "", err
			}
			if h.Modules == nil {
				return back, "", domain.NotFound("modules")
			}
			notice, err := h.Modules.Act(r, p, r.PathValue("action"))
			return back, notice, err
		})(w, r)
	})
}

package dashboard

import (
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// MigrationAdmin backs the Migration import admin tab (CORE-37 / PRD 8.22).
type MigrationAdmin interface {
	// Panel is optional preview/notice data for the tab.
	Panel(r *http.Request, p domain.Principal) (any, error)
	// Act runs opening-invoice or CSV import and returns a notice.
	Act(r *http.Request, p domain.Principal, action string) (string, error)
}

// MigrationActionPrefix is where the Migration import forms post.
const MigrationActionPrefix = "/admin/migration/"

func (h *Handler) migrationRoutes(m *http.ServeMux) {
	m.HandleFunc("POST "+MigrationActionPrefix+"{action}", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
		h.form(func(r *http.Request, p domain.Principal) (string, string, error) {
			back := "/admin?tab=" + settings.TabMigration
			if err := admin(p); err != nil {
				return back, "", err
			}
			if h.Migration == nil {
				return back, "", domain.NotFound("migration import")
			}
			notice, err := h.Migration.Act(r, p, r.PathValue("action"))
			return back, notice, err
		})(w, r)
	})
}

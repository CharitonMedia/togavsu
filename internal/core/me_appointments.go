package core

import (
	"net/http"
	"time"

	"github.com/CharitonMedia/togavsu/internal/calendar"
)

// meAppointmentsRoutes mounts CORE-15 client appointments.
func (a *App) meAppointmentsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/me/appointments", a.getMeAppointmentsJSON)
}

func (a *App) getMeAppointmentsJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.principal(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := a.Calendar.ListClientAppointments(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, clientAppointmentJSON(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"page": map[string]any{"next_cursor": nil},
	})
}

func clientAppointmentJSON(row calendar.ClientAppointment) map[string]any {
	return map[string]any{
		"title":     row.Title,
		"kind":      row.Kind,
		"start":     row.Start.UTC().Format(time.RFC3339),
		"end":       row.End.UTC().Format(time.RFC3339),
		"all_day":   row.AllDay,
		"location":  row.Location,
		"matter_id": string(row.MatterID),
	}
}

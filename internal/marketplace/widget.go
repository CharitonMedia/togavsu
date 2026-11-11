package marketplace

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// WidgetSource is the module-alerts home-screen widget (PRD 8.6: admin; module alerts on
// the admin dashboard). Its title opens the Modules and marketplace tab; each row opens
// that tab at the alert. The service's admin check applies, so another role gets an
// error rather than the alerts. Compose it as Sources()["module-alerts"].
func (s *Service) WidgetSource() dashboard.Source {
	return func(ctx context.Context, p domain.Principal, _ map[string]string) (dashboard.Widget, error) {
		as, err := s.Alerts(ctx, p)
		if err != nil {
			return dashboard.Widget{}, err
		}
		w := dashboard.Widget{ID: "module-alerts", Title: "Module alerts", PagePath: AlertsLink,
			Target: dashboard.Target{Path: "/admin", Query: map[string]string{"tab": "modules"}}, Rows: []dashboard.WidgetRow{}}
		for _, a := range as {
			if len(w.Rows) >= fixed.WidgetRows {
				break
			}
			title, _, _ := alertText(a)
			w.Rows = append(w.Rows, dashboard.WidgetRow{ID: string(a.ID), Title: title, Subtitle: a.Warning, TargetPath: AlertsLink + "#alert-" + string(a.ID)})
		}
		return w, nil
	}
}

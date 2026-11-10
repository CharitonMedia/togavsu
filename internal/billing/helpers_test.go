package billing

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

func fmtSscan(s string, v *int) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(strings.SplitN(s, "\n", 2)[0]))
	*v = n
	return n, err
}
func itoa(n int) string { return strconv.Itoa(n) }

type settingOverride map[string]string

type quiet struct{}

func (quiet) SettingChanged(context.Context, settings.Change) error { return nil }

// overrideSettings wires a real registry on a memory store with the given values set by an admin.
func overrideSettings(s *Service, values settingOverride) func() {
	reg, err := settings.NewWithClock(settings.NewMemory(), quiet{}, time.Now)
	if err != nil {
		panic(err)
	}
	admin := domain.Principal{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
	for k, v := range values {
		if _, err := reg.Set(context.Background(), k, v, admin); err != nil {
			panic(k + ": " + err.Error())
		}
	}
	old := s.d.Settings
	s.d.Settings = reg
	return func() { s.d.Settings = old }
}

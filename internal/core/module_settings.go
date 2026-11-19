package core

import (
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
	"net/http"
)

func (a *App) moduleContractSetting(r *http.Request, p domain.Principal, key string) (settings.Entry, error) {
	c, err := a.caller(r)
	if err != nil {
		return settings.Entry{}, err
	}
	if err := c.RequireScopes("settings:read"); err != nil {
		return settings.Entry{}, shared(err)
	}
	if key != "modules.contract_call_max_bytes" {
		return settings.Entry{}, domain.Forbidden("only module contract runtime settings may be read")
	}
	in, err := a.ext.Modules.Installed(r.Context(), p.ModuleID)
	if err != nil {
		return settings.Entry{}, err
	}
	if !in.Enabled || (!in.Approved["contracts:call"] && !in.Approved["contracts:provide"]) {
		return settings.Entry{}, domain.Forbidden("module is not bound by a contract")
	}
	return a.entry(r.Context(), key)
}

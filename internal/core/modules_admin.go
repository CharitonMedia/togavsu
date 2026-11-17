package core

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// modulesAdmin is the Modules and marketplace admin tab (dashboard.ModulesAdmin) over the
// module runtime and the marketplace component. Every call goes through those services,
// which require an admin, so the tab can do nothing the API would refuse.
type modulesAdmin struct{ x *extensions }

// adminModule is one installed module on the tab.
type adminModule struct {
	modules.View
	Replaces  string
	Undecided bool
	Settings  []modules.SettingValue
}

// marketListing is one browsable marketplace listing on the tab, with the install /
// update / installed state for the firm's current modules.
type marketListing struct {
	marketplace.Listing
	// Action is install, update, installed or installed_newer.
	Action           string
	InstalledVersion string
}

// modulesPanel is the tab's data.
type modulesPanel struct {
	Modules     []adminModule
	Domains     []modules.DomainHandler
	Candidates  map[string][]string
	Keys        []marketplace.LocalKey
	Alerts      []marketplace.ModuleAlert
	Check       marketplace.CheckStatus
	CheckError  string
	Query       string
	Listings    []marketListing
	BrowseError string
	// Address is modules.marketplace_address as stored.
	Address string
	// Unreachable is true when the address is the placeholder, refused or the
	// marketplace could not be reached; the card shows the settings link instead of
	// raw errors.
	Unreachable bool
	// ComposeHTTP is true when Address is plain http to togavsu-marketplace.
	ComposeHTTP bool
}

// Panel opens the tab. Opening it runs the marketplace flag and pull check (PRD 10.5);
// a marketplace that can't be reached is reported on the tab, not as a failed page.
func (m modulesAdmin) Panel(r *http.Request, p domain.Principal) (any, error) {
	ctx := r.Context()
	var v modulesPanel
	alerts, status, err := m.x.Market.OpenModulesScreen(ctx, p)
	if err != nil {
		if isForbidden(err) {
			return nil, err
		}
		v.CheckError = err.Error()
		if alerts, err = m.x.Market.Alerts(ctx, p); err != nil {
			return nil, err
		}
		if status, err = m.x.Market.Status(ctx, p); err != nil {
			return nil, err
		}
	}
	v.Alerts, v.Check = alerts, status
	views, err := m.x.Modules.List(ctx, p)
	if err != nil {
		return nil, err
	}
	v.Candidates = map[string][]string{}
	installed := map[string]string{}
	for _, mv := range views {
		row := adminModule{View: mv}
		if mv.Replaces != nil {
			row.Replaces = *mv.Replaces
			if mv.Status == modules.StatusInstalled && mv.Enabled {
				v.Candidates[row.Replaces] = append(v.Candidates[row.Replaces], mv.ID)
			}
		}
		for _, perm := range mv.Permissions {
			if perm.Decision == "pending" {
				row.Undecided = true
			}
		}
		if mv.Status == modules.StatusInstalled || mv.Status == modules.StatusDisabled {
			if row.Settings, err = m.x.Modules.ModuleSettings(ctx, p, nil, false, mv.ID); err != nil {
				return nil, err
			}
		}
		if mv.Status != modules.StatusRemoved && mv.Status != modules.StatusCancelled {
			installed[mv.ID] = mv.Version
		}
		v.Modules = append(v.Modules, row)
	}
	if v.Domains, err = m.x.Modules.DomainHandlers(ctx, p); err != nil {
		return nil, err
	}
	if v.Keys, err = m.x.Market.Keys(ctx, p); err != nil {
		return nil, err
	}
	v.Query = strings.TrimSpace(r.URL.Query().Get("q"))
	v.Address = strings.TrimSpace(m.x.Market.Address(ctx))
	v.ComposeHTTP = marketplace.IsComposeMarketplaceHTTP(v.Address)
	if _, err := marketplace.BaseURL(v.Address); err != nil {
		v.Unreachable = true
		return v, nil
	}
	ls, err := m.x.Market.Listings(ctx, p, v.Query)
	if err != nil {
		if isForbidden(err) {
			return nil, err
		}
		if marketplaceUnreachable(err) {
			v.Unreachable = true
		} else {
			v.BrowseError = browsePlain(err)
		}
		return v, nil
	}
	for _, l := range ls {
		row := marketListing{Listing: l, Action: "install"}
		if ver, ok := installed[l.ID]; ok {
			row.InstalledVersion = ver
			switch {
			case ver == l.Version:
				row.Action = "installed"
			case marketplace.NewerVersion(l.Version, ver):
				row.Action = "update"
			default:
				// Installed version is newer than the listed one: never offer a downgrade.
				row.Action = "installed_newer"
			}
		}
		v.Listings = append(v.Listings, row)
	}
	return v, nil
}

func marketplaceUnreachable(err error) bool {
	var me *marketplace.Error
	if errors.As(err, &me) {
		return me.Code == "marketplace_unconfigured" || me.Code == "unavailable"
	}
	return errors.Is(err, marketplace.ErrNotConfigured) || errors.Is(err, marketplace.ErrUnavailable)
}

func browsePlain(err error) string {
	var me *marketplace.Error
	if errors.As(err, &me) && me.Message != "" {
		return me.Message
	}
	return "The marketplace could not be searched."
}

func isForbidden(err error) bool {
	api, ok := err.(*domain.APIError)
	return ok && api.Code == domain.CodeForbidden
}

// Act runs one tab form.
func (m modulesAdmin) Act(r *http.Request, p domain.Principal, action string) (string, error) {
	ctx := r.Context()
	id := r.FormValue("module_id")
	svc := m.x.Modules
	switch action {
	case "install":
		raw, err := packageFile(r)
		if err != nil {
			return "", err
		}
		v, err := svc.Install(ctx, p, raw)
		if err != nil {
			return "", err
		}
		return v.Name + " is installed and waits for your permission decisions.", nil
	case "install-listing":
		listingID, version := r.FormValue("listing_id"), r.FormValue("version")
		name := listingID
		if l, err := m.x.Market.Listing(ctx, p, listingID); err == nil && l.Name != "" {
			name = l.Name
		}
		v, err := svc.InstallFromMarketplace(ctx, p, listingID, version)
		if err != nil {
			return "", installPlain(name, err)
		}
		return v.Name + " is installed and waits for your permission decisions.", nil
	case "permissions":
		v, err := svc.Get(ctx, p, id)
		if err != nil {
			return "", err
		}
		var ds []modules.Decision
		for _, perm := range v.Permissions {
			if d := r.FormValue("scope:" + perm.Scope); d != "" {
				ds = append(ds, modules.Decision{Scope: perm.Scope, Decision: d})
			}
		}
		v, err = svc.DecidePermissions(ctx, p, id, ds)
		if err != nil {
			return "", err
		}
		if v.Status == modules.StatusCancelled {
			return v.Name + " was not installed: a required permission was declined.", nil
		}
		return v.Name + " permissions saved.", nil
	case "enable":
		v, err := svc.Enable(ctx, p, id)
		if err != nil {
			return "", err
		}
		return v.Name + " is enabled.", nil
	case "disable":
		v, err := svc.Disable(ctx, p, id)
		if err != nil {
			return "", err
		}
		return v.Name + " is disabled.", nil
	case "update":
		var res modules.UpdateResult
		var err error
		name := id
		if ver := strings.TrimSpace(r.FormValue("version")); ver != "" {
			if l, lerr := m.x.Market.Listing(ctx, p, id); lerr == nil && l.Name != "" {
				name = l.Name
			}
			res, err = svc.UpdateFromMarketplace(ctx, p, id, ver)
		} else {
			raw, ferr := packageFile(r)
			if ferr != nil {
				return "", ferr
			}
			res, err = svc.Update(ctx, p, id, raw)
		}
		if err != nil {
			if strings.TrimSpace(r.FormValue("version")) != "" {
				return "", installPlain(name, err)
			}
			return "", err
		}
		return res.Module.Name + " update: " + strings.ReplaceAll(res.Outcome, "_", " ") + ".", nil
	case "uninstall":
		v, err := svc.Uninstall(ctx, p, id, modules.UninstallInput{Data: r.FormValue("data"), Confirmation: r.FormValue("confirmation")})
		if err != nil {
			return "", err
		}
		return v.Name + " is removed.", nil
	case "settings":
		values := map[string]string{}
		for k, vs := range r.PostForm {
			if key, ok := strings.CutPrefix(k, "setting:"); ok && len(vs) > 0 {
				values[key] = vs[0]
			}
		}
		if _, err := svc.SetModuleSettings(ctx, p, id, values); err != nil {
			return "", err
		}
		return "Module settings saved.", nil
	case "domain":
		h, err := svc.SwitchDomain(ctx, p, r.FormValue("domain"), r.FormValue("module_id"))
		if err != nil {
			return "", err
		}
		return h.Domain + " is handled by " + h.Handler + ".", nil
	case "key-add":
		name, pem := r.FormValue("name"), r.FormValue("public_key_pem")
		k, err := m.x.Market.CreateKey(ctx, p, marketplace.KeyInput{Name: &name, PublicKeyPEM: &pem})
		if err != nil {
			return "", err
		}
		return "Trusted key " + k.Name + " added.", nil
	case "key-delete":
		if err := m.x.Market.DeleteKey(ctx, p, domain.ID(r.FormValue("key_id"))); err != nil {
			return "", err
		}
		return "Trusted key removed.", nil
	}
	return "", domain.NotFound("action")
}

// installPlain turns a marketplace or module install failure into one plain sentence
// naming the module, for the tab's alert (never raw error text).
func installPlain(name string, err error) error {
	if name == "" {
		name = "That module"
	}
	msg := name + " could not be installed from the marketplace."
	var me *marketplace.Error
	if errors.As(err, &me) {
		switch me.Code {
		case "yanked":
			msg = name + " was pulled from the marketplace and can't be installed from there."
		case "unsigned_module", "tampered_module":
			msg = name + " failed verification and was not installed."
		case "marketplace_unconfigured", "unavailable":
			msg = name + " could not be installed because the marketplace isn't reachable."
		}
		return &domain.APIError{Code: me.Code, Message: msg}
	}
	var api *domain.APIError
	if errors.As(err, &api) {
		switch api.Code {
		case modules.CodeUnsigned, modules.CodeTampered:
			msg = name + " failed verification and was not installed."
		case domain.CodeConflict:
			msg = name + " is already installed; update it instead."
		default:
			if api.Message != "" && !strings.Contains(api.Message, ":") {
				msg = name + " could not be installed: " + api.Message
			}
		}
		return &domain.APIError{Code: api.Code, Message: msg, Field: api.Field}
	}
	return &domain.APIError{Code: "unavailable", Message: msg}
}

// packageFile reads the form's package upload. The route capped the body already.
func packageFile(r *http.Request) ([]byte, error) {
	f, _, err := r.FormFile("package")
	if err != nil {
		return nil, domain.Invalid("package", "choose the signed module package file")
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, domain.Invalid("package", "the upload is unreadable")
	}
	return raw, nil
}

package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// domainSetting is the admin-page setting that names a domain's replacing module.
var domainSetting = map[string]string{
	DomainTime:     "modules.replace_time",
	DomainTrust:    "modules.replace_trust",
	DomainCalendar: "modules.replace_calendar",
}

// domainPrefixes are the API paths and screens each replaceable domain owns. When a
// module replaces the domain, these requests go to the module (PRD 9.5).
var domainPrefixes = map[string][]string{
	DomainCalendar: {"/api/v1/calendar-events", "/api/v1/deadlines", "/api/v1/calendar/", "/api/v1/ical-feeds", "/ical/",
		"/api/v1/widgets/upcoming-deadlines", "/api/v1/widgets/todays-calendar", "/calendar"},
	DomainTime: {"/api/v1/time-entries", "/api/v1/timers", "/api/v1/expenses", "/api/v1/invoices", "/api/v1/payments",
		"/api/v1/fee-items", "/api/v1/widgets/my-time", "/api/v1/widgets/running-timer", "/api/v1/widgets/unbilled-time", "/billing"},
	DomainTrust: {"/api/v1/trust", "/api/v1/widgets/trust-alerts", "/api/v1/widgets/replenishment-requests", "/trust"},
}

func pathIn(path, prefix string) bool {
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(path, prefix)
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/") || strings.HasPrefix(path, prefix+":") || strings.HasPrefix(path, prefix+"-")
}

// DomainOf names the replaceable domain a path belongs to, or "".
func DomainOf(path string) string {
	for d, prefixes := range domainPrefixes {
		for _, p := range prefixes {
			if pathIn(path, p) {
				return d
			}
		}
	}
	return ""
}

// contractPaths are the core's OpenAPI paths for a domain: what a replacing module must
// implement.
func contractPaths(d string) ([]string, error) {
	var spec struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(api.OpenAPI, &spec); err != nil {
		return nil, err
	}
	var out []string
	for p := range spec.Paths {
		if DomainOf(p) == d {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Replacement returns the module serving domain d, or "" when the core does. A setting
// that names a module that isn't enabled or doesn't declare the domain leaves the core
// serving it.
func (s *Service) Replacement(ctx context.Context, d string) (string, error) {
	key, ok := domainSetting[d]
	if !ok {
		return "", invalid("domain", "time, trust or calendar")
	}
	id, err := s.d.Settings.Get(ctx, key)
	if err != nil || id == "" {
		return "", err
	}
	in, err := s.Installed(ctx, id)
	if err != nil || !in.Enabled || in.Manifest.Replaces != d {
		log.Printf("modules: %s names %q, which isn't an enabled %s replacement; the core serves %s", key, id, d, d)
		return "", nil
	}
	return id, nil
}

func (s *Service) replacing(ctx context.Context, module string) (string, error) {
	for d, key := range domainSetting {
		v, err := s.d.Settings.Get(ctx, key)
		if err != nil {
			return "", err
		}
		if v == module {
			return d, nil
		}
	}
	return "", nil
}

// SwitchDomain hands domain d to module (or back to the core when module is ""). The
// module must be enabled, declare replaces: d and publish an OpenAPI document covering
// every core path of the domain. The switch is a registry setting, so it applies on the
// next request and its old and new values go to the audit log.
func (s *Service) SwitchDomain(ctx context.Context, p domain.Principal, d, module string) error {
	if err := requireAdmin(p); err != nil {
		return err
	}
	key, ok := domainSetting[d]
	if !ok {
		return invalid("domain", "time, trust or calendar")
	}
	if module != "" {
		in, err := s.Installed(ctx, module)
		if err != nil || !in.Enabled {
			return invalid("module", "module "+module+" isn't enabled")
		}
		if in.Manifest.Replaces != d {
			return invalid("module", "module "+module+" doesn't declare replaces: "+d)
		}
		if err := s.checkContract(ctx, module, d); err != nil {
			return err
		}
	}
	if _, err := s.d.Settings.Set(ctx, key, module, p); err != nil {
		return err
	}
	return s.commit(ctx, func(ctx context.Context) error {
		summary := d + " is served by the core again"
		if module != "" {
			summary = d + " is served by module " + module
		}
		return s.event(ctx, p, "module.domain_switched", module, summary, "")
	})
}

// checkContract reads the module's /api/v1/openapi.json and requires every core path of
// the domain with every method the core serves.
func (s *Service) checkContract(ctx context.Context, module, d string) error {
	if s.d.Runtime == nil {
		return domain.Conflict("no module runtime is configured")
	}
	want, err := contractPaths(d)
	if err != nil {
		return err
	}
	var core struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(api.OpenAPI, &core); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.d.Runtime.BaseURL(module), "/")+"/api/v1/openapi.json", nil)
	if err != nil {
		return err
	}
	resp, err := s.d.HTTP.Do(req)
	if err != nil {
		return domain.Conflict("module " + module + " didn't answer its OpenAPI document: " + err.Error())
	}
	defer resp.Body.Close()
	var got struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, limits().MaxPackageBytes)).Decode(&got) != nil {
		return domain.Conflict("module " + module + " has no readable OpenAPI document")
	}
	var missing []string
	for _, p := range want {
		for method := range core.Paths[p] {
			if !httpMethod(method) {
				continue
			}
			if _, ok := got.Paths[p][method]; !ok {
				missing = append(missing, strings.ToUpper(method)+" "+p)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return &domain.APIError{Code: domain.CodeValidation, Field: "module", Message: fmt.Sprintf("module %s doesn't implement the core %s contract: missing %s", module, d, strings.Join(missing, ", "))}
	}
	return nil
}

func httpMethod(m string) bool {
	switch m {
	case "get", "post", "put", "patch", "delete":
		return true
	}
	return false
}

// Principal resolves the caller of a request for the router. Nil means unauthenticated.
type Principal func(*http.Request) (domain.Principal, error)

// Router sends a replaced domain's API calls and screens to the module, and everything
// else to next (PRD 9.5). The module gets a context token for the viewer instead of the
// core session cookie or bearer token. The core's handlers never see the request, so
// the core's own data for the domain is read-only while the module serves it.
func (s *Service) Router(principal Principal, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d := DomainOf(r.URL.Path)
		if d == "" {
			next.ServeHTTP(w, r)
			return
		}
		module, err := s.Replacement(r.Context(), d)
		if err != nil || module == "" {
			if err != nil {
				log.Printf("modules: replacement lookup for %s: %v", d, err)
			}
			next.ServeHTTP(w, r)
			return
		}
		token := ""
		if principal != nil {
			if p, err := principal(r); err == nil && !p.UserID.IsZero() && p.ModuleID == "" {
				ct, err := s.IssueContext(r.Context(), p, module, "")
				if err != nil {
					writeErr(w, err)
					return
				}
				token = ct.Token
			}
		}
		s.proxy(module, "", token).ServeHTTP(w, r)
	})
}

// proxy forwards to the module, stripping the core's credentials. strip removes a path
// prefix (the /m/{id} frame prefix); token, when set, is sent as the context header.
func (s *Service) proxy(module, strip, token string) http.Handler {
	target, err := url.Parse(s.d.Runtime.BaseURL(module))
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "module address is invalid", http.StatusBadGateway)
		})
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			if strip != "" {
				pr.Out.URL.Path = strings.TrimPrefix(pr.Out.URL.Path, strip)
				if pr.Out.URL.Path == "" {
					pr.Out.URL.Path = "/"
				}
				pr.Out.URL.RawPath = ""
			}
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del(ContextHeader)
			pr.Out.Header.Del("X-Csrf-Token")
			if token != "" {
				pr.Out.Header.Set(ContextHeader, token)
			}
			pr.Out.Header.Set("X-Togavsu-Module", module)
		},
		ModifyResponse: func(resp *http.Response) error {
			// A module can't set cookies on the core's domain, and a module page runs in an
			// opaque origin even when opened outside its sandboxed frame, so its script
			// can't use the viewer's core session.
			resp.Header.Del("Set-Cookie")
			if strip != "" || strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
				resp.Header.Add("Content-Security-Policy", "sandbox allow-scripts allow-forms")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			log.Printf("modules: proxy to %s: %v", module, err)
			http.Error(w, "module "+module+" is not answering", http.StatusBadGateway)
		},
	}
	return rp
}

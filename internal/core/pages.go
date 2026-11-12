package core

import (
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/practice"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

var pages = template.Must(template.New("core").Funcs(template.FuncMap{
	"label": tabLabel,
}).Parse(`
{{define "top"}}<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · Togavsu</title><link rel="stylesheet" href="/practice/static/practice.css">
<style>nav.tabs{display:flex;flex-wrap:wrap;gap:6px 14px;margin:0 0 16px}nav.tabs a[aria-current]{font-weight:700;text-decoration:none}
td form{display:inline-block;margin:0 6px 4px 0}ul.card{padding-left:2.2em}header.bar form button{margin-left:12px}</style></head>
<body><header class="bar"><a class="brand" href="/">Togavsu</a>{{if .Nav}}<nav><a href="/practice/matters">Matters</a><a href="/practice/contacts">Contacts</a><a href="/admin/settings">Settings</a>
<form method="post" action="/logout" style="display:inline"><input type="hidden" name="csrf" value="{{.CSRF}}"><button type="submit">Sign out</button></form></nav>{{end}}</header>
<main><h1>{{.Title}}</h1>{{if .Error}}<p class="warn" role="alert">{{.Error}}</p>{{end}}{{end}}
{{define "bottom"}}</main></body></html>{{end}}

{{define "setup"}}{{template "top" .}}
<p>Name the firm and create the first administrator. Two-factor sign-in is set up next.</p>
<form class="card" method="post" action="/setup">
<p><label>Firm name<br><input name="firm_name" required value="{{.Form.firm_name}}"></label></p>
<p><label>Time zone<br><input name="time_zone" required value="{{or .Form.time_zone "America/Chicago"}}"></label></p>
<p><label>Your name<br><input name="admin_name" required value="{{.Form.admin_name}}"></label></p>
<p><label>Email<br><input name="admin_email" type="email" required value="{{.Form.admin_email}}"></label></p>
<p><label>Password<br><input name="password" type="password" required autocomplete="new-password"></label></p>
<p><label>Backup passphrase<br><input name="backup_passphrase" type="password" required autocomplete="new-password"></label></p>
<p><button type="submit">Continue</button></p></form>
{{template "bottom" .}}{{end}}

{{define "enroll"}}{{template "top" .}}
<p>Add this account to your authenticator app, then enter the six-digit code it shows.</p>
<div class="card"><p>Secret: <code>{{.Enroll.Secret}}</code></p><p class="muted"><code>{{.Enroll.OTPAuthURI}}</code></p></div>
<form class="card" method="post" action="/setup/confirm"><input type="hidden" name="enrollment_token" value="{{.Enroll.EnrollmentToken}}">
<p><label>Code<br><input name="code" inputmode="numeric" autocomplete="one-time-code" required></label></p>
<p><button type="submit">Turn on two-factor</button></p></form>
{{template "bottom" .}}{{end}}

{{define "recovery"}}{{template "top" .}}
<p>Two-factor is on. Save these recovery codes now; each works once and they are not shown again.</p>
<ul class="card">{{range .Codes}}<li><code>{{.}}</code></li>{{end}}</ul>
<p><a href="/practice/matters">Continue to matters</a></p>
{{template "bottom" .}}{{end}}

{{define "login"}}{{template "top" .}}
<form class="card" method="post" action="/login">
<p><label>Email<br><input name="email" type="email" required value="{{.Form.email}}" autocomplete="username"></label></p>
<p><label>Password<br><input name="password" type="password" required autocomplete="current-password"></label></p>
<p><button type="submit">Sign in</button></p></form>
{{template "bottom" .}}{{end}}

{{define "totp"}}{{template "top" .}}
<form class="card" method="post" action="/login/totp"><input type="hidden" name="totp_token" value="{{.Token}}">
<p><label>Authenticator code<br><input name="code" inputmode="numeric" autocomplete="one-time-code" required></label></p>
<p><label><input type="checkbox" name="recovery" value="1"> This is a recovery code</label></p>
<p><button type="submit">Verify</button></p></form>
{{template "bottom" .}}{{end}}

{{define "logout"}}{{template "top" .}}
<form class="card" method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}">
<p>Sign out of Togavsu on this browser?</p><p><button type="submit">Sign out</button> <a href="/practice/matters">Cancel</a></p></form>
{{template "bottom" .}}{{end}}

{{define "settings"}}{{template "top" .}}
<nav class="tabs" aria-label="Settings tabs">{{range .Tabs}}<a href="/admin/settings?tab={{.}}" {{if eq . $.Tab}}aria-current="page"{{end}}>{{label .}}</a>{{end}}</nav>
<table><thead><tr><th>Setting</th><th>Value</th><th>Default</th><th></th></tr></thead><tbody>
{{range .Settings}}<tr><td><strong>{{.Key}}</strong><br><span class="muted">{{.Help}}</span></td>
<td>{{if .Secret}}<span class="muted">{{if .SecretSet}}configured{{else}}not set{{end}}</span>{{else}}{{.Value}}{{end}}{{if and (not .Secret) (ne .Value .Default)}} <span class="badge">changed</span>{{end}}</td>
<td>{{if not .Secret}}{{.Default}}{{end}}</td>
<td>{{if .Locked}}<span class="muted">locked</span>{{else}}
<form method="post" action="/admin/settings/{{.Key}}"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="tab" value="{{$.Tab}}">
<input name="value" aria-label="New value for {{.Key}}" {{if not .Secret}}value="{{.Value}}"{{end}} {{if .Secret}}type="password"{{end}}> <button type="submit">Save</button></form>
{{if and (not .Secret) (ne .Value .Default)}}<form method="post" action="/admin/settings/{{.Key}}/reset"><input type="hidden" name="csrf" value="{{$.CSRF}}"><input type="hidden" name="tab" value="{{$.Tab}}"><button type="submit">Reset to default</button></form>{{end}}{{end}}</td></tr>
{{end}}</tbody></table>
{{template "bottom" .}}{{end}}
`))

// tabLabel turns a settings tab key such as "agents_api" into "Agents API".
func tabLabel(tab string) string {
	words := strings.Fields(strings.ReplaceAll(tab, "_", " "))
	for i, w := range words {
		if w == "ai" || w == "api" {
			words[i] = strings.ToUpper(w)
		}
	}
	if len(words) > 0 {
		r, size := utf8.DecodeRuneInString(words[0])
		words[0] = string(unicode.ToUpper(r)) + words[0][size:]
	}
	return strings.Join(words, " ")
}

type view struct {
	Title    string
	Error    string
	Nav      bool
	CSRF     string
	Form     map[string]string
	Enroll   identity.Enrollment
	Codes    []string
	Token    string
	Tabs     []string
	Tab      string
	Settings []settings.Entry
}

func render(w http.ResponseWriter, code int, name string, v view) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := pages.ExecuteTemplate(w, name, v); err != nil {
		_, _ = w.Write([]byte("render error"))
	}
}

func message(err error) string {
	if api, ok := err.(*domain.APIError); ok {
		return api.Message
	}
	return "Something went wrong. Try again."
}

func csrfOf(r *http.Request) string {
	if c, err := r.Cookie(practice.CSRFCookie); err == nil {
		return c.Value
	}
	return ""
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	if _, err := a.principal(r); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/practice/matters", http.StatusSeeOther)
}

func (a *App) setupPage(w http.ResponseWriter, r *http.Request) {
	if ok, _ := a.Ready(r.Context()); ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	render(w, http.StatusOK, "setup", view{Title: "Set up Togavsu", Form: map[string]string{}})
}

func (a *App) setupPost(w http.ResponseWriter, r *http.Request) {
	in := setupBody{FirmName: r.FormValue("firm_name"), TimeZone: r.FormValue("time_zone"), AdminName: r.FormValue("admin_name"),
		AdminEmail: r.FormValue("admin_email"), Password: r.FormValue("password"), BackupPassphrase: r.FormValue("backup_passphrase")}
	e, err := a.Identity.Setup(r.Context(), in.input())
	if err != nil {
		form := map[string]string{"firm_name": in.FirmName, "time_zone": in.TimeZone, "admin_name": in.AdminName, "admin_email": in.AdminEmail}
		render(w, status(err), "setup", view{Title: "Set up Togavsu", Error: message(err), Form: form})
		return
	}
	a.persistBackupPassphrase(in.BackupPassphrase)
	render(w, http.StatusOK, "enroll", view{Title: "Turn on two-factor sign-in", Enroll: e})
}

func (a *App) confirmPost(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("enrollment_token")
	res, err := a.Identity.ConfirmEnrollment(r.Context(), token, r.FormValue("code"))
	if err != nil {
		e, serr := a.Identity.StartEnrollment(r.Context(), token)
		if serr != nil {
			render(w, status(err), "login", view{Title: "Sign in", Error: message(err), Form: map[string]string{}})
			return
		}
		render(w, status(err), "enroll", view{Title: "Turn on two-factor sign-in", Error: message(err), Enroll: e})
		return
	}
	signIn(w, r, res)
	render(w, http.StatusOK, "recovery", view{Title: "Recovery codes", Codes: res.RecoveryCodes})
}

func (a *App) loginPage(w http.ResponseWriter, r *http.Request) {
	render(w, http.StatusOK, "login", view{Title: "Sign in", Form: map[string]string{}})
}

func (a *App) loginPost(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	res, err := a.Identity.Login(r.Context(), email, r.FormValue("password"))
	if err != nil {
		render(w, status(err), "login", view{Title: "Sign in", Error: message(err), Form: map[string]string{"email": email}})
		return
	}
	a.loginStep(w, r, res)
}

func (a *App) loginStep(w http.ResponseWriter, r *http.Request, res identity.LoginResult) {
	switch {
	case res.SessionToken != "":
		signIn(w, r, res)
		http.Redirect(w, r, "/practice/matters", http.StatusSeeOther)
	case res.TOTPToken != "":
		render(w, http.StatusOK, "totp", view{Title: "Two-factor code", Token: res.TOTPToken})
	case res.EnrollmentToken != "":
		e, err := a.Identity.StartEnrollment(r.Context(), res.EnrollmentToken)
		if err != nil {
			render(w, status(err), "login", view{Title: "Sign in", Error: message(err), Form: map[string]string{}})
			return
		}
		render(w, http.StatusOK, "enroll", view{Title: "Turn on two-factor sign-in", Enroll: e})
	default:
		render(w, http.StatusUnauthorized, "login", view{Title: "Sign in", Error: "Sign in did not complete.", Form: map[string]string{}})
	}
}

func (a *App) totpPost(w http.ResponseWriter, r *http.Request) {
	token, code := r.FormValue("totp_token"), r.FormValue("code")
	verify := a.Identity.VerifyTOTP
	if r.FormValue("recovery") != "" {
		verify = a.Identity.VerifyRecovery
	}
	res, err := verify(r.Context(), token, code)
	if err != nil {
		render(w, status(err), "totp", view{Title: "Two-factor code", Error: message(err), Token: token})
		return
	}
	a.loginStep(w, r, res)
}

func (a *App) logoutPage(w http.ResponseWriter, r *http.Request) {
	if _, err := a.principal(r); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	render(w, http.StatusOK, "logout", view{Title: "Sign out", CSRF: csrfOf(r)})
}

func (a *App) logoutPost(w http.ResponseWriter, r *http.Request) {
	if c, err := a.caller(r); err == nil {
		_ = a.Identity.Logout(r.Context(), c)
	}
	signOut(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) settingsView(r *http.Request, errText string) (view, error) {
	list, err := a.Settings.List(r.Context())
	if err != nil {
		return view{}, err
	}
	seen := map[string]bool{}
	var tabs []string
	for _, e := range list {
		if !seen[e.Tab] {
			seen[e.Tab] = true
			tabs = append(tabs, e.Tab)
		}
	}
	sort.Strings(tabs)
	tab := r.FormValue("tab")
	if !seen[tab] && len(tabs) > 0 {
		tab = tabs[0]
	}
	var rows []settings.Entry
	for _, e := range list {
		if e.Tab == tab {
			rows = append(rows, e)
		}
	}
	return view{Title: "Settings", Nav: true, CSRF: csrfOf(r), Error: errText, Tabs: tabs, Tab: tab, Settings: rows}, nil
}

func (a *App) settingsPage(w http.ResponseWriter, r *http.Request) {
	if _, err := a.admin(r); err != nil {
		a.denied(w, r, err)
		return
	}
	v, err := a.settingsView(r, "")
	if err != nil {
		writeErr(w, err)
		return
	}
	render(w, http.StatusOK, "settings", v)
}

func (a *App) denied(w http.ResponseWriter, r *http.Request, err error) {
	if status(err) == http.StatusUnauthorized {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	writeErr(w, err)
}

func (a *App) settingsPost(w http.ResponseWriter, r *http.Request) {
	a.settingsChange(w, r, func(p domain.Principal) error {
		_, err := a.Settings.Set(r.Context(), r.PathValue("setting_key"), strings.TrimSpace(r.FormValue("value")), p)
		return err
	})
}

func (a *App) settingsResetPost(w http.ResponseWriter, r *http.Request) {
	a.settingsChange(w, r, func(p domain.Principal) error {
		_, err := a.Settings.Reset(r.Context(), r.PathValue("setting_key"), p)
		return err
	})
}

func (a *App) settingsChange(w http.ResponseWriter, r *http.Request, change func(domain.Principal) error) {
	p, err := a.admin(r)
	if err != nil {
		a.denied(w, r, err)
		return
	}
	if err := change(p); err != nil {
		v, verr := a.settingsView(r, "That value is not allowed for "+r.PathValue("setting_key")+". "+strings.TrimPrefix(message(settingErr(err)), "settings: "))
		if verr != nil {
			writeErr(w, verr)
			return
		}
		render(w, status(settingErr(err)), "settings", v)
		return
	}
	http.Redirect(w, r, "/admin/settings?tab="+url.QueryEscape(r.FormValue("tab")), http.StatusSeeOther)
}

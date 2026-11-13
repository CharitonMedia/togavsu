package dashboard

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/help"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// ScopeInfo is one bearer scope from the contract's scope catalog.
type ScopeInfo struct {
	Scope       string `json:"scope"`
	Description string `json:"description"`
}

// TokenScopes is the contract's x-togavsu-scope-catalog: the scopes the Agents and API
// tab offers. Identity still refuses any scope beyond the admin's or the owner's roles.
var TokenScopes = func() []ScopeInfo {
	var doc struct {
		Catalog []ScopeInfo `json:"x-togavsu-scope-catalog"`
	}
	if err := json.Unmarshal(api.OpenAPI, &doc); err != nil {
		panic("dashboard: openapi scope catalog: " + err.Error())
	}
	return doc.Catalog
}()

// PicturePath takes the profile picture upload, the ui's one multipart form.
const PicturePath = "/me/picture"

// tabUsers is the Users and roles admin tab.
const tabUsers = "users_roles"

func (h *Handler) accountRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /admin/users", h.form(h.createUserPost))
	m.HandleFunc("GET /admin/users/{user_id}", h.screen(h.userPage))
	m.HandleFunc("POST /admin/users/{user_id}", h.form(h.updateUserPost))
	m.HandleFunc("POST /admin/users/{user_id}/deactivate", h.form(h.deactivateUserPost))
	m.HandleFunc("POST /admin/users/{user_id}/walls", h.form(h.wallAddPost))
	m.HandleFunc("POST /admin/users/{user_id}/walls/{matter_id}/remove", h.form(h.wallRemovePost))
	m.HandleFunc("POST /admin/api-tokens", h.screen(h.adminIssueTokenPost))
	m.HandleFunc("POST /admin/api-tokens/{token_id}/revoke", h.form(h.adminRevokeTokenPost))
	m.HandleFunc("POST /me/profile", h.form(h.profilePost))
	m.HandleFunc("POST "+PicturePath, h.picturePost)
	m.HandleFunc("POST /me/picture/remove", h.form(h.pictureRemovePost))
	m.HandleFunc("GET /ui/pictures/{user_id}", h.picture)
	m.HandleFunc("POST /me/password", h.form(h.passwordPost))
	m.HandleFunc("POST /me/totp/reset", h.screen(h.totpResetPost))
	m.HandleFunc("POST /me/totp/reset/confirm", h.screen(h.totpConfirmPost))
	m.HandleFunc("POST /me/recovery-codes", h.screen(h.recoveryPost))
	m.HandleFunc("POST /me/api-tokens", h.screen(h.myIssueTokenPost))
	m.HandleFunc("POST /me/api-tokens/{token_id}/revoke", h.form(h.myRevokeTokenPost))
}

func (h *Handler) accounts() (*Accounts, error) {
	if h.Accounts == nil {
		return nil, domain.NotFound("accounts")
	}
	return h.Accounts, nil
}

// redirectErr sends a screen-rendering POST back to back with the error's message.
func redirectErr(w http.ResponseWriter, r *http.Request, back string, err error) (string, string, any, error) {
	var api *domain.APIError
	msg := "Something went wrong. Try again."
	if errors.As(err, &api) {
		msg = api.Message
	}
	http.Redirect(w, r, withParam(back, "error", msg), http.StatusSeeOther)
	return "", "", nil, nil
}

func optional(r *http.Request, key string) *string {
	if _, ok := r.PostForm[key]; !ok {
		return nil
	}
	v := r.PostForm.Get(key)
	return &v
}

// userForm reads the add and edit form. The roles checkboxes are always sent together
// with roles_sent, so an edit that clears every box is refused rather than ignored.
func userForm(r *http.Request) (UserInput, error) {
	if err := r.ParseForm(); err != nil {
		return UserInput{}, domain.Invalid("body", "the form could not be read")
	}
	in := UserInput{Name: optional(r, "name"), Email: optional(r, "email"), Password: r.PostForm.Get("password"),
		BarNumber: optional(r, "bar_number"), BarState: optional(r, "bar_state"), Phone: optional(r, "phone")}
	if in.BarState != nil {
		s := strings.ToUpper(strings.TrimSpace(*in.BarState))
		in.BarState = &s
	}
	if r.PostForm.Get("roles_sent") != "" {
		roles, err := ParseRoleList(r.PostForm["roles"])
		if err != nil {
			return UserInput{}, err
		}
		in.Roles = &roles
	}
	if s := optional(r, "rate"); s != nil {
		var cents int64
		if v := strings.TrimSpace(*s); v != "" {
			c, err := parseDollars(v)
			if err != nil {
				return UserInput{}, err
			}
			cents = c
		}
		in.RateCents = &cents
	}
	if s := optional(r, "linked_contact_id"); s != nil {
		id := domain.ID(strings.TrimSpace(*s))
		in.ContactID = &id
	}
	return in, nil
}

// parseDollars reads an amount such as 250 or 250.50 as integer cents, zero or more.
func parseDollars(s string) (int64, error) {
	c, err := domain.ParseCents(strings.TrimPrefix(strings.TrimSpace(s), "$"))
	if err != nil || int64(c) < noCents {
		return noCents, domain.Invalid("default_hourly_rate_cents", "enter the hourly rate in dollars, such as 250 or 250.50")
	}
	return int64(c), nil
}

func (h *Handler) createUserPost(r *http.Request, p domain.Principal) (string, string, error) {
	back := "/admin?tab=" + tabUsers
	a, err := h.accounts()
	if err != nil {
		return back, "", err
	}
	in, err := userForm(r)
	if err != nil {
		return back, "", err
	}
	if in.Roles == nil {
		return back, "", domain.Invalid("roles", "choose at least one role")
	}
	u, err := a.CreateUser(r.Context(), p, in)
	if err != nil {
		return back, "", err
	}
	return "/admin/users/" + string(u.ID), "Added " + u.Name + ". They set up two-factor at their first sign-in.", nil
}

type userPageView struct {
	Row      UserRow
	Roles    []domain.Role
	Walls    []WallRow
	Self     bool
	RateText string
}

func (h *Handler) userPage(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	a, err := h.accounts()
	if err != nil {
		return "", "", nil, err
	}
	id := domain.ID(r.PathValue("user_id"))
	row, err := a.User(r.Context(), p, id)
	if err != nil {
		return "", "", nil, err
	}
	walls, err := a.Walls(r.Context(), p, id)
	if err != nil {
		return "", "", nil, err
	}
	v := userPageView{Row: row, Roles: AllRoles, Walls: walls, Self: id == p.UserID, RateText: dollars(int64(row.User.DefaultHourlyRateCents))}
	return "admin_user.html", "Administrative · " + row.User.Name, v, nil
}

func dollars(cents int64) string { return domain.Cents(cents).String() }

func (h *Handler) updateUserPost(r *http.Request, p domain.Principal) (string, string, error) {
	id := domain.ID(r.PathValue("user_id"))
	back := "/admin/users/" + url.PathEscape(string(id))
	a, err := h.accounts()
	if err != nil {
		return back, "", err
	}
	in, err := userForm(r)
	if err != nil {
		return back, "", err
	}
	in.Password = ""
	u, err := a.UpdateUser(r.Context(), p, id, in)
	if err != nil {
		return back, "", err
	}
	return back, "Saved " + u.Name + ".", nil
}

func (h *Handler) deactivateUserPost(r *http.Request, p domain.Principal) (string, string, error) {
	id := domain.ID(r.PathValue("user_id"))
	back := "/admin/users/" + url.PathEscape(string(id))
	a, err := h.accounts()
	if err != nil {
		return back, "", err
	}
	if err := a.DeactivateUser(r.Context(), p, id); err != nil {
		return back, "", err
	}
	return back, "Deactivated. Their sessions and API tokens stop working now.", nil
}

func (h *Handler) wallAddPost(r *http.Request, p domain.Principal) (string, string, error) {
	id := domain.ID(r.PathValue("user_id"))
	back := "/admin/users/" + url.PathEscape(string(id))
	a, err := h.accounts()
	if err != nil {
		return back, "", err
	}
	if err := a.SetOnWall(r.Context(), p, id, domain.ID(r.FormValue("matter_id")), true); err != nil {
		return back, "", err
	}
	return back, "Added to the matter wall.", nil
}

func (h *Handler) wallRemovePost(r *http.Request, p domain.Principal) (string, string, error) {
	id := domain.ID(r.PathValue("user_id"))
	back := "/admin/users/" + url.PathEscape(string(id))
	a, err := h.accounts()
	if err != nil {
		return back, "", err
	}
	if err := a.SetOnWall(r.Context(), p, id, domain.ID(r.PathValue("matter_id")), false); err != nil {
		return back, "", err
	}
	return back, "Removed from the matter wall.", nil
}

// IssuedView is a token just issued: the secret is on this one response only.
type IssuedView struct {
	Name   string
	Secret string
	Scopes []string
}

func (h *Handler) adminIssueTokenPost(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	back := "/admin?tab=" + settings.TabAPI
	if err := admin(p); err != nil {
		return "", "", nil, err
	}
	a, err := h.accounts()
	if err != nil {
		return redirectErr(w, r, back, err)
	}
	if err := r.ParseForm(); err != nil {
		return redirectErr(w, r, back, domain.Invalid("body", "the form could not be read"))
	}
	scopes := r.PostForm["scopes"]
	if len(scopes) > fixed.TokenScopesMax {
		return redirectErr(w, r, back, domain.Invalid("scopes", "too many scopes"))
	}
	issued, err := a.IssueToken(r.Context(), p, r.PostForm.Get("name"), domain.ID(r.PostForm.Get("owner_user_id")), scopes)
	if err != nil {
		return redirectErr(w, r, back, err)
	}
	r.URL.RawQuery = "tab=" + settings.TabAPI
	name, title, v, err := h.adminPage(w, r, p)
	if err != nil {
		return "", "", nil, err
	}
	av := v.(adminView)
	av.Issued = &IssuedView{Name: issued.Token.Name, Secret: issued.Secret, Scopes: issued.Token.Scopes}
	return name, title, av, nil
}

func (h *Handler) adminRevokeTokenPost(r *http.Request, p domain.Principal) (string, string, error) {
	back := "/admin?tab=" + settings.TabAPI
	a, err := h.accounts()
	if err != nil {
		return back, "", err
	}
	if err := a.RevokeToken(r.Context(), p, domain.ID(r.PathValue("token_id")), false); err != nil {
		return back, "", err
	}
	return back, "Revoked the token. It stops working on its next request.", nil
}

// profilePageView is My profile. Enrollment, Codes and Issued are set only on the one
// response that shows them.
type profilePageView struct {
	Me         MyProfile
	Roles      string
	Tokens     []TokenRow
	CanIssue   bool
	Scopes     []ScopeInfo
	Enrollment *identity.Enrollment
	Codes      []string
	Issued     *IssuedView
}

func (h *Handler) profileView(r *http.Request, p domain.Principal) (profilePageView, error) {
	a, err := h.accounts()
	if err != nil {
		return profilePageView{}, err
	}
	me, err := a.Me(r.Context(), p)
	if err != nil {
		return profilePageView{}, err
	}
	toks, err := a.MyTokens(r.Context(), p)
	if err != nil {
		return profilePageView{}, err
	}
	v := profilePageView{Me: me, Roles: roleLabel(p.Roles), Tokens: toks, CanIssue: p.HasRole(domain.RoleAdmin)}
	if v.CanIssue {
		v.Scopes = TokenScopes
	}
	return v, nil
}

func (h *Handler) profilePage(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	v, err := h.profileView(r, p)
	if err != nil {
		return "", "", nil, err
	}
	return "profile.html", "My profile", v, nil
}

func (h *Handler) profilePost(r *http.Request, p domain.Principal) (string, string, error) {
	back := "/me/profile"
	a, err := h.accounts()
	if err != nil {
		return back, "", err
	}
	if err := r.ParseForm(); err != nil {
		return back, "", domain.Invalid("body", "the form could not be read")
	}
	if _, err := a.UpdateMe(r.Context(), p, optional(r, "name"), optional(r, "phone"), optional(r, "initials")); err != nil {
		return back, "", err
	}
	return back, "Saved your profile.", nil
}

// picturePost takes a multipart upload. The body is capped before anything reads it,
// including the CSRF field the core's Principal reads from the form.
func (h *Handler) picturePost(w http.ResponseWriter, r *http.Request) {
	back := "/me/profile"
	if r.ContentLength > int64(fixed.PictureFormBytes) {
		http.Redirect(w, r, withParam(back, "error", "the picture is larger than "+strconv.Itoa(fixed.PictureMaxBytes/1024)+" KB"), http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, int64(fixed.PictureFormBytes))
	h.form(func(r *http.Request, p domain.Principal) (string, string, error) {
		a, err := h.accounts()
		if err != nil {
			return back, "", err
		}
		f, _, err := r.FormFile("picture")
		if err != nil {
			return back, "", domain.Invalid("picture", "choose a picture to upload")
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, int64(fixed.PictureMaxBytes)+1))
		if err != nil {
			return back, "", domain.Invalid("picture", "the picture could not be read")
		}
		if len(data) == 0 {
			return back, "", domain.Invalid("picture", "choose a picture to upload")
		}
		if err := a.SetPicture(r.Context(), p, data); err != nil {
			return back, "", err
		}
		return back, "Saved your profile picture.", nil
	})(w, r)
}

func (h *Handler) pictureRemovePost(r *http.Request, p domain.Principal) (string, string, error) {
	back := "/me/profile"
	a, err := h.accounts()
	if err != nil {
		return back, "", err
	}
	if err := a.SetPicture(r.Context(), p, nil); err != nil {
		return back, "", err
	}
	return back, "Removed your profile picture.", nil
}

// picture serves a profile picture to signed-in staff, with its sniffed type.
func (h *Handler) picture(w http.ResponseWriter, r *http.Request) {
	p, err := h.Principal(r)
	if err != nil {
		help.WriteErr(w, err)
		return
	}
	a, err := h.accounts()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, ctype, err := a.Picture(r.Context(), p, domain.ID(r.PathValue("user_id")))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, _ = w.Write(data)
}

func (h *Handler) passwordPost(r *http.Request, p domain.Principal) (string, string, error) {
	back := "/me/profile"
	a, err := h.accounts()
	if err != nil {
		return back, "", err
	}
	if err := a.ChangePassword(r.Context(), p, r.FormValue("current_password"), r.FormValue("new_password"), r.FormValue("confirm_password")); err != nil {
		return back, "", err
	}
	return back, "Changed your password. Your other sessions were signed out.", nil
}

func (h *Handler) totpResetPost(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	back := "/me/profile"
	a, err := h.accounts()
	if err != nil {
		return redirectErr(w, r, back, err)
	}
	en, err := a.StartTOTPReset(r.Context(), p, r.FormValue("password"))
	if err != nil {
		return redirectErr(w, r, back, err)
	}
	v, err := h.profileView(r, p)
	if err != nil {
		return "", "", nil, err
	}
	v.Enrollment = &en
	return "profile.html", "My profile", v, nil
}

func (h *Handler) totpConfirmPost(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	back := "/me/profile"
	a, err := h.accounts()
	if err != nil {
		return redirectErr(w, r, back, err)
	}
	codes, err := a.ConfirmTOTPReset(r.Context(), p, r.FormValue("enrollment_token"), r.FormValue("code"))
	if err != nil {
		return redirectErr(w, r, back, err)
	}
	if len(codes) == 0 {
		http.Redirect(w, r, pathOnly(withParam(back, "notice", "Two-factor is on. Use this authenticator entry from now on; any older one no longer works. Make new recovery codes below.")), http.StatusSeeOther)
		return "", "", nil, nil
	}
	// Two-factor was just turned on for the first time: show the recovery codes once.
	v, err := h.profileView(r, p)
	if err != nil {
		return "", "", nil, err
	}
	v.Codes = codes
	return "profile.html", "My profile", v, nil
}

func (h *Handler) recoveryPost(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	back := "/me/profile"
	a, err := h.accounts()
	if err != nil {
		return redirectErr(w, r, back, err)
	}
	codes, err := a.NewRecoveryCodes(r.Context(), p, r.FormValue("password"))
	if err != nil {
		return redirectErr(w, r, back, err)
	}
	v, err := h.profileView(r, p)
	if err != nil {
		return "", "", nil, err
	}
	v.Codes = codes
	return "profile.html", "My profile", v, nil
}

func (h *Handler) myIssueTokenPost(w http.ResponseWriter, r *http.Request, p domain.Principal) (string, string, any, error) {
	back := "/me/profile"
	a, err := h.accounts()
	if err != nil {
		return redirectErr(w, r, back, err)
	}
	if err := r.ParseForm(); err != nil {
		return redirectErr(w, r, back, domain.Invalid("body", "the form could not be read"))
	}
	scopes := r.PostForm["scopes"]
	if len(scopes) > fixed.TokenScopesMax {
		return redirectErr(w, r, back, domain.Invalid("scopes", "too many scopes"))
	}
	issued, err := a.IssueToken(r.Context(), p, r.PostForm.Get("name"), p.UserID, scopes)
	if err != nil {
		return redirectErr(w, r, back, err)
	}
	v, err := h.profileView(r, p)
	if err != nil {
		return "", "", nil, err
	}
	v.Issued = &IssuedView{Name: issued.Token.Name, Secret: issued.Secret, Scopes: issued.Token.Scopes}
	return "profile.html", "My profile", v, nil
}

func (h *Handler) myRevokeTokenPost(r *http.Request, p domain.Principal) (string, string, error) {
	back := "/me/profile"
	a, err := h.accounts()
	if err != nil {
		return back, "", err
	}
	if err := a.RevokeToken(r.Context(), p, domain.ID(r.PathValue("token_id")), true); err != nil {
		return back, "", err
	}
	return back, "Revoked the token.", nil
}

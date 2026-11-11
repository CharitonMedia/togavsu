package compose

import (
	"bytes"
	"context"
	"html"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// These cover the Codex checkpoint findings on PR #35: the Users and roles tab (PRD 8.7),
// My profile and the user chip picture (PRD 8.9, 8.17), and the Agents and API token
// panel (PRD 8.7). Every write goes through the identity, audit and practice services as
// the signed-in user; the refusals are checked on the server, not the page.

var (
	secretRe = regexp.MustCompile(`id="token-secret">([^<]+)<`)
	totpRe   = regexp.MustCompile(`id="totp-secret">([^<]+)<`)
	enrollRe = regexp.MustCompile(`name="enrollment_token" value="([^"]+)"`)
	codeRe   = regexp.MustCompile(`<li><code>([^<]+)</code></li>`)
)

func query(t *testing.T, loc, key string) string {
	t.Helper()
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}

// formErr posts a form that the service must refuse: a 303 back with an error message.
func formErr(t *testing.T, c *client, path string, v url.Values, want string) {
	t.Helper()
	r := c.form(path, v)
	msg := query(t, r.loc, "error")
	if msg == "" || !strings.Contains(msg, want) {
		t.Fatalf("POST %s: want an error containing %q, got redirect %s", path, want, r.loc)
	}
}

func formOK(t *testing.T, c *client, path string, v url.Values) resp {
	t.Helper()
	r := c.form(path, v)
	if msg := query(t, r.loc, "error"); msg != "" {
		t.Fatalf("POST %s refused: %s", path, msg)
	}
	return r
}

// render posts a form whose answer is a page (a secret shown once).
func render(t *testing.T, c *client, path string, v url.Values) resp {
	t.Helper()
	if v == nil {
		v = url.Values{}
	}
	v.Set("csrf", c.csrf)
	r := c.do("POST", path, v)
	if r.code != http.StatusOK {
		t.Fatalf("POST %s: want 200, got %d %s %.800s", path, r.code, r.loc, r.body)
	}
	return r
}

func (e *env) userRow(id string) domain.User {
	e.t.Helper()
	u, err := e.app.Users.Get(context.Background(), domain.ID(id))
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

// audited finds the audit row for one field change of a user.
func (e *env) audited(id, field, old, new string) bool {
	e.t.Helper()
	rows, err := e.app.Audit.ForEntity(context.Background(), "user", domain.ID(id))
	if err != nil {
		e.t.Fatal(err)
	}
	for _, r := range rows {
		if r.Field == field && r.OldValue == old && r.NewValue == new {
			return true
		}
	}
	return false
}

func (e *env) userCount() int {
	e.t.Helper()
	all, err := listUsers(context.Background(), e.app)
	if err != nil {
		e.t.Fatal(err)
	}
	return len(all)
}

func login(t *testing.T, base, email, password string) resp {
	t.Helper()
	return newClient(t, base).do("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": password})
}

func TestUIUsersAndRoles(t *testing.T) {
	e := newEnv(t)
	tab := e.admin.must(http.StatusOK, "GET", "/admin?tab=users_roles", nil).body
	for _, want := range []string{`action="/admin/users"`, `href="/admin/users/` + e.staff.id + `"`, "Sam Staff", `value="client"`} {
		if !strings.Contains(tab, want) {
			t.Fatalf("Users and roles tab is missing %q", want)
		}
	}
	before := e.userCount()
	add := url.Values{"name": {"Olive Owens"}, "email": {"Olive@Firm.example"}, "phone": {"+1 (314) 555-0101"}, "roles_sent": {"1"},
		"roles": {"attorney"}, "password": {"synthetic-password-Olive Owens"}, "rate": {"275.50"}, "bar_number": {"65432"}, "bar_state": {"mo"}}

	// Refusals first: none of them writes a user.
	formErr(t, e.staff, "/admin/users", add, "")
	if r := e.staff.do("GET", "/admin/users/"+e.atty.id, nil); r.code != http.StatusForbidden {
		t.Fatalf("staff opened the user edit screen: %d", r.code)
	}
	noCSRF := e.admin.do("POST", "/admin/users", add)
	if noCSRF.code != http.StatusForbidden {
		t.Fatalf("add user without the CSRF field: %d %s", noCSRF.code, noCSRF.body)
	}
	bad := func(k, v, want string) {
		in := url.Values{}
		for key, vals := range add {
			in[key] = append([]string(nil), vals...)
		}
		in.Set(k, v)
		formErr(t, e.admin, "/admin/users", in, want)
	}
	bad("roles", "staff", "bar number and state are for attorneys")
	bad("bar_state", "Missouri", "two-letter")
	bad("password", "short", "at least 12")
	bad("rate", "-5", "hourly rate")
	bad("linked_contact_id", "0190a000-0000-7000-8000-000000000001", "a linked contact is for client users")
	bad("roles", "partner", "unknown role")
	if n := e.userCount(); n != before {
		t.Fatalf("a refused add wrote a user: %d -> %d", before, n)
	}

	// Add an attorney from the tab: the extra fields land on the row and are audited.
	r := formOK(t, e.admin, "/admin/users", add)
	if !strings.HasPrefix(r.loc, "/admin/users/") {
		t.Fatalf("add user redirect: %s", r.loc)
	}
	olive := strings.TrimPrefix(strings.SplitN(r.loc, "?", 2)[0], "/admin/users/")
	u := e.userRow(olive)
	if u.Email != "olive@firm.example" || u.DefaultHourlyRateCents != 27550 || u.BarNumber != "65432" || u.BarState != "MO" || !u.Active {
		t.Fatalf("created user: %+v", u)
	}
	if !e.audited(olive, "email", "", "olive@firm.example") || !e.audited(olive, "bar_number", "", "65432") || !e.audited(olive, "default_hourly_rate_cents", "0", "27550") || !e.audited(olive, "phone", "", "+1 (314) 555-0101") {
		t.Fatal("the add is not audited field by field")
	}
	if formErr(t, e.admin, "/admin/users", add, "already in use"); e.userCount() != before+1 {
		t.Fatal("a duplicate email wrote a user")
	}
	page := e.admin.must(http.StatusOK, "GET", "/admin/users/"+olive, nil).body
	if !strings.Contains(page, `class="help-q" href="/help/topics/admin-user"`) {
		t.Fatalf("the edit screen's ? does not open its topic: %s", between(page, `class="titlebar"`, "</div>"))
	}
	for _, want := range []string{`value="275.50"`, `value="65432"`, `value="MO"`, `value="attorney" checked`, "+1 (314) 555-0101", "Deactivate Olive Owens"} {
		if !strings.Contains(html.UnescapeString(page), want) {
			t.Fatalf("edit screen is missing %q: %s", want, between(page, `id="edit-user"`, "</form>"))
		}
	}

	// Edit: roles, rate, and a refused edit leaves the row as it was.
	edit := url.Values{"roles_sent": {"1"}, "roles": {"attorney", "staff"}, "rate": {"300"}, "name": {"Olive Owens"}, "email": {"olive@firm.example"}, "bar_number": {"65432"}, "bar_state": {"MO"}, "phone": {"+1 (314) 555-0101"}, "linked_contact_id": {""}}
	formErr(t, e.staff, "/admin/users/"+olive, edit, "")
	formOK(t, e.admin, "/admin/users/"+olive, edit)
	u = e.userRow(olive)
	if u.DefaultHourlyRateCents != 30000 || !strings.Contains(u.Roles, "staff") {
		t.Fatalf("edit did not save: %+v", u)
	}
	if !e.audited(olive, "default_hourly_rate_cents", "27550", "30000") || !e.audited(olive, "roles", `["attorney"]`, u.Roles) {
		rows, _ := e.app.Audit.ForEntity(context.Background(), "user", domain.ID(olive))
		t.Fatalf("edit not audited with old and new: %+v", rows)
	}
	noRoles := url.Values{"roles_sent": {"1"}}
	formErr(t, e.admin, "/admin/users/"+olive, noRoles, "at least one role")
	if e.userRow(olive).Roles != u.Roles {
		t.Fatal("an edit with every role cleared changed the roles")
	}
	// Dropping the attorney role clears the bar fields, audited.
	formOK(t, e.admin, "/admin/users/"+olive, url.Values{"roles_sent": {"1"}, "roles": {"staff"}})
	if u = e.userRow(olive); u.BarNumber != "" || u.BarState != "" || !e.audited(olive, "bar_number", "65432", "") {
		t.Fatalf("bar fields kept without the attorney role: %+v", u)
	}
	formOK(t, e.admin, "/admin/users/"+olive, url.Values{"roles_sent": {"1"}, "roles": {"attorney"}, "bar_number": {"65432"}, "bar_state": {"MO"}})
	// The linked contact goes to client users only; an unknown contact is refused by the database's key.
	formErr(t, e.admin, "/admin/users/"+olive, url.Values{"linked_contact_id": {"0190a000-0000-7000-8000-000000000001"}}, "for client users")
	cl := e.createUser("Cal Client", "cal@firm.example", "", domain.RoleClient)
	formErr(t, e.admin, "/admin/users/"+string(cl.ID), url.Values{"linked_contact_id": {"0190a000-0000-7000-8000-000000000001"}}, "unknown contact")

	// The last active admin keeps the role and can't deactivate themselves.
	formErr(t, e.admin, "/admin/users/"+e.admin.id, url.Values{"roles_sent": {"1"}, "roles": {"attorney"}}, "at least one active admin")
	formErr(t, e.admin, "/admin/users/"+e.admin.id+"/deactivate", nil, "can't deactivate yourself")
	if !e.userRow(e.admin.id).Active || !strings.Contains(e.userRow(e.admin.id).Roles, "admin") {
		t.Fatal("the last admin lost the role or was deactivated")
	}

	// Matter walls: the admin is on the wall, so practice shows it here.
	m, num, _ := e.matter("Estate of Wallace", nil)
	e.wall(m, e.admin.id, e.atty.id)
	oc := e.signIn("Olive Owens", "olive@firm.example")
	if r := oc.do("GET", "/api/v1/matters/"+m, nil); r.code == http.StatusOK {
		t.Fatal("Olive read the walled matter before being added")
	}
	page = e.admin.must(http.StatusOK, "GET", "/admin/users/"+olive, nil).body
	if !strings.Contains(page, num) || !strings.Contains(page, "Not on the wall") {
		t.Fatalf("walls panel: %s", between(page, `id="walls"`, "</section>"))
	}
	formErr(t, e.staff, "/admin/users/"+olive+"/walls", url.Values{"matter_id": {m}}, "")
	formOK(t, e.admin, "/admin/users/"+olive+"/walls", url.Values{"matter_id": {m}})
	oc.must(http.StatusOK, "GET", "/api/v1/matters/"+m, nil)
	formOK(t, e.admin, "/admin/users/"+olive+"/walls/"+m+"/remove", nil)
	if r := oc.do("GET", "/api/v1/matters/"+m, nil); r.code == http.StatusOK {
		t.Fatal("Olive still reads the matter after leaving the wall")
	}
	formOK(t, e.admin, "/admin/users/"+e.atty.id+"/walls/"+m+"/remove", nil)
	formErr(t, e.admin, "/admin/users/"+e.admin.id+"/walls/"+m+"/remove", nil, "last person")
	open, _, _ := e.matter("Estate of Openshaw", nil)
	formErr(t, e.admin, "/admin/users/"+olive+"/walls", url.Values{"matter_id": {open}}, "no wall")

	// Deactivate: the session and a bearer token stop at once; audited.
	tok := render(t, e.admin, "/admin/api-tokens", url.Values{"name": {"olive agent"}, "owner_user_id": {olive}, "scopes": {"matters:read"}})
	agent := newClient(t, e.srv.URL)
	agent.bearer = secretRe.FindStringSubmatch(tok.body)[1]
	agent.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	formErr(t, e.staff, "/admin/users/"+olive+"/deactivate", nil, "")
	oc.must(http.StatusOK, "GET", "/api/v1/me", nil)
	formOK(t, e.admin, "/admin/users/"+olive+"/deactivate", nil)
	if e.userRow(olive).Active || !e.audited(olive, "active", "true", "false") {
		t.Fatal("deactivate did not write or audit")
	}
	if r := oc.do("GET", "/api/v1/me", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("deactivated user's session still works: %d", r.code)
	}
	if r := agent.do("GET", "/api/v1/matters", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("deactivated user's token still works: %d", r.code)
	}
	if r := login(t, e.srv.URL, "olive@firm.example", "synthetic-password-Olive Owens"); r.code == http.StatusOK {
		t.Fatalf("deactivated user signed in: %s", r.body)
	}
	if tab := e.admin.must(http.StatusOK, "GET", "/admin?tab=users_roles", nil).body; !strings.Contains(tab, `id="u-`+olive+`" class="inactive"`) {
		t.Fatal("the tab does not show the user as inactive")
	}
}

func TestUIAgentsAndAPITokens(t *testing.T) {
	e := newEnv(t)
	if r := e.staff.do("GET", "/admin?tab=agents_api", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff opened Agents and API: %d", r.code)
	}
	tab := e.admin.must(http.StatusOK, "GET", "/admin?tab=agents_api", nil).body
	for _, want := range []string{`action="/admin/api-tokens"`, `value="matters:read"`, "Read matters", `value="` + e.atty.id + `"`, "webhook"} {
		if !strings.Contains(tab, want) {
			t.Fatalf("Agents and API tab is missing %q", want)
		}
	}
	issue := url.Values{"name": {"research agent"}, "owner_user_id": {e.atty.id}, "scopes": {"matters:read", "profile:read"}}
	// Refused: a non-admin, no CSRF, no scopes, a scope beyond the owner's role.
	staffTry := url.Values{"csrf": {e.staff.csrf}, "name": {"x"}, "owner_user_id": {e.staff.id}, "scopes": {"matters:read"}}
	if r := e.staff.do("POST", "/admin/api-tokens", staffTry); r.code != http.StatusForbidden || secretRe.MatchString(r.body) {
		t.Fatalf("staff issued a token: %d", r.code)
	}
	if r := e.admin.do("POST", "/admin/api-tokens", issue); r.code != http.StatusForbidden {
		t.Fatalf("token issued without the CSRF field: %d", r.code)
	}
	formErr(t, e.admin, "/admin/api-tokens", url.Values{"name": {"empty"}, "owner_user_id": {e.atty.id}}, "at least one scope")
	formErr(t, e.admin, "/admin/api-tokens", url.Values{"name": {"too much"}, "owner_user_id": {e.acct.id}, "scopes": {"conflicts:decide"}}, "scope")
	if toks := items(t, e.admin.must(http.StatusOK, "GET", "/api/v1/api-tokens", nil)); len(toks) != 0 {
		t.Fatalf("a refused issue wrote a token: %v", toks)
	}

	// Issued: the secret is on this one page; the list shows scopes, never the secret.
	r := render(t, e.admin, "/admin/api-tokens", issue)
	m := secretRe.FindStringSubmatch(r.body)
	if m == nil || r.csp == "" {
		t.Fatalf("issued page has no one-time secret: %.600s", r.body)
	}
	secret := m[1]
	if !strings.Contains(r.body, "matters:read, profile:read") {
		t.Fatal("issued page does not show the scopes")
	}
	list := e.admin.must(http.StatusOK, "GET", "/admin?tab=agents_api", nil).body
	if strings.Contains(list, secret) || !strings.Contains(list, "research agent") || !strings.Contains(list, "Ada Attorney") || !strings.Contains(list, "matters:read, profile:read") {
		t.Fatal("token list leaks the secret or misses the token, owner or scopes")
	}
	rows, err := e.app.Audit.Rows(context.Background(), audit.RowQuery{Entity: "api_token"})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if strings.Contains(row.NewValue, secret) || strings.Contains(row.OldValue, secret) {
			t.Fatal("the audit log holds the token secret")
		}
	}

	// The token acts as its owner, inside its scopes.
	agent := newClient(t, e.srv.URL)
	agent.bearer = secret
	agent.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	if r := agent.do("POST", "/api/v1/matters", map[string]any{"name": "x"}); r.code != http.StatusForbidden {
		t.Fatalf("matters:read token wrote: %d", r.code)
	}
	toks := items(t, e.admin.must(http.StatusOK, "GET", "/api/v1/api-tokens", nil))
	if len(toks) != 1 {
		t.Fatalf("tokens: %v", toks)
	}
	id := toks[0]["id"].(string)
	formErr(t, e.staff, "/admin/api-tokens/"+id+"/revoke", nil, "")
	agent.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	formOK(t, e.admin, "/admin/api-tokens/"+id+"/revoke", nil)
	if r := agent.do("GET", "/api/v1/matters", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("revoked token still authenticates: %d", r.code)
	}
	if list := e.admin.must(http.StatusOK, "GET", "/admin?tab=agents_api", nil).body; !strings.Contains(between(list, `id="t-`+id+`"`, "</tr>"), "Revoked") {
		t.Fatal("the list does not show the token revoked")
	}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func upload(t *testing.T, c *client, data []byte, csrf bool) resp {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if csrf {
		_ = w.WriteField("csrf", c.csrf)
	}
	fw, err := w.CreateFormFile("picture", "me.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = w.Close()
	req, err := http.NewRequest("POST", c.base+"/me/picture", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return resp{code: res.StatusCode, body: string(raw), loc: res.Header.Get("Location")}
}

func TestUIProfileAccount(t *testing.T) {
	e := newEnv(t)
	page := e.staff.must(http.StatusOK, "GET", "/me/profile", nil).body
	for _, want := range []string{`id="pf-phone"`, `action="/me/picture"`, `action="/me/password"`, `action="/me/totp/reset"`, `action="/me/recovery-codes"`, `id="my-tokens"`, "An admin issues tokens"} {
		if !strings.Contains(page, want) {
			t.Fatalf("profile is missing %q", want)
		}
	}

	// Name, phone and initials: saved, audited, and the chip shows the initials.
	formErr(t, e.staff, "/me/profile", url.Values{"phone": {"call me maybe"}}, "phone number")
	formErr(t, e.staff, "/me/profile", url.Values{"initials": {"S4"}}, "letters only")
	formOK(t, e.staff, "/me/profile", url.Values{"name": {"Sam Q Staff"}, "phone": {"314-555-0199"}, "initials": {"sqs"}})
	if !e.audited(e.staff.id, "phone", "", "314-555-0199") || !e.audited(e.staff.id, "name", "Sam Staff", "Sam Q Staff") || !e.audited(e.staff.id, "initials", "", "SQS") {
		t.Fatal("profile change not audited")
	}
	home := e.staff.must(http.StatusOK, "GET", "/home", nil).body
	if !strings.Contains(home, `aria-label="Account menu for Sam Q Staff">SQS</summary>`) {
		t.Fatalf("chip: %s", between(home, `class="menu user"`, "</summary>"))
	}
	if r := e.staff.do("POST", "/me/profile", url.Values{"phone": {"1"}}); r.code != http.StatusForbidden {
		t.Fatalf("profile write without CSRF: %d", r.code)
	}

	// Picture: sniffed PNG only, capped, CSRF required; the chip then shows it.
	if r := upload(t, e.staff, pngBytes(t), false); r.code != http.StatusForbidden {
		t.Fatalf("picture upload without CSRF: %d", r.code)
	}
	if r := upload(t, e.staff, []byte("<svg onload=alert(1)>"), true); !strings.Contains(query(t, r.loc, "error"), "PNG, JPEG or WebP") {
		t.Fatalf("an svg was accepted: %s", r.loc)
	}
	if r := upload(t, e.staff, append(pngBytes(t), make([]byte, 300_000)...), true); !strings.Contains(query(t, r.loc, "error"), "larger than") {
		t.Fatalf("an oversize picture was accepted: %d %s", r.code, r.loc)
	}
	if r := upload(t, e.staff, pngBytes(t), true); r.code != http.StatusSeeOther || query(t, r.loc, "error") != "" {
		t.Fatalf("picture upload: %d %s", r.code, r.loc)
	}
	home = e.staff.must(http.StatusOK, "GET", "/home", nil).body
	if !regexp.MustCompile(`<img class="avatar" src="/ui/pictures/` + e.staff.id + `\?v=\d+"`).MatchString(home) {
		t.Fatalf("chip picture: %s", between(home, `class="menu user"`, "</summary>"))
	}
	pic := e.atty.must(http.StatusOK, "GET", "/ui/pictures/"+e.staff.id, nil)
	if pic.ctype != "image/png" || !bytes.Equal([]byte(pic.body), pngBytes(t)) {
		t.Fatalf("picture served as %q", pic.ctype)
	}
	if r := newClient(t, e.srv.URL).do("GET", "/ui/pictures/"+e.staff.id, nil); r.code != http.StatusUnauthorized {
		t.Fatalf("signed-out picture read: %d", r.code)
	}
	if !e.audited(e.staff.id, "profile_picture", "", "image/png") {
		t.Fatal("picture not audited")
	}
	formOK(t, e.staff, "/me/picture/remove", nil)
	if home = e.staff.must(http.StatusOK, "GET", "/home", nil).body; strings.Contains(home, `class="avatar"`) {
		t.Fatal("chip still shows the removed picture")
	}

	// Password: a wrong current password is refused and changes nothing.
	other := e.signIn("Sam Staff", "sam@firm.example")
	formErr(t, e.staff, "/me/password", url.Values{"current_password": {"wrong-password-here"}, "new_password": {"brand-new-password-1"}, "confirm_password": {"brand-new-password-1"}}, "not correct")
	formErr(t, e.staff, "/me/password", url.Values{"current_password": {"synthetic-password-Sam Staff"}, "new_password": {"brand-new-password-1"}, "confirm_password": {"brand-new-password-2"}}, "don't match")
	formErr(t, e.staff, "/me/password", url.Values{"current_password": {"synthetic-password-Sam Staff"}, "new_password": {"short"}, "confirm_password": {"short"}}, "at least 12")
	if r := login(t, e.srv.URL, "sam@firm.example", "synthetic-password-Sam Staff"); r.code != http.StatusOK {
		t.Fatalf("a refused change broke the old password: %d", r.code)
	}
	other.must(http.StatusOK, "GET", "/api/v1/me", nil)
	formOK(t, e.staff, "/me/password", url.Values{"current_password": {"synthetic-password-Sam Staff"}, "new_password": {"brand-new-password-1"}, "confirm_password": {"brand-new-password-1"}})
	if r := login(t, e.srv.URL, "sam@firm.example", "synthetic-password-Sam Staff"); r.code == http.StatusOK {
		t.Fatal("the old password still signs in")
	}
	if r := login(t, e.srv.URL, "sam@firm.example", "brand-new-password-1"); r.code != http.StatusOK {
		t.Fatalf("the new password does not sign in: %d %s", r.code, r.body)
	}
	if r := other.do("GET", "/api/v1/me", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("the other session survived the password change: %d", r.code)
	}
	e.staff.must(http.StatusOK, "GET", "/api/v1/me", nil)

	// Two-factor reset: a wrong password shows no secret; a wrong code is refused; the
	// right code from the new key switches sign-in to it.
	formErr(t, e.staff, "/me/totp/reset", url.Values{"password": {"not-my-password"}}, "didn't match")
	r := render(t, e.staff, "/me/totp/reset", url.Values{"password": {"brand-new-password-1"}})
	tok := enrollRe.FindStringSubmatch(r.body)
	if tok == nil || totpRe.FindStringSubmatch(r.body) == nil {
		t.Fatalf("reset page: %.600s", between(r.body, `id="totp-reset"`, "</section>"))
	}
	formErr(t, e.staff, "/me/totp/reset/confirm", url.Values{"enrollment_token": {tok[1]}, "code": {"000000"}}, "")
	r = render(t, e.staff, "/me/totp/reset", url.Values{"password": {"brand-new-password-1"}})
	newSecret := html.UnescapeString(totpRe.FindStringSubmatch(r.body)[1])
	tok = enrollRe.FindStringSubmatch(r.body)
	if r := e.atty.form("/me/totp/reset/confirm", url.Values{"enrollment_token": {html.UnescapeString(tok[1])}, "code": {totp(t, newSecret, time.Now())}}); query(t, r.loc, "error") == "" {
		t.Fatal("another user confirmed this user's reset")
	}
	r = render(t, e.staff, "/me/totp/reset", url.Values{"password": {"brand-new-password-1"}})
	newSecret = html.UnescapeString(totpRe.FindStringSubmatch(r.body)[1])
	tok = enrollRe.FindStringSubmatch(r.body)
	formOK(t, e.staff, "/me/totp/reset/confirm", url.Values{"enrollment_token": {html.UnescapeString(tok[1])}, "code": {totp(t, newSecret, time.Now())}})
	c := newClient(t, e.srv.URL)
	lr := c.must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": "sam@firm.example", "password": "brand-new-password-1"})
	if r := c.do("POST", "/api/v1/auth/totp", map[string]string{"totp_token": str(t, lr.json, "totp_token"), "code": totp(t, newSecret, time.Now().Add(30*time.Second))}); r.code != http.StatusOK || str(t, r.json, "status") != "authenticated" {
		t.Fatalf("sign-in with the new key: %d %s", r.code, r.body)
	}

	// New recovery codes: shown once; the old codes stop working.
	formErr(t, e.staff, "/me/recovery-codes", url.Values{"password": {"not-my-password"}}, "didn't match")
	old := e.recovery["sam@firm.example"]
	r = render(t, e.staff, "/me/recovery-codes", url.Values{"password": {"brand-new-password-1"}})
	codes := codeRe.FindAllStringSubmatch(between(r.body, `id="recovery-codes"`, "</section>"), -1)
	if len(codes) < 8 || len(old) == 0 {
		t.Fatalf("recovery codes: %d new, %d old", len(codes), len(old))
	}
	useCode := func(code string) int {
		c := newClient(t, e.srv.URL)
		lr := c.must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": "sam@firm.example", "password": "brand-new-password-1"})
		return c.do("POST", "/api/v1/auth/recovery", map[string]string{"totp_token": str(t, lr.json, "totp_token"), "recovery_code": code}).code
	}
	if code := useCode(old[len(old)-1]); code == http.StatusOK {
		t.Fatal("an old recovery code still signs in")
	}
	if code := useCode(html.UnescapeString(codes[0][1])); code != http.StatusOK {
		t.Fatalf("a new recovery code does not sign in: %d", code)
	}
	if strings.Contains(e.staff.must(http.StatusOK, "GET", "/me/profile", nil).body, codes[1][1]) {
		t.Fatal("recovery codes show again on the next page load")
	}

	// My API tokens: owner only, listed, created by an admin for themselves, revoked.
	mine := render(t, e.admin, "/admin/api-tokens", url.Values{"name": {"sam agent"}, "owner_user_id": {e.staff.id}, "scopes": {"matters:read"}})
	samAgent := newClient(t, e.srv.URL)
	samAgent.bearer = secretRe.FindStringSubmatch(mine.body)[1]
	theirs := render(t, e.admin, "/admin/api-tokens", url.Values{"name": {"ada agent"}, "owner_user_id": {e.atty.id}, "scopes": {"matters:read"}})
	adaAgent := newClient(t, e.srv.URL)
	adaAgent.bearer = secretRe.FindStringSubmatch(theirs.body)[1]
	var samID, adaID string
	for _, tk := range items(t, e.admin.must(http.StatusOK, "GET", "/api/v1/api-tokens", nil)) {
		switch tk["name"] {
		case "sam agent":
			samID = tk["id"].(string)
		case "ada agent":
			adaID = tk["id"].(string)
		}
	}
	page = e.staff.must(http.StatusOK, "GET", "/me/profile", nil).body
	if !strings.Contains(page, "sam agent") || strings.Contains(page, "ada agent") {
		t.Fatal("My API tokens is not the owner's list")
	}
	if strings.Contains(e.admin.must(http.StatusOK, "GET", "/me/profile", nil).body, "sam agent") {
		t.Fatal("the admin's profile lists another user's token")
	}
	if r := e.staff.do("POST", "/me/api-tokens", url.Values{"csrf": {e.staff.csrf}, "name": {"self"}, "scopes": {"matters:read"}}); r.code != http.StatusSeeOther || query(t, r.loc, "error") == "" {
		t.Fatalf("a non-admin issued a token: %d %s", r.code, r.loc)
	}
	formErr(t, e.staff, "/me/api-tokens/"+adaID+"/revoke", nil, "not found")
	adaAgent.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	formErr(t, e.admin, "/me/api-tokens/"+samID+"/revoke", nil, "not found")
	samAgent.must(http.StatusOK, "GET", "/api/v1/matters", nil)
	formOK(t, e.staff, "/me/api-tokens/"+samID+"/revoke", nil)
	if r := samAgent.do("GET", "/api/v1/matters", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("revoked own token still works: %d", r.code)
	}
	own := render(t, e.admin, "/me/api-tokens", url.Values{"name": {"avery agent"}, "scopes": {"settings:read"}})
	if secretRe.FindStringSubmatch(own.body) == nil || !strings.Contains(between(own.body, `id="my-tokens"`, "</section>"), "avery agent") {
		t.Fatal("an admin's own token was not issued and listed")
	}
	if strings.Contains(e.admin.must(http.StatusOK, "GET", "/me/profile", nil).body, secretRe.FindStringSubmatch(own.body)[1]) {
		t.Fatal("the token secret shows again")
	}
}

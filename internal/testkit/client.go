package testkit

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// Client calls the core as one person or token: a cookie jar for the session, the
// session's CSRF token on every request, and a bearer token when set. Redirects are not
// followed, so a form post's 303 and its Location come back as they are.
type Client struct {
	T    *testing.T
	Base string
	HTTP *http.Client
	// CSRF is sent as X-CSRF-Token when set.
	CSRF string
	// Bearer is sent as "Authorization: Bearer" when set.
	Bearer string
}

// Resp is one response: the status, the body, the body decoded when it is a JSON
// object, and the Location header.
type Resp struct {
	Code int
	Body string
	JSON map[string]any
	Loc  string
}

// NewClient is a signed-out client of the core at base.
func NewClient(t *testing.T, base string) *Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{T: t, Base: base, HTTP: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

// send makes the request with the client's CSRF and bearer, then hdr pairs; an empty
// value deletes the header when del is set.
func (c *Client) send(method, path string, rd io.Reader, ctype string, del bool, hdr ...string) Resp {
	c.T.Helper()
	req, err := http.NewRequest(method, c.Base+path, rd)
	if err != nil {
		c.T.Fatal(err)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if c.CSRF != "" {
		req.Header.Set("X-CSRF-Token", c.CSRF)
	}
	if c.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.Bearer)
	}
	for i := zero; i+one < len(hdr); i += one + one {
		if del && hdr[i+one] == "" {
			req.Header.Del(hdr[i])
		} else {
			req.Header.Set(hdr[i], hdr[i+one])
		}
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		c.T.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := Resp{Code: res.StatusCode, Body: string(raw), Loc: res.Header.Get("Location")}
	_ = json.Unmarshal(raw, &out.JSON)
	return out
}

// Do sends body as JSON, or as a form when it is url.Values. hdr are header name and
// value pairs; an empty value removes that header (the client's CSRF, say).
func (c *Client) Do(method, path string, body any, hdr ...string) Resp {
	c.T.Helper()
	var rd io.Reader
	ctype := ""
	switch b := body.(type) {
	case nil:
	case url.Values:
		rd, ctype = strings.NewReader(b.Encode()), "application/x-www-form-urlencoded"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			c.T.Fatal(err)
		}
		rd, ctype = bytes.NewReader(raw), "application/json"
	}
	return c.send(method, path, rd, ctype, true, hdr...)
}

// Must is Do with no extra headers, failing t unless the status is code.
func (c *Client) Must(code int, method, path string, body any) Resp {
	c.T.Helper()
	r := c.Do(method, path, body)
	if r.Code != code {
		c.T.Fatalf("%s %s: want %d, got %d: %s", method, path, code, r.Code, r.Body)
	}
	return r
}

// Raw sends body as it is (an upload chunk, say) with hdr pairs.
func (c *Client) Raw(method, path string, body []byte, hdr ...string) Resp {
	c.T.Helper()
	return c.send(method, path, bytes.NewReader(body), "", false, hdr...)
}

// Upload posts a multipart form with fields and, when pkg is set, one file field
// "package", as the browser does. The form's csrf field, when set, replaces the header.
func (c *Client) Upload(path string, fields url.Values, pkg []byte) Resp {
	c.T.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, vs := range fields {
		for _, v := range vs {
			_ = mw.WriteField(k, v)
		}
	}
	if pkg != nil {
		fw, _ := mw.CreateFormFile("package", "module.json")
		_, _ = fw.Write(pkg)
	}
	_ = mw.Close()
	csrf := c.CSRF
	if fields.Get("csrf") != "" {
		csrf = ""
	}
	cc := *c
	cc.CSRF = csrf
	return cc.send(http.MethodPost, path, &buf, mw.FormDataContentType(), false)
}

// Session is the client's session cookie value.
func (c *Client) Session() string {
	u, _ := url.Parse(c.Base)
	for _, ck := range c.HTTP.Jar.Cookies(u) {
		if ck.Name == identity.SessionCookie("").Name {
			return ck.Value
		}
	}
	return ""
}

// Caller authenticates c's session with ids, for direct service calls in setup.
func Caller(t *testing.T, ids *identity.Service, c *Client) identity.Caller {
	t.Helper()
	caller, err := ids.Authenticate(context.Background(), identity.Presented{SessionToken: c.Session()})
	if err != nil {
		t.Fatal(err)
	}
	return caller
}

// Str is the string at keys in a decoded JSON object, failing t when it is missing.
func Str(t *testing.T, m map[string]any, keys ...string) string {
	t.Helper()
	var cur any = m
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("no %s in %v", k, m)
		}
		cur = obj[k]
	}
	s, ok := cur.(string)
	if !ok {
		t.Fatalf("%v is not a string in %v", keys, m)
	}
	return s
}

// TOTP is RFC 6238 with SHA-1, 30 seconds and six digits, as identity/params.go sets.
func TOTP(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(strings.TrimRight(secret, "=")))
	if err != nil {
		t.Fatal(err)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-one] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", bin%1000000)
}

// The first-run admin.
const (
	AdminName     = "Avery Admin"
	AdminEmail    = "avery@firm.example"
	AdminPassword = "correct-horse-battery"
)

// FirstRun drives setup over HTTP and returns the signed-in admin. On the way it checks
// that a wrong two-factor code is refused and a spent enrollment token can't be reused;
// two-factor is optional by default, so the admin then skips it and signs in with email
// and password.
func FirstRun(t *testing.T, base string) *Client {
	t.Helper()
	admin := NewClient(t, base)
	enroll := admin.Must(http.StatusCreated, "POST", "/api/v1/setup", map[string]string{
		"firm_name": "Synthetic Firm LLP", "time_zone": "America/Chicago", "admin_name": AdminName,
		"admin_email": AdminEmail, "password": AdminPassword, "backup_passphrase": "synthetic-backup-passphrase",
	})
	token, secret := Str(t, enroll.JSON, "enrollment_token"), Str(t, enroll.JSON, "secret")
	bad := admin.Do("POST", "/api/v1/auth/totp/confirm", map[string]string{"enrollment_token": token, "code": "000000"})
	if bad.Code < http.StatusBadRequest || bad.Code == http.StatusInternalServerError {
		t.Fatalf("wrong code accepted or crashed: %d %s", bad.Code, bad.Body)
	}
	// The challenge is single use: after a wrong code the admin signs in again to enroll.
	if r := admin.Do("POST", "/api/v1/auth/totp/confirm", map[string]string{"enrollment_token": token, "code": TOTP(t, secret, time.Now())}); r.Code != http.StatusUnauthorized {
		t.Fatalf("spent enrollment token reused: %d %s", r.Code, r.Body)
	}
	login := admin.Must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": AdminEmail, "password": AdminPassword})
	if Str(t, login.JSON, "status") != "authenticated" {
		t.Fatal(login.Body)
	}
	admin.CSRF = Str(t, login.JSON, "csrf_token")
	return admin
}

// UserPassword is the synthetic password EnrollUser gives a person named name.
func UserPassword(name string) string { return "synthetic-password-" + name }

// SignIn signs email in over HTTP with password, enrolling two-factor when the firm
// requires it for the person's role.
func SignIn(t *testing.T, base, email, password string) *Client {
	t.Helper()
	c := NewClient(t, base)
	login := c.Must(http.StatusOK, "POST", "/api/v1/auth/login", map[string]string{"email": email, "password": password})
	if Str(t, login.JSON, "status") == "authenticated" {
		c.CSRF = Str(t, login.JSON, "csrf_token")
		return c
	}
	if Str(t, login.JSON, "status") != "enrollment_required" {
		t.Fatal(login.Body)
	}
	en := c.Must(http.StatusOK, "POST", "/api/v1/auth/totp/enroll", map[string]string{"enrollment_token": Str(t, login.JSON, "enrollment_token")})
	done := c.Must(http.StatusOK, "POST", "/api/v1/auth/totp/confirm", map[string]string{
		"enrollment_token": Str(t, en.JSON, "enrollment_token"), "code": TOTP(t, Str(t, en.JSON, "secret"), time.Now()),
	})
	c.CSRF = Str(t, done.JSON, "csrf_token")
	return c
}

// EnrollUser creates a user through the identity service as the signed-in admin and
// signs it in over HTTP. contact binds a client user to its contact; leave it empty for
// staff.
func EnrollUser(t *testing.T, ids *identity.Service, base string, admin *Client, name, email string, contact domain.ID, roles ...domain.Role) (*Client, domain.ID) {
	t.Helper()
	user, err := ids.CreateUser(context.Background(), Caller(t, ids, admin), identity.NewUser{Name: name, Email: email, Password: UserPassword(name), Roles: roles, ContactID: contact})
	if err != nil {
		t.Fatal(err)
	}
	return SignIn(t, base, email, UserPassword(name)), user.ID
}

// Matter opens a matter on practice area area with attorney responsible, and a client
// party who has clientEmail when it is set. extra fields go on the matter body. It
// returns the matter and client contact ids.
func Matter(t *testing.T, admin *Client, area, attorney, name, clientEmail string, extra map[string]any) (matter, clientID string) {
	t.Helper()
	body := map[string]any{"name": name, "practice_area_id": area, "billing_type": "hourly", "responsible_attorney_user_id": attorney}
	for k, v := range extra {
		body[k] = v
	}
	matter = Str(t, admin.Must(http.StatusCreated, "POST", "/api/v1/matters", body).JSON, "id")
	parts := strings.Fields(name)
	clientID = Str(t, admin.Must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{
		"kind": "person", "given_name": "Client", "family_name": parts[len(parts)-one]}).JSON, "id")
	if clientEmail != "" {
		admin.Must(http.StatusCreated, "POST", "/api/v1/contact-emails", map[string]any{"contact_id": clientID, "label": "home", "email": clientEmail, "primary": true})
	}
	admin.Must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{"matter_id": matter, "contact_id": clientID, "role": "client", "primary": true})
	return matter, clientID
}

// TrustKey adds pub as a trusted local signing key over the API.
func TrustKey(admin *Client, name string, pub ed25519.PublicKey) {
	admin.T.Helper()
	admin.Must(http.StatusCreated, "POST", "/api/v1/trusted-signing-keys", map[string]any{"name": name, "public_key_pem": marketplace.PublicKeyPEM(pub)})
}

// TabForm posts an admin Modules tab form (install, update, permissions, uninstall and
// the rest) with the session's CSRF field and returns the redirect's notice or error.
// install and update upload pkg as the package file.
func TabForm(admin *Client, action string, v url.Values, pkg []byte) (notice, problem string) {
	t := admin.T
	t.Helper()
	if v == nil {
		v = url.Values{}
	}
	v.Set("csrf", admin.CSRF)
	var r Resp
	if pkg != nil || action == "install" || action == "update" {
		r = admin.Upload("/admin/modules/"+action, v, pkg)
	} else {
		r = admin.Do("POST", "/admin/modules/"+action, v, "X-CSRF-Token", "")
	}
	if r.Code != http.StatusSeeOther {
		t.Fatalf("tab %s: %d %s", action, r.Code, r.Body)
	}
	u, err := url.Parse(r.Loc)
	if err != nil || !strings.HasPrefix(u.Path, "/admin") {
		t.Fatalf("tab %s redirect %q", action, r.Loc)
	}
	return u.Query().Get("notice"), u.Query().Get("error")
}

// ApproveOnTab approves every undecided permission of module through the admin tab. svc
// and p (the admin) list the module's permissions.
func ApproveOnTab(admin *Client, svc *modules.Service, p domain.Principal, module string) {
	t := admin.T
	t.Helper()
	v, err := svc.Get(context.Background(), p, module)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"module_id": {module}}
	for _, perm := range v.Permissions {
		if perm.Decision == "pending" {
			form.Set("scope:"+perm.Scope, "approve")
		}
	}
	if _, problem := TabForm(admin, "permissions", form, nil); problem != "" {
		t.Fatalf("approve %s: %s", module, problem)
	}
}

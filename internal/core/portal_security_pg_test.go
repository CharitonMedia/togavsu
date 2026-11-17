package core

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/testkit"
)

// TestCORE10PortalSecurityTOTPAndSignOut covers /portal/security password change,
// optional TOTP enrollment (CORE-10), and POST /portal/sign-out.
func TestCORE10PortalSecurityTOTPAndSignOut(t *testing.T) {
	f := newFin(t)
	_, contact := f.matter("Portal Security Estate", "portal.security@client.example", nil)
	cliKit, _ := testkit.EnrollUser(t, f.app.Identity, f.srv.URL, f.admin.kit(), "Portal Sec", "portal.security@client.example", domain.ID(contact), domain.RoleClient)
	cli := wrapClient(cliKit)
	pw := testkit.UserPassword("Portal Sec")

	page := cli.must(http.StatusOK, "GET", "/portal/security", nil)
	if !strings.Contains(page.body, "Change the password") || !strings.Contains(page.body, "/portal/security/totp") {
		t.Fatalf("security page missing password or TOTP: %s", page.body)
	}
	if !strings.Contains(page.body, `action="/portal/sign-out"`) {
		t.Fatalf("security page missing sign-out: %s", page.body)
	}

	if r := cli.do("POST", "/portal/security", url.Values{
		"csrf": {cli.csrf}, "current_password": {pw}, "new_password": {"synthetic-portal-next"}, "confirm_password": {"mismatch"},
	}); r.code != http.StatusBadRequest || !strings.Contains(r.body, "do not match") {
		t.Fatalf("mismatch: %d %s", r.code, r.body)
	}
	changed := cli.do("POST", "/portal/security", url.Values{
		"csrf": {cli.csrf}, "current_password": {pw}, "new_password": {"synthetic-portal-next"}, "confirm_password": {"synthetic-portal-next"},
	})
	if changed.code != http.StatusSeeOther || !strings.Contains(changed.loc, "/portal") {
		t.Fatalf("password change: %d loc=%q body=%s", changed.code, changed.loc, changed.body)
	}
	pw = "synthetic-portal-next"
	// Re-sign-in after ChangePassword revokes other sessions.
	cli = wrapClient(testkit.SignIn(t, f.srv.URL, "portal.security@client.example", pw))

	if r := cli.do("POST", "/portal/security/totp", url.Values{"csrf": {cli.csrf}, "password": {"wrong-password-xx"}}); r.code < 400 {
		t.Fatalf("wrong password enrolled: %d %s", r.code, r.body)
	}
	enroll := cli.must(http.StatusOK, "POST", "/portal/security/totp", url.Values{"csrf": {cli.csrf}, "password": {pw}})
	if !strings.Contains(enroll.body, "Finish setting up two-factor") || !strings.Contains(enroll.body, "enrollment_token") {
		t.Fatalf("enroll page: %s", enroll.body)
	}
	token := between(enroll.body, `name="enrollment_token" value="`, `"`)
	secret := between(enroll.body, `name="secret" value="`, `"`)
	otpauth := between(enroll.body, `name="otpauth_uri" value="`, `"`)
	if token == "" || secret == "" {
		t.Fatalf("missing enroll fields: token=%q secret=%q", token, secret)
	}
	// A wrong code consumes the one-guess challenge; start a fresh enrollment for confirm.
	if r := cli.do("POST", "/portal/security/totp/confirm", url.Values{
		"csrf": {cli.csrf}, "enrollment_token": {token}, "otpauth_uri": {otpauth}, "secret": {secret}, "code": {"000000"},
	}); r.code < 400 {
		t.Fatalf("bad code accepted: %d %s", r.code, r.body)
	}
	enroll = cli.must(http.StatusOK, "POST", "/portal/security/totp", url.Values{"csrf": {cli.csrf}, "password": {pw}})
	token = between(enroll.body, `name="enrollment_token" value="`, `"`)
	secret = between(enroll.body, `name="secret" value="`, `"`)
	otpauth = between(enroll.body, `name="otpauth_uri" value="`, `"`)
	code := testkit.TOTP(t, secret, time.Now())
	ok := cli.must(http.StatusOK, "POST", "/portal/security/totp/confirm", url.Values{
		"csrf": {cli.csrf}, "enrollment_token": {token}, "otpauth_uri": {otpauth}, "secret": {secret}, "code": {code},
	})
	if !strings.Contains(ok.body, "Two-factor is on") && !strings.Contains(ok.body, "recovery codes") && !strings.Contains(ok.body, "recovery-codes") {
		t.Fatalf("confirm: %s", ok.body)
	}
	meID := str(t, cli.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")
	on, err := f.app.Identity.TOTPConfirmed(context.Background(), domain.ID(meID))
	if err != nil || !on {
		t.Fatalf("TOTPConfirmed: %v on=%v", err, on)
	}

	out := cli.do("POST", "/portal/sign-out", url.Values{"csrf": {cli.csrf}})
	if out.code != http.StatusSeeOther || !strings.Contains(out.loc, "/login") {
		t.Fatalf("sign-out: %d loc=%q", out.code, out.loc)
	}
	if r := cli.do("GET", "/portal/security", nil); r.code != http.StatusSeeOther || !strings.Contains(r.loc, "/login") {
		t.Fatalf("after sign-out: %d loc=%q", r.code, r.loc)
	}
}

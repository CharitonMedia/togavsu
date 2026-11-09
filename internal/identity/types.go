package identity

import (
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

const (
	statusTOTP            = "totp_required"
	statusEnroll          = "enrollment_required"
	statusAuthenticated   = "authenticated"
	statusEnrollChallenge = "totp_enrollment_required"
	statusReset           = "totp_reset_pending"

	purposeTOTP     = "totp"
	purposeEnroll   = "enroll"
	purposeReset    = "reset"
	purposePassword = "password"
	purposeReauth   = "reauth"
	cookieName      = "togavsu_session"
	signInField     = "sign_in"
)

// LoginResult is the password or second-factor outcome. SessionToken is set only
// when Status is authenticated. The HTTP layer copies it into SessionCookie.
type LoginResult struct {
	Status          string
	TOTPToken       string
	EnrollmentToken string
	User            domain.User
	CSRFToken       string
	SessionToken    string
	SessionID       domain.ID
	RecoveryCodes   []string
}

// Enrollment is the TOTP secret shown once, before a session exists.
type Enrollment struct {
	Status          string
	EnrollmentToken string
	OTPAuthURI      string
	Secret          string
	ExpiresAt       time.Time
}

// Caller is an authenticated session or bearer token.
type Caller struct {
	Principal domain.Principal
	Scopes    []string
}

// Presented is what the HTTP layer extracted. A write from a cookie needs CSRF.
type Presented struct {
	SessionToken string
	Bearer       string
	CSRF         string
	Write        bool
}

// Cookie is the browser session cookie the HTTP layer sets.
type Cookie struct {
	Name     string
	Value    string
	Path     string
	SameSite string
	HTTPOnly bool
	Secure   bool
}

// SetupInput is the first-run firm and admin.
type SetupInput struct {
	FirmName         string
	TimeZone         string
	AdminName        string
	AdminEmail       string
	Password         string
	BackupPassphrase string
}

// Factor is one recent proof. Exactly one field is set.
type Factor struct {
	Password    string
	TOTPCode    string
	Recovery    string
	ReauthToken string
}

// Issue is a request to mint a bearer token.
type Issue struct {
	Name     string
	UserID   domain.ID
	ModuleID string
	Scopes   []string
	Approved []string
}

// TokenView is token metadata. The secret is not included.
type TokenView struct {
	ID        domain.ID
	Name      string
	UserID    domain.ID
	ModuleID  string
	Scopes    []string
	Revoked   bool
	CreatedAt time.Time
}

// SessionView is session metadata. The cookie value is not included.
type SessionView struct {
	ID            domain.ID
	UserID        domain.ID
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
	Revoked       bool
	Current       bool
}

// ResetMailer receives a password-reset token. Production mail is a later package.
type ResetMailer interface {
	SendPasswordReset(email, token string) error
}

// SessionCookie describes the cookie for a session token.
func SessionCookie(token string) Cookie {
	return Cookie{Name: cookieName, Value: token, Path: "/", SameSite: "Lax", HTTPOnly: true, Secure: true}
}

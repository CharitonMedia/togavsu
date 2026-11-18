package modules

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// PublicStepHeader carries an HMAC step token for public module pages (CORE-32).
const PublicStepHeader = "X-Togavsu-Public-Step"

// PublicCSRFCookie is the double-submit CSRF cookie for public pages (CORE-32).
const PublicCSRFCookie = "togavsu_public_csrf"

// PublicCSRFHeader is the CSRF header matching PublicCSRFCookie.
const PublicCSRFHeader = "X-CSRF-Token"

// PublicCSRFPassHeader is set by the core on every proxied public-page request so the
// module can embed the double-submit token in forms/HTMX (the cookie is HttpOnly).
const PublicCSRFPassHeader = "X-Togavsu-Public-CSRF"

// StepToken is a short-lived HMAC token for one public-page session on one module.
type StepToken struct {
	ModuleID  string `json:"module_id"`
	ExpiresAt int64  `json:"exp"` // unix seconds
	Nonce     string `json:"nonce"`
}

// PublicStepKey returns the core-only HMAC key for public-page step credentials (CORE-32).
// Modules cannot derive this key; forged webhook-secret tokens are refused.
func (s *Service) PublicStepKey() []byte { return s.subkey("public-step") }

// IssueStepToken builds an HMAC step token for moduleID, valid for ttl (CORE-32).
// The public-page proxy signs with Service.PublicStepKey (core-only). The kit helper
// still accepts a caller-supplied secret for local tests.
func IssueStepToken(secret []byte, moduleID string, ttl time.Duration, now time.Time) (string, error) {
	moduleID = strings.TrimSpace(moduleID)
	if moduleID == "" || len(secret) == 0 {
		return "", domain.Invalid("step_token", "module and secret are required")
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	st := StepToken{
		ModuleID:  moduleID,
		ExpiresAt: now.UTC().Add(ttl).Unix(),
		Nonce:     hex.EncodeToString(nonce[:]),
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil)), nil
}

// VerifyStepToken checks an HMAC step token and returns its claims when valid.
func VerifyStepToken(secret []byte, token string, now time.Time) (StepToken, error) {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || payload == "" || sig == "" || len(secret) == 0 {
		return StepToken{}, domain.Unauthenticated("invalid step token")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return StepToken{}, domain.Unauthenticated("invalid step token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return StepToken{}, domain.Unauthenticated("invalid step token")
	}
	var st StepToken
	if err := json.Unmarshal(raw, &st); err != nil || st.ModuleID == "" {
		return StepToken{}, domain.Unauthenticated("invalid step token")
	}
	if now.UTC().Unix() > st.ExpiresAt {
		return StepToken{}, domain.Unauthenticated("step token expired")
	}
	return st, nil
}

// publicCSRF is the signed payload for a public-page CSRF token (CORE-32).
// Tokens are HMAC-signed with the module webhook secret so the core can check
// POSTs without allow-same-origin (opaque-origin pages cannot rely on cookie JS).
type publicCSRF struct {
	ModuleID  string `json:"module_id"`
	ExpiresAt int64  `json:"exp"`
	Nonce     string `json:"nonce"`
}

// IssuePublicCSRF returns a random CSRF token (kit helper for tests and non-proxied forms).
func IssuePublicCSRF() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// IssueSignedPublicCSRF builds an HMAC CSRF token for moduleID (CORE-32).
// secret is the module webhook secret. ttl defaults to 12 hours.
func IssueSignedPublicCSRF(secret []byte, moduleID string, ttl time.Duration, now time.Time) (string, error) {
	moduleID = strings.TrimSpace(moduleID)
	if moduleID == "" || len(secret) == 0 {
		return "", domain.Invalid("csrf", "module and secret are required")
	}
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	c := publicCSRF{
		ModuleID:  moduleID,
		ExpiresAt: now.UTC().Add(ttl).Unix(),
		Nonce:     hex.EncodeToString(nonce[:]),
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("public-csrf|" + payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil)), nil
}

// CheckPublicCSRF compares the double-submit cookie and header/form value (random tokens).
func CheckPublicCSRF(cookie, submitted string) error {
	if cookie == "" || submitted == "" || !hmac.Equal([]byte(cookie), []byte(submitted)) {
		return domain.Forbidden("csrf required")
	}
	return nil
}

// CheckSignedPublicCSRF verifies a signed public CSRF token for moduleID.
// When the cookie is present (same-site navigations), it must match the submitted
// value; opaque-origin HTMX may omit the cookie, so MAC verification alone is enough.
func CheckSignedPublicCSRF(secret []byte, moduleID, cookie, submitted string, now time.Time) error {
	moduleID = strings.TrimSpace(moduleID)
	if submitted == "" || moduleID == "" || len(secret) == 0 {
		return domain.Forbidden("csrf required")
	}
	payload, sig, ok := strings.Cut(submitted, ".")
	if !ok || payload == "" || sig == "" {
		return domain.Forbidden("csrf required")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("public-csrf|" + payload))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return domain.Forbidden("csrf required")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return domain.Forbidden("csrf required")
	}
	var c publicCSRF
	if err := json.Unmarshal(raw, &c); err != nil || c.ModuleID != moduleID {
		return domain.Forbidden("csrf required")
	}
	if now.UTC().Unix() > c.ExpiresAt {
		return domain.Forbidden("csrf required")
	}
	if cookie != "" && !hmac.Equal([]byte(cookie), []byte(submitted)) {
		return domain.Forbidden("csrf required")
	}
	return nil
}

// LooksLikePublicStep reports a bearer that has the step-token shape (payload.hex-mac).
func LooksLikePublicStep(token string) bool {
	payload, sig, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || payload == "" || len(sig) != 64 {
		return false
	}
	for _, c := range sig {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// AuthenticatePublicStep verifies a core-signed public-page credential and returns the
// module id plus its approved scopes. The credential is the required public-page
// Authorization bearer for CORE-32 server calls (limited to public_operations).
func (s *Service) AuthenticatePublicStep(ctx context.Context, token string) (moduleID string, scopes []string, err error) {
	st, err := VerifyStepToken(s.PublicStepKey(), token, s.now())
	if err != nil {
		return "", nil, &domain.APIError{Code: "unauthenticated", Message: "invalid public step token"}
	}
	in, err := s.Installed(ctx, st.ModuleID)
	if err != nil || !in.Enabled {
		return "", nil, &domain.APIError{Code: "unauthenticated", Message: "invalid public step token"}
	}
	if !in.Approved["public_pages:serve"] {
		return "", nil, &domain.APIError{Code: "scope_denied", Message: "module lacks the public_pages:serve scope"}
	}
	for scope, ok := range in.Approved {
		if ok {
			scopes = append(scopes, scope)
		}
	}
	return st.ModuleID, scopes, nil
}

// RequirePublicOperation refuses op when it is not listed under public_operations (CORE-32).
func (s *Service) RequirePublicOperation(ctx context.Context, moduleID, op string) error {
	in, err := s.Installed(ctx, moduleID)
	if err != nil {
		return err
	}
	if !in.Manifest.AllowsPublicOperation(op) {
		return &domain.APIError{Code: "scope_denied", Message: "operation " + op + " is not in public_operations"}
	}
	return nil
}

// CheckPublicStep verifies a core-signed public-page step token and that op is in
// public_operations (CORE-32).
func (s *Service) CheckPublicStep(ctx context.Context, moduleID, token, op string) error {
	st, err := VerifyStepToken(s.PublicStepKey(), token, s.now())
	if err != nil || st.ModuleID != moduleID {
		return &domain.APIError{Code: "scope_denied", Message: "invalid public step token"}
	}
	return s.RequirePublicOperation(ctx, moduleID, op)
}

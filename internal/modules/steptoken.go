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

// StepToken is a short-lived HMAC token for one public-page session on one module.
type StepToken struct {
	ModuleID  string `json:"module_id"`
	ExpiresAt int64  `json:"exp"` // unix seconds
	Nonce     string `json:"nonce"`
}

// IssueStepToken builds an HMAC step token for moduleID, valid for ttl (CORE-32).
// secret is typically the module's webhook secret so the module can VerifyStepToken.
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

// IssuePublicCSRF returns a random CSRF token for a public page form.
func IssuePublicCSRF() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// CheckPublicCSRF compares the double-submit cookie and header/form value.
func CheckPublicCSRF(cookie, submitted string) error {
	if cookie == "" || submitted == "" || !hmac.Equal([]byte(cookie), []byte(submitted)) {
		return domain.Forbidden("csrf required")
	}
	return nil
}

// CheckPublicStep verifies a public-page step token and that op is in public_operations (CORE-32).
func (s *Service) CheckPublicStep(ctx context.Context, moduleID, token, op string) error {
	in, err := s.Installed(ctx, moduleID)
	if err != nil {
		return err
	}
	row, err := s.row(ctx, moduleID)
	if err != nil {
		return err
	}
	secret, err := s.open(row.WebhookSecret)
	if err != nil {
		return &domain.APIError{Code: "scope_denied", Message: "public step token could not be verified"}
	}
	st, err := VerifyStepToken([]byte(secret), token, s.now())
	if err != nil || st.ModuleID != moduleID {
		return &domain.APIError{Code: "scope_denied", Message: "invalid public step token"}
	}
	if !in.Manifest.AllowsPublicOperation(op) {
		return &domain.APIError{Code: "scope_denied", Message: "operation " + op + " is not in public_operations"}
	}
	return nil
}

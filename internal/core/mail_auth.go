package core

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
	"github.com/CharitonMedia/togavsu/internal/modules"
)

const (
	workflowMailMatching = "mail_matching"
	workflowMailFiling   = "mail_filing"
	mailAuthTTL          = 15 * time.Minute
)

type mailAuthClaims struct {
	Workflow   string   `json:"wf"`
	Module     string   `json:"mod"`
	MatterIDs  []string `json:"matters,omitempty"`
	MatterID   string   `json:"matter,omitempty"`
	MessageKey string   `json:"msg,omitempty"`
	ExpiresAt  int64    `json:"exp"`
	IssuedAt   int64    `json:"iat"`
	Nonce      string   `json:"nonce"`
}

// MailFilingAuthorization is the issueMailFilingGrant response.
type MailFilingAuthorization struct {
	TaskAuthorization string    `json:"task_authorization"`
	ModuleID          string    `json:"module_id"`
	MatterID          domain.ID `json:"matter_id"`
	MessageKey        string    `json:"message_key"`
	ExpiresAt         string    `json:"expires_at"`
	AllowedOperations []string  `json:"allowed_operations"`
	IssuedBy          string    `json:"issued_by"`
}

var mailFilingOperations = []string{
	"createNote", "createFileUpload", "getFileUpload", "putFileChunk",
	"completeFileUpload", "publishFile", "bulkFile", "createFileLink",
}

func (a *App) mailAuthMAC(payload string) string {
	mac := hmac.New(sha256.New, a.Modules.MailAuthKey())
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *App) sealMailAuth(c mailAuthClaims) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	c.Nonce = hex.EncodeToString(nonce)
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	return payload + "." + a.mailAuthMAC(payload), nil
}

func (a *App) openMailAuth(token string) (mailAuthClaims, error) {
	denied := domain.Forbidden("the task authorization is missing, invalid or expired")
	payload, mac, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || !hmac.Equal([]byte(mac), []byte(a.mailAuthMAC(payload))) {
		return mailAuthClaims{}, denied
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return mailAuthClaims{}, denied
	}
	var c mailAuthClaims
	if err := json.Unmarshal(raw, &c); err != nil || c.Module == "" || c.Workflow == "" {
		return mailAuthClaims{}, denied
	}
	if time.Now().Unix() >= c.ExpiresAt {
		return mailAuthClaims{}, denied
	}
	return c, nil
}

func (a *App) verifyMailMatchingGrant(ctx context.Context, p domain.Principal, credential string) error {
	c, err := a.openMailAuth(credential)
	if err != nil {
		return err
	}
	if c.Workflow != workflowMailMatching {
		return domain.Forbidden("the task authorization is not a mail matching grant")
	}
	if p.ModuleID == "" || p.ModuleID != c.Module {
		return domain.Forbidden("the task authorization was issued to another module")
	}
	on, err := a.moduleEnabled(ctx, c.Module)
	if err != nil {
		return err
	}
	if !on {
		return domain.Forbidden("the module is not installed and enabled")
	}
	return nil
}

func (a *App) verifyMailFilingGrant(ctx context.Context, p domain.Principal, credential string, matter domain.ID) error {
	c, err := a.openMailAuth(credential)
	if err != nil {
		return err
	}
	if c.Workflow != workflowMailFiling {
		return domain.Forbidden("the task authorization is not a mail filing grant")
	}
	if p.ModuleID == "" || p.ModuleID != c.Module {
		return domain.Forbidden("the task authorization was issued to another module")
	}
	if matter.IsZero() || string(matter) != c.MatterID {
		return domain.NotFound("matter")
	}
	on, err := a.moduleEnabled(ctx, c.Module)
	if err != nil {
		return err
	}
	if !on {
		return domain.Forbidden("the module is not installed and enabled")
	}
	view, err := a.Practice.Policy.Walls.View(ctx, domain.Principal{}, matter)
	if err != nil {
		return err
	}
	if view.Walled {
		return domain.NotFound("matter")
	}
	return nil
}

func (a *App) requireMailFilingFromContext(ctx context.Context, p domain.Principal, matter domain.ID) error {
	if !moduleOwn(p) {
		return nil
	}
	cred := modules.TaskAuth(ctx)
	if cred == "" {
		return &domain.APIError{Code: "scope_denied", Message: "a mail filing grant is required"}
	}
	return a.verifyMailFilingGrant(ctx, p, cred, matter)
}

func (a *App) optionalMailFilingFromContext(ctx context.Context, p domain.Principal, matter domain.ID) error {
	if !moduleOwn(p) {
		return nil
	}
	cred := modules.TaskAuth(ctx)
	if cred == "" || strings.HasPrefix(cred, "tpa1.") {
		return nil
	}
	c, err := a.openMailAuth(cred)
	if err != nil {
		return err
	}
	if c.Workflow != workflowMailFiling {
		return domain.Forbidden("the task authorization is not a mail filing grant")
	}
	return a.verifyMailFilingGrant(ctx, p, cred, matter)
}

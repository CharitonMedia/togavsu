package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/dashboard"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/help"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// Deployment is what the supervisor needs to run one module container. Secrets are
// mounted as Docker secrets; the core never touches the Docker socket (PRD 6).
type Deployment struct {
	Module  string            `json:"module"`
	Version string            `json:"version"`
	Image   string            `json:"image"`
	Digest  string            `json:"digest"`
	Port    int               `json:"port"`
	Secrets map[string]string `json:"secrets"`
}

// Runtime starts and stops module containers through the supervisor.
type Runtime interface {
	Deploy(ctx context.Context, d Deployment) error
	Stop(ctx context.Context, module string) error
	Remove(ctx context.Context, module string) error
	// BaseURL is where the core reaches the module's HTTP server inside the stack.
	BaseURL(module string) string
}

// Backup is one supervisor backup.
type Backup struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	SHA256    string    `json:"sha256"`
	Bytes     int64     `json:"bytes"`
	// Deleting and Source mirror the supervisor's BackupMeta: Source is empty for an
	// archive this installation wrote and "upload" or "destination:<id>" otherwise.
	Deleting bool   `json:"deleting,omitempty"`
	Source   string `json:"source,omitempty"`
}

// Backups makes and finds full backups (database with every module schema).
type Backups interface {
	Create(ctx context.Context, reason string) (Backup, error)
	Get(ctx context.Context, id string) (Backup, error)
}

// Tokens issues and revokes the module's scoped API token (PRD 9.3). Identity
// implements it through IssueToken with ModuleID and the approved list.
type Tokens interface {
	IssueModuleToken(ctx context.Context, admin domain.Principal, module string, scopes []string) (id, secret string, err error)
	RevokeModuleToken(ctx context.Context, admin domain.Principal, id string) error
}

// Help is the help center's module guide registry (internal/help).
type Help interface {
	ReplacePages(ctx context.Context, p domain.Principal, module string, in []help.PageInput) ([]help.Page, error)
	SetModuleEnabled(ctx context.Context, module string, enabled bool) error
	RemoveModule(ctx context.Context, module string) error
}

// Widgets is the home-screen widget catalog (internal/dashboard.Catalog).
type Widgets interface {
	AddModule(module string, ws []dashboard.ManifestWidget, src func(widgetID string) dashboard.Source) error
	RemoveModule(module string)
}

// Settings reads and writes registry settings (internal/settings.Registry). Set
// requires an admin and writes the audit row with old and new values.
type Settings interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, who domain.Principal) (settings.Change, error)
}

// Verifier checks a module package against the firm's trust root before the runtime uses
// anything in it (PRD 9.3): keys published through the marketplace and the firm's trusted
// local keys. The marketplace component owns that store, the package signing format and
// the contract's trusted-signing-keys routes; *marketplace.Service satisfies this.
type Verifier interface {
	// VerifyLocalFile checks an uploaded signed package file (the marketplace's
	// SignedPackage JSON) and returns the verified package bytes.
	VerifyLocalFile(ctx context.Context, p domain.Principal, file []byte) (marketplace.Verified, error)
	// FetchForInstall downloads and checks a listing's version. A pulled module fails:
	// it can't be installed from the marketplace (PRD 10.4).
	FetchForInstall(ctx context.Context, p domain.Principal, listingID, version string) (marketplace.Verified, error)
}

// Users loads a user for the context token check.
type Users interface {
	Get(ctx context.Context, id domain.ID) (domain.User, error)
}

// Supervisor is the core's client for the supervisor's local API (internal/supervisor),
// authenticated with the supervisor secret from bootstrap.
type Supervisor struct {
	URL    string
	Secret string
	HTTP   *http.Client
	// ModuleURL formats the in-stack address of a module from its id and port, for
	// example "http://togavsu-mod-%s:%d"; the platform's Compose network names it.
	ModuleURL string
	Ports     func(module string) int
}

func (s *Supervisor) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return http.DefaultClient
}

func (s *Supervisor) call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.URL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.Secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, limits().ProxyBodyBytes))
		return fmt.Errorf("modules: supervisor %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Deploy hands the supervisor the image, digest and secrets, then starts the container.
// ponytail: ceiling: PUT /v1/modules/{name} (image, digest, secrets) is proposed to the
// platform owner in this PR; the supervisor has only start and stop today. Upgrade path:
// the supervisor creates the container from the digest with the secrets mounted.
// Trigger: integrate-extensions wires the supervisor module endpoints.
func (s *Supervisor) Deploy(ctx context.Context, d Deployment) error {
	if err := s.call(ctx, http.MethodPut, "/v1/modules/"+url.PathEscape(d.Module), d, nil); err != nil {
		return err
	}
	return s.call(ctx, http.MethodPost, "/v1/modules/"+url.PathEscape(d.Module)+"/start", nil, nil)
}

// Stop stops the module container.
func (s *Supervisor) Stop(ctx context.Context, module string) error {
	return s.call(ctx, http.MethodPost, "/v1/modules/"+url.PathEscape(module)+"/stop", nil, nil)
}

// Remove deletes the module container (proposed DELETE /v1/modules/{name}).
func (s *Supervisor) Remove(ctx context.Context, module string) error {
	return s.call(ctx, http.MethodDelete, "/v1/modules/"+url.PathEscape(module), nil, nil)
}

// BaseURL is the module's address on the stack network.
func (s *Supervisor) BaseURL(module string) string {
	var port int
	if s.Ports != nil {
		port = s.Ports(module)
	}
	return fmt.Sprintf(s.ModuleURL, module, port)
}

// Create takes a full backup (POST /v1/backups).
func (s *Supervisor) Create(ctx context.Context, reason string) (Backup, error) {
	var b Backup
	err := s.call(ctx, http.MethodPost, "/v1/backups", map[string]string{"reason": reason}, &b)
	return b, err
}

// Get reads one backup (GET /v1/backups/{id}).
func (s *Supervisor) Get(ctx context.Context, id string) (Backup, error) {
	var b Backup
	err := s.call(ctx, http.MethodGet, "/v1/backups/"+url.PathEscape(id), nil, &b)
	return b, err
}

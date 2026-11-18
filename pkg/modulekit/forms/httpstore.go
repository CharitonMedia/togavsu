package forms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// HTTPSecretStore talks to the core module secret API
// (PUT/GET /api/v1/modules/{id}/secrets/{name}) with the module's bearer.
// CreateSecretIfAbsent uses If-None-Match: * so concurrent EnsureKey creators
// cannot overwrite an existing sealed key (CORE-33).
type HTTPSecretStore struct {
	BaseURL    string // e.g. http://core/api/v1/modules/intake/secrets
	Token      string
	HTTPClient *http.Client
}

func (s *HTTPSecretStore) client() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return http.DefaultClient
}

func (s *HTTPSecretStore) secretURL(name string) (string, error) {
	base := strings.TrimRight(s.BaseURL, "/")
	if base == "" {
		return "", fmt.Errorf("forms: HTTPSecretStore BaseURL is required")
	}
	return base + "/" + url.PathEscape(name), nil
}

// PutSecret implements SecretStore (overwrite).
func (s *HTTPSecretStore) PutSecret(ctx context.Context, name, value string) error {
	return s.put(ctx, name, value, false)
}

// CreateSecretIfAbsent implements CreateIfAbsentStore via If-None-Match: *.
// A 409 conflict means another writer won; created is false and err is nil so
// EnsureKey re-reads the stored key.
func (s *HTTPSecretStore) CreateSecretIfAbsent(ctx context.Context, name, value string) (bool, error) {
	err := s.put(ctx, name, value, true)
	if err == nil {
		return true, nil
	}
	if isConflict(err) {
		return false, nil
	}
	return false, err
}

// GetSecret implements SecretStore.
func (s *HTTPSecretStore) GetSecret(ctx context.Context, name string) (string, error) {
	u, err := s.secretURL(name)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	s.authorize(req)
	res, err := s.client().Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode == http.StatusNotFound {
		return "", ErrSecretNotFound
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("forms: get secret: %s", secretAPIError(res.StatusCode, body))
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.Value, nil
}

func (s *HTTPSecretStore) put(ctx context.Context, name, value string, createIfAbsent bool) error {
	u, err := s.secretURL(name)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{"value": value})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if createIfAbsent {
		req.Header.Set("If-None-Match", "*")
	}
	s.authorize(req)
	res, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusOK {
		return nil
	}
	if res.StatusCode == http.StatusConflict {
		return &secretConflictError{msg: secretAPIError(res.StatusCode, body)}
	}
	return fmt.Errorf("forms: put secret: %s", secretAPIError(res.StatusCode, body))
}

func (s *HTTPSecretStore) authorize(req *http.Request) {
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
}

func secretAPIError(code int, body []byte) string {
	var problem struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &problem) == nil && (problem.Code != "" || problem.Message != "") {
		if problem.Message != "" {
			return fmt.Sprintf("%d %s: %s", code, problem.Code, problem.Message)
		}
		return fmt.Sprintf("%d %s", code, problem.Code)
	}
	return fmt.Sprintf("%d %s", code, strings.TrimSpace(string(body)))
}

type secretConflictError struct{ msg string }

func (e *secretConflictError) Error() string { return e.msg }

func isConflict(err error) bool {
	var c *secretConflictError
	return errors.As(err, &c)
}

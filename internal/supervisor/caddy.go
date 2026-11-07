package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

var errLoadRejected = errors.New("caddy rejected the load")

// CaddyProxy switches the public upstream and records it so a Caddy restart
// keeps the same core.
type CaddyProxy struct {
	AdminURL     string
	AdminSocket  string
	AdminListen  string
	HTTPListen   string
	HTTPSListen  string
	UpstreamFile string
	Domain       string
	HTTP         *http.Client
}

func (c *CaddyProxy) Switch(ctx context.Context, upstream string) error {
	if err := validUpstream(upstream); err != nil {
		return err
	}
	prev, _ := os.ReadFile(c.UpstreamFile)
	prevUpstream := strings.TrimSpace(string(prev))
	if prevUpstream == "" {
		prevUpstream = "core:8080"
	}
	err := c.post(ctx, upstream)
	if err != nil {
		if errors.Is(err, errLoadRejected) {
			c.restoreFile(prev)
			return err
		}
		return c.reconcile(ctx, upstream, prevUpstream, prev, err)
	}
	if err := writeFileAtomic(c.UpstreamFile, []byte(upstream+"\n"), 0o644); err != nil {
		// /load already succeeded. A later file error must not let the
		// updater delete the core Caddy is still serving.
		return c.reconcile(ctx, upstream, prevUpstream, prev, err)
	}
	return nil
}

// reconcile runs when /load does not return a usable response. Caddy may
// already have applied the new upstream. Roll that live config back before
// the caller deletes the new core.
func (c *CaddyProxy) reconcile(ctx context.Context, upstream, prevUpstream string, prev []byte, loadErr error) error {
	dials, getErr := c.activeDials(ctx)
	if getErr != nil {
		return fmt.Errorf("%w: %v", ErrProxyUnreconciled, loadErr)
	}
	if containsDial(dials, upstream) {
		if err := c.post(ctx, prevUpstream); err != nil {
			return fmt.Errorf("%w: %v", ErrProxyUnreconciled, err)
		}
		dials, getErr = c.activeDials(ctx)
		if getErr != nil || containsDial(dials, upstream) {
			return fmt.Errorf("%w: live upstream is still %s", ErrProxyUnreconciled, upstream)
		}
	}
	c.restoreFile(prev)
	return loadErr
}

func (c *CaddyProxy) post(ctx context.Context, upstream string) error {
	body, err := json.Marshal(caddyConfig(upstream, c.adminListen(), c.httpListen(), c.httpsListen(), c.domain()))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.AdminURL, "/")+"/load", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.client()
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("%w: %s %s", errLoadRejected, res.Status, strings.TrimSpace(string(msg)))
	}
	return nil
}

func (c *CaddyProxy) activeDials(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.AdminURL, "/")+"/config/", nil)
	if err != nil {
		return nil, err
	}
	res, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("caddy config %s", res.Status)
	}
	var cfg any
	if err := json.NewDecoder(res.Body).Decode(&cfg); err != nil {
		return nil, err
	}
	var dials []string
	collectDials(cfg, &dials)
	return dials, nil
}

func (c *CaddyProxy) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	if sock := c.socketPath(); sock != "" {
		return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}}}
	}
	return http.DefaultClient
}

func (c *CaddyProxy) ActiveUpstream(ctx context.Context) (string, error) {
	dials, err := c.activeDials(ctx)
	if err != nil {
		return "", err
	}
	seen := map[string]struct{}{}
	var uniq []string
	for _, dial := range dials {
		if _, ok := seen[dial]; ok {
			continue
		}
		seen[dial] = struct{}{}
		uniq = append(uniq, dial)
	}
	if len(uniq) != 1 {
		return "", ErrProxyUnknown
	}
	return uniq[0], nil
}

func (c *CaddyProxy) socketPath() string {
	if c != nil && c.AdminSocket != "" {
		return c.AdminSocket
	}
	listen := ""
	if c != nil {
		listen = c.AdminListen
	}
	if strings.HasPrefix(listen, "unix//") {
		return strings.TrimPrefix(listen, "unix/")
	}
	return ""
}

func collectDials(v any, out *[]string) {
	switch val := v.(type) {
	case map[string]any:
		for key, child := range val {
			if key == "dial" {
				if s, ok := child.(string); ok {
					*out = append(*out, s)
				}
			}
			collectDials(child, out)
		}
	case []any:
		for _, child := range val {
			collectDials(child, out)
		}
	}
}

func containsDial(dials []string, upstream string) bool {
	for _, dial := range dials {
		if dial == upstream {
			return true
		}
	}
	return false
}

func (c *CaddyProxy) restoreFile(prev []byte) {
	if len(prev) == 0 {
		_ = os.Remove(c.UpstreamFile)
		return
	}
	_ = os.WriteFile(c.UpstreamFile, prev, 0o644)
}

func validUpstream(upstream string) error {
	host, port, err := net.SplitHostPort(upstream)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("upstream: %w", ErrBadRequest)
	}
	if strings.ContainsAny(host, " \t/\\") {
		return fmt.Errorf("upstream: %w", ErrBadRequest)
	}
	return nil
}

func (c *CaddyProxy) adminListen() string {
	if c.AdminListen != "" {
		return c.AdminListen
	}
	if c.AdminSocket != "" {
		return "unix/" + c.AdminSocket
	}
	return "unix//run/caddy-admin/admin.sock"
}

func (c *CaddyProxy) httpListen() string {
	if c.HTTPListen != "" {
		return c.HTTPListen
	}
	return ":80"
}

func (c *CaddyProxy) httpsListen() string {
	if c.HTTPSListen != "" {
		return c.HTTPSListen
	}
	return ":443"
}

func (c *CaddyProxy) domain() string {
	if c != nil && c.Domain != "" {
		return c.Domain
	}
	if d := strings.TrimSpace(os.Getenv("TOGAVSU_DOMAIN")); d != "" {
		return d
	}
	return "localhost"
}

func caddyConfig(upstream, adminListen, httpListen, httpsListen, domain string) map[string]any {
	if domain == "" {
		domain = "localhost"
	}
	hostMatch := map[string]any{"host": []string{domain}}
	proxy := map[string]any{
		"match": []any{hostMatch},
		"handle": []any{map[string]any{
			"handler":   "reverse_proxy",
			"upstreams": []any{map[string]any{"dial": upstream}},
		}},
	}
	ready := map[string]any{
		"match": []any{map[string]any{"path": []string{"/ready"}}},
		"handle": []any{map[string]any{
			"handler":     "static_response",
			"status_code": 200,
			"body":        "ok",
		}},
	}
	httpsReady := map[string]any{
		"match": []any{map[string]any{"host": []string{domain}, "path": []string{"/ready"}}},
		"handle": []any{map[string]any{
			"handler":     "static_response",
			"status_code": 200,
			"body":        "ok",
		}},
	}
	// Catch-all HTTP redirect matches deploy/caddy/Caddyfile. /ready stays
	// on HTTP because its route is listed first.
	httpRedirect := map[string]any{
		"handle": []any{map[string]any{
			"handler":     "static_response",
			"status_code": 308,
			"headers": map[string]any{
				"Location": []string{"https://{http.request.host}{http.request.uri}"},
			},
		}},
	}
	return map[string]any{
		"admin": map[string]any{"listen": adminListen},
		"apps": map[string]any{
			"http": map[string]any{
				"servers": map[string]any{
					"http": map[string]any{
						"listen": []string{httpListen},
						"routes": []any{ready, httpRedirect},
					},
					"https": map[string]any{
						"listen": []string{httpsListen},
						"routes": []any{httpsReady, proxy},
						// The HTTP server already redirects. Automatic redirects
						// would bind port 80 beside the configured HTTP listener.
						"automatic_https": map[string]any{"disable_redirects": true},
					},
				},
			},
			"tls": map[string]any{
				"automation": map[string]any{
					"policies": []any{map[string]any{
						"subjects": []string{domain},
						"issuers":  []any{map[string]any{"module": "internal"}},
					}},
				},
			},
		},
	}
}

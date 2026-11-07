package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
)

// CaddyProxy switches the public upstream and records it so a Caddy restart
// keeps the same core.
type CaddyProxy struct {
	AdminURL     string
	AdminListen  string
	HTTPListen   string
	HTTPSListen  string
	UpstreamFile string
	HTTP         *http.Client
}

func (c *CaddyProxy) Switch(ctx context.Context, upstream string) error {
	if err := validUpstream(upstream); err != nil {
		return err
	}
	prev, _ := os.ReadFile(c.UpstreamFile)
	if err := writeFileAtomic(c.UpstreamFile, []byte(upstream+"\n"), 0o644); err != nil {
		return err
	}
	body, err := json.Marshal(caddyConfig(upstream, c.adminListen(), c.httpListen(), c.httpsListen()))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.AdminURL, "/")+"/load", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		c.restoreFile(prev)
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		c.restoreFile(prev)
		return fmt.Errorf("caddy load %s: %s", res.Status, strings.TrimSpace(string(msg)))
	}
	return nil
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
	return "0.0.0.0:2019"
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

func caddyConfig(upstream, adminListen, httpListen, httpsListen string) map[string]any {
	proxy := map[string]any{
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
	return map[string]any{
		"admin": map[string]any{"listen": adminListen},
		"apps": map[string]any{
			"http": map[string]any{
				"servers": map[string]any{
					"http": map[string]any{
						"listen": []string{httpListen},
						"routes": []any{ready},
					},
					"https": map[string]any{
						"listen": []string{httpsListen},
						"routes": []any{ready, proxy},
					},
				},
			},
			"tls": map[string]any{
				"automation": map[string]any{
					"policies": []any{map[string]any{
						"issuers": []any{map[string]any{"module": "internal"}},
					}},
				},
			},
		},
	}
}

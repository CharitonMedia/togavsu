package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// CoreFilesPath is the core's internal file-bytes route. GET streams the
// core's export of every file version and rendition (Markdown copies
// included) from every storage backend the backup covers; PUT hands the same
// stream back after a restore, and the core writes each object where the
// restored database says it lives and checks its SHA-256 there. Both take the
// supervisor token as a bearer.
const CoreFilesPath = "/internal/v1/file-bytes"

// CoreFiles is the supervisor's client for the core's file export and import.
type CoreFiles struct {
	// Upstream returns the core's host:port. Empty uses core:8080.
	Upstream func() string
	Token    string
	HTTP     *http.Client
	// Wait bounds how long Import retries while the restarted core is not
	// answering yet. Zero is three minutes.
	Wait time.Duration
}

// UpstreamFrom reads the upstream the supervisor recorded for Caddy, so a core
// replaced by an update is the one asked. A missing or empty file is core:8080.
func UpstreamFrom(path string) func() string {
	return func() string {
		if path != "" {
			if raw, err := os.ReadFile(path); err == nil {
				if up := strings.TrimSpace(string(raw)); up != "" {
					return up
				}
			}
		}
		return "core:8080"
	}
}

func (c CoreFiles) url() string {
	up := "core:8080"
	if c.Upstream != nil {
		up = c.Upstream()
	}
	if !strings.Contains(up, "://") {
		up = "http://" + up
	}
	return strings.TrimRight(up, "/") + CoreFilesPath
}

// ExportStatusTrailer is the trailer the core sets to "complete" after the
// last byte of a whole export. A stream that ends without it was cut short.
const ExportStatusTrailer = "X-Togavsu-Export"

func (c CoreFiles) client() *http.Client {
	hc := http.Client{}
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	// A redirect (the core's first-run screen) is never file bytes.
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &hc
}

// Export streams the core's file export into w.
func (c CoreFiles) Export(ctx context.Context, w io.Writer) error {
	if c.Token == "" {
		return errors.New("core files: no supervisor token")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	res, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("core files: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return coreStatus(res)
	}
	if ct := res.Header.Get("Content-Type"); ct != "application/x-tar" {
		return fmt.Errorf("core files: the core answered %q, not a tar export", ct)
	}
	if _, err := io.Copy(w, res.Body); err != nil {
		return fmt.Errorf("core files: %w", err)
	}
	if got := res.Trailer.Get(ExportStatusTrailer); got != "complete" {
		return fmt.Errorf("core files: the export ended early: %s", Scrub(got))
	}
	return nil
}

// Import sends the export at path back to the core. A core that is not
// answering yet (it was just restarted) is retried until Wait; a core that
// answers with an error fails at once.
func (c CoreFiles) Import(ctx context.Context, path string) error {
	if c.Token == "" {
		return errors.New("core files: no supervisor token")
	}
	wait := c.Wait
	if wait <= 0 {
		wait = 3 * time.Minute
	}
	deadline := time.Now().Add(wait)
	for {
		err, retry := c.importOnce(ctx, path)
		if err == nil || !retry || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (c CoreFiles) importOnce(ctx context.Context, path string) (error, bool) {
	f, err := os.Open(path)
	if err != nil {
		return err, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.url(), f)
	if err != nil {
		return err, false
	}
	req.ContentLength = info.Size()
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/x-tar")
	res, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("core files: %w", err), ctx.Err() == nil
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusOK:
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
		return nil, false
	case res.StatusCode == http.StatusBadGateway, res.StatusCode == http.StatusServiceUnavailable, res.StatusCode == http.StatusGatewayTimeout:
		return coreStatus(res), true
	default:
		return coreStatus(res), false
	}
}

func coreStatus(res *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
	return fmt.Errorf("core files: %s: %s", res.Status, strings.TrimSpace(Scrub(string(body))))
}

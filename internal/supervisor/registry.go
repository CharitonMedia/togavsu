package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// IndexRegistry reads a JSON release index. It is the stand-in for the OCI
// registry until signed images are published.
//
// ponytail: one JSON document instead of the OCI tag listing, ceiling: no
// registry protocol, upgrade: OCI tags plus cosign referrers, trigger: phase
// 7 publishes a signed core image.
type IndexRegistry struct {
	URL  string
	HTTP *http.Client
}

func (r IndexRegistry) Latest(ctx context.Context) (Release, error) {
	if r.URL == "" {
		return Release{}, ErrReleaseIndexUnconfigured
	}
	ctx, cancel := boundDocker(ctx, dockerWaitLimit)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
	if err != nil {
		return Release{}, err
	}
	client := r.HTTP
	if client == nil {
		client = &http.Client{
			Timeout:   dockerWaitLimit,
			Transport: &http.Transport{ResponseHeaderTimeout: dockerWaitLimit},
		}
	}
	res, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("release index %s", res.Status)
	}
	var doc struct {
		Image   string `json:"image"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return Release{}, err
	}
	if doc.Image == "" || doc.Version == "" {
		return Release{}, fmt.Errorf("release index: %w", ErrBadRequest)
	}
	return Release{Image: doc.Image, Version: doc.Version}, nil
}

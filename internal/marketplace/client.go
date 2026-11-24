package marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client errors. A caller maps them to API errors; none of them means a module is unsafe.
var (
	// ErrNotConfigured: modules.marketplace_address is not an https URL (or http on a
	// loopback host or the compose service name togavsu-marketplace).
	ErrNotConfigured = errors.New("marketplace address is not configured")
	// ErrUnavailable: the marketplace could not be reached or answered with nonsense.
	ErrUnavailable = errors.New("unavailable")
	// ErrNotFound: no such listing or version.
	ErrNotFound = errors.New("not_found")
	// ErrYanked: the listing was pulled or the version yanked. It cannot be installed
	// from the marketplace; a local file install is unaffected.
	ErrYanked = errors.New("yanked")
)

// ComposeMarketplaceHost is the Docker Compose service name for a marketplace that runs
// beside Togavsu. Plain http to this host is allowed for listings and packages; it does
// not confer trust of marketplace-published keys (see TrustsMarketplaceKeys).
const ComposeMarketplaceHost = "togavsu-marketplace"

// Client reads the marketplace's read API. Address is called on every request so a
// change to modules.marketplace_address applies on the next one, with no restart.
type Client struct {
	Address func(ctx context.Context) string
	HTTP    *http.Client
}

// BaseURL validates addr. The marketplace is reached over https; plain http is allowed
// on a loopback host (a firm testing a local marketplace) and on the compose service
// name togavsu-marketplace. Every other host needs https.
func BaseURL(addr string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(addr))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrNotConfigured
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !httpHostAllowed(u.Hostname()) {
			return nil, ErrNotConfigured
		}
	default:
		return nil, ErrNotConfigured
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u, nil
}

// httpHostAllowed reports whether plain http may reach host.
//
// ponytail: ceiling: plain http only for loopback and togavsu-marketplace; a name on
// the firm's Docker network can still be answered by something else, so that name
// confers reach but not key trust (see TrustsMarketplaceKeys). Upgrade path: require
// https for every non-loopback marketplace. trigger: any marketplace reached across a
// network the firm does not control.
func httpHostAllowed(host string) bool {
	if host == "localhost" || host == ComposeMarketplaceHost {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// TrustsMarketplaceKeys reports whether marketplace-published signing keys may be
// fetched and used to verify installs for addr. Only plain http to togavsu-marketplace
// loses that trust (only trusted local keys verify there). Every other address keeps
// today's behavior: https and loopback fetch and trust keys, and an empty, placeholder
// or refused address keeps and trusts the last cached keys.
//
// ponytail: ceiling: over http://togavsu-marketplace listings, packages and alerts are
// unauthenticated and marketplace-published keys are never fetched or trusted (only
// trusted local keys verify an install). Upgrade path: require https (and key trust)
// for every non-loopback marketplace. trigger: any marketplace reached across a
// network the firm does not control.
func TrustsMarketplaceKeys(addr string) bool {
	return !IsComposeMarketplaceHTTP(addr)
}

// IsComposeMarketplaceHTTP reports whether addr is plain http to togavsu-marketplace.
func IsComposeMarketplaceHTTP(addr string) bool {
	u, err := BaseURL(addr)
	return err == nil && u.Scheme == "http" && u.Hostname() == ComposeMarketplaceHost
}

func (c *Client) do(ctx context.Context, path string, q url.Values, dst any) error {
	return c.read(ctx, path, q, fixed.MaxResponseBytes, dst)
}

func (c *Client) read(ctx context.Context, path string, q url.Values, limit int64, dst any) error {
	if c == nil || c.Address == nil {
		return ErrNotConfigured
	}
	base, err := BaseURL(c.Address(ctx))
	if err != nil {
		return err
	}
	u := *base
	u.Path += path
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithTimeout(ctx, clientTimeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ErrNotConfigured
	}
	req.Header.Set("Accept", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{}
	}
	// A redirect could move the read to another host; the marketplace does not need one.
	client := *hc
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return fmt.Errorf("%w: response is too large or broken", ErrUnavailable)
	}
	switch {
	case res.StatusCode == http.StatusOK:
	case res.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case res.StatusCode == http.StatusGone:
		return ErrYanked
	default:
		return fmt.Errorf("%w: marketplace answered %d", ErrUnavailable, res.StatusCode)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("%w: unreadable response", ErrUnavailable)
	}
	return nil
}

// Listings searches the marketplace. Pulled listings never come back.
func (c *Client) Listings(ctx context.Context, query string) ([]Listing, error) {
	var out struct {
		Data []Listing `json:"data"`
	}
	q := url.Values{}
	if query != "" {
		q.Set("q", query)
	}
	if err := c.do(ctx, "/v1/listings", q, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Listing reads one listing, pulled or not, so a firm can still see why it was pulled.
func (c *Client) Listing(ctx context.Context, id string) (Listing, error) {
	if ReservedModuleID(id) {
		return Listing{}, ErrReservedModuleID
	}
	if !ValidModuleID(id) {
		return Listing{}, ErrNotFound
	}
	var out Listing
	if err := c.do(ctx, "/v1/listings/"+url.PathEscape(id), nil, &out); err != nil {
		return Listing{}, err
	}
	if out.ID != id {
		return Listing{}, fmt.Errorf("%w: listing id does not match", ErrUnavailable)
	}
	return out, nil
}

// Package reads one signed version. The caller must verify it before use.
func (c *Client) Package(ctx context.Context, id, version string) (SignedPackage, error) {
	if ReservedModuleID(id) {
		return SignedPackage{}, ErrReservedModuleID
	}
	if !ValidModuleID(id) || !ValidVersion(version) {
		return SignedPackage{}, ErrNotFound
	}
	var out SignedPackage
	// The package travels as base64 inside JSON: four bytes per three, plus the manifest.
	limit := fixed.MaxPackageBytes/3*4 + fixed.MaxResponseBytes
	if err := c.read(ctx, "/v1/listings/"+url.PathEscape(id)+"/versions/"+url.PathEscape(version)+"/package", nil, limit, &out); err != nil {
		return SignedPackage{}, err
	}
	if out.ListingID != id || out.Version != version {
		// The marketplace never substitutes another listing or a later version.
		return SignedPackage{}, fmt.Errorf("%w: package is not the version asked for", ErrUnavailable)
	}
	return out, nil
}

// Alerts lists flags and pulls on the given modules.
func (c *Client) Alerts(ctx context.Context, moduleIDs []string, since time.Time) ([]Alert, error) {
	var all []Alert
	for start := 0; start < len(moduleIDs); start += fixed.MaxQueryModuleIDs {
		end := start + fixed.MaxQueryModuleIDs
		if end > len(moduleIDs) {
			end = len(moduleIDs)
		}
		q := url.Values{}
		for _, id := range moduleIDs[start:end] {
			q.Add("module_id", id)
		}
		if !since.IsZero() {
			q.Set("since", since.UTC().Format(time.RFC3339Nano))
		}
		var out struct {
			Data []Alert `json:"data"`
		}
		if err := c.do(ctx, "/v1/alerts", q, &out); err != nil {
			return nil, err
		}
		all = append(all, out.Data...)
	}
	return all, nil
}

// SigningKeys lists the verified author keys the marketplace publishes.
func (c *Client) SigningKeys(ctx context.Context) ([]SigningKey, error) {
	var out struct {
		Data []SigningKey `json:"data"`
	}
	if err := c.do(ctx, "/v1/signing-keys", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

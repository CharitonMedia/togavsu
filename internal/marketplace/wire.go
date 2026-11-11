package marketplace

import "time"

// The marketplace read API's JSON shapes (api/openapi/marketplace/openapi.json), shared by
// the service in /marketplace and the core's client so the two cannot drift. Fields the
// contract does not list yet are marked; the PR proposes them for the contract.

// Notice is the text every listing shows (PRD 10.2).
const Notice = "Modules are published by their authors. The Togavsu marketplace does not review, test or warrant any module. Install at your own risk."

// FlagWarning opens the warning a flagged listing, an install and a firm alert show
// (PRD 10.3).
const FlagWarning = "We've been alerted that this item has problems."

// PullWarning opens the firm alert for a pulled module (PRD 10.4, 10.5).
const PullWarning = "This module was pulled from the Togavsu marketplace. It can no longer be installed from the marketplace; a firm that has it keeps it."

// Alert kinds.
const (
	KindFlag = "flag"
	KindPull = "pull"
)

// Warning is the text shown for an alert of kind with the operator's note.
func Warning(kind, note string) string {
	head := FlagWarning
	if kind == KindPull {
		head = PullWarning
	}
	if note == "" {
		return head
	}
	return head + " " + note
}

// VersionEntry is one release in a listing's version history.
type VersionEntry struct {
	Version    string    `json:"version"`
	ReleasedAt time.Time `json:"released_at"`
	// Flagged and FlagNote mark a flag on this one version (contract extension).
	Flagged  bool   `json:"flagged,omitempty"`
	FlagNote string `json:"flag_note,omitempty"`
	// Permissions are the scopes this version requests (contract extension).
	Permissions []string `json:"permissions,omitempty"`
}

// Listing is one module on the marketplace (PRD 10.2).
type Listing struct {
	ID       string `json:"id"`
	ModuleID string `json:"module_id"`
	Name     string `json:"name"`
	Author   string `json:"author"`
	// SigningKey is the key id of the author's verified key that signed the current
	// version.
	SigningKey string `json:"signing_key"`
	// SigningKeyPEM is that key's public half (contract extension), so a firm can compare
	// it with what the author publishes elsewhere.
	SigningKeyPEM  string         `json:"signing_key_pem,omitempty"`
	Version        string         `json:"version"`
	Permissions    []string       `json:"permissions"`
	VersionHistory []VersionEntry `json:"version_history"`
	PriceNote      string         `json:"price_note"`
	// Flagged is true when the listing is flagged or its current version is.
	Flagged  bool   `json:"flagged"`
	FlagNote string `json:"flag_note,omitempty"`
	// ListingFlagged is true when the flag is on the whole listing, not only on its
	// current version (contract extension); FlagNote is then the listing's note.
	ListingFlagged bool   `json:"listing_flagged,omitempty"`
	Pulled         bool   `json:"pulled"`
	Notice         string `json:"notice"`
}

// FlagText is the warning a flagged listing shows, or "" when it is not flagged.
func (l Listing) FlagText() string {
	if !l.Flagged {
		return ""
	}
	return Warning(KindFlag, l.FlagNote)
}

// VersionFlag is the flag note on version v, if any: the listing's own flag applies to
// every version.
func (l Listing) VersionFlag(v string) (bool, string) {
	for _, e := range l.VersionHistory {
		if e.Version == v && e.Flagged {
			return true, e.FlagNote
		}
	}
	return false, ""
}

// SignedPackage is one immutable version as the marketplace serves it for install.
type SignedPackage struct {
	ListingID string         `json:"listing_id"`
	Version   string         `json:"version"`
	Immutable bool           `json:"immutable"`
	Manifest  map[string]any `json:"manifest"`
	Digest    string         `json:"digest"`
	Signature string         `json:"signature"`
	KeyID     string         `json:"key_id"`
	// PackageBase64 is the signed package itself (contract extension): the contract
	// carries the manifest but no bytes, and the core installs nothing it has not
	// verified byte for byte.
	PackageBase64 string `json:"package_base64"`
}

// Alert is one flag or pull the marketplace reports for a module (PRD 10.5).
type Alert struct {
	// ID and CreatedAt (contract extensions) let the core tell a new alert from one it
	// already delivered.
	ID        string    `json:"id"`
	ModuleID  string    `json:"module_id"`
	Version   string    `json:"version,omitempty"`
	Kind      string    `json:"kind"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// SigningKey is one verified author key the marketplace publishes.
type SigningKey struct {
	KeyID        string `json:"key_id"`
	Author       string `json:"author"`
	PublicKeyPEM string `json:"public_key_pem"`
}

// Problem is the error body.
type Problem struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

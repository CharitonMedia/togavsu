// Package marketplace is the Togavsu marketplace service (PRD 10.6): a small Go service
// deployed separately from the core. Authors register accounts and signing keys and
// upload signed modules; the service shows listing pages and search; the operator flags
// and pulls; and the core reads listings, packages, keys and alerts through a read API.
//
// The operator does not review, inspect, test or vouch for modules (PRD 10.1). Upload
// checks only who signed a package and that its manifest can be listed.
package marketplace

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/mail"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	shared "github.com/CharitonMedia/togavsu/internal/marketplace"
)

const privateDir = 0o777 &^ 0o077

// Errors the HTTP layer maps to statuses.
var (
	ErrNotFound     = errors.New("not_found")
	ErrPulled       = errors.New("yanked")
	ErrUnauthorized = errors.New("unauthenticated")
	ErrForbidden    = errors.New("forbidden")
	ErrConflict     = errors.New("conflict")
)

// InvalidError is a request the service refuses, with the field at fault.
type InvalidError struct{ Field, Message string }

func (e *InvalidError) Error() string { return e.Field + ": " + e.Message }

func invalid(field, msg string) error { return &InvalidError{Field: field, Message: msg} }

// Market holds the state and applies every rule. Its methods are safe for concurrent use.
type Market struct {
	mu    sync.Mutex
	st    State
	store fileStore
	now   func() time.Time
	// tokenLife is how long an author session lasts.
	tokenLife time.Duration
}

// Open loads the state in dir.
func Open(dir string, tokenLife time.Duration, now func() time.Time) (*Market, error) {
	if now == nil {
		now = time.Now
	}
	fsStore := fileStore{dir: dir}
	st, err := fsStore.load()
	if err != nil {
		return nil, err
	}
	return &Market{st: st, store: fsStore, now: func() time.Time { return now().UTC() }, tokenLife: tokenLife}, nil
}

// commit saves the state after fn changes it; a failed save restores the old state.
func (m *Market) commit(fn func(st *State) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, err := json.Marshal(m.st)
	if err != nil {
		return err
	}
	if err := fn(&m.st); err != nil {
		m.st = State{}
		_ = json.Unmarshal(raw, &m.st)
		return err
	}
	if err := m.store.save(m.st); err != nil {
		m.st = State{}
		_ = json.Unmarshal(raw, &m.st)
		return err
	}
	return nil
}

func (m *Market) read(fn func(st *State)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(&m.st)
}

func randomText() (string, error) {
	b := make([]byte, sha256.Size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// --- Author accounts ---

// AuthorView is an author as the API shows them.
type AuthorView struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// RegisterAuthor creates an account. The email is the login and is unique.
func (m *Market) RegisterAuthor(name, email, password string) (AuthorView, error) {
	name = strings.TrimSpace(name)
	email = strings.ToLower(strings.TrimSpace(email))
	if name == "" || utf8.RuneCountInString(name) > shared.MaxNoteRunes() {
		return AuthorView{}, invalid("name", "an author needs a name")
	}
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		return AuthorView{}, invalid("email", "a valid email address is required")
	}
	hash, err := identity.HashPassword(password)
	if err != nil {
		return AuthorView{}, invalid("password", "the password is too short")
	}
	id, err := domain.NewID()
	if err != nil {
		return AuthorView{}, err
	}
	a := Author{ID: string(id), Name: name, Email: email, PasswordHash: hash, CreatedAt: m.now()}
	err = m.commit(func(st *State) error {
		for _, x := range st.Authors {
			if x.Email == email {
				return ErrConflict
			}
			if strings.EqualFold(x.Name, name) {
				return ErrConflict
			}
		}
		st.Authors = append(st.Authors, a)
		return nil
	})
	return AuthorView{ID: a.ID, Name: a.Name, Email: a.Email}, err
}

// Login checks an author's password and returns a bearer token.
//
// ponytail: ceiling: no per-account lockout or rate limit; Argon2id cost is the only
// brake on guessing. Upgrade path: the core identity package's lockout counters.
// trigger: the marketplace is reachable from the internet.
func (m *Market) Login(email, password string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var hash, authorID string
	m.read(func(st *State) {
		for _, a := range st.Authors {
			if a.Email == email {
				hash, authorID = a.PasswordHash, a.ID
			}
		}
	})
	if hash == "" || !identity.VerifyPassword(password, hash) {
		return "", ErrUnauthorized
	}
	tok, err := randomText()
	if err != nil {
		return "", err
	}
	now := m.now()
	err = m.commit(func(st *State) error {
		kept := st.Tokens[:0]
		for _, t := range st.Tokens {
			if t.ExpiresAt.After(now) {
				kept = append(kept, t)
			}
		}
		st.Tokens = append(kept, Token{Hash: hashToken(tok), AuthorID: authorID, ExpiresAt: now.Add(m.tokenLife)})
		return nil
	})
	return tok, err
}

// Author resolves a bearer token.
func (m *Market) Author(token string) (Author, error) {
	if token == "" {
		return Author{}, ErrUnauthorized
	}
	h := hashToken(token)
	var out Author
	found := false
	m.read(func(st *State) {
		now := m.now()
		for _, t := range st.Tokens {
			if hmac.Equal([]byte(t.Hash), []byte(h)) && t.ExpiresAt.After(now) {
				for _, a := range st.Authors {
					if a.ID == t.AuthorID {
						out, found = a, true
					}
				}
			}
		}
	})
	if !found {
		return Author{}, ErrUnauthorized
	}
	return out, nil
}

// --- Signing keys (verified registration) ---

// KeyView is a key as its author sees it.
type KeyView struct {
	KeyID        string `json:"key_id"`
	PublicKeyPEM string `json:"public_key_pem"`
	Verified     bool   `json:"verified"`
	// Challenge is what the author signs to verify the key; empty once verified.
	Challenge string `json:"challenge,omitempty"`
	// ProofMessage is the exact text to sign (shared.KeyProofMessage).
	ProofMessage string `json:"proof_message,omitempty"`
}

func keyView(k Key) KeyView {
	v := KeyView{KeyID: k.KeyID, PublicKeyPEM: k.PublicKeyPEM, Verified: k.Verified}
	if !k.Verified {
		v.Challenge = k.Challenge
		v.ProofMessage = string(shared.KeyProofMessage(k.KeyID, k.Challenge))
	}
	return v
}

// AddKey registers a public key for the author, unverified, with a fresh challenge. A key
// another author registered is refused: one key names one publisher.
func (m *Market) AddKey(a Author, publicKeyPEM string) (KeyView, error) {
	pub, err := shared.ParsePublicKeyPEM(publicKeyPEM)
	if err != nil {
		return KeyView{}, invalid("public_key_pem", "the key must be one Ed25519 PUBLIC KEY block")
	}
	challenge, err := randomText()
	if err != nil {
		return KeyView{}, err
	}
	k := Key{KeyID: shared.KeyID(pub), AuthorID: a.ID, PublicKeyPEM: shared.PublicKeyPEM(pub), Challenge: challenge, CreatedAt: m.now()}
	err = m.commit(func(st *State) error {
		for _, x := range st.Keys {
			if x.KeyID == k.KeyID {
				if x.AuthorID != a.ID {
					return ErrConflict
				}
				k = x
				return nil
			}
		}
		st.Keys = append(st.Keys, k)
		return nil
	})
	return keyView(k), err
}

// Keys lists the author's keys.
func (m *Market) Keys(a Author) []KeyView {
	out := []KeyView{}
	m.read(func(st *State) {
		for _, k := range st.Keys {
			if k.AuthorID == a.ID {
				out = append(out, keyView(k))
			}
		}
	})
	return out
}

// VerifyKey marks a key verified when signature is the key's signature of its proof
// message. A wrong signature leaves it unverified.
func (m *Market) VerifyKey(a Author, keyID, signature string) (KeyView, error) {
	var out Key
	err := m.commit(func(st *State) error {
		for i := range st.Keys {
			k := &st.Keys[i]
			if k.KeyID != keyID || k.AuthorID != a.ID {
				continue
			}
			if k.Verified {
				out = *k
				return nil
			}
			pub, err := shared.ParsePublicKeyPEM(k.PublicKeyPEM)
			if err != nil {
				return invalid("key_id", "the stored key is unreadable")
			}
			if shared.VerifySignature(pub, shared.KeyProofMessage(k.KeyID, k.Challenge), signature) != nil {
				return invalid("signature", "the signature does not prove this key; sign proof_message with its private key")
			}
			k.Verified, k.VerifiedAt, k.Challenge = true, m.now(), ""
			out = *k
			return nil
		}
		return ErrNotFound
	})
	return keyView(out), err
}

// SigningKeys is every verified key with its author's name (the core's trusted
// marketplace signers, PRD 9.3).
func (m *Market) SigningKeys() []shared.SigningKey {
	out := []shared.SigningKey{}
	m.read(func(st *State) {
		names := map[string]string{}
		for _, a := range st.Authors {
			names[a.ID] = a.Name
		}
		for _, k := range st.Keys {
			if k.Verified {
				out = append(out, shared.SigningKey{KeyID: k.KeyID, Author: names[k.AuthorID], PublicKeyPEM: k.PublicKeyPEM})
			}
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].KeyID < out[j].KeyID })
	return out
}

// --- Upload ---

// Upload is one signed package from an author.
type Upload struct {
	Package   []byte
	Signature string
	KeyID     string
	// PriceNote, when set, replaces the listing's price ("" is free).
	PriceNote *string
}

// Publish checks an upload's signer and signature, reads its manifest and adds it as a new
// immutable version of the listing named by the manifest's module id. Nothing in the
// package is run or reviewed.
func (m *Market) Publish(a Author, up Upload) (shared.Listing, error) {
	if len(up.Package) == 0 || int64(len(up.Package)) > shared.MaxPackageBytes() {
		return shared.Listing{}, invalid("package", "the package is empty or too large")
	}
	if up.PriceNote != nil && utf8.RuneCountInString(*up.PriceNote) > shared.MaxNoteRunes() {
		return shared.Listing{}, invalid("price_note", "the price note is too long")
	}
	var key Key
	m.read(func(st *State) {
		for _, k := range st.Keys {
			if k.KeyID == up.KeyID && k.AuthorID == a.ID && k.Verified {
				key = k
			}
		}
	})
	if key.KeyID == "" {
		return shared.Listing{}, invalid("key_id", "sign with one of your verified signing keys")
	}
	pub, err := shared.ParsePublicKeyPEM(key.PublicKeyPEM)
	if err != nil {
		return shared.Listing{}, invalid("key_id", "the stored key is unreadable")
	}
	if _, err := shared.Verify(up.Package, "", up.Signature, key.KeyID, []shared.TrustedKey{{KeyID: key.KeyID, Public: pub}}); err != nil {
		return shared.Listing{}, invalid("signature", "the signature does not match the package and key")
	}
	man, err := shared.ReadManifest(up.Package)
	if err != nil {
		return shared.Listing{}, invalid("package", "the package needs a "+shared.ManifestName+" with id, name, version and permissions")
	}
	var manifest map[string]any
	if err := json.Unmarshal(man.Raw, &manifest); err != nil {
		return shared.Listing{}, invalid("package", "the manifest is unreadable")
	}
	digest := shared.Digest(up.Package)
	if err := m.store.putPackage(digest, up.Package); err != nil {
		return shared.Listing{}, err
	}
	now := m.now()
	v := Version{Version: man.Version, Name: man.Name, Permissions: man.Permissions, Manifest: manifest, Digest: digest,
		Signature: strings.TrimSpace(up.Signature), KeyID: key.KeyID, Size: int64(len(up.Package)), ReleasedAt: now}
	var out ListingRecord
	err = m.commit(func(st *State) error {
		for i := range st.Listings {
			l := &st.Listings[i]
			if l.ID != man.ID {
				continue
			}
			if l.AuthorID != a.ID {
				return ErrForbidden
			}
			for _, old := range l.Versions {
				if old.Version == v.Version {
					// A version never changes once listed.
					return ErrConflict
				}
			}
			l.Versions = append(l.Versions, v)
			if up.PriceNote != nil {
				l.PriceNote = strings.TrimSpace(*up.PriceNote)
			}
			out = *l
			return nil
		}
		l := ListingRecord{ID: man.ID, AuthorID: a.ID, Versions: []Version{v}, CreatedAt: now}
		if up.PriceNote != nil {
			l.PriceNote = strings.TrimSpace(*up.PriceNote)
		}
		st.Listings = append(st.Listings, l)
		out = l
		return nil
	})
	if err != nil {
		return shared.Listing{}, err
	}
	return m.view(out), nil
}

// --- Reads ---

func (m *Market) view(l ListingRecord) shared.Listing {
	var out shared.Listing
	m.read(func(st *State) { out = viewOf(st, l) })
	return out
}

// viewOf renders a listing; the caller holds the lock.
func viewOf(st *State, l ListingRecord) shared.Listing {
	cur := l.Versions[len(l.Versions)-1]
	out := shared.Listing{ID: l.ID, ModuleID: l.ID, Name: cur.Name, Version: cur.Version, SigningKey: cur.KeyID,
		Permissions: append([]string{}, cur.Permissions...), PriceNote: l.PriceNote, Pulled: l.Pulled, Notice: shared.Notice}
	for _, a := range st.Authors {
		if a.ID == l.AuthorID {
			out.Author = a.Name
		}
	}
	for _, k := range st.Keys {
		if k.KeyID == cur.KeyID {
			out.SigningKeyPEM = k.PublicKeyPEM
		}
	}
	for _, v := range l.Versions {
		out.VersionHistory = append(out.VersionHistory, shared.VersionEntry{Version: v.Version, ReleasedAt: v.ReleasedAt,
			Flagged: v.Flagged, FlagNote: v.FlagNote, Permissions: append([]string{}, v.Permissions...)})
	}
	switch {
	case l.Flagged:
		out.Flagged, out.FlagNote = true, l.FlagNote
	case cur.Flagged:
		out.Flagged, out.FlagNote = true, cur.FlagNote
	}
	return out
}

// Search lists listings matching q (case-insensitive substring of id, name or author),
// newest first. Pulled listings never appear (PRD 10.4).
func (m *Market) Search(q string) []shared.Listing {
	q = strings.ToLower(strings.TrimSpace(q))
	out := []shared.Listing{}
	m.read(func(st *State) {
		for _, l := range st.Listings {
			if l.Pulled {
				continue
			}
			v := viewOf(st, l)
			hay := strings.ToLower(v.ID + "\n" + v.Name + "\n" + v.Author)
			if q == "" || strings.Contains(hay, q) {
				out = append(out, v)
			}
		}
	})
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].VersionHistory[len(out[i].VersionHistory)-1].ReleasedAt.After(out[j].VersionHistory[len(out[j].VersionHistory)-1].ReleasedAt)
	})
	return out
}

// Listing reads one listing, pulled ones included, so a firm can see why.
func (m *Market) Listing(id string) (shared.Listing, error) {
	var out shared.Listing
	found := false
	m.read(func(st *State) {
		for _, l := range st.Listings {
			if l.ID == id {
				out, found = viewOf(st, l), true
			}
		}
	})
	if !found {
		return shared.Listing{}, ErrNotFound
	}
	return out, nil
}

// Package is the signed package for one version. A pulled listing serves no package: it
// can't be installed from the marketplace (PRD 10.4).
func (m *Market) Package(id, version string) (shared.SignedPackage, error) {
	var v Version
	var pulled, found bool
	m.read(func(st *State) {
		for _, l := range st.Listings {
			if l.ID != id {
				continue
			}
			pulled = l.Pulled
			for _, x := range l.Versions {
				if x.Version == version {
					v, found = x, true
				}
			}
		}
	})
	if pulled {
		return shared.SignedPackage{}, ErrPulled
	}
	if !found {
		return shared.SignedPackage{}, ErrNotFound
	}
	body, err := m.store.getPackage(v.Digest)
	if err != nil {
		return shared.SignedPackage{}, err
	}
	if shared.Digest(body) != v.Digest {
		return shared.SignedPackage{}, errors.New("marketplace: stored package does not match its digest")
	}
	return shared.SignedPackage{ListingID: id, Version: v.Version, Immutable: true, Manifest: v.Manifest, Digest: v.Digest,
		Signature: v.Signature, KeyID: v.KeyID, PackageBase64: base64.StdEncoding.EncodeToString(body)}, nil
}

// Alerts lists flags and pulls on the given modules (all when none are named) raised at
// or after since, oldest first.
func (m *Market) Alerts(moduleIDs []string, since time.Time) []shared.Alert {
	want := map[string]bool{}
	for _, id := range moduleIDs {
		want[id] = true
	}
	out := []shared.Alert{}
	m.read(func(st *State) {
		for _, a := range st.Alerts {
			if (len(want) == 0 || want[a.ModuleID]) && !a.CreatedAt.Before(since) {
				out = append(out, shared.Alert{ID: a.ID, ModuleID: a.ModuleID, Version: a.Version, Kind: a.Kind, Note: a.Note, CreatedAt: a.CreatedAt})
			}
		}
	})
	return out
}

// --- Operator controls (PRD 10.3, 10.4) ---

func cleanNote(note string, required bool) (string, error) {
	note = strings.TrimSpace(note)
	if required && note == "" {
		return "", invalid("note", "a flag needs a short note")
	}
	if utf8.RuneCountInString(note) > shared.MaxNoteRunes() {
		return "", invalid("note", "the note is too long")
	}
	return note, nil
}

// Flag flags a listing, or one version of it, with a note. Installs stay possible; the
// listing shows the warning and every installed firm is alerted.
func (m *Market) Flag(id, version, note string) (shared.Listing, error) {
	note, err := cleanNote(note, true)
	if err != nil {
		return shared.Listing{}, err
	}
	alertID, err := domain.NewID()
	if err != nil {
		return shared.Listing{}, err
	}
	var out ListingRecord
	err = m.commit(func(st *State) error {
		for i := range st.Listings {
			l := &st.Listings[i]
			if l.ID != id {
				continue
			}
			if version == "" {
				l.Flagged, l.FlagNote = true, note
			} else {
				found := false
				for j := range l.Versions {
					if l.Versions[j].Version == version {
						l.Versions[j].Flagged, l.Versions[j].FlagNote, found = true, note, true
					}
				}
				if !found {
					return ErrNotFound
				}
			}
			st.Alerts = append(st.Alerts, AlertRecord{ID: string(alertID), ModuleID: id, Version: version, Kind: shared.KindFlag, Note: note, CreatedAt: m.now()})
			out = *l
			return nil
		}
		return ErrNotFound
	})
	if err != nil {
		return shared.Listing{}, err
	}
	return m.view(out), nil
}

// Pull takes a listing off the marketplace: out of search, no marketplace installs.
// Firms that have it keep it and are alerted. Pulling twice changes nothing.
func (m *Market) Pull(id, note string) (shared.Listing, error) {
	note, err := cleanNote(note, false)
	if err != nil {
		return shared.Listing{}, err
	}
	alertID, err := domain.NewID()
	if err != nil {
		return shared.Listing{}, err
	}
	var out ListingRecord
	err = m.commit(func(st *State) error {
		for i := range st.Listings {
			l := &st.Listings[i]
			if l.ID != id {
				continue
			}
			if !l.Pulled {
				l.Pulled, l.PullNote = true, note
				st.Alerts = append(st.Alerts, AlertRecord{ID: string(alertID), ModuleID: id, Kind: shared.KindPull, Note: note, CreatedAt: m.now()})
			}
			out = *l
			return nil
		}
		return ErrNotFound
	})
	if err != nil {
		return shared.Listing{}, err
	}
	return m.view(out), nil
}

// All lists every listing, pulled ones included, for the operator's page.
func (m *Market) All() []shared.Listing {
	out := []shared.Listing{}
	m.read(func(st *State) {
		for _, l := range st.Listings {
			out = append(out, viewOf(st, l))
		}
	})
	return out
}

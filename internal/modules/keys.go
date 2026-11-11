package modules

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TrustedKeyView is the API contract's TrustedKey: a firm-trusted local signing key
// (PRD 9.3), served at /api/v1/trusted-signing-keys.
type TrustedKeyView struct {
	ID           string `json:"id"`
	CreatedAt    string `json:"created_at"`
	CreatedBy    string `json:"created_by"`
	UpdatedAt    string `json:"updated_at"`
	Name         string `json:"name"`
	PublicKeyPEM string `json:"public_key_pem"`
}

// TrustedKeyPatch is the contract's TrustedKeyWritePatch: omitted fields stay.
type TrustedKeyPatch struct {
	Name         *string `json:"name"`
	PublicKeyPEM *string `json:"public_key_pem"`
}

// PublicKeyPEM encodes an ed25519 public key as a PKIX "PUBLIC KEY" PEM block, the
// form the trusted-signing-keys API takes and returns.
func PublicKeyPEM(pub ed25519.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return ""
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// parseKeyPEM reads one PKIX PEM ed25519 public key and nothing else.
func parseKeyPEM(s string) (ed25519.PublicKey, error) {
	bad := invalid("public_key_pem", "a trusted key is an ed25519 public key in PEM (-----BEGIN PUBLIC KEY-----)")
	block, rest := pem.Decode([]byte(strings.TrimSpace(s)))
	if block == nil || block.Type != "PUBLIC KEY" || strings.TrimSpace(string(rest)) != "" {
		return nil, bad
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, bad
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, bad
	}
	return pub, nil
}

type keyRow struct {
	ID, CreatedBy, KeyID, Name, Pub string
	CreatedAt, UpdatedAt            int64
}

func (k keyRow) view() TrustedKeyView {
	raw, _ := base64.StdEncoding.DecodeString(k.Pub)
	return TrustedKeyView{ID: k.ID, CreatedAt: domain.FormatUTC(time.UnixMicro(k.CreatedAt)), CreatedBy: k.CreatedBy,
		UpdatedAt: domain.FormatUTC(time.UnixMicro(k.UpdatedAt)), Name: k.Name, PublicKeyPEM: PublicKeyPEM(raw)}
}

const keyCols = `id, created_by, key_id, name, public_key, created_at, updated_at`

func scanKey(sc interface{ Scan(...any) error }) (keyRow, error) {
	var k keyRow
	err := sc.Scan(&k.ID, &k.CreatedBy, &k.KeyID, &k.Name, &k.Pub, &k.CreatedAt, &k.UpdatedAt)
	return k, err
}

// keyRowByID returns an active (not deleted) local key, or not_found.
func (s *Service) keyRowByID(ctx context.Context, id string) (keyRow, error) {
	k, err := scanKey(s.q(ctx).QueryRowContext(ctx, `SELECT `+keyCols+` FROM core.module_signing_keys WHERE id = $1 AND revoked_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return keyRow{}, domain.NotFound("trusted signing key")
	}
	return k, err
}

func cleanKeyName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalid("name", "name the key")
	}
	return name, nil
}

// CreateTrustedKey adds a firm-trusted local signing key (contract createTrustedKey).
// Adding a key that was deleted before restores that row; adding a key that is already
// trusted is a conflict.
func (s *Service) CreateTrustedKey(ctx context.Context, p domain.Principal, name, pubPEM string) (TrustedKeyView, error) {
	if err := requireAdmin(p); err != nil {
		return TrustedKeyView{}, err
	}
	name, err := cleanKeyName(name)
	if err != nil {
		return TrustedKeyView{}, err
	}
	pub, err := parseKeyPEM(pubPEM)
	if err != nil {
		return TrustedKeyView{}, err
	}
	kid := KeyID(pub)
	id, err := domain.NewID()
	if err != nil {
		return TrustedKeyView{}, err
	}
	at := s.now().UnixMicro()
	var out keyRow
	err = s.commit(ctx, func(ctx context.Context) error {
		var revoked sql.NullInt64
		var oldID string
		err := s.q(ctx).QueryRowContext(ctx, `SELECT id, revoked_at FROM core.module_signing_keys WHERE key_id = $1 FOR UPDATE`, kid).Scan(&oldID, &revoked)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = s.q(ctx).ExecContext(ctx, `INSERT INTO core.module_signing_keys (id, created_at, updated_at, created_by, key_id, name, public_key, revoked_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,NULL)`, string(id), at, at, string(p.UserID), kid, name, encodeKey(pub))
			oldID = string(id)
		case err != nil:
		case !revoked.Valid:
			return domain.Conflict("that public key is already trusted")
		default:
			_, err = s.q(ctx).ExecContext(ctx, `UPDATE core.module_signing_keys SET revoked_at = NULL, name = $1, updated_at = $2 WHERE id = $3`, name, at, oldID)
		}
		if err != nil {
			return err
		}
		if out, err = s.keyRowByID(ctx, oldID); err != nil {
			return err
		}
		return s.audit(ctx, p, "keys", "trusted_key:"+out.ID, "", kid, name)
	})
	return out.view(), err
}

// ListTrustedKeys returns the firm's active local keys, oldest first.
func (s *Service) ListTrustedKeys(ctx context.Context, p domain.Principal) ([]TrustedKeyView, error) {
	if err := requireAdmin(p); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).QueryContext(ctx, `SELECT `+keyCols+` FROM core.module_signing_keys WHERE revoked_at IS NULL ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrustedKeyView{}
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k.view())
	}
	return out, rows.Err()
}

// GetTrustedKey returns one active local key.
func (s *Service) GetTrustedKey(ctx context.Context, p domain.Principal, id string) (TrustedKeyView, error) {
	if err := requireAdmin(p); err != nil {
		return TrustedKeyView{}, err
	}
	k, err := s.keyRowByID(ctx, id)
	return k.view(), err
}

// UpdateTrustedKey renames a key or replaces its public key. Packages already installed
// stay installed; new installs and updates verify against the new key.
func (s *Service) UpdateTrustedKey(ctx context.Context, p domain.Principal, id string, in TrustedKeyPatch) (TrustedKeyView, error) {
	if err := requireAdmin(p); err != nil {
		return TrustedKeyView{}, err
	}
	var out keyRow
	err := s.commit(ctx, func(ctx context.Context) error {
		k, err := s.keyRowByID(ctx, id)
		if err != nil {
			return err
		}
		name, kid, pub := k.Name, k.KeyID, k.Pub
		if in.Name != nil {
			if name, err = cleanKeyName(*in.Name); err != nil {
				return err
			}
		}
		if in.PublicKeyPEM != nil {
			raw, err := parseKeyPEM(*in.PublicKeyPEM)
			if err != nil {
				return err
			}
			kid, pub = KeyID(raw), encodeKey(raw)
		}
		if kid != k.KeyID {
			var n int
			if err := s.q(ctx).QueryRowContext(ctx, `SELECT COUNT(*) FROM core.module_signing_keys WHERE key_id = $1`, kid).Scan(&n); err != nil {
				return err
			}
			if n > zero {
				return domain.Conflict("that public key is already recorded as another trusted key")
			}
		}
		if _, err := s.q(ctx).ExecContext(ctx, `UPDATE core.module_signing_keys SET name = $1, key_id = $2, public_key = $3, updated_at = $4 WHERE id = $5`,
			name, kid, pub, s.now().UnixMicro(), id); err != nil {
			return err
		}
		if err := s.audit(ctx, p, "keys", "trusted_key:"+id, k.KeyID+" "+k.Name, kid+" "+name, "updated"); err != nil {
			return err
		}
		out, err = s.keyRowByID(ctx, id)
		return err
	})
	return out.view(), err
}

// DeleteTrustedKey stops accepting a local key for new installs and updates. The row is
// kept (revoked) for the audit history.
func (s *Service) DeleteTrustedKey(ctx context.Context, p domain.Principal, id string) error {
	if err := requireAdmin(p); err != nil {
		return err
	}
	return s.commit(ctx, func(ctx context.Context) error {
		k, err := s.keyRowByID(ctx, id)
		if err != nil {
			return err
		}
		if _, err := s.q(ctx).ExecContext(ctx, `UPDATE core.module_signing_keys SET revoked_at = $1, updated_at = $1 WHERE id = $2`, s.now().UnixMicro(), id); err != nil {
			return err
		}
		return s.audit(ctx, p, "keys", "trusted_key:"+id, k.KeyID, "", "deleted")
	})
}

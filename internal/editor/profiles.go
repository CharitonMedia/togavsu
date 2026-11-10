package editor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// CourtProfile is a firm court profile (PRD 5 court_profiles, 8.20): LawCraft's profile
// JSON stored whole. One an agent or a module made stays unverified until a person
// approves it.
type CourtProfile struct {
	ID         domain.ID      `json:"id"`
	CreatedAt  string         `json:"created_at"`
	CreatedBy  domain.ID      `json:"created_by"`
	UpdatedAt  string         `json:"updated_at"`
	Name       string         `json:"name"`
	Verified   bool           `json:"verified"`
	VerifiedBy *domain.ID     `json:"verified_by"`
	VerifiedAt *string        `json:"verified_at"`
	Body       map[string]any `json:"body"`
}

// ProfileWrite is a create or a patch.
type ProfileWrite struct {
	Name *string        `json:"name"`
	Body map[string]any `json:"body"`
}

// maxProfileBytes is LawCraft's own cap on a profile file.
const maxProfileBytes = 1 << 20

// lawcraftID is the profile's own id inside its JSON.
func lawcraftID(body map[string]any) string {
	id, _ := body["id"].(string)
	return id
}

// validateProfile checks the body the way LawCraft does (law.profiles.validate at the
// pinned commit, which reports every problem at once), after Togavsu's own checks: a
// schema 1 object, an id LawCraft can use as a file name, and at most 1 MB.
func (s *Service) validateProfile(ctx context.Context, body map[string]any) ([]byte, error) {
	if body == nil {
		return nil, domain.Invalid("body", "body is the LawCraft court profile JSON")
	}
	if !safeProfileID(lawcraftID(body)) {
		return nil, domain.Invalid("body.id", "the profile id must be letters, digits, hyphens or underscores")
	}
	// LawCraft's verified flag is not the caller's to set: approval is a person's action.
	body["verified"] = false
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, domain.Invalid("body", "body is not JSON")
	}
	if len(raw) > maxProfileBytes {
		return nil, domain.Invalid("body", "a court profile is at most 1 MB")
	}
	if _, err := s.cli.Run(ctx, RunInput{Commands: []Command{{ID: "law.profiles.validate", Parameters: body}}, Out: "txt"}); err != nil {
		var api *domain.APIError
		if errors.As(err, &api) {
			return nil, domain.Invalid("body", strings.TrimPrefix(api.Message, "the editor could not finish: "))
		}
		return nil, err
	}
	return raw, nil
}

func canWriteProfiles(p domain.Principal) error { return requireEditor(p) }

func (s *Service) scanProfile(row interface{ Scan(...any) error }) (CourtProfile, error) {
	var c CourtProfile
	var created, updated, verifiedAt int64
	var verified int64
	var verifiedBy sql.NullString
	var body string
	if err := row.Scan(&c.ID, &created, &c.CreatedBy, &updated, &c.Name, &body, &verified, &verifiedBy, &verifiedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c, domain.NotFound("court profile")
		}
		return c, err
	}
	c.CreatedAt, c.UpdatedAt = stamp(fromUS(created)), stamp(fromUS(updated))
	c.Verified = verified != zero
	if verifiedBy.Valid && verifiedBy.String != "" {
		id := domain.ID(verifiedBy.String)
		c.VerifiedBy = &id
	}
	if c.Verified && verifiedAt != zero {
		t := stamp(fromUS(verifiedAt))
		c.VerifiedAt = &t
	}
	if err := json.Unmarshal([]byte(body), &c.Body); err != nil {
		return c, err
	}
	c.Body["verified"] = c.Verified
	return c, nil
}

const profileCols = `id, created_at, created_by, updated_at, name, profile_json, verified, verified_by, verified_at`

// Profiles lists the firm's court profiles.
func (s *Service) Profiles(ctx context.Context, p domain.Principal) ([]CourtProfile, error) {
	if err := requireEditor(p); err != nil {
		return nil, err
	}
	rows, err := audit.Conn(ctx, s.pool).QueryContext(ctx, `SELECT `+profileCols+` FROM court_profiles ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CourtProfile{}
	for rows.Next() {
		c, err := s.scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Profile reads one court profile.
func (s *Service) Profile(ctx context.Context, p domain.Principal, id domain.ID) (CourtProfile, error) {
	if err := requireEditor(p); err != nil {
		return CourtProfile{}, err
	}
	return s.scanProfile(audit.Conn(ctx, s.pool).QueryRowContext(ctx, `SELECT `+profileCols+` FROM court_profiles WHERE id = $1`, id))
}

func (s *Service) idTaken(ctx context.Context, lawID string, self domain.ID) error {
	var n int64
	err := audit.Conn(ctx, s.pool).QueryRowContext(ctx,
		`SELECT count(*) FROM court_profiles WHERE profile_json::jsonb ->> 'id' = $1 AND id <> $2`, lawID, self).Scan(&n)
	if err != nil {
		return err
	}
	if n > zero {
		return domain.Conflict("another court profile already has the id " + lawID)
	}
	return nil
}

// CreateProfile stores a new court profile, unverified whoever made it.
func (s *Service) CreateProfile(ctx context.Context, p domain.Principal, in ProfileWrite) (CourtProfile, error) {
	if err := canWriteProfiles(p); err != nil {
		return CourtProfile{}, err
	}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		return CourtProfile{}, domain.Invalid("name", "name is required")
	}
	raw, err := s.validateProfile(ctx, in.Body)
	if err != nil {
		return CourtProfile{}, err
	}
	id, err := newID()
	if err != nil {
		return CourtProfile{}, err
	}
	err = audit.WithTx(ctx, s.pool, func(ctx context.Context) error {
		if err := s.idTaken(ctx, lawcraftID(in.Body), id); err != nil {
			return err
		}
		now := us(s.now())
		if _, err := audit.Conn(ctx, s.pool).ExecContext(ctx, `INSERT INTO court_profiles (id, created_at, updated_at, created_by, name, profile_json, verified, verified_by, verified_at)
VALUES ($1, $2, $2, $3, $4, $5, 0, NULL, 0)`, id, now, p.UserID, strings.TrimSpace(*in.Name), string(raw)); err != nil {
			return err
		}
		return s.record(ctx, p, "court_profile", id, "body", "", lawcraftID(in.Body)+" (unverified)")
	})
	if err != nil {
		return CourtProfile{}, err
	}
	return s.Profile(ctx, p, id)
}

// UpdateProfile changes a profile's name or body. A changed body is unverified again:
// the approval covered the rules as they were.
func (s *Service) UpdateProfile(ctx context.Context, p domain.Principal, id domain.ID, in ProfileWrite) (CourtProfile, error) {
	if err := canWriteProfiles(p); err != nil {
		return CourtProfile{}, err
	}
	var raw []byte
	if in.Body != nil {
		var err error
		if raw, err = s.validateProfile(ctx, in.Body); err != nil {
			return CourtProfile{}, err
		}
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return CourtProfile{}, domain.Invalid("name", "name cannot be empty")
	}
	err := audit.WithTx(ctx, s.pool, func(ctx context.Context) error {
		db := audit.Conn(ctx, s.pool)
		cur, err := s.scanProfile(db.QueryRowContext(ctx, `SELECT `+profileCols+` FROM court_profiles WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		now := us(s.now())
		if in.Name != nil && strings.TrimSpace(*in.Name) != cur.Name {
			name := strings.TrimSpace(*in.Name)
			if _, err := db.ExecContext(ctx, `UPDATE court_profiles SET name = $1, updated_at = $2 WHERE id = $3`, name, now, id); err != nil {
				return err
			}
			if err := s.record(ctx, p, "court_profile", id, "name", cur.Name, name); err != nil {
				return err
			}
		}
		if raw != nil {
			if err := s.idTaken(ctx, lawcraftID(in.Body), id); err != nil {
				return err
			}
			if _, err := db.ExecContext(ctx, `UPDATE court_profiles SET profile_json = $1, verified = 0, verified_by = NULL, verified_at = 0, updated_at = $2 WHERE id = $3`,
				string(raw), now, id); err != nil {
				return err
			}
			if err := s.record(ctx, p, "court_profile", id, "body", lawcraftID(cur.Body), lawcraftID(in.Body)+" (unverified)"); err != nil {
				return err
			}
			if cur.Verified {
				if err := s.record(ctx, p, "court_profile", id, "verified", "true", "false"); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return CourtProfile{}, err
	}
	return s.Profile(ctx, p, id)
}

// VerifyProfile is a person approving a profile's rules. A token, a module or the system
// cannot: an agent-made profile stays unverified until a person approves it.
func (s *Service) VerifyProfile(ctx context.Context, p domain.Principal, id domain.ID) (CourtProfile, error) {
	if p.Source != domain.SourceUI {
		return CourtProfile{}, domain.Forbidden("only a person signed in to Togavsu approves a court profile")
	}
	if !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) {
		return CourtProfile{}, domain.Forbidden("an admin or attorney approves court profiles")
	}
	err := audit.WithTx(ctx, s.pool, func(ctx context.Context) error {
		db := audit.Conn(ctx, s.pool)
		cur, err := s.scanProfile(db.QueryRowContext(ctx, `SELECT `+profileCols+` FROM court_profiles WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if cur.Verified {
			return nil
		}
		now := us(s.now())
		if _, err := db.ExecContext(ctx, `UPDATE court_profiles SET verified = 1, verified_by = $1, verified_at = $2, updated_at = $2 WHERE id = $3`, p.UserID, now, id); err != nil {
			return err
		}
		return s.record(ctx, p, "court_profile", id, "verified", "false", "true")
	})
	if err != nil {
		return CourtProfile{}, err
	}
	return s.Profile(ctx, p, id)
}

// profileFiles is every firm profile as LawCraft's profile file, by its LawCraft id, for
// a run's own profile folder.
func (s *Service) profileFiles(ctx context.Context) (map[string][]byte, error) {
	rows, err := audit.Conn(ctx, s.pool).QueryContext(ctx, `SELECT `+profileCols+` FROM court_profiles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]byte{}
	for rows.Next() {
		c, err := s.scanProfile(rows)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(c.Body)
		if err != nil {
			return nil, err
		}
		if id := lawcraftID(c.Body); safeProfileID(id) {
			out[id] = b
		}
	}
	return out, rows.Err()
}

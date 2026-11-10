package calendar

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func newSecret() (string, error) {
	b := make([]byte, sha256.Size) // a 256-bit secret
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// wellFormedSecret accepts only a secret as long as the ones newSecret issues
// (base64url of 32 random bytes), which is above the contract's 20-character minimum.
func wellFormedSecret(secret string) bool {
	return len(secret) >= base64.RawURLEncoding.EncodedLen(sha256.Size)
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func (s *Service) icalEnabled(ctx context.Context) (bool, error) {
	v, err := s.setting(ctx, settingICal)
	if err != nil {
		return false, err
	}
	return v == "true", nil
}

// FeedURL is where a secret is served.
func (s *Service) FeedURL(secret string) string {
	return strings.TrimRight(s.d.BaseURL, "/") + "/ical/" + secret + ".ics"
}

func (s *Service) canManageFeed(p domain.Principal, f Feed) bool {
	if p.HasRole(domain.RoleAdmin) {
		return true
	}
	return !f.OwnerUserID.IsZero() && f.OwnerUserID == p.UserID
}

// CreateFeed issues a feed and returns its secret URL once. A zero owner is the firm
// feed, which only an admin may create. A user feed is for the caller, or any
// user when an admin creates it.
func (s *Service) CreateFeed(ctx context.Context, p domain.Principal, owner domain.ID) (Feed, string, error) {
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return Feed{}, "", err
	}
	on, err := s.icalEnabled(ctx)
	if err != nil {
		return Feed{}, "", err
	}
	if !on {
		return Feed{}, "", domain.Invalid("calendar.ical_enabled", "iCal feeds are turned off on the admin page")
	}
	if owner.IsZero() {
		if !p.HasRole(domain.RoleAdmin) {
			return Feed{}, "", domain.Forbidden("only an admin can create the firm feed")
		}
	} else {
		if owner != p.UserID && !p.HasRole(domain.RoleAdmin) {
			return Feed{}, "", domain.Forbidden("a user feed belongs to its user")
		}
		if _, err := s.activeUser(ctx, "owner_user_id", owner); err != nil {
			return Feed{}, "", err
		}
	}
	secret, err := s.d.Secret()
	if err != nil {
		return Feed{}, "", err
	}
	if !wellFormedSecret(secret) {
		return Feed{}, "", errors.New("calendar: feed secret source returned a short secret")
	}
	m, err := s.meta(p)
	if err != nil {
		return Feed{}, "", err
	}
	f := Feed{Meta: m, OwnerUserID: owner, SecretHash: hashSecret(secret)}
	ownerText := string(owner)
	if owner.IsZero() {
		ownerText = "firm"
	}
	err = s.commit(ctx, p,
		func(ctx context.Context) error { return s.d.Store.PutFeed(ctx, f) },
		func(ctx context.Context) error { return s.d.Store.DeleteFeed(ctx, f.ID) },
		"ical_feed", f.ID, "", "ical_feed.created", "iCal feed created", []change{{"owner_user_id", "", ownerText}})
	if err != nil {
		return Feed{}, "", err
	}
	return f, s.FeedURL(secret), nil
}

// ListFeeds returns every feed for an admin and the caller's own feeds otherwise.
func (s *Service) ListFeeds(ctx context.Context, p domain.Principal) ([]Feed, error) {
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return nil, err
	}
	rows, err := s.d.Store.Feeds(ctx)
	if err != nil {
		return nil, err
	}
	var out []Feed
	for _, f := range rows {
		if s.canManageFeed(p, f) {
			out = append(out, f)
		}
	}
	return out, nil
}

// GetFeed returns one feed the caller manages.
func (s *Service) GetFeed(ctx context.Context, p domain.Principal, id domain.ID) (Feed, error) {
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return Feed{}, err
	}
	f, err := s.d.Store.Feed(ctx, id)
	if err != nil || !s.canManageFeed(p, f) {
		return Feed{}, domain.NotFound("ical feed")
	}
	return f, nil
}

// RevokeFeed kills the secret URL for good. Revoking twice is a no-op.
func (s *Service) RevokeFeed(ctx context.Context, p domain.Principal, id domain.ID) (Feed, error) {
	f, err := s.GetFeed(ctx, p, id)
	if err != nil {
		return Feed{}, err
	}
	if f.Revoked {
		return f, nil
	}
	f.Revoked, f.RevokedAt, f.RevokedBy, f.UpdatedAt = true, s.now(), p.UserID, s.now()
	var before Feed
	err = s.commit(ctx, p,
		func(ctx context.Context) error {
			var err error
			if before, err = s.d.Store.Feed(ctx, f.ID); err != nil {
				return domain.NotFound("ical feed")
			}
			return s.d.Store.PutFeed(ctx, f)
		},
		func(ctx context.Context) error { return s.d.Store.PutFeed(ctx, before) },
		"ical_feed", f.ID, "", "ical_feed.revoked", "iCal feed revoked", []change{{"revoked", "false", "true"}})
	if err != nil {
		return Feed{}, err
	}
	return f, nil
}

// Download renders the feed behind secret. A revoked or unknown secret, an inactive
// owner, or feeds switched off are all not_found. Events and deadlines on matters the
// owner cannot see now are omitted; the firm feed omits every walled matter.
func (s *Service) Download(ctx context.Context, secret string) ([]byte, error) {
	on, err := s.icalEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if !on || !wellFormedSecret(secret) {
		return nil, domain.NotFound("ical feed")
	}
	// Only the SHA-256 is stored, so the lookup never compares secrets themselves.
	f, err := s.d.Store.FeedBySecret(ctx, hashSecret(secret))
	if err != nil || f.Revoked {
		return nil, domain.NotFound("ical feed")
	}
	var see func(matter domain.ID) bool
	name := "Firm calendar"
	if f.OwnerUserID.IsZero() {
		see = func(matter domain.ID) bool {
			if matter.IsZero() {
				return true
			}
			v, err := s.d.Policy.Walls.View(ctx, domain.Principal{}, matter)
			return err == nil && !v.Walled
		}
	} else {
		u, err := s.d.Users.Get(ctx, f.OwnerUserID)
		if err != nil {
			return nil, domain.NotFound("ical feed")
		}
		owner, ok := userPrincipal(u)
		if !ok || s.gate(ctx, owner, domain.ActionRead, "") != nil {
			return nil, domain.NotFound("ical feed")
		}
		name = u.Name + " calendar"
		see = func(matter domain.ID) bool { return s.visible(ctx, owner, matter) }
	}
	events, err := s.d.Store.Events(ctx)
	if err != nil {
		return nil, err
	}
	deadlines, err := s.d.Store.Deadlines(ctx)
	if err != nil {
		return nil, err
	}
	loc := s.Zone(ctx)
	var b icsWriter
	b.line("BEGIN:VCALENDAR")
	b.line("VERSION:2.0")
	b.line("PRODID:-//Togavsu//Calendar//EN")
	b.line("CALSCALE:GREGORIAN")
	b.line("METHOD:PUBLISH")
	b.prop("X-WR-CALNAME", name)
	stamp := icsUTC(s.now())
	for _, e := range events {
		if !f.OwnerUserID.IsZero() && !hasID(e.Attendees, f.OwnerUserID) {
			continue
		}
		if !see(e.MatterID) {
			continue
		}
		b.line("BEGIN:VEVENT")
		b.line("UID:" + string(e.ID) + "@togavsu")
		b.line("DTSTAMP:" + stamp)
		if e.AllDay {
			start := localDate(e.Start(), loc)
			end := localDate(e.End(), loc)
			if !end.After(start) {
				end = nextDay(start)
			}
			b.line("DTSTART;VALUE=DATE:" + start.Format("20060102"))
			b.line("DTEND;VALUE=DATE:" + end.Format("20060102"))
		} else {
			b.line("DTSTART:" + icsUTC(e.Start()))
			b.line("DTEND:" + icsUTC(e.End()))
		}
		b.prop("SUMMARY", e.Title)
		if e.Location != "" {
			b.prop("LOCATION", e.Location)
		}
		b.prop("CATEGORIES", strings.ToUpper(e.Kind))
		b.line("END:VEVENT")
	}
	for _, d := range deadlines {
		if d.Status != StatusOpen {
			continue
		}
		if !f.OwnerUserID.IsZero() && d.ResponsibleUserID != f.OwnerUserID {
			continue
		}
		if !see(d.MatterID) {
			continue
		}
		due, err := parseDate(d.DueOn)
		if err != nil {
			continue
		}
		b.line("BEGIN:VEVENT")
		b.line("UID:deadline-" + string(d.ID) + "@togavsu")
		b.line("DTSTAMP:" + stamp)
		b.line("DTSTART;VALUE=DATE:" + due.Format("20060102"))
		b.line("DTEND;VALUE=DATE:" + nextDay(due).Format("20060102"))
		b.prop("SUMMARY", "Deadline: "+d.Title)
		b.prop("CATEGORIES", "DEADLINE")
		b.line("END:VEVENT")
	}
	b.line("END:VCALENDAR")
	return []byte(b.String()), nil
}

func icsUTC(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

// foldWidth is RFC 5545 section 3.1: content lines are folded at 75 octets,
// excluding the CRLF. It is a protocol constant, not a setting.
func foldWidth() int { return 75 }

// icsWriter writes RFC 5545 content lines: CRLF endings, text escaping, and
// folding at 75 octets without splitting a UTF-8 sequence.
type icsWriter struct{ strings.Builder }

func (w *icsWriter) prop(name, value string) { w.line(name + ":" + icsEscape(value)) }

func (w *icsWriter) line(s string) {
	limit := foldWidth()
	for len(s) > limit {
		cut := limit
		for !utf8.RuneStart(s[cut]) {
			cut--
		}
		w.WriteString(s[:cut])
		w.WriteString("\r\n ")
		s = s[cut:]
		limit = foldWidth() - 1 // a continuation line starts with one space
	}
	w.WriteString(s)
	w.WriteString("\r\n")
}

func icsEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`)
	return r.Replace(s)
}

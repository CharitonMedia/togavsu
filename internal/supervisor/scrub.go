package supervisor

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
)

var postgresURL = regexp.MustCompile(`postgres(?:ql)?://[^\s'"]+`)

// Scrub removes credentials from a string before it is logged or returned.
func Scrub(s string) string {
	s = postgresURL.ReplaceAllStringFunc(s, func(match string) string {
		scheme, rest, ok := strings.Cut(match, "://")
		if !ok {
			return scheme + "://***"
		}
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			user := rest[:at]
			if colon := strings.Index(user, ":"); colon >= 0 {
				user = user[:colon] + ":***"
			}
			return scheme + "://" + user + "@" + rest[at+1:]
		}
		return scheme + "://***"
	})
	if strings.Contains(strings.ToLower(s), "bearer ") {
		s = regexp.MustCompile(`(?i)bearer\s+\S+`).ReplaceAllString(s, "Bearer ***")
	}
	return s
}

// ScrubHandler drops log attributes whose keys carry secrets and scrubs values.
type ScrubHandler struct {
	Next slog.Handler
}

func (h ScrubHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.Next.Enabled(ctx, level)
}

func (h ScrubHandler) Handle(ctx context.Context, rec slog.Record) error {
	next := slog.NewRecord(rec.Time, rec.Level, Scrub(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		next.AddAttrs(scrubAttr(a))
		return true
	})
	return h.Next.Handle(ctx, next)
}

func (h ScrubHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = scrubAttr(a)
	}
	return ScrubHandler{Next: h.Next.WithAttrs(clean)}
}

func (h ScrubHandler) WithGroup(name string) slog.Handler {
	return ScrubHandler{Next: h.Next.WithGroup(name)}
}

func scrubAttr(a slog.Attr) slog.Attr {
	if secretKey(a.Key) {
		return slog.String(a.Key, "***")
	}
	if a.Value.Kind() == slog.KindString {
		return slog.String(a.Key, Scrub(a.Value.String()))
	}
	return a
}

func secretKey(key string) bool {
	k := strings.ToLower(key)
	switch {
	case strings.Contains(k, "passphrase"),
		strings.Contains(k, "password"),
		strings.Contains(k, "secret"),
		strings.Contains(k, "token"),
		strings.Contains(k, "authorization"):
		return true
	default:
		return false
	}
}

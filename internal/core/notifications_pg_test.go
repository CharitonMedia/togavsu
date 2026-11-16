package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// TestCORE03CreateNotification: module notification kinds and createNotification
// (success plus wrong kind, bad link, wall drop, service-only recipient_roles).
func TestCORE03CreateNotification(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("tabs:\n"),
			[]byte("notifications:\n  - kind: hours_low\n    label: Hours below target\n    default_channels: [in_app, email]\ntabs:\n"), 1)
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("  - scope: events:read\n"),
			[]byte("  - scope: notifications:write\n    reason: Notify the firm.\n  - scope: events:read\n"), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]
	kind := "matter-clock.hours_low"
	link := "/modules/matter-clock/tabs/hours"

	open, _ := x.matter("Open Estate", "", nil)
	walled, _ := x.matter("Walled Estate", "", nil)
	if err := x.app.Practice.SetWall(ctx, x.adminP(), domain.ID(walled), []domain.ID{domain.ID(x.attyID)}); err != nil {
		t.Fatal(err)
	}

	// Success: service call to one active user.
	ok := bot.must(http.StatusCreated, "POST", "/api/v1/notifications", map[string]any{
		"kind": kind, "title": "Hours are low", "link": link, "user_id": x.staffID,
	})
	if ok.json["created"] != float64(1) {
		t.Fatalf("created: %s", ok.body)
	}
	ids, _ := ok.json["notification_ids"].([]any)
	if len(ids) != 1 {
		t.Fatalf("ids: %s", ok.body)
	}
	list := x.staff.must(http.StatusOK, "GET", "/api/v1/notifications", nil)
	if !strings.Contains(list.body, "Hours are low") {
		t.Fatalf("staff inbox: %s", list.body)
	}

	// Wrong kind.
	if r := bot.do("POST", "/api/v1/notifications", map[string]any{
		"kind": "matter-clock.unknown", "title": "t", "link": link, "user_id": x.staffID,
	}); r.code != http.StatusBadRequest || !strings.Contains(r.body, "kind") {
		t.Fatalf("wrong kind: %d %s", r.code, r.body)
	}

	// Bad link (not under this module).
	if r := bot.do("POST", "/api/v1/notifications", map[string]any{
		"kind": kind, "title": "t", "link": "/modules/other/x", "user_id": x.staffID,
	}); r.code != http.StatusBadRequest || !strings.Contains(r.body, "link") {
		t.Fatalf("bad link: %d %s", r.code, r.body)
	}

	// Service call on a walled matter is not_found (CU-02): no created count from wall membership.
	before := x.countNotifications(ctx, x.adminID, kind)
	if r := bot.do("POST", "/api/v1/notifications", map[string]any{
		"kind": kind, "title": "Walled ping", "link": link, "matter_id": walled, "audience": "admins",
	}); r.code != http.StatusNotFound || r.json["code"] != "not_found" {
		t.Fatalf("walled service audience: %d %s", r.code, r.body)
	}
	if r := bot.do("POST", "/api/v1/notifications", map[string]any{
		"kind": kind, "title": "Walled user", "link": link, "matter_id": walled, "user_id": x.attyID,
	}); r.code != http.StatusNotFound || r.json["code"] != "not_found" {
		t.Fatalf("walled service user_id: %d %s", r.code, r.body)
	}
	if x.countNotifications(ctx, x.adminID, kind) != before {
		t.Fatal("walled service call wrote a notification")
	}
	// Same audience on an open matter reaches the admin.
	reached := bot.must(http.StatusCreated, "POST", "/api/v1/notifications", map[string]any{
		"kind": kind, "title": "Open ping", "link": link, "matter_id": open, "audience": "admins",
	})
	if reached.json["created"].(float64) < 1 {
		t.Fatalf("open admins: %s", reached.body)
	}

	// recipient_roles is service-call only: viewer form is forbidden.
	ct := x.staff.must(http.StatusCreated, "POST", "/api/v1/modules/matter-clock/context-tokens", map[string]any{})
	tok := str(t, ct.json, "token")
	if r := bot.do("POST", "/api/v1/notifications", map[string]any{
		"kind": kind, "title": "Roles", "link": link, "recipient_roles": []string{"admin"},
	}, modules.ContextHeader, tok); r.code != http.StatusForbidden {
		t.Fatalf("viewer recipient_roles: %d %s", r.code, r.body)
	}
	roles := bot.must(http.StatusCreated, "POST", "/api/v1/notifications", map[string]any{
		"kind": kind, "title": "Roles ok", "link": link, "recipient_roles": []string{"admin"},
	})
	if roles.json["created"].(float64) < 1 {
		t.Fatalf("service recipient_roles: %s", roles.body)
	}

	// Prefs grid includes the module group while enabled.
	prefs := x.staff.must(http.StatusOK, "GET", "/api/v1/me/notification-preferences", nil)
	if !strings.Contains(prefs.body, kind) {
		t.Fatalf("prefs missing module kind: %s", prefs.body)
	}
}

func (x *ext) countNotifications(ctx context.Context, user, kind string) int {
	var n int
	if err := x.app.Pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND kind = $2`, user, kind).Scan(&n); err != nil {
		x.t.Fatal(err)
	}
	return n
}

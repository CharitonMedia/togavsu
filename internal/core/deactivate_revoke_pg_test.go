package core

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestDeactivateUserSurfacesRevokeError: when device revoke fails after deactivate, the
// handler returns the error so the caller can retry (CORE-28).
func TestDeactivateUserSurfacesRevokeError(t *testing.T) {
	x := newExt(t)
	_, staffID := enrollUser(t, x.app, x.srv.URL, x.admin, "Revoke Fail", "revoke-fail@firm.example", "staff")
	x.app.revokeDevicesForUser = func(ctx context.Context, p domain.Principal, userID domain.ID) error {
		return domain.Conflict("device revoke failed")
	}
	r := x.admin.do("POST", "/api/v1/users/"+string(staffID)+"/deactivate", nil)
	if r.code != http.StatusConflict {
		t.Fatalf("failing revoke: want 409, got %d %s", r.code, r.body)
	}
	if !strings.Contains(r.body, "device revoke failed") {
		t.Fatalf("error body: %s", r.body)
	}
	// User is deactivated; a later successful revoke path still works on retry.
	x.app.revokeDevicesForUser = nil
	retry := x.admin.must(http.StatusOK, "POST", "/api/v1/users/"+string(staffID)+"/deactivate", nil)
	if retry.json["active"] != false {
		t.Fatalf("retry after revoke failure: %s", retry.body)
	}
}

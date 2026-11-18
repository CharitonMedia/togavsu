package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// TestInstallPlainSentences maps known codes to fixed sentences, never appends raw
// service text, and words marketplace update failures as updates.
func TestInstallPlainSentences(t *testing.T) {
	rawMigration := &domain.APIError{
		Code:    modules.CodeMigration,
		Message: "APPLY 0001_init.sql failed on statement 2",
	}
	got := installPlain("Matter Clock", rawMigration, false)
	var api *domain.APIError
	if !errors.As(got, &api) {
		t.Fatalf("want APIError, got %T %v", got, got)
	}
	if strings.Contains(api.Message, "APPLY") || strings.Contains(api.Message, "0001_init") {
		t.Fatalf("raw migration text leaked: %q", api.Message)
	}
	if api.Message != "Matter Clock could not be installed because its database migration was refused." {
		t.Fatalf("install migration sentence: %q", api.Message)
	}

	upd := installPlain("Matter Clock", rawMigration, true)
	if !errors.As(upd, &api) {
		t.Fatalf("update want APIError, got %T %v", upd, upd)
	}
	if strings.Contains(api.Message, "installed") || strings.Contains(api.Message, "APPLY") {
		t.Fatalf("update failure must say updated and stay plain: %q", api.Message)
	}
	if api.Message != "Matter Clock could not be updated because its database migration was refused." {
		t.Fatalf("update migration sentence: %q", api.Message)
	}

	yanked := &marketplace.Error{Code: "yanked", Message: "yanked: this module was pulled from the marketplace"}
	u := installPlain("Module mod-beta", yanked, true)
	if !errors.As(u, &api) || !strings.Contains(api.Message, "can't be updated") || strings.Contains(api.Message, "yanked:") {
		t.Fatalf("yanked update sentence: %q", api.Message)
	}

	unknown := &domain.APIError{Code: "something_new", Message: "opaque detail without a colon"}
	g := installPlain("Widget", unknown, false)
	if !errors.As(g, &api) || api.Message != "Widget could not be installed from the marketplace." {
		t.Fatalf("unknown code must stay generic, got %q", api.Message)
	}
	gu := installPlain("Widget", unknown, true)
	if !errors.As(gu, &api) || api.Message != "Widget could not be updated from the marketplace." {
		t.Fatalf("unknown update must stay generic update sentence, got %q", api.Message)
	}
}

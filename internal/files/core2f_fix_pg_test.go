package files

import (
	"context"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestCoreUpdate2FFixStatusOwnership(t *testing.T) {
	e := newEnv(t)
	e.svc.SetModuleCheck(func(context.Context, string) (bool, error) { return true, nil })
	for _, source := range []string{"upload", "editor", "billing", "email", "zoom", "importer", "synthetic-module"} {
		f, err := e.svc.Create(context.Background(), e.staff, NewFile{Name: "core.txt", MatterID: e.open, Source: source}, strings.NewReader("x"), 1)
		if err != nil {
			t.Fatal(err)
		}
		p := domain.Principal{Source: domain.SourceService, ModuleID: source}
		if _, err := e.svc.MarkStatus(context.Background(), p, f.ID, "final"); !isCode(err, "scope_denied") {
			t.Errorf("source %s conferred module ownership: %v", source, err)
		}
	}
}

func TestCoreUpdate2FFixStatusExistenceOracle(t *testing.T) {
	e := newEnv(t)
	e.svc.SetModuleCheck(func(context.Context, string) (bool, error) { return true, nil })
	f, err := e.svc.Create(context.Background(), e.staff, NewFile{Name: "other.txt", MatterID: e.open, Source: "other-module"}, strings.NewReader("x"), 1)
	if err != nil {
		t.Fatal(err)
	}
	p := domain.Principal{Source: domain.SourceService, ModuleID: "status-module"}
	for _, status := range []string{"final", "executed"} {
		for _, id := range []domain.ID{f.ID, "00000000-0000-0000-0000-000000000000"} {
			if _, err := e.svc.MarkStatus(context.Background(), p, id, status); !isCode(err, "scope_denied") {
				t.Errorf("%s %s: %v", status, id, err)
			}
		}
	}
}

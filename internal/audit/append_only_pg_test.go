package audit

import (
	"context"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/repository"
)

// TestOwnerCanStillChangeAuditRows records the ceiling: after InstallPrivileges the
// table owner, including a superuser, can still UPDATE audit_log. The non-owner writer
// is refused in TestNonOwnerRoleCannotChangeAuditRows. There is no trigger.
func TestOwnerCanStillChangeAuditRows(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	for i := 0; i < 2; i++ {
		if err := InstallPrivileges(ctx, sqlDB); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}
	svc, err := New(NewPostgres(sqlDB), func() time.Time { return time.Unix(1_700_000_000, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	who := domain.Principal{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
	row, err := svc.Record(ctx, who, Change{Entity: "matter", EntityID: domain.SystemUserID, Field: "name", Old: "A", New: "B"})
	if err != nil {
		t.Fatal(err)
	}
	// The settings store inserts as the application role; that stays allowed.
	if _, err := sqlDB.ExecContext(ctx, `INSERT INTO audit_log (id, created_at, updated_at, created_by, actor_id, role, source, entity, entity_id, field, old_value, new_value, reason)
		VALUES ('raw-insert', 1, 1, 'system', 'system', 'admin', 'ui', 'setting', 'k', 'value', '', 'v', 'settings update')`); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, "UPDATE audit_log SET reason = 'owner-edit' WHERE id = $1", string(row.ID)); err != nil {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, row.ID)
	if err != nil || got.Reason != "owner-edit" {
		t.Fatalf("owner update did not stick: %+v %v", got, err)
	}
	if n := len(mustList(t, svc)); n != 2 {
		t.Fatalf("%d rows", n)
	}
}

func mustList(t *testing.T, svc *Service) []domain.AuditEntry {
	t.Helper()
	page, err := svc.store.List(context.Background(), repository.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

// TestAtomicRollsBackWriteWithFailedAppend: a write made inside Atomic is discarded
// when the audit append in the same unit fails, and kept with its row when it succeeds.
func TestAtomicRollsBackWriteWithFailedAppend(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	if err := InstallPrivileges(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	svc, err := New(NewPostgres(sqlDB), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	who := domain.Principal{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}
	first, err := svc.Record(ctx, who, Change{Entity: "practice_area", EntityID: "pa-0", Field: "name", New: "Seed"})
	if err != nil {
		t.Fatal(err)
	}
	insertArea := func(ctx context.Context, id string) error {
		_, err := Conn(ctx, sqlDB).ExecContext(ctx, `INSERT INTO practice_areas (id, created_at, updated_at, created_by, name, matter_types, stages, active)
			VALUES ($1, 1, 1, 'system', 'Probate', '[]', '[]', 1)`, id)
		return err
	}
	areas := func(id string) int {
		var n int
		if err := sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM practice_areas WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// The append reuses an id that exists, so the insert into audit_log fails.
	svc.ids = func() (domain.ID, error) { return first.ID, nil }
	err = svc.Atomic(ctx, func(ctx context.Context) error {
		if err := insertArea(ctx, "pa-1"); err != nil {
			return err
		}
		_, err := svc.Record(ctx, who, Change{Entity: "practice_area", EntityID: "pa-1", Field: "name", New: "Probate"})
		return err
	})
	if err == nil {
		t.Fatal("append with a duplicate id succeeded")
	}
	if areas("pa-1") != 0 {
		t.Fatal("write kept without its audit row")
	}
	svc.ids = domain.NewID
	// The audit row is written first and the write after it fails: the row goes too.
	if err := insertArea(ctx, "pa-seed"); err != nil {
		t.Fatal(err)
	}
	err = svc.Atomic(ctx, func(ctx context.Context) error {
		if _, err := svc.Record(ctx, who, Change{Entity: "practice_area", EntityID: "pa-3", Field: "name", New: "Probate"}); err != nil {
			return err
		}
		return insertArea(ctx, "pa-seed")
	})
	if err == nil {
		t.Fatal("duplicate practice area insert succeeded")
	}
	if rows, err := svc.ForEntity(ctx, "practice_area", "pa-3"); err != nil || len(rows) != 0 {
		t.Fatalf("audit row kept without its write: %v %v", rows, err)
	}
	err = svc.Atomic(ctx, func(ctx context.Context) error {
		if err := insertArea(ctx, "pa-2"); err != nil {
			return err
		}
		_, err := svc.Record(ctx, who, Change{Entity: "practice_area", EntityID: "pa-2", Field: "name", New: "Probate"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := svc.ForEntity(ctx, "practice_area", "pa-2")
	if areas("pa-2") != 1 || err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
}

package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

const testAdminDSN = "postgres://togavsu:togavsu@127.0.0.1:5432/togavsu?sslmode=disable"

func TestNonOwnerRoleCannotChangeAuditRows(t *testing.T) {
	ctx := context.Background()
	sqlDB := openCoreDB(t)
	if err := InstallPrivileges(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	store := NewPostgres(sqlDB)
	svc, err := New(store, func() time.Time { return time.Unix(1_700_000_000, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	who := domain.Principal{UserID: domain.SystemUserID, Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceAPIToken, ModuleID: "mod-test"}
	row, err := svc.Record(ctx, who, Change{
		Entity: "matter", EntityID: domain.SystemUserID, Field: "name", Old: "A", New: "B", Reason: "rename",
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.Source != string(domain.SourceAPIToken) || !strings.Contains(row.Reason, "module=mod-test") {
		t.Fatalf("provenance %+v", row)
	}
	if err := RawAsWriter(ctx, sqlDB, "UPDATE audit_log SET reason = 'raw' WHERE id = $1", string(row.ID)); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatal(err)
	}
	if err := RawAsWriter(ctx, sqlDB, "DELETE FROM audit_log WHERE id = $1", string(row.ID)); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatal(err)
	}
	got, err := svc.Get(ctx, row.ID)
	if err != nil || got.NewValue != "B" || got.Reason != row.Reason {
		t.Fatalf("row changed: %+v %v", got, err)
	}
	if err := svc.Mutate(ctx, row.ID); err == nil {
		t.Fatal("service mutate")
	}
	typ := reflect.TypeOf(store)
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if strings.Contains(name, "Update") || strings.Contains(name, "Delete") {
			t.Fatalf("production store has %s", name)
		}
	}
}

func openCoreDB(t *testing.T) *sql.DB {
	t.Helper()
	admin, err := db.OpenPostgres(testAdminDSN, "")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err := admin.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(t.Name()))
	name := "t" + hex.EncodeToString(sum[:8])
	if _, err := admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(context.Background(), "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin, err := db.OpenPostgres(testAdminDSN, "")
		if err != nil {
			return
		}
		defer admin.Close()
		_, _ = admin.ExecContext(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
		_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+name)
	})
	plain, err := db.OpenPostgres(fmt.Sprintf("postgres://togavsu:togavsu@127.0.0.1:5432/%s?sslmode=disable", name), "")
	if err != nil {
		t.Fatal(err)
	}
	ms, err := db.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Apply(context.Background(), plain, db.ApplySpec{Owner: db.CoreOwner, Schema: "core", Dialect: "postgres", Migrations: ms}); err != nil {
		t.Fatal(err)
	}
	_ = plain.Close()
	out, err := db.OpenPostgres(fmt.Sprintf("postgres://togavsu:togavsu@127.0.0.1:5432/%s?sslmode=disable", name), "core")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = out.Close() })
	return out
}

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/bootstrap"
)

func TestHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ok.Close()
	if got := healthcheck(ok.URL, io.Discard); got != 0 {
		t.Fatalf("healthy core = %d", got)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer down.Close()
	if got := healthcheck(down.URL, io.Discard); got != 1 {
		t.Fatalf("unavailable core = %d", got)
	}
	down.Close()
	if got := healthcheck(down.URL, io.Discard); got != 1 {
		t.Fatalf("no core = %d", got)
	}
}

// syntheticEnv sets the four bootstrap values to synthetic ones; the database is never
// reached in these tests.
func syntheticEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TOGAVSU_DATABASE_URL", "postgres://togavsu_app:synthetic@127.0.0.1:1/togavsu?sslmode=disable")
	t.Setenv("TOGAVSU_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("TOGAVSU_DOMAIN", "localhost")
	t.Setenv("TOGAVSU_SUPERVISOR_SECRET", "synthetic-supervisor-secret")
}

// TestMigrateFailsWithoutSuperuserURL (AC-024, AC-006): core migrate without the
// superuser URL exits 1 and names the missing value instead of skipping the seal.
func TestMigrateFailsWithoutSuperuserURL(t *testing.T) {
	syntheticEnv(t)
	t.Setenv(bootstrap.AdminDatabaseURLName, "")
	var out, errb bytes.Buffer
	if got := migrate(context.Background(), &out, &errb); got != 1 {
		t.Fatalf("migrate without the superuser url = %d, stderr %q", got, errb.String())
	}
	if !strings.Contains(errb.String(), bootstrap.AdminDatabaseURLName) || !strings.Contains(out.String(), "migrated=false") {
		t.Fatalf("stdout %q stderr %q", out.String(), errb.String())
	}
}

// TestServeRefusesSuperuserURL (AC-006): the serving core exits 2 before it opens the
// database when the superuser URL is in its environment.
func TestServeRefusesSuperuserURL(t *testing.T) {
	syntheticEnv(t)
	t.Setenv(bootstrap.AdminDatabaseURLName, "postgres://togavsu:synthetic@127.0.0.1:1/postgres")
	var out, errb bytes.Buffer
	if got := serve(context.Background(), &out, &errb); got != 2 {
		t.Fatalf("serve with the superuser url = %d, stderr %q", got, errb.String())
	}
	if !strings.Contains(errb.String(), "refusing to serve") {
		t.Fatalf("stderr %q", errb.String())
	}
}

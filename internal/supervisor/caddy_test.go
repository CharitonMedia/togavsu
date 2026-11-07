package supervisor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaddySwitchPostsUpstreamAndRollsBack(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/load" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		got = string(body)
		if strings.Contains(got, "bad:8080") {
			http.Error(w, "no", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	file := filepath.Join(t.TempDir(), "upstream")
	proxy := &CaddyProxy{AdminURL: srv.URL, UpstreamFile: file, HTTP: srv.Client()}
	if err := proxy.Switch(context.Background(), "core-next:8080"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "core-next:8080") || strings.Contains(got, "core:8080") || !strings.Contains(got, ":443") {
		t.Fatalf("posted %s", got)
	}
	raw, err := os.ReadFile(file)
	if err != nil || strings.TrimSpace(string(raw)) != "core-next:8080" {
		t.Fatalf("file %q %v", raw, err)
	}
	if err := proxy.Switch(context.Background(), "bad:8080"); err == nil {
		t.Fatal("accepted a rejected load")
	}
	raw, err = os.ReadFile(file)
	if err != nil || strings.TrimSpace(string(raw)) != "core-next:8080" {
		t.Fatalf("rollback file %q %v", raw, err)
	}
	if err := proxy.Switch(context.Background(), "not a dial"); err == nil {
		t.Fatal("accepted an unsafe upstream")
	}
}

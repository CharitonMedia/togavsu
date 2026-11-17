package modules_test

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/modules"
)

// TestCORE08SidebarBadgeFetch covers badge path fetches: viewer context token, 500 ms
// limit, 30-second cache, and leaving the badge off on failure, non-200, negative count,
// or malformed body (CORE-08). Calls the service badge counter directly because the
// modules harness does not mount dashboard GET /api/v1/nav/context.
func TestCORE08SidebarBadgeFetch(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int64
	var mode atomic.Value
	mode.Store("ok")
	e.rt.Handle("matter-clock", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/badge" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		hits.Add(1)
		if r.Header.Get(modules.ContextHeader) == "" {
			t.Error("badge fetch missing context token")
		}
		switch mode.Load().(string) {
		case "slow":
			time.Sleep(600 * time.Millisecond)
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 1})
		case "500":
			w.WriteHeader(http.StatusInternalServerError)
		case "negative":
			_ = json.NewEncoder(w).Encode(map[string]any{"count": -1})
		case "malformed":
			_, _ = w.Write([]byte(`{"count":"x"}`))
		case "ok":
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 4})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	raw := e.build(func(f map[string][]byte) {
		replace(f, modules.ManifestFile, "widgets:\n",
			"sidebar:\n  - id: hours\n    title: Hours\n    path: /tab\n    section: money\n    icon: mail\n    order: 1\n"+
				"    badge:\n      path: /badge\n      tone: warning\nwidgets:\n")
	})
	r := e.do("POST", "/api/v1/modules", raw, e.as("admin")...)
	if r.code != http.StatusAccepted {
		t.Fatalf("install: %d %s", r.code, r.body)
	}
	e.approveAll("matter-clock")

	inst, err := e.svc.Installed(e.ctx, "matter-clock")
	if err != nil {
		t.Fatal(err)
	}
	staff := e.users["staff"]
	count := func() (int, bool) {
		t.Helper()
		n, ok, err := e.svc.BadgeCountForTest(e.ctx, staff, "matter-clock", "hours", inst.Manifest)
		if err != nil {
			t.Fatal(err)
		}
		return n, ok
	}

	n, ok := count()
	if !ok || n != 4 {
		t.Fatalf("badge count: n=%d ok=%v hits=%d", n, ok, hits.Load())
	}
	firstHits := hits.Load()
	if n2, ok2 := count(); !ok2 || n2 != 4 || hits.Load() != firstHits {
		t.Fatalf("cache miss within 30s: n=%d ok=%v hits %d -> %d", n2, ok2, firstHits, hits.Load())
	}

	for _, bad := range []string{"500", "negative", "malformed", "slow"} {
		mode.Store(bad)
		modules.ClearBadgeCacheForTest()
		before := hits.Load()
		n, ok = count()
		if ok {
			t.Fatalf("%s left badge on: n=%d", bad, n)
		}
		if hits.Load() <= before && bad != "slow" {
			// slow still hits the module; timeout may or may not increment after Do returns
		}
		if bad != "slow" && hits.Load() <= before {
			t.Fatalf("%s did not fetch: hits %d", bad, hits.Load())
		}
		if bad == "slow" && hits.Load() < before {
			t.Fatalf("slow lost hits: %d -> %d", before, hits.Load())
		}
	}
}

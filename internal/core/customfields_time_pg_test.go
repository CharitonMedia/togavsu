package core

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/CharitonMedia/togavsu/api"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

const extTimeManifest = `id: time-plus
name: Time Plus
version: "1.0.0"
author: Synthetic author
container:
  image: ghcr.io/example/time-plus
  digest: sha256:3333333333333333333333333333333333333333333333333333333333333333
  port: 8080
permissions:
  - scope: matters:read
replaces: time
`

// extTimeModule implements the core time contract's paths. It knows the time entries in
// known, refuses custom-field values for refuse, answers 503 to everything but its
// contract while down is set, and records every custom-fields write it accepts or refuses.
type extTimeModule struct {
	known  map[string]bool
	refuse string
	down   atomic.Bool
	mu     sync.Mutex
	puts   []string
}

func (m *extTimeModule) writes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.puts...)
}

func (m *extTimeModule) handler(t *testing.T) http.Handler {
	t.Helper()
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(api.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	paths := map[string]map[string]json.RawMessage{}
	for p, ops := range spec.Paths {
		if modules.DomainOf(p) == modules.DomainTime {
			paths[p] = ops
		}
	}
	doc, _ := json.Marshal(map[string]any{"openapi": "3.1.0", "paths": paths})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/openapi.json" {
			_, _ = w.Write(doc)
			return
		}
		if m.down.Load() {
			http.Error(w, `{"code":"unavailable","message":"down"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/time-entries/")
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "served_by": "time-plus"})
			return
		}
		id, custom := strings.CutSuffix(rest, "/custom-fields")
		if !m.known[id] {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"not_found","message":"time entry"}`))
			return
		}
		if custom && r.Method == http.MethodPut {
			m.mu.Lock()
			m.puts = append(m.puts, id)
			m.mu.Unlock()
			if id == m.refuse {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code":"validation","message":"refused"}`))
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "served_by": "time-plus"})
	})
}

// TestReleaseBulkCustomFieldsReplacedTime is AC-002 for bulkCustomFieldValue while a
// module replaces time: every forwarded record is resolved on the module before the first
// write, so a missing record is not_found and an unavailable replacement is
// batch_rejected, and neither writes a module value or the batch's core matter item.
func TestReleaseBulkCustomFieldsReplacedTime(t *testing.T) {
	x := newExt(t)
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	open, _ := x.matter("Estate of Rowe", "", nil)
	def := func(record, key string) string {
		return str(t, x.admin.must(http.StatusCreated, "POST", "/api/v1/custom-field-definitions",
			map[string]any{"record_type": record, "key": key, "label": key, "field_type": "text"}).json, "id")
	}
	room := def("matter", "court_room")
	phase := def("time_entry", "phase")
	newID := func() string {
		id, err := domain.NewID()
		if err != nil {
			t.Fatal(err)
		}
		return string(id)
	}
	one, two, refused, missing := newID(), newID(), newID(), newID()
	mod := &extTimeModule{known: map[string]bool{one: true, two: true, refused: true}, refuse: refused}
	x.rt.Handle("time-plus", mod.handler(t))
	if _, problem := x.tabForm(x.admin, "install", nil, x.build(onlyManifest(extTimeManifest))); problem != "" {
		t.Fatalf("install time-plus: %s", problem)
	}
	x.approveOnTab("time-plus")
	if _, problem := x.tabForm(x.admin, "domain", url.Values{"domain": {"time"}, "module_id": {"time-plus"}}, nil); problem != "" {
		t.Fatalf("switch time: %s", problem)
	}

	item := func(typ, id, def, v string) map[string]any {
		return map[string]any{"record_type": typ, "record_id": id, "values": []map[string]any{{"definition_id": def, "key": "k", "value": v}}}
	}
	bulk := func(items ...map[string]any) resp {
		return x.staff.do("POST", "/api/v1/custom-field-values:bulk", map[string]any{"items": items})
	}
	roomValue := func() any {
		t.Helper()
		g := x.staff.must(http.StatusOK, "GET", "/api/v1/matters/"+open, nil)
		list, _ := g.json["custom_fields"].([]any)
		for _, v := range list {
			if m := v.(map[string]any); m["definition_id"] == room {
				return m["value"]
			}
		}
		return nil
	}

	// A record the module does not have is not_found before any module write.
	if r := bulk(item("matter", open, room, "1A"), item("time_entry", one, phase, "x"), item("time_entry", missing, phase, "y")); r.code != http.StatusNotFound {
		t.Fatalf("batch with a missing time entry: %d %s", r.code, r.body)
	}
	if got := mod.writes(); len(got) != 0 || roomValue() == "1A" {
		t.Fatalf("a not_found batch wrote: module %v, matter %v", got, roomValue())
	}

	// An unavailable replacement rejects the batch, core matter item included.
	mod.down.Store(true)
	r := bulk(item("matter", open, room, "1A"), item("time_entry", one, phase, "x"))
	mod.down.Store(false)
	if r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "batch_rejected") {
		t.Fatalf("batch with the replacement down: %d %s", r.code, r.body)
	}
	if got := mod.writes(); len(got) != 0 || roomValue() == "1A" {
		t.Fatalf("a batch rejected for an unavailable replacement wrote: module %v, matter %v", got, roomValue())
	}

	// A good batch writes the module's items there and the core's matter item here.
	if r := bulk(item("matter", open, room, "2B"), item("time_entry", one, phase, "x"), item("time_entry", two, phase, "y")); r.code != http.StatusCreated {
		t.Fatalf("good batch: %d %s", r.code, r.body)
	}
	if got := mod.writes(); len(got) != 2 || roomValue() != "2B" {
		t.Fatalf("good batch: module %v, matter %v", got, roomValue())
	}

	// A module that refuses an item's values rejects the batch and the core's item stays
	// unwritten (the documented ceiling: the module keeps a value it already accepted).
	if r := bulk(item("matter", open, room, "3C"), item("time_entry", refused, phase, "z")); r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "batch_rejected") {
		t.Fatalf("batch the module refused: %d %s", r.code, r.body)
	}
	if roomValue() != "2B" {
		t.Fatalf("a refused batch wrote the core item: %v", roomValue())
	}
}

package billing

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestReportWriteJSONMatchesEncoder: the report writer's bytes equal json.Encoder's for
// the cell types reports use, strings needing escapes, and values it hands to
// encoding/json.
func TestReportWriteJSONMatchesEncoder(t *testing.T) {
	for _, r := range []Report{
		{Report: "empty", Columns: []string{}, Rows: []map[string]any{}},
		{Report: "nil"},
		{Report: "unbilled-time", Columns: []string{"kind", "id", "amount_cents"}, Rows: []map[string]any{
			{"kind": "time", "id": domain.ID("019b-1"), "amount_cents": int64(-1250), "hours": "1.5", "rate_cents": "", "n": 3, "c": domain.Cents(7)},
			{"description": `Quote " slash \ tab	 <b>&amp;</b> café ✓ ` + "\u2028 \x01", "ok": true, "none": nil, "f": 1.25,
				"nested": map[string]any{"z": 1, "a": []string{"x"}}},
			nil,
			{"kind": "time", "id": domain.ID("019b-2"), "amount_cents": int64(5), "hours": "0.10", "rate_cents": "", "n": 4, "c": domain.Cents(8)},
			{"kind": "time", "id": domain.ID("019b-3"), "amount_cents": int64(6), "hours": "0.20", "rate_cents": "", "n": 5, "other": "x"},
			{"kind": "time"},
		}},
	} {
		var want bytes.Buffer
		if err := json.NewEncoder(&want).Encode(r); err != nil {
			t.Fatal(err)
		}
		var got bytes.Buffer
		if err := r.WriteJSON(&got); err != nil {
			t.Fatal(err)
		}
		if got.String() != want.String() {
			t.Fatalf("%s:\ngot  %s\nwant %s", r.Report, got.String(), want.String())
		}
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

// TestReportWriteJSONReportsWriteErrors: a failed write is returned, not swallowed.
func TestReportWriteJSONReportsWriteErrors(t *testing.T) {
	r := Report{Report: "x", Columns: []string{"a"}, Rows: []map[string]any{{"a": "b"}}}
	if err := r.WriteJSON(failWriter{}); err == nil {
		t.Fatal("want a write error")
	}
	bad := Report{Report: "x", Rows: []map[string]any{{"a": make(chan int)}}}
	if err := bad.WriteJSON(&bytes.Buffer{}); err == nil {
		t.Fatal("want an encoding error for an unsupported value")
	}
}

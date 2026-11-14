package billing

import (
	"bytes"
	"encoding/binary"
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

// TestPlain8MatchesTable: the eight-byte test agrees with the per-byte table on every
// byte value in every position, and on mixed words.
func TestPlain8MatchesTable(t *testing.T) {
	check := func(b [8]byte) {
		want := true
		for _, c := range b {
			want = want && jsonPlain[c]
		}
		if got := plain8(binary.LittleEndian.Uint64(b[:])); got != want {
			t.Fatalf("plain8(%q) = %v, want %v", b[:], got, want)
		}
	}
	for pos := 0; pos < 8; pos++ {
		for c := 0; c < 256; c++ {
			b := [8]byte{'a', 'b', 'c', '1', '-', ' ', 'z', '~'}
			b[pos] = byte(c)
			check(b)
		}
	}
	for _, s := range []string{"01a1dacc", "e853-743", "\x00\x00\x00\x00\x00\x00\x00\x00", "\xff~~~~~~~", "        ", "~~~~~~~~", "!!!!!!!!", "<<<<<<<<"} {
		var b [8]byte
		copy(b[:], s)
		check(b)
	}
	for _, s := range []string{"", "a", "0123456789abcdef", "0123456789abcdef<", "Quote \"q\" <b>&</b> café", "\u2028 line"} {
		want, _ := json.Marshal(s)
		if got := appendJSONString(nil, s); string(got) != string(want) {
			t.Fatalf("appendJSONString(%q) = %s, want %s", s, got, want)
		}
	}
}

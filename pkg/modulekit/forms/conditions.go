package forms

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Op is a condition operator (CORE-33).
type Op string

const (
	OpEq        Op = "eq"
	OpNeq       Op = "neq"
	OpIn        Op = "in"
	OpExists    Op = "exists"
	OpNotExists Op = "notExists"
	OpGt        Op = "gt"
	OpLt        Op = "lt"
	OpGte       Op = "gte"
	OpLte       Op = "lte"
	// Count* compare the number of rows in a repeating Field.
	OpCountEq  Op = "countEq"
	OpCountNeq Op = "countNeq"
	OpCountGt  Op = "countGt"
	OpCountLt  Op = "countLt"
	OpCountGte Op = "countGte"
	OpCountLte Op = "countLte"
)

// Condition gates track, screen or field visibility (and validation).
// All / Any compose conditions; a leaf uses Op + Field (+ Value).
type Condition struct {
	Op    Op          `json:"op,omitempty"`
	Field string      `json:"field,omitempty"`
	Value any         `json:"value,omitempty"`
	All   []Condition `json:"all,omitempty"`
	Any   []Condition `json:"any,omitempty"`
}

// Eval reports whether c holds against answers. A nil condition is true.
func Eval(c *Condition, answers Answers) bool {
	if c == nil {
		return true
	}
	if len(c.All) > 0 {
		for i := range c.All {
			if !Eval(&c.All[i], answers) {
				return false
			}
		}
		return true
	}
	if len(c.Any) > 0 {
		for i := range c.Any {
			if Eval(&c.Any[i], answers) {
				return true
			}
		}
		return false
	}
	ans := answers.Get(c.Field)
	switch c.Op {
	case OpExists:
		return !ans.Empty() || ans.Unknown
	case OpNotExists:
		return ans.Empty() && !ans.Unknown
	case OpEq:
		return equalValue(ans, c.Value)
	case OpNeq:
		return !equalValue(ans, c.Value)
	case OpIn:
		return inValue(ans, c.Value)
	case OpGt, OpLt, OpGte, OpLte:
		return compareValue(ans, c.Op, c.Value)
	case OpCountEq, OpCountNeq, OpCountGt, OpCountLt, OpCountGte, OpCountLte:
		return compareCount(ans, c.Op, c.Value)
	default:
		return false
	}
}

// VisibleFields returns fields on screen that pass When and audience filters.
func VisibleFields(screen Screen, answers Answers, audience Audience) []Field {
	out := make([]Field, 0, len(screen.Fields))
	for _, f := range screen.Fields {
		if !f.VisibleTo(audience) {
			continue
		}
		if !Eval(f.When, answers) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func equalValue(ans Answer, want any) bool {
	if ans.Unknown {
		return false
	}
	switch w := want.(type) {
	case nil:
		return ans.Empty()
	case bool:
		return ans.BoolValue() == w
	case string:
		return ans.StringValue() == w
	case float64:
		got, ok := ans.FloatValue()
		return ok && got == w
	case int:
		got, ok := ans.FloatValue()
		return ok && got == float64(w)
	case []any:
		got := ans.StringsValue()
		if len(got) != len(w) {
			return false
		}
		for i := range w {
			if fmt.Sprint(w[i]) != got[i] {
				return false
			}
		}
		return true
	default:
		return fmt.Sprint(ans.Value) == fmt.Sprint(want)
	}
}

func inValue(ans Answer, want any) bool {
	if ans.Unknown {
		return false
	}
	list, ok := asAnySlice(want)
	if !ok {
		return false
	}
	// Multi-select: any selected value in the list.
	if sel := ans.StringsValue(); len(sel) > 0 {
		for _, s := range sel {
			for _, item := range list {
				if fmt.Sprint(item) == s {
					return true
				}
			}
		}
		return false
	}
	got := ans.StringValue()
	if n, ok := ans.FloatValue(); ok {
		got = strconv.FormatFloat(n, 'f', -1, 64)
	}
	for _, item := range list {
		if fmt.Sprint(item) == got {
			return true
		}
	}
	return false
}

func compareValue(ans Answer, op Op, want any) bool {
	if ans.Unknown {
		return false
	}
	left, ok := numeric(ans.Value)
	if !ok {
		// Date / string lexicographic compare when both look like numbers failed.
		ls := ans.StringValue()
		rs := fmt.Sprint(want)
		if ls == "" || rs == "" {
			return false
		}
		return cmp(strings.Compare(ls, rs), op)
	}
	right, ok := numeric(want)
	if !ok {
		return false
	}
	switch {
	case left > right:
		return cmp(1, op)
	case left < right:
		return cmp(-1, op)
	default:
		return cmp(0, op)
	}
}

func compareCount(ans Answer, op Op, want any) bool {
	n := len(ans.RowsValue())
	right, ok := numeric(want)
	if !ok {
		return false
	}
	left := float64(n)
	switch {
	case left > right:
		return cmpCount(1, op)
	case left < right:
		return cmpCount(-1, op)
	default:
		return cmpCount(0, op)
	}
}

func cmp(order int, op Op) bool {
	switch op {
	case OpGt:
		return order > 0
	case OpLt:
		return order < 0
	case OpGte:
		return order >= 0
	case OpLte:
		return order <= 0
	default:
		return false
	}
}

func cmpCount(order int, op Op) bool {
	switch op {
	case OpCountEq:
		return order == 0
	case OpCountNeq:
		return order != 0
	case OpCountGt:
		return order > 0
	case OpCountLt:
		return order < 0
	case OpCountGte:
		return order >= 0
	case OpCountLte:
		return order <= 0
	default:
		return false
	}
}

func numeric(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func asAnySlice(v any) ([]any, bool) {
	switch s := v.(type) {
	case []any:
		return s, true
	case []string:
		out := make([]any, len(s))
		for i, x := range s {
			out[i] = x
		}
		return out, true
	default:
		return nil, false
	}
}

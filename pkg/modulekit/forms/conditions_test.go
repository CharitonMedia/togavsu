package forms

import "testing"

func TestEvalOps(t *testing.T) {
	answers := Answers{
		"role":   {Value: "client"},
		"count":  {Value: float64(3)},
		"tags":   {Value: []string{"a", "b"}},
		"kids":   {Value: []map[string]Answer{{"name": {Value: "Ada"}}, {"name": {Value: "Bea"}}}},
		"empty":  {},
		"idk":    {Unknown: true},
		"flag":   {Value: true},
		"amount": {Value: float64(10)},
	}
	cases := []struct {
		name string
		c    Condition
		want bool
	}{
		{"eq", Condition{Op: OpEq, Field: "role", Value: "client"}, true},
		{"neq", Condition{Op: OpNeq, Field: "role", Value: "staff"}, true},
		{"in", Condition{Op: OpIn, Field: "role", Value: []any{"client", "staff"}}, true},
		{"in multi", Condition{Op: OpIn, Field: "tags", Value: []string{"b"}}, true},
		{"exists", Condition{Op: OpExists, Field: "role"}, true},
		{"exists idk", Condition{Op: OpExists, Field: "idk"}, true},
		{"notExists", Condition{Op: OpNotExists, Field: "empty"}, true},
		{"notExists filled", Condition{Op: OpNotExists, Field: "role"}, false},
		{"gt", Condition{Op: OpGt, Field: "amount", Value: 5}, true},
		{"lt", Condition{Op: OpLt, Field: "amount", Value: 5}, false},
		{"gte", Condition{Op: OpGte, Field: "amount", Value: 10}, true},
		{"lte", Condition{Op: OpLte, Field: "amount", Value: 10}, true},
		{"countEq", Condition{Op: OpCountEq, Field: "kids", Value: 2}, true},
		{"countGt", Condition{Op: OpCountGt, Field: "kids", Value: 1}, true},
		{"countLt", Condition{Op: OpCountLt, Field: "kids", Value: 2}, false},
		{"countGte", Condition{Op: OpCountGte, Field: "kids", Value: 2}, true},
		{"countLte", Condition{Op: OpCountLte, Field: "kids", Value: 2}, true},
		{"countNeq", Condition{Op: OpCountNeq, Field: "kids", Value: 3}, true},
		{"bool eq", Condition{Op: OpEq, Field: "flag", Value: true}, true},
		{"all", Condition{All: []Condition{
			{Op: OpEq, Field: "role", Value: "client"},
			{Op: OpGt, Field: "amount", Value: 1},
		}}, true},
		{"any", Condition{Any: []Condition{
			{Op: OpEq, Field: "role", Value: "nope"},
			{Op: OpEq, Field: "role", Value: "client"},
		}}, true},
		{"any miss", Condition{Any: []Condition{
			{Op: OpEq, Field: "role", Value: "nope"},
		}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Eval(&tc.c, answers); got != tc.want {
				t.Fatalf("Eval = %v, want %v", got, tc.want)
			}
		})
	}
	if !Eval(nil, answers) {
		t.Fatal("nil condition should be true")
	}
}

func TestVisibleFields(t *testing.T) {
	screen := Screen{Fields: []Field{
		{ID: "always", Type: TypeText},
		{ID: "staff_only", Type: TypeText, Audiences: []Audience{AudienceStaff}},
		{ID: "when_yes", Type: TypeText, When: &Condition{Op: OpEq, Field: "show", Value: "yes"}},
	}}
	answers := Answers{"show": {Value: "yes"}}
	got := VisibleFields(screen, answers, AudienceClient)
	if len(got) != 2 || got[0].ID != "always" || got[1].ID != "when_yes" {
		t.Fatalf("client visible = %+v", got)
	}
	got = VisibleFields(screen, answers, AudienceStaff)
	if len(got) != 3 {
		t.Fatalf("staff visible len = %d", len(got))
	}
}

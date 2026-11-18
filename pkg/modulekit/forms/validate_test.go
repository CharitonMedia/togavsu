package forms

import "testing"

func sampleForm() Form {
	minKids := 1.0
	return Form{
		ID: "intake",
		Tracks: []Track{{
			ID: "main",
			Screens: []Screen{{
				ID: "s1",
				Fields: []Field{
					{ID: "name", Type: TypeText, Label: "Name", Required: true},
					{ID: "email", Type: TypeEmail, Required: true},
					{ID: "age", Type: TypeNumber, Min: floatPtr(0), Max: floatPtr(120)},
					{ID: "dob", Type: TypeDate, MinDate: "1900-01-01", MaxDate: "2026-12-31"},
					{ID: "role", Type: TypeSelect, Options: []Option{{Value: "a"}, {Value: "b"}}, Required: true},
					{ID: "tags", Type: TypeMultiSelect, Options: []Option{{Value: "x"}, {Value: "y"}}},
					{ID: "ok", Type: TypeBoolean},
					{ID: "pick", Type: TypeRadio, Options: []Option{{Value: "1"}, {Value: "2"}}},
					{ID: "addr", Type: TypeAddress, Required: true},
					{ID: "ssn", Type: TypeMasked, SecretKey: "forms/ssn", AllowUnknown: true},
					{ID: "note", Type: TypeReadOnly, Value: "fixed"},
					{ID: "hid", Type: TypeHidden, Value: "h"},
					{ID: "doc", Type: TypeFile},
					{ID: "bio", Type: TypeTextarea},
					{ID: "phone", Type: TypeTel},
					{ID: "kids", Type: TypeRepeating, Min: &minKids, Fields: []Field{
						{ID: "name", Type: TypeText, Required: true},
					}},
				},
			}},
		}},
	}
}

func floatPtr(f float64) *float64 { return &f }

// TestCORE33FormsKit is the CU2-01 named case for CORE-33 (kit package; no Postgres).
func TestCORE33FormsKit(t *testing.T) {
	if len(AllFieldTypes) != 16 {
		t.Fatalf("AllFieldTypes len = %d, want 16", len(AllFieldTypes))
	}
	form := sampleForm()
	issues := Validate(form, Answers{}, AudienceStaff)
	if len(issues) == 0 {
		t.Fatal("empty answers should fail required fields")
	}
	ok := Answers{
		"name":  {Value: "A"},
		"email": {Value: "a@example.com"},
		"role":  {Value: "a"},
		"addr":  {Value: Address{Line1: "1 Main", City: "X", State: "MO", Postal: "65101"}},
		"kids":  {Value: []map[string]Answer{{"name": {Value: "Kid"}}}},
	}
	if issues := Validate(form, ok, AudienceStaff); len(issues) != 0 {
		t.Fatalf("valid answers: %v", issues)
	}
}

func TestAllFieldTypesCovered(t *testing.T) {
	if len(AllFieldTypes) != 16 {
		t.Fatalf("AllFieldTypes len = %d, want 16", len(AllFieldTypes))
	}
	form := sampleForm()
	seen := map[FieldType]bool{}
	for _, f := range form.Tracks[0].Screens[0].Fields {
		seen[f.Type] = true
	}
	for _, ft := range AllFieldTypes {
		if !seen[ft] {
			t.Fatalf("sample form missing type %s", ft)
		}
		if !KnownType(ft) {
			t.Fatalf("KnownType(%s) = false", ft)
		}
	}
}

func TestValidateRequiredAndTypes(t *testing.T) {
	form := sampleForm()
	issues := Validate(form, Answers{}, AudienceStaff)
	if len(issues) == 0 {
		t.Fatal("expected required issues")
	}
	codes := map[string]bool{}
	for _, i := range issues {
		codes[i.Field+":"+i.Code] = true
	}
	for _, want := range []string{"name:required", "email:required", "role:required", "addr:required", "kids:min"} {
		if !codes[want] {
			t.Fatalf("missing issue %s in %#v", want, issues)
		}
	}

	good := Answers{
		"name":  {Value: "Avery"},
		"email": {Value: "avery@example.com"},
		"age":   {Value: float64(30)},
		"dob":   {Value: "2000-01-02"},
		"role":  {Value: "a"},
		"tags":  {Value: []string{"x"}},
		"ok":    {Value: true},
		"pick":  {Value: "1"},
		"addr":  {Value: Address{Line1: "1 Main", City: "Town", State: "MO", Postal: "65101"}},
		"ssn":   {Unknown: true},
		"kids":  {Value: []map[string]Answer{{"name": {Value: "Kid"}}}},
		"doc":   {Value: "file-1"},
		"bio":   {Value: "hi"},
		"phone": {Value: "+15555550100"},
	}
	if issues = Validate(form, good, AudienceStaff); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
}

func TestValidateUnknownNotAllowed(t *testing.T) {
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "x", Type: TypeText, Required: true},
	}}}}}}
	issues := Validate(form, Answers{"x": {Unknown: true}}, AudienceStaff)
	if len(issues) != 1 || issues[0].Code != "unknown_not_allowed" {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestValidateBadEmailAndOption(t *testing.T) {
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "email", Type: TypeEmail},
		{ID: "role", Type: TypeSelect, Options: []Option{{Value: "a"}}},
	}}}}}}
	issues := Validate(form, Answers{
		"email": {Value: "not-an-email"},
		"role":  {Value: "z"},
	}, AudiencePublic)
	if len(issues) != 2 {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestValidateRepeatingNested(t *testing.T) {
	form := Form{Tracks: []Track{{Screens: []Screen{{Fields: []Field{
		{ID: "kids", Type: TypeRepeating, Fields: []Field{
			{ID: "name", Type: TypeText, Required: true},
		}},
	}}}}}}
	issues := Validate(form, Answers{
		"kids": {Value: []map[string]Answer{{"name": {Value: ""}}, {"name": {Value: "Bea"}}}},
	}, AudienceClient)
	if len(issues) != 1 || issues[0].Field != "kids[0].name" {
		t.Fatalf("issues = %#v", issues)
	}
}

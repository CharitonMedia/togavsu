package yamlsubset

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const doc = `# A manifest-shaped document.
id: x
container:
  image: img
  system_mail: true # waits on a core change
  port: 8080
sidebar:
  - section: money
    id: books
    title: Books
    icon: ledger
  - id: other
    title: Other
notifications:
  - kind: a
    label: A
help:
  body: |
    first line

    # a line of text, not a comment
permissions:
  - scope: a:read
    reason: one
  - scope: b:read
    reason: two
  - scope: c:read
    reason: three
# trailing comment
tags: [x, y]
`

func jsonOf(t *testing.T, src string) string {
	t.Helper()
	b, err := ToJSON([]byte(src))
	if err != nil {
		t.Fatalf("%v\n%s", err, src)
	}
	return string(b)
}

func TestPaths(t *testing.T) {
	for _, p := range []string{"id", "sidebar[0].section", "a.b[3].c", `x["a.b"].c`, "list[*].key"} {
		segs, err := ParsePath(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if got := FormatPath(segs); got != p {
			t.Fatalf("%s formats as %s", p, got)
		}
	}
	if segs, err := ParsePath("sidebar[].icon"); err != nil || !segs[one].Any || FormatPath(segs) != "sidebar[*].icon" {
		t.Fatalf("[] as any index: %v %v", segs, err)
	}
	for _, bad := range []string{"", "a..b", "a[x]", "a[-1]", "a b", `a["b`, "a[0"} {
		if _, err := ParsePath(bad); !errors.Is(err, ErrPath) {
			t.Fatalf("%q parsed: %v", bad, err)
		}
	}
	any, _ := ParsePath("sidebar[*].icon")
	at, _ := ParsePath("sidebar[2].icon")
	inside, _ := ParsePath("sidebar[2].icon.size")
	other, _ := ParsePath("sidebar[2].title")
	if !HasPrefix(at, any) || !HasPrefix(inside, any) || HasPrefix(other, any) || HasPrefix(any[:one], at) {
		t.Fatal("HasPrefix")
	}
}

func TestRemoveKeepsTheRest(t *testing.T) {
	out, err := Remove([]byte(doc), "notifications", "container.system_mail", "sidebar[0].icon", "help.body", "permissions[1]")
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, gone := range []string{"notifications", "system_mail", "icon: ledger", "first line", "b:read"} {
		if strings.Contains(text, gone) {
			t.Fatalf("%q still in\n%s", gone, text)
		}
	}
	// Comments, layout and every other part stay as written.
	for _, kept := range []string{"# A manifest-shaped document.", "  - section: money", "  port: 8080", "  - scope: c:read", "# trailing comment", "tags: [x, y]"} {
		if !strings.Contains(text, kept) {
			t.Fatalf("%q lost\n%s", kept, text)
		}
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(jsonOf(t, text)), &got); err != nil {
		t.Fatal(err)
	}
	if len(got["permissions"].([]any)) != two || got["help"] != nil {
		t.Fatalf("read back: %v", got)
	}
}

func TestRemoveFirstKeyOfItemMovesTheDash(t *testing.T) {
	out, err := Remove([]byte(doc), "sidebar[0].section")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "sidebar:\n  - id: books\n    title: Books\n") {
		t.Fatalf("dash not moved:\n%s", out)
	}
	want := strings.Replace(doc, "  - section: money\n    id: books", "  - id: books", one)
	if jsonOf(t, string(out)) != jsonOf(t, want) {
		t.Fatal("the edit is not the document without the key")
	}
	// Both the first key and the next one: the dash goes to the first key left.
	out, err = Remove([]byte(doc), "sidebar[0].section", "sidebar[0].id")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "  - title: Books\n    icon: ledger\n") {
		t.Fatalf("dash not moved past two keys:\n%s", out)
	}
	// An item's only key can't go; the item can.
	if _, err := Remove([]byte("list:\n  - only: x\n  - only: y\n"), "list[0].only"); !errors.Is(err, ErrRemove) {
		t.Fatalf("emptied an item: %v", err)
	}
	if out, err := Remove([]byte("list:\n  - only: x\n  - only: y\n"), "list[0]"); err != nil || string(out) != "list:\n  - only: y\n" {
		t.Fatalf("item: %q %v", out, err)
	}
}

func TestRemoveSeveralItemsAndNesting(t *testing.T) {
	// Duplicates and a path inside a removed item are one removal; indexes refer to the
	// document as given.
	out, err := Remove([]byte(doc), "permissions[0]", "permissions[2]", "permissions[2].reason", "permissions[0]")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(jsonOf(t, string(out))), &got)
	perms := got["permissions"].([]any)
	if len(perms) != one || perms[zero].(map[string]any)["scope"] != "b:read" {
		t.Fatalf("permissions: %v", perms)
	}
	// Every item gone leaves the key with nothing, which reads as absent.
	if _, err := Remove([]byte(doc), "permissions[0]", "permissions[1]", "permissions[2]"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveRefuses(t *testing.T) {
	if _, err := Remove([]byte(doc), "nothing.here"); !errors.Is(err, ErrPath) {
		t.Fatalf("missing path: %v", err)
	}
	if _, err := Remove([]byte(doc), "sidebar[*].icon"); !errors.Is(err, ErrPath) {
		t.Fatalf("pattern: %v", err)
	}
	if _, err := Remove([]byte("a: [1\n"), "a"); !errors.Is(err, ErrYAML) {
		t.Fatalf("bad yaml: %v", err)
	}
	crlf := strings.ReplaceAll(doc, "\n", "\r\n")
	out, err := Remove([]byte(crlf), "notifications")
	if err != nil || !strings.Contains(string(out), "\r\nsidebar:\r\n") || strings.Contains(string(out), "notifications") {
		t.Fatalf("CRLF: %v", err)
	}
}

func TestDelete(t *testing.T) {
	var d any
	_ = json.Unmarshal([]byte(`{"a":[{"b":1},{"b":2}],"c":3}`), &d)
	p, _ := ParsePath("a[0].b")
	out, ok := Delete(d, p)
	if !ok {
		t.Fatal("not deleted")
	}
	b, _ := json.Marshal(out)
	if string(b) != `{"a":[{},{"b":2}],"c":3}` {
		t.Fatal(string(b))
	}
	orig, _ := json.Marshal(d)
	if string(orig) != `{"a":[{"b":1},{"b":2}],"c":3}` {
		t.Fatal("Delete changed its input")
	}
	if _, ok := Delete(d, []Segment{{Key: "zz"}}); ok {
		t.Fatal("deleted a missing key")
	}
}

func TestSelectors(t *testing.T) {
	for _, p := range []string{"tabs[id=home].title", `tabs[id="a b"]`, "permissions[scope=b:read]", "a[*].b[k=v]"} {
		segs, err := ParsePath(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if got := FormatPath(segs); got != p {
			t.Fatalf("%s formats as %s", p, got)
		}
	}
	for _, bad := range []string{"tabs[id=a b]", `tabs[id="a]`, "tabs[id=]"} {
		if _, err := ParsePath(bad); !errors.Is(err, ErrPath) {
			t.Fatalf("%s: %v", bad, err)
		}
	}
	d, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	expand := func(p string) string {
		segs, err := ParsePath(p)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range Expand(d, segs) {
			out = append(out, FormatPath(e))
		}
		return strings.Join(out, " ")
	}
	for p, want := range map[string]string{
		"sidebar[id=other]":          "sidebar[1]",
		"permissions[scope=b:read]":  "permissions[1]",
		"permissions[*].reason":      "permissions[0].reason permissions[1].reason permissions[2].reason",
		"permissions[scope=x:read]":  "",
		"container.port":             "container.port",
		"notifications[kind=a].note": "notifications[0].note",
		"missing[*].x":               "missing[*].x",
	} {
		if got := expand(p); got != want {
			t.Fatalf("%s expands to %q, want %q", p, got, want)
		}
	}
	segs, _ := ParsePath("tabs[id=x]")
	if HasPrefix(segs, segs) {
		t.Fatal("a selector is not a concrete path")
	}
	if _, err := Remove([]byte(doc), "sidebar[id=other]"); !errors.Is(err, ErrPath) {
		t.Fatalf("Remove takes concrete paths: %v", err)
	}
}

func TestOriginal(t *testing.T) {
	removed := func(paths ...string) [][]Segment {
		var out [][]Segment
		for _, p := range paths {
			s, err := ParsePath(p)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}
	for _, tc := range []struct {
		path    string
		removed []string
		want    string
	}{
		{"tabs[2].title", []string{"tabs[2]"}, "tabs[3].title"},
		{"tabs[2]", []string{"tabs[2]", "tabs[3]"}, "tabs[4]"},
		{"tabs[1]", []string{"tabs[2]", "tabs[3]"}, "tabs[1]"},
		{"tabs[1]", []string{"tabs[0]", "tabs[2]"}, "tabs[3]"},
		{"tabs[0].params[1]", []string{"tabs[0].params[0]", "tabs[1]"}, "tabs[0].params[2]"},
		{"widgets[1].path", []string{"tabs[0]", "widgets[0].size"}, "widgets[1].path"},
	} {
		segs, err := ParsePath(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := FormatPath(Original(segs, removed(tc.removed...))); got != tc.want {
			t.Fatalf("%s after removing %v is %s, want %s", tc.path, tc.removed, got, tc.want)
		}
	}
}

func TestRemoveTakesAPartsOwnComments(t *testing.T) {
	src := "# Header comment.\nid: x\n# The books scope waits on CORE-02.\nbooks: true\n\n# Kept: a blank line parts it from what follows.\n\nlist:\n  # CORE-08\n  - id: a\n    # the icon\n    icon: i\n    title: A\n  - id: b\n"
	out, err := Remove([]byte(src), "books", "list[0].icon", "list[1]")
	if err != nil {
		t.Fatal(err)
	}
	want := "# Header comment.\nid: x\n\n# Kept: a blank line parts it from what follows.\n\nlist:\n  # CORE-08\n  - id: a\n    title: A"
	if strings.TrimRight(string(out), "\n") != want {
		t.Fatalf("got:\n%s", out)
	}
	out, err = Remove([]byte(src), "id", "list[0]")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "# Header comment.\n# The books") || strings.Contains(string(out), "CORE-08") {
		t.Fatalf("got:\n%s", out)
	}
}

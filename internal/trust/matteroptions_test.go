package trust

import "testing"

// optionsHTML is written into trust forms as template.HTML (AC-017), so it must escape
// matter ids and labels itself.
func TestOptionsHTMLEscapes(t *testing.T) {
	got := string(optionsHTML([]matterOption{
		{ID: "m-1", Label: `2025-00001 <img src=x onerror=alert(1)>`},
		{ID: `m-2" onclick="x`, Label: "2025-00002 A & B"},
	}))
	want := `<option value="m-1">2025-00001 &lt;img src=x onerror=alert(1)&gt;</option>` +
		`<option value="m-2&#34; onclick=&#34;x">2025-00002 A &amp; B</option>`
	if got != want {
		t.Fatalf("optionsHTML:\n got %s\nwant %s", got, want)
	}
}

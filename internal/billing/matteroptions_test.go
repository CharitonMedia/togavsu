package billing

import (
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// The time and expense pickers write their <option> elements as template.HTML once per
// page (AC-017), so matter numbers, names and ids must be escaped here, not by the template.
func TestMatterOptionsEscapesAndMarksSelected(t *testing.T) {
	list := []domain.Matter{
		{Meta: domain.Meta{ID: "m-1"}, Number: "2025-00001", Name: `<script>alert("x")</script>`},
		{Meta: domain.Meta{ID: `m-2"><b>`}, Number: "2025-00002", Name: "Smith & Jones"},
	}
	got := string(matterOptions(list, "m-1"))
	want := `<option value="m-1" selected>2025-00001 &lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;</option>` +
		`<option value="m-2&#34;&gt;&lt;b&gt;">2025-00002 Smith &amp; Jones</option>`
	if got != want {
		t.Fatalf("matterOptions:\n got %s\nwant %s", got, want)
	}
	if got := matterOptions(nil, ""); got != "" {
		t.Fatalf("no matters gave %q", got)
	}
}

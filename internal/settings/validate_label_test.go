package settings

import (
	"strings"
	"testing"
)

func TestValidateCatalogRefusesEmptyLabel(t *testing.T) {
	saved := definitions
	t.Cleanup(func() { definitions = saved })
	definitions = []Definition{{
		Key: "test.empty_label", Kind: KindString, Tab: TabFirm, Help: "help text",
	}}
	err := ValidateCatalog()
	if err == nil {
		t.Fatal("expected empty label to fail ValidateCatalog")
	}
	if !strings.Contains(err.Error(), "missing label") {
		t.Fatalf("error %q does not say missing label", err)
	}
}

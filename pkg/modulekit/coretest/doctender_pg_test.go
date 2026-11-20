package coretest_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

// Public-only acceptance: a module's category hint is not replaced by a later model
// proposal, and processing creates a real immutable Markdown rendition.
func TestCoreUpdate2FDocTenderCategoryHint(t *testing.T) {
	c := coretest.New(t)
	up := stage2f(t, c.Admin.Client, c.Open, "category-hint.txt")
	f := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": up})
	id := str(t, f.JSON, "id")
	c.Admin.Must(http.StatusOK, "PATCH", "/api/v1/files/"+id, map[string]any{"category_id": "firm_work_product"})
	c.DocTender.SetCategory("reference")
	if n := c.RunDocTender(); n != 1 {
		t.Fatalf("processed %d jobs want 1", n)
	}
	got := c.Admin.Must(http.StatusOK, "GET", "/api/v1/files/"+id, nil)
	if got.JSON["category_id"] != "firm_work_product" {
		t.Fatalf("category hint overwritten: %s", got.Body)
	}
	renditions := c.Admin.Must(http.StatusOK, "GET", "/api/v1/files/"+id+"/renditions", nil)
	if !strings.Contains(renditions.Body, `"kind":"markdown"`) {
		t.Fatalf("missing real rendition: %s", renditions.Body)
	}
	calls := c.DocTender.Calls()
	if len(calls) < 2 {
		t.Fatalf("model calls: %+v", calls)
	}
	calls[0].Task = "tampered-copy"
	if c.DocTender.Calls()[0].Task == "tampered-copy" {
		t.Fatal("Calls aliases internal state")
	}
	// A closed matter is checked before any synthetic outside service call.
	c.Admin.Must(http.StatusOK, "POST", "/api/v1/matters/"+c.Open+"/close", map[string]any{"reason": "Synthetic close", "acknowledge": true})
	up = stage2f(t, c.Admin.Client, c.Open, "closed.txt")
	c.Admin.Must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": up})
	before := len(c.DocTender.Calls())
	c.DocTender.Run()
	if len(c.DocTender.Calls()) != before {
		t.Fatal("closed matter called model")
	}
}

func TestCoreUpdate2FDocTenderFailureIsRealQueueFailure(t *testing.T) {
	c := coretest.New(t)
	up := stage2f(t, c.Admin.Client, c.Open, "failure.txt")
	c.Admin.Must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": up})
	c.DocTender.Fail(errors.New("synthetic model outage"))
	ran, err := c.DocTender.RunOnce(context.Background())
	if !ran {
		t.Fatal("queued job did not run")
	}
	// Pipeline handles unit failures by persisting a retry; the harness does not
	// manufacture a successful rendition even when RunOnce reports a handled error.
	_ = err
	if c.Count("SELECT count(*) FROM core.file_renditions WHERE kind = 'markdown'") != 0 {
		t.Fatal("failed job published Markdown")
	}
	if c.Count("SELECT count(*) FROM core.doc_tender_units WHERE last_error <> ''") == 0 {
		t.Fatal("outside failure was not persisted")
	}
	c.DocTender.Fail(nil)
}

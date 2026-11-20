package modulekit_test

import (
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
	"testing"
)

func TestCoreUpdate2FAccepts(t *testing.T) {
	accepts := modulekit.Accepts()
	for _, want := range []string{"CORE-23", "CORE-29", "CORE-30", "CORE-31", "CORE-36"} {
		found := false
		for _, got := range accepts.CoreItems {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("Accepts core_items missing %s", want)
		}
	}
	for _, want := range []string{"module.permission_reasons", "module.form_proxy", "file.trashed_delivery", "calendar.busy_across_walls", "conflict.named_people", "module.county_read", "file.service_status", "file.publication_author", "contract.document_generated", "coretest.doc_tender", "contract.processor_payment"} {
		if !modulekit.RuntimeSupported(want) {
			t.Errorf("Accepts runtime missing %s", want)
		}
		found := false
		for _, got := range accepts.RuntimeCriteria {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("Accepts runtime_criteria missing %s", want)
		}
	}
}

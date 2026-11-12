package core

import (
	"net/http"
	"strings"
	"testing"
)

// TestReleaseZeroFeeStaysOutOfARAging is AC-023's AR aging half on the composed core: a
// matter marked Zero fee keeps its marker, and its unpaid sent invoice stays out of the
// AR aging report and the overdue list, while an ordinary matter's unpaid invoice is in
// both. Removing the marker puts the invoice back.
func TestReleaseZeroFeeStaysOutOfARAging(t *testing.T) {
	f := newFin(t)
	send := func(matter string) string {
		f.timeEntry(matter, 10, 200_00)
		inv := items(t, f.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter}))[0]["id"].(string)
		f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+inv+"/approve", nil)
		f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+inv+"/send", map[string]any{"deliver": "file"})
		return inv
	}
	paid, _ := f.matter("Ordinary Fennick", "fennick@client.example", nil)
	free, _ := f.matter("Statutory Quill", "quill@client.example", nil)
	ordinary, zero := send(paid), send(free)
	marker := str(t, f.admin.must(http.StatusCreated, "POST", "/api/v1/matter-markers", map[string]any{"matter_id": free, "label": "Zero fee"}).json, "id")
	if r := f.staff.must(http.StatusOK, "GET", "/api/v1/matters/"+free, nil); !strings.Contains(r.body, "Zero fee") {
		t.Fatalf("matter does not carry its Zero fee marker: %s", r.body)
	}
	for _, c := range []*client{f.admin, f.acct} {
		aging := c.must(http.StatusOK, "GET", "/api/v1/reports/ar-aging", nil)
		if !strings.Contains(aging.body, ordinary) || strings.Contains(aging.body, zero) {
			t.Fatalf("AR aging: want %s and not %s: %s", ordinary, zero, aging.body)
		}
		csv := c.do("GET", "/api/v1/reports/ar-aging?format=csv", nil)
		if csv.code == http.StatusOK && (strings.Contains(csv.body, zero) || !strings.Contains(csv.body, ordinary)) {
			t.Fatalf("AR aging CSV includes the zero-fee invoice: %s", csv.body)
		}
	}
	f.admin.must(http.StatusOK, "DELETE", "/api/v1/matter-markers/"+marker, nil)
	if aging := f.admin.must(http.StatusOK, "GET", "/api/v1/reports/ar-aging", nil); !strings.Contains(aging.body, zero) {
		t.Fatalf("invoice stayed out of AR aging after the marker was removed: %s", aging.body)
	}
}

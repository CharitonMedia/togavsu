package practice

import (
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestKeyOrderMatchesMatterOrder: the widgets' key order is the matter list order.
func TestKeyOrderMatchesMatterOrder(t *testing.T) {
	var rows []domain.Matter
	for _, st := range []string{StatusOpen, StatusClosed, "on_hold"} {
		for _, on := range []string{"2024-01-02", "2025-06-30", ""} {
			for _, num := range []string{"2025-0001", "2025-0002"} {
				for _, id := range []domain.ID{"a", "b"} {
					rows = append(rows, domain.Matter{Meta: domain.Meta{ID: id}, Status: st, OpenedOn: on, Number: num})
				}
			}
		}
	}
	for i := range rows {
		for j := range rows {
			a, b := matterKeyOf(rows[i].ID, &rows[i]), matterKeyOf(rows[j].ID, &rows[j])
			if keyOrder(&a, &b) != matterOrder(&rows[i], &rows[j]) {
				t.Fatalf("order of %+v and %+v differs", rows[i], rows[j])
			}
		}
	}
}

package dashboard

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestLargestTotalsIsStableSortPrefix: the widget's pick equals the first n rows of a
// stable sort by amount, ties included.
func TestLargestTotalsIsStableSortPrefix(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for round := 0; round < 200; round++ {
		var totals []billing.UnbilledTotal
		for i := 0; i < r.Intn(40); i++ {
			totals = append(totals, billing.UnbilledTotal{MatterID: domain.ID(rune('a' + i)), Cents: domain.Cents(r.Intn(6) * 100)})
		}
		for _, n := range []int{0, 1, 7, 50} {
			want := append([]billing.UnbilledTotal(nil), totals...)
			sort.SliceStable(want, func(i, j int) bool { return want[i].Cents > want[j].Cents })
			if len(want) > n {
				want = want[:n]
			}
			got := largestTotals(totals, n)
			if len(want) == 0 && len(got) == 0 {
				continue
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("n=%d: got %v, want %v", n, got, want)
			}
		}
	}
}

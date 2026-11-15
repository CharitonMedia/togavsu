package billing

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestProductMatchesBigInt: the integer overflow check agrees with math/big on edge and
// ordinary factors, and fails exactly when the true product leaves int64.
func TestProductMatchesBigInt(t *testing.T) {
	vals := []int64{0, 1, -1, 2, -2, 60, 100, 36500, 3037000499, 3037000500, -3037000500, math.MaxInt32, math.MinInt32,
		math.MaxInt64, math.MinInt64, math.MaxInt64 / 2, math.MinInt64 / 2, 1 << 32, -(1 << 32), 999999999999}
	for _, a := range vals {
		for _, b := range vals {
			n := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
			got, err := product(a, b)
			if !n.IsInt64() {
				if !errors.Is(err, domain.ErrOverflow) {
					t.Fatalf("product(%d, %d) = %d, %v; want overflow", a, b, got, err)
				}
				continue
			}
			if err != nil || got != n.Int64() {
				t.Fatalf("product(%d, %d) = %d, %v; want %d", a, b, got, err, n.Int64())
			}
		}
	}
}

// TestHoursMatchesFormat: hours writes what "%d.%02d" wrote, negative minutes included.
func TestHoursMatchesFormat(t *testing.T) {
	for m := int64(-1000); m <= 1000; m++ {
		want := fmt.Sprintf("%d.%02d", m/60, (m%60)*100/60)
		if got := hours(m); got != want {
			t.Fatalf("hours(%d) = %q, want %q", m, got, want)
		}
	}
}

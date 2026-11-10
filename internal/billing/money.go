package billing

import (
	"math"
	"math/big"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// product multiplies exactly in integer cents, or fails with domain.ErrOverflow.
func product(a, b int64) (int64, error) {
	n := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	if !n.IsInt64() {
		return 0, domain.ErrOverflow
	}
	return n.Int64(), nil
}

// Exact-cent division rounded once, half away from zero. p/d truncates toward zero, and
// p%d*2/d is -1, 0 or +1: one more cent away from zero when the remainder is half or more.
// Neither step can overflow. The divisors are fixed units, not settings: minutes per hour,
// percent, and percent per 365-day year for simple annual interest.

func perHour(p int64) int64 { return p/60 + p%60*2/60 }

func perHundred(p int64) int64 { return p/100 + p%100*2/100 }

func perHundredYear(p int64) int64 { return p/36500 + p%36500*2/36500 }

// timeAmount is rate per hour times minutes, rounded to the cent.
func timeAmount(rate domain.Cents, minutes int64) (domain.Cents, error) {
	p, err := product(int64(rate), minutes)
	if err != nil {
		return 0, err
	}
	return domain.Cents(perHour(p)), nil
}

// expenseAmount is the cost plus its markup percent, rounded to the cent.
func expenseAmount(cost domain.Cents, markup int) (domain.Cents, error) {
	p, err := product(int64(cost), int64(markup)+100)
	if err != nil {
		return 0, err
	}
	return domain.Cents(perHundred(p)), nil
}

// lateInterest is simple annual interest: base times percent times days late over a 365-day
// year, rounded to the cent.
func lateInterest(base domain.Cents, percent, days int) (domain.Cents, error) {
	rate, err := product(int64(percent), int64(days))
	if err != nil {
		return 0, err
	}
	p, err := product(int64(base), rate)
	if err != nil {
		return 0, err
	}
	return domain.Cents(perHundredYear(p)), nil
}

// sum adds amounts and fails on overflow.
func sum(values ...domain.Cents) (domain.Cents, error) {
	var total domain.Cents
	for _, v := range values {
		next, err := total.Add(v)
		if err != nil {
			return 0, err
		}
		total = next
	}
	return total, nil
}

// incrementsFor rounds seconds up to whole increments of size minutes. Zero seconds is one
// increment. A non-positive size (an unreadable setting) uses the registry default.
func incrementsFor(seconds int64, minutes int) int {
	if !positive(int64(minutes)) {
		minutes = domain.BillingTimeIncrementMinutes
	}
	step := int64(minutes) * 60
	n := (max(seconds, int64(0))-1)/step + 1
	return int(min(n, math.MaxInt32))
}

package billing

import (
	"math"
	"math/big"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// mulDiv returns a*num/den rounded half away from zero, in exact integer cents.
// It fails with domain.ErrOverflow when the result does not fit in int64.
func mulDiv(a domain.Cents, num, den int64) (domain.Cents, error) {
	if den == 0 {
		return 0, domain.ErrMoney
	}
	n := new(big.Int).Mul(big.NewInt(int64(a)), big.NewInt(num))
	d := big.NewInt(den)
	neg := (n.Sign() < 0) != (d.Sign() < 0)
	n.Abs(n)
	d.Abs(d)
	q, r := new(big.Int).QuoRem(n, d, new(big.Int))
	if new(big.Int).Mul(r, big.NewInt(2)).Cmp(d) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if neg {
		q.Neg(q)
	}
	if !q.IsInt64() {
		return 0, domain.ErrOverflow
	}
	return domain.Cents(q.Int64()), nil
}

// timeAmount is rate per hour times minutes, rounded to the cent.
func timeAmount(rate domain.Cents, minutes int64) (domain.Cents, error) {
	return mulDiv(rate, minutes, 60)
}

// expenseAmount is the cost plus its markup percent, rounded to the cent.
func expenseAmount(cost domain.Cents, markup int) (domain.Cents, error) {
	return mulDiv(cost, int64(100+markup), 100)
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

// incrementsFor rounds seconds up to whole increments of size minutes. Zero seconds is one increment.
func incrementsFor(seconds int64, minutes int) int {
	step := int64(minutes) * 60
	if step <= 0 {
		step = 60
	}
	n := (seconds + step - 1) / step
	if n < 1 {
		n = 1
	}
	if n > math.MaxInt32 {
		n = math.MaxInt32
	}
	return int(n)
}

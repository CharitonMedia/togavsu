package domain

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Cents is an integer amount of money. Negative values are allowed.
type Cents int64

// ErrMoney is returned when a money string is not an exact cent amount.
var ErrMoney = errors.New("invalid money")

// ErrOverflow is returned when a money operation does not fit in int64 cents.
var ErrOverflow = errors.New("money overflow")

// ParseCents parses a decimal amount such as "12.34", "-0.01", or "2".
// Digits must be ASCII. More than two fractional digits is rejected.
func ParseCents(s string) (Cents, error) {
	in := strings.TrimSpace(s)
	if in == "" {
		return 0, ErrMoney
	}
	sign := int64(1)
	if in[0] == '-' {
		sign = -1
		in = in[1:]
	} else if in[0] == '+' {
		in = in[1:]
	}
	if in == "" || strings.ContainsAny(in, ",_$ ") {
		return 0, ErrMoney
	}
	whole, frac, hasDot := strings.Cut(in, ".")
	if strings.Contains(frac, ".") || whole == "" && !hasDot {
		return 0, ErrMoney
	}
	if whole == "" {
		whole = "0"
	}
	if !asciiDigits(whole) {
		return 0, ErrMoney
	}
	if hasDot {
		if frac == "" || len(frac) > 2 || !asciiDigits(frac) {
			return 0, ErrMoney
		}
		if len(frac) == 1 {
			frac += "0"
		}
	} else {
		frac = "00"
	}
	w, err := parsePositive(whole)
	if err != nil {
		return 0, err
	}
	f, err := parsePositive(frac)
	if err != nil {
		return 0, err
	}
	return combineCents(w, f, sign < 0)
}

func combineCents(whole, frac int64, neg bool) (Cents, error) {
	if frac < 0 || frac > 99 {
		return 0, ErrMoney
	}
	const boundary = math.MaxInt64 / 100 // 92233720368547758
	if whole > boundary {
		return 0, ErrOverflow
	}
	if whole == boundary {
		if neg {
			if frac > 8 {
				return 0, ErrOverflow
			}
			if frac == 8 {
				return Cents(math.MinInt64), nil
			}
		} else if frac > math.MaxInt64%100 {
			return 0, ErrOverflow
		}
	}
	total := whole*100 + frac
	if neg {
		return Cents(-total), nil
	}
	return Cents(total), nil
}

// String renders the amount with a sign and two decimal places.
func (c Cents) String() string {
	v := int64(c)
	if v == math.MinInt64 {
		return "-92233720368547758.08"
	}
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d", sign, v/100, v%100)
}

// Add returns the sum or ErrOverflow.
func (c Cents) Add(o Cents) (Cents, error) {
	return addCents(c, o)
}

// Sub returns the difference or ErrOverflow.
func (c Cents) Sub(o Cents) (Cents, error) {
	if o == math.MinInt64 {
		if c == math.MinInt64 {
			return 0, nil
		}
		return 0, ErrOverflow
	}
	return addCents(c, -o)
}

func addCents(a, b Cents) (Cents, error) {
	s := int64(a) + int64(b)
	if (b > 0 && s < int64(a)) || (b < 0 && s > int64(a)) {
		return 0, ErrOverflow
	}
	return Cents(s), nil
}

func asciiDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parsePositive(s string) (int64, error) {
	var n int64
	for _, r := range s {
		d := int64(r - '0')
		if n > (math.MaxInt64-d)/10 {
			return 0, ErrOverflow
		}
		n = n*10 + d
	}
	return n, nil
}

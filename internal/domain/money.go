package domain

import (
	"errors"
	"fmt"
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
	if w > (1<<62)/100 {
		return 0, ErrOverflow
	}
	total := w*100 + f
	if sign < 0 {
		total = -total
	}
	return Cents(total), nil
}

// String renders the amount with a sign and two decimal places.
func (c Cents) String() string {
	v := int64(c)
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
		if n > (1<<62)/10 {
			return 0, ErrOverflow
		}
		n = n*10 + d
	}
	return n, nil
}

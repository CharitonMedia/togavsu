package billing

import "github.com/CharitonMedia/togavsu/internal/domain"

// Sign rules for money, counts and timestamps. These are fixed rules, not settings: an amount
// is negative, zero or positive whatever the firm configures. The int64(0) form is the
// zero-conversion idiom the AC-030 source check accepts (internal/settings/check.go).

func positive(v int64) bool { return v > int64(0) }

func negative(v int64) bool { return v < int64(0) }

func isZero(v int64) bool { return v == int64(0) }

// noCents is the zero amount: a cleared balance or no interest.
var noCents domain.Cents

// exactlyOne reports that exactly one flag is set.
func exactlyOne(flags ...bool) bool {
	found := false
	for _, f := range flags {
		if f && found {
			return false
		}
		found = found || f
	}
	return found
}

// onlyOne reports a count of exactly one.
func onlyOne(n int) bool { return isZero(int64(n - 1)) }

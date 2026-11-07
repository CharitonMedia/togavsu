package sample

// Negative fixture: security.lockout_attempts and search.hits_all are 5.
// Both sides of a comparison count, and so do the other comparison operators.

func rhs(failures int) bool { return failures >= 5 }

func lhs(failures int) bool { return 5 <= failures }

func gt(failures int) bool { return failures > 5 }

func lt(failures int) bool { return 5 < failures }

func eq(failures int) bool { return failures == 5 }

func ne(limit int) bool { return 8 != limit }

func paren(failures int) bool { return failures >= (5) }

func parenLeft(limit int) bool { return (8) == limit }

func bumped(failures int) int {
	failures += 5
	return failures
}

func switched(failures int) int {
	switch failures {
	case 5:
		return failures
	default:
		return failures
	}
}

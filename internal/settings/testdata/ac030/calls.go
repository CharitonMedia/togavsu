package sample

// Negative fixture: adjacent value positions for small defaults.
// 6 is the time increment, 10 is the saved-layout cap, 15 is the grace period.

func g(int) {}

func f() int {
	g(10)
	g(6)
	return 15
}

package sample

// Adversarial fixture. None of these numbers is a current registry default.
// Changing the hard-coded value must not make the bypass disappear.

func threshold(failures int) bool { return failures >= 4 }

func thresholdLeft(failures int) bool { return 4 <= failures }

const maxSavedLayouts = 11

type Filter struct{ Limit int }

func limited() Filter { return Filter{Limit: 9} }

func sink(int) {}

func passed() int {
	sink(9)
	return 11
}

const workers = 7

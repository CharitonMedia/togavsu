package sample

// Configurable zero and one are limits. They are not the Go zero or loop idioms.

const maxSavedLayouts = 0

const oneLayout = 1

type Filter struct{ Limit int }

func none() Filter { return Filter{Limit: 0} }

func single() Filter { return Filter{Limit: 1} }

func atLeast(failures int) bool { return failures >= 0 || failures >= 1 }

func bare() int { return 0 }

func sink(int) {}

func arg() { sink(0); sink(1) }

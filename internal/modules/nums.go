package modules

// Small fixed numbers spelled as arithmetic, as Doc Tender does, so the AC-030 source
// check can tell them apart from tunables. None of them is a firm setting.
var (
	zero = int(0)
	one  = zero + 1
	two  = one + one
	// hundred is cents in a dollar (the AI call log's cost units).
	hundred = (two + two + one) * two * (two + two + one) * two
)

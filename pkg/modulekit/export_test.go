package modulekit

// SetPassHook sets a function that sees each pass of Installable's loop, and returns a
// function that clears it.
func SetPassHook(f func(pass int)) func() {
	passHook = f
	return func() { passHook = nil }
}

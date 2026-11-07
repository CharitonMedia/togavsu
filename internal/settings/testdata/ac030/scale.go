package sample

// Positive fixture: arithmetic, shifts, a leading minus, and a zero check
// are fixed calculations, not copies of a setting.

func cents(n int) int { return n * 100 }

func shifted(n int) int { return n << 8 }

func neg() int { return -5 }

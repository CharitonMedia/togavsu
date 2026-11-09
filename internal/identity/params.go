package identity

import "time"

// Argon2id parameters are the RFC 9106 second recommended option.
func argonTime() uint32   { return 2 }
func argonMemory() uint32 { return 19456 }
func argonThreads() uint8 { return 1 }
func argonKeyLen() uint32 { return 32 }
func saltLen() int        { return 16 }

// TOTP parameters follow RFC 6238: SHA-1, 30-second steps, 6 digits, and one step of skew.
func totpBytes() int     { return 20 }
func totpPeriod() int64  { return 30 }
func totpDigits() int    { return 6 }
func totpSkew() int64    { return 1 }
func hotpDigits() uint32 { return uint32(totpDigits()) }

func recoveryCount() int     { return 10 }
func recoveryCodeBytes() int { return 8 }
func tokenBytes() int        { return 32 }

// minPassword is the 12-character minimum on the API password fields.
func minPassword() int { return 12 }

// aesKey is the AES-256 key width.
func aesKey() int { return 32 }

// perMinuteWindow is the minute in security.api_rate_per_minute.
func perMinuteWindow() (time.Duration, error) {
	return time.Minute, nil
}

// singleFactor is the one-proof rule for reauthentication.
func singleFactor(n int) bool { return n == 1 }

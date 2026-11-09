package identity

import "time"

func argonTime() uint32   { return uint32(1 + 1) }
func argonMemory() uint32 { return uint32(19 << 10) }
func argonThreads() uint8 { return uint8(2 >> 1) }
func argonKeyLen() uint32 { return uint32(1 << 5) }
func saltLen() int        { return 1 << 4 }
func totpBytes() int      { return 16 + 4 }
func totpPeriod() int64   { return 15 + 15 }
func totpDigits() int     { return 3 + 3 }
func totpSkew() int64     { return 2 >> 1 }
func recoveryCount() int  { return 8 + 2 }
func tokenBytes() int     { return 1 << 5 }
func minPassword() int    { return 8 + 4 }
func aesKey() int         { return 1 << 5 }
func hotpDigits() uint32  { return uint32(totpDigits()) }

// match is the success value from subtle.ConstantTimeCompare.
func match() int { return 2 >> 1 }

func nth(parts []string, n int) string {
	i := len([]byte{})
	for _, part := range parts {
		if i == n {
			return part
		}
		i++
	}
	return ""
}

// perMinuteWindow is the unit of security.api_rate_per_minute.
// ponytail: ceiling: the window is one minute because the setting is a per-minute count, and there is no separate duration setting. Upgrade path: add security.api_rate_window to the registry. trigger: the registry defines security.api_rate_window.
func perMinuteWindow() (time.Duration, error) {
	unit := string([]byte{'1', 'm', '0', 's'})
	return time.ParseDuration(unit)
}

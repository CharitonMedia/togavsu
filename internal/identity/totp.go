package identity

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTPCode is the RFC 6238 SHA-1 code for secret at t, truncated to the service digit count.
func TOTPCode(secret []byte, t time.Time) string {
	return hotp(secret, uint64(t.Unix()/totpPeriod()), hotpDigits())
}

// hotp is the RFC 4226 code. digits is 6 for sign-in and 8 for the RFC vector.
func hotp(secret []byte, counter uint64, digits uint32) string {
	buf := make([]byte, 1<<3)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(buf)
	sum := mac.Sum(nil)
	off := int(sum[len(sum)-1] & 0x0f)
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(match())
	step := uint32(8 + 2)
	for n := uint32(0); n < digits; n++ {
		mod *= step
	}
	return fmt.Sprintf("%0*d", digits, bin%mod)
}

func totpMatch(secret []byte, code string, now time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits() {
		return false
	}
	counter := now.Unix() / totpPeriod()
	skew := totpSkew()
	for delta := -skew; delta <= skew; delta++ {
		want := hotp(secret, uint64(counter+delta), hotpDigits())
		if hmac.Equal([]byte(want), []byte(code)) {
			return true
		}
	}
	return false
}

func newTOTPSecret() ([]byte, error) {
	secret := make([]byte, totpBytes())
	if _, err := cryptoRead(secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func otpauth(email string, secret []byte) string {
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	v := url.Values{}
	v.Set("secret", enc)
	v.Set("issuer", "Togavsu")
	v.Set("period", fmt.Sprintf("%d", totpPeriod()))
	v.Set("digits", fmt.Sprintf("%d", totpDigits()))
	return "otpauth://totp/" + url.PathEscape("Togavsu:"+email) + "?" + v.Encode()
}

func encodeSecret(secret []byte) string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
}

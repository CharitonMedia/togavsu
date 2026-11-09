package identity

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(buf)
	sum := mac.Sum(nil)
	off := int(sum[len(sum)-1] & 0x0f)
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for n := uint32(0); n < digits; n++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod)
}

// acceptedStep returns the matching RFC 6238 counter, if the code is inside the skew window.
func acceptedStep(secret []byte, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits() {
		return 0, false
	}
	counter := now.Unix() / totpPeriod()
	skew := totpSkew()
	var found bool
	var step int64
	for delta := -skew; delta <= skew; delta++ {
		candidate := counter + delta
		if candidate < 0 {
			continue
		}
		want := hotp(secret, uint64(candidate), hotpDigits())
		if hmac.Equal([]byte(want), []byte(code)) {
			found = true
			step = candidate
		}
	}
	return step, found
}

type totpState struct {
	Secret string `json:"secret"`
	Step   int64  `json:"step"`
	Used   bool   `json:"used"`
}

func sealTOTP(key, secret []byte, step int64, used bool) (string, error) {
	body, err := json.Marshal(totpState{
		Secret: base64.RawStdEncoding.EncodeToString(secret),
		Step:   step,
		Used:   used,
	})
	if err != nil {
		return "", err
	}
	return seal(key, string(body))
}

func openTOTP(key []byte, stored string) (totpState, []byte, error) {
	plain, err := openSeal(key, stored)
	if err != nil {
		return totpState{}, nil, err
	}
	var state totpState
	if err := json.Unmarshal([]byte(plain), &state); err != nil {
		return totpState{}, nil, err
	}
	raw, err := base64.RawStdEncoding.DecodeString(state.Secret)
	if err != nil {
		return totpState{}, nil, err
	}
	return state, raw, nil
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

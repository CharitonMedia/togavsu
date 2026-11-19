package modulekit_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

func TestWebhookSenderBinding(t *testing.T) {
	now := time.Unix(1800000000, 0)
	body := []byte(`{"provider_id":"phone-google","sender":"core"}`)
	headers := modulekit.SignWebhookSenderHeaders("secret", now, "phone-zoom", body)
	// Pin the exact framing independently of the production signer.
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte(`v2.1800000000.phone-zoom.{"provider_id":"phone-google","sender":"core"}`))
	if headers.Get(modulekit.SenderSignatureHeader) != "t=1800000000,v2="+hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("sender wire framing differs")
	}
	if headers.Get(modulekit.SignatureHeader) != modulekit.SignTimestampedWebhook("secret", now, body) {
		t.Fatal("v1 wire changed")
	}
	if err := modulekit.VerifyWebhookHeaders("secret", headers, body, now, 0); err != nil {
		t.Fatal(err)
	}
	if err := modulekit.VerifyWebhook("secret", headers.Get(modulekit.SignatureHeader), body, now, 0); err != nil {
		t.Fatal(err)
	}
	sender, err := modulekit.VerifyWebhookSender("secret", headers, body, now, 0)
	if err != nil || sender != "phone-zoom" {
		t.Fatalf("verified sender %q: %v", sender, err)
	}
	for _, id := range []string{"core", "phone-google"} {
		h := modulekit.SignWebhookSenderHeaders("secret", now, id, body)
		if got, err := modulekit.VerifyWebhookSender("secret", h, body, now, 0); err != nil || got != id {
			t.Fatalf("%q: %v", got, err)
		}
	}
	noStamp := headers.Clone()
	noStamp.Del(modulekit.TimestampHeader)
	if _, err := modulekit.VerifyWebhookSender("secret", noStamp, body, now, 0); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(http.Header){
		"spoof sender":     func(h http.Header) { h.Set(modulekit.SenderHeader, "phone-google") },
		"missing sender":   func(h http.Header) { h.Del(modulekit.SenderHeader) },
		"empty sender":     func(h http.Header) { h.Set(modulekit.SenderHeader, "") },
		"bad sender":       func(h http.Header) { h.Set(modulekit.SenderHeader, "phone.zoom") },
		"missing v2":       func(h http.Header) { h.Del(modulekit.SenderSignatureHeader) },
		"duplicate sender": func(h http.Header) { h.Add(modulekit.SenderHeader, "phone-zoom") },
		"duplicate v2":     func(h http.Header) { h.Add(modulekit.SenderSignatureHeader, h.Get(modulekit.SenderSignatureHeader)) },
		"duplicate v1":     func(h http.Header) { h.Add(modulekit.SignatureHeader, h.Get(modulekit.SignatureHeader)) },
		"bad v2": func(h http.Header) {
			h.Set(modulekit.SenderSignatureHeader, "t=1800000000,v2="+strings.Repeat("0", 64))
		},
		"v2 timestamp mismatch": func(h http.Header) {
			h.Set(modulekit.SenderSignatureHeader, strings.Replace(h.Get(modulekit.SenderSignatureHeader), "1800000000", "1800000001", 1))
		},
		"v1 timestamp mismatch": func(h http.Header) { h.Set(modulekit.TimestampHeader, "1800000001") },
		"bad v1":                func(h http.Header) { h.Set(modulekit.SignatureHeader, "t=1800000000,v1="+strings.Repeat("0", 64)) },
		"legacy v1":             func(h http.Header) { h.Set(modulekit.SignatureHeader, modulekit.SignWebhook("secret", now, body)) },
		"refreshed timestamps": func(h http.Header) {
			h.Set(modulekit.SignatureHeader, strings.Replace(h.Get(modulekit.SignatureHeader), "1800000000", "1800000301", 1))
			h.Set(modulekit.SenderSignatureHeader, strings.Replace(h.Get(modulekit.SenderSignatureHeader), "1800000000", "1800000301", 1))
			h.Set(modulekit.TimestampHeader, "1800000301")
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := headers.Clone()
			change(h)
			got, err := modulekit.VerifyWebhookSender("secret", h, body, now, 0)
			if err == nil || got != "" {
				t.Fatalf("invalid binding returned %q, %v", got, err)
			}
		})
	}
	for name, tc := range map[string]struct {
		secret    string
		headers   http.Header
		body      []byte
		now       time.Time
		tolerance time.Duration
	}{
		"2d":              {"secret", modulekit.SignWebhookHeaders("secret", now, body), body, now, 0},
		"empty key":       {"", headers, body, now, 0},
		"wrong key":       {"other", headers, body, now, 0},
		"body tampered":   {"secret", headers, []byte("other"), now, 0},
		"expired":         {"secret", headers, body, now.Add(301 * time.Second), 0},
		"future":          {"secret", headers, body, now.Add(-301 * time.Second), 0},
		"short window":    {"secret", headers, body, now.Add(time.Minute), 30 * time.Second},
		"negative window": {"secret", headers, body, now, -time.Second},
		"expanded window": {"secret", headers, body, now, 6 * time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := modulekit.VerifyWebhookSender(tc.secret, tc.headers, tc.body, tc.now, tc.tolerance); err == nil || got != "" {
				t.Fatalf("invalid delivery returned %q, %v", got, err)
			}
		})
	}
	for _, id := range []string{"", "phone.zoom", "phone\nzoom"} {
		if modulekit.SignWebhookSenderHeaders("secret", now, id, body) != nil {
			t.Fatalf("signed invalid sender %q", id)
		}
	}
	if modulekit.SignWebhookSenderHeaders("", now, "core", body) != nil {
		t.Fatal("signed with empty key")
	}
	if got, err := modulekit.VerifyWebhookSender("secret", headers, body, now.Add(300*time.Second), 0); err != nil || got != "phone-zoom" {
		t.Fatalf("window boundary: %q, %v", got, err)
	}
}

package modules

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/marketplace"
)

const (
	// SenderHeader identifies the module that pushed/called, or core for core events.
	SenderHeader = "X-Togavsu-Sender"
	// SenderSignatureHeader authenticates "v2." + timestamp + "." + sender + "." + raw body.
	// It is separate from SignatureHeader so strict 2d verifiers keep working.
	SenderSignatureHeader = "X-Togavsu-Sender-Signature"
)

// SignWebhookSenderHeaders adds a sender binding to the unchanged v1 headers.
// It returns nil for an empty secret or invalid/reserved module sender. Core's own
// deliveries use SignCoreWebhookHeaders, never a module-supplied identity.
func SignWebhookSenderHeaders(secret string, at time.Time, sender string, body []byte) http.Header {
	if !marketplace.ValidModuleID(sender) {
		return nil
	}
	return signWebhookSenderHeaders(secret, at, sender, body)
}

// SignCoreWebhookHeaders signs a core-emitted delivery with core's reserved identity.
func SignCoreWebhookHeaders(secret string, at time.Time, body []byte) http.Header {
	return signWebhookSenderHeaders(secret, at, marketplace.CoreSenderID, body)
}

func signWebhookSenderHeaders(secret string, at time.Time, sender string, body []byte) http.Header {
	headers := SignWebhookHeaders(secret, at, body)
	if headers == nil {
		return nil
	}
	headers.Set(SenderHeader, sender)
	headers.Set(SenderSignatureHeader, "t="+WebhookTimestamp(at)+",v2="+senderMAC(secret, WebhookTimestamp(at), sender, body))
	return headers
}

func senderMAC(secret, stamp, sender string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v2." + stamp + "." + sender + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhookSender requires both the timestamped v1 signature and a valid v2
// sender binding, and returns the authenticated sender only on success. It never
// falls back to payload identity or unsigned/legacy delivery. Tolerance follows
// VerifyWebhookHeaders: zero means five minutes, positive may only shorten it.
func VerifyWebhookSender(secret string, headers http.Header, body []byte, now time.Time, tolerance time.Duration) (string, error) {
	for _, name := range []string{SenderHeader, SenderSignatureHeader, SignatureHeader} {
		if len(headers.Values(name)) != one {
			return "", errors.New("modules: missing or repeated sender signature headers")
		}
	}
	sender := headers.Get(SenderHeader)
	if sender != marketplace.CoreSenderID && !marketplace.ValidModuleID(sender) {
		return "", errors.New("modules: invalid webhook sender")
	}
	if err := VerifyWebhookHeaders(secret, headers, body, now, tolerance); err != nil {
		return "", err
	}
	stamp, _, ok := strings.Cut(strings.TrimPrefix(headers.Get(SignatureHeader), "t="), ",v1=")
	if !ok || !strings.HasPrefix(headers.Get(SignatureHeader), "t=") {
		return "", errors.New("modules: sender requires timestamped webhook signature")
	}
	// Compare the complete canonical header, including its timestamp and version.
	want := "t=" + stamp + ",v2=" + senderMAC(secret, stamp, sender, body)
	if !hmac.Equal([]byte(want), []byte(headers.Get(SenderSignatureHeader))) {
		return "", errors.New("modules: webhook sender signature doesn't match")
	}
	return sender, nil
}

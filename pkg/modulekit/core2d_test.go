package modulekit_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

func TestTimestampedWebhookAndProviderHelpers(t *testing.T) {
	now := time.Unix(1800000000, 0)
	body := []byte(`{"id":"synthetic"}`)
	headers := modulekit.SignWebhookHeaders("secret", now, body)
	if err := modulekit.VerifyWebhookHeaders("secret", headers, body, now, 0); err != nil {
		t.Fatal(err)
	}
	// Compile-time assignments pin the existing public API's source compatibility.
	var sign func(string, time.Time, []byte) string = modulekit.SignWebhook
	var verify func(string, string, []byte, time.Time, time.Duration) error = modulekit.VerifyWebhook
	signature := sign("secret", now, body)
	if signature != headers.Get(modulekit.SignatureHeader) || verify("secret", signature, body, now, 0) != nil {
		t.Fatal("legacy string API does not verify the wire signature")
	}
	if sign("", now, body) != "" || verify("", signature, body, now, 0) == nil || verify("secret", signature, []byte("tampered"), now, 0) == nil {
		t.Fatal("legacy API accepted empty key or changed body")
	}
	// Independent batch-1 verifier: HMAC of raw body, sha256=hex, separate timestamp.
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(body)
	if headers.Get(modulekit.SignatureHeader) != "sha256="+hex.EncodeToString(mac.Sum(nil)) || headers.Get(modulekit.TimestampHeader) != "1800000000" {
		t.Fatalf("batch 1 wire format: %v", headers)
	}
	stamp, err := strconv.ParseInt(headers.Get(modulekit.TimestampHeader), 10, 64)
	if err != nil || now.Sub(time.Unix(stamp, 0)) > 5*time.Minute {
		t.Fatal("batch 1 timestamp refused")
	}
	badSig := headers.Clone()
	badSig.Set(modulekit.SignatureHeader, "sha256=deadbeef")
	missing := headers.Clone()
	missing.Del(modulekit.TimestampHeader)
	for name, tc := range map[string]struct {
		secret  string
		headers http.Header
		body    []byte
		at      time.Time
	}{
		"empty key": {"", headers, body, now}, "missing timestamp": {"secret", missing, body, now},
		"expired": {"secret", headers, body, now.Add(5*time.Minute + time.Second)}, "future": {"secret", headers, body, now.Add(-5*time.Minute - time.Second)},
		"changed body": {"secret", headers, []byte("tampered"), now}, "bad signature": {"secret", badSig, body, now},
	} {
		t.Run(name, func(t *testing.T) {
			if modulekit.VerifyWebhookHeaders(tc.secret, tc.headers, tc.body, tc.at, 0) == nil {
				t.Fatal("invalid webhook accepted")
			}
		})
	}
	if modulekit.SignWebhookHeaders("", now, body) != nil {
		t.Fatal("empty secret signed")
	}
	if modulekit.VerifyWebhookHeaders("secret", headers, body, now, -time.Second) == nil {
		t.Fatal("negative tolerance")
	}
	if modulekit.VerifyWebhookHeaders("secret", headers, body, now, 6*time.Minute) == nil {
		t.Fatal("expanded window")
	}
	sum, err := modulekit.ProviderHMAC("provider", []byte("challenge"))
	if err != nil {
		t.Fatal(err)
	}
	crc, err := modulekit.ProviderCRC("provider", "challenge")
	if err != nil || crc != hex.EncodeToString(sum) {
		t.Fatalf("Zoom encryptedToken: %s %v", crc, err)
	}
	base64CRC, err := modulekit.ProviderCRCBase64("provider", "challenge")
	if err != nil || base64CRC != "sha256="+base64.StdEncoding.EncodeToString(sum) {
		t.Fatalf("base64 CRC: %s %v", base64CRC, err)
	}
	if modulekit.VerifyProviderHMAC("provider", []byte("challenge"), sum) != nil {
		t.Fatal("provider signature refused")
	}
	if modulekit.VerifyProviderHMAC("provider", []byte("tampered"), sum) == nil {
		t.Fatal("tampered provider body accepted")
	}
	if _, err := modulekit.ProviderCRC("", "challenge"); err == nil {
		t.Fatal("empty CRC key")
	}
	if _, err := modulekit.ProviderCRCBase64("", "challenge"); err == nil {
		t.Fatal("empty base64 CRC key")
	}
	if _, err := modulekit.ProviderHMAC("", body); err == nil {
		t.Fatal("empty provider key")
	}
}

func TestRuntimePendingCriteriaAndGooseUpInventory(t *testing.T) {
	if modulekit.RuntimeSupported("module.event_record_reads") {
		t.Fatal("removed event-record grants advertised as supported")
	}
	files := sample(t)
	pending, err := modulekit.WritePending([]modulekit.Pending{{Runtime: "processor.payment_principal", Core: modulekit.CoreItems{"CORE-23"}, Reason: "Background payment processor grant is unavailable."}})
	if err != nil {
		t.Fatal(err)
	}
	files[modulekit.PendingFile] = pending
	ip, err := modulekit.Installable(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(ip.Held) != 1 || ip.Held[0].Pending.Runtime != "processor.payment_principal" || len(ip.UnusedRefusals()) != 0 {
		t.Fatalf("runtime pending: %+v", ip)
	}
	if !bytes.Equal(ip.Files[modulekit.ManifestFile], files[modulekit.ManifestFile]) {
		t.Fatal("runtime criterion edited manifest")
	}
	pending, err = modulekit.WritePending([]modulekit.Pending{{Runtime: "migration.up_only", Core: modulekit.CoreItems{"CORE-36"}, Reason: "Only inventory Goose Up."}})
	if err != nil {
		t.Fatal(err)
	}
	files[modulekit.PendingFile] = pending
	ip, err = modulekit.Installable(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(ip.Held) != 0 || len(ip.UnusedRefusals()) != 1 {
		t.Fatal("supported runtime hold was not refused by strict check")
	}
	// The Down body drops the extension: tokenizing it used to erase the inventory.
	delete(files, modulekit.PendingFile)
	for name, sql := range files {
		if len(name) > 11 && name[:11] == "migrations/" {
			files[name] = append(sql, []byte("\n-- +goose Down\nDROP TABLE matter_targets;\n")...)
		}
	}
	if _, _, err := modulekit.Check(files); err != nil {
		t.Fatalf("Goose Up extension vanished: %v", err)
	}
}

func TestWebhookAuthorizationOptInRequiresScope(t *testing.T) {
	files := sample(t)
	files[modulekit.ManifestFile] = append(files[modulekit.ManifestFile], []byte("\ninbound_webhooks:\n  - id: push\n    path: /push\n    forward_authorization: true\n")...)
	if _, _, err := modulekit.Check(files); err == nil || !strings.Contains(err.Error(), "webhooks:authorization") {
		t.Fatalf("authorization forwarding without install permission: %v", err)
	}
}

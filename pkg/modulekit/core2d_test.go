package modulekit_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

func TestTimestampedWebhookAndProviderHelpers(t *testing.T) {
	now := time.Unix(1800000000, 0)
	body := []byte(`{"id":"synthetic"}`)
	sig := modulekit.SignWebhook("secret", now, body)
	if err := modulekit.VerifyWebhook("secret", sig, body, now, 0); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		secret, sig string
		body        []byte
		at          time.Time
	}{
		"empty key": {"", sig, body, now}, "changed timestamp": {"secret", modulekit.SignWebhook("secret", now.Add(time.Second), body), body, now.Add(-5 * time.Minute)},
		"expired": {"secret", sig, body, now.Add(5*time.Minute + time.Second)}, "future": {"secret", sig, body, now.Add(-5*time.Minute - time.Second)},
		"changed body": {"secret", sig, []byte("tampered"), now}, "legacy body only": {"secret", "sha256=deadbeef", body, now},
	} {
		t.Run(name, func(t *testing.T) {
			if modulekit.VerifyWebhook(tc.secret, tc.sig, tc.body, tc.at, 0) == nil {
				t.Fatal("invalid signature accepted")
			}
		})
	}
	if modulekit.SignWebhook("", now, body) != "" {
		t.Fatal("empty secret signed")
	}
	if err := modulekit.VerifyWebhook("secret", sig, body, now, -time.Second); err == nil {
		t.Fatal("negative tolerance")
	}
	mac, err := modulekit.ProviderHMAC("provider", []byte("challenge"))
	if err != nil {
		t.Fatal(err)
	}
	crc, err := modulekit.ProviderCRC("provider", "challenge")
	if err != nil || crc != "sha256="+base64.StdEncoding.EncodeToString(mac) {
		t.Fatalf("CRC %s %v", crc, err)
	}
	if err := modulekit.VerifyProviderHMAC("provider", []byte("challenge"), mac); err != nil {
		t.Fatal(err)
	}
	if modulekit.VerifyProviderHMAC("provider", []byte("tampered"), mac) == nil {
		t.Fatal("tampered provider body accepted")
	}
	if _, err := modulekit.ProviderCRC("", "challenge"); err == nil {
		t.Fatal("empty CRC key")
	}
	if _, err := modulekit.ProviderHMAC("", body); err == nil {
		t.Fatal("empty provider key")
	}
}

func TestRuntimePendingCriteriaAndGooseUpInventory(t *testing.T) {
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

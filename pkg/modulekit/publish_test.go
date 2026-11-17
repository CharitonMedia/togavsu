package modulekit_test

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/marketplace"
	"github.com/CharitonMedia/togavsu/pkg/modulekit"
)

func marketServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	m, err := marketplace.Open(dir, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(dir, "operator_token")
	if err := os.WriteFile(tokenPath, []byte(strings.Repeat("x", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := marketplace.Defaults()
	cfg.DataDir = dir
	cfg.OperatorTokenFile = tokenPath
	cfg.SecureCookies = false
	srv, err := marketplace.NewServer(m, cfg, strings.Repeat("x", 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return hs, hs.URL
}

func signedMatterClock(t *testing.T, priv ed25519.PrivateKey) []byte {
	t.Helper()
	signed, err := modulekit.Build(priv, sample(t))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestPublishEndToEnd(t *testing.T) {
	hs, url := marketServer(t)
	_ = hs
	pub, priv := key(t)
	signed := signedMatterClock(t, priv)

	// New author: register, prove key, upload.
	got, err := modulekit.Publish(modulekit.PublishRequest{
		Market: url, Priv: priv, Name: "Pub Author", Email: "pub@example.test",
		Password: "a long enough password", Files: [][]byte{signed}, HTTP: hs.Client(),
	})
	if err != nil || len(got) != 1 || got[0].ModuleID != "matter-clock" || got[0].Version != "1.0.0" || got[0].Already {
		t.Fatalf("new author: %+v %v", got, err)
	}
	res, err := hs.Client().Get(url + "/v1/listings/matter-clock")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var listing struct {
		PriceNote string `json:"price_note"`
		Author    string `json:"author"`
	}
	if err := json.NewDecoder(res.Body).Decode(&listing); err != nil {
		t.Fatal(err)
	}
	if listing.Author != "Pub Author" || listing.PriceNote != "Free sample" {
		t.Fatalf("listing %+v (key %s)", listing, modulekit.KeyID(pub))
	}

	// Existing author, key already verified: upload is already listed.
	again, err := modulekit.Publish(modulekit.PublishRequest{
		Market: url, Priv: priv, Name: "Pub Author", Email: "pub@example.test",
		Password: "a long enough password", Files: [][]byte{signed}, HTTP: hs.Client(),
	})
	if err != nil || len(again) != 1 || !again[0].Already {
		t.Fatalf("already listed: %+v %v", again, err)
	}

	// Conflicting bytes for the same version.
	other := edit(sample(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, `version: "1.0.0"`, `version: "1.0.0"`)
		// Change a migration comment so the archive digest changes while version stays.
		replaceIn(f, "migrations/0001_matter_targets.sql", "--", "-- changed")
	})
	conflict, err := modulekit.Build(priv, other)
	if err != nil {
		t.Fatal(err)
	}
	_, err = modulekit.Publish(modulekit.PublishRequest{
		Market: url, Priv: priv, Name: "Pub Author", Email: "pub@example.test",
		Password: "a long enough password", Files: [][]byte{conflict}, HTTP: hs.Client(),
	})
	if !errors.Is(err, modulekit.ErrListedConflict) {
		t.Fatalf("conflicting bytes: %v", err)
	}
}

func TestPublishExistingAuthorNewKey(t *testing.T) {
	hs, url := marketServer(t)
	_, first := key(t)
	signed := signedMatterClock(t, first)
	if _, err := modulekit.Publish(modulekit.PublishRequest{
		Market: url, Priv: first, Name: "Same Author", Email: "same@example.test",
		Password: "a long enough password", Files: [][]byte{signed}, HTTP: hs.Client(),
	}); err != nil {
		t.Fatal(err)
	}
	// Same author, a second key: Publish proves it. Ship a new version with that key.
	_, second := key(t)
	files := edit(sample(t), func(f map[string][]byte) {
		replaceIn(f, modulekit.ManifestFile, `version: "1.0.0"`, `version: "1.1.0"`)
	})
	next, err := modulekit.Build(second, files)
	if err != nil {
		t.Fatal(err)
	}
	got, err := modulekit.Publish(modulekit.PublishRequest{
		Market: url, Priv: second, Name: "Same Author", Email: "same@example.test",
		Password: "a long enough password", Files: [][]byte{next}, HTTP: hs.Client(),
	})
	if err != nil || len(got) != 1 || got[0].Version != "1.1.0" || got[0].Already {
		t.Fatalf("existing author new key: %+v %v", got, err)
	}
	res, err := http.Get(url + "/v1/listings/matter-clock")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var listing struct {
		Version string `json:"version"`
	}
	json.NewDecoder(res.Body).Decode(&listing)
	if listing.Version != "1.1.0" {
		t.Fatalf("listing %+v", listing)
	}
}

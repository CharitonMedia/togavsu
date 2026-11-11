package marketplace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testPackage(t *testing.T, id, version string, perms ...any) []byte {
	t.Helper()
	man, _ := json.Marshal(map[string]any{"id": id, "name": "Matter Clock", "version": version, "permissions": perms})
	b, err := BuildPackage(man, map[string][]byte{"migrations/0001.sql": []byte("-- synthetic")})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	pkg := testPackage(t, "matter-clock", "0.1.0", "matters:read")
	sig := Sign(priv, pkg)
	trusted := []TrustedKey{{KeyID: KeyID(pub), Public: pub, Source: "marketplace"}}
	if KeyID(pub) == KeyID(otherPub) || !strings.HasPrefix(KeyID(pub), "ed25519-") || len(KeyID(pub)) != len("ed25519-")+32 {
		t.Fatalf("key id %s", KeyID(pub))
	}
	k, err := Verify(pkg, Digest(pkg), sig, KeyID(pub), trusted)
	if err != nil || k.KeyID != KeyID(pub) {
		t.Fatalf("good package: %v", err)
	}
	tampered := append([]byte{}, pkg...)
	tampered[len(tampered)/2] ^= 1
	cases := []struct {
		name                     string
		pkg                      []byte
		digest, signature, keyID string
		keys                     []TrustedKey
		want                     error
	}{
		{"tampered bytes", tampered, "", sig, KeyID(pub), trusted, ErrSignatureInvalid},
		{"digest mismatch", pkg, Digest(tampered), sig, KeyID(pub), trusted, ErrDigestMismatch},
		{"signature by another key", pkg, "", Sign(otherPriv, pkg), KeyID(pub), trusted, ErrSignatureInvalid},
		{"untrusted signer", pkg, "", Sign(otherPriv, pkg), KeyID(otherPub), trusted, ErrUntrustedKey},
		{"no keys", pkg, "", sig, KeyID(pub), nil, ErrUntrustedKey},
		{"garbage signature", pkg, "", "not base64!", KeyID(pub), trusted, ErrSignatureInvalid},
		// A stored key whose id was relabelled is not that key.
		{"relabelled key", pkg, "", Sign(otherPriv, pkg), KeyID(otherPub), []TrustedKey{{KeyID: KeyID(otherPub), Public: pub}}, ErrUntrustedKey},
	}
	for _, c := range cases {
		if _, err := Verify(c.pkg, c.digest, c.signature, c.keyID, c.keys); !errors.Is(err, c.want) {
			t.Errorf("%s: got %v want %v", c.name, err, c.want)
		}
	}
}

func TestPublicKeyPEM(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	got, err := ParsePublicKeyPEM(PublicKeyPEM(pub))
	if err != nil || !got.Equal(pub) {
		t.Fatalf("round trip: %v", err)
	}
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&ec.PublicKey)
	ecPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	for name, text := range map[string]string{
		"ecdsa key":     ecPEM,
		"two blocks":    PublicKeyPEM(pub) + PublicKeyPEM(pub),
		"private block": strings.ReplaceAll(PublicKeyPEM(pub), "PUBLIC KEY", "PRIVATE KEY"),
		"empty":         "",
	} {
		if _, err := ParsePublicKeyPEM(text); !errors.Is(err, ErrBadKey) {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestReadManifest(t *testing.T) {
	m, err := ReadManifest(testPackage(t, "matter-clock", "1.2.3", "matters:read", map[string]any{"scope": "time:read", "required": false}, "matters:read"))
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "matter-clock" || m.Version != "1.2.3" || strings.Join(m.Permissions, ",") != "matters:read,time:read" {
		t.Fatalf("manifest %+v", m)
	}
	// gzip-compressed packages read the same way.
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write(testPackage(t, "matter-clock", "1.2.3"))
	w.Close()
	if m, err := ReadManifest(gz.Bytes()); err != nil || m.ID != "matter-clock" {
		t.Fatalf("gzip: %v", err)
	}
	bad := map[string][]byte{
		"no manifest": func() []byte {
			var b bytes.Buffer
			tw := tar.NewWriter(&b)
			tw.WriteHeader(&tar.Header{Name: "other.txt", Size: 1, Typeflag: tar.TypeReg})
			tw.Write([]byte("x"))
			tw.Close()
			return b.Bytes()
		}(),
		"not an archive":   []byte("plain text"),
		"bad id":           testPackage(t, "Matter Clock", "1.0.0"),
		"bad version":      testPackage(t, "matter-clock", "latest"),
		"empty permission": testPackage(t, "matter-clock", "1.0.0", ""),
	}
	bad["unsupported yaml"], _ = BuildPackage([]byte("id: &a matter-clock\nname: Matter Clock\nversion: 1.0.0\n"), nil)
	for name, p := range bad {
		if _, err := ReadManifest(p); !errors.Is(err, ErrManifest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Block YAML in the module runtime's subset reads the same as the flow form, and the
	// listing keeps the manifest as JSON.
	block, _ := BuildPackage([]byte("id: matter-clock\nname: Matter Clock\nversion: 1.0.0\npermissions:\n  - scope: matters:read\n  - time:read\n"), nil)
	m, err = ReadManifest(block)
	if err != nil || m.ID != "matter-clock" || len(m.Permissions) != 2 || !json.Valid(m.Raw) {
		t.Fatalf("block yaml: %+v %v", m, err)
	}
}

func TestBaseURL(t *testing.T) {
	for addr, ok := range map[string]bool{
		"https://marketplace.example.test":    true,
		"https://marketplace.example.test/":   true,
		"http://127.0.0.1:8090":               true,
		"http://localhost:8090":               true,
		"http://marketplace.example.test":     false,
		"togavsu-marketplace":                 false,
		"ftp://marketplace.example.test":      false,
		"https://user:pw@marketplace.example": false,
		"":                                    false,
	} {
		if _, err := BaseURL(addr); (err == nil) != ok {
			t.Errorf("%q: %v", addr, err)
		}
	}
}

func TestClientStatuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/listings/pulled-module/versions/1.0.0/package":
			w.WriteHeader(http.StatusGone)
		case "/v1/listings/matter-clock/versions/1.0.0/package":
			// A marketplace that answers with another version is not believed.
			json.NewEncoder(w).Encode(SignedPackage{ListingID: "matter-clock", Version: "2.0.0"})
		case "/v1/listings/matter-clock":
			json.NewEncoder(w).Encode(Listing{ID: "other-module"})
		case "/v1/listings/redirect":
			http.Redirect(w, r, "https://elsewhere.example.test/", http.StatusFound)
		case "/v1/alerts":
			if got := r.URL.Query()["module_id"]; len(got) != 2 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": []Alert{{ID: "a1", ModuleID: "matter-clock", Kind: KindFlag, Note: "n"}}})
		case "/v1/broken":
			w.Write([]byte("{"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := &Client{Address: func(context.Context) string { return srv.URL }}
	ctx := context.Background()
	if _, err := c.Package(ctx, "pulled-module", "1.0.0"); !errors.Is(err, ErrYanked) {
		t.Errorf("410: %v", err)
	}
	if _, err := c.Package(ctx, "matter-clock", "1.0.0"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("substituted version: %v", err)
	}
	if _, err := c.Listing(ctx, "matter-clock"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("wrong listing id: %v", err)
	}
	if _, err := c.Listing(ctx, "redirect"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("redirect followed: %v", err)
	}
	if _, err := c.Listing(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("404: %v", err)
	}
	if _, err := c.Listing(ctx, "../etc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("bad id: %v", err)
	}
	if as, err := c.Alerts(ctx, []string{"matter-clock", "other"}, time.Time{}); err != nil || len(as) != 1 {
		t.Errorf("alerts %v %v", as, err)
	}
	var x map[string]any
	if err := c.do(ctx, "/v1/broken", nil, &x); !errors.Is(err, ErrUnavailable) {
		t.Errorf("broken body: %v", err)
	}
	none := &Client{Address: func(context.Context) string { return "togavsu-marketplace" }}
	if _, err := none.Listings(ctx, ""); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("sentinel address: %v", err)
	}
	srv.Close()
	if _, err := c.Listings(ctx, ""); !errors.Is(err, ErrUnavailable) {
		t.Errorf("closed server: %v", err)
	}
}

func TestAlertPeriodAndWarning(t *testing.T) {
	if AlertPeriod("hourly") != time.Hour || AlertPeriod("daily") != 24*time.Hour || AlertPeriod("weekly") != 7*24*time.Hour || AlertPeriod("bogus") != 24*time.Hour {
		t.Fatal("alert periods")
	}
	if Warning(KindFlag, "n") != "We've been alerted that this item has problems. n" || !strings.HasPrefix(Warning(KindPull, ""), "This module was pulled") {
		t.Fatal("warnings")
	}
	if !applies(Alert{ModuleID: "m"}, InstalledModule{ID: "m", Version: "1"}) || applies(Alert{ModuleID: "m", Version: "2"}, InstalledModule{ID: "m", Version: "1"}) || applies(Alert{ModuleID: "n"}, InstalledModule{ID: "m"}) {
		t.Fatal("applies")
	}
}

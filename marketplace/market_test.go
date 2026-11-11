package marketplace

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	shared "github.com/CharitonMedia/togavsu/internal/marketplace"
)

const opToken = "synthetic-operator-token-0123456789abcdef"

type env struct {
	t   *testing.T
	dir string
	m   *Market
	srv *httptest.Server
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, dir: t.TempDir(), now: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}
	e.open()
	return e
}

func (e *env) clock() time.Time { e.now = e.now.Add(time.Second); return e.now }

func (e *env) open() {
	cfg := Defaults()
	cfg.SecureCookies = false
	m, err := Open(e.dir, cfg.AuthorSession(), e.clock)
	if err != nil {
		e.t.Fatal(err)
	}
	s, err := NewServer(m, cfg, opToken, e.clock)
	if err != nil {
		e.t.Fatal(err)
	}
	if e.srv != nil {
		e.srv.Close()
	}
	e.m = m
	e.srv = httptest.NewServer(s.Handler())
	e.t.Cleanup(e.srv.Close)
}

type resp struct {
	code int
	body []byte
	hdr  http.Header
}

func (r resp) json(t *testing.T, dst any) {
	t.Helper()
	if err := json.Unmarshal(r.body, dst); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (e *env) do(method, path, token string, body any) resp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{code: res.StatusCode, body: b, hdr: res.Header}
}

type author struct {
	token string
	priv  ed25519.PrivateKey
	keyID string
}

// author registers an account, signs in, and registers and verifies one key.
func (e *env) author(name, email string) author {
	e.t.Helper()
	if r := e.do("POST", "/v1/authors", "", map[string]any{"name": name, "email": email, "password": "synthetic author password"}); r.code != http.StatusCreated {
		e.t.Fatalf("register %d %s", r.code, r.body)
	}
	r := e.do("POST", "/v1/sessions", "", map[string]any{"email": email, "password": "synthetic author password"})
	if r.code != http.StatusCreated {
		e.t.Fatalf("login %d %s", r.code, r.body)
	}
	var tok struct{ Token string }
	r.json(e.t, &tok)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	r = e.do("POST", "/v1/authors/me/keys", tok.Token, map[string]any{"public_key_pem": shared.PublicKeyPEM(pub)})
	if r.code != http.StatusCreated {
		e.t.Fatalf("add key %d %s", r.code, r.body)
	}
	var k KeyView
	r.json(e.t, &k)
	if k.Verified || k.Challenge == "" {
		e.t.Fatalf("a new key must start unverified with a challenge: %+v", k)
	}
	sig := shared.Sign(priv, shared.KeyProofMessage(k.KeyID, k.Challenge))
	if r = e.do("POST", "/v1/authors/me/keys/"+k.KeyID+"/verify", tok.Token, map[string]any{"signature": sig}); r.code != http.StatusOK {
		e.t.Fatalf("verify %d %s", r.code, r.body)
	}
	return author{token: tok.Token, priv: priv, keyID: k.KeyID}
}

func pkg(t *testing.T, id, version string, perms ...string) []byte {
	t.Helper()
	man, _ := json.Marshal(map[string]any{"id": id, "name": "Matter Clock", "version": version, "permissions": perms})
	b, err := shared.BuildPackage(man, map[string][]byte{"migrations/0001_matter_targets.sql": []byte("-- synthetic\n")})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *env) publish(a author, p []byte, price *string) resp {
	body := map[string]any{"package_base64": base64.StdEncoding.EncodeToString(p), "signature": shared.Sign(a.priv, p), "key_id": a.keyID}
	if price != nil {
		body["price_note"] = *price
	}
	return e.do("POST", "/v1/authors/me/packages", a.token, body)
}

func TestAuthorAccounts(t *testing.T) {
	e := newEnv(t)
	e.author("Synthetic Author", "author@example.test")
	if r := e.do("POST", "/v1/authors", "", map[string]any{"name": "Other", "email": "author@example.test", "password": "synthetic author password"}); r.code != http.StatusConflict {
		t.Fatalf("duplicate email: %d", r.code)
	}
	if r := e.do("POST", "/v1/authors", "", map[string]any{"name": "Short", "email": "short@example.test", "password": "x"}); r.code != http.StatusBadRequest {
		t.Fatalf("short password: %d", r.code)
	}
	if r := e.do("POST", "/v1/authors", "", map[string]any{"name": "Bad", "email": "not an email", "password": "synthetic author password"}); r.code != http.StatusBadRequest {
		t.Fatalf("bad email: %d", r.code)
	}
	if r := e.do("POST", "/v1/sessions", "", map[string]any{"email": "author@example.test", "password": "wrong password here"}); r.code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", r.code)
	}
	if r := e.do("GET", "/v1/authors/me/keys", "not-a-token", nil); r.code != http.StatusUnauthorized {
		t.Fatalf("bad token: %d", r.code)
	}
}

func TestSigningKeyRegistrationIsVerified(t *testing.T) {
	e := newEnv(t)
	e.do("POST", "/v1/authors", "", map[string]any{"name": "A", "email": "a@example.test", "password": "synthetic author password"})
	r := e.do("POST", "/v1/sessions", "", map[string]any{"email": "a@example.test", "password": "synthetic author password"})
	var tok struct{ Token string }
	r.json(t, &tok)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	r = e.do("POST", "/v1/authors/me/keys", tok.Token, map[string]any{"public_key_pem": shared.PublicKeyPEM(pub)})
	var k KeyView
	r.json(t, &k)
	if k.KeyID != shared.KeyID(pub) {
		t.Fatalf("key id %s", k.KeyID)
	}
	// An unverified key is not published and cannot sign an upload.
	if keys := e.m.SigningKeys(); len(keys) != 0 {
		t.Fatalf("unverified key published: %+v", keys)
	}
	p := pkg(t, "matter-clock", "0.1.0")
	up := map[string]any{"package_base64": base64.StdEncoding.EncodeToString(p), "signature": shared.Sign(priv, p), "key_id": k.KeyID}
	if r := e.do("POST", "/v1/authors/me/packages", tok.Token, up); r.code != http.StatusBadRequest {
		t.Fatalf("upload with an unverified key: %d %s", r.code, r.body)
	}
	// A proof signed by another key, or over the wrong text, does not verify it.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	for _, sig := range []string{shared.Sign(other, shared.KeyProofMessage(k.KeyID, k.Challenge)), shared.Sign(priv, []byte(k.Challenge)), "garbage"} {
		if r := e.do("POST", "/v1/authors/me/keys/"+k.KeyID+"/verify", tok.Token, map[string]any{"signature": sig}); r.code != http.StatusBadRequest {
			t.Fatalf("bad proof accepted: %d %s", r.code, r.body)
		}
	}
	if r := e.do("POST", "/v1/authors/me/keys/"+k.KeyID+"/verify", tok.Token, map[string]any{"signature": shared.Sign(priv, shared.KeyProofMessage(k.KeyID, k.Challenge))}); r.code != http.StatusOK {
		t.Fatalf("good proof refused: %d %s", r.code, r.body)
	}
	keys := e.m.SigningKeys()
	if len(keys) != 1 || keys[0].KeyID != k.KeyID || keys[0].Author != "A" {
		t.Fatalf("published keys %+v", keys)
	}
	// A key another author registered stays theirs.
	b := e.author("B", "b@example.test")
	if r := e.do("POST", "/v1/authors/me/keys", b.token, map[string]any{"public_key_pem": shared.PublicKeyPEM(pub)}); r.code != http.StatusConflict {
		t.Fatalf("key reuse across authors: %d", r.code)
	}
	if r := e.do("POST", "/v1/authors/me/keys", b.token, map[string]any{"public_key_pem": "-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n"}); r.code != http.StatusBadRequest {
		t.Fatalf("garbage key: %d", r.code)
	}
	var list struct{ Data []KeyView }
	e.do("GET", "/v1/signing-keys", "", nil).json(t, &list)
	if len(list.Data) != 2 {
		t.Fatalf("signing-keys %+v", list.Data)
	}
}

func TestUploadAndListing(t *testing.T) {
	e := newEnv(t)
	a := e.author("Synthetic Author", "author@example.test")
	price := "$10 per month"
	r := e.publish(a, pkg(t, "matter-clock", "0.1.0", "matters:read", "time:read"), &price)
	if r.code != http.StatusCreated {
		t.Fatalf("publish %d %s", r.code, r.body)
	}
	if r := e.publish(a, pkg(t, "matter-clock", "0.2.0", "matters:read", "time:read", "notes:write"), nil); r.code != http.StatusCreated {
		t.Fatalf("publish 0.2.0 %d %s", r.code, r.body)
	}
	var l shared.Listing
	e.do("GET", "/v1/listings/matter-clock", "", nil).json(t, &l)
	if l.Author != "Synthetic Author" || l.SigningKey != a.keyID || l.SigningKeyPEM == "" || l.Version != "0.2.0" || l.PriceNote != price || l.Notice != shared.Notice {
		t.Fatalf("listing %+v", l)
	}
	if strings.Join(l.Permissions, ",") != "matters:read,time:read,notes:write" || len(l.VersionHistory) != 2 || l.VersionHistory[0].Version != "0.1.0" {
		t.Fatalf("listing permissions/history %+v", l)
	}
	// Listed versions never change.
	if r := e.publish(a, pkg(t, "matter-clock", "0.2.0", "admin:write"), nil); r.code != http.StatusConflict {
		t.Fatalf("republish same version: %d", r.code)
	}
	// Another author cannot publish under this module id.
	b := e.author("Other Author", "other@example.test")
	if r := e.publish(b, pkg(t, "matter-clock", "0.3.0"), nil); r.code != http.StatusForbidden {
		t.Fatalf("other author: %d", r.code)
	}
	// A signature that does not match the bytes is refused.
	p := pkg(t, "other-module", "1.0.0")
	tampered := append([]byte{}, p...)
	tampered[len(tampered)-1] ^= 1
	body := map[string]any{"package_base64": base64.StdEncoding.EncodeToString(tampered), "signature": shared.Sign(b.priv, p), "key_id": b.keyID}
	if r := e.do("POST", "/v1/authors/me/packages", b.token, body); r.code != http.StatusBadRequest {
		t.Fatalf("tampered upload: %d", r.code)
	}
	// A package without a manifest cannot be listed.
	noManifest := []byte("not a tar archive")
	body = map[string]any{"package_base64": base64.StdEncoding.EncodeToString(noManifest), "signature": shared.Sign(b.priv, noManifest), "key_id": b.keyID}
	if r := e.do("POST", "/v1/authors/me/packages", b.token, body); r.code != http.StatusBadRequest {
		t.Fatalf("no manifest: %d", r.code)
	}
	// The package served is the exact signed bytes, at the requested version.
	var sp shared.SignedPackage
	e.do("GET", "/v1/listings/matter-clock/versions/0.1.0/package", "", nil).json(t, &sp)
	raw, _ := base64.StdEncoding.DecodeString(sp.PackageBase64)
	if sp.Version != "0.1.0" || !sp.Immutable || sp.Digest != shared.Digest(raw) || sp.KeyID != a.keyID || sp.Manifest["id"] != "matter-clock" {
		t.Fatalf("package %+v", sp)
	}
	pub := a.priv.Public().(ed25519.PublicKey)
	if err := shared.VerifySignature(pub, raw, sp.Signature); err != nil {
		t.Fatal(err)
	}
	if r := e.do("GET", "/v1/listings/matter-clock/versions/9.9.9/package", "", nil); r.code != http.StatusNotFound {
		t.Fatalf("missing version: %d", r.code)
	}
	if r := e.do("GET", "/v1/listings/nope", "", nil); r.code != http.StatusNotFound {
		t.Fatalf("missing listing: %d", r.code)
	}
	// Search by name, id and author; the listing page shows every PRD 10.2 item.
	var found struct{ Data []shared.Listing }
	e.do("GET", "/v1/listings?q=CLOCK", "", nil).json(t, &found)
	if len(found.Data) != 1 {
		t.Fatalf("search %+v", found)
	}
	e.do("GET", "/v1/listings?q=synthetic+author", "", nil).json(t, &found)
	if len(found.Data) != 1 {
		t.Fatalf("search by author %+v", found)
	}
	page := e.do("GET", "/listings/matter-clock", "", nil)
	for _, want := range []string{"Synthetic Author", a.keyID, "notes:write", "0.1.0", "0.2.0", "$10 per month", "Modules are published by their authors. The Togavsu marketplace does not review, test or warrant any module. Install at your own risk."} {
		if !strings.Contains(string(page.body), want) && !strings.Contains(string(page.body), htmlEscape(want)) {
			t.Fatalf("listing page lacks %q", want)
		}
	}
	if csp := page.hdr.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("csp %q", csp)
	}
}

func htmlEscape(s string) string {
	return strings.NewReplacer("'", "&#39;", "&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;").Replace(s)
}

func TestFlagAndPull(t *testing.T) {
	e := newEnv(t)
	a := e.author("Synthetic Author", "author@example.test")
	e.publish(a, pkg(t, "matter-clock", "0.1.0", "matters:read"), nil)
	e.publish(a, pkg(t, "matter-clock", "0.2.0", "matters:read"), nil)
	e.publish(a, pkg(t, "other-module", "1.0.0"), nil)

	// Only the operator flags or pulls, and a flag needs a note.
	if r := e.do("POST", "/v1/operator/listings/matter-clock/flag", "", map[string]any{"note": "x"}); r.code != http.StatusUnauthorized {
		t.Fatalf("anonymous flag: %d", r.code)
	}
	if r := e.do("POST", "/v1/operator/listings/matter-clock/flag", a.token, map[string]any{"note": "x"}); r.code != http.StatusUnauthorized {
		t.Fatalf("author flag: %d", r.code)
	}
	if r := e.do("POST", "/v1/operator/listings/matter-clock/flag", opToken, map[string]any{"note": "  "}); r.code != http.StatusBadRequest {
		t.Fatalf("flag without note: %d", r.code)
	}
	if r := e.do("POST", "/v1/operator/listings/matter-clock/flag", opToken, map[string]any{"note": "x", "version": "9.9.9"}); r.code != http.StatusNotFound {
		t.Fatalf("flag missing version: %d", r.code)
	}
	if r := e.do("POST", "/v1/operator/listings/nope/flag", opToken, map[string]any{"note": "x"}); r.code != http.StatusNotFound {
		t.Fatalf("flag missing listing: %d", r.code)
	}

	// A version flag marks that version; the current version is unflagged.
	r := e.do("POST", "/v1/operator/listings/matter-clock/flag", opToken, map[string]any{"note": "0.1.0 leaks hours", "version": "0.1.0"})
	if r.code != http.StatusOK {
		t.Fatalf("flag version %d %s", r.code, r.body)
	}
	var l shared.Listing
	r.json(t, &l)
	if l.Flagged {
		t.Fatalf("a flag on an old version flags the listing: %+v", l)
	}
	if ok, note := l.VersionFlag("0.1.0"); !ok || note != "0.1.0 leaks hours" {
		t.Fatalf("version flag %+v", l.VersionHistory)
	}
	// A module flag shows the warning on the listing; installs stay possible.
	note := "Reports of wrong totals on closed matters."
	r = e.do("POST", "/v1/operator/listings/matter-clock/flag", opToken, map[string]any{"note": note})
	r.json(t, &l)
	if !l.Flagged || l.FlagNote != note || l.FlagText() != "We've been alerted that this item has problems. "+note {
		t.Fatalf("flagged listing %+v", l)
	}
	if r := e.do("GET", "/v1/listings/matter-clock/versions/0.2.0/package", "", nil); r.code != http.StatusOK {
		t.Fatalf("flagged package must stay installable: %d", r.code)
	}
	page := string(e.do("GET", "/listings/matter-clock", "", nil).body)
	if !strings.Contains(page, htmlEscape("We've been alerted that this item has problems. "+note)) {
		t.Fatalf("listing page lacks the warning: %s", page)
	}
	if !strings.Contains(string(e.do("GET", "/?q=matter", "", nil).body), htmlEscape("We've been alerted that this item has problems.")) {
		t.Fatal("search page lacks the warning")
	}

	// A pull takes it out of search and stops marketplace installs; the listing still
	// says why.
	r = e.do("POST", "/v1/operator/listings/matter-clock/pull", opToken, map[string]any{"note": "Author asked us to pull it."})
	if r.code != http.StatusOK {
		t.Fatalf("pull %d %s", r.code, r.body)
	}
	if r := e.do("POST", "/v1/operator/listings/matter-clock/pull", opToken, nil); r.code != http.StatusOK {
		t.Fatalf("second pull %d", r.code)
	}
	var found struct{ Data []shared.Listing }
	e.do("GET", "/v1/listings", "", nil).json(t, &found)
	if len(found.Data) != 1 || found.Data[0].ID != "other-module" {
		t.Fatalf("pulled module in search: %+v", found.Data)
	}
	if strings.Contains(string(e.do("GET", "/?q=matter", "", nil).body), "/listings/matter-clock") {
		t.Fatal("pulled module on the search page")
	}
	for _, v := range []string{"0.1.0", "0.2.0"} {
		if r := e.do("GET", "/v1/listings/matter-clock/versions/"+v+"/package", "", nil); r.code != http.StatusGone {
			t.Fatalf("pulled package %s: %d", v, r.code)
		}
	}
	e.do("GET", "/v1/listings/matter-clock", "", nil).json(t, &l)
	if !l.Pulled {
		t.Fatal("pulled listing reads as not pulled")
	}

	// Alerts: two flags and one pull (the repeat pull adds none), for the asked modules.
	var alerts struct{ Data []shared.Alert }
	e.do("GET", "/v1/alerts?module_id=matter-clock", "", nil).json(t, &alerts)
	if len(alerts.Data) != 3 || alerts.Data[0].Version != "0.1.0" || alerts.Data[1].Note != note || alerts.Data[2].Kind != shared.KindPull {
		t.Fatalf("alerts %+v", alerts.Data)
	}
	for _, a := range alerts.Data {
		if a.ID == "" || a.CreatedAt.IsZero() {
			t.Fatalf("alert without id or time %+v", a)
		}
	}
	since := alerts.Data[2].CreatedAt.Format(time.RFC3339Nano)
	e.do("GET", "/v1/alerts?module_id=matter-clock&since="+url.QueryEscape(since), "", nil).json(t, &alerts)
	if len(alerts.Data) != 1 {
		t.Fatalf("since filter %+v", alerts.Data)
	}
	e.do("GET", "/v1/alerts?module_id=other-module", "", nil).json(t, &alerts)
	if len(alerts.Data) != 0 {
		t.Fatalf("other module alerts %+v", alerts.Data)
	}
	if r := e.do("GET", "/v1/alerts?since=yesterday", "", nil); r.code != http.StatusBadRequest {
		t.Fatalf("bad since: %d", r.code)
	}
}

func TestStateSurvivesRestart(t *testing.T) {
	e := newEnv(t)
	a := e.author("Synthetic Author", "author@example.test")
	e.publish(a, pkg(t, "matter-clock", "0.1.0"), nil)
	e.do("POST", "/v1/operator/listings/matter-clock/flag", opToken, map[string]any{"note": "kept"})
	e.open()
	l, err := e.m.Listing("matter-clock")
	if err != nil || !l.Flagged || l.FlagNote != "kept" {
		t.Fatalf("after restart %+v %v", l, err)
	}
	if _, err := e.m.Package("matter-clock", "0.1.0"); err != nil {
		t.Fatal(err)
	}
	// A stored package whose bytes changed on disk is not served.
	entries, _ := os.ReadDir(filepath.Join(e.dir, "packages"))
	if len(entries) != 1 {
		t.Fatalf("packages %v", entries)
	}
	if err := os.WriteFile(filepath.Join(e.dir, "packages", entries[0].Name()), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := e.do("GET", "/v1/listings/matter-clock/versions/0.1.0/package", "", nil); r.code != http.StatusInternalServerError {
		t.Fatalf("changed package served: %d", r.code)
	}
	// The author's session survives too.
	if r := e.do("GET", "/v1/authors/me/keys", a.token, nil); r.code != http.StatusOK {
		t.Fatalf("session after restart: %d", r.code)
	}
}

func TestOperatorPages(t *testing.T) {
	e := newEnv(t)
	a := e.author("Synthetic Author", "author@example.test")
	e.publish(a, pkg(t, "matter-clock", "0.1.0"), nil)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	post := func(path string, form url.Values) *http.Response {
		res, err := c.PostForm(e.srv.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := post("/operator/login", url.Values{"token": {"wrong token value that is long enough"}}); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", res.StatusCode)
	}
	// Without a session a form only redirects to sign-in and changes nothing.
	post("/operator/listings/matter-clock/flag", url.Values{"note": {"x"}})
	if l, _ := e.m.Listing("matter-clock"); l.Flagged {
		t.Fatal("flag without a session")
	}
	res := post("/operator/login", url.Values{"token": {opToken}})
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindStringSubmatch(string(body))
	if res.StatusCode != http.StatusOK || m == nil {
		t.Fatalf("operator page %d %s", res.StatusCode, body)
	}
	for _, ck := range jar.Cookies(&url.URL{Scheme: "http", Host: strings.TrimPrefix(e.srv.URL, "http://"), Path: "/operator"}) {
		if ck.Name == opCookie && ck.Value == "" {
			t.Fatal("empty session cookie")
		}
	}
	if res := post("/operator/listings/matter-clock/flag", url.Values{"note": {"x"}, "csrf": {"forged"}}); res.StatusCode != http.StatusForbidden {
		t.Fatalf("forged csrf: %d", res.StatusCode)
	}
	if res := post("/operator/listings/matter-clock/flag", url.Values{"note": {""}, "csrf": {m[1]}}); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("flag without note: %d", res.StatusCode)
	}
	if res := post("/operator/listings/matter-clock/flag", url.Values{"note": {"Synthetic flag note"}, "csrf": {m[1]}}); res.StatusCode != http.StatusOK {
		t.Fatalf("flag: %d", res.StatusCode)
	}
	if l, _ := e.m.Listing("matter-clock"); !l.Flagged || l.FlagNote != "Synthetic flag note" {
		t.Fatalf("flag form %+v", l)
	}
	if res := post("/operator/listings/matter-clock/pull", url.Values{"csrf": {m[1]}}); res.StatusCode != http.StatusOK {
		t.Fatalf("pull: %d", res.StatusCode)
	}
	if l, _ := e.m.Listing("matter-clock"); !l.Pulled {
		t.Fatal("pull form")
	}
	post("/operator/logout", url.Values{"csrf": {m[1]}})
	res, _ = c.Get(e.srv.URL + "/operator")
	body, _ = io.ReadAll(res.Body)
	if !strings.Contains(string(body), "Operator sign in") {
		t.Fatal("still signed in after logout")
	}
}

func TestConfig(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	cfgFile := filepath.Join(dir, "config.json")
	os.WriteFile(cfgFile, []byte(`{"data_dir": "`+dir+`", "operator_token_file": "`+tokenFile+`"}`), 0o600)
	cfg, err := LoadConfig(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8090" || cfg.AuthorSession() != 12*time.Hour || cfg.OperatorSession() != time.Hour {
		t.Fatalf("defaults %+v", cfg)
	}
	os.WriteFile(tokenFile, []byte("short\n"), 0o600)
	if _, err := cfg.OperatorToken(); err == nil {
		t.Fatal("short operator token accepted")
	}
	os.WriteFile(tokenFile, []byte(opToken+"\n"), 0o600)
	if tok, err := cfg.OperatorToken(); err != nil || tok != opToken {
		t.Fatalf("token %q %v", tok, err)
	}
	os.WriteFile(cfgFile, []byte(`{"data_dir": "x", "typo": 1}`), 0o600)
	if _, err := LoadConfig(cfgFile); err == nil {
		t.Fatal("unknown config field accepted")
	}
	example, err := LoadConfig("config.example.json")
	if err != nil || example.DataDir == "" {
		t.Fatalf("example config %v", err)
	}
}

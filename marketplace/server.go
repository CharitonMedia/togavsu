package marketplace

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	shared "github.com/CharitonMedia/togavsu/internal/marketplace"
)

//go:embed templates/*.html static/*.css
var assets embed.FS

// Server is the marketplace's HTTP surface: the read API the core uses, the author API,
// the operator API, and the listing, search and operator pages.
type Server struct {
	M             *Market
	operatorHash  [sha256.Size]byte
	secureCookies bool
	opLife        time.Duration
	maxJSON       int64
	pages         *template.Template
	now           func() time.Time

	mu       sync.Mutex
	sessions map[string]opSession
}

type opSession struct {
	csrf    string
	expires time.Time
}

// NewServer returns the server. operatorToken is the operator's secret.
func NewServer(m *Market, cfg Config, operatorToken string, now func() time.Time) (*Server, error) {
	if len(operatorToken) < cfg.MinOperatorTokenBytes {
		return nil, errors.New("marketplace: operator token is too short")
	}
	if now == nil {
		now = time.Now
	}
	t, err := template.New("pages").Funcs(template.FuncMap{
		"date": func(t time.Time) string { return t.UTC().Format("2006-01-02") },
	}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{M: m, operatorHash: sha256.Sum256([]byte(operatorToken)), secureCookies: cfg.SecureCookies,
		opLife: cfg.OperatorSession(), maxJSON: cfg.MaxJSONBytes, pages: t, now: now, sessions: map[string]opSession{}}, nil
}

// Handler routes every request.
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	// Read API (the core).
	m.HandleFunc("GET /v1/listings", s.apiSearch)
	m.HandleFunc("GET /v1/listings/{listing_id}", s.apiListing)
	m.HandleFunc("GET /v1/listings/{listing_id}/versions/{version}/package", s.apiPackage)
	m.HandleFunc("GET /v1/alerts", s.apiAlerts)
	m.HandleFunc("GET /v1/signing-keys", s.apiSigningKeys)
	// Authors.
	m.HandleFunc("POST /v1/authors", s.apiRegister)
	m.HandleFunc("POST /v1/sessions", s.apiLogin)
	m.HandleFunc("GET /v1/authors/me/keys", s.apiKeys)
	m.HandleFunc("POST /v1/authors/me/keys", s.apiAddKey)
	m.HandleFunc("POST /v1/authors/me/keys/{key_id}/verify", s.apiVerifyKey)
	m.HandleFunc("POST /v1/authors/me/packages", s.apiUpload)
	m.HandleFunc("PATCH /v1/authors/me/listings/{listing_id}/price", s.apiSetPrice)
	// Operator.
	m.HandleFunc("POST /v1/operator/listings/{listing_id}/flag", s.apiFlag)
	m.HandleFunc("POST /v1/operator/listings/{listing_id}/pull", s.apiPull)
	// Pages.
	m.HandleFunc("GET /{$}", s.pageSearch)
	m.HandleFunc("GET /listings/{listing_id}", s.pageListing)
	m.HandleFunc("GET /operator", s.pageOperator)
	m.HandleFunc("POST /operator/login", s.formLogin)
	m.HandleFunc("POST /operator/logout", s.formLogout)
	m.HandleFunc("POST /operator/listings/{listing_id}/flag", s.formFlag)
	m.HandleFunc("POST /operator/listings/{listing_id}/pull", s.formPull)
	m.HandleFunc("GET /static/site.css", s.css)
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return headers(m)
}

// headers sets the security headers on every response.
func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(shared.Problem{Status: status, Code: code, Detail: detail})
}

func writeErr(w http.ResponseWriter, err error) {
	var inv *InvalidError
	switch {
	case errors.As(err, &inv):
		problem(w, http.StatusBadRequest, "validation", inv.Error())
	case errors.Is(err, ErrNotFound):
		problem(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, ErrPulled):
		problem(w, http.StatusGone, "yanked", "this module was pulled from the marketplace and can't be installed from it; a firm can still install it from a local file")
	case errors.Is(err, ErrUnauthorized):
		problem(w, http.StatusUnauthorized, "unauthenticated", "sign in")
	case errors.Is(err, ErrForbidden):
		problem(w, http.StatusForbidden, "forbidden", "another author publishes this module id")
	case errors.Is(err, ErrConflict):
		problem(w, http.StatusConflict, "conflict", "already exists; listed versions never change")
	default:
		problem(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func decode(r *http.Request, limit int64, dst any) error {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, limit))
	if err != nil {
		return invalid("body", "the request body is too large or unreadable")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return invalid("body", "the body must be a JSON object with known fields")
	}
	return nil
}

func bearer(r *http.Request) string {
	t, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(t)
}

// --- Read API ---

func (s *Server) apiSearch(w http.ResponseWriter, r *http.Request) {
	list := s.M.Search(r.URL.Query().Get("q"))
	out := make([]map[string]any, 0, len(list))
	for _, l := range list {
		out = append(out, s.listingBody(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) apiListing(w http.ResponseWriter, r *http.Request) {
	l, err := s.M.Listing(r.PathValue("listing_id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.listingBody(l))
}

func (s *Server) listingBody(l shared.Listing) map[string]any {
	body := map[string]any{
		"id": l.ID, "module_id": l.ModuleID, "name": l.Name, "author": l.Author,
		"signing_key": l.SigningKey, "signing_key_pem": l.SigningKeyPEM, "version": l.Version,
		"permissions": l.Permissions, "version_history": l.VersionHistory, "price_note": l.PriceNote,
		"flagged": l.Flagged, "pulled": l.Pulled, "notice": l.Notice,
		"provider_disclaimer": ProviderDisclaimer(l.Author),
	}
	if l.FlagNote != "" {
		body["flag_note"] = l.FlagNote
	}
	if l.ListingFlagged {
		body["listing_flagged"] = true
	}
	if p, err := s.M.ListingPriceOf(l.ID); err == nil && !p.empty() {
		body["price"] = p
	}
	if u, err := s.M.ListingSalesURL(l.ID); err == nil && u != "" {
		body["sales_url"] = u
	}
	return body
}

func (s *Server) apiPackage(w http.ResponseWriter, r *http.Request) {
	p, err := s.M.Package(r.PathValue("listing_id"), r.PathValue("version"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) apiAlerts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var since time.Time
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			problem(w, http.StatusBadRequest, "validation", "since is an RFC 3339 time")
			return
		}
		since = t
	}
	var ids []string
	for _, v := range q["module_id"] {
		for _, id := range strings.Split(v, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.M.Alerts(ids, since)})
}

func (s *Server) apiSigningKeys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": s.M.SigningKeys()})
}

// --- Author API ---

func (s *Server) apiRegister(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	a, err := s.M.RegisterAuthor(b.Name, b.Email, b.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	tok, err := s.M.Login(b.Email, b.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": tok})
}

func (s *Server) author(w http.ResponseWriter, r *http.Request) (Author, bool) {
	a, err := s.M.Author(bearer(r))
	if err != nil {
		writeErr(w, err)
		return Author{}, false
	}
	return a, true
}

func (s *Server) apiKeys(w http.ResponseWriter, r *http.Request) {
	a, ok := s.author(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.M.Keys(a)})
}

func (s *Server) apiAddKey(w http.ResponseWriter, r *http.Request) {
	a, ok := s.author(w, r)
	if !ok {
		return
	}
	var b struct {
		PublicKeyPEM string `json:"public_key_pem"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	k, err := s.M.AddKey(a, b.PublicKeyPEM)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

func (s *Server) apiVerifyKey(w http.ResponseWriter, r *http.Request) {
	a, ok := s.author(w, r)
	if !ok {
		return
	}
	var b struct {
		Signature string `json:"signature"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	k, err := s.M.VerifyKey(a, r.PathValue("key_id"), b.Signature)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, k)
}

func (s *Server) apiUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := s.author(w, r)
	if !ok {
		return
	}
	// Accept the kit's signed package file unchanged (listing_id, version, immutable,
	// manifest, digest, signature, key_id, package_base64). The older three-field body
	// and optional price_note still work for existing callers.
	var b struct {
		PackageBase64 string         `json:"package_base64"`
		Signature     string         `json:"signature"`
		KeyID         string         `json:"key_id"`
		Digest        string         `json:"digest"`
		ListingID     string         `json:"listing_id"`
		Version       string         `json:"version"`
		Immutable     *bool          `json:"immutable"`
		Manifest      map[string]any `json:"manifest"`
		PriceNote     *string        `json:"price_note"`
	}
	if err := decode(r, shared.MaxPackageBytes()/3*4+s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	pkg, err := base64.StdEncoding.DecodeString(b.PackageBase64)
	if err != nil {
		writeErr(w, invalid("package_base64", "the package must be standard base64"))
		return
	}
	if err := checkUploadPackageMeta(b.ListingID, b.Version, b.Immutable, b.Manifest, pkg); err != nil {
		writeErr(w, err)
		return
	}
	l, err := s.M.Publish(a, Upload{Package: pkg, Signature: b.Signature, KeyID: b.KeyID, Digest: b.Digest, PriceNote: b.PriceNote})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.listingBody(l))
}

// checkUploadPackageMeta refuses a signed-file body whose listing_id, version, immutable
// or manifest disagree with the package bytes (kit SignFile or a marketplace download).
func checkUploadPackageMeta(listingID, version string, immutable *bool, manifest map[string]any, pkg []byte) error {
	if listingID == "" && version == "" && immutable == nil && manifest == nil {
		return nil
	}
	man, err := shared.ReadManifest(pkg)
	if err != nil {
		return invalid("package", "the package needs a "+shared.ManifestName+" with id, name, version and permissions")
	}
	if listingID != "" && listingID != man.ID {
		return invalid("listing_id", "listing_id does not match the package")
	}
	if version != "" && version != man.Version {
		return invalid("version", "version does not match the package")
	}
	var pkgMan map[string]any
	if err := json.Unmarshal(man.Raw, &pkgMan); err != nil {
		return invalid("package", "the manifest is unreadable")
	}
	if manifest != nil {
		want, _ := json.Marshal(pkgMan)
		got, _ := json.Marshal(manifest)
		if !bytes.Equal(want, got) {
			return invalid("manifest", "manifest does not match the package")
		}
	}
	if immutable != nil && *immutable && manifest == nil {
		// Marketplace download shape is immutable:true with a matching manifest. The kit
		// SignFile shape is immutable:false and manifest:null. immutable:true without a
		// matching manifest is a mismatch.
		return invalid("immutable", "immutable does not match the signed package")
	}
	return nil
}

func (s *Server) apiSetPrice(w http.ResponseWriter, r *http.Request) {
	a, ok := s.author(w, r)
	if !ok {
		return
	}
	var b struct {
		AmountCents *int64        `json:"amount_cents"`
		Currency    *string       `json:"currency"`
		Interval    *string       `json:"interval"`
		Note        *string       `json:"note"`
		SalesURL    *string       `json:"sales_url"`
		Price       *ListingPrice `json:"price"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	edit := ListingEdit{SalesURL: b.SalesURL}
	switch {
	case b.Price != nil:
		edit.Price = b.Price
	case b.AmountCents != nil || b.Currency != nil || b.Interval != nil || b.Note != nil:
		p := ListingPrice{}
		if b.AmountCents != nil {
			p.AmountCents = *b.AmountCents
		}
		if b.Currency != nil {
			p.Currency = *b.Currency
		}
		if b.Interval != nil {
			p.Interval = *b.Interval
		}
		if b.Note != nil {
			p.Note = *b.Note
		}
		edit.Price = &p
	}
	res, err := s.M.SetListingPrice(a, r.PathValue("listing_id"), edit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.listingBody(res.Listing))
}

func (s *Server) operator(r *http.Request) bool {
	t := bearer(r)
	if t == "" {
		return false
	}
	sum := sha256.Sum256([]byte(t))
	return hmac.Equal(sum[:], s.operatorHash[:])
}

func (s *Server) apiFlag(w http.ResponseWriter, r *http.Request) {
	if !s.operator(r) {
		writeErr(w, ErrUnauthorized)
		return
	}
	var b struct {
		Note    string `json:"note"`
		Version string `json:"version"`
	}
	if err := decode(r, s.maxJSON, &b); err != nil {
		writeErr(w, err)
		return
	}
	l, err := s.M.Flag(r.PathValue("listing_id"), strings.TrimSpace(b.Version), b.Note)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) apiPull(w http.ResponseWriter, r *http.Request) {
	if !s.operator(r) {
		writeErr(w, ErrUnauthorized)
		return
	}
	var b struct {
		Note string `json:"note"`
	}
	if r.ContentLength != int64(0) {
		if err := decode(r, s.maxJSON, &b); err != nil {
			writeErr(w, err)
			return
		}
	}
	l, err := s.M.Pull(r.PathValue("listing_id"), b.Note)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// --- Pages ---

func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var b bytes.Buffer
	if err := s.pages.ExecuteTemplate(&b, name, data); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}

func (s *Server) css(w http.ResponseWriter, _ *http.Request) {
	b, err := fs.ReadFile(assets, "static/site.css")
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(b)
}

type listingPage struct {
	L          shared.Listing
	Warning    string
	Notice     string
	Disclaimer string
	SalesURL   string
	PriceNote  string
}

func (s *Server) pageSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	s.render(w, http.StatusOK, "search.html", map[string]any{"Q": q, "Listings": s.M.Search(q), "Notice": shared.Notice, "FlagWarning": shared.FlagWarning})
}

func (s *Server) pageListing(w http.ResponseWriter, r *http.Request) {
	l, err := s.M.Listing(r.PathValue("listing_id"))
	if err != nil {
		s.render(w, http.StatusNotFound, "notfound.html", nil)
		return
	}
	sales, _ := s.M.ListingSalesURL(l.ID)
	price := l.PriceNote
	if price == "" {
		price = "Free"
	}
	s.render(w, http.StatusOK, "listing.html", listingPage{
		L: l, Warning: l.FlagText(), Notice: shared.Notice,
		Disclaimer: ProviderDisclaimer(l.Author), SalesURL: sales, PriceNote: price,
	})
}

const opCookie = "togavsu_marketplace_operator"

// session is the signed-in operator's browser session, if any.
func (s *Server) session(r *http.Request) (string, opSession, bool) {
	c, err := r.Cookie(opCookie)
	if err != nil || c.Value == "" {
		return "", opSession{}, false
	}
	key := hashToken(c.Value)
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.sessions[key]
	if !ok || !s.now().Before(o.expires) {
		delete(s.sessions, key)
		return "", opSession{}, false
	}
	return key, o, true
}

func (s *Server) pageOperator(w http.ResponseWriter, r *http.Request) {
	_, o, ok := s.session(r)
	if !ok {
		s.render(w, http.StatusOK, "operator_login.html", nil)
		return
	}
	s.render(w, http.StatusOK, "operator.html", map[string]any{"Listings": s.M.All(), "CSRF": o.csrf, "Done": r.URL.Query().Get("done")})
}

func (s *Server) formLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxJSON)
	sum := sha256.Sum256([]byte(strings.TrimSpace(r.PostFormValue("token"))))
	if !hmac.Equal(sum[:], s.operatorHash[:]) {
		s.render(w, http.StatusUnauthorized, "operator_login.html", map[string]any{"Failed": true})
		return
	}
	value, err := randomText()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	csrf, err := randomText()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	now := s.now()
	s.mu.Lock()
	for k, o := range s.sessions {
		if !now.Before(o.expires) {
			delete(s.sessions, k)
		}
	}
	s.sessions[hashToken(value)] = opSession{csrf: csrf, expires: now.Add(s.opLife)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: opCookie, Value: value, Path: "/operator", HttpOnly: true, Secure: s.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: int(s.opLife.Seconds())})
	http.Redirect(w, r, "/operator", http.StatusSeeOther)
}

// formOperator checks the session and the form's CSRF token.
func (s *Server) formOperator(w http.ResponseWriter, r *http.Request) (string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxJSON)
	key, o, ok := s.session(r)
	if !ok {
		http.Redirect(w, r, "/operator", http.StatusSeeOther)
		return "", false
	}
	if !hmac.Equal([]byte(r.PostFormValue("csrf")), []byte(o.csrf)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return "", false
	}
	return key, true
}

func (s *Server) formLogout(w http.ResponseWriter, r *http.Request) {
	key, ok := s.formOperator(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	delete(s.sessions, key)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: opCookie, Value: "", Path: "/operator", HttpOnly: true, Secure: s.secureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.Redirect(w, r, "/operator", http.StatusSeeOther)
}

func (s *Server) formFlag(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.formOperator(w, r); !ok {
		return
	}
	if _, err := s.M.Flag(r.PathValue("listing_id"), strings.TrimSpace(r.PostFormValue("version")), r.PostFormValue("note")); err != nil {
		s.formFailed(w, err)
		return
	}
	http.Redirect(w, r, "/operator?done=flagged", http.StatusSeeOther)
}

func (s *Server) formPull(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.formOperator(w, r); !ok {
		return
	}
	if _, err := s.M.Pull(r.PathValue("listing_id"), r.PostFormValue("note")); err != nil {
		s.formFailed(w, err)
		return
	}
	http.Redirect(w, r, "/operator?done=pulled", http.StatusSeeOther)
}

func (s *Server) formFailed(w http.ResponseWriter, err error) {
	var inv *InvalidError
	msg, status := "That listing or version does not exist.", http.StatusNotFound
	if errors.As(err, &inv) {
		msg, status = inv.Message, http.StatusBadRequest
	} else if !errors.Is(err, ErrNotFound) {
		msg, status = "The change could not be saved.", http.StatusInternalServerError
	}
	s.render(w, status, "operator_error.html", map[string]any{"Message": msg})
}

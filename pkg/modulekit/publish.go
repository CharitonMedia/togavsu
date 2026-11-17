package modulekit

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrListedConflict means that version is already on the marketplace with different bytes.
var ErrListedConflict = errors.New("modulekit: that version is already listed with different bytes")

// PublishRequest is one author's submission of signed package files to a marketplace.
type PublishRequest struct {
	// Market is the marketplace base URL (http://127.0.0.1:8090 or http://togavsu-marketplace:8090).
	Market string
	// Priv is the author's Ed25519 signing key.
	Priv ed25519.PrivateKey
	// Name and Email identify the author account (created when missing).
	Name, Email, Password string
	// Files are the signed package file contents (what package writes).
	Files [][]byte
	// HTTP, when set, is used instead of http.DefaultClient (tests pass an in-process client).
	HTTP *http.Client
}

// Published is one file's result: listed now, or already listed with the same digest.
type Published struct {
	ModuleID string
	Version  string
	Already  bool
}

// Publish creates or signs in the author, registers and proves the key when needed, and
// uploads each signed package file. A version already listed with the same digest counts
// as done (Already); a version listed with different bytes is ErrListedConflict.
func Publish(req PublishRequest) ([]Published, error) {
	if req.Market == "" || req.Priv == nil || req.Name == "" || req.Email == "" || req.Password == "" || len(req.Files) == 0 {
		return nil, errors.New("modulekit: publish needs -market, -key, -name, -email, a password and at least one signed file")
	}
	base, err := url.Parse(strings.TrimRight(req.Market, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("modulekit: bad marketplace URL %q", req.Market)
	}
	c := req.HTTP
	if c == nil {
		c = http.DefaultClient
	}
	cli := marketClient{base: base, http: c}
	tok, err := cli.ensureAuthor(req.Name, req.Email, req.Password)
	if err != nil {
		return nil, err
	}
	if err := cli.ensureKey(tok, req.Priv); err != nil {
		return nil, err
	}
	out := make([]Published, 0, len(req.Files))
	for _, file := range req.Files {
		p, err := cli.upload(tok, file)
		if err != nil {
			return out, err
		}
		out = append(out, p)
	}
	return out, nil
}

type marketClient struct {
	base *url.URL
	http *http.Client
}

func (c marketClient) url(path string) string {
	u := *c.base
	u.Path = strings.TrimRight(c.base.Path, "/") + path
	return u.String()
}

func (c marketClient) do(method, path, token string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.url(path), rdr)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, raw, err
}

func (c marketClient) ensureAuthor(name, email, password string) (string, error) {
	code, raw, err := c.do("POST", "/v1/authors", "", map[string]any{"name": name, "email": email, "password": password})
	if err != nil {
		return "", err
	}
	if code != http.StatusCreated && code != http.StatusConflict {
		return "", marketErr("register", code, raw)
	}
	code, raw, err = c.do("POST", "/v1/sessions", "", map[string]any{"email": email, "password": password})
	if err != nil {
		return "", err
	}
	if code != http.StatusCreated {
		return "", marketErr("sign in", code, raw)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil || tok.Token == "" {
		return "", errors.New("modulekit: marketplace sign-in returned no token")
	}
	return tok.Token, nil
}

func (c marketClient) ensureKey(tok string, priv ed25519.PrivateKey) error {
	pub := priv.Public().(ed25519.PublicKey)
	want := KeyID(pub)
	code, raw, err := c.do("GET", "/v1/authors/me/keys", tok, nil)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return marketErr("list keys", code, raw)
	}
	var list struct {
		Data []struct {
			KeyID        string `json:"key_id"`
			Verified     bool   `json:"verified"`
			ProofMessage string `json:"proof_message"`
			Challenge    string `json:"challenge"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return err
	}
	for _, k := range list.Data {
		if k.KeyID == want && k.Verified {
			return nil
		}
	}
	code, raw, err = c.do("POST", "/v1/authors/me/keys", tok, map[string]any{"public_key_pem": PublicKeyPEM(pub)})
	if err != nil {
		return err
	}
	if code != http.StatusCreated {
		return marketErr("register key", code, raw)
	}
	var k struct {
		KeyID        string `json:"key_id"`
		Verified     bool   `json:"verified"`
		ProofMessage string `json:"proof_message"`
	}
	if err := json.Unmarshal(raw, &k); err != nil {
		return err
	}
	if k.Verified {
		return nil
	}
	sig, err := ProveKey(priv, k.ProofMessage)
	if err != nil {
		return err
	}
	code, raw, err = c.do("POST", "/v1/authors/me/keys/"+k.KeyID+"/verify", tok, map[string]any{"signature": sig})
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return marketErr("prove key", code, raw)
	}
	return nil
}

func (c marketClient) upload(tok string, file []byte) (Published, error) {
	sp, pkg, err := ReadSignedFile(file)
	if err != nil {
		return Published{}, err
	}
	// Post the kit file as-is (plus nothing else): known fields only.
	var body map[string]any
	if err := json.Unmarshal(file, &body); err != nil {
		return Published{}, err
	}
	code, raw, err := c.do("POST", "/v1/authors/me/packages", tok, body)
	if err != nil {
		return Published{}, err
	}
	id, ver := sp.ListingID, sp.Version
	if id == "" || ver == "" {
		if m, err := ReadManifest(pkg); err == nil {
			id, ver = m.ID, m.Version
		}
	}
	switch code {
	case http.StatusCreated:
		return Published{ModuleID: id, Version: ver}, nil
	case http.StatusConflict:
		listed, err := c.listedDigest(id, ver)
		if err != nil {
			return Published{}, err
		}
		if listed == Digest(pkg) {
			return Published{ModuleID: id, Version: ver, Already: true}, nil
		}
		return Published{}, fmt.Errorf("%w: %s %s", ErrListedConflict, id, ver)
	default:
		return Published{}, marketErr("upload "+id+" "+ver, code, raw)
	}
}

func (c marketClient) listedDigest(id, version string) (string, error) {
	code, raw, err := c.do("GET", "/v1/listings/"+url.PathEscape(id)+"/versions/"+url.PathEscape(version)+"/package", "", nil)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", marketErr("fetch listed package", code, raw)
	}
	var sp SignedPackage
	if err := json.Unmarshal(raw, &sp); err != nil {
		return "", err
	}
	return sp.Digest, nil
}

func marketErr(op string, code int, raw []byte) error {
	var p struct {
		Detail string `json:"detail"`
		Code   string `json:"code"`
	}
	_ = json.Unmarshal(raw, &p)
	if p.Detail != "" {
		return fmt.Errorf("modulekit: marketplace %s: %s (%s)", op, p.Detail, p.Code)
	}
	return fmt.Errorf("modulekit: marketplace %s: HTTP %d", op, code)
}

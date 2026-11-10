package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Dropbox stores objects in the firm's Dropbox through an app-folder app, so every
// path is under Apps/Togavsu. It uses the HTTP API v2 and renews the access token with
// the refresh token when Dropbox answers 401.
type Dropbox struct {
	APIBase     string
	ContentBase string
	TokenURL    string
	Prefix      string
	Client      *http.Client

	refresh, appKey, appSecret string

	mu    sync.Mutex
	token string
}

// dropboxChunk is the upload-session chunk. Dropbox accepts at most 150 MiB per call.
const dropboxChunk = 1 << 23

// NewDropbox validates the settings.
func NewDropbox(s Settings, client *http.Client) (*Dropbox, error) {
	if s.AccessToken == "" && (s.RefreshToken == "" || s.AppKey == "") {
		return nil, fmt.Errorf("%w: dropbox needs an access_token or a refresh_token with app_key", ErrConfig)
	}
	if client == nil {
		client = http.DefaultClient
	}
	d := &Dropbox{
		APIBase: s.APIBase, ContentBase: s.ContentBase, TokenURL: s.TokenURL, Prefix: strings.Trim(s.Prefix, "/"),
		Client: client, refresh: s.RefreshToken, appKey: s.AppKey, appSecret: s.AppSecret, token: s.AccessToken,
	}
	if d.APIBase == "" {
		d.APIBase = "https://api.dropboxapi.com"
	}
	if d.ContentBase == "" {
		d.ContentBase = "https://content.dropboxapi.com"
	}
	if d.TokenURL == "" {
		d.TokenURL = d.APIBase + "/oauth2/token"
	}
	return d, nil
}

func (d *Dropbox) path(key string) (string, error) {
	if err := CheckKey(key); err != nil {
		return "", err
	}
	if d.Prefix == "" {
		return "/" + key, nil
	}
	return "/" + d.Prefix + "/" + key, nil
}

func (d *Dropbox) accessToken(ctx context.Context, renew bool) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.token != "" && !renew {
		return d.token, nil
	}
	if d.refresh == "" {
		return "", fmt.Errorf("storage: dropbox token rejected and no refresh token is set")
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {d.refresh}, "client_id": {d.appKey}}
	if d.appSecret != "" {
		form.Set("client_secret", d.appSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := d.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("storage: dropbox token refresh: %s", resp.Status)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("storage: dropbox token refresh returned no token")
	}
	d.token = out.AccessToken
	return d.token, nil
}

// call sends one request, renewing the token once on 401. body is rebuilt for the retry.
func (d *Dropbox) call(ctx context.Context, base, endpoint string, arg any, body func() io.Reader, hdr http.Header) (*http.Response, error) {
	for renewed := false; ; renewed = true {
		token, err := d.accessToken(ctx, renewed)
		if err != nil {
			return nil, err
		}
		var rd io.Reader
		if body != nil {
			rd = body()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+endpoint, rd)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		for k, vs := range hdr {
			req.Header[k] = vs
		}
		if base == d.ContentBase {
			raw, err := json.Marshal(arg)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Dropbox-API-Arg", string(raw))
			req.Header.Set("Content-Type", "application/octet-stream")
		} else {
			raw, err := json.Marshal(arg)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(bytes.NewReader(raw))
			req.ContentLength = int64(len(raw))
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := d.Client.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && !renewed && d.refresh != "" {
			drain(resp)
			continue
		}
		return resp, nil
	}
}

type dropboxErr struct {
	Summary string `json:"error_summary"`
}

func dropboxError(resp *http.Response) (string, error) {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<12))
	var e dropboxErr
	_ = json.Unmarshal(raw, &e)
	return e.Summary, fmt.Errorf("storage: dropbox %s: %s", resp.Status, strings.TrimSpace(string(raw)))
}

// Put uploads in one call when the object fits in a chunk, and through an upload
// session otherwise. Mode add with strict_conflict refuses an existing path.
func (d *Dropbox) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	commit := map[string]any{"path": p, "mode": "add", "autorename": false, "strict_conflict": true, "mute": true}
	if size >= zero && size <= dropboxChunk {
		buf, err := io.ReadAll(io.LimitReader(r, size+1))
		if err != nil {
			return err
		}
		if int64(len(buf)) != size {
			return fmt.Errorf("storage: read %d bytes, expected %d", len(buf), size)
		}
		resp, err := d.call(ctx, d.ContentBase, "/2/files/upload", commit, func() io.Reader { return bytes.NewReader(buf) }, nil)
		if err != nil {
			return err
		}
		defer drain(resp)
		return d.uploadResult(resp)
	}
	return d.putSession(ctx, r, commit)
}

func (d *Dropbox) uploadResult(resp *http.Response) error {
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	summary, err := dropboxError(resp)
	if resp.StatusCode == http.StatusConflict && strings.Contains(summary, "conflict") {
		return ErrExists
	}
	return err
}

func (d *Dropbox) putSession(ctx context.Context, r io.Reader, commit map[string]any) error {
	buf := make([]byte, dropboxChunk)
	var session string
	var offset int64
	for {
		n, rerr := io.ReadFull(r, buf)
		end := rerr == io.EOF || rerr == io.ErrUnexpectedEOF
		if rerr != nil && !end {
			return rerr
		}
		if n > int(zero) || session == "" {
			// Each request gets its own copy: the transport may still be reading a
			// body (a 401 retry, an early response) when the next chunk is read.
			chunk := append([]byte(nil), buf[:n]...)
			body := func() io.Reader { return bytes.NewReader(chunk) }
			var resp *http.Response
			var err error
			if session == "" {
				resp, err = d.call(ctx, d.ContentBase, "/2/files/upload_session/start", map[string]any{"close": false}, body, nil)
			} else {
				resp, err = d.call(ctx, d.ContentBase, "/2/files/upload_session/append_v2",
					map[string]any{"cursor": map[string]any{"session_id": session, "offset": offset}, "close": false}, body, nil)
			}
			if err != nil {
				return err
			}
			if resp.StatusCode != http.StatusOK {
				_, err = dropboxError(resp)
				drain(resp)
				return err
			}
			if session == "" {
				var out struct {
					SessionID string `json:"session_id"`
				}
				err = json.NewDecoder(resp.Body).Decode(&out)
				session = out.SessionID
				if err == nil && session == "" {
					err = fmt.Errorf("storage: dropbox returned no upload session")
				}
			}
			drain(resp)
			if err != nil {
				return err
			}
			offset += int64(n)
		}
		if end {
			break
		}
	}
	arg := map[string]any{"cursor": map[string]any{"session_id": session, "offset": offset}, "commit": commit}
	resp, err := d.call(ctx, d.ContentBase, "/2/files/upload_session/finish", arg, func() io.Reader { return bytes.NewReader(nil) }, nil)
	if err != nil {
		return err
	}
	defer drain(resp)
	return d.uploadResult(resp)
}

// Get downloads with a Range header.
func (d *Dropbox) Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	if length == zero {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	hdr := http.Header{}
	if rg := rangeHeader(offset, length); rg != "" {
		hdr.Set("Range", rg)
	}
	resp, err := d.call(ctx, d.ContentBase, "/2/files/download", map[string]string{"path": p}, nil, hdr)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		return resp.Body, nil
	case http.StatusConflict:
		defer drain(resp)
		summary, err := dropboxError(resp)
		if strings.Contains(summary, "not_found") {
			return nil, ErrNotFound
		}
		return nil, err
	default:
		defer drain(resp)
		_, err := dropboxError(resp)
		return nil, err
	}
}

// Stat reads the file metadata.
func (d *Dropbox) Stat(ctx context.Context, key string) (Info, error) {
	p, err := d.path(key)
	if err != nil {
		return Info{}, err
	}
	resp, err := d.call(ctx, d.APIBase, "/2/files/get_metadata", map[string]string{"path": p}, nil, nil)
	if err != nil {
		return Info{}, err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusConflict {
		summary, err := dropboxError(resp)
		if strings.Contains(summary, "not_found") {
			return Info{}, ErrNotFound
		}
		return Info{}, err
	}
	if resp.StatusCode != http.StatusOK {
		_, err := dropboxError(resp)
		return Info{}, err
	}
	var meta struct {
		Tag  string `json:".tag"`
		Size int64  `json:"size"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return Info{}, err
	}
	return Info{Size: meta.Size}, nil
}

// Delete removes the file. A missing path is not an error.
func (d *Dropbox) Delete(ctx context.Context, key string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	resp, err := d.call(ctx, d.APIBase, "/2/files/delete_v2", map[string]string{"path": p}, nil, nil)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	summary, err := dropboxError(resp)
	if resp.StatusCode == http.StatusConflict && strings.Contains(summary, "not_found") {
		return nil
	}
	return err
}

// Health asks for the connected account.
func (d *Dropbox) Health(ctx context.Context) error {
	resp, err := d.call(ctx, d.APIBase, "/2/users/get_current_account", nil, nil, nil)
	if err != nil {
		return err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		_, err := dropboxError(resp)
		return err
	}
	return nil
}

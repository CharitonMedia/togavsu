// Package storagetest serves in-memory S3-compatible and Dropbox endpoints for tests.
// They implement the requests the storage adapters send, check credentials, and let a
// test change one stored byte to prove reads fail closed.
package storagetest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CharitonMedia/togavsu/internal/storage"
)

// Objects is a locked map of stored bytes.
type Objects struct {
	mu   sync.Mutex
	data map[string][]byte
	// Requests counts calls by "METHOD path" or Dropbox endpoint.
	Requests map[string]int
}

func newObjects() *Objects {
	return &Objects{data: map[string][]byte{}, Requests: map[string]int{}}
}

// Keys lists stored names in order.
func (o *Objects) Keys() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, 0, len(o.data))
	for k := range o.data {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Get returns a copy of one stored object, or nil.
func (o *Objects) Get(name string) []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	if v, ok := o.data[name]; ok {
		return append([]byte(nil), v...)
	}
	return nil
}

// Len is the number of stored objects.
func (o *Objects) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.data)
}

// Flip changes the last byte of every object whose name ends with suffix.
func (o *Objects) Flip(suffix string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	var n int
	for k, v := range o.data {
		if strings.HasSuffix(k, suffix) && len(v) > 0 {
			v[len(v)-1] ^= ^byte(0)
			n++
		}
	}
	return n
}

// Wipe removes every object, as a lost backend would.
func (o *Objects) Wipe() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.data = map[string][]byte{}
}

func (o *Objects) count(name string) {
	o.mu.Lock()
	o.Requests[name]++
	o.mu.Unlock()
}

// S3 is a path-style S3 endpoint for one bucket.
type S3 struct {
	*Objects
	// Unavailable makes object reads answer 503 while it is set, the way a backend
	// having a bad moment does.
	Unavailable atomic.Bool
	Server      *httptest.Server
	Bucket      string
	AccessKey   string
	SecretKey   string
}

// NewS3 starts the endpoint. Close the Server when done.
func NewS3(bucket string) *S3 {
	s := &S3{Objects: newObjects(), Bucket: bucket, AccessKey: "AKIDTESTFAKE", SecretKey: "fake-secret-key"}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Settings returns adapter settings that point at this endpoint.
func (s *S3) Settings() storage.Settings {
	return storage.Settings{Endpoint: s.Server.URL, Bucket: s.Bucket, Region: "us-east-1", AccessKey: s.AccessKey, SecretKey: s.SecretKey}
}

func (s *S3) authorized(r *http.Request) bool {
	got := r.Header.Get("Authorization")
	if !strings.HasPrefix(got, "AWS4-HMAC-SHA256 Credential="+s.AccessKey+"/") {
		return false
	}
	when, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return false
	}
	check, _ := http.NewRequest(r.Method, "http://"+r.Host+r.URL.RequestURI(), nil)
	for _, h := range []string{"Range", "If-None-Match"} {
		if v := r.Header.Get(h); v != "" {
			check.Header.Set(h, v)
		}
	}
	storage.Sign(check, s.AccessKey, s.SecretKey, "us-east-1", "s3", r.Header.Get("X-Amz-Content-Sha256"), when)
	return check.Header.Get("Authorization") == got
}

func (s *S3) serve(w http.ResponseWriter, r *http.Request) {
	s.count(r.Method + " " + r.URL.Path)
	if !s.authorized(r) {
		http.Error(w, "<Error><Code>SignatureDoesNotMatch</Code></Error>", http.StatusForbidden)
		return
	}
	if s.Unavailable.Load() && r.Method == http.MethodGet {
		http.Error(w, "<Error><Code>ServiceUnavailable</Code></Error>", http.StatusServiceUnavailable)
		return
	}
	prefix := "/" + s.Bucket
	if r.URL.Path == prefix || r.URL.Path == prefix+"/" {
		w.WriteHeader(http.StatusOK)
		return
	}
	key, ok := strings.CutPrefix(r.URL.Path, prefix+"/")
	if !ok {
		http.Error(w, "<Error><Code>NoSuchBucket</Code></Error>", http.StatusNotFound)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, found := s.data[key]
	switch r.Method {
	case http.MethodPut:
		if found && r.Header.Get("If-None-Match") == "*" {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || int64(len(body)) != r.ContentLength {
			http.Error(w, "short body", http.StatusBadRequest)
			return
		}
		s.data[key] = body
		w.WriteHeader(http.StatusOK)
	case http.MethodHead:
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		if !found {
			http.Error(w, "<Error><Code>NoSuchKey</Code></Error>", http.StatusNotFound)
			return
		}
		serveRange(w, r, data)
	case http.MethodDelete:
		delete(s.data, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func serveRange(w http.ResponseWriter, r *http.Request, data []byte) {
	rg := r.Header.Get("Range")
	if rg == "" {
		_, _ = w.Write(data)
		return
	}
	spec, _ := strings.CutPrefix(rg, "bytes=")
	a, b, _ := strings.Cut(spec, "-")
	start, err := strconv.ParseInt(a, 10, 64)
	if err != nil || start > int64(len(data)) {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	end := int64(len(data))
	if b != "" {
		e, err := strconv.ParseInt(b, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if e+1 < end {
			end = e + 1
		}
	}
	w.WriteHeader(http.StatusPartialContent)
	_, _ = w.Write(data[start:end])
}

// Dropbox is the subset of the Dropbox HTTP API v2 the adapter uses, on one server
// for both the API and content hosts.
type Dropbox struct {
	*Objects
	Server       *httptest.Server
	Token        string
	RefreshToken string
	sessions     map[string]*bytes.Buffer
	next         int
}

// NewDropbox starts the endpoint. The first access token is expired, so the adapter
// must renew it with the refresh token before any call succeeds.
func NewDropbox() *Dropbox {
	d := &Dropbox{Objects: newObjects(), Token: "token-1", RefreshToken: "refresh-test", sessions: map[string]*bytes.Buffer{}}
	d.Server = httptest.NewServer(http.HandlerFunc(d.serve))
	return d
}

// Settings returns adapter settings that point at this endpoint.
func (d *Dropbox) Settings() storage.Settings {
	return storage.Settings{
		AccessToken: "expired", RefreshToken: d.RefreshToken, AppKey: "app", AppSecret: "secret",
		APIBase: d.Server.URL, ContentBase: d.Server.URL,
	}
}

func (d *Dropbox) conflict(w http.ResponseWriter, summary string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]string{"error_summary": summary})
}

func (d *Dropbox) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/oauth2/token" {
		d.count(r.URL.Path)
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != d.RefreshToken || r.Form.Get("grant_type") != "refresh_token" {
			http.Error(w, "bad refresh", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"access_token": d.Token, "token_type": "bearer"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+d.Token {
		http.Error(w, "expired_access_token", http.StatusUnauthorized)
		return
	}
	d.count(r.URL.Path)
	var arg struct {
		Path   string `json:"path"`
		Cursor struct {
			SessionID string `json:"session_id"`
			Offset    int64  `json:"offset"`
		} `json:"cursor"`
		Commit struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
		} `json:"commit"`
		Mode string `json:"mode"`
	}
	raw := r.Header.Get("Dropbox-API-Arg")
	var body []byte
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &arg)
		body, _ = io.ReadAll(r.Body)
	} else {
		body, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &arg)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	switch r.URL.Path {
	case "/2/users/get_current_account":
		_ = json.NewEncoder(w).Encode(map[string]string{"account_id": "dbid:test"})
	case "/2/files/upload":
		if _, ok := d.data[arg.Path]; ok {
			d.conflict(w, "path/conflict/file/..")
			return
		}
		d.data[arg.Path] = body
		_ = json.NewEncoder(w).Encode(map[string]any{"path_display": arg.Path, "size": len(body)})
	case "/2/files/upload_session/start":
		d.next++
		id := "session-" + strconv.Itoa(d.next)
		d.sessions[id] = bytes.NewBuffer(body)
		_ = json.NewEncoder(w).Encode(map[string]string{"session_id": id})
	case "/2/files/upload_session/append_v2", "/2/files/upload_session/finish":
		buf, ok := d.sessions[arg.Cursor.SessionID]
		if !ok || int64(buf.Len()) != arg.Cursor.Offset {
			d.conflict(w, "incorrect_offset/..")
			return
		}
		buf.Write(body)
		if r.URL.Path == "/2/files/upload_session/finish" {
			if _, exists := d.data[arg.Commit.Path]; exists {
				d.conflict(w, "path/conflict/file/..")
				return
			}
			d.data[arg.Commit.Path] = buf.Bytes()
			delete(d.sessions, arg.Cursor.SessionID)
			_ = json.NewEncoder(w).Encode(map[string]any{"path_display": arg.Commit.Path})
			return
		}
		_, _ = w.Write([]byte("null"))
	case "/2/files/download":
		data, ok := d.data[arg.Path]
		if !ok {
			d.conflict(w, "path/not_found/..")
			return
		}
		serveRange(w, r, data)
	case "/2/files/get_metadata":
		data, ok := d.data[arg.Path]
		if !ok {
			d.conflict(w, "path/not_found/..")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{".tag": "file", "size": len(data)})
	case "/2/files/delete_v2":
		if _, ok := d.data[arg.Path]; !ok {
			d.conflict(w, "path_lookup/not_found/..")
			return
		}
		delete(d.data, arg.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]string{"path_display": arg.Path}})
	default:
		http.Error(w, "unknown endpoint", http.StatusNotFound)
	}
}

package supervisor

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestS3RoundTripAndSignature(t *testing.T) {
	var auth string
	store := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
			http.Error(w, "unsigned", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			store[r.URL.Path] = body
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			if _, ok := store[r.URL.Path]; !ok && !strings.Contains(r.URL.RawQuery, "list-type=2") {
				http.NotFound(w, r)
				return
			}
			if strings.Contains(r.URL.RawQuery, "list-type=2") {
				w.Header().Set("Content-Type", "application/xml")
				_, _ = io.WriteString(w, `<ListBucketResult><Contents><Key>firm/obj.bin</Key><LastModified>2026-03-01T00:00:00Z</LastModified></Contents></ListBucketResult>`)
				return
			}
			_, _ = w.Write(store[r.URL.Path])
		case http.MethodDelete:
			delete(store, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method", 405)
		}
	}))
	defer srv.Close()
	dest := &S3Destination{
		Spec: DestinationSpec{
			ID: "obj", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1",
			Bucket: "backups", Prefix: "firm", AccessKey: "AKID", SecretKey: "secret",
		},
		HTTP: srv.Client(),
		Now:  func() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) },
	}
	body := []byte("synthetic-archive")
	sum, err := sha256Hex(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := dest.Put(context.Background(), "obj.bin", bytes.NewReader(body), sum); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(auth, "Credential=AKID/20260301/us-east-1/s3/aws4_request") {
		t.Fatalf("auth %s", auth)
	}
	rc, err := dest.Get(context.Background(), "obj.bin")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, body) {
		t.Fatalf("got %q", got)
	}
	objs, err := dest.List(context.Background())
	if err != nil || len(objs) != 1 || objs[0].Name != "obj.bin" {
		t.Fatalf("list %+v %v", objs, err)
	}
	if err := dest.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestS3FileUploadSetsContentLength(t *testing.T) {
	var length int64
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method", 405)
			return
		}
		if r.ContentLength <= 0 || r.TransferEncoding != nil && len(r.TransferEncoding) > 0 {
			http.Error(w, "chunked", http.StatusBadRequest)
			return
		}
		length = r.ContentLength
		got, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	f, err := os.CreateTemp(t.TempDir(), "archive")
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("synthetic-archive-from-file")
	if _, err := f.Write(body); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	sum, err := sha256Hex(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	dest := &S3Destination{
		Spec: DestinationSpec{
			ID: "obj", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1",
			Bucket: "backups", AccessKey: "AKID", SecretKey: "secret",
		},
		HTTP: srv.Client(),
		Now:  func() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) },
	}
	if err := dest.Put(context.Background(), "obj.bin", f, sum); err != nil {
		t.Fatal(err)
	}
	if length != int64(len(body)) || !bytes.Equal(got, body) {
		t.Fatalf("length %d body %q", length, got)
	}
}

func TestSignS3KnownRequest(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "examplebucket.s3.amazonaws.com"
	req.Header.Set("Host", req.Host)
	req.Header.Set("x-amz-date", "20130524T000000Z")
	req.Header.Set("x-amz-content-sha256", emptySHA256)
	req.Header.Set("Range", "bytes=0-9")
	signS3(req, emptySHA256, "AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", "s3", "20130524T000000Z")
	// Range is intentionally unsigned. The signed header set is host,
	// x-amz-content-sha256, and x-amz-date. The signature matches an
	// independent HMAC-SHA256 implementation of this canonical request.
	const want = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=df548e2ce037944d03f3e68682813b093763996d597cf890ca3d9037fd231eb4"
	if req.Header.Get("Authorization") != want {
		t.Fatalf("authorization %s", req.Header.Get("Authorization"))
	}
}

func TestS3CanonicalPathEncoding(t *testing.T) {
	// Expected strings are written from the SigV4 unreserved set, not from
	// canonicalPath. "/" separators, empty segments, "." and ".." stay.
	cases := []struct{ raw, want string }{
		{"/test.txt", "/test.txt"},
		{"/test$file.text", "/test%24file.text"},
		{"/bucket/a+b", "/bucket/a%2Bb"},
		{"/bucket/a:b", "/bucket/a%3Ab"},
		{"/bucket/a b", "/bucket/a%20b"},
		{"/bucket/100%", "/bucket/100%25"},
		{"/bucket/a?b", "/bucket/a%3Fb"},
		{"/bucket/a#b", "/bucket/a%23b"},
		{"/bucket/a*b", "/bucket/a%2Ab"},
		{"/bucket/a/b", "/bucket/a/b"},
		{"/bucket/a//b/../c/./d", "/bucket/a//b/../c/./d"},
		{"/bucket/caf\u00e9", "/bucket/caf%C3%A9"},
		{"/bucket/~ok-._", "/bucket/~ok-._"},
		{"/bucket/a%2Bb", "/bucket/a%252Bb"},
		{"", "/"},
	}
	for _, tc := range cases {
		if got := canonicalPath(tc.raw); got != tc.want {
			t.Fatalf("canonicalPath(%q) = %q, want %q", tc.raw, got, tc.want)
		}
		decoded, err := url.PathUnescape(tc.want)
		if err != nil {
			t.Fatalf("PathUnescape(%q): %v", tc.want, err)
		}
		stored := tc.raw
		if stored == "" {
			stored = "/"
		}
		if decoded != stored {
			t.Fatalf("PathUnescape(%q) = %q, want %q", tc.want, decoded, stored)
		}
		u := &url.URL{Path: stored, RawPath: tc.want}
		if u.EscapedPath() != tc.want {
			t.Fatalf("EscapedPath(%q) = %q, want %q", tc.raw, u.EscapedPath(), tc.want)
		}
	}
}

func TestS3CanonicalQueryEncoding(t *testing.T) {
	cases := []struct {
		values url.Values
		want   string
	}{
		{url.Values{"lifecycle": {""}}, "lifecycle="},
		{url.Values{"max-keys": {"2"}, "prefix": {"J"}}, "max-keys=2&prefix=J"},
		{url.Values{"prefix": {"a b"}}, "prefix=a%20b"},
		{url.Values{"k+v": {"a+b"}}, "k%2Bv=a%2Bb"},
		{url.Values{"prefix": {"a:b"}}, "prefix=a%3Ab"},
		{url.Values{"prefix": {"*"}}, "prefix=%2A"},
		{url.Values{"prefix": {"?"}}, "prefix=%3F"},
		{url.Values{"prefix": {"#"}}, "prefix=%23"},
		{url.Values{"prefix": {"100%"}}, "prefix=100%25"},
		{url.Values{"prefix": {"a/b"}}, "prefix=a%2Fb"},
		{url.Values{"prefix": {"caf\u00e9"}}, "prefix=caf%C3%A9"},
		{url.Values{"prefix": {"b", "a"}}, "prefix=a&prefix=b"},
		{url.Values{"marker": {"caf\u00e9"}, "prefix": {"a+b:c d*e?f#g%h/i"}}, "marker=caf%C3%A9&prefix=a%2Bb%3Ac%20d%2Ae%3Ff%23g%25h%2Fi"},
	}
	for _, tc := range cases {
		got := canonicalQuery(tc.values)
		if got != tc.want {
			t.Fatalf("canonicalQuery(%v) = %q, want %q", tc.values, got, tc.want)
		}
		if strings.Contains(got, "+") {
			t.Fatalf("query encoded space or plus as '+': %s", got)
		}
	}
}

func TestS3ObjectKeyPreservesRawPrefix(t *testing.T) {
	dest := &S3Destination{Spec: DestinationSpec{Prefix: "/a//b/../c/"}}
	if got := dest.objectKey("d+e:f"); got != "a//b/../c/d+e:f" {
		t.Fatalf("objectKey %q", got)
	}
}

// sigV4Signature is an independent HMAC-SHA256 SigV4 implementation.
// It does not call signS3, canonicalPath, or awsEscape.
func TestAdvancingClockDoesNotFinishAnUncertainPut(t *testing.T) {
	old := destinationIOTimeout
	destinationIOTimeout = 200 * time.Millisecond
	t.Cleanup(func() { destinationIOTimeout = old })
	var mu sync.Mutex
	stored := map[string][]byte{}
	var puts int
	hold := make(chan struct{})
	gotBody := make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(hold) }) }
	t.Cleanup(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			if r.Method == http.MethodGet {
				mu.Lock()
				body, ok := stored[r.URL.Path]
				mu.Unlock()
				if !ok {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write(body)
				return
			}
			if r.Method == http.MethodDelete {
				mu.Lock()
				delete(stored, r.URL.Path)
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		puts++
		mu.Unlock()
		once.Do(func() { close(gotBody) })
		<-hold
		mu.Lock()
		stored[r.URL.Path] = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	var clock atomic.Int64
	clock.Store(time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC).UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	s := testStore(t)
	s.Now = now
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{
		ID: "share", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1",
		Bucket: "backups", AccessKey: "AKID", SecretKey: "secret", RetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(spec DestinationSpec) (Destination, error) {
		return &S3Destination{Spec: spec, HTTP: &http.Client{Timeout: destinationIOTimeout}, Now: now}, nil
	}
	done := make(chan BackupMeta, 1)
	go func() {
		meta, err := s.Create(context.Background(), reasonManual)
		if err != nil {
			t.Errorf("create: %v", err)
		}
		done <- meta
	}()
	select {
	case <-gotBody:
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not start")
	}
	clock.Add(int64(2 * destinationIOTimeout))
	meta := <-done
	if len(meta.DestinationErrors) != 1 {
		t.Fatalf("errors = %v", meta.DestinationErrors)
	}
	owned, err := s.receiptBook().List("share")
	if err != nil || len(owned) != 1 || owned[0].State != receiptUncertain || owned[0].UncertainAt.Before(owned[0].Created.Add(destinationIOTimeout)) {
		t.Fatalf("uncertainty was tied to the backup clock: %+v %v", owned, err)
	}
	ops := &Operations{Store: s, Gate: &Gate{}, Passphrase: func() (string, error) { return s.Passphrase, nil }}
	req := httptest.NewRequest(http.MethodDelete, "/v1/backups/"+meta.ID, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	NewMux("secret", ops, nil).ServeHTTP(rec, req)
	if rec.Code == http.StatusNoContent {
		t.Fatal("clock past the upload timeout reported deletion")
	}
	clock.Add(int64(2 * destinationIOTimeout))
	rec = httptest.NewRecorder()
	NewMux("secret", ops, nil).ServeHTTP(rec, req)
	if rec.Code == http.StatusNoContent {
		t.Fatal("a second timeout of wall clock reported deletion")
	}
	if owned, err = s.receiptBook().List("share"); err != nil || len(owned) != 1 {
		t.Fatalf("tombstone lost: %+v %v", owned, err)
	}
	release()
	time.Sleep(50 * time.Millisecond)
	specs, err := s.Book.Specs()
	if err != nil {
		t.Fatal(err)
	}
	dest, err := s.openDest(specs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	left := len(stored)
	count := puts
	mu.Unlock()
	if left != 0 || count != 1 {
		t.Fatalf("stored %d puts %d", left, count)
	}
}

func TestLateS3PutKeepsOwnershipUntilTheBytesAreGone(t *testing.T) {
	var mu sync.Mutex
	stored := map[string][]byte{"/backups/other-firm/keep": []byte("keep")}
	var puts int
	hold := make(chan struct{})
	gotBody := make(chan struct{})
	committed := make(chan struct{})
	var bodyOnce sync.Once
	var commitOnce sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(hold) }) }
	t.Cleanup(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			puts++
			mu.Unlock()
			bodyOnce.Do(func() { close(gotBody) })
			<-hold
			mu.Lock()
			stored[r.URL.Path] = body
			mu.Unlock()
			commitOnce.Do(func() { close(committed) })
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			mu.Lock()
			body, ok := stored[r.URL.Path]
			mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
		case http.MethodDelete:
			mu.Lock()
			delete(stored, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	clock := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	s := testStore(t)
	s.Now = func() time.Time { return clock }
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{
		ID: "share", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1",
		Bucket: "backups", AccessKey: "AKID", SecretKey: "secret", RetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(spec DestinationSpec) (Destination, error) {
		return &S3Destination{
			Spec: spec,
			HTTP: &http.Client{Timeout: 200 * time.Millisecond},
			Now:  func() time.Time { return clock },
		}, nil
	}
	type result struct {
		meta BackupMeta
		err  error
	}
	done := make(chan result, 1)
	go func() {
		meta, err := s.Create(context.Background(), reasonManual)
		done <- result{meta, err}
	}()
	select {
	case <-gotBody:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not accept the upload")
	}
	var created result
	select {
	case created = <-done:
		if created.err != nil || len(created.meta.DestinationErrors) != 1 || !strings.Contains(created.meta.DestinationErrors[0], "uncertain") {
			t.Fatalf("%+v %v", created.meta.DestinationErrors, created.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("upload wait did not end while the parent context was live")
	}
	owned, err := s.receiptBook().List("share")
	if err != nil || len(owned) != 1 || owned[0].State != receiptUncertain {
		t.Fatalf("tombstone = %+v %v", owned, err)
	}
	ops := &Operations{Store: s, Gate: &Gate{}, Passphrase: func() (string, error) { return s.Passphrase, nil }}
	req := httptest.NewRequest(http.MethodDelete, "/v1/backups/"+created.meta.ID, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	NewMux("secret", ops, nil).ServeHTTP(rec, req)
	if rec.Code == http.StatusNoContent {
		t.Fatal("delete reported success before the upload could publish")
	}
	if kept, err := s.lookup(created.meta.ID); err != nil || !kept.Deleting {
		t.Fatalf("deleting record = %+v %v", kept, err)
	}
	if owned, err = s.receiptBook().List("share"); err != nil || len(owned) != 1 {
		t.Fatalf("ownership removed while the upload could still publish: %+v %v", owned, err)
	}
	release()
	select {
	case <-committed:
	case <-time.After(3 * time.Second):
		t.Fatal("late upload did not commit")
	}
	specs, err := s.Book.Specs()
	if err != nil || len(specs) != 1 {
		t.Fatal(specs, err)
	}
	dest, err := s.openDest(specs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	_, late := stored["/backups/"+specs[0].Namespace+"/"+created.meta.ID+".tgbk"]
	other := string(stored["/backups/other-firm/keep"])
	putCount := puts
	mu.Unlock()
	if late {
		t.Fatal("late archive was left in place")
	}
	if other != "keep" {
		t.Fatalf("other firm data = %q", other)
	}
	if putCount != 1 {
		t.Fatalf("uploads = %d", putCount)
	}
	if owned, err = s.receiptBook().List("share"); err != nil || len(owned) != 0 {
		t.Fatalf("observed late object did not clear its tombstone: %+v %v", owned, err)
	}
	rec = httptest.NewRecorder()
	NewMux("secret", ops, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("settled delete status %d %s", rec.Code, rec.Body.String())
	}
	if _, err := s.lookup(created.meta.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("local archive remained: %v", err)
	}
	mu.Lock()
	putCount = puts
	other = string(stored["/backups/other-firm/keep"])
	mu.Unlock()
	if putCount != 1 || other != "keep" {
		t.Fatalf("puts %d other %q", putCount, other)
	}
}

func sigV4Signature(canonical, secret, amz, region, service string) string {
	date := amz[:8]
	scope := date + "/" + region + "/" + service + "/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	stringToSign := "AWS4-HMAC-SHA256\n" + amz + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	key := []byte("AWS4" + secret)
	for _, part := range []string{date, region, service, "aws4_request"} {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(part))
		key = mac.Sum(nil)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(stringToSign))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestSignS3PublishedVector(t *testing.T) {
	// Source: https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sig-v4-header-based-auth.html
	// (the page formerly at AmazonS3/latest/API/sig-v4-header-based-auth.html).
	// Example access key AKIAIOSFODNN7EXAMPLE, secret wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY.
	// GET Object signs Range, which this signer does not sign. The canonical
	// URI and the HMAC of that published canonical request are still checked.
	// GET Bucket lifecycle signs host, x-amz-content-sha256, and x-amz-date,
	// so signS3 must reproduce that published signature.
	const secret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	const amz = "20130524T000000Z"
	const getObjectCanonical = "GET\n" +
		"/test.txt\n" +
		"\n" +
		"host:examplebucket.s3.amazonaws.com\n" +
		"range:bytes=0-9\n" +
		"x-amz-content-sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\n" +
		"x-amz-date:20130524T000000Z\n" +
		"\n" +
		"host;range;x-amz-content-sha256;x-amz-date\n" +
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	const getObjectSignature = "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := sigV4Signature(getObjectCanonical, secret, amz, "us-east-1", "s3"); got != getObjectSignature {
		t.Fatalf("published GET object signature %s", got)
	}
	getLines := strings.Split(getObjectCanonical, "\n")
	if canonicalPath("/test.txt") != getLines[1] || getLines[1] != "/test.txt" || getLines[2] != "" {
		t.Fatalf("GET object uri/query %q %q", getLines[1], getLines[2])
	}
	if canonicalPath("/test$file.text") != "/test%24file.text" {
		t.Fatalf("PUT object uri %s", canonicalPath("/test$file.text"))
	}

	const lifecycleCanonical = "GET\n" +
		"/\n" +
		"lifecycle=\n" +
		"host:examplebucket.s3.amazonaws.com\n" +
		"x-amz-content-sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855\n" +
		"x-amz-date:20130524T000000Z\n" +
		"\n" +
		"host;x-amz-content-sha256;x-amz-date\n" +
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	const lifecycleSignature = "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543"
	if got := sigV4Signature(lifecycleCanonical, secret, amz, "us-east-1", "s3"); got != lifecycleSignature {
		t.Fatalf("published lifecycle signature %s", got)
	}
	lifeLines := strings.Split(lifecycleCanonical, "\n")
	if canonicalPath("/") != lifeLines[1] || lifeLines[1] != "/" {
		t.Fatalf("lifecycle uri %q", lifeLines[1])
	}
	q := url.Values{}
	q.Set("lifecycle", "")
	if canonicalQuery(q) != lifeLines[2] || lifeLines[2] != "lifecycle=" {
		t.Fatalf("lifecycle query %q", canonicalQuery(q))
	}
	list := url.Values{}
	list.Set("max-keys", "2")
	list.Set("prefix", "J")
	if canonicalQuery(list) != "max-keys=2&prefix=J" {
		t.Fatalf("list query %s", canonicalQuery(list))
	}

	req, err := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.URL.Path = "/"
	req.URL.RawPath = ""
	req.URL.RawQuery = "lifecycle="
	req.Host = "examplebucket.s3.amazonaws.com"
	req.Header.Set("Host", req.Host)
	req.Header.Set("x-amz-date", amz)
	req.Header.Set("x-amz-content-sha256", emptySHA256)
	signS3(req, emptySHA256, "AKIAIOSFODNN7EXAMPLE", secret, "us-east-1", "s3", amz)
	const want = "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=" + lifecycleSignature
	if req.Header.Get("Authorization") != want {
		t.Fatalf("authorization %s", req.Header.Get("Authorization"))
	}
}

func TestS3ReservedKeyOnWire(t *testing.T) {
	const access = "AKIDEXAMPLE"
	const secret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	var putURI, putEscaped, listQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !wireSigV4OK(r, secret) {
			http.Error(w, "signature "+r.RequestURI, http.StatusForbidden)
			return
		}
		switch {
		case r.Method == http.MethodPut:
			putURI = r.RequestURI
			putEscaped = r.URL.EscapedPath()
			if !strings.Contains(putEscaped, "%2B") || !strings.Contains(putEscaped, "%3A") {
				http.Error(w, "escaped "+putEscaped, http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.Contains(r.URL.RawQuery, "list-type=2"):
			listQuery = r.URL.RawQuery
			if strings.Contains(listQuery, "+") || !strings.Contains(listQuery, "%2B") || !strings.Contains(listQuery, "%3A") {
				http.Error(w, "query "+listQuery, http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<ListBucketResult></ListBucketResult>`)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()
	dest := &S3Destination{
		Spec: DestinationSpec{
			ID: "obj", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1",
			Bucket: "backups", Prefix: "100%+pre:fix//../keep",
			AccessKey: access, SecretKey: secret,
		},
		HTTP: srv.Client(),
		Now:  func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) },
	}
	body := []byte("synthetic-archive")
	sum, err := sha256Hex(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := dest.Put(context.Background(), "obj.bin", bytes.NewReader(body), sum); err != nil {
		t.Fatal(err)
	}
	for _, uri := range []string{putURI, putEscaped} {
		if !strings.Contains(uri, "%25%2B") || !strings.Contains(uri, "%3A") || !strings.Contains(uri, "//../") {
			t.Fatalf("wire path %s", uri)
		}
		if strings.Contains(uri, "%2525") || strings.Contains(uri, "+") || strings.Contains(uri, ":") {
			t.Fatalf("wire path not canonical %s", uri)
		}
	}
	objs, err := dest.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 0 {
		t.Fatalf("list %+v", objs)
	}
	const wantQuery = "list-type=2&prefix=100%25%2Bpre%3Afix%2F%2F..%2Fkeep"
	if listQuery != wantQuery {
		t.Fatalf("list query %s", listQuery)
	}
}

func TestS3DeleteMissingIsIdempotentAndDeniedIsNot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/missing.tgbk"), strings.HasSuffix(r.URL.Path, "/missing.sha256"):
			http.NotFound(w, r)
		case strings.HasSuffix(r.URL.Path, "/denied.tgbk"):
			http.Error(w, "AccessDenied", http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	dest := &S3Destination{
		Spec: DestinationSpec{
			ID: "obj", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1",
			Bucket: "backups", Prefix: "firm", Namespace: "nsowned01",
			AccessKey: "AKID", SecretKey: "secret",
		},
		HTTP: srv.Client(),
		Now:  func() time.Time { return time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) },
	}
	if err := dest.Delete(context.Background(), "missing.tgbk"); err != nil {
		t.Fatalf("missing owned archive: %v", err)
	}
	if err := dest.Delete(context.Background(), "missing.sha256"); err != nil {
		t.Fatalf("missing owned sidecar: %v", err)
	}
	if err := dest.Delete(context.Background(), "denied.tgbk"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("permission error was treated as absence: %v", err)
	}
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	down := *dest
	down.Spec.Endpoint = closedURL
	if err := down.Delete(context.Background(), "missing.tgbk"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("network error was treated as absence: %v", err)
	}
}

func wireSigV4OK(r *http.Request, secret string) bool {
	amz := strings.TrimSpace(r.Header.Get("x-amz-date"))
	payload := strings.TrimSpace(r.Header.Get("x-amz-content-sha256"))
	if len(amz) < 8 || payload == "" {
		return false
	}
	path, query := r.RequestURI, ""
	if i := strings.Index(r.RequestURI, "?"); i >= 0 {
		path, query = r.RequestURI[:i], r.RequestURI[i+1:]
	}
	canonical := r.Method + "\n" +
		path + "\n" +
		query + "\n" +
		"host:" + strings.TrimSpace(r.Host) + "\n" +
		"x-amz-content-sha256:" + payload + "\n" +
		"x-amz-date:" + amz + "\n" +
		"\n" +
		"host;x-amz-content-sha256;x-amz-date\n" +
		payload
	want := sigV4Signature(canonical, secret, amz, "us-east-1", "s3")
	auth := r.Header.Get("Authorization")
	const mark = "Signature="
	i := strings.LastIndex(auth, mark)
	if i < 0 {
		return false
	}
	return auth[i+len(mark):] == want
}

func TestLostNetworkResponsesStayUncertain(t *testing.T) {
	for _, err := range []error{
		io.EOF,
		fmt.Errorf("http: %w", io.EOF),
		io.ErrUnexpectedEOF,
		fmt.Errorf("read: %w", io.ErrUnexpectedEOF),
		syscall.ECONNRESET,
		fmt.Errorf("reset: %w", syscall.ECONNRESET),
		syscall.EPIPE,
		context.DeadlineExceeded,
		os.ErrDeadlineExceeded,
	} {
		if !ambiguousRemote(err) {
			t.Fatalf("%v was treated as a definite failure", err)
		}
	}
	for _, err := range []error{
		ErrNotFound,
		ErrBadRequest,
		ErrChecksum,
		&remoteStatusError{Status: http.StatusForbidden, Detail: "denied"},
		&os.PathError{Op: "open", Path: "archive", Err: os.ErrPermission},
	} {
		if ambiguousRemote(err) {
			t.Fatalf("%v was treated as a lost response", err)
		}
	}
	if !ambiguousRemote(errors.New("timeout")) {
		t.Fatal("a bare timeout string was treated as reliable evidence")
	}
}

func TestRestartedPendingPutSurvivesMissingGet(t *testing.T) {
	var mu sync.Mutex
	stored := map[string][]byte{"/backups/other-firm/keep": []byte("keep")}
	var puts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			puts++
			stored[r.URL.Path] = body
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			mu.Lock()
			body, ok := stored[r.URL.Path]
			mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
		case http.MethodDelete:
			mu.Lock()
			delete(stored, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := testStore(t)
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{
		ID: "share", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1",
		Bucket: "backups", AccessKey: "AKID", SecretKey: "secret", RetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	specs, err := s.Book.Specs()
	if err != nil || len(specs) != 1 {
		t.Fatal(err)
	}
	name := meta.ID + ".tgbk"
	if err := s.receiptBook().Add(OwnedObject{DestinationID: "share", Name: name, Created: meta.CreatedAt, State: receiptPending}); err != nil {
		t.Fatal(err)
	}
	open := func(spec DestinationSpec) (Destination, error) {
		return &S3Destination{Spec: spec, HTTP: srv.Client(), Now: s.Now}, nil
	}
	s.OpenDestination = open
	restarted := &Store{Dir: s.Dir, Book: s.Book, Passphrase: s.Passphrase, Now: s.Now, OpenDestination: open}
	dest, err := restarted.openDest(specs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	owned, err := restarted.receiptBook().List("share")
	if err != nil || len(owned) != 1 || owned[0].State != receiptUncertain {
		t.Fatalf("restart erased a pending upload after NotFound: %+v %v", owned, err)
	}
	if err := restarted.Delete(context.Background(), meta.ID); err == nil {
		t.Fatal("delete succeeded while the upload was still unpublished")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, name)); err != nil {
		t.Fatalf("local archive removed: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(s.Dir, name))
	if err != nil {
		t.Fatal(err)
	}
	late := "/backups/" + specs[0].Namespace + "/" + name
	mu.Lock()
	stored[late] = raw
	count := puts
	keep := string(stored["/backups/other-firm/keep"])
	mu.Unlock()
	if count != 0 || keep != "keep" {
		t.Fatalf("puts %d other %q", count, keep)
	}
	if err := restarted.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	_, left := stored[late]
	keep = string(stored["/backups/other-firm/keep"])
	mu.Unlock()
	if left || keep != "keep" {
		t.Fatalf("late object left=%v other=%q", left, keep)
	}
	if owned, err = restarted.receiptBook().List("share"); err != nil || len(owned) != 0 {
		t.Fatalf("tombstone remained after the late object was removed: %+v %v", owned, err)
	}
}

func TestHTTPCloseEOFKeepsOwnershipUntilDelayedPublish(t *testing.T) {
	old := destinationIOTimeout
	destinationIOTimeout = 2 * time.Second
	t.Cleanup(func() { destinationIOTimeout = old })
	var mu sync.Mutex
	stored := map[string][]byte{"/backups/other-firm/keep": []byte("keep")}
	var puts int
	hold := make(chan struct{})
	gotBody := make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(hold) }) }
	t.Cleanup(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			mu.Lock()
			body, ok := stored[r.URL.Path]
			mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
			return
		}
		if r.Method == http.MethodDelete {
			mu.Lock()
			delete(stored, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPut {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		puts++
		mu.Unlock()
		once.Do(func() { close(gotBody) })
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, buf)
		conn.Close()
		go func() {
			<-hold
			mu.Lock()
			stored[r.URL.Path] = body
			mu.Unlock()
		}()
	}))
	defer srv.Close()
	s := testStore(t)
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{
		ID: "share", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1",
		Bucket: "backups", AccessKey: "AKID", SecretKey: "secret", RetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(spec DestinationSpec) (Destination, error) {
		return &S3Destination{Spec: spec, HTTP: srv.Client(), Now: s.Now}, nil
	}
	meta, err := s.Create(context.Background(), reasonManual)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gotBody:
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not start")
	}
	if len(meta.DestinationErrors) != 1 || !strings.Contains(meta.DestinationErrors[0], "uncertain") {
		t.Fatalf("eof was definite: %+v", meta.DestinationErrors)
	}
	owned, err := s.receiptBook().List("share")
	if err != nil || len(owned) != 1 || owned[0].State != receiptUncertain || !strings.HasSuffix(owned[0].Name, ".tgbk") {
		t.Fatalf("ownership %+v %v", owned, err)
	}
	if err := s.Delete(context.Background(), meta.ID); err == nil {
		t.Fatal("delete succeeded before the closed upload published")
	}
	release()
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(stored)
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delayed publish did not land")
		}
		time.Sleep(10 * time.Millisecond)
	}
	restarted := &Store{Dir: s.Dir, Book: s.Book, Passphrase: s.Passphrase, Now: s.Now, OpenDestination: s.OpenDestination}
	specs, err := restarted.Book.Specs()
	if err != nil {
		t.Fatal(err)
	}
	dest, err := restarted.openDest(specs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	left := len(stored)
	count := puts
	keep := string(stored["/backups/other-firm/keep"])
	mu.Unlock()
	if left != 1 || count != 1 || keep != "keep" {
		t.Fatalf("stored %d puts %d other %q", left, count, keep)
	}
}

func TestSidecarLostAckBlocksDeleteUntilLatePublish(t *testing.T) {
	old := destinationIOTimeout
	destinationIOTimeout = 200 * time.Millisecond
	t.Cleanup(func() { destinationIOTimeout = old })
	var mu sync.Mutex
	stored := map[string][]byte{"/backups/other-firm/keep": []byte("keep")}
	var archivePuts, sidePuts int
	hold := make(chan struct{})
	gotSide := make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(hold) }) }
	t.Cleanup(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			mu.Lock()
			body, ok := stored[r.URL.Path]
			mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
			return
		case http.MethodDelete:
			mu.Lock()
			delete(stored, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPut {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			mu.Lock()
			sidePuts++
			mu.Unlock()
			once.Do(func() { close(gotSide) })
			<-hold
			mu.Lock()
			stored[r.URL.Path] = body
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		archivePuts++
		stored[r.URL.Path] = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s := testStore(t)
	s.Book = &DestinationBook{Path: filepath.Join(s.Dir, "destinations.json")}
	if _, err := s.Book.Put(DestinationSpec{
		ID: "share", Kind: "s3", Endpoint: srv.URL, Region: "us-east-1",
		Bucket: "backups", AccessKey: "AKID", SecretKey: "secret", RetentionDays: 7,
	}); err != nil {
		t.Fatal(err)
	}
	s.OpenDestination = func(spec DestinationSpec) (Destination, error) {
		return &S3Destination{Spec: spec, HTTP: &http.Client{Timeout: destinationIOTimeout}, Now: s.Now}, nil
	}
	done := make(chan BackupMeta, 1)
	go func() {
		meta, err := s.Create(context.Background(), reasonManual)
		if err != nil {
			t.Errorf("create: %v", err)
		}
		done <- meta
	}()
	select {
	case <-gotSide:
	case <-time.After(3 * time.Second):
		t.Fatal("sidecar upload did not start")
	}
	meta := <-done
	if len(meta.DestinationErrors) != 1 {
		t.Fatalf("errors = %v", meta.DestinationErrors)
	}
	owned, err := s.receiptBook().List("share")
	if err != nil || len(owned) != 2 {
		t.Fatalf("receipts %+v %v", owned, err)
	}
	var sideUncertain, archivePending bool
	for _, obj := range owned {
		if strings.HasSuffix(obj.Name, ".sha256") && obj.State == receiptUncertain {
			sideUncertain = true
		}
		if strings.HasSuffix(obj.Name, ".tgbk") && obj.State == receiptPending {
			archivePending = true
		}
	}
	if !sideUncertain || !archivePending {
		t.Fatalf("archive completed before the sidecar ack: %+v", owned)
	}
	restarted := &Store{Dir: s.Dir, Book: s.Book, Passphrase: s.Passphrase, Now: s.Now, OpenDestination: s.OpenDestination}
	specs, err := restarted.Book.Specs()
	if err != nil {
		t.Fatal(err)
	}
	dest, err := restarted.openDest(specs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	owned, err = restarted.receiptBook().List("share")
	if err != nil || len(owned) != 2 {
		t.Fatalf("restart dropped a sidecar tombstone: %+v %v", owned, err)
	}
	if err := restarted.Delete(context.Background(), meta.ID); err == nil {
		t.Fatal("delete succeeded while the sidecar could still publish")
	}
	release()
	time.Sleep(50 * time.Millisecond)
	if err := restarted.reconcileDestination(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	var sideLeft bool
	for path := range stored {
		if strings.HasSuffix(path, ".sha256") {
			sideLeft = true
		}
	}
	keep := string(stored["/backups/other-firm/keep"])
	a, side := archivePuts, sidePuts
	mu.Unlock()
	if sideLeft || keep != "keep" || a != 1 || side != 1 {
		t.Fatalf("sidecar left=%v archivePuts=%d sidePuts=%d other=%q", sideLeft, a, side, keep)
	}
}

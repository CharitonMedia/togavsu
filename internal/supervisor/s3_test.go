package supervisor

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

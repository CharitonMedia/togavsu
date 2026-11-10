package storage_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/storage"
	"github.com/CharitonMedia/togavsu/internal/storage/storagetest"
)

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func readAll(t *testing.T, b storage.Backend, key string, off, n int64) []byte {
	t.Helper()
	rc, err := b.Get(context.Background(), key, off, n)
	if err != nil {
		t.Fatalf("get %s [%d,%d): %v", key, off, n, err)
	}
	defer rc.Close()
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %s [%d,%d): %v", key, off, n, err)
	}
	return out
}

// backends returns every adapter kind, plain and sealed, against local or fake services.
func backends(t *testing.T) map[string]storage.Backend {
	t.Helper()
	key := randomBytes(t, 32)
	disk, err := storage.NewDisk(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fs3 := storagetest.NewS3("bucket")
	t.Cleanup(fs3.Server.Close)
	s3, err := storage.NewS3(fs3.Settings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fdb := storagetest.NewDropbox()
	t.Cleanup(fdb.Server.Close)
	dbx, err := storage.NewDropbox(fdb.Settings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]storage.Backend{"disk": disk, "s3": s3, "dropbox": dbx}
	for name, b := range map[string]storage.Backend{"disk": disk, "s3": s3, "dropbox": dbx} {
		sealed, err := storage.NewSealed(b, key)
		if err != nil {
			t.Fatal(err)
		}
		out["sealed-"+name] = sealed
	}
	return out
}

func TestEveryBackendRoundTripsRangesAndRefusesOverwrite(t *testing.T) {
	ctx := context.Background()
	// Three full seal segments plus a tail, so ranges cross segment edges.
	data := randomBytes(t, 3*65536+777)
	for name, b := range backends(t) {
		t.Run(name, func(t *testing.T) {
			key := "0190f0c2-7f3e-7c1a-8b2d-1f2e3d4c5b6a"
			if err := b.Health(ctx); err != nil {
				t.Fatalf("health: %v", err)
			}
			if _, err := b.Stat(ctx, key); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("stat before put: %v", err)
			}
			if err := b.Put(ctx, key, bytes.NewReader(data), int64(len(data))); err != nil {
				t.Fatalf("put: %v", err)
			}
			if err := b.Put(ctx, key, bytes.NewReader([]byte("other")), 5); !errors.Is(err, storage.ErrExists) {
				t.Fatalf("second put = %v, want ErrExists", err)
			}
			if got := readAll(t, b, key, 0, -1); !bytes.Equal(got, data) {
				t.Fatalf("full read differs after refused overwrite")
			}
			info, err := b.Stat(ctx, key)
			if err != nil || info.Size != int64(len(data)) {
				if _, sealed := b.(*storage.Sealed); sealed || name == "disk" || name == "s3" || name == "dropbox" {
					t.Fatalf("stat = %+v, %v", info, err)
				}
			}
			for _, rg := range [][2]int64{{0, 10}, {65530, 20}, {65536, 65536}, {int64(len(data)) - 5, 5}, {100, -1}, {int64(len(data)) - 1, 100}} {
				end := int64(len(data))
				if rg[1] >= 0 && rg[0]+rg[1] < end {
					end = rg[0] + rg[1]
				}
				if got := readAll(t, b, key, rg[0], rg[1]); !bytes.Equal(got, data[rg[0]:end]) {
					t.Fatalf("range %v: got %d bytes, want %d", rg, len(got), end-rg[0])
				}
			}
			if err := storage.Check(ctx, b, key, int64(len(data)), sum(data)); err != nil {
				t.Fatalf("check: %v", err)
			}
			if err := b.Delete(ctx, key); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if err := b.Delete(ctx, key); err != nil {
				t.Fatalf("second delete: %v", err)
			}
			if _, err := b.Get(ctx, key, 0, -1); !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("get after delete = %v", err)
			}
		})
	}
}

func TestEmptyObject(t *testing.T) {
	ctx := context.Background()
	for name, b := range backends(t) {
		if err := b.Put(ctx, "empty-"+name, bytes.NewReader(nil), 0); err != nil {
			t.Fatalf("%s put: %v", name, err)
		}
		if err := storage.Check(ctx, b, "empty-"+name, 0, sum(nil)); err != nil {
			t.Fatalf("%s check: %v", name, err)
		}
	}
}

func TestKeysAreOpaque(t *testing.T) {
	ctx := context.Background()
	for name, b := range backends(t) {
		for _, key := range []string{"../escape", "a/b", "Name With Spaces.docx", "", "/abs"} {
			if err := b.Put(ctx, key, bytes.NewReader([]byte("x")), 1); !errors.Is(err, storage.ErrKey) {
				t.Fatalf("%s put %q = %v, want ErrKey", name, key, err)
			}
		}
	}
}

func TestVerifiedReadFailsOnChangedByte(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	disk, _ := storage.NewDisk(root)
	data := randomBytes(t, 5000)
	if err := disk.Put(ctx, "k1", bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	var paths []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Base(p) == "k1" {
			paths = append(paths, p)
		}
		return nil
	})
	if len(paths) != 1 {
		t.Fatalf("object paths = %v", paths)
	}
	raw, _ := os.ReadFile(paths[0])
	raw[1234] ^= 1
	if err := os.WriteFile(paths[0], raw, 0o600); err != nil {
		t.Fatal(err)
	}
	called := 0
	v, err := storage.OpenVerified(ctx, disk, "k1", int64(len(data)), sum(data))
	if err != nil {
		t.Fatal(err)
	}
	v.OnMismatch = func(error) { called++ }
	_, err = io.Copy(io.Discard, v)
	if !errors.Is(err, storage.ErrIntegrity) || called != 1 || v.Checked() {
		t.Fatalf("verified read = %v, mismatch calls %d", err, called)
	}
	// A truncated object fails too.
	if err := storage.Check(ctx, disk, "k1", int64(len(data))+1, sum(data)); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatalf("size mismatch = %v", err)
	}
}

func TestSealedDetectsTamperTruncationAndSwap(t *testing.T) {
	ctx := context.Background()
	fs3 := storagetest.NewS3("bucket")
	defer fs3.Server.Close()
	inner, err := storage.NewS3(fs3.Settings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := storage.NewSealed(inner, randomBytes(t, 32))
	if err != nil {
		t.Fatal(err)
	}
	data := randomBytes(t, 2*65536+10)
	for _, k := range []string{"aa", "bb"} {
		if err := sealed.Put(ctx, k, bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatal(err)
		}
	}
	// Nothing on the backend is the plaintext.
	raw := readAll(t, inner, "aa", 0, -1)
	if bytes.Contains(raw, data[:64]) {
		t.Fatal("plaintext reached the backend")
	}
	if int64(len(raw)) != storage.SealedSize(int64(len(data))) {
		t.Fatalf("sealed size %d, want %d", len(raw), storage.SealedSize(int64(len(data))))
	}
	if fs3.Flip("aa") != 1 {
		t.Fatal("flip")
	}
	if _, err := io.ReadAll(mustGet(t, sealed, "aa", 2*65536, -1)); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatalf("tampered last segment = %v", err)
	}
	// The first segment is unchanged and still authenticates on its own.
	if got := readAll(t, sealed, "aa", 0, 100); !bytes.Equal(got, data[:100]) {
		t.Fatal("untouched segment")
	}
	// Truncate by one whole segment: the new last segment was sealed as "more" and fails.
	cut := raw[:len(raw)-int(storage.SealedSize(10))+int(storage.SealedSize(0))]
	_ = inner.Delete(ctx, "cc")
	if err := inner.Put(ctx, "cc", bytes.NewReader(cut), int64(len(cut))); err != nil {
		t.Fatal(err)
	}
	if err := storage.Check(ctx, sealed, "cc", 2*65536, sum(data[:2*65536])); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatalf("truncated object = %v", err)
	}
	// Copying bb's bytes under another key fails: the key is bound into every segment.
	rawB := readAll(t, inner, "bb", 0, -1)
	if err := inner.Put(ctx, "dd", bytes.NewReader(rawB), int64(len(rawB))); err != nil {
		t.Fatal(err)
	}
	if err := storage.Check(ctx, sealed, "dd", int64(len(data)), sum(data)); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatalf("swapped object = %v", err)
	}
	// A different master key cannot open it.
	other, _ := storage.NewSealed(inner, randomBytes(t, 32))
	if err := storage.Check(ctx, other, "bb", int64(len(data)), sum(data)); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatalf("wrong key = %v", err)
	}
	if _, err := storage.NewSealed(inner, []byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
	if err := sealed.Put(ctx, "ee", bytes.NewReader(data), int64(len(data))-1); err == nil {
		t.Fatal("longer plaintext than size accepted")
	}
}

func mustGet(t *testing.T, b storage.Backend, key string, off, n int64) io.Reader {
	t.Helper()
	rc, err := b.Get(context.Background(), key, off, n)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rc.Close() })
	return rc
}

// TestSigV4Vector checks the signer against the GET Object example in the AWS
// Signature Version 4 documentation.
func TestSigV4Vector(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	when := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	storage.Sign(req, "AKIAIOSFODNN7EXAMPLE", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "us-east-1", "s3",
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", when)
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization\n got %s\nwant %s", got, want)
	}
}

func TestS3RejectsBadCredentials(t *testing.T) {
	fs3 := storagetest.NewS3("bucket")
	defer fs3.Server.Close()
	s := fs3.Settings()
	s.SecretKey = "wrong"
	b, err := storage.NewS3(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Health(context.Background()); err == nil {
		t.Fatal("wrong secret passed health")
	}
	if _, err := storage.NewS3(storage.Settings{Endpoint: "ftp://x", Bucket: "b", AccessKey: "a", SecretKey: "s"}, nil); !errors.Is(err, storage.ErrConfig) {
		t.Fatalf("bad endpoint = %v", err)
	}
}

func TestDropboxRefreshesTokenAndUsesSessionsForLargeObjects(t *testing.T) {
	ctx := context.Background()
	fdb := storagetest.NewDropbox()
	defer fdb.Server.Close()
	b, err := storage.NewDropbox(fdb.Settings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	big := randomBytes(t, 2*(1<<23)+12345)
	if err := b.Put(ctx, "big", bytes.NewReader(big), int64(len(big))); err != nil {
		t.Fatal(err)
	}
	if fdb.Requests["/oauth2/token"] != 1 {
		t.Fatalf("token refreshes = %d", fdb.Requests["/oauth2/token"])
	}
	if fdb.Requests["/2/files/upload_session/start"] != 1 || fdb.Requests["/2/files/upload_session/append_v2"] != 2 || fdb.Requests["/2/files/upload_session/finish"] != 1 {
		t.Fatalf("session calls = %v", fdb.Requests)
	}
	if err := storage.Check(ctx, b, "big", int64(len(big)), sum(big)); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(ctx, "big", bytes.NewReader(big), int64(len(big))); !errors.Is(err, storage.ErrExists) {
		t.Fatalf("session overwrite = %v", err)
	}
	if keys := fdb.Keys(); len(keys) != 1 || keys[0] != "/big" {
		t.Fatalf("dropbox paths = %v", keys)
	}
	if _, err := storage.NewDropbox(storage.Settings{}, nil); !errors.Is(err, storage.ErrConfig) {
		t.Fatalf("empty dropbox settings = %v", err)
	}
}

func TestSettingsPublicHidesSecrets(t *testing.T) {
	s, err := storage.ParseSettings([]byte(`{"endpoint":"https://s3.example.invalid","bucket":"b","access_key":"AK","secret_key":"SECRET"}`))
	if err != nil {
		t.Fatal(err)
	}
	pub := s.Public()
	if _, ok := pub["secret_key"]; ok || pub["secrets_set"] != true || pub["bucket"] != "b" {
		t.Fatalf("public = %v", pub)
	}
	if _, err := storage.ParseSettings([]byte(`{"nope":1}`)); !errors.Is(err, storage.ErrConfig) {
		t.Fatalf("unknown field = %v", err)
	}
}

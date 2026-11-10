package storage_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/storage"
)

// TestLiveS3Compatible runs the adapter against a real S3-compatible server (MinIO in
// local runs) when TOGAVSU_TEST_S3_ENDPOINT, _BUCKET, _ACCESS_KEY and _SECRET_KEY are
// set. It is skipped otherwise; the in-memory fake covers CI.
func TestLiveS3Compatible(t *testing.T) {
	endpoint := os.Getenv("TOGAVSU_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("TOGAVSU_TEST_S3_ENDPOINT is not set")
	}
	ctx := context.Background()
	raw, err := storage.NewS3(storage.Settings{
		Endpoint: endpoint, Bucket: os.Getenv("TOGAVSU_TEST_S3_BUCKET"),
		AccessKey: os.Getenv("TOGAVSU_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("TOGAVSU_TEST_S3_SECRET_KEY"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := storage.NewSealed(raw, randomBytes(t, 32))
	if err != nil {
		t.Fatal(err)
	}
	if err := raw.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	key := "live-" + sum(randomBytes(t, 8))[:16]
	data := randomBytes(t, 3*65536+5)
	for _, b := range []storage.Backend{raw, sealed} {
		k := key
		if b == storage.Backend(sealed) {
			k += "-sealed"
		}
		if err := b.Put(ctx, k, bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatalf("put: %v", err)
		}
		if err := b.Put(ctx, k, bytes.NewReader(data), int64(len(data))); !errors.Is(err, storage.ErrExists) {
			t.Fatalf("overwrite = %v", err)
		}
		if got := readAll(t, b, k, 65530, 100); !bytes.Equal(got, data[65530:65630]) {
			t.Fatal("range")
		}
		if err := storage.Check(ctx, b, k, int64(len(data)), sum(data)); err != nil {
			t.Fatal(err)
		}
		if err := b.Delete(ctx, k); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Stat(ctx, k); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("stat after delete = %v", err)
		}
	}
}

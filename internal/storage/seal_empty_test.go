package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/storage"
	"github.com/CharitonMedia/togavsu/internal/storage/storagetest"
)

// TestSealedEmptyObjectIsAuthenticated: an empty sealed object still has one sealed
// segment, and a read opens it, so a tampered or swapped empty object fails its tag
// check instead of reading back as a clean empty file.
func TestSealedEmptyObjectIsAuthenticated(t *testing.T) {
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
	for _, k := range []string{"e1", "e2"} {
		if err := sealed.Put(ctx, k, bytes.NewReader(nil), 0); err != nil {
			t.Fatal(err)
		}
	}
	if got := readAll(t, sealed, "e1", 0, -1); len(got) != 0 {
		t.Fatalf("empty object read %d bytes", len(got))
	}
	if err := storage.Check(ctx, sealed, "e1", 0, sum(nil)); err != nil {
		t.Fatalf("genuine empty object = %v", err)
	}
	// e2's sealed bytes copied under another key: the key is bound into the segment.
	raw := readAll(t, inner, "e2", 0, -1)
	if err := inner.Put(ctx, "e3", bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(mustGet(t, sealed, "e3", 0, -1)); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatalf("swapped empty object = %v", err)
	}
	if err := storage.Check(ctx, sealed, "e3", 0, sum(nil)); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatalf("swapped empty object check = %v", err)
	}
	// A changed byte in the empty object's tag fails too.
	if fs3.Flip("e1") != 1 {
		t.Fatal("flip")
	}
	if _, err := io.ReadAll(mustGet(t, sealed, "e1", 0, -1)); !errors.Is(err, storage.ErrIntegrity) {
		t.Fatalf("tampered empty object = %v", err)
	}
}

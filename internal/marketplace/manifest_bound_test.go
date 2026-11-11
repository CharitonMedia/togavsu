package marketplace

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"testing"
)

// gzPackage writes a gzip tar whose manifest comes after the entries filler adds.
func gzPackage(t *testing.T, filler func(tw *tar.Writer)) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	filler(tw)
	man := []byte(`{"id":"matter-clock","name":"Matter Clock","version":"1.0.0","permissions":["matters:read"]}`)
	if err := tw.WriteHeader(&tar.Header{Name: ManifestName, Size: int64(len(man)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(man); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestReadManifestBoundsExpandedBytes(t *testing.T) {
	old := fixed.MaxExpandedBytes
	fixed.MaxExpandedBytes = 1 << 20
	defer func() { fixed.MaxExpandedBytes = old }()
	pkg := gzPackage(t, func(tw *tar.Writer) {
		zeros := make([]byte, 4<<20)
		if err := tw.WriteHeader(&tar.Header{Name: "layer.bin", Size: int64(len(zeros)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(zeros); err != nil {
			t.Fatal(err)
		}
	})
	if len(pkg) > 1<<20 {
		t.Fatalf("test package should compress small, got %d bytes", len(pkg))
	}
	if _, err := ReadManifest(pkg); !errors.Is(err, ErrManifest) {
		t.Fatalf("a package that expands past the bound before its manifest: %v", err)
	}
	fixed.MaxExpandedBytes = old
	if m, err := ReadManifest(pkg); err != nil || m.ID != "matter-clock" {
		t.Fatalf("the same package under the shipped bound: %+v %v", m, err)
	}
}

func TestReadManifestBoundsEntryCount(t *testing.T) {
	pkg := gzPackage(t, func(tw *tar.Writer) {
		for i := 0; i < fixed.MaxPackageEntries; i++ {
			if err := tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("f%d", i), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
		}
	})
	if _, err := ReadManifest(pkg); !errors.Is(err, ErrManifest) {
		t.Fatalf("a manifest after max_package_entries entries: %v", err)
	}
}

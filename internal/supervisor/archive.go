package supervisor

import (
	"archive/tar"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const manifestFormat = "togavsu-backup-v1"

// ManifestFile is one logical file stored by content hash.
type ManifestFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// Manifest is the plaintext index inside an encrypted backup.
type Manifest struct {
	Format       string           `json:"format"`
	ID           string           `json:"id"`
	CreatedAt    time.Time        `json:"created_at"`
	Reason       string           `json:"reason"`
	RecordCounts map[string]int64 `json:"record_counts"`
	DumpSHA256   string           `json:"dump_sha256,omitempty"`
	Files        []ManifestFile   `json:"files"`
}

func writeTar(dst io.Writer, root string) error {
	tw := tar.NewWriter(dst)
	err := filepath.Walk(root, func(name string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s: %w", rel, ErrUnsafePath)
		}
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

func untar(dst string, r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(hdr.Name)
		if name == "." || name == "" {
			continue
		}
		if strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
			return ErrUnsafePath
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		rel, err := filepath.Rel(dst, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return ErrUnsafePath
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			return ErrUnsafePath
		}
	}
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return sha256Hex(f)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".partial"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newBackupID(now time.Time) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:]), nil
}

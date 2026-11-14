package supervisor

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// fileManifestName is the first entry of the core's file export (files.ExportBytes):
// a JSON list of every object the export carries.
const fileManifestName = "files-manifest.json"

// maxFileManifest bounds the manifest entry read into memory.
const maxFileManifest = 256 << 20

// exportObject is the part of a files.BackupObject manifest entry the supervisor
// checks. The core checks the rest against the restored database when it writes.
type exportObject struct {
	ObjectID string `json:"object_id"`
	Size     int64  `json:"size_bytes"`
	SHA256   string `json:"sha256"`
}

// checkFileExport reads a whole core file export and refuses it unless it is complete:
// the manifest comes first, every entry is a manifest object listed once, each entry's
// size and SHA-256 equal its manifest line, and every manifest object is present. It
// runs before the database swap, so a damaged or partial file set fails the restore
// while the existing data is still in place.
func checkFileExport(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	hdr, err := tr.Next()
	if err != nil || hdr.Name != fileManifestName || hdr.Typeflag != tar.TypeReg || hdr.Size > maxFileManifest {
		return fmt.Errorf("%w: the file export has no manifest", ErrArchive)
	}
	var list []exportObject
	if err := json.NewDecoder(io.LimitReader(tr, hdr.Size)).Decode(&list); err != nil {
		return fmt.Errorf("%w: file manifest: %v", ErrArchive, err)
	}
	want := make(map[string]exportObject, len(list))
	for _, o := range list {
		if o.ObjectID == "" || o.Size < 0 || len(o.SHA256) != sha256.Size*2 {
			return fmt.Errorf("%w: file manifest entry %q is incomplete", ErrArchive, o.ObjectID)
		}
		if _, dup := want[o.ObjectID]; dup {
			return fmt.Errorf("%w: file manifest lists %s twice", ErrArchive, o.ObjectID)
		}
		want[o.ObjectID] = o
	}
	seen := make(map[string]bool, len(want))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: file export: %v", ErrArchive, err)
		}
		id, ok := strings.CutPrefix(hdr.Name, "objects/")
		o, listed := want[id]
		if !ok || !listed || hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("%w: file export entry %s is not in its manifest", ErrArchive, hdr.Name)
		}
		if seen[id] {
			return fmt.Errorf("%w: file export entry %s appears twice", ErrArchive, hdr.Name)
		}
		seen[id] = true
		if hdr.Size != o.Size {
			return fmt.Errorf("%w: file object %s is %d bytes, its manifest says %d", ErrChecksum, id, hdr.Size, o.Size)
		}
		h := sha256.New()
		n, err := io.Copy(h, tr)
		if err != nil {
			return fmt.Errorf("%w: file object %s: %v", ErrArchive, id, err)
		}
		if n != o.Size || hex.EncodeToString(h.Sum(nil)) != o.SHA256 {
			return fmt.Errorf("%w: file object %s does not match its manifest SHA-256", ErrChecksum, id)
		}
	}
	if len(seen) != len(want) {
		for _, o := range list {
			if !seen[o.ObjectID] {
				return fmt.Errorf("%w: the file export ends before object %s (%d of %d present)", ErrArchive, o.ObjectID, len(seen), len(want))
			}
		}
	}
	return nil
}

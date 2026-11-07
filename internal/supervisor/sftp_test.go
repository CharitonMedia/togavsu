package supervisor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSFTPBatchRoundTrip(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "remote")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "sftp")
	body := `#!/bin/sh
batch=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-b" ]; then shift; batch="$1"; fi
  shift
done
while IFS= read -r line; do
  set -- $line
  case "$1" in
    put) cp "$2" "$3" ;;
    get) cp "$2" "$3" ;;
    rm) rm -f "$2" ;;
    ls) ls -1 "$3" ;;
    bye) exit 0 ;;
  esac
done < "$batch"
`
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	dest := &SFTPDestination{
		Spec: DestinationSpec{
			ID: "s", Kind: "sftp", Host: "files.example", User: "backup", Port: 22,
			IdentityFile: filepath.Join(dir, "id"), KnownHosts: filepath.Join(dir, "known"),
			RemotePath: root,
		},
		Command: script,
	}
	if err := os.WriteFile(dest.Spec.IdentityFile, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := []byte("synthetic-sftp")
	sum, err := sha256Hex(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if err := dest.Put(context.Background(), "one.tgbk", bytes.NewReader(payload), sum); err != nil {
		t.Fatal(err)
	}
	rc, err := dest.Get(context.Background(), "one.tgbk")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := ioReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q", got)
	}
	if err := dest.Delete(context.Background(), "one.tgbk"); err != nil {
		t.Fatal(err)
	}
}

func ioReadAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(readerOnly{r})
	return buf.Bytes(), err
}

type readerOnly struct {
	r interface{ Read([]byte) (int, error) }
}

func (r readerOnly) Read(p []byte) (int, error) { return r.r.Read(p) }

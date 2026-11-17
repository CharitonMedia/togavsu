package coretest_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/pkg/modulekit/coretest"
)

// TestCORE36MarkdownRenditionStandIn: SeedMarkdownRendition / OpenMarkdownRendition
// close the CONTRIBUTOR-GAPS.md coretest rendition row (CU2-05) with a public kit
// return type (no internal/files import in the suite).
func TestCORE36MarkdownRenditionStandIn(t *testing.T) {
	c := coretest.New(t)
	data := []byte("stand-in markdown body NEEDLE-CORETEST-MD")
	up := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/file-uploads", map[string]any{
		"file_name": "standin.txt", "matter_id": c.Open, "size_bytes": len(data), "media_type": "text/plain",
	})
	upID := str(t, up.JSON, "id")
	chunk := int(num(t, up.JSON, "chunk_size_bytes"))
	for off := 0; off < len(data); off += chunk {
		end := off + chunk
		if end > len(data) {
			end = len(data)
		}
		r := c.Admin.Raw("PUT", "/api/v1/file-uploads/"+upID, data[off:end],
			"Content-Type", "application/octet-stream",
			"Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, end-1, len(data)))
		if r.Code != http.StatusOK {
			t.Fatalf("chunk: %d %s", r.Code, r.Body)
		}
	}
	c.Admin.Must(http.StatusOK, "POST", "/api/v1/file-uploads/"+upID+"/complete", map[string]any{})
	file := c.Admin.Must(http.StatusCreated, "POST", "/api/v1/files", map[string]any{"upload_id": upID})
	fileID := str(t, file.JSON, "id")
	versionID := str(t, file.JSON, "current_version_id")

	view := c.SeedMarkdownRendition(c.Admin, fileID, versionID, "# hello from coretest\n")
	if view.ID == "" || view.Kind != "markdown" || view.FileVersionID != versionID {
		t.Fatalf("seed view: %+v", view)
	}

	dl := c.OpenMarkdownRendition(c.Admin, fileID, view.ID)
	defer dl.Close()
	body, err := io.ReadAll(dl.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "hello from coretest") {
		t.Fatalf("download body: %q", body)
	}
	if !strings.Contains(dl.MediaType, "markdown") {
		t.Fatalf("media type: %q", dl.MediaType)
	}
}

func str(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok || v == "" {
		t.Fatalf("missing %s in %v", key, m)
	}
	return v
}

func num(t *testing.T, m map[string]any, key string) float64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("missing %s in %v", key, m)
	}
	return v
}

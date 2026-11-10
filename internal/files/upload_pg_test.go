package files

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// synthetic generates a deterministic byte stream by chunk, so a test can send any
// chunk again without holding the whole file in memory.
type synthetic struct {
	block cipher.Block
	size  int64
	chunk int64
}

func newSynthetic(size, chunk int64) synthetic {
	b, _ := aes.NewCipher(bytes.Repeat([]byte{7}, 32))
	return synthetic{block: b, size: size, chunk: chunk}
}

func (s synthetic) part(i int64) []byte {
	n := s.chunk
	if rest := s.size - i*s.chunk; rest < n {
		n = rest
	}
	iv := make([]byte, aes.BlockSize)
	binary.BigEndian.PutUint64(iv, uint64(i))
	out := make([]byte, n)
	cipher.NewCTR(s.block, iv).XORKeyStream(out, out)
	return out
}

// dropAfter returns n bytes and then fails, like a connection that drops mid-chunk.
type dropAfter struct {
	r io.Reader
	n int
}

func (d *dropAfter) Read(p []byte) (int, error) {
	if d.n <= 0 {
		return 0, errors.New("connection dropped")
	}
	if len(p) > d.n {
		p = p[:d.n]
	}
	n, err := d.r.Read(p)
	d.n -= n
	return n, err
}

type apiClient struct {
	t   *testing.T
	url string
}

func (c apiClient) do(method, path string, body io.Reader, hdr map[string]string, out any) (int, error) {
	req, err := http.NewRequest(method, c.url+path, body)
	if err != nil {
		return 0, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("%s: %w", raw, err)
		}
	}
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, raw)
	}
	return resp.StatusCode, nil
}

func (c apiClient) json(method, path string, in, out any) (int, error) {
	raw, _ := json.Marshal(in)
	return c.do(method, path, bytes.NewReader(raw), map[string]string{"Content-Type": "application/json"}, out)
}

// TestPGOneGiBUploadResumesAfterDroppedConnection is AC-039's upload clause: a 1 GB
// upload over a connection that drops resumes, and its SHA-256 matches. The core is
// also restarted mid-upload. -short sends 64 MiB instead.
func TestPGOneGiBUploadResumesAfterDroppedConnection(t *testing.T) {
	e := newEnv(t)
	size := int64(1 << 30)
	if testing.Short() {
		size = 64 << 20
	}
	chunk := int64(8 << 20)
	gen := newSynthetic(size, chunk)
	who := func(*http.Request) (domain.Principal, error) { return e.staff, nil }
	srv := e.server(who)
	c := apiClient{t: t, url: srv.URL}

	var up Upload
	if _, err := c.json("POST", "/api/v1/file-uploads", UploadRequest{FileName: "deposition-video.mp4", MatterID: e.open, SizeBytes: size, MediaType: "video/mp4"}, &up); err != nil {
		t.Fatal(err)
	}
	if up.ChunkBytes != chunk || up.NextOffset != 0 || up.Status != "receiving" {
		t.Fatalf("upload = %+v", up)
	}
	want := sha256.New()
	chunks := (size + chunk - 1) / chunk
	dropAt, restartAt := chunks/3, 2*chunks/3
	for i := int64(0); i < chunks; i++ {
		part := gen.part(i)
		want.Write(part)
		rng := fmt.Sprintf("bytes %d-%d/%d", i*chunk, i*chunk+int64(len(part))-1, size)
		hdr := map[string]string{"Content-Range": rng, "Content-Type": "application/octet-stream"}
		if i == dropAt {
			// The connection drops halfway through this chunk.
			req, _ := http.NewRequest("PUT", c.url+"/api/v1/file-uploads/"+string(up.ID), &dropAfter{r: bytes.NewReader(part), n: len(part) / 2})
			req.ContentLength = int64(len(part))
			for k, v := range hdr {
				req.Header.Set(k, v)
			}
			if resp, err := http.DefaultClient.Do(req); err == nil {
				resp.Body.Close()
				t.Fatalf("a dropped chunk was accepted: %d", resp.StatusCode)
			}
			var st Upload
			if _, err := c.json("GET", "/api/v1/file-uploads/"+string(up.ID), nil, &st); err != nil {
				t.Fatal(err)
			}
			if st.NextOffset != i*chunk {
				t.Fatalf("after the drop the upload resumes at %d, want %d", st.NextOffset, i*chunk)
			}
		}
		if i == restartAt {
			// The core restarts: a new service on the same database and staging volume.
			srv.Close()
			e.svc = e.reopen()
			srv = e.server(who)
			c.url = srv.URL
			var st Upload
			if _, err := c.json("GET", "/api/v1/file-uploads/"+string(up.ID), nil, &st); err != nil {
				t.Fatal(err)
			}
			if st.NextOffset != i*chunk {
				t.Fatalf("after restart next_offset = %d, want %d", st.NextOffset, i*chunk)
			}
		}
		var st Upload
		if _, err := c.do("PUT", "/api/v1/file-uploads/"+string(up.ID), bytes.NewReader(part), hdr, &st); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
		if st.NextOffset != i*chunk+int64(len(part)) {
			t.Fatalf("chunk %d next_offset = %d", i, st.NextOffset)
		}
	}
	wantSHA := hex.EncodeToString(want.Sum(nil))
	var done Upload
	if _, err := c.json("POST", "/api/v1/file-uploads/"+string(up.ID)+"/complete", struct{}{}, &done); err != nil {
		t.Fatal(err)
	}
	if done.Status != "staged" || done.SHA256 != wantSHA {
		t.Fatalf("complete = %+v, want sha %s", done, wantSHA)
	}
	var f FileView
	if _, err := c.json("POST", "/api/v1/files", map[string]any{"upload_id": up.ID}, &f); err != nil {
		t.Fatal(err)
	}
	// Publishing again replays the same file and adds no version.
	var again FileView
	if _, err := c.json("POST", "/api/v1/files", map[string]any{"upload_id": up.ID}, &again); err != nil || again.ID != f.ID {
		t.Fatalf("replayed publish = %v %v", again.ID, err)
	}
	var vs struct {
		Data []VersionView `json:"data"`
	}
	if _, err := c.json("GET", "/api/v1/files/"+string(f.ID)+"/versions", nil, &vs); err != nil || len(vs.Data) != 1 {
		t.Fatalf("versions = %+v %v", vs, err)
	}
	if vs.Data[0].SHA256 != wantSHA || vs.Data[0].SizeBytes != size {
		t.Fatalf("stored version = %+v, want %s", vs.Data[0], wantSHA)
	}
	resp, err := http.Get(c.url + "/api/v1/files/" + string(f.ID) + "/versions/" + string(vs.Data[0].ID) + "/download")
	if err != nil {
		t.Fatal(err)
	}
	got := sha256.New()
	n, err := io.Copy(got, resp.Body)
	resp.Body.Close()
	if err != nil || n != size || hex.EncodeToString(got.Sum(nil)) != wantSHA || resp.Header.Get("X-Content-SHA256") != wantSHA {
		t.Fatalf("download %d bytes sha %x err %v", n, got.Sum(nil), err)
	}
}

func TestPGUploadRefusals(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.set("files.upload_chunk_bytes", "4")
	e.set("files.max_upload_bytes", "20")
	if _, err := e.svc.CreateUpload(ctx, e.staff, UploadRequest{FileName: "big.bin", MatterID: e.open, SizeBytes: 21}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("over the largest upload = %v", err)
	}
	if _, err := e.svc.CreateUpload(ctx, e.accountant, UploadRequest{FileName: "a.bin", MatterID: e.open, SizeBytes: 1}); !isCode(err, domain.CodeForbidden) {
		t.Fatalf("accountant upload = %v", err)
	}
	if _, err := e.svc.CreateUpload(ctx, e.staff, UploadRequest{FileName: "../x", MatterID: e.open, SizeBytes: 1}); !isCode(err, domain.CodeValidation) {
		t.Fatalf("slash in name = %v", err)
	}
	body := "abcdefghij"
	up, err := e.svc.CreateUpload(ctx, e.staff, UploadRequest{FileName: "a.txt", MatterID: e.open, SizeBytes: int64(len(body)), SHA256: shaHex([]byte("something else"))})
	if err != nil {
		t.Fatal(err)
	}
	put := func(p domain.Principal, start, end int64, b string) error {
		_, err := e.svc.PutChunk(ctx, p, up.ID, start, end, int64(len(body)), strings.NewReader(b))
		return err
	}
	if err := put(e.attorney, 0, 3, body[:4]); !isCode(err, domain.CodeNotFound) {
		t.Fatalf("another user's upload = %v", err)
	}
	if err := put(e.staff, 4, 7, body[4:8]); !isCode(err, domain.CodeConflict) {
		t.Fatalf("out-of-order chunk = %v", err)
	}
	if err := put(e.staff, 0, 4, body[:5]); !isCode(err, domain.CodeValidation) {
		t.Fatalf("chunk over the chunk size = %v", err)
	}
	if err := put(e.staff, 0, 3, body[:2]); !isCode(err, domain.CodeValidation) {
		t.Fatalf("short body = %v", err)
	}
	if _, err := e.svc.CompleteUpload(ctx, e.staff, up.ID); !isCode(err, domain.CodeConflict) {
		t.Fatalf("complete before every byte = %v", err)
	}
	if _, err := e.svc.PublishUpload(ctx, e.staff, Publish{UploadID: up.ID}); !isCode(err, domain.CodeConflict) {
		t.Fatalf("publish before complete = %v", err)
	}
	for off := int64(0); off < int64(len(body)); off += 4 {
		end := off + 3
		if end >= int64(len(body)) {
			end = int64(len(body)) - 1
		}
		if err := put(e.staff, off, end, body[off:end+1]); err != nil {
			t.Fatal(err)
		}
	}
	// The declared SHA-256 does not match: the bytes are dropped and the upload restarts.
	st, err := e.svc.CompleteUpload(ctx, e.staff, up.ID)
	if !isCode(err, domain.CodeConflict) || st.NextOffset != 0 || st.Status != "receiving" {
		t.Fatalf("sha mismatch = %+v %v", st, err)
	}
	var n int
	_ = e.pool.QueryRowContext(ctx, `SELECT COUNT(*) FROM files`).Scan(&n)
	if n != 0 {
		t.Fatal("a refused upload created a file")
	}
}

func TestPGDuplicateSHAWarnsAndStillUploads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	first := e.create(e.staff, NewFile{Name: "lease.pdf", MatterID: e.open}, "same bytes")
	up, err := e.svc.CreateUpload(ctx, e.staff, UploadRequest{FileName: "lease-copy.pdf", MatterID: e.open, SizeBytes: 10, SHA256: shaHex([]byte("same bytes"))})
	if err != nil || len(up.Warnings) != 1 || !strings.Contains(up.Warnings[0], string(first.ID)) {
		t.Fatalf("upload warning = %+v %v", up, err)
	}
	if _, err := e.svc.PutChunk(ctx, e.staff, up.ID, 0, 9, 10, strings.NewReader("same bytes")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CompleteUpload(ctx, e.staff, up.ID); err != nil {
		t.Fatal(err)
	}
	f, err := e.svc.PublishUpload(ctx, e.staff, Publish{UploadID: up.ID})
	if err != nil || len(f.Warnings) != 1 || f.ID == first.ID {
		t.Fatalf("publish = %+v %v", f, err)
	}
	// A client's upload is received, with the client as uploader.
	cf, err := e.svc.Create(ctx, domain.Principal{Source: domain.SourceModule, ModuleID: "client-portal", UserID: e.staff.UserID, Roles: []domain.Role{domain.RoleStaff}},
		NewFile{Name: "photo.jpg", MatterID: e.open, Source: "client_upload"}, strings.NewReader("p"), 1)
	if err != nil || cf.Origin != "received" || cf.Status == nil || *cf.Status != "received" {
		t.Fatalf("client upload = %+v %v", cf, err)
	}
}

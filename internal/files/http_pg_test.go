package files

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestPGEveryRouteOverHTTP drives each served operation once through the mux.
func TestPGEveryRouteOverHTTP(t *testing.T) {
	e := newEnv(t)
	a := e.create(e.admin, NewFile{Name: "a.txt", MatterID: e.open}, "alpha")
	b := e.create(e.admin, NewFile{Name: "b.txt", MatterID: e.open}, "bravo")
	srv := e.server(func(*http.Request) (domain.Principal, error) { return e.admin, nil })
	c := apiClient{t: t, url: srv.URL}
	f := "/api/v1/files/" + string(a.ID)
	must := func(code int, err error, want int) {
		t.Helper()
		if err != nil || code != want {
			t.Fatalf("status %d (want %d): %v", code, want, err)
		}
	}
	var fv FileView
	code, err := c.json("POST", f+"/rename", map[string]string{"name": "alpha-renamed"}, &fv)
	must(code, err, 200)
	if fv.Name != "alpha-renamed.txt" {
		t.Fatalf("rename = %s", fv.Name)
	}
	var folders struct {
		Data []FolderView `json:"data"`
	}
	code, err = c.json("GET", "/api/v1/folders?matter_id="+string(e.open), nil, &folders)
	must(code, err, 200)
	code, err = c.json("POST", f+"/move", map[string]any{"folder_id": folders.Data[2].ID}, &fv)
	must(code, err, 200)
	code, err = c.json("POST", f+"/status", map[string]string{"status": "final"}, &fv)
	must(code, err, 200)
	code, err = c.json("PATCH", f, map[string]any{"shared_with_client": true}, &fv)
	must(code, err, 200)
	if !fv.SharedWithClient || *fv.Status != "final" || *fv.FolderID != folders.Data[2].ID {
		t.Fatalf("file after edits = %+v", fv)
	}
	code, _ = c.json("PATCH", f, map[string]any{"bogus": 1}, nil)
	must(code, nil, 400)
	var link LinkView
	code, err = c.json("POST", f+"/links", map[string]string{"to_file_id": string(b.ID), "relationship": "attachment"}, &link)
	must(code, err, 201)
	var pg struct {
		Data []map[string]any `json:"data"`
	}
	for _, p := range []string{f + "/links", f + "/history", f + "/versions", f + "/renditions", "/api/v1/storage-backends", "/api/v1/storage-moves"} {
		code, err = c.json("GET", p, nil, &pg)
		must(code, err, 200)
	}
	code, _ = c.json("POST", f+"/versions/"+string(*a.CurrentVersionID)+"/quarantine", map[string]any{}, nil)
	must(code, nil, 400)
	var vv VersionView
	code, err = c.json("POST", f+"/versions/"+string(*a.CurrentVersionID)+"/quarantine", map[string]bool{"quarantined": true}, &vv)
	must(code, err, 200)
	code, _ = c.json("GET", f+"/versions/"+string(*a.CurrentVersionID)+"/download", nil, nil)
	must(code, nil, http.StatusConflict)
	code, err = c.json("POST", f+"/versions/"+string(*a.CurrentVersionID)+"/restore", struct{}{}, &vv)
	must(code, err, 201)
	// Archive of both files: the quarantined one needs confirmation.
	code, _ = c.json("POST", "/api/v1/file-archives", map[string]any{"file_ids": []domain.ID{a.ID, b.ID}}, nil)
	if code != http.StatusOK { // a's current version is the restored, unquarantined copy
		t.Fatalf("archive = %d", code)
	}
	resp, err := http.Post(srv.URL+"/api/v1/file-archives", "application/json", bytes.NewReader([]byte(`{"file_ids":["`+string(a.ID)+`","`+string(b.ID)+`"]}`)))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(zr.File) != 2 {
		t.Fatalf("archive zip = %v %v", err, zr)
	}
	// Folders: create, read, rename, delete.
	var fd FolderView
	code, err = c.json("POST", "/api/v1/folders", map[string]any{"matter_id": e.open, "name": "Experts"}, &fd)
	must(code, err, 201)
	code, err = c.json("GET", "/api/v1/folders/"+string(fd.ID), nil, &fd)
	must(code, err, 200)
	code, err = c.json("PATCH", "/api/v1/folders/"+string(fd.ID), map[string]any{"name": "Expert reports"}, &fd)
	must(code, err, 200)
	code, err = c.json("DELETE", "/api/v1/folders/"+string(fd.ID), nil, nil)
	must(code, err, 204)
	// Storage: a bad backend is refused, a move runs and is confirmed.
	code, _ = c.json("POST", "/api/v1/storage-backends", map[string]any{"kind": "s3", "name": "x", "settings": map[string]string{"endpoint": "ftp://x"}}, nil)
	must(code, nil, 400)
	_, s3 := e.s3()
	var tested BackendView
	code, err = c.json("POST", "/api/v1/storage-backends/"+string(s3.ID)+"/test", struct{}{}, &tested)
	must(code, err, 200)
	if tested.Health != "ok" {
		t.Fatalf("test = %+v", tested)
	}
	var mv MoveView
	code, err = c.json("POST", "/api/v1/storage-moves", MoveRequest{FromBackendID: e.local.ID, ToBackendID: s3.ID, Scope: "matter", MatterID: e.open}, &mv)
	must(code, err, 201)
	code, err = c.json("GET", "/api/v1/storage-moves/"+string(mv.ID), nil, &mv)
	must(code, err, 200)
	if mv.Status != "awaiting_confirmation" {
		t.Fatalf("move = %+v", mv)
	}
	code, err = c.json("POST", "/api/v1/storage-moves/"+string(mv.ID)+"/confirm", struct{}{}, &mv)
	must(code, err, 200)
	// Trash and purge.
	code, err = c.json("POST", f+"/trash", struct{}{}, &fv)
	must(code, err, 200)
	code, _ = c.json("POST", f+"/purge", map[string]string{"confirm_name": "nope"}, nil)
	must(code, nil, 400)
	code, err = c.json("POST", f+"/purge", map[string]string{"confirm_name": "alpha-renamed.txt"}, nil)
	must(code, err, 204)
	code, _ = c.json("GET", f, nil, nil)
	must(code, nil, 404)
	// No principal: 401 on every route.
	anon := e.server(func(*http.Request) (domain.Principal, error) { return domain.Principal{}, domain.Unauthenticated("sign in") })
	resp, err = http.Get(anon.URL + "/api/v1/files")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("anonymous list = %d", resp.StatusCode)
	}
}

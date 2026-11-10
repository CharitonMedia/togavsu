package files

import (
	"context"
	"io"
	"testing"
)

func TestPGSmokeCreateReadVersion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	f := e.create(e.staff, NewFile{Name: "letter.docx", MatterID: e.open}, "hello world")
	d, err := e.svc.OpenVersion(ctx, e.staff, f.ID, *f.CurrentVersionID, false)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(d.Body)
	d.Close()
	if err != nil || string(body) != "hello world" {
		t.Fatalf("read %q %v", body, err)
	}
	if len(e.versions) != 1 {
		t.Fatalf("version events %v", e.versions)
	}
}

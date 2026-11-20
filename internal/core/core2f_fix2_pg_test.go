package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
	"github.com/CharitonMedia/togavsu/internal/testkit"
)

func TestCoreUpdate2FFix2ClientOwnContactAfterAccessWindow(t *testing.T) {
	f := newFin(t)
	matter, contact := f.matter("Synthetic expired client", "expired@client.example", nil)
	_, foreign := f.matter("Synthetic other client", "other@client.example", nil)
	cli, user := testkit.EnrollUser(t, f.app.Identity, f.srv.URL, f.admin.kit(), "Synthetic Client", "expired@client.example", domain.ID(contact), domain.RoleClient)
	client := wrapClient(cli)
	f.admin.must(http.StatusOK, "POST", "/api/v1/matters/"+matter+"/close", map[string]any{"reason": "done", "acknowledge": true})
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/client_access.days_after_close", map[string]any{"value": 0})
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	if _, err := f.app.Pool.Exec(`UPDATE practice_records SET body=jsonb_set(body::jsonb,'{ClosedOn}',to_jsonb($1::text))::text WHERE kind='matter' AND id=$2`, yesterday, matter); err != nil {
		t.Fatal(err)
	}
	f.restart()
	client.must(http.StatusNotFound, "GET", "/api/v1/matters/"+matter, nil)
	ctx := context.Background()
	// Contact-only reads remain available; contact-only client writes were already
	// forbidden at the target base, including authenticated module viewers.
	file, err := f.app.Files.Create(ctx, domain.Principal{UserID: domain.ID(f.adminID), Roles: []domain.Role{domain.RoleAdmin}, Source: domain.SourceUI}, files.NewFile{Name: "own.txt", ContactID: domain.ID(contact), SharedWithClient: true}, strings.NewReader("x"), 1)
	if err != nil {
		t.Fatal(err)
	}
	f.app.Files.SetModuleCheck(func(context.Context, string) (bool, error) { return true, nil })
	for _, source := range []domain.ActorSource{domain.SourceUI, domain.SourceModule} {
		p := domain.Principal{UserID: user, Roles: []domain.Role{domain.RoleClient}, Source: source}
		if source == domain.SourceModule {
			p.ModuleID = "client-portal"
		}
		if _, err := f.app.Files.Get(ctx, p, file.ID); err != nil {
			t.Errorf("own contact read: %v", err)
		}
		for _, in := range []files.UploadRequest{{FileName: "own.txt", ContactID: domain.ID(contact), SizeBytes: 1}, {FileName: "foreign.txt", ContactID: domain.ID(foreign), SizeBytes: 1}, {FileName: "expired.txt", MatterID: domain.ID(matter), SizeBytes: 1}} {
			if _, err := f.app.Files.CreateUpload(ctx, p, in); err == nil {
				t.Errorf("accepted inaccessible placement: %+v", in)
			}
		}
	}
}

func TestCoreUpdate2FFix2FileEventPolicyFailsClosed(t *testing.T) {
	var empty App
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := empty.fileReadableForModule(context.Background(), "synthetic", id); ok || err != nil {
		t.Fatalf("nil Files: %v %v", ok, err)
	}
	x := newExt(t)
	if ok, err := x.app.fileReadableForModule(context.Background(), "synthetic", id); ok || err != nil {
		t.Fatalf("missing file: %v %v", ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ok, err := x.app.fileReadableForModule(ctx, "synthetic", id); ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("lookup error: %v %v", ok, err)
	}
}

package marketplace

import (
	"encoding/base64"
	"net/http"
	"testing"

	shared "github.com/CharitonMedia/togavsu/internal/marketplace"
)

func TestCoreUpdate2FFix2PublishApprovedSources(t *testing.T) {
	e := newEnv(t)
	a := e.author("Synthetic Approved Sources", "approved-sources@example.test")
	for _, id := range []string{"email", "zoom", "importer"} {
		t.Run(id, func(t *testing.T) {
			p := pkg(t, id, "1.0.0", "files:read")
			body := map[string]any{"package_base64": base64.StdEncoding.EncodeToString(p), "signature": shared.Sign(a.priv, p), "key_id": a.keyID}
			if r := e.do("POST", "/v1/authors/me/packages", a.token, body); r.code != http.StatusCreated {
				t.Fatalf("publish %s: %d %s", id, r.code, r.body)
			}
			if r := e.do("GET", "/v1/listings/"+id, "", nil); r.code != http.StatusOK {
				t.Errorf("listing %s: %d %s", id, r.code, r.body)
			}
		})
	}
}

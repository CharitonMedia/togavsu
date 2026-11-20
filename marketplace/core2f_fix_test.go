package marketplace

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	shared "github.com/CharitonMedia/togavsu/internal/marketplace"
)

func TestCoreUpdate2FFixPublishReservedSources(t *testing.T) {
	e := newEnv(t)
	a := e.author("Synthetic Sources", "sources@example.test")
	for _, id := range []string{"core", "upload", "client_upload", "editor", "billing", "email", "zoom", "drop_folder", "importer", "ui", "api_token", "module", "service", "system", "api", "manual", "timer", "import"} {
		p := pkg(t, id, "1.0.0", "files:read")
		for _, metadata := range []bool{false, true} {
			body := map[string]any{"package_base64": base64.StdEncoding.EncodeToString(p), "signature": shared.Sign(a.priv, p), "key_id": a.keyID}
			if metadata {
				body["listing_id"], body["version"] = id, "1.0.0"
			}
			r := e.do("POST", "/v1/authors/me/packages", a.token, body)
			if r.code != http.StatusBadRequest || !strings.Contains(string(r.body), "reserved") {
				t.Errorf("%s metadata=%v: %d %s", id, metadata, r.code, r.body)
			}
		}
		if r := e.do("GET", "/v1/listings/"+id, "", nil); r.code != http.StatusNotFound {
			t.Errorf("reserved %s listed: %d", id, r.code)
		}
	}
}

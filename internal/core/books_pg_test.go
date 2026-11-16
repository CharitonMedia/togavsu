package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/modules"
)

// TestCORE02BooksReads covers the books reader (CORE-02): module-only books:read,
// service money reads with walls, books profile/matter/contact, writes refused, and
// ordinary API tokens refused books:read.
func TestCORE02BooksReads(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		f[modules.ManifestFile] = bytes.Replace(f[modules.ManifestFile], []byte("permissions:\n"),
			[]byte("permissions:\n  - scope: books:read\n    reason: Read the books.\n"+
				"  - scope: invoices:read\n    reason: List invoices.\n"+
				"  - scope: payments:read\n    reason: List payments.\n"+
				"  - scope: expenses:read\n    reason: List expenses.\n"+
				"  - scope: trust:read\n    reason: Read trust.\n"+
				"  - scope: events:write\n    reason: Append.\n"), 1)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("matter-clock")

	open, openClient := x.matter("Open Books Estate", "open.books@client.example", nil)
	walled, walledClient := x.matter("Walled Books Estate", "walled.books@client.example", nil)
	if err := x.app.Practice.SetWall(ctx, x.adminP(), domain.ID(walled), []domain.ID{domain.ID(x.attyID)}); err != nil {
		t.Fatal(err)
	}

	x.timeEntry(open, 2, 200_00)
	x.timeEntry(walled, 4, 200_00)
	openInv := items(t, x.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": open}))[0]["id"].(string)
	walledInv := items(t, x.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": walled}))[0]["id"].(string)

	bot := newClient(t, x.srv.URL)
	bot.bearer = x.rt.Last("matter-clock").Secrets["togavsu_api_token"]

	prof := bot.must(http.StatusOK, "GET", "/api/v1/books/profile", nil)
	if str(t, prof.json, "currency") != "USD" || str(t, prof.json, "firm_name") == "" || str(t, prof.json, "time_zone") == "" {
		t.Fatalf("profile: %s", prof.body)
	}

	bm := bot.must(http.StatusOK, "GET", "/api/v1/books/matters/"+open, nil)
	if str(t, bm.json, "id") != open || str(t, bm.json, "client_contact_id") != openClient {
		t.Fatalf("books matter: %s", bm.body)
	}
	if r := bot.do("GET", "/api/v1/books/matters/"+walled, nil); r.code != http.StatusNotFound {
		t.Fatalf("walled books matter: %d %s", r.code, r.body)
	}

	bc := bot.must(http.StatusOK, "GET", "/api/v1/books/contacts/"+openClient, nil)
	if str(t, bc.json, "id") != openClient || str(t, bc.json, "email") != "open.books@client.example" {
		t.Fatalf("books contact: %s", bc.body)
	}
	if strings.Contains(bc.body, "date_of_birth") || strings.Contains(bc.body, "identifier") {
		t.Fatalf("books contact leaked fields: %s", bc.body)
	}
	if r := bot.do("GET", "/api/v1/books/contacts/"+walledClient, nil); r.code != http.StatusNotFound {
		t.Fatalf("walled books contact: %d %s", r.code, r.body)
	}

	listed := bot.must(http.StatusOK, "GET", "/api/v1/invoices", nil)
	if strings.Contains(listed.body, walledInv) || !strings.Contains(listed.body, openInv) {
		t.Fatalf("invoice list: %s", listed.body)
	}
	bot.must(http.StatusOK, "GET", "/api/v1/invoices/"+openInv, nil)
	if r := bot.do("GET", "/api/v1/invoices/"+walledInv, nil); r.code != http.StatusNotFound {
		t.Fatalf("walled invoice get: %d %s", r.code, r.body)
	}

	if r := bot.do("POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": open}); r.code != http.StatusForbidden {
		t.Fatalf("books reader write: %d %s", r.code, r.body)
	}

	if r := x.admin.do("POST", "/api/v1/api-tokens", map[string]any{
		"name": "human-books", "user_id": x.adminID, "scopes": []string{"books:read"},
	}); r.code == http.StatusCreated || (!strings.Contains(r.body, "scope_denied") && r.json["code"] != "scope_denied") {
		t.Fatalf("human token books:read: %d %s", r.code, r.body)
	}

	pkg2 := x.build(func(f map[string][]byte) {
		m := string(f[modules.ManifestFile])
		m = strings.Replace(m, "id: matter-clock", "id: no-books", 1)
		m = strings.Replace(m, "name: Matter Clock", "name: No Books", 1)
		m = strings.Replace(m, "permissions:\n",
			"permissions:\n  - scope: invoices:read\n    reason: List invoices.\n", 1)
		f[modules.ManifestFile] = []byte(m)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg2); problem != "" {
		t.Fatalf("install no-books: %s", problem)
	}
	x.approveOnTab("no-books")
	nobooks := newClient(t, x.srv.URL)
	nobooks.bearer = x.rt.Last("no-books").Secrets["togavsu_api_token"]
	if r := nobooks.do("GET", "/api/v1/invoices", nil); r.code != http.StatusForbidden || (r.json["code"] != "scope_denied" && r.json["code"] != "forbidden") {
		t.Fatalf("money read without books:read: %d %s", r.code, r.body)
	}
	if r := nobooks.do("GET", "/api/v1/books/profile", nil); r.code != http.StatusForbidden {
		t.Fatalf("books profile without books:read: %d %s", r.code, r.body)
	}
}

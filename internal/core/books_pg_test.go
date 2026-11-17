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

	x.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+openInv+"/approve", nil)
	x.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+walledInv+"/approve", nil)
	openSent := x.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+openInv+"/send", map[string]any{"deliver": "file"})
	walledSent := x.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+walledInv+"/send", map[string]any{"deliver": "file"})
	openPay := x.admin.must(http.StatusCreated, "POST", "/api/v1/payments", map[string]any{
		"contact_id": openClient, "matter_id": open, "amount_cents": int64(openSent.json["total_cents"].(float64)),
		"destination": "operating", "method": "check", "invoice_id": openInv,
	})
	walledPay := x.atty.must(http.StatusCreated, "POST", "/api/v1/payments", map[string]any{
		"contact_id": walledClient, "matter_id": walled, "amount_cents": int64(walledSent.json["total_cents"].(float64)),
		"destination": "operating", "method": "check", "invoice_id": walledInv,
	})
	pays := bot.must(http.StatusOK, "GET", "/api/v1/payments", nil)
	if strings.Contains(pays.body, str(t, walledPay.json, "id")) || !strings.Contains(pays.body, str(t, openPay.json, "id")) {
		t.Fatalf("payment list: %s", pays.body)
	}
	if r := bot.do("GET", "/api/v1/payments/"+str(t, walledPay.json, "id"), nil); r.code != http.StatusNotFound {
		t.Fatalf("walled payment get: %d %s", r.code, r.body)
	}

	expType := str(t, x.admin.must(http.StatusCreated, "POST", "/api/v1/expense-types", map[string]any{"name": "Books copy fee"}).json, "id")
	openExp := x.staff.must(http.StatusCreated, "POST", "/api/v1/expenses", map[string]any{
		"matter_id": open, "amount_cents": 12_00, "description": "Open copy fee", "incurred_on": "2026-06-01", "expense_type_id": expType,
	})
	walledExp := x.atty.must(http.StatusCreated, "POST", "/api/v1/expenses", map[string]any{
		"matter_id": walled, "amount_cents": 14_00, "description": "Walled copy fee", "incurred_on": "2026-06-01", "expense_type_id": expType,
	})
	exps := bot.must(http.StatusOK, "GET", "/api/v1/expenses", nil)
	if strings.Contains(exps.body, str(t, walledExp.json, "id")) || !strings.Contains(exps.body, str(t, openExp.json, "id")) {
		t.Fatalf("expense list: %s", exps.body)
	}
	if r := bot.do("GET", "/api/v1/expenses/"+str(t, walledExp.json, "id"), nil); r.code != http.StatusNotFound {
		t.Fatalf("walled expense get: %d %s", r.code, r.body)
	}

	x.admin.must(http.StatusCreated, "POST", "/api/v1/payments", map[string]any{
		"contact_id": openClient, "matter_id": open, "amount_cents": 50_00,
		"destination": "trust", "trust_account_id": x.account, "method": "ach",
	})
	x.atty.must(http.StatusCreated, "POST", "/api/v1/payments", map[string]any{
		"contact_id": walledClient, "matter_id": walled, "amount_cents": 50_00,
		"destination": "trust", "trust_account_id": x.account, "method": "ach",
	})
	ledger := bot.must(http.StatusOK, "GET", "/api/v1/trust-ledger", nil)
	if strings.Contains(ledger.body, walled) || !strings.Contains(ledger.body, open) {
		t.Fatalf("trust ledger: %s", ledger.body)
	}
	bals := bot.must(http.StatusOK, "GET", "/api/v1/trust/balances", nil)
	if strings.Contains(bals.body, walled) || !strings.Contains(bals.body, open) {
		t.Fatalf("trust balances: %s", bals.body)
	}

	// Lifting the wall returns the walled records on the next read (CU-02).
	if err := x.app.Practice.SetWall(ctx, x.adminP(), domain.ID(walled), nil); err != nil {
		t.Fatal(err)
	}
	listedAfter := bot.must(http.StatusOK, "GET", "/api/v1/invoices", nil)
	if !strings.Contains(listedAfter.body, walledInv) {
		t.Fatalf("invoice list after wall lift: %s", listedAfter.body)
	}
	bot.must(http.StatusOK, "GET", "/api/v1/invoices/"+walledInv, nil)
	bot.must(http.StatusOK, "GET", "/api/v1/payments/"+str(t, walledPay.json, "id"), nil)
	bot.must(http.StatusOK, "GET", "/api/v1/expenses/"+str(t, walledExp.json, "id"), nil)

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

	// Staff session, client session and viewer-context calls must not read books PII.
	for _, c := range []*client{x.staff, x.admin} {
		if r := c.do("GET", "/api/v1/books/matters/"+open, nil); r.code != http.StatusForbidden {
			t.Fatalf("session books matter: %d %s", r.code, r.body)
		}
		if r := c.do("GET", "/api/v1/books/contacts/"+openClient, nil); r.code != http.StatusForbidden {
			t.Fatalf("session books contact: %d %s", r.code, r.body)
		}
		if r := c.do("GET", "/api/v1/books/profile", nil); r.code != http.StatusForbidden {
			t.Fatalf("session books profile: %d %s", r.code, r.body)
		}
	}
	cli, _ := enrollUser(t, x.app, x.srv.URL, x.admin, "Books Client", "books.client@example.com", domain.RoleClient)
	if r := cli.do("GET", "/api/v1/books/contacts/"+openClient, nil); r.code != http.StatusForbidden {
		t.Fatalf("client books contact: %d %s", r.code, r.body)
	}
	ct := x.staff.must(http.StatusCreated, "POST", "/api/v1/modules/matter-clock/context-tokens", map[string]any{})
	tok := str(t, ct.json, "token")
	if r := bot.do("GET", "/api/v1/books/contacts/"+openClient, nil, modules.ContextHeader, tok); r.code != http.StatusForbidden {
		t.Fatalf("viewer-context books contact: %d %s", r.code, r.body)
	}
	if r := bot.do("GET", "/api/v1/books/matters/"+open, nil, modules.ContextHeader, tok); r.code != http.StatusForbidden {
		t.Fatalf("viewer-context books matter: %d %s", r.code, r.body)
	}
}

// TestCORE02BooksContactUpdatedPump: a books:read module subscribed to contact.updated
// must not stall the pump when the contact is not an unwalled client. Editing a
// non-client contact and a payment-only contact both leave the cursor advancing
// (Codex finding: empty ModuleID made payment/trust lookups unauthenticated).
func TestCORE02BooksContactUpdatedPump(t *testing.T) {
	x := newExt(t)
	ctx := context.Background()
	x.trustKey("Sample module key", x.priv.Public().(ed25519.PublicKey))
	pkg := x.build(func(f map[string][]byte) {
		m := string(f[modules.ManifestFile])
		m = strings.Replace(m, "id: matter-clock", "id: books-sync", 1)
		m = strings.Replace(m, "name: Matter Clock", "name: Books Sync", 1)
		m = strings.Replace(m, "permissions:\n",
			"permissions:\n  - scope: books:read\n    reason: Filter books contact events.\n"+
				"  - scope: payments:read\n    reason: See payer contacts.\n", 1)
		m = strings.Replace(m, "events:\n  - time_entry.*\n  - matter.created\n",
			"events:\n  - contact.updated\n  - matter.created\n", 1)
		f[modules.ManifestFile] = []byte(m)
	})
	if _, problem := x.tabForm(x.admin, "install", nil, pkg); problem != "" {
		t.Fatalf("install: %s", problem)
	}
	x.approveOnTab("books-sync")

	if _, err := x.app.Modules.Pump(ctx); err != nil {
		t.Fatalf("drain setup events: %v", err)
	}

	matter, clientID := x.matter("Books Event Estate", "books.event.client@example.com", nil)
	orphan := str(t, x.admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{
		"kind": "person", "given_name": "Orphan", "family_name": "Witness",
	}).json, "id")
	payer := str(t, x.admin.must(http.StatusCreated, "POST", "/api/v1/contacts", map[string]any{
		"kind": "person", "given_name": "Payer", "family_name": "Only",
	}).json, "id")
	x.admin.must(http.StatusCreated, "POST", "/api/v1/matter-parties", map[string]any{
		"matter_id": matter, "contact_id": payer, "role": "opposing party",
	})
	x.admin.must(http.StatusCreated, "POST", "/api/v1/payments", map[string]any{
		"contact_id": payer, "matter_id": matter, "amount_cents": 25_00,
		"destination": "operating", "method": "check",
	})

	x.admin.must(http.StatusOK, "PATCH", "/api/v1/contacts/"+orphan, map[string]any{"preferred_name": "Orphaned"})
	orphanEv := x.count(`SELECT COUNT(*) FROM core.events WHERE action = 'contact.updated' AND record_id = $1`, orphan)
	if orphanEv == 0 {
		t.Fatal("expected contact.updated for the non-client orphan")
	}
	if _, err := x.app.Modules.Pump(ctx); err != nil {
		t.Fatalf("pump after non-client contact.updated: %v", err)
	}
	if n := x.count(`SELECT COUNT(*) FROM core.module_deliveries WHERE module_id = 'books-sync' AND action = 'contact.updated' AND payload::text LIKE $1`, "%"+orphan+"%"); n != 0 {
		t.Fatalf("books module should drop orphan contact.updated, got %d deliveries", n)
	}

	x.admin.must(http.StatusOK, "PATCH", "/api/v1/contacts/"+payer, map[string]any{"preferred_name": "Paid"})
	if _, err := x.app.Modules.Pump(ctx); err != nil {
		t.Fatalf("pump after payment-only contact.updated: %v", err)
	}
	if n := x.count(`SELECT COUNT(*) FROM core.module_deliveries WHERE module_id = 'books-sync' AND action = 'contact.updated' AND payload::text LIKE $1`, "%"+payer+"%"); n != 1 {
		t.Fatalf("payment-only contact.updated deliveries=%d want 1", n)
	}

	// Cursor kept advancing: a later matter.created still queues after the contact rows.
	later, _ := x.matter("Books Event Later", "books.event.later@example.com", nil)
	if n, err := x.app.Modules.Pump(ctx); err != nil || n == 0 {
		t.Fatalf("pump after later matter: queued=%d err=%v (proves cursor advanced past contact.updated)", n, err)
	}
	if n := x.count(`SELECT COUNT(*) FROM core.module_deliveries WHERE module_id = 'books-sync' AND action = 'matter.created' AND payload::text LIKE $1`, "%"+later+"%"); n != 1 {
		t.Fatalf("later matter.created deliveries=%d want 1", n)
	}
	_ = clientID
}

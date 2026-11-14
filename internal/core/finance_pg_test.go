package core

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/testkit"
)

// fakeSMTP is a minimal SMTP server on loopback that records every message. With reject
// set it refuses every recipient, so a send fails the way an unreachable relay does.
type fakeSMTP struct {
	ln     net.Listener
	mu     sync.Mutex
	msgs   []smtpMsg
	reject bool
}

type smtpMsg struct {
	From string
	To   []string
	Data string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeSMTP{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *fakeSMTP) port() string { return strconv.Itoa(s.ln.Addr().(*net.TCPAddr).Port) }

func (s *fakeSMTP) setReject(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reject = v
}

func (s *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	rd := bufio.NewReader(c)
	say := func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
	say("220 fake.smtp ESMTP")
	var cur smtpMsg
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250-fake.smtp")
			say("250 8BITMIME")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			from := strings.TrimSpace(line[10:])
			if i := strings.Index(from, ">"); i >= 0 {
				from = from[:i]
			}
			cur = smtpMsg{From: strings.TrimPrefix(from, "<")}
			say("250 OK")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			s.mu.Lock()
			reject := s.reject
			s.mu.Unlock()
			if reject {
				say("550 mailbox unavailable")
				continue
			}
			cur.To = append(cur.To, strings.Trim(strings.TrimSpace(line[8:]), "<>\r\n"))
			say("250 OK")
		case cmd == "DATA":
			say("354 go ahead")
			var b strings.Builder
			for {
				l, err := rd.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
				b.WriteString(l)
			}
			cur.Data = b.String()
			s.mu.Lock()
			s.msgs = append(s.msgs, cur)
			s.mu.Unlock()
			say("250 queued")
		case cmd == "RSET", cmd == "NOOP":
			say("250 OK")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("502 not implemented")
		}
	}
}

func (s *fakeSMTP) messages() []smtpMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]smtpMsg(nil), s.msgs...)
}

// to returns the messages delivered to addr.
func (s *fakeSMTP) to(addr string) []smtpMsg {
	var out []smtpMsg
	for _, m := range s.messages() {
		for _, r := range m.To {
			if strings.EqualFold(r, addr) {
				out = append(out, m)
			}
		}
	}
	return out
}

// fin is a ready install with the four staff roles, system mail and one IOLTA account.
type fin struct {
	t        *testing.T
	app      *App
	cfg      Config
	handler  atomic.Pointer[http.Handler]
	srv      *httptest.Server
	smtp     *fakeSMTP
	admin    *client
	atty     *client
	staff    *client
	acct     *client
	adminID  string
	attyID   string
	area     string
	account  string
	activity string
}

const (
	attyEmail  = "atty@firm.example"
	adminEmail = "avery@firm.example"
)

// appRoleDSN is testDSN's database owned by and reached through a fresh non-superuser
// login role, set up the way deploy/postgres/core-role.sh sets up togavsu_app (CREATEROLE
// for module logins, nothing else), so the
// append-only privileges on audit_log and trust_ledger hold as they do in Compose.
func appRoleDSN(t *testing.T) string {
	t.Helper()
	return testkit.AppRole(t, testDSN(t))
}

func newFin(t *testing.T, cfg ...func(*Config)) *fin {
	t.Helper()
	c := Config{DatabaseURL: appRoleDSN(t), Key: testKey, Domain: "firm.example", MailEvery: -1, DataDir: t.TempDir()}
	for _, f := range cfg {
		f(&c)
	}
	app, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	f := &fin{t: t, app: app, cfg: c, smtp: newFakeSMTP(t)}
	h := app.Handler()
	f.handler.Store(&h)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { (*f.handler.Load()).ServeHTTP(w, r) }))
	f.srv = srv
	t.Cleanup(func() {
		srv.Close()
		_ = f.app.Close()
	})
	f.admin = firstRun(t, srv.URL)
	f.adminID = str(t, f.admin.must(http.StatusOK, "GET", "/api/v1/me", nil).json, "id")
	var attyID, ignore domain.ID
	f.atty, attyID = enrollUser(t, app, srv.URL, f.admin, "Ada Attorney", attyEmail, domain.RoleAttorney)
	f.attyID = string(attyID)
	f.staff, ignore = enrollUser(t, app, srv.URL, f.admin, "Sam Staff", "sam@firm.example", domain.RoleStaff)
	f.acct, ignore = enrollUser(t, app, srv.URL, f.admin, "Ari Accountant", "ari@firm.example", domain.RoleAccountant)
	_ = ignore
	for k, v := range map[string]any{"mail.smtp_host": "127.0.0.1", "mail.smtp_port": f.smtp.port(), "mail.smtp_from": "billing@firm.example",
		// The fake mail server offers no STARTTLS; TestFinanceInvoiceWorkflows checks the default.
		"mail.smtp_starttls": false} {
		f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/"+k, map[string]any{"value": v})
	}
	f.area = str(t, f.admin.must(http.StatusCreated, "POST", "/api/v1/practice-areas", map[string]any{"name": "Probate", "stages": []string{"Intake"}}).json, "id")
	f.account = str(t, f.admin.must(http.StatusCreated, "POST", "/api/v1/trust-accounts", map[string]any{
		"bank": "Synthetic Bank", "name": "IOLTA", "iolta": true, "last_four": "4242"}).json, "id")
	f.activity = str(t, f.admin.must(http.StatusCreated, "POST", "/api/v1/activity-types", map[string]any{"name": "Drafting"}).json, "id")
	return f
}

// matter opens a matter with a client party who has an email address and returns the
// matter and client ids. extra fields go on the matter body.
func (f *fin) matter(name, clientEmail string, extra map[string]any) (matter, clientID string) {
	f.t.Helper()
	return testkit.Matter(f.t, f.admin.kit(), f.area, f.attyID, name, clientEmail, extra)
}

// restart closes the core and opens a new one on the same database behind the same URL,
// so the clients' sessions carry over.
func (f *fin) restart() {
	f.t.Helper()
	if err := f.app.Close(); err != nil {
		f.t.Fatal(err)
	}
	app, err := Open(context.Background(), f.cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.app = app
	h := app.Handler()
	f.handler.Store(&h)
}

// superuser opens this test's database as the Postgres superuser, for privilege changes
// the core role cannot make.
func (f *fin) superuser() *sql.DB {
	f.t.Helper()
	var name string
	if err := f.app.Pool.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		f.t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TOGAVSU_TEST_DATABASE_URL"))
	if err != nil {
		f.t.Fatal(err)
	}
	u.Path = "/" + name
	super, err := db.OpenPostgres(u.String(), "core")
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = super.Close() })
	return super
}

func (f *fin) entry(c *client, matter, clientID, typ string, cents int64) resp {
	f.t.Helper()
	return c.do("POST", "/api/v1/trust-ledger", map[string]any{"trust_account_id": f.account, "client_id": clientID, "matter_id": matter,
		"entry_type": typ, "amount_cents": cents, "method": "check", "description": "synthetic " + typ})
}

func (f *fin) post(c *client, matter, clientID, typ string, cents int64) string {
	f.t.Helper()
	r := f.entry(c, matter, clientID, typ, cents)
	if r.code != http.StatusCreated {
		f.t.Fatalf("%s %d: %d %s", typ, cents, r.code, r.body)
	}
	return str(f.t, r.json, "entry", "id")
}

func (f *fin) count(q string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.app.Pool.QueryRow(q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func num(t *testing.T, m map[string]any, keys ...string) int64 {
	t.Helper()
	var cur any = m
	for _, k := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("no %s in %v", k, m)
		}
		cur = obj[k]
	}
	v, ok := cur.(float64)
	if !ok {
		t.Fatalf("%v is not a number in %v", keys, m)
	}
	return int64(v)
}

func items(t *testing.T, r resp) []map[string]any {
	t.Helper()
	raw, _ := r.json["data"].([]any)
	if raw == nil {
		raw, _ = r.json["items"].([]any)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		if m, ok := v.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func (f *fin) timeEntry(matter string, increments int, rate int64) string {
	f.t.Helper()
	return str(f.t, f.atty.must(http.StatusCreated, "POST", "/api/v1/time-entries", map[string]any{
		"matter_id": matter, "worked_on": time.Now().UTC().Format("2006-01-02"), "increments": increments,
		"description": "Synthetic drafting", "activity_type_id": f.activity, "billable": true, "rate_cents": rate}).json, "id")
}

// TestFinanceInvoiceWorkflows is AC-008, AC-009 and AC-026 on the running core: invoices
// draft from unbilled time, approve, go out by system mail with the PDF, take a payment
// and void back to unbilled; a matter with an unpaid invoice and trust left closes after
// a warning; a flat fee in trust shows Ready to apply, applies and is Collected.
func TestFinanceInvoiceWorkflows(t *testing.T) {
	f := newFin(t)

	// AC-008: draft from unbilled time.
	matter, clientID := f.matter("Estate of Harlow", "harlow@client.example", nil)
	t1, t2 := f.timeEntry(matter, 10, 300_00), f.timeEntry(matter, 5, 300_00)
	unbilled := f.admin.must(http.StatusOK, "GET", "/api/v1/reports/unbilled-time?matter_id="+matter, nil)
	if !strings.Contains(unbilled.body, matter) {
		t.Fatalf("unbilled report misses the matter: %s", unbilled.body)
	}
	if r := f.acct.do("POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter}); r.code != http.StatusForbidden {
		t.Fatalf("accountant drafted: %d %s", r.code, r.body)
	}
	drafts := f.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter})
	list := items(t, drafts)
	if len(list) != 1 {
		t.Fatalf("want one draft: %s", drafts.body)
	}
	inv := list[0]
	invID := inv["id"].(string)
	increment, err := strconv.ParseInt(fmt.Sprint(f.admin.must(http.StatusOK, "GET", "/api/v1/settings/billing.time_increment_minutes", nil).json["value"]), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	want := (15 * increment * 300_00) / 60
	if inv["status"] != "draft" || int64(inv["total_cents"].(float64)) != want {
		t.Fatalf("draft total: want %d, got %v", want, inv)
	}
	for _, id := range []string{t1, t2} {
		e := f.admin.must(http.StatusOK, "GET", "/api/v1/time-entries/"+id, nil)
		if e.json["invoice_id"] != invID {
			t.Fatalf("time entry %s is not on the draft: %s", id, e.body)
		}
	}

	// Approve: staff cannot, an attorney can. A draft cannot be sent before approval.
	if r := f.staff.do("POST", "/api/v1/invoices/"+invID+"/approve", nil); r.code != http.StatusForbidden {
		t.Fatalf("staff approved: %d %s", r.code, r.body)
	}
	if r := f.atty.do("POST", "/api/v1/invoices/"+invID+"/send", map[string]any{"deliver": "email"}); r.code != http.StatusConflict {
		t.Fatalf("unapproved send: %d %s", r.code, r.body)
	}
	f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+invID+"/approve", nil)

	// With mail.smtp_starttls on (the default), a relay that offers no STARTTLS gets no
	// invoice: the send fails and the invoice stays a draft.
	relayed := len(f.smtp.messages())
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/mail.smtp_starttls", map[string]any{"value": true})
	if r := f.atty.do("POST", "/api/v1/invoices/"+invID+"/send", map[string]any{"deliver": "email"}); r.code < 400 {
		t.Fatalf("send to a relay without STARTTLS succeeded: %d %s", r.code, r.body)
	}
	if r := f.admin.must(http.StatusOK, "GET", "/api/v1/invoices/"+invID, nil); r.json["status"] != "draft" || len(f.smtp.messages()) != relayed {
		t.Fatalf("a send without STARTTLS left status %v and %d new messages at the relay", r.json["status"], len(f.smtp.messages())-relayed)
	}
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/mail.smtp_starttls", map[string]any{"value": false})

	// A relay that refuses the mail leaves the invoice unsent, with the undo on record.
	f.smtp.setReject(true)
	if r := f.atty.do("POST", "/api/v1/invoices/"+invID+"/send", map[string]any{"deliver": "email"}); r.code < 400 {
		t.Fatalf("send through a refusing relay succeeded: %d %s", r.code, r.body)
	}
	if r := f.admin.must(http.StatusOK, "GET", "/api/v1/invoices/"+invID, nil); r.json["status"] != "draft" {
		t.Fatalf("failed send left status %v", r.json["status"])
	}
	f.smtp.setReject(false)

	sent := f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+invID+"/send", map[string]any{"deliver": "email"})
	if sent.json["status"] != "sent" || sent.json["sent_at"] == nil {
		t.Fatalf("send: %s", sent.body)
	}
	mail := f.smtp.to("harlow@client.example")
	if len(mail) != 1 {
		t.Fatalf("want one invoice mail to the client, got %d", len(mail))
	}
	number := str(t, sent.json, "number")
	if m := mail[0].Data; !strings.Contains(m, "Subject: Synthetic Firm LLP invoice "+number) || !strings.Contains(m, "application/pdf") ||
		!strings.Contains(m, "filename=invoice-"+number+".pdf") || !strings.Contains(m, "JVBERi0") {
		t.Fatalf("invoice mail lacks the subject or the PDF attachment:\n%.600s", m)
	}
	if mail[0].From != "billing@firm.example" {
		t.Fatalf("mail from %q", mail[0].From)
	}
	if pdf := f.admin.do("GET", "/api/v1/invoices/"+invID+"/pdf", nil); pdf.code != http.StatusOK || !strings.HasPrefix(pdf.body, "%PDF") {
		t.Fatalf("pdf download: %d", pdf.code)
	}

	// Accept a payment: part, then the rest; the invoice is paid.
	half := want / 2
	pay := func(amount int64) string {
		r := f.admin.must(http.StatusCreated, "POST", "/api/v1/payments", map[string]any{"contact_id": clientID, "matter_id": matter, "amount_cents": amount,
			"destination": "operating", "method": "check", "reference_number": "1001", "invoice_id": invID})
		return str(t, r.json, "id")
	}
	p1 := pay(half)
	if r := f.admin.must(http.StatusOK, "GET", "/api/v1/invoices/"+invID, nil); num(t, r.json, "balance_due_cents") != want-half || r.json["status"] != "sent" {
		t.Fatalf("after a partial payment: %s", r.body)
	}
	if r := f.admin.do("POST", "/api/v1/invoices/"+invID+"/void", map[string]any{"reason": "paid in part"}); r.code != http.StatusConflict {
		t.Fatalf("void with a payment: %d %s", r.code, r.body)
	}
	p2 := pay(want - half)
	if r := f.admin.must(http.StatusOK, "GET", "/api/v1/invoices/"+invID, nil); r.json["status"] != "paid" || num(t, r.json, "balance_due_cents") != 0 {
		t.Fatalf("after full payment: %s", r.body)
	}
	if r := f.admin.must(http.StatusOK, "GET", "/api/v1/reports/payments-received", nil); !strings.Contains(r.body, p1) || !strings.Contains(r.body, p2) {
		t.Fatalf("payments report: %s", r.body)
	}

	// Void back to unbilled: reverse both payments, void, and the time is unbilled again.
	for _, p := range []string{p1, p2} {
		f.admin.must(http.StatusOK, "POST", "/api/v1/payments/"+p+"/reverse", map[string]any{"reason": "bounced"})
	}
	voided := f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+invID+"/void", map[string]any{"reason": "issued in error"})
	if voided.json["status"] != "void" {
		t.Fatalf("void: %s", voided.body)
	}
	for _, id := range []string{t1, t2} {
		if e := f.admin.must(http.StatusOK, "GET", "/api/v1/time-entries/"+id, nil); e.json["invoice_id"] != nil {
			t.Fatalf("time entry %s still on the void invoice: %s", id, e.body)
		}
	}
	redraft := items(t, f.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter}))
	if len(redraft) != 1 || int64(redraft[0]["total_cents"].(float64)) != want {
		t.Fatalf("voided time did not redraft: %v", redraft)
	}
	hist := f.admin.must(http.StatusOK, "GET", "/api/v1/invoices/"+invID+"/history", nil)
	for _, w := range []string{"approved", "void", "issued in error"} {
		if !strings.Contains(hist.body, w) {
			t.Fatalf("invoice history lacks %q: %s", w, hist.body)
		}
	}

	// AC-009: an unpaid invoice and a leftover trust balance warn, and the matter closes.
	approved := redraft[0]["id"].(string)
	f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+approved+"/approve", nil)
	f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+approved+"/send", map[string]any{"deliver": "file"})
	f.post(f.admin, matter, clientID, "deposit", 125_00)
	preview := f.atty.must(http.StatusOK, "GET", "/api/v1/matters/"+matter+"/close-preview", nil)
	for _, code := range []string{"unpaid_invoice", "nonzero_trust"} {
		if !strings.Contains(preview.body, code) {
			t.Fatalf("close preview lacks %s: %s", code, preview.body)
		}
	}
	page := f.atty.must(http.StatusOK, "GET", "/practice/matters/"+matter+"/close", nil)
	if !strings.Contains(page.body, "unpaid invoice") || !strings.Contains(page.body, "trust balance") {
		t.Fatalf("close screen lacks the warnings: %.800s", page.body)
	}
	closed := f.atty.must(http.StatusOK, "POST", "/api/v1/matters/"+matter+"/close", map[string]any{"reason": "estate settled", "acknowledge": true})
	if str(t, closed.json, "matter", "status") != "closed" || closed.json["acknowledged"] != true || !strings.Contains(closed.body, "nonzero_trust") {
		t.Fatalf("close: %s", closed.body)
	}

	// AC-026: a flat-fee matter with the fee in trust is Ready to apply, then Collected.
	flat, flatClient := f.matter("Will of Pell", "pell@client.example", map[string]any{"billing_type": "flat", "flat_fee_cents": 1500_00})
	status := func() string {
		wl := f.atty.must(http.StatusOK, "GET", "/api/v1/billing/worklist", nil)
		rows, _ := wl.json["flat_fee"].([]any)
		for _, r := range rows {
			if m := r.(map[string]any); m["matter_id"] == flat {
				return m["status"].(string)
			}
		}
		t.Fatalf("flat matter missing from the worklist: %s", wl.body)
		return ""
	}
	if s := status(); s != "awaiting_trust_funds" {
		t.Fatalf("before the deposit: %s", s)
	}
	f.post(f.admin, flat, flatClient, "deposit", 1000_00)
	if s := status(); s != "awaiting_trust_funds" {
		t.Fatalf("short deposit: %s", s)
	}
	short := f.atty.do("POST", "/api/v1/billing/flat-fees/"+flat+"/apply", map[string]any{"client_id": flatClient, "trust_account_id": f.account, "amount_cents": 1500_00})
	if short.code < 400 {
		t.Fatalf("applied a fee trust cannot cover: %d %s", short.code, short.body)
	}
	f.post(f.admin, flat, flatClient, "deposit", 500_00)
	if s := status(); s != "ready_to_apply" {
		t.Fatalf("fee in trust: %s", s)
	}
	if r := f.acct.do("POST", "/api/v1/billing/flat-fees/"+flat+"/apply", map[string]any{"client_id": flatClient, "trust_account_id": f.account, "amount_cents": 1500_00}); r.code != http.StatusForbidden {
		t.Fatalf("accountant applied a flat fee: %d %s", r.code, r.body)
	}
	if wl := f.atty.must(http.StatusOK, "GET", "/billing", nil); !strings.Contains(wl.body, "Ready to apply") {
		t.Fatalf("worklist screen lacks Ready to apply: %.1000s", wl.body)
	}
	before := f.count(`SELECT count(*) FROM trust_ledger WHERE matter_id = $1`, flat)
	f.atty.must(http.StatusOK, "POST", "/api/v1/billing/flat-fees/"+flat+"/apply", map[string]any{"client_id": flatClient, "trust_account_id": f.account, "amount_cents": 1500_00})
	if s := status(); s != "collected" {
		t.Fatalf("after apply: %s", s)
	}
	if n := f.count(`SELECT count(*) FROM trust_ledger WHERE matter_id = $1 AND entry_type = 'transfer_to_operating' AND amount_cents = -150000`, flat); n != 1 || f.count(`SELECT count(*) FROM trust_ledger WHERE matter_id = $1`, flat) != before+1 {
		t.Fatalf("flat fee transfer rows: %d", n)
	}
	bal := f.atty.must(http.StatusOK, "GET", "/api/v1/trust/balances?matter_id="+flat, nil)
	if strings.Contains(bal.body, `"balance_cents":150000`) || !strings.Contains(bal.body, `"balance_cents":0`) {
		t.Fatalf("trust balance after the fee: %s", bal.body)
	}
	if again := f.atty.do("POST", "/api/v1/billing/flat-fees/"+flat+"/apply", map[string]any{"client_id": flatClient, "trust_account_id": f.account, "amount_cents": 1500_00}); again.code < 400 {
		t.Fatalf("second apply: %d %s", again.code, again.body)
	}
	if wl := f.atty.must(http.StatusOK, "GET", "/billing", nil); !strings.Contains(wl.body, "Collected") {
		t.Fatalf("worklist screen lacks Collected")
	}
	// The practice, billing and trust screens link to each other.
	for path, links := range map[string][]string{"/practice/matters": {`href="/billing"`, `href="/trust/ledger"`},
		"/billing": {`href="/trust/ledger"`, `href="/practice/matters"`}, "/trust/ledger": {`href="/billing"`, `href="/practice/matters"`}} {
		page := f.atty.must(http.StatusOK, "GET", path, nil)
		for _, l := range links {
			if !strings.Contains(page.body, l) {
				t.Fatalf("%s lacks %s", path, l)
			}
		}
	}
}

// TestFinanceTrustInvariants is AC-005, AC-006 and AC-007 on the running core: a
// withdrawal below zero posts, flags and mails the admins and the responsible attorney
// from the running delivery loop; ledger rows cannot be changed through HTTP, MCP or SQL
// and reversals restore the balance; reconciliation differences are exact.
func TestFinanceTrustInvariants(t *testing.T) {
	f := newFin(t, func(c *Config) { c.MailEvery = 50 * time.Millisecond })
	matter, clientID := f.matter("Estate of Okafor", "okafor@client.example", nil)
	dep := f.post(f.admin, matter, clientID, "deposit", 400_00)

	// AC-005: the withdrawal posts, raises a flag and the loop mails both recipients.
	r := f.entry(f.atty, matter, clientID, "withdrawal", 650_00)
	if r.code != http.StatusCreated {
		t.Fatalf("below-zero withdrawal was blocked: %d %s", r.code, r.body)
	}
	wd := str(t, r.json, "entry", "id")
	if num(t, r.json, "balances", "matter_cents") != -250_00 {
		t.Fatalf("balance after the withdrawal: %s", r.body)
	}
	flags, _ := r.json["flags"].([]any)
	if len(flags) == 0 {
		t.Fatalf("no flag raised: %s", r.body)
	}
	flagID := flags[0].(map[string]any)["id"].(string)
	if fl := f.admin.must(http.StatusOK, "GET", "/api/v1/trust-flags/"+flagID, nil); fl.json["status"] != "open" || num(t, fl.json, "balance_cents") >= 0 {
		t.Fatalf("flag: %s", fl.body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(f.smtp.to(adminEmail)) == 0 || len(f.smtp.to(attyEmail)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("flag mail not delivered by the running core: %d messages", len(f.smtp.messages()))
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, addr := range []string{adminEmail, attyEmail} {
		m := f.smtp.to(addr)[0]
		if !strings.Contains(strings.ToLower(m.Data), "below zero") && !strings.Contains(strings.ToLower(m.Data), "trust") {
			t.Fatalf("flag mail to %s does not describe the trust flag:\n%s", addr, m.Data)
		}
	}
	// The loop marks a row sent after the server takes the message, so the row can
	// trail the delivery by a moment.
	for f.count(`SELECT count(*) FROM mail_outbox WHERE sent_at = 0`) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("flag mail still queued after delivery")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(f.smtp.to("sam@firm.example")) != 0 || len(f.smtp.to("ari@firm.example")) != 0 {
		t.Fatal("flag mail went to staff or the accountant")
	}
	// A refused delivery stays queued with its error and goes out on a later tick.
	f.smtp.setReject(true)
	f.post(f.atty, matter, clientID, "withdrawal", 10_00)
	deadline = time.Now().Add(10 * time.Second)
	for f.count(`SELECT count(*) FROM mail_outbox WHERE sent_at = 0 AND attempts > 0 AND last_error <> ''`) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("a refused flag mail was not kept with its error")
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.smtp.setReject(false)
	deadline = time.Now().Add(10 * time.Second)
	for f.count(`SELECT count(*) FROM mail_outbox WHERE sent_at = 0`) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("a queued flag mail was not retried")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// AC-006: no edit or delete path.
	rows := f.count(`SELECT count(*) FROM trust_ledger`)
	for _, m := range []string{"PUT", "PATCH", "DELETE"} {
		if r := f.admin.do(m, "/api/v1/trust-ledger/"+dep, map[string]any{"amount_cents": 1}); r.code != http.StatusNotFound && r.code != http.StatusMethodNotAllowed {
			t.Fatalf("%s on a ledger entry answered %d", m, r.code)
		}
	}
	tools, _ := f.admin.must(http.StatusOK, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}).json["result"].(map[string]any)["tools"].([]any)
	for _, tl := range tools {
		d := tl.(map[string]any)["description"].(string)
		if strings.Contains(d, "/trust-ledger/{trust_entry_id}") && !strings.HasPrefix(d, "GET ") && !strings.HasSuffix(d, "/reversals") {
			t.Fatalf("MCP offers a ledger edit: %s", d)
		}
	}
	for _, stmt := range []string{
		`UPDATE trust_ledger SET amount_cents = 1 WHERE id = $1`,
		`DELETE FROM trust_ledger WHERE id = $1`,
	} {
		if _, err := f.app.Pool.Exec(stmt, dep); err == nil {
			t.Fatalf("the core role ran %s", stmt)
		}
	}
	if _, err := f.app.Pool.Exec(`TRUNCATE trust_ledger`); err == nil {
		t.Fatal("the core role truncated the ledger")
	}
	if f.count(`SELECT count(*) FROM trust_ledger`) != rows {
		t.Fatal("ledger rows changed")
	}
	// Reversals: the staff role cannot, an attorney can, once; the balance is restored.
	if r := f.staff.do("POST", "/api/v1/trust-ledger/"+wd+"/reversals", map[string]any{"reason": "keyed twice"}); r.code != http.StatusForbidden {
		t.Fatalf("staff reversed: %d %s", r.code, r.body)
	}
	if r := f.atty.do("POST", "/api/v1/trust-ledger/"+wd+"/reversals", map[string]any{"reason": ""}); r.code != http.StatusBadRequest {
		t.Fatalf("reversal without a reason: %d %s", r.code, r.body)
	}
	rev := f.atty.must(http.StatusCreated, "POST", "/api/v1/trust-ledger/"+wd+"/reversals", map[string]any{"reason": "keyed twice"})
	if num(t, rev.json, "balances", "matter_cents") != 400_00-10_00 || rev.json["entry"].(map[string]any)["entry_type"] != "reversal" {
		t.Fatalf("reversal: %s", rev.body)
	}
	if r := f.atty.do("POST", "/api/v1/trust-ledger/"+wd+"/reversals", map[string]any{"reason": "again"}); r.code != http.StatusConflict {
		t.Fatalf("second reversal: %d %s", r.code, r.body)
	}
	if orig := f.admin.must(http.StatusOK, "GET", "/api/v1/trust-ledger/"+wd, nil); num(t, orig.json, "amount_cents") != 650_00 {
		t.Fatalf("the reversed entry changed: %s", orig.body)
	}
	// The practice copy follows the ledger through the balance sink.
	if prev := f.atty.must(http.StatusOK, "GET", "/api/v1/matters/"+matter+"/close-preview", nil); !strings.Contains(prev.body, "nonzero_trust") {
		t.Fatalf("practice close preview misses the trust balance: %s", prev.body)
	}
	if rep := f.acct.must(http.StatusOK, "GET", "/api/v1/reports/trust-balances", nil); !strings.Contains(rep.body, matter) {
		t.Fatalf("trust balance report: %s", rep.body)
	}

	// AC-007: reconciliation is zero on the seeded data and exact when the statement is off.
	today := time.Now().UTC().Format("2006-01-02")
	ledger := int64(400_00 - 10_00)
	recon := f.admin.must(http.StatusCreated, "POST", "/api/v1/trust-reconciliations", map[string]any{"trust_account_id": f.account, "statement_on": today, "statement_balance_cents": ledger})
	if num(t, recon.json, "statement_difference_cents") != 0 || num(t, recon.json, "client_sum_difference_cents") != 0 || num(t, recon.json, "ledger_total_cents") != ledger {
		t.Fatalf("seeded reconciliation: %s", recon.body)
	}
	off := f.admin.must(http.StatusCreated, "POST", "/api/v1/trust-reconciliations", map[string]any{"trust_account_id": f.account, "statement_on": today, "statement_balance_cents": ledger + 12_34})
	if d := num(t, off.json, "statement_difference_cents"); d != 12_34 && d != -12_34 {
		t.Fatalf("off statement difference: %s", off.body)
	}
	if r := f.admin.do("POST", "/api/v1/trust-reconciliations", map[string]any{"trust_account_id": f.account, "statement_on": today}); r.code != http.StatusBadRequest {
		t.Fatalf("reconciliation without a statement balance: %d %s", r.code, r.body)
	}
	if page := f.admin.must(http.StatusOK, "GET", "/trust/reconciliation", nil); !strings.Contains(page.body, "12.34") {
		t.Fatalf("reconciliation screen lacks the difference: %.1200s", page.body)
	}
	if rep := f.acct.must(http.StatusOK, "GET", "/api/v1/reports/reconciliations", nil); !strings.Contains(rep.body, str(t, off.json, "id")) {
		t.Fatalf("reconciliation report: %s", rep.body)
	}
}

// TestFinancePermissions is AC-003, AC-004 and the audit half of AC-018 for billing and
// trust on the running core: walls hide billing and trust records from staff on every
// surface; the accountant reads and writes nothing; token scopes follow entry_type;
// every write is audited with actor and source.
func TestFinancePermissions(t *testing.T) {
	f := newFin(t)
	open, openClient := f.matter("Estate of Rivera", "rivera@client.example", nil)
	walled, walledClient := f.matter("Estate of Quist", "quist@client.example", nil)
	f.admin.must(http.StatusOK, "PUT", "/api/v1/matters/"+walled+"/wall", map[string]any{"user_ids": []string{f.adminID, f.attyID}})
	wEntry := f.post(f.admin, walled, walledClient, "deposit", 900_00)
	f.post(f.atty, walled, walledClient, "withdrawal", 950_00) // raises a flag on the walled matter
	f.timeEntry(walled, 4, 200_00)
	wInv := items(t, f.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": walled}))[0]["id"].(string)
	oEntry := f.post(f.staff, open, openClient, "deposit", 100_00)
	f.timeEntry(open, 2, 200_00)
	oInv := items(t, f.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": open}))[0]["id"].(string)

	// AC-003: the staff user sees the open matter and never the walled one.
	for _, path := range []string{
		"/api/v1/trust-ledger", "/api/v1/trust-ledger?matter_id=" + walled, "/api/v1/trust/balances", "/api/v1/trust/balances?matter_id=" + walled, "/api/v1/trust-flags", "/api/v1/reports/trust-balances",
		"/api/v1/reports/trust-client-ledger?client_id=" + walledClient, "/api/v1/reports/trust-account-ledger?trust_account_id=" + f.account,
		"/api/v1/widgets/trust-alerts", "/api/v1/notifications", "/api/v1/invoices", "/api/v1/time-entries", "/api/v1/payments",
		"/api/v1/billing/worklist", "/api/v1/reports/ar-aging", "/api/v1/reports/unbilled-time", "/api/v1/reports/time-by-user",
		"/api/v1/search?q=Quist", "/billing", "/billing/invoices", "/billing/time", "/trust/ledger", "/trust/balances", "/trust/flags",
	} {
		r := f.staff.do("GET", path, nil)
		if r.code >= 500 {
			t.Fatalf("staff %s: %d %s", path, r.code, r.body)
		}
		for _, secret := range []string{walled, wEntry, wInv, "Quist"} {
			if strings.Contains(r.body, secret) && !(strings.Contains(path, "q=Quist") && secret == "Quist" && !strings.Contains(r.body, walled)) {
				t.Fatalf("staff sees the walled matter at %s (%s): %.600s", path, secret, r.body)
			}
		}
	}
	if r := f.staff.must(http.StatusOK, "GET", "/api/v1/trust-ledger", nil); !strings.Contains(r.body, oEntry) {
		t.Fatal("staff cannot see the open matter's ledger")
	}
	for _, path := range []string{"/api/v1/trust-ledger/" + wEntry, "/api/v1/invoices/" + wInv,
		"/api/v1/invoices/" + wInv + "/pdf", "/trust/matters/" + walled, "/billing/invoices/" + wInv} {
		if r := f.staff.do("GET", path, nil); r.code != http.StatusNotFound || strings.Contains(r.body, "Quist") {
			t.Fatalf("staff opened %s: %d", path, r.code)
		}
	}
	rows := f.count(`SELECT count(*) FROM trust_ledger`)
	if r := f.entry(f.staff, walled, walledClient, "deposit", 1_00); r.code != http.StatusNotFound {
		t.Fatalf("staff posted on the walled matter: %d %s", r.code, r.body)
	}
	if f.count(`SELECT count(*) FROM trust_ledger`) != rows {
		t.Fatal("a refused post wrote a ledger row")
	}
	call := mcpCall(f.staff, toolName(t, "GET", "/api/v1/trust-ledger/{trust_entry_id}"), map[string]any{"path": map[string]string{"trust_entry_id": wEntry}})
	if !strings.Contains(call.body, `"isError":true`) || strings.Contains(call.body, walled) {
		t.Fatalf("staff read the walled entry over MCP: %s", call.body)
	}
	call = mcpCall(f.staff, toolName(t, "GET", "/api/v1/invoices"), map[string]any{})
	if strings.Contains(call.body, wInv) || strings.Contains(call.body, `"isError":true`) {
		t.Fatalf("MCP invoice list for staff: %s", call.body)
	}
	if r := f.atty.must(http.StatusOK, "GET", "/api/v1/trust-ledger", nil); !strings.Contains(r.body, wEntry) {
		t.Fatal("the attorney inside the wall cannot see the entry")
	}

	// AC-004: the accountant reads billing and trust and every write is refused.
	for _, path := range []string{"/api/v1/trust-ledger", "/api/v1/trust-accounts", "/api/v1/trust/balances", "/api/v1/trust-flags",
		"/api/v1/trust-reconciliations", "/api/v1/reports/trust-balances", "/api/v1/invoices", "/api/v1/invoices/" + oInv, "/api/v1/invoices/" + oInv + "/pdf", "/api/v1/payments",
		"/api/v1/time-entries", "/api/v1/expenses", "/api/v1/reports/ar-aging", "/api/v1/reports/payments-received", "/api/v1/billing/worklist",
		"/trust/ledger", "/billing/invoices"} {
		f.acct.must(http.StatusOK, "GET", path, nil)
	}
	before := f.count(`SELECT count(*) FROM audit_log`)
	ledgerRows := f.count(`SELECT count(*) FROM trust_ledger`)
	for _, w := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/v1/trust-ledger", map[string]any{"trust_account_id": f.account, "client_id": openClient, "matter_id": open, "entry_type": "deposit", "amount_cents": 100, "method": "check", "description": "x"}},
		{"POST", "/api/v1/trust-ledger/" + oEntry + "/reversals", map[string]any{"reason": "x"}},
		{"POST", "/api/v1/trust-accounts", map[string]any{"bank": "B", "name": "N", "iolta": true, "last_four": "1111"}},
		{"PATCH", "/api/v1/trust-accounts/" + f.account, map[string]any{"name": "Renamed"}},
		{"POST", "/api/v1/trust-reconciliations", map[string]any{"trust_account_id": f.account, "statement_on": "2026-01-31", "statement_balance_cents": 0}},
		{"POST", "/api/v1/trust-replenishment-requests", map[string]any{"matter_id": open, "client_id": openClient, "amount_requested_cents": 100}},
		{"POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": open}},
		{"POST", "/api/v1/invoices/" + oInv + "/approve", nil},
		{"POST", "/api/v1/invoices/" + oInv + "/void", map[string]any{"reason": "x"}},
		{"PUT", "/api/v1/invoices/" + oInv + "/draft", map[string]any{"lines": []any{}}},
		{"POST", "/api/v1/invoices/" + oInv + "/apply-trust", map[string]any{"client_id": openClient, "trust_account_id": f.account, "amount_cents": 100}},
		{"POST", "/api/v1/payments", map[string]any{"contact_id": openClient, "matter_id": open, "amount_cents": 100, "destination": "trust", "trust_account_id": f.account, "method": "check"}},
		{"POST", "/api/v1/time-entries", map[string]any{"matter_id": open, "worked_on": "2026-01-02", "increments": 1, "description": "x", "activity_type_id": f.activity}},
		{"POST", "/api/v1/timers", map[string]any{"matter_id": open}},
		{"POST", "/api/v1/activity-types", map[string]any{"name": "Accounting"}},
		{"POST", "/api/v1/notifications:read-all", nil},
	} {
		r := f.acct.do(w.method, w.path, w.body)
		if w.path == "/api/v1/notifications:read-all" {
			// Marking one's own notifications read is not a billing or trust write; it
			// must not touch anything but the accountant's own rows.
			if r.code >= 500 {
				t.Fatalf("accountant read-all: %d", r.code)
			}
			continue
		}
		if r.code != http.StatusForbidden {
			t.Fatalf("accountant %s %s: %d %s", w.method, w.path, r.code, r.body)
		}
	}
	if f.count(`SELECT count(*) FROM trust_ledger`) != ledgerRows || f.count(`SELECT count(*) FROM audit_log`) != before {
		t.Fatal("a refused accountant write changed the ledger or the audit log")
	}
	if call := mcpCall(f.acct, toolName(t, "POST", "/api/v1/trust-ledger"), map[string]any{"body": map[string]any{
		"trust_account_id": f.account, "client_id": openClient, "matter_id": open, "entry_type": "deposit", "amount_cents": 100, "method": "check", "description": "x"}}); !strings.Contains(call.body, `"isError":true`) || !strings.Contains(call.body, "forbidden") {
		t.Fatalf("accountant posted over MCP: %s", call.body)
	}

	// Token scopes follow entry_type: trust:write deposits, only trust:withdraw withdraws.
	tok := f.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "deposits", "scopes": []string{"trust:write", "trust:read"}})
	bot := newClient(t, f.srv.URL)
	bot.bearer = str(t, tok.json, "secret")
	tokEntry := f.post(bot, open, openClient, "deposit", 7_00)
	if r := f.entry(bot, open, openClient, "withdrawal", 1_00); r.code != http.StatusForbidden {
		t.Fatalf("trust:write token withdrew: %d %s", r.code, r.body)
	}
	if r := bot.do("POST", "/api/v1/trust-ledger:bulk", map[string]any{"items": []map[string]any{
		{"trust_account_id": f.account, "client_id": openClient, "matter_id": open, "entry_type": "deposit", "amount_cents": 100, "method": "check", "description": "x"},
		{"trust_account_id": f.account, "client_id": openClient, "matter_id": open, "entry_type": "refund", "amount_cents": 100, "method": "check", "description": "x"},
	}}); r.code != http.StatusForbidden {
		t.Fatalf("bulk with a withdraw item: %d %s", r.code, r.body)
	}
	if r := bot.do("GET", "/api/v1/invoices", nil); r.code != http.StatusForbidden {
		t.Fatalf("trust token read invoices: %d", r.code)
	}
	wtok := f.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "withdrawals", "scopes": []string{"trust:withdraw"}})
	wbot := newClient(t, f.srv.URL)
	wbot.bearer = str(t, wtok.json, "secret")
	f.post(wbot, open, openClient, "withdrawal", 1_00)

	// One bearer trust post authenticates once: the route check and the per-entry scope
	// check share it, so it counts once against security.api_rate_per_minute.
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/security.api_rate_per_minute", map[string]any{"value": 2})
	rtok := f.admin.must(http.StatusCreated, "POST", "/api/v1/api-tokens", map[string]any{"name": "rated", "scopes": []string{"trust:write"}})
	rbot := newClient(t, f.srv.URL)
	rbot.bearer = str(t, rtok.json, "secret")
	f.post(rbot, open, openClient, "deposit", 1_00)
	f.post(rbot, open, openClient, "deposit", 1_00)
	if r := f.entry(rbot, open, openClient, "deposit", 1_00); r.code != http.StatusTooManyRequests {
		t.Fatalf("third post in the minute: %d %s", r.code, r.body)
	}

	// AC-018 audit half: trust and billing writes carry the actor and the source.
	audit := func(entity, id string) string {
		return f.admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type="+entity+"&entity_id="+id, nil).body
	}
	if a := audit("trust_entry", tokEntry); !strings.Contains(a, `"source":"api_token"`) || !strings.Contains(a, f.adminID) {
		t.Fatalf("token deposit audit: %s", a)
	}
	if a := audit("trust_entry", oEntry); !strings.Contains(a, `"source":"ui"`) && !strings.Contains(a, `"source":"session"`) {
		t.Fatalf("session deposit audit: %s", a)
	}
	if a := audit("invoice", wInv); !strings.Contains(a, f.attyID) {
		t.Fatalf("invoice draft audit lacks the attorney: %s", a)
	}
	if a := audit("trust_account", f.account); !strings.Contains(a, f.adminID) {
		t.Fatalf("trust account audit: %s", a)
	}
	var walledAudit int
	if err := f.app.Pool.QueryRow(`SELECT count(*) FROM audit_log WHERE entity_id = $1`, wEntry).Scan(&walledAudit); err != nil || walledAudit == 0 {
		t.Fatalf("walled entry has no audit row: %v", err)
	}
	if a := f.staff.do("GET", "/api/v1/audit-log?entity_type=trust_entry&entity_id="+wEntry, nil); a.code < 400 && strings.Contains(a.body, wEntry) {
		t.Fatalf("staff read the walled entry's audit rows: %s", a.body)
	}
}

// TestFinanceTrustNotices is AC-021 on the running core: a new flag raises the
// responsible attorney's unread count on the API and the screens, and Mark all read
// clears it for that user only.
func TestFinanceTrustNotices(t *testing.T) {
	f := newFin(t)
	matter, clientID := f.matter("Estate of Brandt", "", nil)
	unread := func(c *client) int {
		return len(items(t, c.must(http.StatusOK, "GET", "/api/v1/notifications?read=false", nil)))
	}
	if n := unread(f.atty); n != 0 {
		t.Fatalf("attorney starts with %d unread", n)
	}
	f.post(f.admin, matter, clientID, "deposit", 50_00)
	f.post(f.atty, matter, clientID, "withdrawal", 80_00)
	if n := unread(f.atty); n != 1 {
		t.Fatalf("attorney unread after a flag: %d", n)
	}
	adminUnread := unread(f.admin)
	if adminUnread == 0 {
		t.Fatal("admin was not notified")
	}
	if n := unread(f.staff); n != 0 {
		t.Fatalf("staff notified: %d", n)
	}
	page := f.atty.must(http.StatusOK, "GET", "/trust/notifications", nil)
	if !strings.Contains(page.body, "Mark all read") {
		t.Fatalf("notifications screen: %.800s", page.body)
	}
	if !strings.Contains(page.body, ">1<") && !strings.Contains(page.body, "(1)") {
		t.Fatalf("notifications screen does not show the unread count: %.1200s", page.body)
	}
	// A second flag raises the count again.
	f.post(f.atty, matter, clientID, "withdrawal", 5_00)
	if n := unread(f.atty); n != 2 {
		t.Fatalf("attorney unread after a second flag: %d", n)
	}
	// Another user cannot mark the attorney's notification read.
	note := items(t, f.atty.must(http.StatusOK, "GET", "/api/v1/notifications?read=false", nil))[0]["id"].(string)
	if r := f.staff.do("POST", "/api/v1/notifications/"+note+"/read", nil); r.code != http.StatusNotFound {
		t.Fatalf("staff marked the attorney's notification: %d %s", r.code, r.body)
	}
	// Mark all read from the screen form; a form without the CSRF token is refused.
	if r := f.atty.do("POST", "/trust/notifications/read-all", url.Values{}, "X-CSRF-Token", ""); r.code != http.StatusForbidden {
		t.Fatalf("read-all without csrf: %d", r.code)
	}
	if n := unread(f.atty); n != 2 {
		t.Fatalf("a refused read-all changed the count: %d", n)
	}
	f.atty.must(http.StatusSeeOther, "POST", "/trust/notifications/read-all", url.Values{"csrf": {csrfFrom(t, f.atty)}})
	if n := unread(f.atty); n != 0 {
		t.Fatalf("attorney unread after Mark all read: %d", n)
	}
	if n := unread(f.admin); n != adminUnread+1 {
		t.Fatalf("attorney's Mark all read changed the admin's count: %d", n)
	}
	f.admin.must(http.StatusNoContent, "POST", "/api/v1/notifications:read-all", nil)
	if n := unread(f.admin); n != 0 {
		t.Fatalf("admin unread after read-all: %d", n)
	}
	if w := f.atty.must(http.StatusOK, "GET", "/api/v1/widgets/trust-alerts", nil); !strings.Contains(w.body, "Estate of Brandt") {
		t.Fatalf("trust alerts widget: %s", w.body)
	}
}

// TestFinanceCrossDomainTransfers runs the billing writes that move trust money on the
// running core: a payment into trust, Apply trust to an invoice and a payment reversal
// each commit their trust ledger row, billing record and audit rows together; a billing
// write that fails after its trust post leaves no ledger row; bulk posts honor
// api.bulk_batch_max; and a restart keeps invoices, balances and the practice copies.
func TestFinanceCrossDomainTransfers(t *testing.T) {
	f := newFin(t)
	matter, clientID := f.matter("Estate of Varga", "varga@client.example", nil)
	ledger := func() int { return f.count(`SELECT count(*) FROM trust_ledger`) }
	audits := func() int { return f.count(`SELECT count(*) FROM audit_log`) }

	// A payment into trust posts a deposit linked to the payment.
	payIn := f.admin.must(http.StatusCreated, "POST", "/api/v1/payments", map[string]any{"contact_id": clientID, "matter_id": matter, "amount_cents": 500_00,
		"destination": "trust", "trust_account_id": f.account, "method": "check", "reference_number": "2001"})
	entryID, _ := payIn.json["trust_entry_id"].(string)
	if entryID == "" {
		t.Fatalf("trust payment has no ledger entry: %s", payIn.body)
	}
	if e := f.admin.must(http.StatusOK, "GET", "/api/v1/trust-ledger/"+entryID, nil); e.json["entry_type"] != "deposit" || num(t, e.json, "amount_cents") != 500_00 || e.json["matter_id"] != matter {
		t.Fatalf("trust payment entry: %s", e.body)
	}

	// Two invoices from unbilled time.
	invoice := func(increments int) (string, int64) {
		f.timeEntry(matter, increments, 100_00)
		inv := items(t, f.atty.must(http.StatusCreated, "POST", "/api/v1/invoice-drafts", map[string]any{"matter_id": matter}))[0]
		id := inv["id"].(string)
		f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+id+"/approve", nil)
		f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+id+"/send", map[string]any{"deliver": "file"})
		return id, int64(inv["total_cents"].(float64))
	}
	inv1, total1 := invoice(10)
	inv2, total2 := invoice(5)
	if total1+total2 > 500_00 {
		t.Fatalf("totals %d + %d exceed the deposit", total1, total2)
	}

	// A billing write that fails after its trust post rolls the trust row back with it.
	var role string
	if err := f.app.Pool.QueryRow(`SELECT current_user`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	super := f.superuser()
	if _, err := super.Exec(`REVOKE INSERT, UPDATE ON core.billing_records FROM ` + role); err != nil {
		t.Fatal(err)
	}
	rows, logs := ledger(), audits()
	failed := f.atty.do("POST", "/api/v1/invoices/"+inv1+"/apply-trust", map[string]any{"client_id": clientID, "trust_account_id": f.account, "amount_cents": total1})
	if failed.code < 500 {
		t.Fatalf("apply trust with billing writes refused: %d %s", failed.code, failed.body)
	}
	if ledger() != rows || audits() != logs {
		t.Fatalf("a failed billing write kept its trust row or audit rows: ledger %d -> %d, audit %d -> %d", rows, ledger(), logs, audits())
	}
	if _, err := super.Exec(`GRANT INSERT, UPDATE ON core.billing_records TO ` + role); err != nil {
		t.Fatal(err)
	}
	if r := f.admin.must(http.StatusOK, "GET", "/api/v1/invoices/"+inv1, nil); r.json["status"] != "sent" || num(t, r.json, "balance_due_cents") != total1 {
		t.Fatalf("invoice after the failed apply: %s", r.body)
	}

	// Apply trust: more than the balance due is refused and writes nothing.
	if r := f.atty.do("POST", "/api/v1/invoices/"+inv1+"/apply-trust", map[string]any{"client_id": clientID, "trust_account_id": f.account, "amount_cents": total1 + 1}); r.code < 400 || ledger() != rows {
		t.Fatalf("over-apply: %d %s", r.code, r.body)
	}
	applied := f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+inv1+"/apply-trust", map[string]any{"client_id": clientID, "trust_account_id": f.account, "amount_cents": total1})
	transfer, _ := applied.json["trust_entry_id"].(string)
	if transfer == "" || ledger() != rows+1 {
		t.Fatalf("apply trust: %s", applied.body)
	}
	if e := f.admin.must(http.StatusOK, "GET", "/api/v1/trust-ledger/"+transfer, nil); e.json["entry_type"] != "transfer_to_operating" || num(t, e.json, "amount_cents") != total1 {
		t.Fatalf("apply trust entry: %s", e.body)
	}
	if r := f.admin.must(http.StatusOK, "GET", "/api/v1/invoices/"+inv1, nil); r.json["status"] != "paid" {
		t.Fatalf("invoice after apply trust: %s", r.body)
	}
	if a := f.admin.must(http.StatusOK, "GET", "/api/v1/audit-log?entity_type=trust_entry&entity_id="+transfer, nil); !strings.Contains(a.body, f.attyID) {
		t.Fatalf("apply trust entry audit: %s", a.body)
	}
	balance := func() int64 {
		b := f.admin.must(http.StatusOK, "GET", "/api/v1/trust/balances?matter_id="+matter, nil)
		for _, row := range items(t, b) {
			if row["scope"] == "matter" {
				return int64(row["balance_cents"].(float64))
			}
		}
		t.Fatalf("no matter balance: %s", b.body)
		return 0
	}
	if b := balance(); b != 500_00-total1 {
		t.Fatalf("balance after apply: %d", b)
	}

	// Reversing the trust-funded payment reverses its ledger row and reopens the invoice.
	f.atty.must(http.StatusOK, "POST", "/api/v1/payments/"+str(t, applied.json, "id")+"/reverse", map[string]any{"reason": "applied to the wrong invoice"})
	if b := balance(); b != 500_00 {
		t.Fatalf("balance after the payment reversal: %d", b)
	}
	if n := f.count(`SELECT count(*) FROM trust_ledger WHERE reverses_id = $1`, transfer); n != 1 {
		t.Fatalf("payment reversal ledger rows: %d", n)
	}
	if r := f.admin.must(http.StatusOK, "GET", "/api/v1/invoices/"+inv1, nil); r.json["status"] == "paid" || num(t, r.json, "balance_due_cents") != total1 {
		t.Fatalf("invoice after the payment reversal: %s", r.body)
	}
	f.atty.must(http.StatusOK, "POST", "/api/v1/invoices/"+inv2+"/apply-trust", map[string]any{"client_id": clientID, "trust_account_id": f.account, "amount_cents": total2})

	// Bulk posts are one transaction capped by api.bulk_batch_max.
	f.admin.must(http.StatusOK, "PATCH", "/api/v1/settings/api.bulk_batch_max", map[string]any{"value": 2})
	item := map[string]any{"trust_account_id": f.account, "client_id": clientID, "matter_id": matter, "entry_type": "deposit", "amount_cents": 1_00, "method": "check", "description": "bulk"}
	rows = ledger()
	if r := f.admin.do("POST", "/api/v1/trust-ledger:bulk", map[string]any{"items": []any{item, item, item}}); r.code != http.StatusBadRequest && r.code != http.StatusRequestEntityTooLarge {
		t.Fatalf("bulk over the cap: %d %s", r.code, r.body)
	}
	bad := map[string]any{"trust_account_id": f.account, "client_id": clientID, "matter_id": matter, "entry_type": "deposit", "amount_cents": -5, "method": "check", "description": "bad"}
	if r := f.admin.do("POST", "/api/v1/trust-ledger:bulk", map[string]any{"items": []any{item, bad}}); r.code != http.StatusUnprocessableEntity || !strings.Contains(r.body, "batch_rejected") || ledger() != rows {
		t.Fatalf("bulk with an invalid item: %d %s", r.code, r.body)
	}
	if r := f.admin.do("POST", "/api/v1/trust-ledger:bulk", map[string]any{"items": []any{item, item}}); r.code >= 300 || ledger() != rows+2 {
		t.Fatalf("bulk within the cap: %d %s", r.code, r.body)
	}
	want := balance()

	// A restart on the same database keeps invoices, balances and the practice copies.
	f.restart()
	if b := balance(); b != want {
		t.Fatalf("balance after restart: %d, want %d", b, want)
	}
	if r := f.admin.must(http.StatusOK, "GET", "/api/v1/invoices/"+inv2, nil); r.json["status"] != "paid" {
		t.Fatalf("invoice after restart: %s", r.body)
	}
	if p := f.atty.must(http.StatusOK, "GET", "/api/v1/matters/"+matter+"/close-preview", nil); !strings.Contains(p.body, "nonzero_trust") || !strings.Contains(p.body, "unpaid_invoice") {
		t.Fatalf("practice close preview after restart: %s", p.body)
	}
	if r := f.acct.must(http.StatusOK, "GET", "/api/v1/reports/ar-aging", nil); !strings.Contains(r.body, inv1) {
		t.Fatalf("AR aging after restart lacks the reopened invoice: %s", r.body)
	}
}

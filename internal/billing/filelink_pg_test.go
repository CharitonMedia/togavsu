package billing

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// realFiles runs billing on Postgres with the merged files service behind FileLink, on one
// pool. Billing's practice data lives in practice.Memory, so the users and matters the file
// rows reference are mirrored into the core tables with the same ids.
type realFiles struct {
	*fx
	pool  *sql.DB
	files *files.Service
	link  *FileLink
}

func realFilesFx(t *testing.T) *realFiles {
	t.Helper()
	mig, err := files.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	pool := billingDBWith(t, mig...)
	f := newFxStore(t, NewPostgres(pool))
	f.newService(audit.NewPostgres(pool))
	ctx := f.ctx
	if err := db.ApplyReference(ctx, pool, "postgres"); err != nil {
		t.Fatal(err)
	}
	r := &realFiles{fx: f, pool: pool}
	for _, p := range []domain.Principal{f.admin, f.attorney, f.staff, f.staff2, f.accountant} {
		r.mirrorUser(p)
	}
	r.exec(`INSERT INTO practice_areas (id, created_at, updated_at, created_by, name, matter_types, stages, active) VALUES (?,?,?,?,'Probate','[]','[]',1)`,
		f.area, 0, 0, domain.SystemUserID)
	aud, err := audit.New(audit.NewPostgres(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := settings.New(settings.NewMemory(), aud)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	fs, err := files.New(files.Config{Pool: pool, Audit: aud, Settings: reg, Directory: files.PracticeDirectory{Store: f.pm},
		Walls: f.ps.Policy.Walls, Key: key, StagingDir: root + "/staging"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.EnsureLocalDisk(ctx, f.admin, root+"/local"); err != nil {
		t.Fatal(err)
	}
	link, err := NewFileLink(fs)
	if err != nil {
		t.Fatal(err)
	}
	r.files, r.link = fs, link
	f.svc.d.Receipts, f.svc.d.InvoiceFiles = link, link
	return r
}

func (r *realFiles) exec(q string, args ...any) {
	r.t.Helper()
	if _, err := r.pool.ExecContext(r.ctx, db.Rebind(q, "postgres"), args...); err != nil {
		r.t.Fatalf("%s: %v", q, err)
	}
}

func (r *realFiles) mirrorUser(p domain.Principal) {
	r.t.Helper()
	u, err := r.pm.User(r.ctx, p.UserID)
	if err != nil {
		r.t.Fatal(err)
	}
	var contact any
	if !u.ContactID.IsZero() {
		contact = string(u.ContactID)
		r.mirrorContact(u.ContactID)
	}
	r.exec(`INSERT INTO users (id, created_at, updated_at, created_by, name, email, roles, active, default_hourly_rate_cents, contact_id, bar_number, bar_state)
VALUES (?,0,0,?,?,?,?,1,0,?,'','')`, u.ID, domain.SystemUserID, u.Name, string(u.ID)+"@example.test", u.Roles, contact)
}

func (r *realFiles) mirrorContact(id domain.ID) {
	r.exec(`INSERT INTO contacts (id, created_at, updated_at, created_by, kind, given_name, middle_name, family_name, prefix, suffix, full_name,
preferred_name, also_known_as, date_of_birth, date_of_death, ssn_ein_ciphertext, entity_type, formation_state, formation_date, archived, source)
VALUES (?,0,0,?,'person','Cora','','Client','','','Cora Client','','','','','','','','',0,'test') ON CONFLICT DO NOTHING`, id, domain.SystemUserID)
}

// matterPG creates a matter in practice and mirrors its row.
func (r *realFiles) matterPG(name string) (domain.Matter, domain.ID) {
	r.t.Helper()
	m, contact := r.matter(name, "hourly", 0, 0)
	r.exec(`INSERT INTO matters (id, created_at, updated_at, created_by, number, name, practice_area_id, status, stage, case_number, court, client_position,
opened_on, closed_on, close_reason, statute_of_limitations, billing_type, flat_fee_cents, matter_rate_cents, responsible_attorney_id)
VALUES (?,0,0,?,?,?,?,'open','','','','','','','','','hourly',0,0,?)`, m.ID, domain.SystemUserID, m.Number, m.Name, r.area, r.attorney.UserID)
	return m, contact
}

// clientOf adds a client-portal user for contact and mirrors it.
func (r *realFiles) clientOf(contact domain.ID) domain.Principal {
	r.t.Helper()
	p := person(r.id(), domain.RoleClient)
	enc, _ := domain.MarshalRoles(p.Roles)
	if err := r.pm.PutUser(r.ctx, domain.User{Meta: domain.Meta{ID: p.UserID}, Name: "Cora", Roles: enc, Active: true, ContactID: contact}); err != nil {
		r.t.Fatal(err)
	}
	r.mirrorUser(p)
	return p
}

// upload adds a file directly through the files service, the way the Files tab would.
func (r *realFiles) upload(p domain.Principal, nf files.NewFile) files.FileView {
	r.t.Helper()
	body := []byte("synthetic receipt " + nf.Name)
	v, err := r.files.Create(r.ctx, p, nf, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		r.t.Fatalf("create %s: %v", nf.Name, err)
	}
	return v
}

func (r *realFiles) count(q string, args ...any) int {
	r.t.Helper()
	var n int
	if err := r.pool.QueryRowContext(r.ctx, db.Rebind(q, "postgres"), args...).Scan(&n); err != nil {
		r.t.Fatal(err)
	}
	return n
}

func (r *realFiles) versions(fileID domain.ID) []files.VersionView {
	r.t.Helper()
	vs, err := r.files.Versions(r.ctx, r.admin, fileID)
	if err != nil {
		r.t.Fatal(err)
	}
	return vs
}

func shaOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// PRD 5 "receipt (a file record)" and AC-004 against the merged files service: an expense
// names only an untrashed receipt file on its own matter that the caller can read under the
// files service's roles, walls and attorney-only rules, and the expense screen's upload makes
// a receipt file record an accountant can read.
func TestRealFilesExpenseReceipts(t *testing.T) {
	r := realFilesFx(t)
	m, _ := r.matterPG("Receipt estate")
	other, _ := r.matterPG("Other estate")
	walled, _ := r.matterPG("Walled estate")
	if err := r.ps.SetWall(r.ctx, r.admin, walled.ID, []domain.ID{r.attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	receipt := string(domain.FileReceipt)
	ok := r.upload(r.admin, files.NewFile{Name: "courier.pdf", MatterID: m.ID, FinancialClass: receipt})
	general := r.upload(r.admin, files.NewFile{Name: "pleading.pdf", MatterID: m.ID})
	elsewhere := r.upload(r.admin, files.NewFile{Name: "other.pdf", MatterID: other.ID, FinancialClass: receipt})
	secret := r.upload(r.admin, files.NewFile{Name: "secret.pdf", MatterID: m.ID, FinancialClass: receipt, AttorneyOnly: true})
	behindWall := r.upload(r.attorney, files.NewFile{Name: "walled.pdf", MatterID: walled.ID, FinancialClass: receipt})
	trashed := r.upload(r.admin, files.NewFile{Name: "trashed.pdf", MatterID: m.ID, FinancialClass: receipt})
	if _, err := r.files.Trash(r.ctx, r.admin, trashed.ID); err != nil {
		t.Fatal(err)
	}

	row, err := r.svc.CreateExpense(r.ctx, r.staff, receiptInput(r.fx, m.ID, ok.ID))
	if err != nil || row.FileID != ok.ID {
		t.Fatalf("readable receipt: %+v %v", row, err)
	}
	for name, id := range map[string]domain.ID{"general file": general.ID, "other matter": elsewhere.ID, "attorney-only for staff": secret.ID,
		"trashed": trashed.ID, "missing": r.id(), "walled matter's receipt": behindWall.ID} {
		_, err := r.svc.CreateExpense(r.ctx, r.staff, receiptInput(r.fx, m.ID, id))
		if ae := (*domain.APIError)(nil); !errors.As(err, &ae) || ae.Field != "receipt_file_id" {
			t.Fatalf("%s: want invalid receipt_file_id, got %v", name, err)
		}
		_, err = r.svc.UpdateExpense(r.ctx, r.staff, row.ID, ExpenseInput{ReceiptFileID: &id})
		wantInvalidField(t, err, "receipt_file_id")
	}
	// The attorney may read the attorney-only receipt, so the files service lets it through.
	if _, err := r.svc.UpdateExpense(r.ctx, r.attorney, row.ID, ExpenseInput{ReceiptFileID: &secret.ID}); err != nil {
		t.Fatalf("attorney-only receipt for an attorney: %v", err)
	}
	if _, err := r.svc.UpdateExpense(r.ctx, r.attorney, row.ID, ExpenseInput{ReceiptFileID: &ok.ID}); err != nil {
		t.Fatal(err)
	}
	// Moving the expense re-checks the kept receipt against the new matter.
	_, err = r.svc.UpdateExpense(r.ctx, r.staff, row.ID, ExpenseInput{MatterID: &other.ID})
	wantInvalidField(t, err, "receipt_file_id")

	// The picker lists what the caller may read: staff gets no attorney-only receipt.
	names := func(p domain.Principal) string {
		rows, err := r.svc.ReceiptFiles(r.ctx, p, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, rf := range rows {
			out = append(out, rf.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(r.staff); got != "courier.pdf" {
		t.Fatalf("staff picker: %q", got)
	}
	if got := names(r.attorney); got != "courier.pdf,secret.pdf" {
		t.Fatalf("attorney picker: %q", got)
	}
	_, err = r.svc.ReceiptFiles(r.ctx, r.accountant, m.ID)
	wantCode(t, err, domain.CodeForbidden)

	// Upload from the expense screen: a new receipt file record on the matter, attached.
	before := r.count(`SELECT count(*) FROM files WHERE financial_class = 'receipt'`)
	att, err := r.svc.AttachReceipt(r.ctx, r.staff, row.ID, "taxi.jpg", strings.NewReader("synthetic taxi receipt"), 22)
	if err != nil {
		t.Fatal(err)
	}
	up, err := r.files.Get(r.ctx, r.accountant, att.FileID)
	if err != nil || up.FinancialClass == nil || *up.FinancialClass != receipt || *up.MatterID != m.ID || up.CreatedBy != r.staff.UserID {
		t.Fatalf("uploaded receipt as the accountant sees it: %+v %v", up, err)
	}
	// The accountant cannot read an ordinary file: the receipt class is what grants it.
	wantCode(t, mustErr(r.files.Get(r.ctx, r.accountant, general.ID)), domain.CodeNotFound)
	// Failure paths store nothing: an accountant may not attach, nor anyone to a billed expense.
	wantCode(t, mustErr(r.svc.AttachReceipt(r.ctx, r.accountant, row.ID, "x.pdf", strings.NewReader("x"), 1)), domain.CodeForbidden)
	r.logTime(r.attorney, m.ID, "2026-06-01", 4, nil)
	inv := r.sent(m.ID)
	if got, _ := r.svc.Expense(r.ctx, r.staff, row.ID); got.InvoiceID != inv.ID {
		t.Fatalf("expense not billed on %s: %+v", inv.ID, got)
	}
	wantCode(t, mustErr(r.svc.AttachReceipt(r.ctx, r.staff, row.ID, "late.pdf", strings.NewReader("late"), 4)), domain.CodeConflict)
	if after := r.count(`SELECT count(*) FROM files WHERE financial_class = 'receipt'`); after != before+1 {
		t.Fatalf("receipt files %d -> %d", before, after)
	}
}

// failingPDFs makes the file store refuse invoice PDFs until it is healed.
type failingPDFs struct {
	next InvoiceFiles
	down bool
}

func (f *failingPDFs) SaveInvoicePDF(ctx context.Context, by domain.ID, inv Invoice, fileID domain.ID, pdf []byte) (domain.ID, error) {
	if f.down {
		return "", errors.New("storage backend unavailable")
	}
	return f.next.SaveInvoicePDF(ctx, by, inv, fileID, pdf)
}

// PRD 5 "one file store ... invoice PDFs" and PRD 3 accountant access: a sent invoice's PDF
// is an invoice_pdf file record on its matter, one immutable version per distinct rendering,
// readable by the accountant and the matter's client and hidden from other clients.
func TestRealFilesInvoicePDFsAreFileRecords(t *testing.T) {
	r := realFilesFx(t)
	m, contact := r.matterPG("PDF estate")
	_, otherContact := r.matterPG("Stranger estate")
	client, stranger := r.clientOf(contact), r.clientOf(otherContact)
	r.logTime(r.attorney, m.ID, "2026-06-01", 4, nil)
	pdfFiles := func() int { return r.count(`SELECT count(*) FROM files WHERE financial_class = 'invoice_pdf'`) }

	drafts, err := r.svc.DraftInvoices(r.ctx, r.staff, DraftRequest{MatterID: m.ID})
	if err != nil {
		t.Fatal(err)
	}
	id := drafts[0].ID
	// A draft's preview is not stored.
	if _, inv, err := r.svc.InvoicePDF(r.ctx, r.attorney, id); err != nil || !inv.PDFFileID.IsZero() || pdfFiles() != 0 {
		t.Fatalf("draft PDF stored: %+v %v %d", inv.PDFFileID, err, pdfFiles())
	}
	if _, err := r.svc.ApproveInvoice(r.ctx, r.attorney, id); err != nil {
		t.Fatal(err)
	}
	sent, err := r.svc.SendInvoice(r.ctx, r.attorney, id, "email")
	if err != nil {
		t.Fatal(err)
	}
	if sent.PDFFileID.IsZero() || pdfFiles() != 1 {
		t.Fatalf("send stored no PDF: %+v %d", sent.PDFFileID, pdfFiles())
	}
	fileID := sent.PDFFileID
	attached := r.mail.sent[len(r.mail.sent)-1].Attachments[0].Data
	vs := r.versions(fileID)
	if len(vs) != 1 || vs[0].SHA256 != shaOf(attached) || vs[0].SavedBy != r.attorney.UserID || vs[0].MediaType != "application/pdf" {
		t.Fatalf("stored version is not the emailed PDF: %+v", vs)
	}
	fv, err := r.files.Get(r.ctx, r.accountant, fileID)
	if err != nil || fv.FinancialClass == nil || *fv.FinancialClass != string(domain.FileInvoicePDF) || *fv.MatterID != m.ID ||
		fv.Name != "invoice-"+sent.Number+".pdf" || !fv.SharedWithClient || fv.Source != "billing" {
		t.Fatalf("accountant's view of the invoice PDF file: %+v %v", fv, err)
	}
	if _, err := r.files.Get(r.ctx, client, fileID); err != nil {
		t.Fatalf("the matter's client cannot read the sent invoice PDF: %v", err)
	}
	wantCode(t, mustErr(r.files.Get(r.ctx, stranger, fileID)), domain.CodeNotFound)

	// Downloads add a version only when the rendering differs from the newest stored one.
	pdf, _, err := r.svc.InvoicePDF(r.ctx, r.accountant, id)
	if err != nil {
		t.Fatal(err)
	}
	n := len(r.versions(fileID))
	if got := r.versions(fileID); got[len(got)-1].SHA256 != shaOf(pdf) {
		t.Fatalf("newest version is not the downloaded PDF")
	}
	if _, _, err := r.svc.InvoicePDF(r.ctx, client, id); err != nil {
		t.Fatal(err)
	}
	if got := len(r.versions(fileID)); got != n {
		t.Fatalf("an identical download added a version: %d -> %d", n, got)
	}
	if _, err := r.svc.WriteOff(r.ctx, r.attorney, id, 10_00, "Goodwill"); err != nil {
		t.Fatal(err)
	}
	pdf, inv, err := r.svc.InvoicePDF(r.ctx, r.staff, id)
	if err != nil || inv.PDFFileID != fileID {
		t.Fatalf("after write-off: %v %s", err, inv.PDFFileID)
	}
	got := r.versions(fileID)
	if len(got) != n+1 || got[len(got)-1].SHA256 != shaOf(pdf) || got[len(got)-1].SavedBy != r.staff.UserID {
		t.Fatalf("changed invoice did not add one version: %d -> %+v", n, got)
	}
	// Earlier versions are untouched: the emailed bytes are still version 1.
	if got[0].SHA256 != shaOf(attached) {
		t.Fatal("version 1 changed")
	}

	// A file store failure does not fail the download; it is audited and the next download
	// stores the PDF.
	flaky := &failingPDFs{next: r.link, down: true}
	r.svc.d.InvoiceFiles = flaky
	if _, err := r.svc.WriteOff(r.ctx, r.attorney, id, 5_00, "More goodwill"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.svc.InvoicePDF(r.ctx, r.attorney, id); err != nil {
		t.Fatalf("download failed with the file store down: %v", err)
	}
	if fails := r.count(`SELECT count(*) FROM audit_log WHERE entity_id = ? AND field = 'pdf_file' AND new_value = 'failed'`, id); fails != 1 {
		t.Fatalf("pdf_file failures audited: %d", fails)
	}
	if got := len(r.versions(fileID)); got != n+1 {
		t.Fatalf("a version was stored while the file store was down: %d", got)
	}
	flaky.down = false
	if _, _, err := r.svc.InvoicePDF(r.ctx, r.attorney, id); err != nil {
		t.Fatal(err)
	}
	if got := len(r.versions(fileID)); got != n+2 {
		t.Fatalf("retry did not store the PDF: %d", got)
	}

	// A person trashed the file: billing never writes into the trash and starts a new record.
	if _, err := r.files.Trash(r.ctx, r.admin, fileID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.WriteOff(r.ctx, r.attorney, id, 1_00, "Last goodwill"); err != nil {
		t.Fatal(err)
	}
	_, inv, err = r.svc.InvoicePDF(r.ctx, r.attorney, id)
	if err != nil || inv.PDFFileID.IsZero() || inv.PDFFileID == fileID || pdfFiles() != 2 {
		t.Fatalf("after trash: %v %s %d", err, inv.PDFFileID, pdfFiles())
	}
	if got := len(r.versions(fileID)); got != n+2 {
		t.Fatalf("trashed file got a version: %d", got)
	}
	// The link is billing history, so it survives a new service on the same database.
	stored, err := r.svc.invoice(r.ctx, id)
	if err != nil || stored.PDFFileID != inv.PDFFileID || stored.PDFSHA256 == "" {
		t.Fatalf("stored link: %+v %v", stored, err)
	}
}

// With the files service unwired, receipts are refused and PDFs are rendered but not stored.
func TestFilesUnwiredRefusesReceiptsAndStoresNoPDF(t *testing.T) {
	f := newFx(t)
	m, _ := f.matter("Unwired", "hourly", 0, 0)
	_, err := f.svc.CreateExpense(f.ctx, f.staff, receiptInput(f, m.ID, f.id()))
	wantInvalidField(t, err, "receipt_file_id")
	row, err := f.svc.CreateExpense(f.ctx, f.staff, receiptInput(f, m.ID, ""))
	if err != nil {
		t.Fatal(err)
	}
	wantInvalidField(t, mustErr(f.svc.AttachReceipt(f.ctx, f.staff, row.ID, "r.pdf", strings.NewReader("r"), 1)), "receipt_file_id")
	if got, err := f.svc.ReceiptFiles(f.ctx, f.staff, m.ID); err != nil || got != nil {
		t.Fatalf("picker without files: %v %v", got, err)
	}
	f.logTime(f.attorney, m.ID, "2026-06-01", 2, nil)
	inv := f.sent(m.ID)
	pdf, got, err := f.svc.InvoicePDF(f.ctx, f.attorney, inv.ID)
	if err != nil || len(pdf) == 0 || !got.PDFFileID.IsZero() {
		t.Fatalf("unwired PDF: %d %s %v", len(pdf), got.PDFFileID, err)
	}
}

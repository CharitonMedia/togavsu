package billing

import (
	"bytes"
	"context"
	"errors"
	"io"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// FileLink adapts the one file store (internal/files) to billing's Receipts and InvoiceFiles
// seams. Every read runs as the caller through files.Service, so a receipt check gets the
// files service's own roles, matter walls, attorney-only and accountant rules.
type FileLink struct {
	Svc *files.Service
}

// NewFileLink returns the adapter.
func NewFileLink(svc *files.Service) (*FileLink, error) {
	if svc == nil {
		return nil, errors.New("billing: file link needs the files service")
	}
	return &FileLink{Svc: svc}, nil
}

var (
	_ Receipts     = (*FileLink)(nil)
	_ InvoiceFiles = (*FileLink)(nil)
	_ SignedCopies = (*FileLink)(nil)
)

// CheckMatterFile implements SignedCopies: fileID is an untrashed file on matter that p
// may read. Anything else answers not found.
func (l *FileLink) CheckMatterFile(ctx context.Context, p domain.Principal, matter, fileID domain.ID) error {
	f, err := l.Svc.Get(ctx, p, fileID)
	if err != nil {
		return err
	}
	if f.TrashedAt != nil || f.MatterID == nil || *f.MatterID != matter {
		return domain.NotFound("file")
	}
	return nil
}

func isReceipt(f files.FileView, matter domain.ID) bool {
	return f.TrashedAt == nil && f.MatterID != nil && *f.MatterID == matter &&
		f.FinancialClass != nil && *f.FinancialClass == string(domain.FileReceipt)
}

// CheckReceipt implements Receipts. A file p cannot read, a trashed file, a file on another
// matter and a file that is not classed as a receipt all answer not found.
func (l *FileLink) CheckReceipt(ctx context.Context, p domain.Principal, matter, fileID domain.ID) error {
	f, err := l.Svc.Get(ctx, p, fileID)
	if err != nil {
		return err
	}
	if !isReceipt(f, matter) {
		return domain.NotFound("receipt file")
	}
	return nil
}

// ReceiptFiles implements Receipts: every untrashed receipt on the matter that p may read.
func (l *FileLink) ReceiptFiles(ctx context.Context, p domain.Principal, matter domain.ID) ([]ReceiptFile, error) {
	rows, _, err := l.Svc.List(ctx, p, files.ListQuery{MatterID: matter})
	if err != nil {
		return nil, err
	}
	var out []ReceiptFile
	for _, f := range rows {
		if isReceipt(f, matter) {
			out = append(out, ReceiptFile{ID: f.ID, Name: f.Name})
		}
	}
	return out, nil
}

// AddReceipt implements Receipts: a new file record classed receipt on the matter, created as
// p, so the files service applies its own write check (walls, roles, accountant read-only).
func (l *FileLink) AddReceipt(ctx context.Context, p domain.Principal, matter domain.ID, name string, r io.Reader, size int64) (domain.ID, error) {
	f, err := l.Svc.Create(ctx, p, files.NewFile{
		Name: name, MatterID: matter, Origin: "firm", How: "upload",
		FinancialClass: string(domain.FileReceipt), Note: "Expense receipt from billing",
	}, r, size)
	if err != nil {
		return "", err
	}
	return f.ID, nil
}

// SaveInvoicePDF implements InvoiceFiles. Billing has already authorized the caller on the
// invoice, so the write runs as the system acting for by: a client or accountant download
// stores the PDF without needing file-write rights, and the file's created_by and saved_by
// name the person. The file is shared with the client once the invoice has gone to them.
func (l *FileLink) SaveInvoicePDF(ctx context.Context, by domain.ID, inv Invoice, fileID domain.ID, pdf []byte) (domain.ID, error) {
	p := domain.Principal{Source: domain.SourceSystem, UserID: by}
	note := "Invoice " + inv.Number + " (" + inv.Status + ") from billing"
	keep, err := l.stillInvoicePDF(ctx, fileID, inv.MatterID)
	if err != nil {
		return "", err
	}
	if keep {
		_, err := l.Svc.SaveVersion(ctx, p, fileID, bytes.NewReader(pdf), int64(len(pdf)),
			files.SaveOptions{How: "upload", MediaType: "application/pdf", Note: note})
		if err == nil {
			return fileID, nil
		}
		// A file someone trashed or purged is not written to again: a new record starts.
		var ae *domain.APIError
		if !errors.As(err, &ae) || (ae.Code != domain.CodeNotFound && ae.Code != domain.CodeConflict) {
			return "", err
		}
	}
	f, err := l.Svc.Create(ctx, p, files.NewFile{
		Name: "invoice-" + inv.Number + ".pdf", MatterID: inv.MatterID, MediaType: "application/pdf",
		SharedWithClient: sharedWithClient(inv), Source: "billing", How: "upload", Origin: "firm",
		FinancialClass: string(domain.FileInvoicePDF), Note: note,
	}, bytes.NewReader(pdf), int64(len(pdf)))
	if err != nil {
		return "", err
	}
	return f.ID, nil
}

// stillInvoicePDF reports whether fileID is still this matter's invoice PDF record. A file a
// person moved to another matter or purged gets no further versions from billing.
func (l *FileLink) stillInvoicePDF(ctx context.Context, fileID, matter domain.ID) (bool, error) {
	if fileID.IsZero() {
		return false, nil
	}
	f, err := l.Svc.Get(ctx, domain.SystemPrincipal(), fileID)
	var ae *domain.APIError
	if errors.As(err, &ae) && ae.Code == domain.CodeNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return f.MatterID != nil && *f.MatterID == matter &&
		f.FinancialClass != nil && *f.FinancialClass == string(domain.FileInvoicePDF), nil
}

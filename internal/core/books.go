package core

import (
	"context"
	"net/http"

	"github.com/CharitonMedia/togavsu/internal/billing"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/trust"
)

// booksReaderOps are money and books endpoints that a module service call runs as the
// books reader when the token holds books:read (CORE-02).
var booksReaderOps = map[string]bool{
	"listInvoice": true, "getInvoice": true,
	"listPayment": true, "getPayment": true,
	"listExpense": true, "getExpense": true,
	"listTrustAccount": true, "getTrustAccount": true,
	"listTrustEntry": true, "getTrustEntry": true,
	"getTrustBalances": true,
	"getBooksProfile":  true, "getBooksMatter": true, "getBooksContact": true,
}

// booksReaderPrincipal is a module service principal with the accountant field set and
// no user id, so matter walls treat it as a caller outside every wall (CORE-02).
func booksReaderPrincipal(moduleID string) domain.Principal {
	return domain.Principal{
		ModuleID: moduleID,
		Source:   domain.SourceService,
		Roles:    []domain.Role{domain.RoleAccountant},
	}
}

// booksRoutes mounts CORE-02 books reader endpoints.
func (a *App) booksRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/v1/books/profile", a.booksProfileJSON)
	m.HandleFunc("GET /api/v1/books/matters/{matter_id}", a.booksMatterJSON)
	m.HandleFunc("GET /api/v1/books/contacts/{contact_id}", a.booksContactJSON)
}

// elevateBooksReader sets the accountant books-reader roles on a marked money or books
// service call when the module token holds books:read. Missing books:read is scope_denied.
func (a *App) elevateBooksReader(r *http.Request, p *domain.Principal, c identity.Caller) error {
	t, ok := a.toolFor(r.Method, r.URL.Path)
	if !ok || !booksReaderOps[t.Name] {
		return nil
	}
	if err := c.RequireScopes("books:read"); err != nil {
		// Keep scope_denied (same as allowServiceCall), not Forbidden from shared().
		return err
	}
	*p = booksReaderPrincipal(p.ModuleID)
	p.TokenID = c.Principal.TokenID
	return nil
}

// requireBooksService refuses every caller except a module service principal that
// elevateBooksReader has already raised to the accountant books reader (CORE-02).
// Cookie sessions and viewer context tokens must not read contact PII here.
func (a *App) requireBooksService(r *http.Request) (domain.Principal, error) {
	p, err := a.principal(r)
	if err != nil {
		return p, err
	}
	if p.Source != domain.SourceService || p.ModuleID == "" || !p.UserID.IsZero() || !p.HasRole(domain.RoleAccountant) {
		return p, domain.Forbidden("module token with books:read is required")
	}
	return p, nil
}

func (a *App) booksProfileJSON(w http.ResponseWriter, r *http.Request) {
	if _, err := a.requireBooksService(r); err != nil {
		writeErr(w, err)
		return
	}
	ctx := r.Context()
	name, _ := a.Settings.Get(ctx, "firm.name")
	tz, _ := a.Settings.Get(ctx, "firm.time_zone")
	writeJSON(w, http.StatusOK, map[string]any{
		"firm_name": name, "time_zone": tz, "currency": "USD",
	})
}

func (a *App) booksMatterJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.requireBooksService(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	out, err := a.Practice.BooksMatter(r.Context(), p, domain.ID(r.PathValue("matter_id")))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) booksContactJSON(w http.ResponseWriter, r *http.Request) {
	p, err := a.requireBooksService(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	id := domain.ID(r.PathValue("contact_id"))
	ok, err := a.booksContactReadable(r.Context(), p, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		writeErr(w, domain.NotFound("contact"))
		return
	}
	out, err := a.Practice.BooksContact(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// booksContactReadable is whether getBooksContact would return the contact (CORE-02).
func (a *App) booksContactReadable(ctx context.Context, p domain.Principal, id domain.ID) (bool, error) {
	if id.IsZero() {
		return false, nil
	}
	if ok, err := a.Practice.UnwalledClientContact(ctx, id); err != nil {
		return false, err
	} else if ok {
		return true, nil
	}
	if a.Billing != nil {
		pays, err := a.Billing.Payments(ctx, p, billing.PaymentFilter{})
		if err != nil {
			return false, err
		}
		for _, pay := range pays {
			if pay.ContactID == id {
				return true, nil
			}
		}
	}
	if a.Trust != nil {
		page, err := a.Trust.Entries(ctx, p, trust.EntryFilter{ClientID: id})
		if err != nil {
			return false, err
		}
		if len(page.Items) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// booksContactReadableForModule is the enqueue hook: a books:read module only receives
// contact.updated when getBooksContact would return the contact.
func (a *App) booksContactReadableForModule(ctx context.Context, contactID domain.ID) (bool, error) {
	return a.booksContactReadable(ctx, booksReaderPrincipal(""), contactID)
}

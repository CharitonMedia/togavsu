package files

import (
	"context"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// TestPGFilesTabShowsDocTenderFlags: the Files tab shows each file's Doc Tender label
// and every flag the hook returns for the reader (AC-048, PRD 8.21), keeps a file the
// hook does not show this reader without a label, and fails the page on any other error
// rather than drawing it without the flags.
func TestPGFilesTabShowsDocTenderFlags(t *testing.T) {
	e := newEnv(t)
	flagged := e.create(e.staff, NewFile{Name: "cut-off.pdf", MatterID: e.open}, "%PDF-1.7 trunc")
	clean := e.create(e.staff, NewFile{Name: "motion.txt", MatterID: e.open}, "motion")
	hidden := e.create(e.staff, NewFile{Name: "other.txt", MatterID: e.open}, "other")
	var fail error
	var asked []domain.Principal
	hook := func(_ context.Context, p domain.Principal, id domain.ID) (DocStatus, error) {
		asked = append(asked, p)
		switch {
		case fail != nil:
			return DocStatus{}, fail
		case id == flagged.ID:
			return DocStatus{Label: "Needs review", Flags: []DocFlag{
				{Kind: "damaged", Label: "Damaged file", Detail: "the PDF <ends> early"},
				{Kind: "low_confidence", Label: "Low confidence"},
			}}, nil
		case id == clean.ID:
			return DocStatus{Label: "Processed"}, nil
		}
		return DocStatus{}, domain.NotFound("file")
	}
	page := func() (int, string) {
		h, err := NewHandler(e.svc, os.DirFS("../../web/templates/files"), func(*http.Request) (domain.Principal, error) { return e.staff, nil })
		if err != nil {
			t.Fatal(err)
		}
		h.DocStatus = hook
		rec := httptest.NewRecorder()
		h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/practice/matters/"+string(e.open)+"/files", nil))
		b, _ := io.ReadAll(rec.Body)
		return rec.Code, string(b)
	}
	code, body := page()
	if code != http.StatusOK {
		t.Fatalf("files tab = %d\n%s", code, body)
	}
	row := func(name string) string {
		i := strings.Index(body, `<span class="name">`+name+`</span>`)
		if i < 0 {
			t.Fatalf("no row for %s:\n%s", name, body)
		}
		end := strings.Index(body[i:], "</tr>")
		return body[i : i+end]
	}
	r := row("cut-off.pdf")
	for _, want := range []string{">Needs review<", `flag-damaged">Damaged file<`, html.EscapeString("the PDF <ends> early"), `flag-low_confidence">Low confidence<`, "/download"} {
		if !strings.Contains(r, want) {
			t.Fatalf("flagged row lacks %q:\n%s", want, r)
		}
	}
	if strings.Contains(r, "the PDF <ends> early") {
		t.Fatal("a flag's detail is not escaped")
	}
	if r := row("motion.txt"); !strings.Contains(r, ">Processed<") || strings.Contains(r, "flag-") {
		t.Fatalf("clean row:\n%s", r)
	}
	if r := row("other.txt"); strings.Contains(r, "doc-tender") {
		t.Fatalf("a file the hook hides shows a Doc Tender state:\n%s", r)
	}
	_ = hidden
	if len(asked) != 3 {
		t.Fatalf("hook asked %d times, want once per file", len(asked))
	}
	for _, p := range asked {
		if p.UserID != e.staff.UserID {
			t.Fatalf("hook asked as %v, not the reader", p.UserID)
		}
	}
	fail = errors.New("database gone")
	if code, body := page(); code != http.StatusInternalServerError || strings.Contains(body, "cut-off.pdf") {
		t.Fatalf("a status error drew the page without flags: %d", code)
	}
}

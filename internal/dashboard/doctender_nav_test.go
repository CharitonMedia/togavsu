package dashboard

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/doctender"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/help"
)

// The running core passes its Doc Tender service straight in.
var _ ReviewQueue = (*doctender.Service)(nil)

// fakeQueue counts per viewer, the way doctender.Service.OpenCount answers only with the
// items the viewer's walls and roles let them read.
type fakeQueue struct {
	counts map[domain.ID]int
	asked  []domain.Principal
	err    error
}

func (f *fakeQueue) OpenCount(_ context.Context, p domain.Principal) (int, error) {
	f.asked = append(f.asked, p)
	return f.counts[p.UserID], f.err
}

func docTenderEntry(nc NavContext) (NavEntry, bool) {
	for _, e := range nc.Entries {
		if e.ID == "doc-tender" {
			return e, true
		}
	}
	return NavEntry{}, false
}

func hasSection(nc NavContext, id string) bool {
	for _, g := range nc.Groups {
		if g.ID == id {
			return true
		}
	}
	return false
}

// PRD 8.21 / AC-019: the review queue is the Doc Tender entry in the sidebar's Documents
// section, with a live count, for admins and attorneys only; the count is the Doc Tender
// service's answer for the viewer, so walled items the viewer can't read are not counted.
func TestDocTenderSidebarEntryAndLiveCount(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := newTestService(t, nil)
	q := &fakeQueue{counts: map[domain.ID]int{adminP.UserID: 5, attyP.UserID: 2}}
	svc.d.Badges = CoreServices{DocTender: q}.Badges()

	for _, tc := range []struct {
		p    domain.Principal
		want int
	}{{adminP, 5}, {attyP, 2}} {
		nc, err := svc.Nav(ctx, tc.p)
		if err != nil {
			t.Fatal(err)
		}
		e, ok := docTenderEntry(nc)
		if !ok || e.Section != SectionDocuments || e.Path != "/doc-tender/review" || e.Label != "Doc Tender" {
			t.Fatalf("%s: Doc Tender entry %+v ok=%v", tc.p.UserID, e, ok)
		}
		if e.Count == nil || *e.Count != tc.want || nc.Badges["doc_tender_open"] != tc.want {
			t.Fatalf("%s: count %v badges %+v, want %d", tc.p.UserID, e.Count, nc.Badges, tc.want)
		}
		if !hasSection(nc, SectionDocuments) {
			t.Fatalf("%s: Documents section hidden", tc.p.UserID)
		}
	}
	for _, p := range q.asked {
		if p.UserID != adminP.UserID && p.UserID != attyP.UserID {
			t.Fatalf("count asked as %s", p.UserID)
		}
	}
	if len(q.asked) != 2 || q.asked[1].UserID != attyP.UserID || q.asked[1].Source != domain.SourceUI {
		t.Fatalf("the count must run as the viewer: %+v", q.asked)
	}

	// Staff and accountants get no entry, no count, and no empty Documents section.
	q.asked = nil
	for _, p := range []domain.Principal{staffP, acctP} {
		nc, err := svc.Nav(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := docTenderEntry(nc); ok {
			t.Fatalf("%s sees the Doc Tender queue", p.UserID)
		}
		if _, ok := nc.Badges["doc_tender_open"]; ok || hasSection(nc, SectionDocuments) {
			t.Fatalf("%s: badges %+v / Documents shown", p.UserID, nc.Badges)
		}
	}
	if len(q.asked) != 0 {
		t.Fatalf("queue counted for a role that can't open it: %+v", q.asked)
	}

	// A failing count shows the link with no number rather than a wrong one.
	q.err = errors.New("doctender down")
	nc, err := svc.Nav(ctx, attyP)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := docTenderEntry(nc); !ok || e.Count != nil {
		t.Fatalf("failed count: %+v ok=%v", e, ok)
	}

	// Not composed: the link stays, with no count.
	svc.d.Badges = CoreServices{}.Badges()
	nc, _ = svc.Nav(ctx, adminP)
	if e, ok := docTenderEntry(nc); !ok || e.Count != nil {
		t.Fatalf("uncomposed Doc Tender: %+v ok=%v", e, ok)
	}
}

// PRD 8.21 Guide: the Doc Tender topic covers each file type, naming, the review queue
// and its settings, and opens from the ? on the review queue, the Doc Tender admin page
// and tab, and (by link) the Files tab.
func TestDocTenderGuideTopic(t *testing.T) {
	g, err := help.LoadGuide()
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"/doc-tender/review": "doc-tender", "/doc-tender/guide": "doc-tender", "/admin/doc-tender": "doc-tender",
		"/doc-tender/review/0190": "doc-tender-item",
	} {
		if got, ok := g.ForPath(path); !ok || got.ID != want {
			t.Errorf("? on %s opens %q, want %q", path, got.ID, want)
		}
	}
	for _, tpl := range []string{"doctender/review.html", "doctender/item.html", "doctender/admin.html", "doctender/guide.html"} {
		if _, ok := g.ForTemplate(tpl); !ok {
			t.Errorf("no topic for %s", tpl)
		}
	}
	topic, _ := g.Topic("doc-tender")
	text := help.PlainText(topic.Markdown)
	for _, must := range []string{
		"Word", "Spreadsheets", "PDFs", "Photos", "Audio", "Video", "unsupported", // each file type
		"naming pattern", "smith-2026-0042-motion-to-withdraw-2026-10-15.docx", "Status never goes in a name", "locked", // names
		"Only exceptions", "Accept", "Correct", "Assign matter", "Retry", "Keep as is", "live count", // queue
		"Administrative > Doc Tender", "AI tab", // settings
	} {
		if !strings.Contains(text, must) {
			t.Errorf("Doc Tender topic does not cover %q", must)
		}
	}
	for _, id := range []string{"files-matter", "admin-doc_tender", "doc-tender-item"} {
		tp, ok := g.Topic(id)
		if !ok || !strings.Contains(tp.Markdown, "(/help/topics/doc-tender)") {
			t.Errorf("topic %s does not link to the Doc Tender topic", id)
		}
	}
}

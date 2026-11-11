package help

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

type fakeSettings map[string]string

func (f fakeSettings) Get(_ context.Context, k string) (string, error) { return f[k], nil }

func newSvc(t *testing.T, s fakeSettings) *Service {
	t.Helper()
	n := 0
	svc, err := New(Deps{Store: NewMemory(), Settings: s, Version: "1.2.3", IDs: func() (domain.ID, error) {
		n++
		return domain.ID(fmt.Sprintf("0190c000-0000-7000-8000-%012d", n)), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

var (
	admin  = domain.Principal{UserID: "a", Roles: []domain.Role{domain.RoleAdmin}}
	staff  = domain.Principal{UserID: "s", Roles: []domain.Role{domain.RoleStaff}}
	client = domain.Principal{UserID: "c", Roles: []domain.Role{domain.RoleClient}}
	module = domain.Principal{UserID: "m", Roles: []domain.Role{domain.RoleStaff}, Source: domain.SourceModule, ModuleID: "matter-clock"}
)

func codeOf(err error) string {
	var api *domain.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func TestGuideParsesAndRenders(t *testing.T) {
	g, err := LoadGuide()
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Topics()) < 60 || len(g.Tutorials()) != 4 {
		t.Fatalf("guide: %d topics, %d tutorials", len(g.Topics()), len(g.Tutorials()))
	}
	pdf := g.GuidePDF("1.2.3")
	if !bytes.HasPrefix(pdf, []byte("%PDF-1.4")) || !bytes.HasSuffix(bytes.TrimSpace(pdf), []byte("%%EOF")) {
		t.Fatal("not a PDF")
	}
	for _, want := range []string{"(Contents)", "(Version 1.2.3)", "/Outlines", "/Subtype /Link", "(Home screen)", "(Getting started as an admin)"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Fatalf("PDF lacks %s", want)
		}
	}
	// The xref offsets point at the objects they name.
	xref := bytes.Index(pdf, []byte("\nxref\n")) + 1
	lines := strings.Split(string(pdf[xref:]), "\n")
	for i, l := range lines[3:12] {
		var off int
		fmt.Sscanf(l, "%d", &off)
		if !bytes.HasPrefix(pdf[off:], []byte(fmt.Sprintf("%d 0 obj", i+1))) {
			t.Fatalf("xref entry %d is wrong", i+1)
		}
	}
}

func TestMarkdownEscapesEverything(t *testing.T) {
	out := RenderMarkdown("Hello <script>alert(1)</script> **bold** [x](javascript:alert(1)) [ok](/help)\n\n- a\n- b\n\n1. one")
	for _, bad := range []string{"<script>", `href="javascript`} {
		if strings.Contains(out, bad) {
			t.Fatalf("unsafe markdown output: %s", out)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "<strong>bold</strong>", `<a href="/help">ok</a>`, "<ul>", "<ol>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
}

// AC-022 at the service: staff read, admins write, clients get not_found.
func TestSectionsAndItems(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t, fakeSettings{})
	title := "Firm procedures"
	if _, err := svc.CreateSection(ctx, staff, SectionInput{Title: &title}); codeOf(err) != domain.CodeForbidden {
		t.Fatalf("staff wrote a section: %v", err)
	}
	sec, err := svc.CreateSection(ctx, admin, SectionInput{Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	kind, it, body := "text", "Opening a file", "Every new matter gets a **conflict check** first."
	item, err := svc.CreateItem(ctx, admin, ItemInput{SectionID: &sec.ID, Title: &it, Kind: &kind, Body: &body})
	if err != nil {
		t.Fatal(err)
	}
	video, vt, empty := "video", "Intake walkthrough", ""
	if _, err := svc.CreateItem(ctx, admin, ItemInput{SectionID: &sec.ID, Title: &vt, Kind: &video, Body: &empty}); codeOf(err) != domain.CodeValidation {
		t.Fatalf("video without a file or link: %v", err)
	}
	link := "https://videos.firm.example/intake"
	if _, err := svc.CreateItem(ctx, admin, ItemInput{SectionID: &sec.ID, Title: &vt, Kind: &video, Body: &link}); err != nil {
		t.Fatal(err)
	}
	items, err := svc.Items(ctx, staff, sec.ID)
	if err != nil || len(items) != 2 || items[0].ID != item.ID {
		t.Fatalf("staff read: %v %+v", err, items)
	}
	if _, err := svc.Sections(ctx, client); codeOf(err) != domain.CodeNotFound {
		t.Fatalf("client read help: %v", err)
	}
	if _, err := svc.GuidePDF(client); codeOf(err) != domain.CodeNotFound {
		t.Fatalf("client got the guide: %v", err)
	}
	if err := svc.DeleteSection(ctx, admin, sec.ID); err != nil {
		t.Fatal(err)
	}
	if items, _ := svc.Items(ctx, staff, ""); len(items) != 0 {
		t.Fatal("deleting a section left its items")
	}
}

func TestModulePagesStayUnderTheirHeading(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t, fakeSettings{})
	pg := PageInput{ID: "start", Title: "Matter clock basics", Markdown: "Start the **clock** from the matter tab.", Keywords: []string{"stopwatch"}, Covers: []string{"matter-clock.widget"}}
	if _, err := svc.RegisterPage(ctx, module, "other-module", pg); codeOf(err) != domain.CodeForbidden {
		t.Fatalf("module wrote under another heading: %v", err)
	}
	if _, err := svc.RegisterPage(ctx, staff, "matter-clock", pg); codeOf(err) != domain.CodeForbidden {
		t.Fatalf("staff wrote a module page: %v", err)
	}
	bad := pg
	bad.Covers = []string{"practice-matters"}
	if _, err := svc.RegisterPage(ctx, module, "matter-clock", bad); codeOf(err) != domain.CodeValidation {
		t.Fatalf("module page covered a core screen: %v", err)
	}
	if _, err := svc.RegisterPage(ctx, module, "matter-clock", pg); err != nil {
		t.Fatal(err)
	}
	hits, _ := svc.SearchTopics(ctx, staff, "stopwatch", "")
	if len(hits) != 1 || hits[0].ID != "matter-clock.start" {
		t.Fatalf("module page not in guide search: %+v", hits)
	}
	if _, err := svc.Topic(ctx, staff, "matter-clock.start"); err != nil {
		t.Fatal(err)
	}
	// Update replaces the pages; disable hides them; uninstall removes them.
	if _, err := svc.ReplacePages(ctx, module, "matter-clock", []PageInput{{ID: "v2", Title: "Matter clock 2", Markdown: "New version."}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Topic(ctx, staff, "matter-clock.start"); codeOf(err) != domain.CodeNotFound {
		t.Fatalf("old version's page survived the update: %v", err)
	}
	if err := svc.SetModuleEnabled(ctx, "matter-clock", false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Topic(ctx, staff, "matter-clock.v2"); codeOf(err) != domain.CodeNotFound {
		t.Fatalf("disabled module's page shows: %v", err)
	}
	if err := svc.SetModuleEnabled(ctx, "matter-clock", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Topic(ctx, staff, "matter-clock.v2"); err != nil {
		t.Fatalf("re-enabled page hidden: %v", err)
	}
	if err := svc.RemoveModule(ctx, "matter-clock"); err != nil {
		t.Fatal(err)
	}
	if pages, _ := svc.ModulePages(ctx, admin, "matter-clock"); len(pages) != 0 {
		t.Fatal("uninstall left pages")
	}
}

func TestTopicSearchFindsScreensByWords(t *testing.T) {
	svc := newSvc(t, fakeSettings{})
	hits, err := svc.SearchTopics(context.Background(), staff, "time increment", "")
	if err != nil || len(hits) == 0 || hits[0].ID != "admin-time_billing" {
		t.Fatalf("search by words on the screen: %+v %v", hits, err)
	}
	hits, _ = svc.SearchTopics(context.Background(), staff, "", "/billing/invoices/0190")
	if len(hits) != 1 || hits[0].ID != "billing-invoice" {
		t.Fatalf("screen lookup: %+v", hits)
	}
}

// AC-028: llms.txt holds the product, version and agent pointers; the firm name only
// when shown; never a firm record.
func TestLLMsTxt(t *testing.T) {
	s := fakeSettings{"llms.show_firm_name": "true", "firm.name": "Synthetic Firm LLP", "llms.firm_paragraph": "We practice probate law.\nCall us."}
	svc := newSvc(t, s)
	out := svc.LLMsTxt(context.Background(), "https://firm.example")
	for _, want := range []string{"version 1.2.3", "Synthetic Firm LLP", "https://firm.example/api/v1/openapi.json", "https://firm.example/mcp", "bearer token", "We practice probate law. Call us."} {
		if !strings.Contains(out, want) {
			t.Fatalf("llms.txt lacks %q:\n%s", want, out)
		}
	}
	s["llms.show_firm_name"] = "false"
	if out := svc.LLMsTxt(context.Background(), "https://firm.example"); strings.Contains(out, "Synthetic Firm LLP") {
		t.Fatal("hidden firm name shown")
	}
}

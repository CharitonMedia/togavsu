package practice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

func TestPendingStaysHonest(t *testing.T) {
	text := strings.Join(Pending, "\n")
	for _, needle := range []string{"identity", "application-role SQL", "migration 200", "module include"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("pending list lost %q", needle)
		}
	}
}

func TestAC003WallHidesMatter(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	putUser(t, db, attorney, "Ada Attorney")
	putUser(t, db, staff, "Sam Staff")
	area := makeArea(t, svc, admin, "Probate")
	openMatter := makeMatter(t, svc, staff, area, "Visible matter", staff.UserID)
	secret := makeMatter(t, svc, staff, area, "Walled estate", staff.UserID)
	if err := svc.SetWall(ctx, admin, secret.ID, []domain.ID{attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	db.PutInvoice(ctx, domain.Invoice{Meta: domain.Meta{ID: tid(20)}, MatterID: secret.ID, Number: "INV-WALL", Status: "sent", BalanceDueCents: 500, IssuedOn: "2026-06-01"})
	db.PutTrust(ctx, domain.TrustBalance{Meta: domain.Meta{ID: tid(21)}, MatterID: secret.ID, BalanceCents: 800})

	for _, caller := range []domain.Principal{staff, admin} {
		views, err := svc.ListMatters(ctx, caller, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if hasMatter(views, secret.ID) || !hasMatter(views, openMatter.ID) {
			t.Fatalf("list leaked or dropped a matter for %v", caller.Roles)
		}
		if _, err := svc.GetMatter(ctx, caller, secret.ID); !isCode(err, domain.CodeNotFound) {
			t.Fatalf("get: %v", err)
		}
		for _, scope := range []string{ScopeMatters, ScopeAll} {
			result, err := svc.Search(ctx, caller, secret.Number, scope)
			if err != nil {
				t.Fatal(err)
			}
			if searchHas(result, secret.ID) {
				t.Fatalf("search %s leaked %s", scope, caller.Roles)
			}
		}
		widget, err := svc.OpenMattersWidget(ctx, caller)
		if err != nil {
			t.Fatal(err)
		}
		if widgetHas(widget, secret.ID) {
			t.Fatalf("widget leaked to %v", caller.Roles)
		}
		if caller.UserID == staff.UserID && !widgetHas(widget, openMatter.ID) {
			t.Fatalf("widget dropped the open matter %v", widget.Rows)
		}
		report, err := svc.MattersReport(ctx, caller)
		if err != nil {
			t.Fatal(err)
		}
		if tableHas(report, string(secret.ID)) || !tableHas(report, string(openMatter.ID)) {
			t.Fatalf("report leaked")
		}
		aging, err := svc.ARAging(ctx, caller, "")
		if err != nil {
			t.Fatal(err)
		}
		if tableHas(aging, string(secret.ID)) {
			t.Fatal("ar aging leaked")
		}
		trust, err := svc.TrustBalancesReport(ctx, caller)
		if err != nil {
			t.Fatal(err)
		}
		if tableHas(trust, string(secret.ID)) {
			t.Fatal("trust report leaked")
		}
	}
	seen, err := svc.GetMatter(ctx, attorney, secret.ID)
	if err != nil || seen.Matter.Name != "Walled estate" {
		t.Fatal(err, seen.Matter.Name)
	}
	body := getHTML(t, svc, staff, "/practice/matters")
	if strings.Contains(body, string(secret.ID)) || strings.Contains(body, "Walled estate") {
		t.Fatal("matter list page leaked the wall")
	}
	if !strings.Contains(body, "Visible matter") {
		t.Fatal("visible matter missing from the page")
	}
	status, apiBody := getAPI(t, svc, staff, "/api/v1/matters")
	if status != 200 || strings.Contains(apiBody, string(secret.ID)) {
		t.Fatalf("api list %d %s", status, apiBody)
	}
	status, apiBody = getAPI(t, svc, staff, "/api/v1/matters/"+string(secret.ID))
	if status != 404 || strings.Contains(apiBody, "Walled estate") {
		t.Fatalf("api get %d %s", status, apiBody)
	}
}

func TestAC009CloseWarnsAndStillCloses(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, person(tid(1), domain.RoleAdmin), "Probate")
	plain := makeMatter(t, svc, staff, area, "Plain", "")
	before := len(svc.db.Audits())
	if _, warnings, err := svc.PreviewClose(ctx, staff, plain.ID); err != nil || len(warnings) != 0 {
		t.Fatal(err, warnings)
	}
	if len(svc.db.Audits()) != before {
		t.Fatal("preview wrote history")
	}
	closed, record, err := svc.CloseMatter(ctx, staff, plain.ID, CloseInput{Reason: "done", Acknowledge: true})
	if err != nil || closed.Status != StatusClosed || !record.Acknowledged {
		t.Fatal(err, closed.Status, record.Acknowledged)
	}
	if _, ok := svc.db.LatestClose(plain.ID); !ok {
		t.Fatal("acknowledgement was not stored")
	}

	unpaid := makeMatter(t, svc, staff, area, "Unpaid", "")
	db.PutInvoice(ctx, domain.Invoice{Meta: domain.Meta{ID: tid(30)}, MatterID: unpaid.ID, Number: "INV-9", Status: "sent", BalanceDueCents: 2500, IssuedOn: "2026-05-01"})
	page := getHTML(t, svc, staff, "/practice/matters/"+string(unpaid.ID)+"/close")
	if !strings.Contains(page, "unpaid invoice") {
		t.Fatal(page)
	}
	closed, record, err = svc.CloseMatter(ctx, staff, unpaid.ID, CloseInput{Acknowledge: false})
	if err != nil || closed.Status != StatusClosed {
		t.Fatal("unpaid invoice blocked the close", err)
	}
	if len(record.Warnings) == 0 || record.Warnings[0].Code != WarnUnpaidInvoice {
		t.Fatal("warning was not stored", record.Warnings)
	}

	trustMatter := makeMatter(t, svc, staff, area, "Trust left", "")
	db.PutTrust(ctx, domain.TrustBalance{Meta: domain.Meta{ID: tid(31)}, MatterID: trustMatter.ID, BalanceCents: 75})
	page = getHTML(t, svc, staff, "/practice/matters/"+string(trustMatter.ID)+"/close")
	if !strings.Contains(page, "trust balance") {
		t.Fatal(page)
	}
	closed, record, err = svc.CloseMatter(ctx, staff, trustMatter.ID, CloseInput{Acknowledge: true})
	if err != nil || closed.Status != StatusClosed || record.Warnings[0].Code != WarnNonzeroTrust {
		t.Fatal(err, record.Warnings)
	}
}

func TestAC020SearchScopesAndWall(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	matter := makeMatter(t, svc, staff, area, "Searchable", "")
	if matter.Number != "2026-0001" {
		t.Fatal(matter.Number)
	}
	contact, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Rita", FamilyName: "Stone"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddPhone(ctx, staff, contact.ID, "mobile", "+15555550100", "2026-01-01", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddPhone(ctx, staff, contact.ID, "mobile", "5555550100", "2026-01-01", true); err == nil {
		t.Fatal("non E.164 phone accepted")
	}
	db.PutInvoice(ctx, domain.Invoice{Meta: domain.Meta{ID: tid(40)}, MatterID: matter.ID, Number: "INV-2044", Status: "sent", BalanceDueCents: 100, IssuedOn: "2026-06-02"})
	contact.SSNEINCiphertext = "999-88-7777"
	if err := db.UpdateContact(ctx, contact); err != nil {
		t.Fatal(err)
	}

	matterHit, err := svc.Search(ctx, staff, "2026-0001", ScopeMatters)
	if err != nil || !searchHas(matterHit, matter.ID) {
		t.Fatal(err, matterHit)
	}
	phoneHit, err := svc.Search(ctx, staff, "+15555550100", ScopePeople)
	if err != nil || !searchHas(phoneHit, contact.ID) {
		t.Fatal(err, phoneHit)
	}
	invoiceHit, err := svc.Search(ctx, staff, "INV-2044", ScopeInvoices)
	if err != nil || !searchHas(invoiceHit, tid(40)) || !strings.Contains(invoiceHit.Groups[0].Hits[0].Subtitle, matter.Number) {
		t.Fatal(err, invoiceHit)
	}
	for _, query := range []string{"2026-0001", "+15555550100", "INV-2044"} {
		all, err := svc.Search(ctx, staff, query, ScopeAll)
		if err != nil || len(all.Groups) != 4 {
			t.Fatal(query, err, all)
		}
		found := false
		for _, group := range all.Groups {
			if len(group.Hits) > 0 {
				found = true
			}
		}
		if !found {
			t.Fatalf("all scope missed %s", query)
		}
	}
	ssn, err := svc.Search(ctx, staff, "999-88", ScopePeople)
	if err != nil || searchHas(ssn, contact.ID) {
		t.Fatal("staff search used an attorney-only value", ssn)
	}
	attorney := person(tid(2), domain.RoleAttorney)
	putUser(t, db, attorney, "Ada Attorney")
	ssn, err = svc.Search(ctx, attorney, "999-88", ScopePeople)
	if err != nil || !searchHas(ssn, contact.ID) {
		t.Fatal("attorney should be able to search the stored identifier", err, ssn)
	}
	accountant := person(tid(8), domain.RoleAccountant)
	acctAll, err := svc.Search(ctx, accountant, "2026-0001", ScopeAll)
	if err != nil || searchHas(acctAll, matter.ID) || searchHas(acctAll, contact.ID) {
		t.Fatal("accountant search leaked a matter or person", err, acctAll)
	}
	acctInv, err := svc.Search(ctx, accountant, "INV-2044", ScopeInvoices)
	if err != nil || !searchHas(acctInv, tid(40)) {
		t.Fatal("accountant invoice search", err, acctInv)
	}
	if _, err := svc.Search(ctx, person(tid(9), domain.RoleClient), "2026-0001", ScopeAll); !isCode(err, domain.CodeForbidden) {
		t.Fatal(err)
	}

	secret := makeMatter(t, svc, staff, area, "Hidden file", "")
	hiddenPerson, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Hidden", FamilyName: "Party"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddPhone(ctx, staff, hiddenPerson.ID, "mobile", "+15555550199", "2026-01-01", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, secret.ID, PartyInput{ContactID: hiddenPerson.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	db.PutInvoice(ctx, domain.Invoice{Meta: domain.Meta{ID: tid(41)}, MatterID: secret.ID, Number: "INV-HIDE", Status: "sent", BalanceDueCents: 10, IssuedOn: "2026-06-03"})
	if err := svc.SetWall(ctx, admin, secret.ID, []domain.ID{attorney.UserID}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct{ q, scope string }{
		{secret.Number, ScopeMatters}, {secret.Number, ScopeAll},
		{"+15555550199", ScopePeople}, {"+15555550199", ScopeAll},
		{"INV-HIDE", ScopeInvoices}, {"INV-HIDE", ScopeAll},
	} {
		result, err := svc.Search(ctx, staff, query.q, query.scope)
		if err != nil {
			t.Fatal(err)
		}
		if searchHas(result, secret.ID) || searchHas(result, hiddenPerson.ID) || searchHas(result, tid(41)) {
			t.Fatalf("walled search %s %s leaked", query.scope, query.q)
		}
	}
}

func TestAC023ReopenAndZeroFee(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	matter := makeMatter(t, svc, staff, area, "Hold then close", "")
	matter, err := svc.UpdateMatter(ctx, staff, matter.ID, MatterInput{
		Name: matter.Name, PracticeAreaID: area, Status: StatusOnHold, BillingType: BillingHourly,
	})
	if err != nil {
		t.Fatal(err)
	}
	closed, _, err := svc.CloseMatter(ctx, staff, matter.ID, CloseInput{Reason: "finished", Acknowledge: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReopenMatter(ctx, attorney, matter.ID, "   "); !isCode(err, domain.CodeValidation) {
		t.Fatal(err)
	}
	still, _ := svc.db.Matter(ctx, matter.ID)
	if still.Status != StatusClosed {
		t.Fatal(still.Status)
	}
	if _, err := svc.ReopenMatter(ctx, person(tid(9), domain.RoleClient), matter.ID, "client tried"); !isCode(err, domain.CodeForbidden) {
		t.Fatal(err)
	}
	if _, err := svc.ReopenMatter(ctx, person(tid(8), domain.RoleAccountant), matter.ID, "books tried"); !isCode(err, domain.CodeForbidden) {
		t.Fatal(err)
	}
	if _, err := svc.ReopenMatter(ctx, staff, matter.ID, "staff tried"); !isCode(err, domain.CodeForbidden) {
		t.Fatal(err)
	}
	reopened, err := svc.ReopenMatter(ctx, attorney, matter.ID, "client returned")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != StatusOnHold || reopened.ClosedOn != closed.ClosedOn || reopened.CloseReason != "finished" {
		t.Fatalf("status %s closed_on %s reason %s", reopened.Status, reopened.ClosedOn, reopened.CloseReason)
	}
	timeline, err := svc.Timeline(ctx, attorney, matter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !timelineHas(timeline, "client returned") {
		t.Fatal(timeline)
	}
	card := getHTML(t, svc, attorney, "/practice/matters/"+string(matter.ID))
	if !strings.Contains(card, "data-reason=\"client returned\"") || !strings.Contains(card, ">Who<") || !strings.Contains(card, ">Note<") {
		t.Fatal(card)
	}
	adminMatter := makeMatter(t, svc, staff, area, "Admin reopen", "")
	if _, _, err := svc.CloseMatter(ctx, staff, adminMatter.ID, CloseInput{Reason: "done", Acknowledge: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReopenMatter(ctx, admin, adminMatter.ID, "admin reopen"); err != nil {
		t.Fatal(err)
	}

	zero := makeMatter(t, svc, staff, area, "No fee", "")
	if _, err := svc.PinMarker(ctx, staff, zero.ID, MarkerZeroFee, ""); err != nil {
		t.Fatal(err)
	}
	ordinary := makeMatter(t, svc, staff, area, "Ordinary bill", "")
	db.PutInvoice(ctx, domain.Invoice{Meta: domain.Meta{ID: tid(50)}, MatterID: zero.ID, Number: "INV-ZERO", Status: "sent", BalanceDueCents: 900, IssuedOn: "2026-01-01"})
	db.PutInvoice(ctx, domain.Invoice{Meta: domain.Meta{ID: tid(51)}, MatterID: ordinary.ID, Number: "INV-ORD", Status: "sent", BalanceDueCents: 400, IssuedOn: "2026-02-01"})
	proBono := makeMatter(t, svc, staff, area, "Pro bono work", "")
	proBono.BillingType = BillingProBono
	if err := db.UpdateMatter(ctx, proBono); err != nil {
		t.Fatal(err)
	}
	db.PutInvoice(ctx, domain.Invoice{Meta: domain.Meta{ID: tid(52)}, MatterID: proBono.ID, Number: "INV-PB", Status: "sent", BalanceDueCents: 100, IssuedOn: "2026-03-01"})
	aging, err := svc.ARAging(ctx, attorney, "2026-06-15")
	if err != nil {
		t.Fatal(err)
	}
	if tableHas(aging, string(zero.ID)) || !tableHas(aging, string(ordinary.ID)) || !tableHas(aging, string(proBono.ID)) {
		t.Fatalf("aging rows %+v", aging.Rows)
	}
	view, err := svc.GetMatter(ctx, staff, zero.ID)
	if err != nil || !hasMarker(view, MarkerZeroFee) {
		t.Fatal(err, view.Markers)
	}
}

func TestAC024HistoryRedactsAttorneyOnly(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	matter := makeMatter(t, svc, staff, area, "First name", "")
	if _, err := svc.UpdateMatter(ctx, staff, matter.ID, MatterInput{Name: "Second name", PracticeAreaID: area, BillingType: BillingHourly}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateMatter(ctx, staff, matter.ID, MatterInput{Name: "Third name", PracticeAreaID: area, BillingType: BillingHourly}); err != nil {
		t.Fatal(err)
	}
	history, err := svc.History(ctx, attorney, "matter", matter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !historyHas(history, "name", "First name", "Second name") || !historyHas(history, "name", "Second name", "Third name") {
		t.Fatal(history)
	}
	card := getHTML(t, svc, attorney, "/practice/matters/"+string(matter.ID))
	if !strings.Contains(card, "Edit history") || !strings.Contains(card, "First name") || !strings.Contains(card, "Third name") {
		t.Fatal(card)
	}
	if strings.Count(card, `data-field="name"`) < 2 {
		t.Fatal("card is missing a name change")
	}

	secret := "privilege-strategy-9f3a"
	other := "later-strategy-1c2d"
	record, err := svc.CreateConflict(ctx, attorney, ConflictInput{Name: "Opposing Co", Relationship: "opposing party", MatterID: matter.ID, Reason: secret})
	if err != nil {
		t.Fatal(err)
	}
	_ = record
	run, err := svc.RunConflictCheck(ctx, attorney, matter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Hits) == 0 {
		party, err := svc.CreateContact(ctx, attorney, ContactInput{Kind: KindPerson, FullName: "Opposing Co"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.AddParty(ctx, attorney, matter.ID, PartyInput{ContactID: party.ID, Role: "client", Primary: true}); err != nil {
			t.Fatal(err)
		}
		run, err = svc.RunConflictCheck(ctx, attorney, matter.ID)
		if err != nil || len(run.Hits) == 0 {
			t.Fatal(err, run)
		}
	}
	if _, err := svc.DecideConflict(ctx, staff, run.Hits[0].ID, DecisionClear, secret); !isCode(err, domain.CodeForbidden) {
		t.Fatal(err)
	}
	if _, err := svc.DecideConflict(ctx, attorney, run.Hits[0].ID, DecisionEscalate, secret); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecideConflict(ctx, attorney, run.Hits[0].ID, DecisionClear, other); err != nil {
		t.Fatal(err)
	}
	staffRows, err := svc.History(ctx, staff, "matter", matter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !historyRedacted(staffRows, "conflict_reason") {
		t.Fatal(staffRows)
	}
	raw, _ := json.Marshal(staffRows)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), other) {
		t.Fatal(string(raw))
	}
	status, body := getAPI(t, svc, staff, "/api/v1/matters/"+string(matter.ID)+"/history")
	if status != 200 || strings.Contains(body, secret) || !strings.Contains(body, `"redacted":true`) {
		t.Fatalf("history api %d %s", status, body)
	}
	staffCard := getHTML(t, svc, staff, "/practice/matters/"+string(matter.ID))
	if strings.Contains(staffCard, secret) || strings.Contains(staffCard, other) || !strings.Contains(staffCard, Redacted) {
		t.Fatal("staff card showed the secret")
	}
	attorneyCard := getHTML(t, svc, attorney, "/practice/matters/"+string(matter.ID))
	if !strings.Contains(attorneyCard, secret) || !strings.Contains(attorneyCard, other) {
		t.Fatal("attorney card dropped a change")
	}
	if strings.Count(attorneyCard, "data-field=") < len(history) {
		t.Fatalf("card rows %d history %d", strings.Count(attorneyCard, "data-field="), len(history))
	}
}

func TestAC024AppendOnlyRefusesMutation(t *testing.T) {
	svc, db := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	makeMatter(t, svc, staff, area, "Ledger", "")
	audits := db.Audits()
	events := db.Events()
	if len(audits) == 0 || len(events) == 0 {
		t.Fatal("expected audit and timeline rows")
	}
	auditBefore := audits[0].Entry
	eventBefore := events[0]
	changed := auditBefore
	changed.NewValue = "tampered"
	if err := db.UpdateAudit(ctx, changed); err == nil {
		t.Fatal("store update accepted")
	}
	if err := db.DeleteAudit(ctx, auditBefore.ID); err == nil {
		t.Fatal("store delete accepted")
	}
	if err := svc.UpdateAudit(ctx, changed); !isCode(err, domain.CodeAppendOnly) {
		t.Fatal(err)
	}
	if err := svc.DeleteAudit(ctx, auditBefore.ID); !isCode(err, domain.CodeAppendOnly) {
		t.Fatal(err)
	}
	eventChanged := eventBefore
	eventChanged.Summary = "tampered"
	if err := db.UpdateEvent(ctx, eventChanged); err == nil || svc.UpdateEvent(ctx, eventChanged) == nil {
		t.Fatal("event update accepted")
	}
	if err := db.DeleteEvent(ctx, eventBefore.ID); err == nil || svc.DeleteEvent(ctx, eventBefore.ID) == nil {
		t.Fatal("event delete accepted")
	}
	got, err := db.Audit(ctx, auditBefore.ID)
	if err != nil || got.Entry != auditBefore {
		t.Fatalf("audit changed: %+v", got.Entry)
	}
	gotEvent, err := db.Event(ctx, eventBefore.ID)
	if err != nil || gotEvent != eventBefore {
		t.Fatalf("event changed: %+v", gotEvent)
	}
}

func TestAC025CustomFieldAreaCSVAndUI(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	staff := person(tid(3), domain.RoleStaff)
	probate := makeArea(t, svc, admin, "Probate")
	criminal := makeArea(t, svc, admin, "Criminal")
	def, err := svc.CreateField(ctx, admin, FieldDef{
		RecordType: RecordMatter, Label: "Heir note", FieldType: FieldText, PracticeAreaIDs: []domain.ID{probate},
	})
	if err != nil {
		t.Fatal(err)
	}
	probateMatter := makeMatter(t, svc, staff, probate, "Estate of Quinn", "")
	criminalMatter := makeMatter(t, svc, staff, criminal, "State v Quinn", "")
	if _, err := svc.GetMatter(ctx, staff, criminalMatter.ID); err != nil {
		t.Fatal(err)
	}
	status, body := putJSON(t, svc, staff, "/api/v1/matters/"+string(probateMatter.ID)+"/custom-fields", map[string]any{
		"values": []map[string]string{{"definition_id": string(def.ID), "key": def.Key, "text": "niece"}},
	})
	if status != 200 || !strings.Contains(body, "niece") || !strings.Contains(body, def.Key) {
		t.Fatalf("save %d %s", status, body)
	}
	status, body = getAPI(t, svc, staff, "/api/v1/matters/"+string(criminalMatter.ID))
	if status != 200 || strings.Contains(body, def.Key) || strings.Contains(body, "Heir note") {
		t.Fatalf("criminal payload %d %s", status, body)
	}
	status, _ = putJSON(t, svc, staff, "/api/v1/matters/"+string(criminalMatter.ID)+"/custom-fields", map[string]any{
		"values": []map[string]string{{"definition_id": string(def.ID), "key": def.Key, "text": "nope"}},
	})
	if status < 400 {
		t.Fatal(status)
	}
	csv, err := svc.ExportMattersCSV(ctx, staff)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv, "Heir note") || !strings.Contains(csv, "niece") {
		t.Fatal(csv)
	}
	probatePage := getHTML(t, svc, staff, "/practice/matters/"+string(probateMatter.ID))
	criminalPage := getHTML(t, svc, staff, "/practice/matters/"+string(criminalMatter.ID))
	if !strings.Contains(probatePage, "Heir note") || strings.Contains(criminalPage, "Heir note") {
		t.Fatal("field visibility")
	}
	cookie, token := csrf(t, svc, staff, "/practice/matters/"+string(probateMatter.ID))
	form := url.Values{}
	form.Set("csrf", token)
	form.Set("cf_"+string(def.ID), "nephew")
	status, _ = postForm(t, svc, staff, "/practice/matters/"+string(probateMatter.ID)+"/custom-fields", form, cookie)
	if status != 303 {
		t.Fatal(status)
	}
	status, body = getAPI(t, svc, staff, "/api/v1/matters/"+string(probateMatter.ID))
	if status != 200 || !strings.Contains(body, "nephew") {
		t.Fatal(body)
	}
	bad := url.Values{}
	bad.Set("cf_"+string(def.ID), "blocked")
	status, _ = postForm(t, svc, staff, "/practice/matters/"+string(probateMatter.ID)+"/custom-fields", bad, cookie)
	if status != 403 {
		t.Fatal("csrf", status)
	}
	if _, err := svc.RetireField(ctx, admin, def.ID); err != nil {
		t.Fatal(err)
	}
	view, err := svc.GetMatter(ctx, staff, probateMatter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.CustomFields) != 0 {
		t.Fatal("retired field still shown")
	}
	if len(svc.db.Values(probateMatter.ID)) != 1 {
		t.Fatal("retiring deleted the value")
	}
}

func TestConflictsNotesAndCaseNumber(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	admin := person(tid(1), domain.RoleAdmin)
	attorney := person(tid(2), domain.RoleAttorney)
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, admin, "Probate")
	matter := makeMatter(t, svc, staff, area, "23AR-CV00825", "")
	matter.Court = "Circuit"
	if err := svc.db.UpdateMatter(ctx, matter); err != nil {
		t.Fatal(err)
	}
	needs, err := svc.NeedsCaseNumber(ctx, staff)
	if err != nil || len(needs) != 1 {
		t.Fatal(err, needs)
	}
	if _, err := svc.UpdateMatter(ctx, staff, matter.ID, MatterInput{Name: matter.Name, PracticeAreaID: area, CaseNumber: "23AR-CV00825", Court: "Circuit", BillingType: BillingHourly}); err != nil {
		t.Fatal(err)
	}
	needs, err = svc.NeedsCaseNumber(ctx, staff)
	if err != nil || len(needs) != 0 {
		t.Fatal(needs)
	}
	client, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, GivenName: "Ada", FamilyName: "Lovelace"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, matter.ID, PartyInput{ContactID: client.ID, Role: "client", Primary: true}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.GetMatter(ctx, staff, matter.ID)
	if err != nil || !view.Unchecked {
		t.Fatal(view.Unchecked, err)
	}
	if _, err := svc.CreateConflict(ctx, attorney, ConflictInput{Name: "Ada Lovelace", Relationship: "prior client", Reason: "old file"}); err != nil {
		t.Fatal(err)
	}
	run, err := svc.RunConflictCheck(ctx, staff, matter.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Hits) == 0 || run.Hits[0].Score < 60 {
		t.Fatal(run.Hits)
	}
	if run.Hits[0].Reason != Redacted {
		t.Fatal("staff saw the conflict reason", run.Hits[0].Reason)
	}
	view, err = svc.GetMatter(ctx, staff, matter.ID)
	if err != nil || view.Unchecked {
		t.Fatal("badge stayed after the check", view.Unchecked)
	}
	other, err := svc.CreateContact(ctx, staff, ContactInput{Kind: KindPerson, FullName: "Xavier Quinn"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddParty(ctx, staff, matter.ID, PartyInput{ContactID: other.ID, Role: "witness"}); err != nil {
		t.Fatal(err)
	}
	view, _ = svc.GetMatter(ctx, staff, matter.ID)
	if !view.Unchecked {
		t.Fatal("new party should be unchecked")
	}
	if ScoreName("zzzz", "aaaa", "", "") >= 60 {
		t.Fatal("unrelated names scored too high")
	}
	module := domain.Principal{UserID: tid(4), Roles: []domain.Role{domain.RoleAttorney}, Source: domain.SourceModule}
	note, err := svc.CreateNote(ctx, module, NoteInput{MatterID: matter.ID, Title: "From module", Body: "<script>alert(1)</script><b>hello</b>"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note.Body, "<script>") || strings.Contains(note.Body, "alert") || !strings.Contains(note.Body, "hello") {
		t.Fatal(note.Body)
	}
	staffNote, err := svc.CreateNote(ctx, staff, NoteInput{MatterID: matter.ID, Title: "Staff", Body: "line\n<b>keep</b>", Pinned: true})
	if err != nil || !strings.Contains(staffNote.Body, "<b>keep</b>") {
		t.Fatal(err, staffNote.Body)
	}
	page := getHTML(t, svc, staff, "/practice/notes")
	if !strings.Contains(page, "&lt;b&gt;keep&lt;/b&gt;") || strings.Contains(page, "alert(1)") {
		t.Fatal(page)
	}
	if _, err := svc.GetNote(ctx, person(tid(8), domain.RoleAccountant), staffNote.ID); !isCode(err, domain.CodeForbidden) {
		t.Fatal(err)
	}
	old, err := svc.AddPhone(ctx, staff, client.ID, "home", "+15555550123", "2020-01-01", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ChangePhone(ctx, staff, old.ID, "+15555550124", "2026-06-01"); err != nil {
		t.Fatal(err)
	}
	prev, err := svc.db.Phone(ctx, old.ID)
	if err != nil || prev.EffectiveTo != "2026-06-01" || prev.PhoneE164 != "+15555550123" {
		t.Fatal(prev)
	}
}

func TestActivityGroups(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	staff := person(tid(3), domain.RoleStaff)
	area := makeArea(t, svc, person(tid(1), domain.RoleAdmin), "Probate")
	if _, err := makeMatterErr(svc, staff, area, "Today matter"); err != nil {
		t.Fatal(err)
	}
	groups, err := svc.ActivityFeed(ctx, staff, "")
	if err != nil || len(groups) == 0 || groups[0].Label != "Today" {
		t.Fatal(err, groups)
	}
}

func newSvc(t *testing.T) (*Service, *Memory) {
	t.Helper()
	db := NewMemory()
	svc, err := NewWithSettings(db)
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return time.Date(2026, 6, 15, 15, 0, 0, 0, time.UTC) }
	var n uint64
	svc.newID = func() (domain.ID, error) {
		n++
		return domain.ComposeV7(1_700_000_000_000+n, uint16(n%4000), n+1)
	}
	return svc, db
}

func tid(n uint64) domain.ID {
	id, err := domain.ComposeV7(1_767_000_000_000, uint16(n), n+20)
	if err != nil {
		panic(err)
	}
	return id
}

func person(id domain.ID, roles ...domain.Role) domain.Principal {
	return domain.Principal{UserID: id, Roles: roles, Source: domain.SourceUI}
}

func putUser(t *testing.T, db *Memory, p domain.Principal, name string) {
	t.Helper()
	roles, err := domain.MarshalRoles(p.Roles)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutUser(context.Background(), domain.User{Meta: domain.Meta{ID: p.UserID}, Name: name, Roles: roles, Active: true}); err != nil {
		t.Fatal(err)
	}
}

func makeArea(t *testing.T, svc *Service, admin domain.Principal, name string) domain.ID {
	t.Helper()
	area, err := svc.CreateArea(context.Background(), admin, name, "", []string{"Intake"})
	if err != nil {
		t.Fatal(err)
	}
	return area.ID
}

func makeMatter(t *testing.T, svc *Service, p domain.Principal, area domain.ID, name string, staff domain.ID) domain.Matter {
	t.Helper()
	row, err := makeMatterErr(svc, p, area, name)
	if err != nil {
		t.Fatal(err)
	}
	if !staff.IsZero() {
		row.AssignedStaffID = staff
		row.ResponsibleAttorneyID = domain.ID("")
		if err := svc.db.UpdateMatter(context.Background(), row); err != nil {
			t.Fatal(err)
		}
	}
	return row
}

func makeMatterErr(svc *Service, p domain.Principal, area domain.ID, name string) (domain.Matter, error) {
	return svc.CreateMatter(context.Background(), p, MatterInput{Name: name, PracticeAreaID: area, BillingType: BillingHourly})
}

func hasMatter(rows []MatterView, id domain.ID) bool {
	for _, row := range rows {
		if row.Matter.ID == id {
			return true
		}
	}
	return false
}

func searchHas(result SearchResult, id domain.ID) bool {
	for _, group := range result.Groups {
		for _, hit := range group.Hits {
			if hit.RecordID == id {
				return true
			}
		}
	}
	return false
}

func widgetHas(widget Widget, id domain.ID) bool {
	for _, row := range widget.Rows {
		if row.ID == string(id) {
			return true
		}
	}
	return false
}

func tableHas(table ReportTable, needle string) bool {
	for _, row := range table.Rows {
		for _, value := range row {
			if strings.Contains(value, needle) {
				return true
			}
		}
	}
	return false
}

func timelineHas(rows []TimelineEntry, needle string) bool {
	for _, row := range rows {
		if strings.Contains(row.Summary, needle) {
			return true
		}
	}
	return false
}

func historyHas(rows []HistoryRow, field, old, neu string) bool {
	for _, row := range rows {
		if row.Field == field && row.Old == old && row.New == neu {
			return true
		}
	}
	return false
}

func historyRedacted(rows []HistoryRow, field string) bool {
	found := false
	for _, row := range rows {
		if row.Field != field {
			continue
		}
		found = true
		if !row.Redacted || row.Old != Redacted || row.New != Redacted {
			return false
		}
	}
	return found
}

func hasMarker(view MatterView, label string) bool {
	for _, marker := range view.Markers {
		if marker.Label == label {
			return true
		}
	}
	return false
}

func isCode(err error, code string) bool {
	var api *domain.APIError
	return errors.As(err, &api) && api.Code == code
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func testHandler(t *testing.T, svc *Service) *Handler {
	t.Helper()
	h, err := NewHandler(svc, filepath.Join(repoRoot(t), "web", "templates", "practice"), func(r *http.Request) (domain.Principal, error) {
		raw := r.Header.Get("X-Test-User")
		if raw == "" {
			return domain.Principal{}, domain.Unauthenticated("sign in required")
		}
		id, err := domain.ParseID(raw)
		if err != nil {
			return domain.Principal{}, err
		}
		var roles []domain.Role
		for _, part := range strings.Split(r.Header.Get("X-Test-Roles"), ",") {
			role, err := domain.ParseRole(strings.TrimSpace(part))
			if err != nil {
				return domain.Principal{}, err
			}
			roles = append(roles, role)
		}
		return domain.Principal{UserID: id, Roles: roles, Source: domain.SourceUI}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func authReq(method, path string, body io.Reader, p domain.Principal) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("X-Test-User", string(p.UserID))
	roles := make([]string, len(p.Roles))
	for i, role := range p.Roles {
		roles[i] = string(role)
	}
	req.Header.Set("X-Test-Roles", strings.Join(roles, ","))
	return req
}

func getHTML(t *testing.T, svc *Service, p domain.Principal, path string) string {
	t.Helper()
	h := testHandler(t, svc)
	res := httptest.NewRecorder()
	h.Routes().ServeHTTP(res, authReq(http.MethodGet, path, nil, p))
	if res.Code != 200 {
		t.Fatalf("%s %d %s", path, res.Code, res.Body.String())
	}
	return res.Body.String()
}

func getAPI(t *testing.T, svc *Service, p domain.Principal, path string) (int, string) {
	t.Helper()
	h := testHandler(t, svc)
	res := httptest.NewRecorder()
	h.Routes().ServeHTTP(res, authReq(http.MethodGet, path, nil, p))
	return res.Code, res.Body.String()
}

func postJSON(t *testing.T, svc *Service, p domain.Principal, path string, body any, key string) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	h := testHandler(t, svc)
	req := authReq(http.MethodPost, path, strings.NewReader(string(raw)), p)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res := httptest.NewRecorder()
	h.Routes().ServeHTTP(res, req)
	return res.Code, res.Body.String()
}

func putJSON(t *testing.T, svc *Service, p domain.Principal, path string, body any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	h := testHandler(t, svc)
	res := httptest.NewRecorder()
	req := authReq(http.MethodPut, path, strings.NewReader(string(raw)), p)
	req.Header.Set("Content-Type", "application/json")
	h.Routes().ServeHTTP(res, req)
	return res.Code, res.Body.String()
}

func csrf(t *testing.T, svc *Service, p domain.Principal, path string) (string, string) {
	t.Helper()
	h := testHandler(t, svc)
	res := httptest.NewRecorder()
	h.Routes().ServeHTTP(res, authReq(http.MethodGet, path, nil, p))
	var token string
	for _, cookie := range res.Result().Cookies() {
		if cookie.Name == csrfCookie {
			token = cookie.Value
		}
	}
	if token == "" {
		t.Fatal("csrf cookie missing")
	}
	return token, token
}

func postForm(t *testing.T, svc *Service, p domain.Principal, path string, form url.Values, cookie string) (int, string) {
	t.Helper()
	h := testHandler(t, svc)
	req := authReq(http.MethodPost, path, strings.NewReader(form.Encode()), p)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: csrfCookie, Value: cookie})
	res := httptest.NewRecorder()
	h.Routes().ServeHTTP(res, req)
	return res.Code, res.Body.String()
}

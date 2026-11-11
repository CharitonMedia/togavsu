package editor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// profileBody is a court profile in LawCraft's schema 1 (docs/law.md at the pin).
func profileBody(t *testing.T, id string) map[string]any {
	t.Helper()
	raw := `{"schema": 1, "id": "` + id + `", "name": "Synthetic County brief", "jurisdiction": "Missouri", "verified": true,
 "docs": {"brief": {"label": "Brief", "page": {"size": "Letter", "top": 72, "bottom": 72, "left": 72, "right": 72},
  "lineNumbers": {"enabled": false}, "pageNumbers": {"position": "bottom", "align": "center", "format": "decimal"},
  "styles": [{"name": "Normal", "chr": {"font": "Times New Roman", "size": 13}, "para": {"lineSpacing": {"rule": "multiple", "value": 2}}},
   {"name": "Body", "basedOn": "Normal", "next": "Body"}],
  "sections": [{"id": "argument", "name": "Argument", "style": "Body"}],
  "checks": [{"kind": "minFontSize", "pt": 13, "source": "Synthetic Rule 1", "effective": "2025-01-01"}],
  "source": "Synthetic Rule 1", "effective": "2025-01-01"}}}`
	var b map[string]any
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func ptr(s string) *string { return &s }

func TestPGCourtProfilesStayUnverifiedUntilAPersonApproves(t *testing.T) {
	e := newEnv(t)
	agent := e.staff
	agent.Source, agent.TokenID = domain.SourceAPIToken, tid(t)
	created, err := e.svc.CreateProfile(e.ctx, agent, ProfileWrite{Name: ptr("Synthetic County brief"), Body: profileBody(t, "synthetic-county-brief")})
	if err != nil {
		t.Fatal(err)
	}
	// The body said verified: true; an agent's profile is unverified anyway.
	if created.Verified || created.VerifiedBy != nil || created.Body["verified"] != false {
		t.Fatalf("agent-made profile = %+v", created)
	}
	if _, err := e.svc.VerifyProfile(e.ctx, agent, created.ID); code(err) != domain.CodeForbidden {
		t.Fatalf("a token approved a profile: %v", err)
	}
	module := domain.Principal{UserID: e.admin.UserID, Roles: e.admin.Roles, Source: domain.SourceModule, ModuleID: "synthetic"}
	if _, err := e.svc.VerifyProfile(e.ctx, module, created.ID); code(err) != domain.CodeForbidden {
		t.Fatalf("a module approved a profile: %v", err)
	}
	if _, err := e.svc.VerifyProfile(e.ctx, e.staff, created.ID); code(err) != domain.CodeForbidden {
		t.Fatalf("staff approved a profile: %v", err)
	}
	ok, err := e.svc.VerifyProfile(e.ctx, e.attorney, created.ID)
	if err != nil || !ok.Verified || ok.VerifiedBy == nil || *ok.VerifiedBy != e.attorney.UserID || ok.VerifiedAt == nil || ok.Body["verified"] != true {
		t.Fatalf("approved = %+v, %v", ok, err)
	}
	// A changed body needs approving again.
	body := profileBody(t, "synthetic-county-brief")
	body["jurisdiction"] = "Synthetic State"
	changed, err := e.svc.UpdateProfile(e.ctx, e.staff, created.ID, ProfileWrite{Body: body})
	if err != nil || changed.Verified || changed.Body["jurisdiction"] != "Synthetic State" {
		t.Fatalf("changed = %+v, %v", changed, err)
	}
	renamed, err := e.svc.UpdateProfile(e.ctx, e.staff, created.ID, ProfileWrite{Name: ptr("Renamed")})
	if err != nil || renamed.Name != "Renamed" {
		t.Fatalf("renamed = %+v, %v", renamed, err)
	}
	fields := map[string]int{}
	for _, r := range e.audits("court_profile", created.ID, "") {
		fields[r.Field]++
	}
	if fields["body"] != 2 || fields["verified"] != 2 || fields["name"] != 1 {
		t.Fatalf("court profile audit = %v", fields)
	}
	list, err := e.svc.Profiles(e.ctx, e.staff)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := e.svc.Profiles(e.ctx, e.client); code(err) != domain.CodeForbidden {
		t.Fatalf("a client listed court profiles: %v", err)
	}
}

func TestPGCourtProfilesAreCheckedByLawCraft(t *testing.T) {
	e := newEnv(t)
	bad := profileBody(t, "bad-profile")
	bad["docs"].(map[string]any)["brief"].(map[string]any)["checks"] = []any{map[string]any{"kind": "noSuchCheck"}}
	if _, err := e.svc.CreateProfile(e.ctx, e.staff, ProfileWrite{Name: ptr("Bad"), Body: bad}); code(err) != domain.CodeValidation || !strings.Contains(err.Error(), "noSuchCheck") {
		t.Fatalf("LawCraft did not refuse an unknown check kind: %v", err)
	}
	if _, err := e.svc.CreateProfile(e.ctx, e.staff, ProfileWrite{Name: ptr("Escape"), Body: profileBody(t, "../../etc/x")}); code(err) != domain.CodeValidation {
		t.Fatalf("a path-like profile id = %v", err)
	}
	if _, err := e.svc.CreateProfile(e.ctx, e.staff, ProfileWrite{Name: ptr("One"), Body: profileBody(t, "dup")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateProfile(e.ctx, e.staff, ProfileWrite{Name: ptr("Two"), Body: profileBody(t, "dup")}); code(err) != domain.CodeConflict {
		t.Fatalf("a second profile with the same LawCraft id = %v", err)
	}
}

// The firm's court profiles are core records loaded into every run, so a run can apply one.
func TestPGEditorRunsApplyTheFirmsCourtProfiles(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.CreateProfile(e.ctx, e.staff, ProfileWrite{Name: ptr("Synthetic"), Body: profileBody(t, "synthetic-county-brief")}); err != nil {
		t.Fatal(err)
	}
	f := e.create(e.staff, "Brief.docx", e.matter, e.doc(), false)
	res, err := e.svc.Run(e.ctx, e.staff, f.ID, RunRequest{SaveAs: "version", Commands: []Command{{ID: "law.profiles.apply", Parameters: map[string]any{"id": "synthetic-county-brief", "doc": "brief"}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, b := e.current(f.ID)
	if err := CheckDOCX(b); err != nil || res.Version.Number != 2 {
		t.Fatalf("apply = %+v, %v", res.Version, err)
	}
	if _, err := e.svc.Run(e.ctx, e.staff, f.ID, RunRequest{SaveAs: "version", Commands: []Command{{ID: "law.profiles.apply", Parameters: map[string]any{"id": "not-a-firm-profile"}}}}); code(err) != domain.CodeValidation {
		t.Fatalf("an unknown profile = %v", err)
	}
	// The profile commands that write LawCraft's store are refused in runs.
	if _, err := e.svc.Run(e.ctx, e.staff, f.ID, RunRequest{SaveAs: "version", Commands: []Command{{ID: "law.profiles.approve", Parameters: map[string]any{"id": "synthetic-county-brief"}}}}); code(err) != CodeCommandRefused {
		t.Fatalf("approve in a run = %v", err)
	}
}

func TestPGEditContextGivesLawCraftTheFirmAndTheMatter(t *testing.T) {
	e := newEnv(t)
	p, err := e.svc.CreateProfile(e.ctx, e.staff, ProfileWrite{Name: ptr("Synthetic"), Body: profileBody(t, "synthetic-county-brief")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.VerifyProfile(e.ctx, e.attorney, p.ID); err != nil {
		t.Fatal(err)
	}
	f := e.create(e.staff, "Letter.docx", e.matter, e.doc(), false)
	if _, err := e.svc.Context(e.ctx, e.staff, f.ID); code(err) != domain.CodeConflict {
		t.Fatalf("context without the lock = %v", err)
	}
	if _, err := e.svc.AcquireLock(e.ctx, e.staff, f.ID); err != nil {
		t.Fatal(err)
	}
	c, err := e.svc.Context(e.ctx, e.staff, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, cmd := range c.Commands {
		ids = append(ids, cmd.ID)
	}
	if strings.Join(ids, ",") != "law.firm.set,law.profiles.create,law.profiles.approve,law.courts.save,"+PleadingDefaults {
		t.Fatalf("open commands = %v", ids)
	}
	// New Pleading's caption from the matter: court type, county, case number and both
	// sides, our client (the petitioner here) on the first side.
	defaults, _ := json.Marshal(c.Commands[4].Parameters)
	want := `{"caseNumber":"26AR-DR00123","county":"ADAIR","courtType":"CIRCUIT",` +
		`"firstSide":{"names":[{"client":true,"name":"Jane Roe"}],"role":"Petitioner"},` +
		`"secondSide":{"names":[{"client":false,"name":"John Roe"}],"role":"Respondent"},"weRepresent":"first"}`
	if string(defaults) != want {
		t.Fatalf("New Pleading defaults = %s\nwant %s", defaults, want)
	}
	firm := c.Commands[0].Parameters
	if firm["firmName"] != "Synthetic Law LLC" || firm["city"] != "Kirksville" || firm["zip"] != "63501" || firm["attorneyName"] != "Ada Example" || firm["barNumber"] != "12345" {
		t.Fatalf("firm = %v", firm)
	}
	if c.Matter == nil || c.Matter.CaseNumber != "26AR-DR00123" || c.Matter.County != "ADAIR" || c.Matter.CourtType != "CIRCUIT" || c.Author != "Sam Staff" {
		t.Fatalf("context = %+v", c)
	}
	// The defaults are law.pleadings.new's own fields: with the firm block and a title,
	// LawCraft writes the matter's caption from them.
	params := map[string]any{"type": "motion", "title": "Motion to Continue", "firm": firm}
	for k, v := range c.Commands[4].Parameters {
		if k != "weRepresent" {
			params[k] = v
		}
	}
	out, err := e.cli.Run(e.ctx, RunInput{Commands: []Command{{ID: "law.pleadings.new", Parameters: params}}, Out: "txt"})
	if err != nil {
		t.Fatalf("LawCraft refused New Pleading from the defaults: %v", err)
	}
	for _, s := range []string{"26AR-DR00123", "ADAIR", "JANE ROE", "JOHN ROE"} {
		if !strings.Contains(strings.ToUpper(string(out)), s) {
			t.Fatalf("the caption lacks %s: %s", s, out)
		}
	}
	// Togavsu's own command never reaches LawCraft in a run.
	if _, err := e.svc.Run(e.ctx, e.staff, f.ID, RunRequest{SaveAs: "version", Commands: []Command{c.Commands[4]}}); code(err) != CodeCommandRefused {
		t.Fatalf("%s in a run = %v", PleadingDefaults, err)
	}
	// Run in LawCraft as the browser editor would: the firm block and the court are accepted.
	if _, err := e.cli.Run(e.ctx, RunInput{Commands: []Command{c.Commands[0], c.Commands[3]}, Out: "txt"}); err != nil {
		t.Fatalf("LawCraft refused the open commands: %v", err)
	}
}

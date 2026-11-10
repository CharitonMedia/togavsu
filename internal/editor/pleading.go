package editor

import (
	"context"
	"regexp"
	"strings"
	"unicode"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/files"
)

// Party is one matter party for a caption.
type Party struct {
	Role    string
	Name    string
	Address string
}

// MatterInfo is what a pleading caption needs from a matter.
type MatterInfo struct {
	ID                    domain.ID
	Number                string
	Name                  string
	Court                 string
	County                string
	CaseNumber            string
	ClientPosition        string
	ResponsibleAttorneyID domain.ID
	Parties               []Party
}

// Matters reads a matter as the caller. It returns not found for a matter behind a wall
// the caller is not on, as practice does. PracticeMatters implements it.
type Matters interface {
	Matter(ctx context.Context, p domain.Principal, id domain.ID) (MatterInfo, error)
}

// PleadingRequest is New Pleading from a matter: the document type and its title.
type PleadingRequest struct {
	Type  string `json:"type"`
	Title string `json:"title"`
}

var pleadingTypes = map[string]bool{"motion": true, "notice": true, "answer": true, "petition": true}

// sides maps the firm's position to the caption's roles: (our role, their role, we are
// the first side).
func sides(position string) (string, string, bool) {
	switch pos := strings.ToLower(position); {
	case strings.Contains(pos, "respond"):
		return "Respondent", "Petitioner", false
	case strings.Contains(pos, "defend"):
		return "Defendant", "Plaintiff", false
	case strings.Contains(pos, "plaintiff"):
		return "Plaintiff", "Defendant", true
	default:
		return "Petitioner", "Respondent", true
	}
}

// ponytail: the court type is read from the matter's free-text court. Ceiling: courts
// named the Missouri way ("Circuit Court", "Associate Circuit Court"). Upgrade: a
// structured court type on the matter or a court record. Trigger: a matter whose court
// name does not start with its court type.
//
// courtType turns the matter's court ("Circuit Court of Adair County", "Associate
// Circuit") into New Pleading's court type: the words before COURT, upper case.
func courtType(court string) string {
	c := strings.ToUpper(strings.TrimSpace(court))
	c = strings.TrimPrefix(c, "THE ")
	c, _, _ = strings.Cut(c, " COURT")
	if c == "" || c == "COURT" {
		return "CIRCUIT"
	}
	return c
}

func countyName(county string) string {
	c := strings.ToUpper(strings.TrimSpace(county))
	return strings.TrimSpace(strings.TrimSuffix(c, " COUNTY"))
}

// firmAddressParts reads "street[, more street], city, ST 12345".
var (
	firmAddressParts = regexp.MustCompile(`^\s*(?P<street>.+?)\s*,\s*(?P<city>[^,]+?)\s*,\s*(?P<state>[A-Za-z]{2})\.?\s+(?P<zip>\d{5}(?:-\d{4})?)\s*$`)
	usE164           = regexp.MustCompile(`^\+1(\d{3})(\d{3})(\d{4})$`)
)

// firmAddress splits firm settings' one address ("1 Main St, Suite 2, Kirksville, MO
// 63501", commas or line breaks) into New Pleading's firm fields.
func firmAddress(addr string) (address1, address2, city, state, zip string, ok bool) {
	flat := strings.Join(strings.FieldsFunc(addr, func(r rune) bool { return r == '\n' || r == '\r' }), ", ")
	m := firmAddressParts.FindStringSubmatch(flat)
	if m == nil {
		return "", "", "", "", "", false
	}
	part := func(name string) string { return m[firmAddressParts.SubexpIndex(name)] }
	address1, address2, _ = strings.Cut(part("street"), ",")
	return strings.TrimSpace(address1), strings.TrimSpace(address2), part("city"), strings.ToUpper(part("state")), part("zip"), true
}

// usPhone writes an E.164 US number as (660) 555-0100; anything else stays as entered.
func usPhone(e164 string) string {
	if !usE164.MatchString(e164) {
		return e164
	}
	return usE164.ReplaceAllString(e164, "($1) $2-$3")
}

// Firm is LawCraft's firm block (law.firm.set and law.pleadings.new's firm) from firm
// settings and the responsible attorney.
func (s *Service) Firm(ctx context.Context, attorney domain.ID) (map[string]any, error) {
	get := func(key string) string {
		v, _ := s.settings.Get(ctx, key)
		return strings.TrimSpace(v)
	}
	firm := map[string]any{}
	set := func(k, v string) {
		if v != "" {
			firm[k] = v
		}
	}
	set("firmName", get("firm.name"))
	if a1, a2, city, state, zip, ok := firmAddress(get("firm.address")); ok {
		set("address1", a1)
		set("address2", a2)
		set("city", city)
		set("state", state)
		set("zip", zip)
	}
	set("phone", usPhone(get("firm.phone")))
	set("email", get("firm.email"))
	if !attorney.IsZero() {
		if u, err := s.users.Get(ctx, attorney); err == nil {
			set("attorneyName", u.Name)
			set("barNumber", u.BarNumber)
		}
	}
	return firm, nil
}

// firmRequired are the firm fields New Pleading needs, with where each comes from.
var firmRequired = []struct{ key, from string }{
	{"firmName", "firm name (Firm settings)"},
	{"address1", "firm address (Firm settings, as street, city, state ZIP)"},
	{"city", "firm address city"},
	{"state", "firm address state"},
	{"zip", "firm address ZIP"},
	{"phone", "firm phone (Firm settings)"},
	{"email", "firm email (Firm settings)"},
	{"attorneyName", "the matter's responsible attorney"},
	{"barNumber", "the responsible attorney's bar number"},
}

// PleadingParams are law.pleadings.new's parameters for the matter: court, county, case
// number and parties from the matter, the firm block from firm settings. The caller's
// access to the matter is checked by Matters.
func (s *Service) PleadingParams(ctx context.Context, p domain.Principal, matterID domain.ID, req PleadingRequest) (map[string]any, error) {
	if err := requireEditor(p); err != nil {
		return nil, err
	}
	if s.matters == nil {
		return nil, domain.Conflict("matter details are not available to the editor")
	}
	if !pleadingTypes[req.Type] {
		return nil, domain.Invalid("type", "type is motion, notice, answer or petition")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil, domain.Invalid("title", "title is required")
	}
	m, err := s.matters.Matter(ctx, p, matterID)
	if err != nil {
		return nil, err
	}
	ours, theirs, weFirst := sides(m.ClientPosition)
	type name = map[string]any
	var clients, others []any
	for _, party := range m.Parties {
		switch strings.ToLower(party.Role) {
		case "client":
			clients = append(clients, name{"name": party.Name, "client": true})
		case "opposing party":
			others = append(others, party.Name)
		}
	}
	if len(clients) == 0 {
		return nil, domain.Invalid("matter", "add the client to the matter's parties first")
	}
	if len(others) == 0 {
		return nil, domain.Invalid("matter", "add the opposing party to the matter's parties first")
	}
	county := countyName(m.County)
	if county == "" {
		return nil, domain.Invalid("matter", "set the matter's county first")
	}
	if m.CaseNumber == "" && req.Type != "petition" {
		return nil, domain.Invalid("matter", "set the matter's case number first (only a petition goes without one)")
	}
	firm, err := s.Firm(ctx, m.ResponsibleAttorneyID)
	if err != nil {
		return nil, err
	}
	for _, f := range firmRequired {
		if firm[f.key] == nil {
			return nil, domain.Invalid("firm", "New Pleading needs the "+f.from)
		}
	}
	us := name{"names": clients, "role": ours}
	them := name{"names": others, "role": theirs}
	first, second := us, them
	if !weFirst {
		first, second = them, us
	}
	return map[string]any{
		"type": req.Type, "title": title, "courtType": courtType(m.Court), "county": county,
		"caseNumber": m.CaseNumber, "firstSide": first, "secondSide": second, "firm": firm,
	}, nil
}

// NewPleading writes LawCraft's New Pleading skeleton with its caption filled from the
// matter, and files it as a new DOCX in the matter's Pleadings folder.
func (s *Service) NewPleading(ctx context.Context, p domain.Principal, matterID domain.ID, req PleadingRequest) (files.FileView, error) {
	params, err := s.PleadingParams(ctx, p, matterID, req)
	if err != nil {
		return files.FileView{}, err
	}
	out, err := s.cli.Run(ctx, RunInput{Commands: []Command{{ID: "law.pleadings.new", Parameters: params}}, Out: "docx", Author: s.userName(ctx, p.UserID)})
	if err != nil {
		return files.FileView{}, err
	}
	if err := CheckDOCX(out); err != nil {
		return files.FileView{}, err
	}
	name := sanitizeName(params["title"].(string)) + ".docx"
	unique, err := s.files.UniqueName(ctx, matterID, name)
	if err != nil {
		return files.FileView{}, err
	}
	nf := files.NewFile{Name: unique, MatterID: matterID, MediaType: docxMedia, Source: "editor", How: "editor", Note: "New Pleading (" + req.Type + ")"}
	if folder, err := s.files.FolderByName(ctx, p, matterID, "Pleadings"); err == nil {
		nf.FolderID = folder.ID
	}
	r, n := reader(out)
	return s.files.Create(ctx, p, nf, r, n)
}

// sanitizeName makes a title a file name; the file store checks its length.
func sanitizeName(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || unicode.IsControl(r) {
			return '-'
		}
		return r
	}, s))
}

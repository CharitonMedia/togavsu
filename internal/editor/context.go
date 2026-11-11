package editor

import (
	"context"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// EditContext is what the edit page gives LawCraft when the editor opens (PRD 8.20):
// the firm's details from firm settings, the firm's court profiles, the matter's court
// (LawCraft's law.firm.set, law.profiles.create/approve and law.courts.save) and the
// matter's caption for New Pleading (PleadingDefaults).
type EditContext struct {
	Author string `json:"author"`
	// Commands are the commands the page runs in the browser editor on open.
	Commands []Command `json:"commands"`
	// Matter is the matter's caption details, when the file has a matter.
	Matter *CaptionView `json:"matter,omitempty"`
}

// PleadingDefaults is the open-time command that fills New Pleading from the matter.
//
// LawCraft at the pin has no command that seeds its New Pleading window (law-ui
// pleading.rs opens PleadingForm::default(); only law.firm.set and law.courts.save reach
// it). The Togavsu wrapper (web/editor/src/caption.rs) takes this command instead and
// fills the window's court type, county, case number and both sides' parties and roles
// through law-ui's public PleadingForm whenever New Pleading opens. Its parameters are
// law.pleadings.new's own fields (courtType, county, caseNumber, firstSide, secondSide,
// weRepresent), so a LawCraft law.pleadings.defaults command, if one lands upstream,
// takes them as they are.
const PleadingDefaults = "togavsu.pleadings.defaults"

// CaptionView is the matter's caption details.
type CaptionView struct {
	ID         domain.ID `json:"id"`
	Court      string    `json:"court"`
	CourtType  string    `json:"court_type"`
	County     string    `json:"county"`
	CaseNumber string    `json:"case_number"`
	Parties    []string  `json:"parties"`
}

// Context is the edit page's open-time data. Only the holder of the live lock gets it.
func (s *Service) Context(ctx context.Context, p domain.Principal, fileID domain.ID) (EditContext, error) {
	if err := requireEditor(p); err != nil {
		return EditContext{}, err
	}
	f, err := s.files.Get(ctx, p, fileID)
	if err != nil {
		return EditContext{}, err
	}
	if err := s.holdsLock(ctx, p, fileID); err != nil {
		return EditContext{}, err
	}
	out := EditContext{Author: s.userName(ctx, p.UserID), Commands: []Command{}}
	var attorney domain.ID
	var m *MatterInfo
	if f.MatterID != nil && s.matters != nil {
		info, err := s.matters.Matter(ctx, p, *f.MatterID)
		if err != nil {
			return EditContext{}, err
		}
		m, attorney = &info, info.ResponsibleAttorneyID
	}
	firm, err := s.Firm(ctx, attorney)
	if err != nil {
		return EditContext{}, err
	}
	if len(firm) > 0 {
		out.Commands = append(out.Commands, Command{ID: "law.firm.set", Parameters: firm})
	}
	profiles, err := s.Profiles(ctx, p)
	if err != nil {
		return EditContext{}, err
	}
	for _, c := range profiles {
		out.Commands = append(out.Commands, Command{ID: "law.profiles.create", Parameters: c.Body})
		if c.Verified {
			// LawCraft saves every created profile unverified; a person approved this one
			// in Togavsu, so the editor shows it approved.
			out.Commands = append(out.Commands, Command{ID: "law.profiles.approve", Parameters: map[string]any{"id": lawcraftID(c.Body)}})
		}
	}
	if m != nil {
		cv := &CaptionView{ID: m.ID, Court: m.Court, CourtType: courtType(m.Court), County: countyName(m.County), CaseNumber: m.CaseNumber}
		for _, party := range m.Parties {
			cv.Parties = append(cv.Parties, party.Role+": "+party.Name)
		}
		out.Matter = cv
		if cv.County != "" {
			// The matter's court becomes a saved court, so New Pleading's Saved court list
			// offers it first.
			out.Commands = append(out.Commands, Command{ID: "law.courts.save", Parameters: map[string]any{
				"name": cv.CourtType + " COURT OF " + cv.County + " COUNTY", "courtType": cv.CourtType, "county": cv.County,
			}})
		}
		first, second, _, _ := captionSides(*m)
		_, _, weFirst := sides(m.ClientPosition)
		we := "first"
		if !weFirst {
			we = "second"
		}
		out.Commands = append(out.Commands, Command{ID: PleadingDefaults, Parameters: map[string]any{
			"courtType": cv.CourtType, "county": cv.County, "caseNumber": cv.CaseNumber,
			"firstSide": first, "secondSide": second, "weRepresent": we,
		}})
	}
	return out, nil
}

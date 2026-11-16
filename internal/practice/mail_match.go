package practice

import (
	"context"
	"strings"
	"unicode"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

const mailMatchCap = 50

// MailMatchRequest is matchMail's body (CORE-26).
type MailMatchRequest struct {
	Addresses      []string    `json:"addresses"`
	MatterNumbers  []string    `json:"matter_numbers"`
	CaseNumbers    []string    `json:"case_numbers"`
	NameTokens     []string    `json:"name_tokens"`
	KnownMatterIDs []domain.ID `json:"known_matter_ids"`
}

// MailMatchContact is one contact under contacts_by_address.
type MailMatchContact struct {
	ID         domain.ID `json:"id"`
	GivenName  string    `json:"given_name"`
	FamilyName string    `json:"family_name"`
	FullName   string    `json:"full_name"`
	Kind       string    `json:"kind"`
}

// MailMatchClient is a client party's first and last name on a matched matter.
type MailMatchClient struct {
	GivenName  string `json:"given_name"`
	FamilyName string `json:"family_name"`
}

// MailMatchMatter is one matter in matchMail's answer.
type MailMatchMatter struct {
	ID           domain.ID         `json:"id"`
	Number       string            `json:"number"`
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	CaseNumber   string            `json:"case_number"`
	PracticeArea string            `json:"practice_area"`
	Clients      []MailMatchClient `json:"clients"`
	Via          string            `json:"via"`
}

// MailMatchResult is matchMail's response without match_token.
type MailMatchResult struct {
	ContactsByAddress map[string][]MailMatchContact `json:"contacts_by_address"`
	Matters           []MailMatchMatter             `json:"matters"`
}

var mailViaRank = map[string]int{
	"known": 1, "address": 2, "matter_number": 3,
	"case_number": 4, "case_number_in_name": 5, "client_name": 6,
}

func trimMailList(in []string) ([]string, error) {
	if len(in) > mailMatchCap {
		return nil, domain.Invalid("body", "each match list is at most 50 entries")
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func trimMailIDs(in []domain.ID) ([]domain.ID, error) {
	if len(in) > mailMatchCap {
		return nil, domain.Invalid("body", "each match list is at most 50 entries")
	}
	out := make([]domain.ID, 0, len(in))
	for _, id := range in {
		if !id.IsZero() {
			out = append(out, id)
		}
	}
	return out, nil
}

// MatchMail finds contacts by address and matters by the CORE-26 match keys.
// A service principal omits every walled matter. A viewer call applies that viewer's walls.
func (s *Service) MatchMail(ctx context.Context, p domain.Principal, in MailMatchRequest) (MailMatchResult, error) {
	addresses, err := trimMailList(in.Addresses)
	if err != nil {
		return MailMatchResult{}, err
	}
	numbers, err := trimMailList(in.MatterNumbers)
	if err != nil {
		return MailMatchResult{}, err
	}
	cases, err := trimMailList(in.CaseNumbers)
	if err != nil {
		return MailMatchResult{}, err
	}
	tokens, err := trimMailList(in.NameTokens)
	if err != nil {
		return MailMatchResult{}, err
	}
	known, err := trimMailIDs(in.KnownMatterIDs)
	if err != nil {
		return MailMatchResult{}, err
	}

	serviceCall := (p.Source == domain.SourceService || p.Source == domain.SourceModule) && p.UserID.IsZero()
	out := MailMatchResult{
		ContactsByAddress: map[string][]MailMatchContact{},
		Matters:           []MailMatchMatter{},
	}

	addrIndex := map[string][]MailMatchContact{}
	today := s.clock().Format("2006-01-02")
	for _, c := range s.db.Contacts() {
		if c.Archived {
			continue
		}
		email := ""
		if e := currentPrimaryEmailRow(s.db.Emails(c.ID), today); e != nil {
			email = strings.ToLower(strings.TrimSpace(e.Email))
		}
		if email == "" {
			continue
		}
		addrIndex[email] = append(addrIndex[email], MailMatchContact{
			ID: c.ID, GivenName: c.GivenName, FamilyName: c.FamilyName, FullName: c.FullName, Kind: c.Kind,
		})
	}
	for _, addr := range addresses {
		rows := addrIndex[strings.ToLower(addr)]
		if rows == nil {
			rows = []MailMatchContact{}
		}
		out.ContactsByAddress[addr] = rows
	}

	type hit struct {
		row domain.Matter
		via string
	}
	hits := map[domain.ID]hit{}
	note := func(row domain.Matter, via string) {
		if prior, ok := hits[row.ID]; ok && mailViaRank[via] >= mailViaRank[prior.via] {
			return
		}
		hits[row.ID] = hit{row: row, via: via}
	}

	numberSet := map[string]bool{}
	for _, n := range numbers {
		numberSet[strings.ToLower(n)] = true
	}
	caseSet := map[string]bool{}
	for _, c := range cases {
		caseSet[c] = true
	}
	knownSet := map[domain.ID]bool{}
	for _, id := range known {
		knownSet[id] = true
	}
	addrContacts := map[domain.ID]bool{}
	for _, addr := range addresses {
		for _, c := range out.ContactsByAddress[addr] {
			addrContacts[c.ID] = true
		}
	}
	tokenLower := make([]string, len(tokens))
	for i, t := range tokens {
		tokenLower[i] = strings.ToLower(t)
	}

	ordered := []domain.ID{}
	for _, row := range s.db.Matters() {
		if knownSet[row.ID] {
			note(row, "known")
		}
		if numberSet[strings.ToLower(row.Number)] {
			note(row, "matter_number")
		}
		if row.CaseNumber != "" && caseSet[row.CaseNumber] {
			note(row, "case_number")
		}
		for _, c := range cases {
			if caseNumberInName(row.Name, c) {
				note(row, "case_number_in_name")
			}
		}
		for _, party := range s.db.Parties(row.ID, domain.ID("")) {
			if party.Role != "client" {
				continue
			}
			if addrContacts[party.ContactID] {
				note(row, "address")
			}
			contact, err := s.db.Contact(ctx, party.ContactID)
			if err != nil {
				continue
			}
			family := strings.ToLower(strings.TrimSpace(contact.FamilyName))
			given := strings.ToLower(strings.TrimSpace(contact.GivenName))
			for _, t := range tokenLower {
				if t != "" && (t == family || t == given || strings.EqualFold(t, contact.FullName)) {
					note(row, "client_name")
				}
			}
		}
		if _, ok := hits[row.ID]; ok {
			ordered = append(ordered, row.ID)
		}
	}

	areas := map[domain.ID]string{}
	for _, id := range ordered {
		h := hits[id]
		if serviceCall {
			if len(s.db.Walls(id)) > 0 {
				continue
			}
		} else if !s.visibleMatter(ctx, p, id) {
			continue
		}
		areaName := areas[h.row.PracticeAreaID]
		if areaName == "" && !h.row.PracticeAreaID.IsZero() {
			if area, err := s.db.Area(ctx, h.row.PracticeAreaID); err == nil {
				areaName = area.Name
				areas[h.row.PracticeAreaID] = areaName
			}
		}
		clients := []MailMatchClient{}
		for _, party := range s.db.Parties(id, domain.ID("")) {
			if party.Role != "client" {
				continue
			}
			contact, err := s.db.Contact(ctx, party.ContactID)
			if err != nil {
				continue
			}
			clients = append(clients, MailMatchClient{GivenName: contact.GivenName, FamilyName: contact.FamilyName})
		}
		out.Matters = append(out.Matters, MailMatchMatter{
			ID: id, Number: h.row.Number, Name: h.row.Name, Status: h.row.Status,
			CaseNumber: h.row.CaseNumber, PracticeArea: areaName, Clients: clients, Via: h.via,
		})
		if len(out.Matters) >= mailMatchCap {
			break
		}
	}
	return out, nil
}

// caseNumberInName reports caseNum in name with a boundary that excludes a following hyphen.
func caseNumberInName(name, caseNum string) bool {
	if caseNum == "" || name == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(name[i:], caseNum)
		if j < 0 {
			return false
		}
		at := i + j
		beforeOK := at == 0 || !isMailTokenChar(rune(name[at-1]))
		after := at + len(caseNum)
		afterOK := after >= len(name) || name[after] != '-'
		if beforeOK && afterOK {
			return true
		}
		i = at + 1
		if i >= len(name) {
			return false
		}
	}
}

func isMailTokenChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

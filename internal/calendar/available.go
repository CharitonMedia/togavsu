package calendar

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// Day portions in an available-dates result.
const (
	PortionFull      = "full"
	PortionMorning   = "morning"
	PortionAfternoon = "afternoon"
)

// maxRangeMonths is the allowed maximum of calendar.available_dates_range_months in
// the settings registry, so an explicit end date cannot ask for more than the admin
// could set as the default range.
func maxRangeMonths() int {
	for _, def := range settings.Definitions() {
		if def.Key == settingRangeMonths {
			return int(def.Max)
		}
	}
	panic("calendar: settings registry lacks " + settingRangeMonths)
}

// AvailableRequest is the available-dates form (PRD 8.16). Empty text fields are
// filled from the matter when one is picked, then from the firm defaults.
type AvailableRequest struct {
	UserID           domain.ID
	StartDate        string
	EndDate          string
	IncludeHalfDays  bool
	MatterID         domain.ID
	Court            string
	Caption          string
	LeftLabel        string
	RightLabel       string
	LeftName         string
	RightName        string
	CaseNumber       string
	FirmSide         string
	Purpose          string
	CoordinateWith   string
	Title            string
	ServiceDate      string
	LetterRecipient  string
	LetterAddress    string
	LetterRe         string
	LetterSalutation string
}

// AvailableDay is one free day, or the free half of one.
type AvailableDay struct {
	Date    string
	Portion string
}

// AvailableResult is the free days in both output forms, plus the filled request.
type AvailableResult struct {
	Request  AvailableRequest
	Dates    []AvailableDay
	Pleading string
	Letter   string
}

// AvailableDates lists the weekdays from start to end on which the attorney has no
// court date, block or all-day event. With half days on, a day with only its
// morning or only its afternoon free (split at firm-local noon) is listed as that
// half. It is read only: it never creates, changes or deletes an event, and it
// writes no audit or activity row.
//
// Every one of the attorney's busy events counts, including events on matters the
// caller cannot see, because a hidden court date still makes the attorney busy. The
// result shows only which dates are free, never what makes a date busy.
//
// ponytail: ceiling: Saturdays and Sundays are never listed and court holidays are
// not known. Upgrade path: a firm court-holiday list and a weekend setting on the
// Calendar tab. Trigger: a firm files dates for a court that sits on a weekend or a
// court holiday appears in a result.
func (s *Service) AvailableDates(ctx context.Context, p domain.Principal, in AvailableRequest) (AvailableResult, error) {
	if err := s.gate(ctx, p, domain.ActionRead, ""); err != nil {
		return AvailableResult{}, err
	}
	attorney, err := s.activeUser(ctx, "user_id", in.UserID)
	if err != nil {
		return AvailableResult{}, err
	}
	start, err := parseDate(in.StartDate)
	if err != nil {
		return AvailableResult{}, domain.Invalid("start_date", "start date must be YYYY-MM-DD")
	}
	var end time.Time
	if in.EndDate == "" {
		months, err := s.settingInt(ctx, settingRangeMonths)
		if err != nil {
			return AvailableResult{}, err
		}
		end = addMonths(start, months)
		in.EndDate = end.Format(dateLayout)
	} else if end, err = parseDate(in.EndDate); err != nil {
		return AvailableResult{}, domain.Invalid("end_date", "end date must be YYYY-MM-DD")
	}
	if end.Before(start) {
		return AvailableResult{}, domain.Invalid("end_date", "end date is before the start date")
	}
	if limit := maxRangeMonths(); end.After(addMonths(start, limit)) {
		return AvailableResult{}, domain.Invalid("end_date", fmt.Sprintf("the range can be at most %d months", limit))
	}
	if in.ServiceDate != "" {
		if _, err := parseDate(in.ServiceDate); err != nil {
			return AvailableResult{}, domain.Invalid("service_date", "service date must be YYYY-MM-DD")
		}
	}
	if in.FirmSide != "" && in.FirmSide != "left" && in.FirmSide != "right" {
		return AvailableResult{}, domain.Invalid("firm_side", "firm side must be left or right")
	}
	if !in.MatterID.IsZero() {
		m, err := s.d.Matters.Matter(ctx, p, in.MatterID)
		if err != nil {
			return AvailableResult{}, err
		}
		fillFromMatter(&in, m)
	}
	if err := s.fillDefaults(ctx, &in); err != nil {
		return AvailableResult{}, err
	}

	loc := s.Zone(ctx)
	events, err := s.d.Store.Events(ctx)
	if err != nil {
		return AvailableResult{}, err
	}
	var busy []Event
	for _, e := range events {
		if hasID(e.Attendees, in.UserID) && (e.Kind == KindCourtDate || e.Kind == KindBlock || e.AllDay) {
			busy = append(busy, e)
		}
	}
	var days []AvailableDay
	for d := start; !d.After(end); d = nextDay(d) {
		if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday {
			continue
		}
		ds, de := dayStart(d, loc), dayStart(nextDay(d), loc)
		noon := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, loc)
		var am, pm bool
		for _, e := range busy {
			if e.AllDay {
				if overlaps(e.Start(), e.End(), ds, de) {
					am, pm = true, true
				}
				continue
			}
			am = am || overlaps(e.Start(), e.End(), ds, noon)
			pm = pm || overlaps(e.Start(), e.End(), noon, de)
		}
		date := d.Format(dateLayout)
		switch {
		case !am && !pm:
			days = append(days, AvailableDay{Date: date, Portion: PortionFull})
		case in.IncludeHalfDays && !am:
			days = append(days, AvailableDay{Date: date, Portion: PortionMorning})
		case in.IncludeHalfDays && !pm:
			days = append(days, AvailableDay{Date: date, Portion: PortionAfternoon})
		}
	}
	firm, _ := s.setting(ctx, "firm.name")
	return AvailableResult{
		Request:  in,
		Dates:    days,
		Pleading: pleading(in, days, attorney),
		Letter:   letter(in, days, attorney, firm, s.Today(ctx)),
	}, nil
}

// fillFromMatter copies the matter's court, case number and parties into empty fields.
func fillFromMatter(in *AvailableRequest, m MatterInfo) {
	pos := strings.ToLower(m.ClientPosition)
	labelsFromMatter := in.LeftLabel == "" && in.RightLabel == ""
	if labelsFromMatter && (strings.Contains(pos, "petition") || strings.Contains(pos, "respond")) {
		in.LeftLabel, in.RightLabel = "Petitioner", "Respondent"
	}
	if in.FirmSide == "" && (strings.Contains(pos, "respond") || strings.Contains(pos, "defend")) {
		in.FirmSide = "right"
	}
	if in.Court == "" {
		in.Court = m.Court
	}
	if in.CaseNumber == "" {
		in.CaseNumber = m.CaseNumber
	}
	if in.Caption == "" {
		in.Caption = m.Name
	}
	client, opposing, counsel := "", "", ""
	for _, party := range m.Parties {
		switch party.Role {
		case "client":
			if client == "" {
				client = party.Name
			}
		case "opposing party":
			if opposing == "" {
				opposing = party.Name
			}
		case "opposing counsel":
			if counsel == "" {
				counsel = party.Name
			}
		}
	}
	firmLeft := in.FirmSide != "right"
	if in.LeftName == "" {
		in.LeftName = pick(firmLeft, client, opposing)
	}
	if in.RightName == "" {
		in.RightName = pick(firmLeft, opposing, client)
	}
	if in.CoordinateWith == "" {
		in.CoordinateWith = counsel
	}
}

func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func (s *Service) fillDefaults(ctx context.Context, in *AvailableRequest) error {
	if in.LeftLabel == "" {
		in.LeftLabel = "Plaintiff"
	}
	if in.RightLabel == "" {
		in.RightLabel = "Defendant"
	}
	if in.FirmSide == "" {
		in.FirmSide = "left"
	}
	if in.Purpose == "" {
		v, err := s.setting(ctx, settingPurpose)
		if err != nil {
			return err
		}
		in.Purpose = v
	}
	if in.Title == "" {
		v, err := s.setting(ctx, settingTitle)
		if err != nil {
			return err
		}
		in.Title = v
	}
	if in.LetterRecipient == "" {
		in.LetterRecipient = in.CoordinateWith
	}
	if in.LetterRe == "" {
		re := in.Caption
		if re == "" && (in.LeftName != "" || in.RightName != "") {
			re = in.LeftName + " v. " + in.RightName
		}
		if in.CaseNumber != "" {
			re = strings.TrimSpace(re + ", Case No. " + in.CaseNumber)
		}
		in.LetterRe = re
	}
	if in.LetterSalutation == "" {
		if in.LetterRecipient != "" {
			in.LetterSalutation = "Dear " + in.LetterRecipient + ":"
		} else {
			in.LetterSalutation = "To whom it may concern:"
		}
	}
	return nil
}

func dayLines(days []AvailableDay) string {
	if len(days) == 0 {
		return "    (no available dates in this range)\n"
	}
	var b strings.Builder
	for _, d := range days {
		t, _ := parseDate(d.Date)
		b.WriteString("    " + longDate(t))
		switch d.Portion {
		case PortionMorning:
			b.WriteString(" (morning only)")
		case PortionAfternoon:
			b.WriteString(" (afternoon only)")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func signature(u domain.User) string {
	line := u.Name
	if u.BarNumber != "" {
		line += ", Bar No. " + u.BarNumber
	}
	return "______________________________\n" + line + "\n"
}

func blank(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func pleading(in AvailableRequest, days []AvailableDay, attorney domain.User) string {
	var b strings.Builder
	if in.Court != "" {
		b.WriteString(strings.ToUpper(in.Court) + "\n\n")
	}
	if in.Caption != "" && in.LeftName == "" && in.RightName == "" {
		b.WriteString(in.Caption + "\n\n")
	}
	pad := func(s string) string { return fmt.Sprintf("%-40s)", s) }
	b.WriteString(pad(blank(in.LeftName, "____________")+",") + "\n")
	b.WriteString(pad("        "+in.LeftLabel+",") + "\n")
	b.WriteString(pad("") + "\n")
	b.WriteString(pad("v.") + "    Case No. " + blank(in.CaseNumber, "____________") + "\n")
	b.WriteString(pad("") + "\n")
	b.WriteString(pad(blank(in.RightName, "____________")+",") + "\n")
	b.WriteString(pad("        "+in.RightLabel+".") + "\n\n")
	b.WriteString(in.Title + "\n\n")
	side, sideName := in.LeftLabel, in.LeftName
	if in.FirmSide == "right" {
		side, sideName = in.RightLabel, in.RightName
	}
	who := side
	if sideName != "" {
		who = sideName + ", " + side + ","
	}
	b.WriteString(who + " through counsel, states that counsel is available for " + in.Purpose + " on the following dates")
	if in.CoordinateWith != "" {
		b.WriteString(", to be coordinated with " + in.CoordinateWith)
	}
	b.WriteString(":\n\n")
	b.WriteString(dayLines(days))
	b.WriteString("\nRespectfully submitted,\n\n")
	b.WriteString(signature(attorney))
	b.WriteString("Attorney for " + side + "\n\n")
	b.WriteString("CERTIFICATE OF SERVICE\n\n")
	served := "____________"
	if in.ServiceDate != "" {
		t, _ := parseDate(in.ServiceDate)
		served = t.Format("January 2, 2006")
	}
	b.WriteString("I certify that on " + served + ", a copy of the foregoing was served on " + blank(in.CoordinateWith, "all counsel of record") + ".\n\n")
	b.WriteString(signature(attorney))
	return b.String()
}

func letter(in AvailableRequest, days []AvailableDay, attorney domain.User, firm string, today time.Time) string {
	var b strings.Builder
	if firm != "" {
		b.WriteString(firm + "\n\n")
	}
	b.WriteString(today.Format("January 2, 2006") + "\n\n")
	if in.LetterRecipient != "" {
		b.WriteString(in.LetterRecipient + "\n")
	}
	if in.LetterAddress != "" {
		b.WriteString(strings.TrimRight(in.LetterAddress, "\n") + "\n")
	}
	b.WriteString("\nRe: " + blank(in.LetterRe, in.Title) + "\n\n")
	b.WriteString(in.LetterSalutation + "\n\n")
	b.WriteString("I am available for " + in.Purpose + " on the following dates:\n\n")
	b.WriteString(dayLines(days))
	b.WriteString("\nPlease let me know which of these dates works for you.\n\nSincerely,\n\n")
	b.WriteString(signature(attorney))
	return b.String()
}

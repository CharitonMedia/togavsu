package practice

import (
	"context"
	"strings"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Search finds matters, people, and invoices. Files are a known scope with no rows until the files package is mounted.
// A walled matter, its invoice, and a person who appears only on walled matters are omitted.
// Attorney-only values are not searched for a caller who cannot read them.
func (s *Service) Search(ctx context.Context, p domain.Principal, query, scope string) (SearchResult, error) {
	query = strings.TrimSpace(query)
	minChars := s.intSetting(ctx, "search.min_chars", 2)
	if len([]rune(query)) < minChars {
		return SearchResult{}, domain.Invalid("q", "search needs more characters")
	}
	if scope == "" {
		scope = ScopeAll
	}
	switch scope {
	case ScopeAll, ScopeMatters, ScopePeople, ScopeInvoices, ScopeFiles:
	default:
		return SearchResult{}, domain.Invalid("scope", "unknown scope")
	}
	limit := s.intSetting(ctx, "search.hits_scope", 8)
	if scope == ScopeAll {
		limit = s.intSetting(ctx, "search.hits_all", 5)
	}
	// Each scope is allowed by domain.Policy. PRD 8.10 gives clients no search: a client-only
	// principal fails every scope. An accountant can read invoices, so that scope stays.
	result := SearchResult{Query: query, Scope: scope}
	want := func(name string) bool { return scope == ScopeAll || scope == name }
	var denied error
	allowed := 0
	if want(ScopeMatters) {
		if err := s.allowScope(ctx, p, ScopeMatters); err != nil {
			denied = err
		} else {
			allowed++
			result.Groups = append(result.Groups, SearchGroup{Scope: ScopeMatters, Hits: s.searchMatters(ctx, p, query, limit)})
		}
	}
	if want(ScopePeople) {
		if err := s.allowScope(ctx, p, ScopePeople); err != nil {
			denied = err
		} else {
			allowed++
			result.Groups = append(result.Groups, SearchGroup{Scope: ScopePeople, Hits: s.searchPeople(ctx, p, query, limit)})
		}
	}
	if want(ScopeInvoices) {
		if err := s.allowScope(ctx, p, ScopeInvoices); err != nil {
			denied = err
		} else {
			allowed++
			result.Groups = append(result.Groups, SearchGroup{Scope: ScopeInvoices, Hits: s.searchInvoices(ctx, p, query, limit)})
		}
	}
	if want(ScopeFiles) {
		if err := s.allowScope(ctx, p, ScopeFiles); err != nil {
			denied = err
		} else {
			allowed++
			result.Groups = append(result.Groups, SearchGroup{Scope: ScopeFiles, Hits: []SearchHit{}})
		}
	}
	if allowed == 0 {
		if denied == nil {
			return SearchResult{}, domain.Forbidden(domain.ReasonRole)
		}
		return SearchResult{}, denied
	}
	return result, nil
}

func (s *Service) allowScope(ctx context.Context, p domain.Principal, scope string) error {
	ref := domain.ResourceRef{Action: domain.ActionRead}
	switch scope {
	case ScopeMatters:
		ref.Resource = domain.ResMatter
	case ScopePeople:
		ref.Resource = domain.ResContact
	case ScopeInvoices:
		ref.Resource = domain.ResInvoice
	case ScopeFiles:
		ref.Resource = domain.ResFile
	default:
		return domain.Invalid("scope", "unknown scope")
	}
	_, err := s.gate(ctx, p, ref)
	return err
}

func (s *Service) searchMatters(ctx context.Context, p domain.Principal, query string, limit int) []SearchHit {
	q := strings.ToLower(query)
	var rows []domain.Matter
	for _, row := range s.db.Matters() {
		if !s.visibleMatter(ctx, p, row.ID) {
			continue
		}
		hay := strings.ToLower(row.Name + " " + row.Number + " " + row.CaseNumber)
		if !strings.Contains(hay, q) {
			continue
		}
		rows = append(rows, row)
	}
	sortMatterRows(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	hits := make([]SearchHit, 0, len(rows))
	for _, row := range rows {
		hits = append(hits, SearchHit{
			Title: row.Number + " " + row.Name, Subtitle: row.Status, RecordType: "matter",
			RecordID: row.ID, TargetPath: "/practice/matters/" + string(row.ID),
		})
	}
	return hits
}

func (s *Service) searchPeople(ctx context.Context, p domain.Principal, query string, limit int) []SearchHit {
	q := strings.ToLower(query)
	var rows []domain.Contact
	for _, row := range s.db.Contacts() {
		if s.readableContact(ctx, p, row.ID) != nil {
			continue
		}
		hay := strings.ToLower(strings.Join([]string{
			row.FullName, row.GivenName, row.FamilyName, row.PreferredName, row.AlsoKnownAs,
		}, " "))
		for _, email := range s.db.Emails(row.ID) {
			hay += " " + strings.ToLower(email.Email)
		}
		for _, phone := range s.db.Phones(row.ID) {
			hay += " " + strings.ToLower(phone.PhoneE164)
		}
		if !strings.Contains(hay, q) {
			continue
		}
		rows = append(rows, row)
	}
	sortContacts(rows)
	if len(rows) > limit {
		rows = rows[:limit]
	}
	hits := make([]SearchHit, 0, len(rows))
	for _, row := range rows {
		city := ""
		for _, address := range s.db.Addresses(row.ID) {
			if address.Primary || city == "" {
				city = address.City
			}
		}
		subtitle := row.Kind
		if city != "" {
			subtitle += " · " + city
		}
		hits = append(hits, SearchHit{
			Title: row.FullName, Subtitle: subtitle, RecordType: "contact",
			RecordID: row.ID, TargetPath: "/practice/contacts/" + string(row.ID),
		})
	}
	return hits
}

func (s *Service) searchInvoices(ctx context.Context, p domain.Principal, query string, limit int) []SearchHit {
	q := strings.ToLower(query)
	var rows []domain.Invoice
	for _, row := range s.db.Invoices() {
		if !strings.Contains(strings.ToLower(row.Number), q) {
			continue
		}
		if s.hiddenByWall(ctx, p, row.MatterID) {
			continue
		}
		rows = append(rows, row)
	}
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		j := i
		for j > 0 && row.IssuedOn > rows[j-1].IssuedOn {
			rows[j] = rows[j-1]
			j--
		}
		rows[j] = row
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	hits := make([]SearchHit, 0, len(rows))
	for _, row := range rows {
		matterName := ""
		if matter, err := s.db.Matter(ctx, row.MatterID); err == nil {
			matterName = matter.Number + " " + matter.Name
		}
		hits = append(hits, SearchHit{
			Title: row.Number, Subtitle: matterName, RecordType: "invoice",
			RecordID: row.ID, TargetPath: "/practice/matters/" + string(row.MatterID),
		})
	}
	return hits
}

func sortMatterRows(rows []domain.Matter) {
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		j := i
		for j > 0 && matterRowLess(row, rows[j-1]) {
			rows[j] = rows[j-1]
			j--
		}
		rows[j] = row
	}
}

func matterRowLess(a, b domain.Matter) bool {
	if openRank(a.Status) != openRank(b.Status) {
		return openRank(a.Status) < openRank(b.Status)
	}
	if a.OpenedOn != b.OpenedOn {
		return a.OpenedOn > b.OpenedOn
	}
	return a.Number > b.Number
}

func groupActivity(rows []TimelineEntry, now time.Time, zone string) []ActivityGroup {
	loc := time.UTC
	if zone != "" {
		if loaded, err := time.LoadLocation(zone); err == nil {
			loc = loaded
		}
	}
	today := now.In(loc).Format("2006-01-02")
	yesterday := now.In(loc).AddDate(0, 0, -1).Format("2006-01-02")
	weekday := int(now.In(loc).Weekday())
	if weekday == 0 {
		weekday = 7
	}
	weekStart := now.In(loc).AddDate(0, 0, -(weekday - 1)).Format("2006-01-02")
	monthStart := time.Date(now.In(loc).Year(), now.In(loc).Month(), 1, 0, 0, 0, 0, loc).Format("2006-01-02")
	order := []string{"Today", "Yesterday", "Earlier this week", "Earlier this month", "Earlier"}
	buckets := map[string][]TimelineEntry{}
	for _, row := range rows {
		day := row.At.In(loc).Format("2006-01-02")
		label := "Earlier"
		switch {
		case day == today:
			label = "Today"
		case day == yesterday:
			label = "Yesterday"
		case day >= weekStart:
			label = "Earlier this week"
		case day >= monthStart:
			label = "Earlier this month"
		}
		buckets[label] = append(buckets[label], row)
	}
	var out []ActivityGroup
	for _, label := range order {
		if len(buckets[label]) == 0 {
			continue
		}
		out = append(out, ActivityGroup{Label: label, Rows: buckets[label]})
	}
	return out
}

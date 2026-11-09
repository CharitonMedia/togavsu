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
	if p.HasRole(domain.RoleClient) && !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) && !p.HasRole(domain.RoleStaff) {
		return SearchResult{}, domain.Forbidden(domain.ReasonRole)
	}
	if !p.HasRole(domain.RoleAdmin) && !p.HasRole(domain.RoleAttorney) && !p.HasRole(domain.RoleStaff) {
		return SearchResult{}, domain.Forbidden(domain.ReasonRole)
	}
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
	result := SearchResult{Query: query, Scope: scope}
	want := func(name string) bool { return scope == ScopeAll || scope == name }
	if want(ScopeMatters) {
		result.Groups = append(result.Groups, SearchGroup{Scope: ScopeMatters, Hits: s.searchMatters(ctx, p, query, limit)})
	}
	if want(ScopePeople) {
		result.Groups = append(result.Groups, SearchGroup{Scope: ScopePeople, Hits: s.searchPeople(ctx, p, query, limit)})
	}
	if want(ScopeInvoices) {
		result.Groups = append(result.Groups, SearchGroup{Scope: ScopeInvoices, Hits: s.searchInvoices(ctx, p, query, limit)})
	}
	if want(ScopeFiles) {
		result.Groups = append(result.Groups, SearchGroup{Scope: ScopeFiles, Hits: []SearchHit{}})
	}
	return result, nil
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
	canSSN := s.canReadSensitive(ctx, p)
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
		if canSSN {
			hay += " " + strings.ToLower(row.SSNEINCiphertext)
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
		if !row.MatterID.IsZero() && !s.visibleMatter(ctx, p, row.MatterID) {
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

func (s *Service) canReadSensitive(ctx context.Context, p domain.Principal) bool {
	d, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResSensitiveContact, Action: domain.ActionRead})
	return err == nil && d.Allow && !d.Redact
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

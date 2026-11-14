package practice

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// Search finds matters, people, and invoices. Files are a known scope with no rows until the files package is mounted.
// A walled matter, its invoice, and a person who appears only on walled matters are omitted.
// Attorney-only values are not searched for a caller who cannot read them.
func (s *Service) Search(ctx context.Context, p domain.Principal, query, scope string) (SearchResult, error) {
	ctx = withSettingsMemo(ctx)
	query = strings.TrimSpace(query)
	minChars := s.intSetting(ctx, "search.min_chars", domain.SearchMinChars)
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
	limit := s.intSetting(ctx, "search.hits_scope", domain.SearchHitsScope)
	if scope == ScopeAll {
		limit = s.intSetting(ctx, "search.hits_all", domain.SearchHitsAll)
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
	// The text test comes first: it is cheap, and every matching matter is still checked
	// for walls and role before it is a hit. Only matching matters are read whole.
	for _, id := range s.mattersMatching([]byte(q)) {
		if !s.visibleMatter(ctx, p, id) {
			continue
		}
		if row, err := s.db.Matter(ctx, id); err == nil {
			rows = append(rows, row)
		}
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
	// As for matters, the text test comes first and every match is checked for access.
	qb := []byte(q)
	for _, id := range s.contactsMatching(qb) {
		if s.readableContact(ctx, p, id) != nil {
			continue
		}
		if row, err := s.db.Contact(ctx, id); err == nil {
			rows = append(rows, row)
		}
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
	// The number test comes first; every matching invoice is still checked for its
	// matter's wall.
	for _, row := range s.invoicesMatching([]byte(q)) {
		if s.hiddenByWall(ctx, p, row.MatterID) {
			continue
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].IssuedOn > rows[j].IssuedOn })
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

// mattersMatching is the matters whose name, number and case number text holds q
// (lowercase): through the memory store's search index when it has one.
func (s *Service) mattersMatching(q []byte) []domain.ID {
	match := func(hay []byte) bool { return bytes.Contains(hay, q) }
	if m, ok := s.db.(interface {
		MattersMatching(func([]byte) bool) []domain.ID
	}); ok {
		return m.MattersMatching(match)
	}
	var out []domain.ID
	var hay []byte
	for _, row := range s.db.Matters() {
		hay = appendLower(hay[:0], row.Name)
		hay = appendLower(append(hay, ' '), row.Number)
		hay = appendLower(append(hay, ' '), row.CaseNumber)
		if match(hay) {
			out = append(out, row.ID)
		}
	}
	return out
}

// invoicesMatching is the invoices whose number holds q (lowercase).
func (s *Service) invoicesMatching(q []byte) []domain.Invoice {
	match := func(hay []byte) bool { return bytes.Contains(hay, q) }
	if m, ok := s.db.(interface {
		InvoicesMatching(func([]byte) bool) []domain.Invoice
	}); ok {
		return m.InvoicesMatching(match)
	}
	var out []domain.Invoice
	var hay []byte
	for _, row := range s.db.Invoices() {
		if hay = appendLower(hay[:0], row.Number); match(hay) {
			out = append(out, row)
		}
	}
	return out
}

// contactsMatching is the contacts whose name, email and phone text holds q (lowercase):
// in one pass of the memory store when it has one, else contact by contact.
func (s *Service) contactsMatching(q []byte) []domain.ID {
	match := func(hay []byte) bool { return bytes.Contains(hay, q) }
	if m, ok := s.db.(interface {
		ContactsMatching(func([]byte) bool) []domain.ID
	}); ok {
		return m.ContactsMatching(match)
	}
	var out []domain.ID
	var hay []byte
	for _, row := range s.db.Contacts() {
		hay = appendLower(hay[:0], row.FullName)
		for _, f := range []string{row.GivenName, row.FamilyName, row.PreferredName, row.AlsoKnownAs} {
			hay = appendLower(append(hay, ' '), f)
		}
		for _, e := range s.db.Emails(row.ID) {
			hay = appendLower(append(hay, ' '), e.Email)
		}
		for _, ph := range s.db.Phones(row.ID) {
			hay = appendLower(append(hay, ' '), ph.PhoneE164)
		}
		if match(hay) {
			out = append(out, row.ID)
		}
	}
	return out
}

// appendLower appends strings.ToLower(v): ASCII byte by byte, anything else through
// strings.ToLower.
func appendLower(b []byte, v string) []byte {
	for i := 0; i < len(v); i++ {
		if v[i] >= utf8.RuneSelf {
			return append(b, strings.ToLower(v)...)
		}
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		b = append(b, c)
	}
	return b
}

// sortMatterRows is a stable sort, like the quadratic insertion sort it replaced.
func sortMatterRows(rows []domain.Matter) {
	sort.SliceStable(rows, func(i, j int) bool { return matterRowLess(rows[i], rows[j]) })
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

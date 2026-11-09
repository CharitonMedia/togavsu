package practice

import (
	"context"
	"strings"
	"unicode"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// CreateConflict stores a conflict name. The reason is attorney-only.
func (s *Service) CreateConflict(ctx context.Context, p domain.Principal, in ConflictInput) (domain.ConflictRecord, error) {
	if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResMatter, Action: domain.ActionWrite, MatterID: in.MatterID, SharedWithClient: true}); err != nil {
		return domain.ConflictRecord{}, err
	}
	if !in.MatterID.IsZero() {
		if _, err := s.db.Matter(ctx, in.MatterID); err != nil {
			return domain.ConflictRecord{}, missing(err, "matter")
		}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.ConflictRecord{}, domain.Invalid("name", "name is required")
	}
	switch in.Relationship {
	case "prior client", "opposing party", "other", "":
		if in.Relationship == "" {
			in.Relationship = "other"
		}
	default:
		return domain.ConflictRecord{}, domain.Invalid("relationship", "unknown relationship")
	}
	switch in.Scope {
	case "client", "matter", "":
		if in.Scope == "" {
			in.Scope = "client"
		}
	default:
		return domain.ConflictRecord{}, domain.Invalid("scope", "unknown scope")
	}
	if strings.TrimSpace(in.Reason) != "" {
		if _, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResConflictReason, Action: domain.ActionWrite, MatterID: in.MatterID}); err != nil {
			return domain.ConflictRecord{}, err
		}
	}
	if !validDate(in.DateOfBirth) || !validDate(in.StartsOn) || !validDate(in.EndsOn) {
		return domain.ConflictRecord{}, domain.Invalid("date", "use YYYY-MM-DD")
	}
	meta, err := s.meta(p)
	if err != nil {
		return domain.ConflictRecord{}, err
	}
	row := domain.ConflictRecord{
		Meta: meta, Name: name, Aliases: in.Aliases, DateOfBirth: in.DateOfBirth, Relationship: in.Relationship,
		Scope: in.Scope, Reason: strings.TrimSpace(in.Reason), MatterID: in.MatterID, StartsOn: in.StartsOn,
		EndsOn: in.EndsOn, Source: in.Source,
	}
	if err := s.db.InsertConflict(ctx, row); err != nil {
		return domain.ConflictRecord{}, err
	}
	return s.redactConflict(ctx, p, row), nil
}

// RunConflictCheck scores contacts, parties, and conflict records in the application.
func (s *Service) RunConflictCheck(ctx context.Context, p domain.Principal, matter domain.ID) (ConflictRun, error) {
	if err := s.gateMatterShared(ctx, p, matter, domain.ActionRead, false); err != nil {
		return ConflictRun{}, err
	}
	parties := s.db.Parties(matter, domain.ID(""))
	var probes []nameProbe
	for _, party := range parties {
		contact, err := s.db.Contact(ctx, party.ContactID)
		if err != nil {
			continue
		}
		probes = append(probes, namesOf(contact.FullName, contact.AlsoKnownAs, contact.PreferredName, contact.DateOfBirth)...)
	}
	floor := s.intSetting(ctx, "practice.conflict_score_minimum", 60)
	best := map[string]Hit{}
	consider := func(kind string, id domain.ID, name, dob, reason string) {
		top := 0
		for _, probe := range probes {
			if score := ScoreName(probe.name, name, probe.dob, dob); score > top {
				top = score
			}
		}
		if top < floor || top == 0 {
			return
		}
		key := kind + ":" + string(id)
		if prev, ok := best[key]; ok && prev.Score >= top {
			return
		}
		best[key] = Hit{MatchedRecordType: kind, MatchedID: id, Score: top, Name: name, Reason: reason}
	}
	for _, contact := range s.db.Contacts() {
		if s.onMatter(matter, contact.ID) {
			continue
		}
		if s.readableContact(ctx, p, contact.ID) != nil {
			continue
		}
		consider("contact", contact.ID, contact.FullName, contact.DateOfBirth, "")
		for _, alias := range splitAliases(contact.AlsoKnownAs) {
			consider("contact", contact.ID, alias, contact.DateOfBirth, "")
		}
	}
	for _, party := range s.db.Parties(domain.ID(""), domain.ID("")) {
		if party.MatterID == matter || !s.visibleMatter(ctx, p, party.MatterID) {
			continue
		}
		contact, err := s.db.Contact(ctx, party.ContactID)
		if err != nil {
			continue
		}
		consider("party", party.ID, contact.FullName, contact.DateOfBirth, "")
	}
	for _, record := range s.db.ConflictRecords() {
		if !record.MatterID.IsZero() && !s.visibleMatter(ctx, p, record.MatterID) {
			continue
		}
		consider("conflict_record", record.ID, record.Name, record.DateOfBirth, record.Reason)
		for _, alias := range splitAliases(record.Aliases) {
			consider("conflict_record", record.ID, alias, record.DateOfBirth, record.Reason)
		}
	}
	meta, err := s.meta(p)
	if err != nil {
		return ConflictRun{}, err
	}
	run := ConflictRun{ID: meta.ID, MatterID: matter, CreatedAt: meta.CreatedAt, CreatedBy: actorID(p)}
	for _, hit := range best {
		id, err := s.id()
		if err != nil {
			return ConflictRun{}, err
		}
		hit.ID = id
		run.Hits = append(run.Hits, hit)
	}
	sortHits(run.Hits)
	if err := s.db.AppendRun(ctx, run); err != nil {
		return ConflictRun{}, err
	}
	now := s.clock()
	for _, party := range parties {
		if err := s.db.MarkChecked(ctx, party.ID, now); err != nil {
			return ConflictRun{}, err
		}
	}
	if err := s.record(ctx, p, []change{{
		MatterID: matter, Entity: "conflict_check", EntityID: run.ID, Field: "hits",
		New: strconvHits(len(run.Hits)), Kind: "create", Action: "conflict.checked",
		Summary: "Ran a conflict check",
	}}); err != nil {
		return ConflictRun{}, err
	}
	return s.redactRun(ctx, p, run), nil
}

// DecideConflict records an attorney decision. The reason is attorney-only.
func (s *Service) DecideConflict(ctx context.Context, p domain.Principal, hitID domain.ID, decision, reason string) (Hit, error) {
	switch decision {
	case DecisionClear, DecisionEscalate, DecisionNoGo:
	default:
		return Hit{}, domain.Invalid("decision", "decision must be clear, escalate, or no go")
	}
	run, hit, ok := s.db.FindHit(hitID)
	if !ok {
		return Hit{}, domain.NotFound("conflict hit")
	}
	if _, err := s.gate(ctx, p, domain.ResourceRef{
		Resource: domain.ResConflictReason, Action: domain.ActionConflictDecide, MatterID: run.MatterID,
	}); err != nil {
		return Hit{}, err
	}
	meta, err := s.meta(p)
	if err != nil {
		return Hit{}, err
	}
	review := domain.ConflictReview{
		Meta: meta, MatterID: run.MatterID, ConflictRecordID: hit.MatchedID, Confidence: hit.Score,
		Decision: decision, Reason: strings.TrimSpace(reason), DecidedBy: actorID(p), DecidedAt: meta.CreatedAt.UnixMicro(),
	}
	if hit.MatchedRecordType != "conflict_record" {
		review.ConflictRecordID = domain.ID("")
	}
	if err := s.db.InsertReview(ctx, review); err != nil {
		return Hit{}, err
	}
	prevReason := hit.Reason
	hit.Decision = decision
	hit.Reason = review.Reason
	hit.ReviewID = review.ID
	if err := s.db.SaveHit(ctx, hit); err != nil {
		return Hit{}, err
	}
	if err := s.record(ctx, p, []change{{
		MatterID: run.MatterID, Entity: "matter", EntityID: run.MatterID, Field: "conflict_reason",
		Old: prevReason, New: review.Reason, Reason: decision, Kind: "update", AttorneyOnly: true,
		Action: "conflict.decided", Summary: "Recorded a conflict decision",
	}}); err != nil {
		return Hit{}, err
	}
	return s.redactHit(ctx, p, hit), nil
}

// ConflictChecks lists checks the caller can see, with attorney-only reasons redacted.
func (s *Service) ConflictChecks(ctx context.Context, p domain.Principal, matter domain.ID) ([]ConflictRun, error) {
	if err := s.gateMatterShared(ctx, p, matter, domain.ActionRead, false); err != nil {
		return nil, err
	}
	runs := s.db.Runs(matter)
	for i := range runs {
		runs[i] = s.redactRun(ctx, p, runs[i])
	}
	return runs, nil
}

func (s *Service) redactRun(ctx context.Context, p domain.Principal, run ConflictRun) ConflictRun {
	for i := range run.Hits {
		run.Hits[i] = s.redactHit(ctx, p, run.Hits[i])
	}
	return run
}

func (s *Service) redactHit(ctx context.Context, p domain.Principal, hit Hit) Hit {
	d, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResConflictReason, Action: domain.ActionRead})
	if err != nil || d.Redact || !d.Allow {
		if hit.Reason != "" {
			hit.Reason = Redacted
		}
	}
	return hit
}

func (s *Service) redactConflict(ctx context.Context, p domain.Principal, row domain.ConflictRecord) domain.ConflictRecord {
	d, err := s.gate(ctx, p, domain.ResourceRef{Resource: domain.ResConflictReason, Action: domain.ActionRead, MatterID: row.MatterID})
	if err != nil || d.Redact || !d.Allow {
		if row.Reason != "" {
			row.Reason = Redacted
		}
	}
	return row
}

func (s *Service) onMatter(matter, contact domain.ID) bool {
	return len(s.db.Parties(matter, contact)) > 0
}

type nameProbe struct {
	name string
	dob  string
}

func namesOf(full, aka, preferred, dob string) []nameProbe {
	var out []nameProbe
	for _, name := range append([]string{full, preferred}, splitAliases(aka)...) {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out = append(out, nameProbe{name, dob})
	}
	return out
}

func splitAliases(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	})
}

func sortHits(rows []Hit) {
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		j := i
		for j > 0 && row.Score > rows[j-1].Score {
			rows[j] = rows[j-1]
			j--
		}
		rows[j] = row
	}
}

func strconvHits(n int) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(
		strings.Repeat("x", 0)+itoa(n), " ", ""), "\n", ""))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// ScoreName scores two names from 0 to 100 using normalized text, trigrams, and soundex.
func ScoreName(query, candidate, queryDOB, candidateDOB string) int {
	q := normName(query)
	c := normName(candidate)
	if q == "" || c == "" {
		return 0
	}
	score := 0
	if q == c {
		score = 100
	} else {
		score = int(dice(letters(q), letters(c)) * 100)
		if containsName(q, c) && score < 90 {
			score = 90
		}
		if soundex(firstWord(q)) != "" && soundex(firstWord(q)) == soundex(firstWord(c)) && score < 75 {
			score = 75
		}
	}
	if queryDOB != "" && candidateDOB != "" {
		if queryDOB == candidateDOB && score > 0 && score < 100 {
			score += 10
		}
		if queryDOB != candidateDOB && score < 100 {
			score -= 20
		}
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func normName(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space && b.Len() > 0 {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

func letters(s string) string {
	return strings.ReplaceAll(s, " ", "")
}

func firstWord(s string) string {
	word, _, _ := strings.Cut(s, " ")
	return word
}

func containsName(a, b string) bool {
	if len(a) < 3 || len(b) < 3 {
		return false
	}
	return strings.Contains(a, b) || strings.Contains(b, a)
}

func dice(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	ta, tb := grams(a), grams(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	inter := 0
	left, right := 0, 0
	for g, n := range ta {
		left += n
		if m := tb[g]; m < n {
			inter += m
		} else {
			inter += n
		}
	}
	for _, n := range tb {
		right += n
	}
	if left+right == 0 {
		return 0
	}
	return 2 * float64(inter) / float64(left+right)
}

func grams(s string) map[string]int {
	out := map[string]int{}
	r := []rune(s)
	if len(r) < 3 {
		out[s] = 1
		return out
	}
	for i := 0; i <= len(r)-3; i++ {
		out[string(r[i:i+3])]++
	}
	return out
}

func soundex(s string) string {
	s = strings.ToUpper(letters(s))
	if s == "" {
		return ""
	}
	first := s[0]
	var codes []byte
	prev := soundexCode(first)
	for i := 1; i < len(s); i++ {
		c := soundexCode(s[i])
		if c == '0' {
			prev = '0'
			continue
		}
		if c == prev {
			continue
		}
		codes = append(codes, c)
		prev = c
		if len(codes) == 3 {
			break
		}
	}
	for len(codes) < 3 {
		codes = append(codes, '0')
	}
	return string([]byte{first, codes[0], codes[1], codes[2]})
}

func soundexCode(b byte) byte {
	switch b {
	case 'B', 'F', 'P', 'V':
		return '1'
	case 'C', 'G', 'J', 'K', 'Q', 'S', 'X', 'Z':
		return '2'
	case 'D', 'T':
		return '3'
	case 'L':
		return '4'
	case 'M', 'N':
		return '5'
	case 'R':
		return '6'
	default:
		return '0'
	}
}

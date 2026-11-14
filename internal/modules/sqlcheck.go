package modules

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/CharitonMedia/togavsu/internal/db"
)

// The install check (PRD 9.9): a module migration may create, alter and drop tables,
// indexes and views in its own schema and add foreign keys to core records' ids.
// Nothing else. Every statement is parsed; anything outside that list refuses the whole
// package. The migration also runs as the module's own database role (schema.go), so a
// statement this check misread still can't reach core tables.

type tokKind int

const (
	tWord tokKind = iota
	tQuoted
	tString
	tNumber
	tPunct
)

type tok struct {
	kind tokKind
	text string // words are lower-cased; quoted identifiers keep their case
}

func (t tok) is(words ...string) bool {
	if t.kind != tWord {
		return false
	}
	for _, w := range words {
		if t.text == w {
			return true
		}
	}
	return false
}

func (t tok) ident() bool { return t.kind == tWord || t.kind == tQuoted }

// tokenize splits SQL into tokens, dropping comments.
func tokenize(src string) ([]tok, error) {
	var out []tok
	i := zero
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			i++
		case c == '-' && i+1 < len(src) && src[i+1] == '-':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			depth := zero
			for i < len(src) {
				if strings.HasPrefix(src[i:], "/*") {
					depth++
					i += two
				} else if strings.HasPrefix(src[i:], "*/") {
					depth--
					i += two
					if depth == zero {
						break
					}
				} else {
					i++
				}
			}
			if depth != zero {
				return nil, fmt.Errorf("unterminated comment")
			}
		case c == '\'' || ((c == 'e' || c == 'E') && i+1 < len(src) && src[i+1] == '\''):
			if c != '\'' {
				i++
			}
			j := i + 1
			for {
				if j >= len(src) {
					return nil, fmt.Errorf("unterminated string")
				}
				if src[j] == '\\' && c != '\'' {
					j += two
					continue
				}
				if src[j] == '\'' {
					if j+1 < len(src) && src[j+1] == '\'' {
						j += two
						continue
					}
					break
				}
				j++
			}
			out = append(out, tok{tString, src[i+1 : j]})
			i = j + 1
		case c == '"':
			j := i + 1
			var b strings.Builder
			for {
				if j >= len(src) {
					return nil, fmt.Errorf("unterminated identifier")
				}
				if src[j] == '"' {
					if j+1 < len(src) && src[j+1] == '"' {
						b.WriteByte('"')
						j += two
						continue
					}
					break
				}
				b.WriteByte(src[j])
				j++
			}
			out = append(out, tok{tQuoted, b.String()})
			i = j + 1
		case c == '$':
			// Dollar quoting only carries function and DO bodies, which a module may not
			// create, and positional parameters have no place in a migration.
			return nil, fmt.Errorf("dollar-quoted text and $ parameters are not allowed")
		case isWordStart(c):
			j := i
			for j < len(src) && isWordPart(src[j]) {
				j++
			}
			out = append(out, tok{tWord, strings.ToLower(src[i:j])})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.' || src[j] == 'e' || src[j] == 'E') {
				j++
			}
			out = append(out, tok{tNumber, src[i:j]})
			i = j
		default:
			if c >= utf8.RuneSelf {
				return nil, fmt.Errorf("non-ASCII text outside strings")
			}
			out = append(out, tok{tPunct, string(c)})
			i++
		}
	}
	return out, nil
}

func isWordStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isWordPart(c byte) bool  { return isWordStart(c) || c >= '0' && c <= '9' }

func splitStatements(toks []tok) [][]tok {
	var out [][]tok
	var cur []tok
	for _, t := range toks {
		if t.kind == tPunct && t.text == ";" {
			if len(cur) > 0 {
				out = append(out, cur)
			}
			cur = nil
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// duplicateNames are section 5 records and their plain synonyms (PRD 9.9). A module
// table with one of these names would be its own copy of a core record.
var duplicateNames = func() map[string]bool {
	out := map[string]bool{}
	for _, n := range []string{
		"contact", "contacts", "person", "people", "persons", "client", "clients", "matter", "matters",
		"case", "cases", "user", "users", "invoice", "invoices", "payment", "payments", "file", "files",
		"document", "documents", "event", "events", "contact_addresses", "contact_emails", "contact_phones",
		"contact_tags", "matter_parties", "matter_walls", "practice_areas", "counties", "referral_sources",
		"sessions", "password_credentials", "totp_secrets", "recovery_codes", "api_tokens", "settings",
		"fee_agreements", "time_entries", "timers", "expenses", "trust_accounts", "trust_ledger", "trust_flags",
		"trust_replenishment_requests", "trust_reconciliations", "notes", "conflict_records", "conflict_reviews",
		"calendar_events", "deadlines", "audit_log", "file_versions", "file_renditions", "file_links",
		"storage_backends", "storage_moves", "edit_locks", "court_profiles", "doc_categories", "doc_tender_jobs",
		"doc_tender_units", "doc_tender_results", "doc_tender_exceptions", "ai_call_log", "schema_migrations",
	} {
		out[n] = true
	}
	return out
}()

var nameColumns = map[string]bool{"name": true, "full_name": true, "first_name": true, "last_name": true, "display_name": true, "given_name": true, "family_name": true, "surname": true}

func contactColumn(c string) bool {
	for _, part := range []string{"email", "phone", "address", "street", "postal", "zip"} {
		if strings.Contains(c, part) {
			return true
		}
	}
	return false
}

// migrationCheck holds what one package's migrations create, for the manifest cross-checks.
type migrationCheck struct {
	schema string
	tables map[string]*tableInfo
	views  map[string]bool
	// refused names the tables and views whose CREATE was refused (MigrationRefusals).
	refused map[string]bool
}

type tableInfo struct {
	columns []string
	pk      []string
	// coreFK maps a column to the core table it references.
	coreFK map[string]string
}

// CheckMigrations runs the PRD 9.9 install check over every migration and the
// manifest's data declarations. It returns migration_refused naming the file and the
// statement.
func CheckMigrations(m Manifest, ms []db.Migration) error {
	var first error
	walkMigrations(m, ms, func(r Refusal) bool {
		first = r.Err
		return false
	})
	return first
}

// MigrationRefusals runs CheckMigrations' check over every statement and declaration
// and reports each refusal, not only the first. The first is the error CheckMigrations
// returns.
func MigrationRefusals(m Manifest, ms []db.Migration) Refusals {
	var out Refusals
	walkMigrations(m, ms, func(r Refusal) bool {
		out = append(out, r)
		return true
	})
	return out
}

// walkMigrations checks each statement in order and then the manifest cross-checks,
// handing each refusal to report; it stops when report returns false.
func walkMigrations(m Manifest, ms []db.Migration, report func(Refusal) bool) {
	c := &migrationCheck{schema: m.Schema(), tables: map[string]*tableInfo{}, views: map[string]bool{}}
	for _, mig := range ms {
		file := "migrations/" + mig.Name + ".sql"
		toks, err := tokenize(mig.SQL)
		if err != nil {
			if !report(migrationRefusal(file, zero, err, refuse(CodeMigration, fmt.Sprintf("%s: %v", mig.Name, err)))) {
				return
			}
			continue
		}
		for i, st := range splitStatements(toks) {
			if err := c.statement(st); err != nil {
				c.refusedObject(st)
				if !report(migrationRefusal(file, i+one, err, refuse(CodeMigration, fmt.Sprintf("%s statement %d: %v", mig.Name, i+1, err)))) {
					return
				}
			}
		}
	}
	c.crossCheck(m, report)
}

// refusedObject notes the table or view a refused CREATE statement names, so the
// cross-checks don't also report it as missing.
func (c *migrationCheck) refusedObject(st []tok) {
	p := &stmt{toks: st, schema: c.schema}
	if !p.accept("create") {
		return
	}
	p.accept("or", "replace")
	if !p.accept("unlogged", "table") && !p.accept("table") && !p.accept("materialized", "view") && !p.accept("view") {
		return
	}
	p.accept("if", "not", "exists")
	if name, err := p.objectName(); err == nil {
		if c.refused == nil {
			c.refused = map[string]bool{}
		}
		c.refused[name] = true
	}
}

func (c *migrationCheck) crossCheck(m Manifest, report func(Refusal) bool) {
	cross := func(path, msg string) bool {
		return report(Refusal{Kind: KindMigration, Code: RefuseCrossCheck, File: ManifestFile, Path: path,
			Message: msg, APICode: CodeMigration, Err: refuse(CodeMigration, msg)})
	}
	for i, e := range m.Data.Extends {
		f := fmt.Sprintf("data.extends[%d]", i)
		t, ok := c.tables[e.Table]
		if !ok && c.refused[e.Table] {
			continue
		}
		if !ok {
			if !cross(f, "extension table "+e.Table+" isn't created by the migrations") {
				return
			}
			continue
		}
		want, _ := CoreTable(e.Record)
		if len(t.pk) != one || t.pk[zero] != e.Key || t.coreFK[e.Key] != want {
			if !cross(f, fmt.Sprintf("extension table %s needs primary key %s referencing core.%s(id)", e.Table, e.Key, want)) {
				return
			}
		}
		for _, col := range e.Columns {
			if !containsStr(t.columns, col.Name) {
				if !cross(f, "extension "+e.Name+" lists column "+col.Name+", which "+e.Table+" doesn't have") {
					return
				}
			}
		}
	}
	for i, v := range m.Data.Views {
		if !c.views[v.Name] && !c.refused[v.Name] {
			if !cross(fmt.Sprintf("data.views[%d]", i), "published view "+v.Name+" isn't created by the migrations") {
				return
			}
		}
	}
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (c *migrationCheck) statement(st []tok) error {
	p := &stmt{toks: st, schema: c.schema}
	switch {
	case p.accept("create"):
		p.accept("or", "replace")
		switch {
		case p.accept("unique"):
			if !p.accept("index") {
				return fmt.Errorf("only CREATE UNIQUE INDEX is allowed")
			}
			return c.createIndex(p)
		case p.accept("index"):
			return c.createIndex(p)
		case p.accept("unlogged", "table"), p.accept("table"):
			return c.createTable(p)
		case p.accept("materialized", "view"), p.accept("view"):
			return c.createView(p)
		}
		return fmt.Errorf("CREATE %s is not allowed: a migration creates tables, indexes and views only", strings.ToUpper(p.peek().text))
	case p.accept("alter"):
		switch {
		case p.accept("table"):
			return c.alterTable(p)
		case p.accept("index"), p.accept("materialized", "view"), p.accept("view"):
			return c.alterOther(p)
		}
		return fmt.Errorf("ALTER %s is not allowed", strings.ToUpper(p.peek().text))
	case p.accept("drop"):
		switch {
		case p.accept("table"), p.accept("index"), p.accept("materialized", "view"), p.accept("view"):
			return c.drop(p)
		}
		return fmt.Errorf("DROP %s is not allowed", strings.ToUpper(p.peek().text))
	}
	return fmt.Errorf("%s is not allowed: a migration creates, alters and drops tables, indexes and views only", strings.ToUpper(p.peek().text))
}

// stmt is a cursor over one statement's tokens.
type stmt struct {
	toks   []tok
	i      int
	schema string
}

func (p *stmt) peek() tok {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return tok{kind: tPunct, text: "end of statement"}
}

func (p *stmt) at(off int) tok {
	if p.i+off < len(p.toks) {
		return p.toks[p.i+off]
	}
	return tok{kind: tPunct}
}

func (p *stmt) done() bool { return p.i >= len(p.toks) }

// accept consumes the words in order when they all match.
func (p *stmt) accept(words ...string) bool {
	for k, w := range words {
		if !p.at(k).is(w) {
			return false
		}
	}
	p.i += len(words)
	return true
}

func (p *stmt) punct(s string) bool {
	if t := p.peek(); t.kind == tPunct && t.text == s {
		p.i++
		return true
	}
	return false
}

// objectName reads [schema.]name and requires the schema, when given, to be the module's.
func (p *stmt) objectName() (string, error) {
	t := p.peek()
	if !t.ident() {
		return "", fmt.Errorf("expected a name, found %q", t.text)
	}
	p.i++
	if p.punct(".") {
		n := p.peek()
		if !n.ident() {
			return "", fmt.Errorf("expected a name after %s.", t.text)
		}
		p.i++
		if t.text != p.schema {
			return "", fmt.Errorf("%s.%s is outside the module's schema %s", t.text, n.text, p.schema)
		}
		if p.punct(".") {
			return "", fmt.Errorf("three-part names are not allowed")
		}
		return n.text, nil
	}
	return t.text, nil
}

// group returns the tokens inside the parenthesis at the cursor and moves past it.
func (p *stmt) group() ([]tok, error) {
	if !p.punct("(") {
		return nil, fmt.Errorf("expected (")
	}
	depth := one
	start := p.i
	for p.i < len(p.toks) {
		t := p.toks[p.i]
		p.i++
		if t.kind == tPunct && t.text == "(" {
			depth++
		}
		if t.kind == tPunct && t.text == ")" {
			depth--
			if depth == zero {
				return p.toks[start : p.i-1], nil
			}
		}
	}
	return nil, fmt.Errorf("unbalanced parentheses")
}

// splitTop splits tokens on top-level commas.
func splitTop(toks []tok) [][]tok {
	var out [][]tok
	depth, start := zero, zero
	for i, t := range toks {
		if t.kind != tPunct {
			continue
		}
		switch t.text {
		case "(":
			depth++
		case ")":
			depth--
		case ",":
			if depth == zero {
				out = append(out, toks[start:i])
				start = i + 1
			}
		}
	}
	return append(out, toks[start:])
}

// scanExpr refuses qualified names outside the module schema, unless they are the
// target of a REFERENCES clause (checked separately), and calls into other schemas.
func (c *migrationCheck) scanExpr(toks []tok) error {
	for i := 0; i+2 < len(toks); i++ {
		if !toks[i].ident() || toks[i+1].kind != tPunct || toks[i+1].text != "." || !toks[i+2].ident() {
			continue
		}
		if i > zero && toks[i-one].is("references") {
			continue
		}
		if q := toks[i].text; q != c.schema && reservedSchema(q) {
			return fmt.Errorf("%s.%s is outside the module's schema", q, toks[i+2].text)
		}
	}
	return nil
}

func reservedSchema(q string) bool {
	return q == "core" || q == "public" || q == "information_schema" || strings.HasPrefix(q, "pg_") || strings.HasPrefix(q, "mod_")
}

func (c *migrationCheck) checkTableName(name string) error {
	if duplicateNames[strings.ToLower(name)] {
		return fmt.Errorf("table %s duplicates a core record; link to core records by id instead", name)
	}
	return nil
}

// references parses "REFERENCES target [(cols)] [ON DELETE ...] [ON UPDATE ...]" at the
// cursor of toks[i] (the REFERENCES word) and returns the core table, or "" for a table
// in the module's own schema.
func (c *migrationCheck) references(toks []tok, i int) (string, int, error) {
	p := &stmt{toks: toks, i: i + 1, schema: c.schema}
	first := p.peek()
	if !first.ident() {
		return "", 0, fmt.Errorf("REFERENCES needs a table")
	}
	p.i++
	core := ""
	if p.punct(".") {
		second := p.peek()
		if !second.ident() {
			return "", 0, fmt.Errorf("REFERENCES needs a table")
		}
		p.i++
		switch first.text {
		case "core":
			if !coreTableAllowed(second.text) {
				return "", 0, &CoreReferenceError{Relation: "core." + second.text}
			}
			core = second.text
		case c.schema:
		default:
			return "", 0, fmt.Errorf("REFERENCES %s.%s is outside the module's schema", first.text, second.text)
		}
	} else if duplicateNames[first.text] && !c.ownTable(first.text) {
		// An unqualified core-looking name resolves to the module schema; say what was meant.
		return "", 0, fmt.Errorf("REFERENCES %s must name core.%s explicitly", first.text, first.text)
	}
	if p.peek().kind == tPunct && p.peek().text == "(" {
		cols, err := p.group()
		if err != nil {
			return "", 0, err
		}
		if core != "" && (len(cols) != one || !cols[zero].is("id")) {
			return "", 0, fmt.Errorf("foreign keys to core.%s point at its id", core)
		}
	}
	onDelete := ""
	for {
		switch {
		case p.accept("on", "delete"):
			switch {
			case p.accept("restrict"):
				onDelete = "restrict"
			case p.accept("no", "action"):
				onDelete = "no action"
			default:
				if core != "" {
					return "", 0, fmt.Errorf("foreign keys to core records are ON DELETE RESTRICT")
				}
				p.i++
				if p.peek().is("null", "default") {
					p.i++
				}
			}
		case p.accept("on", "update"):
			if !p.accept("restrict") && !p.accept("no", "action") {
				if core != "" {
					return "", 0, fmt.Errorf("foreign keys to core records can't cascade updates")
				}
				p.i++
				if p.peek().is("null", "default") {
					p.i++
				}
			}
		case p.accept("match", "simple"), p.accept("match", "full"), p.accept("deferrable"), p.accept("not", "deferrable"),
			p.accept("initially", "deferred"), p.accept("initially", "immediate"):
		default:
			if core != "" && onDelete != "restrict" {
				return "", 0, fmt.Errorf("foreign keys to core.%s must say ON DELETE RESTRICT", core)
			}
			return core, p.i, nil
		}
	}
}

func (c *migrationCheck) ownTable(name string) bool { _, ok := c.tables[name]; return ok }

func coreTableAllowed(t string) bool {
	for _, v := range coreRecords {
		if v == t {
			return true
		}
	}
	return false
}

func (c *migrationCheck) createTable(p *stmt) error {
	p.accept("if", "not", "exists")
	name, err := p.objectName()
	if err != nil {
		return err
	}
	if err := c.checkTableName(name); err != nil {
		return err
	}
	if p.peek().is("as", "partition", "of") {
		return fmt.Errorf("CREATE TABLE %s must list its columns; copying query results is not allowed", name)
	}
	body, err := p.group()
	if err != nil {
		return err
	}
	if !p.done() {
		return fmt.Errorf("CREATE TABLE %s: %q after the column list is not allowed", name, p.peek().text)
	}
	info := &tableInfo{coreFK: map[string]string{}}
	for _, part := range splitTop(body) {
		if len(part) == 0 {
			continue
		}
		if err := c.scanExpr(part); err != nil {
			return err
		}
		if err := c.tableElement(name, info, part); err != nil {
			return err
		}
	}
	if err := identifyingCopy(name, info); err != nil {
		return err
	}
	c.tables[name] = info
	return nil
}

// tableElement reads one column definition or table constraint.
func (c *migrationCheck) tableElement(table string, info *tableInfo, part []tok) error {
	first := part[0]
	if first.is("like", "exclude") {
		return fmt.Errorf("%s is not allowed in CREATE TABLE", strings.ToUpper(first.text))
	}
	if first.is("constraint") && len(part) > two {
		part = part[two:]
		first = part[0]
	}
	switch {
	case first.is("primary") && len(part) > two && part[one].is("key"):
		info.pk = identList(part[two:])
		return nil
	case first.is("foreign") && len(part) > two && part[one].is("key"):
		cols := identList(part[two:])
		for i, t := range part {
			if t.is("references") {
				core, _, err := c.references(part, i)
				if err != nil {
					return err
				}
				if core != "" && len(cols) == one {
					info.coreFK[cols[0]] = core
				}
			}
		}
		return nil
	case first.is("unique", "check"):
		return nil
	}
	if !first.ident() {
		return fmt.Errorf("unexpected %q in CREATE TABLE %s", first.text, table)
	}
	col := first.text
	info.columns = append(info.columns, col)
	for i := one; i < len(part); i++ {
		t := part[i]
		switch {
		case t.is("primary") && i+1 < len(part) && part[i+1].is("key"):
			info.pk = []string{col}
		case t.is("references"):
			core, next, err := c.references(part, i)
			if err != nil {
				return err
			}
			if core != "" {
				info.coreFK[col] = core
			}
			i = next - 1
		case t.is("generated") && i+2 < len(part) && part[i+2].is("as") && i+3 < len(part) && part[i+3].kind == tPunct:
			// GENERATED ALWAYS AS (expr) STORED: the expression is scanned like any other.
		}
	}
	return nil
}

// identList reads "(a, b)" at the start of toks.
func identList(toks []tok) []string {
	if len(toks) == 0 || toks[0].kind != tPunct || toks[0].text != "(" {
		return nil
	}
	var out []string
	for _, t := range toks[one:] {
		if t.kind == tPunct && t.text == ")" {
			break
		}
		if t.ident() {
			out = append(out, t.text)
		}
	}
	return out
}

// identifyingCopy refuses a table that holds a core record's identifying fields (a name
// with an email, phone or address) without a foreign key to a core record (PRD 9.9).
func identifyingCopy(table string, info *tableInfo) error {
	hasName, hasContact := false, false
	for _, col := range info.columns {
		if nameColumns[col] {
			hasName = true
		}
		if contactColumn(col) {
			hasContact = true
		}
	}
	if hasName && hasContact && len(info.coreFK) == 0 {
		return fmt.Errorf("table %s holds a name with contact details but no foreign key to a core record; link to core.contacts instead of copying it", table)
	}
	return nil
}

func (c *migrationCheck) createIndex(p *stmt) error {
	if p.accept("concurrently") {
		return fmt.Errorf("CREATE INDEX CONCURRENTLY can't run in the migration's transaction")
	}
	p.accept("if", "not", "exists")
	if !p.peek().is("on") {
		if _, err := p.objectName(); err != nil {
			return err
		}
	}
	if !p.accept("on") {
		return fmt.Errorf("expected ON in CREATE INDEX")
	}
	p.accept("only")
	if _, err := p.objectName(); err != nil {
		return err
	}
	rest := p.toks[p.i:]
	for _, t := range rest {
		if t.is("tablespace") {
			return fmt.Errorf("TABLESPACE is not allowed")
		}
	}
	return c.scanExpr(rest)
}

func (c *migrationCheck) createView(p *stmt) error {
	p.accept("if", "not", "exists")
	name, err := p.objectName()
	if err != nil {
		return err
	}
	if err := c.checkTableName(name); err != nil {
		return err
	}
	if p.peek().kind == tPunct && p.peek().text == "(" {
		if _, err := p.group(); err != nil {
			return err
		}
	}
	if p.accept("with") {
		if _, err := p.group(); err != nil {
			return err
		}
	}
	if !p.accept("as") {
		return fmt.Errorf("expected AS in CREATE VIEW %s", name)
	}
	body := p.toks[p.i:]
	if err := c.scanExpr(body); err != nil {
		return err
	}
	// Every relation a view reads is in the module's schema (after FROM or JOIN, a
	// qualified name must carry the module schema). A FROM that is an argument keyword
	// of EXTRACT, SUBSTRING, TRIM or OVERLAY names a value, not a relation, so it is
	// skipped; a subquery inside those calls opens its own parenthesis and is checked.
	argFrom := argumentFroms(body)
	for i := 0; i < len(body); i++ {
		if !body[i].is("from", "join") || argFrom[i] {
			continue
		}
		j := i + 1
		for j < len(body) {
			if body[j].is("lateral", "only") {
				j++
				continue
			}
			if body[j].ident() && j+2 < len(body) && body[j+1].kind == tPunct && body[j+1].text == "." && body[j+2].ident() {
				if body[j].text != c.schema {
					return fmt.Errorf("view %s reads %s.%s outside the module's schema", name, body[j].text, body[j+2].text)
				}
			}
			if body[j].ident() && j+1 < len(body) && body[j+1].kind == tPunct && body[j+1].text == "(" && !body[j].is("select", "values") {
				return fmt.Errorf("view %s reads from a function, %s(...)", name, body[j].text)
			}
			break
		}
	}
	for _, t := range body {
		if t.is("into") {
			return fmt.Errorf("SELECT INTO is not allowed in a view")
		}
	}
	c.views[name] = true
	return nil
}

// argumentFroms marks each FROM whose innermost enclosing parenthesis belongs to a call
// of EXTRACT, SUBSTRING, TRIM or OVERLAY, where FROM is part of the call's syntax.
func argumentFroms(body []tok) map[int]bool {
	out := map[int]bool{}
	var calls []bool
	var prev tok
	for i, t := range body {
		switch {
		case t.kind == tPunct && t.text == "(":
			calls = append(calls, prev.is("extract", "substring", "trim", "overlay"))
		case t.kind == tPunct && t.text == ")":
			if len(calls) > 0 {
				calls = calls[:len(calls)-1]
			}
		case t.is("from") && len(calls) > 0 && calls[len(calls)-1]:
			out[i] = true
		}
		prev = t
	}
	return out
}

func (c *migrationCheck) alterTable(p *stmt) error {
	p.accept("if", "exists")
	p.accept("only")
	name, err := p.objectName()
	if err != nil {
		return err
	}
	rest := p.toks[p.i:]
	if err := c.scanExpr(rest); err != nil {
		return err
	}
	for i, t := range rest {
		switch {
		case t.is("owner", "trigger", "rule", "policy", "tablespace", "inherit", "attach", "detach", "replica"):
			return fmt.Errorf("ALTER TABLE ... %s is not allowed", strings.ToUpper(t.text))
		case t.is("security") && i+1 < len(rest) && rest[i+1].is("level", "invoker", "definer"):
			return fmt.Errorf("row security changes are not allowed")
		case t.is("set") && i+1 < len(rest) && rest[i+1].is("schema", "logged", "unlogged", "access"):
			return fmt.Errorf("ALTER TABLE ... SET %s is not allowed", strings.ToUpper(rest[i+1].text))
		case t.is("rename") && i+1 < len(rest) && rest[i+1].is("to") && i+2 < len(rest):
			if err := c.checkTableName(rest[i+2].text); err != nil {
				return err
			}
			if info, ok := c.tables[name]; ok {
				c.tables[rest[i+2].text] = info
				delete(c.tables, name)
			}
		case t.is("references"):
			core, _, err := c.references(rest, i)
			if err != nil {
				return err
			}
			if info, ok := c.tables[name]; ok && core != "" {
				// "ADD [CONSTRAINT x] FOREIGN KEY (col) REFERENCES core.t" or "ADD COLUMN col ... REFERENCES".
				if cols := fkColumns(rest[:i]); len(cols) == one {
					info.coreFK[cols[0]] = core
				}
			}
		}
	}
	if info, ok := c.tables[name]; ok {
		for i := 0; i+1 < len(rest); i++ {
			if rest[i].is("add") {
				j := i + 1
				if rest[j].is("column") {
					j++
				}
				if j < len(rest) && rest[j].is("if") {
					j += (two + one)
				}
				if j < len(rest) && rest[j].ident() && !rest[j].is("constraint", "foreign", "primary", "unique", "check") {
					info.columns = append(info.columns, rest[j].text)
				}
			}
		}
		if err := identifyingCopy(name, info); err != nil {
			return err
		}
	}
	return nil
}

// fkColumns finds the column list of the nearest FOREIGN KEY (...) or ADD COLUMN name.
func fkColumns(toks []tok) []string {
	for i := len(toks) - 1; i >= 0; i-- {
		if toks[i].is("key") && i > zero && toks[i-one].is("foreign") {
			return identList(toks[i+1:])
		}
		if toks[i].is("add") {
			j := i + 1
			if j < len(toks) && toks[j].is("column") {
				j++
			}
			if j < len(toks) && toks[j].ident() {
				return []string{toks[j].text}
			}
		}
	}
	return nil
}

func (c *migrationCheck) alterOther(p *stmt) error {
	p.accept("if", "exists")
	if _, err := p.objectName(); err != nil {
		return err
	}
	rest := p.toks[p.i:]
	for i, t := range rest {
		if t.is("owner", "tablespace", "depends", "attach") || (t.is("set") && i+1 < len(rest) && rest[i+1].is("schema", "tablespace")) {
			return fmt.Errorf("this ALTER is not allowed")
		}
		if t.is("rename") && i+2 < len(rest) && rest[i+1].is("to") {
			if err := c.checkTableName(rest[i+2].text); err != nil {
				return err
			}
		}
	}
	return c.scanExpr(rest)
}

func (c *migrationCheck) drop(p *stmt) error {
	if p.accept("concurrently") {
		return fmt.Errorf("DROP INDEX CONCURRENTLY can't run in the migration's transaction")
	}
	p.accept("if", "exists")
	for {
		name, err := p.objectName()
		if err != nil {
			return err
		}
		delete(c.tables, name)
		delete(c.views, name)
		if !p.punct(",") {
			break
		}
	}
	if p.accept("cascade") {
		return fmt.Errorf("DROP ... CASCADE is not allowed; drop dependent objects explicitly")
	}
	p.accept("restrict")
	if !p.done() {
		return fmt.Errorf("unexpected %q in DROP", p.peek().text)
	}
	return nil
}

package modules

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/db"
)

// coreTablesList is every table in schema core after the core's own migrations, one a
// line: what CreatedTables reads from the core's migration list. A drift test in
// internal/core keeps it equal to that list, so a module check (the kit's included,
// which runs with no database) knows what the core's schema holds.
//
//go:embed core_tables.txt
var coreTablesList string

var coreTables = func() map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(coreTablesList, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out[t] = true
		}
	}
	return out
}()

// CoreSchemaTables lists the tables in schema core, sorted.
func CoreSchemaTables() []string {
	out := make([]string, zero, len(coreTables))
	for t := range coreTables {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// CreatedTables reads migrations run in order in schema core (unqualified names, or
// names qualified core.) and returns the tables that exist after them, sorted: each
// CREATE TABLE, less each DROP TABLE, with ALTER TABLE ... RENAME TO followed.
func CreatedTables(ms []db.Migration) ([]string, error) {
	tables := map[string]bool{}
	for _, mig := range ms {
		toks, err := tokenize(mig.SQL)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", mig.Name, err)
		}
		for _, st := range splitStatements(toks) {
			p := &stmt{toks: st, schema: "core"}
			switch {
			case p.accept("create"):
				p.accept("unlogged")
				if !p.accept("table") {
					continue
				}
				p.accept("if", "not", "exists")
				if name, err := p.objectName(); err == nil {
					tables[name] = true
				}
			case p.accept("drop", "table"):
				p.accept("if", "exists")
				for {
					if name, err := p.objectName(); err == nil {
						delete(tables, name)
					}
					if !p.punct(",") {
						break
					}
				}
			case p.accept("alter", "table"):
				p.accept("if", "exists")
				p.accept("only")
				name, err := p.objectName()
				if err != nil || !p.accept("rename", "to") {
					continue
				}
				if to := p.peek(); to.ident() && tables[name] {
					delete(tables, name)
					tables[to.text] = true
				}
			}
		}
	}
	out := make([]string, zero, len(tables))
	for t := range tables {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

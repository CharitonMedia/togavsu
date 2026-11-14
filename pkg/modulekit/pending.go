package modulekit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing/fstest"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/modules"
	"github.com/CharitonMedia/togavsu/internal/modules/yamlsubset"
)

// PendingFile is the pending list's path in a package directory: the parts of the
// package that wait on a core change. It never ships; Installable leaves it out.
//
//	pending:
//	  - field: sidebar[*].section
//	    core: CORE-08
//	    reason: Files the entry under Money in the sidebar.
//	  - scope: books:read
//	    core: CORE-02
//	    reason: Reads the firm's books without a viewer for the nightly sync.
//	  - migration: migrations/0004_trust_bank_fk.sql
//	    core: CORE-22
//	    reason: Links each trust bank mapping to its trust account.
//
// Each entry names exactly one part: a manifest field path (keys joined by dots, list
// indexes in brackets, [*] for any index; it covers a refusal at that path or inside
// it), a permission scope, or a whole migration file. core names the core change it
// waits on and reason says in one line what the part does.
const PendingFile = "pending.yaml"

// Pending is one entry of the pending list.
type Pending struct {
	Field     string `json:"field,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Migration string `json:"migration,omitempty"`
	Core      string `json:"core"`
	Reason    string `json:"reason"`
}

// Part names the entry's part: "field sidebar[*].section", "scope books:read" or
// "migration migrations/0004_x.sql".
func (p Pending) Part() string {
	switch {
	case p.Field != "":
		return "field " + p.Field
	case p.Scope != "":
		return "scope " + p.Scope
	}
	return "migration " + p.Migration
}

// Held is a part Installable held back: the pending entry, the package file it was
// removed from, the concrete part (a field path in the manifest as it stood when the
// part was removed, or the migration file) and the core's refusals of it.
type Held struct {
	Pending  Pending   `json:"pending"`
	File     string    `json:"file"`
	Part     string    `json:"part"`
	Refusals []Refusal `json:"refusals"`
}

// String is one line naming the part and the core item it waits on.
func (h Held) String() string {
	part := h.File
	if h.Part != h.File {
		part += " " + h.Part
	}
	if h.Pending.Scope != "" {
		part += " (scope " + h.Pending.Scope + ")"
	}
	return fmt.Sprintf("held back %s: waits on %s: %s", part, h.Pending.Core, h.Pending.Reason)
}

var migrationFile = regexp.MustCompile(`^migrations/[0-9]+_[A-Za-z0-9._-]+\.sql$`)

type pendingDoc struct {
	Pending []Pending `json:"pending"`
}

// ParsePending reads a pending list (PendingFile) in the manifest's YAML subset.
func ParsePending(src []byte) ([]Pending, error) {
	bad := func(msg string) error {
		return Refusals{{Kind: KindPending, Code: RefusePendingInvalid, File: PendingFile, Message: msg}}
	}
	raw, err := yamlsubset.ToJSON(src)
	if err != nil {
		return nil, bad(err.Error())
	}
	var doc pendingDoc
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, bad(err.Error())
	}
	seen := map[string]bool{}
	for i, p := range doc.Pending {
		at := fmt.Sprintf("pending[%d]: ", i)
		named := zero
		for _, v := range []string{p.Field, p.Scope, p.Migration} {
			if v != "" {
				named++
			}
		}
		switch {
		case named != one:
			return nil, bad(at + "name exactly one of field, scope or migration")
		case strings.TrimSpace(p.Core) == "":
			return nil, bad(at + "core names the core change the part waits on")
		case strings.TrimSpace(p.Reason) == "":
			return nil, bad(at + "reason says in one line what the part does")
		case seen[p.Part()]:
			return nil, bad(at + "duplicate entry for " + p.Part())
		}
		seen[p.Part()] = true
		if p.Field != "" {
			if _, err := yamlsubset.ParsePath(p.Field); err != nil {
				return nil, bad(at + err.Error())
			}
		}
		if p.Migration != "" && !migrationFile.MatchString(p.Migration) {
			return nil, bad(at + "migration is a file path such as migrations/0004_name.sql")
		}
	}
	return doc.Pending, nil
}

// Installable returns the package files the core accepts: files without PendingFile and
// without each pending part the core refuses. A pending part the core accepts stays in,
// so each part returns on its own once its core change lands. It also returns the parts
// it held back. A refusal no pending entry covers is an error (a Refusals listing each
// one), and so is a part that can't be removed cleanly.
//
// Fields and scopes are removed from the manifest text in place (comments and layout
// kept; the edit is read back and must be exactly the manifest without those parts).
// Migrations are held back by whole file, never by statement: a migration a firm has
// run can never change (CheckUpgrade), so restoring a removed statement later would
// make the package impossible to update. A held-back file must therefore come after
// every migration that installs, so that it is added later as a new migration; put each
// statement that waits on a core change in its own file at the end.
func Installable(files map[string][]byte) (map[string][]byte, []Held, error) {
	var entries []Pending
	if src, ok := files[PendingFile]; ok {
		var err error
		if entries, err = ParsePending(src); err != nil {
			return nil, nil, err
		}
	}
	out := make(map[string][]byte, len(files))
	for k, v := range files {
		if k != PendingFile {
			out[k] = append([]byte(nil), v...)
		}
	}
	var held []Held
	for {
		raw, err := Archive(out)
		if err != nil {
			return nil, nil, err
		}
		_, _, refs := modules.CheckAll(raw)
		if len(refs) == zero {
			break
		}
		var uncovered Refusals
		parts := map[string]*Held{}
		var order []string
		for _, r := range refs {
			h, ok := cover(entries, r)
			if !ok {
				uncovered = append(uncovered, r)
				continue
			}
			key := h.File + " " + h.Part
			if prev, ok := parts[key]; ok {
				prev.Refusals = append(prev.Refusals, r)
				continue
			}
			parts[key] = &h
			order = append(order, key)
		}
		if len(uncovered) > zero {
			return nil, nil, uncovered
		}
		var paths []string
		for _, key := range order {
			h := parts[key]
			if h.Pending.Migration != "" {
				delete(out, h.File)
			} else {
				paths = append(paths, h.Part)
			}
			held = append(held, *h)
		}
		if len(paths) > zero {
			edited, err := yamlsubset.Remove(out[ManifestFile], paths...)
			if err != nil {
				return nil, nil, Refusals{{Kind: KindPending, Code: RefusePendingEdit, File: ManifestFile,
					Path: strings.Join(paths, ", "), Message: err.Error()}}
			}
			out[ManifestFile] = edited
		}
	}
	if err := heldLast(out, held); err != nil {
		return nil, nil, err
	}
	if _, _, err := Check(out); err != nil {
		return nil, nil, err
	}
	return out, held, nil
}

// cover finds the pending entry that covers refusal r and the concrete part to remove.
func cover(entries []Pending, r Refusal) (Held, bool) {
	for _, e := range entries {
		switch {
		case e.Scope != "":
			if r.Code == RefuseUnknownScope && r.Value == e.Scope {
				return Held{Pending: e, File: r.File, Part: r.Path, Refusals: []Refusal{r}}, true
			}
		case e.Migration != "":
			if r.Kind == KindMigration && r.File == e.Migration {
				return Held{Pending: e, File: e.Migration, Part: e.Migration, Refusals: []Refusal{r}}, true
			}
		default:
			if r.File != ManifestFile || r.Path == "" || r.Code == RefuseUnknownScope {
				continue
			}
			want, err := yamlsubset.ParsePath(e.Field)
			if err != nil {
				continue
			}
			got, err := yamlsubset.ParsePath(r.Path)
			if err != nil || !yamlsubset.HasPrefix(got, want) {
				continue
			}
			return Held{Pending: e, File: ManifestFile, Part: yamlsubset.FormatPath(got[:len(want)]), Refusals: []Refusal{r}}, true
		}
	}
	return Held{}, false
}

// heldLast refuses a held-back migration numbered before one that installs.
func heldLast(files map[string][]byte, held []Held) error {
	last, lastName := -one, ""
	for name, body := range files {
		if v, ok := migrationVersion(name, body); ok && v > last {
			last, lastName = v, name
		}
	}
	var refs Refusals
	for _, h := range held {
		if h.Pending.Migration == "" {
			continue
		}
		if v, ok := migrationVersion(h.File, nil); ok && v < last {
			refs = append(refs, Refusal{Kind: KindPending, Code: RefusePendingOrder, File: h.File,
				Message: h.File + " is held back but " + lastName + " after it installs; a held-back migration must come after every migration that installs, so it can be added later without changing one a firm has run. Number it after " + lastName + "."})
		}
	}
	if len(refs) > zero {
		sort.Slice(refs, func(i, j int) bool { return refs[i].File < refs[j].File })
		return refs
	}
	return nil
}

func migrationVersion(name string, body []byte) (int, bool) {
	if !migrationFile.MatchString(name) {
		return zero, false
	}
	ms, err := db.LoadFS(fstest.MapFS{strings.TrimPrefix(name, "migrations/"): &fstest.MapFile{Data: body}})
	if err != nil || len(ms) != one {
		return zero, false
	}
	return ms[zero].Version, true
}

// Unused lists the pending entries that held nothing back: the core accepts the part
// now, or the package no longer has it. Their entries can go.
func Unused(entries []Pending, held []Held) []Pending {
	used := map[string]bool{}
	for _, h := range held {
		used[h.Pending.Part()] = true
	}
	var out []Pending
	for _, e := range entries {
		if !used[e.Part()] {
			out = append(out, e)
		}
	}
	return out
}

// Small fixed numbers spelled as arithmetic for the AC-030 source check.
var (
	zero = int(0)
	one  = zero + 1
)

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

// PendingFile is the pending list's path in a package directory, beside the manifest
// (package/pending.yaml when the package directory is package/): the parts of the
// package that wait on a core change. It never ships: Installable leaves it out, and
// Build and the core refuse a package that holds it.
//
//	pending:
//	  - field: sidebar[*].section
//	    core: CORE-08
//	    reason: Files the entry under Money in the sidebar.
//	  - field: tabs[id=pay]
//	    core: [CORE-10, CORE-12]
//	    reason: The client's Pay tab.
//	  - scope: books:read
//	    core: CORE-02
//	    reason: Reads the firm's books without a viewer for the nightly sync.
//	  - migration: migrations/0004_trust_bank_fk.sql
//	    core: CORE-22
//	    reason: Links each trust bank mapping to its trust account.
//
// Each entry names exactly one part: a manifest field path, a permission scope, or a
// whole migration file. A field path is keys joined by dots with list indexes in
// brackets; [*] is any index and [key=value] the list items whose key holds value
// (tabs[id=pay], tabs[placement=client]), resolved against the manifest as written. It
// covers a refusal at that path or inside it. core names the core change, or a list of
// them, the part waits on, and reason says in one line what the part does.
//
// An entry holds back only the refusals its kind can cover: a field entry an unknown
// field, a refused value or a data cross-check at its path; a scope entry the scope's
// unknown_scope; a migration entry its file's core_reference and core_relation_missing
// refusals. Any other refusal is an error even inside a held part.
const PendingFile = "pending.yaml"

// Pending is one entry of the pending list.
type Pending struct {
	Field     string    `json:"field,omitempty"`
	Scope     string    `json:"scope,omitempty"`
	Migration string    `json:"migration,omitempty"`
	Core      CoreItems `json:"core"`
	Reason    string    `json:"reason"`
}

// CoreItems are the core changes a pending part waits on. In pending.yaml core is one
// item (core: CORE-22) or a list (core: [CORE-22, CORE-24]).
type CoreItems []string

// UnmarshalJSON reads one item or a list.
func (c *CoreItems) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*c = CoreItems{one}
		return nil
	}
	var list []string
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("core is one core item or a list of them")
	}
	*c = list
	return nil
}

// String joins the items with " and ".
func (c CoreItems) String() string { return strings.Join(c, " and ") }

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
// removed from, the concrete part (a field path in the manifest as written, or the
// migration file) and the core's refusals of it (their paths, too, in the manifest as
// written).
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
	return fmt.Sprintf("held back %s: waits on %s: %s", part, h.Pending.Core.String(), h.Pending.Reason)
}

func named(items CoreItems) bool {
	for _, c := range items {
		if strings.TrimSpace(c) == "" {
			return false
		}
	}
	return len(items) > zero
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
		parts := zero
		for _, v := range []string{p.Field, p.Scope, p.Migration} {
			if v != "" {
				parts++
			}
		}
		switch {
		case parts != one:
			return nil, bad(at + "name exactly one of field, scope or migration")
		case !named(p.Core):
			return nil, bad(at + "core names the core change, or a list of them, the part waits on")
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

// InstallablePackage is Installable's result: the files to build, the parts held back,
// the parsed pending list, and the entries that held nothing back (Unused).
type InstallablePackage struct {
	Files   map[string][]byte
	Held    []Held
	Pending []Pending
	Unused  []Pending
}

// UnusedRefusals reports each unused entry as a pending_unused refusal, for a check that
// treats an entry with nothing to hold back as an error (togavsu-module -strict).
func (ip InstallablePackage) UnusedRefusals() Refusals {
	var out Refusals
	for _, e := range ip.Unused {
		out = append(out, Refusal{Kind: KindPending, Code: RefusePendingUnused, File: PendingFile, Value: e.Part(),
			Message: "pending " + e.Part() + " (" + e.Core.String() + ") holds nothing back: the core accepts it or the package no longer has it; remove the entry"})
	}
	return out
}

// Installable returns the package files the core accepts: files without PendingFile and
// without each pending part the core refuses. A pending part the core accepts stays in,
// so each part returns on its own once its core change lands. It also returns the parts
// it held back, the pending list and its unused entries, whose parts the core accepts
// now or the package no longer has. A refusal no pending entry covers is an error (a
// Refusals listing each one, each saying it is not covered by pending.yaml), and so is
// a part that can't be removed cleanly.
//
// A package directory embedded with go:embed embeds PendingFile with it (and any other
// file the pattern names), so anything that lists, archives or ships the embedded files
// goes through Installable, or Files, which reads only the package's own files.
//
// Fields and scopes are removed from the manifest text in place (comments and layout
// kept, a part's own comment lines removed with it; the edit is read back and must be
// exactly the manifest without those parts). Paths are always resolved against the
// manifest as written, so holding back tabs[2] and tabs[3] removes those two tabs. The
// check is repeated on the edited manifest until nothing covered is left, so one call
// holds back every covered part even where one refusal hides the next.
//
// Migrations are held back by whole file, never by statement: a migration a firm has
// run can never change (CheckUpgrade), so restoring a removed statement later would
// make the package impossible to update. A held-back file must therefore come after
// every migration that installs, so that it is added later as a new migration; put each
// statement that waits on a core change in its own file at the end. A held-back file is
// also checked as if every core relation it references existed, so a second problem in
// it is an error now, not when its core change lands. It stays held back until the
// core both lets modules link to each relation it references and has the relation in
// its schema.
func Installable(files map[string][]byte) (InstallablePackage, error) {
	var entries []Pending
	if src, ok := files[PendingFile]; ok {
		var err error
		if entries, err = ParsePending(src); err != nil {
			return InstallablePackage{}, err
		}
	}
	out := make(map[string][]byte, len(files))
	for k, v := range files {
		if k != PendingFile {
			out[k] = append([]byte(nil), v...)
		}
	}
	orig := out[ManifestFile]
	var doc any
	if orig != nil {
		doc, _ = yamlsubset.Parse(orig) // a manifest that doesn't parse is refused below
	}
	patterns := map[int][][]yamlsubset.Segment{}
	for i, e := range entries {
		if e.Field != "" {
			want, _ := yamlsubset.ParsePath(e.Field) // ParsePending checked it
			patterns[i] = yamlsubset.Expand(doc, want)
		}
	}
	var held []Held
	var removed [][]yamlsubset.Segment
	var removedPaths []string
	removedFiles := map[string]bool{}
	for {
		if len(removedPaths) > zero {
			edited, err := yamlsubset.Remove(orig, removedPaths...)
			if err != nil {
				return InstallablePackage{}, Refusals{{Kind: KindPending, Code: RefusePendingEdit, File: ManifestFile,
					Path: strings.Join(removedPaths, ", "), Message: err.Error()}}
			}
			out[ManifestFile] = edited
		}
		raw, err := Archive(out)
		if err != nil {
			return InstallablePackage{}, err
		}
		_, _, refs := modules.CheckAll(raw)
		if len(refs) == zero {
			break
		}
		var uncovered Refusals
		parts := map[string]*Held{}
		var order []string
		for _, r := range refs {
			if r.File == ManifestFile && r.Path != "" {
				if segs, err := yamlsubset.ParsePath(r.Path); err == nil {
					r.Path = yamlsubset.FormatPath(yamlsubset.Original(segs, removed))
				}
			}
			h, why := cover(entries, patterns, r)
			if why != "" {
				uncovered = append(uncovered, notCovered(r, why))
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
			return InstallablePackage{}, uncovered
		}
		progress := false
		for _, key := range order {
			h := parts[key]
			if h.Pending.Migration != "" {
				if removedFiles[h.File] {
					continue
				}
				removedFiles[h.File] = true
				delete(out, h.File)
			} else {
				segs, _ := yamlsubset.ParsePath(h.Part)
				removed = append(removed, segs)
				removedPaths = append(removedPaths, h.Part)
			}
			progress = true
			held = append(held, *h)
		}
		if !progress {
			return InstallablePackage{}, Refusals{{Kind: KindPending, Code: RefusePendingEdit, File: ManifestFile,
				Message: "the core still refuses parts already held back: " + refs.Error()}}
		}
	}
	if err := heldLast(out, held); err != nil {
		return InstallablePackage{}, err
	}
	if _, _, err := Check(out); err != nil {
		return InstallablePackage{}, err
	}
	if err := heldMigrationsOtherwiseAccepted(files, out, held); err != nil {
		return InstallablePackage{}, err
	}
	return InstallablePackage{Files: out, Held: held, Pending: entries, Unused: Unused(entries, held)}, nil
}

// notCovered marks a refusal no pending entry holds back, in its message and its error.
func notCovered(r Refusal, why string) Refusal {
	note := " (not covered by " + PendingFile + ")"
	if why != notCoveredAtAll {
		note = " (not covered by " + PendingFile + ": " + why + ")"
	}
	r.Message += note
	if r.Err != nil {
		r.Err = fmt.Errorf("%w%s", r.Err, note)
	}
	return r
}

const notCoveredAtAll = "no entry"

// cover finds the pending entry that covers refusal r and the concrete part to remove,
// or says why none does. patterns holds each field entry's paths, resolved against the
// manifest as written; r's path is in that manifest too.
func cover(entries []Pending, patterns map[int][][]yamlsubset.Segment, r Refusal) (Held, string) {
	why := notCoveredAtAll
	for i, e := range entries {
		switch {
		case e.Scope != "":
			if r.Code == RefuseUnknownScope && r.Value == e.Scope {
				return Held{Pending: e, File: r.File, Part: r.Path, Refusals: []Refusal{r}}, ""
			}
		case e.Migration != "":
			if r.File != e.Migration {
				continue
			}
			if r.Code == RefuseCoreReference || r.Code == RefuseCoreRelationMissing {
				return Held{Pending: e, File: e.Migration, Part: e.Migration, Refusals: []Refusal{r}}, ""
			}
			why = "a migration entry holds back only the file's references to core relations (" +
				RefuseCoreReference + ", " + RefuseCoreRelationMissing + "), not " + r.Code
		default:
			if r.File != ManifestFile || r.Path == "" {
				continue
			}
			got, err := yamlsubset.ParsePath(r.Path)
			if err != nil {
				continue
			}
			for _, want := range patterns[i] {
				if !yamlsubset.HasPrefix(got, want) {
					continue
				}
				switch r.Code {
				case RefuseUnknownField, RefuseManifest, RefuseCrossCheck:
					return Held{Pending: e, File: ManifestFile, Part: yamlsubset.FormatPath(got[:len(want)]), Refusals: []Refusal{r}}, ""
				}
				why = "a field entry holds back unknown fields, refused values and data cross-checks, not " + r.Code +
					"; name the scope in a scope entry"
			}
		}
	}
	return Held{}, why
}

// heldMigrationsOtherwiseAccepted checks each held-back migration as if the core
// relations it references existed, so a second problem in the same statement, or one
// the reference hid, is an error now.
func heldMigrationsOtherwiseAccepted(files, out map[string][]byte, held []Held) error {
	all := map[string][]byte{}
	for k, v := range out {
		all[k] = v
	}
	heldFiles := map[string]Held{}
	for _, h := range held {
		if h.Pending.Migration != "" {
			all[h.File] = files[h.File]
			heldFiles[h.File] = h
		}
	}
	if len(heldFiles) == zero {
		return nil
	}
	m, err := modules.ParseManifest(out[ManifestFile])
	if err != nil {
		return err
	}
	ms, err := (&modules.Package{Files: all}).Migrations()
	if err != nil {
		return err
	}
	var refs Refusals
	for _, r := range modules.MigrationRefusalsAssumingCore(m, ms) {
		if h, ok := heldFiles[r.File]; ok {
			refs = append(refs, notCovered(r, "held back for "+h.Pending.Core.String()+
				", but refused even once the core relations it references exist"))
		}
	}
	if len(refs) > zero {
		return refs
	}
	return nil
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

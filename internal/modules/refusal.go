package modules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/db"
	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/identity"
	"github.com/CharitonMedia/togavsu/internal/marketplace"
	"github.com/CharitonMedia/togavsu/internal/modules/yamlsubset"
)

// Refusal kinds: which part of a package a refusal is about.
const (
	KindPackage   = "package"
	KindManifest  = "manifest"
	KindScope     = "scope"
	KindMigration = "migration"
)

// Refusal codes. They are stable: module tooling matches on them, never on message text.
const (
	// RefusePackage: the archive can't be read, or has no manifest.
	RefusePackage = "package_unreadable"
	// RefuseUnknownFile: a package file the core doesn't read (anything but the
	// manifest and migrations/).
	RefuseUnknownFile = "unknown_file"
	// RefuseYAML: the manifest is not in the supported YAML subset.
	RefuseYAML = "manifest_yaml"
	// RefuseUnknownField: a manifest key, at any depth, that this core doesn't read.
	RefuseUnknownField = "unknown_field"
	// RefuseManifest: a manifest value this core refuses (the manifest checks).
	RefuseManifest = "manifest_invalid"
	// RefuseUnknownScope: a permission scope that is not in this core's scope list.
	RefuseUnknownScope = "unknown_scope"
	// RefuseCoreReference: a migration references a core relation a module can't link to.
	RefuseCoreReference = "core_reference"
	// RefuseCoreRelationMissing: a migration references a core relation a module may
	// link to that this core's schema doesn't have yet.
	RefuseCoreRelationMissing = "core_relation_missing"
	// RefuseMigrationFile: a migration file name, place or version the core refuses.
	RefuseMigrationFile = "migration_file"
	// RefuseStatement: any other migration statement the core refuses.
	RefuseStatement = "migration_statement"
	// RefuseCrossCheck: the manifest's data declarations and the migrations disagree.
	RefuseCrossCheck = "migration_crosscheck"
)

// Refusal is one thing the core refuses in a package. File is the package file
// (togavsu-module.yaml or migrations/NNNN_name.sql); Path is the manifest field path
// (sidebar[0].section, permissions[3]) for manifest and scope refusals, and for a
// cross-check the declaration; Statement is the 1-based statement number in a migration;
// Value is the unknown field's key, the unknown scope or the refused core relation.
// APICode is the code the install API answers the same problem with, and Err the error
// the core's install check returns for it.
type Refusal struct {
	Kind      string `json:"kind"`
	Code      string `json:"code"`
	File      string `json:"file,omitempty"`
	Path      string `json:"path,omitempty"`
	Statement int    `json:"statement,omitempty"`
	Value     string `json:"value,omitempty"`
	Message   string `json:"message"`
	APICode   string `json:"api_code,omitempty"`
	Err       error  `json:"-"`
}

// Error is the core's own error text for the refusal.
func (r Refusal) Error() string {
	if r.Err != nil {
		return r.Err.Error()
	}
	return r.Code + ": " + r.Message
}

// Unwrap is the core's error, so errors.As finds its *domain.APIError.
func (r Refusal) Unwrap() error { return r.Err }

// Where names the refusal's place: file, then path or statement.
func (r Refusal) Where() string {
	switch {
	case r.Path != "":
		return r.File + " " + r.Path
	case r.Statement > zero:
		return fmt.Sprintf("%s statement %d", r.File, r.Statement)
	}
	return r.File
}

// Refusals is every refusal of one check. As an error it is all of them; errors.As
// reaches each Refusal and, through the first, the core's error.
type Refusals []Refusal

func (rs Refusals) Error() string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = r.Error()
	}
	return strings.Join(parts, "; ")
}

// Unwrap returns each refusal.
func (rs Refusals) Unwrap() []error {
	out := make([]error, len(rs))
	for i, r := range rs {
		out[i] = r
	}
	return out
}

// CoreReferenceError is a migration reference to a core relation a module can't link
// to: one not on the list of core records, or (Missing) one on it that this core's
// schema doesn't have.
type CoreReferenceError struct {
	Relation string
	Missing  bool
}

func (e *CoreReferenceError) Error() string {
	if e.Missing {
		return e.Relation + " is not in this core's schema"
	}
	return e.Relation + " is not a core record a module can link to"
}

func migrationRefusal(file string, statement int, cause, apiErr error) Refusal {
	r := Refusal{Kind: KindMigration, Code: RefuseStatement, File: file, Statement: statement,
		Message: cause.Error(), APICode: CodeMigration, Err: apiErr}
	var ref *CoreReferenceError
	if errors.As(cause, &ref) {
		r.Code, r.Value = RefuseCoreReference, ref.Relation
		if ref.Missing {
			r.Code = RefuseCoreRelationMissing
		}
	}
	return r
}

// CheckAll runs the install checks over package bytes, as CheckPackage does, and also
// the scope list the core applies at approval, and reports every refusal instead of the
// first: each file the core doesn't read, each unknown manifest field at any depth, each
// refused manifest value (ValidateAll), each unknown scope, each refused migration
// statement and cross-check. A manifest that doesn't decode (a value of the wrong type)
// stops there. A package with no
// refusals here passes CheckPackage, and its scopes are all in the core's list. The
// package and migrations are returned when they could be read, refused or not.
func CheckAll(raw []byte) (*Package, []db.Migration, Refusals) {
	files, err := unpack(raw)
	if err != nil {
		return nil, nil, Refusals{packageRefusal(err)}
	}
	src, ok := files[ManifestFile]
	if !ok {
		return nil, nil, Refusals{packageRefusal(invalid("package", "the package has no "+ManifestFile))}
	}
	var refs Refusals
	for _, name := range unknownFiles(files) {
		err := unknownFileError(name)
		refs = append(refs, Refusal{Kind: KindPackage, Code: RefuseUnknownFile, File: name,
			Message: apiMessage(err), APICode: errCode(err), Err: err})
	}
	m, mrefs, ok := checkManifest(src)
	refs = append(refs, mrefs...)
	if !ok {
		return nil, nil, refs
	}
	refs = append(refs, scopeRefusals(m)...)
	pkg := &Package{Files: files, Manifest: m, Digest: marketplace.Digest(raw)}
	if !moduleID.MatchString(m.ID) {
		return pkg, nil, refs
	}
	ms, err := pkg.Migrations()
	if err != nil {
		return pkg, nil, append(refs, Refusal{Kind: KindMigration, Code: RefuseMigrationFile, File: "migrations/",
			Message: apiMessage(err), APICode: errCode(err), Err: err})
	}
	refs = append(refs, MigrationRefusals(m, ms)...)
	if len(refs) > zero {
		return pkg, ms, refs
	}
	return pkg, ms, nil
}

func packageRefusal(err error) Refusal {
	return Refusal{Kind: KindPackage, Code: RefusePackage, Message: apiMessage(err), APICode: errCode(err), Err: err}
}

// errCode is the API code err carries, or "".
func errCode(err error) string {
	var api *domain.APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

func apiMessage(err error) string {
	var api *domain.APIError
	if errors.As(err, &api) {
		return api.Message
	}
	return err.Error()
}

// checkManifest is ParseManifest reporting every unknown field. ok is false when no
// manifest could be decoded at all.
func checkManifest(src []byte) (Manifest, Refusals, bool) {
	manifestRefusal := func(code, path, value string, err error) Refusal {
		return Refusal{Kind: KindManifest, Code: code, File: ManifestFile, Path: path, Value: value,
			Message: apiMessage(err), APICode: errCode(err), Err: err}
	}
	raw, err := yamlsubset.ToJSON(src)
	if err != nil {
		return Manifest{}, Refusals{manifestRefusal(RefuseYAML, "", "", invalid("manifest", err.Error()))}, false
	}
	var doc any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return Manifest{}, Refusals{manifestRefusal(RefuseYAML, "", "", invalid("manifest", err.Error()))}, false
	}
	var refs Refusals
	var unknown [][]yamlsubset.Segment
	walkUnknown(doc, reflect.TypeOf(Manifest{}), nil, func(path []yamlsubset.Segment) {
		key := path[len(path)-one].Key
		refs = append(refs, manifestRefusal(RefuseUnknownField, yamlsubset.FormatPath(path), key,
			invalid("manifest", fmt.Sprintf("%v: json: unknown field %q", ErrYAML, key))))
		unknown = append(unknown, path)
	})
	if len(unknown) > zero {
		for _, path := range unknown {
			doc, _ = yamlsubset.Delete(doc, path)
		}
		if raw, err = json.Marshal(doc); err != nil {
			return Manifest{}, append(refs, manifestRefusal(RefuseYAML, "", "", invalid("manifest", err.Error()))), false
		}
	}
	var m Manifest
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&m); err != nil {
		return Manifest{}, append(refs, manifestRefusal(RefuseManifest, "", "", invalid("manifest", fmt.Errorf("%w: %v", ErrYAML, err).Error()))), false
	}
	for _, err := range m.ValidateAll() {
		var api *domain.APIError
		path := ""
		if errors.As(err, &api) {
			path = api.Field
		}
		refs = append(refs, manifestRefusal(RefuseManifest, path, "", err))
	}
	return m, refs, true
}

// walkUnknown reports each key of doc that decodes into no field of t, as the manifest
// decoder (encoding/json with unknown fields refused) matches keys: exactly, else
// ignoring case. It does not descend into a value whose shape doesn't fit t; the decode
// refuses that.
func walkUnknown(doc any, t reflect.Type, path []yamlsubset.Segment, report func([]yamlsubset.Segment)) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	at := func(seg yamlsubset.Segment) []yamlsubset.Segment {
		return append(append([]yamlsubset.Segment(nil), path...), seg)
	}
	switch t.Kind() {
	case reflect.Struct:
		obj, ok := doc.(map[string]any)
		if !ok {
			return
		}
		fields := jsonFields(t)
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			f, ok := fieldFor(fields, k)
			if !ok {
				report(at(yamlsubset.Segment{Key: k}))
				continue
			}
			walkUnknown(obj[k], f.typ, at(yamlsubset.Segment{Key: k}), report)
		}
	case reflect.Slice, reflect.Array:
		list, ok := doc.([]any)
		if !ok {
			return
		}
		for i, v := range list {
			walkUnknown(v, t.Elem(), at(yamlsubset.Segment{IsIndex: true, Index: i}), report)
		}
	case reflect.Map:
		obj, ok := doc.(map[string]any)
		if !ok {
			return
		}
		for k, v := range obj {
			walkUnknown(v, t.Elem(), at(yamlsubset.Segment{Key: k}), report)
		}
	}
}

type jsonField struct {
	name string
	typ  reflect.Type
}

// jsonFields lists the JSON names a struct decodes, as encoding/json names them.
func jsonFields(t reflect.Type) []jsonField {
	var out []jsonField
	for i := zero; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			et := f.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				out = append(out, jsonFields(et)...)
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, jsonField{name: name, typ: f.Type})
	}
	return out
}

func fieldFor(fields []jsonField, key string) (jsonField, bool) {
	for _, f := range fields {
		if f.name == key {
			return f, true
		}
	}
	for _, f := range fields {
		if strings.EqualFold(f.name, key) {
			return f, true
		}
	}
	return jsonField{}, false
}

// scopeRefusals reports each well-formed requested scope the core's scope list doesn't
// hold, which the core refuses when the admin approves the install.
func scopeRefusals(m Manifest) Refusals {
	var out Refusals
	for i, p := range m.Permissions {
		if (!scopeRe.MatchString(p.Scope) && !viewRe.MatchString(p.Scope)) || forbiddenScopes[p.Scope] || identity.KnownScope(p.Scope) {
			continue
		}
		f := fmt.Sprintf("permissions[%d]", i)
		err := domain.Invalid(f, "unknown scope "+p.Scope+": this core's scope list doesn't hold it")
		out = append(out, Refusal{Kind: KindScope, Code: RefuseUnknownScope, File: ManifestFile, Path: f, Value: p.Scope,
			Message: err.Message, APICode: err.Code, Err: err})
	}
	return out
}

// Accepted is what this core accepts from a module package, for tooling to read.
type Accepted struct {
	// ManifestSchema names the manifest fields this core reads: a digest of
	// ManifestFields, so it changes exactly when a field is added or removed.
	ManifestSchema string `json:"manifest_schema"`
	// ManifestFields is every field path the manifest decoder reads; [*] is any index.
	ManifestFields []string `json:"manifest_fields"`
	// Scopes is the core's scope list; ScopePatterns are the scope forms it accepts by
	// shape (another module's published view); ForbiddenScopes are scopes a module may
	// never request.
	Scopes          []string `json:"scopes"`
	ScopePatterns   []string `json:"scope_patterns"`
	ForbiddenScopes []string `json:"forbidden_scopes"`
	// CoreRecords are the record names data.extends, data.views and data.custom_fields
	// take; CoreRelations are the core tables a migration may reference (by id, ON
	// DELETE RESTRICT).
	CoreRecords   []string `json:"core_records"`
	CoreRelations []string `json:"core_relations"`
}

// Accepts reports what this core accepts, from the same tables its checks use.
func Accepts() Accepted {
	fields := ManifestFields()
	sum := sha256.Sum256([]byte(strings.Join(fields, "\n")))
	var records, relations, forbidden []string
	for r, t := range coreRecords {
		records = append(records, r)
		relations = append(relations, "core."+t)
	}
	for s := range forbiddenScopes {
		forbidden = append(forbidden, s)
	}
	sort.Strings(records)
	sort.Strings(relations)
	sort.Strings(forbidden)
	return Accepted{
		ManifestSchema: "sha256:" + hex.EncodeToString(sum[:]), ManifestFields: fields,
		Scopes: identity.Scopes(), ScopePatterns: []string{ViewScope("<module>", "<view>")}, ForbiddenScopes: forbidden,
		CoreRecords: records, CoreRelations: relations,
	}
}

// ManifestFields lists every field path the manifest decoder reads, sorted, with [*]
// for any list index.
func ManifestFields() []string {
	var out []string
	var walk func(t reflect.Type, path []yamlsubset.Segment)
	walk = func(t reflect.Type, path []yamlsubset.Segment) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		switch t.Kind() {
		case reflect.Struct:
			for _, f := range jsonFields(t) {
				p := append(append([]yamlsubset.Segment(nil), path...), yamlsubset.Segment{Key: f.name})
				out = append(out, yamlsubset.FormatPath(p))
				walk(f.typ, p)
			}
		case reflect.Slice, reflect.Array:
			walk(t.Elem(), append(append([]yamlsubset.Segment(nil), path...), yamlsubset.Segment{IsIndex: true, Any: true}))
		}
	}
	walk(reflect.TypeOf(Manifest{}), nil)
	sort.Strings(out)
	return out
}

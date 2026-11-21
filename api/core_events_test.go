package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Inspect actual emit sites as well as the registry. A newly emitted literal
// action must be regenerated and refused by both public event write paths.
func TestCoreEventRegistryCoversEmitSites(t *testing.T) {
	pattern := regexp.MustCompile(`^[a-z_]+(?:\.[a-z_]+)+$`)
	check := func(name string) {
		if !pattern.MatchString(name) {
			return
		}
		if !CoreEventAction(name) {
			t.Errorf("unregistered emitted action %q", name)
		}
		if reserved, err := CoreEventNamespace(name); err != nil || !reserved {
			t.Errorf("unreserved emitted namespace %q: %v", name, err)
		}
	}
	err := filepath.WalkDir("../internal", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		literal := func(expr ast.Expr) {
			if id, ok := expr.(*ast.Ident); ok && id.Obj != nil {
				if spec, ok := id.Obj.Decl.(*ast.ValueSpec); ok {
					for i, name := range spec.Names {
						if name.Name == id.Name && i < len(spec.Values) {
							expr = spec.Values[i]
						}
					}
				}
			}
			if s, ok := expr.(*ast.BasicLit); ok && s.Kind == token.STRING {
				name, err := strconv.Unquote(s.Value)
				if err == nil {
					check(name)
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.KeyValueExpr:
				if key, ok := x.Key.(*ast.Ident); ok && key.Name == "Action" {
					literal(x.Value)
				}
			case *ast.CompositeLit:
				// Only event literals use Name as an action. Settings also have
				// Name fields and event-related constants, but emit nothing.
				eventFields := func(lit *ast.CompositeLit) {
					for _, field := range lit.Elts {
						if kv, ok := field.(*ast.KeyValueExpr); ok {
							if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Name" {
								literal(kv.Value)
							}
						}
					}
				}
				if typ, ok := x.Type.(*ast.Ident); ok && typ.Name == "Event" {
					eventFields(x)
				}
				if typ, ok := x.Type.(*ast.ArrayType); ok {
					if elem, ok := typ.Elt.(*ast.Ident); ok && elem.Name == "Event" {
						for _, el := range x.Elts {
							if lit, ok := el.(*ast.CompositeLit); ok {
								eventFields(lit)
							}
						}
					}
				}
			case *ast.AssignStmt:
				for i, lhs := range x.Lhs {
					name := ""
					switch v := lhs.(type) {
					case *ast.Ident:
						name = v.Name
					case *ast.SelectorExpr:
						name = v.Sel.Name
					}
					if (name == "Action" || name == "action") && i < len(x.Rhs) {
						literal(x.Rhs[i])
					}
				}
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "changedFields" && len(x.Args) > 3 {
					literal(x.Args[3])
				}
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "event" || sel.Sel.Name == "emit") {
					for _, arg := range x.Args {
						literal(arg)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	names, err := CoreEventNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		check(name)
	}
	for _, namespace := range []string{"core.custom", "calendar.custom"} {
		if reserved, err := CoreEventNamespace(namespace); err != nil || !reserved {
			t.Errorf("legacy namespace %s: %v %v", namespace, reserved, err)
		}
	}
	for _, name := range []string{"custom_field.updated", "conflict.checked", "county.created", "practice_area.created", "referral_source.created", "contact.phone_changed", "matter.party_updated", "practice_area.updated", "county.updated", "referral_source.updated", "contact.email_updated", "contact.address_updated", "trust.entry_reversed"} {
		check(name)
	}
}

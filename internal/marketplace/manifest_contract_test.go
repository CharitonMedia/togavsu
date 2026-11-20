package marketplace

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/CharitonMedia/togavsu/api"
)

// Compare the published description with every runtime reservation, including
// underscore labels that the slug pattern independently rejects. Discover the
// switch cases so a new reservation cannot silently escape the contract check.
func TestModuleManifestReservedIDsContract(t *testing.T) {
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Description string
				}
			}
		}
	}
	if err := json.Unmarshal(api.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	description := spec.Components.Schemas["ModuleManifest"].Properties["id"].Description
	_, list, ok := strings.Cut(description, "source and identity labels are reserved: ")
	if !ok || !strings.HasSuffix(list, ".") {
		t.Fatalf("ModuleManifest.id must publish its reserved list: %q", description)
	}
	published := strings.Split(strings.TrimSuffix(list, "."), ", ")

	source, err := parser.ParseFile(token.NewFileSet(), "manifest.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var reserved []string
	for _, decl := range source.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "ReservedModuleID" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			clause, ok := node.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, expr := range clause.List {
				var id string
				switch value := expr.(type) {
				case *ast.BasicLit:
					id, err = strconv.Unquote(value.Value)
					if err != nil {
						t.Fatal(err)
					}
				case *ast.Ident:
					if value.Name != "CoreSenderID" {
						t.Fatalf("unhandled reservation constant %s; update contract enumeration", value.Name)
					}
					id = CoreSenderID
				default:
					t.Fatalf("unhandled reservation expression %T; update contract enumeration", expr)
				}
				if !ReservedModuleID(id) {
					t.Fatalf("enumerated label %q is not reserved at runtime", id)
				}
				reserved = append(reserved, id)
			}
			return true
		})
	}
	sort.Strings(reserved)
	sort.Strings(published)
	if len(reserved) == 0 || !reflect.DeepEqual(published, reserved) {
		t.Fatalf("ModuleManifest.id reservations %v differ from ReservedModuleID %v", published, reserved)
	}
}

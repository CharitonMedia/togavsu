package settings

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

var envSelectors = map[string]bool{
	"Getenv": true, "LookupEnv": true, "Environ": true, "Setenv": true,
	"Unsetenv": true, "Clearenv": true, "ExpandEnv": true,
}

var durationNames = map[string]bool{
	"Millisecond": true, "Second": true, "Minute": true, "Hour": true,
}

// CheckModule fails when core code reads the environment outside internal/bootstrap
// or embeds a named tunable outside the settings registry and domain defaults.
func CheckModule(root string) error {
	var msgs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		msgs = append(msgs, ScanSource(path, string(src))...)
		return nil
	})
	if err != nil {
		return err
	}
	if len(msgs) > 0 {
		return fmt.Errorf("settings check failed:\n%s", strings.Join(msgs, "\n"))
	}
	return nil
}

// ScanSource applies the AC-030 rules to one Go file.
func ScanSource(path, src string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		return []string{path + ": " + err.Error()}
	}
	var out []string
	aliases := importAliases(file)
	for _, imp := range file.Imports {
		if imp.Name == nil || imp.Name.Name != "." {
			continue
		}
		pkg, err := strconv.Unquote(imp.Path.Value)
		if err == nil && (pkg == "os" || pkg == "syscall") && !envAllowed(path) {
			pos := fset.Position(imp.Pos())
			out = append(out, fmt.Sprintf("%s:%d: dot-import of %s can hide an environment read", pos.Filename, pos.Line, pkg))
		}
	}
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return false
		}
		var parent ast.Node
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		out = append(out, violations(path, fset, n, parent, aliases)...)
		stack = append(stack, n)
		return true
	})
	return out
}

func violations(path string, fset *token.FileSet, n, parent ast.Node, aliases map[string]string) []string {
	var out []string
	if sel, ok := n.(*ast.SelectorExpr); ok {
		ident, ok := sel.X.(*ast.Ident)
		if ok && envSelectors[sel.Sel.Name] && isEnvPackage(ident.Name, aliases) && !envAllowed(path) {
			pos := fset.Position(sel.Pos())
			out = append(out, fmt.Sprintf("%s:%d: environment read %s.%s", pos.Filename, pos.Line, ident.Name, sel.Sel.Name))
		}
		if ok && durationNames[sel.Sel.Name] && isTimePackage(ident.Name, aliases) && !defaultFile(path) {
			pos := fset.Position(sel.Pos())
			out = append(out, fmt.Sprintf("%s:%d: tunable duration %s.%s is not in the settings registry", pos.Filename, pos.Line, ident.Name, sel.Sel.Name))
		}
	}
	if call, ok := n.(*ast.CallExpr); ok {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "ParseDuration" && len(call.Args) == 1 && !defaultFile(path) {
			if ident, ok := sel.X.(*ast.Ident); ok && isTimePackage(ident.Name, aliases) {
				if _, ok := call.Args[0].(*ast.BasicLit); ok {
					pos := fset.Position(call.Pos())
					out = append(out, fmt.Sprintf("%s:%d: tunable duration literal is not in the settings registry", pos.Filename, pos.Line))
				}
			}
		}
	}
	if lit, ok := n.(*ast.BasicLit); ok && !defaultFile(path) {
		if lit.Kind == token.STRING {
			val, err := strconv.Unquote(lit.Value)
			if err == nil && anchored(val) {
				pos := fset.Position(lit.Pos())
				out = append(out, fmt.Sprintf("%s:%d: tunable %q belongs in the settings registry", pos.Filename, pos.Line, val))
			}
		}
		if lit.Kind == token.INT && hardCodedNumber(lit, parent) {
			pos := fset.Position(lit.Pos())
			out = append(out, fmt.Sprintf("%s:%d: tunable number %s belongs in the settings registry", pos.Filename, pos.Line, lit.Value))
		}
	}
	return out
}

func hardCodedNumber(lit *ast.BasicLit, parent ast.Node) bool {
	switch parent.(type) {
	case *ast.ValueSpec, *ast.AssignStmt:
	default:
		return false
	}
	n, err := strconv.ParseInt(lit.Value, 0, 64)
	if err != nil {
		return false
	}
	_, ok := numericDefaults()[n]
	return ok
}

func numericDefaults() map[int64]string {
	out := map[int64]string{}
	for _, def := range definitions {
		if def.Kind != KindInt && def.Kind != KindEnum {
			continue
		}
		n, err := strconv.ParseInt(def.Default, 10, 64)
		if err != nil || n < 25 {
			continue
		}
		out[n] = def.Key
	}
	return out
}

func importAliases(file *ast.File) map[string]string {
	aliases := map[string]string{}
	for _, imp := range file.Imports {
		pkg, err := strconv.Unquote(imp.Path.Value)
		if err != nil || imp.Name != nil && (imp.Name.Name == "." || imp.Name.Name == "_") {
			continue
		}
		local := pkg
		if slash := strings.LastIndex(pkg, "/"); slash >= 0 {
			local = pkg[slash+1:]
		}
		if imp.Name != nil {
			local = imp.Name.Name
		}
		aliases[local] = pkg
	}
	return aliases
}

func isEnvPackage(name string, aliases map[string]string) bool {
	return packagePath(name, aliases) == "os" || packagePath(name, aliases) == "syscall" || (aliases[name] == "" && (name == "os" || name == "syscall"))
}

func isTimePackage(name string, aliases map[string]string) bool {
	return packagePath(name, aliases) == "time" || (aliases[name] == "" && name == "time")
}

func packagePath(name string, aliases map[string]string) string {
	if pkg, ok := aliases[name]; ok {
		return pkg
	}
	return ""
}

func envAllowed(path string) bool {
	return strings.HasSuffix(filepath.ToSlash(filepath.Dir(path)), "internal/bootstrap")
}

// defaultFile is the only place named defaults and their duration expressions may appear.
func defaultFile(path string) bool {
	slash := filepath.ToSlash(path)
	if strings.HasSuffix(slash, "_test.go") {
		return true
	}
	return strings.HasSuffix(slash, "internal/settings/definitions.go") || strings.HasSuffix(slash, "internal/domain/defaults.go")
}

func anchored(value string) bool {
	switch value {
	case domain.LawCraftCommit, domain.AvailableDatesTitle, domain.LabelingModel, domain.NamingPattern, domain.MatterNumberFormat:
		return true
	default:
		return false
	}
}

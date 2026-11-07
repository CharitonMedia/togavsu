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
		out = append(out, violations(path, fset, n, stack, aliases)...)
		stack = append(stack, n)
		return true
	})
	return out
}

func violations(path string, fset *token.FileSet, n ast.Node, ancestors []ast.Node, aliases map[string]string) []string {
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
		if lit.Kind == token.INT && hardCodedNumber(path, lit, ancestors) {
			pos := fset.Position(lit.Pos())
			out = append(out, fmt.Sprintf("%s:%d: tunable number %s belongs in the settings registry", pos.Filename, pos.Line, lit.Value))
		}
	}
	return out
}

func hardCodedNumber(path string, lit *ast.BasicLit, ancestors []ast.Node) bool {
	if !tunableContext(ancestors) {
		return false
	}
	n, err := strconv.ParseInt(lit.Value, 0, 64)
	if err != nil || excludedNumber(path, n, lit, ancestors) {
		return false
	}
	_, ok := numericDefaults()[n]
	return ok
}

// tunableContext is a use that copies a setting into ordinary code: a const or var,
// an assignment, a composite field, a return or call, or either side of a comparison.
// Parentheses and unary plus are transparent. Arithmetic, shifts, and a leading minus
// are fixed calculations, so a cents scale or a bit width is not a tunable.
func tunableContext(ancestors []ast.Node) bool {
	for i := len(ancestors) - 1; i >= 0; i-- {
		switch n := ancestors[i].(type) {
		case *ast.ParenExpr:
			continue
		case *ast.UnaryExpr:
			if n.Op == token.ADD {
				continue
			}
			return false
		case *ast.BinaryExpr:
			return comparisonOp(n.Op)
		case *ast.ValueSpec, *ast.AssignStmt, *ast.KeyValueExpr, *ast.ReturnStmt,
			*ast.CallExpr, *ast.CompositeLit, *ast.SendStmt, *ast.IndexExpr, *ast.SliceExpr,
			*ast.CaseClause:
			return true
		default:
			return false
		}
	}
	return false
}

func comparisonOp(op token.Token) bool {
	switch op {
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
		return true
	default:
		return false
	}
}

func numericDefaults() map[int64]string {
	out := map[int64]string{}
	for _, def := range definitions {
		if def.Kind != KindInt && def.Kind != KindEnum {
			continue
		}
		n, err := strconv.ParseInt(def.Default, 10, 64)
		if err != nil {
			continue
		}
		if _, exists := out[n]; !exists {
			out[n] = def.Key
		}
	}
	return out
}

// excludedNumber lists literals that equal a registry default but are a fixed rule.
// The match is the file plus the value. A sibling file is still checked.
// ponytail: each entry is one file. Ceiling: that file does not gain a real tunable literal.
// Upgrade trigger: a setting is hardcoded in one of these files. Drop that file's entry.
func excludedNumber(path string, n int64, lit ast.Expr, ancestors []ast.Node) bool {
	slash := filepath.ToSlash(path)
	switch {
	case n == 0 && zeroValueUse(ancestors):
		// A comparison, return, call, or index of 0 is Go's zero value, not the markup default.
		// const markup = 0 and Filter{Limit: 0} stay visible to the check.
		return true
	case strconvSpec(lit, ancestors):
		// ParseInt's base and bit size are not activity.page_size or any other setting.
		return true
	case strings.HasSuffix(slash, "internal/settings/check.go"):
		// This file lists the defaults it matches, so those integers are the rule, not a bypass.
		return true
	case strings.HasSuffix(slash, "internal/domain/id.go"):
		// Every integer here is a UUID byte index, bit width, or variant check.
		return true
	case strings.HasSuffix(slash, "internal/domain/money.go"):
		// 2 is the cent fraction width. 8 is the MinInt64 fractional boundary. 1 is the positive sign.
		// 100 stays legal only as arithmetic; a bare 100 in this file is still reported.
		return n == 1 || n == 2 || n == 8
	case strings.HasSuffix(slash, "internal/db/seed.go"):
		// Synthetic rows use clock fields and sample counts. They are not live limits.
		return true
	case strings.HasSuffix(slash, "internal/db/goose_store.go"):
		// Version 0 is the ledger baseline, not a percent default.
		return n == 0
	case strings.HasSuffix(slash, "internal/db/migrate.go"):
		// The scan index starts at 0. Versions and placeholder numbers start at 1.
		return n == 0 || n == 1
	case strings.HasSuffix(slash, "internal/settings/registry.go"):
		// Confidence is a fraction from 0 to 1. That 1 is not the reminder-day default.
		return n == 1 && comparisonUse(ancestors)
	case strings.HasSuffix(slash, "internal/bootstrap/config.go"):
		// RunFrom returns process status 2. Success status 0 is the zero-value rule above.
		return n == 2 && returnUse(ancestors)
	default:
		return false
	}
}

// strconvSpec reports a base or bit-size argument of strconv.ParseInt, ParseUint, or ParseFloat.
func strconvSpec(lit ast.Expr, ancestors []ast.Node) bool {
	for _, parent := range ancestors {
		call, ok := parent.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		name, ok := sel.X.(*ast.Ident)
		if !ok || name.Name != "strconv" {
			continue
		}
		switch sel.Sel.Name {
		case "ParseInt", "ParseUint":
			if exprIs(call.Args[1], lit) || len(call.Args) > 2 && exprIs(call.Args[2], lit) {
				return true
			}
		case "ParseFloat":
			if exprIs(call.Args[1], lit) {
				return true
			}
		}
	}
	return false
}

func exprIs(expr, lit ast.Expr) bool {
	for {
		if expr == lit {
			return true
		}
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return false
		}
		expr = paren.X
	}
}

func comparisonUse(ancestors []ast.Node) bool {
	for i := len(ancestors) - 1; i >= 0; i-- {
		switch n := ancestors[i].(type) {
		case *ast.ParenExpr:
			continue
		case *ast.BinaryExpr:
			return comparisonOp(n.Op)
		default:
			return false
		}
	}
	return false
}

func zeroValueUse(ancestors []ast.Node) bool {
	for i := len(ancestors) - 1; i >= 0; i-- {
		switch n := ancestors[i].(type) {
		case *ast.ParenExpr:
			continue
		case *ast.UnaryExpr:
			if n.Op == token.ADD {
				continue
			}
			return false
		case *ast.BinaryExpr:
			return comparisonOp(n.Op)
		case *ast.ReturnStmt, *ast.CallExpr, *ast.IndexExpr, *ast.SliceExpr:
			return true
		default:
			return false
		}
	}
	return false
}

func returnUse(ancestors []ast.Node) bool {
	for i := len(ancestors) - 1; i >= 0; i-- {
		switch ancestors[i].(type) {
		case *ast.ParenExpr:
			continue
		case *ast.ReturnStmt:
			return true
		default:
			return false
		}
	}
	return false
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

// defaultFile is where a registry default, a domain default, or the migration-version
// allocation may appear as a literal. ranges.go publishes the version bands; those
// integers are not settings.
// ponytail: the exemption is that one file. Ceiling: it stays an allocation table.
// Upgrade trigger: a tunable literal is added to ranges.go. Remove the exemption.
func defaultFile(path string) bool {
	slash := filepath.ToSlash(path)
	if strings.HasSuffix(slash, "_test.go") {
		return true
	}
	switch {
	case strings.HasSuffix(slash, "internal/settings/definitions.go"):
		return true
	case strings.HasSuffix(slash, "internal/domain/defaults.go"):
		return true
	case strings.HasSuffix(slash, "internal/db/ranges.go"):
		return true
	default:
		return false
	}
}

func anchored(value string) bool {
	switch value {
	case domain.LawCraftCommit, domain.AvailableDatesTitle, domain.LabelingModel, domain.NamingPattern, domain.MatterNumberFormat:
		return true
	default:
		return false
	}
}

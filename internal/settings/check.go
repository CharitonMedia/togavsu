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
// or places an unproven numeric configuration literal outside the settings registry.
// It is a conservative source heuristic, not acceptance of AC-030. Arithmetic and the
// enumerated fixed-site functions are allowed. A number that is not today's default is
// still reported.
func CheckModule(root string) error {
	var msgs []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// These exact module-relative packages belong to the separate
			// supervisor executable, not the core checked by AC-030.
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if rel == filepath.Join("cmd", "supervisor") || rel == filepath.Join("internal", "supervisor") {
				return filepath.SkipDir
			}
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
		if ok && durationNames[sel.Sel.Name] && isTimePackage(ident.Name, aliases) && !defaultFile(path) && !fixedSite(path, enclosingFunc(ancestors)) {
			pos := fset.Position(sel.Pos())
			out = append(out, fmt.Sprintf("%s:%d: tunable duration %s.%s is not in the settings registry", pos.Filename, pos.Line, ident.Name, sel.Sel.Name))
		}
	}
	if call, ok := n.(*ast.CallExpr); ok {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "ParseDuration" && !defaultFile(path) && !fixedSite(path, enclosingFunc(ancestors)) && oneBasicArg(call.Args) {
			if ident, ok := sel.X.(*ast.Ident); ok && isTimePackage(ident.Name, aliases) {
				pos := fset.Position(call.Pos())
				out = append(out, fmt.Sprintf("%s:%d: tunable duration literal is not in the settings registry", pos.Filename, pos.Line))
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
		if lit.Kind == token.INT && hardCodedNumber(path, lit, ancestors, aliases) {
			pos := fset.Position(lit.Pos())
			out = append(out, fmt.Sprintf("%s:%d: tunable number %s is a configuration literal outside the settings registry", pos.Filename, pos.Line, lit.Value))
		}
	}
	return out
}

func hardCodedNumber(path string, lit *ast.BasicLit, ancestors []ast.Node, aliases map[string]string) bool {
	if _, err := strconv.ParseInt(lit.Value, 0, 64); err != nil {
		return false
	}
	if !configUse(lit, ancestors, aliases) {
		return false
	}
	if idiom(lit, ancestors) || fixedSite(path, enclosingFunc(ancestors)) || validationBound(path, lit, ancestors) {
		return false
	}
	return true
}

// configUse is a position where a numeric literal configures behavior, whatever its value.
// Parentheses and unary plus are transparent. Arithmetic, shifts, and a leading minus are
// fixed calculations, not configuration.
func configUse(lit *ast.BasicLit, ancestors []ast.Node, aliases map[string]string) bool {
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
			*ast.CallExpr, *ast.CompositeLit, *ast.SendStmt, *ast.CaseClause:
			if clockCall(n, aliases) || strconvSpec(lit, ancestors) || parserMode(lit, ancestors) || mathInfSign(lit, ancestors) {
				return false
			}
			return true
		case *ast.IndexExpr, *ast.SliceExpr:
			return lit.Value != "0"
		default:
			return false
		}
	}
	return false
}

func oneBasicArg(args []ast.Expr) bool {
	var found bool
	for _, arg := range args {
		if _, ok := arg.(*ast.BasicLit); !ok || found {
			return false
		}
		found = true
	}
	return found
}

func comparisonOp(op token.Token) bool {
	switch op {
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
		return true
	default:
		return false
	}
}

// idiom is a Go zero, error, or loop shape. It is not a firm setting.
// A named limit of 0 or 1, and a comparison such as failures >= 0, are not idioms.
func idiom(lit *ast.BasicLit, ancestors []ast.Node) bool {
	if lenBound(lit, ancestors) || loopZero(lit, ancestors) || zeroErrorReturn(lit, ancestors) || zeroConversion(lit, ancestors) || zeroMake(lit, ancestors) {
		return true
	}
	return false
}

// lenBound allows only empty-collection checks: len/cap compared with 0, or < 1 / >= 1.
// len(xs) == 2 and len(xs) == 1 stay reported. Exactly-one can be a configurable limit.
func lenBound(lit *ast.BasicLit, ancestors []ast.Node) bool {
	bin, ok := comparisonParent(ancestors)
	if !ok || !otherIsLenCap(bin, lit) {
		return false
	}
	switch lit.Value {
	case "0":
		return true
	case "1":
		return bin.Op == token.LSS || bin.Op == token.GEQ
	default:
		return false
	}
}

func comparisonParent(ancestors []ast.Node) (*ast.BinaryExpr, bool) {
	for i := len(ancestors) - 1; i >= 0; i-- {
		switch n := ancestors[i].(type) {
		case *ast.ParenExpr:
			continue
		case *ast.BinaryExpr:
			if comparisonOp(n.Op) {
				return n, true
			}
			return nil, false
		default:
			return nil, false
		}
	}
	return nil, false
}

func otherIsLenCap(bin *ast.BinaryExpr, lit *ast.BasicLit) bool {
	other := bin.X
	if exprIs(bin.X, lit) {
		other = bin.Y
	}
	call, ok := other.(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := call.Fun.(*ast.Ident)
	return ok && (ident.Name == "len" || ident.Name == "cap")
}

func loopZero(lit *ast.BasicLit, ancestors []ast.Node) bool {
	if lit.Value != "0" {
		return false
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		loop, ok := ancestors[i].(*ast.ForStmt)
		if !ok {
			continue
		}
		if as, ok := loop.Init.(*ast.AssignStmt); ok {
			for _, rhs := range as.Rhs {
				if exprIs(rhs, lit) {
					return true
				}
			}
		}
		cond := loop.Cond
		for {
			paren, ok := cond.(*ast.ParenExpr)
			if !ok {
				break
			}
			cond = paren.X
		}
		if bin, ok := cond.(*ast.BinaryExpr); ok && comparisonOp(bin.Op) && (exprIs(bin.X, lit) || exprIs(bin.Y, lit)) && stepsIndex(loop.Post) {
			return true
		}
	}
	return false
}

func stepsIndex(post ast.Stmt) bool {
	switch n := post.(type) {
	case *ast.IncDecStmt:
		_, ok := n.X.(*ast.Ident)
		return ok
	case *ast.AssignStmt:
		var one, extra bool
		for _, lhs := range n.Lhs {
			if _, ok := lhs.(*ast.Ident); !ok {
				return false
			}
			if one {
				extra = true
			}
			one = true
		}
		return one && !extra
	default:
		return false
	}
}

// zeroErrorReturn allows return 0, err. A bare return 0 and return 1, err stay reported.
func zeroErrorReturn(lit *ast.BasicLit, ancestors []ast.Node) bool {
	if lit.Value != "0" {
		return false
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		switch n := ancestors[i].(type) {
		case *ast.ParenExpr:
			continue
		case *ast.ReturnStmt:
			var found, others bool
			for _, result := range n.Results {
				if exprIs(result, lit) {
					found = true
					continue
				}
				others = true
			}
			return found && others
		default:
			return false
		}
	}
	return false
}

func soleArg(call *ast.CallExpr) ast.Expr {
	var one ast.Expr
	for _, arg := range call.Args {
		if one != nil {
			return nil
		}
		one = arg
	}
	return one
}

func zeroMake(lit *ast.BasicLit, ancestors []ast.Node) bool {
	if lit.Value != "0" || len(ancestors) == 0 {
		return false
	}
	call, ok := ancestors[len(ancestors)-1].(*ast.CallExpr)
	if !ok {
		return false
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "make" {
		return false
	}
	var length ast.Expr
	seenType := false
	for _, arg := range call.Args {
		if !seenType {
			seenType = true
			continue
		}
		length = arg
		break
	}
	return length != nil && exprIs(length, lit)
}

func zeroConversion(lit *ast.BasicLit, ancestors []ast.Node) bool {
	if lit.Value != "0" || len(ancestors) == 0 {
		return false
	}
	call, ok := ancestors[len(ancestors)-1].(*ast.CallExpr)
	if !ok || !exprIs(soleArg(call), lit) {
		return false
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	switch ident.Name {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "byte", "rune":
		return true
	default:
		return false
	}
}

func clockCall(n ast.Node, aliases map[string]string) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || !isTimePackage(ident.Name, aliases) {
		return false
	}
	switch sel.Sel.Name {
	case "Date", "Unix", "UnixMilli", "UnixMicro":
		return true
	default:
		return false
	}
}

func parserMode(lit ast.Expr, ancestors []ast.Node) bool {
	for _, parent := range ancestors {
		call, ok := parent.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "ParseFile" {
			continue
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "parser" {
			continue
		}
		var last ast.Expr
		for _, arg := range call.Args {
			last = arg
		}
		if last != nil && exprIs(last, lit) {
			return true
		}
	}
	return false
}

func enclosingFunc(ancestors []ast.Node) string {
	for i := len(ancestors) - 1; i >= 0; i-- {
		fn, ok := ancestors[i].(*ast.FuncDecl)
		if ok {
			return fn.Name.Name
		}
	}
	return ""
}

// fixedSite is one function whose integer literals are a fixed computational rule.
// A different function in the same file is still checked. The listed function can hide
// a later tunable; that ceiling is recorded in PONYTAIL-DEBT.md.
// ponytail: enumerated functions only. Upgrade path: delete the function from this switch.
// trigger: a configurable literal is added inside one of these functions.
func fixedSite(path, fn string) bool {
	if fn == "" {
		return false
	}
	slash := filepath.ToSlash(path)
	switch {
	case strings.HasSuffix(slash, "internal/domain/money.go"):
		// Cent scale, two fractional digits, and the int64 cent bounds.
		switch fn {
		case "ParseCents", "combineCents", "String", "Sub", "addCents", "parsePositive":
			return true
		}
	case strings.HasSuffix(slash, "internal/domain/id.go"):
		// UUIDv7 byte indexes, variant bits, text groups, and the synthetic system-user id.
		switch fn {
		case "NewID", "ComposeV7", "ParseID", "Time", "formatUUID", "systemUserID":
			return true
		}
	case strings.HasSuffix(slash, "internal/db/seed.go"):
		// Synthetic row builders and the boolean-as-integer helper. Not firm limits.
		switch fn {
		case "ApplyReference", "ApplySynthetic", "insertSynthetic", "sid", "bit":
			return true
		}
	case strings.HasSuffix(slash, "internal/db/ranges.go"):
		// Version allocation bands and the "exactly one band" invariant.
		switch fn {
		case "MigrationRanges", "ValidateMigrationVersions", "TaskForVersion":
			return true
		}
	case strings.HasSuffix(slash, "internal/db/migrate.go"):
		// Positive migration versions, placeholder numbers, the single held connection,
		// and the first-match count passed to strings.Replace.
		switch fn {
		case "LoadFS", "Apply", "Rebind", "migrateOnHeldConn", "gooseNoTransaction":
			return true
		}
	case strings.HasSuffix(slash, "internal/db/goose_store.go"):
		// Ledger baseline version is the integer zero row, not a percent.
		switch fn {
		case "ensureBaseline", "Insert", "TableExists", "check":
			return true
		}
	case strings.HasSuffix(slash, "internal/db/setup.go"):
		// Setup is complete when at least one admin exists. That zero is existence, not a minimum setting.
		return fn == "ReadSetup"
	case strings.HasSuffix(slash, "internal/db/settings_store.go"):
		// RowsAffected of zero means the setting key is absent, so Put inserts it.
		return fn == "Put"
	case strings.HasSuffix(slash, "internal/bootstrap/config.go"):
		// Process status 0 and 2, the 32-byte encryption key, and the 16-character supervisor secret.
		// Those exist before the registry can be read. A different function in this file is still reported.
		return fn == "RunFrom" || fn == "LoadFrom"
	case strings.HasSuffix(slash, "internal/settings/seal.go"):
		// AES-256-GCM keys are 32 bytes. That width is not a firm setting.
		return fn == "NewSealingStore"
	case strings.HasSuffix(slash, "internal/identity/params.go"):
		// Argon2id, RFC 6238 TOTP, the API password minimum, the AES-256 width,
		// the one-proof rule, and the minute named by security.api_rate_per_minute.
		switch fn {
		case "argonTime", "argonMemory", "argonThreads", "argonKeyLen", "saltLen",
			"totpBytes", "totpPeriod", "totpDigits", "totpSkew", "hotpDigits",
			"recoveryCount", "recoveryCodeBytes", "tokenBytes", "minPassword", "aesKey",
			"perMinuteWindow", "singleFactor":
			return true
		}
	case strings.HasSuffix(slash, "internal/identity/totp.go"):
		// RFC 4226 dynamic truncation and the step counter.
		switch fn {
		case "hotp", "acceptedStep":
			return true
		}
	case strings.HasSuffix(slash, "internal/identity/password.go"):
		// PHC field positions and the constant-time success value.
		switch fn {
		case "parsePHC", "constantTimeEqual":
			return true
		}
	case strings.HasSuffix(slash, "internal/identity/token.go"):
		// Challenge token parts and recovery-code groups.
		switch fn {
		case "openChallenge", "grouped":
			return true
		}
	case strings.HasSuffix(slash, "internal/identity/pgstore.go"):
		// Boolean columns are stored as 0 and 1.
		return fn == "flag"
	default:
		return false
	}
	return false
}

// validationBound allows comparisons inside validateSpecial. They bound a value's format
// (a confidence fraction, a day list up to 365). A const or a call in that function is still reported.
func validationBound(path string, lit *ast.BasicLit, ancestors []ast.Node) bool {
	if !strings.HasSuffix(filepath.ToSlash(path), "internal/settings/registry.go") {
		return false
	}
	if enclosingFunc(ancestors) != "validateSpecial" {
		return false
	}
	_, ok := comparisonParent(ancestors)
	return ok
}

func mathInfSign(lit ast.Expr, ancestors []ast.Node) bool {
	basic, ok := lit.(*ast.BasicLit)
	if !ok || basic.Value != "0" {
		return false
	}
	for _, parent := range ancestors {
		call, ok := parent.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "IsInf" {
			continue
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "math" {
			continue
		}
		var second ast.Expr
		seen := false
		extra := false
		for _, arg := range call.Args {
			if !seen {
				seen = true
				continue
			}
			if second != nil {
				extra = true
				break
			}
			second = arg
		}
		if !extra && second != nil && exprIs(second, lit) {
			return true
		}
	}
	return false
}

// strconvSpec reports a base or bit-size argument of strconv.ParseInt, ParseUint, or ParseFloat.
func strconvSpec(lit ast.Expr, ancestors []ast.Node) bool {
	for _, parent := range ancestors {
		call, ok := parent.(*ast.CallExpr)
		if !ok {
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
		case "ParseInt", "ParseUint", "ParseFloat":
		default:
			continue
		}
		seen := false
		for _, arg := range call.Args {
			if !seen {
				seen = true
				continue
			}
			if exprIs(arg, lit) {
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

func importAliases(file *ast.File) map[string]string {
	aliases := map[string]string{}
	for _, imp := range file.Imports {
		pkg, err := strconv.Unquote(imp.Path.Value)
		if err != nil || imp.Name != nil && (imp.Name.Name == "." || imp.Name.Name == "_") {
			continue
		}
		local := pkg
		if strings.Contains(pkg, "/") {
			local = pkg[strings.LastIndex(pkg, "/")+1:]
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

// defaultFile is only the registry and domain default declarations, plus tests.
// Other files are scanned. Fixed computational rules are function-scoped in fixedSite,
// not whole-file allowlists. ranges.go is not exempt: only MigrationRanges and its
// invariant helpers are.
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

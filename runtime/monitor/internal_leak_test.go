package monitor_test

// An exported signature that names a type from .../internal/... is not callable
// by a consumer: `use of internal package … not allowed`. In-module code can
// import internal/ freely, so an in-module test of such a call COMPILES — which
// is precisely why the leak shipped. stats_collector_test.go has been passing
// observability.WithMeterProvider(mp) to both collectors since they were
// written, and proves nothing about external reachability.
//
// So the guard is structural rather than behavioural: derive the offending set
// from the module's own sources and require it to be empty. It fails on the
// signature, not on a hard-coded count, so it stays true as the module grows and
// closes the whole class rather than this one instance. The AST-walk technique
// is the one already used by engine/state_recent_compensation_cmd_ids_test.go.
//
// It lives in runtime/monitor because that is the package the rule was first
// broken in; it is a module-wide assertion and may be moved without changing it.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// moduleRoot walks up from the test's working directory to the directory
// holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "walked past the filesystem root without finding go.mod")
		dir = parent
	}
}

// internalImportIdents returns the file-local identifiers (import alias, or the
// final path element when unaliased) that refer to a package under internal/.
func internalImportIdents(f *ast.File) map[string]string {
	idents := make(map[string]string)
	for _, spec := range f.Imports {
		path := strings.Trim(spec.Path.Value, `"`)
		if !strings.Contains(path, "/internal/") && !strings.HasSuffix(path, "/internal") {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		idents[name] = path
	}
	return idents
}

// receiverTypeName returns the receiver's type name for a method, normalised
// through the shapes a receiver can take: *T, T[X] and T[X, Y] all yield "T".
// The second result is false for a plain func, which has no receiver.
//
// It is shared by reachableExported and declKey on purpose. The two must agree
// about what a receiver IS, or a method could be judged reachable under one
// spelling and keyed under another.
func receiverTypeName(fn *ast.FuncDecl) (string, bool) {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return "", false
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	switch idx := recv.(type) {
	case *ast.IndexExpr: // generic receiver with one type parameter, Foo[T]
		recv = idx.X
	case *ast.IndexListExpr: // ...and with several, Foo[T, U]
		recv = idx.X
	}
	ident, ok := recv.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

// reachableExported reports whether fn is callable by an importer of the
// package: an exported func, or an exported method on an exported receiver.
func reachableExported(fn *ast.FuncDecl) bool {
	if !fn.Name.IsExported() {
		return false
	}
	recv, isMethod := receiverTypeName(fn)
	if !isMethod {
		return true
	}
	return ast.IsExported(recv)
}

// declKey builds the allowlist key for a declaration.
//
// A plain func, a type, a var or a const keys as "<path>:<Name>"; a method keys
// as "<path>:(<Recv>).<Name>". The plain-func spelling is deliberately
// unchanged, so tightening this key moved no allowlist data.
//
// The receiver is in the key because without it a method INHERITS a func's
// exemption: both used to key as "<path>:<Name>", so an allowlisted func Foo
// silently exempted a method (T).Foo declared in the same file. That is a guard
// failing open, and it is what #148 fixed.
func declKey(rel, symbol string) string {
	return filepath.ToSlash(rel) + ":" + symbol
}

// funcSymbol renders a FuncDecl's symbol as it appears in a key and in an
// offender line: "Foo" for a func, "(T).Foo" for a method on T.
func funcSymbol(fn *ast.FuncDecl) string {
	if recv, isMethod := receiverTypeName(fn); isMethod {
		return "(" + recv + ")." + fn.Name.Name
	}
	return fn.Name.Name
}

// embeddedFieldName returns the field name an embedded type contributes, which
// is the final identifier of its type: *internalpkg.T embeds as "T".
func embeddedFieldName(expr ast.Expr) (string, bool) {
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.SelectorExpr:
			return e.Sel.Name, true
		case *ast.Ident:
			return e.Name, true
		default:
			return "", false
		}
	}
}

// consumerReachableTypeExprs narrows a type definition to the parts a consumer
// can actually reach, and it is where the property has to be stated precisely
// rather than generously.
//
// "Names an internal type anywhere in its definition" is too large a domain if
// read literally: a struct's UNEXPORTED fields are how a package holds internal
// state, and reading them reports 13 offender lines across 8 correct
// declarations in this module — Authorizer.inner, OutboxStatsCollector.tel and
// CallNotifier.logOpt among them. A consumer cannot reach those, so they are
// not leaks. Too large a domain is a measurement error exactly as too small a
// one is.
//
// So: for a struct, only exported fields; for an interface, only exported
// methods and embedded interfaces; for anything else — a func type, a named
// type, a map, a slice — the whole expression, because the underlying type IS
// the reachable surface. That last case is the one that matters in practice:
// casbinauthz.DBOption is `func(*internalcasbin.DBConfig)`, and a consumer
// cannot write their own option without naming the internal config.
func consumerReachableTypeExprs(t ast.Expr) []ast.Expr {
	switch typ := t.(type) {
	case *ast.StructType:
		var out []ast.Expr
		for _, field := range typ.Fields.List {
			if len(field.Names) == 0 {
				if name, ok := embeddedFieldName(field.Type); ok && ast.IsExported(name) {
					out = append(out, consumerReachableTypeExprs(field.Type)...)
				}
				continue
			}
			for _, name := range field.Names {
				if name.IsExported() {
					out = append(out, consumerReachableTypeExprs(field.Type)...)
					break
				}
			}
		}
		return out
	case *ast.InterfaceType:
		var out []ast.Expr
		for _, method := range typ.Methods.List {
			if len(method.Names) == 0 { // an embedded interface
				out = append(out, consumerReachableTypeExprs(method.Type)...)
				continue
			}
			for _, name := range method.Names {
				if name.IsExported() {
					out = append(out, consumerReachableTypeExprs(method.Type)...)
					break
				}
			}
		}
		return out
	// The composite arms below RECURSE rather than returning the wrapper whole.
	// Taking it whole made the boundary inconsistent with this function's own
	// rationale: an inline anonymous nested struct behind an exported field —
	// `struct{ Pub struct{ tel secret.T } }` — had its UNEXPORTED inner field
	// read, which is exactly the over-reporting the exported-fields-only rule
	// exists to stop. A pointer, slice, map or channel to such a struct had the
	// same shape.
	case *ast.StarExpr:
		return consumerReachableTypeExprs(typ.X)
	case *ast.ParenExpr:
		return consumerReachableTypeExprs(typ.X)
	case *ast.ArrayType:
		return consumerReachableTypeExprs(typ.Elt)
	case *ast.Ellipsis:
		return consumerReachableTypeExprs(typ.Elt)
	case *ast.ChanType:
		return consumerReachableTypeExprs(typ.Value)
	case *ast.MapType:
		return append(consumerReachableTypeExprs(typ.Key), consumerReachableTypeExprs(typ.Value)...)
	default:
		// A func type, a named type, a selector: here the whole expression IS
		// the reachable surface, which is the case casbinauthz.DBOption proves.
		return []ast.Expr{t}
	}
}

// knownOpenInternalLeaks are offenders this test reports but tolerates, each
// tracked as its own backlog item and owned by another package's delivery.
// Keyed by declKey — "<path>:<Name>" for a func, type, var or const, and
// "<path>:(<Recv>).<Name>" for a method — so a line move does not silence the
// guard and a method does not inherit a same-named func's exemption.
//
// It is SELF-CLEANING: an entry that no longer matches any offender fails the
// test, so a fixed leak cannot leave a stale exemption behind for the next one
// to hide under.
// openLeak is a tolerated offender. BOTH fields matter: the import path is
// checked, exactly as intentionalCapabilitySeals checks its own, because
// tolerance was granted for a SPECIFIC known leak and not for the symbol in
// perpetuity. Keying on the symbol alone fails open in the way the seals
// comment below already describes — a future, unrelated internal type added to
// the same signature would inherit the exemption silently — and that argument
// applies verbatim here.
type openLeak struct {
	importPath string // the internal package this signature is expected to name
	why        string // carried into the failure message; genuinely useful there
}

var knownOpenInternalLeaks = map[string]openLeak{
	"persistence/scheduler_locker.go:NewSchedulerLocker": {
		importPath: "github.com/kartaladev/wrkflw/internal/persistence/dialect",
		why: "takes an internal dialect.Locker parameter — " +
			"found by this guard, outside runtime/, not fixed here; the doc comment even invites a consumer to " +
			"\"bring your own dialect.Locker\", which no consumer can name",
	},
	// Surfaced by widening the walk to *ast.GenDecl (#148): the guard could not
	// see a type declaration at all before that, so this shipped unseen.
	//
	// It is open debt, NOT a capability seal. FromDB and its four With* options
	// are perfectly callable and the shipped options work; what a consumer
	// cannot do is write their OWN DBOption, because that means naming
	// github.com/kartaladev/wrkflw/internal/authz/casbin's DBConfig. The
	// un-extensibility is a side effect of hiding the config struct, not the
	// point of it — the same shape as NewSchedulerLocker above. The fix belongs
	// to whoever owns casbinauthz: give DBOption a public config type.
	"casbinauthz/casbinauthz.go:DBOption": {
		importPath: "github.com/kartaladev/wrkflw/internal/authz/casbin",
		why: "type DBOption func(*internalcasbin.DBConfig) — consumers can use the shipped With* " +
			"options but cannot write their own",
	},
}

// intentionalCapabilitySeals are exported signatures that name an internal type
// ON PURPOSE. They are the opposite of knownOpenInternalLeaks: not debt to be
// paid down, but the mechanism working as designed, and "fixing" one by the
// remedy this guard suggests would break the thing it protects.
//
// A capability seal is an internal token type in an exported signature, used so
// that the function is callable from inside its own subtree and nowhere else.
// Being uncallable by a consumer is the objective, not the accident.
//
// Keyed by declKey — "<path>:<Name>", or "<path>:(<Recv>).<Name>" for a method —
// and valued with the internal import path the signature is expected to name.
// BOTH must match for the exemption to apply. Keying on the symbol alone would
// fail open twice over: a future, unrelated internal type added to the same
// signature would inherit the exemption silently, and so would a same-named
// method on any receiver in the same file. It is also SELF-CLEANING like the map
// above — an entry matching no offender fails the test.
var intentionalCapabilitySeals = map[string]string{
	// The kindreg.Token parameter is what makes model.RegisterKind uncallable by
	// a consumer, which is the point of it: Node is a closed set (Refs #46).
	"definition/model/registry.go:RegisterKind": "github.com/kartaladev/wrkflw/definition/internal/kindreg",
}

// internalLeakScan is one walk's result: the offenders found, plus which
// allowlist entries were matched, which is what makes both maps self-cleaning.
type internalLeakScan struct {
	offenders []string
	seenKnown map[string]bool
	seenSeal  map[string]bool
}

// scanInternalLeaks walks every non-internal, non-test Go file under root and
// reports every consumer-reachable exported declaration that names a type from
// an internal/ package.
//
// It takes the two allowlists as parameters rather than reading the package
// globals so that a test can drive it over a synthetic fixture with allowlists
// of its own. That is the only reason it is a function and not the body of
// TestNoExportedSignatureNamesAnInternalType, which is now a thin caller.
func scanInternalLeaks(root string, known map[string]openLeak, seals map[string]string) (internalLeakScan, error) {
	fset := token.NewFileSet()
	scan := internalLeakScan{
		seenKnown: make(map[string]bool, len(known)),
		seenSeal:  make(map[string]bool, len(seals)),
	}

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch name := d.Name(); {
			case name == "internal", name == "testdata", name == "vendor", strings.HasPrefix(name, "."):
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		f, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}
		internals := internalImportIdents(f)
		if len(internals) == 0 {
			// An exported declaration can only name an internal type through a
			// qualified selector, which requires an import. A file with no
			// internal import cannot contain the defect, so skipping it costs
			// no coverage. The one exception is the alias hole recorded in
			// TestNoExportedSignatureNamesAnInternalType's doc comment.
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}

		// inspect reports every internal type named anywhere inside expr, which
		// is what makes this a test of the PROPERTY rather than of a list of
		// shapes: parameter, result, method set, field and underlying type are
		// all just positions within some declaration's type expression.
		inspect := func(expr ast.Expr, symbol string, pos token.Pos, kind string) {
			if expr == nil {
				return
			}
			ast.Inspect(expr, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				importPath, isInternal := internals[pkgIdent.Name]
				if !isInternal {
					return true
				}
				key := declKey(rel, symbol)
				// Exempt only for the internal package the tolerance was granted
				// for; any other internal type at the same symbol is still an
				// offender. Same rule as the seals below, for the same reason.
				if leak, isKnown := known[key]; isKnown && leak.importPath == importPath {
					scan.seenKnown[key] = true
					return true
				}
				// A deliberate capability seal is exempt only when it names the
				// exact internal package it was sanctioned for; any other
				// internal type at the same symbol is still an offender.
				if want, sealed := seals[key]; sealed && want == importPath {
					scan.seenSeal[key] = true
					return true
				}
				scan.offenders = append(scan.offenders, fmt.Sprintf("%s:%d [%s] %s names %s.%s (%s)",
					filepath.ToSlash(rel), fset.Position(pos).Line, kind, symbol,
					pkgIdent.Name, sel.Sel.Name, importPath))
				return true
			})
		}

		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !reachableExported(d) {
					continue
				}
				inspect(d.Type, funcSymbol(d), d.Pos(), "func")
			case *ast.GenDecl:
				// Type, var and const declarations were invisible to this guard
				// until #148: it kept only *ast.FuncDecl and skipped every
				// *ast.GenDecl, so an exported interface method set, struct
				// field, type definition or typed var could name an internal
				// type unseen. That was the guard failing open, and it was
				// hiding a real offender — see casbinauthz.DBOption below.
				for _, spec := range d.Specs {
					switch sp := spec.(type) {
					case *ast.TypeSpec:
						if !sp.Name.IsExported() {
							continue
						}
						// TypeParams is a SEPARATE go/ast field and is not
						// reachable from sp.Type, so a constraint naming an
						// internal type was invisible: `type G[T secret.C] …`
						// slipped through while `func F[T secret.C]()` was
						// caught, because FuncDecl.Type is an *ast.FuncType
						// whose TypeParams does get walked. An asymmetry
						// introduced by the fix for a blind spot is still one.
						if sp.TypeParams != nil {
							for _, tp := range sp.TypeParams.List {
								inspect(tp.Type, sp.Name.Name, sp.Pos(), "type")
							}
						}
						for _, expr := range consumerReachableTypeExprs(sp.Type) {
							inspect(expr, sp.Name.Name, sp.Pos(), "type")
						}
					case *ast.ValueSpec:
						// DELIBERATELY sp.Type and never sp.Values. A value
						// re-export — `var ErrX = internalpkg.ErrX` — is not a
						// leak: the exported symbol is fully usable through
						// errors.Is, and the consumer never needs to name the
						// internal type. Inspecting the initialiser instead of
						// the declared type reports three such sentinels in this
						// module as offenders, and "fixing" them would break
						// three correct re-exports.
						if sp.Type == nil {
							continue
						}
						for _, name := range sp.Names {
							if !name.IsExported() {
								continue
							}
							inspect(sp.Type, name.Name, name.Pos(), "value")
						}
					}
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		return internalLeakScan{}, walkErr
	}
	return scan, nil
}

// TestNoExportedSignatureNamesAnInternalType walks every non-internal,
// non-test Go file in the module and fails if a consumer-reachable exported
// declaration names a type from an internal/ package.
//
// THE PROPERTY, which is what this guard means — the shapes below are only how
// it is tested:
//
//	No exported declaration — func, method, interface, struct, or named type —
//	may name an internal/ type anywhere a consumer can reach: parameter, result,
//	exported method set, exported field, or underlying type.
//
// "Anywhere a consumer can reach" and not "anywhere in its definition": an
// UNEXPORTED struct field or interface method is how a package holds internal
// state. Reading those reports 13 offender lines across 8 correct declarations
// in this module — Authorizer.inner, OutboxStatsCollector.tel and
// CallNotifier.logOpt among them. CallNotifier.cl is NOT an example: it is
// kernel.CallLinkStore, and runtime/kernel is public, so it can never be
// reported. Too large a domain is a measurement error exactly as too small a
// one is, and this guard has now been wrong in both directions. See
// consumerReachableTypeExprs.
//
// WHAT MAKES THIS TEST FAIL — and it is no longer anything in the module. It
// was written against runtime/monitor/stats_collector.go, whose two
// constructors took `opts ...observability.Option` from
// github.com/kartaladev/wrkflw/internal/observability. That leak has since been
// FIXED: both now take the local monitor.Option, and every offender the walk
// still finds is allowlisted. So nothing in this module makes this assertion
// fail today, and a scanner that reported nothing at all would pass it.
//
// ⚠ THE NON-VACUITY PROOF IS internal_leak_fixture_test.go, NOT THIS TEST.
// A "report nothing" mutant — one that drops the append — leaves this test
// GREEN, because seenKnown and seenSeal are marked BEFORE the append, so both
// self-cleaning loops still pass. Measured, with the mutant proved to compile
// first. Only the fixture's assertions kill it, so the two files are not
// near-duplicates and the fixture is not the redundant one.
//
// ⚠ The offenders are NOT limited to the symbols anyone predicted: this guard
// was generalised from a grep whose pattern only matched `observability.`, and
// it then found persistence.NewSchedulerLocker and casbinauthz.DBOption,
// neither of which anyone had named.
//
// WHAT THIS GUARD STILL CANNOT SEE. Both holes are recorded here rather than
// left silent, because an unstated blind spot gets trusted wrongly:
//
//   - DOT-IMPORTS. internalImportIdents skips "." and "_", so a dot-imported
//     internal package's types appear as a bare ast.Ident and no SelectorExpr
//     arm can reach them. Measured: the module has ZERO dot-imports of an
//     internal package, so the hole is real but currently empty.
//   - TYPE ALIASES ACROSS FILES. `type X = internalpkg.Y` re-exported in one
//     file and used unqualified in another defeats the import gate, since the
//     second file imports no internal package. Measured at the time of writing:
//     the module has 5 exported aliases and NONE resolves to an internal
//     package, so the hole is real but currently unexploited.
//   - ARRAY LENGTHS. consumerReachableTypeExprs recurses into an ArrayType's
//     ELEMENT only, so an internal constant used as the length —
//     `[secret.N]byte` — is no longer reported; before the recursion arm was
//     added, taking the array whole did report it. A deliberate narrowing with
//     zero instances in this module, recorded because silence about a
//     regression is the thing this file's own standard forbids, not the
//     behaviour itself.
//   - METHODS PROMOTED THROUGH AN EMBEDDED UNEXPORTED TYPE, and NAMED
//     UNEXPORTED TYPES BEHIND AN EXPORTED FIELD. Both need the declaration of a
//     type this walk never resolves: an exported struct embedding an unexported
//     type promotes that type's exported methods, and an exported field of a
//     named unexported struct type exposes that struct's exported fields. This
//     walk reads the field, not the declaration it points at. Closing either
//     needs go/types rather than go/ast — a DIFFERENT guard, not a bigger one,
//     which is why they are recorded rather than fixed here. Measured: one
//     latent candidate for the first, (neutralLockerBridge).Lock in
//     scheduler/scheduler.go, which is neither embedded in nor returned from
//     any exported type; and no instance of the second found in this module.
func TestNoExportedSignatureNamesAnInternalType(t *testing.T) {
	t.Parallel()

	scan, err := scanInternalLeaks(moduleRoot(t), knownOpenInternalLeaks, intentionalCapabilitySeals)
	require.NoError(t, err)
	offenders, seenKnown, seenSeal := scan.offenders, scan.seenKnown, scan.seenSeal

	assert.Empty(t, offenders,
		"an exported signature naming an internal/ type is uncallable by a consumer. "+
			"If that is accidental, give the package its own Option type and keep the internal "+
			"one in an unexported field. If it is deliberate — an internal capability token, "+
			"whose whole point is that consumers cannot call the function — do NOT apply that "+
			"remedy: it would unseal what the token seals. Add the symbol to "+
			"intentionalCapabilitySeals with the internal import path it is sanctioned for")

	for key, want := range intentionalCapabilitySeals {
		assert.True(t, seenSeal[key],
			"intentionalCapabilitySeals entry %q (expecting %s) no longer matches any offender — "+
				"the seal was removed or now names a different internal package; delete the entry "+
				"or correct its path, or the next leak at that symbol ships unnoticed", key, want)
	}

	for key, leak := range knownOpenInternalLeaks {
		assert.True(t, seenKnown[key],
			"knownOpenInternalLeaks entry %q (expecting %s) no longer matches any offender — "+
				"the leak was fixed or now names a different internal package; delete the entry "+
				"or correct its path, or the next leak at that symbol ships unnoticed.\n"+
				"The tolerance was recorded because: %s",
			key, leak.importPath, leak.why)
	}
}

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

// reachableExported reports whether fn is callable by an importer of the
// package: an exported func, or an exported method on an exported receiver.
func reachableExported(fn *ast.FuncDecl) bool {
	if !fn.Name.IsExported() {
		return false
	}
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return true
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if idx, ok := recv.(*ast.IndexExpr); ok { // generic receiver, e.g. Foo[T]
		recv = idx.X
	}
	ident, ok := recv.(*ast.Ident)
	return ok && ident.IsExported()
}

// knownOpenInternalLeaks are offenders this test reports but tolerates, each
// tracked as its own backlog item and owned by another package's delivery.
// Keyed by "<path>:<symbol>" so a line move does not silence the guard.
//
// It is SELF-CLEANING: an entry that no longer matches any offender fails the
// test, so a fixed leak cannot leave a stale exemption behind for the next one
// to hide under.
var knownOpenInternalLeaks = map[string]string{
	"persistence/scheduler_locker.go:NewSchedulerLocker": "takes an internal dialect.Locker parameter — " +
		"found by this guard, outside runtime/, not fixed here; the doc comment even invites a consumer to " +
		"\"bring your own dialect.Locker\", which no consumer can name",
}

// TestNoExportedSignatureNamesAnInternalType walks every non-internal,
// non-test Go file in the module and fails if a consumer-reachable exported
// signature names a type from an internal/ package.
//
// What makes it fail today: runtime/monitor/stats_collector.go declares
// NewOutboxStatsCollector and NewTimerStatsCollector with
// `opts ...observability.Option`, where observability is
// github.com/kartaladev/wrkflw/internal/observability. Real RED, no mutation.
//
// ⚠ These are NOT the only two such symbols in the module, and this guard is
// what refuted the claim that they were: it was generalised from a grep whose
// pattern only matched `observability.`. persistence.NewSchedulerLocker is a
// third, of the same class.
func TestNoExportedSignatureNamesAnInternalType(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	fset := token.NewFileSet()

	var offenders []string
	seenKnown := make(map[string]bool, len(knownOpenInternalLeaks))

	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
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
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !reachableExported(fn) {
				continue
			}
			ast.Inspect(fn.Type, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkgIdent, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if importPath, isInternal := internals[pkgIdent.Name]; isInternal {
					key := filepath.ToSlash(rel) + ":" + fn.Name.Name
					if _, known := knownOpenInternalLeaks[key]; known {
						seenKnown[key] = true
						return true
					}
					offenders = append(offenders, fmt.Sprintf("%s:%d %s names %s.%s (%s)",
						rel, fset.Position(fn.Pos()).Line, fn.Name.Name,
						pkgIdent.Name, sel.Sel.Name, importPath))
				}
				return true
			})
		}
		return nil
	}))

	assert.Empty(t, offenders,
		"an exported signature naming an internal/ type is uncallable by a consumer; "+
			"give the package its own Option type and keep the internal one in an unexported field")

	for key := range knownOpenInternalLeaks {
		assert.True(t, seenKnown[key],
			"knownOpenInternalLeaks entry %q no longer matches any offender — delete it, "+
				"or the next leak at that symbol ships unnoticed", key)
	}
}

package engine_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestCorePurityNoOTel asserts the pure core never imports OpenTelemetry.
// Observability lives strictly in the runtime and outer layers.
func TestCorePurityNoOTel(t *testing.T) {
	for _, dir := range []string{".", "../definition"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, imp := range f.Imports {
				if strings.Contains(imp.Path.Value, "go.opentelemetry.io") {
					t.Errorf("%s imports %s: the pure core must not import OpenTelemetry", path, imp.Path.Value)
				}
			}
		}
	}
}

// TestPurity_ASTDetectsWallClock proves the wall-clock detector actually fires:
// it runs wallClockCalls over in-test source strings and asserts what each one
// reports. Without this, the real-file scan below could pass vacuously (a broken
// detector that never reports anything).
//
// The detector resolves the file's LOCAL name for the "time" import rather than
// assuming the identifier is spelled "time". Before it did, the guard was
// evadable by a one-word edit: `import chrono "time"` in any non-test engine
// file made all three purity tests report EXIT=0 while the unaliased form was
// RED. The import denylist is NOT a second line of defence here — "time" is not,
// and never was, an entry in deniedEngineImports.
func TestPurity_ASTDetectsWallClock(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		src    string
		assert func(t *testing.T, got []string)
	}

	detected := func(want string) func(*testing.T, []string) {
		return func(t *testing.T, got []string) {
			t.Helper()
			if len(got) == 0 {
				t.Fatalf("wallClockCalls reported no wall-clock read; want %q", want)
			}
			if got[0] != want {
				t.Fatalf("wallClockCalls reported %v; want [%q]", got, want)
			}
		}
	}
	clean := func(t *testing.T, got []string) {
		t.Helper()
		if len(got) != 0 {
			t.Fatalf("wallClockCalls reported %v; want no wall-clock read", got)
		}
	}

	cases := []testCase{
		{
			// Passes before and after: the control that proves the fixture
			// shape and the detector's happy path.
			name: "plain time import",
			src: `package fixture

import "time"

func readsClock() { _ = time.Now() }
`,
			assert: detected("Now"),
		},
		{
			// Fails before the fix: the AST identifier is "chrono", so the
			// old matcher's pkg.Name != "time" short-circuited to "not a
			// wall-clock read".
			name: "aliased time import",
			src: `package fixture

import chrono "time"

func readsClock() { _ = chrono.Now() }
`,
			assert: detected("Now"),
		},
		{
			// Fails before the fix: a dot-imported Since() is a bare *ast.Ident
			// call, which the selector-only matcher never inspected.
			name: "dot-imported time",
			src: `package fixture

import . "time"

func readsClock() { _ = Since(Time{}) }
`,
			assert: detected("Since"),
		},
		{
			// Fails before the fix: the old matcher keyed on the SPELLING
			// "time", so an unrelated package aliased to that name was
			// reported as a wall-clock read.
			name: "unrelated package aliased to the name time",
			src: `package fixture

import time "example.com/not/stdlib/time"

func readsClock() { _ = time.Now() }
`,
			assert: clean,
		},
		{
			// Fails before the fix, same reason: a local variable named time
			// is not the time package.
			name: "local identifier named time with no time import",
			src: `package fixture

type clock struct{ Now func() int }

func readsClock(time clock) { _ = time.Now() }
`,
			assert: clean,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, err := parser.ParseFile(token.NewFileSet(), "fixture.go", tc.src, 0)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			tc.assert(t, wallClockCalls(f))
		})
	}
}

// deniedEngineImports are import-path substrings the pure engine core must never
// pull in: transport surfaces, concrete persistence, and the swappable vendors
// (watermill/gocron/clockwork) plus OpenTelemetry. The core depends on interfaces
// only, so a match here means a layering leak.
var deniedEngineImports = []string{
	"/transport/",
	"/internal/persistence",
	// The engine core is a layer BELOW the runtime: its seams (IDGenerator,
	// ConditionEvaluator, …) are declared locally and satisfied structurally by
	// runtime types, never by importing them back down.
	"/runtime/",
	"watermill",
	"gocron",
	"clockwork",
	"casbin",
	"go.opentelemetry.io",
}

// TestCorePurityImportDenylist asserts no non-test file of the engine package
// imports a denied path (transport, concrete persistence, or a swappable vendor).
func TestCorePurityImportDenylist(t *testing.T) {
	for _, path := range nonTestGoFiles(t, ".") {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			for _, denied := range deniedEngineImports {
				if strings.Contains(imp.Path.Value, denied) {
					t.Errorf("%s imports %s: the pure engine core must not import %q", path, imp.Path.Value, denied)
				}
			}
		}
	}
}

// TestCorePurityNoWallClock asserts no non-test file of the engine package reads
// the wall clock (time.Now/time.Since/time.Tick/time.After/time.Sleep/time.Until/
// time.NewTimer/time.NewTicker/time.AfterFunc). The core takes time from an
// injected clockwork.Clock so a fake clock drives it deterministically in tests.
func TestCorePurityNoWallClock(t *testing.T) {
	for _, path := range nonTestGoFiles(t, ".") {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, call := range wallClockCalls(f) {
			t.Errorf("%s calls time.%s: the pure engine core must take time from clockwork.Clock, not the wall clock", path, call)
		}
	}
}

// nonTestGoFiles returns the paths of every non-test .go file directly in dir.
func nonTestGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var paths []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths
}

// timeLocalNames reports the identifiers under which f refers to the standard
// library "time" package, and whether f dot-imports it.
//
// The IMPORT PATH is the authority, never the identifier's spelling: `import
// chrono "time"` binds the package to "chrono", and `import time "example.com/
// time"` binds an unrelated package to "time". A blank import (`_`) binds
// nothing callable and is skipped.
func timeLocalNames(f *ast.File) (names []string, dot bool) {
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || path != "time" {
			continue
		}
		switch {
		case imp.Name == nil:
			names = append(names, "time")
		case imp.Name.Name == ".":
			dot = true
		case imp.Name.Name == "_":
			// Bound to nothing; no call can reach the package through it.
		default:
			names = append(names, imp.Name.Name)
		}
	}
	return names, dot
}

// isWallClockName reports whether name is a time-package function that reads the
// wall clock.
func isWallClockName(name string) bool {
	switch name {
	case "Now", "Since", "Tick", "After", "Sleep", "Until", "NewTimer", "NewTicker", "AfterFunc":
		return true
	}
	return false
}

// wallClockCalls reports every wall-clock read in f as the selected identifier
// name ("Now", "Since", "Tick", "After", "Sleep", "Until", "NewTimer",
// "NewTicker", or "AfterFunc"). A read is a call through the file's LOCAL name
// for the "time" import — resolved from the import path by timeLocalNames, so
// an alias cannot evade the check and a same-named foreign package cannot
// trigger it — or, under a dot import, a bare call to one of those names. Empty
// result means f never reads the wall clock.
func wallClockCalls(f *ast.File) []string {
	names, dot := timeLocalNames(f)
	if len(names) == 0 && !dot {
		return nil
	}
	local := make(map[string]bool, len(names))
	for _, n := range names {
		local[n] = true
	}

	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			pkg, ok := fun.X.(*ast.Ident)
			if ok && local[pkg.Name] && isWallClockName(fun.Sel.Name) {
				found = append(found, fun.Sel.Name)
			}
		case *ast.Ident:
			if dot && isWallClockName(fun.Name) {
				found = append(found, fun.Name)
			}
		}
		return true
	})
	return found
}

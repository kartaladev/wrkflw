package engine_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorePurityNoOTel asserts the pure core never imports OpenTelemetry.
// Observability lives strictly in the runtime and outer layers (spec §1).
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
// it runs wallClockCalls over an in-test source string that reads time.Now() and
// asserts the read is reported. Without this, the real-file scan below could pass
// vacuously (a broken detector that never reports anything).
func TestPurity_ASTDetectsWallClock(t *testing.T) {
	const src = `package fixture

import "time"

func readsClock() { _ = time.Now() }
`
	f, err := parser.ParseFile(token.NewFileSet(), "fixture.go", src, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if got := wallClockCalls(f); len(got) == 0 {
		t.Fatalf("wallClockCalls did not detect time.Now() in the fixture; got %v", got)
	}
}

// deniedEngineImports are import-path substrings the pure engine core must never
// pull in: transport surfaces, concrete persistence, and the swappable vendors
// (watermill/gocron/clockwork) plus OpenTelemetry. The core depends on interfaces
// only, so a match here means a layering leak.
var deniedEngineImports = []string{
	"/transport/",
	"/internal/persistence",
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
// injected clock.Clock so a fake clock drives it deterministically in tests.
func TestCorePurityNoWallClock(t *testing.T) {
	for _, path := range nonTestGoFiles(t, ".") {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, call := range wallClockCalls(f) {
			t.Errorf("%s calls time.%s: the pure engine core must take time from clock.Clock, not the wall clock", path, call)
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

// wallClockCalls reports every wall-clock read in f as the selected identifier
// name ("Now", "Since", "Tick", "After", "Sleep", "Until", "NewTimer",
// "NewTicker", or "AfterFunc"). A read is a call whose function is a selector
// time.<Name> where time is a bare package identifier. Empty result means f
// never reads the wall clock.
func wallClockCalls(f *ast.File) []string {
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "time" {
			return true
		}
		switch sel.Sel.Name {
		case "Now", "Since", "Tick", "After", "Sleep", "Until", "NewTimer", "NewTicker", "AfterFunc":
			found = append(found, sel.Sel.Name)
		}
		return true
	})
	return found
}

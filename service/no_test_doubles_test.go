package service_test

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServiceShipsNoTestDoubles guards the invariant this package's mocks were
// moved out for: nothing that only tests need may reach a consumer's binary.
//
// The mocks for PolicyAdmin, RelayStatsAdmin, TimerAdmin, DeadLetterAdmin and
// LineageAdmin were generated into this package as ordinary non-test files.
// That put 22 exported Mock* types on a public package's API and pulled
// go.uber.org/mock into the dependency graph of everything that imports
// service. They now live in service/servicetest.
//
// It reads the package's PRODUCTION file set through go/build — GoFiles
// excludes _test.go and XTestGoFiles by construction — so it asks the same
// question the compiler does when building a consumer, rather than a
// grep-shaped approximation of it.
//
// The regrowth path is a stale //go:generate directive: a directive still
// naming a destination in this directory recreates the leak the next time
// anyone runs go generate. The third subtest fails the moment such a directive
// is written, rather than after someone regenerates and commits the result.
func TestServiceShipsNoTestDoubles(t *testing.T) {
	t.Parallel()

	pkg, err := build.ImportDir(".", 0)
	require.NoError(t, err, "the service package must be loadable by go/build")
	require.NotEmpty(t, pkg.GoFiles, "expected a non-empty production file set")

	t.Run("no test-double dependency in the production build", func(t *testing.T) {
		t.Parallel()

		for _, imp := range pkg.Imports {
			assert.NotContains(t, imp, "go.uber.org/mock",
				"the production build of service must not depend on a mocking "+
					"library; generated doubles belong in service/servicetest")
		}
	})

	t.Run("no exported mock types in the production build", func(t *testing.T) {
		t.Parallel()

		fset := token.NewFileSet()
		for _, name := range pkg.GoFiles {
			file, parseErr := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
			require.NoError(t, parseErr, "parsing %s", name)

			for _, decl := range file.Decls {
				for _, ident := range declaredNames(decl) {
					assert.False(t, strings.HasPrefix(ident, "Mock"),
						"%s declares %q in the production build: a generated "+
							"double must be regenerated into service/servicetest, "+
							"not beside the interface it mocks", name, ident)
				}
			}
		}
	})

	t.Run("generate directives target servicetest", func(t *testing.T) {
		t.Parallel()

		fset := token.NewFileSet()
		var found int
		for _, name := range pkg.GoFiles {
			file, parseErr := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ParseComments|parser.SkipObjectResolution)
			require.NoError(t, parseErr, "parsing %s", name)

			for _, group := range file.Comments {
				for _, c := range group.List {
					if !strings.HasPrefix(c.Text, "//go:generate mockgen") {
						continue
					}
					found++
					assert.Contains(t, c.Text, "-destination=servicetest/",
						"%s: a mockgen directive writing into this directory "+
							"recreates the leak on the next go generate", name)
					assert.Contains(t, c.Text, "-package=servicetest",
						"%s: the destination package must match its directory", name)
				}
			}
		}
		assert.Equal(t, 4, found,
			"expected one mockgen directive per interface file (policyadmin, "+
				"opsadmin, deadletter, lineage); a missing one means a mock is "+
				"no longer regenerated and will silently rot")
	})
}

// declaredNames returns the names a top-level declaration introduces into the
// package scope. Import declarations introduce none that matter here.
func declaredNames(decl ast.Decl) []string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		// A method belongs to its receiver's type, which is itself checked as a
		// TypeSpec, so only plain functions are reported.
		if d.Recv != nil {
			return nil
		}
		return []string{d.Name.Name}
	case *ast.GenDecl:
		var names []string
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, s.Name.Name)
			case *ast.ValueSpec:
				for _, n := range s.Names {
					names = append(names, n.Name)
				}
			}
		}
		return names
	default:
		return nil
	}
}

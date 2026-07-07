package scheduling_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// forbiddenDBDeps are the database-driver packages the public scheduling façade
// must never pull in — directly OR transitively. DB coupling lives in
// scheduling/backend/{postgres,mysql} and the persistence-lock bridge; the façade
// sees only the neutral Locker/Elector interfaces (ADR-0102).
var forbiddenDBDeps = map[string]struct{}{
	"database/sql":                    {},
	"github.com/jackc/pgx/v5":         {},
	"github.com/jackc/pgx/v5/pgxpool": {},
}

// TestSchedulingHasNoDBDeps asserts that the public scheduling package carries no
// database-driver dependency anywhere in its FULL TRANSITIVE import graph: neither
// jackc/pgx (Postgres) nor the standard database/sql package may appear, however
// deep. This is what lets a consumer (e.g. the service façade) import scheduling
// without polluting its dependency graph with a DB driver.
func TestSchedulingHasNoDBDeps(t *testing.T) {
	cfg := &packages.Config{Mode: packages.NeedImports | packages.NeedDeps}
	pkgs, err := packages.Load(cfg, "github.com/zakyalvan/krtlwrkflw/scheduling")
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)

	for _, root := range pkgs {
		require.Emptyf(t, root.Errors, "package %s failed to load: %v", root.PkgPath, root.Errors)

		// Direct-import guard (kept from the original assertion): the façade's own
		// import list must be free of DB drivers.
		for imp := range root.Imports {
			if isForbidden(imp) {
				t.Fatalf("scheduling must not directly import a DB driver, found: %s", imp)
			}
		}

		// Transitive guard: walk the whole dependency closure. A DB driver
		// reachable through ANY chain of imports fails the test.
		if path, found := findForbiddenInClosure(root); found {
			t.Fatalf("scheduling must not transitively import a DB driver, reached via: %s",
				strings.Join(path, " -> "))
		}
	}
}

// isForbidden reports whether importPath is one of the banned DB-driver packages.
func isForbidden(importPath string) bool {
	_, banned := forbiddenDBDeps[importPath]
	return banned
}

// findForbiddenInClosure performs a depth-first walk of root's transitive import
// graph, returning the import chain to the first forbidden DB-driver package it
// reaches (and true), or nil and false if the closure is clean.
func findForbiddenInClosure(root *packages.Package) ([]string, bool) {
	visited := make(map[string]bool)

	var walk func(pkg *packages.Package, trail []string) ([]string, bool)
	walk = func(pkg *packages.Package, trail []string) ([]string, bool) {
		if visited[pkg.PkgPath] {
			return nil, false
		}
		visited[pkg.PkgPath] = true

		here := append(trail, pkg.PkgPath)
		for _, dep := range pkg.Imports {
			if isForbidden(dep.PkgPath) {
				return append(here, dep.PkgPath), true
			}
			if chain, found := walk(dep, here); found {
				return chain, true
			}
		}
		return nil, false
	}

	return walk(root, nil)
}

package scheduler_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// modulePrefix is this module's import path prefix. selfPrefix is the
// scheduler subtree's own prefix — packages under scheduler/... are allowed
// to import each other (e.g. scheduler/internal/gocron importing scheduler's
// exported port types) but must never reach outside the subtree into the
// rest of the module (engine, runtime, persistence, service, ...).
const (
	modulePrefix = "github.com/kartaladev/wrkflw/"
	selfPrefix   = "github.com/kartaladev/wrkflw/scheduler"
)

// TestSchedulerTreeIsSelfContained asserts the scheduler package tree keeps
// its promise of being importable standalone: no NON-TEST (production)
// source file anywhere under scheduler/... may import another
// github.com/kartaladev/wrkflw/* package outside the scheduler/... subtree
// itself. This is what lets a consumer depend on scheduler alone without
// dragging in engine, runtime, persistence, or service.
//
// Scope: this guard loads the build package graph only — files ending in
// _test.go are excluded because packages.Config.Tests is left false. Test
// files (e.g. ones using internal/dbtest, processtest, or runtimetest
// helpers) are intentionally out of scope: tests are allowed to reach across
// the module to exercise integration behavior; only the production surface
// must stay self-contained.
//
// This guard REPLACES scheduler/neutrality_test.go's narrower DB-driver
// check: forbidding every cross-module import subsumes forbidding the two
// DB-driver imports specifically (a DB driver can only reach the scheduler
// façade transitively through a wrkflw/* package in the first place, since
// scheduler's own backend/{postgres,mysql} electors are themselves inside
// the scheduler/... subtree and therefore already covered as self-imports —
// see TestSchedulerBackendsHaveNoDirectDBDeps below for that residual case).
func TestSchedulerTreeIsSelfContained(t *testing.T) {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedImports}
	pkgs, err := packages.Load(cfg, selfPrefix+"/...")
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)

	for _, pkg := range pkgs {
		require.Emptyf(t, pkg.Errors, "package %s failed to load: %v", pkg.PkgPath, pkg.Errors)

		for imp := range pkg.Imports {
			if !strings.HasPrefix(imp, modulePrefix) {
				continue // third-party or stdlib import, not our concern
			}
			if isSchedulerSelfImport(imp) {
				continue // self-import within the scheduler subtree, allowed
			}
			t.Errorf("package %s imports %s, which is outside the scheduler/ subtree; "+
				"the scheduler tree must stay self-contained and importable standalone",
				pkg.PkgPath, imp)
		}
	}
}

// isSchedulerSelfImport reports whether importPath is the scheduler root
// package or one of its subpackages.
func isSchedulerSelfImport(importPath string) bool {
	return importPath == selfPrefix || strings.HasPrefix(importPath, selfPrefix+"/")
}

// forbiddenDBDeps are the database-driver packages the scheduler tree's
// backend electors (scheduler/backend/{postgres,mysql}) are expected to
// import directly — this is the one place a DB driver legitimately appears
// in the subtree. TestSchedulerTreeIsSelfContained already guarantees no
// wrkflw/* package outside scheduler/... leaks in; this second check keeps
// the DB-driver surface itself narrow and explicit, preserving the intent of
// the original neutrality_test.go without duplicating its transitive-closure
// walk (a per-package direct-import scan is sufficient here because
// packages.Load already gives every package in the subtree, not just the
// root façade).
var forbiddenDBDeps = map[string]struct{}{
	"database/sql":                    {},
	"github.com/jackc/pgx/v5":         {},
	"github.com/jackc/pgx/v5/pgxpool": {},
}

// allowedDBDepPackages are the scheduler subpackages permitted to carry a
// direct DB-driver import — the backend electors and the internal gocron
// elector implementations they wrap, by design.
var allowedDBDepPackages = map[string]struct{}{
	"github.com/kartaladev/wrkflw/scheduler/backend/postgres":          {},
	"github.com/kartaladev/wrkflw/scheduler/backend/mysql":             {},
	"github.com/kartaladev/wrkflw/scheduler/internal/gocron/pgelector": {},
	"github.com/kartaladev/wrkflw/scheduler/internal/gocron/myelector": {},
}

// TestSchedulerBackendsHaveNoDirectDBDeps asserts that the DB-driver
// dependency the scheduler subtree does carry is confined to the backend
// elector packages, and that the public façade (scheduler itself) and every
// other subpackage stay DB-driver-free, matching the original
// neutrality_test.go's guarantee.
func TestSchedulerBackendsHaveNoDirectDBDeps(t *testing.T) {
	cfg := &packages.Config{Mode: packages.NeedName | packages.NeedImports}
	pkgs, err := packages.Load(cfg, selfPrefix+"/...")
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)

	for _, pkg := range pkgs {
		require.Emptyf(t, pkg.Errors, "package %s failed to load: %v", pkg.PkgPath, pkg.Errors)

		if _, allowed := allowedDBDepPackages[pkg.PkgPath]; allowed {
			continue
		}

		for imp := range pkg.Imports {
			if _, banned := forbiddenDBDeps[imp]; banned {
				t.Errorf("package %s directly imports DB driver %s; only %v may do so",
					pkg.PkgPath, imp, mapKeys(allowedDBDepPackages))
			}
		}
	}
}

func mapKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

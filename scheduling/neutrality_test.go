package scheduling_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// TestSchedulingHasNoDBDeps asserts that the public scheduling package carries no
// database-driver dependency: it must import NEITHER jackc/pgx (Postgres) NOR the
// standard database/sql package. DB coupling lives in scheduling/backend/{postgres,
// mysql} and the persistence-lock bridge; the façade sees only the neutral
// Locker/Elector interfaces (ADR-0102).
func TestSchedulingHasNoDBDeps(t *testing.T) {
	cfg := &packages.Config{Mode: packages.NeedImports | packages.NeedDeps}
	pkgs, err := packages.Load(cfg, "github.com/zakyalvan/krtlwrkflw/scheduling")
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)

	for _, p := range pkgs {
		for imp := range p.Imports {
			if strings.Contains(imp, "jackc/pgx") || imp == "database/sql" {
				t.Fatalf("scheduling must not import a DB driver, found: %s", imp)
			}
		}
	}
}

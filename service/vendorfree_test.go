package service_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestServiceDependencyGraphIsVendorFree locks in the invariant that the service
// package's compile graph pulls in no database driver, no database/sql, and no
// persistence package. Durability reaches service only through the
// DurableProvider interface, whose driver-backed implementation lives in the
// persistence package — imported solely by consumers who opt into durability.
//
// This is enforced with `go list -deps`, mirroring scripts/check-extraction.sh.
func TestServiceDependencyGraphIsVendorFree(t *testing.T) {
	t.Parallel()

	out, err := exec.Command("go", "list", "-deps",
		"github.com/zakyalvan/krtlwrkflw/service").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}

	banned := []string{
		"github.com/jackc/pgx",
		"github.com/jackc/pgx/v5",
		"github.com/jackc/pgx/v5/pgxpool",
		"github.com/go-sql-driver/mysql",
		"modernc.org/sqlite",
		"database/sql",
		"github.com/zakyalvan/krtlwrkflw/persistence",
		"github.com/zakyalvan/krtlwrkflw/internal/persistence",
		"github.com/zakyalvan/krtlwrkflw/internal/persistence/store",
		"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect",
		"github.com/zakyalvan/krtlwrkflw/internal/database",
	}
	bannedSet := make(map[string]struct{}, len(banned))
	for _, b := range banned {
		bannedSet[b] = struct{}{}
	}

	for _, line := range strings.Split(string(out), "\n") {
		dep := strings.TrimSpace(line)
		if _, ok := bannedSet[dep]; ok {
			t.Errorf("service must be DB-vendor-free but its dependency graph includes %q", dep)
		}
	}
}

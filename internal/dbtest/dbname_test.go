package dbtest_test

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// namePrinterEnv arms [TestPerTestDatabaseNamePrinterHelper], which exists
	// only to be run in a child process; an ordinary run skips it.
	namePrinterEnv = "WRKFLW_DBTEST_PRINT_DB_NAMES"
	// namePrinterHelper is the name of that helper, used to select it in the child.
	namePrinterHelper = "TestPerTestDatabaseNamePrinterHelper"
	// namePrinterMarker prefixes every printed name so the parent can pick the
	// names out of `go test -v` output without matching anything else.
	namePrinterMarker = "WRKFLW-DBNAME="
	// namesPerChild is how many names each child allocates. More than one so the
	// parent asserts the two SETS are disjoint, not merely that the first names
	// differ.
	namesPerChild = 3
)

// TestPerTestDatabaseNamePrinterHelper is a fixture, not an assertion: it prints
// the first few per-test database names THIS process would create. It runs only
// in the child process spawned by
// [TestPerTestDatabaseNamesAreDisjointAcrossProcesses].
func TestPerTestDatabaseNamePrinterHelper(t *testing.T) {
	if os.Getenv(namePrinterEnv) != "1" {
		t.Skipf("armed only by the child process of TestPerTestDatabaseNamesAreDisjointAcrossProcesses (%s=1)", namePrinterEnv)
	}
	for range namesPerChild {
		t.Log(namePrinterMarker + dbtest.NextTestDBNameForTest())
	}
}

// TestPerTestDatabaseNamesAreDisjointAcrossProcesses pins the property that makes
// the shared-server path (blocker 7) safe: `go test ./...` builds one binary per
// package and runs up to GOMAXPROCS of them AT ONCE, all pointed at the single
// server named by WRKFLW_TEST_POSTGRES_DSN / WRKFLW_TEST_MYSQL_DSN. If two of
// those processes can generate the same database name then, on PostgreSQL, one
// CREATE DATABASE fails and one process's DROP ... WITH (FORCE) cuts the other's
// live connections; on MySQL the CREATE silently succeeds into the same database
// and the two packages' rows mix.
//
// The two names must therefore be disjoint per PROCESS, not merely per call — a
// package-level counter is per-call-unique and per-process IDENTICAL, which is
// exactly the defect. Two real child `go test` processes are spawned because a
// per-process identity cannot be simulated inside one process.
func TestPerTestDatabaseNamesAreDisjointAcrossProcesses(t *testing.T) {
	if os.Getenv(namePrinterEnv) == "1" {
		t.Skip("already inside the child process; running this again would spawn another")
	}
	t.Parallel()

	// Concurrent, so the two children are alive at the same time — the situation
	// `go test ./...` actually creates, and the one in which the operating system
	// guarantees them distinct PIDs.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	var (
		wg     sync.WaitGroup
		names  [2][]string
		errs   [2]error
		output [2]string
	)
	for i := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			names[i], output[i], errs[i] = runNamePrinterChild(ctx)
		}()
	}
	wg.Wait()

	for i := range names {
		require.NoErrorf(t, errs[i], "child %d must pass:\n%s", i, output[i])
		require.NotContainsf(t, output[i], "no tests to run",
			"child %d: the -run filter selected nothing, so this test proves nothing:\n%s", i, output[i])
		require.NotContainsf(t, output[i], "SKIP",
			"child %d: the helper must be armed by %s, not skipped:\n%s", i, namePrinterEnv, output[i])
		require.Lenf(t, names[i], namesPerChild,
			"child %d printed %d names, want %d — the -run filter or the marker is wrong:\n%s",
			i, len(names[i]), namesPerChild, output[i])
	}

	first := make(map[string]bool, len(names[0]))
	for _, n := range names[0] {
		first[n] = true
	}
	for _, n := range names[1] {
		assert.Falsef(t, first[n],
			"two concurrent test binaries generated the same per-test database name %q; on one shared server they would fight over the same database\nchild 0: %v\nchild 1: %v",
			n, names[0], names[1])
	}
}

// runNamePrinterChild runs the printer helper in a child `go test` and returns the
// names it printed, the raw output, and the child's exit error. It makes no
// assertions of its own: it runs on a goroutine, where t.FailNow is unsupported,
// so the caller judges what it returns.
func runNamePrinterChild(ctx context.Context) (names []string, output string, err error) {
	// -count=1 so a cached PASS never stands in for a real child process: the
	// whole point is that a second PROCESS runs.
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-run", "^"+namePrinterHelper+"$", ".")
	cmd.Env = append(os.Environ(), namePrinterEnv+"=1")
	out, err := cmd.CombinedOutput()
	output = string(out)

	for line := range strings.SplitSeq(output, "\n") {
		if _, name, found := strings.Cut(line, namePrinterMarker); found {
			names = append(names, strings.TrimSpace(name))
		}
	}
	return names, output, err
}

// legalUnquotedIdentifier matches the names both engines accept UNQUOTED and
// unfolded. PostgreSQL lower-cases an unquoted identifier, and the helpers also
// splice the name into a DSN's database segment verbatim, so an upper-case
// character would make the created database and the connected-to database differ
// by case.
var legalUnquotedIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// TestPerTestDatabaseNameFitsBothEngines pins the length and character budget.
// PostgreSQL truncates identifiers at 63 bytes (NAMEDATALEN-1) — silently, which
// would map two distinct names onto one database — and MySQL rejects a database
// name over 64 characters.
func TestPerTestDatabaseNameFitsBothEngines(t *testing.T) {
	t.Parallel()

	const postgresIdentifierLimit = 63

	for range 8 {
		name := dbtest.NextTestDBNameForTest()
		assert.LessOrEqualf(t, len(name), postgresIdentifierLimit,
			"%q is %d bytes: PostgreSQL truncates at %d (and MySQL at 64), which would silently merge two per-test databases",
			name, len(name), postgresIdentifierLimit)
		assert.Regexpf(t, legalUnquotedIdentifier, name,
			"%q must be usable unquoted on both engines and verbatim in a DSN path", name)
	}
}

// TestPerTestDatabaseNamesAreUniqueWithinAProcess pins the property the counter
// already provided and which the per-process tag must not cost us.
func TestPerTestDatabaseNamesAreUniqueWithinAProcess(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 64)
	for range 64 {
		name := dbtest.NextTestDBNameForTest()
		assert.Falsef(t, seen[name], "duplicate per-test database name %q within one process", name)
		seen[name] = true
	}
}

// TestOwnedTestDBNameGuardsTheDrop pins the guard both cleanup paths route their
// DROP DATABASE through. Dropping is the destructive half of the shared-server
// path: a stray DROP does not merely fail the dropping test, it deletes the
// database another package's test binary is running against — and PostgreSQL's
// WITH (FORCE) severs that binary's live connections first.
//
// It fails if the guard is dropped or weakened to a bare "wrkflw_test_" prefix
// check: the second and third cases are exactly the names a DIFFERENT process
// creates, and both start with that prefix.
func TestOwnedTestDBNameGuardsTheDrop(t *testing.T) {
	t.Parallel()

	ours := dbtest.NextTestDBNameForTest()
	tag := dbtest.ProcessTagForTest

	type testCase struct {
		name   string
		dbName string
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name:   "a name this process generated is droppable",
			dbName: ours,
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:   "the pre-fix counter-only name belongs to no known process",
			dbName: "wrkflw_test_1",
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "workflow-dbtest")
			},
		},
		{
			name:   "another process's database is refused",
			dbName: strings.Replace(ours, tag, "p999999_deadbeefcafe", 1),
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
			},
		},
		{
			name:   "our tag without a counter segment is not a database we made",
			dbName: "wrkflw_test_" + tag,
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
			},
		},
		{
			name:   "an unrelated database is refused",
			dbName: "postgres",
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, dbtest.OwnedTestDBNameForTest(tc.dbName))
		})
	}
}

package store_test

// call_links_errors_test.go — error-branch coverage for CallLinkStore.
//
// Uses the in-process SQLite backend (no Docker) and the drop-table pattern to
// force query errors on the read methods, hitting branches that the happy-path
// conformance suite cannot reach with a live DB.
//
// Also covers the unsupported-conn-type error path (database.From returns an
// error when the connection type is unknown).

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// TestCallLinkStoreErrors covers the error branches of CallLinkStore read
// methods (ClaimPending, MarkNotified, LookupChild, ListRunningChildren,
// ParentOf, ChildrenOf) using the in-process SQLite backend.
//
// The drop-table strategy forces the driver to surface a real error on Query/
// QueryRow/Exec after the table has been removed, exercising the exact error-
// wrap branches that the happy-path conformance suite cannot reach.
func TestCallLinkStoreErrors(t *testing.T) {
	tests := map[string]struct {
		run func(t *testing.T, cls *store.CallLinkStore)
	}{
		"ClaimPending query error on dropped table": {
			run: func(t *testing.T, cls *store.CallLinkStore) {
				_, err := cls.ClaimPending(t.Context(), 10)
				require.Error(t, err, "ClaimPending must surface query error")
				require.Contains(t, err.Error(), "claim", "error must reference claim path")
			},
		},
		"MarkNotified exec error on dropped table": {
			run: func(t *testing.T, cls *store.CallLinkStore) {
				err := cls.MarkNotified(t.Context(), "any-child")
				require.Error(t, err, "MarkNotified must surface exec error")
				require.Contains(t, err.Error(), "mark notified", "error must reference mark-notified path")
			},
		},
		"LookupChild query error on dropped table": {
			run: func(t *testing.T, cls *store.CallLinkStore) {
				_, _, err := cls.LookupChild(t.Context(), "any-child")
				require.Error(t, err, "LookupChild must surface query error")
				require.Contains(t, err.Error(), "lookup", "error must reference lookup path")
			},
		},
		"ListRunningChildren query error on dropped table": {
			run: func(t *testing.T, cls *store.CallLinkStore) {
				_, err := cls.ListRunningChildren(t.Context(), "any-parent")
				require.Error(t, err, "ListRunningChildren must surface query error")
				require.Contains(t, err.Error(), "list running children", "error must reference list path")
			},
		},
		"ParentOf query error on dropped table": {
			run: func(t *testing.T, cls *store.CallLinkStore) {
				_, err := cls.ParentOf(t.Context(), "any-child")
				require.Error(t, err, "ParentOf must surface query error")
				require.Contains(t, err.Error(), "parent of", "error must reference parent-of path")
			},
		},
		"ChildrenOf query error on dropped table": {
			run: func(t *testing.T, cls *store.CallLinkStore) {
				_, err := cls.ChildrenOf(t.Context(), "any-parent")
				require.Error(t, err, "ChildrenOf must surface query error")
				require.Contains(t, err.Error(), "children of", "error must reference children-of path")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			db := dbtest.RunTestSQLite(t)
			// Drop the table so the next read operation fails immediately.
			_, err := db.ExecContext(t.Context(), "DROP TABLE wrkflw_call_links")
			require.NoError(t, err, "drop wrkflw_call_links")
			cls, err := store.NewCallLinkStore(db, dialect.NewSQLite())
			require.NoError(t, err)
			tc.run(t, cls)
		})
	}
}

// TestCallLinkStoreUnsupportedConn verifies that all CallLinkStore read methods
// surface an error when constructed with an unsupported connection type (neither
// *pgxpool.Pool nor *sql.DB), covering the database.From() error path.
func TestCallLinkStoreUnsupportedConn(t *testing.T) {
	cls, err := store.NewCallLinkStore(struct{}{}, dialect.NewSQLite())
	require.NoError(t, err)

	t.Run("ClaimPending unsupported conn", func(t *testing.T) {
		_, err := cls.ClaimPending(t.Context(), 10)
		require.Error(t, err, "ClaimPending must fail on unsupported conn")
	})
	t.Run("MarkNotified unsupported conn", func(t *testing.T) {
		err := cls.MarkNotified(t.Context(), "any")
		require.Error(t, err, "MarkNotified must fail on unsupported conn")
	})
	t.Run("LookupChild unsupported conn", func(t *testing.T) {
		_, _, err := cls.LookupChild(t.Context(), "any")
		require.Error(t, err, "LookupChild must fail on unsupported conn")
	})
	t.Run("ListRunningChildren unsupported conn", func(t *testing.T) {
		_, err := cls.ListRunningChildren(t.Context(), "any")
		require.Error(t, err, "ListRunningChildren must fail on unsupported conn")
	})
	t.Run("ParentOf unsupported conn", func(t *testing.T) {
		_, err := cls.ParentOf(t.Context(), "any")
		require.Error(t, err, "ParentOf must fail on unsupported conn")
	})
	t.Run("ChildrenOf unsupported conn", func(t *testing.T) {
		_, err := cls.ChildrenOf(t.Context(), "any")
		require.Error(t, err, "ChildrenOf must fail on unsupported conn")
	})
}

// TestCallLinkStoreLeasedUnsupportedConn verifies that the leased-claim path
// surfaces an error on unsupported conn type (covers the database.From error
// branch in claimLeasedReturning and claimLeasedSQLite, and the
// transaction.JoinOrBegin error branch in claimLeasedSelectUpdate).
func TestCallLinkStoreLeasedUnsupportedConn(t *testing.T) {
	// Use Postgres dialect → SupportsReturning=true, SupportsSkipLocked=true → claimLeasedReturning
	clsPG, err := store.NewCallLinkStore(struct{}{}, dialect.NewPostgres(),
		store.WithCallLinkLease("owner", 30_000_000_000), // 30s in nanos
	)
	require.NoError(t, err)
	_, err = clsPG.ClaimPending(t.Context(), 10)
	require.Error(t, err, "leased claim (returning) must fail on unsupported conn")

	// Use MySQL dialect → SupportsReturning=false, SupportsSkipLocked=true → claimLeasedSelectUpdate
	clsMY, err := store.NewCallLinkStore(struct{}{}, dialect.NewMySQL(),
		store.WithCallLinkLease("owner", 30_000_000_000),
	)
	require.NoError(t, err)
	_, err = clsMY.ClaimPending(t.Context(), 10)
	require.Error(t, err, "leased claim (select-update) must fail on unsupported conn")

	// Use SQLite dialect → SupportsReturning=true, SupportsSkipLocked=false → claimLeasedSQLite
	clsSQ, err := store.NewCallLinkStore(struct{}{}, dialect.NewSQLite(),
		store.WithCallLinkLease("owner", 30_000_000_000),
	)
	require.NoError(t, err)
	_, err = clsSQ.ClaimPending(t.Context(), 10)
	require.Error(t, err, "leased claim (sqlite) must fail on unsupported conn")
}

// TestCallLinkStoreMarshalError exercises the json.Unmarshal error branch in
// scanPendingRows by seeding a row with invalid JSON in the output column, then
// calling ClaimPending. Uses SQLite (single-writer, in-process).
func TestCallLinkStoreMarshalError(t *testing.T) {
	db := dbtest.RunTestSQLite(t)
	s, err := store.New(db, dialect.NewSQLite())
	require.NoError(t, err)
	cls, err := store.NewCallLinkStore(db, dialect.NewSQLite())
	require.NoError(t, err)

	// Seed a completed link normally.
	seed := callLinkBaseStepStore("scan-err-child")
	seed.NewCallLink = &kernel.CallLink{
		ChildInstanceID:  "scan-err-child",
		ParentInstanceID: "scan-err-parent",
		ParentCommandID:  "cmd-scan",
		ParentDefID:      "def-parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	tok, err := s.Create(t.Context(), seed)
	require.NoError(t, err)

	term := callLinkTerminalStepStore("scan-err-child")
	term.CallOutcome = &kernel.CallOutcome{Completed: true, Output: map[string]any{"ok": float64(1)}}
	_, err = s.Commit(t.Context(), tok, term)
	require.NoError(t, err)

	// Corrupt the output column to be invalid JSON directly via raw SQL.
	_, err = db.ExecContext(t.Context(),
		`UPDATE wrkflw_call_links SET output = 'not-valid-json' WHERE child_instance_id = 'scan-err-child'`,
	)
	require.NoError(t, err)

	// ClaimPending must surface the unmarshal error.
	_, err = cls.ClaimPending(t.Context(), 10)
	require.Error(t, err, "ClaimPending must surface json unmarshal error")
	require.Contains(t, err.Error(), "unmarshal", "error must reference unmarshal path")
}

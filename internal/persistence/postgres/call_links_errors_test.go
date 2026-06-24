package postgres_test

// call_links_errors_test.go — error-branch coverage for the call-link code paths:
//
//   - insertCallLink INSERT error  (store.go:314)  — PK-violation on duplicate child_instance_id
//   - flipCallLink marshal error   (store.go:336)  — non-marshalable Output value
//   - CallLinkStore.MarkNotified   (call_links.go:133) — closed-pool Exec error
//   - CallLinkStore.ClaimPending   (call_links.go:63)  — closed-pool Query error
//   - CallLinkStore.LookupChild    (call_links.go:162) — closed-pool QueryRow error
//
// These tests lift internal/persistence/postgres coverage to ≥85% without
// touching any production file.

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestInsertCallLinkDuplicateError verifies that Store.Create returns an error
// when a call link row with the same child_instance_id already exists (PK violation),
// hitting the insertCallLink INSERT-error branch (store.go:314).
//
// Strategy: insert a call-link row for "dup-child" via a first Create (instance_id =
// "dup-child", NewCallLink.ChildInstanceID = "dup-child"). Then try a SECOND Create
// for a DIFFERENT instance ("dup-child-b") but with NewCallLink.ChildInstanceID still
// pointing at "dup-child" — the wrkflw_instances INSERT for "dup-child-b" succeeds,
// but insertCallLink immediately fails with a PK violation on wrkflw_call_links.
func TestInsertCallLinkDuplicateError(t *testing.T) {
	store, pool := newCallLinkStore(t)

	// First Create: seed a running link with child_instance_id = "dup-child".
	firstStep := callLinkBaseStep("dup-child")
	firstStep.NewCallLink = &runtime.CallLink{
		ChildInstanceID:  "dup-child",
		ParentInstanceID: "dup-parent",
		ParentCommandID:  "cmd-dup-1",
		ParentDefID:      "def-parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	_, err := store.Create(t.Context(), firstStep)
	require.NoError(t, err)

	// Verify the link row exists.
	var count int
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM wrkflw_call_links WHERE child_instance_id = $1`, "dup-child",
	).Scan(&count))
	require.Equal(t, 1, count)

	// Second Create: different instance_id ("dup-child-b") but same
	// NewCallLink.ChildInstanceID = "dup-child". The instances INSERT succeeds
	// (new instance_id), but insertCallLink hits a PK violation on
	// wrkflw_call_links and returns an error — covering the error branch.
	secondStep := callLinkBaseStep("dup-child-b")
	secondStep.NewCallLink = &runtime.CallLink{
		ChildInstanceID:  "dup-child", // same as above — PK violation
		ParentInstanceID: "dup-parent-b",
		ParentCommandID:  "cmd-dup-2",
		ParentDefID:      "def-parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	_, err = store.Create(t.Context(), secondStep)
	require.Error(t, err, "duplicate child_instance_id in wrkflw_call_links must cause insertCallLink to error")
}

// TestFlipCallLinkMarshalError verifies that Store.Commit returns a marshal
// error when CallOutcome.Output contains a non-JSON-marshalable value (a channel),
// hitting the flipCallLink marshal-error branch (store.go:336).
func TestFlipCallLinkMarshalError(t *testing.T) {
	store, _ := newCallLinkStore(t)

	// Seed a child instance + running link so the row is present.
	link := &runtime.CallLink{
		ChildInstanceID:  "err-child-marshal",
		ParentInstanceID: "err-parent-marshal",
		ParentCommandID:  "cmd-marshal",
		ParentDefID:      "def-parent",
		ParentDefVersion: 1,
		Depth:            1,
	}
	createStep := callLinkBaseStep("err-child-marshal")
	createStep.NewCallLink = link
	tok, err := store.Create(t.Context(), createStep)
	require.NoError(t, err)

	// Commit with a non-marshalable Output — json.Marshal(map[string]any{"bad": make(chan int)})
	// returns "json: unsupported type: chan int", which flipCallLink wraps and returns.
	termStep := callLinkTerminalStep("err-child-marshal")
	termStep.CallOutcome = &runtime.CallOutcome{
		Completed: true,
		Output:    map[string]any{"bad": make(chan int)},
	}
	_, err = store.Commit(t.Context(), tok, termStep)
	require.Error(t, err, "non-marshalable CallOutcome.Output must cause flipCallLink to return an error")
	require.Contains(t, err.Error(), "call link", "error message must reference call link path")
}

// TestCallLinkStoreClosedPoolErrors verifies that all three CallLinkStore
// methods (ClaimPending, MarkNotified, LookupChild) surface an error when
// the underlying pool is closed, covering their Query/Exec/QueryRow error
// branches (call_links.go:63, :133, :162).
//
// Each sub-test gets its own isolated pool (from database.RunTestDatabase) so
// closing it does not affect any other test.
func TestCallLinkStoreClosedPoolErrors(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"ClaimPending returns error on closed pool": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				cls := pg.NewCallLinkStore(pool)

				// Close the pool so the subsequent Query fails immediately.
				pool.Close()

				_, err := cls.ClaimPending(t.Context(), 10)
				require.Error(t, err, "ClaimPending on a closed pool must return an error")
				require.Contains(t, err.Error(), "claim", "error must be wrapped with claim context")
			},
		},
		"MarkNotified returns error on closed pool": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				cls := pg.NewCallLinkStore(pool)

				// Close the pool so Exec fails.
				pool.Close()

				err := cls.MarkNotified(t.Context(), "any-child-id")
				require.Error(t, err, "MarkNotified on a closed pool must return an error")
				require.Contains(t, err.Error(), "mark notified", "error must be wrapped with mark-notified context")
			},
		},
		"LookupChild returns error on closed pool": {
			assert: func(t *testing.T) {
				pool := database.RunTestDatabase(t)
				require.NoError(t, pg.Migrate(t.Context(), pool))
				cls := pg.NewCallLinkStore(pool)

				// Close the pool so QueryRow.Scan fails.
				pool.Close()

				_, _, err := cls.LookupChild(t.Context(), "any-child-id")
				require.Error(t, err, "LookupChild on a closed pool must return an error")
				require.Contains(t, err.Error(), "lookup", "error must be wrapped with lookup context")
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

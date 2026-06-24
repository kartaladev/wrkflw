package postgres_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// newCallLinkReader returns a freshly migrated CallLinkStore + pool for read-side tests.
func newCallLinkReader(t *testing.T) (runtime.CallLinkStore, *pgxpool.Pool) {
	t.Helper()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	return pg.NewCallLinkStore(pool), pool
}

// newCallLinkStore returns a freshly migrated store + pool for call-link tests.
func newCallLinkStore(t *testing.T) (*pg.Store, *pgxpool.Pool) {
	t.Helper()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	return pg.NewStore(pool), pool
}

func callLinkBaseStep(instanceID string) runtime.AppliedStep {
	now := time.Unix(1700000000, 0).UTC()
	return runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: instanceID,
			DefID:      "def-parent",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, map[string]any{"k": "v"}),
	}
}

func callLinkTerminalStep(instanceID string) runtime.AppliedStep {
	now := time.Unix(1700000000, 0).UTC()
	return runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: instanceID,
			DefID:      "def-parent",
			DefVersion: 1,
			Status:     engine.StatusCompleted,
			StartedAt:  now,
		},
		Trigger: engine.NewStartInstance(now, nil),
	}
}

// TestStoreCallLink verifies the atomic call-link side-effects on Store.Create
// and Store.Commit (ADR-0025):
//
//   - Create with NewCallLink inserts a wrkflw_call_links row (status='running')
//     in the same transaction as the instance INSERT.
//   - Commit with CallOutcome{Completed:true} flips the row to status='completed'
//     with the output JSONB.
//   - Commit with CallOutcome{Completed:false} flips the row to status='failed'
//     with the error text.
//   - A root instance (no call link) with CallOutcome set is a clean no-op
//     (zero rows affected — not an error).
func TestStoreCallLink(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"create with NewCallLink writes running row": {
			assert: func(t *testing.T) {
				store, pool := newCallLinkStore(t)

				step := callLinkBaseStep("cl-child-1")
				step.NewCallLink = &runtime.CallLink{
					ChildInstanceID:  "cl-child-1",
					ParentInstanceID: "cl-parent",
					ParentCommandID:  "cmd-001",
					ParentDefID:      "def-parent",
					ParentDefVersion: 1,
					Depth:            1,
				}
				_, err := store.Create(t.Context(), step)
				require.NoError(t, err)

				var status, parentID, commandID, defID string
				var defVersion, depth int
				err = pool.QueryRow(t.Context(),
					`SELECT status, parent_instance_id, parent_command_id, parent_def_id, parent_def_version, depth
					   FROM wrkflw_call_links WHERE child_instance_id = $1`,
					"cl-child-1",
				).Scan(&status, &parentID, &commandID, &defID, &defVersion, &depth)
				require.NoError(t, err)
				require.Equal(t, "running", status)
				require.Equal(t, "cl-parent", parentID)
				require.Equal(t, "cmd-001", commandID)
				require.Equal(t, "def-parent", defID)
				require.Equal(t, 1, defVersion)
				require.Equal(t, 1, depth)
			},
		},
		"commit with completed outcome flips row to completed with output": {
			assert: func(t *testing.T) {
				store, pool := newCallLinkStore(t)

				// Create child instance + link.
				createStep := callLinkBaseStep("cl-child-2")
				createStep.NewCallLink = &runtime.CallLink{
					ChildInstanceID:  "cl-child-2",
					ParentInstanceID: "cl-parent",
					ParentCommandID:  "cmd-002",
					ParentDefID:      "def-parent",
					ParentDefVersion: 1,
					Depth:            1,
				}
				tok, err := store.Create(t.Context(), createStep)
				require.NoError(t, err)

				// Commit with a completed outcome.
				termStep := callLinkTerminalStep("cl-child-2")
				termStep.CallOutcome = &runtime.CallOutcome{
					Completed: true,
					Output:    map[string]any{"result": float64(42), "msg": "ok"},
				}
				_, err = store.Commit(t.Context(), tok, termStep)
				require.NoError(t, err)

				var status string
				var outputJSON []byte
				err = pool.QueryRow(t.Context(),
					`SELECT status, output FROM wrkflw_call_links WHERE child_instance_id = $1`,
					"cl-child-2",
				).Scan(&status, &outputJSON)
				require.NoError(t, err)
				require.Equal(t, "completed", status)
				require.NotNil(t, outputJSON)
				require.Contains(t, string(outputJSON), `"result"`)
			},
		},
		"commit with failed outcome flips row to failed with error": {
			assert: func(t *testing.T) {
				store, pool := newCallLinkStore(t)

				// Create child instance + link.
				createStep := callLinkBaseStep("cl-child-3")
				createStep.NewCallLink = &runtime.CallLink{
					ChildInstanceID:  "cl-child-3",
					ParentInstanceID: "cl-parent",
					ParentCommandID:  "cmd-003",
					ParentDefID:      "def-parent",
					ParentDefVersion: 1,
					Depth:            1,
				}
				tok, err := store.Create(t.Context(), createStep)
				require.NoError(t, err)

				// Commit with a failed outcome.
				termStep := callLinkTerminalStep("cl-child-3")
				termStep.CallOutcome = &runtime.CallOutcome{
					Completed: false,
					Err:       "child process failed: timeout",
				}
				_, err = store.Commit(t.Context(), tok, termStep)
				require.NoError(t, err)

				var status string
				var errText *string
				err = pool.QueryRow(t.Context(),
					`SELECT status, error FROM wrkflw_call_links WHERE child_instance_id = $1`,
					"cl-child-3",
				).Scan(&status, &errText)
				require.NoError(t, err)
				require.Equal(t, "failed", status)
				require.NotNil(t, errText)
				require.Equal(t, "child process failed: timeout", *errText)
			},
		},
		"commit with CallOutcome on root instance (no link row) is a no-op": {
			assert: func(t *testing.T) {
				store, _ := newCallLinkStore(t)

				// Create a root instance (no NewCallLink).
				tok, err := store.Create(t.Context(), callLinkBaseStep("cl-root-inst"))
				require.NoError(t, err)

				// Commit with CallOutcome — no link row exists; must be clean no-op.
				termStep := callLinkTerminalStep("cl-root-inst")
				termStep.CallOutcome = &runtime.CallOutcome{
					Completed: true,
					Output:    map[string]any{"x": float64(1)},
				}
				_, err = store.Commit(t.Context(), tok, termStep)
				require.NoError(t, err, "zero-row UPDATE for root instance must not be an error")
			},
		},
		"create without NewCallLink is unaffected": {
			assert: func(t *testing.T) {
				store, pool := newCallLinkStore(t)

				// Existing callers pass nil NewCallLink — must work identically.
				tok, err := store.Create(t.Context(), callLinkBaseStep("cl-plain-inst"))
				require.NoError(t, err)
				require.Greater(t, int64(tok), int64(0))

				// Verify no link row was inserted.
				var count int
				err = pool.QueryRow(t.Context(),
					`SELECT COUNT(*) FROM wrkflw_call_links WHERE child_instance_id = $1`,
					"cl-plain-inst",
				).Scan(&count)
				require.NoError(t, err)
				require.Equal(t, 0, count)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Sequential: each sub-test spins up its own containerized database.
			tc.assert(t)
		})
	}
}

// seedCompletedLink inserts a child instance + call link via Store (Task 5's
// atomic write path), then flips it to terminal by calling store.Commit with
// the given outcome.
func seedCompletedLink(t *testing.T, store *pg.Store, pool *pgxpool.Pool, childID string, outcome runtime.CallOutcome) {
	t.Helper()

	link := &runtime.CallLink{
		ChildInstanceID:  childID,
		ParentInstanceID: "parent-" + childID,
		ParentCommandID:  "cmd-" + childID,
		ParentDefID:      "def-parent",
		ParentDefVersion: 1,
		Depth:            1,
	}

	createStep := callLinkBaseStep(childID)
	createStep.NewCallLink = link
	tok, err := store.Create(t.Context(), createStep)
	require.NoError(t, err)

	termStep := callLinkTerminalStep(childID)
	termStep.CallOutcome = &outcome
	_, err = store.Commit(t.Context(), tok, termStep)
	require.NoError(t, err)
}

// TestCallLinkStoreClaimPending verifies ClaimPending on the Postgres CallLinkStore.
func TestCallLinkStoreClaimPending(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"returns terminal unnotified rows in stable (child_instance_id) order": {
			assert: func(t *testing.T) {
				cls, pool := newCallLinkReader(t)
				store := pg.NewStore(pool)

				// Seed two completed links with IDs that differ in lex order.
				seedCompletedLink(t, store, pool, "zzz-child", runtime.CallOutcome{Completed: true, Output: map[string]any{"a": float64(1)}})
				seedCompletedLink(t, store, pool, "aaa-child", runtime.CallOutcome{Completed: false, Err: "boom"})

				pending, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, pending, 2)
				// Stable order: aaa-child < zzz-child.
				require.Equal(t, "aaa-child", pending[0].Link.ChildInstanceID)
				require.Equal(t, "zzz-child", pending[1].Link.ChildInstanceID)
			},
		},
		"respects limit parameter": {
			assert: func(t *testing.T) {
				cls, pool := newCallLinkReader(t)
				store := pg.NewStore(pool)

				seedCompletedLink(t, store, pool, "lim-child-1", runtime.CallOutcome{Completed: true})
				seedCompletedLink(t, store, pool, "lim-child-2", runtime.CallOutcome{Completed: true})
				seedCompletedLink(t, store, pool, "lim-child-3", runtime.CallOutcome{Completed: true})

				pending, err := cls.ClaimPending(t.Context(), 2)
				require.NoError(t, err)
				require.Len(t, pending, 2)
			},
		},
		"excludes running (non-terminal) rows": {
			assert: func(t *testing.T) {
				cls, pool := newCallLinkReader(t)
				store := pg.NewStore(pool)

				// Create a child instance with a link but do NOT commit — stays 'running'.
				link := &runtime.CallLink{
					ChildInstanceID:  "running-child",
					ParentInstanceID: "parent-x",
					ParentCommandID:  "cmd-x",
					ParentDefID:      "def-parent",
					ParentDefVersion: 1,
					Depth:            1,
				}
				createStep := callLinkBaseStep("running-child")
				createStep.NewCallLink = link
				_, err := store.Create(t.Context(), createStep)
				require.NoError(t, err)

				pending, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Empty(t, pending)
			},
		},
		"output JSONB round-trips into Outcome.Output": {
			assert: func(t *testing.T) {
				cls, pool := newCallLinkReader(t)
				store := pg.NewStore(pool)

				want := map[string]any{"result": float64(99), "label": "ok"}
				seedCompletedLink(t, store, pool, "json-child", runtime.CallOutcome{Completed: true, Output: want})

				pending, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, pending, 1)

				pn := pending[0]
				require.True(t, pn.Outcome.Completed)
				require.Equal(t, want, pn.Outcome.Output)
			},
		},
		"failed outcome maps Completed=false with Err string": {
			assert: func(t *testing.T) {
				cls, pool := newCallLinkReader(t)
				store := pg.NewStore(pool)

				seedCompletedLink(t, store, pool, "fail-child", runtime.CallOutcome{Completed: false, Err: "child timed out"})

				pending, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, pending, 1)

				pn := pending[0]
				require.False(t, pn.Outcome.Completed)
				require.Equal(t, "child timed out", pn.Outcome.Err)
				require.Nil(t, pn.Outcome.Output)
			},
		},
		"limit zero returns all rows": {
			assert: func(t *testing.T) {
				cls, pool := newCallLinkReader(t)
				store := pg.NewStore(pool)

				seedCompletedLink(t, store, pool, "zero-lim-1", runtime.CallOutcome{Completed: true})
				seedCompletedLink(t, store, pool, "zero-lim-2", runtime.CallOutcome{Completed: true})

				pending, err := cls.ClaimPending(t.Context(), 0)
				require.NoError(t, err)
				require.Len(t, pending, 2)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

// TestCallLinkStoreMarkNotified verifies MarkNotified on the Postgres CallLinkStore.
func TestCallLinkStoreMarkNotified(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"notified row is excluded from subsequent ClaimPending": {
			assert: func(t *testing.T) {
				cls, pool := newCallLinkReader(t)
				store := pg.NewStore(pool)

				seedCompletedLink(t, store, pool, "notif-child-1", runtime.CallOutcome{Completed: true})
				seedCompletedLink(t, store, pool, "notif-child-2", runtime.CallOutcome{Completed: true})

				// Mark notif-child-1 as notified.
				require.NoError(t, cls.MarkNotified(t.Context(), "notif-child-1"))

				pending, err := cls.ClaimPending(t.Context(), 10)
				require.NoError(t, err)
				require.Len(t, pending, 1)
				require.Equal(t, "notif-child-2", pending[0].Link.ChildInstanceID)
			},
		},
		"MarkNotified stamps notified_at and sets status to notified": {
			assert: func(t *testing.T) {
				_, pool := newCallLinkReader(t)
				cls := pg.NewCallLinkStore(pool)
				store := pg.NewStore(pool)

				seedCompletedLink(t, store, pool, "stamp-child", runtime.CallOutcome{Completed: true})

				before := time.Now().UTC()
				require.NoError(t, cls.MarkNotified(t.Context(), "stamp-child"))
				after := time.Now().UTC()

				var status string
				var notifiedAt *time.Time
				err := pool.QueryRow(t.Context(),
					`SELECT status, notified_at FROM wrkflw_call_links WHERE child_instance_id = $1`,
					"stamp-child",
				).Scan(&status, &notifiedAt)
				require.NoError(t, err)
				require.Equal(t, "notified", status)
				require.NotNil(t, notifiedAt)
				require.True(t, !notifiedAt.Before(before.Add(-time.Second)), "notified_at should not predate the call")
				require.True(t, !notifiedAt.After(after.Add(time.Second)), "notified_at should not be in the future")
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

// TestCallLinkStoreLookupChild verifies LookupChild on the Postgres CallLinkStore.
func TestCallLinkStoreLookupChild(t *testing.T) {
	tests := map[string]struct {
		assert func(t *testing.T)
	}{
		"returns the link for a known child instance": {
			assert: func(t *testing.T) {
				cls, pool := newCallLinkReader(t)
				store := pg.NewStore(pool)

				seedCompletedLink(t, store, pool, "lookup-child", runtime.CallOutcome{Completed: true})

				link, ok, err := cls.LookupChild(t.Context(), "lookup-child")
				require.NoError(t, err)
				require.True(t, ok)
				require.Equal(t, "lookup-child", link.ChildInstanceID)
				require.Equal(t, "parent-lookup-child", link.ParentInstanceID)
				require.Equal(t, "cmd-lookup-child", link.ParentCommandID)
				require.Equal(t, "def-parent", link.ParentDefID)
				require.Equal(t, 1, link.ParentDefVersion)
				require.Equal(t, 1, link.Depth)
			},
		},
		"returns ok=false for unknown child instance ID": {
			assert: func(t *testing.T) {
				cls, _ := newCallLinkReader(t)

				link, ok, err := cls.LookupChild(t.Context(), "nonexistent-child")
				require.NoError(t, err)
				require.False(t, ok)
				require.Equal(t, runtime.CallLink{}, link)
			},
		},
		"lookup works on running (not yet terminal) link": {
			assert: func(t *testing.T) {
				cls, pool := newCallLinkReader(t)
				store := pg.NewStore(pool)

				callLink := &runtime.CallLink{
					ChildInstanceID:  "running-lookup",
					ParentInstanceID: "parent-running-lookup",
					ParentCommandID:  "cmd-rl",
					ParentDefID:      "def-parent",
					ParentDefVersion: 2,
					Depth:            3,
				}
				createStep := callLinkBaseStep("running-lookup")
				createStep.NewCallLink = callLink
				_, err := store.Create(t.Context(), createStep)
				require.NoError(t, err)

				link, ok, err := cls.LookupChild(t.Context(), "running-lookup")
				require.NoError(t, err)
				require.True(t, ok)
				require.Equal(t, "running-lookup", link.ChildInstanceID)
				require.Equal(t, 2, link.ParentDefVersion)
				require.Equal(t, 3, link.Depth)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t)
		})
	}
}

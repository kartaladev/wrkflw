package postgres_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/database"
	"github.com/zakyalvan/krtlwrkflw/engine"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

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

package postgres_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func TestStoreHistoryCapTrimsClosedVisitsOnLoad(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, postgres.Migrate(t.Context(), pool))

	st := postgres.NewStore(pool, postgres.WithHistoryCap(1))

	base := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	state := engine.InstanceState{
		InstanceID: "cap-1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning,
		StartedAt: base,
		History: []engine.NodeVisit{
			open("human", base), // long-parked open visit
			closed("c1", base.Add(1*time.Minute)),
			closed("c2", base.Add(2*time.Minute)),
		},
	}
	_, err := st.Create(t.Context(), runtime.AppliedStep{
		State:   state,
		Trigger: engine.NewStartInstance(base, nil),
	})
	require.NoError(t, err)

	got, _, err := st.Load(t.Context(), "cap-1")
	require.NoError(t, err)

	// Open visit retained; closed trimmed to the most recent 1 (c2).
	assert.Len(t, got.History, 2)
	assert.Equal(t, "human", got.History[0].NodeID)
	assert.Nil(t, got.History[0].LeftAt)
	assert.Equal(t, "c2", got.History[1].NodeID)

	// And the raw JSONB column was capped (not just the read path).
	var snap []byte
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT snapshot FROM wrkflw_instances WHERE instance_id = $1`, "cap-1").Scan(&snap))
	var raw engine.InstanceState
	require.NoError(t, json.Unmarshal(snap, &raw))
	assert.Len(t, raw.History, 2)
}

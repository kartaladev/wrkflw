package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

func ptrTime(t time.Time) *time.Time { return &t }

// closed builds a closed visit (LeftAt set); open builds an open visit.
func closedVisit(node string, at time.Time) engine.NodeVisit {
	return engine.NodeVisit{NodeID: node, TokenID: node + "-tok", EnteredAt: at, LeftAt: ptrTime(at.Add(time.Second))}
}
func openVisit(node string, at time.Time) engine.NodeVisit {
	return engine.NodeVisit{NodeID: node, TokenID: node + "-tok", EnteredAt: at}
}

func TestCapHistory(t *testing.T) {
	base := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		hist   []engine.NodeVisit
		n      int
		assert func(t *testing.T, got []engine.NodeVisit)
	}{
		{
			name: "n<=0 is a no-op",
			hist: []engine.NodeVisit{closedVisit("a", base), closedVisit("b", base)},
			n:    0,
			assert: func(t *testing.T, got []engine.NodeVisit) {
				assert.Len(t, got, 2)
			},
		},
		{
			name: "old open visit behind many closed visits survives the cap",
			hist: []engine.NodeVisit{
				openVisit("human", base), // the long-parked open visit, oldest
				closedVisit("c1", base.Add(1*time.Minute)),
				closedVisit("c2", base.Add(2*time.Minute)),
				closedVisit("c3", base.Add(3*time.Minute)),
			},
			n: 1, // keep only 1 closed visit
			assert: func(t *testing.T, got []engine.NodeVisit) {
				// open visit retained + most-recent 1 closed (c3); order preserved.
				assert.Len(t, got, 2)
				assert.Equal(t, "human", got[0].NodeID)
				assert.Nil(t, got[0].LeftAt)
				assert.Equal(t, "c3", got[1].NodeID)
			},
		},
		{
			name: "closed visits trimmed to most-recent n, all opens kept",
			hist: []engine.NodeVisit{
				closedVisit("c1", base.Add(1*time.Minute)),
				closedVisit("c2", base.Add(2*time.Minute)),
				openVisit("o1", base.Add(3*time.Minute)),
				closedVisit("c3", base.Add(4*time.Minute)),
			},
			n: 2,
			assert: func(t *testing.T, got []engine.NodeVisit) {
				// keep c2, o1, c3 (drop c1 — oldest closed beyond the 2 most recent).
				assert.Len(t, got, 3)
				assert.Equal(t, []string{"c2", "o1", "c3"}, []string{got[0].NodeID, got[1].NodeID, got[2].NodeID})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := engine.InstanceState{InstanceID: "i1", History: tc.hist}
			got := store.CapHistory(st, tc.n)
			tc.assert(t, got.History)
			// capHistory must not mutate the input slice.
			assert.Len(t, st.History, len(tc.hist))
		})
	}
}

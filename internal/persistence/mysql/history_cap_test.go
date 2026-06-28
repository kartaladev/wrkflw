package mysql_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
)

func TestCapHistory(t *testing.T) {
	now := time.Now()
	left := now.Add(time.Minute)

	openVisit := engine.NodeVisit{NodeID: "open", TokenID: "t1", EnteredAt: now}
	closedVisit := func(id string) engine.NodeVisit {
		return engine.NodeVisit{NodeID: id, TokenID: "t1", EnteredAt: now, LeftAt: &left}
	}

	tests := map[string]struct {
		state engine.InstanceState
		cap   int
		check func(t *testing.T, got engine.InstanceState)
	}{
		"no cap (n<=0) returns unchanged": {
			state: engine.InstanceState{History: []engine.NodeVisit{closedVisit("a"), closedVisit("b"), closedVisit("c")}},
			cap:   0,
			check: func(t *testing.T, got engine.InstanceState) {
				require.Len(t, got.History, 3)
			},
		},
		"cap larger than closed count returns unchanged": {
			state: engine.InstanceState{History: []engine.NodeVisit{closedVisit("a"), closedVisit("b")}},
			cap:   5,
			check: func(t *testing.T, got engine.InstanceState) {
				require.Len(t, got.History, 2)
			},
		},
		"cap trims oldest closed visits, retains open": {
			state: engine.InstanceState{
				History: []engine.NodeVisit{
					closedVisit("old1"),
					closedVisit("old2"),
					openVisit,
					closedVisit("new3"),
				},
			},
			cap: 1,
			check: func(t *testing.T, got engine.InstanceState) {
				// Keeps: 1 newest closed (new3) + 1 open visit = 2 total.
				require.Len(t, got.History, 2)
				// Open visit must always be retained.
				hasOpen := false
				for _, v := range got.History {
					if v.NodeID == "open" {
						hasOpen = true
					}
				}
				require.True(t, hasOpen, "open visit must be retained")
				// The newest closed visit (new3) must be kept.
				hasNew3 := false
				for _, v := range got.History {
					if v.NodeID == "new3" {
						hasNew3 = true
					}
				}
				require.True(t, hasNew3, "newest closed visit must be kept")
			},
		},
		"cap exactly equal to closed count returns unchanged": {
			state: engine.InstanceState{History: []engine.NodeVisit{closedVisit("a"), closedVisit("b")}},
			cap:   2,
			check: func(t *testing.T, got engine.InstanceState) {
				require.Len(t, got.History, 2)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := mypkg.CapHistory(tc.state, tc.cap)
			tc.check(t, got)
		})
	}
}

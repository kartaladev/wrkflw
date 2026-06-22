package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

func TestTimerOpsFor(t *testing.T) {
	at := time.Date(2026, 6, 22, 11, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		cmds   []engine.Command
		trg    engine.Trigger
		assert func(t *testing.T, arms []ArmedTimer, cancels []string)
	}{
		{
			name: "ScheduleTimer becomes an arm",
			cmds: []engine.Command{engine.ScheduleTimer{TimerID: "t1", FireAt: at, Kind: engine.TimerIntermediate}},
			trg:  engine.NewStartInstance(at, nil),
			assert: func(t *testing.T, arms []ArmedTimer, cancels []string) {
				assert.Len(t, arms, 1)
				assert.Equal(t, "t1", arms[0].TimerID)
				assert.Equal(t, at, arms[0].FireAt)
				assert.Empty(t, cancels)
			},
		},
		{
			name: "CancelTimer becomes a cancel",
			cmds: []engine.Command{engine.CancelTimer{TimerID: "t1"}},
			trg:  engine.NewStartInstance(at, nil),
			assert: func(t *testing.T, arms []ArmedTimer, cancels []string) {
				assert.Empty(t, arms)
				assert.Equal(t, []string{"t1"}, cancels)
			},
		},
		{
			name: "TimerFired trigger cancels the fired timer",
			cmds: nil,
			trg:  engine.NewTimerFired(at, "t1"),
			assert: func(t *testing.T, arms []ArmedTimer, cancels []string) {
				assert.Empty(t, arms)
				assert.Equal(t, []string{"t1"}, cancels)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arms, cancels := timerOpsFor(tc.cmds, tc.trg, "d", 1, "i1")
			tc.assert(t, arms, cancels)
		})
	}
}

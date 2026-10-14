package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// TestBaselineJobStoreLoadKeepsTheHealthyRowArmable characterizes the
// restart-recovery scenario "Unarmable rows are dropped, healthy rows survive"
// (openspec/specs/restart-recovery) for the row the neighbouring
// TestJobStoreLoadSkipsARowItCannotArm leaves unasserted: on every example row,
// the healthy hourly row is returned with a NON-ZERO next run.
func TestBaselineJobStoreLoadKeepsTheHealthyRowArmable(t *testing.T) {
	feb := time.Date(2026, 2, 10, 9, 30, 0, 0, time.UTC)
	healthy := armedRow("i-ok", "t-ok", schedule.Every(time.Hour), feb.Add(time.Hour))

	cases := []struct {
		name string
		row  kernel.ArmedTimer
	}{
		{
			name: "beside every 12 months on day 31",
			row:  armedRow("i1", "t1", schedule.Monthly(12, []int{31}), time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)),
		},
		{
			name: "beside every 0 with a zero next run",
			row:  armedRow("i2", "t2", schedule.Every(0), time.Time{}),
		},
		{
			name: "beside every 1h with a zero next run",
			row:  armedRow("i3", "t3", schedule.Every(time.Hour), time.Time{}),
		},
		{
			name: "beside weekly on Monday",
			row:  armedRow("i4", "t4", schedule.Weekly(1, []time.Weekday{time.Monday}), feb.AddDate(0, 0, 6)),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids, nexts := loadJobIDs(t, feb, healthy, tc.row)

			require.Contains(t, ids, healthy.TimerID, "the healthy row must be returned")
			assert.False(t, nexts[healthy.TimerID].IsZero(), "the healthy row must carry a non-zero next run")
		})
	}
}

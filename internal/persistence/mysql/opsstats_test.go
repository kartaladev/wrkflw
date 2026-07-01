package mysql_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestRelayOutboxStats verifies that (*Relay).OutboxStats returns counts and
// OldestPendingAge derived from the live wrkflw_outbox table.
func TestRelayOutboxStats(t *testing.T) {
	cases := []struct {
		name   string
		seed   bool
		assert func(t *testing.T, got runtime.OutboxStats)
	}{
		{
			name: "empty table — zero values",
			seed: false,
			assert: func(t *testing.T, got runtime.OutboxStats) {
				t.Helper()
				require.Equal(t, int64(0), got.Pending)
				require.Equal(t, int64(0), got.Dead)
				require.Equal(t, time.Duration(0), got.OldestPendingAge)
			},
		},
		{
			name: "2 pending + 1 dead + 1 published",
			seed: true,
			assert: func(t *testing.T, got runtime.OutboxStats) {
				t.Helper()
				require.Equal(t, int64(2), got.Pending)
				require.Equal(t, int64(1), got.Dead)
				require.Greater(t, got.OldestPendingAge, time.Duration(0),
					"OldestPendingAge must be > 0 when pending rows exist")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := dbtest.RunTestMySQL(t)

			if tc.seed {
				now := time.Now().UTC().Add(-2 * time.Second) // ensure age > 0
				rows := []struct {
					dedup  string
					status string
				}{
					{"ops-pending-1", "pending"},
					{"ops-pending-2", "pending"},
					{"ops-dead-1", "dead"},
					{"ops-published-1", "published"},
				}
				for _, r := range rows {
					_, err := db.ExecContext(t.Context(),
						`INSERT INTO wrkflw_outbox
						   (instance_id, topic, payload, dedup_key, status, created_at, next_attempt_at)
						 VALUES (?, ?, ?, ?, ?, ?, ?)`,
						"opsstats-instance", "ops.event", `{"k":"v"}`, r.dedup, r.status, now, now,
					)
					require.NoError(t, err, "seed row %s", r.dedup)
				}
			}

			relay := mypkg.NewRelay(db, &recordingPub{})
			got, err := relay.OutboxStats(t.Context())
			require.NoError(t, err)
			tc.assert(t, got)
		})
	}
}

// TestTimerStoreStats verifies that (*TimerStore).Stats returns the count and
// NextFireAt from the live wrkflw_timers table.
func TestTimerStoreStats(t *testing.T) {
	cases := []struct {
		name   string
		seed   bool
		assert func(t *testing.T, got runtime.TimerStats)
	}{
		{
			name: "empty table — zero values and nil NextFireAt",
			seed: false,
			assert: func(t *testing.T, got runtime.TimerStats) {
				t.Helper()
				assert.Equal(t, int64(0), got.Armed)
				assert.Nil(t, got.NextFireAt, "NextFireAt must be nil when no timers exist")
			},
		},
		{
			name: "2 armed timers — count=2 and NextFireAt non-nil",
			seed: true,
			assert: func(t *testing.T, got runtime.TimerStats) {
				t.Helper()
				assert.Equal(t, int64(2), got.Armed)
				assert.NotNil(t, got.NextFireAt, "NextFireAt must be non-nil when timers exist")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := dbtest.RunTestMySQL(t)

			if tc.seed {
				now := time.Now().UTC()
				timers := []struct {
					timerID string
					fireAt  time.Time
				}{
					{"timer-1", now.Add(1 * time.Hour)},
					{"timer-2", now.Add(2 * time.Hour)},
				}
				for _, tm := range timers {
					_, err := db.ExecContext(t.Context(),
						`INSERT INTO wrkflw_timers (instance_id, def_id, def_version, timer_id, fire_at, kind)
						 VALUES (?, ?, ?, ?, ?, ?)`,
						"opsstats-timer-instance", "def-1", 1, tm.timerID, tm.fireAt, 0,
					)
					require.NoError(t, err, "seed timer %s", tm.timerID)
				}
			}

			ts := mypkg.NewTimerStore(db)
			got, err := ts.Stats(t.Context())
			require.NoError(t, err)
			tc.assert(t, got)
		})
	}
}

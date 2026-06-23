package postgres_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// fixedMarkNotifiedTime is the deterministic timestamp injected via fake clock.
var fixedMarkNotifiedTime = time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)

// TestMarkNotifiedUsesClock verifies that MarkNotified stamps notified_at using
// the injected clock.Clock rather than the wall clock. A FakeClock at a fixed
// point in time is injected; after MarkNotified the row's notified_at must equal
// exactly that fixed time (not an approximate wall-clock window).
func TestMarkNotifiedUsesClock(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	fc := clockwork.NewFakeClockAt(fixedMarkNotifiedTime)

	// Build the store with the fake clock injected.
	cls := pg.NewCallLinkStore(pool, pg.WithCallLinkClock(fc))
	store := pg.NewStore(pool)

	// Seed a terminal (completed) call link.
	seedCompletedLink(t, store, pool, "mn-clock-child", runtime.CallOutcome{Completed: true})

	// Call MarkNotified.
	require.NoError(t, cls.MarkNotified(t.Context(), "mn-clock-child"))

	// Read notified_at back from the DB and assert it equals the fake-clock time exactly.
	var notifiedAt time.Time
	err := pool.QueryRow(t.Context(),
		`SELECT notified_at FROM wrkflw_call_links WHERE child_instance_id = $1`,
		"mn-clock-child",
	).Scan(&notifiedAt)
	require.NoError(t, err)

	require.Equal(t, fixedMarkNotifiedTime, notifiedAt.UTC(),
		"notified_at must equal the fake-clock time, not wall-clock time")
}

package casbin_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authzcasbin "github.com/zakyalvan/krtlwrkflw/internal/authz/casbin"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
)

func TestPGWatcherNotifiesOtherNodesNotSelf(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	const channel = "wrkflw_casbin_policy_test"

	// Each watcher signals its ready channel once LISTEN is established, so the
	// test synchronises on the ACTUAL listen state rather than a sleep — closing
	// the NOTIFY-before-LISTEN race that previously made this test flaky.
	readyA := make(chan struct{}, 1)
	readyB := make(chan struct{}, 1)

	// Node A (the writer) and Node B (the observer), each with a watcher.
	wa := authzcasbin.NewPGWatcher(pool, channel, "node-A", readyA)
	defer wa.Close()
	wb := authzcasbin.NewPGWatcher(pool, channel, "node-B", readyB)
	defer wb.Close()

	var aCb, bCb atomic.Int64
	require.NoError(t, wa.SetUpdateCallback(func(string) { aCb.Add(1) }))
	require.NoError(t, wb.SetUpdateCallback(func(string) { bCb.Add(1) }))

	// Wait until BOTH listeners have established LISTEN before A notifies.
	waitListenReady(t, readyA)
	waitListenReady(t, readyB)

	// A signals a policy change (payload = "node-A").
	require.NoError(t, wa.Update())

	// B must observe it (payload "node-A" != "node-B"); A must NOT (self-filter).
	require.Eventually(t, func() bool { return bCb.Load() == 1 }, 5*time.Second, 25*time.Millisecond,
		"node B must reload on node A's policy change")
	assert.Equal(t, int64(0), aCb.Load(), "node A must not reload on its own change")
}

// waitListenReady blocks until the watcher signals its LISTEN is established, with
// a generous upper bound that only trips if establishment never happens.
func waitListenReady(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("watcher did not establish LISTEN within 10s")
	}
}

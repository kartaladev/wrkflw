package casbin_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authzcasbin "github.com/zakyalvan/krtlwrkflw/internal/authz/casbin"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
)

func TestPGWatcherNotifiesOtherNodesNotSelf(t *testing.T) {
	pool := database.RunTestDatabase(t)
	const channel = "wrkflw_casbin_policy_test"

	// Node A (the writer) and Node B (the observer), each with a watcher.
	wa := authzcasbin.NewPGWatcher(pool, channel, "node-A")
	defer wa.Close()
	wb := authzcasbin.NewPGWatcher(pool, channel, "node-B")
	defer wb.Close()

	var aCb, bCb atomic.Int64
	require.NoError(t, wa.SetUpdateCallback(func(string) { aCb.Add(1) }))
	require.NoError(t, wb.SetUpdateCallback(func(string) { bCb.Add(1) }))

	// Let both listeners establish their LISTEN before A notifies.
	time.Sleep(300 * time.Millisecond)

	// A signals a policy change (payload = "node-A").
	require.NoError(t, wa.Update())

	// B must observe it (payload "node-A" != "node-B"); A must NOT (self-filter).
	require.Eventually(t, func() bool { return bCb.Load() == 1 }, 5*time.Second, 25*time.Millisecond,
		"node B must reload on node A's policy change")
	assert.Equal(t, int64(0), aCb.Load(), "node A must not reload on its own change")
}

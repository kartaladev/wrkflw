package casbin_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authzcasbin "github.com/zakyalvan/krtlwrkflw/internal/authz/casbin"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
)

// TestNewDBEnforcer_WithWatcher exercises NewDBEnforcer when WatcherEnabled=true.
// It covers: NewDBEnforcer, the watcher-enabled branch, the SetWatcher path, the
// post-SetWatcher callback override, and watcherCloser.Close.
func TestNewDBEnforcer_WithWatcher(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, authzcasbin.MigrateCasbin(t.Context(), pool))

	enforcer, closer, err := authzcasbin.NewDBEnforcer(t.Context(), pool, authzcasbin.DBConfig{
		ModelText:      rbacModel,
		WatcherEnabled: true,
		WatcherChannel: "wrkflw_casbin_db_test",
		NodeID:         "test-node-1",
	})
	require.NoError(t, err)
	require.NotNil(t, enforcer)
	require.NotNil(t, closer)

	// Minimal enforcer exercise: add a policy and verify it can be loaded back.
	added, err := enforcer.AddPolicy("alice", "data1", "read")
	require.NoError(t, err)
	assert.True(t, added, "AddPolicy should succeed")

	allowed, err := enforcer.Enforce("alice", "data1", "read")
	require.NoError(t, err)
	assert.True(t, allowed, "alice should be allowed to read data1")

	// Close stops the watcher goroutine; covers watcherCloser.Close.
	assert.NoError(t, closer.Close())
}

// TestNewDBEnforcer_WithoutWatcher exercises NewDBEnforcer when WatcherEnabled=false.
// It covers: the watcher-disabled branch and noopCloser.Close.
func TestNewDBEnforcer_WithoutWatcher(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, authzcasbin.MigrateCasbin(t.Context(), pool))

	enforcer, closer, err := authzcasbin.NewDBEnforcer(t.Context(), pool, authzcasbin.DBConfig{
		ModelText:      rbacModel,
		WatcherEnabled: false,
	})
	require.NoError(t, err)
	require.NotNil(t, enforcer)
	require.NotNil(t, closer)

	// The closer is a no-op; it must return nil.
	assert.NoError(t, closer.Close(), "noopCloser.Close must return nil")

	// Verify the enforcer is functional despite no watcher.
	require.NoError(t, enforcer.LoadPolicy())
}

// TestNewDBEnforcer_InvalidModel exercises the model-parse error path in NewDBEnforcer.
// It covers the early-return error branch when the model text is invalid.
func TestNewDBEnforcer_InvalidModel(t *testing.T) {
	pool := database.RunTestDatabase(t)
	// No migration needed — we expect to fail before the adapter is used.

	enforcer, closer, err := authzcasbin.NewDBEnforcer(t.Context(), pool, authzcasbin.DBConfig{
		ModelText:      "this is not a valid casbin model",
		WatcherEnabled: false,
	})
	require.Error(t, err)
	assert.Nil(t, enforcer)
	assert.Nil(t, closer)
}

// TestPGWatcherUpdate_Error exercises pgWatcher.Update when the underlying pool
// is closed, which causes pg_notify to fail and returns a wrapped error.
// This covers the error branch in pgWatcher.Update (pg_watcher.go:60-62).
func TestPGWatcherUpdate_Error(t *testing.T) {
	pool := database.RunTestDatabase(t)

	w := authzcasbin.NewPGWatcher(pool, "wrkflw_casbin_update_err_test", "node-err", nil)
	defer w.Close()

	// Close the underlying pool so that the next Exec inside Update fails.
	pool.Close()

	err := w.Update()
	assert.Error(t, err, "Update must return an error when the pool is closed")
}

// TestPGWatcherListen_AcquireError exercises the reconnect path in pgWatcher.listen
// when pool.Acquire fails (pool closed before the watcher goroutine acquires a conn).
// Closing the watcher immediately after creation causes the listen goroutine to exit
// via the ctx-done fast path inside backoff, covering backoff's <-ctx.Done() branch.
func TestPGWatcherListen_AcquireError(t *testing.T) {
	pool := database.RunTestDatabase(t)

	// Close pool so that pool.Acquire will fail inside the listen goroutine.
	pool.Close()

	// Create watcher AFTER the pool is closed. The listen goroutine will
	// attempt pool.Acquire, get an error, check ctx.Err() == nil (not yet
	// cancelled), then enter backoff waiting on ctx.Done() or time.After.
	w := authzcasbin.NewPGWatcher(pool, "wrkflw_casbin_acquire_err_test", "node-acq-err", nil)

	// Give the goroutine time to attempt Acquire and enter backoff.
	time.Sleep(50 * time.Millisecond)

	// Close the watcher: cancels ctx, unblocking backoff via <-ctx.Done().
	// This also waits for the goroutine to finish (no goroutine leak).
	w.Close()
}

// TestPGWatcherListen_ListenExecError exercises the reconnect path in pgWatcher.listen
// when the LISTEN statement itself fails. An invalid channel name (starting with a digit)
// produces a Postgres syntax error, causing conn.Exec to return an error.
// The test closes the watcher after a brief wait, so the listen goroutine exits via
// backoff's <-ctx.Done() path rather than the 1-second reconnect timer.
// Covered: conn.Release(), the if-ctx.Err check after the LISTEN exec error,
// and the backoff+continue path (pg_watcher.go:88-94 minus the return branch).
func TestPGWatcherListen_ListenExecError(t *testing.T) {
	pool := database.RunTestDatabase(t)

	// "123invalid" starts with a digit: Postgres LISTEN requires a valid identifier,
	// so "LISTEN 123invalid" will return a syntax error from the server.
	w := authzcasbin.NewPGWatcher(pool, "123invalid_channel", "node-listen-err", nil)

	// Allow the goroutine time to acquire a connection, attempt LISTEN, fail, and
	// enter backoff.
	time.Sleep(150 * time.Millisecond)

	// Cancel ctx via Close(); backoff exits immediately via <-ctx.Done().
	w.Close()
}

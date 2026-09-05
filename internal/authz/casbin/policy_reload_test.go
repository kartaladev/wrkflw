package casbin_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	authzcasbin "github.com/kartaladev/wrkflw/internal/authz/casbin"
	"github.com/kartaladev/wrkflw/internal/dbtest"
)

// errReloadFailed is the sentinel the fake reload returns so the assertion can
// prove the ORIGINAL error text reaches the log, not a generic replacement.
//
// It deliberately contains no quotes or spaces-needing-quoting: slog's
// TextHandler escapes embedded quotes, which would make a Contains assertion
// fail on formatting rather than on substance.
var errReloadFailed = errors.New("adapter-relation-casbin_rule-does-not-exist")

// TestPolicyReloadCallback is the regression test for the silently swallowed
// cross-node policy reload (audit item 102).
//
// Before this fix the watcher callback was literally
// `func(string) { _ = enforcer.LoadPolicy() }`: a reload failure produced no log
// line, no metric, and no staleness signal, so a REVOKED permission kept
// answering Enforce=true, err=nil from the last successfully loaded policy.
//
// What makes this test fail today: the callback discards the error with `_ =`
// and the function contains no logger or counter call at all, so both the
// "logged" and the "counted" assertions have nothing to observe.
//
// Note this test deliberately does NOT assert that enforcement fails closed on a
// stale policy. That is a separate availability/security decision (see the
// doc comment on the callback); this fix only makes the failure observable.
func TestPolicyReloadCallback(t *testing.T) {
	t.Parallel()

	const (
		channel = "wrkflw_casbin_reload_test"
		nodeID  = "node-under-test"
		origin  = "peer-node"
	)

	type testCase struct {
		name   string
		reload func() error
		assert func(t *testing.T, logged string, reloadFailures int64)
	}

	cases := []testCase{
		{
			name:   "reload failure is logged at ERROR and counted",
			reload: func() error { return errReloadFailed },
			assert: func(t *testing.T, logged string, reloadFailures int64) {
				require.NotEmpty(t, logged, "a failing reload must produce a log record")
				assert.Contains(t, logged, "ERROR", "the record must be at ERROR level")
				assert.Contains(t, logged, errReloadFailed.Error(),
					"the original adapter error must survive into the log")
				assert.Contains(t, logged, channel, "the record must name the watcher channel")
				assert.Contains(t, logged, nodeID, "the record must name this node")
				assert.Equal(t, int64(1), reloadFailures,
					"a failing reload must increment wrkflw_authz_policy_reload_failures_total")
			},
		},
		{
			name:   "successful reload is silent",
			reload: func() error { return nil },
			assert: func(t *testing.T, logged string, reloadFailures int64) {
				assert.Empty(t, logged, "a successful reload must not log")
				assert.Equal(t, int64(0), reloadFailures,
					"a successful reload must not increment the failure counter")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelError}))

			reader := sdkmetric.NewManualReader()
			meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

			cb := authzcasbin.NewPolicyReloadCallback(t.Context(), authzcasbin.DBConfig{
				WatcherChannel: channel,
				NodeID:         nodeID,
				Logger:         logger,
				MeterProvider:  meterProvider,
			}, tc.reload)
			require.NotNil(t, cb, "the callback constructor must not return nil")

			cb(origin)

			tc.assert(t, logBuf.String(), collectCounter(t, reader,
				"wrkflw_authz_policy_reload_failures_total"))
		})
	}
}

// syncBuffer is a mutex-guarded bytes.Buffer. The watcher's reload callback runs
// on the listener goroutine while the test polls the buffer, so an unguarded
// buffer would be a data race under -race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestNewDBEnforcer_WatcherReloadFailureIsObservable covers the SEAM that
// TestPolicyReloadCallback cannot: that NewDBEnforcer actually installs the
// logging/counting callback on the watcher.
//
// TestPolicyReloadCallback calls the constructor directly, so it stays green
// even if the wiring in NewDBEnforcer is reverted to the old
// `func(string) { _ = enforcer.LoadPolicy() }`. This test drives the real path —
// a peer NOTIFY on the real LISTEN channel — with the casbin_rule table dropped
// so the reload genuinely fails.
//
// What makes it fail with the old wiring: the discarded error produced no log
// record at all, so the Eventually below would time out. Verified by mutation.
//
// Requires Docker (Postgres via the shared dbtest helper).
func TestNewDBEnforcer_WatcherReloadFailureIsObservable(t *testing.T) {
	const (
		channel = "wrkflw_casbin_reload_seam_test"
		nodeID  = "node-under-test"
	)

	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, authzcasbin.MigrateCasbin(t.Context(), pool))

	logBuf := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelError}))

	ready := make(chan struct{}, 1)
	_, closer, err := authzcasbin.NewDBEnforcer(t.Context(), pool, authzcasbin.DBConfig{
		ModelText:      rbacModel,
		WatcherEnabled: true,
		WatcherChannel: channel,
		NodeID:         nodeID,
		ListenReady:    ready,
		Logger:         logger,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = closer.Close() })

	// Synchronise on the real LISTEN before notifying, closing the
	// NOTIFY-before-LISTEN race.
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("watcher did not establish LISTEN within 10s")
	}

	// Break the reload: with casbin_rule gone, LoadPolicy fails inside the
	// callback. This is the cross-node failure the audit item is about.
	_, err = pool.Exec(t.Context(), `DROP TABLE casbin_rule`)
	require.NoError(t, err, "drop casbin_rule")

	// A peer node reports a policy change (payload != our node id, so the
	// self-echo filter lets it through).
	_, err = pool.Exec(t.Context(), `SELECT pg_notify($1, $2)`, channel, "peer-node")
	require.NoError(t, err, "notify")

	// Read the buffer INSIDE the condition. Passing logBuf.String() as a msgAndArgs
	// value evaluated it at the call — before the wait started, when the buffer was
	// still empty — so on the timeout path the diagnostic could only ever report
	// got: "" (#92), losing the contents at exactly the moment they are worth
	// seeing. EventuallyWithT re-reads every tick and, on timeout, reports the last
	// completed tick's failure: the state the verdict was actually based on, and no
	// read after the deadline that could show a record which arrived too late.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Contains(c, logBuf.String(), "cross-node policy reload failed",
			"a failed cross-node reload must produce an ERROR log record")
	}, 15*time.Second, 25*time.Millisecond)

	// Safe against #86's shape, recorded so the next sweep does not re-flag it:
	// the waited-for message and this node_id are two attributes of ONE
	// slog.Record (db.go's single ErrorContext call). slog.TextHandler formats a
	// record into one buffer and issues one Write, and syncBuffer.Write holds the
	// mutex for it, so the message substring cannot become visible without
	// node_id. Two records would be a genuine instance; one record is not.
	assert.Contains(t, logBuf.String(), nodeID, "the record must name this node")
}

// collectCounter reads the current summed value of the named int64 counter from
// reader, or 0 when the instrument has recorded nothing. It exists so a case can
// distinguish "counter absent / never incremented" from "counter incremented".
func collectCounter(t *testing.T, reader *sdkmetric.ManualReader, name string) int64 {
	t.Helper()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm), "collect metrics")

	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name && !strings.HasSuffix(m.Name, name) {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "metric %q must be a Sum[int64]", m.Name)
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
		}
	}
	return total
}

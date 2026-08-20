package casbin

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	casbinv2 "github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
	"go.opentelemetry.io/otel/metric"

	"github.com/kartaladev/wrkflw/internal/observability"
)

// DBConfig configures [NewDBEnforcer].
type DBConfig struct {
	// ModelText is the casbin model definition. Required; the façade passes
	// DefaultModel by default.
	ModelText string

	// WatcherEnabled controls whether a LISTEN/NOTIFY watcher is created to
	// propagate policy changes across nodes. Set to false for single-node
	// deployments.
	WatcherEnabled bool

	// WatcherChannel is the Postgres NOTIFY channel name used by the watcher.
	// Only relevant when WatcherEnabled is true.
	WatcherChannel string

	// NodeID identifies this process in multi-node deployments. The watcher
	// uses it to suppress self-notifications (a node ignores its own echo).
	// Only relevant when WatcherEnabled is true.
	NodeID string

	// ListenReady, when non-nil, is signalled once after the watcher's LISTEN is
	// established. Test-only — nil in production. Lets a test synchronise on the
	// actual listen state instead of guessing with a sleep.
	ListenReady chan struct{}

	// Logger receives the structured log records this package emits, most
	// importantly the ERROR record for a failed cross-node policy reload. Nil
	// (the default) falls back to slog.Default().
	Logger *slog.Logger

	// MeterProvider backs the wrkflw_authz_policy_reload_failures_total counter.
	// Nil (the default) uses the OTel global provider.
	MeterProvider metric.MeterProvider
}

// policyReloadInstrumentationScope is the OTel instrumentation scope for the
// metrics and logs emitted by the cross-node policy-reload path.
const policyReloadInstrumentationScope = "github.com/kartaladev/wrkflw/authz/casbin"

// newPolicyReloadCallback builds the [persist.Watcher] update callback that
// reloads policy when a peer node reports a change.
//
// A reload failure is LOGGED at ERROR and COUNTED on
// wrkflw_authz_policy_reload_failures_total. It is deliberately NOT propagated:
// casbin's watcher-callback signature is func(string) with no error return, and
// there is nowhere to return it to.
//
// ⚠ Enforcement does NOT fail closed on a failed reload. The enforcer keeps
// answering from the last policy it loaded successfully, so a permission revoked
// on another node can still return Enforce=true, err=nil on this one until a
// later reload succeeds. That is the pre-existing behaviour, preserved on
// purpose: switching to fail-closed trades a security exposure for an
// availability outage and is a decision for the operator, not a side effect of
// making the failure observable. The counter is the signal to alert on.
//
// ctx is used only for the metric recording; it must not be one that is
// cancelled when construction returns, because the callback runs for the whole
// life of the watcher goroutine.
func newPolicyReloadCallback(ctx context.Context, cfg DBConfig, reload func() error) func(string) {
	tel := observability.New(
		policyReloadInstrumentationScope,
		observability.WithLogger(cfg.Logger),
		observability.WithMeterProvider(cfg.MeterProvider),
	)
	failures := tel.Int64Counter(
		"wrkflw_authz_policy_reload_failures_total",
		"Number of cross-node casbin policy reloads that failed, leaving enforcement on a stale policy",
	)

	return func(originNodeID string) {
		if err := reload(); err != nil {
			failures.Add(ctx, 1)
			tel.Logger.ErrorContext(ctx,
				"workflow-casbin: cross-node policy reload failed; enforcement continues against the last successfully loaded policy",
				slog.String("error", err.Error()),
				slog.String("channel", cfg.WatcherChannel),
				slog.String("node_id", cfg.NodeID),
				slog.String("origin_node_id", originNodeID),
			)
		}
	}
}

// noopCloser is an io.Closer that does nothing. Used when the watcher is disabled.
type noopCloser struct{}

func (noopCloser) Close() error { return nil }

// watcherCloser closes a pgWatcher and reports nil (the watcher itself never
// returns an error from Close).
type watcherCloser struct{ w *pgWatcher }

func (c watcherCloser) Close() error { c.w.Close(); return nil }

// NewDBEnforcer builds a *casbin.SyncedEnforcer whose policy is loaded from and
// persisted to the casbin_rule table in pool via pgAdapter. When
// cfg.WatcherEnabled is true, a LISTEN/NOTIFY pgWatcher is wired to the enforcer
// so that policy changes from other nodes trigger an automatic reload on this one.
//
// The returned io.Closer stops the watcher goroutine; callers must close it at
// shutdown to avoid goroutine leaks. When cfg.WatcherEnabled is false the closer
// is a no-op.
//
// On any error occurring after the watcher has been started, NewDBEnforcer closes
// the watcher before returning so no goroutine leaks on partial construction.
//
// Note: casbin's SetWatcher wires the watcher callback to the BASE
// *Enforcer.LoadPolicy, which is NOT mutex-synchronized and races with concurrent
// Authorize calls on the *SyncedEnforcer. We therefore override the callback
// AFTER SetWatcher with the *SyncedEnforcer's own LoadPolicy (see the inline
// comment at the SetUpdateCallback call). Do not remove that override.
func NewDBEnforcer(ctx context.Context, pool *pgxpool.Pool, cfg DBConfig) (*casbinv2.SyncedEnforcer, io.Closer, error) {
	m, err := model.NewModelFromString(cfg.ModelText)
	if err != nil {
		return nil, nil, fmt.Errorf("workflow-casbin: db enforcer: model: %w", err)
	}

	adapter := newPGAdapter(pool)

	enforcer, err := casbinv2.NewSyncedEnforcer(m, adapter)
	if err != nil {
		return nil, nil, fmt.Errorf("workflow-casbin: db enforcer: create enforcer: %w", err)
	}

	if !cfg.WatcherEnabled {
		return enforcer, noopCloser{}, nil
	}

	w := newPGWatcher(pool, cfg.WatcherChannel, cfg.NodeID, cfg.ListenReady, clockwork.NewRealClock())

	// SetWatcher (on the base Enforcer) internally calls
	// w.SetUpdateCallback(func(string){ _ = e.LoadPolicy() }) where e is the
	// base *Enforcer, not the *SyncedEnforcer. That would race against
	// SyncedEnforcer.Enforce which holds the RW-lock. We override the callback
	// after SetWatcher to call SyncedEnforcer.LoadPolicy() instead, which
	// correctly acquires the lock.
	if err := enforcer.SetWatcher(w); err != nil {
		w.Close()
		return nil, nil, fmt.Errorf("workflow-casbin: db enforcer: set watcher: %w", err)
	}
	//
	// The reload error is no longer discarded: newPolicyReloadCallback logs it at
	// ERROR and counts it (audit item 102). See that function for why the reload
	// failure is not turned into a fail-closed enforcement decision here.
	//
	// context.WithoutCancel keeps ctx's values (trace/baggage) while detaching its
	// cancellation: the callback outlives this constructor, running until the
	// watcher is closed.
	reloadCallback := newPolicyReloadCallback(context.WithoutCancel(ctx), cfg, enforcer.LoadPolicy)
	if err := w.SetUpdateCallback(reloadCallback); err != nil {
		w.Close()
		return nil, nil, fmt.Errorf("workflow-casbin: db enforcer: set watcher callback: %w", err)
	}

	return enforcer, watcherCloser{w: w}, nil
}

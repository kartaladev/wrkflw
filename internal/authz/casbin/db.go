package casbin

import (
	"context"
	"fmt"
	"io"

	casbinv2 "github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/jackc/pgx/v5/pgxpool"
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

	w := newPGWatcher(pool, cfg.WatcherChannel, cfg.NodeID, cfg.ListenReady)

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
	if err := w.SetUpdateCallback(func(string) { _ = enforcer.LoadPolicy() }); err != nil {
		w.Close()
		return nil, nil, fmt.Errorf("workflow-casbin: db enforcer: set watcher callback: %w", err)
	}

	return enforcer, watcherCloser{w: w}, nil
}

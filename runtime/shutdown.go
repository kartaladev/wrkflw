package runtime

import (
	"context"
	"errors"
	"io"
	"sync"
)

// ShutdownFunc releases a component's resources, honouring ctx for a bounded
// drain. It is the unit a [ShutdownGroup] aggregates. A nil ShutdownFunc is a
// no-op when added.
type ShutdownFunc func(ctx context.Context) error

// ShutdownGroup aggregates the teardown of a set of long-running components and
// resource holders into a single, ordered, error-collecting [ShutdownGroup.Shutdown]
// call (ADR-0054).
//
// The library deliberately does NOT own goroutine startup: a consumer starts the
// Run(ctx) workers (relay, call notifier, chainer runner, …) themselves and stops
// them by cancelling that context. ShutdownGroup covers the *other* half — the
// resource holders whose release is NOT ctx-driven (the scheduler's gocron
// goroutine via the scheduler package's Scheduler.Close, the advisory-lock ownership connection, the
// casbin closer, the pgx pool, an http.Server's graceful Shutdown) — so the whole
// release is one well-defined call.
//
// Components are shut down in REVERSE registration order (last-registered first),
// the same discipline as stacked defers: a component registered later typically
// depends on one registered earlier (e.g. the relay depends on the pool), so it
// must be torn down first. Every registered shutdown runs even if an earlier one
// errors; the errors are aggregated with [errors.Join] and returned together, so
// one failing closer never strands the rest.
//
// The zero value is ready to use. A ShutdownGroup is safe for concurrent Add and
// for a single Shutdown; Shutdown is idempotent (a second call is a no-op).
type ShutdownGroup struct {
	mu   sync.Mutex
	fns  []ShutdownFunc
	done bool
}

// Add registers fn to be invoked by [ShutdownGroup.Shutdown]. A nil fn is
// ignored. Components registered later are shut down earlier.
//
// If the group has ALREADY shut down, fn is closed immediately (best-effort, with
// a background context) rather than being silently dropped — a late registration
// would otherwise leak the component's resource. Its error is intentionally not
// surfaced (Shutdown already returned).
func (g *ShutdownGroup) Add(fn ShutdownFunc) {
	if fn == nil {
		return
	}
	g.mu.Lock()
	if g.done {
		g.mu.Unlock()
		_ = fn(context.Background())
		return
	}
	g.fns = append(g.fns, fn)
	g.mu.Unlock()
}

// AddCloser registers an [io.Closer] (whose Close takes no context) to be closed
// by [ShutdownGroup.Shutdown]. A nil closer is ignored. This adapts the many
// resource holders that expose Close() error — the scheduler package's
// Scheduler.Close, the advisory-lock io.Closer, the casbin closer — into the group.
func (g *ShutdownGroup) AddCloser(c io.Closer) {
	if c == nil {
		return
	}
	g.Add(func(context.Context) error { return c.Close() })
}

// Shutdown invokes every registered shutdown in reverse registration order,
// passing ctx to each so a consumer can bound the total drain with a deadline.
// It does NOT stop at the first error: every shutdown runs and the errors are
// combined with [errors.Join]. A nil return means every component released
// cleanly. Shutdown is idempotent — a second call returns nil without re-running
// anything.
func (g *ShutdownGroup) Shutdown(ctx context.Context) error {
	g.mu.Lock()
	if g.done {
		g.mu.Unlock()
		return nil
	}
	g.done = true
	fns := g.fns
	g.fns = nil
	g.mu.Unlock()

	var errs []error
	for i := len(fns) - 1; i >= 0; i-- {
		if err := fns[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

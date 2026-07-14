package runtime

import (
	"context"
	"errors"
	"fmt"
)

// ErrDriverShuttingDown is returned by every externally-initiated ProcessDriver entry
// point once Shutdown has begun draining. In-flight work already admitted still runs to
// completion; only new work is refused.
var ErrDriverShuttingDown = errors.New("workflow-runtime: driver is shutting down")

// ErrDrainTimeout is returned (joined) by Shutdown when the drain deadline expires before
// every in-flight unit of work has finished. In-flight work is NOT force-cancelled; it
// keeps running to completion on its own goroutine.
var ErrDrainTimeout = errors.New("workflow-runtime: shutdown drain timed out")

// admit reserves an in-flight slot for a new externally-initiated unit of work. It returns
// a release func and true when work may proceed; it returns nil, false once Shutdown has
// begun draining, so the caller rejects with ErrDriverShuttingDown. Call release (via
// defer) exactly once when the unit of work returns.
func (driver *ProcessDriver) admit() (release func(), ok bool) {
	driver.gateMu.RLock()
	defer driver.gateMu.RUnlock()
	if driver.draining.Load() {
		return nil, false
	}
	driver.inflight.Add(1)
	return driver.inflight.Done, true
}

// IsShuttingDown reports whether Shutdown has begun draining. It lets a higher layer
// (e.g. service.Engine's human-task handlers) reject before performing side effects.
func (driver *ProcessDriver) IsShuttingDown() bool {
	return driver.draining.Load()
}

// waitInflight blocks until every admitted in-flight unit of work has released its slot,
// or ctx is done. On ctx expiry it returns ErrDrainTimeout wrapping ctx.Err(); the in-flight
// work is NOT cancelled and keeps running to completion.
func (driver *ProcessDriver) waitInflight(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		driver.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: %w", ErrDrainTimeout, ctx.Err())
	}
}

// effectiveShutdownCtx applies the WithShutdownTimeout fallback: a ctx deadline always wins;
// otherwise, if a positive shutdownTimeout is configured, derive now+timeout; otherwise
// return ctx unchanged (unbounded). The returned cancel is always safe to defer.
func (driver *ProcessDriver) effectiveShutdownCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	if driver.shutdownTimeout > 0 {
		return context.WithTimeout(ctx, driver.shutdownTimeout)
	}
	return ctx, func() {}
}

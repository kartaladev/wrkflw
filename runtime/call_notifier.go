package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// CallDeliverFunc delivers a trigger to a parent process instance. The
// definition is resolved by the CallNotifier via the DefinitionRegistry and
// passed to the delivery function so the caller can route to the correct
// Runner.Deliver call. The instanceID is the parent's instance ID.
//
// A typical wiring:
//
//	fn := runtime.CallDeliverFunc(func(ctx context.Context, def *model.ProcessDefinition, id string, trg engine.Trigger) error {
//	    _, err := runner.Deliver(ctx, def, id, trg)
//	    return err
//	})
type CallDeliverFunc func(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) error

// CallNotifier drains terminal call links and resumes the parked parent token
// with SubInstanceCompleted or SubInstanceFailed (ADR-0024). Delivery is
// idempotent: a parent whose token was already resumed (engine.ErrTokenNotFound)
// is treated as successfully notified and the link is marked notified.
type CallNotifier struct {
	cl      CallLinkStore
	deliver CallDeliverFunc
	reg     DefinitionRegistry
	clk     clock.Clock
	batch   int
	poll    time.Duration
}

// CallNotifierOption configures a [CallNotifier].
type CallNotifierOption func(*CallNotifier)

// WithCallNotifierBatchSize sets the maximum number of terminal links claimed
// per DrainOnce call. Default: 100.
func WithCallNotifierBatchSize(n int) CallNotifierOption {
	return func(c *CallNotifier) {
		if n > 0 {
			c.batch = n
		}
	}
}

// WithCallNotifierPollInterval sets the interval between DrainOnce calls in
// Run. Default: 1s.
func WithCallNotifierPollInterval(d time.Duration) CallNotifierOption {
	return func(c *CallNotifier) {
		if d > 0 {
			c.poll = d
		}
	}
}

// NewCallNotifier constructs a CallNotifier that claims terminal call links
// from cl, resolves each parent definition via reg, and delivers the
// SubInstanceCompleted / SubInstanceFailed trigger via deliver.
//
//   - cl: the CallLinkStore to claim pending notifications from.
//   - deliver: wraps Runner.Deliver (the parent def is pre-resolved by NewCallNotifier).
//   - reg: resolves parent definition references (format "defID:version").
//   - clk: time source for trigger timestamps (ADR-0003).
//   - opts: optional configuration overrides.
//
// REQUIRED registration contract: every parent definition MUST be resolvable from
// reg under the exact key "<defID>:<version>" (the format DrainOnce uses to look it
// up). If a parent def cannot be resolved, DrainOnce SKIPS that link (it stays
// claimable for a later drain) — so a registry missing the "id:version" key leaves
// the parked parent unresumed until the registration is fixed.
func NewCallNotifier(cl CallLinkStore, deliver CallDeliverFunc, reg DefinitionRegistry, clk clock.Clock, opts ...CallNotifierOption) *CallNotifier {
	n := &CallNotifier{
		cl:      cl,
		deliver: deliver,
		reg:     reg,
		clk:     clk,
		batch:   100,
		poll:    time.Second,
	}
	for _, o := range opts {
		o(n)
	}
	return n
}

// DrainOnce claims up to one batch of terminal call links and resumes each
// parent instance. Returns the count of links successfully notified.
//
// Idempotency contract:
//   - If deliver returns a non-nil error that is NOT engine.ErrTokenNotFound,
//     the link is skipped (left claimable) so a later drain retries it.
//   - If deliver succeeds OR returns engine.ErrTokenNotFound (parent already
//     resumed), the link is marked notified and counted.
//   - If reg.Lookup fails, the link is skipped (not marked notified) so a
//     later drain retries it after the definition is available.
func (n *CallNotifier) DrainOnce(ctx context.Context) (int, error) {
	pending, err := n.cl.ClaimPending(ctx, n.batch)
	if err != nil {
		return 0, fmt.Errorf("workflow-runtime: call notifier: claim: %w", err)
	}

	notified := 0
	for _, p := range pending {
		// Resolve the parent definition. Failure is a skip: a transient lookup
		// failure must not permanently block delivery; a later drain retries.
		defRef := fmt.Sprintf("%s:%d", p.Link.ParentDefID, p.Link.ParentDefVersion)
		parentDef, lookupErr := n.reg.Lookup(defRef)
		if lookupErr != nil {
			continue
		}

		// Build the appropriate trigger based on the child's terminal outcome.
		var trg engine.Trigger
		if p.Outcome.Completed {
			trg = engine.NewSubInstanceCompleted(n.clk.Now(), p.Link.ParentCommandID, p.Outcome.Output)
		} else {
			trg = engine.NewSubInstanceFailed(n.clk.Now(), p.Link.ParentCommandID, p.Outcome.Err)
		}

		// Deliver the trigger to the parent instance.
		derr := n.deliver(ctx, parentDef, p.Link.ParentInstanceID, trg)
		if derr != nil && !errors.Is(derr, engine.ErrTokenNotFound) {
			// Transient or structural failure — leave the link claimable for retry.
			continue
		}
		// Success OR duplicate (ErrTokenNotFound = parent already resumed): mark notified.
		if merr := n.cl.MarkNotified(ctx, p.Link.ChildInstanceID); merr != nil {
			return notified, fmt.Errorf("workflow-runtime: call notifier: mark notified: %w", merr)
		}
		notified++
	}
	return notified, nil
}

// Run drains the call link store on each poll interval tick until ctx is
// cancelled. It returns ctx.Err() when the context is done.
//
// Run mirrors the structure of the postgres Relay.Run: an immediate drain is
// attempted before the first tick, and DrainOnce errors are logged and do not
// terminate the loop (unlike infrastructure errors).
func (n *CallNotifier) Run(ctx context.Context) error {
	ticker := time.NewTicker(n.poll)
	defer ticker.Stop()

	// Immediate drain before waiting for the first tick.
	if _, err := n.DrainOnce(ctx); err != nil {
		if ctx.Err() != nil { // Canceled or DeadlineExceeded: honor the Run contract.
			return ctx.Err()
		}
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := n.DrainOnce(ctx); err != nil {
				if ctx.Err() != nil { // Canceled or DeadlineExceeded: honor the Run contract.
					return ctx.Err()
				}
				return err
			}
		}
	}
}

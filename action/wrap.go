package action

import (
	"context"
	"fmt"
	"time"
)

// ── Wrapper types ────────────────────────────────────────────────────────────
//
// Each wrapper carries the bare action in a NAMED field (not embedded) so that no
// capability method is promoted across layers: a wrapper exposes only its own
// capability, plus Unwrap. [ResolvePolicy] walks the Unwrap chain to aggregate the
// capabilities of every layer.

// timedAction declares (and, standalone, self-enforces) an execution timeout.
type timedAction struct {
	bare Action
	d    time.Duration
}

func (a timedAction) ExecTimeout() time.Duration { return a.d }
func (a timedAction) Unwrap() Action             { return a.bare }

// Do runs the bare action under a context bounded by d. The runtime does NOT call
// this method (it runs the bare action under the effective merged policy at a single
// site); it exists so a timed action is self-contained for standalone use.
func (a timedAction) Do(ctx context.Context, in map[string]any) (map[string]any, error) {
	if a.d <= 0 {
		return a.bare.Do(ctx, in)
	}
	cctx, cancel := context.WithTimeout(ctx, a.d)
	defer cancel()
	return a.bare.Do(cctx, in)
}

// retriableAction declares a retry policy. Its Do is DECLARATIVE: it delegates to
// the bare action exactly once and never retries — retry is the runtime's durable,
// engine-driven mechanism, not an in-process loop.
type retriableAction struct {
	bare Action
	p    RetrySpecs
}

func (a retriableAction) RetrySpecs() RetrySpecs { return a.p }
func (a retriableAction) Unwrap() Action         { return a.bare }

// Do delegates to the bare action once. It does NOT retry (see the type doc).
func (a retriableAction) Do(ctx context.Context, in map[string]any) (map[string]any, error) {
	return a.bare.Do(ctx, in)
}

// recoverableAction declares whether panics are recovered. Standalone, its Do
// converts a panic to an error unless recovery is disabled.
type recoverableAction struct {
	bare Action
	on   bool
}

func (a recoverableAction) RecoverPanics() bool { return a.on }
func (a recoverableAction) Unwrap() Action      { return a.bare }

// Do runs the bare action, recovering a panic into an error when on is true. When
// on is false the panic propagates unchanged.
func (a recoverableAction) Do(ctx context.Context, in map[string]any) (out map[string]any, err error) {
	if !a.on {
		return a.bare.Do(ctx, in)
	}
	defer func() {
		if rec := recover(); rec != nil {
			out = nil
			err = fmt.Errorf("workflow-action: action panicked: %v", rec)
		}
	}()
	return a.bare.Do(ctx, in)
}

// ── Unwrap / ResolvePolicy / Wrap ────────────────────────────────────────────

// unwrapper is implemented by the resiliency wrapper layers.
type unwrapper interface{ Unwrap() Action }

// Unwrap returns the innermost bare action, stripping every resiliency layer added
// by [Wrap]. An action that carries no layers is returned unchanged.
func Unwrap(a Action) Action {
	for {
		u, ok := a.(unwrapper)
		if !ok {
			return a
		}
		a = u.Unwrap()
	}
}

// ResolvePolicy walks a's Unwrap chain (starting at a itself) and aggregates the
// resiliency capabilities each layer declares — via a wrapper added by [Wrap] or by
// a consumer type that implements a capability interface directly. For each concern
// the first occurrence (outermost) wins. A concern absent from the whole chain is
// left nil, signalling the runtime to use its own default.
func ResolvePolicy(a Action) Policy {
	var p Policy
	for {
		if p.Timeout == nil {
			if t, ok := a.(TimedAction); ok {
				d := t.ExecTimeout()
				p.Timeout = &d
			}
		}
		if p.Retry == nil {
			if r, ok := a.(RetriableAction); ok {
				rp := r.RetrySpecs()
				p.Retry = &rp
			}
		}
		if p.Recover == nil {
			if r, ok := a.(RecoverableAction); ok {
				on := r.RecoverPanics()
				p.Recover = &on
			}
		}
		u, ok := a.(unwrapper)
		if !ok {
			return p
		}
		a = u.Unwrap()
	}
}

// Wrap returns an action that carries the resiliency capabilities selected by opts.
// It first unwraps a to its bare action, aggregating the capabilities any existing
// [Wrap] layers already declare; each option then overrides its own concern (so
// re-wrapping a concern REPLACES it, never double-stacks); finally it rebuilds the
// layers in canonical order — recover, then retry, then timeout (innermost to
// outermost) — for each concern that ends up set. Distinct concerns nest; the same
// concern is applied at most once. Wrap(a) with no opts returns a unchanged.
func Wrap(a Action, opts ...Option) Action {
	resolved := ResolvePolicy(a)
	bare := Unwrap(a)

	p := policy{timeout: resolved.Timeout, retry: resolved.Retry, recover: resolved.Recover}
	for _, o := range opts {
		o(&p)
	}

	result := bare
	if p.recover != nil {
		result = recoverableAction{bare: result, on: *p.recover}
	}
	if p.retry != nil {
		result = retriableAction{bare: result, p: *p.retry}
	}
	if p.timeout != nil {
		result = timedAction{bare: result, d: *p.timeout}
	}
	return result
}

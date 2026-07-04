package processtest

import (
	"context"
	"sync"

	"github.com/zakyalvan/krtlwrkflw/authz"
)

// AuthzCall is one recorded call to a [SpyAuthorizer].
type AuthzCall struct {
	Spec  authz.AuthzSpec
	Actor authz.Actor
	Vars  map[string]any
	// Err is the decision returned for this call (nil = allowed).
	Err error
}

// DecideFunc is a programmable authorization decision. Returning nil allows the
// actor; returning a non-nil error denies (use [authz.ErrNotAuthorized] or wrap
// it so callers can use errors.Is).
type DecideFunc func(ctx context.Context, spec authz.AuthzSpec, actor authz.Actor, vars map[string]any) error

// SpyAuthorizer is a programmable [authz.Authorizer] that records every call. By
// default it allows all actors; program it with [SpyAuthorizer.Deny],
// [SpyAuthorizer.Allow], or [SpyAuthorizer.SetDecision]. Safe for concurrent use.
type SpyAuthorizer struct {
	mu     sync.Mutex
	decide DecideFunc
	calls  []AuthzCall
}

// Compile-time assertion.
var _ authz.Authorizer = (*SpyAuthorizer)(nil)

// NewSpyAuthorizer returns a SpyAuthorizer that allows every actor until
// programmed otherwise.
func NewSpyAuthorizer() *SpyAuthorizer {
	return &SpyAuthorizer{}
}

// Authorize applies the current decision, records the call, and returns the
// decision.
func (s *SpyAuthorizer) Authorize(ctx context.Context, spec authz.AuthzSpec, actor authz.Actor, vars map[string]any) error {
	s.mu.Lock()
	decide := s.decide
	s.mu.Unlock()

	var err error
	if decide != nil {
		err = decide(ctx, spec, actor, vars)
	}

	s.mu.Lock()
	s.calls = append(s.calls, AuthzCall{Spec: spec, Actor: actor, Vars: vars, Err: err})
	s.mu.Unlock()
	return err
}

// SetDecision installs a custom decision function. A nil fn resets to allow-all.
func (s *SpyAuthorizer) SetDecision(fn DecideFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decide = fn
}

// Deny programs the authorizer to reject every actor with err. A nil err falls
// back to [authz.ErrNotAuthorized].
func (s *SpyAuthorizer) Deny(err error) {
	if err == nil {
		err = authz.ErrNotAuthorized
	}
	s.SetDecision(func(context.Context, authz.AuthzSpec, authz.Actor, map[string]any) error {
		return err
	})
}

// Allow programs the authorizer to permit every actor (the default).
func (s *SpyAuthorizer) Allow() {
	s.SetDecision(nil)
}

// Calls returns a copy of all recorded authorization calls in order.
func (s *SpyAuthorizer) Calls() []AuthzCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AuthzCall(nil), s.calls...)
}

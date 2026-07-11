package action

import "time"

// RetryPolicy is a pure declaration of how a failed action should be retried. It
// mirrors the four core fields of the engine's retry policy; the runtime converts
// it to the engine's own policy type (which owns the retry algorithm) at execution
// time. It is kept as a leaf value here so the action package never imports the
// definition model (which would create an import cycle: model already imports action).
//
// A RetryPolicy is a declaration only — attaching one to an action (via
// [WithRetryPolicy] or by implementing [RetriableAction]) makes the runtime feed it
// into its durable retry mechanism; the [Wrap] wrapper does NOT retry in-process.
//
// PRECEDENCE / FULL REPLACE: an action-level RetryPolicy takes precedence over a
// node-level policy (action > node > runtime-default) and REPLACES it wholesale —
// it does not merge. Because this struct mirrors only the four core fields, a node
// policy's MaxElapsed budget and NonRetryableErrors substrings are NOT carried over
// when an action policy is present; encode any such limits in the action policy's
// MaxAttempts, or mark individual errors non-retryable at runtime via
// [NonRetryable]. (A catalog/registry default RetryPolicy sits in this same action
// tier — see [NewCatalog]/[NewRegistry] — and therefore also overrides a node
// policy for actions that declare none of their own.)
type RetryPolicy struct {
	// MaxAttempts is the total number of execution attempts including the first.
	// 0 means unlimited.
	MaxAttempts int
	// InitialInterval is the delay before the first retry.
	InitialInterval time.Duration
	// Multiplier is the per-attempt exponential backoff multiplier applied to
	// InitialInterval.
	Multiplier float64
	// MaxInterval caps the per-attempt delay. Zero disables the cap.
	MaxInterval time.Duration
}

// TimedAction is an [Action] that declares its own execution timeout. A consumer
// type may implement it directly to natively carry a per-invocation deadline; the
// runtime honours it in place of its global default.
type TimedAction interface {
	Action
	// ExecTimeout returns the maximum duration a single invocation may run. A
	// non-positive value disables the deadline for this action.
	ExecTimeout() time.Duration
}

// RetriableAction is an [Action] that declares its own retry policy. A consumer
// type may implement it directly; the runtime feeds the policy into its durable
// retry mechanism, overriding any node- or runtime-level default.
type RetriableAction interface {
	Action
	// RetryPolicy returns the per-action retry declaration.
	RetryPolicy() RetryPolicy
}

// RecoverableAction is an [Action] that declares whether panics raised by its Do
// should be recovered (converted to an error) or allowed to propagate. A consumer
// type may implement it directly to opt out of the runtime's recover-by-default.
type RecoverableAction interface {
	Action
	// RecoverPanics reports whether a panic in Do should be recovered (true, the
	// default) or allowed to propagate (false).
	RecoverPanics() bool
}

// Policy is the aggregated, runtime-facing view of an action's declared
// resiliency capabilities. A nil field means the capability is unset and the
// runtime should fall back to its own default. It is produced by [ResolvePolicy].
type Policy struct {
	// Timeout is the per-action execution timeout, or nil when unset.
	Timeout *time.Duration
	// Retry is the per-action retry policy, or nil when unset.
	Retry *RetryPolicy
	// Recover is the per-action panic-recovery flag, or nil when unset.
	Recover *bool
}

func (p Policy) empty() bool {
	return p.Timeout == nil && p.Retry == nil && p.Recover == nil
}

// policy is the mutable aggregation an [Option] configures. It is unexported; a
// consumer only ever sees the WithX option constructors.
type policy struct {
	timeout *time.Duration
	retry   *RetryPolicy
	recover *bool
}

// Option configures a resiliency [policy] applied by [Wrap] (and by the
// Catalog/Registry default). Use the WithX constructors.
type Option func(*policy)

// WithExecTimeout sets the per-action execution timeout applied by the runtime,
// overriding its global default for this action.
func WithExecTimeout(d time.Duration) Option {
	return func(p *policy) { p.timeout = &d }
}

// WithRetryPolicy sets the per-action retry policy fed into the runtime's durable
// retry mechanism, overriding any node- or runtime-level default (action > node >
// runtime-default).
func WithRetryPolicy(rp RetryPolicy) Option {
	return func(p *policy) { p.retry = &rp }
}

// WithRecover sets whether a panic raised by the action's Do is recovered
// (on == true, the default) or allowed to propagate (on == false).
func WithRecover(on bool) Option {
	return func(p *policy) { p.recover = &on }
}

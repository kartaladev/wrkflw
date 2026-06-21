package runtime

import "context"

// Ownership decides whether THIS process is the single writer for an instance,
// and therefore whether its mutable state may be cached and served from memory
// by a CachingStore (ADR-0020).
//
// Caching mutable instance state is safe only under a single-writer-per-instance
// guarantee: a stale cached read would otherwise drive a routing decision and
// fire side-effects before the version-CAS could reject the write. Ownership is
// that guarantee; the CAS is the backstop.
//
// Implementations MUST be sticky: Acquire is idempotent and O(1) for an
// already-owned instance (it must not cost a round-trip on the hot path), and
// ownership changes only on explicit Release (or process death).
type Ownership interface {
	// Acquire reports whether this process owns instanceID, taking ownership if
	// it is free. owned=false means another process owns it: do not cache.
	Acquire(ctx context.Context, instanceID string) (owned bool, err error)
	// Release relinquishes ownership of instanceID (triggers cache eviction).
	Release(ctx context.Context, instanceID string) error
}

// AlwaysOwn is the in-process Ownership for single-replica or sticky-routed
// deployments where this process is guaranteed to be the sole writer of every
// instance it touches. Acquire always returns true; Release is a no-op. It is
// correct and free for single-process embedding; multi-process deployments need
// a real lease (e.g. persistence.NewAdvisoryLockOwnership).
type AlwaysOwn struct{}

// Compile-time assertion.
var _ Ownership = AlwaysOwn{}

// Acquire always grants ownership.
func (AlwaysOwn) Acquire(context.Context, string) (bool, error) { return true, nil }

// Release is a no-op.
func (AlwaysOwn) Release(context.Context, string) error { return nil }

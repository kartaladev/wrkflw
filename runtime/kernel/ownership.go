package kernel

import "context"

// InstanceOwnership decides whether THIS process is the single writer for an instance,
// and therefore whether its mutable state may be cached and served from memory
// by a CachingInstanceStore (ADR-0020).
//
// Caching mutable instance state is safe only under a single-writer-per-instance
// guarantee: a stale cached read would otherwise drive a routing decision and
// fire side-effects before the version-CAS could reject the write. InstanceOwnership is
// that guarantee; the CAS is the backstop.
//
// Implementations MUST be sticky: Acquire is idempotent and O(1) for an
// already-owned instance (it must not cost a round-trip on the hot path), and
// ownership changes only on explicit Release (or process death).
type InstanceOwnership interface {
	// Acquire reports whether this process owns instanceID, taking ownership if
	// it is free. owned=false means another process owns it: do not cache.
	Acquire(ctx context.Context, instanceID string) (owned bool, err error)
	// Release relinquishes ownership of instanceID. A CachingInstanceStore must evict
	// the instance's cached state when ownership is relinquished — relinquish
	// through CachingInstanceStore.Release so the cache stays coherent (a re-acquired
	// instance must not serve a stale cached entry, ADR-0020).
	Release(ctx context.Context, instanceID string) error
}

// AlwaysOwn is the in-process Ownership for single-replica or sticky-routed
// deployments where this process is guaranteed to be the sole writer of every
// instance it touches. Acquire always returns true; Release is a no-op. It is
// correct and free for single-process embedding.
//
// SINGLE-REPLICA / SINGLE-WRITER ONLY. AlwaysOwn unconditionally grants
// ownership, so pairing it with a persistence.CachingInstanceStore across more than one
// replica is a stale-read footgun: every replica would cache the same instance
// and serve its own out-of-date snapshot, firing a routing decision and
// side-effects before the version-CAS could reject the write (ADR-0020, ADR-0054).
// For ANY multi-replica deployment use a real lease —
// persistence.NewAdvisoryLockOwnership — so only the owning replica caches.
// persistence.NewCachingInstanceStore logs a one-time Warn when it is constructed
// with AlwaysOwn to make a misconfiguration visible.
type AlwaysOwn struct{}

// Compile-time assertion.
var _ InstanceOwnership = AlwaysOwn{}

// Acquire always grants ownership.
func (AlwaysOwn) Acquire(context.Context, string) (bool, error) { return true, nil }

// Release is a no-op.
func (AlwaysOwn) Release(context.Context, string) error { return nil }

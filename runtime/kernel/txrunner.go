package kernel

import "context"

// TxRunner is an optional capability an InstanceStore may implement (like
// Notifier/Locker in the dialect package): RunInTx runs fn inside one store
// transaction — every JoinOrBegin-aware write invoked with the ctx handed to
// fn joins it. fn's error rolls the whole unit back.
//
// Callers that need atomicity across multiple InstanceStore writes (Task 11)
// type-assert the store against this interface rather than depending on it
// directly, exactly as InstanceOwnership-adjacent capabilities are probed
// elsewhere in this package.
//
// Mem stores (see MemInstanceStore.RunInTx) provide sequencing only: fn runs
// once, its error propagates, but there is no undo — any write fn already
// performed stays applied even when fn later returns an error. Rollback-parity
// guarantees (ADR-0134) are SQL-only; do not rely on Mem for rollback
// semantics in tests that assert on it.
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(txCtx context.Context) error) error
}

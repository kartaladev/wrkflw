package kernel

import "errors"

// ErrDriverShuttingDown is returned by a ProcessDriver's externally-initiated entry
// points once graceful shutdown has begun draining: new work is refused while
// already-admitted work runs to completion. It lives here in kernel — alongside the
// other cross-cutting sentinels (ErrInstanceNotFound, ErrChainLinkExists, …) — so
// downstream consumers such as eventing/chain can classify the benign shutdown case
// (e.g. downgrade a nack log) without importing the root runtime package. The runtime
// package re-exports it as runtime.ErrDriverShuttingDown to preserve its public API.
var ErrDriverShuttingDown = errors.New("workflow-runtime: driver is shutting down")

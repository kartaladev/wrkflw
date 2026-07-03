package persistence

import (
	"context"
	"log/slog"
)

// Exported warning messages so consumers (and tests) can match on them.
const (
	WarnMsgCallLinkLease = "multi-replica deployment has call-links enabled without a call-link lease/ownership wired; child notifications may be delivered more than once"
	WarnMsgHistoryCap    = "WithHistoryCap is not set; instance snapshot history can grow unbounded"
	WarnMsgPruning       = "no pruning/retention job configured; outbox, call-link, chain-link, dedup, and timer tables can grow unbounded"
)

// DeploymentProfile is the consumer's own assertion of how they run wrkflw. It
// is NOT introspected from the live system — the library cannot know deployment
// topology, so the consumer declares it. See docs/production-checklist.md.
type DeploymentProfile struct {
	MultiReplica       bool // more than one engine replica runs concurrently
	CallLinksEnabled   bool // call-activity / sub-process wiring is in use
	CallLinkLeaseWired bool // a call-link lease/ownership is configured
	HistoryCapSet      bool // WithHistoryCap has been applied to the store
	PruningScheduled   bool // a retention/pruning job is running (see docs/retention.md)
}

// WarnUnsafeConfig emits one slog.Warn per known-risky combination in p. It is a
// no-op for a safe profile, never returns an error, and never panics on a nil
// logger (it falls back to slog.Default()). Call it once at consumer startup to
// get a production-readiness reminder. It does not inspect the live system.
func WarnUnsafeConfig(logger *slog.Logger, p DeploymentProfile) {
	if logger == nil {
		logger = slog.Default()
	}
	ctx := context.Background()
	if p.MultiReplica && p.CallLinksEnabled && !p.CallLinkLeaseWired {
		logger.WarnContext(ctx, WarnMsgCallLinkLease)
	}
	if !p.HistoryCapSet {
		logger.WarnContext(ctx, WarnMsgHistoryCap)
	}
	if !p.PruningScheduled {
		logger.WarnContext(ctx, WarnMsgPruning)
	}
}

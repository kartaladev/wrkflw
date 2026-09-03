package persistence

import (
	"context"
	"database/sql"
	"log/slog"
)

// Exported warning messages so consumers (and tests) can match on them.
const (
	WarnMsgCallLinkLease     = "multi-replica deployment has call-links enabled without a call-link lease/ownership wired; child notifications may be delivered more than once"
	WarnMsgHistoryCap        = "WithHistoryCap is not set; instance snapshot history can grow unbounded"
	WarnMsgPruning           = "no pruning/retention job configured; outbox, call-link, chain-link, dedup, and timer tables can grow unbounded"
	WarnMsgSQLiteBusyTimeout = "SQLite pool allows more than one connection while PRAGMA busy_timeout is 0; concurrent writes will fail immediately with SQLITE_BUSY. Set db.SetMaxOpenConns(1) or add _pragma=busy_timeout(5000) to the DSN"
)

// DeploymentProfile is the consumer's own assertion of how they run wrkflw. It
// is NOT introspected from the live system — the library cannot know deployment
// topology, so the consumer declares it.
type DeploymentProfile struct {
	MultiReplica       bool // more than one engine replica runs concurrently
	CallLinksEnabled   bool // call-activity / sub-process wiring is in use
	CallLinkLeaseWired bool // a call-link lease/ownership is configured
	HistoryCapSet      bool // WithHistoryCap has been applied to the store
	PruningScheduled   bool // a retention/pruning job is running
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

// WarnUnsafeSQLite inspects a live SQLite handle and emits [WarnMsgSQLiteBusyTimeout]
// when — and only when — the pool admits more than one connection AND the
// connection's PRAGMA busy_timeout is 0.
//
// Unlike [WarnUnsafeConfig], which reports a profile the consumer asserts, this
// probes the handle itself: pool width comes from db.Stats() and the timeout is
// read back with PRAGMA busy_timeout. That is why it is a separate function
// rather than a [DeploymentProfile] field — DeploymentProfile is by contract not
// introspected from the live system.
//
// The combination is what matters, and the pairing was measured rather than
// assumed: a wide pool WITH the timeout set produced zero failures across four
// runs, while the same pool WITHOUT it failed 174–195 of 200 concurrent
// operations within 4–17 ms. Warning on pool width alone would flag a
// configuration measured to be safe, so it does not.
//
// A pool pinned to a single connection is not flagged: [OpenSQLite]'s contract is
// single-writer serialisation, under which in-process write contention cannot
// arise. (A second OS process opening the same file can still meet SQLITE_BUSY;
// setting the timeout is advisable regardless.)
//
// It warns only — it never returns an error and never rejects a handle, so it is
// safe to call on an already-deployed configuration. A nil logger falls back to
// [slog.Default]; a nil db is a no-op. It is called automatically by [OpenSQLite].
func WarnUnsafeSQLite(ctx context.Context, logger *slog.Logger, db *sql.DB) {
	if db == nil {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}

	maxOpen := db.Stats().MaxOpenConnections
	// database/sql reports 0 for "unlimited", which is the widest pool of all.
	singleWriter := maxOpen == 1
	if singleWriter {
		return
	}

	var busyTimeoutMS int
	if err := db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeoutMS); err != nil {
		// The probe is advisory: an unreadable pragma must never fail an open.
		logger.WarnContext(ctx, "workflow-persistence: could not read PRAGMA busy_timeout to check the SQLite single-writer contract",
			slog.String("err", err.Error()))
		return
	}
	if busyTimeoutMS > 0 {
		return
	}

	logger.WarnContext(ctx, WarnMsgSQLiteBusyTimeout,
		slog.Int("max_open_conns", maxOpen),
		slog.Int("busy_timeout_ms", busyTimeoutMS),
	)
}

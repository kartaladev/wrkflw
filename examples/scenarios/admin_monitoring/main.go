// Package main is the admin/ops monitoring reference walkthrough.
//
// It demonstrates three operator-facing surfaces over an in-process SQLite
// store — no Docker or network database required:
//
//  1. [kernel.InstanceLister] — page through all process instances and print
//     their id/status.
//  2. Incident raise → resolve — drive an instance whose service action fails
//     non-retryably so it parks on an incident (StatusRunning + Incidents),
//     then call [runtime.ProcessDriver.ResolveIncident] to clear it and resume the
//     instance to completion.
//  3. Outbox stats + dead-letter + redrive — wire a deliberately-failing
//     [kernel.Publisher] and a low MaxDeliveryAttempts (1) so the relay
//     quarantines terminal-event rows to status='dead' after one publish
//     attempt; then call [persistence.Relay.ListDeadLettered] to inspect
//     them, [persistence.Relay.Redrive] to re-queue them, and verify the
//     dead count returns to zero via [persistence.Relay.OutboxStats].
//
// All three sections are self-contained: the program starts, exercises each
// surface, prints clearly labelled operator observations, and exits 0.
// No HTTP server is started; no external broker is used.
//
// This is reference wiring ONLY — not a shipped binary. The product is the
// importable library; this file illustrates the admin/ops API surface.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"

	_ "modernc.org/sqlite" // register CGo-free "sqlite" driver

	"database/sql"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// statusName returns a human-readable label for engine.Status values.
// engine.Status is a plain int (no String() method), so we map explicitly.
func statusName(s engine.Status) string {
	switch s {
	case engine.StatusRunning:
		return "running"
	case engine.StatusCompleted:
		return "completed"
	case engine.StatusFailed:
		return "failed"
	case engine.StatusTerminated:
		return "terminated"
	default:
		return fmt.Sprintf("status(%d)", int(s))
	}
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "admin_monitoring: %v\n", err)
		os.Exit(1)
	}
}

// run exercises each admin surface in sequence and returns the first error.
func run() error {
	ctx := context.Background()

	// --- SQLite in-process database (WAL + foreign keys) ---
	//
	// :memory: keeps the demo fully self-contained: the schema is migrated on
	// every run and the file disappears with the process.
	db, err := sql.Open("sqlite", "file::memory:?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer func() { _ = db.Close() }()

	// SQLite single-writer contract: exactly one open connection.
	db.SetMaxOpenConns(1)

	if err := persistence.MigrateSQLite(ctx, db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// Shared SQLite store (also implements kernel.JournalReader).
	store, err := persistence.OpenSQLite(ctx, db)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	// ------------------------------------------------------------------ //
	// Section 1 — InstanceLister                                          //
	// ------------------------------------------------------------------ //
	fmt.Println("=== SECTION 1: InstanceLister ===")
	if err := demonstrateLister(ctx, db, store); err != nil {
		return fmt.Errorf("lister: %w", err)
	}

	// ------------------------------------------------------------------ //
	// Section 2 — Incident raise → resolve                                //
	// ------------------------------------------------------------------ //
	fmt.Println("\n=== SECTION 2: Incident Raise → Resolve ===")
	if err := demonstrateIncident(ctx, db, store); err != nil {
		return fmt.Errorf("incident: %w", err)
	}

	// ------------------------------------------------------------------ //
	// Section 3 — Outbox stats + dead-letter + redrive                    //
	// ------------------------------------------------------------------ //
	fmt.Println("\n=== SECTION 3: OutboxStats + Dead-Letter + Redrive ===")
	if err := demonstrateDeadLetter(ctx, db, store); err != nil {
		return fmt.Errorf("dead-letter: %w", err)
	}

	fmt.Println("\n--- admin_monitoring complete; exit 0 ---")
	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// Section 1: InstanceLister
// ──────────────────────────────────────────────────────────────────────────

// demonstrateLister starts three instances (two completed, one parked with an
// incident so it stays running) and pages through them via the SQLite lister.
func demonstrateLister(ctx context.Context, db *sql.DB, store kernel.Store) error {
	// Simple linear definition: start → greet → end.
	def, err := definition.NewBuilder("greet", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("greet", activity.WithActionName("say-hello"))).
		Add(event.NewEnd("end")).
		Connect("start", "greet").
		Connect("greet", "end").
		Build()
	if err != nil {
		return fmt.Errorf("build def: %w", err)
	}

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"say-hello": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"greeted": true}, nil
		}),
	})

	runner, err := runtime.NewProcessDriver(cat, store)
	if err != nil {
		return fmt.Errorf("build runner: %w", err)
	}

	ids := []string{"greet-001", "greet-002", "greet-003"}
	for _, id := range ids {
		st, err := runner.Run(ctx, def, id, nil)
		if err != nil {
			return fmt.Errorf("run %s: %w", id, err)
		}
		fmt.Printf("  started %s → %s\n", id, statusName(st.Status))
	}

	lister, err := persistence.NewSQLiteLister(db)
	if err != nil {
		return fmt.Errorf("new sqlite lister: %w", err)
	}

	// First page: limit 2.
	page1, err := lister.List(ctx, kernel.InstanceFilter{Limit: 2, IncludeTotal: true})
	if err != nil {
		return fmt.Errorf("list page1: %w", err)
	}
	fmt.Printf("\n  Page 1 (limit 2), total=%d, hasMore=%v:\n", page1.TotalCount, page1.HasMore)
	for _, s := range page1.Items {
		fmt.Printf("    id=%-14s  status=%s\n", s.InstanceID, statusName(s.Status))
	}

	if page1.HasMore {
		page2, err := lister.List(ctx, kernel.InstanceFilter{Limit: 2, Cursor: page1.NextCursor})
		if err != nil {
			return fmt.Errorf("list page2: %w", err)
		}
		fmt.Printf("  Page 2 (cursor), hasMore=%v:\n", page2.HasMore)
		for _, s := range page2.Items {
			fmt.Printf("    id=%-14s  status=%s\n", s.InstanceID, statusName(s.Status))
		}
	}

	// Filter by status=completed.
	completed := engine.StatusCompleted
	onlyCompleted, err := lister.List(ctx, kernel.InstanceFilter{Status: &completed, IncludeTotal: true})
	if err != nil {
		return fmt.Errorf("list completed: %w", err)
	}
	fmt.Printf("  Filter status=completed → %d instance(s)\n", onlyCompleted.TotalCount)

	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// Section 2: Incident raise → resolve
// ──────────────────────────────────────────────────────────────────────────

// demonstrateIncident wires a service action that fails on the first invocation
// (MaxAttempts=1 so no retry, incident raised immediately) then calls
// ResolveIncident to resume the instance to completion.
func demonstrateIncident(ctx context.Context, _ *sql.DB, store kernel.Store) error {
	def, err := definition.NewBuilder("incident-demo", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("risky-op", activity.WithActionName("risky"))).
		Add(event.NewEnd("end")).
		Connect("start", "risky-op").
		Connect("risky-op", "end").
		Build()
	if err != nil {
		return fmt.Errorf("build def: %w", err)
	}

	// attempts counts how many times the action is called.
	// Call 1 → fails (raises incident); call 2+ → succeeds.
	var attempts atomic.Int32
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"risky": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			n := attempts.Add(1)
			if n == 1 {
				return nil, errors.New("transient failure on first attempt")
			}
			return map[string]any{"ok": true}, nil
		}),
	})

	// MaxAttempts=1: the first failure exhausts the retry budget immediately and
	// raises an incident (no backoff retry loop).
	runner, err := runtime.NewProcessDriver(cat, store,
		runtime.WithDefaultRetryPolicy(model.RetryPolicy{
			MaxAttempts:     1,
			InitialInterval: 0,
			BackoffCoef:     1,
		}),
	)
	if err != nil {
		return fmt.Errorf("build runner: %w", err)
	}

	instanceID := "incident-inst-001"
	parked, err := runner.Run(ctx, def, instanceID, nil)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}

	if len(parked.Incidents) == 0 {
		return errors.New("expected an incident but none was raised — check retry policy")
	}

	incidentID := parked.Incidents[0].ID
	fmt.Printf("  INCIDENT RAISED on %s  id=%s  error=%q\n",
		instanceID, incidentID, parked.Incidents[0].Error)
	fmt.Printf("  instance status=%s  incident_count=%d\n", statusName(parked.Status), len(parked.Incidents))

	// Operator resolves the incident and grants 1 additional attempt.
	// ResolveIncident delivers a ResolveIncident trigger → the engine clears the
	// incident record, resets the retry budget by addAttempts, and re-invokes the
	// action. The second call to "risky" succeeds → instance reaches end → Completed.
	resolved, err := runner.ResolveIncident(ctx, def, instanceID, incidentID, 1)
	if err != nil {
		return fmt.Errorf("resolve incident: %w", err)
	}

	fmt.Printf("  INCIDENT RESOLVED → status=%s  incidents_remaining=%d\n",
		statusName(resolved.Status), len(resolved.Incidents))

	if resolved.Status != engine.StatusCompleted {
		return fmt.Errorf("expected StatusCompleted after resolve; got %s", statusName(resolved.Status))
	}

	return nil
}

// ──────────────────────────────────────────────────────────────────────────
// Section 3: Outbox stats + dead-letter + redrive
// ──────────────────────────────────────────────────────────────────────────

// failPublisher is a kernel.Publisher that always returns an error.
// It simulates a permanently unavailable broker — every DrainOnce call
// increments retry_count on the row. With MaxDeliveryAttempts=1 the row
// is quarantined to status='dead' after the very first publish attempt.
type failPublisher struct{}

func (failPublisher) Publish(_ context.Context, _ kernel.OutboxEvent) error {
	return errors.New("broker unavailable (deliberate failure)")
}

// demonstrateDeadLetter drives a process to completion so a terminal event
// lands in wrkflw_outbox, then uses a failing publisher + MaxDeliveryAttempts=1
// to dead-letter the row in a single DrainOnce call. It then:
//   - prints OutboxStats (Dead > 0);
//   - lists the dead row via ListDeadLettered;
//   - redrives the row via Redrive;
//   - confirms OutboxStats.Dead == 0 after redrive (row is pending again).
func demonstrateDeadLetter(ctx context.Context, db *sql.DB, store kernel.Store) error {
	// A simple definition that completes immediately → emits a terminal outbox event.
	def, err := definition.NewBuilder("dl-demo", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("work", activity.WithActionName("work"))).
		Add(event.NewEnd("end")).
		Connect("start", "work").
		Connect("work", "end").
		Build()
	if err != nil {
		return fmt.Errorf("build def: %w", err)
	}

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"work": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"done": true}, nil
		}),
	})

	runner, err := runtime.NewProcessDriver(cat, store)
	if err != nil {
		return fmt.Errorf("build runner: %w", err)
	}

	st, err := runner.Run(ctx, def, "dl-inst-001", nil)
	if err != nil {
		return fmt.Errorf("run: %w", err)
	}
	fmt.Printf("  instance %s → %s (outbox row inserted)\n", st.InstanceID, statusName(st.Status))

	// Build a relay with the failing publisher and MaxDeliveryAttempts=1 so a
	// single DrainOnce call quarantines every row it touches.
	relay, err := persistence.NewSQLiteRelay(db, failPublisher{},
		persistence.WithMaxDeliveryAttempts(1),
	)
	if err != nil {
		return fmt.Errorf("new sqlite relay: %w", err)
	}

	// DrainOnce → publish fails → retry_count reaches maxDel(1) → status='dead'.
	published, drainErr := relay.DrainOnce(ctx)
	if drainErr != nil {
		return fmt.Errorf("drain once: %w", drainErr)
	}
	fmt.Printf("  DrainOnce: published=%d (expected 0 — publisher always fails)\n", published)

	// OutboxStats is now part of the persistence.Relay interface — no type
	// assertion needed.
	stats, err := relay.OutboxStats(ctx)
	if err != nil {
		return fmt.Errorf("outbox stats: %w", err)
	}
	fmt.Printf("  OutboxStats: pending=%d  dead=%d  oldest_pending_age=%s\n",
		stats.Pending, stats.Dead, stats.OldestPendingAge)

	if stats.Dead == 0 {
		return errors.New("expected dead > 0 after failing drain; outbox may not have a row")
	}

	// ListDeadLettered: inspect the quarantined row(s).
	deadRows, err := relay.ListDeadLettered(ctx, 10)
	if err != nil {
		return fmt.Errorf("list dead-lettered: %w", err)
	}
	fmt.Printf("  ListDeadLettered: %d dead row(s)\n", len(deadRows))
	for _, dl := range deadRows {
		fmt.Printf("    id=%d  instance=%s  topic=%s  retry_count=%d  error=%q\n",
			dl.ID, dl.InstanceID, dl.Topic, dl.RetryCount, dl.LastError)
	}

	// Collect dead row IDs for Redrive.
	ids := make([]int64, len(deadRows))
	for i, dl := range deadRows {
		ids[i] = dl.ID
	}

	// Redrive: reset dead rows back to pending.
	redriven, err := relay.Redrive(ctx, ids...)
	if err != nil {
		return fmt.Errorf("redrive: %w", err)
	}
	fmt.Printf("  Redrive(%v): %d row(s) re-queued\n", ids, redriven)

	// OutboxStats after redrive: Dead should now be 0.
	statsAfter, err := relay.OutboxStats(ctx)
	if err != nil {
		return fmt.Errorf("outbox stats (after redrive): %w", err)
	}
	fmt.Printf("  OutboxStats (after redrive): pending=%d  dead=%d\n",
		statsAfter.Pending, statsAfter.Dead)

	if statsAfter.Dead != 0 {
		return fmt.Errorf("expected dead=0 after redrive; got %d", statsAfter.Dead)
	}
	fmt.Println("  dead-letter redrive confirmed: dead=0, pending restored")

	return nil
}

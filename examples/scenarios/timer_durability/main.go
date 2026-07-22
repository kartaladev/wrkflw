// Package main demonstrates timer durability across a simulated process restart.
//
// The scenario proves that an intermediate catch timer, once persisted to a
// SQL store, survives a "process restart" and fires on the resumed driver
// without any manual RehydrateTimers call — the scheduler self-rehydrates on
// Start via the ADR-0102 WithJobStore thunk.
//
// Flow:
//
//	start → wait1h[IntermediateCatchEvent timer 1h] → announce[Service] → end
//
// Scenario steps:
//
//  1. Open + migrate a SQLite file at a temp path.
//  2. Generation 1 — build sched1 + driver1 on the fake clock; drive an
//     instance → it parks at the 1h catch timer. Print "armed timer".
//  3. Advance the clock by 30m (before fire). Print "NOT yet due".
//  4. Stop: driver1.Shutdown + sched1.Close — simulate process exit.
//  5. Generation 2 — build sched2 + driver2 over the SAME SQLite stores +
//     SAME fake clock. sched2.Start self-rehydrates the persisted timer row
//     (no explicit RehydrateTimers call). Print "restarted".
//  6. Deterministically fire: BlockUntilContext (wait for gocron waiter) then
//     Advance(1h). The rehydrated timer fires; the announce action runs.
//  7. Poll until instance reaches StatusCompleted. Print "✅ durable timer
//     fired after restart; instance completed". Remove the DB file.
//
// A *clockwork.FakeClock is shared by both driver generations and both
// schedulers (ADR-0003), so a single Advance drives timer firing without
// real clock waiting. The DB is a real on-disk SQLite file, which is why the
// timer row genuinely survives the driver teardown.
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduler"

	_ "modernc.org/sqlite"
)

func main() {
	ctx := context.Background()

	// ── Step 1: open + migrate the SQLite file ────────────────────────────────
	dbPath := filepath.Join(os.TempDir(), "wrkflw_timer_durability.db")
	// Remove any stale file from a previous run.
	_ = os.Remove(dbPath)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatal("sql.Open:", err)
	}
	// SQLite is single-writer; cap the pool to one connection to avoid
	// "database is locked" errors on concurrent writes.
	db.SetMaxOpenConns(1)
	defer func() {
		_ = db.Close()
		_ = os.Remove(dbPath)
		fmt.Println("DB file removed.")
	}()

	if err := persistence.MigrateSQLite(ctx, db); err != nil {
		log.Fatal("MigrateSQLite:", err)
	}
	fmt.Printf("SQLite DB at: %s\n", dbPath)

	// Build the persistent instance + timer stores (shared across both generations).
	instStore, err := persistence.OpenSQLite(ctx, db)
	if err != nil {
		log.Fatal("OpenSQLite:", err)
	}
	timerStore, err := persistence.NewSQLiteTimerStore(db)
	if err != nil {
		log.Fatal("NewSQLiteTimerStore:", err)
	}

	// ── Process definition ────────────────────────────────────────────────────
	// start → wait1h[IntermediateCatchEvent timer AfterDuration(1h)] → announce[Service] → end
	def, err := definition.NewBuilder("timer-durability", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("wait1h",
			event.WithCatchTimer(schedule.AfterDuration(1*time.Hour)),
		)).
		Add(activity.NewServiceTask("announce",
			activity.WithTaskAction("announce"),
		)).
		Add(event.NewEnd("end")).
		Connect("start", "wait1h").
		Connect("wait1h", "announce").
		Connect("announce", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	reg := kernel.NewMapDefinitionRegistry(def)

	// firedCh is signalled by the announce action so the main goroutine can
	// observe the timer fire deterministically (scheduler fires on its own goroutine).
	firedCh := make(chan struct{}, 1)
	cat := action.NewCatalog(map[string]action.Action{
		"announce": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			fmt.Println("⏰ timer fired — durable timer survived the restart!")
			select {
			case firedCh <- struct{}{}:
			default:
			}
			return nil, nil
		}),
	})

	// ── Shared fake clock ─────────────────────────────────────────────────────
	// ONE fake clock drives both driver generations and both schedulers so a
	// single Advance controls all timer decisions without real-clock waiting.
	fc := clockwork.NewFakeClockAt(time.Date(2026, 7, 8, 9, 0, 0, 0, time.UTC))

	const instanceID = "order-1"

	fmt.Println("--- Timer Durability: restart-survival ---")

	// ── Step 2: Generation 1 — arm the timer, park the instance ──────────────
	var driver1 *runtime.ProcessDriver
	sched1, err := scheduler.NewScheduler(
		scheduler.WithClock(fc),
		// Thunk: driver1 is nil at construction time; it is assigned before
		// sched1.Start is called explicitly, so the provider sees the live pointer.
		scheduler.WithJobStore(func() kernel.JobStore { return runtime.NewJobStore(driver1) }),
	)
	if err != nil {
		log.Fatal("sched1:", err)
	}

	driver1, err = runtime.NewProcessDriver(
		runtime.WithInstanceStore(instStore),
		runtime.WithTimerStore(timerStore),
		runtime.WithDefinitions(reg),
		runtime.WithActionCatalog(cat),
		runtime.WithClock(fc),
		runtime.WithScheduler(sched1),
	)
	if err != nil {
		log.Fatal("driver1:", err)
	}

	// sched1 is consumer-injected (WithScheduler), so driver1.Start is a no-op
	// for the scheduler; start the scheduler explicitly.
	if err := sched1.Start(ctx); err != nil {
		log.Fatal("sched1.Start:", err)
	}

	parked, err := driver1.Drive(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("Drive:", err)
	}
	fmt.Printf("armed timer, parked at T0 — node=%q status=%s\n",
		parked.Tokens[0].NodeID, parked.Status.String())

	// ── Step 3: Advance 30m — NOT yet due ────────────────────────────────────
	// Wait for the gocron waiter to be armed before advancing so we don't race it.
	if err := fc.BlockUntilContext(ctx, 1); err != nil {
		log.Fatal("BlockUntilContext gen1:", err)
	}
	fc.Advance(30 * time.Minute)
	fmt.Println("advanced +30m: timer NOT yet due")

	// Confirm instance is still parked (not completed).
	st, _, err := instStore.Load(ctx, instanceID)
	if err != nil {
		log.Fatal("load after +30m:", err)
	}
	if st.Status == engine.StatusCompleted {
		fmt.Fprintln(os.Stderr, "ERROR: instance completed before timer was due")
		os.Exit(1)
	}
	fmt.Printf("  instance still parked (status=%s) — correct\n", st.Status.String())

	// ── Step 4: Stop — simulate process exit ─────────────────────────────────
	if err := driver1.Shutdown(ctx); err != nil {
		log.Printf("driver1.Shutdown (non-fatal): %v", err)
	}
	if err := sched1.Close(); err != nil {
		log.Printf("sched1.Close (non-fatal): %v", err)
	}
	fmt.Println("generation 1 stopped (simulated process exit)")

	// ── Step 5: Generation 2 — fresh driver self-rehydrates timers on Start ──
	var driver2 *runtime.ProcessDriver
	sched2, err := scheduler.NewScheduler(
		scheduler.WithClock(fc),
		// Thunk breaks the construction cycle: driver2 is nil at this point but
		// will be assigned before sched2.Start() calls the provider.
		scheduler.WithJobStore(func() kernel.JobStore { return runtime.NewJobStore(driver2) }),
	)
	if err != nil {
		log.Fatal("sched2:", err)
	}
	defer func() { _ = sched2.Close() }()

	driver2, err = runtime.NewProcessDriver(
		runtime.WithInstanceStore(instStore),
		runtime.WithTimerStore(timerStore),
		runtime.WithDefinitions(reg),
		runtime.WithActionCatalog(cat),
		runtime.WithClock(fc),
		runtime.WithScheduler(sched2),
	)
	if err != nil {
		log.Fatal("driver2:", err)
	}

	// Start triggers self-rehydration — NO explicit RehydrateTimers call.
	// sched2 is consumer-injected (WithScheduler), so driver2.Start is a no-op
	// for the scheduler; we must start the scheduler explicitly ourselves.
	if err := sched2.Start(ctx); err != nil {
		log.Fatal("sched2.Start:", err)
	}
	fmt.Println("restarted: fresh driver self-rehydrated timers from SQLite")

	// ── Step 6: Deterministically fire the rehydrated timer ──────────────────
	// Wait for gocron to arm its internal waiter on the fake clock (the
	// rehydrated one-shot timer), then advance past the original 1h instant.
	if err := fc.BlockUntilContext(ctx, 1); err != nil {
		log.Fatal("BlockUntilContext gen2:", err)
	}
	fc.Advance(1 * time.Hour)

	// Wait for the announce action to signal (scheduler fires asynchronously).
	select {
	case <-firedCh:
	case <-time.After(30 * time.Second):
		fmt.Fprintln(os.Stderr, "ERROR: timeout — announce action did not fire after Advance")
		os.Exit(1)
	}

	// ── Step 7: Confirm instance completed ───────────────────────────────────
	// The terminal commit may still be in-flight after the action ran; poll
	// until the instance store reflects the completed status.
	var final engine.InstanceState
	for range 500 {
		final, _, err = instStore.Load(ctx, instanceID)
		if err == nil && final.Status == engine.StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		log.Fatal("final load:", err)
	}
	if final.Status != engine.StatusCompleted {
		fmt.Fprintf(os.Stderr, "ERROR: instance not completed after timer fired (status=%s)\n",
			final.Status.String())
		os.Exit(1)
	}

	fmt.Println("✅ durable timer fired after restart; instance completed")
}

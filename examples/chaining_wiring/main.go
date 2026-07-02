// Package main demonstrates the process start-chaining feature wired end-to-end
// on an in-process SQLite database — no Docker required.
//
// Usage:
//
//	go run ./examples/chaining_wiring/
//
// The program:
//  1. Opens an in-process SQLite database and migrates the schema.
//  2. Defines a predecessor process (P_A: start→end) and a successor (S_A: start→end).
//  3. Wires: Runner + Chainer + ChainerRunner + GoChannel pub/sub + Relay.
//  4. Starts the ChainerRunner goroutine (subscribing before any relay publish).
//  5. Runs the predecessor instance "demo-pred".
//  6. Drives the relay (DrainOnce) to publish the terminal outbox event.
//  7. Polls until the successor "demo-pred-next-completed" is created in the store.
//  8. Prints the successor's instance id and exits.
//
// This is reference wiring ONLY — NOT a shipped binary. The product is the
// importable library; this file illustrates how to assemble the chaining feature
// on an in-process SQLite backend. See examples/sqlite_wiring/main.go for a
// fuller wiring including HTTP transport, timer scheduling, and graceful shutdown.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "modernc.org/sqlite" // register "sqlite" driver (CGo-free)

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("chaining demo exited with error", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── 1. Open an in-process SQLite database ────────────────────────────────
	//
	// :memory: is simplest for a demo; file-backed works too. MaxOpenConns(1) is
	// REQUIRED for SQLite: a second open connection races on WAL writes.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	// Apply schema migrations idempotently.
	if err := persistence.MigrateSQLite(ctx, db); err != nil {
		return fmt.Errorf("migrate sqlite: %w", err)
	}

	// Open the runtime.Store backed by SQLite.
	store, err := persistence.OpenSQLite(ctx, db)
	if err != nil {
		return fmt.Errorf("open sqlite store: %w", err)
	}

	// ── 2. Define predecessor (P_A) and successor (S_A) processes ─────────────
	defPA, err := model.NewDefinition("proc-a", 1).
		Add(model.NewStartEvent("start")).
		Add(model.NewEndEvent("end")).
		Connect("start", "end").
		Build()
	if err != nil {
		return fmt.Errorf("build proc-a: %w", err)
	}

	defSA, err := model.NewDefinition("proc-a-succ", 1).
		Add(model.NewStartEvent("start")).
		Add(model.NewEndEvent("end")).
		Connect("start", "end").
		Build()
	if err != nil {
		return fmt.Errorf("build proc-a-succ: %w", err)
	}

	// ── 3. Wire GoChannel pub/sub, relay, chain-link store, runner, chainer ───
	pub, sub, closer := eventing.NewGoChannelPublisher(eventing.WithLogger(logger))
	defer func() { _ = closer.Close() }()

	relay := persistence.NewSQLiteRelay(db, pub)
	links := persistence.NewSQLiteChainLinkStore(db)

	runner := runtime.NewRunner(action.NewMapCatalog(nil), store)

	// SuccessorPolicy: proc-a → proc-a-succ.
	policy := func(ctx context.Context, ev runtime.ChainEvent) (runtime.SuccessorDecision, bool) {
		if ev.PredecessorDefinitionRef == "proc-a:1" {
			return runtime.SuccessorDecision{Def: defSA, Vars: ev.Result}, true
		}
		return runtime.SuccessorDecision{}, false
	}

	core := runtime.NewChainer(runner, policy, runtime.WithChainLinks(links))
	cr := eventing.NewChainerRunner(core)

	// ── 4. Start the ChainerRunner goroutine BEFORE any relay publish ─────────
	//
	// GoChannel is non-persistent: a message published before Subscribe is called
	// is dropped. Subscribe FIRST, then publish (DrainOnce).
	done := make(chan error, 1)
	go func() { done <- cr.Run(ctx, sub) }()

	// ── 5. Run the predecessor instance to completion ─────────────────────────
	predID := "demo-pred"
	startVars := map[string]any{"source": "chaining-wiring-demo"}

	st, err := runner.Run(ctx, defPA, predID, startVars)
	if err != nil {
		return fmt.Errorf("run predecessor: %w", err)
	}
	logger.Info("predecessor completed", "instance_id", st.InstanceID, "status", st.Status)

	// ── 6. Drain the relay: publishes instance.completed from the outbox ──────
	drained, err := relay.DrainOnce(ctx)
	if err != nil {
		return fmt.Errorf("relay drain: %w", err)
	}
	logger.Info("outbox drained", "rows", drained)

	// ── 7. Poll until the successor is created in the store ───────────────────
	succID := predID + "-next-completed"
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, _, loadErr := store.Load(ctx, succID)
		if loadErr == nil {
			break
		}
		if !errors.Is(loadErr, runtime.ErrInstanceNotFound) {
			return fmt.Errorf("load successor: %w", loadErr)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout: successor %q not started within 10s", succID)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Verify the chain link was recorded.
	link, ok, err := links.LookupBySuccessor(ctx, succID)
	if err != nil {
		return fmt.Errorf("lookup chain link: %w", err)
	}
	if !ok {
		return fmt.Errorf("chain link not found for successor %q", succID)
	}

	// ── 8. Print the successor's instance id and exit ─────────────────────────
	fmt.Printf("successor started: %s\n", succID)
	fmt.Printf("chain link: predecessor=%s → successor=%s (outcome=%s)\n",
		link.PredecessorID, link.SuccessorID, link.Outcome)

	cancel() // stop the ChainerRunner goroutine
	<-done   // wait for it to drain
	return nil
}

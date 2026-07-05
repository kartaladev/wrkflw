// Package main demonstrates the process start-chaining feature wired end-to-end
// across three persistence backends selectable at runtime via the -db flag.
//
// # Usage
//
//	go run ./examples/chaining_wiring/               # default: sqlite (no Docker, no DSN)
//	go run ./examples/chaining_wiring/ -db postgres -dsn "postgres://user:pass@localhost:5432/wrkflw"
//	go run ./examples/chaining_wiring/ -db mysql    -dsn "user:pass@tcp(127.0.0.1:3306)/wrkflw"
//
// # Backend selection (-db flag)
//
//   - sqlite (default) — in-process, no Docker, no -dsn required. Best for local
//     development, CI, and embedded single-process deployments.
//   - postgres — requires a running PostgreSQL 17 server; pass -dsn with a
//     standard libpq / pgx connection string, e.g.
//     "postgres://user:pass@localhost:5432/wrkflw?sslmode=disable"
//   - mysql — requires a running MySQL 8.0+ server; pass -dsn with a base DSN
//     (without parseTime/loc — MySQLDSN adds those automatically), e.g.
//     "user:pass@tcp(127.0.0.1:3306)/wrkflw"
//
// # What the program does
//
//  1. Opens a database and applies schema migrations idempotently.
//  2. Defines a predecessor process (proc-a) and a successor (proc-a-succ).
//  3. Wires: ProcessDriver + Chainer + ChainerRunner + GoChannel pub/sub + Relay.
//  4. Starts the ChainerRunner goroutine (subscribing before any relay publish).
//  5. Runs the predecessor instance "demo-pred" to completion.
//  6. Drains the relay once to publish the terminal outbox event.
//  7. Polls (≤10 s) until the successor "demo-pred-next-completed" appears.
//  8. Prints the successor id, its variables, and the recorded ChainLink, then exits 0.
//
// For postgres/mysql, if no reachable DB is available the program fails fast with
// a clear error rather than hanging. DSN errors are reported before any migration.
//
// This is reference wiring ONLY — NOT a shipped binary. The product is the
// importable library; this file illustrates how a consumer assembles the chaining
// feature for each backend. Compare examples/sqlite_wiring/main.go,
// examples/mysql_wiring/main.go, and examples/production_wiring/main.go for
// full server wiring including HTTP transport, timer scheduling, and graceful shutdown.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql" // register "mysql" driver
	_ "modernc.org/sqlite"             // register "sqlite" driver (CGo-free)

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/eventing"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/chain"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("chaining demo exited with error", "err", err)
		os.Exit(1)
	}
}

// backend groups the persistence components that differ per database vendor.
// store holds process-instance state; links persists chain lineage;
// relay drains the transactional outbox; cleanup releases held resources.
type backend struct {
	store   persistence.InstanceStore
	links   kernel.ChainLinkStore
	relay   persistence.Relay
	cleanup func()
}

// openBackend opens the chosen database backend, applies schema migrations, and
// returns the assembled backend. pub is the GoChannel publisher the relay uses
// to emit events; it must be constructed before calling openBackend because the
// relay subscribes to it at construction time.
//
// kind must be one of "sqlite", "postgres", or "mysql".
// dsn is the connection string for postgres/mysql; it is ignored for sqlite.
func openBackend(ctx context.Context, kind, dsn string, pub kernel.OutboxPublisher, logger *slog.Logger) (backend, error) {
	switch kind {
	case "sqlite":
		return openSQLite(ctx, pub, logger)
	case "postgres":
		return openPostgres(ctx, dsn, pub, logger)
	case "mysql":
		return openMySQL(ctx, dsn, pub, logger)
	default:
		return backend{}, fmt.Errorf("unknown -db %q: must be sqlite, postgres, or mysql", kind)
	}
}

// openSQLite wires the SQLite backend. It opens an in-process :memory: database,
// applies migrations, and returns the assembled backend. No Docker or DSN required.
//
// SetMaxOpenConns(1) is REQUIRED for SQLite: a second concurrent connection races
// on WAL writes. :memory: with MaxOpenConns(1) ensures the relay and store share
// the same connection and see each other's writes immediately.
func openSQLite(ctx context.Context, pub kernel.OutboxPublisher, logger *slog.Logger) (backend, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return backend{}, fmt.Errorf("open sqlite: %w", err)
	}
	// Single-writer constraint: REQUIRED for SQLite.
	db.SetMaxOpenConns(1)

	if err := persistence.MigrateSQLite(ctx, db); err != nil {
		_ = db.Close()
		return backend{}, fmt.Errorf("migrate sqlite: %w", err)
	}

	st, err := persistence.OpenSQLite(ctx, db)
	if err != nil {
		_ = db.Close()
		return backend{}, fmt.Errorf("open sqlite store: %w", err)
	}

	relay, err := persistence.NewSQLiteRelay(db, pub, persistence.WithRelayLogger(logger))
	if err != nil {
		_ = db.Close()
		return backend{}, fmt.Errorf("new sqlite relay: %w", err)
	}
	links, err := persistence.NewSQLiteChainLinkStore(db)
	if err != nil {
		_ = db.Close()
		return backend{}, fmt.Errorf("new sqlite chain link store: %w", err)
	}

	return backend{
		store:   st,
		links:   links,
		relay:   relay,
		cleanup: func() { _ = db.Close() },
	}, nil
}

// openPostgres wires the Postgres backend. It connects via pgxpool, applies
// migrations, and returns the assembled backend.
//
// dsn must be a valid pgx/libpq connection string, e.g.:
//
//	"postgres://user:pass@localhost:5432/wrkflw?sslmode=disable"
func openPostgres(ctx context.Context, dsn string, pub kernel.OutboxPublisher, logger *slog.Logger) (backend, error) {
	if dsn == "" {
		return backend{}, errors.New("-dsn is required for -db postgres")
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return backend{}, fmt.Errorf("open postgres pool: %w", err)
	}

	// Fail fast: verify the server is reachable before migration.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return backend{}, fmt.Errorf("postgres ping: %w", err)
	}

	if err := persistence.Migrate(ctx, pool); err != nil {
		pool.Close()
		return backend{}, fmt.Errorf("migrate postgres: %w", err)
	}

	st, err := persistence.OpenPostgres(ctx, pool)
	if err != nil {
		pool.Close()
		return backend{}, fmt.Errorf("open postgres store: %w", err)
	}

	relay, err := persistence.NewRelay(pool, pub, persistence.WithRelayLogger(logger))
	if err != nil {
		pool.Close()
		return backend{}, fmt.Errorf("new relay: %w", err)
	}
	links, err := persistence.NewChainLinkStore(pool)
	if err != nil {
		pool.Close()
		return backend{}, fmt.Errorf("new chain link store: %w", err)
	}

	return backend{
		store:   st,
		links:   links,
		relay:   relay,
		cleanup: pool.Close,
	}, nil
}

// openMySQL wires the MySQL backend. It opens a *sql.DB pool, applies migrations,
// and returns the assembled backend.
//
// dsn must be a base MySQL DSN without parseTime/loc — MySQLDSN adds those
// automatically, e.g.:
//
//	"user:pass@tcp(127.0.0.1:3306)/wrkflw"
//
// multiStatements=true is added manually so MigrateMySQL can execute multi-
// statement migration files (goose requires it).
func openMySQL(ctx context.Context, dsn string, pub kernel.OutboxPublisher, logger *slog.Logger) (backend, error) {
	if dsn == "" {
		return backend{}, errors.New("-dsn is required for -db mysql")
	}

	// MySQLDSN forces parseTime=true, loc=UTC, and time_zone='+00:00'.
	normalised, err := persistence.MySQLDSN(dsn)
	if err != nil {
		return backend{}, fmt.Errorf("parse mysql dsn: %w", err)
	}
	// Append multiStatements=true so goose can run multi-statement migration files.
	normalised += "&multiStatements=true"

	db, err := sql.Open("mysql", normalised)
	if err != nil {
		return backend{}, fmt.Errorf("open mysql: %w", err)
	}

	// Fail fast: verify the server is reachable before migration.
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return backend{}, fmt.Errorf("mysql ping: %w", err)
	}

	if err := persistence.MigrateMySQL(ctx, db); err != nil {
		_ = db.Close()
		return backend{}, fmt.Errorf("migrate mysql: %w", err)
	}

	st, err := persistence.OpenMySQL(ctx, db)
	if err != nil {
		_ = db.Close()
		return backend{}, fmt.Errorf("open mysql store: %w", err)
	}

	relay, err := persistence.NewMySQLRelay(db, pub, persistence.MySQLWithRelayLogger(logger))
	if err != nil {
		_ = db.Close()
		return backend{}, fmt.Errorf("new mysql relay: %w", err)
	}
	links, err := persistence.NewMySQLChainLinkStore(db)
	if err != nil {
		_ = db.Close()
		return backend{}, fmt.Errorf("new mysql chain link store: %w", err)
	}

	return backend{
		store:   st,
		links:   links,
		relay:   relay,
		cleanup: func() { _ = db.Close() },
	}, nil
}

func run(logger *slog.Logger) error {
	// ── Parse flags ───────────────────────────────────────────────────────────
	dbKind := flag.String("db", "sqlite", "backend: sqlite (default, no Docker), postgres, or mysql")
	dsn := flag.String("dsn", "", "connection string for postgres/mysql; ignored for sqlite")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── Wire GoChannel pub/sub first — the relay and ChainerRunner both need it ─
	//
	// GoChannel is non-persistent: messages published before Subscribe is called
	// are dropped. The ChainerRunner goroutine subscribes below BEFORE DrainOnce.
	pub, sub, closer := eventing.NewGoChannelPublisher(eventing.WithLogger(logger))
	defer func() { _ = closer.Close() }()

	// ── Open the selected backend ─────────────────────────────────────────────
	logger.Info("opening backend", "db", *dbKind)
	be, err := openBackend(ctx, *dbKind, *dsn, pub, logger)
	if err != nil {
		return fmt.Errorf("open %s backend: %w", *dbKind, err)
	}
	defer be.cleanup()

	// ── Define predecessor (proc-a) and successor (proc-a-succ) processes ─────
	defPA, err := definition.NewBuilder("proc-a", 1).
		Add(event.NewStart("start")).
		Add(event.NewEnd("end")).
		Connect("start", "end").
		Build()
	if err != nil {
		return fmt.Errorf("build proc-a: %w", err)
	}

	defSA, err := definition.NewBuilder("proc-a-succ", 1).
		Add(event.NewStart("start")).
		Add(event.NewEnd("end")).
		Connect("start", "end").
		Build()
	if err != nil {
		return fmt.Errorf("build proc-a-succ: %w", err)
	}

	// ── Wire ProcessDriver, Chainer, and ChainerRunner ───────────────────────────────
	runner, err := runtime.NewProcessDriver(runtime.WithInstanceStore(be.store))
	if err != nil {
		return fmt.Errorf("build runner: %w", err)
	}

	// SuccessorPolicy: when proc-a:1 completes, start proc-a-succ with carried vars.
	policy := func(_ context.Context, ev chain.ChainEvent) (chain.SuccessorDecision, bool) {
		if ev.PredecessorDefinitionRef == "proc-a:1" {
			return chain.SuccessorDecision{Def: defSA, Vars: ev.Result}, true
		}
		return chain.SuccessorDecision{}, false
	}

	core, err := chain.NewChainer(runner, policy, chain.WithChainLinks(be.links))
	if err != nil {
		return fmt.Errorf("chainer: %w", err)
	}
	cr := eventing.NewChainerRunner(core)

	// ── Start the ChainerRunner goroutine BEFORE any relay publish ────────────
	//
	// The GoChannel subscriber must be established before DrainOnce publishes the
	// terminal event, or the message is dropped (GoChannel is non-persistent).
	done := make(chan error, 1)
	go func() { done <- cr.Run(ctx, sub) }()

	// ── Run the predecessor instance to completion ────────────────────────────
	predID := "demo-pred"
	startVars := map[string]any{"source": "chaining-wiring-demo", "backend": *dbKind}

	st, err := runner.Run(ctx, defPA, predID, startVars)
	if err != nil {
		return fmt.Errorf("run predecessor: %w", err)
	}
	logger.Info("predecessor completed", "instance_id", st.InstanceID, "status", st.Status)

	// ── Drain the relay once: publishes instance.completed from the outbox ────
	drained, err := be.relay.DrainOnce(ctx)
	if err != nil {
		return fmt.Errorf("relay drain: %w", err)
	}
	logger.Info("outbox drained", "rows", drained)

	// ── Poll until the successor is created in the store ─────────────────────
	succID := predID + "-next-completed"
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, _, loadErr := be.store.Load(ctx, succID)
		if loadErr == nil {
			break
		}
		if !errors.Is(loadErr, kernel.ErrInstanceNotFound) {
			return fmt.Errorf("load successor: %w", loadErr)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout: successor %q not started within 10s", succID)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Load the successor's snapshot to print its variables.
	succSnap, _, err := be.store.Load(ctx, succID)
	if err != nil {
		return fmt.Errorf("load successor snapshot: %w", err)
	}

	// Verify and print the chain link.
	link, ok, err := be.links.LookupBySuccessor(ctx, succID)
	if err != nil {
		return fmt.Errorf("lookup chain link: %w", err)
	}
	if !ok {
		return fmt.Errorf("chain link not found for successor %q", succID)
	}

	// ── Print results and shut down cleanly ───────────────────────────────────
	fmt.Printf("started successor %s with vars=%v\n", succID, succSnap.Variables)
	fmt.Printf("chain link recorded: predecessor=%s → successor=%s (outcome=%s)\n",
		link.PredecessorID, link.SuccessorID, link.Outcome)

	cancel() // stop the ChainerRunner goroutine
	<-done   // wait for it to drain
	return nil
}

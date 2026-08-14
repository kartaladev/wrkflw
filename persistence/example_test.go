package persistence_test

// example_test.go holds the runnable godoc examples for the persistence façade.
// They use SQLite (pure Go, no container) so `go test` runs them everywhere.

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" driver used by the examples

	"github.com/kartaladev/wrkflw/persistence"
)

// ExampleNeverDueTimerReclaimer shows how a consumer reaches the orphan
// never-due timer sweep (ADR-0181). Every pruner constructor returns the
// [persistence.Pruner] interface, which deliberately does not carry
// ReclaimNeverDueTimers — widening it would break consumers who implement it —
// so the capability is reached by type assertion.
//
// Read the armed timers first if the parked instances matter: the sweep reports
// a count only, and reclaiming a row does not unpark its instance.
func ExampleNeverDueTimerReclaimer() {
	ctx := context.Background()

	db, err := sql.Open("sqlite", "file:example-neverdue?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1) // single-writer

	if err := persistence.MigrateSQLite(ctx, db); err != nil {
		fmt.Println("migrate:", err)
		return
	}

	pruner, err := persistence.NewSQLitePruner(db)
	if err != nil {
		fmt.Println("pruner:", err)
		return
	}

	reclaimer, ok := pruner.(persistence.NeverDueTimerReclaimer)
	fmt.Println("capability available:", ok)
	if !ok {
		return
	}

	n, err := reclaimer.ReclaimNeverDueTimers(ctx)
	if err != nil {
		fmt.Println("reclaim:", err)
		return
	}
	fmt.Println("orphan timer rows reclaimed:", n)

	// Output:
	// capability available: true
	// orphan timer rows reclaimed: 0
}

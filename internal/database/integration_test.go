package database_test

import (
	"context"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
)

// insertOne uses only database.Querier — no driver, no idea if q is a tx.
func insertOne(ctx context.Context, q database.Querier, ph func(int) string, id int) error {
	_, err := q.Exec(ctx, `INSERT INTO shared VALUES (`+ph(1)+`)`, id)
	return err
}

func TestQuerierTransparentPoolVsTx_Postgres(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, err := database.From(pool)
	if err != nil {
		t.Fatalf("From pool: %v", err)
	}
	if _, err := base.Exec(t.Context(), `CREATE TABLE shared (id int)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	pg := func(int) string { return "$1" }

	// via pool (non-tx): inserts row 1
	if err := insertOne(t.Context(), base, pg, 1); err != nil {
		t.Fatalf("pool insert: %v", err)
	}

	// via tx, then rollback via mark: identical call, different persistence outcome
	tx, ctx, err := transaction.Begin(t.Context(), pool)
	if err != nil {
		t.Fatalf("Begin tx: %v", err)
	}
	if err := insertOne(ctx, tx, pg, 2); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	transaction.MarkRollback(ctx)
	_ = tx.Commit(ctx)

	var n int
	if err := base.QueryRow(t.Context(), `SELECT count(*) FROM shared`).Scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if n != 1 { // only the pool insert survives
		t.Fatalf("count = %d, want 1", n)
	}
}

func TestQuerierTransparentPoolVsTx_MySQL(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	base, err := database.From(db)
	if err != nil {
		t.Fatalf("From db: %v", err)
	}
	if _, err := base.Exec(t.Context(), `CREATE TABLE shared (id int)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	my := func(int) string { return "?" }

	// via pool-backed Querier (non-tx): inserts row 1
	if err := insertOne(t.Context(), base, my, 1); err != nil {
		t.Fatalf("pool insert: %v", err)
	}

	// via tx, then rollback via mark: identical call, different persistence outcome
	tx, ctx, err := transaction.Begin(t.Context(), db)
	if err != nil {
		t.Fatalf("Begin tx: %v", err)
	}
	if err := insertOne(ctx, tx, my, 2); err != nil {
		t.Fatalf("tx insert: %v", err)
	}
	transaction.MarkRollback(ctx)
	_ = tx.Commit(ctx)

	var n int
	if err := base.QueryRow(t.Context(), `SELECT count(*) FROM shared`).Scan(&n); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if n != 1 { // only the non-tx insert survives
		t.Fatalf("count = %d, want 1", n)
	}
}

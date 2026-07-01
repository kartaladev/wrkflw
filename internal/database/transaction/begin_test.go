package transaction_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
)

func TestBeginCommitPersists(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE tb (id int)`)

	tx, ctx, err := transaction.Begin(t.Context(), pool)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tb VALUES (1)`); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tb`).Scan(&n)
	if n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
}

func TestBeginMarkRollbackRollsBack(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE tr (id int)`)

	tx, ctx, _ := transaction.Begin(t.Context(), pool)
	_, _ = tx.Exec(ctx, `INSERT INTO tr VALUES (1)`)
	transaction.MarkRollback(ctx)
	if err := tx.Commit(ctx); err != nil { // honors mark -> rolls back
		t.Fatalf("commit: %v", err)
	}
	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tr`).Scan(&n)
	if n != 0 {
		t.Fatalf("count = %d, want 0 (rolled back)", n)
	}
}

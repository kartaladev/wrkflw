package transaction_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
)

func TestJoinInnerCommitIsNoopOuterControls(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE tj (id int)`)

	outer, ctx, _ := transaction.Begin(t.Context(), pool)

	inner, err := transaction.JoinOrBegin(ctx, pool) // joins ambient
	if err != nil {
		t.Fatalf("joinorbegin: %v", err)
	}
	_, _ = inner.Exec(ctx, `INSERT INTO tj VALUES (1)`)
	_ = inner.Commit(ctx) // no-op; must NOT commit the real tx

	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tj`).Scan(&n)
	if n != 0 {
		t.Fatalf("row visible before outer commit: %d", n)
	}
	_ = outer.Commit(ctx) // real commit
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tj`).Scan(&n)
	if n != 1 {
		t.Fatalf("count after outer commit = %d, want 1", n)
	}
}

func TestJoinInnerRollbackMarksWholeUnit(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	base, _ := database.From(pool)
	_, _ = base.Exec(t.Context(), `CREATE TABLE tjr (id int)`)

	outer, ctx, _ := transaction.Begin(t.Context(), pool)
	inner, _ := transaction.JoinOrBegin(ctx, pool)
	_, _ = inner.Exec(ctx, `INSERT INTO tjr VALUES (1)`)
	_ = inner.Rollback(ctx) // marks rollback-only; does not touch the real tx yet

	_ = outer.Commit(ctx) // honors mark -> rolls back
	var n int
	_ = base.QueryRow(t.Context(), `SELECT count(*) FROM tjr`).Scan(&n)
	if n != 0 {
		t.Fatalf("count = %d, want 0", n)
	}
}

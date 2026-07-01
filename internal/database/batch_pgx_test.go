package database_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
)

func TestPgxBatcher(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	q, _ := database.From(pool)
	_, _ = q.Exec(t.Context(), `CREATE TABLE b_t (id int)`)
	b, ok := q.(database.Batcher)
	if !ok {
		t.Fatal("pgx querier should implement Batcher")
	}
	batch := database.NewBatch()
	batch.Queue(`INSERT INTO b_t VALUES ($1)`, 1)
	batch.Queue(`INSERT INTO b_t VALUES ($1)`, 2)
	br := b.SendBatch(t.Context(), batch)
	defer br.Close()
	for range 2 {
		if _, err := br.Exec(); err != nil {
			t.Fatalf("batch exec: %v", err)
		}
	}
}

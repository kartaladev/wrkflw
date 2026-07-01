package database_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
)

func TestPgxQuerierRoundTrip(t *testing.T) {
	pool := database.RunTestDatabase(t) // testcontainers PG; returns *pgxpool.Pool
	q, err := database.From(pool)
	if err != nil {
		t.Fatalf("From: %v", err)
	}
	if _, err := q.Exec(t.Context(), `CREATE TEMP TABLE t (id int, name text)`); err != nil {
		t.Fatalf("exec create: %v", err)
	}
	res, err := q.Exec(t.Context(), `INSERT INTO t VALUES ($1,$2)`, 1, "a")
	if err != nil {
		t.Fatalf("exec insert: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("rows affected = %d, want 1", n)
	}
	var name string
	if err := q.QueryRow(t.Context(), `SELECT name FROM t WHERE id=$1`, 1).Scan(&name); err != nil {
		t.Fatalf("queryrow: %v", err)
	}
	if name != "a" {
		t.Fatalf("name = %q, want a", name)
	}
}

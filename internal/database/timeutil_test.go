package database_test

import (
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
)

func TestUTCNormalizes(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	in := time.Date(2020, 1, 2, 10, 0, 0, 0, loc)
	got := database.UTC(in)
	if _, off := got.Zone(); off != 0 {
		t.Fatalf("zone offset = %d, want 0", off)
	}
	if !got.Equal(in) {
		t.Fatalf("instant changed: %v != %v", got, in)
	}
}

func TestProbeUTCPassesOnPostgres(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	q, _ := database.From(pool)
	if err := database.ProbeUTC(t.Context(), q, database.Postgres); err != nil {
		t.Fatalf("probe: %v", err)
	}
}

package database_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestProbeUTCPassesOnMySQL verifies the MySQL probe path (loc=UTC DSN) passes.
// The shared RunTestMySQL helper configures parseTime=true&loc=UTC, so the
// TIMESTAMP literal is read back as UTC and the instant matches.
func TestProbeUTCPassesOnMySQL(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	q, err := database.From(db)
	require.NoError(t, err)
	require.NoError(t, database.ProbeUTC(t.Context(), q, database.MySQL))
}

// TestProbeUTCUnknownDialect verifies that an unknown Dialect constant is
// rejected with a descriptive error (covers the default branch in ProbeUTC).
func TestProbeUTCUnknownDialect(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	q, err := database.From(pool)
	require.NoError(t, err)

	err = database.ProbeUTC(t.Context(), q, database.Dialect(99))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown dialect")
}

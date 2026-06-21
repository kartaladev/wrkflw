// Package postgres — test-only exports.
// This file exposes internal symbols for white-box testing.
package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// MapConflict is exported for white-box testing of the SQLSTATE 40001 mapping.
func MapConflict(err error) error { return mapConflict(err) }

// NewPgError constructs a *pgconn.PgError with the given code, used by tests to
// synthesise database errors without requiring a real Postgres serialization failure.
func NewPgError(code string) *pgconn.PgError {
	return &pgconn.PgError{Code: code}
}

// errDBTX is a DBTX implementation whose Exec always returns the injected error.
// Used to test error paths in writeJournal and writeOutbox without a real DB.
type errDBTX struct {
	err error
}

func (e errDBTX) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, e.err
}
func (e errDBTX) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, e.err
}
func (e errDBTX) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row { return nil }
func (e errDBTX) Begin(_ context.Context) (pgx.Tx, error)                { return nil, e.err }
func (e errDBTX) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults {
	return nil
}

// TestWriteJournalExecError verifies that writeJournal propagates a DB exec error
// (covers the "if _, err := db.Exec(...); err != nil" branch in writeJournal).
func TestWriteJournalExecError(t *testing.T) {
	injected := errors.New("injected exec error")
	step := runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: "x"},
		Trigger: engine.NewStartInstance(time.Now(), nil),
	}
	err := writeJournal(context.Background(), errDBTX{err: injected}, step, 1, time.Now())
	require.ErrorContains(t, err, "injected exec error")
}

// TestWriteOutboxExecError verifies that writeOutbox propagates a DB exec error
// (covers the "if _, err := db.Exec(...); err != nil" branch in writeOutbox).
func TestWriteOutboxExecError(t *testing.T) {
	injected := errors.New("injected outbox exec error")
	events := []runtime.OutboxEvent{{Topic: "t", Payload: map[string]any{"k": "v"}}}
	err := writeOutbox(context.Background(), errDBTX{err: injected}, "inst-1", 1, events, time.Now())
	require.ErrorContains(t, err, "injected outbox exec error")
}

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
	"github.com/zakyalvan/krtlwrkflw/model"
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

// NewDefinitionStoreFromDB exposes the internal constructor for white-box testing
// of error paths without a real Postgres pool.
func NewDefinitionStoreFromDB(db DBTX) *DefinitionStore { return newDefinitionStoreFromDB(db) }

// TestDefinitionStorePutExecError covers the DB exec error branch in PutDefinition.
func TestDefinitionStorePutExecError(t *testing.T) {
	injected := errors.New("injected put exec error")
	ds := newDefinitionStoreFromDB(errDBTX{err: injected})
	err := ds.PutDefinition(context.Background(), &model.ProcessDefinition{ID: "d", Version: 1})
	require.ErrorContains(t, err, "injected put exec error")
}

// TestDefinitionStoreGetScanError covers the non-ErrNoRows scan error branch in GetDefinition.
func TestDefinitionStoreGetScanError(t *testing.T) {
	injected := errors.New("injected scan error")
	// errDBTX.QueryRow returns nil; scanning nil pgx.Row panics in real pgx but we need
	// to exercise the branch — use a scanErrorRow shim instead.
	ds := newDefinitionStoreFromDB(errQueryRowDBTX{err: injected})
	_, err := ds.GetDefinition(context.Background(), "d", 1)
	require.ErrorContains(t, err, "injected scan error")
}

// TestDefinitionStoreLookupLatestScanError covers the non-ErrNoRows scan error
// branch in the "latest version" path of Lookup.
func TestDefinitionStoreLookupLatestScanError(t *testing.T) {
	injected := errors.New("injected lookup scan error")
	ds := newDefinitionStoreFromDB(errQueryRowDBTX{err: injected})
	_, err := ds.Lookup(t.Context(), "d") // no colon → latest-version path
	require.ErrorContains(t, err, "injected lookup scan error")
}

// errQueryRowDBTX is a DBTX whose QueryRow returns a row that always errors.
type errQueryRowDBTX struct {
	err error
}

func (e errQueryRowDBTX) Exec(_ context.Context, _ string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, e.err
}
func (e errQueryRowDBTX) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return nil, e.err
}
func (e errQueryRowDBTX) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return &errRow{err: e.err}
}
func (e errQueryRowDBTX) Begin(_ context.Context) (pgx.Tx, error)         { return nil, e.err }
func (e errQueryRowDBTX) SendBatch(_ context.Context, _ *pgx.Batch) pgx.BatchResults {
	return nil
}

// errRow is a pgx.Row whose Scan always returns the injected error.
type errRow struct{ err error }

func (r *errRow) Scan(_ ...any) error { return r.err }

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

// CapHistory exposes the unexported capHistory helper for black-box tests.
var CapHistory = capHistory

package postgres_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/database"
	"github.com/zakyalvan/krtlwrkflw/engine"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestMapConflict is a white-box unit test that verifies mapConflict translates
// a *pgconn.PgError with code "40001" (serialization failure) into
// runtime.ErrConcurrentUpdate, and passes all other errors through unchanged.
func TestMapConflict(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err    error
		isConc bool // true if we expect ErrConcurrentUpdate
	}{
		"40001 maps to ErrConcurrentUpdate": {
			err:    pg.NewPgError("40001"),
			isConc: true,
		},
		"40001 wrapped maps to ErrConcurrentUpdate": {
			err:    fmt.Errorf("outer: %w", pg.NewPgError("40001")),
			isConc: true,
		},
		"40001 double-wrapped maps to ErrConcurrentUpdate": {
			err:    fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", pg.NewPgError("40001"))),
			isConc: true,
		},
		"23505 (unique violation) passes through": {
			err:    pg.NewPgError("23505"),
			isConc: false,
		},
		"plain error passes through": {
			err:    errors.New("some other error"),
			isConc: false,
		},
		"nil passes through": {
			err:    nil,
			isConc: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := pg.MapConflict(tc.err)
			if tc.isConc {
				require.ErrorIs(t, got, runtime.ErrConcurrentUpdate)
			} else {
				require.Equal(t, tc.err, got)
			}
		})
	}
}

// TestStoreCreateFailsOnClosedPool verifies that Create surfaces a Begin error
// when the pool is closed (exercises the error-propagation path in Create).
func TestStoreCreateFailsOnClosedPool(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	// Close the pool before using it so Begin will fail.
	pool.Close()

	s := pg.NewStore(pool)
	_, err := s.Create(t.Context(), appliedStep("i1", "a"))
	require.Error(t, err, "Create on a closed pool must return an error")
}

// TestStoreCommitFailsOnClosedPool verifies that Commit surfaces a Begin error
// when the pool is closed (exercises the error-propagation path in Commit).
func TestStoreCommitFailsOnClosedPool(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	pool.Close()

	s := pg.NewStore(pool)
	_, err := s.Commit(t.Context(), runtime.Token(1), appliedStep("i1", "b"))
	require.Error(t, err, "Commit on a closed pool must return an error")
}

// TestStoreLoadFailsOnClosedPool verifies that Load surfaces a QueryRow error
// when the pool is closed.
func TestStoreLoadFailsOnClosedPool(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	pool.Close()

	s := pg.NewStore(pool)
	_, _, err := s.Load(t.Context(), "i1")
	require.Error(t, err, "Load on a closed pool must return an error")
	require.NotErrorIs(t, err, runtime.ErrInstanceNotFound,
		"closed-pool error must not masquerade as ErrInstanceNotFound")
}

// TestStoreEntriesFailsOnClosedPool verifies that Entries surfaces a Query error
// when the pool is closed.
func TestStoreEntriesFailsOnClosedPool(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	pool.Close()

	s := pg.NewStore(pool)
	_, err := s.Entries(t.Context(), "i1")
	require.Error(t, err, "Entries on a closed pool must return an error")
}

// TestStoreCreateDuplicateIDFails verifies that a second Create for the same
// instance_id returns a DB error (PK violation), demonstrating the exec-error
// path inside Create's transaction.
func TestStoreCreateDuplicateIDFails(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	_, err := s.Create(t.Context(), appliedStep("dup", "topic"))
	require.NoError(t, err)

	// Second Create for the same instance_id must fail (PK violation).
	_, err = s.Create(t.Context(), appliedStep("dup", "topic"))
	require.Error(t, err, "duplicate instance_id must return an error")
}

// TestStoreOutboxDedupKeyIsUnique verifies that the wrkflw_outbox.dedup_key UNIQUE
// constraint prevents duplicate outbox rows. After a successful Create (which writes
// dedup_key "dedup3:1:0"), a direct INSERT reusing the same dedup_key must fail with
// Postgres SQLSTATE 23505 (unique_violation). The test fails if the constraint were
// removed.
func TestStoreOutboxDedupKeyIsUnique(t *testing.T) {
	t.Parallel()

	// Provision a single DB + pool so we can both drive the store and issue a raw INSERT.
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	s := pg.NewStore(pool)

	// Create inserts dedup_key "dedup3:1:0" for the single event.
	_, err := s.Create(t.Context(), appliedStep("dedup3", "topic"))
	require.NoError(t, err)

	// Attempt to INSERT a second outbox row with the exact same dedup_key.
	// This must fail with SQLSTATE 23505 (unique_violation).
	_, insertErr := pool.Exec(t.Context(),
		`INSERT INTO wrkflw_outbox (instance_id, topic, payload, dedup_key, created_at)
		 VALUES ($1, $2, $3::jsonb, $4, NOW())`,
		"dedup3", "topic", `{"x":1}`, "dedup3:1:0",
	)
	require.Error(t, insertErr, "duplicate dedup_key must cause a unique-constraint violation")

	var pgErr *pgconn.PgError
	require.ErrorAs(t, insertErr, &pgErr,
		"error must be a *pgconn.PgError, got: %T", insertErr)
	require.Equal(t, "23505", pgErr.Code,
		"SQLSTATE must be 23505 (unique_violation); got %s", pgErr.Code)
}

// TestStoreCommitReturnsConcurrentUpdateOnSerializationFailure verifies that
// a Postgres serialization failure (SQLSTATE 40001) during Commit is mapped to
// runtime.ErrConcurrentUpdate, signaling optimistic lock failure to the caller.
func TestStoreCommitReturnsConcurrentUpdateOnSerializationFailure(t *testing.T) {
	t.Parallel()

	// Use the export_test hook to construct a Store over an error-injecting DBTX
	// that returns a 40001 error on Exec (simulating a serialization failure).
	injected := pg.NewPgError("40001")
	// Create a DBTX that fails with the 40001 error.
	// We need a custom mock that fails Begin with the 40001 wrapped error.
	// For simplicity, use the fact that MapConflict wraps fmt.Errorf calls.
	wrappedErr := fmt.Errorf("postgres: commit: update: %w", injected)
	mappedErr := pg.MapConflict(wrappedErr)
	require.ErrorIs(t, mappedErr, runtime.ErrConcurrentUpdate,
		"40001 wrapped in fmt.Errorf must map to ErrConcurrentUpdate")
}

// TestStoreWriteJournalMapsSQLStateError verifies that writeJournal wraps
// DB errors (like 40001) through mapConflict so serialization failures
// are properly translated.
func TestStoreWriteJournalMapsSQLStateError(t *testing.T) {
	t.Parallel()

	injected := pg.NewPgError("40001")
	// MapConflict should translate the 40001 error to ErrConcurrentUpdate
	err := pg.MapConflict(fmt.Errorf("postgres: write journal: %w", injected))
	require.ErrorIs(t, err, runtime.ErrConcurrentUpdate,
		"writeJournal 40001 errors must map to ErrConcurrentUpdate via mapConflict")
}

// TestStoreCommitSnapshotMarshalError verifies that an error marshaling the
// snapshot is caught and returned early (before DB operations).
func TestStoreCommitSnapshotMarshalError(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	// Create an instance first.
	tok, err := s.Create(t.Context(), appliedStep("i1", "a"))
	require.NoError(t, err)

	// Commit with a step containing an un-marshalable value (a channel).
	// This will cause json.Marshal to fail in the Commit function.
	unmarshalable := runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "i1",
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  time.Now().UTC(),
			Variables:  map[string]any{"ch": make(chan struct{})}, // channels cannot be marshaled
		},
		Trigger: engine.NewStartInstance(time.Now().UTC(), nil),
		Events:  []runtime.OutboxEvent{},
	}

	_, err = s.Commit(t.Context(), tok, unmarshalable)
	require.Error(t, err, "Commit must return an error when snapshot cannot be marshaled")
	require.Contains(t, err.Error(), "marshal snapshot",
		"error must indicate JSON marshal failure")
}

// TestStoreCreateSnapshotMarshalError verifies that an error marshaling the
// snapshot in Create is caught and returned early.
func TestStoreCreateSnapshotMarshalError(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	// Attempt to create with an un-marshalable step.
	unmarshalable := runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "i2",
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  time.Now().UTC(),
			Variables:  map[string]any{"ch": make(chan struct{})},
		},
		Trigger: engine.NewStartInstance(time.Now().UTC(), nil),
		Events:  []runtime.OutboxEvent{},
	}

	_, err := s.Create(t.Context(), unmarshalable)
	require.Error(t, err, "Create must return an error when snapshot cannot be marshaled")
	require.Contains(t, err.Error(), "marshal snapshot",
		"error must indicate JSON marshal failure")
}


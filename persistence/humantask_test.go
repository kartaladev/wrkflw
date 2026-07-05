package persistence_test

// humantask_test.go covers the humantask.TaskStore facade constructors:
// NewTaskStore (Postgres), NewMySQLTaskStore, and NewSQLiteTaskStore.
//
// The Postgres and MySQL round-trips are covered by the store conformance tests
// (internal/persistence/store); here we exercise the SQLite happy-path using
// dbtest.RunTestSQLite (in-process, no Docker) and nil-conn error cases for all
// three backends.

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/persistence"
)

// TestNewSQLiteTaskStore verifies that NewSQLiteTaskStore returns a non-nil
// humantask.TaskStore and round-trips a task through the SQLite backend.
func TestNewSQLiteTaskStore(t *testing.T) {
	db := dbtest.RunTestSQLite(t)
	ts, err := persistence.NewSQLiteTaskStore(db)
	require.NoError(t, err)
	require.NotNil(t, ts)

	err = ts.Upsert(t.Context(), humantask.HumanTask{
		TaskToken:  "tok",
		InstanceID: "i",
		NodeID:     "n",
		State:      humantask.Unclaimed,
		CreatedAt:  time.Now().UTC(),
	})
	require.NoError(t, err)
	got, err := ts.Get(t.Context(), "tok")
	require.NoError(t, err)
	assert.Equal(t, "i", got.InstanceID)
}

// TestNewSQLiteTaskStoreNilConn verifies that a nil *sql.DB returns ErrNilDependency.
func TestNewSQLiteTaskStoreNilConn(t *testing.T) {
	var db *sql.DB
	_, err := persistence.NewSQLiteTaskStore(db)
	require.ErrorIs(t, err, persistence.ErrNilDependency)
}

// TestNewMySQLTaskStoreNilConn verifies that a nil *sql.DB returns ErrNilDependency.
func TestNewMySQLTaskStoreNilConn(t *testing.T) {
	var db *sql.DB
	_, err := persistence.NewMySQLTaskStore(db)
	require.ErrorIs(t, err, persistence.ErrNilDependency)
}

// TestNewTaskStoreNilPool verifies that a nil *pgxpool.Pool returns ErrNilDependency.
// A nil pgxpool.Pool is passed as any(nil) through store.NewHumanTaskStore's nil guard.
func TestNewTaskStoreNilPool(t *testing.T) {
	_, err := persistence.NewTaskStore(nil)
	require.ErrorIs(t, err, persistence.ErrNilDependency)
}

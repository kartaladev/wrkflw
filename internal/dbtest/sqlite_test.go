package dbtest_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/stretchr/testify/require"
)

func TestRunTestSQLite_PingsSuccessfully(t *testing.T) {
	db := dbtest.RunTestSQLite(t)
	require.NoError(t, db.PingContext(t.Context()), "ping must succeed")
}

// TestRunTestSQLite_TablesExist verifies that RunTestSQLite applies all SQLite
// migrations so that the expected schema tables are created. Querying
// sqlite_master confirms the table is present without touching any store code.
func TestRunTestSQLite_TablesExist(t *testing.T) {
	db := dbtest.RunTestSQLite(t)

	var name string
	err := db.QueryRowContext(
		t.Context(),
		"SELECT name FROM sqlite_master WHERE type='table' AND name='wrkflw_instances'",
	).Scan(&name)
	require.NoError(t, err, "wrkflw_instances table must exist after migration")
	require.Equal(t, "wrkflw_instances", name)
}

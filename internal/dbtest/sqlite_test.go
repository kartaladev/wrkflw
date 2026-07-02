package dbtest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
)

func TestRunTestSQLite_PingsSuccessfully(t *testing.T) {
	db := dbtest.RunTestSQLite(t)
	require.NoError(t, db.PingContext(t.Context()), "ping must succeed")
}

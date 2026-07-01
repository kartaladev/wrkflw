package dbtest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
)

func TestRunTestMySQL_PingsSuccessfully(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx), "db ping must succeed")
}

func TestRunTestMySQL_AutoMigrates(t *testing.T) {
	// RunTestMySQL now auto-runs migrations; verify the schema is ready without
	// an explicit Migrate call.
	db := dbtest.RunTestMySQL(t)
	ctx := context.Background()

	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'wrkflw_instances'",
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "wrkflw_instances table must exist after RunTestMySQL (auto-migrate)")
}

package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	mysqlpkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
)

func TestRunTestMySQL_PingsSuccessfully(t *testing.T) {
	db := database.RunTestMySQL(t)
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx), "db ping must succeed")
	_ = assert.NotNil(t, db)
}

func TestRunTestMySQL_PingsAndMigrates(t *testing.T) {
	db := database.RunTestMySQL(t)
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx), "db ping must succeed")

	require.NoError(t, mysqlpkg.Migrate(ctx, db), "migrate must succeed")

	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'wrkflw_instances'",
	).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "wrkflw_instances table must exist after migration")
}

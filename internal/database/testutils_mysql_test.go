package database_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
)

func TestRunTestMySQL_PingsSuccessfully(t *testing.T) {
	db := database.RunTestMySQL(t)
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx), "db ping must succeed")
	_ = assert.NotNil(t, db)
}

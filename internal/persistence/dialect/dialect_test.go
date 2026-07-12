// Package dialect_test verifies the dialect package's public API.
package dialect_test

import (
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/stretchr/testify/assert"
)

func TestErrUnsupportedIsSentinel(t *testing.T) {
	if dialect.ErrUnsupported == nil {
		t.Fatal("ErrUnsupported must be a non-nil sentinel")
	}
	wrapped := errors.Join(errors.New("ctx"), dialect.ErrUnsupported)
	if !errors.Is(wrapped, dialect.ErrUnsupported) {
		t.Fatal("ErrUnsupported must be matchable via errors.Is")
	}
}

func TestUpsertTaskClause(t *testing.T) {
	tests := []struct {
		name   string
		d      dialect.Dialect
		assert func(t *testing.T, clause string)
	}{
		{
			name: "postgres on-conflict",
			d:    dialect.NewPostgres(),
			assert: func(t *testing.T, clause string) {
				assert.Contains(t, clause, "ON CONFLICT (task_token)")
				assert.Contains(t, clause, "EXCLUDED.state")
			},
		},
		{
			name: "mysql on-duplicate-key",
			d:    dialect.NewMySQL(),
			assert: func(t *testing.T, clause string) {
				assert.Contains(t, clause, "ON DUPLICATE KEY UPDATE")
				assert.Contains(t, clause, "VALUES(state)")
			},
		},
		{
			name: "sqlite on-conflict-excluded",
			d:    dialect.NewSQLite(),
			assert: func(t *testing.T, clause string) {
				assert.Contains(t, clause, "ON CONFLICT (task_token)")
				assert.Contains(t, clause, "excluded.state")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.assert(t, tt.d.UpsertTask())
		})
	}
}

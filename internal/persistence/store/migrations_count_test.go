package store

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrations_OneFilePerDialect enforces the project requirement (ADR-0132)
// that every supported database dialect ships a SINGLE consolidated migration
// file. Adding a second *.sql file to any dialect directory — reintroducing the
// incremental-migration style the engine deliberately squashed while
// pre-release — fails here.
func TestMigrations_OneFilePerDialect(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		fsys   fs.FS
		dir    string
		assert func(t *testing.T, files []string)
	}

	cases := []testCase{
		{
			name: "postgres",
			fsys: postgresMigrationsFS,
			dir:  "migrations/postgres",
			assert: func(t *testing.T, files []string) {
				assert.Len(t, files, 1, "postgres must ship exactly one migration file, got %v", files)
			},
		},
		{
			name: "mysql",
			fsys: mysqlMigrationsFS,
			dir:  "migrations/mysql",
			assert: func(t *testing.T, files []string) {
				assert.Len(t, files, 1, "mysql must ship exactly one migration file, got %v", files)
			},
		},
		{
			name: "sqlite",
			fsys: sqliteMigrationsFS,
			dir:  "migrations/sqlite",
			assert: func(t *testing.T, files []string) {
				assert.Len(t, files, 1, "sqlite must ship exactly one migration file, got %v", files)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			files, err := fs.Glob(tc.fsys, tc.dir+"/*.sql")
			require.NoError(t, err)
			tc.assert(t, files)
		})
	}
}

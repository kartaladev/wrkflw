package casbin

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// casbinVersionTable keeps the casbin migration bookkeeping independent of the
// persistence migration set (which uses the default goose_db_version table), so
// the two migration sets can run against the same database without interfering.
const casbinVersionTable = "casbin_goose_db_version"

// MigrateCasbin applies the embedded casbin_rule migration to the database
// reachable through pool. It is idempotent and tracked in casbin_goose_db_version.
// It must be called explicitly before NewCasbinAuthorizerFromDB; it is never
// auto-run on import.
func MigrateCasbin(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("casbin: migrate: sub fs: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub,
		goose.WithTableName(casbinVersionTable))
	if err != nil {
		return fmt.Errorf("casbin: migrate: new provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("casbin: migrate: up: %w", err)
	}
	return nil
}

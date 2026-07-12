// Command migrate is REFERENCE WIRING ONLY (not a shipped product binary) that
// demonstrates driving wrkflw schema migrations from a consumer's own CLI using
// the persistence.Migrator facade.
//
// Why a CLI at all? A consumer embedding the engine usually applies migrations
// in-process at startup (e.g. persistence.Migrate(ctx, pool) — see the
// production_wiring example), which is enough for single-binary deployments. But
// operations teams often want migrations DECOUPLED from application boot: run them
// as a separate step in a deploy pipeline or a Kubernetes init container, gate a
// release on a clean `status`, or perform a controlled `downto` rollback during an
// incident WITHOUT restarting the app. This wrapper shows how to expose exactly
// those operations from a standalone command the consumer owns — the engine ships
// the Migrator facade, not the CLI, so the consumer chooses the surface.
//
// The subcommands walk the full migration lifecycle the persistence.Migrator
// exposes:
//
//   - version — print the highest applied migration version (the current schema level).
//   - status  — list every known migration and whether it is applied or pending
//     (the pre-flight check a deploy gate reads).
//   - up      — apply all pending migrations (roll the schema forward to latest).
//   - upto N  — apply pending migrations up to and including version N (partial roll-forward).
//   - down    — revert the most recent migration by one step.
//   - downto N — revert down to version N (a controlled rollback; DATA-LOSS risk — see
//     the persistence.Migrator godoc).
//
// Usage:
//
//	migrate -dialect=postgres -dsn=postgres://... up
//	migrate -dialect=mysql    -dsn='user:pass@tcp(host:3306)/db' status
//	migrate -dialect=sqlite   -dsn='file:app.db?_pragma=journal_mode(WAL)' downto 3
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "modernc.org/sqlite"

	"github.com/kartaladev/wrkflw/persistence"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

// run parses args, builds the matching Migrator, executes the subcommand, and
// returns a process exit code (0 ok, 1 runtime error, 2 usage error).
func run(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(out)
	dialect := fs.String("dialect", "", "postgres | mysql | sqlite")
	dsn := fs.String("dsn", "", "database connection string")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	sub := fs.Arg(0)
	if *dialect == "" || *dsn == "" || sub == "" {
		writef(out, "usage: migrate -dialect=<d> -dsn=<dsn> <up|upto <v>|down|downto <v>|status|version>\n")
		return 2
	}

	ctx := context.Background()
	m, closeFn, err := openMigrator(ctx, *dialect, *dsn)
	if err != nil {
		writef(out, "error: %v\n", err)
		return 1
	}
	defer closeFn()

	if err := execCmd(ctx, m, sub, fs.Args()[1:], out); err != nil {
		writef(out, "error: %v\n", err)
		if err == errUsage { //nolint:errorlint // sentinel identity comparison is intentional
			return 2
		}
		return 1
	}
	return 0
}

// writef writes a formatted message to out; write errors are silently ignored
// because stdout/buffer write failures are unrecoverable in a CLI context.
func writef(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

var errUsage = fmt.Errorf("usage error")

// openMigrator opens the dialect-appropriate connection and wraps it in a
// persistence.Migrator. Each branch constructs the matching Migrator over the
// consumer's OWN connection handle (a pgxpool for Postgres, a *sql.DB for
// MySQL/SQLite) and returns a close func so the caller owns the connection
// lifecycle — the engine never opens or owns the database. The returned Migrator
// is dialect-agnostic; execCmd drives it identically regardless of backend.
func openMigrator(ctx context.Context, dialect, dsn string) (persistence.Migrator, func(), error) {
	switch dialect {
	case "postgres":
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return nil, func() {}, err
		}
		m, err := persistence.NewPostgresMigrator(pool)
		if err != nil {
			pool.Close()
			return nil, func() {}, err
		}
		return m, pool.Close, nil
	case "mysql":
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, func() {}, err
		}
		m, err := persistence.NewMySQLMigrator(db)
		if err != nil {
			_ = db.Close()
			return nil, func() {}, err
		}
		return m, func() { _ = db.Close() }, nil
	case "sqlite":
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, func() {}, err
		}
		db.SetMaxOpenConns(1) // SQLite is single-writer (ADR-0082)
		m, err := persistence.NewSQLiteMigrator(db)
		if err != nil {
			_ = db.Close()
			return nil, func() {}, err
		}
		return m, func() { _ = db.Close() }, nil
	default:
		return nil, func() {}, fmt.Errorf("unknown dialect %q", dialect)
	}
}

func execCmd(ctx context.Context, m persistence.Migrator, sub string, rest []string, out io.Writer) error {
	switch sub {
	case "up":
		return m.Up(ctx)
	case "down":
		return m.Down(ctx)
	case "upto", "downto":
		if len(rest) < 1 {
			writef(out, "%s requires a <version> argument\n", sub)
			return errUsage
		}
		v, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid version %q: %w", rest[0], err)
		}
		if sub == "upto" {
			return m.UpTo(ctx, v)
		}
		return m.DownTo(ctx, v)
	case "version":
		v, err := m.Version(ctx)
		if err != nil {
			return err
		}
		writef(out, "current version: %d\n", v)
		return nil
	case "status":
		rows, err := m.Status(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			state := "pending"
			if r.Applied {
				state = "applied"
			}
			writef(out, "%d\t%s\t%s\n", r.Version, state, r.Source)
		}
		return nil
	default:
		writef(out, "unknown subcommand %q\n", sub)
		return errUsage
	}
}

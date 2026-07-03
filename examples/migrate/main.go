// Command migrate is REFERENCE WIRING ONLY (not a shipped product binary) that
// demonstrates driving wrkflw schema migrations from a consumer's own CLI using
// the persistence.Migrator facade.
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

	"github.com/zakyalvan/krtlwrkflw/persistence"
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

func openMigrator(ctx context.Context, dialect, dsn string) (persistence.Migrator, func(), error) {
	switch dialect {
	case "postgres":
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			return nil, func() {}, err
		}
		m, err := persistence.NewPostgresMigrator(pool)
		return m, pool.Close, err
	case "mysql":
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, func() {}, err
		}
		m, err := persistence.NewMySQLMigrator(db)
		return m, func() { _ = db.Close() }, err
	case "sqlite":
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, func() {}, err
		}
		db.SetMaxOpenConns(1)
		m, err := persistence.NewSQLiteMigrator(db)
		return m, func() { _ = db.Close() }, err
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

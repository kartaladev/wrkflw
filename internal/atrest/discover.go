package atrest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
)

// ModuleRoot walks up from the current working directory to the nearest
// ancestor containing a go.mod file, and returns its absolute path. It is
// the anchor DiscoverMigrationDirs and LoadSchemas resolve their
// module-relative paths against, so this package works the same whether
// the caller runs from the repo root or from a subpackage's test binary
// working directory.
func ModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("workflow-atrest: get working directory: %w", err)
	}

	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("workflow-atrest: no go.mod found above %s", dir)
		}
		dir = parent
	}
}

// DiscoverMigrationDirs walks root and returns every directory that is
// named "migrations", or whose parent directory is named "migrations",
// and that directly contains at least one *.sql file — as slash-separated
// paths relative to root, sorted.
//
// This is deliberately a filesystem walk, never a hardcoded list: dialect
// cannot be inferred from a path (the casbin migration set lives at
// internal/authz/casbin/migrations, named for neither its dialect nor its
// engine), so discovery only finds directories. MigrationSets is the
// separate declaration of what each one IS.
//
// It fails closed (M1, final review) on a directory that carries *.sql
// files but sits deeper under a "migrations" ancestor than the two rules
// above reach — e.g. migrations/postgres/v2/*.sql. Silently discovering
// nothing there would make those migrations invisible to every downstream
// at-rest classification while SECURITY.md stays green.
func DiscoverMigrationDirs(root string) ([]string, error) {
	var dirs []string

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}

		base := entry.Name()
		parentBase := filepath.Base(filepath.Dir(path))
		matched := base == "migrations" || parentBase == "migrations"

		hasSQL, err := dirHasSQLFile(path)
		if err != nil {
			return err
		}
		if !hasSQL {
			return nil
		}

		if !matched {
			if isUnderMigrationsAncestor(root, path) {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return fmt.Errorf("workflow-atrest: relativize %s: %w", path, relErr)
				}
				return fmt.Errorf(
					"workflow-atrest: %s carries *.sql files under a \"migrations\" ancestor, but "+
						"is neither named \"migrations\" nor a direct child of one — "+
						"DiscoverMigrationDirs cannot see it",
					filepath.ToSlash(rel))
			}
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("workflow-atrest: relativize %s: %w", path, err)
		}
		dirs = append(dirs, filepath.ToSlash(rel))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("workflow-atrest: discover migration directories under %s: %w", root, err)
	}

	slices.Sort(dirs)

	return dirs, nil
}

// isUnderMigrationsAncestor reports whether any path segment strictly
// above dir's immediate parent (which the base/parentBase check at the
// call site already covers) is named "migrations" — i.e. dir sits two or
// more levels below a "migrations" directory.
func isUnderMigrationsAncestor(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}

	segments := strings.Split(filepath.ToSlash(rel), "/")
	if len(segments) <= 2 {
		return false
	}

	for _, seg := range segments[:len(segments)-2] {
		if seg == "migrations" {
			return true
		}
	}

	return false
}

// dirHasSQLFile reports whether dir directly contains at least one
// regular file with a ".sql" extension (not recursive).
func dirHasSQLFile(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("workflow-atrest: read directory %s: %w", dir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".sql") {
			return true, nil
		}
	}

	return false, nil
}

// MigrationSet declares which dialects a discovered migration directory's
// SQL applies to.
type MigrationSet struct {
	// Dialects lists the dialect names (as passed to ParseSQL) this
	// directory's migrations should be parsed as and merged into.
	Dialects []string
	// Note documents anything about the set that is not obvious from its
	// path alone (e.g. that it is conditionally present). Render publishes
	// this field VERBATIM into SECURITY.md's generated "Data at rest"
	// section (sourced, not retyped — ADR-0187's "one copy of the fact").
	// Treat it as consumer-facing prose: never write an internal
	// evidence-record id here (e.g. "(E3)", pointing at
	// docs/specs/2026-08-22-adr-0187-measurements.md) — a reader of a
	// public security document cannot resolve it. State the fact plainly
	// instead. TestMigrationSetNotesCarryNoInternalEvidenceLabel guards
	// this.
	Note string
}

// MigrationSets declares what each discovered migration directory IS.
// Discovery (DiscoverMigrationDirs) finds the directories; this map says
// which dialects each one applies to, because dialect CANNOT be inferred
// from the path — the casbin set is named "migrations", not "postgres".
//
// It is checked BOTH WAYS by LoadSchemas (and pinned by
// TestDiscoverMigrationDirs_FindsAllFourAndAllAreDeclared): a discovered
// directory absent here fails, and an entry here matching no discovered
// directory fails. Do not "simplify" this into a hardcoded walk — a
// hardcoded three-directory list is exactly what lost casbin_rule
// (ADR-0187 D1).
var MigrationSets = map[string]MigrationSet{
	"internal/persistence/store/migrations/postgres": {Dialects: []string{"postgres"}},
	"internal/persistence/store/migrations/mysql":    {Dialects: []string{"mysql"}},
	"internal/persistence/store/migrations/sqlite":   {Dialects: []string{"sqlite"}},
	"internal/authz/casbin/migrations": {
		Dialects: []string{"postgres"},
		// Presence is conditional on the MIGRATION having been applied, not on
		// which policy source is wired: casbinauthz.MigrateCasbin is an explicit
		// standalone call that is never auto-run, so a deployment that runs it and
		// then wires FromStrings/FromEnforcer still carries the table, while one
		// that wires FromDB without running it does not.
		Note: "it exists only in a Postgres deployment that has called " +
			"`casbinauthz.MigrateCasbin`, which is never run automatically — the `FromDB` " +
			"policy source requires that call, and any deployment that has made it keeps " +
			"the table whatever policy source it later wires",
	},
}

// LoadSchemas discovers every migration directory under root, validates it
// against MigrationSets in both directions (reconcileMigrationSets), parses
// each set's SQL files into the dialect(s) it declares, and returns the
// merged per-dialect Schema.
//
// MySQL's journal payload column is stored as "trigger_" ("trigger" is a
// MySQL reserved word — see internal/persistence/dialect.mysql); LoadSchemas
// normalizes it to the canonical "trigger" name (D2b) before returning, so
// every downstream consumer sees one column identity across dialects.
func LoadSchemas(root string) (map[string]Schema, error) {
	dirs, err := DiscoverMigrationDirs(root)
	if err != nil {
		return nil, err
	}

	if err := reconcileMigrationSets(dirs); err != nil {
		return nil, err
	}

	schemas := make(map[string]Schema)

	for _, dir := range dirs {
		set := MigrationSets[dir]

		sqlFiles, err := sqlFilesIn(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			return nil, err
		}

		for _, dialectName := range set.Dialects {
			schema, ok := schemas[dialectName]
			if !ok {
				schema = Schema{Dialect: dialectName, Columns: make(map[ColumnKey]Column)}
			}

			if err := mergeSQLFilesInto(schema, dialectName, sqlFiles); err != nil {
				return nil, err
			}

			schemas[dialectName] = schema
		}
	}

	normalizeMySQLJournalTrigger(schemas)

	return schemas, nil
}

// reconcileMigrationSets checks discovered against MigrationSets in both
// directions: a discovered directory absent from MigrationSets fails
// (its columns would otherwise never be classified), and a MigrationSets
// entry matching no discovered directory fails (a stale entry hides the
// next undeclared migration set beneath it — ADR-0187 D1).
func reconcileMigrationSets(discovered []string) error {
	for _, dir := range discovered {
		if _, ok := MigrationSets[dir]; !ok {
			return fmt.Errorf("workflow-atrest: discovered migration directory %q has no MigrationSets entry", dir)
		}
	}

	// Collect every stale entry and sort before reporting (M5, final
	// review): ranging MigrationSets and returning on the first stale
	// entry it happened to visit was nondeterministic under Go's
	// randomised map iteration, and with two or more stale entries the
	// one actually reported varied run to run.
	var stale []string
	for declared := range MigrationSets {
		if !slices.Contains(discovered, declared) {
			stale = append(stale, declared)
		}
	}
	if len(stale) > 0 {
		slices.Sort(stale)
		return fmt.Errorf("workflow-atrest: MigrationSets entries matching no discovered migration directory: %q", stale)
	}

	return nil
}

// sqlFilesIn returns the *.sql files directly inside dir, as absolute
// paths, sorted.
func sqlFilesIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("workflow-atrest: read directory %s: %w", dir, err)
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".sql") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	slices.Sort(files)

	return files, nil
}

// mergeSQLFilesInto reads and parses each of sqlFiles as dialectName and
// merges the resulting columns into schema.Columns.
func mergeSQLFilesInto(schema Schema, dialectName string, sqlFiles []string) error {
	for _, sqlFile := range sqlFiles {
		content, err := os.ReadFile(sqlFile) //nolint:gosec // G304: sqlFile is not attacker input — it is a *.sql entry enumerated by sqlFilesIn from a directory DiscoverMigrationDirs itself walked under ModuleRoot() and reconciled against MigrationSets; this package reads the repo's own migration files at build/generation time.
		if err != nil {
			return fmt.Errorf("workflow-atrest: read migration file %s: %w", sqlFile, err)
		}

		parsed, err := ParseSQL(dialectName, string(content))
		if err != nil {
			return fmt.Errorf("workflow-atrest: parse %s: %w", sqlFile, err)
		}

		for key, col := range parsed.Columns {
			schema.Columns[key] = col
		}
	}

	return nil
}

// normalizeMySQLJournalTrigger re-keys the MySQL-specific
// wrkflw_journal.trigger_ column under the canonical wrkflw_journal.trigger
// name shared by Postgres and SQLite (D2b).
//
// ⚠ Only the map KEY is canonicalized; Column.Name deliberately keeps the name
// MySQL's migration actually DECLARES. The canonical key is what cross-dialect
// set operations (the 6a key-set identity guard, the classification's coverage
// guard, Render's per-dialect row lookup) need; the declared name is what a DBA
// writing a MySQL migration needs, and Render publishes it. Overwriting Name
// with the canonical value is how the generated table came to publish "trigger"
// under a "mysql type" heading for a column MySQL rejects under that name.
//
// It is an EXACT (table, column) key match, sourced from
// dialect.NewMySQL().JournalTriggerColumn() and
// dialect.NewPostgres().JournalTriggerColumn() — the same source
// normalizeMySQLTriggerColumn uses in
// internal/persistence/store/migration_parity_test.go — so it can never
// widen into a prefix/suffix rule that would also rewrite
// wrkflw_timers.trigger_kind or wrkflw_timers.trigger_payload.
func normalizeMySQLJournalTrigger(schemas map[string]Schema) {
	mysqlSchema, ok := schemas["mysql"]
	if !ok {
		return
	}

	mysqlCol := dialect.NewMySQL().JournalTriggerColumn()
	canonicalCol := dialect.NewPostgres().JournalTriggerColumn()

	key := ColumnKey{Table: "wrkflw_journal", Column: mysqlCol}
	col, exists := mysqlSchema.Columns[key]
	if !exists {
		return
	}

	delete(mysqlSchema.Columns, key)
	mysqlSchema.Columns[ColumnKey{Table: "wrkflw_journal", Column: canonicalCol}] = col
}

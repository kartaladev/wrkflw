// Package atrest reads the repository's SQL migration files into a
// per-dialect Schema: which tables and columns exist, their declared
// types, and which are keyed (primary key, unique constraint, or
// indexed). Downstream tooling (discovery, classification, the security
// document generator) consumes the Schema this package produces.
package atrest

import (
	"fmt"
	"slices"
	"strings"
)

// Class names the sensitivity classification assigned to a column by
// downstream tooling. It is declared here because Column and Schema are
// the shared vocabulary the rest of the at-rest posture pipeline builds
// on; ParseSQL itself never assigns a Class.
type Class string

// ColumnKey identifies a single column within a Schema. A bare column
// name is not enough: exactly one column name in the schema carries two
// different classes — wrkflw_human_task.claimed_by is a human principal
// while wrkflw_call_links.claimed_by is a worker lease owner — so a
// map[string]Column would merge them into one entry.
type ColumnKey struct {
	Table  string
	Column string
}

// Column describes one column as declared in a CREATE TABLE statement.
type Column struct {
	Table string
	Name  string
	Type  string
	// Keys records the ways this column participates in a key or index,
	// e.g. "PK", "UNIQUE", "index", "index-predicate".
	Keys []string
}

// Schema is the per-dialect result of parsing a migration file's DDL.
type Schema struct {
	Dialect string
	Columns map[ColumnKey]Column
}

// Tables returns the sorted, de-duplicated set of table names present
// in the schema.
func (s Schema) Tables() []string {
	seen := make(map[string]struct{})
	for key := range s.Columns {
		seen[key.Table] = struct{}{}
	}

	tables := make([]string, 0, len(seen))
	for table := range seen {
		tables = append(tables, table)
	}
	slices.Sort(tables)

	return tables
}

// ColumnKeysWithPrefix returns the sorted ColumnKeys of s whose table name
// carries prefix (strings.HasPrefix on ColumnKey.Table). It is exported
// and takes its inputs as parameters — not a closure over package state —
// so both the production dialect-invariance assertion
// (TestNormalizedKeySetAgreesAcrossDialects) and its liveness guard
// (TestKeySetMatcherFires) drive the exact same matcher; a guard
// exercising a different function than the one that ships proves nothing
// about it.
func ColumnKeysWithPrefix(s Schema, prefix string) []ColumnKey {
	var keys []ColumnKey
	for key := range s.Columns {
		if strings.HasPrefix(key.Table, prefix) {
			keys = append(keys, key)
		}
	}

	slices.SortFunc(keys, func(a, b ColumnKey) int {
		if c := strings.Compare(a.Table, b.Table); c != 0 {
			return c
		}
		return strings.Compare(a.Column, b.Column)
	})

	return keys
}

// gooseDownDirective marks the start of a migration's rollback script.
// Everything from this line onward is not part of the schema the
// migration produces and must not be parsed as one: Up/Down are the only
// goose directives present in this repo's migration files.
const gooseDownDirective = "-- +goose Down"

// ParseSQL reads sqlText — the body of one goose migration file — and
// returns the Schema it declares for the given dialect. Only the
// "-- +goose Up" portion is parsed; everything from "-- +goose Down"
// onward is the rollback script and is discarded before parsing begins.
func ParseSQL(dialect, sqlText string) (Schema, error) {
	schema := Schema{
		Dialect: dialect,
		Columns: make(map[ColumnKey]Column),
	}

	upOnly := truncateAtGooseDown(sqlText)
	stripped := stripLineComments(upOnly)

	for _, stmt := range splitStatements(stripped) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		switch {
		case hasKeywordPrefix(stmt, "CREATE", "TABLE"):
			if err := parseCreateTable(stmt, schema.Columns); err != nil {
				return Schema{}, fmt.Errorf("workflow-atrest: parse CREATE TABLE: %w", err)
			}
		case hasKeywordPrefix(stmt, "CREATE", "INDEX"):
			if err := applyCreateIndex(stmt, schema.Columns, false); err != nil {
				return Schema{}, fmt.Errorf("workflow-atrest: parse CREATE INDEX: %w", err)
			}
		case hasKeywordPrefix(stmt, "CREATE", "UNIQUE", "INDEX"):
			if err := applyCreateIndex(stmt, schema.Columns, true); err != nil {
				return Schema{}, fmt.Errorf("workflow-atrest: parse CREATE UNIQUE INDEX: %w", err)
			}
		default:
			// Fail closed: a migration statement this parser does not
			// recognise (e.g. a future ALTER TABLE) must never be silently
			// skipped — a skip means its columns are invisible to every
			// downstream at-rest classification and the generated security
			// document stays green while the schema actually changed
			// underneath it.
			return Schema{}, fmt.Errorf("workflow-atrest: unrecognised statement: %s", statementSummary(stmt))
		}
	}

	return schema, nil
}

// truncateAtGooseDown returns sqlText up to (but not including) the
// first line that is exactly the goose Down directive.
func truncateAtGooseDown(sqlText string) string {
	lines := strings.Split(sqlText, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == gooseDownDirective {
			return strings.Join(lines[:i], "\n")
		}
	}
	return sqlText
}

// stripLineComments removes "--" comments to end-of-line, and ONLY where the
// "--" is not inside a quoted string or a quoted identifier.
//
// The naive strings.Index(line, "--") it replaces cut at the first "--"
// anywhere in the line: a column declared as `a TEXT DEFAULT '--x', b TEXT,`
// lost everything after the literal's first dash, so `b` — and every further
// column sharing that line — vanished from the schema with no error. In a
// document whose whole purpose is enumerating stored columns, a silently
// dropped column is the worst possible failure.
//
// The scan is over the whole text, not per line, so a literal spanning a
// newline keeps its state. A doubled quote (” inside a literal) needs no
// special case: the pair closes and immediately reopens the literal, which
// leaves the state correct either way.
func stripLineComments(sqlText string) string {
	var b strings.Builder
	b.Grow(len(sqlText))

	var inSingle, inDouble bool
	for i := 0; i < len(sqlText); i++ {
		c := sqlText[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			}
		case c == '\'':
			inSingle = true
		case c == '"':
			inDouble = true
		case c == '-' && i+1 < len(sqlText) && sqlText[i+1] == '-':
			for i < len(sqlText) && sqlText[i] != '\n' {
				i++
			}
			if i < len(sqlText) {
				b.WriteByte('\n')
			}
			continue
		}
		b.WriteByte(c)
	}

	return b.String()
}

// splitStatements splits sqlText into individual statements on
// top-level ";" — i.e. it is depth-aware and never splits inside
// parentheses, so a multi-line statement stays one statement
// regardless of embedded newlines.
func splitStatements(sqlText string) []string {
	var (
		statements []string
		depth      int
		start      int
	)

	for i, r := range sqlText {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ';':
			if depth == 0 {
				statements = append(statements, sqlText[start:i])
				start = i + 1
			}
		}
	}

	if tail := strings.TrimSpace(sqlText[start:]); tail != "" {
		statements = append(statements, sqlText[start:])
	}

	return statements
}

// statementSummary collapses stmt's whitespace into single spaces and
// truncates it to a reasonable length for an error message, so a large
// multi-line CREATE TABLE-shaped statement that still failed to parse
// doesn't dump its entire body into the error.
func statementSummary(stmt string) string {
	collapsed := strings.Join(strings.Fields(stmt), " ")
	const maxLen = 120
	if len(collapsed) > maxLen {
		return collapsed[:maxLen] + "…"
	}
	return collapsed
}

// hasKeywordPrefix reports whether stmt (after collapsing whitespace)
// begins with the given keywords in order, case-insensitively.
func hasKeywordPrefix(stmt string, keywords ...string) bool {
	fields := strings.Fields(stmt)
	if len(fields) < len(keywords) {
		return false
	}
	for i, kw := range keywords {
		if !strings.EqualFold(fields[i], kw) {
			return false
		}
	}
	return true
}

// tableLevelClauseKeywords enumerates every leading keyword that can begin a
// CREATE TABLE body clause which is NOT a column declaration. It is the single
// enumeration the classification switch in applyTableLevelKeyClause routes on;
// anything else is a column declaration by construction, which is why that
// function needs no "unrecognised clause" error (the one it used to carry was
// unreachable: only these keywords could ever reach it).
var tableLevelClauseKeywords = map[string]bool{
	"PRIMARY": true, "UNIQUE": true, "FOREIGN": true, "CONSTRAINT": true,
	"CHECK": true, "KEY": true, "INDEX": true, "EXCLUDE": true,
}

// keywordsThatAreAlsoLegalColumnNames are the clause keywords that are legal
// UNQUOTED column identifiers in at least one supported engine: "key" and
// "index" are non-reserved in Postgres, and "exclude" is non-reserved there
// too. For these — and only these — a leading keyword is not on its own proof
// of a table-level clause, so the shape is checked as well (see
// clauseNamesOnlyKnownColumns). The others (PRIMARY, UNIQUE, FOREIGN,
// CONSTRAINT, CHECK) are reserved in Postgres and MySQL alike and can only
// appear as a column name when quoted, in which case the quotes keep the token
// from matching at all.
var keywordsThatAreAlsoLegalColumnNames = map[string]bool{
	"KEY": true, "INDEX": true, "EXCLUDE": true,
}

// spaceParens returns s with every paren surrounded by spaces, so that
// whitespace tokenization sees "UNIQUE(g)" as "UNIQUE ( g )" and
// "PRIMARY KEY(g)" as "PRIMARY KEY ( g )". SQL does not require a space
// before a paren, and three separate matchers in this file used to assume one:
// that disagreement is why a named CONSTRAINT with a glued paren hard-failed
// the whole schema load while its unnamed twin parsed.
func spaceParens(s string) string {
	return strings.NewReplacer("(", " ( ", ")", " ) ").Replace(s)
}

// clauseLeadKeyword returns clause's first token, upper-cased, with a paren
// treated as a token boundary.
func clauseLeadKeyword(clause string) string {
	fields := strings.Fields(spaceParens(clause))
	if len(fields) == 0 {
		return ""
	}
	return strings.ToUpper(fields[0])
}

// clauseNamesOnlyKnownColumns reports whether clause carries a non-empty first
// paren group naming only columns already declared for table. It is the shape
// test that separates the table-level clause "KEY idx_t_h (h)" — whose paren
// group names a real column — from the column declaration "key VARCHAR(255)",
// whose paren group holds a type's length.
func clauseNamesOnlyKnownColumns(clause, table string, columns map[ColumnKey]Column) bool {
	names := columnNamesInParens(clause)
	if len(names) == 0 {
		return false
	}
	for _, name := range names {
		if _, ok := columns[ColumnKey{Table: table, Column: name}]; !ok {
			return false
		}
	}
	return true
}

// splitTopLevel splits s on sep, but only at paren-depth zero, so that
// a value such as strftime('%Y-%m-%dT%H:%M:%SZ', 'now') is never split
// on its internal comma.
func splitTopLevel(s string, sep rune) []string {
	var (
		parts []string
		depth int
		start int
	)

	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case sep:
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])

	return parts
}

// tableNameFromCreate extracts the table name from the header portion
// of a CREATE TABLE statement (everything before the opening paren),
// e.g. "CREATE TABLE t_one " -> "t_one".
func tableNameFromCreate(header string) string {
	fields := strings.Fields(header)
	for i := 0; i < len(fields); i++ {
		if strings.EqualFold(fields[i], "TABLE") && i+1 < len(fields) {
			for _, f := range fields[i+1:] {
				if strings.EqualFold(f, "IF") || strings.EqualFold(f, "NOT") || strings.EqualFold(f, "EXISTS") {
					continue
				}
				return f
			}
		}
	}
	return ""
}

// columnTypeStopWords are the constraint-clause keywords that mark the end
// of a column's type within a column declaration's whitespace-split
// tokens, e.g. "TEXT NOT NULL" or "NUMERIC(10,2) DEFAULT 0". columnType
// stops at the first one of these.
var columnTypeStopWords = map[string]bool{
	"NOT": true, "NULL": true, "PRIMARY": true, "UNIQUE": true,
	"DEFAULT": true, "REFERENCES": true, "CHECK": true, "GENERATED": true,
	"COLLATE": true, "COMMENT": true,
}

// columnType returns the declared type from fields — a column
// declaration's whitespace-split tokens, with fields[0] being the column
// name — as every token from fields[1] up to (not including) the first
// columnTypeStopWords token, space-joined. fields[1]
// alone truncates a multi-word type at its first space: "DOUBLE
// PRECISION" -> "DOUBLE", "CHARACTER VARYING(255)" -> "CHARACTER",
// "TIMESTAMP WITH TIME ZONE" -> "TIMESTAMP", "BIGINT UNSIGNED" ->
// "BIGINT" — all different, real storage types, and publishing the wrong
// one in a security document is a false statement.
func columnType(fields []string) string {
	if len(fields) < 2 {
		return ""
	}

	var typeTokens []string
	for _, f := range fields[1:] {
		if columnTypeStopWords[strings.ToUpper(f)] {
			break
		}
		typeTokens = append(typeTokens, f)
	}

	return strings.Join(typeTokens, " ")
}

// parseCreateTable parses a single "CREATE TABLE <name> ( ... )"
// statement and records its columns into columns.
func parseCreateTable(stmt string, columns map[ColumnKey]Column) error {
	open := strings.Index(stmt, "(")
	if open == -1 {
		return fmt.Errorf("no opening paren in CREATE TABLE statement")
	}

	header := stmt[:open]
	table := tableNameFromCreate(header)
	if table == "" {
		return fmt.Errorf("could not read table name from %q", header)
	}

	// The body ends at the paren MATCHING the opening one, never at the last
	// ")" in the statement: with a trailing table option such as
	// "WITH (fillfactor=70)", LastIndex swallowed the option into the body and
	// published "TEXT) WITH (fillfactor=70" as a column's storage type.
	closeIdx := matchingParen(stmt, open)
	if closeIdx == -1 {
		return fmt.Errorf("unbalanced column list in CREATE TABLE %s", table)
	}

	// Fail closed on anything after the column list. Postgres's
	// INHERITS (parent) is a trailing clause that ADDS COLUMNS, so silently
	// ignoring the remainder could under-report the census of a security
	// document. No migration in this module carries a trailing clause today
	// (verified: all four files parse), so the strictness costs nothing now and
	// turns the next one from silent into loud.
	if remainder := strings.TrimSpace(stmt[closeIdx+1:]); remainder != "" {
		return fmt.Errorf("unparsed remainder after the column list of CREATE TABLE %s: %s",
			table, statementSummary(remainder))
	}

	body := stmt[open+1 : closeIdx]

	for _, clause := range splitTopLevel(body, ',') {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}

		fields := strings.Fields(clause)
		if len(fields) == 0 {
			continue
		}

		// The clause is a table-level constraint/key/index, or it is a column
		// declaration — applyTableLevelKeyClause is the single place that
		// decides which, and reports it through handled.
		handled, err := applyTableLevelKeyClause(clause, table, columns)
		if err != nil {
			return err
		}
		if handled {
			continue
		}

		name := fields[0]
		colType := columnType(fields)

		key := ColumnKey{Table: table, Column: name}
		col := Column{
			Table: table,
			Name:  name,
			Type:  colType,
		}
		spaced := spaceParens(clause)
		if hasKeywordFold(spaced, "UNIQUE") {
			col.Keys = appendKeyOnce(col.Keys, "UNIQUE")
		}
		// Inline column-level PRIMARY KEY, e.g. "id BIGINT PRIMARY KEY" —
		// distinct from the table-level "PRIMARY KEY (a, b)" clause handled in
		// applyTableLevelKeyClause. Both shapes appear in this repo's
		// migrations. The inline set is CLOSED and pinned by
		// TestInlinePrimaryKeySitesAreTheDeclaredSet rather than counted in
		// prose: casbin_rule.id, wrkflw_instances.instance_id, wrkflw_outbox.id
		// and wrkflw_call_links.child_instance_id in every dialect that
		// declares them, plus wrkflw_human_task.task_id in SQLite ONLY
		// (Postgres and MySQL spell that one as a table-level clause). This
		// comment said "three" and the design said "four"; grep says five.
		if hasKeywordFold(spaced, "PRIMARY") && hasKeywordFold(spaced, "KEY") {
			col.Keys = appendKeyOnce(col.Keys, "PK")
		}
		columns[key] = col
	}

	return nil
}

// applyTableLevelKeyClause decides whether clause is a table-level
// constraint/key/index rather than a column declaration, and if it is,
// annotates the columns it references. handled is false — with no error — when
// clause is a column declaration, which is the single place that decision is
// made.
//
// The shapes it recognises: PRIMARY KEY (a, b); a table-level UNIQUE (a, b);
// MySQL's INDEX name (col) / KEY name (col); FOREIGN KEY (…); CHECK (…);
// Postgres's EXCLUDE …; and a named CONSTRAINT wrapping any of them,
// re-dispatched onto the same cases. FOREIGN, CHECK and EXCLUDE are recognised
// (so they are never mistaken for a column) but deliberately produce NO key
// annotation — the FOREIGN KEY exclusion is deliberate and measured: the
// schema's only foreign key is wrkflw_journal.instance_id, already keyed
// through the table-level PRIMARY KEY, so excluding foreign keys changes no row
// of today's output and is a decision rather than an accident.
//
// It fails closed when a clause that can only be a table-level
// constraint names a column this table has not declared: silently deriving no
// key there is how an index-shaped fact disappears from a document whose whole
// subject is which columns are keyed.
func applyTableLevelKeyClause(clause, table string, columns map[ColumnKey]Column) (handled bool, err error) {
	keyword := clauseLeadKeyword(clause)
	if !tableLevelClauseKeywords[keyword] {
		return false, nil
	}

	if !clauseNamesOnlyKnownColumns(clause, table, columns) {
		// "key VARCHAR(255) NOT NULL" is a column named key, not a MySQL KEY
		// clause: its paren group holds a type length, not column names.
		if keywordsThatAreAlsoLegalColumnNames[keyword] {
			return false, nil
		}
		return false, fmt.Errorf(
			"table-level %s clause in %s names no column declared by that table: %s",
			keyword, table, statementSummary(clause))
	}

	switch keyword {
	case "PRIMARY":
		markClauseColumns(clause, table, columns, "PK")
	case "UNIQUE":
		markClauseColumns(clause, table, columns, "UNIQUE")
	case "INDEX", "KEY":
		markClauseColumns(clause, table, columns, "index")
	case "FOREIGN", "CHECK", "EXCLUDE":
		// Recognised, deliberately keyless — see the doc comment.
	case "CONSTRAINT":
		return applyNamedConstraintClause(clause, table, columns)
	}

	return true, nil
}

// applyNamedConstraintClause handles a "CONSTRAINT <name> <shape>" clause by
// re-dispatching onto the shape it names — the same shapes
// applyTableLevelKeyClause handles unnamed. Keyword detection runs over the
// paren-spaced form, so "CONSTRAINT pk_g PRIMARY KEY(g)" is classified exactly
// like "CONSTRAINT pk_g PRIMARY KEY (g)"; the two disagreed before, and the
// glued form hard-failed the entire schema load.
//
// A named CONSTRAINT carrying none of the known shapes is an error: an
// unclassifiable constraint must fail the build rather than silently derive no
// key.
func applyNamedConstraintClause(clause, table string, columns map[ColumnKey]Column) (handled bool, err error) {
	spaced := spaceParens(clause)

	switch {
	case hasKeywordFold(spaced, "FOREIGN"), hasKeywordFold(spaced, "CHECK"), hasKeywordFold(spaced, "EXCLUDE"):
		return true, nil
	case hasKeywordFold(spaced, "PRIMARY") && hasKeywordFold(spaced, "KEY"):
		markClauseColumns(clause, table, columns, "PK")
		return true, nil
	case hasKeywordFold(spaced, "UNIQUE"):
		markClauseColumns(clause, table, columns, "UNIQUE")
		return true, nil
	}

	return false, fmt.Errorf("unrecognised CONSTRAINT clause: %s", statementSummary(clause))
}

// markClauseColumns annotates every column named in clause's first paren group
// with keyName.
func markClauseColumns(clause, table string, columns map[ColumnKey]Column, keyName string) {
	for _, name := range columnNamesInParens(clause) {
		markKey(columns, table, name, keyName)
	}
}

// markKey adds keyName to the Keys of the (table, column) entry if it
// already exists in columns; a clause referencing a column that was
// never declared is ignored (malformed input, not this package's job
// to validate).
func markKey(columns map[ColumnKey]Column, table, column, keyName string) {
	key := ColumnKey{Table: table, Column: column}
	col, ok := columns[key]
	if !ok {
		return
	}
	col.Keys = appendKeyOnce(col.Keys, keyName)
	columns[key] = col
}

// applyCreateIndex parses a "CREATE [UNIQUE] INDEX <name> ON <table>
// [USING <method>] (<cols>) [WHERE <predicate>]" statement and annotates the
// indexed (and, for a partial index, predicate) columns. unique reports whether
// the statement was CREATE UNIQUE INDEX; a unique index records both "UNIQUE"
// and "index", because uniqueness is the fact that tells a consumer
// deterministic ciphertext leaks duplicate detection, and recording only
// "index" dropped it.
//
// It is line-agnostic: the statement was already assembled by the depth-aware
// splitStatements, so a multi-line index definition or one with several spaces
// before ON is handled the same as a single-line one. Keyword offsets are found
// on the ORIGINAL statement (indexOfKeywordFold), never on an upper-cased copy:
// strings.ToUpper is not length-preserving in UTF-8, so a single non-ASCII rune
// before ON or WHERE used to skew every later slice and silently lose the index.
//
// It fails closed on a
// malformed statement it cannot decompose — no ON clause, no column list, an
// unbalanced one, or an unrecognised clause between the table name and the
// column list — instead of returning silently. A missing WHERE clause is NOT
// malformed (most CREATE INDEX statements have none) and remains a normal,
// non-error return.
func applyCreateIndex(stmt string, columns map[ColumnKey]Column, unique bool) error {
	onIdx := indexOfKeywordFold(stmt, "ON")
	if onIdx == -1 {
		return fmt.Errorf("CREATE INDEX statement has no ON clause: %s", statementSummary(stmt))
	}

	rest := stmt[onIdx+len("ON"):]

	open := strings.Index(rest, "(")
	if open == -1 {
		return fmt.Errorf("CREATE INDEX statement has no column list: %s", statementSummary(stmt))
	}

	table, err := indexTargetTable(rest[:open])
	if err != nil {
		return fmt.Errorf("%w: %s", err, statementSummary(stmt))
	}

	closeIdx := matchingParen(rest, open)
	if closeIdx == -1 {
		return fmt.Errorf("CREATE INDEX statement has an unbalanced column list: %s", statementSummary(stmt))
	}
	colsPart := rest[open+1 : closeIdx]

	for _, name := range columnNamesInParens("(" + colsPart + ")") {
		if unique {
			markKey(columns, table, name, "UNIQUE")
		}
		markKey(columns, table, name, "index")
	}

	// A partial index's WHERE predicate is read by the planner, so any
	// *known column of this table* referenced in the predicate is
	// annotated "index-predicate" — not only the indexed column(s):
	// t_pending_idx indexes "a" but filters WHERE b IN (...), so "b"
	// (not "a") is the one that needs the annotation.
	//
	// String literals are removed first: WHERE status = 'error' must not
	// annotate a column named "error", and postgres/0001_init.sql already
	// carries WHERE status IN ('completed','failed') on tables that declare
	// error/output/outcome. A double-quoted identifier is deliberately left
	// alone — it IS a column reference, and identifiersIn tokenizes it.
	afterCols := rest[closeIdx:]
	whereIdx := indexOfKeywordFold(afterCols, "WHERE")
	if whereIdx == -1 {
		return nil
	}
	predicate := stripStringLiterals(afterCols[whereIdx:])
	for _, token := range identifiersIn(predicate) {
		if _, ok := columns[ColumnKey{Table: table, Column: token}]; ok {
			markKey(columns, table, token, "index-predicate")
		}
	}

	return nil
}

// indexTargetTable reads the target table from the span between a CREATE
// INDEX's ON keyword and its column list, and fails closed on anything it
// cannot account for.
//
// Taking the whole span as the table name — which is what this replaces — meant
// "ON t USING gin (col)" resolved to the table "t USING gin", matched nothing,
// and dropped the index with no diagnostic. GIN and GiST are the normal way to
// index this schema's JSONB columns, so that silence sat directly on the path
// this document publishes. A schema qualifier ("public.t") is stripped: the
// parsed schema keys tables by bare name.
func indexTargetTable(span string) (string, error) {
	fields := strings.Fields(span)
	if len(fields) == 0 {
		return "", fmt.Errorf("CREATE INDEX statement names no table after ON")
	}

	table := fields[0]
	if dot := strings.LastIndex(table, "."); dot != -1 {
		table = table[dot+1:]
	}

	remainder := fields[1:]
	if len(remainder) == 2 && strings.EqualFold(remainder[0], "USING") {
		remainder = nil
	}
	if len(remainder) > 0 {
		return "", fmt.Errorf(
			"unrecognised clause %q between the CREATE INDEX table name and its column list",
			strings.Join(remainder, " "))
	}

	return table, nil
}

// stripStringLiterals blanks out every single-quoted string literal (quotes
// included) so a coarse identifier scan cannot mistake a literal's contents for
// a column name. Length is preserved — every removed byte becomes a space — so
// the result stays offset-comparable with its input.
func stripStringLiterals(s string) string {
	var b strings.Builder
	b.Grow(len(s))

	inLiteral := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\'':
			inLiteral = !inLiteral
			b.WriteByte(' ')
		case inLiteral:
			b.WriteByte(' ')
		default:
			b.WriteByte(s[i])
		}
	}

	return b.String()
}

// identifiersIn returns every maximal run of identifier runes
// (ASCII letters, digits, underscore) in s. It is a coarse tokenizer
// used to find which of a table's known columns a WHERE predicate
// mentions; it will also emit non-column tokens (keywords,
// string-literal contents), which the caller filters out by checking
// column membership. It operates on runes, not on a byte(r) truncation
// of them — SQL identifiers are single-byte ASCII, but a multi-byte
// rune (e.g. U+0161 'š') truncates to a byte that can alias an ASCII
// identifier byte, which would wrongly treat the rune as an identifier
// character instead of a token boundary.
func identifiersIn(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !isIdentRune(r)
	})
}

// isIdentRune reports whether r is a byte-range identifier
// character (ASCII letter, digit, or underscore). Any rune outside
// that range — including every multi-byte rune — is not an
// identifier character.
func isIdentRune(r rune) bool {
	return r == '_' ||
		(r >= 'A' && r <= 'Z') ||
		(r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9')
}

// indexOfKeywordFold finds the byte offset of keyword as a standalone word in
// haystack, case-insensitively, or -1.
//
// It scans the ORIGINAL string. Its predecessor searched an upper-cased copy
// and applied the offset to the original, which is only sound while
// strings.ToUpper preserves length — and it does not: U+0131 'ı' is two bytes
// and upper-cases to the one-byte 'I', so a single such rune anywhere before
// the keyword shifted every later slice and silently lost the index.
func indexOfKeywordFold(haystack, keyword string) int {
	n := len(keyword)
	for i := 0; i+n <= len(haystack); i++ {
		if !strings.EqualFold(haystack[i:i+n], keyword) {
			continue
		}
		before := i == 0 || !isIdentByte(haystack[i-1])
		afterPos := i + n
		after := afterPos >= len(haystack) || !isIdentByte(haystack[afterPos])
		if before && after {
			return i
		}
	}

	return -1
}

func isIdentByte(b byte) bool {
	return b == '_' ||
		(b >= 'A' && b <= 'Z') ||
		(b >= 'a' && b <= 'z') ||
		(b >= '0' && b <= '9')
}

// matchingParen returns the index (within s) of the ')' matching the
// '(' at s[open], or -1 if unbalanced.
func matchingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// columnNamesInParens extracts the top-level, comma-separated
// identifiers from the first "(...)" group found in s, e.g.
// "(a, b DESC)" -> ["a", "b"].
func columnNamesInParens(s string) []string {
	open := strings.Index(s, "(")
	if open == -1 {
		return nil
	}
	closeIdx := matchingParen(s, open)
	if closeIdx == -1 {
		return nil
	}
	inner := s[open+1 : closeIdx]

	var names []string
	for _, part := range splitTopLevel(inner, ',') {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		names = append(names, fields[0])
	}
	return names
}

// hasKeywordFold reports whether s contains keyword as a whole word,
// case-insensitively.
func hasKeywordFold(s, keyword string) bool {
	for _, f := range strings.Fields(s) {
		if strings.EqualFold(f, keyword) {
			return true
		}
	}
	return false
}

// appendKeyOnce appends keyName to keys unless it is already present.
func appendKeyOnce(keys []string, keyName string) []string {
	if slices.Contains(keys, keyName) {
		return keys
	}
	return append(keys, keyName)
}

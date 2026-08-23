package atrest

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// beginMarker and endMarker delimit the generated "Data at rest" section
// inside SECURITY.md. ReplaceBlock requires both to already be present in
// the document it splices into — it never invents them, so a SECURITY.md
// that has never been generated once must have them added by hand exactly
// once (ADR-0187 decision 9's golden-file "-update" flow starts from a
// document that already carries an empty marker pair).
const (
	beginMarker = "<!-- BEGIN at-rest (generated) -->"
	endMarker   = "<!-- END at-rest -->"
)

// ReplaceBlock returns current with the region from beginMarker through
// endMarker (inclusive of both marker lines) replaced by a canonical
// rendering of block: BEGIN marker, a blank line, block with any trailing
// newlines trimmed, a blank line, END marker. Everything before beginMarker
// and everything after endMarker is returned byte-for-byte unchanged.
//
// It is idempotent: applying it twice in a row with the same block
// produces the same string both times, because the canonical layout it
// writes is exactly the layout it looks for on the next call. That
// property is what lets scripts/gen-at-rest.sh write and then re-assert in
// a second process (Task 8) and what lets a repeated "-update" run settle
// rather than drift.
//
// It errors if current does not carry both markers, in that order —
// SECURITY.md must be seeded with an empty marker pair before this
// package's generator has anything to splice into.
func ReplaceBlock(current, block string) (string, error) {
	beginIdx := strings.Index(current, beginMarker)
	if beginIdx == -1 {
		return "", fmt.Errorf("workflow-atrest: current document is missing the %s BEGIN marker", quoteMarker(beginMarker))
	}

	afterBegin := beginIdx + len(beginMarker)
	relEndIdx := strings.Index(current[afterBegin:], endMarker)
	if relEndIdx == -1 {
		return "", fmt.Errorf("workflow-atrest: current document is missing the %s END marker (or it appears before BEGIN)", quoteMarker(endMarker))
	}
	endIdx := afterBegin + relEndIdx + len(endMarker)

	prefix := current[:beginIdx]
	suffix := current[endIdx:]
	trimmedBlock := strings.TrimSpace(block)

	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString(beginMarker)
	b.WriteString("\n\n")
	b.WriteString(trimmedBlock)
	b.WriteString("\n\n")
	b.WriteString(endMarker)
	b.WriteString(suffix)

	return b.String(), nil
}

// quoteMarker renders a marker constant for an error message without
// letting its literal HTML comment syntax read as if it were formatting
// the message itself.
func quoteMarker(marker string) string {
	return fmt.Sprintf("%q", marker)
}

// renderDialects is the fixed dialect column order the "Columns" table and
// the required-dialect check use — never derived from ranging a map.
var renderDialects = []string{"postgres", "mysql", "sqlite"}

// casbinMigrationsDir is the MigrationSets key for the casbin_rule migration
// set (see discover.go). It is a plain package-internal constant, not
// exported, because Render is the only reader that needs it by name.
const casbinMigrationsDir = "internal/authz/casbin/migrations"

// claimedByActorKey is the one column ADR-0187 D8's withdrawal names as the
// derived warning that survives: an actor-classed column that is indexed.
var claimedByActorKey = ColumnKey{Table: "wrkflw_human_task", Column: "claimed_by"}

// classDescription pairs a Class with the sentence Render's "Classes"
// section states for it. Order here is classOrder — the section's render
// order — never a map range.
type classDescription struct {
	class Class
	text  string
}

var classOrder = []classDescription{
	{ClassFreeform, "unstructured, application-authored content: JSON blobs, notes, error text, " +
		"snapshots. No shape is assumed; it may hold arbitrary consumer data, including PII the " +
		"consumer put there."},
	{ClassActor, "identifies a human principal. Treat as personal data."},
	{ClassPolicy, "authorization policy data at rest. Its compromise affects access-control " +
		"decisions across the deployment."},
	{ClassReference, "an identifier or foreign-key-shaped value: instance/definition/task ids, " +
		"topic names, dedup keys, a worker lease owner. Not a person."},
	{ClassScalar, "a small structured value: a status, a kind, a count, a version number. " +
		"Generally low sensitivity on its own."},
	{ClassTimestamp, "a point in time. Generally low sensitivity on its own, though timing can " +
		"support correlation with other data."},
}

// casbinRuleTable is the one table name both casbinPolicyColumns and
// casbinColumnsAreAbsentFromDialects key their derivation on.
const casbinRuleTable = "casbin_rule"

// wantCasbinPolicyColumns is the exact shape casbinPolicyColumns requires
// the derived casbin_rule policy-class column set to equal. It is the
// single point of comparison for the C1 fix (ADR-0187 final-review
// finding): render.go must never retype this list as a bare string
// literal inside the rendered sentence again — see
// TestRenderCasbinPolicyColumnsAreDerivedNotRetyped.
var wantCasbinPolicyColumns = []string{"ptype", "v0", "v1", "v2", "v3", "v4", "v5"}

// casbinPolicyColumns returns the sorted column names of casbinRuleTable in
// cls that carry ClassPolicy, deriving the "keyed is a lower bound"
// paragraph's `casbin_rule.{ptype,v0,...}` sentence from cls rather than a
// hardcoded literal (C1). If cls carries no casbinRuleTable entry at all
// (e.g. a synthetic fixture that classifies an unrelated schema — see
// TestRender's distinctness/warning fixtures), it returns (nil, nil) and
// Render skips the paragraph rather than asserting a fact about a table
// that isn't part of this call's input. If casbinRuleTable IS present, the
// derived class-policy set must equal exactly wantCasbinPolicyColumns or
// this errors — a classification change to casbin_rule's columns then
// fails the build instead of silently outdating the published sentence.
func casbinPolicyColumns(cls map[ColumnKey]Class) ([]string, error) {
	var cols []string
	casbinTablePresent := false
	for key, class := range cls {
		if key.Table != casbinRuleTable {
			continue
		}
		casbinTablePresent = true
		if class == ClassPolicy {
			cols = append(cols, key.Column)
		}
	}
	if !casbinTablePresent {
		return nil, nil
	}
	slices.Sort(cols)

	if !slices.Equal(cols, wantCasbinPolicyColumns) {
		return nil, fmt.Errorf(
			"workflow-atrest: render: casbin_rule policy-class columns are %v, want %v — "+
				"the \"keyed is a lower bound\" paragraph's RemovePolicy sentence would be stale",
			cols, wantCasbinPolicyColumns)
	}

	return cols, nil
}

// casbinColumnsAreAbsentFromDialects verifies that every casbin_rule column
// named in cls is absent from schemas under every name in dialects — the
// derivation behind the Columns-table legend's "n/a … (every casbin_rule
// column, in mysql and sqlite)" claim (C1). It errors, naming the column
// and dialect, the moment any casbin_rule column is found to actually exist
// in one of dialects — that claim must never be trusted as a static literal
// again.
func casbinColumnsAreAbsentFromDialects(schemas map[string]Schema, cls map[ColumnKey]Class, dialects []string) error {
	// Collect and sort every violation before reporting (the M5 treatment
	// reconcileMigrationSets already applies): returning on the first one Go's
	// randomised map iteration happened to visit made the error text vary run to
	// run whenever more than one column had drifted, and it hid the rest.
	var violations []string
	for key := range cls {
		if key.Table != casbinRuleTable {
			continue
		}
		for _, d := range dialects {
			schema, ok := schemas[d]
			if !ok {
				continue
			}
			if _, exists := schema.Columns[key]; exists {
				violations = append(violations, fmt.Sprintf("%s.%s in dialect %q", key.Table, key.Column, d))
			}
		}
	}
	if len(violations) == 0 {
		return nil
	}
	slices.Sort(violations)

	return fmt.Errorf(
		"workflow-atrest: render: %v exist(s) outside postgres — the legend's "+
			"\"n/a … (every casbin_rule column, in mysql and sqlite)\" claim is false",
		violations)
}

// englishNumber spells out small counts (as used in the "all seven" RemovePolicy
// sentence) so the word tracks len(casbinPolicyColumns(cls)) instead of being a
// second, independent literal that could drift from it. Falls back to the
// decimal digits for any count outside the spelled range — casbinPolicyColumns
// already guarantees the count Render passes here is exactly 7.
func englishNumber(n int) string {
	words := map[int]string{
		0: "zero", 1: "one", 2: "two", 3: "three", 4: "four",
		5: "five", 6: "six", 7: "seven", 8: "eight", 9: "nine",
	}
	if word, ok := words[n]; ok {
		return word
	}
	return strconv.Itoa(n)
}

// Render produces the "Data at rest" section body (ADR-0187 decision 9) from
// schemas (as returned by LoadSchemas) and cls (normally Classification). It
// contains no BEGIN/END markers — ReplaceBlock owns those.
//
// Render is deterministic (ADR-0187 D12): the table's rows are sorted by
// (table, column) and dialect columns are always emitted in renderDialects
// order, so two calls over the same input produce byte-identical output
// despite Go's randomised map iteration.
func Render(schemas map[string]Schema, cls map[ColumnKey]Class) (string, error) {
	for _, d := range renderDialects {
		if _, ok := schemas[d]; !ok {
			return "", fmt.Errorf("workflow-atrest: render: schemas is missing the required dialect %q", d)
		}
	}

	// Derive, never retype (C1, ADR-0187 final review): both of these back
	// factual claims the "### Columns" section publishes below about
	// casbin_rule specifically — which of its columns are class `policy`,
	// and that none of them exist outside postgres. A hardcoded literal
	// inside the rendered sentence is only ever compared against itself by
	// TestSecurityMdInSync; deriving it here means a classification or
	// schema change that falsifies the sentence fails the build instead.
	casbinCols, err := casbinPolicyColumns(cls)
	if err != nil {
		return "", err
	}
	if err := casbinColumnsAreAbsentFromDialects(schemas, cls, []string{"mysql", "sqlite"}); err != nil {
		return "", err
	}

	var b strings.Builder

	b.WriteString("## Data at rest\n\n")
	b.WriteString("Nothing `wrkflw` stores is encrypted, redacted, or tamper-evident. This section is " +
		"generated and machine-checked (ADR-0187) from the migrations embedded in this module — " +
		"consumer-supplied migrations are out of scope. If it disagrees with the schema, " +
		"`TestSecurityMdInSync` fails the build; edit `internal/atrest/render.go`, not this file, " +
		"and regenerate with `scripts/gen-at-rest.sh`.\n\n")
	b.WriteString("⚠ **This table describes a FRESH database.** goose keys migrations by version and " +
		"stores no checksum, so an in-place edit of an already-applied migration never re-applies to " +
		"an already-migrated deployment — a long-lived deployment can hold a schema this table does " +
		"not describe.\n\n")

	b.WriteString("### Classes\n\n")
	fmt.Fprintf(&b, "Every stored column carries one of %s classes, assigned by logical role rather "+
		"than physical type:\n\n", englishNumber(len(classOrder)))
	for _, cd := range classOrder {
		fmt.Fprintf(&b, "- **`%s`** — %s\n", cd.class, cd.text)
	}
	b.WriteString("\n")

	b.WriteString("### Columns\n\n")
	b.WriteString("`keyed` is a machine-derived, per-dialect **lower bound**: it records `PRIMARY " +
		"KEY`/`UNIQUE` membership and every `CREATE INDEX` (including a partial index's " +
		"`WHERE`-predicate columns) found in this module's migrations, however that key/index is " +
		"spelled — an inline column modifier, a table-level `PRIMARY KEY`/`UNIQUE`/`KEY`/`INDEX` " +
		"clause, a named `CONSTRAINT`, or a `CREATE UNIQUE INDEX` (which records both `UNIQUE` and " +
		"`index`). The parser fails the build on a statement it does not recognize at all, on a " +
		"table-level constraint naming a column its own `CREATE TABLE` never declared, and on any " +
		"clause it cannot account for between a `CREATE INDEX`'s table name and its column list. " +
		"One gap remains open by construction and derives no key while raising no error: a " +
		"`CREATE INDEX` whose target table or column is declared in a DIFFERENT migration file, " +
		"which no migration in this module does today. It is also " +
		"blind to any `WHERE`/`ORDER BY` a query places on a column that carries no index, " +
		"and `FOREIGN KEY` columns are deliberately excluded from `keyed` — read \"machine-checked\" " +
		"as \"every recognized PRIMARY KEY/UNIQUE/INDEX construct is captured\", not as \"every " +
		"index-like thing in the schema is\". `—` marks a column present in that dialect with no " +
		"recorded key or index; `n/a` marks a column that does not exist in that dialect at all " +
		"(every `casbin_rule` column, in mysql and sqlite).\n\n")
	b.WriteString("The `column` cell holds the **canonical** name, which is what the per-dialect " +
		"key sets are compared under. Where a dialect declares that column under a different " +
		"identifier, its type cell discloses it as \"<type> (declared `<name>`)\" — MySQL's " +
		"`wrkflw_journal` payload column is the one instance today, because `trigger` is a MySQL " +
		"reserved word. A migration you write against that dialect must use the DECLARED name.\n\n")
	if len(casbinCols) > 0 {
		fmt.Fprintf(&b, "⚠ **`keyed` is a lower bound on the column, not on the query.** "+
			"`casbin_rule.{%s}` are class `policy`; "+
			"`internal/authz/casbin/pg_adapter.go`'s `RemovePolicy` deletes rows by filtering **all "+
			"%s** of those columns with equality (`WHERE ptype=$1 AND v0=$2 AND … AND v5=$7`), "+
			"regardless of which of them this table marks keyed. Encrypting any of the %s "+
			"non-deterministically breaks that delete.\n\n",
			strings.Join(casbinCols, ","), englishNumber(len(casbinCols)), englishNumber(len(casbinCols)))
	}

	b.WriteString(renderColumnsTable(schemas, cls))
	b.WriteString("\n")

	b.WriteString("### What this table does not tell you\n\n")
	for _, line := range derivedActorWarnings(schemas, cls) {
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")

	casbinSet, ok := MigrationSets[casbinMigrationsDir]
	if !ok {
		return "", fmt.Errorf(
			"workflow-atrest: render: MigrationSets has no %q entry — the casbin availability "+
				"sentence would publish an empty note", casbinMigrationsDir)
	}
	fmt.Fprintf(&b, "`casbin_rule` is conditionally present: %s.\n\n", casbinSet.Note)

	locationLines, err := policyLocationLines(cls)
	if err != nil {
		return "", err
	}
	for _, line := range locationLines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if len(locationLines) > 0 {
		b.WriteString("\n")
	}

	b.WriteString("No column-level codec and no hash-chained journal ship with this delivery " +
		"(ADR-0187 D10, the non-goals — see `docs/specs/2026-08-22-at-rest-posture.md` § D10) — " +
		"nothing in this table implies one exists.\n")

	return b.String(), nil
}

// policyLocationLines renders the "authorization policy is durable at rest in
// N places" paragraph from PolicyAtRestLocations, with N DERIVED from the
// locations that apply to cls rather than retyped. It returns no lines at all
// when cls classifies a schema none of the locations belong to (the synthetic
// fixtures in the tests), exactly as casbinPolicyColumns skips its paragraph.
//
// It fails closed twice, in opposite directions:
//
//   - a declared location whose table IS in cls but whose column is not —
//     a rename or a drop that would leave the sentence naming a place that no
//     longer exists;
//   - a ClassPolicy column in cls that no location covers — the shape of the
//     defect this whole paragraph replaces, where the third location existed
//     and the hand-written sentence said "two".
func policyLocationLines(cls map[ColumnKey]Class) ([]string, error) {
	tablesInCls := make(map[string]bool)
	for key := range cls {
		tablesInCls[key.Table] = true
	}

	var applicable []PolicyLocation
	for _, loc := range PolicyAtRestLocations {
		if !tablesInCls[loc.Table] {
			continue
		}
		if loc.Column != "" {
			if _, ok := cls[ColumnKey{Table: loc.Table, Column: loc.Column}]; !ok {
				return nil, fmt.Errorf(
					"workflow-atrest: render: policy-at-rest location %s.%s is not a classified "+
						"column — the published locations sentence would name a column that is gone",
					loc.Table, loc.Column)
			}
		}
		applicable = append(applicable, loc)
	}

	if err := everyPolicyColumnIsLocated(cls, applicable); err != nil {
		return nil, err
	}

	if len(applicable) == 0 {
		return nil, nil
	}

	lines := []string{fmt.Sprintf(
		"⚠ Authorization policy is durable at rest in **%s** places, not one. Encrypting only the "+
			"`policy`-classed columns is therefore not enough:", englishNumber(len(applicable)))}
	for _, loc := range applicable {
		lines = append(lines, fmt.Sprintf("- `%s` %s.", qualifiedLocation(loc), loc.Detail))
	}

	return lines, nil
}

// everyPolicyColumnIsLocated reports an error naming the first (sorted)
// ClassPolicy column in cls that no location in applicable covers. A
// table-scoped location (empty Column) covers every column of its table.
func everyPolicyColumnIsLocated(cls map[ColumnKey]Class, applicable []PolicyLocation) error {
	var unlocated []ColumnKey
	for key, class := range cls {
		if class != ClassPolicy {
			continue
		}
		located := slices.ContainsFunc(applicable, func(loc PolicyLocation) bool {
			return loc.Table == key.Table && (loc.Column == "" || loc.Column == key.Column)
		})
		if !located {
			unlocated = append(unlocated, key)
		}
	}
	if len(unlocated) == 0 {
		return nil
	}

	slices.SortFunc(unlocated, compareColumnKeys)

	return fmt.Errorf(
		"workflow-atrest: render: %d `policy`-classed column(s) are covered by no "+
			"PolicyAtRestLocations entry (first: %s.%s) — the published \"policy is durable at "+
			"rest in N places\" sentence would undercount",
		len(unlocated), unlocated[0].Table, unlocated[0].Column)
}

// qualifiedLocation renders a location as "table" or "table.column".
func qualifiedLocation(loc PolicyLocation) string {
	if loc.Column == "" {
		return loc.Table
	}
	return loc.Table + "." + loc.Column
}

// renderColumnsTable renders the header, separator, and one row per key in
// cls, sorted by (table, column) (ADR-0187 D12). Go randomises map
// iteration — including two ranges of the same map within one process — so
// the sort is required for Render to be deterministic; see
// TestRenderIsDeterministic.
func renderColumnsTable(schemas map[string]Schema, cls map[ColumnKey]Class) string {
	var keys []ColumnKey
	for key := range cls {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, compareColumnKeys)

	header := []string{"table", "column", "class"}
	for _, d := range renderDialects {
		header = append(header, d+" type", d+" keyed")
	}

	var b strings.Builder
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat("---|", len(header)) + "\n")
	for _, key := range keys {
		fields := []string{key.Table, key.Column, string(cls[key])}
		for _, d := range renderDialects {
			typeCell, keyedCell := columnCell(schemas[d], key)
			fields = append(fields, typeCell, keyedCell)
		}
		b.WriteString("| " + strings.Join(fields, " | ") + " |\n")
	}

	return b.String()
}

// columnCell returns the (type, keyed) cell pair for key in schema: "n/a"
// for both if the column does not exist in this dialect at all; the
// declared type and "—" if it exists but carries no recorded key or index;
// the declared type and its sorted, comma-joined Keys otherwise.
//
// When the dialect DECLARES the column under a different name than the
// canonical key — MySQL's wrkflw_journal.trigger_, normalized to "trigger" for
// cross-dialect set operations (D2b) — the type cell discloses the declared
// name. The normalization exists so key sets can be compared; publishing its
// output as if it were the dialect's own identifier hands a DBA a MySQL
// encryption migration against a column name MySQL rejects.
func columnCell(schema Schema, key ColumnKey) (typeCell, keyedCell string) {
	col, ok := schema.Columns[key]
	if !ok {
		return "n/a", "n/a"
	}

	typeCell = col.Type
	if col.Name != "" && col.Name != key.Column {
		typeCell = fmt.Sprintf("%s (declared `%s`)", col.Type, col.Name)
	}

	if len(col.Keys) == 0 {
		return typeCell, "—"
	}

	sortedKeys := slices.Clone(col.Keys)
	slices.Sort(sortedKeys)

	return typeCell, strings.Join(sortedKeys, ", ")
}

// derivedActorWarnings returns the "What this table does not tell you"
// bullet lines: one per actor-classed column that is keyed (indexed, or a
// key/unique member) in at least one dialect, sorted by (table, column). The
// section is never silently empty: when the derivation finds none, it says so
// as a statement about the DERIVATION and repeats that `keyed` is a lower
// bound. It must never assert the universal negative "no actor-classed column
// is indexed" — that is an upper-bound safety claim drawn from a lower bound,
// so every silent under-derivation in the parser would convert directly into a
// false safety statement (the code-review finding this wording answers).
// Existential claims ("these columns ARE indexed") are sound from a lower
// bound and are the only per-column safety claims Render emits; there is
// deliberately no blanket "safe to encrypt" sentence (ADR-0187 round 1's
// withdrawn claim).
func derivedActorWarnings(schemas map[string]Schema, cls map[ColumnKey]Class) []string {
	var keys []ColumnKey
	for key, class := range cls {
		if class != ClassActor {
			continue
		}
		if actorColumnIsKeyedSomewhere(schemas, key) {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, compareColumnKeys)

	if len(keys) == 0 {
		return []string{
			"This derivation found no `actor`-classed column carrying a recorded key or index " +
				"in any dialect. `keyed` is a **lower bound** (see above), so that is the result of " +
				"the derivation, not an assurance that no index, unique constraint or equality " +
				"predicate touches one.",
		}
	}

	lines := []string{
		"The following `actor`-classed column(s) are indexed; encrypting any of them " +
			"non-deterministically breaks whatever equality lookup assumes deterministic ciphertext:",
	}
	for _, key := range keys {
		if key == claimedByActorKey {
			lines = append(lines, fmt.Sprintf(
				"- `%s.%s` IS indexed, so encrypting it non-deterministically costs the "+
					"`humantask.TaskStore.AssignedTo` lookup.", key.Table, key.Column))
			continue
		}
		lines = append(lines, fmt.Sprintf(
			"- `%s.%s` is indexed; encrypting it non-deterministically breaks the equality "+
				"lookup that index serves.", key.Table, key.Column))
	}

	return lines
}

// actorColumnIsKeyedSomewhere reports whether key carries at least one
// recorded key/index in any of renderDialects's schemas.
func actorColumnIsKeyedSomewhere(schemas map[string]Schema, key ColumnKey) bool {
	for _, d := range renderDialects {
		schema, ok := schemas[d]
		if !ok {
			continue
		}
		if col, ok := schema.Columns[key]; ok && len(col.Keys) > 0 {
			return true
		}
	}
	return false
}

// compareColumnKeys orders ColumnKeys by (Table, Column) — the same order
// ColumnKeysWithPrefix promises in schema.go.
func compareColumnKeys(a, b ColumnKey) int {
	if c := strings.Compare(a.Table, b.Table); c != 0 {
		return c
	}
	return strings.Compare(a.Column, b.Column)
}

package atrest_test

import (
	"flag"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/atrest"
)

// TestReplaceBlock pins ReplaceBlock's marker-splicing contract: it locates
// the BEGIN/END at-rest markers in current, replaces exactly the region
// between them with block, and never touches the surrounding prose. A
// current document missing either marker is an error — SECURITY.md must
// carry both before ReplaceBlock has anything to splice into.
func TestReplaceBlock(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		current string
		block   string
		assert  func(t *testing.T, result string, err error)
	}

	cases := []testCase{
		{
			name: "markers present: block replaces the marked region, prose untouched",
			current: "# Security\n\nSome hand-written prose.\n\n" +
				"<!-- BEGIN at-rest (generated) -->\nstale content\n<!-- END at-rest -->\n\n" +
				"More hand-written prose.\n",
			block: "fresh content",
			assert: func(t *testing.T, result string, err error) {
				require.NoError(t, err)
				assert.Contains(t, result, "Some hand-written prose.")
				assert.Contains(t, result, "More hand-written prose.")
				assert.NotContains(t, result, "stale content")
				assert.Contains(t, result, "<!-- BEGIN at-rest (generated) -->\n\nfresh content\n\n<!-- END at-rest -->")
			},
		},
		{
			name:    "missing BEGIN marker: error, current unchanged conceptually",
			current: "# Security\n\nNo markers here.\n<!-- END at-rest -->\n",
			block:   "fresh content",
			assert: func(t *testing.T, _ string, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "BEGIN")
			},
		},
		{
			name:    "missing END marker: error",
			current: "# Security\n\n<!-- BEGIN at-rest (generated) -->\nstale\n",
			block:   "fresh content",
			assert: func(t *testing.T, _ string, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "END")
			},
		},
		{
			name:    "no markers at all: error naming both",
			current: "# Security\n\nNothing generated here.\n",
			block:   "fresh content",
			assert: func(t *testing.T, _ string, err error) {
				require.Error(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, err := atrest.ReplaceBlock(tc.current, tc.block)
			tc.assert(t, result, err)
		})
	}
}

// TestReplaceBlockIsIdempotent proves that applying ReplaceBlock a second
// time with the same block, over the result of the first application,
// produces the exact same string. TestSecurityMdInSync writes and then
// re-asserts across a second process (via -update); if ReplaceBlock were
// not idempotent — e.g. it grew a blank line on every application — the
// generator could never reach a fixed point.
func TestReplaceBlockIsIdempotent(t *testing.T) {
	t.Parallel()

	current := "# Security\n\n<!-- BEGIN at-rest (generated) -->\n<!-- END at-rest -->\n"
	block := "line one\nline two"

	once, err := atrest.ReplaceBlock(current, block)
	require.NoError(t, err)

	twice, err := atrest.ReplaceBlock(once, block)
	require.NoError(t, err)

	assert.Equal(t, once, twice)
}

// TestRender pins Render's content contract over both the real repository
// schema/classification and small synthetic fixtures that isolate specific
// rendering rules: per-dialect n/a-vs-— distinctness, the derived
// actor-keyed warning (known and generic branches), and the
// missing-required-dialect failure branch.
func TestRender(t *testing.T) {
	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	realSchemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	onlyPGKey := atrest.ColumnKey{Table: "t", Column: "only_pg"}
	everywhereKey := atrest.ColumnKey{Table: "t", Column: "everywhere"}
	distinctnessSchemas := map[string]atrest.Schema{
		"postgres": {Dialect: "postgres", Columns: map[atrest.ColumnKey]atrest.Column{
			onlyPGKey:     {Table: "t", Name: "only_pg", Type: "TEXT"},
			everywhereKey: {Table: "t", Name: "everywhere", Type: "TEXT"},
		}},
		"mysql": {Dialect: "mysql", Columns: map[atrest.ColumnKey]atrest.Column{
			everywhereKey: {Table: "t", Name: "everywhere", Type: "TEXT"},
		}},
		"sqlite": {Dialect: "sqlite", Columns: map[atrest.ColumnKey]atrest.Column{
			everywhereKey: {Table: "t", Name: "everywhere", Type: "TEXT"},
		}},
	}
	distinctnessCls := map[atrest.ColumnKey]atrest.Class{
		onlyPGKey:     atrest.ClassReference,
		everywhereKey: atrest.ClassScalar,
	}

	weirdActorKey := atrest.ColumnKey{Table: "t", Column: "weird_actor"}
	genericWarningSchemas := map[string]atrest.Schema{
		"postgres": {Dialect: "postgres", Columns: map[atrest.ColumnKey]atrest.Column{
			weirdActorKey: {Table: "t", Name: "weird_actor", Type: "TEXT", Keys: []string{"index"}},
		}},
		"mysql":  {Dialect: "mysql", Columns: map[atrest.ColumnKey]atrest.Column{}},
		"sqlite": {Dialect: "sqlite", Columns: map[atrest.ColumnKey]atrest.Column{}},
	}
	genericWarningCls := map[atrest.ColumnKey]atrest.Class{weirdActorKey: atrest.ClassActor}

	noWarningSchemas := map[string]atrest.Schema{
		"postgres": {Dialect: "postgres", Columns: map[atrest.ColumnKey]atrest.Column{
			weirdActorKey: {Table: "t", Name: "weird_actor", Type: "TEXT"},
		}},
		"mysql":  {Dialect: "mysql", Columns: map[atrest.ColumnKey]atrest.Column{}},
		"sqlite": {Dialect: "sqlite", Columns: map[atrest.ColumnKey]atrest.Column{}},
	}

	type testCase struct {
		name    string
		schemas map[string]atrest.Schema
		cls     map[atrest.ColumnKey]atrest.Class
		assert  func(t *testing.T, result string, err error)
	}

	cases := []testCase{
		{
			name: "missing a required dialect is an error",
			schemas: map[string]atrest.Schema{
				"postgres": {Dialect: "postgres", Columns: map[atrest.ColumnKey]atrest.Column{}},
				"mysql":    {Dialect: "mysql", Columns: map[atrest.ColumnKey]atrest.Column{}},
			},
			cls: map[atrest.ColumnKey]atrest.Class{},
			assert: func(t *testing.T, _ string, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "sqlite")
			},
		},
		{
			name:    "real repository data: structure, hazards, and the forbidden blanket claim",
			schemas: realSchemas,
			cls:     atrest.Classification,
			assert: func(t *testing.T, result string, err error) {
				require.NoError(t, err)

				assert.Contains(t, result, "## Data at rest")
				assert.Contains(t, result, "### Classes")
				for _, marker := range []string{"`freeform`", "`actor`", "`policy`", "`reference`", "`scalar`", "`timestamp`"} {
					assert.Contains(t, result, marker)
				}

				// D8's lower-bound hazard and the goose-no-checksum caveat (A15).
				assert.Contains(t, result, "lower bound")
				assert.Contains(t, result, "goose keys migrations by version and stores no checksum")

				// The withdrawn round-1 blanket safety claim must never reappear.
				assert.NotContains(t, result, "index-free")
				assert.NotContains(t, result, "safe to encrypt")
				assert.NotContains(t, result, "without breaking an index")

				// The one derived warning that survives (ADR-0187 D8). AssignedTo is named by
				// its resolvable symbol path (M6, final review): a bare "AssignedTo" is not
				// resolvable to a reader of this public document.
				assert.Contains(t, result,
					"`wrkflw_human_task.claimed_by` IS indexed, so encrypting it non-deterministically "+
						"costs the `humantask.TaskStore.AssignedTo` lookup.")

				// The one column whose DECLARED name differs from the canonical key: MySQL
				// declares wrkflw_journal.trigger_ ("trigger" is a MySQL reserved word) and
				// D2b normalizes it to "trigger" for set comparison. The normalization must
				// not reach the published cell: a DBA writing a MySQL encryption migration
				// from a row that says "trigger" under a "mysql type" heading targets a
				// column MySQL rejects.
				assert.Contains(t, result,
					"| wrkflw_journal | trigger | freeform | JSONB | — | JSON (declared `trigger_`) | — | TEXT | — |")

				// casbin_rule's conditional presence, SOURCED from MigrationSets.Note (not retyped).
				casbinSet := atrest.MigrationSets["internal/authz/casbin/migrations"]
				require.NotEmpty(t, casbinSet.Note, "test fixture assumption: the real Note is non-empty")
				assert.Contains(t, result, casbinSet.Note)

				// Authorization policy is durable at rest in THREE places, not two.
				// definition/model/node_wire.go declares eligible_roles /
				// eligible_privileges / eligible_expr on every node, and
				// internal/persistence/store/definitions.go marshals whole definitions
				// into wrkflw_definitions.definition — so a consumer who encrypts the two
				// `policy`-classed locations leaves per-node eligibility rules in the
				// clear. Each name is asserted in its BACKTICKED, TABLE-QUALIFIED form,
				// which appears only in the locations list: a bare "eligibility" would
				// also match the Columns-table row and could never fail.
				assert.Contains(t, result, "durable at rest in **three** places")
				assert.Contains(t, result, "`wrkflw_human_task.eligibility`")
				assert.Contains(t, result, "`wrkflw_definitions.definition`")
				assert.NotContains(t, result, "durable at rest in **two** places")

				// Consumer migrations out of scope; pointer to the non-goals (D10).
				assert.Contains(t, result, "out of scope")
				assert.Contains(t, result, "ADR-0187")
				assert.Contains(t, result, "D10")

				// Exactly 87 data rows plus a header and separator line = 89 lines
				// starting with "|". ⚠ The previous form compared pipeLines against
				// len(atrest.Classification)+2, which is a TAUTOLOGY — the table is
				// generated by looping over that very map, so the assertion held for
				// any census whatsoever and could not fail. The pin the comment
				// claims is the absolute one: 89 lines. It fails the moment a column
				// is added to or removed from the migrations (which forces a
				// Classification entry, which the completeness guard enforces),
				// making the census a deliberate edit rather than a silent one.
				var pipeLines int
				for _, line := range strings.Split(result, "\n") {
					if strings.HasPrefix(line, "|") {
						pipeLines++
					}
				}
				assert.Equal(t, 89, pipeLines, "87 classified columns + header + separator")
				assert.Equal(t, len(atrest.Classification)+2, pipeLines,
					"and every classified column must have exactly one row")
			},
		},
		{
			name:    "n/a (absent) and — (present, unindexed) render distinctly",
			schemas: distinctnessSchemas,
			cls:     distinctnessCls,
			assert: func(t *testing.T, result string, err error) {
				require.NoError(t, err)
				assert.Contains(t, result,
					"| t | only_pg | reference | TEXT | — | n/a | n/a | n/a | n/a |")
				assert.Contains(t, result,
					"| t | everywhere | scalar | TEXT | — | TEXT | — | TEXT | — |")
			},
		},
		{
			name:    "generic derived-warning branch for an actor column keyed outside the known instance",
			schemas: genericWarningSchemas,
			cls:     genericWarningCls,
			assert: func(t *testing.T, result string, err error) {
				require.NoError(t, err)
				// M6 (final review): the mechanism is that encryption breaks the EQUALITY
				// LOOKUP that goes through the index, not "the index" itself — match the
				// claimed_by branch's wording, which already gets this right.
				assert.Contains(t, result,
					"`t.weird_actor` is indexed; encrypting it non-deterministically breaks the "+
						"equality lookup that index serves.")
			},
		},
		{
			name:    "no actor-classed column is keyed anywhere: reported as a derivation result, never as a universal negative",
			schemas: noWarningSchemas,
			cls:     genericWarningCls,
			assert: func(t *testing.T, result string, err error) {
				require.NoError(t, err)
				// The empty branch must state what the DERIVATION found, and restate that
				// `keyed` is a lower bound. An affirmative universal negative ("no actor
				// column is indexed in any dialect") is an UPPER-bound safety claim drawn
				// from a lower bound: every silent under-derivation in the parser would
				// convert straight into a false safety statement here.
				assert.Contains(t, result,
					"This derivation found no `actor`-classed column carrying a recorded key or index")
				assert.Contains(t, result, "lower bound")
				assert.NotContains(t, result, "No `actor`-classed column is indexed in any dialect today.")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := atrest.Render(tc.schemas, tc.cls)
			tc.assert(t, result, err)
		})
	}
}

// TestRenderIsDeterministic pins ADR-0187 D12: Render's inputs are both Go
// maps, and Go randomises map iteration on every range — including two
// ranges of the SAME map within one process — so two calls with identical
// input must still produce byte-identical output. Measured (round 2):
// without an explicit sort, renders differ in roughly 949-995 of 1000 runs
// (87 distinct row orders), which is a ~1% flaky GREEN for
// scripts/gen-at-rest.sh's write-then-reassert-in-a-second-process check —
// worse than a reliable red, because nobody investigates a green run.
//
// This is a standalone test, not folded into TestRender's table: its shape
// (call the SAME SUT twice and compare the two results to each other) does
// not fit the table's per-case "call once, assert on the one result"
// closure.
func TestRenderIsDeterministic(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	first, err := atrest.Render(schemas, atrest.Classification)
	require.NoError(t, err)

	second, err := atrest.Render(schemas, atrest.Classification)
	require.NoError(t, err)

	assert.Equal(t, first, second, "Render must be deterministic: sort rows by (table, column)")
}

// TestRenderCasbinPolicyColumnsAreDerivedNotRetyped is the regression guard
// for ADR-0187's final-review C1 finding: the "⚠ keyed is a lower bound on
// the column" paragraph and the Columns-table legend's "n/a … (every
// casbin_rule column, in mysql and sqlite)" claim must be DERIVED from cls
// and schemas, not retyped as a static literal that TestSecurityMdInSync can
// only ever compare against itself.
//
// Proof (C1's finding): before this fix, swapping casbin_rule.ptype
// ClassPolicy->ClassScalar and wrkflw_outbox.id ClassScalar->ClassPolicy
// (both keyed, so the byClass total is preserved) left the whole suite
// green while SECURITY.md kept asserting the swapped-out column "is class
// policy". The second subtest below pins exactly that mutation.
func TestRenderCasbinPolicyColumnsAreDerivedNotRetyped(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	t.Run("real classification: the derived sentence names exactly the seven casbin_rule policy columns", func(t *testing.T) {
		t.Parallel()
		result, err := atrest.Render(schemas, atrest.Classification)
		require.NoError(t, err)
		assert.Contains(t, result, "casbin_rule.{ptype,v0,v1,v2,v3,v4,v5}")
	})

	t.Run("reciprocal swap: casbin_rule.ptype<->wrkflw_outbox.id must fail closed, not render a false sentence", func(t *testing.T) {
		t.Parallel()
		mutated := maps.Clone(atrest.Classification)
		mutated[atrest.ColumnKey{Table: "casbin_rule", Column: "ptype"}] = atrest.ClassScalar
		mutated[atrest.ColumnKey{Table: "wrkflw_outbox", Column: "id"}] = atrest.ClassPolicy

		_, err := atrest.Render(schemas, mutated)
		require.Error(t, err,
			"the derived casbin_rule policy-column set no longer equals {ptype,v0..v5}; "+
				"Render must error instead of silently publishing a stale sentence")
	})

	t.Run("a casbin_rule column drifting into mysql must fail closed, not silently keep saying n/a", func(t *testing.T) {
		t.Parallel()
		mutatedSchemas := maps.Clone(schemas)
		mysqlCols := maps.Clone(schemas["mysql"].Columns)
		mysqlCols[atrest.ColumnKey{Table: "casbin_rule", Column: "ptype"}] = atrest.Column{
			Table: "casbin_rule", Name: "ptype", Type: "TEXT",
		}
		mutatedSchemas["mysql"] = atrest.Schema{Dialect: "mysql", Columns: mysqlCols}

		_, err := atrest.Render(mutatedSchemas, atrest.Classification)
		require.Error(t, err,
			"casbin_rule.ptype now exists in mysql, so the legend's \"every casbin_rule column, "+
				"in mysql and sqlite\" claim is false; Render must error rather than keep publishing it")
	})
}

// update rewrites the generated SECURITY.md block instead of asserting it.
// Run with: go test ./internal/atrest/... -run TestSecurityMdInSync -update
var update = flag.Bool("update", false, "rewrite the generated SECURITY.md block instead of asserting it")

// TestSecurityMdInSync is the drift guard (ADR-0187 decision 9): SECURITY.md's
// generated "Data at rest" block must match what Render produces from the
// migrations embedded in this module right now. It is the ONLY per-entry
// regression protection the classification's 85 non-claimed_by entries have
// anywhere in this delivery — see the step-3b reciprocal-swap proof in the
// Task 6 report.
func TestSecurityMdInSync(t *testing.T) {
	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	block, err := atrest.Render(schemas, atrest.Classification)
	require.NoError(t, err)

	path := filepath.Join(root, "SECURITY.md")
	current, err := os.ReadFile(path)
	require.NoError(t, err)

	next, err := atrest.ReplaceBlock(string(current), block)
	require.NoError(t, err, "SECURITY.md must carry the BEGIN/END at-rest markers")

	if *update {
		require.NoError(t, os.WriteFile(path, []byte(next), 0o644))
		t.Log("SECURITY.md rewritten; re-run without -update")
		return
	}

	assert.Equal(t, next, string(current),
		"SECURITY.md's at-rest block is stale — run scripts/gen-at-rest.sh")
}

// TestRenderPolicyLocationsAreDerivedAndChecked pins the machine check behind
// the "authorization policy is durable at rest in N places" sentence. The
// sentence was hand-written and said "two" while a third location —
// wrkflw_definitions.definition, which carries every node's eligible_roles /
// eligible_privileges / eligible_expr — existed and was published as class
// `freeform`. Two failure modes must now be loud rather than silent:
//
//   - a declared location that resolves to no classified column (a rename or a
//     dropped table would otherwise leave the sentence naming a place that no
//     longer exists);
//   - a `policy`-classed column that no declared location covers (the exact
//     shape of the original defect, from the other direction).
func TestRenderPolicyLocationsAreDerivedAndChecked(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	t.Run("every declared location resolves against the real classification", func(t *testing.T) {
		t.Parallel()

		require.NotEmpty(t, atrest.PolicyAtRestLocations)
		for _, loc := range atrest.PolicyAtRestLocations {
			assert.NotEmpty(t, loc.Detail, "%s: a location must say WHAT policy sits there", loc.Table)
		}

		result, err := atrest.Render(schemas, atrest.Classification)
		require.NoError(t, err)
		for _, loc := range atrest.PolicyAtRestLocations {
			assert.Contains(t, result, loc.Detail)
		}
	})

	t.Run("a stale location naming a dropped column fails closed", func(t *testing.T) {
		t.Parallel()

		mutated := maps.Clone(atrest.Classification)
		delete(mutated, atrest.ColumnKey{Table: "wrkflw_definitions", Column: "definition"})

		_, err := atrest.Render(schemas, mutated)
		require.Error(t, err,
			"a declared policy-at-rest location that no longer resolves must fail the build, "+
				"not publish a sentence naming a column that is gone")
	})

	t.Run("a policy-classed column no location covers fails closed", func(t *testing.T) {
		t.Parallel()

		mutated := maps.Clone(atrest.Classification)
		mutated[atrest.ColumnKey{Table: "wrkflw_human_task", Column: "note"}] = atrest.ClassPolicy

		_, err := atrest.Render(schemas, mutated)
		require.Error(t, err,
			"a new `policy`-classed column that no declared location names must fail the build — "+
				"that is exactly how the published count came to say two when it was three")
	})
}

// TestRenderStructureIsDerivedNotRetyped covers two hardcoded literals
// /code-review found in Render, both of which describe a structure the same
// function generates from a variable:
//
//   - the Columns table's header was a 9-cell literal while its rows loop over
//     the dialect list, so a change to that list produced a table whose header
//     and rows disagree;
//   - "Every stored column carries one of six classes" hardcoded "six" while
//     the sibling casbin sentence derives its count.
//
// Neither can fail while today's values happen to match, so each is stated as
// the invariant that DOES fail: header width must equal row width, and the
// spelled class count must equal the number of class bullets rendered. Both
// were mutation-verified (a dialect removed from renderDialects; a seventh
// classDescription added) — RED with the literals, GREEN once derived.
func TestRenderStructureIsDerivedNotRetyped(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	result, err := atrest.Render(schemas, atrest.Classification)
	require.NoError(t, err)

	var tableLines []string
	for _, line := range strings.Split(result, "\n") {
		if strings.HasPrefix(line, "|") {
			tableLines = append(tableLines, line)
		}
	}
	require.Greater(t, len(tableLines), 2, "header, separator and at least one row")

	cells := func(line string) int { return strings.Count(line, "|") }
	for i, line := range tableLines[1:] {
		assert.Equal(t, cells(tableLines[0]), cells(line),
			"row %d has a different cell count than the header: %s", i, line)
	}

	var bullets int
	for _, line := range strings.Split(result, "\n") {
		if strings.HasPrefix(line, "- **`") {
			bullets++
		}
	}
	assert.Contains(t, result,
		"Every stored column carries one of "+numberWord(bullets)+" classes",
		"the spelled class count must track the class bullets actually rendered")
}

// numberWord mirrors the generator's spelled-number rendering for the small
// counts this test can encounter.
func numberWord(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}
	if n >= 0 && n < len(words) {
		return words[n]
	}
	return strconv.Itoa(n)
}

// TestRenderCasbinAbsenceViolationsAreDeterministic pins the M5-class defect
// /code-review found in casbinColumnsAreAbsentFromDialects: it returned on the
// FIRST violation Go's randomised map iteration happened to visit, so with two
// or more violations the reported column varied run to run (three distinct
// error strings over 400 runs). reconcileMigrationSets already collects and
// sorts for exactly this reason.
func TestRenderCasbinAbsenceViolationsAreDeterministic(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	mutated := maps.Clone(schemas)
	mysqlCols := maps.Clone(schemas["mysql"].Columns)
	for _, col := range []string{"ptype", "v0", "v1"} {
		mysqlCols[atrest.ColumnKey{Table: "casbin_rule", Column: col}] = atrest.Column{
			Table: "casbin_rule", Name: col, Type: "TEXT",
		}
	}
	mutated["mysql"] = atrest.Schema{Dialect: "mysql", Columns: mysqlCols}

	_, first := atrest.Render(mutated, atrest.Classification)
	require.Error(t, first)
	for range 200 {
		_, err := atrest.Render(mutated, atrest.Classification)
		require.Error(t, err)
		require.Equal(t, first.Error(), err.Error(),
			"the violation report must be deterministic: collect and sort, do not return on the first map hit")
	}
	assert.Contains(t, first.Error(), "ptype")
	assert.Contains(t, first.Error(), "v1", "every violation must be reported, not just the first")
}

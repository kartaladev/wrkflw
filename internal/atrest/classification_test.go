package atrest_test

import (
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/atrest"
)

// TestClassificationCoversTheSchemaExactly is the completeness and
// staleness guard (ADR-0187 D6): every column the discovered schema
// actually declares must carry a stated class, and no classification
// entry may name a column the schema no longer declares.
func TestClassificationCoversTheSchemaExactly(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	inSchema := map[atrest.ColumnKey]bool{}
	for _, s := range schemas {
		for k := range s.Columns {
			inSchema[k] = true
		}
	}

	var unclassified []string
	for k := range inSchema {
		if _, ok := atrest.Classification[k]; !ok {
			unclassified = append(unclassified, k.Table+"."+k.Column)
		}
	}
	sort.Strings(unclassified)
	assert.Empty(t, unclassified,
		"every stored column must carry a stated class — a consumer who encrypts the columns we "+
			"name and leaves the rest in the clear has been harmed by our documentation")

	var stale []string
	for k := range atrest.Classification {
		if !inSchema[k] {
			stale = append(stale, k.Table+"."+k.Column)
		}
	}
	sort.Strings(stale)
	assert.Empty(t, stale,
		"a classification entry naming an absent column is stale — delete it, or the next "+
			"unclassified column hides under it")

	assert.Len(t, atrest.Classification, 87, "87 = 79 wrkflw_* + 8 casbin_rule (E6)")
}

// TestClassificationPerClassCounts asserts the exact per-class counts
// from the spec's classification section (E6) rather than re-deriving
// them from prose — four of the six refuted the author's own pre-count
// estimates.
func TestClassificationPerClassCounts(t *testing.T) {
	t.Parallel()
	counts := map[atrest.Class]int{}
	for _, c := range atrest.Classification {
		counts[c]++
	}
	assert.Equal(t, map[atrest.Class]int{
		atrest.ClassReference: 27,
		atrest.ClassTimestamp: 19,
		atrest.ClassScalar:    17,
		atrest.ClassFreeform:  11,
		atrest.ClassPolicy:    8,
		atrest.ClassActor:     5,
	}, counts, "E6; four of these six refuted the author's own pre-count estimates")
}

// TestNormalizedKeySetAgreesAcrossDialects is the dialect-invariance pin
// (ADR-0187): once D2b's normalization is applied, the wrkflw_* key set
// declared by each dialect's migrations must be identical. casbin_rule is
// deliberately excluded (prefix "wrkflw_" only) because it is
// postgres-only by construction (E3), and its absence elsewhere is not a
// divergence.
func TestNormalizedKeySetAgreesAcrossDialects(t *testing.T) {
	t.Parallel()

	root, err := atrest.ModuleRoot()
	require.NoError(t, err)
	schemas, err := atrest.LoadSchemas(root)
	require.NoError(t, err)

	// wrkflw_* only: casbin_rule is postgres-only by construction (E3) and its
	// absence elsewhere is not a divergence.
	pg := atrest.ColumnKeysWithPrefix(schemas["postgres"], "wrkflw_")
	my := atrest.ColumnKeysWithPrefix(schemas["mysql"], "wrkflw_")
	sq := atrest.ColumnKeysWithPrefix(schemas["sqlite"], "wrkflw_")

	assert.ElementsMatch(t, pg, my, "postgres vs mysql normalized key set")
	assert.ElementsMatch(t, pg, sq, "postgres vs sqlite normalized key set")
	assert.Len(t, pg, 79)

	// Sorted-order pin (fix round 1, finding 2 — MINOR): ColumnKeysWithPrefix's doc
	// contract promises keys sorted by (Table, Column); Task 7 consumes that
	// order from another package. ElementsMatch above is order-independent
	// and cannot pin this, so compare pg against an independently-sorted
	// copy of itself with assert.Equal — a dropped sort call almost never
	// coincides with the sorted order across 79 map-iterated entries.
	sortedPG := slices.Clone(pg)
	slices.SortFunc(sortedPG, func(a, b atrest.ColumnKey) int {
		if c := strings.Compare(a.Table, b.Table); c != 0 {
			return c
		}
		return strings.Compare(a.Column, b.Column)
	})
	assert.Equal(t, sortedPG, pg,
		"ColumnKeysWithPrefix must return keys sorted by (Table, Column)")
}

// TestKeySetMatcherFires is the liveness guard for ColumnKeysWithPrefix: it
// drives the SAME matcher TestNormalizedKeySetAgreesAcrossDialects uses in
// production, over a fixture that plants both a genuine key divergence
// (t.only_in_pg) and a same-name-different-table column ("other".ignored)
// that the matcher must exclude rather than report. A guard exercising a
// different function than the production assertion proves nothing.
func TestKeySetMatcherFires(t *testing.T) {
	t.Parallel()

	shared := atrest.ColumnKey{Table: "t", Column: "agrees"}
	onlyPG := atrest.ColumnKey{Table: "t", Column: "only_in_pg"}
	otherTable := atrest.ColumnKey{Table: "other", Column: "ignored"}

	// pg carries one column sqlite lacks, plus a column under a DIFFERENT table
	// prefix that the matcher must exclude rather than report as a divergence.
	schemas := map[string]atrest.Schema{
		"pg": {Dialect: "pg", Columns: map[atrest.ColumnKey]atrest.Column{
			shared:     {Table: "t", Name: "agrees", Type: "TEXT"},
			onlyPG:     {Table: "t", Name: "only_in_pg", Type: "TEXT"},
			otherTable: {Table: "other", Name: "ignored", Type: "TEXT"},
		}},
		"sq": {Dialect: "sq", Columns: map[atrest.ColumnKey]atrest.Column{
			shared: {Table: "t", Name: "agrees", Type: "TEXT"},
		}},
	}

	pgKeys := atrest.ColumnKeysWithPrefix(schemas["pg"], "t")
	sqKeys := atrest.ColumnKeysWithPrefix(schemas["sq"], "t")

	assert.NotElementsMatch(t, pgKeys, sqKeys,
		"the matcher must SEE the planted key divergence (t.only_in_pg)")
	assert.ElementsMatch(t, pgKeys, atrest.ColumnKeysWithPrefix(schemas["pg"], "t"),
		"and must be stable for identical input — a matcher that reports everything is as "+
			"useless as one that reports nothing")

	// Prefix-exclusion pin (fix round 1, finding 1 — IMPORTANT): the two
	// assertions above depend only on the planted key divergence
	// (t.only_in_pg) and pass even if the prefix filter is dropped
	// entirely (every column of every table would then leak into pgKeys,
	// but pgKeys would still differ from sqKeys and still be internally
	// stable). Assert directly that otherTable's column is excluded, and
	// that pgKeys contains exactly the two "t"-prefixed columns — nothing
	// else.
	assert.NotContains(t, pgKeys, otherTable,
		"a column under a DIFFERENT table prefix must be excluded, not merely fail to match sqKeys")
	assert.Len(t, pgKeys, 2,
		"pgKeys must contain exactly the two \"t\"-prefixed columns (shared, onlyPG) — not otherTable's")
}

// TestClaimedByIsTwoDifferentColumns pins the reason ColumnKey exists:
// wrkflw_human_task.claimed_by is a human principal (actor), while
// wrkflw_call_links.claimed_by is a worker lease owner (reference).
// A map[string]Class keyed on the bare column name would merge these
// two entries and silently mis-state one of them; this test fails
// against that refactor.
func TestClaimedByIsTwoDifferentColumns(t *testing.T) {
	t.Parallel()

	assert.Equal(t, atrest.ClassActor,
		atrest.Classification[atrest.ColumnKey{Table: "wrkflw_human_task", Column: "claimed_by"}],
		"a human principal: ADR-0098 calls it the scalar projection of claim.actor.id")
	assert.Equal(t, atrest.ClassReference,
		atrest.Classification[atrest.ColumnKey{Table: "wrkflw_call_links", Column: "claimed_by"}],
		"a worker lease owner: WithCallLinkLease(owner string, ttl) — not a person (E7)")
}

// TestDefinitionEligibilityFieldsAreTheDeclaredSet pins the premise behind the
// wrkflw_definitions.definition entry in PolicyAtRestLocations: the
// authorization policy a stored definition carries is exactly the three
// Eligible* fields NodeWire declares. Adding a fourth (say EligibleGroups)
// would widen what that column holds, and the location's Detail — published
// verbatim in SECURITY.md, where it names the three by their JSON keys — would
// silently understate it.
//
// What makes this test fail: any field whose name begins with "Eligible" being
// added to, removed from, or renamed in definition/model.NodeWire. Verified by
// mutation (a fourth field added to NodeWire: RED, naming the new field).
func TestDefinitionEligibilityFieldsAreTheDeclaredSet(t *testing.T) {
	t.Parallel()

	wireType := reflect.TypeOf(model.NodeWire{})

	var got []string
	for i := range wireType.NumField() {
		field := wireType.Field(i)
		if strings.HasPrefix(field.Name, "Eligible") {
			got = append(got, field.Name+" "+field.Tag.Get("json"))
		}
	}
	sort.Strings(got)

	assert.Equal(t, []string{
		"EligibleExpr eligible_expr,omitempty",
		"EligiblePrivileges eligible_privileges,omitempty",
		"EligibleRoles eligible_roles,omitempty",
	}, got,
		"NodeWire's eligibility fields changed; PolicyAtRestLocations' "+
			"wrkflw_definitions.definition entry names these by JSON key and must be re-derived")
}

// TestPolicyAtRestLocationsIncludesTheInstanceSnapshot is the regression test
// for backlog 141.
//
// What makes it fail before the fix: PolicyAtRestLocations names three
// locations and omits wrkflw_instances.snapshot, which carries the full
// authz.AuthzSpec via engine.InstanceState.Tasks[].Eligibility. The published
// SECURITY.md count is therefore short by one.
//
// ⚠ The completeness guard in render.go cannot catch this: it fails only for a
// ClassPolicy column, and wrkflw_instances.snapshot is ClassFreeform — the
// identical case wrkflw_definitions.definition was hand-added for.
func TestPolicyAtRestLocationsIncludesTheInstanceSnapshot(t *testing.T) {
	t.Parallel()

	var found bool
	for _, loc := range atrest.PolicyAtRestLocations {
		if loc.Table == "wrkflw_instances" && loc.Column == "snapshot" {
			found = true
			assert.NotEmpty(t, loc.Detail, "the entry must say WHY it holds policy")
		}
	}
	assert.True(t, found,
		"wrkflw_instances.snapshot holds every in-flight task's Eligibility (an authz.AuthzSpec) "+
			"and must be listed, or SECURITY.md understates where policy is durable")
}

package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

func nestAttr(depth int) map[string]any {
	m := map[string]any{"leaf": 1}
	for range depth {
		m = map[string]any{"a": m}
	}
	return m
}

// TestHumanTaskStoreActorAttributeDepth proves ADR-0189's attribute depth bound
// against a REAL store rather than against the guard itself.
//
// ⚠ This test exists because two earlier versions of that guard were validated only
// against their own logic and both admitted values the store then rejected FOREVER:
// json.Marshal alone admitted 20000 levels, and an Attributes-only JSON round trip
// admitted 9999 where the store admits 9998 — because the durable document nests the
// attributes inside an Actor inside a claim, and encoding/json caps the WHOLE
// document at 10000.
//
// The bound is httpcore's maxActorAttributeDepth = 64. What must be true here is that
// 64 is SUFFICIENT: a 64-deep attribute must survive Upsert → Get. The complementary
// half (that the guard REFUSES deeper values) is pinned in httpcore.
//
// SQLite only, on purpose: it is pure Go and needs no container, and the property
// under test is encoding/json's decoder limit, which is dialect-independent.
func TestHumanTaskStoreActorAttributeDepth(t *testing.T) {
	t.Parallel()

	db := dbtest.RunTestSQLite(t)
	ts, err := store.NewHumanTaskStore(db, dialect.NewSQLite())
	require.NoError(t, err)

	const bound = 64 // == httpcore.maxActorAttributeDepth

	seed := humantask.HumanTask{
		TaskID:     "tok-attr-depth",
		InstanceID: "inst-attr-depth",
		NodeID:     "approve",
		State:      humantask.Claimed,
		Claim: &humantask.Claim{
			Actor: authz.Actor{
				ID:         "alice",
				Roles:      []string{"manager"},
				Attributes: map[string]any{"deep": nestAttr(bound - 1)},
			},
			At: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
		},
		CreatedAt: time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC),
	}

	require.NoError(t, ts.Upsert(t.Context(), seed), "a bound-deep attribute must be storable")

	got, err := ts.Get(t.Context(), seed.TaskID)
	require.NoError(t, err,
		"a bound-deep attribute must be READABLE — this is the assertion both earlier guards never made")
	require.NotNil(t, got.Claim)
	assert.Equal(t, "alice", got.Claim.Actor.ID)
	assert.NotNil(t, got.Claim.Actor.Attributes["deep"], "the attribute survives the round trip")
}

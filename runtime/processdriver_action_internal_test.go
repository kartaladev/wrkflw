package runtime

import (
	"strings"
	"testing"

	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChildInstanceIDFor covers the derivation of a call-activity child instance
// id from its parent id and the StartSubInstance command id. Both id shapes the
// engine can mint are exercised: the built-in per-instance counter
// ("<instance>-c<N>", used when no IDGenerator is injected) and an opaque
// generator id (xid/uuid, what the runtime injects by default).
func TestChildInstanceIDFor(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		parentID   string
		commandID  string
		assertFunc func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			name:      "engine counter command id keeps its short suffix",
			parentID:  "p1",
			commandID: "p1-c3",
			assertFunc: func(t *testing.T, got string) {
				assert.Equal(t, "p1-sub-c3", got,
					"the legacy counter form must keep its byte-for-byte historical shape")
			},
		},
		{
			name:      "hyphenated parent id keeps its counter suffix",
			parentID:  "list-parent-ab-i1",
			commandID: "list-parent-ab-i1-c1",
			assertFunc: func(t *testing.T, got string) {
				assert.Equal(t, "list-parent-ab-i1-sub-c1", got)
			},
		},
		{
			name:      "opaque xid command id folds to a fixed-length suffix",
			parentID:  "d1jd0f3s7bqrq8j0hf9g",
			commandID: "d1jd0f3s7bqrq8j0hfa0",
			assertFunc: func(t *testing.T, got string) {
				suffix, ok := strings.CutPrefix(got, "d1jd0f3s7bqrq8j0hf9g-sub-")
				require.True(t, ok, "child id must stay parent-prefixed, got %q", got)
				assert.Len(t, suffix, childCommandSuffixLen,
					"an opaque command id must not be embedded verbatim")
				assert.NotContains(t, got, "d1jd0f3s7bqrq8j0hfa0",
					"the full command id must not be embedded")
			},
		},
		{
			name:      "hyphenated non-counter command id is hashed, not split",
			parentID:  "p1",
			commandID: "0198f2b1-7c3a-7e51-9a2d-abc123456789",
			assertFunc: func(t *testing.T, got string) {
				suffix, ok := strings.CutPrefix(got, "p1-sub-")
				require.True(t, ok, "child id must stay parent-prefixed, got %q", got)
				assert.Len(t, suffix, childCommandSuffixLen)
				assert.NotEqual(t, "p1-sub-abc123456789", got,
					"a trailing UUID segment is not a counter suffix")
			},
		},
		{
			name:      "counter-shaped id of a different instance is not treated as legacy",
			parentID:  "p1",
			commandID: "other-c1",
			assertFunc: func(t *testing.T, got string) {
				assert.NotEqual(t, "p1-sub-c1", got,
					"only the parent's own counter form may take the literal path")
				suffix, ok := strings.CutPrefix(got, "p1-sub-")
				require.True(t, ok)
				assert.Len(t, suffix, childCommandSuffixLen)
			},
		},
		{
			name:      "counter-shaped suffix carrying a non-digit is hashed",
			parentID:  "p1",
			commandID: "p1-cabbage",
			assertFunc: func(t *testing.T, got string) {
				assert.NotEqual(t, "p1-sub-cabbage", got,
					"only c<digits> is the engine's counter form")
				assert.Len(t, got, len("p1-sub-")+childCommandSuffixLen)
			},
		},
		{
			name:      "trailing hyphen does not yield an empty suffix",
			parentID:  "p1",
			commandID: "p1-",
			assertFunc: func(t *testing.T, got string) {
				assert.NotEqual(t, "p1-sub-", got, "an empty suffix would collide across commands")
				assert.Len(t, got, len("p1-sub-")+childCommandSuffixLen)
			},
		},
		{
			name:      "an over-long derivation folds to a bounded id",
			parentID:  strings.Repeat("x", childInstanceIDMaxLen),
			commandID: "someinstance-c1",
			assertFunc: func(t *testing.T, got string) {
				assert.LessOrEqual(t, len(got), childInstanceIDMaxLen,
					"a child id must never overflow the instance_id column before the depth guard trips")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := childInstanceIDFor(tc.parentID, tc.commandID)
			require.NotEmpty(t, got)
			tc.assertFunc(t, got)
		})
	}
}

// TestChildInstanceIDForIsDeterministic pins the property the call-link identity
// depends on: the same parent + command id must always derive the same child id,
// so a re-driven StartSubInstance addresses the child it already started, while
// two distinct commands of one parent never collide.
func TestChildInstanceIDForIsDeterministic(t *testing.T) {
	t.Parallel()

	const parentID = "d1jd0f3s7bqrq8j0hf9g"
	commandID := xid.New().String()

	first := childInstanceIDFor(parentID, commandID)
	second := childInstanceIDFor(parentID, commandID)
	assert.Equal(t, first, second, "derivation must be a pure function of parent + command id")

	other := childInstanceIDFor(parentID, xid.New().String())
	assert.NotEqual(t, first, other, "distinct commands of one parent must not collide")
}

// TestChildInstanceIDForBoundedAcrossNesting is the id-growth regression: with
// an opaque (hyphen-free) command id the old derivation embedded the WHOLE command
// id, so each nesting level grew the child id by ~25 characters and a chain far
// shallower than maxCallDepth overflowed a VARCHAR(255) instance_id column with
// an opaque driver error instead of the guarded depth failure. Growth per level
// must be constant.
func TestChildInstanceIDForBoundedAcrossNesting(t *testing.T) {
	t.Parallel()

	const levels = 12

	id := xid.New().String()
	growth := 0
	for level := range levels {
		next := childInstanceIDFor(id, xid.New().String())
		delta := len(next) - len(id)
		if level == 0 {
			growth = delta
		}
		assert.Equal(t, growth, delta, "per-level growth must be constant (level %d)", level)
		id = next
	}

	assert.LessOrEqual(t, len(id), childInstanceIDMaxLen,
		"a %d-level chain must stay within the bounded id length, got %d chars", levels, len(id))
}

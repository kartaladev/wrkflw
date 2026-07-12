package action_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
)

// TestCatalogRegistryLazyDefault verifies that a Catalog/Registry default policy is
// applied lazily at Resolve, only to an action that declares no policy of its own,
// and that NewCatalog(m)/NewRegistry() with no opts stay bare (no default policy).
func TestCatalogRegistryLazyDefault(t *testing.T) {
	t.Parallel()

	bare := action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) { return nil, nil })
	preWrapped := action.Wrap(bare, action.WithExecTimeout(90*time.Second))
	defTimeout := 5 * time.Second

	type testCase struct {
		name   string
		build  func() action.Catalog
		lookup string
		assert func(t *testing.T, p action.Policy, ok bool)
	}

	cases := []testCase{
		{
			name:   "NewCatalog with no opts stays bare",
			build:  func() action.Catalog { return action.NewCatalog(map[string]action.Action{"a": bare}) },
			lookup: "a",
			assert: func(t *testing.T, p action.Policy, ok bool) {
				require.True(t, ok)
				assert.True(t, p.Timeout == nil && p.Retry == nil && p.Recover == nil, "bare stays bare without a default")
			},
		},
		{
			name: "catalog default applies to a bare action",
			build: func() action.Catalog {
				return action.NewCatalog(map[string]action.Action{"a": bare}, action.WithExecTimeout(defTimeout))
			},
			lookup: "a",
			assert: func(t *testing.T, p action.Policy, ok bool) {
				require.True(t, ok)
				require.NotNil(t, p.Timeout)
				assert.Equal(t, defTimeout, *p.Timeout)
			},
		},
		{
			name: "catalog default does NOT override an action that declares its own",
			build: func() action.Catalog {
				return action.NewCatalog(map[string]action.Action{"a": preWrapped}, action.WithExecTimeout(defTimeout))
			},
			lookup: "a",
			assert: func(t *testing.T, p action.Policy, ok bool) {
				require.True(t, ok)
				require.NotNil(t, p.Timeout)
				assert.Equal(t, 90*time.Second, *p.Timeout, "per-action policy wins over the catalog default")
			},
		},
		{
			name: "catalog default miss returns not-found",
			build: func() action.Catalog {
				return action.NewCatalog(map[string]action.Action{"a": bare}, action.WithExecTimeout(defTimeout))
			},
			lookup: "missing",
			assert: func(t *testing.T, _ action.Policy, ok bool) {
				assert.False(t, ok)
			},
		},
		{
			name: "registry default applies to a bare registered action",
			build: func() action.Catalog {
				r := action.NewRegistry(action.WithRecover(false))
				require.NoError(t, r.Register("a", bare))
				return r
			},
			lookup: "a",
			assert: func(t *testing.T, p action.Policy, ok bool) {
				require.True(t, ok)
				require.NotNil(t, p.Recover)
				assert.False(t, *p.Recover)
			},
		},
		{
			name: "registry with no opts stays bare",
			build: func() action.Catalog {
				r := action.NewRegistry()
				require.NoError(t, r.Register("a", bare))
				return r
			},
			lookup: "a",
			assert: func(t *testing.T, p action.Policy, ok bool) {
				require.True(t, ok)
				assert.True(t, p.Timeout == nil && p.Retry == nil && p.Recover == nil)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cat := tc.build()
			a, ok := cat.Resolve(tc.lookup)
			var p action.Policy
			if ok {
				p = action.ResolvePolicy(a)
			}
			tc.assert(t, p, ok)
		})
	}
}

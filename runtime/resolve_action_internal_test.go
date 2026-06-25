package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// tag returns a ServiceAction whose Do returns {"tag": name}, used to identify
// which action (inline / scoped / global) was resolved in assertion closures.
func tag(name string) action.ServiceAction {
	return action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return map[string]any{"tag": name}, nil
	})
}

// tagOf calls a.Do and extracts the "tag" key, failing the test on any error.
func tagOf(t *testing.T, a action.ServiceAction) string {
	t.Helper()
	out, err := a.Do(t.Context(), nil)
	require.NoError(t, err, "action.Do must not fail")
	return out["tag"].(string)
}

// resolveActionPrecedenceDef builds a definition for the precedence tests:
//
//	start → inlineNode (WithAction inline) → namedNode (WithActionName "x") → idNode → e
//
// The definition also registers scoped actions "x" (also in global) and "scoped-only"
// (not in global), and no entry for "idNode".
func resolveActionPrecedenceDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := model.NewDefinition("d", 1).
		RegisterAction("x", tag("scoped")).
		RegisterAction("scoped-only", tag("scoped-only")).
		Add(model.NewStartEvent("start")).
		Add(model.NewServiceTask("inlineNode", model.WithAction(tag("inline")))).
		Add(model.NewServiceTask("namedNode", model.WithActionName("x"))).
		Add(model.NewServiceTask("idNode", model.WithActionName("idNode"))).
		Add(model.NewEndEvent("e")).
		Connect("start", "inlineNode").
		Connect("inlineNode", "namedNode").
		Connect("namedNode", "idNode").
		Connect("idNode", "e").
		Build()
	require.NoError(t, err, "resolveActionPrecedenceDef: Build must succeed")
	return def
}

// TestResolveActionFor verifies the inline→scoped→global precedence chain of
// resolveActionFor and the scoped→global chain of resolveActionName.
func TestResolveActionFor(t *testing.T) {
	t.Parallel()

	global := action.NewMapCatalog(map[string]action.ServiceAction{
		"x":    tag("global"),
		"comp": tag("global-comp"),
	})
	// clk and store are nil: resolvers never dereference them.
	r := NewRunner(global, nil, nil)
	def := resolveActionPrecedenceDef(t)

	type testCase struct {
		name   string
		nodeID string
		action string
		assert func(t *testing.T, got action.ServiceAction, ok bool)
	}

	cases := []testCase{
		{
			name:   "inline beats scoped and global",
			nodeID: "inlineNode",
			action: "x",
			assert: func(t *testing.T, got action.ServiceAction, ok bool) {
				require.True(t, ok, "must resolve")
				assert.Equal(t, "inline", tagOf(t, got))
			},
		},
		{
			name:   "scoped beats global when no inline",
			nodeID: "namedNode",
			action: "x",
			assert: func(t *testing.T, got action.ServiceAction, ok bool) {
				require.True(t, ok, "must resolve")
				assert.Equal(t, "scoped", tagOf(t, got))
			},
		},
		{
			name:   "global reached when empty nodeID (name-only path)",
			nodeID: "",
			action: "comp",
			assert: func(t *testing.T, got action.ServiceAction, ok bool) {
				require.True(t, ok, "must resolve")
				assert.Equal(t, "global-comp", tagOf(t, got))
			},
		},
		{
			name:   "miss returns false when name absent from all catalogs",
			nodeID: "idNode",
			action: "no-such-action",
			assert: func(t *testing.T, got action.ServiceAction, ok bool) {
				assert.False(t, ok, "must not resolve unknown name")
				assert.Nil(t, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := r.resolveActionFor(def, tc.nodeID, tc.action)
			tc.assert(t, got, ok)
		})
	}
}

// TestResolveActionName verifies the scoped→global two-tier chain used for
// secondary action references (compensation, cancel, SLA, etc.).
func TestResolveActionName(t *testing.T) {
	t.Parallel()

	global := action.NewMapCatalog(map[string]action.ServiceAction{
		"x":     tag("global-x"),
		"gonly": tag("global-only"),
	})
	r := NewRunner(global, nil, nil)
	def := resolveActionPrecedenceDef(t)

	type testCase struct {
		name       string
		actionName string
		assert     func(t *testing.T, got action.ServiceAction, ok bool)
	}

	cases := []testCase{
		{
			name:       "scoped action takes precedence over global",
			actionName: "x",
			assert: func(t *testing.T, got action.ServiceAction, ok bool) {
				require.True(t, ok, "must resolve")
				assert.Equal(t, "scoped", tagOf(t, got))
			},
		},
		{
			name:       "global action found when not in scoped catalog",
			actionName: "gonly",
			assert: func(t *testing.T, got action.ServiceAction, ok bool) {
				require.True(t, ok, "must resolve")
				assert.Equal(t, "global-only", tagOf(t, got))
			},
		},
		{
			name:       "miss when name absent from both catalogs",
			actionName: "absent",
			assert: func(t *testing.T, got action.ServiceAction, ok bool) {
				assert.False(t, ok)
				assert.Nil(t, got)
			},
		},
		{
			name:       "nil def does not consult scoped catalog",
			actionName: "scoped-only",
			assert: func(t *testing.T, got action.ServiceAction, ok bool) {
				assert.False(t, ok, "scoped-only must not resolve with nil def (not in global)")
				assert.Nil(t, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// For the "nil def" case use nil, else the real def.
			d := def
			if tc.name == "nil def does not consult scoped catalog" {
				d = nil
			}
			got, ok := r.resolveActionName(d, tc.actionName)
			tc.assert(t, got, ok)
		})
	}
}

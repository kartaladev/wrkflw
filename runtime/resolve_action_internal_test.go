package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// tag returns a action.Action whose Do returns {"tag": name}, used to identify
// which action (inline / scoped / global) was resolved in assertion closures.
func tag(name string) action.Action {
	return action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return map[string]any{"tag": name}, nil
	})
}

// tagOf calls a.Do and extracts the "tag" key, failing the test on any error.
func tagOf(t *testing.T, a action.Action) string {
	t.Helper()
	out, err := a.Do(t.Context(), nil)
	require.NoError(t, err, "action.Do must not fail")
	return out["tag"].(string)
}

// resolveActionScopedDef builds a definition whose top-level scoped catalog
// registers "x" (also in global, to prove scoped precedence) and "scoped-only"
// (not in global). It is used for the def-fallback case of resolveInvokeAction
// and the scoped tier of resolveActionName.
func resolveActionScopedDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("d", 1).
		RegisterAction("x", tag("scoped")).
		RegisterAction("scoped-only", tag("scoped-only")).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("idNode", activity.WithTaskAction("idNode"))).
		Add(event.NewEnd("e")).
		Connect("start", "idNode").
		Connect("idNode", "e").
		Build()
	require.NoError(t, err, "resolveActionScopedDef: Build must succeed")
	return def
}

// TestResolveInvokeAction verifies the scoped→global precedence chain of
// resolveInvokeAction, where the scope-effective scoped catalog is carried on
// the command by the engine (with a fallback to the top-level def's scoped
// catalog when the command carries none).
func TestResolveInvokeAction(t *testing.T) {
	t.Parallel()

	global := action.NewCatalog(map[string]action.Action{
		"x":     tag("global"),
		"gonly": tag("global-only"),
	})
	st, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	driver, err := NewProcessDriver(WithActionCatalog(global), WithInstanceStore(st))
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })
	def := resolveActionScopedDef(t)

	// cmdScoped is the scope-effective scoped catalog carried by the engine; it
	// registers "x" so we can prove the carried catalog (not the def's) is used.
	cmdScoped := action.NewCatalog(map[string]action.Action{
		"x": tag("cmd-scoped"),
	})

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		cmd    engine.InvokeAction
		assert func(t *testing.T, got action.Action, ok bool)
	}

	cases := []testCase{
		{
			name: "carried scoped catalog is used (over global, ignoring def)",
			def:  def,
			cmd:  engine.InvokeAction{Name: "x", Scoped: cmdScoped},
			assert: func(t *testing.T, got action.Action, ok bool) {
				require.True(t, ok, "must resolve")
				assert.Equal(t, "cmd-scoped", tagOf(t, got))
			},
		},
		{
			name: "scoped nil falls back to def.ScopedCatalog()",
			def:  def,
			cmd:  engine.InvokeAction{Name: "x"},
			assert: func(t *testing.T, got action.Action, ok bool) {
				require.True(t, ok, "must resolve")
				assert.Equal(t, "scoped", tagOf(t, got))
			},
		},
		{
			name: "global fallback when not in scoped",
			def:  def,
			cmd:  engine.InvokeAction{Name: "gonly"},
			assert: func(t *testing.T, got action.Action, ok bool) {
				require.True(t, ok, "must resolve")
				assert.Equal(t, "global-only", tagOf(t, got))
			},
		},
		{
			name: "total miss returns false",
			def:  def,
			cmd:  engine.InvokeAction{Name: "no-such-action"},
			assert: func(t *testing.T, got action.Action, ok bool) {
				assert.False(t, ok, "must not resolve unknown name")
				assert.Nil(t, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := driver.resolveInvokeAction(tc.def, tc.cmd)
			tc.assert(t, got, ok)
		})
	}
}

// TestResolveActionName verifies the scoped→global two-tier chain used for
// secondary action references (compensation, cancel, deadline, etc.).
func TestResolveActionName(t *testing.T) {
	t.Parallel()

	global := action.NewCatalog(map[string]action.Action{
		"x":     tag("global-x"),
		"gonly": tag("global-only"),
	})
	st2, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	driver, err := NewProcessDriver(WithActionCatalog(global), WithInstanceStore(st2))
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })
	def := resolveActionScopedDef(t)

	type testCase struct {
		name       string
		actionName string
		assert     func(t *testing.T, got action.Action, ok bool)
	}

	cases := []testCase{
		{
			name:       "scoped action takes precedence over global",
			actionName: "x",
			assert: func(t *testing.T, got action.Action, ok bool) {
				require.True(t, ok, "must resolve")
				assert.Equal(t, "scoped", tagOf(t, got))
			},
		},
		{
			name:       "global action found when not in scoped catalog",
			actionName: "gonly",
			assert: func(t *testing.T, got action.Action, ok bool) {
				require.True(t, ok, "must resolve")
				assert.Equal(t, "global-only", tagOf(t, got))
			},
		},
		{
			name:       "miss when name absent from both catalogs",
			actionName: "absent",
			assert: func(t *testing.T, got action.Action, ok bool) {
				assert.False(t, ok)
				assert.Nil(t, got)
			},
		},
		{
			name:       "nil def does not consult scoped catalog",
			actionName: "scoped-only",
			assert: func(t *testing.T, got action.Action, ok bool) {
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
			got, ok := driver.resolveActionName(d, tc.actionName)
			tc.assert(t, got, ok)
		})
	}
}

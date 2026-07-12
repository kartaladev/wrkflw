package action_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
)

// ── MapCatalog (moved from action_test.go) ─────────────────────────────────

func TestMapCatalogResolveAndRun(t *testing.T) {
	cat := action.NewCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return map[string]any{"greeting": "hi " + in["name"].(string)}, nil
		}),
	})

	a, ok := cat.Resolve("greet")
	require.True(t, ok)

	out, err := a.Do(t.Context(), map[string]any{"name": "Ada"})
	require.NoError(t, err)
	assert.Equal(t, "hi Ada", out["greeting"])

	_, ok = cat.Resolve("missing")
	assert.False(t, ok)
}

// ── Registry ───────────────────────────────────────────────────────────────

func TestRegistry_RegisterThenResolve(t *testing.T) {
	r := action.NewRegistry()
	a := action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.NoError(t, r.Register("my-action", a))

	got, ok := r.Resolve("my-action")
	require.True(t, ok)

	out, err := got.Do(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, true, out["ok"])
}

func TestRegistry_ResolveUnknown(t *testing.T) {
	r := action.NewRegistry()
	got, ok := r.Resolve("nope")
	assert.False(t, ok)
	assert.Nil(t, got)
}

func TestRegistry_DuplicateRegister(t *testing.T) {
	r := action.NewRegistry()
	a1 := action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return map[string]any{"who": "first"}, nil
	})
	a2 := action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return map[string]any{"who": "second"}, nil
	})

	require.NoError(t, r.Register("dup", a1))
	err := r.Register("dup", a2)
	require.Error(t, err)
	assert.ErrorIs(t, err, action.ErrActionExists)

	// First registration must be retained.
	got, ok := r.Resolve("dup")
	require.True(t, ok)
	out, _ := got.Do(t.Context(), nil)
	assert.Equal(t, "first", out["who"])
}

func TestRegistry_RegisterValidation(t *testing.T) {
	tests := map[string]struct {
		name    string
		a       action.Action
		wantErr error
	}{
		"empty name": {"", action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) { return nil, nil }), action.ErrEmptyActionName},
		"nil action": {"ok", nil, action.ErrNilAction},
	}

	for label, tc := range tests {
		t.Run(label, func(t *testing.T) {
			r := action.NewRegistry()
			err := r.Register(tc.name, tc.a)
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestRegistry_RegisterFunc(t *testing.T) {
	r := action.NewRegistry()
	fn := func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return map[string]any{"via": "func"}, nil
	}
	require.NoError(t, r.RegisterFunc("fn-action", fn))

	got, ok := r.Resolve("fn-action")
	require.True(t, ok)

	out, err := got.Do(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, "func", out["via"])
}

func TestRegistry_RegisterFuncNilFn(t *testing.T) {
	r := action.NewRegistry()
	err := r.RegisterFunc("fn-action", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, action.ErrNilAction)
}

func TestRegistry_MustRegister(t *testing.T) {
	r := action.NewRegistry()
	a := action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) { return nil, nil })

	// Must not panic for a fresh name.
	require.NotPanics(t, func() { r.MustRegister("new-action", a) })

	// Must panic on duplicate.
	require.Panics(t, func() { r.MustRegister("new-action", a) })
}

func TestRegistry_MustRegisterFunc(t *testing.T) {
	r := action.NewRegistry()
	fn := func(_ context.Context, _ map[string]any) (map[string]any, error) { return nil, nil }

	// Must not panic for a fresh name.
	require.NotPanics(t, func() { r.MustRegisterFunc("fn", fn) })

	// Must panic on duplicate.
	require.Panics(t, func() { r.MustRegisterFunc("fn", fn) })
}

func TestRegistry_PostConstruction(t *testing.T) {
	r := action.NewRegistry()

	names := []string{"alpha", "beta", "gamma", "delta"}
	for _, n := range names {
		n := n
		require.NoError(t, r.Register(n, action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"name": n}, nil
		})))
	}

	for _, n := range names {
		got, ok := r.Resolve(n)
		require.True(t, ok, "name %q must resolve", n)
		out, err := got.Do(t.Context(), nil)
		require.NoError(t, err)
		assert.Equal(t, n, out["name"])
	}
}

func TestRegistry_Concurrency(t *testing.T) {
	const writers = 50
	const readers = 50

	r := action.NewRegistry()
	var wg sync.WaitGroup
	wg.Add(writers + readers)

	// Seed one action so readers have something to find immediately.
	require.NoError(t, r.Register("seed", action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return nil, nil
	})))

	for i := range writers {
		go func(i int) {
			defer wg.Done()
			name := "action-" + string(rune('A'+i%26)) + "-" + string(rune('0'+i%10))
			// Each goroutine index produces a unique name, so no duplicate errors are
			// expected; errors are discarded only to keep the goroutine concise.
			_ = r.Register(name, action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
				return nil, nil
			}))
		}(i)
	}

	for range readers {
		go func() {
			defer wg.Done()
			// Read "seed" — must always be present.
			_, _ = r.Resolve("seed")
		}()
	}

	wg.Wait()
	// "seed" must still resolve correctly after all concurrent ops.
	_, ok := r.Resolve("seed")
	assert.True(t, ok)
}

// Compile-time assertion that *Registry satisfies Catalog.
func TestRegistry_ImplementsCatalog(t *testing.T) {
	var _ action.Catalog = (*action.Registry)(nil)
}

// ── Example ────────────────────────────────────────────────────────────────

func ExampleRegistry() {
	r := action.NewRegistry()
	r.MustRegister("greet", action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
		return map[string]any{"msg": "hello " + in["name"].(string)}, nil
	}))

	a, ok := r.Resolve("greet")
	if !ok {
		panic("greet not found")
	}

	out, _ := a.Do(context.Background(), map[string]any{"name": "world"})
	fmt.Println(out["msg"])
	// Output: hello world
}

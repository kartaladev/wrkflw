package action_test

import (
	"context"
	"testing"

	"github.com/kartaladev/wrkflw/action"
)

func act(tag string) action.Action {
	return action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
		return map[string]any{"tag": tag}, nil
	})
}

func TestResolve(t *testing.T) {
	scoped := action.NewCatalog(map[string]action.Action{"a": act("scoped")})
	global := action.NewCatalog(map[string]action.Action{"a": act("global"), "b": act("global-b")})

	tests := map[string]struct {
		scoped, global action.Catalog
		name           string
		assert         func(t *testing.T, got action.Action, ok bool)
	}{
		"scoped wins over global": {scoped, global, "a", func(t *testing.T, got action.Action, ok bool) {
			if !ok {
				t.Fatal("want ok")
			}
			out, _ := got.Do(context.Background(), nil)
			if out["tag"] != "scoped" {
				t.Fatalf("want scoped, got %v", out["tag"])
			}
		}},
		"falls back to global": {scoped, global, "b", func(t *testing.T, got action.Action, ok bool) {
			if !ok {
				t.Fatal("want ok")
			}
			out, _ := got.Do(context.Background(), nil)
			if out["tag"] != "global-b" {
				t.Fatalf("want global-b, got %v", out["tag"])
			}
		}},
		"nil scoped uses global": {nil, global, "a", func(t *testing.T, _ action.Action, ok bool) {
			if !ok {
				t.Fatal("want ok from global")
			}
		}},
		"nil global, scoped only": {scoped, nil, "a", func(t *testing.T, _ action.Action, ok bool) {
			if !ok {
				t.Fatal("want ok from scoped")
			}
		}},
		"both nil": {nil, nil, "a", func(t *testing.T, _ action.Action, ok bool) {
			if ok {
				t.Fatal("want miss")
			}
		}},
		"miss everywhere": {scoped, global, "zzz", func(t *testing.T, _ action.Action, ok bool) {
			if ok {
				t.Fatal("want miss")
			}
		}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := action.Resolve(tc.scoped, tc.global, tc.name)
			tc.assert(t, got, ok)
		})
	}
}

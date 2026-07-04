package definition_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
)

func noopFn(_ context.Context, in map[string]any) (map[string]any, error) { return in, nil }

func TestServiceTaskActionOptions(t *testing.T) {
	tests := map[string]struct {
		node   definition.Node
		assert func(t *testing.T, node definition.Node)
	}{
		"named action": {
			definition.NewServiceTask("st", definition.WithActionName("pay")),
			func(t *testing.T, node definition.Node) {
				if got := definition.ActionOf(node); got != "pay" {
					t.Fatalf("ActionOf = %q, want %q", got, "pay")
				}
				if got := definition.InlineActionOf(node) != nil; got != false {
					t.Fatalf("InlineActionOf present = %v, want %v", got, false)
				}
			},
		},
		"empty default": {
			definition.NewServiceTask("st"),
			func(t *testing.T, node definition.Node) {
				if got := definition.ActionOf(node); got != "" {
					t.Fatalf("ActionOf = %q, want %q", got, "")
				}
				if got := definition.InlineActionOf(node) != nil; got != false {
					t.Fatalf("InlineActionOf present = %v, want %v", got, false)
				}
			},
		},
		"inline action": {
			definition.NewServiceTask("st", definition.WithAction(action.Func(noopFn))),
			func(t *testing.T, node definition.Node) {
				if got := definition.ActionOf(node); got != "" {
					t.Fatalf("ActionOf = %q, want %q", got, "")
				}
				if got := definition.InlineActionOf(node) != nil; got != true {
					t.Fatalf("InlineActionOf present = %v, want %v", got, true)
				}
			},
		},
		"inline func": {
			definition.NewServiceTask("st", definition.WithActionFunc(noopFn)),
			func(t *testing.T, node definition.Node) {
				if got := definition.ActionOf(node); got != "" {
					t.Fatalf("ActionOf = %q, want %q", got, "")
				}
				if got := definition.InlineActionOf(node) != nil; got != true {
					t.Fatalf("InlineActionOf present = %v, want %v", got, true)
				}
			},
		},
		"businessrule name": {
			definition.NewBusinessRuleTask("br", definition.WithActionName("rule")),
			func(t *testing.T, node definition.Node) {
				if got := definition.ActionOf(node); got != "rule" {
					t.Fatalf("ActionOf = %q, want %q", got, "rule")
				}
				if got := definition.InlineActionOf(node) != nil; got != false {
					t.Fatalf("InlineActionOf present = %v, want %v", got, false)
				}
			},
		},
		"businessrule inline": {
			definition.NewBusinessRuleTask("br", definition.WithAction(action.Func(noopFn))),
			func(t *testing.T, node definition.Node) {
				if got := definition.ActionOf(node); got != "" {
					t.Fatalf("ActionOf = %q, want %q", got, "")
				}
				if got := definition.InlineActionOf(node) != nil; got != true {
					t.Fatalf("InlineActionOf present = %v, want %v", got, true)
				}
			},
		},
		"with name + retry": {
			definition.NewServiceTask("st", definition.WithActionName("pay"), definition.WithName("Pay")),
			func(t *testing.T, node definition.Node) {
				if got := definition.ActionOf(node); got != "pay" {
					t.Fatalf("ActionOf = %q, want %q", got, "pay")
				}
				if got := definition.InlineActionOf(node) != nil; got != false {
					t.Fatalf("InlineActionOf present = %v, want %v", got, false)
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, tc.node)
		})
	}
}

func TestRegisterActionScopedCatalog(t *testing.T) {
	def, err := definition.NewDefinition("d", 1).
		RegisterAction("score", action.Func(noopFn)).
		RegisterActionFunc("notify", noopFn).
		Add(definition.NewStartEvent("st")).
		Add(definition.NewServiceTask("s", definition.WithActionName("score"))).
		Add(definition.NewEndEvent("e")).
		Connect("st", "s").
		Connect("s", "e").
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cat := def.ScopedCatalog()
	if cat == nil {
		t.Fatal("ScopedCatalog nil, want non-nil")
	}
	if _, ok := cat.Resolve("score"); !ok {
		t.Fatal("scoped catalog missing 'score'")
	}
	if _, ok := cat.Resolve("notify"); !ok {
		t.Fatal("scoped catalog missing 'notify'")
	}
}

func TestBuildRejectsInlineAndNameConflict(t *testing.T) {
	_, err := definition.NewDefinition("d", 1).
		Add(definition.NewStartEvent("st")).
		Add(definition.NewServiceTask("s", definition.WithActionName("x"), definition.WithAction(action.Func(noopFn)))).
		Add(definition.NewEndEvent("e")).
		Connect("st", "s").
		Connect("s", "e").
		Build()
	if !errors.Is(err, definition.ErrActionInlineAndNameConflict) {
		t.Fatalf("err = %v, want ErrActionInlineAndNameConflict", err)
	}
}

func TestBuildRejectsDuplicateScopedAction(t *testing.T) {
	_, err := definition.NewDefinition("d", 1).
		RegisterAction("x", action.Func(noopFn)).
		RegisterAction("x", action.Func(noopFn)).
		Add(definition.NewStartEvent("st")).
		Add(definition.NewServiceTask("s", definition.WithActionName("x"))).
		Add(definition.NewEndEvent("e")).
		Connect("st", "s").
		Connect("s", "e").
		Build()
	if !errors.Is(err, definition.ErrDuplicateScopedAction) {
		t.Fatalf("err = %v, want ErrDuplicateScopedAction", err)
	}
}

func TestNoScopedActionsLeavesCatalogNil(t *testing.T) {
	def, err := definition.NewDefinition("d", 1).
		Add(definition.NewStartEvent("st")).
		Add(definition.NewServiceTask("s", definition.WithActionName("x"))).
		Add(definition.NewEndEvent("e")).
		Connect("st", "s").
		Connect("s", "e").
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if def.ScopedCatalog() != nil {
		t.Fatal("ScopedCatalog should be nil when nothing registered")
	}
}

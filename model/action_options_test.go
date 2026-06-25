package model_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/model"
)

func noopFn(_ context.Context, in map[string]any) (map[string]any, error) { return in, nil }

func TestServiceTaskActionOptions(t *testing.T) {
	tests := map[string]struct {
		node   model.Node
		assert func(t *testing.T, node model.Node)
	}{
		"named action": {
			model.NewServiceTask("st", model.WithActionName("pay")),
			func(t *testing.T, node model.Node) {
				if got := model.ActionOf(node); got != "pay" {
					t.Fatalf("ActionOf = %q, want %q", got, "pay")
				}
				if got := model.InlineActionOf(node) != nil; got != false {
					t.Fatalf("InlineActionOf present = %v, want %v", got, false)
				}
			},
		},
		"empty default": {
			model.NewServiceTask("st"),
			func(t *testing.T, node model.Node) {
				if got := model.ActionOf(node); got != "" {
					t.Fatalf("ActionOf = %q, want %q", got, "")
				}
				if got := model.InlineActionOf(node) != nil; got != false {
					t.Fatalf("InlineActionOf present = %v, want %v", got, false)
				}
			},
		},
		"inline action": {
			model.NewServiceTask("st", model.WithAction(action.Func(noopFn))),
			func(t *testing.T, node model.Node) {
				if got := model.ActionOf(node); got != "" {
					t.Fatalf("ActionOf = %q, want %q", got, "")
				}
				if got := model.InlineActionOf(node) != nil; got != true {
					t.Fatalf("InlineActionOf present = %v, want %v", got, true)
				}
			},
		},
		"inline func": {
			model.NewServiceTask("st", model.WithActionFunc(noopFn)),
			func(t *testing.T, node model.Node) {
				if got := model.ActionOf(node); got != "" {
					t.Fatalf("ActionOf = %q, want %q", got, "")
				}
				if got := model.InlineActionOf(node) != nil; got != true {
					t.Fatalf("InlineActionOf present = %v, want %v", got, true)
				}
			},
		},
		"businessrule name": {
			model.NewBusinessRuleTask("br", model.WithActionName("rule")),
			func(t *testing.T, node model.Node) {
				if got := model.ActionOf(node); got != "rule" {
					t.Fatalf("ActionOf = %q, want %q", got, "rule")
				}
				if got := model.InlineActionOf(node) != nil; got != false {
					t.Fatalf("InlineActionOf present = %v, want %v", got, false)
				}
			},
		},
		"businessrule inline": {
			model.NewBusinessRuleTask("br", model.WithAction(action.Func(noopFn))),
			func(t *testing.T, node model.Node) {
				if got := model.ActionOf(node); got != "" {
					t.Fatalf("ActionOf = %q, want %q", got, "")
				}
				if got := model.InlineActionOf(node) != nil; got != true {
					t.Fatalf("InlineActionOf present = %v, want %v", got, true)
				}
			},
		},
		"with name + retry": {
			model.NewServiceTask("st", model.WithActionName("pay"), model.WithName("Pay")),
			func(t *testing.T, node model.Node) {
				if got := model.ActionOf(node); got != "pay" {
					t.Fatalf("ActionOf = %q, want %q", got, "pay")
				}
				if got := model.InlineActionOf(node) != nil; got != false {
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
	def, err := model.NewDefinition("d", 1).
		RegisterAction("score", action.Func(noopFn)).
		RegisterActionFunc("notify", noopFn).
		Add(model.NewStartEvent("st")).
		Add(model.NewServiceTask("s", model.WithActionName("score"))).
		Add(model.NewEndEvent("e")).
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
	_, err := model.NewDefinition("d", 1).
		Add(model.NewStartEvent("st")).
		Add(model.NewServiceTask("s", model.WithActionName("x"), model.WithAction(action.Func(noopFn)))).
		Add(model.NewEndEvent("e")).
		Connect("st", "s").
		Connect("s", "e").
		Build()
	if !errors.Is(err, model.ErrActionInlineAndNameConflict) {
		t.Fatalf("err = %v, want ErrActionInlineAndNameConflict", err)
	}
}

func TestBuildRejectsDuplicateScopedAction(t *testing.T) {
	_, err := model.NewDefinition("d", 1).
		RegisterAction("x", action.Func(noopFn)).
		RegisterAction("x", action.Func(noopFn)).
		Add(model.NewStartEvent("st")).
		Add(model.NewServiceTask("s", model.WithActionName("x"))).
		Add(model.NewEndEvent("e")).
		Connect("st", "s").
		Connect("s", "e").
		Build()
	if !errors.Is(err, model.ErrDuplicateScopedAction) {
		t.Fatalf("err = %v, want ErrDuplicateScopedAction", err)
	}
}

func TestNoScopedActionsLeavesCatalogNil(t *testing.T) {
	def, err := model.NewDefinition("d", 1).
		Add(model.NewStartEvent("st")).
		Add(model.NewServiceTask("s", model.WithActionName("x"))).
		Add(model.NewEndEvent("e")).
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

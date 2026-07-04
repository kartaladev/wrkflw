package model_test

import (
	"context"
	"errors"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

func noopFn(_ context.Context, in map[string]any) (map[string]any, error) { return in, nil }

func TestServiceTaskActionOptions(t *testing.T) {
	tests := map[string]struct {
		node   model.Node
		assert func(t *testing.T, node model.Node)
	}{
		"named action": {
			activity.NewServiceTask("st", activity.WithActionName("pay")),
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
			activity.NewServiceTask("st"),
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
			activity.NewServiceTask("st", activity.WithAction(action.ActionFunc(noopFn))),
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
			activity.NewServiceTask("st", activity.WithActionFunc(noopFn)),
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
			activity.NewBusinessRuleTask("br", activity.WithActionName("rule")),
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
			activity.NewBusinessRuleTask("br", activity.WithAction(action.ActionFunc(noopFn))),
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
			activity.NewServiceTask("st", activity.WithActionName("pay"), activity.WithName("Pay")),
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
	def, err := model.NewBuilder("d", 1).
		RegisterAction("score", action.ActionFunc(noopFn)).
		RegisterActionFunc("notify", noopFn).
		Add(event.NewStart("st")).
		Add(activity.NewServiceTask("s", activity.WithActionName("score"))).
		Add(event.NewEnd("e")).
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
	_, err := model.NewBuilder("d", 1).
		Add(event.NewStart("st")).
		Add(activity.NewServiceTask("s", activity.WithActionName("x"), activity.WithAction(action.ActionFunc(noopFn)))).
		Add(event.NewEnd("e")).
		Connect("st", "s").
		Connect("s", "e").
		Build()
	if !errors.Is(err, model.ErrActionInlineAndNameConflict) {
		t.Fatalf("err = %v, want ErrActionInlineAndNameConflict", err)
	}
}

func TestBuildRejectsDuplicateScopedAction(t *testing.T) {
	_, err := model.NewBuilder("d", 1).
		RegisterAction("x", action.ActionFunc(noopFn)).
		RegisterAction("x", action.ActionFunc(noopFn)).
		Add(event.NewStart("st")).
		Add(activity.NewServiceTask("s", activity.WithActionName("x"))).
		Add(event.NewEnd("e")).
		Connect("st", "s").
		Connect("s", "e").
		Build()
	if !errors.Is(err, model.ErrDuplicateScopedAction) {
		t.Fatalf("err = %v, want ErrDuplicateScopedAction", err)
	}
}

func TestNoScopedActionsLeavesCatalogNil(t *testing.T) {
	def, err := model.NewBuilder("d", 1).
		Add(event.NewStart("st")).
		Add(activity.NewServiceTask("s", activity.WithActionName("x"))).
		Add(event.NewEnd("e")).
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

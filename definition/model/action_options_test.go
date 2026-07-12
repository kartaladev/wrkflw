package model_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

func noopFn(_ context.Context, in map[string]any) (map[string]any, error) { return in, nil }

func TestServiceTaskActionOptions(t *testing.T) {
	tests := map[string]struct {
		node   model.Node
		assert func(t *testing.T, node model.Node)
	}{
		"named action": {
			activity.NewServiceTask("st", activity.WithTaskAction("pay")),
			func(t *testing.T, node model.Node) {
				if got := model.ActionOf(node); got != "pay" {
					t.Fatalf("ActionOf = %q, want %q", got, "pay")
				}
			},
		},
		"empty default": {
			activity.NewServiceTask("st"),
			func(t *testing.T, node model.Node) {
				if got := model.ActionOf(node); got != "" {
					t.Fatalf("ActionOf = %q, want %q", got, "")
				}
			},
		},
		"businessrule name": {
			activity.NewBusinessRuleTask("br", activity.WithTaskAction("rule")),
			func(t *testing.T, node model.Node) {
				if got := model.ActionOf(node); got != "rule" {
					t.Fatalf("ActionOf = %q, want %q", got, "rule")
				}
			},
		},
		"with name + retry": {
			activity.NewServiceTask("st", activity.WithTaskAction("pay"), activity.WithName("Pay")),
			func(t *testing.T, node model.Node) {
				if got := model.ActionOf(node); got != "pay" {
					t.Fatalf("ActionOf = %q, want %q", got, "pay")
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
		Add(activity.NewServiceTask("s", activity.WithTaskAction("score"))).
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

func TestBuildRejectsDuplicateScopedAction(t *testing.T) {
	_, err := model.NewBuilder("d", 1).
		RegisterAction("x", action.ActionFunc(noopFn)).
		RegisterAction("x", action.ActionFunc(noopFn)).
		Add(event.NewStart("st")).
		Add(activity.NewServiceTask("s", activity.WithTaskAction("x"))).
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
		Add(activity.NewServiceTask("s", activity.WithTaskAction("x"))).
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

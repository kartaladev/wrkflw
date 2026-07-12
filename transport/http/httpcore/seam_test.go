package httpcore_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

func TestResolveConfigDefaults(t *testing.T) {
	cfg := httpcore.ResolveConfig[int]() // R=int is a stand-in router type for the seam test
	if cfg.Wrap == nil {
		t.Fatal("Wrap must default to a non-nil identity")
	}
	if got := cfg.Wrap(7); got != 7 {
		t.Fatalf("default Wrap must be identity, got %d", got)
	}
	if cfg.InstanceMapper == nil {
		t.Fatal("InstanceMapper must default to non-nil")
	}
	if cfg.Logger == nil {
		t.Fatal("Logger must default to slog.Default()")
	}
}

func TestOptionsCompose(t *testing.T) {
	inner := func(x int) int { return x + 1 }
	outer := func(x int) int { return x * 2 }
	cfg := httpcore.ResolveConfig(
		httpcore.WithBasePath[int]("/api"),
		httpcore.WithRouterFunc(inner),
		httpcore.WithRouterFunc(outer), // composes: later wraps earlier
	)
	if cfg.BasePath != "/api" {
		t.Fatalf("BasePath=%q", cfg.BasePath)
	}
	// outer(inner(3)) or inner(outer(3)); assert deterministic composition order.
	if got := cfg.Wrap(3); got != outer(inner(3)) {
		t.Fatalf("Wrap composition = %d, want %d", got, outer(inner(3)))
	}
}

type recordCustomizer struct{ hits *int }

func (c recordCustomizer) Customize(r int, _ ...httpcore.CustomizeOption[int]) { *c.hits++ }

func TestMountGroupsInvokesEach(t *testing.T) {
	hits := 0
	httpcore.MountGroups(0, recordCustomizer{&hits}, recordCustomizer{&hits})
	if hits != 2 {
		t.Fatalf("MountGroups invoked %d customizers, want 2", hits)
	}
}

func TestWithInstanceMapperOverrides(t *testing.T) {
	cfg := httpcore.ResolveConfig(httpcore.WithInstanceMapper[int](func(engine.InstanceState) any { return "x" }))
	if cfg.InstanceMapper(engine.InstanceState{}) != "x" {
		t.Fatal("WithInstanceMapper not applied")
	}
}

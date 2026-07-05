package action_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
)

func TestDefaultCatalog_RegisterAndResolve(t *testing.T) {
	t.Parallel()
	const name = "test-default-catalog-register" // unique: global registry, no reset
	if err := action.Register(name, action.ActionFunc(
		func(ctx context.Context, in map[string]any) (map[string]any, error) { return in, nil },
	)); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := action.DefaultCatalog().Resolve(name)
	if !ok || got == nil {
		t.Fatalf("Resolve(%q) after Register = (%v,%v), want hit", name, got, ok)
	}
}

func TestDefaultCatalog_RegisterFuncNil(t *testing.T) {
	t.Parallel()
	if err := action.RegisterFunc("x-nil-fn", nil); !errors.Is(err, action.ErrNilAction) {
		t.Fatalf("RegisterFunc(nil) = %v, want ErrNilAction", err)
	}
}

func TestDefaultCatalog_Identity(t *testing.T) {
	t.Parallel()
	if action.DefaultCatalog() != action.DefaultCatalog() {
		t.Fatal("DefaultCatalog() must return the same process-global registry")
	}
}

func ExampleRegister() {
	_ = action.Register("send-welcome-email", action.ActionFunc(
		func(ctx context.Context, in map[string]any) (map[string]any, error) {
			return map[string]any{"sent": true}, nil
		},
	))
	_, ok := action.DefaultCatalog().Resolve("send-welcome-email")
	fmt.Println(ok)
	// Output: true
}

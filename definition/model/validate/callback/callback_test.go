package callback_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/definition/model/validate"
	"github.com/kartaladev/wrkflw/definition/model/validate/callback"
)

func TestCallback_ValidatesAndIsNotDescribable(t *testing.T) {
	t.Parallel()
	s := callback.New(func(_ context.Context, in map[string]any) error {
		if in["ok"] != true {
			return errors.New("not ok")
		}
		return nil
	})
	if _, isDesc := s.(validate.DescribableStrategy); isDesc {
		t.Fatal("callback strategy must NOT implement DescribableStrategy")
	}
	v, _ := s.NewValidator()
	if err := v.Validate(t.Context(), map[string]any{"ok": true}); err != nil {
		t.Fatalf("valid rejected: %v", err)
	}
	if err := v.Validate(t.Context(), map[string]any{}); err == nil {
		t.Fatal("expected rejection")
	}
}

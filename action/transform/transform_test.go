package transform_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action/transform"
)

func TestTransform(t *testing.T) {
	tests := map[string]struct {
		opts   []transform.Option
		in     map[string]any
		assert func(t *testing.T, out map[string]any, err error)
	}{
		"computes a derived field": {
			[]transform.Option{transform.Set("total", "price * qty")},
			map[string]any{"price": 10, "qty": 3},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("Do err = %v", err)
				}
				if out["total"] != 30 {
					t.Fatalf("total = %v, want 30", out["total"])
				}
			},
		},
		"computes a boolean flag": {
			[]transform.Option{transform.Set("vip", "amount > 1000")},
			map[string]any{"amount": 1500},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("Do err = %v", err)
				}
				if out["vip"] != true {
					t.Fatalf("vip = %v, want true", out["vip"])
				}
			},
		},
		"runtime eval error surfaces": {
			[]transform.Option{transform.Set("x", "missing + 1")},
			map[string]any{},
			func(t *testing.T, out map[string]any, err error) {
				if err == nil {
					t.Fatalf("expected eval error, got nil")
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			a, err := transform.NewTransform(tc.opts...)
			if err != nil {
				t.Fatalf("NewTransform err = %v", err)
			}
			out, err := a.Do(t.Context(), tc.in)
			tc.assert(t, out, err)
		})
	}
}

func TestNewTransformCompileError(t *testing.T) {
	if _, err := transform.NewTransform(transform.Set("x", "price *")); err == nil {
		t.Fatalf("expected compile error for malformed expression, got nil")
	}
}

package transform_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/action/transform"
)

func TestTransform(t *testing.T) {
	tests := map[string]struct {
		opts   []transform.Option
		in     map[string]any
		assert func(t *testing.T, out map[string]any, err error)
	}{
		// 1. WithExpr pure: derived field and boolean flag; robust numeric comparison.
		"WithExpr pure total": {
			[]transform.Option{
				transform.WithExpr("total", "price * qty"),
				transform.WithExpr("vip", "amount > 1000"),
			},
			map[string]any{"price": 10, "qty": 3, "amount": 1500},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("Do err = %v", err)
				}
				if fmt.Sprintf("%v", out["total"]) != "30" {
					t.Fatalf("total = %v, want 30", out["total"])
				}
				if out["vip"] != true {
					t.Fatalf("vip = %v, want true", out["vip"])
				}
			},
		},
		// 2. WithExpr chaining: later expr references earlier expr output.
		"WithExpr chaining": {
			[]transform.Option{
				transform.WithExpr("subtotal", "price * qty"),
				transform.WithExpr("tax", "subtotal * 0.1"),
			},
			map[string]any{"price": 100, "qty": 5},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("Do err = %v", err)
				}
				if fmt.Sprintf("%v", out["subtotal"]) != "500" {
					t.Fatalf("subtotal = %v, want 500", out["subtotal"])
				}
				if fmt.Sprintf("%v", out["tax"]) != "50" {
					t.Fatalf("tax = %v, want 50", out["tax"])
				}
			},
		},
		// 3. WithMapper enrichment (scratch-only): mapper result NOT in out; WithExpr chaining works.
		"WithMapper enrichment scratch-only": {
			[]transform.Option{
				transform.WithMapper(func(_ context.Context, vars map[string]any) (map[string]any, error) {
					db := map[string]map[string]any{
						"C001": {"tier": "gold", "region": "EU"},
					}
					row := db[vars["customerID"].(string)]
					return row, nil
				}),
				transform.WithExpr("vip", "tier == 'gold'"),
			},
			map[string]any{"customerID": "C001"},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("Do err = %v", err)
				}
				if _, ok := out["tier"]; ok {
					t.Fatalf("out[\"tier\"] must not be present (mapper scratch only), got %v", out["tier"])
				}
				if _, ok := out["region"]; ok {
					t.Fatalf("out[\"region\"] must not be present (mapper scratch only), got %v", out["region"])
				}
				if out["vip"] != true {
					t.Fatalf("vip = %v, want true", out["vip"])
				}
			},
		},
		// 4. Pure WithMapper returns empty out.
		"pure WithMapper returns empty out": {
			[]transform.Option{
				transform.WithMapper(func(_ context.Context, vars map[string]any) (map[string]any, error) {
					return map[string]any{"tier": "gold"}, nil
				}),
			},
			map[string]any{"customerID": "C001"},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("Do err = %v", err)
				}
				if len(out) != 0 {
					t.Fatalf("out must be empty when only WithMapper used, got len=%d: %v", len(out), out)
				}
			},
		},
		// 5. Mapper→Expr chaining explicit case.
		"Mapper to Expr chaining": {
			[]transform.Option{
				transform.WithMapper(func(_ context.Context, _ map[string]any) (map[string]any, error) {
					return map[string]any{"tier": "gold"}, nil
				}),
				transform.WithExpr("vip", "tier == 'gold'"),
			},
			map[string]any{},
			func(t *testing.T, out map[string]any, err error) {
				if err != nil {
					t.Fatalf("Do err = %v", err)
				}
				if out["vip"] != true {
					t.Fatalf("vip = %v, want true", out["vip"])
				}
			},
		},
		// 7. NonRetryable propagation.
		"NonRetryable propagation": {
			[]transform.Option{
				transform.WithMapper(func(_ context.Context, _ map[string]any) (map[string]any, error) {
					return nil, action.NonRetryable(errors.New("bad row"))
				}),
			},
			map[string]any{},
			func(t *testing.T, out map[string]any, err error) {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if action.IsRetryable(err) {
					t.Fatalf("IsRetryable must be false for NonRetryable error, got true; err=%v", err)
				}
			},
		},
		// 9. Eval error (missing var).
		"eval error missing var": {
			[]transform.Option{
				transform.WithExpr("x", "missing + 1"),
			},
			map[string]any{},
			func(t *testing.T, out map[string]any, err error) {
				if err == nil {
					t.Fatalf("expected eval error, got nil")
				}
				if !strings.HasPrefix(err.Error(), "workflow-transform:") {
					t.Fatalf("error does not have expected prefix %q: %v", "workflow-transform:", err)
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

// 6. ctx cancellation: pre-cancelled context must cause Do to return context.Canceled.
func TestTransform_CtxCancellation(t *testing.T) {
	a, err := transform.NewTransform(
		transform.WithMapper(func(ctx context.Context, _ map[string]any) (map[string]any, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return map[string]any{"tier": "gold"}, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewTransform err = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel BEFORE calling Do

	_, doErr := a.Do(ctx, map[string]any{})
	if doErr == nil {
		t.Fatalf("expected error from cancelled ctx, got nil")
	}
	if !errors.Is(doErr, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", doErr)
	}
}

// 8. Compile error: malformed expr → NewTransform returns error.
func TestNewTransform_CompileError(t *testing.T) {
	_, err := transform.NewTransform(transform.WithExpr("x", "price *"))
	if err == nil {
		t.Fatalf("expected compile error for malformed expression, got nil")
	}
	if !strings.HasPrefix(err.Error(), "workflow-transform:") {
		t.Fatalf("error does not have expected prefix %q: %v", "workflow-transform:", err)
	}
}

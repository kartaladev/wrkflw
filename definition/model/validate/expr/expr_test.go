package expr_test

import (
	"strings"
	"testing"

	vexpr "github.com/kartaladev/wrkflw/definition/model/validate/expr"
)

func TestExpr_ValidateAndRoundTrip(t *testing.T) {
	t.Parallel()
	s := vexpr.New(`decision in ['approve','reject']`, `amount > 0`)

	v, err := s.NewValidator()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := v.Validate(t.Context(), map[string]any{"decision": "approve", "amount": 5}); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if err := v.Validate(t.Context(), map[string]any{"decision": "maybe", "amount": 5}); err == nil {
		t.Fatal("expected rejection for bad decision")
	}
	// missing field => failure, not silent-false-pass.
	if err := v.Validate(t.Context(), map[string]any{"decision": "approve"}); err == nil {
		t.Fatal("expected failure for missing amount")
	}

	d := s.Descriptor()
	if d.Kind != vexpr.Kind {
		t.Fatalf("kind = %q", d.Kind)
	}
	rebuilt, err := vexpr.Factory(d.Schema)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if got := strings.Split(d.Schema, "\n"); len(got) != 2 {
		t.Fatalf("schema predicates = %d", len(got))
	}
	rv, _ := rebuilt.NewValidator()
	if err := rv.Validate(t.Context(), map[string]any{"decision": "reject", "amount": 1}); err != nil {
		t.Fatalf("rebuilt rejected valid input: %v", err)
	}
}

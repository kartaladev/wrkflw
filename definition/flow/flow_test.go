package flow_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/definition/flow"
)

func TestNewDefaultsAndOptions(t *testing.T) {
	f := flow.New("a", "b")
	if f.ID != "a->b" || f.Source != "a" || f.Target != "b" {
		t.Fatalf("default flow = %+v", f)
	}
	if f.Condition != "" || f.IsDefault {
		t.Fatalf("expected unconditional non-default, got %+v", f)
	}

	g := flow.New("x", "y",
		flow.WithFlowID("custom"),
		flow.WithCondition("amount > 100"),
		flow.AsDefault(),
	)
	if g.ID != "custom" || g.Condition != "amount > 100" || !g.IsDefault {
		t.Fatalf("optioned flow = %+v", g)
	}
}

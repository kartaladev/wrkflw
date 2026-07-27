package flow_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestSequenceFlowJSON(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		flow   flow.SequenceFlow
		assert func(t *testing.T, encoded string, decoded flow.SequenceFlow, err error)
	}

	cases := []testCase{
		{
			name: "plain flow omits condition and default",
			flow: flow.New("a", "b"),
			assert: func(t *testing.T, encoded string, decoded flow.SequenceFlow, err error) {
				require.NoError(t, err)
				assert.JSONEq(t, `{"id":"a->b","source":"a","target":"b"}`, encoded)
				assert.Equal(t, flow.New("a", "b"), decoded)
			},
		},
		{
			name: "conditional default flow carries snake_case keys",
			flow: flow.New("x", "y", flow.WithFlowID("custom"), flow.WithCondition("amount > 100"), flow.AsDefault()),
			assert: func(t *testing.T, encoded string, decoded flow.SequenceFlow, err error) {
				require.NoError(t, err)
				assert.JSONEq(t, `{"id":"custom","source":"x","target":"y","condition":"amount > 100","is_default":true}`, encoded)
				assert.True(t, decoded.IsDefault)
				assert.Equal(t, "amount > 100", decoded.Condition)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			raw, err := json.Marshal(tc.flow)
			var decoded flow.SequenceFlow
			if err == nil {
				err = json.Unmarshal(raw, &decoded)
			}
			tc.assert(t, string(raw), decoded, err)
		})
	}
}

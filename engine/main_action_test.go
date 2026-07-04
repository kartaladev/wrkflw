package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

func TestMainActionName(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		node   model.Node
		assert func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			name: "explicit name on service task",
			node: activity.NewServiceTask("s", activity.WithActionName("pay")),
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "pay", got)
			},
		},
		{
			name: "default to node id for service task",
			node: activity.NewServiceTask("s"),
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "s", got)
			},
		},
		{
			name: "explicit name on business rule task",
			node: activity.NewBusinessRuleTask("b", activity.WithActionName("rule")),
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "rule", got)
			},
		},
		{
			name: "default to node id for business rule task",
			node: activity.NewBusinessRuleTask("b"),
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "b", got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := mainActionName(tc.node)
			tc.assert(t, got)
		})
	}
}

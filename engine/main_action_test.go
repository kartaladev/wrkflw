package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/definition"
)

func TestMainActionName(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		node   definition.Node
		assert func(t *testing.T, got string)
	}

	cases := []testCase{
		{
			name: "explicit name on service task",
			node: definition.NewServiceTask("s", definition.WithActionName("pay")),
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "pay", got)
			},
		},
		{
			name: "default to node id for service task",
			node: definition.NewServiceTask("s"),
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "s", got)
			},
		},
		{
			name: "explicit name on business rule task",
			node: definition.NewBusinessRuleTask("b", definition.WithActionName("rule")),
			assert: func(t *testing.T, got string) {
				assert.Equal(t, "rule", got)
			},
		},
		{
			name: "default to node id for business rule task",
			node: definition.NewBusinessRuleTask("b"),
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

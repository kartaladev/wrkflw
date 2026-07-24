package event_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

// TestWithLabel_AllKinds covers the human display label options (Task 5):
// WithLabel on Start/Catch/End/Boundary, plus the ThrowEvent/CompensationThrowEvent
// dedicated setters, all resolved through model.Node.Label().
func TestWithLabel_AllKinds(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		build  func() model.Node
		assert func(t *testing.T, n model.Node)
	}

	cases := []testCase{
		{
			name: "start",
			build: func() model.Node {
				return event.NewStart("s", event.WithLabel("Start Label"))
			},
			assert: func(t *testing.T, n model.Node) {
				assert.Equal(t, "Start Label", n.Label())
			},
		},
		{
			name: "catch",
			build: func() model.Node {
				return event.NewIntermediateCatch("c", event.WithLabel("Catch Label"))
			},
			assert: func(t *testing.T, n model.Node) {
				assert.Equal(t, "Catch Label", n.Label())
			},
		},
		{
			name: "end",
			build: func() model.Node {
				return event.NewEnd("e", event.WithLabel("End Label"))
			},
			assert: func(t *testing.T, n model.Node) {
				assert.Equal(t, "End Label", n.Label())
			},
		},
		{
			name: "boundary",
			build: func() model.Node {
				return event.NewBoundary("b", "host", event.WithLabel("Boundary Label"))
			},
			assert: func(t *testing.T, n model.Node) {
				assert.Equal(t, "Boundary Label", n.Label())
			},
		},
		{
			name: "intermediate throw",
			build: func() model.Node {
				return event.NewIntermediateThrow("t", event.WithThrowLabel("Throw Label"))
			},
			assert: func(t *testing.T, n model.Node) {
				assert.Equal(t, "Throw Label", n.Label())
			},
		},
		{
			name: "compensation throw",
			build: func() model.Node {
				return event.NewCompensateThrow("ct", event.WithCompensateThrowLabel("Compensate Label"))
			},
			assert: func(t *testing.T, n model.Node) {
				assert.Equal(t, "Compensate Label", n.Label())
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n := tc.build()
			require.NotNil(t, n)
			tc.assert(t, n)
		})
	}
}

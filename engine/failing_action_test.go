package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/model"
)

// TestEffectiveRetryPolicyPrecedence verifies override > node > default > none.
func TestEffectiveRetryPolicyPrecedence(t *testing.T) {
	t.Parallel()

	nodePolicy := &model.RetryPolicy{MaxAttempts: 7, InitialInterval: time.Second, BackoffCoef: 2, MaxInterval: time.Minute}
	defaultPolicy := &model.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Second, BackoffCoef: 2, MaxInterval: time.Minute}
	overridePolicy := &model.RetryPolicy{MaxAttempts: 9, InitialInterval: 2 * time.Second, BackoffCoef: 3, MaxInterval: time.Hour}

	nodeWith := activity.NewServiceTask("t", activity.WithTaskAction("a"), activity.WithRetryPolicy(nodePolicy))
	nodeBare := activity.NewServiceTask("t", activity.WithTaskAction("a"))

	// A node whose policy carries the two safety-only fields the action tier
	// cannot express (ADR-0126 field-merge).
	nodeSafetyPolicy := &model.RetryPolicy{
		MaxAttempts:        7,
		InitialInterval:    time.Second,
		BackoffCoef:        2,
		MaxInterval:        time.Minute,
		MaxElapsed:         5 * time.Minute,
		NonRetryableErrors: []string{"permission denied", "not found"},
	}
	nodeWithSafety := activity.NewServiceTask("t", activity.WithTaskAction("a"), activity.WithRetryPolicy(nodeSafetyPolicy))

	type testCase struct {
		name   string
		node   model.Node
		opt    StepOptions
		assert func(t *testing.T, rp model.RetryPolicy, ok bool)
	}

	cases := []testCase{
		{
			name: "override beats node and default",
			node: nodeWith,
			opt:  StepOptions{OverrideRetryPolicy: overridePolicy, DefaultRetryPolicy: defaultPolicy},
			assert: func(t *testing.T, rp model.RetryPolicy, ok bool) {
				require.True(t, ok)
				assert.Equal(t, 9, rp.MaxAttempts)
			},
		},
		{
			name: "node beats default when no override",
			node: nodeWith,
			opt:  StepOptions{DefaultRetryPolicy: defaultPolicy},
			assert: func(t *testing.T, rp model.RetryPolicy, ok bool) {
				require.True(t, ok)
				assert.Equal(t, 7, rp.MaxAttempts)
			},
		},
		{
			name: "default applies when node bare and no override",
			node: nodeBare,
			opt:  StepOptions{DefaultRetryPolicy: defaultPolicy},
			assert: func(t *testing.T, rp model.RetryPolicy, ok bool) {
				require.True(t, ok)
				assert.Equal(t, 3, rp.MaxAttempts)
			},
		},
		{
			name:   "none when nothing set",
			node:   nodeBare,
			opt:    StepOptions{},
			assert: func(t *testing.T, _ model.RetryPolicy, ok bool) { assert.False(t, ok) },
		},
		{
			name: "override applies even when node bare",
			node: nodeBare,
			opt:  StepOptions{OverrideRetryPolicy: overridePolicy},
			assert: func(t *testing.T, rp model.RetryPolicy, ok bool) {
				require.True(t, ok)
				assert.Equal(t, 9, rp.MaxAttempts)
			},
		},
		{
			// Field-merge (ADR-0126): the action override wins on the four fields
			// it can express, but the node's safety-only fields (MaxElapsed,
			// NonRetryableErrors) — which action.RetrySpecs cannot express — are
			// PRESERVED, not dropped.
			name: "override field-merges node safety fields",
			node: nodeWithSafety,
			opt:  StepOptions{OverrideRetryPolicy: overridePolicy},
			assert: func(t *testing.T, rp model.RetryPolicy, ok bool) {
				require.True(t, ok)
				assert.Equal(t, 9, rp.MaxAttempts, "override wins on MaxAttempts")
				assert.Equal(t, 3.0, rp.BackoffCoef, "override wins on BackoffCoef")
				assert.Equal(t, 5*time.Minute, rp.MaxElapsed, "node MaxElapsed preserved")
				assert.Equal(t, []string{"permission denied", "not found"}, rp.NonRetryableErrors,
					"node NonRetryableErrors preserved")
			},
		},
		{
			// When the node has NO policy, the override applies alone (no safety
			// fields to inherit).
			name: "override alone when node bare — no safety inheritance",
			node: nodeBare,
			opt:  StepOptions{OverrideRetryPolicy: overridePolicy},
			assert: func(t *testing.T, rp model.RetryPolicy, ok bool) {
				require.True(t, ok)
				assert.Equal(t, 9, rp.MaxAttempts)
				assert.Zero(t, rp.MaxElapsed)
				assert.Empty(t, rp.NonRetryableErrors)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rp, ok := effectiveRetryPolicy(tc.node, tc.opt)
			tc.assert(t, rp, ok)
		})
	}
}

// TestFailingActionNode verifies the runtime-facing helper that maps an
// ActionFailed CommandID back to the failing node + its scope-effective definition.
func TestFailingActionNode(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID:      "d",
		Version: 1,
		Nodes:   []model.Node{activity.NewServiceTask("task", activity.WithTaskAction("a"))},
	}

	type testCase struct {
		name   string
		st     InstanceState
		cmdID  string
		assert func(t *testing.T, name string, scopeDef *model.ProcessDefinition, ok bool)
	}

	cases := []testCase{
		{
			name: "resolves action name (default-by-id owned by the engine) and scope def",
			st: InstanceState{
				InstanceID: "p",
				Tokens:     []Token{{ID: "p-t1", NodeID: "task", AwaitCommand: "p-c1", State: TokenWaitingCommand}},
			},
			cmdID: "p-c1",
			assert: func(t *testing.T, name string, scopeDef *model.ProcessDefinition, ok bool) {
				require.True(t, ok)
				assert.Equal(t, "a", name, "explicit WithTaskAction(\"a\") is the lookup key")
				assert.Equal(t, def, scopeDef)
			},
		},
		{
			name: "no token awaiting the command returns false",
			st: InstanceState{
				InstanceID: "p",
				Tokens:     []Token{{ID: "p-t1", NodeID: "task", AwaitCommand: "p-cX", State: TokenWaitingCommand}},
			},
			cmdID: "p-c1",
			assert: func(t *testing.T, _ string, _ *model.ProcessDefinition, ok bool) {
				assert.False(t, ok)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			name, scopeDef, ok := FailingActionName(def, tc.st, tc.cmdID)
			tc.assert(t, name, scopeDef, ok)
		})
	}
}

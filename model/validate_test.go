package model_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/model"
)

func TestValidate(t *testing.T) {
	tests := map[string]struct {
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		"valid linear": {
			def: linearDef(),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"no start event": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{{ID: "end", Kind: model.KindEndEvent}},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrNoStartEvent)
			},
		},
		"multiple start events": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "s1", Kind: model.KindStartEvent},
					{ID: "s2", Kind: model.KindStartEvent},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "s1", Target: "end"},
					{ID: "f2", Source: "s2", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrMultipleStartEvents)
			},
		},
		"dangling flow target": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "ghost"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDanglingFlow)
			},
		},
		"dead end non-end node": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "task", Kind: model.KindServiceTask, Action: "x"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					// task has no outgoing → dead end
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDeadEnd)
			},
		},
		"start has incoming": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "task", Kind: model.KindServiceTask, Action: "x"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "start"}, // illegal: loops back to start
					{ID: "f3", Source: "task", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrStartHasIncoming)
			},
		},
		"end has outgoing": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "end", Kind: model.KindEndEvent},
					{ID: "task", Kind: model.KindServiceTask, Action: "x"},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "end"},
					{ID: "f2", Source: "end", Target: "task"}, // illegal: end has outgoing
					{ID: "f3", Source: "task", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrEndHasOutgoing)
			},
		},
		"dangling flow source": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "ghost", Target: "end"}, // source node missing
					{ID: "f2", Source: "start", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDanglingFlow)
			},
		},
		"condition on parallel gateway outgoing": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "fork", Kind: model.KindParallelGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "fork"},
					{ID: "f2", Source: "fork", Target: "a", Condition: "x > 1"}, // illegal
					{ID: "f3", Source: "fork", Target: "b"},
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrConditionNotAllowed)
			},
		},
		"default on parallel gateway outgoing": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "fork", Kind: model.KindParallelGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "fork"},
					{ID: "f2", Source: "fork", Target: "a", IsDefault: true}, // illegal
					{ID: "f3", Source: "fork", Target: "b"},
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDefaultNotAllowed)
			},
		},
		"multiple defaults on exclusive gateway": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "xor", Kind: model.KindExclusiveGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "xor"},
					{ID: "f2", Source: "xor", Target: "a", IsDefault: true},
					{ID: "f3", Source: "xor", Target: "b", IsDefault: true}, // illegal: two defaults
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrMultipleDefaults)
			},
		},
		// Event-based gateway rules
		"valid event-based gateway targeting catch events": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "ebg", Kind: model.KindEventBasedGateway},
					{ID: "sig-catch", Kind: model.KindIntermediateCatchEvent, SignalName: "sig.a"},
					{ID: "msg-catch", Kind: model.KindIntermediateCatchEvent, MessageName: "msg.b"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "ebg"},
					{ID: "f2", Source: "ebg", Target: "sig-catch"},
					{ID: "f3", Source: "ebg", Target: "msg-catch"},
					{ID: "f4", Source: "sig-catch", Target: "end"},
					{ID: "f5", Source: "msg-catch", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"event-based gateway flow targets non-catch node": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "ebg", Kind: model.KindEventBasedGateway},
					{ID: "sig-catch", Kind: model.KindIntermediateCatchEvent, SignalName: "sig.a"},
					{ID: "task", Kind: model.KindServiceTask, Action: "do-work"}, // non-catch
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "ebg"},
					{ID: "f2", Source: "ebg", Target: "sig-catch"},
					{ID: "f3", Source: "ebg", Target: "task"}, // illegal: non-catch target
					{ID: "f4", Source: "sig-catch", Target: "end"},
					{ID: "f5", Source: "task", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrEventGatewayTarget)
			},
		},
		// Boundary event attachment rules
		"valid boundary attached to service task": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "task", Kind: model.KindServiceTask, Action: "do-work"},
					// NonInterrupting omitted (false) = interrupting, the BPMN default.
					{ID: "boundary", Kind: model.KindBoundaryEvent, SignalName: "cancel", AttachedTo: "task"},
					{ID: "end", Kind: model.KindEndEvent},
					{ID: "cancel-end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "end"},
					{ID: "f3", Source: "boundary", Target: "cancel-end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"boundary attached to missing node": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "end", Kind: model.KindEndEvent},
					{ID: "boundary", Kind: model.KindBoundaryEvent, SignalName: "cancel", AttachedTo: "ghost"},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "end"},
					{ID: "f2", Source: "boundary", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrBoundaryAttachment)
			},
		},
		"boundary attached to non-activity node": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "xor", Kind: model.KindExclusiveGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "end", Kind: model.KindEndEvent},
					// boundary attached to a gateway — not an activity
					{ID: "boundary", Kind: model.KindBoundaryEvent, SignalName: "cancel", AttachedTo: "xor"},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "xor"},
					{ID: "f2", Source: "xor", Target: "a", Condition: "x > 0"},
					{ID: "f3", Source: "xor", Target: "b", IsDefault: true},
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
					{ID: "f6", Source: "boundary", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrBoundaryAttachment)
			},
		},
		"valid exclusive gateway with condition and default": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "xor", Kind: model.KindExclusiveGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "xor"},
					{ID: "f2", Source: "xor", Target: "a", Condition: "x > 1"},
					{ID: "f3", Source: "xor", Target: "b", IsDefault: true},
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, model.Validate(tc.def))
		})
	}
}

// validSubprocessDef returns a well-formed embedded subprocess definition
// (start → service task → end) for use in outer process tests.
func validSubprocessDef(id string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      id,
		Version: 1,
		Nodes: []model.Node{
			{ID: "ns-start", Kind: model.KindStartEvent},
			{ID: "ns-task", Kind: model.KindServiceTask, Action: "inner"},
			{ID: "ns-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "nf1", Source: "ns-start", Target: "ns-task"},
			{ID: "nf2", Source: "ns-task", Target: "ns-end"},
		},
	}
}

func TestValidateSubProcess(t *testing.T) {
	tests := map[string]struct {
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		"valid subprocess with valid nested definition": {
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "sp", Kind: model.KindSubProcess, Subprocess: validSubprocessDef("inner")},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"subprocess with nil Subprocess pointer is invalid": {
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "sp", Kind: model.KindSubProcess, Subprocess: nil},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrMissingSubprocess)
			},
		},
		"event-subprocess with nil Subprocess pointer is invalid": {
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "esp", Kind: model.KindEventSubProcess, Subprocess: nil},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "esp"},
					{ID: "f2", Source: "esp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrMissingSubprocess)
			},
		},
		"subprocess whose nested definition is malformed (start-has-incoming) propagates error": {
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "sp", Kind: model.KindSubProcess, Subprocess: &model.ProcessDefinition{
						ID:      "bad-inner",
						Version: 1,
						Nodes: []model.Node{
							{ID: "ns-start", Kind: model.KindStartEvent},
							{ID: "ns-task", Kind: model.KindServiceTask, Action: "inner"},
							{ID: "ns-end", Kind: model.KindEndEvent},
						},
						Flows: []model.SequenceFlow{
							{ID: "nf1", Source: "ns-start", Target: "ns-task"},
							{ID: "nf2", Source: "ns-task", Target: "ns-end"},
							// illegal: flow into the start event
							{ID: "nf3", Source: "ns-task", Target: "ns-start"},
						},
					}},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				// The nested error is propagated and is unwrappable.
				require.ErrorIs(t, err, model.ErrStartHasIncoming)
			},
		},
		"subprocess whose nested definition is malformed (dangling flow) propagates error": {
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "sp", Kind: model.KindSubProcess, Subprocess: &model.ProcessDefinition{
						ID:      "bad-inner-2",
						Version: 1,
						Nodes: []model.Node{
							{ID: "ns-start", Kind: model.KindStartEvent},
							{ID: "ns-end", Kind: model.KindEndEvent},
						},
						Flows: []model.SequenceFlow{
							{ID: "nf1", Source: "ns-start", Target: "ghost-node"}, // dangling
						},
					}},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDanglingFlow)
			},
		},
		"call-activity with non-empty DefRef is valid": {
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "ca", Kind: model.KindCallActivity, DefRef: "some-external-process"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "ca"},
					{ID: "f2", Source: "ca", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"call-activity with empty DefRef is invalid": {
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "ca", Kind: model.KindCallActivity, DefRef: ""},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "ca"},
					{ID: "f2", Source: "ca", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrMissingDefRef)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, model.Validate(tc.def))
		})
	}
}

// TestValidateCyclicSubprocessDoesNotPanic verifies that Validate does not
// stack-overflow on a hand-constructed cyclic subprocess pointer graph (A→B→A).
func TestValidateCyclicSubprocessDoesNotPanic(t *testing.T) {
	defA := &model.ProcessDefinition{
		ID: "cyclic-a", Version: 1,
		Nodes: []model.Node{
			{ID: "a-start", Kind: model.KindStartEvent},
			{ID: "a-sub", Kind: model.KindSubProcess},
			{ID: "a-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "af1", Source: "a-start", Target: "a-sub"},
			{ID: "af2", Source: "a-sub", Target: "a-end"},
		},
	}
	defB := &model.ProcessDefinition{
		ID: "cyclic-b", Version: 1,
		Nodes: []model.Node{
			{ID: "b-start", Kind: model.KindStartEvent},
			{ID: "b-sub", Kind: model.KindSubProcess},
			{ID: "b-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "bf1", Source: "b-start", Target: "b-sub"},
			{ID: "bf2", Source: "b-sub", Target: "b-end"},
		},
	}
	// Wire the cycle: A's sub-process points to B, B's sub-process points back to A.
	defA.Nodes[1].Subprocess = defB
	defB.Nodes[1].Subprocess = defA

	// Must not panic or stack-overflow.
	require.NotPanics(t, func() {
		_ = model.Validate(defA)
	}, "Validate must not panic on cyclic subprocess graph")
}

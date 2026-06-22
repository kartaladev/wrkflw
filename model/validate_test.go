package model_test

import (
	"testing"
	"time"

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
		// Mixed split+join gateway rules (ADR-0014)
		"mixed gateway both splits and joins": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "gw", Kind: model.KindExclusiveGateway},
					{ID: "c", Kind: model.KindServiceTask, Action: "c"},
					{ID: "d", Kind: model.KindServiceTask, Action: "d"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f0", Source: "start", Target: "a"},
					{ID: "f0b", Source: "start", Target: "b"}, // start splits to a and b
					{ID: "f1", Source: "a", Target: "gw"},
					{ID: "f2", Source: "b", Target: "gw"},     // gw has 2 incoming
					{ID: "f3", Source: "gw", Target: "c"},
					{ID: "f4", Source: "gw", Target: "d"},     // gw has 2 outgoing → mixed
					{ID: "f5", Source: "c", Target: "end"},
					{ID: "f6", Source: "d", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrMixedGateway)
			},
		},
		"pure split gateway is valid": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "gw", Kind: model.KindParallelGateway},
					{ID: "c", Kind: model.KindServiceTask, Action: "c"},
					{ID: "d", Kind: model.KindServiceTask, Action: "d"},
					{ID: "j", Kind: model.KindParallelGateway},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "gw"},
					{ID: "f2", Source: "gw", Target: "c"},
					{ID: "f3", Source: "gw", Target: "d"},
					{ID: "f4", Source: "c", Target: "j"},
					{ID: "f5", Source: "d", Target: "j"},
					{ID: "f6", Source: "j", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"unreachable orphan node": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "task", Kind: model.KindServiceTask, Action: "t"},
					{ID: "orphan", Kind: model.KindServiceTask, Action: "o"},
					{ID: "orphan-end", Kind: model.KindEndEvent},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "end"},
					{ID: "f3", Source: "orphan", Target: "orphan-end"}, // orphan unreachable from start
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrUnreachableNode)
			},
		},
		"node reachable via boundary on reachable host is valid": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "task", Kind: model.KindServiceTask, Action: "t"},
					{ID: "bnd", Kind: model.KindBoundaryEvent, AttachedTo: "task", TimerDuration: "PT1M"},
					{ID: "handler", Kind: model.KindServiceTask, Action: "h"},
					{ID: "hend", Kind: model.KindEndEvent},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "end"},
					{ID: "f3", Source: "bnd", Target: "handler"}, // reachable only via boundary
					{ID: "f4", Source: "handler", Target: "hend"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"node reachable only via boundary on unreachable host is unreachable": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "task", Kind: model.KindServiceTask, Action: "t"},
					{ID: "end", Kind: model.KindEndEvent},
					{ID: "ghost", Kind: model.KindServiceTask, Action: "g"}, // unreachable host
					{ID: "bnd", Kind: model.KindBoundaryEvent, AttachedTo: "ghost", TimerDuration: "PT1M"},
					{ID: "handler", Kind: model.KindServiceTask, Action: "h"},
					{ID: "hend", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "end"},
					{ID: "f3", Source: "ghost", Target: "end"},
					{ID: "f4", Source: "bnd", Target: "handler"},
					{ID: "f5", Source: "handler", Target: "hend"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrUnreachableNode)
			},
		},
		"zero start events does not run reachability": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{{ID: "end", Kind: model.KindEndEvent}},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrNoStartEvent)
				require.NotErrorIs(t, err, model.ErrUnreachableNode)
			},
		},
		"pure join gateway is valid": {
			// A parallel join needs a real parallel fork upstream: a start event
			// follows only its first outgoing flow (moveAlongSingleFlow), so
			// "start -> a, b" would never activate b and the join would deadlock.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "fork", Kind: model.KindParallelGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "j", Kind: model.KindParallelGateway},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f0", Source: "start", Target: "fork"},
					{ID: "f1", Source: "fork", Target: "a"},
					{ID: "f2", Source: "fork", Target: "b"},
					{ID: "f3", Source: "a", Target: "j"},
					{ID: "f4", Source: "b", Target: "j"},
					{ID: "f5", Source: "j", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"parallel join fed by exclusive split is unpaired": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "split", Kind: model.KindExclusiveGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "j", Kind: model.KindParallelGateway},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f0", Source: "start", Target: "split"},
					{ID: "f1", Source: "split", Target: "a"},
					{ID: "f2", Source: "split", Target: "b"},
					{ID: "f3", Source: "a", Target: "j"},
					{ID: "f4", Source: "b", Target: "j"},
					{ID: "f5", Source: "j", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrUnpairedJoin)
			},
		},
		"parallel join fed by inclusive split is paired": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "split", Kind: model.KindInclusiveGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "j", Kind: model.KindParallelGateway},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f0", Source: "start", Target: "split"},
					{ID: "f1", Source: "split", Target: "a"},
					{ID: "f2", Source: "split", Target: "b"},
					{ID: "f3", Source: "a", Target: "j"},
					{ID: "f4", Source: "b", Target: "j"},
					{ID: "f5", Source: "j", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"multiple starts skips pairing (reachability ill-defined)": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "s1", Kind: model.KindStartEvent},
					{ID: "s2", Kind: model.KindStartEvent},
					{ID: "split", Kind: model.KindExclusiveGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "j", Kind: model.KindParallelGateway},
					{ID: "end", Kind: model.KindEndEvent},
					{ID: "end2", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f0", Source: "s1", Target: "split"},
					{ID: "f0b", Source: "s2", Target: "end2"},
					{ID: "f1", Source: "split", Target: "a"},
					{ID: "f2", Source: "split", Target: "b"},
					{ID: "f3", Source: "a", Target: "j"},
					{ID: "f4", Source: "b", Target: "j"},
					{ID: "f5", Source: "j", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrMultipleStartEvents)
				// Pairing is skipped when the start count is ill-defined, so the
				// otherwise-unpaired join is not reported on an already-invalid def.
				require.NotErrorIs(t, err, model.ErrUnpairedJoin)
			},
		},
		"loop containing a properly forked parallel join is valid": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "merge", Kind: model.KindExclusiveGateway}, // loop-back merge (pure join)
					{ID: "fork", Kind: model.KindParallelGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "j", Kind: model.KindParallelGateway},
					{ID: "loop", Kind: model.KindExclusiveGateway}, // loop-back decision (pure split)
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f0", Source: "start", Target: "merge"},
					{ID: "f0b", Source: "merge", Target: "fork"},
					{ID: "f1", Source: "fork", Target: "a"},
					{ID: "f2", Source: "fork", Target: "b"},
					{ID: "f3", Source: "a", Target: "j"},
					{ID: "f4", Source: "b", Target: "j"},
					{ID: "f5", Source: "j", Target: "loop"},
					{ID: "f6", Source: "loop", Target: "merge", Condition: "again"}, // loop back to merge
					{ID: "f7", Source: "loop", Target: "end", IsDefault: true},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"unreachable parallel join reports only unreachable, not unpaired": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "task", Kind: model.KindServiceTask, Action: "t"},
					{ID: "end", Kind: model.KindEndEvent},
					// Disconnected component: an exclusive split feeding a parallel join
					// (would be ErrUnpairedJoin if reachable) — but it is unreachable.
					{ID: "osplit", Kind: model.KindExclusiveGateway},
					{ID: "ox", Kind: model.KindServiceTask, Action: "x"},
					{ID: "oy", Kind: model.KindServiceTask, Action: "y"},
					{ID: "oj", Kind: model.KindParallelGateway},
					{ID: "oend", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "end"},
					{ID: "f3", Source: "osplit", Target: "ox"},
					{ID: "f4", Source: "osplit", Target: "oy"},
					{ID: "f5", Source: "ox", Target: "oj"},
					{ID: "f6", Source: "oy", Target: "oj"},
					{ID: "f7", Source: "oj", Target: "oend"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrUnreachableNode)
				require.NotErrorIs(t, err, model.ErrUnpairedJoin) // unreachable join is skipped
			},
		},
		"inclusive join fed by exclusive split is not flagged (rule is parallel-only)": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "split", Kind: model.KindExclusiveGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "j", Kind: model.KindInclusiveGateway},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f0", Source: "start", Target: "split"},
					{ID: "f1", Source: "split", Target: "a"},
					{ID: "f2", Source: "split", Target: "b"},
					{ID: "f3", Source: "a", Target: "j"},
					{ID: "f4", Source: "b", Target: "j"},
					{ID: "f5", Source: "j", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrUnpairedJoin)
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
		"mixed gateway nested inside subprocess propagates ErrMixedGateway": {
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "sp", Kind: model.KindSubProcess, Subprocess: &model.ProcessDefinition{
						ID:      "inner-mixed",
						Version: 1,
						Nodes: []model.Node{
							{ID: "ns-start", Kind: model.KindStartEvent},
							{ID: "na", Kind: model.KindServiceTask, Action: "na"},
							{ID: "nb", Kind: model.KindServiceTask, Action: "nb"},
							{ID: "ngw", Kind: model.KindParallelGateway},
							{ID: "nc", Kind: model.KindServiceTask, Action: "nc"},
							{ID: "nd", Kind: model.KindServiceTask, Action: "nd"},
							{ID: "ns-end", Kind: model.KindEndEvent},
						},
						Flows: []model.SequenceFlow{
							{ID: "nf0", Source: "ns-start", Target: "na"},
							{ID: "nf0b", Source: "ns-start", Target: "nb"},
							{ID: "nf1", Source: "na", Target: "ngw"},
							{ID: "nf2", Source: "nb", Target: "ngw"},
							{ID: "nf3", Source: "ngw", Target: "nc"},
							{ID: "nf4", Source: "ngw", Target: "nd"},
							{ID: "nf5", Source: "nc", Target: "ns-end"},
							{ID: "nf6", Source: "nd", Target: "ns-end"},
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
				require.ErrorIs(t, err, model.ErrMixedGateway)
			},
		},
		"unpaired parallel join nested inside subprocess propagates ErrUnpairedJoin": {
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "sp", Kind: model.KindSubProcess, Subprocess: &model.ProcessDefinition{
						ID:      "inner-unpaired",
						Version: 1,
						Nodes: []model.Node{
							{ID: "ns-start", Kind: model.KindStartEvent},
							{ID: "nsplit", Kind: model.KindExclusiveGateway},
							{ID: "na", Kind: model.KindServiceTask, Action: "na"},
							{ID: "nb", Kind: model.KindServiceTask, Action: "nb"},
							{ID: "nj", Kind: model.KindParallelGateway}, // parallel join fed by exclusive split
							{ID: "ns-end", Kind: model.KindEndEvent},
						},
						Flows: []model.SequenceFlow{
							{ID: "nf0", Source: "ns-start", Target: "nsplit"},
							{ID: "nf1", Source: "nsplit", Target: "na"},
							{ID: "nf2", Source: "nsplit", Target: "nb"},
							{ID: "nf3", Source: "na", Target: "nj"},
							{ID: "nf4", Source: "nb", Target: "nj"},
							{ID: "nf5", Source: "nj", Target: "ns-end"},
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
				require.ErrorIs(t, err, model.ErrUnpairedJoin)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, model.Validate(tc.def))
		})
	}
}

// TestValidateRejectsBadRetryPolicy checks that Validate returns
// ErrInvalidRetryPolicy when a node carries a RetryPolicy whose fields violate
// the documented invariants (here: BackoffCoef below 1.0 with a positive
// InitialInterval).
func TestValidateRejectsBadRetryPolicy(t *testing.T) {
	bad := -1.0 // BackoffCoef below 1.0 with a positive interval is invalid
	def := &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "task", Kind: model.KindServiceTask, Action: "a",
				RetryPolicy: &model.RetryPolicy{InitialInterval: time.Second, BackoffCoef: bad}},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
	err := model.Validate(def)
	require.ErrorIs(t, err, model.ErrInvalidRetryPolicy)
}

// TestValidateRejectsRecoveryFlowNotFromNode checks that Validate returns
// ErrInvalidRecoveryFlow when a node's RecoveryFlow names a flow ID that does
// not exist or whose Source is not the node itself.
func TestValidateRejectsRecoveryFlowNotFromNode(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "task", Kind: model.KindServiceTask, Action: "a", RecoveryFlow: "nope"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
	err := model.Validate(def)
	require.ErrorIs(t, err, model.ErrInvalidRecoveryFlow)
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

func TestValidateCancelActions(t *testing.T) {
	base := func(cancel []string) *model.ProcessDefinition {
		return &model.ProcessDefinition{
			ID: "d", Version: 1,
			Nodes: []model.Node{
				{ID: "start", Kind: model.KindStartEvent},
				{ID: "end", Kind: model.KindEndEvent},
			},
			Flows:         []model.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
			CancelActions: cancel,
		}
	}
	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "nil cancel actions is valid",
			def:  base(nil),
			assert: func(t *testing.T, err error) { require.NoError(t, err) },
		},
		{
			name: "non-empty cancel action names are valid",
			def:  base([]string{"notify", "refund"}),
			assert: func(t *testing.T, err error) { require.NoError(t, err) },
		},
		{
			name: "empty cancel action name is rejected",
			def:  base([]string{"notify", ""}),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrEmptyCancelAction)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.assert(t, model.Validate(tc.def)) })
	}
}

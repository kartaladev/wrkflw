package definition_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition"
)

func TestValidate(t *testing.T) {
	tests := map[string]struct {
		def    *definition.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		"valid linear": {
			def: linearDef(),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"no start event": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{definition.NewEndEvent("end")},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrNoStartEvent)
			},
		},
		"multiple start events": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("s1"),
					definition.NewStartEvent("s2"),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "s1", Target: "end"},
					{ID: "f2", Source: "s2", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrMultipleStartEvents)
			},
		},
		"dangling flow target": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "ghost"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrDanglingFlow)
			},
		},
		"dead end non-end node": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewServiceTask("task", definition.WithActionName("x")),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					// task has no outgoing → dead end
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrDeadEnd)
			},
		},
		"start has incoming": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewServiceTask("task", definition.WithActionName("x")),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "start"}, // illegal: loops back to start
					{ID: "f3", Source: "task", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrStartHasIncoming)
			},
		},
		"end has outgoing": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewEndEvent("end"),
					definition.NewServiceTask("task", definition.WithActionName("x")),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "end"},
					{ID: "f2", Source: "end", Target: "task"}, // illegal: end has outgoing
					{ID: "f3", Source: "task", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrEndHasOutgoing)
			},
		},
		"dangling flow source": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "ghost", Target: "end"}, // source node missing
					{ID: "f2", Source: "start", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrDanglingFlow)
			},
		},
		"condition on parallel gateway outgoing": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewParallelGateway("fork"),
					definition.NewServiceTask("a", definition.WithActionName("a")),
					definition.NewServiceTask("b", definition.WithActionName("b")),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "fork"},
					{ID: "f2", Source: "fork", Target: "a", Condition: "x > 1"}, // illegal
					{ID: "f3", Source: "fork", Target: "b"},
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrConditionNotAllowed)
			},
		},
		"default on parallel gateway outgoing": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewParallelGateway("fork"),
					definition.NewServiceTask("a", definition.WithActionName("a")),
					definition.NewServiceTask("b", definition.WithActionName("b")),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "fork"},
					{ID: "f2", Source: "fork", Target: "a", IsDefault: true}, // illegal
					{ID: "f3", Source: "fork", Target: "b"},
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrDefaultNotAllowed)
			},
		},
		"multiple defaults on exclusive gateway": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewExclusiveGateway("xor"),
					definition.NewServiceTask("a", definition.WithActionName("a")),
					definition.NewServiceTask("b", definition.WithActionName("b")),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "xor"},
					{ID: "f2", Source: "xor", Target: "a", IsDefault: true},
					{ID: "f3", Source: "xor", Target: "b", IsDefault: true}, // illegal: two defaults
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrMultipleDefaults)
			},
		},
		// Event-based gateway rules
		"valid event-based gateway targeting catch events": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewEventBasedGateway("ebg"),
					definition.NewIntermediateCatchEvent("sig-catch", definition.WithSignalName("sig.a")),
					definition.NewIntermediateCatchEvent("msg-catch", definition.WithMessageNameAndKey("msg.b", "")),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
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
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewEventBasedGateway("ebg"),
					definition.NewIntermediateCatchEvent("sig-catch", definition.WithSignalName("sig.a")),
					definition.NewServiceTask("task", definition.WithActionName("do-work")), // non-catch
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "ebg"},
					{ID: "f2", Source: "ebg", Target: "sig-catch"},
					{ID: "f3", Source: "ebg", Target: "task"}, // illegal: non-catch target
					{ID: "f4", Source: "sig-catch", Target: "end"},
					{ID: "f5", Source: "task", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrEventGatewayTarget)
			},
		},
		// Boundary event attachment rules
		"valid boundary attached to service task": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewServiceTask("task", definition.WithActionName("do-work")),
					// NonInterrupting omitted (false) = interrupting, the default.
					definition.NewBoundaryEvent("boundary", "task", definition.WithBoundarySignal("cancel")),
					definition.NewEndEvent("end"),
					definition.NewEndEvent("cancel-end"),
				},
				Flows: []definition.SequenceFlow{
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
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewEndEvent("end"),
					definition.NewBoundaryEvent("boundary", "ghost", definition.WithBoundarySignal("cancel")),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "end"},
					{ID: "f2", Source: "boundary", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrBoundaryAttachment)
			},
		},
		"boundary attached to non-activity node": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewExclusiveGateway("xor"),
					definition.NewServiceTask("a", definition.WithActionName("a")),
					definition.NewServiceTask("b", definition.WithActionName("b")),
					definition.NewEndEvent("end"),
					// boundary attached to a gateway — not an activity
					definition.NewBoundaryEvent("boundary", "xor", definition.WithBoundarySignal("cancel")),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "xor"},
					{ID: "f2", Source: "xor", Target: "a", Condition: "x > 0"},
					{ID: "f3", Source: "xor", Target: "b", IsDefault: true},
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
					{ID: "f6", Source: "boundary", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrBoundaryAttachment)
			},
		},
		"valid exclusive gateway with condition and default": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewExclusiveGateway("xor"),
					definition.NewServiceTask("a", definition.WithActionName("a")),
					definition.NewServiceTask("b", definition.WithActionName("b")),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
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
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewServiceTask("a", definition.WithActionName("a")),
					definition.NewServiceTask("b", definition.WithActionName("b")),
					definition.NewExclusiveGateway("gw"),
					definition.NewServiceTask("c", definition.WithActionName("c")),
					definition.NewServiceTask("d", definition.WithActionName("d")),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f0", Source: "start", Target: "a"},
					{ID: "f0b", Source: "start", Target: "b"}, // start splits to a and b
					{ID: "f1", Source: "a", Target: "gw"},
					{ID: "f2", Source: "b", Target: "gw"}, // gw has 2 incoming
					{ID: "f3", Source: "gw", Target: "c"},
					{ID: "f4", Source: "gw", Target: "d"}, // gw has 2 outgoing → mixed
					{ID: "f5", Source: "c", Target: "end"},
					{ID: "f6", Source: "d", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrMixedGateway)
			},
		},
		"pure split gateway is valid": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewParallelGateway("gw"),
					definition.NewServiceTask("c", definition.WithActionName("c")),
					definition.NewServiceTask("d", definition.WithActionName("d")),
					definition.NewParallelGateway("j"),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
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
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewServiceTask("task", definition.WithActionName("t")),
					definition.NewServiceTask("orphan", definition.WithActionName("o")),
					definition.NewEndEvent("orphan-end"),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "end"},
					{ID: "f3", Source: "orphan", Target: "orphan-end"}, // orphan unreachable from start
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrUnreachableNode)
			},
		},
		"node reachable via boundary on reachable host is valid": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewServiceTask("task", definition.WithActionName("t")),
					definition.NewBoundaryEvent("bnd", "task", definition.WithBoundaryTimer("PT1M")),
					definition.NewServiceTask("handler", definition.WithActionName("h")),
					definition.NewEndEvent("hend"),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
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
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewServiceTask("task", definition.WithActionName("t")),
					definition.NewEndEvent("end"),
					definition.NewServiceTask("ghost", definition.WithActionName("g")), // unreachable host
					definition.NewBoundaryEvent("bnd", "ghost", definition.WithBoundaryTimer("PT1M")),
					definition.NewServiceTask("handler", definition.WithActionName("h")),
					definition.NewEndEvent("hend"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "end"},
					{ID: "f3", Source: "ghost", Target: "end"},
					{ID: "f4", Source: "bnd", Target: "handler"},
					{ID: "f5", Source: "handler", Target: "hend"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrUnreachableNode)
			},
		},
		"zero start events does not run reachability": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{definition.NewEndEvent("end")},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrNoStartEvent)
				require.NotErrorIs(t, err, definition.ErrUnreachableNode)
			},
		},
		"pure join gateway is valid": {
			// A parallel join needs a real parallel fork upstream: a start event
			// follows only its first outgoing flow (moveAlongSingleFlow), so
			// "start -> a, b" would never activate b and the join would deadlock.
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewParallelGateway("fork"),
					definition.NewServiceTask("a", definition.WithActionName("a")),
					definition.NewServiceTask("b", definition.WithActionName("b")),
					definition.NewParallelGateway("j"),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
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
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewExclusiveGateway("split"),
					definition.NewServiceTask("a", definition.WithActionName("a")),
					definition.NewServiceTask("b", definition.WithActionName("b")),
					definition.NewParallelGateway("j"),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f0", Source: "start", Target: "split"},
					{ID: "f1", Source: "split", Target: "a"},
					{ID: "f2", Source: "split", Target: "b"},
					{ID: "f3", Source: "a", Target: "j"},
					{ID: "f4", Source: "b", Target: "j"},
					{ID: "f5", Source: "j", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrUnpairedJoin)
			},
		},
		"parallel join fed by inclusive split is paired": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewInclusiveGateway("split"),
					definition.NewServiceTask("a", definition.WithActionName("a")),
					definition.NewServiceTask("b", definition.WithActionName("b")),
					definition.NewParallelGateway("j"),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
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
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("s1"),
					definition.NewStartEvent("s2"),
					definition.NewExclusiveGateway("split"),
					definition.NewServiceTask("a", definition.WithActionName("a")),
					definition.NewServiceTask("b", definition.WithActionName("b")),
					definition.NewParallelGateway("j"),
					definition.NewEndEvent("end"),
					definition.NewEndEvent("end2"),
				},
				Flows: []definition.SequenceFlow{
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
				require.ErrorIs(t, err, definition.ErrMultipleStartEvents)
				// Pairing is skipped when the start count is ill-defined, so the
				// otherwise-unpaired join is not reported on an already-invalid def.
				require.NotErrorIs(t, err, definition.ErrUnpairedJoin)
			},
		},
		"loop containing a properly forked parallel join is valid": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewExclusiveGateway("merge"), // loop-back merge (pure join)
					definition.NewParallelGateway("fork"),
					definition.NewServiceTask("a", definition.WithActionName("a")),
					definition.NewServiceTask("b", definition.WithActionName("b")),
					definition.NewParallelGateway("j"),
					definition.NewExclusiveGateway("loop"), // loop-back decision (pure split)
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
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
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewServiceTask("task", definition.WithActionName("t")),
					definition.NewEndEvent("end"),
					// Disconnected component: an exclusive split feeding a parallel join
					// (would be ErrUnpairedJoin if reachable) — but it is unreachable.
					definition.NewExclusiveGateway("osplit"),
					definition.NewServiceTask("ox", definition.WithActionName("x")),
					definition.NewServiceTask("oy", definition.WithActionName("y")),
					definition.NewParallelGateway("oj"),
					definition.NewEndEvent("oend"),
				},
				Flows: []definition.SequenceFlow{
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
				require.ErrorIs(t, err, definition.ErrUnreachableNode)
				require.NotErrorIs(t, err, definition.ErrUnpairedJoin) // unreachable join is skipped
			},
		},
		"inclusive join fed by exclusive split is not flagged (rule is parallel-only)": {
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewExclusiveGateway("split"),
					definition.NewServiceTask("a", definition.WithActionName("a")),
					definition.NewServiceTask("b", definition.WithActionName("b")),
					definition.NewInclusiveGateway("j"),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f0", Source: "start", Target: "split"},
					{ID: "f1", Source: "split", Target: "a"},
					{ID: "f2", Source: "split", Target: "b"},
					{ID: "f3", Source: "a", Target: "j"},
					{ID: "f4", Source: "b", Target: "j"},
					{ID: "f5", Source: "j", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, definition.ErrUnpairedJoin)
			},
		},
		// CompensateRef validation rules
		"compensation throw with dangling CompensateRef is rejected": {
			// KindIntermediateThrowEvent with CompensateRef pointing to a non-existent node.
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewServiceTask("task", definition.WithActionName("do-work")),
					definition.NewIntermediateThrowEvent("comp-throw", definition.WithCompensateRef("missing-node")),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "comp-throw"},
					{ID: "f3", Source: "comp-throw", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrCompensateRefNotFound)
			},
		},
		"compensation throw with valid CompensateRef is accepted": {
			// KindIntermediateThrowEvent with CompensateRef pointing to a real node.
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewServiceTask("task", definition.WithActionName("do-work"), definition.WithCompensation("undo-work")),
					definition.NewIntermediateThrowEvent("comp-throw", definition.WithCompensateRef("task")),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "comp-throw"},
					{ID: "f3", Source: "comp-throw", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"normal intermediate throw event with no CompensateRef is unaffected": {
			// KindIntermediateThrowEvent with empty CompensateRef (a normal signal throw)
			// must not trigger ErrCompensateRefNotFound.
			def: &definition.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewIntermediateThrowEvent("throw", definition.WithThrowSignal("sig.done")),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "throw"},
					{ID: "f2", Source: "throw", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err, "a normal throw with no CompensateRef must validate clean")
			},
		},
		"dangling CompensateRef inside a sub-process is rejected (recursion)": {
			// The CompensateRef rule lives in the recursive validate(), so a dangling
			// ref inside a nested sub-process definition must also be caught.
			def: &definition.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewSubProcess("sp", &definition.ProcessDefinition{
						ID: "inner", Version: 1,
						Nodes: []definition.Node{
							definition.NewStartEvent("ns"),
							definition.NewIntermediateThrowEvent("nthrow", definition.WithCompensateRef("no-such")),
							definition.NewEndEvent("ne"),
						},
						Flows: []definition.SequenceFlow{
							{ID: "nf1", Source: "ns", Target: "nthrow"},
							{ID: "nf2", Source: "nthrow", Target: "ne"},
						},
					}),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrCompensateRefNotFound)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, definition.Validate(tc.def))
		})
	}
}

// validSubprocessDef returns a well-formed embedded subprocess definition
// (start → service task → end) for use in outer process tests.
func validSubprocessDef(id string) *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID:      id,
		Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("ns-start"),
			definition.NewServiceTask("ns-task", definition.WithActionName("inner")),
			definition.NewEndEvent("ns-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "nf1", Source: "ns-start", Target: "ns-task"},
			{ID: "nf2", Source: "ns-task", Target: "ns-end"},
		},
	}
}

func TestValidateSubProcess(t *testing.T) {
	tests := map[string]struct {
		def    *definition.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		"valid subprocess with valid nested definition": {
			def: &definition.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewSubProcess("sp", validSubprocessDef("inner")),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"subprocess with nil Subprocess pointer is invalid": {
			def: &definition.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewSubProcess("sp", nil),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrMissingSubprocess)
			},
		},
		"event-subprocess with nil Subprocess pointer is invalid": {
			def: &definition.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewEventSubProcess("esp", nil),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "esp"},
					{ID: "f2", Source: "esp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrMissingSubprocess)
			},
		},
		"subprocess whose nested definition is malformed (start-has-incoming) propagates error": {
			def: &definition.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewSubProcess("sp", &definition.ProcessDefinition{
						ID:      "bad-inner",
						Version: 1,
						Nodes: []definition.Node{
							definition.NewStartEvent("ns-start"),
							definition.NewServiceTask("ns-task", definition.WithActionName("inner")),
							definition.NewEndEvent("ns-end"),
						},
						Flows: []definition.SequenceFlow{
							{ID: "nf1", Source: "ns-start", Target: "ns-task"},
							{ID: "nf2", Source: "ns-task", Target: "ns-end"},
							// illegal: flow into the start event
							{ID: "nf3", Source: "ns-task", Target: "ns-start"},
						},
					}),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				// The nested error is propagated and is unwrappable.
				require.ErrorIs(t, err, definition.ErrStartHasIncoming)
			},
		},
		"subprocess whose nested definition is malformed (dangling flow) propagates error": {
			def: &definition.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewSubProcess("sp", &definition.ProcessDefinition{
						ID:      "bad-inner-2",
						Version: 1,
						Nodes: []definition.Node{
							definition.NewStartEvent("ns-start"),
							definition.NewEndEvent("ns-end"),
						},
						Flows: []definition.SequenceFlow{
							{ID: "nf1", Source: "ns-start", Target: "ghost-node"}, // dangling
						},
					}),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrDanglingFlow)
			},
		},
		"call-activity with non-empty DefRef is valid": {
			def: &definition.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewCallActivity("ca", "some-external-process"),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "ca"},
					{ID: "f2", Source: "ca", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"call-activity with empty DefRef is invalid": {
			def: &definition.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewCallActivity("ca", ""),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "ca"},
					{ID: "f2", Source: "ca", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrMissingDefRef)
			},
		},
		"mixed gateway nested inside subprocess propagates ErrMixedGateway": {
			def: &definition.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewSubProcess("sp", &definition.ProcessDefinition{
						ID:      "inner-mixed",
						Version: 1,
						Nodes: []definition.Node{
							definition.NewStartEvent("ns-start"),
							definition.NewServiceTask("na", definition.WithActionName("na")),
							definition.NewServiceTask("nb", definition.WithActionName("nb")),
							definition.NewParallelGateway("ngw"),
							definition.NewServiceTask("nc", definition.WithActionName("nc")),
							definition.NewServiceTask("nd", definition.WithActionName("nd")),
							definition.NewEndEvent("ns-end"),
						},
						Flows: []definition.SequenceFlow{
							{ID: "nf0", Source: "ns-start", Target: "na"},
							{ID: "nf0b", Source: "ns-start", Target: "nb"},
							{ID: "nf1", Source: "na", Target: "ngw"},
							{ID: "nf2", Source: "nb", Target: "ngw"},
							{ID: "nf3", Source: "ngw", Target: "nc"},
							{ID: "nf4", Source: "ngw", Target: "nd"},
							{ID: "nf5", Source: "nc", Target: "ns-end"},
							{ID: "nf6", Source: "nd", Target: "ns-end"},
						},
					}),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrMixedGateway)
			},
		},
		"unpaired parallel join nested inside subprocess propagates ErrUnpairedJoin": {
			def: &definition.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []definition.Node{
					definition.NewStartEvent("start"),
					definition.NewSubProcess("sp", &definition.ProcessDefinition{
						ID:      "inner-unpaired",
						Version: 1,
						Nodes: []definition.Node{
							definition.NewStartEvent("ns-start"),
							definition.NewExclusiveGateway("nsplit"),
							definition.NewServiceTask("na", definition.WithActionName("na")),
							definition.NewServiceTask("nb", definition.WithActionName("nb")),
							definition.NewParallelGateway("nj"), // parallel join fed by exclusive split
							definition.NewEndEvent("ns-end"),
						},
						Flows: []definition.SequenceFlow{
							{ID: "nf0", Source: "ns-start", Target: "nsplit"},
							{ID: "nf1", Source: "nsplit", Target: "na"},
							{ID: "nf2", Source: "nsplit", Target: "nb"},
							{ID: "nf3", Source: "na", Target: "nj"},
							{ID: "nf4", Source: "nb", Target: "nj"},
							{ID: "nf5", Source: "nj", Target: "ns-end"},
						},
					}),
					definition.NewEndEvent("end"),
				},
				Flows: []definition.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrUnpairedJoin)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, definition.Validate(tc.def))
		})
	}
}

// TestValidateRejectsBadRetryPolicy checks that Validate returns
// ErrInvalidRetryPolicy when a node carries a RetryPolicy whose fields violate
// the documented invariants (here: BackoffCoef below 1.0 with a positive
// InitialInterval).
func TestValidateRejectsBadRetryPolicy(t *testing.T) {
	bad := -1.0 // BackoffCoef below 1.0 with a positive interval is invalid
	def := &definition.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewServiceTask("task", definition.WithActionName("a"),
				definition.WithRetryPolicy(&definition.RetryPolicy{InitialInterval: time.Second, BackoffCoef: bad}),
			),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
	err := definition.Validate(def)
	require.ErrorIs(t, err, definition.ErrInvalidRetryPolicy)
}

// TestValidateRejectsRecoveryFlowNotFromNode checks that Validate returns
// ErrInvalidRecoveryFlow when a node's RecoveryFlow names a flow ID that does
// not exist or whose Source is not the node itself.
func TestValidateRejectsRecoveryFlowNotFromNode(t *testing.T) {
	def := &definition.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewServiceTask("task", definition.WithActionName("a"), definition.WithRecoveryFlow("nope")),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
	err := definition.Validate(def)
	require.ErrorIs(t, err, definition.ErrInvalidRecoveryFlow)
}

// TestValidateCyclicSubprocessDoesNotPanic verifies that Validate does not
// stack-overflow on a hand-constructed cyclic subprocess pointer graph (A→B→A).
func TestValidateCyclicSubprocessDoesNotPanic(t *testing.T) {
	defA := &definition.ProcessDefinition{
		ID: "cyclic-a", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("a-start"),
			definition.NewSubProcess("a-sub", nil), // nil will be replaced below
			definition.NewEndEvent("a-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "af1", Source: "a-start", Target: "a-sub"},
			{ID: "af2", Source: "a-sub", Target: "a-end"},
		},
	}
	defB := &definition.ProcessDefinition{
		ID: "cyclic-b", Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("b-start"),
			definition.NewSubProcess("b-sub", nil), // nil will be replaced below
			definition.NewEndEvent("b-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "bf1", Source: "b-start", Target: "b-sub"},
			{ID: "bf2", Source: "b-sub", Target: "b-end"},
		},
	}
	// Wire the cycle: A's sub-process points to B, B's sub-process points back to A.
	// We must replace the nodes since they are value types.
	defA.Nodes[1] = definition.NewSubProcess("a-sub", defB)
	defB.Nodes[1] = definition.NewSubProcess("b-sub", defA)

	// Must not panic or stack-overflow.
	require.NotPanics(t, func() {
		_ = definition.Validate(defA)
	}, "Validate must not panic on cyclic subprocess graph")
}

func TestValidateCancelActions(t *testing.T) {
	base := func(cancel []string) *definition.ProcessDefinition {
		return &definition.ProcessDefinition{
			ID: "d", Version: 1,
			Nodes: []definition.Node{
				definition.NewStartEvent("start"),
				definition.NewEndEvent("end"),
			},
			Flows:         []definition.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
			CancelActions: cancel,
		}
	}
	cases := []struct {
		name   string
		def    *definition.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name:   "nil cancel actions is valid",
			def:    base(nil),
			assert: func(t *testing.T, err error) { require.NoError(t, err) },
		},
		{
			name:   "non-empty cancel action names are valid",
			def:    base([]string{"notify", "refund"}),
			assert: func(t *testing.T, err error) { require.NoError(t, err) },
		},
		{
			name: "empty cancel action name is rejected",
			def:  base([]string{"notify", ""}),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, definition.ErrEmptyCancelAction)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.assert(t, definition.Validate(tc.def)) })
	}
}

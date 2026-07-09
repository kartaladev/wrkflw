package model_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	vexpr "github.com/zakyalvan/krtlwrkflw/definition/model/validate/expr"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
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
				Nodes: []model.Node{event.NewEnd("end")},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrNoStartEvent)
			},
		},
		"multiple start events": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("s1"),
					event.NewStart("s2"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("x")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("x")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					event.NewEnd("end"),
					activity.NewServiceTask("task", activity.WithTaskAction("x")),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					gateway.NewParallel("fork"),
					activity.NewServiceTask("a", activity.WithTaskAction("a")),
					activity.NewServiceTask("b", activity.WithTaskAction("b")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					gateway.NewParallel("fork"),
					activity.NewServiceTask("a", activity.WithTaskAction("a")),
					activity.NewServiceTask("b", activity.WithTaskAction("b")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					gateway.NewExclusive("xor"),
					activity.NewServiceTask("a", activity.WithTaskAction("a")),
					activity.NewServiceTask("b", activity.WithTaskAction("b")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					gateway.NewEventBased("ebg"),
					event.NewIntermediateCatch("sig-catch", event.WithCatchSignal("sig.a")),
					event.NewIntermediateCatch("msg-catch", event.WithCatchMessage("msg.b", "")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					gateway.NewEventBased("ebg"),
					event.NewIntermediateCatch("sig-catch", event.WithCatchSignal("sig.a")),
					activity.NewServiceTask("task", activity.WithTaskAction("do-work")), // non-catch
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("do-work")),
					// NonInterrupting omitted (false) = interrupting, the default.
					event.NewBoundary("boundary", "task", event.WithBoundarySignal("cancel")),
					event.NewEnd("end"),
					event.NewEnd("cancel-end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					event.NewEnd("end"),
					event.NewBoundary("boundary", "ghost", event.WithBoundarySignal("cancel")),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					gateway.NewExclusive("xor"),
					activity.NewServiceTask("a", activity.WithTaskAction("a")),
					activity.NewServiceTask("b", activity.WithTaskAction("b")),
					event.NewEnd("end"),
					// boundary attached to a gateway — not an activity
					event.NewBoundary("boundary", "xor", event.WithBoundarySignal("cancel")),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					gateway.NewExclusive("xor"),
					activity.NewServiceTask("a", activity.WithTaskAction("a")),
					activity.NewServiceTask("b", activity.WithTaskAction("b")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewServiceTask("a", activity.WithTaskAction("a")),
					activity.NewServiceTask("b", activity.WithTaskAction("b")),
					gateway.NewExclusive("gw"),
					activity.NewServiceTask("c", activity.WithTaskAction("c")),
					activity.NewServiceTask("d", activity.WithTaskAction("d")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
				require.ErrorIs(t, err, model.ErrMixedGateway)
			},
		},
		"pure split gateway is valid": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					gateway.NewParallel("gw"),
					activity.NewServiceTask("c", activity.WithTaskAction("c")),
					activity.NewServiceTask("d", activity.WithTaskAction("d")),
					gateway.NewParallel("j"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("t")),
					activity.NewServiceTask("orphan", activity.WithTaskAction("o")),
					event.NewEnd("orphan-end"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("t")),
					event.NewBoundary("bnd", "task", event.WithBoundaryTimer(schedule.AfterExpr("PT1M"))),
					activity.NewServiceTask("handler", activity.WithTaskAction("h")),
					event.NewEnd("hend"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
		"timer boundary on non-error host (UserTask) is valid": {
			// Regression: a timer boundary encodes its trigger in the nested
			// wire field (timerTrigger), not the legacy flat timerDuration.
			// The error-boundary classification must consult TimerTrigger, else
			// a timer boundary on a UserTask (a non-error-throwing host) is
			// misclassified as an error boundary and wrongly rejected.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewUserTask("approve", []string{"mgr"}),
					event.NewBoundary("bnd", "approve", event.WithBoundaryTimer(schedule.AfterExpr("PT1H"))),
					activity.NewServiceTask("handler", activity.WithTaskAction("h")),
					event.NewEnd("hend"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "approve"},
					{ID: "f2", Source: "approve", Target: "end"},
					{ID: "f3", Source: "bnd", Target: "handler"},
					{ID: "f4", Source: "handler", Target: "hend"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
				require.NotErrorIs(t, err, model.ErrBoundaryErrorHost)
			},
		},
		"node reachable only via boundary on unreachable host is unreachable": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("t")),
					event.NewEnd("end"),
					activity.NewServiceTask("ghost", activity.WithTaskAction("g")), // unreachable host
					event.NewBoundary("bnd", "ghost", event.WithBoundaryTimer(schedule.AfterExpr("PT1M"))),
					activity.NewServiceTask("handler", activity.WithTaskAction("h")),
					event.NewEnd("hend"),
				},
				Flows: []flow.SequenceFlow{
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
				Nodes: []model.Node{event.NewEnd("end")},
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
					event.NewStart("start"),
					gateway.NewParallel("fork"),
					activity.NewServiceTask("a", activity.WithTaskAction("a")),
					activity.NewServiceTask("b", activity.WithTaskAction("b")),
					gateway.NewParallel("j"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					gateway.NewExclusive("split"),
					activity.NewServiceTask("a", activity.WithTaskAction("a")),
					activity.NewServiceTask("b", activity.WithTaskAction("b")),
					gateway.NewParallel("j"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					gateway.NewInclusive("split"),
					activity.NewServiceTask("a", activity.WithTaskAction("a")),
					activity.NewServiceTask("b", activity.WithTaskAction("b")),
					gateway.NewParallel("j"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("s1"),
					event.NewStart("s2"),
					gateway.NewExclusive("split"),
					activity.NewServiceTask("a", activity.WithTaskAction("a")),
					activity.NewServiceTask("b", activity.WithTaskAction("b")),
					gateway.NewParallel("j"),
					event.NewEnd("end"),
					event.NewEnd("end2"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					gateway.NewExclusive("merge"), // loop-back merge (pure join)
					gateway.NewParallel("fork"),
					activity.NewServiceTask("a", activity.WithTaskAction("a")),
					activity.NewServiceTask("b", activity.WithTaskAction("b")),
					gateway.NewParallel("j"),
					gateway.NewExclusive("loop"), // loop-back decision (pure split)
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("t")),
					event.NewEnd("end"),
					// Disconnected component: an exclusive split feeding a parallel join
					// (would be ErrUnpairedJoin if reachable) — but it is unreachable.
					gateway.NewExclusive("osplit"),
					activity.NewServiceTask("ox", activity.WithTaskAction("x")),
					activity.NewServiceTask("oy", activity.WithTaskAction("y")),
					gateway.NewParallel("oj"),
					event.NewEnd("oend"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					gateway.NewExclusive("split"),
					activity.NewServiceTask("a", activity.WithTaskAction("a")),
					activity.NewServiceTask("b", activity.WithTaskAction("b")),
					gateway.NewInclusive("j"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
		// CompensateRef validation rules
		"compensation throw with dangling CompensateRef is rejected": {
			// KindIntermediateThrowEvent with CompensateRef pointing to a non-existent node.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("do-work")),
					event.NewIntermediateThrow("comp-throw", event.WithCompensateRef("missing-node")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "comp-throw"},
					{ID: "f3", Source: "comp-throw", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrCompensateRefNotFound)
			},
		},
		"compensation throw with valid CompensateRef is accepted": {
			// KindIntermediateThrowEvent with CompensateRef pointing to a real node.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("do-work"), activity.WithCompensateAction("undo-work")),
					event.NewIntermediateThrow("comp-throw", event.WithCompensateRef("task")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					event.NewIntermediateThrow("throw", event.WithThrowSignal("sig.done")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewSubProcess("sp", &model.ProcessDefinition{
						ID: "inner", Version: 1,
						Nodes: []model.Node{
							event.NewStart("ns"),
							event.NewIntermediateThrow("nthrow", event.WithCompensateRef("no-such")),
							event.NewEnd("ne"),
						},
						Flows: []flow.SequenceFlow{
							{ID: "nf1", Source: "ns", Target: "nthrow"},
							{ID: "nf2", Source: "nthrow", Target: "ne"},
						},
					}),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrCompensateRefNotFound)
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
			event.NewStart("ns-start"),
			activity.NewServiceTask("ns-task", activity.WithTaskAction("inner")),
			event.NewEnd("ns-end"),
		},
		Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewSubProcess("sp", validSubprocessDef("inner")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewSubProcess("sp", nil),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					event.NewEventSubProcess("esp", nil),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewSubProcess("sp", &model.ProcessDefinition{
						ID:      "bad-inner",
						Version: 1,
						Nodes: []model.Node{
							event.NewStart("ns-start"),
							activity.NewServiceTask("ns-task", activity.WithTaskAction("inner")),
							event.NewEnd("ns-end"),
						},
						Flows: []flow.SequenceFlow{
							{ID: "nf1", Source: "ns-start", Target: "ns-task"},
							{ID: "nf2", Source: "ns-task", Target: "ns-end"},
							// illegal: flow into the start event
							{ID: "nf3", Source: "ns-task", Target: "ns-start"},
						},
					}),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewSubProcess("sp", &model.ProcessDefinition{
						ID:      "bad-inner-2",
						Version: 1,
						Nodes: []model.Node{
							event.NewStart("ns-start"),
							event.NewEnd("ns-end"),
						},
						Flows: []flow.SequenceFlow{
							{ID: "nf1", Source: "ns-start", Target: "ghost-node"}, // dangling
						},
					}),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewCallActivity("ca", model.Latest("some-external-process")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewCallActivity("ca", model.Qualifier{}),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewSubProcess("sp", &model.ProcessDefinition{
						ID:      "inner-mixed",
						Version: 1,
						Nodes: []model.Node{
							event.NewStart("ns-start"),
							activity.NewServiceTask("na", activity.WithTaskAction("na")),
							activity.NewServiceTask("nb", activity.WithTaskAction("nb")),
							gateway.NewParallel("ngw"),
							activity.NewServiceTask("nc", activity.WithTaskAction("nc")),
							activity.NewServiceTask("nd", activity.WithTaskAction("nd")),
							event.NewEnd("ns-end"),
						},
						Flows: []flow.SequenceFlow{
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
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
					event.NewStart("start"),
					activity.NewSubProcess("sp", &model.ProcessDefinition{
						ID:      "inner-unpaired",
						Version: 1,
						Nodes: []model.Node{
							event.NewStart("ns-start"),
							gateway.NewExclusive("nsplit"),
							activity.NewServiceTask("na", activity.WithTaskAction("na")),
							activity.NewServiceTask("nb", activity.WithTaskAction("nb")),
							gateway.NewParallel("nj"), // parallel join fed by exclusive split
							event.NewEnd("ns-end"),
						},
						Flows: []flow.SequenceFlow{
							{ID: "nf0", Source: "ns-start", Target: "nsplit"},
							{ID: "nf1", Source: "nsplit", Target: "na"},
							{ID: "nf2", Source: "nsplit", Target: "nb"},
							{ID: "nf3", Source: "na", Target: "nj"},
							{ID: "nf4", Source: "nb", Target: "nj"},
							{ID: "nf5", Source: "nj", Target: "ns-end"},
						},
					}),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
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
			event.NewStart("start"),
			activity.NewServiceTask("task", activity.WithTaskAction("a"),
				activity.WithRetryPolicy(&model.RetryPolicy{InitialInterval: time.Second, BackoffCoef: bad}),
			),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
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
			event.NewStart("start"),
			activity.NewServiceTask("task", activity.WithTaskAction("a"), activity.WithRecoveryFlow("nope")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
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
			event.NewStart("a-start"),
			activity.NewSubProcess("a-sub", nil), // nil will be replaced below
			event.NewEnd("a-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "af1", Source: "a-start", Target: "a-sub"},
			{ID: "af2", Source: "a-sub", Target: "a-end"},
		},
	}
	defB := &model.ProcessDefinition{
		ID: "cyclic-b", Version: 1,
		Nodes: []model.Node{
			event.NewStart("b-start"),
			activity.NewSubProcess("b-sub", nil), // nil will be replaced below
			event.NewEnd("b-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "bf1", Source: "b-start", Target: "b-sub"},
			{ID: "bf2", Source: "b-sub", Target: "b-end"},
		},
	}
	// Wire the cycle: A's sub-process points to B, B's sub-process points back to A.
	// We must replace the nodes since they are value types.
	defA.Nodes[1] = activity.NewSubProcess("a-sub", defB)
	defB.Nodes[1] = activity.NewSubProcess("b-sub", defA)

	// Must not panic or stack-overflow.
	require.NotPanics(t, func() {
		_ = model.Validate(defA)
	}, "Validate must not panic on cyclic subprocess graph")
}

// TestValidate_RejectsVersionBelow1 checks that Validate rejects a ROOT
// definition whose Version is below 1 (0 is reserved as the Qualifier "latest"
// resolution sentinel, so an authored definition must use a concrete version),
// while leaving a nested sub-process definition's Version unchecked — a nested
// SubProcess is not independently resolved by qualifier and may legitimately
// carry Version 0.
func TestValidate_RejectsVersionBelow1(t *testing.T) {
	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "root version 0 is rejected",
			def: &model.ProcessDefinition{
				ID: "p", Version: 0,
				Nodes: []model.Node{
					event.NewStart("start"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrInvalidVersion)
			},
		},
		{
			name: "root version negative is rejected",
			def: &model.ProcessDefinition{
				ID: "p", Version: -3,
				Nodes: []model.Node{
					event.NewStart("start"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrInvalidVersion)
			},
		},
		{
			name: "root version 1 has no version error",
			def:  linearDef(),
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrInvalidVersion)
			},
		},
		{
			// CRITICAL guard case: the root definition is Version 1 (valid), but it
			// embeds a SubProcess whose nested *ProcessDefinition has Version 0. The
			// guard must apply to the root only — a nested subprocess definition is
			// not independently resolved by qualifier and may legitimately be
			// Version 0 — so Validate must return NO ErrInvalidVersion here.
			name: "nested subprocess with Version 0 does not trigger the root-only guard",
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewSubProcess("sp", &model.ProcessDefinition{
						ID: "sub", Version: 0,
						Nodes: []model.Node{
							event.NewStart("ns-start"),
							event.NewEnd("ns-end"),
						},
						Flows: []flow.SequenceFlow{
							{ID: "nf1", Source: "ns-start", Target: "ns-end"},
						},
					}),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "sp"},
					{ID: "f2", Source: "sp", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrInvalidVersion)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, model.Validate(tc.def))
		})
	}
}

// TestValidate_RejectsRecurringDeadlineTrigger checks that Validate rejects a
// node whose WithWaitDeadline trigger is recurring (e.g. schedule.Every) — a
// deadline must fire at most once, since the deadline flow/action is only
// meaningful the first time it breaches. A one-shot trigger (AfterDuration)
// remains accepted.
func TestValidate_RejectsRecurringDeadlineTrigger(t *testing.T) {
	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "recurring deadline trigger (Every) is rejected",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewUserTask("review", []string{"reviewer"},
						activity.WithWaitDeadline(schedule.Every(24*time.Hour), "escalate")),
					event.NewEnd("end"),
					event.NewEnd("escalate"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "review"},
					{ID: "f2", Source: "review", Target: "end"},
					{ID: "escalate", Source: "review", Target: "escalate"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDeadlineTriggerRecurring)
			},
		},
		{
			name: "one-shot deadline trigger (AfterDuration) is accepted",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewUserTask("review", []string{"reviewer"},
						activity.WithWaitDeadline(schedule.AfterDuration(24*time.Hour), "escalate")),
					event.NewEnd("end"),
					event.NewEnd("escalate"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "review"},
					{ID: "f2", Source: "review", Target: "end"},
					{ID: "escalate", Source: "review", Target: "escalate"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrDeadlineTriggerRecurring)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, model.Validate(tc.def))
		})
	}
}

func TestValidateCancelActions(t *testing.T) {
	base := func(cancel []string) *model.ProcessDefinition {
		return &model.ProcessDefinition{
			ID: "d", Version: 1,
			Nodes: []model.Node{
				event.NewStart("start"),
				event.NewEnd("end"),
			},
			Flows:         []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
			CancelActions: cancel,
		}
	}
	cases := []struct {
		name   string
		def    *model.ProcessDefinition
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
				require.ErrorIs(t, err, model.ErrEmptyCancelAction)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.assert(t, model.Validate(tc.def)) })
	}
}

// TestValidate_RejectsPayloadValidationOnNonMessageCatch proves the fail-closed
// authoring rule: an IntermediateCatchEvent that declares payload validation but
// is NOT a message catch is rejected, because signal/timer-delivered payloads are
// never validated at runtime (signals are broadcast — no single validatable
// target). A message catch with payload validation is allowed, and a ReceiveTask
// with payload validation is unaffected (the rule is scoped to catch events).
func TestValidate_RejectsPayloadValidationOnNonMessageCatch(t *testing.T) {
	t.Parallel()

	// catchDef wraps a single IntermediateCatchEvent between start and end.
	catchDef := func(catch model.Node) *model.ProcessDefinition {
		return &model.ProcessDefinition{
			ID: "catch-validation", Version: 1,
			Nodes: []model.Node{
				event.NewStart("start"),
				catch,
				event.NewEnd("end"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "f1", Source: "start", Target: "catch"},
				{ID: "f2", Source: "catch", Target: "end"},
			},
		}
	}
	// recvDef wraps a single ReceiveTask between start and end.
	recvDef := func(recv model.Node) *model.ProcessDefinition {
		return &model.ProcessDefinition{
			ID: "recv-validation", Version: 1,
			Nodes: []model.Node{
				event.NewStart("start"),
				recv,
				event.NewEnd("end"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "f1", Source: "start", Target: "recv"},
				{ID: "f2", Source: "recv", Target: "end"},
			},
		}
	}

	payload := vexpr.New("ok == true")

	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "signal catch + payload validation is rejected",
			def: catchDef(event.NewIntermediateCatch("catch",
				event.WithCatchSignal("go"), event.WithPayloadValidation(payload))),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrPayloadValidationRequiresMessage)
			},
		},
		{
			name: "timer catch + payload validation is rejected",
			def: catchDef(event.NewIntermediateCatch("catch",
				event.WithCatchTimer(schedule.AfterDuration(time.Hour)), event.WithPayloadValidation(payload))),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrPayloadValidationRequiresMessage)
			},
		},
		{
			name: "message catch + payload validation is allowed",
			def: catchDef(event.NewIntermediateCatch("catch",
				event.WithCatchMessage("msg", ""), event.WithPayloadValidation(payload))),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "receive task + payload validation is unaffected",
			def: recvDef(activity.NewReceiveTask("recv", "msg",
				activity.WithPayloadValidation(payload))),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, model.Validate(tc.def))
		})
	}
}

package model_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	vexpr "github.com/kartaladev/wrkflw/definition/model/validate/expr"
	"github.com/kartaladev/wrkflw/definition/schedule"
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
		"multiple manual start events": {
			// ADR-0121: multiple start events are legal, but at most one may be a
			// trigger-less "none" start — a second one is rejected.
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
				require.ErrorIs(t, err, model.ErrMultipleManualStarts)
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
					event.NewIntermediateCatch("sig-catch", event.WithSignalName("sig.a")),
					event.NewIntermediateCatch("msg-catch", event.WithMessageCorrelator("msg.b", "")),
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
					event.NewIntermediateCatch("sig-catch", event.WithSignalName("sig.a")),
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
					event.NewBoundary("boundary", "task", event.WithSignalName("cancel")),
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
					event.NewBoundary("boundary", "ghost", event.WithSignalName("cancel")),
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
					event.NewBoundary("boundary", "xor", event.WithSignalName("cancel")),
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
					activity.NewUserTask("approve", activity.WithEligibleRoles("mgr")),
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
		"multiple starts still runs pairing (reachability well-defined via union)": {
			// ADR-0121: reachability/pairing are computed over the union of all
			// starts, so they run (and can flag real defects) even when the start
			// configuration itself is separately invalid (two manual-starts here).
			// The join "j" is genuinely unpaired regardless of start count: its
			// only upstream split ("split") is exclusive, not a concurrency
			// source, so it deadlocks at runtime waiting for a second token.
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
				require.ErrorIs(t, err, model.ErrMultipleManualStarts)
				require.ErrorIs(t, err, model.ErrUnpairedJoin)
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
		"normal intermediate throw event is unaffected": {
			// KindIntermediateThrowEvent does not carry CompensateRef (ADR-0120);
			// a normal signal throw must not trigger ErrCompensateRefNotFound.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					event.NewIntermediateThrow("throw", event.WithThrowSignalName("sig.done")),
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
		"compensation throw event with dangling CompensateRef is rejected": {
			// KindCompensationThrowEvent (ADR-0120) with CompensateRef pointing to a
			// non-existent node.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("do-work")),
					event.NewCompensateThrow("comp-throw", event.WithCompensateRef("no-such")),
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
		"compensation throw event with valid CompensateRef is accepted": {
			// KindCompensationThrowEvent (ADR-0120) with CompensateRef pointing to a
			// real node.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("do-work"), activity.WithCompensateAction("undo-work")),
					event.NewCompensateThrow("comp-throw", event.WithCompensateRef("task")),
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
		"scope-wide compensation throw event with empty CompensateRef is accepted": {
			// KindCompensationThrowEvent (ADR-0120) with empty CompensateRef (scope-wide)
			// must not trigger ErrCompensateRefNotFound.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					event.NewCompensateThrow("comp-throw"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "comp-throw"},
					{ID: "f2", Source: "comp-throw", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err, "a scope-wide compensation throw with no CompensateRef must validate clean")
			},
		},
		"targeted compensation throw with ScopeLocal is rejected (nonsensical combination)": {
			// ADR-0120 review C2: WithScopeLocalCompensation only applies to the
			// scope-wide (empty CompensateRef) branch; combining it with a targeted
			// CompensateRef is a silent no-op at runtime, so it is rejected at
			// authoring time.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("do-work"), activity.WithCompensateAction("undo-work")),
					event.NewCompensateThrow("comp-throw", event.WithCompensateRef("task"), event.WithScopeLocalCompensation()),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "comp-throw"},
					{ID: "f3", Source: "comp-throw", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrScopeLocalWithCompensateRef)
			},
		},
		"scope-wide compensation throw with ScopeLocal is accepted": {
			// ScopeLocal is meaningful only on a scope-wide (empty CompensateRef)
			// throw — it must validate clean.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					event.NewCompensateThrow("comp-throw", event.WithScopeLocalCompensation()),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "comp-throw"},
					{ID: "f2", Source: "comp-throw", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err, "ScopeLocal on a scope-wide throw is valid")
			},
		},
		"event-triggered SubProcess with no incoming flow is a reachability root": {
			// ADR-0122: a KindSubProcess whose nested definition has an
			// event-triggered (message, here) start is recognized as a
			// reachability root —
			// it must not be flagged ErrUnreachableNode despite having no
			// incoming sequence flow of its own.
			def: eventTriggeredSubprocessRootDef(),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"event-triggered SubProcess with an incoming flow is rejected": {
			// ADR-0122 authoring guard: an event-triggered SubProcess must not
			// also carry an incoming sequence flow — that combination is
			// unmodelable (embedded vs. event sub-process semantics collide).
			def: eventTriggeredSubprocessOnFlowDef(),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrEventSubprocessOnFlow)
			},
		},
		"event-triggered SubProcess with an outgoing flow is rejected": {
			// ADR-0122 review guard: an event sub-process never traverses its own
			// sequence flows (it resumes via the enclosing scope), so an OUTGOING
			// flow is dead — and worse, the reachability seed forwardReachable
			// would follow it and wrongly mark the orphan target reachable,
			// letting it escape ErrUnreachableNode. Without this guard the def
			// below validates clean; with it, ErrEventSubprocessOnFlow fires.
			def: eventTriggeredSubprocessOutgoingFlowDef(),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrEventSubprocessOnFlow)
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
							event.NewCompensateThrow("nthrow", event.WithCompensateRef("no-such")),
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
		"duplicate node ID is rejected": {
			// Node lookup is a first-wins linear scan (ProcessDefinition.Node), so a
			// second node sharing an ID is permanently unreachable while Outgoing /
			// Incoming still return the union of both nodes' flows — silent
			// misrouting rather than an error.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("charge", activity.WithTaskAction("charge-card")),
					activity.NewServiceTask("charge", activity.WithTaskAction("charge-wallet")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "charge"},
					{ID: "f2", Source: "charge", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDuplicateNodeID)
				assert.Contains(t, err.Error(), `"charge"`)
			},
		},
		"duplicate node ID inside a sub-process is rejected (recursion)": {
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewSubProcess("sp", &model.ProcessDefinition{
						ID: "inner", Version: 1,
						Nodes: []model.Node{
							event.NewStart("ns"),
							activity.NewServiceTask("dup", activity.WithTaskAction("a")),
							activity.NewServiceTask("dup", activity.WithTaskAction("b")),
							event.NewEnd("ne"),
						},
						Flows: []flow.SequenceFlow{
							{ID: "nf1", Source: "ns", Target: "dup"},
							{ID: "nf2", Source: "dup", Target: "ne"},
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
				require.ErrorIs(t, err, model.ErrDuplicateNodeID)
			},
		},
		"an ID reused across the outer and a nested definition is accepted": {
			// IDs are unique per definition, not globally: every lookup
			// (Node/Outgoing/Incoming) is scoped to one ProcessDefinition, so a
			// nested definition legitimately reuses the outer definition's IDs.
			def: &model.ProcessDefinition{
				ID: "outer", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewSubProcess("sp", &model.ProcessDefinition{
						ID: "inner", Version: 1,
						Nodes: []model.Node{
							event.NewStart("start"),
							activity.NewServiceTask("task", activity.WithTaskAction("a")),
							event.NewEnd("end"),
						},
						Flows: []flow.SequenceFlow{
							{ID: "f1", Source: "start", Target: "task"},
							{ID: "f2", Source: "task", Target: "end"},
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
				require.NoError(t, err)
			},
		},
		"duplicate flow ID is rejected": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("a")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f1", Source: "task", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDuplicateFlowID)
				assert.Contains(t, err.Error(), `"f1"`)
			},
		},
		"blank flow IDs are not duplicates of each other": {
			// Flow IDs are optional — flow.SequenceFlow literals routinely omit
			// ID, and nothing looks a flow up by ID on the execution path. N blank
			// IDs must therefore not be reported as N-1 duplicates.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("a")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{Source: "start", Target: "task"},
					{Source: "task", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"a node and a flow sharing an ID are accepted": {
			// Node IDs and flow IDs are separate namespaces.
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "start", Source: "start", Target: "end"},
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

// catchDef wraps a single IntermediateCatchEvent, which must carry the node id
// "catch", between a start and an end event. Several rules in validate.go are
// exercised against exactly this shape, so it lives here rather than being
// rebuilt per test.
func catchDef(catch model.Node) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
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
					activity.NewUserTask("review", activity.WithEligibleRoles("reviewer"),
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
					activity.NewUserTask("review", activity.WithEligibleRoles("reviewer"),
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
				event.WithSignalName("go"), event.WithPayloadValidation(payload))),
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
				event.WithMessageCorrelator("msg", ""), event.WithPayloadValidation(payload))),
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

// TestValidate_RejectsCompletionActionOnUnsupportedKind checks that Validate
// rejects a node whose CompletionAction is set but whose kind does not honor
// it (only UserTask/ReceiveTask do — engine.completionActionOf silently
// ignores it on any other kind). The field lives on the shared ActivityFields
// embed, so it can be set on e.g. a ServiceTask via direct construction (or a
// hand-authored wire/YAML payload) even though no WithCompletionAction option
// targets that kind; Validate is the only place that catches it.
func TestValidate_RejectsCompletionActionOnUnsupportedKind(t *testing.T) {
	svcWithCompletion := activity.NewServiceTask("svc", activity.WithTaskAction("act")).(activity.ServiceTask)
	svcWithCompletion.CompletionAction = "notAllowed"

	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "ServiceTask with CompletionAction is rejected",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					svcWithCompletion,
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "svc"},
					{ID: "f2", Source: "svc", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrCompletionActionUnsupportedKind)
			},
		},
		{
			name: "UserTask with CompletionAction is accepted",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewUserTask("u1", activity.WithEligibleRoles("r"), activity.WithCompletionAction("recordApproval")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "u1"},
					{ID: "f2", Source: "u1", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrCompletionActionUnsupportedKind)
				require.NoError(t, err)
			},
		},
		{
			name: "ReceiveTask with CompletionAction is accepted",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewReceiveTask("r1", "m", activity.WithCompletionAction("ackOrder")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "r1"},
					{ID: "f2", Source: "r1", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrCompletionActionUnsupportedKind)
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

// TestValidate_RejectsDeadlineActionWithoutDeadline checks that Validate
// rejects a node whose DeadlineAction is set but whose DeadlineTimer is zero
// (i.e. WithDeadlineAction was used without WithWaitDeadline) — the action
// would never fire since no deadline timer is ever armed.
func TestValidate_RejectsDeadlineActionWithoutDeadline(t *testing.T) {
	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "DeadlineAction without WithWaitDeadline is rejected",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewUserTask("review", activity.WithEligibleRoles("reviewer"), activity.WithDeadlineAction("notify")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "review"},
					{ID: "f2", Source: "review", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDeadlineActionWithoutDeadline)
			},
		},
		{
			name: "WithWaitDeadline + WithDeadlineAction together is accepted",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewUserTask("review", activity.WithEligibleRoles("reviewer"),
						activity.WithWaitDeadline(schedule.AfterDuration(24*time.Hour), "escalate"),
						activity.WithDeadlineAction("notify")),
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
				require.NotErrorIs(t, err, model.ErrDeadlineActionWithoutDeadline)
				require.NoError(t, err)
			},
		},
		{
			name: "WithWaitDeadline alone (no action) is accepted",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewUserTask("review", activity.WithEligibleRoles("reviewer"),
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
				require.NotErrorIs(t, err, model.ErrDeadlineActionWithoutDeadline)
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

// TestValidate_RejectsCompensateActionWithoutForwardAction checks that Validate
// rejects a UserTask/ReceiveTask whose CompensateAction is set but whose
// CompletionAction is empty. For these two kinds the completion action IS the
// forward action (engine.handleActionCompleted records compensation only when a
// completion action runs), so a compensate action with no completion action can
// never produce a compensation record — dead config. ServiceTask and other
// activity kinds always have their own forward action (the task Action /
// sub-work) and are NOT gated by this rule.
func TestValidate_RejectsCompensateActionWithoutForwardAction(t *testing.T) {
	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "UserTask with CompensateAction but no CompletionAction is rejected",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewUserTask("u1", activity.WithEligibleRoles("r"), activity.WithCompensateAction("refund")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "u1"},
					{ID: "f2", Source: "u1", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrCompensateActionWithoutForwardAction)
			},
		},
		{
			name: "ReceiveTask with CompensateAction but no CompletionAction is rejected",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewReceiveTask("r1", "msg", activity.WithCompensateAction("refund")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "r1"},
					{ID: "f2", Source: "r1", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrCompensateActionWithoutForwardAction)
			},
		},
		{
			name: "UserTask with both CompletionAction and CompensateAction is accepted",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewUserTask("u1", activity.WithEligibleRoles("r"),
						activity.WithCompletionAction("recordApproval"),
						activity.WithCompensateAction("refund")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "u1"},
					{ID: "f2", Source: "u1", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrCompensateActionWithoutForwardAction)
				require.NoError(t, err)
			},
		},
		{
			name: "ServiceTask with CompensateAction and no CompletionAction is unaffected",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("svc", activity.WithTaskAction("charge-card"),
						activity.WithCompensateAction("refund-card")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "svc"},
					{ID: "f2", Source: "svc", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrCompensateActionWithoutForwardAction)
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

// TestValidateManualTaskRejectsCompletionValidation proves the fail-closed
// authoring rule from ADR-0118: a UserTask marked Manual (WithManual)
// completes on a bare trigger with no payload, so it must not also carry
// completion validation (WithCompletionValidation) — there is no input for the
// validation strategy to ever check. The guard requires BOTH Manual and a
// validation strategy, so the table pairs the reject case with two accept
// cases (manual-without-validation, non-manual-with-validation) — either
// would catch a `&&`→`||` or flipped-`Manual` mutation in the guard.
func TestValidateManualTaskRejectsCompletionValidation(t *testing.T) {
	def := func(opts ...activity.UserTaskOption) *model.ProcessDefinition {
		return &model.ProcessDefinition{
			ID: "p", Version: 1,
			Nodes: []model.Node{
				event.NewStart("start"),
				activity.NewUserTask("confirm", opts...),
				event.NewEnd("end"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "f1", Source: "start", Target: "confirm"},
				{ID: "f2", Source: "confirm", Target: "end"},
			},
		}
	}

	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "manual task with completion validation is rejected",
			def: def(
				activity.WithManual(false),
				activity.WithCompletionValidation(vexpr.New("ok == true")),
			),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrManualTaskValidation)
			},
		},
		{
			name: "manual task with no completion validation is accepted",
			def:  def(activity.WithManual(false)),
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrManualTaskValidation)
			},
		},
		{
			name: "non-manual task with completion validation is accepted",
			def: def(
				activity.WithCompletionValidation(vexpr.New("ok == true")),
			),
			assert: func(t *testing.T, err error) {
				require.NotErrorIs(t, err, model.ErrManualTaskValidation)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, model.Validate(tc.def))
		})
	}
}

// TestValidateStartEvents covers the ADR-0121 relaxation: a process
// definition may now have multiple start events (at most one trigger-less
// "none" start, plus any number of event-triggered starts each declaring
// exactly one trigger family), and reachability/pairing are computed over the
// union of all starts rather than requiring exactly one.
func TestValidateStartEvents(t *testing.T) {
	tests := map[string]struct {
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		"two manual starts rejected": {
			def: twoManualStartDef(),
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, model.ErrMultipleManualStarts)
			},
		},
		"one none + one message start allowed": {
			def:    noneAndMessageStartDef(),
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"two event starts allowed": {
			def:    signalAndTimerStartDef(),
			assert: func(t *testing.T, err error) { assert.NoError(t, err) },
		},
		"message start without name rejected": {
			def: messageStartMissingNameDef(),
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, model.ErrEventStartMissingTrigger)
			},
		},
		"start with signal and timer rejected": {
			def: signalPlusTimerOneNodeDef(),
			assert: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, model.ErrAmbiguousStartTrigger)
			},
		},
		"node reachable from a non-first start is not unreachable": {
			def: twoStartsBothReachDef(),
			assert: func(t *testing.T, err error) {
				assert.NotErrorIs(t, err, model.ErrUnreachableNode)
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, model.Validate(tc.def))
		})
	}
}

// twoManualStartDef has two trigger-less start events — always rejected
// (ErrMultipleManualStarts), regardless of how many event-triggered starts a
// definition also carries.
func twoManualStartDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
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
	}
}

// noneAndMessageStartDef has one manual-start plus one message-triggered
// start — legal under the ADR-0121 relaxation (at most one manual-start).
func noneAndMessageStartDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("s1"),
			event.NewStart("s2", event.WithMessageCorrelator("orderPlaced", "orderId")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "s1", Target: "end"},
			{ID: "f2", Source: "s2", Target: "end"},
		},
	}
}

// signalAndTimerStartDef has two event-triggered starts (signal + timer),
// zero manual-starts — legal.
func signalAndTimerStartDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("s1", event.WithSignalName("sig")),
			event.NewStart("s2", event.WithStartTimer(schedule.AfterDuration(time.Hour))),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "s1", Target: "end"},
			{ID: "f2", Source: "s2", Target: "end"},
		},
	}
}

// messageStartMissingNameDef declares a message-family correlation key
// without a message name — an incompletely-specified trigger family, not a
// manual-start (ErrEventStartMissingTrigger).
func messageStartMissingNameDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("s1", event.WithMessageCorrelator("", "someKey")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "s1", Target: "end"},
		},
	}
}

// signalPlusTimerOneNodeDef sets both a signal name and a timer on the same
// start event — two trigger families set (ErrAmbiguousStartTrigger).
func signalPlusTimerOneNodeDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("s1", event.WithSignalName("sig"), event.WithStartTimer(schedule.AfterDuration(time.Hour))),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "s1", Target: "end"},
		},
	}
}

// twoStartsBothReachDef has a manual-start reaching "end" directly and a
// message start reaching "end" via "task" — every node is reachable from the
// union of both starts, so none should be reported unreachable even though
// "task" is only reachable from the second (non-first) start.
func twoStartsBothReachDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("s1"),
			event.NewStart("s2", event.WithMessageCorrelator("orderPlaced", "orderId")),
			activity.NewServiceTask("task", activity.WithTaskAction("x")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "s1", Target: "end"},
			{ID: "f2", Source: "s2", Target: "task"},
			{ID: "f3", Source: "task", Target: "end"},
		},
	}
}

// eventTriggeredSubprocessRootDef returns a definition whose only
// flow-reachable nodes are the none-start chain (s -> work -> e), plus a
// KindSubProcess ("handleCancel") whose nested definition has a
// message-triggered start and NO incoming sequence flow of its own — it is a
// reachability root by virtue of its event-triggered inner start (ADR-0122).
func eventTriggeredSubprocessRootDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "esc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("onCancel", event.WithMessageCorrelator("cancel", "orderId")),
			activity.NewServiceTask("notify", activity.WithTaskAction("notify")),
			event.NewEnd("ie"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "onCancel", Target: "notify"},
			{ID: "if2", Source: "notify", Target: "ie"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("s"),
			activity.NewServiceTask("work", activity.WithTaskAction("work")),
			event.NewEnd("e"),
			activity.NewSubProcess("handleCancel", inner), // no incoming flow — event-triggered root
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "s", Target: "work"},
			{ID: "f2", Source: "work", Target: "e"},
		},
	}
}

// eventTriggeredSubprocessOnFlowDef is the ADR-0122 authoring-guard
// counter-case: a KindSubProcess whose nested start is event-triggered
// (signal, here) but which ALSO carries an incoming sequence flow. Mixing
// "embedded, flow-driven" and "event sub-process, trigger-driven" semantics
// on the same node is unmodelable, so it must be rejected.
func eventTriggeredSubprocessOnFlowDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "esc2", Version: 1,
		Nodes: []model.Node{
			event.NewStart("onCancel", event.WithSignalName("cancel.signal")),
			event.NewEnd("ie"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "onCancel", Target: "ie"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("s"),
			activity.NewSubProcess("handleCancel", inner),
			event.NewEnd("e"),
		},
		Flows: []flow.SequenceFlow{
			// illegal: an incoming flow into an event-triggered SubProcess.
			{ID: "f1", Source: "s", Target: "handleCancel"},
			{ID: "f2", Source: "handleCancel", Target: "e"},
		},
	}
}

// eventTriggeredSubprocessOutgoingFlowDef is the ADR-0122 review counter-case:
// a KindSubProcess whose nested start is event-triggered (signal, here) but
// which carries an OUTGOING sequence flow to a node ("orphan") that has no
// other way in. An event sub-process never traverses its own sequence flows,
// so the outgoing flow is dead; worse, the reachability seed forwardReachable
// would follow it and wrongly mark "orphan" reachable, letting it escape
// ErrUnreachableNode. It must be rejected with ErrEventSubprocessOnFlow.
func eventTriggeredSubprocessOutgoingFlowDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "esc3", Version: 1,
		Nodes: []model.Node{
			event.NewStart("onCancel", event.WithSignalName("cancel.signal")),
			event.NewEnd("ie"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "onCancel", Target: "ie"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("s"),
			activity.NewServiceTask("work", activity.WithTaskAction("work")),
			event.NewEnd("e"),
			activity.NewSubProcess("handleCancel", inner), // no incoming — event-triggered root
			event.NewEnd("orphan"),                        // reachable ONLY via handleCancel's illegal outgoing flow
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "s", Target: "work"},
			{ID: "f2", Source: "work", Target: "e"},
			// illegal: an outgoing flow FROM an event-triggered SubProcess.
			{ID: "f3", Source: "handleCancel", Target: "orphan"},
		},
	}
}

// TestValidateUserTaskOutcomes covers the structural rules on a UserTask's
// completion-outcome declaration (ADR-0146): the declared set must be usable
// (no blank or duplicate outcomes), an explicit outcome variable must be a
// valid expr identifier so a gateway condition can reference it, and a manual
// task — which completes on a bare trigger the engine fails closed on for any
// outcome — must not declare one.
func TestValidateUserTaskOutcomes(t *testing.T) {
	def := func(opts ...activity.UserTaskOption) *model.ProcessDefinition {
		return &model.ProcessDefinition{
			ID: "p", Version: 1,
			Nodes: []model.Node{
				event.NewStart("start"),
				activity.NewUserTask("review", opts...),
				event.NewEnd("end"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "f1", Source: "start", Target: "review"},
				{ID: "f2", Source: "review", Target: "end"},
			},
		}
	}

	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "declared outcomes with an explicit variable are accepted",
			def: def(
				activity.WithOutcomes("approve", "reject"),
				activity.WithOutcomeVariable("review_decision"),
			),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "no outcome declaration is accepted",
			def:  def(),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "blank outcome is rejected",
			def:  def(activity.WithOutcomes("approve", "  ")),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrEmptyOutcome)
			},
		},
		{
			name: "duplicate outcome is rejected",
			def:  def(activity.WithOutcomes("approve", "approve")),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDuplicateOutcome)
			},
		},
		{
			name: "outcome variable that is not an identifier is rejected",
			def:  def(activity.WithOutcomes("approve"), activity.WithOutcomeVariable("review decision")),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrInvalidOutcomeVariable)
			},
		},
		{
			name: "manual task declaring outcomes is rejected",
			def:  def(activity.WithManual(false), activity.WithOutcomes("approve")),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrManualTaskOutcome)
			},
		},
		{
			name: "manual task opting into outcome exposure is rejected",
			def:  def(activity.WithManual(true), activity.WithExposeOutcome()),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrManualTaskOutcome)
				assert.NotErrorIs(t, err, model.ErrOutcomeExposureWithoutOutcomes,
					"the manual rule is the precise diagnosis; do not also demand a set a manual task may not declare")
			},
		},
		{
			name: "outcome exposure without a declared set is rejected",
			def:  def(activity.WithExposeOutcome()),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrOutcomeExposureWithoutOutcomes)
			},
		},
		{
			name: "an outcome variable without a declared set is rejected",
			def:  def(activity.WithOutcomeVariable("review_decision")),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrOutcomeExposureWithoutOutcomes)
			},
		},
		{
			name: "outcome exposure with a declared set is accepted",
			def:  def(activity.WithOutcomes("approve", "reject"), activity.WithExposeOutcome()),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.assert(t, model.Validate(tc.def))
		})
	}
}

// TestValidateReceiveTaskMessageName covers ADR-0152: a ReceiveTask waits on a
// NAMED message — engine/step_nodes.go:97-99 assigns tok.AwaitMessage =
// rt.MessageName unconditionally, unlike the catch-event and boundary paths,
// which guard != "". A blank name (empty or whitespace-only) parks a token on
// AwaitMessage that no MessageReceived can ever resume once an empty identity
// key stops matching a record (ADR-0152's core guard), so the shape is
// rejected at authoring time rather than left to strand a token at runtime.
func TestValidateReceiveTaskMessageName(t *testing.T) {
	t.Parallel()

	// recvDef wraps a single ReceiveTask between start and end.
	recvDef := func(messageName string) *model.ProcessDefinition {
		return &model.ProcessDefinition{
			ID: "recv-name", Version: 1,
			Nodes: []model.Node{
				event.NewStart("start"),
				activity.NewReceiveTask("recv", messageName),
				event.NewEnd("end"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "f1", Source: "start", Target: "recv"},
				{ID: "f2", Source: "recv", Target: "end"},
			},
		}
	}

	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "a named receive task validates",
			def:  recvDef("order.paid"),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "an unnamed receive task is rejected",
			def:  recvDef(""),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrEmptyMessageName)
			},
		},
		{
			name: "a whitespace-only receive task message name is rejected",
			def:  recvDef("   "),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrEmptyMessageName)
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

// TestValidateBlankEventName covers ADR-0152's authoring-hygiene rule: a
// SignalName or MessageName that was WRITTEN but carries no visible character
// is rejected, while a name that is legitimately ABSENT ("") — the way a
// manual start or an error boundary says "no event trigger at all" — remains
// valid. This rule must reject the shape without disturbing the event-kind
// discriminators elsewhere in this file (:271-272 start-trigger family,
// :503 error-boundary detection, :701 event-subprocess detection), which key
// off a bare `!= ""` / `== ""` comparison and would silently RECLASSIFY a
// node (e.g. a boundary with SignalName " " turning into an error boundary)
// if ever converted to TrimSpace. The "legitimately absent name" case below
// is what pins that those discriminators were left untouched.
func TestValidateBlankEventName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "whitespace-only signal name on a catch event is rejected",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					event.NewIntermediateCatch("catch", event.WithSignalName("   ")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "catch"},
					{ID: "f2", Source: "catch", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrBlankEventName)
			},
		},
		{
			name: "whitespace-only message name on a non-ReceiveTask kind is rejected independently",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					event.NewIntermediateCatch("catch", event.WithMessageCorrelator("   ", "")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "catch"},
					{ID: "f2", Source: "catch", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrBlankEventName)
			},
		},
		{
			name: "both names whitespace-only on the same node reports once",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					event.NewIntermediateCatch("catch",
						event.WithSignalName("   "), event.WithMessageCorrelator("  ", "")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "catch"},
					{ID: "f2", Source: "catch", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrBlankEventName)
				require.Equal(t, 1, strings.Count(err.Error(), model.ErrBlankEventName.Error()),
					"a node with both names blank must contribute exactly one ErrBlankEventName")
			},
		},
		{
			name: "a legitimately absent name on an error boundary stays valid",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("task", activity.WithTaskAction("do-work")),
					// No options: an error boundary, both SignalName and
					// MessageName legitimately absent ("").
					event.NewBoundary("boundary", "task"),
					event.NewEnd("end"),
					event.NewEnd("boundary-end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					{ID: "f2", Source: "task", Target: "end"},
					{ID: "f3", Source: "boundary", Target: "boundary-end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "a normal named catch event stays valid",
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					event.NewIntermediateCatch("catch", event.WithSignalName("go")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "catch"},
					{ID: "f2", Source: "catch", Target: "end"},
				},
			},
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

// boundaryHostDef wraps host between a start and an end event and attaches
// boundary to it, giving the boundary its own escape route so the definition is
// otherwise structurally valid (a boundary with no outgoing flow is a dead end,
// and an unreachable escape target is its own violation).
func boundaryHostDef(host, boundary model.Node) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "boundary-host", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			host,
			boundary,
			activity.NewServiceTask("escalate", activity.WithTaskAction("escalate")),
			event.NewEnd("end"),
			event.NewEnd("end-escalated"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "host"},
			{ID: "f2", Source: "host", Target: "end"},
			{ID: "f3", Source: "bnd", Target: "escalate"},
			{ID: "f4", Source: "escalate", Target: "end-escalated"},
		},
	}
}

// TestValidate_RejectsTriggerBoundaryOnUnarmedHost pins the authoring-time half
// of the boundary no-op fix. A timer/signal/message boundary only ever fires
// because a strategy called armBoundaries at the host's park point, and only
// four strategies do: service task and business-rule task (via
// emitActionInvoke), receive task, and user task. SendTask, SubProcess and
// CallActivity were accepted here and then silently ignored by the engine, so
// the attachment is rejected instead — see ErrBoundaryTriggerHost for why each
// of the three cannot simply be armed.
//
// The ERROR flavour is unaffected: it reaches its host through the
// propagateError scan and the enclosing-scope walk, not through an arm, and
// keeps the wider errorBoundaryHostKinds host set.
func TestValidate_RejectsTriggerBoundaryOnUnarmedHost(t *testing.T) {
	t.Parallel()

	timer := event.WithBoundaryTimer(schedule.AfterDuration(time.Hour))
	signal := event.WithSignalName("cancel")
	message := event.WithMessageCorrelator("msg", "")

	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "timer boundary on a send task is rejected",
			def: boundaryHostDef(
				activity.NewSendTask("host", "notify"),
				event.NewBoundary("bnd", "host", timer)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrBoundaryTriggerHost)
			},
		},
		{
			name: "signal boundary on a sub-process is rejected",
			def: boundaryHostDef(
				activity.NewSubProcess("host", validSubprocessDef("inner")),
				event.NewBoundary("bnd", "host", signal)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrBoundaryTriggerHost)
			},
		},
		{
			name: "message boundary on a call activity is rejected",
			def: boundaryHostDef(
				activity.NewCallActivity("host", model.Latest("child")),
				event.NewBoundary("bnd", "host", message)),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrBoundaryTriggerHost)
			},
		},
		{
			name: "timer boundary on a service task stays valid",
			def: boundaryHostDef(
				activity.NewServiceTask("host", activity.WithTaskAction("work")),
				event.NewBoundary("bnd", "host", timer)),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "timer boundary on a business-rule task stays valid",
			def: boundaryHostDef(
				activity.NewBusinessRuleTask("host", activity.WithTaskAction("decide")),
				event.NewBoundary("bnd", "host", timer)),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "signal boundary on a receive task stays valid",
			def: boundaryHostDef(
				activity.NewReceiveTask("host", "msg"),
				event.NewBoundary("bnd", "host", signal)),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "message boundary on a user task stays valid",
			def: boundaryHostDef(
				activity.NewUserTask("host"),
				event.NewBoundary("bnd", "host", message)),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "error boundary on a sub-process stays valid",
			def: boundaryHostDef(
				activity.NewSubProcess("host", validSubprocessDef("inner")),
				event.NewBoundary("bnd", "host", event.WithBoundaryErrorCode("E1"))),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "error boundary on a call activity stays valid",
			def: boundaryHostDef(
				activity.NewCallActivity("host", model.Latest("child")),
				event.NewBoundary("bnd", "host", event.WithBoundaryErrorCode("E1"))),
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

// TestValidate_RejectsCatchEventWithoutTrigger pins the authoring-time half of
// the trigger-less catch fix. An IntermediateCatchEvent parks its token and is
// resumed only by the trigger it declared — a fired timer, a broadcast signal,
// or a correlated message. One that declares none is resumed by nothing: the
// engine parks the token and no trigger can ever match it, so the branch is
// dead for the life of the instance.
//
// It mirrors ErrEventStartMissingTrigger, which already refuses an
// incompletely-specified start for the same reason.
func TestValidate_RejectsCatchEventWithoutTrigger(t *testing.T) {
	t.Parallel()

	// The event-based-gateway case needs a shape catchDef cannot express, so it
	// builds its definition inline.
	cases := []struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		{
			name: "catch with no trigger at all is rejected",
			def:  catchDef(event.NewIntermediateCatch("catch")),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrCatchEventMissingTrigger)
			},
		},
		{
			name: "catch with only a correlation key is rejected",
			def:  catchDef(event.NewIntermediateCatch("catch", event.WithMessageCorrelator("", "orderId"))),
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrCatchEventMissingTrigger)
			},
		},
		{
			name: "trigger-less catch behind an event gateway is rejected too",
			def: &model.ProcessDefinition{
				ID: "evtgw-catch-trigger", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					gateway.NewEventBased("gw"),
					event.NewIntermediateCatch("catch-timer",
						event.WithCatchTimer(schedule.AfterDuration(time.Hour))),
					event.NewIntermediateCatch("catch-none"),
					event.NewEnd("end-timer"),
					event.NewEnd("end-none"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "gw"},
					{ID: "f2", Source: "gw", Target: "catch-timer"},
					{ID: "f3", Source: "gw", Target: "catch-none"},
					{ID: "f4", Source: "catch-timer", Target: "end-timer"},
					{ID: "f5", Source: "catch-none", Target: "end-none"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrCatchEventMissingTrigger)
			},
		},
		{
			name: "timer catch stays valid",
			def: catchDef(event.NewIntermediateCatch("catch",
				event.WithCatchTimer(schedule.AfterDuration(time.Hour)))),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "signal catch stays valid",
			def:  catchDef(event.NewIntermediateCatch("catch", event.WithSignalName("go"))),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name: "message catch stays valid",
			def:  catchDef(event.NewIntermediateCatch("catch", event.WithMessageCorrelator("msg", "orderId"))),
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

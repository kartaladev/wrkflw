package engine_test

// step_signal_fanout_test.go — ADR-0158: one broadcast signal fires EVERY
// matching arm per family, not the first.
//
// Measured on unpatched main (evidence D1): a parallel fork into two UserTasks,
// each with an interrupting signal boundary on the same name, given ONE
// SignalReceived, went tokens 2→1, Boundaries 2→1, and emitted exactly one
// UpdateTask — one host interrupted, the other left parked with its arm live.
// Through the public processtest harness the realistic Chain(PublishSignal,
// CompleteTasks) shape returned err=<nil> with the instance COMPLETED: a silent
// wrong answer.
//
// ⚠ The snapshot is load-bearing for TWO reasons, and the second is not about
// termination: it also confines the delivery to arms that existed at the DELIVERY
// INSTANT. Today a later tier fires an arm an earlier tier's own drive created —
// see TestSignalDoesNotFireAnArmThisDeliveryCreated.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

var fanoutT0 = time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

// twoInterruptingBoundariesDef is evidence D1's fixture, with each boundary
// routed to a ServiceTask so a fire is observable as an InvokeAction.
//
//	start → fork(parallel) ⇒
//	  taskA(user) [bndA interrupting, signal "escalate"] → endA
//	  taskB(user) [bndB interrupting, signal "escalate"] → endB
//	bndA → escA(service "escA-action") → endEscA
//	bndB → escB(service "escB-action") → endEscB
func twoInterruptingBoundariesDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-fanout-two-boundaries", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("taskA"),
			activity.NewUserTask("taskB"),
			event.NewBoundary("bndA", "taskA", event.WithSignalName("escalate")),
			event.NewBoundary("bndB", "taskB", event.WithSignalName("escalate")),
			activity.NewServiceTask("escA", activity.WithTaskAction("escA-action")),
			activity.NewServiceTask("escB", activity.WithTaskAction("escB-action")),
			event.NewEnd("endA"),
			event.NewEnd("endB"),
			event.NewEnd("endEscA"),
			event.NewEnd("endEscB"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-fork", Source: "start", Target: "fork"},
			{ID: "f-fork-a", Source: "fork", Target: "taskA"},
			{ID: "f-fork-b", Source: "fork", Target: "taskB"},
			{ID: "f-a-end", Source: "taskA", Target: "endA"},
			{ID: "f-b-end", Source: "taskB", Target: "endB"},
			{ID: "f-bnda-esca", Source: "bndA", Target: "escA"},
			{ID: "f-bndb-escb", Source: "bndB", Target: "escB"},
			{ID: "f-esca-end", Source: "escA", Target: "endEscA"},
			{ID: "f-escb-end", Source: "escB", Target: "endEscB"},
		},
	}
}

// TestSignalFiresEveryMatchingBoundaryArm is the headline defect (evidence D1).
//
// It asserts WHERE the tokens are and WHICH actions were invoked, not just a
// count: a count alone would pass if the delivery fired one arm twice.
func TestSignalFiresEveryMatchingBoundaryArm(t *testing.T) {
	t.Parallel()

	def := twoInterruptingBoundariesDef()
	require.NoError(t, model.Validate(def))

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(fanoutT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tokens, 2, "fixture control: both branches park")
	require.Len(t, r1.State.Boundaries, 2, "fixture control: BOTH arms are armed at the delivery instant")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(fanoutT0.Add(time.Second), "escalate", nil), engine.StepOptions{})
	require.NoError(t, err)

	nodes := make([]string, 0, len(r2.State.Tokens))
	for _, tok := range r2.State.Tokens {
		nodes = append(nodes, tok.NodeID)
	}
	assert.ElementsMatch(t, []string{"escA", "escB"}, nodes,
		"BOTH hosts must be interrupted and BOTH boundary targets hold a token")

	assert.Empty(t, r2.State.Boundaries, "both arms are consumed; neither may stay armed")

	var invoked, cancelled []string
	for _, c := range r2.Commands {
		switch cmd := c.(type) {
		case engine.InvokeAction:
			invoked = append(invoked, cmd.Name)
		case engine.UpdateTask:
			if cmd.Task.State == humantask.Cancelled {
				cancelled = append(cancelled, cmd.Task.NodeID)
			}
		}
	}
	assert.ElementsMatch(t, []string{"escA-action", "escB-action"}, invoked,
		"each fired arm must invoke its own action exactly once")
	assert.ElementsMatch(t, []string{"taskA", "taskB"}, cancelled,
		"both hosts' tasks must be cancelled — on main only taskA's was")
}

// twoEventGatewaysDef arms two DISTINCT event-based gateways on one signal.
// Tier 1's plurality is meaningful only across gateway TOKENS: resolveGatewayWin
// removes every arm of the gateway it resolves, so two same-signal arms on ONE
// gateway can never both fire (first-event-wins is preserved).
func twoEventGatewaysDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-fanout-two-gateways", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			gateway.NewEventBased("gwA"),
			gateway.NewEventBased("gwB"),
			event.NewIntermediateCatch("catchA1", event.WithSignalName("x")),
			event.NewIntermediateCatch("catchA2", event.WithSignalName("other")),
			event.NewIntermediateCatch("catchB1", event.WithSignalName("x")),
			event.NewIntermediateCatch("catchB2", event.WithSignalName("other")),
			activity.NewServiceTask("workA", activity.WithTaskAction("workA-action")),
			activity.NewServiceTask("workB", activity.WithTaskAction("workB-action")),
			event.NewEnd("endA"), event.NewEnd("endB"),
			event.NewEnd("endA2"), event.NewEnd("endB2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "g-start-fork", Source: "start", Target: "fork"},
			{ID: "g-fork-a", Source: "fork", Target: "gwA"},
			{ID: "g-fork-b", Source: "fork", Target: "gwB"},
			{ID: "g-gwa-c1", Source: "gwA", Target: "catchA1"},
			{ID: "g-gwa-c2", Source: "gwA", Target: "catchA2"},
			{ID: "g-gwb-c1", Source: "gwB", Target: "catchB1"},
			{ID: "g-gwb-c2", Source: "gwB", Target: "catchB2"},
			{ID: "g-c1-work", Source: "catchA1", Target: "workA"},
			{ID: "g-c2-end", Source: "catchA2", Target: "endA2"},
			{ID: "g-cb1-work", Source: "catchB1", Target: "workB"},
			{ID: "g-cb2-end", Source: "catchB2", Target: "endB2"},
			{ID: "g-worka-end", Source: "workA", Target: "endA"},
			{ID: "g-workb-end", Source: "workB", Target: "endB"},
		},
	}
}

// TestSignalResolvesEveryMatchingGatewayArm pins tier 1's fan-out ACROSS gateway
// tokens.
func TestSignalResolvesEveryMatchingGatewayArm(t *testing.T) {
	t.Parallel()

	def := twoEventGatewaysDef()
	require.NoError(t, model.Validate(def))

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(fanoutT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tokens, 2, "fixture control: one token parked on each gateway")
	require.Len(t, r1.State.ArmedEvents, 4, "fixture control: two arms per gateway")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(fanoutT0.Add(time.Second), "x", nil), engine.StepOptions{})
	require.NoError(t, err)

	nodes := make([]string, 0, len(r2.State.Tokens))
	for _, tok := range r2.State.Tokens {
		nodes = append(nodes, tok.NodeID)
	}
	assert.ElementsMatch(t, []string{"workA", "workB"}, nodes,
		"BOTH gateway tokens must route to their branch targets")
	assert.Empty(t, r2.State.ArmedEvents,
		"resolving each gateway retires ALL of that gateway's arms, including the 'other' losers")
}

// TestSignalDoesNotFireAnArmThisDeliveryCreated is the second defect the snapshot
// closes, and it is NOT a fan-out case — it is the OPPOSITE direction.
//
// Measured on unpatched main (evidence CTL-1): tier 1 resolves the gateway, its
// drive parks at taskH and ARMS bndH on the same signal, and tier 2 — whose
// lookup runs after tier 1's fire — then fires that brand-new arm. The human task
// is minted and cancelled inside one step, its AwaitHuman dropped as stale, and
// the instance completes down the boundary path.
//
// bndH was NOT armed at the delivery instant, so under BPMN single-instant
// broadcast semantics it must not catch this signal.
//
// ⚠ This means the fan-out is NOT a pure superset of today's behaviour: some
// deliveries now fire FEWER arms.
func TestSignalDoesNotFireAnArmThisDeliveryCreated(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "p-fanout-arm-created-mid-delivery", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewEventBased("evtgw"),
			event.NewIntermediateCatch("catchX", event.WithSignalName("x")),
			event.NewIntermediateCatch("catchY", event.WithSignalName("y")),
			activity.NewUserTask("taskH"),
			event.NewBoundary("bndH", "taskH", event.WithSignalName("x")),
			event.NewEnd("endH"), event.NewEnd("endY"), event.NewEnd("endB"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "m-start-gw", Source: "start", Target: "evtgw"},
			{ID: "m-gw-x", Source: "evtgw", Target: "catchX"},
			{ID: "m-gw-y", Source: "evtgw", Target: "catchY"},
			{ID: "m-x-task", Source: "catchX", Target: "taskH"},
			{ID: "m-task-end", Source: "taskH", Target: "endH"},
			{ID: "m-y-end", Source: "catchY", Target: "endY"},
			{ID: "m-bnd-end", Source: "bndH", Target: "endB"},
		},
	}
	require.NoError(t, model.Validate(def))

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(fanoutT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Empty(t, r1.State.Boundaries,
		"fixture control: bndH CANNOT be armed at the delivery instant — taskH is reached only by tier 1's own fire")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(fanoutT0.Add(time.Second), "x", nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, r2.State.Tokens, 1, "the gateway's token advances to taskH and stays there")
	assert.Equal(t, "taskH", r2.State.Tokens[0].NodeID,
		"taskH must remain parked: bndH was armed by THIS delivery and must not catch it")
	assert.False(t, r2.State.Status.IsTerminal(),
		"on main the boundary fires and the instance completes down endB")

	require.Len(t, r2.State.Tasks, 1, "exactly one human task was minted")
	assert.True(t, r2.State.Tasks[0].IsOpen(),
		"the task must stay actionable — on main it is minted and cancelled inside this same step")

	for _, h := range r2.State.History {
		assert.NotEqual(t, "endB", h.NodeID, "the boundary path must not have run")
	}
}

// ── Regression guards: the families ADR-0158 must NOT change ────────────────
//
// ⚠ D5b measured that a message delivery's observable end state is byte-for-byte
// identical to a signal delivery's on the same shape, so these rows key on the
// TRIGGER TYPE, not on the end state.

// TestMessageDeliveryStillFiresOnlyTheFirstArm pins point-to-point message
// semantics: two message boundary arms on one name, ONE delivery, ONE fire.
// handleMessageReceived dispatches through dispatchArmCascade, which ADR-0158
// does not touch.
func TestMessageDeliveryStillFiresOnlyTheFirstArm(t *testing.T) {
	t.Parallel()

	def := twoInterruptingBoundariesDef()
	for i, n := range def.Nodes {
		if b, ok := n.(event.BoundaryEvent); ok {
			b.SignalName = ""
			b.MessageName = "cancelIt"
			def.Nodes[i] = b
		}
	}
	require.NoError(t, model.Validate(def))

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(fanoutT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Boundaries, 2, "fixture control: both MESSAGE arms armed")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewMessageReceived(fanoutT0.Add(time.Second), "cancelIt", "", nil), engine.StepOptions{})
	require.NoError(t, err)

	assert.Len(t, r2.State.Boundaries, 1,
		"a message is point-to-point: exactly ONE arm fires and the other stays armed")

	var invoked []string
	for _, c := range r2.Commands {
		if a, ok := c.(engine.InvokeAction); ok {
			invoked = append(invoked, a.Name)
		}
	}
	assert.Len(t, invoked, 1, "exactly one boundary action — the fan-out must not reach message dispatch")
}

// TestSignalMatchingNothingMutatesNothing pins merge-once: a delivery that
// matches no arm and no token must not merge its payload (evidence D7a).
func TestSignalMatchingNothingMutatesNothing(t *testing.T) {
	t.Parallel()

	def := twoInterruptingBoundariesDef()
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(fanoutT0, map[string]any{"seed": "kept"}), engine.StepOptions{})
	require.NoError(t, err)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(fanoutT0.Add(time.Second), "no-such-signal",
			map[string]any{"poison": "must-not-appear", "seed": "OVERWRITTEN"}), engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"seed": "kept"}, r2.State.Variables,
		"a no-match delivery must not merge its payload")
	assert.Empty(t, r2.Commands, "and must emit nothing")
}

// TestSignalFiresOneArmPerGatewayToken pins first-event-wins WITHIN a gateway:
// resolveGatewayWin retires every arm of the gateway it resolves, so the second
// snapshotted identity re-resolves to nil and is skipped.
func TestSignalFiresOneArmPerGatewayToken(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "p-fanout-one-gateway-two-arms", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewEventBased("evtgw"),
			event.NewIntermediateCatch("catchA", event.WithSignalName("x")),
			event.NewIntermediateCatch("catchB", event.WithSignalName("x")),
			activity.NewServiceTask("workA", activity.WithTaskAction("workA-action")),
			activity.NewServiceTask("workB", activity.WithTaskAction("workB-action")),
			event.NewEnd("endA"), event.NewEnd("endB"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "o-start-gw", Source: "start", Target: "evtgw"},
			{ID: "o-gw-a", Source: "evtgw", Target: "catchA"},
			{ID: "o-gw-b", Source: "evtgw", Target: "catchB"},
			{ID: "o-a-work", Source: "catchA", Target: "workA"},
			{ID: "o-b-work", Source: "catchB", Target: "workB"},
			{ID: "o-worka-end", Source: "workA", Target: "endA"},
			{ID: "o-workb-end", Source: "workB", Target: "endB"},
		},
	}
	require.NoError(t, model.Validate(def))

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(fanoutT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.ArmedEvents, 2, "fixture control: TWO same-signal arms on ONE gateway token")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(fanoutT0.Add(time.Second), "x", nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, r2.State.Tokens, 1, "an event-based gateway is first-event-wins: ONE branch, not two")
	assert.Equal(t, "workA", r2.State.Tokens[0].NodeID, "the first-declared arm wins")

	var invoked []string
	for _, c := range r2.Commands {
		if a, ok := c.(engine.InvokeAction); ok {
			invoked = append(invoked, a.Name)
		}
	}
	assert.Equal(t, []string{"workA-action"}, invoked, "the losing arm must NOT also fire")
}

// TestSignalResolvesALoopingGatewayOnlyOnce pins the resolvedGateways ABA guard.
//
// resolveGatewayWin does NOT consume the gateway token, so a branch looping back
// through a merge gateway re-arms (GatewayToken, CatchNode) BYTE-IDENTICALLY
// within this same Step — same token id, same catch node. A second snapshotted
// identity naming that token would then re-resolve to the FRESH arm and fire,
// letting one signal resolve the same gateway twice.
//
// ⚠ The naive direct loop (catchA → evtgw) is rejected by model.Validate
// ("gateway both splits and joins"), which is why the loop is routed through an
// exclusive-gateway merge. Shown only the naive loop, a reviewer would delete
// this guard as dead code.
func TestSignalResolvesALoopingGatewayOnlyOnce(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "p-fanout-gateway-aba", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewExclusive("merge"),
			gateway.NewEventBased("evtgw"),
			event.NewIntermediateCatch("catchA", event.WithSignalName("x")),
			event.NewIntermediateCatch("catchB", event.WithSignalName("x")),
			activity.NewServiceTask("workB", activity.WithTaskAction("workB-action")),
			event.NewEnd("endB"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "a-start-merge", Source: "start", Target: "merge"},
			{ID: "a-merge-gw", Source: "merge", Target: "evtgw"},
			{ID: "a-gw-a", Source: "evtgw", Target: "catchA"},
			{ID: "a-gw-b", Source: "evtgw", Target: "catchB"},
			{ID: "a-a-merge", Source: "catchA", Target: "merge"},
			{ID: "a-b-work", Source: "catchB", Target: "workB"},
			{ID: "a-work-end", Source: "workB", Target: "endB"},
		},
	}
	require.NoError(t, model.Validate(def), "the merge-gateway shape must validate; the naive direct loop does not")

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(fanoutT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tokens, 1)
	require.Equal(t, "evtgw", r1.State.Tokens[0].NodeID, "fixture control: parked on the event gateway")
	require.Len(t, r1.State.ArmedEvents, 2, "fixture control: TWO same-signal arms on ONE gateway token")
	gwToken := r1.State.Tokens[0].ID

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(fanoutT0.Add(time.Second), "x", nil), engine.StepOptions{})
	require.NoError(t, err)

	// catchA's branch loops back and re-arms the SAME identity mid-delivery.
	require.Len(t, r2.State.Tokens, 1)
	assert.Equal(t, "evtgw", r2.State.Tokens[0].NodeID,
		"the loop returns the token to the gateway, where it re-arms")
	assert.Equal(t, gwToken, r2.State.Tokens[0].ID,
		"and it is the SAME token id — this is what makes the identity re-creation an ABA")
	assert.Len(t, r2.State.ArmedEvents, 2, "both arms are live again after the loop")

	for _, h := range r2.State.History {
		assert.NotEqual(t, "workB", h.NodeID,
			"catchB's branch must NOT run: one delivery may resolve a gateway ONCE")
	}
	for _, c := range r2.Commands {
		if a, ok := c.(engine.InvokeAction); ok {
			assert.NotEqual(t, "workB-action", a.Name, "the re-armed identity must not fire in this delivery")
		}
	}
}

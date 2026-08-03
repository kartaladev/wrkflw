package engine_test

// step_eventsubprocess_test.go — ADR-0162: the scope SUBTREE, not the scope, is
// the unit of abnormal teardown.
//
// Both abnormal teardowns — the interrupting event-sub-process path
// (engine/step_eventsubprocess.go) and the error-boundary enclosing-scope walk
// (engine/step_errors.go) — used to match tokens on exact scope equality and
// never archived the compensation records of the scopes they tore down. The
// tests here pin the three consequences of routing both through
// cancelScopeSubtree: a nested token no longer survives an interrupt, a
// completed instance no longer carries zombie Scopes entries, and work that
// completed inside a torn-down sub-process stays compensable through all three
// reader surfaces (targeted throw, admin cancel, unhandled error).

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
)

// ── Fixture: root-level interrupting event sub-process over a nested scope ────

// rootInterruptingEventSubprocessOverNestedDef builds ADR-0162's Context
// topology — the one reachable in a single delivery today:
//
//	start → host (ServiceTask "host-action") → end
//	          ↑ interrupting signal boundary "divert" → sub[ nested-start →
//	            nested-work (ServiceTask "nested-action") → nested-end ] → end-diverted
//
//	[root-level event sub-process "esp", INTERRUPTING, signal "boom"]
//	  esp-start(signal "boom") → esp-work (ServiceTask "esp-action") → esp-end
//
// Delivering "divert" interrupts the root-scope host and routes into "sub",
// which drive enters — parking a token in a NESTED scope. Delivering "boom"
// then fires the root-level interrupting event sub-process, whose enclosing
// scope is the implicit root (""). Before ADR-0162 that teardown cancelled
// tokens whose ScopeID was exactly "", so the nested token survived.
func rootInterruptingEventSubprocessOverNestedDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "nested-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("nested-start"),
			activity.NewServiceTask("nested-work", activity.WithTaskAction("nested-action")),
			event.NewEnd("nested-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "nf1", Source: "nested-start", Target: "nested-work"},
			{ID: "nf2", Source: "nested-work", Target: "nested-end"},
		},
	}
	espBody := &model.ProcessDefinition{
		ID: "esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start", event.WithSignalName("boom")),
			activity.NewServiceTask("esp-work", activity.WithTaskAction("esp-action")),
			event.NewEnd("esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ef1", Source: "esp-start", Target: "esp-work"},
			{ID: "ef2", Source: "esp-work", Target: "esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "root-interrupt-over-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("host", activity.WithTaskAction("host-action")),
			event.NewBoundary("bnd-divert", "host", event.WithSignalName("divert")),
			activity.NewSubProcess("sub", nested),
			event.NewEnd("end"),
			event.NewEnd("end-diverted"),
			activity.NewSubProcess("esp", espBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "host"},
			{ID: "f2", Source: "host", Target: "end"},
			{ID: "f3", Source: "bnd-divert", Target: "sub"},
			{ID: "f4", Source: "sub", Target: "end-diverted"},
		},
	}
}

// driveToNestedScopeToken runs the fixture above through:
//
//	StartInstance → host parked (root scope)
//	SignalReceived("divert") → boundary interrupts host, routes into "sub",
//	                           parking nested-work in a NESTED scope
//
// It returns the StepResult just before the root-level interrupt, plus the
// nested scope's ID. The require calls are fixture guards: if the token is not
// in a nested scope the test that follows proves nothing.
func driveToNestedScopeToken(t *testing.T, def *model.ProcessDefinition, instanceID string, at time.Time) (engine.StepResult, string) {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tokens, 1, "setup: one token parked at host")
	require.Equal(t, "host", r1.State.Tokens[0].NodeID)
	require.Empty(t, r1.State.Scopes, "setup: host is a ROOT-scope activity")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(at.Add(time.Second), "divert", nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, r2.State.Scopes, 1, "setup: the boundary must have routed into the sub-process")
	nestedScopeID := r2.State.Scopes[0].ID
	require.Len(t, r2.State.Tokens, 1, "setup: exactly the nested token survives the boundary")
	require.Equal(t, "nested-work", r2.State.Tokens[0].NodeID)
	require.NotEmpty(t, r2.State.Tokens[0].ScopeID,
		"setup: the token must sit in a NESTED scope — an empty ScopeID is the root scope and the test would prove nothing")
	require.Equal(t, nestedScopeID, r2.State.Tokens[0].ScopeID)

	return r2, nestedScopeID
}

// TestRootInterruptingEventSubprocessCancelsNestedSubprocessToken asserts that a
// root-level interrupting event sub-process cancels a token an earlier arm had
// pushed into a NESTED sub-process scope. Before ADR-0162 the teardown matched
// tokens on exact scope equality, so the nested token survived the interrupt and
// the instance kept running the very activity the interrupt targeted.
func TestRootInterruptingEventSubprocessCancelsNestedSubprocessToken(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	def := rootInterruptingEventSubprocessOverNestedDef()

	r2, nestedScopeID := driveToNestedScopeToken(t, def, "esp-nested-1", at)

	// Fire the root-level interrupting event sub-process.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewSignalReceived(at.Add(2*time.Second), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)

	for _, tok := range r3.State.Tokens {
		assert.NotEqual(t, nestedScopeID, tok.ScopeID,
			"no token may survive in the torn-down nested scope %q", nestedScopeID)
		assert.NotEqual(t, "nested-work", tok.NodeID,
			"the interrupt must stop the nested activity it targeted")
	}

	// The only live tokens are the event sub-process's own.
	var espScopeID string
	for _, sc := range r3.State.Scopes {
		if sc.NodeID == "esp" {
			espScopeID = sc.ID
		}
	}
	require.NotEmpty(t, espScopeID, "the event sub-process must have opened its own child scope")

	require.Len(t, r3.State.Tokens, 1, "only the event sub-process's own token may be live")
	assert.Equal(t, "esp-work", r3.State.Tokens[0].NodeID)
	assert.Equal(t, espScopeID, r3.State.Tokens[0].ScopeID)
}

// TestRootInterruptingEventSubprocessLeavesNoZombieScopes asserts a COMPLETED
// instance carries no leftover Scopes entries. Before ADR-0162 the cancelled
// descendant scopes were never closed, so a terminal snapshot was committed with
// open scopes in it.
//
// The assertion is made on the COMPLETED state, not mid-interrupt: the enclosing
// scope is deliberately kept open across the interrupt (closeScopeDescendants
// keeps scopeID itself), so a mid-flight assertion of zero scopes would be wrong.
func TestRootInterruptingEventSubprocessLeavesNoZombieScopes(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	def := rootInterruptingEventSubprocessOverNestedDef()

	r2, _ := driveToNestedScopeToken(t, def, "esp-nested-2", at)

	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewSignalReceived(at.Add(2*time.Second), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)

	espCmdID := findInvokeActionID(t, r3.Commands, "esp-action")

	// Drive the event sub-process to completion.
	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewActionCompleted(at.Add(3*time.Second), espCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Equal(t, engine.StatusCompleted, r4.State.Status,
		"the interrupting event sub-process drained, so the instance must reach a terminal status")
	assert.Empty(t, r4.State.Scopes,
		"a terminal snapshot must not carry open scopes cancelled by the interrupt")
	assert.Empty(t, r4.State.Tokens, "a terminal snapshot must not carry live tokens")
}

// ── Fixture: the `fulfil` topology (spec §1.4) ───────────────────────────────

// fulfilInnerDef is the sub-process body shared by both fulfil fixtures:
//
//	start2 → charge (ServiceTask "charge-action", compensable: "refund") → ship
//	         (ServiceTask "ship-action") → end2
//
// "charge" completing records a compensation entry in the fulfil scope; "ship"
// then failing with "OutOfStock" tears that scope down through the error
// boundary.
func fulfilInnerDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "fulfil-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start2"),
			activity.NewServiceTask("charge",
				activity.WithTaskAction("charge-action"),
				activity.WithCompensateAction("refund")),
			activity.NewServiceTask("ship", activity.WithTaskAction("ship-action")),
			event.NewEnd("end2"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "inf1", Source: "start2", Target: "charge"},
			{ID: "inf2", Source: "charge", Target: "ship"},
			{ID: "inf3", Source: "ship", Target: "end2"},
		},
	}
}

// fulfilSubprocessDef returns:
//
//	start → fulfil[ start2 → charge(compensable: refund) → ship → end2 ] → end
//	          ↑ error boundary "OutOfStock" → notify → end3
//
// A sub-process whose completed compensable activity must survive the
// sub-process being torn down by its own error boundary (ADR-0162). "notify" is
// a ServiceTask so the instance PARKS there and stays RUNNING — the branch
// choice a later cancel makes is only observable while the instance can still
// receive one.
func fulfilSubprocessDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "fulfil-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("fulfil", fulfilInnerDef()),
			event.NewBoundary("bnd-oos", "fulfil", event.WithBoundaryErrorCode("OutOfStock")),
			activity.NewServiceTask("notify", activity.WithTaskAction("notify-action")),
			event.NewEnd("end"),
			event.NewEnd("end3"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fulfil"},
			{ID: "f2", Source: "fulfil", Target: "end"},
			{ID: "f3", Source: "bnd-oos", Target: "notify"},
			{ID: "f4", Source: "notify", Target: "end3"},
		},
	}
}

// fulfilSubprocessWithTargetedThrowDef is fulfilSubprocessDef with a targeted
// compensation throw interposed on the boundary's escape path:
//
//	↑ error boundary "OutOfStock" → throw(CompensateRef "fulfil") → notify → end3
//
// It pins the second reader surface ADR-0162 changes: a CompensateThrow naming
// the torn-down sub-process node.
func fulfilSubprocessWithTargetedThrowDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "fulfil-proc-throw", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("fulfil", fulfilInnerDef()),
			event.NewBoundary("bnd-oos", "fulfil", event.WithBoundaryErrorCode("OutOfStock")),
			event.NewCompensateThrow("throw", event.WithCompensateRef("fulfil")),
			activity.NewServiceTask("notify", activity.WithTaskAction("notify-action")),
			event.NewEnd("end"),
			event.NewEnd("end3"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fulfil"},
			{ID: "f2", Source: "fulfil", Target: "end"},
			{ID: "f3", Source: "bnd-oos", Target: "throw"},
			{ID: "f4", Source: "throw", Target: "notify"},
			{ID: "f5", Source: "notify", Target: "end3"},
		},
	}
}

// driveFulfilToBoundary runs either fulfil fixture through:
//
//	StartInstance      → charge invoked
//	ActionCompleted    → charge's compensation record lands in the fulfil scope,
//	                     ship invoked
//	ActionFailed(OOS)  → the error boundary tears the fulfil scope down
//
// It returns the StepResult of the teardown step plus the fulfil scope's ID
// (captured before it is pruned).
func driveFulfilToBoundary(t *testing.T, def *model.ProcessDefinition, instanceID string, at time.Time) (engine.StepResult, string) {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	chargeCmdID := findInvokeActionID(t, r1.Commands, "charge-action")
	require.Len(t, r1.State.Scopes, 1, "setup: the sub-process scope must be open")
	fulfilScopeID := r1.State.Scopes[0].ID

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), chargeCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.Scopes, 1)
	require.Len(t, r2.State.Scopes[0].Compensations, 1,
		"setup: completing the compensable charge must record one entry in the fulfil scope")
	shipCmdID := findInvokeActionID(t, r2.Commands, "ship-action")

	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionFailed(at.Add(2*time.Second), shipCmdID, "OutOfStock", false), engine.StepOptions{})
	require.NoError(t, err)

	return r3, fulfilScopeID
}

// TestErrorBoundaryTeardownArchivesCompensations asserts that when an error
// boundary tears down an enclosing scope, the completed compensable work inside
// it is archived rather than pruned with the scope. Before ADR-0162 the record
// was discarded, so a card charged inside a failed fulfilment could never be
// refunded — while the identical sub-process exiting normally stayed
// compensable.
func TestErrorBoundaryTeardownArchivesCompensations(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC)
	def := fulfilSubprocessDef()

	r3, fulfilScopeID := driveFulfilToBoundary(t, def, "fulfil-arch-1", at)

	// The boundary fired and routed to "notify".
	assert.Equal(t, engine.StatusRunning, r3.State.Status)
	_, ok := findInvokeAction(r3.Commands, "notify-action")
	assert.True(t, ok, "the error boundary must route to the notify handler")

	// ⚠ archiveCompensations keys by scope.NodeID — the SUB-PROCESS node "fulfil",
	// not the compensable activity "charge". Asserting the wrong key would report
	// an empty map both before and after the fix and certify nothing, so the
	// record's own NodeID is asserted too.
	records, keyed := r3.State.ArchivedCompensations["fulfil"]
	require.True(t, keyed,
		"the torn-down scope's records must be archived under the sub-process node id %q; got keys %v",
		"fulfil", r3.State.ArchivedCompensations)
	require.Len(t, records, 1, "exactly the one refund record must survive the teardown")
	assert.Equal(t, "charge", records[0].NodeID,
		"the archived record must be the compensable activity's, not the sub-process node's")
	assert.Equal(t, "refund", records[0].Action)

	assert.Empty(t, r3.State.Scopes,
		"the torn-down fulfil scope %q must be closed, and it is the only scope", fulfilScopeID)
}

// ── Fixture: a compensable record one level BELOW the erroring scope ─────────

// nestedFulfilSubprocessDef builds a THREE-level topology whose compensation
// record sits in a scope the error boundary reaches only by descending:
//
//	start → outer[ o-start → inner[ i-start → ifork ⇒ { charge(compensable:
//	        refund), fail-here } → ijoin → i-end ] → o-end ] → end
//	          ↑ error boundary "OutOfStock" on OUTER → notify → end3
//
//	[event sub-process "inner-esp" inside inner, signal "never-fired"]
//
// "fail-here" throws from the INNER scope; no boundary is attached to "inner",
// so findEnclosingBoundary walks out to the boundary on "outer" and the erroring
// scope is the OUTER one. The record and the event-sub-process arm both belong
// to the INNER scope — a descendant of the scope being torn down.
//
// This is the topology test 3 cannot reach. There the erroring scope IS the
// scope holding the record, so cancelScopeSubtree's `scopeID`-first block
// archives it and the descendant loop is skipped outright by `id == scopeID`.
// Here only the descendant loop can archive the record or retire the arm.
func nestedFulfilSubprocessDef() *model.ProcessDefinition {
	innerESP := &model.ProcessDefinition{
		ID: "inner-esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("iesp-start", event.WithSignalName("never-fired")),
			activity.NewServiceTask("iesp-work", activity.WithTaskAction("iesp-action")),
			event.NewEnd("iesp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "iespf1", Source: "iesp-start", Target: "iesp-work"},
			{ID: "iespf2", Source: "iesp-work", Target: "iesp-end"},
		},
	}
	innerBody := &model.ProcessDefinition{
		ID: "nested-inner-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("i-start"),
			gateway.NewParallel("ifork"),
			activity.NewServiceTask("charge",
				activity.WithTaskAction("charge-action"),
				activity.WithCompensateAction("refund")),
			activity.NewServiceTask("fail-here", activity.WithTaskAction("fail-action")),
			gateway.NewParallel("ijoin"),
			event.NewEnd("i-end"),
			// Armed when the inner scope opens; only the descendant loop retires it.
			activity.NewSubProcess("inner-esp", innerESP),
		},
		Flows: []flow.SequenceFlow{
			{ID: "nif1", Source: "i-start", Target: "ifork"},
			{ID: "nif2", Source: "ifork", Target: "charge"},
			{ID: "nif3", Source: "ifork", Target: "fail-here"},
			{ID: "nif4", Source: "charge", Target: "ijoin"},
			{ID: "nif5", Source: "fail-here", Target: "ijoin"},
			{ID: "nif6", Source: "ijoin", Target: "i-end"},
		},
	}
	outerBody := &model.ProcessDefinition{
		ID: "nested-outer-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("o-start"),
			activity.NewSubProcess("inner", innerBody),
			event.NewEnd("o-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "nof1", Source: "o-start", Target: "inner"},
			{ID: "nof2", Source: "inner", Target: "o-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "nested-fulfil-proc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", outerBody),
			event.NewBoundary("bnd-oos", "outer", event.WithBoundaryErrorCode("OutOfStock")),
			activity.NewServiceTask("notify", activity.WithTaskAction("notify-action")),
			event.NewEnd("end"),
			event.NewEnd("end3"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outer"},
			{ID: "f2", Source: "outer", Target: "end"},
			{ID: "f3", Source: "bnd-oos", Target: "notify"},
			{ID: "f4", Source: "notify", Target: "end3"},
		},
	}
}

// TestErrorBoundaryTeardownArchivesNestedSubtreeCompensations asserts that the
// teardown archives records and retires arms held by a scope BELOW the erroring
// scope — the depth-two case that makes "the subtree is the unit of teardown"
// more than a claim about depth one.
//
// It is the only test that exercises cancelScopeSubtree's descendant loop for
// effect: TestErrorBoundaryTeardownArchivesCompensations drives the erroring
// scope itself, which the loop skips by `id == scopeID`. Without this test both
// statements in that loop can be deleted with the package staying green
// (Task 3 review, IMPORTANT 1), and a nested-nested compensable activity would
// be silently lost on teardown — the exact defect class ADR-0162 exists to
// remove.
func TestErrorBoundaryTeardownArchivesNestedSubtreeCompensations(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	def := nestedFulfilSubprocessDef()

	// ── Step 1: start → outer scope → inner scope → fork ⇒ charge + fail-here.
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "nested-arch-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Scopes, 2, "setup: both the outer and the inner scope must be open")

	var innerScopeID, outerScopeID string
	for _, sc := range r1.State.Scopes {
		switch sc.NodeID {
		case "outer":
			outerScopeID = sc.ID
		case "inner":
			innerScopeID = sc.ID
		}
	}
	require.NotEmpty(t, outerScopeID)
	require.NotEmpty(t, innerScopeID)
	require.Equal(t, outerScopeID, engine.ScopeByID(&r1.State, innerScopeID).ParentID,
		"setup: the inner scope must be a CHILD of the outer scope, or the loop is not exercised")
	require.Len(t, r1.State.EventTriggeredSubprocesses, 1,
		"setup: the inner scope's event sub-process must be armed")
	require.Equal(t, innerScopeID, r1.State.EventTriggeredSubprocesses[0].EnclosingScopeID,
		"setup: the arm must belong to the DESCENDANT scope, not the erroring one")

	chargeCmdID := findInvokeActionID(t, r1.Commands, "charge-action")
	failCmdID := findInvokeActionID(t, r1.Commands, "fail-action")

	// ── Step 2: charge completes → its record lands in the INNER scope, which
	// stays open because the sibling branch has not reached the join.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), chargeCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	innerScope := engine.ScopeByID(&r2.State, innerScopeID)
	require.NotNil(t, innerScope, "setup: the inner scope must still be OPEN — a normal exit would archive it and prove nothing")
	require.Len(t, innerScope.Compensations, 1,
		"setup: the record must live in the inner scope, one level below the erroring scope")
	require.Empty(t, r2.State.ArchivedCompensations,
		"setup: nothing may be archived before the teardown")

	// ── Step 3: fail-here throws OutOfStock. No boundary is attached to "inner",
	// so the walk escapes to the boundary on "outer": the erroring scope is the
	// OUTER one and the inner scope is reached only as a descendant.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionFailed(at.Add(2*time.Second), failCmdID, "OutOfStock", false), engine.StepOptions{})
	require.NoError(t, err)

	_, routed := findInvokeAction(r3.Commands, "notify-action")
	assert.True(t, routed, "the boundary on the OUTER sub-process must have fired")

	// The archive key is the INNER sub-process node id — the NodeID of the scope
	// that owned the record, not the erroring scope's ("outer") and not the
	// compensable activity's ("charge").
	records, keyed := r3.State.ArchivedCompensations["inner"]
	require.True(t, keyed,
		"the descendant scope's records must be archived under its own sub-process node id %q; got keys %v",
		"inner", r3.State.ArchivedCompensations)
	require.Len(t, records, 1, "exactly the one refund record must survive the subtree teardown")
	assert.Equal(t, "charge", records[0].NodeID,
		"the archived record must be the compensable activity's, not the sub-process node's")
	assert.Equal(t, "refund", records[0].Action)

	// The descendant scope's event-sub-process arm must be retired with it: a
	// surviving arm would name a scope that no longer exists.
	assert.Empty(t, r3.State.EventTriggeredSubprocesses,
		"the descendant scope's arm must not outlive the scope it is enclosed by")

	assert.Empty(t, r3.State.Scopes, "the whole torn-down subtree must be closed")
}

// TestArchivedRecordIsReachableByTargetedThrow pins the second reader surface
// ADR-0162 changes: a CompensateThrow naming the torn-down sub-process node used
// to auto-advance on an empty archived-records lookup, in
// compensationThrowEventStrategy.enter's targeted-throw branch, and now walks
// the archived records for real.
func TestArchivedRecordIsReachableByTargetedThrow(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC)
	def := fulfilSubprocessWithTargetedThrowDef()

	r3, _ := driveFulfilToBoundary(t, def, "fulfil-throw-1", at)

	assert.Equal(t, engine.StatusCompensating, r3.State.Status,
		"the targeted throw must start a real compensation walk over the archived record")

	refund, ok := findInvokeAction(r3.Commands, "refund")
	require.True(t, ok, "the throw must emit the archived record's compensation action")
	assert.False(t, refund.FireAndForget,
		"a compensation invocation is awaited, not fire-and-forget")

	_, advanced := findInvokeAction(r3.Commands, "notify-action")
	assert.False(t, advanced,
		"the throw must NOT auto-advance past the walk to the notify handler")
}

// TestTeardownArchiveSwitchesCancelToTheCompensationBranch pins the third reader
// surface, and the one most likely to move an existing expectation (audit
// finding D7). handleCancelRequested and handleUnhandledError both choose their
// branch on len(s.RootCompensations) > 0 || len(s.ArchivedCompensations) > 0. A teardown
// that used to leave the archive empty now populates it, so a later admin cancel
// switches from the SINGLE-STEP immediate-termination branch to a MULTI-STEP
// compensation walk with a different terminal command sequence.
func TestTeardownArchiveSwitchesCancelToTheCompensationBranch(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC)
	def := fulfilSubprocessDef()

	r3, _ := driveFulfilToBoundary(t, def, "fulfil-cancel-1", at)

	// The branch choice is only observable while the instance can still receive a
	// cancel, so the fixture must be parked and RUNNING here.
	require.Equal(t, engine.StatusRunning, r3.State.Status)
	require.Len(t, r3.State.Tokens, 1)
	require.Equal(t, "notify", r3.State.Tokens[0].NodeID)

	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewCancelRequested(at.Add(3*time.Second)), engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusCompensating, r4.State.Status,
		"the archived record must divert the cancel into the multi-step compensation walk")
	_, ok := findInvokeAction(r4.Commands, "refund")
	assert.True(t, ok, "the cancel must run the archived compensation action")

	for _, c := range r4.Commands {
		_, isFail := c.(engine.FailInstance)
		assert.False(t, isFail,
			"the cancel must not terminate immediately: FailInstance belongs at the END of the walk")
	}
}

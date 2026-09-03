package engine_test

// step_scope_drain_test.go: the scope SUBTREE, not the scope, is the
// unit of a DRAIN check.
//
// The sub-process drain checks used to enumerate DIRECT children only, and the
// event-sub-process exit had no descendant check at all. Either way a grandchild
// scope holding the live token was invisible, the exit declared the subtree
// drained, closeScope pruned it transitively, and the surviving token was left
// naming a scope absent from State.Scopes — a permanently wedged instance.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// ── Shared assertions ────────────────────────────────────────────────────────

// assertTokenScopesResolve asserts the invariant whose violation IS the wedge:
// every surviving token names either the implicit root scope ("") or a Scope
// still present in State.Scopes. A token naming a pruned scope makes
// defForScope fail on every subsequent Step.
func assertTokenScopesResolve(t *testing.T, st engine.InstanceState) {
	t.Helper()
	live := make(map[string]bool, len(st.Scopes))
	for _, sc := range st.Scopes {
		live[sc.ID] = true
	}
	for _, tok := range st.Tokens {
		if tok.ScopeID == "" {
			continue // the root scope is implicit and always live
		}
		assert.True(t, live[tok.ScopeID],
			"token %q at node %q names scope %q, which is not in State.Scopes — the instance is wedged",
			tok.ID, tok.NodeID, tok.ScopeID)
	}
}

// taskIDForNode returns the human-task id minted for nodeID. It fails the test
// when no such task exists: a fixture that never parked the task proves nothing.
func taskIDForNode(t *testing.T, st engine.InstanceState, nodeID string) string {
	t.Helper()
	for _, task := range st.Tasks {
		if task.NodeID == nodeID {
			return task.TaskID
		}
	}
	t.Fatalf("fixture: no human task recorded for node %q", nodeID)
	return ""
}

// ── Fixture: three nesting levels under a regular sub-process ────────────────

// nestedGrandchildScopeDef builds
//
//	root:  start → outer(SubProcess) → root-end
//	outer: outer-start → fork ⇒ { A: inner(SubProcess) → a-end
//	                              B: gate(UserTask)    → b-end }
//	inner: inner-start → deep(SubProcess) → inner-end
//	deep:  deep-start  → deep-task(UserTask) → deep-end
//
// Branch A descends TWO scope levels below "outer" and parks in "deep"; branch B
// parks at "gate" in the outer scope itself. Completing "gate" then drives
// branch B into "b-end", which is an EndEvent in the OUTER scope — the drain
// check under test.
//
// The nesting depth is load-bearing. When the outer scope's exit runs, the inner
// scope holds ZERO tokens of its own (the live token is one level further down,
// in "deep"), so the old direct-children scan saw nothing. A two-level topology
// would put the token directly in "inner" and the old scan would already catch
// it — such a fixture reproduces nothing.
func nestedGrandchildScopeDef() *model.ProcessDefinition {
	deep := &model.ProcessDefinition{
		ID: "deep-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("deep-start"),
			activity.NewUserTask("deep-task", activity.WithEligibleRoles("ops")),
			event.NewEnd("deep-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "df1", Source: "deep-start", Target: "deep-task"},
			{ID: "df2", Source: "deep-task", Target: "deep-end"},
		},
	}
	inner := &model.ProcessDefinition{
		ID: "inner-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewSubProcess("deep", deep),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "deep"},
			{ID: "if2", Source: "deep", Target: "inner-end"},
		},
	}
	outer := &model.ProcessDefinition{
		ID: "outer-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			gateway.NewParallel("fork"),
			activity.NewSubProcess("inner", inner),
			event.NewEnd("a-end"),
			activity.NewUserTask("gate", activity.WithEligibleRoles("ops")),
			event.NewEnd("b-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "fork"},
			{ID: "of2", Source: "fork", Target: "inner"},
			{ID: "of3", Source: "inner", Target: "a-end"},
			{ID: "of4", Source: "fork", Target: "gate"},
			{ID: "of5", Source: "gate", Target: "b-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "nested-grandchild-drain", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", outer),
			event.NewEnd("root-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outer"},
			{ID: "f2", Source: "outer", Target: "root-end"},
		},
	}
}

// TestGrandchildScopeBlocksSubprocessDrain reproduces the permanent instance
// wedge. The three drain checks used to enumerate DIRECT children only, so a
// grandchild scope holding the live token was invisible: the exit declared the
// subtree drained, closeScope pruned it transitively, and the surviving token
// named a scope absent from s.Scopes. From that commit on, defForScope failed
// for every subsequent Step — the instance could be neither advanced nor
// terminated, because every path runs through drive.
func TestGrandchildScopeBlocksSubprocessDrain(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	def := nestedGrandchildScopeDef()
	actor := authz.Actor{ID: "alice", Roles: []string{"ops"}}

	// 1. Start: branch A descends outer → inner → deep and parks at deep-task;
	//    branch B parks at "gate" in the outer scope.
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "wedge-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Scopes, 3, "fixture: outer, inner and deep scopes must all be open")
	require.Len(t, r1.State.Tokens, 2, "fixture: exactly the gate and deep-task tokens are parked")

	deepScopeID := r1.State.Scopes[2].ID
	require.Equal(t, r1.State.Scopes[1].ID, r1.State.Scopes[2].ParentID,
		"fixture: deep must be a child of inner")
	require.Equal(t, r1.State.Scopes[0].ID, r1.State.Scopes[1].ParentID,
		"fixture: inner must be a child of outer — the live token must sit in a GRANDCHILD of the scope being exited")

	var deepTok, gateTok bool
	for _, tok := range r1.State.Tokens {
		switch tok.NodeID {
		case "deep-task":
			deepTok = true
			require.Equal(t, deepScopeID, tok.ScopeID,
				"fixture: the live token must be in the DEEPEST scope")
		case "gate":
			gateTok = true
			require.Equal(t, r1.State.Scopes[0].ID, tok.ScopeID,
				"fixture: the sibling branch must park in the OUTER scope")
		}
	}
	require.True(t, deepTok && gateTok, "fixture: both branches must be parked")

	// 2. Drain branch B: completing "gate" drives its token into "b-end", the
	//    outer scope's end event.
	gateTaskID := taskIDForNode(t, r1.State, "gate")
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewHumanCompleted(at.Add(time.Minute), gateTaskID, engine.CompletionInput{}, actor),
		engine.StepOptions{})

	// 3. The draining Step itself succeeds — before AND after the fix.
	require.NoError(t, err, "the draining Step must not error")

	// 4. Every surviving token still names a live Scope.
	assertTokenScopesResolve(t, r2.State)

	// 5. THE LOAD-BEARING ASSERTION. The pre-fix failure is not in the draining
	//    Step but in every SUBSEQUENT one: the orphaned token's scope is gone, so
	//    defForScope fails and the instance can be neither advanced nor
	//    terminated.
	deepTaskID := taskIDForNode(t, r2.State, "deep-task")
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewHumanCompleted(at.Add(2*time.Minute), deepTaskID, engine.CompletionInput{}, actor),
		engine.StepOptions{})
	require.NoError(t, err, "the instance is wedged: no subsequent Step can advance or terminate it")
	assert.Equal(t, engine.StatusCompleted, r3.State.Status,
		"with the grandchild drained, every scope unwinds and the instance completes")
	assert.Empty(t, r3.State.Tokens, "a completed instance carries no tokens")
	assert.Empty(t, r3.State.Scopes, "a completed instance carries no open scopes")
}

// ── Fixture: root-level interrupting event sub-process over a nested scope ────

// eventSubprocessOverNestedScopeDef builds the event-sub-process topology:
//
//	root: start → host(UserTask) → root-end
//	      [root-level INTERRUPTING event sub-process "esp", signal "boom"]
//	esp:  esp-start(signal "boom") → esp-fork ⇒ { A: esp-inner(SubProcess) → esp-a-end
//	                                              B: esp-gate(UserTask)    → esp-b-end }
//	inner: inner-start → inner-task(UserTask) → inner-end
//
// Firing "boom" opens the event sub-process's scope C. Branch A opens scope D
// under C and parks "inner-task" there; branch B parks at "esp-gate" in C.
// Completing "esp-gate" drives branch B to "esp-b-end", at which point
// tokensInScope(C) == 0 — and exitEventSubprocessScope ran closeScope(C)
// unconditionally, which cascades and prunes D too.
//
// espCompensable, when true, replaces the fork/branches with a single
// compensable ServiceTask so the same root shape can pin the archiving half.
func eventSubprocessOverNestedScopeDef(espCompensable bool) *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "esp-inner-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewUserTask("inner-task", activity.WithEligibleRoles("ops")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "nf1", Source: "inner-start", Target: "inner-task"},
			{ID: "nf2", Source: "inner-task", Target: "inner-end"},
		},
	}
	espBody := &model.ProcessDefinition{
		ID: "esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start", event.WithSignalName("boom")),
			gateway.NewParallel("esp-fork"),
			activity.NewSubProcess("esp-inner", inner),
			event.NewEnd("esp-a-end"),
			activity.NewUserTask("esp-gate", activity.WithEligibleRoles("ops")),
			event.NewEnd("esp-b-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "ef1", Source: "esp-start", Target: "esp-fork"},
			{ID: "ef2", Source: "esp-fork", Target: "esp-inner"},
			{ID: "ef3", Source: "esp-inner", Target: "esp-a-end"},
			{ID: "ef4", Source: "esp-fork", Target: "esp-gate"},
			{ID: "ef5", Source: "esp-gate", Target: "esp-b-end"},
		},
	}
	if espCompensable {
		espBody = &model.ProcessDefinition{
			ID: "esp-body", Version: 1,
			Nodes: []model.Node{
				event.NewStart("esp-start", event.WithSignalName("boom")),
				activity.NewServiceTask("esp-work",
					activity.WithTaskAction("book"),
					activity.WithCompensateAction("cancel-book")),
				event.NewEnd("esp-end"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "ef1", Source: "esp-start", Target: "esp-work"},
				{ID: "ef2", Source: "esp-work", Target: "esp-end"},
			},
		}
	}
	return &model.ProcessDefinition{
		ID: "esp-over-nested-scope", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("host", activity.WithEligibleRoles("ops")),
			event.NewEnd("root-end"),
			activity.NewSubProcess("esp", espBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "host"},
			{ID: "f2", Source: "host", Target: "root-end"},
		},
	}
}

// TestEventSubprocessExitBlocksOnChildScope reproduces the permanent wedge at
// the one closeScope call site with no descendant check at all. The topology is
// an event sub-process whose body is
// start(signal) → fork ⇒ { A: SubProcess "inner"[…UserTask…], B: end }.
// Branch A opens scope D under the event sub-process's own scope C — a direct
// child, sufficient here because :283 had no check at all — while branch B
// reaches the event sub-process's end event. tokensInScope(C) == 0, so
// exitEventSubprocessScope ran closeScope(C), which cascades and prunes D too —
// leaving the UserTask token naming an absent scope.
func TestEventSubprocessExitBlocksOnChildScope(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 11, 0, 0, 0, time.UTC)
	def := eventSubprocessOverNestedScopeDef(false)
	actor := authz.Actor{ID: "alice", Roles: []string{"ops"}}

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "esp-wedge-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Tokens, 1, "fixture: the root host task is parked")
	require.Equal(t, "host", r1.State.Tokens[0].NodeID)

	// 1. Fire the interrupting event sub-process: branch A parks "inner-task" in
	//    scope D, branch B parks at "esp-gate" in scope C.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(at.Add(time.Minute), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.Scopes, 2, "fixture: the event sub-process scope C and the nested scope D must both be open")
	scopeC, scopeD := r2.State.Scopes[0].ID, r2.State.Scopes[1].ID
	require.Equal(t, scopeC, r2.State.Scopes[1].ParentID, "fixture: D must be nested under C")
	require.Len(t, r2.State.Tokens, 2, "fixture: exactly the gate and inner-task tokens survive the interrupt")

	var innerTok bool
	for _, tok := range r2.State.Tokens {
		if tok.NodeID == "inner-task" {
			innerTok = true
			require.Equal(t, scopeD, tok.ScopeID,
				"fixture: the live token must sit in the scope NESTED under the event sub-process's own scope")
		}
	}
	require.True(t, innerTok, "fixture: branch A must have descended into the nested sub-process")

	// 2. Drive branch B to the event sub-process's end event.
	gateTaskID := taskIDForNode(t, r2.State, "esp-gate")
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewHumanCompleted(at.Add(2*time.Minute), gateTaskID, engine.CompletionInput{}, actor),
		engine.StepOptions{})

	// 3. The exiting Step succeeds and leaves every token naming a live Scope.
	require.NoError(t, err, "the event sub-process exit must not error")
	assertTokenScopesResolve(t, r3.State)

	// 4. THE LOAD-BEARING ASSERTION, as in
	//    TestGrandchildScopeBlocksSubprocessDrain: the pre-fix failure is in the
	//    NEXT Step, not this one.
	innerTaskID := taskIDForNode(t, r3.State, "inner-task")
	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewHumanCompleted(at.Add(3*time.Minute), innerTaskID, engine.CompletionInput{}, actor),
		engine.StepOptions{})
	require.NoError(t, err, "the instance is wedged: no subsequent Step can advance or terminate it")
	assert.Equal(t, engine.StatusCompleted, r4.State.Status,
		"with the nested scope drained, the event sub-process exits and the instance completes")
	assert.Empty(t, r4.State.Tokens, "a completed instance carries no tokens")
	assert.Empty(t, r4.State.Scopes, "a completed instance carries no open scopes")
}

// TestEventSubprocessNormalExitArchivesCompensations pins the archiving half of
// the fix. Compensable work completed inside ANY event sub-process was dropped
// on its NORMAL exit, because :283 closes without archiving.
func TestEventSubprocessNormalExitArchivesCompensations(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	def := eventSubprocessOverNestedScopeDef(true)

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "esp-arch-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	// 1. Fire the event sub-process: its compensable ServiceTask is invoked.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(at.Add(time.Minute), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	var invokeID string
	for _, cmd := range r2.Commands {
		if inv, ok := cmd.(engine.InvokeAction); ok && inv.Name == "book" {
			invokeID = inv.CommandID
		}
	}
	require.NotEmpty(t, invokeID, "fixture: the event sub-process's compensable action must have been invoked")

	// 2. Complete it — the event sub-process then drives to its NORMAL exit.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Minute), invokeID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Empty(t, r3.State.Scopes, "fixture: the event sub-process scope must have exited normally")

	// 3. The record survives the exit, keyed by the EVENT SUB-PROCESS node id —
	//    the scope's NodeID, not the activity's.
	require.Len(t, r3.State.ArchivedCompensations["esp"], 1,
		"compensable work completed inside an event sub-process must survive its normal exit")
	assert.Equal(t, "cancel-book", r3.State.ArchivedCompensations["esp"][0].Action)
	assert.Equal(t, "esp-work", r3.State.ArchivedCompensations["esp"][0].NodeID)
}

// ── Fixture: a NON-interrupting event sub-process outliving its enclosing scope ──

// nonInterruptingEventSubprocessOverNestedDef builds
//
//	root:  start → outer(SubProcess) → root-end
//	       [root-level NON-INTERRUPTING event sub-process "r-esp", signal "ping"]
//	r-esp: r-esp-start(signal "ping", non-interrupting) → r-esp-end
//	outer: outer-start → inner(SubProcess) → outer-end
//	inner: inner-start → inner-task(UserTask) → inner-end
//
// The live token parks in "inner", a GRANDCHILD of the root scope: the outer
// scope itself holds zero tokens. When "r-esp" completes, the root-level
// event-sub-process exit asks whether any OTHER root child is still busy — and a
// direct-children scan answers "no", because the busy scope is one level further
// down.
func nonInterruptingEventSubprocessOverNestedDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "ni-inner-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewUserTask("inner-task", activity.WithEligibleRoles("ops")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "nif1", Source: "inner-start", Target: "inner-task"},
			{ID: "nif2", Source: "inner-task", Target: "inner-end"},
		},
	}
	outer := &model.ProcessDefinition{
		ID: "ni-outer-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewSubProcess("inner", inner),
			event.NewEnd("outer-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "nof1", Source: "outer-start", Target: "inner"},
			{ID: "nof2", Source: "inner", Target: "outer-end"},
		},
	}
	espBody := &model.ProcessDefinition{
		ID: "ni-esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("r-esp-start", event.WithSignalName("ping"), event.WithNonInterrupting()),
			event.NewEnd("r-esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "nef1", Source: "r-esp-start", Target: "r-esp-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "ni-esp-over-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", outer),
			event.NewEnd("root-end"),
			activity.NewSubProcess("r-esp", espBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outer"},
			{ID: "f2", Source: "outer", Target: "root-end"},
		},
	}
}

// TestRootEventSubprocessExitSeesGrandchildOfRoot pins the widened drain check in
// exitRootEventSubprocessScope. A root-level event sub-process completing while a
// GRANDCHILD of the root still holds the live token must not declare the root
// drained: the direct-children scan it replaced saw only the (empty) outer scope
// and fell through to the root-completion tail, retiring the root's event
// sub-process arms even though the instance was still working.
func TestRootEventSubprocessExitSeesGrandchildOfRoot(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 13, 0, 0, 0, time.UTC)
	def := nonInterruptingEventSubprocessOverNestedDef()
	actor := authz.Actor{ID: "alice", Roles: []string{"ops"}}

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "root-esp-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Scopes, 2, "fixture: the outer and inner scopes must both be open")
	require.Len(t, r1.State.Tokens, 1)
	require.Equal(t, "inner-task", r1.State.Tokens[0].NodeID)
	require.Equal(t, r1.State.Scopes[1].ID, r1.State.Tokens[0].ScopeID,
		"fixture: the live token must sit in the GRANDCHILD scope, leaving the root's direct child empty")
	require.Len(t, r1.State.EventTriggeredSubprocesses, 1, "fixture: the root-level arm must be armed")

	// Fire the non-interrupting root-level event sub-process; its body completes
	// within the same Step.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(at.Add(time.Minute), "ping", nil), engine.StepOptions{})
	require.NoError(t, err)

	assert.Len(t, r2.State.EventTriggeredSubprocesses, 1,
		"the root is NOT drained — a grandchild still holds the live token — so the root's event sub-process arms must survive")
	assert.Equal(t, engine.StatusRunning, r2.State.Status)
	assertTokenScopesResolve(t, r2.State)

	// Drain the grandchild and unwind: the instance must still be able to run to
	// completion, matching the shape of this file's other tests. Without this the
	// assertion above is the test's ONLY discriminating line.
	innerTaskID := taskIDForNode(t, r2.State, "inner-task")
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewHumanCompleted(at.Add(2*time.Minute), innerTaskID, engine.CompletionInput{}, actor),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status,
		"with the grandchild drained, every scope unwinds and the instance completes")
	assert.Empty(t, r3.State.Tokens, "a completed instance carries no tokens")
	assert.Empty(t, r3.State.Scopes, "a completed instance carries no open scopes")
}

// ── Fixture: a non-interrupting ESP that outlives its enclosing sub-process ───

// espOutlivingEnclosingScopeDef builds
//
//	root:  start → outer(SubProcess) → root-end
//	outer: outer-start → work(ServiceTask, compensate "undo-work") → outer-end
//	       [nested NON-INTERRUPTING event sub-process "n-esp", signal "boom"]
//	n-esp: n-esp-start(signal "boom", non-interrupting) → n-esp-gate(UserTask) → n-esp-end
//
// The ordering is the point: "work" completes (recording a compensation record in
// the OUTER scope) and the outer scope drains, but the outer scope is held open
// because the non-interrupting event sub-process alongside it is still running.
// When that event sub-process finally exits, it is the one that closes the
// enclosing scope — so it, not the regular sub-process exit, must archive the
// enclosing scope's records.
func espOutlivingEnclosingScopeDef() *model.ProcessDefinition {
	espBody := &model.ProcessDefinition{
		ID: "outlive-esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("n-esp-start", event.WithSignalName("boom"), event.WithNonInterrupting()),
			activity.NewUserTask("n-esp-gate", activity.WithEligibleRoles("ops")),
			event.NewEnd("n-esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "oef1", Source: "n-esp-start", Target: "n-esp-gate"},
			{ID: "oef2", Source: "n-esp-gate", Target: "n-esp-end"},
		},
	}
	outer := &model.ProcessDefinition{
		ID: "outlive-outer-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewServiceTask("work",
				activity.WithTaskAction("do-work"),
				activity.WithCompensateAction("undo-work")),
			event.NewEnd("outer-end"),
			activity.NewSubProcess("n-esp", espBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "oof1", Source: "outer-start", Target: "work"},
			{ID: "oof2", Source: "work", Target: "outer-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "esp-outlives-enclosing", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", outer),
			event.NewEnd("root-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outer"},
			{ID: "f2", Source: "outer", Target: "root-end"},
		},
	}
}

// TestNestedEventSubprocessExitArchivesEnclosingScope pins the second archive:
// exitNestedEventSubprocessScope closes the ENCLOSING scope, and
// without archiving first, compensable work that scope completed before the event
// sub-process outlived it is dropped. It is the same defect as the :283 one, on
// the scope the event sub-process prunes rather than on its own.
func TestNestedEventSubprocessExitArchivesEnclosingScope(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 14, 0, 0, 0, time.UTC)
	def := espOutlivingEnclosingScopeDef()
	actor := authz.Actor{ID: "alice", Roles: []string{"ops"}}

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "outlive-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	var workCmdID string
	for _, cmd := range r1.Commands {
		if inv, ok := cmd.(engine.InvokeAction); ok && inv.Name == "do-work" {
			workCmdID = inv.CommandID
		}
	}
	require.NotEmpty(t, workCmdID, "fixture: the compensable activity must have been invoked")

	// 1. Fire the non-interrupting event sub-process alongside the still-running
	//    outer scope; it parks at its own user task.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(at.Add(time.Minute), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.Scopes, 2, "fixture: the event sub-process scope must run ALONGSIDE the outer scope")

	// 2. Complete the compensable activity: the outer scope drains and records a
	//    compensation entry, but stays open because the event sub-process is live.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Minute), workCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r3.State.Scopes, 2,
		"fixture: the outer scope must be held open by the event sub-process running alongside it")

	// 3. Complete the event sub-process. IT is what closes the enclosing scope.
	gateTaskID := taskIDForNode(t, r3.State, "n-esp-gate")
	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewHumanCompleted(at.Add(3*time.Minute), gateTaskID, engine.CompletionInput{}, actor),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, r4.State.Status, "fixture: the instance must have run to completion")

	require.Len(t, r4.State.ArchivedCompensations["outer"], 1,
		"the enclosing scope's compensable work must survive the event sub-process pruning that scope")
	assert.Equal(t, "undo-work", r4.State.ArchivedCompensations["outer"][0].Action)
	assert.Equal(t, "work", r4.State.ArchivedCompensations["outer"][0].NodeID)
}

// ── Fixture: a nested ESP exiting beside a busy SIBLING scope subtree ─────────

// nestedEventSubprocessBesideBusySiblingDef builds
//
//	root:  start → outer(SubProcess) → root-end
//	outer: outer-start → fork ⇒ { A: sub(SubProcess) → a-end
//	                              B: gate(UserTask)  → b-end }
//	       [nested NON-INTERRUPTING event sub-process "n-esp", signal "boom"]
//	n-esp: n-esp-start(signal "boom", non-interrupting) → n-esp-gate(UserTask) → n-esp-end
//	sub:   q-start → qdeep(SubProcess) → q-end
//	qdeep: r-start → r-task(UserTask) → r-end
//
// When the event sub-process exits, its enclosing scope (outer) holds no tokens
// and its sibling scope "sub" holds none of its own either — the live token is
// one level further down, in "qdeep". A direct-children scan therefore declared
// the enclosing scope drained and closeScope pruned outer, sub AND qdeep,
// orphaning that token.
func nestedEventSubprocessBesideBusySiblingDef() *model.ProcessDefinition {
	qdeep := &model.ProcessDefinition{
		ID: "sib-qdeep-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("r-start"),
			activity.NewUserTask("r-task", activity.WithEligibleRoles("ops")),
			event.NewEnd("r-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "srf1", Source: "r-start", Target: "r-task"},
			{ID: "srf2", Source: "r-task", Target: "r-end"},
		},
	}
	sub := &model.ProcessDefinition{
		ID: "sib-sub-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("q-start"),
			activity.NewSubProcess("qdeep", qdeep),
			event.NewEnd("q-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "sqf1", Source: "q-start", Target: "qdeep"},
			{ID: "sqf2", Source: "qdeep", Target: "q-end"},
		},
	}
	espBody := &model.ProcessDefinition{
		ID: "sib-esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("n-esp-start", event.WithSignalName("boom"), event.WithNonInterrupting()),
			activity.NewUserTask("n-esp-gate", activity.WithEligibleRoles("ops")),
			event.NewEnd("n-esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "sef1", Source: "n-esp-start", Target: "n-esp-gate"},
			{ID: "sef2", Source: "n-esp-gate", Target: "n-esp-end"},
		},
	}
	outer := &model.ProcessDefinition{
		ID: "sib-outer-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			gateway.NewParallel("fork"),
			activity.NewSubProcess("sub", sub),
			event.NewEnd("a-end"),
			activity.NewUserTask("gate", activity.WithEligibleRoles("ops")),
			event.NewEnd("b-end"),
			activity.NewSubProcess("n-esp", espBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "sof1", Source: "outer-start", Target: "fork"},
			{ID: "sof2", Source: "fork", Target: "sub"},
			{ID: "sof3", Source: "sub", Target: "a-end"},
			{ID: "sof4", Source: "fork", Target: "gate"},
			{ID: "sof5", Source: "gate", Target: "b-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "nested-esp-beside-busy-sibling", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", outer),
			event.NewEnd("root-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outer"},
			{ID: "f2", Source: "outer", Target: "root-end"},
		},
	}
}

// TestNestedEventSubprocessExitBlocksOnBusySiblingSubtree pins the widened drain
// check in exitNestedEventSubprocessScope — the third of the three
// replaced direct-children scans. The wedge is the same one the other two
// produce: the sibling scope's own token count is zero because the live token
// sits in ITS child, so the enclosing scope was closed out from under it.
func TestNestedEventSubprocessExitBlocksOnBusySiblingSubtree(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 15, 0, 0, 0, time.UTC)
	def := nestedEventSubprocessBesideBusySiblingDef()
	actor := authz.Actor{ID: "alice", Roles: []string{"ops"}}

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "sibling-1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.State.Scopes, 3, "fixture: outer, sub and qdeep must all be open")
	require.Equal(t, r1.State.Scopes[1].ID, r1.State.Scopes[2].ParentID,
		"fixture: qdeep must be a child of sub, so sub itself holds no tokens")

	// 1. Spawn the non-interrupting event sub-process alongside the outer scope.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(at.Add(time.Minute), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.Scopes, 4, "fixture: the event sub-process scope must run alongside outer's other children")

	// 2. Drain the outer scope's own token so only the sibling subtree and the
	//    event sub-process remain.
	gateTaskID := taskIDForNode(t, r2.State, "gate")
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewHumanCompleted(at.Add(2*time.Minute), gateTaskID, engine.CompletionInput{}, actor),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r3.State.Scopes, 4, "fixture: the outer scope must be held open by its still-busy children")

	// 3. Exit the event sub-process. Its enclosing scope must NOT be pruned: the
	//    sibling subtree still holds the live token.
	espTaskID := taskIDForNode(t, r3.State, "n-esp-gate")
	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewHumanCompleted(at.Add(3*time.Minute), espTaskID, engine.CompletionInput{}, actor),
		engine.StepOptions{})
	require.NoError(t, err, "the event sub-process exit must not error")
	assertTokenScopesResolve(t, r4.State)

	// 4. The load-bearing assertion: the pre-fix failure surfaces on the NEXT Step.
	rTaskID := taskIDForNode(t, r4.State, "r-task")
	r5, err := engine.Step(t.Context(), def, r4.State,
		engine.NewHumanCompleted(at.Add(4*time.Minute), rTaskID, engine.CompletionInput{}, actor),
		engine.StepOptions{})
	require.NoError(t, err, "the instance is wedged: no subsequent Step can advance or terminate it")
	assert.Equal(t, engine.StatusCompleted, r5.State.Status)
	assert.Empty(t, r5.State.Tokens, "a completed instance carries no tokens")
	assert.Empty(t, r5.State.Scopes, "a completed instance carries no open scopes")
}

// ── Fixture: the drain guard hit MID-DRIVE, with another token still active ───

// eventSubprocessMicroDrainGuardDef builds the one shape in which the `stop`
// value returned by exitEventSubprocessScope's drain guard is observable:
//
//	root:  start → host(UserTask) → root-fork ⇒ { root-x(UserTask) → root-end-x
//	                                              root-y(UserTask) → root-end-y }
//	       [root-level NON-INTERRUPTING event sub-process "esp", signal "boom"]
//	esp:   esp-start(signal "boom", non-interrupting) → esp-fork
//	                       ⇒ { A: esp-inner(SubProcess) → esp-a-end
//	                           B: esp-b-end }
//	inner: inner-start → inner-task(UserTask) → inner-end
//
// Under Micro the signal Step stops as soon as branch A opens the nested scope,
// leaving branch B active ON the event sub-process's end event and the nested
// branch active on its start node. The next Step then resumes "host" into a
// parallel fork — an auto-advancing node, so drive does NOT stop there — and
// reaches branch B's end event while the nested branch is STILL active. That is
// the only moment at which "halt the drive" and "keep advancing" differ.
//
// The root fork is load-bearing scaffolding: it is what lets drive reach the end
// event without stopping first, and its two branches are never advanced by the
// Step under test.
func eventSubprocessMicroDrainGuardDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "micro-inner-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewUserTask("inner-task", activity.WithEligibleRoles("ops")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "mif1", Source: "inner-start", Target: "inner-task"},
			{ID: "mif2", Source: "inner-task", Target: "inner-end"},
		},
	}
	espBody := &model.ProcessDefinition{
		ID: "micro-esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("esp-start", event.WithSignalName("boom"), event.WithNonInterrupting()),
			gateway.NewParallel("esp-fork"),
			activity.NewSubProcess("esp-inner", inner),
			event.NewEnd("esp-a-end"),
			event.NewEnd("esp-b-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "mef1", Source: "esp-start", Target: "esp-fork"},
			{ID: "mef2", Source: "esp-fork", Target: "esp-inner"},
			{ID: "mef3", Source: "esp-inner", Target: "esp-a-end"},
			{ID: "mef4", Source: "esp-fork", Target: "esp-b-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "micro-esp-drain-guard", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("host", activity.WithEligibleRoles("ops")),
			gateway.NewParallel("root-fork"),
			activity.NewUserTask("root-x", activity.WithEligibleRoles("ops")),
			event.NewEnd("root-end-x"),
			activity.NewUserTask("root-y", activity.WithEligibleRoles("ops")),
			event.NewEnd("root-end-y"),
			activity.NewSubProcess("esp", espBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "host"},
			{ID: "f2", Source: "host", Target: "root-fork"},
			{ID: "f3", Source: "root-fork", Target: "root-x"},
			{ID: "f4", Source: "root-x", Target: "root-end-x"},
			{ID: "f5", Source: "root-fork", Target: "root-y"},
			{ID: "f6", Source: "root-y", Target: "root-end-y"},
		},
	}
}

// tokenAtNode returns the surviving token sitting on nodeID, or nil.
func tokenAtNode(st engine.InstanceState, nodeID string) *engine.Token {
	for i := range st.Tokens {
		if st.Tokens[i].NodeID == nodeID {
			return &st.Tokens[i]
		}
	}
	return nil
}

// TestEventSubprocessDrainGuardDoesNotHaltMicroDrive pins the `stop` value the
// new drain guard returns (engine/step_nodes.go, exitEventSubprocessScope). It
// must be false — "this scope is not drained yet", the same answer every other
// not-drained return on this path gives — so drive keeps advancing the tokens the
// guard just declined to prune. Returning true instead parks the instance:
// drive halts, and the very branch whose live token BLOCKED the exit is left
// stranded on its start node.
//
// The value is observable only under Micro, and only when another token is still
// active at the instant the guard returns; see eventSubprocessMicroDrainGuardDef
// for why. Under Macro the returned bool provably cannot be observed at this
// site: endEventStrategy has already consumed the token, removeToken reallocates
// s.Tokens, so the `tok.State = TokenWaiting` write lands on a detached array —
// and drive reads the resulting `stopped` only under `mode == Micro`.
func TestEventSubprocessDrainGuardDoesNotHaltMicroDrive(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 16, 0, 0, 0, time.UTC)
	def := eventSubprocessMicroDrainGuardDef()
	actor := authz.Actor{ID: "alice", Roles: []string{"ops"}}
	micro := engine.StepOptions{Mode: engine.Micro}

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "micro-guard-1"},
		engine.NewStartInstance(at, nil), micro)
	require.NoError(t, err)
	require.Len(t, r1.State.Tokens, 1, "fixture: the root host task is parked")
	require.Equal(t, "host", r1.State.Tokens[0].NodeID)

	// Spawn the non-interrupting event sub-process. Micro stops the drive as soon
	// as branch A opens the nested scope, leaving TWO active tokens behind.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(at.Add(time.Minute), "boom", nil), micro)
	require.NoError(t, err)
	require.Len(t, r2.State.Scopes, 2, "fixture: the event sub-process scope and the nested scope must both be open")

	endTok := tokenAtNode(r2.State, "esp-b-end")
	require.NotNil(t, endTok, "fixture: branch B must be sitting ON the event sub-process's end event, not yet entered")
	require.Equal(t, engine.TokenActive, endTok.State, "fixture: branch B must still be ACTIVE — Micro left it for the next Step")
	nestedTok := tokenAtNode(r2.State, "inner-start")
	require.NotNil(t, nestedTok, "fixture: the nested branch must be sitting on its start node")
	require.Equal(t, engine.TokenActive, nestedTok.State, "fixture: the nested branch must still be ACTIVE at the moment the guard runs")
	require.Equal(t, r2.State.Scopes[1].ID, nestedTok.ScopeID, "fixture: the blocking token must be in the NESTED scope")

	// Resume "host" into the root parallel fork. The fork auto-advances, so drive
	// does not stop there and goes on to branch B's end event — where the drain
	// guard fires while the nested branch is still active.
	hostTaskID := taskIDForNode(t, r2.State, "host")
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewHumanCompleted(at.Add(2*time.Minute), hostTaskID, engine.CompletionInput{}, actor), micro)
	require.NoError(t, err)

	// stop=false: drive carried on past the guard and advanced the blocking branch
	// to its user task within this same Step.
	assert.NotNil(t, tokenAtNode(r3.State, "inner-task"),
		"the drain guard must not halt drive: the nested branch it declined to prune has to keep advancing")
	assert.Nil(t, tokenAtNode(r3.State, "inner-start"),
		"the nested branch must not be left stranded on its start node")
	var minted bool
	for _, cmd := range r3.Commands {
		if ah, ok := cmd.(engine.AwaitHuman); ok && ah.TaskID == taskIDForNode(t, r3.State, "inner-task") {
			minted = true
		}
	}
	assert.True(t, minted, "the nested branch's human task must be minted in the SAME Step the guard ran in")
}

// ── Fixture: an enclosing sub-process carrying its OWN CompensateAction ───────

// enclosingCompensationDef builds
//
//	root:    start → fulfil(SubProcess, compensate "undo-fulfil") → root-end
//	fulfil:  fulfil-start → fulfil-gate(UserTask) → fulfil-end
//	         [nested event sub-process "n-esp", present only when espStart != nil]
//	n-esp:   espStart(signal "boom") → n-esp-gate(UserTask) → n-esp-end
//
// The point is the CompensateAction on "fulfil" ITSELF — the sub-process node,
// not any activity inside it. Which exit closes "fulfil"'s scope is what espStart
// selects:
//
//   - nil                       → exitRegularSubprocessScope closes it.
//   - non-interrupting ESP start → "fulfil" drains but is held open by the child
//     scope beside it, so exitNestedEventSubprocessScope closes it.
//   - interrupting ESP start     → the interrupt cancels "fulfil"'s subtree and
//     keeps its scope open (closeScopeDescendants), so
//     exitNestedEventSubprocessScope closes it.
//
// All three must produce the same compensation record; only the first did before
// the /code-review gate.
func enclosingCompensationDef(espStart model.Node) *model.ProcessDefinition {
	fulfilNodes := []model.Node{
		event.NewStart("fulfil-start"),
		activity.NewUserTask("fulfil-gate", activity.WithEligibleRoles("ops")),
		event.NewEnd("fulfil-end"),
	}
	if espStart != nil {
		fulfilNodes = append(fulfilNodes, activity.NewSubProcess("n-esp", &model.ProcessDefinition{
			ID: "enclosing-comp-esp-body", Version: 1,
			Nodes: []model.Node{
				espStart,
				activity.NewUserTask("n-esp-gate", activity.WithEligibleRoles("ops")),
				event.NewEnd("n-esp-end"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "cef1", Source: espStart.ID(), Target: "n-esp-gate"},
				{ID: "cef2", Source: "n-esp-gate", Target: "n-esp-end"},
			},
		}))
	}
	fulfil := &model.ProcessDefinition{
		ID: "enclosing-comp-fulfil-body", Version: 1, Nodes: fulfilNodes,
		Flows: []flow.SequenceFlow{
			{ID: "cff1", Source: "fulfil-start", Target: "fulfil-gate"},
			{ID: "cff2", Source: "fulfil-gate", Target: "fulfil-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "compensable-enclosing-subprocess", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("fulfil", fulfil,
				activity.WithCompensateAction("undo-fulfil")),
			event.NewEnd("root-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fulfil"},
			{ID: "f2", Source: "fulfil", Target: "root-end"},
		},
	}
}

// TestSubProcessExitRecordsItsOwnCompensation pins the gap the /code-review gate
// found: exitNestedEventSubprocessScope closes the ENCLOSING
// sub-process's scope and resumes in the grandparent along the same shape as
// exitRegularSubprocessScope, but omitted the one step that records the enclosing
// sub-process node's OWN CompensateAction. With that step missing the sub-process
// is silently non-compensable — a later CompensateRequested or error rollback
// walks straight past it.
//
// The regular-exit case is here for the same reason: that call site shipped
// unpinned, and mutating it away broke no test in the package. Both call sites
// now go through recordSubProcessCompensation, and both are mutation-verified.
//
// Every case ends with the SAME Step shape — completing the human task whose
// node's exit closes "fulfil"'s scope — so the cases differ only in which exit
// that is.
func TestSubProcessExitRecordsItsOwnCompensation(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 3, 15, 0, 0, 0, time.UTC)
	actor := authz.Actor{ID: "alice", Roles: []string{"ops"}}
	// step drives one HumanCompleted against the task parked on nodeID.
	step := func(t *testing.T, def *model.ProcessDefinition, st engine.InstanceState, offset time.Duration, nodeID string) engine.StepResult {
		t.Helper()
		res, err := engine.Step(t.Context(), def, st,
			engine.NewHumanCompleted(at.Add(offset), taskIDForNode(t, st, nodeID), engine.CompletionInput{}, actor),
			engine.StepOptions{})
		require.NoError(t, err)
		return res
	}

	tests := []struct {
		name string
		def  *model.ProcessDefinition
		// closingNode is the node whose human task, once completed, drives the
		// exit that closes "fulfil"'s scope.
		closingNode string
		// arrange drives the instance to the point where closingNode is parked.
		arrange func(t *testing.T, def *model.ProcessDefinition, instanceID string) engine.InstanceState
		assert  func(t *testing.T, got engine.InstanceState)
	}{
		{
			name:        "regular sub-process exit",
			def:         enclosingCompensationDef(nil),
			closingNode: "fulfil-gate",
			arrange: func(t *testing.T, def *model.ProcessDefinition, instanceID string) engine.InstanceState {
				t.Helper()
				r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
					engine.NewStartInstance(at, nil), engine.StepOptions{})
				require.NoError(t, err)
				require.Len(t, r1.State.Scopes, 1, "fixture: the enclosing scope must be open")
				return r1.State
			},
			assert: func(t *testing.T, got engine.InstanceState) {
				t.Helper()
				assert.Equal(t, engine.StatusCompleted, got.Status, "fixture: the instance must run to completion")
			},
		},
		{
			name:        "non-interrupting event sub-process outliving the enclosing scope",
			def:         enclosingCompensationDef(event.NewStart("n-esp-start", event.WithSignalName("boom"), event.WithNonInterrupting())),
			closingNode: "n-esp-gate",
			arrange: func(t *testing.T, def *model.ProcessDefinition, instanceID string) engine.InstanceState {
				t.Helper()
				r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
					engine.NewStartInstance(at, nil), engine.StepOptions{})
				require.NoError(t, err)
				// Fire the event sub-process alongside the still-running enclosing scope.
				r2, err := engine.Step(t.Context(), def, r1.State,
					engine.NewSignalReceived(at.Add(time.Minute), "boom", nil), engine.StepOptions{})
				require.NoError(t, err)
				require.Len(t, r2.State.Scopes, 2,
					"fixture: the event sub-process scope must run ALONGSIDE the enclosing scope")
				// Drain the enclosing scope: it is held open by the child beside it.
				r3 := step(t, def, r2.State, 2*time.Minute, "fulfil-gate")
				require.Len(t, r3.State.Scopes, 2,
					"fixture: the enclosing scope must be held open by the event sub-process beside it")
				return r3.State
			},
			assert: func(t *testing.T, got engine.InstanceState) {
				t.Helper()
				assert.Equal(t, engine.StatusCompleted, got.Status, "fixture: the instance must run to completion")
			},
		},
		{
			name:        "interrupting event sub-process pruning the enclosing scope",
			def:         enclosingCompensationDef(event.NewStart("n-esp-start", event.WithSignalName("boom"))),
			closingNode: "n-esp-gate",
			arrange: func(t *testing.T, def *model.ProcessDefinition, instanceID string) engine.InstanceState {
				t.Helper()
				r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
					engine.NewStartInstance(at, nil), engine.StepOptions{})
				require.NoError(t, err)
				// The interrupt cancels the enclosing scope's token but KEEPS its scope
				// open (closeScopeDescendants), so the event sub-process prunes it later.
				r2, err := engine.Step(t.Context(), def, r1.State,
					engine.NewSignalReceived(at.Add(time.Minute), "boom", nil), engine.StepOptions{})
				require.NoError(t, err)
				require.Nil(t, tokenAtNode(r2.State, "fulfil-gate"),
					"fixture: the interrupt must have cancelled the enclosing scope's token")
				require.Len(t, r2.State.Scopes, 2,
					"fixture: the enclosing scope must survive the interrupt so its exit runs here")
				return r2.State
			},
			assert: func(t *testing.T, got engine.InstanceState) {
				t.Helper()
				assert.Equal(t, engine.StatusCompleted, got.Status, "fixture: the instance must run to completion")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parked := tt.arrange(t, tt.def, "enc-comp")
			res := step(t, tt.def, parked, 3*time.Minute, tt.closingNode)

			require.Len(t, res.State.RootCompensations, 1,
				"the enclosing sub-process's own CompensateAction must be recorded in the grandparent scope "+
					"whichever exit closes that scope")
			rec := res.State.RootCompensations[0]
			assert.Equal(t, "undo-fulfil", rec.Action)
			assert.Equal(t, "fulfil", rec.NodeID)
			assert.Equal(t, at.Add(3*time.Minute), rec.CompletedAt,
				"the snapshot is stamped with the step's trigger time, never a wall clock")
			tt.assert(t, res.State)
		})
	}
}

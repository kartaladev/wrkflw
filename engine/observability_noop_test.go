package engine

// observability_noop_test.go — asserts that the engine's deliberate
// silent no-op / swallowed-error paths emit an slog record (Warn for
// genuinely-anomalous no-ops, Debug for expected-but-worth-tracing swallows)
// without changing the (state, commands) output of the path. White-box
// (package engine) because several sites are only reachable by driving
// unexported helpers (drive, findDirectBoundary, handleCancelRequested)
// directly with a deliberately malformed InstanceState — the public Step API
// cannot reach a token parked on a genuinely-missing node or an unresolvable
// scope.
//
// These tests install a capturing slog.Handler via slog.SetDefault and MUST
// run sequentially (no t.Parallel()): slog.Default() is process-global state.

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

// captureHandler is a minimal slog.Handler that records emitted records for
// test assertions. mu guards records against concurrent Handle calls (slog
// itself may invoke handlers from multiple goroutines).
type captureHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

// find returns the first captured record whose message contains msgSubstr.
func (h *captureHandler) find(msgSubstr string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if strings.Contains(r.Message, msgSubstr) {
			return r, true
		}
	}
	return slog.Record{}, false
}

// attrString returns the string value of attribute key on r, if present.
func attrString(r slog.Record, key string) (string, bool) {
	var val string
	var found bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			val = a.Value.String()
			found = true
			return false
		}
		return true
	})
	return val, found
}

// installCaptureHandler swaps slog's process-global default logger for a
// capturing one, restoring the previous default via t.Cleanup. Callers must
// NOT run in parallel with each other (t.Parallel() forbidden on callers)
// since slog.Default() is shared process-global state.
func installCaptureHandler(t *testing.T) *captureHandler {
	t.Helper()
	prev := slog.Default()
	h := &captureHandler{}
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// ─────────────────────────────────────────────────────────────────────────────
// Site 1: late TimerFired on a terminal instance (handleTimerFired) — Warn.
// ─────────────────────────────────────────────────────────────────────────────

// TestHandleTimerFired_TerminalInstanceNoOp_LogsWarn verifies that a TimerFired
// delivered against an already-terminal instance both no-ops (behavior
// unchanged — see TestHandleTimerFired_TerminalInstanceNoOps) AND emits a Warn
// slog record carrying the instance id and timer id, so an operator can spot a
// late-arriving timer against a dead instance instead of it vanishing silently.
//
// ⚠ The emitting site moved, not the guarantee. The record now comes
// from dispatch's single terminal guard rather than handleTimerFired's own, so
// the MESSAGE is the guard's uniform "trigger rejected on terminal instance"
// instead of the old timer-specific "timer fired on terminal instance" — one
// constant message plus structured attributes, which is both the point of
// collapsing eight guards and the better logging shape (an operator filters on
// trigger=engine.TimerFired rather than grepping prose). Every OTHER assertion
// below is unchanged and deliberately so: Warn level, instance_id, and above
// all timer_id are what this test exists to protect, and they are exactly what a
// naive collapse would have silently dropped.
func TestHandleTimerFired_TerminalInstanceNoOp_LogsWarn(t *testing.T) {
	h := installCaptureHandler(t)

	def := timerBoundaryTerminalDef()
	s := InstanceState{
		InstanceID: "i1",
		Status:     StatusFailed,
		Boundaries: []boundaryArm{
			{HostToken: "h1", HostNode: "work", BoundaryNode: "bnd", Flow: "f3", triggerMatch: triggerMatch{TimerID: "bt1"}},
		},
	}

	res, err := Step(t.Context(), def, s, NewTimerFired(time.Unix(1, 0), "bt1"), StepOptions{})
	require.NoError(t, err)
	assert.Empty(t, res.Commands, "a terminal instance must not fire the boundary")
	assert.Equal(t, StatusFailed, res.State.Status, "status unchanged")

	rec, ok := h.find("trigger rejected on terminal instance")
	require.True(t, ok, "expected a Warn log for a late timer against a terminal instance")
	assert.Equal(t, slog.LevelWarn, rec.Level)

	instanceID, ok := attrString(rec, "instance_id")
	assert.True(t, ok, "expected instance_id attribute")
	assert.Equal(t, "i1", instanceID)

	timerID, ok := attrString(rec, "timer_id")
	assert.True(t, ok, "expected timer_id attribute")
	assert.Equal(t, "bt1", timerID)
}

// TestTerminalGuardLogsTriggerIdentity pins the enrichment that keeps site 1
// diagnosable after eight per-handler guards were collapsed into one.
//
// A single generic log line would have cost the operator the ONE field they
// actually need — "why did my timer do nothing" is not answered by
// instance_id/trigger/status alone. The guard therefore appends the trigger's
// own identity field, read through the SAME registry validateTriggerKey uses
// (validatedTriggerKinds / exemptTriggerKinds), so there is no second
// hand-maintained mapping to drift.
//
// The cases are the shapes that registry can present, and the table is what
// stops the enrichment rotting when a trigger is added or reclassified:
//   - validated  → the row's accessor supplies the value (ActionCompleted).
//   - exempt WITH an identity field → the exempt row's optional accessor
//     supplies it (TimerFired). This is the case a naive
//     "validatedTriggerKinds only" implementation silently drops, and it is
//     precisely the timer case site 1 asserts.
//   - no identity field at all → the attribute is OMITTED cleanly, with no
//     empty string and no "<nil>" (CancelRequested).
//   - rejectWithError → the SAME line, distinguished only by outcome=errored
//     (ResolveIncident). Erroring is not a substitute for the record: the
//     caller's 422 is invisible to the operator reading logs, and
//     handleResolveIncident's own guard emitted a Warn, so an error-only arm
//     would make it the single replaced site that regressed.
//
// Not parallel, and no t.Parallel() in the subtests: installCaptureHandler
// swaps the process-global slog.Default().
func TestTerminalGuardLogsTriggerIdentity(t *testing.T) {
	at := time.Unix(1, 0)
	def := &model.ProcessDefinition{ID: "p-terminal-guard-log", Version: 1}
	terminal := InstanceState{InstanceID: "i1", Status: StatusFailed}

	type testCase struct {
		name    string
		trigger Trigger
		// wantErr is true for the rejectWithError rows, whose Step call returns
		// ErrInstanceTerminal while still emitting the same log line.
		wantErr bool
		assert  func(t *testing.T, rec slog.Record)
	}

	cases := []testCase{
		{
			name:    "validated trigger logs its own identity field",
			trigger: NewActionCompleted(at, "cmd-7", nil),
			assert: func(t *testing.T, rec slog.Record) {
				commandID, ok := attrString(rec, "command_id")
				assert.True(t, ok, "expected command_id attribute")
				assert.Equal(t, "cmd-7", commandID)
				outcome, _ := attrString(rec, "outcome")
				assert.Equal(t, "dropped", outcome)
			},
		},
		{
			name:    "exempt trigger with an identity field still logs it",
			trigger: NewTimerFired(at, "bt1"),
			assert: func(t *testing.T, rec slog.Record) {
				timerID, ok := attrString(rec, "timer_id")
				assert.True(t, ok,
					"TimerFired is EXEMPT from validateTriggerKey but still carries a TimerID; "+
						"an implementation reading only validatedTriggerKinds drops exactly this attribute")
				assert.Equal(t, "bt1", timerID)
				outcome, _ := attrString(rec, "outcome")
				assert.Equal(t, "dropped", outcome)
			},
		},
		{
			name:    "trigger with no identity field omits the attribute cleanly",
			trigger: NewCancelRequested(at),
			assert: func(t *testing.T, rec slog.Record) {
				var keys []string
				rec.Attrs(func(a slog.Attr) bool {
					keys = append(keys, a.Key)
					assert.NotEqual(t, "", a.Value.String(),
						"no attribute may be logged with an empty value")
					assert.NotEqual(t, "<nil>", a.Value.String(),
						"no attribute may be logged as <nil>")
					return true
				})
				assert.Equal(t, []string{"instance_id", "trigger", "status", "outcome"}, keys,
					"a trigger carrying no identity key must add no identity attribute")
			},
		},
		{
			// ⚠ What makes this able to fail: CompensateRequested's exempt row
			// registered NO accessor, justified by the false claim that "its
			// terminalPolicy is never rejectSilently, so the guard never logs it".
			// dispatch logs BOTH refusal flavours, and a ToNode-carrying rollback is
			// rejectWithError — so the line WAS emitted, with no node id on it, and
			// an operator could not tell which rollback was refused. Executed before
			// the fix: `msg="trigger rejected on terminal instance"
			// trigger=engine.CompensateRequested outcome=errored` and nothing more.
			name:    "a refused targeted rollback names the node it targeted",
			trigger: NewReverseToNode(at, "svc-charge"),
			wantErr: true,
			assert: func(t *testing.T, rec slog.Record) {
				target, ok := attrString(rec, "rollback_target")
				assert.True(t, ok,
					"a rollback refused on a terminal instance must name its target node; "+
						"instance_id/trigger/status/outcome alone cannot tell an operator "+
						"WHICH rollback was refused")
				assert.Equal(t, "svc-charge", target)
				outcome, _ := attrString(rec, "outcome")
				assert.Equal(t, "errored", outcome)
			},
		},
		{
			name:    "rejectWithError logs the same line with outcome=errored",
			trigger: NewResolveIncident(at, "inc-3", 1),
			wantErr: true,
			assert: func(t *testing.T, rec slog.Record) {
				incidentID, ok := attrString(rec, "incident_id")
				assert.True(t, ok,
					"handleResolveIncident's replaced guard logged incident_id at Warn; "+
						"an error-only arm would lose that record entirely")
				assert.Equal(t, "inc-3", incidentID)
				outcome, _ := attrString(rec, "outcome")
				assert.Equal(t, "errored", outcome,
					"the two refusal flavours share one message and are told apart by this attribute")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := installCaptureHandler(t)

			res, err := Step(t.Context(), def, terminal, tc.trigger, StepOptions{})
			if tc.wantErr {
				require.ErrorIs(t, err, ErrInstanceTerminal)
			} else {
				require.NoError(t, err)
			}
			assert.Empty(t, res.Commands)

			rec, ok := h.find("trigger rejected on terminal instance")
			require.True(t, ok, "expected a Warn log for a trigger refused on a terminal instance")
			assert.Equal(t, slog.LevelWarn, rec.Level)

			// The four fields every line carries regardless of trigger type.
			instanceID, ok := attrString(rec, "instance_id")
			assert.True(t, ok, "expected instance_id attribute")
			assert.Equal(t, "i1", instanceID)
			trigger, ok := attrString(rec, "trigger")
			assert.True(t, ok, "expected trigger attribute")
			assert.Equal(t, triggerTypeName(tc.trigger), trigger)
			status, ok := attrString(rec, "status")
			assert.True(t, ok, "expected status attribute")
			assert.Equal(t, "failed", status)
			_, ok = attrString(rec, "outcome")
			assert.True(t, ok, "expected outcome attribute")

			tc.assert(t, rec)
		})
	}
}

// TestTerminalRollbackWithNothingToCompensateIsLogged covers the THIRD refusal of
// a trigger on a terminal instance — the one dispatch cannot make, because its
// predicate reads state rather than the trigger: a plain
// full rollback is waved through as allowOnTerminal, then refused inside
// stepCompensateRequested when no record survives to walk.
//
// dispatch's own reasoning is what makes this a gap rather than a style point:
// it logs BOTH refusal flavours because "a caller's 422 is not visible to the
// operator reading logs", and refuses to let an error-only arm be the single
// replaced site that regresses. This third refusal returned exactly such an
// error-only 422 — an admin full-rollback of a finished instance vanished from
// the logs entirely.
//
// ⚠ What makes this test able to fail: before the fix the branch returned its
// error with no slog call anywhere on the path, so h.find returned ok=false.
//
// Not parallel: installCaptureHandler swaps the process-global slog.Default().
func TestTerminalRollbackWithNothingToCompensateIsLogged(t *testing.T) {
	h := installCaptureHandler(t)

	at := time.Unix(1, 0)
	def := &model.ProcessDefinition{ID: "p-terminal-rollback-log", Version: 1}
	// Terminal, and carrying NO compensation records — so the walk would find
	// nothing and only re-stamp the terminal transition.
	terminal := InstanceState{InstanceID: "i1", Status: StatusFailed}

	// A PLAIN full rollback: both ToNode and ReverseNode empty, so terminalPolicy
	// is allowOnTerminal and dispatch does not refuse it.
	_, err := Step(t.Context(), def, terminal, NewCompensateRequested(at, ""), StepOptions{})

	require.ErrorIs(t, err, ErrInstanceTerminal)

	rec, ok := h.find("trigger rejected on terminal instance")
	require.True(t, ok,
		"the third refusal must leave the SAME operator record as the two dispatch makes; "+
			"a 422 the caller sees is not a record the operator sees")
	assert.Equal(t, slog.LevelWarn, rec.Level)

	instanceID, ok := attrString(rec, "instance_id")
	assert.True(t, ok)
	assert.Equal(t, "i1", instanceID)
	trigger, ok := attrString(rec, "trigger")
	assert.True(t, ok)
	assert.Equal(t, "engine.CompensateRequested", trigger)
	outcome, ok := attrString(rec, "outcome")
	assert.True(t, ok)
	assert.Equal(t, "errored", outcome,
		"it is a refusal that errors, so it shares the errored outcome with dispatch's rejectWithError arm")
	reason, ok := attrString(rec, "reason")
	assert.True(t, ok,
		"this refusal is NOT interchangeable with dispatch's: same message and instance, "+
			"different cause, so the line must say which one fired")
	assert.Equal(t, "nothing left to compensate", reason)
}

// ─────────────────────────────────────────────────────────────────────────────
// Site 2: missing-node park in drive (engine/step.go) — Warn.
// ─────────────────────────────────────────────────────────────────────────────

// TestDrive_MissingNodePark_LogsWarn verifies that drive parking a token on a
// NodeID absent from the effective definition (a defensive path — should be
// unreachable for a well-formed state built by Step, but the engine must not
// spin if it happens) both parks the token (behavior unchanged) AND emits a
// Warn slog record carrying the instance id, token id, and the missing node id.
func TestDrive_MissingNodePark_LogsWarn(t *testing.T) {
	h := installCaptureHandler(t)

	def := &model.ProcessDefinition{ID: "p-missing-node", Version: 1}
	s := InstanceState{
		InstanceID: "i1",
		Status:     StatusRunning,
		Tokens: []Token{
			{ID: "t1", NodeID: "does-not-exist", State: TokenActive},
		},
	}

	cmds, err := drive(t.Context(), def, &s, time.Unix(1, 0), resolvePolicy(StepOptions{}))
	require.NoError(t, err)
	assert.Empty(t, cmds)
	require.Len(t, s.Tokens, 1)
	assert.Equal(t, TokenWaiting, s.Tokens[0].State, "token must park rather than spin")

	rec, ok := h.find("token routed to a missing node")
	require.True(t, ok, "expected a Warn log for a token parked on a missing node")
	assert.Equal(t, slog.LevelWarn, rec.Level)

	instanceID, ok := attrString(rec, "instance_id")
	assert.True(t, ok, "expected instance_id attribute")
	assert.Equal(t, "i1", instanceID)

	tokenID, ok := attrString(rec, "token_id")
	assert.True(t, ok, "expected token_id attribute")
	assert.Equal(t, "t1", tokenID)

	nodeID, ok := attrString(rec, "node_id")
	assert.True(t, ok, "expected node_id attribute")
	assert.Equal(t, "does-not-exist", nodeID)
}

// ─────────────────────────────────────────────────────────────────────────────
// Site 3: swallowed ErrorExpr eval error in boundary matching
// (engine/step_errors.go, findDirectBoundary) — Debug.
// ─────────────────────────────────────────────────────────────────────────────

// TestFindDirectBoundary_MalformedErrorExpr_LogsDebug verifies that a boundary
// whose ErrorExpr type-errors at runtime is both treated as a non-match
// (behavior unchanged — see TestMalformedErrorExprNonFatal) AND emits a Debug
// slog record carrying the host node id, the boundary node id, and the eval
// error, so the malformed expression is traceable without failing the walk.
func TestFindDirectBoundary_MalformedErrorExpr_LogsDebug(t *testing.T) {
	h := installCaptureHandler(t)

	hostDef := &model.ProcessDefinition{
		ID: "p-bad-expr", Version: 1,
		Nodes: []model.Node{
			event.NewBoundary("bnd-bad-expr", "svc", event.WithBoundaryErrorExpr(`_error + 42`)),
		},
	}

	_, matched := findDirectBoundary(t.Context(), hostDef, "svc", "REAL_CODE", nil, nil, resolveEvaluator(StepOptions{}))
	assert.False(t, matched, "a malformed ErrorExpr must be treated as non-match")

	rec, ok := h.find("boundary ErrorExpr eval error")
	require.True(t, ok, "expected a Debug log for a malformed ErrorExpr")
	assert.Equal(t, slog.LevelDebug, rec.Level)

	hostNodeID, ok := attrString(rec, "host_node_id")
	assert.True(t, ok, "expected host_node_id attribute")
	assert.Equal(t, "svc", hostNodeID)

	boundaryNodeID, ok := attrString(rec, "boundary_node_id")
	assert.True(t, ok, "expected boundary_node_id attribute")
	assert.Equal(t, "bnd-bad-expr", boundaryNodeID)

	errAttr, ok := attrString(rec, "error")
	assert.True(t, ok, "expected error attribute")
	assert.NotEmpty(t, errAttr)
}

// ─────────────────────────────────────────────────────────────────────────────
// Site 4: cancel-path swallowed defForScope error (handleCancelRequested) —
// Debug.
// ─────────────────────────────────────────────────────────────────────────────

// TestHandleCancelRequested_DefForScopeError_LogsDebug verifies that a token
// whose ScopeID cannot be resolved (a defensive path — cancel must not fail on
// it) both skips the token's per-node cancel handler (behavior unchanged) AND
// emits a Debug slog record carrying the instance id, token id, scope id, and
// the resolution error.
func TestHandleCancelRequested_DefForScopeError_LogsDebug(t *testing.T) {
	h := installCaptureHandler(t)

	def := &model.ProcessDefinition{ID: "p-cancel-bad-scope", Version: 1}
	s := InstanceState{
		InstanceID: "i1",
		Status:     StatusRunning,
		Tokens: []Token{
			{ID: "t1", NodeID: "n1", ScopeID: "no-such-scope", State: TokenActive},
		},
	}

	res, err := handleCancelRequested(t.Context(), def, &s, NewCancelRequested(time.Unix(1, 0)), StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, StatusTerminated, res.State.Status)

	rec, ok := h.find("cancel: scope resolution error")
	require.True(t, ok, "expected a Debug log for an unresolvable token scope during cancel")
	assert.Equal(t, slog.LevelDebug, rec.Level)

	instanceID, ok := attrString(rec, "instance_id")
	assert.True(t, ok, "expected instance_id attribute")
	assert.Equal(t, "i1", instanceID)

	tokenID, ok := attrString(rec, "token_id")
	assert.True(t, ok, "expected token_id attribute")
	assert.Equal(t, "t1", tokenID)

	scopeID, ok := attrString(rec, "scope_id")
	assert.True(t, ok, "expected scope_id attribute")
	assert.Equal(t, "no-such-scope", scopeID)

	errAttr, ok := attrString(rec, "error")
	assert.True(t, ok, "expected error attribute")
	assert.NotEmpty(t, errAttr)
}

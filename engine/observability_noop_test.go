package engine

// observability_noop_test.go — Task C7: asserts that the engine's deliberate
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

	rec, ok := h.find("timer fired on terminal instance")
	require.True(t, ok, "expected a Warn log for a late timer against a terminal instance")
	assert.Equal(t, slog.LevelWarn, rec.Level)

	instanceID, ok := attrString(rec, "instance_id")
	assert.True(t, ok, "expected instance_id attribute")
	assert.Equal(t, "i1", instanceID)

	timerID, ok := attrString(rec, "timer_id")
	assert.True(t, ok, "expected timer_id attribute")
	assert.Equal(t, "bt1", timerID)
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

	cmds, err := drive(t.Context(), def, &s, time.Unix(1, 0), Macro, resolveEvaluator(StepOptions{}))
	require.NoError(t, err)
	assert.Empty(t, cmds)
	require.Len(t, s.Tokens, 1)
	assert.Equal(t, TokenWaitingCommand, s.Tokens[0].State, "token must park rather than spin")

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

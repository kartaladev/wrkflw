package httpcore_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/action/httpcall"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/validation"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

func TestClassifyError(t *testing.T) {
	tests := map[string]struct {
		err    error
		assert func(t *testing.T, status int, body httpcore.ErrorBody)
	}{
		"not found": {
			err: fmt.Errorf("wrap: %w", kernel.ErrInstanceNotFound),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusNotFound || body.Error != "not_found" {
					t.Fatalf("got %d/%q", status, body.Error)
				}
			},
		},
		"forbidden": {
			err: authz.ErrNotAuthorized,
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusForbidden || body.Error != "forbidden" {
					t.Fatalf("got %d/%q", status, body.Error)
				}
			},
		},
		"bad input keeps message": {
			err: fmt.Errorf("%w: def_ref required", httpcore.ErrBadInput),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusBadRequest || body.Message == "" {
					t.Fatalf("4xx must keep message; got %d/%q", status, body.Message)
				}
			},
		},
		"validation invalid input -> 400": {
			err: fmt.Errorf("wrap: %w", validation.ErrInvalidInput),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusBadRequest || body.Error != "bad_request" {
					t.Fatalf("got %d/%q", status, body.Error)
				}
			},
		},
		"malformed armed-timer cursor -> 400": {
			// A cursor an operator pasted wrong must be an actionable 400, never a
			// silent reset to page one — which would loop a large listing forever
			// without the operator noticing (ADR-0159).
			err: fmt.Errorf("wrap: %w", kernel.ErrBadArmedTimerCursor),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusBadRequest || body.Error != "bad_request" {
					t.Fatalf("got %d/%q", status, body.Error)
				}
			},
		},
		"internal hides message": {
			err: errors.New("pgx: connection refused at 10.0.0.5:5432"),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusInternalServerError {
					t.Fatalf("status=%d", status)
				}
				if body.Error != "internal_error" || body.Message != "" {
					t.Fatalf("5xx must not leak: error=%q message=%q", body.Error, body.Message)
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			status, body := httpcore.ClassifyError(tc.err)
			tc.assert(t, status, body)
		})
	}
}

// TestClassifyErrorOutcomeSentinels pins that the two user-task outcome sentinels
// are client errors, not server errors. Both describe a bad completion payload the
// caller can correct — an outcome outside the node's declared set, or a missing
// outcome on a node that declares one — so answering 500 tells the client nothing
// and hides a 4xx behind an opaque body (ADR-0146).
func TestClassifyErrorOutcomeSentinels(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		err    error
		assert func(t *testing.T, status int, body httpcore.ErrorBody)
	}

	cases := []testCase{
		{
			name: "an outcome outside the declared set is a bad request",
			err:  fmt.Errorf("workflow-service: complete task: %w", engine.ErrInvalidOutcome),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.Equal(t, "bad_request", body.Error)
				assert.Contains(t, body.Message, "outcome")
			},
		},
		{
			name: "a missing required outcome is a bad request",
			err:  fmt.Errorf("workflow-service: complete task: %w", engine.ErrOutcomeRequired),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.Equal(t, "bad_request", body.Error)
				assert.Contains(t, body.Message, "outcome")
			},
		},
		{
			// ADR-0152: a malformed trigger is a caller-correctable input error,
			// not a server fault.
			name: "empty trigger key is a bad request",
			err:  fmt.Errorf("apply trigger: %w", engine.ErrEmptyTriggerKey),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.Equal(t, "bad_request", body.Error)
				assert.NotEmpty(t, body.Message, "a 4xx body must carry an actionable message")
			},
		},
		{
			// ADR-0180. A PIN, not a change: ClassifyError needed no new arm.
			//
			// ⚠ The status is 422, NOT the 409 ADR-0180 and spec §3b state: in this
			// repo 409 is kernel.ErrConcurrentUpdate's alone, and service.ErrConflict
			// has always mapped to 422 (see the arm above).
			//
			// This row is carried by the service.ErrConflict wrapper, which is exactly
			// what it pins — MEASURED: unwrapping engine.ErrCancelNotApplicable from
			// ErrInvalidTransition leaves this row GREEN. The engine wrapping is
			// pinned by the next row instead.
			name: "a dropped cancel classified by the service layer is a state conflict",
			err: fmt.Errorf("%w: cancel instance %q: %w", service.ErrConflict, "i1",
				fmt.Errorf("workflow-runtime: step: %w", engine.ErrCancelNotApplicable)),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusUnprocessableEntity, status)
				assert.Equal(t, "conflict_state", body.Error)
				assert.Contains(t, body.Message, "cancel is not applicable",
					"the operator must be told WHY, not just refused")
			},
		},
		{
			// The driver is public API in its own right (ProcessDriver.CancelInstance),
			// and its answer carries no service vocabulary at all. Falsifier: drop the
			// ErrInvalidTransition wrapping in engine/errors.go and this row goes to a
			// 500 with an empty body — measured, unlike the row above.
			name: "a dropped cancel straight from the driver is a state conflict",
			err:  fmt.Errorf("workflow-runtime: step: %w", engine.ErrCancelNotApplicable),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusUnprocessableEntity, status)
				assert.Equal(t, "conflict_state", body.Error)
			},
		},
		{
			// ADR-0180's start guard. Same falsifier, and here it is the only carrier:
			// no service-layer arm classifies this sentinel.
			name: "a duplicate start is a state conflict",
			err:  fmt.Errorf("workflow-service: start instance: %w", engine.ErrInstanceAlreadyStarted),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusUnprocessableEntity, status)
				assert.Equal(t, "conflict_state", body.Error)
				assert.Contains(t, body.Message, "already been started")
			},
		},
		{
			name: "wrapped empty trigger key is still a bad request",
			err:  fmt.Errorf("service: %w", fmt.Errorf("engine: %w", engine.ErrEmptyTriggerKey)),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.Equal(t, "bad_request", body.Error)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := httpcore.ClassifyError(tc.err)
			tc.assert(t, status, body)
		})
	}
}

// TestClassifyErrorClaimInvariantSentinels pins the two ADR-0183 sentinels as
// client errors carrying their message. Both arrive WRAPPED in production —
// humantask.Validate wraps ErrInvalidTask itself, and the runtime, the store and
// the caching decorator each add a prefix — so the wrapped rows are the real
// shapes, not extra credit. The control row keeps the table honest: it fails the
// moment ClassifyError starts classifying everything.
func TestClassifyErrorClaimInvariantSentinels(t *testing.T) {
	t.Parallel()

	// A real contradictory task, so the asserted message is the production text
	// rather than one invented here.
	invalidTask := humantask.Validate(humantask.HumanTask{TaskID: "t-1", State: humantask.Claimed})

	type testCase struct {
		name   string
		err    error
		assert func(t *testing.T, status int, body httpcore.ErrorBody)
	}

	cases := []testCase{
		{
			name: "a contradictory task shape is a state conflict",
			err:  invalidTask,
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusUnprocessableEntity, status)
				assert.Equal(t, "conflict_state", body.Error)
				assert.NotEmpty(t, body.Message, "a 4xx body must carry an actionable message")
				assert.Contains(t, body.Message, "t-1",
					"the caller must be told WHICH task contradicts itself")
			},
		},
		{
			name: "a wrapped contradictory task shape is still a state conflict",
			err:  fmt.Errorf("workflow-runtime: commit step: %w", invalidTask),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusUnprocessableEntity, status)
				assert.Equal(t, "conflict_state", body.Error)
				assert.Contains(t, body.Message, "requires a claim",
					"the caller must be told WHAT contradicts")
			},
		},
		{
			name: "an empty reassignment target is a bad request",
			err:  engine.ErrEmptyReassignTarget,
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.Equal(t, "bad_request", body.Error)
				assert.NotEmpty(t, body.Message, "a 4xx body must carry an actionable message")
			},
		},
		{
			name: "a wrapped empty reassignment target is still a bad request",
			err: fmt.Errorf("workflow-service: apply trigger: %w",
				fmt.Errorf("%w: engine.HumanReassigned.To", engine.ErrEmptyReassignTarget)),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.Equal(t, "bad_request", body.Error)
				assert.Contains(t, body.Message, "reassignment target is empty")
			},
		},
		{
			// CONTROL. Neither new arm may widen the default: an error carrying
			// neither sentinel stays a 500 with an empty body.
			name: "an unclassified error still hides behind a 500",
			err:  fmt.Errorf("workflow-runtime: commit step: %w", errors.New("pgx: deadlock detected")),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusInternalServerError, status)
				assert.Equal(t, "internal_error", body.Error)
				assert.Empty(t, body.Message, "a 5xx must not leak the raw error")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := httpcore.ClassifyError(tc.err)
			tc.assert(t, status, body)
		})
	}
}

// TestOversizedBodyClassifiesAs413NotBadRequest pins ADR-0186's 413 arm and,
// crucially, its POSITION in the ordered switch. ClassifyError's switch resolves
// an error matching two arms to whichever arm comes first, so the arm order is
// itself behaviour: the "both sentinels" row below is the only thing that can
// fail if the 413 arm is added AFTER the 400 arm.
//
// Falsifier for the "both sentinels" row: move the 413 arm below the 400 arm in
// errors.go and it answers 400/bad_request. Measured before the arm existed —
// the row's error already classified 400 then, for exactly that reason.
func TestOversizedBodyClassifiesAs413NotBadRequest(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		err    error
		assert func(t *testing.T, status int, body httpcore.ErrorBody)
	}

	cases := []testCase{
		{
			name: "the bare sentinel is a 413 with a static, limit-free body",
			err:  httpcore.ErrRequestBodyTooLarge,
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusRequestEntityTooLarge, status)
				assert.Equal(t, httpcore.ErrorBody{
					Error:   "request_too_large",
					Message: "request body exceeds the configured limit",
				}, body)
			},
		},
		{
			// ⚠ NOT a shape any adapter produces today, and the comment that
			// claimed it was is gone. VERIFIED 2026-08-21 by grep over
			// transport/**/*.go excluding tests: all 15 production sites pass the
			// sentinel BARE — 1 in stdlib/body.go (requestBodyReader), 1 in
			// gin/bodycap.go (capBody), 13 writeErr calls in fiber/groups.go — and
			// zero wrap it. gin/bodycap.go documents bare as the deliberate choice,
			// precisely so classification does not depend on arm ordering.
			//
			// The row is a shape a FUTURE wrapping decode site could produce, and
			// that is exactly why it is worth pinning: it is the only row here that
			// can fail if the 413 arm is moved below the 400 arm, so it holds the
			// arm-ordering invariant STABILITY.md declares against a change no
			// current-behaviour test would notice.
			name: "an error carrying BOTH ErrBadInput and the oversize sentinel is a 413",
			err: fmt.Errorf("%w: decode body: %w", httpcore.ErrBadInput,
				httpcore.ErrRequestBodyTooLarge),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusRequestEntityTooLarge, status,
					"the 413 arm must precede the 400 arm in the ordered switch")
				assert.Equal(t, "request_too_large", body.Error)
			},
		},
		{
			// The 413 body is deliberately static: it must not echo err.Error(),
			// and it must not name the configured limit — that is deployment
			// configuration, and telling an attacker the ceiling tells them what
			// to stay under. Every OTHER 4xx arm renders err.Error(); this one
			// must not inherit that by accident.
			name: "a wrapped oversize error leaks neither the wrapper text nor the limit",
			err: fmt.Errorf("%w: POST /instances body capped at 1048576 bytes: %w",
				httpcore.ErrBadInput, httpcore.ErrRequestBodyTooLarge),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusRequestEntityTooLarge, status)
				assert.Equal(t, "request body exceeds the configured limit", body.Message)
				assert.NotContains(t, body.Message, "1048576",
					"the response must not disclose the configured limit")
				assert.NotContains(t, body.Message, "POST /instances")
			},
		},
		{
			// The near-miss neighbour. action/httpcall.ErrBodyTooLarge is a
			// DIFFERENT sentinel meaning an OUTBOUND response exceeded httpcall's
			// 10 MiB cap — a server-side fault the caller cannot fix — so it must
			// stay an opaque 500. Naming the new sentinel ErrBodyTooLarge would
			// have made these two collide.
			name: "the outbound-response sentinel from action/httpcall is still a 500",
			err:  fmt.Errorf("workflow-action: call service: %w", httpcall.ErrBodyTooLarge),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusInternalServerError, status)
				assert.Equal(t, "internal_error", body.Error)
				assert.Empty(t, body.Message)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := httpcore.ClassifyError(tc.err)
			tc.assert(t, status, body)
		})
	}
}

package scheduler_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/scheduler"
)

// Liveness canaries for the assert.Never / require.Never sites in this package
// (backlog 44). See scheduler/internal/gocron/liveness_test.go for the full
// rationale and the measured vacuity table; in short, a Never window asserts an
// absence, and an absence is only evidence about the SUBJECT once something
// else has demonstrably happened.
//
// Measured here, not reasoned: with the internal scheduler's NewJob rewired to
// register a task that ignores the caller's callback — everything still
// registers, arms and fires, so every BlockUntilContext barrier still passes —
// these subtests PASSED on code where nothing the caller asked for ever ran:
//
//	TestNativeSchedulerSchedule/manual_job_persists_but_leaves_NO_scheduler_record  PASS
//	TestNativeSchedulerDeactivateCancel/Deactivate_disarms_without_Delete           PASS (0.15s)
//	TestNativeSchedulerDeactivateCancel/Cancel_deletes_from_the_store_and_disarms   PASS (0.15s)
//	TestNewScheduler_WithClock_NotFiredWithoutAdvance                               PASS (0.20s)
//
// The fix is a POSITIVE precondition, never a bigger budget: a Never window is
// paid in full on every GREEN run, so raising it is pure cost (ADR-0184 §4).
//
// ⚠ clockwork's BlockUntilContext(ctx, n) returns as soon as len(waiters) >= n
// (clockwork@v0.5.0 clockwork.go:255-258) — a LOWER bound. Arming a canary
// alongside the subject therefore means raising the companion barrier to the
// true armed-job count, or it is satisfied by whichever job armed first.

// scheduleFireCanary schedules an ordinary AUTO job on s (armed at once,
// through the same façade, the same store wiring and the same fake clock as
// the subject) and returns a reader for its fire count. Its firing proves the
// chain the subject depends on — Schedule, arm, Advance delivery, executor
// goroutine, action invocation — was alive across the Never window.
//
// It does NOT wait for the arm: the caller owns the barrier, because the
// waiter count to block on depends on how many other jobs it armed.
func scheduleFireCanary(t *testing.T, s scheduler.Scheduler, id string, trig scheduler.Trigger) func() int32 {
	t.Helper()
	var fires atomic.Int32
	_, err := s.Schedule(t.Context(), mustJob(t, id, surfaceKind, trig, func() { fires.Add(1) }))
	require.NoError(t, err, "the liveness canary itself must schedule and arm")
	return fires.Load
}

// requireCanaryFired blocks until the canary has fired at least want times,
// failing (by name, inside eventuallyBudget) if it never does. Call it
// immediately before the Never it licenses.
func requireCanaryFired(t *testing.T, fires func() int32, want int32) {
	t.Helper()
	require.Eventually(t, func() bool { return fires() >= want }, eventuallyBudget, 5*time.Millisecond,
		"liveness canary must have fired at least %d time(s); until it does, a Never window proves nothing about the subject", want)
}

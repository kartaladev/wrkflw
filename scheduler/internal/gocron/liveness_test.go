package gocron_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	sched "github.com/kartaladev/wrkflw/scheduler/internal/gocron"
)

// Liveness canaries for require.Never / assert.Never sites (backlog 44).
//
// A Never window asserts an absence, and an absence is only evidence about the
// SUBJECT once something else has demonstrably happened. Measured, not
// reasoned: with s.sched.NewJob rewired to register a task that ignores the
// caller's callback — the job still registers, still arms a fake-clock waiter,
// still fires, so every BlockUntilContext barrier still passes — these cases
// PASSED on code where nothing the caller asked for ever ran:
//
//	TestGocronScheduler_Behaviour/cancel_prevents_fire           PASS (0.20s)
//	TestGocronScheduler_WithClock_NotFiredWithoutAdvance         PASS (0.20s)
//	TestNativeSchedulerSchedule/manual_job_persists…             PASS
//	TestNativeSchedulerDeactivateCancel/Deactivate_disarms…      PASS (0.15s)
//	TestNativeSchedulerDeactivateCancel/Cancel_deletes…          PASS (0.15s)
//
// The fix is a POSITIVE precondition, never a bigger budget: a Never window is
// paid in full on every GREEN run, so raising it is pure cost (ADR-0184 §4).
//
// ⚠ clockwork's BlockUntilContext(ctx, n) returns as soon as len(waiters) >= n
// (clockwork@v0.5.0 clockwork.go:255-258, "Fast path: we already have >= n
// waiters") — it is a LOWER bound. Adding a canary therefore means raising
// every companion barrier to the true armed-job count; a stale
// BlockUntilContext(…, 1) would be satisfied by the canary alone and let the
// subject race the Advance.

// newFireCanary arms a sibling job on s under id and returns a reader for its
// fire count. The canary is an ordinary job on the same scheduler, driven by
// the same fake clock, so its firing proves the whole chain the subject
// depends on — registration, arm, Advance delivery, executor goroutine, task
// invocation — was alive across the Never window.
//
// It does NOT wait for the arm: the caller owns the barrier, because the
// waiter count to block on depends on how many other jobs it armed.
func newFireCanary(t *testing.T, s *sched.GocronScheduler, id string, trig sched.TriggerDef) func() int32 {
	t.Helper()
	var fires atomic.Int32
	_, err := s.ScheduleJob(t.Context(), id, trig, func(context.Context) error { fires.Add(1); return nil }, false)
	require.NoError(t, err, "the liveness canary itself must arm")
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

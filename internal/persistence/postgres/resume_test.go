package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/database"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// resumeFixedJitter is a deterministic JitterSource that always returns the
// same fraction. Defined locally in this package (postgres_test) so that the
// retry fire-at time is fully deterministic without depending on runtime_test
// internals (fixedJitter lives in package runtime_test and is unexported here).
type resumeFixedJitter struct{ f float64 }

func (j resumeFixedJitter) Fraction() float64 { return j.f }

// timerResumeDef returns: start → wait(PT1H intermediate-catch timer) → finish(service) → end.
func timerResumeDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "pg-timer-resume",
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "wait1h", Kind: model.KindIntermediateCatchEvent, TimerDuration: `"1h"`},
			{ID: "finish", Kind: model.KindServiceTask, Action: "finish"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait1h"},
			{ID: "f2", Source: "wait1h", Target: "finish"},
			{ID: "f3", Source: "finish", Target: "end"},
		},
	}
}

// boundaryResumeDef returns: start → wait-task(UserTask) → end-normal
// with an interrupting boundary timer ("PT2H") on wait-task that routes to
// finish(service) → end-escalated.
//
// The boundary timer is an interrupting timer boundary event: when it fires
// it cancels the host user-task token and routes to the "finish" service task,
// which then completes the instance via end-escalated. This is used to prove
// that the Boundaries slice survives a Postgres reload (the round-trip assertion
// in TestPostgresParkedBoundaryResumesAfterReload).
func boundaryResumeDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "pg-boundary-resume",
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "wait-task", Kind: model.KindUserTask, CandidateRoles: []string{"reviewer"}},
			{ID: "bnd-timer", Kind: model.KindBoundaryEvent, AttachedTo: "wait-task",
				TimerDuration: `"2h"`, NonInterrupting: false},
			{ID: "finish", Kind: model.KindServiceTask, Action: "finish"},
			{ID: "end-normal", Kind: model.KindEndEvent},
			{ID: "end-escalated", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait-task"},
			{ID: "f2", Source: "wait-task", Target: "end-normal"},
			{ID: "f3", Source: "bnd-timer", Target: "finish"},
			{ID: "f4", Source: "finish", Target: "end-escalated"},
		},
	}
}

// TestPostgresParkedTimerResumesAfterReload proves that a timer-intermediate-parked
// instance's snapshot survives a real Postgres reload through a brand-new Store
// (simulating a process restart) and resumes to completion when the timer fires.
//
// This validates the JSON round-trip of the parked token's AwaitCommand field via
// the JSONB snapshot column: intermediate-catch-event timers park the engine token
// with AwaitCommand set to the timer ID (they do NOT use InstanceState.Timers). If
// the reloaded token's AwaitCommand is empty the test fails immediately, surfacing a
// real persistence bug rather than a test weakness.
func TestPostgresParkedTimerResumesAfterReload(t *testing.T) {
	t.Parallel()

	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	startAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	ran := false
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"finish": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			ran = true
			return map[string]any{"done": true}, nil
		}),
	})

	def := timerResumeDef()
	const id = "pg-resume-timer-1"

	// Runner #1 over the Postgres store: start → park at the intermediate timer.
	store1 := pg.NewStore(pool)
	sched1 := runtime.NewMemScheduler(fc)
	r1 := runtime.NewRunner(cat, fc, store1, runtime.WithScheduler(sched1))

	parked, err := r1.Run(t.Context(), def, id, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "instance must be running (parked at timer)")
	require.False(t, ran, "service must not have run while timer is pending")
	// For intermediate-catch-event timers the engine does NOT add a record to
	// s.Timers. Instead the token parks with AwaitCommand == timerID (see step.go
	// KindIntermediateCatchEvent handler). The timer ID lives on the token.
	require.Len(t, parked.Tokens, 1, "exactly one token must be parked at the timer node")
	require.Equal(t, "wait1h", parked.Tokens[0].NodeID, "parked token must be at the timer node")
	parkedTimerID := parked.Tokens[0].AwaitCommand
	require.NotEmpty(t, parkedTimerID, "parked token's AwaitCommand must be the timer ID")

	// Simulate a process restart: build a brand-new Store over the same pool.
	// Only the Postgres rows survive — the in-memory scheduler (sched1) is discarded.
	store2 := pg.NewStore(pool)
	reloaded, _, err := store2.Load(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, reloaded.Status, "reloaded status must still be running")
	require.Len(t, reloaded.Tokens, 1,
		"the parked token must survive the JSON round-trip through Postgres")
	require.Equal(t, "wait1h", reloaded.Tokens[0].NodeID,
		"reloaded token must be at the timer node")

	// Assert the timer ID (AwaitCommand) round-trips correctly.
	reloadedTimerID := reloaded.Tokens[0].AwaitCommand
	require.NotEmpty(t, reloadedTimerID,
		"reloaded token's AwaitCommand (timer ID) must survive the round-trip")
	require.Equal(t, parkedTimerID, reloadedTimerID,
		"timer ID must be identical before and after Postgres reload")

	// Advance the clock past the 1-hour timer and deliver TimerFired via runner #2
	// over store2. The original sched1 was in-memory and died with runner #1; we
	// use the timer ID from the reloaded token to fire manually.
	fc.Advance(1*time.Hour + time.Second)

	sched2 := runtime.NewMemScheduler(fc)
	r2 := runtime.NewRunner(cat, fc, store2, runtime.WithScheduler(sched2))

	final, err := r2.Deliver(t.Context(), def, id, engine.NewTimerFired(fc.Now(), reloadedTimerID))
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, final.Status,
		"instance must reach StatusCompleted after the timer fires and the service task runs")
	require.True(t, ran, "service action 'finish' must have run after timer fired")
	require.Empty(t, final.Tokens, "no tokens must remain after completion")
}

// TestPostgresParkedBoundaryResumesAfterReload proves that a boundary-timer-parked
// instance's snapshot survives a real Postgres reload through a brand-new Store and
// resumes to completion when the boundary timer fires.
//
// This validates the JSON round-trip of InstanceState.Boundaries via the JSONB
// snapshot column: if reloaded.Boundaries is empty the test fails immediately,
// surfacing a real persistence bug.
func TestPostgresParkedBoundaryResumesAfterReload(t *testing.T) {
	t.Parallel()

	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	startAt := time.Date(2026, 2, 1, 10, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	ran := false
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"finish": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			ran = true
			return map[string]any{"escalated": true}, nil
		}),
	})

	def := boundaryResumeDef()
	const id = "pg-resume-boundary-1"

	// Minimal human-task wiring so the runner can park at the user-task node and
	// arm the boundary timer. We never complete the task — the boundary timer fires
	// to take the escalation path instead.
	reviewer := authz.Actor{ID: "alice", Roles: []string{"reviewer"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"reviewer": {reviewer},
	})
	az := authz.RoleAuthorizer{}

	// Runner #1 over the Postgres store: start → park at user-task with boundary timer armed.
	store1 := pg.NewStore(pool)
	sched1 := runtime.NewMemScheduler(fc)
	r1 := runtime.NewRunner(cat, fc, store1,
		runtime.WithScheduler(sched1),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	parked, err := r1.Run(t.Context(), def, id, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "instance must be running (parked at user-task)")
	require.False(t, ran, "service must not have run while task is pending")
	require.NotEmpty(t, parked.Boundaries,
		"parked state must have at least one boundary arm recorded (the 2h timer boundary)")

	// Simulate a process restart: build a brand-new Store over the same pool.
	store2 := pg.NewStore(pool)
	reloaded, _, err := store2.Load(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, reloaded.Status, "reloaded status must still be running")
	require.Len(t, reloaded.Boundaries, 1,
		"the boundary arm must survive the JSON round-trip through Postgres (len parked=%d, len reloaded=%d)",
		len(parked.Boundaries), len(reloaded.Boundaries))

	// Assert the boundary timer ID round-trips correctly.
	require.Equal(t, parked.Boundaries[0].TimerID, reloaded.Boundaries[0].TimerID,
		"boundary timer ID must survive the round-trip")
	require.Equal(t, "bnd-timer", reloaded.Boundaries[0].BoundaryNode,
		"boundary node ID must survive the round-trip")

	// Advance the clock past the 2-hour boundary timer and deliver TimerFired via
	// runner #2 over store2. The interrupting boundary timer cancels the host task
	// token and routes to the "finish" service task.
	fc.Advance(2*time.Hour + time.Second)
	boundaryTimerID := reloaded.Boundaries[0].TimerID

	sched2 := runtime.NewMemScheduler(fc)
	r2 := runtime.NewRunner(cat, fc, store2,
		runtime.WithScheduler(sched2),
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	final, err := r2.Deliver(t.Context(), def, id, engine.NewTimerFired(fc.Now(), boundaryTimerID))
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, final.Status,
		"instance must reach StatusCompleted after the boundary timer fires and the service task runs")
	require.True(t, ran, "service action 'finish' must have run after boundary timer fired")
	require.Empty(t, final.Tokens, "no tokens must remain after completion")
	require.Empty(t, final.Boundaries, "boundary arms must be cleared after the boundary fires")
}

// retryResumeDef returns a minimal process: start → task(action "a", RetryPolicy) → end.
// The RetryPolicy uses a 1-hour InitialInterval so the retry timer is clearly in the
// future (the test clock starts before the fire-at). MaxAttempts=3 gives two retries.
func retryResumeDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "pg-retry-resume",
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{
				ID:     "task",
				Kind:   model.KindServiceTask,
				Action: "a",
				RetryPolicy: &model.RetryPolicy{
					MaxAttempts:     3,
					InitialInterval: time.Hour,
					BackoffCoef:     2,
					MaxInterval:     24 * time.Hour,
				},
			},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// TestPostgresParkedRetryResumesAfterReload proves that a retry-parked instance
// survives a process restart (brand-new Store over the same pool) and resumes to
// completion when the retry timer fires.
//
// Scenario:
//  1. r1.Run → action "a" fails on attempt 1 → engine schedules a TimerRetry at T+1h
//     and parks the token with AwaitCommand = timerID, RetryAttempts = 1.
//  2. Reload via brand-new store2 — asserts that Token.AwaitCommand, Token.RetryAttempts,
//     and the TimerRetry record in InstanceState.Timers all survive the JSONB round-trip.
//  3. fc.Advance(1h+1s) → r2.Deliver TimerFired → action "a" succeeds on attempt 2
//     → StatusCompleted with the action invoked exactly twice.
//
// A JSONB-codec gap (e.g. RetryAttempts dropped from the snapshot) fails the
// reload assertions and surfaces the bug immediately rather than causing a silent
// wrong-count mismatch.
func TestPostgresParkedRetryResumesAfterReload(t *testing.T) {
	t.Parallel()

	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))

	startAt := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	// attempts counts how many times action "a" has been invoked across both runners.
	// The closure is captured by reference so both r1 and r2 share the counter via cat.
	attempts := 0
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"a": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			attempts++
			if attempts == 1 {
				return nil, errors.New("transient")
			}
			return map[string]any{"done": true}, nil
		}),
	})

	def := retryResumeDef()
	const id = "pg-retry-resume-1"

	// fixedJitter{1.0}: Fraction() always returns 1.0.
	// Attempt 0 backoff: InitialInterval × BackoffCoef^0 = 1h × 1 = 1h.
	// FireAt = startAt + 1.0×1h = startAt + 1h. Deterministic and unambiguous.
	jitter := resumeFixedJitter{f: 1.0}

	// ── Runner #1: drive the instance until it parks on the retry timer ──

	store1 := pg.NewStore(pool)
	sched1 := runtime.NewMemScheduler(fc)
	r1 := runtime.NewRunner(cat, fc, store1,
		runtime.WithScheduler(sched1),
		runtime.WithJitterSource(jitter),
	)

	parked, err := r1.Run(t.Context(), def, id, nil)
	require.NoError(t, err, "Run must not return a hard error on the first attempt failure")

	// Instance must be running (parked), not failed — retry policy intercepts.
	require.Equal(t, engine.StatusRunning, parked.Status,
		"instance must be running (parked at retry timer) after first attempt fails")
	require.Equal(t, 1, attempts, "action 'a' must have been called exactly once by r1.Run")

	// The parked token's AwaitCommand is the retry timer ID; RetryAttempts == 1.
	require.Len(t, parked.Tokens, 1, "exactly one token must be parked")
	require.Equal(t, "task", parked.Tokens[0].NodeID, "parked token must be at the 'task' node")
	parkedTimerID := parked.Tokens[0].AwaitCommand
	require.NotEmpty(t, parkedTimerID,
		"parked token's AwaitCommand must be the retry timer ID")
	require.Equal(t, 1, parked.Tokens[0].RetryAttempts,
		"parked token must record RetryAttempts == 1 after the first failure")

	// The Timers slice must contain exactly one TimerRetry record.
	require.Len(t, parked.Timers, 1, "exactly one timer record must be in InstanceState.Timers")
	require.Equal(t, engine.TimerRetry, parked.Timers[0].Kind,
		"the timer record must have Kind == TimerRetry")
	require.Equal(t, parkedTimerID, parked.Timers[0].TimerID,
		"the timer record's TimerID must match the token's AwaitCommand")

	// ── Simulate process restart: brand-new Store over the same pool ──
	// Only Postgres rows survive; in-memory sched1 is discarded.

	store2 := pg.NewStore(pool)
	reloaded, _, err := store2.Load(t.Context(), id)
	require.NoError(t, err, "store2.Load must succeed after Postgres persist")

	// Core round-trip assertions: these catch snapshot/codec gaps.
	require.Equal(t, engine.StatusRunning, reloaded.Status,
		"reloaded status must still be running")
	require.Len(t, reloaded.Tokens, 1,
		"the parked token must survive the JSONB round-trip")
	require.Equal(t, "task", reloaded.Tokens[0].NodeID,
		"reloaded token must still be at the 'task' node")
	reloadedTimerID := reloaded.Tokens[0].AwaitCommand
	require.NotEmpty(t, reloadedTimerID,
		"reloaded token's AwaitCommand (retry timer ID) must survive the round-trip")
	require.Equal(t, parkedTimerID, reloadedTimerID,
		"retry timer ID must be identical before and after Postgres reload")
	require.Equal(t, 1, reloaded.Tokens[0].RetryAttempts,
		"Token.RetryAttempts must survive the JSONB round-trip (codec gap check)")

	// The TimerRetry record must also survive.
	require.Len(t, reloaded.Timers, 1,
		"the TimerRetry record must survive the JSONB round-trip (codec gap check)")
	require.Equal(t, engine.TimerRetry, reloaded.Timers[0].Kind,
		"reloaded timer record must have Kind == TimerRetry")
	require.Equal(t, reloadedTimerID, reloaded.Timers[0].TimerID,
		"reloaded timer record's TimerID must match the reloaded token's AwaitCommand")

	// ── Advance clock past retry fire-at; deliver TimerFired via runner #2 ──

	fc.Advance(time.Hour + time.Second) // clock is now startAt+1h+1s → past fire-at

	sched2 := runtime.NewMemScheduler(fc)
	r2 := runtime.NewRunner(cat, fc, store2,
		runtime.WithScheduler(sched2),
		runtime.WithJitterSource(jitter),
	)

	final, err := r2.Deliver(t.Context(), def, id, engine.NewTimerFired(fc.Now(), reloadedTimerID))
	require.NoError(t, err, "r2.Deliver must not return a hard error")

	require.Equal(t, engine.StatusCompleted, final.Status,
		"instance must reach StatusCompleted after the retry timer fires and the action succeeds")
	require.Equal(t, 2, attempts,
		"action 'a' must have been invoked exactly twice total (once failing, once succeeding)")
	require.Empty(t, final.Tokens,
		"no tokens must remain after completion")
	require.Empty(t, final.Timers,
		"no timer records must remain after successful completion")
}

package runtime_test

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// recordingHandler is a minimal slog.Handler that captures every emitted record
// so a test can assert on the level and message of what was logged.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, rec slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, rec.Clone())
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// errorMessages returns the messages of every captured record at LevelError.
func (h *recordingHandler) errorMessages() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var msgs []string
	for _, rec := range h.records {
		if rec.Level >= slog.LevelError {
			msgs = append(msgs, rec.Message)
		}
	}
	return msgs
}

// TestRunnerDeadlineBreachActionDoesNotLogDeliverError is the regression test for
// the fire-and-forget fix. Before the fix, the deadline-breach InvokeAction was
// fed back into the engine, where no token awaited its CommandID, producing
// ErrTokenNotFound ("no token awaiting command") that the timer-fire path logged
// as a LevelError "Deliver failed". The instance still completed, so asserting on
// sched.Tick's return is insufficient (Tick swallows the error) — we must assert
// on the captured log records.
func TestRunnerDeadlineBreachActionDoesNotLogDeliverError(t *testing.T) {
	ctx := t.Context()

	startAt := time.Date(2026, 6, 26, 9, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	escalationRan := false
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"notify": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			escalationRan = true
			return map[string]any{"escalated": true}, nil
		}),
	})

	def := &model.ProcessDefinition{
		ID:      "deadline-fireforget",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewUserTask("review", []string{"reviewer"}, model.WithDeadline(`"30m"`, "escalate", "notify")),
			model.NewEndEvent("end-normal"),
			model.NewEndEvent("end-escalated"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "review"},
			{ID: "f2", Source: "review", Target: "end-normal"},
			{ID: "escalate", Source: "review", Target: "end-escalated"},
		},
	}

	reviewer := authz.Actor{ID: "alice", Roles: []string{"reviewer"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"reviewer": {reviewer},
	})
	az := authz.RoleAuthorizer{}
	sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(fc))
	store := runtimetest.MustMemStore(t)

	rec := &recordingHandler{}
	logger := slog.New(rec)

	r := runtimetest.MustRunner(t, cat, store,
		runtime.WithClock(fc),
		runtime.WithHumanTasks(resolver, taskStore, az),
		runtime.WithScheduler(sched),
		runtime.WithLogger(logger),
	)

	const instanceID = "deadline-ff-1"

	parked, err := r.Run(ctx, def, instanceID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, parked.Status)

	// Do NOT complete the task. Advance the clock past the 30-minute deadline.
	fc.Advance(31 * time.Minute)
	require.NoError(t, sched.Tick(ctx))

	// The instance completed via the escalation path and the breach action ran.
	final, _, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status, "instance must complete via the escalation path")
	assert.True(t, escalationRan, "deadline breach action must have run")

	// REGRESSION ASSERTION: no spurious error must be logged for the fire-once
	// breach action. Before the fix, the fed-back ActionCompleted produced
	// "Deliver failed" / "no token awaiting command" at LevelError.
	for _, msg := range rec.errorMessages() {
		if strings.Contains(msg, "Deliver failed") || strings.Contains(msg, "no token awaiting command") {
			t.Errorf("unexpected LevelError log for fire-once breach action: %q", msg)
		}
	}
}

package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// errStateStore is a StateStore whose Save always fails.
type errStateStore struct{ runtime.StateStore }

func (e *errStateStore) Save(_ engine.InstanceState) error { return errors.New("store: forced failure") }
func (e *errStateStore) Load(id string) (engine.InstanceState, error) {
	return engine.InstanceState{}, runtime.ErrInstanceNotFound
}

// errJournal is a Journal whose Append always fails.
type errJournal struct{}

func (j *errJournal) Append(_ string, _ engine.Trigger) error {
	return errors.New("journal: forced failure")
}

// errOutbox is an OutboxWriter whose Write always fails.
type errOutbox struct{}

func (o *errOutbox) Write(_ string, _ map[string]any) error {
	return errors.New("outbox: forced failure")
}

func TestMemOutboxEvents(t *testing.T) {
	out := runtime.NewMemOutbox()
	require.Empty(t, out.Events())

	require.NoError(t, out.Write("instance.completed", map[string]any{"result": "ok"}))
	require.NoError(t, out.Write("instance.failed", map[string]any{"error": "boom"}))

	evs := out.Events()
	require.Len(t, evs, 2)
	assert.Equal(t, "instance.completed", evs[0].Topic)
	assert.Equal(t, "instance.failed", evs[1].Topic)
}

func TestRunnerUnknownActionFailsInstance(t *testing.T) {
	// A catalog with no actions; the runner should receive ActionFailed and
	// record a FailInstance command (outbox write "instance.failed").
	cat := action.NewMapCatalog(nil)
	out := runtime.NewMemOutbox()
	r := runtime.NewRunner(cat, clock.System(), runtime.NewMemStateStore(), runtime.NewMemJournal(), out, nil, nil, nil)

	final, err := r.Run(t.Context(), linearDef(), "i1", nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusFailed, final.Status)

	evs := out.Events()
	require.Len(t, evs, 1)
	assert.Equal(t, "instance.failed", evs[0].Topic)
}

func TestRunnerActionErrorFailsInstance(t *testing.T) {
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return nil, fmt.Errorf("greet exploded")
		}),
	})
	out := runtime.NewMemOutbox()
	r := runtime.NewRunner(cat, clock.System(), runtime.NewMemStateStore(), runtime.NewMemJournal(), out, nil, nil, nil)

	final, err := r.Run(t.Context(), linearDef(), "i1", nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusFailed, final.Status)

	evs := out.Events()
	require.Len(t, evs, 1)
	assert.Equal(t, "instance.failed", evs[0].Topic)
}

func TestRunnerJournalAppendErrorPropagates(t *testing.T) {
	cat := action.NewMapCatalog(nil)
	r := runtime.NewRunner(cat, clock.System(), runtime.NewMemStateStore(), &errJournal{}, runtime.NewMemOutbox(), nil, nil, nil)

	_, err := r.Run(t.Context(), linearDef(), "i1", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtime: journal:")
}

func TestRunnerStoreSaveErrorPropagates(t *testing.T) {
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return nil, nil
		}),
	})
	r := runtime.NewRunner(cat, clock.System(), &errStateStore{}, runtime.NewMemJournal(), runtime.NewMemOutbox(), nil, nil, nil)

	_, err := r.Run(t.Context(), linearDef(), "i1", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtime: save:")
}

func TestRunnerOutboxWriteErrorPropagates(t *testing.T) {
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return nil, nil
		}),
	})
	r := runtime.NewRunner(cat, clock.System(), runtime.NewMemStateStore(), runtime.NewMemJournal(), &errOutbox{}, nil, nil, nil)

	_, err := r.Run(t.Context(), linearDef(), "i1", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtime: outbox:")
}

// userTaskOnlyDef returns a process with a single user-task node: start → userTask → end.
func userTaskOnlyDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "user-task-only",
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "task1", Kind: model.KindUserTask, CandidateRoles: []string{"manager"}},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task1"},
			{ID: "f2", Source: "task1", Target: "end"},
		},
	}
}

// TestRunnerUserTaskWithoutDepsErrors verifies that a Runner constructed without
// human-task dependencies (nil resolver and nil TaskStore) returns a descriptive
// error — rather than panicking — when it reaches an AwaitHuman command.
func TestRunnerUserTaskWithoutDepsErrors(t *testing.T) {
	// Build a Runner with nil resolver and nil tasks (no human-task deps).
	r := runtime.NewRunner(
		nil, // no catalog
		clock.System(),
		runtime.NewMemStateStore(),
		runtime.NewMemJournal(),
		runtime.NewMemOutbox(),
		nil, // nil ActorResolver
		nil, // nil TaskStore
		nil,
	)

	_, err := r.Run(t.Context(), userTaskOnlyDef(), "i1", nil)
	require.Error(t, err, "Run must fail with a descriptive error, not panic")
	assert.Contains(t, err.Error(), "ActorResolver", "error must mention the missing ActorResolver")
}

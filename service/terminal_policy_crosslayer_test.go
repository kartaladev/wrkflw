package service_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/task"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// ── ADR-0165 cross-layer pins: sentinel identity from engine to HTTP status ───
//
// The two tests below drive DIFFERENT service methods (ResolveIncident and
// RefreshTaskCandidates) over structurally different fixtures — one needs a
// failing action to mint an incident, the other a parked user task and a live
// actor resolver — so they stay separate functions rather than one table. Each
// is itself a table over the state that flips the classification.
//
// What they share is the property under test: NEITHER route re-classifies the
// engine's refusal in service/. ProcessEngine.ResolveIncident wraps with %w and
// returns; the ErrConflict re-wrap at service.go's deliverTaskTrigger is on the
// human-task route only. The 422 therefore rests entirely on
// httpcore.ClassifyError's engine.ErrInvalidTransition arm plus an unbroken %w
// chain through runtime/ and service/. A single %v anywhere on that path
// downgrades the response to a 500 with an EMPTY body — which is why these pins
// assert the sentinel identity, not merely the status code.

// incidentFixture is an instance of incidentDef parked Running with exactly one
// open incident, plus the ProcessEngine facade over it.
type incidentFixture struct {
	svc        *service.ProcessEngine
	instanceID string
	incidentID string
	def        *model.ProcessDefinition
	driver     *runtime.ProcessDriver
}

// newIncidentFixture drives incidentDef until its single action exhausts its one
// allowed attempt, leaving the instance Running with an open incident.
func newIncidentFixture(t *testing.T, ctx context.Context, instanceID string) incidentFixture {
	t.Helper()

	clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	alwaysFails := action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
		return nil, errors.New("action always fails")
	})

	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	def := incidentDef()
	reg := kernel.NewMapDefinitionRegistry(def)

	driver, err := runtime.NewProcessDriver(
		runtime.WithActionCatalog(action.NewCatalog(map[string]action.Action{"failing": alwaysFails})),
		runtime.WithInstanceStore(store),
		runtime.WithDefinitions(reg),
		runtime.WithClock(clk),
		runtime.WithHumanTasks(humantask.NewStaticActorResolver(nil), humantask.NewMemTaskStore(), authz.RoleAuthorizer{}),
		runtime.WithDefaultRetryPolicy(model.RetryPolicy{
			MaxAttempts:     1,
			InitialInterval: time.Second,
			BackoffCoef:     1,
			MaxInterval:     time.Minute,
		}),
	)
	require.NoError(t, err)

	svc, err := service.NewProcessEngine(
		service.WithProcessDriver(driver),
		service.WithInstanceStore(store),
		service.WithDefinitions(reg),
		service.WithLister(store),
		service.WithHumanTasks(humantask.NewMemTaskStore(), authz.RoleAuthorizer{}),
		service.WithClock(clk),
	)
	require.NoError(t, err)

	parked, err := driver.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "precondition: instance must park Running")
	require.Len(t, parked.Incidents, 1, "precondition: exactly one open incident")

	return incidentFixture{
		svc:        svc,
		instanceID: instanceID,
		incidentID: parked.Incidents[0].ID,
		def:        def,
		driver:     driver,
	}
}

// TestResolveIncidentOnTerminalInstanceMapsTo422 pins the end-to-end sentinel
// identity for ResolveIncident (ADR-0165). ResolveIncident is classified
// rejectWithError: an admin clearing an incident is a synchronous external
// caller who must be told the operation was refused, not silently ignored.
//
// The "still running" case is the discriminator: it proves the 422 is caused by
// the terminal status and not by an always-failing route.
func TestResolveIncidentOnTerminalInstanceMapsTo422(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// arrange runs before the SUT call; it may cancel the instance.
		arrange func(t *testing.T, ctx context.Context, f incidentFixture)
		assert  func(t *testing.T, status int, body httpcore.ErrorBody, err error)
	}

	cases := []testCase{
		{
			name: "terminal instance is refused with ErrInstanceTerminal and classified 422",
			arrange: func(t *testing.T, ctx context.Context, f incidentFixture) {
				cancelled, err := f.driver.CancelInstance(ctx, f.def, f.instanceID)
				require.NoError(t, err)
				require.True(t, cancelled.Status.IsTerminal(),
					"precondition: the instance must be terminal, got %v", cancelled.Status)
			},
			assert: func(t *testing.T, status int, body httpcore.ErrorBody, err error) {
				require.Error(t, err)
				// The sentinel chain, asserted link by link: a %v anywhere on the
				// runtime→service path breaks exactly these three.
				assert.ErrorIs(t, err, engine.ErrInstanceTerminal,
					"the specific ADR-0165 sentinel must survive every wrap")
				assert.ErrorIs(t, err, engine.ErrInvalidTransition,
					"the classification parent must survive — this is the arm httpcore matches on")
				assert.False(t, errors.Is(err, service.ErrConflict),
					"service/ does NOT re-classify this route; the 422 comes from the engine sentinel alone")

				assert.Equal(t, http.StatusUnprocessableEntity, status)
				assert.Equal(t, "conflict_state", body.Error)
				assert.NotEmpty(t, body.Message,
					"a 500 fallback would have an EMPTY message — that is the regression this pins")
			},
		},
		{
			name: "running instance still resolves its incident",
			assert: func(t *testing.T, status int, body httpcore.ErrorBody, err error) {
				require.NoError(t, err)
				assert.Zero(t, status, "no error to classify")
				assert.Empty(t, body.Error)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			f := newIncidentFixture(t, ctx, "resolve-terminal-inst")
			if tc.arrange != nil {
				tc.arrange(t, ctx, f)
			}

			// Go through the httpcore seam the adapters call, not the service
			// method directly, so the whole transport path is covered.
			_, _, err := httpcore.ResolveIncident(ctx, f.svc, f.instanceID, f.incidentID,
				httpcore.ResolveIncidentInput{AddAttempts: 1})

			var (
				status int
				body   httpcore.ErrorBody
			)
			if err != nil {
				status, body = httpcore.ClassifyError(err)
			}
			tc.assert(t, status, body, err)
		})
	}
}

// TestRefreshTaskCandidatesOnClosedTaskMapsTo422 pins the bug ADR-0165 fixed by
// aliasing runtime/task.ErrTaskNotOpen to engine.ErrTaskNotOpen.
//
// Before this delivery task.ErrTaskNotOpen was a standalone errors.New that
// wrapped nothing: it matched no arm of httpcore.ClassifyError and fell through
// to the 500 default, whose ErrorBody carries an EMPTY message. A caller
// refreshing a task someone else had just completed got an opaque server error
// for a perfectly ordinary race. It is now a sibling of ErrInstanceTerminal
// under ErrInvalidTransition and classifies 422.
//
// NOTE: no HTTP route in transport/ calls RefreshTaskCandidates today, so this
// pins the classification a consumer-mounted route would get, via the same
// ClassifyError seam every adapter uses.
func TestRefreshTaskCandidatesOnClosedTaskMapsTo422(t *testing.T) {
	t.Parallel()

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}

	type testCase struct {
		name    string
		arrange func(t *testing.T, ctx context.Context, f refreshFixture)
		assert  func(t *testing.T, status int, body httpcore.ErrorBody, err error)
	}

	cases := []testCase{
		{
			name: "closed task is refused with ErrTaskNotOpen and classified 422",
			arrange: func(t *testing.T, ctx context.Context, f refreshFixture) {
				_, err := f.svc.CompleteTask(ctx, service.CompleteTaskRequest{TaskID: f.taskID, Actor: manager})
				require.NoError(t, err)
			},
			assert: func(t *testing.T, status int, body httpcore.ErrorBody, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, task.ErrTaskNotOpen,
					"the runtime/task sentinel consumers already match on must keep matching")
				assert.ErrorIs(t, err, engine.ErrTaskNotOpen,
					"runtime/task.ErrTaskNotOpen is an ALIAS of the engine sentinel, not a distinct value")
				assert.ErrorIs(t, err, engine.ErrInvalidTransition,
					"the alias is what carries the classification parent that produces the 422")

				assert.Equal(t, http.StatusUnprocessableEntity, status,
					"before ADR-0165 this fell through to a 500")
				assert.Equal(t, "conflict_state", body.Error)
				assert.NotEmpty(t, body.Message,
					"the 500 fallback body has no message at all; that opacity is the bug")
			},
		},
		{
			name: "open task still refreshes",
			assert: func(t *testing.T, status int, body httpcore.ErrorBody, err error) {
				require.NoError(t, err)
				assert.Zero(t, status)
				assert.Empty(t, body.Error)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			f := newRefreshFixture(t, ctx, true)
			if tc.arrange != nil {
				tc.arrange(t, ctx, f)
			}

			_, err := f.svc.RefreshTaskCandidates(ctx, service.RefreshTaskCandidatesRequest{
				TaskID: f.taskID,
				By:     manager,
			})

			var (
				status int
				body   httpcore.ErrorBody
			)
			if err != nil {
				status, body = httpcore.ClassifyError(err)
			}
			tc.assert(t, status, body, err)
		})
	}
}

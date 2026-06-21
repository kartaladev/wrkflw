package runtime

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
)

// TaskService authorizes human-task interactions and returns the engine triggers
// that the caller (typically via Runner.Deliver) feeds back into the process.
//
// Authorization happens here, in the runtime layer, so that the engine core
// remains pure and free of I/O. TaskService never calls engine.Step itself.
//
// Process variables for attribute-based authorization are carried in
// [humantask.HumanTask.Vars], snapshotted by the runtime's AwaitHuman perform
// at task-creation time. TaskService passes task.Vars to the Authorizer so that
// attribute predicates referencing data variables (e.g. vars["region"] == "EU")
// are correctly evaluated.
type TaskService struct {
	store      humantask.TaskStore
	authz      authz.Authorizer
	clk        clock.Clock
	humanTasks metric.Int64Counter
}

// taskServiceConfig holds the optional configuration for [TaskService].
type taskServiceConfig struct {
	mp metric.MeterProvider
}

// TaskServiceOption configures a [TaskService].
type TaskServiceOption func(*taskServiceConfig)

// WithTaskServiceMeterProvider sets the OTel meter provider used by the
// TaskService human-task lifecycle counter (default: the OTel global meter
// provider). A nil value is ignored.
//
// Use the same provider as the Runner to aggregate all lifecycle events
// (created, claimed, reassigned, completed) into one metric stream under the
// shared instrumentation scope "github.com/zakyalvan/krtlwrkflw/runtime".
func WithTaskServiceMeterProvider(mp metric.MeterProvider) TaskServiceOption {
	return func(c *taskServiceConfig) {
		if mp != nil {
			c.mp = mp
		}
	}
}

// NewTaskService constructs a TaskService with the given task store, authorizer,
// clock, and optional [TaskServiceOption] values.
//
// The variadic opts are additive; callers that do not need custom observability
// can omit them and the default OTel global meter provider is used.
func NewTaskService(store humantask.TaskStore, az authz.Authorizer, clk clock.Clock, opts ...TaskServiceOption) *TaskService {
	cfg := &taskServiceConfig{}
	for _, o := range opts {
		o(cfg)
	}
	var obsOpts []observability.Option
	if cfg.mp != nil {
		obsOpts = append(obsOpts, observability.WithMeterProvider(cfg.mp))
	}
	tel := observability.New(runnerInstrumentationName, obsOpts...)
	ts := &TaskService{
		store:      store,
		authz:      az,
		clk:        clk,
		humanTasks: tel.Int64Counter("wrkflw_human_tasks_total", "Human-task lifecycle transitions."),
	}
	return ts
}

// Claim authorizes actor against the task's eligibility and, on success, returns
// a HumanClaimed trigger for the caller to deliver to the engine via Runner.Deliver.
//
// task.Vars (snapshotted at task-creation by the runner's AwaitHuman perform) are
// forwarded to the Authorizer so that attribute predicates referencing process
// variables (e.g. vars["region"] == "EU") are correctly evaluated.
func (s *TaskService) Claim(ctx context.Context, taskToken string, actor authz.Actor) (engine.Trigger, error) {
	task, err := s.store.Get(ctx, taskToken)
	if err != nil {
		return nil, fmt.Errorf("runtime: taskservice: get task: %w", err)
	}
	if err := s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars); err != nil {
		return nil, fmt.Errorf("runtime: taskservice: claim: %w", err)
	}
	s.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "claimed")))
	return engine.NewHumanClaimed(s.clk.Now(), taskToken, actor), nil
}

// Reassign authorizes the by actor and returns a HumanReassigned trigger.
// by is the admin or supervisor performing the reassignment; from/to are actor IDs.
//
// Authorization policy: the reassigner (by) must satisfy the task's eligibility
// spec — the same check as Claim. A distinct admin/reassign-privilege model is
// deferred. from must equal the current claimant (task.ClaimedBy); if they differ,
// an error is returned and no trigger is issued, preventing a false From in the
// journal.
func (s *TaskService) Reassign(ctx context.Context, taskToken string, from, to string, by authz.Actor) (engine.Trigger, error) {
	task, err := s.store.Get(ctx, taskToken)
	if err != nil {
		return nil, fmt.Errorf("runtime: taskservice: get task: %w", err)
	}
	if from != task.ClaimedBy {
		return nil, fmt.Errorf("runtime: reassign: from %q is not the current claimant %q", from, task.ClaimedBy)
	}
	if err := s.authz.Authorize(ctx, task.Eligibility, by, task.Vars); err != nil {
		return nil, fmt.Errorf("runtime: taskservice: reassign: %w", err)
	}
	s.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "reassigned")))
	return engine.NewHumanReassigned(s.clk.Now(), taskToken, from, to, by), nil
}

// Complete authorizes actor and returns a HumanCompleted trigger carrying the
// actor's output variables.
func (s *TaskService) Complete(ctx context.Context, taskToken string, actor authz.Actor, output map[string]any) (engine.Trigger, error) {
	task, err := s.store.Get(ctx, taskToken)
	if err != nil {
		return nil, fmt.Errorf("runtime: taskservice: get task: %w", err)
	}
	if err := s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars); err != nil {
		return nil, fmt.Errorf("runtime: taskservice: complete: %w", err)
	}
	s.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "completed")))
	return engine.NewHumanCompleted(s.clk.Now(), taskToken, output, actor), nil
}

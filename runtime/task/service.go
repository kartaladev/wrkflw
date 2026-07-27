package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jonboulle/clockwork"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/internal/observability"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// TaskService authorizes human-task interactions and returns the engine triggers
// that the caller (typically via ProcessDriver.ApplyTrigger) feeds back into the process.
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
	store    humantask.TaskStore
	authz    authz.Authorizer
	resolver humantask.ActorResolver
	clk      clockwork.Clock
	// candidateResolveTimeout bounds the single ActorResolver lookup performed by
	// RefreshCandidates. Non-positive disables the bound.
	// Default: defaultCandidateResolveTimeout.
	candidateResolveTimeout time.Duration
	humanTasks              metric.Int64Counter
}

// ErrTaskNotOpen is returned by [TaskService.RefreshCandidates] when the task has
// already been completed or cancelled. A closed task's candidate list is part of
// its audit record and is never rewritten.
var ErrTaskNotOpen = errors.New("workflow-runtime: task is not open")

// ErrNoActorResolver is returned by [TaskService.RefreshCandidates] when the
// service was constructed without an [humantask.ActorResolver]; see
// [WithActorResolver].
var ErrNoActorResolver = errors.New("workflow-runtime: no ActorResolver configured")

// taskServiceConfig holds the optional configuration for [TaskService].
type taskServiceConfig struct {
	clk                     clockwork.Clock
	mp                      metric.MeterProvider
	resolver                humantask.ActorResolver
	candidateResolveTimeout time.Duration
}

// TaskServiceOption configures a [TaskService].
type TaskServiceOption func(*taskServiceConfig)

// WithTaskServiceMeterProvider sets the OTel meter provider used by the
// TaskService human-task lifecycle counter (default: the OTel global meter
// provider). A nil value is ignored.
//
// TaskService accepts only a meter provider (not logger/tracer) because it
// emits solely the wrkflw_human_tasks_total counter and has no spans or
// structured-log calls of its own. The full three-option idiom
// (WithLogger/WithTracerProvider/WithMeterProvider) applies to components that
// own spans or log output; TaskService is intentionally meter-only.
//
// Use the same provider as the ProcessDriver to aggregate all lifecycle events
// (created, claimed, reassigned, completed) into one metric stream under the
// shared instrumentation scope "github.com/kartaladev/wrkflw/runtime".
func WithTaskServiceMeterProvider(mp metric.MeterProvider) TaskServiceOption {
	return func(c *taskServiceConfig) {
		if mp != nil {
			c.mp = mp
		}
	}
}

// WithClock sets the time source used to stamp task-lifecycle triggers.
// Default: clockwork.NewRealClock(). A nil clock is ignored. Inject a fake clock in tests.
func WithClock(clk clockwork.Clock) TaskServiceOption {
	return func(c *taskServiceConfig) {
		if clk != nil {
			c.clk = clk
		}
	}
}

// WithActorResolver sets the resolver used to re-expand a task's eligibility spec
// into concrete actors. It is required only by [TaskService.RefreshCandidates],
// which returns [ErrNoActorResolver] when none is configured; Claim, Reassign and
// Complete do not use it. A nil resolver is ignored.
//
// Pass the same resolver the ProcessDriver uses, so a refreshed candidate list is
// resolved identically to the one minted when the task was created.
func WithActorResolver(r humantask.ActorResolver) TaskServiceOption {
	return func(c *taskServiceConfig) {
		if r != nil {
			c.resolver = r
		}
	}
}

// defaultCandidateResolveTimeout bounds the single [humantask.ActorResolver]
// lookup performed by [TaskService.RefreshCandidates] unless overridden via
// [WithCandidateResolveTimeout]. Without a bound, an unresponsive directory
// service holds the calling goroutine for as long as the caller's context allows
// — indefinitely for a caller that passes a background context.
//
// It duplicates the ProcessDriver's default for the identical lookup (see
// runtime.WithCandidateResolveTimeout), which is an unexported constant of the
// runtime package and therefore unreachable from here. The two values are
// deliberately equal: a refreshed candidate list must fail on the same schedule
// as the one minted when the task was created.
const defaultCandidateResolveTimeout = 10 * time.Second

// WithCandidateResolveTimeout sets the maximum duration the single
// [humantask.ActorResolver] lookup performed by [TaskService.RefreshCandidates]
// may run before its context is cancelled. The default is 10s. A non-positive d
// disables the bound (no deadline is applied).
//
// The resolver's Candidates must honour ctx cancellation for the timeout to take
// effect; a timed-out resolution returns an error and no trigger, so the task's
// stored candidate list is left untouched.
//
// Pass the same value given to the ProcessDriver's
// runtime.WithCandidateResolveTimeout, so that both derivations of a task's
// candidate list — the one minted at task creation and the one produced by a
// refresh — bound the resolver identically.
func WithCandidateResolveTimeout(d time.Duration) TaskServiceOption {
	return func(c *taskServiceConfig) {
		c.candidateResolveTimeout = d
	}
}

// NewTaskService constructs a TaskService with the given task store, authorizer,
// and optional [TaskServiceOption] values. The clock defaults to [clockwork.NewRealClock];
// inject a fake clock via [WithClock] in tests.
//
// The variadic opts are additive; callers that do not need custom observability
// or a custom clock can omit them entirely.
func NewTaskService(store humantask.TaskStore, az authz.Authorizer, opts ...TaskServiceOption) (*TaskService, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: store", kernel.ErrNilDependency)
	}
	if az == nil {
		return nil, fmt.Errorf("%w: authorizer", kernel.ErrNilDependency)
	}
	cfg := taskServiceConfig{
		clk:                     clockwork.NewRealClock(),
		candidateResolveTimeout: defaultCandidateResolveTimeout,
	}
	for _, o := range opts {
		o(&cfg)
	}
	var obsOpts []observability.Option
	if cfg.mp != nil {
		obsOpts = append(obsOpts, observability.WithMeterProvider(cfg.mp))
	}
	tel := observability.New(kernel.InstrumentationScope, obsOpts...)
	return &TaskService{
		store:                   store,
		authz:                   az,
		resolver:                cfg.resolver,
		clk:                     cfg.clk,
		candidateResolveTimeout: cfg.candidateResolveTimeout,
		humanTasks:              tel.Int64Counter("wrkflw_human_tasks_total", "Human-task lifecycle transitions."),
	}, nil
}

// Claim authorizes actor against the task's eligibility and, on success, returns
// a HumanClaimed trigger for the caller to deliver to the engine via ProcessDriver.ApplyTrigger.
//
// task.Vars (snapshotted at task-creation by the runner's AwaitHuman perform) are
// forwarded to the Authorizer so that attribute predicates referencing process
// variables (e.g. vars["region"] == "EU") are correctly evaluated.
func (s *TaskService) Claim(ctx context.Context, taskID string, actor authz.Actor) (engine.Trigger, error) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("workflow-runtime: taskservice: get task: %w", err)
	}
	if err := s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars); err != nil {
		return nil, fmt.Errorf("workflow-runtime: taskservice: claim: %w", err)
	}
	s.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "claimed")))
	return engine.NewHumanClaimed(s.clk.Now(), taskID, actor), nil
}

// Reassign authorizes the by actor and returns a HumanReassigned trigger.
// by is the admin or supervisor performing the reassignment; from/to are actor IDs.
//
// Authorization policy: the reassigner (by) must satisfy the task's eligibility
// spec — the same check as Claim. A distinct admin/reassign-privilege model is
// deferred. from must equal the current claimant (task.Claim.Actor.ID, empty when
// the task is unclaimed); if they differ, an error is returned and no trigger is
// issued, preventing a false From in the journal.
func (s *TaskService) Reassign(ctx context.Context, taskID string, from, to string, by authz.Actor) (engine.Trigger, error) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("workflow-runtime: taskservice: get task: %w", err)
	}
	var claimant string
	if task.Claim != nil {
		claimant = task.Claim.Actor.ID
	}
	if from != claimant {
		return nil, fmt.Errorf("workflow-runtime: reassign: from %q is not the current claimant %q", from, claimant)
	}
	if err := s.authz.Authorize(ctx, task.Eligibility, by, task.Vars); err != nil {
		return nil, fmt.Errorf("workflow-runtime: taskservice: reassign: %w", err)
	}
	s.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "reassigned")))
	return engine.NewHumanReassigned(s.clk.Now(), taskID, from, to, by), nil
}

// Complete authorizes actor and returns a HumanCompleted trigger carrying the
// actor's completion payload: the business outcome, a free-text note, and the
// output variables merged into the process.
//
// The outcome is validated by the engine against the node's declared outcomes:
// a value outside the set fails with [engine.ErrInvalidOutcome], and an empty
// value fails with [engine.ErrOutcomeRequired] because declaring a set makes the
// outcome mandatory. The zero [engine.CompletionInput] therefore completes a task
// only when its node declares no outcomes.
func (s *TaskService) Complete(ctx context.Context, taskID string, actor authz.Actor, c engine.CompletionInput) (engine.Trigger, error) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("workflow-runtime: taskservice: get task: %w", err)
	}
	if err := s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars); err != nil {
		return nil, fmt.Errorf("workflow-runtime: taskservice: complete: %w", err)
	}
	s.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "completed")))
	return engine.NewHumanCompleted(s.clk.Now(), taskID, c, actor), nil
}

// RefreshCandidates re-resolves the eligible actors for an open task and returns
// the [engine.HumanCandidatesResolved] trigger that replaces the task's candidate
// list. The caller delivers it via ProcessDriver.ApplyTrigger, exactly as with
// Claim/Reassign/Complete; this method never writes to the store itself.
//
// A task's candidates are resolved once, when the task is created, but the actor
// registry behind an [humantask.ActorResolver] is not static: people join and
// leave roles while a task sits open. Refresh re-runs the resolver against the
// task's stored eligibility spec and variable snapshot, so the list reflects
// current membership. The resolved set REPLACES the previous one — a departed
// actor disappears.
//
// Candidates are a projection, not an access-control list: authorization is
// always evaluated live against the task's eligibility spec by Claim and
// Complete, so an actor who becomes eligible can act on a task whose candidate
// list has not been refreshed. Refresh keeps the rendered list and the
// candidate-ID arm of [humantask.TaskStore.ClaimableBy] current; it grants
// nothing.
//
// by is the actor requesting the refresh and must satisfy the task's eligibility
// spec — the same policy Reassign applies. A distinct admin/refresh-privilege
// model is deferred.
//
// The resolver lookup is bounded by the service's candidate-resolve timeout
// (default 10s, see [WithCandidateResolveTimeout]) so that an unresponsive
// directory service cannot hold the calling goroutine indefinitely.
//
// Errors: [humantask.ErrTaskNotFound] for an unknown task, [ErrTaskNotOpen] for a
// completed or cancelled one (a closed task's candidate list is part of its audit
// record and stays frozen), [ErrNoActorResolver] when no resolver was configured,
// the authorizer's error when by is not eligible, and the resolver's error —
// [context.DeadlineExceeded] when it outlives the candidate-resolve timeout.
func (s *TaskService) RefreshCandidates(ctx context.Context, taskID string, by authz.Actor) (engine.Trigger, error) {
	if s.resolver == nil {
		return nil, fmt.Errorf("workflow-runtime: taskservice: refresh candidates: %w", ErrNoActorResolver)
	}
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("workflow-runtime: taskservice: get task: %w", err)
	}
	if !task.IsOpen() {
		return nil, fmt.Errorf("workflow-runtime: taskservice: refresh candidates: task %q is %s: %w",
			taskID, task.State, ErrTaskNotOpen)
	}
	if err := s.authz.Authorize(ctx, task.Eligibility, by, task.Vars); err != nil {
		return nil, fmt.Errorf("workflow-runtime: taskservice: refresh candidates: %w", err)
	}
	actors, err := s.resolveCandidates(ctx, task.Eligibility, task.Vars)
	if err != nil {
		return nil, fmt.Errorf("workflow-runtime: taskservice: resolve candidates: %w", err)
	}
	s.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "candidates_refreshed")))
	return engine.NewHumanCandidatesResolved(s.clk.Now(), taskID, actors), nil
}

// resolveCandidates performs one ActorResolver lookup under the service's
// candidate-resolve timeout. It exists so the bound is applied at exactly one
// place, and follows the ProcessDriver's convention for the identical lookup: a
// non-positive timeout passes the parent context through unchanged.
func (s *TaskService) resolveCandidates(ctx context.Context, spec authz.AuthzSpec, vars map[string]any) ([]authz.Actor, error) {
	if s.candidateResolveTimeout <= 0 {
		return s.resolver.Candidates(ctx, spec, vars)
	}
	rctx, cancel := context.WithTimeout(ctx, s.candidateResolveTimeout)
	defer cancel()
	return s.resolver.Candidates(rctx, spec, vars)
}

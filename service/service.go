package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
)

// Service is the single application-layer seam between the transport adapters
// (REST, gRPC) and the workflow engine. All operations are transport-neutral:
// request and result types carry no HTTP/gRPC concerns.
//
// Domain errors (kernel.ErrInstanceNotFound, kernel.ErrDefinitionNotFound,
// authz.ErrNotAuthorized, kernel.ErrConcurrentUpdate, humantask.ErrTaskNotFound)
// are propagated as-is so transport layers can classify them correctly.
type Service interface {
	// StartInstance resolves the process definition by req.DefRef, starts a new
	// instance with the given ID and initial variables, and returns the resulting
	// state (completed or parked).
	StartInstance(ctx context.Context, req StartInstanceRequest) (engine.InstanceState, error)

	// GetInstance loads and returns the current state of an existing instance.
	// Returns kernel.ErrInstanceNotFound when no instance exists for the ID.
	GetInstance(ctx context.Context, instanceID string) (engine.InstanceState, error)

	// DeliverSignal resolves the definition for the instance, then delivers a
	// SignalReceived trigger to it, returning the new state. Returns ErrConflict
	// when the instance has already reached a terminal state.
	DeliverSignal(ctx context.Context, req DeliverSignalRequest) (engine.InstanceState, error)

	// DeliverMessage routes a message to the waiting instance via the runner's
	// internal message-waiter table. The definition is resolved by req.DefRef.
	DeliverMessage(ctx context.Context, req DeliverMessageRequest) error

	// ClaimTask authorizes the actor via TaskService.Claim, then delivers the
	// resulting trigger to the engine, returning the new state.
	ClaimTask(ctx context.Context, req ClaimTaskRequest) (engine.InstanceState, error)

	// CompleteTask authorizes the actor via TaskService.Complete, then delivers
	// the resulting trigger to the engine, returning the new state.
	CompleteTask(ctx context.Context, req CompleteTaskRequest) (engine.InstanceState, error)

	// ReassignTask authorizes the reassigner via TaskService.Reassign, then
	// delivers the resulting trigger to the engine, returning the new state.
	ReassignTask(ctx context.Context, req ReassignTaskRequest) (engine.InstanceState, error)

	// ListInstances returns a paginated list of instance summaries matching the filter.
	ListInstances(ctx context.Context, filter kernel.InstanceFilter) (kernel.InstancePage, error)

	// ResolveIncident clears an open incident on a process instance, grants
	// addAttempts additional execution attempts (≤ 0 defaults to 1), and
	// re-drives the instance. It delegates to Runner.ResolveIncident after
	// resolving the process definition from the registry.
	//
	// Returns the resulting InstanceState (parked or completed) on success.
	// Propagates kernel.ErrInstanceNotFound when no instance exists for the ID.
	ResolveIncident(ctx context.Context, req ResolveIncidentRequest) (engine.InstanceState, error)

	// CancelInstance terminates a running process instance, running any
	// definition-level cancel actions best-effort. Returns ErrConflict when the
	// instance has already reached a terminal state.
	CancelInstance(ctx context.Context, req CancelInstanceRequest) (engine.InstanceState, error)

	// GetInstanceWithDefinition loads the current state of an existing instance
	// and resolves its process definition from the registry. It is the transport
	// layer's entry point whenever both the state and the definition are needed
	// (e.g. to build an InstanceSnapshot or ActionableView).
	//
	// Returns kernel.ErrInstanceNotFound when no instance exists for the ID and
	// kernel.ErrDefinitionNotFound when the registry has no entry for the
	// instance's DefID:DefVersion key.
	GetInstanceWithDefinition(ctx context.Context, instanceID string) (engine.InstanceState, *model.ProcessDefinition, error)
}

// Engine is the concrete implementation of Service. It wires together the
// runtime.ProcessDriver, runtime.TaskService, kernel.DefinitionRegistry,
// kernel.Store, kernel.InstanceLister, and humantask.TaskStore.
//
// The constructor requires all collaborators as interface/concrete parameters;
// no DI container is used so consumers can wire this by hand.
//
// Registry key contract: the DefinitionRegistry must be populated with keys
// in "DefID:DefVersion" format for the resolveDefinition helper to work when
// loading an existing instance. Short aliases (e.g. the bare definition ID)
// may also be registered for use with StartInstance.
type Engine struct {
	runner    *runtime.ProcessDriver
	tasks     *task.TaskService
	reg       kernel.DefinitionRegistry
	store     kernel.Store
	lister    kernel.InstanceLister
	taskStore humantask.TaskStore
	clk       clock.Clock
}

// EngineOption configures an Engine returned by New.
type EngineOption func(*Engine)

// WithEngineClock sets the time source used to stamp signal triggers.
// Default: clock.System(). A nil clock is ignored.
func WithEngineClock(clk clock.Clock) EngineOption {
	return func(e *Engine) {
		if clk != nil {
			e.clk = clk
		}
	}
}

// New constructs an Engine facade. The first six parameters are required;
// opts are optional functional options (e.g. WithEngineClock).
//
//   - runner: drives process execution (Run / Deliver / DeliverMessage).
//   - tasks: authorizes and returns triggers for human-task interactions.
//   - reg: resolves DefRef strings to *model.ProcessDefinition values.
//     Keys must be in "DefID:DefVersion" format for resolveDefinition to work
//     on existing instances. Short aliases are also accepted for StartInstance.
//   - store: loads instance state for GetInstance and definition resolution.
//   - lister: enumerates instance summaries for ListInstances.
//   - taskStore: used to resolve the owning instance ID from a task token in
//     task-lifecycle operations (ClaimTask, CompleteTask, ReassignTask).
//   - opts: optional; use WithEngineClock to override the default clock.System().
func New(
	runner *runtime.ProcessDriver,
	tasks *task.TaskService,
	reg kernel.DefinitionRegistry,
	store kernel.Store,
	lister kernel.InstanceLister,
	taskStore humantask.TaskStore,
	opts ...EngineOption,
) *Engine {
	e := &Engine{
		runner:    runner,
		tasks:     tasks,
		reg:       reg,
		store:     store,
		lister:    lister,
		taskStore: taskStore,
		clk:       clock.System(),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Compile-time assertion: *Engine satisfies Service.
var _ Service = (*Engine)(nil)

// StartInstance resolves the process definition by req.DefRef, starts a new
// instance, and returns the resulting state.
func (e *Engine) StartInstance(ctx context.Context, req StartInstanceRequest) (engine.InstanceState, error) {
	def, err := e.reg.Lookup(ctx, req.DefRef)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: start instance: %w", err)
	}
	st, err := e.runner.Run(ctx, def, req.InstanceID, req.Vars)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: start instance: run: %w", err)
	}
	return st, nil
}

// GetInstance loads and returns the current state of an existing instance.
func (e *Engine) GetInstance(ctx context.Context, instanceID string) (engine.InstanceState, error) {
	st, _, err := e.store.Load(ctx, instanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: get instance: %w", err)
	}
	return st, nil
}

// GetInstanceWithDefinition loads the current state of an existing instance and
// resolves its process definition from the registry. It delegates to the private
// resolveDefinition helper so that both pieces are fetched atomically from the
// same store and registry.
func (e *Engine) GetInstanceWithDefinition(ctx context.Context, instanceID string) (engine.InstanceState, *model.ProcessDefinition, error) {
	def, st, err := e.resolveDefinition(ctx, instanceID)
	if err != nil {
		return engine.InstanceState{}, nil, fmt.Errorf("workflow-service: get instance with definition: %w", err)
	}
	return st, def, nil
}

// DeliverSignal resumes a process instance that is parked at a signal-catch
// node by delivering a SignalReceived trigger. Returns ErrConflict when the
// instance has already reached a terminal state.
func (e *Engine) DeliverSignal(ctx context.Context, req DeliverSignalRequest) (engine.InstanceState, error) {
	def, st, err := e.resolveDefinition(ctx, req.InstanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: deliver signal: %w", err)
	}
	if isTerminal(st.Status) {
		return engine.InstanceState{}, fmt.Errorf("%w: instance %q is in a terminal state", ErrConflict, req.InstanceID)
	}
	trg := engine.NewSignalReceived(e.clk.Now(), req.Signal, req.Payload)
	newSt, err := e.runner.Deliver(ctx, def, st.InstanceID, trg)
	if err != nil {
		// No ErrInvalidTransition classification here: SignalReceived uses
		// broadcast semantics in the engine — a signal matching no awaiting
		// token is a clean no-op, never a wrong-state error. There is nothing
		// to reclassify on this path (see ADR-0026).
		return engine.InstanceState{}, fmt.Errorf("workflow-service: deliver signal: %w", err)
	}
	return newSt, nil
}

// DeliverMessage routes a message to the waiting instance via the runner's
// message-waiter table. No-op when no instance is waiting.
func (e *Engine) DeliverMessage(ctx context.Context, req DeliverMessageRequest) error {
	def, err := e.reg.Lookup(ctx, req.DefRef)
	if err != nil {
		return fmt.Errorf("workflow-service: deliver message: %w", err)
	}
	if err := e.runner.DeliverMessage(ctx, def, req.Name, req.CorrelationKey, req.Payload); err != nil {
		// No ErrInvalidTransition classification here: DeliverMessage routes via
		// the runner's waiter table and no-ops when no instance is waiting, so a
		// wrong-state error is not produced on this path (see ADR-0026).
		return fmt.Errorf("workflow-service: deliver message: %w", err)
	}
	return nil
}

// ClaimTask authorizes the actor, issues a HumanClaimed trigger, and advances the instance.
func (e *Engine) ClaimTask(ctx context.Context, req ClaimTaskRequest) (engine.InstanceState, error) {
	trg, err := e.tasks.Claim(ctx, req.TaskToken, req.Actor)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: claim task: %w", err)
	}
	return e.deliverTaskTrigger(ctx, req.TaskToken, trg)
}

// CompleteTask authorizes the actor, issues a HumanCompleted trigger, and advances the instance.
func (e *Engine) CompleteTask(ctx context.Context, req CompleteTaskRequest) (engine.InstanceState, error) {
	trg, err := e.tasks.Complete(ctx, req.TaskToken, req.Actor, req.Output)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: complete task: %w", err)
	}
	return e.deliverTaskTrigger(ctx, req.TaskToken, trg)
}

// ReassignTask authorizes the reassigner, issues a HumanReassigned trigger, and advances the instance.
func (e *Engine) ReassignTask(ctx context.Context, req ReassignTaskRequest) (engine.InstanceState, error) {
	trg, err := e.tasks.Reassign(ctx, req.TaskToken, req.From, req.To, req.By)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: reassign task: %w", err)
	}
	return e.deliverTaskTrigger(ctx, req.TaskToken, trg)
}

// ListInstances delegates to the InstanceLister.
func (e *Engine) ListInstances(ctx context.Context, filter kernel.InstanceFilter) (kernel.InstancePage, error) {
	page, err := e.lister.List(ctx, filter)
	if err != nil {
		return kernel.InstancePage{}, fmt.Errorf("workflow-service: list instances: %w", err)
	}
	return page, nil
}

// ResolveIncident resolves an open incident on a process instance by resolving
// its definition from the registry and delegating to Runner.ResolveIncident.
// AddAttempts ≤ 0 is coerced to 1 so callers always grant at least one attempt.
func (e *Engine) ResolveIncident(ctx context.Context, req ResolveIncidentRequest) (engine.InstanceState, error) {
	def, _, err := e.resolveDefinition(ctx, req.InstanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: resolve incident: %w", err)
	}
	addAttempts := req.AddAttempts
	if addAttempts <= 0 {
		addAttempts = 1
	}
	st, err := e.runner.ResolveIncident(ctx, def, req.InstanceID, req.IncidentID, addAttempts)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: resolve incident: %w", err)
	}
	return st, nil
}

// CancelInstance resolves the instance's definition, rejects an already-terminal
// instance with ErrConflict, and delegates to Runner.CancelInstance.
func (e *Engine) CancelInstance(ctx context.Context, req CancelInstanceRequest) (engine.InstanceState, error) {
	def, st, err := e.resolveDefinition(ctx, req.InstanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: cancel instance: %w", err)
	}
	if isTerminal(st.Status) {
		return engine.InstanceState{}, fmt.Errorf("%w: instance %q is already terminal", ErrConflict, req.InstanceID)
	}
	st, err = e.runner.CancelInstance(ctx, def, req.InstanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: cancel instance: %w", err)
	}
	return st, nil
}

// resolveDefinition loads the instance state by instanceID, then looks up its
// definition by "DefID:DefVersion" in the registry.
//
// Returns the definition, the current instance state, and any error. Both
// ErrInstanceNotFound and ErrDefinitionNotFound propagate as-is through the
// wrapping chain so transport layers can classify them with errors.Is.
func (e *Engine) resolveDefinition(ctx context.Context, instanceID string) (*model.ProcessDefinition, engine.InstanceState, error) {
	st, _, err := e.store.Load(ctx, instanceID)
	if err != nil {
		return nil, engine.InstanceState{}, err
	}
	defRef := fmt.Sprintf("%s:%d", st.DefID, st.DefVersion)
	def, err := e.reg.Lookup(ctx, defRef)
	if err != nil {
		return nil, engine.InstanceState{}, err
	}
	return def, st, nil
}

// deliverTaskTrigger is the shared helper for ClaimTask, CompleteTask, and
// ReassignTask. It looks up the task by token to get the owning instance ID,
// checks that both the task and its instance are in a state that accepts the
// operation, resolves the definition, and delivers the trigger.
func (e *Engine) deliverTaskTrigger(ctx context.Context, taskToken string, trg engine.Trigger) (engine.InstanceState, error) {
	task, err := e.taskStore.Get(ctx, taskToken)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: deliver task trigger: get task: %w", err)
	}
	if !task.IsOpen() {
		return engine.InstanceState{}, fmt.Errorf("%w: task %q is not open", ErrConflict, taskToken)
	}
	def, st, err := e.resolveDefinition(ctx, task.InstanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-service: deliver task trigger: resolve definition: %w", err)
	}
	if isTerminal(st.Status) {
		return engine.InstanceState{}, fmt.Errorf("%w: instance %q is in a terminal state", ErrConflict, task.InstanceID)
	}
	newSt, err := e.runner.Deliver(ctx, def, task.InstanceID, trg)
	if err != nil {
		if errors.Is(err, engine.ErrInvalidTransition) {
			return engine.InstanceState{}, fmt.Errorf("%w: %w", ErrConflict, err)
		}
		return engine.InstanceState{}, fmt.Errorf("workflow-service: deliver task trigger: deliver: %w", err)
	}
	return newSt, nil
}

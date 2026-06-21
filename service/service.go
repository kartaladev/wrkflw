package service

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Service is the single application-layer seam between the transport adapters
// (REST, gRPC) and the workflow engine. All operations are transport-neutral:
// request and result types carry no HTTP/gRPC concerns.
//
// Domain errors (runtime.ErrInstanceNotFound, runtime.ErrDefinitionNotFound,
// authz.ErrNotAuthorized, runtime.ErrConcurrentUpdate, humantask.ErrTaskNotFound)
// are propagated as-is so transport layers can classify them correctly.
type Service interface {
	// StartInstance resolves the process definition by req.DefRef, starts a new
	// instance with the given ID and initial variables, and returns the resulting
	// state (completed or parked).
	StartInstance(ctx context.Context, req StartInstanceRequest) (engine.InstanceState, error)

	// GetInstance loads and returns the current state of an existing instance.
	// Returns runtime.ErrInstanceNotFound when no instance exists for the ID.
	GetInstance(ctx context.Context, instanceID string) (engine.InstanceState, error)

	// DeliverSignal resolves the definition for the instance, then delivers a
	// SignalReceived trigger to it, returning the new state.
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
	ListInstances(ctx context.Context, filter runtime.InstanceFilter) (runtime.InstancePage, error)
}

// Engine is the concrete implementation of Service. It wires together the
// runtime.Runner, runtime.TaskService, runtime.DefinitionRegistry,
// runtime.Store, and runtime.InstanceLister.
//
// The constructor requires all collaborators as interface/concrete parameters;
// no DI container is used so consumers can wire this by hand.
//
// Registry key contract: the DefinitionRegistry must be populated with keys
// in "DefID:DefVersion" format for the resolveDefinition helper to work when
// loading an existing instance. Short aliases (e.g. the bare definition ID)
// may also be registered for use with StartInstance.
type Engine struct {
	runner    *runtime.Runner
	tasks     *runtime.TaskService
	reg       runtime.DefinitionRegistry
	store     runtime.Store
	lister    runtime.InstanceLister
	taskStore humantask.TaskStore
	clk       clock.Clock
}

// New constructs an Engine facade. All parameters are required.
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
//   - clk: the time source used to stamp signal triggers.
func New(
	runner *runtime.Runner,
	tasks *runtime.TaskService,
	reg runtime.DefinitionRegistry,
	store runtime.Store,
	lister runtime.InstanceLister,
	taskStore humantask.TaskStore,
	clk clock.Clock,
) *Engine {
	return &Engine{
		runner:    runner,
		tasks:     tasks,
		reg:       reg,
		store:     store,
		lister:    lister,
		taskStore: taskStore,
		clk:       clk,
	}
}

// Compile-time assertion: *Engine satisfies Service.
var _ Service = (*Engine)(nil)

// StartInstance resolves the process definition by req.DefRef, starts a new
// instance, and returns the resulting state.
func (e *Engine) StartInstance(ctx context.Context, req StartInstanceRequest) (engine.InstanceState, error) {
	def, err := e.reg.Lookup(req.DefRef)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: start instance: %w", err)
	}
	st, err := e.runner.Run(ctx, def, req.InstanceID, req.Vars)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: start instance: run: %w", err)
	}
	return st, nil
}

// GetInstance loads and returns the current state of an existing instance.
func (e *Engine) GetInstance(ctx context.Context, instanceID string) (engine.InstanceState, error) {
	st, _, err := e.store.Load(ctx, instanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: get instance: %w", err)
	}
	return st, nil
}

// DeliverSignal resumes a process instance that is parked at a signal-catch
// node by delivering a SignalReceived trigger.
func (e *Engine) DeliverSignal(ctx context.Context, req DeliverSignalRequest) (engine.InstanceState, error) {
	def, st, err := e.resolveDefinition(ctx, req.InstanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: deliver signal: %w", err)
	}
	trg := engine.NewSignalReceived(e.clk.Now(), req.Signal, req.Payload)
	newSt, err := e.runner.Deliver(ctx, def, st.InstanceID, trg)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: deliver signal: %w", err)
	}
	return newSt, nil
}

// DeliverMessage routes a message to the waiting instance via the runner's
// message-waiter table. No-op when no instance is waiting.
func (e *Engine) DeliverMessage(ctx context.Context, req DeliverMessageRequest) error {
	def, err := e.reg.Lookup(req.DefRef)
	if err != nil {
		return fmt.Errorf("service: deliver message: %w", err)
	}
	if err := e.runner.DeliverMessage(ctx, def, req.Name, req.CorrelationKey, req.Payload); err != nil {
		return fmt.Errorf("service: deliver message: %w", err)
	}
	return nil
}

// ClaimTask authorizes the actor, issues a HumanClaimed trigger, and advances the instance.
func (e *Engine) ClaimTask(ctx context.Context, req ClaimTaskRequest) (engine.InstanceState, error) {
	trg, err := e.tasks.Claim(ctx, req.TaskToken, req.Actor)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: claim task: %w", err)
	}
	return e.deliverTaskTrigger(ctx, req.TaskToken, trg)
}

// CompleteTask authorizes the actor, issues a HumanCompleted trigger, and advances the instance.
func (e *Engine) CompleteTask(ctx context.Context, req CompleteTaskRequest) (engine.InstanceState, error) {
	trg, err := e.tasks.Complete(ctx, req.TaskToken, req.Actor, req.Output)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: complete task: %w", err)
	}
	return e.deliverTaskTrigger(ctx, req.TaskToken, trg)
}

// ReassignTask authorizes the reassigner, issues a HumanReassigned trigger, and advances the instance.
func (e *Engine) ReassignTask(ctx context.Context, req ReassignTaskRequest) (engine.InstanceState, error) {
	trg, err := e.tasks.Reassign(ctx, req.TaskToken, req.From, req.To, req.By)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: reassign task: %w", err)
	}
	return e.deliverTaskTrigger(ctx, req.TaskToken, trg)
}

// ListInstances delegates to the InstanceLister.
func (e *Engine) ListInstances(ctx context.Context, filter runtime.InstanceFilter) (runtime.InstancePage, error) {
	page, err := e.lister.List(ctx, filter)
	if err != nil {
		return runtime.InstancePage{}, fmt.Errorf("service: list instances: %w", err)
	}
	return page, nil
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
	def, err := e.reg.Lookup(defRef)
	if err != nil {
		return nil, engine.InstanceState{}, err
	}
	return def, st, nil
}

// deliverTaskTrigger is the shared helper for ClaimTask, CompleteTask, and
// ReassignTask. It looks up the task by token to get the owning instance ID,
// resolves the definition for that instance, and delivers the trigger.
func (e *Engine) deliverTaskTrigger(ctx context.Context, taskToken string, trg engine.Trigger) (engine.InstanceState, error) {
	task, err := e.taskStore.Get(ctx, taskToken)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: deliver task trigger: get task: %w", err)
	}
	def, _, err := e.resolveDefinition(ctx, task.InstanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: deliver task trigger: resolve definition: %w", err)
	}
	st, err := e.runner.Deliver(ctx, def, task.InstanceID, trg)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("service: deliver task trigger: deliver: %w", err)
	}
	return st, nil
}

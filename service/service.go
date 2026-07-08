package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/idgen"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
)

// InstanceStarter starts new process instances.
type InstanceStarter interface {
	// StartInstance resolves the process definition by req.DefRef, starts a new
	// instance with the given ID and initial variables, and returns the resulting
	// ProcessInstance (completed or parked).
	StartInstance(ctx context.Context, req StartInstanceRequest) (ProcessInstance, error)
}

// InstanceReader reads instance state.
type InstanceReader interface {
	// GetInstance loads and returns the current ProcessInstance for an existing
	// instance. The fused definition is nil when it cannot be resolved from the
	// registry; only instance-not-found / store errors are returned.
	GetInstance(ctx context.Context, instanceID string) (ProcessInstance, error)

	// ListInstances returns a paginated list of instance summaries matching the filter.
	ListInstances(ctx context.Context, filter kernel.InstanceFilter) (kernel.InstancePage, error)
}

// TaskManager performs human-task lifecycle operations.
type TaskManager interface {
	// ClaimTask authorizes the actor via TaskService.Claim, then delivers the
	// resulting trigger to the engine, returning the new ProcessInstance.
	ClaimTask(ctx context.Context, req ClaimTaskRequest) (ProcessInstance, error)

	// CompleteTask authorizes the actor via TaskService.Complete, then delivers
	// the resulting trigger to the engine, returning the new ProcessInstance.
	CompleteTask(ctx context.Context, req CompleteTaskRequest) (ProcessInstance, error)

	// ReassignTask authorizes the reassigner via TaskService.Reassign, then
	// delivers the resulting trigger to the engine, returning the new ProcessInstance.
	ReassignTask(ctx context.Context, req ReassignTaskRequest) (ProcessInstance, error)
}

// Messaging delivers signals and messages to running instances.
type Messaging interface {
	// DeliverSignal resolves the definition for the instance, then delivers a
	// SignalReceived trigger to it, returning the new ProcessInstance. Returns
	// ErrConflict when the instance has already reached a terminal state.
	DeliverSignal(ctx context.Context, req DeliverSignalRequest) (ProcessInstance, error)

	// DeliverMessage routes a message to the waiting instance via the driver's
	// internal message-waiter table. The definition is resolved by req.DefRef.
	DeliverMessage(ctx context.Context, req DeliverMessageRequest) error
}

// InstanceOps performs administrative operations on running instances.
type InstanceOps interface {
	// ResolveIncident clears an open incident on a process instance, grants
	// addAttempts additional execution attempts (≤ 0 defaults to 1), and
	// re-drives the instance. It delegates to ProcessDriver.ResolveIncident after
	// resolving the process definition from the registry.
	//
	// Returns the resulting ProcessInstance (parked or completed) on success.
	// Propagates kernel.ErrInstanceNotFound when no instance exists for the ID.
	ResolveIncident(ctx context.Context, req ResolveIncidentRequest) (ProcessInstance, error)

	// CancelInstance terminates a running process instance, running any
	// definition-level cancel actions best-effort. Returns ErrConflict when the
	// instance has already reached a terminal state.
	CancelInstance(ctx context.Context, req CancelInstanceRequest) (ProcessInstance, error)
}

// Service is the single application-layer seam between the HTTP transport
// adapters and the workflow engine. All operations are transport-neutral:
// request and result types carry no HTTP-transport concerns.
//
// Domain errors (kernel.ErrInstanceNotFound, kernel.ErrDefinitionNotFound,
// authz.ErrNotAuthorized, kernel.ErrConcurrentUpdate, humantask.ErrTaskNotFound)
// are propagated as-is so transport layers can classify them correctly.
type Service interface {
	InstanceStarter
	InstanceReader
	TaskManager
	Messaging
	InstanceOps
}

// Engine is the concrete implementation of Service. It wires together the
// runtime.ProcessDriver, task.TaskService, kernel.DefinitionRegistry,
// kernel.InstanceStore, kernel.InstanceLister, and humantask.TaskStore.
//
// The constructor requires all collaborators as interface/concrete parameters;
// no DI container is used so consumers can wire this by hand.
//
// Registry key contract: the DefinitionRegistry must be populated with keys
// in "DefID:DefVersion" format for the resolveDefinition helper to work when
// loading an existing instance. Short aliases (e.g. the bare definition ID)
// may also be registered for use with StartInstance.
type Engine struct {
	driver    *runtime.ProcessDriver
	tasks     *task.TaskService
	reg       kernel.DefinitionRegistry
	store     kernel.InstanceStore
	lister    kernel.InstanceLister
	taskStore humantask.TaskStore
	clk       clock.Clock
	idgen     idgen.Generator
	// ownsDriver is true only when NewEngine built the driver itself (no driver
	// was injected via WithProcessDriver). It gates Start/Shutdown so a
	// consumer-injected driver is never started or torn down by the Engine.
	ownsDriver bool
}

// NewEngine constructs an Engine facade from functional options over a coherent
// in-memory default graph. Called with no options it wires a fully-functional,
// non-durable engine: an in-memory instance store, the process-global definition
// registry, an in-memory human-task store, an allow-all authorizer, and a driver
// built over those same leaves (so the store the driver writes is the store the
// reader loads from).
//
// Options that receive nil are ignored (the default is kept). A required leaf
// resolving to nil surfaces as ErrNilDependency during validation.
func NewEngine(opts ...Option) (*Engine, error) {
	c := &engineConfig{}
	for _, o := range opts {
		if o != nil {
			o(c)
		}
	}

	// In-memory defaults are applied only in the non-durable path so that a
	// DurableProvider returning a nil required leaf surfaces via validation
	// rather than being silently replaced.
	if !c.durable {
		if c.store == nil {
			ms, err := kernel.NewMemInstanceStore()
			if err != nil {
				return nil, fmt.Errorf("workflow-service: default instance store: %w", err)
			}
			c.store = ms
		}
		if c.reg == nil {
			c.reg = runtime.DefaultDefinitionRegistry()
		}
		if c.taskStore == nil {
			c.taskStore = humantask.NewMemTaskStore()
		}
	}
	if c.clk == nil {
		c.clk = clock.System()
	}
	if c.idgen == nil {
		c.idgen = idgen.XID()
	}
	if c.authz == nil {
		c.authz = authz.AllowAll{}
	}
	// In the non-durable path, derive the lister from the in-memory store when
	// possible. In the durable path the provider must supply the lister
	// explicitly (a real durable InstanceStore does not double as a lister), so
	// a nil provider lister surfaces via validation instead of being rescued.
	if !c.durable && c.lister == nil {
		if l, ok := c.store.(kernel.InstanceLister); ok {
			c.lister = l
		}
	}

	// Fail-fast on required leaves before building collaborators from them.
	if err := validateEngineLeaves(c); err != nil {
		return nil, err
	}

	tasks, err := task.NewTaskService(c.taskStore, c.authz,
		task.WithClock(c.clk), task.WithDefinitionResolver(c.reg))
	if err != nil {
		return nil, fmt.Errorf("workflow-service: task service: %w", err)
	}

	driver := c.driver
	ownsDriver := c.driver == nil
	if driver == nil {
		dopts := []runtime.Option{
			runtime.WithInstanceStore(c.store),
			runtime.WithDefinitions(c.reg),
			runtime.WithClock(c.clk),
			runtime.WithIDGenerator(c.idgen),
		}
		if c.timerStore != nil {
			dopts = append(dopts, runtime.WithTimerStore(c.timerStore))
		}
		if c.callLinkStore != nil {
			dopts = append(dopts, runtime.WithCallLinkStore(c.callLinkStore))
		}
		d, derr := runtime.NewProcessDriver(dopts...)
		if derr != nil {
			return nil, fmt.Errorf("workflow-service: default driver: %w", derr)
		}
		driver = d
	}
	if driver == nil {
		return nil, fmt.Errorf("%w: process driver", ErrNilDependency)
	}

	e := &Engine{
		driver:     driver,
		tasks:      tasks,
		reg:        c.reg,
		store:      c.store,
		lister:     c.lister,
		taskStore:  c.taskStore,
		clk:        c.clk,
		idgen:      c.idgen,
		ownsDriver: ownsDriver,
	}
	e.logConstructionSummary(c)
	return e, nil
}

// Start starts the engine's owned process driver (its in-process scheduler),
// binding its lifetime to ctx. It is a no-op when the driver was supplied by
// the consumer via WithProcessDriver (that driver is consumer-owned). Idempotent.
func (e *Engine) Start(ctx context.Context) error {
	if !e.ownsDriver {
		return nil
	}
	if err := e.driver.Start(ctx); err != nil {
		return fmt.Errorf("workflow-service: start: %w", err)
	}
	return nil
}

// Shutdown releases resources the engine owns — currently its owned process
// driver. A consumer-injected driver is left untouched. Idempotent; matches
// samber/do ShutdownerWithContextAndError.
func (e *Engine) Shutdown(ctx context.Context) error {
	if !e.ownsDriver {
		return nil
	}
	if err := e.driver.Shutdown(ctx); err != nil {
		return fmt.Errorf("workflow-service: shutdown: %w", err)
	}
	return nil
}

// validateEngineLeaves ensures every required leaf resolved to a non-nil value
// before any collaborator is built from it. Run before constructing the task
// service and driver so a nil leaf (e.g. from a DurableProvider) surfaces as
// service.ErrNilDependency rather than a downstream kernel/runtime error.
func validateEngineLeaves(c *engineConfig) error {
	switch {
	case c.store == nil:
		return fmt.Errorf("%w: instance store", ErrNilDependency)
	case c.reg == nil:
		return fmt.Errorf("%w: definition registry", ErrNilDependency)
	case c.lister == nil:
		return fmt.Errorf("%w: instance lister", ErrNilDependency)
	case c.taskStore == nil:
		return fmt.Errorf("%w: task store", ErrNilDependency)
	}
	return nil
}

// logConstructionSummary emits a DEBUG-level summary of the resolved engine graph
// so a consumer wiring an engine can see which defaults are in effect.
func (e *Engine) logConstructionSummary(c *engineConfig) {
	storeLabel := "in-memory(non-durable)"
	if c.durable {
		storeLabel = "durable"
	}
	authzLabel := "custom"
	if _, ok := c.authz.(authz.AllowAll); ok {
		authzLabel = "allow-all"
	}
	defLabel := "custom"
	if c.reg == runtime.DefaultDefinitionRegistry() {
		defLabel = "default-global"
	}
	slog.Default().LogAttrs(context.Background(), slog.LevelDebug,
		"service.Engine constructed",
		slog.String("store", storeLabel),
		slog.String("definitions", defLabel),
		slog.String("taskStore", storeLabel),
		slog.String("authz", authzLabel),
		slog.String("hint", "in-memory graph is not durable; wire service.WithDurableStore(persistence.NewDurableProvider(...)) for production"),
	)
}

// Compile-time assertion: *Engine satisfies Service.
var _ Service = (*Engine)(nil)

// StartInstance resolves the process definition by req.DefRef, mints a new
// instance ID via the configured generator, and returns the resulting
// ProcessInstance (completed or parked).
func (e *Engine) StartInstance(ctx context.Context, req StartInstanceRequest) (ProcessInstance, error) {
	def, err := e.reg.Lookup(ctx, req.DefRef)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: start instance: %w", err)
	}
	id, err := e.idgen.NewID()
	if err != nil {
		return nil, fmt.Errorf("workflow-service: start instance: generate id: %w", err)
	}
	st, err := e.driver.Drive(ctx, def, id, req.Vars)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: start instance: run: %w", err)
	}
	return NewProcessInstance(def, st), nil
}

// GetInstance loads and returns the current ProcessInstance for an existing
// instance. The fused definition is resolved best-effort from the registry and
// is nil when the registry has no matching entry — a missing definition is NOT
// an error on this path (only instance-not-found / store errors are returned).
func (e *Engine) GetInstance(ctx context.Context, instanceID string) (ProcessInstance, error) {
	st, _, err := e.store.Load(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: get instance: %w", err)
	}
	def, _ := e.reg.Lookup(ctx, model.Version(st.DefID, st.DefVersion))
	return NewProcessInstance(def, st), nil
}

// DeliverSignal resumes a process instance that is parked at a signal-catch
// node by delivering a SignalReceived trigger. Returns ErrConflict when the
// instance has already reached a terminal state.
func (e *Engine) DeliverSignal(ctx context.Context, req DeliverSignalRequest) (ProcessInstance, error) {
	def, st, err := e.resolveDefinition(ctx, req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: deliver signal: %w", err)
	}
	if isTerminal(st.Status) {
		return nil, fmt.Errorf("%w: instance %q is in a terminal state", ErrConflict, req.InstanceID)
	}
	trg := engine.NewSignalReceived(e.clk.Now(), req.Signal, req.Payload)
	newSt, err := e.driver.ApplyTrigger(ctx, def, st.InstanceID, trg)
	if err != nil {
		// No ErrInvalidTransition classification here: SignalReceived uses
		// broadcast semantics in the engine — a signal matching no awaiting
		// token is a clean no-op, never a wrong-state error. There is nothing
		// to reclassify on this path (see ADR-0026).
		return nil, fmt.Errorf("workflow-service: deliver signal: %w", err)
	}
	return NewProcessInstance(def, newSt), nil
}

// DeliverMessage routes a message to the waiting instance via the driver's
// message-waiter table. No-op when no instance is waiting.
func (e *Engine) DeliverMessage(ctx context.Context, req DeliverMessageRequest) error {
	def, err := e.reg.Lookup(ctx, req.DefRef)
	if err != nil {
		return fmt.Errorf("workflow-service: deliver message: %w", err)
	}
	if err := e.driver.DeliverMessage(ctx, def, req.Name, req.CorrelationKey, req.Payload); err != nil {
		// No ErrInvalidTransition classification here: DeliverMessage routes via
		// the driver's waiter table and no-ops when no instance is waiting, so a
		// wrong-state error is not produced on this path (see ADR-0026).
		return fmt.Errorf("workflow-service: deliver message: %w", err)
	}
	return nil
}

// ClaimTask authorizes the actor, issues a HumanClaimed trigger, and advances the instance.
func (e *Engine) ClaimTask(ctx context.Context, req ClaimTaskRequest) (ProcessInstance, error) {
	trg, err := e.tasks.Claim(ctx, req.TaskToken, req.Actor)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: claim task: %w", err)
	}
	return e.deliverTaskTrigger(ctx, req.TaskToken, trg)
}

// CompleteTask authorizes the actor, issues a HumanCompleted trigger, and advances the instance.
func (e *Engine) CompleteTask(ctx context.Context, req CompleteTaskRequest) (ProcessInstance, error) {
	trg, err := e.tasks.Complete(ctx, req.TaskToken, req.Actor, req.Output)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: complete task: %w", err)
	}
	return e.deliverTaskTrigger(ctx, req.TaskToken, trg)
}

// ReassignTask authorizes the reassigner, issues a HumanReassigned trigger, and advances the instance.
func (e *Engine) ReassignTask(ctx context.Context, req ReassignTaskRequest) (ProcessInstance, error) {
	trg, err := e.tasks.Reassign(ctx, req.TaskToken, req.From, req.To, req.By)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: reassign task: %w", err)
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
// its definition from the registry and delegating to ProcessDriver.ResolveIncident.
// AddAttempts ≤ 0 is coerced to 1 so callers always grant at least one attempt.
func (e *Engine) ResolveIncident(ctx context.Context, req ResolveIncidentRequest) (ProcessInstance, error) {
	def, _, err := e.resolveDefinition(ctx, req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: resolve incident: %w", err)
	}
	addAttempts := req.AddAttempts
	if addAttempts <= 0 {
		addAttempts = 1
	}
	st, err := e.driver.ResolveIncident(ctx, def, req.InstanceID, req.IncidentID, addAttempts)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: resolve incident: %w", err)
	}
	return NewProcessInstance(def, st), nil
}

// CancelInstance resolves the instance's definition, rejects an already-terminal
// instance with ErrConflict, and delegates to ProcessDriver.CancelInstance.
func (e *Engine) CancelInstance(ctx context.Context, req CancelInstanceRequest) (ProcessInstance, error) {
	def, st, err := e.resolveDefinition(ctx, req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: cancel instance: %w", err)
	}
	if isTerminal(st.Status) {
		return nil, fmt.Errorf("%w: instance %q is already terminal", ErrConflict, req.InstanceID)
	}
	st, err = e.driver.CancelInstance(ctx, def, req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: cancel instance: %w", err)
	}
	return NewProcessInstance(def, st), nil
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
	def, err := e.reg.Lookup(ctx, model.Version(st.DefID, st.DefVersion))
	if err != nil {
		return nil, engine.InstanceState{}, err
	}
	return def, st, nil
}

// deliverTaskTrigger is the shared helper for ClaimTask, CompleteTask, and
// ReassignTask. It looks up the task by token to get the owning instance ID,
// checks that both the task and its instance are in a state that accepts the
// operation, resolves the definition, and delivers the trigger.
func (e *Engine) deliverTaskTrigger(ctx context.Context, taskToken string, trg engine.Trigger) (ProcessInstance, error) {
	task, err := e.taskStore.Get(ctx, taskToken)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: deliver task trigger: get task: %w", err)
	}
	if !task.IsOpen() {
		return nil, fmt.Errorf("%w: task %q is not open", ErrConflict, taskToken)
	}
	def, st, err := e.resolveDefinition(ctx, task.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("workflow-service: deliver task trigger: resolve definition: %w", err)
	}
	if isTerminal(st.Status) {
		return nil, fmt.Errorf("%w: instance %q is in a terminal state", ErrConflict, task.InstanceID)
	}
	newSt, err := e.driver.ApplyTrigger(ctx, def, task.InstanceID, trg)
	if err != nil {
		if errors.Is(err, engine.ErrInvalidTransition) {
			return nil, fmt.Errorf("%w: %w", ErrConflict, err)
		}
		return nil, fmt.Errorf("workflow-service: deliver task trigger: deliver: %w", err)
	}
	return NewProcessInstance(def, newSt), nil
}

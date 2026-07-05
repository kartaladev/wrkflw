package processtest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
)

// defaultClockStart is the fixed instant a Harness clock starts at when
// WithClockStart is not supplied, so timer-driven tests are reproducible.
var defaultClockStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Harness is an in-memory test fixture that wires a [runtime.ProcessDriver]
// together with a [MemStore], a [FakeClock], a [kernel.MemScheduler], and the
// spy fakes ([SpyCatalog], [SpyAuthorizer]) plus an in-memory human-task stack.
// It drives a definition to completion without any external infrastructure.
//
// Construct one with [New]; drive an instance with [Harness.Start] then
// [Harness.DriveToCompletion]. Accessors expose the owned collaborators for
// assertions.
type Harness struct {
	store    *kernel.MemInstanceStore
	clk      *FakeClock
	sched    *kernel.MemScheduler
	catalog  *SpyCatalog
	authz    *SpyAuthorizer
	tasks    *humantask.MemTaskStore
	resolver humantask.ActorResolver
	taskSvc  *task.TaskService
	bus      *signal.SignalBus
	driver   *runtime.ProcessDriver

	// mu guards instanceDefs, which maps each started instance id to the definition
	// it was started/driven with. The signal bus resolves the correct definition
	// per instance from this map (a single instance-agnostic "last def" would
	// misroute a bus resume to the wrong definition when multiple instances of
	// different definitions run on one harness). Guarded so concurrent Start /
	// DriveToCompletion / bus delivery are race-free under -race.
	mu           sync.Mutex
	instanceDefs map[string]*model.ProcessDefinition

	driveLimit int
}

// config accumulates [Option] settings before New builds the Harness.
type config struct {
	actions    map[string]action.Action
	decide     DecideFunc
	resolver   humantask.ActorResolver
	defs       kernel.DefinitionRegistry
	withBus    bool
	driveLimit int
	clockStart time.Time
}

// Option configures a [Harness].
type Option func(*config)

// WithAction registers a single action under name in the harness catalog.
func WithAction(name string, a action.Action) Option {
	return func(c *config) {
		if c.actions == nil {
			c.actions = make(map[string]action.Action)
		}
		c.actions[name] = a
	}
}

// WithActionFunc registers fn as an action under name.
func WithActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) Option {
	return WithAction(name, action.ActionFunc(fn))
}

// WithActions registers a whole action map in the harness catalog.
func WithActions(m map[string]action.Action) Option {
	return func(c *config) {
		if c.actions == nil {
			c.actions = make(map[string]action.Action)
		}
		for k, v := range m {
			c.actions[k] = v
		}
	}
}

// WithAuthorizer programs the harness [SpyAuthorizer] decision. A nil fn leaves
// the default allow-all behaviour.
func WithAuthorizer(fn DecideFunc) Option {
	return func(c *config) { c.decide = fn }
}

// WithActorResolver overrides the human-task actor resolver (default: an empty
// [humantask.StaticActorResolver], which resolves candidates via role sharing).
func WithActorResolver(r humantask.ActorResolver) Option {
	return func(c *config) { c.resolver = r }
}

// WithDefinitions wires a definition registry so call-activity nodes can resolve
// their child definitions.
func WithDefinitions(reg kernel.DefinitionRegistry) Option {
	return func(c *config) { c.defs = reg }
}

// WithSignalBus wires a [signal.SignalBus] into the driver so signal throw nodes
// and bus-driven fan-out work. Access it via [Harness.Bus].
func WithSignalBus() Option {
	return func(c *config) { c.withBus = true }
}

// WithDriveLimit overrides the maximum number of drive steps (default 1000).
func WithDriveLimit(n int) Option {
	return func(c *config) { c.driveLimit = n }
}

// WithClockStart sets the instant the harness [FakeClock] starts at.
func WithClockStart(t time.Time) Option {
	return func(c *config) { c.clockStart = t }
}

// New builds a Harness with the whole in-memory stack wired together.
func New(opts ...Option) (*Harness, error) {
	cfg := config{
		driveLimit: defaultDriveLimit,
		clockStart: defaultClockStart,
	}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.resolver == nil {
		cfg.resolver = humantask.NewStaticActorResolver(nil)
	}

	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		return nil, err
	}

	clk := NewFakeClock(cfg.clockStart)
	sched := kernel.NewMemScheduler(kernel.WithMemSchedulerClock(clk))
	catalog := NewSpyCatalog(action.NewMapCatalog(cfg.actions))
	az := NewSpyAuthorizer()
	if cfg.decide != nil {
		az.SetDecision(cfg.decide)
	}
	tasks := humantask.NewMemTaskStore()

	taskSvc, err := task.NewTaskService(tasks, az, task.WithClock(clk))
	if err != nil {
		return nil, err
	}

	h := &Harness{
		store:        store,
		clk:          clk,
		sched:        sched,
		catalog:      catalog,
		authz:        az,
		tasks:        tasks,
		resolver:     cfg.resolver,
		taskSvc:      taskSvc,
		instanceDefs: make(map[string]*model.ProcessDefinition),
		driveLimit:   cfg.driveLimit,
	}

	driverOpts := []runtime.Option{
		runtime.WithClock(clk),
		runtime.WithScheduler(sched),
		runtime.WithHumanTasks(cfg.resolver, tasks, az),
	}
	if cfg.defs != nil {
		driverOpts = append(driverOpts, runtime.WithDefinitions(cfg.defs))
	}
	if cfg.withBus {
		// Forward-reference wiring: the bus delivers resume triggers via the
		// driver, which is built with the bus. Close over h.driver, assign below.
		bus, berr := signal.NewSignalBus(func(ctx context.Context, instanceID string, trg engine.Trigger) error {
			// h.driver is set right after NewProcessDriver returns; the bus is only
			// invoked during Deliver/Publish, well after construction. Resolve the
			// instance's own definition so multi-definition fan-out routes correctly.
			def := h.defFor(instanceID)
			if def == nil {
				return fmt.Errorf("processtest: no definition recorded for instance %q (start it before publishing)", instanceID)
			}
			_, derr := h.driver.Deliver(ctx, def, instanceID, trg)
			return derr
		}, signal.WithClock(clk))
		if berr != nil {
			return nil, berr
		}
		h.bus = bus
		driverOpts = append(driverOpts, runtime.WithSignalBus(bus))
	}

	driver, err := runtime.NewProcessDriver(append([]runtime.Option{runtime.WithActionCatalog(catalog), runtime.WithInstanceStore(store)}, driverOpts...)...)
	if err != nil {
		return nil, err
	}
	h.driver = driver
	return h, nil
}

// recordDef associates instance id with its definition so the signal bus can
// resolve the correct definition for a bus-delivered resume trigger.
func (h *Harness) recordDef(id string, def *model.ProcessDefinition) {
	h.mu.Lock()
	h.instanceDefs[id] = def
	h.mu.Unlock()
}

// defFor returns the definition recorded for instance id, or nil if none.
func (h *Harness) defFor(id string) *model.ProcessDefinition {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.instanceDefs[id]
}

// Start creates and runs a new instance, returning its (likely parked) state.
func (h *Harness) Start(ctx context.Context, def *model.ProcessDefinition, id string, vars map[string]any) (engine.InstanceState, error) {
	h.recordDef(id, def)
	return h.driver.Run(ctx, def, id, vars)
}

// DriveToCompletion advances instance id (already started) until it reaches a
// terminal status, the handler returns [Stop]/[Abort], or the drive limit is hit.
// Because the harness owns the clock and scheduler, [AdvanceTimers] decisions are
// honoured here.
//
// The clock and scheduler are shared across all instances on this harness, so an
// [AdvanceTimers] while driving one instance advances the single fake clock and
// fires every globally-due timer — including timers belonging to other in-flight
// instances. For isolated timer assertions across unrelated instances, use one
// Harness per instance.
func (h *Harness) DriveToCompletion(ctx context.Context, def *model.ProcessDefinition, id string, handler ParkHandler) (engine.InstanceState, error) {
	h.recordDef(id, def)
	state, _, err := h.store.Load(ctx, id)
	if err != nil {
		return engine.InstanceState{}, err
	}
	env := harnessEnv{h: h, def: def, id: id}
	return drive(ctx, env, state, handler)
}

// harnessEnv is the [driveEnv] backed by the Harness, able to advance timers.
type harnessEnv struct {
	h   *Harness
	def *model.ProcessDefinition
	id  string
}

func (e harnessEnv) deliver(ctx context.Context, trg engine.Trigger) (engine.InstanceState, error) {
	return e.h.driver.Deliver(ctx, e.def, e.id, trg)
}

func (e harnessEnv) advanceTimers(ctx context.Context) (engine.InstanceState, error) {
	fireAt, ok := e.h.sched.NextFireAt()
	if !ok {
		return engine.InstanceState{}, ErrNoPendingTimer
	}
	// Only ever move the clock forward: a timer whose fireAt is already at or
	// before now is due and fires on Tick without rewinding the shared clock.
	if fireAt.After(e.h.clk.Now()) {
		e.h.clk.Set(fireAt)
	}
	if err := e.h.sched.Tick(ctx); err != nil {
		return engine.InstanceState{}, err
	}
	state, _, err := e.h.store.Load(ctx, e.id)
	return state, err
}

// classify enriches the pure [Classify] result with scheduler knowledge: an
// intermediate timer catch parks as a bare command wait in instance state
// (classified async-child/unknown), but the armed timer is visible only in the
// scheduler. A parked command-wait token whose AwaitCommand matches a pending
// scheduler timer IS a timer park — so promote precisely by that token's own
// command id, never by "some timer somewhere is pending" (which would misclassify
// a genuine async call-activity park that merely coexists with an unrelated
// deadline timer).
func (e harnessEnv) classify(state engine.InstanceState) Park {
	p := Classify(state)
	if p.Reason == ReasonTerminal {
		return p
	}
	for _, tok := range state.Tokens {
		if tok.State != engine.TokenWaitingCommand || tok.AwaitCommand == "" {
			continue
		}
		if _, ok := e.h.sched.Pending(tok.AwaitCommand); !ok {
			continue
		}
		p.HasArmedTimers = true
		if p.Reason == ReasonAsyncChild || p.Reason == ReasonUnknown {
			p.Reason = ReasonTimer
			p.Node = tok.NodeID
		}
		break
	}
	return p
}

func (e harnessEnv) limit() int { return e.h.driveLimit }

// Accessors.

// Driver returns the wired process driver.
func (h *Harness) Driver() *runtime.ProcessDriver { return h.driver }

// Store returns the in-memory store.
func (h *Harness) Store() *kernel.MemInstanceStore { return h.store }

// Clock returns the fake clock shared by the driver and scheduler.
func (h *Harness) Clock() *FakeClock { return h.clk }

// Scheduler returns the in-memory scheduler.
func (h *Harness) Scheduler() *kernel.MemScheduler { return h.sched }

// Catalog returns the spy action catalog.
func (h *Harness) Catalog() *SpyCatalog { return h.catalog }

// Authorizer returns the spy authorizer.
func (h *Harness) Authorizer() *SpyAuthorizer { return h.authz }

// Tasks returns the in-memory human-task store.
func (h *Harness) Tasks() *humantask.MemTaskStore { return h.tasks }

// TaskService returns the human-task service (Claim/Complete/Reassign).
func (h *Harness) TaskService() *task.TaskService { return h.taskSvc }

// Bus returns the signal bus, or nil when WithSignalBus was not supplied.
func (h *Harness) Bus() *signal.SignalBus { return h.bus }

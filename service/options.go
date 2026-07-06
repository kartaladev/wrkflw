package service

import (
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/idgen"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// Option configures NewEngine. Options that receive nil are ignored (the
// coherent in-memory default is kept), except WithDurableStore leaves, which
// are set as-is so a nil leaf surfaces as ErrNilDependency during validation.
type Option func(*engineConfig)

type engineConfig struct {
	driver        *runtime.ProcessDriver
	store         kernel.InstanceStore
	reg           kernel.DefinitionRegistry
	lister        kernel.InstanceLister
	taskStore     humantask.TaskStore
	authz         authz.Authorizer
	timerStore    kernel.TimerStore
	callLinkStore kernel.CallLinkStore
	clk           clock.Clock
	idgen         idgen.Generator
	durable       bool
}

// WithProcessDriver supplies a pre-built driver (escape hatch for tests /
// advanced wiring). When set, NewEngine does not build a driver from the leaves.
func WithProcessDriver(d *runtime.ProcessDriver) Option {
	return func(c *engineConfig) {
		if d != nil {
			c.driver = d
		}
	}
}

// WithInstanceStore overrides the in-memory instance store.
func WithInstanceStore(s kernel.InstanceStore) Option {
	return func(c *engineConfig) {
		if s != nil {
			c.store = s
		}
	}
}

// WithDefinitions overrides the default process-global definition registry.
func WithDefinitions(reg kernel.DefinitionRegistry) Option {
	return func(c *engineConfig) {
		if reg != nil {
			c.reg = reg
		}
	}
}

// WithLister overrides the instance lister (defaults to the instance store when
// it satisfies kernel.InstanceLister).
func WithLister(l kernel.InstanceLister) Option {
	return func(c *engineConfig) {
		if l != nil {
			c.lister = l
		}
	}
}

// WithHumanTasks overrides the human-task store and authorizer used to build
// the internal task service.
func WithHumanTasks(taskStore humantask.TaskStore, az authz.Authorizer) Option {
	return func(c *engineConfig) {
		if taskStore != nil {
			c.taskStore = taskStore
		}
		if az != nil {
			c.authz = az
		}
	}
}

// WithClock overrides the clock used by the engine and the internal task
// service (and the default driver).
func WithClock(clk clock.Clock) Option {
	return func(c *engineConfig) {
		if clk != nil {
			c.clk = clk
		}
	}
}

// WithIDGenerator sets the strategy used to mint every new process-instance ID.
// Default: idgen.XID(). A nil generator is ignored. It is also threaded into the
// default driver, so runtime and service agree on the strategy.
func WithIDGenerator(gen idgen.Generator) Option {
	return func(c *engineConfig) {
		if gen != nil {
			c.idgen = gen
		}
	}
}

// WithDurableStore flips the whole graph durable in one call, setting every
// leaf from the provider and (because the driver is built from those leaves)
// rebuilding the driver durable-coherent. Marking the config durable disables
// the in-memory defaults, so a provider that returns a nil REQUIRED leaf
// (instance store, definitions, lister, or task store) surfaces as
// ErrNilDependency during NewEngine validation rather than being silently
// replaced by an in-memory default.
//
// Precedence is last-writer-wins in option order: a finer per-leaf override
// (e.g. WithInstanceStore) placed AFTER WithDurableStore replaces that single
// leaf; placed before, it is overwritten by the provider. A nil provider is
// ignored.
//
// The driver NewEngine builds from the provider's leaves wires only the
// instance store, definitions, timer store, and call-link store — it does not
// arm human-task nodes or a scheduler. For a durable graph whose processes use
// human tasks or timers, supply a fully-wired *runtime.ProcessDriver via
// WithProcessDriver (built with runtime.WithHumanTasks/WithScheduler) alongside
// WithDurableStore; the service still reads the provider's stores/registries.
func WithDurableStore(p DurableProvider) Option {
	return func(c *engineConfig) {
		if p == nil {
			return
		}
		c.durable = true
		c.store = p.InstanceStore()
		c.reg = p.Definitions()
		c.lister = p.Lister()
		c.taskStore = p.TaskStore()
		c.timerStore = p.TimerStore()
		c.callLinkStore = p.CallLinkStore()
	}
}

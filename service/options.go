package service

import (
	"github.com/jonboulle/clockwork"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/idgen"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// Option configures NewProcessEngine. Options that receive nil are ignored (the
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
	resolver      humantask.ActorResolver
	timerStore    kernel.TimerStore
	callLinkStore kernel.CallLinkStore
	clk           clockwork.Clock
	idgen         idgen.Generator
	durable       bool
	// omitDefinition suppresses the `definition` embed in the marshalled
	// ProcessInstance document. Zero value false keeps the embed, so the
	// default is unchanged.
	omitDefinition bool
}

// WithProcessDriver supplies a pre-built driver (escape hatch for tests /
// advanced wiring). When set, NewProcessEngine does not build a driver from the leaves.
func WithProcessDriver(driver *runtime.ProcessDriver) Option {
	return func(c *engineConfig) {
		if driver != nil {
			c.driver = driver
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

// WithActorResolver supplies the resolver the internal task service uses to
// re-expand a task's eligibility spec into concrete actors. It is required only
// by [ProcessEngine.RefreshTaskCandidates], which returns
// [task.ErrNoActorResolver] when none is configured; claiming, reassigning and
// completing never consult it. A nil resolver is ignored.
//
// There is no default: candidate resolution is consumer-specific I/O (a
// directory or group-membership lookup), so an engine built without this option
// simply cannot refresh. Pass the SAME resolver the driver was built with via
// runtime.WithHumanTasks, so a refreshed candidate list is derived identically
// to the one minted when the task was created.
func WithActorResolver(r humantask.ActorResolver) Option {
	return func(c *engineConfig) {
		if r != nil {
			c.resolver = r
		}
	}
}

// WithClock overrides the clock used by the engine and the internal task
// service (and the default driver).
func WithClock(clk clockwork.Clock) Option {
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

// WithoutEmbeddedDefinition drops the `definition` key from every
// ProcessInstance document this engine marshals. The identity — `def_id` and
// `def_version` — is read off the instance state and is NOT suppressed, so a
// slimmed document still names the template it runs.
//
// The embed is the default: it makes the document self-contained, so
// a consumer that has never seen the template can render the instance from one
// payload. The cost is duplication — the template is byte-identical for every
// instance of a definition and, on a typical graph, is the LARGER half of the
// document, re-shipped on every read. Opt out when the consumer already holds
// the definition: a UI polling one instance, or an aggregate assembled from N
// GetInstance calls that share a handful of templates. Such consumers can fetch
// each template once and key it by (def_id, def_version).
//
// This is a marshalling policy only. [ProcessInstance.Definition] keeps
// returning the resolved template to in-process consumers either way, and
// [NewProcessInstance] — which fabricates an instance outside the engine —
// always embeds.
func WithoutEmbeddedDefinition() Option {
	return func(c *engineConfig) { c.omitDefinition = true }
}

// WithDurableStore flips the whole graph durable in one call, setting every
// leaf from the provider and (because the driver is built from those leaves)
// rebuilding the driver durable-coherent. Marking the config durable disables
// the in-memory defaults, so a provider that returns a nil REQUIRED leaf
// (instance store, definitions, lister, or task store) surfaces as
// ErrNilDependency during NewProcessEngine validation rather than being silently
// replaced by an in-memory default.
//
// Precedence is last-writer-wins in option order: a finer per-leaf override
// (e.g. WithInstanceStore) placed AFTER WithDurableStore replaces that single
// leaf; placed before, it is overwritten by the provider. A nil provider is
// ignored.
//
// The driver NewProcessEngine builds from the provider's leaves wires only the
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

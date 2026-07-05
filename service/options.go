package service

import (
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
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

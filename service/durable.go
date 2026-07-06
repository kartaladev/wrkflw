package service

import (
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// DurableProvider supplies a coherent set of durable graph leaves. The
// driver-backed implementation lives in the persistence package (which may
// import DB drivers); service depends only on this interface so DB drivers
// never enter service's compile graph.
//
// InstanceStore, Definitions, Lister, and TaskStore are required (a nil value
// surfaces as ErrNilDependency during NewEngine validation). TimerStore and
// CallLinkStore are optional driver leaves; nil leaves them at the in-memory
// default.
type DurableProvider interface {
	InstanceStore() kernel.InstanceStore
	Definitions() kernel.DefinitionRegistry
	Lister() kernel.InstanceLister
	TaskStore() humantask.TaskStore
	TimerStore() kernel.TimerStore
	CallLinkStore() kernel.CallLinkStore
}

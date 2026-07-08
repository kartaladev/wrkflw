package kernel

import "errors"

// ErrNilDependency is returned by runtime constructors when a required,
// non-nilable dependency (interface or pointer) is nil. Wrap it with the
// argument name via %w.
var ErrNilDependency = errors.New("workflow-runtime: nil required dependency")

// ErrUnresolvedTimerDefinitions is returned (wrapped) by [JobStore.LoadScheduled]
// when one or more armed timers reference a process definition that cannot be
// found in the configured [DefinitionRegistry]. The partial result (resolvable
// jobs) is still returned alongside this error.
//
// Callers performing automatic self-rehydration (e.g. [scheduling.Scheduler] via
// [scheduling.WithJobStore]) treat this error as non-fatal — startup continues
// with the resolved subset and a WARN is logged. Callers requiring all timers to
// be resolved (e.g. an explicit [ProcessDriver.RehydrateTimers]) may choose to
// propagate the error.
var ErrUnresolvedTimerDefinitions = errors.New("workflow-runtime: some armed timers reference unregistered definitions")

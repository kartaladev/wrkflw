package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

var (
	// registerMu serializes the scan-then-register sequence performed by
	// RegisterDefinition/MustRegisterDefinition (message-start uniqueness check
	// followed by the actual registry write) so two concurrent registrations
	// can never both observe an empty collision set and both succeed
	// (TOCTOU-free).
	registerMu sync.Mutex

	// ErrDuplicateMessageStart is returned by RegisterDefinition/
	// MustRegisterDefinition when a definition's message-start name collides
	// with an already-registered message-start — either on a different
	// definition, or on another start node within the same definition
	// (message-start names must be unique; structural validation does not
	// catch the intra-definition case). See ADR-0121.
	ErrDuplicateMessageStart = errors.New("workflow-runtime: duplicate message start name")
)

// defaultDefinitionRegistry is the process-global DefinitionRegistry a
// ProcessDriver uses when [WithDefinitions] is not supplied. Populate it at
// application startup via [RegisterDefinition] or [MustRegisterDefinition].
//
// # Test isolation
//
// This registry is process-wide: once a definition is registered under a given
// "<ID>:<Version>" key it cannot be overwritten (first-registration-wins). Tests
// that need a clean slate must build an isolated registry and pass it via
// [WithDefinitions](kernel.NewMemDefinitionRegistry()) rather than using the
// global default.
var defaultDefinitionRegistry = kernel.NewMemDefinitionRegistry()

// DefaultDefinitionRegistry returns the process-global [kernel.MemDefinitionRegistry]
// used by [NewProcessDriver] when no [WithDefinitions] option is supplied. The
// same pointer is returned on every call — callers may bind it to local variables
// to compare identity.
//
// Use [RegisterDefinition] or [MustRegisterDefinition] for the ergonomic one-call
// path; call this function directly only when you need the registry value itself
// (e.g. to pass it to a [kernel.CachingDefinitionRegistry]).
func DefaultDefinitionRegistry() *kernel.MemDefinitionRegistry {
	return defaultDefinitionRegistry
}

// RegisterDefinition registers def into the process-global [DefaultDefinitionRegistry].
// It is the definition-side counterpart of [action.Register].
//
// The definition is indexed under both "<ID>" and "<ID>:<Version>" so a
// [engine.StartSubInstance] DefRef in either form resolves correctly. The bare
// "<ID>" key always points to the most-recently-registered version.
//
// Returns:
//   - [kernel.ErrNilDefinition] if def is nil.
//   - [kernel.ErrEmptyDefinitionID] if def.ID is empty.
//   - [kernel.ErrDefinitionExists] (wrapped with the versioned key) if
//     "<ID>:<Version>" was already registered (first-registration-wins).
//   - [ErrDuplicateMessageStart] (wrapped with the colliding name) if any of
//     def's message-start names is already claimed by another registered
//     definition's message-start, or is repeated on two of def's own start
//     nodes (see ADR-0121).
//
// For init-time wiring where a registration failure is a programming error use
// [MustRegisterDefinition].
//
// On successful registration it logs (via [slog.Default]) a WARN for each
// redundant force-termination end event — one that is the only end event in its
// definition, so it has no sibling branch to cancel (see [event.WithForceTermination]).
//
// # Test isolation
//
// The global registry is process-wide. Tests that need an isolated registry must
// pass [WithDefinitions](kernel.NewMemDefinitionRegistry()) to [NewProcessDriver]
// and register directly into that isolated instance.
func RegisterDefinition(def *model.ProcessDefinition) error {
	registerMu.Lock()
	defer registerMu.Unlock()

	if err := checkMessageStartUnique(defaultDefinitionRegistry, def); err != nil {
		return err
	}
	if err := defaultDefinitionRegistry.Register(def); err != nil {
		return err
	}
	warnForceTermination(def)
	warnMixedSubprocessStart(def)
	return nil
}

// MustRegisterDefinition registers def into the process-global
// [DefaultDefinitionRegistry] and panics if registration fails — including on
// [ErrDuplicateMessageStart]. Intended for init-time wiring where a
// registration failure is a programming error (e.g. in package-level var
// blocks or TestMain).
//
// See [RegisterDefinition] for the error-returning variant and the full contract.
func MustRegisterDefinition(def *model.ProcessDefinition) {
	registerMu.Lock()
	defer registerMu.Unlock()

	if err := checkMessageStartUnique(defaultDefinitionRegistry, def); err != nil {
		panic(err)
	}
	defaultDefinitionRegistry.MustRegister(def)
	warnForceTermination(def)
	warnMixedSubprocessStart(def)
}

// checkMessageStartUnique rejects def with [ErrDuplicateMessageStart] when any
// of its message-start names collides with an already-registered definition's
// message-start, or is repeated across two of def's own start nodes. Callers
// must hold registerMu for the whole scan-then-register sequence so two
// concurrent registrations can never both observe a clean collision set
// (TOCTOU-free).
func checkMessageStartUnique(reg *kernel.MemDefinitionRegistry, def *model.ProcessDefinition) error {
	incoming, err := messageStartNames(def)
	if err != nil {
		return err
	}
	if len(incoming) == 0 {
		return nil
	}

	// Compare only against the LATEST version of each OTHER def id: a
	// MemDefinitionRegistry retains every registered version so in-flight
	// instances resume, but only the latest version holds an active message-start
	// subscription (ADR-0121 Camunda semantics). A superseded version's name must
	// therefore not cause a false collision, and a redeploy that keeps the same
	// name is still allowed via the existing existing.ID == def.ID skip.
	for _, existing := range latestPerID(reg.ListDefinitions(context.Background())) {
		if def != nil && existing.ID == def.ID {
			continue // re-registering the same def id is not a message-name collision
		}
		existingNames, err := messageStartNames(existing)
		if err != nil {
			// An already-registered definition failing its own intra-def check
			// would be a bug in a prior registration, not something the current
			// call should surface; skip it defensively rather than block def.
			continue
		}
		for name := range incoming {
			if existingNames[name] {
				return fmt.Errorf("%w: %q", ErrDuplicateMessageStart, name)
			}
		}
	}
	return nil
}

// messageStartNames collects def's message-start event names (its StartNodes
// that carry a non-empty MessageName) into a set. It returns
// [ErrDuplicateMessageStart] when the same name is declared on two different
// start nodes of def itself — a case structural validation does not catch,
// since ADR-0121 permits any number of event-triggered start events.
func messageStartNames(def *model.ProcessDefinition) (map[string]bool, error) {
	if def == nil {
		return nil, nil
	}
	names := make(map[string]bool)
	for _, n := range def.StartNodes() {
		se, ok := n.(event.StartEvent)
		if !ok || se.MessageName == "" {
			continue
		}
		if names[se.MessageName] {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateMessageStart, se.MessageName)
		}
		names[se.MessageName] = true
	}
	return names, nil
}

// forceTerminationWarnings returns a non-fatal warning for each force-termination
// end event in a definition that has only a single end event: force-termination
// exists to cancel *other* branches, so on a single-end definition it is merely
// redundant. Definitions with 2 or more end events produce no warning, since the
// force-termination end then has other branches to cancel.
func forceTerminationWarnings(def *model.ProcessDefinition) []string {
	if def == nil {
		return nil
	}
	var ends, forced []string
	for _, n := range def.Nodes {
		if n.Kind() != model.KindEndEvent {
			continue
		}
		ends = append(ends, n.ID())
		if ev, ok := n.(event.EndEvent); ok && ev.ForceTermination {
			forced = append(forced, n.ID())
		}
	}
	if len(ends) > 1 {
		return nil
	}
	var warns []string
	for _, id := range forced {
		warns = append(warns, fmt.Sprintf(
			"workflow-runtime: end event %q in definition %q forces termination but is the only end event; force-termination has no other branch to cancel (redundant)",
			id, def.ID))
	}
	return warns
}

// warnForceTermination logs each forceTerminationWarnings entry for def at WARN
// level via the process-wide slog default logger.
func warnForceTermination(def *model.ProcessDefinition) {
	for _, w := range forceTerminationWarnings(def) {
		slog.Default().Warn(w)
	}
}

// mixedSubprocessStartWarnings returns a non-fatal warning for each SubProcess
// node in def whose nested definition declares BOTH at least one manual/none
// start (a StartEvent with no signal/message/timer trigger) AND at least one
// event-triggered start. Such a SubProcess is treated as an event sub-process —
// the engine arms it at its event-triggered start — so its manual-start branch
// is dead (never entered). This is a foot-gun, not wrong behaviour, so it is a
// WARN rather than a validation error (ADR-0122 review). Only top-level
// SubProcess nodes are inspected, mirroring forceTerminationWarnings.
func mixedSubprocessStartWarnings(def *model.ProcessDefinition) []string {
	if def == nil {
		return nil
	}
	var warns []string
	for _, n := range def.Nodes {
		if n.Kind() != model.KindSubProcess {
			continue
		}
		sp, ok := n.(activity.SubProcess)
		if !ok || sp.Subprocess == nil {
			continue
		}
		var manual, eventTriggered int
		for _, s := range sp.Subprocess.StartNodes() {
			se, ok := s.(event.StartEvent)
			if !ok {
				continue
			}
			if se.SignalName != "" || se.MessageName != "" || !se.Timer.IsZero() {
				eventTriggered++
			} else {
				manual++
			}
		}
		if manual > 0 && eventTriggered > 0 {
			warns = append(warns, fmt.Sprintf(
				"workflow-runtime: subprocess %q in definition %q has both a manual and an event-triggered start; it acts as an event sub-process and the manual-start branch is unreachable",
				n.ID(), def.ID))
		}
	}
	return warns
}

// warnMixedSubprocessStart logs each mixedSubprocessStartWarnings entry for def
// at WARN level via the process-wide slog default logger.
func warnMixedSubprocessStart(def *model.ProcessDefinition) {
	for _, w := range mixedSubprocessStartWarnings(def) {
		slog.Default().Warn(w)
	}
}

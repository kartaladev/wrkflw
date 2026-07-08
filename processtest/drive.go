package processtest

import (
	"context"
	"errors"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// defaultDriveLimit bounds how many drive steps [DriveToCompletion] takes before
// giving up with [ErrDriveLimitExceeded]. It guards against non-terminating
// definitions and handlers that never make progress.
const defaultDriveLimit = 1000

// Drive-loop sentinel errors.
var (
	// ErrUnhandledPark is returned when the handler returns [Pass] for a park that
	// is not terminal — the instance is stuck because nothing resolved it.
	ErrUnhandledPark = errors.New("workflow-processtest: unhandled park")
	// ErrDriveLimitExceeded is returned when the drive step limit is reached before
	// a terminal status.
	ErrDriveLimitExceeded = errors.New("workflow-processtest: drive step limit exceeded")
	// ErrNoPendingTimer is returned by an [AdvanceTimers] decision when there is no
	// pending timer to advance to.
	ErrNoPendingTimer = errors.New("workflow-processtest: no pending timer to advance to")
	// ErrAdvanceTimersUnsupported is returned when an [AdvanceTimers] decision is
	// used with the free-function [DriveToCompletion], which owns no scheduler. Use
	// a [Harness] (which owns the clock and scheduler) to auto-advance timers.
	ErrAdvanceTimersUnsupported = errors.New("workflow-processtest: AdvanceTimers requires a Harness (no scheduler in the free-function drive)")
	// ErrNilHandler is returned when a nil [ParkHandler] is supplied.
	ErrNilHandler = errors.New("workflow-processtest: nil park handler")
)

// ParkHandler decides how to resolve a parked instance. It receives the classified
// [Park] and returns a [Decision] (and an optional error that aborts the drive).
type ParkHandler func(ctx context.Context, p Park) (Decision, error)

// decisionKind is the discriminant of a [Decision].
type decisionKind int

const (
	kindPass decisionKind = iota // zero value: "not handled — try next / stuck"
	kindDeliver
	kindAdvanceTimers
	kindStop
	kindAbort
)

// Decision is a park handler's instruction to the drive loop. Build one with
// [Deliver], [AdvanceTimers], [Stop], [Abort], or [Pass]. The zero value is
// [Pass].
type Decision struct {
	kind    decisionKind
	trigger engine.Trigger
	err     error
}

// Deliver feeds trigger to the driver's Deliver for the current instance.
func Deliver(trigger engine.Trigger) Decision {
	return Decision{kind: kindDeliver, trigger: trigger}
}

// AdvanceTimers advances the harness clock to the next due timer and ticks the
// scheduler, firing it. Supported only under a [Harness]; the free-function
// [DriveToCompletion] returns [ErrAdvanceTimersUnsupported].
func AdvanceTimers() Decision {
	return Decision{kind: kindAdvanceTimers}
}

// Stop halts the drive and returns the current (possibly non-terminal) state with
// a nil error.
func Stop() Decision {
	return Decision{kind: kindStop}
}

// Abort halts the drive and returns err.
func Abort(err error) Decision {
	return Decision{kind: kindAbort, err: err}
}

// Pass signals that the handler did not resolve this park. It is the zero
// [Decision]; combinators like [Chain] use it to defer to the next handler. If a
// top-level handler returns Pass for a non-terminal park, the drive fails with
// [ErrUnhandledPark].
func Pass() Decision {
	return Decision{kind: kindPass}
}

// driveEnv abstracts how the loop advances and classifies an instance: delivering
// a trigger, (only under a Harness) advancing timers, and classifying a park. The
// classify hook lets the Harness enrich the pure [Classify] result with scheduler
// knowledge (intermediate timer catches are visible only via the scheduler, not
// via instance state).
type driveEnv interface {
	deliver(ctx context.Context, trg engine.Trigger) (engine.InstanceState, error)
	advanceTimers(ctx context.Context) (engine.InstanceState, error)
	classify(state engine.InstanceState) Park
	limit() int
}

// freeEnv drives a consumer-built driver. It cannot advance timers.
type freeEnv struct {
	driver *runtime.ProcessDriver
	def    *model.ProcessDefinition
	id     string
	lim    int
}

func (e freeEnv) deliver(ctx context.Context, trg engine.Trigger) (engine.InstanceState, error) {
	return e.driver.ApplyTrigger(ctx, e.def, e.id, trg)
}

func (e freeEnv) advanceTimers(context.Context) (engine.InstanceState, error) {
	return engine.InstanceState{}, ErrAdvanceTimersUnsupported
}

func (e freeEnv) classify(state engine.InstanceState) Park { return Classify(state) }

func (e freeEnv) limit() int { return e.lim }

// DriveToCompletion advances an already-started instance against a consumer-built
// [runtime.ProcessDriver] until it reaches a terminal status, the handler returns
// [Stop]/[Abort], or the step limit is hit. state is the instance snapshot the
// consumer's Run returned; classification and the instance id are taken from it.
//
// [AdvanceTimers] is not supported here (this path owns no scheduler); use a
// [Harness] for timer-driven flows. Otherwise the handler resolves each park by
// returning a [Deliver] with the appropriate trigger.
func DriveToCompletion(
	ctx context.Context,
	driver *runtime.ProcessDriver,
	def *model.ProcessDefinition,
	state engine.InstanceState,
	handler ParkHandler,
) (engine.InstanceState, error) {
	env := freeEnv{driver: driver, def: def, id: state.InstanceID, lim: defaultDriveLimit}
	return drive(ctx, env, state, handler)
}

// drive is the shared loop used by both the free function and the Harness method.
func drive(ctx context.Context, env driveEnv, state engine.InstanceState, handler ParkHandler) (engine.InstanceState, error) {
	if handler == nil {
		return state, ErrNilHandler
	}

	limit := env.limit()
	for step := 0; step < limit; step++ {
		if IsTerminal(state.Status) {
			return state, nil
		}

		park := env.classify(state)
		d, err := handler(ctx, park)
		if err != nil {
			return state, err
		}

		switch d.kind {
		case kindPass:
			return state, fmt.Errorf("%w: %s at node %q", ErrUnhandledPark, park.Reason, park.Node)
		case kindStop:
			return state, nil
		case kindAbort:
			return state, d.err
		case kindDeliver:
			state, err = env.deliver(ctx, d.trigger)
			if err != nil {
				return state, err
			}
		case kindAdvanceTimers:
			state, err = env.advanceTimers(ctx)
			if err != nil {
				return state, err
			}
		}

		// A productive step may have reached a terminal status; return immediately
		// so a flow that completes on the final allowed step is not misreported as
		// exceeding the limit.
		if IsTerminal(state.Status) {
			return state, nil
		}
	}

	return state, fmt.Errorf("%w after %d steps (last park: %s)", ErrDriveLimitExceeded, limit, env.classify(state).Reason)
}

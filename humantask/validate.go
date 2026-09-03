package humantask

import (
	"errors"
	"fmt"
)

// ErrInvalidTask reports a [HumanTask] whose fields contradict each other. Every
// [TaskStore] implementation in this module returns it (wrapped, naming the task
// and the contradiction) from Upsert, and the runtime refuses to commit a step
// that would project such a task. Match it with errors.Is.
var ErrInvalidTask = errors.New("workflow-humantask: invalid task")

// Validate reports whether t is internally consistent, returning an error
// wrapping [ErrInvalidTask] when it is not.
//
// It enforces the claim invariant documented on [HumanTask.Claim] — a claim is
// present exactly when an actor holds the task:
//
//   - a Claimed task must carry a Claim;
//   - an Unclaimed task must not carry a Claim;
//   - State must be one of the four declared constants.
//
// A Claim whose Actor.ID is empty is deliberately accepted: it is the kiosk
// claimant, anonymous but carrying roles.
//
// Completed and Cancelled are deliberately unconstrained on the claim axis: a task
// cancelled while held keeps its claim as audit, and an immediate manual task
// completes without one. The completion axis (Completed implies a Completion) is
// NOT enforced.
//
// Note that Unclaimed is the zero value of [TaskState], so the Unclaimed rule also
// rejects a task carrying a Claim whose State was never set — including a decode
// that dropped only State. That is deliberate: such a record is exactly as
// contradictory as an explicitly Unclaimed one.
//
// Validate is a TaskStore-WRITE contract, not a whole-model invariant: the engine
// deliberately holds a Completed task with neither claim nor completion in instance
// state for an immediate manual task, and never writes it to a store.
func Validate(t HumanTask) error {
	// R3 first, as an ENUMERATED switch rather than a range check: a range check is
	// exact today but silently coupled to iota contiguity, so a fifth constant would
	// change its coverage with no test failing. TaskState.String() enumerates too.
	switch t.State {
	case Unclaimed, Claimed, Completed, Cancelled:
	default:
		return fmt.Errorf("%w: task %q: unknown state %d", ErrInvalidTask, t.TaskID, int(t.State))
	}
	switch {
	case t.State == Claimed && t.Claim == nil:
		return fmt.Errorf("%w: task %q: state %s requires a claim", ErrInvalidTask, t.TaskID, t.State)
	case t.State == Unclaimed && t.Claim != nil:
		return fmt.Errorf("%w: task %q: state %s must not carry a claim", ErrInvalidTask, t.TaskID, t.State)
	}
	return nil
}

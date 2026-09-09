package engine

import (
	"fmt"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/humantask"
)

// PendingCommandKind discriminates the variant carried by a [PendingCommand].
//
// It is a STRING, and the strings are stable wire values: a pending mark
// round-trips through the persisted instance snapshot, so an existing value can
// never be renamed or reused without reinterpreting stored marks. Appending a
// new kind is safe; changing one is not.
type PendingCommandKind string

// The pending-command kinds. Exactly the [Command] variants whose effect the
// runtime performs AFTER the step's commit, and which therefore have a window
// in which the step is durable and the effect is not.
//
// Deliberately absent, each for a reason that is a property of the command and
// not an oversight:
//
//   - ScheduleTimer, CancelTimer — persisted INSIDE the commit transaction and
//     re-armed on boot by RehydrateTimers. They never reach the runtime's
//     post-commit perform dispatch at all.
//   - CompleteInstance, FailInstance, SendMessage — their durable effect is the
//     outbox row written inside the same commit; post-commit perform is a no-op
//     for all three.
//   - InvokeCancelAction — best-effort by contract: it runs for its side effect
//     on an already-terminal instance, its result is never fed back, and a
//     failure must never fail the cancel. Recovering it would re-run a
//     cancellation side effect against an instance no sweep should be walking.
//   - Compensate — reserved, never emitted.
const (
	PendingInvokeAction PendingCommandKind = "invoke_action"
	PendingAwaitHuman   PendingCommandKind = "await_human"
	PendingUpdateTask   PendingCommandKind = "update_task"
	// PendingThrowSignal is recoverable, and it is the one kind whose duplicate
	// reaches instances OTHER than the one being recovered.
	//
	// Signal delivery is fan-out, so a re-driven throw re-publishes to the whole
	// bus: the "an already-resumed token answers ErrTokenNotFound" argument that
	// bounds a duplicated InvokeAction covers follow-up triggers on the recovered
	// instance and says nothing about fan-out. A duplicate can therefore re-trigger
	// a signal boundary event or a signal-started event sub-process in an unrelated
	// instance that armed AFTER the first delivery. An instance that already
	// consumed its arm is unaffected — the engine removes the arm on delivery and a
	// second SignalReceived finds nothing to match — so the exposed window is
	// between the two deliveries, not the whole park.
	//
	// It is kept recoverable anyway, and the alternative was weighed: excluding it
	// would make a signal thrown inside the commit→perform window unrecoverable
	// and SILENT, stranding every instance waiting on it with nothing that will
	// ever wake them. That is the exact class this mechanism exists to close, and
	// it is a worse failure than a duplicate the engine largely absorbs. Deliver
	// signals whose handlers are idempotent, or accept the narrow re-trigger window.
	PendingThrowSignal      PendingCommandKind = "throw_signal"
	PendingStartSubInstance PendingCommandKind = "start_sub_instance"
)

// ErrUnknownPendingCommand is returned by [PendingCommand.Command] for a kind it
// does not recognise — a mark written by a newer build, or a corrupted snapshot.
var ErrUnknownPendingCommand = fmt.Errorf("workflow-engine: unknown pending command kind")

// PendingCommand is the durable, JSON-round-trippable envelope over one
// [Command] the runtime has committed but not yet performed.
//
// There is no durable representation of a Command anywhere else in the system:
// StepResult.Commands is a return value, and the outbox carries EVENTS derived
// from commands, not the commands themselves. This envelope exists so a step
// that committed and then lost its process can have its unperformed effects
// re-driven from the snapshot alone.
//
// It is a flat struct with one discriminator, mirroring the journal's trigger
// envelope: fields unused by a kind are omitted. Reconstruct with
// [PendingCommand.Command].
//
// ⚠ [InvokeAction.Scoped] is NOT carried. It is an action.Catalog — an
// interface, with no wire form — so a reconstructed InvokeAction resolves its
// action name against the top-level definition's scoped catalog plus the global
// one. That is exactly the documented fallback the engine's own timer- and
// cancel-driven secondary invocations already take (see [InvokeAction]), so a
// recovered invocation is no weaker than a compensation or reminder invocation;
// it does mean an action declared ONLY in a nested sub-process scope's catalog
// is unresolvable after a crash, and reports back as ActionFailed rather than
// silently vanishing.
type PendingCommand struct {
	// Kind selects which of the fields below carry meaning.
	Kind PendingCommandKind `json:"kind"`

	// CommandID correlates the reply that resumes the parked token.
	// InvokeAction, StartSubInstance.
	CommandID string `json:"command_id,omitempty"`
	// Name is the action name (InvokeAction) or the signal name (ThrowSignal).
	Name string `json:"name,omitempty"`
	// Input is the action input (InvokeAction) or the child's seed variables
	// (StartSubInstance).
	Input map[string]any `json:"input,omitempty"`
	// Payload is the signal payload (ThrowSignal).
	Payload map[string]any `json:"payload,omitempty"`
	// FireAndForget marks an InvokeAction no token awaits.
	FireAndForget bool `json:"fire_and_forget,omitempty"`
	// TaskID names the human task to project (AwaitHuman).
	TaskID string `json:"task_id,omitempty"`
	// Eligibility describes who may act on the task (AwaitHuman).
	Eligibility authz.AuthzSpec `json:"eligibility,omitzero"`
	// Task is the record to persist (UpdateTask). A pointer so an absent task is
	// distinguishable from a zero one.
	Task *humantask.HumanTask `json:"task,omitempty"`
	// DefRef references the child definition to start (StartSubInstance).
	DefRef model.Qualifier `json:"def_ref,omitzero"`
}

// newPendingCommand builds the durable envelope for c.
//
// The second return value reports whether c is recoverable at all: false for
// every command listed as deliberately absent on [PendingCommandKind]. A false
// return is a routine classification, not an error.
//
// Unexported on purpose: [PendingCommandsFor] is the only shape a caller needs,
// and this type's field set is a wire contract, so every exported entry point
// into it is one more thing that can never be narrowed.
func newPendingCommand(c Command) (PendingCommand, bool) {
	switch cmd := c.(type) {
	case InvokeAction:
		return PendingCommand{
			Kind:          PendingInvokeAction,
			CommandID:     cmd.CommandID,
			Name:          cmd.Name,
			Input:         copyVars(cmd.Input),
			FireAndForget: cmd.FireAndForget,
		}, true
	case AwaitHuman:
		return PendingCommand{
			Kind:        PendingAwaitHuman,
			TaskID:      cmd.TaskID,
			Eligibility: cmd.Eligibility.Clone(),
		}, true
	case UpdateTask:
		task := cmd.Task.Clone()
		return PendingCommand{Kind: PendingUpdateTask, Task: &task}, true
	case ThrowSignal:
		return PendingCommand{
			Kind:    PendingThrowSignal,
			Name:    cmd.Name,
			Payload: copyVars(cmd.Payload),
		}, true
	case StartSubInstance:
		return PendingCommand{
			Kind:      PendingStartSubInstance,
			CommandID: cmd.CommandID,
			DefRef:    cmd.DefRef,
			Input:     copyVars(cmd.Input),
		}, true
	default:
		return PendingCommand{}, false
	}
}

// PendingCommandsFor returns the durable envelopes for every recoverable command
// in cmds, in order. It returns nil — not an empty slice — when nothing in cmds
// is recoverable, so an unmarked snapshot serialises identically to one written
// before this field existed.
func PendingCommandsFor(cmds []Command) []PendingCommand {
	var out []PendingCommand
	for _, c := range cmds {
		if p, ok := newPendingCommand(c); ok {
			out = append(out, p)
		}
	}
	return out
}

// Command reconstructs the sealed [Command] p envelopes.
//
// It returns [ErrUnknownPendingCommand] for an unrecognised kind rather than a
// zero command: a snapshot written by a newer build must fail loudly at the one
// call site that reads it, not be silently re-driven as something else.
func (p PendingCommand) Command() (Command, error) {
	switch p.Kind {
	case PendingInvokeAction:
		return InvokeAction{
			CommandID:     p.CommandID,
			Name:          p.Name,
			Input:         copyVars(p.Input),
			FireAndForget: p.FireAndForget,
		}, nil
	case PendingAwaitHuman:
		return AwaitHuman{TaskID: p.TaskID, Eligibility: p.Eligibility.Clone()}, nil
	case PendingUpdateTask:
		if p.Task == nil {
			return nil, fmt.Errorf("workflow-engine: pending %s carries no task", p.Kind)
		}
		return UpdateTask{Task: p.Task.Clone()}, nil
	case PendingThrowSignal:
		return ThrowSignal{Name: p.Name, Payload: copyVars(p.Payload)}, nil
	case PendingStartSubInstance:
		return StartSubInstance{
			CommandID: p.CommandID,
			DefRef:    p.DefRef,
			Input:     copyVars(p.Input),
		}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownPendingCommand, p.Kind)
	}
}

// clone independently allocates every reference-typed field ONE LEVEL DEEP: the
// Input and Payload maps, the eligibility spec's two string slices, and the
// task pointee.
//
// ⚠ One level, not "shares no mutable state" — an earlier version of this
// comment said the latter and it was measured false. copyVars is maps.Clone, so
// a map or slice stored INSIDE an Input value is still shared with the source.
// That is the repo-wide convention for process variables (see copyVars), not a
// gap here, and JSON serialisation breaks the alias on the durable path — but
// the sentence has to say what is true, because the difference is exactly what a
// reader would rely on before mutating a nested value.
func (p PendingCommand) clone() PendingCommand {
	c := p
	c.Input = copyVars(p.Input)
	c.Payload = copyVars(p.Payload)
	if p.Task != nil {
		t := p.Task.Clone()
		c.Task = &t
	}
	c.Eligibility = p.Eligibility.Clone()
	return c
}

// clonePendingCommands copies a pending-command slice, cloning each envelope to
// the depth [PendingCommand.clone] documents — one level — and preserving the
// nil-vs-empty distinction (nil in, nil out).
func clonePendingCommands(in []PendingCommand) []PendingCommand {
	if in == nil {
		return nil
	}
	out := make([]PendingCommand, len(in))
	for i, p := range in {
		out[i] = p.clone()
	}
	return out
}

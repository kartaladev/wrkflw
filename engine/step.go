package engine

import (
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// StepMode selects how far one Step advances.
// Macro (default) runs drive until all active tokens are parked or consumed.
// Micro runs drive until the first token park or terminal event, then stops,
// leaving any remaining active tokens for subsequent Step calls.
type StepMode int

const (
	Macro StepMode = iota
	Micro
)

// StepOptions controls optional behaviour of a [Step] call.
type StepOptions struct {
	// Mode selects the step granularity: [Macro] (default) or [Micro].
	Mode StepMode
	// DefaultRetryPolicy is the fallback retry policy applied when a node does
	// not carry its own RetryPolicy. nil means retry is disabled by default.
	DefaultRetryPolicy *model.RetryPolicy
	// OverrideRetryPolicy, when non-nil, takes precedence over both the node's
	// own RetryPolicy and DefaultRetryPolicy for this Step. It is the seam the
	// runtime uses to surface a per-action retry policy (action > node >
	// runtime-default). nil (the default) leaves the node > default chain intact.
	OverrideRetryPolicy *model.RetryPolicy
	// Evaluator overrides the expression evaluator the engine uses for gateway
	// conditions, timer/deadline durations, and correlation keys. When nil (the
	// default) the engine uses its pure, wall-clock-free package-global
	// evaluator, keeping Step deterministic for replay.
	//
	// A consumer that evaluates UNTRUSTED definitions can supply a
	// timeout-capable evaluator (e.g. expreval.New(expreval.WithTimeout(d)),
	// which satisfies ConditionEvaluator) to bound evaluation latency and guard
	// against expression-DoS. Doing so trades the deterministic-replay guarantee
	// for that protection (ADR-0049, ADR-0056) — an explicit, opt-in choice.
	Evaluator ConditionEvaluator
}

// StepResult is the output of a single [Step] call. Commands is the ordered
// list of side effects the runtime must perform. On a no-op step (e.g. a stale
// TimerFired with no matching token) Commands may be nil; callers should use
// len(Commands) rather than Commands != nil to check for work to do.
type StepResult struct {
	State    InstanceState
	Commands []Command
}

// Step applies one trigger to the instance state and returns the new state plus
// the commands the runtime must perform. It is pure: it does not mutate st.
//
// The engine assumes the definition has passed [model.Validate]; in particular,
// an exclusive gateway is assumed to have at most one unconditional non-default
// outgoing flow — the engine takes the first matching flow in definition order
// and does not detect ambiguous multi-unconditional configurations.
func Step(def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error) {
	s := cloneState(st)
	sp := &s

	switch t := trg.(type) {
	case StartInstance:
		return handleStartInstance(def, sp, t, opt)
	case ActionCompleted:
		return handleActionCompleted(def, sp, t, opt)
	case CancelRequested:
		return handleCancelRequested(def, sp, t, opt)
	case CompensateRequested:
		return handleCompensateRequested(def, sp, t, opt)
	case ActionFailed:
		return handleActionFailed(def, sp, t, opt)
	case HumanClaimed:
		return handleHumanClaimed(sp, t)
	case HumanReassigned:
		return handleHumanReassigned(sp, t)
	case TimerFired:
		return handleTimerFired(def, sp, t, opt)
	case HumanCompleted:
		return handleHumanCompleted(def, sp, t, opt)
	case SignalReceived:
		return handleSignalReceived(def, sp, t, opt)
	case SubInstanceCompleted:
		return handleSubInstanceCompleted(def, sp, t, opt)
	case SubInstanceFailed:
		return handleSubInstanceFailed(sp, t)
	case MessageReceived:
		return handleMessageReceived(def, sp, t, opt)
	case ResolveIncident:
		return handleResolveIncident(def, sp, t, opt)
	default:
		return StepResult{}, fmt.Errorf("%w: %T", ErrUnknownTrigger, trg)
	}
}

// drive advances active tokens until each is parked or consumed.
//
// In Macro mode (default) drive loops until no active tokens remain.
// In Micro mode drive stops after the first token park or terminal event,
// leaving any remaining active tokens for subsequent Step(Micro) calls.
// Auto-advancing nodes (StartEvent, gateway routing that produces new active
// tokens) do not count as stops in Micro mode; execution passes through them
// within the same drive call until a park/terminal is reached.
//
// def is the TOP-LEVEL process definition. For each token, the effective
// definition (tdef) is resolved via defForScope against the token's ScopeID so
// that tokens inside a sub-process scope resolve nodes/flows against the nested
// definition rather than the top-level one.
func drive(def *model.ProcessDefinition, s *InstanceState, at time.Time, mode StepMode, eval ConditionEvaluator) ([]Command, error) {
	var cmds []Command
	for {
		tok := s.firstActive()
		if tok == nil {
			break
		}

		// Resolve the effective definition for this token's scope.
		tdef, err := defForScope(def, s, tok.ScopeID)
		if err != nil {
			return cmds, err
		}

		node, ok := tdef.Node(tok.NodeID)
		if !ok {
			// Defensive: a token on a missing node cannot advance.
			tok.State = TokenWaitingCommand
			continue
		}

		// stopped is set to true by any case that parks or terminally consumes
		// this token (ServiceTask, UserTask, EndEvent, etc.). In Micro mode the
		// loop breaks as soon as stopped is true, leaving remaining active tokens
		// for the next Step call. Auto-advancing cases (StartEvent, gateway routing
		// that produces new active tokens) leave stopped false so the loop continues.
		stopped := false

		// Dispatch through the nodeStrategy registry for migrated kinds.
		// Kinds not yet in the registry fall through to the switch below.
		if strat, ok := nodeStrategies[node.Kind()]; ok {
			c := &stepCtx{def: def, tdef: tdef, s: s, at: at, mode: mode, eval: eval}
			produced, halt, stratErr := strat.enter(c, tok, node)
			if stratErr != nil {
				return nil, stratErr
			}
			cmds = append(cmds, produced...)
			if halt {
				// ErrorEndEvent: reproduce the original arm's `return cmds, nil` —
				// exit drive() entirely (the instance is terminal or propagateError
				// already drained/routed all tokens), not just this token.
				return cmds, nil
			}
			// Preserve Micro-mode semantics: a strategy that parks the token
			// (tok.State != TokenActive) counts as a stop, identical to the
			// old `stopped = true` in the case arm.
			stopped = tok.State != TokenActive
		} else {
			// Unhandled node kinds: park the token so the loop terminates rather
			// than spinning. These are intentionally not in the registry:
			// KindBoundaryEvent, KindUnspecified.
			tok.State = TokenWaitingCommand
			stopped = true // token parked: Micro stops here
		} // end else (non-registry kinds)

		// Micro-mode: stop after the first park or terminal event. Auto-advancing
		// cases (StartEvent, gateway routing that produces new active tokens) leave
		// stopped=false so the loop continues to the next token within this Step call.
		if mode == Micro && stopped {
			break
		}
	}
	return cmds, nil
}

package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
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
	// for that protection — an explicit, opt-in choice.
	Evaluator ConditionEvaluator
	// IDGenerator mints the ids the engine stamps on tokens, tasks, commands, timers,
	// incidents, and scopes. nil (the default) keeps the deterministic
	// "<instance id>-<prefix><n>" counter, so pure-engine tests and durable
	// replay stay byte-for-byte reproducible; the runtime injects a real
	// generator (xid) so product instances carry opaque ids. See [IDGenerator].
	IDGenerator IDGenerator
	// CompensationStallAfter bounds how long a dispatched compensation action may
	// go without reporting back before the engine raises a stall incident against
	// the walk. ZERO DISABLES detection, and zero is the default: with it unset
	// no stall timer is scheduled and no command stream changes shape.
	//
	// A compensation walk advances only on a trigger carrying the cursor's
	// command id, and holds no tokens and no other timers — so without this
	// window a walk whose action never replies is permanently stuck AND
	// permanently invisible.
	//
	// ⚠ One engine-wide window is a deliberate v1 simplification, not a
	// precedent-backed choice: a ledger reversal returns in milliseconds and a
	// manual-approval-gated refund takes hours, so a single value forces the
	// operator to size for the slowest. Compare DefaultRetryPolicy, which is a
	// three-tier chain precisely because one timeout for every action is wrong.
	// A per-node tier is deliberate backlog, not scope.
	CompensationStallAfter time.Duration
	// CompensationRetryPolicy makes a compensation action that replies
	// ActionFailed be RE-DISPATCHED after a backoff instead of skipped. nil
	// DISABLES retry, and nil is the default: with it unset the command stream
	// keeps the skip-and-advance timing exactly, and only the always-on WARN +
	// IncidentCompensationFailed are new.
	//
	// The budget is PER RECORD (compensationCursor.RetryAttempts, zeroed whenever
	// the walk advances), so a walk draining ten records gives each of them
	// MaxAttempts, not the walk as a whole.
	//
	// ⚠ MaxElapsed is NOT honoured on this path. The walk holds no token, so
	// there is no RetryStartedAt to measure elapsed time against — the token
	// path's own MaxElapsed term reads tok.RetryStartedAt. MaxAttempts and
	// NonRetryableErrors are honoured, and so is ActionFailed.Retryable.
	//
	// ⚠ On exhaustion the walk SKIPS AND CONTINUES; it never parks. Parking would
	// reverse the safety argument that a failed compensation never strands the
	// instance. The incident is the durable record that it happened.
	//
	// ⚠ One engine-wide policy is a deliberate v1 simplification, the same
	// trade-off CompensationStallAfter documents. A per-node tier is backlog.
	CompensationRetryPolicy *model.RetryPolicy
}

// stepPolicy bundles the per-Step policy values that the drive,
// error-propagation and compensation call chains all thread together. It is
// resolved ONCE per Step (resolvePolicy) and passed by value, so every strategy
// and handler in one Step reads the same evaluator and the same
// granularity — the property the previously hand-threaded (mode, eval) pair
// carried by convention across fourteen signatures.
type stepPolicy struct {
	// mode is the step granularity: [Macro] (default) or [Micro].
	mode StepMode
	// eval is the resolved expression evaluator: the one injected via
	// StepOptions.Evaluator, or the pure package-global default.
	eval ConditionEvaluator
	// stallAfter is StepOptions.CompensationStallAfter. Zero disables stall
	// detection; it reaches armCompensationStallTimer's call sites through this
	// field.
	stallAfter time.Duration
	// compensationRetry is StepOptions.CompensationRetryPolicy. nil disables
	// compensation retry; it reaches handleActionFailed's compensation
	// short-circuit and retryFailedCompensation through this field.
	// Carried un-normalized, exactly as CompensationStallAfter is carried raw —
	// the decision site normalizes once, mirroring effectiveRetryPolicy.
	compensationRetry *model.RetryPolicy
}

// resolvePolicy reduces a caller's StepOptions to the resolved stepPolicy the
// internal call chains thread. Called once per dispatch.
func resolvePolicy(opt StepOptions) stepPolicy {
	return stepPolicy{
		mode:              opt.Mode,
		eval:              resolveEvaluator(opt),
		stallAfter:        opt.CompensationStallAfter,
		compensationRetry: opt.CompensationRetryPolicy,
	}
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
// ctx is used ONLY for trace-correlated, context-aware logging (slog.*Context
// calls at the engine's deliberate silent no-op sites) — it carries
// no cancellation semantics and is never inspected for control flow. Passing a
// context that is already Done, or a nil-adjacent context.TODO(), does not
// change the (state, commands) result: Step remains deterministic and safe to
// replay for identical (def, st, trg, opt) regardless of ctx.
//
// The engine assumes the definition has passed [model.Validate]; in particular,
// an exclusive gateway is assumed to have at most one unconditional non-default
// outgoing flow — the engine takes the first matching flow in definition order
// and does not detect ambiguous multi-unconditional configurations.
//
// Every route a definition can take to reach here enforces that assumption: the
// builder and the YAML loader both end in [model.Validate], and
// [github.com/kartaladev/wrkflw/runtime/kernel.MemDefinitionRegistry.Register] —
// the door every hand-constructed *model.ProcessDefinition literal goes through,
// directly or via [github.com/kartaladev/wrkflw/runtime.RegisterDefinition] —
// runs it too. The one exception is
// [github.com/kartaladev/wrkflw/runtime/kernel.NewMapDefinitionRegistry], whose
// variadic constructor returns no error and therefore cannot reject: a caller
// assembling one owns validation itself, as does anyone calling Step with a
// literal of their own.
func Step(ctx context.Context, def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error) {
	// Reject a malformed trigger before any work: an empty identity key names no
	// record, so there is nothing to dispatch it to. Running before
	// cloneState keeps a rejected trigger free of side effects.
	if err := validateTriggerKey(trg); err != nil {
		return StepResult{}, err
	}
	// A reassignment naming nobody would mint a Claimed task no inbox can see.
	// Rejected here, beside the identity-key check and ahead of
	// cloneState, so a malformed trigger has no side effects at all.
	if r, ok := trg.(HumanReassigned); ok && r.To == "" {
		return StepResult{}, fmt.Errorf("%w: %T.To", ErrEmptyReassignTarget, trg)
	}
	s := cloneState(st)
	sp := &s
	// Install the id-generation seam on the working clone for the duration of
	// this step. The mint sites live in helpers that cannot return an
	// error, so a generator failure is recorded on the state and converted into
	// this call's error below — the partially-built state is never returned.
	sp.ids = idSource{gen: opt.IDGenerator}

	res, err := dispatch(ctx, def, sp, trg, opt)
	if err != nil {
		return StepResult{}, err
	}
	if idErr := sp.ids.err; idErr != nil {
		return StepResult{}, idErr
	}
	// Drop the commands whose awaiter this step destroyed. drive
	// accumulates every token's commands in one pass, so a later token can cancel
	// an earlier one's park — forceTerminate nils s.Tokens, handleUnhandledError
	// fails the instance while LEAVING siblings parked, propagateError tears down
	// a scope, beginCompensation cancels everything, and an interrupting arm
	// consumes its host (that last one only via the signal broadcast path, which
	// fires several arms in one step; dispatchArmCascade is first-match-wins for
	// timer and message, so those can never destroy an earlier command). Runs on &res.State, not sp: each handler
	// returns its own StepResult{State: *s} shallow copy, so mutating the clone
	// would depend on backing-array sharing. Runs AFTER the id-error check above,
	// because a failed generator mints empty ids that must surface as this call's
	// error rather than reach the filter.
	res.Commands = dropStaleTokenCommands(ctx, &res.State, res.Commands)
	// Scrub the transient seam so it never escapes into a caller's state.
	res.State.ids = idSource{}
	return res, nil
}

// dispatch routes a trigger to its handler. It is Step's body, split out so
// Step can wrap every handler return with the id-generation seam's setup and
// teardown in one place.
func dispatch(ctx context.Context, def *model.ProcessDefinition, sp *InstanceState, trg Trigger, opt StepOptions) (StepResult, error) {
	// One structural guard replacing the per-handler copies. It lives
	// here rather than in Step so Step's validateTriggerKey and cloneState keep
	// running first: a malformed trigger is a shape defect and must
	// lose to this state-dependent check, and a rejected trigger must still be
	// judged against a private clone rather than the caller's state.
	//
	// StatusCompensating is NOT terminal, so in-flight compensation walks are
	// unaffected.
	if sp.Status.IsTerminal() {
		policy := trg.terminalPolicy()
		// BOTH refusal flavours log, under ONE constant message with the flavour
		// as a structured attribute. Erroring is not a substitute for the record:
		// handleResolveIncident's own guard emitted a Warn for a rejected admin
		// action, and a caller's 422 is not visible to the operator reading logs,
		// so an error-only arm would be the single replaced site that REGRESSES
		// rather than relocates.
		//
		// instance_id/trigger/status/outcome on every line, plus the trigger's own
		// identity field when it has one. Collapsing eight per-handler guards into
		// one must not cost the operator that field: "why did my timer do nothing"
		// is answered by timer_id, not by the trigger's type name.
		// triggerIdentityAttr reads the same registry validateTriggerKey uses, so
		// there is no second mapping — and no per-trigger message map — to drift.
		if policy == rejectSilently || policy == rejectWithError {
			outcome := "dropped"
			if policy == rejectWithError {
				outcome = "errored"
			}
			attrs := []any{
				"instance_id", sp.InstanceID,
				"trigger", triggerTypeName(trg),
				"status", sp.Status.String(),
				"outcome", outcome,
			}
			if identity, ok := triggerIdentityAttr(trg); ok {
				attrs = append(attrs, identity)
			}
			slog.WarnContext(ctx, "trigger rejected on terminal instance", attrs...)
		}

		switch policy {
		case rejectSilently:
			return StepResult{State: *sp, Commands: nil}, nil
		case rejectWithError:
			return StepResult{}, fmt.Errorf("%w (status %v)", ErrInstanceTerminal, sp.Status)
		case allowOnTerminal:
			// Fall through to the handler: the trigger deliberately operates on a
			// terminal instance (a plain full rollback, the deliberate carve-out).
		}
	}

	switch t := trg.(type) {
	case StartInstance:
		return handleStartInstance(ctx, def, sp, t, opt)
	case ActionCompleted:
		return handleActionCompleted(ctx, def, sp, t, opt)
	case CancelRequested:
		return handleCancelRequested(ctx, def, sp, t, opt)
	case CompensateRequested:
		return handleCompensateRequested(ctx, def, sp, t, opt)
	case ActionFailed:
		return handleActionFailed(ctx, def, sp, t, opt)
	case HumanCandidatesResolved:
		return handleHumanCandidatesResolved(sp, t)
	case HumanClaimed:
		return handleHumanClaimed(sp, t)
	case HumanReassigned:
		return handleHumanReassigned(sp, t)
	case TimerFired:
		return handleTimerFired(ctx, def, sp, t, opt)
	case HumanCompleted:
		return handleHumanCompleted(ctx, def, sp, t, opt)
	case SignalReceived:
		return handleSignalReceived(ctx, def, sp, t, opt)
	case SubInstanceCompleted:
		return handleSubInstanceCompleted(ctx, def, sp, t, opt)
	case SubInstanceFailed:
		return handleSubInstanceFailed(ctx, def, sp, t, opt)
	case MessageReceived:
		return handleMessageReceived(ctx, def, sp, t, opt)
	case ResolveIncident:
		return handleResolveIncident(ctx, def, sp, t, opt)
	case ResolveCompensationStall:
		return handleResolveCompensationStall(ctx, def, sp, t, resolvePolicy(opt))
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
func drive(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, at time.Time, pol stepPolicy) ([]Command, error) {
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
			slog.WarnContext(ctx, "token routed to a missing node",
				"instance_id", s.InstanceID,
				"token_id", tok.ID,
				"node_id", tok.NodeID,
			)
			tok.State = TokenWaiting
			continue
		}

		// stopped is set to true by any case that parks or terminally consumes
		// this token (ServiceTask, UserTask, EndEvent, etc.). In Micro mode the
		// loop breaks as soon as stopped is true, leaving remaining active tokens
		// for the next Step call. Auto-advancing cases (StartEvent, gateway routing
		// that produces new active tokens) leave stopped false so the loop continues.
		stopped := false

		// Dispatch node entry through the nodeStrategy registry. Kinds absent from
		// the registry fall through to the else branch below, which parks the token.
		if strat, ok := nodeStrategies[node.Kind()]; ok {
			c := &stepCtx{ctx: ctx, def: def, tdef: tdef, s: s, at: at, pol: pol}
			produced, halt, stratErr := strat.enter(c, tok, node)
			if stratErr != nil {
				return nil, stratErr
			}
			cmds = append(cmds, produced...)
			if halt {
				// Error-behavior end event (EndEvent with Behavior==EndError):
				// exit drive() entirely (the instance is terminal or
				// propagateError already drained/routed all tokens), not just this
				// token.
				return cmds, nil
			}
			// Micro-mode semantics: a strategy that parks the token
			// (tok.State != TokenActive) counts as a stop.
			stopped = tok.State != TokenActive
		} else {
			// Unhandled node kinds: park the token so the loop terminates rather
			// than spinning. These are intentionally not in the registry:
			// KindBoundaryEvent, KindUnspecified.
			//
			// This park owes a log and nothing more. Both kinds are structural
			// rather than authored — KindUnspecified is the NodeKind zero value,
			// and fireBoundaryArm places tokens on a boundary's flow target, never
			// on the boundary node itself — so a token arriving here is an engine
			// routing bug. Under the policy on raiseDefinitionDefect that is the
			// exclusion: an IncidentDefinitionDefect would send an operator to
			// correct a definition that is not wrong. The kind is logged because it
			// says which of the two happened.
			slog.WarnContext(ctx, "token parked on a node kind the engine does not route",
				"instance_id", s.InstanceID,
				"token_id", tok.ID,
				"node_id", tok.NodeID,
				"node_kind", node.Kind().String(),
			)
			tok.State = TokenWaiting
			stopped = true // token parked: Micro stops here
		} // end else (non-registry kinds)

		// Micro-mode: stop after the first park or terminal event. Auto-advancing
		// cases (StartEvent, gateway routing that produces new active tokens) leave
		// stopped=false so the loop continues to the next token within this Step call.
		if pol.mode == Micro && stopped {
			break
		}
	}
	return cmds, nil
}

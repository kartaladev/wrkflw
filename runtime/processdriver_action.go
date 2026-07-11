package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// terminalErr derives a short, human-readable error message from a terminal
// instance state. It prefers the first recorded incident's error text (the most
// concrete description of what went wrong); if no incidents are present it
// falls back to a status-keyed generic message.
func terminalErr(st engine.InstanceState) string {
	if len(st.Incidents) > 0 {
		return st.Incidents[0].Error
	}
	switch st.Status {
	case engine.StatusTerminated:
		return "instance terminated"
	default:
		return "instance failed"
	}
}

// copyVarsForOutcome returns a shallow copy of vars to avoid aliasing the
// live InstanceState.Variables map via the CallOutcome.Output reference.
// Returns nil when vars is nil (preserving the zero value).
func copyVarsForOutcome(vars map[string]any) map[string]any {
	if vars == nil {
		return nil
	}
	out := make(map[string]any, len(vars))
	for k, v := range vars {
		out[k] = v
	}
	return out
}

// actionContextFor derives the context an action runs under for an effective
// timeout d. When d is positive it applies a deadline; otherwise the parent
// context passes through unchanged. The caller must always invoke the returned
// cancel func. d is the per-action effective timeout ([action.Policy.Timeout] when
// the action declares one, else [ProcessDriver.actionTimeout]).
func actionContextFor(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, d)
}

// invokeActionDo invokes a.Do. When recoverPanics is true it converts a panic into
// an error so a buggy or malicious service action cannot crash the runner — and
// with it every in-flight instance on the replica; a recovered panic is surfaced as
// an ordinary action error so callers route it through their normal failure path.
// When recoverPanics is false (a consumer's explicit [action.WithRecover](false))
// the panic propagates unchanged.
func invokeActionDo(ctx context.Context, a action.Action, in map[string]any, recoverPanics bool) (out map[string]any, err error) {
	if !recoverPanics {
		return a.Do(ctx, in)
	}
	defer func() {
		if rec := recover(); rec != nil {
			out = nil
			err = fmt.Errorf("workflow-runtime: action panicked: %v", rec)
		}
	}()
	return a.Do(ctx, in)
}

// effectiveActionPolicy resolves the effective per-invocation execution policy for
// a resolved action: the bare (fully-unwrapped) action to run at the single site,
// the effective timeout (the action's declared [action.WithExecTimeout] else the
// runtime default), and the effective recover flag (the action's declared
// [action.WithRecover] else true).
func (driver *ProcessDriver) effectiveActionPolicy(a action.Action) (bare action.Action, timeout time.Duration, recoverPanics bool) {
	pol := action.ResolvePolicy(a)
	bare = action.Unwrap(a)
	timeout = driver.actionTimeout
	if pol.Timeout != nil {
		timeout = *pol.Timeout
	}
	recoverPanics = true
	if pol.Recover != nil {
		recoverPanics = *pol.Recover
	}
	return bare, timeout, recoverPanics
}

// actionRetryToModel converts a declarative [action.RetrySpecs] to the engine's
// [model.RetryPolicy] (which owns the retry algorithm). Only the four mirrored
// fields are carried; the model's Normalize fills the rest.
func actionRetryToModel(p action.RetrySpecs) model.RetryPolicy {
	return model.RetryPolicy{
		MaxAttempts:     p.MaxAttempts,
		InitialInterval: p.InitialInterval,
		BackoffCoef:     p.Multiplier,
		MaxInterval:     p.MaxInterval,
	}
}

// overrideRetryPolicy derives the per-action retry override for a trigger, if any.
// It returns non-nil only for an [engine.ActionFailed] whose failing node resolves
// to an action carrying an [action.RetrySpecs] — surfacing precedence action >
// node > runtime-default via [engine.StepOptions.OverrideRetryPolicy]. It is
// re-derived from durable state (def + st + CommandID) on every ActionFailed step,
// so a retry re-attempt after a restart resolves the same override. st is the
// pre-step state; other triggers and policy-less actions yield nil (today's behavior).
func (driver *ProcessDriver) overrideRetryPolicy(def *model.ProcessDefinition, st engine.InstanceState, trg engine.Trigger) *model.RetryPolicy {
	af, ok := trg.(engine.ActionFailed)
	if !ok {
		return nil
	}
	name, scopeDef, ok := engine.FailingActionName(def, st, af.CommandID)
	if !ok {
		return nil
	}
	a, ok := action.Resolve(scopeDef.ScopedCatalog(), driver.cat, name)
	if !ok {
		return nil
	}
	pol := action.ResolvePolicy(a)
	if pol.Retry == nil {
		return nil
	}
	mp := actionRetryToModel(*pol.Retry)
	return &mp
}

// perform executes one command and returns the resulting trigger, if any.
// st is the current instance state, used for variable access when resolving
// human-task candidates. def is the process definition, captured by timer
// fire callbacks that need to call ApplyTrigger.
//
//nolint:cyclop // the command switch is intentionally exhaustive; each case is simple.
func (driver *ProcessDriver) perform(ctx context.Context, def *model.ProcessDefinition, st engine.InstanceState, c engine.Command) (engine.Trigger, error) {
	switch cmd := c.(type) {
	case engine.InvokeAction:
		actx, aspan := driver.obs.tracer().Start(ctx, "wrkflw.action "+cmd.Name,
			trace.WithAttributes(attribute.String("wrkflw.action", cmd.Name)))
		outcome := "error"
		var elapsed float64
		defer func() {
			driver.obs.actionDuration.Record(actx, elapsed,
				metric.WithAttributes(attribute.String("action", cmd.Name), attribute.String("outcome", outcome)))
			aspan.End()
		}()

		a, ok := driver.resolveInvokeAction(def, cmd)
		if !ok {
			err := errors.New("unknown action: " + cmd.Name)
			aspan.RecordError(err)
			aspan.SetStatus(codes.Error, err.Error())
			driver.obs.actionFailures.Add(actx, 1, metric.WithAttributes(
				attribute.String("action", cmd.Name),
				attribute.Bool("retryable", false),
			))
			if cmd.FireAndForget {
				// No token awaits a fire-and-forget action's result, so an
				// ActionFailed would only surface as ErrTokenNotFound. Log and
				// drop instead — the action was never actionable anyway.
				driver.obs.tel.Logger.LogAttrs(actx, slog.LevelWarn, "runtime: fire-and-forget action not found",
					slog.String("action", cmd.Name))
				return nil, nil
			}
			return engine.NewActionFailed(driver.clk.Now(), cmd.CommandID, "unknown action: "+cmd.Name, false), nil
		}
		start := driver.clk.Now()
		bare, timeout, recoverPanics := driver.effectiveActionPolicy(a)
		tctx, cancel := actionContextFor(actx, timeout)
		out, err := invokeActionDo(tctx, bare, cmd.Input, recoverPanics)
		cancel()
		elapsed = driver.clk.Now().Sub(start).Seconds()
		if err != nil {
			aspan.RecordError(err)
			aspan.SetStatus(codes.Error, err.Error())
			driver.obs.actionFailures.Add(actx, 1, metric.WithAttributes(
				attribute.String("action", cmd.Name),
				attribute.Bool("retryable", action.IsRetryable(err)),
			))
			if cmd.FireAndForget {
				// Deadline-breach and reminder actions run for their side effect
				// only; no token awaits the result. Log the failure rather than
				// feeding back an ActionFailed that no token could ever match.
				driver.obs.tel.Logger.LogAttrs(actx, slog.LevelWarn, "runtime: fire-and-forget action failed",
					slog.String("action", cmd.Name), slog.Any("error", err))
				return nil, nil
			}
			return engine.NewActionFailed(driver.clk.Now(), cmd.CommandID, err.Error(), action.IsRetryable(err), engine.WithJitter(driver.jitter.Fraction()), engine.WithCause(err)), nil
		}
		outcome = "ok"
		if cmd.FireAndForget {
			// Side effect performed and observed (span + duration metric). No
			// token awaits the result, so return no trigger.
			return nil, nil
		}
		return engine.NewActionCompleted(driver.clk.Now(), cmd.CommandID, out), nil

	case engine.InvokeCancelAction:
		// Best-effort, fire-and-forget: run the action for its side effect, log any
		// failure, and NEVER feed a result back or return an error — the instance is
		// already terminal and cancellation must report success regardless (ADR-0028).
		a, ok := driver.resolveActionName(def, cmd.Name)
		if !ok {
			driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: cancel action not found",
				slog.String("action", cmd.Name))
			return nil, nil
		}
		// Cancel actions run for their side effect only and MUST NOT crash the
		// terminal-cancel path, so recover is always forced on here regardless of a
		// per-action WithRecover(false) — best-effort semantics (ADR-0028). The
		// per-action execution timeout is still honoured.
		bare, timeout, _ := driver.effectiveActionPolicy(a)
		cctx, cancel := actionContextFor(ctx, timeout)
		_, err := invokeActionDo(cctx, bare, cmd.Input, true)
		cancel()
		if err != nil {
			driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelError, "runtime: cancel action failed",
				slog.String("action", cmd.Name), slog.Any("error", err))
		}
		return nil, nil

	case engine.CompleteInstance:
		// The terminal outbox event is derived status-driven by
		// terminalOutboxEvent at the deliverLoop terminal edge and written inside
		// the Commit tx; nothing to perform here (ADR-0046).
		return nil, nil

	case engine.FailInstance:
		// The terminal outbox event is derived status-driven by
		// terminalOutboxEvent at the deliverLoop terminal edge and written inside
		// the Commit tx; nothing to perform here (ADR-0046).
		return nil, nil

	case engine.AwaitHuman:
		if driver.resolver == nil {
			return nil, fmt.Errorf("workflow-runtime: perform AwaitHuman: no ActorResolver configured")
		}
		if driver.tasks == nil {
			return nil, fmt.Errorf("workflow-runtime: perform AwaitHuman: no TaskStore configured")
		}
		// Resolve candidates from the eligibility spec and process variables.
		actors, err := driver.resolver.Candidates(ctx, cmd.Eligibility, st.Variables)
		if err != nil {
			return nil, fmt.Errorf("workflow-runtime: resolve candidates: %w", err)
		}
		candidateIDs := make([]string, len(actors))
		for i, a := range actors {
			candidateIDs[i] = a.ID
		}

		// Build and persist the HumanTask record. The task skeleton was already
		// added to st.Tasks by the engine (drive → KindUserTask); we find it and
		// enrich it with resolved candidates before upserting.
		task := humantask.HumanTask{
			TaskToken:   cmd.TaskToken,
			InstanceID:  st.InstanceID,
			Eligibility: cmd.Eligibility,
			Candidates:  candidateIDs,
			State:       humantask.Unclaimed,
			CreatedAt:   driver.clk.Now(),
			// Snapshot the process variables so attribute-based eligibility predicates
			// that reference data variables (e.g. vars["region"] == "EU") are
			// deterministically evaluated against the state at task-creation time.
			// maps.Clone returns nil when st.Variables is nil, which is safe.
			// Note: this is a SHALLOW copy — top-level keys are copied defensively,
			// but nested maps/slices remain shared with the instance variables;
			// eligibility predicates should rely on top-level scalar variables only.
			Vars: maps.Clone(st.Variables),
		}
		// Copy NodeID from the in-state task record if present.
		if t := st.TaskByToken(cmd.TaskToken); t != nil {
			task.NodeID = t.NodeID
			task.CreatedAt = t.CreatedAt // preserve engine-stamped time
		}
		if err := driver.tasks.Upsert(ctx, task); err != nil {
			return nil, fmt.Errorf("workflow-runtime: upsert task: %w", err)
		}
		driver.obs.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "created")))
		// No follow-up trigger: the instance parks here.
		return nil, nil

	case engine.UpdateTask:
		if driver.tasks == nil {
			return nil, fmt.Errorf("workflow-runtime: perform UpdateTask: no TaskStore configured")
		}
		if err := driver.tasks.Upsert(ctx, cmd.Task); err != nil {
			return nil, fmt.Errorf("workflow-runtime: update task: %w", err)
		}
		return nil, nil

	case engine.ScheduleTimer:
		// Defensive nil-guard: NewProcessDriver always resolves a scheduler (a
		// consumer-injected one or the in-process default), so sched is non-nil
		// after construction. This guard exists only as dead-safe code.
		if driver.sched == nil {
			return nil, fmt.Errorf("workflow-runtime: perform ScheduleTimer %q: no Scheduler configured", cmd.TimerID)
		}
		if cmd.Kind == engine.TimerRetry {
			driver.obs.actionRetries.Add(ctx, 1)
		}
		driver.armTimer(ctx, def, st.InstanceID, cmd.TimerID, cmd.Trigger)
		return nil, nil

	case engine.CancelTimer:
		// Defensive nil-guard: see ScheduleTimer above — sched is always non-nil
		// after NewProcessDriver, so this is dead-safe code.
		if driver.sched == nil {
			return nil, fmt.Errorf("workflow-runtime: perform CancelTimer %q: no Scheduler configured", cmd.TimerID)
		}
		driver.sched.Cancel(ctx, cmd.TimerID)
		return nil, nil

	case engine.ThrowSignal:
		if driver.sigbus == nil {
			return nil, fmt.Errorf("workflow-runtime: perform ThrowSignal %q: no SignalBus configured", cmd.Name)
		}
		if err := driver.sigbus.Publish(ctx, cmd.Name, cmd.Payload); err != nil {
			return nil, fmt.Errorf("workflow-runtime: perform ThrowSignal %q: %w", cmd.Name, err)
		}
		return nil, nil

	case engine.SendMessage:
		// Delivered transactionally as a message.<Name> outbox event in this step's
		// AppliedStep.Events (ADR-0067). Nothing to perform post-commit.
		return nil, nil

	case engine.StartSubInstance:
		// Defensive nil-guard: defsReg is always non-nil after NewProcessDriver
		// (defaultDefinitionRegistry is set before the option loop, and
		// WithDefinitions ignores nil). This guard exists only as dead-safe code.
		if driver.defsReg == nil {
			return nil, fmt.Errorf("workflow-runtime: perform StartSubInstance %q: no definition registry configured"+
				" (use runtime.RegisterDefinition to populate the default registry, or supply one via WithDefinitions)", cmd.DefRef.String())
		}
		childDef, err := driver.defsReg.Lookup(ctx, cmd.DefRef)
		if err != nil {
			return nil, fmt.Errorf("workflow-runtime: perform StartSubInstance %q: definition not found"+
				" (register it via runtime.RegisterDefinition or supply a registry via WithDefinitions): %w", cmd.DefRef.String(), err)
		}

		// Derive a deterministic child instance ID from the parent and command ID.
		// Scheme: "<parentInstanceID>-sub-<suffix>" where <suffix> is only the
		// trailing segment of cmd.CommandID after the last "-" (e.g. "c1", "c2").
		// cmd.CommandID format is "<instanceID>-c<N>"; embedding the full commandID
		// would cause child IDs to grow O(2^depth). Using just the short suffix
		// keeps growth linear: each nesting level adds a constant-length segment.
		// IDs remain unique because CmdSeq is monotonic within an instance.
		suffix := cmd.CommandID
		if idx := strings.LastIndex(cmd.CommandID, "-"); idx >= 0 {
			suffix = cmd.CommandID[idx+1:]
		}
		childInstanceID := st.InstanceID + "-sub-" + suffix

		// Async path: when a CallLinkStore is configured, the child is started
		// non-blocking. The parent parks at the call node; a notifier delivers
		// the outcome (SubInstanceCompleted / SubInstanceFailed) later.
		if driver.callLinks != nil {
			// Compute depth: look up THIS instance's own link (is it itself a child?).
			// Found ⇒ depth = parentLink.Depth + 1; not found ⇒ depth = 1.
			// A store error must NOT be swallowed as "not found": that would
			// miscompute depth and start a child that the guard should have
			// rejected. Propagate it so the caller can retry.
			depth := 1
			parentLink, ok, lerr := driver.callLinks.LookupChild(ctx, st.InstanceID)
			if lerr != nil {
				return nil, fmt.Errorf("workflow-runtime: call activity: depth lookup for %q: %w", st.InstanceID, lerr)
			}
			if ok {
				depth = parentLink.Depth + 1
			}
			if depth > maxCallDepth {
				return engine.NewSubInstanceFailed(driver.clk.Now(), cmd.CommandID,
					fmt.Sprintf("workflow-runtime: call activity depth limit %d exceeded (possible recursive definition: %q); "+
						"async call activity chain is too deep",
						maxCallDepth, cmd.DefRef.String()),
				), nil
			}

			link := kernel.CallLink{
				ChildInstanceID:  childInstanceID,
				ParentInstanceID: st.InstanceID,
				ParentCommandID:  cmd.CommandID,
				ParentDefID:      def.ID,
				ParentDefVersion: def.Version,
				Depth:            depth,
			}

			// Start the child's first burst non-blocking: drive it until it parks or
			// completes. The link is threaded into the child's first Create atomically.
			if err := driver.runChild(ctx, childDef, childInstanceID, cmd.Input, &link); err != nil {
				return engine.NewSubInstanceFailed(driver.clk.Now(), cmd.CommandID, err.Error()), nil
			}

			// Return nil, nil — no synchronous resume trigger. The parent stays parked
			// at the call node; the engine already handled parking when it emitted
			// StartSubInstance. The notifier will deliver SubInstanceCompleted/Failed later.
			return nil, nil
		}

		// Synchronous path (opt-out: driver.callLinks == nil): run the child to completion
		// in-process. This is the VERBATIM original behavior.

		// Fix 2: Recursion / cycle depth guard.
		//
		// A definition whose call activity references itself (direct: A→A, or via a
		// cycle: A→B→A) causes unbounded synchronous recursion through perform →
		// driver.Drive → deliverLoop → perform, which ultimately stack-overflows. We thread
		// the depth counter through ctx so every nested call increments it; when the
		// limit is reached we return a descriptive SubInstanceFailed instead of
		// crashing. The synchronous runner only supports children that run to
		// completion in one pass; async call activities (a future enhancement) would
		// not use this counter.
		depth := callDepth(ctx)
		if depth >= maxCallDepth {
			return engine.NewSubInstanceFailed(driver.clk.Now(), cmd.CommandID,
				fmt.Sprintf("workflow-runtime: call activity depth limit %d exceeded (possible recursive definition: %q); "+
					"the synchronous runner does not support cyclic or deeply nested call activities",
					maxCallDepth, cmd.DefRef.String()),
			), nil
		}
		childCtx := withCallDepth(ctx, depth+1)

		// Run the child to completion (synchronous within perform). The child uses
		// the same ProcessDriver so it shares the store, journal, outbox, catalog, and
		// scheduler. The child's Drive call drives the child's deliverLoop until the
		// child parks or completes.
		childSt, err := driver.Drive(childCtx, childDef, childInstanceID, cmd.Input)
		if err != nil {
			// Child run returned a hard error (e.g. storage failure). Propagate as
			// SubInstanceFailed so the parent instance can respond.
			return engine.NewSubInstanceFailed(driver.clk.Now(), cmd.CommandID, err.Error()), nil
		}

		// Translate the child's terminal status into a parent trigger.
		switch childSt.Status {
		case engine.StatusCompleted:
			// Pass the child's final variables back as the Output so the parent can
			// merge them. This gives the parent access to everything the child computed.
			return engine.NewSubInstanceCompleted(driver.clk.Now(), cmd.CommandID, childSt.Variables), nil

		case engine.StatusRunning:
			// Fix 1: Explicit parked-child error.
			//
			// The child parked (StatusRunning) without completing. This happens when
			// the child contains a node that requires external input — a human task,
			// timer, signal catch event, or its own call activity — that cannot be
			// resolved within a single synchronous Drive. The synchronous reference
			// runner does not support re-entering a parked child; async call activities
			// are a future enhancement.
			//
			// Return a clear, diagnosable error message so the consumer understands
			// the limitation rather than receiving a generic "did not complete" message.
			return engine.NewSubInstanceFailed(driver.clk.Now(), cmd.CommandID,
				fmt.Sprintf("workflow-runtime: call activity child %q parked (status running): "+
					"the synchronous runner does not support children that wait on human tasks, "+
					"timers, or events; async call activity is a future enhancement",
					childInstanceID),
			), nil

		default:
			// StatusFailed or any other non-completed, non-running terminal state.
			// Include the numeric status in the message so failures are diagnosable.
			return engine.NewSubInstanceFailed(driver.clk.Now(), cmd.CommandID,
				fmt.Sprintf("workflow-runtime: call activity child %q ended with status %d", childInstanceID, childSt.Status),
			), nil
		}

	default:
		return nil, fmt.Errorf("workflow-runtime: unsupported command %T", c)
	}
}

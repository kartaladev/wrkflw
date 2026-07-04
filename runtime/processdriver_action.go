package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
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

// safeActionDo invokes a.Do, converting a panic into an error so a buggy or
// malicious service action cannot crash the runner — and with it every in-flight
// instance on the replica. A recovered panic is surfaced as an ordinary action
// error, so callers route it through their normal failure path (a retryable
// ActionFailed for InvokeAction, a best-effort log for InvokeCancelAction).
// actionContext derives the context an action runs under. When actionTimeout is
// positive it applies a deadline; otherwise the parent context passes through
// unchanged. The caller must always invoke the returned cancel func.
func (r *ProcessDriver) actionContext(parent context.Context) (context.Context, context.CancelFunc) {
	if r.actionTimeout <= 0 {
		return parent, func() {}
	}
	return context.WithTimeout(parent, r.actionTimeout)
}

func safeActionDo(ctx context.Context, a action.ServiceAction, in map[string]any) (out map[string]any, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			out = nil
			err = fmt.Errorf("workflow-runtime: action panicked: %v", rec)
		}
	}()
	return a.Do(ctx, in)
}

// perform executes one command and returns the resulting trigger, if any.
// st is the current instance state, used for variable access when resolving
// human-task candidates. def is the process definition, captured by timer
// fire callbacks that need to call Deliver.
//
//nolint:cyclop // the command switch is intentionally exhaustive; each case is simple.
func (r *ProcessDriver) perform(ctx context.Context, def *definition.ProcessDefinition, st engine.InstanceState, c engine.Command) (engine.Trigger, error) {
	switch cmd := c.(type) {
	case engine.InvokeAction:
		actx, aspan := r.obs.tracer().Start(ctx, "wrkflw.action "+cmd.Name,
			trace.WithAttributes(attribute.String("wrkflw.action", cmd.Name)))
		outcome := "error"
		var elapsed float64
		defer func() {
			r.obs.actionDuration.Record(actx, elapsed,
				metric.WithAttributes(attribute.String("action", cmd.Name), attribute.String("outcome", outcome)))
			aspan.End()
		}()

		a, ok := r.resolveInvokeAction(def, cmd)
		if !ok {
			err := errors.New("unknown action: " + cmd.Name)
			aspan.RecordError(err)
			aspan.SetStatus(codes.Error, err.Error())
			r.obs.actionFailures.Add(actx, 1, metric.WithAttributes(
				attribute.String("action", cmd.Name),
				attribute.Bool("retryable", false),
			))
			if cmd.FireAndForget {
				// No token awaits a fire-and-forget action's result, so an
				// ActionFailed would only surface as ErrTokenNotFound. Log and
				// drop instead — the action was never actionable anyway.
				r.obs.tel.Logger.LogAttrs(actx, slog.LevelWarn, "runtime: fire-and-forget action not found",
					slog.String("action", cmd.Name))
				return nil, nil
			}
			return engine.NewActionFailed(r.clk.Now(), cmd.CommandID, "unknown action: "+cmd.Name, false), nil
		}
		start := r.clk.Now()
		tctx, cancel := r.actionContext(actx)
		out, err := safeActionDo(tctx, a, cmd.Input)
		cancel()
		elapsed = r.clk.Now().Sub(start).Seconds()
		if err != nil {
			aspan.RecordError(err)
			aspan.SetStatus(codes.Error, err.Error())
			r.obs.actionFailures.Add(actx, 1, metric.WithAttributes(
				attribute.String("action", cmd.Name),
				attribute.Bool("retryable", action.IsRetryable(err)),
			))
			if cmd.FireAndForget {
				// Deadline-breach and reminder actions run for their side effect
				// only; no token awaits the result. Log the failure rather than
				// feeding back an ActionFailed that no token could ever match.
				r.obs.tel.Logger.LogAttrs(actx, slog.LevelWarn, "runtime: fire-and-forget action failed",
					slog.String("action", cmd.Name), slog.Any("error", err))
				return nil, nil
			}
			return engine.NewActionFailed(r.clk.Now(), cmd.CommandID, err.Error(), action.IsRetryable(err), engine.WithJitter(r.jitter.Fraction())), nil
		}
		outcome = "ok"
		if cmd.FireAndForget {
			// Side effect performed and observed (span + duration metric). No
			// token awaits the result, so return no trigger.
			return nil, nil
		}
		return engine.NewActionCompleted(r.clk.Now(), cmd.CommandID, out), nil

	case engine.InvokeCancelAction:
		// Best-effort, fire-and-forget: run the action for its side effect, log any
		// failure, and NEVER feed a result back or return an error — the instance is
		// already terminal and cancellation must report success regardless (ADR-0028).
		a, ok := r.resolveActionName(def, cmd.Name)
		if !ok {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: cancel action not found",
				slog.String("action", cmd.Name))
			return nil, nil
		}
		cctx, cancel := r.actionContext(ctx)
		_, err := safeActionDo(cctx, a, cmd.Input)
		cancel()
		if err != nil {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelError, "runtime: cancel action failed",
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
		if r.resolver == nil {
			return nil, fmt.Errorf("workflow-runtime: perform AwaitHuman: no ActorResolver configured")
		}
		if r.tasks == nil {
			return nil, fmt.Errorf("workflow-runtime: perform AwaitHuman: no TaskStore configured")
		}
		// Resolve candidates from the eligibility spec and process variables.
		actors, err := r.resolver.Candidates(ctx, cmd.Eligibility, st.Variables)
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
			CreatedAt:   r.clk.Now(),
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
		if err := r.tasks.Upsert(ctx, task); err != nil {
			return nil, fmt.Errorf("workflow-runtime: upsert task: %w", err)
		}
		r.obs.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "created")))
		// No follow-up trigger: the instance parks here.
		return nil, nil

	case engine.UpdateTask:
		if r.tasks == nil {
			return nil, fmt.Errorf("workflow-runtime: perform UpdateTask: no TaskStore configured")
		}
		if err := r.tasks.Upsert(ctx, cmd.Task); err != nil {
			return nil, fmt.Errorf("workflow-runtime: update task: %w", err)
		}
		return nil, nil

	case engine.ScheduleTimer:
		if r.sched == nil {
			return nil, fmt.Errorf("workflow-runtime: perform ScheduleTimer %q: no Scheduler configured", cmd.TimerID)
		}
		if cmd.Kind == engine.TimerRetry {
			r.obs.actionRetries.Add(ctx, 1)
		}
		r.armTimer(def, st.InstanceID, cmd.TimerID, cmd.FireAt)
		return nil, nil

	case engine.CancelTimer:
		if r.sched == nil {
			return nil, fmt.Errorf("workflow-runtime: perform CancelTimer %q: no Scheduler configured", cmd.TimerID)
		}
		r.sched.Cancel(cmd.TimerID)
		return nil, nil

	case engine.ThrowSignal:
		if r.sigbus == nil {
			return nil, fmt.Errorf("workflow-runtime: perform ThrowSignal %q: no SignalBus configured", cmd.Name)
		}
		if err := r.sigbus.Publish(ctx, cmd.Name, cmd.Payload); err != nil {
			return nil, fmt.Errorf("workflow-runtime: perform ThrowSignal %q: %w", cmd.Name, err)
		}
		return nil, nil

	case engine.SendMessage:
		// Delivered transactionally as a message.<Name> outbox event in this step's
		// AppliedStep.Events (ADR-0067). Nothing to perform post-commit.
		return nil, nil

	case engine.StartSubInstance:
		// Nil-registry guard: a missing registry is a configuration error, not a
		// retryable runtime failure, so we fail fast with a descriptive message.
		if r.defsReg == nil {
			return nil, fmt.Errorf("workflow-runtime: perform StartSubInstance %q: no definition registry configured (use WithDefinitions)", cmd.DefRef)
		}
		childDef, err := r.defsReg.Lookup(ctx, cmd.DefRef)
		if err != nil {
			return nil, fmt.Errorf("workflow-runtime: perform StartSubInstance %q: registry lookup: %w", cmd.DefRef, err)
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
		if r.callLinks != nil {
			// Compute depth: look up THIS instance's own link (is it itself a child?).
			// Found ⇒ depth = parentLink.Depth + 1; not found ⇒ depth = 1.
			// A store error must NOT be swallowed as "not found": that would
			// miscompute depth and start a child that the guard should have
			// rejected. Propagate it so the caller can retry.
			depth := 1
			parentLink, ok, lerr := r.callLinks.LookupChild(ctx, st.InstanceID)
			if lerr != nil {
				return nil, fmt.Errorf("workflow-runtime: call activity: depth lookup for %q: %w", st.InstanceID, lerr)
			}
			if ok {
				depth = parentLink.Depth + 1
			}
			if depth > maxCallDepth {
				return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID,
					fmt.Sprintf("workflow-runtime: call activity depth limit %d exceeded (possible recursive definition: %q); "+
						"async call activity chain is too deep",
						maxCallDepth, cmd.DefRef),
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
			if err := r.runChild(ctx, childDef, childInstanceID, cmd.Input, &link); err != nil {
				return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID, err.Error()), nil
			}

			// Return nil, nil — no synchronous resume trigger. The parent stays parked
			// at the call node; the engine already handled parking when it emitted
			// StartSubInstance. The notifier will deliver SubInstanceCompleted/Failed later.
			return nil, nil
		}

		// Synchronous path (opt-out: r.callLinks == nil): run the child to completion
		// in-process. This is the VERBATIM original behavior.

		// Fix 2: Recursion / cycle depth guard.
		//
		// A definition whose call activity references itself (direct: A→A, or via a
		// cycle: A→B→A) causes unbounded synchronous recursion through perform →
		// r.Run → deliverLoop → perform, which ultimately stack-overflows. We thread
		// the depth counter through ctx so every nested call increments it; when the
		// limit is reached we return a descriptive SubInstanceFailed instead of
		// crashing. The synchronous runner only supports children that run to
		// completion in one pass; async call activities (a future enhancement) would
		// not use this counter.
		depth := callDepth(ctx)
		if depth >= maxCallDepth {
			return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID,
				fmt.Sprintf("workflow-runtime: call activity depth limit %d exceeded (possible recursive definition: %q); "+
					"the synchronous runner does not support cyclic or deeply nested call activities",
					maxCallDepth, cmd.DefRef),
			), nil
		}
		childCtx := withCallDepth(ctx, depth+1)

		// Run the child to completion (synchronous within perform). The child uses
		// the same ProcessDriver so it shares the store, journal, outbox, catalog, and
		// scheduler. The child's Run call drives the child's deliverLoop until the
		// child parks or completes.
		childSt, err := r.Run(childCtx, childDef, childInstanceID, cmd.Input)
		if err != nil {
			// Child run returned a hard error (e.g. storage failure). Propagate as
			// SubInstanceFailed so the parent instance can respond.
			return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID, err.Error()), nil
		}

		// Translate the child's terminal status into a parent trigger.
		switch childSt.Status {
		case engine.StatusCompleted:
			// Pass the child's final variables back as the Output so the parent can
			// merge them. This gives the parent access to everything the child computed.
			return engine.NewSubInstanceCompleted(r.clk.Now(), cmd.CommandID, childSt.Variables), nil

		case engine.StatusRunning:
			// Fix 1: Explicit parked-child error.
			//
			// The child parked (StatusRunning) without completing. This happens when
			// the child contains a node that requires external input — a human task,
			// timer, signal catch event, or its own call activity — that cannot be
			// resolved within a single synchronous Run. The synchronous reference
			// runner does not support re-entering a parked child; async call activities
			// are a future enhancement.
			//
			// Return a clear, diagnosable error message so the consumer understands
			// the limitation rather than receiving a generic "did not complete" message.
			return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID,
				fmt.Sprintf("workflow-runtime: call activity child %q parked (status running): "+
					"the synchronous runner does not support children that wait on human tasks, "+
					"timers, or events; async call activity is a future enhancement",
					childInstanceID),
			), nil

		default:
			// StatusFailed or any other non-completed, non-running terminal state.
			// Include the numeric status in the message so failures are diagnosable.
			return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID,
				fmt.Sprintf("workflow-runtime: call activity child %q ended with status %d", childInstanceID, childSt.Status),
			), nil
		}

	default:
		return nil, fmt.Errorf("workflow-runtime: unsupported command %T", c)
	}
}

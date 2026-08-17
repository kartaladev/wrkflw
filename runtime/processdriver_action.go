package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// terminalErr derives a short, human-readable error message from a terminal
// instance state — the cause of death a call-activity parent sees on
// kernel.CallOutcome.Err. It prefers the error text of the first incident that
// may stand as the cause of death (see [causeOfDeathIncident], which allow-lists
// engine.IncidentAction); if there is none it falls back to a status-keyed
// generic message.
//
// Unlike [terminalEventErr] it has no FailInstance{Err} rung between the two —
// it is handed only the state, never the commands. See [causeOfDeathIncident],
// where that divergence is documented; do not add one here.
func terminalErr(st engine.InstanceState) string {
	if inc, ok := causeOfDeathIncident(st); ok {
		return inc.Error
	}
	switch st.Status {
	case engine.StatusTerminated:
		return "instance terminated"
	default:
		return "instance failed"
	}
}

// Child-instance id derivation (see [childInstanceIDFor]).
const (
	// childInstanceIDInfix separates a parent instance id from the segment that
	// names one of its call-activity children.
	childInstanceIDInfix = "-sub-"
	// childCommandSuffixLen is the length of the hex digest that stands in for an
	// opaque command id. 32 bits is ample: the suffix only has to be unique among
	// the call-activity commands of ONE parent instance (a handful), and the child
	// id is never security-sensitive.
	childCommandSuffixLen = 8
	// childInstanceIDMaxLen bounds every derived child id, leaving headroom under
	// the 255-character instance_id column of the SQL stores.
	childInstanceIDMaxLen = 200
	// childInstanceIDFoldedLen is the digest length used when a derivation would
	// exceed childInstanceIDMaxLen. 128 bits keeps folded ids collision-free in
	// practice while staying far shorter than the bound.
	childInstanceIDFoldedLen = 32
)

// childInstanceIDFor derives the instance id of the child spawned by a
// StartSubInstance command from the parent instance id and the command id.
//
// The derivation is a PURE function of its two inputs, because the id doubles as
// the call link's identity: re-driving the same command (a retry, a crash
// recovery) must address the child that was already started rather than spawn a
// second one.
//
// The shape is "<parentInstanceID>-sub-<suffix>", where <suffix> is:
//
//   - the parent's own engine counter segment ("c3") when commandID has the
//     engine's built-in "<parentInstanceID>-c<N>" form — the id shape [engine.Step]
//     mints when no IDGenerator is injected. Kept verbatim so ids derived before
//     ADR-0149 still resolve to the same child.
//   - otherwise a short fixed-length digest of the whole command id. The runtime
//     injects an opaque generator (xid by default, ADR-0149), whose ids carry no
//     counter segment; embedding one verbatim would grow the child id by ~25
//     characters per nesting level, so a chain far shallower than maxCallDepth
//     would overflow the instance_id column with an opaque driver error instead of
//     failing on the depth guard.
//
// Growth per nesting level is therefore constant, and an id that would still
// exceed childInstanceIDMaxLen (an extremely deep chain, or a pathologically long
// parent id) is folded into a bounded digest form so the depth guard — not the
// database — is what stops a runaway chain.
func childInstanceIDFor(parentInstanceID, commandID string) string {
	id := parentInstanceID + childInstanceIDInfix + childCommandSuffix(parentInstanceID, commandID)
	if len(id) <= childInstanceIDMaxLen {
		return id
	}
	return "sub-" + shortHash(id, childInstanceIDFoldedLen)
}

// childCommandSuffix returns the short, stable segment that names the child of
// parentInstanceID spawned by commandID. See [childInstanceIDFor] for the rules.
func childCommandSuffix(parentInstanceID, commandID string) string {
	if seq, ok := strings.CutPrefix(commandID, parentInstanceID+"-"); ok && isEngineCommandCounter(seq) {
		return seq
	}
	return shortHash(commandID, childCommandSuffixLen)
}

// isEngineCommandCounter reports whether s is the engine's built-in command
// counter segment: "c" followed by at least one decimal digit.
func isEngineCommandCounter(s string) bool {
	digits, ok := strings.CutPrefix(s, "c")
	if !ok || digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// shortHash returns the first n hex characters of the SHA-256 digest of s. It is
// used for id derivation only — never as a security primitive — but SHA-256 keeps
// the truncated output uniformly distributed, which is what bounds collisions.
func shortHash(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:n]
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

// validateTaskCommands rejects a step whose task projections are internally
// contradictory, BEFORE this iteration's state is committed.
//
// This is the primary enforcement seam for the invariant [humantask.Validate]
// defines. It cannot live in perform(): perform runs AFTER the commit, and a
// perform error aborts the remaining command queue — so a rejection there commits
// the state, drops the later commands, raises no incident, and leaves the token
// parked on a command that will never be answered. Measured; see ADR-0183.
//
// It mirrors [ProcessDriver.resolveHumanCandidates], which runs pre-commit for a
// related reason. Only UpdateTask is inspected: AwaitHuman has a single emit
// site and performAwaitHuman builds its task with State: Unclaimed and no Claim,
// so it cannot be claim-invalid.
func validateTaskCommands(cmds []engine.Command) error {
	for _, c := range cmds {
		ut, ok := c.(engine.UpdateTask)
		if !ok {
			continue
		}
		if err := humantask.Validate(ut.Task); err != nil {
			return fmt.Errorf("workflow-runtime: reject step: %w", err)
		}
	}
	return nil
}

// resolveHumanCandidates expands the eligibility spec of every AwaitHuman command
// emitted by a step into concrete actors and writes them onto the matching task
// in st, so the candidates ride the SAME commit that parks the task.
//
// This runs before the step's snapshot is captured, unlike [ProcessDriver.perform],
// which runs after the commit: the instance view is a pure projection over the
// persisted snapshot, so a post-commit write would be invisible to every later
// reader (ADR-0147 amendment #1). Resolving here also means a resolver failure
// aborts the step cleanly instead of leaving a committed instance parked on a
// task the store never received.
//
// It is a no-op when the step emitted no AwaitHuman command, so the resolver is
// required only by definitions that actually contain user tasks.
func (driver *ProcessDriver) resolveHumanCandidates(ctx context.Context, st *engine.InstanceState, cmds []engine.Command) error {
	for _, c := range cmds {
		cmd, ok := c.(engine.AwaitHuman)
		if !ok {
			continue
		}
		if driver.resolver == nil {
			return fmt.Errorf("workflow-runtime: resolve candidates for task %s: no ActorResolver configured", cmd.TaskID)
		}
		actors, err := driver.resolveCandidates(ctx, cmd.Eligibility, st.Variables)
		if err != nil {
			return fmt.Errorf("workflow-runtime: resolve candidates: %w", err)
		}
		task := st.TaskByID(cmd.TaskID)
		if task == nil {
			// The engine creates the task and the AwaitHuman command together, so a
			// missing record is an invariant violation rather than a routine miss.
			return fmt.Errorf("workflow-runtime: resolve candidates: no task record for %q: %w",
				cmd.TaskID, humantask.ErrTaskNotFound)
		}
		// Deep-copy on ingest: the resolver owns the returned slice and the actors
		// inside it — a registry-backed resolver typically hands back values that
		// alias its own state. Aliasing here would let a later registry update
		// rewrite the candidate list of an already-committed instance, and that
		// list is audit data (ADR-0147). The engine's own ingest path
		// (handleHumanCandidatesResolved) clones for the same reason.
		task.Candidates = authz.CloneActors(actors)
	}
	return nil
}

// resolveCandidates performs one ActorResolver lookup under the driver's
// candidate-resolve timeout. It exists so the bound is applied at exactly one
// place, and reuses actionContextFor's convention: a non-positive timeout passes
// the parent context through unchanged.
func (driver *ProcessDriver) resolveCandidates(ctx context.Context, spec authz.AuthzSpec, vars map[string]any) ([]authz.Actor, error) {
	rctx, cancel := actionContextFor(ctx, driver.candidateResolveTimeout)
	defer cancel()
	return driver.resolver.Candidates(rctx, spec, vars)
}

// perform executes one command and returns the resulting trigger, if any.
// st is the current instance state, used for variable access and to project the
// committed human task into the task store. def is the process definition,
// captured by timer fire callbacks that need to call ApplyTrigger.
//
// perform runs AFTER the step's state has been committed, so it must not be used
// to mutate state the caller expects to be persisted — see
// [ProcessDriver.resolveHumanCandidates] for the pre-commit counterpart.
//
// It is a pure dispatcher: every command that does real work has its own
// perform* method, so this switch stays a readable map from command type to
// handler.
func (driver *ProcessDriver) perform(ctx context.Context, def *model.ProcessDefinition, st engine.InstanceState, c engine.Command) (engine.Trigger, error) {
	switch cmd := c.(type) {
	case engine.InvokeAction:
		return driver.performInvokeAction(ctx, def, cmd)

	case engine.InvokeCancelAction:
		return driver.performCancelAction(ctx, def, cmd)

	case engine.CompleteInstance, engine.FailInstance, engine.SendMessage:
		// Nothing to perform post-commit for any of these — each is already
		// delivered inside the Commit tx:
		//   - CompleteInstance / FailInstance: the terminal outbox event is derived
		//     status-driven by terminalOutboxEvent at the deliverLoop terminal edge
		//     (ADR-0046).
		//   - SendMessage: delivered as a message.<Name> outbox event in this step's
		//     AppliedStep.Events (ADR-0067).
		return nil, nil

	case engine.AwaitHuman:
		return driver.performAwaitHuman(ctx, st, cmd)

	case engine.UpdateTask:
		return driver.performUpdateTask(ctx, cmd)

	// NOTE: engine.ScheduleTimer and engine.CancelTimer never reach perform —
	// the deliverLoop handles them entirely on its commit path (in-tx durable
	// persist via the runtime jobStore, post-commit scheduler
	// activate/deactivate — ADR-0134) and skips them before dispatching here.

	case engine.ThrowSignal:
		return driver.performThrowSignal(ctx, cmd)

	case engine.StartSubInstance:
		return driver.performStartSubInstance(ctx, def, st, cmd)

	default:
		return nil, fmt.Errorf("workflow-runtime: unsupported command %T", c)
	}
}

// performInvokeAction invokes the service action named by an [engine.InvokeAction]
// command and translates the outcome into the trigger that resumes the awaiting
// token: ActionCompleted on success, ActionFailed on error or on an unresolvable
// action name.
//
// A fire-and-forget invocation (deadline-breach and reminder actions) has no
// awaiting token, so each of its outcomes is logged and observed rather than fed
// back as a trigger that no token could ever match.
func (driver *ProcessDriver) performInvokeAction(ctx context.Context, def *model.ProcessDefinition, cmd engine.InvokeAction) (engine.Trigger, error) {
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
}

// performCancelAction runs the action named by an [engine.InvokeCancelAction]
// command. It is best-effort and fire-and-forget: the action runs for its side
// effect, any failure is logged, and a result is NEVER fed back nor an error
// returned — the instance is already terminal and cancellation must report
// success regardless (ADR-0028).
func (driver *ProcessDriver) performCancelAction(ctx context.Context, def *model.ProcessDefinition, cmd engine.InvokeCancelAction) (engine.Trigger, error) {
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
}

// performAwaitHuman projects the human task named by an [engine.AwaitHuman]
// command into the queryable task store. The task was already added to st.Tasks
// by the engine (drive → KindUserTask) and enriched with the resolved candidates
// by resolveHumanCandidates BEFORE the commit, so this only mirrors the committed
// record out. No follow-up trigger is returned: the instance parks here.
func (driver *ProcessDriver) performAwaitHuman(ctx context.Context, st engine.InstanceState, cmd engine.AwaitHuman) (engine.Trigger, error) {
	if driver.tasks == nil {
		return nil, fmt.Errorf("workflow-runtime: perform AwaitHuman: no TaskStore configured")
	}
	task := humantask.HumanTask{
		TaskID:      cmd.TaskID,
		InstanceID:  st.InstanceID,
		Eligibility: cmd.Eligibility,
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
	// Copy the engine-owned fields from the committed task record if present.
	if t := st.TaskByID(cmd.TaskID); t != nil {
		task.NodeID = t.NodeID
		task.CreatedAt = t.CreatedAt // preserve engine-stamped time
		task.Vars = t.Vars           // engine-snapshotted at creation
		// Resolved pre-commit and already durable. Clone anyway: handing the
		// store the engine's own backing array would let a TaskStore that keeps
		// the value verbatim share mutable actor state with live instance
		// state — the same hazard the clone on the resolver side guards against.
		task.Candidates = authz.CloneActors(t.Candidates)
		// DueAt is engine-computed from the node's deadline trigger and is what
		// inbox / SLA views render, so the projection must carry it. Copy the
		// POINTEE: the store's record must not alias live engine state.
		if t.DueAt != nil {
			due := *t.DueAt
			task.DueAt = &due
		}
	}
	if err := driver.tasks.Upsert(ctx, task); err != nil {
		return nil, fmt.Errorf("workflow-runtime: upsert task: %w", err)
	}
	driver.obs.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "created")))
	// No follow-up trigger: the instance parks here.
	return nil, nil
}

// performUpdateTask writes the task carried by an [engine.UpdateTask] command
// through to the task store, keeping the queryable projection in step with the
// engine-side record.
func (driver *ProcessDriver) performUpdateTask(ctx context.Context, cmd engine.UpdateTask) (engine.Trigger, error) {
	if driver.tasks == nil {
		return nil, fmt.Errorf("workflow-runtime: perform UpdateTask: no TaskStore configured")
	}
	if err := driver.tasks.Upsert(ctx, cmd.Task); err != nil {
		return nil, fmt.Errorf("workflow-runtime: update task: %w", err)
	}
	return nil, nil
}

// performThrowSignal publishes the signal carried by an [engine.ThrowSignal]
// command on the configured SignalBus. Signal delivery is fan-out and handled by
// the bus, so nothing comes back to the throwing instance.
func (driver *ProcessDriver) performThrowSignal(ctx context.Context, cmd engine.ThrowSignal) (engine.Trigger, error) {
	if driver.sigbus == nil {
		return nil, fmt.Errorf("workflow-runtime: perform ThrowSignal %q: no SignalBus configured", cmd.Name)
	}
	if err := driver.sigbus.Publish(ctx, cmd.Name, cmd.Payload); err != nil {
		return nil, fmt.Errorf("workflow-runtime: perform ThrowSignal %q: %w", cmd.Name, err)
	}
	return nil, nil
}

// performStartSubInstance resolves the child definition and the deterministic
// child instance id for an [engine.StartSubInstance] command, then hands off to
// whichever call-activity strategy is wired: the async, call-link-backed path
// when a CallLinkStore is configured, else the synchronous in-process runner.
func (driver *ProcessDriver) performStartSubInstance(ctx context.Context, def *model.ProcessDefinition, st engine.InstanceState, cmd engine.StartSubInstance) (engine.Trigger, error) {
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

	// Derive the deterministic child instance id from the parent id and the
	// command id. The derivation is length-bounded and grows by a constant per
	// nesting level whatever id shape the injected generator mints — see
	// [childInstanceIDFor] for the scheme and why it matters.
	childInstanceID := childInstanceIDFor(st.InstanceID, cmd.CommandID)

	// Async path: when a CallLinkStore is configured, the child is started
	// non-blocking. The parent parks at the call node; a notifier delivers
	// the outcome (SubInstanceCompleted / SubInstanceFailed) later.
	if driver.callLinks != nil {
		return driver.startSubInstanceAsync(ctx, def, st, cmd, childDef, childInstanceID)
	}

	// Synchronous path (opt-out: driver.callLinks == nil): run the child to completion
	// in-process.
	return driver.startSubInstanceSync(ctx, cmd, childDef, childInstanceID)
}

// startSubInstanceAsync starts the call-activity child non-blocking and records a
// [kernel.CallLink] so the outcome can be routed back later. It returns no
// trigger: the parent stays parked at the call node (the engine parked it when it
// emitted StartSubInstance) and a notifier delivers SubInstanceCompleted /
// SubInstanceFailed once the child finishes.
//
// Depth is derived from the parent's own call link, and a chain deeper than
// maxCallDepth fails the command rather than starting the child.
func (driver *ProcessDriver) startSubInstanceAsync(
	ctx context.Context,
	def *model.ProcessDefinition,
	st engine.InstanceState,
	cmd engine.StartSubInstance,
	childDef *model.ProcessDefinition,
	childInstanceID string,
) (engine.Trigger, error) {
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

// startSubInstanceSync runs the call-activity child to completion in-process and
// translates its terminal status into the parent's resume trigger
// (SubInstanceCompleted / SubInstanceFailed). It is the opt-out path taken when
// no CallLinkStore is configured.
//
// A ctx-threaded depth counter guards against a definition whose call activity
// recurses into itself. A child that merely parked is reported with an explicit,
// diagnosable message rather than a generic failure, since re-entering a parked
// child is a limitation of this runner rather than an error in the definition.
func (driver *ProcessDriver) startSubInstanceSync(ctx context.Context, cmd engine.StartSubInstance, childDef *model.ProcessDefinition, childInstanceID string) (engine.Trigger, error) {
	// Recursion / cycle depth guard.
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
		// Explicit parked-child error.
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
}

# 129. `Step` takes `context.Context` for trace-correlated logging

- Status: Accepted
- Date: 2026-07-13

## Context

Task C7 (commit `de631c2`) added four `slog.Default().Warn`/`Debug` calls at
the engine's deliberate silent no-op / swallowed-error sites: a token routed
to a missing node (`drive`, `engine/step.go`), a malformed boundary
`ErrorExpr` eval error (`findDirectBoundary`, `engine/step_errors.go`), a
scope-resolution error during cancel (`handleCancelRequested`,
`engine/step_triggers.go`), and a `TimerFired` against an already-terminal
instance (`handleTimerFired`, `engine/step_triggers.go`).

`slog.Default().Warn`/`Debug` use the package-level default logger and carry
no request/trace correlation — a log line from one of these sites cannot be
joined to the OpenTelemetry span of the `Step` call that produced it. The
project's `golang-observability` standard (and `cc-skills-golang@golang-observability`)
requires context-aware logging (`slog.*Context(ctx, ...)`) precisely so log
records carry `trace_id`/`span_id` via the `otelslog` bridge or an
equivalent handler. The engine, however, threads **no** `context.Context`
anywhere: `Step` and its entire internal call graph (`drive`,
`resumeAndDrive`, `propagateError` and its boundary-lookup helpers, every
`handle*` trigger function, the compensation-walk functions, the arm-firing
functions, and the `nodeStrategy.enter` dispatch) take no `ctx` parameter.

`cc-skills-golang@golang-context` and this project's own conventions
(CLAUDE.md "Common Pitfalls" / Go skills) are explicit: `ctx` must be an
idiomatic first parameter, and a `context.Context` must **never** be stored
in a struct field on a long-lived type. `StepOptions` is exactly such a
type — it is a value the caller constructs once and may reuse across many
`Step` calls — so storing `ctx` there would violate that rule and would also
make `Step`'s purity harder to reason about (a `StepOptions.Evaluator`-style
field the caller sets once and forgets, silently going stale as its
originating request context is cancelled).

The engine's core invariant (`engine/step.go` godoc, ADR-0049/0056) is that
`Step` is **pure and replay-deterministic**: for identical `(def, st, trg,
opt)` it must return identical `(state, commands)`. Any new input to `Step`
must not become a hidden source of nondeterminism.

## Decision

Add `ctx context.Context` as the **first parameter** of the public `Step`
function:

```go
func Step(ctx context.Context, def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error)
```

`ctx` is threaded through the internal call graph as an explicit first
parameter on every function that lies on a path to one of the four log
sites — the `handle*` trigger functions dispatched by `Step`'s switch
(`handleStartInstance`, `handleActionCompleted`, `handleCancelRequested`,
`handleCompensateRequested`, `handleActionFailed`, `handleTimerFired`,
`handleHumanCompleted`, `handleSignalReceived`, `handleSubInstanceCompleted`,
`handleSubInstanceFailed`, `handleMessageReceived`, `handleResolveIncident`),
`drive`, `resumeAndDrive`, `propagateError`, `findDirectBoundary`,
`findEnclosingBoundary`, `routeToBoundary`, `handleUnhandledError`,
`dispatchArmCascade`, `fireBoundaryArm`, `resolveGatewayWin`,
`fireEventTriggeredSubprocessArm`, `stepCompensateRequested`,
`beginCompensation`, `stepCompensationAdvance`, `stepCompensationFinish`,
`applyFinish`, `handleDeadlineFired`, `handleRetryFired`, and
`reinvokeServiceAction`. Two trigger handlers that never reach a log site or
a `drive`-family call (`handleHumanClaimed`, `handleHumanReassigned`) and one
timer handler with the same property (`handleReminderFired`) are left
unchanged — adding an unused `ctx` parameter there would be pure churn.

For the node-strategy dispatch layer (`nodeStrategy.enter(c *stepCtx, ...)`,
`engine/step_nodes.go`), `ctx` is carried as a **field on the internal
`stepCtx` struct** rather than added to the `enter` method signature. This is
an accepted, narrowly-scoped exception to "never store a context in a
struct": `stepCtx` is not a long-lived, caller-held value — it is
constructed fresh inside `drive` for the duration of a single node-entry
dispatch and discarded immediately after, exactly like `at time.Time` and
`eval ConditionEvaluator`, which the struct already carries the same way.
`drive` sets `stepCtx.ctx` from its own `ctx` parameter; the one strategy
that needs it (`endEventStrategy.enter`'s `EndError` branch, which calls
`propagateError`) reads it back via `c.ctx`.

The four converted call sites become `slog.WarnContext(ctx, ...)` /
`slog.DebugContext(ctx, ...)`, unchanged in level, message, and attributes.

`ctx` is used for **logging only**. It is never read via `ctx.Done()`,
`ctx.Err()`, or `ctx.Value()` anywhere in `engine/`, and no engine code
branches on it — grepping the package for those three calls returns nothing.
`Step` remains pure and replay-deterministic: passing a cancelled, expired,
or `context.TODO()` context changes nothing about the `(state, commands)`
result, only whether/how the four no-op sites' log records correlate to a
trace.

`StepOptions` is unchanged — `ctx` is not added there.

`runtime.ProcessDriver.deliverLoop` already opens an OpenTelemetry span
around each `Step` call (`driver.obs.tracer().Start(ctx, "wrkflw.step",
...)`, local variable `stepCtx`); it now passes that span-carrying context
straight into `engine.Step`, so the engine's no-op-path log records land
inside the same span as the surrounding step metrics/commands, with no
runtime-side plumbing beyond passing the context it already had.

## Consequences

- `Step`'s signature is a **breaking change** for every direct caller —
  library consumers embedding `wrkflw`, and every test/example in this repo.
  Callers with no request-scoped context available use `context.Background()`
  (or `context.TODO()` for "not yet wired") at the call boundary, mirroring
  the pattern `cc-skills-golang@golang-context` describes for entry points;
  tests use `t.Context()` (Go 1.25).
- The four C7 log sites now correlate to the caller's trace span instead of
  being orphaned `slog.Default()` records — the original motivation for this
  task.
- No other exported symbol changes shape; `StepOptions`, `StepResult`,
  `Trigger`, and every trigger constructor are untouched. `go doc ./engine`
  changes only in `Step`'s one-line signature.
- Determinism is preserved by construction and by convention: `ctx` is
  documented on `Step` as logging-only, and the "no `ctx.Done/Err/Value` in
  `engine/`" invariant is a one-line `grep` any future change can re-verify.
  The purity tests (`engine/purity_test.go`) are unaffected — `context` is
  the standard library, not a denied vendor import, and importing it carries
  no wall-clock read.
- `stepCtx` gaining a `ctx` field is intentionally **not** generalized into
  "the engine now stores contexts in structs" — it is scoped to this one
  short-lived, per-call bundle that already carried equivalent per-call
  state (`at`, `eval`, `mode`). No other struct in the engine gains a `ctx`
  field as part of this change.
- `runtime/processdriver.go`'s `deliverLoop` required a one-line change
  (pass its existing `stepCtx` span context into `engine.Step`) since it
  already constructed a suitable context; no new span or tracer wiring was
  needed.

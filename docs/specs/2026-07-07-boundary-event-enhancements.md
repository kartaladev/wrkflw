# Spec: boundary-event enhancements — fire-once action, flexible error matching, activity wait-option cleanup (deadline split + reminder rename), example rename

- **Date:** 2026-07-07
- **Status:** Approved (design)
- **ADRs:** 0103 (boundary action + wait-option cleanup), 0104 (flexible boundary error matching)
- **Depends on:** `2026-07-07-typed-trigger-spec-and-cron.md` (ADR-0102) — the
  `schedule.TriggerSpec` duration type lands first; this spec's timer options
  adopt it.

## Context

Boundary events (`event.NewBoundary`) today (a) only *route* a token down their
outgoing flow when they fire, and (b) match errors only by exact code string
(`WithBoundaryErrorCode`, or catch-all `""`). Two ergonomic gaps:

1. No way to attach a fire-once side-effect action to a boundary (the engine
   already does this for deadlines via `DeadlineAction` →
   `InvokeAction{FireAndForget: true}`; `WithDeadline` is essentially "a timer
   boundary with a bundled action").
2. No way to decide *dynamically* whether an error boundary should catch — e.g.
   catch only when a process variable is set, or match on the real error type
   via `errors.As`.

Relevant existing facts:
- Actions are attached to nodes by name and resolved via the catalog
  (`DeadlineAction`/`ReminderAction`/`CompensationAction`/`CancelHandler`,
  `definition/model/node.go:44-72`).
- The runtime's `perform` already runs a `FireAndForget` action, logs a warning
  on failure, and feeds no trigger back (`runtime/processdriver_action.go`).
- Non-serializable Go values already live on nodes: inline actions
  (`WithAction`/`WithActionFunc` → `TaskAction.Inline action.Action`) are a
  Go-authoring-only escape hatch absent from the YAML/wire form.
- The engine matches boundary errors in `propagateError` (`engine/step_errors.go`)
  using the error CODE string. `ActionFailed` carries only `Err string`
  (= `err.Error()`); the original `error` value is not currently threaded in.
- Not every error source has a Go `error`: an `ErrorEndEvent` and a failed
  sub-instance throw a bare **code** (no underlying `error`).
- Injected error context variables already exist (`_errorMessage`, etc.,
  `engine/step_triggers.go:268`).

These are deliberate, contained engine-core additions (like ADR-0028's cancel
actions).

## Feature 1: `event.WithBoundaryAction` (ADR-0103)

A fire-once side-effect action run when a boundary fires, for ANY trigger type.

**Decisions (locked):** fire-once, result discarded; all trigger types
(timer/message/signal/error); on failure log + continue routing (reuses
`FireAndForget`); the action `InvokeAction` is emitted BEFORE routing, for both
interrupting and non-interrupting boundaries.

**Definition layer**
- Add `Action string` to `event.BoundaryEvent` (unprefixed, like its siblings).
- `WithBoundaryAction(name string) BoundaryOption` in `definition/event/options.go`.
- Serialization: add `BoundaryAction string \`json:"boundaryAction,omitempty"\``
  to `model.NodeWire`; project in the boundary `ToWire`/`FromWire`.

**Engine layer** (only behavioral change for this feature)
- Add `Action string` to `boundaryArm` (`engine/state.go`); capture `n.Action`
  in `armBoundaries`.
- In `fireBoundaryArm`, when `ba.Action != ""`, prepend before routing (both
  branches): `InvokeAction{CommandID: s.nextCommandID(), Name: ba.Action,
  Input: copyVars(s.Variables), FireAndForget: true}`.

**Runtime layer:** no changes.

## Feature 2: flexible boundary error matching (ADR-0104)

A three-tier ladder for deciding whether an ERROR boundary catches. Only applies
to error boundaries (no timer/signal/message trigger set). Precedence, highest
first:

1. **`WithBoundaryErrorCheck(fn)` — Go closure predicate (non-serializable).**
   ```go
   func WithBoundaryErrorCheck(fn func(vars map[string]any, err error) bool) BoundaryOption
   ```
   - Stored as a non-serializable field `ErrorCheck func(map[string]any, error) bool`
     on `event.BoundaryEvent` (Go-authoring-only escape hatch, like inline
     actions — absent from the YAML/wire form).
   - `vars` is the current instance variables; `err` is the thrown error.
   - For **action-thrown** failures, `err` is the ORIGINAL error value (enables
     `errors.Is`/`errors.As`), threaded transiently from the runtime through a
     new non-persisted `ActionFailed` field into `propagateError`. For
     **bare-code** sources (`ErrorEndEvent`, sub-instance failure) `err` is a
     synthesized error whose `Error()` == the code.
   - If `fn` returns true, this boundary catches.

2. **`WithBoundaryErrorExpr(expr string)` — expr-lang predicate (serializable).**
   ```go
   func WithBoundaryErrorExpr(expr string) BoundaryOption
   ```
   - Stored as `ErrorExpr string` on `event.BoundaryEvent`; serialized via a new
     `NodeWire.BoundaryErrorExpr` field.
   - Evaluated by the existing `ConditionEvaluator`/expr infra with an
     environment of the instance variables plus `_error` (the error code string,
     matching the existing `_error*` convention). Truthy result → catch.
   - Consistent with gateway conditions and CLAUDE.md's "use expr-lang for
     predicates" rule; the YAML-authorable dynamic option.

3. **`WithBoundaryErrorCode(code string)` — existing exact/catch-all match.**
   Unchanged. Used when neither Check nor Expr is set.

**Engine layer**
- Add `ErrorCheck func(map[string]any, error) bool` and `ErrorExpr string` to
  `boundaryArm` (Check is transient/live-only; see Determinism below).
- Extend the boundary-match sites in `propagateError` (direct-attachment and
  enclosing-scope scans, `engine/step_errors.go:86,189`): before the
  `n.ErrorCode == "" || n.ErrorCode == errorCode` check, apply Check (if set)
  then Expr (if set); fall back to the code match otherwise.
- Add a live `error` parameter (the thrown cause, nil for bare-code sources) to
  `propagateError`; `handleActionFailed` passes it from the new `ActionFailed`
  field. When nil, synthesize `errors.New(errorCode)` for the Check closure.

**Runtime layer**
- `NewActionFailed` gains an optional live-error carrier (e.g. a
  `WithCause(err error)` option or a transient field) set at
  `runtime/processdriver_action.go:136`. The field is `json:"-"` / excluded from
  the journal so persisted/replayed `ActionFailed` triggers stay serializable.

**Determinism note (planning verification item):** the real-error path is a
live, in-process convenience used during the single synchronous `propagateError`
evaluation that runs at failure time. Confirm during planning that the engine is
snapshot-based (Commit persists the resulting `InstanceState`) and does NOT
re-run `Step(ActionFailed)` through `propagateError` on rehydrate — so matching
happens exactly once and the non-persisted error cannot cause replay
divergence. If any replay path exists, the Check closure must be documented as
"prefer vars/`err.Error()` for deterministic decisions." `ErrorExpr` (over vars
+ `_error` string) is fully deterministic and serializable regardless.

## Feature 3: rename example `boundary_timer` → `user_deadline`

`examples/scenarios/boundary_timer` demonstrates `activity.WithDeadline` on a
UserTask — not a boundary event; the name misleads.
- `git mv examples/scenarios/boundary_timer examples/scenarios/user_deadline`.
- Rewrite its package doc to frame it as a user-task deadline (`WithDeadline`)
  demo; drop "boundary" wording. No code/behavior change.
- Update the cross-reference note in `examples/scenarios/timer_boundary/main.go`
  to point to `user_deadline`.

## Feature 4: new scenario `boundary_action`

`examples/scenarios/boundary_action/main.go` — an interrupting timer boundary on
a UserTask that BOTH runs a fire-once notify action (`WithBoundaryAction`) AND
routes to an escalation path — the boundary-native equivalent of the
`user_deadline` (`WithDeadline`) example, demonstrating the unification:

```
start → approve[UserTask] ──(approved)──────────────────────────→ end-approved
             └─◄ timer "1h" (interrupting)
                  ├─ WithBoundaryAction("notify-overdue")   (fire-once side effect)
                  └─→ escalate[Service "reassign"] → end-escalated
```

Deterministic via `*clockwork.FakeClock` + `kernel.NewMemScheduler`
(`clk.Advance` + `sched.Tick`). Asserts the notify action ran, the human task was
cancelled, and the instance completed via the escalation path. The package doc
contrasts it with `user_deadline`.

## Feature 5: godoc `Example`s (library-consumed API)

Testable `Example`s in `definition/event` for `WithBoundaryAction` and for
`WithBoundaryErrorCheck` / `WithBoundaryErrorExpr`.

## Feature 6: split `WithDeadline` into two options (ADR-0103)

Today `activity.WithDeadline(duration, flowID, action string)` bundles three
concerns in one 3-arg option. Split it so the fire-once action is its own
option, symmetric with `WithBoundaryAction`, and the structural
duration+routing pair stays together:

- `WithDeadlineFlow(deadline, flow string)` — sets `DeadlineDuration = deadline`
  and `DeadlineFlow = flow` (the coupled pair: a deadline needs both a timer and
  where to route on breach).
- `WithDeadlineAction(action string)` — sets `DeadlineAction` (the optional
  fire-once breach action).

`WithDeadline` (the 3-arg form) is **removed** (breaking, pre-v1.0). Migration:
`WithDeadline("1h", "overdue", "notify")` → `WithDeadlineFlow("1h", "overdue"),
WithDeadlineAction("notify")`. All call sites updated (the `user_deadline`
example from Feature 3, and any tests/examples using `WithDeadline`). The
underlying model fields (`DeadlineDuration`/`DeadlineFlow`/`DeadlineAction`) and
wire form are unchanged — this is a surface-API split only.

## Feature 7: rename `WithReminder` → `WithWaitReminder` (ADR-0103)

`activity.WithReminder(every, action string)` schedules a recurring in-wait
reminder action during a wait. Rename to `WithWaitReminder(every, action string)`
to make the in-wait scope explicit and align with the deadline-option cleanup
(same signature, no behavior change). `WithReminder` is **removed** (breaking,
pre-v1.0). All ~11 call sites migrated (tests + the `inwait_reminder` example).
Model field names (`ReminderEvery`/`ReminderAction`) and the wire form are
unchanged — surface-API only.

## Deadline/boundary action semantic parity (ADR-0103)

`WithDeadlineAction` and `WithBoundaryAction` MUST share identical semantics:
fire-once (`InvokeAction{FireAndForget: true}`), result discarded, and on failure
log + continue routing. The deadline breach action is already `FireAndForget`
(`engine/step_timers.go` `handleDeadlineFired`), so this is a consistency
guarantee to lock in with an explicit assertion, not new behavior:

- A test asserts both paths emit an `InvokeAction` with `FireAndForget: true`
  (parallel assertions over the deadline-breach and boundary-fire commands).
- ADR-0103 documents that the two are the same mechanism (a deadline is a timer
  boundary with a bundled fire-once action), so future changes keep them in
  lockstep.

## Testing (strict TDD — red before green for every new symbol)

Boundary action:
- `WithBoundaryAction` sets `Action`; wire round-trips `boundaryAction`.
- `armBoundaries` captures `Action`; `fireBoundaryArm` emits a `FireAndForget`
  `InvokeAction` for interrupting AND non-interrupting, across trigger types.
- A failing boundary action still routes (relies on `FireAndForget`).

Flexible error matching:
- `WithBoundaryErrorCheck` sets `ErrorCheck`; `WithBoundaryErrorExpr` sets
  `ErrorExpr`; wire round-trips `boundaryErrorExpr` (and confirm `ErrorCheck` is
  NOT serialized).
- Engine: Check predicate deciding catch vs no-catch (including an
  `errors.As`-style type match on a real action error); Expr predicate over vars
  + `_error`; precedence Check → Expr → Code; a non-matching Check/Expr lets the
  error propagate to the next handler / incident.
- Runtime e2e: an action returns a typed error; a Check catches it and routes to
  recovery; a different instance whose vars fail the predicate propagates.

Deadline split + parity:
- `WithDeadlineFlow(deadline, flow)` sets `DeadlineDuration`+`DeadlineFlow`;
  `WithDeadlineAction(action)` sets `DeadlineAction` (via the existing
  `DeadlineOf` accessor).
- Parity assertion: the deadline-breach path and the boundary-fire path both
  emit `InvokeAction{FireAndForget: true}`.
- All ~19 existing `WithDeadline(...)` call sites (18 tests + the renamed
  `user_deadline` example) migrated to the two-option form and still green;
  empty-arg cases (`WithDeadline(dur, "", "")`) become `WithDeadlineFlow(dur, "")`
  with no `WithDeadlineAction`.

Examples:
- godoc `Example`s compile and their `// Output:` matches.

## Non-goals

- Boundary action: no result-merging (fire-once, discarded); no runtime change
  beyond the `ActionFailed` cause carrier.
- No refactor of `WithDeadline` to route through the new boundary-action path
  (ADR notes the equivalence only).
- No expr-lang variant for the boundary *action* (actions stay by-name/inline);
  the expr variant is only for error *matching*.
- Not modifying the existing `message_boundary` scenario.

## Verification checklist

- [ ] `WithBoundaryAction` + `BoundaryEvent.Action` + `NodeWire.BoundaryAction`;
      `fireBoundaryArm` emits the `FireAndForget` `InvokeAction` before routing
      (interrupting + non-interrupting, all trigger types); failing action still
      routes.
- [ ] `WithBoundaryErrorCheck` (non-serializable closure) + `WithBoundaryErrorExpr`
      (serializable) + existing `WithBoundaryErrorCode`, with precedence
      Check → Expr → Code, only on error boundaries.
- [ ] Live error threaded via a non-persisted `ActionFailed` cause carrier;
      bare-code sources pass a synthesized error; determinism item confirmed.
- [ ] `boundaryErrorExpr` serializes; `ErrorCheck` does NOT.
- [ ] `WithDeadline` split into `WithDeadlineFlow(deadline, flow)` +
      `WithDeadlineAction(action)`; 3-arg `WithDeadline` removed; all ~19 call
      sites migrated; deadline & boundary actions asserted `FireAndForget`-equal.
- [ ] `WithReminder` renamed to `WithWaitReminder` (same signature); ~11 call
      sites migrated.
- [ ] `user_deadline/` (renamed) + `boundary_action/` (new) build/run to expected
      outcomes; `timer_boundary` note updated.
- [ ] godoc `Example`s present and passing.
- [ ] ADR-0103 (boundary action + wait-option cleanup) + ADR-0104 (flexible error matching) written
      (Nygard template).
- [ ] `go build ./...`, `go test ./...`, `golangci-lint run ./...` clean; touched
      packages ≥ 85% coverage.

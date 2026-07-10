# Manual UserTask: completion mode + payload enforcement — Design

**Date:** 2026-07-10
**Status:** Approved
**Revises:** ADR-0118 (manual user task) — amended in place, no new ADR.

## Problem

ADR-0118 introduced a manual `UserTask` (`WithManual()`): a form-less human
checkpoint that parks and completes on a bare trigger. A whole-branch review
surfaced two gaps:

1. **The "no payload" promise is unenforced.** `TaskService.Complete` applies
   any `output map[string]any` to instance variables unconditionally. A caller
   can submit a payload to a manual task's completion and it is silently
   accepted — defeating the "someone confirmed this" auditability the manual
   task exists to provide. ADR-0118's own godoc claims payload-less as if it
   were an invariant.
2. **Only one completion mode.** A manual task always *waits* for a trigger.
   There is no way to express a pure documentation marker — a step that is
   recorded as "happened" but auto-completes without waiting (the closest BPMN
   analogue to a Manual Task with no execution semantics).

## Decisions (user-confirmed)

- **`WithManual(immediate bool)`** — the existing no-arg option gains a bool
  argument (no new option function). `immediate == false` → wait for a bare
  trigger (today's behaviour). `immediate == true` → auto-complete on entry.
- **Enforcement = reject with error.** Completing a *wait*-mode manual task
  with a non-empty output is rejected with a new sentinel `ErrManualTaskPayload`.
- **Immediate mode records an audit trail.** On entry the engine creates the
  human task and immediately marks it completed, then advances — so history
  still shows the manual step happened.

## API (definition/activity)

```go
// WithManual marks a UserTask as a manual task (a form-less human checkpoint).
// immediate selects the completion mode:
//   - false: the task parks and completes on a bare trigger (no payload). A
//     non-empty completion payload is rejected (ErrManualTaskPayload).
//   - true:  the task auto-completes on entry (a "documentation" marker); the
//     engine records a completed task for audit and advances without waiting.
// A manual task must not carry completion validation (ErrManualTaskValidation),
// regardless of mode. See ADR-0118.
func WithManual(immediate bool) UserTaskOption
```

`UserTask` fields:

```go
Manual          bool // marks a manual task
ManualImmediate bool // true = auto-complete on entry; false = wait for trigger
```

All existing `WithManual()` call sites migrate to `WithManual(false)`.

## Wire / YAML

`NodeWire` and `nodeYAML` gain `ManualImmediate bool`
(`json:"manualImmediate,omitempty"` / `yaml:"manualImmediate,omitempty"`)
beside the existing `manual`. UserTask `FromWire`/`ToWire` and `fromNodeYAML`
carry it. Both fields round-trip; `omitempty` keeps pre-existing definitions
unaffected.

## Build guard

Unchanged: `ErrManualTaskValidation` rejects any manual task (either mode) that
also carries completion validation. Immediate mode is still `Manual`, so it is
already covered.

## Engine

### Wait mode (default) — enforcement

`handleHumanCompleted` (engine/step_triggers.go) gains a check: when the
completed node is a manual task with `ManualImmediate == false` and the
trigger's `Output` is non-empty, return `ErrManualTaskPayload`
(`workflow-engine: manual user task cannot carry a completion payload`). The
engine is the single enforcement point because it holds the definition; the
error surfaces to the caller from `ProcessDriver.ApplyTrigger`. `TaskService`
is unchanged (it does not hold the node definition).

### Immediate mode — record then auto-complete

`userTaskStrategy.enter` (engine/step_nodes.go) detects
`Manual && ManualImmediate` and, instead of emitting `AwaitHuman`, records a
completed human task (audit) and advances the token immediately along the
node's outgoing flow — no wait, no payload, eligibility irrelevant (no actor
acts). The exact command/mechanism (reuse of the AwaitHuman + HumanCompleted
task lifecycle, collapsed) is settled during implementation, mirroring how a
normal user task's task record is created and completed.

## ADR-0118 revision (in place)

Amend ADR-0118:
- Decision: add the two completion modes and the `immediate` argument; add the
  `ErrManualTaskPayload` enforcement; note immediate mode records a completed
  task for audit.
- Supersede the "engine is unchanged / manual adds no engine-observable branch"
  statement — enforcement and immediate-mode auto-completion ARE engine
  behaviour. Keep the deliberate-divergence-from-BPMN framing.

## Testing (TDD strict)

- `WithManual(true/false)` sets `Manual` + `ManualImmediate` (option test).
- `ManualImmediate` JSON round-trip + YAML decode.
- Build guard still rejects manual + completion validation (both modes) —
  existing table extended if needed.
- Wait-mode: bare completion (nil output) drives to Completed (existing lock
  test, updated to `WithManual(false)`); non-empty output → `ErrManualTaskPayload`.
- Immediate-mode: driving a process with an immediate manual task reaches
  `StatusCompleted` without any external trigger, and the instance history
  shows a completed task for that node (audit).

## Example

Extend `examples/scenarios/manual_task` (or add a sibling block) to demonstrate
both modes: a wait-mode "hand over badge" step and an immediate-mode
"documentation" marker that auto-completes.

## Out of scope

- No new ADR (revise 0118).
- `TaskService`-level pre-rejection (engine is the single enforcement point).
- Any change to non-manual UserTask behaviour.

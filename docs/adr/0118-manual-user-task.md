# 118. Manual user task (form-less human checkpoint)

- Status: Accepted
- Date: 2026-07-10
- Amended: 2026-07-10 — added the two completion modes
  (`WithManual(immediate bool)`), wait-mode payload enforcement
  (`ErrManualTaskPayload`), and immediate-mode auto-completion. This
  supersedes the original "no engine change" statement. See
  `docs/specs/2026-07-10-manual-task-completion-mode-design.md`.

## Context

Some human steps in a process are not data-entry work items but simple
**acknowledgements**: "badge handed over", "goods physically inspected",
"call completed". The process must pause for a person, record that someone
confirmed the step, and then continue — but there is no form and no payload
to submit.

BPMN 2.0 defines a **Manual Task**, but it has *no execution semantics*: an
engine is free to auto-complete it, because by definition it happens outside
any system. That is not what we want. We want a durable "someone confirmed
this" checkpoint that actually **waits** for a trigger, so the acknowledgement
is auditable and can drive downstream routing, deadlines, and compensation
like any other node.

The `UserTask` machinery already parks an instance and waits for a completion
trigger. After [ADR-0117](0117-optional-usertask-eligibility.md), a UserTask
can also carry **no eligibility** (open gate, transport-level authorization).
The runtime additionally already accepts a completion with an **empty/nil
output** and, for an empty eligibility spec, authorizes a bare actor. So the
"park and wait for a bare acknowledgement" behaviour is reachable with the
existing engine — what is missing is a way to *mark* a task as manual and to
*reject* nonsensical configuration on it.

## Decision

Add a manual mode to `UserTask` rather than a new node kind:

1. **`Manual bool` + `ManualImmediate bool` on `UserTask`, set via
   `WithManual(immediate bool)`.** A manual task is a form-less human
   checkpoint with two completion modes:
   - **`immediate == false` (wait, default).** The task parks like any user
     task and completes on a **bare trigger** — no claim, no payload/form-data.
     Eligibility, if set, is still honoured (a manual task may be roleless,
     the common case).
   - **`immediate == true` (auto-complete).** On entry the engine records a
     **completed** human task for audit and advances the token immediately —
     no wait, no trigger, no payload. A "documentation" marker.

2. **Build-time guard.** A manual task (either mode) must not carry completion
   validation: it completes with no output, so a validation strategy would
   never receive input to check. The combination is rejected at
   authoring/`Validate` time with the sentinel `ErrManualTaskValidation`
   (`workflow-definition: manual user task cannot carry completion
   validation`). `definition/model` reads the flag via the wire projection
   (`toWire(n).Manual`) because it cannot import `definition/activity`.

3. **Wire support.** `Manual` and `ManualImmediate` round-trip on the JSON
   wire (`NodeWire`, `json:"manual,omitempty"` / `json:"manualImmediate,omitempty"`)
   and decode from YAML (`nodeYAML`, `yaml:"manual,omitempty"` /
   `yaml:"manualImmediate,omitempty"`; YAML is decode-only).

4. **Engine behaviour.** Two engine changes make the modes real:
   - **Wait-mode payload enforcement.** `handleHumanCompleted` returns the
     sentinel `engine.ErrManualTaskPayload`
     (`workflow-engine: manual user task cannot carry a completion payload`)
     when a wait-mode manual node is completed with a non-empty output. The
     guard runs strictly before any output is applied, so a rejected payload
     never mutates instance variables. The engine is the single enforcement
     point (it holds the definition); the error surfaces from
     `ProcessDriver.ApplyTrigger`.
   - **Immediate-mode auto-completion.** `userTaskStrategy.enter` detects
     `Manual && ManualImmediate` and, instead of emitting `AwaitHuman`,
     records a completed task in the instance's `Tasks` and advances along the
     single outgoing flow (leaving the token active so `drive` continues),
     mirroring a pass-through node. No eligibility check, no deadline/reminder/
     boundary arming (there is no wait period and no actor).

5. **Deliberate divergence from BPMN**, documented on the field: wait-mode has
   real execution semantics (it waits and records a completion) rather than
   BPMN's no-op auto-complete; immediate-mode is the closest analogue to a
   BPMN Manual Task (auto-completes) but still leaves a durable audit record.

## Consequences

- A form-less human checkpoint is expressible as
  `NewUserTask("confirm", WithManual(false))` (wait) or
  `NewUserTask("noted", WithManual(true))` (immediate) — a no-eligibility
  UserTask. This is why ADR-0117's optional eligibility was a prerequisite.
- New `manual` and `manualImmediate` fields are additive on both the JSON
  wire (omitempty) and YAML decode; pre-existing stored definitions are
  unaffected.
- Two failure modes now guard misuse: `ErrManualTaskValidation` (authoring
  time — manual + completion validation) and `ErrManualTaskPayload` (runtime —
  wait-mode manual completed with a payload).
- **This amendment supersedes the original "no engine change" statement.**
  Manual mode now has two engine-observable branches: wait-mode payload
  enforcement in `handleHumanCompleted`, and immediate-mode auto-completion in
  `userTaskStrategy.enter`. The earlier characterization — "a plain roleless
  UserTask would also complete on a bare trigger, so manual adds no engine
  branch" — no longer holds: wait-mode manual additionally rejects payloads,
  and immediate-mode manual does not park at all.
- Eligibility on a manual task is still honoured in wait mode, so a manual
  step *can* be restricted (e.g. `WithManual(false), WithEligibleRoles("operator")`)
  when the acknowledgement must come from a specific role. Immediate mode has
  no actor, so eligibility is not consulted.
- **Immediate-mode audit lives in the instance record, not the claimable task
  store.** An immediate manual task's completed record is appended to the
  instance's `Tasks` (durable in the instance store), but no `AwaitHuman`/
  `UpdateTask` command is emitted, so it never enters the `humantask.TaskStore`.
  This is intentional: the `TaskStore` holds *claimable / actionable* tasks,
  and an immediate task is never actionable. Consumers whose audit reporting
  reads the instance's `Tasks` see it; those reading only the claimable
  `TaskStore` will not.

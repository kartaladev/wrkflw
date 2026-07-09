# 118. Manual user task (form-less human checkpoint)

- Status: Accepted
- Date: 2026-07-10

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

1. **`Manual bool` field on `UserTask`, set via `WithManual()`.** A manual
   task parks the instance like any user task and completes on a **bare
   trigger** — no claim, no payload/form-data. Eligibility, if set, is still
   honoured (a manual task may be roleless, which is the common case).

2. **Build-time guard.** A manual task must not carry completion validation:
   it completes with no output, so a validation strategy would never receive
   input to check. The combination is contradictory and is rejected at
   authoring/`Validate` time with the sentinel `ErrManualTaskValidation`
   (`workflow-definition: manual user task cannot carry completion
   validation`). `definition/model` reads the flag via the wire projection
   (`toWire(n).Manual`) because it cannot import `definition/activity`.

3. **Wire support.** `Manual` round-trips on the JSON wire
   (`NodeWire.Manual`, `json:"manual,omitempty"`) and decodes from YAML
   (`nodeYAML.Manual`, `yaml:"manual,omitempty"`; YAML is decode-only).

4. **No engine change.** A characterization test
   (`runtime/manual_task_test.go`) locks that a manual, roleless UserTask
   drives to `StatusCompleted` on a bare, claim-less, payload-less trigger.
   It passes without any engine modification, proving the empty-spec authz +
   empty-output completion path already supports this.

5. **Deliberate divergence from BPMN**, documented on the field: unlike a
   strict BPMN Manual Task, this one has real execution semantics (it waits
   and records a completion) rather than auto-completing.

## Consequences

- A form-less human checkpoint is expressible as
  `NewUserTask("confirm", WithManual())` — a no-eligibility UserTask that
  waits for an acknowledgement. This is why ADR-0117's optional eligibility
  was a prerequisite.
- New `manual` field is additive on both the JSON wire (omitempty) and YAML
  decode; pre-existing stored definitions are unaffected.
- A new authoring-time failure mode (`ErrManualTaskValidation`) guards the
  contradictory `Manual` + completion-validation combination.
- The engine is unchanged: manual mode adds **no** engine-observable branch.
  A plain roleless UserTask would also complete on a bare trigger; what
  `Manual` adds is the semantic marker and the Build guard, not a distinct
  runtime path. Reviewers and consumers should not expect a manual-specific
  engine code path.
- Eligibility on a manual task is still honoured, so a manual step *can* be
  restricted (e.g. `WithManual(), WithEligibleRoles("operator")`) when the
  acknowledgement must come from a specific role.

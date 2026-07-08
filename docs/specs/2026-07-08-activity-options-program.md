# Activity-node options program — type-safe options + validation + completion-action

Date: 2026-07-08
Status: Program spec — Phase 3 fully designed, Phase 2 designed (separate spec), Phase 1 design PENDING
Scope: one coordinated program, executed as three ordered, individually-reviewable phases

## Why one program

Three changes all touch the activity-node **option system** (`definition/activity/options.go`,
`definition/event/options.go`, `model.ActivityFields`, wire/YAML). Per the user's decision they are
built as **one coordinated effort** so the new options are born into the type-safe scheme rather
than added loosely and refactored twice. To avoid the anti-pattern of one tangled diff, the program
runs as **three ordered phases**, each a clean commit series with its own review gate:

1. **Phase 1 — type-safe per-kind options refactor** (behavior-preserving foundation).
2. **Phase 2 — input validation** (born type-safe).
3. **Phase 3 — completion-action** (born type-safe).

Execute Phase 1 → 2 → 3 on a single program branch. `ProcessDriver.ReverseInstance`
(`docs/specs/2026-07-08-reverse-instance-design.md`) is a **separate** track — not part of this
program.

---

## Phase 1 — type-safe per-kind activity options (DESIGN PENDING)

**Goal:** each `WithX(...)` option satisfies only the option interfaces of the node kinds that
actually honor it, so mis-applying an option (e.g. `WithWaitReminder` on a node that never arms
reminders) is a **compile-time** error. This supersedes the runtime `definition.Lint` advisories
(`definition/lint.go`) that today only warn about ignored options.

**Starting points for the design session** (brainstorm this at the start of execution):
- Current pattern: shared options like `activityOnlyOption` implement `applyServiceTask`,
  `applyUserTask`, `applyReceiveTask`, … for *every* activity kind, so any option applies to any
  kind (loose). See `definition/activity/options.go`.
- `definition/lint.go` enumerates the "option ignored by this kind" advisories that this refactor
  removes — that list is the precise inventory of which options are valid on which kinds.
- The refactor likely narrows each option's return type to only the relevant `*Option` interfaces,
  and drops the no-op `apply*` methods that make an option a silent no-op today.

**Deliverable:** behavior-preserving — existing tests stay green; the `Lint` advisories it replaces
are removed; new compile-time rejection covered by `// want`-style compile-fail tests or documented
examples. **ADR-0113.**

**This phase must be designed (its own brainstorm) before implementation.** Phases 2 and 3 depend on
its final option-type shapes.

---

## Phase 2 — optional external-input validation (DESIGNED)

Full design: **`docs/specs/2026-07-08-input-validation-design.md`**. Summary: a neutral
`validation.Validator` port + `ValidationStrategy` provider interface + a registry; adapters
`validation/{expr,callback,jsonschema,avro}` (expr/callback dep-free; json-schema/avro ADR-gated
deps). Node-level validation at start (`StartEvent`), completion (`UserTask`), and message
(`ReceiveTask`/catch) boundaries — reject **before any state mutation**.

**Program adaptation:** the validation options (`WithInputValidation`, `WithCompletionValidation`,
`WithPayloadValidation`) are added **using Phase 1's type-safe option shapes** (each valid only on
its relevant kind). Otherwise unchanged from that spec. **ADR-0110** (architecture) + **ADR-0111**
(json-schema dep) + **ADR-0112** (avro dep).

**Ordering with Phase 3:** validation runs at the input boundary and gates the completion; the
completion action (Phase 3) runs only after a validated, accepted completion.

---

## Phase 3 — completion-action (DESIGNED)

### Goal

An optional node-attached action that runs when a node's **completion** is triggered — regardless of
the trigger source (human completion, message receive, …) — receiving the completion input, to
update external business/domain data (and optionally contribute computed variables).

### Mechanics (verified against the engine)

Completion is **synchronous** today (`handleHumanCompleted`, `engine/step_triggers.go` ~L436:
merge `Output` → advance token in one Step; `handleMessageReceived` is identical for `ReceiveTask`).
A completion action makes it **asynchronous via the existing action round-trip** — the same pattern
compensation already uses (`engine/step_compensation.go`), so **no new token state**:

1. On the completion trigger: `mergeVars(output)`, then if the node has a completion action, emit
   `InvokeAction{CommandID, Name: completionAction, Input: copyVars(merged vars)}`, park the token
   (`tok.State = TokenWaitingCommand; tok.AwaitCommand = cmdID`), cancel the node's timers/boundary
   arms, and **return without advancing**.
2. On the resulting `ActionCompleted`: the **existing** `handleActionCompleted` (`step_triggers.go`
   ~L42) finds the token by `CommandID`, merges the action's return vars, advances along the single
   outgoing flow, and drives forward. No change needed there.

### Attach point (shared across completion-triggered activity kinds)

- `model.ActivityFields.CompletionAction string` — beside `CompensationAction`/`CancelHandler`
  (`definition/model/node.go` ~L68). Inherited by UserTask, ReceiveTask, and the other activity
  kinds uniformly.
- `activity.WithCompletionAction(name string)` — mirrors `WithCompensation`
  (`definition/activity/options.go` ~L128). **Built with Phase 1's type-safe shape** — valid only on
  the completion-triggered kinds (UserTask, ReceiveTask; not ServiceTask, which completes via its own
  action).
- Wire/YAML: `NodeWire.CompletionAction` + `PutActivity`/`Activity` round-trip
  (`definition/model/node_wire.go` ~L34), exactly like `CompensationAction`.
- `engine/node_accessors.go`: `completionActionOf(n model.Node) string`, mirroring
  `compensationActionOf`.

### Semantics

- Runs **after** `mergeVars(output)` (and after Phase 2 validation, if any) — so the action sees the
  final, accepted, validated completion state.
- Uses the `action.Action` catalog (`Do(ctx, in) (out, error)`), resolved by name; its returned map
  **merges into instance variables** (like a service task), so it can both write domain data and
  contribute computed vars.
- **Failure semantics = reuse the existing machinery** (recommended default, not new semantics): the
  parked token behaves like a service-task action, so the node's `RetryPolicy` governs retries, a
  terminal failure raises an **incident** (admin-resumable via `ResolveIncident`) or routes to an
  **error boundary** if one is attached. `WithRetryPolicy` / error-boundary on the UserTask therefore
  govern the completion action for free.
- Catalog-scope limitation (same as compensation/deadline/reminder, documented): a completion action
  resolves against the root definition's scoped catalog + global, not nested scoped catalogs.

### Engine change size

~150 lines, no new token state, no breaking changes (new optional field). The only real edits are
the completion-action branches in `handleHumanCompleted` and `handleMessageReceived`.

### Tests (TDD)

- Completion action success advances the token and merges its output.
- Completion action failure → retry (per node `RetryPolicy`) → incident / error-boundary routing.
- Works for both UserTask (human completion) and ReceiveTask (message completion).
- Wire/YAML round-trip of `CompletionAction`.
- Interaction with a boundary event on the host (boundary arms cancelled when the completion action
  is invoked).

### Example & ADR

`examples/scenarios/completion_action/` — a UserTask whose completion action updates a "domain
record" (recorded via a fake action) from the completion input. **ADR-0114.**

---

## ADR allocation (program)

- ADR-0110 — validation architecture (Phase 2).
- ADR-0111 — json-schema dependency (Phase 2).
- ADR-0112 — avro dependency (Phase 2).
- ADR-0113 — type-safe per-kind options refactor (Phase 1).
- ADR-0114 — completion-action (Phase 3).

(ReverseInstance holds 0109, separately.)

## Execution note

One program branch, three phases in order (1 → 2 → 3), each via
`superpowers:writing-plans` → `superpowers:subagent-driven-development` with a review gate between
phases. Phase 1 requires a design brainstorm first (see its section). A fresh session should read
this spec top-to-bottom, design Phase 1, then execute all three.

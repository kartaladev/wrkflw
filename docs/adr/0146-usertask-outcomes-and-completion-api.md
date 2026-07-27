# 146. User-task outcomes, completion API refactor, outcome→variable exposure

Status: Accepted — 2026-07-27. Spec:
[docs/specs/2026-07-27-processinstance-audit-view-and-idgen.md](../specs/2026-07-27-processinstance-audit-view-and-idgen.md).
Part of the ADR-0144…0149 delivery. Related: manual-task payload rule
[ADR-0118].

## Context

A human task can end with a business **outcome** (approve / reject / revise) that
both records intent and drives downstream routing. Today there is **no** notion of
an outcome anywhere: `httpcore.CompleteInput{Actor, Output}` →
`service.CompleteTaskRequest{TaskToken, Actor, Output}` →
`TaskService.Complete(ctx, token, actor, output)` →
`engine.HumanCompleted{TaskToken, Output, Actor}` (`trigger.go:108`). The only
carrier is `Output map[string]any`, so an outcome and an actor note could only be
smuggled in by convention — defeating validation and reliable audit.

The library already uses **`outcome`** for a terminal disposition
(`TerminationOutcome`, `OutcomeComplete`/`OutcomeAbort`, `event.go:50`) and
**`kind`**/`state` as its discriminator words — never `type`.
`handleHumanCompleted` (`step_triggers.go:519`) already has the node definition in
scope (`humanTdef.Node`) and already `mergeVars(t.Output)`.

## Decision

1. **Declare accepted outcomes.** User tasks gain an optional `Outcomes []string`
   (wire/YAML key `outcomes`, snake_case per ADR-0144), round-tripped through
   `NodeWire`/`nodeYAML` and validated structurally. Empty = unconstrained.
2. **Fail-closed validation in the engine.** In `handleHumanCompleted`, reject a
   completion whose `outcome` is non-empty and not in the node's `outcomes` with a
   new sentinel `ErrInvalidOutcome` (`"workflow-engine: …"` prefix), beside the
   existing `ErrManualTaskPayload`. No path — including durable replay — can
   bypass it. Empty `outcomes` accepts any/no outcome.
3. **Completion API refactor** via an engine value object, threaded through all
   four layers:
   ```go
   type Completion struct { Outcome string; Note string; Output map[string]any }
   func NewHumanCompleted(at time.Time, taskToken string, c Completion, actor authz.Actor) HumanCompleted
   ```
   `HumanCompleted` gains `Outcome`, `Note`; `TaskService.Complete(ctx, token,
   actor, Completion)`; `service.CompleteTaskRequest` gains `Outcome`, `Note`;
   `httpcore.CompleteInput` gains `outcome`, `note`. The durable trigger envelope
   (`trigger_codec.go`) gains **additive omitempty** fields — backward-safe for
   already-persisted commands. Claim/reassign APIs are unchanged.
4. **Outcome → variable exposure (hybrid opt-in).** The outcome is always recorded
   as audit. Exposure as a process variable is opt-in on the node, resolved after
   the `Output` merge with explicit-name precedence:
   ```
   if node.OutcomeVariable != "":  vars[node.OutcomeVariable] = outcome
   else if node.ExposeOutcome:      vars[node.ID + "_outcome"] = outcome
   else:                            (audit-only)
   ```
   The exposed value is the **outcome string** (`"approve"`); gateway conditions
   compare strings. Two new optional user-task fields: `ExposeOutcome bool` (wire
   `expose_outcome`) and `OutcomeVariable string` (wire `outcome_variable`).

## Consequences

- **Positive:** outcome and note are first-class, validated, and auditable; the
  vocabulary (`outcomes` → `completion.outcome` → `expose_outcome` /
  `outcome_variable`) is one family, aligned with `TerminationOutcome`.
- **Positive:** routing is decoupled from audit; consumers opt in per node and
  name the variable, or route explicitly via `Output` — library-flexible.
- **Negative:** `NewHumanCompleted`'s signature changes (value object), churning
  ~1 prod + ~20 test callsites plus the `trigger_codec` encode/decode.
- **Interaction:** a manual wait-mode user task (`Manual && !ManualImmediate`)
  still rejects a non-empty `Output` (ADR-0118). The guard at
  `step_triggers.go:539` currently checks only `len(Output) > 0`; it is **extended
  to also reject a non-empty `outcome`/`note`, before** the outcome-set validation —
  otherwise a manual task (which declares no `outcomes`) would fail-**open** and
  silently record them (rule-#9 audit M5).
- **Edge:** if `Output` and the exposed outcome target the same variable, the
  outcome projection (applied after `Output`) wins for that key; `completion.outcome`
  audit is recorded independently and is never overridden.

## Implementation amendments (2026-07-28, code review)

### 1. A declared outcome set makes the outcome MANDATORY

Decision item 2 said the engine rejects a completion whose `outcome` is
**non-empty** and not in the node's declared set. Code review showed that leaves
the set half-enforced: a node declaring `WithOutcomes("approve","reject")`
together with `WithExposeOutcome()` could still be completed with a bare
`CompletionInput{}`. The guard passed (the outcome was empty),
`applyOutcomeExposure` returned early, no routing variable was published, and a
downstream exclusive gateway with no default flow failed the step with
`ErrNoMatchingFlow` — an authoring-time-detectable misconfiguration surfacing as
an opaque runtime failure.

**Amendment:** when `len(Outcomes) > 0`, an empty outcome is rejected with a new
sentinel **`engine.ErrOutcomeRequired`** (`"workflow-engine: user task requires a
completion outcome"`). The guard is now an explicit four-arm switch: manual task
(exempt), no declared set (unconstrained), empty outcome (`ErrOutcomeRequired`),
value outside the set (`ErrInvalidOutcome`).

`ErrOutcomeRequired` is a **distinct** sentinel rather than a wrap of
`ErrInvalidOutcome`: the two are different caller mistakes with different
remedies — "you sent a value I don't accept" (show the declared set) versus "you
sent nothing" (require the field). Wrapping would make
`errors.Is(err, ErrInvalidOutcome)` true for a completion that carried no outcome
at all, which is simply false.

**Manual tasks are exempt.** A manual task is forbidden from declaring outcomes at
authoring time (`model.ErrManualTaskOutcome`) and completes on a bare trigger, so
it can never be required to supply one. Without the explicit exemption arm, a
manual task carrying a validation-forbidden outcome set would have been newly
rejected on its own bare completion.

### 2. Exposure requires a declared set (authoring-time)

`validateOutcomes` did not require a non-empty `Outcomes` when `ExposeOutcome` or
`OutcomeVariable` was set, so `activity.NewUserTask("approve",
activity.WithExposeOutcome())` was a valid definition in which any free-text
string an actor sent became a process variable. Because process variables can
feed a later node's `EligibleExpr`, that is a footgun worth closing at Build time.

**Amendment:** exposure now requires a non-empty `Outcomes`, enforced by
**`model.ErrOutcomeExposureWithoutOutcomes`** (`"workflow-definition: outcome
exposure requires a declared outcome set"`). Publishing a completer-supplied
value into the variable space demands a declared, closed value domain. Manual
tasks are excluded from this rule too — `ErrManualTaskOutcome` already diagnoses
them precisely, and telling an author to declare a set they are forbidden from
declaring would be contradictory advice.

`engine.Step` does **not** re-validate the definition, so `applyOutcomeExposure`
keeps its empty-outcome guard for the unvalidated-definition path.

### 3. Both outcome sentinels are 4xx, not 5xx

`httpcore.ClassifyError` listed neither sentinel, so both fell through to the
`default` arm — HTTP 500 with an empty body. That was pre-existing for
`ErrInvalidOutcome`, but amendment 1 makes it far likelier to be hit: any client
omitting `outcome` on an outcome-declaring node would have received an opaque 500.
Both are now classified `400 bad_request` with the error text, since both describe
a payload the caller can correct.

### 4. `engine.Completion` renamed to `engine.CompletionInput`

The delivery shipped two exported types named `Completion` — `engine.Completion`
(the payload a caller submits) and `humantask.Completion` (the persisted audit
record) — with overlapping `Outcome`/`Note` fields. A file importing both had to
disambiguate, and neither name said which was the request and which the record.

**Amendment:** the input side is **`engine.CompletionInput`**. The record side
keeps `humantask.Completion`. This follows the repo convention of one name per
concept across Go/JSON/SQL; there is no deprecation alias (pre-v0.1.0 hard-rename
convention, ADR-0098/0107/0108/0141).

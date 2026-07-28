# Empty identity keys must match no record

**Status**: designed, pending implementation
**Date**: 2026-07-28
**ADR**: 0152
**Scope**: the `engine` package, one `definition/model` authoring rule, and one
`transport/http/httpcore` error-classification arm. No persistence, runtime, or
scheduler change.

## Problem

`InstanceState`'s lookup and sweep helpers match a caller-supplied key against a
field on every stored record. None of them treat the empty string specially, so
when a caller passes `""` the helper matches **every record whose field is also
empty** — turning a lookup into a wildcard.

This is not hypothetical. It is a live defect today, and it is reachable from
outside the module.

### The live defect (tier 1)

`cancelTimersByTaskID(taskID, excludeTimerID)` (`engine/state_timers.go:55`)
matches `tr.TaskID == taskID`. Of the three timer-record creation sites, exactly
one leaves `TaskID` unset:

| Creation site | Kind | `TaskID` |
|---|---|---|
| `engine/step_triggers.go:302` | `TimerRetry` | **unset — `""`** |
| `engine/step_nodes.go:586` | `TimerInWait` | `cancelKey` (`tok.ID` or `taskID`, never empty — call sites `step_nodes.go:103,682,745`) |
| `engine/step_nodes.go:659` | `TimerDeadline` | `taskID`, never empty |

So `cancelTimersByTaskID("")` selects **every `TimerRetry` record in the
instance**, regardless of scope.

`cancelTokenWaits` (`engine/step_cancel.go:15`) calls it with
`tok.AwaitCommand`, which is empty for any token not parked on a command — an
active token, or one parked on a signal/message (which use `AwaitSignal` /
`AwaitMessage` instead). `cancelTokenWaits` runs in a per-token loop from four
sweep sites, of which **three** can do damage:

- `engine/step_errors.go:393` — error propagation cancelling a scope
- `engine/step_boundaries.go:146` — interrupting boundary event on a host token
- `engine/step_eventsubprocess.go:201` — interrupting event sub-process
- `engine/step_compensation.go:226` — compensation walk. **Harmless**:
  `beginCompensation` calls `s.cancelAllTimers()` three lines later
  (`step_compensation.go:229`), so every timer dies regardless.

**Failure scenario.** An instance has two parallel branches in different scopes.
Branch A is mid-retry, holding a `TimerRetry` record. Branch B's scope is
cancelled (error, boundary interrupt, or compensation). The sweep reaches a
token in B whose `AwaitCommand` is empty; `cancelTimersByTaskID("")` deletes
A's retry record and emits `CancelTimer` for it. Branch A is **not** being
cancelled, but its timer is gone from both the state and the scheduler. Its
token stays `TokenWaiting` forever. The instance never completes and never
fails — it silently wedges.

Note that the task-keyed sweep contributes nothing legitimate in
`cancelTokenWaits` **at all** — not merely when the key is empty. All three
`timerRecord` creation sites set `Token: tok.ID` (`step_triggers.go:305`,
`step_nodes.go:589`, `:662`), so the `cancelTimersForToken(tok.ID, "")` call on
the next line already removes every timer the task-keyed sweep could find,
including in the human-task case. Deleting the call is therefore a live
alternative to guarding it. This spec **keeps the call and adds the guard**: the
guard is required anyway for the other two call sites
(`step_triggers.go:656`, `step_timers.go:94`), and deleting the call would
change the order of emitted `CancelTimer` commands, which existing tests may
depend on. The redundancy is recorded as a follow-up, not fixed here.

### Externally reachable variants (tier 2)

`wrkflw` is a library. Consumers construct `Trigger` values and call `Step`
directly, so a malformed trigger is an ordinary input, not an internal
invariant. No handler validates its identity key:

**Dispatch order matters.** For `TimerFired` and `SignalReceived` the *arm
cascade* runs before any token lookup (`step_triggers.go:482-486` and
`:720/:733/:746`), so combined with tier-1b below, an empty key matches an arm
first and the token path is never reached.

| Trigger | Field | Consequence of `""` |
|---|---|---|
| `TimerFired` | `TimerID` | `dispatchArmCascade` (`step_triggers.go:482-486`) matches an all-empty arm → `fireBoundaryArm` **interrupts a live host activity**. Only if no arm matches does it reach `tokenAwaiting("")` (`:511`) and advance an arbitrary unparked token (`:519-536`) |
| `SignalReceived` | `Name` | arm branches at `:720/:733/:746` match first; otherwise `tokenIDsAwaitingSignal("")` (`:714`) returns **every token not awaiting a signal** and broadcast resumes them all |
| `MessageReceived` | `Name` | `tokenAwaitingMessage("", "")` (`:921`) matches a token not awaiting a message — **only when `CorrelationKey` is also empty**, since both components must match. Shadowed on the product path: `runtime` `DeliverMessage` (`processdriver_message.go:57-60`) already no-ops an empty name, so this is engine-direct only |
| `ActionCompleted`, `ActionFailed`, `SubInstanceCompleted`, `SubInstanceFailed` | `CommandID` | `tokenAwaiting("")` (`:88,265,801,832`) returns an arbitrary unparked token |

`SignalReceived` **is** reachable on the product path: `service.DeliverSignal`
(`service/service.go:362`) passes `req.Signal` straight to `ApplyTrigger` with no
guard, unlike `runtime.BroadcastSignal`.

**Two exported entry points bypass `Step` entirely** and are therefore covered by
the helper layer only — they never emit a sentinel:

- `engine.TargetNode` (`target_node.go:15`, `case HumanCompleted` → `tokenAwaiting(t.TaskID)` at `:41`). `runtime.ProcessDriver.validateInput` (`processdriver.go:793-806`) calls it and **fails open**, so an empty `TaskID` today runs an *arbitrary* node's `ValidationStrategy` against the payload.
- `engine.FailingActionName` (`failing_action.go:16-17`) → an arbitrary token's node → the wrong `OverrideRetryPolicy` (ADR-0126).

The `Human*` triggers and `ResolveIncident` are safe today, but **not** for the
reason first recorded. `handleHumanCompleted`'s *first* lookup is
`s.tokenAwaiting(t.TaskID)` (`step_triggers.go:594`), which **does** wildcard-match
an arbitrary token; safety comes only from the *second* lookup,
`s.TaskByID(t.TaskID)` (`:602`), failing. The error returned there is
`humantask.ErrTaskNotFound` (`:604`) — not `ErrTokenNotFound`.

### The arm lookups are tier-1 class, not latent (tier 1b)

The three arm families (`armedEvent`, `boundaryArm`,
`eventTriggeredSubprocessArm`) share the embedded `triggerMatch` quartet. The
comment at `engine/state_arms.go:11-12` says *"At most one of the four fields is
non-empty for a given arm (timer XOR signal XOR message)"*, but the **real**
invariant is weaker in both directions, and the difference is load-bearing:

- A **message** arm sets *both* `Message` and `MessageKey`, so "at most one of
  the four" is literally false as written.
- More importantly, **an arm may carry no non-empty match field at all.**
  `armBoundaries` (`step_boundaries.go:38-70`) sets `TimerID`/`Signal`/`Message`
  in an `if / else if / else if` chain, so an **error boundary** — no timer, no
  signal, no message — is appended at `:70` with all four fields empty. The same
  holds for an `armedEvent` on a catch node with no timer/signal/message
  (`step_nodes.go:858-886`).

So the correct premise is: **no arm is guaranteed to carry a non-empty match
field.** The generic lookups match a single field, so:

| Call | Wrongly matches |
|---|---|
| `armBySignal(arms, "")` | the first timer, message, **or error-boundary** arm |
| `armByTimer(arms, "")` | the first signal, message, or error-boundary arm |
| `armByMessage(arms, "", "")` | the first arm with both `Message` and `MessageKey` empty — timer, signal, or error-boundary, whichever comes first in slice order |

This is **worse than a lookup miss**. `handleSignalReceived:733` and
`handleTimerFired:484` feed the returned arm straight to `fireBoundaryArm`,
which interrupts a live host activity and routes it down the error path. An
empty signal name can therefore corrupt a running instance's control flow.

It applies to all nine wrapper methods (`armedEventBy*`, `boundaryArmBy*`,
`eventTriggeredSubprocessArmBy*`). Both layers are required: the arm lookups are
also called internally during boundary/gateway resolution, not only from trigger
dispatch.

### Latent variants (tier 3)

`tokenByID`, `TaskByID`, `timerByID`, `removeTimer`, `removeToken`,
`cancelTimersForToken`, `removeBoundaryArmsForHost`,
`removeArmedEventsForGateway`, and `openVisitFor` are safe **only because** no
stored record currently leaves the matched field empty. That is precisely the
property `TimerRetry` violated to create tier 1. Nothing in the code prevents the
next record kind from reopening the hole.

(`scopeOfToken` is **not** in this list — it is guarded by delegation, see the
inheritance table below. `lastCompensationRecordByNode`
(`step_compensation.go:503`) is likewise unguarded but safe: its only caller
(`:625`) sits inside a `toNode != ""` branch. `eligibleRange`
(`step_compensation.go:48-58`) already carries an explicit `if toNode != ""`
guard — the in-repo precedent this rule generalizes.)

## Decision

Adopt one rule, stated in ADR-0152:

> In the `engine` package, an **identity key** names one specific record. The
> empty string is the *absence* of an identity, and an absent identity matches
> **no** record.

### Exemptions: keys where empty is meaningful

Five parameters are deliberately **not** identity keys. Each already has
documented "empty means something" semantics; the rule must not touch them.
An over-broad fix that guards these would break scope resolution, message
correlation, and two public constructors.

| Key | `""` means | Documented at |
|---|---|---|
| `scopeID` | the **root scope** — a real, matchable scope | `engine/state_timers.go:22-24` |
| `correlationKey` | **uncorrelated** — must keep matching a token/arm that is itself uncorrelated | `engine/step_state.go:106-109` |
| **`excludeTimerID`** | **exclude nothing** — the second parameter of `cancelTimersByTaskID` and `cancelTimersForToken` | `engine/state_timers.go:51-55,69-75` |
| **`setVisitTask`'s `taskID`** | the **value being written**, not a lookup key | `engine/step_state.go:136` |
| `StartInstance.StartNodeID` | resolve the definition's **manual start** | `engine/trigger.go:28-31` |
| `CompensateRequested.ToNode` | **roll back everything** | `engine/trigger.go:323-325` |
| `CompensateRequested.ReverseNode` | terminate rather than resume | `engine/trigger.go:326-329` |

`excludeTimerID` is the highest-risk entry here: it is literally a `TimerID`, it
sits **inside the two functions this change edits**, and five of its seven call
sites pass `""` (`step_triggers.go:656,776,934`, `step_cancel.go:15,20`).
Applying the rule to it would invert its meaning and cancel the very timer the
caller is currently handling.

`HumanReassigned.From`/`To` are actor ids, not record-lookup keys, and are out of
scope.

### Where the guard lives

**In the state-layer helpers, not the call sites.** The helpers are few and are
where the matching semantics actually live; call sites number in the dozens and
grow with every feature. Guarding call sites fixes today's four sweeps and
leaves the next caller to rediscover the trap.

**On an empty key a helper returns its zero result** — `nil` for pointer
lookups, `nil`/empty slice for sweeps. Not an error, not a panic. Their contract
is already "nil if not found", every caller handles nil, and threading errors
through the internal call graph buys nothing over the natural not-found path.

### Trigger-boundary validation

The helper guard alone downgrades a malformed trigger to a silent no-op. For a
library that is poor ergonomics: the consumer gets no signal that their trigger
was rejected. So `Step` additionally **validates identity keys on inbound
triggers** and rejects an empty one with a new exported sentinel:

```go
// ErrEmptyTriggerKey reports a trigger whose identity key is empty. An identity
// key names one specific record; the empty string names none, so the trigger
// cannot be dispatched.
var ErrEmptyTriggerKey = errors.New("workflow-engine: trigger identity key is empty")
```

Validated (11): `SignalReceived.Name`, `MessageReceived.Name`,
`ActionCompleted.CommandID`, `ActionFailed.CommandID`,
`SubInstanceCompleted.CommandID`, `SubInstanceFailed.CommandID`,
`HumanCompleted.TaskID`, `HumanClaimed.TaskID`, `HumanReassigned.TaskID`,
`HumanCandidatesResolved.TaskID`, `ResolveIncident.IncidentID`.

Not validated (see exemptions): `StartInstance`, `CompensateRequested`,
`CancelRequested`, and `MessageReceived.CorrelationKey`.

**`TimerFired.TimerID` is deliberately NOT validated.** `TestTimerFiredStaleTokenIsNoop`
(`engine/step_timers_test.go:113-157`) pins an explicit `{name: "empty timerID"}`
case asserting `require.NoError(t, err, "stale TimerFired must not error")`, with
the intent documented at `:109-112`: *"timers are inherently racy with other
completion paths, and a stale TimerFired must never corrupt state or return an
error (unlike HumanCompleted which fails fast on an unknown token — timers can
arrive late)."* That is a deliberate contract, and the helper guards already give
it exactly what it asks for: an empty `TimerID` becomes a clean no-op. Rejecting
it would break a documented behaviour to gain nothing. Where the two layers
conflict, the existing contract wins.

Because a `Trigger` variant added later would silently fall through the
validator's `default` arm, the validated and exempt sets are **declared** and
cross-checked by an exhaustiveness test, following the established
`AllTriggerKinds` precedent (`internal/persistence/store/trigger_codec.go:33`
with `trigger_codec_test.go:185`).

### Behaviour changes this introduces

The sentinel classifies as **400**. Contrary to the first draft of this spec,
there *is* existing behaviour being changed, and it is wire-visible:

| Trigger with an empty key | Today | After |
|---|---|---|
| `HumanCompleted.TaskID` | `humantask.ErrTaskNotFound` → **404** | **400** |
| `HumanClaimed`, `HumanReassigned`, `HumanCandidatesResolved` | `ErrTokenNotFound` (wraps `ErrInvalidTransition`) → **422** | **400** |
| `ResolveIncident.IncidentID` | documented idempotent no-op (`step_triggers.go:977-980`) → **200** | **400** |

The `ResolveIncident` change is the debatable one. It is kept — an admin call
with a missing incident id is a malformed request, not a race, and the
idempotency contract exists to absorb *unknown* ids, not absent ones. This is a
deliberate reversal and is recorded in the ADR's Consequences.

## Design

### Component 1 — helper guards (`engine/state_*.go`, `engine/step_state.go`)

Add an early `if key == "" { return <zero> }` to each identity-keyed helper:

- `state_timers.go`: `timerByID`, `removeTimer`, `cancelTimersByTaskID`,
  `cancelTimersForToken`
- `state_arms.go`: the three **generic** lookups `armByTimer`, `armBySignal`,
  `armByMessage`, plus `removeArmedEventsForGateway` and
  `removeBoundaryArmsForHost`
- `step_state.go`: `tokenAwaiting`, `tokenByID`, `tokenIDsAwaitingSignal`,
  `tokenAwaitingMessage`, `removeToken`, `openVisitFor`
- `state.go`: `TaskByID`

**Sixteen guards in four files.** Everything else inherits, and must NOT be
guarded separately:

| Helper | Inherits from | Why no own guard |
|---|---|---|
| the nine `armedEventBy*` / `boundaryArmBy*` / `eventTriggeredSubprocessArmBy*` wrappers | `armByTimer` / `armBySignal` / `armByMessage` | each is a one-line delegation to a generic |
| `scopeOfToken` (`target_node.go:85`) | `tokenByID` | delegates its only lookup |
| `messageTargetNodeScoped` (`target_node.go:67`) | the four message lookups | delegates every tier |
| `setVisitTask` (`step_state.go:136`), `closeVisit` (`step_state.go:206`), `closeVisitAs` (`close_kind.go:54`) | `openVisitFor` | each delegates its only lookup |
| `consumeTokenAs` (`close_kind.go:64`), `moveTokenToTargetAs` (`step_state.go:221`) | `openVisitFor` / `removeToken` | delegate |
| `TargetNode` (`target_node.go:15`), `FailingActionName` (`failing_action.go:16`) | `tokenAwaiting` | exported, bypass `Step` — helper layer is their **only** defence |

This list is illustrative, **not closed** — any helper whose lookup is a single
delegation inherits. `target_node.go`, `close_kind.go`, and `failing_action.go`
are **not modified**.

Note `setVisitTask`'s key pair is `(tokenID, nodeID)`; its third argument
`taskID` is the value being written, **not** a key, and must not be guarded.

`handleActionCompleted` (`step_triggers.go:84`) and `handleActionFailed` (`:261`)
compare `t.CommandID` against `s.Compensating.ActiveCmdID` **before** reaching any
guarded helper. On that branch the trigger validator is the *sole* defence —
the one place where the layering is reversed.

For the two-component message lookups (`armByMessage`, `tokenAwaitingMessage`,
`messageTargetNodeScoped`), guard **only the `name` component**.
`correlationKey` keeps its current matching behaviour.

`removeEventTriggeredSubprocessArmsForScope`, `tokensInScope`, `closeScope`,
`archiveCompensations`, and `scopeByID` are **unchanged** — scope keys are exempt.

### Component 2 — reject an unnamed `ReceiveTask` at authoring time

`receiveTaskStrategy.enter` (`engine/step_nodes.go:97-99`) sets
`tok.AwaitMessage = rt.MessageName` **unconditionally** — unlike
`intermediateCatchEventStrategy` (`:720,:725`) and `armBoundaries` (`:60,:62`),
which both guard `!= ""`. `NewReceiveTask(id, "")`
(`definition/activity/activity.go:170`) is accepted and `model.Validate` has no
rule against it, so such a token parks with `AwaitMessage == ""`.

Today that token is resumable through the library's headline API via
`Step(NewMessageReceived(at, "", "", payload))`. After Component 1 it would be
**permanently unresumable** — a token stranded in a shape the definition layer
still permits. (`InstanceState.MessageWaiters()` at `state_waiters.go:111`
already skips empty `AwaitMessage`, so the runtime-mediated path is dead
already.)

Shipping the guard without closing this would knowingly introduce a token-leak.
So `model.Validate` gains a rule rejecting a `ReceiveTask` whose `MessageName` is
empty, with a new `ErrEmptyMessageName` sentinel.

**Implementation constraint:** `definition/model` **cannot import
`definition/activity`** (import cycle). The rule must therefore match on
`n.Kind() == KindReceiveTask` and read `toWire(n).MessageName`, exactly as the
neighbouring `ErrPayloadValidationRequiresMessage` rule does
(`definition/model/validate.go:566-573`). A type assertion to
`activity.ReceiveTask` will not compile.

### Component 3 — trigger validation (`engine/step.go`)

A `validateTriggerKey(Trigger) error` helper, called once at the top of `Step`
before dispatch, switching over the trigger types listed above. Returning early
keeps every handler free of per-handler guards and makes the validated set
readable in one place.

### Data flow

Unchanged. The guards are pure predicates on existing lookups; no new state, no
new commands, no schema change. `Step` remains a pure function.

### Error handling

- Helpers: no errors — an empty key becomes an ordinary not-found.
- `Step`: returns `ErrEmptyTriggerKey` wrapped with the trigger's concrete type
  name, before any state mutation, so a rejected trigger leaves the instance
  untouched.

## Testing

Hot-path-first (Golang rule #8): the token-execution step loop, retry
arm/fire, and trigger dispatch are the paths production traffic exercises.

1. **Tier-1 regression (the wedge).** Two parallel branches in *different*
   scopes; branch A mid-retry with a live `TimerRetry`; cancel branch B's scope
   via error propagation. Assert A's timer record survives, no `CancelTimer` is
   emitted for A's timer, and A is still resumable. **Fails before the fix.**
2. **Tier-2 regressions**, one per validated trigger: an empty-key trigger
   returns `ErrEmptyTriggerKey`. The `SignalReceived` case additionally asserts
   at `Step` level that no unrelated parked token advanced. **Fail before the fix.**
3. **Tier-1b regression (arm cross-matching).** Fixtures must include an
   **error-boundary arm** (all four match fields empty), not only timer/signal/
   message arms — that is the shape that makes an empty key interrupt a live
   activity. Assert `armBySignal("")`, `armByTimer("")`, and
   `armByMessage("", "")` each return nil. **Fail before the fix.**
4. **Invariant lock — every fixture MUST contain a record holding the empty
   value.** This is the failure mode of the obvious implementation: a test that
   sweeps with `""` over a fixture whose records all carry non-empty keys passes
   *before and after* the guard, and would keep passing if the guard were later
   deleted. Such a test is worthless both as TDD evidence and as an invariant
   lock. Concretely, each empty-key case needs a planted record — a
   `timerRecord{TimerID: ""}`, an `armedEvent{GatewayToken: ""}`, a
   `Token{ID: ""}`, a `NodeVisit{TokenID: ""}` — so it genuinely reproduces the
   wildcard.
5. **Exhaustiveness pin.** Declared validated/exempt trigger sets cross-checked
   against the sealed `Trigger` variants, so a new variant fails the build rather
   than silently falling through `default`. Model:
   `internal/persistence/store/trigger_codec_test.go:185`.
6. **Exemption tests** — the tests that catch an over-broad fix. Each must assert
   *behaviour*, not merely that a validator returned nil:
   - `removeEventTriggeredSubprocessArmsForScope("")` still resolves root-scope arms.
   - `tokensInScope("")` still counts root tokens.
   - `tokenAwaitingMessage("m", "")` still matches an uncorrelated token.
   - `armByMessage(arms, "m", "")` still matches an uncorrelated message arm.
   - `cancelTimersByTaskID("h1", "")` with `excludeTimerID == ""` still cancels
     **all** of `h1`'s timers — the `excludeTimerID` exemption.
   - `Step(NewStartInstance(at, nil))` still seeds the manual start node.
   - `Step(NewCompensateRequested(at, ""))` still emits the full rollback walk.
   - `TestTimerFiredStaleTokenIsNoop` stays green **unmodified** — the pinned
     contract that `TimerFired` is not validated.
7. **`ReceiveTask` validation.** `model.Validate` rejects an empty `MessageName`
   with `ErrEmptyMessageName`; a populated one still validates.
8. **Purity.** `engine/purity_test.go` must stay green — no new imports.

## Consequences

**Positive.** Removes a silent instance-wedging bug; stops an empty signal name
from interrupting a live activity through an error-boundary arm; closes the
consumer-reachable state-corruption vectors including the two that bypass `Step`
(`TargetNode`, `FailingActionName`); replaces a misleading `ErrTaskNotFound`/
`ErrTokenNotFound` with an accurate sentinel; closes the unresumable-`ReceiveTask`
shape; and converts a recurring bug class into a test failure.

**Negative.** Sixteen helpers each carry a three-line guard, nine of which fix no
defect today and exist purely to hold the invariant. Three wire-visible status
changes (404/422/200 → 400, tabulated above). The durability claim holds **only
for the empty string** — a future record minted with `" "` or a `"none"` sentinel
would reintroduce the same wildcard, and no test would fail. The stronger
invariant (*no stored record leaves an identity field empty*) is not asserted.

**Neutral.** No schema change and no migration. But **not** "no data impact":
`kernel.JournalReader` is a public replay port (`runtime/kernel/ports.go:23`) and
`service.DeliverSignal` has no empty-name guard, so a historical
`SignalReceived{Name: ""}` may already sit in `wrkflw_journal`. Replaying such a
journal through the new `Step` fails on that entry. Consumers who replay must
skip or repair those rows. Existing well-formed triggers are unaffected.

## Out of scope

Deferred, tracked separately: strict definition decoding
(`DisallowUnknownFields`) before tagging v0.1.0; the `Upsert` incoherent-task
invariant (`State: Claimed, Claim: nil`); extending `scripts/coverage.sh` to
exclude `examples/`.

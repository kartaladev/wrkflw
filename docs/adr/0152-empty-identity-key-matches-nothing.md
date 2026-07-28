# 152. An empty identity key matches no record

Status: Accepted — 2026-07-28. Constrains the `InstanceState` lookup/sweep helpers of the
engine core ([ADR-0002](0002-engine-core-execution-model.md)), including the arm lookups
unified by [ADR-0131](0131-arm-model-unification.md); adds a trigger-boundary check
alongside the input-validation seam of
[ADR-0115](0115-validation-engine-decides-seam.md) and the completion sentinels of
[ADR-0146](0146-usertask-outcomes-and-completion-api.md).

## Context

`InstanceState` resolves records — tokens, timers, tasks, arms, visits — through a
family of small lookup and sweep helpers. Each matches a caller-supplied key against one
field on every stored record:

```go
func (s *InstanceState) cancelTimersByTaskID(taskID, excludeTimerID string) []string {
	for _, tr := range s.Timers {
		if tr.TaskID == taskID { /* cancel it */ }
	}
}
```

None of them treated the empty string specially. When a caller passed `""`, the helper
matched **every record whose field was also empty** — silently converting a lookup into
a wildcard. Three distinct defect classes followed.

### 1. Cross-scope timer cancellation

`timerRecord.TaskID` is documented as `""` for non-human-task timers
(`engine/state_timers.go:18`). Of the three record-creation sites, exactly one leaves it
unset:

| Site | Kind | `TaskID` |
|---|---|---|
| `engine/step_triggers.go:302` | `TimerRetry` | **unset — `""`** |
| `engine/step_nodes.go:586` | `TimerInWait` | `cancelKey`, never empty |
| `engine/step_nodes.go:659` | `TimerDeadline` | `taskID`, never empty |

So `cancelTimersByTaskID("")` selected every retry timer in the instance, regardless of
scope. `cancelTokenWaits` (`engine/step_cancel.go:15`) passes `tok.AwaitCommand`, which
is empty for any token not parked on a command — an active token, or one parked on a
signal or message, which use `AwaitSignal`/`AwaitMessage` instead. It runs in a per-token
loop from four sweep sites (`step_errors.go:393`, `step_boundaries.go:146`,
`step_eventsubprocess.go:201`, `step_compensation.go:226`).

The result: cancelling scope B could delete a retry timer owned by a token in scope A,
emitting `CancelTimer` for it. Scope A was never cancelled, but its token then sat in
`TokenWaiting` forever with no timer left to fire. The instance neither completed nor
failed — it silently wedged.

The task-keyed sweep contributed nothing legitimate in `cancelTokenWaits`: a retry record
carries `Token: tok.ID` and is already removed by the `cancelTimersForToken` call on the
following line.

### 2. Arms matching across kinds

The three arm families share the embedded `triggerMatch` quartet. Its comment
(`engine/state_arms.go:11-12`) claims *"At most one of the four fields is non-empty for a
given arm (timer XOR signal XOR message)"*, but the real invariant is weaker in both
directions: a message arm sets **both** `Message` and `MessageKey`, and — decisively —
an arm may carry **no** non-empty match field at all. `armBoundaries`
(`step_boundaries.go:38-70`) assigns `TimerID`/`Signal`/`Message` in an
`if / else if / else if` chain, so an **error boundary** (no timer, no signal, no
message) is appended at `:70` with all four fields empty; the same holds for an
`armedEvent` on a catch node with none of the three (`step_nodes.go:858-886`).

Since each generic matches a single field, `armBySignal(arms, "")` returned the first
timer, message, **or error-boundary** arm; `armByTimer(arms, "")` and
`armByMessage(arms, "", "")` likewise. All nine `armedEventBy*` / `boundaryArmBy*` /
`eventTriggeredSubprocessArmBy*` wrappers inherited this.

This is not a benign lookup miss. `handleSignalReceived:733` and `handleTimerFired:484`
pass the returned arm straight to `fireBoundaryArm`, which **interrupts a live host
activity and routes it down the error path**. An empty signal name could therefore
corrupt a running instance's control flow.

### 3. Consumer-reachable trigger vectors

`wrkflw` ships as a library. Consumers construct `Trigger` values and call `Step`
directly, so a malformed trigger is ordinary input, not an internal invariant. No handler
validated its identity key:

| Trigger | Field | Consequence of `""` |
|---|---|---|
| `TimerFired` | `TimerID` | the arm cascade (`step_triggers.go:482-486`) runs **first** and matches an all-empty arm → boundary interrupt; only failing that does it reach `tokenAwaiting("")` (`:511`) and advance an arbitrary unparked token |
| `SignalReceived` | `Name` | arm branches (`:720/:733/:746`) match first; otherwise `tokenIDsAwaitingSignal("")` (`:714`) returns **every token not awaiting a signal** and broadcast resumes them all |
| `MessageReceived` | `Name` | `tokenAwaitingMessage("", "")` matches a token not awaiting a message — only when `CorrelationKey` is *also* empty, since both components must match |
| `ActionCompleted`, `ActionFailed`, `SubInstanceCompleted`, `SubInstanceFailed` | `CommandID` | `tokenAwaiting("")` returns an arbitrary unparked token |

Reachability differs by path. `service.DeliverSignal` (`service/service.go:362`) passes
the name straight to `ApplyTrigger` with no guard, so the signal vector is live through
the shipped API; `runtime.BroadcastSignal` and `runtime.DeliverMessage`
(`processdriver_message.go:57-60`) both short-circuit an empty name, so the message
vector is engine-direct only.

Two **exported** entry points bypass `Step` altogether and are reachable regardless:
`engine.TargetNode` (`target_node.go:41`), whose result drives
`ProcessDriver.validateInput` (`processdriver.go:793-806`) — which fails open, so an
empty `TaskID` runs an arbitrary node's validation strategy — and
`engine.FailingActionName` (`failing_action.go:17`), which resolves the wrong retry
policy (ADR-0126).

The `Human*` triggers and `ResolveIncident` were safe only incidentally, and not for the
obvious reason: `handleHumanCompleted`'s **first** lookup is `s.tokenAwaiting(t.TaskID)`
(`step_triggers.go:594`), which *does* wildcard-match; safety comes only from the second
lookup, `s.TaskByID` (`:602`), failing with `humantask.ErrTaskNotFound` (`:604`).

### Why this recurs

The safe helpers are safe **by accident of current record shapes**, not by construction.
`tokenByID("")` finds nothing only because every token happens to carry a non-empty `ID`.
That is precisely the property `TimerRetry` violated to create defect class 1. Nothing in
the code prevented the next record kind from reopening it.

## Decision

Adopt one rule across the `engine` package:

> An **identity key** names one specific record. The empty string is the *absence* of an
> identity, and an absent identity matches **no** record.

**Sixteen helpers** gain an early `if key == "" { return <zero> }` guard, in four files:

- `state_timers.go` — `timerByID`, `removeTimer`, `cancelTimersByTaskID`,
  `cancelTimersForToken`
- `state_arms.go` — the generics `armByTimer`, `armBySignal`, `armByMessage`, plus
  `removeArmedEventsForGateway`, `removeBoundaryArmsForHost`
- `step_state.go` — `tokenAwaiting`, `tokenByID`, `tokenIDsAwaitingSignal`,
  `tokenAwaitingMessage`, `removeToken`, `openVisitFor`
- `state.go` — `TaskByID`

The guard lives in the helpers, not the call sites: helpers are few and are where the
matching semantics reside, while call sites number in the dozens and grow with every
feature. An empty key returns the **zero result** (`nil` / empty slice), not an error —
the contract is already "nil if not found", and every caller handles that path.

**Thirteen helpers across five delegation families inherit their guard and must NOT be
guarded again:** the nine arm wrappers (via the three generics), `scopeOfToken` (via
`tokenByID`), `messageTargetNodeScoped` (via the message lookups), and
`setVisitTask` / `closeVisit` / `closeVisitAs` (via `openVisitFor`). The exported
`TargetNode` and `FailingActionName` likewise inherit from `tokenAwaiting` — for them
the helper layer is the *only* defence, since neither routes through `Step`. The list is
illustrative, not closed: any single-delegation helper inherits. `target_node.go`,
`close_kind.go`, and `failing_action.go` are unmodified.

In a **composite** key, every component that is itself an identity is guarded
(`openVisitFor`'s `tokenID` and `nodeID`, both); a component with documented empty
semantics is exempt (`armByMessage`'s `correlationKey` — only `name` is guarded).

### Exempt keys — where empty is meaningful

The rule governs *identity* keys only. Five parameters carry documented "empty means
something" semantics and keep their current behaviour; guarding them would break scope
resolution, message correlation, and two public constructors:

| Key | `""` means | Documented at |
|---|---|---|
| `scopeID` | the **root scope** — a real, matchable scope | `state_timers.go:22-24` |
| `correlationKey` | **uncorrelated** — matches a token/arm that is itself uncorrelated | `step_state.go:106-109` |
| **`excludeTimerID`** | **exclude nothing** — 2nd param of `cancelTimersByTaskID` / `cancelTimersForToken` | `state_timers.go:51-55,69-75` |
| **`setVisitTask`'s `taskID`** | the **value written**, not a lookup key | `step_state.go:136` |
| `StartInstance.StartNodeID` | resolve the definition's **manual start** | `trigger.go:28-31` |
| `CompensateRequested.ToNode` | **roll back everything** | `trigger.go:323-325` |
| `CompensateRequested.ReverseNode` | terminate rather than resume | `trigger.go:326-329` |

`excludeTimerID` is the sharpest trap: it is literally a `TimerID`, it lives **inside the
two functions this ADR changes**, and five of its seven call sites pass `""`
(`step_triggers.go:656,776,934`, `step_cancel.go:15,20`). Guarding it would cancel the
very timer the caller is handling.

### Trigger-boundary validation

The helper guard alone would downgrade a malformed trigger to a silent no-op. For a
library that is poor ergonomics, so `Step` additionally validates identity keys on
inbound triggers before cloning state, rejecting an empty one with a new sentinel:

```go
var ErrEmptyTriggerKey = errors.New("workflow-engine: trigger identity key is empty")
```

Validated (11): `SignalReceived.Name`, `MessageReceived.Name`, the four `CommandID`
triggers, the four `TaskID` triggers, and `ResolveIncident.IncidentID`. Excluded: the
exempt keys above, and `CancelRequested`, which carries no key.

**`TimerFired.TimerID` is excluded, reversing this ADR's first draft.**
`TestTimerFiredStaleTokenIsNoop` (`engine/step_timers_test.go:113-157`) pins an explicit
empty-`TimerID` case asserting `require.NoError(t, err, "stale TimerFired must not
error")`, documented at `:109-112` as deliberate: *timers are inherently racy with other
completion paths, and a stale TimerFired must never corrupt state or return an error
(unlike HumanCompleted which fails fast on an unknown token — timers can arrive late)*.
The helper guards already deliver exactly that — an empty `TimerID` becomes a clean
no-op. Where the two layers conflict, the older explicit contract wins.

Because a `Trigger` variant added later would fall silently through the validator's
`default` arm, the validated and exempt sets are **declared** and cross-checked by an
exhaustiveness test, following `AllTriggerKinds`
(`internal/persistence/store/trigger_codec.go:33`, test at `trigger_codec_test.go:185`).

`ErrEmptyTriggerKey` is deliberately **not** wrapped in `ErrInvalidTransition`: the
instance state is irrelevant, the trigger itself is malformed. Transports classify it
**400**, alongside the other caller-correctable input sentinels.

### Closing the unresumable-`ReceiveTask` shape

`receiveTaskStrategy.enter` (`step_nodes.go:97-99`) sets `tok.AwaitMessage =
rt.MessageName` unconditionally, unlike the catch-event (`:720,:725`) and boundary
(`:60,:62`) paths which guard `!= ""`, and `model.Validate` has no rule against an
unnamed `ReceiveTask`. Such a token parks on `AwaitMessage == ""` and is resumable today
only via a direct `Step(NewMessageReceived(at, "", "", …))` — which this ADR now
rejects, stranding it permanently.

`model.Validate` therefore gains a rule rejecting an empty `ReceiveTask.MessageName`
(`ErrEmptyMessageName`). Because `definition/model` cannot import `definition/activity`
(import cycle), the rule matches `KindReceiveTask` and reads `toWire(n).MessageName`,
mirroring `ErrPayloadValidationRequiresMessage` (`definition/model/validate.go:566-573`).

### Whitespace names: trimmed at authoring, never in the engine

The guards above compare against `""` exactly. A name of `" "` is therefore *not* an
empty key, and — unlike the empty case — it is **not** a defect: a token parked on
`AwaitMessage == "   "` remains resumable by an exact-equal
`MessageReceived{Name: "   "}`, because neither `tokenAwaitingMessage` nor
`validateTriggerKey` trims. A whitespace name is self-consistent at runtime; it is merely
a name no operator can reasonably produce or correlate on.

`strings.TrimSpace` is therefore applied **only at the authoring layer**, never in the
engine. The distinction is that the two key families have different provenance:

- **Id-shaped keys** (`TimerID`, `TaskID`, token ids, `CommandID`, `IncidentID`) are
  engine-minted by `nextID` (`h1`, `tm3`, `c7`, `inc2`). No record can ever hold `" "`, so
  trimming would change no outcome while adding a scan to hot paths — `tokenByID` runs per
  token per step.
- **Name-shaped keys** (`AwaitMessage`, `AwaitSignal`, and the arm `Signal`/`Message`
  fields) are **definition-authored**: `step_nodes.go:98` assigns
  `tok.AwaitMessage = rt.MessageName` and `:724` assigns `tok.AwaitSignal =
  ice.SignalName`. Trimming *these* would refuse to resume a token legitimately parked on
  a whitespace-named message — converting a merely-odd definition into a permanent token
  leak, the exact class this section exists to prevent.

So `ErrEmptyMessageName`'s predicate widens to `strings.TrimSpace(...) == ""`, and a
companion rule `ErrBlankEventName` rejects any node that *declares* a `SignalName` or
`MessageName` consisting only of whitespace. Both **reject**; neither **reclassifies**.
That distinction is load-bearing: `SignalName != ""` / `MessageName != ""` are used as
event-kind discriminators at `validate.go:299-300` (manual vs event-triggered start),
`:531` (`isErrorBoundary`), and `:777` (`isEventTriggeredSubprocess`). Trimming *those*
comparisons would silently turn a boundary declaring `SignalName: " "` into an **error
boundary**, changing runtime semantics — so they are deliberately left as exact `""`
comparisons.

This is definition **hygiene**, not a live-bug fix: it makes the whitespace shape
unrepresentable rather than merely unmatched. The engine's residual `" "` exposure for
a consumer who bypasses `model.Validate` is recorded under Consequences.

### Alternatives considered

- **Guard the single call site** (`if tok.AwaitCommand != ""` in `cancelTokenWaits`).
  Rejected: fixes the headline bug only, leaving the arm and token vectors open, and the
  next caller rediscovers the trap. Notably, the call is in fact **wholly redundant** —
  all three `timerRecord` sites set `Token: tok.ID`, so the following
  `cancelTimersForToken` already covers it — so *deleting* it is also viable; deferred
  because it would reorder emitted `CancelTimer` commands.
- **Return an error from the helpers.** Rejected: no behavioural gain over the existing
  not-found path, and it would thread errors through dozens of unexported call sites.
- **Panic on an empty key.** Rejected: a library core must not panic on caller input.

## Consequences

**Positive.** An instance can no longer be wedged by a cross-scope sweep. An empty signal
name can no longer match an error-boundary arm and interrupt a live activity. The
consumer-reachable corruption vectors are closed, including the two that bypass `Step`
(`TargetNode`'s fail-open validation and `FailingActionName`'s retry-policy lookup).
`ErrEmptyTriggerKey` replaces a misleading `ErrTaskNotFound`/`ErrTokenNotFound` with an
accurate, classifiable error. The unresumable-`ReceiveTask` shape is closed at authoring
time. The rule is uniform and test-pinned, so a future record kind that leaves a field
empty turns the recurring bug class into a test failure.

**Negative.** Sixteen helpers each carry a three-line guard; **nine** of them fix no
defect today and exist purely to hold the invariant. Three wire-visible status changes:
`HumanCompleted{TaskID:""}` 404 → 400, the other three `Human*` triggers 422 → 400, and
`ResolveIncident{IncidentID:""}` — today a *documented idempotent no-op*
(`step_triggers.go:977-980`) returning 200 — now 400. That last one deliberately reverses
an existing contract: the idempotency guarantee exists to absorb *unknown* incident ids,
not *absent* ones, and an admin call with no id is malformed rather than racy.

In the engine the durability claim holds **only for the empty string**. A future record
minted with `" "` or a `"none"` sentinel would reintroduce the same wildcard with no test
failing; the stronger invariant (*no stored record leaves an identity field empty*) is
asserted nowhere. The authoring layer now closes the whitespace half of that gap for
event names — `ErrEmptyMessageName` trims, and `ErrBlankEventName` rejects a declared
whitespace-only `SignalName`/`MessageName` — but `Step` does not require a definition to
have passed `model.Validate`, so a consumer who skips validation can still feed a
whitespace-named definition straight into the engine. That residual is accepted, not
closed: trimming in the engine would strand tokens legitimately parked on such a name
(see *Whitespace names* above). A `"none"`-style sentinel remains uncaught at every layer.

**Neutral.** No schema change and no migration. It is **not**, however, free of data
impact: `kernel.JournalReader` is a public replay port (`runtime/kernel/ports.go:23`) and
`service.DeliverSignal` never guarded the name, so a historical
`SignalReceived{Name: ""}` may already be persisted in `wrkflw_journal`. Replaying such a
journal through the new `Step` fails on that entry; consumers who replay must skip or
repair those rows. `Step` remains a pure function — the validator is a predicate over the
trigger, reads no clock, and performs no I/O, so engine purity
(`engine/purity_test.go`) is preserved. Existing well-formed triggers are unaffected.

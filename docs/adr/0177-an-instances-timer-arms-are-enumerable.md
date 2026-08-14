# 177. An instance's timer arms are enumerable

- Status: Proposed (**audited** — rule-#9, 3 lenses; this ADR was corrected by it)
- Date: 2026-08-13

> Design and every measurement:
> [`docs/specs/2026-08-13-engine-visibility-and-truthfulness.md`](../specs/2026-08-13-engine-visibility-and-truthfulness.md) §1.
> Premise evidence: `docs/specs/2026-08-13-adr-0177-premise-evidence.md`.
> Plan: [`docs/plans/2026-08-13-engine-visibility-and-truthfulness.md`](../plans/2026-08-13-engine-visibility-and-truthfulness.md).
>
> Closes pre-v0.1.0 **blocker 9** and backlog **3c**.

## Context

`engine.InstanceState` exposes `SignalWaiters()` and `MessageWaiters()` as *authorities* — one
method a runtime or harness mirrors, so a new construct extends the method rather than every call
site (ADR-0123, ADR-0166). Timers have no such authority. `HasArmedTimers()`, added by ADR-0175,
reads `s.Timers` directly.

There are **eight** production timer-arm sites and **five** `TimerKind` constants. Four sites arm
as `TimerIntermediate` and are never written into `s.Timers`; four are. Executed, six fixtures
(evidence §3, `EXIT=0`):

| fixture | `len(s.Timers)` | `HasArmedTimers()` | the arm actually lives in |
|---|---|---|---|
| boundary timer | 0 | **false** | `s.Boundaries[0].TimerID` |
| event-gateway arm | 0 | **false** | `s.ArmedEvents[0].TimerID` |
| event sub-process | 0 | **false** | `s.EventTriggeredSubprocesses[0].TimerID` |
| plain intermediate catch | 0 | **false** | **nothing** — only `tok.AwaitCommand` |
| CONTROL: task deadline | 1 | **true** | `s.Timers[0]` |

The control row proves the others are not vacuous.

Three claims this decision was previously justified by are **false**, and the corrected
justification is weaker but still sufficient:

- The known-gap comment at `state_timers.go:141-145` names **three** invisible sources and says
  "all four". There are **four** invisible sources and **five** total. The plain intermediate catch
  event appears in no enumeration of the **invisible** sources, including `HANDOVER.md`. (⚠ It is
  named in two *other* kind enumerations — `engine/command.go:66` and `engine/state_timers.go:14` —
  both of which are themselves stale; see the spec's §5.)
- `processtest`'s `Park.HasArmedTimers` inherits the defect only **partially**: `harnessEnv.classify`
  overrides it to `true` when a parked token's `AwaitCommand` matches a pending scheduler timer, so
  plain-ICE already classifies correctly *under a `Harness`*. The free `processtest.Classify` does not.
- ⚠ The committed claim that *"`timerRecord.Kind` is unexported"* — and therefore that a consumer
  **cannot** exclude a stall timer — is **false**. `TimerKind` and the field `Kind` are exported;
  only the struct type is not. A consumer compiled and ran
  `st.Timers[i].Kind == engine.TimerCompensationStall` from an external package. What a consumer
  genuinely cannot do is *construct* a `timerRecord`.

So this is an **ergonomics and authority** decision, not an impossibility fix. It is still worth
making: the gap it closes (four invisible sources) is real, and the alternative is every consumer
re-deriving a four-source scan that the engine already gets wrong in its own doc comment.

The structural obstacle: `SignalWaiters`/`MessageWaiters` can enumerate the token source because
`Token.AwaitSignal`/`AwaitMessage` exist. **There is no `Token.AwaitTimer`.** A plain
intermediate-catch timer parks on the overloaded `AwaitCommand`, measured holding human-task ids,
event-gateway sentinels, timer ids and `""` across fixtures — so the source is not identifiable
from state alone.

## Decision

1. Add `TimerWaiter{TimerID, Kind, NodeID, TokenID}` and a `TimerWaiters()` authority to
   `InstanceState`, with five per-source accessors, built on a `timerWaitersOf[T]` sibling of the
   existing generic scanners. Deterministic order, `nil` when empty.
2. Add **`Token.AwaitTimer`**, written at the plain-intermediate-catch arm site, giving the fifth
   source a real home. ⚠ **Dual-write**: `AwaitCommand` keeps its current value, so
   `handleTimerFired`'s path-5 fall-through is untouched and this decision stays additive.
   `AwaitTimer` is an enumeration marker, not a dispatch key.
   ⚠⚠ **It must also be CLEARED wherever `AwaitCommand` is cleared — seven production sites**
   (`step_gateways.go:243`, `step_timers.go:83`, `step_triggers.go:112/376/569/741/1002`), via one
   `Token.clearAwait()` helper. The audit caught this ADR specifying only the set side: left
   unset-only, the field stays populated after the first fire, `HasArmedTimers()` returns `true`
   forever, `Classify` reports `ReasonTimer` for a park with nothing armed, and `AutoTimers()` spins
   on an id path 5 treats as a stale no-op — **inverting this ADR's purpose**, concealed by the very
   "additive, no dispatch change" rationale above. Clearing at `:569` is inside the path-5
   fall-through; that is a *field write*, not a dispatch change, and is in scope.
3. Redefine `HasArmedTimers()` over `TimerWaiters()`, applying the walk-scoped exclusion there.
   `TimerWaiters()` itself enumerates **everything** — the siblings' contract.
   ⚠ `walkScoped()` **does not exist today** (`grep` → zero hits): the exclusion is currently an
   inline `tr.Kind != TimerCompensationStall` at `state_timers.go:146-152`. This ADR introduces the
   named predicate covering that one kind, and leaves it extensible.

Rejected: **id-prefix sniffing** (`<instance>-tm<N>`), which would cover all five sources with no
schema change but parses meaning out of an identity, contradicting the ADR-0152 discipline the arm
lookups are built on. Rejected: **scoping to the four recorded sources**, cheaper but leaving the
free `processtest.Classify` permanently wrong for plain-ICE.

## Consequences

**Positive.** One authority to extend for a future timer construct. `processtest` gains a
`Kind`-bearing view, so a harness can fire a *specific* timer instead of `AdvanceTimers()`'s
current global advance. Blocker 9 closes. Blast radius is small: `HasArmedTimers()` has **two**
call sites repo-wide (one production, one test), and zero consumers outside `engine` read
`InstanceState.Timers`.

**Negative / accepted.**

- ⚠ **Parks reclassify.** `processtest.Classify`'s priority, verified character for character, is
  `terminal > openTasks > incidents > signals > messages > timers > commandWait`. Timers rank below
  messages, so the three *harness-level* fixtures stay `ReasonMessage`. ⚠ A boundary timer on a
  **user task** is held non-reclassified by the `openTasks` rung instead — a separate case needing
  its own pin. What flips is a park with a timer arm and no task, incident, signal or message:
  `ReasonAsyncChild`/`ReasonUnknown` → `ReasonTimer`, which `AutoTimers()` then fires. Intended, and
  the only behavioural risk here.

  ⚠⚠ **Amended in-bundle after `/code-review` (owner gate): "the only behavioural risk" was right
  about the risk and wrong about the count.** The widening reaches a **fourth** shape, and that one
  is a regression: a **boundary** or **event-sub-process** timer arm beside an **in-flight
  `InvokeAction` / child instance** also flips to `ReasonTimer`, so `Chain(AutoTimers(), …)` fires
  the boundary to its timeout instead of letting the action handler resolve the park —
  contradicting the contract `processtest/handlers.go` documents for `AutoTimers`. Measured on
  `start → svc[action work] ⊸ bnd[timer 3h]`: `reason=timer node="svc" hasArmedTimers=true`,
  token `awaitCmd="i-c1"`, `AutoTimers() → AdvanceTimers()`.

  The repair lives in `processtest`, not here: `Classify`'s timer rung outranks `commandWait` only
  for a **primary** timer park — a waiting token whose `AwaitCommand` is in
  `state.TimerWaiters()`'s id set (plain intermediate catch, retry backoff), or a non-empty
  `state.TimerArmedEventWaiters()` (an event-based gateway, whose `evtgw:` sentinel no handler can
  deliver). Everything else yields. See the spec's §1 amendment for the five-shape measurement
  table and for why the adjudicated `Token.AwaitTimer` predicate was rejected: executed, it also
  reddens the retry-backoff park — `ReasonTimer` since long before this ADR, because
  `HasArmedTimers` already read any non-`TimerCompensationStall` record — and the event-gateway park
  this ADR is what promoted.
- **KNOWN LIMITATION (pinned, with its falsifier stated):** an instance parked on a plain
  intermediate-catch timer *before* this ships has no `AwaitTimer` in its stored row, so that source
  stays invisible after rehydration until the arm is re-created. Backfilling would need the rejected
  id-sniffing. ⚠ The pin is falsified **only** by a backfill implemented inside `TimerTokenWaiters`;
  one done in a migration or the rehydrate path leaves it green. It is therefore paired with the
  complementary assertion — the same token *with* `AwaitTimer` yields a waiter — which does
  genuinely fail before the field exists.
- Persisted `Token` JSON gains a field (additive; zero value is correct for every other node kind).
  ⚠ Persistence is whole-state `json.Marshal` with no `DisallowUnknownFields`: new fields
  round-trip on upgrade, and a **downgrade silently drops them**, returning the plain-ICE source to
  invisibility. Same class as the KNOWN LIMITATION above.
- **Forward dependency on ADR-0179 (bundle C).** Its `TimerCompensationRetry` kind will land in
  `s.Timers` and must be added to `walkScoped()`, or a compensating instance reports armed timers a
  harness would fire. That edit and its test belong to bundle C; this ADR only guarantees the
  predicate exists in one place to extend.

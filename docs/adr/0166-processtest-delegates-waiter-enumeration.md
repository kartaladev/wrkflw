# 0166. `processtest` delegates signal and message waiter enumeration to the engine

## Status

Accepted — 2026-08-07. Audited (rule #9); findings folded. Implemented, with one
correction forced by execution (decision 4's fingerprint inputs — see the
correction block there).

Spec: `docs/specs/2026-08-07-processtest-waiter-enumeration.md`
Plan: `docs/plans/2026-08-07-processtest-waiter-enumeration.md`

Related: ADR-0123 (message waiters), ADR-0154 (the sources `SignalWaiters` was
completed with), ADR-0092 (the consumer test harness), ADR-0158 (delivery 3,
blocked by this).

## Context

`engine.InstanceState` exposes two authorities over "what can wake this
instance": `SignalWaiters()` and `MessageWaiters()`. Each enumerates **four**
sources — token awaits, boundary arms, event-based-gateway arms, and
event-subprocess arms. They exist *because* that enumeration was once scattered
and a source was forgotten; ADR-0154 fixed exactly that, and `SignalWaiters`'
doc comment records the failure mode:

> Omitting a source here does not fail loudly — the runtime simply never
> subscribes the name, and the instance parks forever.

`processtest.Classify` — the public consumer harness — did not call either
method. It re-derived source 1 from `Token.AwaitSignal` / `Token.AwaitMessage`
and dropped the other three. `Harness.PublishSignal` and `Harness.DeliverMessage`
iterate those fields, so on a definition parked purely on an arm they match
nothing and `Pass()` forever.

Measured on `main` @ `abccb96` (full output in spec §2): a user task with a
signal boundary yields `Park.AwaitingSignals = nil` while
`state.SignalWaiters() = ["escalate"]`. The same holds for event-subprocess arms,
event-gateway arms, and for **messages**.

The harness is a shipped product surface. A consumer writing a test against a
boundary-armed definition gets a run that cannot progress, and delivery 3's
headline scenario is untestable by the very harness we ship for testing it.

## Decision

### 1. `Classify` calls the engine's authorities instead of re-deriving them

`AwaitingSignals` from `state.SignalWaiters()`, `AwaitingMessages` from
`state.MessageWaiters()`. Both stay *distinct* as documented, so `Classify`
dedups: signals by name, messages by the **`{Name, CorrelationKey}` pair**.

### 2. `Park.AwaitingMessages` becomes `[]engine.MessageWaiter` — a breaking change

The engine tracks a correlation key; `[]string` discards it, and a consumer has
no other way to discover which key an arm expects: the arm slices on
`InstanceState` have unexported element types.

⚠ **Rationale corrected by the audit.** An earlier draft justified this as
turning a 1000-step spin into something diagnosable. Measured, that is false once
decision 4 lands in the same bundle: the drive error for a wrong-key delivery is
**byte-identical** before and after this delivery. The real benefit is narrower —
a **custom** handler can read the expected key off the `Park` — and it is still
worth the break, but the ADR should not claim an effect it does not have.

`v0.1.0` is untagged and the CHANGELOG's pre-v0.1.0 section carries no stability
promise. `AwaitingSignals` stays `[]string`, mirroring `SignalWaiters()`'
signature; a `processtest`-owned duplicate type would reintroduce the second
enumeration this ADR deletes.

### 3. Arms compete in the existing `Reason` ladder, and the timer promotion widens with them

> **Correction (pre-delivery review).** The arm-derived test was first written
> per-await: `ReasonSignal` asked only whether a token carried `AwaitSignal`. A
> signal **arm** raises `ReasonSignal` while a token holds a live `AwaitMessage`,
> so that park was judged arm-derived, promoted to `ReasonTimer`, and `AutoTimers()`
> fired a timer — advancing the shared fake clock an hour and taking a branch
> `main` leaves alone. A reason is arm-derived only when **neither** await is
> token-carried.

No new `Reason` value. Arms compete for the primary reason as token awaits do,
matching `Reason`'s documented meaning — an armed boundary signal *is* the
external stimulus the park waits on.

**`harnessEnv.classify` (`processtest/harness.go:305`) widens in the same
change.** It promotes a timer catch to `ReasonTimer` only from
`ReasonAsyncChild`/`ReasonUnknown`; an arm-derived `ReasonSignal` displaces that,
the promotion never fires, and the shipped `AutoTimers()` recipe passes forever.
Measured: a timer catch beside a live event-subprocess arm goes from
`completed` to `unhandled park: signal at node ""`. The promotion therefore also
accepts `ReasonSignal`/`ReasonMessage` **when the reason is arm-derived** — no
token carries the matching await. A genuine token signal-catch still outranks a
timer.

### 4. Delivery is bounded per **park state**, not per name

Exposing arms without bounding delivery creates a loop that did not exist: a
token signal-catch is consumed when it fires, a non-interrupting arm stays armed,
so `Chain(PublishSignal("ping"), CompleteTasks(...))` spins to
`drive step limit exceeded after 1000 steps` and never reaches `CompleteTasks`.

⚠ **The obvious bound is wrong.** An earlier draft said "deliver each name at
most once", on the premise that delivering one name repeatedly is *never* a
useful test intent. A two-node definition falsifies it:
`start → catch("go") → catch("go") → end` passes today and fails under
fire-once-per-name with `unhandled park: signal at node "c2"`. Two sequential
catches of one name is ordinary BPMN.

So the handlers fingerprint the park they last delivered against and deliver again
only when it changes. Two sequential catches advance between deliveries and fire
twice; a non-interrupting arm re-matches an identical park and fires once. The
fingerprint is **mutex-guarded**: a bare `bool` is a data race when one handler
value drives two concurrent instances, in a harness that documents race-freedom.

> **Correction — the fingerprint's inputs, refuted on execution (implementation,
> rule #11).** This decision originally specified the fingerprint as *"the sorted
> set of token IDs currently awaiting that name, plus the three arm-slice
> lengths"*. Implementing it that way **breaks the very case it was written to
> protect.** Measured on `start → c1("go") → c2("go") → end`:
>
> ```
> park@c1: token id="d9qkk8983g3l2a2etb3g" node="c1"  boundaries=0 armedEvents=0 evtsubs=0
> park@c2: token id="d9qkk8983g3l2a2etb3g" node="c2"  boundaries=0 armedEvents=0 evtsubs=0
> ```
>
> The token **keeps its ID** as it advances; only its NODE changes. So the
> specified fingerprint is byte-identical at both parks, the second delivery is
> suppressed, and row 8 fails — the exact regression this decision exists to
> prevent. The spec's own justification said "the awaiting token changes (`c1` →
> `c2`)", which are node ids; the ADR restated them as token ids and the hedge was
> lost. Two corrections, both verified by mutation:
>
> 1. The fingerprint keys on the sorted **`tokenID@nodeID`** pairs, not token ids.
> 2. It is additionally scoped by **`state.InstanceID`**. An arm-derived park has
>    *no* awaiting token, so two instances of one definition share an otherwise
>    identical key (empty token set, equal arm counts) and one handler value
>    driving both would deliver to the first only — a collision that exists
>    precisely for the arm-derived parks this ADR introduces.

> **Correction 2 — the fingerprint idea itself was wrong (pre-delivery review).**
> Two adversarial reviewers, run before the gate, independently refuted the
> corrected fingerprint as well. Three executed regressions against `main`:
>
> - **A loop back to the SAME catch node is falsely suppressed.** `start → c1("go")
>   → tick → xor ─(n<2)→ c1` completes on `main` with two ticks; under the
>   fingerprint it fails `unhandled park: signal at node "c1"` with one. Correction
>   1 reasoned about `c1 → c2` and stopped there; a cycle re-enters `c1`, where
>   *both* token id and node id are unchanged.
> - **The three arm-slice lengths are instance-wide, so the guard self-defeats.**
>   If the arm's own downstream branch arms anything — e.g. its target task carries
>   a deadline boundary — `len(Boundaries)` grows on every firing, the key changes,
>   and delivery is re-authorised forever: `drive step limit exceeded after 1000
>   steps`, where `main` completes.
> - **One last-key slot DISPLACES across instances.** Adding `InstanceID` stops the
>   *collision* but not the eviction: instance B's key replaces A's, so A's next
>   identical park is delivered to again. Four concurrent drives sharing one
>   handler fired a non-interrupting arm a run-varying 4–28 times.
>
> **The root cause is that bounding token catches was never necessary.** A token
> signal/message catch is *consumed* when it fires and cannot re-match; only an
> **arm** can. The fingerprint existed to stop an arm from spinning and was applied
> to both, which is what broke loops. The shipped bound is therefore:
>
> - a **token** catch is never bounded — every match is a real one;
> - an **arm-derived** delivery fires once per **instance** per **parked node**:
>   the first park delivers, and a later park delivers again only if some token is
>   parked on a node this handler has not already fired at;
> - state is a per-instance map under a mutex, not one slot.

> **Correction 3 — the bound may not key on the waiter COUNT (`/code-review`).**
> Correction 2 first counted the waiters matching the name. `/code-review` showed
> that count is wrong in *both* directions, each verified against `main`:
>
> - **Two sequential arms of one name each report a single waiter**, so the second
>   is silently suppressed: `approve1 ⊸ esc1("go") → approve2 ⊸ esc2("go")` stops
>   with `unhandled park: human-task at node "approve2"`. This is the audit's own
>   `catch("go") → catch("go")` falsification reproduced on the arm side.
> - **An arm whose branch arms another waiter of the same name** makes the count
>   grow every firing, re-authorising delivery forever: `drive step limit exceeded
>   after 60 steps`.
>
> An arm's true identity is unreachable (the arm slices have unexported element
> types), so the bound uses the closest observable proxy: **which nodes tokens are
> parked on**. Two arms on different activities are different parked nodes and both
> fire; one arm re-matching its own unchanged park is the same node and fires once.
>
> `parkKey` is deleted. This is simpler than what it replaces and passes every case
> the fingerprint was built for, plus the three it failed.

### 5. `Node` falls back instead of collapsing to `""`

`Classify` resolves `Node` from `Token.AwaitSignal`, which no arm sets, so an
arm-derived park reported `Node=""` — degrading the two errors a consumer
actually sees. When the reason is arm-derived, `Node` now falls back to the first
waiting token's node id. The arm's *own* node is unreachable without an engine
change this ADR declines.

## Consequences

### Good

- The blocker is closed for **signals and messages** across all three non-token
  sources.
- Delivery 3 (ADR-0158) becomes testable through the shipped harness.
- The expected correlation key becomes reachable by a custom handler.
- One signal/message enumeration in the repo instead of two.

### Bad / accepted

Every item below is a consumer-visible change and must appear in `CHANGELOG.md`:

1. **`Park.AwaitingMessages` changes type** — breaking, pre-v0.1.0.
2. **`Park.AwaitingSignals` changes semantics** while keeping its type. A
   consumer's `if len(p.AwaitingSignals) > 0` branch now fires on definitions
   where it never did. This is the intended fix, and still a behavioural change.
3. **`Reason` shifts** for the measured set: event-gateway arm
   `async-child → signal`; ReceiveTask + signal boundary `message → signal`;
   timer catch beside any arm `async-child`/`unknown` → `signal`/`message`.
   UserTask + signal boundary is unchanged. (An earlier draft cited a ServiceTask
   with a signal boundary; that shape never parks — it completes synchronously —
   so the example was deleted rather than repaired.)
4. **`Park.Node`** now names the parked token's node for arm-derived parks, where
   it would otherwise have been `""`. It still never names the arm's own node.
5. **An ARM-derived `PublishSignal`/`DeliverMessage` delivery is bounded.** It
   fires once per instance per waiter set for that name, where before this ADR an
   arm could not be delivered to at all. A **token** catch is unbounded and
   behaves exactly as it did on `main`, loops included. The bound is per handler
   **value** and per instance, so sharing one handler across concurrent drives is
   equivalent to one handler per drive.

### Deliberately not addressed

- **`Park.HasArmedTimers` has the identical one-source defect.**
  `len(state.Timers) > 0` misses boundary and gateway timer arms — measured:
  `len(Timers)=0, len(Boundaries)=1, HasArmedTimers=false`. Closing it needs an
  engine-side timer authority mirroring `SignalWaiters`, which is its own ADR.
  Owner-adjudicated out of scope and **filed as a follow-up**.
  ⚠ Consequently this ADR does **not** claim the ADR-0154 class is closed in
  `processtest` — only that it is closed for signals and messages. A definition
  parked purely on a timer arm remains undriveable through the harness.
- **`DeliverMessage` matching on the correlation key.** Still matches by name.
- **Any change to `engine`.**
- **Constructing an arm-derived `Park` directly.** The arm slices are unexported,
  so a consumer cannot unit-test a `ParkHandler` against an arm park without
  driving a real definition. A genuine harness gap, not this delivery's.

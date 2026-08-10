# 158. A broadcast signal fires every matching arm per family, not the first

- Status: Accepted
- Date: 2026-08-10
- Amends: [ADR-0124](0124-repeatable-noninterrupting.md) Decision item 3

> This number was reserved by the parked `feat/durable-waiters-delivery-correctness`
> bundle, which failed its adversarial audit and was split. ADR numbers 0155–0157
> remain reserved by the still-parked remainder — verified: no file claims them.
>
> Ships with [ADR-0172](0172-an-event-subprocess-arm-checks-instance-status.md) in
> one bundle: the fan-out multiplies the number of event-sub-process arms that
> fire per delivery, which is what makes 0172's hole routinely reachable.
>
> Design: `docs/specs/2026-08-10-signal-fanout-and-esp-status.md`.
> Executed premises: `docs/specs/2026-08-10-signal-fanout-premise-evidence.md`.

## Context

`handleSignalReceived` dispatches a `SignalReceived` through four tiers —
event-based-gateway arms, boundary arms, event-sub-process arms, then standalone
parked-signal tokens. **Only tier 4 loops.** Tiers 1–3 use singular first-match
lookups, all funnelling into `armBySignal` (`engine/state_arms.go:225`), which
returns at the first arm whose `Signal` matches.

First-match is correct for the families it was written against, and stays correct
there. Measured (evidence D5): a `TimerID` is **unique per arm** — two boundary
arms with the same 30-minute duration get `i1-tm1` and `i1-tm2` — so a
`TimerFired` names exactly one arm by construction and fan-out would be
meaningless, not merely undesirable. Message delivery is point-to-point within an
instance, so `handleMessageReceived`'s first match is the intended semantics.
Both route through `dispatchArmCascade`; `handleSignalReceived` does not, which
instrumentation confirms (zero cascade entries for a signal, one for a message,
two for two timers).

Signal is the odd one out: it is a **broadcast**, and its own tier 4 already
treats it that way. The inconsistency is that broadcast is applied *across*
families but not *within* one.

Measured (evidence D1) on a parallel fork into two `UserTask`s, each with an
interrupting signal boundary on `"escalate"`, given ONE delivery:

```
BEFORE tokens=2  len(Boundaries)=2  tasks: i1-h1 unclaimed, i1-h2 unclaimed
AFTER  tokens=1  len(Boundaries)=1  tasks: i1-h1 cancelled, i1-h2 unclaimed
AFTER  commands=1: UpdateTask{TaskID:i1-h1 State:cancelled}
```

`taskB` stays parked with `bndB` still armed. BPMN fires both.

**Through the public harness this is a silent wrong answer** (evidence D2). The
realistic consumer shape `Chain(PublishSignal, CompleteTasks)` returns
`err=<nil>` and the instance reaches `completed`: branch A escalated, branch B
completed normally. Two explicit deliveries *do* fire both arms, which proves the
second arm is live — the engine defers it, it does not drop it.

The defect is pre-existing, but [ADR-0154](0154-signal-waiters-include-boundary-and-gateway-arms.md)
changed its reachability. Before it, signal boundary and event-gateway arms were
never subscribed on the `SignalBus`, so a broadcast never reached them. ADR-0154
fixed the subscription, promoted this to a routine production path, and recorded
it in its own Consequences as knowingly left open.

### A second defect, found while re-deriving the premises

Each tier performs its **own** lookup at the moment it runs — deliberately;
ADR-0169's comment forbids hoisting them. The unrecorded consequence: an arm armed
by an *earlier tier's own drive*, which did not exist at the delivery instant, is
visible to a later tier and fires in the same delivery. Both routes were executed
(evidence CTL-1, CTL-2):

- **tier 1 → tier 2.** A gateway fire drives into a `UserTask` carrying a signal
  boundary on the same name. The human task is **minted and cancelled inside one
  step**; its `AwaitHuman` is dropped by ADR-0161's filter.
- **tier 2 → tier 3.** A boundary fire drives into a `SubProcess` whose scope arms
  an event sub-process on the same name. The sub-process is **entered and torn
  down inside one step**, and `nested-work` stays in `History` as a visited node
  although its action never ran.

Tier 4 already forbids exactly this for tokens. Tiers 1–3 do not.

## Decision

**Tiers 1–3 change from singular lookup to snapshot-then-fire-each.**

### 1. Snapshot identities as VALUES, then re-resolve

Before any dispatch, snapshot the identity of every matching arm per family:

| family | identity | has `NonInterrupting`? |
|---|---|---|
| `armedEvent` | `(GatewayToken, CatchNode)` | **no** (compile-verified, evidence M2) |
| `boundaryArm` | `(HostToken, BoundaryNode)` | yes |
| `eventTriggeredSubprocessArm` | `(EnclosingScopeID, EventSubprocessNode)` | yes |

Then for each snapshotted identity in order, re-resolve the arm and skip it if it
is gone.

**Re-resolution is an existence check only.** It must never be used to *select*
the next arm — that is the rejected re-scan, which does not terminate.

**Identities are values, not pointers.** Measured (evidence M1): `removeArmsWhere`
allocates a fresh backing array and the wrappers assign it over the field, so a
pointer taken earlier still addresses the **detached** array where the removed arm
is intact; dispatching through it would fire an already-retired arm — a
wrong-outcome bug, not a crash. The hazard is bidirectional: a pointer to a
*surviving* arm is also detached, so a write through it is silently dropped.

**The snapshot is mandatory twice over, and the second reason is new.**

1. *Termination.* A non-interrupting arm deliberately stays armed after firing
   (ADR-0124). Measured (evidence M9) in **both** the boundary and ESP families:
   the same lookup re-resolves the arm byte-identically immediately after its
   fire. A re-scan would find it forever.
2. *Single-instant semantics.* The snapshot confines the delivery to arms that
   existed at the delivery instant, closing the second defect above.

### 2. Ordering is DEFINITION-SCAN ORDER in every family — no sorting

| tier | family | order |
|---|---|---|
| 1 | `armedEvent` | slice (definition-scan) order |
| 2 | `boundaryArm` | slice (definition-scan) order |
| 3 | `eventTriggeredSubprocessArm` | slice (definition-scan) order |

**The engine does not re-order matching arms.** Ordering is outcome-affecting and
**author-controlled**: it follows the order the definition declares its nodes in.

This ADR previously specified a per-family sort on `NonInterrupting` — tier 2
non-interrupting-first, tier 3 interrupting-first — justified by the rule *order
each family so a later arm cannot destroy an earlier arm's effects*. **The audit
refuted both rules by execution, and the underlying rule with them.**

**Tier 3's justification was false.** The measurement it rested on (evidence CTL-3)
used a non-interrupting event sub-process whose body **parks** — an unstated
precondition of the whole conclusion. Given a body that **completes
synchronously** (`ni-start → ni-send(SendTask) → ni-end`, the canonical "notify
while the main flow continues" shape), the child scope drains and closes before
the interrupting arm fires, so `cancelScopeSubtree` has nothing to destroy, and
the `SendMessage` is fire-and-forget with **no `CommandID`**, so ADR-0161's filter
cannot drop it either:

```
# non-interrupting FIRST
ESPORD afterNI: status=running tokens=1 scopes=0 esp=2
ESPORD afterNI   cmd engine.SendMessage {Name:ni-message ...}
ESPORD afterNI   history=[... {ni-start} {ni-send} {ni-end}]

# interrupting FIRST (the rule this ADR previously chose)
ESPORD-B afterINT   history=[... {int-start} {int-work}]   # no ni-* AT ALL
ESPORD-B non-interrupting arm still resolvable: false
```

**Both arms take effect.** So "impossible for both to take effect in any order"
was false, and the chosen order destroyed the non-interrupting arm unfired.

**Tier 2's justification was equally unqualified.** *"Non-interrupting-first lets
both arms take effect (2 tokens)"* assumes the non-interrupting branch leaves the
instance running. Routed to a force-termination end — "escalate, then abort":

```
BNDORD afterNI: status=terminated tokens=0 ... cmd FailInstance{Err:escalated: abort}
BNDORD >>> interrupting sibling SKIPPED by the IsTerminal() re-check; it never fires
BNDORD-B afterINT: status=completed tokens=0     # the other order
```

`tokens=0`, not 2 — and **the two orders produce different terminal statuses**.

**The lesson, and why no sort replaces them.** Whether an earlier arm's effects
are destroyable depends on the arm's **body** — parks vs completes vs terminates —
not on its `NonInterrupting` flag. The flag is not a sufficient statistic for the
ordering decision, so *any* flag-based sort is a heuristic that is wrong for some
definitions. Both refuted rules were derived from a single fixture whose body
shape was never stated; that is [ADR-0170](0170-an-unhandled-error-does-not-restart-a-live-compensation-walk.md)'s
recorded failure mode repeating.

Definition-scan order makes no claim execution can refute, and it removes the two
**opposite-direction sorts** whose confusion the plan itself identified as the
single likeliest implementation error.

⚠ Ordering therefore stays outcome-affecting, and this ADR does not pretend
otherwise: two arms on one host resolve to whichever the definition declares
first, because the first fire's `removeBoundaryArmsForHost` retires the second.
Authors who need a particular order must declare the nodes in that order.

⚠ **Slice order is stable across persistence.** Measured: the arm slices survive a
JSON persist/reload cycle in declaration order (`[bA bB bC] → [bA bB bC]`, with a
root `""` `EnclosingScopeID` preserved). This is a premise of the whole decision —
if it did not hold, "definition-scan order" would be meaningless after a reload.

### 2a. What the previous ordering rules DID establish, and still holds

The refutations above kill the *general* rules, not these bounded measurements:

- With a **parking** non-interrupting body sharing an enclosing scope, an
  interrupting event-sub-process fire does destroy the work the non-interrupting
  arm just created (evidence CTL-3). Under definition-scan order that outcome is
  now reachable, and is the author's to avoid by declaration order.
- The conflict is **bounded to a shared enclosing scope** (evidence CTL-4): arms
  in sibling scopes do not interact in either order.
- ⚠ **Both CTL-3 and CTL-4 are white-box, direct-call measurements** — they invoke
  `fireEventTriggeredSubprocessArm` rather than driving `Step`, because firing two
  same-family arms in one delivery is exactly what this ADR adds. That caveat
  matters here specifically: [ADR-0172](0172-an-event-subprocess-arm-checks-instance-status.md)
  Correction 1 exists *because* a direct-call result was refuted once run
  end-to-end. Plan Phase 2 case 4b is the end-to-end re-verification.
- ⚠ `ASSUMPTION (unverified)`: the ancestor/descendant case — a non-interrupting
  arm nested *inside* the interrupting arm's enclosing scope.

### 3. The per-iteration guard is INHERITED, and WIDENED by ADR-0172

ADR-0169 already folded tiers 1–3 into a slice of lookup-and-fire closures with a
per-iteration re-check, and anticipated this delivery: *"a fourth arm family added
later inherits this guard instead of needing another copy."* The fan-out builds a
**longer `tiers` slice** rather than a new control structure, so the re-check runs
**per arm** instead of per family for free.

⚠ **The parked draft's predicate `s.Status != StatusRunning` must not re-enter.**
Refuted by execution: it conflates the two meanings of `StatusCompensating`, and
was measured swallowing a legitimate signal and stranding an instance forever.

The predicate is **not** `IsTerminal()` either, as this ADR first stated.
[ADR-0172](0172-an-event-subprocess-arm-checks-instance-status.md) replaces it in
the same bundle with `spawnsNewWork()`, which additionally excludes an instance
inside a **terminating** compensation walk. The audit showed why the narrower
predicate is not enough here: `IsTerminal()` is false for `Compensating`, so
within ONE delivery a tier-2 arm whose drive raises an uncaught error can leave
the instance in a terminating rollback and a later tier then fires into it. **The
fan-out multiplies exactly that window**, which is the concrete form of this
bundle's "0158 makes 0172's hole routinely reachable" claim.

### 4. De-duplicate identities, first-in-slice-order wins

`model.Validate` still accepts duplicate node ids, two flows between the same
pair, **and duplicate flow ids** (evidence M8). ADR-0167's strict decoding rejects
unknown *fields*; it added no structural uniqueness check. So two arms can collide
on one identity tuple.

The `…IDsBySignal` wrappers therefore de-duplicate identities, and such a
definition degrades to **one fire per identity** rather than a double fire.
**The tie-break is FIRST IN SLICE ORDER** — consistent with Decision 2 — and it is
load-bearing, not cosmetic.

⚠ **Two corrections the audit forced here.**

1. This ADR previously said the colliding arms are *"identical in **every**
   field"*. **False.** Measured: two same-id boundary nodes produced arms
   differing in `NonInterrupting` (`false`/`true`) **and** `Action` (`""` /
   `"audit-action"`) while colliding on `(HostToken, BoundaryNode)`. The de-dup
   therefore **chooses between materially different arms** — including whether the
   delivery interrupts the host at all. Hence the tie-break must be stated, which
   it now is.
2. This ADR previously recorded an "accepted degradation" that de-dup on
   `(GatewayToken, CatchNode)` *"discards the loser's distinct `Flow`, which is
   what `resolveGatewayWin`'s fallback branch reads"*. **Fabricated.**
   `armedEvent.Flow` is never read: mutating it to `"MUTATED"` leaves the suite
   `EXIT=0`, and `resolveGatewayWin`'s fallback reads `moveAlongSingleFlow`, not
   `ae.Flow`. The sentence is deleted rather than softened.

### 5. Tier 1 fans out across gateway tokens, not within a gateway

`resolveGatewayWin` removes **every** arm of the resolved gateway
(`engine/step_gateways.go:266`, evidence M4), so two same-signal arms on one
gateway token can never both fire — the second re-resolves to nil. That preserves
first-event-wins, the correct event-based-gateway semantics. Tier 1's plurality is
meaningful only **across distinct gateway tokens**.

To keep that unforgeable the delivery records the gateway tokens it has resolved
and skips any later identity naming one. Without it an identity can be re-created
*within* the delivery: `resolveGatewayWin` does not consume the gateway token, so
a branch looping back re-arms `(GatewayToken, CatchNode)` byte-identically
(evidence M5).

⚠ The obvious direct loop is **rejected by `model.Validate`**
(`gateway both splits and joins`). The ABA needs a **merge-gateway** shape. Without
that detail a reviewer would delete this guard as dead code.

⚠ **ABA is guarded for gateways only.** The boundary and event-sub-process
families were checked and found **structurally unable** to re-create a retired
identity within one delivery — but both arguments rest on a premise this ADR must
name rather than assume: **token and scope id uniqueness**, which `nextID`
delegates to a pluggable `IDGenerator` (ADR-0149) in production. A consumer
supplying a generator that reissues ids invalidates both arguments *and* the
gateway guard. Recorded as a stated precondition, not a proof.

### 6. `EnclosingScopeID == ""` is a valid identity

The root scope is named by the empty string (evidence M3): a root arm carries `""`
and fires — opens a scope, places a token, emits a live `InvokeAction`. An
ADR-0152-style empty-key guard on `eventTriggeredSubprocessArmByID` would
**silently disable every top-level event sub-process**. The existing asymmetry is
deliberate: `removeArmedEventsForGateway` and `removeBoundaryArmsForHost`
early-return on `""`; `removeEventTriggeredSubprocessArmsForScope` does not.

⚠ The `…IDsBySignal` helpers' own empty-**name** guard is **defence in depth**, not
the primary defence: `validateTriggerKey` rejects an empty signal name at `Step`
entry, before `handleSignalReceived` runs. Labelled as such so a later reader does
not mistake it for the load-bearing check (or delete it as redundant).

### 7. Unchanged

`markMatched` is **reused, never inlined**: measured (evidence D7) 4 calls produce
exactly 1 `mergeVars`, 0 produce 0, so once-only merge survives the fan-out for
free — but only while the fan-out calls the latch rather than inlining
`mergeVars` into each fire. Timer and message dispatch, cross-family precedence,
and the all-or-nothing error contract are untouched.

## Consequences

**Positive.**

- Broadcast semantics apply consistently: across families (already true) and
  within each family (new).
- Closes the gap ADR-0154 explicitly recorded as left open.
- Closes the intra-delivery arm-creation defect as a side effect of the snapshot.
- Engine-pure — no new port, storage or transport.
- Removes three now-unused singular wrappers. Measured (evidence D4): each had
  exactly one call site, all in `handleSignalReceived`, zero in tests.
  ⚠ **AMENDED DURING IMPLEMENTATION.** This ADR originally added *"the shared
  generic `armBySignal` must stay — `engine/state_arms_test.go:67` calls it
  directly"*. Once the three wrappers were deleted, that test caller was its
  **only** caller: `armBySignal` became production-dead, kept alive solely by a
  test of itself. It is deleted too, along with `TestArmBySignal`;
  `armIDsBySignal` fully replaces it and carries the same ADR-0152 empty-key
  guard with its own tests. `armByTimer` and `armByMessage` are untouched — both
  still have production callers via `dispatchArmCascade`.

**Negative / accepted costs.**

- **Breaking in two directions**, and the second is the one a recap sentence gets
  wrong. Several same-named arms now fire where one did before — **and some
  deliveries now fire FEWER arms**, because an arm created during the delivery is
  no longer in the snapshot. *This is not a pure superset of today's behaviour.*
- No opt-out: the previous behaviour was a defect, not a configuration. v0.1.0 is
  not tagged, so there is no compatibility obligation.
- A delivery firing several interrupting arms produces a larger single
  `StepResult`, bounded per step by the definition's arm count — **not** bounded
  across steps, since non-interrupting arms stay armed and `performThrowSignal`
  publishes with no self-exclusion, so a signal-throwing loop can amplify.
  ⚠ The parked draft stated `2^n` as fact; it is **not** restated as fact here —
  `runtime/` is Docker-gated and the figure was never executed.
- **Ordering is outcome-affecting and author-controlled** — see Decision 2. It can
  decide not only how many tokens survive but, measured, **the instance's final
  status**: two boundary arms on one host, one of whose branches force-terminates,
  yield `terminated` or `completed` depending purely on declaration order. This
  ADR does not hide that behind a sort.
- **Cross-family annihilation is multiplied, and the per-family rule cannot reach
  it.** Measured: tier 2 mints a human task and tier 3 cancels it **inside one
  delivery** (`UpdateTask{i1-h1 cancelled}`, `UpdateTask{i1-h2 cancelled}`, the
  second task's `AwaitHuman` dropped by ADR-0161's filter, a `boundary_interrupted`
  visit for work that never ran). That is the same pathology Decision 2 wrestles
  with, occurring **across** families, where the cross-family precedence — which
  §4 of the spec deliberately does not change — puts the widest blast radius last.
  Today it is one task per delivery; after fan-out it is N. Recorded, not fixed.
- **Operational shape of a wide delivery.** Measured: 100 matching arms produce
  **200 commands and 100 `drive` calls in ~2.6 ms**, in a **single** `StepResult`
  and therefore a **single outbox transaction**. The per-step bound is sound; the
  operational note is that consumers sizing outbox batches or transaction limits
  should know one signal can now produce a delivery this wide.
- An error in any tier still discards the whole delivery (zero `StepResult`,
  measured — evidence D8). On `main` only the single first-match arm could fail;
  now any of N can, and the error is deterministic, so the signal becomes
  permanently undeliverable to the healthy arms. ⚠ This already **disagrees** with
  ADR-0169, which deliberately *returns* partial commands on a mid-delivery
  terminal. The fan-out widens the window without resolving the disagreement;
  per-arm isolation needs an incident model this delivery does not have.
- **Micro mode is scoped out.** Measured (evidence D9), `snapshotIDs` is taken
  over tokens Micro has not driven to their park yet, so an intermediate signal
  catch is silently missed while the signal is still consumed. That is a
  **pre-existing defect independent of this ADR** and is backlogged. Acceptance
  tests run in Macro; in Micro `len(Commands)` is not a proxy for "arms fired".

### Relationship to ADR-0124

ADR-0124 Decision item 3 records "per-delivery-once" as preserved, resting on
"by-name lookup returns the first match". This ADR removes that premise.
Per-delivery-once remains true **per arm** — the snapshot fires each identity at
most once — but is no longer true **per family**.
`TestNonInterruptingBoundarySignalNoSelfCascade` still holds unchanged: one arm
yields one snapshot entry and one fire. That test was mutation-verified to be
capable of failing (evidence M10: two compiling mutations, two REDs), and its
fixture genuinely declares the construct under test.

⚠ ADR-0124 Decision item **4** contains a separate false sentence — that a
lingering arm in a terminal snapshot is harmless because
`fireEventTriggeredSubprocessArm` is "status-guarded to no-op on a non-`Running`
instance". That claim is [ADR-0172](0172-an-event-subprocess-arm-checks-instance-status.md)'s
subject, not this one's, and is corrected there.

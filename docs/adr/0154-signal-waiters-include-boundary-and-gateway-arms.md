# 154. SignalWaiters includes boundary and event-gateway signal arms

- Status: Accepted
- Date: 2026-07-28

## Context

ADR-0123 made `InstanceState` the single authority for "what can wake this
instance", so the runtime never has to enumerate constructs itself. Two methods
implement it: `MessageWaiters()` and `SignalWaiters()`.

They diverged. `MessageWaiters()` unions **four** sources — token
`AwaitMessage`, armed message boundaries, event-based-gateway message arms, and
message-triggered event sub-process arms. `SignalWaiters()` unioned only
**two** — token `AwaitSignal` and signal-triggered event sub-process arms.

Both omitted sources can carry a signal:

- signal boundary arms — `arm.Signal` is set in `armBoundaries`
  (`engine/step_boundaries.go`)
- event-based-gateway signal arms — `ae.Signal` is set by
  `eventBasedGatewayStrategy` (`engine/step_nodes.go`)

The omission was not recoverable downstream. `Token.AwaitSignal` is set in
exactly one place — the intermediate catch strategy — so a host activity with an
attached signal boundary parks on `AwaitCommand` and the signal name exists
*only* on the boundary arm. `syncSignalBus` therefore called
`SignalBus.Sync(id, nil)`, `BroadcastSignal` → `SignalBus.Publish` found no
subscriber, and **the boundary never fired: the instance parked forever.** The
same held for an event-gateway signal arm, which could never win its race.

The defect is **scoped to the subscription-routed path**, and that is why it
survived undetected across four ADRs. Only delivery that has to *discover* which
instances care — `ProcessDriver.BroadcastSignal` → `SignalBus.Publish` — consults
`SignalWaiters()`. Delivery that is already addressed to a named instance does
not: `ProcessEngine.DeliverSignal` (and the `POST /instances/{id}/signals`
transport above it) resolves the instance itself and calls
`ProcessDriver.ApplyTrigger` directly, so `handleSignalReceived` dispatched
through `boundaryArmBySignal`/`armedEventBySignal` and fired the boundary
correctly. A working targeted path masked a broken broadcast path.

This is the exact defect ADR-0123 fixed on the message side, left unfixed on the
signal side. It was found by `/code-review` during the ADR-0153 refactor and
reproduced end-to-end before any fix was designed: a `UserTask` with an
interrupting signal boundary, driven to park, then broadcast — `SignalWaiters()`
returned `nil` and the instance never completed.

The failure mode is silent by construction. Omitting a source does not fail a
build or a type check; it degrades into a process that waits forever, which is
indistinguishable from a process legitimately waiting.

## Decision

We will make `SignalWaiters()` enumerate the same four sources as
`MessageWaiters()`, in the same order.

We add two exported accessors mirroring the message side one-for-one, each a
one-line application of the existing `signalNamesOf` generic seam:

- `(*InstanceState).SignalBoundaryNames()` — mirrors `MessageBoundaryWaiters()`
- `(*InstanceState).SignalArmedEventNames()` — mirrors `MessageArmedEventWaiters()`

`SignalWaiters()` becomes the union of tokens → boundaries → gateway arms →
event-subs, matching `MessageWaiters()`' order exactly.

The design admits no real fork: the message side already established the shape,
the ordering, the nil-vs-empty contract, and the naming, and the generic
`signalNamesOf`/`messageWaitersOf` helpers added by ADR-0153 already make each
accessor a single line. We record the decision rather than a spec because the
choice being made is *that the two methods must stay mirrors*, not *how* to
write them.

We also state that mirror as an invariant in `SignalWaiters()`' doc comment,
naming the consequence of breaking it, so the next construct that can await a
signal is added to both methods rather than one.

## Consequences

- A signal boundary event now fires on **broadcast**, and an event-based gateway
  with a signal arm can now win its race on broadcast. Both were previously
  unreachable that way. Targeted `DeliverSignal` behaviour is unchanged — it
  already worked.
- **This is a behaviour change, not a refactor.** An instance that is currently
  parked with an armed signal boundary will, after upgrading, begin waking on a
  broadcast of that name. For a deployment that has been silently accumulating
  such instances, the first broadcast after upgrade may interrupt many of them at
  once. That is the correct BPMN semantics and the whole point of the fix, but it
  is a live-behaviour change and should be called out in release notes.
- No new authorization exposure: `fireBoundaryArm` performs no authz check for
  any trigger kind (timer, message, or signal) — event-driven routing is
  deliberately not actor-gated, and `authz.Authorizer` governs actor-initiated
  human-task operations instead. Message boundaries already reached this path via
  `MessageBoundaryWaiters()`, and an interrupting signal event sub-process already
  cancelled enclosing-scope tokens on broadcast, so this is parity rather than a
  widened attack surface. `BroadcastSignal` is also not exposed by any transport;
  it is a consumer Go-API call.
- Signal delivery now costs a subscription per armed signal boundary and gateway
  arm, where previously those cost nothing because they were never registered.
  `SignalBus.Sync` is set-based, so duplicate names across constructs collapse.
- The two waiter methods are now symmetric, so the next reader can diff them and
  see that they agree. The asymmetry that hid this bug for four ADRs is gone.
- A regression test exists at both levels: `TestSignalWaiters_Union` pins the
  four-source union in the engine, and `TestBroadcastSignalFiresBoundary` pins
  the user-visible behaviour end-to-end as the mirror of
  `TestDeliverMessageFiresBoundary`.
- Not addressed here: `SignalWaiters()` and `MessageWaiters()` remain two
  hand-maintained enumerations that a compiler cannot force to agree. Unifying
  them over a single arm-source registry would make the mirror structural rather
  than conventional; that is a larger change and is left open.

- **Not addressed here, and important: one delivery still fires only the FIRST
  matching arm per family.** `handleSignalReceived`
  (`engine/step_triggers.go`) dispatches steps 1–3 with singular lookups
  (`armedEventBySignal` / `boundaryArmBySignal` /
  `eventTriggeredSubprocessArmBySignal`, each returning the first match), while
  step 4 loops over every matching token. First-match is correct for messages,
  which are point-to-point 1:1, and **wrong for signals**, which are broadcast.
  Two parallel `UserTask`s each carrying a signal boundary on the same name
  interrupt only one host; BPMN says a broadcast fires both.

  This defect is pre-existing and this ADR does not cause it — but it does
  *promote* it. Before this change no subscription existed for boundary or
  gateway signal arms, so those paths were reachable only via targeted
  `DeliverSignal` or an instance that also had a token on the same name. After
  it, every `BroadcastSignal` reaches them, making this an ordinary production
  path. It is recorded rather than fixed because correcting it means
  snapshot-then-loop across three arm families, with the same
  re-arm-during-delivery hazard the token path already snapshots against — a
  behaviour change with its own blast radius that deserves its own ADR and
  audit rather than being folded into this one.

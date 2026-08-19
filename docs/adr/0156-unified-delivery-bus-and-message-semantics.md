# 156. One delivery bus with a policy; message delivery fans out and also starts

- Status: ⚠ Proposed — AUDIT FAILED, revision required (see docs/plans/2026-07-29-audit-findings.md)
- Date: 2026-07-28

> ⚠⚠ **NOT IMPLEMENTED, NOT ACCEPTED.** This ADR is on `main` to reserve its number and to
> preserve the design — it lives nowhere else. It **failed its audit** and needs revision before
> anyone builds on it; the findings are in
> [`docs/plans/2026-07-29-audit-findings.md`](../plans/2026-07-29-audit-findings.md).
> Do **not** treat it as a decision of record. Imported 2026-08-19 from the parked branch
> `feat/durable-waiters-delivery-correctness`, which was deleted afterwards.


## Context

Signal delivery and message delivery were built as two unrelated mechanisms.

`signal.SignalBus` is an exported package and type with an injected
`DeliverFunc`. Message handling is inlined into `ProcessDriver` as private state
(`msgMu`, `msgWaiters`, `syncMsgWaiters`, `findMessageWaiter`).

The asymmetry is historical, not principled. The bus exists because `ThrowSignal`
is a command the **engine emits** (`runtime/processdriver_action.go`) that must
reach *other* instances, which requires an injectable collaborator holding a
closure over `ApplyTrigger`. `DeliverMessage` is only ever called from outside, so
it never forced the same factoring.

Its costs are load-bearing:

- `Subscribe` / `Unsubscribe` / `Sync` are exported but may legitimately be called
  only by the driver — a leaky abstraction with no invariant guarding it.
- `WithSignalBus` is opt-in, so forgetting it degrades signal delivery silently,
  while message correlation is always-on. Two failure postures for one concept.
- Two hand-maintained reconciliation paths that must be kept in step — exactly how
  the ADR-0154 gap survived four ADRs.
- Consumers construct the bus with a closure capturing a `driver` variable that is
  still nil at construction (`processtest/harness.go`,
  `examples/scenarios/signal_broadcast/main.go`).

The two entry points also diverge, though **not by accident**: `BroadcastSignal`
publishes to the bus **and** creates one instance per signal-start hit,
unconditionally (`runtime/processdriver_signal.go`), while `DeliverMessage` returns
after the first waiter and never reaches the message-start. ADR-0121 chose that
asymmetry deliberately and on BPMN grounds — see Alternatives rejected. Its
consequence is nonetheless awkward: a modelled message-start is unreachable for as
long as any instance happens to be parked on that name, so reachability depends on
runtime state rather than on the model.

Finally, ADR-0155 makes ambiguous message correlation **representable** for the
first time. Under `map[msgKey]string` the second waiter was destroyed at
registration, which left ADR-0125 only two options — pick one, or invent fan-out —
and it deliberately chose the former, recording fan-out as rejected. That
constraint no longer holds: the durable projection is a set, so the choice is
genuinely open and must be made afresh rather than inherited.

## Decision

### One bus, three policies

`signal.SignalBus` is retired. A single `runtime/delivery.Bus` serves both kinds,
parameterised by policy:

- `Broadcast` — every waiter on the name; selector ignored. (signal)
- `Selective` — every waiter whose selector matches. (message, default)
- `Exclusive` — exactly one; `ErrAmbiguousMessageCorrelation` when several match.
  (message, opt-in strict mode)

```go
func (b *Bus) Publish(ctx context.Context, k kernel.WaiterKind, name, selector string,
	payload map[string]any, p Policy,
	mk func(at time.Time, payload map[string]any) engine.Trigger) error
```

`mk` receives the publish instant so the trigger is stamped once and every
recipient observes the **same** `OccurredAt`.

`payload` is a **separate parameter, not captured in the `mk` closure**. An
earlier draft omitted it, which made ADR-0157 unimplementable: the ladder must
record the payload on an `UndeliveredWakeup` so `ReplayUndelivered` can rebuild
the trigger, and a closure-captured payload is not in scope at the recording site.

Two guards on the policy type, because the naive encoding is actively dangerous:

- **`Broadcast` must not be the zero value of the message-mode setting.** With
  `Broadcast Policy = iota`, a driver field of type `Policy` left uninitialised
  means `DeliverMessage` resolves recipients via `SignalWaiters` and never consults
  message waiters at all — every zero-config consumer silently mis-routing. The
  driver-level setting therefore uses its own two-valued `MessageDeliveryMode`
  type in which `Broadcast` is unrepresentable, and `NewProcessDriver` defaults it
  to `Selective` explicitly rather than relying on a zero value.
- **Fan-out is bounded.** `SignalWaiters`/`MessageWaiters` carry `Limit`/`Cursor`,
  mirroring `kernel.InstanceFilter` — the repo's *only* other cursor-paged read
  port (`ListArmed`, `ListDefinitions` and `ListDeadLettered` are unpaged, so this
  is a deliberate addition rather than an existing convention). `Publish` pages the
  fan-out rather than materialising the whole set, and `WithMaxFanout(n)` errors
  past a configured recipient count.

Both entry points become the same call plus their asymmetric start half, which
stays in the driver because it needs the definition registry:

| | waiter half | start half |
|---|---|---|
| `BroadcastSignal` | `Publish(WaiterSignal, name, "", payload, Broadcast, NewSignalReceived)` | `signalStartDefs` → always a fresh instance, never deduped |
| `DeliverMessage` | `Publish(WaiterMessage, name, key, payload, mode, NewMessageReceived)` | `uniqueMessageStartDef` → ADR-0121 deterministic id |

Written once and therefore no longer duplicable: the fan-out loop, the
`errors.Join` accumulation, the `ErrInstanceNotFound` self-heal, the CAS-retry
loop, and the ADR-0157 dead-letter hand-off.

The CAS retry is **new**, not a move: `SignalBus.Publish` called `deliver` exactly
once and joined the error. An earlier draft cited `timerFireFunc` as precedent —
**withdrawn**, because a fired one-shot timer is *consumed*, so its retry is
genuinely a no-op, whereas a non-interrupting signal arm is deliberately never
consumed (ADR-0124) and a blind retry re-fires it. The retry's actual contract is
defined in ADR-0157: retry only when nothing has committed yet.

The package is `runtime/delivery`: a package named for one of the two kinds it now
serves would be misleading, and "event" is already taken by `definition/event`.

`WithSignalBus` is removed in favour of `WithWaiterStore`; the driver constructs
the bus internally, which deletes the nil-capture dance. `delivery.Bus` stays
exported and independently constructible for testing, but a consumer-supplied bus
would need the driver's `applyTrigger` closure — precisely the cycle that produced
the dance.

`WithWaiterStore` requires **both** halves of the port — `kernel.WaiterProjection`,
which embeds `WaiterStore` and `WaiterWriter` — and construction fails
with `kernel.ErrNilDependency` when the supplied value satisfies only the read
side. Type-asserting for the writer and nil-guarding the commit hook would mean a
read-only implementation (a cache decorator, a metrics wrapper, a generated mock)
silently writes no projection, every lookup returns empty, and **all** signal and
message delivery stops with no error and no warning. That is the
`WithSignalBus`-shaped footgun this ADR claims to remove, in a harder-to-diagnose
form; a load-bearing capability is never nil-guarded into a no-op.

### Reachability from the service facade and transports

The waiter projection is useless to a consumer who cannot configure it.
`service.NewProcessEngine` gains `WithWaiterStore`, `WithUndeliveredStore` and
`WithMessageDeliveryMode`; without them the facade would land permanently in the
in-memory degraded configuration this bundle exists to eliminate. `BroadcastSignal`
— which today has no call site in `service/` or `transport/` — is exposed on the
service facade, as are ADR-0157's `ListUndelivered` / `ReplayUndelivered` /
`DeleteUndelivered`.

**No HTTP route is added, deliberately.** ADR-0154 recorded the *absence* of a
broadcast transport as a security-relevant property, and that property is
preserved rather than inherited-and-forgotten: a broadcast reaches every parked
instance awaiting a name across the whole database, which is a blast radius no
existing route has. The per-instance `POST {bp}/instances/{id}/signals` remains a
*targeted* delivery and is unchanged. A consumer who wants broadcast over HTTP
mounts `service.BroadcastSignal` behind their own policy — which is the
library-first stance this project takes everywhere else: we provide the capability
and the consumer owns the deployment shape. The same applies to the undelivered
recovery surface; unlike the outbox dead-letter admin endpoints, it is Go-API only.

### Message delivery semantics

**Fan-out is across instances. Within one instance, message dispatch stays
first-match-wins.** A message is one item delivered to a participant; it lands
once inside a given process instance. `handleMessageReceived`'s four-tier cascade
is unchanged.

`Selective` (fan-out) is the **default**; `Exclusive` (strict) is opt-in via
`WithMessageDeliveryMode`, a driver-level setting rather than a per-call argument.
Fan-out is the default because the legitimate case is real — a correlation key
naming a *scope* (key `order-42` awaited by both an order process and a shipping
sub-process) rather than a single actor — and because under the old behaviour that
second waiter was silently destroyed. Strict mode exists because for a genuinely
1:1 business message, two matching waiters is a modelling bug that fan-out would
mask as a silent double-execution.

ADR-0125's last-writer-wins **overwrite** is superseded — multiplicity is no longer
resolved by destroying a waiter. Its **WARN is re-sited, not retained** (an earlier
draft said "retained", which is not achievable: the WARN's only implementation is
in `syncMsgWaiters`, `runtime/processdriver_waiters.go`, which this ADR deletes
along with the whole registration-time reconciliation path).

It moves to `Bus.Publish`, firing when `len(ids) > 1` under `Selective`. That is a
**different trigger point with different frequency**, and the change is
load-bearing enough to state: ADR-0125's WARN fired once per *park* (only after a
deliverLoop save), whereas this one fires once per *ambiguous delivery*. A hot
correlation key that is genuinely shared will therefore warn repeatedly where it
previously warned once. Accepted — the alternative is losing the only diagnostic
for a degenerate key — but it makes the WARN rate-sensitive, so it carries the
recipient count and the message name so an operator can distinguish "two instances
share a scope key" from "this key matches everything".

Retaining it is load-bearing. The benign case for fan-out is a correlation key
naming a scope; the destructive case is a *degenerate* key. An intermediate
`ReceiveTask` with no correlation key gives **every** instance of that definition
`AwaitMessageKey == ""` (`engine/state_arms.go`), so a single
`DeliverMessage("approve","")` resumes all of them — ten thousand parked instances
from one HTTP POST, synchronously, inside one request. A `CorrelationKey`
expression that evaluates to a constant or to empty has the same effect for a
nominally-keyed await. The WARN is the only signal that distinguishes "two
instances legitimately share a scope key" from "this key is degenerate and I am
about to resume the entire table", and it costs nothing.

Fan-out width is therefore also bounded and instrumented: `WithMaxFanout(n)`
errors past a configured recipient count, and `wrkflw_delivery_recipients` records
the distribution.

### Deliver AND start

`DeliverMessage` delivers to all matching waiters **and** attempts the
message-start, joining errors — structurally identical to `BroadcastSignal`.
`ErrAmbiguousMessageStart` no longer aborts before delivery; it is joined.

## Alternatives rejected

Both message-semantics decisions above **override standing decisions in this
repository**. An earlier draft of this ADR described them as correcting accidents;
that was wrong, and the real prior reasoning is recorded here so the override is
visible rather than implicit.

### ADR-0121's correlate-then-create (superseded by the "Deliver AND start" decision)

ADR-0121 did not leave `DeliverMessage`'s precedence unchosen. It chose it three
times, and grounded it in BPMN rather than convenience:

> *"**Message start is addressed and correlation-controlled** … a running instance
> for the same key correlates the message to itself; otherwise a new instance is
> created (**correlate-to-running-first, then create**)."*
> — and, deliberately dropped as YAGNI: *"No cross-definition message *routing*
> beyond start (**correlate-then-create only**)."*

It drew the signal/message line explicitly: *"**Signal start is a broadcast, 1:N
fan-out** … no correlation is performed — it is a 'signal flare'"* versus a message
being *"addressed and correlation-controlled."* So "structurally identical to
`BroadcastSignal`" is precisely the equivalence ADR-0121 rejected.

**Why we override it anyway.** ADR-0121's asymmetry is defensible for *routing* —
which instance receives an addressed message — but it also silently suppressed a
modelled construct: a message-start was unreachable for as long as any instance
happened to be parked on that name, making reachability depend on runtime state
rather than on the model. A modeller who declares both a message-start and an
intermediate catch has declared two things and should get two things. The cost is
real and stated in Consequences: the first delivery to a parked instance creates a
full duplicate execution. That is accepted deliberately, bounded by
`WithMaxFanout`, and is the single largest behavioural risk in this bundle.

**ADR-0121's `DeliverMessage` correlate-then-create decision is superseded by this
ADR.** Its event-based-start machinery, deterministic-id dedup, and singleton
handling are all unaffected and remain in force.

### ADR-0125's point-to-point contract (superseded by the `Selective` default)

ADR-0125 did not arrive at point-to-point by accident of data structure. It
evaluated the exact proposal adopted here and rejected it:

> *"A `map[msgKey][]string` **with fan-out was rejected**: it rewrites the
> documented 1:1 point-to-point contract and **invents non-BPMN message
> semantics**. Fan-out is the signal model and already exists (`BroadcastSignal` /
> `SignalBus`); a consumer wanting many receivers models a signal, not a message."*

The waiter destroyed by the last-writer-wins overwrite was therefore the **cost
ADR-0125 knowingly accepted** to preserve that contract — not the cause of it.

**Why we override it anyway.** ADR-0125 reasoned under a constraint that no longer
holds: with `map[msgKey]string` the ambiguity was not representable, so its only
options were "pick one" or "invent fan-out". ADR-0155's durable projection is a
set, so multiplicity is representable for the first time and the choice is now
genuinely open. And the legitimate case is real: a correlation key naming a *scope*
— `order-42` awaited by both an order process and a shipping sub-process — is
ordinary modelling that ADR-0125's answer serves by silently dropping one waiter.

**What ADR-0125 got right and we keep:** its WARN. See the retained-WARN paragraph
above — a degenerate or empty key resuming an entire table is exactly the hazard
ADR-0125 was defending against, and the WARN is the only diagnostic for it.

**Honest note on the external prior art.** Spec §1.3 cites Camunda 7, Zeebe and
Flowable for "ambiguity is never silently resolved". That invariant is real, but
all three make **single-recipient the default and fan-out the explicit opt-in** —
so they support `Exclusive` as a default, not `Selective`. (Zeebe additionally does
correlate one publish across definitions, so it is evidence *for* fan-out, but not
for the invariant it was cited under.) This ADR chooses `Selective` on the
scope-key argument alone; the external engines are not authority for it, and the
spec's framing overstated them.

### Two buses, one per kind

Rejected: it duplicates the fan-out loop, the `errors.Join` accumulation, the
`ErrInstanceNotFound` self-heal, and the CAS-retry ladder. Signal and message are
one durable-subscriber channel with different delivery policies (§3.10's EIP
reading); a second bus encodes an implementation accident as an architecture.

## Consequences

**Positive.**

- The design is a net *reduction* against the two-bus alternative: one fan-out
  loop, one error join, one self-heal, one CAS retry.
- "Who is waiting" stops being two hand-maintained in-memory mechanisms and
  becomes one port, closing the class of bug ADR-0154 was.
- The message-start is no longer unreachable-while-parked, removing an
  order-dependent suppression nobody chose.
- No delivery path silently discards a match, in either mode.
- The leaky `Subscribe`/`Unsubscribe`/`Sync` surface disappears, as does the
  "forget `WithSignalBus` and signals silently break" footgun.

**Negative / accepted costs.**

- **Breaking, broadly.** `runtime/signal` is removed entirely; `WithSignalBus` is
  removed; every `examples/scenarios/*` using signals and `processtest.Harness`
  change.
- **Behaviour change — deliberately accepted, and the largest risk in this bundle.**
  A delivery that previously resumed a parked instance now *also* fires a matching
  message-start.

  An earlier draft of this ADR claimed ADR-0121's dedup made that harmless for
  keyed and singleton starts. **That claim was wrong and is withdrawn.** The dedup
  bounds the number of extra instances to one per `(name, correlationKey)` — but
  one extra instance is one extra *complete process execution*, from the start
  event, with the same payload. Concretely: order process `P` (started via
  `StartInstance`) awaits `("approve","ORD-42")`, and the same name also has a
  message-start. `DeliverMessage("approve","ORD-42", …)` resumes `P` **and**
  creates `msgstart-<sha256(name\x00key)>`, which runs the definition and executes
  its `ChargeCard` service task. Money moves twice, once, permanently.
  `uniqueMessageStartDef` scans every registered definition
  (`runtime/event_start.go`), so the start and the catch need not share a
  definition — a modeller can trigger this without either half being visible from
  the other.

  A **keyless non-singleton** start is worse in a different way: it mints a fresh
  instance on *every* delivery, so N publishes leave N instances where today they
  leave one.

  An earlier draft of this ADR called that interaction *quadratic*. **It is not,
  and the claim is withdrawn.** Resuming a message catch clears the token's
  `AwaitMessage`/`AwaitMessageKey` (`engine/step_triggers.go`), and
  `MessageWaiters()` reports only tokens with a non-empty `AwaitMessage` — so a
  resumed instance stops being a waiter. In a straight-line definition
  (message-start plus one intermediate catch on the same name) exactly **one**
  instance is parked at any moment: the one the previous publish created. N
  publishes therefore cost N instances and ~N deliveries — **linear**, not
  N(N−1)/2.

  Quadratic growth *is* reachable, but only when the flow **loops back** to the
  catch so earlier instances re-park and accumulate. That is a narrower modelling
  shape and should be called out as such rather than presented as the default
  outcome.

  The project owner was shown this analysis and chose unconditional
  deliver-and-start anyway, for symmetry with `BroadcastSignal`. It is an accepted
  cost, not an oversight. The mitigations are bounding and observability rather
  than precedence: `WithMaxFanout(n)` caps recipients per publish and errors past
  the bound, and `wrkflw_delivery_recipients` makes the width visible. **A consumer
  who models a message-start and an intermediate catch on the same message name
  must expect both to fire.**
- **Behaviour change:** ambiguous correlation delivers to all matching instances
  by default where it previously delivered to one. A consumer relying on the old
  suppression must opt into `Exclusive`.
- The BPMN message/signal distinction narrows in the default configuration: both
  fan out, differing in whether a selector applies. The distinction is preserved
  where it is load-bearing — message keeps correlation, start dedup, and
  first-match-within-an-instance.
- Message buffering / TTL (EIP's Message Expiration, Zeebe's model) is **not**
  adopted: a message with no waiter and no start stays a clean no-op. This
  interacts with deliver-and-start — for a keyless non-singleton start an early
  message mints an instance rather than waiting for the waiter about to appear.
  Naming the omitted pattern is deliberate; adopting it needs its own ADR.

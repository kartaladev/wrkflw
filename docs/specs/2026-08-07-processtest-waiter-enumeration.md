# Spec — `processtest` sees every signal and message waiter source

**Status:** IMPLEMENTED. Audited (rule #9), findings folded; then one further
decision (D4's fingerprint inputs) refuted by execution — see §2.5.
**Date:** 2026-08-07
**ADR:** `docs/adr/0166-processtest-delegates-waiter-enumeration.md`
**Plan:** `docs/plans/2026-08-07-processtest-waiter-enumeration.md`

## 1. Problem

`processtest.Classify` derives its two discrete await fields from **token fields
only**:

```go
p.AwaitingSignals  = distinctAwaits(state.Tokens, func(t engine.Token) string { return t.AwaitSignal })
p.AwaitingMessages = distinctAwaits(state.Tokens, func(t engine.Token) string { return t.AwaitMessage })
```

The engine enumerates **four** sources — token awaits, boundary arms,
event-based-gateway arms, and event-subprocess arms — behind
`InstanceState.SignalWaiters()` and `InstanceState.MessageWaiters()`.
`processtest` re-derives source 1 and silently drops 2–4.

Consequence: `Harness.PublishSignal` / `Harness.DeliverMessage` iterate an empty
list and return `Pass()` forever, so a consumer cannot drive a definition parked
purely on a **signal or message** arm. This is live in the public consumer
harness and blocks delivery 3 (ADR-0158)'s headline scenario.

⚠ **Scope, stated precisely because an earlier draft over-claimed it.** This
delivery closes the gap for **signals and messages only**. `Park.HasArmedTimers`
carries the identical one-source defect and is **deliberately left open** — see
§4. After this delivery a definition parked purely on a *timer* arm is still
undriveable through the harness.

It is the same defect class as ADR-0154, one layer up. `SignalWaiters`' own doc
comment records the trap:

> Omitting a source here does not fail loudly — the runtime simply never
> subscribes the name, and the instance parks forever (the bug ADR-0154 fixed).

`processtest` did not omit a source *inside* `SignalWaiters`; it declined to call
it.

## 2. Measured ground truth

Every claim in this section was produced by running a probe and recording the
output, against `main` @ `abccb96` unless stated. Claims marked **[audit]** were
produced by the rule-#9 auditors and re-verified.

### 2.1 The gap

**Signal boundary on a user task:**

```
Reason                = human-task
Park.AwaitingSignals  = []string(nil)          <-- what a ParkHandler sees
state.SignalWaiters() = []string{"escalate"}   <-- what the engine says
token[0] node=approve awaitSignal="" awaitCommand="d9q639p83g3n53s3bjrg"
```

**Message boundary with a correlation key:**

```
Park.AwaitingMessages  = []string(nil)
state.MessageWaiters() = []engine.MessageWaiter{{Name:"Cancelled", CorrelationKey:"order-1"}}
```

**Event-subprocess signal arm:**

```
Park.AwaitingSignals         = []string(nil)
state.SignalWaiters()        = []string{"abort"}
```

**Event-based-gateway arm** (with the signal-side fix applied):

```
Reason                 = signal
Park.AwaitingSignals   = []string{"approved"}
Park.AwaitingMessages  = []string(nil)                          <-- message side still broken
state.MessageWaiters() = []engine.MessageWaiter{{Name:"Cancel", CorrelationKey:""}}
```

The gap covers **both signals and messages**, across **all three** non-token
sources.

### 2.2 The non-interrupting loop — and the probe that refuted its own first reading

Non-interrupting signal boundary on a user task, driven with
`Chain(PublishSignal("ping"), CompleteTasks(...))`, **downstream action NOT
registered**:

```
drive err    = <nil>
final status = failed
```

That reads as "no loop". It is not — the instance failed early because the
unregistered action errored, masking the behaviour. With the action registered:

```
at park: Reason=human-task AwaitingSignals=[]string{"ping"}
drive err    = drive step limit exceeded after 1000 steps (last park: human-task)
final status = running
```

The loop is real and is **created by this fix**: a token signal-catch is
*consumed* when it fires and cannot re-match; a non-interrupting arm stays armed
indefinitely.

### 2.3 What the audit refuted **[audit]**

**(a) An earlier draft's cited `Reason`-shift example does not exist.** It claimed
"a ServiceTask with a signal boundary moves `async-child` → `signal`". Re-run
directly:

```
status = completed    Reason = terminal    SignalWaiters = []string(nil)
```

A plain `ServiceTask` executes synchronously and never parks. The sentence was
asserted, not measured, and is deleted.

**(b) The real shift set**, measured:

| Shape | Before | After |
|---|---|---|
| Event-based gateway, signal arm | `async-child` (Node `evtgw`) | `signal` |
| ReceiveTask + signal boundary | `message` (Node `recv`) | `signal` |
| Timer catch + any live arm | `async-child`/`unknown` | `signal`/`message` |
| UserTask + signal boundary | `human-task` | `human-task` (unchanged) |

**(c) D3 breaks the shipped `AutoTimers()` recipe.** `harnessEnv.classify`
(`processtest/harness.go:305`) promotes a timer catch to `ReasonTimer` **only**
from `ReasonAsyncChild`/`ReasonUnknown`. An arm displaces that, the promotion
never fires, and `AutoTimers()` — which acts only on `ReasonTimer` — passes
forever:

```
PRE-FIX:  Reason=async-child Node="t-catch"  drive err=<nil>  status=completed
POST-FIX: Reason=signal      Node=""         drive err=unhandled park: signal at node ""  status=running
```

**(d) `Park.Node` collapses to `""` for arm-derived parks.** `Classify` resolves
`Node` via `firstNodeWhere(state.Tokens, t.AwaitSignal != "")`, and nothing sets
`Token.AwaitSignal` for an arm. Measured `Node=""` on three independent shapes
where the pre-fix value was `evtgw` / `recv` / `t-catch`.

**(e) Fire-once-per-name breaks a legitimate passing test.**
`start → catch("go") → catch("go") → end` driven with `PublishSignal("go")`:

```
PRISTINE:            err=<nil>  status=completed
D1+D2, no fire-once: err=<nil>  status=completed
D1+D2+fire-once:     err=unhandled park: signal at node "c2"  status=running
```

Two sequential catches of one name is ordinary BPMN. The justification "never a
useful test intent" is falsified by a two-node definition.

**(f) A bare `bool` fire-flag is a data race** under `go test -race` when one
handler value drives two concurrent instances — in a harness that explicitly
documents race-freedom (`harness.go:46-49`).

**(g) `HasArmedTimers` has the identical defect.** `len(state.Timers) > 0` reads
one source. Boundary timer on a user task:

```
len(state.Timers) = 0    len(st.Boundaries) = 1    HasArmedTimers = false
```

**(h) `processtest/README.md:124` publishes the `Park` struct** with
`AwaitingMessages []string`, so it is a **fourth** consumer of the type, not
counted by an earlier draft.

**(i) Rows 5–6 of §5 named the wrong sentinel.** A top-level handler returning
`Pass()` on a non-terminal park yields `ErrUnhandledPark`, not
`ErrDriveLimitExceeded`. Measured: `isUnhandled=true isLimit=false`.

### 2.4 Blast radius

With the full fix applied as a scratch patch, `go test ./processtest/...` is
**EXIT=0**, and so are `./engine`, `./runtime/{calllink,signal,task}`,
`./service`, `./transport/http/...`. No package outside `processtest` references
`Park`, `Classify` or `Reason`.

⚠ Green in-repo tests prove only that **no existing test covers these shapes**.
Findings (c) and (e) are consumer-visible regressions that no in-repo test could
have caught, which is why "no existing test observes it" is not evidence of
safety.

In-repo consumers of `AwaitingMessages`: `park.go` (field + `Classify` + the
priority switch), `handlers.go:106`, `park_test.go:129`, and
**`processtest/README.md:124`**.

### 2.5 What IMPLEMENTATION refuted **[impl]**

The rule-#9 audit refuted two of four decisions. Execution then refuted a third —
D4's fingerprint inputs — which neither authorship nor audit caught, because both
were reasoning about token identity rather than measuring it.

**Token identity is stable across a move.** Probe on `start → c1("go") → c2("go")
→ end`, dumping every token at each park:

```
park@c1: token id="d9qkk8983g3l2a2etb3g" node="c1"  boundaries=0 armedEvents=0 evtsubs=0
park@c2: token id="d9qkk8983g3l2a2etb3g" node="c2"  boundaries=0 armedEvents=0 evtsubs=0
```

Same id, different node. So "sorted token IDs + arm counts" is byte-identical at
both parks. Fix: key on `tokenID@nodeID` pairs, and scope by `InstanceID` (an
arm-derived park has no awaiting token at all, so two instances of one definition
would otherwise collide on an empty token set and equal arm counts).

**Non-interrupting boundary, same probe, three deliveries:** host token id, node
and all three arm counts are identical at every park — so that half of D4 holds
under either fingerprint, and only row 8 discriminates.

**Row 7's measured error changed with D5 in place.** §2.3(c) recorded
`unhandled park: signal at node ""`; observed during implementation it is
`unhandled park: signal at node "t-catch"`, because D5's `Node` fallback landed
first. The RED is the same, the string is not.

### 2.6 What the PRE-DELIVERY REVIEW refuted **[review]**

Two adversarial reviewers (one correctness/API, one security), run on the
committed bundle before the owner gate. Each executed its claims against `main`
as a baseline. Both independently found the loop bug and the shared-handler bug.

**(a) A loop back to the same catch node is suppressed.** `start → c1("go") → tick
→ xor ─(n<2)→ c1`, driven with `PublishSignal("go")`:

```
main   : err=<nil>                                        status=completed  ticks=2
bundle : err=unhandled park: signal at node "c1"          status=running    ticks=1
```

**(b) The instance-wide arm counts defeat the guard.** Non-interrupting `ping`
boundary whose branch target is a task carrying its own deadline boundary, so
`len(Boundaries)` grows per firing:

```
main   : err=<nil>                                                status=completed
bundle : err=drive step limit exceeded after 1000 steps           status=running   tokens=1001
```

**(c) One last-key slot displaces across instances.** Four concurrent drives
sharing one handler, non-interrupting arm, expected one firing each:

```
observed notify counts across runs: 4, 8, 10, 17, 27, 28   (expected 4)
```

**(d) `armDerivedReason` demoted a genuine token message await.** A signal boundary
on a `ReceiveTask` beside a timer catch, driven with `AutoTimers()`:

```
main   : err=unhandled park: message at node "recv"   clock 00:00 → 00:00
bundle : err=unhandled park: signal  at node "recv"   clock 00:00 → 01:00   ← timer fired
```

**(e) `TestHandlersPassOnUnawaitedName` could not fail.** Replacing both name
guards left the package green: the handler delivers an unawaited signal, the engine
no-ops, and the drive still ends in `ErrUnhandledPark` — the same assertion. It now
asserts the `Decision` directly, and both halves go RED under that mutation.

**(f) `CompleteTasksWith`'s `memo` map was an unguarded data race** — pre-existing
shipped code, not introduced here, surfaced by sharing one handler across
concurrent drives. Fixed in this bundle since it is the same bug class in the same
file, and the harness documents concurrent drives as supported.

**Not a regression, checked explicitly:** un-bounding token catches does not make a
wrong-correlation-key delivery spin any worse than before — `main` and the reworked
tree both report `drive step limit exceeded after 1000 steps (last park: message)`,
byte-identical.

### 2.7 What `/code-review` refuted **[gate]**

The owner-invoked gate, run after the stand-ins. Four findings; the headline one
killed the waiter-COUNT key that Correction 2 had introduced.

**(a) Two sequential ARMS of one name — the second is never delivered to.**
`approve1 ⊸ esc1(signal "go") → approve2 ⊸ esc2(signal "go")`, driven with a single
`PublishSignal("go")`:

```
park1: reason=human-task node="approve1" signals=[go]  → delivered, esc1 fires
drive: unhandled park: human-task at node "approve2"   ← esc2 never delivered
```

Both parks report exactly ONE "go" waiter, so a size-keyed bound cannot tell them
apart. This is the audit's own `catch("go") → catch("go")` falsification, one level
up on the arm side.

**(b) The mirror: an arm whose branch arms the SAME name spins.** Non-interrupting
`ping` on `approve`, branch → `review` carrying another non-interrupting `ping`:
the count grows every firing, so delivery is re-authorised forever —
`drive step limit exceeded after 60 steps`. The earlier regression test covered
only the strictly weaker differently-named case.

Together (a) and (b) are why the bound keys on **which nodes are parked**, not on
how many waiters there are: the count fails to move when it should and moves when
it should not.

**(c) The `ReasonTimer` promotion is a `Harness`-only feature.** `freeEnv.classify`
is bare `Classify` and `armDerivedReason` is unexported, so a consumer on the
free-function path sees `async-child` where a Harness sees `timer`, with no
exported escape hatch. Documented in `drive.go` and the README rather than changed —
the free drive owns no scheduler and structurally cannot see an armed timer.

**(d) The new `CompleteTasksWith` mutex was held across the consumer's `decide`
callback**, which previously ran lock-free — serialising, or deadlocking, two
drives sharing one handler value. The lock now covers the map only, with a
re-check on insert.

**Cleared by the gate:** the `AutoTimers()` carve-out holds for both intermediate
and retry timers beside a live arm; the event-gateway timer-arm gap fails
identically on `main` (pre-existing, disclosed); no other repo caller of
`Park.AwaitingMessages`; `distinctWaiters`' comparable map key and `deliverFor`'s
nil-map read are sound.

## 3. Decisions

### D1 — `Classify` delegates to the engine's waiter authorities

```go
p.AwaitingSignals  = distinctStrings(state.SignalWaiters())
p.AwaitingMessages = distinctWaiters(state.MessageWaiters())
```

Dedup stays: `SignalWaiters` documents that it may return duplicates and does not
dedup, while `Park` documents these fields as *distinct*. Messages dedup on the
**`{Name, CorrelationKey}` pair** — two arms awaiting one name under different
keys are different waiters.

### D2 — `Park.AwaitingMessages` becomes `[]engine.MessageWaiter` (BREAKING)

The engine tracks a correlation key; `[]string` discards it.

⚠ **Rationale corrected after audit (f/F9).** An earlier draft justified this as
"turns a 1000-step spin into something diagnosable". That is false once D4 is in
the same bundle: measured, the drive error for a wrong-key delivery is
**byte-identical** before and after this delivery (`unhandled park: human-task at
node "approve"`). The true benefit is narrower and still worth the break: a
**custom** handler can read the expected key off the `Park` it already has,
instead of having no way to discover it. `DeliverMessage` continues to match by
name only (§4).

`AwaitingSignals` stays `[]string` — `SignalWaiters()` returns `[]string`, and
mirroring the engine's own signatures avoids reintroducing the second
enumeration this ADR exists to delete.

### D3 — arms compete in the existing ladder, **and the timer promotion is widened**

Arms compete for the primary `Reason` as token awaits do; no new `Reason` value.

**`harnessEnv.classify` (`harness.go:305`) must be widened in the same change**,
or measurement (c) ships as a regression: its promotion to `ReasonTimer` extends
to `ReasonSignal`/`ReasonMessage` **when the reason is arm-derived** — i.e. when
no token carries the corresponding await. A genuine token signal-catch keeps
outranking a timer; only an arm-derived reason yields to it.

### D4 — delivery is bounded per **park state**, not per name

Answers (e) and the loop in §2.2 together. `PublishSignal`/`DeliverMessage`
record a fingerprint of the park they last delivered against, and deliver again
only when it changes.

**The bound applies to ARMS only.** A token signal/message catch is *consumed*
when it fires and cannot re-match, so it is never bounded and behaves exactly as
on `main`. An arm stays armed, so an arm-derived delivery fires once per
**instance** per **parked node**: the first park delivers, and a later park
delivers again only if some token is parked on a node the handler has not already
fired at. State is a per-instance map under a mutex.

Keying on the waiter COUNT instead is wrong in both directions — see §2.7.

- Two sequential catches, and a loop back to one catch: token-derived ⇒ unbounded
  ⇒ fire every time.
- Two arms of one name on different activities: different parked nodes ⇒ both fire.
- Non-interrupting arm re-matching its own park: same parked node ⇒ fires once,
  whatever else the instance arms meanwhile.

> **Correction — the fingerprint idea was refuted twice, and then discarded.**
> This paragraph first specified a fingerprint over token ids; implementation
> showed a token keeps its id across `c1 → c2` (§2.5), so `nodeID` and `InstanceID`
> were added. The pre-delivery review (§2.6) then refuted *that* too — a loop
> re-enters the same node, the arm-slice counts are instance-wide, and one slot
> displaces across instances. The fingerprint is gone; the arms-only bound above
> replaces it.

**Race-safe (f):** the fingerprint is guarded by a mutex, not a bare `bool`, so a
handler value shared across concurrent drives is safe under `-race`.

### D5 — `Node` falls back rather than collapsing to `""`

Answers (d). When the primary reason is arm-derived and no token carries the
await, `Node` falls back to the first **waiting** token's node id, rather than
the empty string. `Park.Node` is documented as *"best-effort"*, and naming the
node the instance is actually parked on is strictly better than `""` in the two
errors a consumer sees (`ErrUnhandledPark`, `ErrDriveLimitExceeded`).

The arm's own node id is **not** reachable: `state.Boundaries`, `ArmedEvents` and
`EventTriggeredSubprocesses` have unexported element types, so naming the arm
would require an engine change this delivery rules out (§4).

## 4. Out of scope

- **`HasArmedTimers` and timer arms (g).** Owner-adjudicated OUT. Closing it
  needs an engine-side `TimerWaiters()`/`ArmedTimerNodes()` authority mirroring
  `SignalWaiters`, which is its own ADR. **Filed as a follow-up blocker.** Until
  then, a definition parked purely on a timer arm is undriveable through the
  harness, and no document in this bundle may claim the ADR-0154 class is closed
  outright — only that it is closed for signals and messages.
- **`DeliverMessage` matching on the correlation key.** Keeps matching by name
  only; the caller's key is what gets delivered.
- **Any change to `engine`.** `SignalWaiters`/`MessageWaiters` are correct.
- **Letting a consumer construct an arm-derived `Park` directly.** The arm slices
  are unexported, so a consumer cannot unit-test their own `ParkHandler` against
  an arm park without driving a real definition. A real harness gap; not this
  delivery's.

## 5. Testing

⚠ **Falsifiability is not uniform.** Rows marked **guard** cannot fail before the
fix — they pin a chosen semantic rather than reproduce the bug — and are owed a
**mutation** instead (plan phase 5).

| # | Test | Falsifiable today? |
|---|---|---|
| 1 | signal **boundary** arm populates `AwaitingSignals` | ✅ field is `nil` |
| 2 | **event-subprocess** signal arm populates it | ✅ field is `nil` |
| 3 | **event-gateway** signal arm populates it | ✅ field is `nil` |
| 4 | **message boundary** populates `AwaitingMessages` with a non-empty `CorrelationKey` | ✅ field is `nil`; the key does not exist in the old type |
| 5 | `PublishSignal` resolves a boundary-only park end-to-end | ✅ `ErrUnhandledPark` today |
| 6 | `DeliverMessage` resolves a message-boundary-only park | ✅ `ErrUnhandledPark` today |
| 7 | `AutoTimers()` still drives a timer catch that coexists with a live arm | ✅ **guard on D3** — fails with D1 and without D3's widening |
| 8 | two sequential catches of one signal name both fire | ✅ **guard on D4** — fails with fire-once-per-name |
| 9 | `Node` is non-empty for an arm-derived park | ✅ **guard on D5** — `""` without it |
| 10 | non-interrupting boundary terminates instead of spinning | ❌ **guard** — passes today; fails only with D1 and without D4 |
| 11 | duplicate names across two arms collapse to one entry | ❌ **guard** — field empty either way |
| 12 | two arms, same name, different keys stay two entries | ❌ **guard** — field empty either way |
| 13 | one handler value across two concurrent drives is race-free | ❌ **guard** — needs `-race`; fails with a bare `bool` |

Rows 7, 8 and 9 are falsifiable **against the partially-implemented tree**, which
is why the plan sequences them so their RED is actually observed.

⚠ Rows 1–4 **cannot** be table rows in `park_test.go`'s existing `TestClassify`:
that table builds `engine.InstanceState{Tokens: …}` literals, and the arm slices
have unexported element types. They must be harness-driven — `h.Start(...)` then
`Classify(st)`.

## 6. Verification

- `go test ./processtest/...` EXIT=0, and `go test -race ./processtest/...` EXIT=0
  — **done**, EXIT=0 both.
- `go test ./...` EXIT=0 (needs Docker — owner approval) — **done, twice**: 64
  packages, 0 failures, 0 data races under `-race`, repo coverage 73.8 % (baseline
  73.6 %). Re-run after `/code-review`'s fixes, because the first run certified a
  tree those fixes had changed.
- `golangci-lint run ./...` 0 issues — **done** for `./processtest/...`.
- `processtest` coverage ≥ **88.0 %** (measured baseline; do not regress) —
  **90.2 %**. Every symbol this delivery added is at 100 %, including
  `armDerivedReason`; only the defensive empty-name skips in the two dedup helpers
  are uncovered.
- `processtest/README.md` shows the new `Park` type and the corrected `Reason`
  prose — **done**, plus the per-park-state delivery bound.

## 7. Audit record (rule #9)

Two Opus auditors, distinct lenses (correctness/completeness; API/scope/honesty),
both required to execute rather than read. **17 findings, 17 accepted**, three
escalated to the owner because they reversed or rescoped a decision:

- **D3** → widen the `harnessEnv.classify` promotion (owner-chosen over reverting
  to a token-first ladder).
- **D4** → bound per park state (owner-chosen over an opt-in repeating variant).
- **`HasArmedTimers`** → out of scope, claims narrowed, follow-up filed.

The audit refuted **two of the four original decisions** and one factual claim in
the spec's own "measured" section. The lesson is recorded rather than smoothed
over: the false sentence — a `Reason`-shift example naming a shape that never
parks — sat in a document whose §2 is otherwise a wall of real measurements.
Being surrounded by evidence is not evidence.

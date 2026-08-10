# Signal arm fan-out and the event-sub-process status guard

- Date: 2026-08-10
- Status: **design complete; awaiting the rule-#9 adversarial audit.** Not an
  input to implementation until that audit has run.
- ADRs: **0158** (fan-out), **0172** (event-sub-process status guard)
- Scope: `engine/` only. No new port, no storage, no transport, no public API
  **signature** change. `StepResult.Commands` and
  `InstanceState.{Tokens,Boundaries,Scopes,EventTriggeredSubprocesses}` change
  *content* — that is the breaking part.
- Base: all measurements against `main` @ `571a380` unless stated otherwise.

> **Premise Discipline.** Claims about current behaviour in this document were
> EXECUTED, with the observed output recorded in
> `docs/specs/2026-08-10-signal-fanout-premise-evidence.md`. Claims that could not
> be executed are marked `ASSUMPTION (unverified)` in place. Claims **refuted** by
> execution are listed in §6 rather than quietly dropped — inherited ones from the
> parked draft, and **this bundle's own**, which its rule-#9 audit refuted after
> the first draft was written.
>
> ⚠ This banner previously asserted that *"Every"* claim was executed and that
> *"Two"* were refuted, while §6 listed more and the commit message a different
> number again. **That was itself an unverified quantifier, in the paragraph
> asserting quantifier discipline.** §6 is the enumeration; this banner does not
> count it.

---

## 1. Problems

### 1.1 A broadcast signal fires only the FIRST arm in each family

`handleSignalReceived` (`engine/step_triggers.go`, `func` at `:741`) dispatches a
`SignalReceived` through four tiers:

| tier | source | lookup | plural today? |
|---|---|---|---|
| 1 | event-based-gateway arms | `armedEventBySignal` | **no** — first match |
| 2 | boundary arms | `boundaryArmBySignal` | **no** — first match |
| 3 | event-sub-process arms | `eventTriggeredSubprocessArmBySignal` | **no** — first match |
| 4 | standalone parked tokens | `tokenIDsAwaitingSignal` + loop | **yes** |

All three singular lookups funnel into `armBySignal` (`engine/state_arms.go:225`),
which returns at the first arm whose `Signal` equals the name.

**Executed (evidence D1).** A parallel fork into two `UserTask`s, each carrying an
interrupting signal boundary on `"escalate"`, given ONE `SignalReceived`:

```
BEFORE status=running tokens=2  len(Boundaries)=2  tasks: i1-h1 unclaimed, i1-h2 unclaimed
AFTER  status=running tokens=1  len(Boundaries)=1  tasks: i1-h1 cancelled, i1-h2 unclaimed
AFTER  commands=1: engine.UpdateTask{TaskID:i1-h1 State:cancelled}
```

Exactly one host is interrupted. `taskB` stays parked with `bndB` **still armed**
and its task still `unclaimed`. BPMN fires both.

The defect is pre-existing, but **ADR-0154 changed its reachability**: before it,
signal boundary arms and event-gateway signal arms were never subscribed on the
`SignalBus`, so a broadcast never reached them. ADR-0154 fixed the subscription
and promoted this to a routine production path, recording it in its own
Consequences as knowingly left open (`ADR-0154`, "one delivery still fires only
the FIRST matching arm per family").

#### 1.1.1 Through the public harness it is a SILENT WRONG ANSWER

**Executed (evidence D2).** The same shape driven through `processtest`:

| drive | result |
|---|---|
| `PublishSignal` alone | first arm fires, then `DriveToCompletion err=workflow-processtest: unhandled park: human-task at node "taskB"`; `notify` count **1** |
| two explicit `ApplyTrigger` deliveries | **both** arms fire, instance completes, `notify` count **2** |
| **`Chain(PublishSignal, CompleteTasks)`** — the realistic consumer shape | **`err=<nil>`, instance `completed`**, branch A escalated, branch B completed normally down `endB`, `notify` count **1** |

The third row is the one that matters: the shape a consumer actually writes is
**green today while being semantically wrong**. The second row proves `bndB` is
live and reachable — the engine *defers* it to the next delivery, it does not
drop it. The defect is precisely *one delivery fires one arm per family*.

⚠ **Sub-finding (D2-x), load-bearing for the test plan.**
`InstanceState.SignalWaiters()` returns `[escalate escalate]` — the engine
authority exposes the multiplicity. `processtest.Classify`'s
`Park.AwaitingSignals` collapses it to `[escalate]`. An acceptance test written
against `Park.AwaitingSignals` **cannot see** "two arms on one name"; it must use
`st.SignalWaiters()`.

### 1.2 A later tier fires an arm the delivery ITSELF created

This is **not** in the parked draft, and it is a defect in its own right.

Each tier performs its own lookup at the moment it runs — deliberately, and
ADR-0169's comment (`engine/step_triggers.go:792-803`) forbids hoisting them. The
unrecorded consequence: an arm armed by an **earlier tier's own drive**, which did
not exist at the delivery instant, is visible to a later tier and fires in the
same delivery. Both routes were executed (evidence CTL-1, CTL-2):

| route | fixture | measured effect today |
|---|---|---|
| tier 1 → tier 2 | gateway fire drives into a `UserTask` carrying a signal boundary on the same name | human task `i1-h1` is **minted and cancelled inside one step**; its `AwaitHuman` is dropped by ADR-0161's filter (`WARN dropping command whose awaiter this step cancelled`) |
| tier 2 → tier 3 | boundary fire drives into a `SubProcess` whose scope arms an ESP on the same name | the sub-process is **entered and torn down inside one step**; `nested-work` stays in `History` as a visited node although its action never ran |

Under BPMN single-instant broadcast semantics neither arm should catch that
signal: neither was armed when it was delivered. Tier 4 already applies exactly
this rule to tokens ("a token spawned by a non-interrupting arm during this Step
is NOT in the snapshot", `engine/step_triggers.go:764-769`); tiers 1–3 do not.

⚠ **Bounded claim.** Only these two routes were executed. The document does not
claim "tier N always feeds tier N+1".

### 1.3 An event-sub-process arm spawns work into a dying instance

`fireEventTriggeredSubprocessArm` (`engine/step_eventsubprocess.go:156`) guards on
instance status for the **root scope only**: the non-root branch checks scope
liveness and never status, and the root branch uses `s.Status != StatusRunning`.

⚠ **This section was opened against an inherited two-direction description.
Execution corrected three of the four inherited claims.** They are recorded in §6
rather than silently applied.

**The terminal formulation is REFUTED end-to-end (evidence E1b).** At the function
level a non-root arm called directly does fire on `completed`/`failed`/`terminated`
(E1a). But no public `Step` route reaches it: `endInstance` →
`cancelAllScheduledWork` already drains ESP arms **2 → 0** (ADR-0164), and on a
hand-forged legacy shape the trigger is refused at `dispatch` with
`outcome=dropped` (ADR-0165) — all three ESP delivery triggers carry
`rejectSilently`.

**The reachable defect is `StatusCompensating` (evidence E1c, E3).**
`beginCompensation` **deliberately does not drain ESP arms**
(`engine/state_arms.go:132-137`) because a walk may still resume the instance —
measured across all four `beginCompensation` writers: ESP **2 → 2** while gateway
and boundary go **1 → 0**. So the arms survive every rollback, *including the
rollbacks that terminate*:

```
rollback in flight:           status=compensating tokens=0 ESP=2 cursorActive="e2d-c1"
NON-ROOT arm during rollback: status=compensating tokens=1 ESP=1
  cmds=[{e2d-c2 nested-esp-action ...}]      ← live action dispatched to a worker
after walk finish:            status=terminated tokens=1 ESP=0
WARN trigger rejected on terminal instance ... engine.ActionCompleted outcome=dropped command_id=e2d-c2
```

Real work is dispatched to a real worker, the instance then terminates, the
worker's `ActionCompleted` is dropped, and a **terminated instance is left carrying
an orphan `TokenWaiting` token awaiting a command that can never be applied**.

**Direction (b) reproduces as inherited (evidence E2).** A *local* compensation
throw leaves the instance legitimately running (`ResumeNode:after`,
`FinalStatus:running`), yet `!= StatusRunning` silences the root arm — while the
delivery is still **consumed** (`vars=map[payload:1]` proves the payload merged
before the fire no-op'd). Signals are one-shot broadcasts, so nothing redelivers.
Driven to termination the walk resumes and a second delivery works, so the measured
cost is exactly **one silently swallowed delivery**.

**The predicate is completely uncovered (evidence E8).** Deleting
`s.Status != StatusRunning` outright leaves `./engine/...` and every container-free
package at `EXIT=0`. Nothing existing catches a regression; nothing existing
confirms a change. **The delivery owes new tests.**

---

## 2. Options considered

### 2.1 For §1.1 / §1.2 — how tiers 1–3 iterate

| option | verdict |
|---|---|
| **A. Loop each family, re-scanning for "the next match"** | **Rejected — does not terminate.** Executed (evidence M9): a non-interrupting arm is immediately re-resolvable by the same lookup after it fires, byte-identically, in BOTH the boundary and ESP families. A re-scan finds the same arm forever. |
| **B. Snapshot arm identities, then re-resolve each and fire it** | **Chosen.** Mirrors the device tier 4 already uses for tokens, and for the same reason. Also closes §1.2 for free. |
| **C. Collect arm POINTERS once and fire through them** | **Rejected — silently fires retired arms.** Executed (evidence M1): `removeArmsWhere` (`engine/state_arms.go:262`) allocates a fresh backing array and the wrappers assign it over the field, so a pointer taken earlier still addresses the **detached** array where the removed arm is intact. The fire functions take arms by value, so the dispatch would succeed on an already-retired arm. M1 also shows the hazard is **bidirectional**: a pointer to a *surviving* arm is likewise detached (`writeVisible=false`), so a write through it is silently dropped. |

Identity tuples, verified against the structs (evidence M2, compile-verified):

| family | identity | `NonInterrupting` field? |
|---|---|---|
| `armedEvent` | `(GatewayToken, CatchNode)` | **no** |
| `boundaryArm` | `(HostToken, BoundaryNode)` | yes |
| `eventTriggeredSubprocessArm` | `(EnclosingScopeID, EventSubprocessNode)` | yes |

Two constraints on the identity scheme, both executed:

- **Uniqueness is a property of well-formed definitions, not an enforced
  invariant.** `model.Validate` still accepts duplicate node ids, two flows
  between the same pair, **and duplicate flow ids** (evidence M8) — ADR-0167's
  strict decoding rejects unknown *fields*, and added no structural uniqueness
  check. Worse than previously recorded: two boundary nodes with the same id on
  the same host produce two `boundaryArm` structs identical in **every field**
  (`WHOLE arm structs identical: true`), so **no value-derived identity can
  distinguish them**. The design therefore de-duplicates identities, and a
  pathological definition degrades to one fire per identity rather than a double
  fire. ⚠ De-dup on `(GatewayToken, CatchNode)` discards the loser's distinct
  `Flow`, which is what `resolveGatewayWin`'s fallback branch reads — stated as
  an accepted degradation.
- **Identity re-creation (ABA) is possible.** `resolveGatewayWin` does not consume
  the gateway token, so a branch looping back re-arms `(GatewayToken, CatchNode)`
  byte-identically **within one `Step`** (evidence M5). ⚠ The naive direct loop is
  still rejected by `model.Validate` (`gateway both splits and joins`); the ABA
  needs a **merge-gateway** shape to be exhibited. Without that detail a reviewer
  would delete the already-resolved-gateway guard as dead code.

`EnclosingScopeID == ""` is the **valid root-scope identity**, not a missing key
(evidence M3): a root arm carries `""` and fires — opens a scope, places a token,
emits a live `InvokeAction`. An ADR-0152-style empty-key guard on the ESP
re-resolver would silently disable every top-level event sub-process. The
existing asymmetry in `state_arms.go` is deliberate and must be preserved:
`removeArmedEventsForGateway` (`:321`) and `removeBoundaryArmsForHost` (`:354`)
early-return on `""`; `removeEventTriggeredSubprocessArmsForScope` (`:389`) does
**not**.

### 2.2 Ordering within a family

| option | verdict |
|---|---|
| **A. Uniform non-interrupting-first** | Rejected — its justification was refuted (below). |
| **B. Uniform interrupting-first** (the parked draft's rule) | Rejected — likewise. |
| **C. Per-family ordering** (this spec's first choice) | **Rejected after the audit** — both family rules were derived from a single fixture whose body shape was an unstated precondition, and execution refuted both. |
| **D. Definition-scan order, no sort** | **Chosen.** |

**Why every flag-based rule fails.** Option C rested on *order each family so a
later arm cannot destroy an earlier arm's effects*. That rule is not implementable
from the `NonInterrupting` flag, because **destroyability is a property of the
arm's BODY, not of its flag**:

- *Tier 3.* Evidence CTL-3 gave the non-interrupting event sub-process a body that
  **parks** (`ni-work` = ServiceTask). With a body that **completes
  synchronously** (`ni-start → ni-send(SendTask) → ni-end`), the child scope drains
  and closes before the interrupting arm fires, so `cancelScopeSubtree` has nothing
  to destroy, and the `SendMessage` is fire-and-forget with **no `CommandID`**, so
  ADR-0161's filter cannot drop it. **Both arms take effect** — refuting
  "impossible for both to take effect in any order":

  ```
  # non-interrupting FIRST
  ESPORD afterNI: status=running tokens=1 scopes=0 esp=2
  ESPORD afterNI   cmd engine.SendMessage {Name:ni-message ...}
  ESPORD afterNI   history=[... {ni-start} {ni-send} {ni-end}]
  # interrupting FIRST (option C's rule for this tier)
  ESPORD-B afterINT   history=[... {int-start} {int-work}]   # no ni-* AT ALL
  ```

- *Tier 2.* "Non-interrupting-first lets **both** arms take effect (2 tokens)"
  assumes the non-interrupting branch leaves the instance running. Routed to a
  force-termination end:

  ```
  BNDORD afterNI: status=terminated tokens=0 ... cmd FailInstance{Err:escalated: abort}
  BNDORD >>> interrupting sibling SKIPPED by the terminal re-check; it never fires
  BNDORD-B afterINT: status=completed tokens=0     # the other order
  ```

  `tokens=0`, not 2 — and the two orders give **different terminal statuses**.

Option D therefore makes no claim execution can refute, and drops the two
opposite-direction sorts the plan itself named as the likeliest implementation
error. Ordering stays outcome-affecting and becomes **author-controlled**.

**What the CTL measurements still establish** (bounded, not general):

- With a **parking** non-interrupting body sharing an enclosing scope, the
  interrupting event-sub-process fire does destroy the work just created
  (CTL-3). Under definition-scan order that is reachable and is the author's to
  avoid by declaration order.
- The conflict is **bounded to a shared enclosing scope** (CTL-4): arms in sibling
  scopes do not interact in either order.
- ⚠ **CTL-3 and CTL-4 are white-box, direct-call measurements** — they invoke
  `fireEventTriggeredSubprocessArm` rather than driving `Step`, because firing two
  same-family arms in one delivery is exactly what this delivery adds. That caveat
  is load-bearing here: §1.3's Correction 1 exists *because* a direct-call result
  was refuted once run end-to-end. Plan Phase 2 case 4b is the end-to-end check.
- ⚠ `ASSUMPTION (unverified)`: the ancestor/descendant case — a non-interrupting
  arm nested *inside* the interrupting arm's enclosing scope.

### 2.3 Error mid-fan-out

| option | verdict |
|---|---|
| **A. All-or-nothing (today's contract)** | **Chosen.** |
| **B. Return partial commands, as ADR-0169 does for a mid-delivery terminal** | Rejected for this delivery — it changes `Step`'s error contract for every trigger kind, and per-arm isolation needs an incident model this delivery does not have. |

**Executed (evidence D8).** On a tier error `Step` returns a **zero**
`StepResult` (`reflect.DeepEqual(r2, engine.StepResult{}) == true`) and the
caller's state is byte-identical before and after — even though `markMatched`
merged `{leak:yes}` into the clone first. The clone is `cloneState`
(`engine/step_state.go:361`), called at `engine/step.go:84`. The check is not
vacuous: the caller's `Variables` is a non-empty pre-existing map, so an
un-cloned map would have been written through.

⚠ **The two paths already disagree, and fan-out widens the window.** An *error*
discards the whole delivery; a mid-delivery *terminal* (ADR-0169) deliberately
**returns** the partial commands. On `main` only the single first-match arm could
fail; with fan-out any of N arms can. The error is deterministic, so every
redelivery fails identically and the signal becomes permanently undeliverable to
the healthy arms. Accepted, and recorded as a named consequence rather than
discovered later.

⚠ **REFUTED premise (evidence D8-0).** The parked plan's suggested error source —
"a boundary whose outgoing flow targets a missing node" — does **not** error. It
logs `WARN token routed to a missing node` and parks a `TokenWaiting` token on the
non-existent node with `AwaitCommand=""`, `err=<nil>`, instance still `running`.
The real reachable error is `fireBoundaryArm`'s `outgoing flow %q not found`
(`engine/step_boundaries.go:124`), reached by arming with definition D and
delivering with a D′ that dropped the flow — a live shape, since definitions are
looked up per `Step` while arms come from persisted state. *(The missing-node
token is a permanent wedge and gets its own backlog line — §4.)*

---

## 3. Design

### 3.1 Fan-out (ADR-0158)

**Snapshot every matching arm identity per family before any dispatch; then
re-resolve each and skip it if it is gone.**

The snapshot is **mandatory twice over**: it is what makes the loop terminate
(§2.1 option A), and it is what confines the delivery to arms that existed at the
delivery instant (§1.2). Re-resolution is an **existence check only** — it must
never be used to select the next arm, or option A's non-termination returns.

New helpers in `engine/state_arms.go`:

```go
type gatewayArmID struct{ GatewayToken, CatchNode string }
type boundaryArmID struct{ HostToken, BoundaryNode string }
type eventSubArmID struct{ EnclosingScopeID, EventSubprocessNode string }

func (s *InstanceState) armedEventIDsBySignal(name string) []gatewayArmID
func (s *InstanceState) boundaryArmIDsBySignal(name string) []boundaryArmID
func (s *InstanceState) eventTriggeredSubprocessArmIDsBySignal(name string) []eventSubArmID

func (s *InstanceState) armedEventByID(id gatewayArmID) *armedEvent
func (s *InstanceState) boundaryArmByID(id boundaryArmID) *boundaryArm
func (s *InstanceState) eventTriggeredSubprocessArmByID(id eventSubArmID) *eventTriggeredSubprocessArm
```

Each `…IDsBySignal`: returns `nil` for an empty name (ADR-0152, **defence in
depth** — `validateTriggerKey` already rejects an empty name at `Step` entry);
scans in slice order; **de-duplicates identities, first-in-slice-order winning**.
`eventTriggeredSubprocessArmByID` must **not** guard an empty `EnclosingScopeID`.

**Ordering: definition-scan order in every family. There is NO sort** (§2.2).

| tier | family | order |
|---|---|---|
| 1 | `armedEvent` | slice (definition-scan) order |
| 2 | `boundaryArm` | slice (definition-scan) order |
| 3 | `eventTriggeredSubprocessArm` | slice (definition-scan) order |

⚠ Earlier drafts specified a per-family `NonInterrupting` sort. **The audit refuted
both directions by execution** (§2.2 and §6): whether an earlier arm's effects are
destroyable depends on the arm's **body**, not its flag, so no flag-based sort is
correct in general. Removing the sort also removes `slices.SortStableFunc` from
this design and the two opposite sort directions the plan itself flagged as the
likeliest implementation error.

⚠ **Slice order survives persistence** — measured across a JSON persist/reload
cycle (`[bA bB bC] → [bA bB bC]`, root `""` preserved). Without that, "definition
-scan order" would be meaningless after a reload.

**Dispatch.** ADR-0169 already folded tiers 1–3 into a slice of lookup-and-fire
closures with a per-iteration re-check, and anticipated this delivery in its own
Decision 2 ("a fourth arm family added later inherits this guard instead of
needing another copy"). The fan-out therefore **builds a longer `tiers` slice**
rather than introducing a new control structure — and ADR-0172 replaces that
loop's `IsTerminal()` with `spawnsNewWork()` in the same bundle (§3.2):

```go
gatewayIDs  := s.armedEventIDsBySignal(t.Name)
boundaryIDs := s.boundaryArmIDsBySignal(t.Name)
eventSubIDs := s.eventTriggeredSubprocessArmIDsBySignal(t.Name)

resolvedGateways := make(map[string]struct{}) // ADR-0158 ABA guard

tiers := make([]func() ([]Command, error), 0, len(gatewayIDs)+len(boundaryIDs)+len(eventSubIDs))
// one closure per snapshotted identity, in tier order, each re-resolving by
// identity and returning (nil, nil) when the arm is gone.
```

The existing loop is unchanged and now runs the terminal re-check **per arm**
instead of per family — which is exactly what the parked draft wanted from its own
status re-check, obtained by inheritance.

⚠ **The predicate is NOT re-derived.** `IsTerminal()` is inherited from ADR-0169.
The parked draft proposed `s.Status != StatusRunning`; that predicate is
**refuted by execution** and must not re-enter — see §6.

**The ABA guard.** Tier 1's plurality is meaningful only *across distinct gateway
tokens*: `resolveGatewayWin` removes every arm of the resolved gateway
(`engine/step_gateways.go:266`, evidence M4), so two same-signal arms on one
gateway can never both fire and first-event-wins is preserved. To keep that
unforgeable the delivery records resolved gateway tokens and skips any later
identity naming one, closing the byte-identical re-arm of evidence M5.

**`markMatched` is reused, never inlined.** Executed (evidence D7): 4
`markMatched` calls produce exactly **1** `mergeVars`; 0 calls produce 0. The
latch means once-only merge survives a fan-out **for free** — but only while the
fan-out calls `markMatched` rather than inlining `mergeVars` into each fire.

**Timer and message dispatch are unchanged.** Executed (evidence D5): they route
tiers 1–3 through `dispatchArmCascade` (`engine/step_arm_dispatch.go:28`) and
`handleSignalReceived` does not — instrumentation recorded **zero** cascade
entries for a signal, one for a message, two for two timers. Two message boundary
arms on one name fire exactly one arm (correct, point-to-point), and a `TimerID`
is **unique per arm** (`i1-tm1` vs `i1-tm2` for two identical 30m arms), so
fan-out is not merely undesirable for timers — it is meaningless.

⚠ The observable end state of D5b's message delivery is byte-for-byte the same as
D1's signal delivery, so a fan-out test **must distinguish the two by trigger
type, not by end state**.

**The three singular wrappers are deleted.** Executed (evidence D4): each has
exactly one call site, all inside `handleSignalReceived`, and zero test call
sites — proved by deleting them and observing `go build ./...` and `go vet ./...`
fail with exactly three errors, all in `step_triggers.go`. ⚠ **AMENDED DURING IMPLEMENTATION:** `armBySignal` was to be kept for its test
caller, but with the wrappers gone that was its only caller — it and
`TestArmBySignal` are deleted. `armByTimer`/`armByMessage` stay (production
callers via `dispatchArmCascade`). The by-timer and by-message
wrappers are a separate set and stay.

### 3.2 Event-sub-process status guard (ADR-0172)

**One predicate for every arm: a dying instance spawns no new work, whichever
scope the arm belongs to.**

```go
// ALLOW-list: an unrecognised Status fails CLOSED.
func (s *InstanceState) spawnsNewWork() bool {
    switch s.Status {
    case StatusRunning:
        return true
    case StatusCompensating:
        return !s.Compensating.walkTerminates(s.PendingCancel)
    default:
        return false // terminal, or out of range
    }
}
```

applied at **two** sites: `fireEventTriggeredSubprocessArm` (retaining the
non-root scope-liveness check below it), **and** the shared dispatch guard —
`handleSignalReceived`'s tier loop and `dispatchArmCascade` — replacing
ADR-0169's `IsTerminal()` so **every** arm family inherits it.

`walkTerminates` mirrors `stepCompensationFinish`'s own plan construction so the
two cannot drift:

| `walkMode()` | terminates? | why |
|---|---|---|
| `walkAdmin` | **yes** | `finishPlan.resume = false` |
| `walkReverse` | **yes** — by decision | resumes, but excluded; see below |
| `walkPartial` | **no** | `resume: true`, `consumePendingCancel` **not** set |
| `walkThrowTargeted` | `pendingCancel` | `resume: true` + `consumePendingCancel` |
| `walkThrowScopeWide` | `pendingCancel` | `resume: true` + `consumePendingCancel` |

⚠ **`walkMode() == walkAdmin || PendingCancel` — this spec's first predicate — is
REFUTED.** `consumePendingCancel` is **not** set on `walkPartial`
(`engine/step_compensation.go:710-722`), and `handleCancelRequested`'s deferral
predicate reads `ResumeNode != "" || ReverseNode != ""` while `walkMode()` gives
`ToNode` precedence over `ReverseNode` — so a cursor with both is `walkPartial`
*and* deferral-eligible, reachable through the public `CompensateRequested`.
Measured, such a walk **resumes** with `PendingCancel` true, and the refuted
predicate silences the arm on it — **reintroducing direction (b)**.

⚠ **A full REVERSE is excluded deliberately**, though it resumes. `ResetVars: true`
plus `finishPlan.rearmRootESP` mean firing the arm yields two concurrent tokens,
the ESP body's variables wiped beneath it, and an **interrupting one-shot arm
resurrected while its body still runs** — the hazard
`engine/step_nodes.go:390-397` already cites as the reason an equivalent re-arm was
rejected. Accepted cost: one swallowed delivery during a reverse, scoped and
documented rather than accidental.

⚠ **`FinalStatus` is unusable** — the admin full rollback terminates yet carries
`FinalStatus=running`, so a `FinalStatus` predicate classifies it backwards.

⚠ **Allow-list, not deny-list.** `Status.IsTerminal`'s godoc records that an
out-of-range value is treated as *not* terminal, so a deny-list predicate starts
firing arms on one. Measured: `engine.Status(9)` silences on `main` and **fires**
under a deny-list.

| option | verdict |
|---|---|
| **A. Same `!= StatusRunning` on the non-root branch** | **Rejected — REFUTED by execution.** Breaks `TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope`, which fires a nested interrupting ESP arm during an in-flight scope-wide throw — a legitimately running instance. A regression, not a fix. |
| **A′. `IsTerminal()` on both branches** | Rejected — leaves the suite `EXIT=0` but closes nothing reachable, since terminal is already unreachable. ⚠ Suite-green is evidence about the **suite**, not the engine. |
| **B. Drain ESP arms on ALL terminal transitions** | **Already true** (`endInstance` → `cancelAllScheduledWork`, 2 → 0). Does not address the reachable `StatusCompensating` defect. |
| **D. Drain ESP arms at `beginCompensation`, but ONLY for terminating walks** | Considered; **not chosen**. Executed, it breaks exactly one existing assertion, about *when* a `CancelTimer` is emitted, and it keeps `walkTerminates` out of the fire path. But it cannot see a walk that becomes terminating **after** it started — the deferred-cancel window, which is the case the refuted predicate above is about. Recorded with its data rather than excluded by an over-general sentence. |
| **C. `spawnsNewWork()` above, at the fire site AND the dispatch guard** | **Chosen.** |

`beginCompensation`'s non-drain stays: keeping ESP arms alive across a *resuming*
walk is what ADR-0124 and the throw-walk fixture depend on.

⚠ **Applies to every trigger kind, and the guard goes into `dispatchArmCascade`
itself.** The cascade runs `onMatch` — merging the payload and setting `matched` —
**before** the fire, so a fire that silently no-ops would *consume* the message and
short-circuit the fall-through: direction (b)'s silent-swallow, reintroduced on the
message path. Checking eligibility **ahead of** the ESP dispatch point avoids it.

⚠ **The empty-cursor assumption is RESOLVED, and its previous rationale was
FALSE.** This spec claimed `ActiveCmdID` is "transiently empty between records
inside a live walk". Measured: a three-record admin walk stamps `c4 → c5 → c6` with
no gap, and a corpus-wide invariant (`Compensating && ActiveCmdID == ""` → panic)
leaves four container-free packages at `EXIT=0` — **no `Step` in the reachable
corpus produces that state**. The conjunct `&& ActiveCmdID != ""` is still not
adopted, but now for a measured reason: the state survives only as a legacy row or
a hand-built `InstanceState`, where classifying it as dying is the conservative
direction for *not spawning work*.

---

## 4. What this delivery does NOT do

- **No change to timer or message ARM SELECTION** — both stay first-match, and
  `dispatchArmCascade`'s selection logic is untouched (§3.1, evidence D5).
  ⚠ **This is NOT "no change to timer or message dispatch".** ADR-0172's
  `spawnsNewWork()` goes into `dispatchArmCascade`, so timer- and message-triggered
  **event sub-process** arms are refused on a dying instance exactly as
  signal-triggered ones are. An earlier draft of this list said "no change to timer
  or message dispatch semantics" and contradicted its own §3.2.
- **No opt-out for the old first-match behaviour** — it was a defect, not a
  configuration.
- **No schema or wire change, and no migration step.** Persisted `InstanceState`
  keeps its shape; the arm slices are read back in declaration order (measured).
  ⚠ But **in-flight instances do change behaviour on the next delivery**: an
  instance persisted under first-match that is carrying two same-named arms will,
  after deploy, fire both on its next signal. That is the fix working as intended,
  and it is the migration-relevant fact. ⚠ *"v0.1.0 is not tagged, so there is no
  compatibility obligation"* — true of the **tag** (verified: no tag exists), but a
  non-sequitur for persisted state, and it is **not** the argument this bundle
  relies on. ADR-0154 did the same analysis for a strictly smaller change and
  called for release notes; this delivery should too.
- **No change to the cross-family precedence** (gateway → boundary → event-sub →
  token). It is un-ADR'd but load-bearing in all three handlers.
- **No change to `mergeVars` merge-once semantics** (§3.1, evidence D7).
- **No Micro-mode fix.** Executed (evidence D9), Micro diverges more than
  expected: `snapshotIDs` is taken over tokens Micro has not driven to their park
  yet, so an intermediate signal catch sitting `TokenActive` with
  `AwaitSignal=""` is **silently missed by tier 4** while the signal is still
  consumed and the catch is not re-armed. **This is a pre-existing defect
  independent of fan-out** — backlogged, not fixed here. Two further Micro facts
  the test plan must respect: an arm created by the delivery's own drive is
  invisible to every tier, and a tier's fire can leave its placed token
  `TokenActive` with **no command emitted**, so `len(Commands)` is not a proxy for
  "arms fired" in Micro. **Fan-out acceptance tests run in Macro.**
- **No per-arm error isolation** (§2.3).
- **No fix for the missing-node token wedge** (evidence D8-0): a flow targeting a
  non-existent node parks a `TokenWaiting` token with `AwaitCommand=""` that
  nothing can ever resume, leaving the instance `running` forever. Backlogged.
- **No fix for `processtest.Classify`'s `Park.AwaitingSignals` collapsing
  multiplicity** (evidence D2-x). Backlogged; tests use `st.SignalWaiters()`.

---

## 5. Consequences

**Breaking**, in two directions — and the second is the one a recap sentence
would get wrong:

1. Several same-named arms now fire per delivery where one did before.
2. **Some deliveries now fire FEWER arms than they do today**, because an arm
   created during the delivery is no longer in the snapshot (§1.2). *The fan-out
   is NOT a pure superset of today's behaviour.*

A delivery firing several interrupting arms produces a larger single
`StepResult`, bounded per step by the definition's arm count — but **not bounded
across steps**: non-interrupting arms stay armed (evidence M9) and
`performThrowSignal` publishes to the signal bus with no self-exclusion, so a
signal-throwing loop can amplify.

⚠ **Three hedges on that sentence, all required.** (a) The parked draft stated
`2^n` as fact; `runtime/` is Docker-gated and the figure was never executed, so it
is **not** restated as fact. (b) *"`performThrowSignal` publishes with no
self-exclusion"* is itself a current-behaviour claim — source-verified and
confirmed by delivery observation (`delivered to: [other thrower]`); ⚠ the comment
at `runtime/processdriver_action.go:485` asserts the **opposite** and is the false
one (own follow-up). (c) **A bound the prior evidence file recorded and this one
must not lose:** `performThrowSignal` calls `sigbus.Publish` directly, whereas the
external `ProcessDriver.BroadcastSignal` *also* walks `signalStartDefs` and creates
signal-start instances. So an in-definition `ThrowSignal` creates **no new
instances** — amplification is bounded to existing waiters.

**Operational shape.** Measured: 100 matching arms produce **200 commands and 100
`drive` calls in ~2.6 ms**, in a **single** `StepResult` and therefore a **single
outbox transaction**. Consumers sizing outbox batches or transaction limits should
know one signal can now produce a delivery this wide.

**Cross-family annihilation is multiplied.** Measured: tier 2 mints a human task
and tier 3 cancels it inside one delivery, leaving a `boundary_interrupted` visit
for work that never ran. No per-family ordering rule could reach this — it is the
cross-family precedence, which §4 deliberately does not change, putting the widest
blast radius last. Today one task per delivery; after fan-out, N.

ADR-0154's explicitly-left-open consequence is closed. ADR-0124 Decision item 3
("by-name lookup returns the first match") is **amended**: per-delivery-once
remains true **per arm** — the snapshot fires each identity at most once — but is
no longer true **per family**.

---

## 6. Claims that execution REFUTED

Recorded rather than quietly dropped, per CLAUDE.md's rule on inherited claims.
**§6a is this bundle's own**, refuted by its rule-#9 audit after the first draft
was written; §6b are inherited from the parked draft.

### 6a. THIS BUNDLE'S OWN claims, refuted by its audit

The audit ran three Opus lenses, all briefed to execute rather than read. It
returned **3 Critical and ~10 High**, and the Criticals killed decisions, not
wording.

1. **"For two ESP arms sharing an enclosing scope it is impossible for both to
   take effect in any order"** (§2.2, ADR-0158 Decision 2). **False.** The
   supporting fixture's non-interrupting body **parked**; with a body that
   completes synchronously both arms take effect. The per-family ordering rule
   built on it is withdrawn — see §2.2 option D.
2. **"Non-interrupting-first lets both arms take effect (2 tokens)"** (tier 2, same
   decision). **False** when the non-interrupting branch force-terminates:
   `tokens=0`, and the two orders yield **different terminal statuses**.
3. **`walkMode() == walkAdmin || PendingCancel` as the dying-instance predicate**
   (§3.2). **Refuted:** `consumePendingCancel` is not set on `walkPartial`, so such
   a walk resumes with `PendingCancel` true and the predicate silences an arm on a
   live instance — reintroducing the very defect it fixes. Replaced by
   `walkTerminates`.
4. **"`ActiveCmdID` is transiently empty between records inside a live walk"**
   (§3.2, the rationale for rejecting the conservative variant). **False, and it
   was never executed** — an unexecuted behavioural claim inside the document
   asserting Premise Discipline. A corpus-wide invariant shows the state is not
   engine-reachable at all.
5. **"Two boundary nodes with the same id produce arms identical in EVERY field"**
   (ADR-0158 Decision 4). **False** — they differed in `NonInterrupting` and
   `Action`, so the de-dup chooses between materially different arms and its
   tie-break had to be specified.
6. **"De-dup discards the loser's distinct `Flow`, which `resolveGatewayWin`'s
   fallback reads"** (same decision). **Fabricated** — `armedEvent.Flow` is never
   read; mutating it leaves the suite green.
7. **"No change to timer or message dispatch semantics"** (§4). Contradicted this
   spec's own §3.2 — corrected to separate *arm selection* from *ESP fire
   eligibility*.
8. **The Premise Discipline banner's own quantifiers** ("Every… Two…") were
   unverified and disagreed with §6 and with the commit message.

⚠ Two of these — 4 and 8 — are **unexecuted claims inside a document whose banner
asserts that no such claim may enter**. Recorded plainly because that is the
failure mode this repo keeps repeating, and hiding it here would be the same
mistake one level up.

### 6b. Claims inherited from the parked draft

1. **`s.Status != StatusRunning` as the per-iteration predicate** (parked draft
   "Status and errors"; prior evidence `§Q4(c)`). Refuted by ADR-0168/0169 and not
   re-derived here: it conflates the two meanings of `StatusCompensating`, and was
   measured swallowing a legitimate signal and stranding an instance forever. This
   delivery **inherits `IsTerminal()` from ADR-0169** instead.
2. **"a test pins the invariant that no `drive`-reachable code writes
   `s.Variables`"** (parked draft §"Status and errors"). The prior evidence file's
   claim C21 found the invariant true but **the pinning test absent**. This
   delivery does not restate the safety net; if it wants one it must add it.
3. **"a boundary whose outgoing flow targets a missing node errors"** (parked
   plan, Phase 2 case 11). Refuted — it parks (§2.3, evidence D8-0).
4. **"the interrupting fire calls `removeEventTriggeredSubprocessArmsForScope`
   (`step_eventsubprocess.go:207`)"** (parked draft Consequences). ADR-0162 moved
   it: the call is now `cancelScopeSubtree` (`engine/step_cancel.go:81`, retiring
   arms at `:101` for the named scope and `:113` for **every descendant**), and
   `removeEventTriggeredSubprocessArmsForScope` is no longer called from
   `step_eventsubprocess.go` at all (evidence M7). The scope wording widens from
   "that scope" to "that scope and every descendant scope".
5. **"A non-root event-sub-process arm fires into a COMPLETED instance"** (the
   inherited framing this delivery's ESP half was opened against, and the basis on
   which the bundling decision was made). **Refuted end-to-end** (evidence E1b):
   true of the unexported function, unreachable through the public API because
   ADR-0164 drains the arms and ADR-0165 refuses the trigger. The real defect is
   `StatusCompensating` — §1.3. *The bundling decision survives the correction:
   the fan-out still multiplies ESP arm firing, and the compensating window is
   wider than the terminal one, not narrower.*
6. **Effect (c) — an ADR-0168 "accepted cost" retiring a scope's ESP arms
   `2 → 0`.** **Stale** (evidence E4): the root-site tail was already removed by
   ADR-0171 in the same delivery, and the removal is pinned by a fixture-audited,
   mutation-verified test. The `2 → 0` figure never came from that fixture and is
   `ASSUMPTION (unverified)`; the measured figure under mutation is `1 → 0`. **Not
   restated as open.**
7. **`engine/step_nodes.go:484-491`'s claim that the nested completion conjunct is
   "asserted by the named test".** Measured false (evidence E4): deleting the
   conjunct leaves the repo suite green. The test's own docstring already admits
   this; the source comment contradicts it. Corrected in this bundle.
8. **Stale citations throughout.** The parked bundle's line numbers were taken
   against `9656799`/`f7b4884` and re-derived once at `9e96112`; ADR-0167 through
   ADR-0171 have moved them again. This document cites **symbols first**, line
   numbers second.

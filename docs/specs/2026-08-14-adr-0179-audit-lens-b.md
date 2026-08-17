# Lens B — failure modes, edge cases, cross-document coherence — ADR-0179 (rewrite)

Worktree: `scratchpad/audit-c-b`, HEAD `caf0bdc8` (bundle) rebased onto `main` `954c2a05`
(ADR-0177/0178/0180 **and** ADR-0181/0182 shipped).

Step 0: **bundle present** — ADR, spec, plan, premise evidence, and all three inherited lens
records confirmed on disk.

Findings are appended as they are established.

---
## B1 — MAJOR — every line-number citation the plan navigates by has rotted, and one now points into a DIFFERENT FUNCTION

**Documents**: spec §1, ADR Context, plan P1 step 2 ("at or above `:292`"), spec §3.5, ADR
Consequences.

Executed on the rebased worktree (`main` `954c2a05` + bundle):

| cited | claimed | actual today | note |
|---|---|---|---|
| `engine/step_triggers.go:292-294` | the compensation `ActionFailed` short-circuit | **`:338-340`** | line 292 is now inside **`handleCancelRequested`** (func boundaries: `handleCancelRequested` at `:150`, `handleActionFailed` at `:334`) |
| `engine/step_triggers.go:316` | `effectiveRetryPolicy` | **`:362`** | |
| `engine/state.go:475` | `endInstance`'s `retireCompensationStallIncidents` | **`:516`** | §3.5's own "five sites" citation |
| `engine/step_compensation.go:{524,1287,1294,1345}` | the four in-file retirement sites | **unchanged** | still correct |

The plan's P1 step 2 is the operative instruction — *"raise the incident and log at the
compensation `ActionFailed` short-circuit (`step_triggers.go`, **at or above** `:292`)"*. An
implementer who follows it literally edits `handleCancelRequested`'s compensation-branch tail,
which is the wrong function and would put the WARN + incident on the **cancel** path rather than
the failure path. §3.5's `state.go:475` is likewise now 41 lines off.

This is the repo's recorded "an audited bundle decays when its base moves" failure, and the bundle
was authored against `12c9d7e3` with **two** merges landing since.

**Fix**: replace every line-number citation in ADR/spec/plan with a **symbol** name —
`handleActionFailed`'s `s.Status == StatusCompensating && s.Compensating.ActiveCmdID == t.CommandID`
short-circuit; `endInstance`'s remainder sweep — and state the rule in the plan's ▶ Progress that
line numbers are not navigation aids in this bundle.

---

## B2 — ⚠⚠ CRITICAL — the ADR's "default-off means no behaviour change" claim is FALSE: the always-on incident RE-CLASSIFIES the instance for `processtest`, and `AutoTimers` then refuses to drive it

**Documents attacked**: ADR Consequences, last bullet — *"Default-off retry means existing consumers
see no timing change — **only** new WARN lines and a new incident kind in the instance document."*
Also spec §2.1 ("Default, on for everyone").

An incident is **not** an inert document field. It is a first-class park signal in the shipped
consumer test harness. `processtest.Classify` (`processtest/park.go:184`) has an incident rung at
priority 3, above signal, message and timer:

```go
case len(p.Incidents) > 0 || hasIncidentToken(state.Tokens):
        p.Reason = ReasonIncident
```

**Executed** (throwaway probe in `processtest`, deleted; `-run` name confirmed with `=== RUN`,
EXIT=0):

```
A no-incident            -> reason=timer     hasArmedTimers=true
B walk-scoped incident   -> reason=incident  hasArmedTimers=true node="step3"
C compensating, 0 tokens -> reason=incident  hasArmedTimers=false
D compensating, no inc   -> reason=unknown
```

A and B differ **only** by one `Incident{TokenID: ""}` — exactly ADR-0179's shape — appended to an
otherwise identical `StatusRunning` state parked on a timer catch. The park flips `timer →
incident`.

Why that is a breaking change with retry **default-off**:

- `AutoTimers()` (`processtest/handlers.go:22`) acts **only** on `ReasonTimer` and otherwise
  returns `Pass()`. `park.go`'s own KNOWN GAP comment records the consequence: *"A handler with no
  case for it Passes, and drive then reports `ErrUnhandledPark`."*
- The **resuming** walks are the ones that bite. A throw-compensation whose undo action fails now
  resumes the instance to `StatusRunning` carrying `IncidentCompensationFailed`, and — see **B3** —
  nothing ever removes it. Every subsequent park in that instance's life classifies
  `ReasonIncident`. A consumer's existing `Chain(AutoTimers(), CompleteTasksWith(...))` test that
  passes today starts failing with `ErrUnhandledPark` on a **default-configured** engine.
- Row C is the retry-backoff shape: `reason=incident`, `hasArmedTimers=false`. So even with retry
  ON, `AutoTimers` can never advance a compensation retry backoff — the feature is **undrivable
  from the shipped harness**, which is the ADR-0166 concern repeating.

The harness's own promotion escape hatch does not help: `harnessEnv.classify`
(`processtest/harness.go:302`) iterates `state.Tokens`, and spec §1 measures a compensation walk as
holding **zero tokens**.

**Fix** (pick one, but the ADR must stop claiming no behaviour change):
1. Rewrite the Consequences bullet with the measured reclassification, and add `ReasonCompensation`
   / a `Park` rung that ranks a walk-scoped incident **below** the timer rung, so `AutoTimers`
   keeps driving; **or**
2. make the incident opt-in on the same `CompensationRetryPolicy` switch as the retry (WARN stays
   always-on), so "default = today's behaviour" becomes true; **or**
3. give `processtest` a `AutoCompensationRetry()` handler and state plainly in the ADR that this
   is a **breaking change for `processtest` consumers**, with the migration.

Whichever is chosen, the test plan needs a `processtest`-level test — §4 has **twelve** tests and
not one of them leaves `engine`/`runtime`.

---

## B3 — MAJOR — `IncidentCompensationFailed` is IMMORTAL: never retired, not resolvable, and no document asks how an operator clears it

**Documents attacked**: ADR Decision 3 + Consequences ("the incident remains as the durable
record"), spec §3.5 (which only argues it must *survive*).

Both halves source-verified:

- **Not retired.** `retireCompensationStallIncidents` (`engine/state.go:462`) filters
  `inc.Kind == IncidentCompensationStall`. `removeOrphanedIncidents` (`:445`) deletes only
  `inc.TokenID != ""`. The new incident carries `TokenID: ""` and a different kind, so **neither
  sweep can ever touch it** — which spec §3.5 celebrates as "survival" without noticing it is also
  permanence.
- **Not resolvable.** `handleResolveIncident` (`engine/step_triggers.go:1306`) refuses on
  `inc.Kind != IncidentAction` — a **whitelist**, so the new kind is refused automatically with
  `ErrIncidentNotResolvable`. Good that it cannot be silently eaten; but it means the shipped
  `ResolveIncident` admin endpoint (`transport/http/httpcore/admin_endpoints.go:102`, all three
  transports) **cannot** clear it either.
- **No escape verb.** ADR-0175's `CompensationEscape` verbs are gated on
  `hasOpenStallIncident(t.IncidentID)` — a `Kind == IncidentCompensationStall` test
  (`engine/state.go:467` is the retirement filter; the escape guard is the sibling). The new kind
  gets no verb.

Contrast: `IncidentCompensationStall` — the incident this one is modelled on — is retired at
**five** sites precisely so it does not outlive the condition.

Consequences the bundle does not state:
- an instance that resumes after a failed compensation is permanently classified
  `ReasonIncident` (measured, B2 row B);
- `runtime/kernel/lister.go:153` documents an incident as meaning *"the instance is parked and
  requires operator intervention via `ResolveIncident`"* — which for this kind is now false and
  unactionable;
- exhaustion appends **one incident per failed record**, so a walk over N poison records leaves N
  permanent incidents, each of which is a candidate for `Incidents[0]` (see spec §3.4).

**Fix**: decide the incident's lifecycle explicitly in the ADR Decision, not only its birth. The
cheapest coherent option: retire it when the walk moves past the record (mirror
`stepCompensationAdvance`'s existing retirement, keyed on `CommandID`) and keep exactly the **final**
exhaustion incident — that is the durable record the ADR actually wants, and it keeps the count
bounded at one per exhausted record rather than one per attempt. Whatever is chosen, add a test and
a Consequences line saying which operator surface clears it.

---

## B4 — ⚠⚠ CRITICAL — during the backoff window the STILL-ARMED stall timer fires and manufactures a FALSE `IncidentCompensationStall`

**Documents attacked**: spec §3.9 (the window), §3.3 (which considers only the *retry* timer's
leak), ADR Decision 2. Neither document mentions the stall timer at all in the backoff context.

Source-verified mechanism:

1. Every compensation dispatch arms a `TimerCompensationStall` record stamped
   `CommandID: cur.ActiveCmdID` (`engine/step_compensation.go:armCompensationStallTimer`).
2. The **only** code that removes such a record is `cancelCompensationStallTimers`, whose two
   callers are `armCompensationStallTimer` (i.e. the *next* dispatch) and `stepCompensationFinish`
   (`:867`). ADR-0179's failure path does **neither** — it stays on the same record and waits.
3. `handleCompensationStallFired` (`engine/step_timers.go:133`) raises the incident iff
   `s.Status == StatusCompensating && rec.CommandID == s.Compensating.ActiveCmdID`. §3.1's
   duplicate predicate and §3.3's late-fire check both **require** `ActiveCmdID` to survive the
   failure, so both guards pass.

So whenever `CompensationStallAfter` is configured and the backoff outlives the remaining stall
window, an operator is told *"compensation action stalled"* about an action that **already
replied**, failed, and is in a healthy scheduled backoff. That is a visibility **regression** in the
ADR whose headline is "always visible", and the two windows are configured independently with
nothing coupling them.

It also opens the double-dispatch route the bundle spent §3.9 avoiding from the other direction:
the false stall incident is exactly what enables `CompensationEscape{Retry}` →
`retryStalledCompensation` → a fresh `ActiveCmdID` and an immediate re-dispatch, **racing** the
scheduled `TimerCompensationRetry`. (§3.3's prescribed late-fire check on the retry handler would
drop the losing one — *if* the retry record carries the cmd id and the handler checks it. That
mitigation is stated for the *walk-finish* leak, never for this race, and the plan's step 10 does
not test it.)

**Fix**: on the failure path, **cancel the stall timer for the failed command before arming the
retry timer** (the stall window's job is done — the action reported). State the arm/cancel ordering
explicitly, because §3.3's prescribed widening of `cancelCompensationStallTimers` to *every*
`walkScoped()` kind makes a cancel-then-arm sequence self-cancelling if written in the wrong order.
Add `TestNoStallIncidentDuringACompensationRetryBackoff` — it fails today against the naive design
because the stall record survives with a matching `CommandID`.

---

## B5 — ⚠⚠ CRITICAL — the bundle DEFERS its own load-bearing decision to implementation: §3.9's state machine is named as required and never specified

**Documents attacked**: spec §3.9, ADR (silent), plan step 11.

Spec §3.9 sets out both horns — keep `ActiveCmdID` (→ redelivered `ActionFailed` is not a
duplicate, two timers dispatch one record) vs clear it (→ §3.3's late-fire check cannot be written)
— then says **"Required: specify the window's state machine explicitly."** It never does. The ADR's
Decision section says nothing about `ActiveCmdID` at all. The plan (step 11) hands it forward:
*"The window's state machine must be explicit: whether `ActiveCmdID` survives a failure decides both
this and step 10."*

That is an unresolved fork sitting under **at least six** other decisions in this bundle: §3.1's
duplicate predicate, §3.3's late-fire check, §3.3's empty-cursor bail, B4's stall-timer race, the
`CompensationEscape` guard (`t.CommandID != cur.ActiveCmdID` refuses every operator verb while
`ActiveCmdID` is cleared), and `handleCompensationStallFired`'s own guard. Rule #9's premise is that
an audited bundle is an implementation input; a bundle that names its central state machine as
"required" and leaves it blank is not.

**Fix**: write the window into the ADR Decision as a state table, e.g. — `ActiveCmdID` **survives**
the failure and names the failed command until the retry re-dispatches; the cursor gains
`RetryAttempts` and `RetryTimerID`; a redelivered `ActionFailed` for `ActiveCmdID` is idempotent
**because `RetryTimerID != ""` means a retry is already armed** (that, not the duplicate-set
predicate, is what closes §3.9 — and it is checkable in one field). Then §3.1's predicate, §3.3's
late-fire check and B4's cancel all follow from one written rule.

---

## B6 — MAJOR — a lost/pruned/unrehydrated retry timer STRANDS the instance, breaking exactly the ADR-0034 safety property the ADR claims to preserve

**Documents attacked**: ADR Decision preamble ("preserving its safety property — a failed
compensation still never strands the instance"), Decision 3, spec §2.3.

Between the failure and the timer fire the walk makes **no** forward progress and holds **no**
token. Exhaustion — the ADR's only "skips and continues" escape — is reached *by the timer firing*.
So if the timer never fires, the instance never terminates. Three executed/source-verified routes
by which it never fires:

1. **Retention pruning deletes it.** The retry timer is armed with `schedule.AfterDuration(delay)`
   (mirroring `armCompensationStallTimer`), and `AfterDuration` is `KindOneTime`
   (`definition/schedule/trigger.go:47`). `nonRecurringTriggerKinds` is
   `{KindUnset, KindOneTime, KindExpr}` (`internal/persistence/store/pruner.go`), so
   `Pruner.PruneTimers(cutoff)` — a consumer's ordinary retention job — **deletes** an expired
   compensation-retry row. For the stall timer this loses a diagnostic; for the retry timer it
   loses the walk.
2. **Rehydration skips it.** `jobStore.Load` (`runtime/jobstore.go`) `continue`s past any row whose
   process definition is not in the registry ("definition not found, skipping timer") and any row
   whose trigger will not convert. Both are logged at WARN and the batch proceeds. A compensation
   walk on a retired definition version is exactly the population most likely to hit rung 1.
3. **ADR-0181's reclamation is disjoint** — its predicate is `next_run < epoch AND trigger_kind IN
   (recurring)`, so it neither helps nor hurts here; noted only because the bundle never mentions
   ADR-0181 at all (see B7).

**Fix**: the ADR must either (a) bound the wait independently of the timer — e.g. the walk-level
`CompensationStallAfter` guard must remain armed *through* the backoff so a lost retry timer still
surfaces (which conflicts with B4's fix, so the two must be designed together: keep a stall guard
armed for `backoff + stallAfter`); or (b) state plainly, under Consequences, that a lost retry timer
strands the instance and that the operator's recovery is ADR-0175's `skip`/`abandon` — which
requires a stall incident to exist, i.e. it requires (a). Add
`TestCompensationRetryTimerLostDoesNotStrandTheWalk`.

---

## B7 — MAJOR — the bundle was authored two merges ago and never re-derived against ADR-0181/0182

**Documents attacked**: spec header ("**Base**: `main` at the ADR-0176 merge `52bf0f80`"), plan
▶ Progress ("newest code on `main` is the ADR-0176 merge `52bf0f80`"), spec §5 ("Bundle **A** …
must merge first").

Executed: `main` is `954c2a05`. **ADR-0177/0178/0180 shipped** (merge `a5b33e4c`) and
**ADR-0181/0182 shipped** (merge `1ac140f6`). Every sequencing statement in this bundle is stale:

- spec §5 and the plan's ▶ Progress both say bundle A "must merge first" and that this bundle
  "depends on" it. It has merged. `walkScoped()` **exists** (`engine/state_timer_waiters.go:38`,
  `return k == TimerCompensationStall`) and its doc comment already names ADR-0179's kind as the
  next one. The gate the plan is waiting on is discharged; nobody reading the plan would know.
- Neither ADR-0181 nor ADR-0182 appears anywhere in the bundle. They ship a **timer-row reclamation
  sweep** and a **never-due validation gate** — both squarely in the surface a new `TimerKind` +
  new `ScheduleTimer` touches (see B6 rung 1, and the arm-must-agree-with-the-scheduler rule of
  ADR-0176 that a zero/negative `Backoff(attempt)` would trip).
- **`walkScoped()` has TWO production consumers, not one.** The bundle treats extending it as a
  single-purpose fix for ADR-0178's guard (spec §3.11, ADR Consequences, plan trap 6). The other
  consumer is `InstanceState.HasArmedTimers` (`engine/state_timers.go:152`), which feeds
  `processtest.Classify` — so the mandated edit ALSO makes a retry-backoff instance report
  `HasArmedTimers == false` (measured, B2 row C). That second consequence is nowhere in the bundle.

**Fix**: re-base the spec header/plan ▶ Progress onto `954c2a05`, delete the discharged dependency
on bundle A, add an "interaction with ADR-0181/0182" subsection, and correct every "extend
`walkScoped()`" sentence to name **both** consumers and the `HasArmedTimers` consequence.

---

## B8 — MAJOR — spec §3.11's last bullet is wrong twice: it is a 422, not a 409, and on the common walk shape there is no error at all

**Document attacked**: spec §3.11, final bullet — *"An instance cancelled while a retry backoff is
pending receives **ADR-0180's new 409** for a walk that is waiting, not stalled, with no way to
distinguish. Decide or document."*

Source-verified, both halves false:

- **Status code.** `ErrCancelNotApplicable` wraps `ErrInvalidTransition` (`engine/errors.go:122`),
  and `httpcore.ClassifyError` maps `engine.ErrInvalidTransition` to
  **`http.StatusUnprocessableEntity` (422)** (`transport/http/httpcore/errors.go:48`). 409 is
  reserved for `kernel.ErrConcurrentUpdate` (`:35`). The engine's own doc comment says so in as
  many words: *"the HTTP transports answer 422 rather than a 500"*.
- **Reachability.** The sentinel is returned **only** when the in-flight walk RESUMES —
  `walkTerminates` is the shared predicate, and `engine/step_triggers.go:237-249` records that
  implementation refuted the blanket form: a `walkAdmin` (cancel/error rollback) walk is
  idempotently satisfied and returns **nil**. A compensation retry backoff on a cancel-started walk
  — the ADR's own headline shape, and the fixture plan step 14 mandates — therefore gets **no
  error and no 409/422 at all**.

The bullet is also the only place in the bundle that flags this and it ends with "Decide or
document", i.e. undecided (same defect class as B5).

**Fix**: replace the bullet with the measured pair — *walkPartial → `ErrCancelNotApplicable` → 422;
walkAdmin → nil (idempotent)* — and then state the actual open question, which is whether a cancel
arriving mid-backoff should short-circuit the backoff and finish the walk rather than wait out a
timer on an instance the operator has just asked to die.

---

## B9 — MINOR (positive, with a gap) — plan step 14's "cancel-started" fixture IS constructible; the plan just doesn't say where it is

The brief asked whether step 14's fixture exists. It does. `startedStallWalk`
(`engine/step_compensation_stall_incident_test.go`) already builds a cancel-started walk, and
`TestStallIncidentIsRaisedOnADyingWalk` (`:90`) already opens with exactly the assertion the plan
prescribes:

```go
require.False(t, engine.SpawnsNewWork(&state),
    "fixture precondition: a cancel-started walk is DYING (spawnsNewWork == false)")
```

`engine.SpawnsNewWork` is exported for `engine_test` at `engine/export_test.go:141`. So the plan's
trap 7 is real and its remedy is available.

**Fix**: name the helper (`startedStallWalk`) and the precedent test in plan step 14, so the
implementer reuses it instead of building a fifth walk fixture — and note that the file is
`package engine_test` (`head -1` rule).

---

## B10 — MINOR — §3.6's "four dispatch sites → five" is correct today, but the *retirement* count in §3.5 has already rotted

Re-derived by `grep` on the rebased tree:

- `compensationInvoke` production call sites: **four** — `engine/step_compensation.go:{412,574,1301}`
  and `engine/step_nodes.go:1139`. §3.6's "four today, five after the retry" holds.
- `retireCompensationStallIncidents` call sites: **five** —
  `engine/step_compensation.go:{524,1287,1294,1345}` + `engine/state.go:516`. The count is right;
  the last citation's **line number** is wrong (§3.5 says `:475`, see B1).
- `cancelCompensationStallTimers` call sites: **two** — `engine/step_compensation.go:472`
  (inside `armCompensationStallTimer`) and `:867` (inside `stepCompensationFinish`). The bundle
  discusses only the finish one (§3.3), yet §3.3's prescribed widening changes **both**, and the
  `armCompensationStallTimer` one runs on **every advance**. Unstated, and it is the ordering
  hazard in B4's fix.

**Fix**: add the `cancelCompensationStallTimers` call-site count to §3.3 with the consequence for
each caller, and correct §3.5's line number to a symbol reference.

---

## B11 — MINOR — the §4 test plan has no test for the ADR's own stated Negative consequences

Cross-checking ADR Consequences against spec §4 and plan P1–P3, three promised behaviours have no
prescribed test:

| ADR Consequence | plan step | test in §4 |
|---|---|---|
| "the retry budget is per record, zeroed when `NextIndex` advances" | 9 | 8 ✔ |
| "`DispatchedCmdIDs` … cap it, or say so" (§3.8) | 15 — **"Decide and record"** | **none** |
| "Downgrade hazard … a poison compensation retries forever" (§3.11) | **none** | **none** |
| "the incident remains as the durable record" | — | **none** (B3) |

Plan step 15 is *"Decide and record"* — a third undecided item shipped inside an
implementation-ready plan (with B5 and B8). And the downgrade hazard is the one the brief flags:
"documented" is not adequate when the documented outcome is **unbounded retrying of a poison
action**. A budget that lives only in a droppable JSON field has no floor.

**Fix**: bound the retry independently of `RetryAttempts` — e.g. also stamp `RetryStartedAt` on the
cursor (already in the design) and make `MaxElapsed` the *primary* exhaustion test, mirroring
`effectiveRetryPolicy`'s existing `MaxElapsed` branch (`engine/step_triggers.go:367`). An elapsed
bound survives an attempts-counter drop, because `StartedAt` is on the cursor a downgrade also
drops — so pin it to the **record**, or accept and state that a mixed-version deployment is
unsupported for this feature (ADR-0175's `Incident.Kind` comment already says exactly that for its
own kind: *"Do not run pre-0175 and post-0175 builds against the same instance store."* — reuse that
wording rather than inventing a weaker one).

---

## B12 — ⚠⚠ CRITICAL — `walkScoped()` is the WRONG predicate to extend: it conflates two orthogonal properties, and the bundle's mandated edit gets one of them backwards

**Documents attacked**: spec §3.11 bullet 1, ADR Consequences ("`walkScoped()` must gain
`TimerCompensationRetry` … or ADR-0178's guard refuses every compensation retry and this ADR
**silently never works**"), plan step 8 and trap 6.

`walkScoped()` today serves **two** consumers with two different questions:

| consumer | question it is asking | right answer for `TimerCompensationStall` | right answer for `TimerCompensationRetry` |
|---|---|---|---|
| `handleTimerFired` path 4 (`engine/step_triggers.go:596`, ADR-0178) | may this timer fire on a **dying** instance? | yes (exempt) | **yes** (exempt) |
| `InstanceState.HasArmedTimers` (`engine/state_timers.go:152`, ADR-0177) | is this timer **work the instance is waiting to do**? | **no** | **yes** |

The bundle extends the single predicate, so it gets row 1 right and row 2 **backwards** — measured
in B2 row C: a walk sitting in a retry backoff reports `hasArmedTimers=false` even though that timer
is the only thing that will ever move it. That propagates straight into `processtest.Classify`'s
timer rung and `AutoTimers`.

`walkScoped()`'s own doc comment states the property it means, and the retry timer **violates it**:

> *"reports whether a timer of this kind belongs to a compensation WALK rather than to the
> instance's **forward work**"* — `engine/state_timer_waiters.go:29-33`

Second, shipped-comment falsification: `handleCompensationStallFired`'s doc records
*"It raises ONE incident and does nothing else. **Emitting no commands is a hard constraint, not a
stylistic choice**: this runs on `handleTimerFired`'s path 4, whose `!spawnsNewWork()` refusal
EXEMPTS walk-scoped kinds … A handler that emitted work here would dispatch it to an instance an
in-flight rollback has already decided to kill (the measured ADR-0172 reminder hole)."*
(`engine/step_timers.go:116-124`). ADR-0179's `retryFailedCompensation` fires on that same exempt
path and **must** emit an `InvokeAction` (and, on exhaustion, whatever
`stepCompensationAdvance` emits next). The bundle nowhere acknowledges that it is deliberately
breaking a comment that calls itself a hard constraint. Per rule #11 / Delivery-Gate item 2 that
comment must be amended **in-bundle**, with the reason a compensation dispatch on a dying instance
is legitimate where a reminder dispatch is not.

**Fix**: split the predicate before extending it.
- Keep a `firesOnDyingInstance()` (or rename `walkScoped()` to it) covering
  `{TimerCompensationStall, TimerCompensationRetry}` — this is what `step_triggers.go:596` reads.
- Give `HasArmedTimers` a narrower `detectionOnly()` covering `{TimerCompensationStall}` alone.

Then `HasArmedTimers` reports **true** during a retry backoff, `Classify` can reach the timer rung
(once B2/B3 stop the incident from outranking it), and `AutoTimers` can drive the feature — without
which the delivery ships a mechanism no consumer test harness can exercise. Add the falsifiability
note the plan owes: `TestHasArmedTimersDuringACompensationRetryBackoff` fails today under the naive
single-predicate extension, measured `false`.

---

## Summary for the controller

| # | sev | one line |
|---|---|---|
| B1 | MAJOR | every cited line number has rotted; `step_triggers.go:292` now names a **different function**, and the plan's P1 step 2 navigates by it |
| B2 | **CRITICAL** | the ADR's "default-off = no behaviour change" is measurably false: the always-on incident flips `processtest` parks `timer → incident` and `AutoTimers` then refuses to drive |
| B3 | MAJOR | `IncidentCompensationFailed` is immortal — never retired, refused by `ResolveIncident`, no escape verb; the bundle only ever argues it must *survive* |
| B4 | **CRITICAL** | the still-armed stall timer fires during the backoff and manufactures a FALSE "compensation action stalled", which then invites an operator retry racing the scheduled one |
| B5 | **CRITICAL** | §3.9's state machine is declared "required" and never specified; ≥ 6 other decisions depend on it, and the plan hands the fork to implementation |
| B6 | MAJOR | a pruned / unrehydrated retry timer strands the instance — the exact ADR-0034 property the ADR claims to preserve; `AfterDuration` is `KindOneTime`, which `PruneTimers` deletes |
| B7 | MAJOR | base stale by two merges: bundle A's gate is discharged, ADR-0181/0182 unmentioned, and `walkScoped()`'s **second** consumer unnoticed |
| B8 | MAJOR | §3.11's "409" is a **422**, and on a cancel-started walk there is no error at all |
| B9 | MINOR | plan step 14's fixture IS constructible (`startedStallWalk`) — the plan should name it |
| B10 | MINOR | `cancelCompensationStallTimers` has **two** callers; §3.3's widening changes both, and §3.5's `state.go:475` line is wrong |
| B11 | MINOR | three ADR consequences have no prescribed test, and two plan steps (11, 15) are "decide during implementation" |
| B12 | **CRITICAL** | `walkScoped()` conflates "fires on a dying instance" with "is forward work"; the mandated extension gets the second backwards, and silently falsifies a shipped comment that calls itself a hard constraint |

Four CRITICALs (B2, B4, B5, B12) each independently block implementation. B2/B12 are the same
root: the bundle reasons about `engine` only and never asks what its two always-on changes (a new
incident, a new walk-scoped kind) do to the **consumer-facing** surfaces that read them.

# A failed compensation action is retried, and always visible (ADR-0179)

**Status**: design — **audited TWICE and re-folded 2026-08-17.** IS an implementation input.
**Date**: 2026-08-13, re-folded 2026-08-17
**Bundle**: C — one delivery, one ADR, one commit
**Base**: `main` at the ADR-0181/0182 merge `1ac140f6`

Closes `HANDOVER.md` ▶ NEXT WORK item **3** — backlog **16** and **3g**.

## §0 — Reading guide

⚠ **This design has failed TWO audits.** It was split out of bundle A after that bundle's rule-#9
audit landed **4 of its 6 Criticals on this decision alone** (records:
`2026-08-13-adr-0179-inherited-audit-lens-{a,b,c}.md`). The rewrite then **also failed** its own
audit — 3 lenses, ~30 findings, **9 Criticals** (records:
`2026-08-14-adr-0179-audit-lens-{a,b,c}.md`).

**Read `docs/specs/2026-08-14-adr-0179-audit-adjudication.md` FIRST.** It carries every
disposition, the four owner decisions taken at the fold, and — in §2 — **four load-bearing
decisions this document previously named as "required" and left blank**. Where this spec and the
ADR disagree, the ADR wins: it is the post-adjudication text.

⚠ **Navigate by SYMBOL, never by line number.** Every `file:line` here was authored against
`12c9d7e3` and had drifted ~+46 lines by the time of the second audit; the plan's operative
instruction pointed into the wrong function entirely.

⚠ Sections below marked **CORRECTED (second audit)** contained false claims. Two were false
*universals* whose hedges were stripped on restatement, and one — the `TestStepDoesNotMutateInput`
"existing gate" — was **the first audit's own prescribed fix**, restated here inside a document
whose §4 opens with *"Check the fixture, not the assertion line."*

---

## §1 — The measured problem

A compensation action returning `ActionFailed` is skipped **silently**. Executed across four walk
shapes — cancel-started, scope-wide throw, targeted throw, and with an explicit retry policy — the
FAIL and CONTROL transcripts are **byte-identical in every printed field**: same statuses, same
command ids, same `RootCompensations`, same `incidents=NONE`, same `endedAt`. With
`DefaultRetryPolicy{MaxAttempts: 5}` in effect:

```
  re-dispatched c3 again? false
  scheduled a TimerRetry? false
```

**The mechanism is the short-circuit at `engine/step_triggers.go:292-294`**, which routes to
`stepCompensationAdvance` before `effectiveRetryPolicy` (`:316`) and `propagateError` are reached.
A retry policy is structurally unreachable. **Any retry feature is an edit at or above line 292.**

⚠ **ADR-0034's own Consequences text is false**: it says the failure is "logged/skipped";
`grep -c slog` over `stepCompensationAdvance` and `handleActionFailed` both return **0**. Today a
failed compensation action is invisible in all three channels — **no retry, no incident, no log**.

Two inherited claims died and must not be built on: *"ownership transfers on dispatch, so the
record is consumed"* is false as a general claim (`consumeDispatchedRecord` is a no-op unless the
cursor carries an `ArchiveKey` or a live teardown window — record loss is **targeted-throw-only**);
and the cited `re-dispatched=[]` vs `[undoB undoA]` measurement belongs to ADR-0175's **abandon**
verb, not to `ActionFailed`.

**The structural constraint**: a compensation walk holds **no token of its own**, so
`RetryAttempts`, `RetryStartedAt`, `TokenIncident`, catch flows and error boundaries are all
unavailable. Attempt state must therefore live off the token.

⚠ **CORRECTED (second audit, C-1).** This previously read *"a compensation walk holds **zero
tokens** (measured at every frame)"*. That universal is **FALSE**: a scope-wide throw with a
parallel sibling holds **one token at every frame** while the walk is in flight, and
`startCompensationWalk`'s own shipped comment says so ("sibling branches keep running while the walk
is in flight"). The premise-evidence record hedged it correctly — "Scenarios A, B and D" — and **the
hedge was stripped on restatement**, which is this repo's most-repeated defect. The conclusion
survives because what the design needs is that the *walk* is not token-driven, not that the
*instance* has no tokens.

⚠ Also corrected: attempt state lives on `compensationCursor`, but the **dispatched-id set does
not** — it moves to `InstanceState` (ADR Decision 5), because the cursor is zeroed at walk finish.

---

## §2 — Decision

**Extend ADR-0034 Decision 4, preserving its safety property.** A failed compensation still never
strands the instance; it becomes visible, and retried when the consumer opts in.

1. **Always visible.** A `slog.WarnContext` line and an `Incident{Kind: IncidentCompensationFailed}`
   in ADR-0175's walk-scoped shape (`TokenID: ""`, keyed by `CommandID`). Default, on for everyone.
2. **Retried when configured.** `StepOptions.CompensationRetryPolicy *model.RetryPolicy`, default
   `nil` = one attempt = today's timing. A failed record is re-dispatched under a **fresh** command
   id after a backoff timer of new kind `TimerCompensationRetry`.
3. **The walk never parks on it.** On exhaustion the walk skips and continues; the incident remains
   as the durable record.
4. **A late reply to a superseded command is a benign duplicate** (backlog 3g) — today both a late
   `ActionCompleted` and a late `ActionFailed` return `ErrTokenNotFound`, which wraps
   `ErrInvalidTransition` → **HTTP 422**, so an at-least-once worker's duplicate surfaces as a
   client error on a healthy walk.

**One policy tier**, `StepOptions.CompensationRetryPolicy`. A per-node tier (backlog 4) needs a
`definition/activity` option and would widen this delivery beyond `engine` + `runtime`.

Rejected: **parking the walk on exhaustion** (reverses ADR-0034's safety argument, and
`abandonCompensationWalk` is refused on a resuming walk, so a parked throw walk would have only
`retry` and `skip`). Rejected: **routing through `propagateError`** (routes a token down a flow; a
walk has none, and ADR-0170's in-flight guard forbids re-entry).

---

## §3 — What the inherited audit changed, and why each matters

Every item below is a defect in the pre-split design, found by execution. They are the reason this
is a separate bundle.

### 3.1 ⚠⚠ CRITICAL — `DispatchedCmdIDs` would swallow every legitimate reply

The four dispatch sites are exactly where `ActiveCmdID` is set, so the in-flight id joins the set
**the moment it is dispatched**. The naive rule — "a reply whose `CommandID` is in the set is a
duplicate" — makes every normal `ActionCompleted` a duplicate, so **the walk never advances**:
a permanent stall, strictly worse than the 422 it replaces.

**Required**: the duplicate predicate is
`cmdID != cur.ActiveCmdID && slices.Contains(cur.DispatchedCmdIDs, cmdID)`, checked **after** the
`ActiveCmdID` match. Test: `TestActiveCompensationCommandIsNotTreatedAsDuplicate`.

### 3.2 ⚠⚠ CRITICAL — `cloneState` aliases the new slice

`cloneState` (`engine/step_state.go:361`) is `s := st` plus field-by-field deep copies; the only
cursor field deep-copied is `Compensating.Records`. Executed, with a control:

```
after clone write: orig[0]="MUTATED" clone[0]="MUTATED"  ALIASED=true
two clones append: a=[MUTATED c2 fromB] b=[MUTATED c2 fromB]  A_LOST_ITS_APPEND=true
CONTROL Records:  orig="n1" clone="MUTATED"                  ALIASED=false
```

The cursor's own comments justify its fields as "plain scalars, keeping this struct value-copyable
by `cloneState`" — this design breaks that invariant. Consequence: two clones of one base append
into the same slot, a dispatched id silently vanishes, and the 422 returns non-deterministically.

**Required**: a deep-copy line + `TestCloneStateDeepCopiesRecentCompensationCmdIDs`.

⚠ **CORRECTED (second audit, A-F5) — there is NO existing gate.** This previously asserted that
`TestStepDoesNotMutateInput` "is the existing gate this trips". **Measured**: with the field added
and deliberately left aliased, that test **PASSES**, and so does the entire `./engine` package
(EXIT=0). Its fixture builds no compensation cursor at all and contains zero assertions naming
`Compensating` — it is structurally incapable of observing the aliasing. The new test is therefore
the **only** gate and must be mutation-verified.

⚠ Note where this false claim came from: it was **the first audit's own prescribed fix**, restated
here — inside a document whose §4 opens with *"Check the fixture, not the assertion line."*

⚠ Under ADR Decision 5 the set moves to `InstanceState`, where the deep-copy one-liner sits beside
the existing `s.DeferredCompensationThrows = append([]string(nil), …)` — which measures
`ALIASED=false`, i.e. the working precedent.

### 3.3 ⚠⚠ CRITICAL — the retry timer is never retired, then fires against a zeroed cursor

`stepCompensationFinish` zeroes the cursor and calls `cancelCompensationStallTimers`, which filters
**strictly** on `Kind == TimerCompensationStall` in both loops — a retry record is invisible to it.
ADR-0178's guard then deliberately exempts `walkScoped()` kinds, so the leaked timer fires against
`compensationCursor{}` (nil `Records`, `NextIndex` 0) — the shape ADR-0171 documents as having
**panicked in the pure core in a consumer's process**.

The stall kind is safe for two reasons this design provides for neither: the sweep retires it, and
`handleCompensationStallFired` re-checks `rec.CommandID == ActiveCmdID`.

**Required**: extend the sweep to every `walkScoped()` kind; give the retry handler the same
late-fire check **and** an empty-cursor bail; tests for both.

### 3.4 ⚠⚠ CRITICAL — the "load-bearing edit" is in `runtime`, not `engine`

The pre-split design cited `engine/step_compensation.go:1338-1344` as the cause-of-death exclusion
site. **That range is a comment.** The publication is two `runtime` functions reading
`st.Incidents[0].Error` unconditionally — `runtime/outbox.go:81` (`terminalEventErr`) and
`runtime/processdriver_action.go:31` (`terminalErr`). ⚠ `Incidents[0]` is **positional**, so on a
cancel walk with no prior incident the new kind *is* index 0 and becomes the published cause of
death.

**Required**: this bundle's packages are **`engine` and `runtime`**; a shared kind-filtering helper
used by both resolvers; verification must run `./runtime/...`. ⚠ `./runtime/...` is **not**
container-free — a subagent implementing this needs Docker stated in its brief.

### 3.5 MAJOR — retirement sites are **five**, not four

`retireCompensationStallIncidents` is called at `step_compensation.go:{524,1287,1294,1345}`
**plus `engine/state.go:475`**, the `endInstance` remainder sweep — the transition on which the
"strands and is published as cause of death" hazard actually fires. The pre-split count of four was
the signature of a single-file grep.

Materially, `IncidentCompensationFailed` is deliberately **not** retired, so the interesting
direction is the reverse: it must **survive** `endInstance`. It does — `removeOrphanedIncidents`
only deletes incidents with `TokenID != ""`, and this one carries `""` — but nothing in the design
said so and nothing tested it. **Required**: a test asserting survival across both `endInstance`
sweeps on a terminating walk.

### 3.6 MAJOR — a **fifth** dispatch site, and a stale counting test

There are four `compensationInvoke` sites today. This design's automatic retry **adds a fifth**, so
"record the command id at all four dispatch sites" is stale on arrival — and a late reply to a
superseded *retry* command would still 422, the very defect this ADR exists to close. That is the
ADR-0175 counting failure repeating inside the bundle documenting it.

**Required**: "every `compensationInvoke` call site, re-derived by `grep` **after** the retry
lands", and a test that **derives** the set rather than hard-coding a count.

### 3.7 MAJOR — `RetryAttempts` has no stated reset point

A walk drains many records. Cursor-scoped attempts with no per-record reset means the first poison
record exhausts the budget and every subsequent record gets zero retries; the mirror bug is
unbounded retrying. **Required**: the budget is **per record**, zeroed when `NextIndex` advances,
with a test using ≥ 2 failing records.

### 3.8 MAJOR — the boundedness claim is false

"Bounded by the walk's dispatch count" ignores ADR-0175's operator verb `retryStalledCompensation`,
which sets a fresh `ActiveCmdID` per invocation with **no cap**, while whole-state `json.Marshal`
re-persists the growing slice every step. **Required**: either cap it (ring of the last K ids) or
state plainly that the operator-driven term is unbounded. ⚠ The pre-split ADR restated the spec's
hedged sentence as plain fact — this repo's named restatement defect.

### 3.9 MAJOR — the backoff-redelivery window is unmodelled

No document said whether `ActiveCmdID` is cleared on failure. Keep it → a redelivered
`ActionFailed` is not a duplicate (§3.1 excludes the active id) → second incident, second retry
timer, doubled attempt count, **two timers dispatching the same record**, which is the
double-refund hazard ADR-0034's post-acceptance fix exists to prevent. Clear it → §3.3's late-fire
check cannot be written. **Required**: specify the window's state machine explicitly, plus
`TestRedeliveredActionFailedDuringBackoffDoesNotDoubleArm`.

### 3.10 MAJOR — "reusing `retryStalledCompensation`" was an unexecuted analogy

Source-verified, that function (a) retires **stall** incidents on both branches — the wrong
incident, and silent about per-attempt `IncidentCompensationFailed` accumulation; (b) arms
`armCompensationStallTimer` — the **wrong timer kind**; (c) has no policy, no counter, no
exhaustion branch. Everything genuinely new is *outside* the template, so "reusing" understated the
work and concealed §3.3 and §3.7. **Required**: a new `retryFailedCompensation`, with the
differences spelled out.

### 3.11 Also required

- **`walkScoped()` must be SPLIT, not extended** — see ADR Decision 4.
  ⚠ **CORRECTED (second audit, A-F2 + B-B12, converged independently).** Extending the single
  predicate is **harmful**: it also drives `HasArmedTimers`, so the retry backoff would measure
  `HasArmedTimers=false`, `Classify.Reason="unknown"`, `AutoTimers fires=false`, and every consumer
  opting in would get `ErrUnhandledPark`.
  ⚠ **The justification was ALSO false (C-2).** "ADR-0178's guard refuses **every** compensation
  retry and this ADR silently never works" is a false universal: the guard is
  `!walkScoped() && !spawnsNewWork()`, and `spawnsNewWork()` is **true** on any resuming walk
  (measured on all five frames of a throw walk). The work is required for **terminating** walks.
  ⚠⚠ The first audit had **already demanded this exact quantifier be narrowed**; the rewrite took
  the fixture half of that fix and dropped the quantifier half, then restated the universal in four
  places — while §4 test 12 of this same document contradicts it.
- ⚠ **Downgrade hazard — THREE terms, not two.** Persistence is whole-state `json.Marshal` with no
  `DisallowUnknownFields`. A downgrade drops `RecentCompensationCmdIDs` (re-opening the 422), drops
  `RetryAttempts` (**resetting the retry budget, so a poison compensation retries forever**), and
  — ⚠ added by the second audit (A-F4) — drops **`IncidentKind`**, degrading the new incident into a
  *resolvable, deletable* `IncidentAction` that the cause-of-death allow-list then also fails to
  exclude. Severity is **higher** than for the stall kind, because this incident is deliberately
  long-lived and so is likelier to be present when a mixed-version deployment round-trips the row.
- `engine/command.go:66`'s comment ("intermediate, deadline, in-wait, and retry timers") omits
  `TimerCompensationStall` and is made strictly more wrong by this ADR's new kind. Fix here.
- `Incident.Kind` already exists (ADR-0175); this adds a **constant**, not a field.
- An instance cancelled while a retry backoff is pending receives a **422**
  (`ErrInvalidTransition` → `StatusUnprocessableEntity`) — ⚠ **not the "409" this bullet previously
  claimed** (second audit C-4/B-B8: ADR-0180 was amended in-bundle, and the inherited sentence came
  from its un-amended source). And on a **cancel-started** walk `ErrCancelNotApplicable` is not
  returned at all, because `walkTerminates` gates it. **Decided**: documented, in the ADR's
  Consequences.

---

## §4 — Test plan, with what makes each test fail today

⚠ Check the **fixture**, not the assertion line.

1. **Visibility** — `ActionFailed` yields one WARN + one `IncidentCompensationFailed`, walk
   continues. **Fails today**: measured `incidents=NONE`, `grep -c slog` → 0.
2. **The active command is not a duplicate** (§3.1). **Fails** against the naive predicate — and
   its failure mode is a hung walk, so assert forward progress, not just the absence of an error.
3. **`cloneState` deep-copies `DispatchedCmdIDs`** (§3.2). **Fails** without the copy line; the
   audit's control proves the assertion is not vacuous.
4. **The retry timer is retired at walk finish**, and a leaked one bails on an empty cursor (§3.3).
5. **The incident is not published as the cause of death** — at **both** `runtime` sites (§3.4),
   including the cancel-walk case where it is `Incidents[0]`.
6. **The incident survives `endInstance`'s two sweeps** (§3.5).
7. **Retry re-dispatches under a fresh command id** after backoff. **Fails today**: measured
   `re-dispatched c3 again? false` with `MaxAttempts:5`.
8. **Per-record budget** — two failing records, each getting its own attempts (§3.7).
9. **Redelivered `ActionFailed` during backoff does not double-arm** (§3.9).
10. **Late reply to a superseded command is benign**, both reply kinds, **including a superseded
    retry command** (§3.6). **Fails today**: measured 422 for both.
11. **Every dispatch site records its command id** — the test **derives** the site set (§3.6).
12. **A compensation retry timer fires on a dying walk** — the `walkScoped()` extension (§3.11).
    ⚠ Use a **cancel-started** walk: a throw walk measures `SpawnsNewWork = TRUE` and the test
    passes regardless of the guard.

**Mutation duty**: break the production line, observe RED, restore from a `cp` backup (⚠ never
`git checkout <path>`), `diff` to confirm.

---

## §5 — Sequencing

Bundle **A** (ADR-0177/0178/0180) introduces `walkScoped()` and must merge first, or this bundle
has no predicate to extend. Bundle **B** is independent. This bundle spans `engine` **and**
`runtime`, so unlike bundle A it is not single-package — but the two phases are ordered, not
parallel, because the `runtime` edit consumes the engine's new incident kind.

**Before implementation: this rewritten design needs its own rule-#9 audit**, with one lens
dedicated to re-counting (the dispatch-site count has now been wrong twice in this decision's
history).

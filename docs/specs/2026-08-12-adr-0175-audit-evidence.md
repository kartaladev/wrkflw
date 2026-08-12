# ADR-0175 bundle audit — record and adjudication

**Date:** 2026-08-12
**Bundle audited:** `f6fd63c` on `feat/stalled-compensation-walk-escape` (spec + ADR-0175 + plan)
**Base:** `main` @ `5270838` (ADR-0174)
**Rule:** CLAUDE.md #9 — ONE adversarial audit over the whole bundle, before implementation.

Three Opus agents, three lenses, each in its own `git worktree`, each briefed to **attack
and EXECUTE** rather than read. Findings were written incrementally to a path outside the
worktrees so they would survive; this file is the in-repo record, because auditor
write-ups have died in worktrees before and left dangling citations (repaired in `02b72be`).

| lens | agent brief |
|---|---|
| **Execution** | refute the load-bearing behavioural claims by running them |
| **Consistency** | every premise row, every quantifier, cross-document drift, dangling citations, tests that cannot fail |
| **Failure modes** | design holes, crash/rehydration, timer leaks, collisions with ADR-0170/0171/0173/0174, concurrency, security |

⚠ **All three worktrees were created at the base commit WITHOUT the bundle.** Each agent's
brief required verifying this first, and all three recovered it (`git checkout <branch> -- <paths>`,
`git reset --hard f6fd63c`, detached checkout). Without that step-0 instruction, three
agents would have audited documents that were not there. Keep the instruction.

---

## 1. Claims that SURVIVED execution

Stated first so they are not re-fought. These are real results, not absences of findings.

- **P1 — CONFIRMED TWICE, once with a control.** `handleTimerFired`'s path-4 `Kind` switch
  runs on a DYING walk. Execution lens: on `spawnsNewWork()==false`, `TimerDeadline` and
  `TimerInWait` fires consumed the record (`timersAfter=0`) while a control
  `TimerIntermediate` — which has no path-4 case — left it intact (`timersAfter=1`). The
  control is what makes it an experiment rather than a coincidence. The detection design's
  foundation holds.
- **P11 — CONFIRMED BY MUTATION.** `ScheduleTimer.Token` was renamed; `go build ./...` and
  `go vet ./...` (which compiles Docker-only test packages) surfaced eight sites, **all
  writes**, all in `engine`. Nothing reads it. Stronger than claimed:
  `engine/state_arms.go:117` already documents an intentional `Token == ""`.
  Restored from byte-exact `cp` backups, never `git checkout`.
- **Retry readability — CONFIRMED** for the pinned cursor, including the shape the spec
  asked for (non-empty `ArchiveKey`): archive drained to `[undo1]` yet
  `cursorRecords[NextIndex] = undo2`, `len=2`.
- **Crash survival of DETECTION — CONFIRMED.** `wrkflw_timers.kind` is a plain
  `SMALLINT NOT NULL` with **no CHECK constraint** in any of the three dialects, so a fifth
  `TimerKind` persists and decodes; `RehydrateTimers` re-arms a non-recurring timer at its
  original `NextRun`; `timerRecord.CommandID` rides the instance snapshot. ADR-0159's
  one-shot classification also holds.
- **Abandon discharges the deferred cancel — CONFIRMED** (the item the plan flagged as
  riskiest): `pendingCancel` true→false, `status=terminated`, `EndedAt` stamped, one
  `FailInstance{Err:"cancelled"}`.
- **P7 — CONFIRMED, and it is load-bearing.** `ResolveIncident` on a `TokenID:""` incident
  returns `err=<nil>, cmds=[], incidents=0` — it silently eats it. §5.4's refusal is real
  protection, not defensiveness.
- **The spec's own §4.1/§4.2/§4.3/§4.4 measurement tables — REPRODUCED EXACTLY** by two
  independent lenses, including error strings, the `StartInstance` anomaly, its stale
  cursor and its moved `StartedAt`.
- **P4, P5, P6, P8, P9, P10, P12, P14, P15, P16 — verified**; the `HANDOVER.md` quotation is
  verbatim; every cited symbol exists with the spelling given; the abandon rule, the
  optional-`IncidentID` rule and the incident-cleanup rule were stated identically across
  all three documents.

## 2. The three Criticals

### C1 — There are THREE compensation dispatch sites, not two (consistency lens)

**Controller-verified independently** before acceptance:

```
engine/step_compensation.go:410   beginCompensation
engine/step_compensation.go:479   stepCompensationAdvance
engine/step_nodes.go:1135         startCompensationWalk    ← absent from the bundle
```

`startCompensationWalk`'s own comment states it: *"The first record is dispatched HERE
rather than through stepCompensationAdvance, so its ownership hand-off happens here too
(ADR-0173)."*

The spec asserted *"`beginCompensation` and `stepCompensationAdvance` **each** dispatch
exactly one compensation `InvokeAction`"* — a two-item enumeration that had rotted — and it
propagated into ADR Decision 1, plan Phase 1.4, and the arm-site list.

**Consequence:** a single-record compensation-throw walk would get **no detection at all**,
and a multi-record one only from record 2 onward. The deadlock the spec's own §4.3 measures
arises at exactly the site the design missed.

**ADJUDICATED: ACCEPTED.** All three documents corrected to three sites; plan Phase 1 gains
`engine/step_nodes.go`; new test **T1c** (a scope-wide throw's FIRST dispatch arms a stall
timer). This is the CLAUDE.md "prefer naming a closed set over counting it / count them
again" rule, failed.

### C2 — `abandon` retains ALREADY-DISPATCHED records; the promised rollback re-runs them

Found independently by the **failure-modes** and **consistency** lenses.

Root cause, which the bundle had backwards: `consumeDispatchedRecord` only acts when
`cur.Records` is **pinned**, and **only `startCompensationWalk` pins**. On a
`beginCompensation` walk it early-returns on `len(cur.Records) == 0`, so `RootCompensations`
still holds every record — run or not.

```
PROBE[after-undoB]     invoked=[undoA] activeCmd="probe-1-c4" nextIndex=0
                       RootCompensations=[svcA/undoA svcB/undoB]  archive=map[]
PROBE[abandon-terminate] status=terminated cmds=[FailInstance]
                       RootCompensations=[svcA/undoA svcB/undoB]
PROBE[later-rollback]  [undoB undoA]      <== undoB had already run
```

`doClearRecords: true` on that branch is documented as the anti-double-compensation guard.
Flipping it blanket-false retains the whole list, so the admin rollback ADR-0175 explicitly
promises re-dispatches completed money-moving undo work.

**ADJUDICATED: ACCEPTED, owner decision taken.** Abandon retains **`[0 .. NextIndex-1]`** —
strictly what the walk never dispatched — and **drops the stalled record at `NextIndex`**,
whose action may still be in flight at the worker. This makes abandon consistent with
`skip`, which also gives up on the stalled record. Recorded consequence: if the stalled
action genuinely never ran, that undo work is lost; the operator who wants it must `retry`,
not `abandon`.

### C3 — `abandon` on a THROW walk destroys un-run records

Found independently by the **failure-modes** and **execution** lenses.

`stepCompensationFinish` picks its plan from `walkMode()`, so a throw walk takes a **resume**
plan and the "terminate plan only" override never applies.

- `walkThrowTargeted` — the in-flight record was already consumed from the archive at
  dispatch. Measured: `archive [undoIA,undoIB] → [undoIA]`, abandon never puts `undoIB`
  back, and the instance **resumes** rather than terminating.
- `walkThrowScopeWide` — `drainedCount = StartRecordCount` clears the whole drained prefix.
  Measured `root=[]` with `undoA` never dispatched. `drainedCount = StartRecordCount` is
  sound only for a walk that ran to completion; abandon breaks that precondition.

**ADJUDICATED: ACCEPTED, owner decision taken.** `abandon` is **refused with a named error
on any walk whose `walkMode() != walkAdmin`**, and the error names `skip` as the verb to use
instead. `skip` already drains a resuming walk to its natural resume, so no escape is lost.
Rejected alternative: re-parking the remainder, which would add a new write direction into
ADR-0173's audited ownership machinery — the exact code ADR-0168/0171/0173/0174 have each
had to correct.

## 3. Remaining findings and adjudications

| # | Sev | Finding | Adjudication |
|---|---|---|---|
| A-1 | High | Stall timer **leaks on every resume finish** — only terminal finishes reach `cancelAllTimers`; the four resume finishes touch `s.Timers` not at all. Measured: record survives onto a Running instance, no `CancelTimer`. Spec asserted coverage that does not exist; plan had no finish-site cancel item and no test. | **ACCEPTED.** Explicit cancel in `stepCompensationFinish` before the plan switch, covering all five modes. New plan item + test + mutation. |
| A-2 | High | **Already-stalled instances are undetectable** — both arm sites are dispatch sites; a stalled walk never dispatches again; `RehydrateTimers` reads the timer store, not instance state. A consumer upgrading *because* they have wedged instances sees zero incidents. | **ACCEPTED**, owner decision: project `Compensating.ActiveCmdID` + `compensating_since` into `service.ProcessInstance` so wedged instances are findable by listing, and state the migration gap in the ADR. Recovery-arm entry point rejected as a second way for a stall timer to exist. |
| A-3 | High | **No idempotency guard on the verbs.** The `CommandID` guard exists only on the timer-fire path; the trigger carries no command id and `IncidentID` is optional. Measured: retry ran the action twice; the original completion was rejected with `no token awaiting command` — a 422 the worker sees, i.e. a redelivery loop on an at-least-once transport. | **ACCEPTED**, owner decision: `CommandID` becomes a **required** field on `ResolveCompensationStall`, refused when it does not match `s.Compensating.ActiveCmdID`. `IncidentID` stays optional. Narrows the earlier optional-`IncidentID` decision deliberately. |
| A-4 | High | **A stall incident survives a NORMAL terminal finish** (no verb involved) and becomes the published cause of death: `terminalEventErr` and `terminalErr` both return `Incidents[0].Error` unconditionally, so `instance.terminated` reports `"compensation action stalled"` while the command still carries `"cancelled"`. Measured. | **ACCEPTED.** Incident retirement moves into `stepCompensationAdvance` (covering `ActionCompleted`, `ActionFailed` and `skip` through one path) **plus** a sweep at `endInstance`. ⚠ Ordering trap accepted with it: on the verbs the sweep must run **before** `stepCompensationFinish`, because `applyFinish → endInstance` clears `s.Compensating` and a later sweep has no `ActiveCmdID` to match. |
| A-5 | High | **Retry readability is only PARTIALLY true.** With `Records == nil` the `cursorRecords` fallback goes out of bounds — measured `len(records)=0` at `NextIndex=1`. §5.3 stated it unconditionally and put the bound only in test T13, so the natural reading **panics inside the pure core**. Citing P9 points at the wrong index: P9 guards `NextIndex-1`, retry reads `NextIndex`. | **ACCEPTED.** Retry's own bound moves into the design text, with the P9 mis-citation corrected. |
| A-6 | High | **"Nothing breaks, by construction" is false.** (a) A 16th trigger variant reddens four exhaustiveness tables the plan never lists (`trigger_validate_test.go`, `trigger_terminal_policy_test.go`, `step_terminal_dispatch_test.go`, `step_harvest_terminal_admission_test.go`); `MarshalTrigger` hard-errors on an unhandled variant. (b) Enabled detection changes the terminal outbox payload. (c) `Incident.Kind` and `timerRecord.CommandID` **enter the persisted snapshot** — `store_core.go` marshals the whole `InstanceState` — so an old build round-trips a new snapshot with `Kind` **dropped**, degrading an `IncidentCompensationStall` to a resolvable `IncidentAction` that the shipped endpoint will delete. | **ACCEPTED in full.** §6 rewritten from a blanket claim to a closed set; the mixed-version rule is recorded as a deployment constraint (do not run pre- and post-0175 builds against one store); the four test files join plan Phase 3. |
| A-7 | High | **`ADR-0034 §2.5` does not exist** — that ADR has no numbered sections; the best-effort-skip contract is **Decision 4**. Cited 6× across the bundle, **inherited from a false code comment** at `engine/step_triggers.go:291`. | **ACCEPTED.** All six replaced with "ADR-0034 Decision 4". The code comment is a separate pre-existing defect → backlog. Textbook "re-verify claims you inherit before restating them". |
| A-8 | High | **"no operator action can move it"** is contradicted two paragraphs later by the bundle's own table (`ActionFailed` advances; `StartInstance` is accepted). Both spec and ADR. | **ACCEPTED.** Reworded to "no clock and no scheduler … exactly one operator action does move it, and one destroys it". |
| A-9 | High | **"the reachable escape count is zero"** is false — `ProcessDriver.ApplyTrigger` is exported and `runtime` is a module-root public package, i.e. *the* product. A consumer's own worker holds the `CommandID`. | **ACCEPTED.** Narrowed to "through the `service`/HTTP operator surface", with the `ApplyTrigger` route stated and bounded (only for a caller that already holds the id; not at all after a driver restart). |
| A-10 | High | **P17 draws no conclusion, and its implication qualifies the whole framing.** `performInvokeAction` wraps every non-fire-and-forget invocation in a 30 s default timeout and turns failure into `ActionFailed`, which advances the walk. **The in-process synchronous path self-heals.** | **ACCEPTED.** §1 now scopes "never reports back" to the genuine shapes: a driver crash between commit and reply, a lost out-of-process callback, `WithActionTimeout(0)`. |
| A-11 | Med-High | **Empty-`IncidentID` verbs are accepted against a HEALTHY walk** — the only precondition is "a walk is in flight", which a 500 ms-old dispatch satisfies. | **ACCEPTED**, resolved by A-3: the required `CommandID` plus its cursor match is the evidence-of-intent guard. An operator naming the current command id is stating intent explicitly. |
| A-12 | Medium | **Plan contradicts itself on package scope**: "Phases 1–4 entirely inside `engine`" vs Phase 3's `internal/persistence/store/trigger_codec.go`. That package is **not** container-free (25 test files import `dbtest`). | **ACCEPTED.** Constraint restated; Phase 3's Docker requirement stated explicitly for the subagent brief. |
| A-13 | Medium | **The `ASSUMPTION (unverified)` was a dodge** — one container-free `runtime` test settled it, and the cited reasoning chain was wrong (`Drive` calls `deliverLoop` directly and never routes through `createAtNode`). Measured: `errors.Is(err, ErrInstanceExists) == true`. | **ACCEPTED.** Replaced with the measurement. CLAUDE.md permits the label only for claims that cannot be executed in reasonable time. |
| A-14 | Medium | **Quantifier defect**: "the walks that terminate are **exactly** the cancel and error walks" — omits the admin full rollback (`walkAdmin` covers it) and `walkReverse`, and a throw walk terminates iff `PendingCancel`. Repeated in the ADR and in P3's gloss. | **ACCEPTED.** Replaced with the closed set. |
| A-15 | Medium | **"Nothing else in the instance state references the walk"** is false for the throw walk the spec itself measures in §4.3 — throw walks are the only walks that leave sibling tokens running, so `beginCompensation` never ran and siblings' tokens/timers/waiters survive. | **ACCEPTED.** Scoped to `beginCompensation`-started walks, with the throw case stated. |
| A-16 | Medium | **T10 prescribes behaviour no Decision specifies** — nothing in §5.2, §5.3 or Decisions 1–4 says the *advance* path clears the incident, so an implementer following the ADR builds nothing and T10 fails. | **ACCEPTED**, folded into A-4's retirement paragraph. |
| A-17 | Medium | **`ADR-0164 carve-out #1` does not exist** (it is Decision 2), and the substance is narrower: measured, only the **full** rollback is admitted on a terminal instance; a partial rollback and `ReverseInstance` are both refused. | **ACCEPTED.** Citation and scope corrected. |
| A-18 | Medium | **The ADR-0173 quotation is a paraphrase lifted out of context.** Actual text: *"The alternative loses the record outright, which is worse."* — adjudicating ONE bounded case (a pre-ADR-0171 unpinned cursor), not a general preference for retention. Recruiting it as one is what licensed C2's double-run. | **ACCEPTED.** Quoted verbatim and scoped. |
| A-19 | Medium | **Retry re-arms without cancelling** the outstanding stall timer; two live records for one walk. | **ACCEPTED.** All three verbs route through A-1's cancel-then-arm helper; T6 asserts `len(Timers) == 1` with the NEW `CommandID`. |
| A-20 | Medium | **`CompensationStallAfter` is engine-wide only, and P13's cited precedent is a THREE-TIER chain** (`OverrideRetryPolicy` > node > default) that exists *because* one timeout for every action is wrong. A ledger reversal returns in ms; a manual-approval refund takes hours. | **ACCEPTED as a documentation fix, not a design change.** P13 stops being cited as precedent for a shape it does not have; the flat knob is recorded as a deliberate v1 simplification and the per-node tier goes to backlog. |
| A-21 | Medium | **Nothing bounds repeated `retry`**; `Incident.Attempts` stays 0 and the cursor carries no counter, unlike `tok.RetryAttempts`. | **ACCEPTED as documentation.** ADR states retries are unbounded by design and that repeated stalls accumulate incidents. A `StallRetries` counter goes to backlog. |
| A-22 | Medium | **T11's fixture must be `StatusCompensating`** — on a terminated instance `dispatch` refuses first with `ErrInstanceTerminal`, so the test would pass for the wrong reason and its "incident still present" assertion would be satisfied by dispatch, not by the new guard. | **ACCEPTED.** Fixture pinned in the plan; T11c added to tell the two refusals apart. Exactly the "test that cannot fail" pattern. |
| A-23 | Low-Med | **Security**: `retry` is a remote re-execution primitive on a surface with two known-open authz defects (self-asserted actor identity, fail-open `AuthzSpec`); `abandon` is destructive and sits at the same privilege level. `incidentJSON` does not carry `CommandID`, which blocks A-3's fix. | **ACCEPTED.** ADR states the required privilege (`abandon` gated separately from `retry`/`skip`); Phase 5 must exercise the authorization path, not only the happy path. Surfacing `CommandID` is now a deliberate choice with a stated cost (it is an internal `<instance>-cN` sequence oracle). |
| A-24 | Low | **`incident_count` inflates** — the three dialects project it as a JSON array length, so a stall incident raises it for a compensating instance in every admin listing. | **ACCEPTED** into the rewritten §6. |
| A-25 | Low | **`String()` omission would ship silently** — `command_test.go:224` names `TimerRetry` explicitly, so a new kind with no case would not redden it. | **ACCEPTED.** Plan requires its own assertion. |
| A-26 | Low | **Abandon changes the persisted shape** of a terminal instance (`RootCompensations` array instead of `null`) — analogous to ADR-0174's `Scopes` `[]`→`null`, which the ADR cites as precedent without noticing. | **ACCEPTED** into §6 and Consequences. |
| A-27 | Low | **Count slips**: "fourth **consecutive** delivery" (ADR-0158/0172 shipped between 0171 and 0173); "the **dominant** real-world cause" is an unverifiable empirical quantifier. | **ACCEPTED.** "fourth delivery in five"; the cause claim marked as the design's assumption. |
| A-28 | Low | **T2 cannot assert byte-identity with another git revision**; **T14 pins existing behaviour** and its column admits nothing makes it fail today. | **ACCEPTED.** T2 becomes an explicit golden list captured from `main`; T14 moves out of the falsifiability table into a "regression pins" subsection with its fixture requirement stated. |

## 4. Pre-existing defects the audit found in SHIPPED code

Not caused by this delivery; recorded so they are not lost.

1. ⚠ **A `TimerInWait` reminder fired on a `spawnsNewWork()==false` instance emits a real
   `InvokeAction`** — an ADR-0172 hole in `handleTimerFired` path 4. Token and record were
   planted; natural reachability is a throw walk carrying `PendingCancel`. It also corrects
   this bundle's reasoning: *"path 4 is safe on a dying instance"* is true for the new kind
   **only because its handler emits nothing**, which is now a stated constraint on the
   handler rather than an inherited property.
2. **`engine/step_triggers.go:291` cites a nonexistent `ADR-0034 §2.5`** — the origin of
   A-7's rot.
3. **`StartInstance` on a resuming walk leaves `PendingCancel=true`** on the now-Running
   instance — the same defect as handover backlog item 14.

## 5. Owner decisions taken at adjudication

| # | Decision |
|---|---|
| D1 | **C3** — `abandon` is refused on any walk whose `walkMode() != walkAdmin`, naming `skip` as the alternative. Re-parking the remainder rejected. |
| D2 | **C2** — `abandon` retains `[0 .. NextIndex-1]` and drops the stalled record at `NextIndex`. Accepted cost: a genuinely-never-run stalled action's undo work is lost; `retry` is the verb for that case. |
| D3 | **A-2 + A-3** — project `Compensating.ActiveCmdID` and `compensating_since` into `service.ProcessInstance`; make `CommandID` a **required**, cursor-matched field on the trigger. `IncidentID` stays optional. Recovery-arm entry point rejected. |

## 6. Process notes for the next audit

- **Three lenses converged on two of the three Criticals**, and the third (C1) was found by
  exactly one lens — the one told to re-count enumerations. Do not drop that lens.
- **Every Critical was found by EXECUTION or by re-counting, none by re-reading prose.**
- **The step-0 "verify the worktree contains the bundle" instruction earned its place**: all
  three worktrees were created without it.
- **The bundle's own measurements were honest** — every §4 number reproduced exactly under
  two independent lenses. The failures were all in *generalisation from* those numbers:
  a two-item enumeration, three false quantifiers, an inherited citation, and a paraphrased
  quote recruited beyond its scope.

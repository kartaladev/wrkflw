# Plan — an operator escape from a stalled compensation walk

**Spec:** [`docs/specs/2026-08-12-operator-escape-from-a-stalled-compensation-walk.md`](../specs/2026-08-12-operator-escape-from-a-stalled-compensation-walk.md)
**ADR:** [ADR-0175](../adr/0175-an-operator-escape-from-a-stalled-compensation-walk.md)
**Audit record:** [`docs/specs/2026-08-12-adr-0175-audit-evidence.md`](../specs/2026-08-12-adr-0175-audit-evidence.md)
**Branch:** `feat/stalled-compensation-walk-escape` (based on `main` @ ADR-0174)

---

## ▶ Progress

**Phase 0 (rule-#9 audit) ✅ COMPLETE.** Three Opus lenses, three Criticals, ~30 findings, all
adjudicated; accepted fixes folded into the spec, the ADR and this plan.

**Phases 1–4 ✅ IMPLEMENTED** in `engine` (+ the trigger codec in
`internal/persistence/store`), every symbol TDD'd with an observable RED, every prescribed
mutation executed. Six things execution corrected about the design are listed below and are
OWED to the ADR and spec at Phase 6.

| phase | state |
|---|---|
| 0 — adversarial bundle audit | ✅ complete — 3 Criticals, all folded |
| 1 — the timer kind and its arm/cancel sites | ✅ complete |
| 2 — the fire handler and the incident kind | ✅ complete |
| 3 — the trigger and its three verbs | ✅ complete |
| 4 — the `handleResolveIncident` refusal | ✅ complete |
| 5 — runtime + service + HTTP surface + projection | ✅ complete |
| 6 — documents describe what shipped | ✅ complete |
| 7 — delivery gate | 🔶 Verification 1–3 PASS; `/code-review` + `/security-review` are OWNER-INVOKED and still owed |

Branch `feat/stalled-compensation-walk-escape` (re-derive the SHA; the bundle commit is
amended on every phase). `main` unchanged at `5270838`.

### ⚠ What EXECUTION corrected about the design (owed to ADR + spec at Phase 6)

**1. `abandon` does NOT discharge the deferred-cancel deadlock — `skip` does. The ADR and
spec §5.3 are WRONG as written, and the two decisions are mutually incompatible.**
`PendingCancel` is stamped by exactly two writers: `handleCancelRequested`, which requires
`ResumeNode != "" || ReverseNode != ""` — i.e. a walk that RESUMES — and
`deferFailureToInFlightCompensationWalk`. The audit's C3 finding then made `abandon` refuse
every non-`walkAdmin` walk, which is exactly the set that can carry `PendingCancel`. So the
sentence *"abandon is what discharges the deferred-cancel deadlock"* was true before C3 and
false after it; nobody re-checked it when C3 landed. Measured:

```
PROBE[throw-walk]    mode=walkThrowScopeWide pendingCancel=false
PROBE[after-cancel]  cmds=0                  pendingCancel=true
PROBE[after-skip-1]  status=compensating     pendingCancel=true
PROBE[after-skip-2]  status=terminated       pendingCancel=false  cmds=[CancelTimer FailInstance{cancelled}]
```

Pinned by `TestSkipDischargesTheDeferredCancelDeadlock`, which also asserts abandon is
refused there. **Amend ADR Decision 3 and spec §5.3.**

**2. THREE exhaustiveness tables redden, not four.** Measured one at a time:
`trigger_validate_test.go` (verified by removing the registration),
`trigger_terminal_policy_test.go` and `step_terminal_dispatch_test.go` all redden;
`step_harvest_terminal_admission_test.go` does **not**, and its own header says so — *"the
enumeration below is deliberately NOT presented as exhaustive: it is hand-maintained, and a
NEW trigger will not appear in it (the sibling catches that)"*. **Correct spec §6 and the
Phase 3 file list.**

**3. `abandon`'s record retention must be re-installed AFTER the finish, not before.**
`walkAdmin`'s `finishPlan` sets `doClearRecords`, so `applyPlanRecordClearing` nils the whole
scope list on the way out and any earlier trim is erased — measured `root=[]` where
`[step1 step2]` was owed, with the later admin rollback then refused *"nothing left to
compensate"*. The retained prefix is written onto `res.State` (a `StepResult` carries a
shallow COPY, so mutating `s` afterwards is invisible).

**4. `retry`'s vanished-source branch must be given the real `ctx`/`def`/`at`.** It routes to
`stepCompensationFinish`, which DRIVES; a nil `def` panicked in `drive` on `def.Node`.
Pinned by `TestRetryOnAVanishedRecordSourceFinishesWithoutPanicking`.

**5. A new pre-existing defect, unrelated to this delivery but touched by it.** With
detection OFF, the cancel path already turns `s.Timers` from nil into an empty non-nil slice
(`beginCompensation`'s prologue runs `cancelTimersByTaskID` for the parked human task).
Measured on `main` @ `5270838`: `timersNil=false`. The throw path does leave nil, which is
why the new writer's no-op-when-disabled property is pinned there instead
(`TestZeroWindowLeavesThrowWalkTimersUntouched`). **Backlog, not fixed here.**

**4b. ⚠ RETRACTED — I got this one wrong, and `/code-review` caught it.** I claimed the
abandon-path incident sweep was redundant because `endInstance` covered it. It does not:
`stepCompensationFinish` clears `s.Compensating` BEFORE calling `applyFinish`, so
`endInstance`'s remainder sweep sees `ActiveCmdID == ""` and early-returns. The line is
LOAD-BEARING. I inferred "redundant" from a green suite after deleting it — but the suite had
no assertion on incidents after abandon at all. **`TestAbandonRetiresTheStallIncident` now
exists and is RED without the line.** The ADR/spec text has been restored to its original
(correct) form, with the mis-correction recorded as the lesson: *a green suite is evidence
about the SUITE, never about the engine.*

**5b. The `handleResolveIncident` guard's POSITION is not what protects the incident.**
ADR-0175 decision 4 says the refusal is placed *"before the removal line"* and the plan's
T11b prescribes a mutation moving it below, expecting the *still present* assertion to
redden. Measured: moving it below leaves the test fully GREEN. `Step` returns the ZERO
`StepResult` on error, so the caller discards the clone whose slice the removal mutated. The
mutation that DOES redden is deleting the guard outright (`err=<nil>` with the incident
consumed — the pre-0175 behaviour). **Amend ADR decision 4, spec §5.4 and the Phase 4 row:
the guard is load-bearing, its position is defence-in-depth.**

**6. Threading decision the bundle did not make.** The stall window reaches the three arm
sites through a new `stepPolicy` value (`mode`, `eval`, `stallAfter`) that REPLACES the
hand-threaded `(mode StepMode, eval ConditionEvaluator)` pair across 15 functions plus
`stepCtx`. Behaviour-preserving, verified green before and after; owner chose it over adding
a third positional parameter.

### What the audit changed about what gets BUILT

1. **THREE arm sites, not two.** `startCompensationWalk` (`engine/step_nodes.go:1135`) is the
   compensation-throw walk's first dispatch and was missing from the design entirely — so a
   single-record throw walk would have had no detection at all.
2. **`abandon` is refused unless `walkMode() == walkAdmin`.** On a throw walk it was measured
   destroying un-run records (`{sub=[undoIA,undoIB]} → {sub=[undoIA]}`; `root=[]`).
3. **`abandon` retains `[0 .. NextIndex-1]` only**, dropping the stalled record. Retaining the
   whole list was measured re-dispatching `[undoB undoA]` with `undoB` already run.
4. **`CommandID` is a REQUIRED, cursor-matched field on the trigger** — without it a
   compensation action was measured running twice.
5. **A finish-site timer cancel is a new work item** — the four *resume* finishes never reach
   `cancelAllTimers`, so the stall record leaks onto a Running instance.
6. **The cursor is projected into `service.ProcessInstance`** so already-stalled instances are
   findable and the required `CommandID` is readable.

## Execution constraints

- ⚠ **Phases 1, 2 and 4 are entirely inside `engine`. Phase 3 additionally touches
  `internal/persistence/store` (the trigger codec).** All four still run **STRICTLY SERIAL**
  because each depends on the previous. Concurrent agents in one Go package break each
  other's `go test` compile even on disjoint files. Phase 5 may fan out **by package**.
- ⚠ **`internal/persistence/store` is NOT container-free** — 25 of its test files import
  `dbtest`. A Phase 3 brief must state the Docker constraint explicitly and name
  `go test -count=1 -run TestMarshalTrigger ./internal/persistence/store/` as the codec check.
- **TDD is non-negotiable**: for every new symbol, a `Write` of the test, then a `Bash` run
  showing RED, then the implementation, then GREEN. A transcript with no visible red state
  fails review regardless of coverage.
- Where a row says *mutation*, implementation owes a real one: break the production line,
  observe RED, restore, `diff` to confirm byte-clean. ⚠ **Commit before mutating** —
  `git checkout <path>` restores from the INDEX and has destroyed uncommitted work twice
  here. Restore from `cp` backups instead (the audit did).
- ⚠ **Check the FIXTURE, not the assertion text.** Every skip-vs-abandon test needs **two**
  compensable activities; a one-record walk finishes on the first advance and cannot
  discriminate the verbs.
- Docker: standing permission for the Verification coverage and no-regressions runs only.
  `engine`, `runtime/{calllink,signal,task}`, `service`, `processtest`, `transport/http` are
  container-free; `./runtime/...` as a whole is **not**.
- A subagent that must measure against a patched tree gets a `git worktree`, and its brief
  must say so **and** require verifying the worktree contains the bundle. ⚠ **All three audit
  worktrees were created WITHOUT it** — that step-0 instruction earned its place.

## Phase 0 — rule-#9 adversarial audit ✅ COMPLETE

Three lenses (execution / consistency / failure-modes), each in its own worktree, briefed to
attack and to EXECUTE. Record committed at
`docs/specs/2026-08-12-adr-0175-audit-evidence.md`.

**Survived execution:** P1 (twice, once with a `TimerIntermediate` control), P11 (by
mutation — all eight `ScheduleTimer.Token` sites are writes), retry-readability with a
non-empty `ArchiveKey`, crash survival of detection, abandon discharging the deferred cancel,
and every §4 measurement reproduced exactly under two lenses.

**Three Criticals**, folded above. ⚠ The failures were all in *generalisation* — a rotted
two-item enumeration, three false quantifiers, an inherited citation restated six times, and
a paraphrased quote recruited beyond its scope. The measurements themselves were honest.

## Phase 1 — the timer kind and its arm/cancel sites ⬜

**Files:** `engine/command.go`, `engine/state_timers.go`, `engine/step.go`,
`engine/step_compensation.go`, **`engine/step_nodes.go`**.

1. `TimerCompensationStall` **appended** to the `TimerKind` iota block, plus its `String()`
   case. ⚠ Append — an existing constant's value must not shift, or every persisted timer row
   is reinterpreted.
2. `timerRecord.CommandID string`.
3. `StepOptions.CompensationStallAfter time.Duration`, documented as *zero disables*.
4. One helper `armCompensationStallTimer(s, cur, nodeID, opt)` — **cancel-then-arm**, so a
   re-arm never leaves two live records — called from **all three** dispatch sites:
   `beginCompensation` (⚠ **after** `s.cancelAllTimers()`), `startCompensationWalk`,
   `stepCompensationAdvance`.
5. **An explicit cancel in `stepCompensationFinish`, before its plan switch**, so all five
   walk modes are covered.

| test | what makes it fail today |
|---|---|
| T1 | `beginCompensation` with the window set emits `ScheduleTimer{Kind: TimerCompensationStall}` — the kind does not exist |
| T1b | **mutation**: hoist the arm above `cancelAllTimers` ⇒ T1 reddens |
| **T1c** | a scope-wide throw's **FIRST** dispatch arms one — `startCompensationWalk` has no arm |
| T2 | window at zero ⇒ `beginCompensation` emits exactly `[UpdateTask, InvokeAction{undoB}]` and `s.Timers` is empty. ⚠ An explicit **golden list captured from `main`** — no test can assert equality with another git revision |
| T2b | the advance cancels the previous timer and arms one with the NEW `ActiveCmdID`; `len(Timers) == 1` |
| **T2c** | a **resume** finish emits `CancelTimer` and leaves `s.Timers` empty — measured leak today |
| — | `TimerCompensationStall.String()` needs its **own** assertion: `command_test.go`'s TimerKind test names `TimerRetry` explicitly, so a missing case ships silently |

**Verify:** `go test -count=1 ./engine/... > /tmp/p1.log 2>&1; echo EXIT=$?`

## Phase 2 — the fire handler and the incident kind ⬜

**Files:** `engine/state.go`, `engine/step_triggers.go`.

1. `IncidentKind` with `IncidentAction` at **iota 0**, `IncidentCompensationStall` at 1;
   `Incident.Kind IncidentKind`.
2. A `case TimerCompensationStall:` in `handleTimerFired`'s path-4 `Kind` switch.
3. The handler applies spec §5.2's guard-then-act and **emits no commands** — ⚠ that is a
   *constraint*, not an inherited property: a `TimerInWait` reminder was measured emitting a
   real `InvokeAction` from path 4 on a dying instance.
4. **Incident retirement in `stepCompensationAdvance`**, before it recomputes the cursor, plus
   a sweep in `endInstance`.

| test | what makes it fail today |
|---|---|
| T3 | fire ⇒ one `Incident{Kind: IncidentCompensationStall, TokenID: ""}`, zero commands, cursor byte-identical |
| T4 | fire on a **dying** walk still raises it — the P1 test |
| T4b | **mutation**: move the case below the `spawnsNewWork()` guard ⇒ T4 reddens |
| T5 | `CommandID` ≠ `ActiveCmdID` ⇒ no incident, no commands |
| T5b | **mutation**: delete the comparison ⇒ T5 reddens |
| T10 | late `ActionCompleted` ⇒ advance **and** incident cleared |
| **T10b** | the same for a late `ActionFailed` — both route to `stepCompensationAdvance`; a sweep placed in `handleActionCompleted` would miss this |
| **T10c** | a **normal** terminal finish leaves **zero** stall incidents and `terminalEventErr` reports `"cancelled"` — measured today as `incidents=1` / `"compensation action stalled"` |

## Phase 3 — the trigger and its three verbs ⬜

**Files:** `engine/trigger.go`, `engine/trigger_validate.go`, `engine/step.go`,
`engine/step_compensation.go`, `internal/persistence/store/trigger_codec.go`, **plus the four
exhaustiveness tables**: `engine/trigger_validate_test.go` (`allTriggerVariants`),
`engine/trigger_terminal_policy_test.go`, `engine/step_terminal_dispatch_test.go`,
`engine/step_harvest_terminal_admission_test.go`.

1. `CompensationDisposition`, `ResolveCompensationStall` (**required** `CommandID`, optional
   `IncidentID`), the three constructors, `terminalPolicy() = rejectWithError`.
2. `handleResolveCompensationStall`: refuse on no walk in flight or a non-matching
   `CommandID` (error + `slog.Warn`); refuse a non-empty `IncidentID` naming nothing.
3. **retry** — own bounds check on `cur.NextIndex` before indexing; do **not** re-run
   `consumeDispatchedRecord`.
4. **skip** — delegate to `stepCompensationAdvance`.
5. **abandon** — refuse unless `walkMode() == walkAdmin`; retain `[0 .. NextIndex-1]`, drop
   `NextIndex`.
6. Incident cleanup **before** delegating to `stepCompensationFinish` (⚠ `applyFinish →
   endInstance` clears `s.Compensating`, so a later sweep matches nothing).

| test | what makes it fail today |
|---|---|
| T6 | retry re-dispatches under a NEW command id; `len(Timers) == 1` carrying it |
| T6b | retry with a non-empty `ArchiveKey` still finds the record |
| T7 | skip advances — same stream as the measured `ActionFailed` lever |
| T8 | abandon on `walkAdmin` ⇒ terminal, `RootCompensations == [0..NextIndex-1]` |
| **T8b** | **mutation**: retain the whole list ⇒ the later-rollback assertion reddens on `[undoB undoA]` vs `[undoA]` |
| **T8c** | abandon on throw / partial / reverse ⇒ **named error**, state untouched |
| T9 | abandon on a `walkAdmin` walk carrying `PendingCancel` ⇒ cancel consumed, instance terminates |
| T12 | every verb with no walk in flight, and with a non-matching `CommandID` ⇒ error + Warn (swap the handler per `observability_noop_test.go`; the core logs through package-level `slog`) |
| T13 | retry against an unpinned cursor with a gone source ⇒ routes to finish, **no panic** |
| T13b | codec round-trip for all three dispositions |
| **T13c** | the four exhaustiveness tables classify the new variant |

## Phase 4 — the `handleResolveIncident` refusal ⬜

**File:** `engine/step_triggers.go`. The guard goes **before** the
`s.Incidents = append(s.Incidents[:idx], …)` removal line.

| test | what makes it fail today |
|---|---|
| T11 | `ResolveIncident` on an `IncidentCompensationStall`, instance **`StatusCompensating`** ⇒ error, incident **still present**. ⚠ The fixture MUST be compensating: on a terminated instance `dispatch` returns `ErrInstanceTerminal` first and the test passes for the wrong reason |
| T11b | **mutation**: move the guard below the removal line ⇒ T11's *still present* assertion reddens. ⚠ If it does not, the assertion checks only the error and is worthless |
| **T11c** | the same call on a **terminated** instance ⇒ `ErrInstanceTerminal`, telling the two refusals apart |

## Phase 5 — runtime, service, HTTP, projection ⬜

May fan out **by package** once Phase 3 lands.

- `runtime`: `WithCompensationStallTimeout(d)`; thread `CompensationStallAfter` into the
  driver's `StepOptions`; `ProcessDriver.ResolveCompensationStall(...)` via `applyTrigger`.
- `service`: the request type and `Service` method; **project `Compensating.ActiveCmdID` and
  `compensating_since` into `ProcessInstance`**, plus `IncidentKind`.
- `transport/http/httpcore`: endpoint + DTO, mounted in the stdlib, gin and fiber groups.
- `processtest`: **exclude `TimerCompensationStall` from `Park.HasArmedTimers`**, or
  `AutoTimers()` fires stall timers by itself in consumers' harnesses. Update `park.go`'s
  KNOWN GAP comment and handover backlog item 13(c).
- T15: end-to-end through `service` and HTTP, **including the authorization path** — `retry`
  is a re-execution primitive and `abandon` is destructive, on a surface with two known-open
  authz defects.

⚠ `runtime`'s timer tests are not container-free as a package; scope any Docker-needing run
explicitly in the agent's brief.

## Phase 6 — documents describe what shipped ⬜

Re-read all three documents against the built code and correct every divergence — **most
importantly any behaviour the ADR promises that implementation changed or dropped** (rule
#11: amend the ADR in the same bundle, with the measurement that refuted it). Sweep the
diff's own comments for unexecuted claims and over-reaching quantifiers.

Expect corrections. Three preceding deliveries into this machinery had design claims die on
execution, and this bundle already had three Criticals die at audit.

## Phase 7 — delivery gate ⬜

1. `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` — ≥ 85 %
   over hand-written code, hot paths and their failure branches first. Probe Docker and run;
   if it is down, say so and label any container-free subset as partial.
2. `go test ./...` from the repo root — no regressions.
3. `golangci-lint run ./...` — repo-wide, not `./engine/...`. Probe for the binary; if
   absent, offer to install or skip, and report which happened.
4. `/code-review`, then `/security-review` — **owner-invoked only**. Fix all findings, folded
   via `--amend`, and **re-run the suite after each fix**.
5. Merge `--no-ff` to `main` and push.

## Verification checklist

- [x] Phase 0 audit adjudicated in writing; accepted fixes folded into all three documents
- [x] Audit record committed in-repo
- [ ] Every new symbol has an observable RED in the transcript
- [ ] Every mutation executed, RED observed, restore verified byte-clean (via `cp`, not `git checkout`)
- [ ] **T1c** proves the third arm site exists (the C1 gap)
- [ ] **T8b** proves abandon does not retain already-run records (the C2 measurement)
- [ ] **T8c** proves abandon is refused on a resuming walk (the C3 measurement)
- [ ] **T2c** proves the resume-finish cancel closes the timer leak
- [ ] **T10c** proves a normal terminal finish no longer publishes `"compensation action stalled"`
- [ ] T11's fixture is `StatusCompensating`, and T11b's mutation reddens the *still present* assertion
- [ ] New trigger round-trips the codec **and** the four exhaustiveness tables
- [ ] `go vet ./...` compiles every test file, including Docker-only ones
- [ ] Verification items 1–3 pass, with any partial run labelled as partial
- [ ] `/code-review` and `/security-review` findings all fixed or explicitly adjudicated
- [ ] `docs/plans/HANDOVER.md` rewritten **in place**; auto-memory updated to point at it

## Backlog this delivery opens or confirms

**Pre-existing, found by the audit:**

1. ⚠ A `TimerInWait` reminder fired on a `spawnsNewWork()==false` instance emits a real
   `InvokeAction` — an ADR-0172 hole in `handleTimerFired` path 4.
2. `engine/step_triggers.go:291` cites a nonexistent `ADR-0034 §2.5` (it is Decision 4) — the
   origin of the citation rot that reached this bundle six times.
3. `StartInstance` accepted on a `compensating` instance, restarting it with a stale cursor;
   on a resuming walk it also leaves `PendingCancel=true` (handover item 14).
4. `CancelInstance` reports success for a cancel that did nothing.

**Deferred from this design:**

5. A per-node `CompensationStallAfter` tier, mirroring `effectiveRetryPolicy`'s three-tier
   chain.
6. A bound on repeated `retry` (a `StallRetries` counter stamped into `Incident.Attempts`).
7. A retry/incident story for a compensation action returning `ActionFailed` (handover item
   11) — changes ADR-0034 Decision 4's contract.

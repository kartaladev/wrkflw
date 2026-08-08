# Plan — ADR-0168 + ADR-0169 + ADR-0170 + ADR-0171 (one bundle)

- Date: 2026-08-08
- Spec: `docs/specs/2026-08-08-compensation-walk-and-mid-delivery-terminal.md`
- ADRs: **0168, 0169, 0170, 0171** — `docs/adr/0168-a-compensation-walk-blocks-completion.md`,
  `docs/adr/0169-a-delivery-stops-at-a-mid-delivery-terminal.md`,
  `docs/adr/0170-an-unhandled-error-does-not-restart-a-live-compensation-walk.md`,
  `docs/adr/0171-a-compensation-walk-owns-its-record-source-and-resume-scope.md`
  (**0170 reworked and 0171 added at the delivery gate** — see the Progress block)
- Branch: `fix/compensation-walk-and-mid-delivery-terminal`
- Base: `main` @ `7180114` (re-derive: `git rev-parse --short main`)

## ▶ Progress

**Status: IMPLEMENTED and self-gated. Awaiting the owner-only reviews, then merge.**

- [x] Spec written; every line ref re-verified by execution
- [x] ADR-0168, ADR-0169 written
- [x] **Rule-#9 adversarial audit — 3 Opus auditors, isolated worktrees, ~35 findings**
- [x] Findings adjudicated; accepted fixes folded into spec + both ADRs
- [x] **ADR-0170 added as a result of the audit** (owner-adjudicated fold-in), fix shape
      executed and measured before writing
- [x] Handover checkpoint (rule #10)
- [x] Implementation (**Tasks 1–7**, plus 2b/3b opened by implementation)
- [x] Delivery Gate steps 1–2 and the stand-in reviews
- [x] **Delivery Gate step 3: `/code-review` — RUN, 5 findings, ALL resolved** (below)
- [ ] Delivery Gate step 4: `/security-review` — **OWNER-ONLY, still to run**
- [ ] Merge `--no-ff` and push

### Implementation state — 2026-08-10

Branch `fix/compensation-walk-and-mid-delivery-terminal`, **ONE commit**, code + tests + spec
+ three ADRs + this plan folded together via `--amend`; subject corrected to `fix(engine):`.
Unpushed.

⚠ **Do not quote this bundle's own SHA here** — every `--amend` invalidates it, and the gate
still has amends to come (CLAUDE.md rule #10). Name the branch.

| task | state |
|---|---|
| **1** — re-fixture `TestActionCompletedOnTerminalInstanceIsNoOp` | ✅ landed |
| **2** — ADR-0168 completion guard, T1/T2/T3 (+2 ESP tests) | ✅ landed |
| **2b** — amend docs for the ESP inversion | ✅ landed (this block, ADR-0168 Decision 3, spec §5.1/§8/§9, plan Tasks 2/7 + checklist) |
| **6** — evidence-file correction | ✅ landed |
| **3** — ADR-0169, T4′/T5/T6/T8/T9 | ✅ landed |
| **3b** — amend docs for the T9 / defence-in-depth finding | ✅ landed |
| **4** — ADR-0170 (`endInFlightCompensationWalk`), 4 mutations | ✅ landed |
| **5** — `processtest` T10 + stale citations | ✅ landed |
| **7** — mutation record (18 rows) | ✅ landed |
| **9** — ADR-0170 deferral rework (gate finding) | ✅ landed |
| **10** — **ADR-0171** pin the walk's record source + hold the scope exit | ✅ landed |
| **8** — Delivery Gate | ▶ owner-only half remains |

`go test ./engine/` **EXIT=0**; `engine` coverage **92.4 %** (baseline was 91.9 %).
Full gate: `go test -race -coverprofile ./...` EXIT=0, 64 pkgs, **0 races**, repo **73.9 %**.
`go vet ./...` EXIT=0; `golangci-lint run ./...` 0 issues; `gofmt` clean.

### ⚠ The bundle's own design claim that implementation refuted

**ADR-0168's two event-sub-process conjuncts were NOT "provably non-discriminating", and those
sites are NOT unreachable.** The spec (§5.1, §8, §9), ADR-0168 (Decision 3) and the audit
evidence (§7.3) all asserted the opposite. Both reproductions were built in Task 2; the
controller re-verified independently by reverting **only** the two ESP conjuncts (leaving
`exitRootScope` patched) → both tests RED → byte-clean restore.

The supporting measurement — *"patch `exitRootScope` alone → suite `EXIT=0`"* — was **correct**.
Its inference was not. `EXIT=0` established that **no test covered those sites**, which is the
"suite-green is not verification" trap this bundle's **own spec §6** was written to document,
recurring inside the bundle that documents it — and it survived three Opus auditors briefed to
execute, because *this* claim was the one nobody thought to run.

**Lesson for the next bundle:** an audit that executes the *headline* claims can still ship a
false *supporting* claim. A sentence of the form "X is provably non-discriminating / unreachable"
is a behavioural claim about the code and needs its own execution — the absence of a failing
test is evidence about the suite, never about the engine.

### ✅ `/code-review` (the real gate) — 5 findings, all resolved

⚠ **The gate found things BOTH adversarial Opus stand-ins missed, on a bundle that had already
passed a three-auditor design audit, full implementation and two stand-in reviews.** This is the
fourth delivery in a row where that held. Stand-ins reduce rework; they are not the gate.

⚠ Its first invocation **died on a session limit after one tool call and returned zero
findings.** An empty result from a review that never ran is not a pass — it was re-run.

| # | finding | resolution |
|---|---|---|
| **F2** Medium | `handleCancelRequested` sets `PendingCancel` without the pending outcome, so a cancel arriving after a deferred error silently inherits the **error's** outcome — measured `FailInstance{Err:"E1"}` where `terminated`/`"cancelled"` was due. `state.go`'s comment asserted the opposite. | **FIXED.** Outcome stamped explicitly; the zero-value overload that caused it is now documented as covering exactly one input (a pre-fix snapshot) and **pinned by its own test**, so the branch does not become uncovered |
| **F3** Medium | ADR-0171's hold is consulted only from `exitSubprocessScope`; `exitNestedEventSubprocessScope`'s close wedges the walk — `defForScope: unknown scope` on the walk's own ack, forever | **REPRODUCED, then FIXED — and the fix is broader than the finding.** `applyFinish` now drops a resume whose scope is gone (WARN) and completes if nothing else runs. That closes **both** unheld routes, including the error-boundary strand ADR-0171 had shipped as an accepted bound. Extending the hold was rejected with a reason: on the interrupting route the enclosing scope's work has just been cancelled, so resuming into it is the wrong outcome |
| **F1** Medium | ADR-0170's deferral closed the last escape from a stalled walk | **VERIFIED — PARTIAL regression, escape adjudicated OUT OF SCOPE.** Measured both trees: for **root**-records the pre-0170 code did *not* terminate either (it clobbered the cursor and dispatched a **second** `undoA`) — pre-existing, backlog 13(a). For **sub-process-scope** records it *did* terminate immediately, so 0170 narrows that to a deferral — the deliberate price of not orphaning a live walk. The escape itself is a new trigger-level decision, already owed by ADR-0168 |
| **F4** Low | ADR-0168's conjunct silently retires root event-sub-process arms when the walk later resumes | **REPRODUCED and FIXED — the reviewer's proposed mitigation REJECTED as unsafe.** `rearmRootESP` re-arms *every* root ESP, resurrecting an interrupting one-shot arm that already fired. Instead the tail's `removeEventTriggeredSubprocessArmsForScope("")` is **removed**: those arms belong to the root scope, which is never closed, and every terminal path sweeps them via `endInstance` → `cancelAllScheduledWork` |
| **F5** Low | Comment falsified by ADR-0171 (`cursorRecords` "reads the live records") | **FIXED, and the sweep found two more of the same class** (`ArchiveKey`'s doc, `applyFinish`'s) |

**Controller-verified independently:** disabling the F3 recovery (`resumeDropped := false`) sends
**both** `…ErrorBoundaryTearsDownItsScope` and `…NestedEventSubprocessTearingDownItsResumeScope`
RED; restored byte-clean.

⚠ **Two of the gate's own claims were corrected by execution**, which is the same discipline
applied back at the reviewer: F3's "no trigger can terminate it" is overstated —
`CancelRequested` succeeds with zero commands and unblocks the finish; what is permanent is that
the walk's own ack can never be applied. And F4's proposed fix was measured unsafe. **A finding
from the real gate is a lead, not a verdict.**

### ⚠⚠⚠ The fourth: ADR-0168 alone turns a silent wrong answer into a PANIC — ADR-0171

Surfaced by an **untracked `FABLE_AUDIT.md`** that appeared in the tree mid-session (not written
by this session's agents; left uncommitted). Its finding D was marked
`ASSUMPTION (unverified)` there, so it was executed rather than believed.

**It reproduces.** A sibling branch inside the throwing scope drains it mid-walk; the drain
archives and closes the scope while the cursor still names it, and `stepCompensationAdvance`
re-reads records **live** from `scopeByID(scopeID).Compensations`:

```
WALK START:                 status=compensating activeCmd="zzd-c3" scopeID="zzd-s1" tokens=1
AFTER SIBLING DRAINS SCOPE: status=compensating activeCmd="zzd-c3" scopeID="zzd-s1" scopes=0 tokens=0
AFTER WALK ActionCompleted: panic: index out of range [0] with length 0
```

⚠ **ADR-0168 changes the outcome, which is what put it in scope.** Controller-measured with the
three conjuncts reverted: **no panic** — the instance silently goes `completed` over the
unfinished rollback. So the bundle converts silent wrong-completion into a **panic inside the
pure engine core, i.e. in the consumer's process**. Owner adjudicated: full fix, in-bundle,
**ADR-0171**. `0168 must not be delivered alone.`

**The one-record variant the audit also predicted was itself unverified — and it reproduces**,
as a permanent wedge rather than a panic: `defForScope: unknown scope` on every redelivery.
Cause is the destroyed *resume scope*, not the record source.

🚨 **This is also the third correction to ADR-0168's event-sub-process story, and it retires the
second one.** The `EventTriggeredSubprocesses` **2 → 0** loss that this plan and the spec both
recorded as a *"measured, accepted cost"* was **not a cost**. It was this defect in the ESP
fixtures' clothing: both fixtures stopped **exactly one `Step` short** of the real failure, and
driven one step further on the pre-0171 tree they wedge permanently. With ADR-0171 the arms stay
**2** and the instance completes.

> **The lesson: a fixture that stops at the first surprising observation will certify that
> surprise as the design's price. Drive it to termination before calling anything an accepted
> cost.** "Accepted cost" is a claim about behaviour and earns no exemption from execution.

**Two bounds recorded rather than hidden**, both measured:
- **ADR-0168's conjunct 3 (`exitNestedEventSubprocessScope`) is now uncovered** — ADR-0171's
  hold returns before those fixtures reach it, so reverting it leaves `EXIT=0`. Kept, not
  deleted: *undemonstrated is not unreachable*, the exact error this delivery already had to
  amend once. Conjunct 2 was re-covered with a purpose-built root-scope fixture.
- ADR-0171 does **not** fully fix the error-boundary teardown route (the record is compensated;
  the finish still wedges on the destroyed resume scope) and does not cover
  `exitNestedEventSubprocessScope`'s sibling close. Both pinned as falsifiable `KNOWN
  LIMITATION` assertions.

⚠ **One element of the owner-adjudicated fix shape was measured DEAD and deleted**: the hold
predicate's `ScopeID` disjunct. The discriminating field is `ResumeScope` alone. And the pin
(element 1) turned out **not** to protect the drain route at all — the hold does — so rather
than ship it unverified the implementer found the route the hold *cannot* defer (an error
boundary on the enclosing sub-process) and pinned it there. **A prescribed fix element that no
test can distinguish is a prescribed test that cannot fail.**

### ⚠⚠ The third: ADR-0170's decided shape was WRONG, and only the gate caught it

**Found by the gate's adversarial code-correctness stand-in, not by the design audit and not by
implementation.** ADR-0170's `endInFlightCompensationWalk` **converts** the live walk by stamping
the outcome on the existing cursor — and that cursor carries the in-flight walk's **narrow record
source** (`ArchiveKey`, or a sub-process `ScopeID`). Measured:

| fixture | shipped 0170 | correct |
|---|---|---|
| **targeted** throw (`WithCompensateRef`) + root record | `compensationsAfterError=[]`, `rootComps` **1 → 0** — `undoRoot` never runs **and its record is erased** | `[undoRoot]` |
| **nested scope-wide** throw + root record | `[undoB]`, `rootComps=1` stranded uncompensated | `[undoB undoRoot]` |
| **nested scope-wide** throw, no root record | guard bypassed entirely (it sat *inside* the records check) → walk **abandoned mid-flight**, `status=failed activeCmd=""`, its dispatched action orphaned | walk completes |

⚠ **This is NOT a regression against working behaviour.** Controller-verified: on unpatched `main`
the same targeted fixture **PANICS** — `index out of range [0] with length 0` in
`stepCompensationAdvance`. The bundle turned a panic into silent loss; the rework turns it into
correct behaviour.

⚠ **Hoisting the guard alone is NOT sufficient — measured.** It converts the abandonment into the
narrow-source bug. Both findings are one root cause: *the deferred termination inherits whatever
record source the in-flight walk had.*

✅ **REWORKED AND VERIFIED.** `engine/step_errors.go`'s guard is hoisted above the records test
and `endInFlightCompensationWalk` is now `deferFailureToInFlightCompensationWalk`;
`applyFinish` consumes a pending outcome. 9 mutations run (**M6/M8 NOT caught** — `pendingKind`
is pinned by no test on either path, recorded in-code rather than papered over with a test that
could not fail). Controller re-verified the hoist independently: un-hoisting fails **only** the
third fixture, confirming hoist and deferral are separately load-bearing. Full gate re-run:
`-race ./...` EXIT=0, 64 pkgs, 0 races, repo 73.9 %, `engine` 92.2 %, lint + vet clean.

**Owner adjudicated: fix in-bundle.** The shape reuses the deferral the engine **already ships**
for the sibling case — `handleCancelRequested` sets `PendingCancel` mid-walk and `applyFinish`
consumes it, clearing this walk's already-compensated records and re-entering `beginCompensation`
over the **remainder**. Generalized from a bool into a pending outcome. Chosen over widening the
cursor's record source, which would require re-basing `NextIndex` — a bare index with no record
identity — where **any off-by-one re-dispatches an already-run compensation**, i.e. buying the fix
with the exact defect ADR-0170 exists to prevent.

**Why the design missed it:** ADR-0170 only ever measured the **root scope-wide** throw, whose
record source happens to be all of `RootCompensations`. The decided shape was correct for the one
fixture it was derived from and wrong for every other throw shape. **A fix derived from a single
fixture inherits that fixture's shape as an unstated precondition** — the audit's four executed
mutations all reused it, so none could see the boundary.

### ⚠ The second design claim implementation refuted: T9 / the tiers-1–3 guard

**T9 as prescribed cannot fail, and no fixture can make it.** Spec §8 justified it with *"no test
exercises the tier-1→2 guard, the only guard site with zero coverage"* — which reads as "so write
one and it will go RED". Executed, the output is **byte-identical** on the guarded tree and on
unpatched `main`, across two distinct terminal routes, because `endInstance` →
`cancelAllScheduledWork` drains `ArmedEvents`, `Boundaries` and every ESP arm before tiers 2–3
run their lookups.

Controller-verified both directions on the built tree, with all five new tests present:
**delete the tiers-1–3 guard → whole package `EXIT=0`; delete the tier-4 in-loop guard →
`EXIT=1`** (`TestSignalDeliveryStopsInsideTheTokenLoop`). So exactly one of ADR-0169's two guard
sites closes an observable defect. The tiers-1–3 guard ships as **deliberate defence in depth**;
Decision 2's "two guard sites, not four" argument is structural and unaffected.

**Provenance worth recording:** T9 came from audit-evidence **§7.2 — the one recommendation in
that section that was never built and run**, unlike T4′/T5/T6 which were. It is the same defect
class the audit itself caught in T4 (a prescribed test that cannot pass), one level down: the
auditor that found T4 by executing prescribed a *new* test it did not execute. **A test prescribed
by an audit inherits no credibility from the audit's other findings.**

T9 is kept, relabelled as a PIN with these measurements in its doc comment and its fixture
extended to tier 3, because the two mechanisms are independent: if the owed ESP-hole ADR narrows
`cancelAllScheduledWork`'s drain, this test starts noticing arms firing on a dead instance.

### Audit record — what it changed

Three lenses (premises/enumerations, design consequences, test falsifiability), each in its own
`git worktree`, each briefed to EXECUTE rather than read. Highlights:

| finding | outcome |
|---|---|
| **T4 vacuous** — `len(Tokens)==0` measured `1` before AND after the fix; contradicted ADR-0169's own Consequences | ACCEPTED. Replaced by **T4′**. Found independently by **all three** auditors |
| **The deferral hang** — once deferred, only the cursor's own `ActionCompleted` releases the instance; `CancelRequested` emits zero commands | ACCEPTED. ADR-0168's "so deferral is not a hang" deleted; escape matrix added; operator escape → own ADR (owner decision) |
| **Cursor clobbering** — `handleUnhandledError` overwrites a live cursor; `undoA` dispatched twice; **the uncaught error silently swallowed, process reports success** | ACCEPTED and **FOLDED IN** (owner decision) → **ADR-0170** |
| **"A `CompleteInstance` can no longer describe an unfinished rollback"** — false; `forceTerminate(OutcomeComplete)` is a fourth site, measured byte-identical before/after | ACCEPTED. Qualified to "on the normal-completion route" + Negative bullet |
| **Tier order is NOT pinned** — swapping 2↔3 or 1↔2 leaves the suite `EXIT=0`; hoisting the lookups also `EXIT=0` | ACCEPTED. **T8** added; the no-hoist requirement promoted into ADR-0169 Decision 2 + a code comment |
| **"All seven are entry-level"** false (route 1 is state, not a trigger); no single ordered route list exists | ACCEPTED. Rewritten; "eighth" → "a further route" |
| ESP guards not inert on fallthrough; `:330` precedent invalidated by this very change | ACCEPTED. ADR-0168 Decision 3 rewritten; Task 1 must rewrite the `:330` comment |
| Decision 4's reason factually wrong (`terminalOutboxEvent` is status-driven) | ACCEPTED. Replaced with the two real reasons |
| no-Warn cost understated — the fix *removes* ADR-0161's existing WARN | ACCEPTED. Measured before/after added |
| `mergeVars` semantics delegated to ADR-0169, never stated there | ACCEPTED. Stated in Decision 4 |
| `processtest.Classify` → `ReasonUnknown` → `ErrUnhandledPark` | ACCEPTED. Named in ADR-0168; **T10** pins it |
| `!= StatusRunning` is **already shipped** at `step_eventsubprocess.go:167` and silences a legitimate signal | ACCEPTED. Strengthens spec §6; ESP ADR must cover both directions |
| Spec §3.1 fixture doesn't build as written; `WithForceTermination` arity elided | ACCEPTED. Both corrected |

**Confirmed by the audit, no action:** both predicates sound and complementary; the guards
compose (only the one expected failure across the whole container-free corpus); T6 discriminates
both guard placements and is required, not decorative; no double terminal-event publish; no
`runtime`/`service` path infers "tokens==0 ⇒ terminal"; every line reference and inherited count
in the bundle resolves; `engine` baseline **91.9 %** confirmed.

🛑 **TWO of those "confirmed, no action" items did not survive implementation.** Recorded here
rather than edited away, because a stale confirmation is more dangerous than a stale claim — it
tells the next reader the question was already settled:

1. *"T6 discriminates **both** guard placements"* — T6 discriminates the **in-loop vs
   ahead-of-loop** placement, which is its job. But **T4′** is protected by *either* ADR-0169
   guard independently, so the tiers-1–3 guard is not falsified by any test (Task 7, row 6).
2. *"every line reference … in the bundle resolves"* — true when written, **false in the commit
   that shipped it**: this bundle's own edits moved seven of them (`step_nodes.go:1035`→1079,
   `:557`→601, `step_terminal_test.go:454`→476, `step_errors.go:249`→254,
   `step_stale_commands.go:59`→62, `:99`→102, `step_triggers.go:959`→996). Found by the gate's
   stand-in reviewer and converted to **symbol citations**. The sharpest instance: the comment
   this bundle rewrote *to replace a rotted line number* named `1035` as stale while ADR-0168
   shipped `1035` as live. **A citation audit must be re-run AFTER the diff exists, not only
   against the base.**

## ⚠ Execution constraint — NO FAN-OUT

**Every code task touches `engine`.** Concurrent subagents inside one Go package break each
other's `go test` compile even on disjoint files. Dispatch **one subagent at a time**, review
its diff, then dispatch the next.

`engine` and `processtest` are container-free — **no Docker** for Tasks 1–5.

## Premise Discipline reminders for every task

- No claim about current behaviour enters a comment, ADR or test doc without being executed.
- Check the **fixture**, not the assertion text.
- Judge every run by its exit code: `go test ./engine/ > /tmp/out.log 2>&1; echo "EXIT=$?"`.
  Never `| grep | head`.
- `go test -run` on a nonexistent name **exits 0**. Confirm a test *ran* with `-v`.
- **The full audit evidence is in the repo**: `docs/specs/2026-08-08-adr-0168-0170-audit-evidence.md`
  — all three auditors' findings with verbatim measurements (§A/§B/§C), the measured ADR-0170
  patch (§D), and four compiling, executed test sources (§E).
  ⚠ **§E is REFERENCE, not a shortcut past TDD.** Rule #6 requires a visible RED per symbol and
  the owner audits the transcript for it. Use §E to confirm a fixture builds and to see which
  assertions were *proven* discriminating — then write the test yourself and observe its RED.

---

## Task 1 — re-fixture `TestActionCompletedOnTerminalInstanceIsNoOp` (LANDS FIRST)

Measured **fix-independent** (passes on unpatched `main`), so landing it first keeps every
intermediate state green and removes a "is this failure mine?" judgement call from Task 2.

**File:** `engine/step_terminal_test.go`.

Change the sibling branch's `end2` to:
`event.NewEnd("end2", event.WithForceTermination("sibling-terminates", event.OutcomeAbort))`
— **two arguments; `OutcomeAbort` explicitly.** `OutcomeComplete` is the zero value and would
bake the F4 counterexample into the permanent suite as the sanctioned fixture.

**Rewrite the WHOLE docstring, which runs from `:430` — not just the `:439-442` paragraph.**
Both halves are invalidated:

- `:430-437` states the premise ADR-0168 **removes**: *"a sibling branch can complete the
  instance while the walk's `InvokeAction` is still in flight."*
- `:439-442` explains why `forceTerminate` was *rejected* for this fixture — now obsolete, and
  measured false: force-termination **does** exercise the target scenario (step 4 still reaches
  ADR-0165's dispatch guard, WARN observed).

Replacement must say: ADR-0168 closes the original route; the test now uses an explicit
termination because that is the only remaining route to a terminal instance with a
dispatched-but-**unowned** compensation command. Do **not** claim the walk's command is
"outstanding" — force-termination clears the cursor.

**Verify:** both step-2 positive controls still hold (`IsTerminal()` false, `ActiveCmdID`
non-empty). Whole package EXIT=0.

---

## Task 2 — ADR-0168: the completion guard

**Files:** `engine/step_nodes.go`; tests in `engine/` (`head -1` the target file first —
`engine/` mixes `package engine` and `package engine_test`).

Add the cursor conjunct to the three `if len(c.s.Tokens) == 0` guards
(`grep -n 'len(c.s.Tokens) == 0' engine/step_nodes.go` — line numbers rot) in `exitRootScope`,
`exitRootEventSubprocessScope`, `exitNestedEventSubprocessScope`:

```go
if len(c.s.Tokens) == 0 && c.s.Compensating.ActiveCmdID == "" {
```

**Also rewrite the `:330` "DEFENSIVE, and unreachable today" comment.** Its stated reason —
*"nothing can be left for `len(c.s.Tokens) != 0` to find"* — is falsified by this very change,
which alters that tail's entry condition. New reason: no token **and** no live compensation
cursor.

**Tests T1, T2, T3** (spec §8; fixture §3.1 — note the explicit flow list, `rb` **must** have an
outgoing flow or no walk starts). T3 passes before and after: its doc comment must record that
it is a **regression pin** against the refuted `!= StatusRunning` shape, and that it
discriminates in **two** places (`fireBoundaryArm` and `handleSignalReceived`).

**Reachability duty.** Attempt a repro for the two ESP sites with a live cursor. If you cannot
build one, **say so** — record the attempted shapes here. Do not claim reachability you did not
demonstrate. If you succeed, the ESP-arm retirement on fallthrough must be an explicit
assertion.

✅ **DISCHARGED — and it inverted the bundle's expectation.** Both repros were built. Both sites
are reachable with a live cursor; **all three conjuncts discriminate.** Shape: a
**non-interrupting** ESP whose body is `svc(compensable) → fork ⇒ {CompensateThrow → end ; end}`,
throw branch declared first (interrupting starts tear down the enclosing scope's other ESP arms
at trigger time); the nested variant nests it in a regular sub-process with **no outgoing flow**.
Tests `TestCompensationWalkBlocksRootEventSubprocessCompletion` and
`…BlocksNestedEventSubprocessCompletion`. ESP-arm retirement asserted explicitly: a standby ESP
in the same scope measured `EventTriggeredSubprocesses` **2 → 0** while the instance stayed
`Compensating`. Controller re-verified independently by reverting **only** the two ESP conjuncts
→ both tests RED → byte-clean restore. ADR-0168 Decision 3, spec §5.1/§8/§9 amended in-bundle.

**Verify:** whole package EXIT=0 (Task 1 already fixed the one collision).

---

## Task 3 — ADR-0169: the mid-delivery terminal re-check

**Files:** `engine/step_triggers.go` (`handleSignalReceived`), tests in `engine/`.

Fold tiers 1–3 into a slice of lookup-and-fire closures; check `s.Status.IsTerminal()` once per
iteration and once **inside** the tier-4 loop. Abort returns
`StepResult{State: *s, Commands: signalCmds}`. No Warn.

⚠ **Each closure does its own lookup when it runs. Do NOT hoist the three lookups.** Measured:
hoisting leaves the suite `EXIT=0`, so nothing will catch it. Put that sentence in a code
comment above the slice.

**Preserve:** tier order (gateway → boundary → ESP), `matched`/`mergeVars` merge-once, and the
snapshot-before-tiers-1–3 behaviour. ⚠ **Tier order is NOT protected by existing tests** —
that is what T8 is for.

**Tests T4′, T5, T6, T8, T9** (spec §8). T6 is the placement falsifier — build the two-catch
fixture and verify the control (exactly two tokens awaiting the signal, `catch1` first) before
trusting it.

**Verify:** whole package EXIT=0.

---

## Task 4 — ADR-0170: don't restart a live compensation walk

**File:** `engine/step_errors.go`, `handleUnhandledError`'s compensate branch.

The measured shape (exact patch: audit-evidence §D): when
`s.Status == StatusCompensating && s.Compensating.ActiveCmdID != ""`, cancel the remaining
tokens/timers/arms as `beginCompensation` would, then clear `ResumeNode`/`ResumeScope` and stamp
`FinalStatus = StatusFailed` / `FinalErr = errorCode` on the **existing** cursor, and return.

⚠ **Clearing `ResumeNode` is load-bearing.** Stamping the outcome alone still completes the
process successfully, because `stepCompensationFinish` takes the resume branch whenever
`ResumeNode` is set. ⚠ **Keeping the cancellation is load-bearing too** — without it a third
parallel branch stays live and human-completable on a doomed instance.

**Tests:** the walk's cursor is unchanged across the error; `undoA` is dispatched **once**
(assert exactly one `InvokeAction` named `undoA` across both steps); the instance ends
**`failed`** with `FailInstance`, not `completed`; and with a third parked branch, `tokens == 0`
at the moment of the error. Each fails today — measured: cursor `c2`→`c3`, `undoA` twice, final
status `completed`.

🛑 **CORRECTED 2026-08-09 — only THREE of those four assertions are RED today.** The measured
list in the sentence above ("cursor `c2`→`c3`, `undoA` twice, final status `completed`") is
correct and enumerates exactly three; the **four**-item list preceding it reads as though all
four were falsifiers. They are not. `tokens == 0` at the error and the third branch's
cancellation **already hold on `main`** — the second walk `beginCompensation` wrongly starts
still drains every branch on its way. Those two are pins against the **stamp-only alternative
shape**, not RED-today assertions, and ADR-0170's own table already says so
(`branch C at error time: main = cancelled`). This is the quantifier/recap failure Premise
Discipline names: a correct detailed measurement, and a list above it that over-counts what the
measurement covered.

⚠ Implementation must still prove **each** assertion can fail — against the decided shape's
alternatives, not only against `main`. Four mutations were run and each produced a RED:
(A) guard removed → `undoA` count; (B) stamp-only, cancellation dropped → surviving `taskC`
token; (C) `ResumeNode`/`ResumeScope` not cleared → final status `completed` not `failed`;
(D) outcome not stamped → final status `terminated` with no error code. **Mutation B is why the
cancellation half exists**: without it, stamp-only is indistinguishable from the decided shape.

**Verify:** whole package EXIT=0.

---

## Task 5 — `processtest` gap (T10) + two stale in-code citations

1. **T10:** pin `processtest.Classify` returning `ReasonUnknown` for a zero-token
   `Compensating` instance, and add a godoc note on `Classify` recording the gap. This is a
   **pin of today's behaviour**, not a fix — say so in the test name and comment.
2. **`engine/step_stale_commands.go`** cites `startCompensationWalk (step_nodes.go:982-993)
   … at :983 … (:989-991)`. Actual: `1035-1047` / `1036` / `1044`. Pre-existing rot in the
   function this bundle leans on; cheap to fix here.

**Verify:** `go test ./engine/ ./processtest/` EXIT=0.

---

## Task 6 — correction note on the premise-evidence file

**File:** `docs/specs/2026-08-08-adr-0158-premise-evidence.md`.

Its §Q4(c) and "Two NEW pre-existing bugs" item 1 carry the `!= StatusRunning` shape described
as verified. It is refuted (spec §6). That file is a **preserved input to the future ADR-0158
rewrite**, so leaving it unmarked propagates the shape.

Add a correction block at both places pointing to ADR-0168/0169/0170 with the measured
stranding output, and note that the same predicate is **already shipped** at
`engine/step_eventsubprocess.go:167`. **Do not rewrite the original text** — mark it, do not
launder it.

---

## Task 7 — mutation verification

For **T1, T4′ and T6**: snapshot the production file, break the line on purpose, run, **observe
RED**, restore, `git diff` to confirm the restore is byte-clean. Record mutation → test →
result here.

🛑 **CORRECTED 2026-08-09.** This read: *"Only ONE of ADR-0168's three conjuncts discriminates …
record the two ESP conjuncts as non-discriminating today."* **Refuted by Task 2.** All three
discriminate, each against its own fixture. Run all three mutations: revert each conjunct
individually and confirm its own test goes RED. The old `EXIT=0` measurement was about test
coverage at the time, not about the conjuncts.

Additional mutations worth running, all measured discriminating during the audit: `!=
StatusRunning` substituted for `IsTerminal()` (T2 + T3 go RED); the guard hoisted ahead of the
tier-4 loop (T6 RED); `nil`-commands abort (T5 RED); tier reorder (T8 RED once written).

### ✅ MUTATION RECORD — executed 2026-08-09

Every row observed by exit code, then restored from a snapshot and `diff`-confirmed byte-clean.
**C** = run by the controller directly; **I** = run by the implementing subagent.

| # | mutation | test that went RED | observed | by |
|---|---|---|---|---|
| 1 | `exitRootScope` conjunct removed | `TestCompensationWalkInFlightBlocksCompletion` (T1) | `EXIT=1` — *"an instance with a compensation walk in flight is not complete"* | **C** |
| 2 | **both** ESP conjuncts removed (`exitRootScope` left patched) | both ESP tests | `EXIT=1` ×2 — *"an event-sub-process exit must not complete an instance whose walk is in flight"* | **C** |
| 3 | each ESP conjunct reverted individually | its own ESP test | `expected: 3, actual: 1` each | I |
| 4 | **both** ADR-0169 guards removed | `TestSignalDeliveryStopsAtMidDeliveryTerminal` (T4′) | `EXIT=1` — live `ScheduleTimer{TimerID:"i1-tm1", Token:"i1-t3", dur:3600000000000}` escapes | **C** |
| 5 | tier-4 **in-loop** guard removed (tiers-1–3 kept) | `TestSignalDeliveryStopsInsideTheTokenLoop` (T6) | `EXIT=1` | **C** |
| 6 | tiers-1–3 guard removed (tier-4 kept) | **none** | `EXIT=0` — defence in depth, see the T9 block above | **C** |
| 7 | guard hoisted *ahead of* the tier-4 loop | T6 only (T4′/T5/T8/T9 pass) | reproduces the audit's three-way table | I |
| 8 | three tier lookups hoisted | **none** | `EXIT=0` — why the no-hoist comment exists | I |
| 9 | tier order swapped 2↔3 | T8 | got `[gw esp bnd]` | I |
| 10 | tier order swapped 1↔2 | T8 | got `[bnd gw esp]` | I |
| 11 | `!= StatusRunning` substituted into `fireBoundaryArm` | T3 | `cmds=[]` — signal swallowed; rest of suite `EXIT=0`, so T3 is the **only** guard | I |
| 12 | ADR-0170 guard removed | `undoA` per-step count | `expected: 0, actual: 1` | I |
| 13 | ADR-0170 stamp-only (cancellation dropped) | surviving `taskC` token | token survives; only one `UpdateTask` | I |
| 14 | `ResumeNode`/`ResumeScope` not cleared | final status | `"endA"`; status `completed` not `failed` | I |
| 15 | `FinalStatus`/`FinalErr` not stamped | cursor stamp + final status | status `terminated`, no error code | I |
| 16–18 | T10: default arm → `ReasonAsyncChild`; `IsTerminal` accepts `Compensating`; `AutoTimers` acts on `ReasonUnknown` | T10 | `7→6`, `7→0`, decision `0→2` | I |

**Two results worth reading as findings, not bookkeeping:**

- **Row 6 is the T9 finding** — the tiers-1–3 guard is unfalsifiable today. Recorded rather than
  quietly passed over.
- **Row 4 needed BOTH guards removed.** T4′ is protected by *either* guard independently
  (aborting at tier 3 skips the tier-4 loop entirely), so it does **not** discriminate the guard
  *placement*. **T6 is the only placement falsifier** — exactly the role the audit assigned it.
  A mutation table that ran only row 4 would have "verified" a placement it never tested.

⚠ The pre-correction instruction below claimed only one of ADR-0168's conjuncts discriminates.
Rows 1–3 refute it.

---

## Task 8 — Delivery Gate

1. ⚠ **Requires Docker; ASK FIRST.**
   `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out`
   — hold `engine` **≥ 91.9 %**, repo **≥ 73.9 %**. Coverage is a floor, not a target.
   (`-race ./...` subsumes a plain `./...` run, so this is the only Docker step.)
2. `golangci-lint run ./...` clean; `go vet ./...` (compiles Docker-only test packages).
3. **Documents describe what shipped.** Re-read all three ADRs, the spec and this plan against
   the built code. Sweep the diff's comments for unexecuted claims and over-reaching
   quantifiers. If implementation contradicted an ADR, **amend the ADR in this bundle**.
4. Adversarial Opus stand-ins before the owner gate — briefed to EXECUTE against a `main`
   baseline, each in its own `git worktree`.
5. `/code-review`, then `/security-review` — **owner-invoked only**. Fix all findings, fold via
   `--amend`.
6. ⚠ **Fix the commit subject.** It is currently `fix(engine): …` on a docs-only commit; once
   the implementation is folded in via `--amend` that becomes accurate. Verify before merging.

Then merge `--no-ff` to `main` and push (standing preference).

## Verification checklist

- [x] Task 1 lands first; both step-2 positive controls verified live (`isTerminal=false`,
      `ActiveCmdID="i-stranded-compensation-c2"`; step 3 `terminated`/0 tokens/cursor cleared;
      step 4 WARN observed); `OutcomeAbort` used
- [x] `:330` comment rewritten — **both halves**, not just the reason: the tail is LIVE, so
      "DEFENSIVE, and unreachable today" was false twice over. Whole `:430`-docstring rewritten
- [x] T1–T3 RED-first, each RED observed in a Bash call; T3 documented as a pin.
      ⚠ T3's second discrimination claim (`handleSignalReceived`) could **not** be re-measured
      in Task 2 — that guard does not exist until Task 3 — and is labelled inherited-from-audit
      in the test comment, to be re-verified once Task 3 lands
- [x] Task 2's first T2 draft was **vacuous and passed unpatched** (counted `CompleteInstance`
      across both steps; the total is 1 either way). Pinning *which step* emits it made it
      falsifiable. Caught only because the RED was actually run
- [x] ESP-site reachability **demonstrated** (both sites; all three conjuncts discriminate —
      the bundle predicted the opposite; ADR/spec/plan amended in-bundle)
- [x] T4′ asserts position/History/Boundaries — **never** `len(Tokens) == 0`
- [x] T6 proven to fail the ahead-of-loop placement; fixture control **measured**
      (`[]string{"catch1","catch2"}` — two tokens awaiting, `catch1` first)
- [x] T8 (tier order) written and proven RED under both reorders (2↔3 and 1↔2); no-hoist code
      comment present and its claim **re-measured** (hoisting → `EXIT=0` even with the five new
      tests present)
- [x] **T9 corrected: it CANNOT fail and no fixture can make it.** Kept as a labelled PIN. Only
      the tier-4 in-loop guard closes an observable defect; the tiers-1–3 guard is defence in
      depth (`endInstance` → `cancelAllScheduledWork` drains every arm family first). Controller
      re-verified: delete tiers-1–3 guard → `EXIT=0`; delete tier-4 guard → `EXIT=1`
      (`TestSignalDeliveryStopsInsideTheTokenLoop`). ADR-0169 Decision 2 + spec §4.5/§8 amended
- [x] ADR-0170: `undoA` dispatched exactly once (counted **per step**, `r3==1`/`r4==0`, not as a
      cross-step total); instance ends `failed`; third branch cancelled. 4 mutations, each RED,
      each restored byte-clean. ⚠ Two of the four prescribed assertions are pins against the
      stamp-only shape, not RED-today — see Task 4's correction block
- [x] ADR-0170 ↔ ADR-0168 interaction measured in **both** directions (0170 stands alone;
      0168 needs 0170 — panic probe discriminates). `beginCompensation`'s "zero cursor here"
      comment was **false on `main`** and becomes true with 0170 on covered paths
- [x] T10 pins `ReasonUnknown` (`TestClassifyPinsUnknownReasonForCompensationWalkPark`);
      `Classify` godoc notes the gap. Re-measured post-Tasks 1–4 on **two** states — a
      hand-built snapshot and a real engine-produced mid-walk state — not inherited from the
      spec. 3 mutations → RED, each restored byte-clean.
      ⚠ **Spec §7's severity was overstated and is corrected there**: the harness reproduction
      it describes was built and **never reaches the park** — the synchronous drive loop
      completes the walk inside one `ApplyTrigger`. The gap bites a **stored** mid-walk snapshot
- [x] Stale citations fixed in `engine/step_stale_commands.go` — **line numbers dropped, not
      re-derived**: the citation had moved twice (`982 → 1035 → 1079`), demonstrating the repo's
      own symbol-names-over-line-numbers lesson. The rot history is recorded in the comment so
      nobody "helpfully" re-adds them. Sweep of the file's other citations:
      `runtime/processdriver_action.go:449-461` is **currently accurate** (left alone, out of
      scope, will rot the same way); the benchmark reference resolves; everything else is
      symbols or ADR numbers
- [x] Evidence file corrected, original text preserved (3 blocks: header, §Q4(c), pre-existing
      bugs item 1)
- [x] Mutation table recorded (18 rows, Task 7); the two **non-discriminating** results — the
      tiers-1–3 guard and the hoisted lookups — recorded as such rather than omitted
- [ ] `engine` ≥ 91.9 %, repo ≥ 73.9 %; lint + vet clean
- [ ] All three ADRs re-read against the built code; divergences amended in-bundle
- [ ] One commit bundling code + tests + spec + three ADRs + this plan; subject corrected

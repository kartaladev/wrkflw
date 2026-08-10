# Plan — signal arm fan-out and the event-sub-process status guard

ADR-0158 · ADR-0172

## ▶ Progress

| | |
|---|---|
| Branch | `feat/signal-fanout-and-esp-status` (off `main` @ `571a380`) |
| Status | **AUDITED and adjudicated — ready to implement.** Rule-#9 audit run (3 Opus lenses, all executing): **3 Critical, ~10 High, ~20 Medium/Low**. Findings folded in; four owner decisions taken |
| Audit | fan-out lens, ESP-predicate lens, cross-document lens — each in its own worktree. Adjudication record: the `▶ Audit outcome` section below |
| Phases landed | **1–4 LANDED. DELIVERY GATE COMPLETE (4/4).** 8 RED→GREEN cycles, **10 mutations** (9 caught, 1 recorded non-catching + re-run). Repo-wide `go test -race ./...`: EXIT=0, 64 pkgs, **0 races**, coverage **74.0 %** (floor 73.9 %); `engine` **92.5 %**. lint/vet/gofmt clean. `/code-review`: **3 findings, all fixed**. `/security-review`: **0 findings**. ⛔ Awaiting owner go-ahead to merge `--no-ff` + push |
| Spec | `docs/specs/2026-08-10-signal-fanout-and-esp-status.md` |
| Evidence | `docs/specs/2026-08-10-signal-fanout-premise-evidence.md` |
| ADRs | `0158-signal-fires-every-matching-arm.md`, `0172-an-event-subprocess-arm-checks-instance-status.md` |
| Owner decisions | **definition-scan order, no sort**; all three tiers fan out; dying-instance predicate at the **shared dispatch guard**; `walkReverse` excluded; ESP hole bundled in |

> **Commit discipline.** One feature-bundle commit: implementation + tests +
> spec + evidence + both ADRs + this plan. Review-driven fixes fold with
> `git commit --amend`, never stacked. The commit stays local until the Delivery
> Gate passes.

---

## ▶ Audit outcome (rule #9) — what changed, and the owner decisions

Three Opus auditors, each in its own `git worktree`, each briefed to **execute**
rather than read. Between them they re-executed most of the bundle's load-bearing
measurements; the core premise work held, and **the failures were concentrated in
recap sentences, quantifiers, and decisions generalised from a single fixture.**

### The three Criticals

1. **Tier-3 ordering rested on a refuted claim.** *"For two ESP arms sharing an
   enclosing scope it is impossible for both to take effect in any order"* — false.
   The supporting fixture's non-interrupting body **parked**; with a body that
   completes synchronously, both arms take effect.
2. **`PendingCancel` does not imply the walk terminates.** `consumePendingCancel`
   is not set on `walkPartial`, so such a walk **resumes** with the flag set — and
   ADR-0172's first predicate silenced an arm on that live instance,
   **reintroducing the defect it exists to fix**.
3. **Unsilencing the root arm during a full REVERSE walks into unready
   machinery** — `ResetVars` + `rearmRootESP` give two concurrent tokens, wiped
   variables, and an interrupting one-shot arm resurrected while its body runs.

### Owner decisions

| # | decision |
|---|---|
| 1 | **Definition-scan order, no sort.** The `NonInterrupting` flag is not a sufficient statistic for ordering — destroyability depends on the arm's **body** — so no flag-based sort is correct in general |
| 2 | **The dying-instance predicate goes in the shared dispatch guard**, covering all arm families, not only the ESP fire site (the ADR-0165/0169 structural pattern) |
| 3 | **`walkReverse` is excluded from the "fires" set**, deliberately, with the one-swallowed-delivery cost documented |
| 4 | **Keep the bundle together** — ADR-0158 and ADR-0172 ship as one delivery |

### Findings NOT accepted as stated

- The cross-document auditor's `step_nodes.go` correction was **understated**:
  the block carries **two** false coverage claims, not one. Widened.
- Its "M5 `walkThrowTargeted` missing" is a **documentation** fix only — the
  predicate was unaffected, since `walkThrowTargeted` is a resume either way.

### What the audit confirmed (re-executed, no action)

The E8 uncovered-predicate result (eleven packages `EXIT=0`), the shape-(A)
refutation byte-for-byte, the three-wrapper deletion probe, D1/D2's figures, the
`walkAdmin ⇔ terminates` structure, arm-slice order surviving a persist/reload
cycle, and **48 of 53 citations**. Three citations were wrong and are fixed;
⚠ one of them (`step_gateways.go:271` → `:266`) had been **inherited from a
premise agent and restated without re-checking** — the failure mode CLAUDE.md
names, committed inside the bundle that cites it.

---

## Source-verified facts

Re-derived by EXECUTION against `main` @ `571a380`; see the evidence file for
verbatim output. **Symbols first, line numbers second** — the parked bundle's
citations rotted three times.

- `handleSignalReceived` — `engine/step_triggers.go:741`. `snapshotIDs` `:770`;
  `markMatched` `:780` (merge `:782`); `tiers` slice `:804` (lookups 807/816/825,
  fires 812/821/830); **tier terminal loop `:843-852`** (guard `:844`); tier-4
  loop `:861-885` (guard `:862`).
- The three singular wrappers — `armedEventBySignal` (`state_arms.go:308`),
  `boundaryArmBySignal` (`:341`), `eventTriggeredSubprocessArmBySignal` (`:368`)
  — have **exactly one call site each**, all in `handleSignalReceived`, and
  **zero test call sites**. Deleting all three is safe; proved by deletion
  (`go build ./...` and `go vet ./...` both `EXIT=1`, exactly three errors).
  ⚠ **AMENDED DURING IMPLEMENTATION:** the generic `armBySignal` was to be kept
  because `engine/state_arms_test.go:67` calls it. With the three wrappers gone
  that became its ONLY caller — production-dead code kept alive by a test of
  itself — so it and `TestArmBySignal` are deleted. `armIDsBySignal` replaces it
  and carries the same ADR-0152 guard. **Keep `armByTimer` and `armByMessage`**:
  both still have production callers via `dispatchArmCascade`.
- `removeArmsWhere` `state_arms.go:262` — reallocates; detachment is
  bidirectional.
- `resolveGatewayWin` `step_gateways.go:214`, removes all gateway arms at `:266`,
  does **not** consume the gateway token.
- `fireBoundaryArm` `step_boundaries.go:95`; its reachable error is
  `outgoing flow %q not found` at `:124`.
- `fireEventTriggeredSubprocessArm` `step_eventsubprocess.go:156`. The guard is
  `:159-170`, and it splits: `:159-164` is the **non-root scope-liveness** check,
  `:165-170` is the **root status** check. `cancelScopeSubtree` call `:207`.
- `cancelScopeSubtree` `step_cancel.go:81`; retires arms `:101` (named scope) and
  `:113` (every descendant). Exactly two non-test callers:
  `step_eventsubprocess.go:207` and `step_errors.go:468`.
- `cloneState` `step_state.go:361`, called at `step.go:84`. On a tier error `Step`
  returns a **zero** `StepResult` and the caller's state is byte-identical.
- `StepMode` is package-level: `engine.Macro` / `engine.Micro` (`step.go:12-26`).
- `TestNonInterruptingBoundarySignalNoSelfCascade` `step_events_test.go:1157`,
  fixture `:1130-1149` — **unchanged**, and mutation-verified as able to fail.
- ⚠ `engine/` mixes `package engine` and `package engine_test`. **`head -1` any
  existing test file before writing into it.**

---

## Conventions for every phase

- **TDD strict.** The test is written and run **RED in its own `Bash` call**
  before the implementation exists. A compile error from a missing *production*
  symbol is a valid red; a missing *test helper* is not — add the helper, re-run,
  get the assertion failure. Each phase is one RED/GREEN cycle for one coherent
  symbol group; **do not batch groups**.
- **Every prescribed test below states its falsifier — either what makes it fail
  today, or that it is a REGRESSION GUARD that passes today.** ⚠ The first draft of
  this plan claimed the universal ("states what makes it fail today") and the audit
  refuted it: roughly a third of the rows pass on `main`. A regression guard is
  legitimate; a row *mislabelled* as RED-today is how a vacuous test ships. This
  repo shipped six such tests in one delivery.
- Black-box `package engine_test` except where the symbol is unexported
  (Phase 1), which is `package engine`.
- Table tests per the `table-test` skill: `assert` closure per case, optional
  `ctx` modifier, `t.Context()`.
- **Acceptance tests run in `Macro`.** In `Micro`, `len(Commands)` is not a proxy
  for "arms fired" (evidence D9).
- ⚠ **No sorting.** Arms are emitted in slice (definition-scan) order; neither
  `slices.SortStableFunc` nor `sort.SliceStable` belongs in this delivery. If you
  reach for a sort, re-read ADR-0158 Decision 2 — a sort was specified once, and
  refuted by execution.
- **Single package ⇒ implemented INLINE in the controller**, not fanned out —
  CLAUDE.md rule #11: concurrent agents in one package break each other's
  `go test` compile even on disjoint files.
- ⚠ **Do not use Docker.** Container-free: `./engine/...`, `./processtest/...`.
  `./runtime/...` as a whole is **not**. `go vet ./...` compiles the Docker-only
  test packages without running them — use it to prove a breaking change has no
  hidden consumer.
- Judge every run by **exit code**: `go test ./engine/... > out.log 2>&1; echo "EXIT=$?"`.

---

## Phase 1 — identity types, `…IDsBySignal`, `…ByID`

**Files:** `engine/state_arms.go`, `engine/state_arms_test.go` (`package engine`).

### 1a — identity types and `…IDsBySignal`

| # | case | assertion | **what makes it fail today** |
|---|---|---|---|
| 1 | two boundary arms share a signal name | both identities returned | `boundaryArmIDsBySignal` does not exist — compile error |
| 2 | gateway: two arms, one name | both identities, **slice order** | as above |
| 3 | boundary: non-interrupting declared SECOND | identities in **declaration order** — the NI one stays second | **REGRESSION GUARD.** Pins "no sort" against a re-introduced `NonInterrupting` sort in either direction |
| 4 | event-sub: non-interrupting declared FIRST | identities in **declaration order** — the NI one stays first | as row 3, opposite declaration order, so a sort in *either* direction breaks one of the two |
| 5 | two arms with an **identical identity tuple**, differing in `NonInterrupting`/`Action` | exactly ONE identity returned, and it is the **FIRST in slice order** | without de-dup, two are returned; with an unspecified tie-break, the wrong arm can win — measured, colliding arms differ in fields that decide whether the host is interrupted |
| 6 | empty name | `nil` | **DEFENCE IN DEPTH.** `validateTriggerKey` already rejects an empty name at `Step` entry, so this cannot be reached through the public API — label it, do not claim it is RED today |
| 7 | timer / message / error-boundary arms present | not returned | a lookup keyed on the wrong `triggerMatch` field returns them |

⚠ Rows 3 and 4 replace the previous "deliberately opposite sorts". **ADR-0158 no
longer sorts**, so these rows exist to keep it that way: together they fail under a
sort in *either* direction, which is what a future well-meaning "fix" would add.

### 1b — `…ByID` re-resolvers

| # | case | assertion | **what makes it fail today** |
|---|---|---|---|
| 1 | re-resolve after an earlier arm was removed | the correct arm is returned | `boundaryArmByID` does not exist — compile error |
| 2 | re-resolve a removed arm | `nil`, no panic | an index-based re-resolver panics or returns the wrong arm |
| 3 | **root event-sub arm, `EnclosingScopeID == ""`** | **non-nil** | an ADR-0152-style empty-key guard returns nil — the trap that would disable every top-level event sub-process |
| 4 | gateway / boundary re-resolve with an empty owner key | `nil` | those two families DO guard the empty key; omitting it returns a spurious arm |

Rows 3 and 4 are also deliberately opposite, mirroring the real asymmetry in
`state_arms.go`.

**GREEN:** `gatewayArmID`, `boundaryArmID`, `eventSubArmID`; three `…IDsBySignal`
methods; three `…ByID` methods. `eventTriggeredSubprocessArmByID` must **not**
guard an empty `EnclosingScopeID`.

---

## Phase 2 — tiers 1–3 fire every matching arm (ADR-0158)

**Files:** `engine/step_triggers.go`, new
`engine/step_signal_fanout_test.go`. Also update: `handleSignalReceived`'s godoc
and its in-function dispatch-order comment block, ADR-0169's `⚠ no-hoist` comment
(which explicitly names this delivery), and `engine/README.md:166` — it describes
the trigger as "resumes all tokens awaiting it", which is tier 4 only.
**Delete the three now-unused singular wrappers** or `unused` fails the gate.

| # | case | assertion | **what makes it fail today** |
|---|---|---|---|
| 1 | two interrupting signal boundaries on two parallel hosts | **both** hosts consumed, both boundary targets hold a token, both tasks `cancelled` | today exactly one fires (evidence D1: tokens 2→1, `Boundaries` 2→1, one `UpdateTask`) |
| 2 | one non-interrupting signal boundary | fires exactly once **and stays armed** | regression guard for ADR-0124 |
| 2b | two non-interrupting boundaries whose targets are catch nodes on the **same** name | two new tokens, both `TokenWaiting` with `AwaitSignal == name`; neither consumed by this delivery | ⚠ **REGRESSION GUARD, not RED today.** The stated falsifier was wrong: tier 4's `snapshotIDs` is taken before tiers 1–3 and is unchanged by this delivery, so the spawned tokens were never consumable. The mutation that *would* break it (re-scan instead of snapshot) **hangs** rather than asserting — see Phase 4.1 |
| 3 | two event-based gateways (parallel fork), same signal | **both** gateway tokens route to their branch targets | today only the first resolves |
| 3b | two arms with the same signal on **one** gateway | exactly one fires | without re-resolution the second fires too, breaking first-event-wins |
| 3c | gateway branch loops back and re-arms the same identity mid-delivery (**merge-gateway shape** — the naive loop is `Validate`-rejected) **AND the gateway carries a SECOND same-signal arm** | the gateway resolves **once** | ⚠ **With one matching arm this row is VACUOUS** — measured `DOUBLE-FIRE REACHABLE WITHOUT resolvedGateways: false`, because the snapshot holds a single identity and the re-arm is never revisited. The second same-signal arm on the same gateway token is what makes the ABA observable (`true`). Without it, Phase 4.1's `resolvedGateways` mutation is a **non-catching** mutation |
| 4 | two event-sub arms in **sibling** scopes | both child scopes opened | today only the first fires |
| 4b | root-scope interrupting + non-interrupting event-sub arms, **non-interrupting declared first**, NI body **parks** | **BOTH fire in declaration order**: the NI arm opens its scope, then the interrupting fire tears the subtree down. Assert the observable end state and the `History` visits, not a token count | ⚠ **This is the END-TO-END re-verification of the ordering decision.** CTL-3/CTL-4 were white-box direct calls; this row drives `Step`. It must be written and its result compared against CTL-3 — if they disagree, the ADR's §2a is wrong, not the test |
| 4d | same two arms, but the NI body **completes synchronously** (`ni-start → ni-send(SendTask) → ni-end`) | **both take effect**: the `SendMessage` is emitted AND the interrupting arm's scope opens | the audit's Critical — under a would-be interrupting-first sort the NI arm never fires at all. Pins that no sort was re-introduced |
| 4c | **root-scope** event-sub arm (`EnclosingScopeID == ""`) | fires | guards the Phase-1b empty-key trap end-to-end |
| 5 | non-interrupting event-sub + gateway + boundary + parked token, one name | all four effects in ONE `StepResult`, **in tier order** | cross-family regression guard (evidence D6 is the exact baseline) |
| 6 | **two INTERRUPTING** boundary arms on one host | exactly one token; `Boundaries` empty; exactly one non-FaF `InvokeAction` | without the nil check after re-resolution the retired sibling fires. ⚠ **Both arms MUST be interrupting** — the previous wording omitted that and contradicted row 7, inviting an implementer to "fix" the contradiction by weakening the design |
| 7 | two boundary arms on one host, **interrupting declared FIRST, non-interrupting second** | **one** token, at the interrupting target; host gone; the NI arm re-resolves to nil | declaration order decides this (ADR-0158 Decision 2). Row 6 and row 7 differ **only** in the arms' interrupting flags and declaration order — that is deliberate and must stay legible |
| 7b | the same two arms with the **non-interrupting declared FIRST** | **two** tokens — the NI target and the interrupting target | the pair 7/7b is the executable statement that ordering is author-controlled |
| 8 | delivery matches nothing | `Variables` unchanged, `Commands` empty | evidence D7a is the baseline |
| ~~8b~~ | ~~delivery fires several arms → payload merged exactly once~~ | **ROW DELETED** | ⚠ **Unwritable.** `mergeVars` is a `maps.Copy` of a fixed payload, and the only non-test writers of `s.Variables` are unreachable from a signal drive — the mutation was run and the whole container-free suite stayed `EXIT=0`. The property is **unobservable by construction**; a row asserting it would be a test that cannot fail. The `markMatched` latch is instead covered by row 8 (no-match ⇒ no merge) |
| **9** | **`MessageReceived` with two matching boundary arms** | still fires only the **first** | the fan-out must not touch `dispatchArmCascade`. ⚠ D5b's end state is byte-identical to D1's — this row must key on the **trigger type**, not the end state |
| 9b | `TimerFired` on a single-arm fixture | command output unchanged | as above |
| 10 | arm 1's drive terminates the instance, arm 2 would fire | arm 2 does **not** fire | inherited from ADR-0169; guards against the fan-out accidentally bypassing the `tiers` loop |
| 11 | **an arm created BY this delivery's own drive** (tier 1 → tier 2, evidence CTL-1) | the new boundary does **not** fire; the human task stays `unclaimed`; instance stays `running` | today it fires: the task is minted and cancelled in one step |
| 11b | same, tier 2 → tier 3 (evidence CTL-2) | the ESP arm does **not** fire; the sub-process token survives | today the sub-process is entered and torn down in one step |
| 12 | tier 2 errors mid-fan-out (**`outgoing flow not found`** — arm with def D, deliver with D′) | `Step` returns non-nil error **and** a zero `StepResult`; caller state byte-identical | ⚠ do **not** use "flow targets a missing node" — measured, it parks, it does not error (evidence D8-0) |
| 13 | **public harness**: `PublishSignal` alone on the D1 shape | `DriveToCompletion` returns `nil` **and** the spy catalog's `notify` count is **2** | today the drive errors `unhandled park: human-task at node "taskB"` with count 1 (evidence D2a) — RED today, GREEN after |

⚠ Row 13 must assert on `st.SignalWaiters()` where it inspects waiters, **not**
`Park.AwaitingSignals`, which collapses multiplicity (evidence D2-x).

⚠⚠ **Not every row here is RED today, and the Conventions section previously
claimed otherwise.** Roughly a third are **regression guards** — they pass on
`main` and exist to stop the change breaking something. Each row's falsifier
column now says which it is. A regression guard is legitimate; a row *mislabelled*
as RED-today is how a vacuous test gets shipped, because nobody re-checks it.
**Before implementing, run each RED-today row against unpatched `main` and confirm
it fails for the stated reason.**

**GREEN shape:** build `tiers` from the three snapshots — one closure per
identity, **in declaration order, no sort** — each re-resolving by identity and
returning `(nil, nil)` when the arm is gone; the gateway closures additionally skip
an identity whose `GatewayToken` is already in `resolvedGateways` and record it
after a successful fire. The tier-4 loop is **unchanged**. The tier loop's guard
becomes `spawnsNewWork()` (ADR-0172, Phase 3) rather than `IsTerminal()`.
**Do NOT touch** `handleTimerFired`'s or `handleMessageReceived`'s arm
*selection* — Phase 3 does add the eligibility guard inside `dispatchArmCascade`,
which is a different thing and must not be confused with first-match selection.

---

## Phase 3 — the event-sub-process status guard (ADR-0172)

**Files:** `engine/state_compensation.go` (`walkTerminates`), `engine/state.go`
(`spawnsNewWork`), `engine/step_eventsubprocess.go`, `engine/step_arm_dispatch.go`
(the cascade guard), `engine/step_triggers.go` (the tier loop's guard), new
`engine/step_eventsubprocess_dying_instance_test.go`.

**Document corrections owed by this phase** (all measured false; a phase that
does not edit them leaves the bundle asserting a fix it did not make):

- `engine/step_nodes.go:483-491` — **two** false coverage claims, not one.
- `docs/adr/0124-repeatable-noninterrupting.md:62-63` — the false parenthetical
  that `fireEventTriggeredSubprocessArm` "is status-guarded to no-op on a
  non-`Running` instance". ⚠ Leave its neighbouring sentence alone: that
  `isTerminal` excludes `Compensating` is **true**, and is half of why the defect
  is reachable.
- `docs/adr/0124-repeatable-noninterrupting.md:55-57` — Decision item **3**, which
  ADR-0158 declares it amends. ⚠ The first draft of this plan declared the
  amendment but scheduled no edit for it.

⚠ **Start from the coverage fact, not from the code.** Measured (evidence E8):
deleting `s.Status != StatusRunning` leaves `./engine/...` **and every
container-free package** at `EXIT=0`. Nothing existing will catch a regression or
confirm the change. Every row below is therefore load-bearing, and every one is
mutation-verified in Phase 4.

| # | case | assertion | **what makes it fail today** |
|---|---|---|---|
| 1 | **non-root** arm delivered during a `walkAdmin` rollback (cancel) | **no `InvokeAction` emitted** and **no new scope** (scopes stay 1, not 2) | ⚠ **Do NOT assert "zero orphan tokens"** — measured `tokens=0` on `main` *and* under the fix, because the re-entrant `beginCompensation` sweeps the token. That assertion **cannot fail**. Today the fire emits `cmds=[{e2d-c2 nested-esp-action}]` and opens a scope (evidence E1c) |
| 1b | same for the **unhandled-error** rollback (`W1`) and the **admin full rollback** (`W3`) | as row 1 | ⚠ `W3` carries `FinalStatus=running` — the row that fails any `FinalStatus`-keyed predicate (evidence E6) |
| 2 | **root** arm during a **local compensation throw** (`walkThrowScopeWide`) | the arm **FIRES** | today silenced by `!= StatusRunning`, delivery consumed anyway (evidence E2) |
| 2b | same for **`walkPartial`** and **`walkThrowTargeted`** | the arm fires | ⚠ **`walkReverse` is NOT in this row** — it is deliberately excluded (ADR-0172 Decision 1a) |
| **2c** | **`walkReverse`** | the arm does **NOT** fire | new: the reverse exclusion. Without it, `ResetVars` + `rearmRootESP` give two concurrent tokens, wiped variables and a resurrected one-shot arm |
| **3** | **`walkPartial` cursor with `PendingCancel=true`** (a `CompensateRequested` carrying **both** `ToNode` and `ReverseNode`, then a mid-walk `CancelRequested`) | the arm **FIRES**, and the walk **resumes** | ⚠ **This row replaces the old row 3, whose premise was refuted.** `consumePendingCancel` is not set on `walkPartial`, so such a walk resumes; a `walkMode()==walkAdmin \|\| PendingCancel` predicate silences the arm on a live instance. **Nothing in the repo reaches this shape** — an invariant probe over four packages is `EXIT=0` |
| 3b | `walkThrowScopeWide` cursor with `PendingCancel=true` | the arm does **NOT** fire | the disjunct's *positive* case. ⚠ The old row 3 claimed to be the "sole" falsifier for `PendingCancel`; it is not — this row and row 3 together bound it from both sides |
| 4 | **non-root** arm whose enclosing scope is gone | no-op | guards against the rewrite dropping the scope-liveness check |
| 5 | control: **nested interrupting ESP arm during an in-flight scope-wide throw** | still fires (a human task for `espTask` appears) | `TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope`, which **shape (A) breaks**. Assert it still passes; do not rewrite it |
| 6 | control: arm delivered while `StatusRunning` | fires, unchanged | **REGRESSION GUARD**, not RED today |
| 7 | **NON-ROOT** timer-triggered ESP arm during a `walkAdmin` rollback | does not fire | ⚠ **Must be NON-root.** A *root* timer arm is silenced on `main` **and** under the fix (`cmds=[]` both ways) — that version cannot discriminate. Non-root discriminates: `main` gives `tokens 0→1 scopes 1→2` and a terminated instance with an orphan token |
| 7b | the same arm's scheduled timer | its `CancelTimer` is still emitted by `endInstance` | measured: **no scheduled-job leak** from the new no-op |
| **8** | **message**-triggered ESP arm during a `walkAdmin` rollback | does not fire **AND the message is NOT consumed** — `Variables` unchanged, and a later matching arm still sees it | the guard must sit **ahead of** `dispatchArmCascade`'s `onMatch`, which merges the payload and sets `matched` before the fire. A fire-site-only guard silently swallows the message |
| **9** | out-of-range `Status` (e.g. `engine.Status(9)`) with an armed root arm | does **NOT** fire | measured: `main` silences it; a **deny-list** predicate fires it. Pins the allow-list shape |
| **10** | guard placement: a tier-2 arm whose drive begins a terminating rollback, with a tier-3 arm on the same signal | the tier-3 arm does **not** fire | the dispatch-guard half. `IsTerminal()` is false for `Compensating`, so the old guard let it through — and ADR-0158 multiplies this window |

**GREEN:** `walkTerminates(pendingCancel bool)` on the cursor (mirroring
`stepCompensationFinish`'s plan construction), `spawnsNewWork()` on
`InstanceState` as an **allow-list**, applied at `fireEventTriggeredSubprocessArm`
(retaining the non-root scope-liveness check) **and** at the shared dispatch guard
(`handleSignalReceived`'s tier loop and `dispatchArmCascade`).

⚠ **Do not adopt the `&& s.Compensating.ActiveCmdID != ""` variant** — but **not**
for the reason this plan previously gave. That reason ("`ActiveCmdID` is transiently
empty between records") was **measured false**. The real reason: the empty-cursor
state is not engine-reachable at all, so the conjunct buys nothing, and for a
legacy row the conservative direction is to treat it as dying.

---

## Phase 4 — mutation-verify, then the Delivery Gate

### 4.1 Mutation duty

Snapshot the file, break the guard on purpose, confirm the test **fails**,
restore, `diff` to prove byte-identical. Required for at least:

| guard | mutation | must go RED in |
|---|---|---|
| snapshot vs re-scan | replace the boundary snapshot with a re-scan | ⚠ **This mutation HANGS, it does not assert** — the re-scan finds a non-interrupting arm forever (spec §2.1 option A). Run it under `go test -timeout 30s` and record the **timeout** as the RED signal, explicitly, or it will be logged as a non-catching mutation |
| ABA guard | delete `resolvedGateways` | P2 case 3c — ⚠ **only with 3c's SECOND same-signal arm on the same gateway.** With one arm, measured non-catching |
| **no sort re-introduced** | add a `NonInterrupting` sort, **each direction separately** | P1a rows 3 **and** 4 (one row catches each direction), plus P2 4b/4d and 7/7b |
| root empty-key | add an `EnclosingScopeID == ""` guard to the re-resolver | P2 case 4c, P1b row 3 |
| nil check after re-resolve | drop it | P2 case 6 |
| identity de-dup | remove it | P1a row 5 |
| de-dup tie-break | keep the LAST colliding identity instead of the first | P1a row 5 — measured: colliding arms differ in `NonInterrupting`/`Action`, so this changes whether the host is interrupted |
| timer/message selection untouched | route signal through `dispatchArmCascade` | P2 cases 9, 9b |
| ESP dying-instance predicate | delete the whole predicate | P3 cases 1, 1b — ⚠ measured: on `main` this mutation is caught by **nothing**, across eleven container-free packages |
| ESP `walkTerminates` vs `walkAdmin\|\|PendingCancel` | restore the refuted disjunct | **P3 case 3** — the Critical. If case 3 does not go RED here, it is not exercising the `walkPartial`+`PendingCancel` shape |
| ESP `walkReverse` exclusion | remove `walkReverse` from the terminating set | P3 case 2c |
| ESP allow-list | rewrite as a deny-list (`if terminal-or-dying { return }`) | P3 case 9 |
| ESP root/non-root symmetry | restore `!= StatusRunning` on the root branch only | P3 case 2 |
| ESP scope-liveness retained | delete the `scopeByID` check | P3 case 4 |
| dispatch-guard placement | put `spawnsNewWork()` **only** in the fire function, not in the cascade/tier loop | P3 cases 8 and 10 — the message-consumption and cross-tier halves |

⚠ **Record non-catching mutations AS SUCH.** Two above are already known to need
care (the hang, and the ABA fixture). A mutation that fails to compile is not a
RED; one that cannot discriminate is not verification.

⚠ A mutation that **fails to compile** is not a RED. A mutation that **cannot
discriminate** is not verification — check that the chosen test is the one the
mutation actually breaks, and record non-catching mutations **as such**, not
hidden.

### 4.1a Mutation evidence — AS RUN

Each guard broken on purpose, RED observed, restored from a snapshot, restore
`cmp`-verified byte-identical. **8 mutations; 7 caught; 1 recorded as
non-catching and re-run.**

| # | mutation | result |
|---|---|---|
| M1 | reverse the emitted identity order (stands in for ANY re-ordering, incl. a re-introduced `NonInterrupting` sort) | ⚠ **FIRST ATTEMPT NON-CATCHING (`EXIT=0`) — and it measured nothing.** The anchor string did not match, so the edit never applied. Re-run with a matching anchor: **`EXIT=1`**, caught by `…IDsBySignal`'s declaration-order rows. *Recorded because a mutation that silently fails to apply looks exactly like a guard that works.* |
| M2 | delete the identity de-dup | `EXIT=1` |
| M3 | add an `EnclosingScopeID == ""` guard to the ESP re-resolver (**the trap**) | `EXIT=1` |
| M4 | restore the REFUTED `walkMode() == walkAdmin \|\| pendingCancel` | `EXIT=1` — the Critical |
| M5 | drop `walkReverse` from the terminating set | `EXIT=1` |
| M6 | rewrite `spawnsNewWork` as a deny-list (out-of-range fires) | `EXIT=1` |
| M7 | delete the fire-site guard entirely | `EXIT=1` |
| M8 | delete the `resolvedGateways` ABA guard | `EXIT=1` — caught by the merge-gateway loop fixture |

⚠ **Not yet mutated, and owed before the gate closes:** the `markMatched` reuse
(row 8b was deleted as unwritable — the property is unobservable by
construction), and the dispatch-guard PLACEMENT rows (P3 8 and 10), whose
fixtures are not yet built.

### 4.1b `/code-review` findings — AS ADJUDICATED

Run on `390be6d`. **3 findings; all 3 accepted and fixed.**

| # | severity | finding | resolution |
|---|---|---|---|
| 1 | **HIGH** | ADR-0172 Decision 4 claims the rule "applies to every trigger kind", but only the SIGNAL token loop was guarded. `handleTimerFired`'s intermediate-timer resume and `handleMessageReceived`'s parked-message resume drove a token and dispatched a live `InvokeAction` on a dying instance. **A source comment in `dispatchArmCascade` asserted the callers covered this. They did not.** | **ACCEPTED.** Reproduced RED as `TestDyingInstanceSpawnsNoWork{OnAnyTriggerKind,OnTimerResume}`, guard added to both fall-throughs **ahead of `mergeVars`** so the delivery is not consumed, false comment corrected, ADR-0172's "two sites" corrected to three. Mutations **M9** (both guards) and **M10** (message only) → `EXIT=1`, restores `cmp`-verified |
| 2 | MEDIUM | `state_dying_test.go`'s "scope-TARGETED throw" row set `ResumeScope`, but `walkMode()` discriminates on **`ArchiveKey`** — the row silently duplicated the scope-wide case and never exercised the fifth mode | **ACCEPTED.** Fixture corrected to `ArchiveKey: "k"`; the assertion message now names the discriminator so the trap is not re-set |
| 3 | LOW | Both ADRs shipped as `Status: Proposed` | **ACCEPTED.** Both set to `Accepted` |

⚠ **Finding 1 is the fifth consecutive delivery where the real gate found
something the design audit did not.** This bundle's rule-#9 audit ran three
executing Opus lenses and found 3 Criticals; it still missed a false comment
asserting coverage that did not exist, because the claim was about *callers of
the changed function* rather than the changed function itself.

### 4.1c `/security-review` — AS RUN

**ZERO findings at or above the reporting bar.** Identification pass examined the
production diff, the surrounding engine code, and the paths the new predicates
gate; no candidate reached 0.7 confidence, so no false-positive filtering pass
was needed.

Angles specifically checked and cleared, with the reason each is negative:

| angle | why negative |
|---|---|
| Authorization bypass | one `humantask.HumanTask` construction site (`step_nodes.go:731`) unconditionally populates `Eligibility`; every extra arm routes through the same node strategies, so a newly-reachable user task carries its own `AuthzSpec`. No `authz/`, `humantask/`, `service/` or `transport/` file touched |
| Compensation double-run | a nested throw mid-walk is **deferred, not clobbered** (`step_nodes.go:1160`, `:1183`); `archiveCompensations` nils `scope.Compensations` (`state_compensation.go:316`) so multiple interrupting arms cannot double-archive |
| Cross-instance effects | `handleSignalReceived` operates on ONE `*InstanceState`; nothing enumerates instances. The broadcast stays intra-instance |
| Identity-snapshot substitution | all three removers key on the owner field that is **part of the identity**, so colliding arms are always removed together and `armByID` returns nil rather than a substitute. Gateway ABA is guarded; boundary identity embeds a never-reused token id; the one reusable ESP identity is re-armed only at compensation *finish*, unreachable from inside a tier's `drive` |
| State confusion | `spawnsNewWork`'s allow-list **fails closed** on an unknown status — strictly safer than the `IsTerminal()` deny-list it replaces |
| Data exposure | `mergeVars` semantics unchanged; the new guards can only PREVENT a merge |

⚠ **Net direction of ADR-0172 on the compensation window is NARROWING, not
widening.** On `main` the tier loop gated on `IsTerminal()` — false for
`StatusCompensating` — so gateway and boundary arms already fired during
terminating rollbacks, and the timer/message token fall-throughs had no gate at
all. The only widening is a root ESP arm during a *resuming* walk.

⚠ **Pre-existing, NOT attributed to this delivery:** signal delivery has no
authorization check at the service/transport layer (`service/service.go:354` →
`httpcore.DeliverSignal`). Already tracked in `AUDIT.md`. Neither introduced nor
materially widened here — the same end state was previously reachable by
delivering the signal N times.

### 4.2 Verification checklist

- [ ] `go build ./... && go vet ./...` clean (vet also compiles Docker-only test packages)
- [ ] `go test -race -count=1 ./engine/... ./processtest/...` — EXIT=0 (container-free)
- [ ] ⚠ **ASK THE OWNER before the repo-wide run.** `go test -race -coverprofile=cover.out ./...` spins testcontainers and therefore needs Docker, which the Conventions above forbid without per-run approval. It is a **gate** step, not a per-phase one — request approval once, at the gate. Target: `engine` ≥ 85 % (baseline **92.4 %**; do not regress it), repo ≥ 73.9 %
- [ ] `golangci-lint run ./...` — 0 issues, **including `unused` after deleting the three wrappers**
- [ ] every new symbol has an **observable RED state in the transcript**
- [ ] mutation evidence recorded in this Progress block, non-catching mutations included
- [ ] documents describe what SHIPPED — re-read spec + both ADRs against the built code and amend every divergence **in this bundle** (rule #11)
- [ ] sweep the diff's own comments for unexecuted claims and over-reaching quantifiers
- [ ] `docs/plans/HANDOVER.md` rewritten **in place**; auto-memory updated to point at it
- [ ] adversarial Opus stand-in reviews run first, to cut rework
- [ ] **owner** runs `/code-review` and `/security-review`; all findings fixed and folded with `--amend`. ⚠ A review that errored out is an **absent** review, not a clean one
- [ ] re-run the full suite on the **merged** tree, then push

---

## Backlog opened by this delivery's premise work

Each was executed; each is deliberately out of scope.

1. **A flow targeting a non-existent node parks a permanent wedge** (evidence
   D8-0): `WARN token routed to a missing node`, then a `TokenWaiting` token with
   `AwaitCommand == ""` that nothing can resume, instance `running` forever.
2. **Micro mode loses a signal delivery** (evidence D9): `snapshotIDs` is taken
   over tokens Micro has not parked yet, so an intermediate signal catch sitting
   `TokenActive` with `AwaitSignal == ""` is silently missed while the signal is
   still consumed and the catch is **not** re-armed. Pre-existing.
3. **`processtest.Classify` collapses signal multiplicity** (evidence D2-x):
   `Park.AwaitingSignals` returns `[escalate]` where `st.SignalWaiters()` returns
   `[escalate escalate]`. Same class as pre-v0.1.0 blocker 9.
4. **`PublishSignal`'s `armFireLog` bound refuses a second delivery to the same
   parked node set** (evidence D2a). ADR-0158 removes the *need* for a second
   delivery in the fan-out case, but the bound itself is untouched.
5. **The error/terminal contract disagreement**: an error discards the delivery
   (zero `StepResult`); a mid-delivery terminal returns partial commands
   (ADR-0169). Fan-out widens the window without resolving it.
6. **`engine/step_nodes.go:501` — `exitNestedEventSubprocessScope`'s arm
   retirement is entirely uncovered** (evidence E4 mutation: replace with
   `_ = parentScopeID` → repo suite green). Left in place deliberately; **not**
   removed on the strength of a green suite, which is exactly the inference this
   repo has been burned by. Needs a test or a decision of its own.
7. **A terminated instance can carry a zombie scope** (evidence E1b(A):
   `endInstance` leaves `scopes=1`). Long-standing; overlaps the still-owed
   zombie-scope ADR from delivery 2b.
8. **`processtest.Classify` has no reason for a compensation-walk park** and
   `Park.HasArmedTimers` reads one source — both pre-existing, both already on the
   repo backlog; this delivery touches neither.
9. **`PendingCancel=true` survives onto a `Running` instance forever.** Measured
   during the audit: after a `walkPartial` walk resumes, the flag is still set, so
   the operator's cancel is silently lost **and will terminate the next throw or
   reverse walk** instead. Pre-existing, independent of this delivery. Own ADR.
10. **`runtime/processdriver_action.go:485`'s comment is false** — it asserts
    `performThrowSignal` excludes the throwing instance; measured, it does not
    (`delivered to: [other thrower]`). ADR-0158 relies on the true behaviour; the
    comment is the wrong one. Doc-only fix, deliberately not folded in here
    because `runtime/` is outside this delivery's tested surface.
11. **`engine/step_nodes.go:501`'s nested arm retirement is entirely uncovered**
    (mutation → repo suite green). Left in place; **not** removed on the strength
    of a green suite, which is the inference this repo has been burned by.
12. **A second counterexample to "resume ⇒ does not terminate"**: `applyFinish`
    terminates a resume plan when the resume is dropped and no tokens remain. An
    arm firing there places a token and **suppresses that recovery completion**.
    `walkTerminates` cannot see it — the outcome depends on token count at finish,
    not on the cursor. Recorded in ADR-0172's Consequences; needs its own ADR.

# Audit — Lens C: re-counting enumerations, quantifiers, inherited citations

Bundle: `docs/specs/2026-08-18-test-wait-budget-and-conformance-completeness.md`,
`docs/adr/0184-conformance-completeness-and-test-wait-budgets.md`,
`docs/plans/2026-08-18-test-wait-budget-and-conformance-completeness.md`

Worktree: `.../scratchpad/audit-c` @ `142a1a66` (bundle commit, detached HEAD). Docker: unavailable — no container-backed runs.

Method: every number, quantifier and citation is treated as guilty until re-derived here by running a command.

---

## VERIFIED-CORRECT (recorded so the next reader does not re-derive them)

### V1 — "40 `Eventually` sites in `scheduler/`" — CORRECT

```
$ grep -rn --include='*_test.go' "Eventually(" scheduler/ | wc -l
40
$ grep -rn --include='*_test.go' "EventuallyWithT" scheduler/ | wc -l
0
$ grep -rn --include='*_test.go' "assert\.Eventually" scheduler/ | wc -l
0
$ grep -rn --include='*_test.go' "require\.Eventually" scheduler/ | wc -l
40
$ grep -rn "Eventually" scheduler/ | wc -l          # no paren
41
```

40 call sites, all `require.Eventually`, zero `assert.Eventually`, zero `EventuallyWithT`, zero
outside `_test.go`. Spec §3.3 / §6.1, ADR §Context and §Decision-3 are right.

### V2 — distribution "30 / 8 / 2" — CORRECT, sums to 40

Re-derived by hand over all 40 sites (26 single-line, 14 multi-line; the multi-line arms read out of
`sed` context):

| budget | single-line | multi-line | total |
|---|---|---|---|
| `time.Second` | 25 | 5 | **30** |
| `2*time.Second` | 1 | 7 | **8** |
| `3*time.Second` | 0 | 2 | **2** |
| | 26 | 14 | **40** |

Multi-line sites and their budgets: `scheduler/timeskew_test.go:109` 2s · `scheduler/start_test.go:48,74`
2s · `scheduler/elector_test.go:141` 3s · `gocron/scheduler_test.go:324` 2s · `gocron/timeskew_test.go:138,179`
2s · `gocron/monitor_test.go:136,154,172` 1s · `gocron/trigger_test.go:64,285` 1s ·
`gocron/bump_regression_test.go:55` 2s · `pgelector/elector_heartbeat_test.go:55` 3s.

### V3 — plan Task 3 Step 3 line-number table — ALL 40 LINES CORRECT

Every `file:line` in the plan's table is a real `require.Eventually(` call at that exact line at
`142a1a66`, every stated current budget matches, and the union of the table is exactly the 40-site
set — no site missing, no extra. Including the 18 `trigger_test.go` lines (40, 61, 64, 88, 109, 130,
171, 212, 283, 285, 308, 324, 341, 359, 377, 395, 413, 448); `trigger_test.go:259` is the *comment*
"same fake-clock-advance + NextRun + Eventually technique" and is correctly excluded.

### V4 — per-package split "6 / 31 / 2 / 1" — CORRECT

`scheduler_test` 6 (timeskew 1, start 2, scheduler_test 1, scheduler_surface 1, elector 1) ·
`gocron_test` 31 (trigger 18, job_schedule 5, monitor 4, timeskew 2, scheduler_test 1, bump 1) ·
`myelector_test` 2 · `pgelector_test` 1. Sum 40.

### V5 — "16 `Never` sites", budgets "(100/150/200/300 ms)" — BOTH CORRECT

```
$ grep -rn --include='*_test.go' "Never(" scheduler/ | wc -l
16
```
Distribution re-derived: 100 ms ×1 (`gocron/scheduler_test.go:195`), 150 ms ×9
(`scheduler_surface_test.go:169,228,256,289`; `gocron/scheduler_test.go:161,220`;
`gocron/job_schedule_test.go:67,87,159`; `gocron/bump_regression_test.go:47`)… **correction: that is
10, see F3** … 200 ms ×4 (`clock_option_test.go:89`, `gocron/clock_option_test.go:87`,
`gocron/scheduler_test.go:141`, `gocron/job_schedule_test.go:127`), 300 ms ×1
(`myelector/mysql_elector_heartbeat_test.go:64`). 1+10+4+1 = 16. The four distinct values named in
spec §2 and ADR §4 are exactly the set present.

### V6 — "4 test packages" — CORRECT for the affected files

`head -1` of all 15 union files: `scheduler_test` (6 files), `gocron_test` (7), `myelector_test` (1),
`pgelector_test` (1). All black-box. The white-box test packages that also exist under `scheduler/`
(`package scheduler` in `export_test.go`, `package gocron` in `scheduler_logger_test.go`,
`package pgelector` in `export_test.go`) hold **zero** `Eventually`/`Never` sites, so a const in the
`_test` package is reachable from every site that needs it. `scheduler/internal/obs` (`obs_test`) has
zero sites. No pre-existing `eventuallyBudget` / `waitbudget*` symbol anywhere in the repo — no
collision.

---

## FINDINGS

### F1 — **MAJOR** — "15 test files" is wrong; there are **13**, and the 2 extra are files the delivery FORBIDS touching

- **Attacked:** plan File Structure table, Half A row: *"15 existing `_test.go` files — modify — 40 timeout literals"*; plan Task 3 **Files** header: *"Modify: the 15 test files enumerated in Step 3"*; spec §6.5: *"All black-box, **15 files**, 4 packages"*.
- **Command:**
  ```
  $ grep -rl --include='*_test.go' "Eventually(" scheduler/ | wc -l
  13
  $ { grep -rl --include='*_test.go' "Eventually(" scheduler/; grep -rl --include='*_test.go' "Never(" scheduler/; } | sort -u | wc -l
  15
  ```
- **Real value:** **13** files contain an `Eventually(` call. The number 15 is the size of the
  `Eventually ∪ Never` file set. The plan's own Step 3 table enumerates exactly **13** rows — so the
  plan contradicts itself by two.
- **Why it bites:** the two files in the difference are `scheduler/clock_option_test.go` and
  `scheduler/internal/gocron/clock_option_test.go`, which contain **only `assert.Never` sites**. An
  implementer handed "modify 15 files" and a 13-row table will hunt for the missing two and land
  precisely on the two files ADR-0184 §4 and the plan's own Global Constraint say must **never** be
  changed. This is the inherited-restatement failure mode: spec §6.5 counted one thing (files
  touching either helper, which is the right denominator for "which packages need the const"), the
  plan restated it as a different thing (files to modify).
- **Corrected text:**
  - plan File Structure: `| 13 existing _test.go files | — | **modify** — 40 timeout literals |`
  - plan Task 3 Files: `Modify: the 13 test files enumerated in Step 3`
  - spec §6.5: `All black-box. 13 files carry an Eventually site; 15 carry Eventually or Never; 4 packages:` (and keep the package list unchanged, which is correct either way).

### F2 — **LOW** — ADR §4's heading quantifies over `require.Never`, but **6 of the 16** sites are `assert.Never`

- **Attacked:** ADR-0184 Decision **§4 heading**: *"`require.Never` budgets are deliberately NOT changed"*; spec §3.2 const comment: *"Deliberately NOT applied to `require.Never`"*; plan Task 3 Step 2 const comment: *"Deliberately NOT used for `require.Never`"*.
- **Command:**
  ```
  $ grep -rn --include='*_test.go' "require\.Never(" scheduler/ | wc -l
  10
  $ grep -rn --include='*_test.go' "assert\.Never(" scheduler/ | wc -l
  6
  ```
- **Real value:** 10 `require.Never` + 6 `assert.Never` = 16. The blanket "`require.Never`" phrasing
  names only 10 of the 16 sites it is meant to protect.
- **Not a defect in the mechanics:** the plan's Global Constraints line *does* say
  "`require.Never` / `assert.Never`", and Step 4's guard greps `Never(`, which catches both. Only the
  prose quantifier is narrow.
- **Corrected text:** in the ADR heading and both const comments, write **"`Never` budgets (10
  `require.Never` + 6 `assert.Never`)"** rather than `require.Never`.

### F3 — **INFO** — spec §6.1's *"Matches that are comments rather than calls: 0"* is pattern-dependent and false as written

- **Attacked:** spec §6.1: *"Matches that are comments rather than calls: **0**, so 40 is a call count."*
- **Command:**
  ```
  $ grep -rn --include='*_test.go' "Eventually" scheduler/ | wc -l       # 41
  $ grep -rn --include='*_test.go' "Eventually(" scheduler/ | wc -l      # 40
  $ sed -n 259p scheduler/internal/gocron/trigger_test.go
  // one-for-one — same fake-clock-advance + NextRun + Eventually technique —
  ```
- **Real value:** there **is** one comment mention of `Eventually` (`trigger_test.go:259`). It is
  excluded only because the grep pattern carries a `(`. The conclusion (40 = call count) is right;
  the supporting sentence is not.
- **Corrected text:** *"Matches of the bare word `Eventually` in `scheduler/`: 41, of which exactly
  one (`gocron/trigger_test.go:259`) is prose in a comment. The pattern `Eventually(` yields 40, all
  calls."*

### F4 — **CRITICAL** — the `writeOnlyTaskStore` re-derivation is WRONG. The count stays **1**, not 2. The plan prescribes an assertion that goes RED and cannot be made green as written.

- **Attacked:**
  - spec §4.3 table row: *"`writeOnlyTaskStore` | legal ⇒ `Len(failures, 1)` | the 2 cases with a `listedBy` ⇒ **2**; the other 3 legal ⇒ **1** | nothing persisted, so the positive inbox assertion also fails"*
  - plan **Task 1 Step 7**, first code block: `want := 1; if c.listedBy != inboxNone { want = 2 }`
  - spec §4.3 heading: *"which breaks **three** pinned expectations"*; ADR Consequences: *"**Three** of its expectations are re-derived"*; spec §7: *"**Three** pinned expectations … are re-derived"*.
- **Command (I implemented the plan's §4.2/Step 3–5 change verbatim in this worktree and printed the real per-store, per-case failure counts through `recorderT`):**
  ```
  $ go test -count=1 -run '^TestZZAudCProbeCounts$' ./processtest/ -v
  EXIT=0
  ```
- **REAL VALUES observed, AFTER the 43b change is applied:**

  | stand-in | legal case | `listedBy` | failures BEFORE | failures AFTER |
  |---|---|---|---|---|
  | `writeOnlyTaskStore` | `unclaimed_without_a_claim_is_accepted` | ClaimableBy | 1 | **1** |
  | `writeOnlyTaskStore` | `claimed_with_a_claim_is_accepted` | AssignedTo | 1 | **1** |
  | `writeOnlyTaskStore` | other 3 legal | none | 1 | **1** |
  | `inboxFailingTaskStore` | `unclaimed_without_a_claim_is_accepted` | ClaimableBy | 0 | **1** |
  | `inboxFailingTaskStore` | `claimed_with_a_claim_is_accepted` | AssignedTo | 0 | **1** |
  | `inboxFailingTaskStore` | other 3 legal | none | 0 | **0** |
  | `MemTaskStore` / `permissiveTaskStore` / `leakyRollbackTaskStore` / `rejectingTaskStore` / `kioskHostileTaskStore` | every case | — | — | **unchanged** |

- **Root cause the bundle missed:** `checkTaskStoreConformance` **returns early** on the `Get` guard —
  ```go
  got, getErr := store.Get(ctx, c.task.TaskID)
  if !assert.NoErrorf(t, getErr, "Get(%s) after an accepted Upsert: the task must be readable", c.name) {
      return                              // ← taskstoreconformance.go:155-157
  }
  ```
  `writeOnlyTaskStore.Get` always returns `ErrTaskNotFound`, so control **never reaches**
  `checkTaskStoreAcceptedTaskIsListed`, which the plan appends *after* the two round-trip
  `assert.Equalf`s. The spec's stated reason — *"nothing persisted, so the positive inbox assertion
  also fails"* — is a reasoning-by-analogy error: the assertion is not evaluated at all.
- **Blast radius:** an implementer executing Task 1 Step 7 verbatim writes an assertion that is red
  and stays red. Per Premise Discipline the obvious "fix" is to relax `Lenf`→`NotEmptyf`, which
  §4.3 itself warns *"destroys the property that file exists to protect"*. The bundle contains both
  the trap and the warning against the way out of it.
- **Corrected text:**
  - spec §4.3 table: delete the `writeOnlyTaskStore` row from the *affected* table and move it to
    the *Unaffected* list with the real reason: **"`writeOnlyTaskStore` — unaffected: the legal leg
    returns early at the `Get` guard (`taskstoreconformance.go:155-157`), so the new inbox check is
    never reached; the pinned `Len(failures, 1)` stays exactly as it is."**
  - spec §4.3 heading: *"which breaks **one** pinned expectation and one comment"*.
  - ADR Consequences and spec §7: *"**One** of its expectations is re-derived, and one false comment
    is rewritten"* (not "three").
  - plan Task 1 **Step 7: delete the `writeOnlyTaskStore` block entirely.** Only the
    `inboxFailingTaskStore` block changes.
  - plan Task 1 **Step 6** currently says *"Two other cases are expected to FAIL now"*. The real
    number is **one** (`inboxFailingTaskStore`); `writeOnlyTaskStore` keeps passing. Correct it, or
    an implementer will hunt for a second failure that does not exist.

### F5 — **MAJOR** — spec §5.1 says *"the **5** re-derived pinned counts"*; the real number of changed count literals is **2**

- **Attacked:** spec §5.1 table row 5: *"| the 5 re-derived pinned counts in §4.3 | current code: the legal leg asks no inbox, so the old counts hold | §4.3 |"*.
- **Real value:** §4.3's own table proposes four count literals (writeOnly 2/1, inboxFailing 1/0) — already not 5. After F4 removes the writeOnly pair, **2** literals change: `inboxFailingTaskStore`'s legal branch goes from a single `assert.Emptyf` to `want ∈ {1, 0}`.
- The bundle therefore states this one quantity **four different ways**: "two pinned expectations and one comment" (§4.3 prose), "three pinned expectations" (§7 and ADR Consequences), "5 re-derived pinned counts" (§5.1), and four literals (§4.3 table). None of them is the measured answer.
- **Corrected text:** §5.1 row → *"the `inboxFailingTaskStore` legal-branch count, re-derived from `Empty` to 1-iff-`listedBy` (2 literals)"*, evidence §4.3 + the measured table in F4.

### F6 — **MAJOR** — *"the helper rises off 77.8 %"* is FALSE. Subprocess coverage is not merged; the figure stays **77.8 %** with both guard blocks at hit-count 0.

- **Attacked:** spec §5.3: *"Coverage expectation: `processtest`'s helper **rises off 77.8 %** because the guards become reachable"*; plan **Verification** bullet 2: *"Expect `processtest`'s helper to rise off 77.8 %"*; spec §7 Good: *"Two misuse guards become reachable, and the helper's **coverage rises** for a real reason."*
- **Command:** I implemented Task 2's nil-`newStore` half (env-armed helper + parent spawning a child `go test`), confirmed it passes, then measured:
  ```
  $ go test -count=1 -coverprofile=/tmp/audc-cover3.out ./processtest/
  ok ... coverage: 91.8% of statements
  $ go tool cover -func=/tmp/audc-cover3.out | grep RunTaskStoreConformance
  .../taskstoreconformance.go:245: RunTaskStoreConformance   77.8%
  $ grep 'taskstoreconformance.go:2(4[5-9]|5[0-9]|6[01])' /tmp/audc-cover3.out
  ...:248.21,250.3 1 0     <- the nil-newStore Fatal, STILL 0 hits
  ...:255.20,257.5 1 0     <- the nil-store   Fatal, STILL 0 hits
  ```
- **Real value:** **77.8 %, unchanged**, and both guard blocks are still recorded as never executed.
  The guards run in a **child `go test` process** that is spawned without `-coverprofile`; its counters
  are discarded. The parent process only ever *skips* the env-armed helper. Baseline for comparison
  (no new tests): the same `77.8 %`, package total `91.8 %`.
- **Why it matters:** the plan's Verification checklist tells the controller to expect a number that
  cannot occur. Two ways that goes wrong at the gate: the delivery is reported as failing when it is
  not, or an implementer "fixes" it by moving the guards' execution in-process — which means
  re-introducing the withdrawn `conformanceRunner` seam that ADR §2 explicitly rejects.
- **Corrected text:**
  - spec §5.3: *"Coverage expectation: `processtest`'s helper stays at **77.8 %**. The guards are
    executed in a **child** `go test` process, whose counters are not merged into the parent's
    profile — measured: both guard blocks remain hit-count 0. This is the accepted cost of the
    subprocess pattern (§4.1); the guards are *tested*, not *covered*."*
  - plan Verification: replace *"Expect `processtest`'s helper to rise off 77.8 %"* with
    *"`processtest`'s helper stays at 77.8 % — subprocess coverage is not collected. Do NOT treat an
    unchanged figure as a missing test."*
  - spec §7 Good bullet: drop *"and the helper's coverage rises for a real reason"*; keep *"Two
    misuse guards become executed for the first time."*
  - The `77.8 %` in spec §1b is **correct** and should stay: 7 of 9 statements in
    `RunTaskStoreConformance`, the two uncovered ones being exactly `:249` and `:256`.

---

## MORE VERIFIED-CORRECT

### V7 — §6.2 *"a store that answers both inbox queries with `nil, nil` passes the entire suite"* — CORRECT
```
$ go test -count=1 -run '^TestZZAudCVacuousInbox$' ./processtest/ -v
EXIT=0 ... --- PASS: TestZZAudCVacuousInbox (0.00s)
   (all 8 subtests RUN, none failed)
ok  github.com/kartaladev/wrkflw/processtest  0.597s
```

### V8 — §6.3 *"the two positive assertions are constructible against the reference store"* — CORRECT
```
PROBE ClaimableBy(alice) -> n=1 err=<nil> ids=[wrkflw-conformance-unclaimed]
PROBE AssignedTo(alice)  -> n=1 err=<nil> ids=[wrkflw-conformance-claimed]
```
Matches the numbers §6.3 records verbatim.

### V9 — *"5 legal and 3 rejected cases"*, and every case name the bundle spells — CORRECT
```
$ grep -c "legal: true"  processtest/taskstoreconformance.go   -> 5
$ grep -c "legal: false" processtest/taskstoreconformance.go   -> 3
```
All five legal names in spec §4.2's table and plan Step 4 exist **verbatim** in
`taskStoreConformanceCases()`: `unclaimed_without_a_claim_is_accepted`,
`claimed_with_a_claim_is_accepted`, `claimed_by_an_empty_kiosk_claimant_is_accepted`,
`completed_without_a_claim_is_accepted`, `cancelled_retaining_its_claim_is_accepted`. The plan's
verbatim `task(...)` lines for the two `listedBy` cases are byte-identical to the source.
`listedBy` assigned to exactly 2 legal cases + `inboxNone` to 3 legal (+ 3 rejected by zero value) = 8.

### V10 — plan's `failedSubtests` hazard — CORRECT and load-bearing
`taskstoreconformance_factory_test.go:129-137` really does hardcode
`"--- FAIL: "+factoryFatalHelper+"/"`. Called unchanged from a new test it returns 0 for every input
and `assert.Zerof` passes vacuously. The plan's Step 1a parameterisation is right, and it has exactly
**one** existing caller (line 96, in `TestRunTaskStoreConformanceAttributesAFactoryFailureToItsSubtest`)
— the plan's "its one existing caller" is exact. The file ships exactly the **three** reusable helpers
the spec §4.1/§6.8 and ADR §2 name (`attributedTest`, `verboseTestName`, `failedSubtests`).

### V11 — the 7 stand-in stores in the negative leg — the bundle's 2-affected + 5-unaffected split is COMPLETE (but mis-assigned; see F4)
`TestCheckTaskStoreConformanceCatchesNonConformingStores` has exactly **7** rows: `MemTaskStore`,
`permissiveTaskStore`, `rejectingTaskStore`, `writeOnlyTaskStore`, `kioskHostileTaskStore`,
`leakyRollbackTaskStore`, `inboxFailingTaskStore`. Spec §4.3 names 2 + 5 = 7 — none missed. The
**membership** is wrong (F4): the real split is **1 affected** (`inboxFailingTaskStore`) + **6
unaffected**.

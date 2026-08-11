# Plan — a dying instance harvests its open scopes, then closes them

**Spec:** [`docs/specs/2026-08-11-dying-instance-harvests-open-scopes.md`](../specs/2026-08-11-dying-instance-harvests-open-scopes.md)
**ADR:** [ADR-0174](../adr/0174-a-dying-instance-harvests-its-open-scopes.md)
**Audit record:** [`docs/specs/2026-08-11-adr-0174-audit-evidence.md`](../specs/2026-08-11-adr-0174-audit-evidence.md)

---

## ▶ Progress

**Branch:** `feat/dying-instance-harvests-open-scopes` (do not quote the bundle SHA — an
amend changes it).
**Base:** `main` @ `02b72be`.
**State: ✅ SHIPPED.** All phases complete; both reviews run; merged to `main` and pushed.

**Implementation and all eleven tests are in, every one mutation-verified.**
`harvestOpenScopeCompensations()` in `state_compensation.go`; the `endInstance` backstop
(harvest **before** the cursor clear, `s.Scopes = nil` last); the harvest at
`handleUnhandledError` and `handleCancelRequested`. Tests T1–T5, T7–T12 (T6 was removed
with the deleted decision). Verified on the branch tree: `go test -race
-coverprofile=cover.out ./...` **EXIT=0, 64 packages, 0 races**, coverage **74.2 %** repo
/ **92.7 %** engine; `golangci-lint run ./...` **0 issues** (repo-wide); `go vet ./...`
clean.

Phase 1 and Phase 2's two call sites were implemented inline by the controller; the
remaining tests ran as subagent-driven development (rule #11) in **two serial waves** —
wave 1 T5+T7, wave 2 T9+T11+T12 — serial because the whole delivery is inside `engine`.
Each wave's diff was reviewed and one of its mutations independently re-run by the
controller before the next was dispatched. Two earlier attempts to delegate died on
transient API 529s with zero progress; they were resumed rather than replaced, then the
work was taken inline.

### Mutation table (all restores verified byte-clean)

| # | Mutation | Tests that went RED | Verdict |
|---|---|---|---|
| M-a | delete the harvest **call site** in `endInstance` | T3 | discriminates |
| M-b | delete `s.Scopes = nil` in `endInstance` | T4, T8 | discriminates |
| M-c | delete the harvest **call site** in `step_errors.go` | T1 | discriminates |
| M-d | delete the harvest **call site** in `step_triggers.go` | T2 | discriminates |
| M-e | **reorder** the harvest to AFTER the cursor clear | T10 | discriminates — see below |
| M-f | neuter the helper **body** (early `return`) | T1, T2, T3 | discriminates |
| M-g | remove `hasCompensationRecordsToWalk`'s **archive branch** | T5 only — every snapshot-shape test stayed GREEN | discriminates; this is what proves T5 earns its place, since M-a alone would have turned it RED only because T3 already covers that |
| M-h | harvest **innermost scope only** (replace the loop with one call) | T7 only, whole-package | T7 is the sole guard on the multi-scope loop |
| M-i | hoist the harvest **above** `handleUnhandledError`'s in-flight guard | T9's error row only | ⚠ its RED shows the mid-walk harvest stamps `TeardownArchiveKey:"subA"` onto a cursor that will still advance — the exact ADR-0173 residue the window fields exist to remove. Stronger evidence for the placement than the spec's prose |
| M-j | hoist the harvest above `handleCancelRequested`'s guard | T9's cancel row only | the two rows are not redundant — each is falsified only by its own site |
| M-k | `CancelRequested.terminalPolicy() → allowOnTerminal` | T11's cancel row | ⚠ its RED **re-dispatches `undo-inner` on an already-dead instance** — flipping that policy is a double-compensation of harvested records |
| M-l | delete the harvest call site in `step_errors.go` | T12 | `expected 3 (Compensating) / actual 2 (Failed)` |
| M-m | delete the harvest in `stepCompensateRequested` | T13's plain-full-rollback row | gate finding 2 |
| M-n | **remove the terminality gate** (harvest unconditionally) | T13's **full-reverse guard** row | ⚠ proves the gate is load-bearing, not decoration — an ungated harvest hoists a resuming walk's records |
| M-o | delete the harvest in `applyFinish`'s deferred re-entry | T14 | gate finding 1 |

M-e is the one that matters, because it is the only check on Decision 3. Its RED
reproduces the audit's measurement: archived `[undoA]` → `[undoA undoB undoC]`, and the
subsequent rollback re-dispatching `undoC` — an already-completed compensation running
a second time.

⚠⚠ **Two process defects hit during implementation. Both are recorded because both
produced a confident wrong conclusion, not a visible failure.**

1. **`git checkout <path>` restores from the INDEX, so it DESTROYS uncommitted work.**
   The mutation loop restored `step_errors.go` / `step_triggers.go` that way while the
   Phase 2 implementation in them was still uncommitted — silently reverting it to
   `main`. `git diff --quiet` then reported *"restored clean"* **because** the
   implementation was gone. T1/T2 sat RED for several steps and the reordering
   mutation appeared to be caught by an existing test when it was not.
   **Rule: commit before mutating, or snapshot by file copy — never `git checkout` a
   file holding uncommitted work.**
2. **M-e was initially run BEFORE T10 existed and reported `EXIT=0`** across the whole
   engine suite — correctly proving the ordering was unprotected. The first
   interpretation of that same signal (that a test *had* caught it) came from defect 1.
   A mutation result is only as good as the tree it ran against.

### `/code-review` findings (delivery gate) — 3 of 4 fixed, 1 HELD

Four findings: 3 Medium, 1 Low. **Seventh consecutive delivery where the gate beat the
pre-gate audit.**

- ✅ **Finding 2 (Medium)** — `stepCompensateRequested` had no harvest, so an admin
  rollback on a LIVE instance dispatched nothing and a SECOND rollback was needed.
  Fixed with a harvest **gated** on `ToNode == "" && ReverseNode == ""`. New test T13
  (two rows: terminating rollback compensates; full reverse must NOT hoist).
- ✅ **Finding 1 (Medium)** — `applyFinish`'s deferred-cancel re-entry had no harvest,
  so a deferred cancel terminated *around* a sibling scope's completed work. Fixed
  after `applyPlanRecordClearing`. New test T14.
- ✅ **Finding 3 (Medium) — ACCEPTED AS A DOCUMENTED BOUND, not fixed** (owner decision).
  Recorded in spec §5.3.1 with its measurement, in the ADR's Decision correction block,
  and in the helper's own doc comment — which had asserted the protection
  unconditionally and was false for this cursor shape. An unpinned (pre-ADR-0171)
  live cursor bypasses `partitionForLiveWalk` entirely, because
  `scopeWideWalkDraining` requires `len(cur.Records) > 0`. Measured
  `pinned=false → archived=[undoA undoB undoC]` where `[undoA]` was owed, and this
  delivery's own T5 then admits a rollback that re-runs them — an exposure **introduced
  here**, since `main` refused that rollback.
  ⚠ **The obvious fix collides with a shipped decision.** ADR-0173 chose that conjunct
  deliberately: *"partitioning on its behalf would delete records nobody ever runs …
  Deliberate: losing the record outright is worse."* Widening the predicate reverses
  that preference on indices ADR-0173 judged untrustworthy without a pinned snapshot,
  and `TestArchiveCompensationsPartitionRequiresALiveWalk` guards the adjacent conjunct.
  `/security-review` then adjudicated it **REAL-BUT-NOT-SECURITY at 2/10**: no attacker
  in the loop, no privilege boundary crossed, the class already pre-existing on `main`
  via `cancelScopeSubtree`, and reachability requiring a restart across an unreleased
  one-day-old commit boundary *with* a walk mid-flight, *then* a force-termination,
  *then* a manual rollback. Owner accepted the bound. A real fix belongs with ADR-0173's
  cursor-migration story.
- ✅ **Finding 4 (Low)** — the ADR's *"only in the no-record shape"* was false:
  `handleSubInstanceFailed`'s tail keeps its tokens **and** nils `Scopes`. Doc-only.

⚠⚠ **The `git checkout` trap recurred, in the same session that documented it.** The
gate-fix mutation loop restored `step_compensation.go` while the two new harvests were
still UNCOMMITTED, reverting them; the next two mutations' anchors then failed and their
`EXIT=1` was the un-patched tree, not a mutation result. Caught by checking that the
anchors matched. **Knowing the rule is not the same as having a habit: commit first.**

### Adjudications from the implementation waves

- **T11 is KEPT despite not being exhaustive.** `engine/step_terminal_dispatch_test.go`'s
  `TestTerminalDispatchOutcomes` (ADR-0165) already carries the exhaustiveness property
  more strongly, because it is driven off `allTriggerVariants` and therefore catches a
  **newly added** trigger, which T11's hand-maintained 10-row list cannot. What T11 adds
  is the **substrate**: the post-harvest terminal snapshot (`Scopes` nilled, the record
  reachable only under an archive key minted by the harvest rather than by a normal scope
  exit) — a state no test could construct before this delivery. The file header documents
  that split rather than implying an exhaustiveness it does not have. Folding it into the
  sibling would require moving to `package engine`, losing the black-box preference
  (Golang rule #5) for no gain.
- **Two cheap cleanups deliberately NOT done here** (out of scope, not forgotten):
  `driveToForceTerminationInsideSub` was committed unused in an earlier amend, and
  `TestDyingInstanceHarvestsOpenScopes`'s table still inlines the same three-Step drive
  the helper now encapsulates. T5 uses the helper; the table's duplication remains.

Source-verified facts, re-derived independently by all three audit lenses:

- `endInstance` (`engine/state.go:380-391`) never touches `s.Scopes` — nine
  statements (**not** ten; ten is the line count).
- The dying-instance records-exist predicates are **three**: `step_errors.go:253`,
  `step_triggers.go:213`, `step_compensation.go:91`. Two *mid-flight* readers do
  consult an open scope (`step_nodes.go:1204`, `:1160`) and are out of scope.
- `endInstance` has **ten** call sites; `beginCompensation` has **four** callers and
  hardcodes `const scopeID = ""` at `step_compensation.go:316`.
- `handleCancelRequested`'s in-flight guard is at `step_triggers.go:163` (**not**
  `:162`, which is a comment).
- No existing test pins a zombie scope on a terminal instance — and two lenses
  implemented the whole fix and got `go test ./engine/` **EXIT=0**.

## Execution constraints

- ⚠ **Every task is in `engine/`.** Per rule #11, concurrent agents inside one Go
  package break each other's `go test` compile even on disjoint files, so this
  delivery runs **strictly serial** in the controller, with docs-only work
  interleaved. Do **not** fan out by task here.
- **Docker: standing permission for the Verification coverage + no-regressions runs**
  (owner, 2026-08-11). Probe the daemon and run; if unavailable, say so and let the owner
  start it or skip. Not needed for `engine` alone. ⚠ Any subagent brief must still
  **say so explicitly** for anything else.
- ⚠ **A green `go test` exit code can be a CACHE HIT.** An auditor's panic probe
  reported `EXIT=0` while the code under it panicked (Go caches on observed
  `os.Getenv`). Confirm with `-v` that the test ran; judge by exit code, never by a
  `| grep | head` tail.
- ⚠ **`go test -run` on a nonexistent name exits 0** — never certify absence with a
  filtered run.
- TDD strict: a visible RED in a `Bash` call before every new symbol.

## Phase 0 — rule-#9 adversarial audit ✅ COMPLETE

Three Opus lenses, separate worktrees, all briefed to execute: (A) scope lifetime /
referential integrity, (B) compensation ownership / ADR-0173 interaction, (C) premise
truth / enumeration / test falsifiability. Adjudication in spec §9; full record in
the audit-evidence file, **committed in-repo** so it cannot die with a worktree.

⚠ **Process defect to avoid next time:** two of the three worktrees were created at
the base commit **without the bundle**. Both auditors recovered and said so; one that
had silently audited a paraphrase would have produced confident, worthless findings.
**The brief must require verifying the audit tree contains the bundle.**

## Phase 1 — the helper, the backstop, and the close

Serial, all in `engine/`. RED before each.

**1.1 `harvestOpenScopeCompensations()`** in `engine/state_compensation.go`.
Snapshot the scope IDs, then call the existing `s.archiveCompensations(id)` per id.
Does **not** remove `Scope` entries. Doc comment must state that only the
`partitionForLiveWalk` **drop** protects (the window stamp is discarded downstream).

→ RED: ⚠ **not** a compile error. The audited draft claimed
`undefined: harvestOpenScopeCompensations`, which is impossible — the symbol is
unexported and these are black-box `engine_test` tests (Golang rule #5). The real RED
is an **assertion** failure: T8 sees `scopes=1` where it wants `nil`; T3 sees
`ArchivedCompensations=map[]`. If a white-box unit test of the helper is also wanted,
name it separately and put it in `package engine` (the package already mixes both —
`state_dying_test.go` is `package engine`).

**1.2 `endInstance` backstop**: harvest **before**
`s.Compensating = compensationCursor{}` (spec M6), then `s.Scopes = nil` at the end.
→ RED: T3 and T4 — today `ArchivedCompensations=map[]` / `scopes=1`.

**1.3 T8** — no-record harvest is a no-op, `Scopes` becomes exactly `nil`.
⚠ Assert `== nil`, **not** `assert.Empty` (which passes for `[]` and `nil` alike and
so cannot see the persisted-shape change).

**1.4 T10** — the ADR-0173 interaction, **re-fixtured** per the audit: a
**force-terminated** scope-wide walk over ≥3 records, **advanced ≥1 step** so the
offset moves. Assert the rollback re-dispatches `[undoA]`, not `[undoC undoB undoA]`.
⚠ Do **not** use a walk's own finish — the cursor is already zeroed at
`step_compensation.go:709`, so both orderings compute identically and the test cannot
fail.

**Verify:** `go test ./engine/... > /tmp/out.log 2>&1; echo EXIT=$?` then read the log.

## Phase 2 — the two live sites

**2.1 `handleUnhandledError`**: harvest immediately before `step_errors.go:253`,
**after** the in-flight guard at `:246`.
→ RED: **T1** — today no compensation `InvokeAction` is emitted at all.
⚠ Assert on the presence of the `undo-inner` invoke, **not** a command count: the
count is fixture-dependent (a human-task park yields `cmds=2`).

**2.2 `handleCancelRequested`**: harvest immediately before `step_triggers.go:213`,
after its own in-flight guard at `:163`.
→ RED: **T2**, same criterion.

**2.3 T9** — an in-flight walk still defers at both sites (ADR-0170).
⚠ Re-specified: the audited draft's falsifier was not one, because both guards read
only the cursor and are blind to harvest placement. Assert on **the record set the
deferral leaves behind**.

**2.4 T12** 🆕 — pin the incident/event-payload consequence (spec M7): the walk
cancels the surviving token, `removeOrphanedIncidents` retires the incident, and
`runtime/outbox.go`'s `terminalEventErr` therefore reports differently.
→ RED: today `tokens=2 incidents=1`.

## Phase 3 — remaining pins

**3.1 T5** — admin rollback walks on an instance terminated via an
**`endInstance`-only** route (force-termination). ⚠ Restricted deliberately: the
audited draft demanded all three routes and was **unsatisfiable** — on the
error/cancel routes the post-fix instance is `compensating`, not terminal.

**3.2 T7** — nested scopes archive under their own `NodeID` keys, asserted **on the
force-termination route**. On the error/cancel routes `beginCompensation` consolidates
immediately, so the archive is observably `map[]`; assert record presence and reverse
order in `RootCompensations` there instead.

**3.3 T11** — replaced with the **structural** assertion: the only `allowOnTerminal`
trigger is a plain full rollback, and its walk uses the root scope. The audited
draft's "stays non-wedging" could not fail.

## Phase 4 — documents describe what shipped

4.1 **Repair ADR-0162's stale sentence** (`docs/adr/0162-…:299-302`) — it names
`endInstance`/ADR-0164 as the closer. Replace with a pointer to ADR-0174 and a note
that 0164 shipped without it.
4.2 Add a sentence to **ADR-0034** recording that its intent is now met for
open-scope records, and that `main` conformed to it as written (the gap was in the
0034 × 0039 interaction).
4.3 Fold any implementation-driven correction back into ADR-0174 **in this bundle**
(rule #11), with the measurement that refuted the original.
4.4 Sweep the diff's own comments for unexecuted claims and over-reaching
quantifiers (Premise Discipline) — this bundle has already produced six.
4.5 Rewrite `docs/plans/HANDOVER.md` **in place**; update this `▶ Progress`; update
auto-memory (`MEMORY.md` index line + topic file).

## Phase 5 — delivery gate

1. `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` on
   the **merged** tree — engine ≥ 92.6 %, repo ≥ 74.2 %. ⚠ Needs Docker for the
   non-`engine` packages → **ask the owner first.**
2. `golangci-lint run ./...` clean.
3. Documents describe what shipped (Delivery Gate step 2).
4. `/code-review` → fix **all** findings, fold via `--amend`.
5. `/security-review` → fix **all** findings, fold via `--amend`.

⚠ `/code-review` has beaten the pre-gate audit in **six consecutive deliveries**.

## Verification checklist

- [x] Phase 0 audit executed, adjudicated (spec §9), record committed in-repo
- [x] T1 — unhandled error compensates before failing (RED observed, then GREEN)
- [x] T2 — cancel compensates before terminating
- [x] T3 — force-termination-inside-sub archives rather than strands
- [x] T4 — no terminal snapshot carries an open `Scope` (force-termination route)
- [x] T5 — admin rollback walks on the `endInstance`-only route
- [x] T7 — nested scopes archive under their own keys (force-termination route)
- [x] T8 — no-record harvest is a no-op; `Scopes` is exactly `nil`
- [x] T9 — ADR-0170 deferral unchanged, asserted on the record set
- [x] T10 — force-terminated advanced walk: `[undoA]`, not `[undoA undoB undoC]`
- [x] T11 — structural: one `allowOnTerminal` trigger, root-scope walk
- [x] T12 — incident retirement / event-payload consequence pinned
- [x] Mutation-verified for everything shipped so far (M-a…M-f above)
- [x] `go vet ./...` compiles cleanly (proves no hidden consumer)
- [x] Delivery gate 1 — `go test -race -coverprofile ./...` EXIT=0, 64 pkgs, 0 races; coverage **74.2 %** repo / **92.7 %** engine
- [x] Delivery gate 2 — repo-wide `golangci-lint run ./...` → 0 issues
- [x] Delivery gate 3 — `/code-review`: 4 findings, **3 fixed + mutation-verified**, 1 accepted as a documented bound
- [x] Delivery gate 4 — `/security-review`: **0 vulnerabilities**; its one candidate filtered at 2/10 (REAL-BUT-NOT-SECURITY)
- [x] Gate re-run on the final tree — suite `-race` EXIT=0 / 64 pkgs / 0 races, coverage **74.2 %** repo / **92.7 %** engine, `golangci-lint run ./...` 0 issues, `go vet` clean
- [x] Merge `--no-ff` to `main` and push

⚠ **T4's coverage is narrower than its spec row claims.** It is asserted on the
force-termination route only; the error/cancel routes end `compensating` and reach a
terminal status one Step later, so "all three routes" needs the walk driven to
completion. Either extend T4 or narrow the §7 row — do not leave the row over-claiming.

## Backlog opened by this delivery

1. **Records already stranded on pre-ADR-0174 rows stay unreachable** (spec §5.3).
   Recovering them safely needs information the row does not carry. An opt-in admin
   operation is the plausible shape — its own ADR.
2. **ADR-0164's "eight terminal sites" is stale** — ten call sites today.
3. **`compensationRecordsForScope` reads an open scope as a records-exist decision**
   (`step_nodes.go:1204`), invisible to the predicate grep. Not a defect; a
   documentation/enumeration hazard for the next reader.
4. **A `go test` cache hit can report `EXIT=0` over panicking code** when the test
   observes `os.Getenv`. Worth a note in CLAUDE.md's pitfalls.

## Mutation table

*Filled during implementation. One row per test: the line broken, the observed RED,
and confirmation the restore was byte-identical (`diff`).*

| # | Test | Line mutated | Observed RED | Restored clean |
|---|---|---|---|---|
| | | | | |

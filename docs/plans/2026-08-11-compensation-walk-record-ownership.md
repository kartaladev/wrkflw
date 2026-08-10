# Plan — ADR-0173: a compensation walk's finish consumes exactly what it drained

Spec: [`docs/specs/2026-08-11-compensation-walk-record-ownership.md`](../specs/2026-08-11-compensation-walk-record-ownership.md)
ADR: [`docs/adr/0173-…`](../adr/0173-a-compensation-walk-finish-consumes-exactly-what-it-drained.md)

## ▶ Progress

- **Branch**: `feat/compensation-double-run-on-scope-teardown` (off `main` @ `1618b29`).
- **State**: audited **and IMPLEMENTED**. Phases 1–5 complete; Phase 6 gate steps 1–4
  PASS. Only `/code-review` and `/security-review` remain (owner-invoked).
- **Verification**: full repo `go test -race -coverprofile=cover.out ./...` **EXIT=0,
  64 packages, 0 races**, `scripts/coverage.sh` **74.1 %** (identical to the last
  shipped delivery — no regression) · `go test -race ./engine/...` **92.6 %** (up from
  92.5 %) · `go vet ./...` EXIT=0 (it compiles the Docker-only test packages, so the
  three new cursor fields broke no hidden consumer) · `golangci-lint run ./...`
  **0 issues**.
- **New code coverage**: `archiveCompensations`, `partitionForLiveWalk`,
  `scopeWideWalkDraining`, `dropArchiveRecordAt`, `deleteArchiveSlot`,
  `consumeDispatchedRecord`, `applyPlanRecordClearing` — **100 % each**.
- **Tests**: T1–T5 and T8–T12 landed, all RED-first with the failure observed
  (`[undoC undoB undoA]`, `[undoB undoA]`, `map[...]{}`, a `nil` dispatch), plus two
  white-box tests added in Phase 5 to cover code no route reaches (below).

### Mutation results — 10/10 catch

Every anchor confirmed **present and compiling** before its run; suite judged by
exit code, restored from snapshot and `diff`ed after each.

| # | mutation | first failure |
|---|---|---|
| M1 | drop `len(cur.Records) > 0` | `TestTeardownMidWalkLeavesAPreADR0171CursorAlone` |
| M2 | drop `cur.ScopeID == scopeID` | `…CompensatesEachRecordExactlyOnce` (ancestor row) |
| M3 | drop `cur.ActiveCmdID != ""` | `TestArchiveCompensationsPartitionRequiresALiveWalk` |
| M4 | archive the whole prefix, not the head | `…WhenTheWalkIsABANDONED` |
| M5 | skip the consume at advance | `…WhenTheWalkIsABANDONED` |
| M6 | skip the consume at targeted walk start | `TestCompensationThrowTargetedParity` |
| M7 | drop the sibling tail from the archive | `…CompensatesEachRecordExactlyOnce` |
| M8 | drop the finish's residual-window removal | `TestApplyPlanRecordClearingRemovesAResidualWindow` |
| M9 | restore the unconditional whole-key delete | `TestTargetedThrowRetainsARecordItNeverDrained` |
| M10 | drop the map-nilling | `TestTargetedThrowFinishLeavesNoEmptyArchiveMap` |

⚠ **M3 and M8 caught NOTHING on the first pass** (whole suite EXIT=0). Neither was
deleted on the strength of a green suite — *undemonstrated is not unreachable* —
and neither was left silent. Both are reachable only if an invariant elsewhere
changes, so both got a direct white-box test; see the spec's §6 for why each is
kept.

### Corrections implementation made to the design

1. **Decision 3 does not stamp `StartRecordCount`** — consuming incrementally
   removed the need, so one of the two promised doc amendments was unnecessary and
   that comment stays true.
2. **A proposed `validate()` invariant was refuted on contact**: key and count are
   NOT set together — the usual finish carries a non-empty key with a zero count.
3. Both are folded into the ADR's own "Corrections made during implementation".

### `/code-review` — 4 findings, all adjudicated and folded

⚠ **Sixth consecutive delivery where the real gate found what the design audit did
not.** Every finding was re-verified by execution before being accepted.

1. **MEDIUM — both halves of the new `applyFinish` block were unverified.** Measured:
   swapping `deleteArchiveSlot` back to a plain `delete`, and dropping the
   `!plan.archiveConsumed ||` disjunct, each left `./engine` at **EXIT=0**. The
   disjunct is load-bearing — without it a pre-ADR-0171 TARGETED cursor's dispatched
   records survive its finish for a later walk to re-run — and it had **no test**.
   → `TestLegacyTargetedCursorStillConsumesItsWholeArchiveKey` (needs TWO records;
   with one the disjunct is an identity). Both mutations now RED.
2. **MEDIUM — a false premise in a committed comment.** T8's docstring and
   `dropArchiveRecordAt`'s doc both credited `applyFinish`'s whole-key delete for
   nilling the map. True of `main`; on this branch the slot is usually emptied by
   `consumeDispatchedRecord` first, so `applyFinish`'s guard short-circuits. → both
   comments corrected. Exactly the Premise-Discipline class the gate exists to catch.
3. **LOW — ownership transfers on DISPATCH, not on success.** A compensation action
   that fails has already had its record consumed: measured `re-dispatched=[]` where
   `main` gave `[undoB undoA]`. Consistent with the pre-existing non-teardown path,
   but documented now in the ADR's Consequences; a retry/incident story is backlog.
4. **LOW — the teardown-window stamp overwrites unconditionally.** Unreachable today
   (every teardown closes its scope, and a second call hits the `len == 0` guard),
   but silent two-directional data loss if that convention broke. → documented at the
   stamp and pinned by `TestArchiveCompensationsPartitionsAScopeAtMostOnce`.
   ⚠ **My first version of that test asserted on the window COUNT, which recomputes
   to the same value — a vacuous assertion.** The offset is what moves; the test now
   asserts that, and says so.

### `/security-review` — 0 vulnerabilities; 1 robustness regression found and fixed

Identification pass returned two MEDIUM candidates; both went through independent
false-positive filters and **both were DROPPED as security findings** (3/10 and
8/10-analysis-confidence-but-verdict-DROP). Reasoning accepted: the impact is a
crashed consumer process (hard exclusion #1, DoS), and the precondition is write
access to the instance-state row — an actor who has that can already rewrite
`Status`, `Variables` and `Tokens` and alter workflow outcomes silently, which
strictly dominates a loud crash.

⚠ **But the underlying defect was real, measured, and MINE**: two places read a
PERSISTED cursor without the clamping the rest of this package applies, and `main`
absorbs the same input — so each was a regression this delivery introduced, not an
inherited gap. Fixed and mutation-verified:

- `partitionForLiveWalk` took `StartRecordCount` unclamped → `slice bounds out of
  range [:-1]` inside the pure engine core. `clearRecordsPrefix` clamps the SAME
  field with an explicit "(defensive)" note; the new code is now that convention.
  → M11.
- `stepCompensationFinish` copied the teardown window straight onto `finishPlan`,
  so a row carrying a count with no key tripped `validate()`'s panic. That
  falsified `validate()`'s own licence to panic ("never from persisted or external
  input"). → `normalizeTeardownWindow` at the cursor read, and the doc comment now
  says why new persisted-sourced plan fields must go through it. → M12.

⚠ **My first test for the second one did not discriminate**: it called
`normalizeTeardownWindow` directly, so deleting the CALL SITE left the suite green.
A mutation has to be able to see the wiring, not just the helper — the end-to-end
route was added and M12 now catches. Third test-quality defect of my own this
delivery.

### Final gate

Full repo `go test -race ./...` **EXIT=0, 64 packages, 0 races**, coverage
**74.2 %** · engine race-clean · `go vet ./...` EXIT=0 · `golangci-lint` **0
issues** · **12/12 mutations RED**.
- ⚠ Fixtures and probes live only in the session scratchpad; nothing `zz_` is in the
  tree (`git status` is the check).

## Constraints

- **Everything is in package `engine`** → **strictly serial**. Concurrent agents inside
  one Go package break each other's `go test` compile even on disjoint files. No fan-out.
- `engine` is **container-free**. ⚠ **Ask before using Docker** — this is standing, and
  any subagent brief must repeat it verbatim: a brief that omitted it last delivery had
  an agent spin testcontainers unasked.
- Black-box `engine_test`. ⚠ `engine/` mixes `package engine` and `package engine_test`
  — `head -1` any existing test file before writing into it.
- **Judge every run by exit code**: `go test ./engine/... > /tmp/out.log 2>&1; echo "EXIT=$?"`.
  ⚠ `go test -run` on a nonexistent name **exits 0** — confirm a test ran with `-v`.
- TDD strict: every task is RED-first with an observable failing run in the transcript.

## Phase 1 — the shared consume helper and the cursor's window

**1.1 (RED)** Test that `consumeDispatchedRecord` drops the right record and nils
`ArchivedCompensations` when its last key goes. Compile failure is a valid RED.

**1.2 (GREEN)** Add `TeardownArchiveKey/Offset/Count` to `compensationCursor` (doc:
what they mean, that they are scalars so `cloneState` stays a value copy, and that
`""` is both "no teardown" and the pre-0173 persisted value). Add
`dropArchiveRecordAt` and `consumeDispatchedRecord`. **The map-nilling lives in the
shared helper** so both the new path and `applyFinish`'s pre-existing whole-key delete
get it — spec §5.7: the empty-map wart is PRE-EXISTING on `main`, and fixing it in one
branch only leaves it alive in the other.

**1.3 (RED first)** T8: `ArchivedCompensations` is nil, not `{}`, after the last key
goes — on **both** paths.

## Phase 2 — the teardown partition (Decision 1)

**2.1 (RED)** T5 first: a sibling appends mid-walk **and** the scope is torn down; the
archive must hold the un-dispatched head **and** the sibling tail, in completion order.

**2.2 (GREEN)** `scopeWideWalkDraining(scopeID)` — exactly the three conjuncts in
ADR Decision 1 — plus the partition in `archiveCompensations` and the window stamp.
⚠ Do **not** add `walkThrowScopeWide` or `scopeID != ""` back: both were measured
non-discriminating by two independent auditors, and this repo deletes a guard nobody
can fail rather than keeping it for decoration.

**2.3 (RED first)** T11: a pre-ADR-0171 cursor (`Records == nil`) is left **alone** —
archive keeps everything, behaviour identical to `main`. This is the Critical the audit
caught; it must have a test that goes RED when the conjunct is dropped (M1).

**2.4 (RED first)** T9: ancestor teardown reaching the walk's scope through
`cancelScopeSubtree`'s **descendant loop** (spec §4.1a). ⚠ Give the *other* scope
**≥ 2 records** — with one, M2 is an identity and the test cannot discriminate.

## Phase 3 — consume as the walk dispatches (Decision 2)

**3.1 (RED)** T12: three records, teardown, **one advance into the head**, then
abandonment via a force-termination end event. Must recover exactly the remainder.
This is the fixture that refuted the first design — it goes RED both on `main` and on
the finish-only shape.

**3.2 (GREEN)** Call `consumeDispatchedRecord` from `stepCompensationAdvance` at the
dispatch, and remove the residual window at the finish.

**3.3 (RED first)** T1, T2, T4 — the three original routes, now with **assertions**,
not the probes' logging. T4 in particular must assert the dispatched set, and must not
`return` early on the refusal path.

**3.4 (RED first)** T10: an unhandled error after a teardown walk re-runs nothing
(`step_errors.go`'s branch — the only automatic re-entry route).

**3.5** Assert — do not comment — that the `consumePendingCancel` re-entry also leaves
no residual window.

## Phase 4 — the targeted branch (Decision 3)

**4.1 (RED)** T3: targeted throw + mid-walk re-entry retains the second visit and the
deferred throw compensates it. Named fixture `targetedReentryDef`.

**4.2 (GREEN)** `consumeDispatchedRecord` at targeted walk start and advance; the
finish deletes the key only when the slot is empty; `len(cur.Records) > 0` gates it so
a pre-0171 targeted cursor keeps the whole-key delete.

**4.3** Amend the doc comments that go false: `StartRecordCount`'s *"Zero for every
other walk (targeted throw, …)"*, and `step_compensation.go:674`'s *"a second throw to
the same ref finds len == 0 and no-ops"*. The latter narrows an ADR-0120 contract and
is already recorded in this ADR's Amends — check the code comment matches.

**4.4** `finishPlan.validate()`: add the invariants the new fields imply. If an
invariant cannot be violated by any in-package construction, do not add it — say so.

## Phase 5 — mutations

Run all ten from spec §6. For each: break the line, **confirm the anchor is present and
compiles**, `go test ./engine/... > log 2>&1; echo EXIT=$?`, restore from snapshot,
`diff` to confirm the restore. ⚠ A mutation whose anchor does not match measures
nothing and looks like a working guard.

⚠ **M3 (`ActiveCmdID != ""`) has no predicted RED.** If nothing goes red, the conjunct
is undiscriminating: delete it, or document it as deliberately uncovered. Do not keep
it silently — that is exactly what M1/M10 of the previous list were.

Table every row — including non-catching ones — in ▶ Progress.

## Phase 6 — Delivery Gate

1. `go test -race ./engine/...` (container-free). Full-repo
   `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out`
   needs Docker → **ask first**.
2. **Delete every `zz_` scratch file BEFORE the gate** — probes at the repo root break
   `go build ./...` outright (`found packages wrkflw and engine`), which step 3 depends on.
3. `go vet ./...` — compiles every test file including Docker-only ones; the cheap proof
   the three new cursor fields broke no hidden consumer. (The audit established there
   are **zero** readers of `ArchivedCompensations`/`Scope.Compensations` outside
   `engine`, so this should be clean; verify rather than assume.)
4. `golangci-lint run ./...` clean.
5. Re-read spec + ADR + plan against the built code; amend every divergence in-bundle
   (CLAUDE.md rule #11), especially any behaviour the ADR promises that implementation
   changed. Sweep the diff's own comments for unexecuted claims and over-reaching
   quantifiers.
6. `/code-review`, then `/security-review` — owner-invoked. Fold fixes with `--amend`;
   re-run the suite after each wave.

## Verification checklist

- [ ] T1 boundary teardown → later cancel re-dispatches nothing
- [ ] T2 nested-ESP teardown → admin rollback on the completed instance refused
- [ ] T3 targeted re-entry → 2nd visit retained and compensated by the deferred throw
- [ ] T4 abandoned walk (2 records) → exactly the un-dispatched head, **asserted**
- [ ] T5 sibling tail survives a teardown, in completion order
- [ ] T8 `ArchivedCompensations` nil, not `{}`, on **both** deletion paths
- [ ] T9 ancestor teardown via the descendant loop, other scope carrying ≥ 2 records
- [ ] T10 unhandled error after a teardown walk re-runs nothing
- [ ] T11 pre-ADR-0171 cursor left alone; identical to `main`
- [ ] T12 abandon after advancing into the head → exactly the remainder
- [ ] 10/10 mutations run, anchors confirmed present, results tabled in ▶ Progress
- [ ] M3 adjudicated: either it discriminates, or the conjunct is deleted/documented
- [ ] `engine` ≥ 85 % line coverage, every touched hot path and failure branch covered
- [ ] no `zz_` file in the tree; `go build ./...`, `go vet ./...`, `golangci-lint` clean
- [ ] spec/ADR/plan re-read against the built code; divergences amended in-bundle
- [ ] `/code-review` + `/security-review` findings fixed or explicitly adjudicated
- [ ] `HANDOVER.md` rewritten in place; backlog item 4 closed, and spec §7's two
      remaining bounds (mixed-version deploy, pre-0171 cursor) filed

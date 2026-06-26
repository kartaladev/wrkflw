# Plan — P1: Serialize concurrent compensation throws

Spec: `docs/specs/2026-06-27-parallel-compensation-throw-design.md`. ADR: `docs/adr/0071-serialize-parallel-compensation-throws.md`.
Branch: `fix/parallel-compensation-cursor`. Module: `github.com/zakyalvan/krtlwrkflw`.

## Global Constraints (binding — copy to reviewers verbatim)

- This is an ENGINE-CORE bug fix. The diff to `engine/` must be MINIMAL and guarded: single-throw
  and ALL existing compensation flows must be byte-for-byte unchanged in behaviour. The new code path
  is reachable ONLY when a compensation throw is encountered while `Compensating.ActiveCmdID != ""`.
- No new `Command` types, no `Status` model change, no new imports (engine import-purity preserved).
- model/ untouched. Engine stays wall-clock-free (use the passed `at time.Time`, never `time.Now`).
- Serialize semantics: at most ONE compensation walk in flight; drain deferred throws one-per-finish.
- Error sentinels keep the `workflow-` prefix; assert with `errors.Is`.
- Gate: `go test -race ./engine/...` green incl. a `FuzzStep` smoke run; engine ≥85% coverage; lint 0;
  gofmt clean. Run `go test -race ./...` to confirm no cross-package regression.
- Project skills: table-test (assert-closure, `t.Context()`); tests in `engine_test` black-box where
  practical (some compensation tests are white-box `package engine` — match the existing file's package).

## Task 1 — Regression test FIRST (RED)

Files: a new/extended test in `engine/` (match the package of `step_compensation_throw_test.go`).
- Build a definition with a `ParallelGateway` forking to branch A and branch B; each branch has a
  sub-process (or pre-seeded archived compensations under refA/refB) with a compensable activity,
  then a compensation-throw `IntermediateThrowEvent` (`CompensateRef` refA / refB) → join/end.
- Drive in Macro mode so BOTH throws process in one `drive` pass.
- Assert the bug: with current code, after both throws, completing the FIRST throw's `InvokeAction`
  (the `ActionCompleted` for the first emitted `ActiveCmdID`) yields `ErrTokenNotFound` (cursor was
  overwritten). Run `go test ./engine/...` to SEE THIS FAIL/expose the bug (RED). Keep this as the
  regression assertion that the fix must flip to: both compensations complete in sequence.

## Task 2 — Implement serialize (GREEN)

Files: `engine/state.go` (+ `engine/step_nodes.go`, `engine/step_compensation.go`).
- Add `DeferredCompensationThrows []string` to `InstanceState`.
- In `intermediateThrowEventStrategy.enter` compensation branch: before the walk-start `else` body,
  if `c.s.Compensating.ActiveCmdID != ""` → park the throw token (`TokenWaitingCommand`, do NOT
  consume, do NOT set cursor, emit nothing), append `tok.ID` to `c.s.DeferredCompensationThrows`,
  return stopped. Otherwise unchanged.
- In `stepCompensationFinish` throw-resume branch (`resumeNode != ""`): after `placeTokenInScope`
  and before `drive`, if `DeferredCompensationThrows` non-empty, pop the first id, find that token,
  set `tok.State = TokenActive` so the following `drive` starts its walk via the normal path.
- Make the Task-1 regression test pass: both branches' compensations run sequentially; both resume.

## Task 3 — Sub-cases + dedup investigation

- Add table cases: 3 parallel throws drain one-per-finish; a throw whose ref has no archived records
  auto-advances and does NOT enqueue; `PendingCancel` arriving mid-first-throw still defers correctly
  and the deferred second throw + cancel interact safely (cancel wins after the in-flight walk, per
  ADR-0040 — verify no deferred-throw is left orphaned; document the resolved ordering).
- recordCompensation dedup: attempt a reproducing double-compensation test (partial-rollback retain
  path or sub-process-exit call site). If reproduced → minimal dedup keyed on token-id+node-id (NOT
  nodeID alone). If NOT reproducible → leave `recordCompensation` untouched and note the analysis in
  the report + ADR-0071 (fill the bracketed outcome). Do not speculatively edit.
- `FuzzStep`: run a short smoke (`-run=^$ -fuzz=FuzzStep -fuzztime=20s` or the repo's convention) to
  confirm no crash; if the corpus/generator can't reach a compensation-throw shape, note it.

## Verification checklist
- [ ] T1 regression RED observed (cursor overwrite → ErrTokenNotFound) before the fix.
- [ ] T2 fix makes both parallel compensations complete in sequence; deferred queue drains.
- [ ] T3 sub-cases (3 throws, empty-ref, PendingCancel) pass; dedup investigated + decided + documented.
- [ ] Single-throw + existing compensation tests unchanged & green; `go test -race ./...` no regressions.
- [ ] engine ≥85%; lint 0; gofmt clean; FuzzStep smoke clean; engine import-pure; model/ untouched.
- [ ] ADR-0071 outcome filled; HANDOVER updated; whole-branch opus review clean.

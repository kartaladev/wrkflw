# Scope-targeted compensation — Implementation Plan

> Executed via superpowers:subagent-driven-development. STRICT TDD. CORE ENGINE change — `Step` pure/deterministic; `cloneState` extended; the no-double-compensation invariant is load-bearing. Phase 2 is the highest-risk task (reverses ADR-0013; interacts with ADR-0034) — give its review extra scrutiny.

**Goal:** Scope-targeted compensation via a compensation throw event (`Node.CompensateRef`) + archive-by-scope (`ArchivedCompensations`, replacing the ADR-0013 hoist) + a `Compensate` handler with resume-and-continue. Single-ownership ⇒ each record compensated at most once.

## Global Constraints
- Module `github.com/kartaladev/wrkflw`; no `pkg/` prefix.
- STRICT TDD; RED before GREEN, visible in its own `go test` call.
- `Step` PURE + DETERMINISTIC (sorted archive iteration; no clock/random); engine import-purity; `cloneState` deep-copies `ArchivedCompensations`.
- **No double-compensation:** a completed compensable activity is compensable at most once across throw / cancel / error paths (single ownership: open-scope | archive | already-run).
- ADR-0013 hoist tests are UPDATED to archive-by-scope (deliberate behaviour change, re-asserted — not silently broken). ADR-0034 cancel/error walk still compensates everything completed (now root + archive) and clears both; existing cancel/error compensation tests stay green or are updated for the new source.
- `workflow-` prefix; black-box tests; table-test assert-closure; `t.Context()`.
- Gate: `go test -race -p 1 ./...` green; ≥85% engine/model/runtime; lint clean; no model→engine inversion (CompensateRef is a model field).
- Spec: docs/specs/2026-06-23-scope-targeted-compensation-design.md. ADR-0039.

## Tasks (= spec §4 phases)

### Task 1 (Phase 1): model `CompensateRef` + Validate
**Files:** `model/definition.go` (+field), `model/validate.go` (+`ErrCompensateRefNotFound`), `model/validate_test.go`.
- Add `Node.CompensateRef string` (godoc: on a KindIntermediateThrowEvent, names the completed activity/sub-process whose compensation to run; empty = current/root scope).
- `Validate`: a `KindIntermediateThrowEvent` with non-empty `CompensateRef` must reference an existing node, else `ErrCompensateRefNotFound` (recurse into sub-processes like the other rules).
- TDD: test a compensation throw with a dangling ref → error; a normal throw (no ref) and a valid ref → no error. RED→GREEN. Commit `feat(model): compensation throw CompensateRef + validation`.

### Task 2 (Phase 2 — HIGHEST RISK): archive-by-scope (replace hoist) + extend cancel/error walk
**Files:** `engine/state.go` (`ArchivedCompensations`, `cloneState`, `archiveCompensations`, `allCompensationRecords`), `engine/step.go` (sub-process exit site ~1106; `beginCompensation`/`stepCompensationFinish` source+clear), `engine/state_test.go`, `engine/step_compensation_test.go`.
- Add `InstanceState.ArchivedCompensations map[string][]CompensationRecord`; `cloneState` deep-copies it.
- Replace `hoistCompensations(child, parent)` at step.go:1106 with `archiveCompensations(childScopeID, subProcessNodeID)` (move `scope.Compensations` into `ArchivedCompensations[subProcessNodeID]`, append).
- Add `allCompensationRecords(s)` returning the combined reverse-order sequence: `RootCompensations` reversed, then each `ArchivedCompensations` entry by ascending key, each reversed — DETERMINISTIC. The instance cancel/error walk (`beginCompensation` with the root/instance outcome, ADR-0034) sources from this; `stepCompensationFinish` (full-rollback) clears BOTH `RootCompensations` and `ArchivedCompensations`.
- UPDATE the ADR-0013 hoist tests (`TestHoistSubProcessCompensationToRoot` etc.) to assert archive-by-scope instead. Assert ADR-0034 cancel-with-a-completed-sub-process still compensates the sub-process records (now via archive) AND is not double-run on re-cancel.
- TDD per change. Commit `feat(engine): archive sub-process compensations by scope; extend cancel/error walk`.

### Task 3 (Phase 3): Compensate producer + handler + resume-and-continue
**Files:** `engine/command.go`/`engine/state.go` (cursor `ResumeNode`), `engine/step.go` (throw producer + `stepCompensate` handler), `engine/step_compensation_test.go`.
- Producer: a token reaching `KindIntermediateThrowEvent` with `CompensateRef` parks and the step emits the first compensation `InvokeAction` for the target's archived (or root) records, setting the cursor (ScopeID/target + `ResumeNode` = the throw's single successor). (Reuse `beginCompensation` with a new resume mode, or a dedicated `stepCompensate`.)
- Handler: the reverse walk runs (advance on ActionCompleted as today); on finish, REMOVE the compensated records from `ArchivedCompensations[ref]` (so they can't re-run), set `StatusRunning`, move the token to `ResumeNode`, and `drive`.
- TDD: sub-process completes → archived → throw referencing it runs its compensation (reverse order) → resumes past the throw → completes; a second throw to the same ref is a no-op; a throw + later instance cancel does NOT double-run the sub-process. RED→GREEN. Commit `feat(engine): compensation throw event runs scope-targeted compensation then resumes`.

### Task 4 (Phase 4): runtime e2e + controller docs/merge
**Files:** `runtime/scope_compensation_test.go`.
- Runtime e2e (MemStore + catalog): a process with a sub-process whose inner task is compensable, then a compensation throw referencing the sub-process → the inner compensation action runs and the instance completes.
- Controller: ADR-0039 verify, HANDOVER + memory, full gate, **opus** whole-branch review (scrutinize the no-double-compensation invariant + ADR-0013/0034 interaction), merge.

## Verification Checklist
- [ ] `go test -race -p 1 ./...` green; ≥85% engine/model/runtime.
- [ ] `golangci-lint run ./...` clean; engine import-purity; `cloneState` test extended.
- [ ] No-double-compensation invariant tested (throw + cancel don't both run a sub-process's records).
- [ ] ADR-0013 hoist tests updated to archive-by-scope (deliberate change); ADR-0034 cancel/error still compensates root+archive.
- [ ] `Step` determinism (sorted archive iteration) test.
- [ ] Opus whole-branch review; merge + push; HANDOVER + memory.

## Spec coverage self-check
- §0 single-ownership/no-double-comp → Task 2 + Task 3 (the core invariant). §1 model → Task 1. §2.1 archive → Task 2. §2.2 producer/handler → Task 3. §3 determinism → tests. §4 e2e → Task 4. ✓

# 0071. Serialize concurrent compensation throws (fix parallel-branch cursor overwrite)

Status: Accepted — 2026-06-27
Relates to: ADR-0039 (deferred this case), ADR-0040 (no cross-trigger compensation re-entry).

## Context

`InstanceState.Compensating` is a single compensation cursor. In Macro mode, `drive()` advances all
active tokens in one pass; two compensation-throw `IntermediateThrowEvent`s in parallel branches were
both processed in one pass, and the second silently OVERWROTE the first's cursor (`ActiveCmdID`),
orphaning the first walk (its `ActionCompleted` → `ErrTokenNotFound`). ADR-0040 guarded cross-trigger
re-entry but not this intra-`drive` case; ADR-0039 explicitly deferred concurrent parallel throws.

A compensation walk runs via `ActionCompleted` triggers, not via `drive`; only **two walks in flight
at once** are unrepresentable. Branch continuations after a throw are ordinary active tokens that may
overlap.

## Decision

**Serialize** concurrent compensation throws (≤1 walk in flight):
- Add `InstanceState.DeferredCompensationThrows []string` (engine bookkeeping; persisted; excluded
  from the snapshot DTO).
- In the compensation-throw handler, when a walk is already in flight (`Compensating.ActiveCmdID != ""`),
  do not start a second walk: park the throw token (`TokenWaitingCommand`, not consumed, cursor
  untouched) and enqueue its id. 
- In `stepCompensationFinish`'s throw-resume branch, after resuming the finished throw's branch and
  before `drive`, re-activate ONE deferred throw token (`TokenActive`); the existing `drive` →
  throw-handler path then starts its walk normally (cursor was just cleared). Drains one-per-finish.

This reuses all existing walk-start/finish logic, adds no `Command` types and no `Status` change, and
leaves single-throw and every existing compensation flow byte-for-byte unchanged (the new behaviour is
guarded by `ActiveCmdID != ""`).

`recordCompensation` dedup (also flagged in the backlog) was investigated: the `ActionCompleted` path
is already idempotent via the `tokenAwaiting` → `ErrTokenNotFound` guard. It is changed ONLY if a
reproducing double-compensation test can be written; otherwise left untouched (no speculative edits)
and the analysis recorded here.

Outcome (implementer, 2026-06-27): NOT reproducible — `recordCompensation` is left UNTOUCHED. Both
call sites are single-shot per execution: (1) the `ActionCompleted` site (`step_triggers.go`) records
before `moveAlongSingleFlow` clears `AwaitCommand`, so a duplicate/stale `ActionCompleted` for the same
`CommandID` finds no awaiting token (`tokenAwaiting` → nil) and returns `ErrTokenNotFound` BEFORE
reaching `recordCompensation`; (2) the sub-process-exit site (`step_nodes.go`) runs exactly once per
scope exit, immediately followed by `closeScope`, so the same scope cannot re-record. A probe test
(`step_compensation_dedup_probe_test.go::TestRecordCompensationDoubleRecordIsRejected`) confirms a
duplicate `ActionCompleted` yields `ErrTokenNotFound` and exactly one record persists. Adding a
`nodeID`-only dedup would have been actively WRONG (it would drop legitimate loop re-executions of the
same node); a `token-id+node-id` key was considered but is unnecessary since no double-record path
exists. Recommendation: revisit only if a future call site records compensation outside the
`ActionCompleted`/scope-exit guards.

## Consequences

- The documented silent cursor-corruption bug is fixed; N parallel compensation throws run their
  walks strictly in sequence, with branch continuations overlapping freely. Compensation order across
  independent parallel scopes is unspecified in BPMN, so serialization is conservative and correct.
- Engine core gains one bookkeeping field + one guarded branch in the throw handler and one
  re-activation step in finish — a deliberate, test-driven bug-fix diff to `engine/` (the standing
  near-zero-diff convention yields to a documented correctness fix). Import-purity preserved.
- Fully concurrent (non-serialized) parallel compensation remains out of scope: it would require a
  multi-cursor or per-scope-status redesign — a separate, approval-gated effort.
- Crash safety: deferred-throw tokens (parked) and the id queue both persist in `InstanceState`, so a
  mid-compensation crash rehydrates with the queue intact.

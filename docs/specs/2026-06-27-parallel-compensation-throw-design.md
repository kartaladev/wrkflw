# Spec — Fix parallel compensation-throw cursor overwrite (serialize concurrent throws)

Date: 2026-06-27
Status: Accepted (autonomous backlog program, track P1)
Relates to: ADR-0039 (scope-targeted compensation / throw events — deferred this case), ADR-0040
(no compensation-walk re-entry across triggers).

## Problem (the documented bug)

In **Macro** mode, `drive()` advances every active token in one pass. If a parallel fork has a
compensation-throw `IntermediateThrowEvent` (`CompensateRef != ""`) in **two** branches, both throw
tokens are processed in the SAME `drive` pass. `intermediateThrowEventStrategy.enter`
(`engine/step_nodes.go:854-877`) unconditionally:
- consumes the throw token,
- sets `s.Status = StatusCompensating`,
- **overwrites the single `s.Compensating` cursor** (`ArchiveKey`, `ActiveCmdID`, …),
- emits the first `InvokeAction`.

The second throw in the same pass overwrites the first throw's cursor: the first walk's
`ActiveCmdID` is lost. When the first `InvokeAction`'s `ActionCompleted` arrives, it no longer
matches `s.Compensating.ActiveCmdID`, so `handleActionCompleted` (`step_triggers.go:46`) falls
through to `tokenAwaiting` → `ErrTokenNotFound`, and the first throw's compensation is orphaned /
the walk corrupted. ADR-0040 fixed cross-*trigger* re-entry but NOT this intra-`drive` case;
ADR-0039 explicitly deferred "compensation throw concurrent with open parallel fork".

## Key insight

The single-cursor design only forbids **two compensation walks in flight at once**. A compensation
walk proceeds via `ActionCompleted` triggers, not via `drive` (the throw token is consumed and there
are no active tokens for that walk). Parallel **branch resumptions** after a throw finishes are
ordinary active tokens and may overlap freely. Therefore the correct, minimal fix is to **serialize**
concurrent compensation throws: at most one throw walk in flight; a throw encountered while a walk is
already in flight is **deferred** and started when the in-flight walk finishes. Branch continuations
are unaffected.

## Design — serialize via defer + re-activate

### State (`engine/state.go`)
Add `DeferredCompensationThrows []string` to `InstanceState` — token IDs of compensation-throw
tokens parked because a walk was already in flight. It is engine bookkeeping (persisted with the
state, excluded from `runtime.InstanceSnapshot` like `Compensating`/`PendingCancel`).

### Throw handler (`engine/step_nodes.go`, compensation-throw branch)
Before starting a walk (the `else` branch that currently stamps the cursor), check:
`if c.s.Compensating.ActiveCmdID != ""` (a walk is already in flight):
- Do **not** consume the token, **not** touch the cursor, **not** emit an `InvokeAction`.
- Park the token: `tok.State = TokenWaitingCommand` (no `AwaitCommand`), append `tok.ID` to
  `c.s.DeferredCompensationThrows`. Return stopped (parked) — `drive` moves to the next active token.
The empty-records / no-successor auto-advance branch is unchanged (it never starts a walk).

### Finish (`engine/step_compensation.go`, `stepCompensationFinish`, throw-resume branch `resumeNode != ""`)
After deleting the archive entry, handling `PendingCancel`, setting `Status = StatusRunning`, and
placing the resume token at `resumeNode` (all unchanged) — and BEFORE the existing `drive(...)`:
- If `len(s.DeferredCompensationThrows) > 0`: pop the first token ID, find that token in `s.Tokens`,
  and re-activate it (`tok.State = TokenActive`). The subsequent `drive(...)` re-enters the throw
  handler for that token; since the cursor was just cleared (`ActiveCmdID == ""`), it starts that
  throw's walk through the **normal, existing path** (no logic duplication). Pop exactly ONE per
  finish so only one walk is ever in flight.
- The popped token might itself reach the "already in flight" guard again only if another walk is
  somehow in flight — it is not (cursor cleared), so it starts cleanly. Any *further* deferred throws
  remain queued and are drained one-per-finish as each walk completes.

This makes N parallel compensation throws run their walks strictly sequentially while their branch
continuations proceed normally. No new `Command` types, no `Status` model change, no duplication of
the walk-start logic.

## recordCompensation dedup (investigate, fix only if reproducible)

The HANDOVER also flags "partial-rollback record-retention hazard (no `recordCompensation` dedup)".
Analysis shows the `ActionCompleted` call site (`step_triggers.go:67`) is already guarded: a duplicate
`ActionCompleted` for the same `CommandID` hits `tokenAwaiting` → `ErrTokenNotFound` (line 52) BEFORE
reaching `recordCompensation`, so it cannot double-record via that path. The implementer MUST:
- Attempt to write a *reproducing* test for a genuine double-record (e.g. via the partial-rollback
  retain path or the sub-process-exit call site `step_nodes.go:421`).
- If a real double-compensation is reproduced → add a minimal dedup keyed on a STABLE per-execution
  identity (e.g. the token id + node id of the completing activity), NOT on `nodeID` alone (which
  would wrongly drop legitimate loop re-executions). Document the key.
- If NOT reproducible → do NOT change `recordCompensation` (no speculative edits to subtle engine
  code); record the analysis in ADR-0071 and defer with a clear note. This is the responsible default.

## Testing

- **Regression (RED first):** build a definition: ParallelGateway forks to branch A and branch B;
  each branch ran a sub-process with a compensable activity (archived under refA/refB) then has a
  compensation-throw `IntermediateThrowEvent` (CompensateRefA / CompensateRefB) followed by a join/end.
  Drive in Macro mode so both throws process in one pass. Assert WITHOUT the fix the cursor is
  corrupted (first throw's `ActionCompleted` → `ErrTokenNotFound`); WITH the fix both compensations
  run to completion in sequence and both branches resume. Assert the deferred queue drains.
- Sub-cases: 3 parallel throws (drains one-per-finish); a throw whose ref has no records still
  auto-advances and does not enqueue; interaction with `PendingCancel` mid-throw still defers cancel.
- Keep `FuzzStep` green; if natural, ensure the corpus can reach the parallel-throw shape.

Gate: `go test -race ./engine/...` green (incl. `FuzzStep` smoke), engine ≥85% coverage, lint 0,
gofmt clean. Engine import-purity preserved (no new imports). model/ untouched.

## Risk & scope

- This is an engine-core behaviour change (a bug fix). It is **additive/defensive**: it only changes
  what happens when a SECOND throw is reached mid-walk (previously: silent corruption; now: deferred
  and run in sequence). Single-throw and all existing compensation flows are byte-for-byte unchanged
  (the new branch is guarded by `ActiveCmdID != ""`).
- Full *concurrent* (non-serialized) parallel compensation remains out of scope (would require a
  multi-cursor / per-scope-status redesign — a separate, approval-gated design). Serialization is the
  correct, conservative semantics and matches BPMN (compensation order across independent scopes is
  unspecified).

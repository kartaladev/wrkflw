# 40. No compensation-walk re-entry (fix mid-walk double-compensation)

- Status: Accepted
- Date: 2026-06-23

## Context

The engine runs compensation as a single instance-wide reverse walk over a cursor
(`InstanceState.Compensating`, `StatusCompensating`): each completed compensable activity's
compensation action is emitted one at a time, advancing as each `ActionCompleted` arrives. Several
triggers can *start* such a walk:

- `CancelRequested` → `beginCompensation` (ADR-0034 compensation-on-cancel).
- unhandled error → `beginCompensation` (ADR-0034 compensation-on-error).
- `CompensateRequested` (admin/debug) → `stepCompensateRequested` → `beginCompensation`.
- a compensation **throw** event reaching a token (ADR-0039).

The ADR-0039 whole-branch review found and reproduced **B1**: a `CancelRequested` delivered *while a
compensation throw walk was in flight* re-entered `beginCompensation` and re-emitted the in-flight
record → double-compensation. ADR-0039 fixed the throw case (defer via `PendingCancel`). The
re-review then found the **same bug class on the pre-existing ADR-0034 path**: a second
`CancelRequested` — or a second admin `CompensateRequested` — delivered *mid an in-flight terminal
cancel/error/admin walk* (`Compensating.ResumeNode == ""`) also re-entered `beginCompensation`,
re-emitting the in-flight compensation (and corrupting the cursor → `ErrTokenNotFound`). Both are
reachable through the public API (`Runner.CancelInstance`, the admin compensate trigger). Money-moving
compensation actions could run twice.

The root invariant was never enforced for the non-throw triggers: **never start a second compensation
walk while one is already in flight.**

## Decision

Guard every `beginCompensation` re-entry against an already-in-flight walk
(`s.Status == StatusCompensating && s.Compensating.ActiveCmdID != ""`):

- **`CancelRequested`:**
  - in-flight **throw** walk (`ResumeNode != ""`) → defer via `PendingCancel` (ADR-0039, unchanged):
    the throw drains, then a full cancel runs over the remaining records and terminates.
  - in-flight **terminal or admin-partial** walk (`ResumeNode == ""`) → **no-op**: the instance is
    already being compensated; the in-flight walk drives it to its end. (A cancel racing an admin
    *partial* rollback is therefore dropped — a rare admin-debug edge — in exchange for the
    no-double-compensation guarantee.)
- **`CompensateRequested`:** if a walk is already in flight → **no-op** (don't restart).

`ActionFailed` already had the correct guard (a failed *compensation* action advances the walk via its
`ActiveCmdID`; any other failure mid-walk finds no awaiting token and is rejected), so it needed no
change.

## Consequences

**Positive**
- Each completed compensable activity is compensated **at most once** across all re-entry orderings
  (cancel-then-cancel, compensate-then-compensate, cancel-during-error-walk, etc.) — the same
  guarantee ADR-0039 established for the throw path now holds for the ADR-0034 path.
- The cursor can no longer be corrupted by a re-entrant trigger (no more `ErrTokenNotFound` from a
  redundant cancel).
- Reproduced by RED-first regression tests
  (`TestSecondCancelMidCompensationWalkDoesNotDoubleCompensate`,
  `TestSecondCompensateRequestedMidWalkDoesNotDoubleCompensate`).

**Negative / trade-offs**
- A `CancelRequested` that races an in-flight admin **partial** rollback is silently dropped (the
  partial walk resumes at its `ToNode`). This is accepted: it is a rare admin-debug interleaving and
  the alternative (re-entering compensation) is the double-run we are eliminating. A future change
  could defer it (extend `PendingCancel` to the partial-rollback finish), but that is entangled with
  the separate pre-existing partial-rollback record-retention issue and is out of scope here.

Engine-only; `Step` stays pure/deterministic; no new dependencies.

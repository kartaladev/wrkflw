# 173. A compensation walk's finish consumes exactly the records it drained

- Status: Accepted
- Date: 2026-08-11

> Closes the double-compensation defect `/security-review` found during the
> ADR-0168–0171 delivery and recorded as PRE-EXISTING on `main`, together with the
> mirror-image over-deletion this delivery's own reproduction work uncovered on the
> targeted-throw branch.
>
> Design and every measurement:
> [`docs/specs/2026-08-11-compensation-walk-record-ownership.md`](../specs/2026-08-11-compensation-walk-record-ownership.md).

## Context

[ADR-0171](0171-a-compensation-walk-owns-its-record-source-and-resume-scope.md) gave a
compensation throw walk a **pinned** record source: `startCompensationWalk` snapshots
the records onto `compensationCursor.Records`, so a scope teardown can no longer
destroy what the walk iterates. It did not touch the other half of the arrangement —
**who owns those records afterwards**. Compensate-once bookkeeping still happens only
at `stepCompensationFinish`, through
`clearRecordsPrefix(s, cursor.ScopeID, StartRecordCount)` against the **live** scope
list. When the scope is gone by then, that call resolves `scopeByID` to `nil` and
silently does nothing.

Meanwhile the teardown that removed the scope has already copied that same live list —
including the prefix the walk committed to — into `ArchivedCompensations[scope.NodeID]`,
which `consolidateArchiveIntoRoot` later folds into `RootCompensations` for any
subsequent walk.

ADR-0171's `compensationWalkHoldsScope` covers the scope exits that **can** be
deferred. The defect lives on the two that cannot: an **error boundary** on the
enclosing sub-process (the boundary must fire) and a nested **interrupting event
sub-process** exit (`exitNestedEventSubprocessScope` closes the enclosing scope and
consults no hold).

### Measured on `main` @ `1618b29`

Error-boundary route, two compensable records:

```
POST-TEARDOWN: scopes=0 root=0 archive={outer=[undoA undoB]}
WALK DRAINED:  dispatched=[undoB undoA]   archive={outer=[undoA undoB]}
AFTER CANCEL:  re-dispatched=[undoB undoA]
```

Nested-event-sub-process route, one record, instance reaching `completed`:

```
WALK DRAINED: status=completed archive={outer=[undoA]}
ADMIN COMPENSATE ON THE COMPLETED INSTANCE: re-dispatched=[undoA]
```

⚠ The backlog entry that opened this work recorded *"1 record, `undoB1` dispatched
twice"* via `CancelRequested`. Both details understated it: with two records **every**
drained record re-runs, and an admin `CompensateRequested` on a **terminal** instance
is a second route — admitted precisely *because* the leaked records make
`hasCompensationRecordsToWalk` true ([ADR-0164](0164-terminal-transitions-are-one-path.md)
carve-out #1).

A normal sibling drain is **not** affected (`archive=nil`); ADR-0171's hold covers it.

### The same rule broken the other way

The **targeted** branch of the same finish runs `delete(s.ArchivedCompensations,
archiveKey)` — the whole key — although `archiveCompensations` documents that a
sub-process entered more than once accumulates both visits into that one slot.
Reproduced with a sibling that re-enters the referenced sub-process while the walk is
outstanding:

```
AFTER 2ND VISIT:   archive={sub=[undoInner undoInner]}   (walk pinned at 1)
AFTER WALK FINISH: archive={}  root=0  dispatched=[]
```

The second visit's record is deleted without ever being compensated, and the deferred
throw that pops at finish finds an empty slot. This is
[ADR-0120](0120-dedicated-compensation-throw.md)'s review-A1 rule — *clear only the
prefix you drained* — present on the scope-wide branch and absent on the targeted one.

So one invariant is violated in both directions: the scope-wide finish clears **less**
than it drained (double-compensation), the targeted finish deletes **more**
(lost compensation). Compensation actions are nowhere in this repo required to be
idempotent, so the first is a real integrity impact — a refund applied twice — and the
second leaves committed work permanently un-rolled-back.

### A third route the reproduction work missed, and the audit found

`cancelScopeSubtree` archives the named scope **and every descendant in slice
order**. A walk running in a *nested* scope therefore has its own records archived
when an **ancestor** is torn down — through the very call site this ADR's first
draft asserted "names scopes this walk does not own". Measured on `main`:

```
WALK START:    cursor.ScopeID="r3-s2" NextIndex=1 StartRecordCount=2 dispatched=[undoB]
POST-TEARDOWN: archive={outer=[undoOuter undoOuter2] inner=[undoA undoB]}
AFTER CANCEL:  re-dispatched=[undoB undoA undoOuter2 undoOuter]
```

Two double-runs. And a **third re-entry** route exists alongside the two operator
ones: `step_errors.go:253`, an unhandled error, which is the only *automatic* way
back into the leaked archive.

## Decision

**A compensation walk's finish consumes exactly the records that walk drained — no
more, no less.** Three changes realize it.

### 1. A mid-walk teardown archives only what the walk has not dispatched

`archiveCompensations(scopeID)` partitions the scope's live records when a
**pinned** scope-wide throw walk owns exactly that scope:

| range | meaning | disposition |
|---|---|---|
| `[NextIndex .. drained-1]` | already dispatched by this walk | **dropped** |
| `[0 .. NextIndex-1]` | committed to, not yet dispatched | **archived**, windowed (§2) |
| `[drained ..]` | appended by a live sibling mid-walk | **archived**, unwindowed |

`drained = min(StartRecordCount, len(records))`; `NextIndex` — the index of the
record currently **in flight**, the walk counting down — clamped into
`[0, drained]`.

The predicate is three conjuncts, extracted as `scopeWideWalkDraining(scopeID)`:

```go
cur.ActiveCmdID != "" && len(cur.Records) > 0 && cur.ScopeID == scopeID
```

`len(cur.Records) > 0` is load-bearing, not defensive: a cursor persisted before
[ADR-0171](0171-a-compensation-walk-owns-its-record-source-and-resume-scope.md) has
no pinned snapshot, so it never dispatches the archived head (see Consequences).
`walkThrowScopeWide` and `scopeID != ""` were **removed as provably
non-discriminating** — `ScopeID` is non-empty only for a scope-wide throw, and the
root scope always takes `archiveCompensations`'s `scope == nil` early return.

### 2. The archived head is consumed AS THE WALK DISPATCHES IT

Three new scalar `compensationCursor` fields record where the head landed —
`TeardownArchiveKey` (the closed scope's `NodeID`), `TeardownArchiveOffset`
(`len(slot)` before the append), `TeardownArchiveCount` — and a shared
`consumeDispatchedRecord(idx)`, called at each dispatch, drops `slot[Offset+idx]`
and sets `Count = idx`. The finish removes whatever remains, which is normally
nothing.

The indices stay trivial because **the walk counts down**: the record being
dispatched is always the window's last element, so removing it shifts nothing
still inside the window.

`TeardownArchiveKey == ""` means *no teardown happened* — the value on every
untorn-down walk and on every pre-0173 persisted cursor.

⚠ **This ADR originally removed the window only at the walk's finish.** The
rule-#9 audit refuted that with a fixture the author's own probe was too small to
reach — three records, a teardown, then one advance into the head, then an
abandonment:

```
finish-only design: walk dispatched [undoC undoB] → ADMIN COMPENSATE [undoB undoA]
main:               walk dispatched [undoC undoB] → ADMIN COMPENSATE [undoC undoB undoA]
```

It reduced the abandoned-walk double-run from two to one instead of closing it —
while that route was the entire justification for choosing this design over the
simpler one. Incremental consumption is the correction.

### 3. A targeted throw consumes its archive slot as it dispatches

The same rule on the branch whose record source **is** the archive.
`consumeDispatchedRecord` drops each record from `ArchivedCompensations[ref]` as it
is dispatched — at walk start too, since `startCompensationWalk` emits the first —
so the slot always holds exactly the un-dispatched remainder plus any
sibling-appended tail. The finish deletes the key only when the slot is empty.

This replaces the whole-key `delete` and subsumes the finish-time prefix trim this
ADR first proposed: with the slot consumed incrementally there is no offset to
compute. Gated on `len(cur.Records) > 0` for the same reason as Decision 1 — a
pre-0171 targeted cursor reads the slot **live**, so shrinking it underneath would
break its absolute indices.

### Rejected: dropping the whole committed prefix at teardown

The one-function version of Decision 1 — skip all `StartRecordCount` records, no
cursor fields, no window — was spiked, fixed every double-run route, and left
`./engine` at EXIT=0. It was rejected on a **measurement**, not a review.

`NextIndex` proves the walk has dispatched only `[NextIndex .. StartRecordCount-1]`,
and a **force-termination end event** ([ADR-0119](0119-unified-end-event.md)) can
abandon the walk in between — `forceTerminate` deliberately runs no compensation,
and `endInstance` clears the cursor. Measured on that route:

```
main:     ADMIN COMPENSATE → dispatched=[undoB undoA]   (undoB double-runs; undoA recovers)
option A: ADMIN COMPENSATE REFUSED: … nothing left to compensate (status terminated)
0173:     ADMIN COMPENSATE → dispatched=[undoA]
```

The simple fix trades a double-run for a lost compensation. The decision above is
correct on both dimensions, and the three cursor fields are what buys that.

## Consequences

**Positive.** Each row measured; `main` → shipped.

| route | `main` | 0173 |
|---|---|---|
| boundary teardown, later cancel | `[undoB undoA]` | `[]` |
| nested-ESP teardown, admin rollback on a completed instance | `[undoA]` | refused |
| ancestor teardown via the descendant loop | `[undoB undoA undoOuter2 undoOuter]` | `[undoOuter2 undoOuter]` |
| unhandled error re-entering the archive | `[undoB undoA]` | `[]` |
| targeted throw + mid-walk re-entry | 2nd visit **lost** | retained and compensated |
| abandoned walk, 3 records, one advance into the head | `[undoC undoB undoA]` | `[undoA]` |

A record a concurrent sibling appended mid-walk now also survives a teardown,
extending [ADR-0120](0120-dedicated-compensation-throw.md) review A1's guarantee to
the case where the scope disappears. No data migration in the
new-code-reads-old-cursor direction.

**Negative / accepted.**

- `compensationCursor` grows three persisted fields. They are scalars, so the
  struct's "every field except `Records` is a plain scalar" property and
  `cloneState`'s plain value copy stay true (verified — no test anywhere asserts
  the cursor's field count or uses a positional literal), but the JSON-projected
  shape widens.
- **Mixed-version deployments are NOT safe.** "No data migration" holds in one
  direction only: whole-`InstanceState` JSON round-trips without
  `DisallowUnknownFields` on that path, so new code reading an old cursor is fine,
  but **old code reading a new cursor silently drops the three window fields and
  re-serializes without them**, reinstating the double-run. Do not run pre-0173 and
  post-0173 engine builds against the same instance store.
- **A pre-ADR-0171 cursor keeps `main`'s behaviour, double-run included.** It has
  no pinned snapshot, so nothing can dispatch its head; the partition declines and
  the archive keeps everything (measured: `recovered=[undoB undoA]`, identical to
  `main`). The alternative loses the record outright, which is worse. This is the
  same old-rows population as the ADR-0167 deployment audit, and a test pins it so
  nobody "fixes" it by widening the predicate.
- A scope teardown now **writes to the compensation cursor**, and a dispatch now
  writes to `ArchivedCompensations`. Both are new couplings between seams
  previously joined only by the cursor's `ScopeID`.
- Decision 2's offset is positional, valid only because the slot is append-only
  between teardown and finish. The audit executed that premise rather than assuming
  it — note that the justification first written for it was wrong on two counts
  (`consolidateArchiveIntoRoot` has two call sites, not one; `beginCompensation`'s
  guards test status, not cursor liveness) even though the conclusion held.
- ADR-0119's contract is untouched: a force-termination end event still runs no
  compensation. This ADR makes the remainder *recoverable*, not automatic.
- **Ownership transfers on DISPATCH, not on success.** A compensation action that
  returns `ActionFailed` has already had its record consumed, so after a teardown it
  is never retried and no incident is raised. Measured on the error-boundary
  fixture: `undoB` fails, and a later cancel re-dispatches `[]` where `main`
  re-dispatched `[undoB undoA]`. This matches the pre-existing non-teardown path —
  `clearRecordsPrefix` has always cleared the drained prefix regardless of failures
  — so it is not a new inconsistency, but it IS the one place where this ADR
  strictly loses information `main` happened to retain, and `main` retained it only
  as a side effect of the leak being fixed here. Best-effort skip is ADR-0034 §2.5's
  existing contract; a real retry/incident story for failed compensation actions is
  a backlog item, not this delivery.

**Amends.**

- [ADR-0120](0120-dedicated-compensation-throw.md) — review A1's prefix-only rule
  was stated for the scope-wide branch; it is now enforced on the targeted branch
  and across a scope teardown. Its "a second throw to the same ref finds len == 0
  and no-ops" contract is **narrowed**: a second throw now finds the sibling tail
  and compensates it, which is the point.
- [ADR-0171](0171-a-compensation-walk-owns-its-record-source-and-resume-scope.md) —
  it pinned the walk's record *source*; this ADR settles record *ownership* after
  the teardown its pinning made survivable, and makes `Records != nil` load-bearing
  for a second reason.

## Corrections made during the rule-#9 audit

Three Opus auditors, isolated worktrees, briefed to execute rather than read.
Eleven findings accepted; four changed the decision. Recorded here because the ADR
as first written would have shipped every one of them:

1. **Critical** — Decision 2 regressed a pre-ADR-0171 cursor into permanent lost
   compensation. Found independently by all three lenses. → the
   `len(cur.Records) > 0` conjunct.
2. **High** — the abandoned-walk Consequence was false; the finish-only window
   halved the double-run rather than closing it. → incremental consumption.
3. **High** — a third teardown route existed, on the call site the ADR called
   impossible. → Context, and its own test.
4. **High** — the test plan was substantially defective: two prescribed tests were
   vacuous, one probe could not fail, two of eight mutations were inert.
5. **Medium** — two predicate conjuncts were provably non-discriminating. → removed.

Full record: [`docs/specs/2026-08-11-adr-0173-audit-evidence.md`](../specs/2026-08-11-adr-0173-audit-evidence.md);
adjudication in the spec's §8.

## Corrections made during implementation

Per the repo's rule that implementation is expected to correct design, and that the
correction belongs in the ADR rather than the transcript:

1. **Decision 3 no longer stamps `StartRecordCount` on a targeted cursor.**
   Consuming the slot incrementally removed the need for it — the finish-time
   prefix trim was the only thing that wanted an offset — so that field's
   "Zero for every other walk (targeted throw, …)" documentation stays TRUE, and
   one of the two doc amendments this ADR promised was not needed.
2. **A proposed `finishPlan.validate()` invariant was refuted on contact.**
   Asserting that `archiveWindowKey` and `archiveWindowCount` are set together
   panicked the suite immediately: the USUAL finish carries a non-empty key with a
   ZERO count, because the window is emptied as the walk dispatches and only the
   key survives to the finish. Only the one-directional invariant
   (count without key) is real.
3. **The teardown window is read from PERSISTED state, so it is sanitized at the
   cursor read.** `InstanceState` round-trips as whole-struct JSON and
   `Compensating` is an exported field, so a corrupt row can carry a negative
   `StartRecordCount` or a window count with no key. Both were measured panicking
   inside the pure engine core — i.e. in the consumer's process — and `main`
   absorbs both, so each was a regression. `partitionForLiveWalk` now clamps into
   `[0, len(records)]` as `clearRecordsPrefix` already did for the same field, and
   `normalizeTeardownWindow` drops a malformed window before it becomes a
   `finishPlan` field, which is what keeps `validate()`'s licence to panic
   ("never from persisted or external input") true.
4. **Two of the ten prescribed mutations initially caught nothing** — the liveness
   conjunct and the finish's residual-window removal. Neither was deleted on the
   strength of a green suite: both are reachable only if an invariant elsewhere
   changes, so both are now covered by direct white-box tests, and 10/10 mutations
   turn the suite RED. Detail in the spec's §6.

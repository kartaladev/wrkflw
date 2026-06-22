# 30. Reachability + conservative parallel-join pairing in model.Validate

- Status: Accepted
- Date: 2026-06-22

## Context

`model.Validate` checks a process definition's structural soundness at load time and returns a
joined error of *all* violations. ADR-0014 added the mixed split+join gateway rule and explicitly
deferred two harder structural checks as a follow-up:

> "A full solution would pair every converging join with a matching diverging fork via reachability
> analysis and check that branch conditions appear only on diverging flows. That is a substantial
> analysis with real false-positive risk on legitimate loop and multi-merge patterns."

Condition/default placement is already validated (`ErrConditionNotAllowed`/`ErrDefaultNotAllowed`),
and dead-ends (no outgoing flow) by `ErrDeadEnd`. The two genuinely-missing checks are:

1. **Reachability** — nothing rejects a node unreachable from the start event (dead/orphan structure).
2. **Fork-join pairing** — a **parallel join** waits for a token on *every* incoming flow; if its
   branches are never concurrently activated it deadlocks at runtime (token parks forever).

The dominant constraint is that `Validate` runs at definition **load**: a false positive rejects a
consumer's *valid* definition. ADR-0014 deferred pairing precisely because a naïve analysis
false-positives on loops and multi-merge. `model` also must not import `engine` (one-way dependency),
so the engine's existing `nodesThatCanReach` helper cannot be reused directly.

## Decision

Add two sentinels to `model.Validate` (inside the recursive `validate()` worker, so both recurse
into sub-processes via the existing traversal + cycle guard), using **model-local pure forward-BFS**
helpers — no engine import, no new model fields. Both rules are **false-positive-averse**: when in
doubt, do not flag (bias toward false-negatives).

### Rule A — `ErrUnreachableNode`

Runs only when the definition has exactly one start event (else the start-count error already fires).
A node is reachable if forward-reachable from the start, **or** it is a boundary event whose host is
reachable (seeded to a fixpoint, since a boundary branch may host another activity-with-boundary),
**or** it is an event-sub-process (an event-triggered root with no incoming flow). Any other node not
in the reachable set is flagged. These are the only node kinds legitimately lacking an incoming
sequence flow, so a flagged node is genuinely orphaned.

### Rule B — `ErrUnpairedJoin` (conservative, parallel-join only)

Only **parallel joins** (`KindParallelGateway`, >1 incoming, 1 outgoing) can deadlock: they wait for
all incoming branches unconditionally. Exclusive and event-based joins fire on first arrival;
inclusive joins self-adjust (the engine fires them once no outside token can reach them, via runtime
reachability) — none deadlock, so checking them would only produce false positives. They are
**excluded**.

A parallel join `J` is flagged iff there is **no** concurrency source for it: no split `F` (`F ≠ J`,
parallel or inclusive, >1 outgoing) has ≥2 distinct outgoing branches whose targets can each
forward-reach `J`. Only parallel/inclusive splits create concurrency; exclusive/event-based splits
pick one branch. This is maximally lenient on acceptance (any plausible fork clears the join) and
flags only a provable deadlock (no fork can ever deliver two concurrent tokens toward J). Unreachable
joins are skipped (Rule A already reports them).

## Consequences

**Positive**

- Catches the two most impactful remaining structural mistakes: orphan/dead nodes and the classic
  `exclusive-split → parallel-join` deadlock — at load time, with a clear node-identified error.
- `model` stays engine-free; no new model fields; rules recurse into sub-processes for free.
- The conservative bias means legitimate concurrent, nested, and looped models are not rejected.

**Negative / trade-offs**

- **`Validate` is now stricter**: a previously-accepted definition with an orphan node or a
  deadlocking parallel join is now rejected at load (the intended correctness nudge, consistent with
  ADR-0014 making `ErrMixedGateway` always-on). The repo's own fixtures are verified to still pass.
- **Incomplete by design (false-negatives):** the pairing rule misses *conditional* deadlocks
  (e.g. an exclusive choice inside a parallel region that can starve a join branch) and does not
  attempt full structured-soundness/SESE analysis. This is the deliberate price of zero false
  positives. Full soundness remains a deferred follow-up.
- **Inclusive/exclusive join pairing not checked.** Justified: those join kinds cannot deadlock. If a
  future engine change made inclusive-join firing non-adaptive, this decision would need revisiting.

**Neutral**

- `ErrUnreachableNode` does not double-report nodes wired only by a dangling flow (already covered by
  `ErrDanglingFlow`); it simply won't reach them.
- No-path-to-end / sink detection and condition-satisfiability were considered and deferred
  (higher false-positive risk; lower marginal value than reachability + parallel-join pairing).

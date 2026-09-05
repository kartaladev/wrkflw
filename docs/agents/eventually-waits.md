# Waiting in tests: wait for what you assert

A test that waits for one signal and then asserts on another is racy whenever the
two are written by different code at different times. The wait is satisfied while
the asserted state is still unwritten, and the assertion reads a value that has
not arrived yet.

`scripts/check-test-timeout.sh` cannot see this class. A budget guard asks **"did
you wait long enough?"**; it cannot ask **"did you wait for the right thing?"**
Every site catalogued below used `eventuallyBudget` (or an explicit budget)
correctly and passed that script.

## The rule

> **An `Eventually` must poll every fact the assertions after it depend on.**

Fold the conditions into ONE `Eventually` as a conjunction:

```go
require.Eventually(t, func() bool {
    return countFor(reader, runsTotal) >= 1 &&
        histogramCountFor(reader, duration) >= 1
}, eventuallyBudget, 5*time.Millisecond, "both instruments must record the run")
```

Three corollaries:

1. **Do not gate on whichever signal happens to be written last.** It works
   today and encodes the production write order as a test dependency; reordering
   two lines in the production file brings the flake back. The `&&` form does
   not.
2. **Where two near-identical rows carry the same shape, extract one helper.**
   In #80 the duplication is precisely how the second row inherited the first's
   defect.
3. **Never follow a wait with a non-blocking receive that fails on `default`.**
   That is the shape with zero tolerance — see `nonNilCh` below. If the value is
   worth asserting on, it is the value to wait for; carry it out of the
   `Eventually` closure instead.

## Proving one, either way

`-race` does not reproduce these. #80's confirmed defect survived `-race
-count=2`; the window is narrow enough to need CI load. Anyone hammering `-race`
will wrongly conclude a site is safe, which is exactly how #80 survived in the
tree.

The method that works: **insert a delay between the two production writes**, run,
and see whether the test goes red. Then fix, re-apply the same delay, and see it
stay green. Remove the delay before committing. A site that stays green with the
window widened is not an instance — record why, in a comment at the site, so the
next sweep does not re-open it.

## Ledger

The #86 sweep classified 7 of the repo's 70 `Eventually` sites as matching the
shape. Reproduction (per the method above) resolved them as follows.

| Site | Verdict | Why |
| --- | --- | --- |
| `scheduler/internal/gocron/monitor_test.go` (#80) | **defect, fixed in #80** | `job_runs_total` and `job_duration_seconds` are two `Add`/`Record` calls; a poll landing between them satisfied the wait and read an empty histogram. |
| `scheduler/internal/gocron/job_schedule_test.go`, `nonNilCh` row | **defect, fixed in #86** | Waited on `fired`, which `fired.Add(1)` writes BEFORE the channel send, then took a non-blocking receive whose `default` branch calls `t.Fatal`. A 200 ms sleep between the two writes made it fail every run. |
| `scheduler/internal/gocron/trigger_test.go`, one-shot disarm | not an instance | A `fired >= 1` wait a few lines up already establishes the counter the trailing assertion reads, and a one-shot cannot fire twice. Widening the window keeps it green; deleting that earlier wait reproduces the predicted failure. |
| `persistence/chaining_e2e_test.go` ×3 (`inst-a`, `inst-b`, `inst-a-idem`) | not an instance; refactored anyway | `Chainer.Handle` records the chain link BEFORE `Drive`, and a start→end definition is created already-completed in a single `Store.Create` — so the waited-for row implies both asserted facts. Both are unstated production orderings: moving `Handle`'s `Record` block below its `Drive` call failed all three scenarios with no test change. Now folded into one `awaitChainedSuccessor` helper that waits for the conjunction, and the same reorder passes. |
| `internal/authz/casbin/policy_reload_test.go` | not an instance | The waited-for message and the asserted `node_id` are two attributes of ONE `slog.Record`. `TextHandler` issues one `Write` per record and `syncBuffer` holds the mutex for it. |
| `runtime/processdriver_scheduler_e2e_test.go` | not an instance | Wait and assertion read one atomically-loaded snapshot. |
| `firstFired`/`secondFired` row | not an instance | The follow-up assertion is negative, and a negative cannot be "not arrived yet". |

## Can this be guarded mechanically?

**Not in general — no check is worth shipping, and this document is the durable
output.**

The syntactic tell is cheap to compute: an `Eventually` whose condition reads
identifier set A, immediately followed by an assertion reading identifier set B,
with A ∩ B = ∅. It is also almost entirely false positives. Of the seven sites
above, **five** are disjoint-identifier sites and **four of those five are
correct** — correct because production code orders the writes (`Record` before
`Drive`), or because one write carries both facts (one `slog.Record`, one
`Store.Create`, one atomic snapshot), or because an earlier wait already
established the second signal. Separating those from the real defects needs a
happens-before model spanning the production code, across a database and a log
handler. That is whole-program concurrency analysis, and it is not available
here.

One narrow sub-shape *is* precisely checkable: **a non-blocking `select` whose
`default` branch calls `t.Fatal`/`t.Error`, following a wait that does not read
that channel.** A non-blocking receive is only ever correct when the send is
ordered before the wait's own condition, which is rare and always worth stating
explicitly. That is the `nonNilCh` defect, and it is greppable.

It is deliberately not shipped as a CI check. After the fix above the repo has
zero matching sites, so the check would pass vacuously from the day it landed —
the class of guard this repo has shipped before and regretted (see the header of
`scripts/check-test-timeout.sh`). Rule 3 above carries the same information at
review time, where a human is already reading the diff. If a future sweep finds
the sub-shape recurring, revisit — a check that has ever caught something is a
different proposition from one that never has.

## Related

- #80 — the first confirmed instance and the fix shape.
- #66 — raw `time.After` deadlines outside the budget guard. Adjacent and
  disjoint; neither ticket absorbs the other.
- `scripts/check-test-timeout.sh` — the budget guard, and its own statement of
  what it does not cover.

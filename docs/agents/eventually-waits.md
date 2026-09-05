# Waiting in tests: wait for what you assert

A test that waits for one signal and then asserts on another is racy whenever the
two are written by different code at different times. The wait is satisfied while
the asserted state is still unwritten, and the assertion reads a value that has
not arrived yet.

`scripts/check-test-timeout.sh` cannot see this class. A budget guard asks **"did
you wait long enough?"**; it cannot ask **"did you wait for the right thing?"**
Every site below that the script counts at all used `eventuallyBudget` correctly
and passed it. (The `persistence` sites below are not counted: they pass explicit
literal budgets, and the script finds Eventually sites by grepping the
`eventuallyBudget` identifier. Its header calls a bare literal "a separate review
problem"; that debt is real and still open. ⚠ This paragraph previously said the
script "scans only packages carrying a `waitbudget_test.go`" and attributed the
debt to #66 — both went stale when #66 landed: the script now walks every package
with test files, and #66 covered the raw-`time.After` gap without touching literal
budgets. The verdict was right and the mechanism wrong, which is the pattern this
document's own ledger flags twice.)

## The rule

> **An `Eventually` must poll every fact the assertions after it depend on.**

Fold the conditions into ONE `Eventually` as a conjunction:

```go
require.Eventually(t, func() bool {
    return countFor(reader, runsTotal) >= 1 &&
        histogramCountFor(reader, duration) >= 1
}, eventuallyBudget, 5*time.Millisecond, "both instruments must record the run")
```

Four corollaries:

1. **Do not gate on whichever signal happens to be written last.** It works
   today and encodes the production write order as a test dependency; reordering
   two lines in the production file brings the flake back. The `&&` form does
   not.
2. **Where two near-identical rows carry the same shape, extract one helper.**
   In #80 the duplication is precisely how the second row inherited the first's
   defect.
3. **Never follow a wait with a non-blocking receive that fails on `default`.**
   That is the shape with zero tolerance — see the `nonNilCh` row below. If the
   value is worth asserting on, it is the value to wait for; carry it out of the
   `Eventually` closure instead.
4. **A conjunction is not always the shape.** When the facts arrive on a channel,
   two receives inside one condition would consume what a later tick needs. Send
   one value carrying everything the case asserts on, and let the condition
   consume that.

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

⚠ **The delay is the whole method; a reordering alone is not a reproduction.**
When the two writes are ordered by production code, it is tempting to demonstrate
the dependency by swapping them and running. That under-reproduces for the same
reason `-race` does: swapping `Chainer.Handle`'s link `Record` below its `Drive`
call leaves a sub-millisecond window against a 20 ms poll, and the pre-fix
chaining tests **pass** — on all three dialects. Insert 200 ms into that same
window and they fail every scenario on every dialect. An earlier draft of this
document stated the swap alone as measured; it was not, and a recipe that
silently under-reproduces is the precise failure this section exists to prevent.

## Ledger

One row per site, so the next sweep can check each one off. "In #86" marks the
seven sites #86's sweep flagged; the other two are #80's original defect and one
site with the same shape that the sweep did not list.

| Site | In #86 | Verdict | Basis |
| --- | --- | --- | --- |
| `scheduler/internal/gocron/monitor_test.go`, `TestGocronScheduler_MonitorStatus` | no (#80) | **defect, fixed in #80** | Reproduced in #80: `job_runs_total` and `job_duration_seconds` are two calls; a poll landing between them satisfied the wait and read an empty histogram. |
| `scheduler/internal/gocron/job_schedule_test.go`, `TestGocronScheduler_ScheduleJob`, `nonNilCh` row | yes (closer read) | **defect, fixed in #86** | Reproduced. Waited on `fired`, which `fired.Add(1)` writes BEFORE the channel send, then took a non-blocking receive whose `default` calls `t.Fatal`. 200 ms between the two writes fails it every run; green over `-count=5` after the fix with the same delay. |
| `scheduler/internal/gocron/trigger_test.go`, `TestGocronNativeTriggers`, one-shot disarm | yes (genuine) | not an instance | Reproduced both ways. A `fired >= 1` wait a few lines up already establishes the counter the trailing assertion reads, and a one-shot cannot fire twice. 300 ms before `fired.Add(1)` stays green over `-count=5`; the same delay with that earlier wait deleted fails `expected: 1, actual: 0`. The sweep read the site without the line above it. |
| `persistence/chaining_e2e_test.go`, `happy_path_vars_carry` (`inst-a`) | yes (genuine) | not an instance; refactored | Reproduced. `Chainer.Handle` records the link BEFORE `Drive`, and a start→end definition is created already-completed in a single `Store.Create` (instrumented: one commit, `status=completed`), so the waited-for row implies both asserted facts. 200 ms after the create-commit changes nothing. |
| `persistence/chaining_e2e_test.go`, `branch_routing` (`inst-b`) | yes (genuine) | not an instance; refactored | As above. |
| `persistence/chaining_e2e_test.go`, `idempotency` (`inst-a-idem`) | no | not an instance; refactored | As above. Not in the sweep's list, but it carried the same dependency, not merely the same shape: under the reorder-plus-delay it failed on both "chain link must exist for the successor" and "exactly one chain link must be recorded". |
| `internal/authz/casbin/policy_reload_test.go`, `TestNewDBEnforcer_WatcherReloadFailureIsObservable` | yes (closer read) | not an instance | Read from source. The waited-for message and the asserted `node_id` are two attributes of ONE `slog.Record` (`db.go`'s single `ErrorContext` call); `TextHandler` issues one `Write` per record and `syncBuffer` holds the mutex for it. Two records would be an instance; one is not. (#86 cites `:710`; the file is 218 lines and the site is `:187`.) |
| `runtime/processdriver_scheduler_e2e_test.go`, `TestProcessDriverSchedulerE2E` | yes (safe) | not an instance — **inherited from #86, not independently reproduced** | ⚠ #86's stated reason is wrong: the wait does its own `store.Load`, and the assertions read a **second** `store.Load` below it, so this is not "one atomically-loaded snapshot". It is safe because `StatusCompleted` is TERMINAL — nothing writes the instance after it — so the second read cannot observe an earlier state. Applying #86's reason to a non-terminal status would be a mistake. (#86 cites `:261`; the file is 121 lines and the site is `:112`.) |
| `scheduler/internal/gocron/job_schedule_test.go`, `firstFired`/`secondFired` row | yes (safe) | not an instance — **inherited from #86, not independently reproduced** | #86's reason ("the follow-up assertion is a negative one") covers only half of it: the row also asserts `secondFired == 1` positively. That half is safe for the same reason as the `trigger_test.go` row above — the `secondFired >= 1` wait immediately above established it. |

## Can this be guarded mechanically?

**No — no check here is worth shipping, and this document is the durable output.**

The syntactic tell looks cheap: an `Eventually` whose condition reads identifier
set A, immediately followed by an assertion reading identifier set B, with
A ∩ B = ∅. Run it against the ledger and it fails in both directions.

**It over-fires.** Three rows above are identifier-disjoint on their face — the
`nonNilCh` defect, the `trigger_test.go` row, and `branch_routing` — and two of
the three are correct: correct because an earlier wait already established the
second signal, or because production code orders the writes (`Record` before
`Drive`). That count is already generous to the check, because it assumes the
checker traces an assignment back to its source. A flow-insensitive one fares
worse and flags `happy_path_vars_carry` as well, whose assertions read `succSt`
rather than the `d.store` its wait polls. Separating true from false here needs a
happens-before model spanning a database and a log handler. That is whole-program
concurrency analysis, and it is not available here.

**It also under-fires, on the one defect that started this.** #80's confirmed
instance reads `reader` on both sides of the wait — `sumFor(reader, …)` then
`histogramCountFor(reader, …)` — so it is not identifier-disjoint and the check
never flags it. The signals that differ are the two instruments, which are
arguments, not identifiers. A check that misses the case it was written for is
worse than no check, because a green run now reads as evidence.

One narrower sub-shape *is* precisely checkable: **a non-blocking `select` whose
`default` branch calls `t.Fatal`/`t.Error`, following a wait that does not read
that channel.** A non-blocking receive is only ever correct when the send is
ordered before the wait's own condition, which is rare and always worth stating
explicitly. That is the `nonNilCh` defect, and it is greppable.

It is deliberately not shipped either. After the fix the repo has zero matching
sites, so it would pass vacuously from the day it landed — the class of guard
this repo has shipped before and regretted (see the header of
`scripts/check-test-timeout.sh`). Corollary 3 carries the same information at
review time, where a human is already reading the diff. If a future sweep finds
the sub-shape recurring, revisit: a check that has ever caught something is a
different proposition from one that never has.

## Related

- #80 — the first confirmed instance and the fix shape.
- #66 — raw `time.After` deadlines outside the budget guard. Adjacent and
  disjoint; neither ticket absorbs the other. Its output is
  `docs/agents/test-deadlines.md`, which classifies the raw-deadline family the
  way this document classifies waits. The literal-budget debt is in neither: see
  that document's "What this ticket did not settle".
- `scripts/check-test-timeout.sh` — the budget guard, and its own statement of
  what it does not cover.

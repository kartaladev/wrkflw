# fix-runtime-wave1 — runtime defect fixes, wave 1

Evidence log for the `runtime/` items of the post-audit sweep. One section per
item, written the moment that item went green. Every RED text below is pasted
from an observed `go test` run, judged by its exit code.

---

## Item 87 — cancel propagation orphans the whole descendant subtree — **DONE**

### Files changed

- `runtime/processdriver.go` — new `casRetryAttempts` const and
  `(*ProcessDriver).applyTriggerRetryingCAS`, the bounded CAS-retry wrapper
  around `applyTrigger`.
- `runtime/timerops.go` — `timerFireFunc` now delegates its retry loop to the
  shared helper. Both of its log lines are preserved verbatim: it distinguishes
  "gave up on conflicts" from "failed for another reason" by `errors.Is` on the
  returned error.
- `runtime/processdriver_cancel.go` — the parent cancel in `CancelInstance` and
  the child cancel in `propagateCancel` both use the shared helper. Doc comments
  updated to state the retry and why a cancel needs it.
- `runtime/cancel_cas_retry_test.go` — new
  `TestCancelPropagationRetriesChildCASConflict` + `instanceCASConflictStore`.

The triage fix sketch (share `timerFireFunc`'s CAS-retry loop with
`propagateCancel`) was **verified against the source and adopted as written** —
the two loops share cleanly. The only wrinkle is that `timerFireFunc` needs to
tell an exhausted-retry error from an ordinary one to pick its log line; the
helper returns the last `ErrConcurrentUpdate` on exhaustion, so `errors.Is`
answers that without an extra return value.

The `continue` on a **non-CAS** child error is deliberately left alone: the
triage classifies reversing it as a separate `D` needing an ADR, and the code
comment at `processdriver_cancel.go` documents it as a choice.

### What makes the test fail today

`applyTrigger` is a single `store.Load` + `deliverLoop`, so
`kernel.ErrConcurrentUpdate` propagates straight out of the child cancel. It is
not `engine.ErrCancelNotApplicable`, so `propagateCancel` logs a WARN and
`continue`s — skipping the recursion. The child and the grandchild both stay
`StatusRunning` while `CancelInstance` returns `err=nil`. The **grandchild**
assertion is what makes a one-level fix insufficient.

### Observed RED

```
EXIT=1
=== RUN   TestCancelPropagationRetriesChildCASConflict
2026/08/20 15:00:04 WARN runtime: propagateCancel: cancel child instance failed child_id=cascas-c1 error="workflow-runtime: commit: workflow-runtime: concurrent update"
    cancel_cas_retry_test.go:113:
        	Error:      	Not equal:
        	            	expected: 4
        	            	actual  : 0
        	Messages:   	the child's cancel must be RETRIED past the single CAS conflict
    cancel_cas_retry_test.go:115:
        	Error:      	Not equal:
        	            	expected: 4
        	            	actual  : 0
        	Messages:   	the grandchild must terminate too: a retry that does not restore the recursion still orphans the subtree
--- FAIL: TestCancelPropagationRetriesChildCASConflict (0.00s)
FAIL	github.com/kartaladev/wrkflw/runtime	0.611s
```

(`4` is `engine.StatusTerminated`, `0` is `engine.StatusRunning`. The parent
assertion passed in RED — only the descendants were orphaned, exactly as the
triage measured: `err=<nil>`, parent terminated, child AND grandchild running.)

### Observed GREEN

```
EXIT=0
=== RUN   TestCancelPropagationRetriesChildCASConflict
--- PASS: TestCancelPropagationRetriesChildCASConflict (0.00s)
ok  	github.com/kartaladev/wrkflw/runtime	0.593s
```

### Premises checked

- Triage's "`ListRunningChildren` has exactly one non-test caller" — re-derived,
  still **true**: `runtime/processdriver_cancel.go` is the only one (the other
  hits are the interface decl, the two implementations, and two doc comments).
  So nothing revisits an orphaned subtree, which is why the retry belongs here.
- Triage's "the only CAS-retry loop in `runtime/` is in `timerFireFunc`" —
  **true** before this change; there are now two call sites of one shared loop.
- Re-delivery safety: `deliverLoop` performs side effects only **after** the
  commit, so a conflicting attempt persisted nothing and dispatched nothing.
  Retrying re-Loads, so the retry applies to the winner's state.

---

## Item 89 — a foreign scheduler that omits `Location()` silently persists wrong `NextRun`s — **DONE**

### Files changed

- `runtime/timerops.go` — `schedulingLocation` kept pure and unchanged in
  signature (its existing internal test still covers it); new
  `reportedSchedulingLocation` returning `(loc, reported)`; new
  `schedulingNow(ctx)` = the location-rendered clock read **plus** a
  `sync.Once`-guarded WARN when the answer is the UTC fallback. `timerJobsFor`
  now reads the scheduling clock **lazily** so a step with no `ScheduleTimer`
  never consults the location; `buildTimerJob` gained a `ctx` parameter.
- `runtime/processdriver.go` — new `schedLocWarnOnce sync.Once` field; the
  in-transaction arm site uses `schedulingNow(txCtx)`.
- `runtime/jobstore.go` — passes its existing `ctx` into `buildTimerJob`.
- `runtime/timerops_location_warn_test.go` — new
  `TestSchedulerWithoutLocationCapabilityWarnsOnce`.

### Design choices worth stating

- **Warned at first arm, not at construction.** Construction-time warning would
  fire for every driver that never arms a timer and so never relies on the
  fallback. `sync.Once` keeps it to one line per driver, which is what makes it
  safe to emit on a hot path at all. The test pins both directions: zero lines
  after construction, exactly one after two armed timers.
- **WARN, not a hard error**, as instructed: UTC is the only safe default for a
  scheduler that reports nothing, and refusing to arm would break every consumer
  running a foreign scheduler today. The defect was the silence, not the default.
- The capability was **not** exported. Doing so is a public-API change; the
  warning closes the discoverability gap without one.

### What makes the test fail today

`schedulingLocation`'s fallback contains no log statement of any level, so the
buffer holds zero matching records however many timers are armed.

### Observed RED

```
EXIT=1
=== RUN   TestSchedulerWithoutLocationCapabilityWarnsOnce
    timerops_location_warn_test.go:95:
        	Error:      	"[]" should have 1 item(s), but has 0
        	Messages:   	arming a timer under a location-less scheduler must warn exactly once
--- FAIL: TestSchedulerWithoutLocationCapabilityWarnsOnce (0.00s)
FAIL	github.com/kartaladev/wrkflw/runtime	0.667s
```

The fixture assertions all passed inside that RED — the double really lacks
`Location()`, and the instance really parked at an armed timer — so the failure
is the missing warning and nothing else.

### Observed GREEN

```
EXIT=0
--- PASS: TestSchedulerWithoutLocationCapabilityWarnsOnce (0.00s)
ok  	github.com/kartaladev/wrkflw/runtime	0.604s
```

### Premises checked

- The backlog's ⚠ correction is **confirmed**: `NativeScheduler.Location` IS
  exported, and `jobStore.Save`'s job-implementation assertion returns a typed
  error rather than falling back silently. Neither was touched. Only the UTC
  fallback was real, and only it was fixed.

---

## Item 116 — `runtime/monitor`'s collector options are unreachable from outside the module — **DONE**

### Files changed

- `runtime/monitor/options.go` (new) — package-owned `Option`, `WithLogger`,
  `WithTracerProvider`, `WithMeterProvider` (+ `WithClock`, added under item
  107), with the internal option list held in an unexported `collectorConfig`.
  Mirrors `runtime/calllink`.
- `runtime/monitor/stats_collector.go` — both constructors now take
  `...Option`.
- `runtime/monitor/stats_collector_test.go` — the two in-module call sites
  repointed at `monitor.WithMeterProvider`; the `internal/observability` import
  is gone from the test package entirely.
- `runtime/monitor/internal_leak_test.go` (new) —
  `TestNoExportedSignatureNamesAnInternalType`, a module-wide AST guard.

No caller outside `runtime/` needed changing: the only Go call sites in the repo
were those two test lines. (`docs/observability.md` names these constructors
wrongly already; that is item 108's, and is owned elsewhere.)

### ⚠ Premise found FALSE — the triage undercounted

Triage entry 116 states: *"Exactly two exported symbols in the whole module leak
an `internal/` type into a public signature, and both are in `runtime/monitor`."*
**That is false.** There is a third, of the same class:

```
persistence/scheduler_locker.go:39  func NewSchedulerLocker(dl dialect.Locker) scheduler.Locker
```

`dialect.Locker` is `github.com/kartaladev/wrkflw/internal/persistence/dialect`,
so no consumer can name an argument for it — and the doc comment on that very
function invites the consumer to *"bring your own concurrency-safe
[dialect.Locker]"*, which is not possible.

The claim was generalised from a grep whose pattern only ever matched
`observability.` — the measurement was sound, the sentence summarising it was
not. This is the Premise Discipline quantifier failure exactly.

It is **outside `runtime/`** and was not fixed here. It is recorded in the
guard's `knownOpenInternalLeaks` map, which is **self-cleaning**: an entry that
stops matching any offender fails the test, so a stale exemption cannot linger
and shelter a future leak.

### What makes the test fail today

An in-module test cannot fail for this — in-module code may import `internal/`,
which is exactly why the leak shipped and why `stats_collector_test.go` has been
passing `observability.WithMeterProvider(mp)` for its whole life. The guard is
therefore structural: it derives the offender set from the module's own sources
via `go/ast` and requires it to be empty. No count is hard-coded and no mutation
is needed — it fails on today's sources.

### Observed RED

Before the allowlist (the run that refuted the triage's count):

```
EXIT=1
    Error: Should be empty, but was [persistence/scheduler_locker.go:39 NewSchedulerLocker names dialect.Locker (github.com/kartaladev/wrkflw/internal/persistence/dialect) runtime/monitor/stats_collector.go:36 NewOutboxStatsCollector names observability.Option (github.com/kartaladev/wrkflw/internal/observability) runtime/monitor/stats_collector.go:116 NewTimerStatsCollector names observability.Option (github.com/kartaladev/wrkflw/internal/observability)]
--- FAIL: TestNoExportedSignatureNamesAnInternalType (0.04s)
```

After scoping the out-of-scope offender into the self-cleaning allowlist, i.e.
the RED this change is actually against:

```
EXIT=1
    Error: Should be empty, but was [runtime/monitor/stats_collector.go:36 NewOutboxStatsCollector names observability.Option (github.com/kartaladev/wrkflw/internal/observability) runtime/monitor/stats_collector.go:116 NewTimerStatsCollector names observability.Option (github.com/kartaladev/wrkflw/internal/observability)]
--- FAIL: TestNoExportedSignatureNamesAnInternalType (0.06s)
FAIL	github.com/kartaladev/wrkflw/runtime/monitor	3.348s
```

### Observed GREEN

```
EXIT=0
--- PASS: TestNoExportedSignatureNamesAnInternalType (0.03s)
ok  	github.com/kartaladev/wrkflw/runtime/monitor	0.549s
```

---

## Item 107 — timer lateness is not measured — **DONE**

### Files changed

- `runtime/monitor/stats_collector.go` — `TimerStatsCollector` gained a
  `clockwork.Clock`; new `overdueSeconds` helper; a second gauge
  `wrkflw_timers_next_fire_age_seconds` registered and observed from the
  `NextFireAt` the callback already had in hand. No new query.
- `runtime/monitor/options.go` — `WithClock` (ADR-0138: an outer stateful layer
  depends on `clockwork.Clock` directly).
- `runtime/monitor/timer_overdue_test.go` (new) —
  `TestTimerStatsCollectorReportsOverdueAge` (4 cases) and
  `TestTimerStatsCollectorOverdueAgeReaderError`.

### ⚠ The backlog wording is wrong and was NOT repeated

Lateness **is** computed once today — in the scheduler's gocron adapter — but
only for a one-shot **already past due at arm time**, only above the skew
tolerance, and only as a WARN. A recurring timer, and a one-shot that goes
overdue *after* arming (the stalled-scheduler case), never reach it. Neither the
new code comments nor this record says "lateness is not measured anywhere", and
`scheduler/` was not touched.

### Clamping — three cases, all of them real

`overdueSeconds` clamps to zero for nil `NextFireAt` (empty table), the zero time
(a legitimately stored `0001-01-01`, ADR-0181), and any future instant (a
*healthy* timer). A naive `now.Sub(*NextFireAt)` reports a ~2000-year age for the
second and a negative age for the third — i.e. it would be wrong in the normal
case, not just an edge case. Each is a table row.

### What makes the tests fail today

`NewTimerStatsCollector` registered exactly one instrument
(`wrkflw_timers_armed`) and its callback observed only `stats.Armed`, so no
metric by the new name exists to be found. That is the triage's measured
"1 instrument" result turned into an assertion — real RED, no mutation.

### Observed RED

First a compile RED (`undefined: monitor.WithClock`), then, with `WithClock`
added but no gauge, the assertion RED:

```
EXIT=1
    --- FAIL: TestTimerStatsCollectorReportsOverdueAge/an_overdue_next_fire_reports_its_exact_age_in_seconds
        	Error:      	Should be true
        	Messages:   	wrkflw_timers_next_fire_age_seconds must be present
    --- FAIL: TestTimerStatsCollectorReportsOverdueAge/a_nil_next_fire_(empty_table)_reports_zero,_not_an_epoch_age
    --- FAIL: TestTimerStatsCollectorReportsOverdueAge/a_zero_next_fire_(ADR-0181_stored_row)_reports_zero,_not_~2000_years
    --- FAIL: TestTimerStatsCollectorReportsOverdueAge/a_future_next_fire_is_not_overdue_and_must_not_go_negative
FAIL	github.com/kartaladev/wrkflw/runtime/monitor	0.612s
```

### Observed GREEN

```
EXIT=0
ok  	github.com/kartaladev/wrkflw/runtime/monitor	0.638s
```

### Mutation verification of the clock seam

The stated trap is that `clockwork.NewFakeClock()` seeds from wall time, so an
age test built on it can pass against production code that never consults the
injected clock. Every case here pins `clockwork.NewFakeClockAt(2031-03-04T12:00Z)`
and asserts an exact duration, so the seam is load-bearing by construction — and
that was executed, not argued:

- mutate `c.clk.Now()` → `time.Now()`: `MUTANT_EXIT=1` (RED, as required)
- restore from a `cp` backup: `diff` clean, byte-identical

---

## Final verification (all four items)

```
go test -count=1 -timeout 900s ./runtime/...   → EXIT=0   (all 10 packages ok)
go build ./...                                 → EXIT=0
go vet ./...                                   → EXIT=0
golangci-lint run ./runtime/...                → EXIT=0, 0 issues
```

⚠ The lint run is **package-scoped to `./runtime/...`**, not the repo-wide
`golangci-lint run ./...` the Verification section requires. That is a partial
result and is labelled as one; the repo-wide run belongs to the controller's
delivery gate, and could not have been meaningfully attributed here anyway while
three other packages were mid-flight.

### ⚠ A 10-minute hang that was NOT ours, and how that was established

An earlier `go test ./runtime/` run hung and was killed at the 10-minute test
timeout, its dump pointing at
`TestGocronSchedulerDrivesRunnerToCompletion` blocked in
`clockwork.FakeClock.BlockUntilContext` waiting for gocron to register a waiter.

Arguing that a `runtime/` change cannot affect gocron's waiter registration would
have been a guess. It was measured instead: a **detached worktree at HEAD** was
created, only this delivery's `runtime/` files were copied into it, and the test
was run there against a **pristine `scheduler/`**:

```
EXIT=0   --- PASS: TestGocronSchedulerDrivesRunnerToCompletion (0.00s)
```

0.00 s against 10 minutes. The hang belonged to another agent's uncommitted
`scheduler/internal/gocron/{job_schedule,trigger}.go` edits, which were live in
the shared tree at the time. The same worktree then produced the clean
`./runtime/...`, `go build ./...` and lint results above, and both trees were
re-verified green afterwards. The worktree was removed.

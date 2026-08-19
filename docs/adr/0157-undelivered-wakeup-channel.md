# 157. Undelivered-wakeup channel for wake-ups that permanently fail

- Status: ⚠ Proposed — AUDIT FAILED, revision required (see docs/plans/2026-07-29-audit-findings.md)
- Date: 2026-07-28

> ⚠⚠ **NOT IMPLEMENTED, NOT ACCEPTED.** This ADR is on `main` to reserve its number and to
> preserve the design — it lives nowhere else. It **failed its audit** and needs revision before
> anyone builds on it; the findings are in
> [`docs/plans/2026-07-29-audit-findings.md`](../plans/2026-07-29-audit-findings.md).
> Do **not** treat it as a decision of record. Imported 2026-08-19 from the parked branch
> `feat/durable-waiters-delivery-correctness`, which was deleted afterwards.


## Context

ADR-0155 and ADR-0156 close the paths on which a wake-up is *dropped*: a
restart-emptied cache, a per-replica blind spot, an overwritten waiter. They leave
open the path on which a wake-up **permanently fails**.

`SignalBus.Publish` accumulates per-recipient errors and returns them joined. That
is a correct library contract, but it is the caller's only record. A consumer who
ignores the returned error — or logs it and moves on — loses the wake-up with no
trace, and the instance stays parked forever. That is the same observable
end-state as the bugs this bundle exists to eliminate, reached by a different
route.

Two changes make this materially more likely than it was.

`ErrConcurrentUpdate` was previously a rare loser in a single-replica race, and
the broadcast path did not even retry it — `SignalBus.Publish` called `deliver`
exactly once, while only `timerFireFunc` carried the bounded CAS-retry loop. Under
ADR-0155's multi-replica delivery, two replicas publishing against one instance
makes CAS conflict a routine outcome, and CAS **exhaustion** a realistic terminal
one.

ADR-0156's fan-out default multiplies the exposure: one publish now attempts N
deliveries, each independently capable of failing permanently.

### Why this is not called a dead-letter queue

The repository already has one. `monitor.DeadLetter` (`runtime/monitor/dlq.go`)
and the `service.DeadLetterAdmin` port describe **outbound** failures: outbox rows
the relay could not publish to the broker, quarantined as `status = 'dead'` in
`wrkflw_outbox`, recovered with `Redrive`.

This ADR describes the opposite direction: an **inbound** wake-up the engine could
not apply to a process instance. The two are the same EIP pattern (Dead Letter
Channel) pointing opposite ways, and they share nothing operationally — different
identity (outbox row id vs instance + waiter), different recovery verb (`Redrive`
re-queues a publish; `Replay` re-applies a trigger), different lifecycle owner
(the relay vs the driver). Unifying them would force one API over two unrelated
recovery stories.

The concept is therefore named **undelivered wakeup** throughout, so a reader who
encounters `DeadLetter` in this codebase can rely on it meaning exactly one thing.

## Decision

Add a durable undelivered-wakeup channel, and an escalation ladder inside
`Bus.Publish` applied per recipient:

0. `ctx.Err() != nil` → **abort the fan-out, record nothing.** `store.Load` maps
   only `sql.ErrNoRows` to `ErrInstanceNotFound`; `context.Canceled` and connection
   failures arrive as wrapped store errors and would otherwise fall to step 3. A
   client disconnecting mid-broadcast would then attempt one record per remaining
   recipient, each write using the same cancelled context, each failing, each
   ERROR-logged — ten thousand failed writes from one dropped connection.
1. `ErrConcurrentUpdate` → **retry only if nothing has committed yet.**
2. `ErrInstanceNotFound` → **self-heal, not recorded.** An orphan waiter row means
   the projection is inconsistent, not that a delivery failed: there is no instance
   to wake. Delete the row, WARN, count a metric, continue.
3. Anything else — retries exhausted, store error, engine `Step` error → **record
   an undelivered wakeup**, then continue with the remaining recipients.

#### Step 1 in full: why a blind retry is unsafe

An earlier draft retried `ErrConcurrentUpdate` unconditionally, citing
`timerFireFunc`. **That precedent is false.** A fired one-shot timer is consumed,
so re-applying `TimerFired` is genuinely a no-op. A signal is the opposite: a
non-interrupting boundary or event-sub-process arm is deliberately **never**
consumed (`engine/step_boundaries.go`, `engine/step_eventsubprocess.go`, ADR-0124).

`deliverLoop` commits **once per iteration** and performs side effects after each
commit. So a blind retry has two distinct failure modes:

- **Duplicate side effects.** Iteration 1 commits and fires a non-interrupting arm;
  iteration 2 CAS-fails; the retry re-applies the same `SignalReceived` against the
  advanced state; the arm is still armed and fires again. Up to `maxAttempts`
  reminder emails or escalations.
- **Silent success over a permanently stuck instance.** Iteration 1 commits and
  `perform` runs an `InvokeAction`; iteration 2 CAS-fails, so the queued
  `ActionCompleted` is dropped and the token stays parked forever. The bus then
  retries, finds the arm gone, and returns **nil** — no error, no record, no log,
  while the instance is stuck and the action has already executed. That is exactly
  the R3 failure shape this ADR exists to close, introduced by the retry itself.

The retry is therefore **conditional on nothing having been committed**.
`deliverLoop` surfaces a committed-step count; a CAS failure on its *first* commit
means no state change and no side effects occurred, so re-delivery is safe. A CAS
failure at any later iteration is **not** retried — it is recorded as an
undelivered wakeup, because re-delivering the original trigger cannot recover the
dropped follow-up trigger and would re-fire whatever already succeeded.

```go
// runtime/kernel/undelivered.go

// UndeliveredWakeup is one wake-up that could not be applied to its waiter.
// It is the inbound counterpart to monitor.DeadLetter (outbound outbox
// failures) and deliberately does not share its name — see ADR-0157.
type UndeliveredWakeup struct {
	ID             string
	InstanceID     string
	Kind           WaiterKind
	Name           string
	CorrelationKey string
	Payload        map[string]any
	OccurredAt     time.Time // when delivery originally failed; PROVENANCE, not the replay instant
	FailedAt       time.Time
	Attempts       int
	Cause          string
	// Waiters is the instance's waiter set as it was when delivery failed.
	// ReplayUndelivered refuses when the instance no longer awaits a matching
	// entry, so a recovery attempt cannot wake a wait that has since moved on.
	Waiters []Waiter
}

type UndeliveredStore interface {
	Record(ctx context.Context, u UndeliveredWakeup) error
	List(ctx context.Context, f UndeliveredFilter) (UndeliveredPage, error)
	Delete(ctx context.Context, id string) error
}

// ReplayOption configures ReplayUndelivered. WithForce skips the waiter-set
// check — the operator's explicit acknowledgement that the instance has moved on.
type ReplayOption func(*replayConfig)

func WithForce() ReplayOption
```

Backed by `wrkflw_undelivered` across all three dialects, with a
`(failed_at DESC, id DESC)` keyset index for listing and an `instance_id` index
for per-instance inspection. `ID` comes from the driver's existing
`idgen.Generator` (ADR-0149) — no new id concept. `PruneUndelivered(cutoff)` is
added, sibling to the existing `PruneTimers`.

Three properties are deliberate:

**`Record` is not in the delivery transaction.** By definition that transaction
failed. It is a separate best-effort write afterwards; if it also fails, the
failure is ERROR-logged, because there is nowhere left to escalate.

**The joined error return is unchanged.** The record is defence in depth for a
caller who ignores the error, never a replacement for it.

**Replay restamps with the current time; the original instant is kept as
provenance.** `(*ProcessDriver).ReplayUndelivered(ctx, id)` rebuilds the trigger
with `clk.Now()` and surfaces the recorded `OccurredAt` through
`ListUndelivered` rather than replaying at it.

An earlier draft decided the opposite, on the reasoning that restamping "would
shift every downstream timer anchored to the signal/message instant." **That
reasoning does not survive source verification and is withdrawn.** No downstream
timer is anchored to `Trigger.OccurredAt()`: `timerJobsFor` computes
`now := driver.clk.Now()` and derives `nextRun` from `strig.Next(now)`
(`runtime/timerops.go`), and `ScheduleTimer.Trigger` is a definition-derived
`TriggerSpec`, not an absolute instant. The ADR-0134 analogy does not transfer —
that path re-arms `schedule.At(a.NextRun)`, a *stored absolute time*, which is a
different mechanism entirely.

What `OccurredAt` actually drives is `at` inside `Step`: `Token.EnteredAt`,
`openVisit`/`closeVisit` timestamps, and `s.EndedAt`. Replaying at a stale instant
therefore **backdates `NodeVisit` records and can set `EndedAt` earlier than the
close of the visit preceding it** — corrupting the ordered history the ADR-0144–0151
audit view exists to provide. Since replay is manual with no automatic sweeper, the
recorded instant is stale by hours or days by construction, so this is the normal
case rather than an edge one.

**Replay is at-least-once with side effects, not idempotent.** An earlier draft
claimed "a trigger whose waiter has since been consumed is already a clean engine
no-op." True for an interrupting arm; false for a non-interrupting one, which is
never consumed (ADR-0124) — so replaying twice sends two escalations. Worse, an
instance that looped back and re-armed the same signal name will accept a
day-old replay as a fresh wake-up and merge the stale payload into its variables.
`ReplayUndelivered(ctx, id, opts ...ReplayOption)` therefore persists the **waiter
set** alongside the record and **refuses** with `ErrWaiterSetChanged` when the
instance no longer awaits a matching entry, unless the caller passes `WithForce()`.

A waiter set, not an arm identity: an arm identity is not one shape — ADR-0158
gives three, plus tier-4 token ids — and none of them is meaningful to an operator
reading the row. The waiter set is the type the projection already speaks, costs
one JSON column, and catches the two recovery mistakes that actually happen: the
instance completed, or it advanced past the wait. It does **not** catch a loop-back
that re-armed the *same* name with a *different* arm; that case still replays, and
is the reason replay remains at-least-once with side effects rather than safe.

The store is an **opt-in capability**. Absent, step 3 degrades to ERROR log plus
metric, and delivery behaviour is otherwise unchanged — so zero-config and
`processtest` keep working.

### Alternatives rejected

- **Observable-only (ERROR log + metric, no table).** Cheaper and closes the
  invisibility problem, but leaves no way to recover the wake-up. Rejected by the
  project owner in favour of the durable channel.
- **Leave as joined errors.** Smallest surface and arguably correct for a library
  that should not impose operational policy, but a caller who ignores the error
  loses the wake-up with no trace — the exact failure shape this bundle exists to
  eliminate.
- **Reuse `monitor.DeadLetter` / extend `service.DeadLetterAdmin`.** Rejected: see
  Context. Opposite direction, unrelated identity, incompatible recovery verb.
- **Record `ErrInstanceNotFound` too.** Rejected: it is not a failed delivery, it
  is an inconsistent projection. Recording it would generate noise proportional to
  a bug rather than to lost work, and self-healing removes the cause.

## Consequences

**Positive.**

- A permanently-undeliverable wake-up is durable, inspectable, and replayable
  instead of vanishing into an ignored error.
- The bounded CAS retry lands on the broadcast path for the first time, so the
  common contention case now succeeds instead of failing.
- Orphan waiter rows self-heal as a side-effect of delivery, so an inconsistent
  projection converges rather than accumulating.
- Replay preserving `OccurredAt` means recovery does not silently shift downstream
  timers.
- `DeadLetter` keeps exactly one meaning in the codebase.

**Negative / accepted costs.**

- A fourth table, a fourth set of dialect implementations, a conformance suite,
  and a replay API — on top of an already large bundle.
- Two similarly-shaped recovery surfaces now exist (`DeadLetterAdmin.Redrive` for
  outbound, `ReplayUndelivered` for inbound). A consumer must learn which is
  which; the naming carries that burden, and both are documented in terms of the
  other.
- `wrkflw_undelivered.occurred_at` / `failed_at` are subject to ADR-0151: SQLite
  stores times as TEXT and the keyset index sorts lexicographically, so both
  columns must go through the fixed-width 9-digit-fraction encoder
  (`internal/persistence/store/time_codec.go`), gated on
  `dialect.Dialect.TimestampsAsText` — never on a `Name == "sqlite"` comparison.
  Writing `RFC3339Nano` directly reintroduces exactly the bug ADR-0151 fixed.
- Replay is manual. There is no automatic retry sweeper; that is deliberate, since
  a wake-up that exhausted retries usually needs a human to know why before it is
  re-attempted.
- A record carries a `Cause` string, not a typed error. Callers cannot `errors.Is`
  against it. Accepted: the record is an operator artifact, and the typed error is
  still returned to the caller at failure time.

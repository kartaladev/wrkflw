# 15. Engine-modeled retry executor with runtime-recorded jitter

- Status: Accepted
- Date: 2026-06-21

## Context

A load-bearing project requirement — *"A process error must be able to be retried. Consider also other
resilient aspect."* — is unimplemented. `engine.ActionFailed` carries a `Retryable bool` flag,
but nothing acts on it: a failed `ServiceAction` goes straight to `propagateError`
(error-boundary routing, else `StatusFailed`). There is no backoff, no attempt cap, no
poison handling.

The hard constraint is the core's purity (HANDOVER "Core invariants" #2, #3, ADR-0003): `Step`
must never read the wall clock or randomness, and identical `(state, trigger)` must yield
identical `(state, commands)`. A retry executor needs both wall-clock backoff *and* jitter (AWS
"Exponential Backoff And Jitter": un-jittered backoff synchronizes failing clients into retry
storms; Full Jitter de-synchronizes them). Naively, both are non-deterministic.

Mature engines resolve this by putting the retry loop **outside** the deterministic unit:
Temporal's server owns the retry loop and *records* non-deterministic values (timer fire times,
attempt counts) into Event History so workflow replay stays deterministic; Camunda's Job Executor
owns acquisition/backoff, not the BPMN model. The question for `wrkflw` is *where* the loop lives
given we already have a pure `Step` + a `Scheduler` port + a transactional `Store`.

Two placements were considered:

1. **Runtime-owned loop** — the runtime catches `ActionFailed`, consults the policy, sleeps/
   schedules, and re-invokes without re-entering `Step`. The engine sees only the final outcome.
   But attempt state then lives outside the persisted snapshot (lost on restart unless a parallel
   persistence is built), and it bypasses the codebase's event-sourced replay strength.
2. **Engine-modeled loop** — the engine models a retry as an ordinary scheduled timer. The wait
   and the jitter draw happen at the runtime edge; their *results* are recorded on the trigger and
   in `InstanceState`, which `Step` consumes deterministically.

## Decision

**Adopt the engine-modeled loop. A retry is a timer.**

- `ActionFailed` gains `JitterFraction float64 ∈ [0,1)`. The **runtime samples it** from a seeded
  RNG (`runtime.JitterSource`, injectable for tests) when constructing the trigger, and it is
  persisted on the journalled trigger. `Step` computes
  `delay = JitterFraction × RetryPolicy.Backoff(attempt)` — pure, replayable.
- On a retryable failure with budget remaining, `Step` emits
  `ScheduleTimer{Kind: TimerRetry, FireAt: OccurredAt + delay}`, increments `Token.RetryAttempts`,
  anchors `Token.RetryStartedAt`, and parks the token. The existing `Scheduler` fires it; the
  resulting `TimerFired{TimerRetry}` makes `Step` re-emit the `InvokeAction`. No new scheduler
  port, no new command.
- The policy is `model.RetryPolicy` (a pure value type: `MaxAttempts` default 3 — **finite**, the
  safe default for an embedded library; `InitialInterval` 1s; `BackoffCoef` 2.0; `MaxInterval`
  100×; `MaxElapsed` budget; `NonRetryableErrors`). It is authored **per node**
  (`Node.RetryPolicy`) with a **runtime default fallback** (`StepOptions.DefaultRetryPolicy`, set
  via `runtime.WithDefaultRetryPolicy`).
- **Retry is opt-in.** When neither a node policy nor a runtime default exists, `Step` takes the
  legacy `propagateError` path **verbatim** — existing behaviour and tests are unchanged.

## Consequences

**Easier:** retries reuse the entire existing timer/`Scheduler`/`Store`/rehydration machinery —
a parked retry is just a timer in the snapshot, so it survives restart and persists for free. The
decision logic is pure and table-testable in `engine_test` (the codebase's strength), including a
determinism test asserting identical commands (incl. `FireAt`) for identical
`(state, ActionFailed{jitter})`. Jitter lives only at the edge, honoring ADR-0003. Full Jitter
prevents retry storms.

**Harder / trade-offs:** the pure core grows new state (`Token.RetryAttempts`/`RetryStartedAt`,
`TimerRetry`) and `cloneState` must deep-copy it (invariant #4) — more surface in the sealed
sets and the snapshot. Recording `JitterFraction` on the trigger slightly enlarges the journal.
The runtime's RNG is non-deterministic on the *first* draw (only replay is deterministic), which
is correct but means a live run and its replay differ only if the journal is lost. `MaxAttempts`
default 3 diverges from Temporal's "unlimited" — deliberate, to avoid hanging an embedding
consumer on a poison action.

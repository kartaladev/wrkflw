# Resilience (Retry / Backoff / DLQ / Idempotency) — Design Spec

Status: **Approved** (design forks decided 2026-06-21) · Date: 2026-06-21
Sub-project: **Resilience** — second track of the deferred-backlog run.
Drives REQUIREMENTS.md line 25: *"A process error must be able to be retried. Consider also
other resilient aspect."*

This spec is the contract. When a plan and this spec disagree, **the spec wins** — except where
the spec lists example code: trust the current source over any listing (the engine has grown a
lot; ground every edit against the then-current code).

## 1. Scope & decisions

The design forks were decided up front:

| Fork | Decision |
|---|---|
| Track scope | **Full**: action retry executor + **Catch-style alternative-flow routing** + **incidents** + relay **DLQ/poison isolation** + **idempotency** (stable key + consumer dedup). |
| Policy authoring | **Node-level `RetryPolicy` + runtime default fallback.** |
| Exhaustion behaviour | **Catch-flow → Incident** precedence; incident parks the token, admin-resumable. |
| Idempotency | **Stable action key + consumer dedup table.** |

Constraints fixed by existing invariants (HANDOVER "Core invariants"):

- **The pure core never reads the clock or randomness.** The retry *wait* and the jitter *draw*
  happen in the runtime; `Step` only applies values recorded on the trigger (the Temporal
  "record non-determinism into history" pattern). This makes retries deterministically replayable.
- **Retry rides the existing timer machinery.** A scheduled retry is a `ScheduleTimer` →
  `Scheduler` → `TimerFired` round-trip, exactly like every other wait. No new scheduler port.
- **Retry is opt-in and backward-compatible.** Absent an *effective* `RetryPolicy` (no node
  policy and no runtime default), `ActionFailed` behaves **exactly as today**
  (`propagateError` → error boundary or `StatusFailed`). All existing engine tests stay green.

Research backing (workflow-engine survey, recorded in the ADRs): Temporal `RetryPolicy` field
shape + server-owned retry loop; Step Functions `Retry → Catch` precedence; Camunda incident
model; AWS "Exponential Backoff And Jitter" (Full Jitter default); transactional-outbox DLQ via
a `status`/`retry_count`/`next_attempt_at` quarantine column; idempotent-consumer dedup table.

## 2. Architecture overview

```
                 ┌────────────────────────── pure core (deterministic) ───────────────────────────┐
   action fails  │                                                                                  │
  (runtime edge) │   ActionFailed{Retryable, JitterFraction}                                        │
   samples ─────────►  Step:  effective policy = node.RetryPolicy ?? opt.DefaultRetryPolicy         │
   JitterFraction │         ├─ retryable & budget left → ScheduleTimer{TimerRetry, FireAt}          │
   from seeded RNG│         │                              token.RetryAttempts++ ; park token       │
                  │         └─ terminal (non-retryable | budget exhausted):                         │
                  │              1. node.RecoveryFlow set → route token down it (+ _error vars)      │
                  │              2. else existing propagateError (error boundary) — UNCHANGED        │
                  │              3. else → raise Incident (park token, persist) ── admin-resumable   │
                  │                                                                                  │
   TimerFired{TimerRetry} ───────►  re-emit InvokeAction for token.NodeID                           │
   ResolveIncident{id,addAttempts} ►  remove incident, grant budget, re-emit InvokeAction           │
                  └──────────────────────────────────────────────────────────────────────────────┘

  Outbox relay (internal/persistence/postgres):  poll status='pending' AND next_attempt_at<=now()
     publish ok   → status='published'
     publish err  → retry_count++, next_attempt_at = now()+backoff, last_error  (row-isolated)
     retry_count ≥ ceiling → status='dead'   (quarantined; batch advances — no head-of-line block)
     admin: ListDeadLettered / Redrive

  Idempotency:  engine sets InvokeAction.Input["_idempotencyKey"] = instanceID:nodeID  (retry-stable)
                consumer-side Deduper: INSERT (subscriber, message_id) → false on conflict (in-tx)
```

## 3. Pure model changes (`model/`)

### 3.1 `RetryPolicy` value type

A new pure value type (`model/retry.go`). `time.Duration`/`float64`/`[]string` are pure data —
no clock read, core stays clean.

```go
// RetryPolicy describes how a failed ServiceAction is retried. The zero value is
// NOT a usable policy; use DefaultRetryPolicy or set fields explicitly.
type RetryPolicy struct {
	MaxAttempts        int           // total attempts incl. the first; default 3. 0 = unlimited.
	InitialInterval    time.Duration // wait before the first retry; default 1s.
	BackoffCoef        float64       // per-attempt multiplier; default 2.0.
	MaxInterval        time.Duration // per-attempt cap; default 100×InitialInterval.
	MaxElapsed         time.Duration // total time budget across attempts; 0 = no budget cap.
	NonRetryableErrors []string      // error substrings that abort retries immediately.
}

// DefaultRetryPolicy returns the Temporal-style defaults with a FINITE attempt cap
// (3) — the safe choice for an embedded library (unlimited retries on a poison
// action would hang a consumer's process).
func DefaultRetryPolicy() RetryPolicy

// Backoff returns the capped, UN-jittered delay before the given zero-based attempt
// (attempt 0 = the first retry). delay = min(InitialInterval × BackoffCoef^attempt, MaxInterval).
// Pure: depends only on the policy and the attempt number.
func (p RetryPolicy) Backoff(attempt int) time.Duration

// IsNonRetryable reports whether errMsg matches any NonRetryableErrors substring.
func (p RetryPolicy) IsNonRetryable(errMsg string) bool

// Normalize returns a copy with zero-valued fields filled from DefaultRetryPolicy
// (so a partially-specified node policy is usable). MaxAttempts==0 is preserved
// (means unlimited) — only negative is treated as unset.
func (p RetryPolicy) Normalize() RetryPolicy
```

Full-jitter is applied in the engine: `actualDelay = JitterFraction × Backoff(attempt)`, where
`JitterFraction ∈ [0,1)` arrives on the trigger.

### 3.2 `Node` additions

```go
// RetryPolicy is the optional per-node retry policy for an action-bearing node
// (KindServiceTask and other action nodes). nil ⇒ fall back to the runtime
// default (engine.StepOptions.DefaultRetryPolicy); if that is also nil, retry is
// disabled and a failed action behaves exactly as before (propagateError).
RetryPolicy *RetryPolicy

// RecoveryFlow is the ID of the sequence flow to take when an action's retries are
// exhausted (Step-Functions "Catch"). Mirrors SLAFlow. Empty ⇒ no catch-flow;
// exhaustion falls through to error-boundary propagation, then an Incident.
// When taken, the engine injects _error / _errorMessage / _errorAttempts into the
// instance variables (ResultPath-style) so the recovery path can branch on them.
RecoveryFlow string
```

### 3.3 `Validate` additions

- `RetryPolicy` (when present, on the node or its `Normalize`d form): `MaxAttempts >= 0`,
  `InitialInterval >= 0`, `BackoffCoef >= 1.0` when `InitialInterval > 0`, `MaxInterval >= 0`.
  New sentinel `ErrInvalidRetryPolicy`.
- `RecoveryFlow` (when non-empty): must reference an existing sequence flow whose `Source` is
  this node. New sentinel `ErrInvalidRecoveryFlow`. Recurse into sub-process definitions like
  the existing rules.

## 4. Pure engine changes (`engine/`)

### 4.1 Trigger / Command sealed-set additions

- **`ActionFailed` gains `JitterFraction float64`.** New constructor
  `NewActionFailedJittered(at, commandID, errMsg string, retryable bool, jitter float64)`;
  the existing `NewActionFailed(...)` stays and sets `JitterFraction: 0` (back-compat — a
  zero jitter just means "retry immediately if scheduled", harmless for non-retry callers).
- **`ResolveIncident` trigger** (admin): `{IncidentID string, AddAttempts int}`. `OccurredAt`
  carried as usual. Re-grants `AddAttempts` to the parked token's budget and re-invokes.
- **`TimerKind` gains `TimerRetry`** (+ `String()` case). A `ScheduleTimer{Kind: TimerRetry}`
  is an ordinary timer to the runtime; only the engine attaches retry meaning.

No new `Command` is needed — retry reuses `ScheduleTimer`/`InvokeAction`.

### 4.2 `InstanceState` / `Token` additions

```go
// Token additions:
RetryAttempts  int       // attempts already made for the in-flight action on this token (0 = none yet).
RetryStartedAt time.Time // OccurredAt of the first failure — anchors the MaxElapsed budget. Zero = not retrying.

// InstanceState addition:
Incidents []Incident // unresolved retry-exhaustion incidents (parked tokens awaiting admin action).
IncidentSeq int      // deterministic incident-id counter (suffix "-in").

// Incident:
type Incident struct {
	ID        string    // "<instanceID>-in<n>"
	TokenID   string
	NodeID    string
	ScopeID   string
	CommandID string    // the last failed InvokeAction's CommandID
	Error     string    // last error message
	Attempts  int       // attempts made before giving up
	CreatedAt time.Time // OccurredAt of the terminal failure
}
```

- New `TokenState` value `TokenIncident` — the token is parked in an incident (distinct from
  `TokenWaitingCommand` so admin/monitoring can see it; the token retains `NodeID`/`ScopeID`).
- **`cloneState` MUST deep-copy `Incidents`** (slice of value structs — append-copy suffices; no
  nested maps) and the new scalar `Token`/`InstanceState` fields (copied by struct/element copy).
  Extend the cloneState test accordingly (invariant #4).

### 4.3 `StepOptions` addition

```go
// DefaultRetryPolicy is the fallback policy applied to an action-bearing node that
// declares no RetryPolicy of its own. nil ⇒ retry disabled by default (current
// behaviour). The runtime sets this from runtime.WithDefaultRetryPolicy.
DefaultRetryPolicy *model.RetryPolicy
```

Effective policy resolution (pure): `eff := node.RetryPolicy ?? opt.DefaultRetryPolicy`; if both
nil ⇒ **no retry** (fall straight through to the existing `propagateError`). Otherwise
`eff = eff.Normalize()`.

### 4.4 `Step` retry logic (the `ActionFailed` case)

Today `ActionFailed` cancels boundary arms then calls `propagateError`. The new flow, **gated on
an effective policy existing**:

1. Find the token by `CommandID`. Resolve `eff` (§4.3). If no effective policy ⇒ unchanged
   legacy path (`propagateError`). The rest applies only when `eff` exists.
2. **Non-retryable?** If `!trg.Retryable` or `eff.IsNonRetryable(trg.Err)` ⇒ go to **exhaustion**.
3. **Budget check.** `attempt := token.RetryAttempts` (0-based). If `eff.MaxAttempts != 0 &&
   attempt+1 >= eff.MaxAttempts` ⇒ exhaustion. If `eff.MaxElapsed > 0 && !token.RetryStartedAt.IsZero()
   && trg.OccurredAt.Sub(token.RetryStartedAt) > eff.MaxElapsed` ⇒ exhaustion.
4. **Schedule retry.** Otherwise:
   - `base := eff.Backoff(attempt)`; `delay := time.Duration(trg.JitterFraction * float64(base))`
     (Full Jitter). `FireAt := trg.OccurredAt.Add(delay)`.
   - Emit `ScheduleTimer{TimerID: <TimerSeq>, Token: token.ID, FireAt, Kind: TimerRetry}`.
   - `token.RetryAttempts++`; set `token.RetryStartedAt` if zero; park token
     `TokenWaitingCommand` on the retry `TimerID` (record in `Timers`).
5. **Exhaustion path** (precedence Catch → boundary → Incident):
   - **(a) Catch-flow.** If `node.RecoveryFlow != ""`: inject `_error` (code, if any),
     `_errorMessage = trg.Err`, `_errorAttempts = token.RetryAttempts` into `Variables`; reset
     the token's retry fields; route the token down `RecoveryFlow` (drive from the flow's
     target). Continue normal driving.
   - **(b) Error boundary.** Else call the existing `propagateError` — if an error boundary is
     armed on the node/scope it catches as today (preserves the existing feature).
   - **(c) Incident.** Else append an `Incident`, set the token `TokenIncident`, leave the
     instance `StatusRunning` (other tokens keep running). **No** `FailInstance` — the instance
     is not failed, it is *stuck pending intervention*.

   > Backward-compat note: legacy flows reaching this case had *no* effective policy and so never
   > enter steps 2–5 — they keep hitting `propagateError`/`StatusFailed` verbatim. Incidents only
   > arise when a policy is configured and exhausts with no recovery flow and no error boundary.

### 4.5 `TimerFired{TimerRetry}` handling

When a fired timer's record has `Kind == TimerRetry`: re-emit `InvokeAction` for the parked
token's `NodeID` (re-evaluate the action input + stamp the idempotency key, §6), park the token
`TokenWaitingCommand` on the new command id, remove the consumed timer record. Determinism: same
machinery as every other timer.

### 4.6 `ResolveIncident` handling

Find the `Incident` by `ID`. If absent ⇒ no-op (idempotent — a late/duplicate resolve is clean).
Otherwise: re-grant budget (the engine treats `AddAttempts` as raising the effective attempt cap
for this token — implemented by resetting `token.RetryAttempts` to
`max(0, RetryAttempts - AddAttempts)` so up to `AddAttempts` more retries are allowed), remove the
incident, move the token from `TokenIncident` back to active, and re-emit `InvokeAction` for the
node. Removing the incident clears its slot from `Incidents`.

## 5. Runtime changes (`runtime/`)

- **Jitter source.** A small port `JitterSource` with `Fraction() float64` returning `[0,1)`.
  Default impl seeds `math/rand/v2` from the clock at construction (entropy is fine — the value
  is *recorded* on the trigger and replayed deterministically thereafter; only the live draw is
  random). Injectable via `WithJitterSource` for deterministic tests. The runtime samples
  `Fraction()` when building the `ActionFailed` trigger in `perform`.
- **Default policy.** `WithDefaultRetryPolicy(p model.RetryPolicy)` → threaded into
  `StepOptions.DefaultRetryPolicy` on every `Step` call.
- **Incident admin API.** `Runner.ResolveIncident(ctx, instanceID, incidentID string, addAttempts int) error`
  loads the instance, delivers a `ResolveIncident` trigger through the normal `Deliver` path (so
  it is journalled + persisted + drives follow-on commands).
- **Surfacing incidents.** `InstanceState.Incidents` flows through the existing snapshot
  persistence automatically (it is part of the snapshot JSONB). `runtime.InstanceSummary` gains an
  `IncidentCount int` (or the lister exposes incidents) so admin monitoring can flag stuck
  instances. No change to the `Store` contract.
- **perform.** `TimerRetry` requires no special handling in `perform` — `ScheduleTimer` already
  schedules any `Kind` via the `Scheduler`. The fire callback path (with its existing CAS
  retry-with-reload, HANDOVER Scheduling R4b) is reused.

## 6. Idempotency

### 6.1 Stable action key (producer / action side)

When the engine emits an `InvokeAction` for a node, it stamps
`Input["_idempotencyKey"] = instanceID + ":" + nodeID`. The key is **attempt-independent** — the
same across all retries of the same action on the same instance — so a `ServiceAction` author can
dedup an external side effect across retries. Documented in the `action.ServiceAction` godoc and
the engine-core spec. Pure: the engine already knows `instanceID` and `nodeID`.

### 6.2 Consumer dedup table (consumer side)

Provide an idempotent-consumer helper so library consumers get **exactly-once effect** over our
at-least-once delivery:

- New migration: `wrkflw_processed_message (subscriber TEXT, message_id TEXT, processed_at
  TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY (subscriber, message_id))`.
- `persistence` façade exposes `NewDeduper(pool) Deduper` returning an interface:
  `Seen(ctx, tx, subscriber, messageID string) (firstTime bool, err error)` — `INSERT ... ON
  CONFLICT DO NOTHING`; `firstTime == false` ⇒ duplicate, skip the side effect. The insert is
  done **inside the consumer's business transaction** so the dedup record and the side effect
  commit atomically. `message_id` is the outbox `dedup_key`.
- The `Deduper` interface and a Postgres impl live behind the `persistence` façade (ADR-0008
  layering); `internal/persistence/postgres` holds the pgx wiring.

## 7. Relay DLQ / poison isolation (`internal/persistence/postgres`)

### 7.1 Migration (new goose migration, additive)

Add to `wrkflw_outbox`:

```sql
ALTER TABLE wrkflw_outbox ADD COLUMN status          TEXT        NOT NULL DEFAULT 'pending';
ALTER TABLE wrkflw_outbox ADD COLUMN retry_count     INT         NOT NULL DEFAULT 0;
ALTER TABLE wrkflw_outbox ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE wrkflw_outbox ADD COLUMN last_error      TEXT;
-- Backfill: rows with published_at set ⇒ status 'published'.
UPDATE wrkflw_outbox SET status = 'published' WHERE published_at IS NOT NULL;
-- Replace the unpublished partial index with a claim index.
DROP INDEX IF EXISTS wrkflw_outbox_unpublished_idx;
CREATE INDEX wrkflw_outbox_claim_idx ON wrkflw_outbox (next_attempt_at)
	WHERE status = 'pending';
CREATE INDEX wrkflw_outbox_dead_idx ON wrkflw_outbox (id) WHERE status = 'dead';
```

`status ∈ {'pending','published','dead'}`. `published_at` retained for audit.

### 7.2 Relay behaviour

- **Claim predicate** becomes `WHERE status = 'pending' AND next_attempt_at <= now()
  ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $batch`. `dead` rows are skipped ⇒ a poison event no
  longer blocks the batch (fixes the head-of-line limitation).
- **Per-row isolation.** Publish each claimed row individually; on success mark
  `status='published', published_at=now()`. On publish error for a row: `retry_count++`,
  `next_attempt_at = now() + backoff(retry_count)`, `last_error = err`. When
  `retry_count >= MaxDeliveryAttempts` (configurable, default e.g. 10) ⇒ `status='dead'`. A
  single row's failure does **not** roll back the others (each row's state update commits).
- **Backoff** uses a pure helper `relayBackoff(retryCount, base, cap) time.Duration` (capped
  exponential; no jitter needed for a single-writer relay, but a configurable jitter hook is
  acceptable). Driven by the injected clock — no `time.Now()` in core, the relay is an edge
  component so it may read the clock via the same `clock.Clock`.
- **Ordering caveat (documented):** quarantining `dead` breaks strict order; loss is bounded to
  the affected `instance_id` lane. Strict global order was never promised (`SKIP LOCKED`).

### 7.3 DLQ admin API (`persistence` façade)

- `Relay.ListDeadLettered(ctx, limit int) ([]DeadLetter, error)` — id, instance_id, topic,
  retry_count, last_error, created_at.
- `Relay.Redrive(ctx, ids ...int64) (int, error)` — set `status='pending', retry_count=0,
  next_attempt_at=now()` for the given dead rows; returns the count requeued.

All returns are façade interface/value types (ADR-0008) — no internal struct leak.

## 8. Transports / service (optional, thin)

To keep the feature reachable through the public API (CLAUDE.md "library-first"):

- `service.Service` gains `ResolveIncident(ctx, instanceID, incidentID string, addAttempts int)
  error` and surfaces `IncidentCount` on the instance response.
- `transport/rest`: `POST /admin/instances/{id}/incidents/{incidentID}/resolve` (admin-gated);
  optionally `GET /admin/dead-letters` + `POST /admin/dead-letters/redrive` mapping to the relay
  DLQ API. `transport/grpc`: matching RPCs.
- These are **thin pass-throughs**; the engine/runtime hold the behaviour. If the plan runs long,
  the transport surface for DLQ admin may ship as a follow-up — the runtime/persistence APIs are
  the load-bearing deliverable.

## 9. ADRs

| ADR | Decision |
|---|---|
| **0015** | Engine-modeled retry executor: retries are timers; jitter & clock live in the runtime and are *recorded* on `ActionFailed.JitterFraction`; `Step` stays pure/deterministic. Retry is opt-in (effective policy gates all new behaviour). |
| **0016** | Retry-exhaustion behaviour: `Catch (RecoveryFlow) → error-boundary → Incident` precedence; incidents park the token (instance stays `StatusRunning`), admin-resumable via `ResolveIncident`. |
| **0017** | Outbox relay poison isolation / DLQ: `status`/`retry_count`/`next_attempt_at`/`last_error` columns; per-row isolation; quarantine to `dead`; `ListDeadLettered`/`Redrive` admin API. |
| **0018** | Idempotency: stable attempt-independent `_idempotencyKey` for actions + `wrkflw_processed_message` consumer dedup table behind a `Deduper` port. |

## 10. Testing strategy

- **Pure engine** (black-box `engine_test`): table-driven (assert-closure form) over the retry
  decision matrix — retryable+budget→schedule; non-retryable→exhaust; budget exhausted→exhaust;
  Catch-flow routing + `_error` injection; incident raised + token `TokenIncident` +
  instance still `StatusRunning`; `ResolveIncident` re-invokes; `TimerFired{TimerRetry}`
  re-invokes; **determinism** (same `(state, ActionFailed{jitter})` ⇒ same commands incl. FireAt);
  legacy no-policy path unchanged. `cloneState` test extended for `Incidents`/token fields.
- **model** (`model_test`): `Backoff` math, `Normalize`, `IsNonRetryable`, Validate sentinels.
- **runtime** (`runtime_test`): jitter source injection (fake → fixed fraction); default-policy
  threading; `ResolveIncident` end-to-end with `MemStore`; an action that fails N times then
  succeeds drives start→retry×k→complete on a fake clock (one shared fake clock drives engine +
  scheduler, per ADR-0003/0009).
- **internal/persistence/postgres** (testcontainers via `database.RunTestDatabase`): migration
  up/down; relay per-row isolation (one poison + healthy peers → peers delivered, poison
  retried then `dead`); `Redrive`; `Deduper` first-time vs duplicate in-tx; **parked-retry resume
  e2e** — park on a retry timer, reload via a fresh `Store`, advance the clock, resume.
- **eventing/idempotency** example: idempotent-consumer dedups a redelivered message.

Gate (every package touched): `go test -race ./...` green; ≥85% line coverage on touched
packages; `golangci-lint run ./...` clean; **engine/model purity intact** (no transport/storage/
bus/time-vendor imports — verified by the existing import guards).

## 11. Out of scope (deferred follow-ups)

- Per-attempt heartbeat / checkpoint for long-running actions (Temporal heartbeat) — future.
- `MaxElapsed`-as-wall-budget across process restarts beyond what `RetryStartedAt` captures.
- LISTEN/NOTIFY relay push (still a Persistence deferred item).
- Strict per-`instance_id` relay ordering (partitioned claiming).
- Broker-native DLQ (SQS/SNS redrive) — the relay DLQ is the engine-side equivalent.
- Casbin-gated incident-resolve authz (admin middleware gate is the v1 boundary).

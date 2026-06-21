# True async call activity — Design Spec

- Status: Accepted (deferred-backlog item — engine follow-up #3)
- Date: 2026-06-22
- Supersedes: nothing. Replaces the synchronous-only call-activity limitation
  documented in `docs/plans/HANDOVER.md` (engine-core tracked follow-up #3) and the
  runtime error "the synchronous runner does not support children that wait on human
  tasks, timers, or events; async call activity is a future enhancement".
- Related ADRs (new): 0024 (durable async call activity via a call-link store +
  notifier), 0025 (atomic call-link side-effects on the transactional `Store`).
  Builds on 0002 (pure core / sealed sets), 0006–0008 (persistence shape /
  transactional `Store` / façade-over-internal), 0017 (relay poison isolation —
  the notifier mirrors its claim/backoff patterns), 0022 (LISTEN/NOTIFY).

## 1. Problem & scope

A call-activity node starts a child process instance and parks the parent token
until the child finishes. Today `runtime/runner.go`'s `perform(StartSubInstance)`
runs the child **synchronously** via `r.Run` and translates the child's terminal
status into a `SubInstanceCompleted`/`SubInstanceFailed` trigger applied to the
parent within the same `deliverLoop`. If the child **parks** (its own human task,
timer, signal, or nested call activity), the synchronous runner cannot re-enter it
and returns a hard error.

**True async call activity:** the parent parks; the child runs independently across
however many later `Deliver`s; when the child reaches terminal status, the parent is
resumed by a `SubInstanceCompleted`/`SubInstanceFailed` trigger delivered durably and
crash-safely.

### The load-bearing finding: the engine core is already async-ready

The mapping confirms the engine needs **no change**:
- `engine.StartSubInstance{CommandID, DefRef, Input}` command — exists (`engine/command.go:116`).
- `engine.SubInstanceCompleted{CommandID, Output}` / `SubInstanceFailed{CommandID, Err}`
  triggers — exist (`engine/trigger.go:154`, `:175`).
- The parent token already parks with `State=TokenWaitingCommand, AwaitCommand=commandID`
  at the call node (`engine/step.go:1229`), and the resume logic
  (`engine/step.go:514–539`) already finds the parked token by `tokenAwaiting(CommandID)`,
  merges the child output, and drives forward.

So the parent-side park/resume is **identical** to today; the only difference is the
trigger arrives later, from a durable driver, instead of synchronously from `perform`.
**This is a runtime + persistence change; `engine` and `model` are untouched
(determinism/purity invariants fully preserved).**

**In scope:** a `wrkflw_call_links` correlation table + `CallLinkStore` port
(Mem + Postgres), atomic link side-effects on the `Store`, the `perform(StartSubInstance)`
refactor, the child-terminal hook, the `CallNotifier` driver, idempotent parent
resume, wiring, and tests. **Out of scope:** distributed cross-runtime child
execution on a different machine (the child runs in the same runtime/`DefinitionRegistry`;
the *notification* is durable and crash-safe, but a child is driven by whichever
runtime delivers its triggers); cancellation propagation parent→child (a separate
design); call-activity multi-instance / loop cardinality.

## 2. Invariants this change must not violate

1. **Engine/model untouched.** No edits to `engine/` or `model/` production code (a
   guard test asserts the call-activity engine paths are unchanged). `engine.Step`
   stays pure and deterministic; the sealed Trigger/Command sets are unchanged.
2. **No parent metadata on `InstanceState`.** The parent↔child link lives in the
   persistence layer (`wrkflw_call_links`), NOT as fields on `engine.InstanceState`
   (keeps the pure engine type free of runtime correlation data).
3. **Crash-safety.** A parent that has parked on a call activity must ALWAYS have a
   durable path to resume: the link is created atomically with child creation, and
   flipped to terminal atomically with the child's terminal commit, so no crash
   window leaves a parent parked with no pending notification.
4. **Idempotent parent resume.** Delivering `SubInstanceCompleted` twice is safe: the
   second finds the token already consumed (`ErrTokenNotFound`) and the notifier
   treats that as success.
5. **Opt-in, behavior-preserving by default.** A runtime constructed without a
   `CallLinkStore` keeps today's synchronous behavior (children that park still error)
   — existing consumers and tests are unaffected unless they opt into async.
6. **Façade/internal layering (ADR-0008).** Postgres `CallLinkStore` + notifier stay
   in `internal/persistence/postgres`; consumers reach them through `persistence`
   constructors returning stable interface types. The `CallLinkStore` port +
   `MemCallLinkStore` + `CallNotifier` live in `runtime/`.

## 3. The correlation table (`wrkflw_call_links`)

One row per child instance; doubles as the durable parent-notification queue.

```sql
CREATE TABLE wrkflw_call_links (
    child_instance_id   TEXT PRIMARY KEY,
    parent_instance_id  TEXT        NOT NULL,
    parent_command_id   TEXT        NOT NULL,   -- matches the parent token's AwaitCommand
    parent_def_id       TEXT        NOT NULL,   -- to resolve the parent definition for Deliver
    parent_def_version  INT         NOT NULL,
    depth               INT         NOT NULL,   -- call-chain depth (cycle/runaway guard)
    status              TEXT        NOT NULL DEFAULT 'running', -- running|completed|failed|notified
    output              JSONB,                  -- child terminal variables (on completed)
    error               TEXT,                   -- child error message (on failed)
    created_at          TIMESTAMPTZ NOT NULL,
    notified_at         TIMESTAMPTZ
);
-- The notifier's claim set: terminal but not yet delivered to the parent.
CREATE INDEX wrkflw_call_links_pending_idx ON wrkflw_call_links (child_instance_id)
    WHERE status IN ('completed','failed');
```

- `parent_command_id` is the `StartSubInstance.CommandID` the parent token awaits —
  the exact value the resume `SubInstanceCompleted{CommandID}` must carry.
- `parent_def_id`/`parent_def_version` let the notifier resolve the parent
  `*model.ProcessDefinition` via the `DefinitionRegistry` for the `Deliver` call.
- `depth` replaces the synchronous `maxCallActivityDepth` ctx counter: a child whose
  link `depth > maxCallDepth` is rejected at start (guards self-referential
  definitions, which async would otherwise spawn unboundedly). `maxCallDepth` is the
  renamed successor of the existing `maxCallActivityDepth = 64` constant.

## 4. The `CallLinkStore` port (runtime)

```go
// CallLink is the durable parent↔child correlation for one async call activity.
type CallLink struct {
    ChildInstanceID  string
    ParentInstanceID string
    ParentCommandID  string
    ParentDefID      string
    ParentDefVersion int
    Depth            int
}

// CallOutcome is a child's terminal result, recorded for the parent notification.
type CallOutcome struct {
    Completed bool           // true => SubInstanceCompleted, false => SubInstanceFailed
    Output    map[string]any // child terminal variables (when Completed)
    Err       string         // child error (when !Completed)
}

// PendingNotify is a claimed terminal link awaiting parent delivery.
type PendingNotify struct {
    Link    CallLink
    Outcome CallOutcome
}

// CallLinkStore persists parent↔child call-activity correlation and the durable
// parent-notification queue. The Mem and Postgres impls share this contract.
type CallLinkStore interface {
    // ClaimPending returns up to limit terminal-but-unnotified links (Postgres:
    // FOR UPDATE SKIP LOCKED), for the notifier to deliver to parents.
    ClaimPending(ctx context.Context, limit int) ([]PendingNotify, error)
    // MarkNotified records that the parent for childID has been resumed.
    MarkNotified(ctx context.Context, childInstanceID string) error
    // LookupChild returns the link for a child instance, or ErrNoCallLink.
    LookupChild(ctx context.Context, childInstanceID string) (CallLink, bool, error)
}
```

The **write side is atomic with the `Store`** (§5), so the port itself exposes only
the read/claim/mark operations the notifier needs. `MemCallLinkStore` implements this
over in-memory maps for the pure-runtime/test path.

## 5. Atomic link side-effects on the transactional `Store` (ADR-0025)

Crash-safety (invariant #3) requires:
- the call-link row is written **in the same tx** as the child's first `Create`;
- the link is flipped to `completed`/`failed` **in the same tx** as the child's
  terminal `Commit`.

The runtime `Store` already commits snapshot + journal + outbox atomically per step
(ADR-0007). We extend `AppliedStep` with optional, additive call-link side-effects:

```go
type AppliedStep struct {
    State   engine.InstanceState
    Trigger engine.Trigger
    Events  []OutboxEvent
    // NewCallLink, when non-nil, inserts a wrkflw_call_links row in this step's tx
    // (set on the child's first Create so the link exists iff the child exists).
    NewCallLink *CallLink
    // CallOutcome, when non-nil, flips this instance's own call-link to terminal in
    // this step's tx (set on the child's terminal Commit so the notification is
    // durable iff the child is terminal).
    CallOutcome *CallOutcome
}
```

`MemStore` and the Postgres `Store` both honor these optional fields (no-ops when
nil — existing callers and behavior unchanged). This keeps the Store as the single
atomic-write authority rather than introducing a second uncoordinated transaction.

## 6. Runtime changes

### 6.1 `perform(StartSubInstance)` — start non-blocking, return nil

Refactor (`runtime/runner.go`):
1. Resolve the child `*model.ProcessDefinition` (via `DefinitionRegistry`, unchanged).
2. Derive the child instance id (`<parent>-sub-<suffix>`, unchanged).
3. Compute the link `depth` (parent link depth + 1; the parent's own link, if any,
   is looked up via `CallLinkStore.LookupChild(parentID)`; a root parent is depth 0).
   Reject with `SubInstanceFailed` if `depth > maxCallDepth`.
4. **Start the child**: drive the child's first burst with `r.Run`, passing the
   `CallLink` so the child's first `Create` writes the link atomically (§5). The
   child runs to its first park or to completion.
5. **Return `nil`** — no synchronous `SubInstanceCompleted` to the parent. The parent
   stays parked. (If the child completed within this first burst with no parks, its
   terminal commit already flipped the link, and the notifier will deliver shortly.)

The `maxCallActivityDepth` ctx threading and the "synchronous runner does not support
parked children" error are removed (or the error becomes unreachable — a parked child
is now the normal case).

### 6.2 `deliverLoop` — child-terminal hook

When `deliverLoop` commits a transition into a terminal status (`isTerminal(st.Status)`
&& !isTerminal(prevStatus)), it sets `AppliedStep.CallOutcome` for that terminal
`Commit` (Completed → `st.Variables` as Output; Failed → the error), so the link flips
atomically. For a root instance (no link), `CallOutcome` is still set but the Store's
`UPDATE wrkflw_call_links` simply affects zero rows — a clean no-op. (Determining
Completed vs Failed uses the existing terminal `Status`.)

### 6.3 `CallNotifier` driver

A relay-shaped background component (`runtime/` port wiring; Postgres impl mirrors
`internal/persistence/postgres/relay.go`):
- `Run(ctx)` loops: `ClaimPending(limit)` → for each, resolve the parent def, build
  `SubInstanceCompleted{ParentCommandID, Output}` or `SubInstanceFailed{ParentCommandID, Err}`,
  call `Deliver(parentDef, ParentInstanceID, trigger)`, then `MarkNotified(childID)`.
- **Idempotency:** if `Deliver` returns `ErrTokenNotFound` (the parent token was already
  resumed — duplicate), treat as success and `MarkNotified` anyway.
- Reuses the relay's per-row isolation + capped backoff (a parent whose `Deliver`
  fails transiently is retried; it never blocks healthy peers). Optional LISTEN/NOTIFY
  (`wrkflw_call_links`) for low latency, with the poll as fallback (ADR-0022 pattern).
- `DefinitionRegistry` is injected (the notifier needs to resolve parent defs).

### 6.4 Wiring

- `runtime.NewRunner(..., WithCallLinks(store CallLinkStore))` — opt-in; absent it,
  `perform(StartSubInstance)` keeps today's synchronous behavior.
- `persistence.NewCallLinkStore(pool) CallLinkStore` and
  `persistence.NewCallNotifier(pool, deliver DeliverFunc, reg DefinitionRegistry, ...Option) CallNotifier`
  (façade; internal Postgres impls).
- A `DeliverFunc` closure (`func(ctx, instanceID, trigger) error`) wrapping
  `runner.Deliver` + parent-def resolution — same shape the `SignalBus` already uses
  (`runtime/broadcast.go`).

## 7. Error handling & edge cases

- **Child fails:** `CallOutcome{Completed:false, Err}` → notifier delivers
  `SubInstanceFailed{ParentCommandID, Err}` → the engine's existing call-node error
  handling (propagate / error boundary) runs on the parent. Unchanged engine behavior.
- **Duplicate notify:** idempotent (§6.3) — `ErrTokenNotFound` ⇒ mark notified.
- **Parent already terminal** (e.g. parent was cancelled while the child ran): the
  parent `Deliver` finds no awaiting token ⇒ `ErrTokenNotFound` ⇒ marked notified.
  The orphaned child result is dropped (documented; a future cancellation-propagation
  design would terminate the child instead).
- **Notifier crash mid-delivery:** the link stays `completed`/`failed` (not `notified`),
  so a restarted notifier re-claims and re-delivers (at-least-once + idempotent).
- **Runaway recursion:** `depth > maxCallDepth` ⇒ the child is never started; the
  parent gets `SubInstanceFailed` synchronously from `perform` (the one remaining
  synchronous failure path).

## 8. Package layout

| Package | Adds |
|---|---|
| `runtime/` | `CallLink`/`CallOutcome`/`PendingNotify` value types; `CallLinkStore` port; `MemCallLinkStore`; `AppliedStep.NewCallLink`/`CallOutcome` fields (honored by `MemStore`); `WithCallLinks` Runner option; `perform(StartSubInstance)` refactor + `deliverLoop` terminal hook; `CallNotifier` interface + `DeliverFunc`. |
| `internal/persistence/postgres/` | `wrkflw_call_links` migration; `Store.Create`/`Commit` honor the new `AppliedStep` fields in-tx; Postgres `CallLinkStore`; Postgres `CallNotifier` (claim/backoff/poison-isolation; optional LISTEN/NOTIFY). |
| `persistence/` (façade) | `NewCallLinkStore`, `NewCallNotifier`, the migration (folded into `Migrate`), options. |
| `engine/`, `model/` | **Nothing.** A guard test asserts the call-activity engine paths are unchanged. |

## 9. Testing

- **Engine-unchanged guard:** assert `engine`/`model` diffs are empty for this track
  (and the existing call-activity engine tests stay green).
- **The headline e2e (Mem + Postgres):** parent calls a child that **parks on a human
  task**; assert the parent stays parked and the child is running; complete the human
  task → child completes → run the `CallNotifier` → assert the parent **resumes and
  completes**, with the child's output merged. (This is exactly what errors today.)
- **Crash-safety (Postgres):** park the parent + child terminal committed; build a
  **fresh** notifier over a new pool; assert it delivers and the parent resumes
  (proves the link's durability across process restart).
- **Idempotent duplicate:** deliver the parent notification twice; assert the second
  is a clean no-op (token already consumed) and the link ends `notified`.
- **Nested async:** parent → child → grandchild, each parking; assert completion
  cascades up correctly and `depth` increments.
- **Failure path:** child fails → parent receives `SubInstanceFailed` → error boundary
  / propagation behaves as in the synchronous case.
- **Runaway guard:** a self-calling definition is rejected at `depth > maxCallDepth`.
- **Opt-out preserved:** a runtime without `WithCallLinks` still returns today's
  synchronous-parked-child error (behavior-preserving default).

## 10. Risks & follow-ups

- **Atomicity seam (§5)** is the load-bearing implementation detail — the call-link
  writes MUST ride the child's `Create`/`Commit` tx. Reviewed carefully; the additive
  `AppliedStep` fields keep the Store the single atomic-write authority.
- **Child execution locus:** the child is driven by whichever runtime delivers its
  triggers; this design makes the *parent notification* durable, not the child's
  execution distributed. True cross-machine child execution is a later concern.
- **Cancellation propagation** (parent cancel → child terminate, and orphaned-child
  cleanup) is out of scope; documented as a follow-up.
- **Notifier ordering:** like the relay, `SKIP LOCKED` gives throughput, not strict
  per-parent ordering; multiple children of one parent resume in claim order (each is
  an independent token, so order is immaterial for correctness).
- **`maxCallDepth`** is a global guard in v1 (not per-definition).
- **History/snapshot growth** of long-lived parents is bounded by the existing history
  cap (Performance/caching track).

## 11. Verification

- `go test -race ./...` green (Postgres pkgs `-p 1`); ≥85% coverage on touched
  `runtime`, `internal/persistence/postgres`, `persistence`.
- `golangci-lint run ./...` clean.
- `engine`/`model` unchanged (guard test green; no new imports; determinism/purity
  intact). No `watermill`/`casbin`/`gocron`/`clockwork` added to the core.
- The full engine+runtime e2e suite passes; the new async-call-activity e2e passes on
  both `MemStore`+`MemCallLinkStore` and the Postgres `Store`+`CallLinkStore`.

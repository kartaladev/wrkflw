# Spec: boundary-event scenario + cache-wiring example

- **Date:** 2026-07-07
- **Status:** Approved
- **Type:** Reference-wiring examples only (no library code changes)

## Context

Two documentation gaps in `examples/`:

1. **No true boundary-event scenario.** `examples/scenarios/boundary_timer/`
   is named as if it demonstrates a BPMN boundary event, but it actually
   demonstrates `activity.WithDeadline` — an activity *deadline*, a different
   feature. The real boundary-event API (`event.NewBoundary` +
   `event.WithBoundaryMessage/Timer/Signal/ErrorCode` +
   `event.WithBoundaryNonInterrupting`) has no runnable example; it is only
   exercised in engine/runtime tests.

2. **No focused cache-wiring example.** `sqlite_wiring` and `mysql_wiring`
   call `persistence.NewCachingInstanceStore(...)` inline as one line among a
   large SQL/transport wiring, and `persistence.NewDurableProvider` caches by
   default. Nothing teaches cache wiring as its own subject: the
   `cache.Provider` substrate, the two caching store wrappers
   (`NewCachingInstanceStore`, `NewCachingTaskStore`), TTL options, and how to
   swap the in-process adapter for a distributed one (Redis).

Both deliverables are `package main` reference wiring. They introduce **no new
exported library symbols**, so the red-green TDD cycle does not apply; the
verification contract is `go build ./...`, `go vet ./...`,
`golangci-lint run ./...`, and actually running each `main`.

## Deliverable 1: `examples/scenarios/message_boundary/main.go`

An **order-approval** process. A `UserTask("approve")` hosts **two** message
boundary events, demonstrating both interrupting and non-interrupting
behavior on the same host:

- **Interrupting** message boundary `bnd-cancel` — `event.NewBoundary(
  "bnd-cancel", "approve", event.WithBoundaryMessage("order.cancel", ""))`.
  Delivering `order.cancel` interrupts the approval task (marks it Cancelled)
  and routes to `end-cancelled`.
- **Non-interrupting** message boundary `bnd-remind` — `event.NewBoundary(
  "bnd-remind", "approve", event.WithBoundaryMessage("order.remind", ""),
  event.WithBoundaryNonInterrupting())`. Delivering `order.remind` spawns an
  additional token down a reminder side-path (`notify` service task →
  `end-reminded`) **without** disturbing the still-parked approval task.

```
start → approve[UserTask] ──(approver approves)──────────→ end-approved
             ├─◄ order.cancel (interrupting)   → end-cancelled
             └─◌ order.remind (non-interrupting)→ notify[Service] → end-reminded
```

### Driving sequence (deterministic, no clock/scheduler)

1. `Run` → instance parks at `approve` (`StatusRunning`); two message
   boundary waiters are armed.
2. `DeliverMessage(def, "order.remind", "", …)` — fires the **non-interrupting**
   boundary once: the `notify` reminder action runs, and the instance is
   **still** `StatusRunning`, still parked at `approve`. (A non-interrupting
   boundary fires once then de-arms — a second `order.remind` delivery would be
   a clean no-op, which the example notes but does not rely on.)
3. `DeliverMessage(def, "order.cancel", "", …)` — fires the **interrupting**
   boundary: the human task is Cancelled, the instance completes via
   `end-cancelled` (`StatusCompleted`, no tokens remain).

The example prints the status/outcome after each step and asserts the final
state (reminder ran, order cancelled) with a clear success/failure line, in the
style of the sibling scenarios.

### Wiring (public API only, mirrors `boundary_timer/main.go`)

- `definition.NewBuilder(...).Add(...).Connect(...).Build()` with
  `flow.WithFlowID` on the boundary-outgoing connects (`bnd-cancel`,
  `bnd-remind`) so the boundary flow IDs are addressable.
- `action.NewMapCatalog` with a `notify` action.
- `kernel.NewMemInstanceStore`, `humantask.NewMemTaskStore`,
  `humantask.NewStaticActorResolver`, `authz.RoleAuthorizer{}`.
- `runtime.NewProcessDriver(WithActionCatalog, WithInstanceStore,
  WithHumanTasks)`. No scheduler/clock needed — message delivery drives
  everything.

## Deliverable 2: `examples/cache_wiring/main.go`

Runnable, self-contained, **runtime-level** cache wiring. Wraps in-memory
stores with cache providers, wires them into a `ProcessDriver`, runs a
human-task instance, and reads the instance back to show the cache serving it.

### Wiring

- **Instance store** → in-process `hotcache.New(hotcache.WithCapacity(...),
  hotcache.WithTTL(...))`, wrapped by
  `persistence.NewCachingInstanceStore(memStore, kernel.AlwaysOwn{}, provider,
  persistence.WithInstanceCacheTTL(...))`. `AlwaysOwn{}` is the correct
  single-process ownership gate (matches `sqlite_wiring`).
- **Human-task store** → distributed `rediscache.New(redisClient)`, wrapped by
  `persistence.NewCachingTaskStore(memTaskStore, provider,
  persistence.WithHumanTaskCacheTTL(...))`.
- **Redis is live**, connected via `go-redis` against `REDIS_ADDR`
  (default `localhost:6379`). On startup the example `PING`s Redis; if the
  ping succeeds, the human-task cache genuinely uses Redis. If it fails
  (no Redis running), the example logs a clear notice and falls back to a
  second `hotcache.New()` for the task store so the file still runs to
  completion. This satisfies "hotcache + Redis both live" while keeping the
  example runnable with no external dependency.
- Both wrapped stores wired into `runtime.NewProcessDriver` via
  `WithInstanceStore` and `WithHumanTasks`.

### Demonstration

Run a small `start → approve[UserTask] → end` process. Park it, then
`store.Load`/read the instance back through the caching store to show the
read-through/read-hit path (a `slog` line noting the cache serve). Print the
active cache providers in use.

### Documented (comment) alternatives

A comment block shows, without requiring them at runtime:

- swapping the in-process adapter to `ottercache.New(...)` or the distributed
  `memcache.New(client)`;
- the **service-level** durable path for SQL backends:
  `persistence.NewDurableProvider(ctx, pool,
  persistence.WithCacheProvider(provider))` fed to
  `service.NewEngine(service.WithDurableStore(provider))`, which caches on by
  default and is customized with `WithInstanceCacheProvider` /
  `WithHumanTaskCacheProvider` / `WithDurableInstanceCacheTTL` / `WithoutCache`.

## Non-goals

- No changes to engine/runtime/persistence library code.
- No new exported symbols; no ADR (these are examples of existing, already-
  ADR'd features — boundary events and the caching refactor ADR-0099).
- Not touching the misleadingly-named `boundary_timer` scenario (leave as-is;
  it correctly demonstrates `WithDeadline`).

## Verification checklist

- [ ] `examples/scenarios/message_boundary/main.go` compiles and, when run,
      prints: reminder fired while still parked → order cancelled → completed.
- [ ] `examples/cache_wiring/main.go` compiles and runs to completion both with
      and without a live Redis (fallback path logs and proceeds).
- [ ] `go build ./...` clean.
- [ ] `go vet ./...` clean.
- [ ] `golangci-lint run ./...` clean for the two new files.
- [ ] Package-level godoc on each `main` explains the scenario and that it is
      reference wiring, not a shipped binary (matches sibling examples).

# wrkflw Engine Core — Execution Handover

This document lets a **fresh session with zero prior context** understand the state of `wrkflw`
and pick up the next work. Read it top to bottom before starting.

## Status: engine-core sub-project #1 is COMPLETE ✅

**All 8 engine-core plans are merged to `main`.** The pure engine core + reference runtime now
covers the broad BPMN scope. Total line coverage **88.2%**; `go test -race ./...` green;
`golangci-lint run ./...` clean.

| Plan | Scope | Merge commit | Status |
|---|---|---|---|
| 1 | Foundations: model+Validate, clock, Trigger/Command, InstanceState, pure `Step` (linear), action catalog, runtime + fakes | `4a2b092` | ✅ merged |
| 2 | Gateways: `expreval`, Exclusive (XOR), Parallel (AND fork+join) | `6d3733d` | ✅ merged |
| 3 | Inclusive (OR) gateway: OR-fork + reachability OR-join | `51c4f44` | ✅ merged |
| 4 | Human tasks: `authz`, `humantask`, AwaitHuman, claim/reassign/complete + audit + bucket | `e9a9d65` | ✅ merged |
| 5 | Timers & SLA: ScheduleTimer/TimerFired, timer intermediate, SLA breach, in-wait reminders, `MemScheduler` | `320dae1` | ✅ merged |
| 6 | Events & event-based gateway: signal/message catch+throw, first-event-wins, boundary timer/signal, `SignalBus` | `8499c32` | ✅ merged |
| 7 | Sub-processes & call activity: scope tree, scope-aware `drive`, embedded + event sub-process, call activity, `DefinitionRegistry` | `2be77e9` | ✅ merged |
| 8 | Errors, compensation & micro-step: error end/boundary error propagation, compensation rollback, cancel, real Micro mode | `f4b2b85` | ✅ merged |

The plan files remain under `docs/plans/*.md` as the record of what was built. ADRs `0001`–`0005`
in `docs/adr/`. The design spec `docs/specs/2026-06-20-engine-core-design.md` is still the contract.

## What `wrkflw` is

A library-first, BPMN-flavored Go workflow engine (Go 1.25), shipped as an importable module (no
daemon we own). Authoritative references — **read these first**:

- `REQUIREMENTS.md` — original loose requirements.
- `CLAUDE.md` — project rules (TDD discipline, root-package layout, locked tech stack, required Go
  skills). **Binding.**
- `docs/specs/2026-06-20-engine-core-design.md` — the engine-core design **spec** (the contract).
  When a plan and the spec disagree, the spec wins.
- ADRs: `0002` (pure stepper returning **Commands** driven by **Triggers**), `0003` (time via
  in-repo `clock.Clock`, impl by clockwork at the edge), `0004` (public packages at module root, no
  `pkg/`), `0005` (Runner functional-options construction).

## Core invariants (never violate — still binding for any future engine work)

1. **Pure core.** `engine` and `model` import only stdlib (+ `model`/`authz`/`humantask`/`expreval`
   as the spec allows). No transport/storage/bus/time-vendor in the core.
2. **No clock in the engine.** `Step` never calls `time.Now()`. Time enters as `Trigger.OccurredAt`;
   `FireAt = OccurredAt + duration`. `clockwork` only enters via the runtime's `clock.Clock`/`Scheduler`
   (and is imported **only in test files**; `clock/clock.go` is the edge adapter).
3. **`Step` is deterministic** — identical `(state, trigger)` ⇒ identical `(state, commands)`. All IDs
   (command `-c`, token `-t`, task `-h`, timer `-tm`, scope `-s`, gateway sentinel `evtgw:`, call
   child `-sub-`) come from in-`InstanceState` counters, never randomness or the clock. Flows in
   **definition order**; all bookkeeping slices iterated in slice order; no map iteration into command order.
4. **`Step` is pure** — never mutates its input `InstanceState` (`cloneState` deep-copies every slice +
   nested map: Tokens/Payload, Variables, History, Tasks, Timers, ArmedEvents, Boundaries,
   EventSubprocesses, Scopes+Compensations, RootCompensations, the compensation cursor). **Extend
   `cloneState` for every new state field.**
5. **`Step` signature is stable:** `Step(def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error)`.
6. **Sealed sets:** `Trigger` (`isTrigger()`+`OccurredAt()`) and `Command` (`isCommand()`) are closed;
   adding a variant is a deliberate edit in `engine`.

## Quick map of the merged code

- `model/` — `ProcessDefinition`, `Node` (all BPMN kinds incl. gateways/events/boundary/sub-process/
  call-activity), `SequenceFlow`, lookups, recursive `Validate` (+ sentinels, cycle guard).
- `clock/` — `Clock` interface + `System()`; clockwork is the fake-clock edge.
- `expreval/` — the only `expr-lang/expr` wrapper: `EvalBool`/`EvalDuration`/`EvalString` (memoized).
- `authz/` — `Actor`, `AuthzSpec`, `Authorizer` (+ `AllowAll`, `RoleAuthorizer`).
- `humantask/` — `HumanTask`, `TaskState`, `ActorResolver`, `TaskStore` (+ `MemTaskStore`, `StaticActorResolver`).
- `action/` — `ServiceAction`, `Catalog`, `MapCatalog`, `Func`.
- `engine/` — `trigger.go`/`command.go` (sealed sets), `state.go` (`InstanceState` + all bookkeeping +
  helpers + `cloneState`/`Clone`), `step.go` (`Step`, scope-aware `drive`, every node-kind case, all
  the gateway/event/boundary/timer/human/sub-process/error/compensation/micro logic), `conditions.go`.
- `runtime/` — `runner.go` (`Runner`, `NewRunner(cat, clk, store, jnl, out, ...Option)` with
  `WithHumanTasks`/`WithScheduler`/`WithSignalBus`/`WithDefinitions`, `Run`, `Deliver`, `deliverLoop`,
  `perform`), `ports.go`, `memory.go`, `scheduler.go` (`MemScheduler`), `broadcast.go` (`SignalBus`),
  `definition_registry.go`, `taskservice.go`.

## Tracked follow-ups (discovered during execution — address before / during productionization)

These are deliberately deferred, not bugs in the shipped scope. The most important first:

1. **Nested-scope compensation (MUST-FIX before relying on compensation in nested sagas).** A completed
   sub-process scope's `Scope.Compensations` are dropped on `closeScope`, so `CompensateRequested`
   (which today targets only `RootCompensations`) cannot roll back activities that ran inside a
   now-closed sub-process. Fix: on regular sub-process exit, hoist the closing scope's `Compensations`
   into the parent (or an archive keyed by closed scope id) **before** `closeScope`; make
   `CompensateRequested`/the reserved `Compensate{ScopeID,FromNode}` command scope-targetable. (Plan-8
   e2e is flat/root-level, so nothing regresses today.)
2. **`Compensate` command is reserved/inert.** `Compensate{ScopeID,FromNode}` is in the sealed set but
   not emitted or consumed (godoc says so honestly). Wire it as part of (1).
3. **Async call activity.** `perform StartSubInstance` runs the child **synchronously** via `r.Run`; a
   child that parks (human task/timer/signal) returns a clear "synchronous runner does not support
   parked children" error. True async call activity (parent stays parked; `SubInstanceCompleted`
   delivered when the child finishes independently) is a later architectural change. Child instance id
   is linear (`<parent>-sub-c<n>`); depth guard = 64.
4. **Typed/paired gateway validation.** `model.Validate` doesn't distinguish a *converging* vs
   *diverging* gateway by incoming-count, so a mis-authored gateway can misroute silently. Add a
   structural rule when typed gateways / a diamond-validation pass lands.
5. **Inner-scope topology tests.** Scope propagation through forks/boundaries/event-gateways/SLA timers
   *inside* a sub-process is code-correct; only parallel-fork-in-subprocess has a dedicated test. Add
   tests for boundary/event-gateway/inclusive/SLA inside a sub-process.
6. **Retry/backoff/poison executor.** `ActionFailed.Retryable` + `InvokeAction.RetryPolicy` are carried
   but the retry executor (backoff, max attempts, poison queue) is a runtime/productionization concern.
7. **Minor test hardening** (non-blocking): a few `*_example_test.go`-bundled unit tests could move to
   same-named files (project convention is 1:1, see the test-file-naming memory); root-level event
   sub-process and message-arm-gateway paths have light coverage.

## Persistence (PostgreSQL) sub-project — ✅ COMPLETE, merged to `main`

Built on branch `feat/persistence-postgres` (range `fb39a87..9f9ab0f`), reviewed (per-task + opus
whole-branch), merged to `main`. All 10 plan tasks complete. Design: spec
`docs/specs/2026-06-21-persistence-postgres-design.md`, plan `docs/plans/2026-06-21-persistence-postgres.md`,
ADRs 0006–0008.
Gate: `go test -race ./...` green, total coverage 87.3% (model 96.2%, runtime 94.6%,
`internal/persistence/postgres` 86.0%, `persistence` 100%), lint clean (0 issues), no forbidden imports
(`watermill`/`casbin`/`gocron`/`clockwork`) in production code.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `runtime/` port collapse | Replaced 3 separate in-memory ports (`StateStore`/`Journal`/`OutboxWriter`) with a single transactional `Store` (`Create`/`Load`/`Commit`) + `JournalReader`; per-applied-trigger atomic commit with optimistic-version CAS (`ErrConcurrentUpdate`); outbox events derived by the pure `outboxEventsFor` helper. `runtime.Publisher`/`CachingDefinitionRegistry` (read-through TTL + singleflight; definitions immutable per `id:version` so no invalidation). | ADR-0007; `MemStore` is the in-memory reference impl |
| `internal/persistence/postgres/` | Postgres `Store`: transactional snapshot-JSONB writes with optimistic-CAS + journal + outbox in one tx; `DefinitionStore`; broker-agnostic outbox `Relay` (`FOR UPDATE SKIP LOCKED`, at-least-once); goose migrations — **4 tables: `wrkflw_instances`, `wrkflw_journal`, `wrkflw_outbox`, `wrkflw_definitions`**; trigger codec (`MarshalTrigger`/`UnmarshalTrigger`, exhaustive over the 13 sealed variants) | ADR-0006, ADR-0008 |
| `persistence/` root façade | `OpenPostgres`, `Migrate`, `NewRelay`, `NewDefinitionStore` — **all return stable port/interface types** (`Store`, `DefinitionStore`, `Relay` interfaces), never internal concrete structs (ADR-0008); sentinel aliases `ErrInstanceNotFound`/`ErrConcurrentUpdate` | ADR-0008 |
| `database/` | `RunTestDatabase` testcontainers helper — shared by all Postgres integration tests; returns a `*pgxpool.Pool` backed by `postgres:17` | test-helper-only package (0% own coverage is expected) |
| `model/` | `NodeKind` now has name-based JSON (`MarshalJSON`/`UnmarshalJSON`) so the persisted definition format is stable against iota reordering | whole-branch-review fix |

### Key design decisions (ADRs)
- **ADR-0006** — snapshot-as-JSONB storage shape (one row per instance: JSONB `snapshot` source-of-truth + plain engine-written projected columns `status`/`def_id`/`version`/timestamps for indexed admin queries; no tree normalization in v1).
- **ADR-0007** — per-step transactional `Store`: three runtime ports collapsed into one so `Commit` writes snapshot + journal + outbox atomically per applied trigger; optimistic CAS keeps the engine pure (no concurrency token in `InstanceState`).
- **ADR-0008** — `persistence` (root façade) over `internal/persistence/postgres` (impl): consumers import only the root package, which exposes interface types; all pgx/goose wiring stays unexported.

### Relay design note
The `Relay` is broker-agnostic: it polls the outbox with `FOR UPDATE SKIP LOCKED` and calls a `runtime.Publisher` interface (re-exported as `persistence.Publisher`). The **Eventing** sub-project will provide a watermill-backed `Publisher` — watermill is never imported here.

### Deferred follow-ups (deliberate, not bugs — backlog for later sub-projects)
1. **Owned-instance state cache** — instance state is fetched from Postgres on every `Run`/`Deliver`. A *single-writer* (instance-leased) cache is the only safe way to cache mutable state (a version-CAS protects writes but stale reads already fire side-effects); deferred. v1 caches only immutable definitions.
2. **History / snapshot cap** — the `snapshot` JSONB grows with inline `History`; an optional retention cap is deferred (the journal `wrkflw_journal` remains the unbounded audit source).
3. **LISTEN/NOTIFY relay trigger** — relay polls on a fixed interval; a Postgres `LISTEN`/`NOTIFY` push would cut latency/DB load (layered on the poll fallback).
4. **Per-aggregate relay ordering** — `SKIP LOCKED` gives throughput, not strict per-instance order; partition claiming by `instance_id` if strict in-order delivery is needed.
5. **Retry/DLQ + relay head-of-line** — full-batch rollback on a publish error means a persistent poison event blocks its batch (at-least-once intact). Poison isolation / retry-backoff executor is the resilience sub-project.
6. **Parked-async persistence resume e2e** — the capstone e2e is start→end; add a test that parks on a timer/boundary, reloads from Postgres via a fresh `Store`, advances the fake clock, and resumes (the JSON round-trip of `Timers`/`ArmedEvents`/`Boundaries`/`EventSubprocesses` is structurally correct — exported fields — but only e2e-proven for the sync path). **Highest-value missing test.**
7. **TOAST / fillfactor tuning** — the per-transition snapshot rewrite causes TOAST write amplification; lower `fillfactor` on `wrkflw_instances` + autovacuum tuning is a DBA step.
8. **Numeric fidelity** — process-variable integers round-trip from JSONB as `float64` (standard `encoding/json`, documented spec §7); `json.Decoder.UseNumber()` is the escape hatch if a consumer needs int fidelity.
9. **Instance-snapshot int enums** — `Status`/`TokenState`/`TimerKind` still serialize as ints in the snapshot (self-consistent within a version, unlike the now-name-based `NodeKind`); name-encode them too if cross-version snapshot stability is ever needed.
10. **`DefinitionRegistry.Lookup` lacks `ctx`** — the Postgres impl uses `context.Background()`; adding `ctx` to the port is a follow-up.

---

## Scheduling (gocron) sub-project — ✅ COMPLETE, merged to `main`

Built on branch `feat/scheduling-gocron` (HEAD `87c0ca6`, including the whole-branch-review
fix wave). Design: spec
`docs/specs/2026-06-21-scheduling-gocron-design.md`, plan `docs/plans/2026-06-21-scheduling-gocron.md`,
ADR-0009.
Gate: `go test -race ./...` green, `internal/scheduling/gocron` 87.5%, `scheduling` 85.7%,
`runtime` 94.5%, lint clean (0 issues), gocron not imported from `engine`/`runtime`/`model`
production code, clockwork not in `engine`/`runtime`/`model` production code.

### Whole-branch-review fixes (post-implementation, HEAD `87c0ca6`)

- **R4a (`runtime/memstore.go`)** — `MemStore` is now goroutine-safe (`sync.RWMutex` guards
  all five methods). The async scheduler makes concurrent `Deliver` real, so the e2e
  `syncStore` wrapper was removed in favour of `runtime.NewMemStore()` directly.
- **R4b (`runtime/runner.go`)** — the timer-fire callback no longer silently drops
  `TimerFired` on a CAS conflict: it now retries `Deliver` (reload-per-attempt) up to 5 times
  on `ErrConcurrentUpdate`, logging loudly if all attempts are exhausted. Non-CAS errors keep
  the single log-and-return behaviour.
- **Minor (`internal/scheduling/gocron/scheduler.go`)** — a non-future `fireAt` now fires
  immediately (`gocron.OneTimeJobStartImmediately()`) instead of being dropped.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `internal/scheduling/gocron/` | `GocronScheduler` — implements `runtime.Scheduler` backed by gocron v2.21.2; mutex-guarded `timerID→uuid` map; `Schedule` replaces any existing job for the same timer (cancel + re-add); `Cancel` is a no-op for unknown IDs (`ErrJobNotFound`-safe); `Close` calls gocron `Shutdown`; `AfterJobRuns` hook cleans the map entry after the job fires so the map stays bounded. Shares the same `clockwork.Clock` instance as the `Runner` so a single `FakeClock.Advance` drives both the engine and the scheduler in tests (ADR-0003). | ADR-0009 |
| `scheduling/` root façade | `NewScheduler(clock, ...Option) (runtime.Scheduler, io.Closer)` — consumers import only this root package; compile-time `var _ runtime.Scheduler` + `var _ io.Closer` assertions guard the contract. Never exposes internal gocron types. | ADR-0009 |
| `runtime/` | `MemScheduler` retained — tests that require only the pure engine (no gocron dependency) still use it. `runner.go` already accepts `runtime.Scheduler`. The whole-branch-review fix wave added two runtime changes (see "Whole-branch-review fixes"): a goroutine-safe `MemStore` (R4a) and bounded retry-with-reload on the timer-fire `Deliver` (R4b). | |
| Tests | Unit tests for `GocronScheduler` use `clockwork.NewFakeClock`, `BlockUntilContext` arm barrier before `Advance`, and synchronize on actual callback execution via WaitGroup/channel (not on `Advance` returning). Capstone e2e: one shared fake clock is both the runner's `clock.Clock` and the scheduler's `clockwork.Clock`; a timer-waiter process drives start→fire→resume→`StatusCompleted`. | |

### Key design decision (ADR)

- **ADR-0009** — `scheduling` root façade over `internal/scheduling/gocron` (impl): the same
  layer pattern as ADR-0008 for persistence — consumers import only the façade, which returns
  `runtime.Scheduler` + `io.Closer`; all gocron wiring stays unexported. Ensures gocron is
  **never imported transitively from engine/runtime/model code**.

### Deferred follow-ups

1. **Timer-fire CAS-drop [HIGH]** — `runner.go`'s `ScheduleTimer` fire callback only LOGS
   `ErrConcurrentUpdate`, silently dropping the `TimerFired` trigger under concurrent `Deliver`.
   Needs retry-with-reload on the fire path so a lost timer-fire is retried rather than silently
   discarded.
2. **`runtime.MemStore` not goroutine-safe** — async schedulers make concurrent `Deliver` real
   (the fire callback runs on a gocron goroutine while the caller may be in `Run`/`Deliver`).
   Add a mutex or a `runtime.NewSyncStore` wrapper so MemStore is safe under real concurrency.
3. **Rehydration on restart** — timers persist in `InstanceState.Timers` (snapshot-JSONB via
   the Postgres store) but full re-arming on startup requires a persistence "list pending timers"
   enumeration query. Not built in v1: a restart loses in-memory gocron jobs until rehydration
   lands. The Persistence `Store` is the prerequisite.

---

## Authorization (casbin) sub-project — ✅ COMPLETE, merged to `main`

Built on branch `feat/authz-casbin` (HEAD `a7edee0`). Design: spec
`docs/specs/2026-06-21-authz-casbin-design.md`, plan `docs/plans/2026-06-21-authz-casbin.md`,
ADR-0010.
Gate: `go test -race ./...` green, total coverage **87.5%** (`internal/authz/casbin` 92.0%,
`casbinauthz` 85.7%, `authz` 100%, `runtime` 94.5%, `humantask` 100%), lint clean (0 issues),
casbin not imported outside `internal/authz/casbin` + `casbinauthz`, pinned at `v2.135.0`.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `internal/authz/casbin/` | `*Authorizer` wrapping `*casbin.SyncedEnforcer` — **hybrid evaluator**: (1) role check with hierarchy via `GetImplicitRolesForUser` (degrades to `RoleAuthorizer` any-match with no `g` policy); (2) resource-privilege via `Enforce(sub, obj, act)` per privilege (activates the previously-reserved `AuthzSpec.Privileges` field); (3) attribute predicate via `expreval` over `{actor, vars}` (preserves the `expr-lang` dialect — no govaluate fork). Empty spec → allow. Deny / failed check → `authz.ErrNotAuthorized` (fail-closed). Casbin engine error → plain wrapped error (distinguishable from "policy says no"). `SyncedEnforcer` makes concurrent authorizations race-safe. | ADR-0010; only casbin imports in the codebase |
| `casbinauthz/` root façade | `NewCasbinAuthorizer(e *casbin.SyncedEnforcer) authz.Authorizer` (consumer-owned enforcer) + `NewCasbinAuthorizerFromStrings(modelText, policyText string) (authz.Authorizer, error)` (builds enforcer internally; `DefaultModel` used when `modelText` is empty). Both **return the `authz.Authorizer` interface** — no internal-concrete leak. `ReloadPolicy() error` available via type assertion for hot-reload. Compile-time `var _ authz.Authorizer` guard. | ADR-0008 template; ADR-0010 |
| `humantask/` | Added `Vars map[string]any` field to `HumanTask` to carry a process-variable snapshot. | Prerequisite for attribute-based-over-data-variables |
| `runtime/` | `perform engine.AwaitHuman` snapshots a **defensive copy** of `st.Variables` into the `HumanTask.Vars` at task creation time; `TaskService.Claim`/`Reassign`/`Complete` pass `task.Vars` (not `nil`) to `Authorize`. Makes attribute eligibility deterministic and auditable (evaluated against task-creation-time state). | ADR-0010 §Vars plumbing |
| `authz/` | **Unchanged** — `AllowAll`/`RoleAuthorizer` retained as pure built-ins; casbin NOT added. | The pure `authz` package stays stdlib + expreval only |

### Key design decision (ADR)

- **ADR-0010** — hybrid casbin + expr evaluator behind the `authz.Authorizer` port, following
  the ADR-0008 façade/internal template. Preserves the `expr-lang` dialect for attribute
  predicates (avoiding a govaluate fork); adds role-hierarchy and resource-privilege on top.
  `SyncedEnforcer` for race safety. Deny → fail-closed `ErrNotAuthorized`. `AllowAll`/`RoleAuthorizer`
  retained as pure built-ins — casbin is an additional implementation, not a replacement.

### Deferred follow-ups

1. **DB policy adapter** — v1 accepts model+policy as strings or a consumer-built `SyncedEnforcer`. A **pgx/gorm/sqlx casbin adapter** loading policy from the `wrkflw` Postgres database (with a watcher for multi-node reload) is a follow-up. The façade is adapter-agnostic — landing this requires only a new `casbinauthz.NewCasbinAuthorizerFromDB(pool, ...)` constructor.
2. **casbin ABAC-in-matchers** — the `Attribute` predicate today runs via `expreval` *outside* the casbin matcher (to keep the expr dialect). A casbin-native ABAC path (govaluate expression in the matcher, vars injected as subject attributes) is a viable alternative for consumers who prefer unified policy files; deferred because it forks the expression dialect.
3. **Shallow snapshot caveat** — `HumanTask.Vars` is a top-level-keys defensive copy of `st.Variables`: top-level scalars/strings/bools are independent copies, but any nested `map[string]any` values remain aliased to the instance state. Only top-level scalars are safe from mutation; deeply nested vars may reflect later engine writes. Full deep-copy (e.g. `encoding/json` round-trip) is the follow-up if nested-map fidelity is required.
4. **Richer resource modeling for Privileges** — `AuthzSpec.Privileges` today is a `[]string` carrying space-delimited `"obj act"` tokens; the `casbinauthz` façade splits on space into `(obj, act)` for `Enforce`. Richer modeling (domains/tenants, object hierarchies, wildcard policies) is a follow-up once the DB adapter provides a policy-management API.
5. **Sensitive-variable persisting in task snapshot** — `HumanTask.Vars` snapshots ALL top-level process variables into the task record; once a Postgres-backed `TaskStore` lands (currently in-memory), this would persist potentially sensitive process data (PII/secrets) — consider a snapshot allowlist or redaction before persisting tasks.

---

## Eventing (watermill) sub-project — ✅ COMPLETE

Built on branch `feat/eventing-watermill` (HEAD `38a1a47`). Design: spec
`docs/specs/2026-06-21-eventing-watermill-design.md`, plan `docs/plans/2026-06-21-eventing-watermill.md`,
ADR-0012 (watermill adapter behind the `eventing` façade; never imported from engine/model/runtime).
Gate: `go test -race ./...` green (all packages including `internal/persistence/postgres` via Docker),
total coverage on touched packages **97.6%** (`eventing` 100%, `internal/eventing/watermill` 96.6%),
lint clean (0 issues), watermill not present in `engine`/`model`/`runtime` (vendor-isolation guard: CLEAN).
watermill v1.5.2, OTel API v1.43.0 (SDK only in test files).

### What shipped

| Layer | What | Notes |
|---|---|---|
| `runtime/` — `OutboxEvent` extended | Added `DedupKey string` and `InstanceID string` fields to `runtime.OutboxEvent` so the watermill adapter can set a stable message UUID and per-instance metadata without reaching into engine internals. | Task 1 |
| `internal/persistence/postgres/relay.go` | Relay scans and maps `DedupKey`/`InstanceID` columns from `wrkflw_outbox`; a column-order comment documents the `rows.Scan` projection order. | Task 1 |
| `internal/eventing/watermill/` | `Publisher` adapter: maps one `OutboxEvent` → one watermill message (DedupKey→UUID, InstanceID→metadata); emits one OTel span (`eventing.publish`) and increments `wrkflw_eventing_published_total` counter (attributes: `status=ok/error`); records error status on the span on failure. `NewWatermillLogger` slog bridge: forwards watermill's `Info`/`Debug`/`Trace`/`Error`/`With` to a `*slog.Logger`. `WithLogger`/`WithTracerProvider`/`WithMeterProvider` options. | Tasks 2–3 |
| `eventing/` root façade | `NewPublisher(pub, ...Option) runtime.Publisher` — wraps any `message.Publisher` as a `runtime.Publisher`; `NewGoChannelPublisher(...Option) (runtime.Publisher, message.Subscriber, io.Closer)` — in-process GoChannel for tests and simple deployments. Façade re-exports the three option constructors; watermill is confined to this package and `internal/eventing/watermill`. Compile-time `var _ runtime.Publisher` guard. | Tasks 4–5 |
| GoChannel e2e | `ExampleNewGoChannelPublisher` exercises start→subscribe→publish→receive in a single in-process test; confirms message UUID, metadata, and payload round-trip correctly. | Task 5 |

### Key design decision (ADR)

- **ADR-0012** — watermill adapter behind the `eventing` façade (same layer pattern as ADR-0008/ADR-0009): consumers import only `eventing.NewPublisher`/`NewGoChannelPublisher` which return `runtime.Publisher`; all watermill wiring stays in `internal/eventing/watermill`. Ensures watermill is never transitively imported from engine/model/runtime code.

### Deferred follow-ups

1. **Broker-specific constructors** — `NewGoChannelPublisher` ships for in-process use; production deployments need constructor helpers for Kafka, NATS, AWS SNS, etc. Each is a thin `eventing.NewKafkaPublisher(cfg, ...Option)` wrapping the corresponding watermill adapter. Deferred to a broker-specific sub-project.
2. **Richer event envelope / topic-mapping** — the current mapping uses `ev.Topic` verbatim as the watermill topic and stores it in `Metadata["topic"]`. A topic-routing function option (e.g. `WithTopicMapper(fn)`) and a richer envelope schema (schema version, event type, causation/correlation IDs) are deferred.
3. **Retry / DLQ poison isolation** — the `Relay` rolls back the entire batch on a publisher error (head-of-line blocking). Poison-event isolation and retry-with-backoff are the resilience sub-project; the relay deliberately defers this.
4. **Optional LISTEN/NOTIFY relay trigger** — the relay polls on a fixed interval; a Postgres `LISTEN`/`NOTIFY` push would cut latency and DB load (layered on the poll fallback). Deferred from the Persistence sub-project and still unbuilt.

---

## What's next: productionization sub-projects (each its own brainstorm → spec → plan → SDD cycle)

The engine core depends on interfaces only. The next sub-projects implement them (per CLAUDE.md):

- **Persistence** — ✅ COMPLETE, merged to `main`. See section above.
- **Eventing** — ✅ COMPLETE, merged to `main`. See "Eventing (watermill) sub-project" section above.
- **Scheduling** — ✅ COMPLETE, merged to `main`. See "Scheduling (gocron) sub-project" section above.
- **Authorization** — ✅ COMPLETE, merged to `main`. See "Authorization (casbin) sub-project" section above.
- **Transports** — ✅ COMPLETE, merged to `main`. See "Transports (REST/gRPC) sub-project" section below.
- **Admin monitoring** + **`ProcessInstance` response customization** — ✅ included in the Transports sub-project (admin middleware + keyset pagination + `WithInstanceMapper` response customization).

## How to run the next sub-project (the workflow that built Plans 1–8)

This repo was built with **subagent-driven development**, autonomously, per the user's cadence
(`working-cadence-autonomous-sdd` memory): brainstorm → spec (`docs/specs/`) → plan (`docs/plans/`) →
execute, with ADRs for significant decisions, branch → SDD → merge to `main` → push.

1. **Brainstorm + spec + plan** the sub-project (`superpowers:brainstorming` → `writing-plans`).
2. **Branch:** `git switch -c feat/<sub-project-slug>` (never implement on `main`).
3. **Execute with SDD** (`superpowers:subagent-driven-development`): per task — `scripts/task-brief PLAN N`
   → dispatch a fresh implementer (TDD, visible RED→GREEN) → `scripts/review-package BASE HEAD` → dispatch
   a task reviewer → fix Critical/Important → re-review → mark done in `.superpowers/sdd/progress.md`. The
   scripts live under the SDD plugin dir. Cheap model for transcription, sonnet for integration, **opus
   for the final whole-branch review**. Always set the model explicitly.
4. **Finish:** final whole-branch review (opus) → `superpowers:finishing-a-development-branch` → merge to
   `main`, push, delete the branch.

### Binding conventions (learned the hard way across Plans 1–8)

- **TDD strict** — every new symbol gets a failing test first with a **visible RED** before the impl
  (CLAUDE.md "TDD Operational Discipline"). The SDD per-task flow makes this auditable.
- **Tests:** black-box (`package <pkg>_test`); table-driven with an **`assert` closure per case** (project
  `table-test` skill, *not* `want`/`wantErr`); `t.Context()`; **pair each `foo.go` with `foo_test.go`**
  (reserve `*_example_test.go` for genuine e2e — see the test-file-naming memory).
- **Lint:** `golangci-lint` is **v2** (`.golangci.yml`, `version: "2"`).
- **Verify on completion:** `go test -race ./...` green; coverage ≥ 85% on touched packages; lint clean.
- **Commits:** Conventional Commits scoped to the area; end with the
  `Co-Authored-By: Claude Opus 4.8 (1M context)` trailer. Commit per logical change.
- **Hard-won lesson:** the plan's example code can be wrong — **trust the test, not the plan listing**;
  observe the red state. Ground every edit against the then-current code (the engine grew a lot).
- **gitignored scratch:** `cover.out`, `.superpowers/` (SDD briefs/reports/ledger/diffs). Don't commit them.

---

## Transports (REST/gRPC) sub-project — ✅ COMPLETE, merged to `main`

Built on branch `feat/transports-rest-grpc` (HEAD `24a4644`). Design: spec
`docs/specs/2026-06-21-transports-rest-grpc-design.md`, plan `docs/plans/2026-06-21-transports-rest-grpc.md`,
ADRs 0011 (consumer-mounted transports, no shipped binary) and 0004 (public packages at module root).
Gate: `go test -race ./...` green, `service` 86.7%, `transport/rest` 90.1%, `transport/grpc` 86.0%
(`workflowpb` generated package excluded from bar — 0% expected), `runtime` 94.7%, lint clean (0 issues),
no grpc/protobuf/net-http in `engine`/`model`; `service` is transport-neutral.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `runtime/` — `InstanceLister` port | `InstanceLister` interface (`List(ctx, cursor, limit, filters) ([]InstanceSummary, nextCursor string, err)`) with keyset cursor codec (`encodeCursor`/`decodeCursor`, base64url-encoded `id:created_at` pairs). `MemStore` implements `InstanceLister` via an in-memory sorted scan; `internal/persistence/postgres` implements it with a keyset SQL query. `InstanceSummary` carries `ID`, `DefID`, `DefVersion`, `Status`, `CreatedAt`, `EndedAt`, `Variables`. | Task 1 |
| `service/` — transport-agnostic facade | `service.New(runner, lister, reg) *Service` — eight operations: `StartInstance`, `GetInstance`, `DeliverSignal`, `DeliverMessage`, `ClaimTask`, `ReassignTask`, `CompleteTask`, `ListInstances`. Resolves `ProcessDefinition` by `DefID:DefVersion` from the registry; passes all instance ops through `runtime.Runner`. Fully usable without any transport import. Note: `CancelInstance` is NOT in v1 — it is a deferred follow-up. | Task 2 |
| `transport/rest` — stdlib HTTP handler | `rest.NewHandler(svc, ...Option) http.Handler` — stdlib `*http.ServeMux` (no third-party router), mountable under `http.StripPrefix`. Routes: `POST /instances`, `GET /instances/{id}`, `POST /instances/{id}/signals`, `POST /messages`, `POST /tasks/{token}/claim`, `POST /tasks/{token}/complete`, `POST /tasks/{token}/reassign`. There is no `POST /instances/{id}/cancel` and no unauthenticated `GET /instances` — the only list route is `GET /admin/instances` (admin only, see Task 4). `WithInstanceMapper(fn)` customizes the `InstanceResponse` shape applied to ALL instance-returning endpoints. `WriteHTTPError(w, err)` maps sentinel errors to HTTP status codes. | Task 3 |
| `transport/rest` — admin monitoring | `GET /admin/instances` — keyset-paginated instance list scoped to admin callers; `?status=`, `?limit=` filters; cursor-based `next_cursor` in response. `WithAdminMiddleware(mw)` installs a middleware gate on all `/admin/*` routes; default-deny (403) when no middleware is configured. Admin routes are fully separated from consumer routes so the consumer can mount them on a different sub-path or omit them. | Task 4 |
| `transport/grpc` — gRPC service | `workflowpb` package: `.proto` at `transport/grpc/proto/workflow.proto`; committed generated `workflow.pb.go` + `workflow_grpc.pb.go` via `protoc` (no `buf` needed at build time). `RegisterWorkflowServiceServer(s grpc.ServiceRegistrar, svc *service.Service)` registers the implementation. `mapToGRPCStatus(err) error` translates sentinel errors to gRPC status codes. Tested end-to-end via `google.golang.org/grpc/interop/grpc_testing` + `bufconn` in-process dialer (no real network). | Task 5 |

### Key design decisions (ADRs)

- **ADR-0011** — consumer-mounted transports, no shipped binary. Both `rest.NewHandler` and
  `RegisterWorkflowServiceServer` return/register into caller-provided server infrastructure. The
  consumer chooses how to compose, secure, and start the server. `wrkflw` ships no `main`.
- **ADR-0004** — public packages at module root (no `pkg/` prefix). `service/`, `transport/rest/`,
  `transport/grpc/` are all importable directly as `github.com/zakyalvan/krtlwrkflw/service`, etc.

### Deferred follow-ups (deliberate, not bugs)

1. **422/FailedPrecondition for wrong-state transitions** — currently wrong-state errors (e.g. claiming
   a completed task, cancelling a finished instance) map to 500/Internal and `codes.Internal` because
   there is no typed wrong-state sentinel yet. Add a sentinel (e.g. `ErrWrongState`) in `service/` and
   wire it to 422/`FailedPrecondition` in both transport layers.
2. **`DeliverMessage` requires a `*ProcessDefinition` from the caller** — `Runner.DeliverMessage`
   accepts a `*model.ProcessDefinition`; the `service.Service` facade currently requires the caller to
   supply a `DefRef` (id + version) so it can resolve the definition. A cleaner API would have the
   runner/facade look up the definition from the matched waiting instance directly. Deferred until
   the runner's `Deliver` port is revisited.
3. **REST deny-body Content-Type** — FIXED (pre-merge): `denyAllMiddleware` now emits a proper
   JSON body with `Content-Type: application/json` via `writeJSON`.
4. **Admin pagination total-count assertion** — the no-skip (first-page) assertion in the admin list
   tests does not cover total-count (no `X-Total-Count` header or `total` field shipped). Add total-count
   if admin UI needs it.
5. **Streaming/watch endpoints, OpenAPI/grpc-gateway, richer admin filters** — the current surface is
   request/response only. Server-streaming (watch for instance-state changes), an OpenAPI spec (via
   grpc-gateway or swag), and richer admin query filters (date range, def-id filter, multi-status) are
   follow-up features.
6. **`ended_at` non-optional in proto** — `EndedAt` is a `google.protobuf.Timestamp` (nullable) in the
   proto but non-optional in `InstanceSummary.EndedAt time.Time`; the gRPC mapping emits a zero-value
   timestamp for running instances. Make `EndedAt` a `*time.Time` in `InstanceSummary` or add a separate
   `has_ended` bool in the proto to avoid the zero-timestamp ambiguity.

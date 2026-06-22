# wrkflw Engine Core — Execution Handover

This document lets a **fresh session with zero prior context** understand the state of `wrkflw`
and pick up the next work. Read it top to bottom before starting.

## ⏩ CURRENT RESUME POINT (read this first) — updated 2026-06-22

> **Fresh session: jump to the "🧭 START HERE (fresh session) — consolidated backlog" section
> below (just after the gate note).** It is the single prioritized entry point for new work. The
> rest of this doc is the per-track detail behind it. Nothing *named* is in flight — `main` is
> green and all work through ADR-0027 is merged (or in final merge for the timer-rehydration track).

**Where we are:** the engine core (Plans 1–8), **all 5 productionization sub-projects**
(Persistence, Scheduling, Authorization, Transports, Eventing), **all 4 deferred-backlog tracks**
(Correctness → Resilience → Observability → Performance/caching), and **all 3 "also-outstanding"
items** (flaky-singleflight fix, DB casbin adapter, true async call activity) are merged to `main`,
plus the **engine wrong-state sentinel + `workflow-` prefix sweep** track (ADR-0026) and the
**timer rehydration on restart** track (ADR-0027, branch `feat/timer-rehydration`). ADRs 0001–0027.
**No named work remains in flight.** Future work = the consolidated backlog
(below). Each item is its own track:
`brainstorm → spec (docs/specs/) → ADR(s) (docs/adr/, next #0026) → plan (docs/plans/) → branch →
SDD → opus whole-branch review → merge to main → push`. Confirm scope with the user first.

**Tracks (run in this order; the first is done):**

1. **Correctness & tests** — ✅ COMPLETE, merged `314358c` (2026-06-21). See the
   "Correctness & tests hardening sub-project" section below.
2. **Resilience (retry/backoff/DLQ)** — ✅ COMPLETE, merged (2026-06-21). The named REQUIREMENTS
   feature ("A process error must be able to be retried"). Engine-modeled retry executor,
   catch-flow→incident exhaustion, outbox relay poison isolation + DLQ, idempotency. ADRs
   0015–0018. See the "Resilience (retry/backoff/DLQ) sub-project" section below.
3. **Observability** — ✅ COMPLETE, merged (2026-06-22). Metrics + traces + slog across
   runtime/transports/scheduling/eventing/persistence-relay (REQUIREMENTS line 17). ADR-0019.
   See the "Observability (metrics/traces/slog) sub-project" section below.
4. **Performance/caching** — ✅ COMPLETE, branch `feat/performance-caching` (2026-06-22).
   Owned-instance write-through cache (`CachingStore`), history/snapshot cap (`WithHistoryCap`),
   LISTEN/NOTIFY relay wakeup (`WithOutboxNotify` + `WithListenNotify`), advisory-lock
   multi-process ownership (`NewAdvisoryLockOwnership`). ADRs 0020–0022.
   See the "Performance/caching sub-project" section below.
5. **"Also outstanding" — ✅ ALL DONE (2026-06-22):**
   - **DB casbin policy adapter (Authz deferred #1)** — ✅ DONE, branch `feat/casbin-db-adapter`.
     See "DB casbin policy adapter" section below. ADR-0023.
   - **True async call activity (engine follow-up #3)** — ✅ DONE, branch `feat/async-call-activity`.
     See "True async call activity" section below. ADRs 0024–0025. Engine/model UNTOUCHED.
   - **Pre-existing flaky singleflight test** — ✅ FIXED (a real TOCTOU in `CachingDefinitionRegistry.Lookup`,
     not a test barrier — see tracked-follow-up #8 below).
   The productionization run (5 sub-projects) and the deferred-backlog run (Correctness → Resilience →
   Observability → Performance/caching) plus these three "also outstanding" items are ALL complete.
   Future work is the per-section deferred follow-ups recorded in each track's section below.

**How to execute a track:** follow "How to run the next sub-project" + "Binding conventions"
sections below (subagent-driven development, visible RED→GREEN per task, opus final review). The
per-track spec/plan live under `docs/specs/` and `docs/plans/` (never a path containing
"superpowers"). The cross-session memory file `productionization-run` also tracks this run.

**Gate after every track:** `go test -race ./...` green; ≥85% on touched packages;
`golangci-lint run ./...` clean; engine/model purity intact (no transport/vendor imports).

---

## 🧭 START HERE (fresh session) — consolidated backlog

**Current state:** everything through **ADR-0027** is on `main`/in-merge (productionization ×5 +
deferred-backlog ×4 + the 3 "also-outstanding" items + the **engine wrong-state sentinel** and
**timer rehydration** tracks). No *named* work remains. `main` is green (`go test -race ./...`,
`golangci-lint`, engine/model untouched). **Convention note:** all production error messages carry a
**`workflow-`** prefix (e.g. `workflow-engine:`); assert on sentinels with `errors.Is`, never
string-matching — see the `error-sentinel-prefix` memory and ADR-0026. Pick the next piece of work
from the prioritized backlog below — each item is a self-contained track: **brainstorm → spec
(`docs/specs/`) → ADR (`docs/adr/`, next number **0028**) → plan (`docs/plans/`) → branch → SDD →
opus whole-branch review → merge + push**. Confirm scope with the user before starting. The full
per-item detail lives in the per-track "Deferred follow-ups" sections further down; this is the index.

**Recommended priority (top picks):**
1. **`CancelInstance`** end-to-end (engine/runtime → `service` → REST + gRPC). Most-requested missing
   operation. *(Transports)*
2. **gRPC `ResolveIncident` RPC + DLQ admin REST** (`GET /admin/dead-letters`, redrive) — the
   runtime/persistence APIs already exist; only the transport surface is unbuilt. *(Resilience)*
3. **Reachability / fork-join pairing validation** — extend `model.Validate` beyond the mixed-gateway
   rule (ADR-0014) to match converging joins to diverging forks + condition-placement checks. *(Correctness)*
4. **Multi-replica timer/call-link exclusivity** — `FOR UPDATE SKIP LOCKED` / ownership claim so
   `RehydrateTimers` + the call-link notifier don't double-process across replicas (today: correct but
   redundant via idempotency). *(Production-hardening — follow-up to ADR-0027/0024)*

**Backlog by theme** (✅-done items already removed; cite track for full detail below):
- **Correctness / robustness:** reachability/fork-join pairing validation;
  compensation-on-error/cancel paths; scope-targeted compensation (`Compensate` producer); casbin
  adapter/watcher `context` propagation; `AdvisoryLockOwnership` use-after-close guard; JSONB
  numeric/enum fidelity. *(engine wrong-state sentinel — ✅ DONE, ADR-0026, see section below.)*
- **Production-hardening:** cancellation propagation parent→child + orphaned-child
  cleanup; multi-replica `FOR UPDATE SKIP LOCKED` exclusivity (timers + call-links); lease-column ownership; per-worker NOTIFY
  fairness; `wrkflw_processed_message` pruning job; per-aggregate relay ordering; TOAST/fillfactor
  tuning; `RetryPolicy.Backoff` overflow guard; richer `SubInstanceFailed`→parent error (create an Incident).
- **Observability:** public `observability` root pkg (trace-correlating `slog.Handler` + `Setup`); Store
  Load/Commit spans + `wrkflw_store_duration_seconds`; CallNotifier `wrkflw.callnotifier.batch` span;
  async DB-backed `instances_active` gauge; REST/relay meters actually emitting; route-template span
  naming; exemplars; OTel-contrib option; migrate eventing onto the shared helper.
- **API / feature completeness:** `CancelInstance`; gRPC `ResolveIncident` + DLQ admin REST; casbin
  policy-admin REST/gRPC; broker-specific eventing constructors (Kafka/NATS/SNS) + richer envelope;
  streaming/watch + OpenAPI/grpc-gateway + richer admin filters; admin total-count; `ended_at` optional
  in proto; casbin ABAC-in-matchers; richer Privilege modeling; `DeliverMessage` self-resolving the def.
- **Performance / scale:** casbin `FilteredAdapter` + `WatcherEx`; per-definition history-cap + per-def
  `maxCallDepth`; cross-machine child execution; tunable watcher reconnect backoff.
- **Test / doc / cosmetic:** `HumanTask.Vars` deep-copy + sensitive-var redaction; `DefinitionRegistry.Lookup`
  ctx; `MarkNotified` clock injection; relay/listen establish-sleep→poll; residual hard-to-force infra
  branches; move bundled example-test unit tests to 1:1 files; misc godoc/test nits. NOTE: the repo has
  **pre-existing gofmt-unclean files** (golangci-lint v2 doesn't run gofmt) — a repo-wide `gofmt -w`
  sweep is an optional hygiene follow-up.

---

## Engine wrong-state sentinel + `workflow-` prefix sweep sub-project — ✅ COMPLETE

First track picked from the consolidated backlog (top pick #1). Built on branch
`feat/engine-wrong-state-sentinel`. Design: spec
`docs/specs/2026-06-22-engine-wrong-state-sentinel-design.md`, plan
`docs/plans/2026-06-22-engine-wrong-state-sentinel.md`, **ADR-0026**. 6 SDD tasks + opus
whole-branch review. Gate: `go test -race -p 1 ./...` green (incl. Postgres), touched pkgs ≥85%
(engine 85.6%, service 87.3%, transport/rest 91.1%, transport/grpc 87.6%, runtime 91.0%), lint 0,
engine/model purity PURE.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `engine/` | New `engine.ErrInvalidTransition` parent sentinel; `ErrTokenNotFound` **wraps** it (`errors.Is(ErrTokenNotFound, ErrInvalidTransition)` holds) so all seven wrong-state handlers are reclassifiable with **zero change to `Step`'s `(state, commands)` output** — pure error-chain enrichment. Sentinels relocated to `engine/errors.go` (+ `engine/errors_test.go` asserting the wrapping graph). `ErrNoMatchingFlow`/`ErrUnknownTrigger` deliberately do NOT wrap it (definition/infra errors → stay 500). | ADR-0026 |
| `service/` | `deliverTaskTrigger` (Claim/Complete/Reassign) classifies a leaked `engine.ErrInvalidTransition` into `service.ErrConflict` via **double-`%w` multi-wrap** (both sentinels stay inspectable), closing the race-to-500 gap. **NOT** applied to `DeliverSignal` (broadcast no-op) / `DeliverMessage` (waiter no-op) — those paths produce no wrong-state error (documented; YAGNI). | controller-adjudicated scope |
| `transport/rest` + `transport/grpc` | `engine.ErrInvalidTransition` added as a direct fallback in `classifyError` (→ 422 `conflict_state`) and `mapToGRPCStatus` (→ `codes.FailedPrecondition`), for consumers mounting a transport over a bare runner without the `service` facade. | |
| repo-wide | **`workflow-` error-prefix sweep:** every production `errors.New`/`fmt.Errorf` package-segment now prefixed `workflow-` (~187 sites across ~28 files + the 4 call-activity `fmt.Sprintf` `SubInstanceFailed` payloads); ~4 string-matching tests updated. Assert via `errors.Is`. New project convention (see `error-sentinel-prefix` memory). | |

### Deferred follow-ups
1. **Engine terminal-instance guard** — intentionally NOT added; `Step` already returns
   `ErrTokenNotFound` (→ `ErrInvalidTransition`) transitively for triggers to a finished instance.
   An explicit guard would change engine behavior; out of scope.
2. **Signal/message wrong-state classification** — re-add the `DeliverSignal`/`DeliverMessage`
   classification branches IF a future engine change makes signal/message delivery error on a
   no-match (today they broadcast/waiter no-op).
3. **`gofmt` hygiene** — the optional repo-wide `gofmt -w` sweep (noted in the backlog) is still open.
4. **Flaky `TestListenLoopExitsOnContextCancellation`** (`internal/persistence/postgres`) — pre-existing,
   timing-sensitive LISTEN/NOTIFY context-cancellation test; flakes under full-suite Docker contention
   but passes reliably in isolation (verified 3/3). Unrelated to this track (error-message-only changes).
   Follow-up: harden the test's cancellation barrier (synchronize on the loop's actual exit, not a sleep).

---

## Timer rehydration on restart sub-project — ✅ COMPLETE

Second track from the consolidated backlog (was top pick #1). Built on branch
`feat/timer-rehydration`. Design: spec `docs/specs/2026-06-22-timer-rehydration-design.md`, plan
`docs/plans/2026-06-22-timer-rehydration.md`, **ADR-0027**. 7 SDD tasks + opus whole-branch review.
Gate: `go test -race -p 1 ./...` green (incl. Postgres), touched pkgs ≥85% (runtime 90.2%,
`internal/persistence/postgres` 86.3%, persistence 88.0%), lint 0, **engine/model production diff
ZERO** (the load-bearing invariant — same as async call activity).

**The load-bearing finding:** `FireAt` is not persisted anywhere in `InstanceState` (`timerRecord`
and `Token` carry no fire time; several `ScheduleTimer` sites don't even write a `timerRecord`), so
rehydration required persisting it in a new location — solved with a runtime-owned side table written
in the commit tx, engine untouched (the ADR-0024/0025 call-link pattern).

### What shipped

| Layer | What | Notes |
|---|---|---|
| `runtime/` | `runtime.TimerStore` read port + `ArmedTimer{InstanceID,DefID,DefVersion,TimerID,FireAt,Kind}` + `MemTimerStore`; `AppliedStep.TimerArms/TimerCancels`; pure kind-agnostic `timerOpsFor` (ScheduleTimer→arm, CancelTimer/TimerFired→cancel) wired into deliverLoop (gated `r.timerStore != nil`); `MemStore`/`NewMemStoreWithTimers` record arms/cancels in Create+Commit. | ADR-0027 |
| `runtime/` | Fire-callback extracted into `r.armTimer(def,instanceID,timerID,fireAt)` (behavior-preserving — byte-for-byte) shared by `perform(ScheduleTimer)` and rehydration; **`Runner.RehydrateTimers(ctx)`** one-shot re-arm (lists armed timers, resolves def via registry, re-arms; skips+counts unresolved defs; requires `WithScheduler`+`WithTimerStore`+`WithDefinitions`). Opt-in via `WithTimerStore`. | |
| `internal/persistence/postgres/` | Migration `0005_timers.sql` (`wrkflw_timers` PK(instance_id,timer_id) + `fire_at` index); `upsertTimer`/`deleteTimer`/`applyTimerOps` applied in `Store.Create`+`Commit` **inside the tx before commit** (atomic with state/journal/outbox); `postgres.TimerStore.ListArmed` (ordered by fire_at). | |
| `persistence/` | `persistence.NewTimerStore(pool) runtime.TimerStore` façade + compile-time assertion. | |
| tests | Mem rehydration e2e (discard runner+scheduler → fresh → `RehydrateTimers` → advance → resume) and Postgres crash-safety e2e (fresh `Store`+`TimerStore`+`Runner` → `RehydrateTimers` + `Tick` → resume, **no manual `TimerFired` deliver**). | |

### Deferred follow-ups
1. **Multi-replica rehydration exclusivity** — two replicas both `RehydrateTimers` → double-arm →
   correct-but-redundant (idempotent re-fire). `FOR UPDATE SKIP LOCKED` / ownership claim is the
   follow-up (shared with the call-link notifier; now top pick #4).
2. **Orphan-row pruning** — defense-in-depth sweep for `wrkflw_timers` rows of instances that reached
   terminal without a clean cancel (the in-tx delete on fire/cancel should keep it clean).
3. **Rehydration observability** — count re-armed / span (align with the observability track).
4. **`Commit` `applyTimerOps` error not wrapped via `mapConflict`** — defensive consistency nit
   (the version CAS returns `ErrConcurrentUpdate` before timer ops run, so not a live bug).

---

## Status: engine-core sub-project #1 is COMPLETE ✅ (historical — see resume point above for current state)

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

1. **Nested-scope compensation — DONE (ADR-0013).** On regular sub-process exit, the closing
   scope's `Compensations` are now hoisted into the parent scope's compensation list before
   `closeScope` discards the child scope, so `CompensateRequested` can roll back activities that
   ran inside a now-closed sub-process. The `Correctness & tests hardening` sub-project
   (Task 1) implemented and tested this via `hoistCompensations(childID, parentID)` in `engine/step.go`.
2. **`Compensate` command is reserved for scope-targeted use.** `Compensate{ScopeID,FromNode}`
   is in the sealed set but not emitted or consumed (godoc says so honestly). It is reserved to
   be wired as the scope-targeted rollback command (companion to item 1 above) — a deliberate
   deferred follow-up.
3. **Async call activity.** `perform StartSubInstance` runs the child **synchronously** via `r.Run`; a
   child that parks (human task/timer/signal) returns a clear "synchronous runner does not support
   parked children" error. True async call activity (parent stays parked; `SubInstanceCompleted`
   delivered when the child finishes independently) is a later architectural change. Child instance id
   is linear (`<parent>-sub-c<n>`); depth guard = 64.
4. **Typed/paired gateway validation — PARTIALLY DONE (ADR-0014).** The mixed-gateway rule
   (a node may not mix both `KindExclusiveGateway` incoming/outgoing flows with parallel-join
   semantics) was added to `model.Validate` as `ErrMixedGateway` in the `Correctness & tests
   hardening` sub-project (Task 2). Full diverging-vs-converging structural validation (diamond
   pairing, reachability checks) remains a deferred follow-up.
5. **Inner-scope topology tests — DONE.** Tests for boundary-event, event-based gateway, inclusive
   gateway, and SLA-timer scope propagation *inside* a sub-process were added in the `Correctness
   & tests hardening` sub-project (Task 6). A `fireBoundaryArm` scope-resolution bug was found
   and fixed as part of this work (commit `82badcd`): the boundary outgoing flow was being resolved
   from the root definition instead of the containing sub-process scope.
6. **Retry/backoff/poison executor — DONE (ADRs 0015–0018).** Built in the `Resilience` sub-project:
   engine-modeled retry executor (retries ride the timer machinery; runtime-recorded jitter keeps
   `Step` deterministic), catch-flow→error-boundary→incident exhaustion, outbox relay per-row poison
   isolation + dead-letter quarantine, and idempotency (stable action key + `Deduper`). See the
   "Resilience (retry/backoff/DLQ) sub-project" section below.
7. **Minor test hardening** (non-blocking): a few `*_example_test.go`-bundled unit tests could move to
   same-named files (project convention is 1:1, see the test-file-naming memory); root-level event
   sub-process and message-arm-gateway paths have light coverage.
8. **Pre-existing flaky singleflight test — ✅ FIXED (2026-06-22, merge see below).** Root cause was a
   check-then-act gap in `CachingDefinitionRegistry.Lookup` between the fast-path cache check and
   `singleflight.Group.Do`: a straggler that missed the fast path could start a fresh flight after the
   first flight had already cached and freed the key, issuing a redundant `backing.Lookup` (2–4 calls
   observed under `-race`). Fixed by double-checking the cache at the top of the `Do` closure so any
   flight running after the cache is populated short-circuits — collapsing stragglers to exactly one
   backing call regardless of scheduling. Verified 500× `-race`. (Not a "timing-sensitive barrier" in
   the test as previously assumed — a real TOCTOU in the production code.)

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
6. **Parked-async persistence resume e2e — DONE.** `TestPostgresParkedTimerResumesAfterReload`
   and `TestPostgresParkedBoundaryResumesAfterReload` (in `internal/persistence/postgres/resume_test.go`)
   added in the `Correctness & tests hardening` sub-project (Task 5): parks on a timer/boundary,
   reloads from Postgres via a brand-new `Store`, advances the fake clock, and resumes to
   `StatusCompleted`. Proves the JSONB round-trip of `token.AwaitCommand` and `Boundaries`.
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

1. **422/FailedPrecondition for wrong-state transitions — DONE (`service.ErrConflict`).** Added
   `service.ErrConflict` sentinel in the `Correctness & tests hardening` sub-project (Tasks 3–4):
   `service.ClaimTask` and `service.DeliverSignal` wrap wrong-state errors with `ErrConflict`;
   `transport/rest` maps it to HTTP 422 with body error code `"conflict_state"`;
   `transport/grpc` maps it to `codes.FailedPrecondition`. The engine-level wrong-state sentinel
   (for callers using the engine directly, bypassing `service/`) remains a deferred follow-up.
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

---

## Correctness & tests hardening sub-project — ✅ COMPLETE, merged to `main`

First track of the **deferred-backlog run** (see the resume point at the top). Built on branch
`feat/correctness-hardening` (merge `314358c`, 2026-06-21). Design: spec
`docs/specs/2026-06-21-correctness-hardening-design.md`, plan
`docs/plans/2026-06-21-correctness-hardening.md`, ADRs 0013 (compensation hoist) and 0014
(mixed-gateway validation). 7 SDD tasks + opus whole-branch review (Ready to merge: Yes).
Gate: `go test -race ./...` green, touched pkgs all ≥85% (engine 85.4%, model 96.4%, service 86.6%,
transport/grpc 86.1%, transport/rest 90.1%, internal/persistence/postgres 86.1%), lint 0,
engine/model purity CLEAN.

### What shipped

| Work-stream | What | Notes |
|---|---|---|
| Nested-scope compensation **MUST-FIX** | `engine` hoists a closing sub-process scope's `Compensations` into its parent (root) in completion order *before* `closeScope` (`hoistCompensations` in `state.go`), so the existing root `CompensateRequested` walk rolls back completed-sub-process activities. No new `InstanceState` field; reverse-order saga semantics proven by ordering + two-level-nesting tests. | ADR-0013 |
| Mixed split+join gateway validation | `model.Validate` rejects a gateway with both >1 incoming AND >1 outgoing (`ErrMixedGateway`), recursively into sub-processes. | ADR-0014 |
| `service.ErrConflict` wrong-state sentinel | Closed-task / terminal-instance ops classified at the `service` seam → REST **422** (`"conflict_state"`), gRPC **`codes.FailedPrecondition`**. Engine/runtime taxonomy unchanged; not-found stays 404. | Tasks 3–4 |
| Parked-async Postgres resume e2e | `internal/persistence/postgres/resume_test.go`: park → persist → reload via a **fresh `Store`** → resume to `StatusCompleted` (timer + boundary variants). Note: intermediate-timer ids live on `token.AwaitCommand`, not `InstanceState.Timers` (which holds boundary/SLA arms). | "Highest-value missing test" |
| Inner-scope topology tests | Boundary / event-gateway / inclusive / SLA constructs nested inside a sub-process. **Surfaced + fixed a real bug:** `fireBoundaryArm` resolved the boundary's outgoing flow from the top-level def instead of the host token's scope def — fixed via `defForScope(def, s, hostTok.ScopeID)` (root-scope non-regression verified). | engine |

### Deferred follow-ups (still open after this track)

1. **Scope-targeted compensation** — the reserved `Compensate{ScopeID,FromNode}` command stays inert;
   true per-scope targeting needs an archive-by-scope + a producer (a BPMN compensation boundary/throw
   event). The hoist makes *root* rollback correct; per-scope targeting is future work.
2. **Reachability / fork-join pairing validation** — the mixed-gateway rule is the focused first cut;
   matching every converging join to a diverging fork (and condition-placement checks) is deferred.
3. **Engine-level wrong-state sentinel** — `ErrConflict` is classified only at the `service` seam;
   embedded consumers calling the engine/runtime directly still get untyped wrong-state errors.
4. **Compensation-on-error / cancel paths** — only *normal* sub-process exit hoists; error/cancel
   scope-close compensation semantics are a separate design.
5. **Pre-existing flaky singleflight test — ✅ FIXED (2026-06-22).** Was a TOCTOU in
   `CachingDefinitionRegistry.Lookup` (fast-path check / `singleflight.Do` gap), not a test barrier;
   fixed by an in-flight cache re-check. See tracked-follow-up #8 in the engine-core section.

---

## Resilience (retry/backoff/DLQ/idempotency) sub-project — ✅ COMPLETE, merged to `main`

Second track of the **deferred-backlog run**. Built on branch `feat/resilience-retry-dlq`
(HEAD `4673325`), 20 SDD tasks + opus whole-branch review (**Ready to merge: Yes-with-nits**;
no blocking issues, all 5 binding invariants confirmed). Design: spec
`docs/specs/2026-06-21-resilience-retry-dlq-design.md`, plan
`docs/plans/2026-06-21-resilience-retry-dlq.md`, ADRs 0015–0018.
Gate: `go test -race ./...` green, touched pkgs all ≥85% (engine 85.6%, model 95.8%, runtime 94.8%,
service 87.0%, transport/rest 90.2%, `internal/persistence/postgres` 86.8%, `persistence` 100%),
lint 0, engine/model purity CLEAN (`math/rand` only in `runtime/jitter.go`; `clockwork` test-only).
**Run the Postgres package with limited container parallelism** (`go test -p 1` / `-parallel N`) — high
concurrency exhausts Docker and surfaces spurious testcontainers startup failures (NOT regressions).

### Key design (ADRs 0015–0018)

- **ADR-0015 — engine-modeled retry executor.** A retry IS a timer: the runtime samples a jitter
  fraction at the edge and records it on `ActionFailed.JitterFraction`; the pure `Step` computes a
  deterministic `FireAt = OccurredAt + JitterFraction × Backoff(attempt)` (Full Jitter) and emits
  `ScheduleTimer{Kind: TimerRetry}`; the existing `Scheduler` fires it; `TimerFired{TimerRetry}`
  re-invokes the action. **Retry is opt-in** — absent an effective `RetryPolicy` (node policy or
  `StepOptions.DefaultRetryPolicy`), `ActionFailed` behaves exactly as before (`propagateError`).
- **ADR-0016 — exhaustion precedence Catch → boundary → Incident.** On a terminal failure with a
  policy: route `Node.RecoveryFlow` (injecting `_error`/`_errorMessage`/`_errorAttempts`) → else the
  existing error-boundary `propagateError` (now via a `raiseIncidentOnUnhandled` flag) → else raise an
  `Incident` (token → `TokenIncident`, instance stays `StatusRunning`), admin-resumable via the new
  `ResolveIncident` trigger.
- **ADR-0017 — outbox relay poison isolation / DLQ.** `wrkflw_outbox` gains
  `status`/`retry_count`/`next_attempt_at`/`last_error`; the relay claims
  `WHERE status='pending' AND next_attempt_at<=now`, isolates per row (a publish error quarantines
  only that row with backoff, → `dead` after `maxDelivery`; healthy peers still commit `published`),
  fixing head-of-line blocking. **Contract change:** `Run`/`DrainOnce` no longer fail-fast on a
  publish/broker error (they absorb + quarantine); infra errors still propagate. `ListDeadLettered` /
  `Redrive` admin API.
- **ADR-0018 — idempotency.** Engine stamps a stable `_idempotencyKey = instanceID:nodeID` (attempt-
  independent) on the primary service-task action; a `wrkflw_processed_message` table + `Deduper`
  (`Seen(ctx, tx, subscriber, messageID)` via `INSERT … ON CONFLICT DO NOTHING`, committed in the
  consumer's own tx) give consumers exactly-once *effect* over at-least-once delivery.

### What shipped (by layer)

| Layer | What |
|---|---|
| `model/` | `RetryPolicy` value type (`Backoff`/`Normalize`/`IsNonRetryable`/`DefaultRetryPolicy`); `Node.RetryPolicy *RetryPolicy` + `Node.RecoveryFlow`; `Validate` sentinels `ErrInvalidRetryPolicy`/`ErrInvalidRecoveryFlow` (recursive). |
| `engine/` | `ActionFailed.JitterFraction`; `ResolveIncident` trigger; `TimerRetry` kind; `Token.RetryAttempts`/`RetryStartedAt`; `Incident`/`InstanceState.Incidents`/`IncidentSeq`; `TokenIncident`; `StepOptions.DefaultRetryPolicy`; retry-schedule + `handleRetryFired` + exhaustion + `ResolveIncident` + `reinvokeServiceAction` + `serviceActionInput` (idempotency key); `cloneState` extended. |
| `runtime/` | `JitterSource` port + `math/rand/v2` impl + `WithJitterSource`; `WithDefaultRetryPolicy`; `Runner.ResolveIncident`; `InstanceSummary.IncidentCount`. |
| `internal/persistence/postgres/` | trigger codec for jitter + `ResolveIncident`; migration `0003_resilience.sql` (outbox DLQ cols + `wrkflw_processed_message`); relay per-row isolation + `RelayBackoff` + dead quarantine + `ListDeadLettered`/`Redrive`; `Deduper`; `TestPostgresParkedRetryResumesAfterReload`. |
| `persistence/` | `Relay` interface +`ListDeadLettered`/`Redrive`; `WithRelayClock`/`WithMaxDeliveryAttempts`/`WithRelayBackoff`; `Deduper`/`NewDeduper`. |
| `runtime/` shared | `runtime.DeadLetter` value type (façade return type, no import cycle). |
| `service/` + `transport/rest` | `service.ResolveIncident`; REST `POST /admin/instances/{id}/incidents/{incidentID}/resolve` (admin-gated, default-deny 403); `IncidentCount` in the admin list response. |

### Deferred follow-ups (recorded by the opus whole-branch review)

1. **gRPC `ResolveIncident` RPC + DLQ admin REST** (`GET /admin/dead-letters`, redrive) — the
   runtime/persistence APIs exist; only the gRPC proto regen + REST DLQ endpoints are unbuilt (spec §8/§11).
2. **`wrkflw_processed_message` retention/pruning job** — the dedup table grows unbounded; a TTL prune
   (well past `maxDelivery × max backoff`) is an operator task.
3. **REST resolve-incident empty-body fidelity** — a genuinely empty body (Content-Length 0) hits
   `decodeBody`'s `io.EOF`→400; tests only send `{}`. Accept EOF as "defaults" or require a `{}` body.
4. **`RetryPolicy.Backoff` overflow guard** — safe in-engine (always `Normalize()`d, positive
   `MaxInterval` caps first), but a directly-constructed non-normalized policy with `MaxInterval==0` +
   huge attempt could overflow `time.Duration(d)`; consider a defensive cap inside `Backoff`.
5. **casbin-gated per-incident authz** — `ResolveIncident` is gated only by the transport admin
   middleware in v1; a per-incident attribute rule is future work.
6. **Cosmetic test/doc nits** (non-blocking, from per-task reviews): a few single-case tests use bare
   `t.Fatal` vs testify; `countUnpublished` counts dead rows as unpublished; `RelayBackoff` dead
   post-loop guard; godoc phrasings (`Backoff` "0=first retry", `RetryStartedAt` "wall-clock").
   The one timing-sensitive relay test was deflaked pre-merge (poll instead of `time.Sleep`).

---

## Observability (metrics/traces/slog) sub-project — ✅ COMPLETE, merged to `main`

Third track of the **deferred-backlog run**. Built on branch `feat/observability`.
Design: spec `docs/specs/2026-06-21-observability-design.md`, plan
`docs/plans/2026-06-21-observability.md`, ADR-0019.
Gate: `go test -race ./runtime/...` green, lint 0, engine/model purity CLEAN (confirmed by
`TestCorePurityNoOTel` guard), OTel SDK packages confined to test files in production code.

### Design forks (key decisions)

- **OTel-API-direct** — `go.opentelemetry.io/otel`, `.../metric`, `.../trace` imported directly
  from production code; no in-repo tracing/metrics port. The OTel API *is* the vendor-neutral
  abstraction (unlike watermill/casbin/gocron). SDK packages appear in test files only.
- **Manual transport spans** — `transport/rest` and `transport/grpc` open spans manually at
  handler/interceptor entry; no `otelhttp`/`otelgrpc` contrib dependency.
- **Full metric catalog** — 9 instruments covering the complete instance lifecycle, step timing,
  action timing, retries, incidents, and human-task events.
- **Runtime is the boundary** — engine core (`engine/`, `model/`) never sees a span, meter, or
  logger; all instrumentation wraps around the pure `Step` call in `runtime.Runner`.

### What shipped (by layer)

| Layer | What |
|---|---|
| `internal/observability/` | Shared `Telemetry` struct + `New(scope, ...Option)` constructor; `WithLogger`/`WithTracerProvider`/`WithMeterProvider` options; `LogAttrs` helper injecting `trace_id`/`span_id` into slog records. Used by all instrumented components. |
| `runtime/` — `Runner` | `WithLogger`/`WithTracerProvider`/`WithMeterProvider` functional options; `runnerObs` bundles all instruments; spans on `Run` (`wrkflw.runner.Run`), `Deliver` (`wrkflw.runner.Deliver`), each `engine.Step` (`wrkflw.step`), each `InvokeAction` (`wrkflw.action <name>`); 9 metric instruments: `wrkflw_instances_started_total`, `wrkflw_instances_completed_total`, `wrkflw_instances_active`, `wrkflw_step_duration_seconds`, `wrkflw_action_duration_seconds`, `wrkflw_action_retries_total`, `wrkflw_incidents_raised_total`, `wrkflw_incidents_resolved_total`, `wrkflw_human_tasks_total`; injected logger for timer-fire retry logging. |
| `runtime/` — `TaskService` | `WithTaskServiceMeterProvider` option; increments `wrkflw_human_tasks_total{event=claimed/reassigned/completed}` on successful lifecycle transitions. |
| `transport/rest/` | `WithLogger` option; manual `wrkflw.rest METHOD` span at handler entry; W3C `traceparent`/`tracestate` propagation; injected logger replaces any package-global `slog.*` calls. |
| `transport/grpc/` | `WithLogger` option; per-RPC `wrkflw.grpc /<svc>/<method>` span at interceptor level; gRPC metadata propagation. |
| `internal/scheduling/gocron/` | `WithLogger` option; injected logger for scheduler lifecycle events. |
| `internal/persistence/postgres/relay.go` | `wrkflw.relay.batch` span wrapping each `DrainOnce` batch; structured logs for relay errors. |
| `engine/` — purity guard | `TestCorePurityNoOTel` in `engine/purity_test.go`: asserts via `go list -f {{.Deps}}` subprocess that neither `engine` nor `model` transitively imports any `go.opentelemetry.io` package. Runs as part of `go test ./engine/...`. |
| `runtime/` — testable example | `ExampleRunner_observability` in `runtime/observability_example_test.go`: documents the complete consumer wiring (SDK `TracerProvider` + `MeterProvider` + `*slog.Logger` → `With*` options → `r.Run`). |

### Deferred follow-ups

1. **Public `observability` root package** — a consumer-facing package exporting a ready-made
   trace-correlating `slog.Handler` (injects `trace_id`/`span_id`) and a convenience `Setup` helper
   configuring OTel globals + injecting into a runner. Currently `internal/observability` is unexported.
2. **Migrate eventing adapter onto the shared helper** — `internal/eventing/watermill` has its own
   With-option wiring; it should delegate to `internal/observability.New` for consistency.
3. **Async DB-backed `instances_active` gauge** — current UpDownCounter resets to zero on restart;
   a true gauge would query `wrkflw_instances` for the live count (periodic background query or
   async observable gauge callback).
4. **`instances_active` mid-run abort caveat** — hard errors aborting `deliverLoop` before terminal
   state do NOT decrement `wrkflw_instances_active`; the instance is non-terminal in the store
   (intentional semantic, documented).
5. **Store-commit / `perform` error span coverage** — `wrkflw.step` span ends before `store.Commit`;
   commit and command-execution errors surface on the parent `Run`/`Deliver` span only. Deeper span
   hierarchy is a follow-up.
6. **OTel contrib transport option** — `transport/rest` and `transport/grpc` could accept an
   `otelhttp`/`otelgrpc`-based option for consumers preferring automatic propagation; manual is v1.
7. **Persistence `Store` (Load/Commit) spans and metrics** — Postgres store operations are not
   instrumented; a `wrkflw_store_duration_seconds` histogram is a follow-up for the
   Performance/caching track.
8. **REST route-template span naming** — span name is currently `wrkflw.rest METHOD`; the route
   pattern is unavailable at middleware time (`r.Pattern` not accessible). High-cardinality path
   parameters must not appear in span names; per-handler span creation is the follow-up.
9. **Histogram exemplars** — exemplars linking data points to trace IDs are not yet configured;
   `exemplar.AlwaysOnFilter` option is a follow-up.
10. **REST/relay `WithMeterProvider` parity** — both accept the option for future use but emit no
    metrics yet; route-level request counters/latency histograms and relay throughput counters are
    a follow-up.

---

## Performance/caching sub-project — ✅ COMPLETE

Fourth track of the **deferred-backlog run**. Built on branch `feat/performance-caching`
(2026-06-22; merge-base `610982e` from `main` after the Observability track). 9 SDD tasks
+ opus whole-branch review (**Ready to merge: With fixes** — one Important issue I1 fixed
pre-merge, see below). Design: spec `docs/specs/2026-06-22-performance-caching-design.md`,
plan `docs/plans/2026-06-22-performance-caching.md`, ADRs 0020–0022.

Gate (final, controller-verified):
- `runtime`: **94.9%** ✅ — `go test -race ./runtime/...` green
- `persistence` façade: **100.0%** ✅
- `internal/persistence/postgres`: **85.3%** ✅ (`go test -race -p 1`, ~35s)
- `golangci-lint run ./...`: **0 issues** ✅
- `go test ./engine/... -run TestCorePurity` (`TestCorePurityNoOTel`): **PASS** ✅
- Vendor purity grep (`watermill|casbin|gocron|clockwork` in `engine`/`model` deps): **PURE** ✅

### Opus whole-branch review outcome (pre-merge fix)

The final review flagged one Important issue (**I1**): `Ownership.Release`'s godoc claimed it
"triggers cache eviction", but `CachingStore` never called `owner.Release` and had no
Release→evict hook — a latent stale-read hazard on the advisory-lock multi-process path (the
default `AlwaysOwn` path is immune). **Fixed** by adding a `CachingStore.Release(ctx, id)` seam
that evicts the cache entry *then* forwards to `owner.Release`, with godoc on both
`CachingStore.Release` and `Ownership.Release` and a warning on `NewAdvisoryLockOwnership` that
consumers using a cache MUST relinquish ownership through `CachingStore.Release` (not the bare
`Ownership`) so the cache stays coherent on hand-off. Test `TestCachingStoreReleaseEvicts`
proves a post-`Release` Load re-reads the backing. Minor doc corrections also landed (poll path
now `drainUntilEmpty`/drain-to-empty, not "unchanged"; `capHistory` append-order note;
`OpenPostgres` godoc `WithHistoryCap` example).

### What shipped (by layer)

| Layer | What | Task |
|---|---|---|
| `internal/persistence/postgres/` — `capHistory` | `capHistory(history []engine.NodeVisit, n int)` keeps every open visit (nil `LeftAt`) plus the n most-recent closed visits; input not mutated; n≤0 is a no-op. | 1 |
| `internal/persistence/postgres/` — `WithHistoryCap` | `WithHistoryCap(n int) StoreOption` wires `capHistory` into `Store.Create`/`Commit` before the JSONB snapshot write; default (unset) preserves full inline history; `persistence.WithHistoryCap` façade re-exports it. | 2 |
| `internal/persistence/postgres/` — NOTIFY | `WithOutboxNotify() StoreOption` emits a transactional `NOTIFY wrkflw_outbox` inside the same transaction when at least one outbox row was inserted; opt-in, default off. | 3 |
| `internal/persistence/postgres/` — LISTEN relay | `WithListenNotify() RelayOption` opens a dedicated `LISTEN wrkflw_outbox` connection; on each `NOTIFY` the relay calls `DrainOnce` immediately, well before the poll-interval tick; the poll-fallback remains active. | 4 |
| `runtime/` — `Ownership` port | `Ownership` interface (`Acquire(ctx, id) (bool, error)` / `Release(ctx, id) error`); `AlwaysOwn{}` (always owns, no-op release) for single-replica or sticky deployments. | 5 |
| `runtime/` — `CachingStore` | `CachingStore` write-through LRU+TTL store decorator (`NewCachingStore(backing, owner, clk, ...CachingStoreOption)`). Owned instances are served from cache; non-owned bypass. `ErrConcurrentUpdate` evicts the stale entry. Per-instance keyed mutex serializes concurrent Load/Commit (held across Load's `[get→backing.Load→put]` for coherence). `Release(ctx, id)` evicts-then-relinquishes ownership (the required seam for cache-coherent hand-off, added in the final-review fix). `WithCacheTTL` / `WithCacheMaxEntries` options. | 6 |
| `runtime/` — `CachingStore` tests | TTL expiry forces reload; LRU evicts at cap; concurrent Load/Commit coherent under `-race`. | 7 |
| `internal/persistence/postgres/` — advisory-lock `Ownership` | `NewAdvisoryLockOwnership(ctx, pool)` holds a dedicated connection; `Acquire` uses `pg_try_advisory_lock` (sticky); `Release` uses `pg_advisory_unlock`; tests: A acquires, B blocked, A releases, B acquires. `persistence.NewAdvisoryLockOwnership` façade. | 8 |
| `runtime/` — testable example | `ExampleNewCachingStore` in `runtime/caching_store_example_test.go`: wires `NewCachingStore(NewMemStore(), AlwaysOwn{}, clock.System())` as the runner store, parks an instance at a signal-catch node, delivers `SignalReceived("approved")` — the second Deliver is served from cache — prints `"completed"`. | 9 |

### Key design decisions (ADRs)

- **ADR-0020** — `CachingStore` + `Ownership` port: write-through, single-writer cache gated by
  `Ownership.Acquire`; the optimistic-concurrency CAS (`ErrConcurrentUpdate`) is the backstop.
  `AlwaysOwn` for in-process / sticky; Postgres advisory lock for multi-replica.
- **ADR-0021** — history cap: `capHistory` keeps all open visits (never dropped) plus the n
  most-recent closed; the journal table remains the complete audit source; cap is per-store, not
  per-definition.
- **ADR-0022** — LISTEN/NOTIFY relay trigger: opt-in transactional `NOTIFY` from `Store` + opt-in
  `LISTEN` goroutine in the relay, layered on top of the existing poll fallback so the relay
  remains correct without NOTIFY.

### Deferred follow-ups

1. **Lease-column ownership alternative** — the advisory-lock implementation ties ownership to
   a Postgres session; a `lease_owner` column + heartbeat approach survives connection churn. A
   follow-up ADR can weigh the trade-offs.
2. **Per-worker push fairness** — with multiple relay workers each `LISTEN`ing, all receive every
   `NOTIFY`; they all race to claim. A single designated listener that fans out internally avoids
   thundering-herd. Deferred.
3. **`Store` Load/Commit spans and metrics** — Observability follow-up #7: `wrkflw_store_duration_seconds`
   histogram for Postgres store operations. Still unbuilt.
4. **History-cap per-definition granularity** — the cap is set at store construction; a per-definition
   cap (e.g. `model.Node.HistoryCap`) would allow fine-grained control. Deferred.
5. **`AdvisoryLockOwnership` use-after-close guard** — after `Close`, the dedicated connection is
   released but non-nil; a subsequent `Acquire`/`Release` would use a returned-to-pool connection
   (doc-warned, shutdown-only call). A cheap `closed bool` guard returning a sentinel would harden
   it. Deferred (opus-review Minor M2).
6. **Residual hard-to-force infra branches uncovered** — `maybeNotify`'s NOTIFY-exec-error path and
   a few `DrainOnce`/`listenLoop` infrastructure-failure branches are not deterministically
   forceable; package totals clear ≥85% without them. Fault-injection (a failing/closeable conn
   wrapper) could cover them if desired.
7. **Relay LISTEN test establish-sleep** — `relay_listen_test.go` uses a fixed 200 ms wait for the
   listener to establish before writing the event; on a very slow CI host this could race. Prefer
   polling for the `LISTEN` to be established (opus-review Minor; test-only, non-blocking).

---

## DB casbin policy adapter — ✅ COMPLETE

Fifth track of the **deferred-backlog run**. Built on branch `feat/casbin-db-adapter`
(merge-base `610982e`, the `feat/performance-caching` merge onto `main`, 2026-06-22).
Design: spec `docs/specs/2026-06-22-casbin-db-adapter-design.md`,
plan `docs/plans/2026-06-22-casbin-db-adapter.md`, ADR-0023.

Gate (final, Task 6 verified — 2026-06-22):
- `go test -race` (all non-Docker packages): **PASS** ✅ — 18 packages, 0 failures
- `go test -race -p 1 ./internal/authz/casbin/... ./casbinauthz/... ./internal/persistence/postgres/...`: **PASS** ✅
- `casbinauthz` per-package coverage: **90.9%** ✅ (≥85%)
- `internal/authz/casbin` per-package coverage: **85.6%** ✅ (≥85%)
- `golangci-lint run ./...`: **0 issues** ✅
- Confinement guard (`TestCasbinConfinement`): **PASS** ✅ — casbin absent from engine/model/runtime/persistence transitive deps (proven to bite: 25 violations when casbin injected into runtime, clean after revert)
- No ORM in go.mod: **CLEAN** ✅ (`gorm`/`go-pg`/`sqlx`/`ent` absent)
- casbin version: **v2.135.0** ✅ (pinned, not bumped)
- Opus whole-branch review: **Ready to merge: Yes** — no Critical/Important; all binding invariants (callback race-fix ordering, watcher leak/connection-release, no `RemoveFilteredPolicy` over-deletion, separate version table, façade type confinement, additive-only) verified.

**Coverage note (resolved):** `internal/authz/casbin` initially measured 73.1% because `db.go`
(`NewDBEnforcer`, the two closers) is exercised only from `casbinauthz/casbinauthz_db_test.go`
(coverage attributed to the caller). Closed by adding `internal/authz/casbin/db_test.go`
(black-box) that calls `NewDBEnforcer` directly (watcher-enabled / disabled / invalid-model
paths) plus three watcher error-branch tests (Update error, listen acquire/LISTEN failures via
fault injection) → **85.6%**. `NewDBEnforcer` itself sits at 66.7%; its three remaining error
branches (`NewSyncedEnforcer`/`SetWatcher`/`SetUpdateCallback` failing) are structurally
unreachable in black-box because the production watcher never returns an error.

### What shipped (by layer)

| Layer | What | Notes |
|---|---|---|
| `internal/authz/casbin/migrate.go` + `casbinauthz.MigrateCasbin` | `MigrateCasbin(ctx, pool)` runs goose migrations tracked in a **separate `casbin_goose_db_version` table** (independent of `wrkflw_goose_db_version`). Creates `casbin_rule(id, ptype, v0–v5, created_at)` with a unique constraint on `(ptype,v0–v5)`. Idempotent; safe to call multiple times. | ADR-0023; separate version table prevents version-number conflicts with the main `persistence.Migrate` |
| `internal/authz/casbin/pg_adapter.go` + exports | `pgAdapter` implements `casbin/persist.Adapter` over pgx/v5. `LoadPolicy` reads all rows and feeds the casbin model via `model.AddPolicy`. `SavePolicy` truncates then bulk-inserts (single-pass with padded 6-column rules). `AddPolicy`/`RemovePolicy`/`RemoveFilteredPolicy` are incremental mutations persisted immediately. Padding/trimming (`padRule`/`ruleFromCols`) ensures row format is correct regardless of policy arity. `NewPGAdapter` (exported via `export_test.go`) for black-box tests. | ADR-0023 §3 |
| `internal/authz/casbin/pg_watcher.go` | `pgWatcher` implements `casbin/persist.Watcher` via `pgconn.LISTEN`/`NOTIFY`. `Update(s)` sends a `NOTIFY` whose payload is `{nodeID}:{s}` (the colon-separated node identifier allows self-filtering). The background `listen` loop ignores notifications where the payload's node prefix matches this node's ID, so a node does not reload on its own writes. `Close` cancels the listen loop and closes the dedicated connection. `backoff` (unexported) gives a jitter-retry helper for reconnects. `SetUpdateCallback` wires the handler. | ADR-0023 §4 |
| `internal/authz/casbin/db.go` | `NewDBEnforcer(ctx, pool, DBConfig)` assembles a `*casbin.SyncedEnforcer` over `pgAdapter`. When `WatcherEnabled`, creates and wires a `pgWatcher`. **Critical race fix:** casbin's `SetWatcher` internally calls `w.SetUpdateCallback(func(string){ _ = e.LoadPolicy() })` where `e` is the BASE `*Enforcer` (not mutex-synchronized). We override the callback *after* `SetWatcher` to call `enforcer.LoadPolicy()` on the `*SyncedEnforcer` so the lock is held during reload. `DBConfig` carries `ModelText`, `WatcherEnabled`, `WatcherChannel`, `NodeID`. | ADR-0023 §5 |
| `casbinauthz/` façade additions | `MigrateCasbin(ctx, pool)` re-exported; `DBOption` functional options: `WithModel`, `WithoutWatcher`, `WithWatcherChannel`, `WithNodeID`; `defaultNodeID()` generates a per-process random node ID. `NewCasbinAuthorizerFromDB(ctx, pool, ...DBOption) (authz.Authorizer, io.Closer, error)` — builds enforcer via `NewDBEnforcer`, wraps via the existing `NewCasbinAuthorizer` single-wrapping path, returns the stable `authz.Authorizer` interface + `io.Closer`. | ADR-0023 §6 |

### Key design decision (ADR)

- **ADR-0023** — pgx-native DB casbin policy adapter + LISTEN/NOTIFY watcher, following the
  same façade/internal layer pattern as ADR-0008/ADR-0009/ADR-0010. `casbinauthz` and
  `internal/authz/casbin` are the only packages importing casbin (enforced by the
  `TestCasbinConfinement` guard). The `*SyncedEnforcer`-callback override is the critical
  fix preventing a data-race on policy reload in multi-node deployments. A separate
  `casbin_goose_db_version` table keeps the casbin migration version independent of the
  main persistence schema version.

### Deferred follow-ups

1. **`FilteredAdapter` / incremental `WatcherEx` updates for large policy sets** — `LoadPolicy`
   re-reads the entire `casbin_rule` table on every watcher-triggered reload. For large policy
   sets this is expensive. Implementing `casbin/persist.FilteredAdapter` (partial load) and the
   `casbin/persist.WatcherEx` interface (per-rule delta updates instead of full reload) would cut
   reload cost significantly. Deferred until policy-set sizes warrant it.
2. **Policy-admin REST/gRPC surface** — `NewCasbinAuthorizerFromDB` provides policy persistence
   but no API to add/remove/list rules at runtime (other than direct DB manipulation). A
   `casbinauthz.PolicyAdmin` interface with REST/gRPC endpoints (e.g. `POST /admin/policy`,
   `DELETE /admin/policy/{id}`, `GET /admin/policy`) is a follow-up; the persisted `pgAdapter`
   is the backend.
3. **Watcher reconnect-delay not tunable** — the `backoff` helper uses a fixed
   `watcherReconnectDelay` constant (1 s), no jitter. There is no `DBOption` to override it.
   A `WithWatcherReconnectBackoff(...)` option (and optional jitter) would make this configurable
   for consumers with stricter SLA requirements.
4. **Separate `casbin_goose_db_version` table note** — the casbin migration intentionally uses
   a separate `casbin_goose_db_version` version table (via `goose.WithTableName`) to avoid
   version-number conflicts with the persistence migration set's `goose_db_version`. Consumers
   calling both `persistence.Migrate` and `casbinauthz.MigrateCasbin` will see two goose version
   tables in their schema; this is expected and documented.
5. **`context.Background()` in adapter/watcher methods** — casbin's `persist.Adapter`/`Watcher`
   method signatures take no `context`, so the pgx calls inside them cannot propagate a caller
   deadline/cancellation. A context-aware adapter wrapper (storing a base context) is a follow-up.

---

## True async call activity — ✅ COMPLETE

Final "also outstanding" item (engine follow-up #3). Built on branch `feat/async-call-activity`
(2026-06-22; merge-base `4b8137e`). 9 SDD tasks + opus whole-branch review. Design: spec
`docs/specs/2026-06-22-async-call-activity-design.md`, plan `docs/plans/2026-06-22-async-call-activity.md`,
ADRs 0024 (durable async call activity) + 0025 (atomic call-link Store side-effects).

**The headline:** a call-activity child that PARKS (its own human task, timer, signal, or nested
call activity) now works. Previously `perform(StartSubInstance)` ran the child synchronously and
errored on a parking child ("the synchronous runner does not support parked children"). Now the
parent parks; the child runs independently across later `Deliver`s; when the child reaches terminal
status, the parent is resumed by `SubInstanceCompleted`/`SubInstanceFailed` delivered **durably and
crash-safely**, idempotently.

**Engine/model UNTOUCHED** (the load-bearing property): `git diff 4b8137e..HEAD -- engine model`
shows **zero** production-line changes. The `SubInstanceCompleted`/`SubInstanceFailed` triggers, the
`StartSubInstance` command, the parent token park (`AwaitCommand`), and the resume logic
(`engine/step.go:514–539`) already existed and are used as-is. This is a runtime + persistence change.

### What shipped (by layer)

| Layer | What |
|---|---|
| `runtime/` | `CallLink`/`CallOutcome`/`PendingNotify` value types; `CallLinkStore` port (`ClaimPending`/`MarkNotified`/`LookupChild`) + `MemCallLinkStore`; additive `AppliedStep.NewCallLink`/`CallOutcome` (nil for all existing callers; `MemStore` honors them via `NewMemStoreWithCallLinks`). Non-blocking `perform(StartSubInstance)` + `WithCallLinks` option (opt-in; absent it the synchronous behavior is preserved verbatim); `maxCallActivityDepth`→`maxCallDepth`; the `deliverLoop` child-terminal hook (sets `CallOutcome` on the terminal commit). `CallNotifier` (`DrainOnce`/`Run`) — claims terminal links, resolves the parent def, delivers `SubInstanceCompleted`/`SubInstanceFailed`, **idempotent** (`engine.ErrTokenNotFound` ⇒ treated as success); `CallDeliverFunc`. |
| `internal/persistence/postgres/` | `0004_call_links.sql` (`wrkflw_call_links` + partial pending index); `Store.Create`/`Commit` honor `NewCallLink`/`CallOutcome` IN-TX (the crash-safety seam — link created with the child's Create, flipped with its terminal Commit, atomically); Postgres `CallLinkStore`; crash-safety e2e (a FRESH notifier over a NEW pool resumes a parked parent purely from durable DB state). |
| `persistence/` (façade) | `NewCallLinkStore(pool) runtime.CallLinkStore`; `NewCallNotifier(pool, deliver, reg, clk, ...opts)` reusing `runtime.CallNotifier` over the Postgres store (one wrapping path, no logic duplication). |
| `engine/`, `model/` | **Nothing.** Zero production diff (proven). |

### Key design decisions (ADRs)
- **ADR-0024** — durable async call activity via a `wrkflw_call_links` correlation table + a relay-shaped
  notifier; correlation lives in persistence (NOT on the pure `InstanceState`); opt-in; idempotent
  parent resume; crash-safe.
- **ADR-0025** — atomic call-link side-effects on the transactional `Store` (additive `AppliedStep`
  fields), so the link's existence is tied to the child's existence and the link's terminal flip is
  tied to the child's terminal commit, each in one transaction.

### Gate (final, controller-verified)
`go test -race ./...` green (Postgres `-p 1`); coverage **runtime 91.0% / persistence 91.7% /
internal/persistence/postgres 86.2%** (all ≥85%); `golangci-lint run ./...` **0 issues**;
**engine/model production code unchanged** (zero-line diff over the branch).

### Deferred follow-ups
1. **`FOR UPDATE SKIP LOCKED` claim for strict multi-replica exclusivity** — `ClaimPending` is a plain
   SELECT; idempotency (`ErrTokenNotFound`) makes concurrent multi-replica notifiers SAFE but allows a
   duplicate delivery (wasted work). A tx-holding `DrainOnce` with `FOR UPDATE SKIP LOCKED` would make
   the claim exclusive.
2. **CallNotifier relay-shaping** — telemetry span (`wrkflw.callnotifier.batch`), per-row backoff, and
   an optional `LISTEN`/`NOTIFY` wakeup on `wrkflw_call_links` (the relay has these; the notifier reuse
   inherits per-row isolation + retry-via-poll but not these). Latency/observability, not correctness.
3. **Richer `SubInstanceFailed`→parent error text** — `SubInstanceFailed` does not create an `Incident`,
   so `terminalErr` falls back to a generic message (e.g. the depth-limit cause is lost in a deep
   runaway cascade). Populating an `Incident` would surface the cause.
4. **Cancellation propagation** parent→child (parent cancel → child terminate) and orphaned-child
   cleanup (when the parent is already terminal, the child result is dropped — the parent `Deliver`
   no-ops on `ErrTokenNotFound`).
5. **Cross-machine child execution** — this design makes the parent *notification* durable, not the
   child's execution distributed; the child is driven by whichever runtime delivers its triggers.
6. **Per-definition `maxCallDepth`** (global guard in v1); `MarkNotified` clock injection (uses
   `time.Now()` today).

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

## What's next: productionization sub-projects (each its own brainstorm → spec → plan → SDD cycle)

The engine core depends on interfaces only. The next sub-projects implement them (per CLAUDE.md):

- **Persistence** — ✅ COMPLETE, merged to `main`. See section above.
- **Eventing** — watermill `Publisher` implementing the `persistence.Publisher` interface (outbox relay is ready; this sub-project wires the broker side, behind the eventing abstraction; never import watermill from engine/workflow code).
- **Scheduling** — gocron `Scheduler` (replace `MemScheduler`; shares the `clock.Clock`/clockwork so the
  same fake clock drives engine + scheduler in tests, per ADR-0003).
- **Authorization** — casbin behind the `authz.Authorizer`.
- **Transports** — REST `http.Handler` factories + gRPC `ServiceRegistrar` registrations the consumer
  mounts (library-provided, never a shipped binary; ADR-0004 / CLAUDE.md).
- **Admin monitoring** + **`ProcessInstance` response customization**.

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

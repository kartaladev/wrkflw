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

## Persistence (PostgreSQL) sub-project — IMPLEMENTED, pending review+merge

Branch: `feat/persistence-postgres` (HEAD `18f7ebc`). All 10 tasks are complete and verified.
Gate: `go test -race ./...` green, total coverage 87.3%, touched packages all ≥85%, lint clean (0 issues),
no forbidden imports (`watermill`/`casbin`/`gocron`/`ThreeDotsLabs`/`clockwork`) in production code.

### What shipped

| Layer | What | Notes |
|---|---|---|
| `runtime/` port collapse | Replaced 3 separate in-memory ports (`StateStore`/`Journal`/`Outbox`) with a single transactional `Store` + `JournalReader` | ADR-0007; `MemStore` is the in-memory reference impl |
| `internal/persistence/postgres/` | Postgres `Store`: transactional snapshot-JSONB writes with optimistic-CAS; `DefinitionStore`; outbox with SKIP LOCKED relay; goose migrations (4 tables: `process_instances`, `applied_steps`, `outbox_events`, `process_definitions`); trigger codec (`MarshalTrigger`/`UnmarshalTrigger`) | ADR-0006 (snapshot-JSONB schema), ADR-0008 |
| `persistence/` root façade | `OpenPostgres`, `Migrate`, `NewRelay`, `NewDefinitionStore`; sentinel errors `ErrInstanceNotFound`, `ErrConcurrentUpdate`; `CachingDefinitionRegistry` (singleflight + in-memory LRU for hot-path definition reads) | ADR-0008 |
| `database/` | `RunTestDatabase` testcontainers helper — shared by all Postgres integration tests; returns a `*pgxpool.Pool` backed by `postgres:17-alpine` | test-helper-only package (0% own coverage is expected) |

### Key design decisions (ADRs)
- **ADR-0006** — snapshot-JSONB storage shape (one row per instance, JSONB state blob + projected columns for indexed queries).
- **ADR-0007** — `Store` port collapse: three separate runtime interfaces replaced by a single transactional `Store` so `Commit` writes instance state + outbox in one atomic transaction.
- **ADR-0008** — `persistence` / `internal/persistence/postgres` façade split: library consumers only import the root `persistence` package; all pgx/goose wiring stays unexported in `internal/`.

### Relay design note
The `Relay` in `internal/persistence/postgres` is broker-agnostic: it polls the outbox with SKIP LOCKED and calls a `persistence.Publisher` interface. A watermill adapter (to be written in the **Eventing** sub-project) will implement that interface — watermill is never imported here.

### Deferred follow-ups (deliberate, not bugs)
1. **Owned-instance cache** — instance state is fetched from Postgres on every `Run`/`Deliver`; a per-runner in-memory cache keyed by instance id would cut DB round-trips on hot flows.
2. **History cap** — `applied_steps` grows unbounded; a configurable retention / archive policy is needed before large-scale use.
3. **LISTEN/NOTIFY relay trigger** — current relay polls on a fixed interval; a Postgres `LISTEN`/`NOTIFY` push would reduce latency and DB load.
4. **Per-aggregate relay ordering** — SKIP LOCKED gives throughput but no ordering guarantee within a single instance; a per-instance sequencing layer (e.g. advisory lock per instance) is needed if strict in-order delivery matters.
5. **TOAST / fillfactor tuning** — large JSONB blobs will TOAST; setting `fillfactor=70` on `process_instances` to leave room for HOT updates is a DBA tuning step.

---

## What's next: productionization sub-projects (each its own brainstorm → spec → plan → SDD cycle)

The engine core depends on interfaces only. The next sub-projects implement them (per CLAUDE.md):

- **Persistence** — ✅ Implemented on `feat/persistence-postgres` (pending final whole-branch review + merge). See section above.
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

# Engine Core Design (sub-project #1)

- Date: 2026-06-20
- Status: Approved for planning
- Related: ADR-0002 (stepper + commands), ADR-0003 (clockwork), ADR-0004 (root layout)
- Discussion artifacts: `2026-06-20-engine-core-execution-model-options.html`,
  `2026-06-20-engine-core-execution-model-explained.{html,pdf}`

## 1. Purpose & scope

`wrkflw` is a library-first BPMN-flavored workflow engine. This spec defines
**sub-project #1: the pure engine core** — the token state machine that advances
a process instance across nodes — plus the seam interfaces other subsystems
attach to and an in-memory reference runtime for end-to-end testing.

Per ADR-0002 the core is a **pure stepper**: a deterministic, side-effect-free
function `Step(definition, state, trigger) -> (state, []Command)`. It performs no
I/O, reads no clock, and spawns no goroutines.

### In scope (#1)

- The process-definition model (`model/`) for **broad BPMN coverage** + validation.
- The engine core (`engine/`): `Step` (macro + micro), `InstanceState`, the
  `Trigger`/`Command` taxonomies, token & scope model, gateway/event/sub-process
  routing semantics, deterministic ordering.
- The expression wrapper over `expr-lang/expr`.
- Seam **interfaces only**: `action.ServiceAction`/`Catalog`, `authz.Authorizer`
  + `AuthzSpec` + `Actor`, `clock.Clock`, the human-task ports
  (`humantask.ActorResolver`, `humantask.TaskStore`), and persistence ports
  (`StateStore`, `Journal`, `OutboxWriter`).
- Human-task model: pluggable **actor lookup** (candidate resolution at task
  entry), a **task bucket** (queryable inbox — "my tasks" / "claimable by me"),
  and **actor audit** (who did each lifecycle action) — interfaces + in-memory
  fakes here; concrete directory/Postgres impls + query transports come later.
- An in-memory **reference runtime** (`runtime/`) + in-memory fakes, so flows run
  end-to-end in tests.

### Out of scope (later sub-projects)

Postgres persistence + hot-path cache; watermill/outbox eventing; gocron
scheduling integration; casbin `Authorizer` implementation; REST/gRPC
transports; admin/superuser monitoring; `ProcessInstance` response
customization; BPMN2-XML / YAML loaders (the model is the in-memory target form;
loaders come later). The core only defines the interfaces these attach to.

## 2. Package layout (module root, per ADR-0004)

Module path: `github.com/kartaladev/wrkflw` (consumer import root).

| Package | Responsibility | May import |
|---|---|---|
| `model/` | Process-definition value types + `Validate()`. Pure data. | stdlib only |
| `engine/` | Core: `Step`, `InstanceState`, `Trigger`, `Command`, tokens, scopes, routing. | `model`, `authz`, `humantask`, `expr`, `time` |
| `clock/` | `Clock` interface (`Now() time.Time`) + `System()` real impl. The sole time port. | stdlib only |
| `action/` | `ServiceAction` interface + `Catalog` (name → action). Used by runtime. | stdlib |
| `authz/` | `Authorizer` interface + `AuthzSpec` + `Actor` data. Casbin impl is later/internal. | `expr` (attribute predicates) |
| `humantask/` | `Actor`-lookup + task-bucket model: `HumanTask`, `TaskState`, `ActorResolver`, `TaskStore` (queryable inbox). | `authz` |
| `runtime/` | Reference loop + in-memory `StateStore`/`Journal`/`OutboxWriter`/`ActorResolver`/`TaskStore` fakes; default embeddable loop. | `engine`, `action`, `authz`, `humantask`, `clock` |
| `internal/` | Non-exported impl for later subsystems (Postgres, watermill, casbin). | — |

The core (`engine/`, `model/`) imports **no** transport, storage, bus, or
time-vendor code. `clockwork` is imported only by tests and the later scheduling
wiring; everything else depends on the in-repo `clock.Clock` interface, which
`clockwork.Clock` satisfies structurally.

## 3. Definition model (`model/`)

A `Node` is a struct with a `Kind` discriminator + kind-specific fields
(struct-with-kind, not an interface zoo) so it serializes cleanly to YAML/Go and
is straightforward to validate.

- **Events:** Start, End, TerminateEnd, ErrorEnd, IntermediateCatch
  (timer/signal/message), IntermediateThrow, **Boundary** (timer/error/signal;
  interrupting + non-interrupting).
- **Activities:** ServiceTask, UserTask (human), ReceiveTask, SendTask,
  BusinessRuleTask, **SubProcess** (embedded), **CallActivity** (calls another
  definition), **EventSubProcess**.
- **Gateways:** Exclusive (XOR), Parallel (AND), **Inclusive (OR)**, EventBased.
- **SequenceFlow:** `source`, `target`, optional `condition` (expr string),
  `isDefault` marker.

`Validate(def) error` rejects malformed graphs before execution: missing/multiple
start, dangling or dead-end flows, gateway misuse (e.g. conditions on parallel),
boundary events not attached to an activity, unresolved call-activity refs.

## 4. Instance state model (snapshot + journal)

The runtime persists `InstanceState` as the **source of truth** and appends each
applied `Trigger` to a **journal** (enables replay, audit, time-travel debug, and
rollback). The core remains a pure `(state, trigger) -> (state, commands)`
function regardless of how it is stored.

```go
type InstanceState struct {
    InstanceID string
    DefID      string
    DefVersion int
    Status     Status            // Running, Completed, Failed, Compensating, Terminated
    Variables  map[string]any    // process variables
    Tokens     []Token           // live execution markers
    Scopes     []Scope           // sub-process / event-subprocess / compensation scope tree
    Tasks      []humantask.HumanTask // authoritative human-task records (claim/complete state)

    StartedAt  time.Time         // from the StartInstance trigger's OccurredAt
    EndedAt    *time.Time        // nil until terminal; set from the terminal trigger
    History    []NodeVisit       // rich, inline per-node visit history (see Timing below)
}

type Token struct {
    ID          string
    NodeID      string           // where it sits
    ScopeID     string           // containing scope (sub-processes & boundary events)
    State       TokenState       // Active, WaitingCommand, AtJoin
    AwaitCommand string           // CommandID it is parked on, if any
    Payload     map[string]any   // token-local data merged into Variables on arrival
    EnteredAt   time.Time        // when this token arrived at NodeID (from trigger OccurredAt)
}

type Scope struct {
    ID         string
    NodeID     string            // the SubProcess/CallActivity/EventSubProcess node
    ParentID   string            // scope tree
    Compensations []CompensationRecord // completed activities eligible for compensation
}

// NodeVisit is one traversal of one node by one token. Appended on entry,
// LeftAt set on exit. Loops/re-entries produce multiple visits; a node's visit
// count is len(visits for that NodeID).
type NodeVisit struct {
    NodeID    string
    TokenID   string
    EnteredAt time.Time
    LeftAt    *time.Time   // nil while a token still occupies the node
    ActorID   *string      // who completed this visit, for human-task nodes (audit); nil otherwise
}
```

### Actor audit

For human tasks we record **who** performed each lifecycle action, not just
when. Every human-task `Trigger` (`HumanClaimed`, `HumanReassigned`,
`HumanCompleted`) carries an `authz.Actor`, so the **journal is the unified audit
trail**: (actor, `OccurredAt`, action) for the full task lifecycle (all of these
flow through `Step` — see §5). The **snapshot** mirrors the key facts: the
`HumanTask` record holds `ClaimedBy`/`State`/timestamps, and the completing
`NodeVisit.ActorID` shows who finished a given user-task visit. An `authz.Actor`
carries `ID`, `Roles`, and `Attributes` (the latter feed attribute-based authz).

### Timing

The core reads no clock (ADR-0003). All timestamps enter as **data**: every
`Trigger` carries an `OccurredAt time.Time` that the runtime produces by calling
`clock.Clock.Now()` (our in-repo time port; `clockwork.Clock` satisfies it), and
the core writes timestamps into state *from `trigger.OccurredAt`* — never from a
clock read of its own. This keeps `Step` deterministic: folding the journal
reproduces identical timestamps.

Timing responsibilities split across three places:

| Concern | Owner | Location |
|---|---|---|
| Domain lifecycle (instance start/end, token entered-node, task due) | core writes from trigger/command data | `InstanceState` |
| Rich per-node visit history (entered/left, counts, loops) | core appends `NodeVisit` | `InstanceState.History` (inline) |
| Full immutable audit ledger | runtime stamps each trigger | the journal |
| Performance timing (step latency, spans) | observability | traces/metrics (later sub-project) |

Human-task "created at" = the token's `EnteredAt` at the user-task node; "due at"
is `AwaitHuman.SLA`; timer fire time is `ScheduleTimer.FireAt`. No separate
task/timer entity is needed in the core.

**Consequence (accepted):** inline `History` grows with process length and loop
iterations, so long-running/looping instances produce larger snapshots. The
later persistence/cache sub-project must account for this (and may offer an
optional history-retention cap); the journal remains the unbounded source of
truth for audit regardless.

Determinism: tokens are processed in a defined order (creation order) so a given
`(state, trigger)` always yields the same `(state, commands)`.

## 5. Trigger & Command taxonomies (closed/sealed sets)

`Trigger` is a sealed interface (`isTrigger()` + `OccurredAt() time.Time`);
`Command` is a sealed interface (`isCommand()`). "Sealed" = unexported marker
method so the set cannot be extended outside the package; adding a variant is a
deliberate, reviewable change. Every `Trigger` variant carries an `OccurredAt`
timestamp set by the runtime from the shared clock (see §4 Timing).

**Triggers** (drive the next step — initiating causes *and* returning results):

| Trigger | Carries | Meaning |
|---|---|---|
| `StartInstance` | `Vars` | begin a new instance |
| `ActionCompleted` | `CommandID, Output` | a `ServiceAction` finished OK |
| `ActionFailed` | `CommandID, Err, Retryable` | a `ServiceAction` failed |
| `TimerFired` | `CommandID` | a scheduled timer elapsed |
| `HumanClaimed` | `TaskToken, Actor` | an eligible actor claimed a task (audited; no token move) |
| `HumanReassigned` | `TaskToken, From, To, By` | a task was reassigned/delegated (audited; no token move) |
| `HumanCompleted` | `TaskToken, Output, Actor` | a person completed a user task |
| `SignalReceived` | `Name, Payload` | a broadcast signal arrived |
| `MessageReceived` | `Name, CorrelationKey, Payload` | a correlated message arrived |
| `SubInstanceCompleted` | `CommandID, Output` | a call-activity child finished |
| `SubInstanceFailed` | `CommandID, Err` | a call-activity child failed |
| `CompensateRequested` | `ToNode` | admin/debug rollback request |
| `CancelRequested` | — | cancel the instance |

**Commands** (what the runtime must do):

| Command | Carries | Runtime action |
|---|---|---|
| `InvokeAction` | `CommandID, Name, Input, RetryPolicy` | resolve via `Catalog`, run, return `ActionCompleted/Failed` |
| `ScheduleTimer` | `CommandID, FireAt|Duration, Kind` | schedule (gocron); on fire → `TimerFired`. Kind ∈ SLA, Intermediate, InWait, Boundary |
| `CancelTimer` | `CommandID` | cancel a pending timer |
| `EmitEvent` | `Topic, Payload` | write to outbox in the same tx as state |
| `AwaitHuman` | `TaskToken, AuthzSpec, SLA, Reminders` | resolve candidates via `ActorResolver`; create the bucket task (Unclaimed); later inject `HumanClaimed`/`HumanCompleted` |
| `UpdateTask` | `HumanTask` | sync the task-bucket (`TaskStore`) projection after a claim/reassign/complete trigger |
| `StartSubInstance` | `CommandID, DefRef, Input` | start child instance; on finish → `SubInstanceCompleted/Failed` |
| `Compensate` | `ScopeID|FromNode` | drive compensation (core emits the actual `InvokeAction`s) |
| `CompleteInstance` | `Result` | mark instance complete |
| `FailInstance` | `Err` | mark instance failed |

## 6. `Step` semantics

```go
func Step(def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error)

type StepOptions struct { Mode StepMode } // Macro (default) | Micro
type StepResult  struct { State InstanceState; Commands []Command }
```

Algorithm:

1. **Apply** the trigger: locate the parked token by `CommandID`/`TaskToken`,
   merge `Output`/`Payload` into `Variables`, unpark it (or, for `StartInstance`,
   place a token on the start event).
2. **Drive forward** a worklist of active tokens, in deterministic order. Each
   automatic node resolves *inside* the step:
   - **Gateways** route via the expr wrapper. Parallel fork → N tokens; joins
     synchronize by counting arrived vs. expected incoming tokens (inclusive
     OR-join uses reachable-incoming analysis).
   - **ServiceTask / UserTask / Timer / SubProcess / CallActivity** emit their
     command (`InvokeAction` / `AwaitHuman` / `ScheduleTimer` / `StartSubInstance`)
     and **park** the token (stop advancing it).
   - **Boundary events** register their waiter (timer/error/signal) for the
     attached activity's token.
   - **End events** consume the token; when no tokens remain → `CompleteInstance`
     (TerminateEnd ends the whole instance immediately).
3. **Macro** mode drains the worklist until every token is parked or done.
   **Micro** mode advances exactly one node, then returns (debug/step-through).
4. Return accumulated `Commands` + new `State`.

`Step` never blocks and never performs the commands — it only returns them.

## 7. Expression evaluation

A thin internal wrapper over `expr-lang/expr`:

- `Compile(expr string) (Program, error)` with an internal cache keyed by the
  expression string (compiled programs are reused).
- `Eval(program, env) (any, error)` where `env` is built from `Variables`
  (+ token `Payload`).
- Powers: sequence-flow conditions, inclusive-gateway branch selection, timer
  duration/date expressions, and `authz` attribute predicates.

The wrapper is the only place that imports `expr`, keeping the dependency
swappable per CLAUDE.md.

## 8. Seams (interfaces; #1 ships only in-memory fakes)

```go
// clock/ — the sole time port (clockwork.Clock satisfies it structurally)
type Clock interface { Now() time.Time }
func System() Clock // standard-library-backed real clock; no clockwork import

// action/
type ServiceAction interface {
    Do(ctx context.Context, in map[string]any) (out map[string]any, err error)
}
type Catalog interface { Resolve(name string) (ServiceAction, bool) }

// authz/
type Actor struct { ID string; Roles []string; Attributes map[string]any }
type Authorizer interface {
    Authorize(ctx context.Context, spec AuthzSpec, actor Actor, vars map[string]any) error
}
// AuthzSpec carries roles, resource-privileges, and attribute predicates (expr);
// it travels as DATA on AwaitHuman and is evaluated by the Authorizer at the
// boundary — the core never calls the Authorizer itself.

// humantask/ — pluggable actor lookup + the task bucket (queryable inbox)
type TaskState int // Unclaimed, Claimed, Completed, Cancelled
type HumanTask struct {
    TaskToken, InstanceID, NodeID string
    Eligibility authz.AuthzSpec   // who may claim/complete
    Candidates  []string          // actor IDs from the resolver (bucket projection; runtime-filled)
    State       TaskState
    ClaimedBy   string
    CreatedAt   time.Time
    DueAt       *time.Time
}

// ActorResolver is the pluggable actor-lookup mechanism, invoked by the runtime
// when a token enters a user task (on the AwaitHuman command). It expands the
// eligibility spec (+ process variables, for attribute-based rules) into the
// candidate actor set. Default fake here; LDAP/DB/casbin-group impls later.
type ActorResolver interface {
    Candidates(ctx context.Context, spec authz.AuthzSpec, vars map[string]any) ([]authz.Actor, error)
}

// TaskStore is the task bucket: a queryable projection of human tasks, updated by
// the runtime from UpdateTask commands. Supports the two access patterns:
type TaskStore interface {
    Upsert(ctx context.Context, t HumanTask) error            // applied from UpdateTask
    Get(ctx context.Context, taskToken string) (HumanTask, error)
    AssignedTo(ctx context.Context, actorID string) ([]HumanTask, error)   // "my tasks"
    ClaimableBy(ctx context.Context, actor authz.Actor) ([]HumanTask, error) // unclaimed + eligible (e.g. by role)
}

// runtime/ persistence ports
type StateStore  interface { Load(id string) (InstanceState, error); Save(InstanceState) error }
type Journal     interface { Append(id string, trg Trigger) error }
type OutboxWriter interface { Write(topic string, payload map[string]any) error }
```

The core emits `InvokeAction`/`AwaitHuman`/`EmitEvent` as **data**; the runtime
resolves and performs them. Time enters only at the runtime boundary via the
in-repo `clock.Clock` port (ADR-0003), implemented by `clockwork` at the edge;
the core uses `time.Time`/`time.Duration` values but never reads "now".

**Human-task flow.** On `AwaitHuman`, the runtime calls
`ActorResolver.Candidates(spec, vars)` and creates the bucket task (`Unclaimed`).
A user claims/reassigns/completes through a runtime API that **authorizes the
actor** (via `Authorizer` against the eligibility spec + candidates) and then
injects the corresponding `Trigger` (`HumanClaimed` / `HumanReassigned` /
`HumanCompleted`) — so every lifecycle action passes through `Step`, lands in the
journal with its `authz.Actor` + `OccurredAt` (the unified audit trail), updates
the authoritative `HumanTask` in `InstanceState`, and yields an `UpdateTask`
command that the runtime applies to the `TaskStore` projection. Authorization is
a runtime concern (the `Authorizer` does I/O); the core records the
already-authorized action and, on completion, advances the token.

## 9. Reference runtime (`runtime/`)

A minimal, single-process loop demonstrating Option A end-to-end and serving as
the default embeddable driver:

```
load state → Step → save state (+ journal append, + outbox writes) → perform
commands → enqueue resulting triggers → repeat until parked/done
```

Ships with in-memory `StateStore`/`Journal`/`OutboxWriter`, an in-memory
`Catalog`, an allow-all `Authorizer`, a static-role `ActorResolver` (a
role→actor-IDs map) and an in-memory `TaskStore` (so "my tasks" / "claimable by
me" queries and claim→complete are demonstrable end-to-end), and a `clock.Clock`
(`clock.System()` in prod, a `clockwork` fake in tests). This is reference
wiring, not the product; later sub-projects replace the fakes with
Postgres/watermill/casbin/gocron and a real directory-backed `ActorResolver`
behind the same interfaces.

## 10. Resolved minor decisions

- **Idempotency:** `CommandID` is the at-least-once idempotency key; the runtime
  dedupes redelivered results. Action-level idempotency tokens deferred.
- **Sub-processes:** modeled via the `StartSubInstance` command + a scope tree in
  state, not by inlining a child engine — keeps boundary events and compensation
  scoping clean.
- **Step granularity:** configurable; Macro is the default, Micro is opt-in for
  debugging.
- **State model:** snapshot is the source of truth; the journal is an append-only
  companion for replay/audit/rollback.
- **Timing:** timestamps enter only as `Trigger.OccurredAt` (runtime-stamped from
  the shared clock); the core never reads a clock. The snapshot keeps rich inline
  `History` (per-node visits) plus lifecycle times; the journal is the unbounded
  audit ledger; perf timing lives in observability.
- **Human tasks:** actor lookup (`ActorResolver`) and the task bucket
  (`TaskStore`) are pluggable ports with in-memory fakes in #1. All lifecycle
  actions (claim/reassign/complete) flow through `Step` as triggers carrying
  `authz.Actor`, giving one journal-based audit trail; the `TaskStore` is a
  runtime-maintained projection synced via `UpdateTask`. Authorization stays a
  runtime concern (the `Authorizer` does I/O); the core records already-authorized
  actions.

## 11. Testing strategy

- Black-box `engine_test` / `model_test` / `humantask_test` / `runtime_test` packages.
- Table-driven tests per the project `table-test` skill (assert-closure form;
  `ctx` modifier; `t.Context()`).
- Fake `clockwork` clock for timer/SLA/in-wait tests.
- `mockgen` (`use-mockgen` skill) only for the seam interfaces the reference
  runtime needs.
- Testable `Example` functions for the public API (`engine`, `runtime`).
- Coverage ≥ 85% line on every touched package; `go test ./...` clean; `golangci-lint run ./...` clean.

## 12. TDD increments (build order, each red→green)

1. `model` types + `Validate` (malformed-graph cases first).
2. `engine` skeleton: `Trigger`/`Command` sealed sets; `StartInstance` → token on start; linear Start→ServiceTask→End (macro).
3. Exclusive gateway (expr conditions, default flow).
4. Parallel gateway fork + synchronizing join.
5. Inclusive gateway fork + OR-join synchronization.
6. UserTask → `AwaitHuman`; `ActorResolver` candidate resolution + `TaskStore` bucket (AssignedTo / ClaimableBy); `HumanClaimed`/`HumanReassigned`/`HumanCompleted` triggers with `authz.Actor` audit + `UpdateTask` sync; resume on complete; SLA + in-wait reminders via `ScheduleTimer`.
7. Timer intermediate catch; boundary timer (interrupting + non-interrupting).
8. Signal/message catch + `EventBased` gateway.
9. Embedded SubProcess + EventSubProcess (scopes).
10. CallActivity (`StartSubInstance` / `SubInstanceCompleted`).
11. Error end / boundary error; `FailInstance`.
12. Compensation (`Compensate` over a scope's `CompensationRecord`s) + admin `CompensateRequested`.
13. Micro-step mode.
14. `clock.Clock` interface + `clock.System()`; then the reference `runtime` loop (stamping `Trigger.OccurredAt` via `Clock.Now()`) + in-memory fakes + end-to-end examples.

## 13. Verification checklist

- [ ] `model.Validate` rejects every malformed-graph class listed in §3.
- [ ] `Step` is deterministic: identical `(state, trigger)` ⇒ identical `(state, commands)`.
- [ ] Every BPMN element in §3 has macro-step coverage; gateways cover fork + join + default.
- [ ] Long waits park with zero retained goroutines/memory in the core; resume via the matching `CommandID`/`TaskToken`.
- [ ] SLA breach and in-wait reminders fire via fake-clock advancement.
- [ ] `ActorResolver` is invoked on task entry; `TaskStore.AssignedTo`/`ClaimableBy` return the right buckets (claimable = unclaimed + eligible, e.g. by role).
- [ ] `HumanClaimed`/`HumanReassigned`/`HumanCompleted` each record `authz.Actor`+`OccurredAt` in the journal; the completing `NodeVisit.ActorID` and `HumanTask.ClaimedBy`/`State` reflect the actor; claim of an ineligible actor is rejected at the runtime boundary.
- [ ] Compensation walks scope `CompensationRecord`s in reverse completion order.
- [ ] Micro-step advances exactly one node per call.
- [ ] Timestamps derive only from `Trigger.OccurredAt`; the runtime produces it via `clock.Clock.Now()`. An import-boundary/grep test confirms `engine`/`model` never call `time.Now()` and never import `clockwork` (the `clock` package's `System()` is the single allowed real-time adapter).
- [ ] `History` records a `NodeVisit` per entry with `LeftAt` set on exit; loop re-entries produce distinct visits; `StartedAt`/`EndedAt` set from the start/terminal triggers.
- [ ] Core packages (`engine`, `model`) import no transport/storage/bus/time-vendor packages (verified by an import-boundary test).
- [ ] Coverage ≥ 85% per touched package; `go test ./...` and `golangci-lint run ./...` clean.
```

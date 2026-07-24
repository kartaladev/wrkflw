# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

> **Pre-1.0 notice.** Until a `v1.0.0` tag is cut, the public API may change between
> minor versions. See [`STABILITY.md`](STABILITY.md) for the stability and deprecation policy.

## [Unreleased]

The first tagged release (`v0.1.0`) will be cut from this section: it is the inaugural
feature set, built across ADRs 0001–0095. Because nothing has shipped yet, everything below
is **Added** — the list describes the engine as it stands today, not a delta from a prior
release.

### Breaking changes (pre-v0.1.0 — no stability promise)

- **`scheduling` package renamed to `scheduler` and unified with the internal gocron engine.**
  The public import path is now `github.com/kartaladev/wrkflw/scheduler` (formerly
  `github.com/kartaladev/wrkflw/scheduling`). The internal gocron implementation relocated from
  `internal/scheduling/gocron` to `scheduler/internal/gocron`. Public signatures returning
  `scheduling.*` types now return `scheduler.*` equivalents (e.g. `persistence.NewSchedulerLocker`
  now returns `scheduler.Locker` instead of `scheduling.Locker`; `scheduler.Elector`,
  `scheduler.Scheduler`, etc. replace their `scheduling.*` counterparts).

- **Scheduler-owned durable jobs; `scheduler` is now a self-contained, spinnable-standalone
  library (ADR-0134).** The `runtime/kernel.Scheduler` / `kernel.JobStore` / `kernel.ScheduledJob`
  port that `runtime` previously depended on is **deleted from `kernel`**; `runtime.WithScheduler`
  now takes a `scheduler.Scheduler` directly, and `runtime.NewJobStore` returns a `scheduler.JobStore`.
  `kernel.ArmedTimer`, `kernel.TimerStore`, and `kernel.JobSpec` (+ `JobKind`) are unaffected and
  remain in `kernel`. The sentinels `ErrUnsupportedTrigger` and `ErrUnresolvedTimerDefinitions`
  move from `kernel` to `scheduler` (message prefix `workflow-scheduler:`, unchanged text otherwise).
  `scheduler.JobStore` gains a real `Save`/`Delete` write path (previously `LoadScheduled`-only,
  now `Load`/`Save`/`Delete`). **The old `AppliedStep.TimerArms`/`TimerCancels` fused-write
  mechanism is deleted** (`applyTimerOps` is gone from `Store.Create`/`Commit`); atomicity is now
  achieved by the runtime's own `jobStore.Save`/`deleteTimer` (routed through the new
  `kernel.TimerWriter` capability) running **inside the same state-commit transaction** as the
  step write (`kernel.TxRunner.RunInTx` / `JoinOrBegin`) — the scheduler itself is never called
  during commit (direct-save). New `scheduler.Job`/`scheduler.NewJob`/`scheduler.NewJobWithID`,
  `scheduler.ActivationType` (`ActivationAuto`/`ActivationManual`, `scheduler.WithManualActivation`),
  and `scheduler.Scheduler.Activate` close the fire-before-commit race this way: a Manual job's
  durable row is written inside the caller's own transaction, and only armed in-memory
  (`Activate`, an idempotent upsert-by-id) strictly **after** that transaction commits — a failed
  post-commit `Activate` is logged and benign, since the durable arm rehydrates on next boot.
  `scheduler.WithJobStore(kind, provide)` registers a per-`JobKind` store; on `NativeScheduler.Start`
  the scheduler self-rehydrates every registered kind (`Load` + `Activate` each). Job ids are
  unchanged engine timer ids — no composite id scheme. New observability:
  `wrkflw_scheduler_job_runs_total` counter and `wrkflw_scheduler_job_duration_seconds` histogram,
  emitted via gocron's native `MonitorStatus` hook (`scheduler.WithMeterProvider`).
  `go-co-op/gocron/v2` bumped to the pinned `v2.22.0` (ADR-0135). See `scheduler/example_test.go`
  for `NewScheduler`/`NewJob`/`Trigger`/`WithJobStore` usage.

- **Calendar (`Daily`/`Weekly`/`Monthly`) and cron triggers now resolve their at-times in
  UTC by default on the live scheduler (ADR-0136).** Previously the live scheduler used the
  host's `time.Local` (the internal gocron engine never pinned a location), while the pure
  `scheduler.Trigger.Next` reference computed calendar at-times in UTC — so on a non-UTC host
  the two disagreed. The live scheduler is now pinned to UTC by default, matching
  `Trigger.Next` (whose cron branch is likewise normalized to UTC). Deployments running
  `TZ=UTC` (typical containers) are unaffected. A non-UTC host that intends host-local
  resolution must now pass the new `scheduler.WithLocation(time.Local)` option (which also
  accepts any named `*time.Location`). Under a non-UTC location the trigger fires in that zone
  while `Trigger.Next` stays UTC — a reporting-only difference that never affects firing or
  rehydration; named zones additionally resolve at-times per their DST rules on the live
  scheduler. In a multi-replica deployment every replica must use the same location.

- **`DefinitionRegistry.Lookup(ctx, defRef string)` → `Lookup(ctx, model.Qualifier)`;
  def-ref fields, params, and constructors now typed `definition.Qualifier` (ADR-0101).**
  The following Go symbols are now `definition.Qualifier` (or `model.Qualifier` internally)
  rather than `string`: `service.StartInstanceRequest.DefRef`,
  `service.DeliverMessageRequest.DefRef`, `engine.StartSubInstance.DefRef`,
  `activity.CallActivity.DefRef`, `kernel.OutboxEvent.DefinitionRef`,
  `kernel.ChainLink.{Predecessor,Successor}DefinitionRef`,
  `kernel.ChainLinkRef.DefinitionRef`, `chain.ChainEvent.PredecessorDefinitionRef`.
  Constructors `activity.NewCallActivity(id, ref model.Qualifier, …)` and
  `build.(*Builder).AddCallActivity(id, ref model.Qualifier, …)` take the typed value.
  `NewMapDefinitionRegistry` is now variadic (`...*model.ProcessDefinition`).
  The HTTP `def_ref` JSON key and the `definition_ref` TEXT database columns are
  **unchanged** — wire and schema remain byte-identical.
  Use `definition.Latest(id)`, `definition.Version(id, v)`, or `definition.ParseQualifier(s)`
  to construct a `Qualifier`.

- **`instance_id` removed from the start-instance request body; `StartInstanceRequest.InstanceID` removed.**
  Process-instance IDs are now server-generated (ADR-0100). Remove the `instance_id` field from
  any `POST /instances` request body and any direct use of `service.StartInstanceRequest.InstanceID`;
  the server mints the ID using `runtime/idgen.XID()` by default and returns it in the response.
  To use a different strategy, pass `service.WithIDGenerator(idgen.UUIDv7())` (or
  `idgen.Func(...)` in tests). The `instance_id` key is unchanged in all **response** bodies.
  Requests that address an existing instance (`DeliverSignal`, `CancelInstance`, `CompleteTask`,
  etc.) are unaffected.

- **`persistence.NewCachingInstanceStore` now requires a `cache.Provider` argument**
  (previously `runtime/kernel.NewCachingInstanceStore` took no provider; that name was itself
  renamed from `kernel.NewCachingStore` in ADR-0096 — full lineage: `kernel.NewCachingStore` →
  `kernel.NewCachingInstanceStore` → `persistence.NewCachingInstanceStore`). The type also
  moved from `runtime/kernel` to `persistence`. Supply `hotcache.New()` (the default) or any
  other `cache.Provider` from `persistence/cache/{hotcache,ottercache,rediscache,memcache}`.
  Consumers using `NewDurableProvider` / `NewMySQLDurableProvider` / `NewSQLiteDurableProvider`
  are unaffected — caching is wired automatically by the provider constructors.

- **`runtime.NewProcessDriver` is now all-optional.** The two required positional
  arguments (`cat action.Catalog`, `store kernel.InstanceStore`) have been replaced with
  functional options. A zero-argument call — `d, _ := runtime.NewProcessDriver()` — gives
  a fully usable in-memory, non-durable driver backed by `action.DefaultCatalog()`,
  `kernel.NewMemInstanceStore()`, and `runtime.DefaultDefinitionRegistry()`. A DEBUG log
  at construction reports the wired collaborators and advises how to go durable.
  - Supply your own catalog via `runtime.WithActionCatalog(cat)`.
  - Supply a durable store via `runtime.WithInstanceStore(store)`.
  - Supply an explicit definition registry via `runtime.WithDefinitions(reg)` (passing
    `nil` is a no-op — the default stands).
  - Populate the default catalog with `action.Register(name, fn)`,
    `action.RegisterFunc(name, fn)`, `action.MustRegister`, or `action.MustRegisterFunc`.
  - Populate the default definition registry with `runtime.RegisterDefinition(def)` or
    `runtime.MustRegisterDefinition(def)`.

- **`InstanceStore` / `MemInstanceStore` / `CachingInstanceStore` renames (breaking).**
  All references to the old names must be updated:
  - `kernel.Store` → `kernel.InstanceStore`
  - `kernel.MemStore` → `kernel.MemInstanceStore`; `kernel.NewMemStore(` → `kernel.NewMemInstanceStore(`; `kernel.MemStoreOption` → `kernel.MemInstanceStoreOption`
  - `kernel.CachingStore` → `kernel.CachingInstanceStore`; `kernel.NewCachingStore(` → `kernel.NewCachingInstanceStore(`; `kernel.CachingStoreOption` → `kernel.CachingInstanceStoreOption`
  - `persistence.Store` (the façade interface) → `persistence.InstanceStore`

- **`kernel.Token` → `kernel.Version`** — the optimistic-concurrency version scalar is
  now named `Version` throughout the kernel package.

- **`kernel.Outcome` → `kernel.ChainOutcome`** — the chain-outcome type is renamed to
  avoid colliding with the generic word "outcome".

- **`kernel.Ownership` → `kernel.InstanceOwnership`** — the ownership port interface is
  renamed for clarity.

- **`kernel.Publisher` → `kernel.OutboxPublisher`** (and `persistence.Publisher` alias
  → `persistence.OutboxPublisher`) — the outbox-publish port is renamed to be explicit
  about its role.

- **`action.Retryabler` → `action.RetryableError`** — the retry-classification interface
  is renamed to follow Go error-interface naming conventions.

- **`action.Default()` → `action.DefaultCatalog()`** — the zero-argument catalog accessor
  is renamed to be unambiguous.

- **Activity/event option-naming consolidation, deadline/wait split, and inline-action
  removal (ADR-0114).** Public option renames (hard renames, no deprecated aliases):
  - `activity.WithCompensation(a)` → `WithCompensateAction(a)` (field `CompensationAction`
    → `CompensateAction`).
  - `activity.WithCancelHandler(a)` → `WithCancelAction(a)` (field `CancelHandler` →
    `CancelAction`).
  - `activity.WithActionName(a)` → `WithTaskAction(a)`.
  - `activity.WithDeadline(t, flow, action)` / `event.WithCatchDeadline(t, flow, action)` →
    split into a mandatory `WithWaitDeadline(t schedule.TriggerSpec, flow string)` plus the
    new optional `WithDeadlineAction(action string)` (see Added below). `WithWaitDeadline`
    now rejects a recurring trigger at `Build`, returning the new sentinel error
    `ErrDeadlineTriggerRecurring`.
  - `activity.WithWaitReminder(t, action)` / `event.WithCatchWaitReminder(t, action)` →
    `WithWaitAction(t schedule.TriggerSpec, action string)` (accepts one-shot or recurring
    triggers). Backing fields rename `WaitFields.ReminderEvery` → `WaitEvery` and
    `ReminderAction` → `WaitAction`.
  - `event.WithStartMessage`/`WithCatchMessage`/`WithBoundaryMessage` → one
    `event.WithMessageCorrelator(msg, key string)` usable on Start/Catch/Boundary events.
  - `event.WithStartSignal`/`WithCatchSignal`/`WithBoundarySignal` → one
    `event.WithSignalName(name string)` usable on Start/Catch/Boundary events.
  - `event.WithThrowSignal(name)` → `event.WithThrowSignalName(name)`.
  - `processtest` harness: `WithAction`/`WithActionFunc` → `WithCatalogAction`/
    `WithCatalogActionFunc`.

  **Removed, no replacement:** `activity.WithAction`/`activity.WithActionFunc` (inline
  node-local action closures), `model.TaskAction.Inline`, `engine.InvokeAction.Inline`, and
  the inline-vs-name conflict check at `Build`. Every action now resolves by catalog name
  only — register it (`action.Register`/`action.RegisterFunc`) and reference it via
  `WithTaskAction` (or another `WithXxxAction` option). Definitions are consequently fully
  serializable: no node can carry a non-serializable closure.

  **Wire/YAML key renames** (persisted definitions serialized with the old keys will not
  decode — see the migration note below): `compensationAction` → `compensateAction`,
  `cancelHandler` → `cancelAction`, `reminderTrigger`/`reminderAction`/`reminderEvery` →
  `waitTrigger`/`waitAction`/`waitEvery`. The `service` instance JSON `inline` action-binding
  field is removed. **Unchanged:** `deadlineTrigger`/`deadlineFlow`/`deadlineAction`,
  `signalName`, `messageName`, `correlationKey`.

  **Migration note.** Persisted process definitions serialized with the old wire/YAML keys
  (`compensationAction`, `cancelHandler`, `reminderTrigger`/`reminderAction`/`reminderEvery`)
  will fail to decode after upgrading — re-author or re-serialize them with the renamed keys.
  Any definition relying on an inline node-local action closure must register that action in
  a catalog and reference it by name via `WithTaskAction` (or the matching `WithXxxAction`
  option) instead.

- **`DeliverMessage` drops its `def` parameter; `DeliverMessageRequest.DefRef` removed
  (ADR-0121).** `runtime.ProcessDriver.DeliverMessage(ctx, name, key, payload)` and
  `service.Engine.DeliverMessage` no longer take a target definition — message delivery is
  now def-less, matching `BroadcastSignal`. `service.DeliverMessageRequest.DefRef` is removed
  (`StartInstanceRequest.DefRef` is unaffected). Consumers correlating a message to a running
  instance must have that instance's definition registered with the driver's definition
  registry (resolved via `Lookup` at correlation time); an unregistered definition now fails
  correlation with `kernel.ErrDefinitionNotFound` instead of relying on the caller-supplied
  `def`. `BroadcastSignal` and `DeliverMessage` also change miss-branch behaviour: a signal or
  message with no waiter now additionally checks for a matching signal-/message-start event
  and creates an instance when one exists (previously always a no-op); definitions with no
  event-starts see no behaviour change.

- **`EventSubProcess` node kind removed; an event sub-process is now an `activity.SubProcess`
  with an event-triggered inner start (ADR-0122).** Deleted: `event.EventSubProcess`,
  `model.KindEventSubProcess`, `event.NewEventSubProcess`, `event.WithEventSubProcessNonInterrupting`,
  `event.EventSubProcessOption`, `build.(*Builder).AddEventSubProcess`, and the `"eventSubProcess"`
  wire discriminator (old JSON/YAML carrying it no longer unmarshals). Author an event sub-process
  as `activity.NewSubProcess(id, sub)` where `sub` has an event-triggered inner start; the new
  `event.WithNonInterrupting()` start option (`event.StartEvent.NonInterrupting`) carries the
  interrupting marker (default interrupting). New validation sentinel
  `model.ErrEventSubprocessOnFlow` rejects an event-triggered SubProcess that has an incoming
  sequence flow. Known limitation: `DeliverMessage` does not route to a message-triggered
  event-sub arm (pre-existing) — use `ApplyTrigger`.

### Added

- **Graceful shutdown for `runtime.ProcessDriver` (ADR-0133).** `ProcessDriver.Shutdown`
  now performs real admission control and in-flight drain: it rejects new externally-initiated
  work with `runtime.ErrDriverShuttingDown` (every exported entry point — `Drive`,
  `ApplyTrigger`, `DeliverMessage`, `BroadcastSignal`, `CancelInstance`, `ResolveIncident`,
  `ReverseInstance`, and timer-start fires) and waits for in-flight instance execution to
  complete before returning, bounded by the `ctx` deadline (or the new `WithShutdownTimeout`
  fallback when `ctx` carries none). On drain-deadline expiry it returns
  `runtime.ErrDrainTimeout` WITHOUT force-cancelling in-flight work. Added
  `runtime.WithShutdownTimeout(d)` and `ProcessDriver.IsShuttingDown()`. `service.Engine`
  inherits rejection automatically; its human-task ops (`ClaimTask`/`CompleteTask`/`ReassignTask`)
  reject before any task-store write. The owned scheduler is now closed via a deadline-raced
  closer so `Shutdown(ctx)` honours the `ctx` deadline when closing it (previously the close
  used gocron's internal stop timeout and ignored `ctx` — audit Finding 3).

- **Event-based start events: message, signal, and timer starts (ADR-0121).**
  A process definition may now declare multiple start events — up to one trigger-less
  **manual start** (BPMN's "none start", `ErrMultipleManualStarts` if more than one) plus any
  number of event-triggered starts, each with exactly one trigger family
  (`ErrAmbiguousStartTrigger`/`ErrEventStartMissingTrigger` otherwise). Reachability validation
  now walks from the union of all start nodes. `engine.StartInstance` gains `StartNodeID`
  (empty resolves the manual start, `ErrNoManualStart` if there is none); the driver resolves
  which start node fired and the engine only places the token.
  - **Signal start** — broadcast fan-out: `BroadcastSignal(ctx, name, payload)` now also
    creates one instance per registered definition with a matching signal-start, in addition
    to resuming parked waiters. Signal names need not be unique across definitions.
  - **Message start** — correlate-to-running-first, then create: `DeliverMessage` (see
    Breaking changes) resolves a running waiter by `(name, key)` first; on a miss it creates a
    new instance at the unique matching message-start (`ErrAmbiguousMessageStart` if more than
    one matches). New-instance dedup is via a deterministic `(messageName, correlationKey)`
    instance id plus `Store.Create`'s `ErrInstanceExists` — fully multi-replica and restart
    safe, no advisory lock, no new schema (the `runtime/chain.Chainer` pattern). Message-start
    name uniqueness is enforced at `RegisterDefinition`/`MustRegisterDefinition`
    (`ErrDuplicateMessageStart`).
  - **Timer start** — scheduler-driven, multi-replica safe via the existing
    `scheduler.Elector`. New explicit `runtime.ProcessDriver.RehydrateStartTimers(ctx) error`
    step (a sibling of `RehydrateTimers`) arms recurring/one-shot start timers by enumerating
    registered definitions; each fire creates one instance.
  - New opt-in `runtime/kernel.DefinitionLister` capability (`ListDefinitions(ctx)
    []*model.ProcessDefinition`) lets the event-start subsystem enumerate registered
    definitions for signal/message matching; `MemDefinitionRegistry` and
    `MapDefinitionRegistry` implement it, `CachingDefinitionRegistry` passes through. A
    registry that does not implement it disables event-based *start* (correlate-to-running
    still works).
  - A definition with only event-starts (no manual start) makes plain `Drive` error rather
    than silently doing nothing.
  - See `examples/scenarios/event_start` for a signal fan-out + message correlation walkthrough.

- **`definition.Qualifier`: typed process-definition reference (ADR-0101).**
  New value type `definition.Qualifier{ID string; Version int}` (`Version == 0` = latest)
  with helpers `definition.Latest(id)`, `definition.Version(id, v)`,
  `definition.ParseQualifier(s) (Qualifier, error)`, `q.IsLatest()`, and `q.String()`
  (`"id"` or `"id:version"`). JSON and YAML marshalers keep the wire byte-identical to the
  former string encoding. `ErrInvalidQualifier` (`"workflow-model: invalid qualifier"`)
  is returned for empty id, non-numeric/negative/zero explicit version (`:0` is rejected —
  zero is the reserved latest sentinel). `model.ParseQualifier` is re-exported from the
  `definition` root package as `definition.ParseQualifier`.

- **Server-generated process-instance IDs via pluggable `runtime/idgen` (ADR-0100).**
  New nested package `runtime/idgen` with three constructors: `XID()` (default — `github.com/rs/xid`,
  ~20-char lowercase base32hex, k-sortable, never errors), `UUIDv7()` (`github.com/google/uuid`
  NewV7, RFC 9562, propagates rare entropy errors), and `Func(fn)` (deterministic test adapter).
  `Generator` interface: `NewID() (string, error)`. New option `WithIDGenerator(gen)` on both
  `runtime.ProcessDriver` and `service.Engine` (nil-guarded, default `idgen.XID()`); mirrors the
  existing `WithClock` seam. `service.Engine.StartInstance` always mints the ID; the service option
  also threads the generator into the default driver so both layers agree on the strategy.

- **Token-based, BPMN-inspired engine core** — process definitions across 18 typed node
  kinds (start/end/error events, service/user/business-rule/send/receive tasks,
  exclusive/parallel/inclusive/event-based gateways, sub-process — embeddable as an event
  sub-process via an event-triggered inner start — call activity, boundary, and intermediate
  catch/throw/compensation events), token execution, and `expr-lang`-driven gateway routing.
  The vocabulary is BPMN-inspired, not BPMN-compatible; there is no BPMN2 XML loader.

- **Authoring** — the `definition` root package is a thin aggregator with two entry points:
  `definition.NewBuilder(id, version)` (fluent Go builder with one `Add<Kind>` per node kind)
  and `definition.NewLoader(r io.Reader)` (YAML). Types, validation and serialization live in
  `definition/model`; sequence flows in `definition/flow`; node constructors in
  `definition/{event,gateway,activity}`; the fluent builder in `definition/build`; the
  deserialization registration bundle in `definition/kinds`.

- **Service actions** — a name-resolved catalog (`action.Catalog`, `action.Action`,
  `action.ActionFunc`, `action.MapCatalog`, `action.Registry`) with definition-scoped and
  node-inline registration (three-tier resolution: inline → scoped → global). Built-in
  actions: `httpcall` (10 MiB body cap by default via `WithMaxResponseSize`), `email`,
  `transform`, and `logaction`. Service-action invocations time out after 30s by default
  (`runtime.WithActionTimeout`); a timeout surfaces as a retryable failure.

- **`activity.WithCompletionAction(name string)` — optional post-completion action hook on
  UserTask/ReceiveTask (ADR-0114).** Sets `ActivityFields.CompletionAction` (wire/YAML key
  `completionAction`, decode-only per existing YAML convention). When set,
  `handleHumanCompleted`/`handleMessageReceived` (`engine/step_triggers.go`) merge the
  completion's output vars, then invoke the named catalog action via the existing
  `InvokeAction`/`ActionCompleted` round-trip — parking the token as `TokenWaitingCommand` —
  before advancing; the action's return vars are merged and the token advances only once the
  action completes. Failure is governed by the host node's `RetryPolicy` and error boundary,
  identically to a service-task action failure: no new token state or failure model was
  introduced. Distinct from the existing `WithCompletionValidation` (which gates the
  completion input *before* it is accepted) — this option runs an action *after* the
  completion is accepted.
- **`activity.WithDeadlineAction(name string)` — optional standalone deadline-breach action
  (ADR-0114).** Split out of the old bundled `WithDeadline(t, flow, action)` (see the Breaking
  changes section above); pair it with the new mandatory `WithWaitDeadline(t, flow)` to
  attach a breach action only when one is actually wanted.

- **Default `DefinitionRegistry` for zero-config call activities (ADR-0097, follows ADR-0096).**
  `runtime.NewProcessDriver()` now wires `runtime.DefaultDefinitionRegistry()` automatically,
  giving `KindCallActivity` nodes a working registry without any `WithDefinitions` call —
  symmetric with how `action.DefaultCatalog()` works for service tasks. New API:
  - `runtime.DefaultDefinitionRegistry() *kernel.MemDefinitionRegistry` — returns the
    process-global mutable registry.
  - `runtime.RegisterDefinition(def *model.ProcessDefinition) error` — registers `def`
    into the global registry under both `"<ID>"` and `"<ID>:<Version>"`. Bare `"<ID>"`
    resolves to the most recently registered version. Returns `ErrDefinitionExists` if the
    exact `"<ID>:<Version>"` is already registered.
  - `runtime.MustRegisterDefinition(def *model.ProcessDefinition)` — panics on error
    (init-time wiring, mirrors `action.MustRegister`).
  - `kernel.MemDefinitionRegistry` — the new concurrency-safe, mutable sibling of the
    immutable `MapDefinitionRegistry`. Obtain with `kernel.NewMemDefinitionRegistry()`.
    New sentinel errors: `kernel.ErrNilDefinition`, `kernel.ErrEmptyDefinitionID`,
    `kernel.ErrDefinitionExists`.
  - **`runtime.WithDefinitions(nil)` is now a no-op** (nil-ignored, matching
    `WithActionCatalog` / `WithInstanceStore`). A nil argument no longer clobbers the
    default registry. Passing a non-nil registry overrides the default, as before. Tests
    needing a fully isolated, empty registry should pass
    `WithDefinitions(kernel.NewMemDefinitionRegistry())`.

- **Persistence** — SQL backends for **PostgreSQL 17**, **MySQL 8.0+**, and **SQLite**
  (`modernc.org/sqlite`, pure-Go, WAL, single-writer; single-node/test/embedded only) behind
  ONE neutral `internal/persistence/store` parametrized by `internal/persistence/dialect`
  (ADR-0081/0082). Capability interfaces `Notifier` (LISTEN/NOTIFY) and `Locker` (distributed
  advisory lock) are opt-in per dialect. Facade constructors `persistence.Open{Postgres,MySQL,SQLite}`
  and `persistence.Migrate{Postgres,MySQL,SQLite}` (plus a public `persistence.Migrator`).
  Optimistic-concurrency (CAS) writes, a transactional **outbox** relay with poison isolation +
  DLQ + redrive, hot-path caching (see below), and data-retention pruners.

- **Persistence caching layer (ADR-0099)** — a neutral `persistence/cache` port (`Cache`,
  optional `ValueCache` capability, `Provider`, generic `Codec[V]`) with **four swappable
  adapter subpackages**: `persistence/cache/hotcache` (`github.com/samber/hot`, **default**,
  in-memory), `persistence/cache/ottercache` (`github.com/maypok86/otter/v2`, in-memory
  alternative), `persistence/cache/rediscache` (`github.com/redis/go-redis/v9`, distributed),
  and `persistence/cache/memcache` (`github.com/bradfitz/gomemcache`, distributed). Each
  adapter lives in its own subpackage so its library dependency is optional. `CachingInstanceStore`
  is relocated from `runtime/kernel` into `persistence` and re-substrated onto the `Cache`
  port (all correctness-bearing behavior preserved: ownership gate, per-instance keyed locks,
  evict-on-`ErrConcurrentUpdate`, `AlwaysOwn` single-replica `Warn`, `Release`-evict-first).
  A new `CachingTaskStore` provides read-through / write-through point-read caching over
  `humantask.TaskStore` (set-wide queries `AssignedTo`/`ClaimableBy` are uncached in v1).
  Caching is **default-on** on all three `DurableProvider` constructors (`NewDurableProvider`,
  `NewMySQLDurableProvider`, `NewSQLiteDurableProvider`) using `hotcache` in-memory, `AlwaysOwn`
  + one-time Warn, instance TTL 5m, human-task TTL 30s. New `DurableOption`s:
  `WithCacheProvider`, `WithInstanceCacheProvider`, `WithHumanTaskCacheProvider`,
  `WithDurableInstanceCacheOwnership`, `WithDurableInstanceCacheTTL`,
  `WithDurableHumanTaskCacheTTL`, and `WithoutCache` (escape hatch). Definition caching is
  deferred; human-task query caching (`AssignedTo`/`ClaimableBy`) is deferred.

- **Runtime driver** — `runtime.ProcessDriver` wires the engine to persistence, scheduling,
  and actions; supporting pieces live in `runtime/{kernel,view,chain,task,signal,calllink,monitor}`.
  Stateful constructors fail fast, returning `(T, error)` and wrapping `kernel.ErrNilDependency`
  on a nil required dependency rather than panicking later.

- **Scheduling / waits** — `gocron`-driven timers, deadlines, and in-wait reminder actions;
  multi-replica timer exclusivity via advisory-lock leader election.

- **Resilience** — engine-modeled retry with backoff/jitter, incident creation on exhaustion,
  catch-flow handling, and a retryable-error contract (`action.IsRetryable` / `action.NonRetryable`).

- **Compensation** — optional per-node compensation actions, scope-targeted rollback, and
  best-effort cancel actions on instance cancellation.

- **Authorization** — pluggable `authz.Authorizer` with a casbin baseline (role,
  resource-privilege, and attribute/variable-based evaluation), a DB-backed policy adapter,
  and a runtime policy admin.

- **Eventing** — vendor-neutral eventing abstraction over watermill (in-process GoChannel
  publisher by default; broker wiring documented in `docs/eventing-brokers.md`), transactional
  `SendTask` messaging via the outbox (`message.*` topics), and event-driven process-instance
  chaining (`chain.Chainer`).

- **HTTP transports** — mountable route groups over a shared pure root:
  `transport/http/httpcore` (pure per-endpoint functions, DTOs with `go-playground/validator/v10`
  validation, `ClassifyError` with 5xx body redaction, `NewInstanceView`, health-probe
  evaluation, static-route-template observability, and the generic `RouteCustomizer[R]` /
  `CustomizeOption[R]` / `CustomizeConfig[R]` seam), plus three native adapters —
  `transport/http/stdlib` (`*http.ServeMux`), `transport/http/gin`, and `transport/http/fiber`
  (fiber v3). Each adapter exposes `InstanceRoutes`, `TaskRoutes`, `MessageRoutes`,
  `AdminRoutes`, and `HealthRoutes` structs plus `Mount`/`MountHealth` conveniences. Admin
  routes are **default-absent by composition** — they exist only when a consumer mounts
  `AdminRoutes` (with the desired admin-port fields set) on a router group their own auth
  middleware already protects. Import isolation: stdlib pulls no third-party transport
  dependency; gin/fiber consumers pull only their respective framework.

- **Service façade** — `service.Service` is the single transport-neutral application seam;
  the HTTP adapters are thin translators over it.

- **Observability** — OpenTelemetry metrics + traces and `slog` logging across runtime,
  transports, scheduling, eventing, and the persistence relay; SLI gauges/counters
  (`wrkflw_outbox_*`, `wrkflw_timers_armed`, `wrkflw_timer_fired_total`,
  `wrkflw_action_failures_total{action,retryable}`), `/healthz` + `/readyz` handlers, and
  reference `docs/dashboards/`, `docs/runbooks/`, and `docs/observability.md`.

- **Operability** — graceful `runtime.ShutdownGroup`, opt-in `persistence.WarnUnsafeConfig`,
  a `processtest` consumer test harness, example reference wiring under `examples/`, and
  `STABILITY.md` / `docs/production-checklist.md`.

- **Project** — Apache-2.0 license, contributor and security policies, and a GitHub Actions
  CI pipeline (build, race tests, lint, `gosec`/`bodyclose`/`errorlint`, vulnerability scan,
  CodeQL).

[Unreleased]: https://github.com/kartaladev/wrkflw/commits/main

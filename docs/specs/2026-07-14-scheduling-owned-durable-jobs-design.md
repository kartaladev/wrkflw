# Scheduling-owned durable jobs (Plan-3): self-contained `scheduler` library

- **Status:** Draft **v2** (post-audit consolidated design; user-approved 2026-07-22)
- **Date:** 2026-07-14, revised 2026-07-22
- **Related:** supersedes the write-ownership half of ADR-0027 (transactional
  arm/cancel); builds on ADR-0102 (scheduler lifecycle), ADR-0059 (locker),
  ADR-0003 (clock). New ADRs to be filed: **ADR-0134** (this design) and a
  gocron pin-bump ADR (D13).
- **Anticipated by:** `runtime/timerops.go:48` ("the Plan-3 JobStore, which will
  own the arm/persist lifecycle under one ambient tx").
- **Revision note:** v1 was audited by four parallel, source-verified reviews
  (architecture / dependency boundaries / API / completeness). The audit
  confirmed one blocker in the D11 single-call model (fire-before-commit) and a
  descriptor-rebuild blocker (B1), plus a set of mechanical fixes. v2 resolves
  all of them; the key change is the **Activation model (D3a)**, which
  supersedes D11.

## Context

`scheduler` is intended to be spinnable as a standalone library (it already
houses `Locker`/`Elector` and the neutral façade over an internal gocron
engine). Several things block that today and one architectural wrinkle shapes
the fix:

1. **`scheduling` depends on `runtime/kernel`.** It imports `kernel.Scheduler`,
   `kernel.JobStore`, `kernel.ScheduledJob`, `kernel.JobSpec`, and
   `kernel.ErrUnresolvedTimerDefinitions`. A library cannot depend on the
   application layer that consumes it.

2. **The gocron engine lives at repo-root `internal/scheduling/gocron`** (a tree
   separate from `scheduling/`). A spun-out `scheduler` module cannot reach a
   sibling repo-root `internal/`. (The engine also imports
   `internal/observability` — a transitive blocker.)

3. **`scheduling` depends on `definition/schedule`.** `Scheduler.Schedule` and
   the gocron engine take `schedule.TriggerSpec` — a wrkflw type (with expr-lang
   `Expr` forms). A generic scheduler must own its firing vocabulary.

4. **Timer durability is fused into the state commit (ADR-0027).** Arms/cancels
   travel on `AppliedStep.TimerArms/TimerCancels` and are written to
   `wrkflw_timers` inside `Store.Commit`'s transaction. The scheduler only
   *reads* (`LoadScheduled`) at rehydration. Moving write-ownership to the
   scheduler must not lose the atomic-with-state guarantee.

The wrinkle behind (4): the **runtime (`ProcessDriver`) is vendor-free** — it
depends on the `kernel.InstanceStore` interface and holds **no DB connection**,
so it cannot itself open a transaction. The transaction is owned *inside*
`Store.Commit`, which already calls `transaction.JoinOrBegin(ctx, conn)` — i.e.
it **joins an ambient ctx-transaction if one is present**. That existing seam is
what makes a fully-atomic, vendor-free redesign possible.

**The audit-confirmed timing hazard (BLOCKER-1) that shapes v2:** gocron runs
jobs asynchronously on separate goroutines (executor `jobsIn` + `go func`;
verified against v2.22.0, version-independent). If an immediate/past-due
one-shot is armed *in memory* inside the still-open outer transaction, it can
**fire before the commit**. On the instance-Create path the fire's `Load` (pool
conn, `context.Background`) sees no committed row → `ErrInstanceNotFound` →
`timerFireFunc` drops the fire **permanently**. Symmetrically, disarming
in-memory inside a tx that later rolls back yields a missed fire until restart.
Any design that arms/disarms in-memory inside the ambient tx is therefore
unsafe *for wrkflw's join-an-outer-tx integration* — while remaining perfectly
safe for a library consumer whose `JobStore.Save` self-commits. The Activation
model (D3a) resolves this structurally rather than with compensation hooks.

## Goals

- `scheduler` is self-contained: **zero `kartaladev/wrkflw/*` imports** (no
  `runtime`, no `kernel`, no repo-root `internal/`, and — new — no
  `definition/schedule`). Its only deps are third-party (`gocron`, `clockwork`,
  `otel`, `google/uuid`, `robfig/cron` for pure cron next-run computation —
  already a transitive dep via gocron). It can be lifted out as an independent
  module.
- A `Job` interface is the input to `Schedule`; a `ScheduledJob` extends it with
  `NextRun()`. A gocron-native executable contract (`JobFunc`/`DataProvider`).
- The `Scheduler` retrieves, lists, activates, deactivates, and cancels
  scheduled jobs and routes durability to one-or-more consumer-supplied
  `JobStore`s by a typed `JobKind`.
- `JobStore` has `Save`/`Delete`/`Load`, **used internally by the `Scheduler`**
  (`Schedule` always persists through it), and — for wrkflw — executed
  **atomically with the state commit** via the ambient ctx-transaction.
- Arming in memory is decoupled from persisting via the **Activation model**:
  wrkflw persists in-tx and activates post-commit, so no in-memory timer can
  exist before its durable state is committed.
- The rehydration implementation (definition resolution + executable rebuild)
  stays in `runtime` — only the consumer knows how to rebuild an executable job.

## Non-goals

- Removing or rewriting `definition/schedule`. It stays the wrkflw **authoring**
  vocabulary (including the `Expr`/`EveryExpr` expr-lang forms, which a generic
  scheduler must not know about). `scheduler` gets its own generic `Trigger`;
  the runtime converts between them. No churn to the definition/engine layers'
  use of `schedule.TriggerSpec`.
- Publishing `scheduler` as its own Go module now. This design makes it
  *ready* to spin out (dependency arrows all point inward); the actual module
  split is out of scope.
- Changing the `wrkflw_timers` schema. Same columns; the SQL simply moves behind
  the store-side `TimerWriter`. (A `run_count` column is a deferred follow-up —
  see D16.)

## Decisions

| # | Decision | Choice |
|---|----------|--------|
| D1 | `JobStore` persistence semantics | Port defined in `scheduler`, **implemented by the consumer** (runtime). `Scheduler` holds the port and delegates — `Schedule` always persists through the routed store. (User-confirmed post-audit: the scheduler *has* the port; the consumer implements it.) |
| D2 | Routing across multiple stores | **By `Job.Kind()` discriminator.** Each store registered under a `JobKind`; `Save`/`Delete` route by match; rehydrate loads from every store. A kind with **no registered store is a supported non-durable mode** (used by wrkflw's ADR-0121 event-based start timers) — in-memory only, **no WARN**. |
| D3 | wrkflw runtime write semantics | **Scheduler-owned writes; retire the fused `AppliedStep.TimerArms/TimerCancels`.** Atomicity preserved via the ambient ctx-transaction. |
| **D3a** | **Activation model** (supersedes D11) | **`Job.Activation() ActivationType` ∈ {`ActivationAuto`, `ActivationManual`}.** `Schedule` ALWAYS persists via the routed `JobStore.Save`; it arms in-memory **only if `Auto`**. `Manual` = persist-only; the caller starts it later with `Activate`. **wrkflw jobs are `Manual`** → no in-memory arm can exist inside the ambient tx → the fire-before-commit blocker is structurally gone. Library consumers with self-committing stores use `Auto` and keep single-call ergonomics. |
| D4 | Atomic-commit seam | **`RunInTx` on an optional `TxRunner` capability interface** the runtime type-asserts for (like `Notifier`/`Locker`). No break to `InstanceStore.Commit`. `RunInTx`/`TxRunner`/same-conn discipline are **wrkflw-side (consumer) concerns, not library API**. |
| D5 | Executable contract | `Job.Action() JobFunc` + `Job.Data() DataProvider` where `JobFunc = func(ctx, DataProvider) error` and `DataProvider{ Get(ctx) (map[string]any, error); Static() bool }`. The engine **wraps** the pair as `gocron.NewTask(func(ctx) error { return j.Action()(ctx, j.Data()) })` — **zero gocron task parameters**, so gocron's schedule-time param reflection (nil → panic; non-struct kind → `ErrNewJobWrongTypeOfParameters` in `verifyNonVariadic`) can never trip. A nil/failing provider surfaces at fire time as the task's error (gocron-recovered). The type is named `JobFunc` (not `Action`) to avoid clashing with wrkflw's `action.Action`. *(Open for plan-time review: collapsing to a single `Run(ctx) error` remains on the table if `Data()` proves vestigial.)* |
| D6 | `JobKind` API | **Typed `JobKind string`** + registration option. **Keep the thunk form** `WithJobStore(kind JobKind, provide func() JobStore)` — a plain value reintroduces the driver ↔ jobstore ↔ scheduler construction cycle; the thunk is invoked on first `Start`. |
| D7 | Error sentinel homes | **`ErrUnresolvedTimerDefinitions` moves to `scheduler`** (runtime `JobStore` wraps it; scheduler treats it as non-fatal by `errors.Is`). **`kernel.ErrUnsupportedTrigger` also relocates to `scheduler`** (it describes the scheduler's own trigger vocabulary). |
| D8 | `Job` construction | **Relaxed (post-audit):** `scheduler` still ships `NewJob`/`NewJobWithID`/`NewScheduledJob` constructors for the common case, but `Job`/`ScheduledJob` are **implementable interfaces** — the wrkflw runtime provides its **own concrete implementation** carrying a typed descriptor (see B1) instead of using `NewJob`. |
| D9 | Job IDs | `Job.ID()` is **any non-empty string** (gocron `WithName`, confirmed). `NewJob` auto-generates a UUID string; `NewJobWithID` takes a caller/idgen id (wrkflw uses this for stable timer identity — a random UUID would break cancel-by-identity and rehydration). |
| D10 | Trigger coupling + pure next-run | `scheduler` defines its **own generic `Trigger`** value type (no expr-lang): constructors `At / After / Every / EveryRandom / Cron / Daily / Weekly / Monthly` with a scheduler-owned `ClockTime`. It exposes **`Next(after time.Time) (time.Time, bool)` — a pure computation** used to persist `next_run` in-tx *before* any in-memory arming (generalizes today's `nextRunFor`; covers cron via `robfig/cron` parsing and the calendar shapes, which today persist zero). gocron's *live* next-fire is separate and surfaced via `Scheduled().NextRun()`. The runtime converts `schedule.TriggerSpec → scheduler.Trigger` at Job-build time; the converter is **total over all 10 `schedule.Kind` values** — executable kinds convert, `Unset`/`Expr`/`EveryExpr` are explicit conversion **errors** (engine resolves expr forms before arming). |
| D11 | ~~Single-call persist+arm~~ | **SUPERSEDED by D3a.** The audit proved arming in-memory inside the ambient tx lets an immediate one-shot fire before commit (Create-path fire → `ErrInstanceNotFound` → permanent drop) and a rolled-back cancel lose a fire. Single-call remains safe — and available via `ActivationAuto` — for self-committing stores only. |
| D12 | Package name + unification | Rename `scheduling` → **`scheduler`** (not `schedule` — collides with `definition/schedule`, which the runtime also imports). Unify `scheduling/` + `internal/scheduling/` into ONE tree: public `scheduler/`, hidden impl `scheduler/internal/gocron/**`; delete `internal/scheduling/` entirely. Mild `scheduler.Scheduler` stutter accepted. |
| D13 | gocron version | **Bump the hard pin `v2.21.2` → `v2.22.0`** (CONFIRMED; separate ADR — hard pin). Carries fixes for wrkflw's exact usage: OneTimeJob CPU-spin (#943), shared-mutable-state-across-jobs isolation, "skipped runs don't consume `WithLimitedRuns` budget", `ErrSchedulerBusy`, typed `LimitMode`. Monitor/EventListener surface unchanged; async-executor behaviour verified unchanged vs 2.21.2. Regression-verify existing one-shot `WithLimitedRuns` + singleton behaviour on the bump. |
| D14 | Production scope | **In this refactor: ① observability (gocron native Monitor/EventListeners), ③ overrun (singleton mode), ⑧ `List` enumeration.** ② panic-recovery DROPPED (gocron built-in `callJobWithRecover` → `ErrPanicRecovered`). Deferred to follow-up ADRs: ④ timeout, ⑤ retry/backoff, ⑥ durable-snapshot + run-count/last-run, ⑦ elector metrics, ⑨ phantom-skip-gate. |
| D15 | Retrieval semantics | `Scheduled(ctx, id) (ScheduledJob, error)` — **returns `error`, not bool**, with sentinel **`ErrJobNotFound`** (`workflow-scheduler:` prefix, `errors.Is`-checkable), mirroring `Cancel(ctx, id) error`. Its `NextRun()` reads gocron's **live** next-fire for armed jobs. **`NextRun(id)` is DROPPED** (redundant with `Scheduled().NextRun()`; a `kernel.Scheduler` carryover). `Cancel` of an unknown id returns **nil** (idempotent). |
| D16 | `JobStore` shape | **`Save` / `Delete` / `Load` only — no `Update` in core.** Run-count/last-run tracking is deferred with follow-up ⑥ (needs a `wrkflw_timers.run_count` column; gocron exposes no public run-count accessor — verified, only internal `limitRunsTo.runCount` — and advances `NextRun` internally per fire). |

## Architecture

### Package unification + rename (self-containment)

**Imposed requirement:** unify the two trees `scheduling/` and
`internal/scheduling/` into **one** tree, and **rename the package
`scheduling` → `scheduler`** (chosen over `schedule` because
`definition/schedule` already occupies package name `schedule`, and the runtime
imports both — see D12). Implementation that must stay hidden lives under
`scheduler/internal/`. After the refactor **`internal/scheduling/` is deleted
entirely**; nothing scheduler-related lives at the repo root outside `scheduler/`.

The **spin-out blockers** today are every import of a repo-root `internal/*`
from the current `scheduling/` tree. Three exist, plus one transitive:

1. `scheduling/scheduler.go` → `internal/scheduling/gocron`
2. `scheduling/backend/{postgres,mysql}/elector.go` → `internal/scheduling/gocron/{pgelector,myelector}`
3. `internal/scheduling/gocron/scheduler.go` → `internal/observability` (transitive)

Moves:

- **Relocate the entire `internal/scheduling/` subtree** (engine `scheduler.go`,
  `adapt.go`, and the `pgelector` / `myelector` elector impls) →
  `scheduler/internal/gocron/`, and rename the root tree `scheduling/` →
  `scheduler/`. This unifies both trees under `scheduler/` and brings the gocron
  engine **and both locker/elector implementations inside `scheduler/`** — the
  whole tree can then be lifted out as an independent module. The electors use
  `pgxpool.Pool` / `sql.DB` directly (no `internal/database` coupling), so they
  are already module-portable.
- **Sever `internal/observability`** from the gocron engine. The replacement
  shim must carry more than slog: production item ① needs the **meter-scoped
  instruments** (`Int64Counter`, `Float64Histogram` helpers) that
  `internal/observability` currently provides — port those into a minimal
  in-`scheduler` observability shim alongside the plain `slog` + otel provider
  options the façade already accepts (`WithLogger` / `WithTracerProvider` /
  `WithMeterProvider`). No repo-root `internal/` import may remain under
  `scheduler/`.
- `Locker` / `Elector` **interfaces** stay at the `scheduler` root (already
  there); their **implementations** now live under `scheduler/internal/gocron`
  (electors + gocron locker adapter) and `scheduler/backend/{postgres,mysql}`
  (public wrappers) — all within `scheduler/`.
- `kernel.Scheduler` (runtime port) → **replaced** by `scheduler.Scheduler`
  interface. `runtime` depends on `scheduler`; `processtest.MemScheduler` and
  `runtimetest.RecordingScheduler` are **rewritten** (not re-pointed) against
  the new surface.
- `kernel.JobStore`, `kernel.ScheduledJob`, `kernel.ErrUnresolvedTimerDefinitions`,
  `kernel.ErrUnsupportedTrigger` → `scheduler`. `kernel.JobSpec` stays a wrkflw
  runtime type (the typed descriptor — see B1) and gains a
  `Kind engine.TimerKind` field.
- **`scheduler` gains its own `Trigger`** (D10); the runtime adds a
  `schedule.TriggerSpec → scheduler.Trigger` converter (in `timerops.go` /
  the Job builder). `kernel.ArmedTimer` keeps `schedule.TriggerSpec` (runtime
  side); conversion happens only at the `scheduler.Job` boundary.
- **Test migration** (audit findings): `scheduling/processdriver_e2e_test.go`
  exercises the driver, not the scheduler — move it **out of the `scheduler/`
  tree** (it would otherwise violate self-containment). The elector tests'
  `internal/dbtest` + `persistence` imports must be resolved (relocate the
  helpers or the tests). The self-containment **guard test is AST-based**
  (`go/parser` or `golang.org/x/tools/go/packages` over `scheduler/...`), not a
  grep — it must whitelist the tree's own self-imports and ignore string
  literals, and fail on any other `kartaladev/wrkflw/` import path.

Resulting dependency arrows: `runtime → scheduler` and
`runtime → definition/schedule`; **`scheduler` imports no `kartaladev/wrkflw/*`
at all**.

### Core interfaces (`scheduler`)

```go
package scheduler

// JobKind routes a Job to the JobStore registered for it. (Distinct from a
// Trigger's internal shape discriminator, which stays unexported, and from
// definition/schedule.Kind on the wrkflw side.) A kind with no registered
// store is a supported NON-DURABLE mode: the job is armed in-memory only.
type JobKind string

// ActivationType controls whether Schedule arms the job in-memory immediately.
type ActivationType int

const (
    // ActivationAuto: Schedule persists AND arms in one call. Safe when the
    // JobStore self-commits (the durable record exists before any fire).
    ActivationAuto ActivationType = iota
    // ActivationManual: Schedule persists only; the caller arms later via
    // Activate — after its ambient transaction commits. wrkflw uses this.
    ActivationManual
)

// Trigger is scheduler's OWN generic firing vocabulary — the executable shapes
// a scheduler can run, with no wrkflw coupling. Value type; constructors
// At / After / Every / EveryRandom / Cron / Daily / Weekly / Monthly, with a
// scheduler-owned ClockTime for the calendar shapes. It deliberately does NOT
// carry expr-lang forms — those are a wrkflw authoring concern resolved to
// concrete triggers before scheduler.
type Trigger struct { /* unexported kind + fields; see constructors */ }

func At(t time.Time) Trigger
func After(d time.Duration) Trigger
func Every(d time.Duration) Trigger
func EveryRandom(min, max time.Duration) Trigger
func Cron(expr string) Trigger
func Daily(at ClockTime) Trigger
func Weekly(day time.Weekday, at ClockTime) Trigger
func Monthly(day int, at ClockTime) Trigger

// Next computes the next DUE fire instant for a trigger armed at `after`,
// PURELY (no gocron, no I/O; cron via robfig/cron parsing). One-shots (At,
// After) report ok=true even when already due — fire immediately on arm,
// never dropped; recurring shapes report the next occurrence strictly after
// `after`. ok=false means the trigger can never fire meaningfully: the zero
// Trigger, an At built from the zero time, an unparseable Cron expression,
// or a non-positive recurring interval (Every/EveryRandom). Used to persist
// next_run in-tx before any in-memory arming; gocron's live next-fire is
// separate (surfaced via Scheduled().NextRun()).
func (t Trigger) Next(after time.Time) (next time.Time, ok bool)

// DataProvider yields the argument payload for a Job's JobFunc. The consumer
// may perform optional I/O in Get (e.g. hydrate instance variables), using the
// ctx passed at fire time. Static reports whether Get always yields the same
// map — Static()==true means invariant payload (consumers may cache; a future
// durable-snapshot may persist it); false means a fresh read each call.
type DataProvider interface {
    Get(ctx context.Context) (map[string]any, error)
    Static() bool
}

// JobFunc is the executable contract. It receives the fire-time ctx and the
// job's DataProvider and loads the payload itself via data.Get(ctx). Named
// JobFunc (not Action) to avoid clashing with wrkflw's action.Action.
// The engine schedules it as a ZERO-PARAM gocron task:
//   gocron.NewTask(func(ctx context.Context) error {
//       return j.Action()(ctx, j.Data())
//   })
// so gocron's schedule-time parameter reflection is never engaged.
type JobFunc = func(ctx context.Context, data DataProvider) error

// Job is the unit handed to Schedule. Implementable by consumers (the wrkflw
// runtime provides its own typed implementation — see the descriptor section).
type Job interface {
    ID() string
    Kind() JobKind
    Activation() ActivationType
    Trigger() Trigger
    Action() JobFunc
    Data() DataProvider
}

// ScheduledJob is a Job accepted by the scheduler. NextRun reports the live
// gocron next-fire for ARMED jobs. For a Manual job, the ScheduledJob value
// returned by Schedule carries the Trigger.Next-computed instant — but the
// scheduler itself holds no record of it until Activate; Scheduled/List
// reflect armed jobs only (a persisted-unarmed id is ErrJobNotFound).
type ScheduledJob interface {
    Job
    NextRun() time.Time
}

// JobStore is the durability port — defined here, implemented by the consumer,
// used internally by the Scheduler (Schedule always persists through it).
// One store per JobKind. No Update in core (run-count/last-run deferred).
type JobStore interface {
    // Load rebuilds executable ScheduledJobs for rehydration. Only the
    // consumer knows how to rebuild JobFunc/Data from persisted descriptors.
    Load(ctx context.Context) ([]ScheduledJob, error)
    Save(ctx context.Context, j ScheduledJob) error
    Delete(ctx context.Context, id string) error
}

// Sentinels (workflow-scheduler: prefix, errors.Is-checkable).
var (
    ErrJobNotFound = errors.New("workflow-scheduler: job not found")
    // Moved from kernel:
    ErrUnresolvedTimerDefinitions = errors.New(
        "workflow-scheduler: some scheduled jobs reference unresolved definitions")
    ErrUnsupportedTrigger = errors.New("workflow-scheduler: unsupported trigger")
)
```

**Package-provided constructors (the common case; consumers MAY also implement
`Job` directly — D8 relaxed):**

```go
// NewJob builds an Auto-activation Job with an auto-generated UUID id (string
// form). Errors on invalid input (empty kind, zero trigger, nil func, nil
// provider). Options cover Manual activation etc.
func NewJob(kind JobKind, trig Trigger, fn JobFunc, data DataProvider, opts ...JobOption) (Job, error)

// NewJobWithID builds a Job with a caller-supplied id. wrkflw uses this shape
// (via its own implementation) for STABLE timer identity (idgen-sourced).
func NewJobWithID(id string, kind JobKind, trig Trigger, fn JobFunc, data DataProvider, opts ...JobOption) (Job, error)

// NewScheduledJob wraps a Job with its computed next-run instant.
func NewScheduledJob(j Job, nextRun time.Time) (ScheduledJob, error)

// NewStaticDataProvider wraps a fixed map (Get returns a copy; Static()==true).
func NewStaticDataProvider(m map[string]any) DataProvider

// NewEmptyDataProvider returns a Static provider yielding an empty map.
func NewEmptyDataProvider() DataProvider
```

**ID research result (why `NewJob` generates a string, not a forced UUID).**
gocron's `Job.ID()` is a `uuid.UUID`, but the *external* identifier is a
free-form string via `gocron.WithName`; the wrkflw adapter already maps arbitrary
string keys → gocron UUIDs internally. So `scheduler.Job.ID()` accepts **any
non-empty string**. `NewJob` generates a UUID string as a self-contained default
(via `github.com/google/uuid`, already a transitive dep — no `runtime/idgen`
coupling); wrkflw timers use idgen-sourced stable ids.

### `Scheduler` surface

```go
// Schedule ALWAYS persists via the routed JobStore.Save (joining an ambient
// ctx-tx when one is present). It arms in-memory ONLY when
// j.Activation() == ActivationAuto. For Manual jobs it is persist-only: the
// returned ScheduledJob (NextRun = Trigger.Next(now)) is handed to the CALLER,
// and the scheduler retains NO in-memory record of it until Activate — so a
// caller whose ambient tx rolls back leaves zero scheduler state behind
// (Scheduled/List never expose a rolled-back Manual job). A Manual job of an
// unregistered kind persists nothing and arms nothing (harmless; documented).
Schedule(ctx context.Context, j Job) (ScheduledJob, error)

// Activate arms a ScheduledJob in memory. In-memory-only — no durable write.
// CONTRACT: upsert by job id — activating an id that is already armed REPLACES
// the existing registration (idempotent re-arm; carries forward today's
// remove-then-add engine behaviour). Rehydration uses this primitive after
// Load, so repeated rehydration (elector failover, double Start) never
// accumulates duplicate registrations.
Activate(ctx context.Context, j ScheduledJob) error

// Deactivate disarms the in-memory registration only — no durable write.
Deactivate(ctx context.Context, id string) error

// Cancel is the turnkey form: routed JobStore.Delete (joins ambient tx) +
// in-memory disarm, in one call. Unknown id returns nil (idempotent).
// (wrkflw does NOT use Cancel on its commit path — see the Drive flow.)
Cancel(ctx context.Context, id string) error

// Scheduled retrieves one job; ErrJobNotFound when absent. NextRun() is the
// live gocron next-fire for armed jobs.
Scheduled(ctx context.Context, id string) (ScheduledJob, error)

// List enumerates all scheduled jobs (admin/monitoring; production item ⑧).
List(ctx context.Context) iter.Seq[ScheduledJob]
```

`NextRun(id)` from `kernel.Scheduler` is **dropped** — callers use
`Scheduled(ctx, id)` and read `NextRun()` off the result
(`processtest.MemScheduler.NextFireAt` and driver monitoring update accordingly).

**Why the Activation split is structural, not defensive (D3a).** With
`Manual`, no in-memory registration exists until the consumer explicitly calls
`Activate` — which wrkflw does *after* its transaction commits. There is no
window in which a fire can observe uncommitted state, no compensation hook, no
phantom-from-rollback on the arm path. The library keeps single-call ergonomics
for the common self-committing case (`Auto`), so the fix does not distort the
generic API to serve wrkflw's integration.

**Data resolution — inside the JobFunc (D5).** The scheduler does **not**
resolve data itself and does **not** pass the provider through gocron's task
parameters (audit C1: gocron validates task params at schedule time via
reflection — a nil interface panics, a non-struct kind fails
`verifyNonVariadic` — moving arm-time failures into an opaque layer). Instead
the engine wraps the pair into a zero-param closure (see `JobFunc` doc above).
A `Get` error surfaces as the task's returned error into gocron's job-error
handling. `Static()` is advisory (cacheability / future snapshot).

Registration: `WithJobStore(kind JobKind, provide func() JobStore) Option` —
the **thunk form is kept** (audit M3: a plain value reintroduces the
driver ↔ jobstore ↔ scheduler construction cycle); the thunk is invoked on
first `Start`. The scheduler holds `map[JobKind]JobStore`. `Schedule`/`Cancel`
route by `j.Kind()`; a kind with no registered store is the supported
non-durable mode (in-memory only, no WARN — D2). Rehydration calls `Load` on
**every** registered store and `Activate`s each returned job.

### wrkflw typed descriptor (B1 resolution)

A generic `ScheduledJob` cannot rebuild the typed `wrkflw_timers` row
(`instance_id` / `def_id` / `def_version` / `kind` / `trigger_payload` …) —
three of four auditors flagged this. Resolution (user-chosen): **the runtime
implements `scheduler.Job`/`ScheduledJob` itself** with a concrete type that
carries the typed descriptor:

```go
// runtime (package-private)
type timerJob struct {
    spec kernel.JobSpec // + new field: Kind engine.TimerKind
    // trigger scheduler.Trigger, fn scheduler.JobFunc, data scheduler.DataProvider …
}

func (j *timerJob) Activation() scheduler.ActivationType { return scheduler.ActivationManual }
func (j *timerJob) descriptor() kernel.JobSpec           { return j.spec } // unexported
```

The runtime's `JobStore.Save` (same package) **type-asserts** the incoming
`ScheduledJob` back to its concrete type and recovers the typed
`kernel.JobSpec` — no stringly-typed map extraction, no reverse
`Trigger → TriggerSpec` converter. A `Save` receiving a foreign implementation
under the wrkflw kind is a programming error (typed error return).

### Atomic durability seam (D4) — `TxRunner` + `TimerWriter`

```go
// A capability the InstanceStore MAY implement (type-asserted, like Notifier).
type TxRunner interface {
    RunInTx(ctx context.Context, fn func(txCtx context.Context) error) error
}
```

- SQL `Store.RunInTx`: `transaction.Begin(ctx, conn)` → stash handle in `txCtx`
  → run `fn(txCtx)` → commit (or rollback on error). **Rollback-only detection
  (audit v2-A6):** a joined participant's `Rollback` marks the unit
  rollback-only and the owner's `Commit` then rolls back returning nil — so
  `RunInTx` MUST detect the rollback-only state at commit time and return a
  sentinel error instead of reporting success on a rolled-back tx (else the
  driver would `Activate` jobs whose rows never committed).
- `MemInstanceStore.RunInTx`: runs `fn(ctx)` directly (sequencing-only). The
  Mem store cannot make the fn atomic across its internal maps — rollback-parity
  tests are **scoped to the SQL stores**. Additionally (audit v2-A8): on Mem, an
  fn error occurring AFTER `Commit` inside `RunInTx` leaves the instance
  permanently advanced (there is no undo); Mem fault-injection tests must not
  assume retryability of that state.
- **Atomicity is ctx-carried, not conn-carried (audit v2 correction).**
  `transaction.JoinOrBegin(ctx, conn)` joins the ambient handle found in `ctx`
  unconditionally, IGNORING its `conn` argument (`begin.go:44-47`); a joined
  querier executes on the owner's transaction. There is therefore no "same-conn
  invariant" — any `JoinOrBegin`-aware writer given `txCtx` composes. The real
  requirement is that the **`TimerWriter` targets the same database** as the
  `Store` (guaranteed by wiring both from the same backend), since a writer
  constructed over a different pool would still execute against the store's DB
  via the joined handle. The timer write SQL moves behind a **`TimerWriter`**
  capability (`UpsertJob`/`DeleteJob` over `kernel.JobSpec`), type-asserted off
  the store supplied via `WithTimerStore`, using `JoinOrBegin`. A test asserts
  the true contract: a writer over a different conn handed `txCtx` still joins
  (its write rolls back with the tx).
- **`persistence.CachingInstanceStore` must forward the capability (audit v2
  BLOCKER).** The ADR-0099 production wrapper implements only
  `Create/Load/Commit/Release/Entries` — without forwarding, the `TxRunner`
  type-assert fails on exactly the documented production wiring and atomicity
  silently degrades. It gains `RunInTx` by **delegating to the inner store**,
  and on any error/rollback it **evicts** (never puts) the touched instance
  from the cache — a put-after-commit inside a tx that later rolls back would
  poison the cache. A rollback-through-wrapper test locks this.
- **Both instance-creating and step-committing paths are wrapped** (audit M8):
  `store.Create` (StartInstance arms start-path timers) *and* `store.Commit`
  run inside `RunInTx` with their timer persists.

### Drive commit flow (retire fused arm/cancel)

Per applied step, the runtime computes the step's arm `Job`s and cancel keys
(from `ScheduleTimer`/`CancelTimer` commands + `TimerFired` consumption — the
existing `timerOpsFor` logic, now producing Manual-activation `timerJob`s).

**Direct-Save model (audit v2 revision — replaces routing the in-tx persist
through `sched.Schedule`).** The runtime persists through its OWN `jobStore`
inside the tx; the scheduler is touched only POST-commit. This is load-bearing
for three audit findings at once: (a) `ProcessDriver.Shutdown` closes the owned
scheduler *before* draining in-flight Drive loops (ADR-0133 step order), so any
scheduler call inside `commitFn` would fail with `ErrSchedulerClosed` during
drain and roll back an otherwise-healthy state commit — with direct-Save the
commit survives and only the post-commit `Activate` is skipped (logged; the
committed row rehydrates on next boot, exactly today's WARN-skip semantics);
(b) durability becomes **scheduler-independent** — a consumer-injected
`WithScheduler` (which never gets the runtime `JobStore` registered) persists
timers exactly like the owned default, matching today's fused-path behaviour;
(c) arm and cancel take **symmetric** paths.

```go
var armed []*scheduledTimerJob // built in-tx; nextRun = the persisted Trigger.Next value
err := store.RunInTx(ctx, func(txCtx context.Context) error {
    if _, err := store.Commit(txCtx, token, appliedStep); err != nil { // state+events+outbox
        return err
    }
    for _, j := range armJobs { // JobStore.Save joins the SAME tx (JoinOrBegin)
        sj := newScheduledTimerJob(j) // carries the in-tx-computed next_run
        if err := jobStore.Save(txCtx, sj); err != nil { return err }
        armed = append(armed, sj)
    }
    for _, ck := range cancelKeys { // durable delete joins the tx; NO in-mem disarm yet
        if err := jobStore.deleteTimer(txCtx, ck.instanceID, ck.timerID); err != nil { return err }
    }
    return nil
})
if err != nil {
    return err // tx rolled back: state, events, outbox, timer rows — all reverted; scheduler untouched
}
// Post-commit: flip the in-memory side to match the now-durable truth.
for _, sj := range armed {
    _ = sched.Activate(ctx, sj)   // log-on-error; rehydrate self-heals on restart
}
for _, ck := range cancelKeys {
    _ = sched.Deactivate(ctx, ck.timerID) // in-memory only; idempotent
}
```

The `ScheduledJob` handed to `Activate` is the SAME value persisted in-tx
(collected in `armed`), so the activated `NextRun` never drifts from the
persisted `next_run` (relative triggers are not re-anchored post-commit).

Key properties:

- **No fire before commit** — nothing is in gocron until `Activate`, which runs
  only after the durable row is committed (fixes BLOCKER-1).
- **No missed fire from a rolled-back cancel** — the in-memory timer stays
  armed until `Deactivate`, which runs only after the durable delete committed
  (fixes MAJOR-1). A fire racing the post-commit `Deactivate` is the existing
  benign stale-fire no-op in `handleTimerFired`.
- **Post-commit `Activate` failure or skip** (scheduler already closed during
  shutdown drain) is logged, not fatal: the durable row is committed, so the
  next rehydration re-arms it (self-healing, at-least-once).
- **Arm/cancel interleave race (audit v2-A4, documented):** post-commit flips
  are not serialized across steps — step A (arm T) and concurrent step B
  (cancel T) can order their post-commit `Activate(T)`/`Deactivate(T)` either
  way. The bad order leaves a phantom in-memory arm with no durable row. It
  self-limits: the phantom's fire takes the engine's stale-fire no-op path and
  its `TimerFired` consumption (armedRecurring=false on the deleted row)
  cancels+deactivates it — cost is one spurious no-op fire. A test locks the
  self-limiting behaviour (phantom fires at most once, then disappears).
- The runtime never calls `scheduler.Schedule` for durable timers; the turnkey
  `Schedule`/`Cancel` (persist+arm / delete+disarm in one call) exist for
  library consumers with self-committing stores.
- **Delete `perform`'s `ScheduleTimer`/`CancelTimer` cases** and relocate the
  TimerRetry metric emission (audit completeness: leaving them would double-arm
  alongside the new path).

### Rehydration (stays in runtime)

The runtime `JobStore` impl (registered under `JobKind("wrkflw.timer")`):

- `Load`: today's logic — `timerStore.ListArmed` → resolve def via registry →
  convert `schedule.TriggerSpec → scheduler.Trigger` → build the `JobFunc` +
  `NewStaticDataProvider` → construct the runtime's `timerJob` (Manual) →
  return `[]ScheduledJob`. Unresolved defs are skipped and counted, wrapped in
  the moved `scheduler.ErrUnresolvedTimerDefinitions` (non-fatal; WARN).
- `Save`: type-asserts the descriptor (B1), then **delegates** to the
  store-side `TimerWriter.Upsert` (holds the conn, `JoinOrBegin`). `Delete`
  delegates to `TimerWriter.Delete`. The runtime `JobStore` is a
  **composition**: rebuild/`Load` in `runtime` (needs the definition
  registry), writes delegated to the store-side writer (needs the conn). Mem
  path: `MemTimerStore` as the writer.
- **`next_run` persisted = `Trigger.Next(now)`** computed in-tx (D10) —
  correct values for cron/Daily/Weekly/Monthly, which today persist zero.

The wrkflw timer `JobFunc` closure captures the resolved definition + driver
and delivers a `TimerFired` trigger (retrying on optimistic-CAS conflict),
exactly as `timerFireFunc` does today; its `DataProvider` is static over the
identifying fields (`instance_id`, `timer_id`, `def_id`, `def_version`).

**Event-based start timers (ADR-0121)** — `armStartTimer` /
`RehydrateStartTimers` — are **not durable timer rows**: they register under a
distinct **non-durable `JobKind`** (no store registered → in-memory only,
`ActivationAuto`, no WARN). They currently call the old scheduler signature and
must be ported.

**Rehydration trigger points (audit v2-A5).** Scheduler self-rehydration
(`Load` on every registered store + `Activate` each) is pinned to
**`Start`**; if the lazy first-`Schedule` trigger is kept, it MUST run with a
background-derived context (today's precedent) — never the caller's ctx, so
Load I/O and `Activate`s can never execute inside an ambient transaction. A
`Start`-rehydrate racing a Drive that commits+Activates the same id is safe
because `Activate` is an **upsert by id** (see surface contract): the second
registration replaces the first.

**`ProcessDriver.RehydrateTimers` is RETAINED** (ADR-0102 §5 unchanged),
reimplemented as `Load` + `Activate` over the runtime `JobStore` — it remains
the rehydration path for consumer-injected schedulers and for elector
leadership-acquired re-arms.

### Distributed operation (elector mode)

Arm-locality is UNCHANGED by this design: with `WithElector`, arms are
registered in-memory on whichever replica committed the step; the elector
gates **fires**, not arms. A fire due on a non-leader is skipped; the leader
learns of jobs armed elsewhere only via rehydration (its own `Start`, or
`WithOnLeadershipAcquired → RehydrateTimers` on a leadership change) — exactly
today's recovery model (ADR-0059), now safe under repeated rehydration because
`Activate` is an upsert by id. `examples/mysql_wiring` exercises this path.

## Error handling

- `RunInTx` returns any error from `fn`; the SQL impl rolls back. A failed
  `jobStore.Save` (durable persist) rolls back the state commit too (single
  tx) — no partial arm, and nothing to undo in memory (the scheduler was never
  touched inside the tx).
- Post-commit `Activate`/`Deactivate` failures are logged and non-fatal;
  rehydration reconciles from durable truth on next start.
- `Load` returning `ErrUnresolvedTimerDefinitions` is non-fatal for
  auto-rehydrate (partial registration + WARN), as today.
- Per-job registration errors during rehydrate are logged and skipped (one
  unschedulable job never aborts the batch), as today.
- `Scheduled` → `ErrJobNotFound`; `Cancel`/`Deactivate` of unknown ids → nil.
- Converter errors (`Unset`/`Expr`/`EveryExpr` reaching the boundary) are
  programming errors surfaced as wrapped `ErrUnsupportedTrigger`.

## Testing

- **TDD strict**, red-first per new symbol (`Job`, `ScheduledJob`, `JobKind`,
  `ActivationType`, `Trigger` + constructors + `Next`, `DataProvider`,
  `JobFunc`, constructors, `JobStore` methods, `TxRunner.RunInTx`,
  `TimerWriter`, `Scheduler.Schedule/Activate/Deactivate/Cancel/Scheduled/List`,
  runtime `JobStore` write methods, `TriggerSpec→Trigger` converter).
- **Self-containment guard test — AST-based** (`go/packages` over
  `scheduler/...`), failing on any `kartaladev/wrkflw/` import; whitelists
  self-imports; ignores string literals. (Not a grep — audit dep-F5.)
- **Trigger conversion tests:** total over all 10 `schedule.Kind` values —
  equivalence for executable kinds, explicit **error branches** for
  `Unset`/`Expr`/`EveryExpr`.
- **`Trigger.Next` tests:** every shape incl. cron and calendar forms;
  past-due one-shots (`At`/`After`) return the already-due instant with
  `ok=true` (fire immediately on arm); `ok=false` means the trigger can
  never fire meaningfully — the zero Trigger, an `At` of the zero time, an
  unparseable `Cron`, or a non-positive recurring interval.
- **Parity-first for the invariant-retiring change (D3):** before deleting
  `AppliedStep.TimerArms/TimerCancels` and `upsertTimer`/`deleteTimer`, port the
  existing durability tests to drive the new `RunInTx` + `Schedule(Manual)` path
  and prove `wrkflw_timers` rows land **in the same tx** as state (rollback →
  neither persists). Only delete the fused path once the new path is green.
- **Audit-mandated additions:** descriptor round-trip (all 8 `wrkflw_timers`
  columns through Save→Load); **join-by-ctx** test (a writer over a foreign
  conn handed `txCtx` still joins — its write rolls back with the tx);
  **double-arm** regression (perform-path cases deleted); **Create-path
  rollback** (StartInstance tx rollback leaves no timer row *and* no in-memory
  arm); activation-model test (Manual job never fires before `Activate`);
  rolled-back-cancel test (timer still fires); **rollback leaves no scheduler
  state** (`Scheduled` → `ErrJobNotFound`, `List` empty after a rolled-back
  arm); **double-`Activate` upsert** (same id re-armed, never duplicated);
  **arm/cancel interleave** (phantom fires at most once, then disappears);
  **rollback-through-`CachingInstanceStore`** (capability forwarded, cache
  evicted — never poisoned — on rollback); **RunInTx rollback-only detection**
  (joined participant marks rollback-only → sentinel error, not nil).
  Rollback-parity scoped to SQL stores (MAJOR-2).
- **gocron v2.22.0 bump regression:** re-verify one-shot `WithLimitedRuns` +
  singleton overrun behaviour under the new pin, and re-verify ADR-0135's fix
  claims against the fetched v2.22.0 source/changelog at bump time (they are
  unverifiable offline until the module is fetched).
- Conformance tests for each `JobStore`/`TimerWriter` impl (Mem + SQL dialects)
  via the `internal/dbtest` helpers (`dbtest.RunTestDatabase` /
  `dbtest.RunTestMySQL` / `dbtest.RunTestSQLite`). The existing store
  conformance/fault suites that seed timers via `Commit(AppliedStep{TimerArms})`
  re-seed through `TimerWriter.UpsertJob`.
- Table tests per the project `table-test` skill (assert-closure form, `ctx`
  modifier). Mocks via `use-mockgen` (`--typed`). Black-box (`_test` packages);
  testable examples for the new `scheduler` public API.
- Coverage ≥ 85% on touched packages; `go test ./...` + `golangci-lint run ./...`
  clean.

## Migration / blast radius

- `scheduler` (renamed from `scheduling`): type moves + `JobKind` routing +
  Activation model + `Job`/`DataProvider` + constructors + `Trigger`+`Next`;
  **relocate `internal/scheduling/` → `scheduler/internal/gocron/`** (engine +
  electors + locker adapter) and rename the root tree; **sever
  `internal/observability`** (port meter-scoped instruments into the shim);
  re-point `scheduler/backend/{postgres,mysql}`; delete `internal/scheduling/`;
  move `processdriver_e2e_test.go` out of the tree; **gocron `trigger.go` is a
  REWRITE, not a move** (new `Trigger` vocabulary + zero-param task wrapping).
- gocron pin bump `v2.21.2 → v2.22.0` in `go.mod` (D13; separate ADR — hard pin).
- `runtime`: `jobstore.go` (typed `timerJob`, Save/Delete delegation, `Load`),
  Drive commit path (`RunInTx` wrap + post-commit Activate/Deactivate — both
  Create and Commit paths), `timerops.go` (produce Manual `timerJob`s +
  converter), retire `AppliedStep` timer fields, delete `perform`'s timer
  cases + relocate TimerRetry metric, port ADR-0121 start-timer path to the
  non-durable kind, re-point `kernel.Scheduler` → `scheduler.Scheduler`.
- `kernel`: delete moved symbols (`Scheduler`, `JobStore`, `ScheduledJob`,
  sentinels); keep `ArmedTimer`/`TimerStore` (read side) + `JobSpec`
  (descriptor; + `Kind engine.TimerKind` field).
- `internal/persistence/store`: add `RunInTx` (with rollback-only detection);
  expose `TimerWriter` (`JoinOrBegin`, same database); drop
  `upsertTimer`/`deleteTimer` from `Commit` (after parity).
- `persistence.CachingInstanceStore`: forward `TxRunner` (delegate to inner;
  evict — never put — the touched instance on error/rollback).
- `persistence.PruneTimers` interplay: `next_run` is written once at arm and
  never updated (D16 defers `Update`), so a recurring timer's persisted
  `next_run` goes stale after its first fire — a routine
  `PruneTimers(olderThan)` would delete a **still-armed** recurring timer's
  durable row. Exclude recurring trigger kinds from the prune predicate now
  (one WHERE clause) and note the caveat in `docs/production-checklist.md`;
  the full fix (run-count/last-run updates) rides deferred item ⑥.
- `processtest.MemScheduler` + `runtimetest.RecordingScheduler`: **rewrites**
  against the new surface (`NextFireAt()` is arg-less/global — it enumerates
  via `List`, not `Scheduled`; `Pending`/`Tick` keep keying by engine timer
  id).
- `examples/`: **eleven** mains touch the old surface and must be updated —
  `timer_boundary`, `inwait_reminder`, `usertask_deadline`,
  `event_based_gateway`, `catch_event_reminder`, `retry_recovery`,
  `boundary_action`, `timer_durability`, `production_wiring`, `mysql_wiring`
  (semantic dependency: leadership-acquired `RehydrateTimers`), and
  `sqlite_wiring`.
- `persistence/scheduler_locker.go`: public API returns `scheduling.Locker`
  today — its return-type import path changes with the rename (CHANGELOG
  entry).
- **ADR-0134** records the decision (Nygard template), noting it supersedes the
  write-ownership half of ADR-0027 while *preserving* its atomicity guarantee
  through the ambient ctx-transaction + Activation model.

## Production-readiness scope (D14 resolved)

**In this refactor:**

1. **Observability via gocron's native hooks [in].** Implement gocron's
   **`MonitorStatus`** (`RecordJobTimingWithStatus` + `IncrementJob`) backed by
   the OTel meter, wired via `WithMonitorStatus` — per-status counters
   (`success/fail/skip/singleton_rescheduled`) + fire-timing histogram, keyed by
   job id/name/tags. Register **`AfterJobRunsWithError`**,
   **`AfterJobRunsWithPanic`** (structured panic log + stack), and
   **`AfterLockError`** via `WithEventListeners`, backed by `slog`. Requires the
   meter-scoped instrument helpers ported into the shim (see moves). Subsumes
   bespoke panic recovery — gocron's `callJobWithRecover` → `ErrPanicRecovered`
   already keeps the worker alive (verified unchanged in v2.22.0).
2. **Overrun protection [in].** Default recurring jobs to
   `WithSingletonMode(gocron.LimitModeReschedule)` (typed `LimitMode` in
   v2.22.0) with a per-Job opt-out. Pairs with the v2.22.0 "skipped runs don't
   consume `WithLimitedRuns` budget" fix so a one-shot's single run is never
   burned by a skip.
3. **`List(ctx)` enumeration [in]** — see D15; pairs with `Scheduled` retrieve.

**Deferred (follow-up ADRs):** ④ per-fire timeout (`WithJobTimeout`), ⑤ action
retry/backoff (mirror `action.RetrySpecs`, ADR-0126), ⑥ durable-snapshot for
Static providers + run-count/last-run (needs `wrkflw_timers.run_count`; also
unblocks pruner-safe retention for recurring rows — see Migration),
⑦ elector leadership metrics + failover test, ⑨ phantom-skip-gate
(`BeforeJobRunsSkipIfBeforeFuncErrors` re-checking the durable arm — largely
moot now that Manual activation eliminates the arm-path phantom; the only
residual phantom is the benign post-commit-Deactivate race).

## Phasing (re-sequenced post-audit; plan will expand)

1. **Mechanical, compile-preserving move first:** rename `scheduling` →
   `scheduler`, relocate `internal/scheduling/**` → `scheduler/internal/gocron/**`,
   sever `internal/observability` (shim), move the e2e test out — **keeping the
   old `kernel.Scheduler` signature intact** so the tree compiles and all tests
   stay green. One commit.
2. gocron pin bump v2.21.2 → v2.22.0 (+ its ADR + regression checks).
3. New `scheduler` vocabulary red-first: `Trigger` (+`Next`), `Job`/`ScheduledJob`/
   `JobKind`/`ActivationType`/`DataProvider`/`JobFunc`, constructors, sentinels,
   `JobStore` port. gocron `trigger.go` rewrite + zero-param task wrapping.
4. **Interface reshape in one movement** (audit M7): `Scheduler` surface
   (Schedule/Activate/Deactivate/Cancel/Scheduled/List) + all call-sites +
   test-double **rewrites** together — no long-lived half-converted state.
5. `TxRunner.RunInTx` (Mem + SQL) + `TimerWriter` capability — red-first.
6. Runtime typed `timerJob` + `JobStore` (Save type-assert / Delete / Load) +
   parity tests proving in-tx atomicity + activation-ordering tests.
7. Wire Drive commit + Create through `RunInTx` + post-commit
   Activate/Deactivate; delete `perform` timer cases; retire `AppliedStep`
   timer fields + `upsertTimer`/`deleteTimer` once parity is green.
8. Production items ①③⑧ (observability shim wiring, singleton default, List).
9. Update `processtest`/`runtimetest`/examples; self-containment AST guard;
   ADR-0134; docs/godoc.

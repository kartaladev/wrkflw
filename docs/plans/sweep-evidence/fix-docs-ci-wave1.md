# Fix wave 1 — docs, CI, example wiring

Agent: docs/CI/examples wave-1 fixer. Branch: `main` (working tree; the controller commits).
Date: 2026-08-20.

Scope handed to this agent: backlog **95**, **48**, **58**+**121**, **blocker 7**, then
(second message) **113**, **122**, new **127**, then (third message) **3d**, **3e**, **38**.

Rule applied throughout: no replacement claim was written until the symbol it asserts over
was read or executed. Each claim below names that symbol.

---

## Item 95 — `STABILITY.md` stale facts (+ adjacent README rot) — **DONE**

Files changed: `STABILITY.md`, `README.md`.

### Claims corrected, with the symbol verified against

| was | now | verified against |
|---|---|---|
| `STABILITY.md:81` "`gocron` pinned to **v2.21.2**" | "pinned to **v2.22.0**" | `go.mod:11` → `github.com/go-co-op/gocron/v2 v2.22.0` |
| `STABILITY.md:35` lists root package **`model/`** | **`definition/`** | `ls -d */` at repo root: `action definition docs engine eventing examples humantask internal observability persistence processtest runtime scheduler scripts service transport`. There is no `model/`; the package is `definition/model` (`package model` declared in `definition/model/definition.go:6`). README's own "Package map" (`README.md:223`) already lists `definition`, not `model`. |
| `README.md:633` "There are **19** node kinds" | the closed set, named | see below |

### The node-kind statement — re-derived, not copied

Executed reads:

- `definition/model/definition.go:15-35` declares **18** `NodeKind` constants:
  `KindUnspecified` (iota 0), `KindStartEvent`, `KindEndEvent`, `KindServiceTask`,
  `KindUserTask`, `KindReceiveTask`, `KindSendTask`, `KindBusinessRuleTask`,
  `KindSubProcess`, `KindCallActivity`, `KindIntermediateCatchEvent`,
  `KindIntermediateThrowEvent`, `KindBoundaryEvent`, `KindExclusiveGateway`,
  `KindParallelGateway`, `KindInclusiveGateway`, `KindEventBasedGateway`,
  `KindCompensationThrowEvent`.
  ⇒ **17 authorable kinds** once the iota-zero sentinel is excluded.
- `engine/step_dispatch.go:36` `nodeStrategies` has **16** entries
  (`awk 'NR>=36 && NR<=60' engine/step_dispatch.go | grep -c "model.Kind"` → `16`).
- `engine/step_nodes_test.go:11` `armBearingKinds` lists the same **16**;
  `engine/step_nodes_test.go:32` `intentionallyUnhandledKinds` = `{KindBoundaryEvent,
  KindUnspecified}`.
- 17 authorable − `KindBoundaryEvent` = 16 = the registry size. Consistent.

⚠ **Both prior numbers were wrong.** README said 19; the handover said 18 (that is the
*constant* count, which includes the zero sentinel). Neither was written.

The replacement text names the closed set explicitly and cites
`definition/model/definition.go`, `engine/step_dispatch.go` and
`intentionallyUnhandledKinds`, so a future reader can re-derive it rather than trust a
count. Per the brief: **no bare count is printed.**

### ⚠ Item 120 — DEFERRED TO OWNER, both documents left untouched

`samber/do` v2 is listed as a **locked dependency** in two places:

- `STABILITY.md:81` — "Locked dependencies (… `casbin`, **`samber/do` v2**) are changed only via an ADR."
- `CLAUDE.md` tech-stack table — "DI container | `samber/do` v2 | application-layer wiring only".

Executed: `grep -n "samber" go.mod` returns **only** `github.com/samber/hot v0.13.0` (line
25) and the indirect `github.com/samber/go-singleflightx v0.3.2` (line 128).
**`samber/do` is not in `go.mod` and is imported by zero files.** The `CLAUDE.md`
Dependency Injection section additionally states it is used "internally and in `examples/`
reference wiring" — which is also not true today.

Two options, for the owner:

1. **Adopt it.** Add `github.com/samber/do/v2` and actually wire the internal/example
   composition the two documents describe. Larger than a docs fix; the `examples/`
   wiring would have to change.
2. **Correct both documents.** Drop `samber/do` from the `STABILITY.md` locked list and
   restate the `CLAUDE.md` row as an *intended* choice not yet adopted (or delete the row
   and the DI section's claim of current internal use).

**Neither was applied.** The `samber/do` rows in `STABILITY.md` and `CLAUDE.md` are
byte-identical to what they were before this pass.

---

## Item 127 (NEW — no backlog entry existed) — `README.md` documented a constructor that does not exist — **DONE**

File changed: `README.md`.

Verification: `grep -rn "func NewErrorEnd" .` → **the only hit in the whole repo is
`docs/plans/2026-06-23-node-interface-redesign.md:343`**, a 2026-06 design document
proposing `NewErrorEndEvent`. There is **no `NewErrorEnd` and no `NewErrorEndEvent` in any
Go file.** `grep -h "^func New" definition/event/*.go` lists exactly: `NewStart`, `NewEnd`,
`NewIntermediateCatch`, `NewIntermediateThrow`, `NewCompensateThrow`, `NewBoundary`.

README's Events table advertised `event.NewErrorEnd(id, errorCode string, name ...string) Node`
as a distinct **ErrorEndEvent** node kind, with a worked example
`event.NewErrorEnd("insufficient-funds", "FUNDS_ERROR")` that could not compile.

Real API, read at `definition/event/options.go:327-341`: an error end event is an
**`EndEvent`** carrying `event.WithErrorCode(code) EndOption`, whose `applyEnd` sets
`n.Behavior = EndError; n.ErrorCode = o.code`. Its godoc states the empty-code case is an
anonymous catch-all and that it is mutually exclusive with `WithForceTermination`
(`definition/event/options.go:322,336-338`) — both now stated in README.

There is correspondingly **no `KindErrorEndEvent`** in the `NodeKind` enum (see item 95's
enumeration), which is why the row was removable rather than fixable in place.

### Same class, found in the same tables and fixed in the same pass

All verified by reading the constructor:

| README claim | reality | symbol |
|---|---|---|
| `activity.NewCallActivity(id, defRef string, opts ...)` | second param is `model.Qualifier`, not `string` | `definition/activity/activity.go:206` `func NewCallActivity(id string, ref model.Qualifier, opts ...ActivityOption) model.Node` |
| example `activity.NewCallActivity("credit-check", "credit-check")` | would not compile | rewritten to `model.Latest("credit-check")` — `definition/model/qualifier.go:22` `func Latest(id string) Qualifier` |
| `gateway.NewExclusive/NewParallel/NewInclusive/NewEventBased(id string, name ...string)` | all four take `opts ...Option` | `definition/gateway/gateway.go:60,66,72,78` |
| "Gateways take no options beyond an optional name" | there are **two** options | `definition/gateway/gateway.go:40-46` — `type Option func(*model.Base)`, `WithName`, `WithLabel` |
| prose naming `NewIntermediateCatchEvent`, `NewIntermediateThrowEvent`, `NewBoundaryEvent` | constructors are `NewIntermediateCatch`, `NewIntermediateThrow`, `NewBoundary` | `definition/event/event.go:236,246,269` |

### ⚠ Also found and fixed: README documented the **pre-`TriggerSpec`** timer API

This was not in any backlog item. README instructed readers to pass **quoted duration
strings** to every timer option:

> "Durations are expr-lang expressions parsed by Go's `time.ParseDuration`. Write them as
> quoted Go-duration strings — `` `"1h"` `` … This applies to `WithBoundaryTimer`,
> `WithCatchTimer`, `WithWaitDeadline` …, `WithWaitAction` …, and `WithStartTimer`."

**False for all six.** Executed signature reads:

- `definition/event/options.go:121` `func WithStartTimer(t schedule.TriggerSpec) StartOption`
- `definition/event/options.go:162` `func WithCatchTimer(t schedule.TriggerSpec) CatchOption`
- `definition/event/options.go:171` `func WithWaitDeadline(t schedule.TriggerSpec, flowID string) CatchOption`
- `definition/event/options.go:187` `func WithWaitAction(t schedule.TriggerSpec, action string) CatchOption`
- `definition/event/options.go:251` `func WithBoundaryTimer(t schedule.TriggerSpec) BoundaryOption`
- `definition/activity/options.go:140,174` — the activity twins, same `schedule.TriggerSpec` first param

and `definition/schedule/trigger.go:33` `type TriggerSpec struct{ … }` is a **struct with
unexported fields**, buildable only via `AfterDuration`/`At`/`AfterExpr`/`Every`/
`EveryExpr`/`EveryRandom`/`Cron`/`Daily`/`Weekly`/`Monthly`
(`definition/schedule/trigger.go:47-89`). A bare string is a compile error.

README was also **internally inconsistent**: two examples already used the current API
(`event.WithCatchTimer(schedule.AfterDuration(24*time.Hour))`,
`event.WithWaitAction(schedule.Every(30*time.Minute), "nudge")`), matching what
`examples/scenarios/timer_boundary/main.go:69` and
`examples/scenarios/usertask_deadline/main.go:81` actually compile against, while seven
other lines used the dead string form.

Fixed: the callout now describes `schedule.TriggerSpec` and enumerates the ten
constructors; the seven stale example lines and the six stale option-list signatures were
rewritten. The expr-lang duration-string note survives only where it is true — inside
`schedule.AfterExpr` / `schedule.EveryExpr`, whose godoc
(`definition/schedule/trigger.go:52-53,59-60`) says the expression "must evaluate to a
duration string".

⚠ **Not swept:** the YAML-authoring section and the scenario prose further down README were
not re-verified line by line. A full README-vs-code sweep is a larger item than this wave.

---

## Item 48 — CI ran `go test` at the implicit 600 s default, with nothing enforcing ADR-0184's rule — **DONE**

Files changed: `.github/workflows/ci.yml`, `scripts/coverage.sh`, **new** `scripts/check-test-timeout.sh`.

### What was true before

- `.github/workflows/ci.yml` → `go test -race -coverprofile=cover.out ./...`
- `scripts/coverage.sh:23` → `go test -race -coverprofile="${profile}" ./...`

Neither passed `-timeout`, so both ran at Go's implicit 600 s per-binary default,
and **nothing checked** the rule stated in `scheduler/waitbudget_test.go:30-31`:
*"budget × the densest package's site count must stay under 600s."*

### What shipped

Both invocations now pass an explicit `-timeout=600s` (same value as the implicit
default — this change makes it **checkable**, it does not relax or tighten it).

`scripts/check-test-timeout.sh` enforces the rule, and runs as its own CI job
(`test-timeout-budget`) so a violation is still reported when the suite itself is
the thing timing out. It is pure bash + grep: no Go toolchain, no Docker.

### Why it is not vacuous — every number is DERIVED

The script hard-codes nothing. It reads the timeout from the `-timeout=` flags
actually present in the two files (and fails if they disagree), the budget from
each `const eventuallyBudget` in each `waitbudget_test.go`, and the site count by
grepping that package's `*_test.go` (occurrences, minus comment lines and the
declaration itself). `find`-driven, so a fifth `waitbudget_test.go` is picked up
automatically.

Observed output today:

```
PACKAGE                                      BUDGET  SITES    PRODUCT VERDICT
scheduler/internal/gocron/myelector             10s      2        20s ok
scheduler/internal/gocron/pgelector             10s      1        10s ok
scheduler/internal/gocron                       10s     34       340s ok
scheduler                                       10s      6        60s ok

go test -timeout: 600s (600s), agreed by .github/workflows/ci.yml and scripts/coverage.sh
EXIT=0
```

The derived counts (6 / 34 / 2 / 1) matched an independent hand count at the moment of
that run.

⭐ **They have already moved.** A re-run later in the same session reported
`scheduler` at **8** sites, not 6 — another agent added `Eventually` sites while this was
being written. The guard picked the change up with no edit, which is the whole argument for
deriving rather than pinning: a hard-coded version would have gone stale **within the
hour**. Do not quote these numbers as facts; run the script.

### Mutation-verified — what makes it FAIL, executed

⚠ Run against a **scratch mirror** of the four files in the scratchpad, never
against `scheduler/` in the working tree — another agent owns that package
concurrently. The mirror was `diff`ed against the repo afterwards and is
**IDENTICAL**, so nothing in `scheduler/` was touched.

| mutation | result | exit |
|---|---|---|
| raise `scheduler/internal/gocron` budget 10s → 20s | `20s × 34 = 680s` **OVER LIMIT** | 1 |
| add 26 `eventuallyBudget` sites to gocron (34 → 60) | `10s × 60 = 600s` **OVER LIMIT** | 1 |
| lower `-timeout` to 300s in **both** files | `10s × 34 = 340s` **OVER LIMIT** | 1 |
| ci.yml 300s vs coverage.sh 600s | "timeout mismatch" | 1 |
| remove `-timeout` from ci.yml entirely | "no -timeout flag found" | 1 |
| **restored** | table green | **0** |

The boundary case is exact: 600s is reported OVER LIMIT (`>=`, not `>`), because a
product that merely *equals* the timeout still dies as `panic: test timed out`.

### ⚠ Reported, NOT fixed — a rotted enumeration in `scheduler/waitbudget_test.go`

Its doc comment says *"scheduler/internal/gocron carries **31** of the **40** sites"*
and *"At 10s it is **310s**"*. Re-derived today: **34 of 43**, and **340s**. The
comment rotted, which is precisely the failure mode Premise Discipline names.

**Not edited** — `scheduler/` belongs to another agent in this wave, and its files
are already modified in the working tree. The new script makes the numbers
self-deriving, so the comment is now the only stale copy. **For the controller to
route.**

### Scope limitation, stated so a green run is not over-read

The guard covers `Eventually` sites that pass the shared `eventuallyBudget`
constant — the convention ADR-0184 established. A site passing a bare literal is
not counted. `require.Never` budgets are deliberately excluded (ADR-0184 §4: a
`Never` budget is paid in full on every GREEN run and is a different cost model).
Both limitations are stated in the script's own header.

---

## Blocker 7 — one container boot per PACKAGE — **DONE**

Files changed: **new** `internal/dbtest/dsn.go`, **new** `internal/dbtest/dsn_test.go`,
**new** `internal/dbtest/export_test.go`, `internal/dbtest/postgres.go`,
`internal/dbtest/mysql.go`, **new** `scripts/testdb.sh`, `.github/workflows/ci.yml`.

### Premise, re-verified (not inherited)

- `internal/dbtest/postgres.go:87` `sharedOnce sync.Once`; `internal/dbtest/mysql.go:31`
  `mysqlSharedOnce sync.Once`. Package-level `sync.Once` + one test binary per
  package ⇒ one boot per package.
- `WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN`: **0 hits repo-wide** before
  this change. `scripts/` held exactly `check-extraction.sh` and `coverage.sh`.

⚠ The **12 / 7** package counts are **inherited from triage**, which hand-counted
them from `grep -rl`. I did not re-derive them. They appear in the new package doc
and in `scripts/testdb.sh` as *"counted 2026-08-20"*, dated so the provenance is
visible. `ASSUMPTION (unverified by this agent)`.

⚠ The **wall-clock benefit is likewise unmeasured** — measuring it needs Docker and
two full suite runs, which this agent was instructed not to do. No speedup number
is claimed anywhere in the code or docs; the mechanism (19 boots → 2) is stated,
the saving is not.

### What shipped

`PostgresDSNForDB(base, dbName)` / `MySQLDSNForDB(base, dbName)` rewrite only the
database segment of an operator-supplied DSN, so **per-test isolation is
unchanged** — each test still gets its own `CREATE DATABASE`, dropped in
`t.Cleanup`. `sharedBase` (Postgres) and `initMySQLContainer` (MySQL) consult the
env var first and boot a container only when it is blank.

`scripts/testdb.sh up|down|env|status` manages the two long-lived servers, mirroring
`internal/dbtest`'s own images (`postgres:17-alpine`, `mysql:8.0`), credentials and
`max_connections=300`. Non-default host ports (55432 / 53306) so it cannot collide
with a developer's own local server. CI runs the same script and exports via
`$GITHUB_ENV`.

**The fallback is the default, as required.** With neither variable set the boot
path is byte-for-byte the previous one. Verified by `TestEnvDSNSelectsTheContainerFallback`.

### TDD trail

1. **RED** — `internal/dbtest/dsn_test.go` written first;
   `go test ./internal/dbtest/...` → `EXIT=1`, five `undefined:` compile errors
   (`dbtest.EnvPostgresDSN`, `dbtest.PostgresDSNForDB`, `dbtest.EnvMySQLDSN`,
   `dbtest.MySQLDSNForDB`).
2. **GREEN attempt** — `EXIT=1`, two MySQL cases failing:
   `"…?multiStatements=true&parseTime=true" does not contain "loc=UTC"`.
   ⭐ **The test was wrong, not the code**: `FormatDSN` omits `loc=` when it equals
   the driver's default (UTC), so the DSN was already correct. The assertions were
   rewritten to re-parse the produced DSN with `mysqldriver.ParseDSN` and assert the
   **effective** `cfg.DBName` / `ParseTime` / `Loc` / `MultiStatements` — strictly
   stronger than a substring match, since it is the driver's own interpretation.
3. **GREEN** — `EXIT=0`.

### Mutation-verified

`TestEnvDSNSelectsTheContainerFallback` passed on its first run (`envDSN` already
trimmed), so it is a **characterization test, disclosed as such**, and was
mutation-verified rather than assumed:

- First attempt — `envDSN` → `os.Getenv(name)` — produced a **build failure**
  (unused import `strings`). ⚠ Per the standing lesson, *a mutation that fails to
  compile is not a RED*, so it was discarded, not counted.
- Second attempt — `envDSN` → `strings.Clone(os.Getenv(name))`, which compiles —
  gave a genuine assertion RED, `EXIT=1`:
  `blank_means_container_path: Should be empty, but was "   \t "` and
  `a_real_DSN_diverts_to_the_shared_server,_trimmed: Not equal`.
- Restored from a `cp` backup (**not** `git checkout <path>`, which restores from
  the index and destroys uncommitted work); `diff` confirms **FILE IDENTICAL**;
  re-run `EXIT=0`.

### Cross-check: the script's DSNs actually parse

The exact strings `scripts/testdb.sh` prints were run through the helpers' logic
and observed:

```
PG  err=<nil> -> postgres://wrkflw:wrkflw@127.0.0.1:55432/wrkflw_test_42?sslmode=disable
SQL err=<nil> -> root:wrkflw_root@tcp(127.0.0.1:53306)/wrkflw_test_42?multiStatements=true&parseTime=true
    reparsed: db=wrkflw_test_42 parseTime=true loc=UTC multi=true
```

⚠ Run in a **standalone throwaway module** in the scratchpad, because at that
moment `go test ./internal/dbtest/...` could not build: another agent's in-flight
edit left `runtime/monitor/stats_collector.go:101,112` with `undefined: clockwork`
/ `undefined: time`, and `dbtest` imports it transitively. **That breakage is not
mine and was not touched.** The probe module is deleted.

### ⚠ NOT verified by this agent — the live path

Nobody has run `RunTestDatabase`/`RunTestMySQL` against a real server through the
env branch; that needs Docker, which was out of scope. What is verified: the DSN
rewriting, the default-selection decision, that the whole package builds and vets,
and that the script's own output is accepted by the parsers. **The first CI run
with `testdb.sh` wired in is the real test of the live path.**

One design decision worth flagging to the owner: `allocTestMySQLDB` now opens its
admin connection against a new `adminDSN` field rather than `rootDSN(mysqlDefaultDB)`.
On the container branch these are the same string; on the env branch they differ,
because `wrkflw_test` may not exist on an operator's server while the database
their DSN names certainly does.

---

## Items 58 + 121 — `examples/production_wiring` durability and driver lifecycle — **DONE**

File changed: `examples/production_wiring/main.go`.

### Premises re-verified before changing anything

- **The timers DO fire.** `runtime/processdriver.go:245-271`: with no `WithScheduler`,
  `NewProcessDriver` builds `scheduler.NewScheduler(schedOpts...)` and assigns **both**
  `driver.sched` and `driver.ownedScheduler`. The backlog's original "timers silently
  disabled" was FALSE and was not re-propagated.
- **No `WithTimerStore`, no `WithScheduler`** — 0 hits in the file before this change.
- **`driver.` appeared nowhere** (item 121): `grep -n "driver\."` returned nothing, so
  neither `driver.Start` nor `driver.Shutdown` was ever called.
- **`notifier` appeared 3×, all in comments** — the example has no notifier at all.
- **`AdminRoutes` never mounted**; no metrics.

### ⭐ Two further defects found while verifying, both now fixed

1. **The `sched` the example built was DEAD CODE that made a false claim.**
   `scheduler.NewScheduler(scheduler.WithClock(clk), ...)` was constructed and registered
   via `shutdown.AddCloser(sched)`, but never passed to the driver — so the driver built
   its *own* scheduler and the registered one fired nothing. The adjacent comment *"One
   clock drives both the engine and the scheduler (ADR-0003); a single fake-clock advance
   moves both under test"* was therefore **false in both halves**: `clk` reached only the
   unused scheduler, and the driver was never given `WithClock` at all.

2. **The shutdown ordering comment was inverted.** The example said *"The pool is the
   lowest-level resource: closed LAST (registered first)"*, but the pool was registered
   **third** (after `evClose` and `sched`). `runtime/shutdown.go:80-98` iterates
   `for i := len(fns) - 1; i >= 0; i--` — **reverse** registration order — and its `Add`
   godoc states *"Components registered later are shut down earlier."* So the pool was in
   fact closed **FIRST**, before anything that still needed the database.

### Why the fix does NOT inject a scheduler — and a library gap this exposed

`runtime.WithScheduler` was deliberately **not** used. Durable timer rehydration depends on
a `scheduler.JobStore` registered under the runtime's timer job kind, and
`runtime/processdriver.go:256` registers it **only on the driver-owned path**:

```go
schedOpts = append(schedOpts, scheduler.WithJobStore(timerJobKind, func() scheduler.JobStore { return driver.jobStore }))
```

⚠ **`timerJobKind` is UNEXPORTED** (`runtime/timerjob.go:16`, `= "wrkflw.timer"`). Although
`runtime.NewJobStore(driver)` *is* exported (`runtime/jobstore.go:69`), a consumer who
injects their own scheduler has no exported way to name the kind to register it under —
short of hardcoding the string literal. **So injecting a scheduler silently costs you timer
durability, which is exactly the defect item 58 is about.** Reported as a library-ergonomics
finding; nothing in `runtime/` was changed (another agent owns it).

Using the owned default is therefore both the thinner change and the only one that works.

### What shipped

- `runtime.WithTimerStore(persistence.NewTimerStore(pool))` on the `DATABASE_URL` branch —
  appended conditionally, so the in-memory branch never receives a nil store.
- `runtime.WithDefinitions(reg)` — `runtime/timerops.go:434` requires a non-nil `defsReg`
  or `RehydrateTimers` refuses outright.
- `runtime.WithClock(clk)`, `runtime.WithLogger(logger)`, `runtime.WithMeterProvider(...)`.
- `driver.Start(workerCtx)` + `shutdown.Add(driver.Shutdown)` (**item 121**), registered
  after the pool so it drains **before** the pool closes.
- `driver.RehydrateTimers(workerCtx)`, guarded on `timerStore != nil` so the in-memory
  branch does not log a spurious refusal.
- The dead `sched` and the `scheduler` import removed; the false clock and shutdown-order
  comments rewritten to what the code does.
- Metrics: an `sdkmetric.NewMeterProvider()` injected into driver **and** transport, and
  flushed via `shutdown.Add(meterProvider.Shutdown)`. No new dependency —
  `go.opentelemetry.io/otel/sdk/metric` is already direct in `go.mod:34`. The comment is
  explicit that a bare provider has no reader and a real consumer attaches one.
- `AdminRoutes` mounted (see below).

Registration order is now `meterProvider, evClose, pool, driver`, so the reverse-order drain
is **driver → pool → evClose → meterProvider**: timer fires finish while the database and
publisher are still open, and metrics flush last.

### `AdminRoutes` mounted, fail-closed

Verified `transport/http/stdlib/groups.go` registers **every** admin route under `/admin/`
(10 paths, all prefixed), so one prefix handler covers the subtree. The example mounts them
on their own `adminMux` and wraps it in `requireAdminToken`, which **fails closed**: with
`ADMIN_TOKEN` unset every request is refused 503, so forgetting to configure it cannot
silently expose the admin surface. Comparison is `subtle.ConstantTimeCompare`. The
`AdminRoutes` godoc's SECURITY caveat (*"NO built-in authentication … admin-by-composition,
ADR-0095"*) is quoted in the example.

### ⚠ NOT verified — the restart probe

The durability claim ("a 2 s timer never fires after restart without `WithTimerStore`") was
**not** executed: it needs a live Postgres and two process lifetimes, and this agent was
instructed not to start containers. What **is** verified is the precondition and the
mechanism — `RehydrateTimers` refuses without a timer store (`runtime/timerops.go:434-435`),
and the example previously had none. `ASSUMPTION (unverified): the observable symptom.`

Verification actually run: `go build ./examples/... ` → **EXIT=0**;
`go vet ./examples/...` → **EXIT=0**; `gofmt -l examples/` → empty;
`grep -c notifier` → **0**; `grep -n "driver\."` → `Start`, `Shutdown`, `RehydrateTimers`.

---

## Item 122 — `examples/broker_wiring` relay comment — **DONE**

File changed: `examples/broker_wiring/main.go`.

⚠ **Both the comment AND the backlog's correction were false.** I re-read the source rather
than restating either:

| claim | source | verdict |
|---|---|---|
| "`Run` … loops `DrainOnce`" | `internal/persistence/store/relay.go:505,516,524` — all three call sites call **`drainUntilEmpty`** | **FALSE** |
| "`Run` … with backoff" | `relay.go:482` `ticker := r.clk.NewTicker(r.poll)`; nothing in the loop grows, jitters or resets the interval | **FALSE** — the pacing is a fixed ticker |
| backlog: "there is no backoff in `Run`, `drainUntilEmpty` or `DrainOnce`" | `relay.go:296`, inside `DrainOnce` (`:241`): `nextAttempt := now.Add(RelayBackoff(c.retryCount, r.backoff.base, r.backoff.max))` | **ALSO FALSE** — there IS a per-row backoff |
| "(on Postgres/MySQL) LISTEN/NOTIFY wakeups" | `relay.go:489-501` starts `listenLoop` only when `r.notifier != nil`; `internal/persistence/dialect/dialect.go:200-212` — SQLite implements no `Notifier` | **TRUE**, and sharpened: this SQLite example is poll-only |

Supporting values read, not assumed: `relay.go:196` `poll: time.Second`, `:200-201`
`backoff.base = time.Second` / `backoff.max = time.Minute`, `:198` `maxDel: 10`,
`internal/persistence/store/relay_backoff.go:11-23` (doubling, capped at `maxInterval`),
`relay.go:452-468` (`drainUntilEmpty` loops `DrainOnce` until 0, returns on first error).

The rewritten comment says: `Run` paces on a **fixed** poll ticker (default 1s) and calls
`drainUntilEmpty`, which loops `DrainOnce` until it returns 0 so a burst coalesces into one
sweep; the **only** backoff is per-row inside `DrainOnce`, writing `next_attempt_at` with a
capped exponential (1s → 1m) and quarantining to `'dead'` at `MaxDeliveryAttempts`; SQLite
is poll-only because it implements no `Notifier`.

---

## Item 38 — `Incidents[0]` read positionally in `examples/scenarios/admin_monitoring` — **DONE**

File changed: `examples/scenarios/admin_monitoring/main.go`.

### The defect, and why it matters

`main.go:248,250` read `parked.Incidents[0].ID` and fed it straight to
`driver.ResolveIncident`. Verified refusal path: `engine/step_triggers.go:1426-1432` —

```go
if inc.Kind != IncidentAction {
    ... return StepResult{}, ErrIncidentNotResolvable
}
```

so any walk-scoped incident (`IncidentCompensationStall`, `IncidentCompensationFailed`,
both with empty `TokenID` per `engine/state.go:159-184`) landing at index 0 makes the example
fail. Fixed by selecting on `Kind` via a new `firstResolvableIncident` helper, with an
explicit error when no resolvable incident exists.

### The fifth-site check the brief asked for

`grep -rn "Incidents\[0\]"` over every `.go` file in the repo. Outside `_test.go` files
there are exactly **two** occurrences, and **one comment**:

| site | verdict |
|---|---|
| `examples/scenarios/admin_monitoring/main.go:248,250` | **the defect — fixed here** |
| `processtest/park.go:446` (`return state.Incidents[0].NodeID`) | **correct, leave alone.** It is the documented last-resort fallback of `incidentNode`, reached only after `tokenScopedIncident` and the `TokenIncident` token scan; `park.go:429-432` explains that dropping it silently breaks a stall park's node naming. Bundle C's de-positionalisation. |
| `runtime/terminal_cause.go:17` | a **comment** describing the positional read it replaced, not a read |

⇒ **There is no fifth site.** Nothing outside `examples/` was touched.

`go build ./examples/...` → **EXIT=0**.

---

## Item 113 — no rolling-upgrade statement — **DONE** (audit's wording REJECTED)

Files changed: `STABILITY.md` (new section), `CHANGELOG.md`.

### The audit's proposal is rejected, in writing

The audit proposed publishing *"schema N works with library N-1"*. **That would be a false
guarantee**, contradicted by three sources I re-read rather than inherited:

| source | text verified today |
|---|---|
| `docs/adr/0173-*.md:226` | "**Mixed-version deployments are NOT safe.**" |
| `docs/adr/0175-*.md:277` | "⚠ **Mixed-version deployment is unsafe.** … an old build round-trips a new snapshot with `Kind` **dropped**" |
| `engine/state.go:221-227` (on `Incident.Kind`) | "⚠ Kind enters the persisted snapshot. An OLD build round-trips a NEW snapshot with Kind dropped … Do not run pre-0175 and post-0175 builds against the same instance store." |

`grep -cin "N-1\|rolling upgrade\|rolling-upgrade\|mixed.version" CHANGELOG.md` → **0**,
confirming no statement existed.

### The structural root cause, verified

`internal/persistence/store/store_core.go:78` and `:216` —
`json.Marshal(capHistory(step.State, s.historyCap))` — marshal the **whole**
`engine.InstanceState`; `:164` decodes with a plain `json.Unmarshal`, and
`grep -n "DisallowUnknownFields" store_core.go` returns **nothing**. So the loss is not two
incidents but a property: every future `InstanceState` field inherits it.

### What was published

`STABILITY.md` gains a **"Rolling upgrades are NOT supported"** section naming the
mechanism, the two shipped instances, and the drain-and-stop upgrade procedure.
`CHANGELOG.md` carries the summary under Breaking changes.

⚠ **One hedge deliberately preserved.** ADR-0173 says the *forward* direction (new code
reading an old snapshot) is safe. I did **not** restate that as a general guarantee — it is
a per-ADR property of a specific field whose zero value was defined to mean "pre-ADR
record". `STABILITY.md` says exactly that and notes a future state-carrying change owes the
same analysis in its own ADR. Restating it unhedged is the inherited-claim failure mode.

---

## Items 3d + 3e — `CHANGELOG.md` release notes — **DONE**

File changed: `CHANGELOG.md`. Written as **one pass** together with item 113, as instructed.

### 3e (breaking) — verified

`service/service.go:97` declares
`ResolveCompensationStall(ctx context.Context, req ResolveCompensationStallRequest) (ProcessInstance, error)`
inside `InstanceOps`, which `Service` embeds (`service/service.go:115-121`). Any consumer
type satisfying `Service` by declaring its own methods stops compiling. The entry gives the
**embed-don't-re-declare** migration with a worked decorator example.

### 3d (additive) — verified

`service/instance.go:129` `Incidents []incidentJSON`, `:136`
`Compensating *compensatingJSON \`json:"compensating,omitempty"\``, `:214`
`Kind string \`json:"kind"\``, and `:181-196` `compensatingJSON{ActiveCommandID, Since *time.Time, ScopeID}`.
⚠ Confirmed **not** in `httpcore.InstanceView`, which carries no incident or compensation
fields. The entry reproduces the *"do not route on `incidents[0]`"* caveat from
`incidentJSON.Kind`'s own godoc (`service/instance.go:210-213`).

`grep -cin "compensating|active_command_id|ResolveCompensationStall" CHANGELOG.md` was **0**
before this change, confirming both notes were genuinely missing.

⚠ **Vacuity, stated plainly:** a CHANGELOG edit is not testable and no test was invented to
pretend otherwise. The 3e half is already pinned by the compiler.

⚠ Another agent may add a scheduler item-28 release note to the same file; nothing outside
these three entries was edited, so a concurrent addition merges cleanly.

---

## Verification actually run by this agent

| check | result |
|---|---|
| `go build ./...` | **EXIT=0** |
| `go vet ./...` (compiles Docker-only test files) | **EXIT=0** |
| `go build ./examples/...` | **EXIT=0** |
| `go test -race -count=1` on the three new `internal/dbtest` tests | **EXIT=0**, 14 subtests pass |
| `golangci-lint run ./internal/dbtest/... ./examples/...` | **EXIT=0**, 0 issues |
| `bash scripts/check-test-timeout.sh` | **EXIT=0** |
| `bash -n scripts/testdb.sh` | clean |
| `gofmt -l examples/ internal/dbtest/` | empty |

⚠ **`golangci-lint run ./...` (repo-wide) FAILS, and not because of this agent.** The sole
finding is `scheduler/trigger_test.go:793-847` — `undefined: assert`, `undefined: require`,
`undefined: math` — another agent's in-flight edit with imports not yet added. My packages
are clean; **the package-scoped run above is a PARTIAL result and the repo-wide gate has not
passed.**

⚠ **No database tests were run and no containers were started**, per the brief. The coverage
floor was therefore not measured, and nothing here should be read as the Verification
section passing.

---

## Findings reported, NOT fixed (for the controller to route)

1. **`scheduler/waitbudget_test.go:22-31` has a rotted enumeration** — says gocron carries
   *"31 of the 40 sites"* and *"At 10s it is 310s"*; re-derived today it is **34 of 43** and
   **340s**. Not edited: `scheduler/` belongs to another agent this wave.
2. **`runtime.timerJobKind` is unexported**, so a consumer who injects their own scheduler
   via `WithScheduler` cannot register the durable timer `JobStore` and silently loses timer
   durability — the exact defect item 58 is about. `runtime/timerjob.go:16`.
3. **`runtime/timerops.go:415` overstates its own requirement.** Its godoc says
   `RehydrateTimers` "Requires WithScheduler", but the code guards on
   `driver.sched == nil`, which the driver-**owned** default also satisfies
   (`processdriver.go:262` sets `driver.sched`). The owned path works; the godoc says it
   does not.
4. **Item 120 (`samber/do` listed as locked but absent from `go.mod`)** — options recorded
   above; both documents left untouched, pending the owner's decision.

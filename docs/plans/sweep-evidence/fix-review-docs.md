# `/code-review` findings 5 (MEDIUM) and 7 (LOW) — documentation truth

Branch `feat/backlog-sweep-small-tier`, main working tree. Scope: `docs/observability.md`
(backlog item **108**) and `CHANGELOG.md`.

Every replacement claim below is backed by the symbol it was read from or by a compiler
exit code. No claim in the corrected document was written from memory.

---

## Finding 5 — `docs/observability.md`

### Method

The failure mode of this file is that its wiring recipes do not compile. Reading them
cannot detect that, so each recipe was extracted into a **separate Go module outside the
repo tree** — `module scratch/<name>` with
`replace github.com/kartaladev/wrkflw => /Users/zakyalvan/Documents/RND/wrkflw` — and
built. An external module also makes the ADR-0004 internal-package leak visible: in-module
code may import `internal/`, so an in-repo compile would have proved nothing.

The final pass extracted the four ```` ```go ```` blocks **verbatim from the committed
markdown** (regex over the file, wrapped as `package main` + `func main() {}`), so what is
recorded green below is the text a reader will copy, not a nearby paraphrase.

### Recipe compile results — BEFORE

Each block is the doc's own text at the review commit.

```
===== before_outbox =====      (doc §"Outbox stats")
./main.go:12:20: undefined: runtime.OutboxStatsReader
./main.go:15:28: undefined: runtime.NewOutboxStatsCollector
./main.go:17:17: undefined: observability.WithMeterProvider
EXIT=1

===== before_timer =====       (doc §"Timer stats")
./main.go:12:25: undefined: runtime.TimerStatsReader
./main.go:15:28: undefined: runtime.NewTimerStatsCollector
./main.go:17:17: undefined: observability.WithMeterProvider
EXIT=1

===== before_health =====      (doc §"Health-probe recipe")
main.go:5:2: no required module provides package github.com/kartaladev/wrkflw/rest; to add it:
	go get github.com/kartaladev/wrkflw/rest
EXIT=1
```

Six distinct compile errors across two recipes, plus a third recipe that cannot resolve its
import at all. The reviewer named three defects in the wiring section; the compiler found
more, and the health recipe — which the reviewer did not mention — was the worst of the
three, since `github.com/kartaladev/wrkflw/rest` has never existed (`ls rest` →
`No such file or directory`; no package in the module declares `package rest`).

### Recipe compile results — AFTER (extracted verbatim from the corrected markdown)

```
===== doc_outbox =====  EXIT=0
===== doc_timer  =====  EXIT=0
===== doc_pool   =====  EXIT=0
===== doc_health =====  EXIT=0
```

`go vet ./...` in each of the four scratch modules: clean, no output.

### Corrected claims and their verification

| Old claim | Why false | Replacement | Verified by |
|---|---|---|---|
| `runtime.NewOutboxStatsCollector` / `NewTimerStatsCollector` | Package is `runtime/monitor`, not `runtime` | `monitor.New…` | `go doc ./runtime/monitor` lists both under `package monitor` |
| `collector, err := …` | Neither constructor returns an error | single return value | `runtime/monitor/stats_collector.go:39` `func NewOutboxStatsCollector(r kernel.OutboxStatsReader, opts ...Option) *OutboxStatsCollector`; `:142` likewise |
| "Both constructors accept the same `observability.Option` variadic" | They take `monitor.Option`; and the `internal/observability` leak this sentence described **is what this commit fixed** (backlog 116) | `monitor.Option` with `WithMeterProvider` / `WithLogger` / `WithTracerProvider` / `WithClock` | `runtime/monitor/options.go:24` `type Option func(*collectorConfig)`; the four `With*` at `:55/:66/:76/:86`. Guard: `runtime/monitor/internal_leak_test.go` `TestNoExportedSignatureNamesAnInternalType` |
| "`NewTimerStatsCollector` registers `wrkflw_timers_armed`" | It registers **two** gauges | both `wrkflw_timers_armed` and `wrkflw_timers_next_fire_age_seconds` | `stats_collector.go:149` and `:158`, both observed in the one `RegisterCallback` at `:168` |
| "`relay` is a `*persistence.Relay` (or any `runtime.OutboxStatsReader`)" | `persistence.Relay` is an interface, not a pointer; the reader interface is `kernel.OutboxStatsReader` | recipe takes `persistence.Relay` and the prose names `kernel.OutboxStatsReader` | `go doc ./persistence Relay` (interface, declares `OutboxStats`); `go doc ./runtime/kernel OutboxStatsReader` |
| "`timerStore` is a `*persistence.TimerStore` (or any `runtime.TimerStatsReader`)" | There is no `persistence.TimerStore` type. `persistence.NewTimerStore` returns `kernel.TimerStore`, whose method set does **not** include `Stats` — so the value cannot be passed to the collector without an assertion | recipe asserts `store.(kernel.TimerStatsReader)` and handles the failure | `go doc ./runtime/kernel TimerStore` (methods: `ListArmed`, `ArmedTimer` — no `Stats`); the implementer is `internal/persistence/store/timerstore.go:45` `var _ kernel.TimerStatsReader = (*TimerStore)(nil)` |
| `rest.NewHealthHandler` / `rest.WithHealthCheck` / "returns a `rest.HealthCheck`" | The `rest` package does not exist anywhere in the module | `stdlib.MountHealth(mux, relayCheck)`; the contract is `httpcore.HealthCheck` | `transport/http/stdlib/mount.go:26` `func MountHealth(mux *http.ServeMux, checks ...httpcore.HealthCheck)`; `persistence/relay_health.go:37` doc states structural satisfaction; `go doc ./persistence RelayBacklogCheck` returns the struct, not an interface |
| Admin table omits ADR-0175's escape verb | Route is registered unconditionally | added `POST /admin/instances/{id}/compensation/resolve-stall` under `Svc` (always) | `transport/http/stdlib/groups.go` `AdminRoutes.Customize`, registered in the "Always-present admin routes" block before any nil guard; present in all three adapters (`grep -c` → stdlib 1, fiber 2, gin 3 occurrences) |
| `wrkflw_instances_completed_total` "reached a terminal end-event **successfully**" | Emitted on **any** terminal status, carrying a `status` label | corrected, with the label's real values | `runtime/processdriver.go:739` — `if st.Status.IsTerminal() && !prevStatus.IsTerminal()` with `attribute.String("status", st.Status.String())` |
| Labels column read `—` for eleven metrics that carry attributes | — | per-metric label sets filled in | `def` at `processdriver.go:735/739/746` and `processdriver_incident.go:26`; `event` at `processdriver_action.go:497` + `runtime/task/service.go:202/237/258/313`; `action`+`retryable` at `processdriver_action.go:379/402`; `action`+`outcome` at `:369` (`outcome = "ok"` at `:416`); `trigger` at `processdriver.go:688`; `op` at `internal/persistence/store/store_core.go:148/203`; `http.method`/`http.route`/`http.status_code` at `transport/http/httpcore/observability.go` `Observe`; `status`+`job_id` at `scheduler/internal/gocron/monitor.go` |
| Two metrics marked "**New this release.**", one section headed "(new this release)" | Stale — those instruments predate several shipped ADRs; nothing has been released (`## [Unreleased]` is still the only section in `CHANGELOG.md`) | markers dropped | `grep -n '^## ' CHANGELOG.md` → `## [Unreleased]` only |

### Metrics that were missing from the inventory

Derived by `grep -rhoE '"wrkflw_[a-z_]+"' --include='*.go' .` and then filtering out table
names, index names and test-only literals by opening each registration site. The closed set
of real instruments the file did not list:

- `wrkflw_timer_arms_refused_total` — `runtime/observability.go:56`
- `wrkflw_timers_next_fire_age_seconds` — `runtime/monitor/stats_collector.go:158`
- `wrkflw_db_pool_in_use` / `_idle` / `_max_open` / `_waits_total` — `persistence/pool_stats.go:149/158/167/176`
- `wrkflw_scheduler_job_runs_total` / `wrkflw_scheduler_job_duration_seconds` — `scheduler/internal/gocron/monitor.go:33/37`
- `wrkflw_eventing_published_total` — `internal/eventing/watermill/publisher.go:52`
- `wrkflw_human_task_audit_drops_total` — `internal/persistence/store/humantask_store.go:106`
- `wrkflw_authz_policy_reload_failures_total` — `internal/authz/casbin/db.go:83`

⚠ The reviewer's brief said "**5** real metrics". The re-derived count is larger; the set is
named above rather than counted in the document, per the repo's own
count-them-again rule. Names excluded on inspection, so the next reader need not redo the
work: `wrkflw_outbox`, `wrkflw_timers`, `wrkflw_instances`, `wrkflw_journal`, `wrkflw_root`,
`wrkflw_call_links`, `wrkflw_chain_links`, `wrkflw_badrows`, `wrkflw_casbin_policy`
(tables/indexes: `wrkflw_timers_keyset_idx`, `wrkflw_timers_next_run_idx`), and the
`wrkflw_*_test` literals.

### Claims re-verified and left unchanged

- Alert-rules table: all nine alerts, severities and thresholds match
  `docs/dashboards/wrkflw-alerts.yml` (parsed, not eyeballed).
- `schemaVersion 39` and the `${DS_PROMETHEUS}` datasource: match the dashboard JSON.
- All three runbook paths and `docs/retention.md` exist.
- Every optional `AdminRoutes` field is nil-guarded, and the field→route mapping is correct
  for the nine pre-existing rows.

### Claims ADDED where the doc had been silent

- `/readyz` response shapes (`200 {"status":"ok"}` / `503 {"status":"unavailable"}`, failing
  checks reported without the underlying error) — `transport/http/httpcore/health.go`
  `EvaluateReady`.
- `resolve-stall` request body: `command_id` and `disposition` REQUIRED, `incident_id`
  optional — `transport/http/httpcore/dto.go:145` `ResolveCompensationStallInput` and
  `ParseCompensationDisposition`'s fail-closed rationale at `:157`.
- gin/fiber register the same admin routes with `:id` rather than `{id}` —
  `grep -ohE '"/admin[^"]*"'` across the three adapters.
- The shipped Grafana dashboard has **no** panel for `wrkflw_timers_next_fire_age_seconds`,
  `wrkflw_timer_arms_refused_total` or the `wrkflw_db_pool_*` series — enumerated from the
  dashboard JSON's own `targets[].expr`. Called out in the document rather than silently
  implying coverage. Fixing the dashboard is out of scope here (it would touch
  `docs/dashboards/`).

### Is backlog 108 closed?

**Yes for the document.** The wiring section, the second (health) recipe, the admin table
and the metric inventory were all corrected and the recipes now compile from outside the
module. Item 108 had been blocked on item 116 — no doc text could make the recipe callable
off-module while the constructors named an `internal/` type — and 116 ships in this commit,
which is what unblocked it.

Two follow-ups fall outside this file and are **not** closed by it:

1. `docs/dashboards/wrkflw-overview.json` has no panels for the three newer signals above.
2. `persistence.NewSchedulerLocker` is still an ADR-0004 leak of the same class as 116
   (`knownOpenInternalLeaks` in `runtime/monitor/internal_leak_test.go`), and is tracked on
   its own.

---

## Finding 7 — `CHANGELOG.md`

The public surface was **re-derived** with `go doc ./persistence` and `go doc
./runtime/monitor` immediately before the entry was written, not taken from the review
text, because another agent was concurrently reworking the persistence facade's option
surface (`persistence/options.go`, and `DeduperOption` on the three `New*Deduper`
constructors).

### Re-derivation, run immediately before the entry was finalised

```
$ go doc ./persistence | grep -E 'Deduper|PoolStats|WarnUnsafeSQLite'
func WarnUnsafeSQLite(ctx context.Context, logger *slog.Logger, db *sql.DB)
type Deduper interface{ ... }
    func NewDeduper(pool *pgxpool.Pool, opts ...DeduperOption) (Deduper, error)
    func NewMySQLDeduper(db *sql.DB, opts ...DeduperOption) (Deduper, error)
    func NewSQLiteDeduper(db *sql.DB, opts ...DeduperOption) (Deduper, error)
type DeduperOption func(*deduperConfig)
    func WithDeduperClock(clk clockwork.Clock) DeduperOption
type PoolStatsCollector struct{ ... }
    func NewPoolStatsCollector(db *sql.DB, opts ...PoolStatsOption) *PoolStatsCollector
    func NewPostgresPoolStatsCollector(pool *pgxpool.Pool, opts ...PoolStatsOption) *PoolStatsCollector
type PoolStatsOption func(*poolStatsConfig)
    func WithPoolStatsLogger(l *slog.Logger) PoolStatsOption
    func WithPoolStatsMeterProvider(mp metric.MeterProvider) PoolStatsOption

$ go doc ./runtime/monitor | grep -E 'Option|StatsCollector'
type Option func(*collectorConfig)
    func WithClock(clk clockwork.Clock) Option
    func WithLogger(l *slog.Logger) Option
    func WithMeterProvider(mp metric.MeterProvider) Option
    func WithTracerProvider(tp trace.TracerProvider) Option
type OutboxStatsCollector struct{ ... }
    func NewOutboxStatsCollector(r kernel.OutboxStatsReader, opts ...Option) *OutboxStatsCollector
type TimerStatsCollector struct{ ... }
    func NewTimerStatsCollector(r kernel.TimerStatsReader, opts ...Option) *TimerStatsCollector
```

**`NewSQLiteDeduper` did change under this work**, exactly as the brief anticipated: the
review text listed it as `(db *sql.DB) (Deduper, error)`, and by the time the entry was
written the concurrent finding-4 work had added `opts ...DeduperOption` to all three deduper
constructors. The CHANGELOG documents the signature that is actually there. The
`DeduperOption` / clock-injection story itself is the other delivery's to describe; this entry
does not claim it.

### Entries written

1. **Breaking changes** — the `monitor.Option` signature change on `NewOutboxStatsCollector`
   and `NewTimerStatsCollector`, placed under the breaking heading rather than "Added" because
   two exported signatures changed. Verified against
   `git diff main -- runtime/monitor/stats_collector.go`:
   `opts ...observability.Option` → `opts ...Option`. In-repo non-test callers:
   **none** — `grep -rn 'NewOutboxStatsCollector\|NewTimerStatsCollector' --exclude='*_test.go'`
   returns only the declarations and doc references in `runtime/monitor` itself.
2. **Added** — `persistence.PoolStatsCollector` and its two constructors, the new
   `wrkflw_timers_next_fire_age_seconds` gauge, `persistence.NewSQLiteDeduper`, and
   `persistence.WarnUnsafeSQLite` / `WarnMsgSQLiteBusyTimeout`.

### Claims corrected during self-review of my own draft

Two sentences I wrote were false and were caught by executing them before shipping — the same
failure mode this document exists to fix:

- I wrote that `wrkflw_instances_completed_total`'s `status` label takes `Completed`,
  `Cancelled`, `Failed`. `engine/state.go:26` `func (s Status) String()` returns **lowercase**
  values, and there is no `cancelled`; the terminal set per `IsTerminal` is `completed`,
  `failed`, `terminated`. Corrected in `docs/observability.md`.
- I wrote that `OpenSQLite` probes when `MaxOpenConns > 1`. `persistence/unsafe_config.go`
  `WarnUnsafeSQLite` tests `maxOpen == 1` and returns early only then — so `0`, which
  `database/sql` means as *unlimited* and is the widest pool of all, **is** probed. My version
  would have told a reader the default configuration is exempt. Corrected in `CHANGELOG.md`.

### Other claims verified before writing

- `wrkflw_db_pool_waits_total` is an **observable counter**, not a gauge —
  `persistence/pool_stats.go:175` `tel.Meter.Int64ObservableCounter`.
- "costs no extra query" for the overdue-timer gauge —
  `internal/persistence/store/timerstore.go` `statsNative` issues one
  `SELECT count(*), MIN(next_run) FROM wrkflw_timers`; `NextFireAt` was already in the result
  and discarded.
- "Every SQLite DSN in the package's doc examples" — scoped to the `persistence` package,
  which is exactly what `TestDocumentedSQLiteDSNsSetBusyTimeout` enforces: `harvestDocDSNs`
  reads `os.ReadDir(".")`, non-test `.go` files only.
- Nil-handle tolerance and "no background goroutine" — `persistence/pool_stats.go:97` and
  `:122` return zero `poolStats`; all reads run inside the single `RegisterCallback` at `:184`.

### Verification

- `go build ./...` → `EXIT=0`
- `go vet ./...` → `EXIT=0`

Both were run repo-wide while three other agents held uncommitted work in `engine/`,
`internal/dbtest/` and `persistence/`; both were green at that moment. Neither is evidence
*about this task*, which touched no Go file — the changes are `docs/observability.md`,
`CHANGELOG.md` and this record. The recipe compiles above are the evidence that matters, and
they were run against the module as a `replace` target, so they exercised the real tree.

⚠ **Not run:** `go test ./...`, `golangci-lint`, and the coverage gate. This task was
docs-only and explicitly scoped away from Docker. The Verification section's items 1–3 remain
the controller's to run over the folded commit.

# Bounded armed-timer reads

**Status**: approved design, revised after adversarial audit, not yet implemented
**Date**: 2026-07-30
**Branch**: `feat/bounded-armed-timer-reads` (off `main` @ `9656799`)
**ADR**: 0159

> **Revision note.** A first version of this bundle proposed deleting
> `kernel.TimerStore.ListArmed`, streaming `scheduler.JobStore.Load` as
> `iter.Seq2`, and adding a `0002_*.sql` migration. Two independent Opus audits
> returned "do not implement": the streaming half was read-your-own-writes and
> could fail to terminate, the migration violated an executable project policy,
> and the row-value keyset predicate was **measured** to degrade to a full index
> scan on MySQL. The scope is now narrowed to the fire path plus admin paging.
> `scheduler.JobStore` is untouched. See "Audit corrections" at the end for the
> full list of what changed and why.

## Problem

`ProcessDriver.armedTimerRecurring` (`runtime/timerops.go:175-194`) answers one
boolean — *is the timer that just fired armed with a recurring trigger?* — for a
known `(instanceID, timerID)` pair. To answer it, it reads the **entire**
armed-timer table and linear-searches the result:

```go
armed, err := driver.timerStore.ListArmed(ctx)
...
for _, a := range armed {
    if a.InstanceID == instanceID && a.TimerID == timerID {
        return a.Trigger.Recurring()
    }
}
```

`ListArmed` on the SQL backend is unfiltered — no `WHERE`, no `LIMIT`
(`internal/persistence/store/timerstore.go:83-86`). It runs on **every**
`TimerFired` step (`runtime/timerops.go:158-165`, closure supplied at
`runtime/processdriver.go:691-694`), and every row's `trigger_payload` is
JSON-unmarshalled and passed through `model.ReadTrigger` during the scan
(`internal/persistence/store/timerstore.go:304`, `325-334`). So the per-fire cost
is N rows plus N JSON decodes to produce one bit.

**Cost model.** Per fire the cost is O(N) rows and O(N) trigger decodes. In steady
state — a stable population of N armed timers with mean lifetime P, so timers are
armed as fast as they fire — the fire rate is N/P and the aggregate is **N²/P rows
scanned per second**. By Little's law this holds for one-shot timers as well as
recurring ones, with P the mean lifetime.

The dominant term is **N**, not lifetime: a long lifetime means a *large* P, which
divides the rate. So the worst workloads are large populations, and at equal N a
shorter period is worse. Both shapes occur here — 100k human-task deadline waiters
at a 3-day lifetime is ≈38.6k rows/s; 1k in-wait reminders on a 60s period is
≈16.7k rows/s at a thousandth of the population.

The in-memory path degrades on the same axis. `MemTimerStore.armed` is already
`map[timerKey]ArmedTimer` keyed by exactly `(instanceID, timerID)`
(`runtime/kernel/timerstore.go:65-70`), so the answer is one map lookup. Instead
`ListArmed` copies all N entries out under `s.mu` and then sorts them
(`runtime/kernel/timerstore.go:92-105`), on every fire. Embedded and SQLite
deployments therefore pay an allocation plus O(N log N) per timer fire.

Separately, the admin endpoint `AdminTimers`
(`transport/http/httpcore/admin_endpoints.go:333-359`) serializes every armed
timer into one JSON body with no limit or cursor.

## Requirements

1. The fire path must read **one row**, not N.
2. The admin listing must be pageable, following the paging conventions this
   repository already uses.
3. **The fire path's cancel decisions must not change**, except for one behaviour
   delta that is explicitly recorded and tested (see "The one behaviour delta").
4. Startup rehydration must keep its current single-statement snapshot
   consistency.

Requirement 4 is a deliberate *non*-goal reversal from the first draft, which tried
to bound rehydration memory by paging it. Auditing showed paging rehydration while
activating jobs makes later pages observe the writes caused by earlier ones —
duplicate yields and, under sustained arm churn, non-termination inside
`rehydrateOnce.Do`. The memory benefit was marginal (peak startup memory is
dominated by the N live gocron jobs and their retained fire closures, which must be
resident regardless). Rehydration is therefore left exactly as it is.

## Design

### `runtime/kernel` — one additive method

```go
type TimerStore interface {
	// ListArmed returns all timers currently armed, ordered by
	// (NextRun, InstanceID, TimerID). Unchanged: rehydration reads the whole
	// set in one statement and therefore sees one consistent snapshot.
	ListArmed(ctx context.Context) ([]ArmedTimer, error)

	// ArmedTimer returns the timer armed for the exact (instanceID, timerID)
	// pair. found is false when no such timer is armed; err is reserved for
	// genuine infrastructure failures.
	ArmedTimer(ctx context.Context, instanceID, timerID string) (t ArmedTimer, found bool, err error)
}
```

`ListArmed` **stays**, on the ground that rehydration needs its single-statement
snapshot (see requirement 4). That is the whole argument.

The first draft deleted it and justified the breakage by claiming an unbounded read
would then be "inexpressible, enforced by the compiler". The claim was false, but
the counter-example an earlier revision offered — `ListArmedPage(…, math.MaxInt)` —
is itself refuted by this design's own clamping, which caps that at 200 rows. Both
the claim and that rebuttal are struck; retention rests on snapshot consistency
alone, which is the stronger argument regardless.

`ArmedTimer` is a **required** interface method rather than an opt-in capability
asserted by type. This cuts against local precedent and the reasoning should be
honest about that: the very same concrete type already exposes two type-asserted
capabilities, `kernel.TimerWriter` and `kernel.TimerStatsReader`
(`internal/persistence/store/timerstore.go:43-47`, ADR-0134), and `dialect.Notifier`
/ `dialect.Locker` follow the same pattern (ADR-0081/0082). A capability would also
be **non-breaking** for consumer stores passed to the public
`runtime.WithTimerStore` (`runtime/processdriver_options.go:160`), and the fallback
would be today's six lines with exactly one caller — so "two code paths" is a weak
objection, not the decisive one an earlier draft implied.

The decision rests instead on: one question should have one answer, recurrence is
not an optional capability of a timer store the way *writing* or *stats* are, and
pre-release breakage is cheap (v0.1.0 untagged; precedent ADR-0141/0142/0154). If
the tag existed, the capability route would be correct.

### `service.TimerAdmin` — paging, using the house conventions

The repository already has an audited paging seam in `runtime/kernel/lister.go`:
opaque base64 cursors via `EncodeCursor`/`DecodeCursor` with `ErrBadCursor`
(`:14-41`), `NormalizeLimit` clamping (`:48`), and a page struct carrying
`NextCursor` + `HasMore` (`:112-116`). This delivery reuses all four rather than
inventing a parallel vocabulary:

```go
// kernel
func EncodeArmedTimerCursor(nextRun time.Time, instanceID, timerID string) string
func DecodeArmedTimerCursor(cursor string) (nextRun time.Time, instanceID, timerID string, err error)

// ErrBadArmedTimerCursor is distinct from ErrBadCursor, whose message reads
// "malformed instance cursor" (lister.go:15) and would misreport a timer
// cursor to an operator. errors.go:36's 400 mapping is extended to it.
var ErrBadArmedTimerCursor = errors.New("workflow-runtime: malformed armed-timer cursor")

type ArmedTimerFilter struct {
	Cursor       string // empty = first page
	Limit        int    // clamped via NormalizeLimit
	IncludeTotal bool   // when false, TotalCount is 0 and no count(*) is issued
}

type ArmedTimerPage struct {
	Items      []ArmedTimer
	NextCursor string // empty when HasMore is false
	HasMore    bool
	TotalCount int64 // populated only when the filter asked for it
}

// service
type TimerAdmin interface {
	Stats(ctx context.Context) (kernel.TimerStats, error)
	ListArmedPage(ctx context.Context, filter kernel.ArmedTimerFilter) (kernel.ArmedTimerPage, error)
}
```

A **filter struct**, not positional `(cursor, limit)`: the house read-side shape is
`InstanceLister.List(ctx, InstanceFilter)`, and positional parameters would block
the obvious next filter (by `Kind`).

`IncludeTotal` mirrors `InstanceFilter.IncludeTotal` / `InstancePage.TotalCount`
(`runtime/kernel/lister.go:79-81`, `:117-120`) and exists because paging otherwise
bounds only the payload, not the database work: `AdminTimers` calls `Stats` on
**every** request (`admin_endpoints.go:334`), and `Stats` is an unbounded
`count(*)`. Defaulting it off means a paged request issues no aggregate at all.

### Cursor encoding — which layer the fixed width belongs to

The cursor is a base64 envelope in the **same form as `EncodeCursor`**
(`runtime/kernel/lister.go:26-44`). `time.Time`'s JSON encoding is RFC3339 with
nanoseconds and is lossless for the instant, so **no fixed-width requirement
applies to the cursor's own bytes**.

The fixed-width nine-digit layout is load-bearing only at the **SQL bind**, against
SQLite's TEXT `next_run` column — and that is already solved: the store passes the
decoded cursor time through `timeArg(dialect, t)` before binding, exactly as
`Lister.List` does (`internal/persistence/store/lister.go:105`).

This layering is not optional. The layout constant `textTimeLayout` is
**unexported** (`internal/persistence/store/time_codec.go:26`), and
`internal/persistence/store` imports `runtime/kernel`
(`internal/persistence/store/timerstore.go:17`) — so `kernel` cannot import
`store`. A cursor helper in `kernel` therefore *cannot* reach the layout, and
duplicating the constant would split a single serialization decision across two
packages. Encoding nine-digit text into the cursor and then binding the decoded
`time.Time` **directly** is the failure this pins down: modernc's driver
stringifies a `time.Time` in a non-ISO8601 form (`timerstore.go:28-32`), the
predicate matches nothing, and **every listing silently truncates at one page with
no error on any backend**.

### Limit clamping — order matters

Reuse `kernel.NormalizeLimit`: default 50, maximum 200 (`lister.go:46-57`). Clamp
**then** add the extra row, once, in the store — the order `Lister.List` already
uses (`internal/persistence/store/lister.go:75-76`):

```go
limit := kernel.NormalizeLimit(filter.Limit)
fetch := limit + 1 // detect HasMore
```

The reverse order is a live defect, not a style preference: `math.MaxInt + 1`
overflows to `math.MinInt`. Postgres and MySQL error on a negative `LIMIT`;
**SQLite treats it as no limit and returns the entire table**, reinstating the
unbounded read this delivery removes, on the one backend that gives no error to
reveal it.

An **opaque string** cursor rather than an exported struct is what makes the
first-page sentinel structural: `""` means "first page", and it cannot be aliased
by real data. A struct sentinel could be: `timerJobsFor` arms with a zero
`nextRun` whenever `strig.Next` returns `ok == false`
(`runtime/timerops.go:156-159`), and `rehydrateTrigger` exists precisely to
handle "a one-shot with no persisted NextRun". Such a row sorts first, so a
legitimate cursor can equal the zero value, and a design that infers "first
page" from a zero field would omit the keyset predicate and loop forever.

⚠ **Measured during implementation:** such a row is persisted on Postgres and
SQLite but **rejected by MySQL** — `DATETIME(6) NOT NULL` refuses the
`'0000-00-00'` the driver emits for a zero `time.Time` under strict mode, so
`UpsertJob` errors and the step fails. An earlier revision of this spec asserted
the row is persisted on all backends; it is not. This does not weaken the
opaque-cursor argument — a sentinel safe on only some backends is no sentinel —
and the paging matrix now asserts the MySQL rejection explicitly rather than
skipping the case. The write-path divergence itself predates this delivery and
is backlog, not scope.

`next_run` is encoded at **full precision** using the store's fixed-width
nine-digit layout, never `time.RFC3339`. The existing `FireAt` display field is
`t.NextRun.Format(time.RFC3339)` (`admin_endpoints.go:350`), which discards the
fraction; sub-second `next_run` values are normal and `TestTimerStoreFireAtSubSecond`
exists for exactly that. A cursor derived from the display layout would silently
skip or repeat every row sharing a second with the page boundary — the ADR-0151
bug class, which this project has already shipped once.

`HasMore` is returned by fetching `limit+1` rows and truncating, as the instance
lister does. **Callers branch on `HasMore`, never on `len(Items)`.**

`ListArmedPage` clamps rather than erroring, consistent with its sibling.

### `scheduler.JobStore` — untouched

`Load` keeps its `([]ScheduledJob, error)` signature. Every finding about nil
interface yields, the unconvertible-trigger error class, partially-armed
schedulers, and the latched-unretryable `rehydrateErr` is out of scope by
construction.

### Call-site behaviour

`armedTimerRecurring` becomes a point lookup: `WHERE instance_id = ? AND timer_id = ?`,
one row, one JSON decode, on the existing primary key. `MemTimerStore` becomes a
single map lookup. It reads through **`s.querier()`** — the pool handle, matching
`ListArmed` — not `transaction.JoinOrBegin` as the neighbouring *write* methods do.
That is verified safe rather than assumed: the closure is built at
`runtime/processdriver.go:691-693` and invoked from `timerJobsFor` **before** the
commit transaction opens, so there is no ambient-transaction visibility change and
no SQLite single-connection deadlock.

`found == false` continues to yield "not recurring" — a genuinely one-shot or
unknown timer is consumed, exactly as today.

### A store error is "undeterminable", not "not recurring"

`ArmedTimer(ctx, …) (ArmedTimer, bool, error)` distinguishes *"no such timer"* from
*"the store could not answer"* for the first time; `ListArmed` could not, because a
scan error and an absent timer were indistinguishable at the call site. This design
uses that distinction rather than discarding it.

On `err != nil` the fired timer is **left alone**, matching `timerJobsFor`'s existing
nil-closure branch, which already means precisely "recurrence undeterminable, do not
touch it" (`runtime/timerops.go:126-131` doc, `:161-163` code). Collapsing an error
to `false` would mean `false` ⇒ cancel ⇒ a single connection blip **permanently
disarms** a recurring job — an in-wait reminder loop, for instance. The cost of the
alternative is bounded and small: a one-shot's durable row survives, causing at most
one duplicate fire at rehydration, which is already an idempotent engine no-op.

Note also that a point lookup is *less* likely to hit a statement timeout than the
unfiltered scan it replaces, so transient-error-driven behaviour becomes rarer, not
more common.

A nil `armedRecurring` closure is unchanged. The `driver.timerStore == nil` guard
inside `armedTimerRecurring` (`runtime/timerops.go:176-178`) is **unreachable in
production** — `processdriver.go:692` only builds the closure when `timerStore != nil`
— so it is testable only through the unexported method; the test plan says so rather
than pretending it is a production branch.

### The one behaviour delta

The first draft claimed the fire path change was "byte-identical". It is not, and
the difference is worth recording rather than discovering later.

`ListArmed` aborts on the **first** scan failure and returns `nil, err`
(`internal/persistence/store/timerstore.go:93-99`); `scanArmedTimer` fails on an
unparseable `next_run` or a malformed `trigger_payload` for **any** row
(`:290-293`, `:304-307`). So today **one corrupt row anywhere** in `wrkflw_timers`
makes `armedTimerRecurring` return `false` for *every* fire, and `timerJobsFor`
then **cancels** the fired timer — including genuinely recurring ones.

After the point lookup, an unrelated corrupt row is invisible: a recurring timer
returns `true` and is correctly not cancelled. This is a strict improvement and it
is **accepted deliberately**, with a test asserting the new behaviour in the
presence of a corrupt sibling row.

## Schema

The point lookup rides `PRIMARY KEY (instance_id, timer_id)`, declared on all three
backends (`migrations/postgres/0001_init.sql:109`, `mysql:101`, `sqlite:108`). It
needs no new index.

The admin keyset read does. The only secondary index is
`wrkflw_timers_next_run_idx (next_run)` — single-column.

### The index change edits `0001_init.sql` in place

There is **no `0002_*.sql`**. `TestMigrations_OneFilePerDialect`
(`internal/persistence/store/migrations_count_test.go`) asserts exactly one
migration file per dialect, with the comment that adding a second file
"reintroduc[es] the incremental-migration style the engine deliberately squashed
while pre-release — fails here". ADR-0132 records the same rule as policy. A second
file would also flip the goose head version 1 → 2, breaking the version-coupled
migrator tests.

ADR-0132's own rationale applies: the library is pre-release and no consumer
database exists, so there is no `goose_db_version` history to preserve. The
`CREATE INDEX` line in each `0001_init.sql` is therefore edited in place:

```sql
CREATE INDEX wrkflw_timers_keyset_idx ON wrkflw_timers (next_run, instance_id, timer_id);
```

replacing `CREATE INDEX wrkflw_timers_next_run_idx ON wrkflw_timers (next_run);`.
One file per dialect is preserved, head version stays 1, and the `-- +goose Down`
sections already present need no change since they drop the whole table.

`Stats`' `MIN(next_run)` and the `ListArmed` ordering are both still served, by the
composite's leading column.

### Per-dialect index necessity

Postgres and SQLite genuinely need the composite. **MySQL does not**: InnoDB
appends the primary key to every secondary index, so `wrkflw_timers_next_run_idx`
is already physically `(next_run, instance_id, timer_id)`. The MySQL edit is made
anyway, for cross-dialect uniformity and so the parity assertion stays a simple
name comparison; it is a no-op in physical terms and is documented as such.

### MySQL index key length

MySQL reports `key_len: 2052` for the composite. The arithmetic: DATETIME(6) is
8 bytes, and each utf8mb4 `VARCHAR(255)` key part is 1020 bytes **plus a 2-byte
length prefix** — so 8 + 1022 + 1022 = 2052, not the 2048 an earlier draft stated.
Either way it is far inside InnoDB's 3072-byte limit for the default DYNAMIC row
format **at the default `innodb_page_size=16384`** (at 8KB the limit would be 1536
and this would fail).

The first draft called the existing PK "the decisive evidence, not the arithmetic".
That was circular — the PK bounds the limit only *given* utf8mb4, and given utf8mb4
the arithmetic already suffices. The real reason no length risk exists is the
preceding paragraph: on MySQL this key shape is already in production as the
secondary index's physical form.

**Postgres deserves the analysis MySQL got.** Its btree key limit (~2704 bytes) is
*tighter* than InnoDB's 3072, and `instance_id`/`timer_id` are unbounded `TEXT`
(`migrations/postgres/0001_init.sql:101-102`) rather than length-capped. The new
index is the existing PK plus 8 bytes, so a row within 8 bytes of the PK's headroom
would begin failing `INSERT` after this change. Practically unreachable with
engine-minted ids, but it is the one dialect where the new index exceeds an existing
constraint, and the bundle should not spend two sections on MySQL and none here.

### MySQL collation

`wrkflw_timers` inherits `utf8mb4_0900_ai_ci` — case- and accent-insensitive —
which would normally break the strict total order a keyset predicate needs. It does
not here, because `PRIMARY KEY (instance_id, timer_id)` is enforced under the *same*
collation, so two collation-equal rows cannot coexist: inserting `('A','TM1')`
beside `('a','tm1')` fails with a duplicate-key error. The triple is therefore
strictly ordered on MySQL.

Cross-dialect order *does* diverge (MySQL `ai_ci`, Postgres's DB collation, SQLite
`BINARY`), so shared-matrix paging fixtures are pinned to a single case class.

## The keyset predicate is a dialect concern

The plan must not hand-roll a row-value comparison. `internal/persistence/dialect`
already owns this construct — `KeysetCursorPredicate()` / `KeysetCursorArgCount()`
(`dialect/dialect.go:142`, `:147`) — with Postgres using row values and arg count 2
(`postgres.go:121`) and MySQL and SQLite using an expanded OR decomposition with
arg count 3 (`mysql.go:119`, `sqlite.go:140`).

That existing pair is two-column and cannot serve a three-column ordering, so a
sibling capability is added for the armed-timer triple:

```go
// Dialect
ArmedTimerKeysetPredicate() string
ArmedTimerKeysetArgs(nextRun any, instanceID, timerID string) []any
```

The capability returns the **args slice**, not an arg *count*. The existing
`KeysetCursorArgCount()` works because 2-vs-3 uniquely determines whether the
cursor time is bound once or twice. That does not generalise: the row-value form
binds 3 values `(nextRun, instanceID, timerID)` while the expanded form binds 5
`(nextRun, nextRun, instanceID, instanceID, timerID)`, so a count would force the
caller into an `if argCount == 5` magic-number branch — worse than the
`dialect.Name()` comparison the rules forbid. Returning the args makes predicate
and bindings unable to drift.

### Per-dialect shape, set by measurement

| dialect | shape | measured plan |
|---|---|---|
| Postgres 17 | row value | `Index Scan using wrkflw_timers_keyset_idx` + `Index Cond: (ROW(next_run, instance_id, timer_id) > ROW(...))` |
| SQLite (modernc v1.53.0) | row value | one `SEARCH wrkflw_timers USING INDEX wrkflw_timers_keyset_idx ((next_run,instance_id,timer_id)>(?,?,?))` |
| MySQL 8.0 | expanded lexicographic OR | `type: range`, `key: wrkflw_timers_keyset_idx`, `Extra: Using index condition`, `Handler_read_next` = pageSize |

**Neither `Index Only Scan` nor `USING COVERING INDEX` is achievable for this query
and neither must be asserted.** `ListArmed`'s projection selects `def_id`,
`def_version`, `kind` and `trigger_payload`
(`internal/persistence/store/timerstore.go:83-84`), none of which the index holds, so
no plan can be index-only. An earlier draft of this spec carried those strings after
inheriting them from an audit that had probed a keys-only `SELECT`; they were never
reproducible from the shipped statement.

On **MySQL** a row constructor is not treated as an index range condition: measured
`type: index`, `possible_keys: NULL`, and `Handler_read_next` proportional to cursor
depth (≈21,650 for a mid-table cursor at 50k rows, tending to N at the last page)
versus pageSize for the expanded form. So a row-value predicate there makes paging
O(N²/pageSize) — slower than the scan this delivery removes.

On **SQLite** the converse holds, and the divergence is therefore justified rather
than cosmetic. Measured directly: with fully distinct `next_run` values the expanded
form produces **two** `SEARCH` operations — `(next_run>?)` and `(next_run=?)`, an OR
decomposition — while with duplicate-heavy values it produces a flat
`SCAN … USING INDEX`. Row value produces one seek on the full triple in both
fixtures. Row value is thus the only shape whose SQLite plan is both optimal and
stable across data distributions.

Selection is by dialect capability method, **never** by comparing `dialect.Name()`
— the rule the codebase already applies to `TimestampsAsText()`.

### The SQLite row-value correctness question

`internal/persistence/dialect/sqlite.go:133-135` states that "SQLite does not
guarantee correct row-value comparison semantics for mixed-type columns, so the
predicate uses the same explicit OR decomposition as MySQL". That rationale does not
transfer to this triple: `next_run`, `instance_id` and `timer_id` are all
TEXT-affinity columns (`migrations/sqlite/0001_init.sql:100-102`) and all three binds
are strings, so no mixed-type comparison arises. The new capability's doc comment
must say this explicitly — an implementer reading the adjacent existing comment has
no other way to know the divergence is deliberate and safe.

> Backlog, out of scope: whether the *existing* two-column predicate should also use
> row values on SQLite. It is governed by the mixed-type caveat above (`started_at`
> vs `instance_id`) and needs its own measurement and ADR.

## Blast radius

Production:

- `runtime/kernel/timerstore.go` — `ArmedTimer` on the port and on `MemTimerStore`.
- `runtime/kernel/lister.go` (or a sibling) — `EncodeArmedTimerCursor`, `DecodeArmedTimerCursor`, `ArmedTimerPage`.
- `internal/persistence/store/timerstore.go` — `ArmedTimer`, `ListArmedPage`.
- `internal/persistence/dialect/{dialect,postgres,mysql,sqlite}.go` — the three-column keyset capability.
- `internal/persistence/store/migrations/{postgres,mysql,sqlite}/0001_init.sql` — edited in place.
- `runtime/timerops.go` — `armedTimerRecurring`.
- `service/opsadmin.go` + `service/opsadmin_mock.go` (regenerated).
- `transport/http/httpcore/admin_endpoints.go` + `dto.go` — `AdminTimers` currently takes no query argument (`admin_endpoints.go:333`), so it gains one plus a query DTO.
- `transport/http/{stdlib,gin,fiber}/groups.go` — only the **closure bodies** at `stdlib:404`, `fiber:410`, `gin:416`. These take `Timers service.TimerAdmin` as a Deps **struct field** (`stdlib:197`, `fiber:217`, `gin:223`), so the exported factory signatures and Deps structs do **not** change. The real breaks are `httpcore.AdminTimers`'s signature and the HTTP query surface, both already counted — an earlier draft overstated this as a breaking route-group change.

Stale doc comments — exactly **two**: `service/opsadmin.go:33-35` ("`MemTimerStore`
implements only `ListArmed`") and `runtime/timerops.go:174` ("the `ListArmed` read
stays off the hot path"), plus the WARN text at `runtime/timerops.go:181` ("list
armed failed"), which must be reworded rather than kept.

The `persistence/persistence.go:417,425`, `persistence/sqlite.go:142` and
`persistence/mysql.go:112` snippets are **still correct** and need no change:
`ListArmed` is retained. An earlier draft listed them because it deleted the method.

Implementors of `kernel.TimerStore` — the complete set, verified by searching for
the method rather than for callers: `kernel.MemTimerStore`, `store.TimerStore`, and
`faultTimerWriter` (`runtime/timer_txflow_test.go:71`, a pure delegator over
`inner *store.TimerStore`, so `ArmedTimer` is a one-line delegation — its fault
injection is confined to `UpsertJob`/`DeleteJob`). Nothing in `examples/`,
`processtest/`, or `runtime/internal/runtimetest` implements it.

Files that merely **call** `ListArmed` compile unchanged and are *not* in scope:
`internal/persistence/store/timerwriter_test.go`, `runtime/jobstore_internal_test.go`,
`runtime/jobstore_test.go`, `runtime/rehydrate_durable_test.go`,
`persistence/facade_{constructors,mysql,sqlite}_test.go`.

Tests that do change: `internal/persistence/store/timerstore_conformance_test.go`
(the real home of the timer-store coverage, driven by `forEachDialect`; **there is
no `timerstore_test.go`**), `runtime/kernel/timerstore_test.go`, and
`MockTimerAdmin` expectations in **four** transport packages:
`httpcore/admin_endpoints_test.go`, `gin/{gin_admin_test.go,gin_admin_errors_test.go}`,
`fiber/fiber_test.go`, `stdlib/{errors_test.go,coverage_test.go}`.

`persistence/persistence.go:183,426`, `persistence/sqlite.go:143`,
`persistence/mysql.go` and `persistence/durableprovider.go:26,34` traffic in
`kernel.TimerStore` as an **interface value** — `persistence.NewTimerStore` returns
the interface — so widening the method set narrows what a consumer may supply.
Those compile-time assertion sites need verifying, not editing.

With `IncludeTotal` defaulting off, the admin response's `count` is 0 unless
requested, so handler tests asserting `count == len(items)` change accordingly.

## Testing

TDD strict, RED first with an observable failing state per new symbol.
Hot-path-first per Golang rule #8.

1. **`armedTimerRecurring`**: found+recurring, found+non-recurring, not-found,
   **store error → timer left alone** (the new third state), and the
   production-unreachable nil-store guard via the unexported method.
2. **The behaviour delta**: a corrupt sibling row must no longer cancel a genuinely
   recurring fired timer. The corruption vector is **dialect-specific** and must be
   named, not left to the implementer: the malformed-`next_run` route
   (`timerstore.go:290-293`) exists **only on SQLite**, where `next_run` is TEXT;
   the malformed-`trigger_payload` route needs bytes the database accepts as JSON
   (Postgres `JSONB` and MySQL `JSON` reject invalid JSON outright) but which fail
   to decode into `model.TriggerWire`. The delta is therefore *not* reachable with
   `MemTimerStore`, so it is split: a store-conformance test for the scan-error
   behaviour, plus a driver-level test with a fake whose `ListArmed` errors while
   `ArmedTimer` succeeds.
3. **Keyset paging**: page boundaries, ties on `next_run`, `HasMore` at the exact
   boundary, and a row with `next_run == time.Time{}` (sorts first, must page and
   terminate).
4. **Cursor round-trip fidelity**, at the HTTP layer as well as the store. Assert
   the cursor round-trips the value **as read back from the store**, never against
   the fixture: Postgres `TIMESTAMPTZ` and MySQL `DATETIME(6)` round to
   microseconds, so a `…123456789Z` fixture returns `…123457Z` and a
   cursor-vs-fixture assertion fails on two of three backends. Worse at boundaries —
   `…999999500Z` rounds *up* on MySQL into the next second, moving the row relative
   to its siblings and diverging the tie and `HasMore` expectations per dialect.
   All timestamp fixtures are therefore `Truncate(time.Millisecond)`.
   A malformed cursor is a 400 via `ErrBadArmedTimerCursor`.
5. **Per-dialect proof that a page is a seek, not a scan.** Without this the MySQL
   regression ships green — plan Risk 1. The mechanics matter:
   - MySQL: `Handler_read_*` are **session**-scoped, but `dbtest.RunTestMySQL`
     returns an 8-connection pool (`internal/dbtest/mysql.go:122-148`), so
     `FLUSH STATUS`, the query, and `SHOW STATUS` can land on three different
     connections — the naive test reads `0` and **passes regardless of predicate
     shape**. Pin a single `*sql.Conn` (or `SetMaxOpenConns(1)`) and build the store
     over it. Mark the test non-parallel: `forEachDialect` uses `t.Parallel()` against
     a shared server, and a global `FLUSH STATUS` is corrupted by concurrent tests.
   - The primary assertion is `EXPLAIN`: `type='range'` **and**
     `key='wrkflw_timers_keyset_idx'`. `Handler_read_next <= 4*limit` is the volume
     backstop. **Not `<= 1`** — a 50-row page reads 50 rows in key order, so the
     correct implementation reports ~pageSize; an earlier draft's "versus 1" figure
     would have failed the *correct* shape.
   - Fixture: ≥10k rows with the cursor near the **end**. With the cursor at the
     start, even the row-value form reports a small row count and the regression is
     undetectable.
   - Postgres: ≥10k rows and `ANALYZE` first (or `SET LOCAL enable_seqscan = off`) —
     a small fixture is seq-scanned and no index node appears. Assert
     `Index Cond: (ROW(` and the index name; never `Index Only Scan`.
   - SQLite: **`EXPLAIN QUERY PLAN`**, not `EXPLAIN` — plain `EXPLAIN` returns VDBE
     bytecode rows, not plan text. Assert `USING INDEX wrkflw_timers_keyset_idx`.
     Stable at small row counts with no `ANALYZE`.
   - The query text is unexported, so expose the built SQL through `export_test.go`
     rather than hand-copying it into the test, where it would drift.
6. **`limit` clamping** at and above the bound, including `math.MaxInt` — which must
   yield ≤200 rows, not a negative `LIMIT`.
7. **Mem-vs-SQL parity for `ArmedTimer` only.** `MemTimerStore` has no paging: this
   design adds only `ArmedTimer` to it, and it is deliberately not a `TimerAdmin`
   (no `Stats` — `service/opsadmin.go:33-35`). There is nothing on the mem side to
   page against.
8. **The invariant between the two coexisting reads**, stated in the `TimerStore`
   doc comment and asserted: *`ArmedTimer(i,t)` returns exactly the row `ListArmed`
   would return for `(i,t)`, when that row itself is well-formed.* The qualifier is
   the accepted delta — a row `ListArmed` refuses to return because a *sibling* is
   corrupt, `ArmedTimer` returns.
9. **Index parity**: explicitly-named, non-implicit indexes only.
10. **A before/after benchmark** for the fire path (rule #12).

All three backends via `internal/dbtest`. No ad-hoc containers.

## Out of scope

- `scheduler.JobStore` and rehydration behaviour — unchanged.
- Bounding rehydration memory (former requirement 3) — withdrawn; see Requirements.
- `Stats`' `count(*)`, which remains an O(N) aggregate.
- The existing two-column SQLite keyset predicate.
- The parked signal & message delivery bundle.

## Audit corrections folded into this revision

Deleting `ListArmed` and its false compiler-enforcement justification · streaming
`Load` (withdrawn entirely, with its duplicate-yield, non-termination,
nil-interface, unconvertible-trigger and unretryable-partial-rehydration findings)
· `0002_*.sql` → edit `0001_init.sql` in place · row-value predicate → dialect
capability with per-dialect measured shapes · struct cursor → opaque string,
fixing the zero-`next_run` aliasing loop · invented paging vocabulary → the
existing `EncodeCursor`/`NormalizeLimit`/`HasMore` seam · "byte-identical parity"
→ the corrupt-row delta recorded and tested · "short page is not end-of-data"
→ `HasMore` · draft-1 requirement 2 ("no unbounded read expressible") withdrawn —
**not** the current requirement 2, which is the admin paging this bundle implements
(`Stats`' `count(*)` made the draft-1 version unsatisfiable) · workload
attribution corrected to N-dominant · MySQL key-length reasoning de-circularised
and the InnoDB PK-append mechanism recorded · collation question closed by the PK
· blast radius corrected (dialect package, four transport packages,
`timerstore_conformance_test.go` instead of a file that does not exist, the third
test double, `migrations_count_test.go`) · index parity re-scoped to
explicitly-named indexes · seek-not-scan assertions added.

Two auditor findings were **rejected**:

- One auditor claimed `N²/P` requires all timers to be recurring and so does not
  apply to one-shot deadlines. It does apply: by Little's law a steady-state
  population N with mean lifetime P fires at N/P. The other auditor said so
  correctly. Formula kept.
- The same auditor's conclusion that short-period recurring timers are "the
  genuinely worst workload, not long-lived deadlines" overreaches: its two
  examples vary N *and* P simultaneously, and its own figures make the large-N
  deadline case worse in absolute terms (≈38.6k vs ≈16.7k rows/s). The correct
  statement, now in the spec, is that N dominates.

## Round-2 audit corrections

A second pair of Opus auditors reviewed the narrowed bundle. The core direction was
independently confirmed by both — point lookup on the PK, `ListArmed` retained for
the snapshot, MySQL on the expanded form, in-place `0001_init.sql` edit — but the
*mechanics* had three blockers, now folded:

- **The cursor encoding was unimplementable as specified.** `textTimeLayout` is
  unexported and `store` imports `kernel`, so `kernel` cannot reach it. Fixed-width
  now correctly belongs at the SQL bind via `timeArg`; the cursor is a plain lossless
  base64 envelope. The plausible misreading silently truncated every listing to one
  page on SQLite.
- **The `EXPLAIN` evidence strings were unreproducible.** `Index Only Scan` and
  `USING COVERING INDEX` cannot occur for a projection that selects
  `trigger_payload`. They entered this spec by inheriting a round-1 measurement taken
  from a keys-only probe — a reminder that an auditor's measured claim needs the same
  verification as one's own.
- **T3's MySQL measurement could not work** on the 8-connection pool `dbtest`
  returns, and the "`Handler_read_next` versus 1" figure would have failed the
  *correct* implementation.

Also folded: `math.MaxInt + 1` overflowing to a negative `LIMIT` (SQLite reads it as
*no limit*, restoring the unbounded read) · clamp-then-increment order and the actual
`NormalizeLimit` bounds · a filter struct instead of positional parameters ·
`IncludeTotal`, so paging stops issuing `count(*)` on every request · store error
promoted to "undeterminable" rather than "not recurring" · `ErrBadArmedTimerCursor` ·
the dialect capability named and returning args rather than a count · µs-vs-ns
rounding, so cursor fidelity is asserted against the read-back row not the fixture ·
T5 scoped to `ArmedTimer` (`MemTimerStore` has no paging) · the corrupt-row test
re-homed with its dialect-specific vector named · the blast radius corrected (four
listed files need no change now that `ListArmed` stays; the `persistence` doc
snippets are still valid) · the overstated "breaking route-group factories" claim ·
`key_len` 2052 not 2048 · Postgres's tighter btree limit analysed · the in-place
edit's staleness against already-migrated local databases · the "required method vs
capability" argument re-grounded after it was shown to argue against a strawman ·
`ErrBadCursor`'s instance-specific message · citation drift.

**One round-2 blocker was rejected on my own measurement.** An auditor found that the
expanded lexicographic OR seeks correctly on all three dialects and concluded the
per-dialect divergence, the row-value branch and the new capability should all
dissolve into reusing the existing two-column shape. Measured directly (modernc
SQLite v1.53.0, 5k rows, `ANALYZE`): with distinct `next_run` values the expanded form
produces **two** `SEARCH` operations, and with duplicate-heavy values a flat
`SCAN … USING INDEX`, while row value produces one seek on the full triple in both
fixtures. Round 1's "`MULTI-INDEX OR` plus a temp B-tree" was right in substance —
OR decomposition into multiple index searches — though its literal plan string did
not reproduce either. Row value is the only shape whose SQLite plan is both optimal
and stable, so the divergence stands.

## Verification

```bash
go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out
go test ./...
golangci-lint run ./...
```

85% floor on touched packages excluding generated files (ADR-0143) — a floor, not
a target. Postgres and MySQL need Docker. Then `/code-review` and
`/security-review`, all findings fixed and folded via `--amend`.

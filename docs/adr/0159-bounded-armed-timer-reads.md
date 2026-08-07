# 159. Bounded armed-timer reads

- Status: Accepted
- Date: 2026-07-30

> ADR numbers 0155–0158 are reserved by the parked
> `feat/durable-waiters-delivery-correctness` branch and do not exist on `main`.
> This ADR takes 0159 so either branch can land first without a renumber.

## Context

`ProcessDriver.armedTimerRecurring` (`runtime/timerops.go:175-194`) answers one
boolean about one timer — *is the fired timer armed with a recurring trigger?* —
and to do it reads the **entire** armed-timer table via
`kernel.TimerStore.ListArmed`, then linear-searches the result. `ListArmed` is
unfiltered on the SQL backend: no `WHERE`, no `LIMIT`
(`internal/persistence/store/timerstore.go:83-86`).

It runs on every `TimerFired` step (`runtime/timerops.go:158-165`, closure at
`runtime/processdriver.go:691-694`), and each row's `trigger_payload` is
JSON-unmarshalled and passed through `model.ReadTrigger` during the scan
(`internal/persistence/store/timerstore.go:304`, `325-334`). Per fire: O(N) rows
and O(N) trigger decodes, to produce one bit.

In steady state — a stable population of N armed timers with mean lifetime P — the
fire rate is N/P, so the aggregate is **N²/P rows scanned per second**. By Little's
law this holds for one-shot timers as well as recurring ones. **N dominates**; a
long lifetime means a large P, which divides the rate.

The in-memory store degrades on the same axis, needlessly: `MemTimerStore.armed`
is already `map[timerKey]ArmedTimer` keyed by exactly `(instanceID, timerID)`
(`runtime/kernel/timerstore.go:65-70`), yet `ListArmed` copies all N entries out
under `s.mu` and sorts them (`:92-105`) on every fire.

Separately, `AdminTimers` (`transport/http/httpcore/admin_endpoints.go:333-359`)
returns every armed timer in one unpaged JSON body.

**A first design for this change was rejected by its own audit**, and the reasons
shape this decision. That draft deleted `ListArmed`, streamed
`scheduler.JobStore.Load` as `iter.Seq2` to bound rehydration memory, and added a
`0002_*.sql` migration. Two independent Opus audits returned "do not implement":

- The justification for deleting `ListArmed` — that an unbounded read would then be
  "inexpressible, enforced by the compiler" — was **false**. (The `math.MaxInt`
  counter-example first offered against it was itself wrong, since this design
  clamps to 200; the claim fails simply because no such requirement is needed.)
- Streaming `Load` while activating each job made rehydration read-your-own-writes:
  a fired recurring timer re-armed at a later `next_run` is yielded twice, and under
  sustained arm churn the paged read never sees an empty page, so rehydration never
  terminates — inside `rehydrateOnce.Do`, blocking startup and every concurrent arm.
- A `0002_*.sql` file is forbidden by `TestMigrations_OneFilePerDialect` and by
  ADR-0132.
- The row-value keyset predicate was **measured** to degrade on MySQL: for a
  50-row page, `Handler_read_next` proportional to cursor depth (≈21,650 at a
  mid-table cursor over 50k rows, tending to N at the last page) versus ~pageSize
  for the expanded form. A later revision also had to correct its own restatement
  of this as "50,000 versus 1" — a 50-row page necessarily reads ~50 rows in key
  order, so a test asserting `<= 1` would fail the *correct* implementation.

## Decision

**Scope is the fire path plus admin paging. `scheduler.JobStore` is untouched and
rehydration is unchanged.**

**Add one method to `kernel.TimerStore` and keep `ListArmed`:**

```go
type TimerStore interface {
	ListArmed(ctx context.Context) ([]ArmedTimer, error)   // unchanged
	ArmedTimer(ctx context.Context, instanceID, timerID string) (ArmedTimer, bool, error)
}
```

`ListArmed` is retained deliberately. Rehydration legitimately wants the whole set,
it is a once-per-boot cost, and reading it in one statement gives it **one
consistent snapshot** — the property whose loss made the streaming design unsafe.
Retaining it is now a stated requirement, not an omission.

`ArmedTimer` is a **required** method rather than a capability asserted by type.
This cuts against local precedent, and the reasoning should say so: the same
concrete type already exposes `kernel.TimerWriter` and `kernel.TimerStatsReader`
by type-assertion (`internal/persistence/store/timerstore.go:43-47`, ADR-0134), as
do `dialect.Notifier`/`Locker` (ADR-0081/0082), and a capability would be
**non-breaking** for consumer stores passed to `runtime.WithTimerStore`. The
decision rests not on "two code paths" — the fallback would be six lines with one
caller — but on: recurrence is not an optional capability of a timer store the way
writing or stats are, and pre-release breakage is cheap. With a tag in place, the
capability route would be correct.

**Page the admin listing using the seam the repository already has.**
`runtime/kernel/lister.go` provides opaque base64 cursors with `ErrBadCursor`
(`:14-41`), `NormalizeLimit` clamping (`:48`), and a page struct carrying
`NextCursor` + `HasMore` (`:112-116`). This adds `EncodeArmedTimerCursor` /
`DecodeArmedTimerCursor` / `ArmedTimerFilter` / `ArmedTimerPage` in that idiom and changes
`service.TimerAdmin.ListArmed` to `ListArmedPage(ctx, kernel.ArmedTimerFilter)` — a
filter struct, matching `InstanceLister.List(ctx, InstanceFilter)`, so a later
filter (by `Kind`) does not break the signature again.

The filter carries `IncludeTotal`, mirroring `InstanceFilter.IncludeTotal` /
`InstancePage.TotalCount` (`lister.go:79-81`, `:117-120`), and it defaults **off**.
Without it, paging would bound the payload but not the database work: `AdminTimers`
calls `Stats` on every request (`admin_endpoints.go:334`) and `Stats` is an
unbounded `count(*)`. `limit` is clamped through `kernel.NormalizeLimit` (default
50, max 200), clamped **before** the `limit+1` HasMore probe — the order matters,
because `math.MaxInt + 1` overflows to a negative `LIMIT`, which SQLite treats as
*no limit* and would silently restore the unbounded read.

The cursor is an **opaque string**, which makes the first-page sentinel structural.
An exported struct could not: the engine arms with a zero `nextRun` when
`strig.Next` returns `ok == false` (`runtime/timerops.go:156-159`), and such a
row sorts first, so a design inferring "first page" from a zero field would omit
the predicate and loop forever.

**Correction, measured during implementation.** An earlier revision of this ADR
said a zero `next_run` "is genuinely persisted", full stop. That is true on
Postgres and SQLite and **false on MySQL**: `go-sql-driver` serialises the zero
`time.Time` as `'0000-00-00'`, which `DATETIME(6) NOT NULL` rejects under MySQL
8's default strict mode, so `UpsertJob` fails at write time with
`Error 1292 (22007): Incorrect datetime value`. The argument for the opaque
cursor is unaffected and arguably strengthened — a sentinel that is only safe on
some backends is not a sentinel — but the premise is now stated accurately, and
the paging test asserts the MySQL rejection rather than pretending the row
exists there. The underlying write-path divergence (a timer whose trigger cannot
compute a next run cannot be armed at all on MySQL, and the error fails the
step) **predates this ADR and is out of its scope**; it is recorded as backlog,
since deciding between rejecting and normalising such an arm is its own
decision.

**Correction, source-verified 2026-08-07.** Every revision of this ADR up to and
including the pushed one named the cursor seam `EncodeArmedCursor` /
`DecodeArmedCursor` / `ErrBadArmedCursor`. **No such symbols exist.** What shipped
is `EncodeArmedTimerCursor` / `DecodeArmedTimerCursor` / `ErrBadArmedTimerCursor`
in `runtime/kernel/armed_timer_paging.go` — the `Timer` infix was added during
implementation to keep the three names parallel with `ArmedTimerFilter` /
`ArmedTimerPage`, and the file itself was renamed from `armed_timer_lister.go`
(it holds no lister). The names above are corrected in place throughout this ADR,
its spec and its plan; only this note records the original spelling. Nothing about
the decision changes — the defect was purely that a reader grepping for the ADR's
own symbols found nothing.

The cursor body is the same base64 envelope as `EncodeCursor`; `time.Time`'s JSON
form is lossless, so no fixed-width requirement applies to the cursor's own bytes.
The fixed-width nine-digit layout is load-bearing only at the **SQL bind** against
SQLite's TEXT column, and is satisfied by passing the decoded time through the
existing `timeArg`, exactly as `Lister.List` does
(`internal/persistence/store/lister.go:105`). This layering is forced, not
preferred: `textTimeLayout` is unexported
(`internal/persistence/store/time_codec.go:26`) and `store` imports `kernel`
(`timerstore.go:17`), so `kernel` cannot reach it. The display layout `FireAt` uses
(`admin_endpoints.go:350`) must never drive the cursor — its discarded fraction
would skip or repeat rows sharing a second with the boundary, the ADR-0151 bug
class.

**The keyset predicate is a dialect capability, with per-dialect shapes chosen by
measurement.** `internal/persistence/dialect` already owns this construct
(`KeysetCursorPredicate`/`KeysetCursorArgCount`, `dialect.go:142`, `:147`); that
pair is two-column, so a three-column sibling is added. It is named
`ArmedTimerKeysetPredicate()` / `ArmedTimerKeysetArgs(...)` and returns the **args
slice**, not an arg count. **Correction, found in review:** the reason first given
here — that "3-vs-5 defeats an arg-count return" — is **false against the code**.
The existing two-column pair already encodes a 2-vs-3 split as a count and
`Lister.List` branches on it (`internal/persistence/store/lister.go:110`, `:123`);
3-vs-5 would work identically. The real justification is simply that returning the
slice is the better API — it deletes the caller-side branch entirely. Which means
the honest conclusion is not "these two pairs must differ" but **the new pair
demonstrates the old one should be migrated too**; `KeysetCursorArgCount` has
exactly two call sites and converting it is roughly ten lines. That is left as
backlog rather than folded in here, because instance listing is out of this
delivery's scope. The original phrasing follows for the record: row value binds 3
values, the expanded form binds 5, and
a count would force the caller into an `if argCount == 5` branch — worse than the
`dialect.Name()` comparison the rules forbid.

Row value on Postgres 17 and SQLite; expanded lexicographic OR on MySQL 8.0, where
a row constructor is not treated as a range condition. The SQLite choice was
re-measured directly after two audits disagreed about it: with distinct `next_run`
values the expanded form produces **two** `SEARCH` operations (an OR decomposition)
and with duplicate-heavy values a flat `SCAN … USING INDEX`, while row value
produces one seek on the full triple in both cases. Neither `Index Only Scan` nor
`USING COVERING INDEX` is achievable for this query — the projection selects
`trigger_payload` and three other non-indexed columns — so neither may be asserted.
Selection is by capability method, **never** by comparing `dialect.Name()`.

**The index change edits `0001_init.sql` in place** in all three dialects,
replacing `wrkflw_timers_next_run_idx (next_run)` with
`wrkflw_timers_keyset_idx (next_run, instance_id, timer_id)`. ADR-0132's own
rationale applies — pre-release, no consumer database, no `goose_db_version`
history to preserve — so one file per dialect and head version 1 are both
preserved.

**One behaviour delta is accepted and recorded rather than denied.** The first
draft called this change "byte-identical"; it is not. `ListArmed` aborts on the
first scan failure (`internal/persistence/store/timerstore.go:93-99`), so today
**one corrupt row anywhere** makes `armedTimerRecurring` return `false` for *every*
fire, and `timerJobsFor` then **cancels** the fired timer — including recurring
ones that should survive. After the point lookup an unrelated corrupt row is
invisible and the recurring timer is correctly not cancelled. This is a strict
improvement, accepted deliberately, with a test asserting it.

**A store error becomes a third state, not "not recurring".** `ArmedTimer`'s
`(value, found, error)` shape distinguishes *"no such timer"* from *"the store could
not answer"* for the first time — `ListArmed` could not — and this design uses that
rather than discarding it. On `err != nil` the fired timer is **left alone**,
matching `timerJobsFor`'s existing nil-closure branch, which already means exactly
"recurrence undeterminable, do not touch it" (`runtime/timerops.go:126-131`,
`:161-163`). Collapsing an error to `false` means cancel, so a single connection
blip would **permanently disarm** a recurring job such as an in-wait reminder loop.
A point lookup is also less timeout-prone than the scan it replaces, so this path
gets rarer, not commoner.

**The residual cost, stated properly.** An earlier revision of this ADR said the
alternative costs "at most one duplicate fire at rehydration, already an idempotent
no-op". That understates it, and review caught the gap. Because no `cancelKey` is
emitted, a fired **one-shot** timer's `wrkflw_timers` row is not deleted, while its
native scheduler job has already self-consumed — the row is orphaned. Usually it
self-heals: the row is re-armed at next boot, fires again, and that fire finds the
row and cancels it. But `timerFireFunc` drops a fire whose `applyTrigger` fails for
any non-CAS reason — including the instance having since completed and been pruned.
In that case the row is re-armed on **every** subsequent boot and shows up
permanently in `GET /admin/timers`. It is reclaimed only by
`Pruner.PruneTimers`, whose `next_run < cutoff AND trigger_kind IN (Unset, OneTime,
Expr)` predicate covers exactly these rows — but that retention job is **optional**,
so a consumer who never wires it accumulates orphans. The trade is still right (a
blip must not permanently disarm a recurring job), but `PruneTimers` is the required
mitigation, not a nicety, and is documented as such rather than left implicit.

`found == false` still yields "not recurring" (a genuinely one-shot or unknown timer
is consumed), and a nil `armedRecurring` closure is unchanged.

## Consequences

**Positive.**

- The fire path reads one row and decodes one trigger payload instead of N. The
  N²/P aggregate becomes N/P.
- `MemTimerStore` answers from the map it already keys correctly, dropping a
  per-fire allocation and O(N log N) sort. Embedded and SQLite deployments benefit
  with no database involved.
- Rehydration keeps its single-statement snapshot, so the duplicate-yield,
  non-termination, partial-arm and unretryable-`rehydrateErr` failure modes are all
  absent by construction rather than mitigated.
- Operators can page the timer listing, in the same cursor idiom as the instance
  listing.
- Index parity becomes guarded, closing a silent-failure gap that predates this
  work.
- A corrupt timer row no longer cancels unrelated recurring timers.

**Negative / costs.**

- Breaking for consumers on two public interfaces: `kernel.TimerStore` gains a
  required method, and `service.TimerAdmin.ListArmed` becomes `ListArmedPage`.
  Accepted because v0.1.0 is untagged (precedent: ADR-0141, ADR-0142, ADR-0154).
- `AdminTimers` takes no query argument today (`admin_endpoints.go:333`), so it
  gains one plus a DTO. The three route-group closure bodies that call it
  (`stdlib/groups.go:404`, `fiber:410`, `gin:416`) change, but their exported
  factory signatures and Deps structs do **not** — `Timers service.TimerAdmin` is a
  struct field (`stdlib:197`, `fiber:217`, `gin:223`). So the breakage is
  `httpcore.AdminTimers` plus consumers implementing `service.TimerAdmin`, not the
  route-group API.
- `count` in the admin response comes from `Stats` (table total) while `items` is
  now one page, so `count == len(items)` no longer holds. Because `Stats` is
  issued only when the request asks for the total, `count` and `next_fire_at`
  became **`omitempty` pointers**: an ordinary paged request omits them rather
  than reporting `count: 0` beside a non-empty page, which would read as "no
  timers armed". Both are table-wide aggregates and belong together behind the
  same `total=true` gate — `Stats` computes them in one query.
- Paging introduces cursor-correctness risk a full scan did not have. Ties on
  `next_run` are the sharp edge — a naive `next_run > ?` predicate silently *drops*
  rows — which is why the ordering is the full triple and the index matches it.
- Editing a shipped migration file in place is only defensible pre-release. After
  v0.1.0 this same change would require an incremental migration and a superseding
  of ADR-0132's one-file rule.

**Neutral.**

- **MySQL does not actually need the new index.** InnoDB appends the primary key to
  every secondary index, so `wrkflw_timers_next_run_idx` is already physically
  `(next_run, instance_id, timer_id)`. The MySQL edit is made for cross-dialect
  uniformity — keeping the parity assertion a simple name comparison — and is a
  physical no-op.
- MySQL key length: MySQL reports `key_len: 2052` — DATETIME(6) 8 bytes plus two
  utf8mb4 `VARCHAR(255)` key parts at 1020 **plus a 2-byte length prefix each**, so
  2052 rather than the 2048 an earlier draft computed. Well inside InnoDB's
  3072-byte DYNAMIC limit **at the default `innodb_page_size=16384`**. The prior draft called the existing 2040-byte PK the
  "decisive evidence"; that was circular, since the PK bounds the limit only given
  utf8mb4, and given utf8mb4 the arithmetic suffices. The real assurance is the
  InnoDB PK-append above: this key shape already runs in production.
- MySQL's `utf8mb4_0900_ai_ci` collation is case- and accent-insensitive, which
  would break the strict total order keyset paging needs — except the PK is
  enforced under the same collation, so two collation-equal rows cannot coexist.
  The triple is strictly ordered. Cross-dialect ordering still diverges (MySQL
  `ai_ci`, Postgres DB collation, SQLite `BINARY`), so shared paging fixtures are
  pinned to one case class.
- `Stats` remains an O(N) `count(*)` aggregate, but it is no longer issued on every
  paged request: `IncludeTotal` defaults off. Bounding `Stats` itself is out of scope.
- **Postgres, not MySQL, is the dialect where the new index approaches a limit.** Its
  btree key limit (~2704 bytes) is tighter than InnoDB's 3072, and
  `instance_id`/`timer_id` are unbounded `TEXT`
  (`migrations/postgres/0001_init.sql:101-102`) rather than length-capped. The new
  index is the existing PK plus 8 bytes, so a row within 8 bytes of the PK's headroom
  would begin failing `INSERT`. Unreachable with engine-minted ids, but it is the one
  dialect where the new index exceeds an existing constraint.
- The in-place migration edit reaches **only databases migrated after the change**.
  goose keys applied migrations by version and stores no checksum, so version 1 is
  never re-applied. Fresh `dbtest` databases are unaffected, but a persistent local
  database — `examples/sqlite_wiring` writes `file:app.db` in the working directory —
  or a reused test DSN keeps the old index forever and silently loses the paging
  benefit.
- Whether the existing two-column `KeysetCursorPredicate` should also use row values
  on SQLite is left open: `dialect/sqlite.go:133-135` justifies the expanded form on
  a mixed-type correctness caveat, which does not apply to the armed triple (all
  three columns are TEXT-affinity, all three binds are strings) but may apply to
  `started_at`/`instance_id`. Backlog, own measurement, own ADR.
- The `TimerStore` contract now spans two reads, so the invariant is stated in its
  doc comment: `ArmedTimer(i,t)` returns exactly the row `ListArmed` would return for
  `(i,t)` **when that row itself is well-formed**. The qualifier is the accepted
  delta above.
- Requirement "no unbounded read expressible" was withdrawn: `Stats` alone makes it
  unsatisfiable, and it rested on the false compiler-enforcement claim.

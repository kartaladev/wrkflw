# Plan — bounded armed-timer reads

## ▶ Progress

**▶▶ DELIVERY COMPLETE. Full gate passed.** HEAD of
`feat/bounded-armed-timer-reads` (off `main` @ `9656799`), tree clean, NOT merged
and NOT pushed. One amend-in-place feature-bundle commit (implementation + tests
+ spec + ADR + plan) — the SHA is deliberately not quoted here, since amending
this very file would invalidate it. Design frozen at tag
`audit/bounded-armed-timer-reads-r3`.

**Next session, owner-approved order:** (1) merge this `--no-ff` to `main` and
ASK before pushing; (2) port the cursor hardening below to the instance-listing
sibling `runtime/kernel/lister.go` — both defects probe-verified, takes ADR
**0160** since 0155–0158 are reserved by the parked branch; (3) restart the
parked signal/message delivery bundle at ADR-0158.

**Stage 1 COMPLETE — repo green.** `go build ./...` clean; `go test ./...`
exit 0 with 64 packages ok, 0 FAIL, 0 real SKIP (46 packages have no test
files — unchanged baseline); `golangci-lint run ./...` 0 issues. Docker was up,
so all three dialects really ran.

Landed, RED observed before each new symbol:

- **S1.1** `kernel.TimerStore.ArmedTimer` (interface + `MemTimerStore` map
  lookup, `ListArmed` KEPT with the snapshot-consistency note); the cursor seam
  `EncodeArmedTimerCursor`/`DecodeArmedTimerCursor`/`ErrBadArmedTimerCursor`/
  `ArmedTimerFilter`/`ArmedTimerPage` in `runtime/kernel/armed_timer_lister.go`;
  `ErrBadArmedTimerCursor` wired into the 400 arm of `httpcore.ClassifyError`.
- **S1.2** `dialect.ArmedTimerKeysetPredicate()` / `ArmedTimerKeysetArgs(...)`
  returning the ARGS SLICE — row value on Postgres + SQLite (3 args), expanded
  lexicographic OR on MySQL (5 args). The existing two-column pair is untouched.
- **S1.3** `store.TimerStore.ArmedTimer` and `ListArmedPage` (clamp before
  `limit+1`, cursor bound through `timeArg`, `count(*)` only on `IncludeTotal`,
  `s.querier()` not `JoinOrBegin`); `listArmedPageSQL` split out and exposed via
  `export_test.go` so plan-shape tests EXPLAIN the real statement;
  `0001_init.sql` edited **in place** on all three dialects to
  `wrkflw_timers_keyset_idx (next_run, instance_id, timer_id)`.
- **S1.4** fire path: `armedTimerRecurring` returns `(recurring, determinable)`;
  `timerJobsFor` cancels only when `determinable && !recurring`.
- **S1.5** `service.TimerAdmin.ListArmed` → `ListArmedPage`, mock regenerated
  via `go generate` (mockgen lives at `$(go env GOPATH)/bin` and is NOT on the
  default PATH — prefix the command); `httpcore.ListArmedTimersQuery` DTO;
  `AdminTimers` takes the query; `limit`/`cursor`/`total` parsed in all three
  `groups.go` closure bodies.
- **S1.6** sweep: every remaining `ListArmed` reference is rehydration
  (`runtime/jobstore.go`), the kernel port, the store impl, or an accurate doc
  comment — none on the fire path.

**Deltas from this plan, decided while implementing:**

- The admin response's `count` and `next_fire_at` are now **`omitempty`
  pointers**, present only when `total=true`. Reporting `count: 0` beside a
  non-empty page would read as "no timers armed". Recorded in the ADR.
- `mockgen` is not on PATH; `go generate ./service/...` needs
  `PATH="$PATH:$(go env GOPATH)/bin"`.
- The Stage-1 index RED lives in `migration_parity_test.go` as
  `TestMigrationTimersKeysetIndex` (targeted presence/absence per dialect); T2
  extends that same file to full index-name parity.

**Stage 2 COMPLETE** — fanned out to three agents grouped BY GO PACKAGE, not by
task: concurrent agents in one working tree break each other's `go test` compile
if they share a package. A = T1+T2+T3 (`internal/persistence/store`), B = T4
(`runtime`), C = T5+T6 (`runtime/kernel` + `persistence` doc comments).

- **T1** `TestTimerStoreListArmedPageMatrix` — 6 cases × 3 backends. Each case
  wipes `wrkflw_timers` first: `HasMore`/`NextCursor`/`TotalCount` are TABLE
  properties, so prefix-scoping cannot isolate them. Non-parallel for that reason.
- **T2** `TestMigrationParity_IndexNamesConverge` — names only; green on arrival,
  all three dialects yield the same 11 explicitly-named indexes. Carries a
  `require.NotEmpty` precondition so an over-excluding filter cannot make three
  empty sets compare equal.
- **T3** `TestTimerStoreListArmedPageSeeksIndex` — the highest-value test here.
  10k rows, cursor at row 9,500, limit 50, statement taken from
  `ListArmedPageSQLForTest`. Measured: Postgres `Index Scan using
  wrkflw_timers_keyset_idx` + `Index Cond: (ROW(`; MySQL `type=range`,
  `key_len=2052`, `Handler_read_next=50`; SQLite `SEARCH … USING INDEX … (`.
  **The rejected row-value form on MySQL gives `type=index`, empty
  `possible_keys`, `Handler_read_next=9551` — 191× amplification**, confirming
  ADR-0159's central claim on live data.
- **T4** fire-path branches + the corrupt-sibling delta (driven through a verbatim
  reproduction of the pre-ADR algorithm, RED observed) + benchmark: 42→46 ns/op
  flat across N=1…10,000 with zero allocations, versus 87 ns → 3.56 ms.
- **T5** `MemTimerStore.ArmedTimer` contract + the ListArmed-agreement invariant.
  No red state (Stage 1 already shipped the behaviour) — proven non-vacuous by
  mutating the impl to key on `timerID` alone; exactly 2 subtests failed.
- **T6** four runnable `Example`s incl. a 3-page paging loop with a `NextRun` tie;
  `persistence` doc blocks now mention `ArmedTimer` and the `service.TimerAdmin`
  type assertion.

**Two traps Stage 2 caught that the design had not:**

1. **A zero `next_run` is NOT persistable on MySQL** — `DATETIME(6) NOT NULL`
   rejects the `'0000-00-00'` the driver emits under strict mode. ADR, spec, plan
   and the `EncodeArmedTimerCursor` doc comment all claimed it for all three backends.
   All four corrected; the paging matrix asserts the rejection. The opaque-cursor
   argument is unaffected. **Backlog:** a timer whose trigger cannot compute a next
   run cannot be armed at all on MySQL, and `jobStore.Save` propagates the error so
   the step fails. Predates this delivery; fixing it means choosing between
   rejecting and normalising, which needs its own decision.
2. **Asserting the SQLite index name alone passes unconditionally.** An
   index-defeating predicate still plans as
   `SCAN wrkflw_timers USING INDEX wrkflw_timers_keyset_idx` — a full 10k-row walk
   that names the index. Only `SEARCH … USING INDEX <name> (` plus
   `NotContains("SCAN wrkflw_timers")` discriminates.

**Delivery Gate:**

- Verification ✅ — `go build ./...` clean; `go test ./...` exit 0 (64 ok, 0 FAIL);
  `-race -coverprofile` exit 0; `golangci-lint run ./...` 0 issues. Coverage on
  every touched package clears the 85% floor: kernel 88.2%, dialect 98.8%, store
  87.3%, runtime 93.3%, service 93.1% (excluding generated mocks, ADR-0143),
  httpcore 94.3%, stdlib 97.8%, gin 96.3%, fiber 86.4%. Repo total 73.2%
  (long-standing, `examples/` + adapters are the drag — not a regression).
- `/security-review` ✅ — **0 findings.** SQL is parameterised throughout
  (`LIMIT ?` bound, not interpolated; the only concatenated fragment is a
  per-dialect literal constant); the cursor JSON decodes into a closed
  three-field struct with no interface fields; the 5xx arm still emits no
  `Message`. Noted positively: the endpoint's scope did not widen — the
  pre-change `ListArmed` already returned the entire global armed set.
- `/code-review` ✅ — run by the owner; **4 findings, all 4 folded** (see the
  second block below). Three adversarial Opus reviews had already been run as a
  stand-in beforehand; `/code-review` still found a Medium the stand-ins missed,
  which is the argument for the real gate.

**Review round — accepted and folded:**

1. **A real defect, found independently by two reviewers.** `AdminTimers`
   forwarded `IncludeTotal` into the store filter *and* called `Stats`, so
   `?total=true` issued `count(*)` twice and discarded `page.TotalCount`. The one
   path that asks for a total became more expensive than before the delivery —
   inverting its own premise. `Stats` stays (`NextFireAt` is not derivable from
   the page); the filter no longer carries `IncludeTotal`, pinned by
   exact-argument matchers in four test packages.
2. **`EncodeArmedTimerCursor` could return the sentinel it forbids.** It
   discarded the marshal error, and `""` IS the first-page sentinel — a page
   could answer `has_more: true` with an empty `next_cursor` and loop a client
   forever. Now returns `(string, error)`.
3. Added `var _ service.TimerAdmin = (*store.TimerStore)(nil)`; without it a port
   drift degraded to a runtime `ok == false` and a silently unregistered route.
4. The handler clamps `limit` like `AdminListInstances` — `TimerAdmin` is a port
   consumers implement, so the obligation must not live only in a doc comment.
5. `count` → `total_count`; the field's meaning changed and a rename beats
   silently reinterpreting one consumers already parse.
6. Cursor helpers renamed `Armed*` → `ArmedTimer*` for family consistency; file
   renamed `armed_timer_lister.go` → `armed_timer_paging.go` (it holds no lister).
7. Five test weaknesses closed, **each verified by mutation**: the `math.MaxInt`
   clamp case was blind at 55 rows (now 205); the tie fixture never exercised the
   innermost `(instance_id = ? AND timer_id > ?)` term — deleting that clause left
   the old test green and fails the new ones; `ArmedTimer`/`ListArmedPage` had no
   error-path coverage; the corrupt-sibling delta ran only against a double where
   both halves held by construction, and now runs on all three real backends via
   valid-JSON-of-the-wrong-shape (`'[]'`).

**Declined, with reasons:**

- **Make `ArmedTimer` an optional capability interface** (matching the
  `TimerWriter`/`DefinitionLister`/`TxRunner` precedent). Argued well from
  codebase precedent, but the failure modes are asymmetric: under a capability a
  consumer's existing store compiles and every one-shot timer then leaks
  silently forever; under a required method they get a compile error. Pre-v0.1.0
  the compile error is the better outcome — on the same library-ergonomics axis
  the finding invoked.
- **Migrate `KeysetCursorArgCount` to return an args slice.** Out of scope
  (instance listing). But the reviewer proved this ADR's stated justification for
  the divergence was *false*, so the ADR's reasoning is corrected in place and the
  migration is recorded as backlog.
- **Rename the `ArmedTimer` method to avoid colliding with the type it returns.**
  Legal Go, settled by the audited design, and it reads naturally as a getter.
- **Deduplicate the six copies of query-param parsing** across adapters, and
  fiber's `lim > 0` divergence. Behaviour-identical today and it would touch
  instance listing. Backlog.

**`/code-review` round — 4 findings, all folded:**

1. **Medium, and the stand-in reviews all missed it.** The cursor payload had no
   discriminator, and `json.Unmarshal` ignores unknown fields — so an *instance*
   cursor (also base64-of-JSON, also carrying an `instance_id`) decoded into the
   armed-timer payload with **no error**, yielding `(zero, "<instance>", "")`. As
   a keyset predicate that matches nearly every row, so an operator pasting the
   wrong cursor got 200 and page one back, forever, silently — precisely what
   `ErrBadArmedTimerCursor` exists to prevent. Reproduced first, then fixed with a
   `kind` discriminator + `DisallowUnknownFields` + rejection of empty identity
   fields. Three new RED cases cover it.
2. `ListArmedTimersQuery`'s doc said limit is "never clamped a second time here"
   while `AdminTimers` clamps — the two comments contradicted after the earlier
   review round changed the handler. The handler's version is the true one.
3. The `service.TimerAdmin` assertion added in the previous round put a
   production `persistence` → `service` import edge in, a latent import cycle.
   Moved to the external test package: same compile-time guard, no production
   edge. **Mutation-tested** — renaming the concrete method makes it fire with
   `*store.TimerStore does not implement service.TimerAdmin`.
4. The ADR claimed the undeterminable branch costs "at most one duplicate fire".
   It understates: the fired one-shot's row is orphaned, and if the instance is
   pruned before the next fire, `timerFireFunc` drops that fire, so the row is
   re-armed **every boot** and lingers in the admin listing. Only the **optional**
   `Pruner.PruneTimers` reclaims it. The ADR and `timerops.go` now name it as the
   required mitigation rather than implying self-healing.

⚠ Lint caught a self-inflicted problem in fix 3: the wrapper `TestXxx` I first
wrote around the assertion compared a typed-nil interface against `nil`
(staticcheck SA4023 — never true). The bare `var _` assertion IS the test; the
wrapper asserted nothing the compiler had not already proven.

## ▶ Follow-on found during this delivery — the same bug class, unfixed

`runtime/kernel/lister.go`'s **instance** cursor has BOTH defects finding 1 above
fixed in the armed-timer cursor. Probe-verified on 2026-07-30, not inferred:

1. `DecodeCursor` has no discriminator and uses plain `json.Unmarshal`, so an
   armed-timer cursor decodes into it with **no error** as `(zero, "inst-x")`.
   Instance listing is DESC, so that predicate matches nothing — the operator
   gets a silently EMPTY page with a 200 rather than the armed-timer case's
   infinite loop. Less severe, still a wrong answer with no diagnostic.
2. `EncodeCursor` swallows its marshal error and returns `""` — the first-page
   sentinel. Less reachable than the timer case, because `StartedAt` is
   engine-minted from the clock rather than user-supplied like `schedule.At`.

The fix is a direct port of `runtime/kernel/armed_timer_paging.go`: a `kind`
discriminator, `DisallowUnknownFields`, empty-identity rejection, and
`EncodeCursor` returning `(string, error)` with its two call sites updated. It
changes a public signature, so it needs its own ADR — **0160**, since 0155–0158
are reserved by the parked `feat/durable-waiters-delivery-correctness` branch.

Deliberately NOT folded into this delivery: it is a separate public-API change to
a different subsystem, and this bundle had already passed its gate.

Verification commands are in the checklist at the foot of this plan.

---

Spec: `docs/specs/2026-07-30-bounded-armed-timer-reads.md`
ADR: `docs/adr/0159-bounded-armed-timer-reads.md`
Branch: `feat/bounded-armed-timer-reads` (off `main` @ `9656799`)

> Revised after two adversarial audits returned "do not implement" on the first
> draft. Streaming `scheduler.JobStore.Load` is **withdrawn entirely**; the
> `0002_*.sql` migration is replaced by an in-place edit of `0001_init.sql`; the
> keyset predicate is a dialect capability rather than a hand-rolled row-value
> comparison. See the spec's "Audit corrections" section.

## Shape of the work

Adding a required method to `kernel.TimerStore` breaks compilation for every
implementor and test double at once. Per CLAUDE.md rule #11 a serial,
compile-breaking, shared-type change that everything else blocks on **stays inline
in the controller**. So Stage 1 is inline and serial; Stage 2 fans out on disjoint
files once the repo compiles again.

Stage 1 carries the *minimum* RED test per new symbol; Stage 2 broadens the matrix.
That split is not permission to defer Stage 1 testing — a Stage 1 symbol with no
observable red state is a rule-#6 violation regardless of what Stage 2 adds.

## Stage 1 — inline, serial

### S1.1 — `kernel`: the point lookup and the cursor seam

Files: `runtime/kernel/timerstore.go`, `runtime/kernel/timerstore_test.go`, and the
cursor helpers alongside `runtime/kernel/lister.go`.

Add `ArmedTimer(ctx, instanceID, timerID) (ArmedTimer, bool, error)` to the
`TimerStore` interface. **Keep `ListArmed` exactly as it is** — rehydration depends
on its single-statement snapshot. Implement `ArmedTimer` on `MemTimerStore` as a
direct lookup on the existing `armed` map under `s.mu`.

Add, in the idiom of `lister.go`'s existing `EncodeCursor`/`DecodeCursor`/
`ErrBadCursor`/`NormalizeLimit`:

- `EncodeArmedTimerCursor(nextRun time.Time, instanceID, timerID string) string` — the
  **same base64 envelope as `EncodeCursor`** (`lister.go:26-44`). `time.Time`'s JSON
  form is lossless, so do **not** try to encode a fixed-width nine-digit layout here.
  You could not anyway: `textTimeLayout` is unexported
  (`internal/persistence/store/time_codec.go:26`) and `store` imports `kernel`
  (`timerstore.go:17`), so `kernel` cannot import `store`; duplicating the constant
  would split one serialization decision across two packages.
- `DecodeArmedTimerCursor(cursor string) (time.Time, string, string, error)`.
- `ErrBadArmedTimerCursor` — a **new** sentinel, not `ErrBadCursor`, whose message reads
  "malformed instance cursor" (`lister.go:15`) and would misreport a timer cursor to
  an operator. Extend the 400 mapping at `transport/http/httpcore/errors.go:36`.
- `ArmedTimerFilter{Cursor string; Limit int; IncludeTotal bool}` and
  `ArmedTimerPage{Items []ArmedTimer; NextCursor string; HasMore bool; TotalCount int64}`.

The empty-string cursor is the first-page sentinel. Do **not** add an exported cursor
struct and do **not** infer "first page" from a zero `time.Time`: the engine arms
with a zero `nextRun` (`runtime/timerops.go:156-159` arms regardless of
`strig.Next` returning `ok == false`) and such a row sorts first, so a zero-value
sentinel aliases a real row and loops forever. (Measured during implementation:
the row is persisted on Postgres and SQLite but **rejected by MySQL** — see the
spec's correction. The cursor argument stands either way.)

RED first: assert `store.ArmedTimer(...)` before the method exists; run
`go test ./runtime/kernel/...` and observe `undefined`.

Verify: `go test ./runtime/kernel/... > /tmp/s11.txt 2>&1; echo $?`

### S1.2 — dialect: a three-column keyset capability

Files: `internal/persistence/dialect/{dialect,postgres,mysql,sqlite}.go` and their
tests.

The existing `KeysetCursorPredicate()` / `KeysetCursorArgCount()`
(`dialect/dialect.go:142`, `:147`) is **two-column** and cannot serve the armed
triple. Add a sibling pair, named:

```go
ArmedTimerKeysetPredicate() string
ArmedTimerKeysetArgs(nextRun any, instanceID, timerID string) []any
```

Return the **args slice, not an arg count**. The existing `ArgCount` works only
because 2-vs-3 uniquely encodes "bind the cursor time once or twice"; that does not
generalise — row value binds 3 values, the expanded form binds 5 — and a count would
force the caller into an `if argCount == 5` branch, worse than the `dialect.Name()`
comparison the rules forbid. Do not generalise or change the existing two-column
pair; instance listing is out of scope.

Per-dialect shape, set by measurement, **not** symmetry:

- **Postgres**: row value — `(next_run, instance_id, timer_id) > (?, ?, ?)`, 3 args.
  Measured `Index Scan using wrkflw_timers_keyset_idx` + `Index Cond: (ROW(...) > ROW(...))`.
- **SQLite**: row value, 3 args. Measured one
  `SEARCH … USING INDEX wrkflw_timers_keyset_idx ((next_run,instance_id,timer_id)>(?,?,?))`.
  **Not** `USING COVERING INDEX` — impossible, the projection selects `trigger_payload`
  and three other non-indexed columns.
- **MySQL**: expanded lexicographic OR —
  `next_run > ? OR (next_run = ? AND (instance_id > ? OR (instance_id = ? AND timer_id > ?)))`,
  5 args. **Row value must not be used here**: measured `type: index`,
  `possible_keys: NULL`, and `Handler_read_next` proportional to cursor depth
  (≈21,650 mid-table over 50k rows) versus `type: range` and ~pageSize for the
  expanded form.

The SQLite choice was re-measured after two audits disagreed. With distinct
`next_run` values the expanded form yields **two** `SEARCH` ops — `(next_run>?)` and
`(next_run=?)`, an OR decomposition; with duplicate-heavy values a flat
`SCAN … USING INDEX`. Row value yields one triple seek in both. Document in the new
capability's doc comment that row value is safe on SQLite **because all three columns
are TEXT-affinity and all three binds are strings** — `dialect/sqlite.go:133-135`
warns against row values for *mixed-type* columns, and an implementer reading that
adjacent comment has no other way to know the divergence is deliberate.

Selection is by capability method. **Never** compare `dialect.Name()` — same rule
the codebase applies to `TimestampsAsText()`.

Verify: `go test ./internal/persistence/dialect/... > /tmp/s12.txt 2>&1; echo $?`

### S1.3 — SQL store

Files: `internal/persistence/store/timerstore.go`, its conformance suite (see
below), and the three `0001_init.sql` files.

`ArmedTimer`: `SELECT … WHERE instance_id = ? AND timer_id = ?` through
`dialect.Rebind`, reusing `scanArmedTimer`. Not-found is `(zero, false, nil)`, never
an error. Use **`s.querier()`** — the pool-backed handle, matching `ListArmed` — and
**not** `transaction.JoinOrBegin`, which the neighbouring write methods use. Copying
the adjacent method would give the read transaction-joining visibility semantics
mid-step.

`ListArmedPage(ctx, filter kernel.ArmedTimerFilter) (kernel.ArmedTimerPage, error)`:
decode the cursor, apply the dialect keyset predicate (omitted entirely when the
cursor is empty), `ORDER BY next_run, instance_id, timer_id`, fetch `limit+1` rows to
derive `HasMore`, truncate to `limit`. Issue the `count(*)` **only** when
`filter.IncludeTotal`.

Clamp **once, here** — not also in the handler — and in this exact order, matching
`internal/persistence/store/lister.go:75-76`:

```go
limit := kernel.NormalizeLimit(filter.Limit) // default 50, max 200
fetch := limit + 1
```

The reverse order is a live defect: `math.MaxInt + 1` overflows to `math.MinInt`;
Postgres and MySQL error on a negative `LIMIT` but **SQLite treats it as no limit and
returns the whole table**.

Bind the decoded cursor time through the existing `timeArg(s.dialect, t)` — the same
call `Lister.List` makes at `lister.go:105` — gated on
`dialect.TimestampsAsText()`. Binding a raw `time.Time` on SQLite makes the driver
stringify it non-ISO8601 (`timerstore.go:28-32`), the predicate matches nothing, and
**every listing silently truncates at one page with no error**.

Expose the built SQL through `export_test.go` so T3 can `EXPLAIN` the real statement
instead of a hand-copied duplicate that drifts.

**Migrations: edit `0001_init.sql` in place in all three dialects.** Replace
`CREATE INDEX wrkflw_timers_next_run_idx ON wrkflw_timers (next_run);` with
`CREATE INDEX wrkflw_timers_keyset_idx ON wrkflw_timers (next_run, instance_id, timer_id);`.

Do **not** add `0002_*.sql`. `TestMigrations_OneFilePerDialect`
(`internal/persistence/store/migrations_count_test.go`) asserts exactly one file per
dialect and ADR-0132 makes it policy; a second file also flips the goose head
version and breaks the version-coupled migrator tests. The in-place edit keeps head
version 1, so `migrator_test.go`'s head-version assertions stay green untouched, and
the existing `-- +goose Down` sections need no change because they drop the whole
table.

The timer-store coverage lives in **`timerstore_conformance_test.go`** —
`TestTimerStoreListArmed`, `TestTimerStoreListArmedEmpty`,
`TestTimerStoreListArmedMultiInstance`, `TestTimerStoreDescriptorRoundTrip`,
`TestTimerStoreFireAtSubSecond`, `TestTimerStoreStats` (note the last three have **no**
`ListArmed` infix), driven by `forEachDialect`. **There is no `timerstore_test.go`** —
do not create one; extend the conformance suite.

Verify: `go test ./internal/persistence/store/... > /tmp/s13.txt 2>&1; echo $?`
(Docker required.)

### S1.4 — the fire path

Files: `runtime/timerops.go`, **`runtime/timerops_internal_test.go`** — the white-box
home for these tests. **`runtime/timerops_test.go` does not exist**; do not create it
(`armedTimerRecurring` and `timerJobsFor` are unexported, and the existing
`TestTimerJobsFor` table already lives in the internal file).

Rewrite `armedTimerRecurring` to call `ArmedTimer` once, through **`s.querier()`**
semantics (the read is issued before the commit transaction opens — the closure is
built at `processdriver.go:691-693` and invoked from `timerJobsFor` — so there is no
ambient-transaction visibility change and no SQLite single-connection deadlock).

`found == false` → "not recurring", exactly as today.

**`err != nil` → undeterminable: leave the fired timer alone.** This is a decided
behaviour change, not an oversight. It matches `timerJobsFor`'s nil-closure branch,
which already means "recurrence undeterminable, do not touch it"
(`runtime/timerops.go:126-131`, `:161-163`). Collapsing an error to `false` means
cancel, so one connection blip permanently disarms a recurring job; the alternative
costs at most one duplicate fire at rehydration, already an idempotent no-op. Plumb
the third state through the `armedRecurring` closure signature.

Keep a WARN on store error but **reword** it — the current text says "list armed
failed" (`runtime/timerops.go:181`), now untrue. Fix the stale comment at
`runtime/timerops.go:174`.

The `driver.timerStore == nil` guard (`:176-178`) is **unreachable in production** —
`processdriver.go:692` only builds the closure when `timerStore != nil` — so test it
via the unexported method and say so rather than calling it a hot-path branch.

**The corrupt-sibling-row delta does not belong solely in this file.** It arises only
in `store.TimerStore.scanArmedTimer`'s error paths, so it is unreachable with
`MemTimerStore` — the only store available here without Docker. Split it: a
store-conformance test for the scan-error behaviour, plus a driver-level test using a
fake whose `ListArmed` errors while `ArmedTimer` succeeds. Name the vector: the
malformed-`next_run` route (`timerstore.go:290-293`) exists **only on SQLite** (TEXT
column); the malformed-`trigger_payload` route needs bytes the database accepts as
JSON (Postgres `JSONB`/MySQL `JSON` reject invalid JSON) but which fail to decode into
`model.TriggerWire`.

Verify: `go test ./runtime/... > /tmp/s14.txt 2>&1; echo $?`

### S1.5 — admin port, handler, transports

Files: `service/opsadmin.go`, `service/opsadmin_mock.go` (regenerated via
`go generate ./service/...`, never hand-edited),
`transport/http/httpcore/admin_endpoints.go`, `transport/http/httpcore/dto.go`,
and `transport/http/{stdlib,gin,fiber}/groups.go`.

`TimerAdmin.ListArmed` → `ListArmedPage(ctx, filter kernel.ArmedTimerFilter)`.
`AdminTimers` currently takes **no query argument** (`admin_endpoints.go:333`), so
it gains one plus a query DTO mirroring `ListInstancesQuery` (`dto.go:104-105`).
`limit` is **clamped, not rejected** — and clamped in the store only, not twice. A
malformed cursor is a **400** via `ErrBadArmedTimerCursor` — never a silent reset to page
one, which would make an operator paging a large table loop without noticing.

`Stats` is called only when the request asks for the total; `IncludeTotal` defaults
off so a plain paged request issues no `count(*)`.

Response gains `next_cursor` and `has_more`, mirroring `adminListResponse`. Note
`count` comes from `Stats` (table total) while `items` is one page, so
`count == len(items)` no longer holds — existing handler tests asserting it must
change.

The three `groups.go` **closure bodies** change (`stdlib:404`, `fiber:410`, `gin:416`); their exported factory signatures and Deps structs do not — `Timers service.TimerAdmin` is a struct field.

`MockTimerAdmin` expectations must be updated in **four** transport packages:
`httpcore/admin_endpoints_test.go`, `gin/{gin_admin_test.go,gin_admin_errors_test.go}`,
`fiber/fiber_test.go`, `stdlib/{errors_test.go,coverage_test.go}`.

Verify: `go test ./service/... ./transport/... > /tmp/s15.txt 2>&1; echo $?`

### S1.6 — sweep to green

This stage is much smaller than an earlier draft claimed. The complete set of
`kernel.TimerStore` **implementors** is `kernel.MemTimerStore`, `store.TimerStore`,
and `faultTimerWriter` (`runtime/timer_txflow_test.go:71`) — verified by searching for
the method, not for callers. `faultTimerWriter` is a pure delegator over
`inner *store.TimerStore`, so `ArmedTimer` is a one-line delegation; its fault
injection is confined to `UpsertJob`/`DeleteJob`.

Files that merely **call** `ListArmed` compile unchanged and are **not** in scope —
an earlier draft listed them because it deleted the method:
`internal/persistence/store/timerwriter_test.go`, `runtime/jobstore_internal_test.go`,
`runtime/jobstore_test.go`, `runtime/rehydrate_durable_test.go`,
`persistence/facade_{constructors,mysql,sqlite}_test.go`.

Stale doc comments — exactly **two**, plus one log line: `service/opsadmin.go:33-35`,
`runtime/timerops.go:174`, and the WARN text at `runtime/timerops.go:181`. The
`persistence/persistence.go:417,425`, `persistence/sqlite.go:142` and
`persistence/mysql.go:112` snippets are **still correct** — `ListArmed` is retained.

Confirm the interface-value assertion sites still hold (they need verifying, not
editing): `persistence/persistence.go:183,426`, `persistence/sqlite.go:143`,
`persistence/mysql.go`, `persistence/durableprovider.go:26,34` — `NewTimerStore`
returns `kernel.TimerStore`, so widening the method set narrows what a consumer may
supply.

Verify: `go build ./... && go test ./... > /tmp/s16.txt 2>&1; echo $?` — must be 0
before Stage 2 starts.

## Stage 2 — fan out (fresh subagent per task, disjoint files)

| Task | Files (disjoint) | Content |
|---|---|---|
| T1 | `internal/persistence/store/timerstore_conformance_test.go` | Paging matrix on all three backends: page boundaries, **ties on `next_run`**, `HasMore` exactly at the boundary, a row with `next_run == time.Time{}` (sorts first, must page correctly), `limit` clamping incl. `math.MaxInt`, cursor round-trip at sub-second precision. |
| T2 | `internal/persistence/store/migration_parity_test.go` | Extend parity to index **names**, comparing only explicitly-named, non-implicit indexes. Exclude PK-backing (`PRIMARY` / `*_pkey` / `sqlite_autoindex_*`), UNIQUE-backing, and MySQL FK-backing indexes. Assert `wrkflw_timers_keyset_idx` present and `wrkflw_timers_next_run_idx` absent as targeted assertions. **Do not "fix" the deliberate per-dialect column-list divergences** (`wrkflw_outbox_dead_idx`, `wrkflw_call_links_pending_idx`) by loosening anything. |
| T3 | `internal/persistence/store/timerstore_seek_test.go` (new) | **Per-dialect proof a page is a seek, not a scan** — without it the MySQL regression ships green (Risk 1). See the T3 mechanics block below; the naive version of this test passes unconditionally. |
| T4 | `runtime/timerops_internal_test.go` (**not** `timerops_test.go`, which does not exist) | Extend the `armedTimerRecurring` / `timerJobsFor` tables landed in S1.4; add the corrupt-sibling-row delta (driver-level fake whose `ListArmed` errors but `ArmedTimer` succeeds) and a before/after benchmark evidencing N→1. |
| T5 | `runtime/kernel/timerstore_test.go` | Mem-vs-SQL parity for **`ArmedTimer` only** — `MemTimerStore` has no paging (this design adds only `ArmedTimer` to it, and it is deliberately not a `TimerAdmin`: no `Stats`, `service/opsadmin.go:33-35`). Fixtures `Truncate(time.Millisecond)`; assert the store-side invariant that `ArmedTimer(i,t)` matches what `ListArmed` returns for `(i,t)` when that row is well-formed. |
| T6 | `runtime/kernel/example_test.go`, `persistence/*.go` doc comments | Testable `Example` for the point lookup and paged listing (Golang rule #6 — consumer-facing root-package surface), closing the silent-rot gap in the `persistence` comment blocks. |

T1, T2 and T3 share the `internal/persistence/store` package but occupy **different
files**; do not let any of them touch another's file.

### T3 mechanics — the naive version of this test passes unconditionally

- `Handler_read_*` are **session**-scoped, but `dbtest.RunTestMySQL` returns an
  8-connection pool (`internal/dbtest/mysql.go:122-148`), so `FLUSH STATUS`, the
  query, and `SHOW STATUS` can land on three different connections. The test then
  reads `0` and **passes regardless of predicate shape** — green-lighting exactly the
  regression it exists to catch. Pin one connection: `db.SetMaxOpenConns(1)` /
  `SetMaxIdleConns(1)` / `SetConnMaxLifetime(0)`, or take an explicit `*sql.Conn`, and
  build the `TimerStore` over that handle.
- Mark it **non-parallel**. `forEachDialect` uses `t.Parallel()` against a shared
  MySQL server, and a global `FLUSH STATUS` is corrupted by concurrent tests.
  `FLUSH STATUS` needs `RELOAD`, which `dbtest`'s root user has.
- **Primary assertion is `EXPLAIN`**: `type='range'` and
  `key='wrkflw_timers_keyset_idx'`. `Handler_read_next <= 4*limit` is the volume
  backstop — **never `<= 1`**; a 50-row page reads ~50 rows in key order, so `<= 1`
  fails the *correct* implementation.
- Fixture: ≥10k rows, cursor near the **end**. With the cursor at the start even the
  row-value form reports few reads and the regression is undetectable.
- Postgres: ≥10k rows and `ANALYZE` first (or `SET LOCAL enable_seqscan = off`) — a
  small fixture is seq-scanned and no index node appears. Assert `Index Cond: (ROW(`
  and the index name. **Never** `Index Only Scan`.
- SQLite: **`EXPLAIN QUERY PLAN`**, not `EXPLAIN` — plain `EXPLAIN` returns VDBE
  bytecode rows, not plan text. Assert `USING INDEX wrkflw_timers_keyset_idx`. Stable
  at small row counts, no `ANALYZE` needed.
- `EXPLAIN` the statement the store actually builds, via `export_test.go`.

## Verification checklist

- [ ] `go build ./...` clean
- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` — ≥85% on touched packages excluding generated files (ADR-0143). **Read the number; the script only reports, its exit code proves nothing.**
- [ ] `go test ./...` from repo root — no regressions (Docker up)
- [ ] `golangci-lint run ./...` clean
- [ ] Every fire-path branch covered — found+recurring, found+non-recurring, not-found, store error, nil store
- [ ] The corrupt-sibling-row behaviour delta asserted, not assumed
- [ ] Ties on `next_run` covered on **all three** backends
- [ ] A row with `next_run == time.Time{}` pages correctly and terminates
- [ ] **Seek-not-scan asserted per dialect** — MySQL `Handler_read_next` bounded
- [ ] Cursor round-trip byte-exact at sub-second precision, at the HTTP layer as well as the store
- [ ] Malformed cursor → 400; `limit` above max → clamped, not rejected
- [ ] Index parity asserted; deliberate column-list divergences left intact
- [ ] `ls internal/persistence/store/migrations/*/` shows exactly one file per dialect; goose head version still 1
- [ ] No `dialect.Name()` comparison introduced
- [ ] `grep -rn "ListArmed\b" --include='*.go' .` — every remaining hit is rehydration or admin, none on the fire path
- [ ] Doc comments updated (`persistence/*`, `service/opsadmin.go:33-35`, `runtime/timerops.go:174`, the reworded WARN)
- [ ] Cursor decoded time is bound through `timeArg`, not raw — page 2 is non-empty on SQLite
- [ ] Clamp happens before `limit+1`; `math.MaxInt` yields ≤200 rows, never a negative `LIMIT`
- [ ] `IncludeTotal` defaults off — a plain paged request issues no `count(*)`
- [ ] T3 runs on a pinned single connection, non-parallel, cursor near the end of a ≥10k fixture
- [ ] SQLite plan assertions use `EXPLAIN QUERY PLAN`; no assertion mentions `Index Only Scan` or `COVERING INDEX`
- [ ] Store-error path leaves the fired timer alone (undeterminable), asserted
- [ ] Benchmark recorded
- [ ] Red state observable in the transcript for every new symbol (rule #6 self-audit)
- [ ] `/code-review` and `/security-review` — all findings fixed and folded via `--amend`, or explicitly adjudicated with reasons
- [ ] One feature-bundle commit: implementation + tests + spec + ADR + plan

## Risks

1. **MySQL keyset shape.** The single highest-risk item: using the row-value form on
   MySQL makes paging *slower* than the scan it replaces, and no ordinary test would
   catch it. T3 exists solely to make that failure visible.
2. **Cursor precision.** Sub-second `next_run` is normal. A cursor derived from the
   `RFC3339` display layout silently skips or repeats rows — the ADR-0151 bug class,
   already shipped once here.
3. **Zero `next_run` rows.** Real and persisted. A value-inferred first-page
   sentinel loops forever; the opaque-string cursor is the structural fix, and T1
   asserts it.
4. **Editing a shipped migration in place.** Only defensible pre-release. If a tag
   exists by implementation time, stop — this needs an incremental migration and a
   superseding of ADR-0132 instead.
5. **Breaking two public interfaces plus `httpcore.AdminTimers`.** Route-group factory signatures are NOT affected.
6. **The in-place migration edit reaches only databases migrated after it.** goose
   keys by version and stores no checksum, so version 1 is never re-applied. Fresh
   `dbtest` databases are fine, but a persistent local database —
   `examples/sqlite_wiring` writes `file:app.db` in the working directory — or a
   reused test DSN keeps the old index forever and silently loses the benefit. T2
   must **fail loudly** when `wrkflw_timers_keyset_idx` is absent rather than skip.
   This interacts with the queued "session-scoped DB containers /
   `WRKFLW_TEST_POSTGRES_DSN`" backlog item: a reused DSN would run T2/T3 against the
   old schema.
   The doc-comment sweep in S1.6 is part of that breakage, not a nicety.

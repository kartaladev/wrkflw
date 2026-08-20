# Fix wave 1 — `persistence` package (backlog 119, 112, 117, 109, and 34)

Date: 2026-08-20. Working tree: `main`, no branch, no commit (controller commits).
Method: strict RED → GREEN per item, evidence recorded here immediately after each item.

---

## 119 — `NewSQLiteDeduper` does not exist — **DONE**

### Files changed

- `persistence/dedup.go` — added `NewSQLiteDeduper(db *sql.DB) (Deduper, error)`, a pure mirror of
  `NewMySQLDeduper` delegating to `store.NewDeduper(db, dialect.NewSQLite())`.
- `persistence/facade_sqlite_test.go` — added `TestNewSQLiteDeduperSeenIsIdempotent`
  (+ `internal/database/transaction` import).

### Observed RED (verbatim)

```
$ go test -count=1 -run 'TestNewSQLiteDeduperSeenIsIdempotent' ./persistence/...
EXIT=1
# github.com/kartaladev/wrkflw/persistence_test [github.com/kartaladev/wrkflw/persistence.test]
persistence/facade_sqlite_test.go:468:24: undefined: persistence.NewSQLiteDeduper
FAIL	github.com/kartaladev/wrkflw/persistence [build failed]
```

### GREEN

```
$ go test -count=1 -v -run 'TestNewSQLiteDeduperSeenIsIdempotent' ./persistence/
EXIT=0
--- PASS: TestNewSQLiteDeduperSeenIsIdempotent (0.01s)
```

### Non-vacuity — why the assertions can fail

A compile-only RED proves nothing about behaviour (triage §119.4), so the test carries three
behavioural discriminators, all observed to change outcome within the one run:

1. `Seen("sqlite-msg-1")` → `true`, then the identical call → `false`. Same input, opposite
   answers ⇒ the duplicate assertion is driven by real stored state, not a constant.
2. Control: `Seen("sqlite-msg-2")` → `true` in the same DB ⇒ a `Seen` that always answered
   "already seen" fails.
3. Ambient-transaction contract (`persistence/dedup.go:19` godoc): `Seen("sqlite-msg-3")` inside a
   **rolled-back** `transaction.Begin` tx reports `true`, and a subsequent committed call reports
   `true` **again**. If the constructor's `Deduper` had opened its own leaf tx instead of joining
   the ambient one, the mark would have survived and the second call would report `false`.
   This is the assertion that would fail for a constructor that satisfies the interface but
   violates the contract.

### Premises checked

- Triage claim "the table exists for SQLite in all three migration sets" — confirmed by the test
  running against `dbtest.RunTestSQLite` (which applies `store.MigrateSQLite`) with no missing-table
  error. No premise found false.

---

## 112 — DB pool saturation is invisible — **DONE**

Severity kept Low, scope kept proportionate: the consumer still owns the pool, the collectors are
opt-in, they open/close/reconfigure nothing, and no ownership was redesigned.

### Files changed

- `persistence/pool_stats.go` (new) — `PoolStatsCollector`, `NewPoolStatsCollector(db *sql.DB, ...)`,
  `NewPostgresPoolStatsCollector(pool *pgxpool.Pool, ...)`, `PoolStatsOption`,
  `WithPoolStatsMeterProvider`, `WithPoolStatsLogger`.
- `persistence/pool_stats_test.go` (new) — the tests below (a third, `TestPoolStatsCollectorToleratesNilOptions`, was added later under item 34).

### Instruments

| instrument | kind | `*sql.DB` source | pgx source |
|---|---|---|---|
| `wrkflw_db_pool_in_use` | gauge | `Stats().InUse` | `Stat().AcquiredConns()` |
| `wrkflw_db_pool_idle` | gauge | `Stats().Idle` | `Stat().IdleConns()` |
| `wrkflw_db_pool_max_open` | gauge | `Stats().MaxOpenConnections` | `Stat().MaxConns()` |
| `wrkflw_db_pool_waits_total` | observable counter | `Stats().WaitCount` | `Stat().EmptyAcquireCount()` |

Deviation from the triage sketch (`in_use`, `idle`, `wait_count`), stated deliberately:
`max_open` was added because saturation is `in_use / max_open` and the sketch's three gauges have no
denominator — the item's title is "saturation is invisible", and three gauges without the ceiling
leave it invisible. `wait_count` is registered as an **observable counter** named
`wrkflw_db_pool_waits_total`, not a gauge: the underlying value is cumulative and monotonic, and the
repo's counter convention is `_total` (`wrkflw_eventing_published_total`,
`wrkflw_scheduler_job_runs_total`).

Backlog 116 (exported symbols leaking `internal/` types) was deliberately not repeated:
`PoolStatsOption` is `persistence`'s own type and keeps the `internal/observability` options in an
unexported field, mirroring the `runtime/chain` precedent.

### Observed RED (verbatim)

```
$ go test -count=1 -run 'TestPoolStatsCollectorObservesPoolState|TestPostgresPoolStatsCollectorObservesPoolState' ./persistence/
EXIT=1
# github.com/kartaladev/wrkflw/persistence_test [github.com/kartaladev/wrkflw/persistence.test]
persistence/pool_stats_test.go:79:19: undefined: persistence.NewPoolStatsCollector
persistence/pool_stats_test.go:79:57: undefined: persistence.WithPoolStatsMeterProvider
persistence/pool_stats_test.go:129:19: undefined: persistence.NewPostgresPoolStatsCollector
persistence/pool_stats_test.go:129:67: undefined: persistence.WithPoolStatsMeterProvider
FAIL	github.com/kartaladev/wrkflw/persistence [build failed]
```

### GREEN

```
$ go test -count=1 -v -run 'TestPoolStatsCollectorObservesPoolState|TestPostgresPoolStatsCollectorObservesPoolState' ./persistence/
EXIT=0
--- PASS: TestPoolStatsCollectorObservesPoolState (0.06s)      # container-free (dbtest.RunTestSQLite)
--- PASS: TestPostgresPoolStatsCollectorObservesPoolState (2.12s)  # dbtest.RunTestDatabase
```

### Non-vacuity — two executed checks, because a compile-only RED proves nothing

1. **Mutation** — deleted `o.ObserveInt64(inUse, s.inUse)` from the callback:

   ```
   MUTATION EXIT=1
   --- FAIL: TestPoolStatsCollectorObservesPoolState (0.01s)
       pool_stats_test.go:89: metric "wrkflw_db_pool_in_use" was not collected
   ```

   Restored from a `cp` backup; `diff` clean. (⚠ `git checkout <path>` was NOT used — it restores
   from the index and would have destroyed the uncommitted implementation.)
2. **Fixture probe for the counter assertion** — the `waits_total >= 1` assertion would be vacuous if
   the counter were already non-zero before the forced wait. Temporarily asserted
   `waits_total == 0` at the earlier collection point: **EXIT=0**. So the counter genuinely
   transitions `0 → ≥1` across the forced acquire, and the assertion discriminates. Probe reverted;
   `diff` clean.

Every value assertion pins an exact number (`assert.Equal`), never mere presence — `RunTestSQLite`'s
`SetMaxOpenConns(1)` is what makes `in_use == 1`, `idle == 0`, `max_open == 1` exact.

### Premises checked

- Triage's "zero hits for `db.Stats()`/`pool.Stat()`" — re-derived before implementing:
  `grep -rn "db.Stats()\|pool.Stat()" --include=*.go . | grep -v _test` returned nothing. TRUE.
- Behavioural premise executed rather than reasoned: `database/sql` increments `WaitCount` for an
  acquire that blocks on an exhausted pool **even when that acquire then fails on context
  deadline**. Confirmed by the passing `>= 1` assertion combined with the `== 0` probe above.

---

## 117 — ADR-0082's documented SQLite DSN is inert — **DONE**

### ⭐ The executed `PRAGMA busy_timeout` readback (this is the artifact the brief demanded)

Measured on the pinned `modernc.org/sqlite`, by opening a throwaway database with each DSN form
and running `PRAGMA busy_timeout` — **not reasoned from the driver source**. This is
`TestSQLiteDSNSyntaxOnPinnedDriver` in `persistence/sqlite_dsn_test.go`, which passes and stays in
the tree as executable evidence:

| DSN query | `PRAGMA busy_timeout` readback |
|---|---|
| `_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on` (**mattn**, what ADR-0082 said) | **0** |
| `_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)` (**the replacement**) | **5000** |
| `_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)` (what the godoc example said) | **0** |

So the replacement DSN was verified to set the pragma **before** it was written into the ADR, and
the godoc example's omission was confirmed to be just as inert as the mattn form.

### Files changed

- `docs/adr/0082-sqlite-backend.md` §1 — the three bullets rewritten to `_pragma=` form, the full
  DSN added as a copyable line, an explicit warning that the driver ignores unrecognised
  `_`-prefixed keys **silently**, and a dated amendment note carrying the readback numbers and the
  109 measurement. The cross-reference points at §2 (single-writer constraint), the section that
  owns the pool/timeout combination.
- `persistence/sqlite.go` — **10** godoc DSN examples, plus a new paragraph on `OpenSQLite`
  explaining the parameter form and why the timeout is not cosmetic.
- `persistence/humantask.go` — **1** godoc DSN example.
- `persistence/sqlite_dsn_test.go` (new) — the three tests.

⭐ **The item named one godoc example; there were eleven.** The brief (following triage §117) flagged
`persistence/sqlite.go`'s `OpenSQLite` example. Re-derived with
`grep -rn 'sql.Open("sqlite"' persistence/`: **11** consumer-facing godoc DSN literals across two
files, **every one of them** missing `busy_timeout`. Fixing only the named one would have left ten
copies of the 174–195/200-failure configuration in the published API docs. The guard test derives
the set from the sources, so a new example is covered the moment it lands rather than being counted
once and forgotten.

### Observed RED (verbatim, abridged to the summary lines)

```
$ go test -count=1 -v -run 'TestSQLiteDSNSyntaxOnPinnedDriver|TestDocumentedSQLiteDSNsSetBusyTimeout|TestADR0082UsesPinnedDriverPragmaSyntax' ./persistence/
EXIT=1
--- FAIL: TestADR0082UsesPinnedDriverPragmaSyntax (0.00s)
--- PASS: TestSQLiteDSNSyntaxOnPinnedDriver (0.00s)          # evidence, not the gate — see table above
--- FAIL: TestDocumentedSQLiteDSNsSetBusyTimeout (0.00s)
    --- FAIL: …/sqlite.go:_file:app.db      (and #01 … #09, plus humantask.go:_file:app.db — 11 subtests)
```

with, per subtest:

```
Messages: documented DSN "_pragma=journal_mode(WAL)" leaves PRAGMA busy_timeout at 0 on
          modernc.org/sqlite; a consumer copying it gets the configuration measured to fail
          174–195 of 200 concurrent operations (backlog 109)
```

### GREEN

```
$ go test -count=1 -run 'TestSQLiteDSNSyntaxOnPinnedDriver|TestDocumentedSQLiteDSNsSetBusyTimeout|TestADR0082UsesPinnedDriverPragmaSyntax' ./persistence/
EXIT=0
ok  	github.com/kartaladev/wrkflw/persistence	0.704s
```

### One correction made during implementation

The ADR guard first asserted the mattn strings appear **nowhere** in the file. That failed after the
fix — because the amendment note must quote the superseded syntax to be useful. The assertion was
narrowed to the **normative** text (blockquote lines excluded), with a control
(`require.Contains(body, "### 1. Driver and DSN")`) so that a filter which accidentally stripped the
whole document could not make the test pass vacuously.

Re-mutation-verified afterwards, since narrowing an assertion can disarm it — the §1 bullet and the
full-DSN line were reverted to mattn form:

```
MUTATION EXIT=1
--- FAIL: TestADR0082UsesPinnedDriverPragmaSyntax (0.00s)
    Messages: ADR-0082 prescribes mattn/go-sqlite3 syntax "_busy_timeout=", which modernc.org/sqlite ignores silently
    Messages: ADR-0082 must document the busy timeout in the form the pinned driver honours
```

Restored via `cp` backup; `diff` clean.

---

## 109 — `OpenSQLite` never checks the single-writer contract — **DONE (warn), with a scoped ADR recommendation**

### The adjudication the brief asked for: patch or ADR?

Triage rated this `D` because the choice is *reject vs warn*. Split, and the two halves land in
different places:

- **Warn — implemented as a patch.** It changes no signature, no return value and no behaviour any
  consumer can observe except an extra log line, and it applies a pattern this very package already
  ships under an existing decision (`WarnUnsafeConfig`, `WarnMsg*` constants). Nothing was decided
  that was not already decided.
- **Reject — NOT done, and explicitly recommended for its own ADR.** Returning an error from
  `OpenSQLite` on `MaxOpenConns != 1` is a breaking change to a shipped public constructor. That is
  a real decision with a migration story, and it is not mine to take silently. It is recorded as an
  open question in ADR-0082 §2 rather than left in a transcript.

`OpenSQLite`'s contract is therefore unchanged: it still returns `(store, nil)` for a wide pool, and
a test asserts exactly that so a future edit cannot quietly upgrade the warning into a rejection.

### Why not a `DeploymentProfile` field

Triage noted `DeploymentProfile` has 5 fields and none is dialect-aware. It stays that way
deliberately: its own godoc says it is **"NOT introspected from the live system — the consumer
declares it"**. This check *is* introspected from a live handle, so putting it there would have
falsified the type's stated contract. A separate `WarnUnsafeSQLite(ctx, logger, db)` keeps the
distinction intact.

### Files changed

- `persistence/unsafe_config.go` — `WarnMsgSQLiteBusyTimeout` + `WarnUnsafeSQLite`.
- `persistence/sqlite.go` — `OpenSQLite` calls it (advisory, before `store.New`); godoc updated.
- `docs/adr/0082-sqlite-backend.md` §2 — the check, the measured justification, and the explicit
  statement that rejection is undecided and needs its own ADR.
- `persistence/sqlite_unsafe_test.go` (new).

### Observed RED (verbatim)

```
$ go test -count=1 -run 'TestWarnUnsafeSQLite|TestOpenSQLiteWarns|TestOpenSQLiteStaysSilent' ./persistence/
EXIT=1
# github.com/kartaladev/wrkflw/persistence_test [github.com/kartaladev/wrkflw/persistence.test]
persistence/sqlite_unsafe_test.go:70:44: undefined: persistence.WarnMsgSQLiteBusyTimeout
persistence/sqlite_unsafe_test.go:112:16: undefined: persistence.WarnUnsafeSQLite
… (8 undefined-symbol errors)
FAIL	github.com/kartaladev/wrkflw/persistence [build failed]
```

### GREEN

```
EXIT=0
--- PASS: TestOpenSQLiteWarnsOnUnsafePool (0.01s)
--- PASS: TestOpenSQLiteStaysSilentOnSafePool (0.00s)
--- PASS: TestWarnUnsafeSQLiteNilArgumentsDoNotPanic (0.00s)
--- PASS: TestWarnUnsafeSQLite (0.00s)
    --- PASS: …/wide_pool_without_busy_timeout_warns
    --- PASS: …/unlimited_pool_without_busy_timeout_warns
    --- PASS: …/wide_pool_WITH_busy_timeout_stays_silent
    --- PASS: …/single_connection_without_busy_timeout_stays_silent
```

### Mutation — the causal correction is load-bearing, so it was verified, not asserted

The whole point of this item's correction is that pool width **alone** is not the hazard. Deleting
the `busyTimeoutMS > 0` guard (i.e. implementing the naive "warn if pool > 1" check triage warns
against) must turn the safe case RED — and it does:

```
MUTATION EXIT=1
--- FAIL: TestWarnUnsafeSQLite/wide_pool_WITH_busy_timeout_stays_silent (0.00s)
    Error:    "…msg=\"SQLite pool allows more than one connection while PRAGMA busy_timeout is 0…\"
               max_open_conns=8 busy_timeout_ms=5000" should not contain …
    Messages: a safe configuration must warn nothing
```

Note `busy_timeout_ms=5000` in the mutated warning: the naive check would have reported a timeout of
5000 ms in a message asserting the timeout is 0. Restored via `cp` backup; `diff` clean.

### Fixture discipline

Per triage's ⚠, `dbtest.RunTestSQLite` is **not** used in this file — it applies WAL,
`busy_timeout(5000)` and `SetMaxOpenConns(1)`, i.e. it builds precisely the safe configuration, so
the unsafe-path assertions would have been structurally unable to fail. Each case opens its own
`*sql.DB` by hand with the exact pragma query and pool width under test.

### Premises checked, and one refinement found

- `persistence/sqlite.go`'s `OpenSQLite` body inspected neither property — TRUE before the fix.
- `DeploymentProfile` has exactly 5 fields, none dialect-aware — re-counted, TRUE.
- ⭐ **Refinement not in the item**: `db.SetMaxOpenConns(0)` means **unlimited**, and 0 is
  `database/sql`'s default. A check written as `maxOpen > 1` would therefore miss the *widest*
  possible pool — the out-of-the-box one. The implemented predicate is `maxOpen != 1`, and
  `unlimited pool without busy timeout warns` is a dedicated case covering it.
- `Option` is a type **alias** of the opaque `store.Option` (`persistence/persistence.go:106`), so
  `OpenSQLite` cannot read back a logger passed via `WithStoreLogger`. That is why the automatic
  call reports through `slog.Default()` and the logger is a parameter of the exported
  `WarnUnsafeSQLite` instead. Verified by reading the type, not assumed.

---

## 34 — `persistence` under the 85 % line-coverage floor — **DONE (86.8 %)**

### Coverage, and which number is which

⚠ Both numbers are quoted because a sibling item (backlog 20) turned out to be a raw-vs-filtered
confusion. **In `persistence` they are identical** — `scripts/coverage.sh` excludes generated
`*_mock.go` files and this package has none, so raw `go tool cover -func` and the filtered total
agree to the decimal. Triage said the same, and it was re-executed rather than inherited.

| point | raw (`go tool cover -func`) | filtered (`scripts/coverage.sh`) |
|---|---|---|
| **baseline**, pristine `main` @ `70a631e9` | **84.1 %** | **84.1 %** |
| after items 119/112/117/109 (before any 34 work) | 83.1 % | 83.1 % |
| **final** | **86.8 %** | **86.8 %** |

The baseline was measured in a **detached `git worktree` at HEAD**, not by stashing or reverting my
own tree — this session's edits were already in the working tree by the time the item arrived, and
`git checkout <path>` restores from the index and destroys uncommitted work. The worktree has been
removed; `git worktree list` shows only the primary tree.

Note the **dip to 83.1 %**: items 112/109 added production statements (the pool collector's
instrument-registration error branches) faster than they added covered ones. Worth recording because
it is the mechanism by which a package silently drifts under the floor — shipping a feature, not
neglecting tests.

⚠ `scripts/coverage.sh` only **reports**; its exit code proves nothing. The numbers above are read
from its output, and the test runs are judged by `EXIT=` on `go test` (`COV EXIT=0`).

### How it was closed — the advisory lock, not the option setters

Golang rule #8 says the 85 % is a floor, not a target, and that padding it while a hot path stays
uncovered is the anti-pattern. The four symbols at **0.0 %** that matter were the real hole:

| symbol | before | after |
|---|---|---|
| `scheduler_locker.go:112` `poolSchedulerLocker.Lock` | 0.0 % | covered |
| `scheduler_locker.go:137` `poolSchedulerLock.Unlock` | 0.0 % | covered |
| `scheduler_locker.go:52` `NewPostgresSchedulerLocker` | 0.0 % | covered |
| `scheduler_locker.go:68` `NewMySQLSchedulerLocker` | 0.0 % | covered |

⚠ **The pre-existing `persistence/scheduler_locker_test.go` is a cited-but-not-covering test**: it
exercises only `NewSchedulerLocker`, the *single-session* bridge, through an in-memory
`fakeDialectLocker`. A fake that returns `tryOK: false` on demand cannot observe whether a real
advisory lock is session-scoped or per-key — which is the entire multi-replica guarantee. That is
why the new file drives real contention against real databases.

The eight `MySQLWith…` option setters and `NewCallNotifier` are **deliberately still at 0.0 %** —
they are the "do not chase these" half of the item, and the floor is cleared without them.

### Files changed

- `persistence/scheduler_locker_db_test.go` (new) — Postgres and MySQL legs share one
  `assertSchedulerLockerContract` helper, so a divergence between dialects surfaces as a failure
  instead of an untested gap.
- `persistence/pool_stats_test.go` — `TestPoolStatsCollectorToleratesNilOptions`, closing the one
  0.0 % symbol this session itself introduced (`WithPoolStatsLogger`).

### What the lock tests assert

1. a free key is obtainable;
2. **contention** — a second session is refused with `ErrSchedulerLockNotObtained` (the assertion the
   exclusion guarantee rests on: if it passed, two replicas would fire the same timer);
3. **per-key control** — a *different* key stays obtainable while the first is held, so a locker that
   refused everything while any lock was held cannot pass;
4. release-then-reacquire, proving (2) is a lock and not a permanent wedge;
5. `TestMySQLSchedulerLockerAcquireFailure` — a closed handle must produce an error that is **not**
   `ErrSchedulerLockNotObtained`, because the scheduler treats that sentinel as "another replica has
   it" and would swallow an infrastructure outage in silence.

Docker is used only through `dbtest.RunTestDatabase` / `dbtest.RunTestMySQL` / `RunTestMySQLDSN`; no
ad-hoc container. There is no SQLite leg — SQLite omits the `Locker` capability (ADR-0082).

### Mutations

**Contention branch** — replaced `if !ok { closeConn(); return nil, ErrSchedulerLockNotObtained }`
with an unconditional success. **Both legs went RED:**

```
--- FAIL: TestMySQLSchedulerLockerContract (11.04s)
    Error:    Expected error with "workflow-persistence: scheduler advisory lock not obtained"
              in chain but got nil.
    Messages: a key held by another session must be refused with ErrSchedulerLockNotObtained
```

The Postgres leg reached the same assertion — its stack shows
`require.ErrorIs(... {0x0, 0x0} ...)` at `scheduler_locker_db_test.go:56`, i.e. a **nil** error
where the sentinel was required — and the run then hung to the 10-minute test timeout and panicked.
⭐ That hang is itself informative: the deleted branch also drops `closeConn()`, so every refused
`Lock` **leaks a pooled connection**. The `closeConn()` on the refusal path is load-bearing for pool
health, not just tidiness — worth knowing before anyone refactors that function.

**Nil-option guard** — deleting `if o != nil` from `newPoolStatsCollector` panics with
`invalid memory address or nil pointer dereference`, so the nil-tolerance test is not a
does-not-panic tautology.

Both restored from `cp` backups; `diff` clean in each case.

---

## Final verification (whole task)

```
$ go test -count=1 ./persistence/...            EXIT=0   (7 packages ok)
$ go build ./...                                EXIT=0
$ go test -race -count=1 -coverprofile ./persistence/   EXIT=0 → 86.8 % (raw == filtered)
$ golangci-lint run ./persistence/...           EXIT=0   0 issues
```

⚠ The lint run is **package-scoped**, not the repo-wide `golangci-lint run ./...` — it is a partial
result and must not be reported as the repo gate. Other agents were mid-flight in `engine`,
`runtime`, `runtime/monitor` and `internal/persistence/store` throughout this session; their
transient build breakages (`undefined: slog`, `undefined: clockwork`, `undefined: time`,
`buildTimerJob` arity) were waited out, never "fixed" from here.

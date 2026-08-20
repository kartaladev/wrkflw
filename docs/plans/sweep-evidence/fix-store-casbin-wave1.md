# Fix wave 1 — `internal/persistence/store` + `internal/authz/casbin`

Evidence record for the tier-1 sweep items assigned to this agent. One section per
item, written immediately after that item reached GREEN. Every claim below was
executed; observed output is pasted verbatim.

---

## Item 84 — the store layer reads the wall clock directly, against ADR-0138

**Status:** DONE

### Re-derived enumeration (Premise Discipline — counted again, not inherited)

`grep -rn "time\.Now()" internal/persistence/store/ --include="*.go" | grep -v "_test.go"`
returns **8 lines**, but one of them (`definitions.go:89`) is a **doc comment**, not
code. So the real count is **7 `time.Now()` call sites**:

| Site | Kind | Action |
|---|---|---|
| `store_core.go:83` (`Store.Create`) | **persisted** → `wrkflw_instances.updated_at` | threaded |
| `store_core.go:136` (`Store.Load`) | latency stopwatch, never persisted | left alone |
| `store_core.go:191` (`Store.Commit`) | latency stopwatch, never persisted | left alone |
| `store_core.go:223` (`Store.Commit`) | **persisted** → `wrkflw_instances.updated_at` | threaded |
| `definitions.go:98` (`PutDefinition`) | **persisted** → `wrkflw_definitions.created_at` | threaded |
| `dedup.go:86` (`Deduper.Seen`) | **persisted** → `wrkflw_processed_message.processed_at` | threaded |
| `chainlink.go:81` (`ChainLinkStore.Record`) | **persisted** → `wrkflw_chain_links.created_at` | threaded |

⇒ **5 persisted wall-clock sites**, exactly as triage re-counted (the backlog's
original enumeration, which missed `ChainLinkStore.Record` and `DefinitionStore`,
stays refuted).

**3 already-compliant types**, also confirmed by grep for `clockwork`:
`PgxNotifier` (`notifier_pgx.go:33`, option at `:55`), `Relay` (`relay.go:49`,
option at `:101`), `CallLinkStore` (`call_links.go:75`, option at `:50`).

The two stopwatches are deliberately **not** converted: they measure elapsed real
time for the `wrkflw_store_duration_seconds` histogram and would be frozen to 0 s
by a fake clock injected to make persisted values deterministic. This is stated in
the `Store.clk` doc comment so the exclusion is not mistaken for an oversight.

### Files changed

- `internal/persistence/store/store.go` — `Store.clk` field, `WithStoreClock`, default `clockwork.NewRealClock()` in `New`.
- `internal/persistence/store/store_core.go` — `Create` and `Commit` persisted stamps now `s.clk.Now().UTC()`.
- `internal/persistence/store/definitions.go` — `DefinitionStore.clk`, new `DefinitionOption` + `WithDefinitionClock`, `NewDefinitionStore` made variadic; stale godoc "created_at is written as time.Now().UTC()" corrected.
- `internal/persistence/store/dedup.go` — `Deduper.clk`, new `DeduperOption` + `WithDeduperClock`, `NewDeduper` made variadic.
- `internal/persistence/store/chainlink.go` — `ChainLinkStore.clk`, new `ChainLinkOption` + `WithChainLinkClock`, `NewChainLinkStore` made variadic.
- `internal/persistence/store/clock_injection_test.go` — **new**, `TestPersistedTimestampsUseInjectedClock`.

**Plain-constructor path preserved**: all four constructors take the clock as a
*variadic option* with a real-clock default, so every existing zero-option call
site compiles and behaves unchanged, and no consumer is forced to supply a clock
or adopt a DI container.

### The item-126 trap, verified rather than assumed

Probe run before writing the test:

```
NewFakeClock().Now() = 2026-08-20 07:59:56.669315 +0000 UTC
time.Now()          = 2026-08-20 07:59:56.669315 +0000 UTC
delta               = -417ns
```

Confirmed: `clockwork.NewFakeClock()` seeds from wall time, so a naive
"persisted == fake clock" assertion cannot discriminate. The test therefore uses
`clockwork.NewFakeClockAt(1999-03-04T05:06:07.890123456Z)` and asserts exact
equality. The reasoning is recorded in the test file so a future editor does not
"simplify" it back to `NewFakeClock()`.

### Observed RED (verbatim)

`go test -count=1 ./internal/persistence/store/... ; EXIT=1`

```
# github.com/kartaladev/wrkflw/internal/persistence/store_test [.../store.test]
internal/persistence/store/clock_injection_test.go:70:50: undefined: store.WithStoreClock
internal/persistence/store/clock_injection_test.go:85:50: undefined: store.WithStoreClock
internal/persistence/store/clock_injection_test.go:108:60: too many arguments in call to store.NewDefinitionStore
	have (*sql.DB, dialect.Dialect, unknown type)
	want (any, dialect.Dialect)
internal/persistence/store/clock_injection_test.go:108:66: undefined: store.WithDefinitionClock
internal/persistence/store/clock_injection_test.go:124:57: undefined: store.WithDeduperClock
internal/persistence/store/clock_injection_test.go:140:66: undefined: store.WithChainLinkClock
FAIL	github.com/kartaladev/wrkflw/internal/persistence/store [build failed]
```

### Mutation proof that the assertion discriminates

A compile-error RED proves the option was absent; it does **not** prove the
assertion can fail. So `store_core.go:83` was reverted to `time.Now().UTC()` and
the test re-run:

```
--- FAIL: TestPersistedTimestampsUseInjectedClock/Store.Create_stamps_updated_at_from_the_injected_clock
    Messages: updated_at must be the injected instant 1999-03-04 05:06:07.890123456 +0000 UTC,
              got 2026-08-20 08:06:04.053176 +0000 UTC
```

Production line restored from a `cp` backup; `diff` clean.

### GREEN

- `go test -count=1 -run '^TestPersistedTimestampsUseInjectedClock$' -v ./internal/persistence/store/...` → **EXIT=0**, all 5 subtests PASS.
- `go test -count=1 ./internal/persistence/store/...` (full package, Docker up → Postgres + MySQL + SQLite) → **EXIT=0**, `ok … 52.276s`.
- `go build ./...` → **EXIT=0**.

### False premises found

1. **Triage's fix sketch names a type that does not exist.** It says to give
   "`Store`, `DefinitionStore`, `DedupStore` and `ChainLinkStore`" a clock. There is
   no `DedupStore` in this package — the type is **`Deduper`** (`dedup.go:22`), so
   the option is `WithDeduperClock`. Cosmetic, but it is a factual claim about the
   codebase that is false as written.
2. **The `grep` count of 8 conflates code with a comment.** Triage says `time.Now()`
   "appears **8×** in non-test files"; one of the eight is prose in the
   `PutDefinition` godoc. The derived figure (5 persisted) is nevertheless correct,
   because that comment is not one of the two stopwatches subtracted — the
   arithmetic 8−2=6 would have been wrong, and 7−2=5 is right. The published total
   happens to land on the true value.
   That stale comment has now been corrected as part of this fix.

---

## Item 102 — a casbin cross-node policy-reload failure is silently swallowed

**Status:** DONE

### The defect, confirmed at the cited line

`internal/authz/casbin/db.go:97` read verbatim before the change:

```go
if err := w.SetUpdateCallback(func(string) { _ = enforcer.LoadPolicy() }); err != nil {
```

The triage citation is exact. Startup load does fail closed (the `SetWatcher` and
`SetUpdateCallback` errors immediately around it *are* returned) — that half is
unchanged by this fix, as instructed.

### Files changed

- `internal/authz/casbin/db.go` — new `newPolicyReloadCallback`; `DBConfig` gains optional `Logger *slog.Logger` and `MeterProvider metric.MeterProvider` (both nil-safe: nil falls back to `slog.Default()` / the OTel global provider); the watcher wiring now installs the instrumented callback.
- `internal/authz/casbin/export_test.go` — `NewPolicyReloadCallback` test hook, matching the package's existing export-for-black-box-test convention.
- `internal/authz/casbin/policy_reload_test.go` — **new**, two tests (see below).

A failing reload is now logged at ERROR (with the original error text, the
watcher channel, this node id and the origin node id) and increments
`wrkflw_authz_policy_reload_failures_total`.

### The failure mode was NOT changed — recommendation only

Enforcement still does **not** fail closed on a stale policy: the enforcer keeps
answering from the last successfully loaded policy, so a permission revoked on
another node can still return `Enforce=true, err=nil` here until a later reload
succeeds. That is preserved deliberately and documented in the callback's godoc.

> **Recommendation (NOT shipped, needs a decision):** the counter is now the
> alertable signal, but there is still no *automatic* protection. A fail-closed
> variant — or, softer, a staleness flag surfaced through a `HealthCheck` so the
> node can be pulled from rotation — trades a security exposure for an
> availability outage and is the operator's call, not a side effect of making the
> failure observable. Triage tiers this `D` and links it to item **106**
> (readiness surface); it should go there.

### Observed RED (verbatim)

`go test -count=1 ./internal/authz/casbin/... ; EXIT=1`

```
# github.com/kartaladev/wrkflw/internal/authz/casbin_test [.../casbin.test]
internal/authz/casbin/policy_reload_test.go:89:22: undefined: authzcasbin.NewPolicyReloadCallback
internal/authz/casbin/policy_reload_test.go:92:5: unknown field Logger in struct literal of type "…/internal/authz/casbin".DBConfig
internal/authz/casbin/policy_reload_test.go:93:5: unknown field MeterProvider in struct literal of type "…/internal/authz/casbin".DBConfig
FAIL	github.com/kartaladev/wrkflw/internal/authz/casbin [build failed]
```

A second, genuine RED appeared after the constructor existed — the assertion on
the error text failed because `slog`'s TextHandler escapes embedded quotes and
the sentinel contained them. Fixed in the test (the sentinel now has no quotes);
the log content was correct all along.

### The seam gap, found and closed

`TestPolicyReloadCallback` calls the exported constructor directly, so it is
**blind to the wiring**: revert `NewDBEnforcer` to the old swallowing closure and
that test stays green. A unit test of an extracted helper does not prove the
helper is reachable — the ADR-0179 lesson.

So `TestNewDBEnforcer_WatcherReloadFailureIsObservable` was added: real Postgres
(shared `dbtest.RunTestDatabase` helper), real watcher, `ListenReady`
synchronisation, `DROP TABLE casbin_rule` to break the reload for real, then a
peer `pg_notify` on the live channel.

**Mutation proof it covers the seam** — wiring reverted to
`func(string) { _ = enforcer.LoadPolicy() }`:

```
--- FAIL: TestNewDBEnforcer_WatcherReloadFailureIsObservable (16.68s)
        	Error:      	Condition never satisfied
FAIL	github.com/kartaladev/wrkflw/internal/authz/casbin	17.249s
```

`TestPolicyReloadCallback` passed throughout that mutation, exactly as predicted
— which is the whole reason the seam test had to exist. `db.go` restored from a
`cp` backup; `diff` clean.

### GREEN

- `go test -count=1 -race -run '^TestNewDBEnforcer_WatcherReloadFailureIsObservable$' ./internal/authz/casbin/...` → **EXIT=0** (no data race; the log buffer is mutex-guarded because the callback runs on the listener goroutine).
- `go test -count=1 ./internal/authz/casbin/...` (full package) → **EXIT=0**, `ok … 3.525s`.

### False premises found

None in the item-102 entry — this was, as triage said, the audit's most accurate
citation: the line number, the code text, and the startup-fails-closed
qualification all checked out unmodified.

---

## Item 118 — the same `SQLITE_BUSY` reaches callers under two identities

**Status:** DONE

### Re-derived enumeration — the count rotted a THIRD time

Counted again from source, not inherited. Every error return in `Store.Create`
(`store_core.go:64`) and `Store.Commit` (`:188`):

**`Create` — 9 error returns**

| branch | line | maps? | verdict |
|---|---|---|---|
| `JoinOrBegin` → `create: begin` | `:69` | ❌ raw | **FIXED** |
| `json.Marshal` → `marshal snapshot` | `:80` | ❌ raw | correct as-is (not a DB error) |
| `IsUniqueViolation` → `ErrInstanceExists` | `:99` | sentinel | correct as-is |
| instance `INSERT` fallthrough | `:101` | ❌ raw | **FIXED** (the item's headline) |
| `writeJournal` | `:105` | ✅ | — |
| `writeOutbox` | `:108` | ✅ | — |
| **`maybeNotify`** | `:111` | ❌ **raw** | **FIXED** ⭐ *nobody had listed this* |
| `insertCallLink` | `:116` | ✅ | — |
| `q.Commit` | `:121` | ✅ | — |

**`Commit` — 10 error returns**

| branch | line | maps? | verdict |
|---|---|---|---|
| `JoinOrBegin` → `commit: begin` | `:207` | ❌ raw | **FIXED** |
| `json.Marshal` | `:220` | ❌ raw | correct as-is (not a DB error) |
| `UPDATE` exec | `:238` (mapped `:236`) | ✅ | — |
| `res.RowsAffected()` | `:244` | ❌ raw | **FIXED** |
| `rows == 0` → `ErrConcurrentUpdate` | `:253` | sentinel | correct as-is |
| `writeJournal` / `writeOutbox` / `maybeNotify` / `flipCallLink` / `q.Commit` | `:261,266,271,278,285` | ✅ | — |

⇒ `mapConflict` call sites: **4 in `Create`, 6 in `Commit`**. This confirms
triage's correction (6, not the backlog's 8) and adds two of its own:

1. ⭐ **A fifth unmapped site nobody had listed: `Create`'s `maybeNotify` at
   `:111`.** `maybeNotify` issues a real `q.Exec` (`store_core.go:340`) inside the
   transaction, so it can fail with exactly the transient conflicts this item is
   about. `Commit`'s equivalent branch (`:269`) **already mapped**; `Create`'s did
   not. That asymmetry survived the backlog AND the triage re-count. The fix class
   is therefore **5 sites, not 4**.
2. **`Commit` has ten error returns, not the nine triage counted.** Triage's table
   omits the `rows == 0` → `ErrConcurrentUpdate` return at `:253`. Harmless — that
   return is deliberately correct — but the quantifier was wrong as written.

The `~93% / ~7%` split is inherited from the original probe and was **not**
re-derived here; it needs a contended run and is not load-bearing for this fix.
`ASSUMPTION (unverified)` as to the ratio; the *mechanism* is verified below.

### Files changed

- `internal/persistence/store/store_core.go` — 5 branches routed through `s.mapConflict`.
- `internal/persistence/store/conflict_mapping_test.go` — **new**, `TestStoreMapsDriverErrorsToConcurrentUpdate`.

`IsUniqueViolation` stays checked **before** `mapConflict` on the INSERT, so a
genuine duplicate instance is still `ErrInstanceExists` and never degrades into
"retry forever". A control case locks that in.

### Test construction

Triage's preferred deterministic form, realised as:

- a `conflictDialect` embedding a real `dialect.Dialect` and forcing
  `IsRetryableConflict` to a fixed answer — real contention (SQLITE_BUSY, PG 40001,
  MySQL 1213) is racy to reproduce, and the branch under test is *whether the error
  reaches `mapConflict` at all*, not how the classifier decides;
- real SQLite (`dbtest.RunTestSQLite`, **no container**) with the failure induced
  per case: dropped table (INSERT), closed DB (both `begin`s), an invalid injected
  NOTIFY statement (`maybeNotify`);
- a **minimal fake `database/sql` driver** for the `RowsAffected` case — no real
  driver can be made to fail there (they all compute the count locally and return
  a nil error), so that branch is otherwise unreachable from a test.

**Dialect scope, stated plainly:** the cases run on **SQLite only**. The branches
they exercise sit *above* the dialect seam and are shared verbatim by Postgres and
MySQL, so the fix is dialect-neutral by construction — but Postgres and MySQL are
**not re-verified per dialect** by this test. Labelled, not implied.

### Observed RED (verbatim)

`go test -count=1 -run '^TestStoreMapsDriverErrorsToConcurrentUpdate$' ; EXIT=1`

```
Messages: a retryable driver conflict must reach the caller as ErrConcurrentUpdate,
          got: workflow-store: commit: rows affected: driver: rows affected unavailable
Messages: ... got: workflow-store: notify outbox: SQL logic error: near "THIS": syntax error (1)
Messages: ... got: workflow-store: create: insert instance: SQL logic error: no such table: wrkflw_instances (1)
Messages: ... got: workflow-store: create: begin: workflow-database: begin sql tx: sql: database is closed
Messages: ... got: workflow-store: commit: begin: workflow-database: begin sql tx: sql: database is closed

--- FAIL: .../Commit:_RowsAffected_fallthrough
--- FAIL: .../Create:_begin_fallthrough
--- PASS: .../control:_a_non-retryable_error_passes_through_unchanged
--- PASS: .../control:_a_duplicate_instance_still_maps_to_ErrInstanceExists
--- FAIL: .../Commit:_begin_fallthrough
--- FAIL: .../Create:_maybeNotify_fallthrough
--- FAIL: .../Create:_instance_INSERT_fallthrough
```

All **5** mapped-path cases RED; both controls green before *and* after, which is
what a control is for.

### GREEN

`go test -count=1 -run '^TestStoreMapsDriverErrorsToConcurrentUpdate$' -v ./internal/persistence/store/...`
→ **EXIT=0**, all 7 subtests PASS.

### False premises found

1. ⭐ **The enumeration was incomplete a third time** — `Create`'s `maybeNotify`
   (`:111`) is a fifth unmapped DB path, missed by both the backlog and triage. A
   fix scoped to the four sites triage named would have left it leaking.
2. **`Commit` has 10 error returns, not 9** — triage's table omits the `rows == 0`
   sentinel return at `:253`.

---

## Item 125 — `store.Create` SIGSEGVs on a nil `AppliedStep.Trigger`

**Status:** DONE

### Located by symbol, and the line number settled

Triage gave `:100` in one place and `:101` in another. Located by symbol
(`func MarshalTrigger`) as instructed: the deref is **`trigger_codec.go:100`**,
the first statement of the function —

```go
env := triggerEnvelope{At: t.OccurredAt()}   // :100 — method call on a nil interface
```

The RED stack trace confirms it independently:
`store.MarshalTrigger({0x0, 0x0}) …/trigger_codec.go:100`. **`:100` is right,
`:101` is off by one.**

### Files changed

- `internal/persistence/store/trigger_codec.go` — two-line nil guard returning `workflow-store: marshal trigger: nil trigger`; godoc corrected to include the nil case and to say why the guard sits above the switch.
- `internal/persistence/store/trigger_nil_test.go` — **new**, `TestMarshalTrigger`.

Kept small as instructed: no codec redesign, no new exported sentinel (the
message matches the sibling `unhandled variant %T` error style in the same
function), no API change.

### Observed RED (verbatim, and it is a PANIC not an assertion)

```
--- FAIL: TestMarshalTrigger/nil_trigger_returns_an_error_instead_of_panicking (0.00s)
panic: runtime error: invalid memory address or nil pointer dereference [recovered, repanicked]
[signal SIGSEGV: segmentation violation code=0x2 addr=0x18 pc=0x103023908]
…
github.com/kartaladev/wrkflw/internal/persistence/store.MarshalTrigger({0x0, 0x0})
	/Users/zakyalvan/Documents/RND/wrkflw/internal/persistence/store/trigger_codec.go:100 +0x28
```

The assertion is `require.Error`, **not** `require.Panics` — per triage, asserting
the panic would pin the broken behaviour permanently. A control case ("a valid
trigger still marshals") sits beside it so the guard cannot be written to reject
everything.

### GREEN

`go test -count=1 -run '^TestMarshalTrigger$' -v ./internal/persistence/store/...`
→ **EXIT=0**, both subtests PASS.

### Triage's `ASSUMPTION (unverified)` about `UnmarshalTrigger` — RESOLVED by execution

Triage said to *check, not assume*, whether `UnmarshalTrigger` has the same
shape. Probed with a throwaway test, four input shapes, output pasted verbatim:

```
nil data, empty kind       -> trigger=<nil> err=workflow-store: unmarshal trigger "": unexpected end of JSON input
nil data, valid kind       -> trigger=<nil> err=workflow-store: unmarshal trigger "start_instance": unexpected end of JSON input
empty data, valid kind     -> trigger=<nil> err=workflow-store: unmarshal trigger "start_instance": unexpected end of JSON input
valid data, unknown kind   -> trigger=<nil> err=workflow-store: unmarshal trigger: unknown kind "no_such_kind"
```

⇒ **No symmetric defect.** `UnmarshalTrigger(kind string, data []byte)` takes
value types only and never dereferences a nil interface; all four shapes return a
descriptive error and no panic. **No fix and no test were added** — a test that is
green before any production change is vacuous by this repo's own standard. The
probe was deleted; the numbers above are the record.

### False premises found

1. **The cited line is `:100`, not `:101`.** Triage flagged its own inconsistency
   and said to locate by symbol; doing so resolves it to `:100`, corroborated by
   the panic's stack frame.
2. **`UnmarshalTrigger` does *not* share the defect** — triage's explicitly
   labelled assumption, now executed and closed negative.

---

## Final verification (all four items)

| gate | command | result |
|---|---|---|
| internal suite | `go test -count=1 ./internal/... > /tmp/int-final.log 2>&1` | **EXIT=0** — 9 packages ok, incl. `internal/persistence/store` (82.1 s, all 3 dialects) and `internal/authz/casbin` (4.4 s) |
| build | `go build ./... > /tmp/build.log 2>&1` | **EXIT=0** |
| lint (**partial, scoped**) | `golangci-lint run ./internal/persistence/store/... ./internal/authz/casbin/...` | **EXIT=0, 0 issues** |

⚠ The lint run is **package-scoped, not repo-wide** — it is a partial result and
is labelled as one. A repo-wide run was not attempted because other agents were
mid-edit in `engine/`, `runtime/monitor/`, `internal/dbtest/` and `scheduler/`
throughout this session (three of their in-flight compile breaks were observed
and waited out); a repo-wide result would have described their work, not this one.

Docker was up and used only through the shared `dbtest` helpers
(`RunTestSQLite` — pure Go, no container — for every new test except the casbin
seam test, which uses `dbtest.RunTestDatabase`). No ad-hoc container was created.

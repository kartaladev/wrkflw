# Fix — `/code-review` finding 4 (MEDIUM): the new clock options are unreachable by any consumer

Branch: `feat/backlog-sweep-small-tier` (main working tree, uncommitted — the controller folds with `--amend`).
Date: 2026-08-20.

## The finding, restated

Commit `75d0cc7d` added four ADR-0138 clock options on `internal/persistence/store`
(`WithStoreClock`, `WithDefinitionClock`, `WithDeduperClock`, `WithChainLinkClock`). The public
`persistence` facade forwarded none of them, and three of the four constructors that would carry
them took no options at all. `store.WithStoreClock`'s own doc comment — *"Inject a
`clockwork.FakeClock` in tests for deterministic persisted timestamps"* — was therefore a promise
no consumer outside the module could keep.

## Decision: make the promise true, with facade-OWNED option types

Chosen over narrowing the doc comment. A consumer-injectable clock is exactly what ADR-0138 asks
for and what every sibling package already offers (`runtime.Option`, `runtime/monitor.Option`,
`calllink.CallNotifierOption`, `WithCallLinkClock`, `WithRelayClock`). Narrowing the doc would
have made the module *less* testable by its own consumers to avoid writing five forwarders.

The facade now defines its **own** option types instead of aliasing the internal ones:

| Before | After |
|---|---|
| `type Option = store.Option` | `type Option func(*storeConfig)` |
| `type CallLinkOption = store.CallLinkOption` | `type CallLinkOption func(*callLinkConfig)` |
| `type MySQLOption = store.Option` | `type MySQLOption = Option` |
| `type MySQLCallLinkOption = store.CallLinkOption` | `type MySQLCallLinkOption = CallLinkOption` |
| `type SQLiteCallLinkOption = store.CallLinkOption` | `type SQLiteCallLinkOption = CallLinkOption` |
| — | `type DefinitionOption func(*definitionConfig)` |
| — | `type DeduperOption func(*deduperConfig)` |
| — | `type ChainLinkOption func(*chainLinkConfig)` |

Merely adding forwarders on top of the aliases would have moved the leak, not closed it: `go doc
persistence.Option` printed `type Option = store.Option`, publishing an `internal/` type as the
public contract. Verified after the change — no `store.*` type appears in any exported signature:

```
$ go doc ./persistence Option
type Option func(*storeConfig)
$ go doc ./persistence | grep -E '^type (Option|.*Option)'
type CallLinkOption func(*callLinkConfig)
type ChainLinkOption func(*chainLinkConfig)
type DeduperOption func(*deduperConfig)
type DefinitionOption func(*definitionConfig)
type MySQLCallLinkOption = CallLinkOption
type MySQLOption = Option
type Option func(*storeConfig)
type SQLiteCallLinkOption = CallLinkOption
```

The per-family fold (`buildStoreOptions` etc.) mirrors the `relayConfig` fold that already lived
in `persistence.go`, **including its absence of a nil-option guard**: a nil option panics on
apply, exactly as it did while these types were aliases. No behaviour change, no untested
defensive branch.

## Decision: the three no-opts constructors

All three — and their Postgres/MySQL siblings, nine constructors in total — now take a variadic
option parameter. `NewSQLiteDeduper` was **brand new in this same commit** (backlog 119), so its
signature had never been published; getting it right now is free, and a later `opts ...` addition
would be a public-API break. Existing zero-option call sites compile unchanged.

- `NewDeduper` / `NewMySQLDeduper` / `NewSQLiteDeduper` → `opts ...DeduperOption`
- `NewDefinitionStore` / `NewMySQLDefinitionStore` / `NewSQLiteDefinitionStore` → `opts ...DefinitionOption`
- `NewChainLinkStore` / `NewMySQLChainLinkStore` / `NewSQLiteChainLinkStore` → `opts ...ChainLinkOption`

### Sub-decision: no `MySQLWithStoreClock`

Briefly added, then removed. `MySQLOption` is an alias of `Option`, so `persistence.WithStoreClock`
already configures a MySQL store; the `MySQLWith*` set is a naming mirror, not a separate surface,
and it has never been exhaustive (`WithOutboxNotify` has no mirror either). Two reasons to drop it:
it would have been public surface with no test, and — disclosed per CLAUDE.md's self-audit — it was
written without a preceding red state. `MySQLOption`'s doc comment now states explicitly that every
`With…` constructor in `options.go` applies to MySQL, and a comment where the mirror would have sat
says why there isn't one.

## RED — observed, not assumed

`persistence/clock_injection_test.go` (new, package `persistence_test`, black-box) is the consumer
counterpart to the in-module `internal/persistence/store/clock_injection_test.go`. Four cases, one
per clock option, each pinning `clockwork.NewFakeClockAt(1998-07-12T13:14:15.16017018Z)` and
asserting exact equality on the persisted column.

The 1998 instant is load-bearing: `clockwork.NewFakeClock()` seeds from wall time, so a fake-clock
assertion written against it passes just as well against code that still calls `time.Now()`.

```
$ go test -count=1 ./persistence/... ./internal/persistence/store/... > /tmp/p.log 2>&1; echo "EXIT=$?"
EXIT=1

# github.com/kartaladev/wrkflw/persistence_test [github.com/kartaladev/wrkflw/persistence.test]
persistence/clock_injection_test.go:74:67: undefined: persistence.WithStoreClock
persistence/clock_injection_test.go:86:57: too many arguments in call to persistence.NewSQLiteDefinitionStore
	have (*sql.DB, unknown type)
	want (*sql.DB)
persistence/clock_injection_test.go:86:69: undefined: persistence.WithDefinitionClock
persistence/clock_injection_test.go:99:48: too many arguments in call to persistence.NewSQLiteDeduper
	have (*sql.DB, unknown type)
	want (*sql.DB)
persistence/clock_injection_test.go:99:60: undefined: persistence.WithDeduperClock
persistence/clock_injection_test.go:112:57: too many arguments in call to persistence.NewSQLiteChainLinkStore
	have (*sql.DB, unknown type)
	want (*sql.DB)
persistence/clock_injection_test.go:112:69: undefined: persistence.WithChainLinkClock
FAIL	github.com/kartaladev/wrkflw/persistence [build failed]
```

That is the finding stated as a compile error: seven diagnostics, one per unreachable symbol or
missing parameter.

## GREEN, and the mutation that proves the assertions can fail

```
$ go test -count=1 ./persistence/... ./internal/persistence/store/... ; echo EXIT=$?
EXIT=0
ok  github.com/kartaladev/wrkflw/persistence            18.644s
ok  github.com/kartaladev/wrkflw/internal/persistence/store  46.609s
```

Confirmed the four subtests actually *ran* (a `-run` filter on a nonexistent name exits 0):

```
--- PASS: TestFacadeClockOptionsReachPersistedTimestamps (0.00s)
=== RUN .../OpenSQLite_+_WithStoreClock_stamps_instances.updated_at
=== RUN .../NewSQLiteDefinitionStore_+_WithDefinitionClock_stamps_definitions.created_at
=== RUN .../NewSQLiteDeduper_+_WithDeduperClock_stamps_processed_message.processed_at
=== RUN .../NewSQLiteChainLinkStore_+_WithChainLinkClock_stamps_chain_links.created_at
```

A compile-error RED proves the symbols were absent; it does **not** prove the new plumbing
actually threads the option through. Mutation: `buildStoreOptions`, `buildDefinitionOptions`,
`buildDeduperOptions` and `buildChainLinkOptions` each made to `return nil` (options collected,
then discarded). All four subtests failed, each reading back the real wall clock:

```
--- FAIL: .../OpenSQLite_+_WithStoreClock_stamps_instances.updated_at
    updated_at must be the injected instant 1998-07-12 13:14:15.16017018 +0000 UTC,
    got 2026-08-20 10:48:02.750869 +0000 UTC
    (…same shape for created_at ×2 and processed_at)
```

Restored with `cp /tmp/options.go.bak persistence/options.go` (**not** `git checkout` — the tree
holds several agents' uncommitted work); `diff` against the backup is empty, `go build ./...` OK.

## Files changed

- `persistence/options.go` — **new**. The five facade-owned option families, their folds, and
  eleven `With…` constructors including the four clock options.
- `persistence/clock_injection_test.go` — **new**. The four-case consumer-perspective table.
- `persistence/persistence.go` — option/`CallLinkOption` declarations moved to `options.go`;
  `OpenPostgres`, `NewCallLinkStore` fold; `NewDefinitionStore`, `NewChainLinkStore` gain options.
- `persistence/mysql.go` — `MySQLOption`/`MySQLCallLinkOption` re-aliased to the facade types;
  `OpenMySQL`, `NewMySQLCallLinkStore` fold; `NewMySQLDefinitionStore`, `NewMySQLChainLinkStore`
  gain options; header comment corrected; the no-`MySQLWithStoreClock` rationale recorded.
- `persistence/sqlite.go` — `SQLiteCallLinkOption` re-aliased; `OpenSQLite`,
  `NewSQLiteCallLinkStore` fold; `NewSQLiteDefinitionStore`, `NewSQLiteChainLinkStore` gain options.
- `persistence/dedup.go` — all three deduper constructors gain `opts ...DeduperOption`.
- `internal/persistence/store/{store,definitions,dedup,chainlink}.go` — doc-only. Each of the four
  clock options now says it is reachable off-module only through its facade forwarder, so the next
  option added here does not repeat the omission.

## Verification

| Check | Result |
|---|---|
| `go build ./...` | EXIT=0 |
| `go vet ./...` (compiles Docker-only test packages — proves no hidden consumer broke) | EXIT=0 |
| `go test -count=1 ./persistence/... ./internal/persistence/store/...` | EXIT=0 |
| `golangci-lint run ./persistence/... ./internal/persistence/store/...` | EXIT=0, 0 issues |
| `go test -race -coverprofile ./persistence/` | **87.5 %** (from 86.8 % as stated in the review brief) |

Docker daemon probed (`docker info`, EXIT=0) and used, so the Postgres/MySQL facade tests ran.
Every new function in `options.go` and every touched constructor reports **100.0 %** in
`go tool cover -func`. Package-scoped lint only — the repo-wide `golangci-lint run ./...` is the
controller's gate.

## Still open — NOT fixed here

**Backlog item 128** (`persistence.NewSchedulerLocker(dl dialect.Locker)` exposing the internal
`dialect.Locker` in an exported signature) is **untouched and still open**. It is the same
internal-type-leak class but a different symbol, and it is scheduled for bundle B6.
`persistence/scheduler_locker.go` was not modified.

## Note for the controller

`CHANGELOG.md:882` (working tree, another agent's edit) documents
`persistence.NewSQLiteDeduper(db *sql.DB, opts ...DeduperOption) (Deduper, error)`. That signature
was **false when written** — the constructor took no options — and is **true as of this change**.
The two edits are now consistent; no CHANGELOG edit was made from here (docs are another agent's).

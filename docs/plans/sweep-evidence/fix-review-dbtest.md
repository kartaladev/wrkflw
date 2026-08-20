# /code-review HIGH findings 1–3: shared-server per-test database collision

Branch `feat/backlog-sweep-small-tier`, main working tree, **not committed** (the
controller folds this into the existing commit with `--amend`).

All three findings share one root cause: blocker 7 moved every package's test
binary onto ONE database server, but the per-test database name stayed
`wrkflw_test_<n>` where `<n>` comes from a **package-level (per-process)** counter.
`go test ./...` runs up to `-p`=GOMAXPROCS binaries concurrently, and every one of
them starts that counter at 1.

## Files changed

| File | Change |
|---|---|
| `internal/dbtest/dbname.go` | **new** — `testDBPrefix`, `processTag`, `nextTestDBName`, `ownedTestDBName`; one counter now serves both engines |
| `internal/dbtest/dbname_test.go` | **new** — the subprocess collision test, the length/legality budget, in-process uniqueness, the drop guard's table |
| `internal/dbtest/export_test.go` | exports `NextTestDBNameForTest`, `OwnedTestDBNameForTest`, `ProcessTagForTest` |
| `internal/dbtest/postgres.go` | uses `nextTestDBName()`; `DROP … WITH (FORCE)` goes through `ownedTestDBName`; `dbCounter` removed; doc updated |
| `internal/dbtest/mysql.go` | uses `nextTestDBName()`; **`CREATE DATABASE IF NOT EXISTS` → `CREATE DATABASE`**; guarded drop; `mysqlDBCounter` removed |
| `internal/dbtest/dsn.go` | doc: one namespace ⇒ per-process names; corrected boot count |
| `.github/workflows/ci.yml` | false "never a correctness dependency" comment replaced; corrected boot count |
| `scripts/testdb.sh` | `PG_MAX_CONNECTIONS` justification re-derived (value **unchanged**, see below); "nothing requires this script" corrected; usage `sed` range fixed |

## Observed RED — finding 1 + 2 (the name)

`go test -count=1 -run 'TestPerTestDatabaseName' -v ./internal/dbtest` → `EXIT=1`.
Two **real, concurrent** child `go test` processes each printed the same three
names:

```
    Messages:   	two concurrent test binaries generated the same per-test database name "wrkflw_test_1";
                	on one shared server they would fight over the same database
                	child 0: [wrkflw_test_1 wrkflw_test_2 wrkflw_test_3]
                	child 1: [wrkflw_test_1 wrkflw_test_2 wrkflw_test_3]
--- FAIL: TestPerTestDatabaseNamesAreDisjointAcrossProcesses (1.65s)
```

This is deterministic — it does not depend on winning a race — because per-process
identity, not timing, is the missing ingredient. After the fix the same command is
`EXIT=0`.

### What each engine then does — executed against the live shared servers

```
== PG: two processes creating the same name ==
CREATE DATABASE
ERROR:  database "wrkflw_test_1" already exists          <- finding 1: the loser's require.NoError fails
== PG: the loser's cleanup drops the winner's live database ==
DROP DATABASE                                            <- finding 1: WITH (FORCE) severs live connections
== MySQL: IF NOT EXISTS silently succeeds twice ==
both CREATE IF NOT EXISTS succeeded                      <- finding 2: silent corruption, two packages one DB
== MySQL: plain CREATE DATABASE fails loudly ==
ERROR 1007 (HY000): Can't create database 'wrkflw_test_1'; database exists
```

The last line is why `IF NOT EXISTS` is gone: with process-unique names a
collision should be impossible, and if it ever happens again it must fail loudly
rather than merge two binaries into one database.

## Observed RED — the drop guard

`go test -count=1 -run 'TestOwnedTestDBName' -v ./internal/dbtest` → `EXIT=1`:

```
./dbname_test.go:180:16: undefined: dbtest.ProcessTagForTest
./dbname_test.go:231:24: undefined: dbtest.OwnedTestDBNameForTest
FAIL	github.com/kartaladev/wrkflw/internal/dbtest [build failed]
```

The table fails if the guard is deleted **or weakened to a bare `wrkflw_test_`
prefix check**: two of its cases (`wrkflw_test_1`, and a name carrying another
process's tag) start with that prefix and must still be refused.

## Naming scheme and length budget

```
wrkflw_test_p<pid>_<12 hex>_<counter>
e.g. wrkflw_test_p25931_4cec8bab1607_1   (34 bytes)
```

* **PID** separates processes alive at the same instant on one host — exactly the
  `go test ./...` case, where the OS guarantees distinct PIDs.
* **6 crypto/rand bytes** separate processes on different hosts or in different PID
  namespaces pointed at the same server, and cover PID reuse after exit.
* **counter** keeps names unique within a process (unchanged property).

Worst case: `wrkflw_test_` (12) + `p` + 7-digit PID (8) + `_` + 12 hex (13) + `_` +
19-digit int64 counter (20) = **53 bytes**. PostgreSQL truncates identifiers at 63
bytes *silently* (which would merge two databases); MySQL rejects a database name
over 64 characters. Both bounds hold with margin, and
`TestPerTestDatabaseNameFitsBothEngines` asserts the 63-byte bound and
`^[a-z][a-z0-9_]*$` (lower-case only: PostgreSQL folds unquoted identifiers, and
the name is also spliced verbatim into the DSN path).

Collision argument: two databases collide only if two processes share a PID **and**
6 random bytes. Concurrent processes on one host cannot share a PID; across hosts
the random half gives 2^48.

## Finding 4 — `PG_MAX_CONNECTIONS=300`: kept, with the numbers

* Worst case on a 4-vCPU CI runner: `-p`=GOMAXPROCS binaries × `-parallel`=GOMAXPROCS
  tests × `perTestMaxConns`=8, plus one 4-connection admin pool per binary:
  `4 × 4 × 8 + 4 × 4 = 144` connection **slots** — under 300.
* Measured 2026-08-20 on a 14-core machine, `go test -race` over
  `casbinauthz`, `eventing`, `internal/authz/casbin`, `internal/database/...`,
  `scheduler/...`, `runtime/...` against the shared server, sampling
  `pg_stat_activity` 233 times: **peak 8 concurrent client backends, peak 2
  concurrent per-test databases**. `pgxpool.MaxConns` is a lazy cap, not a
  preallocation, so real demand sits far below the arithmetic bound.
* Therefore 300 is not the thing the shared server broke; it was left alone and its
  comment now carries the derivation instead of an assumption. Raising it costs
  shared memory for slots the lazy pools never open.

## Boot count correction (found while re-deriving)

Four comments introduced by this same commit said "**12** Postgres boots". Counting
the packages whose own `*_test.go` files actually call the helper gives **10**
(the 12 came from grep hits that included `internal/dbtest`'s own definition and a
`transporttest/harness.go` comment that merely mentions `RunTestDatabase`). MySQL's
**7** is correct. Corrected in `dsn.go`, `postgres.go`, `ci.yml`, `testdb.sh`.

## Verification run

| Command | Result |
|---|---|
| `go test -count=1 -run 'TestPerTestDatabaseName\|TestOwnedTestDBName' -v ./internal/dbtest` | `EXIT=0` |
| `go test -count=1 -v ./internal/dbtest/...` **with both DSNs exported** (the CI configuration) | `EXIT=0`; `TestRunTestMySQL_PingsSuccessfully` / `_AutoMigrates` PASS on the shared server |
| `go test -count=1 -race ./internal/dbtest/...` with **both variables unset** (the container fallback) | `EXIT=0` (26.6s) |
| `go test -count=1 -race ./casbinauthz/... ./eventing/... ./internal/authz/casbin/... ./internal/database/... ./scheduler/... ./runtime/...` with both DSNs exported — 4+ Postgres/MySQL packages **concurrent on one server** | `EXIT=0`, 20 `ok` |
| `select datname from pg_database where datname like 'wrkflw\_test\_%'` after the run | **empty** — every process dropped exactly what it created, nothing leaked |
| same count on both engines after the final `-race` run (`pg_database`, `information_schema.schemata`) | `0` and `0` |
| `golangci-lint run ./internal/dbtest/...` | `EXIT=0`, 0 issues (**package-scoped — not the repo-wide gate**) |
| `python3 -c "yaml.safe_load(open('.github/workflows/ci.yml'))"` | YAML OK |
| `bash scripts/testdb.sh --help` / `status` | render correctly; the usage block no longer truncates mid-sentence |

**Unexecuted claim, labelled:** ASSUMPTION (unverified) — that a *real* `go test ./...`
under CI's 4-vCPU `-p 4` would have hit the collision. What was executed is the
name generator producing identical names in two concurrent processes, and both
engines' response to that name being created twice. The end-to-end CI failure was
not reproduced.

## Note for the controller

Two containers are left RUNNING (`wrkflw-testdb-postgres`, `wrkflw-testdb-mysql`)
so the Verification coverage run can reuse them; `bash scripts/testdb.sh down`
removes them.

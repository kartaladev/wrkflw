## Why

kartaladev/wrkflw#169 reports tests that fail intermittently when the whole tree runs from several worktrees at once and pass in isolation. A false red under load costs a contributor a rerun. Worse, a harness that reads exit status scores it as a real result. The issue asks for a deterministic reproduction of the mechanism before any fix, and re-measurement shows the reported failures are two separate mechanisms. One, a MySQL container boot missing its 60 s startup deadline, reproduced in every one of four concurrent whole-tree runs; the other, in the HTTP tests, did not reproduce.

## What Changes

Every premise below was re-measured on `main` (`4763d36e`) before it was listed.

- **kartaladev/wrkflw#169 (absorbed, premises partly stale).** The change reproduces each mechanism deterministically, captures the natural failure text, then applies the remedy the maintainer chooses (design Q1, Q2). It adds no library behavior.

**Measured state (re-verified, not inherited)**
- The three named tests are, at HEAD: `TestDisabledCapSkipsBothChecks` (`transport/http/fiber/bodylimit_test.go:409`), `TestParity_MaxBodyBytes_Disabled` (`transport/http/parity/parity_test.go:1184`), and no single test for the third. The third entry names packages, not a test: `internal/persistence/store` and `internal/atrest`, "surfacing `internal/dbtest`'s shared MySQL container".
- **Stale: one mechanism.** The two HTTP tests build an in-memory harness (`internal/transporttest.NewHarness`, an in-memory instance store). `go list -test -deps` shows that neither `transport/http/fiber`, `transport/http/parity` nor `internal/atrest` links `internal/dbtest` or testcontainers. They cannot emit the MySQL signature, so the issue comment's attribution of that signature to `transport/http/fiber` is a misattribution. `internal/atrest` is not a container consumer either; the failing tests are `TestAtRest*LiveIntrospection_PostgresAndMySQL` in `internal/persistence/store/atrest_crosscheck_test.go`, which the issue appears to have filed under the package their name suggests.
- **Stale: "shared MySQL container".** Locally each test binary boots its own container (package-level `sync.Once`). Seven packages call `RunTestMySQL*`, so five worktrees can boot up to 35 MySQL containers at once on one Docker host. CI exports `WRKFLW_TEST_MYSQL_DSN` via `scripts/testdb.sh` and never runs this path.
- **Mechanism A (reproduced under induced load, not yet deterministically).** The signature `"port: 3306  MySQL Community Server" matched 0 times, expected 1` followed by `context deadline exceeded` is the startup wait of `testcontainers-go/modules/mysql` v0.44.0, whose deadline is the 60 s default. Four concurrent `GOTOOLCHAIN=go1.26.9 go test -race -count=1 ./...` runs on this host (14 CPUs, 16 GB, Docker 29.8.0; no `WRKFLW_TEST_*_DSN`) all exited 1 with it: 628 occurrences. `internal/persistence/store`, `persistence` and `runtime` failed in 4 of 4 runs and `scheduler/internal/gocron/myelector` in 3 of 4. `internal/dbtest`, `internal/database` and `scheduler` passed in all four. The captured container log shows mysqld still at `Initializing database files` when the 60 s expired, not exited or OOM-killed. A lone `mysql:8.0` boot to that log line took 5 s; with `--cpus 0.1` 47 s; with `--cpus 0.05` 182 s. That throttle is the deterministic reproduction task 1.1 builds.
- **Mechanism B (hypothesis, not reproduced).** Both HTTP tests send 2 MiB through fiber v3.5.0 `app.Test`, whose default is a 1 s deadline that fails the call on timeout. All four `app.Test` call sites use the default, and `scripts/check-test-timeout.sh` cannot see it. The issue never quoted the HTTP failure text, so this matches the shape of the failures only.
- **Mechanism B did not reproduce.** In the same four runs `transport/http/fiber` and `transport/http/parity` passed every time, including both named HTTP tests. It stays a hypothesis; at a ~1-in-3 rate four clean runs disprove nothing, and task 1.3 decides whether it is kept.
- **The issue's discriminator, applied.** Across the four runs the failing set moved by one package (`myelector`), and every failure carried the one container-boot signature. That fits contention for the Docker host, not a code defect in the failing tests.
- The baseline's full `-race` run passed 64/64 packages, and its audit cited `TestParity_MaxBodyBytes_Disabled` as a flaky test. Both are consistent with a load-dependent mechanism and prove nothing about which.

**Out of scope**
- The sizing rule for real-I/O deadlines and the shared constant's convention. Owned by `size-io-test-deadlines` (kartaladev/wrkflw#98); this change supplies the reproduction that rule waits for.
- Extending `scripts/check-test-timeout.sh` to count `app.Test` deadlines. Owned by `harden-ci-guard-wiring`.
- The other contention classes the issue lists: golangci-lint's temp-dir lock (kartaladev/wrkflw#146, fixed), toolchain extraction under `GOTOOLCHAIN=auto` (the issue's sibling ticket) and process or fd exhaustion. None is named by a failing test here.
- Moving `internal/dbtest`. Owned by `split-persistence-modules` (kartaladev/wrkflw#39).

**Decisions needed before group 2 of the tasks** (design, Open Questions): Q1, the remedy for mechanism A (named startup timeout, cross-process boot lock, shared-server path for parallel worktrees, or a `-p` bound); Q2, the remedy for mechanism B if confirmed. The issue leaves both open and no comment decides them.

**BREAKING:** none. **Persisted formats:** not affected. **Default security posture:** not affected. **Consumer go.mod:** not affected. `internal/dbtest` is internal and test-only.

**Coordination**
- Size: S
- Depends on: nothing
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. The change is test hygiene with no observable library behavior, so `skip_specs: true` is set.

## Impact

- **Tests and test support:** `internal/dbtest/mysql.go` (a boot seam and, per Q1, a named startup timeout), new reproduction tests in `internal/dbtest`, `transport/http/fiber` and `transport/http/parity`, and, per Q2, an `app.Test` helper in each of those two packages.
- **Docs:** per Q1, `CONTRIBUTING.md` and the `internal/dbtest` package comment; after `size-io-test-deadlines` lands, `docs/agents/test-deadlines.md`.
- **Not affected:** exported identifiers, persisted formats, the default security posture, any consumer's `go.mod`, CI workflow files.

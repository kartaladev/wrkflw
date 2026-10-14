## Context

See proposal.md, *Why* and *Measured state*. The issue names three tests. Re-measurement shows they are two mechanisms, and neither has a deterministic reproduction yet.

**Mechanism A: a MySQL container boot that misses its startup deadline.** The signature `"port: 3306  MySQL Community Server" matched 0 times` is produced by the log wait strategy that `testcontainers-go/modules/mysql` v0.44.0 installs (`wait.ForLog("port: 3306  MySQL Community Server")`, no explicit timeout). That strategy's default deadline is 60 s (`wait.defaultStartupTimeout`). Read off the v0.44.0 source, the "matched 0 times" text is returned only when the deadline expires while the container is still running. A container that exited or was OOM-killed returns `container exited with code N` or `container crashed with out-of-memory (OOMKilled)` instead. Measured: four concurrent whole-tree runs on one host all failed with that text, and the captured container log showed mysqld still at `Initializing database files` when the 60 s expired (proposal, *Measured state*). A load run is not a deterministic reproduction, so the first task still builds one.

- The text comes only from the testcontainers path. `internal/dbtest` boots one MySQL container per test binary (a package-level `sync.Once`), so a local whole-tree run boots one per package that calls `RunTestMySQL*`. Seven packages do: `internal/database`, `internal/dbtest`, `internal/persistence/store`, `persistence`, `runtime`, `scheduler` and `scheduler/internal/gocron/myelector`. Five parallel worktrees therefore boot up to 35 MySQL containers and 50 PostgreSQL containers on one Docker host. The contended resource is the Docker host, not one shared container.
- CI never takes this path. `.github/workflows/ci.yml` runs `scripts/testdb.sh up` and exports `WRKFLW_TEST_MYSQL_DSN`, so every binary adopts one long-lived server and no wait strategy runs. That explains why the flake is local-only.
- The PostgreSQL helper sets `WithStartupTimeout(60*time.Second)` explicitly. That equals the default, so it is no more tolerant than the MySQL helper.

**Mechanism B: fiber's implicit 1 s `app.Test` deadline (hypothesis).** Both HTTP tests send a 2 MiB body through fiber v3.5.0 `app.Test`. Called with no `TestConfig`, it uses `Timeout: time.Second, FailOnTimeout: true`, so a request slower than 1 s fails the test with a timeout error rather than a status. All four `app.Test` call sites in the repo use that default (`transport/http/fiber/fiber_test.go:49` and `transport/http/parity/parity_test.go:213`, `:258` and `:887`). Neither `scripts/check-test-timeout.sh` nor `docs/agents/test-deadlines.md` can see the deadline, because both count raw `time.After` clauses only. The issue never quoted the HTTP failure text, and both HTTP packages passed in all four measured load runs, so this mechanism is a candidate that matches the shape of the failures, not a finding.

## Goals / Non-Goals

**Goals:**
- Turn each mechanism into a test that fails deterministically on one machine, with no reliance on ambient load. That is the precondition for any fix, and it gives `size-io-test-deadlines` the evidence its sizing rule waits for.
- Apply the issue's discriminator ("a code defect yields a deterministic set; container contention does not") as an explicit check: after the fix, a repeat of the induced-load run yields no failures, or a fixed set.

**Non-Goals:**
- No change to library behavior. The change touches `_test.go` files, `internal/dbtest` and contributor docs only.
- No statistical sampling to establish a rate. The issue itself warns that a 1-in-3 rate is machine-dependent and that three clean runs disprove nothing.

## Decisions

### D1. Reproduce by inducing the mechanism, not by sampling load

This follows the #156 precedent cited in the issue, where removing the overlap failed the test on the first run.
- **Mechanism A** is reproduced by booting the MySQL container through the helper's own option set with its CPU quota throttled (a `HostConfig` `NanoCPUs` limit), so that initialisation cannot finish inside the startup deadline. Measured with plain `docker run mysql:8.0` to the ready log line: 5 s unthrottled, 47 s at 0.1 CPU (inside the deadline, so too weak), 182 s at 0.05 CPU (outside it). The red test asserts the boot succeeds; it fails with the byte-identical signature.
- **Mechanism B** is reproduced by wrapping the harness `service.Service` in a decorator that adds a fixed delay above 1 s, then driving it through the existing `appDo` / parity helpers. The red test asserts the handler's status comes back; it fails with fiber's timeout error.

Rejected option: **N concurrent whole-tree runs until a failure appears.** Measured in this change's proposal: four concurrent `go test -race -count=1 ./...` runs on a 14-CPU host reproduced mechanism A in 4 of 4 runs but mechanism B in none. Load is host-dependent, and a fix verified only against it cannot tell "fixed" from "this host was less busy". It is kept as the confirming check (task 1.3, 3.1), not as the red test.

### D2. Each mechanism's reproduction is gated on the natural failure text

A deterministic reproduction proves a mechanism exists, not that it caused the observed flake. Task 1.3 captures the failure text from a natural induced-load run (`go test -json`). If the HTTP failures do not carry fiber's timeout text, mechanism B is refuted and its fix tasks are dropped rather than applied speculatively. The same applies to mechanism A if its natural failure carries an exit or OOM message instead of the "matched 0 times" text followed by `context deadline exceeded`.

## Risks / Trade-offs

- [The throttled-CPU reproduction depends on Docker honouring `NanoCPUs`, which Docker Desktop on macOS does] → The test skips with a named reason when the daemon reports no CPU quota support, and the measurement in 1.1 records the daemon version.
- [A wider timeout only makes the symptom rarer] → The value is derived from the boot times measured in 1.1, per the rule `size-io-test-deadlines` writes. Q1 also offers remedies that remove the contention rather than tolerate it.
- [The reproductions are slow: a throttled MySQL boot takes over 60 s by construction] → They run under the existing package timeout and are counted by `scripts/check-test-timeout.sh` if they use a raw deadline. The task verifies the ceiling still holds.

## Cross-change coupling

- **`size-io-test-deadlines` (kartaladev/wrkflw#98)** writes the rule for sizing deadlines that wait on real I/O and says a value needs "a reproduction under induced contention". Task 1.1 produces exactly that for the MySQL boot. Fiber's implicit `app.Test` deadline is a construct that document's taxonomy does not list. This change adds it to `docs/agents/test-deadlines.md` only after `size-io-test-deadlines` lands, or rebases onto it, because both edit that file. No `Depends on`: the reproduction tasks do not need the note, and the note does not need the reproduction.
- **`split-persistence-modules` (kartaladev/wrkflw#39)** makes `internal/dbtest` stop importing the store, which rewrites `RunTestMySQL` in `internal/dbtest/mysql.go`, the same function this change's seam touches. Either may land first; the second rebases. Q5 of that change may move `internal/dbtest` to `persistence/internal/dbtest`, which moves the files but not the mechanism.
- **`harden-ci-guard-wiring`** owns `scripts/check-test-timeout.sh`. Whether that guard should also count `app.Test` deadlines is its call; this change does not edit the script.
- **`fix-doc-rot`** owns whether derived counts may live in prose. This change cites the seven-package and 35-container figures only in this design, not in contributor docs.

## Open Questions

These are the issue's own open question, which no maintainer comment has decided. They decide what group 2 of the tasks builds, so Readiness is human. Group 1 (the reproductions) does not depend on them.

**Q1. Remedy for mechanism A** (per-test, per-resource or per-runner, in the issue's terms).
- (a) Per-resource: give the MySQL helper an explicit, named startup timeout whose value comes from the 1.1 measurement. Cheapest; tolerates contention without removing it.
- (b) Per-resource: bound concurrent container boots across test binaries on one host with a cross-process lock. It removes the contention, but the lock file lands in a temp directory and inherits the macOS `mktemp -d` / `TMPDIR` pitfall the issue records for golangci-lint.
- (c) Per-runner: make the shared-server path (`scripts/testdb.sh up`, already what CI uses) the documented way to run the whole tree with parallel worktrees. Fixed container names and host ports mean every worktree adopts the same server, and per-test database names are already process-unique (`internal/dbtest/dbname.go`). It removes all per-package boots, but it is opt-in, so the default local path keeps the flake.
- (d) Per-runner: document or enforce a bound on `go test -p`. It degrades every run to protect the parallel-worktree case.
- **Recommendation:** (a) and (c) together. (c) removes the contention for the people who create it, and (a) keeps the default path from failing on a slow host with an evidence-sized deadline. Reject (b) unless 1.1 shows boots fail even at the measured worst-case duration.

**Q2. Remedy for mechanism B, if 1.3 confirms it.**
- (a) Route every `app.Test` call through one helper that passes an explicit `TestConfig` with a named timeout, sized from evidence.
- (b) Pass `Timeout: 0` to disable fiber's deadline and rely on `go test -timeout`. A hang then surfaces as a whole-binary panic far from its cause.
- (c) Shrink the 2 MiB fixtures. Rejected in advance by the tests' own comments: the body must exceed the 1 MiB default cap to prove anything.
- **Recommendation:** (a).

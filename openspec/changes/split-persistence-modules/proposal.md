## Why

A consumer who imports only the engine still gets pgx, the MySQL driver, modernc SQLite, goose, gin, fiber, fasthttp, quic-go and the Mongo driver in its module graph, because they all sit in one module. On top of that, 52 exported constructors are tied to `*pgxpool.Pool` or `*sql.DB`. kartaladev/wrkflw#22 (decided) and kartaladev/wrkflw#30 (decided) settled the cure: split modules by dependency, put an explicit `Conn` seam on the driver axis, and **a consumer imports only what they use or need**. PR A (#224, `affd6ae3`) laid the workspace. The split itself has not started, and it blocks the first tag (`cut-v0-release`) and the GORM module (`add-gorm-adapter`).

## What Changes

Every premise below was re-measured on `main` (`eebe55ba`).

**Absorbed issues**
- **kartaladev/wrkflw#39 (folded in, the remainder only).** PR A landed `go.work`, `examples/go.mod`, `scripts/modules.sh`, `check-module-drift.sh`, `check-lint-modules.sh` and the `check-extraction.sh` message fix. This change does everything else: PR B's whole list, plus the five items that the #30 comment on #39 added.
- **kartaladev/wrkflw#207 (folded in).** Move the surviving `internal/atrest` parser into the persistence subtree, rename it, and re-anchor `MigrationSets` as the first step. Its line citations are stale (see below).
- **kartaladev/wrkflw#220 (folded in; the remedy is open).** §1 holds. `uncrossCheckedTables` and the `schemasWith` fixture both range a literal `{postgres, mysql, sqlite}` (`internal/persistence/store/atrest_crosscheck_test.go:294`, `:334`), so a fourth dialect key goes unseen. §2 is reachable on reading: `reconcileMigrationSets` checks presence only (`internal/atrest/discover.go:266`), and `ParseSQL` accepts any dialect string (`internal/atrest/schema.go:124`). It stays a **hypothesis** until its failing test goes red.
- Decisions built and not reopened: **kartaladev/wrkflw#22 (decided)**, calls 1–5 and 10, and **kartaladev/wrkflw#30 (decided)**, calls 1, 3, 4 (except the post-tag `GOWORK=off` job) and 5, plus its adversarial-audit items.

**The split** (module list from #30, with `casbinauthz` already removed by `remove-casbin`)
- **`persistence` becomes its own module.** It takes the dialect-neutral store, the dialects, migrations, the exported querier seam, the cache interfaces and the otter and hot providers. `internal/persistence/{store,dialect}` and `internal/database{,/transaction}` move under `persistence/internal/`. The `persistence` import path is unchanged.
- **`persistence/pgx` (new module)** gets `FromPool(*pgxpool.Pool) persistence.Conn`, the pool and tx adapters, LISTEN/NOTIFY, advisory-lock ownership and **the Postgres elector**.
- **`persistence/sql` (new module)** gets `FromDB(*sql.DB, persistence.Dialect)`, which takes an explicit dialect and never infers one, plus `WithErrorClassifier` and **the MySQL elector**. Postgres over `database/sql` becomes reachable: the relay polls only, and ownership runs over `*sql.Conn`.
- **BREAKING (API).** About 17 `New...(Conn)` constructors replace the 52 driver-typed exported signatures (18 on `*pgxpool.Pool`, 34 on `*sql.DB`, re-measured). `DurableProvider` stays as the bundle. `add-lent-transaction-seam`'s two `Tx` constructors move to `pgx.FromTx` and `sql.FromTx`.
- **Postgres errors are classified through `SQLState()`.** The sql path no longer imports pgx. MySQL and modernc keep their driver imports.
- **Cache provider modules.** The redis and memcache providers leave the `persistence` module. **BREAKING:** `persistence/cache/cachetest` becomes test-only. The directory layout is open (Q2).
- **The HTTP bindings `transport/http/gin`, `transport/http/fiber` each become a module.** The Fiber v2 binding is added later by `add-fiber-v2-adapter`, which creates `transport/http/fiberv2` as its own module.
- **BREAKING:** `scheduler/backend/{postgres,mysql}` are deleted. The scheduler keeps only the `Elector` port.
- **The journal codec moves to `runtime/kernel`** (#30 call 3). The stored bytes and kind discriminators are pinned unchanged.
- **`internal/dbtest` stays in root and stops importing the store; callers apply migrations** (#30 comment on #39, item 3). The fixture's import path does not change.
- **Workspace and guards.** The new modules go into `go.work` and `MIN_MODULES` is raised. A guard test checks that no importable root package's non-test build names a database driver, web framework or cache client. `check-extraction.sh` and its check-run name `extraction constraint (internal/database)` are re-anchored in the same commit as the move. The internal-leak guard covers the new modules. Nested modules require root at a **pseudo-version of a pushed commit**, never a `replace`.

**Stale premises found on re-measurement**
- #39: "`internal/dbtest` imported by 74 files across 16 packages". The 74 files still hold, but they now sit in **12** packages, not 16.
- #39/#207: "137-file import rewrite". It is now **140** files that import a moving path. Because `internal/dbtest` stays in root, **104** of them need a path rewrite.
- #39: "`internal/observability` must be promoted or duplicated". **Withdrawn by #30 and re-confirmed:** a toy workspace shows that a nested module may import its parent's `internal/`. The same build shows that a root package importing a child's `internal/` is refused (`use of internal package … not allowed`).
- #22 call 10's rationale, that `pgx.Tx` must be adaptable "from a module that cannot import `persistence/internal`", is **stale**. The same toy shows `persistence/pgx` can import `persistence/internal`. The call itself (export the querier seam) stays decided and is not reopened. Design records this for the maintainer.
- #207: "2,337 lines" is now **2,477**. `discover.go:186`/`:268-273` now sits at `:264-288`, and `discover.go:345-346` at `:368-369`.
- #30: "`persistence/cache` module holds redis and memcache" **cannot be built as stated**. A `go.mod` at `persistence/cache/` would capture the cache-interface package and `hotcache`, which `persistence` imports in production code (Q2).
- #39 comment 2 (PR A's "12 → 0" edges): for this split there are **0** non-test edges from root into the moving set. There are **21** root test files importing it. remove-casbin deletes 6 of them and the elector move takes 5. That leaves 8 files that really import the moving set (Q1), and 4 of them import paths that become `persistence/internal`. Two more guard tests only name those paths as strings: `runtime/monitor/internal_leak_test.go` and `service/vendorfree_test.go`.

**Impact flags**
- **Consumer go.mod (flagged, the core of this change).**
  - (1) A consumer of the stores must newly require `…/persistence` plus `…/persistence/pgx` or `…/persistence/sql`. A consumer of gin or fiber requires that binding's module, and a consumer of redis or memcache requires that provider's module.
  - (2) A consumer importing only root packages drops every driver, framework and cache client from its build.
  - (3) **Ambiguous-import hazard.** A root pseudo-version from before the split, paired with a post-split `persistence` module, fails with "ambiguous import". The first tag must therefore come after the split (`cut-v0-release`).
  - (4) Root `go.mod` keeps its test-only driver requirements (#30 call 5) and the requirements of `internal/dbtest`. Pruning keeps them out of consumer builds, but they still appear in a consumer's `go list -m all`.
- **Persisted formats (flagged): no change.** The migration files move but keep their goose version table and contents. The journal codec moves with byte-identical output, pinned by a new requirement.
- **Default security posture: unchanged.**

**Out of scope**
- The lent-transaction seam itself (`persistence.Tx`, `Dialect`, `WithTx`, `Continuation`): `add-lent-transaction-seam` (kartaladev/wrkflw#109).
- The `persistence/gorm` module: `add-gorm-adapter` (kartaladev/wrkflw#42).
- The release script, lockstep tags and the post-tag `GOWORK=off` CI job: `cut-v0-release` (kartaladev/wrkflw#48).
- The route table: `unify-http-routes-as-data` (kartaladev/wrkflw#40). The Fiber v2 binding and its module: `add-fiber-v2-adapter`.
- Deleting casbin and its migration set: `remove-casbin`.
- Rewriting the examples' wiring for terminal events: `clarify-terminal-event-delivery`. This change only moves those files' imports and constructor calls.
- Inverting the scheduler into its own module: #30 records it as the end state, not for this release.

**Decisions not yet made** (design, Open Questions; the specs avoid depending on them)
- **Q1:** where the 8 back-edge root test files and `transport/http/parity` go once their imports leave root.
- **Q2:** cache provider module layout.
- **Q3:** the #220 remedy.
- **Q4:** the `atrest` package's new name and the exported names of the codec in `runtime/kernel`.
- **Q5:** confirming #30's `internal/dbtest` placement over group-17's later `persistence/dbtest` plan.

**Coordination**
- Size: XL
- Depends on: add-lent-transaction-seam, remove-casbin, unify-http-routes-as-data
- Blocks: add-gorm-adapter, add-fiber-v2-adapter, cut-v0-release, `add-user-task-updates`, `document-stability-policy`
- Readiness: human

## Capabilities

### New Capabilities
- `module-dependencies`: what each wrkflw module brings into a consumer's build. The core carries no database driver, web framework or cache client. The `database/sql` adapter carries no pgx. Each HTTP binding carries only its own framework.
- `persistence-connection`: how a consumer hands a database to wrkflw. Covers `pgx.FromPool` and `sql.FromDB` with an explicit, closed dialect, refusal of a nil handle, the `WithErrorClassifier` override, and advisory-lock ownership for Postgres over `database/sql`.

### Modified Capabilities
- `instance-store`: duplicate refusal gains a Postgres-over-`database/sql` row, recognised by SQLSTATE. A new requirement pins the journal encoding across the codec move.
- `event-delivery`: notification wake-up moves to the pgx module, and Postgres over `database/sql` drains by polling.

## Impact

- **Modules:** new `persistence/go.mod`, `persistence/pgx/go.mod`, `persistence/sql/go.mod`, the cache provider modules (Q2), and `transport/http/{gin,fiber}/go.mod`. Plus `go.work`, `scripts/modules.sh` (`MIN_MODULES`), and the root and `examples` `go.mod`/`go.sum`.
- **Moves:** `internal/persistence/**`, `internal/database/**` and `internal/atrest` go to `persistence/internal/**`. `scheduler/internal/gocron/{pgelector,myelector}` go to the driver modules. `internal/persistence/store/trigger_codec.go` goes to `runtime/kernel`.
- **Deleted:** `scheduler/backend/{postgres,mysql}` and the exported `persistence/cache/cachetest`.
- **CI:** `.github/workflows/ci.yml` (the extraction job's name and anchors, lint steps per module), `scripts/check-extraction.sh`, `scripts/check-module-drift.sh` (reviewed against the new modules), `runtime/monitor/internal_leak_test.go` and `service/vendorfree_test.go`.
- **Examples:** 11 files import `persistence` and one imports `internal/database`. They move to `Conn` constructors.
- **Docs:** `CONTRIBUTING.md` (per-module commands), the package docs that name the moved constructors, and `runtime/README.md` where it names them.

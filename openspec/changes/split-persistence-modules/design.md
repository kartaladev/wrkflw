# Design

## Context

See proposal.md for the motivation and the stale premises. The measurements this design rests on, all on `main` at `eebe55ba`:

- **Workspace.** `go.work` uses `.` and `./examples`. `go list ./...` gives 69 packages and `./examples/...` gives 43. `examples/go.mod` reaches root through `replace github.com/kartaladev/wrkflw => ../` at the zero version.
- **Moving set.** It is `persistence/**` (7 packages), `internal/persistence/{store,dialect}`, `internal/database{,/transaction}`, `internal/dbtest` and `internal/atrest`. Root has **0** non-test imports into it.
- **Root test files that import the moving set.** There are 21:
  - 6 are casbin (`remove-casbin` deletes them).
  - 5 are the electors in `scheduler/internal/gocron/{pg,my}elector`.
  - 8 are real back-edges: `eventing/message_e2e_test.go`, `processtest/taskstoreconformance_test.go`, `runtime/{jobstore_rehydrate_durable,rehydrate_durable,timer_txflow}_test.go` and `scheduler/{elector,locker,mysql_elector}_test.go`. All 8 are external `_test` packages.
  - 2 only name the paths as strings: `runtime/monitor/internal_leak_test.go:257` and `service/vendorfree_test.go:32-36`.
  - The processtest file and the three runtime files import `internal/persistence/{store,dialect}`. Once those paths sit under `persistence/internal/`, root may not import them.
- **Non-test drivers and frameworks in importable root packages.** They appear only in `casbinauthz`, `scheduler/backend/postgres` (pgx), `transport/http/gin` (gin, quic-go, mongo-driver) and `transport/http/fiber` (fiber, fasthttp). `internal/dbtest` also carries pgx, mysql, modernc, goose and testcontainers, but consumers cannot import it.
- **Driver-typed exported signatures in `persistence`.** There are 52: 18 on `*pgxpool.Pool` and 34 on `*sql.DB`. `casbinauthz` has 2 more and `scheduler/backend` 2 more.
- **Imports of each moving path.** These are file counts, and the union is 140:

  | Path | Files |
  |---|---|
  | `internal/persistence/store` | 59 |
  | `internal/persistence/dialect` | 62 |
  | `internal/database` | 33 |
  | `internal/database/transaction` | 18 |
  | `internal/dbtest` | 78 (74 outside itself, in 12 packages) |
  | `internal/atrest` | 3 |

  Without `internal/dbtest` the union is 104.
- **`internal/atrest`.** It is 2,477 lines. Its only non-stdlib import is `internal/persistence/dialect`, used at `discover.go:368-369`. `MigrationSets` has four keys relative to `ModuleRoot()`, which is the nearest ancestor with a `go.mod` (`discover.go:20`). One key is `internal/authz/casbin/migrations`.
- **Journal codec.** `internal/persistence/store/trigger_codec.go` is 222 lines and imports only stdlib, `authz` and `engine`. It has 16 kind discriminators. An unknown kind gets an unexported error (`:220`).
- **Cross-module `internal/`.** A toy workspace in the session scratchpad, three modules `r`, `r/child` and `r/child/pgx`, measured two things:
  - `r/child/pgx` can import `r/child/internal/q`, and `r/child` can import `r/internal/obs`.
  - `r/sib`, in the root module, importing `r/child/internal/q` is refused with `use of internal package … not allowed`.

## Goals / Non-Goals

**Goals:**
- Land #30's module list. The one exception is the cache module, whose layout is open (Q2).
- Replace the 52 driver-typed signatures with `Conn` constructors (#22).
- Keep `go build`, `go test`, lint and vulncheck green per module through `scripts/modules.sh`.
- Leave persisted data byte-identical.

**Non-Goals:**
- Tagging, the release script, and the `GOWORK=off` job (`cut-v0-release`).
- `persistence/gorm` (`add-gorm-adapter`).
- Designing the lent seam (`add-lent-transaction-seam`). This change only moves its constructors.
- Promoting `internal/observability`, which is withdrawn (#30).

## Decisions

1. **Driver axis, not dialect axis** (decided, #22 call 1). The rejected option was a module per dialect. The evidence: the store is written once against a three-method querier, so dialect is data. Only the facade ties dialect to driver.
2. **Explicit seam types** (decided, #22 calls 2–3).
   - `pgx.FromPool` and `sql.FromDB(db, persistence.Dialect)` return an opaque `persistence.Conn`.
   - The rejected options were `conn any` at the boundary, and inferring the dialect from `db.Driver()`. Inference breaks under wrapped drivers, which is the GORM/mattn case of #31.
   - `persistence.Dialect` comes from `add-lent-transaction-seam`, a distinct type and never an alias (`runtime/monitor/internal_leak_test.go` stays green).
3. **Postgres errors are classified through `interface{ SQLState() string }`** (decided, #22 call 4).
   - The rejected option was a pgconn import on the sql path. It would put pgx back into every `database/sql` graph. Today `internal/persistence/dialect` imports `pgconn`, measured in the per-package import listing.
   - MySQL and modernc keep their driver imports. `WithErrorClassifier` covers drivers we do not know.
4. **Querier seam exported, fourteen methods** (decided, #22 call 10). Not reopened.
   - **Finding for the maintainer:** the call's stated reason is refuted by the toy build. That reason was that `persistence/pgx` cannot import `persistence/internal`.
   - The surviving reason is that a third-party driver module could implement it. If the maintainer withdraws the call, the seam can stay under `persistence/internal`. The specs are unaffected either way, because they never name the querier.
5. **Electors move to the driver modules behind `scheduler.Elector`; `scheduler/backend/*` is deleted** (decided, #30 audit). The rejected option kept them in `scheduler`, which leaves pgx in every graph: `scheduler/backend/postgres` and `pgelector` import it today.
6. **Journal codec to `runtime/kernel`** (decided, #30 call 3). The rejected option kept it in the store, which makes every new trigger a two-module release.
   - The codec has to be **exported** from `kernel`. `persistence` is not under `runtime/kernel/`, so it cannot import a `kernel/internal` package. The names are Q4.
   - Golden rows captured *before* the move pin the bytes (instance-store spec).
7. **`internal/dbtest` stays in root and stops importing the store** (decided on #39 from #30, item 3).
   - Each caller runs the migration itself, so the package-level cycle (`dbtest` → `store`, store tests → `dbtest`) goes away.
   - The rejected option is group-17's later plan of an exported `persistence/dbtest`. It adds a public test-support API and was never ruled on. Q5 asks for confirmation only.
8. **Nested modules require root at a pushed-commit pseudo-version, never `replace`** (decided, #30 audit).
   - A `replace` in a library's `go.mod` is ignored by its consumers.
   - `go mod tidy` ignores `go.work`, so the pseudo-version must exist upstream. `cut-v0-release`'s script rewrites it at the first tag.
   - `examples/` keeps its `replace` because nothing consumes it.
9. **`MigrationSets` is re-anchored first** (#207).
   - Moving the store under `persistence/` moves `ModuleRoot()` for every test inside the subtree. All keys go stale at once, and the reconcile fails closed with "MigrationSets entries matching no discovered migration directory".
   - Do **not** soften the reconcile. Re-anchor the keys relative to the `persistence` module.
   - The casbin key is gone with `remove-casbin`, so no key points outside the subtree.
10. **`check-extraction.sh` moves with the toolkit.** Its two anchors (`module`, `toolkit_dir`) and the check-run name `extraction constraint (internal/database)` change in the same commit as the move. A red run that names a vanished path is what the #39 manager note warned of. The check is a required check-run, so the branch-protection name changes too.
11. **The root import guard enumerates packages** (`go list` over importable root packages) and is never a hand list. Its negative control runs the same lister over `persistence/pgx` and must find pgx (module-dependencies spec).

## Cross-change coupling

| Change | Coupling | Owner / order |
|---|---|---|
| `add-lent-transaction-seam` | It defines `persistence.Tx`, `Dialect`, `WithTx`, `Continuation`, and the single-module `Tx` constructors this change renames to `pgx.FromTx`/`sql.FromTx`. #39 says it lands first. | It lands first. This change owns the rename. |
| `remove-casbin` | It deletes `casbinauthz`, `internal/authz/casbin` and its migration set, which removes a `MigrationSets` key outside the subtree and 6 back-edge files. It holds the go.mod slot. | It lands first. |
| `unify-http-routes-as-data` | #40 says "do the table refactor on the single module first; #39/#30 move finished bindings". It reshapes `transport/http/parity`. | It lands first, and lists this change under Blocks. This change moves the bindings. |
| `add-fiber-v2-adapter` | Creates `transport/http/fiberv2` as its own module. | It depends on this change, so the module exists from the start. |
| `add-gorm-adapter` | It needs `Conn`, `sql.FromDB`, `sql.FromTx`, `WithErrorClassifier`, `NewMigrator(Conn)` and the module layout. Its proposal also credits this change with "lockstep tags", which are `cut-v0-release`'s. | It lands after. |
| `cut-v0-release` | First tags, the release script, the `GOWORK=off` job, and rewriting the pseudo-versions. It must tag only post-split (the ambiguous-import hazard). | It lands after. |
| `clarify-terminal-event-delivery` | Shares `examples/{production_wiring,sqlite_wiring,mysql_wiring}/main.go`. | Either order. The second lander rebases. |
| `harden-definition-store-parity`, `persist-pending-commands`, `recover-waiters-on-restart` | They edit migration files this change moves. | Either order. The second lander carries the edit. `harden-definition-store-parity` prefers to land first. |
| `encode-persisted-enums-by-name` | Its guard walks the trigger types registered in `trigger_codec.go`, which this change moves to `runtime/kernel`. | Either order. The second lander re-points the guard. |
| `document-stability-policy` (#158) | Cites `internal/persistence/store/store_core.go`, which moves. | It re-derives its paths after this change. |

## Risks / Trade-offs

- [Ambiguous import for a consumer mixing a pre-split root with the post-split `persistence` module] → No tag exists yet. `cut-v0-release` depends on this change, and the release notes tell pseudo-version users to move root and `persistence` together.
- [Six open Dependabot PRs (#104, #105, #228–#231) conflict with the move] → Drain or close them before the move commit. Rebasing them across it is the costlier path.
- [Root `go.mod` still lists test-only drivers, plus `internal/dbtest`'s non-test requirements, so they appear in a consumer's `go list -m all`] → #30 call 5 accepts this. Pruning keeps them out of builds, and the guard checks builds.
- [The guard passes vacuously after a path move] → It enumerates packages, has the pgx negative control, and has a self-test row that plants a driver import in a temporary root package.
- [The moved runtime tests (Q1 a) lose package-local context] → They are already external `_test` packages, so they use only exported API plus the store internals they will reach from inside `persistence`.
- [`check-module-drift.sh` compares only root against the workspace] → After the split, `persistence/go.mod` owns pgx and root may not. Review the script against the new modules, and keep its self-test.

## Migration Plan

The order is forced. Each step stays green per module.

1. Re-anchor `MigrationSets` (Decision 9) and land #220's failing test.
2. `internal/dbtest` stops importing the store.
3. Golden journal rows are captured on the pre-move tree, then the codec moves to `runtime/kernel`.
4. `persistence/go.mod` is created. The store, dialects, toolkit and parser move under `persistence/internal`, the imports are rewritten, and `check-extraction.sh` is re-anchored in the same commit.
5. Back-edge tests are relocated (Q1).
6. `Conn` and the querier seam are added, along with the `persistence/pgx` and `persistence/sql` modules, the `New...(Conn)` constructors and the removal of the 52 signatures. The `Tx` constructors are renamed.
7. Electors move and `scheduler/backend` is deleted.
8. Cache provider modules (Q2) are created.
9. The transport binding modules are created.
10. The root import guard and the module-dependencies checks are added, and `MIN_MODULES` is raised.

Rollback is a revert of the merge. No data migrates.

## Open Questions

Readiness is human until these are answered. None of them changes the specs, which are written to hold under every option.

1. **Root test back-edges.** Eight root test files import the moving set, four of them through paths that become `persistence/internal`. `transport/http/parity` imports the gin and fiber bindings.
   - (a) Move each file into the module whose packages it exercises: the persistence module for the runtime, processtest, eventing and scheduler durable tests, and a test-only `transport/http/parity` module that requires every binding.
   - (b) Keep them in root with test-only `require`s of the child modules. That is a root↔child requirement cycle, which #30's audit treated as a blocker, and the four internal imports still need rewriting to public API.
   - (c) One integration-test module that requires all modules.
   - **Recommendation: (a).** All the files are black-box already. (b) re-creates the cycle, and (c) moves tests away from both of their subjects.
2. **Cache provider module layout.** #30's "`persistence/cache` module holding redis and memcache" cannot be built at the current paths. A `go.mod` at `persistence/cache/` would capture the cache-interface package and `hotcache`, which `persistence` imports in production code.
   - (a) One module per remote provider at its existing path (`persistence/cache/rediscache`, `persistence/cache/memcache`). Import paths stay the same, at the cost of 11 modules instead of 10.
   - (b) One module at a new directory. Import paths change.
   - (c) Move the interfaces and the otter and hot providers out of `persistence/cache`. That renames a package every caching consumer imports.
   - **Recommendation: (a).** `cachetest` moves to `persistence/cache/internal/cachetest`, which the provider modules may import (path rule, toy-verified).
3. **#220 remedy.** The issue requires the input on which each candidate still fails.
   - (a) Range the schema map's own keys in both `uncrossCheckedTables` and `schemasWith`. It still fails on a typo'd dialect name (`"postgress"`): the guard visits the table, but `LoadSchemas` reports no error for the typo, so the typo hides the set from every other consumer.
   - (b) Validate `Dialects` at declaration against the dialect vocabulary. It still fails when a correctly spelled fourth dialect joins the vocabulary and the guard's literal triple is not updated.
   - **Recommendation: (c), both.** The vocabulary comes from one source, the dialect package, so "two places to keep in step" becomes one source and two readers.
4. **Names.** **Recommendation:**
   - `persistence/internal/schemaparse` for `internal/atrest` (#207's suggestion). It is internal, so nothing outside the persistence tree sees it.
   - `kernel.MarshalTrigger`/`kernel.UnmarshalTrigger` for the codec, which are its current names. Export the vocabulary as `kernel.TriggerKinds()` rather than a mutable slice.
5. **`internal/dbtest` placement.** #30's decision (Decision 7) and group-17's later `persistence/dbtest` plan conflict. **Recommendation:** follow the decision. If Q1 (a) is chosen, root has no remaining `dbtest` consumer. The maintainer may then move it to `persistence/internal/dbtest`, still not exported, which drops testcontainers from root's `go.mod`.

## Findings: sentinels to export

Some spec rows say "refused with an error naming …" because no sentinel exists today:
- a nil handle to `pgx.FromPool` or `sql.FromDB`;
- a `persistence.Dialect` outside the vocabulary (align with `add-lent-transaction-seam` Q4, which recommends the same sentinel for `FromSQLTx`);
- an unknown journal kind (`trigger_codec.go:220` builds an ad-hoc `fmt.Errorf`).

Each should get an exported sentinel when it is implemented.

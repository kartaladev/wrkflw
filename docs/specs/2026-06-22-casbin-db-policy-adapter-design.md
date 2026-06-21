# DB-backed casbin policy adapter + watcher — Design Spec

- Status: Accepted (deferred-backlog item — Authz deferred follow-up #1)
- Date: 2026-06-22
- Supersedes: nothing. Implements the "DB policy adapter (with a watcher for
  multi-node reload)" deferred by the Authorization sub-project
  (`docs/specs/2026-06-21-authz-casbin-design.md` §Deferred follow-ups #1;
  ADR-0010 "v1 is adapter-agnostic").
- Related ADRs (new): 0023 (pgx-native DB policy adapter + LISTEN/NOTIFY watcher).
  Builds on 0008 (façade-over-internal), 0010 (hybrid casbin authorizer behind
  `authz.Authorizer`), 0006 (Postgres/pgx/goose persistence shape), 0022
  (LISTEN/NOTIFY mechanics the watcher reuses).

## 1. Problem & scope

The Authorization sub-project shipped a hybrid casbin `authz.Authorizer` but kept
policy **adapter-agnostic**: v1 accepts model+policy as strings
(`casbinauthz.NewCasbinAuthorizerFromStrings`) or a consumer-built
`*casbin.SyncedEnforcer` (`casbinauthz.NewCasbinAuthorizer`). Neither loads policy
from a database, so a multi-node deployment cannot share one authoritative policy
store, and policy changes on one node are invisible to the others.

This track lands a **PostgreSQL-backed casbin policy adapter** plus a **policy-change
watcher** so that:

- Policy lives in one `casbin_rule` table that all nodes load from.
- Policy mutations (via the casbin auto-save API) persist to that table.
- A change on one node is propagated to the others, which reload — **multi-node
  policy coherence**.

**In scope:** a pgx-native `persist.Adapter`, a Postgres `LISTEN`/`NOTIFY`
`persist.Watcher`, an opt-in migration for `casbin_rule`, the
`casbinauthz.NewCasbinAuthorizerFromDB` façade constructor, and testcontainers
tests. **Out of scope:** a policy-management REST/admin transport surface (that is
a transport-layer feature, separate); casbin filtered-policy loading
(`FilteredAdapter`); the casbin-native ABAC-in-matchers alternative (ADR-0010
deferred #2); richer resource modeling (domains/tenants).

## 2. Invariants this track must not violate

1. **casbin confinement.** casbin (`github.com/casbin/casbin/v2` and its
   `model`/`persist` subpackages) is imported **only** in `casbinauthz/` and
   `internal/authz/casbin/`. The new adapter + watcher live in
   `internal/authz/casbin/`; `engine`/`model`/`runtime`/`internal/persistence/*`
   gain no casbin import.
2. **No new ORM (stack lock).** The stack is pgx v5 + pgxpool + goose v3 + embed.FS
   (CLAUDE.md). The adapter is **hand-rolled over `*pgxpool.Pool`** — community
   adapters (`casbin-pg-adapter` on go-pg, `gorm-adapter`) are rejected because
   they pull a second driver/ORM, which would require a stack-change ADR for no
   benefit. casbin v2.135.0 stays the pinned version; **no casbin adapter
   dependency is added** (casbin ships the `persist.Adapter`/`Watcher` interfaces
   and `LoadPolicy*` helpers internally).
3. **Façade discipline (ADR-0008/0010).** The public constructor returns the stable
   `authz.Authorizer` interface (plus an `io.Closer` for the watcher), never the
   internal concrete enforcer/adapter/watcher.
4. **Opt-in, no behavior change for existing consumers.** The string- and
   enforcer-based constructors are untouched; nothing auto-runs on import (the
   `casbin_rule` migration is explicit, like `persistence.Migrate`).
5. **Authorizer semantics unchanged.** The hybrid evaluation (role hierarchy →
   resource-privilege → expr attribute, fail-closed `ErrNotAuthorized`) is reused
   verbatim via `internalcasbin.New(enforcer)`; only the *source of policy* changes.

## 3. The pgx-native adapter (`internal/authz/casbin/pg_adapter.go`)

Implements casbin's `persist.Adapter` (confirmed signatures, casbin v2.135.0):

```go
type Adapter interface {
    LoadPolicy(model model.Model) error
    SavePolicy(model model.Model) error
    AddPolicy(sec string, ptype string, rule []string) error
    RemovePolicy(sec string, ptype string, rule []string) error
    RemoveFilteredPolicy(sec string, ptype string, fieldIndex int, fieldValues ...string) error
}
```

over the conventional table:

```sql
CREATE TABLE casbin_rule (
    id    BIGSERIAL PRIMARY KEY,
    ptype TEXT NOT NULL,
    v0    TEXT NOT NULL DEFAULT '',
    v1    TEXT NOT NULL DEFAULT '',
    v2    TEXT NOT NULL DEFAULT '',
    v3    TEXT NOT NULL DEFAULT '',
    v4    TEXT NOT NULL DEFAULT '',
    v5    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX casbin_rule_ptype_idx ON casbin_rule (ptype);
```

- **`pgAdapter`** holds a `*pgxpool.Pool` (constructed by the façade). Uses `TEXT`
  (idiomatic Postgres; no `VARCHAR(100)` length traps) and `DEFAULT ''` so unused
  rule fields are empty strings, matching casbin's line model.
- **`LoadPolicy(model)`** — `SELECT ptype, v0, v1, v2, v3, v4, v5 FROM casbin_rule
  ORDER BY id`; for each row build the rule slice `[ptype, v0..v5]` trimmed of
  trailing empty fields and feed it via `persist.LoadPolicyArray(rule, model)`
  (confirmed helper). (Confirm-point: `LoadPolicyArray` vs constructing a CSV line
  for `LoadPolicyLine` — pick whichever the v2.135.0 helper accepts cleanly;
  `LoadPolicyArray([]string{ptype, v0, ...})` is the array form.)
- **`SavePolicy(model)`** — one `pgx.Tx`: `DELETE FROM casbin_rule`, then bulk
  insert every `p`/`g` line from the model. Use `pgx.Batch` (or `CopyFrom`) for the
  insert. Iterate the model's `p` and `g` sections → each ptype's stored rules,
  writing `ptype` + the rule fields padded to v0..v5.
- **`AddPolicy(sec, ptype, rule)`** — `INSERT` one row (rule padded to v0..v5).
- **`RemovePolicy(sec, ptype, rule)`** — `DELETE ... WHERE ptype=$ AND v0=$ AND …`
  matching the exact rule fields.
- **`RemoveFilteredPolicy(sec, ptype, fieldIndex, fieldValues...)`** — `DELETE`
  with a `WHERE ptype=$` plus equality on `v{fieldIndex..fieldIndex+len-1}` for the
  provided non-empty `fieldValues` (skip empty filter slots, matching casbin
  semantics).
- A small `padRule([]string) [6]string` / `ruleFields` helper centralizes the
  rule↔(v0..v5) mapping so the five methods share one mapping (DRY).

The adapter is **auto-save capable**: with the enforcer's default auto-save on,
`enforcer.AddPolicy(...)`/`RemovePolicy(...)` call the adapter's `AddPolicy`/`RemovePolicy`,
persisting the change.

## 4. The LISTEN/NOTIFY watcher (`internal/authz/casbin/pg_watcher.go`)

Implements casbin's `persist.Watcher` (confirmed, v2.135.0):

```go
type Watcher interface {
    SetUpdateCallback(func(string)) error
    Update() error
    Close()
}
```

reusing the relay's LISTEN/NOTIFY mechanics (ADR-0022):

- **`pgWatcher`** holds the `*pgxpool.Pool`, a channel name (default
  `wrkflw_casbin_policy`), a per-process `nodeID` (default: a value derived at
  construction — see below), the update callback, and a cancel func for the
  listener goroutine.
- **`Update()`** — called by the enforcer after a local policy change (auto-notify
  is on by default). Emits `SELECT pg_notify($channel, $nodeID)` so the payload
  carries the originating node's id.
- **Listener goroutine** — `pool.Acquire(ctx)` → `LISTEN <channel>` → loop
  `conn.Conn().WaitForNotification(ctx)`; on each notification, **if the payload ≠
  this node's `nodeID`**, invoke the update callback (classically
  `enforcer.LoadPolicy`). Skipping self-payloads avoids a node reloading in
  response to its own write. Reconnect on transient failure with a **cancellable
  backoff** (`select { ctx.Done() | time.After(delay) }`), mirroring
  `relay.listenLoop`; on ctx cancel the goroutine exits and releases the conn.
- **`SetUpdateCallback(fn)`** — stores `fn`. (Confirm-point: casbin's
  `SyncedEnforcer.SetWatcher` may itself call `watcher.SetUpdateCallback(e.LoadPolicy)`;
  if so we rely on that, otherwise the façade sets it explicitly. Either path wires
  the callback to `enforcer.LoadPolicy`.)
- **`Close()`** — cancels the listener context, waits for the goroutine to exit,
  releases the connection. Idempotent.

**nodeID** is a process-unique identifier so self-notifications are filtered. It is
NOT generated from `Math.random`/wall clock at engine level (those are fine here —
this is edge infrastructure, not the pure core); the façade derives it (e.g. a
`WithNodeID(id)` option; default a UUID-ish value the façade computes at
construction). Two distinct processes get distinct ids; the same process filters
its own echo.

## 5. Migration (`internal/authz/casbin/migrations/0001_casbin_rule.sql`)

- Embedded via `//go:embed migrations/*.sql`, run by a goose v3 `Provider`.
- **Separate goose version table** (`casbin_goose_db_version`, via
  `goose.WithStore`/a custom-table store) so the casbin migration set is
  independent of the persistence migration set (`goose_db_version`). A consumer
  runs `casbinauthz.MigrateCasbin(ctx, pool)` **only** if they use DB-backed policy;
  persistence consumers who don't use casbin never get the table.
- Explicit, never auto-run on import (project convention; the adapter does NOT
  `CREATE TABLE IF NOT EXISTS` on construct).

## 6. The façade (`casbinauthz/casbinauthz.go`)

```go
// NewCasbinAuthorizerFromDB builds a hybrid casbin Authorizer whose policy is
// loaded from (and saved to) the casbin_rule table in pool, with a LISTEN/NOTIFY
// watcher that reloads policy when another node changes it. Call MigrateCasbin
// first so the table exists. Close the returned io.Closer at shutdown to stop the
// watcher goroutine and release its connection.
func NewCasbinAuthorizerFromDB(
    ctx context.Context, pool *pgxpool.Pool, opts ...DBOption,
) (authz.Authorizer, io.Closer, error)
```

Construction sequence:
1. Build the model: `WithModel(text)` option, default `DefaultModel`
   (`model.NewModelFromString`).
2. `adapter := newPGAdapter(pool)`.
3. `enforcer, err := casbin.NewSyncedEnforcer(model, adapter)` — this auto-loads the
   policy from the DB.
4. If the watcher is enabled (default; `WithoutWatcher()` disables;
   `WithWatcherChannel(name)` / `WithNodeID(id)` tune): `w := newPGWatcher(...)`;
   `enforcer.SetWatcher(w)`; ensure the callback is `enforcer.LoadPolicy`
   (explicitly via `w.SetUpdateCallback` if casbin didn't).
5. `inner := internalcasbin.New(enforcer)` (reuses the hybrid evaluator unchanged).
6. Return `(casbinAuthorizer{inner, enforcer}, closer, nil)` where the `io.Closer`
   closes the watcher (no-op when the watcher is disabled).

Options (`DBOption`): `WithModel(text string)`, `WithoutWatcher()`,
`WithWatcherChannel(name string)`, `WithNodeID(id string)`. The existing
`NewCasbinAuthorizer` / `NewCasbinAuthorizerFromStrings` / `DefaultModel` /
`ReloadPolicy` surface is unchanged. `MigrateCasbin(ctx, pool) error` is added.

## 7. Package layout

| Package | Adds |
|---|---|
| `internal/authz/casbin/` | `pg_adapter.go` (`pgAdapter` impl of `persist.Adapter`), `pg_watcher.go` (`pgWatcher` impl of `persist.Watcher`), `migrations/0001_casbin_rule.sql` + an embed + a `migrate` helper (goose Provider, separate version table). Imports casbin `persist`/`model` + pgx — allowed here. |
| `casbinauthz/` (façade) | `NewCasbinAuthorizerFromDB(ctx, pool, ...DBOption) (authz.Authorizer, io.Closer, error)`, `MigrateCasbin(ctx, pool) error`, the `DBOption` constructors. Re-exports the stable interface. |
| `engine`/`model`/`runtime`/`internal/persistence/*` | **Nothing.** casbin confinement holds. |

## 8. Error handling

- Adapter SQL errors wrap with context (`fmt.Errorf("casbin pgadapter: load: %w", err)`)
  and propagate to casbin (which surfaces them from `LoadPolicy`/`AddPolicy`/…).
- Watcher listener errors: transient conn failures log + reconnect (poll/​backoff,
  no hot-spin); a failed `Update()` NOTIFY returns its error to casbin (the local
  write already persisted via the adapter; a missed NOTIFY only delays remote
  reload — not a correctness loss, mirroring ADR-0022's "NOTIFY is never a
  correctness dependency"; remote nodes can also be reloaded manually via the
  existing `ReloadPolicy`).
- Construction errors (bad model, enforcer build, initial load) return from the
  constructor; the partially-built watcher (if any) is closed before returning so no
  goroutine leaks on the error path.

## 9. Testing (testcontainers Postgres 17, `-p 1`)

- **Adapter:** `MigrateCasbin` then — `SavePolicy` writes all lines; `LoadPolicy`
  round-trips them into a fresh enforcer; `AddPolicy`/`RemovePolicy` persist (verify
  via a second enforcer's `LoadPolicy`); `RemoveFilteredPolicy` deletes the matched
  subset only; rule fields with fewer than 6 values round-trip (padding correct).
- **Watcher (the multi-node proof):** two `Authorizer`s built via
  `NewCasbinAuthorizerFromDB` over the **same** pool/DB (distinct nodeIDs). Mutate
  policy on A (`enforcer.AddPolicy`); assert B observes the change — poll B's
  `Authorize`/`enforcer.GetPolicy` until it reflects the new rule (NOTIFY → B's
  listener → `LoadPolicy`), within a bounded timeout well under any poll fallback.
  Assert A does **not** spuriously reload on its own write (self-payload filtered).
  Assert `Close()` stops the listener (no goroutine leak; use a cancellable ctx).
- **End-to-end:** a DB-sourced policy drives the full hybrid `Authorize` (role
  hierarchy + privilege + attribute) — same assertions as the string-based authz
  tests, proving the only change is the policy source.
- **Confinement guard:** keep/extend the check that casbin is not imported outside
  `casbinauthz/` + `internal/authz/casbin/`.

## 10. Risks & follow-ups

- **casbin API confirm-points** — `LoadPolicyArray` vs `LoadPolicyLine`; whether
  `SetWatcher` auto-sets the `LoadPolicy` callback; the exact `model.Model` shape
  for iterating `p`/`g` lines in `SavePolicy`. The implementer verifies against
  v2.135.0 (the signatures in §3/§4 are confirmed from the module cache).
- **Watcher connection cost** — one pinned `LISTEN` connection per process with a
  watcher (same tradeoff as the relay listener / advisory-lock ownership).
- **No filtered loading** — the whole policy loads on every reload; large policy
  sets would benefit from `FilteredAdapter`/incremental `WatcherEx` updates
  (deferred).
- **Policy-admin transport** — no REST/gRPC policy-management endpoints here;
  mutation is via the casbin enforcer API. A transport surface is a separate track.
- **Migration table proliferation** — a separate `casbin_goose_db_version` table is
  intentional (independent opt-in); documented so operators expect two goose
  bookkeeping tables when both persistence and DB-casbin are used.

## 11. Verification

- `go test -race ./...` green (Postgres pkgs `-p 1`); ≥85% line coverage on
  `casbinauthz` and `internal/authz/casbin`.
- `golangci-lint run ./...` clean.
- casbin absent from `engine`/`model`/`runtime`/`internal/persistence/*` (confinement
  guard); pgx-only (no gorm/go-pg/sqlx/ent added to `go.mod`); casbin still pinned
  v2.135.0.
- The existing authz/casbinauthz test suites stay green (string/enforcer
  constructors unchanged).

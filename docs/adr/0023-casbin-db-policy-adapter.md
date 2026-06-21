# 23. pgx-native DB casbin policy adapter with a LISTEN/NOTIFY watcher

- Status: Accepted
- Date: 2026-06-22

## Context

The Authorization sub-project (ADR-0010) added a hybrid casbin `authz.Authorizer`
but kept policy **adapter-agnostic**: `casbinauthz` accepts model+policy strings or
a consumer-built `*casbin.SyncedEnforcer`. Neither loads policy from a database, so
a multi-node deployment has no shared authoritative policy store and a policy change
on one node is invisible to the others. ADR-0010 explicitly deferred "a DB policy
adapter (with a watcher for multi-node reload)".

casbin defines a small `persist.Adapter` interface (`LoadPolicy`/`SavePolicy` +
`AddPolicy`/`RemovePolicy`/`RemoveFilteredPolicy` for auto-save) and a
`persist.Watcher` interface (`SetUpdateCallback`/`Update`/`Close`) for exactly this.
The locked stack is **pgx v5 + pgxpool + goose v3 + embed.FS** (CLAUDE.md), and
casbin is pinned at v2.135.0 with **no adapter dependency**. The two well-known
community adapters bring a second data layer: `casbin-pg-adapter` uses `go-pg`,
`gorm-adapter` uses GORM — adopting either adds an ORM/driver the project has
deliberately avoided, a stack change with no payoff here. We already built
Postgres `LISTEN`/`NOTIFY` mechanics for the outbox relay (ADR-0022) that a policy
watcher can reuse.

## Decision

Implement a **hand-rolled pgx-native casbin policy adapter and a LISTEN/NOTIFY
watcher**, confined to the casbin packages, behind the existing `casbinauthz`
façade.

- **`pgAdapter`** (`internal/authz/casbin/`) implements `persist.Adapter` over the
  conventional `casbin_rule(id, ptype, v0..v5 TEXT)` table using `*pgxpool.Pool`:
  `LoadPolicy` (`SELECT` → `persist.LoadPolicyArray` per row), `SavePolicy`
  (tx: `DELETE` + bulk insert), and `AddPolicy`/`RemovePolicy`/`RemoveFilteredPolicy`
  so casbin auto-save persists mutations. A single `padRule` helper centralizes the
  rule↔(v0..v5) mapping.
- **`pgWatcher`** (`internal/authz/casbin/`) implements `persist.Watcher`: `Update()`
  emits `pg_notify('wrkflw_casbin_policy', '<nodeID>')`; a listener goroutine
  (`pool.Acquire` + `LISTEN` + `WaitForNotification` + cancellable reconnect
  backoff, mirroring `relay.listenLoop`) invokes the update callback
  (`enforcer.LoadPolicy`) on every notification **whose payload ≠ this node's id**,
  so a node ignores the echo of its own write. `Close()` stops the goroutine and
  releases the connection.
- **Migration** ships as `internal/authz/casbin/migrations/0001_casbin_rule.sql`,
  applied by `casbinauthz.MigrateCasbin(ctx, pool)` through a goose `Provider` on a
  **separate version table** (`casbin_goose_db_version`) so it is independent of the
  persistence migration set and runs only when a consumer opts into DB-backed
  policy. Never auto-run on import; the adapter does not `CREATE TABLE IF NOT EXISTS`.
- **Façade:** `casbinauthz.NewCasbinAuthorizerFromDB(ctx, pool, ...DBOption)
  (authz.Authorizer, io.Closer, error)` builds the model (default `DefaultModel`),
  the adapter, a `SyncedEnforcer` (auto-loads policy), wires the watcher
  (`enforcer.SetWatcher` + `LoadPolicy` callback), and wraps the enforcer in the
  unchanged hybrid evaluator (`internalcasbin.New`). It returns the stable
  `authz.Authorizer` interface plus an `io.Closer` for the watcher — never the
  internal concrete types (ADR-0008/0010). Options: `WithModel`, `WithoutWatcher`,
  `WithWatcherChannel`, `WithNodeID`.

casbin stays imported only in `casbinauthz/` and `internal/authz/casbin/`; pgx is
allowed there. No casbin adapter dependency, no ORM, no second driver is added.

## Consequences

**Easier:** policy lives in one `casbin_rule` table all nodes share; mutations
persist through the casbin auto-save API; a change on one node propagates to the
others via `NOTIFY`, which reload — real multi-node policy coherence, the point of
a DB adapter. The hybrid authorization semantics (role hierarchy → privilege →
attribute, fail-closed) are reused verbatim; only the policy *source* changes. The
feature is fully opt-in (string/enforcer constructors and existing consumers are
untouched; the migration is explicit). The stack stays pgx-only and casbin stays
confined and pinned.

**Harder / trade-offs:** a watcher pins one `LISTEN` connection per process (same
cost as the relay listener and advisory-lock ownership). `NOTIFY` is best-effort:
a lost notification only delays a remote reload (recoverable via the existing
`ReloadPolicy` or the next change), never corrupts policy — correctness never
depends on it, mirroring ADR-0022. The whole policy reloads on every change (no
`FilteredAdapter`/incremental `WatcherEx` updates — deferred for large policy
sets). A separate `casbin_goose_db_version` table appears alongside the persistence
one — intentional independence, documented for operators. No policy-management
transport surface is provided; mutation is via the casbin enforcer API (an admin
REST/gRPC surface is a separate track).

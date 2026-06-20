# 6. PostgreSQL snapshot-as-JSONB storage with projected columns

- Status: Accepted
- Date: 2026-06-21

## Context

The persistence sub-project must store `engine.InstanceState` — a single large,
deeply nested aggregate (variables map, token slice with payload maps, node-visit
history, human tasks, timers, armed events, boundary arms, a scope tree with
compensation records, event-subprocess arms, a compensation cursor, ID counters).
The engine core already treats this aggregate as one unit: it is wholesale
deep-cloned on every `Step` and is the deterministic source of truth.

Two storage shapes were considered:

1. **Normalized child tables** — tokens, tasks, timers, scopes each in their own
   table, reassembled by joins. Best-in-class for ad-hoc query/indexing and admin
   monitoring; worst for object↔relational mapping cost, migration churn, and
   transactional fan-out per transition.
2. **Whole-aggregate JSONB snapshot** — one `JSONB` column mapping 1:1 to the Go
   struct. One atomic `UPDATE ... SET snapshot=$1` per transition; near-zero DDL
   churn (shape changes live in Go); O(1) recovery. Costs: whole-row lock per
   update, TOAST write amplification, coarse indexing.

Research into mature engines (Temporal, Camunda 7/Zeebe, Conductor, AWS Step
Functions) shows the snapshot-of-current-state + journal-of-changes hybrid is the
Zeebe/Temporal shape and the best fit for a pure `Step(def, state, trigger)`
core, since it gives O(1) recovery and replay/audit without Temporal-style
deterministic-code-replay obligations. CLAUDE.md makes library ergonomics and
minimal migration effort load-bearing.

## Decision

Store each instance as a **full `JSONB` snapshot (source of truth) plus a small
set of plain, engine-written, indexed projected columns**:

- `wrkflw_instances(instance_id PK, def_id, def_version, status, snapshot JSONB,
  version BIGINT, started_at, ended_at, updated_at)`, with a partial index on
  `status WHERE ended_at IS NULL` for admin monitoring.
- The journal (`wrkflw_journal`) and outbox (`wrkflw_outbox`) are separate tables
  (schema in the design spec §4).

Projected columns are written **explicitly by the runtime**, not as `GENERATED`
columns: a generated column re-indexes on every snapshot change, whereas
plain columns keep their indexes churning only when the projected value changes.

We do **not** normalize the aggregate tree in v1. If one child (most likely human
tasks or armed timers for SLA scans) later becomes a heavy independent-query
target, that single child is promoted to its own table — the tree is not
normalized up front.

Migrations are embedded `.sql` via `embed.FS`, executed by the consumer through
`persistence.Migrate(ctx, db)` using **`pressly/goose`** (`SetBaseFS`); never
auto-run on import.

## Consequences

**Easier:** trivial Go↔row mapping; atomic single-row save; near-zero migration
churn as the state shape evolves; O(1) load/recovery; admin queries hit projected
columns without parsing JSONB (GIN on `snapshot` available later if needed).

**Harder / trade-offs:** the snapshot changes every transition, so it never gets
Postgres's "unchanged out-of-line value" TOAST discount — each commit rewrites
the row and its TOAST pages and produces dead tuples. Mitigations (tuning
follow-ups, not blockers): keep snapshots small (optional history cap), lower
table `fillfactor` to favour HOT updates, watch autovacuum on the instance table
and its TOAST table. Variable-level ad-hoc queries are not first-class in v1
(would need GIN/expression indexes on the JSONB). Numbers in `map[string]any`
decode as `float64` unless the codec uses `json.Decoder.UseNumber()`.

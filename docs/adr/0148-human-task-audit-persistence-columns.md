# 148. Human-task audit persistence: normalized columns + migration

Status: Accepted — 2026-07-27. Spec:
[docs/specs/2026-07-27-processinstance-audit-view-and-idgen.md](../specs/2026-07-27-processinstance-audit-view-and-idgen.md).
Part of the ADR-0144…0149 delivery. Persists the model of [ADR-0147]. Store
conventions from [ADR-0081].

## Context

A `HumanTask` is persisted in **two** places: inside the instance `snapshot` JSONB
blob (untagged Go structs — additive fields ride along for free) and as
**normalized columns** in `wrkflw_human_task` (`humantask_store.go:68` column list:
`task_id, instance_id, node_id, state, claimed_by, eligibility, candidates,
vars, created_at, due_at`), maintained via `Upsert` and read via `scanTask`. The
normalized table backs admin/authz task queries (`AssignedTo`, `ClaimableBy`,
`Get`). The ADR-0147 audit fields (`Candidates []authz.Actor`, `Claim`,
`Completion`) therefore survive in the snapshot but are **invisible to admin
queries** unless the normalized table also carries them. The store is one neutral
implementation over a dialect (Postgres/MySQL/SQLite — ADR-0081).

## Decision

Extend `wrkflw_human_task` so the audit is queryable, via one migration across all
three dialects:

1. **`candidates`** column: change from an id-only encoding to **rich JSON**
   (`[]authz.Actor`), replacing the previous id-only column.
2. **`claim`** column: nullable JSON encoding `{actor, timestamp}`. Keep a plain,
   **application-maintained `claimed_by`** scalar column (written by `Upsert` from
   `claim.actor.id`) for `AssignedTo`'s indexed `WHERE claimed_by = ?` lookup —
   fully portable and indexable across all three dialects with **no** per-dialect
   JSON-path DDL or generated-column constraints. (`ClaimableBy` already loads
   unclaimed rows and filters candidates in Go, so no candidate index is needed —
   rule-#9 audit m2.)
3. **`completion`** column: nullable JSON encoding `{actor, timestamp, outcome?, note?}`.
4. Update `humantask_store.go` `Upsert` (column list + bind), `scanTask`, and the
   `AssignedTo`/`ClaimableBy` predicates to read the new shapes. A round-trip test
   per dialect (testcontainers for Postgres/MySQL; pure-Go SQLite) proves the audit
   persists and reads back.

Because the library is **pre-release**, the migration is a clean schema change with
**no data backfill**; fixtures are rewritten to the new shape.

## Consequences

- **Positive:** admin/authz task queries see the full audit (eligible actors,
  claim, completion) directly from SQL, not only from a rehydrated snapshot.
- **Negative:** `AssignedTo`/`ClaimableBy` currently filter on `claimed_by` /
  `candidates` (id strings). Moving to JSON requires either a derived
  `claimed_by`/candidate-id index column or a dialect-portable JSON-path predicate;
  to stay portable and indexable across Postgres/MySQL/SQLite, keep a lightweight
  generated/companion id column for the hot lookups and store the rich JSON
  alongside. Settled in implementation; the round-trip + query tests gate it.
- **Negative / breaking:** the `wrkflw_human_task` schema changes; any pre-existing
  rows are incompatible. Pre-release ⇒ no backfill, migrations reset.
- **Risk:** three dialects must stay behaviourally identical — covered by the
  shared `dbtest` harness running the same assertions against each backend.

## Implementation amendments (2026-07-27, rule-#9 re-audit)

1. **Encode with `json.Marshal`, never hand-written keys.** ADR-0147 amendment #3
   moved the wire contract onto the types: `humantask.Claim` is
   `{actor, timestamp}` and `humantask.Completion` is
   `{actor, timestamp, outcome?, note?}` — the timestamp key is **`timestamp`**,
   not `at`. The Decision text above has been corrected; marshal the Go types
   rather than composing the JSON by hand so the two cannot drift again.
2. **The column is `task_id`.** ADR-0144's amendment renamed `task_token`
   repo-wide, including the column and `humanTaskColumns`.
3. **`candidates` is already rich JSON.** `Upsert` marshals `[]authz.Actor`
   today, so Decision #1 is a no-op at the DDL level — only the read path's
   expectations change.
4. **The interim state fabricates a claim.** Until these columns land, `scanTask`
   rebuilds `Claim{Actor: {ID: claimed_by}}` with a **zero `At`**, and
   `Completion` is dropped entirely. That is fabricated audit data, not missing
   data: a caller checking `Claim != nil` gets a record asserting the task was
   claimed at `0001-01-01`. Closing this is the whole point of this ADR; until it
   lands, the store is a lossy round-trip for the delivery's headline feature.
5. **Round-trip tests must be able to fail.** The existing conformance fixtures
   only ever store a claim with a zero `At` and no completion, so the loss is
   invisible. The RED for this phase must persist a claim with a **non-zero
   timestamp and populated roles/attributes** plus a non-nil completion, on all
   three dialects.
6. **Update the dialect `UpsertTask()` column lists too.** `internal/persistence/
   dialect/{postgres,mysql,sqlite}.go` each enumerate the columns alongside the
   three `0001_init.sql` files (edited in place — ADR-0132 one-file-per-dialect
   rule). `TestMigrationParity_LogicalSchemaConverges` fails if a dialect is missed.

## Amendment 2 (2026-07-28): normalize the audit scalars out of JSON

### Context

Amendment 1 stored the whole `Claim`/`Completion` as opaque JSON. Implementing the
degrade-vs-fail policy for an undecodable column exposed two problems with that:

1. **A JSON blob forces an all-or-nothing decode.** Losing it loses the timestamp,
   the actor id, the outcome and the note together — even though all four are
   scalars that cannot meaningfully be "corrupt" if stored in typed columns.
2. **The audit is unqueryable.** "Every task completed with outcome `reject` last
   week" requires scanning every row and decoding JSON in Go. For a feature whose
   entire purpose is audit, that is the wrong shape.

A third observation reframed the risk: **Postgres (`JSONB`) and MySQL (`JSON`)
validate syntax at the column**, so malformed JSON is impossible there and only
SQLite's `TEXT` can hold garbage. The realistic failure on the primary database is
therefore not corruption but **our own struct evolution** — a shape mismatch after
someone edits `humantask.Claim`.

### Decision

Store the scalars as typed columns and keep JSON only for the genuinely variable
remainder — the actor's roles and attributes.

| Column | Type | Meaning |
|---|---|---|
| `claimed_by` | text, indexed | `claim.actor.id`; unchanged, still backs `AssignedTo` |
| `claimed_at` | timestamp, NULL | **presence discriminator**: NULL ⇔ `Claim == nil` |
| `claim_actor` | JSON, NULL | `{roles, attributes}` remainder of the claimant |
| `completed_by` | text, NULL | `completion.actor.id` |
| `completed_at` | timestamp, NULL | **presence discriminator**: NULL ⇔ `Completion == nil` |
| `outcome` | text, NULL | `completion.outcome` |
| `note` | text, NULL | `completion.note` |
| `completion_actor` | JSON, NULL | `{roles, attributes}` remainder of the completer |

The `claim` and `completion` JSON columns introduced by amendment 1 are **removed**
(they never shipped; no migration path is owed).

Presence is keyed on the **timestamp**, not on the id: a claim always has a time,
and keying on `claimed_by != ''` would resurrect the fabricated-claim bug that
amendment 1 §4 recorded. Reconstruction is faithful — `Actor{ID: claimed_by,
Roles/Attributes: claim_actor}` — not synthesized from a single column.

This makes the load-bearing/descriptive split **physical** rather than a
convention: the routing and reporting fields are typed columns that cannot
shape-mismatch, and the only decodable-and-therefore-degradable data left is the
actor remainder, whose loss costs display detail and nothing else.

### Consequences

- **Positive:** the audit becomes queryable — outcome, completion time, completer
  and claim time are all indexable SQL, so reporting needs no full scan.
- **Positive:** most of the corruption/version-skew surface becomes
  unrepresentable rather than handled; what remains degrades display only.
- **Positive:** the degrade policy (list queries drop an unreadable audit and keep
  serving the row; point reads fail loudly) now has a much smaller blast radius.
- **Negative:** eight columns instead of two. Accepted: they are all scalars the
  audit view already renders, and six of them are directly queryable.
- **Negative:** `scanTask` grows more scan targets. Mitigated by the ordering
  contract already in place — load-bearing columns decode first and fatally,
  descriptive ones last and degradably.

### Amendment 2 — points settled during implementation

Raised by the implementer as under-specified; adjudicated here so the ADR is the
record rather than the code.

1. **A zero audit timestamp is rejected at `Upsert`.** Presence is keyed on the
   timestamp, so a record without one would persist as "claimed, at no time"; it
   is also unstorable (MySQL `DATETIME` starts at 1000-01-01, surfacing as an
   opaque driver range error). `humantask.TaskStore` is a public port, so a
   caller assembling a `Claim` by hand gets a descriptive error instead. The
   engine never produces one — every record is stamped from `Trigger.OccurredAt`
   — so this guards consumer and test code.
2. **The timestamp columns are descriptive, not load-bearing.** On SQLite they
   are TEXT and can hold an unparseable value. They are classified with the actor
   remainder: a list query degrades the whole audit record, a point read fails.
   Routing is unaffected because `AssignedTo` filters on the `claimed_by` column,
   not on the decoded `Claim`.
3. **`ClaimableBy` has no degrade surface by construction.** An Unclaimed row has
   NULL `claimed_at` and NULL `completed_at`, so there is nothing to decode. The
   policy is exercised through `AssignedTo` (same `query()` path). This is a
   consequence of normalization, not a gap.
4. **`outcome`/`note` are bound verbatim whenever the completion exists**, so
   `completed_at IS NOT NULL AND outcome IS NULL` never occurs for rows we write
   and `WHERE outcome = ''` stays meaningful.
5. **`claim_actor` is `{}`, not NULL, for a role-less actor**, so
   `claim_actor IS NULL` is not a usable predicate; presence stays on the
   timestamp.
6. **No indexes beyond `claimed_by`.** The decision claims *indexable*, not
   *indexed*: `outcome`/`completed_at` are queryable, and an index is a reporting
   decision a consumer should make against their own access pattern rather than
   one we impose on every deployment.
7. **MySQL widths**: `completed_by`/`outcome` are `VARCHAR(255)` (matching
   `claimed_by`), `note` is `TEXT`. Outcomes are drawn from a node's declared
   `Outcomes` set, so 255 is ample.
8. **Degradation is counted, not only logged** — `wrkflw_human_task_audit_drops_total`
   (labelled by `op` and `column`), configurable via `WithHumanTaskMeterProvider`
   and defaulting to the OTel global provider. A silently-degrading row that only
   ever produced a WARN was not operationally actionable.

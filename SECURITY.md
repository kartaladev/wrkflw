# Security Policy

## Supported versions

`wrkflw` is pre-1.0. Until a `v1.0.0` release, security fixes are applied to the `main` branch and
the most recent tagged release only. See [`STABILITY.md`](STABILITY.md) for the API-stability policy.

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Instead, report them privately by either:

- using GitHub's **["Report a vulnerability"](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing/privately-reporting-a-security-vulnerability)**
  private advisory flow on this repository, or
- emailing **security@kartala.id** (or **zaky@kartala.id**).

Please include:

- a description of the vulnerability and its impact,
- the affected version / commit,
- step-by-step reproduction (a failing test or minimal program is ideal), and
- any suggested remediation.

## What to expect

- We aim to acknowledge a report within **3 business days**.
- We will work with you to confirm the issue, assess severity, and prepare a fix.
- We follow **coordinated disclosure**: please give us a reasonable window to release a fix before
  any public disclosure. We will credit reporters who wish to be named once a fix ships.

## Scope notes for embedders

`wrkflw` is an embeddable library; the consumer owns the deployed surface. A few responsibilities sit
with the embedder and are documented rather than enforced by default:

- **Authorization** of the admin HTTP routes. Admin endpoints are default-absent by
  composition (ADR-0095): they exist only when you mount `AdminRoutes` on a router group
  that your own auth middleware already protects. They carry no built-in authentication.
- **TLS** for the database, SMTP, and transport servers.
- **Untrusted definitions** — enable the expression-evaluation timeout (injectable evaluator) before
  loading process definitions from untrusted input.

## Request body limits (ADR-0186)

Every HTTP route group mounted from `transport/http/{stdlib,gin,fiber}` bounds the request body at
**1 MiB by default**. Set your own with the adapter option, or pass a **non-positive** value to
disable the bound entirely:

```go
stdlib.Mount(mux, svc, stdlib.WithMaxBodyBytes(4<<20))  // 4 MiB
stdlib.Mount(mux, svc, stdlib.WithMaxBodyBytes(0))      // disabled
```

When the cap is active, the body read is also bounded in **time** — **30 s by default**, and a
non-positive value disables it:

```go
stdlib.Mount(mux, svc, stdlib.WithBodyReadTimeout(10*time.Second))
```

⚠ **This deadline overwrites the whole-request deadline `net/http` derives from
`http.Server.ReadTimeout`** for the duration of the body read. If you set a *shorter*
`ReadTimeout`, it is silently extended to `BodyReadTimeout` while the body is being read — keep
`ReadTimeout` no shorter than `BodyReadTimeout`. There is no `fiber` equivalent: fasthttp has
already read the body before the route group is entered.

Oversize requests are answered **413** with a static body that deliberately does **not** name the
configured limit. The bound applies to what is read from the wire, and each adapter's JSON decoding
is otherwise unchanged.

Five properties are documented rather than enforced. Read them before relying on the cap:

1. **The cap bounds SIZE; `BodyReadTimeout` bounds TIME — and you should set `ReadTimeout` too.**
   The body is read to completion before it is parsed, so without a deadline a slow client holds a
   handler open indefinitely. Measured: a request declaring `Content-Length: 400000` that sends a
   complete JSON value and then stalls returned in **0 s** before this change and **never returned**
   after it. That is why `BodyReadTimeout` defaults to **30 s** on `stdlib` and `gin` rather than
   being left to documentation. ⚠ It is **not** a substitute for `http.Server.ReadTimeout`, which
   also covers requests whose body the cap never wraps — all three `examples/` now set both.
2. **Peak memory is per-adapter and is NOT `MaxBodyBytes × in-flight`.** On `stdlib` and `gin` it is
   roughly **2.12 × the cap** per in-flight request (buffer growth), **including for requests that
   are ultimately rejected**. On `fiber` it is governed by **`fiber.Config.BodyLimit`** (default
   4 MiB), not by `MaxBodyBytes` at all, because fasthttp reads and limits the body before the route
   group is entered — ⚠ and for a **compressed** body, roughly **twice** that, because the size
   pre-check and the JSON binder each decompress once. Nothing here bounds concurrency.
3. **A 413 carries no correlation id and writes no log record.** Error-body correlation and 4xx
   logging are a separate, later delivery; today `writeErr` logs only at `status >= 500`.
4. **The cap is per route group, so pass the option to every group you mount.** `Mount` covers the
   instance, message and task groups — **6 of the 13 decode sites per adapter**. The remaining
   sites, **including the optional-body admin resolve-incident route**, live behind `AdminRoutes`,
   which ADR-0095 keeps out of `Mount` by design. `MountHealth` forwards no options.
   ```go
   stdlib.Mount(mux, svc, stdlib.WithMaxBodyBytes(n))
   stdlib.AdminRoutes{Svc: svc}.Customize(mux, stdlib.WithMaxBodyBytes(n))  // do this too
   ```
5. **On `fiber`, a body whose WIRE size exceeds `fiber.Config.BodyLimit` never reaches the route
   group.** fasthttp refuses it first, so the client receives a framework `text/plain` response with
   **no `ErrorBody` envelope, no correlation id and no log line**. Raising `fiber.Config.BodyLimit`
   above your cap restores identical behaviour across all three adapters — measured.
   ⚠ **A body whose DECOMPRESSED size exceeds `BodyLimit` does reach the route group**, and is
   answered **413 with a normal `ErrorBody`** — `fiber` sets that status itself during decoding and
   the adapter now preserves it instead of overwriting it with a 400.

⚠ **A known, pre-existing divergence, unrelated to the cap:** a body containing a complete JSON
value followed by trailing bytes is accepted by `stdlib` and `gin` (`json.Decoder` stops at the
first value) and rejected with **400** by `fiber` (`Bind().JSON` uses `json.Unmarshal`, which is
strict). This predates ADR-0186 and is now pinned by the parity suite.

These and other hardening items are tracked in `docs/plans/2026-06-30-production-readiness-backlog.md`.

<!-- BEGIN at-rest (generated) -->

## Data at rest

Nothing `wrkflw` stores is encrypted, redacted, or tamper-evident. This section is generated and machine-checked (ADR-0187) from the migrations embedded in this module — consumer-supplied migrations are out of scope. If it disagrees with the schema, `TestSecurityMdInSync` fails the build; edit `internal/atrest/render.go`, not this file, and regenerate with `scripts/gen-at-rest.sh`.

⚠ **This table describes a FRESH database.** goose keys migrations by version and stores no checksum, so an in-place edit of an already-applied migration never re-applies to an already-migrated deployment — a long-lived deployment can hold a schema this table does not describe.

### Classes

Every stored column carries one of six classes, assigned by logical role rather than physical type:

- **`freeform`** — unstructured, application-authored content: JSON blobs, notes, error text, snapshots. No shape is assumed; it may hold arbitrary consumer data, including PII the consumer put there.
- **`actor`** — identifies a human principal. Treat as personal data.
- **`policy`** — authorization policy data at rest. Its compromise affects access-control decisions across the deployment.
- **`reference`** — an identifier or foreign-key-shaped value: instance/definition/task ids, topic names, dedup keys, a worker lease owner. Not a person.
- **`scalar`** — a small structured value: a status, a kind, a count, a version number. Generally low sensitivity on its own.
- **`timestamp`** — a point in time. Generally low sensitivity on its own, though timing can support correlation with other data.

### Columns

`keyed` is a machine-derived, per-dialect **lower bound**: it records `PRIMARY KEY`/`UNIQUE` membership and every `CREATE INDEX` (including a partial index's `WHERE`-predicate columns) found in this module's migrations, however that key/index is spelled — an inline column modifier, a table-level `PRIMARY KEY`/`UNIQUE`/`KEY`/`INDEX` clause, a named `CONSTRAINT`, or a `CREATE UNIQUE INDEX` (which records both `UNIQUE` and `index`). The parser fails the build on a statement it does not recognize at all, on a table-level constraint naming a column its own `CREATE TABLE` never declared, and on any clause it cannot account for between a `CREATE INDEX`'s table name and its column list. One gap remains open by construction and derives no key while raising no error: a `CREATE INDEX` whose target table or column is declared in a DIFFERENT migration file, which no migration in this module does today. It is also blind to any `WHERE`/`ORDER BY` a query places on a column that carries no index, and `FOREIGN KEY` columns are deliberately excluded from `keyed` — read "machine-checked" as "every recognized PRIMARY KEY/UNIQUE/INDEX construct is captured", not as "every index-like thing in the schema is". `—` marks a column present in that dialect with no recorded key or index; `n/a` marks a column that does not exist in that dialect at all (every `casbin_rule` column, in mysql and sqlite).

The `column` cell holds the **canonical** name, which is what the per-dialect key sets are compared under. Where a dialect declares that column under a different identifier, its type cell discloses it as "<type> (declared `<name>`)" — MySQL's `wrkflw_journal` payload column is the one instance today, because `trigger` is a MySQL reserved word. A migration you write against that dialect must use the DECLARED name.

⚠ **`keyed` is a lower bound on the column, not on the query.** `casbin_rule.{ptype,v0,v1,v2,v3,v4,v5}` are class `policy`; `internal/authz/casbin/pg_adapter.go`'s `RemovePolicy` deletes rows by filtering **all seven** of those columns with equality (`WHERE ptype=$1 AND v0=$2 AND … AND v5=$7`), regardless of which of them this table marks keyed. Encrypting any of the seven non-deterministically breaks that delete.

| table | column | class | postgres type | postgres keyed | mysql type | mysql keyed | sqlite type | sqlite keyed |
|---|---|---|---|---|---|---|---|---|
| casbin_rule | id | scalar | BIGSERIAL | PK | n/a | n/a | n/a | n/a |
| casbin_rule | ptype | policy | TEXT | index | n/a | n/a | n/a | n/a |
| casbin_rule | v0 | policy | TEXT | — | n/a | n/a | n/a | n/a |
| casbin_rule | v1 | policy | TEXT | — | n/a | n/a | n/a | n/a |
| casbin_rule | v2 | policy | TEXT | — | n/a | n/a | n/a | n/a |
| casbin_rule | v3 | policy | TEXT | — | n/a | n/a | n/a | n/a |
| casbin_rule | v4 | policy | TEXT | — | n/a | n/a | n/a | n/a |
| casbin_rule | v5 | policy | TEXT | — | n/a | n/a | n/a | n/a |
| wrkflw_call_links | child_instance_id | reference | TEXT | PK, index | VARCHAR(255) | PK, index | TEXT | PK, index |
| wrkflw_call_links | claimed_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | index | TEXT | index |
| wrkflw_call_links | claimed_by | reference | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_call_links | created_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_call_links | depth | scalar | INT | — | INT | — | INTEGER | — |
| wrkflw_call_links | error | freeform | TEXT | — | TEXT | — | TEXT | — |
| wrkflw_call_links | notified_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | index | TEXT | index |
| wrkflw_call_links | output | freeform | JSONB | — | JSON | — | TEXT | — |
| wrkflw_call_links | parent_command_id | reference | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_call_links | parent_def_id | reference | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_call_links | parent_def_version | scalar | INT | — | INT | — | INTEGER | — |
| wrkflw_call_links | parent_instance_id | reference | TEXT | index | VARCHAR(255) | index | TEXT | index |
| wrkflw_call_links | status | scalar | TEXT | index-predicate | VARCHAR(50) | index | TEXT | index |
| wrkflw_chain_links | created_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_chain_links | outcome | reference | TEXT | PK | VARCHAR(255) | PK | TEXT | PK |
| wrkflw_chain_links | predecessor_definition_ref | reference | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_chain_links | predecessor_instance_id | reference | TEXT | PK | VARCHAR(255) | PK | TEXT | PK |
| wrkflw_chain_links | start_vars | freeform | JSONB | — | JSON | — | TEXT | — |
| wrkflw_chain_links | successor_definition_ref | reference | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_chain_links | successor_instance_id | reference | TEXT | index | VARCHAR(255) | index | TEXT | index |
| wrkflw_definitions | created_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_definitions | def_id | reference | TEXT | PK | VARCHAR(255) | PK | TEXT | PK |
| wrkflw_definitions | definition | freeform | JSONB | — | JSON | — | TEXT | — |
| wrkflw_definitions | version | scalar | INT | PK | INT | PK | INTEGER | PK |
| wrkflw_human_task | candidates | actor | JSONB | — | JSON | — | TEXT | — |
| wrkflw_human_task | claim_actor | actor | JSONB | — | JSON | — | TEXT | — |
| wrkflw_human_task | claimed_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_human_task | claimed_by | actor | TEXT | index | VARCHAR(255) | index | TEXT | index |
| wrkflw_human_task | completed_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_human_task | completed_by | actor | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_human_task | completion_actor | actor | JSONB | — | JSON | — | TEXT | — |
| wrkflw_human_task | created_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_human_task | due_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_human_task | eligibility | policy | JSONB | — | JSON | — | TEXT | — |
| wrkflw_human_task | instance_id | reference | TEXT | index | VARCHAR(255) | index | TEXT | index |
| wrkflw_human_task | node_id | reference | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_human_task | note | freeform | TEXT | — | TEXT | — | TEXT | — |
| wrkflw_human_task | outcome | reference | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_human_task | state | scalar | TEXT | index | VARCHAR(64) | index | TEXT | index |
| wrkflw_human_task | task_id | reference | TEXT | PK | VARCHAR(255) | PK | TEXT | PK |
| wrkflw_human_task | vars | freeform | JSONB | — | JSON | — | TEXT | — |
| wrkflw_instances | def_id | reference | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_instances | def_version | scalar | INT | — | INT | — | INTEGER | — |
| wrkflw_instances | ended_at | timestamp | TIMESTAMPTZ | index-predicate | DATETIME(6) | — | TEXT | — |
| wrkflw_instances | instance_id | reference | TEXT | PK, index | VARCHAR(255) | PK, index | TEXT | PK, index |
| wrkflw_instances | snapshot | freeform | JSONB | — | JSON | — | TEXT | — |
| wrkflw_instances | started_at | timestamp | TIMESTAMPTZ | index | DATETIME(6) | index | TEXT | index |
| wrkflw_instances | status | scalar | SMALLINT | index | SMALLINT | index | INTEGER | index |
| wrkflw_instances | updated_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_instances | version | scalar | BIGINT | — | BIGINT | — | INTEGER | — |
| wrkflw_journal | applied_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_journal | instance_id | reference | TEXT | PK | VARCHAR(255) | PK | TEXT | PK |
| wrkflw_journal | kind | scalar | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_journal | occurred_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_journal | seq | scalar | BIGINT | PK | BIGINT | PK | INTEGER | PK |
| wrkflw_journal | trigger | freeform | JSONB | — | JSON (declared `trigger_`) | — | TEXT | — |
| wrkflw_outbox | created_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_outbox | dedup_key | reference | TEXT | UNIQUE | VARCHAR(255) | UNIQUE | TEXT | UNIQUE |
| wrkflw_outbox | definition_ref | reference | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_outbox | id | scalar | BIGSERIAL | PK, index | BIGINT AUTO_INCREMENT | PK, index | INTEGER | PK, index |
| wrkflw_outbox | instance_id | reference | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_outbox | last_error | freeform | TEXT | — | TEXT | — | TEXT | — |
| wrkflw_outbox | next_attempt_at | timestamp | TIMESTAMPTZ | index | DATETIME(6) | index | TEXT | index |
| wrkflw_outbox | payload | freeform | JSONB | — | JSON | — | TEXT | — |
| wrkflw_outbox | published_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_outbox | retry_count | scalar | INT | — | INT | — | INTEGER | — |
| wrkflw_outbox | status | scalar | TEXT | index-predicate | VARCHAR(50) | index | TEXT | index |
| wrkflw_outbox | topic | reference | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_processed_message | message_id | reference | TEXT | PK | VARCHAR(255) | PK | TEXT | PK |
| wrkflw_processed_message | processed_at | timestamp | TIMESTAMPTZ | — | DATETIME(6) | — | TEXT | — |
| wrkflw_processed_message | subscriber | reference | TEXT | PK | VARCHAR(255) | PK | TEXT | PK |
| wrkflw_timers | def_id | reference | TEXT | — | VARCHAR(255) | — | TEXT | — |
| wrkflw_timers | def_version | scalar | INT | — | INT | — | INTEGER | — |
| wrkflw_timers | instance_id | reference | TEXT | PK, index | VARCHAR(255) | PK, index | TEXT | PK, index |
| wrkflw_timers | kind | scalar | SMALLINT | — | SMALLINT | — | INTEGER | — |
| wrkflw_timers | next_run | timestamp | TIMESTAMPTZ | index | DATETIME(6) | index | TEXT | index |
| wrkflw_timers | timer_id | reference | TEXT | PK, index | VARCHAR(255) | PK, index | TEXT | PK, index |
| wrkflw_timers | trigger_kind | scalar | SMALLINT | — | SMALLINT | — | INTEGER | — |
| wrkflw_timers | trigger_payload | freeform | JSONB | — | JSON | — | TEXT | — |

### What this table does not tell you

The following `actor`-classed column(s) are indexed; encrypting any of them non-deterministically breaks whatever equality lookup assumes deterministic ciphertext:
- `wrkflw_human_task.claimed_by` IS indexed, so encrypting it non-deterministically costs the `humantask.TaskStore.AssignedTo` lookup.

`casbin_rule` is conditionally present: it exists only in a Postgres deployment that has called `casbinauthz.MigrateCasbin`, which is never run automatically — the `FromDB` policy source requires that call, and any deployment that has made it keeps the table whatever policy source it later wires.

⚠ Authorization policy is durable at rest in **three** places, not one. Encrypting only the `policy`-classed columns is therefore not enough:
- `casbin_rule` holds the deployment's casbin policy rules verbatim, one rule per row (class `policy`).
- `wrkflw_human_task.eligibility` holds the per-task eligibility rule the four `Authorize` sites in `runtime/task/service.go` evaluate (class `policy`).
- `wrkflw_definitions.definition` is class `freeform` because it holds the whole serialized process definition — but every node's `eligible_roles`, `eligible_privileges` and `eligible_expr` are serialized INSIDE that JSON, so encrypting only the `policy`-classed columns leaves per-node eligibility rules in the clear.

No column-level codec and no hash-chained journal ship with this delivery (ADR-0187 D10, the non-goals — see `docs/specs/2026-08-22-at-rest-posture.md` § D10) — nothing in this table implies one exists.

<!-- END at-rest -->

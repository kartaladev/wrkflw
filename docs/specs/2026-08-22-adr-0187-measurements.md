# ADR-0187 §AT-REST — executed measurements

**Date:** 2026-08-22 · **Base commit:** `afef1404` · **Branch:** `design/at-rest-posture`

Every factual claim about current behaviour in `2026-08-22-at-rest-posture.md` and
`docs/adr/0187-at-rest-classification-is-machine-checked.md` is recorded here with the command that
produced it and its verbatim output. Claims that could not be executed are marked
`ASSUMPTION (unverified)` in the spec and are **not** in this file.

⚠ **This file is an INPUT to the rule-#9 audit, not a conclusion of it.** Attack it. On ADR-0186
findings landed inside the bundle's own evidence file in two separate rounds, and in one the
author's own probe refuted a real audit finding and was itself wrong.

---

## E1 — Migration discovery: there are FOUR migration directories, not three

```
$ find . -name '*.sql' | sort
./internal/authz/casbin/migrations/0001_casbin_rule.sql
./internal/persistence/store/migrations/mysql/0001_init.sql
./internal/persistence/store/migrations/postgres/0001_init.sql
./internal/persistence/store/migrations/sqlite/0001_init.sql
```

```
$ grep -rn "go:embed" --include='*.go' .
internal/persistence/store/migrate_mysql.go:9://go:embed migrations/mysql/*.sql
internal/persistence/store/migrate_postgres.go:10://go:embed migrations/postgres/*.sql
internal/persistence/store/migrate_sqlite.go:14://go:embed migrations/sqlite/*.sql
internal/authz/casbin/migrate.go:14://go:embed migrations/*.sql
```

**Confirms F5.** Four embedded migration sets. A glob over `**/migrations/*.sql` finds all four; a
hardcoded three-directory list finds three.

## E2 — Why the fourth is invisible to every existing schema tool

All three introspection helpers in `internal/persistence/store/migration_parity_test.go` filter on
the table-name prefix:

- `introspectPostgres` — `WHERE table_schema = 'public' AND table_name LIKE 'wrkflw_%'`
- `introspectMySQL` — `WHERE table_schema = DATABASE() AND table_name LIKE 'wrkflw_%'`
- `introspectSQLite` — `WHERE type='table' AND name LIKE 'wrkflw_%'`

`casbin_rule` does not match `wrkflw_%`. It is **structurally invisible** to the repo's existing
schema machinery — not overlooked once, but excluded by construction.

## E3 — `casbin_rule` is Postgres-only and `FromDB`-only

```
$ grep -n "MigrateCasbin" casbinauthz/casbinauthz.go internal/authz/casbin/migrate.go
casbinauthz/casbinauthz.go:236:func MigrateCasbin(ctx context.Context, pool *pgxpool.Pool) error {
casbinauthz/casbinauthz.go:237:	return internalcasbin.MigrateCasbin(ctx, pool)
internal/authz/casbin/migrate.go:26:func MigrateCasbin(ctx context.Context, pool *pgxpool.Pool) error {
```

`internal/authz/casbin/migrate.go` uses `goose.DialectPostgres`; the DDL declares
`id BIGSERIAL PRIMARY KEY`. The module-root public entry point is `casbinauthz.MigrateCasbin`,
which delegates to the internal one.

The three policy sources in `casbinauthz/casbinauthz.go` are `FromEnforcer` (line 59),
`FromStrings` (line 75) and `FromDB` (line 92). **Only `FromDB` puts policy in a table.**

⇒ the 8 `casbin_rule` columns exist only in a Postgres deployment that also uses `FromDB`.

## E4 — Column census: 79 `wrkflw_*` columns, identical across all three dialects

Column-definition lines inside `CREATE TABLE` blocks, excluding blank lines, `--` comments and
table-level `PRIMARY KEY` / `UNIQUE` / `FOREIGN` / `CONSTRAINT` / `KEY` / `INDEX` clauses:

| table | postgres | mysql | sqlite |
|---|---|---|---|
| `wrkflw_instances` | 9 | 9 | 9 |
| `wrkflw_journal` | 6 | 6 | 6 |
| `wrkflw_outbox` | 12 | 12 | 12 |
| `wrkflw_definitions` | 4 | 4 | 4 |
| `wrkflw_processed_message` | 3 | 3 | 3 |
| `wrkflw_call_links` | 13 | 13 | 13 |
| `wrkflw_timers` | 8 | 8 | 8 |
| `wrkflw_chain_links` | 7 | 7 | 7 |
| `wrkflw_human_task` | 17 | 17 | 17 |
| **total** | **79** | **79** | **79** |
| `casbin_rule` | 8 | — | — |

9 + 6 + 12 + 4 + 3 + 13 + 8 + 7 + 17 = **79**. Grand total **87** distinct `table.column` keys.

**Confirms** the deferred-slices spec's "9 tables, table set identical across postgres, mysql and
sqlite, 79 columns schema-wide".

## E5 — "48 free-form columns" is a Postgres number; SQLite has 67 — and the delta is ALL timestamps

```
$ # count of columns whose declared type matches TEXT|VARCHAR|JSONB?
postgres TEXT-ish columns: 48
mysql    TEXT-ish columns: 48
sqlite   TEXT-ish columns: 67
```

**Reproduces the audit's E20/C4 exactly.** The mechanism, from the Postgres type histogram:

```
$ awk '<column lines>' postgres/0001_init.sql | awk '{print $2}' | sort | uniq -c | sort -rn
  31 TEXT          5 TEXT,          -> TEXT        36
   7 JSONB         5 JSONB,         -> JSONB       12
  12 TIMESTAMPTZ   7 TIMESTAMPTZ,   -> TIMESTAMPTZ 19
   6 INT                            -> INT          6
   3 SMALLINT                       -> SMALLINT     3
   2 BIGINT                         -> BIGINT       2
   1 BIGSERIAL                      -> BIGSERIAL    1
                                       total       79
```

36 + 12 = **48** TEXT-ish in Postgres. SQLite's declared mapping (its own migration header) is
`TIMESTAMPTZ/DATETIME(6) → TEXT`, and 48 + **19** = **67**.

⇒ **The entire 19-column divergence is the timestamp mapping.** A classification derived from the
*physical type* therefore reports 19 engine-written instants as "free-form" on SQLite and not on
Postgres. This is the measurement that justifies classifying by **logical role** instead, which is
dialect-invariant — the alternative the audit's own finding #2 explicitly permits
("or explicitly justified as dialect-invariant").

## E6 — The classification covers the schema exactly

87 classification rows, checked against the 87 machine-dumped `table.column` keys with `comm`:

```
unclassified (in schema, not classified):   <empty>
stale (classified, not in schema):          <empty>
EXACT MATCH: yes
```

Per-class counts (`cut -f3 | sort | uniq -c`):

```
  27 reference
  19 timestamp
  17 scalar
  11 freeform
   8 policy
   5 actor
```

27 + 19 + 17 + 11 + 8 + 5 = **87**.

⚠ **Four of the six counts refute the author's own pre-count estimates** (`freeform` 15→11,
`reference` 22→27, `scalar` 14→17, `actor` 6→5). The estimates were written into a question to the
owner before being counted. Recorded because it is the count-them-again rule catching the author.

⚠ **The `timestamp` count of 19 is an independent cross-check on E5**: it was assigned by hand from
column semantics and lands on the same 19 the type histogram produced.

## E7 — Exactly one column NAME carries two different classes

```
$ awk '<group classes by bare column name, print names with >1 distinct class>' class.tsv
claimed_by: reference actor
```

- `wrkflw_human_task.claimed_by` — a human principal. `humantask_store.go` writes the actor id;
  the ADR-0098 schema comment calls it "the application-maintained scalar projection of
  `claim.actor.id`". Class **`actor`**.
- `wrkflw_call_links.claimed_by` — a **worker lease owner**, not a person:
  `WithCallLinkLease(owner string, ttl time.Duration)` in `internal/persistence/store/call_links.go`
  sets `s.leaseOwner`, and `ClaimPending` "atomically stamps claimed_at/claimed_by, hiding each row
  from concurrent workers until the lease expires". Class **`reference`**.

⇒ **the classification key MUST be `table.column`.** A `column`-keyed map would silently merge a
process identity with a human identity and mis-state one of them.

## E8 — `eligibility` is authorization policy at rest, re-derived not inherited

```
$ grep -rn "Authorize(ctx" --include='*.go' . | grep -v _test | grep -v "func "
casbinauthz/casbinauthz.go:163:	return a.inner.Authorize(ctx, spec, actor, vars)      <- delegation, not a call site
runtime/task/service.go:199:	if err := s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars); err != nil {
runtime/task/service.go:234:	if err := s.authz.Authorize(ctx, task.Eligibility, by,    task.Vars); err != nil {
runtime/task/service.go:255:	if err := s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars); err != nil {
runtime/task/service.go:306:	if err := s.authz.Authorize(ctx, task.Eligibility, by,    task.Vars); err != nil {
authz/authz.go:92:	Authorize(ctx context.Context, spec AuthzSpec, actor Actor, vars map[string]any) error   <- interface decl
```

**Four** call sites, all passing `task.Eligibility`, which `humantask_store.go:397-398` unmarshals
from the `eligibility` column. The inherited claim ("`wrkflw_human_task.eligibility` is the one all
four `Authorize` sites read") **holds** on re-derivation.

⇒ authorization policy is durable in **three** places: `wrkflw_human_task.eligibility` (every
dialect, always); `casbin_rule.{ptype,v0..v5}` (Postgres, wherever `casbinauthz.MigrateCasbin` has
been run); and `wrkflw_definitions.definition`, which carries every node's `eligible_roles` /
`eligible_privileges` / `eligible_expr` inside the marshalled definition.

⚠⚠ **E8 said TWO and was wrong — the omission survived into the ADR, the spec, the plan and the
generated `SECURITY.md`.** E8 re-derived the `Authorize` call sites correctly and then answered a
DIFFERENT question — "which columns are class `policy`" — as if it were "where does authorization
policy sit at rest". Executed 2026-08-23, marshalling a definition whose user task carries all three
eligibility options:

```
contains eligible_roles: true
contains eligible_privileges: true
contains eligible_expr: true
excerpt: ...,{"id":"approve","kind":"userTask","eligible_roles":["manager"],
"eligible_privileges":["approve:invoice"],"eligible_expr":"vars.amount \u003c 100"},...
```

and `internal/persistence/store/definitions.go` `PutDefinition` writes exactly those bytes into
`wrkflw_definitions.definition`.

## E9 — The `keyed` lower bound: 27 of 79 Postgres columns

Derived from PK membership, `UNIQUE`, and `CREATE INDEX` column lists **plus partial-index `WHERE`
predicate columns**:

```
DISTINCT KEYED COLUMNS: 27 of 79
--- keyed, by class ---
  15 reference
   7 scalar
   4 timestamp
   1 actor
```

⚠⚠ **THIS PARAGRAPH WAS THE ROUND-1 CRITICAL AND IS CORRECTED HERE.** It read: *"Zero `freeform`
and zero `policy` columns are keyed. All 11 free-form columns and **both policy locations** are
index-free in Postgres."* The derivation above is scoped to the **79 `wrkflw_*`** columns; "both
policy locations" includes `casbin_rule`, which is **outside that scope**.
`internal/authz/casbin/migrations/0001_casbin_rule.sql:12` declares
`CREATE INDEX casbin_rule_ptype_idx ON casbin_rule (ptype)`.

**Corrected, derived over every table and every dialect:**

```
postgres: columns=87  keyed=29   actor=1 policy=1 reference=15 scalar=8 timestamp=4
mysql:    columns=79  keyed=28   actor=1 reference=15 scalar=7 timestamp=5
sqlite:   columns=79  keyed=28   actor=1 reference=15 scalar=7 timestamp=5
```

⇒ **`policy`-keyed is 1, not 0.** The 11 `freeform` columns are index-free in every dialect; the
`policy` class is **not**. ⚠ And independently of any index,
`internal/authz/casbin/pg_adapter.go` `RemovePolicy` filters **all seven** policy columns by
equality, so no "safe to encrypt" statement holds for that class at all.

⭐ The single keyed `actor` column is `wrkflw_human_task.claimed_by`
(`idx_wrkflw_human_task_claimed_by`; the ADR-0098 schema comment states it "keeps `AssignedTo`'s
lookup indexed").

⚠ **This is a LOWER BOUND.** It is derived from the DDL only. Columns filtered or ordered by query
text in the store layer are not visible to it. The generated `SECURITY.md` text must say so.

## E10 — `keyed` is dialect-DEPENDENT, unlike `class`

```
postgres: CREATE INDEX=11  partial(WHERE)=5
mysql:    CREATE INDEX=8   partial(WHERE)=0
sqlite:   CREATE INDEX=11  partial(WHERE)=0
```

MySQL's 8-vs-11 is **not** a missing-index divergence — it declares three inline:

```
$ grep -nE "^\s*(KEY|INDEX|UNIQUE)" mysql/0001_init.sql
145:    INDEX idx_wrkflw_human_task_instance   (instance_id),
146:    INDEX idx_wrkflw_human_task_state      (state),
147:    INDEX idx_wrkflw_human_task_claimed_by (claimed_by)
```

8 + 3 = 11, which is why `TestMigrationParity_IndexNamesConverge` passes. But Postgres expresses 5
indexes as **partial** (`... WHERE status = 'pending'`) while MySQL and SQLite have no partial-index
support and fold the predicate column into the key instead — as that test's own doc comment states.

⇒ **`class` is dialect-invariant; `keyed` is not.** `keyed` must be emitted per dialect.
⚠ This **corrects the author's own design proposal**, which had `keyed` riding along with the
invariant `class`.

## E11 — Two parser traps, both found by executing a throwaway probe

**Trap 1 — multi-line statements and multi-space `ON`.** A first probe using
`sed 's/CREATE INDEX [^ ]* ON //'` emitted four rows whose table name was the literal string
`CREATE`, because `idx_wrkflw_human_task_instance   ON` has three spaces before `ON` and because
`wrkflw_call_links_pending_idx` spans two physical lines. The parser must tokenise **statements**
(split on `;` after stripping comments), not lines.

**Trap 2 — MySQL inline `INDEX name (col)` clauses** (E10) sit inside the `CREATE TABLE` body. They
must be recognised as index declarations but **not** counted as columns. The column census (E4) is
correct only because those lines were skipped.

Both are prescribed as parser test cases in the plan.

## E16 — A FOURTH parser trap, found by IMPLEMENTATION (rule #11)

The bundle enumerated three parser traps (E11, E15). Implementing Task 1 found a fourth by probing
the real migration files: **inline single-column `PRIMARY KEY`**, declared on the column rather than
as a table-level `PRIMARY KEY (…)` clause.

```
$ grep -n "PRIMARY KEY" postgres/0001_init.sql casbin/0001_casbin_rule.sql | grep -v "PRIMARY KEY ("
casbin_rule.sql:3:      id                BIGSERIAL PRIMARY KEY,
postgres/0001_init.sql:11:  instance_id       TEXT PRIMARY KEY,
postgres/0001_init.sql:41:  id                BIGSERIAL PRIMARY KEY,
postgres/0001_init.sql:77:  child_instance_id TEXT PRIMARY KEY,
```

**Four occurrences.** A parser that reads only the table-level form silently omits `PK` from all
four — and two of them (`casbin_rule.id`, `wrkflw_outbox.id`) are inside the **29-of-87** Postgres
`keyed` count that decision 8 publishes. ⇒ this trap is load-bearing for the delivery's headline
number, and no prescribed test in the plan would have caught it: the trap table had three cases.

⚠ **An enumeration rotted again**, in the delivery whose subject is enumerations rotting, after two
audit rounds one of which was dedicated to re-counting. Found by executing against real files.

## E12 — Repo conventions this delivery reuses rather than reinvents

Recorded because the ADR-0186 lineage claimed a gap the repo had already filled **four** times.

- `internal/persistence/store/migration_parity_test.go` — `introspectPostgres` / `introspectMySQL` /
  `introspectSQLite`, already Docker-gated for pg+mysql and pure-Go for SQLite. The new guard lives
  in the **same package** (`store_test`) so it calls them with no refactoring.
- `runtime/monitor/internal_leak_test.go` — the **self-cleaning allow-list**: "an entry that no
  longer matches any offender fails the test, so a fixed leak cannot leave a stale exemption behind".
- `engine/terminal_sites_test.go` — the **liveness guard**: run the matcher over an in-test fixture
  holding one rogue and one innocent case and assert 1 and 0, "so a detector that reports nothing
  ever cannot pass", plus mutation verification against real files.
- `scripts/` already holds four shell entry points (`check-extraction.sh`, `check-test-timeout.sh`,
  `coverage.sh`, `testdb.sh`) and the repo has **no** `go:generate` convention beyond mockgen.

## E13 — There is no encryption, redaction or tamper-evidence AT REST

⚠⚠ **The inherited form of this claim is misleading and is corrected here.** The deferred-slices
spec records it as: *"`grep -rniE "encrypt|redact"` over `persistence/`, `internal/persistence/` and
`engine/` (non-test) exits 1."* Re-executed:

```
$ grep -rniE "encrypt|redact" persistence/ internal/persistence/ engine/ --include='*.go' | grep -v _test
EXIT=1                       <- but this exit code comes from the -v _test FILTER, not from the search

$ grep -rniE "encrypt|redact" persistence/ internal/persistence/ engine/ --include='*.go'
RAW EXIT=0, 2 matching lines
engine/step_compensation_retry_test.go:356
engine/step_compensation_retry_test.go:369

$ grep -rniEo "encrypt|redact" ...
   1 engine/step_compensation_retry_test.go:356:redAct
   1 engine/step_compensation_retry_test.go:369:redAct
```

Both are the **false positive** `Redelive` + `redAct` + `ionFailed` inside the test name
`TestRedeliveredActionFailedDuringBackoffDoesNotDoubleArm`. Case-insensitive `redact` matches across
that word boundary.

⇒ **The substance holds — no encryption or redaction exists in the persistence or engine layers —
but "exits 1" is a measurement that is false as LABELLED.** A filter manufactured the premise. If
the false positive had been in a non-test file, the inherited command would have reported *absence*
while the raw search reported *presence*, and nobody would have looked.

**Repo-wide, non-test, the two real hits are both about data IN TRANSIT or IN AN ERROR BODY, never
at rest:**

```
$ grep -rniE "encrypt|redact" --include='*.go' . | grep -v _test
doc.go:66:  ClassifyError (5xx redaction), Instrumentation.Observe (static route template),
action/email/email.go:127:  sent over the encrypted channel.        <- SMTP STARTTLS
```

⇒ the sharper and checkable statement: **the repo has redaction (5xx error bodies) and encryption
(SMTP TLS) concepts, and applies neither to stored data.**

`wrkflw_journal` is 6 columns (E4): `instance_id`, `seq`, `kind`, `trigger`, `occurred_at`,
`applied_at`. No hash column, no prev-hash column, no signature column — so nothing in the schema
supports tamper-evidence either.

## E14 — goose directives: only `Up` and `Down` appear

```
$ grep -rhn "^-- +goose" internal/persistence/store/migrations/*/*.sql internal/authz/casbin/migrations/*.sql | sort | uniq -c
   4 1:-- +goose Up
   1 14:-- +goose Down
   1 150:-- +goose Down
   1 157:-- +goose Down
   1 162:-- +goose Down
```

Four files, each with exactly one `-- +goose Up` at line 1 and one `-- +goose Down`. **No
`StatementBegin` / `StatementEnd` directives exist.** The parser must respect the `Down` marker
(everything after it is `DROP TABLE`, which would otherwise be parsed as schema) and nothing else.

⇒ this **resolves** what the spec first recorded as `ASSUMPTION (unverified)`. It is now measured.

## E15 — There is exactly ONE foreign key, declared THREE different ways

```
$ grep -rn "REFERENCES\|FOREIGN KEY" internal/persistence/store/migrations/*/*.sql
mysql/0001_init.sql:35:    CONSTRAINT fk_journal_instance FOREIGN KEY (instance_id) REFERENCES wrkflw_instances(instance_id)
postgres/0001_init.sql:27:    instance_id TEXT        NOT NULL REFERENCES wrkflw_instances(instance_id),
sqlite/0001_init.sql:37:    instance_id TEXT    NOT NULL REFERENCES wrkflw_instances(instance_id),
```

**Parser trap 3** (with E11's two): MySQL declares it as a **table-level
`CONSTRAINT … FOREIGN KEY (…)` clause inside the `CREATE TABLE` body**, where Postgres and SQLite
declare it inline on the column. The column census (E4) is correct only because lines beginning
`CONSTRAINT` are skipped; a parser that treated that line as a column would report 80 columns for
MySQL and break dialect parity.

**It also moots the spec's open question 3** ("should the `keyed` derivation include foreign-key
columns, since Postgres does not auto-index them and MySQL does?"). The only FK column is
`wrkflw_journal.instance_id`, and `wrkflw_journal`'s primary key is `(instance_id, seq)` in all three
dialects — so that column is **already `keyed` via PK membership**. Including or excluding FK
columns from the derivation changes **no** row of the output today.
⚠ It could change one later; the plan therefore prescribes the FK-exclusion as a **stated rule with
a comment**, not as an accident of the parser.

# Design

## Context

See proposal.md, Why. The rule is decided: kartaladev/wrkflw#25 (decided) says names everywhere, strict readers with no integer arm, all sites in one pass, a reflective guard, and the `G115` suppression removed. This design covers only how to apply it, plus the two questions the decision leaves open.

State re-measured on main (`eebe55ba`, go1.26.9). The probes were compiled into `internal/persistence/store` through `go test -overlay`, so nothing was written to the tree.

- **Reflective walk of the persisted roots.** It follows encoding/json's visibility rules: exported fields, embedded structs, no `json:"-"`, and it stops at a `json.Marshaler`. It found these module-declared integer types without an encoder:
  - `engine.InstanceState`:
    - `Status` at `.Status`, `.Compensating.FinalStatus` and `.PendingFinalStatus`;
    - `TokenState` at `.Tokens[].State`;
    - `IncidentKind` at `.Incidents[].Kind`;
    - `TimerKind` at `.Timers[].Kind`;
    - `humantask.TaskState` at `.Tasks[].State`.
  - The journal envelope: none. Its disposition field is a plain `*int`.
  - `engine.ResolveCompensationStall`: `CompensationDisposition`.
  - `model.NodeWire` and `model.TriggerWire`: none. `NodeKind` already has an encoder, trigger `kind` is a `string`, and the only named integer reached is `time.Duration` in the retry policy.
  - `humantask.HumanTask`: `TaskState`. The SQL task store writes it to a `TEXT` column through `String()`, and the caching task store JSON-encodes the whole struct.
  - `kernel.ArmedTimer`: `TimerKind`, written to the `kind` column.
- **Columns** (`internal/persistence/store/migrations/*/0001_init.sql`):
  - `wrkflw_instances.status` is `SMALLINT` on Postgres and MySQL and `INTEGER` on SQLite;
  - `wrkflw_timers.kind` has the same types;
  - `wrkflw_timers.trigger_kind` has the same types, `DEFAULT 0`.
  - The status index is partial on Postgres (`WHERE ended_at IS NULL`) and full on MySQL and SQLite. The timers table has only the keyset index.
- **Write and bind sites:** 19 `int16(...)` casts, 4 in `store_core.go`, 11 in `pruner.go` and 4 in `lister.go`, plus two `int16` scan variables, in `lister.go` and `timerstore.go`.
- **Silent reads, reproduced:**
  - `json.Unmarshal({"Status":99})` into `InstanceState` returns nil and yields `Status(99)`.
  - On SQLite, a task row updated to `state='bogus'` reads back through the SQL task store's `Get` as `unclaimed`, with a nil error.
- **Existing vocabularies:**
  - `Status`, `TaskState` and `CompensationDisposition` have lowercase `String()` names.
  - `IncidentKind` and `TimerKind` have Go-identifier `String()` names. The admin timers endpoint already renders `TimerKind.String()`.
  - `TokenState` has no `String()`, but `service/instance.go` `tokenStateString` renders `active`, `waiting`, `joining` and `incident` on the HTTP instance document.
  - `schedule.Kind` names exist only in `definition/model/trigger_wire.go` `kindString`: `onetime`, `duration`, `expr`, `everyExpr`, `cron`, `durationRand`, `daily`, `weekly`, `monthly`, and `""` for `KindUnset`.

## Goals / Non-Goals

**Goals:**
- Every enum that reaches persisted bytes is a name. Specs: instance-store *Instance enums encode as their stable names*, *No enum the instance store persists is written as an ordinal* and *The journal records a compensation disposition by name*; human-task-lifecycle *SQL task stores persist the task state by name*; restart-recovery *Armed timer rows store their kinds by name*.
- Every reader refuses an out-of-vocabulary value by name. Specs: instance-store *Decoding an instance enum accepts only an exact name* and *A stored instance enum outside the vocabulary is refused on read*; human-task-lifecycle *SQL task stores refuse a stored task whose state is outside the vocabulary*; restart-recovery *An armed timer row whose kind is outside the vocabulary is refused*.
- A regrowth guard makes the next persisted enum without an encoder a test failure.

**Non-Goals:**
- Harmonising spellings across types. kartaladev/wrkflw#44 says to keep each type's current spelling, because the strings are also the view vocabulary.
- A dual-form or migrating reader. kartaladev/wrkflw#25 rules it out.
- Encoding `time.Duration` as anything other than nanoseconds.

## Decisions

### D0. The format promises live with the store that persists them

No cross-cutting format capability is created. The archived baseline design (`establish-baseline-specs`, D2) decided that "format promises (#44, #138) live with the store that persists them", and named `instance-store` rather than a `persisted-state-format` capability. So the requirements are split by owner:
- `instance-store`: the snapshot and journal enums, their encode/decode contract, the `wrkflw_instances.status` column, strict reads on `Load`, `Entries` and listing, and listing by status name.
- `human-task-lifecycle`: the task store's `state` column and its strict read, which is the defect this change found.
- `restart-recovery`: the `wrkflw_timers` `kind` and `trigger_kind` columns, the strict armed-timer listing, and the pruning sweeps that select on them. Its Purpose names the durable timer store as its subject.

`humantask.TaskState`'s JSON form is stated in `instance-store`, because the snapshot is where it is encoded as JSON. The task store writes a column, not JSON. The reflective guard is a test, not a promise. Each owner's "written by name, never as an ordinal" requirement is the promise it enforces.

Rejected: a new `persisted-enum-encoding` capability. A first draft of this change did that, and it recreated exactly the cross-cutting home D2 ruled out.

### D1. Names come from the vocabulary each type already exposes

`Status`, `TaskState`, `CompensationDisposition`, `IncidentKind` and `TimerKind` reuse their `String()` names. `TokenState` gains a `String()` that returns the HTTP projection's names, and `tokenStateString` then delegates to it. `trigger_kind` uses the `kindString` names.

- **Rejected: harmonise to lowerCamelCase or snake_case.** It would change the HTTP views (`TimerKind` on the admin endpoint, `Kind` on incidents) and the slog `timer_kind` attribute, all for no persisted-format benefit. kartaladev/wrkflw#44 already rejected it.
- **Rejected: new names for `TokenState`.** The HTTP instance document already promises `active`/`waiting`/`joining`/`incident`. A second vocabulary would give one value two spellings.
- **`KindUnset` stores `""`.** `""` is what the wire vocabulary uses for an unset trigger. The column becomes `TEXT NOT NULL DEFAULT ''`, the textual counterpart of today's `DEFAULT 0`. Rejected: `"unset"`, a spelling that exists nowhere else.
- `String()` keeps its `"unknown"` / `"TimerKind(unknown)"` fallback for logging. That fallback is never written, because the encoder refuses a nameless value (D2).

### D2. One name table per type drives `String()`, `MarshalJSON`, `UnmarshalJSON` and column binding

Each type gets a fixed bidirectional table, the same shape as `NodeKind`'s `nodeKindNames`/`nodeKindByName` but without the registration step.
- `MarshalJSON` looks up the table and returns the unknown-value sentinel on a miss.
- `UnmarshalJSON` requires a JSON string and an exact table hit. Any other input, including a number and `null`, returns the sentinel.
- Column binding uses the same table, through a method the store calls rather than a store-side switch. The store keeps no vocabulary of its own. The guard (D4) and the name-table test (D5) then cover the columns too.

`null` is refused, deliberately. encoding/json's convention is to treat `null` as a no-op, but that would let `null` decay to the zero value, which is exactly what kartaladev/wrkflw#25 forbids.

Rejected: `MarshalText`/`UnmarshalText`. They also cover map keys, but no persisted map is keyed by these enums. A text unmarshaler also cannot tell a JSON number from a string as cleanly, and `NodeKind` set the `MarshalJSON` precedent.

### D3. The journal envelope carries the named type

The journal envelope's disposition field becomes `*engine.CompensationDisposition`. It stays a pointer, for the reason its comment gives: `omitempty` must keep it off the other 15 kinds. The codec's int conversions go away.

Evidence: with the field left as `*int`, a guard that walks the trigger structs passes as soon as `CompensationDisposition` has an encoder, while the journal still writes `0`/`1`/`2`. The guard would pass vacuously. The guard therefore walks the envelope as a root, as well as every trigger type. A test asserts on the raw `payload` bytes: the *The journal records a compensation disposition by name* rows.

The codec's comment about round-tripping a newer build's unknown disposition "unchanged" is deleted. Strict reading reverses it, by decision.

### D4. The guard

A test named for the rule ("persisted enums encode by name") walks these roots:
- `engine.InstanceState`;
- every trigger type in the journal codec's variant list, cross-checked against `AllTriggerKinds` so that a new trigger cannot be skipped;
- the journal envelope;
- `model.NodeWire` and `model.TriggerWire`;
- `humantask.HumanTask`;
- `kernel.ArmedTimer`. It is persisted through columns, not JSON, but its `Kind` must have a name table.

It fails on any integer-kinded named type that is declared in module `github.com/kartaladev/wrkflw`, is reachable through encoding/json-visible fields, and does not implement `json.Marshaler`. The failure message names the path, for example `InstanceState.Tokens[].State`.

- **Exempt:** types declared outside the module. `time.Duration` is the one reached today. It is a unit, not an enum.
- **Rejected:** an explicit allowlist of the six types. It cannot catch the seventh, which is the guard's whole purpose.
- **Rejected:** walking from `model.ProcessDefinition`. It implements `json.Marshaler`, so the walk would stop at the root. `NodeWire` is the shape actually encoded.

`persist-pending-commands` adds its envelope as one more root.

### D5. Name-table tests pin the vocabulary

One table test per enum iterates from 0 to one past the last constant. It asserts that every constant has a name and that no two constants share one, and it pins each name literally. The literal pin is what makes a rename fail loudly. The scan bound is derived from a sentinel constant or an iota probe, as in `definition/kinds/nodetype_guard_test.go` (`kindScanLimit`), so an appended constant without a name fails.

### D6. Columns become `TEXT` in place

Each dialect's `0001_init.sql` changes `status`, `kind` and `trigger_kind` to `TEXT` on Postgres and SQLite, and to `VARCHAR(64)` on MySQL. MySQL cannot index an unbounded `TEXT` column, and `wrkflw_instances_status_idx` is a plain index there. `VARCHAR(64)` is the width the MySQL file already uses for `wrkflw_human_task.state`, and the other enum-like MySQL columns are `VARCHAR(50)`. The longest name is 26 bytes (`IncidentCompensationFailed`), and the name-table test asserts every name fits. Indexes keep their shape and names.

- **Rejected: a `0002` migration.** kartaladev/wrkflw#25 records that no persisted data exists before the first tag and that the squash policy (`TestMigrations_OneFilePerDialect`) holds.
- **Rejected: a CHECK constraint listing the names.** It would duplicate the vocabulary in three SQL files, and the strict reader already refuses out-of-band values.

No column is added or dropped, so the `internal/atrest` census (`discover_test.go`, `schema_test.go`) does not move. That census is keyed by `table.column` identity and is type-independent, as the comment on kartaladev/wrkflw#44 records. `TestMigrationParity_LogicalSchemaConverges` compares table, column and nullability, not physical type (measured in `migration_parity_test.go`), so the type change does not move it, but `NOT NULL` must be kept on all three columns.

### D7. Concurrency and performance

- **Concurrency does not apply.** The name tables are immutable package-level values, and encoding is pure, so no race or lost-update dimension exists. The existing `ErrConcurrentUpdate` behaviour is untouched.
- **Performance:** snapshots are rewritten on every step, and names are longer than one-digit ordinals. The history cap bounds the dominant growth. Task 5.2 records a before/after benchmark of snapshot marshal size and allocations. No ceiling is promised.

## Risks / Trade-offs

- [A database created by an earlier build has `SMALLINT` columns that goose will not re-migrate. Binding a name into an integer column fails on Postgres, and may coerce on MySQL or SQLite.] → This is pre-release by decision: no `v*` tag exists. The README's persistence section states that a database from an earlier build must be recreated (Migration Plan).
- [A byte cache (for example Redis) populated by an earlier build holds integer snapshots.] → On a decode error the caching stores fall back to the backing store (`persistence/cache/codec.go`: an unmarshal error is a miss to the caller at `caching_instance_store.go` Load). The entry is overwritten on the next write, so no stale ordinal is ever served.
- [Strict reading makes one bad timer row fail the whole armed-timer listing, and so rehydration.] → That is already true for an undecodable trigger payload today. Only an out-of-band write can produce the row. Changing the batch policy is not this change's decision.
- [A consumer that encodes `engine.InstanceState` itself sees names.] → Called out in the proposal as public-API impact. v0.x.

## Migration Plan

1. Land after `harden-definition-store-parity`, then rebase. That change edits the `wrkflw_definitions` block of `migrations/mysql/0001_init.sql`, and this one edits the `wrkflw_instances` and `wrkflw_timers` blocks of all three dialects, so the rebase is textual.
2. Land before `cut-v0-release`, which closes the in-place-edit window by tagging.
3. Rollback before a tag means reverting the commit and recreating any database it migrated. Rollback after a tag is not supported, which is why it goes in now.

## Cross-change coupling

| Change | Coupling | Owner / order |
|---|---|---|
| `harden-definition-store-parity` (kartaladev/wrkflw#152) | Same file, `migrations/mysql/0001_init.sql`, different table. Same in-place-edit window. | It lands first (wave 1). This change rebases, and lists it under Depends on. |
| `surface-unresolvable-node` (kartaladev/wrkflw#79) | Appends a fifth `IncidentKind` (ordinal 4). | Whichever lands second adds the name row: to this change's name table and spec table, or to that change's constant. No dependency edge, because either order works and the name-table test (D5) fails until the row exists. |
| `persist-pending-commands` (kartaladev/wrkflw#110) | Its envelope embeds `HumanTask` (`TaskState`), and it adds a table to all three `0001_init.sql` files. | It depends on this change (its D10) and adds its envelope as a guard root. |
| `recover-waiters-on-restart` | Under its Q1 b, a waiter-kind column. | It follows this change's rule. No edge unless Q1 b is chosen. |
| `refuse-unrecognized-wire-values` (kartaladev/wrkflw#206) | Refuses an unknown trigger `kind` string at definition decode. `trigger_kind` reuses the same names. | Independent. Neither edits the other's files. |
| `export-refusal-sentinels` | Its `model.ErrUnknownNodeKind` is the naming precedent for Q1. | Independent. |
| `split-persistence-modules` | Moves the migration files. | Whichever lands second rebases. |
| `cut-v0-release` | Tagging closes the window. | This change blocks it. |

## Open Questions

**Q1. Sentinel shape for an unknown or nameless value.** The decision requires an error but names none, and the project rule requires a named one. This affects every spec row marked `pending Q1`.
- (a) **One sentinel per enum type** (recommended): `engine.ErrUnknownStatus`, `engine.ErrUnknownTokenState`, `engine.ErrUnknownIncidentKind`, `engine.ErrUnknownTimerKind`, `engine.ErrUnknownCompensationDisposition`, `humantask.ErrUnknownTaskState`, and `schedule.ErrUnknownKind` for the column write. It mirrors `model.ErrUnknownNodeKind` (`export-refusal-sentinels`), and `errors.Is` tells the caller which field was bad without parsing text.
- (b) One per package (`engine.ErrUnknownEnumValue`, `humantask.ErrUnknownTaskState`, `schedule.ErrUnknownKind`). Fewer exports, but the type appears only in the message.
- (c) Reuse `humantask.ErrInvalidTask` for task state on read. Rejected as the recommendation: that sentinel means a write-side invariant violation, so it would conflate two refusals.

**Q2. An absent enum field in a stored document.** encoding/json never calls `UnmarshalJSON` for a missing key, so a snapshot object without `"Status"`, or a `resolve_compensation_stall` payload without `"disposition"`, decodes to the zero value: `StatusRunning`, or `CompensationRetry`, the latter documented today in the codec. The library always writes these fields, so only an out-of-band row can omit one. No spec rows are written for absence.
- (a) **Keep the zero value for snapshot struct fields** (recommended), with the guard also failing on `omitempty` on any persisted enum field, so the library can never omit one. **Refuse in the journal codec**, where presence is already visible through the pointer, so a `resolve_compensation_stall` payload without a disposition fails with the Q1 sentinel. This costs one branch and no extra decoding.
- (b) Refuse absence everywhere. This needs presence tracking on every containing struct (`Token`, `Incident`, `timerRecord`, `HumanTask`, `InstanceState`), which means custom decoders on five types for rows only an out-of-band writer can produce.
- (c) Accept absence everywhere, including the journal, as today.

# Proposal

## Why

Six engine and task enums reach persisted documents as bare iota ordinals, and three SQL columns store enum ordinals as `SMALLINT`. Inserting or reordering a constant silently relabels every stored row, and a stored value the build does not know decodes without error. `NodeKind` already encodes by stable name for exactly this reason. The fix edits the squashed `0001_init.sql` files, which is only legitimate before the first release tag, so it has to land before `cut-v0-release`.

## What Changes

Applies the decision recorded on kartaladev/wrkflw#25 (decided): every enum that reaches a persisted document or column encodes by stable name, readers are strict with no integer arm, and a reflective guard test fails on the next enum persisted without an encoder. Absorbs kartaladev/wrkflw#44 (folded in, with its scope re-measured below). Everything below was measured on main (`eebe55ba`, go1.26.9) with throwaway probes compiled in through `-overlay`; nothing was written to the tree.

- **BREAKING (persisted format)** Six exported enums gain `MarshalJSON`/`UnmarshalJSON` by stable name. Each keeps the spelling it already has in its `String()` method or in the view vocabulary, without harmonising:

  | Enum | Where it is persisted (measured by reflective walk) | Names |
  |---|---|---|
  | `engine.Status` | snapshot `Status`, `Compensating.FinalStatus`, `PendingFinalStatus` | `running`, `completed`, `failed`, `compensating`, `terminated` |
  | `engine.TokenState` | snapshot `Tokens[].State` | `active`, `waiting`, `joining`, `incident` (the HTTP projection's vocabulary; the type gains a matching `String()`) |
  | `engine.IncidentKind` | snapshot `Incidents[].Kind` | `IncidentAction`, `IncidentCompensationStall`, `IncidentCompensationFailed`, `IncidentDefinitionDefect` |
  | `engine.TimerKind` | snapshot `Timers[].Kind` | `TimerIntermediate`, `TimerDeadline`, `TimerInWait`, `TimerRetry`, `TimerCompensationStall`, `TimerCompensationRetry` |
  | `humantask.TaskState` | snapshot `Tasks[].State` | `unclaimed`, `claimed`, `completed`, `cancelled` |
  | `engine.CompensationDisposition` | journal payload of a `resolve_compensation_stall` trigger | `retry`, `skip`, `abandon` |

  Encoding a value with no name is an error, never a substituted name. Decoding refuses an unknown name, a case or whitespace variant, an integer and `null`.
- **BREAKING (persisted format, all three dialects)** `wrkflw_instances.status`, `wrkflw_timers.kind` and `wrkflw_timers.trigger_kind` change from `SMALLINT` (`INTEGER` on SQLite) to `TEXT` (`VARCHAR(64)` on MySQL, so the status index stays legal; design D6) in each dialect's squashed `0001_init.sql`, edited in place under the one-file-per-dialect policy. `trigger_kind` stores the `schedule.Kind` names the definition wire format already uses for trigger objects (`onetime`, `duration`, …, with `""` for an unset trigger). The instance lister, the two timer pruner sweeps and the timer store bind and scan names. The indexes keep their shape. No column is added or dropped, so the `internal/atrest` census does not move.
- **BREAKING** Strict readers on every persisted enum site:
  - `Load` refuses a snapshot carrying an unknown name. Today a snapshot with `"Status":99` decodes with no error into `Status(99)` (measured).
  - The task store refuses a stored row whose `state` is not a task-state name. **Defect found here, not in the issue:** the SQL task store's reader maps every unrecognised `state` to `unclaimed`. Reproduced on SQLite, where a row updated to `state='bogus'` reads back `unclaimed` with a nil error. The write side already refuses an out-of-range state with `humantask.ErrInvalidTask`.
  - The journal reader refuses a disposition it does not know. This reverses the documented forward-compatible behaviour, where a newer build's unknown disposition "round-trips unchanged".
  - Listing armed timers refuses a row whose `kind` is unknown. A row whose trigger payload cannot be decoded already fails the listing.
- **BREAKING** `InstanceLister.List` with a status filter that has no name is refused, by the SQL lister and by the in-memory store alike. Today the SQL lister binds the ordinal and silently matches nothing.
- **Regrowth guard (tooling, no spec).** A reflective test, named after the rule, walks every root that reaches a persisted JSON document: `engine.InstanceState`, every trigger type the journal codec handles together with the journal envelope itself, `model.NodeWire` and `model.TriggerWire`, and `humantask.HumanTask`. It fails on any module-declared integer-kinded type reachable through encoding/json-visible fields that does not implement `json.Marshaler`. A per-enum name-table test pins every constant's name, so a new constant without a name fails, and a renamed name fails loudly.
- **The `G115` suppression is removed** from `.golangci.yml`. All 19 `int16(...)` casts it covers are in `internal/persistence/store` (4 in `store_core.go`, 11 in `pruner.go`, 4 in `lister.go`, measured), plus two `int16` scan variables, and the change deletes every one of them.

Re-measured premises from kartaladev/wrkflw#44:
- **"Eleven int-based enum types":** holds. There are 11 exported integer enums across `engine`, `humantask` and `definition`, and only `NodeKind` encodes by name. But the persisted surface is narrower than the issue implies: 5 in the snapshot and 1 in the journal, plus `schedule.Kind` in a column only. `EndBehavior`, `TerminationOutcome` and `StepMode` never reach a persisted document as integers, and `schedule.Kind` travels as a name in the trigger payload. This confirms the issue's own comment.
- **"The journal's trigger payloads carry whatever enum a trigger struct holds":** partly stale. The journal writes a hand-built envelope whose disposition field is a plain `*int`. A reflective walk over that envelope sees no named enum, and one over the trigger structs would pass once `CompensationDisposition` has an encoder while the envelope still wrote the integer. The guard therefore walks both, and the envelope carries the named type (design D3).
- **"Nine test files hard-coding `int16` into raw SQL" (issue comment):** not reproduced. Six test files issue raw SQL against `wrkflw_instances` or `wrkflw_timers` (five in `internal/persistence/store`, plus `persistence/pruner_test.go`), and five carry `int16` (eight occurrences in all).
- **Blockers:** kartaladev/wrkflw#147 and kartaladev/wrkflw#47 are both closed. `definition/testdata/golden_definition.json` carries no integer enum (measured), so this change does not regenerate it.

Impact flags:
- **Persisted format: YES, the core of the change.** It touches the snapshot JSON, the journal payload, three columns in all three dialects, and the byte form of cache entries written by the caching stores. No `v*` tag exists, so per kartaladev/wrkflw#25 this is a schema edit, not a data migration. goose does not re-run an applied `0001`, so a database migrated by an earlier build keeps `SMALLINT` columns and must be recreated (design, Migration Plan). The SQL task store's `state` column is already `TEXT` and does not change.
- **Default security posture:** tightened. Silent decays on read become named refusals. Nothing is loosened.
- **Consumer `go.mod`:** unchanged.
- **Public API:** `json.Marshal` of the six types, and of any struct embedding them (for example an `engine.InstanceState` a consumer encodes itself), yields names instead of integers. The HTTP views already render names through their own DTOs and do not change.

Out of scope:
- `NodeKind` already encodes by name. Its unknown-name sentinel (`model.ErrUnknownNodeKind`) belongs to `export-refusal-sentinels`.
- The new `IncidentKind` that `surface-unresolvable-node` (kartaladev/wrkflw#79) appends. Whichever of the two changes lands second adds its name row (design, Cross-change coupling).
- Refusing an unrecognised trigger `kind` in a definition: `refuse-unrecognized-wire-values` (kartaladev/wrkflw#206).
- The MySQL `def_id` collation in the same `0001_init.sql`: `harden-definition-store-parity` (kartaladev/wrkflw#152).
- The pending-commands envelope: `persist-pending-commands` (kartaladev/wrkflw#110) adds it as a root of this change's guard.
- A waiter-kind column: `recover-waiters-on-restart`, under its Q1 b only.
- `time.Duration` fields (retry-policy intervals) persist as nanosecond integers. They are units, not enums, so the guard exempts types declared outside the module. No change owns this, and it is not reported as a defect.
- Moving the migration files to another module: `split-persistence-modules`.

Decisions this change cannot be fully specified without (design, Open Questions). The decision on kartaladev/wrkflw#25 settles everything else:
- Q1: the sentinel shape for an unknown or nameless enum value: one per enum type (recommended) or one per package. Spec rows that name the sentinel are marked `pending Q1`.
- Q2: whether an absent enum field in a stored document is refused or keeps its zero value. No spec rows are written for absence.

**Coordination**
- Size: L
- Depends on: harden-definition-store-parity
- Blocks: cut-v0-release, persist-pending-commands
- Readiness: human

## Capabilities

### New Capabilities

None. Per the baseline design's D2 (archived `establish-baseline-specs`), format promises live with the store that persists them (design D0).

### Modified Capabilities

All deltas are ADDED requirements. No baseline requirement's text changes, because the existing scenarios name enum values symbolically and hold unchanged.
- `instance-store`: snapshot and journal enums encode and decode by exact name. A nameless value is not persisted. The status column and snapshot hold no ordinal. The journal disposition is stored by name. Out-of-vocabulary values are refused on `Load`, `Entries` and listing. Listing filters by status name.
- `human-task-lifecycle`: SQL task stores persist the task state by name, and refuse a stored state outside the vocabulary (the decay defect).
- `restart-recovery`: armed timer rows store their kind and trigger kind by name. An out-of-vocabulary kind is refused on listing. Pruning selects rows by trigger-kind name.

## Impact

- `engine/state.go`, `engine/command.go`, `engine/trigger.go`, `humantask/humantask.go`: encoders, a `TokenState.String()`, name tables, and the "APPENDED deliberately … persisted as a plain integer" comments rewritten, since ordinals stop being persisted.
- `service/instance.go`: `tokenStateString` delegates to the new `String()`. The HTTP output is unchanged.
- `internal/persistence/store`: `store_core.go`, `lister.go`, `pruner.go`, `timerstore.go`, `trigger_codec.go`, `humantask_store.go` (`htParseTaskState`), and the three `migrations/*/0001_init.sql`.
- Tests: the six raw-SQL test files, `engine/state_token_provenance_test.go` (its legacy token fixture carries `"State":2`), the new guard and name-table tests, and the dialect conformance rows.
- `.golangci.yml`: the `G115` exclusion is removed.
- Consumers: any database created by an earlier build must be recreated, and code that `json.Marshal`s engine state sees names.

# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to`. Cases that share one call go in one table test (`table-test` skill). `T` below stands for `GOTOOLCHAIN=go1.26.9 go test -race`. Dialect rows need Docker: Postgres through `dbtest.RunTestDatabase`, MySQL through `dbtest.RunTestMySQL`, both via `forEachDialect`. SQLite rows run without Docker.

Group 1 is a gate. Every later group names a sentinel (Q1) or an absence rule (Q2), so none of them starts until the maintainer's answers are recorded in design.md. Rebase onto `harden-definition-store-parity` before group 5 if it has landed. Check whether `surface-unresolvable-node` has landed. If it has, add its `IncidentKind` name row in 3.1 and in the spec's name table.

## 1. Decisions

- [ ] 1.1 Record the maintainer's answers to Q1 (sentinel shape) and Q2 (absent fields) in design.md. Replace every `(pending Q1)` marker in `specs/instance-store/spec.md`, `specs/human-task-lifecycle/spec.md` and `specs/restart-recovery/spec.md` with the chosen sentinel. If Q2 picks (a) or (b), add the absence rows to instance-store's *A stored instance enum outside the vocabulary is refused on read*, with a negative control for each. Verify: `openspec validate encode-persisted-enums-by-name --strict` and `scripts/check-openspec.sh` pass.

## 2. Task-state read decay (defect, reproduced on SQLite)

- [ ] 2.1 Red: in `internal/persistence/store`, add a `forEachDialect` table test for human-task-lifecycle's *Out-of-band task rows*. Each row upserts a task, overwrites `wrkflw_human_task.state` with raw SQL, and calls `Get`; the known-name rows are the negative controls. Also add *Raw state column after each transition* (it passes already, as a characterization). Verify: `T ./internal/persistence/store/ -run <test>` fails on every dialect for the out-of-vocabulary rows, because they read back `unclaimed` with a nil error. The controls pass.
- [ ] 2.2 Green: the SQL task store's state reader refuses an unknown name with the Q1 task-state sentinel, using the `TaskState` name table from 3.1. Do 3.1's `TaskState` part first if it is needed here. Verify: the 2.1 test is green on all three dialects, and `T ./internal/persistence/store/ -run HumanTask` stays green.

## 3. Encoders, name tables and the guard

- [ ] 3.1 Red, then green: add a name-table test per enum (design D5). It scans from 0 to one past the last constant, pins each literal name, asserts names are unique and no longer than 64 bytes, and covers the out-of-range and negative values. This makes *The full name table*, *The name does not depend on the constant's position*, *One past the last constant, and negative values* and *Strict decode, no integer arm* pass. Then add the name tables, `MarshalJSON` and `UnmarshalJSON` to `engine.Status`, `engine.TokenState` (plus `String()`, with `service/instance.go` `tokenStateString` delegating to it), `engine.IncidentKind`, `engine.TimerKind`, `engine.CompensationDisposition` and `humantask.TaskState`, each with its Q1 sentinel (design D1, D2). Verify: `T ./engine/... ./humantask/... ./service/...` red before and green after. `TestTokenStateString` and the HTTP instance-document tests pass unchanged.
- [ ] 3.2 Add a column-name table for `schedule.Kind` (`""` for `KindUnset`, then the `kindString` names), owned by the `schedule` package so the store and the wire format share it. Make `definition/model/trigger_wire.go` `kindString` delegate to it. Verify: `T ./definition/...` green, and the 3.1-style name test for `schedule.Kind` passes.
- [ ] 3.3 Add the guard test "persisted enums encode by name" (design D4). Its roots are `engine.InstanceState`, every trigger type cross-checked against `AllTriggerKinds`, the journal envelope, `model.NodeWire`, `model.TriggerWire`, `humantask.HumanTask` and `kernel.ArmedTimer`. It exempts types declared outside the module and names the failing path. If Q2 chose (a), it also fails on `omitempty` on a persisted enum field, except the envelope's disposition pointer. Verify: it is red when run against a copy with any one encoder removed (show it by temporarily deleting `TokenState.MarshalJSON`, then restore), and green at HEAD. `T ./internal/persistence/store/ -run PersistedEnums`.
- [ ] 3.4 Update `engine/state_token_provenance_test.go`'s legacy token fixture from `"State":2` to `"State":"joining"`, and add one row proving the integer form is refused (the *Strict decode, no integer arm* `TokenState` row). Rewrite the three "APPENDED deliberately … persisted as a plain integer" comments in `engine/state.go` and `engine/command.go`: order no longer matters for persistence, and the name table is the persisted contract. Cite `openspec/specs/instance-store`. Verify: `T ./engine/...` green, and `scripts/check-doc-refs.sh` passes.

## 4. Journal disposition by name

- [ ] 4.1 Red: add the *Disposition round-trip* rows on all three dialects, asserting on the raw `wrkflw_journal.payload` bytes. Add the journal rows of instance-store's *Out-of-band instance rows on each read path* (`"Retry"`, `0` and the `"abandon"` control). Verify: `T ./internal/persistence/store/ -run <test>` fails, because the payload holds `"disposition":0`, `1` or `2` and an integer is accepted.
- [ ] 4.2 Green: make the envelope's disposition `*engine.CompensationDisposition` (design D3) and drop the int conversions. Delete the "newer build round-trips unchanged" comment. Apply the Q2 journal-absence rule if one was chosen. Verify: the 4.1 rows and `T ./internal/persistence/store/ -run 'Trigger|Journal|Entries'` are green, and the guard from 3.3 still passes with the envelope as a root.

## 5. Columns and store binding

- [ ] 5.1 Red: add `forEachDialect` tests for instance-store's *Raw column and snapshot contents after create and commit*, restart-recovery's *Armed timer rows*, instance-store's *A state carrying a nameless value is not persisted*, the lister rows of *Each status and an out-of-range filter* (plus the in-memory row against `kernel.MemInstanceStore`), *A filter value is bound, never interpolated*, and restart-recovery's *Which rows each sweep removes*. Verify: `T ./internal/persistence/store/... ./runtime/kernel/...` fails on the raw-column assertions, which read integers, and on the out-of-range filter rows, which return an empty page with a nil error.
- [ ] 5.2 Record a baseline benchmark of snapshot marshal size and allocations for a representative state (design D7). Verify: the numbers are written into the PR description. They are not asserted.
- [ ] 5.3 Green: in all three `migrations/*/0001_init.sql`, change `wrkflw_instances.status`, `wrkflw_timers.kind` and `wrkflw_timers.trigger_kind` to `TEXT` (`VARCHAR(64)` on MySQL), keeping `NOT NULL`, with `trigger_kind DEFAULT ''` (design D6). Bind and scan names in `store_core.go`, `lister.go` (filter and count), `pruner.go` (both name lists) and `timerstore.go`, deleting all 19 `int16(...)` casts and both `int16` scan variables. The in-memory store refuses a nameless status filter. Verify: the 5.1 tests are green on every dialect, plus `TestMigrations_OneFilePerDialect`, `TestMigrationParity_LogicalSchemaConverges`, `TestMigrationParity_IndexNamesConverge` and `T ./internal/atrest/...`.
- [ ] 5.4 Update the six test files that write enum columns with raw SQL (`pruner_compensation_retry_test.go`, `pruner_conformance_test.go`, `pruner_neverdue_test.go`, `timerstore_conformance_test.go`, `timerstore_seek_test.go`, `persistence/pruner_test.go`) to bind names. Verify: `T ./internal/persistence/... ./persistence/...` green, and `grep -rn int16 internal/persistence persistence` matches no enum binding.
- [ ] 5.5 Add the Load and lister rows of instance-store's *Out-of-band instance rows on each read path* and every row of restart-recovery's *Out-of-band timer rows*, including the integer forms. Verify: green on all three dialects. The negative-control rows load.
- [ ] 5.6 Re-run the benchmark from 5.2. Verify: the after numbers are in the PR description beside the before numbers.
- [ ] 5.7 Document in the godoc of `persistence.Migrate`, `persistence.MigrateMySQL` and `persistence.MigrateSQLite` that a database created by a build before this change must be recreated, because the squashed `0001` is edited in place pre-release. Verify: `scripts/lint.sh` passes.

## 6. Lint

- [ ] 6.1 Remove the `G115` exclusion for `internal/persistence/` from `.golangci.yml`. Verify: `scripts/lint.sh` reports zero issues for every module. If an `int -> int16` conversion survives, narrow the exclusion to that file and state why in a comment, or remove the conversion.

## 7. Integration

- [ ] 7.1 Run the full suite for every module as CONTRIBUTING.md describes, with Docker. Verify: every module is green on go1.26.9, and `openspec validate encode-persisted-enums-by-name --strict`, `scripts/check-openspec.sh` and `scripts/check-doc-refs.sh` pass.

## Workflow follow-up

- The PR body names `encode-persisted-enums-by-name`. Its last commit runs `openspec archive encode-persisted-enums-by-name`.
- After merge, comment on kartaladev/wrkflw#44 with the change name and close it, if the planning PR's conversion has not already closed it.
- Tell `persist-pending-commands` that its blocker is archived and that it must add its envelope as a guard root. Tell `surface-unresolvable-node` whether its `IncidentKind` name row is already in, or is now owed by it.
- Hand `cut-v0-release` the BREAKING lines: enum names in the snapshot, journal and columns; databases from earlier builds must be recreated; a nameless status filter is refused; the task store refuses an unknown state.

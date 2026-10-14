# Design

## Context

See proposal.md, *Why*. These are the constraints, measured on main (`9c6dd2c1`):

- **Commit path.** `deliverLoop` builds `commitFn` (`runtime/processdriver.go:804`). Inside one `RunInTx` (`:833`) it writes the step and every timer arm or cancel. After the commit it activates timers (`:853`), syncs waiters (`:898`) and performs commands (`:900`), all in memory. A store without `kernel.TxRunner` runs `commitFn` without a transaction (`:837`), which is the documented degraded atomicity.
- **Command kinds** (`engine/command.go:259-270`): twelve. `perform` (`runtime/processdriver_action.go:315`) treats `CompleteInstance`, `FailInstance` and `SendMessage` as no-ops, because their effect is the outbox row in the commit. `ScheduleTimer` and `CancelTimer` never reach it. `Compensate` is reserved and never emitted.
- **Identity.** Only `InvokeAction` and `StartSubInstance` carry a `CommandID`. `AwaitHuman` and `UpdateTask` carry a task, and `ThrowSignal` carries nothing unique.
- **`InvokeAction.Scoped`** is an `action.Catalog`, a live value that cannot be persisted.
- **Enums.** `humantask.TaskState` is a bare `int` with no `MarshalJSON` (`humantask/humantask.go:24`). `UpdateTask` carries a whole `HumanTask`, so a naive JSON payload would persist an ordinal.
- **Claim precedent.** `CallNotifier.Run` (`runtime/calllink/notifier.go:180`) calls `ClaimPending`. That stamps `claimed_at`/`claimed_by` under `FOR UPDATE SKIP LOCKED` on Postgres and MySQL, and through a conditional update on SQLite (`dialect.SupportsSkipLocked`).
- **Migrations.** There is one squashed `0001_init.sql` per dialect, guarded by `TestMigrations_OneFilePerDialect`. No `v*` tag exists.
- **Ownership.** `CachingInstanceStore.Load` calls `owner.Acquire` first (`persistence/caching_instance_store.go:264`). The owner is the value passed to `NewCachingInstanceStore`, not any port the recovery code holds.
- **Prior attempt.** Branch `origin/fix/110-recover-unperformed-commands` at `bb759708` (PR #162) is the snapshot-mark design. Its `engine/pending_command.go` envelope and kind list are reused below. Its sweep is not.

## Goals / Non-Goals

**Goals:**
- Close the commit-to-perform window for every recoverable kind, on every store.
- Delete the instance-scan dependency that produced PR #162's five defect classes, instead of guarding it.
- One claim mechanism shared by the live driver and the worker, so "real exclusion" is a lease, not an age comparison.

**Non-Goals:**
- Exactly-once side effects. Delivery is at least once, and actions deduplicate on `_idempotencyKey` (D6).
- Recovering signal and message waiters (`recover-waiters-on-restart`).
- An instance pruner.

## Decisions

### D1. Commands become rows in `wrkflw_pending_commands`, written inside `commitFn`

The shape follows kartaladev/wrkflw#167. `commitFn` writes one row per recoverable command, after the step and timer writes, so the rows join the same transaction, including a lent one once `add-lent-transaction-seam` lands. After the commit, the driver performs each command and deletes its row by primary key.

*Rejected:* **(a) a mark inside the snapshot.** It was built in PR #162 and failed by measurement. The mark lives in a document the commit path replaces atomically, which led to a nil-map panic on `null`, a follow-up apply superseding the unperformed remainder, and progress needing a separate store capability. Driving recovery by instance scan through `CachingInstanceStore` also acquired ownership of every listed instance (`acquires=3 releases=0 held=3`), so the first replica to sweep owned everything.

*Rejected:* **a command queue on `wrkflw_outbox`.** The outbox carries events with reference payloads to external consumers. Mixing internal commands into it changes what relay consumers see.

### D2. The row key is `(instance_id, command_id)`, and `command_id` is derived from the step

`command_id` is `<committed version>:<ordinal in StepResult.Commands>`. The engine's own `CommandID`, where one exists, stays in the payload. The derived key is unique and stable for all five kinds. Ordinal order is perform order, so recovery of one instance performs its rows in `(version, ordinal)` order.

*Rejected:* **the engine `CommandID` as the key.** It is absent on three of the five recoverable kinds, as measured above.

### D3. Recoverable kinds: `InvokeAction`, `AwaitHuman`, `UpdateTask`, `ThrowSignal`, `StartSubInstance`

The kind list is the one PR #162's three review rounds cleared, and #167 records it as cleared by measurement. The excluded kinds are excluded because their effect is already durable or best-effort:
- timers are written in the commit;
- `CompleteInstance`, `FailInstance` and `SendMessage` are outbox rows written in the commit;
- `InvokeCancelAction` is best-effort by contract and runs against an already-terminal instance;
- `Compensate` is never emitted.

`ThrowSignal` stays recoverable even though its duplicate fans out to other instances. The alternative is a signal lost silently, which is the very class this change closes. The fan-out window is documented on the kind constant.

### D4. Claims are leases, and the live driver's own rows are inserted already claimed

Columns:
- `instance_id`, `command_id`, `kind` (TEXT name);
- `payload` (JSON);
- `def_id`, `def_version`;
- `attempts`, `next_attempt_at`;
- `claimed_at`, `claimed_by`;
- `last_error`, `dead_lettered_at`;
- `created_at`.

The committing driver inserts each row with `claimed_by` set to itself and `claimed_at` set to now. A worker claims only rows whose claim is older than the lease and whose `next_attempt_at` has passed. On Postgres and MySQL the claim uses `FOR UPDATE SKIP LOCKED` plus an update, as `ClaimPending` does. On SQLite it uses a conditional `UPDATE … WHERE claimed_at < ?`, whose affected-row count is the exclusion. A lease is held for one perform. The lease must exceed the longest action timeout, and the worker option documents this.

*Rejected:* **an age comparison with no claim write.** #167 measured two performs of one row under concurrency, and the claim brings that to one. *Rejected:* **instance ownership (`WithInstanceOwnership`) as the exclusion.** It reintroduces the coupling between the wrapper and the recovery unit that D1 removes.

### D5. Recovery queries rows, groups them by instance, and loads only those instances

A pass:
1. claims up to the batch size;
2. loads each claimed row's instance and definition through the driver's ordinary paths;
3. calls `perform`;
4. feeds any follow-up trigger through the CAS-retrying `ApplyTrigger` path;
5. deletes the row.

Ownership is acquired only for an instance being recovered, as an ordinary trigger delivery would acquire it. The regression test for R3-F1 builds a **real** `CachingInstanceStore` over the **same** ownership value the driver gets, because #167 measured that the mock wiring used before cannot express the defect.

### D6. A re-drive is a delayed perform, not a re-decision

Recovery performs exactly what the live driver would have performed after that commit, including commands of a step that made the instance terminal (an end event throwing a signal). Each outcome is absorbed and deletes the row:
- `ErrTokenNotFound` and `ErrInstanceTerminal` from the follow-up;
- `ErrInstanceExists` from a deterministic child id (`childInstanceIDFor`).

The `InvokeAction` input is persisted verbatim, so `_idempotencyKey` (`engine/step_state.go:380`) is identical on re-drive. `Scoped` is not persisted. On re-drive the runtime re-resolves the scope from the definition and the node that issued the command, the same lookup `FailingActionName` already performs for `ActionFailed`. `reserve-engine-variable-keys` D2 already says an engine-emitted child start is not refused for reserved names, whichever change lands first.

### D7. `AwaitHuman` re-drive is insert-if-absent

Today `performAwaitHuman` calls `tasks.Upsert`, which replaces the row and would erase a claimant written before the crash. On the recovery path it calls a create-only write, and an existing row counts as performed. The live path is unchanged.

### D8. The red, and the fault-injection seam it needs

As #110's own thread corrects, the issue's "passes today, fails after the fix" is not a red. Three artifacts:
1. `TestCommittedStepWithUnperformedCommandsIsRecoveredOnRestart`, which fails today.
2. A fault-injection guard: a spy counter reads exactly 0 before the restart and at least 1 after, so a fault that silently fails to apply cannot record a false green.
3. A test-only hook, in `export_test.go`, that stops `deliverLoop` immediately before the command loop at `:900`. It must not sit right after `RunInTx`, or it would also suppress timer activation and waiter sync.

No existing test stops a driver mid-step. The nearest precedent is `runtime/timer_txflow_test.go`.

### D9. Attempts, backoff, dead-letter (kartaladev/wrkflw#182)

A perform that returns an error is an infrastructure failure: a task-store write, a missing definition, a signal bus. It increments `attempts`, records `last_error`, clears the claim and sets `next_attempt_at` from an exponential backoff with jitter. An action's own error is a business outcome. It becomes `ActionFailed`, follows the retry policy and deletes the row. At the configured maximum, `dead_lettered_at` is set and the row is excluded from claims. What reports it, and how an operator releases it, is Q2.

### D10. The envelope encodes by name, and this change lands after `encode-persisted-enums-by-name`

Kinds are TEXT names (`invoke_action`, …) under the decided rule kartaladev/wrkflw#25. The payload embeds `HumanTask`, whose `TaskState` is an ordinal today. Landing after #44 means the envelope only needs adding as one more root of #44's reflective guard. Landing first would ship ordinals that #44 then has to migrate. That ordering is the `Depends on` entry.

### D11. No umbrella `Recover(ctx)` here

`recover-waiters-on-restart` leaves this change to decide whether to add one entry point covering timers, waiters and commands. It does not add one. Timer and waiter rehydration are one-shot boot calls. Pending-command recovery is a long-running claim loop with a lease, the same shape as `CallNotifier.Run`. Folding the two into one call would give a one-shot call a background lifecycle, or give a loop a one-shot one. `service.ProcessEngine` already composes the pieces it starts.

*Rejected:* **an umbrella call.** It couples two changes for a convenience that `service` already provides.

### D12. In-memory parity

`runtime/kernel` gets an in-memory pending-command store with the same claim, lease and attempt semantics. It cannot survive a process restart. It exists so that perform-failure re-drive and the conformance suite hold on every store.

## Risks / Trade-offs

- [An action runs longer than the claim lease, so a worker re-performs it concurrently.] → The lease option documents that it must exceed the action timeout. Delivery is at least once, and `_idempotencyKey` deduplicates.
- [A re-driven `ThrowSignal` re-triggers an instance that armed after the first delivery.] → Documented on the kind (D3). An instance that already consumed its arm is unaffected.
- [Every step now writes N extra rows and deletes them, which adds write amplification on the hot path.] → Rows are written only for the five recoverable kinds. Benchmark task 6.3 measures the commit path before and after, and the PR reports the delta.
- [Dead-lettered rows accumulate with no pruner.] → Q2 decides release. A `Pruner` method for dead-lettered rows older than a cutoff is added with them.
- [A database migrated by an earlier build lacks the table, so every step commit fails.] → Q3. The failure is loud (a missing-table error at commit), never silent.

## Migration Plan

1. Land after `encode-persisted-enums-by-name`. Edit each dialect's `0001_init.sql` in place (Q3), adding the table and the claim index `(next_attempt_at, claimed_at) WHERE dead_lettered_at IS NULL`. MySQL and SQLite get the closest equivalent, and index names converge under `TestMigrationParity_IndexNamesConverge`.
2. Pre-release databases are recreated. Rollback is a revert. The table holds only in-flight work, so dropping it loses at most the unperformed commands of crashed steps, which is today's behavior.

## Cross-change coupling

- **`encode-persisted-enums-by-name`** (#44), the owner of enum encoding. It goes first (D10). Both edit all three `0001_init.sql` files. Whichever lands second re-runs the parity and `internal/atrest` guards.
- **`harden-definition-store-parity`** edits `migrations/mysql/0001_init.sql`, a different block of the same file. The merge is additive. Its Q3 and this change's Q3 are the same maintainer question and should get one answer.
- **`add-lent-transaction-seam`** (#109) owns `WithTx`, `Continuation` and savepoints. The rows are written inside `commitFn`, so they join a lent transaction with no extra code. The rows also make `Discard`, or a `Resume` that never comes, recoverable. Both changes recommend landing the seam first (Q1). The shared file is `runtime/processdriver.go` (`deliverLoop`), and the second lander rebases.
- **`add-user-task-updates`** (#41) owns `wrkflw_task_update`. Per #29 (decided), an update parks on an `InvokeAction`, so its re-drive comes from this change. A `submitted` row with no closing row becomes the observable result of a dead-lettered or still-pending command.
- **`replace-casbin-with-composed-deciders`** (#114, decided) may reshape `authz.AuthzSpec`, which the `AwaitHuman` payload persists. Whichever lands second updates the envelope golden test.
- **`recover-waiters-on-restart`** (#181) adds requirements to the same `restart-recovery` spec. The deltas are additive, and the Purpose line is rewritten once, at the second archive. Its review flagged that the instance-scan requirement read unscoped. It now says *pending-command* recovery, so a waiter rehydration that reads snapshots is not forbidden. The umbrella entry point it deferred here is declined (D11).
- **`reserve-engine-variable-keys`**: see D6.
- **`split-persistence-modules`** moves the migration files, and the second lander carries the edit.

## Open Questions

- **Q1. Landing order against `add-lent-transaction-seam`.** The #109 thread asks for this to be re-decided deliberately.
  - (a) This change first: `Resume` becomes a fast path over a safety net, and `main` never ships `Discard` with nothing to recover what it drops.
  - (b) The seam first: the window is open on purpose between the two merges, which #22 call 8 already accepts.
  - Recommended: **(b)**, the same as `add-lent-transaction-seam`'s own Q1. This change is XL and waits on `encode-persisted-enums-by-name`, and the seam unblocks kartaladev/worktrack#26, `add-gorm-adapter` and `join-ambient-publish`. The maintainer answers it once for both changes. The specs and tasks are unaffected. Under (a), `add-lent-transaction-seam` adds this change to its Depends on.
- **Q2. What a dead-lettered row is (#182).**
  - (a) The row is retained with `dead_lettered_at`, logged at error with a metric, listed through the store, and released by an explicit operator re-drive call that resets `attempts`.
  - (b) (a), plus a new `IncidentKind` on the instance, resolvable through `ResolveIncident`. That adds a persisted enum and a step on the instance.
  - (c) No cap: backoff only, capped at a maximum interval.
  - Defaults for the maximum attempts and backoff are also open.
  - Recommended: **(a)**, with the backoff expressed in `retry-policy`'s backoff vocabulary (initial interval, multiplier, maximum interval, jitter) so one set of semantics serves both. The default values are the maintainer's call. The dead-letter scenarios in the spec are marked *pending Q2*. Under (c) they are removed. Under (b) one scenario is added in `operator-incidents`.
- **Q3. Migration window.**
  - (a) An in-place edit of `0001_init.sql`, the stance recorded on #44.
  - (b) A `0002` migration, which breaks `TestMigrations_OneFilePerDialect`.
  - Recommended: **(a)**, answered once with `harden-definition-store-parity` Q3.

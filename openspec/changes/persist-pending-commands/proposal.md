# Proposal

## Why

When a step commits, its commands are performed only afterwards, in process memory. A crash between the commit and the perform leaves an instance with a durable state and nothing that will ever move it. Timers are re-armed on restart; nothing re-drives an action, a human task row, a signal or a child start. The lent-transaction seam (`add-lent-transaction-seam`) will let a consumer reach this state on purpose, by never calling `Resume` or by calling `Discard`. A public API that can produce a stuck instance makes this a contract hole, not a crash residual.

## What Changes

- **New: commands as durable rows.** A step writes one row per command it will perform after commit. The row is written in the same transaction as the step, keyed by instance and command, and deleted by primary key once performed. A worker claims the rows nobody performed and re-drives them. This is the shape kartaladev/wrkflw#167 sets out (option (b) of kartaladev/wrkflw#110, "the destination recorded on #22"). Absorbs kartaladev/wrkflw#167.
- **Recovery for a committed step whose commands were never performed.** After a restart, every unperformed command of `InvokeAction`, `AwaitHuman`, `UpdateTask`, `ThrowSignal` and `StartSubInstance` is performed once a worker claims it. This holds on Postgres, MySQL, SQLite and the in-memory stores. Absorbs kartaladev/wrkflw#110. Option (a), a mark inside the snapshot, is **superseded**: it was built (PR #162), reviewed for three rounds and closed unmerged.
  - Correction to #110's text, carried from its own thread: the issue asks for a test that "passes today and fails once the fix exists". That is not a red. The red is a recovery test that fails today, plus a fault-injection guard proving `perform` was skipped before the restart (design D8).
- **Re-drive policy: attempts, backoff, dead-letter.** A row carries an attempt count and a claim time. A failed re-drive is retried after a backoff. After a configured maximum it stops being re-driven and is reported. Absorbs kartaladev/wrkflw#182. How a dead-lettered row is reported and released is open (Q2).
- **Recovery reads rows, never instances.** It loads only instances that have a pending row. This removes the defect that ended PR #162 (R3-F1): an instance scan through `CachingInstanceStore` took ownership of every instance it listed, including the healthy ones.
- **Measured properties PR #162 established, carried as requirements.** Per-command progress: a part-failed step re-runs only what did not succeed (measured 11 duplicate charges down to 1). A re-driven `AwaitHuman` never erases an existing claimant. Two concurrent workers perform a row once, not twice. A corrupt or unknown row never panics and never blocks other rows.
- **Delivery is at least once.** A crash after a perform but before its row is deleted re-drives it. A re-driven `InvokeAction` gets the same input, including the engine's `_idempotencyKey`, so an action can deduplicate.

Re-measured on main (`9c6dd2c1`):
- The commit is at `runtime/processdriver.go:833`, and the post-commit `perform` loop starts at `:900`. Nothing in `runtime/` re-drives a parked action token.
- `CachingInstanceStore.Load` acquires ownership first, at `persistence/caching_instance_store.go:264`. #167 cites `:305`, which is the line on the PR #162 branch. The property it describes holds on main.
- No instance pruner exists. `Pruner` has five methods, and `NeverDueTimerReclaimer` is a sixth retention call. No store deletes from `wrkflw_instances`.
- **Stale premise in #167:** "replaces `engine.InstanceState.PendingCommands`". That field exists only on the unmerged branch `bb759708`. Main has no pending mark of any kind, so nothing is replaced and nothing needs migrating.
- **Imprecise in #110, not stale:** it places the fault "after `RunInTx` returns and before `perform`". Between those two points the driver also activates timers and syncs waiters (`:853-898`). The fault must sit immediately before the command loop at `:900`, so it does not also suppress timer activation (design D8).

Impact flags:
- **Persisted format: YES.** A new table, `wrkflw_pending_commands`, is added in all three dialects. The command payload is a new persisted document. It encodes kinds and every enum by name, as the #25 decision requires (kartaladev/wrkflw#25, decided). This change follows the squashed one-file-per-dialect policy and edits `0001_init.sql` in place (Q3). A database created by an earlier build lacks the table, and goose will not re-run version 1. Such a database must be recreated, or its step commits fail.
- **BREAKING (persisted, pre-release only):** a database migrated before this change cannot run this build's step commits until the table exists (Q3).
- **Default security posture: unchanged.** A re-driven `AwaitHuman` carries the eligibility frozen at commit. #167 measured that this is not an escalation path: claim, reassign and complete re-authorize live.
- **Consumer `go.mod`: unchanged.**
- **New exported surface:** a pending-command store capability, a recovery worker the consumer starts (as with `CallNotifier.Run`), its options, and the sentinels `engine.ErrUnknownPendingCommand` and `engine.ErrMalformedPendingCommand`.

Out of scope:
- Re-registering signal and message waiters on restart: `recover-waiters-on-restart` (kartaladev/wrkflw#181).
- The lent transaction, `Continuation`, `Resume` and `Discard`: `add-lent-transaction-seam` (kartaladev/wrkflw#109). This change only makes rows join whatever transaction the step runs in.
- Replaying a `submitted` task update with no closing row: `add-user-task-updates` (kartaladev/wrkflw#41). Under the #29 decision, an update parks its token on an `InvokeAction`, so this change re-drives it with no extra code. The audit rows belong to #41.
- Converting existing enum columns and snapshot enums to names: `encode-persisted-enums-by-name` (kartaladev/wrkflw#44).
- Moving migration files into a separate module: `split-persistence-modules`.
- An instance pruner: no change owns it. The gap is recorded in design, not filed.

Decisions this change cannot be fully specified without:
- **Q1. Landing order against `add-lent-transaction-seam`.** #109's thread says "re-decide this deliberately; do not inherit it". That change's own Q1 recommends landing the seam first, knowingly shipping the unresumed-`Continuation` window. This change agrees: it is XL, waits on `encode-persisted-enums-by-name`, and would hold back kartaladev/worktrack#26, `add-gorm-adapter` and `join-ambient-publish`. Recommended: the seam first. The specs do not depend on Q1.
- **Q2. What a dead-lettered row is (#182).** The options are a retained row with a log, a metric and an operator re-drive call, that plus an engine incident on the instance, or no cap at all. Recommended: the first option. Specs mark the dead-letter rows *pending Q2*. The attempt and backoff rows do not depend on it.
- **Q3. Migration window.** The options are an in-place `0001_init.sql` edit (the stance recorded on #44, which `harden-definition-store-parity` Q3 also asks to confirm) or a `0002` migration. Recommended: in place. The specs describe a store migrated by this version.

**Coordination**
- Size: XL
- Depends on: establish-baseline-specs, encode-persisted-enums-by-name
- Blocks: add-user-task-updates, cut-v0-release
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `restart-recovery` (archived baseline design D2 names it as the home for command recovery). It gains these requirements:
  - commands are durable with their step;
  - unperformed commands are performed after a restart;
  - recovery reads rows and never scans instances;
  - one perform per claim under concurrency;
  - per-command progress;
  - an `AwaitHuman` re-drive preserves the task row;
  - a re-drive is idempotent toward the instance;
  - corrupt and unknown rows are isolated;
  - attempts, backoff and dead-letter.

## Impact

- `internal/persistence/store/migrations/{postgres,mysql,sqlite}/0001_init.sql`: new `wrkflw_pending_commands` table and its claim index. Guards: `TestMigrationParity_LogicalSchemaConverges`, `TestMigrationParity_IndexNamesConverge`, `TestMigrations_OneFilePerDialect` and the `internal/atrest` census.
- `internal/persistence/store`: the pending-command store with claim, delete and re-stamp, plus conformance tests on all three dialects.
- `runtime/kernel`: the store port and its in-memory implementation.
- `engine`: the persisted command envelope, `ErrUnknownPendingCommand` and `ErrMalformedPendingCommand`.
- `runtime/processdriver.go` (`deliverLoop`): rows written inside `commitFn`, and each row deleted after its perform.
- `runtime`: the recovery worker, its options and metrics. `service.ProcessEngine` starts and stops it.
- `runtime/processdriver_action.go` (`performAwaitHuman`): insert-if-absent on re-drive.
- Persisted-enum guard from `encode-persisted-enums-by-name`: gains the envelope as a root.
- Docs: `runtime/README.md` (recovery, at-least-once delivery), and the `restart-recovery` Purpose line at archive.

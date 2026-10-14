# Tasks

Every Go task starts with `cc-skills-golang:golang-how-to`, and every task is test-first: red, then green, then refactor. Dialect rows need Docker (testcontainers). `V` below stands for `GOTOOLCHAIN=go1.26.9 go test -race`.

## 1. Decision gate

- [ ] 1.1 Record the maintainer's answers to design Q1 (landing order), Q2 (dead-letter form and defaults) and Q3 (migration window) in design.md, update the spec rows marked *pending Q2* to match, and verify with `openspec validate persist-pending-commands --strict`.
- [ ] 1.2 Confirm that `encode-persisted-enums-by-name` is archived (`openspec list --archived --json`), as the Depends on line requires, and verify that its reflective guard test exists on main.

## 2. The red and the fault-injection seam

- [ ] 2.1 Add a test-only hook in `runtime/export_test.go` that stops `deliverLoop` immediately before the command loop, after timer activation and waiter sync (design D8). Verify that a hook-only test still sees the step's timer activated: `V ./runtime/ -run TestFaultHookLeavesTimerActivation`.
- [ ] 2.2 Write `TestCommittedStepWithUnperformedCommandsIsRecoveredOnRestart`, the scenario "A service task parked by a crash completes after restart", SQLite row first. Include the guard that the spy reads 0 before restart and at least 1 after. Watch it go red: `V ./runtime/ -run TestCommittedStepWithUnperformedCommandsIsRecoveredOnRestart` fails on the post-restart assertion, and the guard assertion passes.
- [ ] 2.3 Write the red for "A user task whose task row was never written gets its row", and watch it fail the same way.

## 3. Persisted envelope and schema

- [ ] 3.1 Add the engine envelope over the five recoverable kinds (design D3), encoding the kind by name, with `ErrUnknownPendingCommand` and `ErrMalformedPendingCommand`. Add a golden test of the kind-name table, and add the envelope as a root of the persisted-enum guard. Verify with `V ./engine/ -run 'PendingCommand'`, and with a mutation check: decoding through a struct that drops unknown keys must turn the guard red.
- [ ] 3.2 Add `wrkflw_pending_commands` and its claim index to the three `0001_init.sql` files (design D4, Migration Plan). Verify with `V ./internal/persistence/store/ -run 'TestMigration'` and `V ./internal/atrest/...`.
- [ ] 3.3 Add the kernel store port and the in-memory store (D12), plus the SQL store with insert, claim (lease, `next_attempt_at`, batch), delete, re-stamp-on-failure and dead-letter. Add conformance tests on Postgres, MySQL, SQLite and in-memory covering these scenarios:
  - "Two workers racing for one row perform it once";
  - "A claim abandoned by a dead worker becomes claimable again";
  - "One pass claims no more rows than its batch size";
  - "A row is not re-claimed inside its backoff".

  Verify with `V ./internal/persistence/store/ ./runtime/kernel/ -run PendingCommand`.

## 4. Commit path

- [ ] 4.1 Write rows inside `commitFn`, inserted already claimed by the committing driver, and delete each row after its perform. These make pass: "Rows are written per command kind on every store", "Commands whose effect is already durable write no row", "A failed commit leaves no pending-command row", and "Negative control, a performed command leaves no row behind". Verify with `V ./runtime/ -run 'PendingCommandRows|TxFlow'`, and confirm that the existing `transaction-seam` scenarios stay green.
- [ ] 4.2 Make `AwaitHuman` insert-if-absent on the recovery path (D7). These make pass: "A claimant survives a duplicate AwaitHuman re-drive" and "Negative control, a missing task row is created". Verify on all three dialects with `V ./runtime/ -run AwaitHumanRedrive`.

## 5. Recovery worker

- [ ] 5.1 Implement the recovery worker (D5, D6): claim, load the marked instance, re-resolve the action scope, perform, feed the follow-up through the CAS-retrying path, and delete. Tasks 2.2 and 2.3 go green. These also make pass:
  - "Each recoverable kind reaches its effect after restart";
  - "Commands of a step that ended the instance are still performed";
  - "A running worker picks up rows left by another process's crash";
  - "A follow-up that is no longer applicable is absorbed";
  - "The re-driven action receives the original input";
  - "An engine-emitted child start is not refused for reserved keys".

  Verify with `V ./runtime/ -run 'Recover'`.
- [ ] 5.2 Write the R3-F1 regression, "Healthy instances are never acquired by recovery". Build a real `CachingInstanceStore` over the same ownership value passed to the driver, never a mock. Prove that it can fail by temporarily routing recovery through an instance list and watching it go red. Then add "A second replica recovers rows the first replica never touched" on Postgres. Verify with `V ./runtime/ ./persistence/ -run 'RecoveryOwnership'`.
- [ ] 5.3 Per-command progress: write "A part-failed step re-drives only the remainder", including the 3-command, 10-failure row that reproduces #110's 11-to-1 measurement. Verify with `V ./runtime/ -run RecoveryProgress`.
- [ ] 5.4 Isolate corrupt and unknown rows: write "A bad row does not block healthy rows" (every Examples row, including the null and case or whitespace variants) and its negative control. Verify with `V ./runtime/ -run RecoveryBadRow`, and confirm that no `recover()` is needed for the null row.
- [ ] 5.5 Attempts, backoff and dead-letter (D9, in the Q2 answer's form): write "Attempts are counted and bounded", "Negative control, a transient failure recovers below the limit" and "Business failure of an action is not a re-drive failure". Verify with `V ./runtime/ -run RecoveryAttempts`.
- [ ] 5.6 Lease timing: write "A row the live driver is still performing is not taken". Use waits per `docs/agents/eventually-waits.md` and deadlines per `docs/agents/test-deadlines.md`. Verify with `V ./runtime/ -run RecoveryLease -count=20`.
- [ ] 5.7 Make `service.ProcessEngine` start and stop the worker. Verify with `V ./service/...`: `Shutdown` stops the worker, and goleak stays clean.

## 6. Docs, comments, performance

- [ ] 6.1 Document the worker, the lease-versus-action-timeout rule, at-least-once delivery and `_idempotencyKey` in godoc and `runtime/README.md`. Code comments cite `openspec/specs/restart-recovery/spec.md`, never a change path. Verify with `scripts/check-doc-refs.sh`.
- [ ] 6.2 Put the `ThrowSignal` fan-out window and the excluded-kind reasons on the kind constants (D3). Verify by review, and with `go vet ./engine/...`.
- [ ] 6.3 Benchmark the commit path with and without recoverable commands before and after the change (`cc-skills-golang:golang-benchmark`, `benchstat`), and record the delta in the PR body. Verify that the benchmark runs: `GOTOOLCHAIN=go1.26.9 go test -run '^$' -bench DeliverLoop ./runtime/`.

## 7. Integration

- [ ] 7.1 Run the full suite per module: `for pat in $(scripts/modules.sh); do GOTOOLCHAIN=go1.26.9 go test -race "$pat"; done` with Docker. Then run lint per module, and `scripts/check-openspec.sh`.

## Workflow follow-up

- Rewrite the `restart-recovery` Purpose line in `openspec/specs/restart-recovery/spec.md` so command recovery is promised, not "left to future changes". This happens in the archive commit, coordinated with `recover-waiters-on-restart`.
- The PR's last commit runs `openspec archive persist-pending-commands --yes`.

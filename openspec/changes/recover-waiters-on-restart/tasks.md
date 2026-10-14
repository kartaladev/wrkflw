# Tasks

Apply waits for the maintainer's rulings on Q1 to Q4 (design, *Open Questions*). Group 1 does not depend on any of them and can start first. Groups 2 and 3 assume the recommended Q1 (a) and Q2 (a). If the ruling is Q1 (b) or (c), replace task 2.1 with a migration-and-backfill group on all three dialects, coordinated with `persist-pending-commands` and `split-persistence-modules` (design, *Cross-change coupling*). Every Go task starts with `cc-skills-golang:golang-how-to`. Run commands per module, as CONTRIBUTING.md describes. Waits follow `docs/agents/eventually-waits.md`.

## 1. Reproduce the defect (red)

- [ ] 1.1 Write the failing in-memory tests in `runtime/`: "Every message-waiting construct resumes after a restart" (all four rows), "Every signal-waiting construct resumes after a restart" (all four rows), and "A keyed wait is not shadowed by a message start after a restart". The restart is simulated by discarding the driver and its `SignalBus` and building fresh ones over the same store. Until the entry point exists, the "recovers waiters" step is a no-op hook. Watch the tests go red: on main the instance stays parked. For the shadowing scenario, record whether a second instance is created; this confirms or refutes the proposal's hypothesis. Verify with `GOTOOLCHAIN=go1.26.9 go test ./runtime/ -run 'TestRecoverWaiters'`.
- [ ] 1.2 Write the failing ownership test for "Recovery over a caching store leaves ownership untouched" (all three rows). It uses a real `persistence.NewCachingInstanceStore` over a SQLite store and a counting `InstanceOwnership`, and that same value is the driver's instance store (design D3). Never use a mock in either slot. Prove the test can fail by temporarily driving recovery through `Load`: the count must exceed 0. Verify with `GOTOOLCHAIN=go1.26.9 go test ./persistence/ -run TestRecoverWaitersOwnership`.

## 2. Snapshot enumeration without ownership (Q1 a)

- [ ] 2.1 Add the optional read capability that streams the snapshots of non-terminal instances over the status index. Implement it on the in-memory store, the Postgres, MySQL and SQLite stores, and `persistence.CachingInstanceStore`, which delegates to its backing store and never calls `Acquire`. Add a conformance test on every dialect: it returns exactly the non-terminal instances, a corrupt row fails with an error naming the instance id rather than decaying to an empty state, and an empty store returns none. Verify with `GOTOOLCHAIN=go1.26.9 go test ./internal/persistence/store/ ./persistence/`, with Docker running for the Postgres and MySQL rows.

## 3. Waiter recovery (Q2 a)

- [ ] 3.1 Add `ProcessDriver.RehydrateWaiters(ctx) error`. It enumerates through 2.1 and feeds each state through the live reconciliation (design D2), with per-instance version-guarded application (design D4). With no read capability wired, it returns an error naming the missing capability, matching `RehydrateTimers`' refusal. Verify that 1.1 and 1.2 go green, and that "A keyless wait resumes after a restart", "Negative control, a different key does not resume the recovered wait" (all six rows), "Negative control, another signal name does not resume the recovered wait" and "Two parked instances on one key keep the live ambiguity outcome" pass, with `GOTOOLCHAIN=go1.26.9 go test ./runtime/ ./persistence/`.
- [ ] 3.2 Add the terminal-instance tests "A terminal instance is not woken after a restart" (all three statuses) and "Negative control, a running instance with the same arm is woken". Verify with `GOTOOLCHAIN=go1.26.9 go test ./runtime/ -run TestRecoverWaitersTerminal`.
- [ ] 3.3 Add the concurrency tests "Recovering twice resumes a wait once", "A wait left during recovery is not re-registered" and "A wait entered during recovery is reachable afterwards". Verify with `GOTOOLCHAIN=go1.26.9 go test -race -count=20 ./runtime/ -run TestRecoverWaitersConcurrent`.
- [ ] 3.4 Add the durable rows: "A message wait survives a restart on every SQL dialect" (Postgres, MySQL, SQLite), "A broadcast after a restart reaches every parked instance" (all six rows), and "Recovery holds no advisory lock on Postgres". Verify with `GOTOOLCHAIN=go1.26.9 go test ./runtime/ -run TestRecoverWaitersDurable`, with Docker running.
- [ ] 3.5 (Pending Q4.) Under (i), log a one-time WARN on the first `DeliverMessage` or `BroadcastSignal` of a driver with a durable store that has not recovered, and test it with a log-capturing handler. Under (ii), add the sentinel and its requirement first. Verify with `GOTOOLCHAIN=go1.26.9 go test ./runtime/ -run TestRecoverWaitersWarn`.

## 4. Documentation and wiring

- [ ] 4.1 Document `RehydrateWaiters` in its godoc and in `runtime/README.md`, next to `RehydrateTimers`, including the start-up ordering (Q4). Its godoc cites `openspec/specs/restart-recovery/spec.md`. Call it in `examples/production_wiring`, `examples/sqlite_wiring` and `examples/mysql_wiring`. Verify with `go build ./...` in the examples module, `scripts/check-doc-refs.sh` and `go vet ./...`.

## 5. Integration

- [ ] 5.1 Run the full suite per module, and `scripts/check-openspec.sh`. Verify that both exit 0.
- [ ] 5.2 At archive, edit the *Purpose* of `openspec/specs/restart-recovery/spec.md` so it no longer says waiter re-registration is "not yet promised" (design D5). If `persist-pending-commands` has archived first, keep its wording for commands. Verify with `openspec validate --specs --strict`.

## Workflow follow-up

- The PR body names `recover-waiters-on-restart`, and its last commit runs `openspec archive recover-waiters-on-restart`.
- After merge, comment on kartaladev/wrkflw#181 with the change name and close it.

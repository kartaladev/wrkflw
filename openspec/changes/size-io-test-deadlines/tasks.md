## 1. Measure the current state

This change has no specs and fixes no defect, so no task starts with a failing test. Verification is by measurement and by the repo's doc guards.

- [ ] 1.1 Run an AST classification pass over every `_test.go` file in every workspace module (`scripts/modules.sh`). It classifies each `select` clause on `time.After` into the five constructs of `docs/agents/test-deadlines.md`, with an UNCLASSIFIED bucket. Keep the pass outside the repo, because it is a measurement and not a deliverable. Verify: the per-construct counts sum to the raw total that `scripts/check-test-timeout.sh` prints (58 on `eebe55ba`, or whatever `main` now prints), and every UNCLASSIFIED site has been read and filed by its clause body.
- [ ] 1.2 Within the Timeout bucket, mark each site as a hang watchdog or not, and as waiting on a cross-process or cross-container event or not (design D3). Verify: `scheduler/elector_test.go:179` lands in the qualifying set, `scheduler/trigger_test.go:749` lands in the watchdog exclusion, and the qualifying count is recorded with the commit it was measured on.
- [ ] 1.3 Re-measure the evidence state. Search every retrievable failed CI run log for `TestSchedulerElectorOnLeadershipAcquired` and `advisory lock not obtained`. Verify: the window covered, the count of logs searched, the count expired, and the count of hits are all recorded. The proposal's baseline is 117 searched, 19 expired and 0 hits, for 2026-07-12 to 2026-10-14.

## 2. Write the note

- [ ] 2.1 Update the construct table in `docs/agents/test-deadlines.md` with the counts from 1.1. Rewrite the Timeout row's "Wants to be" cell so that it says generous applies only to non-watchdog real-I/O timeouts, and only once evidence exists (design D3, D2). Fix the prose totals ("45 … across 27 files") to match. Verify: every number in the table and in the prose matches the 1.1 output, and the rows sum to the total.
- [ ] 2.2 Add a section on sizing deadlines that wait on real I/O. It states the three-part qualification (D3), the default of not widening, the two acceptable evidence triggers and the signature to search for (D2), that the eventual fix is one shared, named constant whose value is derived from that evidence, and the evidence state from 1.3 with its date and window. Use the elector site as the worked qualifying example and the trigger watchdog as the worked exclusion. Replace the existing forward-reference in "What the guard can and cannot do" with a pointer to this section. Verify: a reviewer can classify `scheduler/elector_test.go:179`, `scheduler/trigger_test.go:749` and `internal/persistence/store/notifier_pgx_test.go:299` from the note alone and reach qualify, exclude and stay-short respectively.
- [ ] 2.3 Run the repo's doc and OpenSpec guards. Verify: `scripts/check-doc-refs.sh` and `scripts/check-openspec.sh` pass, `scripts/check-test-timeout.sh` output is byte-identical to before the change (no `_test.go` file was touched), and `git diff --stat` shows only `docs/agents/test-deadlines.md` plus this change's directory.

## Workflow follow-up

- The PR body names `size-io-test-deadlines`. Its last commit runs `openspec archive size-io-test-deadlines`.
- After merge, comment on kartaladev/wrkflw#98 with the change name and close it.

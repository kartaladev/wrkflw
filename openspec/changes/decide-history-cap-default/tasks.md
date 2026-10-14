# Tasks

**Do not start until design Open Question Q1 is answered.** Q1 decides the `instance-store` delta and group 1. If option (b) is chosen, re-plan group 1 and the delta before apply. The tasks below assume the recommended option, Q1 (a).

`split-persistence-modules` may land before or after this change. Re-measure every path below at apply, because that change moves `internal/persistence/store` and makes `persistence` its own module. Run Go commands from inside the owning module, as `CONTRIBUTING.md` describes, with `GOTOOLCHAIN=go1.26.9`. Postgres and MySQL rows need Docker (testcontainers).

## 1. History-cap default and journal promise (kartaladev/wrkflw#37, `instance-store` delta)

These three requirements describe current behavior, so a passing test proves nothing on its own (design D2). For each task, write the test, then apply the named mutation locally, watch the test go red, and revert the mutation. Record the red output in the PR.

- [ ] 1.1 Add a table test beside `TestBaselineInstanceStoreCapsClosedVisits` (`persistence/baseline_instance_store_test.go` at `eebe55ba`). It drives *Unset and non-positive caps keep every visit* (all seven rows) and *A long uncapped history survives reopening the store on each dialect* (Postgres, MySQL, SQLite; 500 closed visits; close and reopen the store before `Load`). Mutation to watch red: make an unset cap behave as a cap of 1. Verify: `go test -race -run 'TestInstanceStoreHistory' ./...` in the `persistence` module.
- [ ] 1.2 Add a table test for *Journal length is independent of the cap* (six rows over three dialects), asserting both the loaded closed-visit count and `len(Entries) == 6`. Mutation to watch red: apply the cap to the journal write path. Verify: `go test -race -run 'TestInstanceStoreJournalIgnoresCap' ./...` in the `persistence` module.
- [ ] 1.3 Extend `persistence/unsafe_config_test.go` until it covers *Warning follows the declared profile* (both rows, asserting an exact count of entries whose message is `WarnMsgHistoryCap`) and *A nil logger still reports the unset cap*. Reuse the cases already there, and do not add a second test with the same shape (table-test skill). Mutation to watch red: invert the `HistoryCapSet` check. Verify: `go test -race -run 'TestWarnUnsafeConfig' ./...` in the `persistence` module.
- [ ] 1.4 Make the `WithHistoryCap` godoc (in `persistence` and in the store package, wherever it lives at apply) state the unbounded default as a promise, and cite `openspec/specs/instance-store/spec.md`. Verify: `scripts/check-doc-refs.sh` passes and `go doc` for `persistence.WithHistoryCap` shows the sentence.
- [ ] 1.5 Remove the `PENDING` comment from `specs/instance-store/spec.md` in this change. Verify: `openspec validate decide-history-cap-default --strict`.

## 2. Integration

- [ ] 2.1 Run the whole gate: the per-module build, test and lint loop from `CONTRIBUTING.md`, plus `scripts/check-doc-refs.sh`, `scripts/check-openspec.sh` and `openspec validate decide-history-cap-default --strict`. Verify: all exit 0.

## Workflow follow-up

- The PR body names `decide-history-cap-default`. Its last commit runs `openspec archive decide-history-cap-default`.
- After merge, comment on kartaladev/wrkflw#37 with the change name, and close it.

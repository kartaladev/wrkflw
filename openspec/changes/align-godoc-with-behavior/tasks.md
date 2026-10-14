# Tasks

This change has no spec deltas (`skip_specs: true`), so no task makes a scenario pass. Each godoc task names the existing test, and where one exists the baseline capability-spec scenario, that its rewritten comment must agree with. Every task is verified by reading the comment beside that evidence, plus `scripts/check-doc-refs.sh`, `scripts/lint.sh ./...` (run from the repo root) and the named test(s). All files are in the root module.

## 1. Retry policy MaxInterval (the one unpinned claim)

- [ ] 1.1 Write the characterization test first (design D2): a table test in `engine/step_retry_backoff_test.go`, `TestActionFailedRetryUnsetMaxIntervalIsCappedAtDefault`, following the `table-test` skill. It drives `engine.Step` with an `ActionFailed` on a service task, InitialInterval 2m and jitter at its default, and asserts the armed retry timer's delay. Rows: node policy with MaxInterval unset arms at 100s; the same policy via `StepOptions.OverrideRetryPolicy` with MaxInterval 0 arms at 100s; negative control: MaxInterval 5m arms at 2m. Expected green on first run; if any row is red, stop, drop task 1.2 and report the red test as a defect for a new change. Verify: `GOTOOLCHAIN=go1.26.9 go test -race -run TestActionFailedRetryUnsetMaxIntervalIsCappedAtDefault ./engine/...`.
- [ ] 1.2 Rewrite the MaxInterval godoc on `model.RetryPolicy` (`definition/model/retry.go`) and `action.RetrySpecs` (`action/policy.go`). A non-positive value is replaced with `DefaultRetryPolicy`'s 100s by `RetryPolicy.Normalize`, which every engine retry path applies, so it never means "no cap" there. Only a direct `Backoff` call on an un-normalized policy treats ≤0 as uncapped. Name the `retry-policy` capability spec and the 1.1 test. Verify: read both comments beside `Normalize`, `effectiveRetryPolicy` and the 1.1 test; `GOTOOLCHAIN=go1.26.9 go test -race ./definition/model/... ./action/... ./engine/...`; `scripts/check-doc-refs.sh`; `scripts/lint.sh ./...`.

## 2. Chaining idempotency godoc

- [ ] 2.1 Rewrite `chain.WithChainLinks` and the `Chainer.Handle` outcome list (`runtime/chain/chainer.go`). A link already recorded (`kernel.ErrChainLinkExists`) does not skip the start: `Handle` re-attempts it, and `kernel.ErrInstanceExists` from the starter is the exactly-once backstop. Cite the `instance-chaining` spec. Verify: read against the `ErrChainLinkExists` branch and `TestChainerHandleRetriesStartAfterTransientFailure`; `GOTOOLCHAIN=go1.26.9 go test -race ./runtime/chain/...`; `scripts/check-doc-refs.sh`; `scripts/lint.sh ./...`.

## 3. Message-start uniqueness godoc

- [ ] 3.1 Rewrite `runtime.ErrDuplicateMessageStart`, the `ErrDuplicateMessageStart` bullet of `runtime.RegisterDefinition`, and `checkMessageStartUnique`'s comment (`runtime/definition_registry.go`). The incoming names are compared only with the latest registered version of each other definition ID, a superseded version's name is free, re-registering the same ID never collides with itself, and a repeat across the definition's own start nodes still collides. Leave the `ErrDefinitionExists` bullet untouched (design, cross-change coupling). Verify: read against `latestPerID` and `TestRegisterDefinitionRejectsDuplicateMessageStart` (row "superseded version's message name does not block a new def reusing it"); `GOTOOLCHAIN=go1.26.9 go test -race -run TestRegisterDefinitionRejectsDuplicateMessageStart ./runtime/...`; `scripts/check-doc-refs.sh`; `scripts/lint.sh ./...`.

## 4. Dropped-cancel sentinel godoc

- [ ] 4.1 Rewrite the first paragraph of `engine.ErrCancelNotApplicable` (`engine/errors.go`): it is returned only when the in-flight walk resumes the instance (an admin partial rollback). A re-cancel during a terminal (cancel or error) walk returns nil, because that walk already ends the instance. Keep the error text unchanged. Cite the `instance-lifecycle` spec. Verify: read against `walkTerminates` in `engine/step_triggers.go` and `TestCancelOnAnInFlightWalkIsTruthful`; `GOTOOLCHAIN=go1.26.9 go test -race -run 'TestCancelOnAnInFlightWalkIsTruthful|TestSecondCancelMidCompensationWalkDoesNotDoubleCompensate' ./engine/...`; `scripts/check-doc-refs.sh`; `scripts/lint.sh ./...`.

## 5. Timer store godoc (retired AppliedStep path)

- [ ] 5.1 Rewrite `runtime.WithTimerStore` (`runtime/processdriver_options.go`). The driver persists each armed or cancelled timer through the store's `kernel.TimerWriter` in the same transaction as the state commit. A store that does not implement `TimerWriter` receives no writes. `ProcessDriver.RehydrateTimers` re-arms from `ListArmed`. Without the option, timers live only in the scheduler and are lost on restart. Verify: read against the commit path in `runtime/processdriver.go` and `kernel.AppliedStep`; `GOTOOLCHAIN=go1.26.9 go test -race -run 'Rehydrate|JobStore' ./runtime/...`; `scripts/check-doc-refs.sh`; `scripts/lint.sh ./...`.
- [ ] 5.2 Rewrite `kernel.MemTimerStore` and `kernel.WithTimers` (`runtime/kernel/timerstore.go`, `runtime/kernel/memstore.go`) per design D4. `MemInstanceStore` does not write timers. `WithTimers` is retained for compatibility and has no effect. A `MemTimerStore` receives writes when passed to `runtime.WithTimerStore`. Verify: `grep -n timers runtime/kernel/memstore.go` shows no read of the field; `GOTOOLCHAIN=go1.26.9 go test -race ./runtime/kernel/... ./runtime/...`; `scripts/check-doc-refs.sh`; `scripts/lint.sh ./...`.

## 6. Start-timer comments

- [ ] 6.1 Rewrite `startTimerID`'s comment and `ProcessDriver.RehydrateStartTimers`'s godoc (`runtime/timerops.go`). Only the latest registered version of each definition ID arms its timer-start events; a superseded version is not armed. The version stays in the timer ID so that a version bump gets its own scheduler entry. Per design Risks, claim nothing about a superseded entry left armed by an earlier call. Verify: read against `timerStartDefs`/`latestPerID` in `runtime/event_start.go` and `TestRehydrateStartTimersLatestVersionOnly`; `GOTOOLCHAIN=go1.26.9 go test -race -run 'TestRehydrateStartTimersLatestVersionOnly|TestEventStartEnumerationLatestVersionOnly' ./runtime/...`; `scripts/check-doc-refs.sh`; `scripts/lint.sh ./...`.

## 7. httpcall redirect precedence comment

- [ ] 7.1 Edit the scheme-before-host comment in `refuseUnsafeRedirect` (`action/httpcall/redirect.go`) per design D3. Keep the rationale. State that the precedence for a hop that both downgrades and changes host is not pinned by any test and is not a consumer promise, so callers must accept either `ErrDowngradeRedirect` or `ErrCrossHostRedirect` for it. Add no test. Verify: read against `TestDefaultRedirectPolicy_Scheme`, confirming no row combines a downgrade with a host change; `GOTOOLCHAIN=go1.26.9 go test -race ./action/httpcall/...`; `scripts/check-doc-refs.sh`; `scripts/lint.sh ./...`.

## 8. Integration check

- [ ] 8.1 Run the whole suite and every guard once over the combined edit. Verify: `mods="$(scripts/modules.sh)"; for pat in $(printf '%s\n' "${mods}"); do GOTOOLCHAIN=go1.26.9 go test -race "${pat}"; done`; `scripts/check-doc-refs.sh`; `scripts/check-openspec.sh`; lint per module as CONTRIBUTING.md describes.

## Workflow follow-up

- Open a PR whose body names `align-godoc-with-behavior`; its last commit runs `openspec archive align-godoc-with-behavior`.
- No GitHub intake issue is cited, so none is closed.

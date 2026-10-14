# Proposal

## Why

The baseline (`establish-baseline-specs`, design D7 group 3) found exported godoc and code comments that promise something the code does not do. In each case the tests and the baseline specs pin the real behavior, so the comment is the defect. A consumer reading godoc today would build on behavior that does not exist, for example a retry policy with no cap or a redelivered chain event that skips its start.

## What Changes

Each item was re-verified against the code on `chore/plan-baseline-follow-ups` (HEAD `44ba0879`) before it was listed. No behavior changes; comments only, plus one characterization test where a claim is not yet pinned.

- **`chain.WithChainLinks` and `chain.Chainer.Handle` godoc (`runtime/chain/chainer.go`).** Both say an already-recorded link skips the start (`ErrChainLinkExists -> nil (already chained)`). `Handle` falls through and re-attempts the start; `ErrInstanceExists` is the real backstop. Rewrite both to say so. Already pinned by `TestChainerHandleRetriesStartAfterTransientFailure` and the `instance-chaining` spec.
- **`runtime.ErrDuplicateMessageStart`, `runtime.RegisterDefinition` godoc and `checkMessageStartUnique`'s comment (`runtime/definition_registry.go`).** They say any already-registered message-start collides. Only the latest registered version of each other definition ID is compared, and a re-registration of the same ID is never a collision. Already pinned by the "superseded version's message name does not block a new def reusing it" row of `TestRegisterDefinitionRejectsDuplicateMessageStart`.
- **`model.RetryPolicy.MaxInterval` (`definition/model/retry.go`) and `action.RetrySpecs.MaxInterval` (`action/policy.go`) godoc.** Both say a zero (or negative) value disables the cap. Every engine path runs the policy through `RetryPolicy.Normalize`, which replaces a non-positive MaxInterval with 100s. Only a direct `Backoff` call on an un-normalized value is uncapped. Rewrite both; a characterization test pins that an action-tier `RetrySpecs{MaxInterval: 0}` is capped at 100s by the effective policy.
- **`engine.ErrCancelNotApplicable` godoc (`engine/errors.go`).** It says the cancel is dropped while "a terminal or admin partial-rollback compensation walk" is in flight. Only the partial-rollback walk takes the sentinel; a re-cancel during a terminal walk returns nil. Already pinned by `TestCancelOnAnInFlightWalkIsTruthful` and the `instance-lifecycle` spec.
- **`runtime.WithTimerStore` godoc (`runtime/processdriver_options.go`).** It says armed and cancelled timers are recorded into the `AppliedStep`. `AppliedStep` carries no timers; the writes go through the configured store's `kernel.TimerWriter` inside the state-commit transaction, and a store without `TimerWriter` gets no writes. Two adjacent comments describe the same retired path and are fixed with it: `kernel.MemTimerStore` ("MemInstanceStore records arms/cancels into it") and `kernel.WithTimers` ("records armed-timer side-effects into mts atomically with each Create/Commit"), whose value the in-memory store stores and never reads.
- **Start-timer comments (`runtime/timerops.go`).** `startTimerID`'s comment says "Each registered VERSION arms independently", and `ProcessDriver.RehydrateStartTimers` says it arms "every registered definition's timer-start event". Only the latest version of each definition ID is armed. Verified: the enumeration goes through `latestPerID`, and `TestRehydrateStartTimersLatestVersionOnly` asserts one instance, from version 2. Rewrite both, keeping why the version stays in the timer ID.
- **httpcall downgrade-over-cross-host precedence (`action/httpcall/redirect.go`).** The internal comment says a hop that both downgrades and changes host is reported as `ErrDowngradeRedirect`. No test has such a row, and no exported godoc or spec promises it. **Decision: note it untested.** The comment keeps its rationale but says the precedence is unpinned and not a consumer promise. Pinning it would turn an internal ordering into a de facto contract without a spec.

Decisions this change cannot be planned without, resolved here:
- The spec follows the tests in every case (D7 group 3), so no requirement changes and `skip_specs: true` is set.
- httpcall precedence: note it untested rather than drop the rationale or pin it (above).
- `kernel.WithTimers` is documented as having no effect, not removed; removing an exported option is out of scope.

**Out of scope**
- The in-memory registry refusing an identical re-registration, against #111's idempotent-if-identical decision. It needs a decision and is owned by the change `make-definition-registration-idempotent`.
- Removing `kernel.WithTimers` or the `timers` field it sets.
- Whether `RehydrateStartTimers`, called again after a newer version is registered, leaves the older version's scheduler entry armed (the version is part of the timer ID). Unmeasured hypothesis, recorded in design.md for a later change.
- Promoting "a superseded version's message-start name does not collide" into the `definition-store` spec, which currently says it "is not specified". That changes a spec and belongs to a change that does not skip specs.
- kartaladev/wrkflw#225 (a test comment contradicting `validate.go`) is a similar comment-vs-code mismatch but is a pending-migration issue with its own owner.

No GitHub issue is absorbed; these findings come from the baseline, not from intake.

**BREAKING:** none. **Persisted formats:** not affected. **Default security posture:** not affected. **Consumer go.mod:** not affected.

**Coordination**
- Size: S.
- Depends on: `establish-baseline-specs`.
- Blocks: nothing.
- Readiness: agent.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. Docs only; behavior stays as the baseline specs and tests pin it (`skip_specs: true`).

## Impact

- **Code (comments only):** `runtime/chain/chainer.go`, `runtime/definition_registry.go`, `definition/model/retry.go`, `action/policy.go`, `engine/errors.go`, `runtime/processdriver_options.go`, `runtime/kernel/timerstore.go`, `runtime/kernel/memstore.go`, `runtime/timerops.go`, `action/httpcall/redirect.go`.
- **Tests:** one characterization test for the normalized MaxInterval cap on the action tier.
- **Not affected:** exported identifiers, runtime behavior, persisted formats, the default security posture, any consumer's `go.mod`.

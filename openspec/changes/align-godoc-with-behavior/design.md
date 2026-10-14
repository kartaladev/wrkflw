# Design

## Context

See proposal.md for why and for the item list. Every item was re-measured against the code at `44ba0879`:

| Item | What the comment says | What the code does | Evidence that pins the code |
|---|---|---|---|
| `chain.WithChainLinks`, `Chainer.Handle` | a recorded link skips the start | on `ErrChainLinkExists`, `Handle` falls through and re-attempts the start; `ErrInstanceExists` makes a real duplicate a no-op | `runtime/chain` `TestChainerHandleRetriesStartAfterTransientFailure`; `instance-chaining` scenario "A redelivery after a failed start re-attempts the start" |
| `ErrDuplicateMessageStart`, `RegisterDefinition`, `checkMessageStartUnique` | any registered message-start collides | compares against `latestPerID(...)` of the registry, skipping the incoming definition's own ID | `runtime` `TestRegisterDefinitionRejectsDuplicateMessageStart`, row "superseded version's message name does not block a new def reusing it" |
| `model.RetryPolicy.MaxInterval`, `action.RetrySpecs.MaxInterval` | zero (or ≤0) disables the cap | `effectiveRetryPolicy` returns `Normalize()`d policies on all three tiers, and the compensation path normalizes too; `Normalize` sets a non-positive MaxInterval to 100s. `Backoff` alone honours ≤0 as no cap | `retry-policy` spec, requirement on `Normalize`; `TestBaselineRetryPolicyNormalize`; the comment in `runtime/processdriver_compensation_retry_test.go` that measured a 2m interval arming at 100s. No test pins the cap on the forward (token) retry path |
| `engine.ErrCancelNotApplicable` | dropped during "a terminal or admin partial-rollback" walk | `walkTerminates` gates it: only a walk that resumes the instance (admin partial rollback) returns the sentinel; a terminal walk returns nil | `engine` `TestCancelOnAnInFlightWalkIsTruthful`, rows "DROPPED against an admin partial rollback" and "REDUNDANT re-cancel of a terminal walk still returns nil" |
| `runtime.WithTimerStore` | arms/cancels are recorded into the `AppliedStep` | `kernel.AppliedStep` has no timer field; `ProcessDriver` saves and deletes timers through `jobStore` inside the commit function, using the store's `kernel.TimerWriter` resolved at construction; a store without it gets no writes | `runtime/kernel/ports.go` `AppliedStep`; `runtime/processdriver.go` commit path; `runtime/kernel/timerstore.go` `TimerWriter` godoc |
| `kernel.MemTimerStore`, `kernel.WithTimers` | `MemInstanceStore` records arms/cancels into the timer store | `MemInstanceStore.timers` is assigned in the constructor and read nowhere (`grep -n timers runtime/kernel/memstore.go`) | read of `memstore.go`; tests that pass `WithTimers(mts)` work because the driver writes `mts` through `WithTimerStore` |
| `startTimerID`, `RehydrateStartTimers` | "Each registered VERSION arms independently"; arms "every registered definition's timer-start event" | `timerStartDefs` iterates `latestPerID(defs)`, so only the latest version per definition ID is armed | `runtime` `TestRehydrateStartTimersLatestVersionOnly` (one instance, `DefVersion == 2`) |
| `refuseUnsafeRedirect` comment | a downgrade that also changes host is reported as a downgrade | the scheme check runs first, so it does; but no row of `TestDefaultRedirectPolicy_Scheme` or `TestDefaultRedirectPolicy*` combines a downgrade with a host change | read of `action/httpcall/redirect_internal_test.go` |

## Goals / Non-Goals

**Goals:**
- Every listed comment states what the code does, and names the test or capability spec that pins it, so the next drift is caught in review.
- Add one characterization test where the corrected claim is not yet pinned on the path consumers actually use: the forward retry path capping an unset MaxInterval at 100s.

**Non-Goals:**
- No change to any behavior, error text, exported identifier or spec. If verifying a comment shows the code is wrong, that becomes a new change with a failing test, not an edit here.

## Decisions

### D1. The comment moves to the code, never the reverse
The baseline specs and tests already pin each behavior (D7 group 3 of `establish-baseline-specs`). *Rejected:* changing code to match the comment, e.g. making a zero MaxInterval mean uncapped. That would contradict the `retry-policy` spec's `Normalize` requirement, change retry timing for every consumer with an unset MaxInterval, and needs its own decision.

### D2. Only an unpinned claim gets a new test
Six of the seven items are already pinned by a named test (Context table). Only the MaxInterval claim lacks a test on the path consumers use: `Normalize` is pinned in isolation, and the 100s cap is only measured on the compensation path. The new test drives `engine.Step` with an `ActionFailed` trigger and asserts the armed retry delay. Rows: a node policy with MaxInterval unset and InitialInterval 2m arms at 100s; the same through `StepOptions.OverrideRetryPolicy` (the action tier `action.RetrySpecs` feeds) arms at 100s; negative control: MaxInterval 5m arms at 2m. It is a characterization test, expected green on first run. A red run means a comment would have described something false, so stop and open a change. *Rejected:* testing at the runtime layer with `action.WithRetrySpecs`. The conversion is a plain field copy, and the engine is where normalization happens.

### D3. httpcall precedence: note it untested
The comment's ordering is real (the scheme check runs first), but nothing pins it and no consumer is promised it: `ErrDowngradeRedirect`'s exported godoc and the `outbound-http-calls` spec say nothing about a hop that is both. *Rejected:* (a) dropping the claim, which throws away a correct rationale for why the scheme check comes first; (b) adding a test row, which would turn an internal ordering into a de facto contract with no spec behind it. The comment keeps its rationale and says the precedence is not pinned by a test and not a promise, so a caller must accept either sentinel for such a hop.

### D4. `kernel.WithTimers` is documented, not removed
The option is exported and used by about ten tests. Its godoc will say it is retained for compatibility and has no effect on the in-memory store, and that durable timer writes reach a `MemTimerStore` only through `runtime.WithTimerStore`. *Rejected:* removing the option or the field, which is an API change outside a docs-only change.

### D5. Cite capability specs, never change paths
Comments may cite `openspec/specs/<cap>/spec.md` (e.g. `instance-chaining`, `instance-lifecycle`, `retry-policy`) and test names. `scripts/check-doc-refs.sh` rejects any `openspec/changes/...` citation in Go source.

**Cross-change coupling:** none. No shared migration, public type or landing order. The excluded in-memory re-registration change, `make-definition-registration-idempotent`, may also touch `runtime/definition_registry.go` godoc (`RegisterDefinition`'s `ErrDefinitionExists` bullet). Whichever lands second rebases that bullet; this change does not edit it.

## Risks / Trade-offs

- [A rewritten comment is itself wrong] → each godoc task names the test or spec it was checked against, and review reads the comment beside that test.
- [The MaxInterval characterization test goes red] → treat it as proof of a defect (CLAUDE.md, *Defects need proof*): drop the godoc edit for that item and open a change seeded by the red test.
- [`RehydrateStartTimers` called again after a version bump may leave the superseded version's scheduler entry armed, because the version is part of the timer ID] → unmeasured hypothesis. The rewritten comment must not claim either outcome for a second call; a later change measures it.

## Migration Plan

None. Comments and one test only; rollback is a revert.

## Open Questions

- Whether "a superseded version's message-start name does not collide" should be promoted into the `definition-store` spec, which says it "is not specified" today. Deferrable: it changes a spec, not this change's comments.
- Whether `RehydrateStartTimers` leaves a superseded version's start timer armed after a re-run (Risks). Deferrable: the comment fix avoids claiming either outcome.

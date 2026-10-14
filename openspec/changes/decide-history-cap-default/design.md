## Context

Measured on `main` at `eebe55ba`. The motivation is in proposal.md, *Why*.

`persistence.WithHistoryCap(n)` bounds the stored inline `History` to the open visits plus the n most recent closed visits. A value of n ≤ 0, or no option at all, keeps everything (`persistence/options.go:57-61`). `persistence.WarnUnsafeConfig` logs `WarnMsgHistoryCap` when the consumer's `DeploymentProfile.HistoryCapSet` is false (`persistence/unsafe_config.go:40-41`). The baseline `instance-store` spec covers only n > 0.

Re-measured on this branch (no code diff from `main`) with `GOTOOLCHAIN=go1.26.9 go test ./internal/persistence/store/ -run '^$' -bench 'BenchmarkInstanceLifetime|BenchmarkSnapshotMarshal' -benchmem -count 1`. The figures are lower bounds, because the benchmarks exclude the database round trip.

| arm | 400 transitions | 800 transitions | ×(800/400) |
| --- | --- | --- | --- |
| lifetime `cap=off` | 131.1 ms, 166.9 MB | 517.4 ms, 672.6 MB | 3.95 |
| lifetime `cap=100` | 93.1 ms, 137.9 MB | 328.4 ms, 521.2 MB | 3.53 |
| lifetime `marshal=off` | 11.9 ms, 62.1 MB | 42.4 ms, 236.0 MB | 3.55 |
| one snapshot marshal, `cap=off` | 582 µs, 481 KB | 1137 µs, 971 KB | 1.95 |
| one snapshot marshal, `cap=100` | 362 µs, 323 KB | 680 µs, 615 KB | 1.88 |

At 800 transitions `cap=100` saves 36.5% of lifetime time and 22.5% of lifetime bytes. #96 recorded 34.9%. Every lifetime arm, `marshal=off` included, still roughly quadruples per doubling.

## Goals / Non-Goals

**Goals:**
- A specified history-cap default (`instance-store` delta), promised in the `WithHistoryCap` godoc.
- A promise that the journal is complete whatever the cap.

**Non-Goals:**
- Removing the quadratic growth.
- The README persistence section and the stability statement (`document-stability-policy`).

## Decisions

The default itself is undecided (Open Questions, Q1). The decisions below hold under either option.

### D1. The journal is promised complete under any cap
The cap bounds the inline snapshot only. `Entries` returns one trigger per successful `Create` and `Commit` whatever the cap, so a capped instance stays replayable and auditable. This holds today; the requirement makes it a promise.
*Rejected:* leaving it unspecified. A future cap on the journal would silently remove the audit trail a consumer relies on once inline history is truncated.

### D2. Characterization tests are watched red under a mutation
The three ADDED requirements describe current behavior, so a passing test proves nothing on its own. Each test is watched failing under a named local mutation, then the mutation is reverted (tasks).

## Risks / Trade-offs

- [Recommended Q1 option leaves a resource-growth hazard on by default] → Requirement *An unset history cap is reported at start-up*. Only consumers who call `WarnUnsafeConfig` see the log line, so `document-stability-policy` states the growth in the README too.
- [The spec's three ADDED requirements are characterization tests of existing behavior] → D2.
- Concurrency does not apply to the ADDED requirements. The cap is applied inside a commit that the existing *A commit against a stale version is refused with ErrConcurrentUpdate* requirement already serializes.

## Cross-change coupling

| Change | Coupling | Owner / order |
| --- | --- | --- |
| `document-stability-policy` | Its README persistence section states the default, the growth shape and the benchmark this change settles. | This change lands first (Blocks). |
| `split-persistence-modules` | Moves `internal/persistence/store`, whose `WithHistoryCap` godoc task 1.4 edits, and may move the benchmark. | Either order. The second lander re-derives the store path. |
| `cut-v0-release` | The default must be settled before the tag. | This change lands first (Blocks). |

## Migration Plan

Nothing to migrate under the recommended Q1 option. If a default cap is chosen, existing instances start truncating their stored history on their next commit. The journal stays complete (requirement *A history cap never truncates the instance's journal*). A consumer who wants the old behavior passes the explicit "unbounded" value that option introduces.

## Open Questions

This needs the maintainer before apply (Readiness: human).

**Q1. History-cap default (#37).** It changes the spec.
- (a) **Keep unbounded (recommended).** Promise it, and keep the start-up warning. Evidence (Context): the cap is a 36.5% constant-factor saving on a curve that stays quadratic in every arm, because `RootCompensations` and the per-step clone grow regardless. A behavior change bought for a constant factor is the trade #96's comment recommends against.
- (b) **Default cap of 100.** A **BREAKING** behavior change: inline history is truncated by default. It needs an explicit "unbounded" value, because n ≤ 0 means unbounded today. The spec's first and third requirements are rewritten, and the existing cap requirement is MODIFIED.
- (c) Default cap with no opt-out. Rejected: it removes a consumer's ability to keep full history inline.

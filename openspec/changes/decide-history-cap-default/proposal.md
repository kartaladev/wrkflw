## Why

The history-cap default is the one instance-store default the baseline left unspecified. A durable instance's stored inline history grows with every step unless the consumer opts into a cap, and the first tag (`cut-v0-release`) should not ship with that default undecided and unpromised. This change was split out of `document-stability-policy`, which keeps the documentation half and states whatever default this change settles.

## What Changes

Every premise below was re-measured on `main` (`eebe55ba`) before it was listed.

- **kartaladev/wrkflw#37 (folded in, the remaining half only; the decision is open).** The benchmark half shipped in PR #96 (`2995ef7f`). `internal/persistence/store/history_cap_bench_test.go` and its guard test exist at `main`. What remains is the decision on whether to turn on a default history cap. The baseline `instance-store` spec covers only a cap of n > 0 and deliberately leaves the default unspecified. This change specifies the default. **Recommended option (pending the maintainer): keep the unbounded default**, signal it, and promise it. Re-measured on this branch (no code diff from `main`), `go1.26.9`, one run: at 800 transitions `cap=100` cuts lifetime cost by 36.5% (328.4 ms against 517.4 ms; #96 recorded 34.9%), and it cuts per-step marshal by 40% (680 µs against 1137 µs). From 400 to 800 transitions, `cap=off` grows ×3.95, `cap=100` grows ×3.53 and `marshal=off` grows ×3.55. A cap therefore changes the constant, not the quadratic shape.
- It also promises, under either option, that a history cap never truncates the instance's journal, and that `persistence.WarnUnsafeConfig` reports an unset cap.

**Stale premises found on re-measurement**
- #37's body says the history cap is "off by default" with nothing more. Partly stale: `persistence.WarnUnsafeConfig` already logs `WarnMsgHistoryCap` when a consumer's `DeploymentProfile` says the cap is unset. The default is unbounded but not silent, for consumers who call it.

**Decisions this change cannot be finished without (unresolved, Readiness: human)**
1. #37: keep the unbounded history default (recommended), or turn on a default cap (design, Open Questions, Q1).

**Out of scope**
- The README persistence section that states the default, the growth shape and the benchmark name, and the stability statement. Owned by `document-stability-policy` (kartaladev/wrkflw#158), which depends on this change.
- The snapshot redesign that would remove the quadratic growth (whole-document rewrite, per-step `cloneState`). #37 puts it out of scope.
- Moving `internal/persistence/store` into the `persistence` module. Owned by `split-persistence-modules`.

**BREAKING:** none under the recommended option. A default cap (the other option) would be **BREAKING** in behavior: every instance starts truncating its stored inline history. **Persisted formats:** the encoding does not change under either option. A default cap changes what a snapshot retains. **Default security posture:** not affected. The unbounded default is a resource-growth hazard, reported at start-up by `WarnUnsafeConfig` and stated in the README by `document-stability-policy`. **Consumer go.mod:** not affected.

**Coordination**
- Size: S
- Depends on: nothing
- Blocks: document-stability-policy, cut-v0-release
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `instance-store`: ADDED. With no history cap configured, the store keeps the full inline history (the recommended option for decision 1, pending). ADDED. A history cap never truncates the journal (independent of decision 1). ADDED. An unset history cap is reported at start-up. If a default cap is chosen instead, the first and third requirements are rewritten to state that default, and the existing cap requirement is MODIFIED to say what "unset" means.

## Impact

- **Tests:** characterization tests for the ADDED `instance-store` requirements across Postgres, MySQL and SQLite, and for `WarnUnsafeConfig`.
- **Docs:** `persistence` and `internal/persistence/store` godoc on `WithHistoryCap`, which cites the spec.
- **Code:** none under the recommended option. A default cap would change the store's option default and `WarnUnsafeConfig`.
- **Coupling:** `split-persistence-modules` moves `internal/persistence/store`. Either order works; the second lander re-derives the store path. `document-stability-policy` writes the README persistence section that states the default this change settles.

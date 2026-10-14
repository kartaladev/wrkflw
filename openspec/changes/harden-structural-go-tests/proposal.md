# Proposal

## Why

Two structural Go tests guard promises that no single behavior test covers: that every node kind is classified in `doc.go`'s BPMN table and registered, and that no exported signature names an internal type. One kind-walk claims to cover appended kinds and does not, and the internal-leak scanner silently skips methods promoted through an embedded unexported type. Each is a green run that proves nothing, which is the defect class these tests exist to prevent.

## What Changes

Every premise below was re-measured on `main` at `4763d36e`. Probes ran in a scratch `git archive` copy, never in the working tree.

- **kartaladev/wrkflw#189 (absorbed; partly stale).** Items 1 and 3 hold. In a scratch copy, dropping `parallelGateway` from the *Matches BPMN* list fired the named `UNCLASSIFIED` assertion (`doc_bpmn_test.go:174`). Moving it out of the section fired the same assertion. Item 2 is **false**: the walk is bounded by a named constant, `k <= model.KindCompensationThrowEvent` (`doc_bpmn_test.go:165`), not derived. An appended `KindProbeAppended` left `TestDocGoClassifiesEveryKind` green. The same bound is in `definition/kinds/kinds_test.go:19` (`TestAllKindsRegistered`), whose comment claims the opposite. `definition/kinds/nodetype_guard_test.go:83-91` already documents this exact trap and walks by registration instead. Both walks are folded in. **Stale:** item 4 (the gateway rows of `doc.go`) was settled when #120 closed, fixed by `b7ebc191`. The process half ("merged with no reviewer") refers to a session brief that no longer exists.
- **kartaladev/wrkflw#180 (absorbed; latent).** Holds. The limit is documented at `runtime/monitor/internal_leak_test.go:528-537`, and `(neutralLockerBridge).Lock` (`scheduler/scheduler.go:508`) is still the only candidate, embedded nowhere. The issue says this "needs go/types". That is a hypothesis this change tests: an unexported type can be embedded only inside its own package, so a per-package `go/ast` resolution may be enough (design, Q1). The issue calls the limit fail-closed. It actually under-reports, so the guard's silence is not evidence, which is fail-open.
- No library behavior changes. Test files only, so this sets `skip_specs`.
- **BREAKING:** none. **Persisted formats:** not affected. **Default security posture:** not affected (test hygiene only). **Consumer go.mod:** not affected.

**Decisions this change cannot be finished without.** The options and recommendation are in design.md, Open Questions. Tasks follow the recommendation and are marked *pending Q1*.
- **Q1 (#180).** Close the promoted-method hole now with a per-package `go/ast` resolution, or keep it documented with a tripwire.

This change was split out of `harden-ci-guard-wiring` along file lines; it owns the structural Go tests named below.

**Out of scope**
- The truth of `doc.go`'s BPMN rows is owned by the `token-routing-and-joins` capability (#120, fixed).
- Extending the internal-leak guard to new modules is owned by `split-persistence-modules` (its task 8.2).
- The `casbinauthz` entry in `knownOpenInternalLeaks` is owned by `remove-casbin` / `replace-casbin-with-composed-deciders` (#179).
- CI wiring and `check-lint-modules.sh` (#233, #232) are owned by `harden-ci-guard-wiring`; `scripts/lint.sh` (#188, #177, #178) by `harden-lint-script`; instruction files (#172, #173) by `check-instruction-file-refs`.

**Coordination**
- Size: M
- Depends on: nothing
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. Only test hygiene changes, so `skip_specs: true` is set.

## Impact

- **Tests:** `doc_bpmn_test.go`, `definition/kinds/kinds_test.go`, `runtime/monitor/internal_leak_test.go`, and a fixture package for Q1.
- **Shared files:** `runtime/monitor/internal_leak_test.go` with `split-persistence-modules` and `remove-casbin`. There is no ordering dependency; whichever lands second rebases.
- When archived, comment on and close kartaladev/wrkflw#189 and #180.

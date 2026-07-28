# 153. Collapse duplicated engine/runtime helpers onto single definitions

- Status: Accepted
- Date: 2026-07-28

## Context

A structured quality review of `engine/` (~7.5k LOC) and `runtime/` (~7.4k LOC,
including subpackages) across four dimensions — reuse, simplification,
efficiency, and altitude — found that neither package carries architectural
debt, but that a specific failure mode recurs: **helpers get extracted for the
shape but not for the last mile**, and **knowledge escapes the type that owns
it**.

Concretely, before this change:

- `removeArmsWhere` and friends returned `[]string` timer ids, and 29 call sites
  each hand-wrote the same three-line loop converting them to `CancelTimer`
  commands.
- Five terminal paths each repeated the same three-call arm/timer sweep; the
  invariant was documented in a prose caller list on `cancelAllArmsAndBoundaries`
  that had already gone stale (it named five callers; there were six).
- The `[]authz.Actor` deep copy existed three times in three packages, in the two
  packages that do *not* own `authz.Actor`.
- `runtime` re-derived `engine.Status.IsTerminal()` and `.String()` as private
  helpers, while its sibling `runtime/view` already delegated to the engine and
  documented it as canonical.
- `copyVars`, `copyVarsForOutcome`, and `mergeVars` reimplemented `maps.Clone` /
  `maps.Copy` — one of them 380 lines below a call to `maps.Clone` in the same
  file.
- `cloneState` deep-copied `[]CompensationRecord` three separate times.
- `trigger_validate.go` encoded the same 11 trigger→identity-field facts twice
  (a map for the error message, a type switch for the value), with only the map
  pinned by `TestValidateTriggerKindsAreExhaustive`.
- `perform` was a 313-line switch carrying a `//nolint:cyclop`, with two complete
  sub-algorithms inlined into switch arms.

None of these is a defect. Each is a place where a future field addition or a
new node/trigger/arm kind must be applied at N sites, correctly, by discipline
rather than by the compiler.

## Decision

We will collapse each duplicated definition onto a single one, preserving
behaviour. Specifically:

- Add `appendCancelTimers` and `(*InstanceState).cancelAllScheduledWork` as the
  single `[]string`→`[]Command` and terminal-sweep definitions.
- Export `authz.CloneActors` beside `authz.Actor.Clone`, and delete the three
  private copies. This is **new permanent public API**, justified because
  `authz` owns the type and the slice-level deep copy crosses the same isolation
  boundary that `Actor.Clone` already documents.
- Delete `runtime`'s `isTerminal` / `statusName` in favour of the exported
  `engine.Status` methods.
- Reduce `copyVars`/`mergeVars` to `maps.Clone`/`maps.Copy` (keeping `copyVars`'
  domain name for its ~25 call sites) and delete `copyVarsForOutcome`.
- Add `cloneCompensationRecords`, `cancelTimersWhere`, `compensationInvoke`,
  `startCompensationWalk`, `fireJoin`/`joinedAt`, `messageWaitersOf` /
  `signalNamesOf`, `distinctDefinitions`, and `startEvents`.
- Make `validatedTriggerKinds` a single table of `{field, read}` rows, and
  extend `TestValidateTriggerKindsAreExhaustive` to invoke every row's `read`.
- Split `perform` into a 47-line dispatcher plus eight methods; drop the
  `//nolint:cyclop`.
- Modernize surviving `sort.Slice`/`sort.Strings` to `slices`/`cmp`, matching the
  form already used elsewhere in the same packages.

We accept **two deliberate behaviour deltas**, both of which tighten an existing
contract rather than change intent:

1. `statusName`'s out-of-range label changes from `"running"` to `"unknown"`.
   Unreachable today (`StatusRunning` is 0 and explicitly cased); it removes the
   hazard that a future sixth `Status` would be silently mislabelled `"running"`
   in every metric and span attribute. `runtime/view.StatusString` already ships
   `"unknown"`.
2. `cloneState` now allocates a fresh slice for a non-nil **zero-length**
   `Compensations`, `Tasks`, `Scopes`, and `Incidents`. Previously those hit
   neither the `== nil` nor the `len > 0` arm and kept the struct copy's aliased
   header — the exact append-aliasing hazard `cloneState` exists to prevent.

We explicitly **decline** several findings, recording them so they are not
re-litigated as oversights:

- Changing the six arm/timer removers to return `[]Command` is the better
  altitude, but ten existing tests reference them; the `appendCancelTimers`
  helper removes the same duplication with no test churn.
- Deleting the nine arm-lookup forwarder methods would trade domain vocabulary
  at 12 call sites for the generic seam; a wash, not an improvement.
- Non-allocating `Outgoing`/`Incoming` variants, hoisting `stepCtx` out of the
  `drive` loop, and hoisting `MemInstanceStore`'s clone out of its lock are all
  **unbenchmarked** performance trades. The last was implemented and then
  reverted after review showed it deep-clones on `Create`'s normal duplicate-
  message path. Optimization requires measurement first.
- `armedTimerRecurring`'s unfiltered full-table read per timer fire, and the
  O(global waiters) per-step waiter reconciliation, are real and valuable but
  require a new store capability and new index invariants respectively. Each
  needs its own ADR.

## Consequences

- Adding a node kind, trigger variant, arm family, or `CompensationRecord` field
  now requires editing one definition instead of 3–29 sites, and a mis-paired
  trigger row fails a test rather than panicking inside `Step`.
- `authz.CloneActors` is public API and therefore permanent; it is covered by a
  testable Example pinning its nil-vs-empty contract.
- Net −42 lines of production code. `engine` shrank by 102 lines; `runtime` grew
  by 45, trading one 313-line function for eight functions of ≤67 lines.
- The two behaviour deltas are unobservable for any value the library currently
  constructs, but they are deltas, and a consumer asserting on the literal metric
  label `"running"` for a corrupt `Status` would see `"unknown"`.
- Coverage of the touched packages held or improved (`runtime/kernel` 85.1% →
  87.1%); the repo-wide figure is unchanged within measurement noise.
- The declined items above remain open. In particular a **separate, pre-existing
  bug** was found during review and deliberately left unfixed here because
  correcting it changes behaviour: `SignalWaiters()` omits signal boundary arms
  and event-based-gateway signal arms, so a signal boundary never fires from a
  broadcast — the mirror of the ADR-0123 message-side gap. It needs its own ADR
  and delivery.

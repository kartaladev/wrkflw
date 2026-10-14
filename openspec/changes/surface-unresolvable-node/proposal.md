# Proposal

## Why

A token whose node is not declared by the definition driving it can never move again, yet the engine parks it as `TokenWaiting` with one WARN line and no incident, so the instance sits `StatusRunning` forever and no incident view shows it. Next to that site, the parallel-join concession that keeps upgraded instances from deadlocking states its bound in two comments that disagree. No test checks that bound, and the join-resettle regression file only tests the direction in which a join fires.

## What Changes

Measured on main (`9c6dd2c1`) before listing. Every issue figure below was re-derived. Where the code changed after the issue was filed, the drift is noted.

- **Absorbs kartaladev/wrkflw#79 (folded in).** `drive` still meets a token whose node is absent from the effective definition with `slog.WarnContext(ctx, "token routed to a missing node", …)`. It then sets `tok.State = TokenWaiting` (`engine/step.go:336-345`) and records no incident. Pinned today by `TestDrive_MissingNodePark_LogsWarn` (`engine/observability_noop_test.go:364`). Under the park-site policy on `raiseDefinitionDefect` (`engine/step_nodes.go:845-875`) this is *stuck*, so it owes an incident. The maintainer note on #79 settles the direction. It is a **distinct incident kind** (option 1), following the `ErrOutcomeRequired` / `ErrInvalidOutcome` split (`engine/errors.go:190-204`). Widening `IncidentDefinitionDefect` (option 2) is ruled out, and so is leaving the WARN (option 4), which the policy forbids. **BREAKING:** the token moves to `TokenIncident` instead of `TokenWaiting`, and a new exported `engine.IncidentKind` constant is appended. Its name is undecided (Q1).
- The new kind is not resolvable through `ResolveIncident`. It is refused with `ErrIncidentNotResolvable` and kept, by the existing `IncidentAction` whitelist. `CancelRequested` still retires the instance. `causeOfDeathIncident` (`runtime/terminal_cause.go`) admits only `IncidentAction`, so it ignores the new kind with no edit. `IncidentCount` on the instance listing counts it.
- **Absorbs kartaladev/wrkflw#216 (folded in, premise partly stale).** The issue cites `engine/state.go:162-167` at #203's head. On main the paragraph is at `engine/state.go:162-167` (`Token.ArrivalFlow`), and its twin is at `engine/step_gateways.go:154-160` (`matchJoinTokensToFlows`). Re-measured, the per-token bound ("each such token satisfies at most one otherwise-unsatisfied flow") is implemented by `assigned[t.ID]`. The issue proposes a test of that direction, with one relocated token and two unsatisfied flows. The maintainer comments rule that it is "not in doubt", so it becomes a pinning scenario and is not counted as the measurement. The direction that matters is the aggregate: *N* empty-provenance tokens satisfying *N* flows. By reading the code, nothing caps it. That stays a **hypothesis** until task 1.1 measures it through `engine.Step` with a trigger that actually reaches `drive`. Whether the aggregate width is acceptable is undecided (Q3). Both comments are rewritten to state the aggregate explicitly and to agree with each other.
- The `ArrivalFlow` comment says that making the join concession's residual visible "is #79's subject". So this change also decides whether a join that fires by spending an empty-provenance token logs it (Q4).
- **Absorbs kartaladev/wrkflw#215 (folded in).** On main, `engine/step_join_resettles_test.go` is 231 lines with exactly two rows (`:134`, `:167`), and every assertion is positive (fired, completed). This adds negative rows, in which a settle with an unsatisfied join must not fire it, and keeps an at-limit accept beside each. The re-settle behavior from #203 is not in any capability spec, so this change adds it to `token-routing-and-joins`.
- **Persisted format:** the new kind enters the instance snapshot as the next free `IncidentKind` ordinal (4), appended after `IncidentDefinitionDefect`. A build older than this change decodes such a row as an unnamed kind. It refuses to resolve it and logs `IncidentKind(unknown)`. A token that an older build parked `TokenWaiting` on a missing node is not re-examined after upgrade (Q5). No column or migration changes. No change to the default security posture or a consumer's go.mod.

Decisions this change cannot be fully specified without (design, Open Questions). Each spec row that depends on one is marked `pending Qn` and states the recommended option:

- Q1: the new kind's name.
- Q2: whether the missing-node site always raises the new kind, or splits by cause.
- Q3: whether the aggregate join width is accepted as documented or capped.
- Q4: whether an empty-provenance join fire is logged.
- Q5: whether pre-existing silently parked tokens are surfaced.

Out of scope:
- drive's other park, the branch for a node kind the engine does not route (`KindBoundaryEvent`, `KindUnspecified`, `engine/step.go:374-395`). The node exists there, so it is a different blame case. It keeps its WARN. No issue owns it yet. The policy comment's "until a kind exists" is narrowed to say so.
- Encoding `IncidentKind` by name rather than by ordinal. That belongs to `encode-persisted-enums-by-name` (kartaladev/wrkflw#44), which must include this change's new kind when it lands.
- The wrong-target boundary fire, kartaladev/wrkflw#212 (closed). It is now refused with an error (`token-routing-and-joins`, *A boundary whose target names no node fails the step*). The sentinel export belongs to `export-refusal-sentinels`.
- Vacuous refusal-path assertions, including kartaladev/wrkflw#227, belong to `strengthen-unfailable-assertions`. This change's new rows are written to be discriminating from the start, so the two touch no common file.

**Coordination**
- Size: M
- Depends on: nothing
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities
None.

### Modified Capabilities
- `operator-incidents`: a token on a node the definition does not declare raises a new, non-resolvable incident kind. The resolve-refusal and cancel requirements cover that kind.
- `token-routing-and-joins`: the parallel join states how tokens that crossed no flow are counted, per token and in aggregate. Join readiness is re-tested when the token set settles, with negative rows.

## Impact

- `engine/state.go`: new `IncidentKind` constant appended, plus its `String()` case. The `Token.ArrivalFlow` and `Incident` comments are rewritten.
- `engine/step.go`: the missing-node branch of `drive` records the incident instead of the bare WARN park.
- `engine/step_nodes.go`: the park-site policy comment names the new kind. A sibling of `recordDefinitionDefect` (or a kind parameter on it) records the incident.
- `engine/step_gateways.go`: `matchJoinTokensToFlows` comment. Possibly a WARN on an empty-provenance spend (Q4).
- `runtime/kernel/lister.go`: the `IncidentCount` comment lists the new kind.
- Tests: `engine/state_incident_kind_test.go` (ordinal row), `engine/observability_noop_test.go` (missing-node site), `engine/step_join_resettles_test.go`, a new empty-provenance join test, `engine/baseline_operator_incidents_test.go`.
- Consumers that switch on `IncidentKind` or count `TokenWaiting` tokens see the new kind and state.

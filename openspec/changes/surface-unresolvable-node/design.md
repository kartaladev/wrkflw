# Design

## Context

See proposal.md, Why. Facts below were measured on main (`9c6dd2c1`) and constrain the approach.

- `drive` resolves each active token's node against the definition for its scope (`defForScope`). When `tdef.Node(tok.NodeID)` misses, it logs a WARN and parks the token as `TokenWaiting` (`engine/step.go:336-345`). `drive` only advances active tokens, so a token parked there is never visited again.
- `engine.Step` never compares `InstanceState.DefID`/`DefVersion` with the definition it is handed. `DefVersion` is written only on start (`engine/step_triggers.go:44`). The runtime looks up the definition by the snapshot's own `(DefID, DefVersion)` (`runtime/processdriver.go:518`, `runtime/jobstore.go:85`). So, through the runtime, a missing node means the content registered under that version changed. That happens when an in-memory registry is rebuilt at boot from edited code. The durable store refuses different content under a published version (`definition-store`), and in-memory re-registration in one process does too (`make-definition-registration-idempotent` keeps that refusal). Through a direct `engine.Step` call, the caller can also pass any definition.
- A third population reaches the same site: a definition that skipped `model.Validate` and has a flow targeting an undeclared node. The token is routed onto the flow's target and the lookup misses.
- `IncidentKind` is a persisted `int` (`engine/state.go:199`), appended only. Its ordinals are pinned by `TestIncidentKindOrdinalsAndNames`. `handleResolveIncident` whitelists `IncidentAction`, so every other kind is refused with `ErrIncidentNotResolvable`, with no case of its own. `causeOfDeathIncident` is an allow-list of `IncidentAction` too.
- `matchJoinTokensToFlows` (`engine/step_gateways.go:161-193`) makes two passes over the incoming flows. Pass 0 matches tokens whose `ArrivalFlow` equals the flow's identity. Pass 1 lets any unassigned token with an empty `ArrivalFlow` satisfy one still-unsatisfied flow. `assigned[t.ID]` stops one token being spent twice. Nothing limits how many flows the empty-provenance tokens satisfy between them. That is read off the code, and task 1.1 measures it.
- `engine.Token.ArrivalFlow` is exported, so a black-box test can build an `InstanceState` holding empty-provenance tokens parked at a join. The trigger it applies must reach `drive`. A `SignalReceived` that no token awaits is a documented no-op that never reaches `drive`, and the earlier probe recorded on #216 was inert for exactly that reason.

## Goals / Non-Goals

**Goals:**
- No token stops on an undeclared node without an incident naming it.
- The join concession's bound is stated once, the same way in both comments, and pinned in both directions by tests that can fail.
- Each join re-settle row has a negative neighbor.

**Non-Goals:**
- Detecting definition drift before a token reaches the missing node, for example by fingerprinting the definition in the snapshot. That would be a persisted-format change of its own.
- Retroactively surfacing tokens already parked by the old WARN (Q5, recommended no).

## Decisions

### D1. A distinct incident kind, not a widened `IncidentDefinitionDefect` (recorded on kartaladev/wrkflw#79)

The maintainer note on #79 records this. Two causes that arrive at one site but mean different things to the consumer get two named values. That is the `ErrOutcomeRequired`/`ErrInvalidOutcome` precedent (`engine/errors.go:190-204`, "deliberately distinct … so a caller can tell").

Rejected:
- **Widen `IncidentDefinitionDefect`'s wording** (option 2). It loses precision on every existing incident to cover a cause that is not a definition defect.
- **Keep the WARN** (option 4). The park-site policy classifies this as *stuck*, and stuck owes an incident.

The mechanics do not carry over from the error precedent. An `IncidentKind` is a persisted ordinal, so the new kind is appended as ordinal 4 and gets a `String()` case and an ordinal-test row.

### D2. Not resolvable, retired by cancel

No new code. Re-driving the token lands on the same missing node, so there is nothing for `ResolveIncident` to retry. The whitelist already refuses it. The spec pins the refusal and the cancel path so a future whitelist edit cannot silently admit the kind.

### D3. Raise site

The missing-node branch records the incident through the same body as `recordDefinitionDefect`. That body gains the kind as a parameter, or a sibling records it. The choice is left to the implementer, with one invariant: one place builds an `Incident` for a parked token. The incident carries the token, its scope, the missing node ID as `NodeID`, `Attempts: 1`, and a reason. The reason states that the definition driving the instance does not declare the node. When the instance's `DefID`/`DefVersion` differ from the definition's, the reason names both versions. That costs nothing and gives the operator the one fact the site can know.

### D4. The join bound is stated as a count over the population

Whatever Q3 decides, both comments state the aggregate in one sentence: "each empty-provenance token satisfies at most one otherwise-unsatisfied incoming flow, so *k* such tokens satisfy at most *k*, and only after every crossing token has been matched". The cap, if any, is then stated the same way. Rejected: fixing only `state.go`, which leaves the unambiguous but incomplete twin in `step_gateways.go`.

### D5. Re-settle rows are discriminating pairs

Each negative row (join not ready, a settle happens, the join must not fire) sits beside an at-limit accept that differs by one delivered flow. The accept row must fire. A join that refuses everything then fails the accept row, and one that fires too eagerly fails the negative row.

### Coupling

- `encode-persisted-enums-by-name` (kartaladev/wrkflw#44) will re-encode `IncidentKind`. Whichever lands second includes this kind. Neither blocks the other. If that change lands first, this one adds a name-table entry instead of relying on the ordinal alone.
- `strengthen-unfailable-assertions` edits `engine/baseline_operator_incidents_test.go` input-purity checks, but leaves them in place (its out of scope). This change adds rows to the same file. That is a textual merge only.
- `export-refusal-sentinels` and closed kartaladev/wrkflw#212 cover the boundary-target refusal. A boundary target that names no node now fails the step before any token reaches `drive`. So #212 no longer feeds this site, which corrects the forecast in the #79 thread.

## Risks / Trade-offs

- [A consumer counting `TokenWaiting` tokens, or switching exhaustively on `IncidentKind`, changes behavior] → BREAKING, called out in the proposal. v0.x.
- [An older build reads a snapshot with ordinal 4] → It cannot resolve it (whitelist), and it logs `IncidentKind(unknown)`. That is safe: no data loss, unlike the `Kind`-dropping hazard documented on `Incident.Kind`. Flagged as a persisted-format impact.
- [A missing-node token in a child scope] → The incident carries the token's `ScopeID`. Cancel still sweeps child scopes. Covered by a scenario row.
- [The aggregate measurement comes back as one, not *k*] → Then the code reading was wrong. D4's sentence is rewritten to the measured value, Q3 falls away, and the result is reported on the change.

## Migration Plan

No data migration. Rollback means reverting the commit. Snapshots written with ordinal 4 remain readable by the reverted build, as described in Risks.

## Open Questions

These block Readiness: agent. Spec rows that depend on them are marked `pending Qn` and written for the recommended option.

- **Q1. Name of the kind.** The #79 thread asks that the name say what the consumer got wrong, not what the engine saw.
  - `IncidentUnresolvableNode`: the engine's view, accurate for every cause.
  - `IncidentDefinitionMismatch`: the consumer's view, "the definition driving this instance is not the one it ran under". It is wrong for the unvalidated-flow cause, unless Q2 routes that cause elsewhere.
  - `IncidentVersionSkew`: too narrow, because the version usually matches (Context, second bullet).
  - Recommendation: `IncidentDefinitionMismatch` if Q2 picks (b). Otherwise `IncidentUnresolvableNode`. Specs use the placeholder name `IncidentUnresolvableNode`.
- **Q2. One kind, or split by cause at the site.**
  - (a) Always raise the new kind.
  - (b) If any flow of the effective definition targets the missing node, the definition itself routes there, so raise `IncidentDefinitionDefect` with reason "flow target is not declared". Otherwise raise the new kind. This discriminator is measurable at the site, unlike version comparison, which the runtime path cannot see.
  - (c) Compare `DefVersion` with `def.Version`. Rejected as the sole test, for the reason in Context.
  - Recommendation: (b). Specs write the "no flow targets it" row, which is the same under (a) and (b), as decided, and the flow-targets-it row as `pending Q2`.
- **Q3. Aggregate width of the join concession.**
  - (a) Accept *k* tokens for *k* flows and document it.
  - (b) Cap the aggregate at one flow per join firing.
  - Option (b) deadlocks the case the concession exists for: a pre-field snapshot mid-join holds one empty token per flow already crossed, and capping at one leaves the rest unsatisfied forever.
  - Recommendation: (a). The rows are marked `pending Q3`.
- **Q4. Make the early fire visible.** The `ArrivalFlow` comment assigns this to #79.
  - (a) A WARN when a join fires spending an empty-provenance token, naming the join, the instance and the count spent. Under the policy this is *degraded*, not stuck: the instance runs on.
  - (b) An incident. That would halt an instance that was going to finish, and the policy forbids it.
  - (c) Nothing.
  - Recommendation: (a). Marked `pending Q4`.
- **Q5. Tokens parked by the old WARN before upgrade.** They are `TokenWaiting` on a missing node and are never revisited.
  - (a) Leave them, and document how to find them (instance listing, node absent from definition).
  - (b) Re-examine waiting tokens on every `Step`, which costs a node lookup per waiting token per step.
  - Recommendation: (a). No spec row; it is documented in the release note.

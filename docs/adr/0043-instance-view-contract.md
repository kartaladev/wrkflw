# 43. Instance view serialization contract (snapshot + actionable DTOs)

- Status: Accepted
- Date: 2026-06-23

## Context

FOLLOWUPS.md item ③ asked that a process instance be serializable to JSON with
"complete information," so a front end can render process history and decide what
to do next (especially for human-task nodes).

There is no public `ProcessInstance` type. Runtime execution state is
`engine.InstanceState` (~25 fields), of which only about a third are
consumer-relevant; the rest is engine bookkeeping — `Timers`, `ArmedEvents`,
`Boundaries`, `Scopes`, `RootCompensations`, `ArchivedCompensations`,
`EventSubprocesses`, `Compensating`, `PendingCancel`, and various `*Seq`
counters, some of unexported types that would not even marshal. Marshalling
`engine.InstanceState` directly would (a) leak engine internals into a public
wire contract so every engine change breaks the FE, and (b) silently drop the
unexported fields. So "serialize the instance" must mean an explicit,
engine-decoupled view contract.

## Decision

A transport-agnostic DTO layer in the `runtime` package (alongside the existing
`InstanceSummary`/`InstancePage` read-side types), with pure mapper functions —
**the `engine` package stays zero-diff**:

- **`InstanceSnapshot`** — the full consumer-facing view: `InstanceID`, `DefID`,
  `DefVersion`, `Status` (string), `Variables`, `Tokens` (`[]TokenView`),
  `History` (`[]NodeVisitView`), `Tasks` (`[]TaskView`), `Incidents`
  (`[]IncidentView`), `StartedAt`, `EndedAt`. It deliberately excludes ALL engine
  bookkeeping; a JSON no-leak guard test asserts the marshaled output contains
  none of the banned keys.
- **`ActionableView`** — the curated decision view: `InstanceID`, `Status`, and
  `OpenTasks` (only tasks where `IsOpen()`), each with its `AllowedActions`
  derived from the definition's outgoing flows (`def.Outgoing(nodeID)` →
  `NextAction{FlowID, Target, Condition, IsDefault}`). This is why the mapper
  takes the `*model.ProcessDefinition`, not just the state.
- **Pure mappers** `NewInstanceSnapshot(state, def)` and
  `NewActionableView(state, def)`. Enum→string conversion (`StatusString`,
  token-state, task-state) lives in the mapper, so no `String()`/`MarshalJSON`
  is added to the `engine` enums.
- **Reachable via REST:** `GET /instances/{id}/snapshot` and
  `GET /instances/{id}/actionable`, registered on the existing router. A thin
  public facade method `service.Service.GetInstanceWithDefinition(ctx, id)`
  returns both the state and its resolved definition for the handlers (no such
  combined fetch existed before).

## Consequences

- The FE contract is stable across engine refactors: the engine can change its
  internal state shape freely without breaking the wire format.
- The `engine` package is untouched (verified zero-diff for this sub-project),
  honoring ADR-0002 purity and keeping enum semantics out of the wire layer.
- The `ActionableView` requires the process definition to derive next actions;
  when `def` is nil the actions list is empty (documented on the mapper).
- **Known duplication:** `runtime.StatusString` mirrors the private
  `statusString` mapping in `transport/rest`. Consolidating them onto the new
  exported helper is a follow-up, flagged here rather than left silent.
- **gRPC exposure is deferred.** A `GetInstanceSnapshot` RPC mirroring the REST
  task is a thin follow-up against `transport/grpc/proto`; logged so the gap is
  explicit.
- A condition on a flow that leaves a non-gateway node is rejected by
  `model.Validate` (`ErrConditionNotAllowed`); the actionable-view tests route
  conditions through a gateway accordingly.

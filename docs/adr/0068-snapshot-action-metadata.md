# 0068. Surface definition-scoped action metadata in snapshots and gRPC snapshot RPCs

Status: Accepted — 2026-06-27
Supersedes: none. Follow-up to ADR-0063 (definition-scoped action catalog).

## Context

ADR-0063 added definition-scoped and inline service actions. None of that wiring is
observable to a consumer inspecting a running instance: `runtime.InstanceSnapshot` carries
no action metadata (and `NewInstanceSnapshot` ignores the `*model.ProcessDefinition` it is
handed), and the gRPC transport has no snapshot RPC at all — only `GetInstance` returning the
basic `Instance` message, so tokens/history/tasks/incidents are gRPC-invisible. The REST
transport already serves `GET /instances/{id}/snapshot` and `.../actionable`.

`action.Catalog` is deliberately a one-method interface (`Resolve`) and is not enumerable.

## Decision

1. **`model.ProcessDefinition.ScopedActionNames() []string`** — additive accessor returning
   the sorted scoped-action names (nil when none). Populated at `Build()` from the builder's
   accumulator. We do **not** widen the `action.Catalog` interface; enumeration stays a
   definition concern.

2. **Enrich `runtime.InstanceSnapshot`** with `ScopedActions []string` and
   `ActionBindings []ActionBindingView` (`{NodeID, NodeKind, Action, Inline}`), populated by
   `NewInstanceSnapshot` from the definition (the formerly-ignored `def`). Bindings cover
   `ServiceTask`/`BusinessRuleTask` nodes; `Inline` = node carries an inline action; `Action`
   empty = default-by-id. Pure, def-derived; no global-catalog/tier resolution (YAGNI).
   `ActionableView` is unchanged — it is human-task-routing focused and service-action
   bindings do not belong there.

3. **gRPC `GetInstanceSnapshot` and `GetActionableView` RPCs** mirroring the REST endpoints,
   with proto messages for the full snapshot/actionable projections. Regeneration uses the
   raw-protoc fallback documented in `buf.gen.yaml` (buf is not installed locally).

## Consequences

- `model` gains one additive accessor; no behavior change, engine untouched (import-purity
  preserved). This is a deliberate, feature-justified `model` diff (cf. the standing
  "engine/model kept near-zero-diff" convention).
- Consumers can now see a definition's action wiring (inline vs named, scoped names) directly
  from a snapshot, over both REST and gRPC, and gRPC reaches snapshot/actionable parity.
- Inline actions remain non-serializable; only their presence (`Inline: true`) and the bound
  name are surfaced — never the Go function.
- The snapshot does not report the resolved tier (inline/scoped/global) — adding it later is
  additive and would require the global catalog at mapping time.

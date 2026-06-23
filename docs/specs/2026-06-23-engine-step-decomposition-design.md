# engine/step.go Decomposition — Design & Decisions

- Status: Approved (brainstorming), implementation deferred
- Date: 2026-06-23
- Relation: a late addition to the FOLLOWUPS resolution wave
  (`docs/specs/2026-06-23-followups-resolution-design.md`). Not one of the
  original six FOLLOWUPS.md items; raised separately ("forgot to mention").
  Executes as a sub-project **between layout hygiene (①) and the Node-interface
  redesign (②)**.

## Problem

`engine/step.go` is a 3251-line god file — the pure token state machine
(ADR-0002). It concentrates two large dispatch switches plus the engine's
gnarliest cross-cutting machinery in one file, which is hard to read, hard to
hold in context, and risky to edit.

Measured shape:

| Region | ~Lines | What |
|---|---|---|
| `Step()` trigger dispatch | 680 | ~18 trigger cases (StartInstance, ActionCompleted/Failed, Cancel/CompensateRequested, TimerFired{SLA,InWait,Retry}, Human*, Signal/Message/SubInstance*, ResolveIncident) |
| `drive()` node-kind dispatch | 630 | 13-arm `switch node.Kind` |
| Cross-cutting machinery | ~1850 | error propagation (~260), compensation walk (~350), boundary + event-subprocess arming/firing, parallel/inclusive fork-join, gateway-win resolution, SLA/reminder/retry handlers, token/id/var utils |

A node-kind strategy split alone addresses only ~19% of the file; the trigger
switch and the compensation/error machinery are just as large.

## Decision

Decompose the whole file, by its natural seams, as a **behavior-preserving pure
refactor**:

1. **Node-kind strategy registry.** A `nodeStrategy` interface with one stateless
   zero-size concrete strategy per kind, dispatched through a package-level
   immutable `map[model.NodeKind]nodeStrategy`. `drive()` becomes a thin
   dispatcher.
   ```go
   type nodeStrategy interface {
       enter(c *stepCtx, tok *Token, node model.Node) ([]Command, error)
   }
   var nodeStrategies = map[model.NodeKind]nodeStrategy{ /* one per arm-bearing kind */ }
   ```
   The registry holds ONLY the kinds that have a `drive()` arm today. Kinds with
   no arm must keep falling through to the existing post-switch logic **exactly**
   (today's switch is deliberately non-exhaustive — e.g. `BusinessRuleTask`,
   `ReceiveTask`, `SendTask`, `TerminateEndEvent`, `BoundaryEvent`,
   `EventSubProcess` have no arm). This is the chief correctness trap.

2. **Trigger handlers.** `Step()` keeps a thin **type-switch** (needed anyway to
   recover the concrete trigger type) delegating each case to an extracted typed
   handler func (`handleActionCompleted(c, t)` …). No trigger registry — that
   would require adding a discriminator method to the `Trigger` interface (an
   engine API change) for no real gain, and a type-assert is still needed inside
   each handler.

3. **`stepCtx` context object** carrying the repeated inputs, scoped to the new
   dispatch layer:
   ```go
   type stepCtx struct {
       def  *model.ProcessDefinition
       s    *InstanceState
       at   time.Time
       mode StepMode
       opt  StepOptions
   }
   ```
   Strategies and trigger handlers take `*stepCtx`. **Relocated helper functions
   keep their current signatures** — threading `stepCtx` deeper is explicitly out
   of scope (limits churn/risk; can be a later follow-up).

4. **Collaborator files** (same `engine` package; Go allows methods across
   files). Cross-cutting algorithms move out of `step.go` unchanged:
   - `step_nodes.go` — `nodeStrategy` interface + registry + the per-kind strategies.
   - `step_triggers.go` — the ~18 trigger handler funcs.
   - `step_gateways.go` — fork/join (parallel/inclusive), exclusive selection, gateway-win, reachability.
   - `step_boundaries.go` — boundary arming/firing.
   - `step_eventsubprocess.go` — event-subprocess arming/firing.
   - `step_compensation.go` — the compensation walk (`stepCompensateRequested`/`beginCompensation`/`...Advance`/`...Finish`, record helpers).
   - `step_errors.go` — `propagateError`.
   - `step_timers.go` — SLA/reminder/retry-fired handlers + `reinvokeServiceAction`.
   - `step_state.go` — token/id/visit/var utilities, `defForScope`, `effectiveRetryPolicy`, `cloneState`.
   - `step.go` shrinks to: `Step()` (trigger type-switch), `drive()` (registry dispatch), `stepCtx`.

## Why the Strategy registry despite a closed set

A `switch` is the idiomatic Go choice for a closed, engine-owned variant set, and
a registry **loses the compiler's exhaustiveness check** and scatters behavior.
The project owner chose the registry deliberately (uniform "one type per handler"
structure, composability). To buy back the lost safety, the design **mandates a
completeness test** (below). This is the explicit trade and its mitigation.

## Guardrails (non-negotiable)

- **Pure refactor.** No behavior change; no change to `Step`'s
  `(StepResult, error)` contract or any exported signature. Gate: the existing
  `engine` test suite green before AND after every task (CLAUDE.md pure-refactor
  carve-out — no new behavioral tests required, but see the completeness test).
- **Exhaustiveness recovered by test.** A new `engine` test asserts every
  `NodeKind` that has a `drive()` arm today has a registered strategy, and pins
  the documented set of intentionally-unhandled kinds (so a future omission
  fails loudly). This is the one *new* test, TDD'd.
- **Stateless strategies.** Zero-size structs; registry built once at package
  init and never mutated; dispatch by key (never map iteration) so determinism
  and ADR-0002 purity hold. No injected dependencies on any strategy.
- **Incremental, SDD.** One handler extracted per task, engine tests green per
  task, commit per task. Never batch multiple handlers.
- **Sequenced before the Node-interface redesign (②).** The `nodeStrategy.enter`
  signature takes `model.Node`, which is a struct now and an interface after ②;
  the registry key (`NodeKind`) is stable across ②. So ② later changes only each
  strategy's *internals* (`node.Field` → type-assert), in small focused files
  instead of one monolith. This is the payoff of "before ②".

## ADR

**ADR-0044** (engine/step.go decomposition: node-kind strategy registry + trigger
handlers + collaborator files). Note: ADR *number* 0044 > 0042/0043 (reserved by
the already-written ② and ③ plans) even though this executes *before* them — ADR
numbers are chronological IDs, not execution order.

## Out of scope

- Threading `stepCtx` into the relocated helper functions (later follow-up).
- A trigger registry / `Trigger` interface discriminator.
- Any behavior change, bug fix, or new node/trigger handling.
- The Node-interface migration itself (sub-project ②).

## Verification

`go test -race ./engine/... ./...` green (incl. Postgres via testcontainers);
touched-package coverage ≥ 85%; `golangci-lint run ./...` clean; engine/model
import-purity intact; the new completeness test passes.

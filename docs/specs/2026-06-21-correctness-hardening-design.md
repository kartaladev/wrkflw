# Correctness & tests hardening — design spec

- Status: Accepted
- Date: 2026-06-21
- Related: ADR-0002 (pure stepper), ADR-0013 (nested-scope compensation hoist),
  ADR-0014 (mixed split+join gateway validation), ADR-0011 (REST/gRPC transports)
- Plan: `docs/plans/2026-06-21-correctness-hardening.md`

## 1. Goal & scope

A focused hardening pass over correctness bugs and the highest-value missing
tests called out in `docs/plans/HANDOVER.md`. It fixes one labelled **MUST-FIX**
engine bug, adds one structural validation rule, introduces a typed wrong-state
error across the transports, and closes two test gaps. No new product surface
beyond the validation rule and the error sentinel; the engine stays pure
(stdlib + `model`/`authz`/`humantask`/`expreval`), deterministic, and unchanged
in signature.

Five independent work-streams (sequenced A→E, but each is its own task with its
own test cycle):

- **A. Nested-scope compensation hoist (MUST-FIX)** — completed sub-process scope
  compensation records survive scope close and become rollback-able. ADR-0013.
- **B. Mixed split+join gateway validation** — `model.Validate` rejects a gateway
  that both splits and joins. ADR-0014.
- **C. `service.ErrConflict` wrong-state sentinel** — wrong-state operations map to
  HTTP 422 / gRPC `FailedPrecondition` instead of 500/Internal.
- **D. Parked-async Postgres resume e2e** — the "highest-value missing test":
  park → persist → reload via a fresh `Store` → advance clock → resume to
  completion.
- **E. Inner-scope topology tests** — scope propagation through boundary /
  event-gateway / inclusive / SLA-timer constructs *inside* a sub-process.

**Explicitly out of scope (YAGNI):** scope-targeted compensation (archive keyed by
scope id + a scope selector on the admin trigger); reachability/fork-join pairing
analysis in `Validate`; an engine/runtime-level wrong-state sentinel. These are
recorded as deferred follow-ups (§8).

Non-goals carried from the engine-core contract: `Step` stays
deterministic and pure (never mutates its input `InstanceState`); all IDs come
from `InstanceState` counters; no clock/transport/vendor in the engine core.

## 2. Work-stream A — nested-scope compensation hoist (MUST-FIX)

### The bug
When a sub-process scope completes normally, `engine/step.go`'s sub-process-exit
path calls `s.closeScope(currentScopeID)`, which removes the `Scope` from
`s.Scopes` and **drops its `Compensations`**. A subsequent `CompensateRequested`
(which walks only `RootCompensations`) therefore cannot roll back compensable
activities that ran inside a now-completed sub-process — silent data loss for
nested sagas. `CompensateRequested`'s godoc documents this as a known limitation;
`Compensate{ScopeID,FromNode}` is reserved but inert.

### The fix (ADR-0013)
On the normal sub-process-exit path, **before** `closeScope`, hoist the closing
scope's `Compensations` into its parent, preserving completion order:

```go
// hoistCompensations moves childID's accumulated compensation records into its
// parent (parentID), appended in completion order, so they remain rollback-able
// after the child scope closes. parentID "" targets RootCompensations.
func (s *InstanceState) hoistCompensations(childID, parentID string) {
    child := s.scopeByID(childID)
    if child == nil || len(child.Compensations) == 0 {
        return
    }
    if parentID == "" {
        s.RootCompensations = append(s.RootCompensations, child.Compensations...)
    } else if parent := s.scopeByID(parentID); parent != nil {
        parent.Compensations = append(parent.Compensations, child.Compensations...)
    }
    child.Compensations = nil
}
```

Call site (sub-process exit, replacing the bare `closeScope`):

```go
s.hoistCompensations(currentScopeID, parentScopeID)
s.closeScope(currentScopeID)
// ... then the existing "if spNode has CompensationAction → recordCompensation(parentScopeID, …)"
```

Result: the parent scope's (or root's) compensation list ends up
`[…parent-records, …hoisted-child-records, spNode-own-comp]`. The reverse-order
walk compensates the sub-process node's own action first, then the child
activities most-recent-first, then the parent's earlier activities — correct saga
order. **Nested sub-processes work by induction**: each close hoists one level up,
so a grandchild's records reach the root after two closes.

### Why hoist (not archive)
Hoisting needs **no new `InstanceState` field**, so `cloneState`, the snapshot
JSONB shape, and the persistence round-trip are unchanged. The existing root
`CompensateRequested` walk reaches everything. It matches BPMN saga semantics
(compensation of a completed embedded sub-process is performed as part of the
enclosing scope's compensation, reverse order). The trade-off — losing per-scope
identity — is acceptable because the only compensation entry point today is the
root-scope admin trigger; per-scope targeting is deferred (§8).

### `Compensate` command
Stays reserved and inert. Its godoc and `CompensateRequested`'s "root scope only /
records dropped" limitation note are **corrected** to state that nested records
are now reachable via the root walk and that `Compensate{ScopeID,FromNode}`
remains the future vehicle for *scope-targeted* compensation (which needs a
producer — a BPMN compensation boundary/throw event — not built here).

### Other `closeScope` callers
The error-propagation and cancel paths also call `closeScope`. Behavioural change
there is **out of scope** (compensation-on-error has different semantics). The
implementer audits those call sites and confirms the hoist is added **only** to
the normal-exit path; the audit result is recorded so a reviewer can see the
other callers were considered, not missed.

### Tests (regression-first)
1. **RED reproduction:** a definition with a compensable ServiceTask *inside* a
   sub-process that then completes; root continues; `CompensateRequested{ToNode:""}`
   ⇒ assert the inner activity's compensating `InvokeAction` is emitted. Fails
   today (record dropped).
2. Nested two-level sub-process: grandchild compensable activity is reachable
   after both scopes close.
3. Ordering: parent activity + sub-process inner activity + sub-process own
   `CompensationAction` compensate in correct reverse order.
4. Existing compensation tests (`engine/step_compensation_test.go`) stay green.

## 3. Work-stream B — mixed split+join gateway validation (ADR-0014)

`model.Validate` cannot today distinguish a converging from a diverging gateway,
so a gateway authored with both multiple inputs and multiple outputs routes
ambiguously and silently. New rule:

> A gateway node (`KindExclusiveGateway`, `KindInclusiveGateway`,
> `KindParallelGateway`, `KindEventBasedGateway`) with **both** more than one
> incoming sequence flow **and** more than one outgoing sequence flow is a
> structural error.

- New sentinel `model.ErrMixedGateway` (sibling of the existing `Validate`
  sentinels), wrapped with the offending node id for diagnostics.
- Enforced recursively into sub-process definitions, exactly like the existing
  `Validate` recursion (same traversal, same cycle guard).
- Counts use the existing incoming/outgoing flow lookups; no new model fields.

Pure split (1-in, N-out), pure join (N-in, 1-out), and pass-through (1-in, 1-out)
gateways all remain valid. Tests cover: mixed → `ErrMixedGateway`; pure split,
pure join, pass-through → ok; mixed gateway nested in a sub-process → error.

## 4. Work-stream C — `service.ErrConflict` wrong-state sentinel

Wrong-state operations (claiming/completing/reassigning a task not in the right
state; delivering a signal/message or completing a task on a finished instance)
currently surface as untyped errors that both transports map to **500 / Internal**
— wrong for a client-caused precondition failure.

- **Sentinel:** `var ErrConflict = errors.New("service: conflicting state")` in the
  transport-neutral `service` package (sibling of its existing sentinels). It is a
  *classification* the service attaches; the underlying error is wrapped
  (`fmt.Errorf("%w: …", ErrConflict, …)`) so `errors.Is(err, service.ErrConflict)`
  holds while the cause remains inspectable.
- **Where the service maps:** the implementer enumerates, in the spec's
  appendix during implementation, the exact source errors (from `humantask`
  task-state checks and `runtime`/engine "instance not running" / "token not
  found in a completable state" returns) that classify as wrong-state, and wraps
  them at the `service.Service` boundary. Errors that are genuinely *not-found*
  keep their existing `ErrInstanceNotFound`/not-found mapping (404); only true
  *wrong-state* conditions classify as `ErrConflict`.
- **Transport mapping:**
  - `transport/rest` `WriteHTTPError`: `errors.Is(err, service.ErrConflict)` →
    **422 Unprocessable Entity** (JSON error body, consistent with the existing
    error writer).
  - `transport/grpc` `mapToGRPCStatus`: → **`codes.FailedPrecondition`**.
- Engine and runtime error taxonomy are **unchanged** (decision per design Q3-A):
  the classification lives at the service seam, keeping the engine pure.

Tests: a table per wrong-state operation asserting the service returns an error
satisfying `errors.Is(_, ErrConflict)`; a REST handler test asserting 422; a gRPC
test asserting `codes.FailedPrecondition`. Existing not-found (404 / `NotFound`)
mappings stay green.

## 5. Work-stream D — parked-async Postgres resume e2e (highest-value missing test)

A `testcontainers` e2e (uses `database.RunTestDatabase`) proving the persisted
snapshot of a *parked* instance survives a real DB reload through a **fresh
`Store`** and resumes deterministically:

1. Build a runner over the Postgres `Store` + a fake clock; start a process that
   parks on a **timer** (intermediate timer / SLA waiter).
2. Construct a **new** `Store` over the same pool (simulating a process restart) —
   load the instance state from Postgres only.
3. Advance the shared fake clock; deliver the `TimerFired` resume trigger via a
   runner built on the fresh store; drive to `StatusCompleted`.
4. Assert the final persisted status + that no token/timer leaked.

A second variant parks on a **boundary timer / armed signal-or-message event** to
exercise the JSON round-trip of `Boundaries`/`ArmedEvents`/`EventSubprocesses`
(only sync-path-proven today). Lives in the `internal/persistence/postgres` or
`runtime` test package as appropriate; black-box, real Postgres, never mocked.

## 6. Work-stream E — inner-scope topology tests

Engine tests (`engine` package, black-box) for scope propagation through
constructs nested *inside* a sub-process — only parallel-fork-in-subprocess has a
dedicated test today. Add one focused test each for:

- a **boundary event** (timer or error) on an activity inside a sub-process,
- an **event-based gateway** inside a sub-process,
- an **inclusive (OR) gateway** fork+join inside a sub-process,
- an **SLA timer** on a human task inside a sub-process.

Each asserts the inner construct drives correctly within the child scope and the
sub-process still exits cleanly to the parent. These are confidence tests on
code believed correct; **if any surfaces a real bug, that becomes a
regression-fix task** (test-first) rather than being silently adjusted.

## 7. Testing, determinism & verification

- TDD strict (CLAUDE.md "TDD Operational Discipline"): every new symbol and every
  behavioural change gets a failing test first with a visible RED. Bug fixes (A)
  get a regression test that reproduces the bug first.
- Black-box tests (`package <pkg>_test`); table tests use the project `table-test`
  `assert`-closure form; `t.Context()`; Postgres tests via
  `database.RunTestDatabase` (testcontainers), never mocked.
- Determinism preserved: the hoist uses slice-order appends only; no map
  iteration into command/record order; no new IDs; no clock reads.
- Gate: `go test -race ./...` green; ≥85% line coverage on touched packages
  (`engine`, `model`, `service`, `transport/rest`, `transport/grpc`,
  `internal/persistence/postgres`); `golangci-lint run ./...` clean; engine purity
  intact (no transport/vendor imports added to `engine`/`model`).

## 8. Deferred follow-ups (deliberate)

1. **Scope-targeted compensation.** Archive closed-scope compensations keyed by
   scope id + a scope selector on `CompensateRequested`, with `Compensate{ScopeID,
   FromNode}` as the emitted vehicle and a BPMN compensation boundary/throw event
   as its producer. The hoist makes root rollback correct; per-scope targeting is
   a future feature.
2. **Reachability/fork-join pairing validation.** The mixed-gateway rule catches
   the silent-misroute case; a full converging-join-matches-diverging-fork
   reachability pass (and condition-placement checks) is a larger validation
   effort.
3. **Engine/runtime wrong-state sentinel.** Embedded consumers calling the engine
   directly still get untyped wrong-state errors; promoting the classification
   into the engine/runtime taxonomy is deferred (service-seam classification
   covers the transport users now).
4. **Compensation-on-error / cancel paths.** This pass only hoists on *normal*
   sub-process exit; compensation semantics for error/cancel scope closes are a
   separate design.

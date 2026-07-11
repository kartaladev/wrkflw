# Action-Level Resiliency Policy — Implementation Plan

**Date:** 2026-07-11
**Spec:** `docs/specs/2026-07-11-action-resiliency-policy-design.md` (LOCKED)
**ADR:** ADR-0126
**Branch:** `feat/action-resiliency-policy`

Move action-execution resiliency (timeout, panic-recover, retry) from runtime-only to
per-action configurable in the `action` package, with runtime fallback. Precedence:
timeout = action ?? runtime-default; recover = action ?? on; retry = action > node > runtime-default.

## Confirmed source facts (investigated before planning)

- `definition/model` imports `action` (`ScopedCatalog() action.Catalog`, `builder.go`), so `action`
  MUST NOT import `definition/model`. `action.RetryPolicy` is a pure-data mirror; the runtime converts.
  `model.RetryPolicy` field is `BackoffCoef` (not `Multiplier`); `action.RetryPolicy.Multiplier` → `BackoffCoef`.
- Runtime builds `StepOptions` once per trigger in `deliverLoop` (`processdriver.go:465`), passing
  `DefaultRetryPolicy: driver.defaultRetryPolicy` and `Evaluator`. `st` there is the PRE-step state.
- `engine.handleActionFailed` (`step_triggers.go:268`) calls `effectiveRetryPolicy(node, opt)`
  (`step_state.go:212`): precedence node > `opt.DefaultRetryPolicy` > none.
- Retry is DURABLE (TimerRetry scheduled; survives restart). Each ActionFailed step must
  re-derive the override from durable state (def+st+CommandID) — cannot be stashed in memory.
- `engine.ActionFailed` carries only `CommandID`. The failing token (`AwaitCommand==CommandID`)
  holds `NodeID`+`ScopeID` (exported). `defForScope` (engine-internal) resolves the scope def.
- Node primary-action name = `model.ActionOf(node)` (exported) or `node.ID()` (default-by-id) —
  mirrors engine-internal `mainActionName`.
- `MapCatalog` = `type MapCatalog map[string]Action`. `NewCatalog(m) MapCatalog`. NO external code
  depends on the concrete `MapCatalog` return type (only `def.scoped action.Catalog` assignment and
  `cat := ...` inferred sites, all used as `Catalog`). So changing the return type to `Catalog` is safe.
- `perform` InvokeAction (`processdriver_action.go:117`): `actionContext(actx)` (global timeout) +
  `safeActionDo` (always recover). InvokeCancelAction (`:156`): same, best-effort.

## MapCatalog design choice (documented)

Keep the `MapCatalog` map type and its bare `Resolve` UNCHANGED (no default). Change
`NewCatalog` to `func NewCatalog(m map[string]Action, opts ...Option) Catalog`:
- no opts → returns `MapCatalog(m)` (concrete value, assignable to `Catalog`, identical behavior — back-compatible);
- with opts → returns `*defaultingCatalog{inner: MapCatalog(m), defaults: opts}` that applies the
  lazy default at Resolve. This keeps bare stored actions bare in the map and adds zero cost to the
  no-default path. Chosen over a struct-typed `MapCatalog` (would break the `map[string]Action`
  conversion used by `builder.go`) and over a separate `NewCatalogWithDefaults` (spec wants variadic).

## Retry-override seam mechanism (documented)

New exported engine helper (`engine/failing_action.go`), NO action import:
`func FailingActionNode(def *model.ProcessDefinition, st InstanceState, commandID string) (model.Node, *model.ProcessDefinition, bool)`
— finds the token awaiting commandID, resolves its scope-effective def via `defForScope`, returns the
node + scope def. Runtime (`processdriver_action.go`, new `overrideRetryPolicy`): for an
`engine.ActionFailed` trigger only, resolve the node's action via `action.Resolve(scopeDef.ScopedCatalog(),
driver.cat, name)`, `action.ResolvePolicy(a).Retry`; if set, convert to `*model.RetryPolicy` and pass as
`StepOptions.OverrideRetryPolicy`. Non-ActionFailed / no-retry-policy ⇒ nil ⇒ today's behavior. Engine
`effectiveRetryPolicy` gains a top tier: override > node > default > none.

## TDD steps (each RED via `go test` before GREEN; commit per unit)

### Unit a — action.RetryPolicy + 3 capability interfaces + WithX ctors + unexported policy
- File: `action/policy.go` (+ `action/policy_test.go`, black-box `action_test`).
- RED: test asserting `action.WithExecTimeout`, `WithRetryPolicy`, `WithRecover` build a `policy`
  (observed indirectly via `Wrap`+`ResolvePolicy` in unit b) — for unit a, test the capability
  interfaces are satisfied by the wrappers (deferred to b). Practically fold a+b: write
  `action/policy.go` types (RetryPolicy, TimedAction, RetriableAction, RecoverableAction, Option,
  Policy) and `action/wrap.go` together but drive each with its own failing test first.
- Types: `RetryPolicy{MaxAttempts int; InitialInterval time.Duration; Multiplier float64; MaxInterval time.Duration}`;
  `TimedAction{Action; ExecTimeout() time.Duration}`; `RetriableAction{Action; RetryPolicy() RetryPolicy}`;
  `RecoverableAction{Action; RecoverPanics() bool}`; `Policy{Timeout *time.Duration; Retry *RetryPolicy; Recover *bool}`;
  `policy{timeout *time.Duration; retry *RetryPolicy; recover *bool}`; `Option func(*policy)`;
  `WithExecTimeout/WithRetryPolicy/WithRecover`.

### Unit b — wrappers + Wrap + Unwrap + ResolvePolicy
- File: `action/wrap.go` (+ `action/wrap_test.go`).
- RED tests (table where 2+ cases):
  - `timedAction.Do` enforces context deadline (a blocking action sees `ctx.Done()`); standalone.
  - `recoverableAction.Do` converts panic→error when `on==true`; propagates panic when `on==false`.
  - `retriableAction.Do` does NOT retry (calls bare exactly once even if it errors).
  - `Wrap(bare)` no opts returns bare unchanged (identity).
  - `Wrap` merge/override: re-wrapping the same concern REPLACES (no double-stack); distinct concerns nest.
  - `ResolvePolicy` aggregates the chain; a custom type implementing ONE capability interface directly
    (no Unwrap) is detected.
  - `Unwrap(a)` fully unwraps to the innermost bare action.
- Impl:
  - `timedAction{bare Action; d time.Duration}`: `ExecTimeout()`, `Unwrap() Action`, `Do` = timeout-wrapped bare.Do.
  - `retriableAction{bare Action; p RetryPolicy}`: `RetryPolicy()`, `Unwrap()`, `Do` = bare.Do (declarative; doc it).
  - `recoverableAction{bare Action; on bool}`: `RecoverPanics()`, `Unwrap()`, `Do` = recover-wrapped (unless on==false).
  - `Unwrap(a Action) Action`: loop over `interface{ Unwrap() Action }` to innermost.
  - `ResolvePolicy(a Action) Policy`: walk `a` then Unwrap chain; at each level type-assert each
    capability interface, first occurrence per concern wins.
  - `Wrap(a, opts...)`: `p := ResolvePolicy(a)`; `bare := Unwrap(a)`; seed `pol` from p; apply opts;
    rebuild recover→retry→timeout (innermost→outermost) for each set field; if none set return bare.
  - `Policy.empty()` unexported helper.

### Unit c — Catalog/Registry lazy default
- Files: `action/catalog.go` (+ `action/catalog_test.go`).
- RED (table): default applied only when action declares nothing; per-action Wrap wins; bare passes
  through when no default; `NewCatalog(m)` (no opts) identical to before.
- Impl: `NewCatalog(m, opts...) Catalog` (see design choice); `defaultingCatalog`; `NewRegistry(opts...)`
  stores `defaults []Option`, Resolve applies lazy default.

### Unit d — engine StepOptions.OverrideRetryPolicy + precedence
- Files: `engine/step.go` (field), `engine/step_state.go` (precedence), `engine/failing_action.go` (helper).
- RED: `effectiveRetryPolicy` table (`engine/step_state_test.go` or new) — override>node>default>none.
- RED: `FailingActionNode` returns node+scopeDef for a token awaiting a command; false otherwise.
- Impl: add `OverrideRetryPolicy *model.RetryPolicy` to `StepOptions`; prepend
  `case opt.OverrideRetryPolicy != nil: return opt.OverrideRetryPolicy.Normalize(), true` in
  `effectiveRetryPolicy`; add `FailingActionNode`.

### Unit e — runtime integration
- Files: `runtime/processdriver_action.go` (single-site exec + `overrideRetryPolicy` + `actionRetryToModel`),
  `runtime/processdriver.go` (wire `OverrideRetryPolicy` into StepOptions).
- RED (e2e, package `runtime_test`, construct via `runtimetest.MustRunner`):
  - action `WithExecTimeout` LONGER than runtime default is respected (deadline ≈ action value, proves not min()).
  - action `WithRetryPolicy` overrides a node-level `model.RetryPolicy` (node MaxAttempts=1 no-retry;
    action MaxAttempts≥3) and drives the durable retry to completion (fake clock + scheduler + FixedJitter).
  - `WithRecover(false)` lets a panic propagate (`assert.Panics` around Drive).
  - no-policy ⇒ defaults unchanged (regression — existing timeout/panic/retry tests stay green).
- Impl:
  - Refactor `actionContext(parent)` → `actionContextFor(parent, d)`; `safeActionDo` →
    `invokeActionDo(ctx, a, in, recoverPanics)`.
  - InvokeAction: `pol := action.ResolvePolicy(a)`; `bare := action.Unwrap(a)`; effective timeout =
    `pol.Timeout ?? driver.actionTimeout`; effective recover = `pol.Recover ?? true`; run bare once.
  - InvokeCancelAction: same effective timeout on bare; recover ALWAYS true (best-effort preserved).
  - `overrideRetryPolicy(def, st, t)`: ActionFailed → `engine.FailingActionNode` → resolve action →
    `ResolvePolicy(a).Retry` → `actionRetryToModel`.
  - `actionRetryToModel(action.RetryPolicy) model.RetryPolicy`: MaxAttempts, InitialInterval,
    BackoffCoef=Multiplier, MaxInterval.
  - deliverLoop: `OverrideRetryPolicy: driver.overrideRetryPolicy(def, st, t)`.

## Verification checklist

- [ ] `go build ./...`
- [ ] `go test -race ./...` — 0 failures
- [ ] action, runtime, engine coverage ≥ 85% (`go test -race -coverprofile ...`)
- [ ] `golangci-lint run ./...` clean
- [ ] `go list -deps ./action/... | grep definition/model` returns NOTHING (no import cycle)
- [ ] ADR-0126 written (Nygard)
- [ ] `/code-review high` findings adjudicated + fixed; `/security-review` clean
- [ ] merge --no-ff to main + push + delete branch

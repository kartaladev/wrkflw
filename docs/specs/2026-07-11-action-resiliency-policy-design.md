# Action-Level Resiliency Policy — Design

**Date:** 2026-07-11
**Status:** Approved (autonomous SDD run)
**New ADR:** ADR-0126

## Context

Action-execution resiliency is enforced entirely in the **runtime** today, in three separate places:

- **Timeout** — global, via `WithActionTimeout(d)`; `actionContext` wraps every `a.Do()` in
  `context.WithTimeout(driver.actionTimeout)`. No node-level or per-action timeout exists.
- **Panic recover** — always-on; `safeActionDo` `recover()`s and converts a panic to an error.
- **Retry** — **engine-decided and durable**. The runtime runs the action once; on failure it emits
  `ActionFailed(retryable)`. The engine's `handleActionFailed` then decides retry via
  `effectiveRetryPolicy(node, StepOptions)` (precedence **node-level `model.RetryPolicy` >
  `StepOptions.DefaultRetryPolicy` > none**) and schedules a **durable backoff** (survives restart,
  integrates with incident/resume). It is NOT an in-process loop.

Goal: let a consumer attach a resiliency policy to an **action** in the `action` package — by
wrapping a bare action, or via a Catalog/Registry default — and have the runtime **respect the
per-action policy, falling back to the runtime default when unset**.

### Hard constraint (import cycle)

`definition/model` **imports** `action` (`definition/model/definition.go`, `builder.go`). Therefore
`action` **must not** import `definition/model` — so the action package cannot reference
`model.RetryPolicy`. Resolution: `action` declares its own pure-data `RetryPolicy` (the same four
fields); the runtime (which imports both) converts it to `model.RetryPolicy` at the boundary. The
retry *algorithm* (`Normalize`/`Backoff`) stays solely in `model` — `action.RetryPolicy` is a
declaration, `model.RetryPolicy` is the mechanism.

## Decisions (locked)

From the brainstorm:

- **Retry feeds the existing durable engine mechanism** (not a new in-process loop). An action's
  retry policy becomes an input to `effectiveRetryPolicy`.
- **Precedence: action > node > runtime-default** for retry (user override of the default node-first
  chain — the action's own policy is authoritative). For timeout: **action > runtime-default** (no
  node tier). For recover: **action-override else always-on**.
- **Catalog/Registry default policy is applied lazily at Resolve**, only to an action that carries
  no policy of its own — so a per-action `action.Wrap(...)` always wins.

## Design

### 1. `action` package — three capability interfaces + wrappers

Three **separate, single-method capability interfaces** — chosen (user) so a consumer's own action
type can implement just ONE directly (e.g. a custom action that natively knows its timeout), without
adopting a combined accessor. Interface *satisfaction* signals the capability is present; the method
returns its value.

```go
// RetryPolicy is a pure declaration mirroring the engine's retry fields. The
// runtime converts it to model.RetryPolicy (which owns Normalize/Backoff). Kept
// separate to avoid the model→action import cycle.
type RetryPolicy struct {
    MaxAttempts     int
    InitialInterval time.Duration
    Multiplier      float64
    MaxInterval     time.Duration
}

// Capability interfaces (each embeds Action). A type that implements one DECLARES
// that capability; the runtime type-asserts each independently.
type TimedAction interface {
    Action
    ExecTimeout() time.Duration // the per-action execution timeout
}
type RetriableAction interface {
    Action
    RetryPolicy() RetryPolicy // the per-action retry policy
}
type RecoverableAction interface {
    Action
    RecoverPanics() bool // true = recover panics (default), false = let them propagate
}

// Options build the wrapper layers. Same ergonomic surface as before.
type Option func(*policy) // policy is unexported; consumers only use the WithX ctors
func WithExecTimeout(d time.Duration) Option
func WithRetryPolicy(p RetryPolicy) Option
func WithRecover(on bool) Option

// Wrap applies the option-selected capabilities as typed wrapper layers. It first
// UNWRAPS a to its bare action, aggregating any capabilities the existing wrapper
// layers already carry; options override same-concern values; then it REBUILDS the
// layers in canonical order (recover → retry → timeout, innermost→outermost). So
// re-wrapping the same concern REPLACES (never double-stacks a concern), while
// distinct concerns nest. Wrap(bare) with no opts returns bare unchanged.
func Wrap(a Action, opts ...Option) Action

// Policy is the aggregated, runtime-facing view of an action's declared
// capabilities (nil field ⇒ unset ⇒ fall back). ResolvePolicy walks a's Unwrap
// chain, type-asserting each capability interface (first occurrence wins), and
// returns the aggregate. Used by the runtime and by Wrap's unwrap step.
type Policy struct {
    Timeout *time.Duration
    Retry   *RetryPolicy
    Recover *bool
}
func ResolvePolicy(a Action) Policy
```

Three unexported wrapper types — `timedAction{bare; d}`, `retriableAction{bare; p}`,
`recoverableAction{bare; on}` — each implements its capability interface + `Unwrap() Action` and
delegates `Do`. **Timed and recoverable self-enforce in `Do`** (timeout deadline; panic→error)
for standalone use; **retriable is declarative** (its `Do` just delegates — retry is engine-only).
Nesting order in `Do` composes cleanly (e.g. timed(retriable(recoverable(bare)))). The internal
`policy` builder + `ResolvePolicy` aggregation is how `Wrap` avoids double-stacking a concern.

### 2. Catalog / Registry defaults

- `NewCatalog(m map[string]Action, opts ...Option)` and `NewRegistry(opts ...Option)` store a
  default `Policy`.
- Resolve **decorates lazily**: if the resolved action declares no capability (`ResolvePolicy(a)`
  all-nil) and a catalog default is set, return `Wrap(a, defaultOpts...)`; otherwise return it
  unchanged. Bare stored actions stay bare in the map; a per-action `Wrap` (or a custom type
  implementing a capability interface) wins because it already declares a policy.
- Consumers may also `Wrap` at registration if they prefer eager, self-describing storage.

### 3. Runtime integration (single execution site — no double-application)

At `InvokeAction` (`runtime/processdriver_action.go`):

- Resolve the action; `pol := action.ResolvePolicy(a)` (walks the Unwrap chain); `bare` = innermost
  action (unwrap all layers).
- **Timeout:** effective = `pol.Timeout` if set else `driver.actionTimeout`. Apply once around
  `bare.Do()` (replaces the fixed `actionContext`). The runtime runs the **bare** action under the
  effective policy (not the wrappers' `Do`), so timeout/recover apply exactly once and spans/metrics
  stay at the one site.
- **Recover:** effective = `pol.Recover` if set else true. `safeActionDo` becomes conditional.
- **Retry (action > node > default):** the runtime surfaces the failing node's action retry policy
  as an **override above the node**. Engine seam:
  - Add `StepOptions.OverrideRetryPolicy *model.RetryPolicy`.
  - `effectiveRetryPolicy` precedence becomes **override > node > `DefaultRetryPolicy` > none**.
  - The runtime, when it feeds the `ActionFailed` step for a node whose action carries a
    `Retry` policy, resolves that action, converts `action.RetryPolicy → model.RetryPolicy`, and
    sets `StepOptions.OverrideRetryPolicy`. When the action has no retry policy, the override is nil
    and today's node>default behavior is unchanged.

No other engine change; interrupting/durable/incident paths untouched.

### 4. Precedence summary

| Concern | Precedence | Enforced |
|---|---|---|
| Timeout | action ?? runtime-default | runtime (per-invocation) |
| Recover | action ?? on | runtime (per-invocation) |
| Retry | **action > node > runtime-default** | engine (durable), fed by runtime |

## Rejected / alternatives

- **In-process retry loop in the wrapper** — rejected (brainstorm): non-durable, bypasses
  incident/resume, would double with node retry.
- **Single `ResilientAction` accessor** — rejected in favor of three separate capability interfaces
  (`TimedAction`/`RetriableAction`/`RecoverableAction`). Reason (user): a consumer's own action type
  can implement just ONE capability interface directly, which is cleaner than adopting a combined
  Policy accessor. Wrap still emits typed layers, canonical order, no double-stacking of a concern;
  the runtime aggregates via `ResolvePolicy`.
- **Reuse `model.RetryPolicy` in `action`** — impossible (import cycle). Chosen: a pure-data
  `action.RetryPolicy` + runtime converter (action stays a leaf, zero change to `model`). Alternative
  (extract `RetryPolicy` to a neutral leaf both packages import, `model.RetryPolicy` a type alias) was
  considered and NOT chosen.

## Quality attributes

- **Ergonomics (library-first):** resiliency is now declarable where the action lives, composably
  (`action.Wrap`), with sensible runtime fallback.
- **Consistency:** one execution site keeps spans/metrics/observability coherent; retry stays the
  single durable mechanism.
- **Low coupling:** `action` stays a leaf (pure-data policy); the engine gains one optional override
  field; the runtime owns the translation.
- **No double-application:** runtime runs the bare action under the *effective* merged policy.

## Testing strategy

TDD, observable RED first.

- **`action` unit:** `Wrap` merge + unwrap-idempotency (re-wrap doesn't nest; later option
  overrides same concern, distinct concerns nest); `ResolvePolicy` aggregates the chain; a custom
  type implementing ONE capability interface is detected; standalone `timedAction.Do`/
  `recoverableAction.Do` enforce timeout (context deadline) / recover (panic→error), `retriableAction`
  does NOT retry.
- **Catalog/Registry:** lazy default applied only when unset; per-action `Wrap` wins; bare passes
  through when no default.
- **Runtime e2e:** (a) action `WithExecTimeout` shorter/longer than the runtime default is respected
  (longer proves it's not just min()); (b) action `WithRetryPolicy` overrides a node-level
  `model.RetryPolicy` (action>node) and drives the durable retry; (c) `WithRecover(false)` lets a
  panic propagate as a (non-recovered) failure; (d) no policy ⇒ runtime defaults unchanged
  (regression).
- **Engine unit:** `effectiveRetryPolicy` override>node>default precedence table.

Coverage ≥ 85% on touched packages; `go test ./...` clean; `golangci-lint run ./...` clean.

## ADR

ADR-0126 (Nygard): Context = 3-locus runtime-only resiliency + the model→action cycle; Decision =
`action.{TimedAction,RetriableAction,RecoverableAction}` + `Wrap` + `ResolvePolicy` + `action.RetryPolicy` (declaration) + lazy catalog default
+ runtime effective-policy execution + `StepOptions.OverrideRetryPolicy` engine seam, precedence
action>node>default; Consequences = per-action resiliency, one execution site, durable retry intact,
one new optional engine field.

# 126. Per-action resiliency policy (timeout, recover, retry)

- Status: Accepted
- Date: 2026-07-11

## Context

Action-execution resiliency was enforced entirely in the **runtime**, in three separate loci,
with no way for a consumer to attach a policy to an individual action:

- **Timeout** — global only, via `WithActionTimeout(d)`; `actionContext` wrapped every `a.Do()` in
  `context.WithTimeout(driver.actionTimeout)`. No per-action or node-level timeout existed.
- **Panic recover** — always-on; `safeActionDo` recovered every panic into an error.
- **Retry** — engine-decided and **durable**. The runtime ran the action once; on failure it emitted
  `ActionFailed`, and the engine's `handleActionFailed`/`effectiveRetryPolicy` decided retry
  (precedence node > `StepOptions.DefaultRetryPolicy` > none) and scheduled a durable backoff
  (survives restart, integrates with incident/resume). Not an in-process loop.

The goal was to let a consumer declare a resiliency policy **where the action lives** — by wrapping
a bare action, or via a Catalog/Registry default — and have the runtime respect the per-action
policy, falling back to the runtime default when unset.

A hard constraint shaped the design: `definition/model` **imports** `action` (for
`ScopedCatalog() action.Catalog`), so `action` must **not** import `definition/model`. The action
package therefore cannot reference `model.RetryPolicy`.

## Decision

Add per-action resiliency to the `action` package with runtime fallback, keeping the single durable
retry mechanism and one execution site.

**`action` package (stays a leaf — no `definition/model` import):**

- `action.RetryPolicy` — pure-data struct mirroring the engine's four core retry fields
  (`MaxAttempts`, `InitialInterval`, `Multiplier`, `MaxInterval`). A declaration; the runtime
  converts it to `model.RetryPolicy` (which owns `Normalize`/`Backoff`).
- **Three single-method capability interfaces**, each embedding `Action`, so a consumer's own type
  may implement just ONE directly: `TimedAction{ ExecTimeout() time.Duration }`,
  `RetriableAction{ RetryPolicy() RetryPolicy }`, `RecoverableAction{ RecoverPanics() bool }`.
  Interface satisfaction signals the capability; the method returns its value.
- Three unexported wrapper types (`timedAction`, `retriableAction`, `recoverableAction`), each with
  its capability method + `Unwrap() Action` + `Do`. Timed and recoverable **self-enforce** in `Do`
  for standalone use; retriable's `Do` is **declarative** (delegates once — retry is engine-only).
  The bare action is a NAMED field (not embedded) so no capability is promoted across layers.
- `Option` (`func(*policy)`) with `WithExecTimeout`/`WithRetryPolicy`/`WithRecover`.
- `Wrap(a, opts...)`: unwraps `a` to bare while aggregating existing layers' capabilities, applies
  options (same-concern **override**, never double-stack), and rebuilds layers in canonical order
  recover→retry→timeout (innermost→outermost) for each set field. `Wrap(bare)` with no opts returns
  bare unchanged.
- `Unwrap(a)` (full unwrap to bare) and `ResolvePolicy(a) Policy` (walks the Unwrap chain,
  first-occurrence-per-concern wins; also detects a consumer type implementing a capability directly).
  `Policy{ Timeout *time.Duration; Retry *RetryPolicy; Recover *bool }` is the runtime-facing view.
- `NewCatalog(m, opts...)` and `NewRegistry(opts...)` store a **lazy default** applied at Resolve
  only to an action whose `ResolvePolicy` is all-nil; a per-action `Wrap` (or a custom capability
  type) always wins. Both remain back-compatible with no opts. `NewCatalog` now returns `Catalog`;
  with no opts it returns a bare `MapCatalog` (unchanged behavior), else a small `defaultingCatalog`
  decorator (the `MapCatalog` map type is kept so `builder.go`'s `map[string]Action` conversion is
  intact — no external code depended on the concrete return type).

**Engine seam (no `action` import):**

- `StepOptions.OverrideRetryPolicy *model.RetryPolicy`; `effectiveRetryPolicy` precedence becomes
  **override > node > `DefaultRetryPolicy` > none**.
- `FailingActionNode(def, st, commandID)` maps an `ActionFailed` command back to the failing node and
  its scope-effective definition, so the runtime can resolve the node's action.

**Runtime integration (single execution site):**

- Resolve the action; run the **bare** action once under the effective timeout
  (`Policy.Timeout` ?? `driver.actionTimeout`) and effective recover (`Policy.Recover` ?? true) — not
  the wrappers' `Do`, avoiding double-application and keeping spans/metrics at one site.
- `overrideRetryPolicy(def, st, trg)` — for an `ActionFailed` only — resolves the failing node's
  action, converts `action.RetryPolicy → model.RetryPolicy`, and sets `StepOptions.OverrideRetryPolicy`.
  It is re-derived from durable state each step, so a retry re-attempt after a restart resolves the
  same override. `InvokeCancelAction` honours the per-action timeout but always recovers (best-effort).

**Precedence:** timeout = action ?? runtime-default; recover = action ?? on; retry = action > node >
runtime-default.

## Consequences

- **Positive:** resiliency is declarable where the action lives, composably (`action.Wrap`) or via a
  catalog default, with sensible runtime fallback. The three separate capability interfaces let a
  consumer type opt into exactly one capability natively. Retry stays the single durable mechanism;
  one execution site keeps observability coherent. `action` stays a leaf (pure-data policy, no cycle).
- **Negative / trade-offs:** one new optional engine field (`StepOptions.OverrideRetryPolicy`); the
  runtime re-resolves the failing action on every `ActionFailed` step to re-derive the override (cheap
  map lookup; required for restart-durability). `WithRecover(false)` intentionally lets a panic
  propagate out of `Drive` — an explicit consumer opt-out of the crash-safety default. The retriable
  wrapper's `Do` does not retry (documented) — retry is only realized when the action is driven
  through the runtime.
- **Neutral:** `NewCatalog`'s return type widened from `MapCatalog` to `Catalog`; no caller depended
  on the concrete type. Rejected alternatives: a single combined `ResilientAction` accessor (three
  capability interfaces chosen instead); an in-process retry loop in the wrapper (non-durable, would
  double with node retry); extracting `RetryPolicy` to a shared leaf with a `model` type alias.

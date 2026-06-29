# 0076. Action-execution safety limits

Status: **Accepted — 2026-06-30.**
Design doc: `docs/specs/2026-06-30-action-safety-limits-design.md`.
Plan: `docs/plans/2026-06-30-action-safety-limits.md`.
Relates to: ADR-0048 (recover panics in action execution), ADR-0074 (retryable-action error contract),
ADR-0066 (optional clock).

## Context

The 2026-06-30 production-readiness audit found two unbounded-resource gaps in action execution:

- **`action/httpcall`** read the entire HTTP response with `io.ReadAll(resp.Body)` with no size
  bound. A large or malicious upstream response can exhaust memory and crash the replica, taking
  every in-flight instance with it. The streaming-`WithBodyFunc` validator buffer shared the same
  unbounded shape.
- **Service-action execution had no timeout.** `runtime.safeActionDo` recovers panics (ADR-0048) but
  a hung action (a blocking HTTP/SMTP/DB call that ignores nothing) stalls the instance and ties up
  the driving goroutine indefinitely.

The engine core (`engine/`, `model/`) must remain transport-free and wall-clock-free (ADR-0003). The
`clock.Clock` interface deliberately exposes only `Now()`, so it cannot drive a timeout.

## Decision

Add two additive functional options; engine/ and model/ stay zero-diff.

1. **`httpcall.WithMaxResponseSize(n int64)`** — bounds the response body (and any buffered request
   body) read into memory. Default **10 MiB** (`defaultMaxResponseSize`). A non-positive `n` disables
   the bound (explicit opt-out). Reads use `io.LimitReader(r, n+1)`; exceeding the bound returns
   `action.NonRetryable(ErrBodyTooLarge)` — a too-large response will not shrink on retry. New
   exported sentinel `httpcall.ErrBodyTooLarge`.

2. **`runtime.WithActionTimeout(d time.Duration)`** on `Runner` — bounds a single action invocation.
   Default **30s** (`defaultActionTimeout`); `d <= 0` disables (no deadline applied). When active, each
   `safeActionDo` call (`InvokeAction` and `InvokeCancelAction`) runs under a
   `context.WithTimeout`-derived context. The mechanism is the wall clock (`context.WithTimeout`),
   justified because `runtime` is not the engine core and already performs real I/O; `clock.Clock`
   has only `Now()`. A timed-out action's `Do` receives a cancelled context (httpcall honours it via
   `NewRequestWithContext`); the resulting error routes through the normal failure path as a
   **retryable** `ActionFailed` (a transient deadline may pass on retry, per the default-true
   `action.IsRetryable` contract).

Both defaults are **on**: the size cap actually closes the OOM vector for the common case, and a
30s action timeout protects against hangs out of the box. Both are overridable per the consumer's needs.

## Consequences

- **Behaviour change (default-on 30s action timeout):** a consumer with a legitimately >30s
  synchronous action must call `WithActionTimeout(d)` with a larger value or `WithActionTimeout(0)` to
  disable. Documented in CHANGELOG and godoc. The existing test suite uses fast actions; the full race
  suite (27 pkgs) stays green.
- **Behaviour change (default 10 MiB response cap):** responses larger than 10 MiB now fail
  `NonRetryable` unless `WithMaxResponseSize` raises or disables the cap. Acceptable pre-1.0.
- An action that does **not** honour context cancellation cannot be timed out — the timeout bounds the
  context, not the goroutine. Documented as a consumer responsibility (well-behaved actions respect ctx).
- The 30s timeout is wall-clock; determinism-sensitive tests are unaffected because they use fast
  actions, but a future clock-driven timeout would require extending `clock.Clock` (recorded as
  possible future work, not done here).

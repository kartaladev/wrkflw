# Design — Action-execution safety limits & security-linter rollout

**Date:** 2026-06-30
**Status:** Approved (design forks confirmed with maintainer)
**ADRs:** 0076 (action-execution safety limits), 0077 (security-linter adoption + nolint policy)
**Backlog items:** P0-4, P0-5, P1-D of `docs/plans/2026-06-30-production-readiness-backlog.md`

## Problem

Three safety gaps from the 2026-06-30 production-readiness audit:

- **P0-4** — `action/httpcall` reads the entire HTTP response with `io.ReadAll(resp.Body)`
  (`httpcall.go:289`) with no size bound. A large or malicious upstream OOMs the replica. The
  request-body validator buffer shares the same unbounded-read shape.
- **P0-5** — service-action execution has no timeout. `runtime.safeActionDo` (`runner.go:727`)
  recovers panics but a hung action (blocking HTTP/SMTP/DB) stalls the instance and ties up
  goroutines/connections indefinitely.
- **P1-D** — `.golangci.yml` runs only the `standard` linter set. Security-relevant linters
  (`gosec`, `bodyclose`, `errorlint`) are not enforced; `gosec` currently reports 36 findings.

## Decisions (maintainer-confirmed)

1. **httpcall cap: default ON, ~10 MiB, overridable.** Fixes the OOM by default (acceptable pre-1.0).
2. **Action timeout: default ON, 30s, overridable;** `WithActionTimeout(0)` disables.
3. **P1-D: folded into this track** — adopt the three linters and triage all findings here.

## Design

### P0-4 — httpcall response & validator size cap (ADR-0076)

- New option `WithMaxResponseSize(n int64)` on `action/httpcall`.
- New field `maxResponseSize int64`, initialised to `defaultMaxResponseSize = 10 << 20` (10 MiB) in
  `NewHTTPCall` **before** applying options. `WithMaxResponseSize(n)` sets it; `n <= 0` means unlimited
  (explicit opt-out).
- **Response path:** replace `io.ReadAll(resp.Body)` with a bounded read. When a cap is set, read via
  `io.LimitReader(resp.Body, max+1)`; if the result length `> max`, return
  `action.NonRetryable(errResponseTooLarge)` — a too-large response will not shrink on retry. New
  sentinel `ErrResponseTooLarge` (message prefixed `workflow-httpcall:`).
- **Validator-buffer path:** the streaming `WithBodyFunc` body is buffered for `WithBodyValidator`
  with the same cap; over-cap buffering returns `NonRetryable(ErrRequestBodyTooLarge)`.
- Engine/model untouched. Change confined to `action/httpcall`.

### P0-5 — action-execution timeout (ADR-0076)

- New option `WithActionTimeout(d time.Duration)` on `runtime.Runner`.
- New field `actionTimeout time.Duration`, initialised to `defaultActionTimeout = 30 * time.Second`
  in `NewRunner` before options. `WithActionTimeout(d)`: `d > 0` sets the timeout; `d == 0` disables
  (no timeout); negative is treated as disabled.
- A small helper wraps each `safeActionDo` call site (InvokeAction + InvokeCancelAction):
  when `actionTimeout > 0`, derive `actx, cancel := context.WithTimeout(parent, d)` and `defer cancel()`;
  otherwise pass the parent context through unchanged.
- **Mechanism = `context.WithTimeout` (wall clock).** `clock.Clock` exposes only `Now()`, so a
  clock-driven timeout would require extending a foundational locked interface — out of scope. The
  `runtime` package is not the engine core and already performs real I/O; the engine/model core stays
  wall-clock-free. The action's `Do` already receives and must honour `ctx` (httpcall uses
  `NewRequestWithContext`).
- On timeout the action returns a context error; for an awaited `InvokeAction` this routes through the
  existing failure path as a **retryable** `ActionFailed` (a transient deadline may pass on retry).
  `context.Canceled`/`DeadlineExceeded` are retryable by `action.IsRetryable` (default-true contract).
- Engine/model untouched.

### P1-D — security linters + finding triage (ADR-0077)

Add to `.golangci.yml` `linters`: `gosec`, `bodyclose`, `errorlint`. Exclude generated protobuf
(`transport/grpc/workflowpb`) — already excluded for the standard set; extend to gosec (`G103` there
is generated `unsafe`).

**gosec triage policy (36 findings):**

| Rule | Sites | Disposition |
|---|---|---|
| **G115** int→int16 | postgres/mysql `store.go`, `lister.go` (smallint columns) | **Real fix** where unbounded; **documented `//nolint:gosec` with bounds rationale** where the value is already clamped (e.g. page limits ≤ `NormalizeLimit` max). Each gets an inline reason. Behaviour-changing bounds checks are TDD'd. |
| **G115** int→int32 | `transport/grpc/server.go` (proto counts/limits) | Same: clamp + test where a real overflow is reachable; justified nolint where bounded upstream. |
| **G115** uint→int64 | `internal/expreval/expreval.go:174,182` | Inspect expr numeric coercion; justified nolint or guarded conversion. |
| **G404** math/rand | `runtime/jitter.go:16` | **Intentional** — retry-backoff jitter is not security-sensitive. `//nolint:gosec // G404: jitter is not security-sensitive`. |
| **G201/G202** SQL format/concat | `internal/persistence/mysql/{relay,lister,call_links}.go` | The int-only `LIMIT %d` (placeholder impossible alongside `FOR UPDATE`/locking). **Add constructor validation that batch/limit/fetch are non-negative ints** (TDD'd), then `//nolint:gosec` documenting the int-only invariant. |
| **G101** hardcoded creds | `internal/database/testutils_mysql.go:20` | Test-helper default DSN, LOW-confidence false positive. `//nolint:gosec`. |
| **G103** unsafe | generated `workflow.pb.go` | Exclude generated path. |

`bodyclose` / `errorlint` findings are unknown until enabled; triage on first run (close any unclosed
bodies; convert any `==`/type-assert error checks to `errors.Is`/`errors.As`; `%w` wrapping). Each
real finding is fixed; each false positive gets a justified `//nolint` with a one-line reason (per the
golang-security skill's "document the decision" rule).

## Testing

- **httpcall:** test a response larger than the cap → `ErrResponseTooLarge` (`errors.Is`) and
  `NonRetryable`; a response at/under the cap → success; `WithMaxResponseSize(0)` → unlimited read of a
  large body. Validator-buffer over-cap → `ErrRequestBodyTooLarge`. Table-test the boundary cases.
- **action timeout:** an action that blocks past a short configured timeout → awaited `InvokeAction`
  yields a **retryable** `ActionFailed`; a fast action under the timeout → completes normally;
  `WithActionTimeout(0)` → a slow action is not cancelled. Use short real durations (the timeout is
  wall-clock); no fake-clock dependency.
- **linters:** `golangci-lint run ./...` clean after the rollout; gosec 0 unjustified findings.
- Full `go test -race ./...` green (27 pkgs); touched packages ≥ 85%.

## Out of scope

- Extending `clock.Clock` with timer methods (would make the action timeout fake-clock-driven) —
  recorded as a possible future ADR, not done here.
- Circuit-breaker / rate-limit action decorators (separate P2 DX item).
- Per-action (vs per-Runner) timeout override.

## Risk

- The **default-on 30s action timeout is a behaviour change**: any consumer with a legitimately
  >30s synchronous action must call `WithActionTimeout(largerOrZero)`. Documented in CHANGELOG + godoc.
  The existing test suite uses fast actions, so no regression expected — verified by the full race suite.
- gosec bounds-check fixes touch grpc + persistence; each behaviour change is TDD'd and the full suite
  gates the branch.

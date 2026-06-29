# Built-in / template service actions — design

Date: 2026-06-29
Status: **Approved** (maintainer chose: all four actions; add retryable-error contract;
configurable key-mapping + interpolation; one subpackage per action — 2026-06-29).
Relates to: `action.ServiceAction` / `action.Catalog` (`action/action.go`), the runtime action
invocation (`runtime/runner.go`, `runtime/resolve_action.go`), the engine `ActionFailed` trigger
(`engine/trigger.go`), ADR-0063 (definition-scoped action catalog).

## Goal

Ship a small **standard library of ready-to-use service actions** so a consumer embedding the
engine does not have to hand-write the most common integrations. The two named by the maintainer
— **call a REST API** and **send an email** — plus two more broadly-useful actions (**transform
variables** and **log**) form a coherent first release. Each is a configured constructor returning
the existing `action.ServiceAction`, so it registers and resolves exactly like any consumer action
today (scoped or global catalog, `WithActionName`). The product is a library; these actions extend
its public, importable surface.

Everything builds on the **standard library plus the already-present `expr-lang/expr`** — no new
third-party runtime dependency — honouring the "actions are user-supplied; avoid dependency bloat"
stance the engine took until now (we now supply a curated few without bloating `go.mod`).

## Decisions

1. **Four actions, one public subpackage each** under `action/`:
   - `action/httpcall` — `NewHTTPCall(opts ...Option) action.ServiceAction` (built on `net/http`)
   - `action/email` — `NewEmail(opts ...Option) action.ServiceAction` (`net/smtp` + `text/template`)
   - `action/transform` — `NewTransform(opts ...Option) action.ServiceAction` (`expr-lang/expr`)
   - `action/logaction` — `NewLog(opts ...Option) action.ServiceAction` (`log/slog`)

   One subpackage per action isolates each one's options, dependency footprint, and tests, matching
   the project's small-well-bounded-unit principle and test-file-naming convention. (Rejected: a
   single `action/builtin` package — one import, but couples the four together and grows one option
   namespace.)

2. **Configurable key-mapping + interpolation for per-call values.** A constructor takes *static*
   config (base URL, SMTP creds, default headers/from). *Dynamic* per-call values are read from
   designated instance-variable keys (with sensible default key names) and may be interpolated
   against the variable map. `expr` is used for scalar/value expressions (URL, computed transform
   fields); `text/template` is used for free-form text bodies (email subject/body — the idiomatic
   fit, supports HTML and loops/conditionals). (Rejected: fixed hard-coded convention keys — simplest
   but rigid and prone to colliding with unrelated process variables.)

3. **Add a retryable-error contract honoured by the runtime.** Today `runtime/runner.go` returns
   `engine.NewActionFailedJittered(..., retryable=true, ...)` unconditionally — every action error
   retries. We add to the **`action` package**:

   ```go
   // Retryabler lets an action error state whether the runtime should retry it.
   type Retryabler interface {
       error
       Retryable() bool
   }

   // NonRetryable wraps err so the runtime will not retry the failed action.
   // The returned error unwraps to err (errors.Is/As see through it).
   func NonRetryable(err error) error
   ```

   The runtime default stays `true` (backward compatible — a plain `error` still retries). When the
   returned error satisfies `Retryabler` via `errors.As`, the runtime passes `retryable =
   err.Retryable()` into the existing `ActionFailed` trigger. The engine `ActionFailed` trigger
   **already carries `Retryable bool`** (`engine/trigger.go`), so **engine and model stay
   zero-diff**; the only core touch is the runtime's hardcoded `true` becoming an error inspection.

4. **Zero changes to engine core, model, or catalog.** The actions plug into the existing
   `ServiceAction` seam. The single core change is the runtime retry inspection in decision 3.

5. **Tracing seam via dependency injection, not a new direct dependency.** `httpcall` accepts
   `WithHTTPClient(*http.Client)` so a consumer can inject an otel-instrumented client; the action
   itself imports only `net/http`. The runtime already wraps each action call in a span, so timing
   is covered without the action importing OpenTelemetry.

## Per-action design

### `action/httpcall`

Static config: `WithBaseURL(string)`, `WithMethod(string)` (default: POST when a body is present,
else GET), `WithHeader(k, v string)` (repeatable, static default headers), `WithHTTPClient(*http.Client)`
(default: a client with a sane timeout). Dynamic: optional `url` / `method` / `body` input keys
(key names overridable); the URL supports `expr` interpolation over the variable map; a struct/map
`body` is JSON-encoded.

Output keys (overridable; defaults shown): `httpStatus int`, `httpBody any` (JSON-decoded when the
response is JSON, else the raw string), `httpHeaders map[string]string`.

Retry classification: response 4xx **except** 408 and 429 → `action.NonRetryable`; 5xx, 408, 429,
and transport/timeout errors → retryable (plain error).

### `action/email`

Static config: `WithSMTPAddr("host:port")`, `WithAuth(user, pass string)` (`net/smtp` PLAIN),
`WithTLS()` / `WithStartTLS()`, `WithFrom(addr string)`. Recipients (`to`/`cc`/`bcc`), subject, and
body come from config or designated input keys. Subject and body are `text/template` rendered over
the variable map; `WithHTML()` sets the `Content-Type` to `text/html`. Output: `emailSent bool`.

Construction (recipient list, MIME headers, template render) is split from the network send behind a
small internal `sender` seam so the message-building logic is unit-testable without SMTP, and the
real send is covered by one integration test.

### `action/transform`

Pure, no I/O. `Set(outKey, exprString)` (repeatable) compiles each `expr` expression once at
construction and evaluates it against the input variable map at run time, writing each result to its
`outKey`. Useful for reshaping/computing variables between nodes (totals, flags, projections) without
a bespoke closure. Because expressions are compiled eagerly, `NewTransform` can fail; its signature is
`NewTransform(opts ...Option) (action.ServiceAction, error)` so a malformed expression surfaces at
construction (wiring time), not mid-process. (The other three constructors cannot fail and return a
bare `action.ServiceAction`.)

### `action/logaction`

`WithLogger(*slog.Logger)` (default: `slog.Default()`), `WithLevel(slog.Level)`, `WithMessage(string)`,
`WithKeys(...string)` (which variables to include; default: all). Emits one structured log record and
returns the input variables unchanged (pass-through), so it is safe to drop onto any path and is an
ideal fire-and-forget action.

## File structure

```
action/retry.go                     Retryabler, NonRetryable          (+ retry_test.go)
action/httpcall/httpcall.go         NewHTTPCall, options              (+ httpcall_test.go, example_test.go)
action/email/email.go               NewEmail, options, sender seam    (+ email_test.go, email_integration_test.go, example_test.go)
action/transform/transform.go       NewTransform, Set                 (+ transform_test.go, example_test.go)
action/logaction/logaction.go       NewLog, options                   (+ logaction_test.go, example_test.go)
runtime/runner.go                    inspect Retryabler (the one core change)  (+ runner test for retry honouring)
```

## Testing

- **httpcall**: `httptest.Server` (stdlib). Table tests for status→output mapping, retry
  classification (4xx vs 5xx vs 408/429 vs transport error), JSON encode/decode, static + dynamic
  header injection, URL `expr` interpolation, and error paths.
- **email**: unit-test message construction / template render / retry classification against an
  injected fake `sender`; **one testcontainers integration test against mailpit** for a real SMTP
  round-trip (per `use-testcontainers` — heavy external resource is faithful, not mocked, behind a
  `RunTestMailpit`-style helper if none exists).
- **transform**: pure table tests (expression results, missing-variable handling, compile error).
- **logaction**: capture output with a test `slog.Handler`; table tests for level, message, key
  selection, and pass-through.
- **retry contract**: `action` unit test for `NonRetryable` + `errors.As`/`errors.Is` see-through;
  a `runtime` test asserting `ActionFailed.Retryable == false` when an action returns a `NonRetryable`
  error and `== true` for a plain error (regression guard for the default).
- Black-box `_test` packages; project `table-test` `assert`-closure form; a testable `Example` per
  action (these are consumer-facing API per the documentation rule).

TDD is strict: each new exported symbol gets a failing test (observable red) before implementation.

## ADRs

- **ADR-0074 — Retryable-error contract for service actions.** Context: the runtime treated all
  action errors as retryable; built-in actions need to signal non-retryable failures (HTTP 4xx).
  Decision: the `action.Retryabler` interface + `NonRetryable` wrapper; runtime honours it, default
  unchanged. Consequences: backward compatible; engine/model zero-diff; consumers can now mark their
  own errors non-retryable.
- **ADR-0075 — Built-in service-action catalog.** Context: consumers re-implement common
  integrations. Decision: ship `httpcall`, `email`, `transform`, `logaction` as public `action/*`
  subpackages on stdlib + expr-lang; configurable key-mapping + interpolation; deferred wrappers
  documented. Consequences: faster consumer onboarding; a maintained surface to evolve; deliberate
  scope boundary against vendor-specific connectors.

## Non-goals (deferred, as examples not core)

- Slack / Teams / generic outbound webhook — thin wrappers over `httpcall`; ship as `examples/` if
  demanded.
- Shell / command execution — security surface; out of scope.
- Standalone delay / sleep / publish-event actions — the engine already provides timers and the
  transactional outbox (`SendTask`); a duplicate action would invite divergence.
- Bulk "register all defaults" helper — each action needs per-consumer config (SMTP creds, which
  expressions), so there is nothing safe to register without configuration.

## Consequences

- Library ergonomics improve (the load-bearing priority): the four most-common integrations are
  importable and configured with functional options, registered through the existing catalog with no
  new wiring concept.
- One deliberate, backward-compatible core change (runtime retry inspection); engine and model stay
  zero-diff, consistent with the project's standing constraint to avoid unprompted engine changes.
- A new maintained surface (four packages + the retry contract) to keep tested and documented; each
  package is small and isolated, limiting the blast radius of future change.
- `go.mod` gains no new runtime dependency; the email integration test adds a test-only mailpit
  container.

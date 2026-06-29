# 0075. Built-in service-action catalog

Status: **Accepted — 2026-06-29.**
Design doc: `docs/specs/2026-06-29-builtin-service-action-catalog-design.md`.
Plan: `docs/plans/builtin-service-action-catalog.md`.
Relates to: ADR-0063 (definition-scoped action catalog), ADR-0074 (retryable-action error contract).

## Context

Consumers of the workflow engine repeatedly re-implement common integrations (REST calls, email notifications, data transformation, logging) as inline closures, then register them with a `ServiceAction` implementations or inline `WithAction` callbacks. The library provides no built-in actions, partly to avoid dependency bloat, but this forces every consumer to solve the same problems and leaves no standardized, tested, documented surface for these common patterns.

The engine core deliberately stays transport-agnostic and avoids pulling in heavy dependencies (HTTP clients, SMTP, web frameworks). However, the standard library now contains sufficient support (net/http, net/smtp, text/template, log/slog) for a lean set of common actions without adding runtime dependencies. A built-in catalog would accelerate consumer onboarding, provide a maintained and tested foundation, and establish scope boundaries against vendor-specific integrations.

## Decision

Ship four public `action/*` subpackages, each providing a configured constructor that returns a `action.ServiceAction`:

1. **`action/httpcall`** — `NewHTTPCall(opts...HTTPCallOption) ServiceAction`
   - Sends HTTP requests using stdlib `net/http`.
   - Per-call configuration via options: base URL, method, headers, query/form data, body, TLS client cert (optional).
   - Values interpolated from process/token variables using key-mapped expressions (`expr-lang` for scalar values; URL from `WithURLExpr` option).
   - Implements `Retryabler` (ADR-0074): HTTP 4xx (except 408/429) and 5xx (except 503/504) are non-retryable; connection errors and timeouts are retryable.
   - Response status and body available to downstream nodes via token variables (configurable key mapping).

2. **`action/email`** — `NewEmail(opts...EmailOption) ServiceAction`
   - Sends email via stdlib `net/smtp`.
   - Per-call configuration: recipient/sender (interpolated), subject, body template.
   - Body rendered via `text/template`, allowing structured token-variable substitution.
   - Pluggable `Sender` seam (`DefaultSMTPSender` implementation; tests use in-memory stubs).
   - Implements `Retryabler`: permanent SMTP errors (5xx except 421) are non-retryable.

3. **`action/transform`** — `NewTransform(opts...TransformOption) ServiceAction`
   - Transforms and enriches token variables using `expr-lang` expressions.
   - Per-call configuration: key-mapped expressions to compute new/updated variables (result stored back in token).
   - Pure computation; non-blocking; always succeeds (syntax errors caught at definition parse time).
   - Replaces ad-hoc inline closures for simple data manipulation.

4. **`action/logaction`** — `NewLog(opts...LogOption) ServiceAction`
   - Logs structured messages via stdlib `slog`.
   - Per-call configuration: log level, message template, key-mapped expressions for structured fields.
   - Non-blocking; always succeeds.
   - Aids observability and debugging during process execution.

**Deferred (documented, not shipped):** Slack/Teams/webhook (thin HTTP wrappers over httpcall), shell-exec (security concerns, non-portable), standalone delay/publish (engine timers + outbox SendTask already cover these).

**Tech-stack impact:** The `go.mod` file gains one new **test-only** dependency (`mailpit` container for email integration tests); all action implementations use only the standard library and the already-present `expr-lang/expr`.

**Public surface:** Constructors and option functions live in the `action/<subpackage>` namespace, consistent with the definition-scoped catalog pattern (ADR-0063). Examples in `examples/` demonstrate wiring these actions into a process definition.

## Consequences

- **Faster consumer onboarding:** Common integrations are available out-of-the-box, tested, and documented.
- **Maintained surface:** The action catalog evolves as a library concern, not reinvented per consumer.
- **No runtime bloat:** All four actions use only stdlib + `expr-lang` (already required).
- **Scope boundary:** The library does not ship vendor-specific connectors (Slack, Salesforce, etc.); consumers extend with their own `ServiceAction` implementations or inline callbacks.
- **Definition composition:** Actions compose cleanly with definition-scoped catalogs (ADR-0063), inline `WithAction` options, and error handling (retry contract from ADR-0074).
- **Future seams:** `Sender` interface in email and configurable HTTP client support extensibility without code changes.

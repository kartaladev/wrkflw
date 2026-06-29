# 0075. Built-in service-action catalog

Status: **Accepted — 2026-06-29.**
Design doc: `docs/specs/2026-06-29-builtin-actions-design.md`.
Plan: `docs/plans/2026-06-29-builtin-actions.md`.
Relates to: ADR-0063 (definition-scoped action catalog), ADR-0074 (retryable-action error contract).

## Context

Consumers of the workflow engine repeatedly re-implement common integrations (REST calls, email notifications, data transformation, logging) as inline closures, then register them with a `ServiceAction` implementations or inline `WithAction` callbacks. The library provides no built-in actions, partly to avoid dependency bloat, but this forces every consumer to solve the same problems and leaves no standardized, tested, documented surface for these common patterns.

The engine core deliberately stays transport-agnostic and avoids pulling in heavy dependencies (HTTP clients, SMTP, web frameworks). However, the standard library now contains sufficient support (net/http, net/smtp, text/template, log/slog) for a lean set of common actions without adding runtime dependencies. A built-in catalog would accelerate consumer onboarding, provide a maintained and tested foundation, and establish scope boundaries against vendor-specific integrations.

## Decision

Ship four public `action/*` subpackages, each providing a configured constructor that returns a `action.ServiceAction`:

1. **`action/httpcall`** — `NewHTTPCall(opts ...httpcall.Option) action.ServiceAction`
   - Sends HTTP requests using stdlib `net/http`.
   - Per-call configuration via options: `WithBaseURL`, `WithMethod`, `WithHeader`, `WithBodyKey`, `WithHTTPClient` (the tracing/customization seam), `WithOutputKeys`.
   - The request URL may be computed from instance variables with `WithURLExpr` (an `expr-lang` expression evaluated at execution time); the JSON request body is read from a designated input-variable key.
   - Retry classification per ADR-0074: HTTP 4xx **except 408 and 429** are non-retryable (`action.NonRetryable`); 408, 429, all 5xx, and transport/timeout errors are retryable.
   - Response status, decoded body, and headers are written back to instance variables via configurable output keys (`httpStatus`/`httpBody`/`httpHeaders` by default).

2. **`action/email`** — `NewEmail(opts ...email.Option) action.ServiceAction`
   - Sends email via stdlib `net/smtp`.
   - Per-call configuration: SMTP address, auth, from, recipients, subject and body templates, HTML toggle.
   - Subject and body are rendered via `text/template` (`missingkey=error`); rendered header values are CRLF-validated to prevent SMTP header injection.
   - **I/O recipients + per-recipient personalization:** `WithTo(...)` sets static recipients; `WithRecipientResolver(func(ctx, vars) ([]Recipient, error))` resolves recipients at send time (e.g. a DB lookup). Each `Recipient{Address, Data}` gets an **individual** message whose templates render with `Data` overlaid on the instance variables (recipients do not see each other). Sending is best-effort across the list: failures are aggregated (`errors.Join`, retryable — at-least-once, so retries may resend); a resolver may return `action.NonRetryable`; an empty list is non-retryable.
   - **Real TLS:** `WithStartTLS()` enforces STARTTLS (errors if the server does not advertise it — never silently plaintext); `WithTLS()` uses implicit TLS (`tls.Dial`); `WithTLSConfig(*tls.Config)` customizes either. Pluggable sender seam (unexported `sender` interface; default `smtp.SendMail`-backed; tests/consumers inject via the exported `SenderFunc` + `WithSender`, which overrides the TLS modes).
   - Retry classification: template/render and header-validation errors are non-retryable; send-level (SMTP/transport) errors are retryable.

3. **`action/transform`** — `NewTransform(opts ...transform.Option) (action.ServiceAction, error)`
   - An **I/O-capable enricher**, not a pure mapper. `WithMapper(func(ctx, vars) (map[string]any, error))` performs I/O (e.g. a database lookup) to enrich the action's internal working set; `WithExpr(outKey, exprStr)` evaluates an `expr-lang` expression. Stages run in order and chain (later stages see earlier results).
   - **Scratch-vs-persisted invariant:** `WithMapper` enrichment is action-local scratch — available to later stages but **never returned as process variables**. Only `WithExpr` results are persisted; to persist a fetched value, project it explicitly (`WithExpr("k", "k")`). This keeps bulky/sensitive/transient lookup data out of the persisted instance state.
   - `WithExpr` expressions compile eagerly (malformed → `NewTransform` error); a nil `WithMapper` is rejected at `NewTransform`; runtime evaluation/mapper errors (which may be `action.NonRetryable`) surface from `Do`.

4. **`action/logaction`** — `NewLog(opts ...logaction.Option) action.ServiceAction`
   - Emits one structured `slog` record of selected instance variables (`WithLogger`/`WithLevel`/`WithMessage`/`WithKeys`).
   - Pass-through: returns the input variables unchanged and never errors, making it safe on any path (including fire-and-forget).
   - Aids observability and debugging during process execution.

**Deferred (documented, not shipped):** Slack/Teams/webhook (thin HTTP wrappers over httpcall), shell-exec (security concerns, non-portable), standalone delay/publish (engine timers + outbox SendTask already cover these).

**Tech-stack impact:** The `go.mod` file gains one new **test-only** dependency (`mailpit` container for email integration tests); all action implementations use only the standard library and the already-present `expr-lang/expr`.

**Public surface:** Constructors and option functions live in the `action/<subpackage>` namespace, consistent with the definition-scoped catalog pattern (ADR-0063). Each subpackage ships a testable `Example`, and `examples/builtin_actions/` is a runnable reference wiring of all four actions end-to-end.

## Consequences

- **Faster consumer onboarding:** Common integrations are available out-of-the-box, tested, and documented.
- **Maintained surface:** The action catalog evolves as a library concern, not reinvented per consumer.
- **No runtime bloat:** All four actions use only stdlib + `expr-lang` (already required).
- **Scope boundary:** The library does not ship vendor-specific connectors (Slack, Salesforce, etc.); consumers extend with their own `ServiceAction` implementations or inline callbacks.
- **Definition composition:** Actions compose cleanly with definition-scoped catalogs (ADR-0063), inline `WithAction` options, and error handling (retry contract from ADR-0074).
- **Future seams:** `Sender` interface in email and configurable HTTP client support extensibility without code changes.

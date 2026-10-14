# Design

## Context

See proposal.md, *Why*, for the measurements. Two facts shape the approach:

- `httpcore` already imports no framework. The endpoint functions (`StartInstance`, `GetInstance`, the admin endpoints) are framework-neutral. What each adapter repeats is the per-route plumbing around them: read path and query values, read and decode the body, resolve identity, build the disclosure, call the endpoint, encode the result, and classify errors. That plumbing is 402, 444 and 420 non-comment lines in the three `groups.go` files.
- The adapter suites (2,600 to 2,900 lines each) and `transport/http/parity` are the safety net. Each existing divergence is pinned by a named test: `TestParity_MaxBodyBytes_UnderCap` (trailing bytes), `TestParity_WithMiddleware_ScopeAndMultiplicity` (scope), the `noBodyParity` row of `TestParity_ErrorEnvelopes` (error prose) and `TestFiberDivergence_AboveFiberConfigBodyLimit` (fasthttp `BodyLimit`).

The shape was decided on kartaladev/wrkflw#23 and amended by its post-decision adversarial audit. This design records those decisions with their evidence and does not reopen them. Only the questions #23 left open go to Open Questions.

## Goals / Non-Goals

**Goals:**
- One place for every per-route behavior, so a new adapter cannot drift and an existing one cannot either.
- Byte-level parity across adapters, checked by one table-driven test over every route and binding.
- Consumer call sites compile unchanged: route-group types, `Mount`, `MountHealth` and the option functions keep their signatures.

**Non-Goals:**
- Moving bindings into their own modules (#30, decided; owned by `split-persistence-modules`).
- Changing any route's path, method, success status or response shape.
- Refusing unknown request fields. Measured: `{"bogus":1}` is accepted everywhere. That is a separate decision.
- Removing either fasthttp divergence. Both are framework facts and stay documented.

## Decisions

**D1. `Route` as data, a `Binding` per framework, one `Serve`** (decided on #23; amended by its audit).
`httpcore` declares `Route{Method, Path, Body, Identity, Disclose, Query, Handle}`. `Path` uses `{param}` syntax; each binding translates it to its framework's syntax. `Body` is one of `BodyNone`, `BodyRequired` or `BodyOptional`. `Handle` is a closure built per route group (`InstanceRoutes(svc)`, `TaskRoutes(svc)`, `MessageRoutes(svc)`, `AdminRoutes(deps)`, `HealthRoutes(checks)`), not a function taking `service.Service`. The audit found the admin and health routes need `DeadLetters`, `RelayStats`, `Timers`, `Lineage` and health checks, so a fixed `service.Service` parameter was the wrong shape. `Binding` has six methods: `Param`, `Query`, `Context`, `ReadBody(max, timeout) ([]byte, error)`, `Write(status int, contentType string, body []byte)` and `SetHeader`.
- Rejected: keep per-adapter route code and add Fiber v2 (`add-fiber-v2-adapter`) as a fourth transcription. #23 measured every documented divergence but the two fasthttp ones as transcription drift, so a fourth copy is a fourth source of it.
- Rejected: `Binding.Write(status, body any)`. It would let each framework keep its own encoder (fiber's is configurable to sonic or goccy), so byte parity would stay impossible (audit, #23).

**D2. One encoder; `Content-Type: application/json`.**
`Serve` encodes with `encoding/json` into a buffer and hands the bytes to `Binding.Write`. The media type is `application/json` with no charset: RFC 8259 §11 registers no `charset` parameter for it, so gin's and fiber's `; charset=utf-8` carries no information. The body has no trailing newline. stdlib's newline comes from `json.Encoder`, and dropping it is what lets a parity test compare raw bytes, which #40 asked for.
- Rejected: keep `json.Encoder` and its newline. Measured on main: it is the only reason raw bodies differ on every JSON response (parity_test.go file comment).

**D3. Marshal before commit** (kartaladev/wrkflw#205; the failure response is pending OQ1).
Because D2 encodes into a buffer before `Write`, a marshal failure happens while nothing has reached the client. That makes #205's two cases separable by construction: a marshal failure can still be answered, and a write failure after commit cannot. Measured on main with a failing mapper: stdlib 201 empty, gin 201 empty, fiber 500 `text/plain` with the raw error text. The #205 lead that gin panics is stale for gin v1.12.0, whose `Context.Render` records the error and aborts.

**D4. One decoder: exactly one JSON value, then EOF** (decided on #23).
`Serve` decodes the bytes `ReadBody` returned with a `json.Decoder`, then requires that the next token read returns `io.EOF`. Trailing JSON whitespace passes; anything else is `ErrBadInput` (400). No framework decoder is used on any binding (fiber v3 `Bind().JSON`, gin `ShouldBindJSON`; `add-fiber-v2-adapter` likewise never calls v2 `BodyParser`), so the divergence cannot come back through one (audit, #23). The decode error is wrapped once, by `httpcore`, so the "bind from body" prefix fiber adds today disappears. What the `def_ref` message says is owned by `improve-wire-gate-diagnostics`.

**D5. Identity before body, once, in `Serve`** (decided on #23, "held" in its audit).
A `Route` with `Identity: true` resolves identity before `ReadBody` is called. That moves the ordering from nine adapter call sites into one. The existing `human-task-authorization` requirement *Identity is refused before the request body is read*, with its per-adapter 401-before-413 tests, is the pin and does not change.

**D6. Query parsing in `httpcore`** (decided on #23; the refusal policy is pending OQ2).
`Route.Query` names the parameters a route reads. `Serve` parses `limit`, `cursor`, `status` and `total` once. Measured: the negative-limit divergence #40 cites is unobservable, because every listing endpoint clamps through `kernel.NormalizeLimit`. But a non-integer `limit` and an unrecognized `total` are silently defaulted on all three adapters, while `?status=bogus` is refused 400. That inconsistency is what OQ2 decides.

**D7. Route-scoped `WithMiddleware` on every binding** (decided on #23 and #40).
The binding registers each route with the consumer's middleware ahead of the library handler. gin and fiber accept a handler chain per route, and stdlib wraps the `http.Handler` (audit, #23: "held"). stdlib's option is `WithMiddleware(...func(http.Handler) http.Handler)`, first listed outermost. Middleware runs before identity resolution and the body read (spec *Consumer middleware runs before identity resolution and the body read*), so a middleware that puts an actor on the context keeps working. The fiber memo and its map in `transport/http/fiber/options.go` are deleted, as #40's coordinator note asked. `fiber.WithMiddleware` keeps its name: #74 deliberately left it unrenamed until this change made it accurate. A consumer short-circuit still carries no nosniff unless `NosniffMiddleware` is listed first. That is the shipped requirement *The library does not inject into the consumer's handler chain*, unchanged.

**D8. fiber `WithBodyReadTimeout`** (decided on #40).
It is exported for symmetry with stdlib and gin. fasthttp reads the body before any handler runs, so a per-route read deadline cannot be applied. The option is documented as inert and logs one WARN at mount when set to a positive duration.

**D9. Migration order** (decided in #23's audit).
stdlib first, with its full suite and the parity package green against the still hand-written gin and fiber. Then gin, then fiber. Never all three at once. Each divergence row flips in the commit that removes the divergence, not before.

### Cross-change coupling

| Change | Shared surface | Order |
|---|---|---|
| `define-access-and-disclosure` | the three `groups.go` and `httpcore` route wrappers. It adds `Cache-Control: private, no-store` to the eleven disclosing routes per adapter, and the actor-to-context step on hooked routes. | Either order. Its design already says whichever lands second moves the directive into the route-as-data path. If this change lands first, the directive becomes one line in `Serve` keyed on `Route.Disclose`. |
| `improve-wire-gate-diagnostics` | the `def_ref` 400 message (#235). It also rewrites `transport/http/gin/gin_bodycap_test.go` so it no longer pins stdlib text, and both changes retire the `noBodyParity` row. | Either order. Whichever lands second rebases the row deletion and the gin test. That change's spec requires the `def_ref` message to be identical across adapters, which D4 delivers structurally. |
| `replace-casbin-with-composed-deciders` | the six `/admin/policies` and `/admin/role-bindings` routes and the `Policies` field of `AdminRoutes`. | Either order. If casbin lands first, the table starts at 20 routes; otherwise it deletes six `Route` entries instead of six hand-written blocks. |
| `bound-untrusted-input` | `httpcore/errors.go` gains a 413 classification. | Independent. Classification already lives in `httpcore`. |
| `split-persistence-modules` | module placement of `gin` and `fiber` (#30), and where `transport/http/parity` goes (its Q1). | This change lands first (it is in Blocks); the split moves finished bindings, as #40 decided ("do the table refactor on the single module first; #39/#30 move finished bindings"). |
| `add-fiber-v2-adapter` | `Binding`, `Serve`, the route groups and the parity table, which the v2 binding consumes. | This change lands first (it is in Blocks). The v2 parity rows follow this change's OQ2 ruling. |

## Risks / Trade-offs

- [78 handlers rewritten in one change] → D9's per-adapter order, with the other adapters' hand-written routes as the oracle at each step. The Fiber v2 binding is split out to `add-fiber-v2-adapter`.
- [Byte-level breaking changes (newline, charset, error prose) surprise a consumer matching raw text] → called out as BREAKING in the proposal. The decoded documents are unchanged except error prose.
- [fiber consumers lose app-wide middleware coverage] → flagged as a security-posture change. The godoc on `fiber.WithMiddleware` and the release notes say to use `app.Use` for app-wide middleware.
- [`Serve` becomes a hot path for every request] → it does one buffer encode, as gin and fiber already do. A benchmark task compares `POST /instances` allocations before and after on each adapter.
- [A per-route middleware chain changes `observe` instrumentation boundaries] → the existing observability tests per adapter stay unchanged and must pass.

## Migration Plan

Consumers change nothing at call sites, with three exceptions: a fiber consumer relying on `WithMiddleware` for app-wide coverage moves that middleware to `app.Use`; a consumer that authored a `CustomizeOption` against `Wrap` follows OQ4's outcome; and a consumer matching error prose or raw bytes updates its matchers. Rollback is a revert. No data or migration is involved.

## Open Questions

None of these is decided on #23, #40 or #205. Each changes a requirement marked pending, so `Readiness: human`.

- **OQ1 (kartaladev/wrkflw#205): what does the single encoder do with a marshal failure?**
  - (a) Answer the 500 `internal_error` envelope with no message, log at ERROR, and never render the error text. **Recommended:** nothing has been written yet (D3), so the response is still controllable. It matches *Error responses use one client-safe envelope*, and it ends fiber's leak.
  - (b) Keep the route's success status with an empty body, as stdlib and gin do today. Rejected by the project rule that silence is never correct.
  - (c) Panic, so the consumer's recovery middleware answers. Loud, but every adapter's recovery writes a different body, which breaks parity again.
  - For a write failure after commit, all options log and do not rewrite.
- **OQ2: what does a malformed paging parameter get?** #23 ruled that "a negative limit gets one answer" without saying which.
  - (a) Refuse a `limit` that is not a base-10 integer of at least 1, an empty `limit=`, and a `total` outside `true`, `false`, `1` and `0`, with 400 `bad_request`. Absent means the default; above the maximum clamps. **Recommended:** it matches `?status=bogus`, which is already refused, and the silence rule.
  - (b) Keep `kernel.NormalizeLimit`'s defaulting and only move the parsing. Parity holds, but `?limit=abc` stays a silent default.
  - (c) As (a), but treat `limit=0` as the default. That keeps a value some clients may send to mean "server default".
- **OQ3: a malformed non-empty body on an optional-body route.** Measured: incident-resolve with `{"add_attempts":"three"}`, `not json` or `{} xx` proceeds as if no body were sent, on all three adapters. The same happens on claim.
  - (a) Refuse with 400 `bad_request`; an empty or whitespace-only body stays absent. **Recommended:** `add_attempts:"three"` silently becoming 0 is exactly a decay to a zero value.
  - (b) Keep ignoring it, and document that. It keeps the code comment's intent ("Body is optional — ignore decode errors"), but leaves the silence.
- **OQ4 (#40 coordinator note): the contract of `CustomizeConfig.Wrap` and `WithRouterFunc`.** No spec depends on it.
  - (a) Remove both. **Recommended:** their only in-tree use is gin's `WithMiddleware`, which D7 replaces. On fiber, `Group` registers handlers, so no single "call count" contract holds across frameworks.
  - (b) Keep them, documented as called once per route-group registration and required to be side-effect free. That keeps a seam that is unsafe on fiber by construction.

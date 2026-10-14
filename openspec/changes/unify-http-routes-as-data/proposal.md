# Proposal

## Why

The three HTTP adapters each transcribe the same 26 routes by hand, and the transcriptions drift: they disagree on trailing bytes after a JSON body, on the scope of `WithMiddleware`, on the bytes and `Content-Type` of every JSON response, and on what a response that cannot be encoded produces. That last one ranges from a success status with an empty body to a 500 that leaks the raw error text. kartaladev/wrkflw#23 (decided) settled the cure: the route table becomes data in `httpcore`, each framework supplies a small binding, and one generic serve path decodes, orders identity before the body, encodes and writes errors exactly once. Fiber v2 then becomes a fourth binding rather than a fourth transcription; that binding is `add-fiber-v2-adapter`.

Every premise below was re-measured on `eebe55ba` with go1.26.9. The probes ran as scratch tests in `transport/http/parity` through `go test -overlay`; nothing was written to the tree.

| Premise | Measurement on main |
|---|---|
| #205: stdlib turns a marshal failure into a success status with an empty body | **Holds.** With an instance mapper whose value fails to marshal, `POST /instances` answers stdlib **201**, empty body, `Content-Type: application/json`, nothing logged. `transport/http/stdlib/write.go:32` commits the status and `:34` discards the encode error. |
| #205 lead: "gin panics, fiber returns the error" | **Stale for gin, and fiber is worse than stated.** gin v1.12.0 records the error and aborts, so it answers **201 with an empty body**, the same as stdlib. fiber v3.5.0 hands the error to the app's default `ErrorHandler`, which answers **500 `text/plain`** whose body is the raw marshal error text. That breaks the shipped rule that a 5xx carries no message. |
| #40: trailing bytes after a JSON value are admitted by stdlib and gin, refused by fiber | **Holds.** `TestParity_MaxBodyBytes_UnderCap` pins stdlib 201, gin 201, fiber 400. |
| #40: fiber drops a negative `?limit` that stdlib and gin pass through | **Holds in code, stale as an observable divergence.** `fiber/groups.go:307,382,526` drop `lim <= 0`, but `httpcore` clamps every limit through `kernel.NormalizeLimit`, which maps `n <= 0` to 50. `?limit=-5`, `?limit=0` and `?limit=abc` answer the same 200 page on all three adapters. All three also silently ignore a non-integer `limit` and a `total` that is neither `true` nor `1`, while `?status=bogus` is refused 400 on all three. |
| #40: `WithMiddleware` is object-scoped on gin, app-wide on fiber, absent on stdlib | **Holds.** `TestParity_WithMiddleware_ScopeAndMultiplicity` pins a consumer route at 0 runs on gin and 1 on fiber, and an unrouted path at 1 on fiber. stdlib has no `WithMiddleware`. |
| #40: fiber has no `WithBodyReadTimeout` | **Holds.** |
| #40: three error messages for one bad JSON body | **Holds.** `TestParity_ErrorEnvelopes` marks its malformed-`def_ref` row `noBodyParity`. |
| #40: `httpcore/headers.go:16-18` says "27 stdlib routes"; the count is 26 | **Holds.** 26, health probes included, of which six are the policy routes `replace-casbin-with-composed-deciders` deletes. |
| #40/#23 figures: 1,045 code lines in the three `groups.go`; gin and fiber decode inline 11 and 10 times; parity is 1,822 test lines; `c.Context()` at 44 sites | **Drifted.** 1,266 non-comment lines (402, 444, 420); 11 and 12 decode sites; 2,454 parity test lines in four files; 35 `c.Context()` sites in the v3 adapter's non-test files. |
| Not in either issue | **New.** Every JSON response differs in bytes across adapters: stdlib appends a newline and sends `application/json`; gin and fiber send `application/json; charset=utf-8`. A malformed non-empty body on an optional-body route (incident resolve, claim) is silently treated as empty on all three adapters. |
| #40 item 9: bump fiber v3 to v3.5.0 | **Stale.** go.mod already pins v3.5.0. (The item's v2.52.15 pin moved with the Fiber v2 adapter to `add-fiber-v2-adapter`.) |

## What Changes

- **Route table as data, one serve path** (kartaladev/wrkflw#40 part 1, absorbed; design per kartaladev/wrkflw#23, decided). `httpcore` gains `Route` values grouped per route group, each `Handle` a closure over its own dependencies, a six-method `Binding` per framework, and one generic `Serve`. `Serve` does decoding, identity-before-body ordering, disclosure, instrumentation, JSON encoding and the error envelope once. The adapters shrink to bindings. Their exported route-group types, `Mount`, `MountHealth` and option functions stay as thin wrappers, so consumer call sites compile unchanged. The parity package becomes one table-driven check over every binding and every route, plus the two fasthttp divergences.
- **One encoder: byte-identical responses on every adapter** (kartaladev/wrkflw#40, absorbed). Every JSON body is the same bytes on every adapter, with `Content-Type: application/json`. **BREAKING** at the byte level: stdlib loses its trailing newline, and gin and fiber lose `; charset=utf-8`.
- **A response that cannot be encoded is a 500, never a silent success** (kartaladev/wrkflw#205, absorbed; **pending OQ1**). Recommended: encode before the status is committed. A marshal failure answers the client-safe 500 envelope on every adapter and logs the error at ERROR. A write that fails after the status is committed is logged, not rewritten. This closes fiber's disclosure of the raw error text.
- **A JSON body is exactly one value** (kartaladev/wrkflw#40; the policy is decided on #23). Trailing non-whitespace bytes answer 400 `bad_request` on every adapter, and malformed JSON gets one message on every adapter. **BREAKING** for stdlib and gin callers who send trailing bytes.
- **Query parameters parse once, in `httpcore`** (kartaladev/wrkflw#40; **pending OQ2**). `limit`, `cursor`, `status` and `total` are parsed once for every adapter. Recommended: a `limit` that is not an integer of at least 1, or a `total` that is not `true`, `false`, `1` or `0`, is refused 400 `bad_request`, as `?status=bogus` already is. Absent means the default; a limit above the maximum is clamped as documented. **BREAKING** for callers sending those values.
- **A malformed optional body is refused, not ignored** (found while measuring; **pending OQ3**). Recommended: on an optional-body route, an empty body is still accepted, but a non-empty body that is not one valid JSON value of the expected shape answers 400 `bad_request`, as on a required-body route. **BREAKING** for callers relying on the silent discard.
- **`WithMiddleware` is route-scoped on every adapter, stdlib included** (kartaladev/wrkflw#40; decided on #23). The binding prepends the middleware to each library route. It runs once per request a library route answers, and never for a consumer's own route or an unrouted path. stdlib gains `WithMiddleware(func(http.Handler) http.Handler)`. The fiber memo and its map are deleted, as #40's coordinator note asked. The name `WithMiddleware`, deliberately not renamed on kartaladev/wrkflw#74, is now accurate on every adapter. **BREAKING, default security posture flagged:** on fiber, a consumer relying on `WithMiddleware` to cover their own routes or unrouted paths loses that coverage.
- **`CustomizeConfig.Wrap` gets a contract or goes** (#40 coordinator note; **pending OQ4**). Recommended: remove `Wrap` and `WithRouterFunc`, because route-scoped middleware replaces their only in-tree use and no router transform has one call count across frameworks. **BREAKING** for consumers who author a `CustomizeOption` against `Wrap`.
- **fiber `WithBodyReadTimeout` is exported for symmetry** (kartaladev/wrkflw#40; decided). It is documented as inert on fasthttp and logs one WARN when mounted. The fasthttp `BodyLimit` divergence stays, with its requirement and test.
- **Persisted formats:** none. No snapshot, journal, outbox, column or migration changes.
- **Consumer go.mod:** none. No dependency is added or removed; the Fiber v2 dependency went to `add-fiber-v2-adapter` with the adapter.
- **Default security posture:** tightened in two places (fiber no longer leaks a marshal error's text; trailing bytes are refused everywhere) and narrowed in one (fiber `WithMiddleware` scope, above).

**Absorbed issues**
- kartaladev/wrkflw#40: part 1 absorbed. Part 2, the Fiber v2 adapter, moved to `add-fiber-v2-adapter`. Item 9's v3 bump is stale (already done), and several of its figures drifted (table above).
- kartaladev/wrkflw#205: absorbed. Its gin lead is stale; its stdlib claim holds; fiber was found worse than stated.
- kartaladev/wrkflw#23: the decision issue (closed, decided). It is cited, not reopened.

**Out of scope**
- The Fiber v2 adapter (kartaladev/wrkflw#40 part 2): `add-fiber-v2-adapter`, which depends on this change and on `split-persistence-modules`.
- Moving each binding (`gin`, `fiber`) into its own module, decided on kartaladev/wrkflw#30: `split-persistence-modules`, which depends on this change.
- The `Cache-Control` directive and the actor-to-context step on disclosing routes: `define-access-and-disclosure`. The two changes share the adapter route files; the landing order is in design.
- Naming `def_ref` in its 400 (#235): `improve-wire-gate-diagnostics`. This change makes every message identical across adapters; that change decides what the `def_ref` message says.
- Deleting the policy administration routes: `replace-casbin-with-composed-deciders`.
- The variables-limit 413: `bound-untrusted-input`. It classifies in `httpcore`, so it needs no per-adapter work here.
- Refusing unknown fields in request bodies. Measured: `{"bogus":1}` is accepted by every adapter. No change owns it; recorded as a finding.

**Coordination**
- Size: L
- Depends on: nothing
- Blocks: split-persistence-modules, add-fiber-v2-adapter
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `http-transport`:
  - MODIFIED *WithMiddleware runs consumer middleware once per request*: route-scoped on every adapter.
  - ADDED *Every adapter writes the same response bytes*.
  - ADDED *A response that cannot be encoded answers 500* (pending OQ1).
  - ADDED *A JSON request body is exactly one value*.
  - ADDED *A malformed JSON body is refused with one message on every adapter*.
  - ADDED *Paging query parameters are refused rather than defaulted when malformed* (pending OQ2).
  - ADDED *A malformed body on an optional-body route is refused* (pending OQ3).
  - ADDED *Consumer middleware runs before identity resolution and the body read*.
  - ADDED *fiber accepts WithBodyReadTimeout and reports it inert*.

## Impact

- **Code:** `transport/http/httpcore` (new route table, `Binding`, `Serve`, encoder, decoder, query parsing; `seam.go`; `headers.go`); `transport/http/{stdlib,gin,fiber}` (each shrinks to a binding plus wrappers; `groups.go`, `write.go`, `body.go`, `bodycap.go`, `bodylimit.go`, `observe.go`, `options.go`).
- **Tests:** the three adapter suites keep their assertions except where a divergence is deliberately removed. `transport/http/parity` becomes table-driven over all bindings; the trailing-bytes, `noBodyParity` and middleware-scope divergence rows flip to parity.
- **Public API:** route-group types, `Mount`, `MountHealth` and options keep their signatures; stdlib gains `WithMiddleware`, fiber gains `WithBodyReadTimeout`. `httpcore.RouteCustomizer`, `CustomizeConfig` and `ResolveConfig` may change shape (BREAKING for consumers who author options against them).
- **Dependencies:** none added or removed.

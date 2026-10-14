# Proposal

## Why

Consumers still on Fiber v2 cannot mount wrkflw's HTTP routes: the `fiber` adapter targets Fiber v3, and the two majors are different modules with different handler and context types. kartaladev/wrkflw#40 (part 2) asked for a v2 adapter. kartaladev/wrkflw#23 (decided) ruled that it comes only after the route table becomes data, so that v2 is a fourth binding over one serve path instead of a fourth hand transcription of 26 routes. `unify-http-routes-as-data` builds that table and its `Binding` seam; this change adds the v2 binding on top of it.

Premises re-measured on `eebe55ba` with go1.26.9:

| Premise | Measurement on main |
|---|---|
| #40: pin Fiber v2 at v2.52.15 | **Holds.** `go list -m -versions github.com/gofiber/fiber/v2` lists v2.52.15 as the newest release. The local module cache tops out at v2.52.13, so the first build fetches it. |
| #23: a copy of the v3 binding compiles on v2 and silently loses consumer context | **Holds by construction.** In v2, `c.Context()` returns `*fasthttp.RequestCtx`, which satisfies `context.Context`, so a v3-style call compiles and drops every value middleware set through `SetUserContext`. The v3 adapter's non-test files contain 36 lines calling `.Context()`, each one a site that would carry the trap if copied. |
| The root module requires no Fiber v2 | **Holds.** Root `go.mod` requires only `github.com/gofiber/fiber/v3 v3.5.0`. |

## What Changes

- **Fiber v2 adapter** (kartaladev/wrkflw#40 part 2, absorbed; design decided on kartaladev/wrkflw#23). New `transport/http/fiberv2` is a `Binding` over `*fiber.Ctx` for the route table `unify-http-routes-as-data` introduces. It reads and writes the request context only through `UserContext`/`SetUserContext`, never `c.Context()`, and hands `c.Body()` bytes to the shared serve path with no framework decoder. Its route-group types, `Mount`, `MountHealth` and options mirror the v3 adapter's, so it answers every route with the same status and bytes as stdlib.
- **The fasthttp `BodyLimit` divergence applies to v2 too.** A body above the v2 app's own `BodyLimit` is refused by the framework with no wrkflw envelope, as already specified for the v3 adapter.
- **Its own module from birth** (kartaladev/wrkflw#30, decided: each HTTP binding is its own module). Because it lands after `split-persistence-modules`, `transport/http/fiberv2` gets its own `go.mod` and never enters the root module.
- **Consumer go.mod (flagged).** A consumer who wants the v2 adapter newly requires `github.com/kartaladev/wrkflw/transport/http/fiberv2`, which brings `github.com/gofiber/fiber/v2 v2.52.15` and fasthttp. The root module's `go.mod` does not change, and a consumer who does not import the adapter gets no Fiber v2 in its build.
- **Persisted formats:** none.
- **Default security posture:** unchanged. The adapter inherits the shared serve path's posture (client-safe envelope, identity before body, route-scoped `WithMiddleware`).
- Not BREAKING: purely additive.

**Absorbed issues**
- kartaladev/wrkflw#40: part 2 (the Fiber v2 adapter) only. Part 1, the route table and the parity fixes, stays in `unify-http-routes-as-data`.
- kartaladev/wrkflw#23: the decision issue (closed, decided). It is cited, not reopened.

**Out of scope**
- The route table, `Binding`, the shared serve path, and every parity fix: `unify-http-routes-as-data`.
- Moving the existing `gin` and `fiber` bindings into modules, and deciding where `transport/http/parity` lives (its Q1): `split-persistence-modules`.
- Any change to the v3 `fiber` adapter.

**Coordination**
- Size: M
- Depends on: unify-http-routes-as-data, split-persistence-modules
- Blocks: nothing
- Readiness: agent

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `http-transport`:
  - ADDED *The Fiber v2 adapter serves the route table like the others*.
  - ADDED *Fiber v2 routes see the context consumer middleware sets*.

## Impact

- **Code:** new `transport/http/fiberv2` (binding, route-group wrappers, options, package doc).
- **Modules:** new `transport/http/fiberv2/go.mod` requiring `github.com/gofiber/fiber/v2 v2.52.15` and root at a pseudo-version of a pushed commit (no `replace`); `go.work` gains it; `scripts/modules.sh` `MIN_MODULES` rises by one.
- **Tests:** fiberv2 joins the parity table wherever `split-persistence-modules` places `transport/http/parity`; a fiberv2 `BodyLimit` divergence test; a guard test against `.Context()` calls; the module-dependency guard gains fiberv2 rows.
- **Public API:** additive only.
